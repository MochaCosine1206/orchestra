package orchestrator

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/MochaCosine1206/orchestra/internal/db"
)

// MergeStrategy controls how completed task branches are merged.
type MergeStrategy string

const (
	// MergeStrategyBatch waits for all tasks then merges in batch (default).
	MergeStrategyBatch MergeStrategy = "batch"
	// MergeStrategyFIFO processes completed tasks as they finish, one at a time.
	MergeStrategyFIFO MergeStrategy = "fifo"
)

// MergeQueueResult captures the outcome of FIFO queue processing.
type MergeQueueResult struct {
	Merged       []string // branch names merged successfully
	Failed       []string // branch names that failed to merge
	Refining     []string // branches sent back for refinement
	AutoResolved []string // branches where conflicts were auto-resolved
	TestsFailed  bool
}

// StartMergeQueue runs the FIFO merge queue as a background goroutine.
// It polls for completed tasks, enqueues them, and processes the queue
// sequentially. Returns a channel that receives the final result when
// all tasks are terminal and the queue is drained.
func (c *Conductor) StartMergeQueue(ctx context.Context, opts MergeOpts) (<-chan *MergeQueueResult, error) {
	resultCh := make(chan *MergeQueueResult, 1)

	conductorID := c.ConductorID
	if conductorID == "" {
		conductorID = c.SessionID
	}

	go func() {
		defer close(resultCh)
		result := &MergeQueueResult{}

		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				resultCh <- result
				return
			case <-ticker.C:
				// Enqueue any newly completed tasks
				if err := c.enqueueCompletedTasks(ctx, conductorID); err != nil {
					c.log("Merge queue: enqueue error: %v", err)
				}

				// Process next entry in the queue
				processed, err := c.processNextEntry(ctx, conductorID, opts)
				if err != nil {
					c.log("Merge queue: process error: %v", err)
				}
				if processed != nil {
					switch processed.Status {
					case "merged":
						result.Merged = append(result.Merged, processed.Branch)
					case "failed":
						result.Failed = append(result.Failed, processed.Branch)
					case "refining":
						result.Refining = append(result.Refining, processed.Branch)
					}
				}

				// Check if we're done: all tasks terminal and queue drained
				if c.isMergeQueueComplete(ctx, conductorID) {
					resultCh <- result
					return
				}
			}
		}
	}()

	return resultCh, nil
}

// enqueueCompletedTasks finds done tasks not yet in the queue and enqueues them.
func (c *Conductor) enqueueCompletedTasks(ctx context.Context, conductorID string) error {
	taskIDs := c.sessionTaskIDs(ctx)

	// Get all tasks
	allTasks, err := c.DB.ListTasks(ctx)
	if err != nil {
		return fmt.Errorf("listing tasks: %w", err)
	}

	sessionSet := make(map[string]bool)
	for _, id := range taskIDs {
		sessionSet[id] = true
	}

	// Filter to completed tasks with branches
	var doneTasks []db.Task
	for _, t := range allTasks {
		if !sessionSet[t.ID] || t.Status != "done" {
			continue
		}
		if !t.Branch.Valid || t.Branch.String == "" {
			continue
		}
		doneTasks = append(doneTasks, t)
	}

	// Compute positions via toposort
	positions, err := computeQueuePositions(doneTasks)
	if err != nil {
		// Fallback to creation order
		for i, t := range doneTasks {
			positions[t.ID] = i
		}
	}

	// Enqueue tasks not already in the queue
	for _, t := range doneTasks {
		existing, err := c.DB.GetMergeQueueEntryByTaskID(ctx, conductorID, t.ID)
		if err != nil {
			c.log("Merge queue: error checking entry for %s: %v", t.ID, err)
			continue
		}
		if existing != nil {
			// G117: If the entry was sent for refinement and the task is now
			// "done" again, transition the entry back to "pending" so it
			// re-enters the merge queue.
			if existing.Status == "refining" {
				c.DB.UpdateMergeQueueEntryStatus(ctx, existing.ID, "pending")
				c.log("Merge queue: re-enqueued %s after refinement", t.Branch.String)
			}
			continue // already enqueued (or just re-enqueued)
		}

		entry := db.MergeQueueEntry{
			ID:          db.GenID("mq"),
			ConductorID: conductorID,
			TaskID:      t.ID,
			Branch:      t.Branch.String,
			Position:    positions[t.ID],
			Status:      "pending",
		}

		// Check if dependencies are all merged
		if t.BlockedBy.Valid && t.BlockedBy.String != "" && t.BlockedBy.String != "[]" {
			if !c.areDependenciesMerged(ctx, conductorID, t.BlockedBy.String) {
				entry.Status = "blocked"
			}
		}

		if err := c.DB.CreateMergeQueueEntry(ctx, entry); err != nil {
			c.log("Merge queue: error enqueuing %s: %v", t.ID, err)
			continue
		}
		c.log("Merge queue: enqueued %s at position %d (status=%s)", t.Branch.String, entry.Position, entry.Status)
		c.DB.LogEvent(ctx, "merge_queue_enqueued", "", t.ID,
			fmt.Sprintf(`{"branch":%s,"position":%d}`, mustJSON(t.Branch.String), entry.Position))
	}

	// Unblock entries whose dependencies are now merged
	c.unblockReadyEntries(ctx, conductorID)

	return nil
}

