package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/MochaCosine1206/orchestra/internal/db"
)

// SpecGenOpts configures spec generation.
type SpecGenOpts struct {
	DB       *db.DB
	RepoRoot string
	TaskID   string
}

// GenerateSpec produces a task specification from DB fields and blackboard entries.
func GenerateSpec(ctx context.Context, opts SpecGenOpts) (string, error) {
	d := opts.DB
	taskID := opts.TaskID

	task, err := d.GetTaskByID(ctx, taskID)
	if err != nil {
		return "", fmt.Errorf("looking up task %s: %w", taskID, err)
	}
	if task == nil {
		return "", fmt.Errorf("task %q not found", taskID)
	}

	// Resolve role: prefer agent's role, fall back to task.Role
	role, model := resolveRole(ctx, d, opts.RepoRoot, task)

	// Resolve base branch
	baseBranch := resolveBaseBranch(ctx, d)

	// Description with reference doc injection + truncation
	desc := task.Description.String
	if !task.Description.Valid || desc == "" {
		desc = "No description provided."
	}
	// G-CSS-4: Inject referenced documents into description so agents get content directly.
	desc = injectReferenceDocuments(desc, opts.RepoRoot)
	descTruncated := false
	origDescSize := len(desc)
	if origDescSize > 3000 {
		desc = truncateDescription(desc, origDescSize)
		descTruncated = true
	}

	// File locks
	lockTable := buildFileLockTable(ctx, d, taskID)

	// Inline schema
	schema, schemaWarning := buildSchemaSection(ctx, d, taskID)

	// Interface contracts
	contracts := buildContractSection(ctx, d, taskID)

	// Predecessor results
	predecessorSection := buildPredecessorSection(ctx, d, task)

	// Quality standards (researcher/architect only)
	qualitySection := buildQualitySection(role)

	// Self-check checklist (implementer/researcher/architect only)
	selfCheckSection := buildSelfCheckSection(role)

	// Retry context
	retrySection := buildRetrySection(ctx, d, taskID)

	// Refinement context (overrides retry section when present)
	refinementSection := buildRefinementSection(ctx, d, taskID)

	// Worktree paths
	worktreeDisplay := "not assigned"
	if task.Worktree.Valid && task.Worktree.String != "" {
		worktreeDisplay = task.Worktree.String
	}
	branchDisplay := "not assigned"
	if task.Branch.Valid && task.Branch.String != "" {
		branchDisplay = task.Branch.String
	}
	worktreeRoot := worktreeDisplay
	if !task.Worktree.Valid || task.Worktree.String == "" {
		worktreeRoot = opts.RepoRoot + "/not-assigned"
	}

	dependsDisplay := "none"
	if task.DependsOn.Valid && task.DependsOn.String != "" {
		dependsDisplay = task.DependsOn.String
	}
	blockedDisplay := "none"
	if task.BlockedBy.Valid && task.BlockedBy.String != "" {
		blockedDisplay = task.BlockedBy.String
	}

	// Collect file paths from locks for frontmatter
	locks, _ := d.ListFileLocksForTask(ctx, taskID)
	var lockPaths []string
	for _, l := range locks {
		lockPaths = append(lockPaths, l.FilePath)
	}

	// Session ID from blackboard
	sessionID, _ := d.GetBlackboardValue(ctx, "conductor:session_id")

	// Build spec
	var b strings.Builder
	b.WriteString(buildFrontmatter(taskID, task.Title, role, model, sessionID, task.DependsOn.String, lockPaths))
	fmt.Fprintf(&b, "# Task Specification: %s\n\n", taskID)
	b.WriteString("## Overview\n\n")
	b.WriteString("| Field | Value |\n|---|---|\n")
	fmt.Fprintf(&b, "| **Task ID** | `%s` |\n", taskID)
	fmt.Fprintf(&b, "| **Title** | %s |\n", task.Title)
	fmt.Fprintf(&b, "| **Assigned Role** | %s |\n", role)
	fmt.Fprintf(&b, "| **Model Tier** | %s |\n", model)
	if task.PriorityLabel.Valid && task.PriorityLabel.String != "" {
		fmt.Fprintf(&b, "| **Priority** | %d / %s |\n", task.Priority, task.PriorityLabel.String)
	} else {
		fmt.Fprintf(&b, "| **Priority** | %d |\n", task.Priority)
	}
	fmt.Fprintf(&b, "| **Depends On** | %s |\n", dependsDisplay)
	fmt.Fprintf(&b, "| **Blocked By** | %s |\n", blockedDisplay)
	fmt.Fprintf(&b, "| **Worktree** | %s |\n", worktreeDisplay)
	fmt.Fprintf(&b, "| **Branch** | %s |\n", branchDisplay)

	fmt.Fprintf(&b, "\n## Description\n\n%s\n\n", desc)

	if refinementSection != "" {
		b.WriteString(refinementSection)
	} else {
		b.WriteString(retrySection)
	}

	b.WriteString("## Files Owned\n\n")
	b.WriteString("These are the ONLY files this agent may create or modify:\n\n")
	b.WriteString("| File Path | Action | Description |\n|---|---|---|\n")
	b.WriteString(lockTable)

	b.WriteString("\n## Inline Schema (MANDATORY)\n\n")
	b.WriteString("**Use ONLY the definitions below. Do NOT guess or invent column names, field names, or API shapes.**\n\n")
	if schemaWarning != "" {
		b.WriteString(schemaWarning + "\n\n")
	}
	fmt.Fprintf(&b, "```sql\n%s\n```\n\n", schema)
	b.WriteString("**DateTime note:** SQLite `DATETIME DEFAULT CURRENT_TIMESTAMP` stores ISO-8601 strings (e.g., `2026-02-06 12:34:56`). Use `datetime()` and `julianday()` directly — do NOT use the `'unixepoch'` modifier.\n\n")

	fmt.Fprintf(&b, "## Interface Contracts\n\n%s\n\n", contracts)

	b.WriteString(predecessorSection)

	// Optional repo map (opt-in via blackboard flag)
	repoMapFlag, _ := d.GetBlackboardValue(ctx, "conductor:repo_map")
	if repoMapFlag == "1" {
		b.WriteString(buildRepoMapSection(opts.RepoRoot))
	}

	b.WriteString(buildContinuationSection(ctx, d, taskID))
	b.WriteString(qualitySection)

	b.WriteString("## Acceptance Criteria\n\n")
	if task.AcceptanceCriteria.Valid && task.AcceptanceCriteria.String != "" {
		b.WriteString(task.AcceptanceCriteria.String)
		b.WriteString("\n\n")
	}
	b.WriteString("- [ ] Implementation matches the description above\n")
	b.WriteString("- [ ] All tests pass\n")
	b.WriteString("- [ ] No files modified outside of ownership list\n")
	b.WriteString("- [ ] **All changes committed** to the feature branch (see Delivery below)\n\n")

	b.WriteString(selfCheckSection)

	b.WriteString("## Delivery\n\n")
	fmt.Fprintf(&b, "**CRITICAL: You MUST commit your work before finishing.** Your output is validated by checking for commits ahead of %s. Uncommitted files = failed task.\n\n", baseBranch)
	b.WriteString("**IMPORTANT: If the Description section above mentions any git commands, IGNORE THEM. Use ONLY the commands below — they target your isolated worktree, not the main repo.**\n\n")
	b.WriteString("After completing your work, run these commands using the Bash tool:\n\n")
	fmt.Fprintf(&b, "  git -C %s add -A\n", worktreeRoot)
	fmt.Fprintf(&b, "  git -C %s commit -m 'Describe your changes here'\n\n", worktreeRoot)
	b.WriteString("Do NOT skip the commit step. Without it, your work will be lost and the task will fail validation.\n\n")

	b.WriteString("## Context\n\n")
	b.WriteString("- Task is part of the Claude Orchestra project\n")
	b.WriteString("- Read `SPEC.md` for architecture context\n")
	b.WriteString("- Follow existing code patterns in the codebase\n\n")

	b.WriteString("## Working Directory\n\n")
	fmt.Fprintf(&b, "**CRITICAL:** You are operating in an isolated git worktree at: `%s`\n\n", worktreeDisplay)
	b.WriteString("- ALWAYS use absolute paths for ALL file operations (Read, Edit, Write, Glob, Grep)\n")
	fmt.Fprintf(&b, "- Your worktree root is: `%s`\n", worktreeRoot)
	b.WriteString("- Do NOT use relative paths — Claude Code's working directory can reset between tool calls\n")
	fmt.Fprintf(&b, "- For git commands, use: `git -C \"%s\" <command>`\n", worktreeRoot)
	b.WriteString("- Verify your working directory before any file modification\n\n")

	b.WriteString(buildContextManagementSection())

	b.WriteString("## Token Budget\n\n")
	b.WriteString("| Limit | Value |\n|---|---|\n")
	b.WriteString("| **Max tokens** | unlimited |\n")
	b.WriteString("| **Expected complexity** | medium |\n\n")

	b.WriteString("## Notes\n\n")
	b.WriteString("This spec was auto-generated by `GenerateSpec` from the orchestrator database.\n")

	spec := b.String()

	// Log events
	if descTruncated {
		d.LogEvent(ctx, "spec_truncated", "", taskID,
			fmt.Sprintf(`{"original_desc_size":%d,"spec_size":%d}`, origDescSize, len(spec)))
	}
	if len(spec) > 5000 {
		d.LogEvent(ctx, "spec_size_warning", "", taskID,
			fmt.Sprintf(`{"spec_size":%d}`, len(spec)))
	}
	d.LogEvent(ctx, "spec_generated", "", taskID,
		fmt.Sprintf(`{"spec_size":%d}`, len(spec)))

	return spec, nil
}

