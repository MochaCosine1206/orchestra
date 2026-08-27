-- Migration 006: FIFO merge queue (B-280)
-- Adds merge_queue_entries table for sequential merge processing
-- and merge_strategy column on conductors.

CREATE TABLE IF NOT EXISTS merge_queue_entries (
    id TEXT PRIMARY KEY,
    conductor_id TEXT NOT NULL,
    task_id TEXT NOT NULL,
    branch TEXT NOT NULL,
    position INTEGER NOT NULL,
    status TEXT DEFAULT 'pending',  -- pending|blocked|merging|merged|failed|refining
    resolution_tier TEXT,
    conflict_files TEXT,
    test_result TEXT,
    error_message TEXT,
    refinement_count INTEGER DEFAULT 0,
    enqueued_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    merged_at DATETIME
);

CREATE INDEX IF NOT EXISTS idx_merge_queue_conductor ON merge_queue_entries(conductor_id);
CREATE INDEX IF NOT EXISTS idx_merge_queue_status ON merge_queue_entries(status);

-- ALTER TABLE conductors ADD COLUMN merge_strategy TEXT DEFAULT 'batch';
-- (applied programmatically in schema.go to guard against duplicate column errors)