// processNextEntry dequeues and processes the next pending entry.
// Returns the processed entry (with updated status) or nil if nothing to process.
func (c *Conductor) processNextEntry(ctx context.Context, conductorID string, opts MergeOpts) (*db.MergeQueueEntry, error) {
	entry, err := c.DB.NextPendingMergeQueueEntry(ctx, conductorID)
	if err != nil {
		return nil, fmt.Errorf("getting next entry: %w", err)
	}
	if entry == nil {
		return nil, nil // nothing to process
	}

	c.log("Merge queue: processing %s (branch=%s, position=%d)", entry.TaskID, entry.Branch, entry.Position)

	// Mark as merging
	if err := c.DB.UpdateMergeQueueEntryStatus(ctx, entry.ID, "merging"); err != nil {
		return nil, fmt.Errorf("marking entry merging: %w", err)
	}

	// Attempt the merge using existing merge infrastructure
	mergeErr := c.mergeOneEntry(ctx, entry, opts)
	if mergeErr != nil {
		// Merge failed — decide whether to refine or permanently fail
		if c.shouldRefineMergeEntry(ctx, entry) {
			if refineErr := c.requeueForRefinement(ctx, conductorID, entry, mergeErr.Error()); refineErr != nil {
				c.log("Merge queue: refinement failed for %s: %v", entry.Branch, refineErr)
				c.DB.SetMergeQueueEntryFailed(ctx, entry.ID, mergeErr.Error())
				entry.Status = "failed"
			} else {
				entry.Status = "refining"
			}
		} else {
			c.DB.SetMergeQueueEntryFailed(ctx, entry.ID, mergeErr.Error())
			entry.Status = "failed"
		}
		return entry, nil
	}

	// Success
	if err := c.DB.SetMergeQueueEntryMerged(ctx, entry.ID); err != nil {
		c.log("Merge queue: error marking merged %s: %v", entry.ID, err)
	}

	// B-145: Record branch as merged for crash recovery
	c.DB.AddMergedBranch(ctx, conductorID, entry.Branch)
	c.DB.LogEvent(ctx, "branch_merged", "", entry.TaskID,
		fmt.Sprintf(`{"branch":"%s","via":"fifo_queue"}`, entry.Branch))
	c.log("Merge queue: merged %s", entry.Branch)

	entry.Status = "merged"
	return entry, nil
}

