package orchestrator

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// GoRunner executes a single phase. In production this calls c.Go();
// in tests it can be mocked.
type GoRunner interface {
	RunPhase(ctx context.Context, opts GoOpts) (*GoResult, error)
}

// GateRetrier is an optional interface that GoRunner implementations can
// satisfy to support retrying failed tasks when a phase gate fails.
// G136: When a gate fails and there are retryable failed tasks (stuck_killed,
// rate_limit, session_limit), reset them to pending and re-run Go().
type GateRetrier interface {
	RetryFailedTasks(ctx context.Context, sessionID string, opts GoOpts) (*GoResult, error)
}

// maxGateRetries is the maximum number of gate retry attempts per phase.
const maxGateRetries = 2

// retryableFailureTypes lists failure types that are safe to retry at the gate level.
// Normal failures (code errors past circuit breaker) are not retryable.
var retryableFailureTypes = map[string]bool{
	"stuck_killed":      true,
	"rate_limit":        true,
	"session_limit":     true,
	"context_exhausted": true,
}

// conductorGoRunner wraps a Conductor to implement GoRunner and GateRetrier.
type conductorGoRunner struct {
	c *Conductor
}

func (r *conductorGoRunner) RunPhase(ctx context.Context, opts GoOpts) (*GoResult, error) {
	return r.c.Go(ctx, opts)
}

// RetryFailedTasks resets retryable failed tasks to pending and spawns them
// directly, skipping decomposition. The original Go() call already created the
// tasks — re-calling Go() would re-decompose the goal, creating duplicates and
// leaving stale worktrees/branches that block the spawner with "branch already
// exists" errors.
func (r *conductorGoRunner) RetryFailedTasks(ctx context.Context, sessionID string, opts GoOpts) (*GoResult, error) {
	// Get session task IDs from blackboard
	taskIDs := r.c.sessionTaskIDs(ctx)
	if len(taskIDs) == 0 {
		return nil, fmt.Errorf("no session task IDs found for retry")
	}

	var resetTaskIDs []string
	for _, taskID := range taskIDs {
		task, err := r.c.DB.GetTaskByID(ctx, taskID)
		if err != nil || task == nil || task.Status != "failed" {
			continue
		}

		// Check failure type — only retry infrastructure/transient failures
		failureType := r.c.getBlackboard(ctx, "failure_type:"+taskID)
		if !retryableFailureTypes[failureType] {
			r.c.log("Gate retry: skipping task %s (failure_type=%s, not retryable)", taskID, failureType)
			continue
		}

		// Reset task to pending
		if err := r.c.DB.ResetTask(ctx, taskID); err != nil {
			r.c.log("Gate retry: failed to reset task %s: %v", taskID, err)
			continue
		}

		// Clear stuck_killed marker if present
		r.c.setBlackboard(ctx, "stuck_killed:"+taskID, "")

		r.c.log("Gate retry: reset task %s (was %s) to pending", taskID, failureType)
		resetTaskIDs = append(resetTaskIDs, taskID)
	}

	if len(resetTaskIDs) == 0 {
		return nil, fmt.Errorf("no retryable failed tasks found")
	}

	// Clean up stale worktrees and branches for reset tasks so spawner can
	// create fresh ones without "branch already exists" errors.
	for _, taskID := range resetTaskIDs {
		r.c.cleanupTaskWorktree(ctx, taskID)
	}
	r.c.log("Gate retry: cleaned up %d stale worktrees, spawning directly (skip decomposition)", len(resetTaskIDs))

	// Build result from existing session (no new decomposition)
	result := &GoResult{
		SessionID: sessionID,
		TaskCount: len(taskIDs),
	}

	// Start monitor (auto-spawns pending tasks when dependencies resolve)
	if opts.Interval > 0 {
		r.c.Monitor.Interval = time.Duration(opts.Interval) * time.Second
	}
	if err := r.c.Monitor.Start(ctx); err != nil {
		r.c.log("Gate retry: monitor start warning: %v", err)
	}
	defer r.c.Monitor.Stop()

	// Batch spawn unblocked pending tasks immediately
	maxParallel := opts.MaxParallel
	if maxParallel <= 0 {
		maxParallel = 8
	}
	spawned, err := r.c.Spawner.Batch(ctx, maxParallel, 2*time.Second, taskIDs)
	if err != nil {
		r.c.log("Gate retry: batch spawn error: %v", err)
	}
	r.c.log("Gate retry: spawned %d agents", len(spawned))

	// Wait for all tasks to reach terminal state
	doneCount, failedCount, waitErr := r.c.waitForTasks(ctx)
	if waitErr != nil {
		r.c.log("Gate retry: wait error: %v", waitErr)
	}
	result.DoneCount = doneCount
	result.FailedCount = failedCount

	// Merge completed branches
	if doneCount > 0 {
		mergeResult, mergeErr := r.c.Merge(ctx, opts.mergeOpts())
		if mergeErr != nil {
			r.c.log("Gate retry: merge error: %v", mergeErr)
		}
		result.MergeResult = mergeResult
	}

	// Capture staging branch for cross-phase chaining (G111)
	conductorID := r.c.ConductorID
	if conductorID == "" {
		conductorID = r.c.SessionID
	}
	if rec, err := r.c.DB.GetConductor(ctx, conductorID); err == nil && rec != nil {
		result.StagingBranch = rec.StagingBranch
	}

	return result, nil
}

