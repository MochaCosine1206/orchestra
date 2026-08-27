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
	"time"

	"github.com/MochaCosine1206/orchestra/internal/agent"
	"github.com/MochaCosine1206/orchestra/internal/db"
)

const (
	defaultPollInterval = 5 * time.Second
	defaultMaxWait      = 6 * time.Hour
)

// waitForTasks polls the DB until all session tasks reach a terminal state (done or failed).
// Returns counts of done and failed tasks.
func (c *Conductor) waitForTasks(ctx context.Context) (done int, failed int, err error) {
	taskIDs := c.sessionTaskIDs(ctx)
	if len(taskIDs) == 0 {
		return 0, 0, nil
	}

	deadline := time.Now().Add(defaultMaxWait)
	for {
		select {
		case <-ctx.Done():
			return done, failed, ctx.Err()
		default:
		}

		if time.Now().After(deadline) {
			return done, failed, fmt.Errorf("timed out waiting for tasks after %s", defaultMaxWait)
		}

		doneCount, err := c.DB.CountTasksByStatuses(ctx, []string{"done"}, taskIDs)
		if err != nil {
			return 0, 0, fmt.Errorf("counting done tasks: %w", err)
		}
		failedCount, err := c.DB.CountTasksByStatuses(ctx, []string{"failed"}, taskIDs)
		if err != nil {
			return 0, 0, fmt.Errorf("counting failed tasks: %w", err)
		}

		total := doneCount + failedCount
		if total >= len(taskIDs) {
			// G134: Don't exit if failed tasks have pending wait_until entries
			// (rate_limit or session_limit). The monitor will retry them.
			if failedCount > 0 && c.hasActiveWaiters(ctx, taskIDs) {
				time.Sleep(defaultPollInterval)
				continue
			}
			return doneCount, failedCount, nil
		}

		// Detect stuck state: no running agents but pending tasks remain with failures.
		// The monitor's cascade phase should resolve this within one cycle, so we wait
		// one extra interval and re-check before giving up.
		if failedCount > 0 {
			runningCount, _ := c.DB.CountTasksByStatuses(ctx, []string{"assigned", "running"}, taskIDs)
			pendingCount, _ := c.DB.CountTasksByStatuses(ctx, []string{"pending"}, taskIDs)
			if runningCount == 0 && pendingCount > 0 {
				// Wait one more poll interval for cascade to propagate
				time.Sleep(defaultPollInterval)
				doneCount2, _ := c.DB.CountTasksByStatuses(ctx, []string{"done"}, taskIDs)
				failedCount2, _ := c.DB.CountTasksByStatuses(ctx, []string{"failed"}, taskIDs)
				if doneCount2+failedCount2 >= len(taskIDs) {
					return doneCount2, failedCount2, nil
				}
				runningCount2, _ := c.DB.CountTasksByStatuses(ctx, []string{"assigned", "running"}, taskIDs)
				if runningCount2 == 0 {
					return doneCount2, failedCount2, fmt.Errorf("stuck: %d pending tasks with no running agents and %d failures", pendingCount, failedCount)
				}
			}
		}

		time.Sleep(defaultPollInterval)
	}
}

// sessionTaskIDs returns the task IDs associated with the current conductor session.
// Tries conductor_id column first, falls back to blackboard for backward compat.
func (c *Conductor) sessionTaskIDs(ctx context.Context) []string {
	if c.ConductorID != "" {
		ids, err := c.DB.ListTaskIDsByConductor(ctx, c.ConductorID)
		if err == nil && len(ids) > 0 {
			return ids
		}
	}
	// Fallback to blackboard for sessions created before Phase 4
	raw, _ := c.DB.GetBlackboardValue(ctx, "conductor:task_ids")
	return parseTaskIDsJSON(raw)
}

// parseTaskIDsJSON parses a JSON array of strings like `["T-001","T-002"]`.
func parseTaskIDsJSON(raw string) []string {
	if raw == "" || raw == "[]" {
		return nil
	}
	var ids []string
	if err := json.Unmarshal([]byte(raw), &ids); err != nil {
		// Fallback: manual parse
		raw = strings.Trim(raw, "[]")
		if raw == "" {
			return nil
		}
		for _, part := range strings.Split(raw, ",") {
			id := strings.Trim(strings.TrimSpace(part), `"`)
			if id != "" {
				ids = append(ids, id)
			}
		}
	}
	return ids
}

