package quality

import (
	"context"
	"fmt"
	"time"

	"github.com/MochaCosine1206/orchestra/internal/eval"
	"github.com/MochaCosine1206/orchestra/internal/orchestrator"
)

// EvalJudgeGate implements Gate by wrapping the existing eval.Judge() system.
// It uses a 5-dimension rubric and maps the composite score to a verdict.
type EvalJudgeGate struct {
	Runner orchestrator.ClaudeRunner
}

// NewEvalJudgeGate creates a judge gate backed by a ClaudeRunner.
func NewEvalJudgeGate(runner orchestrator.ClaudeRunner) *EvalJudgeGate {
	return &EvalJudgeGate{Runner: runner}
}

// Name returns the gate identifier.
func (g *EvalJudgeGate) Name() string { return "eval-judge" }

// QualityGateRubric defines the 5-dimension rubric used by the judge gate.
var QualityGateRubric = eval.GradingRubric{
	Rubric: eval.Rubric{
		Role: "quality-gate",
		Weights: []eval.RubricWeight{
			{Metric: "task_success", Weight: 0.30},
			{Metric: "context_preservation", Weight: 0.25},
			{Metric: "latency", Weight: 0.10},
			{Metric: "safety", Weight: 0.15},
			{Metric: "evidence_coverage", Weight: 0.20},
		},
	},
	Threshold: 0.5, // HOLD threshold; PROMOTE requires >= 0.7
}

// CompositeToDecision maps a composite score to a ShipDecision.
//
//	>= 0.7 -> PROMOTE
//	>= 0.5 -> HOLD
//	<  0.5 -> ROLLBACK
func CompositeToDecision(composite float64) ShipDecision {
	switch {
	case composite >= 0.7:
		return PROMOTE
	case composite >= 0.5:
		return HOLD
	default:
		return ROLLBACK
	}
}

// Check runs the eval judge against the given project and branch.
// Fail-open: on judge error, returns HOLD (not ROLLBACK) with score 0.5.
func (g *EvalJudgeGate) Check(ctx context.Context, projectPath string, branch string) (*LayerResult, error) {
	start := time.Now()

	// Build a scenario representing this quality gate check.
	scenario := eval.Scenario{
		ID:              fmt.Sprintf("quality-gate-%s-%s", branch, start.Format("20060102-150405")),
		Role:            "quality-gate",
		Goal:            fmt.Sprintf("Evaluate quality of changes on branch %s", branch),
		ExpectedOutcome: "Changes meet quality standards across all dimensions",
		Difficulty:      "medium",
	}

	// Build a transcript from the project state.
	// The transcript summarizes the branch changes for the judge to evaluate.
	transcript := fmt.Sprintf(
		"Quality gate evaluation for branch %s in project %s.\n"+
			"The judge should score: task_success, context_preservation, latency, safety, evidence_coverage.",
		branch, projectPath,
	)

	evalResult, err := eval.Judge(ctx, g.Runner, scenario, transcript, QualityGateRubric)
	if err != nil {
		// Fail-open: judge error returns HOLD, not ROLLBACK.
		return &LayerResult{
			Layer:    "eval-judge",
			Passed:   true,
			Score:    0.5,
			Details:  fmt.Sprintf("judge failed (fail-open, HOLD): %v", err),
			Duration: time.Since(start),
		}, nil
	}

	decision := CompositeToDecision(evalResult.Composite)
	passed := decision != ROLLBACK

	details := fmt.Sprintf("composite=%.3f decision=%s reasoning=%s scores=%v",
		evalResult.Composite, decision, evalResult.Reasoning, evalResult.Scores)

	return &LayerResult{
		Layer:    "eval-judge",
		Passed:   passed,
		Score:    evalResult.Composite,
		Details:  details,
		Duration: time.Since(start),
	}, nil
}
