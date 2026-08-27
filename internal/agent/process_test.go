package agent

import (
	"context"
	"database/sql"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/MochaCosine1206/orchestra/internal/db"
)

func setupTestDB(t *testing.T) *db.DB {
	t.Helper()
	d, err := db.OpenMemory()
	if err != nil {
		t.Fatalf("opening memory db: %v", err)
	}
	if err := d.InitSchema(context.Background()); err != nil {
		t.Fatalf("initializing schema: %v", err)
	}
	t.Cleanup(func() { d.Close() })
	return d
}

func TestPidAlive(t *testing.T) {
	// Our own PID should be alive.
	if !PidAlive(os.Getpid()) {
		t.Error("expected own PID to be alive")
	}

	// PID 0 should be invalid.
	if PidAlive(0) {
		t.Error("expected PID 0 to be not alive")
	}

	// Large PID unlikely to exist.
	if PidAlive(999999999) {
		t.Error("expected very large PID to be not alive")
	}
}

func TestWriteAndReadPID(t *testing.T) {
	dir := t.TempDir()

	if err := WritePID(dir, "task-1", 12345); err != nil {
		t.Fatalf("WritePID: %v", err)
	}

	pid, err := ReadPID(dir, "task-1")
	if err != nil {
		t.Fatalf("ReadPID: %v", err)
	}
	if pid != 12345 {
		t.Errorf("expected PID 12345, got %d", pid)
	}
}

func TestReadPIDMissing(t *testing.T) {
	dir := t.TempDir()
	pid, err := ReadPID(dir, "nonexistent")
	if err != nil {
		t.Fatalf("ReadPID: %v", err)
	}
	if pid != 0 {
		t.Errorf("expected 0 for missing PID file, got %d", pid)
	}
}

func TestReadPIDInvalid(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "bad.pid"), []byte("not-a-number"), 0o644)

	_, err := ReadPID(dir, "bad")
	if err == nil {
		t.Error("expected error for invalid PID content")
	}
}

func TestRemovePID(t *testing.T) {
	dir := t.TempDir()
	WritePID(dir, "task-1", 12345)
	RemovePID(dir, "task-1")

	pid, _ := ReadPID(dir, "task-1")
	if pid != 0 {
		t.Errorf("expected 0 after remove, got %d", pid)
	}
}

func TestKillProcessInvalid(t *testing.T) {
	err := KillProcess(0, false, time.Second)
	if err == nil {
		t.Error("expected error for PID 0")
	}
}

func TestKillProcessGraceful(t *testing.T) {
	// Start a sleep process we can kill.
	cmd := exec.Command("sleep", "60")
	if err := cmd.Start(); err != nil {
		t.Fatalf("starting sleep: %v", err)
	}
	pid := cmd.Process.Pid

	if err := KillProcess(pid, false, 2*time.Second); err != nil {
		t.Fatalf("KillProcess: %v", err)
	}

	// Wait for process to fully exit (reaping may take a moment).
	cmd.Wait()
	if PidAlive(pid) {
		t.Error("expected process to be dead after kill")
	}
}

func TestKillProcessForce(t *testing.T) {
	cmd := exec.Command("sleep", "60")
	if err := cmd.Start(); err != nil {
		t.Fatalf("starting sleep: %v", err)
	}

	pid := cmd.Process.Pid

	if err := KillProcess(pid, true, time.Second); err != nil {
		t.Fatalf("KillProcess force: %v", err)
	}

	cmd.Wait()
	if PidAlive(pid) {
		t.Error("expected process to be dead after force kill")
	}
}

func TestKillTaskProcess(t *testing.T) {
	d := setupTestDB(t)
	ctx := context.Background()
	pidsDir := t.TempDir()

	// Start a real process.
	cmd := exec.Command("sleep", "60")
	if err := cmd.Start(); err != nil {
		t.Fatalf("starting sleep: %v", err)
	}
	pid := cmd.Process.Pid

	// Register agent and task.
	err := d.RegisterAgent(ctx, db.Agent{
		ID:     "a1",
		Role:   "implementer",
		Status: "active",
		PID:    sql.NullInt64{Int64: int64(pid), Valid: true},
	})
	if err != nil {
		t.Fatalf("registering agent: %v", err)
	}

	err = d.CreateTask(ctx, db.Task{
		ID:     "t1",
		Title:  "Test task",
		Status: "running",
		Role:   "implementer",
	})
	if err != nil {
		t.Fatalf("creating task: %v", err)
	}
	d.AssignTask(ctx, "t1", "a1", ".worktree/t1", "feature/t1")

	// Write PID file too.
	WritePID(pidsDir, "t1", pid)

	if err := KillTaskProcess(ctx, d, "t1", pidsDir, false); err != nil {
		t.Fatalf("KillTaskProcess: %v", err)
	}

	// Verify agent is dead.
	agents, _ := d.ListAgents(ctx)
	if len(agents) != 1 || agents[0].Status != "dead" {
		t.Errorf("expected dead agent, got %v", agents)
	}

	// Verify task is failed.
	tasks, _ := d.ListTasks(ctx)
	if len(tasks) != 1 || tasks[0].Status != "failed" {
		t.Errorf("expected failed task, got %v", tasks)
	}

	// Verify PID file removed.
	p, _ := ReadPID(pidsDir, "t1")
	if p != 0 {
		t.Errorf("expected PID file removed, got %d", p)
	}
}

func TestKillTaskProcessViaPIDFile(t *testing.T) {
	d := setupTestDB(t)
	ctx := context.Background()
	pidsDir := t.TempDir()

	// Task with no assigned agent but has PID file.
	cmd := exec.Command("sleep", "60")
	if err := cmd.Start(); err != nil {
		t.Fatalf("starting sleep: %v", err)
	}
	pid := cmd.Process.Pid

	err := d.CreateTask(ctx, db.Task{
		ID:     "t1",
		Title:  "Test task",
		Status: "running",
		Role:   "implementer",
	})
	if err != nil {
		t.Fatalf("creating task: %v", err)
	}

	WritePID(pidsDir, "t1", pid)

	if err := KillTaskProcess(ctx, d, "t1", pidsDir, false); err != nil {
		t.Fatalf("KillTaskProcess: %v", err)
	}

	// Wait for process to fully exit.
	cmd.Wait()
	if PidAlive(pid) {
		t.Error("expected process to be dead")
	}
}

// Helper — needed by other test files but defined here for the package.
func pidFileContains(dir, taskID string, expected int) bool {
	data, err := os.ReadFile(filepath.Join(dir, taskID+".pid"))
	if err != nil {
		return false
	}
	pid, err := strconv.Atoi(string(data))
	return err == nil && pid == expected
}
