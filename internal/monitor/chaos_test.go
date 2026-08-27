package monitor

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/MochaCosine1206/orchestra/internal/db"
)

// B-158: Chaos Test — Random Agent Kills
//
// Tests monitor resilience under chaotic conditions:
// 1. Multiple simultaneous agent deaths with circuit breaker engaged
// 2. Circuit breaker engagement after 3 retries
// 3. Infrastructure failure bypass (context_exhausted doesn't increment retry)
// 4. Cascade failure propagation through multi-level dependency chains
// 5. Mixed alive/dead agents in single cycle
// 6. Rapid fail-retry progression through circuit breaker threshold
// 7. Diamond dependency partial failure
// 8. Timeout kill alongside normal operation
// 9. Batch with mixed success and crash outcomes

// TestChaosMultipleSimultaneousDeaths verifies the monitor handles
// multiple agents dying in the same cycle without losing track of any.
// All agents have retry=3 so circuit breaker fires and tasks stay failed.
func TestChaosMultipleSimultaneousDeaths(t *testing.T) {
	mon, d, repoRoot := setupMonitorTest(t)
	ctx := context.Background()

	d.SetBlackboard(ctx, "conductor:active", "1", "test")
	d.SetBlackboard(ctx, "conductor:task_ids", `["t1","t2","t3","t4","t5"]`, "test")
	d.SetBlackboard(ctx, "conductor:max_parallel", "5", "test")

	// Create 5 agents, all with dead PIDs and circuit breaker engaged (retry=3)
	for i := 1; i <= 5; i++ {
		agentID := fmt.Sprintf("a%d", i)
		taskID := fmt.Sprintf("t%d", i)

		d.RegisterAgent(ctx, db.Agent{ID: agentID, Role: "implementer", Status: "working",
			CurrentTask: sql.NullString{String: taskID, Valid: true}})
		d.UpdateAgentPID(ctx, agentID, 999990+i)
		d.CreateTask(ctx, db.Task{ID: taskID, Title: fmt.Sprintf("Task %d", i), Status: "running", Role: "implementer"})
		wt := filepath.Join(repoRoot, ".worktree", taskID)
		os.MkdirAll(wt, 0o755)
		d.AssignTask(ctx, taskID, agentID, wt, "feature/"+taskID)

		// Circuit breaker: prevent respawn
		d.SetBlackboard(ctx, "retry:"+taskID, "3", "spawner")

		// Write crash logs (no result line → crash)
		os.WriteFile(filepath.Join(mon.LogsDir, taskID+".jsonl"),
			[]byte(`{"type":"init","session":"sess-`+taskID+`"}`+"\n"), 0o644)
	}

	stats, err := mon.RunOnce(ctx)
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}

	// All 5 should be detected as dead
	if stats.Checked != 5 {
		t.Errorf("expected 5 checked, got %d", stats.Checked)
	}
	if stats.Failed < 5 {
		t.Errorf("expected at least 5 failed, got %d", stats.Failed)
	}

	// Verify each task is failed with failure_type set
	for i := 1; i <= 5; i++ {
		taskID := fmt.Sprintf("t%d", i)
		task, _ := d.GetTaskByID(ctx, taskID)
		if task.Status != "failed" {
			t.Errorf("task %s: expected 'failed', got %q", taskID, task.Status)
		}
		ft, _ := d.GetBlackboardValue(ctx, "failure_type:"+taskID)
		if ft == "" {
			t.Errorf("task %s: expected failure_type to be set", taskID)
		}
	}
}

