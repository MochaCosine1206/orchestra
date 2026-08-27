package orchestrator

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	"github.com/MochaCosine1206/orchestra/internal/db"
)

// === go.go: mergeOpts ===

func TestMergeOpts_Defaults(t *testing.T) {
	opts := GoOpts{Goal: "test"}
	m := opts.mergeOpts()
	if m.TestCmd != "" {
		t.Errorf("TestCmd = %q, want empty", m.TestCmd)
	}
	if m.Review {
		t.Error("Review should be false")
	}
	if m.TestCmdTimeout != 0 {
		t.Errorf("TestCmdTimeout = %v, want 0", m.TestCmdTimeout)
	}
	if m.MergeMode != "" {
		t.Errorf("MergeMode = %q, want empty", m.MergeMode)
	}
}

func TestMergeOpts_WithValues(t *testing.T) {
	opts := GoOpts{
		Goal:            "test",
		TestCmd:         "go test ./...",
		Review:          true,
		TestCmdTimeout:  120,
		TestFailureMode: "warn_only",
		MergeMode:       "pr",
		BaseBranch:      "main",
		PhaseID:         "phase-1",
	}
	m := opts.mergeOpts()
	if m.TestCmd != "go test ./..." {
		t.Errorf("TestCmd = %q", m.TestCmd)
	}
	if !m.Review {
		t.Error("Review should be true")
	}
	if m.TestCmdTimeout.Seconds() != 120 {
		t.Errorf("TestCmdTimeout = %v, want 120s", m.TestCmdTimeout)
	}
	if m.TestFailureMode != TestFailureModeWarnOnly {
		t.Errorf("TestFailureMode = %q", m.TestFailureMode)
	}
	if m.MergeMode != MergeModePR {
		t.Errorf("MergeMode = %q", m.MergeMode)
	}
	if m.BaseBranch != "main" {
		t.Errorf("BaseBranch = %q", m.BaseBranch)
	}
	if m.PhaseID != "phase-1" {
		t.Errorf("PhaseID = %q", m.PhaseID)
	}
}

func TestMergeOpts_ZeroTimeout(t *testing.T) {
	opts := GoOpts{TestCmdTimeout: 0}
	m := opts.mergeOpts()
	if m.TestCmdTimeout != 0 {
		t.Errorf("expected zero timeout, got %v", m.TestCmdTimeout)
	}
}

// === phase.go: checkPhaseGate ===

func TestCheckPhaseGate_NoneMode(t *testing.T) {
	phase := Phase{ID: "p1", Gate: PhaseGate{Mode: "none"}}
	passed, output, err := checkPhaseGate("/tmp", phase)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !passed {
		t.Error("none mode should always pass")
	}
	if output == "" {
		t.Error("output should contain explanation")
	}
}

func TestCheckPhaseGate_TestModeNoCmd(t *testing.T) {
	phase := Phase{ID: "p1", Gate: PhaseGate{Mode: "test"}}
	passed, _, err := checkPhaseGate("/tmp", phase)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if passed {
		t.Error("test mode with no test_cmd should fail")
	}
}

func TestCheckPhaseGate_TestModeWithCmd(t *testing.T) {
	phase := Phase{ID: "p1", Gate: PhaseGate{Mode: "test", TestCmd: "true"}}
	passed, _, err := checkPhaseGate(t.TempDir(), phase)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !passed {
		t.Error("test mode with 'true' command should pass")
	}
}

func TestCheckPhaseGate_TestModeFailing(t *testing.T) {
	phase := Phase{ID: "p1", Gate: PhaseGate{Mode: "test", TestCmd: "false"}}
	passed, output, err := checkPhaseGate(t.TempDir(), phase)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if passed {
		t.Error("test mode with 'false' command should fail")
	}
	if output == "" {
		t.Error("output should contain failure info")
	}
}

func TestCheckPhaseGate_AcceptanceModeNoCriteria(t *testing.T) {
	phase := Phase{ID: "p1", Gate: PhaseGate{Mode: "acceptance"}}
	passed, _, err := checkPhaseGate("/tmp", phase)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !passed {
		t.Error("acceptance mode with no criteria should pass")
	}
}

