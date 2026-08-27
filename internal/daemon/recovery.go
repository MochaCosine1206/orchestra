package daemon

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"strings"
	"time"
)

const (
	// DefaultCooldownDuration is the minimum time between recovery attempts
	// for the same work item.
	DefaultCooldownDuration = 5 * time.Minute

	// MaxRecoveryAttempts is the maximum number of recovery attempts allowed
	// per work item before giving up.
	MaxRecoveryAttempts = 3
)

// RecoveryPipeline implements a 3-stage recovery pipeline for failed work items:
// 1. Diagnose — classify the failure type from error output
// 2. Attempt fix — record the recovery attempt with diagnosis and applied fix
// 3. Evaluate — track best score across attempts (best, not latest)
type RecoveryPipeline struct {
	DB               *sql.DB
	Logger           *slog.Logger
	CooldownDuration time.Duration
}

// NewRecoveryPipeline creates a RecoveryPipeline with default settings.
func NewRecoveryPipeline(db *sql.DB, logger *slog.Logger) *RecoveryPipeline {
	return &RecoveryPipeline{
		DB:               db,
		Logger:           logger,
		CooldownDuration: DefaultCooldownDuration,
	}
}

// ensureTable creates the recovery_attempts table if it does not exist.
func (rp *RecoveryPipeline) ensureTable(ctx context.Context) error {
	_, err := rp.DB.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS recovery_attempts (
			id TEXT PRIMARY KEY,
			work_item_id TEXT NOT NULL,
			stage TEXT NOT NULL,
			attempt_number INTEGER NOT NULL,
			diagnosis TEXT,
			fix_applied TEXT,
			result TEXT,
			best_score REAL DEFAULT 0,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP
		)
	`)
	if err != nil {
		return fmt.Errorf("creating recovery_attempts table: %w", err)
	}
	return nil
}

// ShouldRecover checks whether a recovery attempt is allowed for the given
// work item. It returns (allowed, reason). Recovery is blocked when:
// - retry_count >= MaxRecoveryAttempts
// - last attempt was within CooldownDuration
func (rp *RecoveryPipeline) ShouldRecover(ctx context.Context, workItemID string) (bool, string) {
	if err := rp.ensureTable(ctx); err != nil {
		rp.Logger.Error("recovery: ensure table failed", "error", err)
		return false, fmt.Sprintf("table init failed: %v", err)
	}

	count, err := rp.AttemptCount(ctx, workItemID)
	if err != nil {
		rp.Logger.Error("recovery: attempt count failed", "error", err)
		return false, fmt.Sprintf("attempt count failed: %v", err)
	}

	if count >= MaxRecoveryAttempts {
		return false, fmt.Sprintf("max attempts reached (%d/%d)", count, MaxRecoveryAttempts)
	}

	if rp.InCooldown(ctx, workItemID) {
		return false, "in cooldown period"
	}

	return true, "recovery allowed"
}

// Diagnose classifies a failure based on keyword matching in the error output.
// Returns one of: "build_error", "test_failure", "merge_conflict",
// "context_exhaustion", "runtime_error", "unknown".
func (rp *RecoveryPipeline) Diagnose(_ context.Context, _ string, errorOutput string) string {
	lower := strings.ToLower(errorOutput)

	switch {
	case strings.Contains(lower, "build") && strings.Contains(lower, "error"),
		strings.Contains(lower, "compile") && strings.Contains(lower, "error"),
		strings.Contains(lower, "syntax error"),
		strings.Contains(lower, "cannot find package"),
		strings.Contains(lower, "undefined:"):
		return "build_error"

	case strings.Contains(lower, "test") && strings.Contains(lower, "fail"),
		strings.Contains(lower, "--- fail"),
		strings.Contains(lower, "assertion") && strings.Contains(lower, "fail"),
		strings.Contains(lower, "expected") && strings.Contains(lower, "got"):
		return "test_failure"

	case strings.Contains(lower, "merge conflict"),
		strings.Contains(lower, "conflict") && strings.Contains(lower, "merge"),
		strings.Contains(lower, "<<<<<<"),
		strings.Contains(lower, "unmerged paths"):
		return "merge_conflict"

	case strings.Contains(lower, "context") && strings.Contains(lower, "exhaust"),
		strings.Contains(lower, "context_exhausted"),
		strings.Contains(lower, "token limit"),
		strings.Contains(lower, "max.*context"):
		return "context_exhaustion"

	case strings.Contains(lower, "runtime error"),
		strings.Contains(lower, "panic:"),
		strings.Contains(lower, "segfault"),
		strings.Contains(lower, "nil pointer"),
		strings.Contains(lower, "index out of range"):
		return "runtime_error"

	default:
		return "unknown"
	}
}

// RecordAttempt persists a recovery attempt to the database.
func (rp *RecoveryPipeline) RecordAttempt(ctx context.Context, workItemID, stage, diagnosis, fixApplied, result string, score float64) error {
	if err := rp.ensureTable(ctx); err != nil {
		return fmt.Errorf("ensuring recovery table: %w", err)
	}

	count, err := rp.AttemptCount(ctx, workItemID)
	if err != nil {
		return fmt.Errorf("getting attempt count: %w", err)
	}

	id := fmt.Sprintf("rec-%s-%d-%d", workItemID, count+1, time.Now().UnixNano())

	_, err = rp.DB.ExecContext(ctx,
		`INSERT INTO recovery_attempts (id, work_item_id, stage, attempt_number, diagnosis, fix_applied, result, best_score)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		id, workItemID, stage, count+1, diagnosis, fixApplied, result, score,
	)
	if err != nil {
		return fmt.Errorf("inserting recovery attempt: %w", err)
	}

	rp.Logger.Info("recovery: attempt recorded",
		"work_item_id", workItemID,
		"stage", stage,
		"attempt", count+1,
		"diagnosis", diagnosis,
		"score", score,
	)

	return nil
}