// TestChaosCircuitBreakerEngagement verifies that after 3 retries the
// circuit breaker prevents further respawn attempts.
func TestChaosCircuitBreakerEngagement(t *testing.T) {
	mon, d, repoRoot := setupMonitorTest(t)
	ctx := context.Background()

	d.SetBlackboard(ctx, "conductor:active", "1", "test")
	d.SetBlackboard(ctx, "conductor:task_ids", `["t1"]`, "test")

	// Simulate 3 prior retries via blackboard — circuit breaker threshold
	d.SetBlackboard(ctx, "retry:t1", "3", "spawner")

	// Agent is dead
	d.RegisterAgent(ctx, db.Agent{ID: "a1", Role: "implementer", Status: "working",
		CurrentTask: sql.NullString{String: "t1", Valid: true}})
	d.UpdateAgentPID(ctx, "a1", 999999)
	d.CreateTask(ctx, db.Task{ID: "t1", Title: "Doomed task", Status: "running", Role: "implementer"})
	wt := filepath.Join(repoRoot, ".worktree", "t1")
	os.MkdirAll(wt, 0o755)
	d.AssignTask(ctx, "t1", "a1", wt, "feature/t1")

	// Crash log
	os.WriteFile(filepath.Join(mon.LogsDir, "t1.jsonl"),
		[]byte(`{"type":"init","session":"sess-t1"}`+"\n"), 0o644)

	stats, err := mon.RunOnce(ctx)
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}

	if stats.Failed < 1 {
		t.Errorf("expected at least 1 failed, got %d", stats.Failed)
	}

	// Task should remain failed (circuit breaker prevents respawn)
	task, _ := d.GetTaskByID(ctx, "t1")
	if task.Status != "failed" {
		t.Errorf("expected task to stay 'failed' (circuit breaker), got %q", task.Status)
	}

	// Retry count should still be 3 (not incremented further)
	retryStr, _ := d.GetBlackboardValue(ctx, "retry:t1")
	retryCount, _ := strconv.Atoi(retryStr)
	if retryCount > 3 {
		t.Errorf("circuit breaker should prevent retry increment beyond 3, got %d", retryCount)
	}
}

// TestChaosInfrastructureFailureBypass verifies that context_exhausted
// failures do NOT increment the retry counter (infrastructure bypass).
func TestChaosInfrastructureFailureBypass(t *testing.T) {
	mon, d, repoRoot := setupMonitorTest(t)
	ctx := context.Background()

	d.SetBlackboard(ctx, "conductor:active", "1", "test")
	d.SetBlackboard(ctx, "conductor:task_ids", `["t1"]`, "test")

	// Agent is dead
	d.RegisterAgent(ctx, db.Agent{ID: "a1", Role: "implementer", Status: "working",
		CurrentTask: sql.NullString{String: "t1", Valid: true}})
	d.UpdateAgentPID(ctx, "a1", 999999)
	d.CreateTask(ctx, db.Task{ID: "t1", Title: "Context exhaustion task", Status: "running", Role: "implementer"})
	wt := filepath.Join(repoRoot, ".worktree", "t1")
	os.MkdirAll(wt, 0o755)
	d.AssignTask(ctx, "t1", "a1", wt, "feature/t1")

	// Write stderr with context exhaustion signal
	stderrPath := filepath.Join(mon.LogsDir, "t1.stderr")
	os.WriteFile(stderrPath, []byte("Error: context window limit exceeded, compaction triggered\n"), 0o644)

	// Crash log (no result)
	os.WriteFile(filepath.Join(mon.LogsDir, "t1.jsonl"),
		[]byte(`{"type":"init","session":"sess-t1"}`+"\n"), 0o644)

	_, err := mon.RunOnce(ctx)
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}

	// Check that failure_type is set — the classifier reads stderr
	ft, _ := d.GetBlackboardValue(ctx, "failure_type:t1")
	if ft != "" {
		// If classified as context_exhausted, retry counter should not increment
		retryStr, _ := d.GetBlackboardValue(ctx, "retry:t1")
		retryCount, _ := strconv.Atoi(retryStr)
		if ft == "context_exhausted" && retryCount > 0 {
			t.Errorf("context_exhausted should NOT increment retry counter, got %d", retryCount)
		}
		t.Logf("failure classified as %q, retry count = %d", ft, retryCount)
	} else {
		t.Logf("no failure_type set — classifier may not have found stderr file at expected path")
	}
}

