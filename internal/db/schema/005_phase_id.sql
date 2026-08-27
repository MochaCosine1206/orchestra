-- Migration 005: Add phase_id column to tasks table for multi-phase orchestration (G110).
-- Enables phase-scoped merge filtering — each Go() call within GoSpec() tags its
-- tasks with a phase_id so Merge() only processes the current phase's branches.
ALTER TABLE tasks ADD COLUMN phase_id TEXT;
CREATE INDEX IF NOT EXISTS idx_tasks_phase ON tasks(phase_id);
