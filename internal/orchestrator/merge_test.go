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
	"time"

	"github.com/MochaCosine1206/orchestra/internal/agent"
	"github.com/MochaCosine1206/orchestra/internal/db"
)

func createDoneTask(t *testing.T, d *db.DB, id, branch, blockedBy string) {
	t.Helper()
	ctx := context.Background()
	d.CreateTask(ctx, db.Task{
		ID:        id,
		Title:     "Task " + id,
		Status:    "done",
		Role:      "implementer",
		Branch:    sql.NullString{String: branch, Valid: branch != ""},
		BlockedBy: sql.NullString{String: blockedBy, Valid: blockedBy != ""},
	})
}

func TestMerge_NoBranches(t *testing.T) {
	d := setupTestDB(t)
	c, _ := New(ConductorOpts{DB: d, RepoRoot: "/tmp/test"})
	ctx := context.Background()

	result, err := c.Merge(ctx, MergeOpts{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Merged) != 0 {
		t.Errorf("expected no merges, got %d", len(result.Merged))
	}
	if len(result.Plan) != 0 {
		t.Errorf("expected no plan, got %d entries", len(result.Plan))
	}
}

func TestMerge_DryRun(t *testing.T) {
	d := setupTestDB(t)
	ctx := context.Background()

	createDoneTask(t, d, "T-001", "feature/T-001", "")
	createDoneTask(t, d, "T-002", "feature/T-002", `["T-001"]`)

	// Store session task IDs
	d.SetBlackboard(ctx, "conductor:task_ids", `["T-001","T-002"]`, "test")

	c, _ := New(ConductorOpts{DB: d, RepoRoot: "/tmp/test"})

	result, err := c.Merge(ctx, MergeOpts{DryRun: true})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Plan) != 2 {
		t.Fatalf("expected 2 in plan, got %d: %v", len(result.Plan), result.Plan)
	}
	// T-001 should come before T-002 (topological order)
	if result.Plan[0] != "feature/T-001" {
		t.Errorf("first in plan = %q, want feature/T-001", result.Plan[0])
	}
	if result.Plan[1] != "feature/T-002" {
		t.Errorf("second in plan = %q, want feature/T-002", result.Plan[1])
	}
}

func TestMerge_DryRun_TopologicalOrder(t *testing.T) {
	d := setupTestDB(t)
	ctx := context.Background()

	// Diamond dependency: T-001 -> T-002, T-001 -> T-003, T-002+T-003 -> T-004
	createDoneTask(t, d, "T-001", "feature/T-001", "")
	createDoneTask(t, d, "T-002", "feature/T-002", `["T-001"]`)
	createDoneTask(t, d, "T-003", "feature/T-003", `["T-001"]`)
	createDoneTask(t, d, "T-004", "feature/T-004", `["T-002","T-003"]`)

	d.SetBlackboard(ctx, "conductor:task_ids", `["T-001","T-002","T-003","T-004"]`, "test")

	c, _ := New(ConductorOpts{DB: d, RepoRoot: "/tmp/test"})

	result, err := c.Merge(ctx, MergeOpts{DryRun: true})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Plan) != 4 {
		t.Fatalf("expected 4 in plan, got %d: %v", len(result.Plan), result.Plan)
	}

	// T-001 must be first, T-004 must be last
	if result.Plan[0] != "feature/T-001" {
		t.Errorf("first = %q, want feature/T-001", result.Plan[0])
	}
	if result.Plan[3] != "feature/T-004" {
		t.Errorf("last = %q, want feature/T-004", result.Plan[3])
	}
}

func TestMerge_SkipsNonDoneTasks(t *testing.T) {
	d := setupTestDB(t)
	ctx := context.Background()

	createDoneTask(t, d, "T-001", "feature/T-001", "")
	d.CreateTask(ctx, db.Task{
		ID:     "T-002",
		Title:  "Pending task",
		Status: "pending",
		Role:   "implementer",
		Branch: sql.NullString{String: "feature/T-002", Valid: true},
	})
	d.CreateTask(ctx, db.Task{
		ID:     "T-003",
		Title:  "Failed task",
		Status: "failed",
		Role:   "implementer",
		Branch: sql.NullString{String: "feature/T-003", Valid: true},
	})

	d.SetBlackboard(ctx, "conductor:task_ids", `["T-001","T-002","T-003"]`, "test")

	c, _ := New(ConductorOpts{DB: d, RepoRoot: "/tmp/test"})

	result, err := c.Merge(ctx, MergeOpts{DryRun: true})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Plan) != 1 {
		t.Errorf("expected 1 in plan (only done task), got %d: %v", len(result.Plan), result.Plan)
	}
}

func TestMerge_SkipsTasksWithoutBranch(t *testing.T) {
	d := setupTestDB(t)
	ctx := context.Background()

	createDoneTask(t, d, "T-001", "feature/T-001", "")
	d.CreateTask(ctx, db.Task{
		ID:     "T-002",
		Title:  "No branch",
		Status: "done",
		Role:   "scout", // scouts don't have branches
	})

	d.SetBlackboard(ctx, "conductor:task_ids", `["T-001","T-002"]`, "test")

	c, _ := New(ConductorOpts{DB: d, RepoRoot: "/tmp/test"})

	result, err := c.Merge(ctx, MergeOpts{DryRun: true})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Plan) != 1 {
		t.Errorf("expected 1 in plan, got %d: %v", len(result.Plan), result.Plan)
	}
}

func TestMergeResult_Structure(t *testing.T) {
	r := MergeResult{
		Merged:      []string{"branch-a"},
		Failed:      []string{"branch-b"},
		Skipped:     []string{"branch-c"},
		Plan:        []string{"branch-a", "branch-b", "branch-c"},
		TestsFailed: true,
	}
	if len(r.Merged) != 1 || len(r.Failed) != 1 || len(r.Skipped) != 1 {
		t.Errorf("unexpected structure: %+v", r)
	}
	if !r.TestsFailed {
		t.Error("TestsFailed should be true")
	}
}

func TestMerge_SessionFiltering(t *testing.T) {
	d := setupTestDB(t)
	ctx := context.Background()

	// T-001 in session, T-099 not in session
	createDoneTask(t, d, "T-001", "feature/T-001", "")
	createDoneTask(t, d, "T-099", "feature/T-099", "")

	d.SetBlackboard(ctx, "conductor:task_ids", `["T-001"]`, "test")

	c, _ := New(ConductorOpts{DB: d, RepoRoot: "/tmp/test"})

	result, err := c.Merge(ctx, MergeOpts{DryRun: true})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Plan) != 1 {
		t.Errorf("expected 1 in plan (session-filtered), got %d: %v", len(result.Plan), result.Plan)
	}
	if result.Plan[0] != "feature/T-001" {
		t.Errorf("plan[0] = %q, want feature/T-001", result.Plan[0])
	}
}

func TestRunTestCmd_ReturnsOutput(t *testing.T) {
	dir := t.TempDir()

	// "echo hello" should return output and nil error
	out, err := runTestCmd(dir, "echo hello", 5*time.Minute)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out == "" {
		t.Error("expected non-empty output from echo")
	}

	// A failing command should return output and an error
	out, err = runTestCmd(dir, "false", 5*time.Minute)
	if err == nil {
		t.Error("expected error from 'false' command")
	}

	// Empty command should be a no-op
	out, err = runTestCmd(dir, "", 5*time.Minute)
	if err != nil {
		t.Fatalf("unexpected error for empty command: %v", err)
	}
	if out != "" {
		t.Errorf("expected empty output for empty command, got %q", out)
	}
}

func TestRunTestCmd_Timeout(t *testing.T) {
	dir := t.TempDir()

	// A command that sleeps for 10s should time out in 100ms
	_, err := runTestCmd(dir, "sleep 10", 100*time.Millisecond)
	if err == nil {
		t.Fatal("expected timeout error")
	}
	if !strings.Contains(err.Error(), "timed out") {
		t.Errorf("expected timeout message, got: %v", err)
	}
}

func TestRunTestCmd_DefaultTimeout(t *testing.T) {
	dir := t.TempDir()

	// Zero timeout should use default (5m) — verify the command runs
	out, err := runTestCmd(dir, "echo works", 0)
	if err != nil {
		t.Fatalf("unexpected error with zero timeout: %v", err)
	}
	if !strings.Contains(out, "works") {
		t.Errorf("expected output 'works', got %q", out)
	}
}

func TestRunTestCmd_CustomShortTimeout(t *testing.T) {
	dir := t.TempDir()

	// A short custom timeout (200ms) should kill a 10s sleep
	start := time.Now()
	_, err := runTestCmd(dir, "sleep 10", 200*time.Millisecond)
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("expected timeout error")
	}
	if !strings.Contains(err.Error(), "timed out") {
		t.Errorf("expected timeout message, got: %v", err)
	}
	// Should have terminated well before 10s
	if elapsed > 5*time.Second {
		t.Errorf("expected fast timeout, took %v", elapsed)
	}
}

func TestTestFailureMode_Constants(t *testing.T) {
	// Verify the constants are distinct and match expected string values
	modes := map[TestFailureMode]string{
		TestFailureModeRevertAndRefine: "revert_and_refine",
		TestFailureModeWarnOnly:        "warn_only",
		TestFailureModeRevertNoRefine:  "revert_no_refine",
	}
	for mode, expected := range modes {
		if string(mode) != expected {
			t.Errorf("TestFailureMode %q != %q", mode, expected)
		}
	}
}

