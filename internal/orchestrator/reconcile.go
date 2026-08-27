package orchestrator

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/MochaCosine1206/orchestra/internal/agent"
	"github.com/MochaCosine1206/orchestra/internal/db"
)

// ReconcileOpts configures post-session reconciliation.
type ReconcileOpts struct {
	DryRun    bool
	SkipLLM   bool
	SessionID string // empty = current session
}

// ReconcileResult captures the outcome of post-session reconciliation.
type ReconcileResult struct {
	OrphanWorktrees    []string
	SalvagedWorktrees  []string
	CleanedWorktrees   []string
	TaskSummaries      map[string]string // taskID → result_summary
	FailedTasks        []FailedTaskInfo
	GoalAlignmentScore float64 // 0.0–1.0
	GoalAlignmentNote  string  // LLM analysis (if partial)
	GapsIdentified     []string
	FollowUpGoal       string
	FollowUpTaskIDs    []string
	Report             string
}

// FailedTaskInfo captures information about a failed task for reconciliation.
type FailedTaskInfo struct {
	TaskID      string
	Title       string
	FailureType string
	HasPartial  bool
}

// Reconcile performs post-session analysis: scan for orphan worktrees, salvage
// uncommitted changes, score goal alignment, identify gaps, and optionally
// generate follow-up tasks.
func (c *Conductor) Reconcile(ctx context.Context, opts ReconcileOpts) (*ReconcileResult, error) {
	result := &ReconcileResult{
		TaskSummaries: make(map[string]string),
	}

	// Determine which session to reconcile
	sessionTaskIDs := c.getReconcileTaskIDs(ctx, opts.SessionID)
	if len(sessionTaskIDs) == 0 {
		result.Report = "No session tasks found to reconcile."
		return result, nil
	}

	// Step 1: Scan for orphan worktrees
	orphans := c.scanOrphanWorktrees(ctx, sessionTaskIDs)
	result.OrphanWorktrees = orphans

	// Step 2: Salvage orphan worktrees
	if !opts.DryRun {
		salvaged, cleaned := c.salvageOrphanWorktrees(ctx, orphans)
		result.SalvagedWorktrees = salvaged
		result.CleanedWorktrees = cleaned
	}

	// Step 3: Collect task summaries
	c.collectTaskSummaries(ctx, sessionTaskIDs, result)

	// Step 4: Score goal alignment
	c.scoreGoalAlignment(ctx, sessionTaskIDs, result, opts.SkipLLM)

	// Step 5: Generate follow-up
	if len(result.GapsIdentified) > 0 && !opts.DryRun {
		c.generateFollowUp(ctx, result)
	}

	// Step 6: Build report
	goal := c.getBlackboard(ctx, "conductor:goal")
	result.Report = c.buildReconcileReport(result, goal, sessionTaskIDs)

	// Store report in blackboard
	sessionID := opts.SessionID
	if sessionID == "" {
		sessionID = c.SessionID
	}
	c.setBlackboard(ctx, "reconcile:report:"+sessionID, result.Report)

	return result, nil
}

// getReconcileTaskIDs returns task IDs for the session being reconciled.
func (c *Conductor) getReconcileTaskIDs(ctx context.Context, sessionID string) []string {
	if sessionID != "" {
		// Look up task IDs from a specific session stored in blackboard
		raw := c.getBlackboard(ctx, "conductor:task_ids")
		return parseTaskIDsJSON(raw)
	}
	return c.sessionTaskIDs(ctx)
}

// scanOrphanWorktrees finds worktrees that exist on disk but don't belong to
// any active (non-terminal) task in the session.
func (c *Conductor) scanOrphanWorktrees(ctx context.Context, sessionTaskIDs []string) []string {
	worktreeDir := filepath.Join(c.RepoRoot, ".worktree")
	entries, err := os.ReadDir(worktreeDir)
	if err != nil {
		return nil // no .worktree directory
	}

	// Build set of session task IDs
	sessionSet := make(map[string]bool)
	for _, id := range sessionTaskIDs {
		sessionSet[id] = true
	}

	// Build set of task IDs that have active worktrees in the DB
	tasks, _ := c.DB.ListTasks(ctx)
	activeWorktrees := make(map[string]bool)
	for _, t := range tasks {
		if t.Worktree.Valid && (t.Status == "running" || t.Status == "assigned" || t.Status == "pending") {
			// Extract task ID from worktree path (last path component)
			base := filepath.Base(t.Worktree.String)
			activeWorktrees[base] = true
		}
	}

	var orphans []string
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		name := entry.Name()
		// An orphan is a worktree directory that isn't tracked by any active task
		if !activeWorktrees[name] {
			orphans = append(orphans, filepath.Join(worktreeDir, name))
		}
	}
	return orphans
}