// resolveRole determines the agent role and model tier for a task.
func resolveRole(ctx context.Context, d *db.DB, repoRoot string, task *db.Task) (string, string) {
	role := task.Role
	model := "sonnet"

	if task.AssignedTo.Valid && task.AssignedTo.String != "" {
		agent, err := d.GetAgentByTask(ctx, task.ID)
		if err == nil && agent != nil {
			role = agent.Role
		}
	}

	if role == "" {
		role = "implementer"
	}

	// Derive model from agent definition file
	if agentDef, err := ParseAgentDef(repoRoot, Role(role)); err == nil && agentDef.Model != "" {
		model = agentDef.Model
	}

	return role, model
}

// resolveBaseBranch returns the base branch from blackboard or defaults to "dev".
func resolveBaseBranch(ctx context.Context, d *db.DB) string {
	if branch, _ := d.GetBlackboardValue(ctx, "base_branch"); branch != "" {
		return branch
	}
	return "dev"
}

// buildFileLockTable builds the markdown table rows for file locks.
func buildFileLockTable(ctx context.Context, d *db.DB, taskID string) string {
	locks, _ := d.ListFileLocksForTask(ctx, taskID)
	if len(locks) == 0 {
		return "| *(no files locked yet)* | | |\n"
	}
	var b strings.Builder
	for _, l := range locks {
		fmt.Fprintf(&b, "| `%s` | modify | Locked for this task |\n", l.FilePath)
	}
	return b.String()
}

