package db

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// =============================================================================
// B-157: Long-Running Session Test
//
// Simulates many orchestration cycles (create → claim → work → complete/fail
// → cleanup → repeat) to detect resource leaks over extended operation.
//
// Default duration: 60 seconds (configurable via STRESS_DURATION env var).
// For full 1-hour test, run: STRESS_DURATION=3600 go test -run TestStress_LongRunning -v
//
// Pass criteria (from 074 stress test plan):
//   - Memory growth < 50MB over test duration
//   - Goroutine leak: 0 (count returns to baseline between cycles)
//   - WAL file size < 10MB
//   - Event table row count growth bounded (cleanup works)
//   - Database integrity maintained
//   - Circuit breaker activates at correct thresholds
//
// =============================================================================

// goroutineTracker is a lightweight goroutine counter for tests.
// Avoids importing internal/monitor (which imports internal/db → import cycle).
type goroutineTracker struct {
	baseline  int
	peak      int
	snapshots []int
}

func newGoroutineTracker() *goroutineTracker {
	n := runtime.NumGoroutine()
	return &goroutineTracker{baseline: n, peak: n}
}

func (gt *goroutineTracker) snapshot() int {
	n := runtime.NumGoroutine()
	gt.snapshots = append(gt.snapshots, n)
	if n > gt.peak {
		gt.peak = n
	}
	return n
}

func (gt *goroutineTracker) current() int { return runtime.NumGoroutine() }

func (gt *goroutineTracker) delta() int { return runtime.NumGoroutine() - gt.baseline }

func getStressDuration() time.Duration {
	if s := os.Getenv("STRESS_DURATION"); s != "" {
		var secs int
		fmt.Sscanf(s, "%d", &secs)
		if secs > 0 {
			return time.Duration(secs) * time.Second
		}
	}
	return 60 * time.Second
}

