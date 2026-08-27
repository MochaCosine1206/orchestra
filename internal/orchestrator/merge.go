package orchestrator

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/MochaCosine1206/orchestra/internal/agent"
	"github.com/MochaCosine1206/orchestra/internal/db"
)

// MergeMode controls how staging branch changes are integrated into the base branch.
type MergeMode string

const (
	// MergeModeLocal uses update-ref + reset --hard (default, fast, local-only).
	MergeModeLocal MergeMode = "local"
	// MergeModePR creates a GitHub PR from the staging branch.
	MergeModePR MergeMode = "pr"
)

// PRResult captures the outcome of creating a GitHub PR from the staging branch.
type PRResult struct {
	PRURL    string // e.g. "https://github.com/org/repo/pull/123"
	PRNumber int
	Branch   string // staging branch name
	Base     string // target base branch
}

// TestFailureMode controls behavior when test commands fail after merge.
type TestFailureMode string

const (
	// TestFailureModeRevertAndRefine reverts the merge and triggers refinement (default).
	TestFailureModeRevertAndRefine TestFailureMode = "revert_and_refine"
	// TestFailureModeWarnOnly logs the failure but keeps the merge.
	TestFailureModeWarnOnly TestFailureMode = "warn_only"
	// TestFailureModeRevertNoRefine reverts the merge without spawning refinement.
	TestFailureModeRevertNoRefine TestFailureMode = "revert_no_refine"
)

// MergeOpts configures a merge invocation.
type MergeOpts struct {
	TestCmd         string
	TestFailureMode TestFailureMode // default: revert_and_refine
	TestCmdTimeout  time.Duration   // default: 5m
	Review          bool
	DryRun          bool
	BaseBranch      string    // explicit base branch override (empty = auto-detect)
	SkipBranches    []string  // branches to skip test gate (already verified in prior merge attempt)
	MergeMode       MergeMode // "local" (default) or "pr" (create GitHub PR)
	PhaseID         string    // phase filter: only merge this phase's tasks (G110)
}

// MergeResult captures the outcome of merging branches.
type MergeResult struct {
	Merged       []string // branch names merged successfully
	Failed       []string // branch names that failed to merge
	Skipped      []string // branches skipped (e.g., review rejected)
	Refining     []string // branches sent back for refinement
	Plan         []string // merge order (for dry-run)
	AutoResolved []string // branches where conflicts were auto-resolved
	TestsFailed  bool
}

// ConflictHunk represents a single conflict region in a file.
type ConflictHunk struct {
	StartLine int
	EndLine   int
	Ours      string
	Theirs    string
}

// ConflictResolution captures the outcome of an auto-resolution attempt.
type ConflictResolution struct {
	Attempted   bool
	Resolved    bool
	Tier        string // "import", "non_overlapping", "complex"
	FilesFixed  []string
	FilesFailed []string
}