// buildRetrySection builds the retry context section if retry count > 0.
func buildRetrySection(ctx context.Context, d *db.DB, taskID string) string {
	retryStr, _ := d.GetBlackboardValue(ctx, "retry:"+taskID)
	if retryStr == "" || retryStr == "0" {
		return ""
	}

	lastFailure, _ := d.GetBlackboardValue(ctx, "last_failure:"+taskID)
	if lastFailure == "" {
		lastFailure = "Unknown"
	}

	var b strings.Builder
	fmt.Fprintf(&b, "## Previous Attempt Failed (Retry %s/3)\n\n", retryStr)
	b.WriteString("**IMPORTANT:** A previous agent attempted this task and failed post-completion validation.\n\n")
	fmt.Fprintf(&b, "**Failure reason:** %s\n\n", lastFailure)
	b.WriteString("**What this means:**\n")

	switch {
	case strings.Contains(lastFailure, "implementer_partial_work"):
		b.WriteString("- The previous agent only modified SOME of the files it was assigned\n")
		b.WriteString("- You MUST modify ALL files listed in Files Owned — partial work is not accepted\n")
		b.WriteString("- Run `git diff --name-only` and compare against the Files Owned table\n")
	case strings.Contains(lastFailure, "implementer_no_changes") || strings.Contains(lastFailure, "implementer_writes_gitignored"):
		b.WriteString("- The previous agent finished without producing any committed file changes\n")
		b.WriteString("- You MUST create or modify files and commit them before finishing\n")
		b.WriteString("- Verify your changes are on tracked (non-gitignored) paths\n")
	case strings.Contains(lastFailure, "_no_commits"):
		b.WriteString("- The previous agent finished without committing any work\n")
		b.WriteString("- You MUST commit your deliverables before finishing\n")
		b.WriteString("- Use: git -C <worktree> add -A && git -C <worktree> commit -m 'description'\n")
	case strings.Contains(lastFailure, "scout_empty_result"):
		b.WriteString("- The previous agent produced no substantial output\n")
		b.WriteString("- Ensure your response includes detailed findings (>100 chars)\n")
	case strings.Contains(lastFailure, "reviewer_no_output"):
		b.WriteString("- The previous reviewer produced no review output\n")
		b.WriteString("- Ensure you commit review findings or produce a substantial result\n")
	default:
		b.WriteString("- Review the failure reason above and address it in this attempt\n")
	}

	b.WriteString("\n**Do not repeat the same mistake.** Focus on producing committed, validated output.\n\n")
	return b.String()
}

