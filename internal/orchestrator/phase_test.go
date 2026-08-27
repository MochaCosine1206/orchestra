package orchestrator

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"
)

// mockGoRunner records calls and returns preconfigured results.
type mockGoRunner struct {
	calls   []GoOpts
	results []*GoResult
	errors  []error
	idx     int
}

func (m *mockGoRunner) RunPhase(ctx context.Context, opts GoOpts) (*GoResult, error) {
	m.calls = append(m.calls, opts)
	i := m.idx
	m.idx++
	var res *GoResult
	if i < len(m.results) {
		res = m.results[i]
	} else {
		res = &GoResult{DoneCount: 1, TaskCount: 1}
	}
	var err error
	if i < len(m.errors) {
		err = m.errors[i]
	}
	return res, err
}

// helper to build a minimal valid spec with N linear phases.
func linearSpec(n int) *OrchestraSpec {
	spec := &OrchestraSpec{
		Version:  "1",
		Metadata: SpecMetadata{Title: "Test Spec"},
	}
	for i := 1; i <= n; i++ {
		p := Phase{
			ID:          fmt.Sprintf("phase-%d", i),
			Name:        fmt.Sprintf("Phase %d", i),
			Description: fmt.Sprintf("Do phase %d work", i),
			Gate: PhaseGate{
				TestCmd:    "echo ok",
				Acceptance: []string{"acceptance criterion"},
			},
			Tasks: []SpecTask{
				{
					Title:              fmt.Sprintf("Task %d", i),
					Role:               "implementer",
					Files:              []string{fmt.Sprintf("file%d.go", i)},
					Description:        fmt.Sprintf("Implement task %d", i),
					AcceptanceCriteria: []string{"it works"},
				},
			},
		}
		if i > 1 {
			p.DependsOn = []string{fmt.Sprintf("phase-%d", i-1)}
		}
		spec.Phases = append(spec.Phases, p)
	}
	return spec
}

// --- Core Loop Tests ---

func TestGoSpec_SinglePhase(t *testing.T) {
	runner := &mockGoRunner{
		results: []*GoResult{{DoneCount: 2, TaskCount: 2, Duration: time.Second}},
	}
	spec := linearSpec(1)

	result, err := GoSpec(context.Background(), spec, GoSpecOpts{}, runner)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.PhaseResults) != 1 {
		t.Fatalf("expected 1 phase result, got %d", len(result.PhaseResults))
	}
	if !result.PhaseResults[0].GatePassed {
		t.Error("expected gate to pass")
	}
	if result.TotalDone != 2 {
		t.Errorf("TotalDone = %d, want 2", result.TotalDone)
	}
	if result.SpecTitle != "Test Spec" {
		t.Errorf("SpecTitle = %q, want %q", result.SpecTitle, "Test Spec")
	}
	if len(runner.calls) != 1 {
		t.Errorf("expected 1 Go() call, got %d", len(runner.calls))
	}
}

func TestGoSpec_ThreePhases(t *testing.T) {
	runner := &mockGoRunner{
		results: []*GoResult{
			{DoneCount: 1, TaskCount: 1, Duration: time.Second},
			{DoneCount: 2, TaskCount: 2, Duration: 2 * time.Second},
			{DoneCount: 3, TaskCount: 3, Duration: 3 * time.Second},
		},
	}
	spec := linearSpec(3)

	result, err := GoSpec(context.Background(), spec, GoSpecOpts{}, runner)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.PhaseResults) != 3 {
		t.Fatalf("expected 3 phase results, got %d", len(result.PhaseResults))
	}
	for i, pr := range result.PhaseResults {
		if !pr.GatePassed {
			t.Errorf("phase %d: expected gate to pass", i)
		}
		if pr.Skipped {
			t.Errorf("phase %d: expected not skipped", i)
		}
	}
	if result.TotalDone != 6 {
		t.Errorf("TotalDone = %d, want 6", result.TotalDone)
	}
	if result.TotalFailed != 0 {
		t.Errorf("TotalFailed = %d, want 0", result.TotalFailed)
	}
	if len(runner.calls) != 3 {
		t.Errorf("expected 3 Go() calls, got %d", len(runner.calls))
	}
}

