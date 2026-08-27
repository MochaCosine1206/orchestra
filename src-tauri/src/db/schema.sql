CREATE TABLE IF NOT EXISTS standards (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    code TEXT NOT NULL UNIQUE,
    description TEXT NOT NULL,
    subject TEXT NOT NULL,
    grade_band TEXT NOT NULL,
    domain TEXT NOT NULL DEFAULT '',
    cluster TEXT NOT NULL DEFAULT '',
    parent_code TEXT,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS lessons (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    title TEXT NOT NULL,
    description TEXT NOT NULL DEFAULT '',
    subject TEXT NOT NULL DEFAULT '',
    grade_level TEXT NOT NULL DEFAULT '',
    duration_minutes INTEGER,
    content TEXT NOT NULL DEFAULT '',
    standard_codes TEXT NOT NULL DEFAULT '[]',
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS assessments (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    title TEXT NOT NULL,
    description TEXT NOT NULL DEFAULT '',
    lesson_id INTEGER,
    assessment_type TEXT NOT NULL DEFAULT 'formative',
    content TEXT NOT NULL DEFAULT '',
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    FOREIGN KEY (lesson_id) REFERENCES lessons(id)
);

CREATE TABLE IF NOT EXISTS rubrics (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    title TEXT NOT NULL,
    description TEXT NOT NULL DEFAULT '',
    assessment_id INTEGER,
    criteria TEXT NOT NULL DEFAULT '[]',
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    FOREIGN KEY (assessment_id) REFERENCES assessments(id)
);

CREATE TABLE IF NOT EXISTS settings (
    key TEXT PRIMARY KEY,
    value TEXT NOT NULL,
    updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
);

CREATE VIRTUAL TABLE IF NOT EXISTS standards_fts USING fts5(
    code,
    description,
    subject,
    grade_band,
    domain,
    cluster,
    content='standards',
    content_rowid='id'
);

-- Triggers to keep FTS index in sync
CREATE TRIGGER IF NOT EXISTS standards_ai AFTER INSERT ON standards BEGIN
    INSERT INTO standards_fts(rowid, code, description, subject, grade_band, domain, cluster)
    VALUES (new.id, new.code, new.description, new.subject, new.grade_band, new.domain, new.cluster);
END;

CREATE TRIGGER IF NOT EXISTS standards_ad AFTER DELETE ON standards BEGIN
    INSERT INTO standards_fts(standards_fts, rowid, code, description, subject, grade_band, domain, cluster)
    VALUES ('delete', old.id, old.code, old.description, old.subject, old.grade_band, old.domain, old.cluster);
END;

CREATE TRIGGER IF NOT EXISTS standards_au AFTER UPDATE ON standards BEGIN
    INSERT INTO standards_fts(standards_fts, rowid, code, description, subject, grade_band, domain, cluster)
    VALUES ('delete', old.id, old.code, old.description, old.subject, old.grade_band, old.domain, old.cluster);
    INSERT INTO standards_fts(rowid, code, description, subject, grade_band, domain, cluster)
    VALUES (new.id, new.code, new.description, new.subject, new.grade_band, new.domain, new.cluster);
END;
