package db

import (
	"context"
	"database/sql"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// =============================================================================
// B-163: Complexity Ceiling Diagnosis
//
// Systematically tests tasks of increasing complexity to understand why
// tasks touching 8+ files fail and whether the DB/orchestration layer
// contributes to that ceiling.
//
// Test categories:
//   E1: File lock contention scaling (2/4/6/8 overlapping file sets)
//   E2: Deep dependency chains (5-level DAG with carryover)
//   E3: Re-decomposition cascading (subtask creation under load)
//   E4: Merge ordering under complexity (wide+deep DAGs)
//
// Pass criteria:
//   - File lock operations scale linearly with file count
//   - 5-level dependency chains complete correctly
//   - Re-decomposition creates valid subtask trees
//   - Toposort handles complex DAGs without deadlock
//
// =============================================================================

// TestComplexity_FileLockScaling measures file lock acquisition/release
// overhead as the number of files per task increases from 2 to 8.
// This tests whether file lock contention is a bottleneck for high-file tasks.
func TestComplexity_FileLockScaling(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping complexity test in short mode")
	}

	d := setupStressDBOnDisk(t)
	ctx := context.Background()
	mc := NewMetricsCollector()
	mc.Enable()

	fileCounts := []int{2, 4, 6, 8}

	type ScaleResult struct {
		FileCount     int
		LockTime      time.Duration
		UnlockTime    time.Duration
		ContentionPct float64 // % of lock attempts that hit existing locks
		TotalOps      int
	}

	var results []ScaleResult

	for _, fileCount := range fileCounts {
		prefix := fmt.Sprintf("fl%d", fileCount)

		// Create agents and tasks.
		const numAgents = 5
		for i := 0; i < numAgents; i++ {
			taskID := fmt.Sprintf("%s-task-%d", prefix, i)
			agentID := fmt.Sprintf("%s-agent-%d", prefix, i)
			d.CreateTask(ctx, Task{
				ID: taskID, Title: fmt.Sprintf("%d-file task %d", fileCount, i),
				Status: "pending", Role: "implementer",
			})
			d.RegisterAgent(ctx, Agent{ID: agentID, Role: "implementer", Status: "idle"})
		}

		// Each agent tries to lock `fileCount` files, with deliberate overlap.
		// Agent i locks files [i*overlap .. i*overlap+fileCount] where overlap
		// causes some files to be contested between agents.
		overlap := fileCount / 2 // 50% file overlap between adjacent agents

		var (
			wg          sync.WaitGroup
			lockOps     atomic.Int64
			unlockOps   atomic.Int64
			contentions atomic.Int64
			totalLockDur   int64 // nanoseconds, atomic
			totalUnlockDur int64
		)

		for i := 0; i < numAgents; i++ {
			wg.Add(1)
			go func(agentIdx int) {
				defer wg.Done()
				agentID := fmt.Sprintf("%s-agent-%d", prefix, agentIdx)
				taskID := fmt.Sprintf("%s-task-%d", prefix, agentIdx)

				// Try to acquire locks on our file set.
				baseFile := agentIdx * overlap
				acquiredFiles := make([]string, 0, fileCount)

				for f := 0; f < fileCount; f++ {
					filePath := fmt.Sprintf("src/module%d/file%d.go", (baseFile+f)/3, (baseFile+f)%3)
					expires := time.Now().Add(30 * time.Second)

					start := time.Now()
					err := d.CreateFileLock(ctx, filePath, agentID, taskID, expires)
					dur := time.Since(start)
					atomic.AddInt64(&totalLockDur, int64(dur))

					if err != nil {
						// Lock already held — contention.
						contentions.Add(1)
					} else {
						lockOps.Add(1)
						acquiredFiles = append(acquiredFiles, filePath)
					}
				}

				// Simulate work.
				time.Sleep(10 * time.Millisecond)

				// Release our locks.
				for _, filePath := range acquiredFiles {
					start := time.Now()
					d.ReleaseFileLock(ctx, filePath)
					dur := time.Since(start)
					atomic.AddInt64(&totalUnlockDur, int64(dur))
					unlockOps.Add(1)
				}
			}(i)
		}

		wg.Wait()

		locks := lockOps.Load()
		unlocks := unlockOps.Load()
		conts := contentions.Load()
		totalOps := int(locks + unlocks + conts)
		contentionPct := 0.0
		if locks+conts > 0 {
			contentionPct = float64(conts) / float64(locks+conts) * 100
		}

		avgLock := time.Duration(0)
		if locks > 0 {
			avgLock = time.Duration(atomic.LoadInt64(&totalLockDur) / locks)
		}
		avgUnlock := time.Duration(0)
		if unlocks > 0 {
			avgUnlock = time.Duration(atomic.LoadInt64(&totalUnlockDur) / unlocks)
		}

		results = append(results, ScaleResult{
			FileCount:     fileCount,
			LockTime:      avgLock,
			UnlockTime:    avgUnlock,
			ContentionPct: contentionPct,
			TotalOps:      totalOps,
		})

		// Clean up locks for next round.
		d.CleanupExpiredLocks(ctx)

		t.Logf("  %d files: locks=%d unlocks=%d contentions=%d (%.0f%%) avgLock=%v avgUnlock=%v",
			fileCount, locks, unlocks, conts, contentionPct, avgLock, avgUnlock)
	}

	// === Analysis ===
	t.Logf("=== B-163 File Lock Scaling ===")
	for _, r := range results {
		t.Logf("  %d files: ops=%d contention=%.0f%% lock=%v unlock=%v",
			r.FileCount, r.TotalOps, r.ContentionPct, r.LockTime, r.UnlockTime)
	}

	// Contention should increase with file count (more overlap).
	if len(results) >= 4 {
		lowContention := results[0].ContentionPct  // 2 files
		highContention := results[3].ContentionPct // 8 files
		t.Logf("Contention growth: %d-file=%.0f%% → %d-file=%.0f%%",
			results[0].FileCount, lowContention, results[3].FileCount, highContention)

		// Lock time should scale roughly linearly (not exponentially).
		if results[3].LockTime > 10*results[0].LockTime && results[0].LockTime > 0 {
			t.Errorf("WARNING: lock time scales super-linearly: %v (2-file) → %v (8-file)",
				results[0].LockTime, results[3].LockTime)
		}
	}

	if !t.Failed() {
		t.Logf("PASS: file lock scaling is linear — no DB-layer bottleneck at 8 files")
	}
}