func TestGoSpec_GateFailure(t *testing.T) {
	runner := &mockGoRunner{
		results: []*GoResult{
			{DoneCount: 1, TaskCount: 1},
			{DoneCount: 1, TaskCount: 1}, // phase 2 Go() succeeds, but gate will fail
		},
	}
	spec := linearSpec(3)
	// Make phase 2's gate fail — use a command that will exit non-zero
	spec.Phases[1].Gate.TestCmd = "false"

	result, err := GoSpec(context.Background(), spec, GoSpecOpts{}, runner)
	if err == nil {
		t.Fatal("expected error from gate failure")
	}
	// Error should include gate output (not generic "tests did not pass")
	if strings.Contains(err.Error(), "tests did not pass") {
		t.Errorf("error should include specific gate output, not generic message: %v", err)
	}
	if !strings.Contains(err.Error(), "gate failed") {
		t.Errorf("error should contain 'gate failed': %v", err)
	}
	// Should have 2 phase results (phase 1 passed, phase 2 failed gate)
	if len(result.PhaseResults) != 2 {
		t.Fatalf("expected 2 phase results, got %d", len(result.PhaseResults))
	}
	if !result.PhaseResults[0].GatePassed {
		t.Error("phase 1 gate should have passed")
	}
	if result.PhaseResults[1].GatePassed {
		t.Error("phase 2 gate should have failed")
	}
	// Phase 3 should not have been executed
	if len(runner.calls) != 2 {
		t.Errorf("expected 2 Go() calls (stopped at phase 2), got %d", len(runner.calls))
	}
}

func TestGoSpec_EmptySpec(t *testing.T) {
	runner := &mockGoRunner{}
	spec := &OrchestraSpec{
		Version:  "1",
		Metadata: SpecMetadata{Title: "Empty"},
	}

	_, err := GoSpec(context.Background(), spec, GoSpecOpts{}, runner)
	if err == nil {
		t.Fatal("expected error for empty spec")
	}
}

func TestGoSpec_DryRun(t *testing.T) {
	runner := &mockGoRunner{}
	spec := linearSpec(3)

	result, err := GoSpec(context.Background(), spec, GoSpecOpts{DryRun: true}, runner)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Dry run should not call Go()
	if len(runner.calls) != 0 {
		t.Errorf("expected 0 Go() calls in dry run, got %d", len(runner.calls))
	}
	// Should still have phase results (all skipped)
	if len(result.PhaseResults) != 3 {
		t.Fatalf("expected 3 phase results in dry run, got %d", len(result.PhaseResults))
	}
	for i, pr := range result.PhaseResults {
		if !pr.Skipped {
			t.Errorf("phase %d: expected skipped in dry run", i)
		}
	}
}

// --- StartPhase / Resume Tests ---

func TestGoSpec_StartPhase(t *testing.T) {
	runner := &mockGoRunner{
		results: []*GoResult{
			{DoneCount: 2, TaskCount: 2},
			{DoneCount: 3, TaskCount: 3},
		},
	}
	spec := linearSpec(3)

	result, err := GoSpec(context.Background(), spec, GoSpecOpts{StartPhase: "phase-2"}, runner)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.PhaseResults) != 3 {
		t.Fatalf("expected 3 phase results, got %d", len(result.PhaseResults))
	}
	// Phase 1 should be skipped
	if !result.PhaseResults[0].Skipped {
		t.Error("phase 1 should be skipped")
	}
	// Phase 2 and 3 should execute
	if result.PhaseResults[1].Skipped {
		t.Error("phase 2 should not be skipped")
	}
	if result.PhaseResults[2].Skipped {
		t.Error("phase 3 should not be skipped")
	}
	// Only 2 Go() calls (phases 2 and 3)
	if len(runner.calls) != 2 {
		t.Errorf("expected 2 Go() calls, got %d", len(runner.calls))
	}
}

