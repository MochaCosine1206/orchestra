package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/MochaCosine1206/orchestra/internal/agent"
	"github.com/MochaCosine1206/orchestra/internal/db"
	"github.com/MochaCosine1206/orchestra/internal/isolation"
	"github.com/MochaCosine1206/orchestra/internal/orchestrator"
	"github.com/MochaCosine1206/orchestra/internal/recursion"
)

// NewGoCmd creates the go subcommand (orchestrate a goal).
func NewGoCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "go",
		Short: "Orchestrate a goal",
		Long: `Decompose a goal into tasks, spawn agents, monitor execution, and merge results.

Requires a git repository (agents work in isolated worktrees).

By default, the conductor runs as a detached process that survives terminal
close and parent process death. Use --foreground to run in-process (blocks
until completion).`,
		RunE: func(cmd *cobra.Command, args []string) error {
			requireVersion, _ := cmd.Flags().GetBool("require-version")
			if err := checkCLIVersion(cmd, requireVersion); err != nil {
				return err
			}

			foreground, _ := cmd.Flags().GetBool("foreground")

			if foreground {
				return runForeground(cmd)
			}
			return runDetached(cmd)
		},
	}

	cmd.Flags().StringP("goal", "g", "", "Goal description (required)")
	cmd.Flags().String("test-cmd", "", "Post-implementation test command")
	cmd.Flags().Bool("iterative", false, "Session-cycling mode for large goals")
	cmd.Flags().Int("max-tasks", 8, "Maximum tasks for decomposition (iterative mode: max rounds, capped at 5)")
	cmd.Flags().Int("max-files-per-task", 25, "Maximum files per task (0 = unlimited)")
	cmd.Flags().Bool("review", false, "Enable pre-decompose and pre-merge review gates")
	cmd.Flags().Bool("dry-run", false, "Show plan without executing")
	cmd.Flags().Int("max-parallel", 8, "Maximum concurrent agents")
	cmd.Flags().Int("interval", 15, "Monitor poll interval in seconds")
	cmd.Flags().String("model-strategy", "all-opus", "Model strategy: all-opus, per-role, all-sonnet")
	cmd.Flags().Bool("clarify", false, "Enable goal clarification before decomposition")
	cmd.Flags().String("clarify-mode", "auto", "Clarification mode: auto (use defaults), cli (interactive)")
	cmd.Flags().Bool("repo-map", false, "Include compact repo map in task specs")
	cmd.Flags().String("base-branch", "", "Base branch to merge into (default: auto-detect from repo)")
	cmd.Flags().Bool("reconcile", true, "Run post-session reconciliation")
	cmd.Flags().Bool("lenient-deps", false, "Lenient dependency mode: proceed with partial predecessor output")
	cmd.Flags().Bool("cascade", false, "Cascade routing: auto-select single/iterative/multi-agent based on complexity")
	cmd.Flags().String("file-enforcement", "", "File ownership enforcement: defense, pessimistic")
	cmd.Flags().String("test-failure-mode", "", "Test failure behavior: revert_and_refine, warn_only, revert_no_refine")
	cmd.Flags().Int("test-cmd-timeout", 300, "Test command timeout in seconds")
	cmd.Flags().Bool("disable-action-expansion", false, "Skip vague file action expansion")
	cmd.Flags().StringSlice("read-only-files", nil, "Files to exclude from modification targets")
	cmd.Flags().Bool("foreground", false, "Run conductor in foreground (blocks until completion)")
	cmd.Flags().String("runtime", "", "Execution runtime: local (default), docker")
	cmd.Flags().String("merge-mode", "local", "Merge mode: local (default), pr (create GitHub PR)")
	cmd.Flags().String("merge-strategy", "", "Merge strategy: batch (default), fifo (process as tasks complete)")
	cmd.Flags().Bool("hierarchical", false, "Enable hierarchical decomposition with feature clusters (B-281)")
	cmd.Flags().Bool("sandbox", false, "Enable Docker container sandboxing with read-only root and network restrictions")
	cmd.Flags().Bool("json", false, "Machine-readable JSON output")
	cmd.Flags().Bool("require-version", false, "Abort if Claude CLI version is below minimum")
	_ = cmd.MarkFlagRequired("goal")

	return cmd
}

