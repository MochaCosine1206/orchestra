package orchestrator

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestABTest_EmptyGoal(t *testing.T) {
	d := setupTestDB(t)
	c, _ := New(ConductorOpts{DB: d, RepoRoot: "/tmp/test"})
	ctx := context.Background()

	_, err := c.ABTest(ctx, ABTestOpts{TestCmd: "true"})
	if err == nil {
		t.Fatal("expected error for empty goal")
	}
}

func TestABTest_EmptyTestCmd(t *testing.T) {
	d := setupTestDB(t)
	c, _ := New(ConductorOpts{DB: d, RepoRoot: "/tmp/test"})
	ctx := context.Background()

	_, err := c.ABTest(ctx, ABTestOpts{Goal: "test"})
	if err == nil {
		t.Fatal("expected error for empty test-cmd")
	}
}

func TestABTest_DryRun(t *testing.T) {
	d := setupTestDB(t)
	c, _ := New(ConductorOpts{DB: d, RepoRoot: "/tmp/test"})
	ctx := context.Background()

	result, err := c.ABTest(ctx, ABTestOpts{
		Goal:    "test goal",
		TestCmd: "go test ./...",
		DryRun:  true,
		Runs:    3,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Summary.TotalRuns != 3 {
		t.Errorf("TotalRuns = %d, want 3", result.Summary.TotalRuns)
	}
	if len(result.Runs) != 0 {
		t.Errorf("DryRun should not produce actual runs, got %d", len(result.Runs))
	}
}

func TestABTest_DefaultRuns(t *testing.T) {
	d := setupTestDB(t)
	c, _ := New(ConductorOpts{DB: d, RepoRoot: "/tmp/test"})
	ctx := context.Background()

	result, err := c.ABTest(ctx, ABTestOpts{
		Goal:    "test",
		TestCmd: "true",
		DryRun:  true,
		// Runs defaults to 1
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Summary.TotalRuns != 1 {
		t.Errorf("TotalRuns = %d, want 1", result.Summary.TotalRuns)
	}
}

func TestCompareArms_BothPassed_AFaster(t *testing.T) {
	a := ArmResult{Passed: true, WallClock: 10 * time.Second}
	b := ArmResult{Passed: true, WallClock: 20 * time.Second}

	cmp := compareArms(a, b)
	if cmp.Winner != "arm_a" {
		t.Errorf("Winner = %q, want 'arm_a'", cmp.Winner)
	}
	if cmp.TimeRatio >= 1.0 {
		t.Errorf("TimeRatio = %f, should be < 1 (A faster)", cmp.TimeRatio)
	}
}

func TestCompareArms_BothPassed_BFaster(t *testing.T) {
	a := ArmResult{Passed: true, WallClock: 30 * time.Second}
	b := ArmResult{Passed: true, WallClock: 10 * time.Second}

	cmp := compareArms(a, b)
	if cmp.Winner != "arm_b" {
		t.Errorf("Winner = %q, want 'arm_b'", cmp.Winner)
	}
}

func TestCompareArms_OnlyAPassed(t *testing.T) {
	a := ArmResult{Passed: true, WallClock: 30 * time.Second}
	b := ArmResult{Passed: false, WallClock: 10 * time.Second}

	cmp := compareArms(a, b)
	if cmp.Winner != "arm_a" {
		t.Errorf("Winner = %q, want 'arm_a'", cmp.Winner)
	}
}

func TestCompareArms_OnlyBPassed(t *testing.T) {
	a := ArmResult{Passed: false, WallClock: 10 * time.Second}
	b := ArmResult{Passed: true, WallClock: 30 * time.Second}

	cmp := compareArms(a, b)
	if cmp.Winner != "arm_b" {
		t.Errorf("Winner = %q, want 'arm_b'", cmp.Winner)
	}
}

func TestCompareArms_BothFailed(t *testing.T) {
	a := ArmResult{Passed: false}
	b := ArmResult{Passed: false}

	cmp := compareArms(a, b)
	if cmp.Winner != "both_failed" {
		t.Errorf("Winner = %q, want 'both_failed'", cmp.Winner)
	}
}

func TestCompareArms_Tie(t *testing.T) {
	a := ArmResult{Passed: true, WallClock: 10 * time.Second}
	b := ArmResult{Passed: true, WallClock: 10 * time.Second}

	cmp := compareArms(a, b)
	if cmp.Winner != "tie" {
		t.Errorf("Winner = %q, want 'tie'", cmp.Winner)
	}
}

func TestSummarizeRuns(t *testing.T) {
	runs := []ABTestRun{
		{Comparison: ABComparison{Winner: "arm_a"}},
		{Comparison: ABComparison{Winner: "arm_b"}},
		{Comparison: ABComparison{Winner: "arm_a"}},
		{Comparison: ABComparison{Winner: "both_failed"}},
		{Comparison: ABComparison{Winner: "tie"}},
	}

	s := summarizeRuns(runs)
	if s.TotalRuns != 5 {
		t.Errorf("TotalRuns = %d, want 5", s.TotalRuns)
	}
	if s.ArmAWins != 2 {
		t.Errorf("ArmAWins = %d, want 2", s.ArmAWins)
	}
	if s.ArmBWins != 1 {
		t.Errorf("ArmBWins = %d, want 1", s.ArmBWins)
	}
	if s.Ties != 1 {
		t.Errorf("Ties = %d, want 1", s.Ties)
	}
	if s.BothFailed != 1 {
		t.Errorf("BothFailed = %d, want 1", s.BothFailed)
	}
}

func TestSummarizeRuns_Empty(t *testing.T) {
	s := summarizeRuns(nil)
	if s.TotalRuns != 0 {
		t.Errorf("TotalRuns = %d, want 0", s.TotalRuns)
	}
}

func TestFormatABTestResult(t *testing.T) {
	r := &ABTestResult{
		Runs: []ABTestRun{
			{
				RunNumber:  1,
				ArmA:       ArmResult{Mode: "multi-agent", Passed: true, WallClock: 10 * time.Second, TaskCount: 3},
				ArmB:       ArmResult{Mode: "single-agent", Passed: true, WallClock: 15 * time.Second},
				Comparison: ABComparison{Winner: "arm_a", Notes: "both passed, Arm A faster"},
			},
		},
		Summary: ABTestSummary{TotalRuns: 1, ArmAWins: 1},
	}

	out := FormatABTestResult(r)
	if !strings.Contains(out, "A/B Test: 1 run(s)") {
		t.Errorf("missing header in output: %s", out)
	}
	if !strings.Contains(out, "Arm A wins: 1") {
		t.Errorf("missing summary in output: %s", out)
	}
	if !strings.Contains(out, "winner=arm_a") {
		t.Errorf("missing run detail in output: %s", out)
	}
}

func TestFormatABTestResult_Nil(t *testing.T) {
	out := FormatABTestResult(nil)
	if out != "No results" {
		t.Errorf("expected 'No results', got %q", out)
	}
}

func TestABTest_ClarifyDryRun(t *testing.T) {
	d := setupTestDB(t)
	c, _ := New(ConductorOpts{DB: d, RepoRoot: "/tmp/test"})
	ctx := context.Background()

	var logLines []string
	c.Log = func(msg string) { logLines = append(logLines, msg) }

	result, err := c.ABTest(ctx, ABTestOpts{
		Goal:    "test goal",
		TestCmd: "go test ./...",
		DryRun:  true,
		Clarify: true,
		Runs:    2,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Summary.TotalRuns != 2 {
		t.Errorf("TotalRuns = %d, want 2", result.Summary.TotalRuns)
	}

	// Verify dry-run output describes clarify arms
	found := false
	for _, line := range logLines {
		if strings.Contains(line, "no clarification") || strings.Contains(line, "with clarification") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("dry-run log should mention clarification arms, got: %v", logLines)
	}
}

func TestABTest_ClarifyArmLabels(t *testing.T) {
	// Verify that when clarify is used, Arm B has ClarifyUsed: true
	arm := ArmResult{
		Mode:         "multi-agent-clarified",
		ClarifyUsed:  true,
		ClarifyCount: 3,
		Passed:       true,
		WallClock:    10 * time.Second,
	}

	if !arm.ClarifyUsed {
		t.Error("expected ClarifyUsed=true")
	}
	if arm.ClarifyCount != 3 {
		t.Errorf("ClarifyCount = %d, want 3", arm.ClarifyCount)
	}

	// Verify FormatABTestResult shows clarify info
	r := &ABTestResult{
		Runs: []ABTestRun{
			{
				RunNumber:  1,
				ArmA:       ArmResult{Mode: "multi-agent", Passed: true, WallClock: 10 * time.Second, TaskCount: 2},
				ArmB:       arm,
				Comparison: ABComparison{Winner: "arm_b", Notes: "both passed, Arm B faster"},
			},
		},
		Summary: ABTestSummary{TotalRuns: 1, ArmBWins: 1},
	}

	out := FormatABTestResult(r)
	if !strings.Contains(out, "clarify=3") {
		t.Errorf("output should contain clarify info, got: %s", out)
	}
}

func TestABTestResultJSON(t *testing.T) {
	r := &ABTestResult{
		Summary: ABTestSummary{TotalRuns: 1, ArmAWins: 1},
	}

	jsonStr, err := ABTestResultJSON(r)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(jsonStr, "TotalRuns") {
		t.Errorf("JSON should contain TotalRuns: %s", jsonStr)
	}
}

func TestABTest_MCPTestDryRun(t *testing.T) {
	d := setupTestDB(t)
	c, _ := New(ConductorOpts{DB: d, RepoRoot: "/tmp/test"})
	ctx := context.Background()

	var logLines []string
	c.Log = func(msg string) { logLines = append(logLines, msg) }

	result, err := c.ABTest(ctx, ABTestOpts{
		Goal:    "test goal",
		TestCmd: "go test ./...",
		DryRun:  true,
		MCPTest: true,
		Runs:    1,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Summary.TotalRuns != 1 {
		t.Errorf("TotalRuns = %d, want 1", result.Summary.TotalRuns)
	}

	// Verify dry-run output describes MCP arms
	foundMCP := false
	for _, line := range logLines {
		if strings.Contains(line, "with MCP tools") || strings.Contains(line, "without MCP tools") {
			foundMCP = true
			break
		}
	}
	if !foundMCP {
		t.Errorf("dry-run log should mention MCP arms, got: %v", logLines)
	}
}

func TestABTest_ReviewTestDryRun(t *testing.T) {
	d := setupTestDB(t)
	c, _ := New(ConductorOpts{DB: d, RepoRoot: "/tmp/test"})
	ctx := context.Background()

	var logLines []string
	c.Log = func(msg string) { logLines = append(logLines, msg) }

	result, err := c.ABTest(ctx, ABTestOpts{
		Goal:       "test goal",
		TestCmd:    "go build ./...",
		DryRun:     true,
		ReviewTest: true,
		Runs:       2,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Summary.TotalRuns != 2 {
		t.Errorf("TotalRuns = %d, want 2", result.Summary.TotalRuns)
	}

	found := false
	for _, line := range logLines {
		if strings.Contains(line, "spec-diff review") || strings.Contains(line, "default review") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("dry-run log should mention review arms, got: %v", logLines)
	}
}

func TestABTest_StructuredReviewDryRun(t *testing.T) {
	d := setupTestDB(t)
	c, _ := New(ConductorOpts{DB: d, RepoRoot: "/tmp/test"})
	ctx := context.Background()

	var logLines []string
	c.Log = func(msg string) { logLines = append(logLines, msg) }

	result, err := c.ABTest(ctx, ABTestOpts{
		Goal:             "test goal",
		TestCmd:          "go build ./...",
		DryRun:           true,
		StructuredReview: true,
		Runs:             1,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Summary.TotalRuns != 1 {
		t.Errorf("TotalRuns = %d, want 1", result.Summary.TotalRuns)
	}

	found := false
	for _, line := range logLines {
		if strings.Contains(line, "structured JSON review") || strings.Contains(line, "default review") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("dry-run log should mention structured review arms, got: %v", logLines)
	}
}

func TestABTest_CascadeDryRun(t *testing.T) {
	d := setupTestDB(t)
	c, _ := New(ConductorOpts{DB: d, RepoRoot: "/tmp/test"})
	ctx := context.Background()

	var logLines []string
	c.Log = func(msg string) { logLines = append(logLines, msg) }

	result, err := c.ABTest(ctx, ABTestOpts{
		Goal:    "test goal",
		TestCmd: "go build ./...",
		DryRun:  true,
		Cascade: true,
		Runs:    1,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Summary.TotalRuns != 1 {
		t.Errorf("TotalRuns = %d, want 1", result.Summary.TotalRuns)
	}

	found := false
	for _, line := range logLines {
		if strings.Contains(line, "GoCascade") || strings.Contains(line, "cascade routing") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("dry-run log should mention cascade arms, got: %v", logLines)
	}
}

func TestABTest_MCPArmLabels(t *testing.T) {
	// Verify that MCP arm results have correct MCPEnabled flags
	armA := ArmResult{
		Mode:       "multi-agent",
		MCPEnabled: true,
		Passed:     true,
		WallClock:  10 * time.Second,
		TaskCount:  2,
	}
	armB := ArmResult{
		Mode:       "no-mcp",
		MCPEnabled: false,
		Passed:     true,
		WallClock:  15 * time.Second,
		TaskCount:  2,
	}

	if !armA.MCPEnabled {
		t.Error("expected Arm A MCPEnabled=true")
	}
	if armB.MCPEnabled {
		t.Error("expected Arm B MCPEnabled=false")
	}

	// Verify FormatABTestResult shows MCP info
	r := &ABTestResult{
		Runs: []ABTestRun{
			{
				RunNumber:  1,
				ArmA:       armA,
				ArmB:       armB,
				Comparison: ABComparison{Winner: "arm_a", Notes: "both passed, Arm A faster"},
			},
		},
		Summary: ABTestSummary{TotalRuns: 1, ArmAWins: 1},
	}

	out := FormatABTestResult(r)
	if !strings.Contains(out, "mcp=true") {
		t.Errorf("output should contain mcp=true for Arm A, got: %s", out)
	}
	if !strings.Contains(out, "mcp=false") {
		t.Errorf("output should contain mcp=false for Arm B, got: %s", out)
	}
}

func TestABTestTimeout_Default(t *testing.T) {
	timeout := abTestTimeout(ABTestOpts{})
	if timeout != 5*time.Minute {
		t.Errorf("expected 5m default, got %v", timeout)
	}
}

func TestABTestTimeout_Custom(t *testing.T) {
	timeout := abTestTimeout(ABTestOpts{TestCmdTimeout: 600})
	if timeout != 10*time.Minute {
		t.Errorf("expected 10m (600s), got %v", timeout)
	}
}

func TestABTestTimeout_Short(t *testing.T) {
	timeout := abTestTimeout(ABTestOpts{TestCmdTimeout: 30})
	if timeout != 30*time.Second {
		t.Errorf("expected 30s, got %v", timeout)
	}
}