func TestGoSpec_StartPhaseNotFound(t *testing.T) {
	runner := &mockGoRunner{}
	spec := linearSpec(2)

	_, err := GoSpec(context.Background(), spec, GoSpecOpts{StartPhase: "nonexistent"}, runner)
	if err == nil {
		t.Fatal("expected error for nonexistent start phase")
	}
}

// --- Conversion Tests ---

func TestBuildPhaseGoOpts_Defaults(t *testing.T) {
	phase := Phase{
		ID:          "api",
		Name:        "API Layer",
		Description: "Build the API",
		Gate:        PhaseGate{TestCmd: "make test", Acceptance: []string{"API works"}},
		Tasks: []SpecTask{
			{Title: "Routes", Role: "implementer", Files: []string{"routes.go"}, Description: "Add routes", AcceptanceCriteria: []string{"routes work"}},
			{Title: "Handlers", Role: "implementer", Files: []string{"handlers.go"}, Description: "Add handlers", AcceptanceCriteria: []string{"handlers work"}},
		},
	}
	meta := SpecMetadata{Title: "My Project"}
	global := GoSpecOpts{
		MaxParallel: 4,
		Interval:    5,
		Review:      true,
		RepoMap:     true,
		BaseBranch:  "main",
		Runtime:     "local",
		MergeMode:   "pr",
	}

	opts := buildPhaseGoOpts(phase, meta, global, false)

	if opts.MaxParallel != 4 {
		t.Errorf("MaxParallel = %d, want 4", opts.MaxParallel)
	}
	if opts.Interval != 5 {
		t.Errorf("Interval = %d, want 5", opts.Interval)
	}
	if !opts.Review {
		t.Error("expected Review = true")
	}
	if !opts.RepoMap {
		t.Error("expected RepoMap = true")
	}
	if opts.BaseBranch != "main" {
		t.Errorf("BaseBranch = %q, want %q", opts.BaseBranch, "main")
	}
	if opts.Runtime != "local" {
		t.Errorf("Runtime = %q, want %q", opts.Runtime, "local")
	}
	if opts.MergeMode != "pr" {
		t.Errorf("MergeMode = %q, want %q", opts.MergeMode, "pr")
	}
	if opts.TestCmd != "" {
		t.Errorf("TestCmd = %q, want %q (G123: phase gate runs via checkPhaseGate, not per-task)", opts.TestCmd, "")
	}
	if opts.Clarify {
		t.Error("expected Clarify = false (spec already defines tasks)")
	}
	if !opts.LenientDeps {
		t.Error("expected LenientDeps = true")
	}
	if opts.MaxFilesPerTask != 25 {
		t.Errorf("MaxFilesPerTask = %d, want 25", opts.MaxFilesPerTask)
	}
}

func TestBuildPhaseGoOpts_DefenseAutoEnabled(t *testing.T) {
	phase := Phase{
		ID:          "big",
		Name:        "Big Phase",
		Description: "Lots of tasks",
		Gate:        PhaseGate{TestCmd: "make test", Acceptance: []string{"works"}},
		Tasks: []SpecTask{
			{Title: "T1", Role: "implementer", Files: []string{"a.go"}, Description: "d1", AcceptanceCriteria: []string{"ac"}},
			{Title: "T2", Role: "implementer", Files: []string{"b.go"}, Description: "d2", AcceptanceCriteria: []string{"ac"}},
			{Title: "T3", Role: "implementer", Files: []string{"c.go"}, Description: "d3", AcceptanceCriteria: []string{"ac"}},
		},
	}
	meta := SpecMetadata{}
	opts := buildPhaseGoOpts(phase, meta, GoSpecOpts{}, false)

	if opts.FileEnforcement != "defense" {
		t.Errorf("FileEnforcement = %q, want %q (3+ tasks should auto-enable defense)", opts.FileEnforcement, "defense")
	}
}