// Merge merges completed task branches back into the base branch in topological order.
func (c *Conductor) Merge(ctx context.Context, opts MergeOpts) (*MergeResult, error) {
	result := &MergeResult{}

	// G110: Get task IDs — phase-scoped when PhaseID set, otherwise all session tasks.
	var taskIDs []string
	if opts.PhaseID != "" {
		conductorID := c.ConductorID
		if conductorID == "" {
			conductorID = c.SessionID
		}
		phaseIDs, err := c.DB.ListTaskIDsByPhase(ctx, conductorID, opts.PhaseID)
		if err == nil && len(phaseIDs) > 0 {
			taskIDs = phaseIDs
			c.log("Merge: filtering to phase %s (%d tasks)", opts.PhaseID, len(taskIDs))
		} else {
			taskIDs = c.sessionTaskIDs(ctx)
			c.log("Merge: phase %s filter returned 0 tasks, falling back to session tasks (%d)", opts.PhaseID, len(taskIDs))
		}
	} else {
		taskIDs = c.sessionTaskIDs(ctx)
	}
	sessionSet := make(map[string]bool)
	for _, id := range taskIDs {
		sessionSet[id] = true
	}

	// Get all tasks
	allTasks, err := c.DB.ListTasks(ctx)
	if err != nil {
		return nil, fmt.Errorf("listing tasks: %w", err)
	}

	// Filter to completed tasks in this session with branches
	type mergeEntry struct {
		task   db.Task
		branch string
	}
	var mergeableTasks []db.Task
	branchByID := make(map[string]string)

	for _, t := range allTasks {
		if len(sessionSet) > 0 && !sessionSet[t.ID] {
			continue
		}
		if t.Status != "done" {
			continue
		}
		if !t.Branch.Valid || t.Branch.String == "" {
			continue
		}
		mergeableTasks = append(mergeableTasks, t)
		branchByID[t.ID] = t.Branch.String
	}

	if len(mergeableTasks) == 0 {
		c.log("No branches to merge")
		return result, nil
	}

	// Topologically sort for merge order
	sorted, err := TopoSort(mergeableTasks)
	if err != nil {
		c.log("Toposort failed, using creation order: %v", err)
		sorted = nil
		for _, t := range mergeableTasks {
			sorted = append(sorted, t.ID)
		}
	}

	// Build ordered list
	type orderedEntry struct {
		taskID string
		branch string
	}
	var ordered []orderedEntry
	for _, id := range sorted {
		if b, ok := branchByID[id]; ok {
			ordered = append(ordered, orderedEntry{id, b})
		}
	}

	// Build plan
	for _, entry := range ordered {
		result.Plan = append(result.Plan, entry.branch)
	}

	if opts.DryRun {
		c.log("Merge plan (dry-run):")
		for i, branch := range result.Plan {
			c.log("  %d. %s", i+1, branch)
		}
		return result, nil
	}

	// Get merge target: staging branch (if conductor exists) > base branch.
	// Task branches merge to staging, then staging merges to dev in Go().
	baseBranch := opts.BaseBranch
	if baseBranch == "" {
		baseBranch = c.getBlackboard(ctx, "base_branch")
	}
	if baseBranch == "" {
		baseBranch = agent.DetectBaseBranch(c.RepoRoot)
	}

	// Use staging branch as merge target if available
	mergeBranch := baseBranch
	conductorID := c.ConductorID
	if conductorID == "" {
		conductorID = c.SessionID
	}
	if conductorID != "" {
		rec, err := c.DB.GetConductor(ctx, conductorID)
		if err == nil && rec != nil && rec.StagingBranch != "" {
			mergeBranch = rec.StagingBranch
		}
	}
	c.log("Merging into branch: %s", mergeBranch)

	// Stash any uncommitted changes to prevent merge failures.
	// Agents or Claude Code may modify files like .claude/.gitignore
	// in the main repo during execution.
	stashed := false
	if status, _ := gitExec(c.RepoRoot, "status", "--porcelain"); status != "" {
		if _, err := gitExec(c.RepoRoot, "stash", "push", "-m", "orchestra-merge-autostash"); err == nil {
			stashed = true
		}
	}
	defer func() {
		if stashed {
			gitExec(c.RepoRoot, "stash", "pop")
		}
	}()

	// Ensure we're on the merge target branch (staging or base).
	// G106: Save original branch so we can restore it after merging.
	originalBranch, _ := gitExec(c.RepoRoot, "rev-parse", "--abbrev-ref", "HEAD")
	originalBranch = strings.TrimSpace(originalBranch)
	if originalBranch != mergeBranch {
		if _, err := gitExec(c.RepoRoot, "checkout", mergeBranch); err != nil {
			return nil, fmt.Errorf("checking out merge branch %s: %w", mergeBranch, err)
		}
	}
	// G106: Restore original branch when merge is done. This prevents leaving the
	// user's working directory on the staging branch after `orchestra go --foreground`.
	defer func() {
		if originalBranch != "" && originalBranch != "HEAD" && originalBranch != mergeBranch {
			if _, err := gitExec(c.RepoRoot, "checkout", originalBranch); err != nil {
				c.log("WARNING: Could not restore original branch %s: %v", originalBranch, err)
			}
		}
	}()

	// Probe test environment once before merge loop (B-240a)
	testEnvOK := true
	if opts.TestCmd != "" && opts.TestCmd != "true" {
		probe := probeTestEnvironment(c.RepoRoot, opts.TestCmd, 30*time.Second)
		if !probe.CanRun {
			c.log("Test probe failed (%s): %s — skipping test gate", probe.Framework, probe.Reason)
			c.DB.LogEvent(ctx, "test_probe_skip", "", "",
				fmt.Sprintf(`{"framework":%s,"reason":%s}`, mustJSON(probe.Framework), mustJSON(probe.Reason)))
			testEnvOK = false
		} else {
			c.log("Test probe passed (%s)", probe.Framework)
		}
	}

	// B-145: Set merge status to "merging" before the branch loop.
	if conductorID != "" {
		c.DB.SetMergeStatus(ctx, conductorID, "merging")
	}

	// Build skip set for recovery runs (B-145)
	skipBranchSet := make(map[string]bool)
	for _, b := range opts.SkipBranches {
		skipBranchSet[b] = true
	}

	// Merge each branch in order
	for _, entry := range ordered {
		branch := entry.branch

		// B-145: Skip already-merged branches on recovery
		if skipBranchSet[branch] {
			c.log("Skipping already-merged branch %s (recovery)", branch)
			result.Merged = append(result.Merged, branch)
			continue
		}

		// G113: Check if branch ref exists before attempting merge.
		// Branch may have been deleted by monitor auto-merge or prior cleanup.
		if _, err := gitExec(c.RepoRoot, "rev-parse", "--verify", branch); err != nil {
			c.log("Skipping missing branch %s (already merged or deleted)", branch)
			result.Merged = append(result.Merged, branch)
			continue
		}

		// G110: Skip branches already merged into the target (ancestry defense).
		// Prevents re-merging Phase 1 branches when Phase 2 calls Merge().
		if _, err := gitExec(c.RepoRoot, "merge-base", "--is-ancestor", branch, mergeBranch); err == nil {
			c.log("Skipping already-merged branch %s (ancestor of %s)", branch, mergeBranch)
			result.Merged = append(result.Merged, branch)
			continue
		}

		// Optional review gate
		if opts.Review {
			reviewMode := ReviewModeDefault
			if modeStr := c.getBlackboard(ctx, "conductor:review_mode"); modeStr != "" {
				switch modeStr {
				case "spec-diff":
					reviewMode = ReviewModeSpecDiff
				case "structured":
					reviewMode = ReviewModeStructured
				}
			}
			reviewResult := c.Review(ctx, ReviewOpts{
				TaskID:  entry.taskID,
				Enabled: true,
				Mode:    reviewMode,
			})
			if !reviewResult.Approved {
				c.log("Review rejected %s, attempting refinement", branch)
				if err := c.triggerMergeRefinement(ctx, entry.taskID, "review_rejected", reviewResult.Feedback); err != nil {
					c.log("Review refinement failed for %s: %v, skipping", branch, err)
					result.Skipped = append(result.Skipped, branch)
				} else {
					result.Refining = append(result.Refining, branch)
				}
				continue
			}
		}

		// File ownership validation (defense-in-depth or pessimistic)
		enforcement := c.getBlackboard(ctx, "conductor:file_enforcement")
		if enforcement == "defense" || enforcement == "pessimistic" {
			if violations := c.validateFileOwnership(ctx, entry.taskID, branch); len(violations) > 0 {
				c.log("BLOCKED merge for %s: file ownership violations: %v", branch, violations)
				result.Failed = append(result.Failed, branch)
				c.DB.LogEvent(ctx, "merge_ownership_violation", "", entry.taskID,
					fmt.Sprintf(`{"violations":%q}`, violations))
				continue
			}
		}

		// Attempt merge
		mergeOut, err := gitExec(c.RepoRoot, "merge", "--no-ff", branch,
			"-m", fmt.Sprintf("Merge %s into %s", branch, mergeBranch))
		if err != nil {
			failureType, conflictFiles := classifyMergeFailure(mergeOut)

			// G99: Transient failures (e.g. Docker container holding .git/index lock).
			// Abort, wait for lock release, and retry once before giving up.
			if failureType == "transient" {
				gitExec(c.RepoRoot, "merge", "--abort")
				c.log("Transient merge failure for %s, retrying in 2s: %s", branch, truncate(mergeOut, 200))
				c.DB.LogEvent(ctx, "merge_transient_retry", "", entry.taskID,
					fmt.Sprintf(`{"git_output":%s}`, mustJSON(truncate(mergeOut, 500))))
				time.Sleep(2 * time.Second)
				mergeOut, err = gitExec(c.RepoRoot, "merge", "--no-ff", branch,
					"-m", fmt.Sprintf("Merge %s into %s", branch, mergeBranch))
				if err == nil {
					goto mergeSuccess
				}
				// Retry also failed — reclassify and fall through to normal handling
				failureType, conflictFiles = classifyMergeFailure(mergeOut)
			}

			// Attempt auto-resolution if enabled and this is a conflict (not a git error)
			autoResolveFlag := c.getBlackboard(ctx, "conductor:auto_resolve_conflicts")
			if failureType == "conflict" && autoResolveFlag == "1" {
				resolution := c.attemptAutoResolve(ctx, entry.taskID, conflictFiles, branch, mergeBranch)
				if resolution.Resolved {
					c.log("Auto-resolved conflicts for %s (tier=%s, files=%v)", branch, resolution.Tier, resolution.FilesFixed)
					result.AutoResolved = append(result.AutoResolved, branch)
					payload := fmt.Sprintf(`{"tier":%s,"files_fixed":%s}`,
						mustJSON(resolution.Tier), mustJSON(resolution.FilesFixed))
					c.DB.LogEvent(ctx, "merge_auto_resolved", "", entry.taskID, payload)
					// Fall through to success path (test cmd, merged append, etc.)
					goto mergeSuccess
				}
				// Auto-resolution failed — log it and fall through to normal failure
				c.log("Auto-resolution failed for %s (tier=%s, fixed=%v, failed=%v)",
					branch, resolution.Tier, resolution.FilesFixed, resolution.FilesFailed)
				c.DB.LogEvent(ctx, "merge_auto_resolve_failed", "", entry.taskID,
					fmt.Sprintf(`{"tier":%s,"files_fixed":%s,"files_failed":%s}`,
						mustJSON(resolution.Tier), mustJSON(resolution.FilesFixed), mustJSON(resolution.FilesFailed)))
			}

			c.log("Merge failed for %s: %v: %s", branch, err, mergeOut)
			gitExec(c.RepoRoot, "merge", "--abort")
			result.Failed = append(result.Failed, branch)
			payload := fmt.Sprintf(`{"type":%s,"files":%s,"git_output":%s}`,
				mustJSON(failureType), mustJSON(conflictFiles), mustJSON(truncate(mergeOut, 2000)))
			c.DB.LogEvent(ctx, "merge_"+failureType, "", entry.taskID, payload)
			// Release file locks for failed merge
			if locks, lockErr := c.DB.ListFileLocksForTask(ctx, entry.taskID); lockErr == nil {
				for _, lock := range locks {
					c.DB.ReleaseFileLock(ctx, lock.FilePath)
				}
			}
			continue
		}
	mergeSuccess:

		// Post-merge content clobber detection (warning only)
		c.checkContentClobber(ctx, entry.taskID)

		// Run test command if provided and environment supports it.
		// Skip test gate for branches already verified in a prior merge attempt (B-145 recovery).
		if opts.TestCmd != "" && testEnvOK && !skipBranchSet[branch] {
			testOutput, testErr := runTestCmd(c.RepoRoot, opts.TestCmd, opts.TestCmdTimeout)
			if testErr != nil {
				mode := opts.TestFailureMode
				if mode == "" {
					mode = TestFailureModeRevertAndRefine
				}
				switch mode {
				case TestFailureModeWarnOnly:
					c.log("Test failed after merging %s (warn_only mode, keeping merge): %v", branch, testErr)
					result.TestsFailed = true
					c.DB.LogEvent(ctx, "merge_test_failed_warn", "", entry.taskID, testErr.Error())
				case TestFailureModeRevertNoRefine:
					c.log("Test failed after merging %s, reverting (no refinement)", branch)
					gitExec(c.RepoRoot, "reset", "--hard", "HEAD~1")
					result.TestsFailed = true
					result.Failed = append(result.Failed, branch)
					c.DB.LogEvent(ctx, "merge_test_failed", "", entry.taskID, testErr.Error())
					continue
				default: // revert_and_refine
					c.log("Test failed after merging %s, reverting", branch)
					gitExec(c.RepoRoot, "reset", "--hard", "HEAD~1")
					result.TestsFailed = true
					if err := c.triggerMergeRefinement(ctx, entry.taskID, "test_cmd_failed", testOutput); err != nil {
						c.log("Refinement failed for %s: %v", branch, err)
						result.Failed = append(result.Failed, branch)
					} else {
						result.Refining = append(result.Refining, branch)
					}
					c.DB.LogEvent(ctx, "merge_test_failed", "", entry.taskID, testErr.Error())
					continue
				}
			}
		}

		result.Merged = append(result.Merged, branch)
		// B-145: Record this branch as merged for crash recovery.
		if conductorID != "" {
			c.DB.AddMergedBranch(ctx, conductorID, branch)
		}
		c.DB.LogEvent(ctx, "branch_merged", "", entry.taskID,
			fmt.Sprintf(`{"branch":"%s"}`, branch))
		c.log("Merged %s", branch)

		// Post-merge file audit: log any files changed outside the task's declared ownership
		c.auditMergedFiles(ctx, entry.taskID)

		// Release file locks for merged task
		if locks, err := c.DB.ListFileLocksForTask(ctx, entry.taskID); err == nil {
			for _, lock := range locks {
				c.DB.ReleaseFileLock(ctx, lock.FilePath)
			}
		}

		// Best-effort worktree and branch cleanup after successful merge
		if task, err := c.DB.GetTaskByID(ctx, entry.taskID); err == nil && task != nil && task.Worktree.Valid {
			wt := task.Worktree.String
			if _, statErr := os.Stat(wt); statErr == nil {
				if _, rmErr := gitExec(c.RepoRoot, "worktree", "remove", "--force", wt); rmErr != nil {
					c.log("WARN: worktree cleanup failed for %s: %v", wt, rmErr)
				}
			}
		}
		if _, err := gitExec(c.RepoRoot, "branch", "-d", branch); err != nil {
			c.log("WARN: branch cleanup failed for %s: %v", branch, err)
		}
	}

	return result, nil
}