// mergeOneEntry performs the actual git merge for a single queue entry.
// This reuses the same merge logic as batch Merge(): git merge, auto-resolve, test gate.
func (c *Conductor) mergeOneEntry(ctx context.Context, entry *db.MergeQueueEntry, opts MergeOpts) error {
	branch := entry.Branch
	conductorID := entry.ConductorID

	// Get merge target (staging branch)
	mergeBranch := opts.BaseBranch
	if mergeBranch == "" {
		mergeBranch = c.getBlackboard(ctx, "base_branch")
	}
	if conductorID != "" {
		if rec, err := c.DB.GetConductor(ctx, conductorID); err == nil && rec != nil && rec.StagingBranch != "" {
			mergeBranch = rec.StagingBranch
		}
	}

	// Check if branch ref exists
	if _, err := gitExec(c.RepoRoot, "rev-parse", "--verify", branch); err != nil {
		return nil // branch already merged or deleted — treat as success
	}

	// Check if already merged (ancestor check)
	if _, err := gitExec(c.RepoRoot, "merge-base", "--is-ancestor", branch, mergeBranch); err == nil {
		return nil // already merged
	}

	// Ensure we're on the merge target branch
	if _, err := gitExec(c.RepoRoot, "checkout", mergeBranch); err != nil {
		return fmt.Errorf("checkout merge branch %s: %w", mergeBranch, err)
	}

	// Attempt merge
	mergeOut, err := gitExec(c.RepoRoot, "merge", "--no-ff", branch,
		"-m", fmt.Sprintf("Merge %s into %s (FIFO queue)", branch, mergeBranch))
	if err != nil {
		failureType, conflictFiles := classifyMergeFailure(mergeOut)

		// Auto-resolve if enabled
		autoResolveFlag := c.getBlackboard(ctx, "conductor:auto_resolve_conflicts")
		if failureType == "conflict" && autoResolveFlag == "1" {
			resolution := c.attemptAutoResolve(ctx, entry.TaskID, conflictFiles, branch, mergeBranch)
			if resolution.Resolved {
				c.log("Merge queue: auto-resolved conflicts for %s (tier=%s)", branch, resolution.Tier)
				c.DB.LogEvent(ctx, "merge_auto_resolved", "", entry.TaskID,
					fmt.Sprintf(`{"tier":%s,"via":"fifo_queue"}`, mustJSON(resolution.Tier)))
				goto testGate
			}
		}

		// Merge failed
		gitExec(c.RepoRoot, "merge", "--abort")
		return fmt.Errorf("merge %s: %s: %s", branch, failureType, truncate(mergeOut, 500))
	}

	// Post-merge content clobber detection
	c.checkContentClobber(ctx, entry.TaskID)

testGate:
	// Run test command if provided
	if opts.TestCmd != "" {
		testOutput, testErr := runTestCmd(c.RepoRoot, opts.TestCmd, opts.TestCmdTimeout)
		if testErr != nil {
			mode := opts.TestFailureMode
			if mode == "" {
				mode = TestFailureModeRevertAndRefine
			}
			switch mode {
			case TestFailureModeWarnOnly:
				c.log("Merge queue: test failed for %s (warn_only, keeping merge)", branch)
			default:
				// Revert merge
				gitExec(c.RepoRoot, "reset", "--hard", "HEAD~1")
				return fmt.Errorf("test failed after merge %s: %s", branch, truncate(testOutput, 500))
			}
		}
	}

	// Worktree + branch cleanup after successful merge
	if task, err := c.DB.GetTaskByID(ctx, entry.TaskID); err == nil && task != nil && task.Worktree.Valid {
		wt := task.Worktree.String
		if _, rmErr := gitExec(c.RepoRoot, "worktree", "remove", "--force", wt); rmErr != nil {
			c.log("WARN: worktree cleanup failed for %s: %v", wt, rmErr)
		}
	}
	if _, err := gitExec(c.RepoRoot, "branch", "-d", branch); err != nil {
		c.log("WARN: branch cleanup failed for %s: %v", branch, err)
	}

	// Release file locks
	if locks, err := c.DB.ListFileLocksForTask(ctx, entry.TaskID); err == nil {
		for _, lock := range locks {
			c.DB.ReleaseFileLock(ctx, lock.FilePath)
		}
	}

	return nil
}

// computeQueuePositions assigns merge positions based on toposort order.
func computeQueuePositions(tasks []db.Task) (map[string]int, error) {
	positions := make(map[string]int)
	if len(tasks) == 0 {
		return positions, nil
	}

	sorted, err := TopoSort(tasks)
	if err != nil {
		return positions, err
	}

	for i, id := range sorted {
		positions[id] = i
	}
	return positions, nil
}

// shouldRefineMergeEntry returns true if the entry should be retried via refinement.
// Circuit breaker: max 2 refinement attempts.
func (c *Conductor) shouldRefineMergeEntry(ctx context.Context, entry *db.MergeQueueEntry) bool {
	return entry.RefinementCount < 2
}

