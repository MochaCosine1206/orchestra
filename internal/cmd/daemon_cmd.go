package cmd

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	orchestra "github.com/MochaCosine1206/orchestra"
	"github.com/MochaCosine1206/orchestra/internal/config"
	"github.com/MochaCosine1206/orchestra/internal/daemon"
	"github.com/MochaCosine1206/orchestra/internal/priority"
	v2 "github.com/MochaCosine1206/orchestra/internal/priority/v2"
	"github.com/spf13/cobra"
)

// NewDaemonCmd creates the daemon command group with all subcommands.
func NewDaemonCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "daemon",
		Short: "Manage the Orchestra background daemon",
		Long: `The Orchestra daemon runs in the background, monitoring registered projects
and executing scheduled orchestration sessions.

Configuration is read from ~/.config/orchestra/daemon.json (or the path set
by ORCHESTRA_CONFIG_DIR). Example daemon.json:

  {
    "interval": "5m",
    "quiet_hours": { "start": "22:00", "end": "08:00" },
    "max_concurrent": 2,
    "schedules": [
      {
        "project": "/path/to/project",
        "cron": "0 9 * * 1-5",
        "goal": "run tests and fix failures"
      }
    ]
  }

The daemon stores its state in ~/.config/orchestra/daemon.db.`,
	}

	cmd.AddCommand(
		newDaemonRunCmd(),
		newDaemonStartCmd(),
		newDaemonStopCmd(),
		newDaemonStatusCmd(),
		newDaemonInstallCmd(),
		newDaemonUninstallCmd(),
		newDaemonLogsCmd(),
	)

	return cmd
}

// newDaemonRunCmd runs the daemon in the foreground.
func newDaemonRunCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "run",
		Short: "Run the daemon in the foreground",
		Long: `Starts the daemon loop in the current terminal. The daemon acquires a
singleton lock (flock on ~/.config/orchestra/daemon.lock) to prevent
multiple instances. Press Ctrl+C to stop.`,
		RunE: runDaemonRun,
	}
}

func runDaemonRun(cmd *cobra.Command, args []string) error {
	// Acquire singleton lock.
	fd, err := orchestra.AcquireLock()
	if err != nil {
		return err
	}
	defer orchestra.ReleaseLock(fd)

	// Open and initialize the daemon database.
	d, err := orchestra.OpenDaemonDB()
	if err != nil {
		return fmt.Errorf("opening daemon database: %w", err)
	}
	defer d.Close()

	ctx := cmd.Context()
	if err := orchestra.InitDaemonSchema(ctx, d); err != nil {
		return fmt.Errorf("initializing daemon schema: %w", err)
	}

	// Set daemon state to running.
	if _, err := d.ExecContext(ctx,
		"INSERT OR REPLACE INTO daemon_state(key, value, updated_at) VALUES('status', 'running', CURRENT_TIMESTAMP)"); err != nil {
		return fmt.Errorf("setting daemon state: %w", err)
	}
	if _, err := d.ExecContext(ctx,
		"INSERT OR REPLACE INTO daemon_state(key, value, updated_at) VALUES('pid', ?, CURRENT_TIMESTAMP)",
		fmt.Sprintf("%d", os.Getpid())); err != nil {
		return fmt.Errorf("setting daemon PID: %w", err)
	}

	cmd.Println("Orchestra daemon running (PID", os.Getpid(), ")— press Ctrl+C to stop")

	// Create and start the daemon loop
	engine := priority.NewEngine(nil, "") // nil runner = no LLM alignment (fast recalc)
	dmn, err := daemon.New(engine, func(msg string) {
		cmd.Printf("[daemon] %s\n", msg)
	})
	if err != nil {
		return fmt.Errorf("creating daemon: %w", err)
	}
	dmn.DB = d
	dmn.Scheduler = &orchestra.Scheduler{DB: d, MaxConcurrent: dmn.Loader.Config.MaxConcurrent}

	// Wire v2 ScoringEngine with collectors
	configDir, _ := config.GlobalDir()
	prioritiesPath := filepath.Join(configDir, "priorities.md")
	var repoPaths []string
	if reg, err := config.LoadProjects(); err == nil {
		for _, p := range reg.Projects {
			repoPaths = append(repoPaths, p.Path)
		}
	}
	// Default repo for items without a target
	var defaultRepo string
	if len(repoPaths) > 0 {
		defaultRepo = repoPaths[0]
	}
	// Discovery report path — compiled from domain research
	var discoveryReportPath string
	if defaultRepo != "" {
		discoveryReportPath = filepath.Join(defaultRepo, "notes", "discovery-full-report-2026-03-26.md")
	}
	// Notes dir for grant research
	var notesDir string
	if defaultRepo != "" {
		notesDir = filepath.Join(defaultRepo, "notes")
	}

	collectors := []v2.Collector{
		v2.NewUserCollector(prioritiesPath),
		v2.NewBacklogCollector(repoPaths),
		v2.NewDiscoveryCollector(discoveryReportPath),
		v2.NewGrantCollector(notesDir, defaultRepo),
	}
	dmn.ScoringEngine = v2.NewScoringEngine(d, nil, collectors)

	// Run daemon loop with cancellation via signal
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		sig := <-sigCh
		cmd.Println("\nReceived", sig, "— shutting down")
		cancel()
	}()

	if err := dmn.Run(ctx); err != nil && err != context.Canceled {
		cmd.Printf("Daemon error: %v\n", err)
	}

	// Set daemon state to stopped.
	bgCtx := context.Background()
	d.ExecContext(bgCtx,
		"INSERT OR REPLACE INTO daemon_state(key, value, updated_at) VALUES('status', 'stopped', CURRENT_TIMESTAMP)")

	return nil
}