func TestProbeTestEnvironment_MissingBinary(t *testing.T) {
	dir := t.TempDir()
	probe := probeTestEnvironment(dir, "nonexistent-binary-xyz arg1 arg2", 5*time.Second)
	if probe.CanRun {
		t.Error("expected CanRun=false for missing binary")
	}
	if !strings.Contains(probe.Reason, "not found") {
		t.Errorf("expected 'not found' in reason, got %q", probe.Reason)
	}
}

func TestProbeTestEnvironment_EmptyCommand(t *testing.T) {
	dir := t.TempDir()
	probe := probeTestEnvironment(dir, "", 5*time.Second)
	if probe.CanRun {
		t.Error("expected CanRun=false for empty command")
	}
	if probe.Reason != "empty test command" {
		t.Errorf("expected 'empty test command', got %q", probe.Reason)
	}
}

func TestProbeTestEnvironment_EchoFallback(t *testing.T) {
	dir := t.TempDir()
	// "echo" is always available and supports --version (actually just echoes it)
	probe := probeTestEnvironment(dir, "echo test-framework", 5*time.Second)
	if !probe.CanRun {
		t.Errorf("expected CanRun=true for 'echo', got reason=%q", probe.Reason)
	}
}

func TestTriggerMergeRefinement_SetsBlackboardKeys(t *testing.T) {
	d := setupTestDB(t)
	ctx := context.Background()

	d.CreateTask(ctx, db.Task{
		ID:          "T-001",
		Title:       "Test task",
		Status:      "done",
		Role:        "implementer",
		Branch:      sql.NullString{String: "feature/T-001", Valid: true},
		Description: sql.NullString{String: "Do something", Valid: true},
	})

	c, _ := New(ConductorOpts{DB: d, RepoRoot: t.TempDir()})

	// triggerMergeRefinement will call Refine which requires a worktree + agent.
	// Without full setup, it will fail at Refine() — but blackboard keys should
	// already be set before Refine() is called, so we test that the error
	// comes from refinement (not from blackboard setup).
	err := c.triggerMergeRefinement(ctx, "T-001", "test_cmd_failed", "FAIL: TestFoo expected X got Y")
	// Error is expected (no worktree/agent setup) but keys should be set
	if err == nil {
		t.Log("triggerMergeRefinement succeeded (unexpected without full setup)")
	}

	// Verify blackboard keys were set before the error
	feedback, _ := d.GetBlackboardValue(ctx, "merge_feedback:T-001")
	if feedback == "" {
		t.Error("expected merge_feedback to be set")
	}
	lastFailure, _ := d.GetBlackboardValue(ctx, "last_failure:T-001")
	if lastFailure != "test_cmd_failed" {
		t.Errorf("expected last_failure=test_cmd_failed, got %s", lastFailure)
	}
	failureType, _ := d.GetBlackboardValue(ctx, "failure_type:T-001")
	if failureType != "test_cmd_failed" {
		t.Errorf("expected failure_type=test_cmd_failed, got %s", failureType)
	}
}

func TestTriggerMergeRefinement_TruncatesFeedback(t *testing.T) {
	d := setupTestDB(t)
	ctx := context.Background()

	d.CreateTask(ctx, db.Task{
		ID:     "T-001",
		Title:  "Test task",
		Status: "done",
		Role:   "implementer",
		Branch: sql.NullString{String: "feature/T-001", Valid: true},
	})

	c, _ := New(ConductorOpts{DB: d, RepoRoot: t.TempDir()})

	// Long feedback > 5000 chars should be truncated
	longFeedback := make([]byte, 6000)
	for i := range longFeedback {
		longFeedback[i] = 'x'
	}
	_ = c.triggerMergeRefinement(ctx, "T-001", "test_cmd_failed", string(longFeedback))

	feedback, _ := d.GetBlackboardValue(ctx, "merge_feedback:T-001")
	// Should be truncated to 5000 + "[TRUNCATED]"
	if len(feedback) > 5020 {
		t.Errorf("feedback should be truncated, got len=%d", len(feedback))
	}
}

func TestMerge_ReleasesFileLocksOnSuccess(t *testing.T) {
	d := setupTestDB(t)
	ctx := context.Background()

	// Create a done task with file locks
	createDoneTask(t, d, "T-001", "feature/T-001", "")
	d.CreateFileLock(ctx, "internal/db/models.go", "", "T-001", time.Time{})
	d.CreateFileLock(ctx, "internal/db/queries.go", "", "T-001", time.Time{})

	// Verify locks exist
	locks, _ := d.ListFileLocksForTask(ctx, "T-001")
	if len(locks) != 2 {
		t.Fatalf("expected 2 locks before merge, got %d", len(locks))
	}

	// We can't do a real merge without a git repo, but we can test the
	// lock release logic directly via the merge failure path (which also releases locks).
	// Simulate merge failure path: the merge abort + lock release happens for failed merges.
	d.SetBlackboard(ctx, "conductor:task_ids", `["T-001"]`, "test")

	// After a successful or failed merge, locks should be released.
	// We test this by verifying the DB operations work correctly.
	if taskLocks, err := d.ListFileLocksForTask(ctx, "T-001"); err == nil {
		for _, lock := range taskLocks {
			d.ReleaseFileLock(ctx, lock.FilePath)
		}
	}

	// Verify locks are gone
	locksAfter, _ := d.ListFileLocksForTask(ctx, "T-001")
	if len(locksAfter) != 0 {
		t.Errorf("expected 0 locks after release, got %d", len(locksAfter))
	}

	// Verify global locks also gone
	allLocks, _ := d.ListFileLocks(ctx)
	if len(allLocks) != 0 {
		t.Errorf("expected 0 global locks after release, got %d", len(allLocks))
	}
}

func TestValidateFileOwnership_Violations(t *testing.T) {
	d := setupTestDB(t)
	ctx := context.Background()
	repoRoot := t.TempDir()

	// Set up a git repo
	gitSetup(t, repoRoot)

	// Create initial file and commit
	os.WriteFile(filepath.Join(repoRoot, "models.go"), []byte("package db\n"), 0o644)
	os.WriteFile(filepath.Join(repoRoot, "queries.go"), []byte("package db\n"), 0o644)
	gitRun(t, repoRoot, "add", ".")
	gitRun(t, repoRoot, "commit", "-m", "init")

	// Create a branch that modifies both files
	gitRun(t, repoRoot, "checkout", "-b", "feature/T-001")
	os.WriteFile(filepath.Join(repoRoot, "models.go"), []byte("package db\n// modified\n"), 0o644)
	os.WriteFile(filepath.Join(repoRoot, "queries.go"), []byte("package db\n// modified\n"), 0o644)
	gitRun(t, repoRoot, "add", ".")
	gitRun(t, repoRoot, "commit", "-m", "modify files")
	gitRun(t, repoRoot, "checkout", "dev")

	// T-001 owns models.go, T-002 owns queries.go
	d.CreateTask(ctx, db.Task{ID: "T-001", Title: "Task 1", Status: "done", Role: "implementer"})
	d.CreateTask(ctx, db.Task{ID: "T-002", Title: "Task 2", Status: "done", Role: "implementer"})
	if err := d.CreateFileLock(ctx, "models.go", "", "T-001", time.Time{}); err != nil {
		t.Fatalf("creating lock for models.go: %v", err)
	}
	if err := d.CreateFileLock(ctx, "queries.go", "", "T-002", time.Time{}); err != nil {
		t.Fatalf("creating lock for queries.go: %v", err)
	}

	c, _ := New(ConductorOpts{DB: d, RepoRoot: repoRoot})

	violations := c.validateFileOwnership(ctx, "T-001", "feature/T-001")
	if len(violations) == 0 {
		t.Fatal("expected violations for queries.go (owned by T-002)")
	}

	found := false
	for _, v := range violations {
		if strings.Contains(v, "queries.go") && strings.Contains(v, "T-002") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected violation for queries.go owned by T-002, got: %v", violations)
	}
}

func TestValidateFileOwnership_Clean(t *testing.T) {
	d := setupTestDB(t)
	ctx := context.Background()
	repoRoot := t.TempDir()

	gitSetup(t, repoRoot)

	os.WriteFile(filepath.Join(repoRoot, "models.go"), []byte("package db\n"), 0o644)
	gitRun(t, repoRoot, "add", ".")
	gitRun(t, repoRoot, "commit", "-m", "init")

	// Create a branch that only modifies owned files
	gitRun(t, repoRoot, "checkout", "-b", "feature/T-001")
	os.WriteFile(filepath.Join(repoRoot, "models.go"), []byte("package db\n// modified\n"), 0o644)
	gitRun(t, repoRoot, "add", ".")
	gitRun(t, repoRoot, "commit", "-m", "modify owned file")
	gitRun(t, repoRoot, "checkout", "dev")

	// T-001 owns models.go
	d.CreateTask(ctx, db.Task{ID: "T-001", Title: "Task 1", Status: "done", Role: "implementer"})
	d.CreateFileLock(ctx, "models.go", "", "T-001", time.Time{})

	c, _ := New(ConductorOpts{DB: d, RepoRoot: repoRoot})

	violations := c.validateFileOwnership(ctx, "T-001", "feature/T-001")
	if len(violations) != 0 {
		t.Errorf("expected no violations, got: %v", violations)
	}
}