// runForeground runs the conductor in-process (original behavior).
func runForeground(cmd *cobra.Command) error {
	goal, _ := cmd.Flags().GetString("goal")
	testCmd, _ := cmd.Flags().GetString("test-cmd")
	iterative, _ := cmd.Flags().GetBool("iterative")
	review, _ := cmd.Flags().GetBool("review")
	dryRun, _ := cmd.Flags().GetBool("dry-run")
	maxTasks, _ := cmd.Flags().GetInt("max-tasks")
	maxFilesPerTask, _ := cmd.Flags().GetInt("max-files-per-task")
	maxParallel, _ := cmd.Flags().GetInt("max-parallel")
	interval, _ := cmd.Flags().GetInt("interval")
	modelStrategy, _ := cmd.Flags().GetString("model-strategy")
	clarify, _ := cmd.Flags().GetBool("clarify")
	clarifyModeStr, _ := cmd.Flags().GetString("clarify-mode")
	clarifyMode := parseClarifyMode(clarifyModeStr)
	repoMap, _ := cmd.Flags().GetBool("repo-map")
	baseBranch, _ := cmd.Flags().GetString("base-branch")
	reconcile, _ := cmd.Flags().GetBool("reconcile")
	lenientDeps, _ := cmd.Flags().GetBool("lenient-deps")
	fileEnforcement, _ := cmd.Flags().GetString("file-enforcement")
	testFailureMode, _ := cmd.Flags().GetString("test-failure-mode")
	testCmdTimeout, _ := cmd.Flags().GetInt("test-cmd-timeout")
	disableActionExpansion, _ := cmd.Flags().GetBool("disable-action-expansion")
	readOnlyFiles, _ := cmd.Flags().GetStringSlice("read-only-files")
	cascade, _ := cmd.Flags().GetBool("cascade")
	runtimeFlag, _ := cmd.Flags().GetString("runtime")
	mergeMode, _ := cmd.Flags().GetString("merge-mode")
	mergeStrategy, _ := cmd.Flags().GetString("merge-strategy")
	hierarchical, _ := cmd.Flags().GetBool("hierarchical")
	sandboxFlag, _ := cmd.Flags().GetBool("sandbox")

	d, err := openDB(cmd)
	if err != nil {
		return fmt.Errorf("opening database: %w", err)
	}
	defer d.Close()

	repoRoot, _ := os.Getwd()

	// Project-level lock: only one conductor per project.
	projectSlug := filepath.Base(repoRoot)
	locked, existingPID, lockErr := isolation.IsLocked(projectSlug)
	if lockErr == nil && locked {
		return fmt.Errorf("project %q is already running (PID %d) — stop the existing conductor first", projectSlug, existingPID)
	}
	projectLock, err := isolation.AcquireLock(projectSlug)
	if err != nil {
		return fmt.Errorf("acquiring project lock: %w", err)
	}
	defer projectLock.ReleaseLock()

	// Recursion safety: check depth and acquire repo lock.
	guard := recursion.NewGuard()
	cleanup, err := guard.Enter(repoRoot)
	if err != nil {
		return fmt.Errorf("recursion guard: %w", err)
	}
	defer cleanup()

	// Cap maxParallel to depth-based agent limit.
	depthCap := guard.MaxAgentsForCurrentDepth()
	if maxParallel > depthCap {
		maxParallel = depthCap
	}

	runner := &orchestrator.ExecRunner{}
	c, err := orchestrator.New(orchestrator.ConductorOpts{
		DB:       d,
		RepoRoot: repoRoot,
		Runner:   runner,
	})
	if err != nil {
		return fmt.Errorf("creating conductor: %w", err)
	}

	if err := c.SetupLogFile(); err != nil {
		cmd.PrintErrf("Warning: conductor log: %v\n", err)
	}
	defer c.CloseLogFile()

	goOpts := orchestrator.GoOpts{
		Goal:                   goal,
		TestCmd:                testCmd,
		Iterative:              iterative,
		Review:                 review,
		DryRun:                 dryRun,
		MaxTasks:               maxTasks,
		MaxFilesPerTask:        maxFilesPerTask,
		MaxParallel:            maxParallel,
		Interval:               interval,
		ModelStrategy:          modelStrategy,
		Clarify:                clarify,
		ClarifyMode:            clarifyMode,
		RepoMap:                repoMap,
		BaseBranch:             baseBranch,
		Reconcile:              reconcile,
		LenientDeps:            lenientDeps,
		FileEnforcement:        fileEnforcement,
		TestFailureMode:        testFailureMode,
		TestCmdTimeout:         testCmdTimeout,
		DisableActionExpansion: disableActionExpansion,
		ReadOnlyFiles:          readOnlyFiles,
		Runtime:                runtimeFlag,
		MergeMode:              mergeMode,
		MergeStrategy:          mergeStrategy,
		Hierarchical:           hierarchical,
		Sandbox:                sandboxFlag,
	}

	jsonMode, _ := cmd.Flags().GetBool("json")

	var result *orchestrator.GoResult
	if cascade {
		result, err = c.GoCascade(cmd.Context(), goOpts)
	} else {
		result, err = c.Go(cmd.Context(), goOpts)
	}
	if err != nil {
		oe := wrapGoInfraError(err)
		if jsonMode {
			PrintJSONError(cmd.ErrOrStderr(), oe)
		}
		return oe
	}

	// Print success summary to stdout.
	printGoSummary(cmd, result)

	// JSON success output.
	if jsonMode {
		enc := json.NewEncoder(cmd.OutOrStdout())
		enc.SetIndent("", "  ")
		_ = enc.Encode(result)
	}

	// Detect task-level failures even when Go() returns nil error.
	if result.FailedCount > 0 {
		oe := buildGoTaskFailureError(result)
		if jsonMode {
			PrintJSONError(cmd.ErrOrStderr(), oe)
		}
		return oe
	}

	// Check for merge-only failures.
	if result.MergeResult != nil && len(result.MergeResult.Failed) > 0 {
		oe := &OrchestraError{
			ExitCode: ExitPartialFailure,
			Category: CategoryPartial,
			Summary:  fmt.Sprintf("%d branches failed to merge", len(result.MergeResult.Failed)),
			Detail:   fmt.Sprintf("Session: %s | Duration: %s", result.SessionID, result.Duration.Round(1000000000)),
			Suggestions: []string{
				"orchestra status --json  # inspect merge failures",
				"orchestra reset  # clean slate and retry",
			},
			Cause: fmt.Errorf("merge failures: %s", strings.Join(result.MergeResult.Failed, ", ")),
		}
		if jsonMode {
			PrintJSONError(cmd.ErrOrStderr(), oe)
		}
		return oe
	}

	return nil
}

