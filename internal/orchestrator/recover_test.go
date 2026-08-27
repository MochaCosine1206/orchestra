package orchestrator

import (
	"context"
	"fmt"
	"os"
	"testing"
)

func TestRecoverNoSession(t *testing.T) {
	d := setupTestDB(t)
	c, err := New(ConductorOpts{DB: d, RepoRoot: t.TempDir()})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	result, err := c.Recover(context.Background())
	if err != nil {
		t.Fatalf("Recover: %v", err)
	}
	if result != nil {
		t.Errorf("expected nil result when no session, got %+v", result)
	}
}

func TestRecoverLiveConductor(t *testing.T) {
	d := setupTestDB(t)
	c, err := New(ConductorOpts{DB: d, RepoRoot: t.TempDir()})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ctx := context.Background()

	// Simulate an active conductor with our own PID (which is alive)
	// With parallel conductors, a live conductor is not an error — it's just skipped.
	d.SetBlackboard(ctx, "conductor:active", "1", "test")
	d.SetBlackboard(ctx, "conductor:pid", fmt.Sprintf("%d", os.Getpid()), "test")

	result, err := c.Recover(ctx)
	if err != nil {
		t.Fatalf("Recover should not error for live conductor: %v", err)
	}
	if result != nil {
		t.Errorf("expected nil result (live conductor = nothing to recover), got %+v", result)
	}
}

func TestRecoverOrphanSession(t *testing.T) {
	d := setupTestDB(t)
	c, err := New(ConductorOpts{DB: d, RepoRoot: t.TempDir()})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ctx := context.Background()

	// Simulate orphan: dead PID, active session with tasks
	d.SetBlackboard(ctx, "conductor:active", "1", "test")
	d.SetBlackboard(ctx, "conductor:pid", "999999", "test") // dead PID
	d.SetBlackboard(ctx, "conductor:session_id", "s-orphan-1", "test")
	d.SetBlackboard(ctx, "conductor:goal", "test orphan goal", "test")
	d.SetBlackboard(ctx, "conductor:task_ids", `["T-001","T-002"]`, "test")

	// Create tasks
	d.ExecContext(ctx, `INSERT INTO tasks (id, title, status, role) VALUES ('T-001', 'task 1', 'done', 'implementer')`)
	d.ExecContext(ctx, `INSERT INTO tasks (id, title, status, role) VALUES ('T-002', 'task 2', 'failed', 'scout')`)

	result, err := c.Recover(ctx)
	if err != nil {
		t.Fatalf("Recover: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if !result.Adopted {
		t.Error("expected Adopted=true")
	}
	if result.SessionID != "s-orphan-1" {
		t.Errorf("SessionID = %q, want s-orphan-1", result.SessionID)
	}
	if result.DoneCount != 1 {
		t.Errorf("DoneCount = %d, want 1", result.DoneCount)
	}
	if result.FailedCount != 1 {
		t.Errorf("FailedCount = %d, want 1", result.FailedCount)
	}
	if c.SessionID != "s-orphan-1" {
		t.Errorf("conductor SessionID should be adopted: got %q", c.SessionID)
	}
}

func TestRecoverStaleKeysNoTasks(t *testing.T) {
	d := setupTestDB(t)
	c, err := New(ConductorOpts{DB: d, RepoRoot: t.TempDir()})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ctx := context.Background()

	// Simulate stale keys with no task_ids
	d.SetBlackboard(ctx, "conductor:active", "1", "test")
	d.SetBlackboard(ctx, "conductor:pid", "999999", "test")
	d.SetBlackboard(ctx, "conductor:session_id", "s-stale-1", "test")

	result, err := c.Recover(ctx)
	if err != nil {
		t.Fatalf("Recover: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if result.Adopted {
		t.Error("expected Adopted=false for stale keys with no tasks")
	}

	// Conductor keys should be cleaned up
	active, _ := d.GetBlackboardValue(ctx, "conductor:active")
	if active == "1" {
		t.Error("expected conductor:active to be cleaned up")
	}
}
