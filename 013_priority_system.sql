-- Migration 013: Automatic priority system.
-- Applied to ~/.config/orchestra/daemon.db alongside 012_daemon.sql tables.

-- ─── WORK ITEMS: unified table for all prioritizable work ───

CREATE TABLE IF NOT EXISTS work_items (
  id TEXT PRIMARY KEY,                          -- 8-char hex (db.GenID("wi"))
  source TEXT NOT NULL,                         -- collector name: user, backlog, discovery, grant, research, retry, stale, oss, market
  source_id TEXT,                               -- original ID in source system (e.g., "B-142", grant ID, issue #)
  source_repo TEXT,                             -- repo path where this item originated
  title TEXT NOT NULL,
  description TEXT NOT NULL DEFAULT '',

  -- Hierarchy tier (from B-051)
  tier INTEGER NOT NULL DEFAULT 4,              -- 1=user, 2=retry, 3=goal-impl, 4=goal-research, 5=validation, 6=tech-debt, 7=self-improve, 8=exploratory

  -- Raw scores from source
  base_priority INTEGER NOT NULL DEFAULT 50,    -- P0=100, P1=80, P2=60, P3=40, P4=20, P5=10
  feasibility REAL DEFAULT NULL,                -- 0.0-1.0 from discovery
  impact REAL DEFAULT NULL,                     -- 0.0-1.0 from discovery
  uniqueness REAL DEFAULT NULL,                 -- 0.0-1.0 from discovery

  -- Deadline
  deadline DATETIME DEFAULT NULL,               -- hard deadline (grants, regulations)
  deadline_type TEXT DEFAULT NULL,              -- 'hard' (grant), 'soft' (feature), 'regulatory' (EU AI Act)

  -- Effort estimate
  effort_hours REAL DEFAULT NULL,               -- estimated hours to complete
  effort_confidence REAL DEFAULT NULL,          -- 0.0-1.0 confidence in estimate

  -- Dependencies
  blocked_by TEXT DEFAULT NULL,                 -- JSON array of work_item IDs this depends on
  blocks TEXT DEFAULT NULL,                     -- JSON array of work_item IDs that depend on this

  -- User signals
  user_picked INTEGER NOT NULL DEFAULT 0,       -- 1 if Steven selected this from daily report
  user_picked_at DATETIME DEFAULT NULL,
  user_priority_rank INTEGER DEFAULT NULL,      -- position in priorities.md (1 = top)

  -- Market signals
  market_signal_ids TEXT DEFAULT NULL,           -- JSON array of signal IDs that boosted this
  competitor_shipped INTEGER NOT NULL DEFAULT 0, -- 1 if a competitor shipped something related

  -- Routing
  target_repo TEXT DEFAULT NULL,                -- resolved repo for execution
  is_new_project INTEGER NOT NULL DEFAULT 0,    -- 0=improvement to existing, 1=new project
  agent_roles TEXT DEFAULT NULL,                -- JSON array: ["researcher","implementer"]

  -- Computed
  goal_alignment REAL DEFAULT NULL,             -- 0.0-1.0 from LLM alignment scorer
  effective_priority REAL NOT NULL DEFAULT 0,
  priority_explanation TEXT DEFAULT NULL,        -- human-readable why this score

  -- Project classification
  license_type TEXT DEFAULT 'open-source',      -- open-source, closed-source, dual-license
  license_reason TEXT DEFAULT '',               -- why this classification was chosen

  -- State
  status TEXT NOT NULL DEFAULT 'pending',       -- pending, queued, running, completed, failed, blocked, stale, cancelled
  retry_count INTEGER NOT NULL DEFAULT 0,
  last_failure_reason TEXT DEFAULT NULL,

  -- Staleness
  created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
  updated_at DATETIME DEFAULT CURRENT_TIMESTAMP,
  scored_at DATETIME DEFAULT NULL,              -- last time scoring ran
  stale_after DATETIME DEFAULT NULL,            -- when this item should be revalidated
  last_validated_at DATETIME DEFAULT NULL       -- last revalidation
);

-- ─── USER PRIORITIES: parsed from ~/.config/orchestra/priorities.md ───

CREATE TABLE IF NOT EXISTS user_priorities (
  id TEXT PRIMARY KEY,
  rank INTEGER NOT NULL,                        -- 1 = highest priority
  title TEXT NOT NULL,
  description TEXT NOT NULL DEFAULT '',
  repo_hint TEXT DEFAULT NULL,                  -- optional repo constraint
  parsed_at DATETIME DEFAULT CURRENT_TIMESTAMP,
  active INTEGER NOT NULL DEFAULT 1
);

-- ─── PRIORITY HISTORY: audit trail for score changes ───

CREATE TABLE IF NOT EXISTS priority_history (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  work_item_id TEXT NOT NULL REFERENCES work_items(id),
  old_priority REAL,
  new_priority REAL NOT NULL,
  reason TEXT NOT NULL,                         -- what triggered the rescore
  scored_at DATETIME DEFAULT CURRENT_TIMESTAMP
);

-- ─── DAILY REPORTS: track what was generated and what user picked ───

CREATE TABLE IF NOT EXISTS daily_reports (
  id TEXT PRIMARY KEY,
  generated_at DATETIME DEFAULT CURRENT_TIMESTAMP,
  top_items TEXT NOT NULL,                      -- JSON array of work_item IDs in ranked order
  user_picks TEXT DEFAULT NULL,                 -- JSON array of work_item IDs user selected
  pick_received_at DATETIME DEFAULT NULL
);

-- ─── DEPENDENCY GRAPH: explicit edges between work items ───

CREATE TABLE IF NOT EXISTS work_item_deps (
  parent_id TEXT NOT NULL REFERENCES work_items(id),
  child_id TEXT NOT NULL REFERENCES work_items(id),
  dep_type TEXT NOT NULL DEFAULT 'blocks',      -- 'blocks', 'suggested_before', 'same_area'
  PRIMARY KEY (parent_id, child_id)
);

-- ─── INDEXES ───

CREATE INDEX IF NOT EXISTS idx_work_items_source ON work_items(source);
CREATE INDEX IF NOT EXISTS idx_work_items_status ON work_items(status);
CREATE INDEX IF NOT EXISTS idx_work_items_tier ON work_items(tier);
CREATE INDEX IF NOT EXISTS idx_work_items_priority ON work_items(effective_priority DESC);
CREATE INDEX IF NOT EXISTS idx_work_items_deadline ON work_items(deadline);
CREATE INDEX IF NOT EXISTS idx_work_items_repo ON work_items(target_repo);
CREATE INDEX IF NOT EXISTS idx_work_items_source_id ON work_items(source, source_id);
CREATE INDEX IF NOT EXISTS idx_priority_history_item ON priority_history(work_item_id);
CREATE INDEX IF NOT EXISTS idx_user_priorities_rank ON user_priorities(rank);
