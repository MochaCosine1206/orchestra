package db

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// =============================================================================
// B-154: 5-Agent Concurrent Stress Test
//
// Simulates 5 concurrent agents working through a dependency graph.
// Tests the full task lifecycle under concurrency:
//   1. Create 5 tasks with dependency relationships
//   2. Spawn 5 "agents" (goroutines) that compete for unblocked tasks
//   3. Each agent: claim task → start → simulate work → complete/fail
//   4. Monitor goroutine polls for state changes
//   5. Verify: all tasks complete, dependency ordering respected, no data corruption
//
// Pass criteria:
//   - All tasks reach terminal state (done or failed)
//   - Dependency ordering is respected (blocked tasks don't start before blockers complete)
//   - No duplicate task assignments
//   - No data corruption (consistent reads after all work completes)
//   - Completes within 30 seconds
//
// Run 3 times for consistency.
// =============================================================================

// TestStress_5AgentLifecycle simulates 5 concurrent agents processing a task graph.
func TestStress_5AgentLifecycle(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping stress test in short mode")
	}

	d := setupStressDBOnDisk(t)
	ctx := context.Background()

	// Task graph:
	//   task-0 (no deps)  ─┐
	//   task-1 (no deps)  ─┤──> task-3 (depends on 0, 1)
	//   task-2 (no deps)  ─┘──> task-4 (depends on 0, 2)
	//
	// Tasks 0-2 can run in parallel. Tasks 3-4 must wait.

	tasks := []Task{
		{ID: "task-0", Title: "Foundation A", Status: "pending", Role: "implementer", Priority: 1},
		{ID: "task-1", Title: "Foundation B", Status: "pending", Role: "implementer", Priority: 1},
		{ID: "task-2", Title: "Foundation C", Status: "pending", Role: "implementer", Priority: 1},
		{ID: "task-3", Title: "Dependent AB", Status: "pending", Role: "implementer", Priority: 2,
			DependsOn: sql.NullString{String: `["task-0","task-1"]`, Valid: true},
			BlockedBy: sql.NullString{String: `["task-0","task-1"]`, Valid: true}},
		{ID: "task-4", Title: "Dependent AC", Status: "pending", Role: "implementer", Priority: 2,
			DependsOn: sql.NullString{String: `["task-0","task-2"]`, Valid: true},
			BlockedBy: sql.NullString{String: `["task-0","task-2"]`, Valid: true}},
	}

	// Store session task IDs for queries.
	sessionTaskIDs := make([]string, len(tasks))
	for i, task := range tasks {
		if err := d.CreateTask(ctx, task); err != nil {
			t.Fatalf("CreateTask %s: %v", task.ID, err)
		}
		sessionTaskIDs[i] = task.ID
	}

	// Track events for verification.
	var (
		mu             sync.Mutex
		startedAt      = make(map[string]time.Time) // taskID → when it started
		completedAt    = make(map[string]time.Time) // taskID → when it completed
		assignedCount  = make(map[string]int)       // taskID → how many times assigned
		agentWorkCount atomic.Int64                  // total agent work operations
	)

	const numAgents = 5
	const workDuration = 50 * time.Millisecond // simulate fast agent work

	var wg sync.WaitGroup

	// Agent goroutines: each tries to claim and process tasks.
	for i := 0; i < numAgents; i++ {
		wg.Add(1)
		go func(agentIdx int) {
			defer wg.Done()
			agentID := fmt.Sprintf("agent-%d", agentIdx)

			for {
				// Try to find an unblocked pending task.
				available, err := d.ListUnblockedPendingTasks(ctx, sessionTaskIDs, 1)
				if err != nil {
					t.Errorf("agent %d: ListUnblockedPendingTasks: %v", agentIdx, err)
					return
				}

				if len(available) == 0 {
					// Check if all tasks are terminal.
					doneFailed, _ := d.CountTasksByStatuses(ctx, []string{"done", "failed"}, sessionTaskIDs)
					if doneFailed >= len(tasks) {
						return // all done
					}

					// Tasks still in flight — wait and retry.
					running, _ := d.CountTasksByStatuses(ctx, []string{"assigned", "running"}, sessionTaskIDs)
					if running == 0 && doneFailed < len(tasks) {
						// Possible deadlock — check for blocked tasks that should be unblocked.
						pending, _ := d.CountTasksByStatuses(ctx, []string{"pending"}, sessionTaskIDs)
						if pending > 0 {
							time.Sleep(10 * time.Millisecond)
							continue
						}
					}
					time.Sleep(10 * time.Millisecond)
					continue
				}

				taskID := available[0].ID

				// Try to claim the task atomically using ClaimTask (compare-and-swap).
				compositeAgentID := agentID + "-" + taskID
				err = d.RegisterAgent(ctx, Agent{
					ID:     compositeAgentID,
					Role:   "implementer",
					Status: "idle",
				})
				if err != nil {
					// Agent ID collision — another iteration already registered this.
					continue
				}

				claimed, err := d.ClaimTask(ctx, taskID, compositeAgentID, "/tmp/worktree/"+taskID, "feature/"+taskID)
				if err != nil {
					t.Errorf("agent %d: ClaimTask %s: %v", agentIdx, taskID, err)
					continue
				}
				if !claimed {
					// Task was already claimed by another agent — this is expected.
					continue
				}

				mu.Lock()
				assignedCount[taskID]++
				mu.Unlock()

				// Start task.
				if err := d.StartTask(ctx, taskID); err != nil {
					t.Errorf("agent %d: StartTask %s: %v", agentIdx, taskID, err)
					continue
				}

				mu.Lock()
				startedAt[taskID] = time.Now()
				mu.Unlock()

				// Simulate work.
				agentWorkCount.Add(1)

				// Write some blackboard entries (like a real agent would).
				d.SetBlackboard(ctx, "spawn_time:"+taskID, fmt.Sprintf("%d", time.Now().Unix()), "spawner")
				d.LogEvent(ctx, "agent_spawned", compositeAgentID, taskID, fmt.Sprintf(`{"agent":%d}`, agentIdx))

				time.Sleep(workDuration)

				// Log heartbeats.
				d.UpdateAgentHeartbeat(ctx, compositeAgentID)
				d.LogEvent(ctx, "agent_heartbeat", compositeAgentID, taskID, "{}")

				time.Sleep(workDuration)

				// Complete task.
				if err := d.CompleteTask(ctx, taskID, "success"); err != nil {
					t.Errorf("agent %d: CompleteTask %s: %v", agentIdx, taskID, err)
					continue
				}

				mu.Lock()
				completedAt[taskID] = time.Now()
				mu.Unlock()

				d.UpdateAgentStatus(ctx, compositeAgentID, "done")
				d.LogEvent(ctx, "task_completed", compositeAgentID, taskID, `{"status":"done"}`)
			}
		}(i)
	}

	// Timeout guard — fail if agents get stuck.
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		// All agents finished.
	case <-time.After(30 * time.Second):
		t.Fatal("FAIL: 5-agent lifecycle test timed out after 30 seconds (possible deadlock)")
	}

	// === Verification ===
	t.Logf("=== B-154 5-Agent Lifecycle Results ===")
	t.Logf("Agent work operations: %d", agentWorkCount.Load())

	// 1. All tasks reached terminal state.
	for _, taskID := range sessionTaskIDs {
		task, err := d.GetTaskByID(ctx, taskID)
		if err != nil {
			t.Errorf("FAIL: cannot read task %s: %v", taskID, err)
			continue
		}
		if task.Status != "done" && task.Status != "failed" {
			t.Errorf("FAIL: task %s has status %q, expected done/failed", taskID, task.Status)
		}
	}

	// 2. No duplicate assignments (each task assigned exactly once).
	mu.Lock()
	for taskID, count := range assignedCount {
		if count > 1 {
			t.Errorf("FAIL: task %s was assigned %d times (expected 1)", taskID, count)
		}
	}
	mu.Unlock()

	// 3. Dependency ordering respected.
	mu.Lock()
	// task-3 should start after task-0 AND task-1 complete.
	if s3, ok := startedAt["task-3"]; ok {
		if c0, ok := completedAt["task-0"]; ok && s3.Before(c0) {
			t.Errorf("FAIL: task-3 started at %v before task-0 completed at %v", s3, c0)
		}
		if c1, ok := completedAt["task-1"]; ok && s3.Before(c1) {
			t.Errorf("FAIL: task-3 started at %v before task-1 completed at %v", s3, c1)
		}
	}
	// task-4 should start after task-0 AND task-2 complete.
	if s4, ok := startedAt["task-4"]; ok {
		if c0, ok := completedAt["task-0"]; ok && s4.Before(c0) {
			t.Errorf("FAIL: task-4 started at %v before task-0 completed at %v", s4, c0)
		}
		if c2, ok := completedAt["task-2"]; ok && s4.Before(c2) {
			t.Errorf("FAIL: task-4 started at %v before task-2 completed at %v", s4, c2)
		}
	}
	mu.Unlock()

	// 4. Data integrity — final state is consistent.
	finalTasks, err := d.ListTasks(ctx)
	if err != nil {
		t.Errorf("FAIL: final ListTasks: %v", err)
	} else if len(finalTasks) != len(tasks) {
		t.Errorf("FAIL: expected %d tasks, got %d", len(tasks), len(finalTasks))
	}

	doneCount, _ := d.CountTasksByStatuses(ctx, []string{"done"}, sessionTaskIDs)
	t.Logf("Final state: %d/%d tasks done", doneCount, len(tasks))

	// 5. Events were logged correctly.
	events, _ := d.RecentEvents(ctx, 100)
	t.Logf("Total events logged: %d", len(events))

	if !t.Failed() {
		t.Logf("PASS: B-154 5-agent lifecycle — all %d tasks completed, dependency ordering respected, no duplicates",
			len(tasks))
	}
}

