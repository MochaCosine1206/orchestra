package orchestra

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"time"

	_ "github.com/ncruces/go-sqlite3/driver"
	_ "github.com/ncruces/go-sqlite3/embed"
)

// MultiDB aggregates multiple project databases using SQLite ATTACH DATABASE.
type MultiDB struct {
	db      *sql.DB
	aliases map[string]string // alias → dbPath
}

// ProjectSummary holds aggregated status for a single project.
type ProjectSummary struct {
	Name          string    `json:"name"`
	Path          string    `json:"path"`
	ActiveSession string    `json:"active_session"`
	AgentCount    int       `json:"agent_count"`
	TasksDone     int       `json:"tasks_done"`
	TasksPending  int       `json:"tasks_pending"`
	LastActivity  time.Time `json:"last_activity"`
}

// GlobalStatus holds status summaries across all projects.
type GlobalStatus struct {
	Projects []ProjectSummary `json:"projects"`
}

// NewMultiDB creates a MultiDB backed by an in-memory database.
func NewMultiDB() (*MultiDB, error) {
	dsn := "file::memory:?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=foreign_keys(ON)"
	db, err := sql.Open("sqlite3", dsn)
	if err != nil {
		return nil, fmt.Errorf("opening multi-db: %w", err)
	}
	db.SetMaxOpenConns(1)
	return &MultiDB{db: db, aliases: make(map[string]string)}, nil
}

// AttachProject attaches a project database under the given alias.
func (m *MultiDB) AttachProject(alias, dbPath string) error {
	absPath, err := filepath.Abs(dbPath)
	if err != nil {
		return fmt.Errorf("resolving path %s: %w", dbPath, err)
	}
	_, err = m.db.Exec(fmt.Sprintf(`ATTACH DATABASE '%s' AS "%s"`, absPath, alias))
	if err != nil {
		return fmt.Errorf("attaching database %s as %s: %w", dbPath, alias, err)
	}
	m.aliases[alias] = absPath
	return nil
}

// Close closes the underlying database connection.
func (m *MultiDB) Close() error {
	return m.db.Close()
}

// GlobalStatusSummary queries all provided project paths and returns a GlobalStatus
// with per-project summaries. Each project path should point to a directory containing
// .orchestra/orchestrator.db.
func GlobalStatusSummary(ctx context.Context, projectPaths []string) (*GlobalStatus, error) {
	status := &GlobalStatus{}

	for _, projPath := range projectPaths {
		dbPath := filepath.Join(projPath, ".orchestra", "orchestrator.db")
		summary, err := queryProjectSummary(ctx, projPath, dbPath)
		if err != nil {
			// Include the project with an error indicator rather than failing entirely.
			status.Projects = append(status.Projects, ProjectSummary{
				Name: filepath.Base(projPath),
				Path: projPath,
			})
			continue
		}
		status.Projects = append(status.Projects, *summary)
	}

	return status, nil
}

// queryProjectSummary opens a single project DB read-only and extracts status counts.
func queryProjectSummary(ctx context.Context, projPath, dbPath string) (*ProjectSummary, error) {
	dsn := fmt.Sprintf("file:%s?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=foreign_keys(ON)", dbPath)
	db, err := sql.Open("sqlite3", dsn)
	if err != nil {
		return nil, fmt.Errorf("opening %s: %w", dbPath, err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)

	if err := db.PingContext(ctx); err != nil {
		return nil, fmt.Errorf("pinging %s: %w", dbPath, err)
	}

	summary := &ProjectSummary{
		Name: filepath.Base(projPath),
		Path: projPath,
	}

	// Active session (most recent active conductor)
	var activeSession sql.NullString
	_ = db.QueryRowContext(ctx,
		`SELECT id FROM conductors WHERE status = 'active' ORDER BY started_at DESC LIMIT 1`,
	).Scan(&activeSession)
	if activeSession.Valid {
		summary.ActiveSession = activeSession.String
	}

	// Agent count (running agents)
	_ = db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM agents WHERE status = 'running'`,
	).Scan(&summary.AgentCount)

	// Task counts
	_ = db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM tasks WHERE status = 'done'`,
	).Scan(&summary.TasksDone)
	_ = db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM tasks WHERE status = 'pending'`,
	).Scan(&summary.TasksPending)

	// Last activity (most recent event timestamp)
	var lastActivity sql.NullString
	_ = db.QueryRowContext(ctx,
		`SELECT MAX(timestamp) FROM events`,
	).Scan(&lastActivity)
	if lastActivity.Valid && lastActivity.String != "" {
		if t, err := time.Parse("2006-01-02 15:04:05", lastActivity.String); err == nil {
			summary.LastActivity = t
		}
	}

	return summary, nil
}
