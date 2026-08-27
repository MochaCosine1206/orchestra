-- Conductor registry for parallel conductor support (B-030)
-- Each conductor session gets its own staging branch and isolated state.

CREATE TABLE IF NOT EXISTS conductors (
  id TEXT PRIMARY KEY,
  pid INTEGER NOT NULL,
  goal TEXT NOT NULL,
  status TEXT DEFAULT 'active',  -- active, completed, failed, abandoned
  staging_branch TEXT NOT NULL,
  base_branch TEXT NOT NULL,
  max_parallel INTEGER DEFAULT 3,
  test_cmd TEXT,
  merge_review INTEGER DEFAULT 0,
  model_strategy TEXT DEFAULT 'all-opus',
  runtime TEXT DEFAULT 'local',
  repo_map INTEGER DEFAULT 0,
  lenient_deps INTEGER DEFAULT 0,
  file_enforcement TEXT DEFAULT '',
  started_at DATETIME DEFAULT CURRENT_TIMESTAMP,
  heartbeat_at DATETIME DEFAULT CURRENT_TIMESTAMP,
  completed_at DATETIME
);

CREATE INDEX IF NOT EXISTS idx_conductors_status ON conductors(status);

-- Add conductor_id FK to existing tables (nullable for backward compat).
-- SQLite ALTER TABLE ADD COLUMN is idempotent if guarded by a check.
-- We use a trigger-free approach: just run ALTER and ignore "duplicate column" errors.