func TestBuildPhaseGoOpts_ReconcileOnlyLast(t *testing.T) {
	phase := Phase{
		ID:          "mid",
		Name:        "Mid Phase",
		Description: "Middle",
		Gate:        PhaseGate{TestCmd: "true", Acceptance: []string{"ok"}},
		Tasks: []SpecTask{
			{Title: "T1", Role: "implementer", Files: []string{"a.go"}, Description: "d1", AcceptanceCriteria: []string{"ac"}},
		},
	}
	meta := SpecMetadata{}

	// Non-final phase: reconcile = false
	optsNonFinal := buildPhaseGoOpts(phase, meta, GoSpecOpts{Reconcile: true}, false)
	if optsNonFinal.Reconcile {
		t.Error("non-final phase should have Reconcile = false")
	}

	// Final phase: reconcile mirrors global setting
	optsFinal := buildPhaseGoOpts(phase, meta, GoSpecOpts{Reconcile: true}, true)
	if !optsFinal.Reconcile {
		t.Error("final phase with global Reconcile=true should have Reconcile = true")
	}
}

// --- Gate Tests ---

func TestCheckPhaseGate_Passes(t *testing.T) {
	phase := Phase{Gate: PhaseGate{TestCmd: "true"}}
	passed, output, err := checkPhaseGate(t.TempDir(), phase)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !passed {
		t.Error("expected gate to pass")
	}
	_ = output
}

func TestCheckPhaseGate_Fails(t *testing.T) {
	phase := Phase{Gate: PhaseGate{TestCmd: "echo 'test failed' && exit 1"}}
	passed, output, err := checkPhaseGate(t.TempDir(), phase)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if passed {
		t.Error("expected gate to fail")
	}
	if !strings.Contains(output, "test failed") || !strings.Contains(output, "exit code") {
		t.Errorf("expected output with 'test failed' and 'exit code', got %q", output)
	}
}

func TestCheckPhaseGate_CommandNotFound(t *testing.T) {
	phase := Phase{Gate: PhaseGate{TestCmd: "/nonexistent/binary/xyz"}}
	passed, output, err := checkPhaseGate(t.TempDir(), phase)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if passed {
		t.Error("expected gate to fail for nonexistent command")
	}
	if !strings.Contains(strings.ToLower(output), "not found") {
		t.Errorf("expected output to contain 'not found', got %q", output)
	}
	if strings.Contains(output, "tests did not pass") {
		t.Errorf("should not contain generic 'tests did not pass', got %q", output)
	}
}

func TestCheckPhaseGate_ExitCodeInOutput(t *testing.T) {
	phase := Phase{Gate: PhaseGate{TestCmd: "echo 'some test output' && exit 42"}}
	passed, output, err := checkPhaseGate(t.TempDir(), phase)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if passed {
		t.Error("expected gate to fail")
	}
	if !strings.Contains(output, "exit code 42") {
		t.Errorf("expected output to contain 'exit code 42', got %q", output)
	}
}

func TestCheckPhaseGate_NoTestCmd(t *testing.T) {
	phase := Phase{Gate: PhaseGate{TestCmd: ""}}
	passed, output, err := checkPhaseGate(t.TempDir(), phase)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !passed {
		t.Error("expected auto-pass when no test cmd")
	}
	if output != "no gate defined" {
		t.Errorf("expected 'no gate defined' output, got %q", output)
	}
}

// --- Result Aggregation ---

func TestGoSpecResult_Aggregation(t *testing.T) {
	runner := &mockGoRunner{
		results: []*GoResult{
			{DoneCount: 2, FailedCount: 1, TaskCount: 3, Duration: time.Second},
			{DoneCount: 3, FailedCount: 0, TaskCount: 3, Duration: 2 * time.Second},
		},
	}
	spec := linearSpec(2)

	result, err := GoSpec(context.Background(), spec, GoSpecOpts{}, runner)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.TotalDone != 5 {
		t.Errorf("TotalDone = %d, want 5 (2+3)", result.TotalDone)
	}
	if result.TotalFailed != 1 {
		t.Errorf("TotalFailed = %d, want 1", result.TotalFailed)
	}
}

// --- Context Cancellation ---

