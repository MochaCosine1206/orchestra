package eval

import (
	"context"
	"encoding/json"
	"math"
	"testing"

	"github.com/MochaCosine1206/orchestra/internal/orchestrator"
)

func TestRubricWeightsSum(t *testing.T) {
	rubrics := []struct {
		name   string
		rubric Rubric
	}{
		{"implementer", ImplementerRubric},
		{"reviewer", ReviewerRubric},
		{"researcher", ResearcherRubric},
		{"architect", ArchitectRubric},
	}

	for _, r := range rubrics {
		t.Run(r.name, func(t *testing.T) {
			sum := 0.0
			for _, w := range r.rubric.Weights {
				sum += w.Weight
			}
			if math.Abs(sum-1.0) > 0.001 {
				t.Errorf("rubric %s weights sum to %.3f, want 1.0", r.name, sum)
			}
		})
	}
}

func TestRubricForRole(t *testing.T) {
	tests := []struct {
		role     string
		expected string
	}{
		{"implementer", "implementer"},
		{"Implementer", "implementer"},
		{"reviewer", "reviewer"},
		{"researcher", "researcher"},
		{"architect", "architect"},
		{"unknown", "implementer"}, // default
	}

	for _, tt := range tests {
		t.Run(tt.role, func(t *testing.T) {
			rubric := RubricForRole(tt.role)
			if rubric.Rubric.Role != tt.expected {
				t.Errorf("RubricForRole(%q) = %q, want %q", tt.role, rubric.Rubric.Role, tt.expected)
			}
			if rubric.Threshold != PassThreshold {
				t.Errorf("threshold = %.1f, want %.1f", rubric.Threshold, PassThreshold)
			}
		})
	}
}

func TestComputeComposite(t *testing.T) {
	scores := map[string]float64{
		"completion":  0.8,
		"test_pass":   1.0,
		"merge_clean": 0.6,
		"quality":     0.7,
		"efficiency":  0.9,
	}

	composite := computeComposite(scores, ImplementerRubric)

	// Expected: (0.8*0.30 + 1.0*0.25 + 0.6*0.15 + 0.7*0.15 + 0.9*0.15) / 1.0
	// = 0.24 + 0.25 + 0.09 + 0.105 + 0.135 = 0.82
	expected := 0.82
	if math.Abs(composite-expected) > 0.01 {
		t.Errorf("composite = %.3f, want %.3f", composite, expected)
	}
}

func TestComputeCompositePartialScores(t *testing.T) {
	// Only some metrics present
	scores := map[string]float64{
		"completion": 1.0,
		"test_pass":  0.5,
	}

	composite := computeComposite(scores, ImplementerRubric)
	// (1.0*0.30 + 0.5*0.25) / (0.30 + 0.25) = 0.425 / 0.55 ≈ 0.7727
	expected := 0.425 / 0.55
	if math.Abs(composite-expected) > 0.01 {
		t.Errorf("composite = %.3f, want %.3f", composite, expected)
	}
}

func TestComputeCompositeEmpty(t *testing.T) {
	composite := computeComposite(map[string]float64{}, ImplementerRubric)
	if composite != 0 {
		t.Errorf("composite = %.3f, want 0", composite)
	}
}

func TestJudgeNilRunner(t *testing.T) {
	_, err := Judge(context.Background(), nil, Scenario{}, "transcript", ImplementerGradingRubric())
	if err == nil {
		t.Error("expected error for nil runner")
	}
}

func TestJudgeEmptyTranscript(t *testing.T) {
	runner := &orchestrator.MockRunner{Outputs: []string{"{}"}}
	_, err := Judge(context.Background(), runner, Scenario{}, "", ImplementerGradingRubric())
	if err == nil {
		t.Error("expected error for empty transcript")
	}
}

func TestJudgeWithMockRunner(t *testing.T) {
	resp := judgeResponse{
		Scores: map[string]float64{
			"completion":  0.9,
			"test_pass":   0.8,
			"merge_clean": 0.7,
			"quality":     0.6,
			"efficiency":  0.5,
		},
		Reasoning: "Good overall execution",
		Pass:      true,
	}
	respJSON, _ := json.Marshal(resp)

	runner := &orchestrator.MockRunner{
		Outputs: []string{string(respJSON)},
	}

	scenario := Scenario{
		ID:              "test-scenario",
		Role:            "implementer",
		Goal:            "Implement feature X",
		ExpectedOutcome: "Feature X works",
		Difficulty:      "medium",
	}

	result, err := Judge(context.Background(), runner, scenario, "test transcript", ImplementerGradingRubric())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.ScenarioID != "test-scenario" {
		t.Errorf("ScenarioID = %q, want %q", result.ScenarioID, "test-scenario")
	}
	if result.Role != "implementer" {
		t.Errorf("Role = %q, want %q", result.Role, "implementer")
	}
	if len(result.Scores) != 5 {
		t.Errorf("got %d scores, want 5", len(result.Scores))
	}
	// composite = (0.9*0.30 + 0.8*0.25 + 0.7*0.15 + 0.6*0.15 + 0.5*0.15)
	// = 0.27 + 0.20 + 0.105 + 0.09 + 0.075 = 0.74
	if math.Abs(result.Composite-0.74) > 0.01 {
		t.Errorf("Composite = %.3f, want 0.74", result.Composite)
	}
	if !result.Pass {
		t.Error("expected pass=true for composite 0.74 >= 0.6")
	}
}

func TestBuildJudgePrompt(t *testing.T) {
	scenario := Scenario{
		ID:              "s1",
		Role:            "implementer",
		Goal:            "Test goal",
		ExpectedOutcome: "Expected result",
		Difficulty:      "easy",
	}
	rubric := ImplementerGradingRubric()
	prompt := buildJudgePrompt(scenario, "my transcript", rubric)

	if len(prompt) == 0 {
		t.Error("prompt should not be empty")
	}
	// Check key content is included
	for _, want := range []string{"implementer", "Test goal", "my transcript", "completion", "test_pass"} {
		if !searchSubstring(prompt, want) {
			t.Errorf("prompt missing %q", want)
		}
	}
}