// printGoSummary prints the human-readable success summary for orchestra go.
func printGoSummary(cmd *cobra.Command, result *orchestrator.GoResult) {
	cmd.Printf("Session: %s\n", result.SessionID)
	cmd.Printf("Tasks: %d (done=%d, failed=%d)\n",
		result.TaskCount, result.DoneCount, result.FailedCount)
	cmd.Printf("Duration: %s\n", result.Duration.Round(1000000000))

	if result.MergeResult != nil {
		mr := result.MergeResult
		cmd.Printf("Merged: %d, Failed: %d, Skipped: %d\n",
			len(mr.Merged), len(mr.Failed), len(mr.Skipped))
	}

	if result.PRResult != nil {
		cmd.Printf("\n--- Pull Request ---\n")
		cmd.Printf("PR URL: %s\n", result.PRResult.PRURL)
		cmd.Printf("Branch: %s → %s\n", result.PRResult.Branch, result.PRResult.Base)
	}

	if result.ReconcileResult != nil {
		rr := result.ReconcileResult
		cmd.Printf("\n--- Reconciliation ---\n")
		cmd.Printf("Alignment: %.0f%%\n", rr.GoalAlignmentScore*100)
		if len(rr.OrphanWorktrees) > 0 {
			cmd.Printf("Orphan worktrees: %d (salvaged: %d)\n",
				len(rr.OrphanWorktrees), len(rr.SalvagedWorktrees))
		}
		if len(rr.GapsIdentified) > 0 {
			cmd.Printf("Gaps: %d\n", len(rr.GapsIdentified))
			for _, g := range rr.GapsIdentified {
				cmd.Printf("  - %s\n", g)
			}
		}
		if len(rr.FollowUpTaskIDs) > 0 {
			cmd.Printf("Follow-up tasks: %d\n", len(rr.FollowUpTaskIDs))
		}
	}
}