// GoSpecOpts holds global defaults for all phases. These come from CLI flags
// or user preference and apply to every phase unless overridden.
type GoSpecOpts struct {
	DryRun      bool   // show plan without executing
	MaxParallel int    // max concurrent agents per phase (default 8)
	Interval    int    // monitor poll interval in seconds
	Review      bool   // enable review gates
	Reconcile   bool   // run reconciliation (only after final phase)
	RepoMap     bool   // include repo map in task specs
	BaseBranch  string // base branch (auto-detect if empty)
	Runtime     string // "local" or "docker"
	MergeMode   string // "local" or "pr"
	StartPhase  string // resume from this phase ID (skip prior phases)
	RepoRoot    string // G114: target project root (for gate tests + git ops)
	Force       bool   // G-CSS-2: skip start-phase gate validation
	Sandbox     bool   // enable Docker container sandboxing for agents
}

// GoSpecResult holds aggregated results across all phases.
type GoSpecResult struct {
	SpecTitle    string
	PhaseResults []PhaseResult
	TotalDone    int
	TotalFailed  int
	Duration     time.Duration
}

// PhaseResult holds the outcome of a single phase execution.
type PhaseResult struct {
	PhaseID    string
	PhaseName  string
	GoResult   *GoResult // result from Go() call (nil if skipped)
	GatePassed bool
	GateOutput string // test command output (for diagnostics)
	Skipped    bool   // true if phase was skipped (dry run or --start-phase)
	Duration   time.Duration
}