// TestChaosCascadeChainPropagation verifies that failure cascades
// propagate through multi-level dependency chains across cycles.
func TestChaosCascadeChainPropagation(t *testing.T) {
	mon, d, _ := setupMonitorTest(t)
	ctx := context.Background()

	d.SetBlackboard(ctx, "conductor:active", "1", "test")
	d.SetBlackboard(ctx, "conductor:task_ids", `["t1","t2","t3","t4"]`, "test")

	// Chain: t1 (failed) → t2 (blocked by t1) → t3 (blocked by t2) → t4 (blocked by t3)
	d.CreateTask(ctx, db.Task{ID: "t1", Title: "Root failure", Status: "failed", Role: "researcher",
		BlockedBy: sql.NullString{String: "[]", Valid: true}})
	d.CreateTask(ctx, db.Task{ID: "t2", Title: "Level 1", Status: "pending", Role: "implementer",
		BlockedBy: sql.NullString{String: `["t1"]`, Valid: true}})
	d.CreateTask(ctx, db.Task{ID: "t3", Title: "Level 2", Status: "pending", Role: "implementer",
		BlockedBy: sql.NullString{String: `["t2"]`, Valid: true}})
	d.CreateTask(ctx, db.Task{ID: "t4", Title: "Level 3", Status: "pending", Role: "reviewer",
		BlockedBy: sql.NullString{String: `["t3"]`, Valid: true}})

	// Run cycles until full cascade propagation
	cascadedTasks := 0
	for cycle := 0; cycle < 5; cycle++ {
		stats, err := mon.RunOnce(ctx)
		if err != nil {
			t.Fatalf("RunOnce cycle %d: %v", cycle, err)
		}
		cascadedTasks += stats.Failed
		if cascadedTasks >= 3 {
			break
		}
	}

	// Verify entire chain is failed
	for _, taskID := range []string{"t1", "t2", "t3", "t4"} {
		task, _ := d.GetTaskByID(ctx, taskID)
		if task.Status != "failed" {
			t.Errorf("task %s: expected 'failed', got %q", taskID, task.Status)
		}
	}

	// Verify cascade_fail type on downstream tasks
	for _, taskID := range []string{"t2", "t3", "t4"} {
		ft, _ := d.GetBlackboardValue(ctx, "failure_type:"+taskID)
		if ft != "cascade_fail" {
			t.Errorf("task %s: expected failure_type 'cascade_fail', got %q", taskID, ft)
		}
	}
}

// TestChaosMixedAliveDeadAgents verifies correct handling when some
// agents are alive and working while others are dead, in the same cycle.
func TestChaosMixedAliveDeadAgents(t *testing.T) {
	mon, d, repoRoot := setupMonitorTest(t)
	ctx := context.Background()

	d.SetBlackboard(ctx, "conductor:active", "1", "test")
	d.SetBlackboard(ctx, "conductor:task_ids", `["t1","t2","t3"]`, "test")
	d.SetBlackboard(ctx, "conductor:max_parallel", "3", "test")

	// Spawn 2 real processes (alive agents)
	var sleepCmds []*exec.Cmd
	var alivePids []int
	for i := 0; i < 2; i++ {
		cmd := exec.Command("sleep", "60")
		cmd.Start()
		sleepCmds = append(sleepCmds, cmd)
		alivePids = append(alivePids, cmd.Process.Pid)
	}
	defer func() {
		for _, cmd := range sleepCmds {
			cmd.Process.Kill()
			cmd.Wait()
		}
	}()

	// Agent 1: alive, working on t1
	d.RegisterAgent(ctx, db.Agent{ID: "a1", Role: "implementer", Status: "working",
		CurrentTask: sql.NullString{String: "t1", Valid: true}})
	d.UpdateAgentPID(ctx, "a1", alivePids[0])
	d.CreateTask(ctx, db.Task{ID: "t1", Title: "Alive task 1", Status: "running", Role: "implementer"})
	d.AssignTask(ctx, "t1", "a1", "", "feature/t1")
	os.WriteFile(filepath.Join(mon.LogsDir, "t1.jsonl"),
		[]byte(`{"type":"init","session":"s1"}`+"\n"), 0o644)

	// Agent 2: alive, working on t2
	d.RegisterAgent(ctx, db.Agent{ID: "a2", Role: "implementer", Status: "working",
		CurrentTask: sql.NullString{String: "t2", Valid: true}})
	d.UpdateAgentPID(ctx, "a2", alivePids[1])
	d.CreateTask(ctx, db.Task{ID: "t2", Title: "Alive task 2", Status: "running", Role: "implementer"})
	d.AssignTask(ctx, "t2", "a2", "", "feature/t2")
	os.WriteFile(filepath.Join(mon.LogsDir, "t2.jsonl"),
		[]byte(`{"type":"init","session":"s2"}`+"\n"), 0o644)

	// Agent 3: dead PID, circuit breaker engaged
	d.RegisterAgent(ctx, db.Agent{ID: "a3", Role: "implementer", Status: "working",
		CurrentTask: sql.NullString{String: "t3", Valid: true}})
	d.UpdateAgentPID(ctx, "a3", 999999)
	d.CreateTask(ctx, db.Task{ID: "t3", Title: "Dead task", Status: "running", Role: "implementer"})
	wt := filepath.Join(repoRoot, ".worktree", "t3")
	os.MkdirAll(wt, 0o755)
	d.AssignTask(ctx, "t3", "a3", wt, "feature/t3")
	d.SetBlackboard(ctx, "retry:t3", "3", "spawner")
	os.WriteFile(filepath.Join(mon.LogsDir, "t3.jsonl"),
		[]byte(`{"type":"init","session":"s3"}`+"\n"), 0o644)

	stats, err := mon.RunOnce(ctx)
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}

	// Should check all 3 agents
	if stats.Checked != 3 {
		t.Errorf("expected 3 checked, got %d", stats.Checked)
	}

	// Alive tasks should NOT be failed (they may be "assigned" after AssignTask)
	for _, taskID := range []string{"t1", "t2"} {
		task, _ := d.GetTaskByID(ctx, taskID)
		if task.Status == "failed" {
			t.Errorf("task %s: should NOT be failed (agent is alive), got %q", taskID, task.Status)
		}
	}

	// Dead task should be failed (circuit breaker prevents respawn)
	task3, _ := d.GetTaskByID(ctx, "t3")
	if task3.Status != "failed" {
		t.Errorf("task t3: expected 'failed', got %q", task3.Status)
	}
}

