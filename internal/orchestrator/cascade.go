package orchestrator

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/MochaCosine1206/orchestra/internal/agent"
)

// ComplexityTier categorizes task complexity for cascade routing.
type ComplexityTier int

const (
	Tier1Simple  ComplexityTier = 1 // 1-3 files, single agent
	Tier2Medium  ComplexityTier = 2 // 4-7 files, iterative
	Tier3Complex ComplexityTier = 3 // 8+ files, full multi-agent
)

// ComplexityEstimate captures the LLM's assessment of task complexity.
type ComplexityEstimate struct {
	EstimatedFiles    int            `json:"estimated_files"`
	Reasoning         string         `json:"reasoning"`
	IsResearch        bool           `json:"is_research"`
	SemanticCohesion  string         `json:"semantic_cohesion"` // "high", "medium", "low"
	Tier              ComplexityTier `json:"tier"`
}

const complexityPromptTemplate = `Estimate the complexity of this coding task. How many files will need to be created or modified?

TASK: %s

Respond with JSON only:
{"estimated_files": N, "reasoning": "brief explanation", "is_research": false, "semantic_cohesion": "high|medium|low"}

Rules:
- estimated_files: your best estimate of files that need changes
- is_research: true only if this is a pure research/documentation task with no code changes
- semantic_cohesion: "high" if all changes serve a single coherent feature/concept flowing through the codebase (e.g., adding a field end-to-end), "medium" if changes are related but touch distinct subsystems, "low" if changes are independent concerns that happen to be bundled
- Be precise — count actual files, not modules or packages`

// estimateComplexity makes a single LLM call to estimate task file count and assigns a tier.
func (c *Conductor) estimateComplexity(ctx context.Context, goal string) (*ComplexityEstimate, error) {
	if c.Runner == nil {
		return nil, fmt.Errorf("ClaudeRunner is required for complexity estimation")
	}

	prompt := fmt.Sprintf(complexityPromptTemplate, goal)
	runResult, err := c.Runner.Run(ctx, RunOpts{
		Prompt:  prompt,
		Model:   agent.ModelOpus,
		WorkDir: c.RepoRoot,
	})
	if err != nil {
		return nil, fmt.Errorf("complexity estimation: %w", err)
	}

	var estimate ComplexityEstimate
	jsonStr := extractJSON(runResult.Output)
	if err := json.Unmarshal([]byte(jsonStr), &estimate); err != nil {
		// Default to Tier 2 if parsing fails
		return &ComplexityEstimate{
			EstimatedFiles: 5,
			Reasoning:      "estimation parse failed, defaulting to medium",
			Tier:           Tier2Medium,
		}, nil
	}

	// Assign tier based on file count, with cohesion-based downgrade.
	// B-207: high-cohesion goals with up to 10 files route to Tier 2 (iterative)
	// instead of Tier 3 (multi-agent). B-206b showed single-agent was 43% faster
	// with better coverage on cohesive goals.
	switch {
	case estimate.IsResearch:
		estimate.Tier = Tier1Simple
	case estimate.EstimatedFiles <= 3:
		estimate.Tier = Tier1Simple
	case estimate.SemanticCohesion == "high" && estimate.EstimatedFiles <= 10:
		estimate.Tier = Tier2Medium
	case estimate.EstimatedFiles <= 7:
		estimate.Tier = Tier2Medium
	default:
		estimate.Tier = Tier3Complex
	}

	return &estimate, nil
}

// goSingleAgent runs a goal as a single Claude session with no decomposition.
func (c *Conductor) goSingleAgent(ctx context.Context, opts GoOpts) (*GoResult, error) {
	start := time.Now()

	if c.Runner == nil {
		return nil, fmt.Errorf("ClaudeRunner is required for single-agent mode")
	}

	result := &GoResult{
		SessionID: c.SessionID,
		TaskCount: 1,
	}

	prompt := fmt.Sprintf(`You are working on the following goal as a single agent.

GOAL: %s

Implement the changes needed. Commit your work when done.
If a test command is provided, make sure your changes pass: %s`, opts.Goal, opts.TestCmd)

	_, err := c.Runner.Run(ctx, RunOpts{
		Prompt:  prompt,
		Model:   agent.ModelOpus,
		WorkDir: c.RepoRoot,
	})

	if err != nil {
		result.FailedCount = 1
		result.Duration = time.Since(start)
		return result, err
	}

	result.DoneCount = 1

	// Verify with test command
	if opts.TestCmd != "" {
		timeout := 5 * time.Minute
		if opts.TestCmdTimeout > 0 {
			timeout = time.Duration(opts.TestCmdTimeout) * time.Second
		}
		if _, err := runTestCmd(c.RepoRoot, opts.TestCmd, timeout); err != nil {
			result.FailedCount = 1
			result.DoneCount = 0
		}
	}

	result.Duration = time.Since(start)
	return result, nil
}

// GoCascade routes a goal through the cascade: estimate complexity, then
// pick the lightest-weight execution strategy that can handle it.
// Tier 1 → single agent (escalate on failure to iterative)
// Tier 2 → iterative (escalate on failure to multi-agent)
// Tier 3 → full multi-agent Go()
func (c *Conductor) GoCascade(ctx context.Context, opts GoOpts) (*GoResult, error) {
	if opts.Goal == "" {
		return nil, fmt.Errorf("goal is required")
	}

	// Expand @file references
	opts.Goal = expandFileReferences(opts.Goal, c.RepoRoot)

	// Estimate complexity
	estimate, err := c.estimateComplexity(ctx, opts.Goal)
	if err != nil {
		c.log("Cascade: complexity estimation failed, falling back to full Go(): %v", err)
		return c.Go(ctx, opts)
	}

	c.log("Cascade: estimated %d files (tier %d, cohesion=%s): %s", estimate.EstimatedFiles, estimate.Tier, estimate.SemanticCohesion, estimate.Reasoning)
	c.DB.LogEvent(ctx, "cascade_estimate", "", "", fmt.Sprintf(
		`{"files":%d,"tier":%d,"cohesion":"%s","reasoning":"%s"}`, estimate.EstimatedFiles, estimate.Tier, estimate.SemanticCohesion, estimate.Reasoning))

	// Auto-enable defense enforcement for Tier 3 when user didn't explicitly set it.
	// Experiment 097: enforcement is neutral/harmful at low parallelism but valuable at 3+ agents.
	if opts.FileEnforcement == "" && estimate.Tier == Tier3Complex {
		opts.FileEnforcement = "defense"
		c.log("Cascade: auto-enabling defense enforcement for Tier 3")
	}

	switch estimate.Tier {
	case Tier1Simple:
		c.log("Cascade: Tier 1 — single agent")
		result, err := c.goSingleAgent(ctx, opts)
		if err != nil || (result != nil && result.FailedCount > 0) {
			c.log("Cascade: Tier 1 failed, escalating to iterative")
			return c.goIterative(ctx, opts)
		}
		return result, nil

	case Tier2Medium:
		c.log("Cascade: Tier 2 — iterative mode")
		result, err := c.goIterative(ctx, opts)
		if err != nil || (result != nil && result.FailedCount > 0 && result.DoneCount == 0) {
			c.log("Cascade: Tier 2 failed, escalating to full multi-agent")
			return c.Go(ctx, opts)
		}
		return result, nil

	default: // Tier3Complex
		c.log("Cascade: Tier 3 — full multi-agent pipeline")
		return c.Go(ctx, opts)
	}
}
