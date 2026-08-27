-- Migration 017: Add execution_mode to priority_queue for work item routing.
-- Supports: conduct (multi-agent), research (single researcher), grant, direct.

ALTER TABLE priority_queue ADD COLUMN execution_mode TEXT DEFAULT 'conduct';