// newDaemonStartCmd starts the daemon via the OS service manager.
func newDaemonStartCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "start",
		Short: "Start the daemon as a background service",
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := orchestra.StartService(); err != nil {
				return fmt.Errorf("starting service: %w", err)
			}
			cmd.Println("Orchestra daemon started")
			return nil
		},
	}
}

// newDaemonStopCmd stops the daemon via the OS service manager.
func newDaemonStopCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "stop",
		Short: "Stop the background daemon service",
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := orchestra.StopService(); err != nil {
				return fmt.Errorf("stopping service: %w", err)
			}
			cmd.Println("Orchestra daemon stopped")
			return nil
		},
	}
}

// newDaemonStatusCmd shows daemon state from daemon.db.
func newDaemonStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show current daemon state",
		RunE:  runDaemonStatus,
	}
}

func runDaemonStatus(cmd *cobra.Command, args []string) error {
	dir, err := config.GlobalDir()
	if err != nil {
		return fmt.Errorf("resolving config dir: %w", err)
	}

	dbPath := filepath.Join(dir, "daemon.db")
	if _, err := os.Stat(dbPath); os.IsNotExist(err) {
		cmd.Println("Daemon has not been initialized (no daemon.db found)")
		return nil
	}

	d, err := orchestra.OpenDaemonDB()
	if err != nil {
		return fmt.Errorf("opening daemon database: %w", err)
	}
	defer d.Close()

	ctx := cmd.Context()
	if err := orchestra.InitDaemonSchema(ctx, d); err != nil {
		return fmt.Errorf("initializing daemon schema: %w", err)
	}

	w := cmd.OutOrStdout()

	// Read all daemon_state entries.
	rows, err := d.QueryContext(ctx, "SELECT key, value, updated_at FROM daemon_state ORDER BY key")
	if err != nil {
		return fmt.Errorf("querying daemon state: %w", err)
	}
	defer rows.Close()

	hasState := false
	for rows.Next() {
		var key, value, updatedAt string
		if err := rows.Scan(&key, &value, &updatedAt); err != nil {
			return fmt.Errorf("scanning daemon state: %w", err)
		}
		if !hasState {
			fmt.Fprintln(w, "Daemon State:")
			hasState = true
		}
		fmt.Fprintf(w, "  %-12s %s  (updated %s)\n", key+":", value, updatedAt)
	}

	if !hasState {
		fmt.Fprintln(w, "Daemon has no recorded state (never started)")
	}

	// Check if the service is installed and running.
	svcStatus, err := orchestra.ServiceStatus()
	if err == nil {
		var label string
		switch svcStatus {
		case 1: // StatusRunning
			label = "running"
		case 2: // StatusStopped
			label = "stopped"
		default:
			label = "unknown"
		}
		fmt.Fprintf(w, "\nOS Service:    %s\n", label)
	}

	// Show recent run history.
	histRows, err := d.QueryContext(ctx,
		"SELECT project_path, status, tasks_done, tasks_failed, started_at FROM run_history ORDER BY started_at DESC LIMIT 5")
	if err == nil {
		defer histRows.Close()
		first := true
		for histRows.Next() {
			var project, status, startedAt string
			var done, failed int
			if err := histRows.Scan(&project, &status, &done, &failed, &startedAt); err != nil {
				continue
			}
			if first {
				fmt.Fprintln(w, "\nRecent Runs:")
				fmt.Fprintf(w, "  %-30s %-10s %5s %5s %s\n", "PROJECT", "STATUS", "DONE", "FAIL", "STARTED")
				first = false
			}
			fmt.Fprintf(w, "  %-30s %-10s %5d %5d %s\n",
				truncate(filepath.Base(project), 30), status, done, failed, startedAt)
		}
	}

	return nil
}