// MaxRefinements is the maximum number of refinement rounds before falling through
// to the circuit breaker / permanent failure path.
const MaxRefinements = 2

// refinableFailures is the set of validation failure reasons that qualify for
// the refinement loop (not crash/infra failures).
// Only quality/output failures qualify — not infrastructure failures (no_worktree)
// or role-specific paths (reviewer/scout) that need different handling.
var refinableFailures = map[string]bool{
	"implementer_no_changes":            true,
	"implementer_writes_gitignored":     true,
	"implementer_partial_work":          true,
	"researcher_no_commits":             true,
	"researcher_no_markdown":            true,
	"researcher_markdown_too_short":     true,
	"researcher_insufficient_headings":  true,
	"researcher_insufficient_urls":      true,
	"architect_no_commits":              true,
	"architect_no_markdown":             true,
	"architect_markdown_too_short":      true,
	"architect_insufficient_headings":   true,
	"architect_insufficient_urls":       true,
	"test_cmd_failed":                   true,
	"review_rejected":                   true,
}

// IsRefinableFailure returns true if the failure reason qualifies for the
// refinement loop (validation failures, not crash/infra).
func IsRefinableFailure(reason string) bool {
	return refinableFailures[reason]
}

// buildRefinementSection builds a detailed critique section when a refinement
// round has been completed. This replaces the generic retry section with
// structured reviewer feedback.
func buildRefinementSection(ctx context.Context, d *db.DB, taskID string) string {
	refinementStr, _ := d.GetBlackboardValue(ctx, "refinement:"+taskID)
	if refinementStr == "" || refinementStr == "0" {
		return ""
	}

	critique, _ := d.GetBlackboardValue(ctx, "critique:"+taskID)
	if critique == "" {
		return ""
	}

	lastFailure, _ := d.GetBlackboardValue(ctx, "last_failure:"+taskID)
	if lastFailure == "" {
		lastFailure = "Unknown"
	}

	var b strings.Builder
	fmt.Fprintf(&b, "## Previous Attempt — Reviewer Critique (Refinement %s/%d)\n\n", refinementStr, MaxRefinements)
	b.WriteString("**IMPORTANT:** A previous agent attempted this task and failed post-completion validation. ")
	b.WriteString("A reviewer agent has analyzed the failure and produced the following critique.\n\n")
	fmt.Fprintf(&b, "**Failure reason:** `%s`\n\n", lastFailure)
	b.WriteString("### Reviewer Critique\n\n")
	b.WriteString(critique)
	b.WriteString("\n\n")

	// Include merge feedback (test output or review critique) if present
	mergeFeedback, _ := d.GetBlackboardValue(ctx, "merge_feedback:"+taskID)
	if mergeFeedback != "" {
		if len(mergeFeedback) > 5000 {
			mergeFeedback = mergeFeedback[:5000] + "\n[TRUNCATED]"
		}
		if strings.Contains(lastFailure, "test_cmd_failed") {
			fmt.Fprintf(&b, "### Test Output\n\n```\n%s\n```\n\n", mergeFeedback)
		} else if strings.Contains(lastFailure, "review_rejected") {
			fmt.Fprintf(&b, "### Review Feedback\n\n%s\n\n", mergeFeedback)
		}
	}

	// V3 gate: include gate error output and triage context for refinement
	gateTriage, _ := d.GetBlackboardValue(ctx, "gate_triage:"+taskID)
	gateError, _ := d.GetBlackboardValue(ctx, "gate_error:"+taskID)
	if gateTriage != "" && gateError != "" {
		if len(gateError) > 5000 {
			gateError = gateError[:5000] + "\n[TRUNCATED]"
		}
		fmt.Fprintf(&b, "### Task Gate Failure (triage: %s)\n\n", gateTriage)
		switch gateTriage {
		case "heal":
			b.WriteString("The build/test gate detected a fixable issue. Apply the fix hints below:\n\n")
			fmt.Fprintf(&b, "```\n%s\n```\n\n", gateError)
		case "refine":
			b.WriteString("The build/test gate failed. Review the full error output and fix accordingly:\n\n")
			fmt.Fprintf(&b, "```\n%s\n```\n\n", gateError)
		case "redo":
			b.WriteString("The build/test gate detected a fundamental issue requiring a fresh approach.\n")
			b.WriteString("The worktree has been reset to the fork point. Start from scratch.\n\n")
			fmt.Fprintf(&b, "Error context:\n```\n%s\n```\n\n", gateError)
		default:
			fmt.Fprintf(&b, "```\n%s\n```\n\n", gateError)
		}
	}

	b.WriteString("### Remediation Guidance\n\n")

	// Include skip reports if available (B-207: explains WHY files were skipped)
	skipReports, _ := d.GetBlackboardValue(ctx, "skip_reports:"+taskID)
	if skipReports != "" {
		fmt.Fprintf(&b, "### Previous Agent Skip Reports\n\n%s\n\n", skipReports)
		b.WriteString("The previous agent skipped these files for the reasons above. You MUST still modify them — find a concrete change to make.\n\n")
	}

	switch {
	case strings.Contains(lastFailure, "partial_work"):
		b.WriteString("- You did NOT modify all files in the Files Owned table\n")
		b.WriteString("- Run `git diff --name-only` and compare against the Files Owned table above\n")
		b.WriteString("- Every file listed in Files Owned MUST be modified — partial work is not accepted\n")
		b.WriteString("- Review the task description for what changes each file needs\n")
	case strings.Contains(lastFailure, "no_changes") || strings.Contains(lastFailure, "writes_gitignored"):
		b.WriteString("- You MUST create or modify tracked files and commit them before finishing\n")
		b.WriteString("- Verify changes are on non-gitignored paths\n")
		b.WriteString("- Use `git status` to confirm your changes are staged\n")
	case strings.Contains(lastFailure, "no_commits"):
		b.WriteString("- You MUST commit all deliverables using: git -C <worktree> add -A && git -C <worktree> commit -m 'description'\n")
		b.WriteString("- Do NOT finish without committing — the validation checks for commits ahead of the base branch\n")
	case strings.Contains(lastFailure, "no_markdown"):
		b.WriteString("- You MUST produce at least one .md file as a deliverable\n")
		b.WriteString("- The file must be committed to the worktree branch\n")
	case strings.Contains(lastFailure, "markdown_too_short"):
		b.WriteString("- Your markdown output was too short (< 500 chars)\n")
		b.WriteString("- Produce comprehensive, detailed output with thorough analysis\n")
	case strings.Contains(lastFailure, "insufficient_headings"):
		b.WriteString("- Your markdown needs at least 2 section headings (## or ###)\n")
		b.WriteString("- Structure your output with clear sections\n")
	case strings.Contains(lastFailure, "insufficient_urls"):
		b.WriteString("- Your research must include at least 3 URLs as source references\n")
		b.WriteString("- Cite sources with full http:// or https:// URLs\n")
	case strings.Contains(lastFailure, "no_output") || strings.Contains(lastFailure, "result_too_short"):
		b.WriteString("- You must produce substantial output (>100 chars in the result)\n")
		b.WriteString("- Include detailed findings in your response\n")
	case strings.Contains(lastFailure, "gate_failed"):
		b.WriteString("- The V3 task gate FAILED build/test validation on your worktree\n")
		b.WriteString("- Review the Task Gate Failure section above for the error output\n")
		b.WriteString("- Fix the issues causing the gate failure, then commit\n")
		b.WriteString("- Run the test command yourself before completing to verify\n")
	case strings.Contains(lastFailure, "test_cmd_failed"):
		b.WriteString("- The test command FAILED after your branch was merged into the base\n")
		b.WriteString("- Review the Test Output section above for specific failures\n")
		b.WriteString("- Fix the code causing test failures, then commit\n")
		b.WriteString("- Run the test command yourself before completing to verify\n")
	case strings.Contains(lastFailure, "review_rejected"):
		b.WriteString("- Your code was REJECTED during review\n")
		b.WriteString("- Address ALL reviewer concerns in the Review Feedback above\n")
		b.WriteString("- Focus on correctness, security, and code quality\n")
	default:
		b.WriteString("- Address the specific failure reason above\n")
		b.WriteString("- Follow the reviewer's critique carefully\n")
	}

	b.WriteString("\n**Do not repeat the same mistake.** The reviewer critique above identifies exactly what went wrong. Fix it.\n\n")
	return b.String()
}

