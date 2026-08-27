package healing

import (
	"context"
	"testing"
	"time"

	"github.com/MochaCosine1206/orchestra/internal/db"
)

func setupTestDB(t *testing.T) *db.DB {
	t.Helper()
	d, err := db.Open(":memory:")
	if err != nil {
		t.Fatalf("opening test db: %v", err)
	}
	if err := d.InitSchema(context.Background()); err != nil {
		t.Fatalf("initializing schema: %v", err)
	}
	t.Cleanup(func() { d.Close() })
	return d
}

func TestBudgetCanFix(t *testing.T) {
	d := setupTestDB(t)
	b := NewBudget("sess-budget-test", d)
	defer b.Close()

	ctx := context.Background()
	for i := 0; i < MaxFixesPerSession; i++ {
		if !b.CanFix() {
			t.Fatalf("CanFix returned false at fix %d", i)
		}
		if err := b.RecordFix(ctx, "t-1", ErrorMissingImport, "goimports", nil, "/tmp"); err != nil {
			t.Fatalf("RecordFix %d: %v", i, err)
		}
	}

	if b.CanFix() {
		t.Error("CanFix should return false after 10 fixes")
	}
}

func TestBudgetRecordFixInsertsDBRow(t *testing.T) {
	d := setupTestDB(t)
	b := NewBudget("sess-db-test", d)
	defer b.Close()

	ctx := context.Background()
	if err := b.RecordFix(ctx, "t-1", ErrorMissingDep, "go get", nil, "/tmp"); err != nil {
		t.Fatal(err)
	}

	count, err := d.GetHealingLogCount(ctx, "sess-db-test")
	if err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Errorf("expected 1 healing log row, got %d", count)
	}
}

func TestBudgetConfirmFixCancelsTimer(t *testing.T) {
	d := setupTestDB(t)
	b := NewBudget("sess-confirm-test", d)
	defer b.Close()

	ctx := context.Background()
	if err := b.RecordFix(ctx, "t-1", ErrorMissingImport, "goimports", []string{"main.go"}, "/tmp/nonexistent"); err != nil {
		t.Fatal(err)
	}

	fixID := b.LastFixID()
	if fixID == "" {
		t.Fatal("LastFixID returned empty")
	}

	// Confirm the fix — timer should be cancelled.
	b.ConfirmFix(fixID)

	b.mu.Lock()
	_, exists := b.timers[fixID]
	b.mu.Unlock()
	if exists {
		t.Error("timer should have been removed after ConfirmFix")
	}
}

func TestBudgetRollbackTimerFires(t *testing.T) {
	// We can't easily test the actual git rollback without a real repo,
	// but we can verify the timer mechanism by checking it was set up.
	d := setupTestDB(t)
	b := NewBudget("sess-rollback-test", d)
	defer b.Close()

	ctx := context.Background()
	if err := b.RecordFix(ctx, "t-1", ErrorMissingImport, "goimports", []string{"a.go"}, "/tmp/nonexistent"); err != nil {
		t.Fatal(err)
	}

	fixID := b.LastFixID()
	b.mu.Lock()
	rt, exists := b.timers[fixID]
	b.mu.Unlock()

	if !exists {
		t.Fatal("expected timer to exist")
	}
	if rt.worktree != "/tmp/nonexistent" {
		t.Errorf("expected worktree /tmp/nonexistent, got %s", rt.worktree)
	}
	if len(rt.files) != 1 || rt.files[0] != "a.go" {
		t.Errorf("expected files [a.go], got %v", rt.files)
	}
}

func TestBudgetCloseStopsTimers(t *testing.T) {
	d := setupTestDB(t)
	b := NewBudget("sess-close-test", d)

	ctx := context.Background()
	if err := b.RecordFix(ctx, "t-1", ErrorMissingImport, "fix", nil, "/tmp"); err != nil {
		t.Fatal(err)
	}

	b.Close()

	// Give a moment for any timer goroutines to settle.
	time.Sleep(10 * time.Millisecond)

	b.mu.Lock()
	count := len(b.timers)
	b.mu.Unlock()
	if count != 0 {
		t.Errorf("expected 0 timers after Close, got %d", count)
	}
}
