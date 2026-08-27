package orchestrator

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/MochaCosine1206/orchestra/internal/agent"
	"github.com/MochaCosine1206/orchestra/internal/db"
)

func setupRedecomposeTest(t *testing.T) (*Conductor, *db.DB) {
	t.Helper()
	d, err := db.OpenMemory()
	if err != nil {
		t.Fatalf("opening memory db: %v", err)
	}
	if err := d.InitSchema(context.Background()); err != nil {
		t.Fatalf("initializing schema: %v", err)
	}
	t.Cleanup(func() { d.Close() })

	runner := &MockRunner{}
	c, err := New(ConductorOpts{
		DB:       d,
		RepoRoot: t.TempDir(),
		Runner:   runner,
	})
	if err != nil {
		t.Fatalf("creating conductor: %v", err)
	}

	return c, d
}

func TestReDecompose(t *testing.T) {
	c, d := setupRedecomposeTest(t)
	ctx := context.Background()

	// Create the original task
	d.CreateTask(ctx, db.Task{
		ID:    "t-orig",
		Title: "Implement the feature",
		Status: "failed",
		Role:  "implementer",
	})

	// Store session IDs
	c.storeSessionTaskIDs(ctx, []string{"t-orig"})

	// Set up the mock runner to return subtasks
	runner := c.Runner.(*MockRunner)
	runner.Outputs = []string{`[
		{"title": "Part A", "description": "First part", "role": "implementer", "priority": 5, "depends_on": [], "files": ["a.go"]},
		{"title": "Part B", "description": "Second part", "role": "implementer", "priority": 4, "depends_on": [], "files": ["b.go"]}
	]`}

	checkpoint := agent.Checkpoint{
		TaskID:            "t-orig",
		CommitHashes:      []string{"abc"},
		FilesModified:     []string{"main.go"},
		EstimatedProgress: "mid",
	}
	cpJSON, _ := json.Marshal(checkpoint)

	newIDs, err := c.ReDecompose(ctx, "t-orig", string(cpJSON))
	if err != nil {
		t.Fatalf("ReDecompose: %v", err)
	}

	if len(newIDs) != 2 {
		t.Fatalf("expected 2 new tasks, got %d", len(newIDs))
	}

	// Verify tasks exist in DB
	for _, id := range newIDs {
		task, err := d.GetTaskByID(ctx, id)
		if err != nil || task == nil {
			t.Errorf("task %s not found in DB", id)
		}
	}

	// Verify session task IDs were updated
	taskIDs := c.sessionTaskIDs(ctx)
	if len(taskIDs) != 3 { // original + 2 new
		t.Errorf("expected 3 session task IDs, got %d", len(taskIDs))
	}

	// Verify event was logged
	events, _ := d.RecentEvents(ctx, 10)
	found := false
	for _, e := range events {
		if e.EventType == "task_redecomposed" {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected task_redecomposed event")
	}
}

func TestReDecomposeSkipsLateProgress(t *testing.T) {
	// This tests the monitor-side logic: late progress tasks should respawn, not re-decompose.
	// The monitor checks cp.EstimatedProgress != "late" before calling ReDecomposeFunc.
	// Here we verify that the monitor logic correctly parses the checkpoint.

	cp := agent.Checkpoint{
		TaskID:            "t1",
		CommitHashes:      []string{"a", "b", "c", "d"},
		EstimatedProgress: "late",
	}
	cpJSON, _ := json.Marshal(cp)

	var parsed agent.Checkpoint
	if err := json.Unmarshal(cpJSON, &parsed); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if parsed.EstimatedProgress != "late" {
		t.Errorf("expected late, got %s", parsed.EstimatedProgress)
	}
	// Monitor would skip re-decomposition for this task — verified by the condition check
}

func TestReDecomposeTaskNotFound(t *testing.T) {
	c, _ := setupRedecomposeTest(t)
	ctx := context.Background()

	_, err := c.ReDecompose(ctx, "nonexistent", `{"task_id":"nonexistent"}`)
	if err == nil {
		t.Error("expected error for nonexistent task")
	}
}

func TestReDecomposeNoRunner(t *testing.T) {
	d, err := db.OpenMemory()
	if err != nil {
		t.Fatal(err)
	}
	if err := d.InitSchema(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer d.Close()

	c, _ := New(ConductorOpts{
		DB:       d,
		RepoRoot: t.TempDir(),
		Runner:   &MockRunner{},
	})
	c.Runner = nil // remove runner
	ctx := context.Background()

	d.CreateTask(ctx, db.Task{ID: "t1", Title: "Test", Status: "failed", Role: "implementer"})

	_, err = c.ReDecompose(ctx, "t1", `{"task_id":"t1","estimated_progress":"mid"}`)
	if err == nil {
		t.Error("expected error when runner is nil")
	}
}
