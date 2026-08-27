package isolation

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	_ "github.com/ncruces/go-sqlite3/driver"
	_ "github.com/ncruces/go-sqlite3/embed"
)

var configDir = filepath.Join(homeDir(), ".config", "orchestra")

func homeDir() string {
	h, err := os.UserHomeDir()
	if err != nil {
		return os.TempDir()
	}
	return h
}

// ProjectLock provides per-project flock-based mutual exclusion.
// Only one orchestra conductor can run per project at a time.
type ProjectLock struct {
	slug    string
	pidPath string
	file    *os.File
}

// AcquireLock acquires an exclusive flock for the given project slug.
// It writes the current PID to ~/.config/orchestra/pids/<slug>.pid.
// Returns an error if the lock is already held by another process.
func AcquireLock(slug string) (*ProjectLock, error) {
	pidsDir := filepath.Join(configDir, "pids")
	if err := os.MkdirAll(pidsDir, 0o755); err != nil {
		return nil, fmt.Errorf("creating pids dir: %w", err)
	}

	pidPath := filepath.Join(pidsDir, slug+".pid")

	f, err := os.OpenFile(pidPath, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, fmt.Errorf("opening pid file: %w", err)
	}

	err = syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
	if err != nil {
		f.Close()
		return nil, fmt.Errorf("project %q is already locked (see IsLocked for details)", slug)
	}

	// Write our PID.
	pid := strconv.Itoa(os.Getpid())
	if err := f.Truncate(0); err != nil {
		syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		f.Close()
		return nil, fmt.Errorf("truncating pid file: %w", err)
	}
	if _, err := f.WriteAt([]byte(pid), 0); err != nil {
		syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		f.Close()
		return nil, fmt.Errorf("writing pid: %w", err)
	}

	return &ProjectLock{
		slug:    slug,
		pidPath: pidPath,
		file:    f,
	}, nil
}

// ReleaseLock releases the flock and removes the PID file.
func (l *ProjectLock) ReleaseLock() {
	if l.file == nil {
		return
	}
	syscall.Flock(int(l.file.Fd()), syscall.LOCK_UN)
	l.file.Close()
	os.Remove(l.pidPath)
	l.file = nil
}

// IsLocked checks whether the given project slug is currently locked.
// Returns locked status, the PID of the holding process, and any error.
func IsLocked(slug string) (bool, int, error) {
	pidPath := filepath.Join(configDir, "pids", slug+".pid")

	f, err := os.OpenFile(pidPath, os.O_RDONLY, 0)
	if err != nil {
		if os.IsNotExist(err) {
			return false, 0, nil
		}
		return false, 0, fmt.Errorf("opening pid file: %w", err)
	}
	defer f.Close()

	// Try to acquire the lock non-blocking. If we get it, it's not locked.
	err = syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
	if err == nil {
		// We got the lock — nobody holds it. Release immediately.
		syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		return false, 0, nil
	}

	// Lock is held. Read the PID from the file.
	buf := make([]byte, 32)
	n, _ := f.Read(buf)
	pidStr := strings.TrimSpace(string(buf[:n]))
	pid, _ := strconv.Atoi(pidStr)

	return true, pid, nil
}

// SharedRateLimiter coordinates rate limit usage across concurrent orchestra
// instances using a shared SQLite database at ~/.config/orchestra/rate-limits.db.
type SharedRateLimiter struct {
	db          *sql.DB
	globalLimit int
	windowSecs  int
}

const rateLimitSchema = `
CREATE TABLE IF NOT EXISTS rate_usage (
	project      TEXT PRIMARY KEY,
	messages_used INTEGER NOT NULL DEFAULT 0,
	window_start DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);
`

// NewSharedRateLimiter opens (or creates) the shared rate-limits database.
// globalLimit is the total messages allowed per window across all projects.
// windowSecs is the rolling window duration in seconds.
func NewSharedRateLimiter(globalLimit, windowSecs int) (*SharedRateLimiter, error) {
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		return nil, fmt.Errorf("creating config dir: %w", err)
	}

	dbPath := filepath.Join(configDir, "rate-limits.db")
	dsn := fmt.Sprintf("file:%s?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=foreign_keys(ON)&_pragma=synchronous(FULL)&_pragma=fullfsync(ON)", dbPath)

	sqlDB, err := sql.Open("sqlite3", dsn)
	if err != nil {
		return nil, fmt.Errorf("opening rate-limits db: %w", err)
	}
	sqlDB.SetMaxOpenConns(1)

	if _, err := sqlDB.Exec(rateLimitSchema); err != nil {
		sqlDB.Close()
		return nil, fmt.Errorf("initializing rate-limits schema: %w", err)
	}

	return &SharedRateLimiter{
		db:          sqlDB,
		globalLimit: globalLimit,
		windowSecs:  windowSecs,
	}, nil
}

// TryConsume attempts to consume one message for the given project.
// Returns false if the global rate limit at 80% threshold would be exceeded.
func (r *SharedRateLimiter) TryConsume(project string) bool {
	threshold := int(float64(r.globalLimit) * 0.8)
	now := time.Now().UTC()
	windowStart := now.Add(-time.Duration(r.windowSecs) * time.Second)

	tx, err := r.db.Begin()
	if err != nil {
		return false
	}
	defer tx.Rollback()

	// Reset stale windows.
	_, _ = tx.Exec(
		"UPDATE rate_usage SET messages_used = 0, window_start = datetime('now') WHERE window_start < datetime(?)",
		windowStart.Format("2006-01-02 15:04:05"),
	)

	// Sum current usage across all projects in the active window.
	var total int
	err = tx.QueryRow(
		"SELECT COALESCE(SUM(messages_used), 0) FROM rate_usage WHERE window_start >= datetime(?)",
		windowStart.Format("2006-01-02 15:04:05"),
	).Scan(&total)
	if err != nil {
		return false
	}

	if total >= threshold {
		return false
	}

	// Upsert this project's usage.
	_, err = tx.Exec(`
		INSERT INTO rate_usage (project, messages_used, window_start)
		VALUES (?, 1, datetime('now'))
		ON CONFLICT(project) DO UPDATE SET messages_used = messages_used + 1
	`, project)
	if err != nil {
		return false
	}

	return tx.Commit() == nil
}

// Close closes the underlying database connection.
func (r *SharedRateLimiter) Close() error {
	if r.db != nil {
		return r.db.Close()
	}
	return nil
}
