package monitor

import (
	"context"
	"database/sql"
	"os/exec"
	"testing"

	"github.com/MochaCosine1206/orchestra/internal/db"
)

// --- B-160: Failure Cascade Test with Lenient Deps ---
//
// These tests exercise phase1_5LenientCascade through monitor.RunOnce()
// with complex DAGs and mixed-outcome scenarios.

func TestLenientCascade_DiamondDAG_MixedOutcomes(t *testing.T) {
	mon, d, _ := setupMonitorTest(t)
	ctx := context.Background()

	// DAG: root(done) -> left(done) + right(failed) -> merge(pending, blocked_by=[left,right])
	d.SetBlackboard(ctx, "conductor:active", "1", "test")
	d.SetBlackboard(ctx, "conductor:lenient_deps", "1", "test")
	d.SetBlackboard(ctx, "conductor:task_ids", `["t-root","t-left","t-right","t-merge"]`, "test")

	d.CreateTask(ctx, db.Task{ID: "t-root", Title: "Root", Status: "done", Role: "researcher"})
	d.CreateTask(ctx, db.Task{ID: "t-left", Title: "Left", Status: "done", Role: "implementer",
		BlockedBy: sql.NullString{String: `["t-root"]`, Valid: true}})
	d.CreateTask(ctx, db.Task{ID: "t-right", Title: "Right", Status: "failed", Role: "implementer",
		BlockedBy: sql.NullString{String: `["t-root"]`, Valid: true}})
	d.CreateTask(ctx, db.Task{ID: "t-merge", Title: "Merge", Status: "pending", Role: "implementer",
		BlockedBy: sql.NullString{String: `["t-left","t-right"]`, Valid: true}})

	stats, err := mon.RunOnce(ctx)
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}

	// merge should NOT be cascade-failed; it should be lenient-unblocked and auto-spawned
	task, _ := d.GetTaskByID(ctx, "t-merge")
	if task.Status == "failed" {
		t.Errorf("expected merge to NOT be cascade-failed, got %s", task.Status)
	}

	// lenient_deps annotation should be set
	val, _ := d.GetBlackboardValue(ctx, "lenient_deps:t-merge")
	if val != "1" {
		t.Errorf("expected lenient_deps:t-merge=1, got %q", val)
	}

	// lenient_unblock event should be logged
	events, _ := d.RecentEvents(ctx, 20)
	found := false
	for _, e := range events {
		if e.EventType == "lenient_unblock" && e.TaskID.Valid && e.TaskID.String == "t-merge" {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected lenient_unblock event for t-merge")
	}

	// No cascade failures (merge was lenient-unblocked, not cascade-failed)
	if stats.Failed != 0 {
		t.Errorf("expected 0 cascade failures, got %d", stats.Failed)
	}
}

