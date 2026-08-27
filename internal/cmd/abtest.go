package cmd

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/MochaCosine1206/orchestra/internal/orchestrator"
)

// NewAbTestCmd creates the ab-test subcommand.
func NewAbTestCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "ab-test",
		Short: "Compare multi-agent vs single-agent",
		Long:  "Run A/B comparison: multi-agent orchestra (Arm A) vs single opus agent (Arm B).",
		RunE: func(cmd *cobra.Command, args []string) error {
			goal, _ := cmd.Flags().GetString("goal")
			testCmd, _ := cmd.Flags().GetString("test-cmd")
			runs, _ := cmd.Flags().GetInt("runs")
			routing, _ := cmd.Flags().GetString("routing")
			dryRun, _ := cmd.Flags().GetBool("dry-run")
			clarify, _ := cmd.Flags().GetBool("clarify")
			mcpTest, _ := cmd.Flags().GetBool("mcp-test")
			reviewTest, _ := cmd.Flags().GetBool("review-test")
			structuredReview, _ := cmd.Flags().GetBool("structured-review")
			cascade, _ := cmd.Flags().GetBool("cascade")
			testCmdTimeout, _ := cmd.Flags().GetInt("test-cmd-timeout")

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

			result, err := c.ABTest(cmd.Context(), orchestrator.ABTestOpts{
				Goal:             goal,
				TestCmd:          testCmd,
				Runs:             runs,
				Routing:          routing,
				DryRun:           dryRun,
				Clarify:          clarify,
				MCPTest:          mcpTest,
				ReviewTest:       reviewTest,
				StructuredReview: structuredReview,
				Cascade:          cascade,
				TestCmdTimeout:   testCmdTimeout,
			})
			if err != nil {
				return fmt.Errorf("ab-test: %w", err)
			}

			cmd.Print(orchestrator.FormatABTestResult(result))

			return nil
		},
	}

	cmd.Flags().StringP("goal", "g", "", "Goal description (required)")
	cmd.Flags().String("test-cmd", "", "Test command (required)")
	cmd.Flags().Int("runs", 1, "Number of repetitions")
	cmd.Flags().String("routing", "", "Model routing strategy for Arm B")
	cmd.Flags().Bool("dry-run", false, "Show plan without executing")
	cmd.Flags().Bool("clarify", false, "A/B test: Arm A = no clarify, Arm B = with clarify")
	cmd.Flags().Bool("mcp-test", false, "A/B test: Arm A = with MCP tools, Arm B = without MCP")
	cmd.Flags().Bool("review-test", false, "A/B test: Arm A = default review, Arm B = spec-diff review")
	cmd.Flags().Bool("structured-review", false, "A/B test: Arm A = default review, Arm B = structured JSON review")
	cmd.Flags().Bool("cascade", false, "A/B test: Arm A = Go(), Arm B = GoCascade()")
	cmd.Flags().Int("test-cmd-timeout", 0, "Test command timeout in seconds (default: 300)")
	_ = cmd.MarkFlagRequired("goal")
	_ = cmd.MarkFlagRequired("test-cmd")

	return cmd
}
