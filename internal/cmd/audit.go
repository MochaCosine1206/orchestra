package cmd

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/MochaCosine1206/orchestra/internal/orchestrator"
)

// NewAuditCmd creates the audit subcommand.
func NewAuditCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "audit",
		Short: "Scan for issues",
		Long:  "Scan codebase for syntax errors, TODO/FIXME markers, open gaps, and test failures.",
		RunE: func(cmd *cobra.Command, args []string) error {
			scope, _ := cmd.Flags().GetString("scope")
			dryRun, _ := cmd.Flags().GetBool("dry-run")

			d, err := openDB(cmd)
			if err != nil {
				return fmt.Errorf("opening database: %w", err)
			}
			defer d.Close()

			repoRoot, _ := os.Getwd()
			c, err := orchestrator.New(orchestrator.ConductorOpts{
				DB:       d,
				RepoRoot: repoRoot,
			})
			if err != nil {
				return fmt.Errorf("creating conductor: %w", err)
			}

			result, err := c.Audit(cmd.Context(), orchestrator.AuditOpts{
				Scope:  orchestrator.AuditScope(scope),
				DryRun: dryRun,
			})
			if err != nil {
				return fmt.Errorf("audit: %w", err)
			}

			// Print findings
			for _, f := range result.Findings {
				if f.File != "" {
					cmd.Printf("[%s] %s: %s\n", f.Category, f.File, f.Message)
				} else {
					cmd.Printf("[%s] %s\n", f.Category, f.Message)
				}
			}

			cmd.Println("\n" + result.Summary)
			cmd.Printf("\nTotal findings: %d\n", len(result.Findings))
			return nil
		},
	}

	cmd.Flags().String("scope", "all", "What to scan: all, tests, code, or gaps")
	cmd.Flags().Bool("dry-run", false, "Report findings without creating tasks")

	return cmd
}
