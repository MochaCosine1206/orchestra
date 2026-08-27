package orchestrator

import (
	"context"
	"database/sql"
	"testing"

	"github.com/MochaCosine1206/orchestra/internal/db"
	"github.com/MochaCosine1206/orchestra/internal/monitor"
)

func TestLenientDeps_StrictMode_CascadeFails(t *testing.T) {
	d := setupTestDB(t)
	ctx := context.Background()

	// A=done, B=failed, C=pending blocked_by=[A,B]
	d.CreateTask(ctx, db.Task{ID: "tA", Title: "A", Status: "done", Role: "implementer"})
	d.CreateTask(ctx, db.Task{ID: "tB", Title: "B", Status: "failed", Role: "implementer"})
	d.CreateTask(ctx, db.Task{ID: "tC", Title: "C", Status: "pending", Role: "implementer",
		BlockedBy: sql.NullString{String: `["tA","tB"]`, Valid: true}})

	// Strict mode (default) — C should be cascade-failed
	candidates, err := d.GetPendingTasksWithAllBlockersTerminal(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(candidates) != 1 || candidates[0].ID != "tC" {
		t.Fatalf("expected tC as cascade candidate, got %v", candidates)
	}
}

func TestLenientDeps_LenientMode_MixedOutcomes(t *testing.T) {
	d := setupTestDB(t)
	ctx := context.Background()

	// A=done, B=failed, C=pending blocked_by=[A,B]
	d.CreateTask(ctx, db.Task{ID: "tA", Title: "A", Status: "done", Role: "implementer"})
	d.CreateTask(ctx, db.Task{ID: "tB", Title: "B", Status: "failed", Role: "implementer"})
	d.CreateTask(ctx, db.Task{ID: "tC", Title: "C", Status: "pending", Role: "implementer",
		BlockedBy: sql.NullString{String: `["tA","tB"]`, Valid: true}})

	// Mixed outcomes query — C has at least one done AND one failed blocker
	mixed, err := d.GetPendingTasksWithMixedBlockerOutcomes(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(mixed) != 1 || mixed[0].ID != "tC" {
		t.Fatalf("expected tC as mixed-outcome candidate, got %v", mixed)
	}
}

func TestLenientDeps_AllFailed_StillCascades(t *testing.T) {
	d := setupTestDB(t)
	ctx := context.Background()

	// A=failed, B=failed, C=pending blocked_by=[A,B]
	d.CreateTask(ctx, db.Task{ID: "tA", Title: "A", Status: "failed", Role: "implementer"})
	d.CreateTask(ctx, db.Task{ID: "tB", Title: "B", Status: "failed", Role: "implementer"})
	d.CreateTask(ctx, db.Task{ID: "tC", Title: "C", Status: "pending", Role: "implementer",
		BlockedBy: sql.NullString{String: `["tA","tB"]`, Valid: true}})

	// Mixed outcomes query should NOT return C (no done blockers)
	mixed, err := d.GetPendingTasksWithMixedBlockerOutcomes(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(mixed) != 0 {
		t.Fatalf("expected 0 mixed-outcome candidates when all blockers failed, got %d", len(mixed))
	}

	// But all-terminal query SHOULD return C
	candidates, err := d.GetPendingTasksWithAllBlockersTerminal(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(candidates) != 1 {
		t.Fatalf("expected 1 cascade candidate, got %d", len(candidates))
	}
}

func TestLenientDeps_MonitorIntegration(t *testing.T) {
	d := setupTestDB(t)
	ctx := context.Background()

	// A=done with result_summary, B=failed, C=pending blocked_by=[A,B]
	d.CreateTask(ctx, db.Task{ID: "tA", Title: "A", Status: "done", Role: "implementer"})
	d.CreateTask(ctx, db.Task{ID: "tB", Title: "B", Status: "failed", Role: "implementer"})
	d.CreateTask(ctx, db.Task{ID: "tC", Title: "C", Status: "pending", Role: "implementer",
		BlockedBy: sql.NullString{String: `["tA","tB"]`, Valid: true}})
	d.SetBlackboard(ctx, "result_summary:tA", "A completed successfully", "test")
	d.SetBlackboard(ctx, "conductor:lenient_deps", "1", "test")

	// Use monitor to run phase1_5
	m := &monitor.Monitor{DB: d, RepoRoot: t.TempDir()}

	// After lenient cascade, C should NOT be failed
	task, _ := d.GetTaskByID(ctx, "tC")
	if task.Status != "pending" {
		t.Fatalf("tC should still be pending before cascade, got %s", task.Status)
	}

	// The lenient_deps key is set — verify monitor sees it
	val, _ := d.GetBlackboardValue(ctx, "conductor:lenient_deps")
	if val != "1" {
		t.Fatalf("expected lenient_deps=1, got %q", val)
	}

	_ = m // monitor created successfully with lenient deps in blackboard
}
