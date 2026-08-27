package governor

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"time"

	_ "github.com/ncruces/go-sqlite3/driver"
	_ "github.com/ncruces/go-sqlite3/embed"
)

// ensureBudgetTable creates the token_usage table if it does not exist.
func ensureBudgetTable(ctx context.Context, db *sql.DB) error {
	_, err := db.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS token_usage (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			task_id TEXT NOT NULL,
			agent_id TEXT NOT NULL DEFAULT '',
			tokens_input INTEGER NOT NULL DEFAULT 0,
			tokens_output INTEGER NOT NULL DEFAULT 0,
			recorded_at DATETIME DEFAULT CURRENT_TIMESTAMP
		)
	`)
	if err != nil {
		return fmt.Errorf("creating token_usage table: %w", err)
	}
	return nil
}

// RecordUsage inserts a token usage record for a task and agent.
func (rg *ResourceGovernor) RecordUsage(ctx context.Context, taskID, agentID string, tokensInput, tokensOutput int64) error {
	if err := ensureBudgetTable(ctx, rg.DB); err != nil {
		return fmt.Errorf("ensuring budget table: %w", err)
	}

	_, err := rg.DB.ExecContext(ctx,
		"INSERT INTO token_usage (task_id, agent_id, tokens_input, tokens_output) VALUES (?, ?, ?, ?)",
		taskID, agentID, tokensInput, tokensOutput,
	)
	if err != nil {
		return fmt.Errorf("recording token usage: %w", err)
	}

	rg.Logger.Info("recorded token usage",
		"task_id", taskID,
		"agent_id", agentID,
		"tokens_input", tokensInput,
		"tokens_output", tokensOutput,
	)
	return nil
}

// GetDailyStatus returns the budget status for the current calendar day (UTC).
func (rg *ResourceGovernor) GetDailyStatus(ctx context.Context) (*BudgetStatus, error) {
	if err := ensureBudgetTable(ctx, rg.DB); err != nil {
		return nil, fmt.Errorf("ensuring budget table: %w", err)
	}

	today := time.Now().UTC().Format("2006-01-02")
	var totalTokens int64
	err := rg.DB.QueryRowContext(ctx,
		"SELECT COALESCE(SUM(tokens_input + tokens_output), 0) FROM token_usage WHERE date(recorded_at) = ?",
		today,
	).Scan(&totalTokens)
	if err != nil {
		return nil, fmt.Errorf("querying daily token usage: %w", err)
	}

	return rg.buildStatus(totalTokens, rg.Config.DailyTokenLimit), nil
}

// CanSpawnTask returns true if the daily budget allows spawning another task.
// It also checks the warn threshold and logs a warning if close to limit.
func (rg *ResourceGovernor) CanSpawnTask(ctx context.Context) (bool, error) {
	status, err := rg.GetDailyStatus(ctx)
	if err != nil {
		return false, fmt.Errorf("checking daily status for spawn: %w", err)
	}

	if status.OverBudget {
		rg.Logger.Warn("daily token budget exceeded",
			"used", status.TokensUsed,
			"limit", status.TokenLimit,
		)
		return false, nil
	}

	if rg.Config.WarnThreshold > 0 && status.UtilizationPct >= rg.Config.WarnThreshold {
		rg.Logger.Warn("daily token budget approaching limit",
			"utilization_pct", status.UtilizationPct,
			"used", status.TokensUsed,
			"limit", status.TokenLimit,
		)
	}

	return true, nil
}

// GetTaskUsage returns the budget status for a specific task.
func (rg *ResourceGovernor) GetTaskUsage(ctx context.Context, taskID string) (*BudgetStatus, error) {
	if err := ensureBudgetTable(ctx, rg.DB); err != nil {
		return nil, fmt.Errorf("ensuring budget table: %w", err)
	}

	var totalTokens int64
	err := rg.DB.QueryRowContext(ctx,
		"SELECT COALESCE(SUM(tokens_input + tokens_output), 0) FROM token_usage WHERE task_id = ?",
		taskID,
	).Scan(&totalTokens)
	if err != nil {
		return nil, fmt.Errorf("querying task token usage for %s: %w", taskID, err)
	}

	return rg.buildStatus(totalTokens, rg.Config.TaskTokenLimit), nil
}

// buildStatus constructs a BudgetStatus from usage and limit values.
func (rg *ResourceGovernor) buildStatus(used, limit int64) *BudgetStatus {
	s := &BudgetStatus{
		TokensUsed: used,
		TokenLimit: limit,
	}
	if limit <= 0 {
		s.Remaining = -1
		s.UtilizationPct = 0
		s.OverBudget = false
	} else {
		s.Remaining = limit - used
		if s.Remaining < 0 {
			s.Remaining = 0
		}
		s.UtilizationPct = float64(used) / float64(limit)
		s.OverBudget = used >= limit
	}
	return s
}

// openTestDB opens an in-memory SQLite database for testing.
func openTestDB() (*sql.DB, error) {
	db, err := sql.Open("sqlite3", "file::memory:?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=foreign_keys(ON)")
	if err != nil {
		return nil, fmt.Errorf("opening test database: %w", err)
	}
	return db, nil
}

// newTestGovernor creates a ResourceGovernor backed by in-memory SQLite for tests.
func newTestGovernor() (*ResourceGovernor, error) {
	db, err := openTestDB()
	if err != nil {
		return nil, err
	}
	logger := slog.Default()
	cfg := DefaultBudgetConfig()
	return NewResourceGovernor(db, cfg, logger), nil
}
