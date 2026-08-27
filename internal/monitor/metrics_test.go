package monitor

import (
	"context"
	"runtime"
	"sync"
	"testing"
	"time"
)

// B-159: Memory Profiling + Goroutine Leak Detection (monitor package)
//
// Tests the GoroutineMetrics infrastructure and validates goroutine
// leak detection during simulated multi-agent workloads.

// TestGoroutineMetricsBaseline verifies that NewGoroutineMetrics captures
// the correct baseline and that fresh instances have zero delta.
func TestGoroutineMetricsBaseline(t *testing.T) {
	gm := NewGoroutineMetrics()

	baseline := gm.Baseline()
	current := gm.Current()

	// Baseline should match runtime at creation time
	if baseline <= 0 {
		t.Errorf("expected positive baseline, got %d", baseline)
	}

	// Delta from baseline should be very small (±2 for test goroutines)
	delta := gm.DeltaFromBaseline()
	if delta > 5 || delta < -5 {
		t.Errorf("expected delta near 0, got %d (baseline=%d, current=%d)", delta, baseline, current)
	}

	// Peak should equal baseline with no snapshots
	peak := gm.Peak()
	if peak != baseline {
		t.Errorf("expected peak == baseline (%d), got %d", baseline, peak)
	}

	// No snapshots yet
	if gm.SnapshotCount() != 0 {
		t.Errorf("expected 0 snapshots, got %d", gm.SnapshotCount())
	}
}

// TestGoroutineMetricsSnapshotTracking verifies snapshot recording and
// peak detection across goroutine creation/destruction cycles.
func TestGoroutineMetricsSnapshotTracking(t *testing.T) {
	gm := NewGoroutineMetrics()

	// Take initial snapshot
	snap0 := gm.Snapshot()
	if snap0.Count <= 0 {
		t.Errorf("expected positive count in snapshot, got %d", snap0.Count)
	}

	// Spawn goroutines and measure
	const numGoroutines = 20
	ch := make(chan struct{})
	var wg sync.WaitGroup

	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-ch // block until signaled
		}()
	}

	// Let goroutines start
	runtime.Gosched()
	time.Sleep(10 * time.Millisecond)

	// Snapshot during peak
	snapPeak := gm.Snapshot()
	t.Logf("peak snapshot: count=%d, delta=%d", snapPeak.Count, snapPeak.Delta)

	// Peak should be higher than baseline
	if snapPeak.Count < gm.Baseline()+numGoroutines/2 {
		t.Errorf("expected peak count to increase by at least %d, got count=%d (baseline=%d)",
			numGoroutines/2, snapPeak.Count, gm.Baseline())
	}

	// Signal goroutines to exit
	close(ch)
	wg.Wait()
	runtime.GC()
	time.Sleep(10 * time.Millisecond)

	// Snapshot after cleanup
	snapAfter := gm.Snapshot()
	t.Logf("after cleanup: count=%d, delta=%d", snapAfter.Count, snapAfter.Delta)

	// Should be close to baseline again
	if snapAfter.Count > gm.Baseline()+5 {
		t.Errorf("goroutine leak: count=%d, baseline=%d, delta=%d",
			snapAfter.Count, gm.Baseline(), snapAfter.Delta)
	}

	// Peak should capture the maximum
	peak := gm.Peak()
	if peak < snapPeak.Count {
		t.Errorf("expected peak >= %d, got %d", snapPeak.Count, peak)
	}

	// Should have 3 snapshots
	if gm.SnapshotCount() != 3 {
		t.Errorf("expected 3 snapshots, got %d", gm.SnapshotCount())
	}
}

// TestGoroutineMetricsLeakDetection verifies the DetectLeaks method
// correctly identifies leaked goroutines.
func TestGoroutineMetricsLeakDetection(t *testing.T) {
	gm := NewGoroutineMetrics()

	// No leaks initially
	warnings := gm.DetectLeaks(2)
	if len(warnings) > 0 {
		t.Errorf("expected no leak warnings initially, got %v", warnings)
	}

	// Spawn goroutines that will "leak" (block forever until cleanup)
	const leakedCount = 10
	ch := make(chan struct{})
	for i := 0; i < leakedCount; i++ {
		go func() {
			<-ch
		}()
	}
	runtime.Gosched()
	time.Sleep(10 * time.Millisecond)

	// Should detect leaks with threshold 2
	warnings = gm.DetectLeaks(2)
	if len(warnings) == 0 {
		t.Errorf("expected leak warning with %d goroutines above baseline", leakedCount)
	} else {
		t.Logf("leak detected: %s", warnings[0])
	}

	// Should NOT detect with high threshold
	warnings = gm.DetectLeaks(20)
	if len(warnings) > 0 {
		t.Errorf("expected no warnings with high threshold, got %v", warnings)
	}

	// Cleanup
	close(ch)
	time.Sleep(10 * time.Millisecond)

	// After cleanup, should have no leaks
	warnings = gm.DetectLeaks(2)
	if len(warnings) > 0 {
		t.Errorf("expected no leaks after cleanup, got %v", warnings)
	}
}