// MergeStagingToDev merges the conductor's staging branch into the base branch (dev).
// Called at the end of Go() after all task branches have been merged to staging.
//
// Uses a temporary worktree to perform the merge, avoiding checkout in the main
// working tree. This prevents interference from concurrent conductors or commits
// landing in the main repo during conflict resolution.
//
// Flow: create temp worktree → merge staging there → get SHA → git update-ref → cleanup.
func (c *Conductor) MergeStagingToDev(ctx context.Context) error {
	conductorID := c.ConductorID
	if conductorID == "" {
		conductorID = c.SessionID
	}
	if conductorID == "" {
		return nil
	}

	rec, err := c.DB.GetConductor(ctx, conductorID)
	if err != nil || rec == nil || rec.StagingBranch == "" {
		return nil // no staging branch configured
	}

	stagingBranch := rec.StagingBranch
	baseBranch := rec.BaseBranch
	if baseBranch == "" {
		baseBranch = agent.DetectBaseBranch(c.RepoRoot)
	}

	// Check if staging branch has any commits beyond base
	revCount, err := gitExec(c.RepoRoot, "rev-list", "--count", baseBranch+".."+stagingBranch)
	if err != nil || strings.TrimSpace(revCount) == "0" {
		c.log("Staging branch %s has no new commits — skipping staging-to-dev merge", stagingBranch)
		c.DB.LogEvent(ctx, "staging_merge_skipped", "", "",
			fmt.Sprintf(`{"staging":%s,"base":%s,"reason":"no_new_commits"}`,
				mustJSON(stagingBranch), mustJSON(baseBranch)))
		return nil
	}

	// B-145: Mark transition to staging phase.
	c.DB.SetMergeStatus(ctx, conductorID, "staging")

	c.log("Merging staging branch %s into %s (via temp worktree)", stagingBranch, baseBranch)

	// Create a temporary worktree detached at the base branch for an isolated merge.
	// --detach avoids "already checked out" errors from concurrent operations.
	tmpWorktree := filepath.Join(c.RepoRoot, ".worktree", fmt.Sprintf("_staging-merge-%s", conductorID))
	defer func() {
		gitExec(c.RepoRoot, "worktree", "remove", "--force", tmpWorktree)
	}()

	if _, err := gitExec(c.RepoRoot, "worktree", "add", "--detach", tmpWorktree, baseBranch); err != nil {
		return fmt.Errorf("creating temp worktree for staging merge: %w", err)
	}

	// Merge staging into the worktree (which is at baseBranch HEAD)
	mergeOut, err := gitExec(tmpWorktree, "merge", "--no-ff", stagingBranch,
		"-m", fmt.Sprintf("Merge staging %s into %s", stagingBranch, baseBranch))
	if err != nil {
		failureType, conflictFiles := classifyMergeFailure(mergeOut)

		if failureType == "conflict" {
			// Attempt auto-resolution in the temp worktree
			resolution := c.attemptAutoResolveInDir(ctx, tmpWorktree, "", conflictFiles, stagingBranch, baseBranch)
			if resolution.Resolved {
				c.log("Auto-resolved staging-to-dev conflicts (tier=%s, files=%v)", resolution.Tier, resolution.FilesFixed)
				c.DB.LogEvent(ctx, "staging_merge_auto_resolved", "", "",
					fmt.Sprintf(`{"tier":%s,"files_fixed":%s}`,
						mustJSON(resolution.Tier), mustJSON(resolution.FilesFixed)))
				// Fall through to update-ref below
				goto updateRef
			}
			// Auto-resolution failed — abort and leave staging branch for manual resolution
			c.log("WARN: Staging-to-dev merge has conflicts that could not be auto-resolved. Staging branch %s preserved for manual resolution.", stagingBranch)
			c.DB.LogEvent(ctx, "staging_merge_conflict", "", "",
				fmt.Sprintf(`{"staging":%s,"files":%s}`, mustJSON(stagingBranch), mustJSON(conflictFiles)))
			return fmt.Errorf("staging-to-dev merge conflict in %d files; staging branch %s preserved", len(conflictFiles), stagingBranch)
		}

		// Non-conflict error
		return fmt.Errorf("staging-to-dev merge failed: %s", truncate(mergeOut, 500))
	}

updateRef:
	// Get the merge commit SHA from the temp worktree
	mergeSHA, err := gitExec(tmpWorktree, "rev-parse", "HEAD")
	if err != nil {
		return fmt.Errorf("getting merge SHA: %w", err)
	}

	// Atomically update the base branch ref without touching the main working tree.
	// This is safe even if another conductor is running — it's a single ref update.
	if _, err := gitExec(c.RepoRoot, "update-ref", "refs/heads/"+baseBranch, mergeSHA); err != nil {
		return fmt.Errorf("updating %s ref to %s: %w", baseBranch, mergeSHA, err)
	}

	// B-145: Mark merge complete.
	c.DB.SetMergeStatus(ctx, conductorID, "done")

	c.log("Successfully merged staging %s into %s (sha=%s)", stagingBranch, baseBranch, mergeSHA[:8])
	c.DB.LogEvent(ctx, "staging_merged_to_dev", "", "",
		fmt.Sprintf(`{"staging":%s,"base":%s,"sha":%s}`, mustJSON(stagingBranch), mustJSON(baseBranch), mustJSON(mergeSHA)))

	return nil
}