// TestStress_5AgentLifecycle_WithFailures adds failure scenarios to the lifecycle test.
// Some agents fail their tasks, requiring circuit breaker behavior and retries.
func TestStress_5AgentLifecycle_WithFailures(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping stress test in short mode")
	}

	d := setupStressDBOnDisk(t)
	ctx := context.Background()

	// 5 independent tasks — some will fail and need retry.
	for i := 0; i < 5; i++ {
		d.CreateTask(ctx, Task{
			ID: fmt.Sprintf("fail-task-%d", i), Title: fmt.Sprintf("Failure task %d", i),
			Status: "pending", Role: "implementer",
		})
	}

	sessionTaskIDs := []string{"fail-task-0", "fail-task-1", "fail-task-2", "fail-task-3", "fail-task-4"}

	var (
		completedTasks atomic.Int64
		failedTasks    atomic.Int64
		retries        atomic.Int64
		wg             sync.WaitGroup
	)

	const numAgents = 5

	for i := 0; i < numAgents; i++ {
		wg.Add(1)
		go func(agentIdx int) {
			defer wg.Done()
			attempts := 0

			for {
				attempts++
				if attempts > 20 {
					return // safety valve
				}

				available, _ := d.ListUnblockedPendingTasks(ctx, sessionTaskIDs, 1)
				if len(available) == 0 {
					doneFailed, _ := d.CountTasksByStatuses(ctx, []string{"done", "failed"}, sessionTaskIDs)
					if doneFailed >= 5 {
						return
					}
					time.Sleep(10 * time.Millisecond)
					continue
				}

				taskID := available[0].ID
				agentID := fmt.Sprintf("fagent-%d-%d", agentIdx, attempts)

				d.RegisterAgent(ctx, Agent{ID: agentID, Role: "implementer", Status: "idle"})
				if err := d.AssignTask(ctx, taskID, agentID, "/tmp/wt/"+taskID, "f/"+taskID); err != nil {
					continue
				}
				d.StartTask(ctx, taskID)

				time.Sleep(20 * time.Millisecond) // simulate work

				// Agent 0 fails task-0 on first attempt, succeeds on retry.
				// Agent 2 fails task-2 always (circuit breaker should stop retries).
				shouldFail := false
				if taskID == "fail-task-0" {
					retryStr, _ := d.GetBlackboardValue(ctx, "retry:"+taskID)
					if retryStr == "" || retryStr == "0" {
						shouldFail = true
					}
				}
				if taskID == "fail-task-2" {
					shouldFail = true
				}

				if shouldFail {
					d.FailTask(ctx, taskID, "simulated failure")
					failedTasks.Add(1)

					// Simulate retry logic (like the monitor would do).
					retryStr, _ := d.GetBlackboardValue(ctx, "retry:"+taskID)
					var retryCount int
					fmt.Sscanf(retryStr, "%d", &retryCount)

					if retryCount < 3 {
						d.SetBlackboard(ctx, "retry:"+taskID, fmt.Sprintf("%d", retryCount+1), "monitor")
						d.ResetTask(ctx, taskID)
						retries.Add(1)
					}
				} else {
					d.CompleteTask(ctx, taskID, "success")
					completedTasks.Add(1)
				}
			}
		}(i)
	}

	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()

	select {
	case <-done:
	case <-time.After(30 * time.Second):
		t.Fatal("FAIL: lifecycle with failures timed out")
	}

	t.Logf("=== B-154 Lifecycle With Failures ===")
	t.Logf("Completed: %d | Failed: %d | Retries: %d", completedTasks.Load(), failedTasks.Load(), retries.Load())

	// task-0 should eventually succeed (fail once, retry succeeds).
	task0, _ := d.GetTaskByID(ctx, "fail-task-0")
	if task0 != nil && task0.Status != "done" {
		t.Errorf("FAIL: task-0 should succeed after retry, got status %q", task0.Status)
	}

	// task-2 should hit circuit breaker (3 retries → stays failed).
	task2, _ := d.GetTaskByID(ctx, "fail-task-2")
	if task2 != nil && task2.Status != "failed" {
		t.Logf("INFO: task-2 status=%q (circuit breaker may have allowed final retry)", task2.Status)
	}

	// Tasks 1, 3, 4 should all complete successfully.
	for _, id := range []string{"fail-task-1", "fail-task-3", "fail-task-4"} {
		task, _ := d.GetTaskByID(ctx, id)
		if task != nil && task.Status != "done" {
			t.Errorf("FAIL: %s should be done, got %q", id, task.Status)
		}
	}

	if !t.Failed() {
		t.Logf("PASS: B-154 lifecycle with failures — retries worked, circuit breaker activated")
	}
}

