package healing

import (
	"context"
	"errors"
	"testing"
)

func TestHealerEndToEnd(t *testing.T) {
	d := setupTestDB(t)
	h := NewHealer("sess-healer-e2e", d)
	defer h.Close()

	// Replace registry with a mock that always succeeds.
	h.Registry = &Registry{
		fixes: map[ErrorType]FixFunc{
			ErrorMissingImport: func(ctx context.Context, path string) error {
				return nil
			},
		},
	}

	result := h.Heal(context.Background(), "t-1", `could not import "fmt"`, "/tmp", []string{"main.go"}, nil)
	if !result.Fixed {
		t.Fatalf("expected Fixed=true, got err: %v", result.Err)
	}
	if result.ErrorType != ErrorMissingImport {
		t.Errorf("expected error type missing_import, got %s", result.ErrorType)
	}
	if result.FixID == "" {
		t.Error("expected non-empty FixID")
	}
}

func TestHealerBudgetExhaustion(t *testing.T) {
	d := setupTestDB(t)
	h := NewHealer("sess-healer-exhaust", d)
	defer h.Close()

	h.Registry = &Registry{
		fixes: map[ErrorType]FixFunc{
			ErrorMissingImport: func(ctx context.Context, path string) error {
				return nil
			},
		},
	}

	// Exhaust the budget.
	for i := 0; i < MaxFixesPerSession; i++ {
		result := h.Heal(context.Background(), "t-1", `could not import "os"`, "/tmp", nil, nil)
		if !result.Fixed {
			t.Fatalf("fix %d failed: %v", i, result.Err)
		}
	}

	// Next heal should fail with budget exhausted.
	result := h.Heal(context.Background(), "t-1", `could not import "os"`, "/tmp", nil, nil)
	if result.Fixed {
		t.Error("expected budget exhaustion")
	}
	if result.Err == nil || result.Err.Error() != "fix budget exhausted" {
		t.Errorf("unexpected error: %v", result.Err)
	}
}

func TestHealerUnfixableError(t *testing.T) {
	d := setupTestDB(t)
	h := NewHealer("sess-healer-unfixable", d)
	defer h.Close()

	result := h.Heal(context.Background(), "t-1", `syntax error: unexpected }`, "/tmp", nil, nil)
	if result.Fixed {
		t.Error("syntax errors should not be fixable")
	}
	if !errors.Is(result.Err, ErrNoFixAvailable) {
		t.Errorf("expected ErrNoFixAvailable, got %v", result.Err)
	}
}

func TestHealerNoParsedErrors(t *testing.T) {
	d := setupTestDB(t)
	h := NewHealer("sess-healer-noparse", d)
	defer h.Close()

	result := h.Heal(context.Background(), "t-1", "random output", "/tmp", nil, nil)
	if result.Fixed {
		t.Error("expected not fixed for unparseable output")
	}
}

func TestHealerConfirmDelegates(t *testing.T) {
	d := setupTestDB(t)
	h := NewHealer("sess-healer-confirm", d)
	defer h.Close()

	h.Registry = &Registry{
		fixes: map[ErrorType]FixFunc{
			ErrorMissingImport: func(ctx context.Context, path string) error {
				return nil
			},
		},
	}

	result := h.Heal(context.Background(), "t-1", `could not import "fmt"`, "/tmp", []string{"a.go"}, nil)
	if !result.Fixed {
		t.Fatal("expected fixed")
	}

	// Confirm should remove the timer.
	h.Confirm(result.FixID)

	h.Budget.mu.Lock()
	_, exists := h.Budget.timers[result.FixID]
	h.Budget.mu.Unlock()
	if exists {
		t.Error("timer should have been removed after Confirm")
	}
}

func TestHealerCloseClean(t *testing.T) {
	d := setupTestDB(t)
	h := NewHealer("sess-healer-close", d)

	h.Registry = &Registry{
		fixes: map[ErrorType]FixFunc{
			ErrorMissingImport: func(ctx context.Context, path string) error {
				return nil
			},
		},
	}

	_ = h.Heal(context.Background(), "t-1", `could not import "fmt"`, "/tmp", []string{"a.go"}, nil)
	h.Close()

	h.Budget.mu.Lock()
	count := len(h.Budget.timers)
	h.Budget.mu.Unlock()
	if count != 0 {
		t.Errorf("expected 0 timers after Close, got %d", count)
	}
}
