package healing

import (
	"context"
	"testing"
	"time"
)

func TestImpactCheckOverlap(t *testing.T) {
	d := setupTestDB(t)
	ctx := context.Background()

	// Set up agents and tasks.
	_, _ = d.ExecContext(ctx, `INSERT INTO agents (id, role, status) VALUES ('a1', 'impl', 'active'), ('a2', 'impl', 'active')`)
	_, _ = d.ExecContext(ctx, `INSERT INTO tasks (id, title, status, role) VALUES ('t1', 'T1', 'failed', 'impl'), ('t2', 'T2', 'running', 'impl')`)

	// file_locks has file_path as PK, so each file can only be locked by one task.
	// Use CheckWithFiles to simulate the failed task's files after its locks were released.
	exp := time.Now().Add(time.Hour)
	d.CreateFileLock(ctx, "main.go", "a2", "t2", exp)
	d.CreateFileLock(ctx, "other.go", "a2", "t2", exp)

	imp := NewImpact(d)
	// t1 had main.go and util.go; t2 currently locks main.go — overlap on main.go.
	affected, err := imp.CheckWithFiles(ctx, "t1", []string{"main.go", "util.go"}, []string{"t2"})
	if err != nil {
		t.Fatal(err)
	}
	if len(affected) != 1 || affected[0] != "t2" {
		t.Errorf("expected [t2], got %v", affected)
	}
}

func TestImpactCheckNoOverlap(t *testing.T) {
	d := setupTestDB(t)
	ctx := context.Background()

	_, _ = d.ExecContext(ctx, `INSERT INTO agents (id, role, status) VALUES ('a1', 'impl', 'active'), ('a2', 'impl', 'active')`)
	_, _ = d.ExecContext(ctx, `INSERT INTO tasks (id, title, status, role) VALUES ('t1', 'T1', 'failed', 'impl'), ('t2', 'T2', 'running', 'impl')`)

	exp := time.Now().Add(time.Hour)
	d.CreateFileLock(ctx, "main.go", "a1", "t1", exp)
	d.CreateFileLock(ctx, "other.go", "a2", "t2", exp)

	imp := NewImpact(d)
	affected, err := imp.Check(ctx, "t1", []string{"t2"})
	if err != nil {
		t.Fatal(err)
	}
	if len(affected) != 0 {
		t.Errorf("expected no overlap, got %v", affected)
	}
}

func TestImpactCheckMultipleOverlaps(t *testing.T) {
	d := setupTestDB(t)
	ctx := context.Background()

	_, _ = d.ExecContext(ctx, `INSERT INTO agents (id, role, status) VALUES ('a1', 'impl', 'active'), ('a2', 'impl', 'active'), ('a3', 'impl', 'active')`)
	_, _ = d.ExecContext(ctx, `INSERT INTO tasks (id, title, status, role) VALUES ('t1', 'T1', 'failed', 'impl'), ('t2', 'T2', 'running', 'impl'), ('t3', 'T3', 'running', 'impl')`)

	// Each task locks different files, but t2 and t3 share files with t1's set.
	exp := time.Now().Add(time.Hour)
	d.CreateFileLock(ctx, "shared.go", "a2", "t2", exp)
	d.CreateFileLock(ctx, "common.go", "a3", "t3", exp)

	imp := NewImpact(d)
	// t1 had shared.go and common.go — both t2 and t3 overlap.
	affected, err := imp.CheckWithFiles(ctx, "t1", []string{"shared.go", "common.go"}, []string{"t2", "t3"})
	if err != nil {
		t.Fatal(err)
	}
	if len(affected) != 2 {
		t.Errorf("expected 2 affected tasks, got %d: %v", len(affected), affected)
	}
}

func TestImpactCheckSelfExclusion(t *testing.T) {
	d := setupTestDB(t)
	ctx := context.Background()

	_, _ = d.ExecContext(ctx, `INSERT INTO agents (id, role, status) VALUES ('a1', 'impl', 'active')`)
	_, _ = d.ExecContext(ctx, `INSERT INTO tasks (id, title, status, role) VALUES ('t1', 'T1', 'failed', 'impl')`)

	exp := time.Now().Add(time.Hour)
	d.CreateFileLock(ctx, "main.go", "a1", "t1", exp)

	imp := NewImpact(d)
	affected, err := imp.Check(ctx, "t1", []string{"t1"})
	if err != nil {
		t.Fatal(err)
	}
	if len(affected) != 0 {
		t.Errorf("expected self-exclusion, got %v", affected)
	}
}

func TestImpactCheckNoLocks(t *testing.T) {
	d := setupTestDB(t)
	ctx := context.Background()

	imp := NewImpact(d)
	affected, err := imp.Check(ctx, "t1", []string{"t2"})
	if err != nil {
		t.Fatal(err)
	}
	if len(affected) != 0 {
		t.Errorf("expected empty, got %v", affected)
	}
}
