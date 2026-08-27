-- Migration 012: Eval framework and healing log tables

CREATE TABLE IF NOT EXISTS eval_scenarios (
    id              TEXT PRIMARY KEY,
    role            TEXT NOT NULL,
    category        TEXT,
    repo_path       TEXT,
    goal            TEXT NOT NULL,
    expected_outcome TEXT,
    difficulty      TEXT,
    created_at      DATETIME DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS eval_versions (
    id          TEXT PRIMARY KEY,
    parent_id   TEXT,
    branch      TEXT,
    commit_hash TEXT,
    created_at  DATETIME DEFAULT CURRENT_TIMESTAMP,
    status      TEXT NOT NULL DEFAULT 'candidate'
);

CREATE TABLE IF NOT EXISTS eval_runs (
    id           TEXT PRIMARY KEY,
    version_id   TEXT NOT NULL REFERENCES eval_versions(id),
    scenario_id  TEXT NOT NULL REFERENCES eval_scenarios(id),
    started_at   DATETIME,
    completed_at DATETIME,
    status       TEXT NOT NULL DEFAULT 'pending',
    raw_output   TEXT
);

CREATE INDEX IF NOT EXISTS idx_eval_runs_version ON eval_runs(version_id);
CREATE INDEX IF NOT EXISTS idx_eval_runs_scenario ON eval_runs(scenario_id);
CREATE INDEX IF NOT EXISTS idx_eval_runs_status ON eval_runs(status);

CREATE TABLE IF NOT EXISTS eval_results (
    id      TEXT PRIMARY KEY,
    run_id  TEXT NOT NULL REFERENCES eval_runs(id),
    metric  TEXT NOT NULL,
    score   REAL NOT NULL,
    weight  REAL NOT NULL DEFAULT 1.0,
    details TEXT
);

CREATE INDEX IF NOT EXISTS idx_eval_results_run ON eval_results(run_id);

CREATE TABLE IF NOT EXISTS healing_log (
    id          TEXT PRIMARY KEY,
    session_id  TEXT NOT NULL,
    task_id     TEXT,
    error_type  TEXT,
    fix_applied TEXT,
    success     INTEGER NOT NULL DEFAULT 0,
    rolled_back INTEGER NOT NULL DEFAULT 0,
    created_at  DATETIME DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_healing_log_session ON healing_log(session_id);
CREATE INDEX IF NOT EXISTS idx_healing_log_task ON healing_log(task_id);
