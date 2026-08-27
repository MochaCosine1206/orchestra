use rusqlite::Connection;
use tracing::info;

use crate::error::AppError;

const SCHEMA: &str = include_str!("schema.sql");

pub fn init_db(path: &str) -> Result<Connection, AppError> {
    info!(db_path = path, "initializing database");

    let conn = Connection::open(path)?;

    conn.execute_batch("PRAGMA journal_mode = WAL;")?;
    conn.execute_batch("PRAGMA busy_timeout = 5000;")?;

    info!("running schema migrations");
    conn.execute_batch(SCHEMA)?;

    info!("database initialized successfully");
    Ok(conn)
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn test_init_db_in_memory() {
        let conn = init_db(":memory:").expect("init_db should succeed");

        // Verify WAL mode
        let mode: String = conn
            .query_row("PRAGMA journal_mode;", [], |row| row.get(0))
            .unwrap();
        // In-memory databases may report "memory" instead of "wal"
        assert!(mode == "wal" || mode == "memory", "unexpected mode: {mode}");

        // Verify tables exist
        let count: i64 = conn
            .query_row(
                "SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name IN ('standards', 'lessons', 'assessments', 'rubrics', 'settings')",
                [],
                |row| row.get(0),
            )
            .unwrap();
        assert_eq!(count, 5, "expected 5 tables");

        // Verify FTS virtual table
        let fts_count: i64 = conn
            .query_row(
                "SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='standards_fts'",
                [],
                |row| row.get(0),
            )
            .unwrap();
        assert_eq!(fts_count, 1, "expected standards_fts table");
    }
}
