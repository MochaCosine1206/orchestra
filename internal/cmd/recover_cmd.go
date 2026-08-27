package cmd

import (
	"fmt"
	"os"
	"time"

	"github.com/spf13/cobra"

	"github.com/MochaCosine1206/orchestra/internal/agent"
	"github.com/MochaCosine1206/orchestra/internal/orchestrator"
)

// NewRecoverCmd creates the recover subcommand for orphan session recovery.
func NewRecoverCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "recover",
		Short: "Recover an orphan conductor session",
		Long: `Check for an orphan conductor session (e.g., after dashboard crash or SIGKILL)
and either report its status, spawn pending tasks, or merge completed branches.

Examples:
  orchestra recover              # Show session status
  orchestra recover --spawn      # Adopt session and spawn pending tasks
  orchestra recover --merge      # Adopt session and merge completed branches
  orchestra recover --reset      # Deactivate session (allows fresh 'orchestra go')`,
		RunE: func(cmd *cobra.Command, args []string) error {
			merge, _ := cmd.Flags().GetBool("merge")
			spawn, _ := cmd.Flags().GetBool("spawn")
			reset, _ := cmd.Flags().GetBool("reset")

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

			result, err := c.Recover(cmd.Context())
			if err != nil {
				return fmt.Errorf("recover: %w", err)
			}

			if result == nil {
				cmd.Println("No orphan session found.")
				return nil
			}

			cmd.Printf("Session:  %s\n", result.SessionID)
			cmd.Printf("Goal:     %s\n", result.Goal)
			cmd.Printf("Tasks:    %d (done=%d, failed=%d, running=%d, pending=%d)\n",
				len(result.TaskIDs), result.DoneCount, result.FailedCount,
				result.RunningCount, result.PendingCount)

			if result.Adopted {
				cmd.Println("Status:   Adopted (this conductor now owns the session)")
			}

			// --reset: deactivate the session so the user can start fresh
			if reset {
				c.Deactivate(cmd.Context())
				cmd.Println("\nSession deactivated. Run 'orchestra go' to start fresh.")
				return nil
			}

			// --spawn: batch-spawn pending tasks
			if spawn && result.PendingCount > 0 {
				if !orchestrator.IsGitRepo(repoRoot) {
					return fmt.Errorf("not a git repository: %s\nRun 'git init && git add -A && git commit -m \"Initial commit\"' first", repoRoot)
				}

				cmd.Printf("\nSpawning agents for %d pending tasks...\n", result.PendingCount)
				spawner := &agent.Spawner{
					DB:       d,
					RepoRoot: repoRoot,
					LogsDir:  repoRoot + "/.orchestra/logs",
					PidsDir:  repoRoot + "/.orchestra/pids",
				}
				results, err := spawner.Batch(cmd.Context(), 5, 2*time.Second, result.TaskIDs)
				if err != nil {
					return fmt.Errorf("batch spawn: %w", err)
				}
				cmd.Printf("Spawned %d agents.\n", len(results))
				if len(results) > 0 {
					cmd.Println("Run 'orchestra dashboard' to monitor progress.")
				}
				return nil
			} else if spawn && result.PendingCount == 0 {
				cmd.Println("\nNo pending tasks to spawn.")
			}

			allTerminal := (result.DoneCount+result.FailedCount) == len(result.TaskIDs) && len(result.TaskIDs) > 0

			if merge && allTerminal && result.DoneCount > 0 {
				cmd.Println("\nMerging completed branches...")
				mergeResult, err := c.Merge(cmd.Context(), orchestrator.MergeOpts{})
				if err != nil {
					return fmt.Errorf("merge: %w", err)
				}
				if mergeResult != nil {
					cmd.Printf("Merged: %d, Failed: %d, Skipped: %d\n",
						len(mergeResult.Merged), len(mergeResult.Failed), len(mergeResult.Skipped))
				}
				// Clean up after merge
				c.Deactivate(cmd.Context())
			} else if merge && !allTerminal {
				cmd.Println("\nCannot merge: not all tasks are in terminal state.")
			} else if merge && result.DoneCount == 0 {
				cmd.Println("\nNothing to merge: no tasks completed successfully.")
			}

			// Print next-step hints when no action flag was given
			if !merge && !spawn && !reset {
				cmd.Println()
				if result.PendingCount > 0 {
					cmd.Println("Next steps:")
					cmd.Println("  orchestra recover --spawn   # Spawn agents for pending tasks")
					cmd.Println("  orchestra recover --reset   # Cancel session and start fresh")
				} else if allTerminal && result.DoneCount > 0 {
					cmd.Println("Next steps:")
					cmd.Println("  orchestra recover --merge   # Merge completed branches")
				} else if allTerminal && result.DoneCount == 0 {
					cmd.Println("All tasks failed. Run 'orchestra recover --reset' to start fresh.")
				}
			}

			return nil
		},
	}

	cmd.Flags().Bool("merge", false, "Trigger merge if all tasks are terminal")
	cmd.Flags().Bool("spawn", false, "Spawn agents for pending tasks")
	cmd.Flags().Bool("reset", false, "Deactivate session (allows fresh 'orchestra go')")

	return cmd
}