func TestCheckPhaseGate_AutoDetectNoGate(t *testing.T) {
	phase := Phase{ID: "p1", Gate: PhaseGate{}}
	passed, output, err := checkPhaseGate("/tmp", phase)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !passed {
		t.Error("no gate defined should auto-pass")
	}
	if output != "no gate defined" {
		t.Errorf("output = %q, want 'no gate defined'", output)
	}
}

func TestCheckPhaseGate_AutoDetectWithTestCmd(t *testing.T) {
	phase := Phase{ID: "p1", Gate: PhaseGate{TestCmd: "true"}}
	passed, _, err := checkPhaseGate(t.TempDir(), phase)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !passed {
		t.Error("auto-detect with test_cmd 'true' should pass")
	}
}

func TestCheckPhaseGate_AutoDetectWithAcceptance(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "chapters"), 0o755)
	os.WriteFile(filepath.Join(dir, "chapters", "ch1.md"), []byte("c1"), 0o644)

	phase := Phase{
		ID:   "p1",
		Gate: PhaseGate{Acceptance: []string{"1 chapter written"}},
	}
	passed, _, err := checkPhaseGate(dir, phase)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !passed {
		t.Error("auto-detect with acceptance criteria should pass when files exist")
	}
}

func TestCheckPhaseGate_AcceptanceWithGlobCriteria(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "chapters"), 0o755)
	os.WriteFile(filepath.Join(dir, "chapters", "ch1.md"), []byte("c1"), 0o644)
	os.WriteFile(filepath.Join(dir, "chapters", "ch2.md"), []byte("c2"), 0o644)

	phase := Phase{
		ID:   "p1",
		Gate: PhaseGate{Mode: "acceptance", Acceptance: []string{"2 chapters written"}},
	}
	passed, output, err := checkPhaseGate(dir, phase)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !passed {
		t.Errorf("should pass with 2 chapters, output: %s", output)
	}
}

func TestCheckPhaseGate_AcceptanceFailing(t *testing.T) {
	dir := t.TempDir()
	phase := Phase{
		ID:   "p1",
		Gate: PhaseGate{Mode: "acceptance", Acceptance: []string{"5 chapters written"}},
	}
	passed, _, err := checkPhaseGate(dir, phase)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if passed {
		t.Error("should fail with no chapters present")
	}
}

// === phase.go: classifyGateFailure ===

func TestClassifyGateFailure_NotFoundInOutput(t *testing.T) {
	output := classifyGateFailure("nonexistent-cmd", "bash: nonexistent-cmd: not found", os.ErrInvalid)
	if output == "" {
		t.Error("expected non-empty output")
	}
}

func TestClassifyGateFailure_PathError(t *testing.T) {
	result := classifyGateFailure("foo", "", &os.PathError{Op: "stat", Path: "/foo", Err: os.ErrNotExist})
	if result == "" {
		t.Error("expected non-empty result for path error")
	}
}

func TestClassifyGateFailure_GenericError(t *testing.T) {
	output := classifyGateFailure("cmd", "some failure", os.ErrInvalid)
	if output == "" {
		t.Error("expected non-empty output")
	}
}

// === phase.go: buildPhaseGoOpts ===

func TestBuildPhaseGoOpts_Basic(t *testing.T) {
	phase := Phase{ID: "p1", Name: "Phase 1", Description: "Do the thing", Gate: PhaseGate{TestCmd: "go test"}}
	meta := SpecMetadata{Title: "Test Spec"}
	global := GoSpecOpts{MaxParallel: 4, Interval: 10, Review: true, RepoMap: true, BaseBranch: "dev", Runtime: "local"}
	opts := buildPhaseGoOpts(phase, meta, global, false)

	if opts.PhaseID != "p1" {
		t.Errorf("PhaseID = %q", opts.PhaseID)
	}
	if opts.TestCmd != "" {
		t.Error("TestCmd should be empty (gate runs post-Go)")
	}
	if opts.MaxParallel != 4 {
		t.Errorf("MaxParallel = %d", opts.MaxParallel)
	}
	if !opts.Review {
		t.Error("Review should be true")
	}
	if opts.Reconcile {
		t.Error("Reconcile should be false for non-final phase")
	}
	if !opts.KeepStaging {
		t.Error("KeepStaging should be true for non-final phase")
	}
}