// CreateStagingPR creates a GitHub PR from the conductor's staging branch to the base branch.
// Used when merge mode is "pr" instead of MergeStagingToDev(). Does NOT delete the staging
// branch (the PR references it; GitHub handles cleanup after merge).
func (c *Conductor) CreateStagingPR(ctx context.Context) (*PRResult, error) {
	conductorID := c.ConductorID
	if conductorID == "" {
		conductorID = c.SessionID
	}
	if conductorID == "" {
		return nil, fmt.Errorf("no conductor ID")
	}

	rec, err := c.DB.GetConductor(ctx, conductorID)
	if err != nil || rec == nil || rec.StagingBranch == "" {
		return nil, nil // no staging branch configured
	}

	stagingBranch := rec.StagingBranch
	baseBranch := rec.BaseBranch
	if baseBranch == "" {
		baseBranch = agent.DetectBaseBranch(c.RepoRoot)
	}

	// Check if staging branch has any commits beyond base
	revCount, err := gitExec(c.RepoRoot, "rev-list", "--count", baseBranch+".."+stagingBranch)
	if err != nil || strings.TrimSpace(revCount) == "0" {
		c.log("Staging branch %s has no new commits — skipping PR creation", stagingBranch)
		return nil, nil
	}

	// Preflight: verify gh CLI is available
	if _, err := exec.LookPath("gh"); err != nil {
		return nil, fmt.Errorf("gh CLI not found in PATH: %w", err)
	}

	// Push the staging branch to the remote so gh can create a PR from it.
	// Best-effort: if the branch already exists on the remote, force-push to update.
	if _, pushErr := gitExec(c.RepoRoot, "push", "-u", "origin", stagingBranch); pushErr != nil {
		return nil, fmt.Errorf("pushing staging branch %s: %w", stagingBranch, pushErr)
	}

	// Build PR title and body
	prTitle := fmt.Sprintf("Orchestra: merge %s", conductorID)
	prBody := fmt.Sprintf("Automated PR from Orchestra session `%s`.\n\nStaging branch `%s` → `%s`.\n\nCreated by `orchestra go --merge-mode pr`.",
		conductorID, stagingBranch, baseBranch)

	// Create PR via gh CLI
	cmd := exec.CommandContext(ctx, "gh", "pr", "create",
		"--base", baseBranch,
		"--head", stagingBranch,
		"--title", prTitle,
		"--body", prBody,
	)
	cmd.Dir = c.RepoRoot
	out, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("gh pr create failed: %w\nOutput: %s", err, truncate(string(out), 500))
	}

	// Parse PR URL from stdout (gh pr create outputs the URL on stdout)
	prURL := strings.TrimSpace(string(out))

	// Extract PR number from URL (last path segment)
	prNumber := 0
	if parts := strings.Split(prURL, "/"); len(parts) > 0 {
		last := parts[len(parts)-1]
		if n, err := fmt.Sscanf(last, "%d", &prNumber); n == 0 || err != nil {
			prNumber = 0
		}
	}

	// B-145: Mark merge complete
	c.DB.SetMergeStatus(ctx, conductorID, "done")

	c.log("Created PR %s from %s → %s", prURL, stagingBranch, baseBranch)
	c.DB.LogEvent(ctx, "staging_pr_created", "", "",
		fmt.Sprintf(`{"pr_url":%s,"staging":%s,"base":%s,"pr_number":%d}`,
			mustJSON(prURL), mustJSON(stagingBranch), mustJSON(baseBranch), prNumber))

	return &PRResult{
		PRURL:    prURL,
		PRNumber: prNumber,
		Branch:   stagingBranch,
		Base:     baseBranch,
	}, nil
}

// validateFileOwnership checks whether a branch modified files owned by other tasks.
// Returns a list of violation descriptions, or nil if clean.
func (c *Conductor) validateFileOwnership(ctx context.Context, taskID, branch string) []string {
	diff, err := gitExec(c.RepoRoot, "diff", "--name-only", "HEAD..."+branch)
	if err != nil {
		return nil // fail-open
	}
	changedFiles := strings.Split(strings.TrimSpace(diff), "\n")

	locks, err := c.DB.ListFileLocksForTask(ctx, taskID)
	if err != nil {
		return nil
	}
	ownedFiles := make(map[string]bool)
	for _, lock := range locks {
		ownedFiles[lock.FilePath] = true
	}

	allLocks, _ := c.DB.ListFileLocks(ctx)
	lockMap := make(map[string]string) // file -> taskID
	for _, l := range allLocks {
		if l.TaskID.Valid {
			lockMap[l.FilePath] = l.TaskID.String
		}
	}

	var violations []string
	for _, file := range changedFiles {
		if file == "" {
			continue
		}
		if owner, exists := lockMap[file]; exists && owner != taskID {
			violations = append(violations, fmt.Sprintf("%s (owned by %s)", file, owner))
		}
	}
	return violations
}

