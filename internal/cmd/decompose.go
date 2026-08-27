package cmd

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/MochaCosine1206/orchestra/internal/orchestrator"
)

// NewDecomposeCmd creates the decompose subcommand.
func NewDecomposeCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "decompose",
		Short: "Decompose a goal into tasks",
		Long:  "Use an architect agent to break a goal into focused, independent tasks.",
		RunE: func(cmd *cobra.Command, args []string) error {
			goal, _ := cmd.Flags().GetString("goal")
			maxTasks, _ := cmd.Flags().GetInt("max-tasks")
			maxFilesPerTask, _ := cmd.Flags().GetInt("max-files-per-task")
			critique, _ := cmd.Flags().GetString("critique")
			dryRun, _ := cmd.Flags().GetBool("dry-run")
			clarify, _ := cmd.Flags().GetBool("clarify")
			clarifyModeStr, _ := cmd.Flags().GetString("clarify-mode")
			clarifyMode := parseClarifyMode(clarifyModeStr)

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

			result, err := c.Decompose(cmd.Context(), orchestrator.DecomposeOpts{
				Goal:            goal,
				MaxTasks:        maxTasks,
				MaxFilesPerTask: maxFilesPerTask,
				Critique:        critique,
				DryRun:          dryRun,
				Clarify:         clarify,
				ClarifyMode:     clarifyMode,
			})
			if err != nil {
				return fmt.Errorf("decompose: %w", err)
			}

			cmd.Printf("Session: %s\n", result.SessionID)
			if dryRun {
				cmd.Printf("Dry-run: %d tasks (not created):\n", len(result.Tasks))
			} else {
				cmd.Printf("Created %d tasks:\n", len(result.Tasks))
			}
			for _, t := range result.Tasks {
				cmd.Printf("  %s [%s] %s (priority=%d)\n", t.ID, t.Role, t.Title, t.Priority)
				if len(t.DependsOn) > 0 {
					cmd.Printf("    depends_on: %v\n", t.DependsOn)
				}
			}
			return nil
		},
	}

	cmd.Flags().StringP("goal", "g", "", "Goal description (required)")
	cmd.Flags().Int("max-tasks", 8, "Maximum number of tasks to create")
	cmd.Flags().Int("max-files-per-task", 25, "Maximum files per task (0 = unlimited)")
	cmd.Flags().String("critique", "", "Reviewer feedback for re-decomposition")
	cmd.Flags().Bool("dry-run", false, "Show decomposition plan without creating tasks")
	cmd.Flags().Bool("clarify", false, "Enable goal clarification before decomposition")
	cmd.Flags().String("clarify-mode", "auto", "Clarification mode: auto (use defaults), cli (interactive)")
	_ = cmd.MarkFlagRequired("goal")

	return cmd
}