// GoSpec executes a multi-phase orchestration spec. It loops through phases
// in topological order, calling Go() per phase via the GoRunner, and gating
// advancement on test commands.
func GoSpec(ctx context.Context, spec *OrchestraSpec, opts GoSpecOpts, runner GoRunner) (*GoSpecResult, error) {
	start := time.Now()

	// 1. Validate spec (fail fast on errors, log warnings)
	validation := ValidateSpec(spec)
	if !validation.IsValid() {
		return nil, fmt.Errorf("spec validation failed: %s", validation.Errors[0].Message)
	}

	// 2. TopoSortPhases → get execution order
	order, err := TopoSortPhases(spec.Phases)
	if err != nil {
		return nil, fmt.Errorf("topological sort failed: %w", err)
	}
	if len(order) == 0 {
		return nil, fmt.Errorf("spec has no phases to execute")
	}

	// Build phase lookup map
	phaseMap := make(map[string]Phase, len(spec.Phases))
	for _, p := range spec.Phases {
		phaseMap[p.ID] = p
	}

	// 3. If StartPhase set, validate it exists
	startReached := opts.StartPhase == ""
	if opts.StartPhase != "" {
		if _, exists := phaseMap[opts.StartPhase]; !exists {
			return nil, fmt.Errorf("start phase %q not found in spec", opts.StartPhase)
		}
	}

	result := &GoSpecResult{
		SpecTitle: spec.Metadata.Title,
	}

	// G111: Rolling base — each phase forks from the prior phase's staging branch.
	// Starts as the user-specified base branch (or auto-detected), then advances
	// to the staging branch of each completed phase.
	rollingBase := opts.BaseBranch

	// 4. For each phase in topological order
	for i, phaseID := range order {
		if err := ctx.Err(); err != nil {
			return result, fmt.Errorf("context cancelled: %w", err)
		}

		phase := phaseMap[phaseID]
		isFinal := i == len(order)-1

		pr := PhaseResult{
			PhaseID:   phase.ID,
			PhaseName: phase.Name,
		}

		// Handle start-phase skipping
		if !startReached {
			if phaseID == opts.StartPhase {
				// G-CSS-2: Before resuming, validate the last prior phase's gate if set.
				// This catches cases where the prior phase's work is incomplete or broken.
				if i > 0 && !opts.Force {
					lastPriorID := order[i-1]
					lastPrior := phaseMap[lastPriorID]
					if lastPrior.Gate.TestCmd != "" {
						gateRepoRoot := opts.RepoRoot
						if gateRepoRoot == "" {
							gateRepoRoot = "."
						}
						passed, gateOut, gateErr := checkPhaseGate(gateRepoRoot, lastPrior)
						if gateErr != nil {
							return result, fmt.Errorf("start-phase gate validation error for prior phase %q: %w", lastPriorID, gateErr)
						}
						if !passed {
							return result, fmt.Errorf("start-phase gate validation failed for prior phase %q: %s\nUse --force to skip this check", lastPriorID, gateOut)
						}
					}
				}
				startReached = true
			} else {
				pr.Skipped = true
				pr.GatePassed = true // assume prior phases passed
				result.PhaseResults = append(result.PhaseResults, pr)
				continue
			}
		}

		// Handle dry run
		if opts.DryRun {
			pr.Skipped = true
			pr.GatePassed = true
			result.PhaseResults = append(result.PhaseResults, pr)
			continue
		}

		// Build GoOpts for this phase
		goOpts := buildPhaseGoOpts(phase, spec.Metadata, opts, isFinal)

		// G111: Override base branch with rolling base from prior phase
		if rollingBase != "" {
			goOpts.BaseBranch = rollingBase
		}

		// Execute phase
		phaseStart := time.Now()
		goResult, goErr := runner.RunPhase(ctx, goOpts)
		pr.Duration = time.Since(phaseStart)

		if goErr != nil {
			pr.GoResult = goResult
			result.PhaseResults = append(result.PhaseResults, pr)
			result.Duration = time.Since(start)
			aggregateResults(result)
			return result, fmt.Errorf("phase %q execution failed: %w", phaseID, goErr)
		}

		pr.GoResult = goResult

		// G114: Resolve repo root for gate tests and git ops.
		// ConductorGoSpec sets opts.RepoRoot from c.RepoRoot; fall back to "."
		// for direct GoSpec callers (e.g. tests with mock runners).
		gateRepoRoot := opts.RepoRoot
		if gateRepoRoot == "" {
			gateRepoRoot = "."
		}

		// G111: Checkout the staging branch before running the gate test.
		// After Go() returns, the repo is on the original branch (G106 restore).
		// The merged code lives on the staging branch — gate must test against it.
		var gateRestoreBranch string
		if goResult != nil && goResult.StagingBranch != "" {
			origOut, _ := exec.Command("git", "-C", gateRepoRoot, "rev-parse", "--abbrev-ref", "HEAD").Output()
			gateRestoreBranch = strings.TrimSpace(string(origOut))
			exec.Command("git", "-C", gateRepoRoot, "checkout", goResult.StagingBranch).Run()
		}

		// Check gate
		passed, output, gateErr := checkPhaseGate(gateRepoRoot, phase)

		// Restore original branch after gate check (not defer — we're in a loop)
		if gateRestoreBranch != "" && gateRestoreBranch != goResult.StagingBranch {
			exec.Command("git", "-C", gateRepoRoot, "checkout", gateRestoreBranch).Run()
		}
		pr.GatePassed = passed
		pr.GateOutput = output

		result.PhaseResults = append(result.PhaseResults, pr)

		if gateErr != nil {
			result.Duration = time.Since(start)
			aggregateResults(result)
			return result, fmt.Errorf("phase %q gate error: %w", phaseID, gateErr)
		}

		// G136: Gate retry — if gate failed and runner supports retries,
		// reset retryable failed tasks and re-run Go() + gate check.
		if !passed {
			retrier, canRetry := runner.(GateRetrier)
			retried := false

			if canRetry && goResult != nil && goResult.FailedCount > 0 {
				for attempt := 1; attempt <= maxGateRetries; attempt++ {
					if err := ctx.Err(); err != nil {
						break
					}

					sessionID := goResult.SessionID
					retryResult, retryErr := retrier.RetryFailedTasks(ctx, sessionID, goOpts)
					if retryErr != nil {
						// No retryable tasks or retry setup failed — stop retrying
						break
					}

					// Update phase result with retry outcome
					pr.GoResult = retryResult
					pr.Duration = time.Since(phaseStart)

					// Re-checkout staging branch for gate test
					if retryResult != nil && retryResult.StagingBranch != "" {
						exec.Command("git", "-C", gateRepoRoot, "checkout", retryResult.StagingBranch).Run()
					}

					// Re-run gate check
					passed, output, gateErr = checkPhaseGate(gateRepoRoot, phase)
					pr.GatePassed = passed
					pr.GateOutput = output

					// Restore branch after gate check
					if gateRestoreBranch != "" {
						stagingBr := ""
						if retryResult != nil {
							stagingBr = retryResult.StagingBranch
						}
						if stagingBr == "" && goResult != nil {
							stagingBr = goResult.StagingBranch
						}
						if gateRestoreBranch != stagingBr {
							exec.Command("git", "-C", gateRepoRoot, "checkout", gateRestoreBranch).Run()
						}
					}

					// Update goResult for rolling base tracking
					if retryResult != nil {
						goResult = retryResult
					}

					if gateErr != nil {
						result.PhaseResults[len(result.PhaseResults)-1] = pr
						result.Duration = time.Since(start)
						aggregateResults(result)
						return result, fmt.Errorf("phase %q gate error on retry %d: %w", phaseID, attempt, gateErr)
					}

					if passed {
						retried = true
						break
					}

					// Gate still failed — if no more retryable failures, stop
					if retryResult != nil && retryResult.FailedCount == 0 {
						break
					}
				}
			}

			// Update the phase result in the slice
			result.PhaseResults[len(result.PhaseResults)-1] = pr

			if !passed && !retried {
				// Correction loop: give the LLM the gate output and let it fix whatever is wrong.
				// No regex parsing — the LLM reads the error text directly.
				gateRepoRoot := opts.RepoRoot
				if gateRepoRoot == "" {
					gateRepoRoot = "."
				}
				fmt.Fprintf(os.Stderr, "[correction-loop] Gate failed, spawning agent to fix\n")
				_, loopErr := RunCorrectionLoop(ctx, gateRepoRoot, phase.Gate.TestCmd, 3, func(ctx context.Context, repoRoot string, prompt string) error {
					cmd := exec.CommandContext(ctx, "claude",
						"-p", prompt,
						"--model", "claude-opus-4-6",
						"--permission-mode", "bypassPermissions",
						"--output-format", "stream-json", "--verbose")
					cmd.Dir = repoRoot
					devNull, _ := os.Open(os.DevNull)
					cmd.Stdin = devNull
					_, cmdErr := cmd.CombinedOutput()
					return cmdErr
				})
				if loopErr == nil {
					// Correction loop already verified the gate passed internally.
					// Trust its result — don't re-check with checkPhaseGate which
					// may use DetectTestCommand (a different command than what the
					// correction loop fixed against).
					passed = true
					output = "correction loop verified gate passed"
					pr.GatePassed = true
					pr.GateOutput = output
					result.PhaseResults[len(result.PhaseResults)-1] = pr
					fmt.Fprintf(os.Stderr, "[correction-loop] Gate PASSED after auto-repair\n")
				} else {
					fmt.Fprintf(os.Stderr, "[correction-loop] Auto-repair failed: %v\n", loopErr)
				}
			}

			if !passed && !retried {
				result.Duration = time.Since(start)
				aggregateResults(result)
				return result, fmt.Errorf("phase %q gate failed: %s", phaseID, output)
			}
		}

		// G-CSS-7/10: Post-merge validation hook — run after gate passes.
		if phase.Gate.PostMergeCmd != "" && pr.GatePassed {
			postMergeRoot := opts.RepoRoot
			if postMergeRoot == "" {
				postMergeRoot = "."
			}
			fmt.Fprintf(os.Stderr, "[post-merge] Running: %s\n", phase.Gate.PostMergeCmd)
			pmOut, pmErr := runTestCmd(postMergeRoot, phase.Gate.PostMergeCmd, 5*time.Minute)
			if pmErr != nil {
				fmt.Fprintf(os.Stderr, "[post-merge] WARNING: %s\n", pmOut)
				pr.GateOutput += fmt.Sprintf("\n[POST-MERGE WARNING] %s: %s", phase.Gate.PostMergeCmd, pmOut)
			} else {
				fmt.Fprintf(os.Stderr, "[post-merge] OK\n")
			}
		}

		// Fix framework-specific dev config that agents commonly miss.
		// Runs after gate passes so we don't fix files that haven't been created yet.
		if pr.GatePassed {
			fixRoot := opts.RepoRoot
			if fixRoot == "" {
				fixRoot = "."
			}
			if fixes := FixDevConfig(fixRoot); len(fixes) > 0 {
				for _, f := range fixes {
					fmt.Fprintf(os.Stderr, "[dev-config-fix] %s\n", f)
				}
				commitAutoGenerated(fixRoot)
			}
		}

		// G111: Advance rolling base to this phase's staging branch for the next phase.
		if goResult != nil && goResult.StagingBranch != "" {
			rollingBase = goResult.StagingBranch
		}
	}

	// Final E2E verification — runs after ALL phases complete successfully.
	// Catches cross-agent wiring bugs that per-phase gates miss.
	if spec.Metadata.FinalE2ECmd != "" {
		gateRepoRoot := opts.RepoRoot
		if gateRepoRoot == "" {
			gateRepoRoot = "."
		}
		// Install deps (agents install in worktrees which get cleaned after merge)
		if installCmd := DetectInstallCommand(gateRepoRoot); installCmd != "" {
			fmt.Fprintf(os.Stderr, "[final-e2e] Installing dependencies...\n")
			runTestCmd(gateRepoRoot, installCmd, 5*time.Minute)
		}
		fmt.Fprintf(os.Stderr, "[final-e2e] Running: %s\n", spec.Metadata.FinalE2ECmd)
		e2eOut, e2eErr := runTestCmd(gateRepoRoot, spec.Metadata.FinalE2ECmd, 10*time.Minute)
		if e2eErr != nil {
			fmt.Fprintf(os.Stderr, "[final-e2e] FAILED\n")
			// Trigger correction loop on E2E failure
			cleanOutput := stripANSI(e2eOut)
			correctionPrompt := fmt.Sprintf(`The final end-to-end test failed after all phases passed. Fix the errors.

Gate command: %s
Working directory: %s

Error output:
%s

Read the error output, find the file(s) causing the failure, fix them, and verify the fix compiles.
Do not add features or refactor — only fix what the E2E test says is broken.`, spec.Metadata.FinalE2ECmd, gateRepoRoot, truncateOutput(cleanOutput, 8000))
			for attempt := 1; attempt <= 3; attempt++ {
				fmt.Fprintf(os.Stderr, "[final-e2e] Correction attempt %d/3\n", attempt)
				corrCmd := exec.CommandContext(ctx, "claude",
					"-p", correctionPrompt,
					"--model", "claude-opus-4-6",
					"--permission-mode", "bypassPermissions",
					"--output-format", "stream-json", "--verbose")
				corrCmd.Dir = gateRepoRoot
				devNull, _ := os.Open(os.DevNull)
				corrCmd.Stdin = devNull
				corrCmd.CombinedOutput()
				// Re-run E2E
				e2eOut, e2eErr = runTestCmd(gateRepoRoot, spec.Metadata.FinalE2ECmd, 10*time.Minute)
				if e2eErr == nil {
					fmt.Fprintf(os.Stderr, "[final-e2e] PASSED after correction attempt %d\n", attempt)
					break
				}
			}
			if e2eErr != nil {
				result.Duration = time.Since(start)
				aggregateResults(result)
				return result, fmt.Errorf("final E2E verification failed after 3 correction attempts: %s", truncateOutput(e2eOut, 500))
			}
		} else {
			fmt.Fprintf(os.Stderr, "[final-e2e] PASSED\n")
		}
	}

	result.Duration = time.Since(start)
	aggregateResults(result)
	return result, nil
}

