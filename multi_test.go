package orchestra

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestNewMultiDB(t *testing.T) {
	m, err := NewMultiDB()
	if err != nil {
		t.Fatalf("NewMultiDB: %v", err)
	}
	defer m.Close()

	if m.db == nil {
		t.Fatal("expected non-nil db")
	}
	if len(m.aliases) != 0 {
		t.Fatalf("expected empty aliases, got %d", len(m.aliases))
	}
}

func TestAttachProject(t *testing.T) {
	// Create a temp SQLite database to attach.
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")
	db, err := sql.Open("sqlite3", "file:"+dbPath)
	if err != nil {
		t.Fatalf("creating test db: %v", err)
	}
	_, err = db.Exec(`CREATE TABLE IF NOT EXISTS test_table (id TEXT PRIMARY KEY)`)
	if err != nil {
		t.Fatalf("creating table: %v", err)
	}
	db.Close()

	m, err := NewMultiDB()
	if err != nil {
		t.Fatalf("NewMultiDB: %v", err)
	}
	defer m.Close()

	if err := m.AttachProject("proj1", dbPath); err != nil {
		t.Fatalf("AttachProject: %v", err)
	}

	if _, ok := m.aliases["proj1"]; !ok {
		t.Fatal("expected alias to be registered")
	}

	// Verify we can query the attached database.
	var count int
	err = m.db.QueryRow(`SELECT COUNT(*) FROM proj1.test_table`).Scan(&count)
	if err != nil {
		t.Fatalf("querying attached db: %v", err)
	}
}

func TestAttachProject_InvalidPath(t *testing.T) {
	m, err := NewMultiDB()
	if err != nil {
		t.Fatalf("NewMultiDB: %v", err)
	}
	defer m.Close()

	err = m.AttachProject("bad", "/nonexistent/path/db.sqlite")
	if err == nil {
		t.Fatal("expected error for nonexistent path")
	}
}

func TestGlobalStatusSummary_EmptyPaths(t *testing.T) {
	ctx := context.Background()
	status, err := GlobalStatusSummary(ctx, nil)
	if err != nil {
		t.Fatalf("GlobalStatusSummary: %v", err)
	}
	if len(status.Projects) != 0 {
		t.Fatalf("expected 0 projects, got %d", len(status.Projects))
	}
}

func TestGlobalStatusSummary_MissingDB(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()

	status, err := GlobalStatusSummary(ctx, []string{dir})
	if err != nil {
		t.Fatalf("GlobalStatusSummary: %v", err)
	}
	if len(status.Projects) != 1 {
		t.Fatalf("expected 1 project, got %d", len(status.Projects))
	}
	// Should have the name but zero counts (graceful degradation).
	p := status.Projects[0]
	if p.Name != filepath.Base(dir) {
		t.Errorf("expected name %s, got %s", filepath.Base(dir), p.Name)
	}
	if p.AgentCount != 0 || p.TasksDone != 0 || p.TasksPending != 0 {
		t.Error("expected zero counts for missing DB")
	}
}