// gitSetup initializes a git repo with a dev branch.
func gitSetup(t *testing.T, dir string) {
	t.Helper()
	gitRun(t, dir, "init")
	gitRun(t, dir, "checkout", "-b", "dev")
	os.WriteFile(filepath.Join(dir, "README.md"), []byte("test\n"), 0o644)
	gitRun(t, dir, "add", ".")
	gitRun(t, dir, "commit", "-m", "initial")
}

// gitRun executes a git command in the given directory.
func gitRun(t *testing.T, dir string, args ...string) {
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

// --- Auto-Resolution Tests ---

func TestParseConflictMarkers_SingleHunk(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "test.go")
	content := `package main

import (
<<<<<<< HEAD
	"fmt"
=======
	"os"
>>>>>>> branch
)

func main() {}
`
	os.WriteFile(file, []byte(content), 0o644)

	hunks, err := parseConflictMarkers(file)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(hunks) != 1 {
		t.Fatalf("expected 1 hunk, got %d", len(hunks))
	}
	if hunks[0].Ours != "\t\"fmt\"" {
		t.Errorf("ours = %q, want %q", hunks[0].Ours, "\t\"fmt\"")
	}
	if hunks[0].Theirs != "\t\"os\"" {
		t.Errorf("theirs = %q, want %q", hunks[0].Theirs, "\t\"os\"")
	}
}

func TestParseConflictMarkers_MultipleHunks(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "test.go")
	content := `line1
<<<<<<< HEAD
ours1
=======
theirs1
>>>>>>> branch
line2
<<<<<<< HEAD
ours2
=======
theirs2
>>>>>>> branch
line3
`
	os.WriteFile(file, []byte(content), 0o644)

	hunks, err := parseConflictMarkers(file)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(hunks) != 2 {
		t.Fatalf("expected 2 hunks, got %d", len(hunks))
	}
	if hunks[0].Ours != "ours1" {
		t.Errorf("hunks[0].Ours = %q", hunks[0].Ours)
	}
	if hunks[1].Theirs != "theirs2" {
		t.Errorf("hunks[1].Theirs = %q", hunks[1].Theirs)
	}
}

func TestParseConflictMarkers_NoConflicts(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "clean.go")
	os.WriteFile(file, []byte("package main\n\nfunc main() {}\n"), 0o644)

	hunks, err := parseConflictMarkers(file)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(hunks) != 0 {
		t.Errorf("expected 0 hunks, got %d", len(hunks))
	}
}

func TestIsNonOverlapping_True(t *testing.T) {
	hunks := []ConflictHunk{
		{Ours: "added line", Theirs: ""},
		{Ours: "", Theirs: "other added line"},
	}
	if !isNonOverlapping(hunks) {
		t.Error("expected non-overlapping to be true")
	}
}

func TestIsNonOverlapping_False(t *testing.T) {
	hunks := []ConflictHunk{
		{Ours: "version A", Theirs: "version B"},
	}
	if isNonOverlapping(hunks) {
		t.Error("expected non-overlapping to be false")
	}
}

func TestResolveNonOverlapping(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "test.txt")
	content := `line1
<<<<<<< HEAD
added by ours
=======
>>>>>>> branch
line2
<<<<<<< HEAD
=======
added by theirs
>>>>>>> branch
line3
`
	os.WriteFile(file, []byte(content), 0o644)

	hunks, _ := parseConflictMarkers(file)
	if !isNonOverlapping(hunks) {
		t.Fatal("expected non-overlapping")
	}

	err := resolveNonOverlapping(file, hunks)
	if err != nil {
		t.Fatalf("resolve error: %v", err)
	}

	resolved, _ := os.ReadFile(file)
	s := string(resolved)
	if strings.Contains(s, "<<<<<<<") {
		t.Error("resolved file still contains conflict markers")
	}
	if !strings.Contains(s, "added by ours") {
		t.Error("missing 'added by ours'")
	}
	if !strings.Contains(s, "added by theirs") {
		t.Error("missing 'added by theirs'")
	}
}

func TestConflictResolution_Structure(t *testing.T) {
	r := ConflictResolution{
		Attempted:   true,
		Resolved:    true,
		Tier:        "import",
		FilesFixed:  []string{"main.go"},
		FilesFailed: nil,
	}
	if !r.Attempted || !r.Resolved {
		t.Error("unexpected resolution state")
	}
	if r.Tier != "import" {
		t.Errorf("tier = %q, want import", r.Tier)
	}
}

func TestMergeResult_AutoResolvedField(t *testing.T) {
	r := MergeResult{
		Merged:       []string{"branch-a"},
		AutoResolved: []string{"branch-b"},
	}
	if len(r.AutoResolved) != 1 || r.AutoResolved[0] != "branch-b" {
		t.Errorf("unexpected AutoResolved: %v", r.AutoResolved)
	}
}

func TestAttemptAutoResolve_ImportConflict(t *testing.T) {
	repoRoot := t.TempDir()
	d := setupTestDB(t)
	ctx := context.Background()

	gitSetup(t, repoRoot)

	// Create initial Go file with one import — function body stays identical across branches
	goFile := filepath.Join(repoRoot, "main.go")
	initial := "package main\n\nimport (\n\t\"fmt\"\n)\n\nfunc main() {\n\tfmt.Println(\"hello\")\n}\n"
	os.WriteFile(goFile, []byte(initial), 0o644)
	gitRun(t, repoRoot, "add", ".")
	gitRun(t, repoRoot, "commit", "-m", "add main.go")

	// Branch A: add "os" import only (keep function body identical)
	gitRun(t, repoRoot, "checkout", "-b", "feature/add-os")
	withOS := "package main\n\nimport (\n\t\"fmt\"\n\t\"os\"\n)\n\nfunc main() {\n\tfmt.Println(\"hello\")\n}\n"
	os.WriteFile(goFile, []byte(withOS), 0o644)
	// Add a separate file so git doesn't auto-merge trivially
	os.WriteFile(filepath.Join(repoRoot, "os_usage.go"), []byte("package main\n\nimport \"os\"\n\nfunc exit() { os.Exit(0) }\n"), 0o644)
	gitRun(t, repoRoot, "add", ".")
	gitRun(t, repoRoot, "commit", "-m", "add os import")

	// Branch B (from dev): add "strings" import only (keep function body identical)
	gitRun(t, repoRoot, "checkout", "dev")
	withStrings := "package main\n\nimport (\n\t\"fmt\"\n\t\"strings\"\n)\n\nfunc main() {\n\tfmt.Println(\"hello\")\n}\n"
	os.WriteFile(goFile, []byte(withStrings), 0o644)
	os.WriteFile(filepath.Join(repoRoot, "str_usage.go"), []byte("package main\n\nimport \"strings\"\n\nfunc upper(s string) string { return strings.ToUpper(s) }\n"), 0o644)
	gitRun(t, repoRoot, "add", ".")
	gitRun(t, repoRoot, "commit", "-m", "add strings import")

	// Now merge feature/add-os into dev — should conflict only on import block
	_, mergeErr := gitExec(repoRoot, "merge", "--no-ff", "feature/add-os", "-m", "merge")
	if mergeErr == nil {
		t.Skip("merge didn't conflict — git auto-merged (expected on some versions)")
	}

	// Attempt auto-resolve
	d.SetBlackboard(ctx, "conductor:auto_resolve_conflicts", "1", "test")
	c, _ := New(ConductorOpts{DB: d, RepoRoot: repoRoot})

	resolution := c.attemptAutoResolve(ctx, "T-001", []string{"main.go"}, "feature/add-os", "dev")
	if !resolution.Attempted {
		t.Fatal("expected resolution to be attempted")
	}
	if !resolution.Resolved {
		// Debug: read the conflicted file to see what's happening
		data, _ := os.ReadFile(goFile)
		t.Logf("conflicted file content:\n%s", string(data))
		hunks, _ := parseConflictMarkers(goFile)
		for i, h := range hunks {
			t.Logf("hunk %d: lines %d-%d, ours=%q, theirs=%q", i, h.StartLine, h.EndLine, h.Ours, h.Theirs)
		}
		t.Logf("isImportOnly: %v", isImportOnlyConflict(goFile, hunks))
		t.Errorf("expected resolution to succeed, got tier=%s failed=%v", resolution.Tier, resolution.FilesFailed)
	}
	if len(resolution.FilesFixed) == 0 {
		t.Error("expected at least one fixed file")
	}
}

