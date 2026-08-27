package cmd

import (
	"bytes"
	"context"
	"path/filepath"
	"strings"
	"testing"

	orchestra "github.com/MochaCosine1206/orchestra"
	"github.com/MochaCosine1206/orchestra/internal/db"
)

// setupDaemonTestDB creates a temp config dir with an initialized daemon.db
// and returns the dir path and open DB handle.
func setupDaemonTestDB(t *testing.T) (string, *db.DB) {
	t.Helper()
	tmpDir := t.TempDir()
	t.Setenv("ORCHESTRA_CONFIG_DIR", tmpDir)

	dbPath := filepath.Join(tmpDir, "daemon.db")
	d, err := db.Open(dbPath)
	if err != nil {
		t.Fatalf("opening daemon db: %v", err)
	}
	if err := orchestra.InitDaemonSchema(context.Background(), d); err != nil {
		t.Fatalf("init daemon schema: %v", err)
	}
	t.Cleanup(func() { d.Close() })
	return tmpDir, d
}

func TestDaemonSubcommandTree(t *testing.T) {
	cmd := NewDaemonCmd()

	want := map[string]bool{
		"run":       false,
		"start":     false,
		"stop":      false,
		"status":    false,
		"install":   false,
		"uninstall": false,
		"logs":      false,
	}

	for _, sub := range cmd.Commands() {
		if _, ok := want[sub.Name()]; ok {
			want[sub.Name()] = true
		}
	}

	for name, found := range want {
		if !found {
			t.Errorf("expected subcommand %q not registered on daemon command", name)
		}
	}
}

func TestDaemonSingletonLock(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("ORCHESTRA_CONFIG_DIR", tmpDir)

	// First lock should succeed.
	fd, err := orchestra.AcquireLock()
	if err != nil {
		t.Fatalf("first AcquireLock failed: %v", err)
	}
	defer orchestra.ReleaseLock(fd)

	// Second lock should fail with "already running".
	_, err = orchestra.AcquireLock()
	if err == nil {
		t.Fatal("second AcquireLock should have failed")
	}
	if !strings.Contains(err.Error(), "already running") {
		t.Errorf("expected error to mention 'already running', got: %v", err)
	}
}

func TestDaemonStatusNoDB(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("ORCHESTRA_CONFIG_DIR", tmpDir)

	root := NewRootCmd()
	buf := new(bytes.Buffer)
	root.SetOut(buf)
	root.SetErr(buf)
	root.SetArgs([]string{"daemon", "status"})

	if err := root.Execute(); err != nil {
		t.Fatalf("daemon status should not error with missing db, got: %v", err)
	}

	out := buf.String()
	if !strings.Contains(out, "not been initialized") {
		t.Errorf("expected 'not been initialized' message, got: %s", out)
	}
}

func TestDaemonStatusWithDBState(t *testing.T) {
	_, d := setupDaemonTestDB(t)
	ctx := context.Background()

	_, err := d.ExecContext(ctx,
		"INSERT INTO daemon_state(key, value, updated_at) VALUES('status', 'running', '2026-03-25 20:00:00')")
	if err != nil {
		t.Fatalf("inserting status: %v", err)
	}
	_, err = d.ExecContext(ctx,
		"INSERT INTO daemon_state(key, value, updated_at) VALUES('pid', '12345', '2026-03-25 20:00:00')")
	if err != nil {
		t.Fatalf("inserting pid: %v", err)
	}
	d.Close()

	root := NewRootCmd()
	buf := new(bytes.Buffer)
	root.SetOut(buf)
	root.SetErr(buf)
	root.SetArgs([]string{"daemon", "status"})

	if err := root.Execute(); err != nil {
		t.Fatalf("daemon status failed: %v", err)
	}

	out := buf.String()
	if !strings.Contains(out, "pid") {
		t.Errorf("output should contain 'pid', got: %s", out)
	}
	if !strings.Contains(out, "12345") {
		t.Errorf("output should contain PID value '12345', got: %s", out)
	}
	if !strings.Contains(out, "running") {
		t.Errorf("output should contain 'running', got: %s", out)
	}
}

func TestDaemonStatusWithRunHistory(t *testing.T) {
	_, d := setupDaemonTestDB(t)
	ctx := context.Background()

	_, err := d.ExecContext(ctx,
		`INSERT INTO run_history(id, project_path, session_id, started_at, status, tasks_done, tasks_failed)
		 VALUES('r1', '/tmp/my-project', 's1', '2026-03-25 19:00:00', 'completed', 3, 0)`)
	if err != nil {
		t.Fatalf("inserting run history: %v", err)
	}
	d.Close()

	root := NewRootCmd()
	buf := new(bytes.Buffer)
	root.SetOut(buf)
	root.SetErr(buf)
	root.SetArgs([]string{"daemon", "status"})

	if err := root.Execute(); err != nil {
		t.Fatalf("daemon status failed: %v", err)
	}

	out := buf.String()
	if !strings.Contains(out, "Recent Runs") {
		t.Errorf("output should contain 'Recent Runs', got: %s", out)
	}
	if !strings.Contains(out, "my-project") {
		t.Errorf("output should contain project name 'my-project', got: %s", out)
	}
	if !strings.Contains(out, "completed") {
		t.Errorf("output should contain status 'completed', got: %s", out)
	}
}

func TestDaemonInstallError(t *testing.T) {
	root := NewRootCmd()
	buf := new(bytes.Buffer)
	root.SetOut(buf)
	root.SetErr(buf)
	root.SetArgs([]string{"daemon", "install"})

	err := root.Execute()
	if err == nil {
		// In some environments kardianos/service may succeed; that's OK.
		return
	}
	if !strings.Contains(err.Error(), "installing service") {
		t.Errorf("expected error wrapping 'installing service', got: %v", err)
	}
}

func TestDaemonUninstallError(t *testing.T) {
	root := NewRootCmd()
	buf := new(bytes.Buffer)
	root.SetOut(buf)
	root.SetErr(buf)
	root.SetArgs([]string{"daemon", "uninstall"})

	err := root.Execute()
	if err == nil {
		// In some environments kardianos/service may succeed; that's OK.
		return
	}
	if !strings.Contains(err.Error(), "uninstalling service") {
		t.Errorf("expected error wrapping 'uninstalling service', got: %v", err)
	}
}