func TestBuildPhaseGoOpts_FinalPhase(t *testing.T) {
	phase := Phase{ID: "p2", Description: "Wrap up"}
	meta := SpecMetadata{Title: "Test"}
	global := GoSpecOpts{Reconcile: true}
	opts := buildPhaseGoOpts(phase, meta, global, true)

	if !opts.Reconcile {
		t.Error("Reconcile should be true for final phase")
	}
	if opts.KeepStaging {
		t.Error("KeepStaging should be false for final phase")
	}
}

func TestBuildPhaseGoOpts_AtomicTasks(t *testing.T) {
	phase := Phase{
		ID:          "p1",
		Description: "test",
		Tasks: []SpecTask{
			{Title: "Task A", Atomic: true},
			{Title: "Task B", Atomic: false},
			{Title: "Task C", Atomic: true},
		},
	}
	opts := buildPhaseGoOpts(phase, SpecMetadata{}, GoSpecOpts{}, false)

	if len(opts.AtomicTasks) != 2 {
		t.Fatalf("AtomicTasks = %d, want 2", len(opts.AtomicTasks))
	}
	if opts.AtomicTasks[0] != "Task A" || opts.AtomicTasks[1] != "Task C" {
		t.Errorf("AtomicTasks = %v", opts.AtomicTasks)
	}
}

func TestBuildPhaseGoOpts_SandboxPropagation(t *testing.T) {
	phase := Phase{ID: "p1", Description: "test"}
	opts := buildPhaseGoOpts(phase, SpecMetadata{}, GoSpecOpts{Sandbox: true}, false)
	if !opts.Sandbox {
		t.Error("Sandbox should propagate from global opts")
	}
}

// === phase.go: aggregateResults ===

func TestAggregateResults_Multiple(t *testing.T) {
	result := &GoSpecResult{
		PhaseResults: []PhaseResult{
			{GoResult: &GoResult{DoneCount: 3, FailedCount: 1}},
			{GoResult: &GoResult{DoneCount: 2, FailedCount: 0}},
			{GoResult: nil}, // skipped phase
		},
	}
	aggregateResults(result)
	if result.TotalDone != 5 {
		t.Errorf("TotalDone = %d, want 5", result.TotalDone)
	}
	if result.TotalFailed != 1 {
		t.Errorf("TotalFailed = %d, want 1", result.TotalFailed)
	}
}

func TestAggregateResults_Empty(t *testing.T) {
	result := &GoSpecResult{}
	aggregateResults(result)
	if result.TotalDone != 0 || result.TotalFailed != 0 {
		t.Errorf("expected zeros, got done=%d failed=%d", result.TotalDone, result.TotalFailed)
	}
}

// === phase.go: inferGlobPattern ===

func TestInferGlobPattern(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"chapters", "chapters/*.md"},
		{"chapter", "chapters/*.md"},
		{"research files", "research/*.md"},
		{"research", "research/*.md"},
		{"lessons", "lessons/*.md"},
		{"slides", "slides/*.md"},
		{"outlines", "outlines/*.md"},
		{"specs", "specs/*.md"},
		{"diagrams", "diagrams/*"},
		{"figures", "figures/*"},
		{"files", "**/*.md"},
		{"unknowntype", ""},
		{"syllabi", "syllabi/*.md"},
	}
	for _, tt := range tests {
		got := inferGlobPattern(tt.input)
		if got != tt.want {
			t.Errorf("inferGlobPattern(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

// === phase.go: evaluateAcceptanceCriterion ===

func TestEvaluateAcceptanceCriterion_GlobPattern(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "research"), 0o755)
	os.WriteFile(filepath.Join(dir, "research", "doc1.md"), []byte("x"), 0o644)
	os.WriteFile(filepath.Join(dir, "research", "doc2.md"), []byte("x"), 0o644)

	passed, detail := evaluateAcceptanceCriterion(dir, "research/*.md files exist")
	if !passed {
		t.Errorf("should pass, detail: %s", detail)
	}
}

func TestEvaluateAcceptanceCriterion_NoMatch(t *testing.T) {
	dir := t.TempDir()
	passed, detail := evaluateAcceptanceCriterion(dir, "nonexistent/*.md files exist")
	if passed {
		t.Errorf("should fail, detail: %s", detail)
	}
}

func TestEvaluateAcceptanceCriterion_Unparseable(t *testing.T) {
	passed, detail := evaluateAcceptanceCriterion("/tmp", "the code should be well-tested")
	if !passed {
		t.Errorf("unparseable criterion should auto-pass, detail: %s", detail)
	}
}

func TestEvaluateAcceptanceCriterion_AtLeast(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "research"), 0o755)
	for i := 0; i < 5; i++ {
		os.WriteFile(filepath.Join(dir, "research", string(rune('a'+i))+".md"), []byte("x"), 0o644)
	}
	passed, _ := evaluateAcceptanceCriterion(dir, "at least 3 research files exist")
	if !passed {
		t.Error("should pass with 5 research files")
	}
}