// wrapGoInfraError wraps an infrastructure error from Go()/GoCascade()
// into a categorized OrchestraError.
func wrapGoInfraError(err error) *OrchestraError {
	msg := err.Error()
	exitCode := exitCodeHeuristic(err)
	category := CategoryFatal
	suggestions := suggestFromRawError(err)

	// Provide more specific categorization.
	switch {
	case strings.Contains(msg, "not a git repository"):
		category = CategoryInput
	case strings.Contains(msg, "decompos"):
		category = CategoryFatal
		if len(suggestions) == 0 {
			suggestions = append(suggestions, "orchestra go --help  # check goal format")
		}
	case strings.Contains(msg, "database"), strings.Contains(msg, "schema"):
		category = CategoryInput
	}

	return &OrchestraError{
		ExitCode:    exitCode,
		Category:    category,
		Summary:     fmt.Sprintf("go: %s", msg),
		Suggestions: suggestions,
		Cause:       err,
	}
}

// buildGoTaskFailureError builds an OrchestraError from a GoResult with failed tasks.
func buildGoTaskFailureError(result *orchestrator.GoResult) *OrchestraError {
	exitCode := ExitPartialFailure
	if result.DoneCount == 0 {
		exitCode = ExitTotalFailure
	}

	var failedTasks []TaskFailure
	if result.ReconcileResult != nil {
		for _, ft := range result.ReconcileResult.FailedTasks {
			failedTasks = append(failedTasks, TaskFailure{
				TaskID:      ft.TaskID,
				Title:       ft.Title,
				FailureType: ft.FailureType,
				Suggestion:  TaskRecoverySuggestion(ft.FailureType),
			})
		}
	}

	return &OrchestraError{
		ExitCode:    exitCode,
		Category:    CategoryPartial,
		Summary:     fmt.Sprintf("%d of %d tasks failed", result.FailedCount, result.TaskCount),
		Detail:      fmt.Sprintf("Session: %s | Duration: %s", result.SessionID, result.Duration.Round(1000000000)),
		FailedTasks: failedTasks,
		Suggestions: []string{
			"orchestra status --json  # inspect failed task details",
			"orchestra reset  # clean slate and retry",
		},
	}
}

