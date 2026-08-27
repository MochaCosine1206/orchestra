---
paths:
  - scripts/init-db.sh
  - internal/db/**
  - "*.sql"
---

# SQL Conventions

## Connection Pragmas (enforced by Go, bash, Node.js, and Python — all 5 access layers)
```sql
PRAGMA journal_mode = WAL;
PRAGMA busy_timeout = 5000;
PRAGMA synchronous = FULL;
PRAGMA fullfsync = ON;
PRAGMA foreign_keys = ON;
```

## Query Safety
- **Go:** Parameterized queries only (`db.QueryContext(ctx, "SELECT ... WHERE id = ?", id)`)
- **Bash:** `escape_sql` for all string interpolation into SQL
- Never string-interpolate user data or variable content directly into SQL
- Use `?` placeholders in Go, single-quote escaping via `escape_sql` in bash

## Schema
- `CREATE TABLE IF NOT EXISTS` for idempotent initialization
- `ALTER TABLE ... ADD COLUMN` with existence check for migrations
- Foreign key references enforced (`REFERENCES table(id)`)
- `DATETIME DEFAULT CURRENT_TIMESTAMP` for audit columns
- `TEXT PRIMARY KEY` for short IDs (8-char hex), `INTEGER PRIMARY KEY AUTOINCREMENT` for event sequences