// ConductorGoSpec is a convenience method that creates a GoRunner from the
// conductor and delegates to GoSpec.
func (c *Conductor) GoSpec(ctx context.Context, spec *OrchestraSpec, opts GoSpecOpts) (*GoSpecResult, error) {
	runner := &conductorGoRunner{c: c}

	// G114: Thread RepoRoot so GoSpec runs gate tests in the target project dir
	if opts.RepoRoot == "" {
		opts.RepoRoot = c.RepoRoot
	}

	// B-287: When resuming with --start-phase, dev already has all prior phases
	// merged (since we now merge staging→dev after every phase). Use dev as the
	// base branch directly. The DB staging branch lookup is kept as a safety
	// fallback in case dev somehow fell behind.
	if opts.StartPhase != "" && opts.BaseBranch == "" {
		// Primary path: dev has all prior phases — use it directly
		c.log("B-287: --start-phase %q — using dev branch (all prior phases merged)", opts.StartPhase)

		// Fallback: if dev doesn't contain the prior phase's work, try staging branch
		order, sortErr := TopoSortPhases(spec.Phases)
		if sortErr == nil {
			for i, pid := range order {
				if pid == opts.StartPhase && i > 0 {
					priorPhaseID := order[i-1]
					if rec, err := c.DB.GetConductorByPhaseID(ctx, priorPhaseID); err == nil && rec != nil {
						if gitRefExists(c.RepoRoot, rec.StagingBranch) {
							// Verify dev contains the staging branch's HEAD
							devContains, _ := gitExec(c.RepoRoot, "merge-base", "--is-ancestor", rec.StagingBranch, "HEAD")
							if devContains != "" || !gitRefExists(c.RepoRoot, "HEAD") {
								// If merge-base check fails (non-zero exit = not ancestor), use staging as fallback
								c.log("B-287: Fallback — prior phase %q staging branch %s not merged to dev, using it as base", priorPhaseID, rec.StagingBranch)
								opts.BaseBranch = rec.StagingBranch
							}
						}
					}
					break
				}
			}
		}
	}

	// Log spec execution start
	c.log("Spec execution starting: %s (%d phases)", spec.Metadata.Title, len(spec.Phases))
	c.notifyTelegram(fmt.Sprintf("🎼 Orchestra Spec: Starting — %s (%d phases)", spec.Metadata.Title, len(spec.Phases)))

	result, err := GoSpec(ctx, spec, opts, runner)

	// Log completion
	if err != nil {
		c.log("Spec execution failed: %v", err)
		phasesRun := 0
		if result != nil {
			for _, pr := range result.PhaseResults {
				if !pr.Skipped {
					phasesRun++
				}
			}
		}
		c.notifyTelegram(fmt.Sprintf("🎼 Orchestra Spec: FAILED at phase %d/%d — %s", phasesRun, len(spec.Phases), spec.Metadata.Title))
	} else {
		c.log("Spec execution complete: %d phases, %d done, %d failed (%s)",
			len(result.PhaseResults), result.TotalDone, result.TotalFailed, result.Duration.Round(time.Second))
		c.notifyTelegram(fmt.Sprintf("🎼 Orchestra Spec: Complete — %d/%d phases passed (%s)",
			len(result.PhaseResults), len(spec.Phases), result.Duration.Round(time.Second)))
	}

	return result, err
}

