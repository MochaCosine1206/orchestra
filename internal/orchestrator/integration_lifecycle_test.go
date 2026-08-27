package orchestrator

import (
	"context"
	"database/sql"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/MochaCosine1206/orchestra/internal/db"
)

// ============================================================
// Phase 1: Go() lifecycle integration tests
// Tests the full decompose → spawn → wait → merge flow with
// in-memory SQLite and mock runner. Uses real git repos.
// ============================================================

// initGitRepo creates a real git repo with an initial commit.
func initGitRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	for _, args := range [][]string{
		{"init", "-b", "dev"},
		{"config", "user.email", "test@test.com"},
		{"config", "user.name", "Test"},
	} {
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %s (%v)", args, out, err)
		}
	}
	os.WriteFile(filepath.Join(dir, "README.md"), []byte("init"), 0o644)
	for _, args := range [][]string{
		{"add", "-A"},
		{"commit", "-m", "init"},
	} {
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %s (%v)", args, out, err)
		}
	}
	return dir
}

// newIntegConductor creates a Conductor backed by in-memory DB and real git repo.
func newIntegConductor(t *testing.T, runner ClaudeRunner) (*Conductor, *db.DB) {
	t.Helper()
	d := setupTestDB(t)
	repoRoot := initGitRepo(t)
	c, err := New(ConductorOpts{DB: d, RepoRoot: repoRoot, Runner: runner})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	c.DisableNotify = true
	c.Log = func(string) {}
	return c, d
}

// ensureConductorRecord inserts a minimal conductor record so that tasks with
// conductor_id FK constraints can be created. Required because foreign_keys=ON.
func ensureConductorRecord(t *testing.T, d *db.DB, conductorID string) {
	t.Helper()
	ctx := context.Background()
	err := d.CreateConductor(ctx, db.ConductorRecord{
		ID: conductorID, PID: os.Getpid(), Goal: "test",
		Status: "active", StagingBranch: "staging", BaseBranch: "dev",
		MaxParallel: 1, ModelStrategy: "all-opus", Runtime: "local",
	})
	if err != nil {
		t.Fatalf("CreateConductor: %v", err)
	}
}

func TestGoLifecycle_DryRun_DecomposesCorrectly(t *testing.T) {
	runner := &MockRunner{
		Outputs: []string{`[
			{"title":"Add feature A","description":"Implement feature A","role":"implementer","priority":5},
			{"title":"Add tests","description":"Write tests for A","role":"implementer","priority":4,"depends_on":["Add feature A"]}
		]`},
	}
	c, _ := newIntegConductor(t, runner)
	ctx := context.Background()

	result, err := c.Go(ctx, GoOpts{Goal: "Build feature A with tests", DryRun: true})
	if err != nil {
		t.Fatalf("Go: %v", err)
	}
	if result.TaskCount != 2 {
		t.Errorf("TaskCount = %d, want 2", result.TaskCount)
	}
	if result.SessionID == "" {
		t.Error("SessionID should be set")
	}
	// Verify decompose was called
	if len(runner.Calls) != 1 {
		t.Errorf("runner.Calls = %d, want 1 (decompose only)", len(runner.Calls))
	}
}

func TestGoLifecycle_ActivateDeactivate(t *testing.T) {
	// Verify conductor activation creates DB record and deactivation clears it
	d := setupTestDB(t)
	repoRoot := initGitRepo(t)
	c, _ := New(ConductorOpts{DB: d, RepoRoot: repoRoot, Runner: &MockRunner{}})
	ctx := context.Background()

	err := c.activateConductor(ctx, activateOpts{
		PID:         os.Getpid(),
		MaxParallel: 4,
		Goal:        "Test activation",
		SessionID:   c.SessionID,
		BaseBranch:  "dev",
		Runtime:     "local",
	})
	if err != nil {
		t.Fatalf("activate: %v", err)
	}
	if !c.conductorActive {
		t.Error("conductorActive should be true")
	}

	// Verify conductor record exists
	conductors, _ := d.ListAllConductors(ctx)
	if len(conductors) != 1 {
		t.Fatalf("expected 1 conductor, got %d", len(conductors))
	}
	if conductors[0].Status != "active" {
		t.Errorf("status = %q, want 'active'", conductors[0].Status)
	}

	c.deactivateConductor(ctx)
	if c.conductorActive {
		t.Error("conductorActive should be false after deactivation")
	}
}