// buildSchemaSection returns the inline schema and optional warning.
func buildSchemaSection(ctx context.Context, d *db.DB, taskID string) (string, string) {
	schema, _ := d.GetBlackboardValue(ctx, "schema:"+taskID)
	if schema == "" {
		warning := fmt.Sprintf("**WARNING: INSUFFICIENT_SCHEMA — No inline schema found for this task in the blackboard (key: schema:%s). The orchestrator should populate this before spawning.**", taskID)
		return "-- No schema provided. See WARNING above.", warning
	}
	return schema, ""
}

// buildContractSection returns interface contracts or the self-contained fallback.
func buildContractSection(ctx context.Context, d *db.DB, taskID string) string {
	contracts, _ := d.GetBlackboardValue(ctx, "contract:"+taskID)
	if contracts == "" {
		return "None — this task is self-contained."
	}
	return contracts
}

// buildPredecessorSection renders predecessor results for blocked_by dependencies.
func buildPredecessorSection(ctx context.Context, d *db.DB, task *db.Task) string {
	if !task.BlockedBy.Valid || task.BlockedBy.String == "" {
		return ""
	}

	raw := task.BlockedBy.String
	if raw == "null" || raw == "[]" {
		return ""
	}

	var predIDs []string
	if err := json.Unmarshal([]byte(raw), &predIDs); err != nil || len(predIDs) == 0 {
		return ""
	}

	var b strings.Builder
	b.WriteString("## Predecessor Results\n\n")
	b.WriteString("The following tasks completed before this one:\n\n")

	for _, predID := range predIDs {
		predTask, _ := d.GetTaskByID(ctx, predID)
		predTitle := predID
		if predTask != nil {
			predTitle = predTask.Title
		}

		predSummary, _ := d.GetBlackboardValue(ctx, "result_summary:"+predID)
		if predSummary != "" {
			if len(predSummary) > 1400 {
				predSummary = predSummary[:1400] + "... [truncated]"
			}
			fmt.Fprintf(&b, "### %s: %s\n\n%s\n\n", predID, predTitle, predSummary)
		} else {
			fmt.Fprintf(&b, "### %s: %s\n\n*(No result summary available — check git log for commits on the predecessor's branch)*\n\n", predID, predTitle)
		}
	}

	section := b.String()
	if len(section) > 1500 {
		section = section[:1400] + "\n\n[TRUNCATED — predecessor results exceeded 1500 chars]\n"
	}
	return section
}

