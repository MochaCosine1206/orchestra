package cmd

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/MochaCosine1206/orchestra/internal/db"
)

func newInitTestDir(t *testing.T) string {
	t.Helper()
	return t.TempDir()
}

func executeInit(t *testing.T, dir string, args ...string) error {
	t.Helper()
	root := NewRootCmd()
	dbPath := filepath.Join(dir, "orchestrator.db")
	root.SetArgs(append([]string{"init", "--db", dbPath}, args...))
	return root.Execute()
}

func TestInitCreatesDB(t *testing.T) {
	dir := newInitTestDir(t)
	if err := executeInit(t, dir); err != nil {
		t.Fatalf("init: %v", err)
	}

	dbPath := filepath.Join(dir, "orchestrator.db")
	if _, err := os.Stat(dbPath); os.IsNotExist(err) {
		t.Fatal("database file not created")
	}
}

func TestInitCreatesSchema(t *testing.T) {
	dir := newInitTestDir(t)
	if err := executeInit(t, dir); err != nil {
		t.Fatalf("init: %v", err)
	}

	dbPath := filepath.Join(dir, "orchestrator.db")
	d, err := db.Open(dbPath)
	if err != nil {
		t.Fatalf("opening db: %v", err)
	}
	defer d.Close()

	tables := []string{"agents", "tasks", "file_locks", "events", "blackboard", "ideas", "loops", "loop_steps"}
	for _, table := range tables {
		t.Run(table, func(t *testing.T) {
			var name string
			err := d.QueryRowContext(context.Background(),
				`SELECT name FROM sqlite_master WHERE type='table' AND name=?`, table).Scan(&name)
			if err != nil {
				t.Errorf("table %s not found: %v", table, err)
			}
		})
	}
}

func TestInitCreatesDirs(t *testing.T) {
	dir := newInitTestDir(t)
	if err := executeInit(t, dir); err != nil {
		t.Fatalf("init: %v", err)
	}

	for _, sub := range []string{"logs", "pids", "specs"} {
		path := filepath.Join(dir, ".orchestra", sub)
		info, err := os.Stat(path)
		if os.IsNotExist(err) {
			t.Errorf("directory %s not created", sub)
		} else if !info.IsDir() {
			t.Errorf("%s is not a directory", sub)
		}
	}
}

func TestInitIdempotent(t *testing.T) {
	dir := newInitTestDir(t)

	if err := executeInit(t, dir); err != nil {
		t.Fatalf("first init: %v", err)
	}
	if err := executeInit(t, dir); err != nil {
		t.Fatalf("second init should not error: %v", err)
	}
}

func TestInitClean(t *testing.T) {
	dir := newInitTestDir(t)

	// First init + insert data.
	if err := executeInit(t, dir); err != nil {
		t.Fatalf("init: %v", err)
	}

	dbPath := filepath.Join(dir, "orchestrator.db")
	d, err := db.Open(dbPath)
	if err != nil {
		t.Fatalf("opening: %v", err)
	}
	_, err = d.ExecContext(context.Background(),
		`INSERT INTO tasks (id, title, status, role) VALUES ('t1', 'Test', 'pending', 'implementer')`)
	if err != nil {
		t.Fatalf("inserting: %v", err)
	}
	d.Close()

	// Clean init should succeed and wipe data.
	if err := executeInit(t, dir, "--clean"); err != nil {
		t.Fatalf("clean init: %v", err)
	}

	d2, err := db.Open(dbPath)
	if err != nil {
		t.Fatalf("reopening: %v", err)
	}
	defer d2.Close()

	var count int
	if err := d2.QueryRowContext(context.Background(), `SELECT COUNT(*) FROM tasks`).Scan(&count); err != nil {
		t.Fatalf("counting: %v", err)
	}
	if count != 0 {
		t.Errorf("expected 0 tasks after clean init, got %d", count)
	}
}

func TestInitCleanPreservesSchema(t *testing.T) {
	dir := newInitTestDir(t)

	if err := executeInit(t, dir); err != nil {
		t.Fatalf("init: %v", err)
	}
	if err := executeInit(t, dir, "--clean"); err != nil {
		t.Fatalf("clean init: %v", err)
	}

	dbPath := filepath.Join(dir, "orchestrator.db")
	d, err := db.Open(dbPath)
	if err != nil {
		t.Fatalf("opening: %v", err)
	}
	defer d.Close()

	tables := []string{"agents", "tasks", "file_locks", "events", "blackboard", "ideas", "loops", "loop_steps"}
	for _, table := range tables {
		var name string
		err := d.QueryRowContext(context.Background(),
			`SELECT name FROM sqlite_master WHERE type='table' AND name=?`, table).Scan(&name)
		if err != nil {
			t.Errorf("table %s missing after clean init: %v", table, err)
		}
	}
}
