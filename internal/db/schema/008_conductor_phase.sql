-- Migration 008: Add phase_id to conductors for cross-invocation phase chaining (B-287).
-- Enables --start-phase to find the prior phase's staging branch from a previous
-- GoSpec() invocation, so the resumed phase forks from the right base.
ALTER TABLE conductors ADD COLUMN phase_id TEXT;
CREATE INDEX IF NOT EXISTS idx_conductors_phase ON conductors(phase_id);