func TestGoSpec_ContextCancelled(t *testing.T) {
	runner := &mockGoRunner{
		results: []*GoResult{{DoneCount: 1, TaskCount: 1}},
	}
	spec := linearSpec(3)
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	_, err := GoSpec(ctx, spec, GoSpecOpts{}, runner)
	if err == nil {
		t.Fatal("expected error from cancelled context")
	}
}

// --- GoFunc Error Propagation ---

func TestGoSpec_GoFuncError(t *testing.T) {
	runner := &mockGoRunner{
		results: []*GoResult{{DoneCount: 0, FailedCount: 1, TaskCount: 1}},
		errors:  []error{fmt.Errorf("agent spawn failed")},
	}
	spec := linearSpec(1)

	_, err := GoSpec(context.Background(), spec, GoSpecOpts{}, runner)
	if err == nil {
		t.Fatal("expected error from Go() failure")
	}
}

// --- G110: Phase-Scoped Merge Filtering ---

func TestGoSpec_PhaseIDInGoOpts(t *testing.T) {
	// Verify that buildPhaseGoOpts sets PhaseID from the phase definition.
	runner := &mockGoRunner{
		results: []*GoResult{
			{DoneCount: 1, TaskCount: 1, StagingBranch: "conductor/s1"},
			{DoneCount: 1, TaskCount: 1, StagingBranch: "conductor/s2"},
		},
	}
	spec := linearSpec(2)

	_, err := GoSpec(context.Background(), spec, GoSpecOpts{}, runner)
	if err != nil {
		t.Fatalf("GoSpec failed: %v", err)
	}

	if len(runner.calls) != 2 {
		t.Fatalf("expected 2 calls, got %d", len(runner.calls))
	}

	// Phase 1 should have PhaseID = "phase-1"
	if runner.calls[0].PhaseID != "phase-1" {
		t.Errorf("phase 1 PhaseID = %q, want %q", runner.calls[0].PhaseID, "phase-1")
	}
	// Phase 2 should have PhaseID = "phase-2"
	if runner.calls[1].PhaseID != "phase-2" {
		t.Errorf("phase 2 PhaseID = %q, want %q", runner.calls[1].PhaseID, "phase-2")
	}
}

// --- G111: Cross-Phase Worktree Fork Point ---

func TestGoSpec_RollingBase(t *testing.T) {
	// Verify that Phase 2 gets Phase 1's staging branch as BaseBranch.
	runner := &mockGoRunner{
		results: []*GoResult{
			{DoneCount: 1, TaskCount: 1, StagingBranch: "conductor/phase1-staging"},
			{DoneCount: 1, TaskCount: 1, StagingBranch: "conductor/phase2-staging"},
			{DoneCount: 1, TaskCount: 1, StagingBranch: "conductor/phase3-staging"},
		},
	}
	spec := linearSpec(3)

	_, err := GoSpec(context.Background(), spec, GoSpecOpts{BaseBranch: "dev"}, runner)
	if err != nil {
		t.Fatalf("GoSpec failed: %v", err)
	}

	if len(runner.calls) != 3 {
		t.Fatalf("expected 3 calls, got %d", len(runner.calls))
	}

	// Phase 1 should use the initial base branch ("dev")
	if runner.calls[0].BaseBranch != "dev" {
		t.Errorf("phase 1 BaseBranch = %q, want %q", runner.calls[0].BaseBranch, "dev")
	}
	// Phase 2 should use Phase 1's staging branch
	if runner.calls[1].BaseBranch != "conductor/phase1-staging" {
		t.Errorf("phase 2 BaseBranch = %q, want %q", runner.calls[1].BaseBranch, "conductor/phase1-staging")
	}
	// Phase 3 should use Phase 2's staging branch
	if runner.calls[2].BaseBranch != "conductor/phase2-staging" {
		t.Errorf("phase 3 BaseBranch = %q, want %q", runner.calls[2].BaseBranch, "conductor/phase2-staging")
	}
}

