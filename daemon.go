package orchestra

import (
	"context"
	_ "embed"
	"fmt"
	"path/filepath"

	"github.com/MochaCosine1206/orchestra/internal/config"
	"github.com/MochaCosine1206/orchestra/internal/db"
)

//go:embed 012_daemon.sql
var daemonSchema string

//go:embed 013_priority_system.sql
var prioritySchema string

// OpenDaemonDB opens (or creates) the daemon database at ~/.config/orchestra/daemon.db.
// It reuses the db.DB type with the same WAL/busy_timeout pragmas as the project DB.
func OpenDaemonDB() (*db.DB, error) {
	dir, err := config.GlobalDir()
	if err != nil {
		return nil, fmt.Errorf("resolving global config dir: %w", err)
	}
	dbPath := filepath.Join(dir, "daemon.db")
	return db.Open(dbPath)
}

// InitDaemonSchema applies the daemon-specific schema (migration 012) to the given database.
// This does NOT apply any project-level migrations — only the 5 daemon tables.
func InitDaemonSchema(ctx context.Context, d *db.DB) error {
	_, err := d.ExecContext(ctx, daemonSchema)
	if err != nil {
		return fmt.Errorf("initializing daemon schema: %w", err)
	}
	_, err = d.ExecContext(ctx, prioritySchema)
	if err != nil {
		return fmt.Errorf("initializing priority schema: %w", err)
	}
	return nil
}
