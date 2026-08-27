package cmd

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/MochaCosine1206/orchestra/internal/orchestrator"
)

// NewAutoCmd creates the auto subcommand (autonomous multi-cycle mode).
func NewAutoCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "auto",
		Short: "Autonomous multi-cycle mode",
		Long:  "Run autonomous discovery-and-execution cycles using audit, markers, and gap sources.",
		RunE: func(cmd *cobra.Command, args []string) error {
			requireVersion, _ := cmd.Flags().GetBool("require-version")
			if err := checkCLIVersion(cmd, requireVersion); err != nil {
				return err
			}

			pause, _ := cmd.Flags().GetBool("pause")
			resume, _ := cmd.Flags().GetBool("resume")
			stop, _ := cmd.Flags().GetBool("stop")

			// Handle control signals (pause/resume/stop) without running Auto loop
			if pause || resume || stop {
				return autoControl(cmd, pause, resume, stop)
			}

			goal, _ := cmd.Flags().GetString("goal")
			sources, _ := cmd.Flags().GetString("sources")
			maxCycles, _ := cmd.Flags().GetInt("max-cycles")
			testCmd, _ := cmd.Flags().GetString("test-cmd")
			maxParallel, _ := cmd.Flags().GetInt("max-parallel")
			interval, _ := cmd.Flags().GetInt("interval")
			review, _ := cmd.Flags().GetBool("review")
			dryRun, _ := cmd.Flags().GetBool("dry-run")

			d, err := openDB(cmd)
			if err != nil {
				return fmt.Errorf("opening database: %w", err)
			}
			defer d.Close()

			repoRoot, _ := os.Getwd()
			runner := &orchestrator.ExecRunner{}
			c, err := orchestrator.New(orchestrator.ConductorOpts{
				DB:       d,
				RepoRoot: repoRoot,
				Runner:   runner,
			})
			if err != nil {
				return fmt.Errorf("creating conductor: %w", err)
			}

			if dryRun {
				cmd.Printf("Dry-run: would start autonomous mode\n")
				cmd.Printf("  Sources: %s\n", sources)
				cmd.Printf("  Max cycles: %d\n", maxCycles)
				if goal != "" {
					cmd.Printf("  Starting goal: %s\n", goal)
				}
				if testCmd != "" {
					cmd.Printf("  Test command: %s\n", testCmd)
				}
				return nil
			}

			jsonMode, _ := cmd.Flags().GetBool("json")

			result, err := c.Auto(cmd.Context(), orchestrator.AutoOpts{
				Goal:        goal,
				Sources:     sources,
				MaxCycles:   maxCycles,
				TestCmd:     testCmd,
				MaxParallel: maxParallel,
				Interval:    interval,
				Review:      review,
			})
			if err != nil {
				oe := &OrchestraError{
					ExitCode:    ExitGeneral,
					Category:    CategoryFatal,
					Summary:     fmt.Sprintf("auto: %s", err.Error()),
					Suggestions: suggestFromRawError(err),
					Cause:       err,
				}
				if jsonMode {
					PrintJSONError(cmd.ErrOrStderr(), oe)
				}
				return oe
			}

			// Print human-readable summary.
			cmd.Printf("Cycles: %d, Goals: %d, Done: %d, Failed: %d\n",
				result.CyclesRun, result.GoalsFound, result.TasksDone, result.TasksFailed)
			cmd.Printf("Stop reason: %s\n", result.StopReason)
			cmd.Printf("Duration: %s\n", result.Duration.Round(1000000000))

			// JSON success output.
			if jsonMode {
				enc := json.NewEncoder(cmd.OutOrStdout())
				enc.SetIndent("", "  ")
				_ = enc.Encode(result)
			}

			// Detect task-level failures.
			if result.TasksFailed > 0 {
				exitCode := ExitPartialFailure
				if result.TasksDone == 0 {
					exitCode = ExitTotalFailure
				}
				oe := &OrchestraError{
					ExitCode: exitCode,
					Category: CategoryPartial,
					Summary:  fmt.Sprintf("auto: %d tasks failed across %d cycles (stop reason: %s)", result.TasksFailed, result.CyclesRun, result.StopReason),
					Suggestions: []string{
						"orchestra status --json  # inspect failed task details",
						"orchestra auto --dry-run  # preview next cycle",
						"orchestra reset  # clean slate and retry",
					},
				}
				if jsonMode {
					PrintJSONError(cmd.ErrOrStderr(), oe)
				}
				return oe
			}

			return nil
		},
	}

	cmd.Flags().StringP("goal", "g", "", "Optional starting goal")
	cmd.Flags().String("sources", "audit,markers,gaps", "Work discovery sources")
	cmd.Flags().Int("max-cycles", 10, "Circuit breaker: max cycles before stopping")
	cmd.Flags().String("test-cmd", "", "Test command per cycle")
	cmd.Flags().Int("max-parallel", 8, "Maximum concurrent agents per cycle")
	cmd.Flags().Int("interval", 15, "Monitor poll interval in seconds")
	cmd.Flags().Bool("review", false, "Enable review gates per cycle")
	cmd.Flags().Bool("dry-run", false, "Show plan without executing")
	cmd.Flags().Bool("pause", false, "Pause a running autonomous session")
	cmd.Flags().Bool("resume", false, "Resume a paused autonomous session")
	cmd.Flags().Bool("stop", false, "Gracefully stop a running autonomous session")
	cmd.Flags().Bool("json", false, "Machine-readable JSON output")
	cmd.Flags().Bool("require-version", false, "Abort if Claude CLI version is below minimum")

	return cmd
}

// autoControl handles pause/resume/stop signals by setting blackboard keys.
func autoControl(cmd *cobra.Command, pause, resume, stop bool) error {
	d, err := openDB(cmd)
	if err != nil {
		return fmt.Errorf("opening database: %w", err)
	}
	defer d.Close()

	ctx := cmd.Context()

	switch {
	case stop:
		if err := d.SetBlackboard(ctx, "auto:stop", "1", "cli"); err != nil {
			return fmt.Errorf("setting stop signal: %w", err)
		}
		// Also clear pause in case both are set
		d.DeleteBlackboard(ctx, "auto:pause")
		cmd.Println("Stop signal sent to autonomous session")
		d.LogEvent(ctx, "auto_stop_requested", "", "", "")

	case pause:
		if err := d.SetBlackboard(ctx, "auto:pause", "1", "cli"); err != nil {
			return fmt.Errorf("setting pause signal: %w", err)
		}
		cmd.Println("Pause signal sent to autonomous session")
		d.LogEvent(ctx, "auto_pause_requested", "", "", "")

	case resume:
		if err := d.DeleteBlackboard(ctx, "auto:pause"); err != nil {
			return fmt.Errorf("clearing pause signal: %w", err)
		}
		cmd.Println("Resume signal sent to autonomous session")
		d.LogEvent(ctx, "auto_resume_requested", "", "", "")
	}

	return nil
}
