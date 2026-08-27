-- Orchestra coordination database schema
-- Core schema for orchestrator.db

PRAGMA journal_mode = WAL;
PRAGMA busy_timeout = 5000;
PRAGMA synchronous = FULL;
PRAGMA fullfsync = ON;
PRAGMA foreign_keys = ON;

-- Agent registry
CREATE TABLE IF NOT EXISTS agents (
  id TEXT PRIMARY KEY,
  role TEXT NOT NULL,
  status TEXT DEFAULT 'idle',
  worktree TEXT,
  pid INTEGER,
  current_task TEXT,
  heartbeat_at DATETIME,
  created_at DATETIME DEFAULT CURRENT_TIMESTAMP
);

-- Task queue
CREATE TABLE IF NOT EXISTS tasks (
  id TEXT PRIMARY KEY,
  title TEXT NOT NULL,
  description TEXT,
  acceptance_criteria TEXT,
  status TEXT DEFAULT 'pending',
  priority INTEGER DEFAULT 0,
  priority_label TEXT,
  role TEXT DEFAULT 'implementer',
  assigned_to TEXT REFERENCES agents(id),
  depends_on TEXT,
  blocked_by TEXT,
  worktree TEXT,
  branch TEXT,
  result TEXT,
  created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
  started_at DATETIME,
  completed_at DATETIME
);

-- File locks (prevent concurrent modification)
CREATE TABLE IF NOT EXISTS file_locks (
  file_path TEXT PRIMARY KEY,
  locked_by TEXT REFERENCES agents(id),
  task_id TEXT REFERENCES tasks(id),
  locked_at DATETIME DEFAULT CURRENT_TIMESTAMP,
  expires_at DATETIME
);

-- Event log (audit trail)
CREATE TABLE IF NOT EXISTS events (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  timestamp DATETIME DEFAULT CURRENT_TIMESTAMP,
  agent_id TEXT,
  task_id TEXT,
  event_type TEXT NOT NULL,
  payload TEXT
);

-- Shared knowledge (blackboard)
CREATE TABLE IF NOT EXISTS blackboard (
  key TEXT PRIMARY KEY,
  value TEXT NOT NULL,
  written_by TEXT,
  updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
);

-- Performance indexes for status queries (B-173)
CREATE INDEX IF NOT EXISTS idx_tasks_status ON tasks(status);
CREATE INDEX IF NOT EXISTS idx_agents_status ON agents(status);
CREATE INDEX IF NOT EXISTS idx_events_timestamp ON events(timestamp DESC);

-- Ideas
CREATE TABLE IF NOT EXISTS ideas (
  id TEXT PRIMARY KEY,
  title TEXT NOT NULL,
  description TEXT,
  status TEXT DEFAULT 'raw',
  current_phase TEXT,
  parent_id TEXT REFERENCES ideas(id),
  tags TEXT,
  knowledge_refs TEXT,
  created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
  updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
);

-- Loops
CREATE TABLE IF NOT EXISTS loops (
  id TEXT PRIMARY KEY,
  idea_id TEXT REFERENCES ideas(id),
  loop_type TEXT NOT NULL,
  status TEXT DEFAULT 'pending',
  iteration INTEGER DEFAULT 0,
  max_iterations INTEGER DEFAULT 10,
  token_budget INTEGER,
  tokens_used INTEGER DEFAULT 0,
  result TEXT,
  created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
  started_at DATETIME,
  completed_at DATETIME
);

-- Loop steps
CREATE TABLE IF NOT EXISTS loop_steps (
  id TEXT PRIMARY KEY,
  loop_id TEXT REFERENCES loops(id),
  task_id TEXT REFERENCES tasks(id),
  step_type TEXT NOT NULL,
  step_order INTEGER NOT NULL,
  status TEXT DEFAULT 'pending',
  retry_count INTEGER DEFAULT 0,
  max_retries INTEGER DEFAULT 3,
  result TEXT,
  knowledge_refs TEXT,
  error_class TEXT,
  created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
  completed_at DATETIME
);
