package orchestrator

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/MochaCosine1206/orchestra/internal/agent"
)

const (
	defaultReviewTimeout = 5 * time.Minute
)

// ReviewMode selects the review strategy.
type ReviewMode int

const (
	ReviewModeDefault    ReviewMode = iota // generic bug-finding review
	ReviewModeSpecDiff                     // anchored to task spec acceptance criteria
	ReviewModeStructured                   // structured JSON output with severity/confidence
)

// ReviewOpts configures a review invocation.
type ReviewOpts struct {
	TaskID  string
	Enabled bool
	Timeout time.Duration
	Mode    ReviewMode
}

// ReviewResult captures the outcome of a review.
type ReviewResult struct {
	Approved bool
	Feedback string
	Skipped  bool   // true if review was disabled or timed out
	Reason   string // why skipped: "disabled", "timeout", "error"
}

const (
	reviewPromptTemplate = `You are a code reviewer. Review the following task's output for quality, correctness, and security.

TASK: %s
DESCRIPTION: %s
BRANCH: %s

Review the changes in this branch compared to the base branch (%s).

Respond with a JSON object:
{
  "approved": true/false,
  "feedback": "Your detailed feedback here"
}

Be strict but fair. Approve if the changes accomplish the task goal without introducing bugs or security issues.`

	specDiffReviewPromptTemplate = `You are a spec-anchored code reviewer. Review the changes against the TASK SPEC below.

TASK: %s
DESCRIPTION: %s
BRANCH: %s

## TASK SPEC (acceptance criteria)
%s

Review whether the implementation satisfies each acceptance criterion in the spec.
Focus on spec compliance, not general code style.

Respond with a JSON object:
{
  "approved": true/false,
  "feedback": "Which criteria are met/unmet and why"
}

Approve if all acceptance criteria are satisfied. Reject only if specific criteria are unmet.`

	structuredReviewPromptTemplate = `You are a structured code reviewer. Analyze the changes and produce findings with severity and confidence scores.

TASK: %s
DESCRIPTION: %s
BRANCH: %s

Review the changes in this branch compared to the base branch (%s).

Respond with a JSON object:
{
  "approved": true/false,
  "findings": [
    {
      "severity": "critical|high|medium|low|info",
      "confidence": 0.0-1.0,
      "file": "path/to/file.go",
      "line_start": 10,
      "line_end": 15,
      "category": "bug|security|performance|correctness|style",
      "description": "What the issue is",
      "suggestion": "How to fix it"
    }
  ],
  "feedback": "Overall summary"
}

Only mark approved=false if there are critical or high severity findings with confidence >= 0.7.`
)

// ReviewFinding represents a single structured review finding.
type ReviewFinding struct {
	Severity    string  `json:"severity"`
	Confidence  float64 `json:"confidence"`
	File        string  `json:"file"`
	LineStart   int     `json:"line_start"`
	LineEnd     int     `json:"line_end"`
	Category    string  `json:"category"`
	Description string  `json:"description"`
	Suggestion  string  `json:"suggestion"`
}

// StructuredReviewResult extends ReviewResult with parsed findings.
type StructuredReviewResult struct {
	*ReviewResult
	Findings []ReviewFinding
}