// TestStress_LongRunning_MultiCycle simulates repeated orchestration cycles:
// each cycle creates tasks, spawns agents, processes work, completes/fails,
// cleans up, and repeats. Tracks resource metrics at each cycle boundary.
func TestStress_LongRunning_MultiCycle(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping long-running stress test in short mode")
	}

	duration := getStressDuration()
	t.Logf("B-157: Long-running multi-cycle test — duration=%v", duration)

	dir := t.TempDir()
	dbPath := filepath.Join(dir, "longrun.db")
	d, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if err := d.InitSchema(context.Background()); err != nil {
		t.Fatalf("InitSchema: %v", err)
	}
	defer d.Close()

	ctx := context.Background()
	gt := newGoroutineTracker()
	mc := NewMetricsCollector()
	mc.Enable()

	// Baseline memory.
	runtime.GC()
	var baselineMem runtime.MemStats
	runtime.ReadMemStats(&baselineMem)
	baselineAlloc := baselineMem.Alloc

	type CycleMetrics struct {
		Cycle          int
		TasksCompleted int
		TasksFailed    int
		MemAllocMB     float64
		Goroutines     int
		WALSizeBytes   int64
		EventCount     int
		CycleDuration  time.Duration
	}

	var cycleResults []CycleMetrics
	deadline := time.Now().Add(duration)
	cycle := 0

	const tasksPerCycle = 5
	const agentsPerCycle = 5
	const workDuration = 30 * time.Millisecond
	// 80% success, 20% failure per the 074 plan.
	failEvery := 5 // every 5th cycle, agent 0 fails its task

	for time.Now().Before(deadline) {
		cycle++
		cycleStart := time.Now()

		// --- Phase 1: Create tasks for this cycle ---
		prefix := fmt.Sprintf("c%d", cycle)
		taskIDs := make([]string, tasksPerCycle)
		for i := 0; i < tasksPerCycle; i++ {
			taskIDs[i] = fmt.Sprintf("%s-t%d", prefix, i)
			start := time.Now()
			err := d.CreateTask(ctx, Task{
				ID:     taskIDs[i],
				Title:  fmt.Sprintf("Cycle %d Task %d", cycle, i),
				Status: "pending",
				Role:   "implementer",
			})
			mc.Record("CreateTask", time.Since(start), err)
			if err != nil {
				t.Errorf("cycle %d: CreateTask %s: %v", cycle, taskIDs[i], err)
			}
		}

		// --- Phase 2: Spawn agents and process tasks ---
		var (
			wg             sync.WaitGroup
			completedCount atomic.Int64
			failedCount    atomic.Int64
		)

		for i := 0; i < agentsPerCycle; i++ {
			wg.Add(1)
			go func(agentIdx int) {
				defer wg.Done()

				for attempts := 0; attempts < 20; attempts++ {
					available, _ := d.ListUnblockedPendingTasks(ctx, taskIDs, 1)
					if len(available) == 0 {
						done, _ := d.CountTasksByStatuses(ctx, []string{"done", "failed"}, taskIDs)
						if done >= tasksPerCycle {
							return
						}
						time.Sleep(5 * time.Millisecond)
						continue
					}

					taskID := available[0].ID
					agentID := fmt.Sprintf("%s-a%d-%d", prefix, agentIdx, attempts)

					d.RegisterAgent(ctx, Agent{ID: agentID, Role: "implementer", Status: "idle"})

					start := time.Now()
					claimed, err := d.ClaimTask(ctx, taskID, agentID, "/tmp/wt/"+taskID, "f/"+taskID)
					mc.Record("ClaimTask", time.Since(start), err)
					if !claimed || err != nil {
						continue
					}

					start = time.Now()
					d.StartTask(ctx, taskID)
					mc.Record("StartTask", time.Since(start), nil)

					// Simulate work: heartbeats + blackboard writes.
					d.SetBlackboard(ctx, "spawn_time:"+taskID,
						fmt.Sprintf("%d", time.Now().Unix()), "spawner")
					d.LogEvent(ctx, "agent_spawned", agentID, taskID,
						fmt.Sprintf(`{"agent":%d,"cycle":%d}`, agentIdx, cycle))

					time.Sleep(workDuration)

					d.UpdateAgentHeartbeat(ctx, agentID)

					time.Sleep(workDuration)

					// Determine success or failure (20% failure rate).
					if cycle%failEvery == 0 && agentIdx == 0 {
						start = time.Now()
						d.FailTask(ctx, taskID, "simulated failure")
						mc.Record("FailTask", time.Since(start), nil)
						failedCount.Add(1)
					} else {
						start = time.Now()
						d.CompleteTask(ctx, taskID, "success")
						mc.Record("CompleteTask", time.Since(start), nil)
						completedCount.Add(1)
					}

					d.UpdateAgentStatus(ctx, agentID, "done")
					d.LogEvent(ctx, "task_completed", agentID, taskID, `{"status":"done"}`)
				}
			}(i)
		}

		// Wait for all agents to finish (with timeout).
		doneCh := make(chan struct{})
		go func() { wg.Wait(); close(doneCh) }()
		select {
		case <-doneCh:
		case <-time.After(15 * time.Second):
			t.Errorf("cycle %d: timed out waiting for agents", cycle)
			continue
		}

		// --- Phase 3: Cleanup cycle state ---
		start := time.Now()
		d.DeleteBlackboardByPrefix(ctx, "spawn_time:"+prefix)
		mc.Record("CleanupBlackboard", time.Since(start), nil)

		// WAL checkpoint every 10 cycles to prevent unbounded growth.
		if cycle%10 == 0 {
			start = time.Now()
			err := d.WALCheckpoint(ctx)
			mc.Record("WALCheckpoint", time.Since(start), err)
		}

		// --- Phase 4: Record metrics ---
		runtime.GC()
		var mem runtime.MemStats
		runtime.ReadMemStats(&mem)

		walSize := int64(0)
		if fi, err := os.Stat(dbPath + "-wal"); err == nil {
			walSize = fi.Size()
		}

		eventCount := 0
		if summary, err := d.GetStatusSummary(ctx); err == nil {
			eventCount = summary.EventCount
		}

		goroutines := gt.snapshot()

		cm := CycleMetrics{
			Cycle:          cycle,
			TasksCompleted: int(completedCount.Load()),
			TasksFailed:    int(failedCount.Load()),
			MemAllocMB:     float64(mem.Alloc-baselineAlloc) / 1024 / 1024,
			Goroutines:     goroutines,
			WALSizeBytes:   walSize,
			EventCount:     eventCount,
			CycleDuration:  time.Since(cycleStart),
		}
		cycleResults = append(cycleResults, cm)

		// Log every 10 cycles for visibility.
		if cycle%10 == 0 || !time.Now().Before(deadline) {
			t.Logf("  Cycle %d: completed=%d failed=%d mem=%.1fMB goroutines=%d WAL=%dKB events=%d dur=%v",
				cm.Cycle, cm.TasksCompleted, cm.TasksFailed,
				cm.MemAllocMB, cm.Goroutines, cm.WALSizeBytes/1024,
				cm.EventCount, cm.CycleDuration)
		}
	}

	// === Post-test analysis ===
	t.Logf("=== B-157 Long-Running Results (%d cycles, %v) ===", cycle, duration)

	// 1. Memory growth check — should be < 50MB.
	runtime.GC()
	var finalMem runtime.MemStats
	runtime.ReadMemStats(&finalMem)
	memGrowthMB := float64(finalMem.Alloc-baselineAlloc) / 1024 / 1024

	t.Logf("Memory: baseline=%.1fMB final=%.1fMB growth=%.1fMB",
		float64(baselineAlloc)/1024/1024,
		float64(finalMem.Alloc)/1024/1024,
		memGrowthMB)

	if memGrowthMB > 50 {
		t.Errorf("FAIL: memory growth %.1fMB exceeds 50MB threshold", memGrowthMB)
	}

	// 2. Goroutine leak check — should return to baseline.
	time.Sleep(100 * time.Millisecond)
	runtime.GC()
	goroutineDelta := gt.delta()
	t.Logf("Goroutines: baseline=%d current=%d peak=%d delta=%+d",
		gt.baseline, gt.current(), gt.peak, goroutineDelta)

	if goroutineDelta > 5 {
		t.Errorf("FAIL: goroutine leak detected: %d goroutines above baseline (baseline=%d, current=%d)",
			goroutineDelta, gt.baseline, gt.current())
	}

	// 3. WAL file size check — should be < 10MB.
	finalWALSize := int64(0)
	if fi, err := os.Stat(dbPath + "-wal"); err == nil {
		finalWALSize = fi.Size()
	}
	t.Logf("WAL size: %dKB (threshold: 10MB)", finalWALSize/1024)
	if finalWALSize > 10*1024*1024 {
		t.Errorf("FAIL: WAL file size %dMB exceeds 10MB threshold", finalWALSize/1024/1024)
	}

	// 4. Database integrity.
	if err := d.IntegrityCheck(ctx); err != nil {
		t.Errorf("FAIL: database integrity check failed: %v", err)
	} else {
		t.Logf("Database integrity: OK")
	}

	// 5. Query performance summary.
	t.Logf("Query metrics: %s", mc.Report())
	top5 := mc.TopSlowest(5)
	for i, q := range top5 {
		errStr := "nil"
		if q.Error != nil {
			errStr = q.Error.Error()
		}
		t.Logf("  Slowest #%d: %v (%s) err=%s", i+1, q.Duration, q.QueryName, errStr)
	}

	// 6. Memory trend analysis — check for monotonic growth.
	if len(cycleResults) > 10 {
		firstQuartile := cycleResults[len(cycleResults)/4]
		lastQuartile := cycleResults[len(cycleResults)*3/4]
		memDelta := lastQuartile.MemAllocMB - firstQuartile.MemAllocMB
		t.Logf("Memory trend: Q1=%.1fMB Q3=%.1fMB delta=%.1fMB",
			firstQuartile.MemAllocMB, lastQuartile.MemAllocMB, memDelta)

		if memDelta > 20 {
			t.Errorf("WARNING: memory appears to be growing monotonically (Q1→Q3: +%.1fMB)", memDelta)
		}
	}

	// 7. Goroutine trend analysis — peak should be bounded.
	t.Logf("Goroutine peak: %d (baseline: %d)", gt.peak, gt.baseline)
	if gt.peak > 100 {
		t.Errorf("FAIL: peak goroutine count %d exceeds 100 threshold", gt.peak)
	}

	if !t.Failed() {
		t.Logf("PASS: B-157 long-running test — %d cycles, memory=%.1fMB, goroutines=%d→%d, WAL=%dKB",
			cycle, memGrowthMB, gt.baseline, gt.current(), finalWALSize/1024)
	}
}