// requeueForRefinement triggers task refinement and requeues the entry at the end.
func (c *Conductor) requeueForRefinement(ctx context.Context, conductorID string, entry *db.MergeQueueEntry, feedback string) error {
	// Increment refinement count
	if err := c.DB.IncrementMergeQueueRefinement(ctx, entry.ID); err != nil {
		return fmt.Errorf("incrementing refinement: %w", err)
	}

	// Trigger refinement on the task
	if err := c.triggerMergeRefinement(ctx, entry.TaskID, "fifo_merge_failed", feedback); err != nil {
		return fmt.Errorf("triggering refinement: %w", err)
	}

	// Get the max position to place this entry at the end
	entries, err := c.DB.ListMergeQueueEntries(ctx, conductorID)
	if err != nil {
		return fmt.Errorf("listing entries for requeue: %w", err)
	}
	maxPos := 0
	for _, e := range entries {
		if e.Position > maxPos {
			maxPos = e.Position
		}
	}

	// Requeue at end with refining status (will become pending when task completes again)
	if err := c.DB.UpdateMergeQueueEntryStatus(ctx, entry.ID, "refining"); err != nil {
		return fmt.Errorf("marking entry refining: %w", err)
	}

	c.log("Merge queue: requeued %s for refinement (attempt %d)", entry.Branch, entry.RefinementCount+1)
	c.DB.LogEvent(ctx, "merge_queue_requeued", "", entry.TaskID,
		fmt.Sprintf(`{"refinement":%d,"reason":%s}`, entry.RefinementCount+1, mustJSON(truncate(feedback, 200))))

	return nil
}

// areDependenciesMerged checks if all task dependencies have been merged in the queue.
func (c *Conductor) areDependenciesMerged(ctx context.Context, conductorID, blockedByJSON string) bool {
	blockedByJSON = strings.TrimSpace(blockedByJSON)
	if blockedByJSON == "" || blockedByJSON == "[]" {
		return true
	}

	// Parse the blocked_by JSON array
	var depIDs []string
	for _, raw := range strings.Split(strings.Trim(blockedByJSON, "[]"), ",") {
		id := strings.Trim(strings.TrimSpace(raw), `"`)
		if id != "" {
			depIDs = append(depIDs, id)
		}
	}

	for _, depID := range depIDs {
		entry, err := c.DB.GetMergeQueueEntryByTaskID(ctx, conductorID, depID)
		if err != nil || entry == nil || entry.Status != "merged" {
			return false
		}
	}
	return true
}

// unblockReadyEntries transitions blocked entries to pending when their deps are merged.
func (c *Conductor) unblockReadyEntries(ctx context.Context, conductorID string) {
	entries, err := c.DB.ListMergeQueueEntries(ctx, conductorID)
	if err != nil {
		return
	}

	for _, e := range entries {
		if e.Status != "blocked" {
			continue
		}

		// Look up the task to get its blocked_by
		task, err := c.DB.GetTaskByID(ctx, e.TaskID)
		if err != nil || task == nil {
			continue
		}

		if task.BlockedBy.Valid && c.areDependenciesMerged(ctx, conductorID, task.BlockedBy.String) {
			c.DB.UpdateMergeQueueEntryStatus(ctx, e.ID, "pending")
			c.log("Merge queue: unblocked %s (deps merged)", e.Branch)
		}
	}
}

// --- Hierarchical merge (B-281): two-level cluster → staging merge ---

// FeatureClusterBranch returns the branch name for a feature cluster.
func FeatureClusterBranch(conductorID, cluster string) string {
	return fmt.Sprintf("cluster/%s/%s", conductorID, cluster)
}

// createClusterBranches creates feature branches for each unique cluster, forked from staging HEAD.
func (c *Conductor) createClusterBranches(ctx context.Context, conductorID string, stagingBranch string) error {
	clusters, err := c.DB.ListDistinctClusters(ctx, conductorID)
	if err != nil {
		return fmt.Errorf("listing clusters: %w", err)
	}

	for _, cluster := range clusters {
		branchName := FeatureClusterBranch(conductorID, cluster)
		// Check if branch already exists
		if _, err := gitExec(c.RepoRoot, "rev-parse", "--verify", branchName); err == nil {
			c.log("Cluster branch %s already exists, skipping creation", branchName)
			continue
		}
		// Create branch from staging HEAD
		if _, err := gitExec(c.RepoRoot, "branch", branchName, stagingBranch); err != nil {
			return fmt.Errorf("creating cluster branch %s from %s: %w", branchName, stagingBranch, err)
		}
		c.log("Created cluster branch %s from %s", branchName, stagingBranch)
		c.DB.LogEvent(ctx, "cluster_branch_created", "", "",
			fmt.Sprintf(`{"cluster":%s,"branch":%s}`, mustJSON(cluster), mustJSON(branchName)))
	}
	return nil
}

