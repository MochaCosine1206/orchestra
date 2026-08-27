package cmd

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/MochaCosine1206/orchestra/internal/orchestrator"
)

// NewTokensCmd creates the tokens subcommand.
func NewTokensCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "tokens",
		Short: "Show token usage",
		Long:  "Parse agent logs and report token usage for the current session.",
		RunE: func(cmd *cobra.Command, args []string) error {
			jsonMode, _ := cmd.Flags().GetBool("json")

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

			summary, err := c.Tokens(cmd.Context())
			if err != nil {
				return fmt.Errorf("tokens: %w", err)
			}

			if jsonMode {
				enc := json.NewEncoder(cmd.OutOrStdout())
				enc.SetIndent("", "  ")
				return enc.Encode(summary)
			}

			cmd.Print(orchestrator.FormatTokenSummary(summary))
			return nil
		},
	}

	cmd.Flags().Bool("json", false, "Output as JSON")

	return cmd
}
