package eval

import (
	"context"
	"fmt"
	"testing"

	"github.com/MochaCosine1206/orchestra/internal/db"
)

func openTestDB(t *testing.T) *db.DB {
	t.Helper()
	d, err := db.Open(":memory:")
	if err != nil {
		t.Fatalf("opening in-memory db: %v", err)
	}
	if err := d.InitSchema(context.Background()); err != nil {
		d.Close()
		t.Fatalf("initializing schema: %v", err)
	}
	t.Cleanup(func() { d.Close() })
	return d
}

func TestCurateFromDBNilDB(t *testing.T) {
	_, err := CurateFromDB(context.Background(), nil)
	if err == nil {
		t.Error("expected error for nil db")
	}
}

func TestCurateFromDB(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()

	// Insert some tasks — need a conductor first
	_, err := d.ExecContext(ctx, `INSERT INTO conductors (id, pid, goal, status, staging_branch, base_branch) VALUES ('c1', 1, 'test goal', 'active', 'staging', 'main')`)
	if err != nil {
		t.Fatalf("inserting conductor: %v", err)
	}

	// Insert a failed task
	_, err = d.ExecContext(ctx, `INSERT INTO tasks (id, title, description, status, priority, role, conductor_id)
		VALUES ('t1', 'Failing task', 'Task that failed due to build error', 'failed', 1, 'implementer', 'c1')`)
	if err != nil {
		t.Fatalf("inserting failed task: %v", err)
	}

	// Insert a successful task (should be excluded)
	_, err = d.ExecContext(ctx, `INSERT INTO tasks (id, title, status, priority, role, conductor_id)
		VALUES ('t2', 'Good task', 'done', 1, 'implementer', 'c1')`)
	if err != nil {
		t.Fatalf("inserting done task: %v", err)
	}

	scenarios, err := CurateFromDB(ctx, d)
	if err != nil {
		t.Fatalf("CurateFromDB: %v", err)
	}

	if len(scenarios) != 1 {
		t.Fatalf("got %d scenarios, want 1", len(scenarios))
	}

	s := scenarios[0]
	if s.ID != "curated-t1" {
		t.Errorf("ID = %q, want %q", s.ID, "curated-t1")
	}
	if s.Role != "implementer" {
		t.Errorf("Role = %q, want %q", s.Role, "implementer")
	}
	if s.Category != "real_failure" {
		t.Errorf("Category = %q, want %q", s.Category, "real_failure")
	}
}

func TestSeedDefaultScenariosNilDB(t *testing.T) {
	err := SeedDefaultScenarios(context.Background(), nil)
	if err == nil {
		t.Error("expected error for nil db")
	}
}

func TestSeedDefaultScenarios(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()

	// First seed
	if err := SeedDefaultScenarios(ctx, d); err != nil {
		t.Fatalf("first seed: %v", err)
	}

	scenarios, err := d.ListEvalScenarios(ctx)
	if err != nil {
		t.Fatalf("listing scenarios: %v", err)
	}
	if len(scenarios) != 8 {
		t.Errorf("got %d scenarios, want 8", len(scenarios))
	}

	// Second seed should be idempotent
	if err := SeedDefaultScenarios(ctx, d); err != nil {
		t.Fatalf("second seed (idempotent): %v", err)
	}

	scenarios2, err := d.ListEvalScenarios(ctx)
	if err != nil {
		t.Fatalf("listing scenarios after second seed: %v", err)
	}
	if len(scenarios2) != len(scenarios) {
		t.Errorf("got %d scenarios after re-seed, want %d (idempotent)", len(scenarios2), len(scenarios))
	}
}

func TestIsDuplicateError(t *testing.T) {
	tests := []struct {
		msg  string
		want bool
	}{
		{"UNIQUE constraint failed: eval_scenarios.id", true},
		{"duplicate key value violates unique constraint", true},
		{"some other error", false},
		{"", false},
	}

	for _, tt := range tests {
		got := isDuplicateError(fmt.Errorf("%s", tt.msg))
		if got != tt.want {
			t.Errorf("isDuplicateError(%q) = %v, want %v", tt.msg, got, tt.want)
		}
	}
}
