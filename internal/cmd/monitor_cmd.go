package cmd

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/MochaCosine1206/orchestra/internal/agent"
	"github.com/MochaCosine1206/orchestra/internal/db"
	"github.com/MochaCosine1206/orchestra/internal/monitor"
)

// NewMonitorCmd creates the monitor subcommand tree.
func NewMonitorCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "monitor",
		Short: "Manage the agent lifecycle monitor",
		Long:  "Start, stop, or run a single cycle of the background agent monitor.",
	}

	cmd.AddCommand(
		newMonitorRunOnceCmd(),
		newMonitorStartCmd(),
		newMonitorStatusCmd(),
	)

	return cmd
}

func newMonitor(cmd *cobra.Command, d *db.DB) *monitor.Monitor {
	repoRoot, _ := os.Getwd()
	logsDir := filepath.Join(repoRoot, ".orchestra", "logs")
	pidsDir := filepath.Join(repoRoot, ".orchestra", "pids")

	spawner := &agent.Spawner{
		DB:       d,
		RepoRoot: repoRoot,
		LogsDir:  logsDir,
		PidsDir:  pidsDir,
	}

	interval, _ := cmd.Flags().GetInt("interval")
	if interval <= 0 {
		interval = 15
	}

	return &monitor.Monitor{
		DB:       d,
		Spawner:  spawner,
		RepoRoot: repoRoot,
		LogsDir:  logsDir,
		PidsDir:  pidsDir,
		Interval: time.Duration(interval) * time.Second,
		Log: func(msg string) {
			fmt.Fprintf(os.Stderr, "[monitor] %s\n", msg)
		},
	}
}

func newMonitorRunOnceCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "run-once",
		Short: "Run a single monitor cycle (for debugging)",
		RunE: func(cmd *cobra.Command, args []string) error {
			d, err := openDB(cmd)
			if err != nil {
				return fmt.Errorf("opening database: %w", err)
			}
			defer d.Close()

			mon := newMonitor(cmd, d)
			stats, err := mon.RunOnce(context.Background())
			if err != nil {
				return fmt.Errorf("monitor run-once: %w", err)
			}

			cmd.Printf("Monitor cycle completed in %s\n", stats.Duration)
			cmd.Printf("  Checked:   %d\n", stats.Checked)
			cmd.Printf("  Completed: %d\n", stats.Completed)
			cmd.Printf("  Failed:    %d\n", stats.Failed)
			cmd.Printf("  Retried:   %d\n", stats.Retried)
			cmd.Printf("  Stuck:     %d\n", stats.StuckWarned)
			cmd.Printf("  Waiters:   %d\n", stats.WaitersResumed)
			cmd.Printf("  Timed out: %d\n", stats.TimedOut)
			cmd.Printf("  Spawned:   %d\n", stats.AutoSpawned)
			return nil
		},
	}

	cmd.Flags().Int("interval", 15, "Monitor interval in seconds")

	return cmd
}

func newMonitorStartCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "start",
		Short: "Start the monitor as a foreground process",
		RunE: func(cmd *cobra.Command, args []string) error {
			d, err := openDB(cmd)
			if err != nil {
				return fmt.Errorf("opening database: %w", err)
			}
			defer d.Close()

			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()

			mon := newMonitor(cmd, d)
			if err := mon.Start(ctx); err != nil {
				return fmt.Errorf("starting monitor: %w", err)
			}

			cmd.Println("Monitor running. Press Ctrl+C to stop.")

			// Wait for signal
			sigCh := make(chan os.Signal, 1)
			signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
			<-sigCh

			cmd.Println("Shutting down monitor...")
			mon.Stop()
			return nil
		},
	}

	cmd.Flags().Int("interval", 15, "Monitor interval in seconds")

	return cmd
}

func newMonitorStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Check monitor status from blackboard",
		RunE: func(cmd *cobra.Command, args []string) error {
			d, err := openDB(cmd)
			if err != nil {
				return fmt.Errorf("opening database: %w", err)
			}
			defer d.Close()

			ctx := context.Background()
			heartbeat, _ := d.GetBlackboardValue(ctx, "monitor:heartbeat")
			if heartbeat == "" {
				cmd.Println("Monitor: STOPPED (no heartbeat)")
			} else {
				cmd.Printf("Monitor: RUNNING (last heartbeat: %s)\n", heartbeat)
			}
			return nil
		},
	}
}