func TestGlobalStatusSummary_WithData(t *testing.T) {
	dir := t.TempDir()
	projDir := filepath.Join(dir, "my-project")
	orchDir := filepath.Join(projDir, ".orchestra")
	if err := os.MkdirAll(orchDir, 0o755); err != nil {
		t.Fatal(err)
	}

	dbPath := filepath.Join(orchDir, "orchestrator.db")
	db, err := sql.Open("sqlite3", "file:"+dbPath)
	if err != nil {
		t.Fatal(err)
	}

	// Create minimal schema.
	for _, stmt := range []string{
		`CREATE TABLE IF NOT EXISTS conductors (id TEXT PRIMARY KEY, status TEXT, started_at DATETIME DEFAULT CURRENT_TIMESTAMP)`,
		`CREATE TABLE IF NOT EXISTS agents (id TEXT PRIMARY KEY, status TEXT, created_at DATETIME DEFAULT CURRENT_TIMESTAMP)`,
		`CREATE TABLE IF NOT EXISTS tasks (id TEXT PRIMARY KEY, status TEXT, created_at DATETIME DEFAULT CURRENT_TIMESTAMP)`,
		`CREATE TABLE IF NOT EXISTS events (id INTEGER PRIMARY KEY AUTOINCREMENT, timestamp DATETIME DEFAULT CURRENT_TIMESTAMP, event_type TEXT)`,
	} {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatalf("schema: %v", err)
		}
	}

	// Insert test data.
	now := time.Now().UTC().Format("2006-01-02 15:04:05")
	db.Exec(`INSERT INTO conductors (id, status) VALUES ('s-001', 'active')`)
	db.Exec(`INSERT INTO agents (id, status) VALUES ('a-001', 'running')`)
	db.Exec(`INSERT INTO agents (id, status) VALUES ('a-002', 'running')`)
	db.Exec(`INSERT INTO agents (id, status) VALUES ('a-003', 'idle')`)
	db.Exec(`INSERT INTO tasks (id, status) VALUES ('t-001', 'done')`)
	db.Exec(`INSERT INTO tasks (id, status) VALUES ('t-002', 'done')`)
	db.Exec(`INSERT INTO tasks (id, status) VALUES ('t-003', 'pending')`)
	db.Exec(`INSERT INTO events (event_type, timestamp) VALUES ('task_completed', ?)`, now)
	db.Close()

	ctx := context.Background()
	status, err := GlobalStatusSummary(ctx, []string{projDir})
	if err != nil {
		t.Fatalf("GlobalStatusSummary: %v", err)
	}

	if len(status.Projects) != 1 {
		t.Fatalf("expected 1 project, got %d", len(status.Projects))
	}

	p := status.Projects[0]
	if p.Name != "my-project" {
		t.Errorf("Name = %q, want %q", p.Name, "my-project")
	}
	if p.ActiveSession != "s-001" {
		t.Errorf("ActiveSession = %q, want %q", p.ActiveSession, "s-001")
	}
	if p.AgentCount != 2 {
		t.Errorf("AgentCount = %d, want 2", p.AgentCount)
	}
	if p.TasksDone != 2 {
		t.Errorf("TasksDone = %d, want 2", p.TasksDone)
	}
	if p.TasksPending != 1 {
		t.Errorf("TasksPending = %d, want 1", p.TasksPending)
	}
	if p.LastActivity.IsZero() {
		t.Error("LastActivity should not be zero")
	}
}

func TestGlobalStatusSummary_MultipleProjects(t *testing.T) {
	dir := t.TempDir()

	// Create two projects.
	for _, name := range []string{"proj-a", "proj-b"} {
		projDir := filepath.Join(dir, name)
		orchDir := filepath.Join(projDir, ".orchestra")
		if err := os.MkdirAll(orchDir, 0o755); err != nil {
			t.Fatal(err)
		}
		dbPath := filepath.Join(orchDir, "orchestrator.db")
		db, err := sql.Open("sqlite3", "file:"+dbPath)
		if err != nil {
			t.Fatal(err)
		}
		db.Exec(`CREATE TABLE IF NOT EXISTS conductors (id TEXT, status TEXT, started_at DATETIME DEFAULT CURRENT_TIMESTAMP)`)
		db.Exec(`CREATE TABLE IF NOT EXISTS agents (id TEXT, status TEXT, created_at DATETIME DEFAULT CURRENT_TIMESTAMP)`)
		db.Exec(`CREATE TABLE IF NOT EXISTS tasks (id TEXT, status TEXT, created_at DATETIME DEFAULT CURRENT_TIMESTAMP)`)
		db.Exec(`CREATE TABLE IF NOT EXISTS events (id INTEGER PRIMARY KEY AUTOINCREMENT, timestamp DATETIME DEFAULT CURRENT_TIMESTAMP, event_type TEXT)`)
		db.Exec(`INSERT INTO tasks (id, status) VALUES ('t-1', 'done')`)
		db.Close()
	}

	ctx := context.Background()
	paths := []string{filepath.Join(dir, "proj-a"), filepath.Join(dir, "proj-b")}
	status, err := GlobalStatusSummary(ctx, paths)
	if err != nil {
		t.Fatalf("GlobalStatusSummary: %v", err)
	}
	if len(status.Projects) != 2 {
		t.Fatalf("expected 2 projects, got %d", len(status.Projects))
	}
	for _, p := range status.Projects {
		if p.TasksDone != 1 {
			t.Errorf("%s: TasksDone = %d, want 1", p.Name, p.TasksDone)
		}
	}
}
