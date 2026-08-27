package orchestrator

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/MochaCosine1206/orchestra/internal/agent"
)

// ABTestOpts configures an A/B test run.
type ABTestOpts struct {
	Goal             string
	TestCmd          string
	Runs             int
	Routing          string // model routing strategy
	DryRun           bool
	Clarify          bool // if true: Arm A = no clarify, Arm B = with clarify
	MCPTest          bool // if true: Arm A = with MCP tools, Arm B = without MCP tools
	ReviewTest       bool // if true: Arm A = default review, Arm B = spec-diff review
	StructuredReview bool // if true: Arm A = default review, Arm B = structured JSON review
	Cascade          bool // if true: Arm A = Go(), Arm B = GoCascade()
	TestCmdTimeout   int  // seconds; 0 = default (300s)
}

// ABTestResult captures all runs and aggregated summary.
type ABTestResult struct {
	Runs    []ABTestRun
	Summary ABTestSummary
}

// ABTestRun captures one A/B comparison run.
type ABTestRun struct {
	RunNumber  int
	ArmA       ArmResult
	ArmB       ArmResult
	Comparison ABComparison
}

// ArmResult captures the outcome of one arm.
type ArmResult struct {
	Mode         string        `json:"mode"` // "multi-agent" or "single-agent"
	Passed       bool          `json:"passed"`
	WallClock    time.Duration `json:"wall_clock_s"`
	TaskCount    int           `json:"task_count"`
	DoneCount    int           `json:"done_count"`
	FailedCount  int           `json:"failed_count"`
	Error        string        `json:"error,omitempty"`
	ClarifyUsed  bool          `json:"clarify_used"`
	ClarifyCount int           `json:"clarify_question_count"`
	MCPEnabled   bool          `json:"mcp_enabled"`

	// Rubric metrics (captured automatically)
	LinesChanged     int     `json:"lines_changed"` // insertions + deletions
	FilesChanged     int     `json:"files_changed"`
	RetryCount       int     `json:"retry_count"`       // refinement rounds needed
	Score            float64 `json:"score"`             // weighted rubric score (0-10)
	ReviewFindings   int     `json:"review_findings"`   // structured review: total findings
	ReviewActionable int     `json:"review_actionable"` // structured review: actionable (high/critical)
}

// ABComparison derives comparison metrics between arms.
type ABComparison struct {
	Winner    string  `json:"winner"` // "arm_a", "arm_b", "tie", "both_failed"
	TimeRatio float64 `json:"time_ratio"`
	ScoreA    float64 `json:"score_a"`
	ScoreB    float64 `json:"score_b"`
	Notes     string  `json:"notes"`
}

// ABTestSummary aggregates results across multiple runs.
type ABTestSummary struct {
	TotalRuns  int
	ArmAWins   int
	ArmBWins   int
	Ties       int
	BothFailed int
}

