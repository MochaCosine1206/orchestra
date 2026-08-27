package cmd

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/MochaCosine1206/orchestra/internal/orchestrator"
	"github.com/MochaCosine1206/orchestra/internal/recursion"
)

// NewConductorRunCmd creates the hidden conductor-run command.
// This is the detached process that actually runs the conductor lifecycle.
// It is spawned by `orchestra go` and not intended for direct user invocation.
func NewConductorRunCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:    "conductor-run",
		Short:  "Run conductor lifecycle (internal)",
		Hidden: true,
		RunE: func(cmd *cobra.Command, args []string) error {
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
			cascade, _ := cmd.Flags().GetBool("cascade")
			fileEnforcement, _ := cmd.Flags().GetString("file-enforcement")
			testFailureMode, _ := cmd.Flags().GetString("test-failure-mode")
			testCmdTimeout, _ := cmd.Flags().GetInt("test-cmd-timeout")
			disableActionExpansion, _ := cmd.Flags().GetBool("disable-action-expansion")
			readOnlyFiles, _ := cmd.Flags().GetStringSlice("read-only-files")
			runtimeFlag, _ := cmd.Flags().GetString("runtime")
			mergeMode, _ := cmd.Flags().GetString("merge-mode")
			mergeStrategy, _ := cmd.Flags().GetString("merge-strategy")
			hierarchical, _ := cmd.Flags().GetBool("hierarchical")
			sandboxFlag, _ := cmd.Flags().GetBool("sandbox")
			sessionID, _ := cmd.Flags().GetString("session-id")

			// Recursion safety: block if depth exceeded (conductor is the long-running process)
			guard := recursion.NewGuard()
			cleanup, guardErr := guard.Enter(".", sessionID)
			if guardErr != nil {
				return fmt.Errorf("recursion guard: %w", guardErr)
			}
			defer cleanup()

			d, err := openDB(cmd)
			if err != nil {
				return fmt.Errorf("opening database: %w", err)
			}
			defer d.Close()

			repoRoot, _ := os.Getwd()
			runner := &orchestrator.ExecRunner{}
			c, err := orchestrator.New(orchestrator.ConductorOpts{
				DB:        d,
				RepoRoot:  repoRoot,
				Runner:    runner,
				SessionID: sessionID,
			})
			if err != nil {
				return fmt.Errorf("creating conductor: %w", err)
			}

			if err := c.SetupLogFile(); err != nil {
				fmt.Fprintf(os.Stderr, "Warning: conductor log: %v\n", err)
			}
			defer c.CloseLogFile()

			// Set up signal handling: SIGINT/SIGTERM → cancel context →
			// allows deferred cleanup in Go() (deactivateConductor, etc.).
			ctx, cancel := context.WithCancel(cmd.Context())
			defer cancel()

			sigCh := make(chan os.Signal, 1)
			signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
			go func() {
				sig := <-sigCh
				fmt.Fprintf(os.Stderr, "conductor-run: received %s, shutting down...\n", sig)
				cancel()
			}()

			goOpts := orchestrator.GoOpts{
				Goal:            goal,
				TestCmd:         testCmd,
				Iterative:       iterative,
				Review:          review,
				DryRun:          dryRun,
				MaxTasks:        maxTasks,
				MaxFilesPerTask: maxFilesPerTask,
				MaxParallel:     maxParallel,
				Interval:        interval,
				ModelStrategy:   modelStrategy,
				Clarify:         clarify,
				ClarifyMode:     clarifyMode,
				RepoMap:         repoMap,
				BaseBranch:      baseBranch,
				Reconcile:       reconcile,
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

			var result *orchestrator.GoResult
			if cascade {
				result, err = c.GoCascade(ctx, goOpts)
			} else {
				result, err = c.Go(ctx, goOpts)
			}
			if err != nil {
				return wrapGoInfraError(err)
			}

			// Print results to stdout (goes to conductor.log in detached mode).
			printGoSummary(cmd, result)

			// Detect task-level failures.
			if result.FailedCount > 0 {
				return buildGoTaskFailureError(result)
			}

			return nil
		},
	}

	// Same flags as `orchestra go` so they can be forwarded.
	cmd.Flags().StringP("goal", "g", "", "Goal description")
	cmd.Flags().String("test-cmd", "", "Post-implementation test command")
	cmd.Flags().Bool("iterative", false, "Session-cycling mode")
	cmd.Flags().Int("max-tasks", 8, "Maximum tasks for decomposition")
	cmd.Flags().Int("max-files-per-task", 25, "Maximum files per task (0 = unlimited)")
	cmd.Flags().Bool("review", false, "Enable review gates")
	cmd.Flags().Bool("dry-run", false, "Show plan without executing")
	cmd.Flags().Int("max-parallel", 8, "Maximum concurrent agents")
	cmd.Flags().Int("interval", 15, "Monitor poll interval in seconds")
	cmd.Flags().String("model-strategy", "all-opus", "Model strategy")
	cmd.Flags().Bool("clarify", false, "Enable goal clarification")
	cmd.Flags().String("clarify-mode", "auto", "Clarification mode")
	cmd.Flags().Bool("repo-map", false, "Include compact repo map in task specs")
	cmd.Flags().String("base-branch", "", "Base branch to merge into")
	cmd.Flags().Bool("reconcile", true, "Run post-session reconciliation")
	cmd.Flags().Bool("lenient-deps", false, "Lenient dependency mode")
	cmd.Flags().Bool("cascade", false, "Cascade routing")
	cmd.Flags().String("file-enforcement", "", "File ownership enforcement: defense, pessimistic")
	cmd.Flags().String("test-failure-mode", "", "Test failure behavior: revert_and_refine, warn_only, revert_no_refine")
	cmd.Flags().Int("test-cmd-timeout", 300, "Test command timeout in seconds")
	cmd.Flags().Bool("disable-action-expansion", false, "Skip vague file action expansion")
	cmd.Flags().StringSlice("read-only-files", nil, "Files to exclude from modification targets")
	cmd.Flags().String("runtime", "", "Execution runtime: local (default), docker")
	cmd.Flags().String("merge-mode", "local", "Merge mode: local (default), pr (create GitHub PR)")
	cmd.Flags().String("merge-strategy", "", "Merge strategy: batch (default), fifo")
	cmd.Flags().Bool("hierarchical", false, "Enable hierarchical decomposition with feature clusters")
	cmd.Flags().Bool("sandbox", false, "Enable Docker container sandboxing")
	cmd.Flags().String("session-id", "", "Session ID (generated by orchestra go)")

	return cmd
}