// runDetached spawns `orchestra conductor-run` as a detached process that
// survives parent death, then returns immediately.
func runDetached(cmd *cobra.Command) error {
	repoRoot, _ := os.Getwd()

	// Ensure runtime directories exist.
	logsDir := filepath.Join(repoRoot, ".orchestra", "logs")
	pidsDir := filepath.Join(repoRoot, ".orchestra", "pids")
	if err := os.MkdirAll(logsDir, 0o755); err != nil {
		return fmt.Errorf("creating logs dir: %w", err)
	}
	if err := os.MkdirAll(pidsDir, 0o755); err != nil {
		return fmt.Errorf("creating pids dir: %w", err)
	}

	// Project-level lock: only one conductor per project.
	projectSlug := filepath.Base(repoRoot)
	locked, existingPID, lockErr := isolation.IsLocked(projectSlug)
	if lockErr == nil && locked {
		return fmt.Errorf("project %q is already running (PID %d) — stop the existing conductor first", projectSlug, existingPID)
	}
	projectLock, err := isolation.AcquireLock(projectSlug)
	if err != nil {
		return fmt.Errorf("acquiring project lock: %w", err)
	}
	defer projectLock.ReleaseLock()

	// Generate session ID early so the log file is session-scoped.
	sessionID := db.GenID("s")

	// Build args for conductor-run, forwarding all flags.
	conductorArgs := []string{"conductor-run"}
	conductorArgs = append(conductorArgs, "--session-id", sessionID)
	conductorArgs = appendFlagStr(conductorArgs, cmd, "goal", "g")
	conductorArgs = appendFlagStr(conductorArgs, cmd, "test-cmd", "")
	conductorArgs = appendFlagBool(conductorArgs, cmd, "iterative")
	conductorArgs = appendFlagInt(conductorArgs, cmd, "max-tasks")
	conductorArgs = appendFlagInt(conductorArgs, cmd, "max-files-per-task")
	conductorArgs = appendFlagBool(conductorArgs, cmd, "review")
	conductorArgs = appendFlagBool(conductorArgs, cmd, "dry-run")
	conductorArgs = appendFlagInt(conductorArgs, cmd, "max-parallel")
	conductorArgs = appendFlagInt(conductorArgs, cmd, "interval")
	conductorArgs = appendFlagStr(conductorArgs, cmd, "model-strategy", "")
	conductorArgs = appendFlagBool(conductorArgs, cmd, "clarify")
	conductorArgs = appendFlagStr(conductorArgs, cmd, "clarify-mode", "")
	conductorArgs = appendFlagBool(conductorArgs, cmd, "repo-map")
	conductorArgs = appendFlagStr(conductorArgs, cmd, "base-branch", "")
	conductorArgs = appendFlagBool(conductorArgs, cmd, "reconcile")
	conductorArgs = appendFlagBool(conductorArgs, cmd, "lenient-deps")
	conductorArgs = appendFlagBool(conductorArgs, cmd, "cascade")
	conductorArgs = appendFlagStr(conductorArgs, cmd, "file-enforcement", "")
	conductorArgs = appendFlagStr(conductorArgs, cmd, "test-failure-mode", "")
	conductorArgs = appendFlagInt(conductorArgs, cmd, "test-cmd-timeout")
	conductorArgs = appendFlagBool(conductorArgs, cmd, "disable-action-expansion")
	conductorArgs = appendFlagStr(conductorArgs, cmd, "read-only-files", "")
	conductorArgs = appendFlagStr(conductorArgs, cmd, "runtime", "")
	conductorArgs = appendFlagStr(conductorArgs, cmd, "merge-mode", "")
	conductorArgs = appendFlagStr(conductorArgs, cmd, "merge-strategy", "")
	conductorArgs = appendFlagBool(conductorArgs, cmd, "hierarchical")
	conductorArgs = appendFlagBool(conductorArgs, cmd, "sandbox")

	// Forward persistent flags.
	dbPath, _ := cmd.Root().PersistentFlags().GetString("db")
	conductorArgs = append(conductorArgs, "--db", dbPath)

	// Set up session-scoped log file.
	logPath := filepath.Join(logsDir, "conductor-"+sessionID+".log")
	logFd, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return fmt.Errorf("creating conductor log: %w", err)
	}

	// Spawn detached conductor process.
	binary, err := os.Executable()
	if err != nil {
		logFd.Close()
		return fmt.Errorf("resolving executable path: %w", err)
	}

	child := exec.Command(binary, conductorArgs...)
	child.Dir = repoRoot
	child.Stdout = logFd
	child.Stderr = logFd
	child.Env = recursion.IncrementedEnv()
	child.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	if err := child.Start(); err != nil {
		logFd.Close()
		return fmt.Errorf("starting conductor process: %w", err)
	}

	pid := child.Process.Pid

	// Write session-scoped PID file (supports parallel conductors).
	if err := agent.WritePID(pidsDir, "conductor-"+sessionID, pid); err != nil {
		logFd.Close()
		return fmt.Errorf("writing conductor PID: %w", err)
	}

	// Release the child — don't wait for it.
	child.Process.Release()
	logFd.Close()

	cmd.Printf("Conductor started (PID %d)\n", pid)
	cmd.Printf("Log: %s\n", logPath)
	cmd.Printf("\nMonitor with:\n")
	cmd.Printf("  orchestra dashboard\n")
	cmd.Printf("  orchestra status\n")
	cmd.Printf("  tail -f %s\n", logPath)

	return nil
}

// appendFlagStr appends --name=value if the flag was changed.
func appendFlagStr(args []string, cmd *cobra.Command, name, shorthand string) []string {
	f := cmd.Flags().Lookup(name)
	if f == nil || !f.Changed {
		return args
	}
	return append(args, "--"+name, f.Value.String())
}

// appendFlagBool appends --name if the bool flag was changed.
func appendFlagBool(args []string, cmd *cobra.Command, name string) []string {
	f := cmd.Flags().Lookup(name)
	if f == nil || !f.Changed {
		return args
	}
	return append(args, "--"+name+"="+f.Value.String())
}

// appendFlagInt appends --name=value if the int flag was changed.
func appendFlagInt(args []string, cmd *cobra.Command, name string) []string {
	f := cmd.Flags().Lookup(name)
	if f == nil || !f.Changed {
		return args
	}
	val, err := strconv.Atoi(f.Value.String())
	if err != nil {
		return args
	}
	return append(args, "--"+name, strconv.Itoa(val))
}