// mergeOneEntryToCluster merges a task branch into its cluster branch (instead of staging).
// This is the first level of the two-level merge.
func (c *Conductor) mergeOneEntryToCluster(ctx context.Context, entry *db.MergeQueueEntry, opts MergeOpts) error {
	// Look up the task to get its cluster
	task, err := c.DB.GetTaskByID(ctx, entry.TaskID)
	if err != nil || task == nil {
		return fmt.Errorf("getting task %s: %w", entry.TaskID, err)
	}

	if !task.FeatureCluster.Valid || task.FeatureCluster.String == "" {
		// No cluster — fall back to merging directly to staging
		return c.mergeOneEntry(ctx, entry, opts)
	}

	clusterBranch := FeatureClusterBranch(entry.ConductorID, task.FeatureCluster.String)
	taskBranch := entry.Branch

	// Check if branch ref exists
	if _, err := gitExec(c.RepoRoot, "rev-parse", "--verify", taskBranch); err != nil {
		return nil // branch already merged or deleted
	}

	// Check if already merged
	if _, err := gitExec(c.RepoRoot, "merge-base", "--is-ancestor", taskBranch, clusterBranch); err == nil {
		return nil // already merged
	}

	// Checkout cluster branch
	if _, err := gitExec(c.RepoRoot, "checkout", clusterBranch); err != nil {
		return fmt.Errorf("checkout cluster branch %s: %w", clusterBranch, err)
	}

	// Attempt merge
	mergeOut, err := gitExec(c.RepoRoot, "merge", "--no-ff", taskBranch,
		"-m", fmt.Sprintf("Merge %s into %s (cluster merge)", taskBranch, clusterBranch))
	if err != nil {
		failureType, conflictFiles := classifyMergeFailure(mergeOut)

		// Auto-resolve if enabled
		autoResolveFlag := c.getBlackboard(ctx, "conductor:auto_resolve_conflicts")
		if failureType == "conflict" && autoResolveFlag == "1" {
			resolution := c.attemptAutoResolve(ctx, entry.TaskID, conflictFiles, taskBranch, clusterBranch)
			if resolution.Resolved {
				c.log("Cluster merge: auto-resolved conflicts for %s → %s (tier=%s)", taskBranch, clusterBranch, resolution.Tier)
				goto testGate
			}
		}

		gitExec(c.RepoRoot, "merge", "--abort")
		return fmt.Errorf("merge %s → %s: %s: %s", taskBranch, clusterBranch, failureType, truncate(mergeOut, 500))
	}

testGate:
	// Run test command if provided
	if opts.TestCmd != "" {
		testOutput, testErr := runTestCmd(c.RepoRoot, opts.TestCmd, opts.TestCmdTimeout)
		if testErr != nil {
			mode := opts.TestFailureMode
			if mode == "" {
				mode = TestFailureModeRevertAndRefine
			}
			switch mode {
			case TestFailureModeWarnOnly:
				c.log("Cluster merge: test failed for %s (warn_only, keeping merge)", taskBranch)
			default:
				gitExec(c.RepoRoot, "reset", "--hard", "HEAD~1")
				return fmt.Errorf("test failed after cluster merge %s → %s: %s", taskBranch, clusterBranch, truncate(testOutput, 500))
			}
		}
	}

	// Worktree + branch cleanup after successful merge
	if task.Worktree.Valid {
		wt := task.Worktree.String
		if _, rmErr := gitExec(c.RepoRoot, "worktree", "remove", "--force", wt); rmErr != nil {
			c.log("WARN: worktree cleanup failed for %s: %v", wt, rmErr)
		}
	}
	if _, err := gitExec(c.RepoRoot, "branch", "-d", taskBranch); err != nil {
		c.log("WARN: branch cleanup failed for %s: %v", taskBranch, err)
	}

	// Release file locks
	if locks, err := c.DB.ListFileLocksForTask(ctx, entry.TaskID); err == nil {
		for _, lock := range locks {
			c.DB.ReleaseFileLock(ctx, lock.FilePath)
		}
	}

	c.log("Cluster merge: merged %s → %s", taskBranch, clusterBranch)
	c.DB.LogEvent(ctx, "cluster_task_merged", "", entry.TaskID,
		fmt.Sprintf(`{"task_branch":%s,"cluster_branch":%s,"cluster":%s}`,
			mustJSON(taskBranch), mustJSON(clusterBranch), mustJSON(task.FeatureCluster.String)))
	return nil
}

