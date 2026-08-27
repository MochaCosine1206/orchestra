// Package healthcheck implements a three-layer heartbeat system and escalation
// ladder for the Claude Orchestra dead man's switch (Phase 5).
package healthcheck

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"time"

	_ "github.com/ncruces/go-sqlite3/driver"
	_ "github.com/ncruces/go-sqlite3/embed"
)

// --- Layer 1: Agent heartbeat interface ---

// AgentHeartbeat defines the interface that the monitor package's heartbeat
// system satisfies. We define it here to avoid importing internal/monitor.
type AgentHeartbeat interface {
	// RecordHeartbeat records that an agent is alive.
	RecordHeartbeat(ctx context.Context, agentID string) error
	// LastHeartbeat returns the most recent heartbeat time for an agent.
	LastHeartbeat(ctx context.Context, agentID string) (time.Time, error)
	// IsAlive returns true if the agent has sent a heartbeat within the threshold.
	IsAlive(ctx context.Context, agentID string, threshold time.Duration) (bool, error)
}

// --- Layer 2: Conductor heartbeat ---

// ConductorHeartbeat pings itself on a schedule, writing timestamps to SQLite.
// This lets external observers detect when the conductor daemon has died.
type ConductorHeartbeat struct {
	db       *sql.DB
	interval time.Duration
	logger   *slog.Logger

	mu     sync.Mutex
	stopCh chan struct{}
	done   chan struct{}
}

// NewConductorHeartbeat creates a ConductorHeartbeat that writes to the given
// SQLite database on the specified interval.
func NewConductorHeartbeat(db *sql.DB, interval time.Duration, logger *slog.Logger) *ConductorHeartbeat {
	return &ConductorHeartbeat{
		db:       db,
		interval: interval,
		logger:   logger,
	}
}

