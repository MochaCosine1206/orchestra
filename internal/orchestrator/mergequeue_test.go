package orchestrator

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MochaCosine1206/orchestra/internal/db"
)

func TestComputeQueuePositions(t *testing.T) {
	tasks := []db.Task{
		{ID: "T-001", Status: "done", DependsOn: sql.NullString{}},
		{ID: "T-002", Status: "done", DependsOn: sql.NullString{String: `["T-001"]`, Valid: true}},
		{ID: "T-003", Status: "done", DependsOn: sql.NullString{String: `["T-001"]`, Valid: true}},
	}

	positions, err := computeQueuePositions(tasks)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(positions) != 3 {
		t.Fatalf("expected 3 positions, got %d", len(positions))
	}
	// T-001 must come before T-002 and T-003
	if positions["T-001"] >= positions["T-002"] {
		t.Errorf("T-001 (pos=%d) should be before T-002 (pos=%d)", positions["T-001"], positions["T-002"])
	}
	if positions["T-001"] >= positions["T-003"] {
		t.Errorf("T-001 (pos=%d) should be before T-003 (pos=%d)", positions["T-001"], positions["T-003"])
	}
}

func TestComputeQueuePositions_Empty(t *testing.T) {
	positions, err := computeQueuePositions(nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(positions) != 0 {
		t.Errorf("expected empty positions, got %d", len(positions))
	}
}

func TestEnqueueDoneTask(t *testing.T) {
	d := setupTestDB(t)
	ctx := context.Background()

	// Create conductor
	d.CreateConductor(ctx, db.ConductorRecord{
		ID: "c-001", PID: 1, Goal: "test", Status: "active",
		StagingBranch: "staging/c-001", BaseBranch: "dev",
		MaxParallel: 1, ModelStrategy: "all-opus", Runtime: "local",
	})

	// Create a done task with branch
	d.CreateTask(ctx, db.Task{
		ID: "T-001", Title: "Test", Status: "done", Role: "implementer",
		Branch:      sql.NullString{String: "feature/T-001", Valid: true},
		ConductorID: sql.NullString{String: "c-001", Valid: true},
	})

	c, _ := New(ConductorOpts{DB: d, RepoRoot: "/tmp/test"})
	c.ConductorID = "c-001"

	// Enqueue
	err := c.enqueueCompletedTasks(ctx, "c-001")
	if err != nil {
		t.Fatalf("enqueue error: %v", err)
	}

	// Verify entry created
	entries, err := d.ListMergeQueueEntries(ctx, "c-001")
	if err != nil {
		t.Fatalf("list error: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}
	if entries[0].TaskID != "T-001" {
		t.Errorf("expected task T-001, got %s", entries[0].TaskID)
	}
	if entries[0].Branch != "feature/T-001" {
		t.Errorf("expected branch feature/T-001, got %s", entries[0].Branch)
	}
	if entries[0].Status != "pending" {
		t.Errorf("expected status pending, got %s", entries[0].Status)
	}

	// Enqueue again — should be idempotent
	err = c.enqueueCompletedTasks(ctx, "c-001")
	if err != nil {
		t.Fatalf("re-enqueue error: %v", err)
	}
	entries, _ = d.ListMergeQueueEntries(ctx, "c-001")
	if len(entries) != 1 {
		t.Errorf("expected idempotent enqueue (1 entry), got %d", len(entries))
	}
}

func TestProcessNextEntry_EmptyQueue(t *testing.T) {
	d := setupTestDB(t)
	ctx := context.Background()

	c, _ := New(ConductorOpts{DB: d, RepoRoot: "/tmp/test"})

	result, err := c.processNextEntry(ctx, "c-001", MergeOpts{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != nil {
		t.Errorf("expected nil result for empty queue, got %v", result)
	}
}

func TestProcessNextEntry_BlockedByDep(t *testing.T) {
	d := setupTestDB(t)
	ctx := context.Background()

	// Create conductor
	d.CreateConductor(ctx, db.ConductorRecord{
		ID: "c-001", PID: 1, Goal: "test", Status: "active",
		StagingBranch: "staging/c-001", BaseBranch: "dev",
		MaxParallel: 1, ModelStrategy: "all-opus", Runtime: "local",
	})

	// Create tasks with dependency
	d.CreateTask(ctx, db.Task{
		ID: "T-001", Title: "Base", Status: "done", Role: "implementer",
		Branch:      sql.NullString{String: "feature/T-001", Valid: true},
		ConductorID: sql.NullString{String: "c-001", Valid: true},
	})
	d.CreateTask(ctx, db.Task{
		ID: "T-002", Title: "Dependent", Status: "done", Role: "implementer",
		Branch:      sql.NullString{String: "feature/T-002", Valid: true},
		BlockedBy:   sql.NullString{String: `["T-001"]`, Valid: true},
		ConductorID: sql.NullString{String: "c-001", Valid: true},
	})

	c, _ := New(ConductorOpts{DB: d, RepoRoot: "/tmp/test"})
	c.ConductorID = "c-001"

	// Enqueue — T-002 should be blocked
	err := c.enqueueCompletedTasks(ctx, "c-001")
	if err != nil {
		t.Fatalf("enqueue error: %v", err)
	}

	entries, _ := d.ListMergeQueueEntries(ctx, "c-001")
	if len(entries) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(entries))
	}

	// Find T-002's entry
	var t002Entry *db.MergeQueueEntry
	for i, e := range entries {
		if e.TaskID == "T-002" {
			t002Entry = &entries[i]
			break
		}
	}
	if t002Entry == nil {
		t.Fatal("T-002 entry not found")
	}
	if t002Entry.Status != "blocked" {
		t.Errorf("expected T-002 to be blocked, got %s", t002Entry.Status)
	}

	// Process next — should process T-001 (the pending one), not T-002 (blocked)
	next, err := d.NextPendingMergeQueueEntry(ctx, "c-001")
	if err != nil {
		t.Fatalf("next error: %v", err)
	}
	if next == nil {
		t.Fatal("expected a pending entry")
	}
	if next.TaskID != "T-001" {
		t.Errorf("expected T-001 to be next, got %s", next.TaskID)
	}
}

func TestRequeueForRefinement_CircuitBreaker(t *testing.T) {
	d := setupTestDB(t)
	ctx := context.Background()

	// Create conductor
	d.CreateConductor(ctx, db.ConductorRecord{
		ID: "c-001", PID: 1, Goal: "test", Status: "active",
		StagingBranch: "staging/c-001", BaseBranch: "dev",
		MaxParallel: 1, ModelStrategy: "all-opus", Runtime: "local",
	})

	// Create entry with 2 refinements already (at the circuit breaker limit)
	entry := db.MergeQueueEntry{
		ID: "mq-001", ConductorID: "c-001", TaskID: "T-001",
		Branch: "feature/T-001", Position: 0, Status: "merging",
		RefinementCount: 2,
	}
	d.CreateMergeQueueEntry(ctx, entry)

	c, _ := New(ConductorOpts{DB: d, RepoRoot: "/tmp/test"})

	// Circuit breaker should prevent refinement
	if c.shouldRefineMergeEntry(ctx, &entry) {
		t.Error("expected circuit breaker to prevent refinement at count=2")
	}

	// Count=1 should allow refinement
	entry.RefinementCount = 1
	if !c.shouldRefineMergeEntry(ctx, &entry) {
		t.Error("expected refinement to be allowed at count=1")
	}
}

func TestFIFOQueue_AutoEnable(t *testing.T) {
	// Test that FIFO auto-enables at 3+ tasks
	tests := []struct {
		taskCount int
		strategy  string
		expected  string
	}{
		{1, "", ""},           // 1 task: no auto-enable
		{2, "", ""},           // 2 tasks: no auto-enable
		{3, "", "fifo"},       // 3 tasks: auto-enable FIFO
		{5, "", "fifo"},       // 5 tasks: auto-enable FIFO
		{3, "batch", "batch"}, // explicit batch: respect user choice
		{3, "fifo", "fifo"},   // explicit FIFO: respect user choice
	}

	for _, tt := range tests {
		strategy := tt.strategy
		if strategy == "" && tt.taskCount >= 3 {
			strategy = "fifo"
		}
		if tt.expected != "" && strategy != tt.expected {
			t.Errorf("taskCount=%d, strategy=%q: expected %q, got %q",
				tt.taskCount, tt.strategy, tt.expected, strategy)
		}
	}
}

func TestAreDependenciesMerged(t *testing.T) {
	d := setupTestDB(t)
	ctx := context.Background()

	c, _ := New(ConductorOpts{DB: d, RepoRoot: "/tmp/test"})

	// Empty deps should return true
	if !c.areDependenciesMerged(ctx, "c-001", "") {
		t.Error("empty deps should be merged")
	}
	if !c.areDependenciesMerged(ctx, "c-001", "[]") {
		t.Error("empty array deps should be merged")
	}

	// Non-existent dep should return false
	if c.areDependenciesMerged(ctx, "c-001", `["T-001"]`) {
		t.Error("non-existent dep should not be merged")
	}

	// Create a merged entry
	d.CreateMergeQueueEntry(ctx, db.MergeQueueEntry{
		ID: "mq-001", ConductorID: "c-001", TaskID: "T-001",
		Branch: "feature/T-001", Position: 0, Status: "merged",
	})

	if !c.areDependenciesMerged(ctx, "c-001", `["T-001"]`) {
		t.Error("merged dep should be reported as merged")
	}

	// Mixed: one merged, one not
	if c.areDependenciesMerged(ctx, "c-001", `["T-001","T-002"]`) {
		t.Error("partially merged deps should not be merged")
	}
}

func TestIsMergeQueueComplete(t *testing.T) {
	d := setupTestDB(t)
	ctx := context.Background()

	// Create conductor
	d.CreateConductor(ctx, db.ConductorRecord{
		ID: "c-001", PID: 1, Goal: "test", Status: "active",
		StagingBranch: "staging/c-001", BaseBranch: "dev",
		MaxParallel: 1, ModelStrategy: "all-opus", Runtime: "local",
	})

	c, _ := New(ConductorOpts{DB: d, RepoRoot: "/tmp/test"})
	c.ConductorID = "c-001"

	// No tasks = complete
	if !c.isMergeQueueComplete(ctx, "c-001") {
		t.Error("empty queue should be complete")
	}

	// Add a running task
	d.CreateTask(ctx, db.Task{
		ID: "T-001", Title: "Test", Status: "running", Role: "implementer",
		ConductorID: sql.NullString{String: "c-001", Valid: true},
	})
	if c.isMergeQueueComplete(ctx, "c-001") {
		t.Error("queue with running task should not be complete")
	}

	// Mark task done + add merged queue entry
	d.CompleteTask(ctx, "T-001", "ok")
	d.CreateMergeQueueEntry(ctx, db.MergeQueueEntry{
		ID: "mq-001", ConductorID: "c-001", TaskID: "T-001",
		Branch: "feature/T-001", Position: 0, Status: "merged",
	})
	if !c.isMergeQueueComplete(ctx, "c-001") {
		t.Error("queue with all tasks done and merged should be complete")
	}

	// Add a pending queue entry — should not be complete
	d.CreateMergeQueueEntry(ctx, db.MergeQueueEntry{
		ID: "mq-002", ConductorID: "c-001", TaskID: "T-002",
		Branch: "feature/T-002", Position: 1, Status: "pending",
	})
	if c.isMergeQueueComplete(ctx, "c-001") {
		t.Error("queue with pending entry should not be complete")
	}
}

// G134: Failed tasks with wait_until entries should not be considered terminal.
func TestIsMergeQueueComplete_WaiterNotTerminal(t *testing.T) {
	d := setupTestDB(t)
	ctx := context.Background()

	d.CreateConductor(ctx, db.ConductorRecord{
		ID: "c-001", PID: 1, Goal: "test", Status: "active",
		StagingBranch: "staging/c-001", BaseBranch: "dev",
		MaxParallel: 1, ModelStrategy: "all-opus", Runtime: "local",
	})

	c, _ := New(ConductorOpts{DB: d, RepoRoot: "/tmp/test"})
	c.ConductorID = "c-001"

	// Create two tasks: one done, one failed
	d.CreateTask(ctx, db.Task{
		ID: "T-001", Title: "Ch01", Status: "done", Role: "implementer",
		ConductorID: sql.NullString{String: "c-001", Valid: true},
	})
	d.CreateTask(ctx, db.Task{
		ID: "T-002", Title: "Ch02", Status: "failed", Role: "implementer",
		ConductorID: sql.NullString{String: "c-001", Valid: true},
	})

	// Without wait_until, failed task IS terminal — queue should be complete
	d.CreateMergeQueueEntry(ctx, db.MergeQueueEntry{
		ID: "mq-001", ConductorID: "c-001", TaskID: "T-001",
		Branch: "feature/T-001", Position: 0, Status: "merged",
	})
	if !c.isMergeQueueComplete(ctx, "c-001") {
		t.Error("queue with failed task (no waiter) should be complete")
	}

	// Set wait_until for the failed task (simulating rate_limit retry scheduled)
	d.SetBlackboard(ctx, "wait_until:T-002", "9999999999", "monitor")
	d.SetBlackboard(ctx, "wait_reason:T-002", "rate_limit", "monitor")

	if c.isMergeQueueComplete(ctx, "c-001") {
		t.Error("queue with failed task that has wait_until should NOT be complete")
	}

	// Remove wait_until (simulating monitor consumed it and retried)
	d.DeleteBlackboard(ctx, "wait_until:T-002")
	d.DeleteBlackboard(ctx, "wait_reason:T-002")

	if !c.isMergeQueueComplete(ctx, "c-001") {
		t.Error("queue should be complete after wait_until cleared")
	}
}

func TestHasActiveWaiters(t *testing.T) {
	d := setupTestDB(t)
	ctx := context.Background()

	c, _ := New(ConductorOpts{DB: d, RepoRoot: "/tmp/test"})

	taskIDs := []string{"T-001", "T-002", "T-003"}

	// No waiters
	if c.hasActiveWaiters(ctx, taskIDs) {
		t.Error("should have no active waiters")
	}

	// Waiter for a session task
	d.SetBlackboard(ctx, "wait_until:T-002", "9999999999", "monitor")
	if !c.hasActiveWaiters(ctx, taskIDs) {
		t.Error("should detect active waiter for T-002")
	}

	// Waiter for a non-session task should be ignored
	d.DeleteBlackboard(ctx, "wait_until:T-002")
	d.SetBlackboard(ctx, "wait_until:T-OTHER", "9999999999", "monitor")
	if c.hasActiveWaiters(ctx, taskIDs) {
		t.Error("waiter for non-session task should be ignored")
	}
}

func TestMergeQueueCRUD(t *testing.T) {
	d := setupTestDB(t)
	ctx := context.Background()

	entry := db.MergeQueueEntry{
		ID: "mq-001", ConductorID: "c-001", TaskID: "T-001",
		Branch: "feature/T-001", Position: 0, Status: "pending",
	}

	// Create
	if err := d.CreateMergeQueueEntry(ctx, entry); err != nil {
		t.Fatalf("create error: %v", err)
	}

	// List
	entries, err := d.ListMergeQueueEntries(ctx, "c-001")
	if err != nil {
		t.Fatalf("list error: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}
	if entries[0].ID != "mq-001" {
		t.Errorf("expected id mq-001, got %s", entries[0].ID)
	}

	// Update status
	if err := d.UpdateMergeQueueEntryStatus(ctx, "mq-001", "merging"); err != nil {
		t.Fatalf("update status error: %v", err)
	}
	entries, _ = d.ListMergeQueueEntries(ctx, "c-001")
	if entries[0].Status != "merging" {
		t.Errorf("expected status merging, got %s", entries[0].Status)
	}

	// Set merged
	if err := d.SetMergeQueueEntryMerged(ctx, "mq-001"); err != nil {
		t.Fatalf("set merged error: %v", err)
	}
	entries, _ = d.ListMergeQueueEntries(ctx, "c-001")
	if entries[0].Status != "merged" {
		t.Errorf("expected status merged, got %s", entries[0].Status)
	}
	if !entries[0].MergedAt.Valid {
		t.Error("expected merged_at to be set")
	}

	// Count by status
	counts, err := d.CountMergeQueueByStatus(ctx, "c-001")
	if err != nil {
		t.Fatalf("count error: %v", err)
	}
	if counts["merged"] != 1 {
		t.Errorf("expected 1 merged, got %d", counts["merged"])
	}

	// Add another and test failed
	d.CreateMergeQueueEntry(ctx, db.MergeQueueEntry{
		ID: "mq-002", ConductorID: "c-001", TaskID: "T-002",
		Branch: "feature/T-002", Position: 1, Status: "pending",
	})
	if err := d.SetMergeQueueEntryFailed(ctx, "mq-002", "conflict"); err != nil {
		t.Fatalf("set failed error: %v", err)
	}
	entry2, _ := d.GetMergeQueueEntryByTaskID(ctx, "c-001", "T-002")
	if entry2 == nil {
		t.Fatal("expected to find entry by task ID")
	}
	if entry2.Status != "failed" {
		t.Errorf("expected failed, got %s", entry2.Status)
	}
	if !entry2.ErrorMessage.Valid || entry2.ErrorMessage.String != "conflict" {
		t.Errorf("expected error message 'conflict', got %v", entry2.ErrorMessage)
	}

	// Increment refinement
	if err := d.IncrementMergeQueueRefinement(ctx, "mq-002"); err != nil {
		t.Fatalf("increment refinement error: %v", err)
	}
	entries, _ = d.ListMergeQueueEntries(ctx, "c-001")
	for _, e := range entries {
		if e.ID == "mq-002" && e.RefinementCount != 1 {
			t.Errorf("expected refinement count 1, got %d", e.RefinementCount)
		}
	}

	// Requeue
	if err := d.RequeueMergeQueueEntry(ctx, "mq-002", 5); err != nil {
		t.Fatalf("requeue error: %v", err)
	}
	entry2, _ = d.GetMergeQueueEntryByTaskID(ctx, "c-001", "T-002")
	if entry2.Status != "pending" {
		t.Errorf("expected pending after requeue, got %s", entry2.Status)
	}
	if entry2.Position != 5 {
		t.Errorf("expected position 5 after requeue, got %d", entry2.Position)
	}

	// Next pending
	next, err := d.NextPendingMergeQueueEntry(ctx, "c-001")
	if err != nil {
		t.Fatalf("next pending error: %v", err)
	}
	if next == nil {
		t.Fatal("expected a pending entry")
	}
	if next.ID != "mq-002" {
		t.Errorf("expected mq-002 (requeued), got %s", next.ID)
	}
}

// --- B-281: Hierarchical cluster merge tests ---

// mqGitSetup initializes a git repo with a dev branch and staging branch.
func mqGitSetup(t *testing.T, dir string) {
	t.Helper()
	mqGitRun(t, dir, "init")
	mqGitRun(t, dir, "checkout", "-b", "dev")
	os.WriteFile(filepath.Join(dir, "README.md"), []byte("test\n"), 0o644)
	mqGitRun(t, dir, "add", ".")
	mqGitRun(t, dir, "commit", "-m", "initial")
	mqGitRun(t, dir, "branch", "staging/c-cluster")
}

func mqGitRun(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=test",
		"GIT_AUTHOR_EMAIL=test@test.com",
		"GIT_COMMITTER_NAME=test",
		"GIT_COMMITTER_EMAIL=test@test.com",
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v: %s", args, err, string(out))
	}
}

func TestFeatureClusterBranch(t *testing.T) {
	name := FeatureClusterBranch("c-001", "auth")
	expected := "cluster/c-001/auth"
	if name != expected {
		t.Errorf("got %q, want %q", name, expected)
	}
}

func TestCreateClusterBranches(t *testing.T) {
	d := setupTestDB(t)
	ctx := context.Background()
	repoRoot := t.TempDir()
	mqGitSetup(t, repoRoot)

	// Create conductor and tasks with clusters
	d.CreateConductor(ctx, db.ConductorRecord{
		ID: "c-cluster", PID: 1, Goal: "test", Status: "active",
		StagingBranch: "staging/c-cluster", BaseBranch: "dev",
		MaxParallel: 4, ModelStrategy: "all-opus", Runtime: "local",
	})
	d.CreateTask(ctx, db.Task{
		ID: "T-001", Title: "Auth", Status: "pending", Role: "implementer",
		ConductorID:    sql.NullString{String: "c-cluster", Valid: true},
		FeatureCluster: sql.NullString{String: "auth", Valid: true},
	})
	d.CreateTask(ctx, db.Task{
		ID: "T-002", Title: "API", Status: "pending", Role: "implementer",
		ConductorID:    sql.NullString{String: "c-cluster", Valid: true},
		FeatureCluster: sql.NullString{String: "api", Valid: true},
	})

	c, _ := New(ConductorOpts{DB: d, RepoRoot: repoRoot})
	c.ConductorID = "c-cluster"

	err := c.createClusterBranches(ctx, "c-cluster", "staging/c-cluster")
	if err != nil {
		t.Fatalf("createClusterBranches error: %v", err)
	}

	// Verify branches exist
	for _, cluster := range []string{"auth", "api"} {
		branchName := FeatureClusterBranch("c-cluster", cluster)
		cmd := exec.Command("git", "rev-parse", "--verify", branchName)
		cmd.Dir = repoRoot
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Errorf("cluster branch %s not found: %v: %s", branchName, err, out)
		}
	}

	// Idempotent: calling again should not error
	err = c.createClusterBranches(ctx, "c-cluster", "staging/c-cluster")
	if err != nil {
		t.Fatalf("second createClusterBranches should be idempotent: %v", err)
	}
}

func TestIsClusterComplete(t *testing.T) {
	d := setupTestDB(t)
	ctx := context.Background()

	d.CreateConductor(ctx, db.ConductorRecord{
		ID: "c-001", PID: 1, Goal: "test", Status: "active",
		StagingBranch: "staging/c-001", BaseBranch: "dev",
		MaxParallel: 4, ModelStrategy: "all-opus", Runtime: "local",
	})

	// Create tasks in "auth" cluster
	d.CreateTask(ctx, db.Task{
		ID: "T-001", Title: "Task A", Status: "done", Role: "implementer",
		Branch:         sql.NullString{String: "feature/T-001", Valid: true},
		ConductorID:    sql.NullString{String: "c-001", Valid: true},
		FeatureCluster: sql.NullString{String: "auth", Valid: true},
	})
	d.CreateTask(ctx, db.Task{
		ID: "T-002", Title: "Task B", Status: "running", Role: "implementer",
		ConductorID:    sql.NullString{String: "c-001", Valid: true},
		FeatureCluster: sql.NullString{String: "auth", Valid: true},
	})

	// Create merge queue entry for T-001 (merged)
	d.CreateMergeQueueEntry(ctx, db.MergeQueueEntry{
		ID: "mq-001", ConductorID: "c-001", TaskID: "T-001",
		Branch: "feature/T-001", Position: 0, Status: "merged",
	})

	c, _ := New(ConductorOpts{DB: d, RepoRoot: "/tmp/test"})
	c.ConductorID = "c-001"

	// Cluster not complete: T-002 still running
	if c.isClusterComplete(ctx, "c-001", "auth") {
		t.Error("cluster should not be complete — T-002 is still running")
	}

	// Mark T-002 as done and create merged entry
	d.CompleteTask(ctx, "T-002", "ok")
	d.CreateMergeQueueEntry(ctx, db.MergeQueueEntry{
		ID: "mq-002", ConductorID: "c-001", TaskID: "T-002",
		Branch: "feature/T-002", Position: 1, Status: "merged",
	})

	// Now cluster should be complete
	if !c.isClusterComplete(ctx, "c-001", "auth") {
		t.Error("cluster should be complete — both tasks done and merged")
	}
}

func TestClusterMergeWorkflow(t *testing.T) {
	d := setupTestDB(t)
	ctx := context.Background()
	repoRoot := t.TempDir()
	mqGitSetup(t, repoRoot)

	// Create conductor
	d.CreateConductor(ctx, db.ConductorRecord{
		ID: "c-cluster", PID: 1, Goal: "test", Status: "active",
		StagingBranch: "staging/c-cluster", BaseBranch: "dev",
		MaxParallel: 4, ModelStrategy: "all-opus", Runtime: "local",
	})

	// Create tasks with clusters
	d.CreateTask(ctx, db.Task{
		ID: "T-001", Title: "Add auth", Status: "done", Role: "implementer",
		ConductorID:    sql.NullString{String: "c-cluster", Valid: true},
		FeatureCluster: sql.NullString{String: "auth", Valid: true},
	})

	c, _ := New(ConductorOpts{DB: d, RepoRoot: repoRoot})
	c.ConductorID = "c-cluster"

	// Create cluster branches
	err := c.createClusterBranches(ctx, "c-cluster", "staging/c-cluster")
	if err != nil {
		t.Fatalf("createClusterBranches error: %v", err)
	}

	clusterBranch := FeatureClusterBranch("c-cluster", "auth")

	// Create a task branch with a commit
	mqGitRun(t, repoRoot, "checkout", "-b", "feature/T-001", "staging/c-cluster")
	os.WriteFile(filepath.Join(repoRoot, "auth.go"), []byte("package auth\n"), 0o644)
	mqGitRun(t, repoRoot, "add", "auth.go")
	mqGitRun(t, repoRoot, "commit", "-m", "add auth module")

	// Merge task branch to cluster branch
	entry := &db.MergeQueueEntry{
		ID:          "mq-001",
		ConductorID: "c-cluster",
		TaskID:      "T-001",
		Branch:      "feature/T-001",
		Position:    0,
		Status:      "pending",
	}

	err = c.mergeOneEntryToCluster(ctx, entry, MergeOpts{})
	if err != nil {
		t.Fatalf("mergeOneEntryToCluster error: %v", err)
	}

	// Verify auth.go exists on cluster branch
	mqGitRun(t, repoRoot, "checkout", clusterBranch)
	if _, err := os.Stat(filepath.Join(repoRoot, "auth.go")); os.IsNotExist(err) {
		t.Error("auth.go should exist on cluster branch after merge")
	}

	// Merge cluster to staging
	err = c.mergeClusterToStaging(ctx, "c-cluster", "auth", MergeOpts{})
	if err != nil {
		t.Fatalf("mergeClusterToStaging error: %v", err)
	}

	// Verify auth.go exists on staging
	mqGitRun(t, repoRoot, "checkout", "staging/c-cluster")
	if _, err := os.Stat(filepath.Join(repoRoot, "auth.go")); os.IsNotExist(err) {
		t.Error("auth.go should exist on staging branch after cluster merge")
	}
}

func TestMergeOneEntryToCluster_NoClusters_FallsBackToStaging(t *testing.T) {
	d := setupTestDB(t)
	ctx := context.Background()
	repoRoot := t.TempDir()
	mqGitSetup(t, repoRoot)

	d.CreateConductor(ctx, db.ConductorRecord{
		ID: "c-flat", PID: 1, Goal: "test", Status: "active",
		StagingBranch: "staging/c-flat", BaseBranch: "dev",
		MaxParallel: 1, ModelStrategy: "all-opus", Runtime: "local",
	})

	// Create a staging branch
	mqGitRun(t, repoRoot, "branch", "staging/c-flat")

	// Create task WITHOUT feature_cluster
	d.CreateTask(ctx, db.Task{
		ID: "T-001", Title: "Simple", Status: "done", Role: "implementer",
		ConductorID: sql.NullString{String: "c-flat", Valid: true},
	})

	// Create a task branch with a commit
	mqGitRun(t, repoRoot, "checkout", "-b", "feature/T-001", "staging/c-flat")
	os.WriteFile(filepath.Join(repoRoot, "simple.go"), []byte("package simple\n"), 0o644)
	mqGitRun(t, repoRoot, "add", "simple.go")
	mqGitRun(t, repoRoot, "commit", "-m", "add simple")

	c, _ := New(ConductorOpts{DB: d, RepoRoot: repoRoot})
	c.ConductorID = "c-flat"

	entry := &db.MergeQueueEntry{
		ID:          "mq-001",
		ConductorID: "c-flat",
		TaskID:      "T-001",
		Branch:      "feature/T-001",
		Position:    0,
		Status:      "pending",
	}

	// Should fall back to merging directly to staging (no cluster)
	err := c.mergeOneEntryToCluster(ctx, entry, MergeOpts{})
	if err != nil {
		t.Fatalf("mergeOneEntryToCluster should fall back to staging: %v", err)
	}

	// Verify simple.go exists on staging
	mqGitRun(t, repoRoot, "checkout", "staging/c-flat")
	if _, err := os.Stat(filepath.Join(repoRoot, "simple.go")); os.IsNotExist(err) {
		t.Error("simple.go should exist on staging branch after fallback merge")
	}
}

// --- G117: Refinement re-enqueue test ---

func TestEnqueueCompletedTasks_ReEnqueuesAfterRefinement(t *testing.T) {
	d := setupTestDB(t)
	ctx := context.Background()

	d.CreateConductor(ctx, db.ConductorRecord{
		ID: "c-001", PID: 1, Goal: "test", Status: "active",
		StagingBranch: "staging/c-001", BaseBranch: "dev",
		MaxParallel: 1, ModelStrategy: "all-opus", Runtime: "local",
	})

	// Create a done task with branch
	d.CreateTask(ctx, db.Task{
		ID: "T-001", Title: "Test", Status: "done", Role: "implementer",
		Branch:      sql.NullString{String: "feature/T-001", Valid: true},
		ConductorID: sql.NullString{String: "c-001", Valid: true},
	})

	// Create a merge queue entry in "refining" status (simulating post-refinement)
	d.CreateMergeQueueEntry(ctx, db.MergeQueueEntry{
		ID: "mq-001", ConductorID: "c-001", TaskID: "T-001",
		Branch: "feature/T-001", Position: 0, Status: "refining",
		RefinementCount: 1,
	})

	c, _ := New(ConductorOpts{DB: d, RepoRoot: "/tmp/test"})
	c.ConductorID = "c-001"

	// Enqueue — should transition "refining" → "pending"
	err := c.enqueueCompletedTasks(ctx, "c-001")
	if err != nil {
		t.Fatalf("enqueue error: %v", err)
	}

	entry, err := d.GetMergeQueueEntryByTaskID(ctx, "c-001", "T-001")
	if err != nil {
		t.Fatalf("get entry error: %v", err)
	}
	if entry == nil {
		t.Fatal("expected entry to exist")
	}
	if entry.Status != "pending" {
		t.Errorf("expected status 'pending' after re-enqueue, got %q", entry.Status)
	}
}

// --- G118: isClusterComplete tests ---

func TestIsClusterComplete_FailedTasksNotComplete(t *testing.T) {
	d := setupTestDB(t)
	ctx := context.Background()

	d.CreateConductor(ctx, db.ConductorRecord{
		ID: "c-001", PID: 1, Goal: "test", Status: "active",
		StagingBranch: "staging/c-001", BaseBranch: "dev",
		MaxParallel: 4, ModelStrategy: "all-opus", Runtime: "local",
	})

	// Create tasks in "auth" cluster — one done+merged, one failed
	d.CreateTask(ctx, db.Task{
		ID: "T-001", Title: "Task A", Status: "done", Role: "implementer",
		Branch:         sql.NullString{String: "feature/T-001", Valid: true},
		ConductorID:    sql.NullString{String: "c-001", Valid: true},
		FeatureCluster: sql.NullString{String: "auth", Valid: true},
	})
	d.CreateTask(ctx, db.Task{
		ID: "T-002", Title: "Task B", Status: "failed", Role: "implementer",
		ConductorID:    sql.NullString{String: "c-001", Valid: true},
		FeatureCluster: sql.NullString{String: "auth", Valid: true},
	})

	// Merged entry for T-001
	d.CreateMergeQueueEntry(ctx, db.MergeQueueEntry{
		ID: "mq-001", ConductorID: "c-001", TaskID: "T-001",
		Branch: "feature/T-001", Position: 0, Status: "merged",
	})

	c, _ := New(ConductorOpts{DB: d, RepoRoot: "/tmp/test"})
	c.ConductorID = "c-001"

	// G118: Cluster should NOT be complete when any task is failed
	if c.isClusterComplete(ctx, "c-001", "auth") {
		t.Error("cluster should NOT be complete — T-002 is failed")
	}
}

func TestIsClusterComplete_AllFailedNotComplete(t *testing.T) {
	d := setupTestDB(t)
	ctx := context.Background()

	d.CreateConductor(ctx, db.ConductorRecord{
		ID: "c-001", PID: 1, Goal: "test", Status: "active",
		StagingBranch: "staging/c-001", BaseBranch: "dev",
		MaxParallel: 4, ModelStrategy: "all-opus", Runtime: "local",
	})

	// Both tasks failed
	d.CreateTask(ctx, db.Task{
		ID: "T-001", Title: "Task A", Status: "failed", Role: "implementer",
		ConductorID:    sql.NullString{String: "c-001", Valid: true},
		FeatureCluster: sql.NullString{String: "auth", Valid: true},
	})
	d.CreateTask(ctx, db.Task{
		ID: "T-002", Title: "Task B", Status: "failed", Role: "implementer",
		ConductorID:    sql.NullString{String: "c-001", Valid: true},
		FeatureCluster: sql.NullString{String: "auth", Valid: true},
	})

	c, _ := New(ConductorOpts{DB: d, RepoRoot: "/tmp/test"})
	c.ConductorID = "c-001"

	// G118: All-failed cluster should NOT be complete
	if c.isClusterComplete(ctx, "c-001", "auth") {
		t.Error("all-failed cluster should NOT be complete")
	}
}

// --- Content clobber detection tests ---

// clobberGitSetup initializes a git repo with a base branch containing a file with N lines.
func clobberGitSetup(t *testing.T, dir string, fileName string, lineCount int) {
	t.Helper()
	mqGitRun(t, dir, "init")
	mqGitRun(t, dir, "checkout", "-b", "main")

	// Create a file with lineCount lines
	var content strings.Builder
	for i := 1; i <= lineCount; i++ {
		fmt.Fprintf(&content, "line %d\n", i)
	}
	os.WriteFile(filepath.Join(dir, fileName), []byte(content.String()), 0o644)
	mqGitRun(t, dir, "add", ".")
	mqGitRun(t, dir, "commit", "-m", "initial with "+fileName)
	mqGitRun(t, dir, "branch", "staging/c-clobber")
}

func TestCheckContentClobber_Over50Percent(t *testing.T) {
	d := setupTestDB(t)
	ctx := context.Background()
	repoRoot := t.TempDir()

	// Create repo with a 100-line file
	clobberGitSetup(t, repoRoot, "big.go", 100)

	d.CreateConductor(ctx, db.ConductorRecord{
		ID: "c-clobber", PID: 1, Goal: "test", Status: "active",
		StagingBranch: "staging/c-clobber", BaseBranch: "main",
		MaxParallel: 1, ModelStrategy: "all-opus", Runtime: "local",
	})

	// Create feature branch that reduces the file to 40 lines (60% reduction)
	mqGitRun(t, repoRoot, "checkout", "-b", "feature/T-clobber", "staging/c-clobber")
	var reduced strings.Builder
	for i := 1; i <= 40; i++ {
		fmt.Fprintf(&reduced, "line %d\n", i)
	}
	os.WriteFile(filepath.Join(repoRoot, "big.go"), []byte(reduced.String()), 0o644)
	mqGitRun(t, repoRoot, "add", "big.go")
	mqGitRun(t, repoRoot, "commit", "-m", "reduce big.go")

	// Create task
	d.CreateTask(ctx, db.Task{
		ID: "T-clobber", Title: "Clobber test", Status: "done", Role: "implementer",
		Branch:      sql.NullString{String: "feature/T-clobber", Valid: true},
		ConductorID: sql.NullString{String: "c-clobber", Valid: true},
	})

	c, _ := New(ConductorOpts{DB: d, RepoRoot: repoRoot})
	c.ConductorID = "c-clobber"

	// Merge the feature branch into staging
	entry := &db.MergeQueueEntry{
		ID:          "mq-clobber",
		ConductorID: "c-clobber",
		TaskID:      "T-clobber",
		Branch:      "feature/T-clobber",
		Position:    0,
		Status:      "pending",
	}
	err := c.mergeOneEntry(ctx, entry, MergeOpts{})
	if err != nil {
		t.Fatalf("mergeOneEntry error: %v", err)
	}

	// Verify that the merge_content_clobber event was logged
	events, err := d.RecentEvents(ctx, 20)
	if err != nil {
		t.Fatalf("RecentEvents error: %v", err)
	}
	found := false
	for _, e := range events {
		if e.EventType == "merge_content_clobber" {
			found = true
			if !e.Payload.Valid {
				t.Error("expected payload to be set")
				break
			}
			payload := e.Payload.String
			if !strings.Contains(payload, `"big.go"`) {
				t.Errorf("payload should contain file name, got: %s", payload)
			}
			if !strings.Contains(payload, `"before_lines":100`) {
				t.Errorf("payload should contain before_lines:100, got: %s", payload)
			}
			if !strings.Contains(payload, `"after_lines":40`) {
				t.Errorf("payload should contain after_lines:40, got: %s", payload)
			}
			if !strings.Contains(payload, `"reduction_pct":60`) {
				t.Errorf("payload should contain reduction_pct ~60, got: %s", payload)
			}
			break
		}
	}
	if !found {
		t.Error("expected merge_content_clobber event to be logged for >50% reduction")
	}
}

func TestCheckContentClobber_Exactly50Percent_NoTrigger(t *testing.T) {
	d := setupTestDB(t)
	ctx := context.Background()
	repoRoot := t.TempDir()

	// Create repo with a 100-line file
	clobberGitSetup(t, repoRoot, "half.go", 100)

	d.CreateConductor(ctx, db.ConductorRecord{
		ID: "c-clobber", PID: 1, Goal: "test", Status: "active",
		StagingBranch: "staging/c-clobber", BaseBranch: "main",
		MaxParallel: 1, ModelStrategy: "all-opus", Runtime: "local",
	})

	// Create feature branch that reduces the file to exactly 50 lines (exactly 50%)
	mqGitRun(t, repoRoot, "checkout", "-b", "feature/T-half", "staging/c-clobber")
	var halfContent strings.Builder
	for i := 1; i <= 50; i++ {
		fmt.Fprintf(&halfContent, "line %d\n", i)
	}
	os.WriteFile(filepath.Join(repoRoot, "half.go"), []byte(halfContent.String()), 0o644)
	mqGitRun(t, repoRoot, "add", "half.go")
	mqGitRun(t, repoRoot, "commit", "-m", "reduce half.go to 50%")

	d.CreateTask(ctx, db.Task{
		ID: "T-half", Title: "Half test", Status: "done", Role: "implementer",
		Branch:      sql.NullString{String: "feature/T-half", Valid: true},
		ConductorID: sql.NullString{String: "c-clobber", Valid: true},
	})

	c, _ := New(ConductorOpts{DB: d, RepoRoot: repoRoot})
	c.ConductorID = "c-clobber"

	entry := &db.MergeQueueEntry{
		ID:          "mq-half",
		ConductorID: "c-clobber",
		TaskID:      "T-half",
		Branch:      "feature/T-half",
		Position:    0,
		Status:      "pending",
	}
	err := c.mergeOneEntry(ctx, entry, MergeOpts{})
	if err != nil {
		t.Fatalf("mergeOneEntry error: %v", err)
	}

	// Verify that NO merge_content_clobber event was logged
	events, err := d.RecentEvents(ctx, 20)
	if err != nil {
		t.Fatalf("RecentEvents error: %v", err)
	}
	for _, e := range events {
		if e.EventType == "merge_content_clobber" {
			t.Error("exactly 50% reduction should NOT trigger merge_content_clobber")
		}
	}
}

func TestCheckContentClobber_Under50Percent_NoTrigger(t *testing.T) {
	d := setupTestDB(t)
	ctx := context.Background()
	repoRoot := t.TempDir()

	// Create repo with a 100-line file
	clobberGitSetup(t, repoRoot, "small.go", 100)

	d.CreateConductor(ctx, db.ConductorRecord{
		ID: "c-clobber", PID: 1, Goal: "test", Status: "active",
		StagingBranch: "staging/c-clobber", BaseBranch: "main",
		MaxParallel: 1, ModelStrategy: "all-opus", Runtime: "local",
	})

	// Create feature branch that reduces the file to 60 lines (40% reduction — under threshold)
	mqGitRun(t, repoRoot, "checkout", "-b", "feature/T-small", "staging/c-clobber")
	var smallContent strings.Builder
	for i := 1; i <= 60; i++ {
		fmt.Fprintf(&smallContent, "line %d\n", i)
	}
	os.WriteFile(filepath.Join(repoRoot, "small.go"), []byte(smallContent.String()), 0o644)
	mqGitRun(t, repoRoot, "add", "small.go")
	mqGitRun(t, repoRoot, "commit", "-m", "reduce small.go slightly")

	d.CreateTask(ctx, db.Task{
		ID: "T-small", Title: "Small test", Status: "done", Role: "implementer",
		Branch:      sql.NullString{String: "feature/T-small", Valid: true},
		ConductorID: sql.NullString{String: "c-clobber", Valid: true},
	})

	c, _ := New(ConductorOpts{DB: d, RepoRoot: repoRoot})
	c.ConductorID = "c-clobber"

	entry := &db.MergeQueueEntry{
		ID:          "mq-small",
		ConductorID: "c-clobber",
		TaskID:      "T-small",
		Branch:      "feature/T-small",
		Position:    0,
		Status:      "pending",
	}
	err := c.mergeOneEntry(ctx, entry, MergeOpts{})
	if err != nil {
		t.Fatalf("mergeOneEntry error: %v", err)
	}

	// Verify that NO merge_content_clobber event was logged
	events, err := d.RecentEvents(ctx, 20)
	if err != nil {
		t.Fatalf("RecentEvents error: %v", err)
	}
	for _, e := range events {
		if e.EventType == "merge_content_clobber" {
			t.Error("40% reduction should NOT trigger merge_content_clobber")
		}
	}
}

func TestCountLines(t *testing.T) {
	tests := []struct {
		input    string
		expected int
	}{
		{"", 0},
		{"hello\n", 1},
		{"hello", 1},
		{"a\nb\nc\n", 3},
		{"a\nb\nc", 3},
	}
	for _, tt := range tests {
		got := countLines(tt.input)
		if got != tt.expected {
			t.Errorf("countLines(%q) = %d, want %d", tt.input, got, tt.expected)
		}
	}
}