// auditMergedFiles compares files actually changed by a merge commit against
// the task's declared file locks and logs file_violation events for any extras.
// Informational only — errors are logged but never returned.
func (c *Conductor) auditMergedFiles(ctx context.Context, taskID string) {
	diffOutput, err := gitExec(c.RepoRoot, "diff", "--name-only", "HEAD~1", "HEAD")
	if err != nil {
		c.log("WARN: audit diff failed for task %s: %v", taskID, err)
		return
	}

	locks, err := c.DB.ListFileLocksForTask(ctx, taskID)
	if err != nil {
		c.log("WARN: audit lock query failed for task %s: %v", taskID, err)
		return
	}

	declared := make(map[string]bool, len(locks))
	for _, lock := range locks {
		declared[lock.FilePath] = true
	}

	var extras []string
	for _, line := range strings.Split(diffOutput, "\n") {
		file := strings.TrimSpace(line)
		if file == "" {
			continue
		}
		if !declared[file] {
			extras = append(extras, file)
			payload := fmt.Sprintf(`{"file_path":%q,"task_id":%q}`, file, taskID)
			c.DB.LogEvent(ctx, "file_violation", "", taskID, payload)
		}
	}

	if len(extras) > 0 {
		c.log("Task %s: %d extra files beyond spec (%v)", taskID, len(extras), extras)
	}
}

// gitExec runs a git command in the given directory.
func gitExec(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	return strings.TrimSpace(string(out)), err
}

// runTestCmd runs a test command in the repo root with a timeout, returning combined output and error.
// Uses "bash -c" to support complex commands (pipes, quotes, Docker-wrapped commands).
func runTestCmd(dir, testCmd string, timeout time.Duration) (string, error) {
	if strings.TrimSpace(testCmd) == "" {
		return "", nil
	}
	if timeout <= 0 {
		timeout = 5 * time.Minute
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "bash", "-l", "-c", testCmd)
	cmd.Dir = dir
	cmd.Env = os.Environ() // inherit full user environment (cargo, npm, go, etc.)
	out, err := cmd.CombinedOutput()
	if ctx.Err() == context.DeadlineExceeded {
		// G99: Kill stale Docker containers that may hold .git/index lock after timeout.
		// Without this, the next merge attempt fails with "Unable to write index".
		if strings.Contains(testCmd, "docker") {
			cleanupDockerContainers(dir)
		}
		return string(out), fmt.Errorf("test command timed out after %s", timeout)
	}
	return string(out), err
}

// cleanupDockerContainers kills any running Docker containers that were started
// from the given directory. Best-effort — errors are silently ignored.
func cleanupDockerContainers(dir string) {
	// Find containers with the orchestra label or that mount the repo directory
	listCmd := exec.Command("docker", "ps", "-q", "--filter", "label=orchestra")
	listOut, err := listCmd.Output()
	if err != nil || len(strings.TrimSpace(string(listOut))) == 0 {
		// Fallback: find containers mounting the repo dir
		listCmd = exec.Command("docker", "ps", "-q", "--filter", "volume="+dir)
		listOut, err = listCmd.Output()
		if err != nil || len(strings.TrimSpace(string(listOut))) == 0 {
			return
		}
	}

	ids := strings.Fields(strings.TrimSpace(string(listOut)))
	if len(ids) == 0 {
		return
	}

	args := append([]string{"kill"}, ids...)
	killCmd := exec.Command("docker", args...)
	killCmd.CombinedOutput() // best-effort
}

// ProbeResult captures whether a test environment is functional.
type ProbeResult struct {
	CanRun    bool
	Reason    string
	Framework string
}

// probeTestEnvironment checks whether the test command can actually run in the given directory.
// It identifies the test framework from the command and runs a lightweight check (e.g., --collect-only
// for pytest, -run ^$ for go test). A probe that fails means the test gate should be skipped
// rather than blocking the merge pipeline.
// Docker-prefixed commands skip the host probe entirely — the container image has deps by definition.
func probeTestEnvironment(dir, testCmd string, timeout time.Duration) ProbeResult {
	parts := strings.Fields(testCmd)
	if len(parts) == 0 {
		return ProbeResult{CanRun: false, Reason: "empty test command", Framework: "unknown"}
	}

	// Docker-wrapped test commands: deps exist inside the container image.
	// Skip host probing — just verify docker is available.
	if strings.HasPrefix(strings.TrimSpace(testCmd), "docker ") {
		if _, err := exec.LookPath("docker"); err != nil {
			return ProbeResult{CanRun: false, Reason: "docker not found on PATH", Framework: "docker"}
		}
		return ProbeResult{CanRun: true, Framework: "docker"}
	}

	runner := filepath.Base(parts[0])
	if timeout <= 0 {
		timeout = 30 * time.Second
	}

	// Check if the runner binary exists
	if _, err := exec.LookPath(parts[0]); err != nil {
		return ProbeResult{CanRun: false, Reason: fmt.Sprintf("binary not found: %s", parts[0]), Framework: runner}
	}

	// Framework-specific probes
	var probeArgs []string
	framework := runner

	switch {
	case runner == "pytest" || runner == "python3" || runner == "python":
		framework = "pytest"
		// Use the full command prefix + --collect-only to check if pytest can start
		probeArgs = append(parts, "--collect-only", "-q", "--no-header", "-x")
	case runner == "go":
		framework = "go"
		probeArgs = []string{"go", "test", "-run", "^$", "./..."}
	case runner == "npm" || runner == "npx":
		framework = "jest"
		probeArgs = []string{runner, "jest", "--listTests"}
	default:
		// Generic: try --version
		probeArgs = []string{parts[0], "--version"}
	}

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, probeArgs[0], probeArgs[1:]...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()

	if ctx.Err() == context.DeadlineExceeded {
		return ProbeResult{CanRun: false, Reason: "probe timed out", Framework: framework}
	}

	if err != nil {
		// pytest exit code 1 = tests collected but some fail — environment works
		if framework == "pytest" {
			if exitErr, ok := err.(*exec.ExitError); ok && exitErr.ExitCode() == 1 {
				return ProbeResult{CanRun: true, Framework: framework}
			}
		}
		return ProbeResult{
			CanRun:    false,
			Reason:    fmt.Sprintf("probe failed (exit %v): %s", err, truncate(string(out), 200)),
			Framework: framework,
		}
	}

	return ProbeResult{CanRun: true, Framework: framework}
}

// conflictFileRe matches git merge conflict markers like:
//
//	CONFLICT (content): Merge conflict in path/to/file.go
var conflictFileRe = regexp.MustCompile(`(?m)CONFLICT.*Merge conflict in (.+)$`)

// classifyMergeFailure inspects git merge output and returns:
//   - "conflict" + list of conflicting files if CONFLICT markers are found
//   - "error" + nil if the failure is not a git content conflict
func classifyMergeFailure(mergeOut string) (string, []string) {
	// G99: Detect transient lock errors (e.g. Docker container holding .git/index lock).
	// These should be retried rather than treated as permanent errors.
	if strings.Contains(mergeOut, "Unable to write index") {
		return "transient", nil
	}

	matches := conflictFileRe.FindAllStringSubmatch(mergeOut, -1)
	if len(matches) == 0 {
		return "error", nil
	}
	var files []string
	for _, m := range matches {
		files = append(files, strings.TrimSpace(m[1]))
	}
	return "conflict", files
}

// mustJSON marshals v to a JSON string, returning "null" on error.
func mustJSON(v interface{}) string {
	b, err := json.Marshal(v)
	if err != nil {
		return "null"
	}
	return string(b)
}