// buildQualitySection returns the quality standards table for researcher/architect roles.
func buildQualitySection(role string) string {
	if role != "researcher" && role != "architect" {
		return ""
	}
	var b strings.Builder
	b.WriteString("## Quality Standards\n\n")
	b.WriteString("Your output will be evaluated on 6 dimensions (score 1-5 each, minimum 3 required):\n\n")
	b.WriteString("| Dimension | What's Evaluated |\n|---|---|\n")
	b.WriteString("| Methodology | Systematic approach, reproducible methods |\n")
	b.WriteString("| Sources | 5+ credible sources with URLs, proper attribution |\n")
	b.WriteString("| Novelty & Insight | Synthesis beyond summary, original frameworks |\n")
	b.WriteString("| Actionability | Specific recommendations with evidence |\n")
	b.WriteString("| Clarity & Structure | Logical flow, section headings, tables |\n")
	b.WriteString("| Completeness | All questions answered, limitations noted |\n\n")
	b.WriteString("**Minimum threshold:** Score 3+ on every dimension. See `research/techniques/029-research-output-standards.md` for full rubric.\n\n")
	return b.String()
}

// buildSelfCheckSection returns a role-specific self-verification checklist.
func buildSelfCheckSection(role string) string {
	switch role {
	case "implementer":
		return "## Self-Check (before marking complete)\n\n" +
			"**IMPORTANT: When this spec says to modify a file, IMPLEMENT the change — do not merely verify or confirm it works.** If you read a file and cannot identify a concrete modification, explain why in a commit message and still attempt a minimal change (e.g., add a comment noting the file was reviewed for this feature).\n\n" +
			"- [ ] All acceptance criteria from the Description section are met\n" +
			"- [ ] **Every file in Files Owned has been modified** — run `git diff --name-only` and compare against the Files Owned table above. If any owned file is unmodified, you missed part of the task\n" +
			"- [ ] Tests pass (`go test ./...` or relevant test command)\n" +
			"- [ ] Only files listed in Files Owned were modified\n" +
			"- [ ] No unrelated changes or refactoring included\n" +
			"- [ ] Code compiles without errors or warnings\n\n"
	case "researcher":
		return "## Self-Check (before marking complete)\n\n" +
			"- [ ] At least 5 distinct sources cited with URLs or references\n" +
			"- [ ] All questions from the Description section are answered\n" +
			"- [ ] Limitations and gaps in findings are explicitly noted\n" +
			"- [ ] Findings are organized with clear headings and structure\n" +
			"- [ ] Raw data/quotes are included to support conclusions\n\n"
	case "architect":
		return "## Self-Check (before marking complete)\n\n" +
			"- [ ] No task overlaps — each task has a unique, non-overlapping scope\n" +
			"- [ ] Dependencies are acyclic (no circular depends_on chains)\n" +
			"- [ ] Interface contracts are defined for all cross-task boundaries\n" +
			"- [ ] File ownership is assigned with no conflicts between tasks\n" +
			"- [ ] Each task is independently testable\n\n"
	case "illustrator":
		return "## Self-Check (before marking complete)\n\n" +
			"- [ ] All figure descriptions from the chapter have corresponding Python scripts\n" +
			"- [ ] Each script produces both PNG (600 DPI) and SVG output\n" +
			"- [ ] Figure file naming follows figures/chNN-figNN-description pattern\n" +
			"- [ ] Scripts are self-contained (no external data dependencies)\n" +
			"- [ ] All generated images committed to the worktree\n\n"
	default:
		return ""
	}
}

