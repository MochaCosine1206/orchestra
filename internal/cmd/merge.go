package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/MochaCosine1206/orchestra/internal/orchestrator"
)

// NewMergeCmd creates the merge subcommand.
// Both batch and FIFO merge paths run post-merge content clobber detection,
// logging merge_content_clobber events for files that shrink by >50%.
func NewMergeCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "merge",
		Short: "Merge completed branches",
		Long:  "Topologically sort and merge completed agent branches back into the base branch.",
		RunE: func(cmd *cobra.Command, args []string) error {
			testCmd, _ := cmd.Flags().GetString("test-cmd")
			testCmdTimeout, _ := cmd.Flags().GetInt("test-cmd-timeout")
			review, _ := cmd.Flags().GetBool("review")
			dryRun, _ := cmd.Flags().GetBool("dry-run")
			baseBranch, _ := cmd.Flags().GetString("base-branch")

			d, err := openDB(cmd)
			if err != nil {
				return fmt.Errorf("opening database: %w", err)
			}
			defer d.Close()

			repoRoot, _ := os.Getwd()
			var runner orchestrator.ClaudeRunner
			if review {
				runner = &orchestrator.ExecRunner{}
			}
			c, err := orchestrator.New(orchestrator.ConductorOpts{
				DB:       d,
				RepoRoot: repoRoot,
				Runner:   runner,
			})
			if err != nil {
				return fmt.Errorf("creating conductor: %w", err)
			}

			var timeout time.Duration
			if testCmdTimeout > 0 {
				timeout = time.Duration(testCmdTimeout) * time.Second
			}
			result, err := c.Merge(cmd.Context(), orchestrator.MergeOpts{
				TestCmd:        testCmd,
				TestCmdTimeout: timeout,
				Review:         review,
				DryRun:         dryRun,
				BaseBranch:     baseBranch,
			})
			if err != nil {
				return fmt.Errorf("merge: %w", err)
			}

			if dryRun {
				cmd.Println("Merge plan:")
				for i, branch := range result.Plan {
					cmd.Printf("  %d. %s\n", i+1, branch)
				}
				return nil
			}

			jsonMode, _ := cmd.Flags().GetBool("json")

			// Print human-readable summary.
			if len(result.Merged) > 0 {
				cmd.Printf("Merged %d branches:\n", len(result.Merged))
				for _, b := range result.Merged {
					cmd.Printf("  + %s\n", b)
				}
			}
			if len(result.Failed) > 0 {
				cmd.Printf("Failed %d branches:\n", len(result.Failed))
				for _, b := range result.Failed {
					cmd.Printf("  x %s\n", b)
				}
			}
			if len(result.Skipped) > 0 {
				cmd.Printf("Skipped %d branches:\n", len(result.Skipped))
				for _, b := range result.Skipped {
					cmd.Printf("  ~ %s\n", b)
				}
			}

			// JSON success output.
			if jsonMode {
				enc := json.NewEncoder(cmd.OutOrStdout())
				enc.SetIndent("", "  ")
				_ = enc.Encode(result)
			}

			// Fix: return error on merge failures instead of silent exit 0.
			if len(result.Failed) > 0 {
				oe := &OrchestraError{
					ExitCode: ExitPartialFailure,
					Category: CategoryPartial,
					Summary:  fmt.Sprintf("%d of %d branches failed to merge", len(result.Failed), len(result.Merged)+len(result.Failed)),
					Detail:   fmt.Sprintf("Failed: %s", strings.Join(result.Failed, ", ")),
					Suggestions: []string{
						"orchestra status --json  # inspect merge failures",
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

	cmd.Flags().String("test-cmd", "", "Test gate command to run per branch")
	cmd.Flags().Int("test-cmd-timeout", 0, "Test command timeout in seconds (default: 300)")
	cmd.Flags().Bool("review", false, "Enable pre-merge code review")
	cmd.Flags().Bool("dry-run", false, "Show merge plan without executing")
	cmd.Flags().String("base-branch", "", "Target branch to merge into (default: auto-detect)")
	cmd.Flags().Bool("json", false, "Machine-readable JSON output")

	return cmd
}