// isClusterComplete returns true if all tasks in a cluster are "done" with
// merged queue entries. Failed tasks do NOT count as complete — a cluster
// with any failed task is not eligible for cluster→staging merge (G118).
func (c *Conductor) isClusterComplete(ctx context.Context, conductorID, cluster string) bool {
	tasks, err := c.DB.ListTasksByCluster(ctx, conductorID, cluster)
	if err != nil || len(tasks) == 0 {
		return false
	}

	for _, t := range tasks {
		if t.Status != "done" {
			return false // includes "failed", "running", "pending", etc.
		}
		// All done tasks must have a merged queue entry
		entry, err := c.DB.GetMergeQueueEntryByTaskID(ctx, conductorID, t.ID)
		if err != nil || entry == nil || entry.Status != "merged" {
			return false
		}
	}
	return true
}

// mergeClusterToStaging merges a completed cluster branch into staging.
func (c *Conductor) mergeClusterToStaging(ctx context.Context, conductorID, cluster string, opts MergeOpts) error {
	clusterBranch := FeatureClusterBranch(conductorID, cluster)

	// Get staging branch
	stagingBranch := opts.BaseBranch
	if stagingBranch == "" {
		stagingBranch = c.getBlackboard(ctx, "base_branch")
	}
	if conductorID != "" {
		if rec, err := c.DB.GetConductor(ctx, conductorID); err == nil && rec != nil && rec.StagingBranch != "" {
			stagingBranch = rec.StagingBranch
		}
	}

	// Check if cluster branch exists
	if _, err := gitExec(c.RepoRoot, "rev-parse", "--verify", clusterBranch); err != nil {
		return nil // branch doesn't exist — nothing to merge
	}

	// Check if already merged
	if _, err := gitExec(c.RepoRoot, "merge-base", "--is-ancestor", clusterBranch, stagingBranch); err == nil {
		return nil // already merged
	}

	// Checkout staging
	if _, err := gitExec(c.RepoRoot, "checkout", stagingBranch); err != nil {
		return fmt.Errorf("checkout staging %s: %w", stagingBranch, err)
	}

	// Merge cluster branch to staging
	mergeOut, err := gitExec(c.RepoRoot, "merge", "--no-ff", clusterBranch,
		"-m", fmt.Sprintf("Merge cluster %s (%s) into %s", cluster, clusterBranch, stagingBranch))
	if err != nil {
		failureType, conflictFiles := classifyMergeFailure(mergeOut)

		autoResolveFlag := c.getBlackboard(ctx, "conductor:auto_resolve_conflicts")
		if failureType == "conflict" && autoResolveFlag == "1" {
			resolution := c.attemptAutoResolve(ctx, "", conflictFiles, clusterBranch, stagingBranch)
			if resolution.Resolved {
				c.log("Cluster→staging: auto-resolved conflicts for %s (tier=%s)", cluster, resolution.Tier)
				goto clusterTestGate
			}
		}

		gitExec(c.RepoRoot, "merge", "--abort")
		return fmt.Errorf("merge cluster %s → staging: %s: %s", cluster, failureType, truncate(mergeOut, 500))
	}

clusterTestGate:
	// Run tests after cluster-to-staging merge
	if opts.TestCmd != "" {
		testOutput, testErr := runTestCmd(c.RepoRoot, opts.TestCmd, opts.TestCmdTimeout)
		if testErr != nil {
			mode := opts.TestFailureMode
			if mode == "" {
				mode = TestFailureModeRevertAndRefine
			}
			switch mode {
			case TestFailureModeWarnOnly:
				c.log("Cluster→staging: test failed for %s (warn_only, keeping merge)", cluster)
			default:
				gitExec(c.RepoRoot, "reset", "--hard", "HEAD~1")
				return fmt.Errorf("test failed after cluster→staging merge %s: %s", cluster, truncate(testOutput, 500))
			}
		}
	}

	// Cleanup cluster branch
	if _, err := gitExec(c.RepoRoot, "branch", "-d", clusterBranch); err != nil {
		c.log("WARN: cluster branch cleanup failed for %s: %v", clusterBranch, err)
	}

	c.log("Cluster→staging: merged cluster %s → %s", cluster, stagingBranch)
	c.DB.LogEvent(ctx, "cluster_merged_to_staging", "", "",
		fmt.Sprintf(`{"cluster":%s,"branch":%s,"staging":%s}`,
			mustJSON(cluster), mustJSON(clusterBranch), mustJSON(stagingBranch)))

	// Record branch as merged for crash recovery
	c.DB.AddMergedBranch(ctx, conductorID, clusterBranch)

	return nil
}

