-- Migration 009: Stall score tracking for stalled agent detection (B-142).
-- Records composite stall scores and individual signal values per task,
-- enabling graduated response to stalled agents in Dark Factory mode.

CREATE TABLE IF NOT EXISTS stall_scores (
  id INTEGER PRIMARY KEY,
  task_id TEXT NOT NULL,
  agent_id TEXT NOT NULL,
  composite_score REAL NOT NULL,
  signal_fingerprint REAL NOT NULL,
  signal_progress REAL NOT NULL,
  signal_files REAL NOT NULL,
  signal_errors REAL NOT NULL,
  signal_readwrite REAL NOT NULL,
  phase TEXT,
  created_at DATETIME DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_stall_scores_task ON stall_scores(task_id);
CREATE INDEX IF NOT EXISTS idx_stall_scores_created ON stall_scores(created_at);