// BestScore returns the highest score across all recovery attempts for a work
// item. Tracks best, not latest — a subsequent worse attempt does not replace
// a prior good score.
func (rp *RecoveryPipeline) BestScore(ctx context.Context, workItemID string) (float64, error) {
	if err := rp.ensureTable(ctx); err != nil {
		return 0, fmt.Errorf("ensuring recovery table: %w", err)
	}

	var score sql.NullFloat64
	err := rp.DB.QueryRowContext(ctx,
		`SELECT MAX(best_score) FROM recovery_attempts WHERE work_item_id = ?`,
		workItemID,
	).Scan(&score)
	if err != nil {
		return 0, fmt.Errorf("querying best score: %w", err)
	}

	if !score.Valid {
		return 0, nil
	}
	return score.Float64, nil
}

// AttemptCount returns the number of recovery attempts for a work item.
func (rp *RecoveryPipeline) AttemptCount(ctx context.Context, workItemID string) (int, error) {
	if err := rp.ensureTable(ctx); err != nil {
		return 0, fmt.Errorf("ensuring recovery table: %w", err)
	}

	var count int
	err := rp.DB.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM recovery_attempts WHERE work_item_id = ?`,
		workItemID,
	).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("counting recovery attempts: %w", err)
	}

	return count, nil
}

// InCooldown returns true if the most recent recovery attempt for the work
// item was within the CooldownDuration window.
func (rp *RecoveryPipeline) InCooldown(ctx context.Context, workItemID string) bool {
	var lastAttempt sql.NullString
	err := rp.DB.QueryRowContext(ctx,
		`SELECT MAX(created_at) FROM recovery_attempts WHERE work_item_id = ?`,
		workItemID,
	).Scan(&lastAttempt)
	if err != nil || !lastAttempt.Valid {
		return false
	}

	t, err := time.Parse("2006-01-02 15:04:05", lastAttempt.String)
	if err != nil {
		return false
	}

	return time.Since(t) < rp.CooldownDuration
}
