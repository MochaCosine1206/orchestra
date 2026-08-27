package daemon

import (
	"context"
	"fmt"
	"os/exec"
	"path/filepath"
	"strconv"
	"sync"

	"github.com/MochaCosine1206/orchestra/internal/agent"
	"github.com/MochaCosine1206/orchestra/internal/db"
)

const maxConsecutiveFailures = 3

// Watchdog monitors conductor health across registered projects and restarts dead ones.
type Watchdog struct {
	mu       sync.Mutex
	failures map[string]int // project path -> consecutive failure count
	log      func(string)
}

// NewWatchdog creates a Watchdog with the given logger.
func NewWatchdog(log func(string)) *Watchdog {
	return &Watchdog{
		failures: make(map[string]int),
		log:      log,
	}
}

// ProjectStatus holds health check results for a single project.
type ProjectStatus struct {
	Path    string
	Alive   bool
	PID     int
	Tripped bool // circuit breaker tripped
}

// Check iterates registered projects, reads conductor PIDs, and restarts dead ones.
// Returns status for each project checked.
func (w *Watchdog) Check(projects []ProjectInfo) []ProjectStatus {
	var results []ProjectStatus
	for _, p := range projects {
		status := w.checkProject(p)
		results = append(results, status)
	}
	return results
}

// ProjectInfo holds the info needed to check a project's conductor health.
type ProjectInfo struct {
	Path    string
	PidsDir string // for agent PID files (.orchestra/pids/)
	DBPath  string // path to project's orchestrator.db
}

func (w *Watchdog) checkProject(p ProjectInfo) ProjectStatus {
	// Primary: read conductor PID from project's SQLite blackboard.
	// The conductor writes conductor:pid on activation and clears it on deactivation.
	// This is the single source of truth — no stale PID file issues.
	pid := w.readConductorPIDFromDB(p)

	// Fallback: check PID files (for backward compat or if DB is unavailable)
	if pid == 0 {
		pidsDir := p.PidsDir
		if pidsDir == "" {
			pidsDir = filepath.Join(p.Path, ".orchestra", "pids")
		}
		var err error
		pid, err = agent.ReadPID(pidsDir, "conductor")
		if err != nil || pid == 0 {
			return ProjectStatus{Path: p.Path, Alive: false, PID: 0}
		}
	}

	if agent.PidAlive(pid) {
		w.resetFailures(p.Path)
		return ProjectStatus{Path: p.Path, Alive: true, PID: pid}
	}

	// Conductor is dead — attempt restart
	w.mu.Lock()
	w.failures[p.Path]++
	count := w.failures[p.Path]
	w.mu.Unlock()

	if count > maxConsecutiveFailures {
		w.logf("watchdog: circuit breaker tripped for %s (%d consecutive failures)", p.Path, count)
		return ProjectStatus{Path: p.Path, Alive: false, PID: pid, Tripped: true}
	}

	w.logf("watchdog: conductor dead at %s (PID %d), attempting restart (%d/%d)", p.Path, pid, count, maxConsecutiveFailures)
	if err := w.restart(p.Path); err != nil {
		w.logf("watchdog: restart failed for %s: %v", p.Path, err)
	}

	return ProjectStatus{Path: p.Path, Alive: false, PID: pid}
}

// restart tries 'orchestra recover' first (handles orphan sessions),
// then falls back to 'orchestra go --foreground' for a fresh start.
func (w *Watchdog) restart(projectPath string) error {
	orchBin := filepath.Join(projectPath, "bin", "orchestra")
	if _, err := exec.LookPath(orchBin); err != nil {
		orchBin = "orchestra"
	}

	cmd := exec.Command(orchBin, "recover")
	cmd.Dir = projectPath
	if out, err := cmd.CombinedOutput(); err == nil {
		w.logf("watchdog: recovered session at %s: %s", projectPath, string(out))
		return nil
	}

	cmd = exec.Command(orchBin, "go", "--foreground")
	cmd.Dir = projectPath
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("restart failed: %w", err)
	}
	w.logf("watchdog: restarted conductor at %s (PID %d)", projectPath, cmd.Process.Pid)
	return nil
}

// readConductorPIDFromDB opens the project's orchestrator.db read-only and
// reads the conductor:pid from the blackboard. Returns 0 if unavailable.
// This is the primary PID detection method — the blackboard is the single
// source of truth, written by helpers.go:activateConductor and cleared by
// deactivateConductor. Supports multiple conductors (B-030) when keys are
// namespaced per conductor.
func (w *Watchdog) readConductorPIDFromDB(p ProjectInfo) int {
	dbPath := p.DBPath
	if dbPath == "" {
		dbPath = filepath.Join(p.Path, ".orchestra", "orchestrator.db")
	}

	d, err := db.OpenReadOnly(dbPath)
	if err != nil {
		return 0 // DB doesn't exist or is locked — graceful fallback
	}
	defer d.Close()

	var pidStr string
	err = d.QueryRowContext(context.Background(),
		`SELECT value FROM blackboard WHERE key = 'conductor:pid'`,
	).Scan(&pidStr)
	if err != nil {
		return 0
	}

	pid, err := strconv.Atoi(pidStr)
	if err != nil {
		return 0
	}
	return pid
}

func (w *Watchdog) resetFailures(path string) {
	w.mu.Lock()
	delete(w.failures, path)
	w.mu.Unlock()
}

// ResetCircuitBreaker manually resets the failure counter for a project.
func (w *Watchdog) ResetCircuitBreaker(path string) {
	w.resetFailures(path)
}

// ConsecutiveFailures returns the current failure count for a project.
func (w *Watchdog) ConsecutiveFailures(path string) int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.failures[path]
}

func (w *Watchdog) logf(format string, args ...interface{}) {
	if w.log != nil {
		w.log(fmt.Sprintf(format, args...))
	}
}
