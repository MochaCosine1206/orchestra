package orchestrator

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime/debug"
	"strings"
	"time"

	"github.com/MochaCosine1206/orchestra/internal/agent"
	"github.com/MochaCosine1206/orchestra/internal/sandbox"
)

// GoOpts configures a full go (orchestrate goal) invocation.
type GoOpts struct {
	Goal            string
	TestCmd         string
	Review          bool
	DryRun          bool
	Iterative       bool
	MaxTasks        int
	MaxFilesPerTask int         // default: 25; 0 = unlimited
	MaxParallel     int
	Interval        int         // monitor poll interval in seconds
	ModelStrategy   string      // "all-opus", "per-role", "all-sonnet"
	Clarify         bool        // enable goal clarification before decomposition
	ClarifyMode     ClarifyMode // how to resolve ambiguity questions
	RepoMap         bool        // include compact repo map in task specs
	BaseBranch      string      // explicit base branch (empty = auto-detect)
	Reconcile       bool        // run post-session reconciliation (default true)
	LenientDeps     bool        // lenient dependency mode: partial predecessor output OK
	FileEnforcement string      // "", "defense", "pessimistic" — file ownership enforcement level
	TestFailureMode        string   // "revert_and_refine", "warn_only", "revert_no_refine"
	TestCmdTimeout         int      // seconds (default: 300)
	DisableActionExpansion bool
	ReadOnlyFiles          []string
	Runtime                string   // "local" (default) or "docker"
	MergeMode              string   // "local" (default) or "pr" (create GitHub PR)
	MergeStrategy          string   // "batch" (default) or "fifo" (B-280)
	Hierarchical           bool     // B-281: enable feature cluster decomposition + two-level merge
	Sandbox                bool     // enable Docker container sandboxing for agents
	PhaseID                string   // phase identifier for multi-phase GoSpec() (G110)
	KeepStaging            bool     // preserve staging branch after Go() for inter-phase chaining (G111)
	AtomicTasks            []string   // G-CSS-6: task titles that must not be split during decomposition
	SpecTasks              []SpecTask // Spec-defined tasks with roles to enforce post-decomposition
}

// GoResult captures the outcome of the full orchestration.
type GoResult struct {
	SessionID       string
	TaskCount       int
	DoneCount       int
	FailedCount     int
	RefiningCount   int // G-CSS-9: tasks that entered refinement
	AbandonedCount  int // G-CSS-9: tasks abandoned after max refinement rounds
	MergeResult     *MergeResult
	PRResult        *PRResult
	ReconcileResult *ReconcileResult
	StagingBranch   string // staging branch name (for cross-phase chaining, G111)
	Duration        time.Duration
}

// mergeOpts builds MergeOpts from GoOpts, converting the string/int fields to typed values.
func (o GoOpts) mergeOpts() MergeOpts {
	m := MergeOpts{
		TestCmd:         o.TestCmd,
		TestFailureMode: TestFailureMode(o.TestFailureMode),
		Review:          o.Review,
		BaseBranch:      o.BaseBranch,
		MergeMode:       MergeMode(o.MergeMode),
		PhaseID:         o.PhaseID,
	}
	if o.TestCmdTimeout > 0 {
		m.TestCmdTimeout = time.Duration(o.TestCmdTimeout) * time.Second
	}
	return m
}