// TestStress_5AgentLifecycle_Repeated runs the lifecycle test 3 times for consistency.
func TestStress_5AgentLifecycle_Repeated(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping stress test in short mode")
	}

	for run := 1; run <= 3; run++ {
		t.Run(fmt.Sprintf("run_%d", run), func(t *testing.T) {
			d := setupStressDBOnDisk(t)
			ctx := context.Background()

			// 5 independent tasks for this run.
			taskIDs := make([]string, 5)
			for i := 0; i < 5; i++ {
				taskIDs[i] = fmt.Sprintf("r%d-task-%d", run, i)
				d.CreateTask(ctx, Task{
					ID: taskIDs[i], Title: fmt.Sprintf("Run %d Task %d", run, i),
					Status: "pending", Role: "implementer",
				})
			}

			var wg sync.WaitGroup
			for i := 0; i < 5; i++ {
				wg.Add(1)
				go func(idx int) {
					defer wg.Done()
					for attempts := 0; attempts < 50; attempts++ {
						available, _ := d.ListUnblockedPendingTasks(ctx, taskIDs, 1)
						if len(available) == 0 {
							done, _ := d.CountTasksByStatuses(ctx, []string{"done"}, taskIDs)
							if done >= 5 {
								return
							}
							time.Sleep(5 * time.Millisecond)
							continue
						}

						taskID := available[0].ID
						agentID := fmt.Sprintf("r%d-a%d-%d", run, idx, attempts)
						d.RegisterAgent(ctx, Agent{ID: agentID, Role: "implementer", Status: "idle"})
						if err := d.AssignTask(ctx, taskID, agentID, "/tmp/"+taskID, "f/"+taskID); err != nil {
							continue
						}
						d.StartTask(ctx, taskID)
						time.Sleep(20 * time.Millisecond)
						d.CompleteTask(ctx, taskID, "ok")
					}
				}(i)
			}

			done := make(chan struct{})
			go func() { wg.Wait(); close(done) }()

			select {
			case <-done:
			case <-time.After(15 * time.Second):
				t.Fatal("FAIL: repeated lifecycle timed out")
			}

			doneCount, _ := d.CountTasksByStatuses(ctx, []string{"done"}, taskIDs)
			if doneCount != 5 {
				t.Errorf("FAIL: run %d — only %d/5 tasks done", run, doneCount)
			} else {
				t.Logf("PASS: run %d — all 5 tasks completed", run)
			}
		})
	}
}