// ABTest runs A/B comparison: multi-agent (Arm A) vs single-agent (Arm B).
func (c *Conductor) ABTest(ctx context.Context, opts ABTestOpts) (*ABTestResult, error) {
	if opts.Goal == "" {
		return nil, fmt.Errorf("goal is required")
	}

	// Expand @file references in goal text (keep original for display)
	displayGoal := opts.Goal
	opts.Goal = expandFileReferences(opts.Goal, c.RepoRoot)

	if opts.TestCmd == "" {
		return nil, fmt.Errorf("test-cmd is required")
	}

	runs := opts.Runs
	if runs <= 0 {
		runs = 1
	}

	result := &ABTestResult{}

	c.log("A/B Test: %d runs, goal=%q", runs, displayGoal)
	c.log("Test command: %s", opts.TestCmd)

	if opts.DryRun {
		c.log("Dry-run: would run %d A/B test(s)", runs)
		if opts.Cascade {
			c.log("  Arm A: multi-agent pipeline (Go)")
			c.log("  Arm B: cascade routing (GoCascade)")
		} else if opts.StructuredReview {
			c.log("  Arm A: multi-agent pipeline (default review)")
			c.log("  Arm B: multi-agent pipeline (structured JSON review)")
		} else if opts.ReviewTest {
			c.log("  Arm A: multi-agent pipeline (default review)")
			c.log("  Arm B: multi-agent pipeline (spec-diff review)")
		} else if opts.MCPTest {
			c.log("  Arm A: multi-agent pipeline (with MCP tools)")
			c.log("  Arm B: multi-agent pipeline (without MCP tools)")
		} else if opts.Clarify {
			c.log("  Arm A: multi-agent pipeline (no clarification)")
			c.log("  Arm B: multi-agent pipeline (with clarification)")
		} else {
			c.log("  Arm A: multi-agent pipeline (decompose → spawn → merge)")
			c.log("  Arm B: single-agent (one Claude session)")
		}
		c.log("  Verify: %s", opts.TestCmd)
		result.Summary = ABTestSummary{TotalRuns: runs}
		return result, nil
	}

	for run := 1; run <= runs; run++ {
		c.log("Run %d/%d", run, runs)

		runResult := ABTestRun{RunNumber: run}

		// Save git state before arms
		baseHash, _ := gitExec(c.RepoRoot, "rev-parse", "HEAD")
		baseHash = strings.TrimSpace(baseHash)

		if opts.Cascade {
			// Cascade A/B: Arm A = Go(), Arm B = GoCascade()
			c.log("  Arm A: multi-agent pipeline (Go)")
			runResult.ArmA = c.runArmA(ctx, opts)
			c.captureArmMetrics(&runResult.ArmA, baseHash)

			c.resetToCommit(baseHash)

			c.log("  Arm B: cascade routing (GoCascade)")
			runResult.ArmB = c.runArmBCascade(ctx, opts)
			c.captureArmMetrics(&runResult.ArmB, baseHash)
		} else if opts.StructuredReview {
			// Structured review A/B: Arm A = default review, Arm B = structured JSON review
			c.log("  Arm A: multi-agent (default review)")
			runResult.ArmA = c.runArmAWithReview(ctx, opts, "default")
			c.captureArmMetrics(&runResult.ArmA, baseHash)

			c.resetToCommit(baseHash)

			c.log("  Arm B: multi-agent (structured review)")
			runResult.ArmB = c.runArmAWithReview(ctx, opts, "structured")
			c.captureArmMetrics(&runResult.ArmB, baseHash)
		} else if opts.ReviewTest {
			// Review A/B: Arm A = default review, Arm B = spec-diff review
			c.log("  Arm A: multi-agent (default review)")
			runResult.ArmA = c.runArmAWithReview(ctx, opts, "default")
			c.captureArmMetrics(&runResult.ArmA, baseHash)

			c.resetToCommit(baseHash)

			c.log("  Arm B: multi-agent (spec-diff review)")
			runResult.ArmB = c.runArmAWithReview(ctx, opts, "spec-diff")
			c.captureArmMetrics(&runResult.ArmB, baseHash)
		} else if opts.MCPTest {
			// MCP A/B: Arm A = with MCP, Arm B = without MCP
			c.log("  Arm A: multi-agent (with MCP tools)")
			runResult.ArmA = c.runArmAMCP(ctx, opts)
			c.captureArmMetrics(&runResult.ArmA, baseHash)

			c.resetToCommit(baseHash)

			c.log("  Arm B: multi-agent (without MCP tools)")
			runResult.ArmB = c.runArmBNoMCP(ctx, opts)
			c.captureArmMetrics(&runResult.ArmB, baseHash)
		} else if opts.Clarify {
			// Clarify A/B: Arm A = no clarify, Arm B = with clarify
			c.log("  Arm A: multi-agent (no clarification)")
			runResult.ArmA = c.runArmA(ctx, opts)
			c.captureArmMetrics(&runResult.ArmA, baseHash)

			c.resetToCommit(baseHash)

			c.log("  Arm B: multi-agent (with clarification)")
			runResult.ArmB = c.runArmBClarify(ctx, opts)
			c.captureArmMetrics(&runResult.ArmB, baseHash)
		} else {
			// Standard A/B: Arm A = multi-agent, Arm B = single-agent
			c.log("  Arm A: multi-agent pipeline")
			runResult.ArmA = c.runArmA(ctx, opts)
			c.captureArmMetrics(&runResult.ArmA, baseHash)

			c.resetToCommit(baseHash)

			c.log("  Arm B: single-agent")
			runResult.ArmB = c.runArmB(ctx, opts)
			c.captureArmMetrics(&runResult.ArmB, baseHash)
		}

		// Reset to base after Arm B
		c.resetToCommit(baseHash)

		// Compare
		runResult.Comparison = compareArms(runResult.ArmA, runResult.ArmB)
		c.log("  Winner: %s", runResult.Comparison.Winner)

		result.Runs = append(result.Runs, runResult)
	}

	result.Summary = summarizeRuns(result.Runs)

	c.log("A/B Summary: A=%d, B=%d, tie=%d, both_failed=%d",
		result.Summary.ArmAWins, result.Summary.ArmBWins,
		result.Summary.Ties, result.Summary.BothFailed)

	return result, nil
}

