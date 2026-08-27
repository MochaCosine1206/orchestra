package db

import (
	"context"
	"database/sql"
	"fmt"
	"os"

	_ "github.com/ncruces/go-sqlite3/driver"
	_ "github.com/ncruces/go-sqlite3/embed"
)

// DB wraps a sql.DB connection with orchestra-specific configuration.
type DB struct {
	*sql.DB
	path     string
	readOnly bool
}

// Open opens a read-write SQLite connection with WAL mode, busy_timeout, and foreign_keys.
func Open(path string) (*DB, error) {
	dsn := fmt.Sprintf("file:%s?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=foreign_keys(ON)&_pragma=synchronous(FULL)&_pragma=fullfsync(ON)", path)
	sqlDB, err := sql.Open("sqlite3", dsn)
	if err != nil {
		return nil, fmt.Errorf("opening database %s: %w", path, err)
	}
	// Single writer to avoid SQLITE_BUSY on concurrent writes.
	sqlDB.SetMaxOpenConns(1)

	if err := sqlDB.Ping(); err != nil {
		sqlDB.Close()
		return nil, fmt.Errorf("pinging database %s: %w", path, err)
	}

	return &DB{DB: sqlDB, path: path, readOnly: false}, nil
}

// OpenReadOnly opens a read-only SQLite connection. Multiple concurrent readers are safe.
func OpenReadOnly(path string) (*DB, error) {
	dsn := fmt.Sprintf("file:%s?mode=ro&_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=foreign_keys(ON)&_pragma=synchronous(FULL)&_pragma=fullfsync(ON)", path)
	sqlDB, err := sql.Open("sqlite3", dsn)
	if err != nil {
		return nil, fmt.Errorf("opening read-only database %s: %w", path, err)
	}

	if err := sqlDB.Ping(); err != nil {
		sqlDB.Close()
		return nil, fmt.Errorf("pinging database %s: %w", path, err)
	}

	return &DB{DB: sqlDB, path: path, readOnly: true}, nil
}

// OpenMemory opens an in-memory SQLite database (for testing).
func OpenMemory() (*DB, error) {
	dsn := "file::memory:?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=foreign_keys(ON)&_pragma=synchronous(FULL)&_pragma=fullfsync(ON)"
	sqlDB, err := sql.Open("sqlite3", dsn)
	if err != nil {
		return nil, fmt.Errorf("opening in-memory database: %w", err)
	}
	sqlDB.SetMaxOpenConns(1)

	return &DB{DB: sqlDB, path: ":memory:", readOnly: false}, nil
}

// Path returns the database file path.
func (d *DB) Path() string {
	return d.path
}

// Ping checks that the database is reachable.
func (d *DB) Ping(ctx context.Context) error {
	return d.DB.PingContext(ctx)
}

// IntegrityCheck runs PRAGMA integrity_check and returns an error if the database is corrupt.
func (d *DB) IntegrityCheck(ctx context.Context) error {
	var result string
	if err := d.QueryRowContext(ctx, `PRAGMA integrity_check`).Scan(&result); err != nil {
		return fmt.Errorf("running integrity check: %w", err)
	}
	if result != "ok" {
		return fmt.Errorf("database corrupt: %s", result)
	}
	return nil
}

// Reinitialize closes the current database, removes the file, and creates a fresh
// database with the full schema. For in-memory databases, it simply re-initializes
// the schema without file removal.
func (d *DB) Reinitialize(ctx context.Context) error {
	dbPath := d.path
	if d.DB != nil {
		d.Close()
	}

	if dbPath != ":memory:" {
		os.Remove(dbPath)
		os.Remove(dbPath + "-wal")
		os.Remove(dbPath + "-shm")
	}

	newDB, err := Open(dbPath)
	if err != nil {
		return fmt.Errorf("reopening database after reinitialize: %w", err)
	}
	if err := newDB.InitSchema(ctx); err != nil {
		newDB.Close()
		return fmt.Errorf("initializing schema after reinitialize: %w", err)
	}
	*d = *newDB
	return nil
}

// CheckAndReinitialize runs an integrity check and reinitializes only if corrupt.
func (d *DB) CheckAndReinitialize(ctx context.Context) error {
	if err := d.IntegrityCheck(ctx); err == nil {
		return nil
	}
	return d.Reinitialize(ctx)
}