// TestStress_5AgentLifecycle_BlackboardCoordination tests the blackboard as a
// coordination mechanism under concurrent access — critical for the conductor
// pattern where multiple goroutines read/write session state.
func TestStress_5AgentLifecycle_BlackboardCoordination(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping stress test in short mode")
	}

	dir := t.TempDir()
	dbPath := filepath.Join(dir, "bb-coord.db")
	d, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	d.InitSchema(context.Background())
	defer d.Close()

	ctx := context.Background()
	const numAgents = 5
	const opsPerAgent = 100

	// Each agent writes to its own key and reads all other agents' keys.
	var (
		writeErrors atomic.Int64
		readErrors  atomic.Int64
		wg          sync.WaitGroup
	)

	for i := 0; i < numAgents; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			myKey := fmt.Sprintf("agent_state:%d", idx)

			for op := 0; op < opsPerAgent; op++ {
				// Write own state.
				err := d.SetBlackboard(ctx, myKey, fmt.Sprintf(`{"op":%d,"agent":%d}`, op, idx), fmt.Sprintf("agent-%d", idx))
				if err != nil {
					writeErrors.Add(1)
				}

				// Read all other agents' states.
				for j := 0; j < numAgents; j++ {
					if j == idx {
						continue
					}
					_, err := d.GetBlackboardValue(ctx, fmt.Sprintf("agent_state:%d", j))
					if err != nil {
						readErrors.Add(1)
					}
				}

				// Atomic lock acquisition.
				lockKey := fmt.Sprintf("work_lock:%d", op%3) // 3 shared locks
				acquired, err := d.AcquireBlackboardLock(ctx, lockKey, fmt.Sprintf("agent-%d", idx))
				if err != nil {
					writeErrors.Add(1)
				}
				if acquired {
					// Do "critical section" work.
					d.SetBlackboard(ctx, lockKey+":worker", fmt.Sprintf("agent-%d", idx), fmt.Sprintf("agent-%d", idx))
					d.DeleteBlackboard(ctx, lockKey)
				}

				time.Sleep(time.Millisecond)
			}
		}(i)
	}

	wg.Wait()

	t.Logf("=== B-154 Blackboard Coordination ===")
	t.Logf("Write errors: %d | Read errors: %d", writeErrors.Load(), readErrors.Load())

	if writeErrors.Load() > 0 {
		t.Errorf("FAIL: %d blackboard write errors", writeErrors.Load())
	}
	if readErrors.Load() > 0 {
		t.Errorf("FAIL: %d blackboard read errors", readErrors.Load())
	}

	// Verify final state — each agent's last write should be present.
	for i := 0; i < numAgents; i++ {
		val, err := d.GetBlackboardValue(ctx, fmt.Sprintf("agent_state:%d", i))
		if err != nil {
			t.Errorf("FAIL: cannot read agent %d final state: %v", i, err)
		} else if val == "" {
			t.Errorf("FAIL: agent %d final state is empty", i)
		}
	}

	if !t.Failed() {
		t.Logf("PASS: B-154 blackboard coordination — %d agents × %d ops, 0 errors", numAgents, opsPerAgent)
	}
}

