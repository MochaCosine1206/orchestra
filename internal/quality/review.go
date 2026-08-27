package quality

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/MochaCosine1206/orchestra/internal/orchestrator"
)

// RiskScore classifies the risk level from a code review.
type RiskScore string

const (
	RiskLow      RiskScore = "low"
	RiskMedium   RiskScore = "medium"
	RiskHigh     RiskScore = "high"
	RiskCritical RiskScore = "critical"
)

// ReviewResult captures the output of a review provider.
type ReviewResult struct {
	RiskScore  RiskScore `json:"risk_score"`
	Findings   int       `json:"findings"`
	BlockMerge bool      `json:"block_merge"`
	Summary    string    `json:"summary"`
}

// ReviewProvider abstracts the source of code reviews for testability.
type ReviewProvider interface {
	Review(ctx context.Context, projectPath string, branch string) (*ReviewResult, error)
}

// ReviewGate implements Gate using a pluggable ReviewProvider.
type ReviewGate struct {
	Provider ReviewProvider
}

// NewReviewGate creates a ReviewGate with the given provider.
func NewReviewGate(provider ReviewProvider) *ReviewGate {
	return &ReviewGate{Provider: provider}
}

// Name returns the gate identifier.
func (g *ReviewGate) Name() string { return "review" }

// riskScoreToScore maps risk levels to numeric scores.
func riskScoreToScore(risk RiskScore) float64 {
	switch risk {
	case RiskLow:
		return 1.0
	case RiskMedium:
		return 0.7
	case RiskHigh:
		return 0.3
	case RiskCritical:
		return 0.0
	default:
		return 0.5
	}
}

// Check runs the review provider and maps the result to a LayerResult.
// Fail-open: on error, returns PASS with score 0.5.
func (g *ReviewGate) Check(ctx context.Context, projectPath string, branch string) (*LayerResult, error) {
	start := time.Now()

	result, err := g.Provider.Review(ctx, projectPath, branch)
	if err != nil {
		// Fail-open: review error should not block the pipeline.
		return &LayerResult{
			Layer:    "review",
			Passed:   true,
			Score:    0.5,
			Details:  fmt.Sprintf("review failed (fail-open): %v", err),
			Duration: time.Since(start),
		}, nil
	}

	score := riskScoreToScore(result.RiskScore)
	passed := !result.BlockMerge

	details := fmt.Sprintf("risk=%s findings=%d block_merge=%t summary=%s",
		result.RiskScore, result.Findings, result.BlockMerge, result.Summary)

	return &LayerResult{
		Layer:    "review",
		Passed:   passed,
		Score:    score,
		Details:  details,
		Duration: time.Since(start),
	}, nil
}

// reviewPrompt is the prompt sent to the LLM for internal review.
const reviewPrompt = `You are a code review gate for an orchestration system. Review the changes on branch "%s" in the project at "%s".

Analyze the diff for:
1. Bugs and correctness issues
2. Security vulnerabilities
3. Performance problems
4. Breaking changes

Respond with JSON:
{
  "risk_score": "low|medium|high|critical",
  "findings": <number of issues found>,
  "block_merge": true/false,
  "summary": "Brief summary of findings"
}

Set block_merge=true only for critical or high-severity issues.`

// InternalReviewProvider uses the Orchestra's ClaudeRunner to perform reviews.
type InternalReviewProvider struct {
	Runner orchestrator.ClaudeRunner
}

// NewInternalReviewProvider creates a provider backed by a ClaudeRunner.
func NewInternalReviewProvider(runner orchestrator.ClaudeRunner) *InternalReviewProvider {
	return &InternalReviewProvider{Runner: runner}
}

// Review runs an LLM-based code review via ClaudeRunner.
func (p *InternalReviewProvider) Review(ctx context.Context, projectPath string, branch string) (*ReviewResult, error) {
	if p.Runner == nil {
		return nil, fmt.Errorf("runner is nil")
	}

	prompt := fmt.Sprintf(reviewPrompt, branch, projectPath)

	runResult, err := p.Runner.Run(ctx, orchestrator.RunOpts{
		Prompt:  prompt,
		WorkDir: projectPath,
	})
	if err != nil {
		return nil, fmt.Errorf("review LLM call failed: %w", err)
	}

	return parseReviewJSON(runResult.Output)
}

// parseReviewJSON extracts a ReviewResult from the LLM's JSON output.
func parseReviewJSON(output string) (*ReviewResult, error) {
	output = strings.TrimSpace(output)

	// Try to find JSON in the output (LLMs sometimes wrap in markdown code blocks).
	jsonStr := output
	if idx := strings.Index(output, "{"); idx >= 0 {
		if end := strings.LastIndex(output, "}"); end > idx {
			jsonStr = output[idx : end+1]
		}
	}

	var result ReviewResult
	if err := json.Unmarshal([]byte(jsonStr), &result); err != nil {
		return nil, fmt.Errorf("parsing review JSON: %w (raw: %.500s)", err, output)
	}

	// Validate risk score.
	switch result.RiskScore {
	case RiskLow, RiskMedium, RiskHigh, RiskCritical:
		// valid
	default:
		result.RiskScore = RiskMedium // default to medium for unknown values
	}

	return &result, nil
}