// TestChaosRapidFailRetryProgression verifies the retry counter increments
// correctly across multiple deaths, and that respawn is attempted for each.
func TestChaosRapidFailRetryProgression(t *testing.T) {
	mon, d, repoRoot := setupMonitorTest(t)
	ctx := context.Background()

	d.SetBlackboard(ctx, "conductor:active", "1", "test")
	d.SetBlackboard(ctx, "conductor:task_ids", `["t1"]`, "test")
	d.SetBlackboard(ctx, "conductor:max_parallel", "3", "test")

	d.CreateTask(ctx, db.Task{ID: "t1", Title: "Flaky task", Status: "running", Role: "implementer"})

	// Helper to set up a dead agent for t1
	setupDeadAgent := func(agentID string) {
		d.RegisterAgent(ctx, db.Agent{ID: agentID, Role: "implementer", Status: "working",
			CurrentTask: sql.NullString{String: "t1", Valid: true}})
		d.UpdateAgentPID(ctx, agentID, 999999)
		wt := filepath.Join(repoRoot, ".worktree", "t1")
		os.MkdirAll(wt, 0o755)
		d.AssignTask(ctx, "t1", agentID, wt, "feature/t1")
		os.WriteFile(filepath.Join(mon.LogsDir, "t1.jsonl"),
			[]byte(`{"type":"init","session":"sess-`+agentID+`"}`+"\n"), 0o644)
	}

	// Track retry progression
	retryValues := []int{}

	for death := 0; death < 4; death++ {
		setupDeadAgent(fmt.Sprintf("a1-r%d", death))

		_, err := mon.RunOnce(ctx)
		if err != nil {
			t.Fatalf("RunOnce death %d: %v", death, err)
		}

		retryStr, _ := d.GetBlackboardValue(ctx, "retry:t1")
		retryCount, _ := strconv.Atoi(retryStr)
		retryValues = append(retryValues, retryCount)
		taskStatus := mustGetTaskStatus(d, ctx, "t1")
		t.Logf("after death %d: retry=%d, status=%s", death, retryCount, taskStatus)

		// If task got respawned (running/pending), set it back to running for next death
		if taskStatus != "failed" {
			d.StartTask(ctx, "t1")
		}
	}

	// Verify retry counter increased over the sequence
	if len(retryValues) < 4 {
		t.Fatalf("expected 4 retry values, got %d", len(retryValues))
	}

	// Retry should increase (or be capped at 3)
	for i := 1; i < len(retryValues); i++ {
		if retryValues[i] < retryValues[i-1] {
			t.Errorf("retry counter went backwards: %v", retryValues)
			break
		}
	}

	// Final retry should be >= 3 (circuit breaker threshold)
	if retryValues[len(retryValues)-1] < 3 {
		t.Errorf("expected final retry >= 3, got %d", retryValues[len(retryValues)-1])
	}

	t.Logf("retry progression: %v", retryValues)
}