// === mergequeue.go: areDependenciesMerged ===

func TestAreDependenciesMerged_EmptyDeps(t *testing.T) {
	d := setupTestDB(t)
	c, _ := New(ConductorOpts{DB: d, RepoRoot: "/tmp/test"})
	ctx := context.Background()

	if !c.areDependenciesMerged(ctx, "cond-1", "") {
		t.Error("empty deps should be merged")
	}
	if !c.areDependenciesMerged(ctx, "cond-1", "[]") {
		t.Error("empty array deps should be merged")
	}
}

func TestAreDependenciesMerged_DepsNotInQueue(t *testing.T) {
	d := setupTestDB(t)
	c, _ := New(ConductorOpts{DB: d, RepoRoot: "/tmp/test"})
	ctx := context.Background()

	if c.areDependenciesMerged(ctx, "cond-1", `["T-001"]`) {
		t.Error("should return false when dep not in queue")
	}
}

func TestAreDependenciesMerged_DepMerged(t *testing.T) {
	d := setupTestDB(t)
	c, _ := New(ConductorOpts{DB: d, RepoRoot: "/tmp/test"})
	ctx := context.Background()

	d.CreateMergeQueueEntry(ctx, db.MergeQueueEntry{
		ID: "mq-1", ConductorID: "cond-1", TaskID: "T-001", Branch: "feature/T-001", Status: "merged",
	})

	if !c.areDependenciesMerged(ctx, "cond-1", `["T-001"]`) {
		t.Error("should return true when dep is merged")
	}
}

func TestAreDependenciesMerged_DepPending(t *testing.T) {
	d := setupTestDB(t)
	c, _ := New(ConductorOpts{DB: d, RepoRoot: "/tmp/test"})
	ctx := context.Background()

	d.CreateMergeQueueEntry(ctx, db.MergeQueueEntry{
		ID: "mq-1", ConductorID: "cond-1", TaskID: "T-001", Branch: "feature/T-001", Status: "pending",
	})

	if c.areDependenciesMerged(ctx, "cond-1", `["T-001"]`) {
		t.Error("should return false when dep is pending")
	}
}

func TestAreDependenciesMerged_MultipleDeps(t *testing.T) {
	d := setupTestDB(t)
	c, _ := New(ConductorOpts{DB: d, RepoRoot: "/tmp/test"})
	ctx := context.Background()

	d.CreateMergeQueueEntry(ctx, db.MergeQueueEntry{
		ID: "mq-1", ConductorID: "cond-1", TaskID: "T-001", Branch: "feature/T-001", Status: "merged",
	})
	d.CreateMergeQueueEntry(ctx, db.MergeQueueEntry{
		ID: "mq-2", ConductorID: "cond-1", TaskID: "T-002", Branch: "feature/T-002", Status: "pending",
	})

	if c.areDependenciesMerged(ctx, "cond-1", `["T-001","T-002"]`) {
		t.Error("should return false when not all deps merged")
	}
}

// === mergequeue.go: isMergeQueueComplete ===