// runArmA executes the multi-agent pipeline.
func (c *Conductor) runArmA(ctx context.Context, opts ABTestOpts) ArmResult {
	start := time.Now()
	arm := ArmResult{Mode: "multi-agent"}

	goResult, err := c.Go(ctx, GoOpts{
		Goal:    opts.Goal,
		TestCmd: opts.TestCmd,
	})
	arm.WallClock = time.Since(start)

	if err != nil {
		arm.Error = err.Error()
		return arm
	}

	arm.TaskCount = goResult.TaskCount
	arm.DoneCount = goResult.DoneCount
	arm.FailedCount = goResult.FailedCount

	// Capture retry count from merge refinement
	if goResult.MergeResult != nil {
		arm.RetryCount = len(goResult.MergeResult.Refining)
	}

	// Run test command to verify
	if _, err := runTestCmd(c.RepoRoot, opts.TestCmd, abTestTimeout(opts)); err == nil {
		arm.Passed = true
	}

	return arm
}

// runArmB executes a single Claude session.
func (c *Conductor) runArmB(ctx context.Context, opts ABTestOpts) ArmResult {
	start := time.Now()
	arm := ArmResult{Mode: "single-agent", TaskCount: 1}

	if c.Runner == nil {
		arm.Error = "ClaudeRunner is required"
		arm.WallClock = time.Since(start)
		return arm
	}

	prompt := fmt.Sprintf(`You are working on the following goal as a single agent.

GOAL: %s

Implement the changes needed. Commit your work when done.
If a test command is provided, make sure your changes pass: %s`, opts.Goal, opts.TestCmd)

	model := agent.ModelOpus
	if opts.Routing != "" {
		model = opts.Routing
	}

	_, err := c.Runner.Run(ctx, RunOpts{
		Prompt:  prompt,
		Model:   model,
		WorkDir: c.RepoRoot,
	})
	arm.WallClock = time.Since(start)

	if err != nil {
		arm.Error = err.Error()
		arm.FailedCount = 1
		return arm
	}

	arm.DoneCount = 1

	// Run test command to verify
	if _, err := runTestCmd(c.RepoRoot, opts.TestCmd, abTestTimeout(opts)); err == nil {
		arm.Passed = true
	}

	return arm
}

// runArmBClarify executes the multi-agent pipeline with clarification enabled.
func (c *Conductor) runArmBClarify(ctx context.Context, opts ABTestOpts) ArmResult {
	start := time.Now()
	arm := ArmResult{Mode: "multi-agent-clarified", ClarifyUsed: true}

	goResult, err := c.Go(ctx, GoOpts{
		Goal:        opts.Goal,
		TestCmd:     opts.TestCmd,
		Clarify:     true,
		ClarifyMode: ClarifyAuto,
	})
	arm.WallClock = time.Since(start)

	if err != nil {
		arm.Error = err.Error()
		return arm
	}

	arm.TaskCount = goResult.TaskCount
	arm.DoneCount = goResult.DoneCount
	arm.FailedCount = goResult.FailedCount

	// Read clarification count from blackboard
	clarifyJSON := c.getBlackboard(ctx, "conductor:clarifications")
	if clarifyJSON != "" {
		var questions []ClarifyQuestion
		if json.Unmarshal([]byte(clarifyJSON), &questions) == nil {
			arm.ClarifyCount = len(questions)
		}
	}

	// Run test command to verify
	if _, err := runTestCmd(c.RepoRoot, opts.TestCmd, abTestTimeout(opts)); err == nil {
		arm.Passed = true
	}

	return arm
}

