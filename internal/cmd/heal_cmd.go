package cmd

import (
	"context"
	"fmt"

	"github.com/MochaCosine1206/orchestra/internal/healing"
	"github.com/spf13/cobra"
)

// NewHealCmd creates the heal subcommand with sub-commands for healing diagnostics.
func NewHealCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "heal",
		Short: "Healing system diagnostics",
		Long: `View healing budget and history for the current session.

Sub-commands:
  status   Show healing budget remaining for current session
  history  Show recent healing fixes from healing_log`,
	}

	cmd.AddCommand(
		newHealStatusCmd(),
		newHealHistoryCmd(),
	)

	return cmd
}

func newHealStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show healing budget remaining for current session",
		RunE: func(cmd *cobra.Command, args []string) error {
			d, err := openDB(cmd)
			if err != nil {
				return fmt.Errorf("opening database: %w", err)
			}
			defer d.Close()

			ctx := context.Background()
			sessionID, _ := d.GetBlackboardValue(ctx, "conductor:session_id")
			if sessionID == "" {
				cmd.Println("No active session found.")
				return nil
			}

			count, err := d.GetHealingLogCount(ctx, sessionID)
			if err != nil {
				return fmt.Errorf("querying healing log: %w", err)
			}

			remaining := healing.MaxFixesPerSession - count
			if remaining < 0 {
				remaining = 0
			}

			cmd.Printf("Session:   %s\n", sessionID)
			cmd.Printf("Fixes used:     %d / %d\n", count, healing.MaxFixesPerSession)
			cmd.Printf("Budget remaining: %d\n", remaining)
			return nil
		},
	}
}

func newHealHistoryCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "history",
		Short: "Show recent healing fixes from healing_log",
		RunE: func(cmd *cobra.Command, args []string) error {
			d, err := openDB(cmd)
			if err != nil {
				return fmt.Errorf("opening database: %w", err)
			}
			defer d.Close()

			ctx := context.Background()
			sessionID, _ := d.GetBlackboardValue(ctx, "conductor:session_id")
			if sessionID == "" {
				cmd.Println("No active session found.")
				return nil
			}

			logs, err := d.ListHealingLogForSession(ctx, sessionID)
			if err != nil {
				return fmt.Errorf("querying healing log: %w", err)
			}

			if len(logs) == 0 {
				cmd.Println("No healing fixes recorded for this session.")
				return nil
			}

			cmd.Printf("%-20s  %-12s  %-20s  %-7s  %-10s\n", "ID", "TASK", "ERROR TYPE", "SUCCESS", "ROLLED BACK")
			for _, h := range logs {
				taskID := ""
				if h.TaskID.Valid {
					taskID = h.TaskID.String
				}
				errType := ""
				if h.ErrorType.Valid {
					errType = h.ErrorType.String
				}
				success := "no"
				if h.Success != 0 {
					success = "yes"
				}
				rolledBack := "no"
				if h.RolledBack != 0 {
					rolledBack = "yes"
				}
				cmd.Printf("%-20s  %-12s  %-20s  %-7s  %-10s\n", h.ID, taskID, errType, success, rolledBack)
			}
			return nil
		},
	}
}
