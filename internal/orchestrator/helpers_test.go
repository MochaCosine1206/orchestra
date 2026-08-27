package orchestrator

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/MochaCosine1206/orchestra/internal/db"
)

func TestWaitForTasksExitsOnCascadeFailure(t *testing.T) {
	d := setupTestDB(t)
	c, _ := New(ConductorOpts{DB: d, RepoRoot: "/tmp/test"})
	ctx := context.Background()

	// Create tasks: t1 failed, t2 pending (blocked by t1)
	d.CreateTask(ctx, db.Task{ID: "t1", Title: "Task 1", Status: "failed", Role: "researcher"})
	d.CreateTask(ctx, db.Task{ID: "t2", Title: "Task 2", Status: "pending", Role: "implementer"})

	d.SetBlackboard(ctx, "conductor:task_ids", `["t1","t2"]`, "test")

	// waitForTasks should detect the stuck state and return an error
	// (no running agents, pending tasks, failures present)
	doneCh := make(chan struct{})
	var doneCount, failedCount int
	var waitErr error

	go func() {
		doneCount, failedCount, waitErr = c.waitForTasks(ctx)
		close(doneCh)
	}()

	// Should complete within a reasonable time (not hang forever)
	select {
	case <-doneCh:
		// Good — it returned
	case <-time.After(30 * time.Second):
		t.Fatal("waitForTasks hung — deadlock detected")
	}

	if waitErr == nil {
		t.Error("expected error from waitForTasks for stuck state")
	}
	if failedCount != 1 {
		t.Errorf("expected 1 failed, got %d", failedCount)
	}
	_ = doneCount
}

func TestWaitForTasksCompletesNormally(t *testing.T) {
	d := setupTestDB(t)
	c, _ := New(ConductorOpts{DB: d, RepoRoot: "/tmp/test"})
	ctx := context.Background()

	// Both tasks done
	d.CreateTask(ctx, db.Task{ID: "t1", Title: "Task 1", Status: "done", Role: "implementer"})
	d.CreateTask(ctx, db.Task{ID: "t2", Title: "Task 2", Status: "done", Role: "implementer"})

	d.SetBlackboard(ctx, "conductor:task_ids", `["t1","t2"]`, "test")

	doneCount, failedCount, err := c.waitForTasks(ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if doneCount != 2 {
		t.Errorf("expected 2 done, got %d", doneCount)
	}
	if failedCount != 0 {
		t.Errorf("expected 0 failed, got %d", failedCount)
	}
}

func TestActivateConductorIdempotent(t *testing.T) {
	// G103: calling activateConductor twice on the same Conductor should not create
	// a duplicate DB record — the second call should be a no-op.
	d := setupTestDB(t)
	repoRoot := t.TempDir()
	c, _ := New(ConductorOpts{DB: d, RepoRoot: repoRoot})
	ctx := context.Background()

	// Need a minimal git repo for createStagingBranch (best-effort, OK to fail)
	os.MkdirAll(repoRoot+"/.git", 0o755)

	opts := activateOpts{
		PID:           12345,
		MaxParallel:   3,
		Goal:          "Test G103 idempotency",
		SessionID:     "s-g103-test",
		ModelStrategy: "all-opus",
		BaseBranch:    "dev",
		Runtime:       "local",
	}

	// First activation should succeed
	if err := c.activateConductor(ctx, opts); err != nil {
		t.Fatalf("first activateConductor failed: %v", err)
	}

	if !c.conductorActive {
		t.Error("expected conductorActive to be true after first activation")
	}

	// Second activation should be a no-op (not error, not insert duplicate)
	if err := c.activateConductor(ctx, opts); err != nil {
		t.Fatalf("second activateConductor failed: %v", err)
	}

	// Verify only 1 conductor record exists
	all, err := d.ListAllConductors(ctx)
	if err != nil {
		t.Fatalf("listing conductors: %v", err)
	}
	if len(all) != 1 {
		t.Errorf("expected 1 conductor record, got %d", len(all))
	}

	// Deactivate and verify flag is cleared
	c.deactivateConductor(ctx)
	if c.conductorActive {
		t.Error("expected conductorActive to be false after deactivation")
	}
}