// buildPhaseGoOpts converts a phase definition + global opts into GoOpts for Go().
func buildPhaseGoOpts(phase Phase, meta SpecMetadata, global GoSpecOpts, isFinal bool) GoOpts {
	config := PhaseGoOpts(phase, meta)

	// G123: Don't pass phase gate test_cmd to Go() as the per-task merge test.
	// The phase gate checks the collective outcome (e.g., "all 11 files exist")
	// which only passes after ALL tasks merge. Individual task merges would fail
	// this test because each task only produces a subset. The phase gate runs
	// separately after Go() returns (checkPhaseGate at line 182).
	// G-CSS-6: Extract atomic task titles from the phase tasks for enforcement
	var atomicTasks []string
	for _, t := range phase.Tasks {
		if t.Atomic {
			atomicTasks = append(atomicTasks, t.Title)
		}
	}

	opts := GoOpts{
		Goal:            config.Goal,
		TestCmd:         "", // phase gate runs post-Go(), not per-task
		MaxTasks:        config.MaxTasks,
		FileEnforcement: config.FileEnforcement,
		MaxParallel:     global.MaxParallel,
		Interval:        global.Interval,
		Review:          global.Review,
		RepoMap:         global.RepoMap,
		BaseBranch:      global.BaseBranch,
		Runtime:         global.Runtime,
		MergeMode:       global.MergeMode,
		Reconcile:       isFinal && global.Reconcile,
		Clarify:         false, // spec already defines tasks
		LenientDeps:     true,  // phases are sequential, partial OK
		MaxFilesPerTask: 25,    // default cap
		PhaseID:         phase.ID,                // G110: tag tasks with phase
		KeepStaging:     !isFinal,                // G111: preserve staging for next phase
		AtomicTasks:     atomicTasks,             // G-CSS-6: enforce atomic tasks
		SpecTasks:       phase.Tasks,             // Enforce spec-defined roles post-decomposition
		Sandbox:         global.Sandbox,          // propagate sandbox setting
	}

	return opts
}