func TestGoSpec_GateRunsInRepoRoot(t *testing.T) {
	// G114: Verify that checkPhaseGate runs in opts.RepoRoot, not process CWD.
	// Create a temp dir with a marker file. The gate command checks for that file.
	// If GoSpec correctly passes RepoRoot, the gate passes. If it uses ".", it fails.
	repoDir := t.TempDir()

	// Create marker file that the gate will look for
	if err := os.WriteFile(repoDir+"/marker.txt", []byte("present"), 0o644); err != nil {
		t.Fatal(err)
	}

	runner := &mockGoRunner{
		results: []*GoResult{
			{DoneCount: 1, TaskCount: 1},
		},
	}

	spec := &OrchestraSpec{
		Version:  "1",
		Metadata: SpecMetadata{Title: "RepoRoot Test"},
		Phases: []Phase{{
			ID:          "check",
			Name:        "Check",
			Description: "Verify gate runs in correct dir",
			Gate: PhaseGate{
				TestCmd:    "test -f marker.txt",
				Acceptance: []string{"marker exists"},
			},
			Tasks: []SpecTask{{
				Title:              "T1",
				Role:               "implementer",
				Files:              []string{"a.go"},
				Description:        "d1",
				AcceptanceCriteria: []string{"ac"},
			}},
		}},
	}

	result, err := GoSpec(context.Background(), spec, GoSpecOpts{RepoRoot: repoDir}, runner)
	if err != nil {
		t.Fatalf("GoSpec failed: %v (gate should pass when RepoRoot points to dir with marker.txt)", err)
	}
	if !result.PhaseResults[0].GatePassed {
		t.Errorf("gate should pass when RepoRoot=%s contains marker.txt, output: %s",
			repoDir, result.PhaseResults[0].GateOutput)
	}
}

// --- G136: Gate Retry Tests ---

// mockGateRetryRunner implements GoRunner and GateRetrier for testing gate retries.
type mockGateRetryRunner struct {
	runResults   []*GoResult // results for RunPhase calls
	runErrors    []error
	retryResults []*GoResult // results for RetryFailedTasks calls
	retryErrors  []error
	runIdx       int
	retryIdx     int
	runCalls     []GoOpts
	retryCalls   []string // session IDs passed to RetryFailedTasks
}

func (m *mockGateRetryRunner) RunPhase(ctx context.Context, opts GoOpts) (*GoResult, error) {
	m.runCalls = append(m.runCalls, opts)
	i := m.runIdx
	m.runIdx++
	var res *GoResult
	if i < len(m.runResults) {
		res = m.runResults[i]
	} else {
		res = &GoResult{DoneCount: 1, TaskCount: 1}
	}
	var err error
	if i < len(m.runErrors) {
		err = m.runErrors[i]
	}
	return res, err
}

func (m *mockGateRetryRunner) RetryFailedTasks(ctx context.Context, sessionID string, opts GoOpts) (*GoResult, error) {
	m.retryCalls = append(m.retryCalls, sessionID)
	i := m.retryIdx
	m.retryIdx++
	var res *GoResult
	if i < len(m.retryResults) {
		res = m.retryResults[i]
	}
	var err error
	if i < len(m.retryErrors) {
		err = m.retryErrors[i]
	}
	return res, err
}

