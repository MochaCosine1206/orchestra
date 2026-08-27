package healthcheck

import (
	"context"
	"database/sql"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	_ "github.com/ncruces/go-sqlite3/driver"
	_ "github.com/ncruces/go-sqlite3/embed"
)

func openTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("opening test db: %v", err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { db.Close() })
	return db
}

func TestConductorHeartbeat_RecordAndLastRecorded(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	logger := slog.Default()

	ch := NewConductorHeartbeat(db, 1*time.Hour, logger) // long interval, we call Record manually

	if err := ensureConductorHeartbeatTable(ctx, db); err != nil {
		t.Fatalf("ensure table: %v", err)
	}

	if err := ch.Record(ctx); err != nil {
		t.Fatalf("first record: %v", err)
	}

	ts, err := ch.LastRecorded(ctx)
	if err != nil {
		t.Fatalf("last recorded: %v", err)
	}

	if time.Since(ts) > 5*time.Second {
		t.Errorf("last recorded timestamp too old: %v", ts)
	}
}

func TestConductorHeartbeat_StartStop(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	logger := slog.Default()

	ch := NewConductorHeartbeat(db, 50*time.Millisecond, logger)

	if err := ch.Start(ctx); err != nil {
		t.Fatalf("start: %v", err)
	}

	// Wait for a few heartbeats.
	time.Sleep(200 * time.Millisecond)
	ch.Stop()

	// Should have at least 2 heartbeats (initial + ticks).
	var count int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM conductor_heartbeats`).Scan(&count); err != nil {
		t.Fatalf("counting heartbeats: %v", err)
	}
	if count < 2 {
		t.Errorf("expected at least 2 heartbeats, got %d", count)
	}
}

func TestConductorHeartbeat_LastRecordedEmpty(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	logger := slog.Default()

	ch := NewConductorHeartbeat(db, 1*time.Hour, logger)
	if err := ensureConductorHeartbeatTable(ctx, db); err != nil {
		t.Fatalf("ensure table: %v", err)
	}

	_, err := ch.LastRecorded(ctx)
	if err == nil {
		t.Error("expected error for empty table, got nil")
	}
}

func TestExternalPing_DetectMissedPing_NoPingSent(t *testing.T) {
	logger := slog.Default()
	ep := NewExternalPing("http://localhost:0/noop", 1*time.Hour, logger)

	// No ping has been sent — should report missed.
	if !ep.DetectMissedPing(1 * time.Second) {
		t.Error("expected missed ping when no ping has been sent")
	}
}

func TestExternalPing_PingAndDetect(t *testing.T) {
	hits := 0
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()

	logger := slog.Default()
	ep := NewExternalPing(ts.URL, 50*time.Millisecond, logger)

	ctx, cancel := context.WithCancel(context.Background())
	ep.Start(ctx)

	// Wait for a few pings.
	time.Sleep(200 * time.Millisecond)
	cancel()
	ep.Stop()

	if hits < 2 {
		t.Errorf("expected at least 2 pings, got %d", hits)
	}

	if ep.DetectMissedPing(5 * time.Second) {
		t.Error("should not detect missed ping immediately after pinging")
	}
}