// ensureConductorHeartbeatTable creates the conductor_heartbeats table if it
// does not exist.
func ensureConductorHeartbeatTable(ctx context.Context, db *sql.DB) error {
	_, err := db.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS conductor_heartbeats (
			id          INTEGER PRIMARY KEY AUTOINCREMENT,
			recorded_at DATETIME DEFAULT CURRENT_TIMESTAMP
		)
	`)
	if err != nil {
		return fmt.Errorf("creating conductor_heartbeats table: %w", err)
	}
	return nil
}

// Start begins the periodic heartbeat loop. It is safe to call only once;
// call Stop to terminate.
func (ch *ConductorHeartbeat) Start(ctx context.Context) error {
	if err := ensureConductorHeartbeatTable(ctx, ch.db); err != nil {
		return fmt.Errorf("ensuring conductor heartbeat table: %w", err)
	}

	ch.mu.Lock()
	ch.stopCh = make(chan struct{})
	ch.done = make(chan struct{})
	ch.mu.Unlock()

	go ch.loop(ctx)
	return nil
}

func (ch *ConductorHeartbeat) loop(ctx context.Context) {
	defer close(ch.done)

	ticker := time.NewTicker(ch.interval)
	defer ticker.Stop()

	// Record immediately on start.
	if err := ch.Record(ctx); err != nil {
		ch.logger.Error("conductor heartbeat failed", "error", err)
	}

	for {
		select {
		case <-ticker.C:
			if err := ch.Record(ctx); err != nil {
				ch.logger.Error("conductor heartbeat failed", "error", err)
			}
		case <-ch.stopCh:
			return
		case <-ctx.Done():
			return
		}
	}
}

// Record writes a single heartbeat timestamp to SQLite.
func (ch *ConductorHeartbeat) Record(ctx context.Context) error {
	_, err := ch.db.ExecContext(ctx, `INSERT INTO conductor_heartbeats (recorded_at) VALUES (CURRENT_TIMESTAMP)`)
	if err != nil {
		return fmt.Errorf("recording conductor heartbeat: %w", err)
	}
	ch.logger.Debug("conductor heartbeat recorded")
	return nil
}

// LastRecorded returns the most recent heartbeat timestamp from SQLite.
func (ch *ConductorHeartbeat) LastRecorded(ctx context.Context) (time.Time, error) {
	var ts string
	err := ch.db.QueryRowContext(ctx,
		`SELECT recorded_at FROM conductor_heartbeats ORDER BY id DESC LIMIT 1`,
	).Scan(&ts)
	if err != nil {
		return time.Time{}, fmt.Errorf("querying last conductor heartbeat: %w", err)
	}
	// SQLite CURRENT_TIMESTAMP may return either "2006-01-02 15:04:05" or
	// "2006-01-02T15:04:05Z" depending on the driver.
	for _, layout := range []string{"2006-01-02 15:04:05", time.RFC3339, "2006-01-02T15:04:05Z"} {
		if t, err := time.Parse(layout, ts); err == nil {
			return t, nil
		}
	}
	return time.Time{}, fmt.Errorf("parsing conductor heartbeat timestamp %q: unsupported format", ts)
}

// Stop terminates the heartbeat loop and waits for it to finish.
func (ch *ConductorHeartbeat) Stop() {
	ch.mu.Lock()
	if ch.stopCh != nil {
		close(ch.stopCh)
	}
	done := ch.done
	ch.mu.Unlock()
	if done != nil {
		<-done
	}
}

// --- Layer 3: External ping ---

// ExternalPing sends periodic pings to an external webhook URL so that a
// third-party monitoring service can detect when Orchestra is down.
type ExternalPing struct {
	webhookURL string
	interval   time.Duration
	client     *http.Client
	logger     *slog.Logger

	mu       sync.Mutex
	lastPing time.Time
	stopCh   chan struct{}
	done     chan struct{}
}

// NewExternalPing creates an ExternalPing that hits the given webhook URL on
// the specified interval.
func NewExternalPing(webhookURL string, interval time.Duration, logger *slog.Logger) *ExternalPing {
	return &ExternalPing{
		webhookURL: webhookURL,
		interval:   interval,
		client:     &http.Client{Timeout: 10 * time.Second},
		logger:     logger,
	}
}

// Start begins the periodic ping loop.
func (ep *ExternalPing) Start(ctx context.Context) {
	ep.mu.Lock()
	ep.stopCh = make(chan struct{})
	ep.done = make(chan struct{})
	ep.mu.Unlock()

	go ep.loop(ctx)
}

func (ep *ExternalPing) loop(ctx context.Context) {
	defer close(ep.done)

	ticker := time.NewTicker(ep.interval)
	defer ticker.Stop()

	// Ping immediately.
	ep.ping(ctx)

	for {
		select {
		case <-ticker.C:
			ep.ping(ctx)
		case <-ep.stopCh:
			return
		case <-ctx.Done():
			return
		}
	}
}

func (ep *ExternalPing) ping(ctx context.Context) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, ep.webhookURL, nil)
	if err != nil {
		ep.logger.Error("creating external ping request", "error", err)
		return
	}
	resp, err := ep.client.Do(req)
	if err != nil {
		ep.logger.Error("external ping failed", "url", ep.webhookURL, "error", err)
		return
	}
	resp.Body.Close()

	ep.mu.Lock()
	ep.lastPing = time.Now()
	ep.mu.Unlock()

	ep.logger.Debug("external ping sent", "url", ep.webhookURL, "status", resp.StatusCode)
}

// LastPingTime returns the timestamp of the last successful ping.
func (ep *ExternalPing) LastPingTime() time.Time {
	ep.mu.Lock()
	defer ep.mu.Unlock()
	return ep.lastPing
}

// DetectMissedPing returns true if the last successful ping was longer ago
// than the given threshold.
func (ep *ExternalPing) DetectMissedPing(threshold time.Duration) bool {
	ep.mu.Lock()
	lp := ep.lastPing
	ep.mu.Unlock()

	if lp.IsZero() {
		return true
	}
	return time.Since(lp) > threshold
}

// Stop terminates the ping loop and waits for it to finish.
func (ep *ExternalPing) Stop() {
	ep.mu.Lock()
	if ep.stopCh != nil {
		close(ep.stopCh)
	}
	done := ep.done
	ep.mu.Unlock()
	if done != nil {
		<-done
	}
}