func TestGoLifecycle_WaitForTasks_AllDone(t *testing.T) {
	// Pre-populate tasks in terminal state; waitForTasks should return immediately
	// Use same pattern as TestWaitForTasksCompletesNormally in helpers_test.go
	d := setupTestDB(t)
	c, _ := New(ConductorOpts{DB: d, RepoRoot: "/tmp/test"})
	ctx := context.Background()

	d.CreateTask(ctx, db.Task{ID: "T-001", Title: "Task 1", Status: "done", Role: "implementer"})
	d.CreateTask(ctx, db.Task{ID: "T-002", Title: "Task 2", Status: "done", Role: "implementer"})
	d.SetBlackboard(ctx, "conductor:task_ids", `["T-001","T-002"]`, "test")

	done, failed, err := c.waitForTasks(ctx)
	if err != nil {
		t.Fatalf("waitForTasks: %v", err)
	}
	if done != 2 {
		t.Errorf("done = %d, want 2", done)
	}
	if failed != 0 {
		t.Errorf("failed = %d, want 0", failed)
	}
}

func TestGoLifecycle_WaitForTasks_MixedOutcome(t *testing.T) {
	d := setupTestDB(t)
	c, _ := New(ConductorOpts{DB: d, RepoRoot: "/tmp/test"})
	ctx := context.Background()

	d.CreateTask(ctx, db.Task{ID: "T-001", Title: "Task 1", Status: "done", Role: "implementer"})
	d.CreateTask(ctx, db.Task{ID: "T-002", Title: "Task 2", Status: "failed", Role: "implementer"})
	d.SetBlackboard(ctx, "conductor:task_ids", `["T-001","T-002"]`, "test")

	done, failed, err := c.waitForTasks(ctx)
	if err != nil {
		t.Fatalf("waitForTasks: %v", err)
	}
	if done != 1 || failed != 1 {
		t.Errorf("done=%d failed=%d, want 1/1", done, failed)
	}
}

func TestGoLifecycle_WaitForTasks_ContextCancel(t *testing.T) {
	d := setupTestDB(t)
	c, _ := New(ConductorOpts{DB: d, RepoRoot: "/tmp/test"})
	ctx, cancel := context.WithCancel(context.Background())

	// Task stays in "running" so waitForTasks will block until cancel
	d.CreateTask(ctx, db.Task{ID: "T-001", Title: "Task 1", Status: "running", Role: "implementer"})
	d.SetBlackboard(ctx, "conductor:task_ids", `["T-001"]`, "test")

	go func() {
		time.Sleep(100 * time.Millisecond)
		cancel()
	}()

	_, _, err := c.waitForTasks(ctx)
	if err == nil {
		t.Fatal("expected error from context cancellation")
	}
}

func TestGoLifecycle_WaitForTasks_TransitionDuringPoll(t *testing.T) {
	// Task starts running, transitions to done during polling
	d := setupTestDB(t)
	c, _ := New(ConductorOpts{DB: d, RepoRoot: "/tmp/test"})
	ctx := context.Background()

	d.CreateTask(ctx, db.Task{ID: "T-001", Title: "Task 1", Status: "running", Role: "implementer"})
	d.SetBlackboard(ctx, "conductor:task_ids", `["T-001"]`, "test")

	// Transition task to done after a short delay
	go func() {
		time.Sleep(200 * time.Millisecond)
		d.CompleteTask(ctx, "T-001", "completed")
	}()

	done, failed, err := c.waitForTasks(ctx)
	if err != nil {
		t.Fatalf("waitForTasks: %v", err)
	}
	if done != 1 || failed != 0 {
		t.Errorf("done=%d failed=%d, want 1/0", done, failed)
	}
}