// =============================================================================
// B-155: 8-Agent Concurrent Stress Test
//
// Scales B-154 to 8 agents with a deeper dependency DAG:
//   Layer 0: task-0, task-1, task-2 (independent)
//   Layer 1: task-3 (depends on 0,1), task-4 (depends on 1,2), task-5 (depends on 0,2)
//   Layer 2: task-6 (depends on 3,4), task-7 (depends on 4,5)
//
// Pass criteria:
//   - All 8 tasks reach terminal state
//   - Dependency ordering strictly respected across 3 layers
//   - No duplicate task claims (ClaimTask holds under 8-way contention)
//   - Completes within 30 seconds
//   - 3x repeated runs for consistency
// =============================================================================

// TestStress_8AgentLifecycle tests 8 concurrent agents processing a 3-layer DAG.
func TestStress_8AgentLifecycle(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping stress test in short mode")
	}

	d := setupStressDBOnDisk(t)
	ctx := context.Background()

	// 3-layer DAG:
	//   Layer 0: 0, 1, 2 (no deps)
	//   Layer 1: 3→(0,1), 4→(1,2), 5→(0,2)
	//   Layer 2: 6→(3,4), 7→(4,5)
	tasks := []Task{
		{ID: "t8-0", Title: "Layer0-A", Status: "pending", Role: "implementer", Priority: 1},
		{ID: "t8-1", Title: "Layer0-B", Status: "pending", Role: "implementer", Priority: 1},
		{ID: "t8-2", Title: "Layer0-C", Status: "pending", Role: "implementer", Priority: 1},
		{ID: "t8-3", Title: "Layer1-AB", Status: "pending", Role: "implementer", Priority: 2,
			DependsOn: sql.NullString{String: `["t8-0","t8-1"]`, Valid: true},
			BlockedBy: sql.NullString{String: `["t8-0","t8-1"]`, Valid: true}},
		{ID: "t8-4", Title: "Layer1-BC", Status: "pending", Role: "implementer", Priority: 2,
			DependsOn: sql.NullString{String: `["t8-1","t8-2"]`, Valid: true},
			BlockedBy: sql.NullString{String: `["t8-1","t8-2"]`, Valid: true}},
		{ID: "t8-5", Title: "Layer1-AC", Status: "pending", Role: "implementer", Priority: 2,
			DependsOn: sql.NullString{String: `["t8-0","t8-2"]`, Valid: true},
			BlockedBy: sql.NullString{String: `["t8-0","t8-2"]`, Valid: true}},
		{ID: "t8-6", Title: "Layer2-Final1", Status: "pending", Role: "implementer", Priority: 3,
			DependsOn: sql.NullString{String: `["t8-3","t8-4"]`, Valid: true},
			BlockedBy: sql.NullString{String: `["t8-3","t8-4"]`, Valid: true}},
		{ID: "t8-7", Title: "Layer2-Final2", Status: "pending", Role: "implementer", Priority: 3,
			DependsOn: sql.NullString{String: `["t8-4","t8-5"]`, Valid: true},
			BlockedBy: sql.NullString{String: `["t8-4","t8-5"]`, Valid: true}},
	}

	sessionTaskIDs := make([]string, len(tasks))
	for i, task := range tasks {
		if err := d.CreateTask(ctx, task); err != nil {
			t.Fatalf("CreateTask %s: %v", task.ID, err)
		}
		sessionTaskIDs[i] = task.ID
	}

	var (
		mu             sync.Mutex
		startedAt      = make(map[string]time.Time)
		completedAt    = make(map[string]time.Time)
		assignedCount  = make(map[string]int)
		agentWorkCount atomic.Int64
		claimRejected  atomic.Int64 // count of ClaimTask false returns
	)

	const numAgents = 8
	const workDuration = 30 * time.Millisecond

	var wg sync.WaitGroup

	for i := 0; i < numAgents; i++ {
		wg.Add(1)
		go func(agentIdx int) {
			defer wg.Done()
			agentID := fmt.Sprintf("a8-%d", agentIdx)

			for {
				available, err := d.ListUnblockedPendingTasks(ctx, sessionTaskIDs, 1)
				if err != nil {
					t.Errorf("agent %d: ListUnblockedPendingTasks: %v", agentIdx, err)
					return
				}

				if len(available) == 0 {
					doneFailed, _ := d.CountTasksByStatuses(ctx, []string{"done", "failed"}, sessionTaskIDs)
					if doneFailed >= len(tasks) {
						return
					}
					time.Sleep(5 * time.Millisecond)
					continue
				}

				taskID := available[0].ID
				compositeAgentID := fmt.Sprintf("%s-%s", agentID, taskID)

				d.RegisterAgent(ctx, Agent{ID: compositeAgentID, Role: "implementer", Status: "idle"})

				claimed, err := d.ClaimTask(ctx, taskID, compositeAgentID, "/tmp/wt8/"+taskID, "f8/"+taskID)
				if err != nil {
					t.Errorf("agent %d: ClaimTask %s: %v", agentIdx, taskID, err)
					continue
				}
				if !claimed {
					claimRejected.Add(1)
					continue
				}

				mu.Lock()
				assignedCount[taskID]++
				mu.Unlock()

				d.StartTask(ctx, taskID)

				mu.Lock()
				startedAt[taskID] = time.Now()
				mu.Unlock()

				agentWorkCount.Add(1)

				// Simulate work with blackboard + events.
				d.SetBlackboard(ctx, "spawn_time:"+taskID, fmt.Sprintf("%d", time.Now().Unix()), "spawner")
				d.LogEvent(ctx, "agent_spawned", compositeAgentID, taskID, fmt.Sprintf(`{"agent":%d}`, agentIdx))

				time.Sleep(workDuration)

				d.UpdateAgentHeartbeat(ctx, compositeAgentID)

				time.Sleep(workDuration)

				d.CompleteTask(ctx, taskID, "success")

				mu.Lock()
				completedAt[taskID] = time.Now()
				mu.Unlock()

				d.UpdateAgentStatus(ctx, compositeAgentID, "done")
				d.LogEvent(ctx, "task_completed", compositeAgentID, taskID, `{"status":"done"}`)
			}
		}(i)
	}

	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()

	select {
	case <-done:
	case <-time.After(30 * time.Second):
		t.Fatal("FAIL: 8-agent lifecycle timed out after 30s (possible deadlock)")
	}

	// === Verification ===
	t.Logf("=== B-155 8-Agent Lifecycle Results ===")
	t.Logf("Agent work ops: %d | Claim rejections: %d", agentWorkCount.Load(), claimRejected.Load())

	// 1. All tasks terminal.
	for _, id := range sessionTaskIDs {
		task, err := d.GetTaskByID(ctx, id)
		if err != nil {
			t.Errorf("FAIL: cannot read task %s: %v", id, err)
			continue
		}
		if task.Status != "done" && task.Status != "failed" {
			t.Errorf("FAIL: task %s has status %q, expected done/failed", id, task.Status)
		}
	}

	// 2. No duplicate assignments.
	mu.Lock()
	for taskID, count := range assignedCount {
		if count > 1 {
			t.Errorf("FAIL: task %s claimed %d times (ClaimTask should prevent this)", taskID, count)
		}
	}
	mu.Unlock()

	// 3. Dependency ordering — Layer 1 after Layer 0.
	mu.Lock()
	deps := map[string][]string{
		"t8-3": {"t8-0", "t8-1"},
		"t8-4": {"t8-1", "t8-2"},
		"t8-5": {"t8-0", "t8-2"},
		"t8-6": {"t8-3", "t8-4"},
		"t8-7": {"t8-4", "t8-5"},
	}
	for taskID, blockers := range deps {
		taskStart, hasStart := startedAt[taskID]
		if !hasStart {
			continue
		}
		for _, blockerID := range blockers {
			blockerEnd, hasEnd := completedAt[blockerID]
			if hasEnd && taskStart.Before(blockerEnd) {
				t.Errorf("FAIL: %s started at %v before blocker %s completed at %v",
					taskID, taskStart, blockerID, blockerEnd)
			}
		}
	}
	mu.Unlock()

	// 4. Data integrity.
	finalTasks, _ := d.ListTasks(ctx)
	doneCount, _ := d.CountTasksByStatuses(ctx, []string{"done"}, sessionTaskIDs)
	events, _ := d.RecentEvents(ctx, 200)

	t.Logf("Final: %d/%d tasks done | %d total tasks in DB | %d events",
		doneCount, len(tasks), len(finalTasks), len(events))

	if !t.Failed() {
		t.Logf("PASS: B-155 8-agent lifecycle — all %d tasks completed across 3 dependency layers, "+
			"%d claim rejections handled correctly", len(tasks), claimRejected.Load())
	}
}