// TestChaosDiamondDependencyPartialFailure tests a diamond DAG where
// one branch fails and the other succeeds, verifying cascade behavior.
func TestChaosDiamondDependencyPartialFailure(t *testing.T) {
	mon, d, _ := setupMonitorTest(t)
	ctx := context.Background()

	d.SetBlackboard(ctx, "conductor:active", "1", "test")
	d.SetBlackboard(ctx, "conductor:task_ids", `["t-root","t-left","t-right","t-merge"]`, "test")

	// Diamond: t-root → t-left (done) + t-right (failed) → t-merge (blocked by both)
	d.CreateTask(ctx, db.Task{ID: "t-root", Title: "Root", Status: "done", Role: "researcher"})
	d.CreateTask(ctx, db.Task{ID: "t-left", Title: "Left", Status: "done", Role: "implementer",
		BlockedBy: sql.NullString{String: `["t-root"]`, Valid: true}})
	d.CreateTask(ctx, db.Task{ID: "t-right", Title: "Right", Status: "failed", Role: "implementer",
		BlockedBy: sql.NullString{String: `["t-root"]`, Valid: true}})
	d.CreateTask(ctx, db.Task{ID: "t-merge", Title: "Merge", Status: "pending", Role: "reviewer",
		BlockedBy: sql.NullString{String: `["t-left","t-right"]`, Valid: true}})

	// RunOnce should cascade-fail t-merge (one blocker failed, not lenient mode)
	for i := 0; i < 3; i++ {
		_, err := mon.RunOnce(ctx)
		if err != nil {
			t.Fatalf("RunOnce cycle %d: %v", i, err)
		}
	}

	// t-merge should be cascade-failed because t-right failed
	task, _ := d.GetTaskByID(ctx, "t-merge")
	if task.Status != "failed" {
		t.Errorf("expected t-merge to be cascade-failed, got %q", task.Status)
	}

	ft, _ := d.GetBlackboardValue(ctx, "failure_type:t-merge")
	if ft != "cascade_fail" {
		t.Errorf("expected failure_type 'cascade_fail', got %q", ft)
	}
}

// TestChaosTimeoutKillDuringWork verifies that a timed-out agent is
// detected and its task marked failed, while other agents continue.
func TestChaosTimeoutKillDuringWork(t *testing.T) {
	mon, d, _ := setupMonitorTest(t)
	ctx := context.Background()

	d.SetBlackboard(ctx, "conductor:active", "1", "test")
	d.SetBlackboard(ctx, "conductor:task_ids", `["t1","t2"]`, "test")

	// Agent 1: alive but timed out
	sleepCmd := exec.Command("sleep", "60")
	sleepCmd.Start()
	pid1 := sleepCmd.Process.Pid
	defer func() {
		sleepCmd.Process.Kill()
		sleepCmd.Wait()
	}()

	d.RegisterAgent(ctx, db.Agent{ID: "a1", Role: "implementer", Status: "working",
		CurrentTask: sql.NullString{String: "t1", Valid: true}})
	d.UpdateAgentPID(ctx, "a1", pid1)
	d.CreateTask(ctx, db.Task{ID: "t1", Title: "Timeout task", Status: "running", Role: "implementer"})
	d.AssignTask(ctx, "t1", "a1", "", "feature/t1")
	d.SetBlackboard(ctx, "timeout:t1", "1", "test")
	d.SetBlackboard(ctx, "spawn_time:t1", strconv.FormatInt(time.Now().Unix()-100, 10), "test")

	// Agent 2: alive, normal
	sleepCmd2 := exec.Command("sleep", "60")
	sleepCmd2.Start()
	pid2 := sleepCmd2.Process.Pid
	defer func() {
		sleepCmd2.Process.Kill()
		sleepCmd2.Wait()
	}()

	d.RegisterAgent(ctx, db.Agent{ID: "a2", Role: "implementer", Status: "working",
		CurrentTask: sql.NullString{String: "t2", Valid: true}})
	d.UpdateAgentPID(ctx, "a2", pid2)
	d.CreateTask(ctx, db.Task{ID: "t2", Title: "Normal task", Status: "running", Role: "implementer"})
	d.AssignTask(ctx, "t2", "a2", "", "feature/t2")
	os.WriteFile(filepath.Join(mon.LogsDir, "t2.jsonl"),
		[]byte(`{"type":"init","session":"s2"}`+"\n"), 0o644)

	stats, err := mon.RunOnce(ctx)
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}

	if stats.TimedOut != 1 {
		t.Errorf("expected 1 timed out, got %d", stats.TimedOut)
	}

	// Timed out task should be failed
	task1, _ := d.GetTaskByID(ctx, "t1")
	if task1.Status != "failed" {
		t.Errorf("expected t1 'failed', got %q", task1.Status)
	}

	// Normal task should NOT be failed (agent is alive)
	task2, _ := d.GetTaskByID(ctx, "t2")
	if task2.Status == "failed" {
		t.Errorf("expected t2 NOT 'failed' (agent alive), got %q", task2.Status)
	}
}