// runArmAMCP executes the multi-agent pipeline with MCP tools enabled (default).
func (c *Conductor) runArmAMCP(ctx context.Context, opts ABTestOpts) ArmResult {
	arm := c.runArmA(ctx, opts)
	arm.MCPEnabled = true
	return arm
}

// runArmBNoMCP executes the multi-agent pipeline without MCP tools.
func (c *Conductor) runArmBNoMCP(ctx context.Context, opts ABTestOpts) ArmResult {
	c.setBlackboard(ctx, "conductor:skip_mcp", "1")
	defer c.DB.DeleteBlackboard(ctx, "conductor:skip_mcp")
	arm := c.runArmA(ctx, opts) // Same pipeline, just without MCP
	arm.MCPEnabled = false
	arm.Mode = "no-mcp"
	return arm
}

// captureArmMetrics fills in diff stats and rubric score for an arm result.
// baseHash is the commit before the arm ran; current HEAD has the arm's changes.
func (c *Conductor) captureArmMetrics(arm *ArmResult, baseHash string) {
	if baseHash == "" {
		return
	}

	// Get diff stats: files changed, lines changed
	stat, err := gitExec(c.RepoRoot, "diff", "--shortstat", baseHash+"..HEAD")
	if err == nil {
		arm.FilesChanged, arm.LinesChanged = parseDiffStat(stat)
	}

	// Score the arm
	arm.Score = scoreArm(*arm)
}

// parseDiffStat extracts files changed and lines changed from git diff --shortstat.
// Example: " 3 files changed, 45 insertions(+), 12 deletions(-)"
func parseDiffStat(stat string) (files, lines int) {
	stat = strings.TrimSpace(stat)
	if stat == "" {
		return 0, 0
	}
	// Parse "N file(s) changed"
	if idx := strings.Index(stat, " file"); idx > 0 {
		fmt.Sscanf(stat[:idx], "%d", &files)
	}
	// Parse insertions + deletions
	var ins, del int
	if idx := strings.Index(stat, " insertion"); idx > 0 {
		// Find the number before "insertion"
		sub := stat[:idx]
		if comma := strings.LastIndex(sub, ", "); comma >= 0 {
			fmt.Sscanf(sub[comma+2:], "%d", &ins)
		} else if comma := strings.LastIndex(sub, " "); comma >= 0 {
			fmt.Sscanf(sub[comma+1:], "%d", &ins)
		}
	}
	if idx := strings.Index(stat, " deletion"); idx > 0 {
		sub := stat[:idx]
		if comma := strings.LastIndex(sub, ", "); comma >= 0 {
			fmt.Sscanf(sub[comma+2:], "%d", &del)
		} else if comma := strings.LastIndex(sub, " "); comma >= 0 {
			fmt.Sscanf(sub[comma+1:], "%d", &del)
		}
	}
	lines = ins + del
	return files, lines
}

// scoreArm computes a weighted rubric score (0-10) for an arm result.
//
// Dimensions and weights:
//
//	Correctness (3x): tests pass = 3, fail = 0
//	Speed (2x):       scored relative (caller handles comparison)
//	Precision (2x):   fewer lines/files = better (scaled 0-2)
//	Retries (1x):     0 retries = 1, any retries = 0
//
// Speed is scored comparatively in compareArms, not here.
// This returns the non-comparative components (max 6 out of 10).
func scoreArm(arm ArmResult) float64 {
	var score float64

	// Correctness (0 or 3)
	if arm.Passed {
		score += 3.0
	}

	// Precision (0-2): fewer changes = better
	// Heuristic: <50 lines = 2, 50-200 = 1.5, 200-500 = 1, 500+ = 0.5
	switch {
	case arm.LinesChanged == 0 && arm.Passed:
		score += 2.0 // clean pass with no changes is ideal
	case arm.LinesChanged < 50:
		score += 2.0
	case arm.LinesChanged < 200:
		score += 1.5
	case arm.LinesChanged < 500:
		score += 1.0
	default:
		score += 0.5
	}

	// Retries (0 or 1)
	if arm.RetryCount == 0 {
		score += 1.0
	}

	return score
}