// triggerMergeRefinement stores feedback and triggers the refinement loop for a task
// that failed during the merge phase (test failure or review rejection).
func (c *Conductor) triggerMergeRefinement(ctx context.Context, taskID, failureType, feedback string) error {
	// Truncate feedback to 5000 chars
	if len(feedback) > 5000 {
		feedback = feedback[:5000] + "\n[TRUNCATED]"
	}

	// Store merge feedback and failure metadata
	c.setBlackboard(ctx, "merge_feedback:"+taskID, feedback)
	c.setBlackboard(ctx, "last_failure:"+taskID, failureType)
	c.setBlackboard(ctx, "failure_type:"+taskID, failureType)

	// Transition task from done → failed so Refine can pick it up
	if err := c.DB.FailTask(ctx, taskID, failureType); err != nil {
		return fmt.Errorf("failing task for refinement: %w", err)
	}

	// Trigger the existing refinement pipeline
	if _, err := c.Spawner.Refine(ctx, taskID); err != nil {
		return fmt.Errorf("refinement: %w", err)
	}

	c.DB.LogEvent(ctx, "merge_refinement_triggered", "", taskID,
		fmt.Sprintf(`{"failure_type":"%s","feedback_len":%d}`, failureType, len(feedback)))

	return nil
}

// attemptAutoResolve tries to automatically resolve merge conflicts in the main repo.
// It delegates to attemptAutoResolveInDir with c.RepoRoot as the working directory.
func (c *Conductor) attemptAutoResolve(ctx context.Context, taskID string, conflictFiles []string, branch, baseBranch string) ConflictResolution {
	return c.attemptAutoResolveInDir(ctx, c.RepoRoot, taskID, conflictFiles, branch, baseBranch)
}

// attemptAutoResolveInDir tries to automatically resolve merge conflicts in the given directory.
// It must be called while the merge is still in a conflicted state (before --abort).
// On success, it stages resolved files and commits. On failure, it resets with git checkout.
func (c *Conductor) attemptAutoResolveInDir(ctx context.Context, workDir string, taskID string, conflictFiles []string, branch, baseBranch string) ConflictResolution {
	res := ConflictResolution{Attempted: true}

	// Tier 0: Infrastructure files (.orchestra-hooks/) — mechanical "take ours" (G98).
	// These are identical hook scripts written per-worktree; conflicts are always trivial.
	var remaining []string
	for _, file := range conflictFiles {
		if strings.HasPrefix(file, ".orchestra-hooks/") {
			gitExec(workDir, "checkout", "--ours", file)
			gitExec(workDir, "add", file)
			res.FilesFixed = append(res.FilesFixed, file)
			if res.Tier == "" {
				res.Tier = "infrastructure"
			}
			continue
		}
		remaining = append(remaining, file)
	}
	conflictFiles = remaining

	// Tier 0.5: Binary files — cannot parse conflict markers; use "ours" strategy.
	// Binary conflicts happen with .webp, .png, .woff2, .ttf, .jpg, etc.
	{
		var textFiles []string
		for _, file := range conflictFiles {
			if isBinaryFile(filepath.Join(workDir, file)) {
				gitExec(workDir, "checkout", "--ours", file)
				gitExec(workDir, "add", file)
				res.FilesFixed = append(res.FilesFixed, file)
				if res.Tier == "" {
					res.Tier = "binary"
				}
				c.log("Binary conflict resolved (ours): %s", file)
				continue
			}
			textFiles = append(textFiles, file)
		}
		conflictFiles = textFiles
	}

	for _, file := range conflictFiles {
		absPath := filepath.Join(workDir, file)
		hunks, err := parseConflictMarkers(absPath)
		if err != nil || len(hunks) == 0 {
			res.FilesFailed = append(res.FilesFailed, file)
			res.Tier = "complex"
			continue
		}

		// Tier 1: Import-only conflicts (Go files)
		if strings.HasSuffix(file, ".go") && isImportOnlyConflict(absPath, hunks) {
			if err := resolveImportConflict(absPath, hunks); err == nil {
				gitExec(workDir, "add", file)
				res.FilesFixed = append(res.FilesFixed, file)
				if res.Tier == "" {
					res.Tier = "import"
				}
				continue
			}
		}

		// Tier 2: Non-overlapping changes (one side empty in each hunk)
		if isNonOverlapping(hunks) {
			if err := resolveNonOverlapping(absPath, hunks); err == nil {
				gitExec(workDir, "add", file)
				res.FilesFixed = append(res.FilesFixed, file)
				if res.Tier == "" || res.Tier == "import" {
					res.Tier = "non_overlapping"
				}
				continue
			}
		}

		// Tier 3: Complex — try LLM resolution as backstop
		res.FilesFailed = append(res.FilesFailed, file)
		res.Tier = "complex"
	}

	// Tier 3.5: Lock files — regenerate instead of text-merging.
	// Cargo.lock and package-lock.json are machine-generated and must be
	// regenerated after dependency changes, not text-merged by LLM.
	if len(res.FilesFailed) > 0 {
		var nonLockFailed []string
		for _, file := range res.FilesFailed {
			base := filepath.Base(file)
			if base == "Cargo.lock" || base == "package-lock.json" || base == "yarn.lock" || base == "pnpm-lock.yaml" {
				// Accept theirs and regenerate after merge completes
				gitExec(workDir, "checkout", "--theirs", file)
				gitExec(workDir, "add", file)
				res.FilesFixed = append(res.FilesFixed, file)
				c.log("Lock file %s: accepted theirs (will regenerate)", file)
			} else {
				nonLockFailed = append(nonLockFailed, file)
			}
		}
		res.FilesFailed = nonLockFailed
	}

	// Tier 4: LLM-based resolution for files that Tiers 1-3.5 couldn't handle
	if len(res.FilesFailed) > 0 && c.Runner != nil {
		var stillFailed []string
		for _, file := range res.FilesFailed {
			absPath := filepath.Join(workDir, file)
			task, _ := c.DB.GetTaskByID(ctx, taskID)
			var taskDesc string
			if task != nil && task.Description.Valid {
				taskDesc = task.Description.String
			}
			if err := c.resolveWithLLM(ctx, workDir, absPath, file, taskDesc); err == nil {
				gitExec(workDir, "add", file)
				res.FilesFixed = append(res.FilesFixed, file)
				res.Tier = "llm"
				c.log("LLM resolved conflict in %s", file)
			} else {
				c.log("LLM resolution failed for %s: %v", file, err)
				stillFailed = append(stillFailed, file)
			}
		}
		res.FilesFailed = stillFailed
	}

	// If any file still failed after all tiers, treat as full failure
	if len(res.FilesFailed) > 0 {
		gitExec(workDir, "checkout", "--", ".")
		gitExec(workDir, "merge", "--abort")
		res.Resolved = false
		return res
	}

	// Regenerate lock files after merge (they may have been accepted as-is
	// and need to reflect the merged dependency manifests).
	if _, err := os.Stat(filepath.Join(workDir, "src-tauri", "Cargo.toml")); err == nil {
		c.log("Regenerating Cargo.lock after merge")
		cmd := exec.Command("bash", "-l", "-c", "cargo generate-lockfile")
		cmd.Dir = filepath.Join(workDir, "src-tauri")
		cmd.Env = os.Environ()
		if out, err := cmd.CombinedOutput(); err != nil {
			c.log("WARN: cargo generate-lockfile failed: %v\n%s", err, string(out))
		} else {
			gitExec(workDir, "add", "src-tauri/Cargo.lock")
		}
	}
	if _, err := os.Stat(filepath.Join(workDir, "package.json")); err == nil {
		if _, err := os.Stat(filepath.Join(workDir, "package-lock.json")); err == nil {
			c.log("Regenerating package-lock.json after merge")
			cmd := exec.Command("bash", "-l", "-c", "npm install --package-lock-only")
			cmd.Dir = workDir
			cmd.Env = os.Environ()
			if out, err := cmd.CombinedOutput(); err != nil {
				c.log("WARN: npm install --package-lock-only failed: %v\n%s", err, string(out))
			} else {
				gitExec(workDir, "add", "package-lock.json")
			}
		}
	}

	// All files resolved — commit the merge
	_, err := gitExec(workDir, "commit", "--no-edit")
	if err != nil {
		// Commit failed — abort
		gitExec(workDir, "merge", "--abort")
		res.Resolved = false
		return res
	}

	res.Resolved = true
	return res
}