// salvageOrphanWorktrees attempts to salvage uncommitted changes from orphan
// worktrees, then removes them.
func (c *Conductor) salvageOrphanWorktrees(ctx context.Context, orphans []string) (salvaged, cleaned []string) {
	for _, wt := range orphans {
		taskID := filepath.Base(wt)

		// Check for uncommitted changes
		statusCmd := exec.CommandContext(ctx, "git", "status", "--porcelain")
		statusCmd.Dir = wt
		out, err := statusCmd.Output()
		if err == nil && len(out) > 0 {
			// Has uncommitted changes — salvage them
			agent.SalvageWorktreeChanges(ctx, wt, taskID)
			salvaged = append(salvaged, wt)
		}

		// Check if branch has commits ahead of base
		logCmd := exec.CommandContext(ctx, "git", "log", "--oneline", "HEAD", "--not", "--remotes", "-n", "1")
		logCmd.Dir = wt
		logOut, _ := logCmd.Output()
		if len(logOut) > 0 {
			// Branch has commits — create salvage branch at worktree's HEAD (not repo root HEAD)
			branchName := "salvage/" + taskID
			shaCmd := exec.CommandContext(ctx, "git", "rev-parse", "HEAD")
			shaCmd.Dir = wt
			if shaOut, err := shaCmd.Output(); err == nil {
				sha := strings.TrimSpace(string(shaOut))
				branchCmd := exec.CommandContext(ctx, "git", "branch", branchName, sha)
				branchCmd.Dir = c.RepoRoot
				branchCmd.CombinedOutput() // best-effort
			}
		}

		// Remove the worktree
		rmCmd := exec.CommandContext(ctx, "git", "worktree", "remove", "--force", wt)
		rmCmd.Dir = c.RepoRoot
		if rmOut, err := rmCmd.CombinedOutput(); err == nil {
			cleaned = append(cleaned, wt)
		} else {
			c.log("Failed to remove orphan worktree %s: %s", wt, string(rmOut))
		}
	}
	return
}

// collectTaskSummaries gathers result_summary, failure_type, and checkpoint
// data from the blackboard for each session task.
func (c *Conductor) collectTaskSummaries(ctx context.Context, sessionTaskIDs []string, result *ReconcileResult) {
	for _, taskID := range sessionTaskIDs {
		// Collect result summary
		summary := c.getBlackboard(ctx, "result_summary:"+taskID)
		if summary != "" {
			result.TaskSummaries[taskID] = summary
		}

		// Check for failed tasks
		task, err := c.DB.GetTaskByID(ctx, taskID)
		if err != nil || task == nil {
			continue
		}

		if task.Status == "failed" {
			failureType := c.getBlackboard(ctx, "failure_type:"+taskID)
			checkpoint := c.getBlackboard(ctx, "checkpoint:"+taskID)

			result.FailedTasks = append(result.FailedTasks, FailedTaskInfo{
				TaskID:      taskID,
				Title:       task.Title,
				FailureType: failureType,
				HasPartial:  checkpoint != "" || summary != "",
			})
		}
	}
}

// scoreGoalAlignment computes how well the session results align with the goal.
// Uses a heuristic score (done/total) and optionally an LLM for deeper analysis.
func (c *Conductor) scoreGoalAlignment(ctx context.Context, sessionTaskIDs []string, result *ReconcileResult, skipLLM bool) {
	doneCount, _ := c.DB.CountTasksByStatuses(ctx, []string{"done"}, sessionTaskIDs)
	totalCount := len(sessionTaskIDs)

	if totalCount == 0 {
		result.GoalAlignmentScore = 0
		return
	}

	// Heuristic score
	result.GoalAlignmentScore = float64(doneCount) / float64(totalCount)

	if result.GoalAlignmentScore >= 1.0 || skipLLM {
		if result.GoalAlignmentScore >= 1.0 {
			result.GoalAlignmentNote = "All tasks completed successfully."
		}
		return
	}

	// Use LLM for deeper analysis
	goal := c.getBlackboard(ctx, "conductor:goal")
	if goal == "" || c.Runner == nil {
		return
	}

	// Build context for LLM
	var summaryLines []string
	for id, summary := range result.TaskSummaries {
		task, _ := c.DB.GetTaskByID(ctx, id)
		title := id
		if task != nil {
			title = task.Title
		}
		summaryLines = append(summaryLines, fmt.Sprintf("- [done] %s: %s", title, summary))
	}
	for _, ft := range result.FailedTasks {
		summaryLines = append(summaryLines, fmt.Sprintf("- [failed] %s: %s", ft.Title, ft.FailureType))
	}

	prompt := fmt.Sprintf(`Analyze how well these task results align with the original goal.

Goal: %s

Task Results:
%s

Respond in JSON only:
{"score": 0.85, "gaps": ["gap 1", "gap 2"], "note": "brief analysis"}`, goal, strings.Join(summaryLines, "\n"))

	runResult, err := c.Runner.Run(ctx, RunOpts{
		Prompt:  prompt,
		WorkDir: c.RepoRoot,
	})
	if err != nil {
		// Fail-open: keep heuristic score
		c.log("LLM goal alignment analysis failed: %v", err)
		return
	}
	output := runResult.Output

	// Parse JSON response
	var analysis struct {
		Score float64  `json:"score"`
		Gaps  []string `json:"gaps"`
		Note  string   `json:"note"`
	}

	// Try to extract JSON from the output (LLM may include extra text)
	jsonStr := extractJSON(output)
	if err := json.Unmarshal([]byte(jsonStr), &analysis); err != nil {
		c.log("Failed to parse LLM alignment analysis: %v", err)
		return
	}

	result.GoalAlignmentScore = analysis.Score
	result.GoalAlignmentNote = analysis.Note
	result.GapsIdentified = analysis.Gaps
}