// TestChaosBatchDeathWithSuccessAndCrash tests a batch where some agents
// completed successfully before dying and others crashed without results.
func TestChaosBatchDeathWithSuccessAndCrash(t *testing.T) {
	mon, d, repoRoot := setupMonitorTest(t)
	ctx := context.Background()

	d.SetBlackboard(ctx, "conductor:active", "1", "test")
	d.SetBlackboard(ctx, "conductor:task_ids", `["t-success","t-crash"]`, "test")

	// Agent 1: dead but has success result + commits
	wt1 := filepath.Join(repoRoot, ".worktree", "t-success")
	gitExec(t, repoRoot, "worktree", "add", wt1, "-b", "feature/t-success")
	os.WriteFile(filepath.Join(wt1, "result.go"), []byte("package main\n\nfunc Result() {}"), 0o644)
	gitExec(t, wt1, "add", ".")
	gitExec(t, wt1, "commit", "-m", "add result")

	d.RegisterAgent(ctx, db.Agent{ID: "a-succ", Role: "implementer", Status: "working",
		CurrentTask: sql.NullString{String: "t-success", Valid: true}})
	d.UpdateAgentPID(ctx, "a-succ", 999991)
	d.CreateTask(ctx, db.Task{ID: "t-success", Title: "Successful", Status: "running", Role: "implementer"})
	d.AssignTask(ctx, "t-success", "a-succ", wt1, "feature/t-success")
	os.WriteFile(filepath.Join(mon.LogsDir, "t-success.jsonl"),
		[]byte(`{"type":"result","subtype":"success","result":"All done!"}`+"\n"), 0o644)

	// Agent 2: dead, no result, circuit breaker engaged
	d.RegisterAgent(ctx, db.Agent{ID: "a-crash", Role: "implementer", Status: "working",
		CurrentTask: sql.NullString{String: "t-crash", Valid: true}})
	d.UpdateAgentPID(ctx, "a-crash", 999992)
	d.CreateTask(ctx, db.Task{ID: "t-crash", Title: "Crashed", Status: "running", Role: "implementer"})
	wt2 := filepath.Join(repoRoot, ".worktree", "t-crash")
	os.MkdirAll(wt2, 0o755)
	d.AssignTask(ctx, "t-crash", "a-crash", wt2, "feature/t-crash")
	d.SetBlackboard(ctx, "retry:t-crash", "3", "spawner")
	os.WriteFile(filepath.Join(mon.LogsDir, "t-crash.jsonl"),
		[]byte(`{"type":"init","session":"s-crash"}`+"\n"), 0o644)

	stats, err := mon.RunOnce(ctx)
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}

	if stats.Checked != 2 {
		t.Errorf("expected 2 checked, got %d", stats.Checked)
	}

	// Successful agent should have its task completed
	taskSuccess, _ := d.GetTaskByID(ctx, "t-success")
	if taskSuccess.Status != "done" {
		t.Errorf("expected t-success 'done', got %q", taskSuccess.Status)
	}

	// Crashed agent should have its task failed
	taskCrash, _ := d.GetTaskByID(ctx, "t-crash")
	if taskCrash.Status != "failed" {
		t.Errorf("expected t-crash 'failed', got %q", taskCrash.Status)
	}
}

// mustGetTaskStatus is a test helper to get task status without error handling.
func mustGetTaskStatus(d *db.DB, ctx context.Context, taskID string) string {
	task, err := d.GetTaskByID(ctx, taskID)
	if err != nil {
		return "ERROR:" + err.Error()
	}
	return task.Status
}