// commitAutoGenerated commits any files generated by build/test commands.
// Gates run cargo build, npm run build, etc. which create auto-generated files
// (Tauri schemas, lock files, compiled assets). If these aren't committed,
// subsequent merges fail with "local changes would be overwritten."
func commitAutoGenerated(repoRoot string) {
	statusOut, _ := exec.CommandContext(context.Background(), "git", "-C", repoRoot, "status", "--porcelain").Output()
	if len(strings.TrimSpace(string(statusOut))) > 0 {
		exec.CommandContext(context.Background(), "git", "-C", repoRoot, "add", "-A").Run()
		exec.CommandContext(context.Background(), "git", "-C", repoRoot, "commit", "-m", "auto-commit: build-generated files from gate").Run()
	}
}

// checkPhaseGate runs the phase gate and returns whether it passed.
// B-288: Supports three modes — test (run test_cmd), acceptance (heuristic file checks),
// none (always pass). Default auto-detects: test_cmd if present, then acceptance heuristics,
// then auto-pass.
func checkPhaseGate(repoRoot string, phase Phase) (passed bool, output string, err error) {
	mode := phase.Gate.Mode

	// Explicit mode: none
	if mode == "none" {
		return true, "gate mode: none (explicit opt-out)", nil
	}

	// Explicit mode: test — require test_cmd
	if mode == "test" {
		if phase.Gate.TestCmd == "" {
			return false, "gate mode: test but no test_cmd defined", nil
		}
		// Install dependencies before running gate tests.
		// Agents install deps in worktrees which are cleaned after merge.
		// The main branch needs deps reinstalled before build/test gates.
		if installCmd := DetectInstallCommand(repoRoot); installCmd != "" {
			instOut, instErr := runTestCmd(repoRoot, installCmd, 5*time.Minute)
			if instErr != nil {
				return false, fmt.Sprintf("dependency install failed: %s\n%s", instErr, instOut), nil
			}
		}

		// Override spec's test_cmd with detected command if available.
		// LLM-generated test_cmd may be wrong (e.g., Tauri's npm run build
		// triggers DMG bundling). DetectTestCommand knows framework specifics.
		testCmd := phase.Gate.TestCmd
		if detected := DetectTestCommand(repoRoot); detected != "" {
			testCmd = detected
		}
		out, runErr := runTestCmd(repoRoot, testCmd, 5*time.Minute)
		commitAutoGenerated(repoRoot)
		if runErr != nil {
			return false, classifyGateFailure(phase.Gate.TestCmd, out, runErr), nil
		}
		return true, out, nil
	}

	// Explicit mode: acceptance — run heuristic checks
	if mode == "acceptance" {
		if len(phase.Gate.Acceptance) == 0 {
			return true, "gate mode: acceptance but no criteria defined", nil
		}
		return checkAcceptanceHeuristics(repoRoot, phase)
	}

	// Auto-detect mode (default): test_cmd → acceptance → auto-pass
	if phase.Gate.TestCmd != "" {
		out, runErr := runTestCmd(repoRoot, phase.Gate.TestCmd, 5*time.Minute)
		commitAutoGenerated(repoRoot)
		if runErr != nil {
			return false, classifyGateFailure(phase.Gate.TestCmd, out, runErr), nil
		}
		return true, out, nil
	}

	if len(phase.Gate.Acceptance) > 0 {
		return checkAcceptanceHeuristics(repoRoot, phase)
	}

	return true, "no gate defined", nil
}