// refDocPattern matches "FIRST: Read <path>" or "FIRST: Read `<path>`" directives in task descriptions.
var refDocPattern = regexp.MustCompile(`(?i)(?:FIRST:\s*Read\s+)` + "`?" + `([^\s` + "`" + `]+)` + "`?")

// injectReferenceDocuments scans the description for "FIRST: Read <path>" patterns,
// reads those files from the repo root, and appends their content (truncated to 3000
// chars each) under an "## Injected Reference Documents" section. This ensures agents
// get reference content directly instead of hoping they'll read the files themselves.
func injectReferenceDocuments(desc, repoRoot string) string {
	if repoRoot == "" {
		return desc
	}
	matches := refDocPattern.FindAllStringSubmatch(desc, -1)
	if len(matches) == 0 {
		return desc
	}

	seen := make(map[string]bool)
	var injected []string
	for _, m := range matches {
		relPath := m[1]
		if seen[relPath] {
			continue
		}
		seen[relPath] = true

		absPath := relPath
		if !filepath.IsAbs(relPath) {
			absPath = filepath.Join(repoRoot, relPath)
		}
		data, err := os.ReadFile(absPath)
		if err != nil {
			continue
		}
		content := string(data)
		if len(content) > 3000 {
			content = content[:3000] + "\n[TRUNCATED — original was " + fmt.Sprintf("%d", len(data)) + " chars]"
		}
		injected = append(injected, fmt.Sprintf("### %s\n\n```\n%s\n```", relPath, content))
	}

	if len(injected) == 0 {
		return desc
	}
	return desc + "\n\n## Injected Reference Documents\n\n" + strings.Join(injected, "\n\n")
}

