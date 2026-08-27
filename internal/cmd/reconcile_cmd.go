package cmd

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/MochaCosine1206/orchestra/internal/orchestrator"
)

// NewReconcileCmd creates the reconcile subcommand for post-session analysis.
func NewReconcileCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "reconcile",
		Short: "Post-session reconciliation and gap analysis",
		Long: `Scan for orphan worktrees, salvage uncommitted changes, score goal alignment,
identify gaps, and optionally generate follow-up tasks.

Runs automatically after 'orchestra go' unless --reconcile=false is set.
Can also be run standalone to reconcile a previous session.

Examples:
  orchestra reconcile              # Reconcile current/last session
  orchestra reconcile --dry-run    # Report only, no side effects
  orchestra reconcile --skip-llm   # Skip LLM analysis, use heuristic score`,
		RunE: func(cmd *cobra.Command, args []string) error {
			dryRun, _ := cmd.Flags().GetBool("dry-run")
			skipLLM, _ := cmd.Flags().GetBool("skip-llm")
			session, _ := cmd.Flags().GetString("session")

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

			result, err := c.Reconcile(cmd.Context(), orchestrator.ReconcileOpts{
				DryRun:    dryRun,
				SkipLLM:   skipLLM,
				SessionID: session,
			})
			if err != nil {
				return fmt.Errorf("reconcile: %w", err)
			}

			// Print the report
			cmd.Print(result.Report)

			// Summary line
			if len(result.OrphanWorktrees) > 0 {
				cmd.Printf("Orphan worktrees: %d (salvaged: %d, cleaned: %d)\n",
					len(result.OrphanWorktrees), len(result.SalvagedWorktrees), len(result.CleanedWorktrees))
			}
			if len(result.FollowUpTaskIDs) > 0 {
				cmd.Printf("Follow-up tasks created: %d\n", len(result.FollowUpTaskIDs))
			}

			return nil
		},
	}

	cmd.Flags().Bool("dry-run", false, "Report only, no side effects")
	cmd.Flags().Bool("skip-llm", false, "Skip LLM goal alignment analysis")
	cmd.Flags().String("session", "", "Reconcile a specific session ID")

	return cmd
}
