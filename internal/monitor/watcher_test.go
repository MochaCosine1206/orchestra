package monitor

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/MochaCosine1206/orchestra/internal/db"
)

func setupWatcherDB(t *testing.T) *db.DB {
	t.Helper()
	d, err := db.OpenMemory()
	if err != nil {
		t.Fatalf("failed to open in-memory DB: %v", err)
	}
	ctx := context.Background()
	if err := d.InitSchema(ctx); err != nil {
		t.Fatalf("failed to init schema: %v", err)
	}
	t.Cleanup(func() { d.Close() })
	return d
}

func TestNewFileWatcher(t *testing.T) {
	d := setupWatcherDB(t)
	fw := NewFileWatcher(d, nil)
	if fw == nil {
		t.Fatal("expected non-nil FileWatcher")
	}
	if fw.WatchedTaskCount() != 0 {
		t.Errorf("expected 0 watched tasks, got %d", fw.WatchedTaskCount())
	}
}

func TestFileWatcher_StartStop(t *testing.T) {
	d := setupWatcherDB(t)
	var logs []string
	fw := NewFileWatcher(d, func(s string) { logs = append(logs, s) })

	ctx := context.Background()
	if err := fw.Start(ctx); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	fw.Stop()

	if len(logs) < 2 {
		t.Errorf("expected at least 2 log messages (start+stop), got %d", len(logs))
	}
}

func TestFileWatcher_AddRemoveWorktree(t *testing.T) {
	d := setupWatcherDB(t)
	ctx := context.Background()

	fw := NewFileWatcher(d, func(s string) { t.Log(s) })
	if err := fw.Start(ctx); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer fw.Stop()

	// Create a temp worktree dir
	wtDir := t.TempDir()

	// Add worktree
	d.CreateFileLock(ctx, "main.go", "", "T-001", time.Time{})
	fw.AddWorktree(ctx, "T-001", wtDir)
	if fw.WatchedTaskCount() != 1 {
		t.Errorf("expected 1 watched task, got %d", fw.WatchedTaskCount())
	}

	// Remove worktree
	fw.RemoveWorktree("T-001")
	if fw.WatchedTaskCount() != 0 {
		t.Errorf("expected 0 watched tasks after remove, got %d", fw.WatchedTaskCount())
	}
}