// setBlackboard is a convenience wrapper for setting a blackboard key with "conductor" as writer.
func (c *Conductor) setBlackboard(ctx context.Context, key, value string) error {
	return c.DB.SetBlackboard(ctx, key, value, "conductor")
}

// getBlackboard is a convenience wrapper for getting a blackboard value.
func (c *Conductor) getBlackboard(ctx context.Context, key string) string {
	val, _ := c.DB.GetBlackboardValue(ctx, key)
	return val
}

// storeSessionTaskIDs stores task IDs as a JSON array in the blackboard.
func (c *Conductor) storeSessionTaskIDs(ctx context.Context, ids []string) error {
	data, err := json.Marshal(ids)
	if err != nil {
		return fmt.Errorf("marshaling task IDs: %w", err)
	}
	return c.setBlackboard(ctx, "conductor:task_ids", string(data))
}

// activateConductor registers the conductor in the conductors table and sets
// backward-compatible blackboard keys for the monitor and TUI.
func (c *Conductor) activateConductor(ctx context.Context, opts activateOpts) error {
	// G103: prevent double-activation when cascade escalation calls Go() again
	if c.conductorActive {
		c.log("Conductor already active (session=%s), skipping re-activation", c.ConductorID)
		return nil
	}

	// Resolve base branch: explicit flag > auto-detect from repo
	baseBranch := opts.BaseBranch
	if baseBranch == "" {
		baseBranch = agent.DetectBaseBranch(c.RepoRoot)
	}

	// Build staging branch name
	stagingBranch := fmt.Sprintf("conductor/%s", opts.SessionID)

	// Create staging branch from base (fail-open: branch may already exist)
	c.createStagingBranch(stagingBranch, baseBranch)

	// Store conductor ID on the Conductor struct for downstream use
	c.ConductorID = opts.SessionID

	// Create conductor record in the conductors table
	mergeMode := opts.MergeMode
	if mergeMode == "" {
		mergeMode = "local"
	}
	rec := db.ConductorRecord{
		ID:            opts.SessionID,
		PID:           opts.PID,
		Goal:          opts.Goal,
		Status:        "active",
		StagingBranch: stagingBranch,
		BaseBranch:    baseBranch,
		MaxParallel:   opts.MaxParallel,
		TestCmd:       sql.NullString{Valid: opts.TestCmd != "", String: opts.TestCmd},
		MergeReview:   opts.MergeReview,
		ModelStrategy: opts.ModelStrategy,
		Runtime:       opts.Runtime,
		RepoMap:       opts.RepoMap,
		LenientDeps:   opts.LenientDeps,
		MergeMode:     mergeMode,
		PhaseID:       opts.PhaseID, // B-287: store phase for cross-invocation lookup
	}
	if rec.ModelStrategy == "" {
		rec.ModelStrategy = "all-opus"
	}
	if rec.Runtime == "" {
		rec.Runtime = "local"
	}
	if err := c.DB.CreateConductor(ctx, rec); err != nil {
		return fmt.Errorf("creating conductor record: %w", err)
	}
	c.conductorActive = true // G103: mark active after successful DB insert

	// Backward-compatible blackboard writes — monitor and TUI still read these.
	// These will be removed in Phase 5 after monitor/TUI migration.
	c.setBlackboard(ctx, "conductor:active", "1")
	c.setBlackboard(ctx, "conductor:pid", fmt.Sprintf("%d", opts.PID))
	if opts.MaxParallel > 0 {
		c.setBlackboard(ctx, "conductor:max_parallel", fmt.Sprintf("%d", opts.MaxParallel))
	}
	if opts.TestCmd != "" {
		c.setBlackboard(ctx, "conductor:test_cmd", opts.TestCmd)
	}
	if opts.MergeReview {
		c.setBlackboard(ctx, "conductor:merge_review", "1")
	}
	if opts.Goal != "" {
		c.setBlackboard(ctx, "conductor:goal", opts.Goal)
	}
	if opts.SessionID != "" {
		c.setBlackboard(ctx, "conductor:session_id", opts.SessionID)
	}
	if opts.ModelStrategy != "" {
		c.setBlackboard(ctx, "conductor:model_strategy", opts.ModelStrategy)
	}
	if opts.RepoMap {
		c.setBlackboard(ctx, "conductor:repo_map", "1")
	}
	c.setBlackboard(ctx, "base_branch", baseBranch)
	if opts.LenientDeps {
		c.setBlackboard(ctx, "conductor:lenient_deps", "1")
	}
	if opts.Runtime != "" {
		c.setBlackboard(ctx, "conductor:runtime", opts.Runtime)
	}
	if mergeMode != "local" {
		c.setBlackboard(ctx, "conductor:merge_mode", mergeMode)
	}
	return nil
}