func TestLenientCascade_MultiLevel_ThreeDeep(t *testing.T) {
	mon, d, _ := setupMonitorTest(t)
	ctx := context.Background()

	// DAG: a(done) + b(failed) -> c(pending) + d(done) -> e(pending)
	// c is blocked by [a,b] (mixed), e is blocked by [c,d]
	d.SetBlackboard(ctx, "conductor:active", "1", "test")
	d.SetBlackboard(ctx, "conductor:lenient_deps", "1", "test")
	d.SetBlackboard(ctx, "conductor:task_ids", `["t-a","t-b","t-c","t-d","t-e"]`, "test")

	d.CreateTask(ctx, db.Task{ID: "t-a", Title: "A", Status: "done", Role: "researcher"})
	d.CreateTask(ctx, db.Task{ID: "t-b", Title: "B", Status: "failed", Role: "researcher"})
	d.CreateTask(ctx, db.Task{ID: "t-c", Title: "C", Status: "pending", Role: "implementer",
		BlockedBy: sql.NullString{String: `["t-a","t-b"]`, Valid: true}})
	d.CreateTask(ctx, db.Task{ID: "t-d", Title: "D", Status: "done", Role: "researcher"})
	d.CreateTask(ctx, db.Task{ID: "t-e", Title: "E", Status: "pending", Role: "implementer",
		BlockedBy: sql.NullString{String: `["t-c","t-d"]`, Valid: true}})

	// Cycle 1: c should be lenient-unblocked and auto-spawned, e stays pending (c not terminal yet)
	stats, err := mon.RunOnce(ctx)
	if err != nil {
		t.Fatalf("RunOnce cycle 1: %v", err)
	}

	taskC, _ := d.GetTaskByID(ctx, "t-c")
	if taskC.Status == "failed" {
		t.Errorf("cycle 1: expected c to NOT be cascade-failed, got %s", taskC.Status)
	}
	val, _ := d.GetBlackboardValue(ctx, "lenient_deps:t-c")
	if val != "1" {
		t.Errorf("cycle 1: expected lenient_deps:t-c=1, got %q", val)
	}

	// e should still be pending — blocker c is not terminal yet (running/assigned)
	taskE, _ := d.GetTaskByID(ctx, "t-e")
	if taskE.Status != "pending" {
		t.Errorf("cycle 1: expected e to stay pending (c not terminal), got %s", taskE.Status)
	}

	// No cascade failures in this cycle
	if stats.Failed != 0 {
		t.Errorf("cycle 1: expected 0 cascade failures, got %d", stats.Failed)
	}

	// Cycle 2: e should still be pending (c is running, not terminal)
	stats2, err := mon.RunOnce(ctx)
	if err != nil {
		t.Fatalf("RunOnce cycle 2: %v", err)
	}

	taskE, _ = d.GetTaskByID(ctx, "t-e")
	if taskE.Status != "pending" {
		t.Errorf("cycle 2: expected e to still be pending, got %s", taskE.Status)
	}
	if stats2.Failed != 0 {
		t.Errorf("cycle 2: expected 0 cascade failures, got %d", stats2.Failed)
	}
}

func TestLenientCascade_PartialPredecessorOutput(t *testing.T) {
	mon, d, _ := setupMonitorTest(t)
	ctx := context.Background()

	// DAG: research(done) + design(done) + security(failed) -> impl(pending)
	d.SetBlackboard(ctx, "conductor:active", "1", "test")
	d.SetBlackboard(ctx, "conductor:lenient_deps", "1", "test")
	d.SetBlackboard(ctx, "conductor:task_ids", `["t-research","t-design","t-security","t-impl"]`, "test")

	d.CreateTask(ctx, db.Task{ID: "t-research", Title: "Research", Status: "done", Role: "researcher"})
	d.CreateTask(ctx, db.Task{ID: "t-design", Title: "Design", Status: "done", Role: "architect"})
	d.CreateTask(ctx, db.Task{ID: "t-security", Title: "Security", Status: "failed", Role: "researcher"})
	d.CreateTask(ctx, db.Task{ID: "t-impl", Title: "Implement", Status: "pending", Role: "implementer",
		BlockedBy: sql.NullString{String: `["t-research","t-design","t-security"]`, Valid: true}})

	// Set result_summaries for done tasks
	d.SetBlackboard(ctx, "result_summary:t-research", "Found 3 relevant papers", "test")
	d.SetBlackboard(ctx, "result_summary:t-design", "API schema designed", "test")

	_, err := mon.RunOnce(ctx)
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}

	// impl should be lenient-unblocked and auto-spawned (not cascade-failed)
	task, _ := d.GetTaskByID(ctx, "t-impl")
	if task.Status == "failed" {
		t.Errorf("expected impl to NOT be cascade-failed, got %s", task.Status)
	}
	val, _ := d.GetBlackboardValue(ctx, "lenient_deps:t-impl")
	if val != "1" {
		t.Errorf("expected lenient_deps:t-impl=1, got %q", val)
	}

	// result_summaries from done tasks should still be accessible
	rs1, _ := d.GetBlackboardValue(ctx, "result_summary:t-research")
	if rs1 != "Found 3 relevant papers" {
		t.Errorf("expected research result_summary preserved, got %q", rs1)
	}
	rs2, _ := d.GetBlackboardValue(ctx, "result_summary:t-design")
	if rs2 != "API schema designed" {
		t.Errorf("expected design result_summary preserved, got %q", rs2)
	}

	// No result_summary for failed task
	rs3, _ := d.GetBlackboardValue(ctx, "result_summary:t-security")
	if rs3 != "" {
		t.Errorf("expected no result_summary for failed task, got %q", rs3)
	}
}