func TestFileWatcher_DetectsViolation(t *testing.T) {
	d := setupWatcherDB(t)
	ctx := context.Background()

	fw := NewFileWatcher(d, func(s string) { t.Log(s) })
	if err := fw.Start(ctx); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer fw.Stop()

	// Create worktree dir with a subdirectory
	wtDir := t.TempDir()

	// Task owns "main.go" but not "other.go"
	d.CreateTask(ctx, db.Task{ID: "T-001", Title: "Test", Status: "running", Role: "implementer"})
	d.CreateFileLock(ctx, "main.go", "", "T-001", time.Time{})
	fw.AddWorktree(ctx, "T-001", wtDir)

	// Write to an unowned file — should trigger violation
	unownedFile := filepath.Join(wtDir, "other.go")
	os.WriteFile(unownedFile, []byte("package main\n"), 0o644)

	// Wait for fsnotify to pick up the event
	time.Sleep(800 * time.Millisecond)

	// Check if violation was logged
	events, err := d.RecentEvents(ctx, 20)
	if err != nil {
		t.Fatalf("ListEvents error: %v", err)
	}

	found := false
	for _, e := range events {
		if e.EventType == "file_ownership_violation_detected" {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected file_ownership_violation_detected event")
	}
}

func TestFileWatcher_NoViolationForOwnedFile(t *testing.T) {
	d := setupWatcherDB(t)
	ctx := context.Background()

	fw := NewFileWatcher(d, func(s string) { t.Log(s) })
	if err := fw.Start(ctx); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer fw.Stop()

	wtDir := t.TempDir()

	// Task owns "main.go"
	d.CreateTask(ctx, db.Task{ID: "T-001", Title: "Test", Status: "running", Role: "implementer"})
	d.CreateFileLock(ctx, "main.go", "", "T-001", time.Time{})
	fw.AddWorktree(ctx, "T-001", wtDir)

	// Write to the owned file — should NOT trigger violation
	ownedFile := filepath.Join(wtDir, "main.go")
	os.WriteFile(ownedFile, []byte("package main\n"), 0o644)

	time.Sleep(800 * time.Millisecond)

	events, err := d.RecentEvents(ctx, 20)
	if err != nil {
		t.Fatalf("ListEvents error: %v", err)
	}

	for _, e := range events {
		if e.EventType == "file_ownership_violation_detected" {
			t.Error("unexpected violation event for owned file")
		}
	}
}

func TestFileWatcher_NoViolationWithoutLocks(t *testing.T) {
	d := setupWatcherDB(t)
	ctx := context.Background()

	fw := NewFileWatcher(d, func(s string) { t.Log(s) })
	if err := fw.Start(ctx); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer fw.Stop()

	wtDir := t.TempDir()

	// Task has NO file locks (e.g., scout task)
	d.CreateTask(ctx, db.Task{ID: "T-002", Title: "Scout", Status: "running", Role: "scout"})
	fw.AddWorktree(ctx, "T-002", wtDir)

	// Write to any file — should NOT trigger violation (no locks = no enforcement)
	os.WriteFile(filepath.Join(wtDir, "anything.go"), []byte("package scout\n"), 0o644)

	time.Sleep(800 * time.Millisecond)

	events, err := d.RecentEvents(ctx, 20)
	if err != nil {
		t.Fatalf("ListEvents error: %v", err)
	}

	for _, e := range events {
		if e.EventType == "file_ownership_violation_detected" {
			t.Error("unexpected violation for task with no file locks")
		}
	}
}

func TestFileWatcher_Debounce(t *testing.T) {
	d := setupWatcherDB(t)
	ctx := context.Background()

	fw := NewFileWatcher(d, func(s string) { t.Log(s) })
	if err := fw.Start(ctx); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer fw.Stop()

	wtDir := t.TempDir()
	d.CreateTask(ctx, db.Task{ID: "T-001", Title: "Test", Status: "running", Role: "implementer"})
	d.CreateFileLock(ctx, "main.go", "", "T-001", time.Time{})
	fw.AddWorktree(ctx, "T-001", wtDir)

	// Write to unowned file multiple times rapidly
	unownedFile := filepath.Join(wtDir, "other.go")
	for i := 0; i < 5; i++ {
		os.WriteFile(unownedFile, []byte("package main\n// edit\n"), 0o644)
		time.Sleep(50 * time.Millisecond)
	}

	time.Sleep(800 * time.Millisecond)

	events, err := d.RecentEvents(ctx, 50)
	if err != nil {
		t.Fatalf("ListEvents error: %v", err)
	}

	count := 0
	for _, e := range events {
		if e.EventType == "file_ownership_violation_detected" {
			count++
		}
	}
	// Due to debounce, we should see far fewer events than 5
	if count > 2 {
		t.Errorf("expected debounced violations (<=2), got %d", count)
	}
}

func TestResolveTaskForPath(t *testing.T) {
	d := setupWatcherDB(t)
	fw := NewFileWatcher(d, nil)

	fw.mu.Lock()
	fw.worktrees["T-001"] = "/tmp/wt/T-001"
	fw.worktrees["T-002"] = "/tmp/wt/T-002"
	fw.mu.Unlock()

	taskID := fw.resolveTaskForPath("/tmp/wt/T-001/internal/main.go")
	if taskID != "T-001" {
		t.Errorf("expected T-001, got %q", taskID)
	}

	taskID = fw.resolveTaskForPath("/tmp/wt/T-002/go.mod")
	if taskID != "T-002" {
		t.Errorf("expected T-002, got %q", taskID)
	}

	taskID = fw.resolveTaskForPath("/tmp/other/file.go")
	if taskID != "" {
		t.Errorf("expected empty, got %q", taskID)
	}
}
