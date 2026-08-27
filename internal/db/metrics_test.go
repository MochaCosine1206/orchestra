package db

import (
	"context"
	"fmt"
	"runtime"
	"sync"
	"testing"
	"time"
)

// B-159: Memory Profiling + Goroutine Leak Detection (db package)
//
// Tests the MetricsCollector infrastructure and validates SQLite query
// performance profiling during multi-agent workloads.

// TestMetricsCollectorBasic verifies basic metrics recording and retrieval.
func TestMetricsCollectorBasic(t *testing.T) {
	mc := NewMetricsCollector()

	// Disabled by default — recording should be a no-op
	mc.Record("test_query", 10*time.Millisecond, nil)
	if mc.Count() != 0 {
		t.Errorf("expected 0 queries when disabled, got %d", mc.Count())
	}

	// Enable and record
	mc.Enable()
	mc.Record("query_1", 5*time.Millisecond, nil)
	mc.Record("query_2", 15*time.Millisecond, nil)
	mc.Record("query_3", 10*time.Millisecond, nil)

	if mc.Count() != 3 {
		t.Errorf("expected 3 queries, got %d", mc.Count())
	}
	if mc.ErrorCount() != 0 {
		t.Errorf("expected 0 errors, got %d", mc.ErrorCount())
	}
}

// TestMetricsCollectorPercentiles verifies P50 and P99 calculations.
func TestMetricsCollectorPercentiles(t *testing.T) {
	mc := NewMetricsCollector()
	mc.Enable()

	// Record 100 queries with known durations: 1ms, 2ms, ..., 100ms
	for i := 1; i <= 100; i++ {
		mc.Record(fmt.Sprintf("q%d", i), time.Duration(i)*time.Millisecond, nil)
	}

	p50 := mc.P50()
	p99 := mc.P99()

	// P50 should be around 50ms (index 49)
	if p50 < 45*time.Millisecond || p50 > 55*time.Millisecond {
		t.Errorf("expected P50 ~50ms, got %v", p50)
	}

	// P99 should be around 99ms (index 98)
	if p99 < 95*time.Millisecond || p99 > 100*time.Millisecond {
		t.Errorf("expected P99 ~99ms, got %v", p99)
	}

	t.Logf("P50=%v, P99=%v", p50, p99)
}

// TestMetricsCollectorBusyTimeout verifies SQLITE_BUSY error counting.
func TestMetricsCollectorBusyTimeout(t *testing.T) {
	mc := NewMetricsCollector()
	mc.Enable()

	mc.Record("success", 5*time.Millisecond, nil)
	mc.Record("busy1", 100*time.Millisecond, fmt.Errorf("database is locked"))
	mc.Record("busy2", 200*time.Millisecond, fmt.Errorf("SQLITE_BUSY (5): database is locked"))
	mc.Record("other_err", 10*time.Millisecond, fmt.Errorf("constraint failed"))

	if mc.BusyTimeoutCount() != 2 {
		t.Errorf("expected 2 busy timeouts, got %d", mc.BusyTimeoutCount())
	}
	if mc.ErrorCount() != 3 {
		t.Errorf("expected 3 total errors, got %d", mc.ErrorCount())
	}
}

// TestMetricsCollectorTopSlowest verifies slowest query retrieval.
func TestMetricsCollectorTopSlowest(t *testing.T) {
	mc := NewMetricsCollector()
	mc.Enable()

	mc.Record("fast", 1*time.Millisecond, nil)
	mc.Record("slow", 100*time.Millisecond, nil)
	mc.Record("medium", 50*time.Millisecond, nil)
	mc.Record("slowest", 200*time.Millisecond, nil)
	mc.Record("fast2", 2*time.Millisecond, nil)

	top3 := mc.TopSlowest(3)
	if len(top3) != 3 {
		t.Fatalf("expected 3 results, got %d", len(top3))
	}
	if top3[0].QueryName != "slowest" {
		t.Errorf("expected slowest first, got %q", top3[0].QueryName)
	}
	if top3[1].QueryName != "slow" {
		t.Errorf("expected slow second, got %q", top3[1].QueryName)
	}
	if top3[2].QueryName != "medium" {
		t.Errorf("expected medium third, got %q", top3[2].QueryName)
	}
}