// Go orchestrates the full workflow: decompose -> spawn -> wait -> review -> merge.
func (c *Conductor) Go(ctx context.Context, opts GoOpts) (result *GoResult, err error) {
	// G105: Panic recovery — safety net. If Go() panics during merge or any other phase,
	// catch it, log the stack trace, and update conductor status to "failed" so we don't
	// leave a stale "active" record in the DB.
	defer func() {
		if r := recover(); r != nil {
			c.log("PANIC in Go(): %v\n%s", r, debug.Stack())
			bgCtx := context.Background()
			cid := c.ConductorID
			if cid == "" {
				cid = c.SessionID
			}
			if cid != "" {
				c.DB.UpdateConductorStatus(bgCtx, cid, "failed")
			}
			err = fmt.Errorf("panic in Go(): %v", r)
		}
	}()

	start := time.Now()

	if opts.Goal == "" {
		return nil, fmt.Errorf("goal is required")
	}

	// Fail fast if not a git repository — agents need worktrees
	if !opts.DryRun && !IsGitRepo(c.RepoRoot) {
		return nil, fmt.Errorf("not a git repository: %s\nOrchestra requires a git repo for agent worktrees. Run 'git init && git add -A && git commit -m \"Initial commit\"' first", c.RepoRoot)
	}

	// Expand @file references in goal text (keep original for display)
	displayGoal := opts.Goal
	opts.Goal = expandFileReferences(opts.Goal, c.RepoRoot)

	// Store immutable goal for drift detection (auto mode).
	// SetBlackboardOnce ensures re-entries from auto cycles don't overwrite.
	if !opts.DryRun {
		goalKey := fmt.Sprintf("immutable_goal:%s", c.SessionID)
		c.DB.SetBlackboardOnce(ctx, goalKey, opts.Goal, "conductor")
	}

	// Check for orphan session to recover — but only if the new goal matches
	// the old session's goal. A new goal means the user wants a fresh start.
	if !opts.DryRun {
		recoverResult, err := c.Recover(ctx)
		if err != nil {
			return nil, fmt.Errorf("recovery check: %w", err)
		}
		if recoverResult != nil && recoverResult.Adopted && recoverResult.Goal != opts.Goal {
			// New goal != old session's goal — deactivate old session and start fresh
			c.log("New goal differs from recovered session %s — starting fresh", recoverResult.SessionID)
			c.progress("cleanup", "Cleaning up previous session for new goal")
			c.deactivateConductor(ctx)
		} else if recoverResult != nil && recoverResult.Adopted && recoverResult.Goal == opts.Goal {
			c.log("Recovered orphan session %s (%d done, %d failed, %d running, %d pending)",
				recoverResult.SessionID, recoverResult.DoneCount, recoverResult.FailedCount,
				recoverResult.RunningCount, recoverResult.PendingCount)
			c.progress("recover", fmt.Sprintf("Recovered session %s", recoverResult.SessionID))

			// Skip decompose/spawn — go straight to monitor+wait+merge
			result := &GoResult{
				SessionID: recoverResult.SessionID,
				TaskCount: len(recoverResult.TaskIDs),
			}

			if opts.Interval > 0 {
				c.Monitor.Interval = time.Duration(opts.Interval) * time.Second
			}
			if err := c.Monitor.Start(ctx); err != nil {
				c.log("Monitor start warning: %v", err)
			}
			defer c.Monitor.Stop()
			defer c.deactivateConductor(ctx)

			c.progress("wait", "Waiting for recovered tasks to complete")
			doneCount, failedCount, err := c.waitForTasks(ctx)
			if err != nil {
				c.log("Wait error: %v", err)
			}
			result.DoneCount = doneCount
			result.FailedCount = failedCount

			if doneCount > 0 {
				c.progress("merge", fmt.Sprintf("Merging %d completed branches", doneCount))
				mergeResult, err := c.Merge(ctx, opts.mergeOpts())
				if err != nil {
					c.log("Merge error: %v", err)
				}
				result.MergeResult = mergeResult

				// G105: Log staging merge failures as events for forensics
				c.progress("staging-merge", "Merging staging branch into base")
				if err := c.MergeStagingToDev(ctx); err != nil {
					c.log("CRITICAL: Staging-to-dev merge failed: %v", err)
					c.DB.LogEvent(ctx, "staging_merge_failed", "", "",
						fmt.Sprintf(`{"error":%s}`, mustJSON(truncate(err.Error(), 500))))
				}
				c.setBlackboard(ctx, "conductor:merge_complete", "true")
			}

			result.Duration = time.Since(start)
			c.progress("done", fmt.Sprintf("Recovered session completed in %s", result.Duration.Round(time.Second)))
			return result, nil
		}
	}

	// Docker preflight: verify daemon, image, and auth token before decomposing
	if opts.Runtime == "docker" {
		if err := agent.PreflightCheck(agent.DefaultDockerConfig()); err != nil {
			return nil, fmt.Errorf("docker preflight: %w", err)
		}
	}

	// Sandbox mode: instantiate sandbox and wire into spawner
	if opts.Sandbox {
		logsDir := filepath.Join(c.RepoRoot, ".orchestra", "logs")
		// Extract auth token from keychain for container injection
		authToken := os.Getenv("ANTHROPIC_API_KEY")
		if authToken == "" {
			// Try keychain extraction via helper script
			if out, err := exec.CommandContext(ctx, "bash", "-c",
				`security find-generic-password -s "Claude Code-credentials" -w 2>/dev/null | python3 -c "import sys,json; d=json.loads(sys.stdin.read()); print(d['claudeAiOauth']['accessToken'])" 2>/dev/null`,
			).Output(); err == nil {
				authToken = strings.TrimSpace(string(out))
			}
		}
		sb := sandbox.New(true, c.RepoRoot, logsDir, authToken)
		c.Spawner.Sandbox = sb
		defer func() {
			sb.CleanupAll(context.Background())
			c.Spawner.Sandbox = nil
		}()
		c.log("Sandbox mode enabled: agents will run in Docker containers")
	}

	// PR mode preflight: verify gh CLI is available and authenticated before decomposing.
	// Fail early rather than after 30+ minutes of agent work.
	if MergeMode(opts.MergeMode) == MergeModePR {
		if _, err := exec.LookPath("gh"); err != nil {
			return nil, fmt.Errorf("--merge-mode pr requires the GitHub CLI (gh): %w", err)
		}
		authCmd := exec.CommandContext(ctx, "gh", "auth", "status")
		authCmd.Dir = c.RepoRoot
		if authOut, authErr := authCmd.CombinedOutput(); authErr != nil {
			return nil, fmt.Errorf("--merge-mode pr requires gh auth: %s", truncate(string(authOut), 200))
		}
	}

	maxTasks := opts.MaxTasks
	if maxTasks <= 0 {
		maxTasks = 8
	}
	maxParallel := opts.MaxParallel
	if maxParallel <= 0 {
		maxParallel = 8
	}

	result = &GoResult{SessionID: c.SessionID}

	// Store PhaseID and KeepStaging on conductor for downstream use (decompose, merge)
	c.currentPhaseID = opts.PhaseID
	c.keepStaging = opts.KeepStaging

	c.log("Goal: %s", displayGoal)
	c.log("Session: %s", c.SessionID)

	// If iterative mode, delegate
	if opts.Iterative {
		return c.goIterative(ctx, opts)
	}

	// Step 1: Activate conductor
	c.progress("activate", "Initializing conductor session")
	if !opts.DryRun {
		pid := os.Getpid()
		if err := c.activateConductor(ctx, activateOpts{
			PID:           pid,
			MaxParallel:   maxParallel,
			TestCmd:       opts.TestCmd,
			MergeReview:   opts.Review,
			Goal:          opts.Goal,
			SessionID:     c.SessionID,
			ModelStrategy: opts.ModelStrategy,
			RepoMap:       opts.RepoMap,
			BaseBranch:    opts.BaseBranch,
			LenientDeps:   opts.LenientDeps,
			Runtime:       opts.Runtime,
			MergeMode:     opts.MergeMode,
			PhaseID:       opts.PhaseID, // B-287: store phase for cross-invocation lookup
		}); err != nil {
			return nil, fmt.Errorf("activating conductor: %w", err)
		}
		// B-287: Always deactivate conductor after phase completes.
		// Staging branch is preserved (not deleted) and staging→dev merge
		// ensures dev has all work. Prior G111 skipped deactivation to
		// preserve staging, but that's no longer needed.
		defer c.deactivateConductor(ctx)

		// Always enable auto-resolve — merge conflicts can occur with any task count.
		// Previously gated on defense mode (3+ tasks) but 2-task runs also conflict.
		c.setBlackboard(ctx, "conductor:auto_resolve_conflicts", "1")

		if opts.FileEnforcement != "" {
			c.setBlackboard(ctx, "conductor:file_enforcement", opts.FileEnforcement)
			c.setBlackboard(ctx, "conductor:file_watcher", "1")
		}
	}

	// Step 2: Decompose goal into tasks
	c.progress("decompose", fmt.Sprintf("Decomposing: %s", displayGoal))
	c.log("Decomposing goal...")
	decompResult, err := c.Decompose(ctx, DecomposeOpts{
		Goal:                   opts.Goal,
		MaxTasks:               maxTasks,
		MaxFilesPerTask:        opts.MaxFilesPerTask,
		DryRun:                 opts.DryRun,
		Clarify:                opts.Clarify,
		ClarifyMode:            opts.ClarifyMode,
		DisableActionExpansion: opts.DisableActionExpansion,
		ReadOnlyFiles:          opts.ReadOnlyFiles,
		Hierarchical:           opts.Hierarchical,
		AtomicTasks:            opts.AtomicTasks,
		SpecTasks:              opts.SpecTasks,
		OriginalGoal:           displayGoal,
	})
	if err != nil {
		return nil, fmt.Errorf("decompose: %w", err)
	}

	result.TaskCount = len(decompResult.Tasks)
	c.log("Created %d tasks", result.TaskCount)

	// Notify Telegram: decomposition complete
	{
		roles := make(map[string]int)
		for _, t := range decompResult.Tasks {
			roles[t.Role]++
		}
		var parts []string
		for role, count := range roles {
			parts = append(parts, fmt.Sprintf("%d %s", count, role))
		}
		c.notifyTelegram(fmt.Sprintf("<b>Orchestra:</b> Decomposed into %d tasks (%s)", result.TaskCount, strings.Join(parts, ", ")))
	}

	// Auto-enable defense mode when ≥3 tasks to prevent file ownership violations
	// (auto-resolve already enabled unconditionally at session start)
	if opts.FileEnforcement == "" && result.TaskCount >= 3 {
		opts.FileEnforcement = "defense"
		c.setBlackboard(ctx, "conductor:file_enforcement", "defense")
		c.setBlackboard(ctx, "conductor:file_watcher", "1")
		c.log("Auto-enabled defense mode: %d tasks (≥3 threshold)", result.TaskCount)
		c.DB.LogEvent(ctx, "defense_mode_auto_enabled", "", "",
			fmt.Sprintf(`{"task_count":%d,"trigger":"go_threshold"}`, result.TaskCount))
	}

	// B-280: Auto-enable FIFO merge strategy at 3+ tasks
	mergeStrategy := MergeStrategy(opts.MergeStrategy)
	if opts.MergeStrategy == "" && result.TaskCount >= 3 {
		mergeStrategy = MergeStrategyFIFO
		c.log("Auto-enabled FIFO merge strategy: %d tasks (≥3 threshold)", result.TaskCount)
	}

	// L-026: Auto-enable hierarchical decomposition at 3+ tasks with FIFO
	// A/B experiment (B-282): hierarchical is faster (186s vs 240s), zero merge conflicts,
	// higher pass rate (75% vs 57%). Falls back to flat if clustering degenerates.
	if !opts.Hierarchical && mergeStrategy == MergeStrategyFIFO && result.TaskCount >= 3 {
		opts.Hierarchical = true
		c.log("Auto-enabled hierarchical mode: %d tasks with FIFO strategy", result.TaskCount)
	}
	if mergeStrategy == MergeStrategyFIFO {
		conductorID := c.ConductorID
		if conductorID == "" {
			conductorID = c.SessionID
		}
		c.DB.SetMergeStrategy(ctx, conductorID, "fifo")
		c.setBlackboard(ctx, "conductor:merge_strategy", "fifo")
	}

	for _, t := range decompResult.Tasks {
		if t.PriorityLabel != "" {
			c.log("  %s [%s] (pri %d / %s) %s", t.ID, t.Role, t.Priority, t.PriorityLabel, t.Title)
		} else {
			c.log("  %s [%s] (pri %d) %s", t.ID, t.Role, t.Priority, t.Title)
		}
	}

	// Inject interface contracts from spec tasks into blackboard.
	// When spec tasks have Contracts, write the definition to blackboard
	// so buildContractSection in agent/specgen.go can inject them into task specs.
	if len(opts.SpecTasks) > 0 {
		for _, specTask := range opts.SpecTasks {
			if len(specTask.Contracts) == 0 {
				continue
			}
			// Find decomposed tasks that match this spec task (by title substring or file overlap)
			for _, dt := range decompResult.Tasks {
				if !strings.Contains(strings.ToLower(dt.Title), strings.ToLower(specTask.Title[:min(len(specTask.Title), 30)])) {
					continue
				}
				var contractText strings.Builder
				for _, contract := range specTask.Contracts {
					contractText.WriteString(fmt.Sprintf("### %s (%s)\n\n", contract.Name, contract.Role))
					if contract.Role == "producer" {
						contractText.WriteString("You MUST return data matching this type definition:\n\n")
					} else {
						contractText.WriteString("The other side of this API returns data matching this type:\n\n")
					}
					contractText.WriteString(fmt.Sprintf("```\n%s\n```\n\n", contract.Definition))
				}
				c.DB.SetBlackboard(ctx, "contract:"+dt.ID, contractText.String(), "conductor")
			}
		}
	}

	if opts.DryRun {
		c.log("Dry-run: would spawn %d agents, then merge", result.TaskCount)
		result.Duration = time.Since(start)
		return result, nil
	}

	// Step 3: Spawn agents for all unblocked tasks
	c.progress("spawn", fmt.Sprintf("Spawning agents for %d tasks", result.TaskCount))
	c.log("Spawning agents...")
	taskIDs := c.sessionTaskIDs(ctx)
	spawned, err := c.Spawner.Batch(ctx, maxParallel, 2*time.Second, taskIDs)
	if err != nil {
		c.log("Batch spawn error: %v", err)
	}
	c.log("Spawned %d agents", len(spawned))

	// Step 4: Start monitor for auto-spawn of blocked tasks
	if opts.Interval > 0 {
		c.Monitor.Interval = time.Duration(opts.Interval) * time.Second
	}
	if err := c.Monitor.Start(ctx); err != nil {
		c.log("Monitor start warning: %v", err)
	}
	defer c.Monitor.Stop()

	var doneCount, failedCount int

	// B-280: FIFO merge strategy — merge branches as tasks complete
	if mergeStrategy == MergeStrategyFIFO {
		// B-281: Use hierarchical queue when clusters are present
		var queueResultCh <-chan *MergeQueueResult
		var queueErr error
		if opts.Hierarchical {
			c.progress("fifo-queue", fmt.Sprintf("Hierarchical FIFO merge queue active for %d tasks", result.TaskCount))
			c.log("Starting hierarchical FIFO merge queue...")
			queueResultCh, queueErr = c.StartHierarchicalMergeQueue(ctx, opts.mergeOpts())
		} else {
			c.progress("fifo-queue", fmt.Sprintf("FIFO merge queue active for %d tasks", result.TaskCount))
			c.log("Starting FIFO merge queue...")
			queueResultCh, queueErr = c.StartMergeQueue(ctx, opts.mergeOpts())
		}
		if queueErr != nil {
			c.log("FIFO queue start error: %v", queueErr)
			// Fall back to batch mode
			goto batchMode
		}

		// Wait for queue to complete (blocks until all tasks terminal + queue drained)
		queueResult := <-queueResultCh

		// Collect final task counts
		taskIDs = c.sessionTaskIDs(ctx)
		doneCount, _ = c.DB.CountTasksByStatuses(ctx, []string{"done"}, taskIDs)
		failedCount, _ = c.DB.CountTasksByStatuses(ctx, []string{"failed"}, taskIDs)
		result.DoneCount = doneCount
		result.FailedCount = failedCount

		// Convert queue result to MergeResult
		if queueResult != nil {
			result.MergeResult = &MergeResult{
				Merged:       queueResult.Merged,
				Failed:       queueResult.Failed,
				Refining:     queueResult.Refining,
				AutoResolved: queueResult.AutoResolved,
				TestsFailed:  queueResult.TestsFailed,
			}
		}

		c.log("FIFO queue results: %d merged, %d failed, %d refining",
			len(queueResult.Merged), len(queueResult.Failed), len(queueResult.Refining))
		c.notifyTelegram(fmt.Sprintf("<b>Orchestra:</b> FIFO merge complete. %s",
			formatTaskSummary(doneCount, failedCount, result.TaskCount)))

		goto postMerge
	}

batchMode:
	// Step 5: Wait for all tasks to complete (batch mode)
	c.progress("wait", fmt.Sprintf("Waiting for %d tasks to complete", result.TaskCount))
	c.log("Waiting for tasks...")
	{
		var waitErr error
		doneCount, failedCount, waitErr = c.waitForTasks(ctx)
		if waitErr != nil {
			c.log("Wait error: %v", waitErr)
		}
	}
	result.DoneCount = doneCount
	result.FailedCount = failedCount

	c.log("Results: %d done, %d failed", doneCount, failedCount)
	c.notifyTelegram(fmt.Sprintf("<b>Orchestra:</b> All agents done. %s", formatTaskSummary(doneCount, failedCount, result.TaskCount)))

	// Step 6: Merge completed branches (batch mode)
	if doneCount > 0 {
		c.progress("merge", fmt.Sprintf("Merging %d completed branches", doneCount))
		c.log("Merging branches...")
		mergeResult, err := c.Merge(ctx, opts.mergeOpts())
		if err != nil {
			c.log("Merge error: %v", err)
		}
		result.MergeResult = mergeResult

		if mergeResult != nil {
			c.log("Merged: %d, Failed: %d, Skipped: %d, Refining: %d",
				len(mergeResult.Merged), len(mergeResult.Failed), len(mergeResult.Skipped), len(mergeResult.Refining))
		}

		// Step 6b: Bounded refinement loop — wait and re-merge refining branches.
		// G-CSS-1: Max 2 rounds. If still refining after that, abandon with warning.
		const maxRefinementRounds = 2
		if mergeResult != nil && len(mergeResult.Refining) > 0 {
			result.RefiningCount = len(mergeResult.Refining)
			for round := 1; round <= maxRefinementRounds; round++ {
				refiningCount := len(mergeResult.Refining)
				if refiningCount == 0 {
					break
				}
				c.log("Refinement round %d/%d: waiting for %d branches...", round, maxRefinementRounds, refiningCount)
				c.progress("refine", fmt.Sprintf("Refining %d branches (round %d/%d)", refiningCount, round, maxRefinementRounds))

				doneCount2, failedCount2, _ := c.waitForTasks(ctx)
				result.DoneCount = doneCount2
				result.FailedCount = failedCount2

				mergeResult2, mergeErr := c.Merge(ctx, opts.mergeOpts())
				if mergeErr != nil {
					c.log("Re-merge error (round %d): %v", round, mergeErr)
				}
				if mergeResult2 != nil {
					mergeResult.Merged = append(mergeResult.Merged, mergeResult2.Merged...)
					mergeResult.Failed = append(mergeResult.Failed, mergeResult2.Failed...)
					mergeResult.Refining = mergeResult2.Refining
				} else {
					mergeResult.Refining = nil
				}
			}

			// G-CSS-9: If branches are still refining after max rounds, abandon them
			if len(mergeResult.Refining) > 0 {
				result.AbandonedCount = len(mergeResult.Refining)
				c.log("WARNING: Abandoning %d branches still in refinement after %d rounds: %v",
					result.AbandonedCount, maxRefinementRounds, mergeResult.Refining)
				c.DB.LogEvent(ctx, "refinement_abandoned", "", "",
					fmt.Sprintf(`{"abandoned_count":%d,"branches":%s}`, result.AbandonedCount, mustJSON(mergeResult.Refining)))
			}
		}
	}

postMerge:

	// Capture staging branch in result for cross-phase chaining (G111)
	if !opts.DryRun {
		conductorID := c.ConductorID
		if conductorID == "" {
			conductorID = c.SessionID
		}
		if rec, err := c.DB.GetConductor(ctx, conductorID); err == nil && rec != nil {
			result.StagingBranch = rec.StagingBranch
		}
	}

	// Merge staging branch into dev (Phase 3: per-conductor staging)
	// B-287: Always merge staging to dev after every phase (not just final).
	// This ensures --start-phase can fork from dev with all prior phase work.
	// The staging branch is preserved (not deleted) as a record.
	if doneCount > 0 {
		if MergeMode(opts.MergeMode) == MergeModePR && !opts.KeepStaging {
			// PR mode: create a GitHub PR from the staging branch instead of local merge.
			// Only create PRs for the final phase — intermediate phases merge locally.
			c.progress("create-pr", "Creating PR from staging branch")
			prResult, prErr := c.CreateStagingPR(ctx)
			if prErr != nil {
				c.log("CRITICAL: PR creation failed: %v", prErr)
				c.DB.LogEvent(ctx, "staging_pr_failed", "", "",
					fmt.Sprintf(`{"error":%s}`, mustJSON(truncate(prErr.Error(), 500))))
			}
			if prResult != nil {
				result.PRResult = prResult
			}
			// Skip git reset --hard — working tree is unchanged
		} else {
			// Local mode: merge staging into base via update-ref + reset
			// G105: Log staging merge failures as events for forensics
			c.progress("staging-merge", "Merging staging branch into base")
			if err := c.MergeStagingToDev(ctx); err != nil {
				c.log("CRITICAL: Staging-to-dev merge failed: %v", err)
				c.DB.LogEvent(ctx, "staging_merge_failed", "", "",
					fmt.Sprintf(`{"error":%s}`, mustJSON(truncate(err.Error(), 500))))
			}
			// G107: MergeStagingToDev uses update-ref to advance the base branch ref
			// without touching the working tree or index. The Merge() defer already
			// restored us to the base branch, but at the OLD ref. Now the ref has
			// advanced, so the index/working tree are stale. Reset to sync them.
			if _, resetErr := gitExec(c.RepoRoot, "reset", "--hard", "HEAD"); resetErr != nil {
				c.log("WARNING: git reset --hard after staging merge failed: %v", resetErr)
			}
		}
	}

	// G96: Signal merge complete — Telegram hook gates push buttons on this.
	c.setBlackboard(ctx, "conductor:merge_complete", "true")

	// Step 7: Post-session reconciliation
	if opts.Reconcile {
		c.progress("reconcile", "Running post-session reconciliation")
		reconcileResult, reconcileErr := c.Reconcile(ctx, ReconcileOpts{})
		if reconcileErr != nil {
			c.log("Reconcile warning: %v", reconcileErr)
		} else {
			result.ReconcileResult = reconcileResult
		}
	}

	result.Duration = time.Since(start)
	c.progress("done", fmt.Sprintf("Completed in %s", result.Duration.Round(time.Second)))
	c.log("Completed in %s", result.Duration.Round(time.Second))

	// Notify Telegram: orchestration complete
	{
		mergeInfo := ""
		if result.MergeResult != nil {
			mergeInfo = fmt.Sprintf(", %d merged", len(result.MergeResult.Merged))
		}
		prInfo := ""
		if result.PRResult != nil {
			prInfo = fmt.Sprintf("\n<a href=\"%s\">Review PR</a>", result.PRResult.PRURL)
		}
		c.notifyTelegram(fmt.Sprintf("<b>Orchestra:</b> Complete — %s%s (%s)%s",
			formatTaskSummary(doneCount, failedCount, result.TaskCount),
			mergeInfo,
			result.Duration.Round(time.Second),
			prInfo))
	}

	c.DB.LogEvent(ctx, "conductor_completed", "", "", fmt.Sprintf(
		`{"session":"%s","done":%d,"failed":%d,"duration_s":%.0f}`,
		c.SessionID, doneCount, failedCount, result.Duration.Seconds()))

	return result, nil
}