// classifyGateFailure inspects a runTestCmd error and output to produce a
// descriptive failure message distinguishing command-not-found from test failures.
func classifyGateFailure(testCmd, output string, err error) string {
	// Check for exec.ErrNotFound (binary not in PATH)
	if errors.Is(err, exec.ErrNotFound) {
		return fmt.Sprintf("command not found: %s", testCmd)
	}
	// Check for os.PathError (path doesn't exist)
	var pathErr *os.PathError
	if errors.As(err, &pathErr) {
		return fmt.Sprintf("path not found: %s (%v)", testCmd, pathErr)
	}
	// Check output for not-found indicators (bash reports these)
	outLower := strings.ToLower(output)
	if strings.Contains(outLower, "not found") || strings.Contains(outLower, "no such file") {
		return fmt.Sprintf("command not found: %s — %s", testCmd, strings.TrimSpace(output))
	}
	// Extract exit code from ExitError
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		tail := strings.TrimSpace(output)
		if len(tail) > 500 {
			tail = tail[len(tail)-500:]
		}
		return fmt.Sprintf("test failed (exit code %d): %s", exitErr.ExitCode(), tail)
	}
	return fmt.Sprintf("test failed: %s", strings.TrimSpace(output))
}

// acceptanceFileCountRe matches patterns like "3 chapters written", "5 files exist",
// "at least 10 research files".
var acceptanceFileCountRe = regexp.MustCompile(`(?i)(?:at\s+least\s+)?(\d+)\s+(\w+(?:\s+\w+)?)\s+(?:written|exist|created|produced|generated)`)