func TestIsMergeQueueComplete_NoTasks(t *testing.T) {
	d := setupTestDB(t)
	c, _ := New(ConductorOpts{DB: d, RepoRoot: "/tmp/test"})
	ctx := context.Background()

	if !c.isMergeQueueComplete(ctx, c.SessionID) {
		t.Error("should be complete when no tasks")
	}
}

func TestIsMergeQueueComplete_ActiveTasks(t *testing.T) {
	d := setupTestDB(t)
	c, _ := New(ConductorOpts{DB: d, RepoRoot: "/tmp/test"})
	ctx := context.Background()

	// FK constraint requires a conductor record before tasks with conductor_id
	ensureConductorRecord(t, d, c.SessionID)
	if err := d.CreateTask(ctx, db.Task{ID: "T-001", Title: "t1", Status: "running", Role: "implementer",
		ConductorID: sql.NullString{String: c.SessionID, Valid: true}}); err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	d.SetBlackboard(ctx, "conductor:task_ids", `["T-001"]`, "test")

	if c.isMergeQueueComplete(ctx, c.SessionID) {
		t.Error("should not be complete with running tasks")
	}
}

func TestIsMergeQueueComplete_AllDoneQueueDrained(t *testing.T) {
	d := setupTestDB(t)
	c, _ := New(ConductorOpts{DB: d, RepoRoot: "/tmp/test"})
	ctx := context.Background()

	ensureConductorRecord(t, d, c.SessionID)
	if err := d.CreateTask(ctx, db.Task{ID: "T-001", Title: "t1", Status: "done", Role: "implementer",
		ConductorID: sql.NullString{String: c.SessionID, Valid: true}}); err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	d.SetBlackboard(ctx, "conductor:task_ids", `["T-001"]`, "test")
	d.CreateMergeQueueEntry(ctx, db.MergeQueueEntry{
		ID: "mq-1", ConductorID: c.SessionID, TaskID: "T-001", Branch: "feature/T-001", Status: "merged",
	})

	if !c.isMergeQueueComplete(ctx, c.SessionID) {
		t.Error("should be complete when all tasks done and queue drained")
	}
}

func TestIsMergeQueueComplete_QueuePending(t *testing.T) {
	d := setupTestDB(t)
	c, _ := New(ConductorOpts{DB: d, RepoRoot: "/tmp/test"})
	ctx := context.Background()

	ensureConductorRecord(t, d, c.SessionID)
	if err := d.CreateTask(ctx, db.Task{ID: "T-001", Title: "t1", Status: "done", Role: "implementer",
		ConductorID: sql.NullString{String: c.SessionID, Valid: true}}); err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	d.SetBlackboard(ctx, "conductor:task_ids", `["T-001"]`, "test")
	d.CreateMergeQueueEntry(ctx, db.MergeQueueEntry{
		ID: "mq-1", ConductorID: c.SessionID, TaskID: "T-001", Branch: "feature/T-001", Status: "pending",
	})

	if c.isMergeQueueComplete(ctx, c.SessionID) {
		t.Error("should not be complete with pending queue entries")
	}
}

func TestIsMergeQueueComplete_WithActiveWaiters(t *testing.T) {
	d := setupTestDB(t)
	c, _ := New(ConductorOpts{DB: d, RepoRoot: "/tmp/test"})
	ctx := context.Background()

	ensureConductorRecord(t, d, c.SessionID)
	if err := d.CreateTask(ctx, db.Task{ID: "T-001", Title: "t1", Status: "failed", Role: "implementer",
		ConductorID: sql.NullString{String: c.SessionID, Valid: true}}); err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	d.SetBlackboard(ctx, "conductor:task_ids", `["T-001"]`, "test")
	d.SetBlackboard(ctx, "wait_until:T-001", "2099-01-01T00:00:00Z", "monitor")

	if c.isMergeQueueComplete(ctx, c.SessionID) {
		t.Error("should not be complete with active waiters")
	}
}

// === mergequeue.go: shouldRefineMergeEntry ===

