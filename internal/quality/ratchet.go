package quality

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"time"

	_ "github.com/ncruces/go-sqlite3/driver"
	_ "github.com/ncruces/go-sqlite3/embed"
)

// CoverageRatchetGate enforces a monotonically increasing coverage threshold.
// It reads the current threshold from the quality_ratchet table in SQLite,
// compares it against the measured coverage, and updates the threshold on pass.
// First run with no stored threshold inserts 0.0 and always passes.
type CoverageRatchetGate struct {
	DB     *sql.DB // SQLite connection (must be opened with WAL + busy_timeout)
	Runner CommandRunner
	Logger *slog.Logger
}

// Name returns the gate identifier.
func (g *CoverageRatchetGate) Name() string { return "coverage-ratchet" }

// Check measures current coverage and compares against the stored threshold.
func (g *CoverageRatchetGate) Check(ctx context.Context, projectPath string, _ string) (*LayerResult, error) {
	start := time.Now()
	g.Logger.Info("running coverage ratchet gate", "project", projectPath)

	// Ensure the quality_ratchet table exists.
	if err := g.ensureTable(ctx); err != nil {
		return nil, fmt.Errorf("ensuring quality_ratchet table: %w", err)
	}

	// Measure current coverage.
	out, err := g.Runner.RunDir(projectPath, "go", "test", "-coverprofile=coverage.out", "./...")
	duration := time.Since(start)
	output := string(out)

	result := &LayerResult{
		Layer:    g.Name(),
		Duration: duration,
	}

	if err != nil {
		result.Passed = false
		result.Details = fmt.Sprintf("tests failed (cannot measure coverage): %s", output)
		return result, nil
	}

	current := parseCoveragePercent(output)

	// Get stored threshold.
	threshold, err := g.getThreshold(ctx)
	if err != nil {
		return nil, fmt.Errorf("reading coverage threshold: %w", err)
	}

	result.Score = current

	if current < threshold {
		result.Passed = false
		result.Details = fmt.Sprintf("coverage %.1f%% is below threshold %.1f%%", current, threshold)
		g.Logger.Warn("coverage ratchet failed",
			"current", current,
			"threshold", threshold,
		)
		return result, nil
	}

	// Ratchet up: store the new high watermark.
	newThreshold := max(threshold, current)
	if err := g.setThreshold(ctx, newThreshold); err != nil {
		return nil, fmt.Errorf("updating coverage threshold: %w", err)
	}

	result.Passed = true
	result.Details = fmt.Sprintf("coverage %.1f%% meets threshold %.1f%% (ratcheted to %.1f%%)",
		current, threshold, newThreshold)
	g.Logger.Info("coverage ratchet passed",
		"current", current,
		"threshold", threshold,
		"new_threshold", newThreshold,
	)
	return result, nil
}

// ensureTable creates the quality_ratchet table if it does not exist.
func (g *CoverageRatchetGate) ensureTable(ctx context.Context) error {
	_, err := g.DB.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS quality_ratchet (
			metric TEXT PRIMARY KEY,
			threshold REAL NOT NULL DEFAULT 0.0,
			updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
		)
	`)
	if err != nil {
		return fmt.Errorf("creating quality_ratchet table: %w", err)
	}
	return nil
}

// getThreshold retrieves the stored coverage threshold. Returns 0.0 if no row exists (first run).
func (g *CoverageRatchetGate) getThreshold(ctx context.Context) (float64, error) {
	var threshold float64
	err := g.DB.QueryRowContext(ctx,
		"SELECT threshold FROM quality_ratchet WHERE metric = ?",
		"coverage_threshold",
	).Scan(&threshold)
	if err == sql.ErrNoRows {
		// First run — insert 0.0 and return it.
		_, insertErr := g.DB.ExecContext(ctx,
			"INSERT INTO quality_ratchet (metric, threshold) VALUES (?, 0.0)",
			"coverage_threshold",
		)
		if insertErr != nil {
			return 0.0, fmt.Errorf("inserting initial coverage threshold: %w", insertErr)
		}
		return 0.0, nil
	}
	if err != nil {
		return 0.0, fmt.Errorf("querying coverage threshold: %w", err)
	}
	return threshold, nil
}

// setThreshold updates the stored coverage threshold.
func (g *CoverageRatchetGate) setThreshold(ctx context.Context, value float64) error {
	_, err := g.DB.ExecContext(ctx,
		"UPDATE quality_ratchet SET threshold = ?, updated_at = CURRENT_TIMESTAMP WHERE metric = ?",
		value, "coverage_threshold",
	)
	if err != nil {
		return fmt.Errorf("updating coverage threshold: %w", err)
	}
	return nil
}