func TestLenientCascade_StrictVsLenient_SameDAG(t *testing.T) {
	// Same DAG tested under strict and lenient modes
	// A=done, B=failed -> C=pending(blocked_by=[A,B])

	t.Run("Strict", func(t *testing.T) {
		mon, d, _ := setupMonitorTest(t)
		ctx := context.Background()

		d.SetBlackboard(ctx, "conductor:active", "1", "test")
		// No lenient_deps — strict mode (default)
		d.SetBlackboard(ctx, "conductor:task_ids", `["tA","tB","tC"]`, "test")

		d.CreateTask(ctx, db.Task{ID: "tA", Title: "A", Status: "done", Role: "implementer"})
		d.CreateTask(ctx, db.Task{ID: "tB", Title: "B", Status: "failed", Role: "implementer"})
		d.CreateTask(ctx, db.Task{ID: "tC", Title: "C", Status: "pending", Role: "implementer",
			BlockedBy: sql.NullString{String: `["tA","tB"]`, Valid: true}})

		stats, err := mon.RunOnce(ctx)
		if err != nil {
			t.Fatalf("RunOnce: %v", err)
		}

		// Strict: C should be cascade-failed
		task, _ := d.GetTaskByID(ctx, "tC")
		if task.Status != "failed" {
			t.Errorf("strict: expected C to be cascade-failed, got %s", task.Status)
		}

		ft, _ := d.GetBlackboardValue(ctx, "failure_type:tC")
		if ft != "cascade_fail" {
			t.Errorf("strict: expected failure_type cascade_fail, got %q", ft)
		}

		if stats.Failed != 1 {
			t.Errorf("strict: expected 1 cascade failure, got %d", stats.Failed)
		}
	})

	t.Run("Lenient", func(t *testing.T) {
		mon, d, _ := setupMonitorTest(t)
		ctx := context.Background()

		d.SetBlackboard(ctx, "conductor:active", "1", "test")
		d.SetBlackboard(ctx, "conductor:lenient_deps", "1", "test")
		d.SetBlackboard(ctx, "conductor:task_ids", `["tA","tB","tC"]`, "test")

		d.CreateTask(ctx, db.Task{ID: "tA", Title: "A", Status: "done", Role: "implementer"})
		d.CreateTask(ctx, db.Task{ID: "tB", Title: "B", Status: "failed", Role: "implementer"})
		d.CreateTask(ctx, db.Task{ID: "tC", Title: "C", Status: "pending", Role: "implementer",
			BlockedBy: sql.NullString{String: `["tA","tB"]`, Valid: true}})

		stats, err := mon.RunOnce(ctx)
		if err != nil {
			t.Fatalf("RunOnce: %v", err)
		}

		// Lenient: C should NOT be cascade-failed; it should be lenient-unblocked and auto-spawned
		task, _ := d.GetTaskByID(ctx, "tC")
		if task.Status == "failed" {
			t.Errorf("lenient: expected C to NOT be cascade-failed, got %s", task.Status)
		}

		val, _ := d.GetBlackboardValue(ctx, "lenient_deps:tC")
		if val != "1" {
			t.Errorf("lenient: expected lenient_deps:tC=1, got %q", val)
		}

		if stats.Failed != 0 {
			t.Errorf("lenient: expected 0 cascade failures, got %d", stats.Failed)
		}
	})
}

