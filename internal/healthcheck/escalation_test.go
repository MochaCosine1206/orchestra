package healthcheck

import (
	"context"
	"log/slog"
	"testing"
)

func TestEscalationManager_Init(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	logger := slog.Default()

	em := NewEscalationManager(db, logger)
	if err := em.Init(ctx); err != nil {
		t.Fatalf("init: %v", err)
	}

	if em.Level() != LevelSelfHeal {
		t.Errorf("expected initial level %v, got %v", LevelSelfHeal, em.Level())
	}
}

func TestEscalationManager_EscalateThroughLevels(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	logger := slog.Default()

	em := NewEscalationManager(db, logger)
	if err := em.Init(ctx); err != nil {
		t.Fatalf("init: %v", err)
	}

	levels := []EscalationLevel{LevelDegrade, LevelPause, LevelShutdown}
	for i, expected := range levels {
		if err := em.Escalate(ctx, "test escalation"); err != nil {
			t.Fatalf("escalate step %d: %v", i, err)
		}
		if em.Level() != expected {
			t.Errorf("step %d: expected level %v, got %v", i, expected, em.Level())
		}
	}

	// Escalating past shutdown should return an error.
	if err := em.Escalate(ctx, "beyond shutdown"); err == nil {
		t.Error("expected error when escalating past shutdown")
	}
}

func TestEscalationManager_DeEscalate(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	logger := slog.Default()

	em := NewEscalationManager(db, logger)
	if err := em.Init(ctx); err != nil {
		t.Fatalf("init: %v", err)
	}

	// Escalate to Degrade.
	if err := em.Escalate(ctx, "test"); err != nil {
		t.Fatalf("escalate: %v", err)
	}
	if em.Level() != LevelDegrade {
		t.Fatalf("expected LevelDegrade, got %v", em.Level())
	}

	// De-escalate back to SelfHeal.
	em.DeEscalate()
	if em.Level() != LevelSelfHeal {
		t.Errorf("expected LevelSelfHeal after de-escalate, got %v", em.Level())
	}

	// De-escalating at bottom should be a no-op.
	em.DeEscalate()
	if em.Level() != LevelSelfHeal {
		t.Errorf("expected LevelSelfHeal at floor, got %v", em.Level())
	}
}

func TestEscalationManager_History(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	logger := slog.Default()

	em := NewEscalationManager(db, logger)
	if err := em.Init(ctx); err != nil {
		t.Fatalf("init: %v", err)
	}

	if err := em.Escalate(ctx, "first"); err != nil {
		t.Fatalf("escalate: %v", err)
	}
	if err := em.Escalate(ctx, "second"); err != nil {
		t.Fatalf("escalate: %v", err)
	}

	h := em.History()
	if len(h) != 2 {
		t.Fatalf("expected 2 history entries, got %d", len(h))
	}
	if h[0].Reason != "first" || h[1].Reason != "second" {
		t.Errorf("history reasons mismatch: %v", h)
	}
}

func TestEscalationManager_ActionCallback(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	logger := slog.Default()

	em := NewEscalationManager(db, logger)
	if err := em.Init(ctx); err != nil {
		t.Fatalf("init: %v", err)
	}

	var calledLevel EscalationLevel
	var calledReason string
	em.RegisterAction(LevelDegrade, func(_ context.Context, level EscalationLevel, reason string) error {
		calledLevel = level
		calledReason = reason
		return nil
	})

	if err := em.Escalate(ctx, "test callback"); err != nil {
		t.Fatalf("escalate: %v", err)
	}

	if calledLevel != LevelDegrade {
		t.Errorf("expected action called at LevelDegrade, got %v", calledLevel)
	}
	if calledReason != "test callback" {
		t.Errorf("expected reason 'test callback', got %q", calledReason)
	}
}

func TestEscalationManager_PersistsToSQLite(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	logger := slog.Default()

	em := NewEscalationManager(db, logger)
	if err := em.Init(ctx); err != nil {
		t.Fatalf("init: %v", err)
	}

	if err := em.Escalate(ctx, "persist test"); err != nil {
		t.Fatalf("escalate: %v", err)
	}

	var count int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM escalation_history`).Scan(&count); err != nil {
		t.Fatalf("counting escalation rows: %v", err)
	}
	if count != 1 {
		t.Errorf("expected 1 row in escalation_history, got %d", count)
	}

	var levelName, reason string
	if err := db.QueryRowContext(ctx,
		`SELECT level_name, reason FROM escalation_history ORDER BY id DESC LIMIT 1`,
	).Scan(&levelName, &reason); err != nil {
		t.Fatalf("reading escalation row: %v", err)
	}
	if levelName != "degrade" {
		t.Errorf("expected level_name 'degrade', got %q", levelName)
	}
	if reason != "persist test" {
		t.Errorf("expected reason 'persist test', got %q", reason)
	}
}

func TestEscalationLevel_String(t *testing.T) {
	tests := []struct {
		level EscalationLevel
		want  string
	}{
		{LevelSelfHeal, "self-heal"},
		{LevelDegrade, "degrade"},
		{LevelPause, "pause"},
		{LevelShutdown, "shutdown"},
		{EscalationLevel(99), "unknown(99)"},
	}
	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			if got := tt.level.String(); got != tt.want {
				t.Errorf("String() = %q, want %q", got, tt.want)
			}
		})
	}
}
