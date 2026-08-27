package eval

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/MochaCosine1206/orchestra/internal/orchestrator"
)

// EvalResult holds the full judging output for a single transcript.
type EvalResult struct {
	ScenarioID string
	Role       string
	Scores     map[string]float64 // metric name -> score (0.0-1.0)
	Composite  float64            // weighted composite score
	Pass       bool               // composite >= threshold (0.6)
	Reasoning  string             // LLM reasoning text
}

// GradingRubric wraps a Rubric with a pass threshold.
type GradingRubric struct {
	Rubric    Rubric
	Threshold float64
}

// PassThreshold is the default minimum composite score to pass.
const PassThreshold = 0.6

// judgeResponseSchema is the JSON schema for the judge LLM output.
const judgeResponseSchema = `{
  "type": "object",
  "properties": {
    "scores": {
      "type": "object",
      "additionalProperties": { "type": "number" }
    },
    "reasoning": { "type": "string" },
    "pass": { "type": "boolean" }
  },
  "required": ["scores", "reasoning", "pass"]
}`

// judgeResponse is the parsed JSON from the judge LLM.
type judgeResponse struct {
	Scores    map[string]float64 `json:"scores"`
	Reasoning string             `json:"reasoning"`
	Pass      bool               `json:"pass"`
}

// Judge evaluates a task execution transcript against a grading rubric.
// Uses an LLM call (via orchestrator.ClaudeRunner) to score the transcript.
// Returns structured scores per dimension + composite + pass/fail.
func Judge(ctx context.Context, runner orchestrator.ClaudeRunner, scenario Scenario, transcript string, rubric GradingRubric) (*EvalResult, error) {
	if runner == nil {
		return nil, fmt.Errorf("runner is nil")
	}
	if transcript == "" {
		return nil, fmt.Errorf("transcript is empty")
	}

	// Build the grading prompt
	prompt := buildJudgePrompt(scenario, transcript, rubric)

	result, err := runner.Run(ctx, orchestrator.RunOpts{
		Prompt:     prompt,
		JSONSchema: judgeResponseSchema,
	})
	if err != nil {
		return nil, fmt.Errorf("judge LLM call failed: %w", err)
	}

	var resp judgeResponse
	if err := json.Unmarshal([]byte(result.Output), &resp); err != nil {
		return nil, fmt.Errorf("parsing judge response: %w (raw: %.500s)", err, result.Output)
	}

	// Compute weighted composite from the rubric weights
	composite := computeComposite(resp.Scores, rubric.Rubric)
	pass := composite >= rubric.Threshold

	return &EvalResult{
		ScenarioID: scenario.ID,
		Role:       scenario.Role,
		Scores:     resp.Scores,
		Composite:  composite,
		Pass:       pass,
		Reasoning:  resp.Reasoning,
	}, nil
}

// computeComposite calculates the weighted average score from rubric weights.
func computeComposite(scores map[string]float64, rubric Rubric) float64 {
	total := 0.0
	weightSum := 0.0
	for _, w := range rubric.Weights {
		if score, ok := scores[w.Metric]; ok {
			total += score * w.Weight
			weightSum += w.Weight
		}
	}
	if weightSum == 0 {
		return 0
	}
	return total / weightSum
}

// buildJudgePrompt constructs the evaluation prompt for the LLM judge.
func buildJudgePrompt(scenario Scenario, transcript string, rubric GradingRubric) string {
	var sb strings.Builder
	sb.WriteString("You are an evaluation judge for an AI agent orchestration system.\n\n")
	sb.WriteString("## Task\n")
	sb.WriteString("Evaluate the following agent transcript against the grading rubric.\n")
	sb.WriteString("Score each metric from 0.0 to 1.0 where 0.0 is complete failure and 1.0 is perfect.\n\n")

	sb.WriteString("## Scenario\n")
	sb.WriteString(fmt.Sprintf("- Role: %s\n", scenario.Role))
	sb.WriteString(fmt.Sprintf("- Goal: %s\n", scenario.Goal))
	sb.WriteString(fmt.Sprintf("- Expected Outcome: %s\n", scenario.ExpectedOutcome))
	sb.WriteString(fmt.Sprintf("- Difficulty: %s\n\n", scenario.Difficulty))

	sb.WriteString("## Grading Rubric\n")
	sb.WriteString(fmt.Sprintf("Pass threshold: %.1f\n", rubric.Threshold))
	for _, w := range rubric.Rubric.Weights {
		sb.WriteString(fmt.Sprintf("- %s: weight %.0f%%\n", w.Metric, w.Weight*100))
	}
	sb.WriteString("\n")

	sb.WriteString("## Agent Transcript\n")
	sb.WriteString("```\n")
	sb.WriteString(transcript)
	sb.WriteString("\n```\n\n")

	sb.WriteString("Provide your evaluation as JSON with fields: scores (object mapping metric names to 0.0-1.0 scores), reasoning (string explaining your evaluation), pass (boolean).\n")

	return sb.String()
}

// ImplementerGradingRubric returns the grading rubric for the implementer role.
func ImplementerGradingRubric() GradingRubric {
	return GradingRubric{Rubric: ImplementerRubric, Threshold: PassThreshold}
}

// ResearcherGradingRubric returns the grading rubric for the researcher role.
func ResearcherGradingRubric() GradingRubric {
	return GradingRubric{Rubric: ResearcherRubric, Threshold: PassThreshold}
}

// ArchitectGradingRubric returns the grading rubric for the architect role.
func ArchitectGradingRubric() GradingRubric {
	return GradingRubric{Rubric: ArchitectRubric, Threshold: PassThreshold}
}

// ReviewerGradingRubric returns the grading rubric for the reviewer role.
func ReviewerGradingRubric() GradingRubric {
	return GradingRubric{Rubric: ReviewerRubric, Threshold: PassThreshold}
}

// RubricForRole returns the appropriate grading rubric for a role string.
// Returns the implementer rubric as default for unknown roles.
func RubricForRole(role string) GradingRubric {
	switch strings.ToLower(role) {
	case "implementer":
		return ImplementerGradingRubric()
	case "researcher":
		return ResearcherGradingRubric()
	case "architect":
		return ArchitectGradingRubric()
	case "reviewer":
		return ReviewerGradingRubric()
	default:
		return ImplementerGradingRubric()
	}
}
