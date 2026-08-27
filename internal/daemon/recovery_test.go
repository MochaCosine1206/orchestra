package daemon

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"os"
	"testing"
	"time"

	_ "github.com/ncruces/go-sqlite3/driver"
	_ "github.com/ncruces/go-sqlite3/embed"
)

func setupRecoveryPipeline(t *testing.T) *RecoveryPipeline {
	t.Helper()
	db, err := sql.Open("sqlite3", "file::memory:?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)")
	if err != nil {
		t.Fatalf("open memory db: %v", err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { db.Close() })

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	rp := NewRecoveryPipeline(db, logger)
	if err := rp.ensureTable(context.Background()); err != nil {
		t.Fatalf("ensure table: %v", err)
	}
	return rp
}

func TestRecovery_ShouldRecover_NoAttempts(t *testing.T) {
	rp := setupRecoveryPipeline(t)
	ctx := context.Background()

	allowed, reason := rp.ShouldRecover(ctx, "work-1")
	if !allowed {
		t.Errorf("expected ShouldRecover=true with no attempts, got false: %s", reason)
	}
	if reason != "recovery allowed" {
		t.Errorf("expected reason 'recovery allowed', got %q", reason)
	}
}

func TestRecovery_ShouldRecover_InCooldown(t *testing.T) {
	rp := setupRecoveryPipeline(t)
	ctx := context.Background()

	// Record an attempt (created_at defaults to CURRENT_TIMESTAMP = now).
	err := rp.RecordAttempt(ctx, "work-2", "diagnose", "build_error", "fix imports", "partial", 0.5)
	if err != nil {
		t.Fatalf("record attempt: %v", err)
	}

	// With default 5-minute cooldown, should be in cooldown.
	allowed, reason := rp.ShouldRecover(ctx, "work-2")
	if allowed {
		t.Errorf("expected ShouldRecover=false during cooldown, got true")
	}
	if reason != "in cooldown period" {
		t.Errorf("expected reason 'in cooldown period', got %q", reason)
	}
}

func TestRecovery_ShouldRecover_MaxAttempts(t *testing.T) {
	rp := setupRecoveryPipeline(t)
	// Set cooldown to 0 so it doesn't interfere.
	rp.CooldownDuration = 0
	ctx := context.Background()

	for i := 0; i < MaxRecoveryAttempts; i++ {
		err := rp.RecordAttempt(ctx, "work-3", "diagnose", "build_error", fmt.Sprintf("fix-%d", i), "failed", 0.1)
		if err != nil {
			t.Fatalf("record attempt %d: %v", i, err)
		}
	}

	allowed, reason := rp.ShouldRecover(ctx, "work-3")
	if allowed {
		t.Errorf("expected ShouldRecover=false at max attempts, got true")
	}
	if reason != fmt.Sprintf("max attempts reached (%d/%d)", MaxRecoveryAttempts, MaxRecoveryAttempts) {
		t.Errorf("unexpected reason: %q", reason)
	}
}

func TestRecovery_Diagnose_BuildError(t *testing.T) {
	rp := setupRecoveryPipeline(t)
	ctx := context.Background()

	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{"compile error", "compile error: undefined reference to foo", "build_error"},
		{"syntax error", "line 42: syntax error near unexpected token", "build_error"},
		{"undefined", "./main.go:10: undefined: SomeFunc", "build_error"},
		{"build error", "Build error in package main", "build_error"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := rp.Diagnose(ctx, "work-diag", tt.input)
			if result != tt.expected {
				t.Errorf("Diagnose(%q) = %q, want %q", tt.input, result, tt.expected)
			}
		})
	}
}

func TestRecovery_Diagnose_TestFailure(t *testing.T) {
	rp := setupRecoveryPipeline(t)
	ctx := context.Background()

	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{"test fail", "--- FAIL: TestFoo (0.01s)", "test_failure"},
		{"assertion fail", "assertion failed: expected 3 but got 5", "test_failure"},
		{"expected got", "expected 42 got 0", "test_failure"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := rp.Diagnose(ctx, "work-diag", tt.input)
			if result != tt.expected {
				t.Errorf("Diagnose(%q) = %q, want %q", tt.input, result, tt.expected)
			}
		})
	}
}