// createStagingBranch creates the staging branch from base. Best-effort: if the branch
// already exists (e.g. from a recovered session), we skip creation.
func (c *Conductor) createStagingBranch(stagingBranch, baseBranch string) {
	cmd := exec.Command("git", "branch", stagingBranch, baseBranch)
	cmd.Dir = c.RepoRoot
	cmd.CombinedOutput() // ignore error if branch exists
}

type activateOpts struct {
	PID           int
	MaxParallel   int
	TestCmd       string
	MergeReview   bool
	Goal          string
	SessionID     string
	ModelStrategy string
	RepoMap       bool
	BaseBranch    string
	LenientDeps  bool
	Runtime       string
	MergeMode     string
	PhaseID       string // B-287: phase identifier for cross-invocation chaining
}

// gitRefExists returns true if the given git ref exists in the repo.
func gitRefExists(repoRoot, ref string) bool {
	cmd := exec.CommandContext(context.Background(), "git", "rev-parse", "--verify", ref)
	cmd.Dir = repoRoot
	return cmd.Run() == nil
}

// Deactivate is the public interface for cleaning up conductor state.
func (c *Conductor) Deactivate(ctx context.Context) {
	c.deactivateConductor(ctx)
}

// deactivateConductor cleans up conductor state: updates the conductors table,
// removes the staging branch, cleans worktrees, and removes blackboard keys.
// G105: Uses context.Background() for all DB operations (caller's ctx may be cancelled)
// and includes panic recovery to ensure partial cleanup doesn't crash the process.
func (c *Conductor) deactivateConductor(ctx context.Context) {
	defer func() {
		if r := recover(); r != nil {
			c.log("PANIC in deactivateConductor: %v", r)
		}
	}()

	c.conductorActive = false // G103: allow re-activation if needed

	// G105: Use background context for all DB operations — the caller's context
	// may be cancelled (e.g. from signal handling), which would silently fail all writes.
	bgCtx := context.Background()

	// Kill all Docker containers if running in Docker mode
	runtime, _ := c.DB.GetBlackboardValue(bgCtx, "conductor:runtime")
	if runtime == "docker" {
		agent.KillAllOrchestraContainers()
	}

	// Clean up worktrees for all terminal tasks before deactivating
	c.cleanupSessionWorktrees(bgCtx)

	// Update conductor record to terminal status
	conductorID := c.ConductorID
	if conductorID == "" {
		conductorID = c.SessionID
	}
	if conductorID != "" {
		// Determine terminal status based on task outcomes
		taskIDs := c.sessionTaskIDs(bgCtx)
		failedCount, _ := c.DB.CountTasksByStatuses(bgCtx, []string{"failed"}, taskIDs)
		status := "completed"
		if failedCount > 0 {
			status = "failed"
		}
		// G105: Error check + retry on status update — failure leaves conductor "active" forever
		if err := c.DB.UpdateConductorStatus(bgCtx, conductorID, status); err != nil {
			c.log("WARNING: UpdateConductorStatus failed: %v — retrying", err)
			time.Sleep(500 * time.Millisecond)
			if err2 := c.DB.UpdateConductorStatus(bgCtx, conductorID, status); err2 != nil {
				c.log("CRITICAL: UpdateConductorStatus retry failed: %v", err2)
			}
		}
	}

	// Remove staging branch (best-effort, after merges are done).
	// Skip deletion in PR mode — the PR references the staging branch;
	// GitHub handles cleanup after the PR is merged.
	// G111: Skip deletion when keepStaging is true — next phase needs this branch.
	// G106: Must checkout base branch first — can't delete a branch you're on.
	if conductorID != "" {
		rec, _ := c.DB.GetConductor(bgCtx, conductorID)
		if rec != nil && rec.StagingBranch != "" && rec.MergeMode != "pr" && !c.keepStaging {
			// If we're still on the staging branch, switch to base first
			currentBranch, _ := exec.Command("git", "-C", c.RepoRoot, "rev-parse", "--abbrev-ref", "HEAD").Output()
			if strings.TrimSpace(string(currentBranch)) == rec.StagingBranch {
				baseBranch := rec.BaseBranch
				if baseBranch == "" {
					baseBranch = "dev"
				}
				cmd := exec.Command("git", "checkout", baseBranch)
				cmd.Dir = c.RepoRoot
				cmd.CombinedOutput() // best-effort
			}
			cmd := exec.Command("git", "branch", "-D", rec.StagingBranch)
			cmd.Dir = c.RepoRoot
			cmd.CombinedOutput() // best-effort
		}
	}

	// Remove backward-compat blackboard keys
	for _, key := range []string{
		"conductor:active",
		"conductor:pid",
		"conductor:test_cmd",
		"conductor:merge_review",
		"conductor:max_parallel",
		"conductor:goal",
		"conductor:session_id",
		"conductor:model_strategy",
		"base_branch",
		"conductor:repo_map",
		"conductor:lenient_deps",
		"conductor:merge_complete",
		"conductor:runtime",
		"conductor:merge_mode",
	} {
		c.DB.DeleteBlackboard(bgCtx, key)
	}
}

