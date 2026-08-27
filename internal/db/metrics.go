package db

import (
	"fmt"
	"sort"
	"sync"
	"time"
)

// QueryMetrics captures timing data for a single database operation.
type QueryMetrics struct {
	QueryName string
	Duration  time.Duration
	Error     error
	Timestamp time.Time
}

// MetricsCollector aggregates query timing data for performance analysis.
// Thread-safe for concurrent use by multiple goroutines.
type MetricsCollector struct {
	mu      sync.Mutex
	queries []QueryMetrics
	enabled bool
}

// NewMetricsCollector creates a new collector. Disabled by default.
func NewMetricsCollector() *MetricsCollector {
	return &MetricsCollector{}
}

// Enable turns on metrics collection.
func (mc *MetricsCollector) Enable() {
	mc.mu.Lock()
	defer mc.mu.Unlock()
	mc.enabled = true
}

// Disable turns off metrics collection.
func (mc *MetricsCollector) Disable() {
	mc.mu.Lock()
	defer mc.mu.Unlock()
	mc.enabled = false
}

// Record adds a query timing observation.
func (mc *MetricsCollector) Record(name string, duration time.Duration, err error) {
	mc.mu.Lock()
	defer mc.mu.Unlock()
	if !mc.enabled {
		return
	}
	mc.queries = append(mc.queries, QueryMetrics{
		QueryName: name,
		Duration:  duration,
		Error:     err,
		Timestamp: time.Now(),
	})
}

// Count returns the total number of recorded queries.
func (mc *MetricsCollector) Count() int {
	mc.mu.Lock()
	defer mc.mu.Unlock()
	return len(mc.queries)
}

// ErrorCount returns the number of queries that returned an error.
func (mc *MetricsCollector) ErrorCount() int {
	mc.mu.Lock()
	defer mc.mu.Unlock()
	count := 0
	for _, q := range mc.queries {
		if q.Error != nil {
			count++
		}
	}
	return count
}

// Percentile returns the duration at the given percentile (0-100).
func (mc *MetricsCollector) Percentile(p float64) time.Duration {
	mc.mu.Lock()
	defer mc.mu.Unlock()
	if len(mc.queries) == 0 {
		return 0
	}

	durations := make([]time.Duration, len(mc.queries))
	for i, q := range mc.queries {
		durations[i] = q.Duration
	}
	sort.Slice(durations, func(i, j int) bool { return durations[i] < durations[j] })

	idx := int(float64(len(durations)-1) * p / 100.0)
	if idx >= len(durations) {
		idx = len(durations) - 1
	}
	return durations[idx]
}

// P50 returns the median query duration.
func (mc *MetricsCollector) P50() time.Duration { return mc.Percentile(50) }

// P99 returns the 99th percentile query duration.
func (mc *MetricsCollector) P99() time.Duration { return mc.Percentile(99) }

// BusyTimeoutCount returns the number of queries that failed with SQLITE_BUSY.
func (mc *MetricsCollector) BusyTimeoutCount() int {
	mc.mu.Lock()
	defer mc.mu.Unlock()
	count := 0
	for _, q := range mc.queries {
		if q.Error != nil && (contains(q.Error.Error(), "database is locked") || contains(q.Error.Error(), "SQLITE_BUSY")) {
			count++
		}
	}
	return count
}

// TopSlowest returns the N slowest queries.
func (mc *MetricsCollector) TopSlowest(n int) []QueryMetrics {
	mc.mu.Lock()
	defer mc.mu.Unlock()

	sorted := make([]QueryMetrics, len(mc.queries))
	copy(sorted, mc.queries)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Duration > sorted[j].Duration })

	if n > len(sorted) {
		n = len(sorted)
	}
	return sorted[:n]
}

// Report generates a human-readable metrics summary.
func (mc *MetricsCollector) Report() string {
	mc.mu.Lock()
	count := len(mc.queries)
	mc.mu.Unlock()

	if count == 0 {
		return "No queries recorded"
	}

	return fmt.Sprintf(
		"Queries: %d | Errors: %d | Busy timeouts: %d | P50: %v | P99: %v",
		count, mc.ErrorCount(), mc.BusyTimeoutCount(), mc.P50(), mc.P99(),
	)
}

// Reset clears all recorded metrics.
func (mc *MetricsCollector) Reset() {
	mc.mu.Lock()
	defer mc.mu.Unlock()
	mc.queries = nil
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && searchString(s, substr)
}

func searchString(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