// resetToCommit resets the working tree to a specific commit.
func (c *Conductor) resetToCommit(hash string) {
	if hash == "" {
		return
	}
	// Safety: stash any uncommitted work before destructive reset.
	// This prevents data loss if the user has in-progress changes.
	stashOut, _ := gitExec(c.RepoRoot, "stash", "push", "-m", "orchestra-abtest-safety-stash")
	if stashOut != "" && stashOut != "No local changes to save" {
		c.log("resetToCommit: stashed uncommitted work: %s", stashOut)
	}
	gitExec(c.RepoRoot, "reset", "--hard", hash) //nolint:errcheck
	gitExec(c.RepoRoot, "clean", "-fd")          //nolint:errcheck
}

// compareArms derives comparison metrics using weighted rubric scoring.
func compareArms(a, b ArmResult) ABComparison {
	cmp := ABComparison{}

	// Time ratio (a/b, < 1 means A is faster)
	if b.WallClock > 0 {
		cmp.TimeRatio = float64(a.WallClock) / float64(b.WallClock)
	}

	// Start with base scores from scoreArm (correctness + precision + retries = max 6)
	scoreA := a.Score
	scoreB := b.Score

	// Add speed component (2 points): faster arm gets 2, slower gets 1, >2x slower gets 0
	if a.Passed || b.Passed { // only score speed if at least one passed
		switch {
		case a.WallClock < b.WallClock:
			scoreA += 2.0
			if cmp.TimeRatio > 0 && cmp.TimeRatio < 0.5 {
				scoreB += 0 // >2x slower
			} else {
				scoreB += 1.0
			}
		case b.WallClock < a.WallClock:
			scoreB += 2.0
			if cmp.TimeRatio > 2.0 {
				scoreA += 0 // >2x slower
			} else {
				scoreA += 1.0
			}
		default:
			scoreA += 1.5
			scoreB += 1.5
		}
	}

	cmp.ScoreA = scoreA
	cmp.ScoreB = scoreB

	// Determine winner by score (pass/fail takes priority)
	switch {
	case !a.Passed && !b.Passed:
		cmp.Winner = "both_failed"
		cmp.Notes = "neither arm passed"
	case a.Passed && !b.Passed:
		cmp.Winner = "arm_a"
		cmp.Notes = fmt.Sprintf("A passed, B failed (A=%.1f, B=%.1f)", scoreA, scoreB)
	case !a.Passed && b.Passed:
		cmp.Winner = "arm_b"
		cmp.Notes = fmt.Sprintf("B passed, A failed (A=%.1f, B=%.1f)", scoreA, scoreB)
	case scoreA > scoreB:
		cmp.Winner = "arm_a"
		cmp.Notes = fmt.Sprintf("A scored %.1f vs B %.1f", scoreA, scoreB)
	case scoreB > scoreA:
		cmp.Winner = "arm_b"
		cmp.Notes = fmt.Sprintf("B scored %.1f vs A %.1f", scoreB, scoreA)
	default:
		cmp.Winner = "tie"
		cmp.Notes = fmt.Sprintf("tied at %.1f", scoreA)
	}

	return cmp
}

// summarizeRuns aggregates comparison results across runs.
func summarizeRuns(runs []ABTestRun) ABTestSummary {
	s := ABTestSummary{TotalRuns: len(runs)}
	for _, r := range runs {
		switch r.Comparison.Winner {
		case "arm_a":
			s.ArmAWins++
		case "arm_b":
			s.ArmBWins++
		case "tie":
			s.Ties++
		case "both_failed":
			s.BothFailed++
		}
	}
	return s
}