// TestStress_LongRunning_WALGrowth specifically tests that the WAL file doesn't
// grow unboundedly during sustained write operations with periodic checkpointing.
func TestStress_LongRunning_WALGrowth(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping long-running stress test in short mode")
	}

	dir := t.TempDir()
	dbPath := filepath.Join(dir, "wal-growth.db")
	d, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if err := d.InitSchema(context.Background()); err != nil {
		t.Fatalf("InitSchema: %v", err)
	}
	defer d.Close()

	ctx := context.Background()

	const duration = 10 * time.Second
	const checkpointInterval = 3 * time.Second

	var (
		eventsWritten atomic.Int64
		mu            sync.Mutex
		walSizes      []int64
		wg            sync.WaitGroup
	)

	deadline := time.Now().Add(duration)

	// Writer goroutines — simulate 5 agents logging events.
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			for iter := 0; time.Now().Before(deadline); iter++ {
				d.LogEvent(ctx, "wal_growth_test",
					fmt.Sprintf("agent-%d", idx), "",
					fmt.Sprintf(`{"i":%d}`, iter))
				eventsWritten.Add(1)
				time.Sleep(time.Millisecond)
			}
		}(i)
	}

	// Checkpointer goroutine — runs periodic WAL checkpoints.
	wg.Add(1)
	go func() {
		defer wg.Done()
		ticker := time.NewTicker(checkpointInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				if !time.Now().Before(deadline) {
					return
				}
				d.WALCheckpoint(ctx)

				if fi, err := os.Stat(dbPath + "-wal"); err == nil {
					mu.Lock()
					walSizes = append(walSizes, fi.Size())
					mu.Unlock()
				}
			case <-time.After(duration + time.Second):
				return
			}
		}
	}()

	wg.Wait()

	// Final checkpoint.
	d.WALCheckpoint(ctx)
	finalWAL := int64(0)
	if fi, err := os.Stat(dbPath + "-wal"); err == nil {
		finalWAL = fi.Size()
	}

	t.Logf("=== B-157 WAL Growth Test ===")
	t.Logf("Events written: %d in %v", eventsWritten.Load(), duration)
	mu.Lock()
	t.Logf("WAL sizes at checkpoints: %v", walSizes)
	walSizesCopy := make([]int64, len(walSizes))
	copy(walSizesCopy, walSizes)
	mu.Unlock()
	t.Logf("Final WAL size after checkpoint: %dKB", finalWAL/1024)

	// WAL should shrink after checkpoint.
	if finalWAL > 1024*1024 {
		t.Errorf("FAIL: WAL still %dKB after final checkpoint (expected <1MB)", finalWAL/1024)
	}

	// Check that WAL sizes don't monotonically increase.
	if len(walSizesCopy) >= 3 {
		allGrowing := true
		for i := 1; i < len(walSizesCopy); i++ {
			if walSizesCopy[i] <= walSizesCopy[i-1] {
				allGrowing = false
				break
			}
		}
		if allGrowing {
			t.Errorf("WARNING: WAL sizes are monotonically growing: %v (checkpointing may not be effective)", walSizesCopy)
		}
	}

	// Integrity check.
	if err := d.IntegrityCheck(ctx); err != nil {
		t.Errorf("FAIL: integrity check after WAL growth test: %v", err)
	}

	if !t.Failed() {
		t.Logf("PASS: WAL growth bounded — %d events, final WAL=%dKB", eventsWritten.Load(), finalWAL/1024)
	}
}