// TestGoroutineMetricsReset verifies Reset clears state and re-baselines.
func TestGoroutineMetricsReset(t *testing.T) {
	gm := NewGoroutineMetrics()

	// Take some snapshots
	gm.Snapshot()
	gm.Snapshot()
	gm.Snapshot()

	if gm.SnapshotCount() != 3 {
		t.Fatalf("expected 3 snapshots before reset, got %d", gm.SnapshotCount())
	}

	oldBaseline := gm.Baseline()
	gm.Reset()

	if gm.SnapshotCount() != 0 {
		t.Errorf("expected 0 snapshots after reset, got %d", gm.SnapshotCount())
	}

	// Baseline should be re-established (may differ slightly)
	newBaseline := gm.Baseline()
	diff := newBaseline - oldBaseline
	if diff > 5 || diff < -5 {
		t.Errorf("expected baseline to be similar after reset, old=%d new=%d", oldBaseline, newBaseline)
	}
}

// TestGoroutineMetricsConcurrentAccess verifies thread safety of
// GoroutineMetrics under concurrent snapshot/read access.
func TestGoroutineMetricsConcurrentAccess(t *testing.T) {
	gm := NewGoroutineMetrics()

	var wg sync.WaitGroup
	const workers = 20

	// Concurrent snapshots
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				gm.Snapshot()
				gm.Current()
				gm.Peak()
				gm.DeltaFromBaseline()
			}
		}()
	}
	wg.Wait()

	if gm.SnapshotCount() != workers*50 {
		t.Errorf("expected %d snapshots, got %d", workers*50, gm.SnapshotCount())
	}

	report := gm.Report()
	if report == "" {
		t.Error("expected non-empty report")
	}
	t.Logf("report: %s", report)
}

// TestGoroutineMetricsMonitorIntegration tests goroutine tracking during
// a simulated monitor RunOnce cycle with multiple agents.
func TestGoroutineMetricsMonitorIntegration(t *testing.T) {
	mon, d, _ := setupMonitorTest(t)
	ctx := context.Background()

	gm := NewGoroutineMetrics()
	t.Logf("baseline: %d goroutines", gm.Baseline())

	// Run multiple monitor cycles and track goroutine counts
	const cycles = 10
	for i := 0; i < cycles; i++ {
		// Set up some work each cycle
		d.SetBlackboard(ctx, "conductor:active", "1", "test")
		d.SetBlackboard(ctx, "conductor:task_ids", `[]`, "test")

		_, err := mon.RunOnce(ctx)
		if err != nil {
			t.Fatalf("RunOnce cycle %d: %v", i, err)
		}
		gm.Snapshot()
	}

	t.Logf("after %d cycles: %s", cycles, gm.Report())

	// No goroutine leaks after monitor cycles
	warnings := gm.DetectLeaks(3)
	if len(warnings) > 0 {
		t.Errorf("goroutine leak after %d monitor cycles: %v", cycles, warnings)
	}
}

// TestMemoryStabilityDuringMonitorCycles measures memory allocation
// stability across many monitor cycles to detect memory leaks.
func TestMemoryStabilityDuringMonitorCycles(t *testing.T) {
	mon, d, _ := setupMonitorTest(t)
	ctx := context.Background()

	// Force GC and get baseline
	runtime.GC()
	var memBefore runtime.MemStats
	runtime.ReadMemStats(&memBefore)

	const cycles = 50
	for i := 0; i < cycles; i++ {
		d.SetBlackboard(ctx, "conductor:active", "1", "test")
		d.SetBlackboard(ctx, "conductor:task_ids", `[]`, "test")

		_, err := mon.RunOnce(ctx)
		if err != nil {
			t.Fatalf("RunOnce cycle %d: %v", i, err)
		}
	}

	// Force GC and measure
	runtime.GC()
	var memAfter runtime.MemStats
	runtime.ReadMemStats(&memAfter)

	allocDelta := int64(memAfter.TotalAlloc) - int64(memBefore.TotalAlloc)
	heapDelta := int64(memAfter.HeapAlloc) - int64(memBefore.HeapAlloc)
	sysMemMB := float64(memAfter.Sys) / 1024 / 1024

	t.Logf("memory after %d cycles: total_alloc_delta=%dKB, heap_delta=%dKB, sys=%.1fMB",
		cycles, allocDelta/1024, heapDelta/1024, sysMemMB)

	// Heap growth should be bounded (50 empty cycles should not leak significant memory)
	// Allow 5MB for overhead
	if heapDelta > 5*1024*1024 {
		t.Errorf("excessive heap growth: %dKB after %d cycles", heapDelta/1024, cycles)
	}
}