// truncateDescription truncates a long description and adds a truncation notice.
func truncateDescription(desc string, origSize int) string {
	return desc[:2000] + fmt.Sprintf("\n\n[TRUNCATED — original description was %d chars. See task DB for full text.]", origSize)
}

// buildContinuationSection generates a structured continuation context for respawned agents.
// It reads checkpoint data from the blackboard (stored by monitor on context_exhausted).
func buildContinuationSection(ctx context.Context, d *db.DB, taskID string) string {
	checkpoint, _ := d.GetBlackboardValue(ctx, "checkpoint:"+taskID)
	if checkpoint == "" {
		return ""
	}

	var cp Checkpoint
	if err := json.Unmarshal([]byte(checkpoint), &cp); err != nil {
		// Fall back to raw text
		return "## Continuation Context\n\n" + checkpoint + "\n\n"
	}

	var b strings.Builder
	b.WriteString("## Continuation Context\n\n")
	b.WriteString("**A previous agent worked on this task but hit context limits. Resume from where they left off.**\n\n")
	b.WriteString("| Field | Value |\n|---|---|\n")
	fmt.Fprintf(&b, "| Commits made | %d |\n", len(cp.CommitHashes))
	fmt.Fprintf(&b, "| Files modified | %s |\n", strings.Join(cp.FilesModified, ", "))
	fmt.Fprintf(&b, "| Uncommitted files | %d |\n", cp.UncommittedFiles)
	fmt.Fprintf(&b, "| Estimated progress | %s |\n", cp.EstimatedProgress)

	if cp.LogTailSummary != "" {
		fmt.Fprintf(&b, "\n### Last Actions\n\n%s\n", cp.LogTailSummary)
	}

	b.WriteString("\n**Do NOT repeat completed work. Check git log for what's already done. Focus on remaining items.**\n\n")
	return b.String()
}

// buildRepoMapSection generates a compact codebase map section for the spec.
func buildRepoMapSection(repoRoot string) string {
	repoMap, err := GenerateRepoMap(repoRoot, 2000) // ~500 tokens
	if err != nil || repoMap == "" {
		return ""
	}
	return "## Codebase Map\n\n```\n" + repoMap + "```\n\n"
}

// buildFrontmatter builds YAML frontmatter with machine-parseable task metadata.
func buildFrontmatter(taskID, title, role, model, sessionID, dependsOn string, files []string) string {
	var b strings.Builder
	b.WriteString("---\n")
	fmt.Fprintf(&b, "task_id: %s\n", taskID)
	fmt.Fprintf(&b, "title: %s\n", title)
	fmt.Fprintf(&b, "role: %s\n", role)
	fmt.Fprintf(&b, "model: %s\n", model)
	if sessionID != "" {
		fmt.Fprintf(&b, "session_id: %s\n", sessionID)
	}
	if dependsOn != "" && dependsOn != "null" && dependsOn != "[]" {
		fmt.Fprintf(&b, "depends_on: %s\n", dependsOn)
	}
	if len(files) > 0 {
		fmt.Fprintf(&b, "files: [%s]\n", strings.Join(files, ", "))
	}
	fmt.Fprintf(&b, "generated_at: %s\n", time.Now().UTC().Format(time.RFC3339))
	b.WriteString("---\n")
	return b.String()
}

// buildContextManagementSection returns instructions for agents to manage their own context.
func buildContextManagementSection() string {
	return `## Context Management

For long-running tasks, manage your context actively:
- After completing each major subtask, commit your work immediately
- If modifying 3+ files, commit after each file is complete rather than all at once
- Focus on outcomes, not process — do not retain exploratory dead-end details
- If you notice your context growing large, commit your work and summarize progress before continuing
- After each major milestone, write a checkpoint file:
  echo '{"completed":["list of done items"],"remaining":["list of todo items"],"notes":"any context"}' > .checkpoint.json

`
}