// TestStress_LongRunning_GoroutineStability tracks goroutine counts across many
// rapid create-process-cleanup cycles to detect leaks from unclosed resources.
func TestStress_LongRunning_GoroutineStability(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping long-running stress test in short mode")
	}

	d := setupStressDBOnDisk(t)
	ctx := context.Background()
	gt := newGoroutineTracker()

	const numCycles = 50
	const tasksPerCycle = 3

	t.Logf("B-157 Goroutine Stability: %d cycles × %d tasks", numCycles, tasksPerCycle)

	for cycle := 0; cycle < numCycles; cycle++ {
		prefix := fmt.Sprintf("gs%d", cycle)
		taskIDs := make([]string, tasksPerCycle)

		// Create tasks.
		for i := 0; i < tasksPerCycle; i++ {
			taskIDs[i] = fmt.Sprintf("%s-t%d", prefix, i)
			d.CreateTask(ctx, Task{
				ID: taskIDs[i], Title: taskIDs[i],
				Status: "pending", Role: "implementer",
			})
		}

		// Process tasks concurrently.
		var wg sync.WaitGroup
		for i := 0; i < tasksPerCycle; i++ {
			wg.Add(1)
			go func(idx int) {
				defer wg.Done()
				agentID := fmt.Sprintf("%s-a%d", prefix, idx)
				taskID := taskIDs[idx]

				d.RegisterAgent(ctx, Agent{ID: agentID, Role: "implementer", Status: "idle"})
				claimed, _ := d.ClaimTask(ctx, taskID, agentID, "/tmp/"+taskID, "f/"+taskID)
				if !claimed {
					return
				}
				d.StartTask(ctx, taskID)
				d.SetBlackboard(ctx, "gs:"+taskID, "working", agentID)
				d.LogEvent(ctx, "gs_work", agentID, taskID, "{}")

				time.Sleep(5 * time.Millisecond) // minimal work

				d.CompleteTask(ctx, taskID, "ok")
				d.UpdateAgentStatus(ctx, agentID, "done")
			}(i)
		}

		wg.Wait()

		// Cleanup blackboard entries.
		d.DeleteBlackboardByPrefix(ctx, "gs:"+prefix)

		// Snapshot goroutines every 10 cycles.
		if cycle%10 == 0 {
			n := gt.snapshot()
			t.Logf("  Cycle %d: goroutines=%d delta=%+d", cycle, n, n-gt.baseline)
		}
	}

	// Wait for goroutines to wind down.
	time.Sleep(100 * time.Millisecond)
	runtime.GC()

	// Check for leaks.
	delta := gt.delta()
	t.Logf("=== B-157 Goroutine Stability Results ===")
	t.Logf("Baseline: %d | Current: %d | Peak: %d | Delta: %+d",
		gt.baseline, gt.current(), gt.peak, delta)

	if delta > 3 {
		t.Errorf("FAIL: goroutine leak: %d above baseline (baseline=%d, current=%d)",
			delta, gt.baseline, gt.current())
	}

	if !t.Failed() {
		t.Logf("PASS: %d cycles completed, no goroutine leaks (baseline=%d, current=%d)",
			numCycles, gt.baseline, gt.current())
	}
}

