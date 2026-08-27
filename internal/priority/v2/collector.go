package priority

import "context"

// Collector gathers work items from a single source.
// Each collector is responsible for deduplication against existing work_items.
type Collector interface {
	// Name returns the source identifier (e.g., "backlog", "discovery").
	Name() Source

	// Collect gathers new or updated work items from this source.
	// Must be idempotent — running twice should not create duplicates.
	// Uses source + source_id as the dedup key.
	Collect(ctx context.Context) ([]WorkItem, error)
}