func TestLenientCascade_WideFanOut_SingleFailure(t *testing.T) {
	mon, d, _ := setupMonitorTest(t)
	ctx := context.Background()

	// DAG: 4 done + 1 failed -> merge(pending)
	d.SetBlackboard(ctx, "conductor:active", "1", "test")
	d.SetBlackboard(ctx, "conductor:lenient_deps", "1", "test")
	d.SetBlackboard(ctx, "conductor:task_ids", `["t1","t2","t3","t4","t5","t-merge"]`, "test")

	d.CreateTask(ctx, db.Task{ID: "t1", Title: "Task 1", Status: "done", Role: "implementer"})
	d.CreateTask(ctx, db.Task{ID: "t2", Title: "Task 2", Status: "done", Role: "implementer"})
	d.CreateTask(ctx, db.Task{ID: "t3", Title: "Task 3", Status: "done", Role: "implementer"})
	d.CreateTask(ctx, db.Task{ID: "t4", Title: "Task 4", Status: "done", Role: "implementer"})
	d.CreateTask(ctx, db.Task{ID: "t5", Title: "Task 5", Status: "failed", Role: "implementer"})
	d.CreateTask(ctx, db.Task{ID: "t-merge", Title: "Merge All", Status: "pending", Role: "implementer",
		BlockedBy: sql.NullString{String: `["t1","t2","t3","t4","t5"]`, Valid: true}})

	// Set result_summaries for all done tasks
	d.SetBlackboard(ctx, "result_summary:t1", "Component A built", "test")
	d.SetBlackboard(ctx, "result_summary:t2", "Component B built", "test")
	d.SetBlackboard(ctx, "result_summary:t3", "Component C built", "test")
	d.SetBlackboard(ctx, "result_summary:t4", "Component D built", "test")

	stats, err := mon.RunOnce(ctx)
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}

	// merge should be lenient-unblocked and auto-spawned (not cascade-failed)
	task, _ := d.GetTaskByID(ctx, "t-merge")
	if task.Status == "failed" {
		t.Errorf("expected merge to NOT be cascade-failed, got %s", task.Status)
	}

	val, _ := d.GetBlackboardValue(ctx, "lenient_deps:t-merge")
	if val != "1" {
		t.Errorf("expected lenient_deps:t-merge=1, got %q", val)
	}

	// All 4 result_summaries should be accessible
	for i := 1; i <= 4; i++ {
		key := "result_summary:t" + string(rune('0'+i))
		rs, _ := d.GetBlackboardValue(ctx, key)
		if rs == "" {
			t.Errorf("expected result_summary for t%d to be accessible", i)
		}
	}

	if stats.Failed != 0 {
		t.Errorf("expected 0 cascade failures, got %d", stats.Failed)
	}
}