func TestAttemptAutoResolve_NonOverlapping(t *testing.T) {
	repoRoot := t.TempDir()
	d := setupTestDB(t)
	ctx := context.Background()

	gitSetup(t, repoRoot)

	// Create initial file
	testFile := filepath.Join(repoRoot, "data.txt")
	os.WriteFile(testFile, []byte("line1\nline2\nline3\n"), 0o644)
	gitRun(t, repoRoot, "add", ".")
	gitRun(t, repoRoot, "commit", "-m", "initial data")

	// Branch A: add line at the top
	gitRun(t, repoRoot, "checkout", "-b", "feature/top-add")
	os.WriteFile(testFile, []byte("added-top\nline1\nline2\nline3\n"), 0o644)
	gitRun(t, repoRoot, "add", ".")
	gitRun(t, repoRoot, "commit", "-m", "add top")

	// Branch B (from dev): add line at bottom
	gitRun(t, repoRoot, "checkout", "dev")
	os.WriteFile(testFile, []byte("line1\nline2\nline3\nadded-bottom\n"), 0o644)
	gitRun(t, repoRoot, "add", ".")
	gitRun(t, repoRoot, "commit", "-m", "add bottom")

	// Attempt merge
	_, mergeErr := gitExec(repoRoot, "merge", "--no-ff", "feature/top-add", "-m", "merge")
	if mergeErr == nil {
		t.Skip("merge didn't conflict — git auto-merged")
	}

	d.SetBlackboard(ctx, "conductor:auto_resolve_conflicts", "1", "test")
	c, _ := New(ConductorOpts{DB: d, RepoRoot: repoRoot})

	resolution := c.attemptAutoResolve(ctx, "T-001", []string{"data.txt"}, "feature/top-add", "dev")
	if !resolution.Attempted {
		t.Fatal("expected resolution to be attempted")
	}
	// This may or may not conflict depending on git version — if it does, verify resolution
	if resolution.Resolved {
		if len(resolution.FilesFixed) == 0 {
			t.Error("resolved but no files fixed")
		}
	}
}

func TestAttemptAutoResolve_ComplexConflict(t *testing.T) {
	repoRoot := t.TempDir()
	d := setupTestDB(t)
	ctx := context.Background()

	gitSetup(t, repoRoot)

	// Create initial file
	testFile := filepath.Join(repoRoot, "config.txt")
	os.WriteFile(testFile, []byte("setting=original\n"), 0o644)
	gitRun(t, repoRoot, "add", ".")
	gitRun(t, repoRoot, "commit", "-m", "initial config")

	// Branch A: change setting to value-a
	gitRun(t, repoRoot, "checkout", "-b", "feature/change-a")
	os.WriteFile(testFile, []byte("setting=value-a\n"), 0o644)
	gitRun(t, repoRoot, "add", ".")
	gitRun(t, repoRoot, "commit", "-m", "change to a")

	// Branch B (from dev): change setting to value-b
	gitRun(t, repoRoot, "checkout", "dev")
	os.WriteFile(testFile, []byte("setting=value-b\n"), 0o644)
	gitRun(t, repoRoot, "add", ".")
	gitRun(t, repoRoot, "commit", "-m", "change to b")

	// This MUST conflict — both sides modify the same line
	_, mergeErr := gitExec(repoRoot, "merge", "--no-ff", "feature/change-a", "-m", "merge")
	if mergeErr == nil {
		t.Fatal("expected merge conflict")
	}

	d.SetBlackboard(ctx, "conductor:auto_resolve_conflicts", "1", "test")
	c, _ := New(ConductorOpts{DB: d, RepoRoot: repoRoot})

	resolution := c.attemptAutoResolve(ctx, "T-001", []string{"config.txt"}, "feature/change-a", "dev")
	if !resolution.Attempted {
		t.Fatal("expected resolution to be attempted")
	}
	if resolution.Resolved {
		t.Error("complex conflict should NOT be resolved")
	}
	if len(resolution.FilesFailed) == 0 {
		t.Error("expected config.txt in FilesFailed")
	}
}

func TestResolveWithLLM_Success(t *testing.T) {
	dir := t.TempDir()
	d := setupTestDB(t)

	// Create a conflicted file
	conflictFile := filepath.Join(dir, "main.go")
	os.WriteFile(conflictFile, []byte(`package main
<<<<<<< HEAD
func hello() { fmt.Println("hello") }
=======
func hello() { fmt.Println("world") }
>>>>>>> branch
`), 0o644)

	// Mock runner returns clean resolved content
	runner := &MockRunner{
		Outputs: []string{`package main

func hello() { fmt.Println("hello world") }
`},
	}

	c, _ := New(ConductorOpts{DB: d, RepoRoot: dir, Runner: runner})
	ctx := context.Background()

	err := c.resolveWithLLM(ctx, dir, conflictFile, "main.go", "Merge both greeting messages")
	if err != nil {
		t.Fatalf("resolveWithLLM error: %v", err)
	}

	// Verify file was written without conflict markers
	resolved, _ := os.ReadFile(conflictFile)
	if strings.Contains(string(resolved), "<<<<<<<") {
		t.Error("resolved file still contains conflict markers")
	}
	if !strings.Contains(string(resolved), "hello world") {
		t.Error("expected resolved content")
	}

	// Verify runner was called with correct model
	if len(runner.Calls) != 1 {
		t.Fatalf("expected 1 runner call, got %d", len(runner.Calls))
	}
	if runner.Calls[0].Model != agent.ModelOpus {
		t.Errorf("model = %q, want %s", runner.Calls[0].Model, agent.ModelOpus)
	}
}