func TestShouldRefineMergeEntry(t *testing.T) {
	d := setupTestDB(t)
	c, _ := New(ConductorOpts{DB: d, RepoRoot: "/tmp/test"})
	ctx := context.Background()

	entry := &db.MergeQueueEntry{RefinementCount: 0}
	if !c.shouldRefineMergeEntry(ctx, entry) {
		t.Error("should allow refinement at count 0")
	}
	entry.RefinementCount = 1
	if !c.shouldRefineMergeEntry(ctx, entry) {
		t.Error("should allow refinement at count 1")
	}
	entry.RefinementCount = 2
	if c.shouldRefineMergeEntry(ctx, entry) {
		t.Error("should NOT allow refinement at count 2")
	}
}

// === mergequeue.go: hasActiveWaiters ===

func TestHasActiveWaiters_NoWaiters(t *testing.T) {
	d := setupTestDB(t)
	c, _ := New(ConductorOpts{DB: d, RepoRoot: "/tmp/test"})
	ctx := context.Background()

	if c.hasActiveWaiters(ctx, []string{"T-001"}) {
		t.Error("should not have active waiters")
	}
}

func TestHasActiveWaiters_WithWaiter(t *testing.T) {
	d := setupTestDB(t)
	c, _ := New(ConductorOpts{DB: d, RepoRoot: "/tmp/test"})
	ctx := context.Background()

	d.SetBlackboard(ctx, "wait_until:T-001", "2099-01-01T00:00:00Z", "monitor")

	if !c.hasActiveWaiters(ctx, []string{"T-001"}) {
		t.Error("should have active waiter for T-001")
	}
}

func TestHasActiveWaiters_WaiterForOtherTask(t *testing.T) {
	d := setupTestDB(t)
	c, _ := New(ConductorOpts{DB: d, RepoRoot: "/tmp/test"})
	ctx := context.Background()

	d.SetBlackboard(ctx, "wait_until:T-999", "2099-01-01T00:00:00Z", "monitor")

	if c.hasActiveWaiters(ctx, []string{"T-001"}) {
		t.Error("should not have waiter for T-001")
	}
}

// === conductor.go: IsGitRepo ===

func TestIsGitRepo_True(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, ".git"), 0o755)
	if !IsGitRepo(dir) {
		t.Error("should detect .git directory")
	}
}

func TestIsGitRepo_False(t *testing.T) {
	dir := t.TempDir()
	if IsGitRepo(dir) {
		t.Error("should not detect git repo in empty dir")
	}
}

// === conductor.go: progress callback ===

func TestProgressCallback(t *testing.T) {
	d := setupTestDB(t)
	c, _ := New(ConductorOpts{DB: d, RepoRoot: "/tmp/test"})

	var captured []string
	c.ProgressFunc = func(phase, detail string) {
		captured = append(captured, phase+":"+detail)
	}
	c.Log = func(string) {}
	c.progress("test-phase", "test-detail")

	if len(captured) != 1 {
		t.Fatalf("expected 1 callback, got %d", len(captured))
	}
	if captured[0] != "test-phase:test-detail" {
		t.Errorf("captured = %q", captured[0])
	}
}

func TestProgressCallback_Nil(t *testing.T) {
	d := setupTestDB(t)
	c, _ := New(ConductorOpts{DB: d, RepoRoot: "/tmp/test"})
	c.Log = func(string) {}
	c.progress("phase", "detail") // should not panic
}

// === conductor.go: ConductorLogCh ===

func TestConductorLogChannel(t *testing.T) {
	d := setupTestDB(t)
	c, _ := New(ConductorOpts{DB: d, RepoRoot: "/tmp/test"})

	ch := make(chan string, 10)
	c.ConductorLogCh = ch
	c.Log = func(string) {}

	c.log("test message %d", 42)

	select {
	case msg := <-ch:
		if msg != "test message 42" {
			t.Errorf("got %q, want 'test message 42'", msg)
		}
	default:
		t.Error("expected message on ConductorLogCh")
	}
}

// === phase.go: GoSpec with mock runner ===