// FormatABTestResult returns a human-readable summary.
func FormatABTestResult(r *ABTestResult) string {
	if r == nil {
		return "No results"
	}

	var b strings.Builder
	b.WriteString(fmt.Sprintf("A/B Test: %d run(s)\n", r.Summary.TotalRuns))
	b.WriteString(fmt.Sprintf("  Arm A wins: %d\n", r.Summary.ArmAWins))
	b.WriteString(fmt.Sprintf("  Arm B wins: %d\n", r.Summary.ArmBWins))
	b.WriteString(fmt.Sprintf("  Ties: %d\n", r.Summary.Ties))
	b.WriteString(fmt.Sprintf("  Both failed: %d\n", r.Summary.BothFailed))

	for _, run := range r.Runs {
		b.WriteString(fmt.Sprintf("\nRun %d: winner=%s (A=%.1f, B=%.1f)\n",
			run.RunNumber, run.Comparison.Winner, run.Comparison.ScoreA, run.Comparison.ScoreB))
		b.WriteString(formatArmLine("A", run.ArmA))
		b.WriteString(formatArmLine("B", run.ArmB))
		if run.Comparison.Notes != "" {
			b.WriteString(fmt.Sprintf("  Note: %s\n", run.Comparison.Notes))
		}
	}

	return b.String()
}

// formatArmLine formats a single arm result for display.
func formatArmLine(label string, arm ArmResult) string {
	line := fmt.Sprintf("  %s: passed=%v, time=%s, lines=%d, files=%d, retries=%d",
		label, arm.Passed, arm.WallClock.Round(time.Second),
		arm.LinesChanged, arm.FilesChanged, arm.RetryCount)
	if arm.MCPEnabled {
		line += ", mcp=true"
	} else if arm.Mode == "no-mcp" {
		line += ", mcp=false"
	}
	if arm.ClarifyUsed {
		line += fmt.Sprintf(", clarify=%d", arm.ClarifyCount)
	}
	return line + "\n"
}

// runArmAWithReview executes the multi-agent pipeline with a specific review mode.
func (c *Conductor) runArmAWithReview(ctx context.Context, opts ABTestOpts, reviewMode string) ArmResult {
	// Set review mode in blackboard for merge.go to pick up
	c.setBlackboard(ctx, "conductor:review_mode", reviewMode)
	defer c.DB.DeleteBlackboard(ctx, "conductor:review_mode")

	start := time.Now()
	arm := ArmResult{Mode: "multi-agent-" + reviewMode}

	goResult, err := c.Go(ctx, GoOpts{
		Goal:    opts.Goal,
		TestCmd: opts.TestCmd,
		Review:  true, // Enable review gate
	})
	arm.WallClock = time.Since(start)

	if err != nil {
		arm.Error = err.Error()
		return arm
	}

	arm.TaskCount = goResult.TaskCount
	arm.DoneCount = goResult.DoneCount
	arm.FailedCount = goResult.FailedCount

	if goResult.MergeResult != nil {
		arm.RetryCount = len(goResult.MergeResult.Refining)
	}

	// Run test command to verify
	if _, err := runTestCmd(c.RepoRoot, opts.TestCmd, abTestTimeout(opts)); err == nil {
		arm.Passed = true
	}

	return arm
}

// runArmBCascade executes the cascade routing pipeline.
func (c *Conductor) runArmBCascade(ctx context.Context, opts ABTestOpts) ArmResult {
	start := time.Now()
	arm := ArmResult{Mode: "cascade"}

	goResult, err := c.GoCascade(ctx, GoOpts{
		Goal:    opts.Goal,
		TestCmd: opts.TestCmd,
	})
	arm.WallClock = time.Since(start)

	if err != nil {
		arm.Error = err.Error()
		return arm
	}

	arm.TaskCount = goResult.TaskCount
	arm.DoneCount = goResult.DoneCount
	arm.FailedCount = goResult.FailedCount

	// Run test command to verify
	if _, err := runTestCmd(c.RepoRoot, opts.TestCmd, abTestTimeout(opts)); err == nil {
		arm.Passed = true
	}

	return arm
}

// abTestTimeout returns the test command timeout for A/B tests.
func abTestTimeout(opts ABTestOpts) time.Duration {
	if opts.TestCmdTimeout > 0 {
		return time.Duration(opts.TestCmdTimeout) * time.Second
	}
	return 5 * time.Minute
}

// ABTestResultJSON returns the result as JSON matching the ab-result.json schema.
func ABTestResultJSON(r *ABTestResult) (string, error) {
	data, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return "", err
	}
	return string(data), nil
}
