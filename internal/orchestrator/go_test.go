package orchestrator

import (
	"context"
	"testing"
)

func TestGo_EmptyGoal(t *testing.T) {
	d := setupTestDB(t)
	c, _ := New(ConductorOpts{DB: d, RepoRoot: "/tmp/test"})
	ctx := context.Background()

	_, err := c.Go(ctx, GoOpts{Goal: ""})
	if err == nil {
		t.Fatal("expected error for empty goal")
	}
}

func TestGo_DryRun(t *testing.T) {
	d := setupTestDB(t)
	runner := &MockRunner{
		Outputs: []string{`[{"title":"Fix bug","description":"details","role":"implementer","priority":5}]`},
	}
	c, _ := New(ConductorOpts{DB: d, RepoRoot: "/tmp/test", Runner: runner})
	ctx := context.Background()

	result, err := c.Go(ctx, GoOpts{
		Goal:   "Fix the bug",
		DryRun: true,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.TaskCount != 1 {
		t.Errorf("TaskCount = %d, want 1", result.TaskCount)
	}
	if result.SessionID == "" {
		t.Error("SessionID should be set")
	}
}

func TestGo_DryRun_MultipleTasks(t *testing.T) {
	d := setupTestDB(t)
	output := `[
		{"title":"Research","description":"r","role":"researcher","priority":4},
		{"title":"Implement","description":"i","role":"implementer","priority":5}
	]`
	runner := &MockRunner{Outputs: []string{output}}
	c, _ := New(ConductorOpts{DB: d, RepoRoot: "/tmp/test", Runner: runner})
	ctx := context.Background()

	result, err := c.Go(ctx, GoOpts{
		Goal:   "Big feature",
		DryRun: true,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.TaskCount != 2 {
		t.Errorf("TaskCount = %d, want 2", result.TaskCount)
	}
}

func TestGo_NilRunner(t *testing.T) {
	d := setupTestDB(t)
	c, _ := New(ConductorOpts{DB: d, RepoRoot: "/tmp/test"})
	ctx := context.Background()

	_, err := c.Go(ctx, GoOpts{Goal: "test"})
	if err == nil {
		t.Fatal("expected error for nil runner (needed for decompose)")
	}
}

func TestGo_MaxTasksDefault(t *testing.T) {
	d := setupTestDB(t)
	output := `[
		{"title":"T1","description":"d","role":"implementer","priority":5},
		{"title":"T2","description":"d","role":"implementer","priority":4},
		{"title":"T3","description":"d","role":"implementer","priority":3},
		{"title":"T4","description":"d","role":"implementer","priority":2}
	]`
	runner := &MockRunner{Outputs: []string{output}}
	c, _ := New(ConductorOpts{DB: d, RepoRoot: "/tmp/test", Runner: runner})
	ctx := context.Background()

	result, err := c.Go(ctx, GoOpts{
		Goal:   "test",
		DryRun: true,
		// MaxTasks defaults to 8 (B-236)
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// With default MaxTasks=8, all 4 tasks should be created
	if result.TaskCount != 4 {
		t.Errorf("TaskCount = %d, want 4 (all tasks within default max of 8)", result.TaskCount)
	}
}

func TestGo_TestCmdPassthrough(t *testing.T) {
	d := setupTestDB(t)
	runner := &MockRunner{
		Outputs: []string{`[{"title":"Test","description":"d","role":"implementer","priority":5}]`},
	}
	c, _ := New(ConductorOpts{DB: d, RepoRoot: "/tmp/test", Runner: runner})
	ctx := context.Background()

	// In dry-run, test-cmd is stored but not executed
	result, err := c.Go(ctx, GoOpts{
		Goal:    "test",
		DryRun:  true,
		TestCmd: "go test ./...",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.TaskCount != 1 {
		t.Errorf("TaskCount = %d, want 1", result.TaskCount)
	}
}

func TestGo_ReviewFlag(t *testing.T) {
	d := setupTestDB(t)
	runner := &MockRunner{
		Outputs: []string{`[{"title":"Test","description":"d","role":"implementer","priority":5}]`},
	}
	c, _ := New(ConductorOpts{DB: d, RepoRoot: "/tmp/test", Runner: runner})
	ctx := context.Background()

	result, err := c.Go(ctx, GoOpts{
		Goal:   "test",
		DryRun: true,
		Review: true,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil {
		t.Fatal("result should not be nil")
	}
}

func TestGoResult_Structure(t *testing.T) {
	r := GoResult{
		SessionID:   "s-001",
		TaskCount:   3,
		DoneCount:   2,
		FailedCount: 1,
	}
	if r.SessionID != "s-001" || r.TaskCount != 3 || r.DoneCount != 2 || r.FailedCount != 1 {
		t.Errorf("unexpected structure: %+v", r)
	}
}
