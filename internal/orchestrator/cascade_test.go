package orchestrator

import (
	"context"
	"testing"
)

func TestEstimateComplexity_SmallTask(t *testing.T) {
	d := setupTestDB(t)
	runner := &MockRunner{
		Outputs: []string{`{"estimated_files": 2, "reasoning": "Just two files", "is_research": false}`},
	}
	c, _ := New(ConductorOpts{DB: d, RepoRoot: "/tmp/test", Runner: runner})
	ctx := context.Background()

	est, err := c.estimateComplexity(ctx, "Add a simple flag")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if est.EstimatedFiles != 2 {
		t.Errorf("EstimatedFiles = %d, want 2", est.EstimatedFiles)
	}
	if est.Tier != Tier1Simple {
		t.Errorf("Tier = %d, want %d (Tier1Simple)", est.Tier, Tier1Simple)
	}
	if est.Reasoning != "Just two files" {
		t.Errorf("Reasoning = %q", est.Reasoning)
	}
}

func TestEstimateComplexity_MediumTask(t *testing.T) {
	d := setupTestDB(t)
	runner := &MockRunner{
		Outputs: []string{`{"estimated_files": 5, "reasoning": "Several files", "is_research": false}`},
	}
	c, _ := New(ConductorOpts{DB: d, RepoRoot: "/tmp/test", Runner: runner})
	ctx := context.Background()

	est, err := c.estimateComplexity(ctx, "Refactor module")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if est.Tier != Tier2Medium {
		t.Errorf("Tier = %d, want %d (Tier2Medium)", est.Tier, Tier2Medium)
	}
}

func TestEstimateComplexity_LargeTask(t *testing.T) {
	d := setupTestDB(t)
	runner := &MockRunner{
		Outputs: []string{`{"estimated_files": 12, "reasoning": "Major refactor", "is_research": false}`},
	}
	c, _ := New(ConductorOpts{DB: d, RepoRoot: "/tmp/test", Runner: runner})
	ctx := context.Background()

	est, err := c.estimateComplexity(ctx, "Full rewrite")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if est.Tier != Tier3Complex {
		t.Errorf("Tier = %d, want %d (Tier3Complex)", est.Tier, Tier3Complex)
	}
}

func TestEstimateComplexity_ResearchTask(t *testing.T) {
	d := setupTestDB(t)
	runner := &MockRunner{
		Outputs: []string{`{"estimated_files": 1, "reasoning": "Just docs", "is_research": true}`},
	}
	c, _ := New(ConductorOpts{DB: d, RepoRoot: "/tmp/test", Runner: runner})
	ctx := context.Background()

	est, err := c.estimateComplexity(ctx, "Write research doc")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if est.Tier != Tier1Simple {
		t.Errorf("Tier = %d, want %d (Tier1Simple for research)", est.Tier, Tier1Simple)
	}
	if !est.IsResearch {
		t.Error("expected IsResearch=true")
	}
}

func TestEstimateComplexity_NoRunner(t *testing.T) {
	d := setupTestDB(t)
	c, _ := New(ConductorOpts{DB: d, RepoRoot: "/tmp/test"})
	ctx := context.Background()

	_, err := c.estimateComplexity(ctx, "test")
	if err == nil {
		t.Fatal("expected error when no runner")
	}
}

func TestEstimateComplexity_MalformedJSON(t *testing.T) {
	d := setupTestDB(t)
	runner := &MockRunner{
		Outputs: []string{`not valid json`},
	}
	c, _ := New(ConductorOpts{DB: d, RepoRoot: "/tmp/test", Runner: runner})
	ctx := context.Background()

	est, err := c.estimateComplexity(ctx, "test")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Should default to Tier2Medium
	if est.Tier != Tier2Medium {
		t.Errorf("Tier = %d, want %d (Tier2Medium default)", est.Tier, Tier2Medium)
	}
}

func TestGoCascade_EmptyGoal(t *testing.T) {
	d := setupTestDB(t)
	c, _ := New(ConductorOpts{DB: d, RepoRoot: "/tmp/test"})
	ctx := context.Background()

	_, err := c.GoCascade(ctx, GoOpts{})
	if err == nil {
		t.Fatal("expected error for empty goal")
	}
}

