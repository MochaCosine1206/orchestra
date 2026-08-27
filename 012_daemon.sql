-- Migration 012: Daemon database schema.
-- This schema is applied to ~/.config/orchestra/daemon.db (separate from the per-project DB).
-- Tables support the daemon's priority queue, scheduling, run history, trust tracking, and state.

CREATE TABLE IF NOT EXISTS priority_queue (
  id TEXT PRIMARY KEY,
  project_path TEXT NOT NULL,
  task_id TEXT NOT NULL,
  effective_priority REAL NOT NULL DEFAULT 0,
  computed_at DATETIME DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS schedules (
  id TEXT PRIMARY KEY,
  project_path TEXT NOT NULL,
  cron_expr TEXT NOT NULL,
  goal TEXT NOT NULL,
  enabled INTEGER NOT NULL DEFAULT 1,
  last_run DATETIME,
  next_run DATETIME,
  created_at DATETIME DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS run_history (
  id TEXT PRIMARY KEY,
  project_path TEXT NOT NULL,
  session_id TEXT NOT NULL,
  started_at DATETIME DEFAULT CURRENT_TIMESTAMP,
  finished_at DATETIME,
  status TEXT NOT NULL DEFAULT 'running',
  tasks_done INTEGER NOT NULL DEFAULT 0,
  tasks_failed INTEGER NOT NULL DEFAULT 0
);

CREATE TABLE IF NOT EXISTS trust_scores (
  project_path TEXT PRIMARY KEY,
  score REAL NOT NULL DEFAULT 0,
  total_runs INTEGER NOT NULL DEFAULT 0,
  successful_runs INTEGER NOT NULL DEFAULT 0,
  test_passes INTEGER NOT NULL DEFAULT 0,
  test_total INTEGER NOT NULL DEFAULT 0,
  overrides INTEGER NOT NULL DEFAULT 0,
  updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS daemon_state (
  key TEXT PRIMARY KEY,
  value TEXT NOT NULL,
  updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_priority_queue_project ON priority_queue(project_path);
CREATE INDEX IF NOT EXISTS idx_schedules_project ON schedules(project_path);
CREATE INDEX IF NOT EXISTS idx_schedules_next_run ON schedules(next_run);
CREATE INDEX IF NOT EXISTS idx_run_history_project ON run_history(project_path);
CREATE INDEX IF NOT EXISTS idx_run_history_started ON run_history(started_at DESC);
