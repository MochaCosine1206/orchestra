package cmd

import (
	"fmt"

	"github.com/MochaCosine1206/orchestra/internal/orchestrator"
	"github.com/spf13/cobra"
)

// NewCacheCmd creates the cache subcommand for plan cache management.
func NewCacheCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "cache",
		Short: "Manage the plan decomposition cache",
		Long:  `View and manage cached decomposition plans. Use 'orchestra cache clear' to remove entries.`,
	}

	clearCmd := &cobra.Command{
		Use:   "clear",
		Short: "Clear plan cache entries",
		Long: `Clear all plan cache entries, or clear a specific entry by goal text.

Examples:
  orchestra cache clear                         # Clear all cached plans
  orchestra cache clear --goal "Add auth middleware"  # Clear cache for a specific goal`,
		RunE: func(cmd *cobra.Command, args []string) error {
			d, err := openDB(cmd)
			if err != nil {
				return fmt.Errorf("opening database: %w", err)
			}
			defer d.Close()

			goal, _ := cmd.Flags().GetString("goal")
			if goal != "" {
				goalHash := orchestrator.NormalizeGoalHash(goal)
				if err := d.InvalidatePlanCache(cmd.Context(), goalHash); err != nil {
					return fmt.Errorf("clearing cache entry: %w", err)
				}
				cmd.Printf("Cleared cache entry for goal hash %s\n", goalHash[:16])
			} else {
				_, err := d.ExecContext(cmd.Context(), `DELETE FROM plan_cache`)
				if err != nil {
					return fmt.Errorf("clearing all cache entries: %w", err)
				}
				cmd.Println("Cleared all plan cache entries")
			}
			return nil
		},
	}

	clearCmd.Flags().String("goal", "", "Clear cache for a specific goal (computes hash)")

	cmd.AddCommand(clearCmd)
	return cmd
}