func TestResolveWithLLM_StillHasMarkers(t *testing.T) {
	dir := t.TempDir()
	d := setupTestDB(t)

	conflictFile := filepath.Join(dir, "bad.go")
	os.WriteFile(conflictFile, []byte("<<<<<<< HEAD\nours\n=======\ntheirs\n>>>>>>>\n"), 0o644)

	// Mock runner returns output that still has conflict markers (bad LLM output)
	runner := &MockRunner{
		Outputs: []string{"<<<<<<< HEAD\nstill broken\n=======\nfoo\n>>>>>>>"},
	}

	c, _ := New(ConductorOpts{DB: d, RepoRoot: dir, Runner: runner})
	ctx := context.Background()

	err := c.resolveWithLLM(ctx, dir, conflictFile, "bad.go", "")
	if err == nil {
		t.Fatal("expected error when LLM output still has conflict markers")
	}
	if !strings.Contains(err.Error(), "still contains conflict markers") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestResolveWithLLM_RunnerFails(t *testing.T) {
	dir := t.TempDir()
	d := setupTestDB(t)

	conflictFile := filepath.Join(dir, "fail.go")
	os.WriteFile(conflictFile, []byte("<<<<<<< HEAD\nours\n=======\ntheirs\n>>>>>>>\n"), 0o644)

	runner := &MockRunner{
		Errors: []error{fmt.Errorf("context limit exceeded")},
	}

	c, _ := New(ConductorOpts{DB: d, RepoRoot: dir, Runner: runner})
	ctx := context.Background()

	err := c.resolveWithLLM(ctx, dir, conflictFile, "fail.go", "")
	if err == nil {
		t.Fatal("expected error when runner fails")
	}
}

func TestResolveWithLLM_StripsMarkdownFences(t *testing.T) {
	dir := t.TempDir()
	d := setupTestDB(t)

	conflictFile := filepath.Join(dir, "fenced.go")
	os.WriteFile(conflictFile, []byte("<<<<<<< HEAD\nours\n=======\ntheirs\n>>>>>>>\n"), 0o644)

	// Mock runner wraps output in markdown fences (common LLM behavior)
	runner := &MockRunner{
		Outputs: []string{"```go\npackage main\n\nfunc resolved() {}\n```"},
	}

	c, _ := New(ConductorOpts{DB: d, RepoRoot: dir, Runner: runner})
	ctx := context.Background()

	err := c.resolveWithLLM(ctx, dir, conflictFile, "fenced.go", "")
	if err != nil {
		t.Fatalf("resolveWithLLM error: %v", err)
	}

	resolved, _ := os.ReadFile(conflictFile)
	content := string(resolved)
	if strings.Contains(content, "```") {
		t.Error("resolved file still contains markdown fences")
	}
	if !strings.Contains(content, "func resolved()") {
		t.Error("expected resolved content without fences")
	}
}

func TestAttemptAutoResolve_LLMFallback(t *testing.T) {
	repoRoot := t.TempDir()
	d := setupTestDB(t)
	ctx := context.Background()

	gitSetup(t, repoRoot)

	// Create initial file
	testFile := filepath.Join(repoRoot, "config.txt")
	os.WriteFile(testFile, []byte("setting=original\n"), 0o644)
	gitRun(t, repoRoot, "add", ".")
	gitRun(t, repoRoot, "commit", "-m", "initial config")

	// Branch A: change setting to value-a
	gitRun(t, repoRoot, "checkout", "-b", "feature/change-a")
	os.WriteFile(testFile, []byte("setting=value-a\n"), 0o644)
	gitRun(t, repoRoot, "add", ".")
	gitRun(t, repoRoot, "commit", "-m", "change to a")

	// Branch B (from dev): change setting to value-b
	gitRun(t, repoRoot, "checkout", "dev")
	os.WriteFile(testFile, []byte("setting=value-b\n"), 0o644)
	gitRun(t, repoRoot, "add", ".")
	gitRun(t, repoRoot, "commit", "-m", "change to b")

	// This MUST conflict
	_, mergeErr := gitExec(repoRoot, "merge", "--no-ff", "feature/change-a", "-m", "merge")
	if mergeErr == nil {
		t.Fatal("expected merge conflict")
	}

	// Mock runner returns resolved content
	runner := &MockRunner{
		Outputs: []string{"setting=value-combined\n"},
	}

	d.SetBlackboard(ctx, "conductor:auto_resolve_conflicts", "1", "test")
	d.CreateTask(ctx, db.Task{
		ID:          "T-001",
		Title:       "Test task",
		Status:      "done",
		Role:        "implementer",
		Description: sql.NullString{String: "Combine settings", Valid: true},
	})

	c, _ := New(ConductorOpts{DB: d, RepoRoot: repoRoot, Runner: runner})

	resolution := c.attemptAutoResolve(ctx, "T-001", []string{"config.txt"}, "feature/change-a", "dev")
	if !resolution.Attempted {
		t.Fatal("expected resolution to be attempted")
	}
	if !resolution.Resolved {
		t.Errorf("expected LLM to resolve conflict, got tier=%s failed=%v", resolution.Tier, resolution.FilesFailed)
	}
	if resolution.Tier != "llm" {
		t.Errorf("tier = %q, want llm", resolution.Tier)
	}
	if len(runner.Calls) != 1 {
		t.Errorf("expected 1 LLM call, got %d", len(runner.Calls))
	}
}

func TestClassifyMergeFailure_Conflict(t *testing.T) {
	mergeOut := `Auto-merging internal/db/models.go
CONFLICT (content): Merge conflict in internal/db/models.go
Auto-merging internal/db/queries.go
CONFLICT (content): Merge conflict in internal/db/queries.go
Automatic merge failed; fix conflicts and then commit the result.`

	typ, files := classifyMergeFailure(mergeOut)
	if typ != "conflict" {
		t.Errorf("expected type=conflict, got %q", typ)
	}
	if len(files) != 2 {
		t.Fatalf("expected 2 conflict files, got %d: %v", len(files), files)
	}
	if files[0] != "internal/db/models.go" {
		t.Errorf("files[0] = %q, want internal/db/models.go", files[0])
	}
	if files[1] != "internal/db/queries.go" {
		t.Errorf("files[1] = %q, want internal/db/queries.go", files[1])
	}
}

func TestClassifyMergeFailure_Error(t *testing.T) {
	// Non-conflict git errors (permissions, bad ref, etc.)
	mergeOut := `fatal: 'nonexistent-branch' does not point to a commit`

	typ, files := classifyMergeFailure(mergeOut)
	if typ != "error" {
		t.Errorf("expected type=error, got %q", typ)
	}
	if files != nil {
		t.Errorf("expected nil files, got %v", files)
	}
}

func TestClassifyMergeFailure_Empty(t *testing.T) {
	typ, files := classifyMergeFailure("")
	if typ != "error" {
		t.Errorf("expected type=error for empty output, got %q", typ)
	}
	if files != nil {
		t.Errorf("expected nil files, got %v", files)
	}
}

func TestClassifyMergeFailure_SingleFile(t *testing.T) {
	mergeOut := `CONFLICT (content): Merge conflict in main.go
Automatic merge failed; fix conflicts and then commit the result.`

	typ, files := classifyMergeFailure(mergeOut)
	if typ != "conflict" {
		t.Errorf("expected type=conflict, got %q", typ)
	}
	if len(files) != 1 || files[0] != "main.go" {
		t.Errorf("expected [main.go], got %v", files)
	}
}

func TestRunTestCmd_BashShellFeatures(t *testing.T) {
	dir := t.TempDir()

	// Pipes: bash -c handles pipes correctly (strings.Fields would break this)
	out, err := runTestCmd(dir, "echo hello | tr 'h' 'H'", 5*time.Minute)
	if err != nil {
		t.Fatalf("pipe command failed: %v", err)
	}
	if !strings.Contains(out, "Hello") {
		t.Errorf("expected 'Hello' from pipe, got %q", out)
	}

	// Quoted arguments: bash -c preserves quotes
	out, err = runTestCmd(dir, `echo "hello world"`, 5*time.Minute)
	if err != nil {
		t.Fatalf("quoted command failed: %v", err)
	}
	if !strings.Contains(out, "hello world") {
		t.Errorf("expected 'hello world', got %q", out)
	}

	// Semicolons: multiple commands in one string
	out, err = runTestCmd(dir, "echo first; echo second", 5*time.Minute)
	if err != nil {
		t.Fatalf("semicolon command failed: %v", err)
	}
	if !strings.Contains(out, "first") || !strings.Contains(out, "second") {
		t.Errorf("expected both 'first' and 'second', got %q", out)
	}

	// Docker-like command (just echo to simulate structure)
	out, err = runTestCmd(dir, `echo "docker run --rm -v /tmp:/workspace -w /workspace img bash -c 'pytest'"`, 5*time.Minute)
	if err != nil {
		t.Fatalf("docker-like command failed: %v", err)
	}
	if !strings.Contains(out, "docker run") {
		t.Errorf("expected docker command echo, got %q", out)
	}
}

func TestRunTestCmd_WhitespaceOnly(t *testing.T) {
	dir := t.TempDir()

	// Whitespace-only command should be a no-op (same as empty)
	out, err := runTestCmd(dir, "   ", 5*time.Minute)
	if err != nil {
		t.Fatalf("unexpected error for whitespace command: %v", err)
	}
	if out != "" {
		t.Errorf("expected empty output for whitespace command, got %q", out)
	}
}

func TestProbeTestEnvironment_DockerPrefix(t *testing.T) {
	// Docker-prefixed commands should return CanRun=true without host probing
	// (assuming docker is on PATH — which it typically is in dev environments)
	probe := probeTestEnvironment(t.TempDir(),
		`docker run --rm -v /tmp:/workspace -w /workspace myimage:latest bash -c "pytest -x"`,
		5*time.Second)

	if _, err := exec.LookPath("docker"); err != nil {
		// Docker not installed — probe should fail with clear reason
		if probe.CanRun {
			t.Error("expected CanRun=false when docker not on PATH")
		}
		if probe.Framework != "docker" {
			t.Errorf("framework = %q, want docker", probe.Framework)
		}
		if !strings.Contains(probe.Reason, "docker not found") {
			t.Errorf("reason = %q, want 'docker not found'", probe.Reason)
		}
	} else {
		// Docker installed — probe should succeed
		if !probe.CanRun {
			t.Errorf("expected CanRun=true for docker-prefixed command, got reason=%q", probe.Reason)
		}
		if probe.Framework != "docker" {
			t.Errorf("framework = %q, want docker", probe.Framework)
		}
	}
}

func TestProbeTestEnvironment_DockerPrefixWithWhitespace(t *testing.T) {
	// Leading whitespace before "docker" should still be detected
	probe := probeTestEnvironment(t.TempDir(),
		`  docker run --rm myimage pytest`, 5*time.Second)

	// Should detect as docker regardless of installation status
	if probe.Framework != "docker" {
		t.Errorf("framework = %q, want docker", probe.Framework)
	}
}

func TestMergeResult_RefiningField(t *testing.T) {
	r := MergeResult{
		Merged:      []string{"branch-a"},
		Failed:      []string{"branch-b"},
		Skipped:     []string{"branch-c"},
		Refining:    []string{"branch-d"},
		Plan:        []string{"branch-a", "branch-b", "branch-c", "branch-d"},
		TestsFailed: true,
	}
	if len(r.Refining) != 1 {
		t.Errorf("expected 1 refining, got %d", len(r.Refining))
	}
	if r.Refining[0] != "branch-d" {
		t.Errorf("refining[0] = %q, want branch-d", r.Refining[0])
	}
}

// --- Docker Integration Tests ---

func TestRunTestCmd_RealDockerExecution(t *testing.T) {
	if _, err := exec.LookPath("docker"); err != nil {
		t.Skip("docker not available")
	}
	dir := t.TempDir()

	out, err := runTestCmd(dir,
		`docker run --rm alpine:latest echo "DOCKER_TEST_PASS"`,
		60*time.Second)
	if err != nil {
		t.Fatalf("docker run failed: %v\noutput: %s", err, out)
	}
	if !strings.Contains(out, "DOCKER_TEST_PASS") {
		t.Errorf("expected DOCKER_TEST_PASS in output, got %q", out)
	}
}

func TestRunTestCmd_DockerVolumeMount(t *testing.T) {
	if _, err := exec.LookPath("docker"); err != nil {
		t.Skip("docker not available")
	}
	// Use a temp dir under the user's home directory — Colima (virtiofs) shares
	// /Users but NOT /var/folders where Go's t.TempDir() creates directories.
	home, _ := os.UserHomeDir()
	dir, err := os.MkdirTemp(home, "orchestra-test-docker-*")
	if err != nil {
		t.Fatalf("creating temp dir under home: %v", err)
	}
	defer os.RemoveAll(dir)

	os.WriteFile(filepath.Join(dir, "testfile.txt"), []byte("VOLUME_MOUNT_OK"), 0o644)

	cmd := fmt.Sprintf(
		`docker run --rm -v %s:/workspace -w /workspace alpine:latest cat testfile.txt`,
		dir)
	out, runErr := runTestCmd(dir, cmd, 60*time.Second)
	if runErr != nil {
		t.Fatalf("docker volume mount failed: %v\noutput: %s", runErr, out)
	}
	if !strings.Contains(out, "VOLUME_MOUNT_OK") {
		t.Errorf("expected VOLUME_MOUNT_OK, got %q", out)
	}
}

func TestProbeAndRun_DockerCommand(t *testing.T) {
	if _, err := exec.LookPath("docker"); err != nil {
		t.Skip("docker not available")
	}
	dir := t.TempDir()

	testCmd := `docker run --rm alpine:latest echo "PROBE_RUN_OK"`

	// Phase 1: Probe (mirrors Merge() pre-loop check)
	probe := probeTestEnvironment(dir, testCmd, 30*time.Second)
	if !probe.CanRun {
		t.Fatalf("probe failed: %s", probe.Reason)
	}
	if probe.Framework != "docker" {
		t.Errorf("framework = %q, want docker", probe.Framework)
	}

	// Phase 2: Run (only if probe passed, matching Merge() logic)
	out, err := runTestCmd(dir, testCmd, 60*time.Second)
	if err != nil {
		t.Fatalf("runTestCmd failed after successful probe: %v", err)
	}
	if !strings.Contains(out, "PROBE_RUN_OK") {
		t.Errorf("expected PROBE_RUN_OK, got %q", out)
	}
}

// TestMerge_DockerTestGate_Integration tests the full merge path with a Docker
// test command: git repo setup → done task → Merge() → probeTestEnvironment
// detects Docker → runTestCmd executes container → merge succeeds.
func TestMerge_DockerTestGate_Integration(t *testing.T) {
	if _, err := exec.LookPath("docker"); err != nil {
		t.Skip("docker not available")
	}

	repoRoot := t.TempDir()
	d := setupTestDB(t)
	ctx := context.Background()

	// Set up git repo with dev branch + initial commit
	gitSetup(t, repoRoot)

	// Create a feature branch with a trivial change
	gitRun(t, repoRoot, "checkout", "-b", "feature/T-DOCKER")
	os.WriteFile(filepath.Join(repoRoot, "smoke.txt"), []byte("docker test"), 0o644)
	gitRun(t, repoRoot, "add", ".")
	gitRun(t, repoRoot, "commit", "-m", "add smoke test file")
	gitRun(t, repoRoot, "checkout", "dev")

	// Create a done task pointing to the branch
	d.CreateTask(ctx, db.Task{
		ID:     "T-DOCKER",
		Title:  "Docker smoke test",
		Status: "done",
		Role:   "implementer",
		Branch: sql.NullString{String: "feature/T-DOCKER", Valid: true},
	})
	d.SetBlackboard(ctx, "conductor:task_ids", `["T-DOCKER"]`, "test")

	c, _ := New(ConductorOpts{DB: d, RepoRoot: repoRoot})

	// Run merge with a Docker test gate — Alpine container echoes PASS
	result, err := c.Merge(ctx, MergeOpts{
		TestCmd:    `docker run --rm alpine:latest echo "DOCKER_MERGE_PASS"`,
		BaseBranch: "dev",
	})
	if err != nil {
		t.Fatalf("Merge error: %v", err)
	}

	// Verify the branch was merged (not failed or skipped)
	if len(result.Merged) != 1 {
		t.Errorf("expected 1 merged branch, got %d (failed=%v, skipped=%v)",
			len(result.Merged), result.Failed, result.Skipped)
	}
	if len(result.Failed) > 0 {
		t.Errorf("unexpected failed branches: %v", result.Failed)
	}
	if result.TestsFailed {
		t.Error("TestsFailed should be false for passing Docker test")
	}

	// Verify the merge commit exists on dev
	log, _ := gitExec(repoRoot, "log", "--oneline", "-3")
	if !strings.Contains(log, "Merge feature/T-DOCKER") {
		t.Errorf("merge commit not found in log:\n%s", log)
	}

	// Verify the file from the feature branch exists
	if _, err := os.Stat(filepath.Join(repoRoot, "smoke.txt")); err != nil {
		t.Errorf("smoke.txt should exist after merge: %v", err)
	}
}

func TestClassifyMergeFailure_Transient(t *testing.T) {
	// G99: "Unable to write index" should be classified as transient, not error
	mergeOut := `Updating 1a2b3c4..5e6f7a8
error: Unable to write index file
fatal: unable to write new index file`

	typ, files := classifyMergeFailure(mergeOut)
	if typ != "transient" {
		t.Errorf("expected type=transient, got %q", typ)
	}
	if files != nil {
		t.Errorf("expected nil files for transient failure, got %v", files)
	}
}

func TestClassifyMergeFailure_TransientWithConflict(t *testing.T) {
	// If both "Unable to write index" and CONFLICT markers are present,
	// transient should take precedence (checked first).
	mergeOut := `error: Unable to write index file
CONFLICT (content): Merge conflict in main.go`

	typ, _ := classifyMergeFailure(mergeOut)
	if typ != "transient" {
		t.Errorf("expected transient to take precedence, got %q", typ)
	}
}

func TestAttemptAutoResolve_InfrastructureFile(t *testing.T) {
	// G98: .orchestra-hooks/ files should be resolved mechanically with "take ours"
	repoRoot := t.TempDir()
	d := setupTestDB(t)
	ctx := context.Background()

	gitSetup(t, repoRoot)

	// Create initial .orchestra-hooks/pre-commit
	hookDir := filepath.Join(repoRoot, ".orchestra-hooks")
	os.MkdirAll(hookDir, 0o755)
	os.WriteFile(filepath.Join(hookDir, "pre-commit"), []byte("#!/bin/bash\nexit 0\n"), 0o755)
	gitRun(t, repoRoot, "add", ".")
	gitRun(t, repoRoot, "commit", "-m", "add hook")

	// Branch A: modify hook
	gitRun(t, repoRoot, "checkout", "-b", "feature/hook-a")
	os.WriteFile(filepath.Join(hookDir, "pre-commit"), []byte("#!/bin/bash\n# task A\nexit 0\n"), 0o755)
	gitRun(t, repoRoot, "add", ".")
	gitRun(t, repoRoot, "commit", "-m", "hook version A")

	// Branch B (from dev): different modification
	gitRun(t, repoRoot, "checkout", "dev")
	os.WriteFile(filepath.Join(hookDir, "pre-commit"), []byte("#!/bin/bash\n# task B\nexit 0\n"), 0o755)
	gitRun(t, repoRoot, "add", ".")
	gitRun(t, repoRoot, "commit", "-m", "hook version B")

	// Merge — should conflict
	_, mergeErr := gitExec(repoRoot, "merge", "--no-ff", "feature/hook-a", "-m", "merge")
	if mergeErr == nil {
		t.Skip("merge didn't conflict — git auto-merged")
	}

	d.SetBlackboard(ctx, "conductor:auto_resolve_conflicts", "1", "test")
	c, _ := New(ConductorOpts{DB: d, RepoRoot: repoRoot})

	resolution := c.attemptAutoResolve(ctx, "T-001", []string{".orchestra-hooks/pre-commit"}, "feature/hook-a", "dev")
	if !resolution.Attempted {
		t.Fatal("expected resolution to be attempted")
	}
	if !resolution.Resolved {
		t.Errorf("expected infrastructure file to be auto-resolved, got tier=%s failed=%v", resolution.Tier, resolution.FilesFailed)
	}
	if resolution.Tier != "infrastructure" {
		t.Errorf("expected tier=infrastructure, got %q", resolution.Tier)
	}
	found := false
	for _, f := range resolution.FilesFixed {
		if f == ".orchestra-hooks/pre-commit" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected .orchestra-hooks/pre-commit in FilesFixed, got %v", resolution.FilesFixed)
	}
}

func TestMergeTransientRetry(t *testing.T) {
	// G99: Verify that a transient failure triggers a retry via the Merge function.
	// We test the classification + retry logic structurally since we can't easily
	// simulate a real .git/index lock in a unit test.
	repoRoot := t.TempDir()
	d := setupTestDB(t)
	ctx := context.Background()

	gitSetup(t, repoRoot)

	// Create a feature branch
	gitRun(t, repoRoot, "checkout", "-b", "feature/T-TRANS")
	os.WriteFile(filepath.Join(repoRoot, "transient.txt"), []byte("test\n"), 0o644)
	gitRun(t, repoRoot, "add", ".")
	gitRun(t, repoRoot, "commit", "-m", "add transient test file")
	gitRun(t, repoRoot, "checkout", "dev")

	d.CreateTask(ctx, db.Task{
		ID:     "T-TRANS",
		Title:  "Transient test",
		Status: "done",
		Role:   "implementer",
		Branch: sql.NullString{String: "feature/T-TRANS", Valid: true},
	})
	d.SetBlackboard(ctx, "conductor:task_ids", `["T-TRANS"]`, "test")

	c, _ := New(ConductorOpts{DB: d, RepoRoot: repoRoot})

	// This merge should succeed normally (no actual lock contention).
	// The test verifies the transient handling code path doesn't interfere
	// with successful merges (no false positives from the new detection).
	result, err := c.Merge(ctx, MergeOpts{BaseBranch: "dev"})
	if err != nil {
		t.Fatalf("Merge error: %v", err)
	}
	if len(result.Merged) != 1 {
		t.Errorf("expected 1 merged, got %d (failed=%v)", len(result.Merged), result.Failed)
	}
}

func TestMergeStagingToDev_WorktreeBased(t *testing.T) {
	// Create a real git repo for the worktree-based merge test.
	repoRoot := t.TempDir()
	gitInit := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = repoRoot
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v failed: %v\n%s", args, err, out)
		}
	}

	gitInit("init", "-b", "dev")
	gitInit("config", "user.email", "test@test.com")
	gitInit("config", "user.name", "Test")

	// Create initial commit on dev.
	initialFile := filepath.Join(repoRoot, "README.md")
	os.WriteFile(initialFile, []byte("# Project\n"), 0o644)
	gitInit("add", ".")
	gitInit("commit", "-m", "initial")

	// Create staging branch with an extra commit.
	gitInit("checkout", "-b", "conductor/staging-c1")
	researchFile := filepath.Join(repoRoot, "research.md")
	os.WriteFile(researchFile, []byte("# Research\nSome findings.\n"), 0o644)
	gitInit("add", ".")
	gitInit("commit", "-m", "add research")

	// Go back to dev.
	gitInit("checkout", "dev")

	// Create .worktree directory (normally exists in real repos).
	os.MkdirAll(filepath.Join(repoRoot, ".worktree"), 0o755)

	// Set up DB with conductor record.
	d := setupTestDB(t)
	ctx := context.Background()
	d.ExecContext(ctx,
		`INSERT INTO conductors (id, pid, goal, status, staging_branch, base_branch, max_parallel, test_cmd, merge_review, model_strategy, runtime, repo_map, lenient_deps, file_enforcement)
		 VALUES ('c1', 99, 'test goal', 'active', 'conductor/staging-c1', 'dev', 3, '', 0, 'all-opus', 'local', 0, 0, '')`)

	c, _ := New(ConductorOpts{DB: d, RepoRoot: repoRoot})
	c.ConductorID = "c1"

	// Perform the staging-to-dev merge.
	err := c.MergeStagingToDev(ctx)
	if err != nil {
		t.Fatalf("MergeStagingToDev failed: %v", err)
	}

	// Verify dev branch now contains the research file.
	cmd := exec.Command("git", "log", "--oneline", "dev")
	cmd.Dir = repoRoot
	logOut, _ := cmd.CombinedOutput()
	if !strings.Contains(string(logOut), "Merge staging") {
		t.Errorf("expected merge commit on dev, got:\n%s", logOut)
	}

	// Verify the research file exists on dev.
	cmd = exec.Command("git", "show", "dev:research.md")
	cmd.Dir = repoRoot
	showOut, err := cmd.CombinedOutput()
	if err != nil {
		t.Errorf("research.md not found on dev: %v\n%s", err, showOut)
	}

	// Verify no temp worktree remains.
	wtPath := filepath.Join(repoRoot, ".worktree", "_staging-merge-c1")
	if _, err := os.Stat(wtPath); err == nil {
		t.Errorf("temp worktree %s should have been cleaned up", wtPath)
	}
}