func TestGoCascade_Tier1Route(t *testing.T) {
	d := setupTestDB(t)

	// First call: complexity estimation returns tier 1
	// Second call: single agent execution
	runner := &MockRunner{
		Outputs: []string{
			`{"estimated_files": 1, "reasoning": "tiny", "is_research": false}`,
			`Done. Committed changes.`,
		},
	}
	c, _ := New(ConductorOpts{DB: d, RepoRoot: "/tmp/test", Runner: runner})
	ctx := context.Background()

	var logLines []string
	c.Log = func(msg string) { logLines = append(logLines, msg) }

	result, err := c.GoCascade(ctx, GoOpts{Goal: "Add a simple constant"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.TaskCount != 1 {
		t.Errorf("TaskCount = %d, want 1", result.TaskCount)
	}

	// Verify tier 1 route was taken
	foundTier1 := false
	for _, line := range logLines {
		if cascadeContains(line, "Tier 1") {
			foundTier1 = true
			break
		}
	}
	if !foundTier1 {
		t.Errorf("expected Tier 1 log line, got: %v", logLines)
	}
}

func TestGoCascade_Tier3Route(t *testing.T) {
	d := setupTestDB(t)

	// Complexity estimation returns tier 3 — this will call Go() which will fail
	// because we don't have a real git repo, but we can verify routing
	runner := &MockRunner{
		Outputs: []string{
			`{"estimated_files": 15, "reasoning": "massive", "is_research": false}`,
		},
	}
	c, _ := New(ConductorOpts{DB: d, RepoRoot: "/tmp/test", Runner: runner})
	ctx := context.Background()

	var logLines []string
	c.Log = func(msg string) { logLines = append(logLines, msg) }

	// Will fail because /tmp/test is not a git repo, but we verify routing
	_, _ = c.GoCascade(ctx, GoOpts{Goal: "Major refactor across many files"})

	foundTier3 := false
	for _, line := range logLines {
		if cascadeContains(line, "Tier 3") {
			foundTier3 = true
			break
		}
	}
	if !foundTier3 {
		t.Errorf("expected Tier 3 log line, got: %v", logLines)
	}
}

func TestGoCascade_Tier3AutoEnablesDefenseEnforcement(t *testing.T) {
	d := setupTestDB(t)

	runner := &MockRunner{
		Outputs: []string{
			`{"estimated_files": 15, "reasoning": "massive", "is_research": false}`,
		},
	}
	c, _ := New(ConductorOpts{DB: d, RepoRoot: "/tmp/test", Runner: runner})
	ctx := context.Background()

	var logLines []string
	c.Log = func(msg string) { logLines = append(logLines, msg) }

	// No explicit FileEnforcement — should auto-enable for Tier 3
	_, _ = c.GoCascade(ctx, GoOpts{Goal: "Major refactor across many files"})

	foundAutoEnable := false
	for _, line := range logLines {
		if cascadeContains(line, "auto-enabling defense enforcement for Tier 3") {
			foundAutoEnable = true
			break
		}
	}
	if !foundAutoEnable {
		t.Errorf("expected auto-enable defense log line for Tier 3, got: %v", logLines)
	}
}

func TestGoCascade_Tier1DoesNotAutoEnableEnforcement(t *testing.T) {
	d := setupTestDB(t)

	runner := &MockRunner{
		Outputs: []string{
			`{"estimated_files": 1, "reasoning": "tiny", "is_research": false}`,
			`Done. Committed changes.`,
		},
	}
	c, _ := New(ConductorOpts{DB: d, RepoRoot: "/tmp/test", Runner: runner})
	ctx := context.Background()

	var logLines []string
	c.Log = func(msg string) { logLines = append(logLines, msg) }

	_, _ = c.GoCascade(ctx, GoOpts{Goal: "Add a simple constant"})

	for _, line := range logLines {
		if cascadeContains(line, "auto-enabling defense") {
			t.Errorf("Tier 1 should NOT auto-enable enforcement, got: %s", line)
		}
	}
}

func TestGoCascade_ExplicitEnforcementNotOverridden(t *testing.T) {
	d := setupTestDB(t)

	runner := &MockRunner{
		Outputs: []string{
			`{"estimated_files": 15, "reasoning": "massive", "is_research": false}`,
		},
	}
	c, _ := New(ConductorOpts{DB: d, RepoRoot: "/tmp/test", Runner: runner})
	ctx := context.Background()

	var logLines []string
	c.Log = func(msg string) { logLines = append(logLines, msg) }

	// Explicit pessimistic — should NOT be overridden to defense
	_, _ = c.GoCascade(ctx, GoOpts{
		Goal:            "Major refactor across many files",
		FileEnforcement: "pessimistic",
	})

	for _, line := range logLines {
		if cascadeContains(line, "auto-enabling defense") {
			t.Errorf("explicit enforcement should not be overridden, got: %s", line)
		}
	}
}

// --- B-207: Semantic cohesion tests ---

func TestEstimateComplexity_HighCohesion_DowngradesToTier2(t *testing.T) {
	d := setupTestDB(t)
	runner := &MockRunner{
		Outputs: []string{`{"estimated_files": 8, "reasoning": "End-to-end field addition", "is_research": false, "semantic_cohesion": "high"}`},
	}
	c, _ := New(ConductorOpts{DB: d, RepoRoot: "/tmp/test", Runner: runner})
	ctx := context.Background()

	est, err := c.estimateComplexity(ctx, "Add priority_label field end-to-end")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if est.EstimatedFiles != 8 {
		t.Errorf("EstimatedFiles = %d, want 8", est.EstimatedFiles)
	}
	if est.SemanticCohesion != "high" {
		t.Errorf("SemanticCohesion = %q, want high", est.SemanticCohesion)
	}
	// 8 files normally would be Tier3Complex, but high cohesion downgrades to Tier2Medium
	if est.Tier != Tier2Medium {
		t.Errorf("Tier = %d, want %d (Tier2Medium for high cohesion ≤10 files)", est.Tier, Tier2Medium)
	}
}

func TestEstimateComplexity_HighCohesion_10Files_StillTier2(t *testing.T) {
	d := setupTestDB(t)
	runner := &MockRunner{
		Outputs: []string{`{"estimated_files": 10, "reasoning": "Many files same feature", "is_research": false, "semantic_cohesion": "high"}`},
	}
	c, _ := New(ConductorOpts{DB: d, RepoRoot: "/tmp/test", Runner: runner})
	ctx := context.Background()

	est, err := c.estimateComplexity(ctx, "Large cohesive change")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if est.Tier != Tier2Medium {
		t.Errorf("Tier = %d, want %d (Tier2Medium for high cohesion ≤10 files)", est.Tier, Tier2Medium)
	}
}

func TestEstimateComplexity_HighCohesion_11Files_Tier3(t *testing.T) {
	d := setupTestDB(t)
	runner := &MockRunner{
		Outputs: []string{`{"estimated_files": 11, "reasoning": "Too many files", "is_research": false, "semantic_cohesion": "high"}`},
	}
	c, _ := New(ConductorOpts{DB: d, RepoRoot: "/tmp/test", Runner: runner})
	ctx := context.Background()

	est, err := c.estimateComplexity(ctx, "Very large cohesive change")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// 11 files exceeds the ≤10 cohesion downgrade threshold
	if est.Tier != Tier3Complex {
		t.Errorf("Tier = %d, want %d (Tier3Complex for >10 files even with high cohesion)", est.Tier, Tier3Complex)
	}
}

func TestEstimateComplexity_LowCohesion_8Files_Tier3(t *testing.T) {
	d := setupTestDB(t)
	runner := &MockRunner{
		Outputs: []string{`{"estimated_files": 8, "reasoning": "Unrelated changes", "is_research": false, "semantic_cohesion": "low"}`},
	}
	c, _ := New(ConductorOpts{DB: d, RepoRoot: "/tmp/test", Runner: runner})
	ctx := context.Background()

	est, err := c.estimateComplexity(ctx, "Multiple unrelated changes")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Low cohesion with 8 files should remain Tier3Complex (no downgrade)
	if est.Tier != Tier3Complex {
		t.Errorf("Tier = %d, want %d (Tier3Complex for low cohesion)", est.Tier, Tier3Complex)
	}
}

func TestEstimateComplexity_MediumCohesion_8Files_Tier3(t *testing.T) {
	d := setupTestDB(t)
	runner := &MockRunner{
		Outputs: []string{`{"estimated_files": 8, "reasoning": "Related but distinct", "is_research": false, "semantic_cohesion": "medium"}`},
	}
	c, _ := New(ConductorOpts{DB: d, RepoRoot: "/tmp/test", Runner: runner})
	ctx := context.Background()

	est, err := c.estimateComplexity(ctx, "Medium cohesion change")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Only "high" cohesion triggers the downgrade
	if est.Tier != Tier3Complex {
		t.Errorf("Tier = %d, want %d (Tier3Complex for medium cohesion ≥8 files)", est.Tier, Tier3Complex)
	}
}

func TestEstimateComplexity_NoCohesion_DefaultEmpty(t *testing.T) {
	d := setupTestDB(t)
	// Old-style LLM output without semantic_cohesion field
	runner := &MockRunner{
		Outputs: []string{`{"estimated_files": 8, "reasoning": "Old format", "is_research": false}`},
	}
	c, _ := New(ConductorOpts{DB: d, RepoRoot: "/tmp/test", Runner: runner})
	ctx := context.Background()

	est, err := c.estimateComplexity(ctx, "Test backward compat")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Empty cohesion string should NOT match "high", so no downgrade
	if est.Tier != Tier3Complex {
		t.Errorf("Tier = %d, want %d (Tier3Complex when cohesion not provided)", est.Tier, Tier3Complex)
	}
}

func cascadeContains(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