// StartHierarchicalMergeQueue runs the FIFO merge queue with two-level cluster merging.
// Task branches merge into cluster branches, then completed clusters merge into staging.
func (c *Conductor) StartHierarchicalMergeQueue(ctx context.Context, opts MergeOpts) (<-chan *MergeQueueResult, error) {
	conductorID := c.ConductorID
	if conductorID == "" {
		conductorID = c.SessionID
	}

	// Get staging branch
	stagingBranch := opts.BaseBranch
	if conductorID != "" {
		if rec, err := c.DB.GetConductor(ctx, conductorID); err == nil && rec != nil && rec.StagingBranch != "" {
			stagingBranch = rec.StagingBranch
		}
	}

	// Create cluster branches from staging HEAD
	if err := c.createClusterBranches(ctx, conductorID, stagingBranch); err != nil {
		return nil, fmt.Errorf("creating cluster branches: %w", err)
	}

	resultCh := make(chan *MergeQueueResult, 1)
	mergedClusters := make(map[string]bool)

	go func() {
		defer close(resultCh)
		result := &MergeQueueResult{}

		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				resultCh <- result
				return
			case <-ticker.C:
				// Enqueue any newly completed tasks
				if err := c.enqueueCompletedTasks(ctx, conductorID); err != nil {
					c.log("Hierarchical queue: enqueue error: %v", err)
				}

				// Process next entry — merge to cluster branch (not staging)
				entry, err := c.DB.NextPendingMergeQueueEntry(ctx, conductorID)
				if err != nil {
					c.log("Hierarchical queue: next entry error: %v", err)
					continue
				}
				if entry != nil {
					c.log("Hierarchical queue: processing %s (branch=%s)", entry.TaskID, entry.Branch)
					c.DB.UpdateMergeQueueEntryStatus(ctx, entry.ID, "merging")

					mergeErr := c.mergeOneEntryToCluster(ctx, entry, opts)
					if mergeErr != nil {
						if c.shouldRefineMergeEntry(ctx, entry) {
							if refineErr := c.requeueForRefinement(ctx, conductorID, entry, mergeErr.Error()); refineErr != nil {
								c.DB.SetMergeQueueEntryFailed(ctx, entry.ID, mergeErr.Error())
								result.Failed = append(result.Failed, entry.Branch)
							} else {
								result.Refining = append(result.Refining, entry.Branch)
							}
						} else {
							c.DB.SetMergeQueueEntryFailed(ctx, entry.ID, mergeErr.Error())
							result.Failed = append(result.Failed, entry.Branch)
						}
					} else {
						c.DB.SetMergeQueueEntryMerged(ctx, entry.ID)
						c.DB.AddMergedBranch(ctx, conductorID, entry.Branch)
						result.Merged = append(result.Merged, entry.Branch)
					}
				}

				// Check for completed clusters and merge them to staging
				clusters, _ := c.DB.ListDistinctClusters(ctx, conductorID)
				for _, cluster := range clusters {
					if mergedClusters[cluster] {
						continue
					}
					if c.isClusterComplete(ctx, conductorID, cluster) {
						c.log("Hierarchical queue: cluster %q complete — merging to staging", cluster)
						if err := c.mergeClusterToStaging(ctx, conductorID, cluster, opts); err != nil {
							c.log("Hierarchical queue: cluster→staging merge failed for %s: %v", cluster, err)
							result.Failed = append(result.Failed, FeatureClusterBranch(conductorID, cluster))
						} else {
							mergedClusters[cluster] = true
						}
					}
				}

				// Check if we're done
				if c.isMergeQueueComplete(ctx, conductorID) {
					resultCh <- result
					return
				}
			}
		}
	}()

	return resultCh, nil
}