// acceptanceGlobRe matches patterns like "all research/*.md exist" or "chapters/*.md files exist".
var acceptanceGlobRe = regexp.MustCompile(`(?i)(?:all\s+)?([a-zA-Z0-9_.*/-]+\.\w+)\s+(?:files?\s+)?(?:exist|written|created)`)

// checkAcceptanceHeuristics evaluates acceptance criteria strings using file-based
// heuristics. Returns (passed, output, nil). Criteria that can't be evaluated
// programmatically auto-pass with a warning.
func checkAcceptanceHeuristics(repoRoot string, phase Phase) (bool, string, error) {
	var results []string
	allPassed := true

	for _, criterion := range phase.Gate.Acceptance {
		passed, detail := evaluateAcceptanceCriterion(repoRoot, criterion)
		status := "PASS"
		if !passed {
			status = "FAIL"
			allPassed = false
		}
		results = append(results, fmt.Sprintf("[%s] %s: %s", status, criterion, detail))
	}

	output := strings.Join(results, "\n")
	return allPassed, output, nil
}

// evaluateAcceptanceCriterion tries to evaluate a single acceptance criterion
// using file-based heuristics. Returns (passed, detail).
func evaluateAcceptanceCriterion(repoRoot, criterion string) (bool, string) {
	// Pattern 1: "N <things> written/exist/created" → check file count with glob
	if m := acceptanceFileCountRe.FindStringSubmatch(criterion); len(m) >= 3 {
		expectedCount, _ := strconv.Atoi(m[1])
		fileType := strings.ToLower(m[2])

		// Build glob pattern from the file type description
		pattern := inferGlobPattern(fileType)
		if pattern != "" {
			matches, _ := filepath.Glob(filepath.Join(repoRoot, pattern))
			if len(matches) >= expectedCount {
				return true, fmt.Sprintf("found %d files matching %s (need %d)", len(matches), pattern, expectedCount)
			}
			return false, fmt.Sprintf("found %d files matching %s (need %d)", len(matches), pattern, expectedCount)
		}
	}

	// Pattern 2: "path/to/*.ext exist" → check glob directly
	if m := acceptanceGlobRe.FindStringSubmatch(criterion); len(m) >= 2 {
		globPattern := m[1]
		matches, _ := filepath.Glob(filepath.Join(repoRoot, globPattern))
		if len(matches) > 0 {
			return true, fmt.Sprintf("found %d files matching %s", len(matches), globPattern)
		}
		return false, fmt.Sprintf("no files found matching %s", globPattern)
	}

	// Can't evaluate programmatically — auto-pass with warning
	return true, "auto-pass (cannot evaluate programmatically)"
}

// inferGlobPattern converts a human file type description to a glob pattern.
func inferGlobPattern(fileType string) string {
	// Normalize
	ft := strings.TrimSuffix(fileType, "s") // "chapters" → "chapter"
	ft = strings.TrimSpace(ft)

	switch {
	case strings.Contains(ft, "chapter"):
		return "chapters/*.md"
	case strings.Contains(ft, "research file"):
		return "research/*.md"
	case strings.Contains(ft, "research"):
		return "research/*.md"
	case strings.Contains(ft, "lesson"):
		return "lessons/*.md"
	case strings.Contains(ft, "syllab"):
		return "syllabi/*.md"
	case strings.Contains(ft, "slide"):
		return "slides/*.md"
	case strings.Contains(ft, "outline"):
		return "outlines/*.md"
	case strings.Contains(ft, "spec"):
		return "specs/*.md"
	case strings.Contains(ft, "diagram"):
		return "diagrams/*"
	case strings.Contains(ft, "figure"):
		return "figures/*"
	case strings.Contains(ft, "file"):
		return "**/*.md"
	default:
		return ""
	}
}

// aggregateResults sums done/failed counts across all phase results.
func aggregateResults(result *GoSpecResult) {
	result.TotalDone = 0
	result.TotalFailed = 0
	for _, pr := range result.PhaseResults {
		if pr.GoResult != nil {
			result.TotalDone += pr.GoResult.DoneCount
			result.TotalFailed += pr.GoResult.FailedCount
		}
	}
}
