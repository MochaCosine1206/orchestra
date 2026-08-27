package quality

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/MochaCosine1206/orchestra/internal/orchestrator"
)

// mockClaudeRunner implements orchestrator.ClaudeRunner for testing.
type mockClaudeRunner struct {
	output string
	err    error
}

func (m *mockClaudeRunner) Run(_ context.Context, _ orchestrator.RunOpts) (orchestrator.RunResult, error) {
	if m.err != nil {
		return orchestrator.RunResult{}, m.err
	}
	return orchestrator.RunResult{Output: m.output}, nil
}

func TestEvalJudgeGateName(t *testing.T) {
	g := NewEvalJudgeGate(nil)
	if g.Name() != "eval-judge" {
		t.Errorf("expected name 'eval-judge', got %q", g.Name())
	}
}

func TestCompositeToDecision(t *testing.T) {
	tests := []struct {
		composite float64
		want      ShipDecision
	}{
		{1.0, PROMOTE},
		{0.7, PROMOTE},
		{0.69, HOLD},
		{0.5, HOLD},
		{0.49, ROLLBACK},
		{0.0, ROLLBACK},
	}
	for _, tt := range tests {
		t.Run(fmt.Sprintf("%.2f", tt.composite), func(t *testing.T) {
			got := CompositeToDecision(tt.composite)
			if got != tt.want {
				t.Errorf("CompositeToDecision(%.2f) = %q, want %q", tt.composite, got, tt.want)
			}
		})
	}
}

// judgeOutputForScores builds a JSON string that eval.Judge will parse.
func judgeOutputForScores(scores map[string]float64, pass bool) string {
	resp := struct {
		Scores    map[string]float64 `json:"scores"`
		Reasoning string             `json:"reasoning"`
		Pass      bool               `json:"pass"`
	}{
		Scores:    scores,
		Reasoning: "test reasoning",
		Pass:      pass,
	}
	b, _ := json.Marshal(resp)
	return string(b)
}

func TestEvalJudgeGateCheck_Promote(t *testing.T) {
	// All high scores -> composite > 0.7 -> PROMOTE
	scores := map[string]float64{
		"task_success":         0.9,
		"context_preservation": 0.8,
		"latency":              0.9,
		"safety":               0.85,
		"evidence_coverage":    0.8,
	}
	runner := &mockClaudeRunner{output: judgeOutputForScores(scores, true)}
	gate := NewEvalJudgeGate(runner)

	result, err := gate.Check(context.Background(), "/tmp/project", "feature/test")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !result.Passed {
		t.Error("expected passed=true for PROMOTE")
	}
	if result.Score < 0.7 {
		t.Errorf("expected score >= 0.7, got %.3f", result.Score)
	}
	if result.Layer != "eval-judge" {
		t.Errorf("expected layer 'eval-judge', got %q", result.Layer)
	}
}

func TestEvalJudgeGateCheck_Hold(t *testing.T) {
	// Mixed scores -> composite between 0.5 and 0.7 -> HOLD
	scores := map[string]float64{
		"task_success":         0.6,
		"context_preservation": 0.5,
		"latency":              0.7,
		"safety":               0.6,
		"evidence_coverage":    0.5,
	}
	runner := &mockClaudeRunner{output: judgeOutputForScores(scores, false)}
	gate := NewEvalJudgeGate(runner)

	result, err := gate.Check(context.Background(), "/tmp/project", "feature/test")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !result.Passed {
		t.Error("expected passed=true for HOLD (HOLD is not a failure)")
	}
	if result.Score < 0.5 || result.Score >= 0.7 {
		t.Errorf("expected score in [0.5, 0.7), got %.3f", result.Score)
	}
}

func TestEvalJudgeGateCheck_Rollback(t *testing.T) {
	// All low scores -> composite < 0.5 -> ROLLBACK
	scores := map[string]float64{
		"task_success":         0.2,
		"context_preservation": 0.3,
		"latency":              0.1,
		"safety":               0.2,
		"evidence_coverage":    0.3,
	}
	runner := &mockClaudeRunner{output: judgeOutputForScores(scores, false)}
	gate := NewEvalJudgeGate(runner)

	result, err := gate.Check(context.Background(), "/tmp/project", "feature/test")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.Passed {
		t.Error("expected passed=false for ROLLBACK")
	}
	if result.Score >= 0.5 {
		t.Errorf("expected score < 0.5, got %.3f", result.Score)
	}
}

func TestEvalJudgeGateCheck_FailOpen(t *testing.T) {
	// Runner error -> fail-open with HOLD (score 0.5, passed=true)
	runner := &mockClaudeRunner{err: fmt.Errorf("LLM unavailable")}
	gate := NewEvalJudgeGate(runner)

	result, err := gate.Check(context.Background(), "/tmp/project", "feature/test")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !result.Passed {
		t.Error("expected passed=true for fail-open")
	}
	if result.Score != 0.5 {
		t.Errorf("expected score 0.5 for fail-open, got %.3f", result.Score)
	}
}

func TestEvalJudgeGateCheck_NilRunner(t *testing.T) {
	// nil runner -> fail-open
	gate := NewEvalJudgeGate(nil)

	result, err := gate.Check(context.Background(), "/tmp/project", "feature/test")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !result.Passed {
		t.Error("expected passed=true for nil runner fail-open")
	}
	if result.Score != 0.5 {
		t.Errorf("expected score 0.5, got %.3f", result.Score)
	}
}

func TestQualityGateRubricWeights(t *testing.T) {
	// Verify the rubric weights sum to 1.0
	total := 0.0
	for _, w := range QualityGateRubric.Rubric.Weights {
		total += w.Weight
	}
	if total < 0.99 || total > 1.01 {
		t.Errorf("rubric weights sum to %.3f, expected ~1.0", total)
	}

	// Verify 5 dimensions
	if len(QualityGateRubric.Rubric.Weights) != 5 {
		t.Errorf("expected 5 dimensions, got %d", len(QualityGateRubric.Rubric.Weights))
	}
}