func TestLenientCascade_ProgressiveResolution_MultipleCycles(t *testing.T) {
	mon, d, _ := setupMonitorTest(t)
	ctx := context.Background()

	// DAG: root(running, real sleep process) + dep(done) -> blocked(pending)
	d.SetBlackboard(ctx, "conductor:active", "1", "test")
	d.SetBlackboard(ctx, "conductor:lenient_deps", "1", "test")
	d.SetBlackboard(ctx, "conductor:task_ids", `["t-root","t-dep","t-blocked"]`, "test")

	// Spawn a real process for root (alive PID)
	sleepCmd := exec.Command("sleep", "60")
	sleepCmd.Start()
	pid := sleepCmd.Process.Pid
	defer func() {
		sleepCmd.Process.Kill()
		sleepCmd.Wait()
	}()

	d.RegisterAgent(ctx, db.Agent{ID: "a-root", Role: "implementer", Status: "working",
		CurrentTask: sql.NullString{String: "t-root", Valid: true}})
	d.UpdateAgentPID(ctx, "a-root", pid)
	d.CreateTask(ctx, db.Task{ID: "t-root", Title: "Root", Status: "running", Role: "implementer"})
	d.AssignTask(ctx, "t-root", "a-root", "", "")

	d.CreateTask(ctx, db.Task{ID: "t-dep", Title: "Dep", Status: "done", Role: "researcher"})
	d.CreateTask(ctx, db.Task{ID: "t-blocked", Title: "Blocked", Status: "pending", Role: "implementer",
		BlockedBy: sql.NullString{String: `["t-root","t-dep"]`, Valid: true}})

	// Cycle 1: root alive, blocked stays pending (blockers not all terminal)
	_, err := mon.RunOnce(ctx)
	if err != nil {
		t.Fatalf("RunOnce cycle 1: %v", err)
	}

	taskBlocked, _ := d.GetTaskByID(ctx, "t-blocked")
	if taskBlocked.Status != "pending" {
		t.Errorf("cycle 1: expected blocked to stay pending (root alive), got %s", taskBlocked.Status)
	}

	// No lenient_deps annotation (root is still running, not terminal)
	val, _ := d.GetBlackboardValue(ctx, "lenient_deps:t-blocked")
	if val != "" {
		t.Errorf("cycle 1: expected no lenient_deps annotation (root alive), got %q", val)
	}

	// Kill root and mark it done (simulating natural completion)
	sleepCmd.Process.Kill()
	sleepCmd.Wait()
	d.CompleteTask(ctx, "t-root", "completed successfully")
	d.SetAgentDead(ctx, "a-root")
	d.SetBlackboard(ctx, "result_summary:t-root", "Root finished", "test")

	// Cycle 2: root done, dep done → blocked unblocks naturally (all blockers done, no cascade)
	_, err = mon.RunOnce(ctx)
	if err != nil {
		t.Fatalf("RunOnce cycle 2: %v", err)
	}

	taskBlocked, _ = d.GetTaskByID(ctx, "t-blocked")
	// In phase2 auto-spawn, blocked should be picked up as unblocked since all blockers are done
	// It may be spawned (status=running/assigned) or still pending but no longer blocked
	if taskBlocked.Status == "failed" {
		t.Errorf("cycle 2: blocked should NOT be cascade-failed (all blockers done), got %s", taskBlocked.Status)
	}

	// No lenient_deps annotation needed (natural unblock, not mixed outcomes)
	val, _ = d.GetBlackboardValue(ctx, "lenient_deps:t-blocked")
	if val != "" {
		t.Errorf("cycle 2: expected no lenient_deps (natural unblock), got %q", val)
	}
}