// cleanupSessionWorktrees removes worktrees and branches for all terminal tasks
// in the current session. Mirrors monitor.cleanupSessionWorktrees for conductor shutdown.
func (c *Conductor) cleanupSessionWorktrees(ctx context.Context) {
	taskIDs := c.sessionTaskIDs(ctx)
	sessionSet := make(map[string]bool)
	for _, id := range taskIDs {
		sessionSet[id] = true
	}

	tasks, err := c.DB.ListTasks(ctx)
	if err != nil {
		return
	}

	for _, t := range tasks {
		if t.Status != "done" && t.Status != "failed" {
			continue
		}
		if !t.Worktree.Valid {
			continue
		}
		if len(sessionSet) > 0 && !sessionSet[t.ID] {
			continue
		}
		wt := t.Worktree.String
		if _, err := os.Stat(wt); err == nil {
			// Salvage uncommitted changes before removal (failed tasks may have partial work)
			if t.Status == "failed" {
				agent.SalvageWorktreeChanges(ctx, wt, t.ID)
			}
			cmd := exec.Command("git", "worktree", "remove", "--force", wt)
			cmd.Dir = c.RepoRoot
			cmd.CombinedOutput()
		}
		if t.Branch.Valid {
			cmd := exec.Command("git", "branch", "-D", t.Branch.String)
			cmd.Dir = c.RepoRoot
			cmd.CombinedOutput()
		}
	}
}

// cleanupTaskWorktree removes a stale worktree and branch for a single task.
// Used by gate retry to clean up artifacts from a failed attempt before the
// spawner creates a fresh worktree. Salvages uncommitted work first.
func (c *Conductor) cleanupTaskWorktree(ctx context.Context, taskID string) {
	worktreePath := filepath.Join(c.RepoRoot, ".worktree", taskID)
	branch := "feature/" + taskID

	// Salvage any uncommitted work before destroying
	agent.SalvageWorktreeChanges(ctx, worktreePath, taskID)

	// Remove worktree (best-effort)
	rmCmd := exec.CommandContext(ctx, "git", "worktree", "remove", worktreePath, "--force")
	rmCmd.Dir = c.RepoRoot
	rmCmd.CombinedOutput()

	// Delete branch (best-effort)
	delCmd := exec.CommandContext(ctx, "git", "branch", "-D", branch)
	delCmd.Dir = c.RepoRoot
	delCmd.CombinedOutput()
}
