package daemon

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"testing"
	"time"

	_ "github.com/ncruces/go-sqlite3/driver"
	_ "github.com/ncruces/go-sqlite3/embed"
)

func setupDecisionQueue(t *testing.T) *DecisionQueue {
	t.Helper()
	db, err := sql.Open("sqlite3", "file::memory:?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=foreign_keys(ON)")
	if err != nil {
		t.Fatalf("open memory db: %v", err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { db.Close() })

	dq := &DecisionQueue{
		DB:     db,
		Logger: slog.Default(),
	}
	if err := dq.ensureTable(context.Background()); err != nil {
		t.Fatalf("ensure table: %v", err)
	}
	return dq
}

func TestDecisionSubmitAndPending(t *testing.T) {
	dq := setupDecisionQueue(t)
	ctx := context.Background()

	id, err := dq.Submit(ctx, "wi-1", "Should we proceed?", "Some context", []string{"yes", "no"})
	if err != nil {
		t.Fatalf("submit: %v", err)
	}
	if id == "" {
		t.Fatal("expected non-empty id")
	}

	pending, err := dq.PendingDecisions(ctx)
	if err != nil {
		t.Fatalf("pending: %v", err)
	}
	if len(pending) != 1 {
		t.Fatalf("expected 1 pending, got %d", len(pending))
	}
	if pending[0].ID != id {
		t.Errorf("expected id %s, got %s", id, pending[0].ID)
	}
	if pending[0].Question != "Should we proceed?" {
		t.Errorf("unexpected question: %s", pending[0].Question)
	}
	if pending[0].WorkItemID != "wi-1" {
		t.Errorf("unexpected work_item_id: %s", pending[0].WorkItemID)
	}
	if len(pending[0].Options) != 2 || pending[0].Options[0] != "yes" {
		t.Errorf("unexpected options: %v", pending[0].Options)
	}
}

func TestDecisionDecideAndVerify(t *testing.T) {
	dq := setupDecisionQueue(t)
	ctx := context.Background()

	id, err := dq.Submit(ctx, "wi-2", "Pick a color", "", []string{"red", "blue"})
	if err != nil {
		t.Fatalf("submit: %v", err)
	}

	if err := dq.Decide(ctx, id, "blue"); err != nil {
		t.Fatalf("decide: %v", err)
	}

	// Verify decided_at is set.
	var decidedAt sql.NullString
	err = dq.DB.QueryRowContext(ctx, "SELECT decided_at FROM decision_queue WHERE id = ?", id).Scan(&decidedAt)
	if err != nil {
		t.Fatalf("query decided_at: %v", err)
	}
	if !decidedAt.Valid {
		t.Error("expected decided_at to be set")
	}

	// GetDecision should return the decision.
	dec, ok, err := dq.GetDecision(ctx, "wi-2")
	if err != nil {
		t.Fatalf("get decision: %v", err)
	}
	if !ok {
		t.Error("expected decision to be found")
	}
	if dec != "blue" {
		t.Errorf("expected 'blue', got %q", dec)
	}
}

func TestDecisionPendingExcludesDecided(t *testing.T) {
	dq := setupDecisionQueue(t)
	ctx := context.Background()

	id1, _ := dq.Submit(ctx, "wi-3", "Q1?", "", nil)
	_, _ = dq.Submit(ctx, "wi-4", "Q2?", "", nil)

	// Decide the first one.
	if err := dq.Decide(ctx, id1, "done"); err != nil {
		t.Fatalf("decide: %v", err)
	}

	pending, err := dq.PendingDecisions(ctx)
	if err != nil {
		t.Fatalf("pending: %v", err)
	}
	if len(pending) != 1 {
		t.Fatalf("expected 1 pending, got %d", len(pending))
	}
	if pending[0].WorkItemID != "wi-4" {
		t.Errorf("expected wi-4, got %s", pending[0].WorkItemID)
	}
}

func TestDecisionGetDecisionNotDecided(t *testing.T) {
	dq := setupDecisionQueue(t)
	ctx := context.Background()

	_, _ = dq.Submit(ctx, "wi-5", "Pending question", "", nil)

	dec, ok, err := dq.GetDecision(ctx, "wi-5")
	if err != nil {
		t.Fatalf("get decision: %v", err)
	}
	if ok {
		t.Errorf("expected not decided, got %q", dec)
	}
}

func TestDecisionGetDecisionNotFound(t *testing.T) {
	dq := setupDecisionQueue(t)
	ctx := context.Background()

	_, ok, err := dq.GetDecision(ctx, "nonexistent")
	if err != nil {
		t.Fatalf("get decision: %v", err)
	}
	if ok {
		t.Error("expected not found")
	}
}

func TestDecisionAutoDecideStale(t *testing.T) {
	dq := setupDecisionQueue(t)
	ctx := context.Background()

	// Insert two items: one old, one recent.
	id1, _ := dq.Submit(ctx, "wi-old", "Old question", "", nil)
	id2, _ := dq.Submit(ctx, "wi-new", "New question", "", nil)

	// Backdate the first item to 3 days ago.
	old := time.Now().Add(-72 * time.Hour).UTC().Format("2006-01-02 15:04:05")
	_, err := dq.DB.ExecContext(ctx, "UPDATE decision_queue SET created_at = ? WHERE id = ?", old, id1)
	if err != nil {
		t.Fatalf("backdate: %v", err)
	}

	// Auto-decide with 48h threshold.
	n, err := dq.AutoDecideStale(ctx, 48*time.Hour, "auto-skip")
	if err != nil {
		t.Fatalf("auto-decide: %v", err)
	}
	if n != 1 {
		t.Errorf("expected 1 auto-decided, got %d", n)
	}

	// The old one should be decided.
	dec, ok, err := dq.GetDecision(ctx, "wi-old")
	if err != nil {
		t.Fatalf("get decision: %v", err)
	}
	if !ok || dec != "auto-skip" {
		t.Errorf("expected 'auto-skip', got %q (ok=%v)", dec, ok)
	}

	// The new one should still be pending.
	_, ok, err = dq.GetDecision(ctx, "wi-new")
	if err != nil {
		t.Fatalf("get decision: %v", err)
	}
	if ok {
		t.Error("expected wi-new to still be pending")
	}
	_ = id2 // used for insertion
}

func TestDecisionMultipleIndependent(t *testing.T) {
	dq := setupDecisionQueue(t)
	ctx := context.Background()

	// Submit several decisions for different work items.
	ids := make([]string, 5)
	for i := 0; i < 5; i++ {
		id, err := dq.Submit(ctx, fmt.Sprintf("wi-%d", i), fmt.Sprintf("Question %d?", i), "", nil)
		if err != nil {
			t.Fatalf("submit %d: %v", i, err)
		}
		ids[i] = id
	}

	// Decide items 0, 2, 4.
	for _, idx := range []int{0, 2, 4} {
		if err := dq.Decide(ctx, ids[idx], fmt.Sprintf("answer-%d", idx)); err != nil {
			t.Fatalf("decide %d: %v", idx, err)
		}
	}

	// Pending should be items 1 and 3.
	pending, err := dq.PendingDecisions(ctx)
	if err != nil {
		t.Fatalf("pending: %v", err)
	}
	if len(pending) != 2 {
		t.Fatalf("expected 2 pending, got %d", len(pending))
	}

	// Verify the decided ones are correct.
	for _, idx := range []int{0, 2, 4} {
		dec, ok, err := dq.GetDecision(ctx, fmt.Sprintf("wi-%d", idx))
		if err != nil {
			t.Fatalf("get decision wi-%d: %v", idx, err)
		}
		if !ok {
			t.Errorf("expected wi-%d to be decided", idx)
		}
		expected := fmt.Sprintf("answer-%d", idx)
		if dec != expected {
			t.Errorf("wi-%d: expected %q, got %q", idx, expected, dec)
		}
	}
}
