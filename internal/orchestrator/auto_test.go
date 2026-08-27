package orchestrator

import (
	"context"
	"testing"
)

func TestParseAutoSources(t *testing.T) {
	tests := []struct {
		input string
		want  int
	}{
		{"", 0},
		{"audit", 1},
		{"audit,markers", 2},
		{"audit, markers, gaps", 3},
		{" audit , markers ", 2},
	}
	for _, tt := range tests {
		got := parseAutoSources(tt.input)
		if len(got) != tt.want {
			t.Errorf("parseAutoSources(%q) len = %d, want %d", tt.input, len(got), tt.want)
		}
	}
}

func TestParseAutoSources_Values(t *testing.T) {
	got := parseAutoSources("audit, markers, gaps")
	if len(got) != 3 || got[0] != "audit" || got[1] != "markers" || got[2] != "gaps" {
		t.Errorf("unexpected values: %v", got)
	}
}

func TestDiscoverWork_StartingGoal(t *testing.T) {
	d := setupTestDB(t)
	c, _ := New(ConductorOpts{DB: d, RepoRoot: "/tmp/test"})
	ctx := context.Background()

	goal := c.discoverWork(ctx, []string{"audit"}, "Fix the auth bug")
	if goal != "Fix the auth bug" {
		t.Errorf("expected starting goal, got %q", goal)
	}
}

func TestDiscoverWork_NoGoalNoFindings(t *testing.T) {
	d := setupTestDB(t)
	c, _ := New(ConductorOpts{DB: d, RepoRoot: "/tmp/nonexistent-auto-test"})
	ctx := context.Background()

	// Use only audit and markers sources (gaps produces false findings from grep errors)
	goal := c.discoverWork(ctx, []string{"audit", "markers"}, "")
	if goal != "" {
		t.Errorf("expected empty, got %q", goal)
	}
}

func TestAuto_StopSignal(t *testing.T) {
	d := setupTestDB(t)
	c, _ := New(ConductorOpts{DB: d, RepoRoot: "/tmp/test"})
	ctx := context.Background()

	// Set stop signal before running
	d.SetBlackboard(ctx, "auto:stop", "1", "test")

	result, err := c.Auto(ctx, AutoOpts{
		Goal:      "test goal",
		MaxCycles: 5,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.StopReason != "stopped" {
		t.Errorf("StopReason = %q, want 'stopped'", result.StopReason)
	}
	if result.CyclesRun != 0 {
		t.Errorf("CyclesRun = %d, want 0", result.CyclesRun)
	}
}

func TestAuto_EmptyCyclesBreaker(t *testing.T) {
	d := setupTestDB(t)
	c, _ := New(ConductorOpts{DB: d, RepoRoot: "/tmp/nonexistent-auto-test"})
	ctx := context.Background()

	result, err := c.Auto(ctx, AutoOpts{
		Sources:   "audit",
		MaxCycles: 10,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.StopReason != "empty_cycles" {
		t.Errorf("StopReason = %q, want 'empty_cycles'", result.StopReason)
	}
	if result.GoalsFound != 0 {
		t.Errorf("GoalsFound = %d, want 0", result.GoalsFound)
	}
}

func TestAuto_AllFailBreaker(t *testing.T) {
	d := setupTestDB(t)
	// nil Runner → Go() → Decompose fails → allFail every cycle
	// Starting goal is reused on failure (opts.Goal only cleared on success)
	c, _ := New(ConductorOpts{DB: d, RepoRoot: "/tmp/test"})
	ctx := context.Background()

	result, err := c.Auto(ctx, AutoOpts{
		Goal:      "test goal",
		MaxCycles: 10,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.StopReason != "all_fail" {
		t.Errorf("StopReason = %q, want 'all_fail'", result.StopReason)
	}
	if result.GoalsFound != maxConsecutiveAllFail {
		t.Errorf("GoalsFound = %d, want %d", result.GoalsFound, maxConsecutiveAllFail)
	}
	if result.TasksFailed != maxConsecutiveAllFail {
		t.Errorf("TasksFailed = %d, want %d", result.TasksFailed, maxConsecutiveAllFail)
	}
}

func TestAuto_MaxCyclesReached(t *testing.T) {
	d := setupTestDB(t)
	c, _ := New(ConductorOpts{DB: d, RepoRoot: "/tmp/nonexistent-auto-test"})
	ctx := context.Background()

	// maxCycles=1: one empty cycle, then loop exits before empty breaker (needs 2)
	result, err := c.Auto(ctx, AutoOpts{
		Sources:   "audit",
		MaxCycles: 1,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.StopReason != "max_cycles" {
		t.Errorf("StopReason = %q, want 'max_cycles'", result.StopReason)
	}
}

func TestAuto_DefaultsApplied(t *testing.T) {
	d := setupTestDB(t)
	c, _ := New(ConductorOpts{DB: d, RepoRoot: "/tmp/nonexistent-auto-test"})
	ctx := context.Background()

	// No MaxCycles or Sources set → defaults applied
	// Default sources include "gaps" which finds false positives from grep errors,
	// causing all_fail (nil Runner) rather than empty_cycles
	result, err := c.Auto(ctx, AutoOpts{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Verify it completed with a valid stop reason (defaults were applied)
	if result.StopReason == "" {
		t.Error("StopReason should be set")
	}
	if result.Duration <= 0 {
		t.Error("Duration should be positive")
	}
}

func TestAuto_ResultDuration(t *testing.T) {
	d := setupTestDB(t)
	c, _ := New(ConductorOpts{DB: d, RepoRoot: "/tmp/nonexistent-auto-test"})
	ctx := context.Background()

	result, err := c.Auto(ctx, AutoOpts{
		Sources:   "audit",
		MaxCycles: 1,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Duration <= 0 {
		t.Error("Duration should be positive")
	}
}

func TestAuto_GoalFoundCounting(t *testing.T) {
	d := setupTestDB(t)
	// nil Runner with starting goal → goal found each cycle until allFail
	c, _ := New(ConductorOpts{DB: d, RepoRoot: "/tmp/test"})
	ctx := context.Background()

	result, err := c.Auto(ctx, AutoOpts{
		Goal:      "persistent goal",
		MaxCycles: 5,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Goal reused on failure, allFail after 3 cycles
	if result.GoalsFound != 3 {
		t.Errorf("GoalsFound = %d, want 3", result.GoalsFound)
	}
	if result.CyclesRun != 0 {
		t.Errorf("CyclesRun = %d, want 0 (all failed, none succeeded)", result.CyclesRun)
	}
}

func TestAuto_ContextCancelled(t *testing.T) {
	d := setupTestDB(t)
	c, _ := New(ConductorOpts{DB: d, RepoRoot: "/tmp/test"})
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	result, err := c.Auto(ctx, AutoOpts{
		Goal:      "test",
		MaxCycles: 5,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.StopReason != "timeout" {
		t.Errorf("StopReason = %q, want 'timeout'", result.StopReason)
	}
}