func TestLenientCascade_AllFailedBoundary(t *testing.T) {
	// Tests the exact boundary: all-failed cascades, but exactly-one-succeeds lenient-unblocks

	t.Run("AllFailed", func(t *testing.T) {
		mon, d, _ := setupMonitorTest(t)
		ctx := context.Background()

		d.SetBlackboard(ctx, "conductor:active", "1", "test")
		d.SetBlackboard(ctx, "conductor:lenient_deps", "1", "test")
		d.SetBlackboard(ctx, "conductor:task_ids", `["t1","t2","t3","t-target"]`, "test")

		// 3 failed blockers
		d.CreateTask(ctx, db.Task{ID: "t1", Title: "T1", Status: "failed", Role: "implementer"})
		d.CreateTask(ctx, db.Task{ID: "t2", Title: "T2", Status: "failed", Role: "implementer"})
		d.CreateTask(ctx, db.Task{ID: "t3", Title: "T3", Status: "failed", Role: "implementer"})
		d.CreateTask(ctx, db.Task{ID: "t-target", Title: "Target", Status: "pending", Role: "implementer",
			BlockedBy: sql.NullString{String: `["t1","t2","t3"]`, Valid: true}})

		stats, err := mon.RunOnce(ctx)
		if err != nil {
			t.Fatalf("RunOnce: %v", err)
		}

		// All blockers failed → cascade-fail even in lenient mode
		task, _ := d.GetTaskByID(ctx, "t-target")
		if task.Status != "failed" {
			t.Errorf("all-failed: expected t-target to be cascade-failed, got %s", task.Status)
		}

		ft, _ := d.GetBlackboardValue(ctx, "failure_type:t-target")
		if ft != "cascade_fail" {
			t.Errorf("all-failed: expected failure_type cascade_fail, got %q", ft)
		}

		if stats.Failed != 1 {
			t.Errorf("all-failed: expected 1 cascade failure, got %d", stats.Failed)
		}
	})

	t.Run("ExactlyOneSucceeds", func(t *testing.T) {
		mon, d, _ := setupMonitorTest(t)
		ctx := context.Background()

		d.SetBlackboard(ctx, "conductor:active", "1", "test")
		d.SetBlackboard(ctx, "conductor:lenient_deps", "1", "test")
		d.SetBlackboard(ctx, "conductor:task_ids", `["t1","t2","t3","t-target"]`, "test")

		// 2 failed + 1 done — the boundary case
		d.CreateTask(ctx, db.Task{ID: "t1", Title: "T1", Status: "failed", Role: "implementer"})
		d.CreateTask(ctx, db.Task{ID: "t2", Title: "T2", Status: "failed", Role: "implementer"})
		d.CreateTask(ctx, db.Task{ID: "t3", Title: "T3", Status: "done", Role: "implementer"})
		d.CreateTask(ctx, db.Task{ID: "t-target", Title: "Target", Status: "pending", Role: "implementer",
			BlockedBy: sql.NullString{String: `["t1","t2","t3"]`, Valid: true}})

		stats, err := mon.RunOnce(ctx)
		if err != nil {
			t.Fatalf("RunOnce: %v", err)
		}

		// Exactly one succeeded → lenient-unblock and auto-spawn, not cascade-fail
		task, _ := d.GetTaskByID(ctx, "t-target")
		if task.Status == "failed" {
			t.Errorf("exactly-one: expected t-target to NOT be cascade-failed, got %s", task.Status)
		}

		val, _ := d.GetBlackboardValue(ctx, "lenient_deps:t-target")
		if val != "1" {
			t.Errorf("exactly-one: expected lenient_deps:t-target=1, got %q", val)
		}

		if stats.Failed != 0 {
			t.Errorf("exactly-one: expected 0 cascade failures, got %d", stats.Failed)
		}
	})
}

// --- B-174: Auto-Spawn After Lenient Annotation Tests ---

func TestLenientCascade_AutoSpawn_AfterAnnotation(t *testing.T) {
	mon, d, _ := setupMonitorTest(t)
	ctx := context.Background()

	// DAG: a(done) + b(failed) -> c(pending, blocked_by=[a,b])
	d.SetBlackboard(ctx, "conductor:active", "1", "test")
	d.SetBlackboard(ctx, "conductor:lenient_deps", "1", "test")
	d.SetBlackboard(ctx, "conductor:max_parallel", "5", "test")
	d.SetBlackboard(ctx, "conductor:task_ids", `["t-a","t-b","t-c"]`, "test")

	d.CreateTask(ctx, db.Task{ID: "t-a", Title: "A", Status: "done", Role: "researcher"})
	d.CreateTask(ctx, db.Task{ID: "t-b", Title: "B", Status: "failed", Role: "researcher"})
	d.CreateTask(ctx, db.Task{ID: "t-c", Title: "C", Status: "pending", Role: "implementer",
		BlockedBy: sql.NullString{String: `["t-a","t-b"]`, Valid: true}})

	// RunOnce should: phase1.5 annotates t-c, then phase2 spawns it
	stats, err := mon.RunOnce(ctx)
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}

	// t-c should have been annotated
	val, _ := d.GetBlackboardValue(ctx, "lenient_deps:t-c")
	if val != "1" {
		t.Errorf("expected lenient_deps:t-c=1, got %q", val)
	}

	// t-c should have been auto-spawned
	if stats.AutoSpawned < 1 {
		t.Errorf("expected AutoSpawned >= 1, got %d", stats.AutoSpawned)
	}

	// Verify t-c is no longer pending (it should be assigned or running)
	task, _ := d.GetTaskByID(ctx, "t-c")
	if task.Status == "pending" {
		t.Errorf("expected t-c to be spawned (assigned/running), still pending")
	}
}

