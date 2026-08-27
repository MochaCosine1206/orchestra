package orchestrator

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	"github.com/MochaCosine1206/orchestra/internal/db"
)

func TestReconcile_NoSessionTasks(t *testing.T) {
	d := setupTestDB(t)
	c, _ := New(ConductorOpts{DB: d, RepoRoot: t.TempDir(), Runner: &MockRunner{}})
	ctx := context.Background()

	result, err := c.Reconcile(ctx, ReconcileOpts{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Report != "No session tasks found to reconcile." {
		t.Errorf("expected no-session report, got: %s", result.Report)
	}
}

func TestReconcile_NoOrphans_AllDone(t *testing.T) {
	d := setupTestDB(t)
	repoRoot := t.TempDir()
	c, _ := New(ConductorOpts{DB: d, RepoRoot: repoRoot, Runner: &MockRunner{}})
	ctx := context.Background()

	// Create tasks that are all done
	d.CreateTask(ctx, db.Task{ID: "t1", Title: "Task 1", Status: "done", Role: "implementer"})
	d.CreateTask(ctx, db.Task{ID: "t2", Title: "Task 2", Status: "done", Role: "implementer"})

	// Set up session
	c.storeSessionTaskIDs(ctx, []string{"t1", "t2"})
	c.setBlackboard(ctx, "conductor:goal", "Build feature X")
	c.setBlackboard(ctx, "result_summary:t1", "Implemented component A")
	c.setBlackboard(ctx, "result_summary:t2", "Implemented component B")

	result, err := c.Reconcile(ctx, ReconcileOpts{SkipLLM: true})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// No orphans
	if len(result.OrphanWorktrees) != 0 {
		t.Errorf("expected 0 orphans, got %d", len(result.OrphanWorktrees))
	}
	// No failed tasks
	if len(result.FailedTasks) != 0 {
		t.Errorf("expected 0 failed, got %d", len(result.FailedTasks))
	}
	// All summaries collected
	if len(result.TaskSummaries) != 2 {
		t.Errorf("expected 2 summaries, got %d", len(result.TaskSummaries))
	}
	// Full alignment
	if result.GoalAlignmentScore != 1.0 {
		t.Errorf("expected score 1.0, got %f", result.GoalAlignmentScore)
	}
	// Report generated
	if result.Report == "" {
		t.Error("expected non-empty report")
	}
}

func TestReconcile_WithOrphans(t *testing.T) {
	d := setupTestDB(t)
	repoRoot := t.TempDir()

	// Create .worktree directory with orphan entries
	worktreeDir := filepath.Join(repoRoot, ".worktree")
	os.MkdirAll(filepath.Join(worktreeDir, "t-orphan1"), 0755)
	os.MkdirAll(filepath.Join(worktreeDir, "t-orphan2"), 0755)

	c, _ := New(ConductorOpts{DB: d, RepoRoot: repoRoot, Runner: &MockRunner{}})
	ctx := context.Background()

	// Create a done task (not orphan — task exists in DB but is terminal)
	d.CreateTask(ctx, db.Task{ID: "t1", Title: "Task 1", Status: "done", Role: "implementer"})
	c.storeSessionTaskIDs(ctx, []string{"t1"})
	c.setBlackboard(ctx, "conductor:goal", "Build feature X")

	result, err := c.Reconcile(ctx, ReconcileOpts{DryRun: true, SkipLLM: true})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Should find orphan worktrees
	if len(result.OrphanWorktrees) != 2 {
		t.Errorf("expected 2 orphans, got %d: %v", len(result.OrphanWorktrees), result.OrphanWorktrees)
	}

	// Dry run — no salvage or cleanup
	if len(result.SalvagedWorktrees) != 0 {
		t.Errorf("expected 0 salvaged in dry-run, got %d", len(result.SalvagedWorktrees))
	}
}

func TestReconcile_PartialCompletion_NoLLM(t *testing.T) {
	d := setupTestDB(t)
	repoRoot := t.TempDir()
	c, _ := New(ConductorOpts{DB: d, RepoRoot: repoRoot, Runner: &MockRunner{}})
	ctx := context.Background()

	// Mix of done and failed
	d.CreateTask(ctx, db.Task{ID: "t1", Title: "Task 1", Status: "done", Role: "implementer"})
	d.CreateTask(ctx, db.Task{ID: "t2", Title: "Task 2", Status: "failed", Role: "implementer",
		Result: sql.NullString{String: "context_exhausted", Valid: true}})

	c.storeSessionTaskIDs(ctx, []string{"t1", "t2"})
	c.setBlackboard(ctx, "conductor:goal", "Build feature X")
	c.setBlackboard(ctx, "result_summary:t1", "Completed A")
	c.setBlackboard(ctx, "failure_type:t2", "context_exhausted")

	result, err := c.Reconcile(ctx, ReconcileOpts{SkipLLM: true})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Heuristic score: 1/2 = 0.5
	if result.GoalAlignmentScore != 0.5 {
		t.Errorf("expected score 0.5, got %f", result.GoalAlignmentScore)
	}

	// Should have 1 failed task
	if len(result.FailedTasks) != 1 {
		t.Errorf("expected 1 failed task, got %d", len(result.FailedTasks))
	}
	if result.FailedTasks[0].FailureType != "context_exhausted" {
		t.Errorf("expected failure_type 'context_exhausted', got %q", result.FailedTasks[0].FailureType)
	}
}

func TestReconcile_LLMAnalysis(t *testing.T) {
	d := setupTestDB(t)
	repoRoot := t.TempDir()

	// Mock LLM returns JSON analysis
	runner := &MockRunner{
		Outputs: []string{`{"score": 0.7, "gaps": ["Missing error handling", "No tests"], "note": "Core logic done but quality gaps remain"}`},
	}
	c, _ := New(ConductorOpts{DB: d, RepoRoot: repoRoot, Runner: runner})
	ctx := context.Background()

	d.CreateTask(ctx, db.Task{ID: "t1", Title: "Implement core", Status: "done", Role: "implementer"})
	d.CreateTask(ctx, db.Task{ID: "t2", Title: "Add tests", Status: "failed", Role: "implementer"})

	c.storeSessionTaskIDs(ctx, []string{"t1", "t2"})
	c.setBlackboard(ctx, "conductor:goal", "Build and test feature X")
	c.setBlackboard(ctx, "result_summary:t1", "Core implemented")
	c.setBlackboard(ctx, "failure_type:t2", "normal_failure")

	result, err := c.Reconcile(ctx, ReconcileOpts{SkipLLM: false})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// LLM score should be used
	if result.GoalAlignmentScore != 0.7 {
		t.Errorf("expected LLM score 0.7, got %f", result.GoalAlignmentScore)
	}

	// Gaps should be identified
	if len(result.GapsIdentified) != 2 {
		t.Errorf("expected 2 gaps, got %d: %v", len(result.GapsIdentified), result.GapsIdentified)
	}

	// LLM should have been called
	if len(runner.Calls) != 1 {
		t.Errorf("expected 1 LLM call, got %d", len(runner.Calls))
	}
}

func TestReconcile_FollowUpTaskCreation(t *testing.T) {
	d := setupTestDB(t)
	repoRoot := t.TempDir()

	runner := &MockRunner{
		Outputs: []string{`{"score": 0.6, "gaps": ["Add unit tests", "Fix error handling"], "note": "Partial completion"}`},
	}
	c, _ := New(ConductorOpts{DB: d, RepoRoot: repoRoot, Runner: runner})
	ctx := context.Background()

	d.CreateTask(ctx, db.Task{ID: "t1", Title: "Implement", Status: "done", Role: "implementer"})
	d.CreateTask(ctx, db.Task{ID: "t2", Title: "Test", Status: "failed", Role: "implementer"})

	c.storeSessionTaskIDs(ctx, []string{"t1", "t2"})
	c.setBlackboard(ctx, "conductor:goal", "Build and test")
	c.setBlackboard(ctx, "result_summary:t1", "Done")

	result, err := c.Reconcile(ctx, ReconcileOpts{SkipLLM: false})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Follow-up tasks should be created
	if len(result.FollowUpTaskIDs) != 2 {
		t.Errorf("expected 2 follow-up tasks, got %d", len(result.FollowUpTaskIDs))
	}

	// Verify tasks exist in DB
	for _, id := range result.FollowUpTaskIDs {
		task, err := d.GetTaskByID(ctx, id)
		if err != nil {
			t.Errorf("error getting follow-up task %s: %v", id, err)
		}
		if task == nil {
			t.Errorf("follow-up task %s not found in DB", id)
		} else if task.Status != "pending" {
			t.Errorf("follow-up task %s status = %q, want 'pending'", id, task.Status)
		}
	}

	// Follow-up goal should be set
	if result.FollowUpGoal == "" {
		t.Error("expected non-empty follow-up goal")
	}
}

func TestReconcile_DryRun_NoSideEffects(t *testing.T) {
	d := setupTestDB(t)
	repoRoot := t.TempDir()

	runner := &MockRunner{
		Outputs: []string{`{"score": 0.5, "gaps": ["Gap 1"], "note": "Partial"}`},
	}
	c, _ := New(ConductorOpts{DB: d, RepoRoot: repoRoot, Runner: runner})
	ctx := context.Background()

	d.CreateTask(ctx, db.Task{ID: "t1", Title: "Task 1", Status: "done", Role: "implementer"})
	d.CreateTask(ctx, db.Task{ID: "t2", Title: "Task 2", Status: "failed", Role: "implementer"})

	c.storeSessionTaskIDs(ctx, []string{"t1", "t2"})
	c.setBlackboard(ctx, "conductor:goal", "Goal")
	c.setBlackboard(ctx, "result_summary:t1", "Done")

	result, err := c.Reconcile(ctx, ReconcileOpts{DryRun: true, SkipLLM: false})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// No follow-up tasks should be created in dry-run
	if len(result.FollowUpTaskIDs) != 0 {
		t.Errorf("expected 0 follow-up tasks in dry-run, got %d", len(result.FollowUpTaskIDs))
	}

	// No salvaged worktrees
	if len(result.SalvagedWorktrees) != 0 {
		t.Errorf("expected 0 salvaged worktrees in dry-run, got %d", len(result.SalvagedWorktrees))
	}
}

func TestReconcile_LLMFailure_FallsBack(t *testing.T) {
	d := setupTestDB(t)
	repoRoot := t.TempDir()

	// LLM returns invalid JSON
	runner := &MockRunner{
		Outputs: []string{"This is not JSON at all"},
	}
	c, _ := New(ConductorOpts{DB: d, RepoRoot: repoRoot, Runner: runner})
	ctx := context.Background()

	d.CreateTask(ctx, db.Task{ID: "t1", Title: "Task 1", Status: "done", Role: "implementer"})
	d.CreateTask(ctx, db.Task{ID: "t2", Title: "Task 2", Status: "failed", Role: "implementer"})

	c.storeSessionTaskIDs(ctx, []string{"t1", "t2"})
	c.setBlackboard(ctx, "conductor:goal", "Goal")

	result, err := c.Reconcile(ctx, ReconcileOpts{SkipLLM: false})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Should fall back to heuristic score (1/2 = 0.5)
	if result.GoalAlignmentScore != 0.5 {
		t.Errorf("expected fallback score 0.5, got %f", result.GoalAlignmentScore)
	}
}

func TestReconcile_ReportContainsGoal(t *testing.T) {
	d := setupTestDB(t)
	repoRoot := t.TempDir()
	c, _ := New(ConductorOpts{DB: d, RepoRoot: repoRoot, Runner: &MockRunner{}})
	ctx := context.Background()

	d.CreateTask(ctx, db.Task{ID: "t1", Title: "Task 1", Status: "done", Role: "implementer"})
	c.storeSessionTaskIDs(ctx, []string{"t1"})
	c.setBlackboard(ctx, "conductor:goal", "Build the reconciliation system")

	result, err := c.Reconcile(ctx, ReconcileOpts{SkipLLM: true})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.Report == "" {
		t.Fatal("expected non-empty report")
	}
	if !contains(result.Report, "Build the reconciliation system") {
		t.Errorf("report should contain the goal, got: %s", result.Report)
	}
	if !contains(result.Report, "Reconciliation Report") {
		t.Errorf("report should contain header, got: %s", result.Report)
	}
}

func TestExtractJSON(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{`{"score": 0.5}`, `{"score": 0.5}`},
		{`Here is the analysis: {"score": 0.5, "gaps": []} done.`, `{"score": 0.5, "gaps": []}`},
		{`no json here`, `no json here`},
		{`{"nested": {"a": 1}}`, `{"nested": {"a": 1}}`},
	}
	for _, tt := range tests {
		got := extractJSON(tt.input)
		if got != tt.want {
			t.Errorf("extractJSON(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestTruncate(t *testing.T) {
	if got := truncate("short", 10); got != "short" {
		t.Errorf("truncate('short', 10) = %q", got)
	}
	if got := truncate("this is a long string", 10); got != "this is..." {
		t.Errorf("truncate('this is a long string', 10) = %q", got)
	}
}

// contains and containsSubstring are declared in decompose_test.go