// parseConflictMarkers reads a file and extracts conflict hunks from git merge markers.
func parseConflictMarkers(filePath string) ([]ConflictHunk, error) {
	f, err := os.Open(filePath)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var hunks []ConflictHunk
	var current *ConflictHunk
	inOurs := false
	inTheirs := false
	scanner := bufio.NewScanner(f)
	lineNum := 0

	for scanner.Scan() {
		lineNum++
		line := scanner.Text()

		if strings.HasPrefix(line, "<<<<<<<") {
			current = &ConflictHunk{StartLine: lineNum}
			inOurs = true
			inTheirs = false
			continue
		}
		if strings.HasPrefix(line, "=======") && current != nil {
			inOurs = false
			inTheirs = true
			continue
		}
		if strings.HasPrefix(line, ">>>>>>>") && current != nil {
			current.EndLine = lineNum
			inOurs = false
			inTheirs = false
			hunks = append(hunks, *current)
			current = nil
			continue
		}

		if current != nil {
			if inOurs {
				if current.Ours != "" {
					current.Ours += "\n"
				}
				current.Ours += line
			} else if inTheirs {
				if current.Theirs != "" {
					current.Theirs += "\n"
				}
				current.Theirs += line
			}
		}
	}

	return hunks, scanner.Err()
}

// importBlockRe matches the start of a Go import block.
var importBlockRe = regexp.MustCompile(`(?m)^import \($`)

// isImportOnlyConflict checks whether all conflict hunks in a Go file fall
// within an import(...) block.
func isImportOnlyConflict(filePath string, hunks []ConflictHunk) bool {
	data, err := os.ReadFile(filePath)
	if err != nil {
		return false
	}
	content := string(data)

	// Find import block boundaries (line numbers)
	lines := strings.Split(content, "\n")
	var importStart, importEnd int
	inImport := false
	for i, line := range lines {
		lineNum := i + 1
		trimmed := strings.TrimSpace(line)
		if importBlockRe.MatchString(trimmed) || trimmed == "import (" {
			importStart = lineNum
			inImport = true
			continue
		}
		if inImport && trimmed == ")" {
			importEnd = lineNum
			break
		}
	}

	if importStart == 0 || importEnd == 0 {
		return false
	}

	for _, h := range hunks {
		if h.StartLine < importStart || h.EndLine > importEnd {
			return false
		}
	}
	return true
}

// resolveImportConflict merges conflicting import blocks by taking the union
// of import paths from both sides.
func resolveImportConflict(filePath string, hunks []ConflictHunk) error {
	data, err := os.ReadFile(filePath)
	if err != nil {
		return err
	}

	content := string(data)
	lines := strings.Split(content, "\n")

	// Collect all import paths from both sides of all hunks
	importSet := make(map[string]bool)
	for _, h := range hunks {
		for _, side := range []string{h.Ours, h.Theirs} {
			for _, line := range strings.Split(side, "\n") {
				trimmed := strings.TrimSpace(line)
				if trimmed == "" {
					continue
				}
				importSet[trimmed] = true
			}
		}
	}

	// Also collect non-conflicted imports from the file
	inImportBlock := false
	inConflict := false
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "import (" {
			inImportBlock = true
			continue
		}
		if inImportBlock && trimmed == ")" {
			break
		}
		if strings.HasPrefix(trimmed, "<<<<<<<") {
			inConflict = true
			continue
		}
		if strings.HasPrefix(trimmed, ">>>>>>>") {
			inConflict = false
			continue
		}
		if strings.HasPrefix(trimmed, "=======") {
			continue
		}
		if inImportBlock && !inConflict && trimmed != "" {
			importSet[trimmed] = true
		}
	}

	// Sort imports for deterministic output
	var sortedImports []string
	for imp := range importSet {
		sortedImports = append(sortedImports, imp)
	}
	sort.Strings(sortedImports)

	// Rebuild the file: replace everything from "import (" to ")" with merged imports
	var result strings.Builder
	inImportBlock = false
	importWritten := false
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "import (" {
			inImportBlock = true
			result.WriteString("import (\n")
			for _, imp := range sortedImports {
				result.WriteString("\t" + imp + "\n")
			}
			importWritten = true
			continue
		}
		if inImportBlock {
			if trimmed == ")" {
				inImportBlock = false
				result.WriteString(")\n")
			}
			// Skip all lines inside the old import block
			continue
		}
		if !importWritten || !inImportBlock {
			result.WriteString(line + "\n")
		}
	}

	// Trim trailing extra newline added by reconstruction
	output := result.String()
	if strings.HasSuffix(output, "\n\n") && !strings.HasSuffix(string(data), "\n\n") {
		output = strings.TrimRight(output, "\n") + "\n"
	}

	return os.WriteFile(filePath, []byte(output), 0o644)
}

// isNonOverlapping returns true if every hunk has one empty side (pure addition).
func isNonOverlapping(hunks []ConflictHunk) bool {
	for _, h := range hunks {
		oursEmpty := strings.TrimSpace(h.Ours) == ""
		theirsEmpty := strings.TrimSpace(h.Theirs) == ""
		if !oursEmpty && !theirsEmpty {
			return false
		}
	}
	return true
}

// resolveNonOverlapping resolves conflicts where one side is empty by keeping
// the non-empty side. It rewrites the file with conflict markers removed.
func resolveNonOverlapping(filePath string, _ []ConflictHunk) error {
	data, err := os.ReadFile(filePath)
	if err != nil {
		return err
	}

	lines := strings.Split(string(data), "\n")
	var result []string
	inConflict := false
	inTheirs := false
	var oursLines, theirsLines []string

	for _, line := range lines {
		if strings.HasPrefix(line, "<<<<<<<") {
			inConflict = true
			inTheirs = false
			oursLines = nil
			theirsLines = nil
			continue
		}
		if strings.HasPrefix(line, "=======") && inConflict {
			inTheirs = true
			continue
		}
		if strings.HasPrefix(line, ">>>>>>>") && inConflict {
			// Take the non-empty side
			oursContent := strings.TrimSpace(strings.Join(oursLines, "\n"))
			theirsContent := strings.TrimSpace(strings.Join(theirsLines, "\n"))
			if oursContent != "" {
				result = append(result, oursLines...)
			}
			if theirsContent != "" {
				result = append(result, theirsLines...)
			}
			inConflict = false
			inTheirs = false
			continue
		}

		if inConflict {
			if inTheirs {
				theirsLines = append(theirsLines, line)
			} else {
				oursLines = append(oursLines, line)
			}
		} else {
			result = append(result, line)
		}
	}

	return os.WriteFile(filePath, []byte(strings.Join(result, "\n")), 0o644)
}