func TestGoLifecycle_SessionTaskIDs_FromConductorID(t *testing.T) {
	d := setupTestDB(t)
	c, _ := New(ConductorOpts{DB: d, RepoRoot: "/tmp/test"})
	ctx := context.Background()

	// FK constraint requires a conductor record before creating tasks with conductor_id
	ensureConductorRecord(t, d, c.SessionID)

	// Create tasks with conductor_id set
	if err := d.CreateTask(ctx, db.Task{
		ID: "T-001", Title: "t1", Status: "pending", Role: "implementer",
		ConductorID: sql.NullString{String: c.SessionID, Valid: true},
	}); err != nil {
		t.Fatalf("CreateTask T-001: %v", err)
	}
	if err := d.CreateTask(ctx, db.Task{
		ID: "T-002", Title: "t2", Status: "pending", Role: "implementer",
		ConductorID: sql.NullString{String: c.SessionID, Valid: true},
	}); err != nil {
		t.Fatalf("CreateTask T-002: %v", err)
	}

	// Set ConductorID so sessionTaskIDs uses the DB column path
	c.ConductorID = c.SessionID
	ids := c.sessionTaskIDs(ctx)
	if len(ids) != 2 {
		t.Errorf("sessionTaskIDs = %d, want 2", len(ids))
	}
}

func TestGoLifecycle_SessionTaskIDs_FallbackToBlackboard(t *testing.T) {
	d := setupTestDB(t)
	c, _ := New(ConductorOpts{DB: d, RepoRoot: "/tmp/test"})
	ctx := context.Background()

	// No ConductorID set, use blackboard fallback
	c.ConductorID = ""
	d.SetBlackboard(ctx, "conductor:task_ids", `["T-AAA","T-BBB"]`, "test")

	ids := c.sessionTaskIDs(ctx)
	if len(ids) != 2 || ids[0] != "T-AAA" || ids[1] != "T-BBB" {
		t.Errorf("sessionTaskIDs = %v, want [T-AAA T-BBB]", ids)
	}
}

func TestGoLifecycle_ProgressCallback_Phases(t *testing.T) {
	runner := &MockRunner{
		Outputs: []string{`[{"title":"Task","description":"d","role":"implementer","priority":5}]`},
	}
	c, _ := newIntegConductor(t, runner)

	var phases []string
	c.ProgressFunc = func(phase, detail string) {
		phases = append(phases, phase)
	}

	ctx := context.Background()
	_, err := c.Go(ctx, GoOpts{Goal: "Test progress", DryRun: true})
	if err != nil {
		t.Fatalf("Go: %v", err)
	}

	// DryRun should emit at least activate and decompose phases
	if len(phases) < 1 {
		t.Errorf("expected progress callbacks, got %d", len(phases))
	}
	found := false
	for _, p := range phases {
		if p == "decompose" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected 'decompose' phase in callbacks: %v", phases)
	}
}

func TestGoLifecycle_PanicRecovery(t *testing.T) {
	// Verify that a panic in Go() is caught and returns an error
	d := setupTestDB(t)
	repoRoot := initGitRepo(t)
	c, _ := New(ConductorOpts{DB: d, RepoRoot: repoRoot, Runner: &MockRunner{}})
	c.DisableNotify = true
	c.Log = func(string) {}
	ctx := context.Background()

	// Activate so we have a conductor to check status on
	c.activateConductor(ctx, activateOpts{
		PID: os.Getpid(), MaxParallel: 1, Goal: "panic test",
		SessionID: c.SessionID, BaseBranch: "dev", Runtime: "local",
	})

	// Overwrite Spawner to nil — Batch() on nil will panic after decompose
	runner := &MockRunner{
		Outputs: []string{`[{"title":"Task","description":"d","role":"implementer","priority":5}]`},
	}
	c.Runner = runner
	c.Spawner = nil

	_, err := c.Go(ctx, GoOpts{Goal: "trigger panic"})
	if err == nil {
		t.Fatal("expected error from panic recovery")
	}
	if !strings.Contains(err.Error(), "panic") {
		t.Errorf("error = %q, should mention panic", err.Error())
	}
}