// TestMetricsCollectorReset verifies Reset clears all recorded metrics.
func TestMetricsCollectorReset(t *testing.T) {
	mc := NewMetricsCollector()
	mc.Enable()

	mc.Record("q1", 5*time.Millisecond, nil)
	mc.Record("q2", 10*time.Millisecond, nil)

	if mc.Count() != 2 {
		t.Fatalf("expected 2 queries, got %d", mc.Count())
	}

	mc.Reset()

	if mc.Count() != 0 {
		t.Errorf("expected 0 queries after reset, got %d", mc.Count())
	}
}

// TestMetricsCollectorConcurrentAccess verifies thread safety.
func TestMetricsCollectorConcurrentAccess(t *testing.T) {
	mc := NewMetricsCollector()
	mc.Enable()

	var wg sync.WaitGroup
	const workers = 20
	const queriesPerWorker = 100

	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			for q := 0; q < queriesPerWorker; q++ {
				mc.Record(
					fmt.Sprintf("w%d-q%d", workerID, q),
					time.Duration(q)*time.Microsecond,
					nil,
				)
			}
		}(w)
	}
	wg.Wait()

	expected := workers * queriesPerWorker
	if mc.Count() != expected {
		t.Errorf("expected %d queries, got %d", expected, mc.Count())
	}

	// Percentiles should still work
	p50 := mc.P50()
	p99 := mc.P99()
	t.Logf("concurrent: %d queries, P50=%v, P99=%v", mc.Count(), p50, p99)
}

// TestMetricsCollectorReport verifies the Report output format.
func TestMetricsCollectorReport(t *testing.T) {
	mc := NewMetricsCollector()

	// Empty report
	report := mc.Report()
	if report != "No queries recorded" {
		t.Errorf("expected 'No queries recorded', got %q", report)
	}

	mc.Enable()
	mc.Record("q1", 5*time.Millisecond, nil)
	mc.Record("q2", 10*time.Millisecond, fmt.Errorf("database is locked"))

	report = mc.Report()
	t.Logf("report: %s", report)

	// Should contain key fields
	if !searchString(report, "Queries: 2") {
		t.Errorf("expected 'Queries: 2' in report, got %q", report)
	}
	if !searchString(report, "Errors: 1") {
		t.Errorf("expected 'Errors: 1' in report, got %q", report)
	}
}

// TestSQLiteQueryProfileDuringLifecycle profiles actual DB operations
// during a simulated multi-agent task lifecycle.
func TestSQLiteQueryProfileDuringLifecycle(t *testing.T) {
	d := setupStressDB(t)
	ctx := context.Background()

	mc := NewMetricsCollector()
	mc.Enable()

	// Simulate 5-agent lifecycle with manual profiling
	const agents = 5
	const cycles = 10

	for cycle := 0; cycle < cycles; cycle++ {
		for i := 0; i < agents; i++ {
			taskID := fmt.Sprintf("t-%d-%d", cycle, i)
			agentID := fmt.Sprintf("a-%d-%d", cycle, i)

			// Profile CreateTask
			start := time.Now()
			d.CreateTask(ctx, Task{ID: taskID, Title: fmt.Sprintf("Task %d-%d", cycle, i),
				Status: "pending", Role: "implementer"})
			mc.Record("CreateTask", time.Since(start), nil)

			// Profile RegisterAgent
			start = time.Now()
			d.RegisterAgent(ctx, Agent{ID: agentID, Role: "implementer", Status: "idle"})
			mc.Record("RegisterAgent", time.Since(start), nil)

			// Profile ClaimTask
			start = time.Now()
			d.ClaimTask(ctx, taskID, agentID, "/tmp/wt-"+taskID, "feature/"+taskID)
			mc.Record("ClaimTask", time.Since(start), nil)

			// Profile SetBlackboard
			start = time.Now()
			d.SetBlackboard(ctx, "result_summary:"+taskID,
				fmt.Sprintf(`{"output":"result-%d-%d"}`, cycle, i), agentID)
			mc.Record("SetBlackboard", time.Since(start), nil)

			// Profile CompleteTask
			start = time.Now()
			d.CompleteTask(ctx, taskID, "done")
			mc.Record("CompleteTask", time.Since(start), nil)

			// Profile LogEvent
			start = time.Now()
			d.LogEvent(ctx, "task_status", taskID, "done", "Task completed")
			mc.Record("LogEvent", time.Since(start), nil)
		}
	}

	t.Logf("profile: %s", mc.Report())
	t.Logf("total operations: %d", mc.Count())

	// Verify no SQLITE_BUSY errors in this controlled scenario
	if mc.BusyTimeoutCount() > 0 {
		t.Errorf("unexpected SQLITE_BUSY errors: %d", mc.BusyTimeoutCount())
	}

	// Log top 5 slowest queries
	top5 := mc.TopSlowest(5)
	for i, q := range top5 {
		t.Logf("  #%d slowest: %s = %v", i+1, q.QueryName, q.Duration)
	}

	// P99 should be under 100ms for in-memory DB
	if mc.P99() > 100*time.Millisecond {
		t.Errorf("P99 too high for in-memory DB: %v", mc.P99())
	}
}