func TestGoSpec_GateRetryRespawnsFailedTasks(t *testing.T) {
	// Phase gate fails on first Go() (1 failed task), retry resets it,
	// second attempt passes the gate.
	repoDir := t.TempDir()

	// Create marker file that gate checks for — simulates "retry fixes the problem"
	// First gate check: marker absent → fail. After retry: marker present → pass.
	markerPath := repoDir + "/ready.txt"

	runner := &mockGateRetryRunner{
		runResults: []*GoResult{
			{DoneCount: 1, FailedCount: 1, TaskCount: 2, SessionID: "sess-1"},
		},
		retryResults: []*GoResult{
			{DoneCount: 2, FailedCount: 0, TaskCount: 2, SessionID: "sess-1"},
		},
	}

	spec := &OrchestraSpec{
		Version:  "1",
		Metadata: SpecMetadata{Title: "Retry Test"},
		Phases: []Phase{{
			ID:          "p1",
			Name:        "Phase 1",
			Description: "Test gate retry",
			Gate: PhaseGate{
				TestCmd:    fmt.Sprintf("test -f %s", markerPath),
				Acceptance: []string{"ready file exists"},
			},
			Tasks: []SpecTask{{
				Title:              "T1",
				Role:               "implementer",
				Files:              []string{"a.go"},
				Description:        "do work",
				AcceptanceCriteria: []string{"works"},
			}},
		}},
	}

	// Gate will fail first (no marker). Simulate retry creating the marker.
	// We use a custom runner where RetryFailedTasks creates the marker file.
	// But since we're using a mock, we need to create the marker before the
	// retry's gate check runs. Trick: override RetryFailedTasks to also create marker.
	// Actually, with a mock we can't do this. Instead, use a gate command that
	// passes on the second call. Use "echo ok" (always passes) for the retry gate.
	// Better approach: create marker before test, use a gate that always passes
	// on second run. Simplest: use "true" as gate and verify retry flow via call counts.

	// Simplest approach: gate always fails, but the retry returns FailedCount=0,
	// which means the retry loop exits. But we want to test the success path.
	// Let's use an approach where the retry runner creates the file.

	// Actually the simplest: pre-create the marker so the gate always passes.
	// The first RunPhase returns FailedCount=1, so GoSpec checks the gate.
	// Gate passes on first check → no retry needed. That doesn't test retry.

	// Best approach: use a temp file counter to make the gate pass on 2nd call.
	counterFile := repoDir + "/counter"
	os.WriteFile(counterFile, []byte("0"), 0o644)

	// Gate script: increments counter, passes only when counter >= 2
	spec.Phases[0].Gate.TestCmd = fmt.Sprintf(
		`c=$(cat %s); c=$((c + 1)); echo $c > %s; test $c -ge 2`,
		counterFile, counterFile,
	)

	result, err := GoSpec(context.Background(), spec, GoSpecOpts{RepoRoot: repoDir}, runner)
	if err != nil {
		t.Fatalf("expected success after retry, got: %v", err)
	}

	// Verify retry was called
	if len(runner.retryCalls) != 1 {
		t.Fatalf("expected 1 retry call, got %d", len(runner.retryCalls))
	}
	if runner.retryCalls[0] != "sess-1" {
		t.Errorf("retry session ID = %q, want %q", runner.retryCalls[0], "sess-1")
	}

	// Phase should have passed after retry
	if !result.PhaseResults[0].GatePassed {
		t.Error("expected gate to pass after retry")
	}

	// GoResult should reflect the retry result
	if result.PhaseResults[0].GoResult.FailedCount != 0 {
		t.Errorf("expected FailedCount=0 after retry, got %d", result.PhaseResults[0].GoResult.FailedCount)
	}
}

func TestGoSpec_GateRetryMaxAttempts(t *testing.T) {
	// Gate always fails, retries are exhausted after maxGateRetries (2).
	repoDir := t.TempDir()

	runner := &mockGateRetryRunner{
		runResults: []*GoResult{
			{DoneCount: 0, FailedCount: 2, TaskCount: 2, SessionID: "sess-2"},
		},
		retryResults: []*GoResult{
			{DoneCount: 0, FailedCount: 1, TaskCount: 2, SessionID: "sess-2"},
			{DoneCount: 0, FailedCount: 1, TaskCount: 2, SessionID: "sess-2"},
		},
	}

	spec := &OrchestraSpec{
		Version:  "1",
		Metadata: SpecMetadata{Title: "Max Retry Test"},
		Phases: []Phase{{
			ID:          "p1",
			Name:        "Phase 1",
			Description: "Test max retries",
			Gate: PhaseGate{
				TestCmd:    "false", // always fails
				Acceptance: []string{"never passes"},
			},
			Tasks: []SpecTask{{
				Title:              "T1",
				Role:               "implementer",
				Files:              []string{"a.go"},
				Description:        "do work",
				AcceptanceCriteria: []string{"works"},
			}},
		}},
	}

	result, err := GoSpec(context.Background(), spec, GoSpecOpts{RepoRoot: repoDir}, runner)
	if err == nil {
		t.Fatal("expected error after max retries exhausted")
	}
	if !strings.Contains(err.Error(), "gate failed") {
		t.Errorf("expected gate failure error, got: %v", err)
	}

	// Should have retried exactly maxGateRetries times
	if len(runner.retryCalls) != maxGateRetries {
		t.Errorf("expected %d retry calls, got %d", maxGateRetries, len(runner.retryCalls))
	}

	// Gate should not have passed
	if result.PhaseResults[0].GatePassed {
		t.Error("expected gate to remain failed after max retries")
	}
}