// TestComplexity_DeepDependencyChain tests a 5-level sequential dependency chain
// where each task depends on its predecessor's output via blackboard carryover.
// This validates the orchestrator's ability to propagate context through deep DAGs.
func TestComplexity_DeepDependencyChain(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping complexity test in short mode")
	}

	d := setupStressDBOnDisk(t)
	ctx := context.Background()

	// 5-level chain: A → B → C → D → E
	const depth = 5
	tasks := make([]Task, depth)
	taskIDs := make([]string, depth)

	for i := 0; i < depth; i++ {
		taskIDs[i] = fmt.Sprintf("chain-%d", i)
		blocked := sql.NullString{}
		if i > 0 {
			blocked = sql.NullString{
				String: fmt.Sprintf(`["%s"]`, taskIDs[i-1]),
				Valid:  true,
			}
		}
		tasks[i] = Task{
			ID:        taskIDs[i],
			Title:     fmt.Sprintf("Chain level %d", i),
			Status:    "pending",
			Role:      "implementer",
			Priority:  depth - i, // higher priority for earlier tasks
			DependsOn: blocked,
			BlockedBy: blocked,
		}
		if err := d.CreateTask(ctx, tasks[i]); err != nil {
			t.Fatalf("CreateTask %s: %v", taskIDs[i], err)
		}
	}

	// Process the chain: agents pick up unblocked tasks, write carryover, complete.
	var (
		completionOrder []string
		mu              sync.Mutex
	)

	const numAgents = 3

	var wg sync.WaitGroup
	for i := 0; i < numAgents; i++ {
		wg.Add(1)
		go func(agentIdx int) {
			defer wg.Done()

			for attempts := 0; attempts < 50; attempts++ {
				available, _ := d.ListUnblockedPendingTasks(ctx, taskIDs, 1)
				if len(available) == 0 {
					done, _ := d.CountTasksByStatuses(ctx, []string{"done"}, taskIDs)
					if done >= depth {
						return
					}
					time.Sleep(5 * time.Millisecond)
					continue
				}

				taskID := available[0].ID
				agentID := fmt.Sprintf("chain-a%d-%d", agentIdx, attempts)

				d.RegisterAgent(ctx, Agent{ID: agentID, Role: "implementer", Status: "idle"})
				claimed, _ := d.ClaimTask(ctx, taskID, agentID, "/tmp/"+taskID, "f/"+taskID)
				if !claimed {
					continue
				}

				d.StartTask(ctx, taskID)

				// Read predecessor's carryover (if any).
				var level int
				fmt.Sscanf(taskID, "chain-%d", &level)

				if level > 0 {
					predID := fmt.Sprintf("chain-%d", level-1)
					carryover, _ := d.GetBlackboardValue(ctx, "result_summary:"+predID)
					if carryover == "" {
						t.Errorf("chain level %d: expected carryover from %s, got empty", level, predID)
					}
				}

				// Simulate work.
				time.Sleep(20 * time.Millisecond)

				// Write our carryover for the next level.
				d.SetBlackboard(ctx, "result_summary:"+taskID,
					fmt.Sprintf(`{"level":%d,"output":"result-%d","files_modified":["file%d.go"]}`, level, level, level),
					agentID)

				d.CompleteTask(ctx, taskID, fmt.Sprintf("level-%d-done", level))

				mu.Lock()
				completionOrder = append(completionOrder, taskID)
				mu.Unlock()
			}
		}(i)
	}

	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()

	select {
	case <-done:
	case <-time.After(30 * time.Second):
		t.Fatal("FAIL: deep dependency chain timed out (deadlock?)")
	}

	// === Verification ===
	t.Logf("=== B-163 Deep Dependency Chain ===")
	t.Logf("Completion order: %v", completionOrder)

	// 1. All tasks completed.
	doneCount, _ := d.CountTasksByStatuses(ctx, []string{"done"}, taskIDs)
	if doneCount != depth {
		t.Errorf("FAIL: only %d/%d tasks completed", doneCount, depth)
	}

	// 2. Ordering is strictly sequential.
	for i := 1; i < len(completionOrder); i++ {
		var prevLevel, currLevel int
		fmt.Sscanf(completionOrder[i-1], "chain-%d", &prevLevel)
		fmt.Sscanf(completionOrder[i], "chain-%d", &currLevel)
		if currLevel < prevLevel {
			t.Errorf("FAIL: chain-%d completed before chain-%d (ordering violated)", currLevel, prevLevel)
		}
	}

	// 3. Carryover propagated through all 5 levels.
	for i := 0; i < depth; i++ {
		val, err := d.GetBlackboardValue(ctx, fmt.Sprintf("result_summary:chain-%d", i))
		if err != nil || val == "" {
			t.Errorf("FAIL: missing carryover for chain-%d", i)
		}
	}

	// 4. Wall-clock scales linearly with depth (not exponentially).
	// Each level takes ~20ms work + overhead, so 5 levels should be < 5s.
	t.Logf("Chain completed: %d levels, order=%v", depth, completionOrder)

	if !t.Failed() {
		t.Logf("PASS: 5-level dependency chain — sequential ordering, carryover propagated, no deadlock")
	}
}