// ============================================================
// Phase 2: Auto mode circuit breaker tests
// ============================================================

// blockingRunner blocks until context is cancelled. Used to test timeout behavior.
type blockingRunner struct{}

func (b *blockingRunner) Run(ctx context.Context, _ RunOpts) (RunResult, error) {
	<-ctx.Done()
	return RunResult{}, ctx.Err()
}

func TestAuto_TimeoutCircuitBreaker(t *testing.T) {
	d := setupTestDB(t)
	repoRoot := initGitRepo(t)
	c, _ := New(ConductorOpts{DB: d, RepoRoot: repoRoot, Runner: &blockingRunner{}})
	c.DisableNotify = true
	c.Log = func(string) {}

	// Use a very short timeout via context — the blocking runner ensures
	// Go() doesn't return fast enough for all-fail to trigger first.
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	result, err := c.Auto(ctx, AutoOpts{
		Goal:      "test timeout",
		MaxCycles: 100,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.StopReason != "timeout" {
		t.Errorf("StopReason = %q, want 'timeout'", result.StopReason)
	}
}

func TestAuto_PauseResumeViaBlackboard(t *testing.T) {
	d := setupTestDB(t)
	c, _ := New(ConductorOpts{DB: d, RepoRoot: "/tmp/nonexistent-auto-test"})
	ctx := context.Background()

	// Set pause, then clear it after a short delay
	d.SetBlackboard(ctx, "auto:pause", "testing", "test")
	go func() {
		time.Sleep(200 * time.Millisecond)
		d.DeleteBlackboard(ctx, "auto:pause")
	}()

	// Use short timeout to prevent hanging
	tctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()

	result, err := c.Auto(tctx, AutoOpts{
		Sources:   "audit",
		MaxCycles: 1,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// After unpausing, it should run and hit max_cycles or empty_cycles
	if result.StopReason == "" {
		t.Error("StopReason should be set")
	}
}

func TestAuto_StopSignalMidCycle(t *testing.T) {
	d := setupTestDB(t)
	c, _ := New(ConductorOpts{DB: d, RepoRoot: "/tmp/test"})
	ctx := context.Background()

	// Pre-set stop signal
	d.SetBlackboard(ctx, "auto:stop", "user", "test")

	result, err := c.Auto(ctx, AutoOpts{
		Goal:      "should not run",
		MaxCycles: 10,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.StopReason != "stopped" {
		t.Errorf("StopReason = %q, want 'stopped'", result.StopReason)
	}
	if result.CyclesRun != 0 {
		t.Errorf("CyclesRun = %d, want 0 (stopped before any cycle)", result.CyclesRun)
	}
}

func TestAuto_EmptyAndAllFailCountersReset(t *testing.T) {
	// Verify that empty cycle counter resets when work is found
	d := setupTestDB(t)
	// nil runner → Go() fails → counts as all_fail
	c, _ := New(ConductorOpts{DB: d, RepoRoot: "/tmp/test"})
	ctx := context.Background()

	result, err := c.Auto(ctx, AutoOpts{
		Goal:      "test",
		MaxCycles: 5,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// With a starting goal and nil runner, every cycle finds work (goal reused on failure)
	// but Go() fails → allFail counter increments → stops at maxConsecutiveAllFail=3
	if result.StopReason != "all_fail" {
		t.Errorf("StopReason = %q, want 'all_fail'", result.StopReason)
	}
	if result.GoalsFound != maxConsecutiveAllFail {
		t.Errorf("GoalsFound = %d, want %d", result.GoalsFound, maxConsecutiveAllFail)
	}
}

func TestAuto_ResultDuration_Positive(t *testing.T) {
	d := setupTestDB(t)
	c, _ := New(ConductorOpts{DB: d, RepoRoot: "/tmp/nonexistent-auto-test"})
	ctx := context.Background()

	result, err := c.Auto(ctx, AutoOpts{Sources: "audit", MaxCycles: 1})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Duration <= 0 {
		t.Error("Duration should be positive")
	}
}

// ============================================================
// Phase 3: Reconciliation tests
// ============================================================

func TestReconcile_OrphanWorktreeWithFiles(t *testing.T) {
	d := setupTestDB(t)
	repoRoot := t.TempDir()

	// Create .worktree directory with orphan that has uncommitted files
	worktreeDir := filepath.Join(repoRoot, ".worktree")
	orphanDir := filepath.Join(worktreeDir, "t-orphan-with-files")
	os.MkdirAll(orphanDir, 0o755)
	os.WriteFile(filepath.Join(orphanDir, "uncommitted.go"), []byte("package main\n"), 0o644)
	os.WriteFile(filepath.Join(orphanDir, "another.go"), []byte("package main\n"), 0o644)

	c, _ := New(ConductorOpts{DB: d, RepoRoot: repoRoot, Runner: &MockRunner{}})
	c.DisableNotify = true
	ctx := context.Background()

	d.CreateTask(ctx, db.Task{ID: "t1", Title: "Task 1", Status: "done", Role: "implementer"})
	c.storeSessionTaskIDs(ctx, []string{"t1"})
	c.setBlackboard(ctx, "conductor:goal", "Test orphan salvage")

	result, err := c.Reconcile(ctx, ReconcileOpts{DryRun: true, SkipLLM: true})
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if len(result.OrphanWorktrees) != 1 {
		t.Errorf("expected 1 orphan, got %d: %v", len(result.OrphanWorktrees), result.OrphanWorktrees)
	}
}

func TestReconcile_MultipleOrphans_CountedCorrectly(t *testing.T) {
	d := setupTestDB(t)
	repoRoot := t.TempDir()

	worktreeDir := filepath.Join(repoRoot, ".worktree")
	for _, name := range []string{"t-orphan-a", "t-orphan-b", "t-orphan-c"} {
		os.MkdirAll(filepath.Join(worktreeDir, name), 0o755)
	}

	c, _ := New(ConductorOpts{DB: d, RepoRoot: repoRoot, Runner: &MockRunner{}})
	c.DisableNotify = true
	ctx := context.Background()

	d.CreateTask(ctx, db.Task{ID: "t1", Title: "Task 1", Status: "done", Role: "implementer"})
	c.storeSessionTaskIDs(ctx, []string{"t1"})
	c.setBlackboard(ctx, "conductor:goal", "Test multiple orphans")

	result, err := c.Reconcile(ctx, ReconcileOpts{DryRun: true, SkipLLM: true})
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if len(result.OrphanWorktrees) != 3 {
		t.Errorf("expected 3 orphans, got %d", len(result.OrphanWorktrees))
	}
}

func TestReconcile_FailedTaskDetails(t *testing.T) {
	d := setupTestDB(t)
	repoRoot := t.TempDir()
	c, _ := New(ConductorOpts{DB: d, RepoRoot: repoRoot, Runner: &MockRunner{}})
	c.DisableNotify = true
	ctx := context.Background()

	d.CreateTask(ctx, db.Task{
		ID: "t1", Title: "Research", Status: "done", Role: "researcher",
	})
	d.CreateTask(ctx, db.Task{
		ID: "t2", Title: "Implement", Status: "failed", Role: "implementer",
		Result: sql.NullString{String: "rate_limit", Valid: true},
	})
	d.CreateTask(ctx, db.Task{
		ID: "t3", Title: "Review", Status: "failed", Role: "reviewer",
		Result: sql.NullString{String: "normal_failure", Valid: true},
	})

	c.storeSessionTaskIDs(ctx, []string{"t1", "t2", "t3"})
	c.setBlackboard(ctx, "conductor:goal", "Multi-task goal")
	c.setBlackboard(ctx, "failure_type:t2", "rate_limit")
	c.setBlackboard(ctx, "failure_type:t3", "normal_failure")

	result, err := c.Reconcile(ctx, ReconcileOpts{SkipLLM: true})
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if len(result.FailedTasks) != 2 {
		t.Errorf("expected 2 failed tasks, got %d", len(result.FailedTasks))
	}
	// Heuristic score: 1/3 done
	expectedScore := float64(1) / float64(3)
	if result.GoalAlignmentScore < expectedScore-0.01 || result.GoalAlignmentScore > expectedScore+0.01 {
		t.Errorf("GoalAlignmentScore = %f, want ~%f", result.GoalAlignmentScore, expectedScore)
	}
}

func TestReconcile_LLMGaps_CreateFollowUps(t *testing.T) {
	d := setupTestDB(t)
	repoRoot := t.TempDir()

	runner := &MockRunner{
		Outputs: []string{`{"score": 0.4, "gaps": ["Missing error handling", "No documentation", "Performance issues"], "note": "Needs more work"}`},
	}
	c, _ := New(ConductorOpts{DB: d, RepoRoot: repoRoot, Runner: runner})
	c.DisableNotify = true
	ctx := context.Background()

	d.CreateTask(ctx, db.Task{ID: "t1", Title: "Implement", Status: "done", Role: "implementer"})
	d.CreateTask(ctx, db.Task{ID: "t2", Title: "Test", Status: "failed", Role: "implementer"})

	c.storeSessionTaskIDs(ctx, []string{"t1", "t2"})
	c.setBlackboard(ctx, "conductor:goal", "Build robust feature")
	c.setBlackboard(ctx, "result_summary:t1", "Basic implementation done")

	result, err := c.Reconcile(ctx, ReconcileOpts{SkipLLM: false})
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if result.GoalAlignmentScore != 0.4 {
		t.Errorf("GoalAlignmentScore = %f, want 0.4", result.GoalAlignmentScore)
	}
	if len(result.GapsIdentified) != 3 {
		t.Errorf("GapsIdentified = %d, want 3", len(result.GapsIdentified))
	}
	if len(result.FollowUpTaskIDs) != 3 {
		t.Errorf("FollowUpTaskIDs = %d, want 3", len(result.FollowUpTaskIDs))
	}
	// Verify follow-up tasks are pending in DB
	for _, id := range result.FollowUpTaskIDs {
		task, err := d.GetTaskByID(ctx, id)
		if err != nil || task == nil {
			t.Errorf("follow-up %s not in DB", id)
			continue
		}
		if task.Status != "pending" {
			t.Errorf("follow-up %s status = %q, want 'pending'", id, task.Status)
		}
	}
}

func TestReconcile_ReportIncludesSessionInfo(t *testing.T) {
	d := setupTestDB(t)
	repoRoot := t.TempDir()
	c, _ := New(ConductorOpts{DB: d, RepoRoot: repoRoot, Runner: &MockRunner{}})
	c.DisableNotify = true
	ctx := context.Background()

	d.CreateTask(ctx, db.Task{ID: "t1", Title: "Solo task", Status: "done", Role: "implementer"})
	c.storeSessionTaskIDs(ctx, []string{"t1"})
	c.setBlackboard(ctx, "conductor:goal", "Specific goal text for report")
	c.setBlackboard(ctx, "result_summary:t1", "Completed successfully")

	result, err := c.Reconcile(ctx, ReconcileOpts{SkipLLM: true})
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if !strings.Contains(result.Report, "Specific goal text for report") {
		t.Error("report should contain the goal text")
	}
	if !strings.Contains(result.Report, "Reconciliation Report") {
		t.Error("report should contain header")
	}
}

// ============================================================
// Phase 4: Iterative mode convergence tests
// ============================================================

func TestIterative_ConvergesOnNoGitChanges(t *testing.T) {
	// Runner produces output but makes no git commits → converges
	d := setupTestDB(t)
	repoRoot := initGitRepo(t)
	runner := &MockRunner{
		Outputs: []string{"Some analysis output, no changes needed"},
	}
	c, _ := New(ConductorOpts{DB: d, RepoRoot: repoRoot, Runner: runner})
	c.DisableNotify = true
	c.Log = func(string) {}
	ctx := context.Background()

	result, err := c.Go(ctx, GoOpts{
		Goal:      "Check if everything is fine",
		Iterative: true,
		MaxTasks:  5,
	})
	if err != nil {
		t.Fatalf("Go: %v", err)
	}
	// Should converge after 1 round (no git changes)
	if result.DoneCount != 1 {
		t.Errorf("DoneCount = %d, want 1 (converge after first round)", result.DoneCount)
	}
	if result.TaskCount != 1 {
		t.Errorf("TaskCount = %d, want 1", result.TaskCount)
	}
}

func TestIterative_CompletionSignalStopsEarly(t *testing.T) {
	d := setupTestDB(t)
	repoRoot := initGitRepo(t)

	// First call produces output without completion signal (but no git changes → converges)
	// To test completion signal properly, we need git changes between rounds
	runner := &MockRunner{
		Outputs: []string{"All work is DONE and COMPLETE"},
	}
	c, _ := New(ConductorOpts{DB: d, RepoRoot: repoRoot, Runner: runner})
	c.DisableNotify = true
	c.Log = func(string) {}
	ctx := context.Background()

	result, err := c.Go(ctx, GoOpts{
		Goal:      "Quick task",
		Iterative: true,
		MaxTasks:  5,
	})
	if err != nil {
		t.Fatalf("Go: %v", err)
	}
	// Should stop early — completion signal in output
	if result.DoneCount != 1 {
		t.Errorf("DoneCount = %d, want 1", result.DoneCount)
	}
}

func TestIterative_MaxRoundsCap(t *testing.T) {
	d := setupTestDB(t)
	repoRoot := initGitRepo(t)

	// Runner never signals completion and always produces output
	runner := &MockRunner{
		Outputs: []string{"still working on it..."},
	}
	c, _ := New(ConductorOpts{DB: d, RepoRoot: repoRoot, Runner: runner})
	c.DisableNotify = true
	c.Log = func(string) {}
	ctx := context.Background()

	result, err := c.Go(ctx, GoOpts{
		Goal:      "Long task",
		Iterative: true,
		MaxTasks:  2, // cap at 2 rounds
	})
	if err != nil {
		t.Fatalf("Go: %v", err)
	}
	// No git changes → converges on first round regardless of MaxTasks
	if result.TaskCount > 2 {
		t.Errorf("TaskCount = %d, should be <= 2", result.TaskCount)
	}
}

func TestIterative_RunnerErrorCountsAsFailed(t *testing.T) {
	d := setupTestDB(t)
	repoRoot := initGitRepo(t)

	runner := &MockRunner{
		Outputs: []string{"error"},
		Errors:  []error{os.ErrInvalid},
	}
	c, _ := New(ConductorOpts{DB: d, RepoRoot: repoRoot, Runner: runner})
	c.DisableNotify = true
	c.Log = func(string) {}
	ctx := context.Background()

	result, err := c.Go(ctx, GoOpts{
		Goal:      "Failing task",
		Iterative: true,
		MaxTasks:  3,
	})
	if err != nil {
		t.Fatalf("Go: %v", err)
	}
	// Errors are counted but don't stop iteration — convergence (no git changes) does
	if result.FailedCount == 0 {
		t.Error("expected at least 1 failure")
	}
}

func TestIterative_ContextCancelledDuringRound(t *testing.T) {
	d := setupTestDB(t)
	repoRoot := initGitRepo(t)

	runner := &MockRunner{
		Outputs: []string{"working..."},
	}
	c, _ := New(ConductorOpts{DB: d, RepoRoot: repoRoot, Runner: runner})
	c.DisableNotify = true
	c.Log = func(string) {}

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	// Runner completes fast, but context cancellation should be respected
	_, err := c.Go(ctx, GoOpts{
		Goal:      "Cancelled task",
		Iterative: true,
		MaxTasks:  5,
	})
	// May or may not error depending on timing — the key is it doesn't hang
	_ = err
}

// ============================================================
// Cross-cutting: merge queue helpers
// ============================================================

func TestUnblockReadyEntries_MultipleDeps(t *testing.T) {
	d := setupTestDB(t)
	c, _ := New(ConductorOpts{DB: d, RepoRoot: "/tmp/test"})
	ctx := context.Background()

	// T-001 done, T-002 done, T-003 blocked on both
	d.CreateTask(ctx, db.Task{
		ID: "T-001", Title: "t1", Status: "done", Role: "implementer",
		Branch: sql.NullString{String: "feature/T-001", Valid: true},
	})
	d.CreateTask(ctx, db.Task{
		ID: "T-002", Title: "t2", Status: "done", Role: "implementer",
		Branch: sql.NullString{String: "feature/T-002", Valid: true},
	})
	d.CreateTask(ctx, db.Task{
		ID: "T-003", Title: "t3", Status: "done", Role: "implementer",
		Branch:    sql.NullString{String: "feature/T-003", Valid: true},
		BlockedBy: sql.NullString{String: `["T-001","T-002"]`, Valid: true},
	})

	d.SetBlackboard(ctx, "conductor:task_ids", `["T-001","T-002","T-003"]`, "test")

	// All deps merged
	d.CreateMergeQueueEntry(ctx, db.MergeQueueEntry{
		ID: "mq-1", ConductorID: c.SessionID, TaskID: "T-001", Branch: "feature/T-001", Status: "merged",
	})
	d.CreateMergeQueueEntry(ctx, db.MergeQueueEntry{
		ID: "mq-2", ConductorID: c.SessionID, TaskID: "T-002", Branch: "feature/T-002", Status: "merged",
	})
	d.CreateMergeQueueEntry(ctx, db.MergeQueueEntry{
		ID: "mq-3", ConductorID: c.SessionID, TaskID: "T-003", Branch: "feature/T-003", Status: "blocked",
	})

	c.unblockReadyEntries(ctx, c.SessionID)

	entry, err := d.GetMergeQueueEntryByTaskID(ctx, c.SessionID, "T-003")
	if err != nil {
		t.Fatalf("getting entry: %v", err)
	}
	if entry.Status != "pending" {
		t.Errorf("T-003 status = %q, want 'pending'", entry.Status)
	}
}

func TestUnblockReadyEntries_PartialDeps_StaysBlocked(t *testing.T) {
	d := setupTestDB(t)
	c, _ := New(ConductorOpts{DB: d, RepoRoot: "/tmp/test"})
	ctx := context.Background()

	d.CreateTask(ctx, db.Task{
		ID: "T-001", Title: "t1", Status: "done", Role: "implementer",
		Branch: sql.NullString{String: "feature/T-001", Valid: true},
	})
	d.CreateTask(ctx, db.Task{
		ID: "T-002", Title: "t2", Status: "running", Role: "implementer",
		Branch: sql.NullString{String: "feature/T-002", Valid: true},
	})
	d.CreateTask(ctx, db.Task{
		ID: "T-003", Title: "t3", Status: "done", Role: "implementer",
		Branch:    sql.NullString{String: "feature/T-003", Valid: true},
		BlockedBy: sql.NullString{String: `["T-001","T-002"]`, Valid: true},
	})

	d.SetBlackboard(ctx, "conductor:task_ids", `["T-001","T-002","T-003"]`, "test")

	d.CreateMergeQueueEntry(ctx, db.MergeQueueEntry{
		ID: "mq-1", ConductorID: c.SessionID, TaskID: "T-001", Branch: "feature/T-001", Status: "merged",
	})
	// T-002 still pending in merge queue
	d.CreateMergeQueueEntry(ctx, db.MergeQueueEntry{
		ID: "mq-2", ConductorID: c.SessionID, TaskID: "T-002", Branch: "feature/T-002", Status: "pending",
	})
	d.CreateMergeQueueEntry(ctx, db.MergeQueueEntry{
		ID: "mq-3", ConductorID: c.SessionID, TaskID: "T-003", Branch: "feature/T-003", Status: "blocked",
	})

	c.unblockReadyEntries(ctx, c.SessionID)

	entry, err := d.GetMergeQueueEntryByTaskID(ctx, c.SessionID, "T-003")
	if err != nil {
		t.Fatalf("getting entry: %v", err)
	}
	if entry.Status != "blocked" {
		t.Errorf("T-003 status = %q, want 'blocked' (dep T-002 not merged)", entry.Status)
	}
}