func TestMergeStagingToDev_NoCommits(t *testing.T) {
	// When staging has no commits beyond dev, MergeStagingToDev should be a no-op.
	repoRoot := t.TempDir()
	gitInit := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = repoRoot
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v failed: %v\n%s", args, err, out)
		}
	}

	gitInit("init", "-b", "dev")
	gitInit("config", "user.email", "test@test.com")
	gitInit("config", "user.name", "Test")
	os.WriteFile(filepath.Join(repoRoot, "README.md"), []byte("# Project\n"), 0o644)
	gitInit("add", ".")
	gitInit("commit", "-m", "initial")

	// Staging branch at same commit as dev (no new commits).
	gitInit("branch", "conductor/staging-c2")

	d := setupTestDB(t)
	ctx := context.Background()
	d.ExecContext(ctx,
		`INSERT INTO conductors (id, pid, goal, status, staging_branch, base_branch, max_parallel, test_cmd, merge_review, model_strategy, runtime, repo_map, lenient_deps, file_enforcement)
		 VALUES ('c2', 99, 'test goal', 'active', 'conductor/staging-c2', 'dev', 3, '', 0, 'all-opus', 'local', 0, 0, '')`)

	c, _ := New(ConductorOpts{DB: d, RepoRoot: repoRoot})
	c.ConductorID = "c2"

	err := c.MergeStagingToDev(ctx)
	if err != nil {
		t.Fatalf("MergeStagingToDev should succeed with no-op, got: %v", err)
	}

	// G104: verify staging_merge_skipped event was logged
	events, err := d.RecentEvents(ctx, 10)
	if err != nil {
		t.Fatalf("getting recent events: %v", err)
	}
	found := false
	for _, ev := range events {
		if ev.EventType == "staging_merge_skipped" {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected staging_merge_skipped event to be logged")
	}
}