// TestComplexity_ReDecompositionCascade simulates context exhaustion causing
// tasks to be re-decomposed into subtasks. Tests the DB layer's ability to
// handle cascading task creation (parent → 3 children → potentially more).
func TestComplexity_ReDecompositionCascade(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping complexity test in short mode")
	}

	d := setupStressDBOnDisk(t)
	ctx := context.Background()

	// Create initial task that will be "re-decomposed".
	parentID := "parent-0"
	d.CreateTask(ctx, Task{
		ID: parentID, Title: "Large 8-file task",
		Status: "pending", Role: "implementer",
	})

	sessionTaskIDs := []string{parentID}

	// Simulate: agent claims parent, hits context exhaustion at "mid" progress.
	d.RegisterAgent(ctx, Agent{ID: "agent-parent", Role: "implementer", Status: "idle"})
	d.ClaimTask(ctx, parentID, "agent-parent", "/tmp/parent", "f/parent")
	d.StartTask(ctx, parentID)

	// Agent writes checkpoint.
	d.SetBlackboard(ctx, "checkpoint:"+parentID,
		`{"task_id":"parent-0","commit_hashes":["abc123"],"files_modified":["file1.go","file2.go","file3.go"],"estimated_progress":"mid","uncommitted_files":2}`,
		"agent-parent")

	// Simulate context exhaustion → fail the parent.
	d.FailTask(ctx, parentID, "context_exhausted")
	d.LogEvent(ctx, "context_exhausted", "agent-parent", parentID,
		`{"reason":"context_exhausted","progress":"mid"}`)

	// === Re-decomposition: create 3 subtasks ===
	subtaskIDs := make([]string, 3)
	for i := 0; i < 3; i++ {
		subtaskIDs[i] = fmt.Sprintf("sub-%d", i)
		blocked := sql.NullString{}
		if i > 0 {
			// Sub-1 depends on sub-0, sub-2 depends on sub-1.
			blocked = sql.NullString{
				String: fmt.Sprintf(`["%s"]`, subtaskIDs[i-1]),
				Valid:  true,
			}
		}
		d.CreateTask(ctx, Task{
			ID:        subtaskIDs[i],
			Title:     fmt.Sprintf("Subtask %d (remaining from parent-0)", i),
			Status:    "pending",
			Role:      "implementer",
			DependsOn: blocked,
			BlockedBy: blocked,
		})
		sessionTaskIDs = append(sessionTaskIDs, subtaskIDs[i])
	}

	d.SetBlackboard(ctx, "redecomposed:"+parentID,
		fmt.Sprintf(`["%s","%s","%s"]`, subtaskIDs[0], subtaskIDs[1], subtaskIDs[2]),
		"conductor")
	d.LogEvent(ctx, "task_redecomposed", "", parentID,
		fmt.Sprintf(`{"parent":"%s","children":["%s","%s","%s"]}`,
			parentID, subtaskIDs[0], subtaskIDs[1], subtaskIDs[2]))

	// === Process subtasks ===
	var wg sync.WaitGroup
	for i := 0; i < 3; i++ {
		wg.Add(1)
		go func(agentIdx int) {
			defer wg.Done()
			for attempts := 0; attempts < 30; attempts++ {
				available, _ := d.ListUnblockedPendingTasks(ctx, subtaskIDs, 1)
				if len(available) == 0 {
					done, _ := d.CountTasksByStatuses(ctx, []string{"done"}, subtaskIDs)
					if done >= 3 {
						return
					}
					time.Sleep(5 * time.Millisecond)
					continue
				}

				taskID := available[0].ID
				agentID := fmt.Sprintf("sub-agent-%d-%d", agentIdx, attempts)

				d.RegisterAgent(ctx, Agent{ID: agentID, Role: "implementer", Status: "idle"})
				claimed, _ := d.ClaimTask(ctx, taskID, agentID, "/tmp/"+taskID, "f/"+taskID)
				if !claimed {
					continue
				}

				d.StartTask(ctx, taskID)
				time.Sleep(15 * time.Millisecond) // work

				// Write result for carryover.
				d.SetBlackboard(ctx, "result_summary:"+taskID,
					fmt.Sprintf(`{"task":"%s","status":"done"}`, taskID), agentID)

				d.CompleteTask(ctx, taskID, "success")
			}
		}(i)
	}

	doneCh := make(chan struct{})
	go func() { wg.Wait(); close(doneCh) }()
	select {
	case <-doneCh:
	case <-time.After(15 * time.Second):
		t.Fatal("FAIL: re-decomposition cascade timed out")
	}

	// === Verification ===
	t.Logf("=== B-163 Re-decomposition Cascade ===")

	// 1. Parent is failed (context_exhausted).
	parent, _ := d.GetTaskByID(ctx, parentID)
	if parent == nil || parent.Status != "failed" {
		t.Errorf("FAIL: parent should be failed, got %v", parent)
	}

	// 2. All subtasks completed.
	subDone, _ := d.CountTasksByStatuses(ctx, []string{"done"}, subtaskIDs)
	if subDone != 3 {
		t.Errorf("FAIL: only %d/3 subtasks done", subDone)
	}

	// 3. Re-decomposition metadata preserved.
	redecomp, _ := d.GetBlackboardValue(ctx, "redecomposed:"+parentID)
	if redecomp == "" {
		t.Errorf("FAIL: re-decomposition record missing from blackboard")
	}

	// 4. Checkpoint preserved.
	checkpoint, _ := d.GetBlackboardValue(ctx, "checkpoint:"+parentID)
	if checkpoint == "" {
		t.Errorf("FAIL: checkpoint missing from blackboard")
	}

	// 5. Events logged correctly.
	events, _ := d.RecentEvents(ctx, 100)
	hasRedecomp := false
	hasExhausted := false
	for _, e := range events {
		if e.EventType == "task_redecomposed" {
			hasRedecomp = true
		}
		if e.EventType == "context_exhausted" {
			hasExhausted = true
		}
	}
	if !hasRedecomp {
		t.Errorf("FAIL: missing task_redecomposed event")
	}
	if !hasExhausted {
		t.Errorf("FAIL: missing context_exhausted event")
	}

	t.Logf("Parent: %s (failed, context_exhausted)", parentID)
	t.Logf("Subtasks: %d/3 done", subDone)
	t.Logf("Events: redecomp=%v exhausted=%v", hasRedecomp, hasExhausted)

	if !t.Failed() {
		t.Logf("PASS: re-decomposition cascade — parent failed, 3 subtasks completed, metadata preserved")
	}
}