func TestRecovery_BestScore_TracksHighest(t *testing.T) {
	rp := setupRecoveryPipeline(t)
	rp.CooldownDuration = 0
	ctx := context.Background()

	// Record attempts with varying scores.
	scores := []float64{0.3, 0.9, 0.5}
	for i, s := range scores {
		err := rp.RecordAttempt(ctx, "work-score", "evaluate", "build_error", fmt.Sprintf("fix-%d", i), "partial", s)
		if err != nil {
			t.Fatalf("record attempt: %v", err)
		}
	}

	best, err := rp.BestScore(ctx, "work-score")
	if err != nil {
		t.Fatalf("best score: %v", err)
	}
	if best != 0.9 {
		t.Errorf("expected best score 0.9, got %f", best)
	}
}

func TestRecovery_RecordAttempt_Persists(t *testing.T) {
	rp := setupRecoveryPipeline(t)
	rp.CooldownDuration = 0
	ctx := context.Background()

	err := rp.RecordAttempt(ctx, "work-persist", "diagnose", "merge_conflict", "rebase", "success", 1.0)
	if err != nil {
		t.Fatalf("record attempt: %v", err)
	}

	count, err := rp.AttemptCount(ctx, "work-persist")
	if err != nil {
		t.Fatalf("attempt count: %v", err)
	}
	if count != 1 {
		t.Errorf("expected 1 attempt, got %d", count)
	}

	// Verify row contents via direct query.
	var diagnosis, fixApplied, result string
	var score float64
	err = rp.DB.QueryRowContext(ctx,
		`SELECT diagnosis, fix_applied, result, best_score FROM recovery_attempts WHERE work_item_id = ?`,
		"work-persist",
	).Scan(&diagnosis, &fixApplied, &result, &score)
	if err != nil {
		t.Fatalf("query row: %v", err)
	}
	if diagnosis != "merge_conflict" {
		t.Errorf("expected diagnosis 'merge_conflict', got %q", diagnosis)
	}
	if fixApplied != "rebase" {
		t.Errorf("expected fix_applied 'rebase', got %q", fixApplied)
	}
	if result != "success" {
		t.Errorf("expected result 'success', got %q", result)
	}
	if score != 1.0 {
		t.Errorf("expected score 1.0, got %f", score)
	}
}

func TestRecovery_Diagnose_AllCategories(t *testing.T) {
	rp := setupRecoveryPipeline(t)
	ctx := context.Background()

	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{"merge conflict", "CONFLICT (content): Merge conflict in main.go", "merge_conflict"},
		{"conflict markers", "<<<<<<< HEAD\nsome code", "merge_conflict"},
		{"context exhaustion", "context_exhausted: hit token limit", "context_exhaustion"},
		{"runtime panic", "panic: runtime error: invalid memory address", "runtime_error"},
		{"nil pointer", "nil pointer dereference at line 42", "runtime_error"},
		{"unknown", "something went wrong in an unusual way", "unknown"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := rp.Diagnose(ctx, "work-all", tt.input)
			if result != tt.expected {
				t.Errorf("Diagnose(%q) = %q, want %q", tt.input, result, tt.expected)
			}
		})
	}
}

func TestRecovery_CooldownExpires(t *testing.T) {
	rp := setupRecoveryPipeline(t)
	ctx := context.Background()

	// Insert an attempt with a created_at far in the past.
	pastTime := time.Now().Add(-10 * time.Minute).Format("2006-01-02 15:04:05")
	_, err := rp.DB.ExecContext(ctx,
		`INSERT INTO recovery_attempts (id, work_item_id, stage, attempt_number, diagnosis, fix_applied, result, best_score, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		"rec-old-1", "work-old", "diagnose", 1, "build_error", "fix", "partial", 0.5, pastTime,
	)
	if err != nil {
		t.Fatalf("insert old attempt: %v", err)
	}

	// Should NOT be in cooldown (attempt was 10 min ago, cooldown is 5 min).
	if rp.InCooldown(ctx, "work-old") {
		t.Error("expected InCooldown=false for attempt 10 minutes ago")
	}

	// ShouldRecover should allow it.
	allowed, _ := rp.ShouldRecover(ctx, "work-old")
	if !allowed {
		t.Error("expected ShouldRecover=true after cooldown expires")
	}
}