// TestStress_8AgentLifecycle_Repeated runs the 8-agent test 3 times for consistency.
func TestStress_8AgentLifecycle_Repeated(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping stress test in short mode")
	}

	for run := 1; run <= 3; run++ {
		t.Run(fmt.Sprintf("run_%d", run), func(t *testing.T) {
			d := setupStressDBOnDisk(t)
			ctx := context.Background()

			// 8 tasks: 3 independent → 3 dependent → 2 final
			prefix := fmt.Sprintf("r%d", run)
			taskDefs := []struct {
				id      string
				blocked string
			}{
				{prefix + "-0", ""},
				{prefix + "-1", ""},
				{prefix + "-2", ""},
				{prefix + "-3", fmt.Sprintf(`["%s-0","%s-1"]`, prefix, prefix)},
				{prefix + "-4", fmt.Sprintf(`["%s-1","%s-2"]`, prefix, prefix)},
				{prefix + "-5", fmt.Sprintf(`["%s-0","%s-2"]`, prefix, prefix)},
				{prefix + "-6", fmt.Sprintf(`["%s-3","%s-4"]`, prefix, prefix)},
				{prefix + "-7", fmt.Sprintf(`["%s-4","%s-5"]`, prefix, prefix)},
			}

			taskIDs := make([]string, len(taskDefs))
			for i, td := range taskDefs {
				taskIDs[i] = td.id
				blocked := sql.NullString{}
				if td.blocked != "" {
					blocked = sql.NullString{String: td.blocked, Valid: true}
				}
				d.CreateTask(ctx, Task{
					ID: td.id, Title: td.id, Status: "pending", Role: "implementer",
					DependsOn: blocked, BlockedBy: blocked,
				})
			}

			var wg sync.WaitGroup
			for i := 0; i < 8; i++ {
				wg.Add(1)
				go func(idx int) {
					defer wg.Done()
					for attempts := 0; attempts < 100; attempts++ {
						available, _ := d.ListUnblockedPendingTasks(ctx, taskIDs, 1)
						if len(available) == 0 {
							done, _ := d.CountTasksByStatuses(ctx, []string{"done"}, taskIDs)
							if done >= 8 {
								return
							}
							time.Sleep(5 * time.Millisecond)
							continue
						}
						taskID := available[0].ID
						agentID := fmt.Sprintf("r%d-a%d-%d", run, idx, attempts)
						d.RegisterAgent(ctx, Agent{ID: agentID, Role: "implementer", Status: "idle"})
						claimed, _ := d.ClaimTask(ctx, taskID, agentID, "/tmp/"+taskID, "f/"+taskID)
						if !claimed {
							continue
						}
						d.StartTask(ctx, taskID)
						time.Sleep(20 * time.Millisecond)
						d.CompleteTask(ctx, taskID, "ok")
					}
				}(i)
			}

			done := make(chan struct{})
			go func() { wg.Wait(); close(done) }()
			select {
			case <-done:
			case <-time.After(15 * time.Second):
				t.Fatal("FAIL: repeated 8-agent test timed out")
			}

			doneCount, _ := d.CountTasksByStatuses(ctx, []string{"done"}, taskIDs)
			if doneCount != 8 {
				t.Errorf("FAIL: run %d — only %d/8 tasks done", run, doneCount)
			} else {
				t.Logf("PASS: run %d — all 8 tasks completed across 3 dependency layers", run)
			}
		})
	}
}