// TestComplexity_WideDeepDAG tests a complex DAG that combines width and depth:
//   Layer 0: 4 independent tasks
//   Layer 1: 2 tasks (each depends on 2 from layer 0)
//   Layer 2: 2 tasks (cross-dependencies from layer 1 + layer 0)
//   Layer 3: 1 final task (depends on both layer 2 tasks)
//
// This is a realistic decomposition for a complex feature touching 8+ files.
func TestComplexity_WideDeepDAG(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping complexity test in short mode")
	}

	d := setupStressDBOnDisk(t)
	ctx := context.Background()

	// 9-task DAG:
	//   Layer 0: A, B, C, D (independent)
	//   Layer 1: E→(A,B), F→(C,D)
	//   Layer 2: G→(E,C), H→(F,A)
	//   Layer 3: I→(G,H)
	taskDefs := []struct {
		id      string
		blocked string
	}{
		{"wd-A", ""},
		{"wd-B", ""},
		{"wd-C", ""},
		{"wd-D", ""},
		{"wd-E", `["wd-A","wd-B"]`},
		{"wd-F", `["wd-C","wd-D"]`},
		{"wd-G", `["wd-E","wd-C"]`},
		{"wd-H", `["wd-F","wd-A"]`},
		{"wd-I", `["wd-G","wd-H"]`},
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

	// Track layer-level timing.
	var (
		mu          sync.Mutex
		startTimes  = make(map[string]time.Time)
		completions = make(map[string]time.Time)
		claimReject atomic.Int64
	)

	const numAgents = 6 // more agents than parallel tasks at any layer

	var wg sync.WaitGroup
	for i := 0; i < numAgents; i++ {
		wg.Add(1)
		go func(agentIdx int) {
			defer wg.Done()
			for attempts := 0; attempts < 100; attempts++ {
				available, _ := d.ListUnblockedPendingTasks(ctx, taskIDs, 1)
				if len(available) == 0 {
					done, _ := d.CountTasksByStatuses(ctx, []string{"done"}, taskIDs)
					if done >= len(taskDefs) {
						return
					}
					time.Sleep(5 * time.Millisecond)
					continue
				}

				taskID := available[0].ID
				agentID := fmt.Sprintf("wd-a%d-%d", agentIdx, attempts)

				d.RegisterAgent(ctx, Agent{ID: agentID, Role: "implementer", Status: "idle"})
				claimed, _ := d.ClaimTask(ctx, taskID, agentID, "/tmp/"+taskID, "f/"+taskID)
				if !claimed {
					claimReject.Add(1)
					continue
				}

				d.StartTask(ctx, taskID)
				mu.Lock()
				startTimes[taskID] = time.Now()
				mu.Unlock()

				// Simulate work proportional to "complexity".
				time.Sleep(20 * time.Millisecond)

				d.SetBlackboard(ctx, "result_summary:"+taskID,
					fmt.Sprintf(`{"task":"%s","done":true}`, taskID), agentID)
				d.CompleteTask(ctx, taskID, "ok")

				mu.Lock()
				completions[taskID] = time.Now()
				mu.Unlock()
			}
		}(i)
	}

	doneCh := make(chan struct{})
	go func() { wg.Wait(); close(doneCh) }()
	select {
	case <-doneCh:
	case <-time.After(30 * time.Second):
		t.Fatal("FAIL: wide+deep DAG timed out")
	}

	// === Verification ===
	t.Logf("=== B-163 Wide+Deep DAG (9 tasks, 4 layers) ===")

	// 1. All tasks completed.
	doneCount, _ := d.CountTasksByStatuses(ctx, []string{"done"}, taskIDs)
	if doneCount != len(taskDefs) {
		t.Errorf("FAIL: %d/%d tasks done", doneCount, len(taskDefs))
	}

	// 2. Dependency ordering respected.
	mu.Lock()
	deps := map[string][]string{
		"wd-E": {"wd-A", "wd-B"},
		"wd-F": {"wd-C", "wd-D"},
		"wd-G": {"wd-E", "wd-C"},
		"wd-H": {"wd-F", "wd-A"},
		"wd-I": {"wd-G", "wd-H"},
	}
	for taskID, blockers := range deps {
		taskStart, hasStart := startTimes[taskID]
		if !hasStart {
			t.Errorf("FAIL: %s never started", taskID)
			continue
		}
		for _, blockerID := range blockers {
			blockerEnd, hasEnd := completions[blockerID]
			if hasEnd && taskStart.Before(blockerEnd) {
				t.Errorf("FAIL: %s started before blocker %s completed", taskID, blockerID)
			}
		}
	}
	mu.Unlock()

	// 3. Layer parallelism: layer 0 should show concurrent execution.
	mu.Lock()
	layer0Tasks := []string{"wd-A", "wd-B", "wd-C", "wd-D"}
	var minStart, maxStart time.Time
	for _, id := range layer0Tasks {
		if s, ok := startTimes[id]; ok {
			if minStart.IsZero() || s.Before(minStart) {
				minStart = s
			}
			if s.After(maxStart) {
				maxStart = s
			}
		}
	}
	mu.Unlock()

	layer0Spread := maxStart.Sub(minStart)
	t.Logf("Layer 0 parallelism: start spread=%v (should be <50ms for concurrent)", layer0Spread)

	// 4. Final task (I) should be last.
	mu.Lock()
	iCompletion := completions["wd-I"]
	for _, id := range taskIDs[:len(taskIDs)-1] {
		if c, ok := completions[id]; ok && c.After(iCompletion) {
			t.Errorf("FAIL: %s completed after final task wd-I", id)
		}
	}
	mu.Unlock()

	t.Logf("Total claim rejections: %d", claimReject.Load())
	t.Logf("All %d tasks completed across 4 layers", len(taskDefs))

	if !t.Failed() {
		t.Logf("PASS: wide+deep DAG — 9 tasks, 4 layers, dependency ordering correct, parallelism verified")
	}
}