func TestGoSpec_SinglePhase_NoneGate(t *testing.T) {
	spec := &OrchestraSpec{
		Metadata: SpecMetadata{Title: "Test Spec"},
		Phases: []Phase{
			{ID: "p1", Name: "Phase 1", Description: "Do something", Gate: PhaseGate{Mode: "none"}},
		},
	}

	runner := &integTestGoRunner{
		results: map[string]*GoResult{"p1": {SessionID: "s-1", TaskCount: 2, DoneCount: 2}},
	}

	result, err := GoSpec(context.Background(), spec, GoSpecOpts{}, runner)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.TotalDone != 2 {
		t.Errorf("TotalDone = %d, want 2", result.TotalDone)
	}
	if !result.PhaseResults[0].GatePassed {
		t.Error("gate should pass with mode=none")
	}
}

func TestGoSpec_MultiPhase_Order(t *testing.T) {
	spec := &OrchestraSpec{
		Metadata: SpecMetadata{Title: "Multi-Phase"},
		Phases: []Phase{
			{ID: "p1", Description: "First", Gate: PhaseGate{Mode: "none"}},
			{ID: "p2", Description: "Second", Gate: PhaseGate{Mode: "none"}, DependsOn: []string{"p1"}},
		},
	}

	var order []string
	runner := &integTestGoRunner{
		results: map[string]*GoResult{
			"p1": {SessionID: "s-1", DoneCount: 1},
			"p2": {SessionID: "s-2", DoneCount: 1},
		},
		onRun: func(opts GoOpts) { order = append(order, opts.PhaseID) },
	}

	_, err := GoSpec(context.Background(), spec, GoSpecOpts{}, runner)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(order) != 2 || order[0] != "p1" || order[1] != "p2" {
		t.Errorf("execution order = %v, want [p1, p2]", order)
	}
}

func TestGoSpec_StartPhase_SkipsPrior(t *testing.T) {
	spec := &OrchestraSpec{
		Metadata: SpecMetadata{Title: "Skip"},
		Phases: []Phase{
			{ID: "p1", Description: "First", Gate: PhaseGate{Mode: "none"}},
			{ID: "p2", Description: "Second", Gate: PhaseGate{Mode: "none"}, DependsOn: []string{"p1"}},
		},
	}

	runner := &integTestGoRunner{
		results: map[string]*GoResult{"p2": {SessionID: "s-2", DoneCount: 3}},
	}

	result, err := GoSpec(context.Background(), spec, GoSpecOpts{StartPhase: "p2", Force: true}, runner)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.PhaseResults[0].Skipped {
		t.Error("p1 should be skipped")
	}
	if result.PhaseResults[1].Skipped {
		t.Error("p2 should not be skipped")
	}
}

func TestGoSpec_InvalidSpec(t *testing.T) {
	spec := &OrchestraSpec{}
	_, err := GoSpec(context.Background(), spec, GoSpecOpts{}, &integTestGoRunner{})
	if err == nil {
		t.Fatal("expected error for empty spec")
	}
}

func TestGoSpec_InvalidStartPhase(t *testing.T) {
	spec := &OrchestraSpec{
		Metadata: SpecMetadata{Title: "Test"},
		Phases:   []Phase{{ID: "p1", Description: "test"}},
	}
	_, err := GoSpec(context.Background(), spec, GoSpecOpts{StartPhase: "nonexistent"}, &integTestGoRunner{})
	if err == nil {
		t.Fatal("expected error for invalid start phase")
	}
}

func TestGoSpec_GateFail_TestCmd(t *testing.T) {
	spec := &OrchestraSpec{
		Metadata: SpecMetadata{Title: "Gate Fail"},
		Phases:   []Phase{{ID: "p1", Description: "test", Gate: PhaseGate{Mode: "test", TestCmd: "false"}}},
	}

	runner := &integTestGoRunner{
		results: map[string]*GoResult{"p1": {SessionID: "s-1", DoneCount: 1}},
	}

	_, err := GoSpec(context.Background(), spec, GoSpecOpts{RepoRoot: t.TempDir()}, runner)
	if err == nil {
		t.Fatal("expected error for gate failure")
	}
}

