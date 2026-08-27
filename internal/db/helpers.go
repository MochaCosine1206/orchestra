package db

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"fmt"
	"time"
)

// GenID generates a human-readable, sortable, unique-enough ID.
// Format: PREFIX-YYYYMMDD-HHMMSS-XXXX.
func GenID(prefix string) string {
	now := time.Now()
	ts := now.Format("20060102-150405")
	b := make([]byte, 2)
	rand.Read(b)
	return fmt.Sprintf("%s-%s-%s", prefix, ts, hex.EncodeToString(b))
}

// WithTx runs fn inside a transaction. It commits on success, rolls back on error or panic.
func (d *DB) WithTx(ctx context.Context, fn func(*sql.Tx) error) error {
	if d.readOnly {
		return fmt.Errorf("cannot start transaction on read-only connection")
	}
	tx, err := d.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("beginning transaction: %w", err)
	}
	defer func() {
		if p := recover(); p != nil {
			tx.Rollback()
			panic(p)
		}
	}()
	if err := fn(tx); err != nil {
		tx.Rollback()
		return err
	}
	return tx.Commit()
}
