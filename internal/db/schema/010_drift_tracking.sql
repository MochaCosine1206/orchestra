-- Migration 010: Drift score tracking for goal drift detection.
-- Records per-cycle drift scores with explanations and actions taken,
-- enabling the drift engine to track and respond to goal divergence.

CREATE TABLE IF NOT EXISTS drift_scores (
  id INTEGER PRIMARY KEY,
  session_id TEXT NOT NULL,
  cycle_number INTEGER NOT NULL,
  score REAL NOT NULL,
  explanation TEXT,
  action_taken TEXT,
  created_at DATETIME DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_drift_scores_session ON drift_scores(session_id);
CREATE INDEX IF NOT EXISTS idx_drift_scores_cycle ON drift_scores(session_id, cycle_number);