// Review runs a review gate for a completed task. Fail-open design: errors/timeouts proceed.
func (c *Conductor) Review(ctx context.Context, opts ReviewOpts) *ReviewResult {
	if !opts.Enabled {
		return &ReviewResult{Approved: true, Skipped: true, Reason: "disabled"}
	}

	task, err := c.DB.GetTaskByID(ctx, opts.TaskID)
	if err != nil || task == nil {
		c.log("Review: task %s not found, proceeding (fail-open)", opts.TaskID)
		return &ReviewResult{Approved: true, Skipped: true, Reason: "task_not_found"}
	}

	if c.Runner == nil {
		c.log("Review: no runner configured, proceeding (fail-open)")
		return &ReviewResult{Approved: true, Skipped: true, Reason: "no_runner"}
	}

	timeout := opts.Timeout
	if timeout <= 0 {
		timeout = defaultReviewTimeout
	}

	reviewCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	title := task.Title
	desc := ""
	if task.Description.Valid {
		desc = task.Description.String
	}
	branch := ""
	if task.Branch.Valid {
		branch = task.Branch.String
	}

	// Resolve base branch from blackboard or detect from repo
	baseBranch := c.getBlackboard(ctx, "conductor:base_branch")
	if baseBranch == "" {
		baseBranch = agent.DetectBaseBranch(c.RepoRoot)
	}

	// Select prompt based on review mode
	var prompt string
	switch opts.Mode {
	case ReviewModeSpecDiff:
		spec := c.getBlackboard(ctx, "spec:"+opts.TaskID)
		if spec == "" {
			c.log("Review: no spec found for %s, falling back to default mode", opts.TaskID)
			prompt = fmt.Sprintf(reviewPromptTemplate, title, desc, branch, baseBranch)
		} else {
			prompt = fmt.Sprintf(specDiffReviewPromptTemplate, title, desc, branch, spec)
		}
	case ReviewModeStructured:
		prompt = fmt.Sprintf(structuredReviewPromptTemplate, title, desc, branch, baseBranch)
	default:
		prompt = fmt.Sprintf(reviewPromptTemplate, title, desc, branch, baseBranch)
	}

	workDir := c.RepoRoot
	if task.Worktree.Valid && task.Worktree.String != "" {
		workDir = task.Worktree.String
	}

	runResult, err := c.Runner.Run(reviewCtx, RunOpts{
		Prompt:  prompt,
		Model:   agent.ModelOpus,
		WorkDir: workDir,
	})
	if err != nil {
		// Fail-open: reviewer crash/timeout -> proceed
		reason := "error"
		if reviewCtx.Err() != nil {
			reason = "timeout"
		}
		c.log("Review failed for %s (fail-open): %v", opts.TaskID, err)
		return &ReviewResult{Approved: true, Skipped: true, Reason: reason}
	}

	// Parse review output
	output := runResult.Output
	var result *ReviewResult
	if opts.Mode == ReviewModeStructured {
		result = parseStructuredReviewOutput(output)
	} else {
		result = parseReviewOutput(output)
	}

	// Store in blackboard
	status := "rejected"
	if result.Approved {
		status = "approved"
	}
	c.setBlackboard(ctx, "review:"+opts.TaskID, status)
	if result.Feedback != "" {
		c.setBlackboard(ctx, "review_feedback:"+opts.TaskID, result.Feedback)
	}
	c.DB.LogEvent(ctx, "review_completed", "", opts.TaskID,
		fmt.Sprintf(`{"approved":%t}`, result.Approved))

	return result
}

// parseReviewOutput extracts approval and feedback from Claude's review output.
func parseReviewOutput(output string) *ReviewResult {
	output = strings.TrimSpace(output)
	lower := strings.ToLower(output)

	result := &ReviewResult{}

	// Try to find "approved": true/false in the output
	if strings.Contains(lower, `"approved": true`) || strings.Contains(lower, `"approved":true`) {
		result.Approved = true
	} else if strings.Contains(lower, `"approved": false`) || strings.Contains(lower, `"approved":false`) {
		result.Approved = false
	} else {
		// Heuristic: look for approval/rejection keywords
		result.Approved = !strings.Contains(lower, "reject") &&
			!strings.Contains(lower, "not approved") &&
			!strings.Contains(lower, "changes needed")
	}

	// Extract feedback
	if idx := strings.Index(lower, `"feedback"`); idx >= 0 {
		// Find the value after "feedback":
		rest := output[idx:]
		if colonIdx := strings.Index(rest, ":"); colonIdx >= 0 {
			feedbackStart := rest[colonIdx+1:]
			feedbackStart = strings.TrimSpace(feedbackStart)
			feedbackStart = strings.TrimPrefix(feedbackStart, `"`)
			if endIdx := strings.Index(feedbackStart, `"`); endIdx >= 0 {
				result.Feedback = feedbackStart[:endIdx]
			} else {
				result.Feedback = feedbackStart
			}
		}
	}

	if result.Feedback == "" {
		result.Feedback = output
	}

	return result
}

// parseStructuredReviewOutput extracts structured findings from Claude's JSON review output.
// Falls back to parseReviewOutput if JSON extraction fails.
func parseStructuredReviewOutput(output string) *ReviewResult {
	jsonStr := extractJSON(output)

	var parsed struct {
		Approved bool            `json:"approved"`
		Findings []ReviewFinding `json:"findings"`
		Feedback string          `json:"feedback"`
	}

	if err := json.Unmarshal([]byte(jsonStr), &parsed); err != nil {
		// Fall back to basic parsing
		return parseReviewOutput(output)
	}

	result := &ReviewResult{
		Approved: parsed.Approved,
		Feedback: parsed.Feedback,
	}

	if result.Feedback == "" && len(parsed.Findings) > 0 {
		var parts []string
		for _, f := range parsed.Findings {
			parts = append(parts, fmt.Sprintf("[%s/%s] %s", f.Severity, f.Category, f.Description))
		}
		result.Feedback = strings.Join(parts, "; ")
	}

	return result
}