func TestGoSpec_ContextCancelled_Phase(t *testing.T) {
	spec := &OrchestraSpec{
		Metadata: SpecMetadata{Title: "Cancel"},
		Phases:   []Phase{{ID: "p1", Description: "test", Gate: PhaseGate{Mode: "none"}}},
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := GoSpec(ctx, spec, GoSpecOpts{}, &integTestGoRunner{})
	if err == nil {
		t.Fatal("expected error for cancelled context")
	}
}

func TestGoSpec_PhaseRunnerError(t *testing.T) {
	spec := &OrchestraSpec{
		Metadata: SpecMetadata{Title: "Error"},
		Phases:   []Phase{{ID: "p1", Description: "test", Gate: PhaseGate{Mode: "none"}}},
	}

	runner := &integTestGoRunner{
		errs: map[string]error{"p1": os.ErrInvalid},
	}

	_, err := GoSpec(context.Background(), spec, GoSpecOpts{}, runner)
	if err == nil {
		t.Fatal("expected error when runner fails")
	}
}

// === unblockReadyEntries ===

func TestUnblockReadyEntries(t *testing.T) {
	d := setupTestDB(t)
	c, _ := New(ConductorOpts{DB: d, RepoRoot: "/tmp/test"})
	ctx := context.Background()

	d.CreateTask(ctx, db.Task{
		ID: "T-001", Title: "t1", Status: "done", Role: "implementer",
		Branch: sql.NullString{String: "feature/T-001", Valid: true},
	})
	d.CreateTask(ctx, db.Task{
		ID: "T-002", Title: "t2", Status: "done", Role: "implementer",
		Branch:    sql.NullString{String: "feature/T-002", Valid: true},
		BlockedBy: sql.NullString{String: `["T-001"]`, Valid: true},
	})

	d.SetBlackboard(ctx, "conductor:task_ids", `["T-001","T-002"]`, "test")

	d.CreateMergeQueueEntry(ctx, db.MergeQueueEntry{
		ID: "mq-1", ConductorID: c.SessionID, TaskID: "T-001", Branch: "feature/T-001", Status: "merged",
	})
	d.CreateMergeQueueEntry(ctx, db.MergeQueueEntry{
		ID: "mq-2", ConductorID: c.SessionID, TaskID: "T-002", Branch: "feature/T-002", Status: "blocked",
	})

	c.unblockReadyEntries(ctx, c.SessionID)

	entry, err := d.GetMergeQueueEntryByTaskID(ctx, c.SessionID, "T-002")
	if err != nil {
		t.Fatalf("getting entry: %v", err)
	}
	if entry.Status != "pending" {
		t.Errorf("T-002 status = %q, want 'pending'", entry.Status)
	}
}

// === phase.go: retryableFailureTypes ===

func TestRetryableFailureTypes(t *testing.T) {
	retryable := []string{"stuck_killed", "rate_limit", "session_limit", "context_exhausted"}
	for _, ft := range retryable {
		if !retryableFailureTypes[ft] {
			t.Errorf("%q should be retryable", ft)
		}
	}
	nonRetryable := []string{"normal_failure", "unknown", ""}
	for _, ft := range nonRetryable {
		if retryableFailureTypes[ft] {
			t.Errorf("%q should NOT be retryable", ft)
		}
	}
}

// === mergequeue.go: MergeStrategy constants ===

func TestMergeStrategyConstants(t *testing.T) {
	if MergeStrategyBatch != "batch" {
		t.Errorf("MergeStrategyBatch = %q", MergeStrategyBatch)
	}
	if MergeStrategyFIFO != "fifo" {
		t.Errorf("MergeStrategyFIFO = %q", MergeStrategyFIFO)
	}
}

// --- integTestGoRunner: mock GoRunner for GoSpec tests ---

type integTestGoRunner struct {
	results map[string]*GoResult
	errs    map[string]error
	onRun   func(GoOpts)
}

func (r *integTestGoRunner) RunPhase(_ context.Context, opts GoOpts) (*GoResult, error) {
	if r.onRun != nil {
		r.onRun(opts)
	}
	if r.errs != nil {
		if e, ok := r.errs[opts.PhaseID]; ok {
			return nil, e
		}
	}
	if r.results == nil {
		return &GoResult{SessionID: "mock"}, nil
	}
	result, ok := r.results[opts.PhaseID]
	if !ok {
		return &GoResult{SessionID: "mock"}, nil
	}
	return result, nil
}
