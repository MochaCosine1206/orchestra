package healthcheck

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"sync"
	"time"
)

// EscalationLevel represents one of the 4 escalation tiers.
type EscalationLevel int

const (
	// LevelSelfHeal restarts a failed agent.
	LevelSelfHeal EscalationLevel = 0
	// LevelDegrade reduces concurrency and sends a Telegram alert.
	LevelDegrade EscalationLevel = 1
	// LevelPause stops spawning new work and sends a Telegram alert.
	LevelPause EscalationLevel = 2
	// LevelShutdown kills all agents, stops the daemon, and sends a Telegram alert.
	LevelShutdown EscalationLevel = 3
)

// String returns the human-readable name for an escalation level.
func (l EscalationLevel) String() string {
	switch l {
	case LevelSelfHeal:
		return "self-heal"
	case LevelDegrade:
		return "degrade"
	case LevelPause:
		return "pause"
	case LevelShutdown:
		return "shutdown"
	default:
		return fmt.Sprintf("unknown(%d)", l)
	}
}

// EscalationEntry records a single escalation event.
type EscalationEntry struct {
	Level     EscalationLevel
	Reason    string
	Timestamp time.Time
}

// EscalationAction is a callback invoked when the escalation level changes.
// The callback receives the new level and the reason for escalation.
type EscalationAction func(ctx context.Context, level EscalationLevel, reason string) error

// EscalationManager tracks the current escalation level, maintains a history
// of escalation events, and triggers registered actions at each level.
type EscalationManager struct {
	db      *sql.DB
	logger  *slog.Logger
	actions map[EscalationLevel]EscalationAction

	mu      sync.Mutex
	level   EscalationLevel
	history []EscalationEntry
}

// NewEscalationManager creates an EscalationManager starting at LevelSelfHeal.
func NewEscalationManager(db *sql.DB, logger *slog.Logger) *EscalationManager {
	return &EscalationManager{
		db:      db,
		logger:  logger,
		level:   LevelSelfHeal,
		actions: make(map[EscalationLevel]EscalationAction),
	}
}

// ensureEscalationTable creates the escalation_history table if it does not
// exist.
func ensureEscalationTable(ctx context.Context, db *sql.DB) error {
	_, err := db.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS escalation_history (
			id         INTEGER PRIMARY KEY AUTOINCREMENT,
			level      INTEGER NOT NULL,
			level_name TEXT    NOT NULL,
			reason     TEXT    NOT NULL,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP
		)
	`)
	if err != nil {
		return fmt.Errorf("creating escalation_history table: %w", err)
	}
	return nil
}

// Init ensures the backing SQLite table exists.
func (em *EscalationManager) Init(ctx context.Context) error {
	return ensureEscalationTable(ctx, em.db)
}

// RegisterAction sets the callback for a given escalation level.
func (em *EscalationManager) RegisterAction(level EscalationLevel, action EscalationAction) {
	em.mu.Lock()
	defer em.mu.Unlock()
	em.actions[level] = action
}

// Level returns the current escalation level.
func (em *EscalationManager) Level() EscalationLevel {
	em.mu.Lock()
	defer em.mu.Unlock()
	return em.level
}

// History returns a copy of the escalation history.
func (em *EscalationManager) History() []EscalationEntry {
	em.mu.Lock()
	defer em.mu.Unlock()
	h := make([]EscalationEntry, len(em.history))
	copy(h, em.history)
	return h
}

// Escalate moves up one level (unless already at LevelShutdown) and triggers
// the action registered for the new level. The escalation is recorded in
// both in-memory history and SQLite.
func (em *EscalationManager) Escalate(ctx context.Context, reason string) error {
	em.mu.Lock()

	if em.level >= LevelShutdown {
		em.mu.Unlock()
		return fmt.Errorf("already at maximum escalation level (shutdown)")
	}

	em.level++
	newLevel := em.level
	entry := EscalationEntry{
		Level:     newLevel,
		Reason:    reason,
		Timestamp: time.Now(),
	}
	em.history = append(em.history, entry)
	action := em.actions[newLevel]

	em.mu.Unlock()

	em.logger.Warn("escalation triggered",
		"level", newLevel.String(),
		"reason", reason,
	)

	// Persist to SQLite.
	if err := em.recordEscalation(ctx, entry); err != nil {
		em.logger.Error("recording escalation to DB", "error", err)
	}

	// Fire action if registered.
	if action != nil {
		if err := action(ctx, newLevel, reason); err != nil {
			return fmt.Errorf("escalation action for level %s: %w", newLevel, err)
		}
	}

	return nil
}

// DeEscalate moves down one level if conditions improve. It does not trigger
// any action — de-escalation is passive.
func (em *EscalationManager) DeEscalate() {
	em.mu.Lock()
	defer em.mu.Unlock()

	if em.level <= LevelSelfHeal {
		return
	}

	em.level--
	em.logger.Info("de-escalated", "level", em.level.String())
}

func (em *EscalationManager) recordEscalation(ctx context.Context, entry EscalationEntry) error {
	_, err := em.db.ExecContext(ctx, `
		INSERT INTO escalation_history (level, level_name, reason, created_at)
		VALUES (?, ?, ?, ?)
	`, entry.Level, entry.Level.String(), entry.Reason, entry.Timestamp.UTC().Format("2006-01-02 15:04:05"))
	if err != nil {
		return fmt.Errorf("inserting escalation entry: %w", err)
	}
	return nil
}
