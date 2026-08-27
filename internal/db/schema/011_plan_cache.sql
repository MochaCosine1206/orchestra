-- Migration 011: Plan cache for decomposition plan reuse.
-- Caches decomposition plans keyed by goal hash, enabling exact match,
-- W5H2-based fuzzy match, and keyword-based Jaccard similarity lookups.

CREATE TABLE IF NOT EXISTS plan_cache (
  id INTEGER PRIMARY KEY,
  goal_hash TEXT NOT NULL UNIQUE,
  goal_text TEXT NOT NULL,
  w5h2_key TEXT,
  keywords TEXT,
  plan_json TEXT NOT NULL,
  action_type TEXT,
  tier INTEGER,
  ttl_days INTEGER NOT NULL DEFAULT 30,
  fail_count INTEGER NOT NULL DEFAULT 0,
  hit_count INTEGER NOT NULL DEFAULT 0,
  file_mtimes TEXT,
  created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
  expires_at DATETIME NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_plan_cache_goal_hash ON plan_cache(goal_hash);
CREATE INDEX IF NOT EXISTS idx_plan_cache_w5h2 ON plan_cache(w5h2_key);
CREATE INDEX IF NOT EXISTS idx_plan_cache_expires ON plan_cache(expires_at);