// newDaemonInstallCmd registers the daemon with the OS service manager.
func newDaemonInstallCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "install",
		Short: "Register the daemon with the OS service manager",
		Long: `Installs the Orchestra daemon as a user-level service:
  - macOS: creates a LaunchAgent plist (no sudo required)
  - Linux: creates a user-level systemd unit (no sudo required)`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := orchestra.Install(); err != nil {
				return fmt.Errorf("installing service: %w", err)
			}
			cmd.Println("Orchestra daemon installed as a user-level service")
			cmd.Println("Start with: orchestra daemon start")
			return nil
		},
	}
}

// newDaemonUninstallCmd removes the daemon from the OS service manager.
func newDaemonUninstallCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "uninstall",
		Short: "Remove the daemon from the OS service manager",
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := orchestra.Uninstall(); err != nil {
				return fmt.Errorf("uninstalling service: %w", err)
			}
			cmd.Println("Orchestra daemon removed from OS service manager")
			return nil
		},
	}
}

// newDaemonLogsCmd tails the daemon log file.
func newDaemonLogsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "logs",
		Short: "Tail the daemon log file",
		RunE:  runDaemonLogs,
	}
	cmd.Flags().IntP("lines", "n", 50, "Number of lines to show")
	cmd.Flags().BoolP("follow", "f", false, "Follow the log file (like tail -f)")
	return cmd
}

func runDaemonLogs(cmd *cobra.Command, args []string) error {
	dir, err := config.GlobalDir()
	if err != nil {
		return fmt.Errorf("resolving config dir: %w", err)
	}

	logPath := filepath.Join(dir, "daemon.log")
	if _, err := os.Stat(logPath); os.IsNotExist(err) {
		cmd.Println("No daemon log file found at", logPath)
		return nil
	}

	lines, _ := cmd.Flags().GetInt("lines")
	follow, _ := cmd.Flags().GetBool("follow")

	f, err := os.Open(logPath)
	if err != nil {
		return fmt.Errorf("opening log file: %w", err)
	}
	defer f.Close()

	w := cmd.OutOrStdout()

	if follow {
		// Stream the entire file then follow.
		if _, err := io.Copy(w, f); err != nil {
			return err
		}
		// Follow new lines.
		scanner := bufio.NewScanner(f)
		sigCh := make(chan os.Signal, 1)
		signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

		for {
			select {
			case <-sigCh:
				return nil
			case <-cmd.Context().Done():
				return nil
			default:
				if scanner.Scan() {
					fmt.Fprintln(w, scanner.Text())
				}
			}
		}
	}

	// Read all lines, then print the last N.
	scanner := bufio.NewScanner(f)
	var allLines []string
	for scanner.Scan() {
		allLines = append(allLines, scanner.Text())
	}

	start := 0
	if len(allLines) > lines {
		start = len(allLines) - lines
	}
	for _, line := range allLines[start:] {
		fmt.Fprintln(w, line)
	}

	return nil
}