// isMergeQueueComplete returns true when all session tasks are terminal
// and no queue entries are pending/blocked/merging/refining.
func (c *Conductor) isMergeQueueComplete(ctx context.Context, conductorID string) bool {
	taskIDs := c.sessionTaskIDs(ctx)
	if len(taskIDs) == 0 {
		return true
	}

	// Check if any tasks are still active
	active, _ := c.DB.CountTasksByStatuses(ctx, []string{"pending", "assigned", "running"}, taskIDs)
	if active > 0 {
		return false
	}

	// G134: Check if any failed tasks have pending wait_until entries (rate_limit
	// or session_limit). These tasks are scheduled for retry by the monitor and
	// should not be considered terminal yet.
	if c.hasActiveWaiters(ctx, taskIDs) {
		return false
	}

	// Check if any queue entries are in-flight
	counts, err := c.DB.CountMergeQueueByStatus(ctx, conductorID)
	if err != nil {
		return false
	}

	inflightStatuses := []string{"pending", "blocked", "merging", "refining"}
	for _, s := range inflightStatuses {
		if counts[s] > 0 {
			return false
		}
	}

	return true
}

// checkContentClobber detects files that lost more than 50% of their lines after a merge.
// It logs a warning event but does not block the merge.
func (c *Conductor) checkContentClobber(ctx context.Context, taskID string) {
	// Get list of files modified by the merge commit
	diffOut, err := gitExec(c.RepoRoot, "diff", "--name-only", "HEAD~1", "HEAD")
	if err != nil || diffOut == "" {
		return
	}

	files := strings.Split(diffOut, "\n")
	for _, file := range files {
		file = strings.TrimSpace(file)
		if file == "" {
			continue
		}

		// Skip binary files
		if isBinaryFile(filepath.Join(c.RepoRoot, file)) {
			continue
		}

		// Get line count before merge (from parent commit)
		beforeOut, err := gitExec(c.RepoRoot, "show", "HEAD~1:"+file)
		if err != nil {
			continue // file didn't exist before merge
		}
		beforeLines := countLines(beforeOut)
		if beforeLines == 0 {
			continue // empty file, skip
		}

		// Get line count after merge (current HEAD)
		afterOut, err := gitExec(c.RepoRoot, "show", "HEAD:"+file)
		if err != nil {
			continue
		}
		afterLines := countLines(afterOut)

		// Check if reduction exceeds 50% (strictly greater than)
		reductionPct := float64(beforeLines-afterLines) / float64(beforeLines) * 100
		if reductionPct > 50 {
			c.log("WARN: content clobber detected in %s: %d → %d lines (%.1f%% reduction)", file, beforeLines, afterLines, reductionPct)
			c.DB.LogEvent(ctx, "merge_content_clobber", "", taskID,
				fmt.Sprintf(`{"file":%s,"before_lines":%d,"after_lines":%d,"reduction_pct":%.1f}`,
					mustJSON(file), beforeLines, afterLines, reductionPct))
		}
	}
}

// countLines returns the number of lines in the given string.
func countLines(s string) int {
	if s == "" {
		return 0
	}
	n := strings.Count(s, "\n")
	// If the string doesn't end with a newline, count the last line
	if !strings.HasSuffix(s, "\n") {
		n++
	}
	return n
}

// hasActiveWaiters checks if any session tasks have unexpired wait_until entries,
// indicating the monitor will retry them after a rate limit or session limit cooldown.
func (c *Conductor) hasActiveWaiters(ctx context.Context, taskIDs []string) bool {
	entries, err := c.DB.ListBlackboardByPrefix(ctx, "wait_until:")
	if err != nil {
		return false
	}

	taskSet := make(map[string]bool, len(taskIDs))
	for _, id := range taskIDs {
		taskSet[id] = true
	}

	for _, e := range entries {
		taskID := e.Key[len("wait_until:"):]
		if taskSet[taskID] {
			return true
		}
	}
	return false
}