func TestGoSpec_GateRetryNotCalledWithoutGateRetrier(t *testing.T) {
	// Verify that the basic mockGoRunner (no GateRetrier) doesn't trigger retries.
	runner := &mockGoRunner{
		results: []*GoResult{
			{DoneCount: 0, FailedCount: 1, TaskCount: 1},
		},
	}
	spec := linearSpec(1)
	spec.Phases[0].Gate.TestCmd = "false" // gate fails

	_, err := GoSpec(context.Background(), spec, GoSpecOpts{}, runner)
	if err == nil {
		t.Fatal("expected error from gate failure")
	}
	// Only 1 RunPhase call — no retry
	if len(runner.calls) != 1 {
		t.Errorf("expected 1 Go() call (no retry), got %d", len(runner.calls))
	}
}

func TestGoSpec_GateRetrySkippedWhenNoFailedTasks(t *testing.T) {
	// Gate fails but FailedCount=0 — no retry should be attempted.
	repoDir := t.TempDir()

	runner := &mockGateRetryRunner{
		runResults: []*GoResult{
			{DoneCount: 2, FailedCount: 0, TaskCount: 2, SessionID: "sess-3"},
		},
	}

	spec := &OrchestraSpec{
		Version:  "1",
		Metadata: SpecMetadata{Title: "No Failed Tasks Test"},
		Phases: []Phase{{
			ID:          "p1",
			Name:        "Phase 1",
			Description: "Gate fails but no failed tasks",
			Gate: PhaseGate{
				TestCmd:    "false",
				Acceptance: []string{"fails"},
			},
			Tasks: []SpecTask{{
				Title:              "T1",
				Role:               "implementer",
				Files:              []string{"a.go"},
				Description:        "do work",
				AcceptanceCriteria: []string{"works"},
			}},
		}},
	}

	_, err := GoSpec(context.Background(), spec, GoSpecOpts{RepoRoot: repoDir}, runner)
	if err == nil {
		t.Fatal("expected gate failure error")
	}

	// No retry calls — FailedCount was 0
	if len(runner.retryCalls) != 0 {
		t.Errorf("expected 0 retry calls when FailedCount=0, got %d", len(runner.retryCalls))
	}
}

func TestGoSpec_KeepStagingInterPhase(t *testing.T) {
	// Verify that non-final phases have KeepStaging=true, final has false.
	runner := &mockGoRunner{
		results: []*GoResult{
			{DoneCount: 1, TaskCount: 1, StagingBranch: "conductor/s1"},
			{DoneCount: 1, TaskCount: 1, StagingBranch: "conductor/s2"},
			{DoneCount: 1, TaskCount: 1, StagingBranch: "conductor/s3"},
		},
	}
	spec := linearSpec(3)

	_, err := GoSpec(context.Background(), spec, GoSpecOpts{}, runner)
	if err != nil {
		t.Fatalf("GoSpec failed: %v", err)
	}

	if len(runner.calls) != 3 {
		t.Fatalf("expected 3 calls, got %d", len(runner.calls))
	}

	// Phase 1 and 2 should have KeepStaging=true (non-final)
	if !runner.calls[0].KeepStaging {
		t.Error("phase 1 should have KeepStaging=true (non-final)")
	}
	if !runner.calls[1].KeepStaging {
		t.Error("phase 2 should have KeepStaging=true (non-final)")
	}
	// Phase 3 should have KeepStaging=false (final)
	if runner.calls[2].KeepStaging {
		t.Error("phase 3 should have KeepStaging=false (final)")
	}
}