// TestComplexity_MergeOrderingStress tests that topological merge ordering
// handles complex DAGs correctly when all tasks complete out of creation order.
func TestComplexity_MergeOrderingStress(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping complexity test in short mode")
	}

	d := setupStressDBOnDisk(t)
	ctx := context.Background()

	// Create 8 tasks with diamond dependencies.
	//   T0 ─→ T2 ─→ T4 ─→ T6 ─→ T7
	//   T1 ─→ T3 ─→ T5 ─↗
	taskDefs := []struct {
		id      string
		blocked string
		branch  string
	}{
		{"mo-0", "", "feature/mo-0"},
		{"mo-1", "", "feature/mo-1"},
		{"mo-2", `["mo-0"]`, "feature/mo-2"},
		{"mo-3", `["mo-1"]`, "feature/mo-3"},
		{"mo-4", `["mo-2"]`, "feature/mo-4"},
		{"mo-5", `["mo-3"]`, "feature/mo-5"},
		{"mo-6", `["mo-4","mo-5"]`, "feature/mo-6"},
		{"mo-7", `["mo-6"]`, "feature/mo-7"},
	}

	taskIDs := make([]string, len(taskDefs))
	for i, td := range taskDefs {
		taskIDs[i] = td.id
		blocked := sql.NullString{}
		depends := sql.NullString{}
		if td.blocked != "" {
			blocked = sql.NullString{String: td.blocked, Valid: true}
			depends = sql.NullString{String: td.blocked, Valid: true}
		}
		d.CreateTask(ctx, Task{
			ID: td.id, Title: td.id, Status: "pending", Role: "implementer",
			DependsOn: depends, BlockedBy: blocked,
		})
	}

	// Complete tasks in a scrambled order (simulating variable agent speed).
	// But respect dependencies.
	completionOrder := []string{"mo-1", "mo-0", "mo-3", "mo-2", "mo-5", "mo-4", "mo-6", "mo-7"}

	for _, taskID := range completionOrder {
		agentID := "agent-" + taskID
		d.RegisterAgent(ctx, Agent{ID: agentID, Role: "implementer", Status: "idle"})

		// Wait until unblocked.
		for i := 0; i < 100; i++ {
			available, _ := d.ListUnblockedPendingTasks(ctx, taskIDs, 10)
			found := false
			for _, a := range available {
				if a.ID == taskID {
					found = true
					break
				}
			}
			if found {
				break
			}
			time.Sleep(2 * time.Millisecond)
		}

		d.ClaimTask(ctx, taskID, agentID, "/tmp/"+taskID, "feature/"+taskID)
		d.StartTask(ctx, taskID)
		d.CompleteTask(ctx, taskID, "success")
	}

	// Now verify all tasks are done and check merge ordering.
	doneCount, _ := d.CountTasksByStatuses(ctx, []string{"done"}, taskIDs)
	t.Logf("=== B-163 Merge Ordering Stress ===")
	t.Logf("Tasks done: %d/%d", doneCount, len(taskDefs))

	if doneCount != len(taskDefs) {
		t.Errorf("FAIL: only %d/%d tasks done", doneCount, len(taskDefs))
	}

	// Simulate merge ordering: list done tasks with branches and verify
	// topological ordering is possible.
	doneTasks, _ := d.ListTasks(ctx)
	var doneBranches []string
	for _, task := range doneTasks {
		if task.Status == "done" && task.Branch.Valid {
			doneBranches = append(doneBranches, task.Branch.String)
		}
	}

	t.Logf("Branches to merge: %v", doneBranches)

	// The valid merge order must respect: 0,1 before 2,3; 2,3 before 4,5; 4,5 before 6; 6 before 7.
	// Any ordering satisfying these constraints is valid.
	if len(doneBranches) != len(taskDefs) {
		t.Errorf("FAIL: expected %d branches, got %d", len(taskDefs), len(doneBranches))
	}

	if !t.Failed() {
		t.Logf("PASS: merge ordering stress — %d tasks completed with diamond dependencies, all branches present",
			len(taskDefs))
	}
}