// resolveWithLLM uses the ClaudeRunner to resolve a complex merge conflict.
// It reads the conflicted file (with markers), sends it to Claude with context
// about the task goal, and writes the resolved content back.
// workDir specifies the working directory for the LLM invocation.
func (c *Conductor) resolveWithLLM(ctx context.Context, workDir, absPath, relPath, taskDesc string) error {
	conflictedContent, err := os.ReadFile(absPath)
	if err != nil {
		return fmt.Errorf("reading conflicted file: %w", err)
	}

	// Build a focused prompt with the conflicted content and task context
	var prompt strings.Builder
	prompt.WriteString("You are resolving a git merge conflict. ")
	prompt.WriteString("The file below contains git conflict markers (<<<<<<< / ======= / >>>>>>>). ")
	prompt.WriteString("Return the fully resolved file content in the JSON response.\n\n")

	if taskDesc != "" {
		prompt.WriteString("## Task Context\n")
		desc := taskDesc
		if len(desc) > 2000 {
			desc = desc[:2000] + "\n[TRUNCATED]"
		}
		prompt.WriteString(desc)
		prompt.WriteString("\n\n")
	}

	prompt.WriteString("## File: " + relPath + "\n```\n")
	prompt.WriteString(string(conflictedContent))
	prompt.WriteString("\n```\n\n")
	prompt.WriteString("Rules:\n")
	prompt.WriteString("1. Include ALL changes from BOTH sides — do not drop any code\n")
	prompt.WriteString("2. If both sides modify the same line, use the version that best fits the task context\n")
	prompt.WriteString("3. Ensure the result compiles (valid syntax)\n")
	prompt.WriteString("4. Remove ALL conflict markers\n")
	prompt.WriteString("5. The resolved_content field must contain the COMPLETE file — nothing else\n")

	// Use JSON schema to force structured output — prevents LLM from
	// outputting reasoning/explanation instead of code (doc 021 Gap 4).
	const mergeResolveSchema = `{
		"type": "object",
		"properties": {
			"resolved_content": {
				"type": "string",
				"description": "The complete resolved file content with all conflict markers removed. Must be valid source code."
			}
		},
		"required": ["resolved_content"]
	}`

	runResult, err := c.Runner.Run(ctx, RunOpts{
		Prompt:     prompt.String(),
		Model:      agent.ModelOpus,
		WorkDir:    workDir,
		JSONSchema: mergeResolveSchema,
	})
	if err != nil {
		return fmt.Errorf("LLM run failed: %w", err)
	}

	// Parse JSON response to extract resolved content
	var resolveResult struct {
		ResolvedContent string `json:"resolved_content"`
	}
	resolved := strings.TrimSpace(runResult.Output)
	if err := json.Unmarshal([]byte(resolved), &resolveResult); err != nil {
		// Fallback: if JSON parsing fails, try using raw output with cleanup
		c.log("LLM merge resolve: JSON parse failed, falling back to raw output: %v", err)
		if strings.HasPrefix(resolved, "```") {
			lines := strings.SplitN(resolved, "\n", 2)
			if len(lines) > 1 {
				resolved = lines[1]
			}
			if idx := strings.LastIndex(resolved, "```"); idx >= 0 {
				resolved = resolved[:idx]
			}
			resolved = strings.TrimSpace(resolved)
		}
	} else {
		resolved = resolveResult.ResolvedContent
	}

	// Sanity check: resolved content must not contain conflict markers
	if strings.Contains(resolved, "<<<<<<<") || strings.Contains(resolved, ">>>>>>>") {
		return fmt.Errorf("LLM output still contains conflict markers")
	}

	// Sanity check: for Go files, verify the output starts with a package declaration.
	// The LLM sometimes outputs reasoning/explanation instead of code (doc 021, Gap 4).
	if strings.HasSuffix(relPath, ".go") {
		trimmed := strings.TrimSpace(resolved)
		if !strings.HasPrefix(trimmed, "package ") && !strings.HasPrefix(trimmed, "// ") && !strings.HasPrefix(trimmed, "/*") {
			return fmt.Errorf("LLM output for .go file does not start with package declaration — likely contains reasoning instead of code")
		}
	}

	// Write resolved content
	if err := os.WriteFile(absPath, []byte(resolved+"\n"), 0o644); err != nil {
		return fmt.Errorf("writing resolved file: %w", err)
	}

	// Post-write validation for Go files: run go build to verify the resolved output
	// compiles. If build fails, restore the conflicted version and return an error so
	// the caller can escalate (doc 021, Gap 5).
	// Only run build check if go.mod exists in the worktree (indicates a Go module).
	if strings.HasSuffix(relPath, ".go") {
		goModPath := filepath.Join(workDir, "go.mod")
		if _, statErr := os.Stat(goModPath); statErr == nil {
			// Derive package path relative to worktree root
			packageDir := filepath.Dir(relPath)
			if packageDir == "." {
				packageDir = "."
			} else {
				packageDir = "./" + packageDir
			}

			buildCmd := exec.Command("go", "build", packageDir)
			buildCmd.Dir = workDir
			buildOutput, buildErr := buildCmd.CombinedOutput()
			if buildErr != nil {
				// Restore the conflicted version so the caller can retry or escalate
				restoreErr := os.WriteFile(absPath, conflictedContent, 0o644)
				if restoreErr != nil {
					return fmt.Errorf("LLM-resolved Go file failed to compile (%s) AND restore failed: %v; build output: %s",
						buildErr, restoreErr, string(buildOutput))
				}
				return fmt.Errorf("LLM-resolved Go file failed to compile — restored conflicted version for escalation: %s; build output: %s",
					buildErr, strings.TrimSpace(string(buildOutput)))
			}
		}
	}

	return nil
}

// binaryExtensions lists file extensions that are always binary.
var binaryExtensions = map[string]bool{
	".webp": true, ".png": true, ".jpg": true, ".jpeg": true, ".gif": true,
	".ico": true, ".bmp": true, ".tiff": true, ".tif": true, ".svg": false, // SVG is text
	".woff": true, ".woff2": true, ".ttf": true, ".otf": true, ".eot": true,
	".zip": true, ".gz": true, ".tar": true, ".bz2": true, ".xz": true,
	".pdf": true, ".wasm": true, ".so": true, ".dylib": true, ".dll": true,
	".exe": true, ".bin": true, ".dat": true, ".db": true, ".sqlite": true,
	".mp3": true, ".mp4": true, ".wav": true, ".ogg": true, ".flac": true,
	".avi": true, ".mov": true, ".webm": true,
}

// isBinaryFile returns true if the file at the given path is binary.
// It checks the file extension first, then probes the first 8KB for null bytes.
func isBinaryFile(filePath string) bool {
	ext := strings.ToLower(filepath.Ext(filePath))
	if isBin, known := binaryExtensions[ext]; known {
		return isBin
	}

	// Probe first 8KB for null bytes
	f, err := os.Open(filePath)
	if err != nil {
		return false
	}
	defer f.Close()

	buf := make([]byte, 8192)
	n, err := f.Read(buf)
	if err != nil || n == 0 {
		return false
	}
	for _, b := range buf[:n] {
		if b == 0 {
			return true
		}
	}
	return false
}