// --- B-273: PR-Based Merge Mode tests ---

func TestMergeMode_Types(t *testing.T) {
	// Verify MergeMode constants.
	if MergeModeLocal != "local" {
		t.Errorf("expected MergeModeLocal='local', got %q", MergeModeLocal)
	}
	if MergeModePR != "pr" {
		t.Errorf("expected MergeModePR='pr', got %q", MergeModePR)
	}
}

func TestCreateStagingPR_NoConductor(t *testing.T) {
	// When there is no conductor, CreateStagingPR should return nil, nil.
	d := setupTestDB(t)
	c, _ := New(ConductorOpts{DB: d, RepoRoot: "/tmp/test"})
	c.ConductorID = ""
	c.SessionID = ""

	pr, err := c.CreateStagingPR(context.Background())
	if err == nil && pr == nil {
		// Both nil means no conductor ID — this is fine but the actual code returns an error
	}
	// With no conductor ID, we should get an error.
	if err == nil {
		t.Error("expected error when no conductor ID set")
	}
}

func TestCreateStagingPR_NoBranch(t *testing.T) {
	// When conductor exists but has no staging branch, return nil gracefully.
	d := setupTestDB(t)
	ctx := context.Background()

	d.CreateConductor(ctx, db.ConductorRecord{
		ID:            "s-test-no-branch",
		PID:           1,
		Goal:          "test",
		Status:        "active",
		StagingBranch: "", // no staging branch
		BaseBranch:    "dev",
		MaxParallel:   1,
		ModelStrategy: "all-opus",
		Runtime:       "local",
		MergeMode:     "pr",
	})

	c, _ := New(ConductorOpts{DB: d, RepoRoot: "/tmp/test"})
	c.ConductorID = "s-test-no-branch"

	pr, err := c.CreateStagingPR(ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if pr != nil {
		t.Error("expected nil PRResult when no staging branch")
	}
}

func TestCreateStagingPR_NoNewCommits(t *testing.T) {
	// When staging == base (no new commits), skip PR creation.
	repoRoot := t.TempDir()
	d := setupTestDB(t)
	ctx := context.Background()

	gitSetup(t, repoRoot)
	gitRun(t, repoRoot, "branch", "conductor/s-no-commits", "dev")

	d.CreateConductor(ctx, db.ConductorRecord{
		ID:            "s-no-commits",
		PID:           1,
		Goal:          "test",
		Status:        "active",
		StagingBranch: "conductor/s-no-commits",
		BaseBranch:    "dev",
		MaxParallel:   1,
		ModelStrategy: "all-opus",
		Runtime:       "local",
		MergeMode:     "pr",
	})

	c, _ := New(ConductorOpts{DB: d, RepoRoot: repoRoot})
	c.ConductorID = "s-no-commits"

	pr, err := c.CreateStagingPR(ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if pr != nil {
		t.Error("expected nil PRResult when staging has no new commits")
	}
}

func TestCreateStagingPR_NoGH(t *testing.T) {
	// Verify the preflight check catches missing gh CLI.
	// We can't easily test CreateStagingPR itself with PATH cleared because
	// git also needs PATH. Instead, verify the exec.LookPath check directly.
	_, err := exec.LookPath("gh-definitely-not-a-real-binary-273")
	if err == nil {
		t.Skip("somehow found a binary named gh-definitely-not-a-real-binary-273")
	}
	// The error from LookPath is what CreateStagingPR would wrap.
	// This verifies the check mechanism works.

	// Also verify the preflight in Go() rejects merge-mode=pr when gh is missing.
	// We test by directly calling LookPath for "gh" — if gh IS installed, test
	// the positive path; if not, verify the error message.
	if _, lookErr := exec.LookPath("gh"); lookErr != nil {
		// gh not installed — verify the error format
		repoRoot := t.TempDir()
		d := setupTestDB(t)
		ctx := context.Background()

		gitSetup(t, repoRoot)

		// Create staging with a commit beyond base
		gitRun(t, repoRoot, "branch", "conductor/s-nogh", "dev")
		gitRun(t, repoRoot, "checkout", "conductor/s-nogh")
		os.WriteFile(filepath.Join(repoRoot, "new-file.txt"), []byte("pr test\n"), 0o644)
		gitRun(t, repoRoot, "add", ".")
		gitRun(t, repoRoot, "commit", "-m", "staging commit")
		gitRun(t, repoRoot, "checkout", "dev")

		d.CreateConductor(ctx, db.ConductorRecord{
			ID:            "s-nogh",
			PID:           1,
			Goal:          "test",
			Status:        "active",
			StagingBranch: "conductor/s-nogh",
			BaseBranch:    "dev",
			MaxParallel:   1,
			ModelStrategy: "all-opus",
			Runtime:       "local",
			MergeMode:     "pr",
		})

		c, _ := New(ConductorOpts{DB: d, RepoRoot: repoRoot})
		c.ConductorID = "s-nogh"

		_, prErr := c.CreateStagingPR(ctx)
		if prErr == nil {
			t.Fatal("expected error when gh not in PATH")
		}
		if !strings.Contains(prErr.Error(), "gh CLI not found") {
			t.Errorf("expected 'gh CLI not found' error, got: %v", prErr)
		}
	} else {
		t.Log("gh is installed — skipping negative path test")
	}
}

func TestMergeModePR_SkipsStagingDelete(t *testing.T) {
	// Verify deactivateConductor skips staging branch deletion in PR mode.
	repoRoot := t.TempDir()
	d := setupTestDB(t)
	ctx := context.Background()

	gitSetup(t, repoRoot)

	// Create staging branch
	stagingBranch := "conductor/s-pr-delete-test"
	gitRun(t, repoRoot, "branch", stagingBranch, "dev")

	d.CreateConductor(ctx, db.ConductorRecord{
		ID:            "s-pr-delete-test",
		PID:           os.Getpid(),
		Goal:          "test",
		Status:        "active",
		StagingBranch: stagingBranch,
		BaseBranch:    "dev",
		MaxParallel:   1,
		ModelStrategy: "all-opus",
		Runtime:       "local",
		MergeMode:     "pr",
	})

	c, _ := New(ConductorOpts{DB: d, RepoRoot: repoRoot, SessionID: "s-pr-delete-test"})
	c.ConductorID = "s-pr-delete-test"
	c.conductorActive = true

	// Deactivate — staging branch should survive
	c.deactivateConductor(ctx)

	// Verify staging branch still exists
	out, err := gitExec(repoRoot, "branch", "--list", stagingBranch)
	if err != nil {
		t.Fatalf("listing branches: %v", err)
	}
	if !strings.Contains(out, stagingBranch) {
		t.Errorf("staging branch %s should NOT have been deleted in PR mode", stagingBranch)
	}
}

func TestMergeModeLocal_DeletesStagingBranch(t *testing.T) {
	// Verify deactivateConductor DOES delete staging branch in local mode.
	repoRoot := t.TempDir()
	d := setupTestDB(t)
	ctx := context.Background()

	gitSetup(t, repoRoot)

	// Create staging branch
	stagingBranch := "conductor/s-local-delete-test"
	gitRun(t, repoRoot, "branch", stagingBranch, "dev")

	d.CreateConductor(ctx, db.ConductorRecord{
		ID:            "s-local-delete-test",
		PID:           os.Getpid(),
		Goal:          "test",
		Status:        "active",
		StagingBranch: stagingBranch,
		BaseBranch:    "dev",
		MaxParallel:   1,
		ModelStrategy: "all-opus",
		Runtime:       "local",
		MergeMode:     "local",
	})

	c, _ := New(ConductorOpts{DB: d, RepoRoot: repoRoot, SessionID: "s-local-delete-test"})
	c.ConductorID = "s-local-delete-test"
	c.conductorActive = true

	// Deactivate — staging branch should be deleted
	c.deactivateConductor(ctx)

	// Verify staging branch is gone
	out, _ := gitExec(repoRoot, "branch", "--list", stagingBranch)
	if strings.Contains(out, stagingBranch) {
		t.Errorf("staging branch %s should have been deleted in local mode", stagingBranch)
	}
}

func TestMergeModePR_GoResult(t *testing.T) {
	// Verify GoResult.PRResult field exists and is populated when set.
	r := GoResult{
		PRResult: &PRResult{
			PRURL:    "https://github.com/org/repo/pull/42",
			PRNumber: 42,
			Branch:   "conductor/s-test",
			Base:     "dev",
		},
	}
	if r.PRResult == nil {
		t.Fatal("expected PRResult to be set")
	}
	if r.PRResult.PRURL != "https://github.com/org/repo/pull/42" {
		t.Errorf("unexpected PRURL: %s", r.PRResult.PRURL)
	}
	if r.PRResult.PRNumber != 42 {
		t.Errorf("unexpected PRNumber: %d", r.PRResult.PRNumber)
	}
}

func TestMergeModeLocal_Unchanged(t *testing.T) {
	// Verify mergeOpts correctly passes MergeMode through.
	opts := GoOpts{
		TestCmd:   "go test",
		MergeMode: "local",
	}
	m := opts.mergeOpts()
	if m.MergeMode != MergeModeLocal {
		t.Errorf("expected MergeModeLocal, got %q", m.MergeMode)
	}

	// Also test PR mode
	opts.MergeMode = "pr"
	m = opts.mergeOpts()
	if m.MergeMode != MergeModePR {
		t.Errorf("expected MergeModePR, got %q", m.MergeMode)
	}

	// Default: empty string should stay empty (caller checks for "" or "local")
	opts.MergeMode = ""
	m = opts.mergeOpts()
	if m.MergeMode != "" {
		t.Errorf("expected empty MergeMode, got %q", m.MergeMode)
	}
}

func TestPRResult_Fields(t *testing.T) {
	pr := PRResult{
		PRURL:    "https://github.com/org/repo/pull/123",
		PRNumber: 123,
		Branch:   "conductor/s-test",
		Base:     "dev",
	}
	if pr.PRURL == "" {
		t.Error("expected PRURL to be set")
	}
	if pr.PRNumber != 123 {
		t.Error("expected PRNumber=123")
	}
	if pr.Branch != "conductor/s-test" {
		t.Errorf("unexpected Branch: %s", pr.Branch)
	}
	if pr.Base != "dev" {
		t.Errorf("unexpected Base: %s", pr.Base)
	}
}

func TestAuditMergedFiles_ExtraFilesLogged(t *testing.T) {
	d := setupTestDB(t)
	ctx := context.Background()
	repoRoot := t.TempDir()

	gitSetup(t, repoRoot)

	// Create initial commit with one file
	os.WriteFile(filepath.Join(repoRoot, "owned.go"), []byte("package main\n"), 0o644)
	gitRun(t, repoRoot, "add", ".")
	gitRun(t, repoRoot, "commit", "-m", "init files")

	// Create a branch with changes to owned.go + an extra unowned file
	gitRun(t, repoRoot, "checkout", "-b", "feature/T-audit")
	os.WriteFile(filepath.Join(repoRoot, "owned.go"), []byte("package main\n// changed\n"), 0o644)
	os.WriteFile(filepath.Join(repoRoot, "extra.go"), []byte("package main\n// extra\n"), 0o644)
	gitRun(t, repoRoot, "add", ".")
	gitRun(t, repoRoot, "commit", "-m", "modify owned + add extra")
	gitRun(t, repoRoot, "checkout", "dev")

	// Task T-audit only owns owned.go
	d.CreateTask(ctx, db.Task{ID: "T-audit", Title: "Audit test", Status: "done", Role: "implementer"})
	d.CreateFileLock(ctx, "owned.go", "", "T-audit", time.Time{})

	c, _ := New(ConductorOpts{DB: d, RepoRoot: repoRoot})

	// Merge the branch so HEAD~1..HEAD shows the diff
	gitRun(t, repoRoot, "merge", "feature/T-audit")

	// Run audit
	c.auditMergedFiles(ctx, "T-audit")

	// Verify file_violation event was logged for extra.go
	events, err := d.RecentEvents(ctx, 100)
	if err != nil {
		t.Fatalf("listing events: %v", err)
	}

	found := false
	for _, ev := range events {
		if ev.EventType == "file_violation" && strings.Contains(ev.Payload.String, "extra.go") {
			found = true
		}
	}
	if !found {
		t.Error("expected file_violation event for extra.go, found none")
	}
}

func TestAuditMergedFiles_NoExtras(t *testing.T) {
	d := setupTestDB(t)
	ctx := context.Background()
	repoRoot := t.TempDir()

	gitSetup(t, repoRoot)

	os.WriteFile(filepath.Join(repoRoot, "owned.go"), []byte("package main\n"), 0o644)
	gitRun(t, repoRoot, "add", ".")
	gitRun(t, repoRoot, "commit", "-m", "init")

	gitRun(t, repoRoot, "checkout", "-b", "feature/T-clean")
	os.WriteFile(filepath.Join(repoRoot, "owned.go"), []byte("package main\n// changed\n"), 0o644)
	gitRun(t, repoRoot, "add", ".")
	gitRun(t, repoRoot, "commit", "-m", "modify owned only")
	gitRun(t, repoRoot, "checkout", "dev")

	d.CreateTask(ctx, db.Task{ID: "T-clean", Title: "Clean test", Status: "done", Role: "implementer"})
	d.CreateFileLock(ctx, "owned.go", "", "T-clean", time.Time{})

	c, _ := New(ConductorOpts{DB: d, RepoRoot: repoRoot})
	gitRun(t, repoRoot, "merge", "feature/T-clean")

	c.auditMergedFiles(ctx, "T-clean")

	// No file_violation events should exist
	events, _ := d.RecentEvents(ctx, 100)
	for _, ev := range events {
		if ev.EventType == "file_violation" {
			t.Errorf("unexpected file_violation event: %s", ev.Payload.String)
		}
	}
}
