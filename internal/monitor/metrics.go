package monitor

import (
	"fmt"
	"runtime"
	"sync"
	"time"
)

// GoroutineSnapshot records the goroutine count at a point in time.
type GoroutineSnapshot struct {
	Timestamp time.Time
	Count     int
	Delta     int // change since last snapshot
}

// GoroutineMetrics tracks goroutine counts over time to detect leaks.
// Thread-safe for concurrent use.
type GoroutineMetrics struct {
	mu        sync.Mutex
	snapshots []GoroutineSnapshot
	baseline  int // initial goroutine count
}

// NewGoroutineMetrics creates a new tracker, recording the current
// goroutine count as the baseline.
func NewGoroutineMetrics() *GoroutineMetrics {
	return &GoroutineMetrics{
		baseline: runtime.NumGoroutine(),
	}
}

// Snapshot records the current goroutine count.
func (gm *GoroutineMetrics) Snapshot() GoroutineSnapshot {
	gm.mu.Lock()
	defer gm.mu.Unlock()

	count := runtime.NumGoroutine()
	delta := 0
	if len(gm.snapshots) > 0 {
		delta = count - gm.snapshots[len(gm.snapshots)-1].Count
	} else {
		delta = count - gm.baseline
	}

	snap := GoroutineSnapshot{
		Timestamp: time.Now(),
		Count:     count,
		Delta:     delta,
	}
	gm.snapshots = append(gm.snapshots, snap)
	return snap
}

// Current returns the current goroutine count.
func (gm *GoroutineMetrics) Current() int {
	return runtime.NumGoroutine()
}

// Baseline returns the initial goroutine count.
func (gm *GoroutineMetrics) Baseline() int {
	return gm.baseline
}

// DeltaFromBaseline returns the difference between current and baseline.
func (gm *GoroutineMetrics) DeltaFromBaseline() int {
	return runtime.NumGoroutine() - gm.baseline
}

// Peak returns the highest goroutine count observed.
func (gm *GoroutineMetrics) Peak() int {
	gm.mu.Lock()
	defer gm.mu.Unlock()

	peak := gm.baseline
	for _, s := range gm.snapshots {
		if s.Count > peak {
			peak = s.Count
		}
	}
	return peak
}

// DetectLeaks returns warnings if the goroutine count exceeds baseline
// by more than threshold. Call after all work should be complete.
func (gm *GoroutineMetrics) DetectLeaks(threshold int) []string {
	delta := gm.DeltaFromBaseline()
	if delta <= threshold {
		return nil
	}
	return []string{
		fmt.Sprintf("goroutine leak detected: %d goroutines above baseline (baseline=%d, current=%d, threshold=%d)",
			delta, gm.baseline, runtime.NumGoroutine(), threshold),
	}
}

// SnapshotCount returns the number of snapshots recorded.
func (gm *GoroutineMetrics) SnapshotCount() int {
	gm.mu.Lock()
	defer gm.mu.Unlock()
	return len(gm.snapshots)
}

// Report generates a human-readable goroutine metrics summary.
func (gm *GoroutineMetrics) Report() string {
	return fmt.Sprintf(
		"Goroutines: baseline=%d current=%d peak=%d delta=%+d snapshots=%d",
		gm.baseline, runtime.NumGoroutine(), gm.Peak(), gm.DeltaFromBaseline(), gm.SnapshotCount(),
	)
}

// Reset clears all snapshots and re-establishes the baseline.
func (gm *GoroutineMetrics) Reset() {
	gm.mu.Lock()
	defer gm.mu.Unlock()
	gm.snapshots = nil
	gm.baseline = runtime.NumGoroutine()
}
