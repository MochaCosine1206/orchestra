-- Migration 015: Quality infrastructure tables (ratchet thresholds + ship decisions)

CREATE TABLE IF NOT EXISTS quality_ratchet (
    project_path        TEXT PRIMARY KEY,
    coverage_threshold  REAL DEFAULT 0.0,
    mutation_threshold  REAL DEFAULT 0.0,
    updated_at          DATETIME DEFAULT CURRENT_TIMESTAMP,
    updated_by          TEXT DEFAULT 'system'
);

CREATE TABLE IF NOT EXISTS ship_decisions (
    id              TEXT PRIMARY KEY,
    session_id      TEXT,
    project_path    TEXT,
    branch          TEXT,
    decision        TEXT,
    reason          TEXT,
    quality_report  TEXT,
    created_at      DATETIME DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_ship_decisions_session ON ship_decisions(session_id);
CREATE INDEX IF NOT EXISTS idx_ship_decisions_project ON ship_decisions(project_path);