// TestMemoryStabilityDuringDBOperations measures memory allocation
// stability across many DB operation cycles.
func TestMemoryStabilityDuringDBOperations(t *testing.T) {
	d := setupStressDB(t)
	ctx := context.Background()

	// Warm up
	for i := 0; i < 10; i++ {
		d.CreateTask(ctx, Task{ID: fmt.Sprintf("warmup-%d", i), Title: "Warmup", Status: "pending", Role: "implementer"})
	}

	runtime.GC()
	var memBefore runtime.MemStats
	runtime.ReadMemStats(&memBefore)

	// Run 100 full lifecycle cycles
	const cycles = 100
	for cycle := 0; cycle < cycles; cycle++ {
		taskID := fmt.Sprintf("mem-%d", cycle)
		agentID := fmt.Sprintf("agent-mem-%d", cycle)

		d.CreateTask(ctx, Task{ID: taskID, Title: "Mem test", Status: "pending", Role: "implementer"})
		d.RegisterAgent(ctx, Agent{ID: agentID, Role: "implementer", Status: "idle"})
		d.ClaimTask(ctx, taskID, agentID, "/tmp/wt-"+taskID, "feature/"+taskID)
		d.SetBlackboard(ctx, "key:"+taskID, "value", agentID)
		d.CompleteTask(ctx, taskID, "done")
		d.LogEvent(ctx, "task_status", taskID, "done", "Complete")

		// Cleanup blackboard entry to prevent unbounded growth
		d.DeleteBlackboard(ctx, "key:"+taskID)
	}

	runtime.GC()
	var memAfter runtime.MemStats
	runtime.ReadMemStats(&memAfter)

	heapDeltaKB := (int64(memAfter.HeapAlloc) - int64(memBefore.HeapAlloc)) / 1024
	totalAllocMB := float64(memAfter.TotalAlloc-memBefore.TotalAlloc) / 1024 / 1024

	t.Logf("memory after %d cycles: heap_delta=%dKB, total_alloc=%.1fMB, mallocs=%d",
		cycles, heapDeltaKB, totalAllocMB, memAfter.Mallocs-memBefore.Mallocs)

	// Heap growth should be bounded for in-memory DB
	if heapDeltaKB > 5*1024 { // 5MB
		t.Errorf("excessive heap growth: %dKB after %d cycles", heapDeltaKB, cycles)
	}
}

// TestGoroutineStabilityDuringConcurrentDBOps verifies that concurrent
// DB operations don't leak goroutines.
func TestGoroutineStabilityDuringConcurrentDBOps(t *testing.T) {
	d := setupStressDB(t)
	ctx := context.Background()

	baseline := runtime.NumGoroutine()
	t.Logf("baseline goroutines: %d", baseline)

	// Pre-create tasks
	const tasks = 20
	for i := 0; i < tasks; i++ {
		d.CreateTask(ctx, Task{
			ID: fmt.Sprintf("gor-%d", i), Title: fmt.Sprintf("Task %d", i),
			Status: "pending", Role: "implementer",
		})
	}

	// Run concurrent operations
	var wg sync.WaitGroup
	const workers = 10
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			for i := 0; i < 50; i++ {
				taskID := fmt.Sprintf("gor-%d", i%tasks)
				d.GetTaskByID(ctx, taskID)
				d.GetBlackboardValue(ctx, "key:"+taskID)
				d.LogEvent(ctx, "test", taskID, "check", fmt.Sprintf("w%d-i%d", workerID, i))
			}
		}(w)
	}
	wg.Wait()

	// Allow goroutines to settle
	runtime.GC()
	time.Sleep(50 * time.Millisecond)

	current := runtime.NumGoroutine()
	delta := current - baseline

	t.Logf("after concurrent ops: goroutines=%d, delta=%d", current, delta)

	// Delta should be small (±3 for test infrastructure)
	if delta > 5 {
		t.Errorf("goroutine leak: baseline=%d, current=%d, delta=%d", baseline, current, delta)
	}
}