// extractJSON tries to find a JSON object in a string that may contain extra text.
func extractJSON(s string) string {
	start := strings.Index(s, "{")
	if start == -1 {
		return s
	}
	// Find matching closing brace
	depth := 0
	for i := start; i < len(s); i++ {
		switch s[i] {
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return s[start : i+1]
			}
		}
	}
	return s[start:]
}

// generateFollowUp creates follow-up tasks for identified gaps.
func (c *Conductor) generateFollowUp(ctx context.Context, result *ReconcileResult) {
	if len(result.GapsIdentified) == 0 {
		return
	}

	// Build a follow-up goal from gaps
	result.FollowUpGoal = "Follow-up: " + strings.Join(result.GapsIdentified, "; ")

	// Create follow-up tasks
	for i, gap := range result.GapsIdentified {
		taskID := db.GenID("t")
		task := db.Task{
			ID:          taskID,
			Title:       fmt.Sprintf("Follow-up %d: %s", i+1, truncate(gap, 80)),
			Description: sql.NullString{String: gap, Valid: true},
			Status:      "pending",
			Priority:    3,
			Role:        "implementer",
		}

		if err := c.DB.CreateTask(ctx, task); err != nil {
			c.log("Failed to create follow-up task: %v", err)
			continue
		}
		result.FollowUpTaskIDs = append(result.FollowUpTaskIDs, taskID)
	}
}

// buildReconcileReport generates a markdown-formatted reconciliation report.
func (c *Conductor) buildReconcileReport(result *ReconcileResult, goal string, sessionTaskIDs []string) string {
	var b strings.Builder

	b.WriteString("# Post-Session Reconciliation Report\n\n")

	if goal != "" {
		b.WriteString(fmt.Sprintf("**Goal:** %s\n\n", goal))
	}

	// Completion
	totalCount := len(sessionTaskIDs)
	doneCount := totalCount - len(result.FailedTasks)
	b.WriteString(fmt.Sprintf("**Completion:** %d/%d tasks (%.0f%%)\n",
		doneCount, totalCount, result.GoalAlignmentScore*100))
	b.WriteString(fmt.Sprintf("**Alignment Score:** %.2f\n\n", result.GoalAlignmentScore))

	if result.GoalAlignmentNote != "" {
		b.WriteString(fmt.Sprintf("**Analysis:** %s\n\n", result.GoalAlignmentNote))
	}

	// Task summaries
	if len(result.TaskSummaries) > 0 {
		b.WriteString("## Completed Tasks\n\n")
		for id, summary := range result.TaskSummaries {
			b.WriteString(fmt.Sprintf("- **%s**: %s\n", id, truncate(summary, 120)))
		}
		b.WriteString("\n")
	}

	// Failed tasks
	if len(result.FailedTasks) > 0 {
		b.WriteString("## Failed Tasks\n\n")
		for _, ft := range result.FailedTasks {
			partial := ""
			if ft.HasPartial {
				partial = " (has partial results)"
			}
			b.WriteString(fmt.Sprintf("- **%s** [%s]: %s%s\n", ft.TaskID, ft.FailureType, ft.Title, partial))
		}
		b.WriteString("\n")
	}

	// Orphan worktrees
	if len(result.OrphanWorktrees) > 0 {
		b.WriteString("## Orphan Worktrees\n\n")
		b.WriteString(fmt.Sprintf("Found %d orphan worktrees.\n", len(result.OrphanWorktrees)))
		if len(result.SalvagedWorktrees) > 0 {
			b.WriteString(fmt.Sprintf("Salvaged: %d\n", len(result.SalvagedWorktrees)))
		}
		if len(result.CleanedWorktrees) > 0 {
			b.WriteString(fmt.Sprintf("Cleaned: %d\n", len(result.CleanedWorktrees)))
		}
		b.WriteString("\n")
	}

	// Gaps and follow-up
	if len(result.GapsIdentified) > 0 {
		b.WriteString("## Identified Gaps\n\n")
		for _, gap := range result.GapsIdentified {
			b.WriteString(fmt.Sprintf("- %s\n", gap))
		}
		b.WriteString("\n")
	}

	if result.FollowUpGoal != "" {
		b.WriteString(fmt.Sprintf("## Follow-Up\n\n**Goal:** %s\n", result.FollowUpGoal))
		if len(result.FollowUpTaskIDs) > 0 {
			b.WriteString(fmt.Sprintf("**Tasks Created:** %s\n", strings.Join(result.FollowUpTaskIDs, ", ")))
		}
		b.WriteString("\n")
	}

	return b.String()
}

// truncate shortens a string to maxLen characters.
func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen-3] + "..."
}