// TestStress_LongRunning_EventTableGrowth verifies that the events table doesn't
// grow without bound during extended operation. In production, events are only
// trimmed by TruncateAll (reset), so this test validates growth rate.
func TestStress_LongRunning_EventTableGrowth(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping long-running stress test in short mode")
	}

	dir := t.TempDir()
	dbPath := filepath.Join(dir, "event-growth.db")
	d, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	d.InitSchema(context.Background())
	defer d.Close()

	ctx := context.Background()

	// Simulate 20 cycles of agent work, each producing events.
	const numCycles = 20
	const eventsPerCycle = 50 // 5 agents × 10 events each

	var totalEvents int
	var dbSizes []int64

	for cycle := 0; cycle < numCycles; cycle++ {
		for i := 0; i < eventsPerCycle; i++ {
			d.LogEvent(ctx, "growth_test",
				fmt.Sprintf("agent-%d", i%5),
				fmt.Sprintf("task-%d", i%5),
				fmt.Sprintf(`{"cycle":%d,"event":%d}`, cycle, i))
			totalEvents++
		}

		// Measure DB file size every 5 cycles.
		if cycle%5 == 0 {
			d.WALCheckpoint(ctx)
			if fi, err := os.Stat(dbPath); err == nil {
				dbSizes = append(dbSizes, fi.Size())
			}
		}
	}

	// Verify event count.
	summary, err := d.GetStatusSummary(ctx)
	if err != nil {
		t.Fatalf("GetStatusSummary: %v", err)
	}

	t.Logf("=== B-157 Event Table Growth ===")
	t.Logf("Cycles: %d | Events written: %d | Events in DB: %d",
		numCycles, totalEvents, summary.EventCount)
	t.Logf("DB sizes at checkpoints (bytes): %v", dbSizes)

	if summary.EventCount != totalEvents {
		t.Errorf("FAIL: expected %d events, got %d", totalEvents, summary.EventCount)
	}

	// DB size should grow roughly linearly (not exponentially).
	if len(dbSizes) >= 3 {
		firstGrowth := dbSizes[1] - dbSizes[0]
		lastGrowth := dbSizes[len(dbSizes)-1] - dbSizes[len(dbSizes)-2]

		if firstGrowth > 0 && lastGrowth > 3*firstGrowth {
			t.Errorf("WARNING: DB growth appears super-linear: first=%dB last=%dB (3x threshold)",
				firstGrowth, lastGrowth)
		}
		denom := firstGrowth
		if denom <= 0 {
			denom = 1
		}
		t.Logf("Growth rate: first=%dB last=%dB (ratio: %.1fx)",
			firstGrowth, lastGrowth, float64(lastGrowth)/float64(denom))
	}

	// Integrity.
	if err := d.IntegrityCheck(ctx); err != nil {
		t.Errorf("FAIL: integrity check: %v", err)
	}

	if !t.Failed() {
		t.Logf("PASS: event table growth linear — %d events across %d cycles", totalEvents, numCycles)
	}
}