func TestLenientCascade_AutoSpawn_RespectsSlotLimit(t *testing.T) {
	mon, d, _ := setupMonitorTest(t)
	ctx := context.Background()

	// max_parallel=1, one task already running, lenient task should NOT be spawned
	d.SetBlackboard(ctx, "conductor:active", "1", "test")
	d.SetBlackboard(ctx, "conductor:lenient_deps", "1", "test")
	d.SetBlackboard(ctx, "conductor:max_parallel", "1", "test")
	d.SetBlackboard(ctx, "conductor:task_ids", `["t-a","t-b","t-c","t-running"]`, "test")

	d.CreateTask(ctx, db.Task{ID: "t-a", Title: "A", Status: "done", Role: "researcher"})
	d.CreateTask(ctx, db.Task{ID: "t-b", Title: "B", Status: "failed", Role: "researcher"})
	d.CreateTask(ctx, db.Task{ID: "t-c", Title: "C", Status: "pending", Role: "implementer",
		BlockedBy: sql.NullString{String: `["t-a","t-b"]`, Valid: true}})
	d.CreateTask(ctx, db.Task{ID: "t-running", Title: "Running", Status: "running", Role: "implementer"})

	stats, err := mon.RunOnce(ctx)
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}

	// With 1 slot and 1 running, no spawns should happen
	if stats.AutoSpawned != 0 {
		t.Errorf("expected 0 spawns (slot limit), got %d", stats.AutoSpawned)
	}

	// t-c should still be pending
	task, _ := d.GetTaskByID(ctx, "t-c")
	if task.Status != "pending" {
		t.Errorf("expected t-c to remain pending (no slots), got %s", task.Status)
	}
}

func TestLenientCascade_AutoSpawn_NoDuplicateSpawn(t *testing.T) {
	mon, d, _ := setupMonitorTest(t)
	ctx := context.Background()

	d.SetBlackboard(ctx, "conductor:active", "1", "test")
	d.SetBlackboard(ctx, "conductor:lenient_deps", "1", "test")
	d.SetBlackboard(ctx, "conductor:max_parallel", "5", "test")
	d.SetBlackboard(ctx, "conductor:task_ids", `["t-a","t-b","t-c"]`, "test")

	d.CreateTask(ctx, db.Task{ID: "t-a", Title: "A", Status: "done", Role: "researcher"})
	d.CreateTask(ctx, db.Task{ID: "t-b", Title: "B", Status: "failed", Role: "researcher"})
	d.CreateTask(ctx, db.Task{ID: "t-c", Title: "C", Status: "pending", Role: "implementer",
		BlockedBy: sql.NullString{String: `["t-a","t-b"]`, Valid: true}})

	// Cycle 1: annotate and spawn
	stats1, err := mon.RunOnce(ctx)
	if err != nil {
		t.Fatalf("RunOnce cycle 1: %v", err)
	}
	if stats1.AutoSpawned < 1 {
		t.Fatalf("cycle 1: expected at least 1 spawn, got %d", stats1.AutoSpawned)
	}

	// Record the spawn count from events
	events1, _ := d.RecentEvents(ctx, 100)
	spawnCount := 0
	for _, e := range events1 {
		if e.EventType == "auto_spawned" && e.TaskID.Valid && e.TaskID.String == "t-c" {
			spawnCount++
		}
	}

	// Cycle 2: should NOT spawn t-c again (it's no longer pending)
	stats2, _ := mon.RunOnce(ctx)

	events2, _ := d.RecentEvents(ctx, 100)
	spawnCount2 := 0
	for _, e := range events2 {
		if e.EventType == "auto_spawned" && e.TaskID.Valid && e.TaskID.String == "t-c" {
			spawnCount2++
		}
	}

	// Spawn count should not have increased
	if spawnCount2 != spawnCount {
		t.Errorf("expected no new spawns for t-c, spawn events went from %d to %d", spawnCount, spawnCount2)
	}

	_ = stats2
}