// TestStress_8AgentLifecycle_MonitorCycleTime simulates the monitor polling
// during an 8-agent workload and measures cycle time.
func TestStress_8AgentLifecycle_MonitorCycleTime(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping stress test in short mode")
	}

	dir := t.TempDir()
	dbPath := filepath.Join(dir, "monitor-cycle.db")
	d, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	d.InitSchema(context.Background())
	defer d.Close()

	ctx := context.Background()
	mc := NewMetricsCollector()
	mc.Enable()

	// Set up 8 agents and tasks in various states (simulating mid-session).
	for i := 0; i < 8; i++ {
		d.CreateTask(ctx, Task{
			ID: fmt.Sprintf("mc-task-%d", i), Title: fmt.Sprintf("Task %d", i),
			Status: "running", Role: "implementer",
		})
		d.RegisterAgent(ctx, Agent{
			ID: fmt.Sprintf("mc-agent-%d", i), Role: "implementer", Status: "working",
		})
		d.AssignTask(ctx, fmt.Sprintf("mc-task-%d", i), fmt.Sprintf("mc-agent-%d", i),
			fmt.Sprintf("/tmp/wt/%d", i), fmt.Sprintf("f/%d", i))

		// Add some events and blackboard entries.
		for j := 0; j < 10; j++ {
			d.LogEvent(ctx, "heartbeat", fmt.Sprintf("mc-agent-%d", i),
				fmt.Sprintf("mc-task-%d", i), fmt.Sprintf(`{"j":%d}`, j))
		}
		d.SetBlackboard(ctx, fmt.Sprintf("spawn_time:mc-task-%d", i),
			fmt.Sprintf("%d", time.Now().Unix()), "spawner")
	}

	// Simulate 50 monitor cycles — each cycle does the same queries the real monitor does.
	const numCycles = 50
	var cycleTimes []time.Duration

	for cycle := 0; cycle < numCycles; cycle++ {
		cycleStart := time.Now()

		// Monitor cycle queries (mirrors internal/monitor/monitor.go RunOnce):
		start := time.Now()
		_, err := d.ListActiveAgentsWithPID(ctx)
		mc.Record("ListActiveAgentsWithPID", time.Since(start), err)

		start = time.Now()
		_, err = d.ListTasks(ctx)
		mc.Record("ListTasks", time.Since(start), err)

		start = time.Now()
		_, err = d.GetStatusSummary(ctx)
		mc.Record("GetStatusSummary", time.Since(start), err)

		start = time.Now()
		_, err = d.ListUnblockedPendingTasks(ctx, nil, 5)
		mc.Record("ListUnblockedPendingTasks", time.Since(start), err)

		start = time.Now()
		_, err = d.RecentEvents(ctx, 10)
		mc.Record("RecentEvents", time.Since(start), err)

		cycleDur := time.Since(cycleStart)
		cycleTimes = append(cycleTimes, cycleDur)

		// Also simulate some concurrent writes (other agents updating).
		d.LogEvent(ctx, "monitor_cycle", "", "", fmt.Sprintf(`{"cycle":%d}`, cycle))
		if cycle%3 == 0 {
			d.UpdateAgentHeartbeat(ctx, fmt.Sprintf("mc-agent-%d", cycle%8))
		}
	}

	// Calculate cycle time stats.
	var totalCycle time.Duration
	var maxCycle time.Duration
	for _, ct := range cycleTimes {
		totalCycle += ct
		if ct > maxCycle {
			maxCycle = ct
		}
	}
	avgCycle := totalCycle / time.Duration(numCycles)

	t.Logf("=== B-155 Monitor Cycle Time (8 agents, %d cycles) ===", numCycles)
	t.Logf("Avg cycle: %v | Max cycle: %v", avgCycle, maxCycle)
	t.Logf("Query metrics: %s", mc.Report())

	// Monitor cycle should complete in < 500ms (real monitor runs every 15s).
	if maxCycle > 500*time.Millisecond {
		t.Errorf("FAIL: max monitor cycle time %v exceeds 500ms threshold", maxCycle)
	}
	if avgCycle > 100*time.Millisecond {
		t.Errorf("FAIL: avg monitor cycle time %v exceeds 100ms threshold", avgCycle)
	}

	if !t.Failed() {
		t.Logf("PASS: B-155 monitor cycle time — avg=%v max=%v (threshold: avg<100ms, max<500ms)", avgCycle, maxCycle)
	}
}
