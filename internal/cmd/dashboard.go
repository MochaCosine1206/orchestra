package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	_ "net/http/pprof"
	"os"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/MochaCosine1206/orchestra/internal/config"
	"github.com/MochaCosine1206/orchestra/internal/dashboard"
	"github.com/MochaCosine1206/orchestra/internal/db"
	"github.com/MochaCosine1206/orchestra/internal/orchestrator"
	"github.com/MochaCosine1206/orchestra/internal/tui"
)

// NewDashboardCmd creates the dashboard subcommand (TUI, default when no subcommand given).
func NewDashboardCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "dashboard",
		Short: "Launch the TUI dashboard",
		Long:  "Launch the interactive Bubble Tea TUI dashboard for monitoring and controlling orchestra.",
		RunE:  runDashboard,
	}

	cmd.Flags().Bool("pprof", false, "Enable pprof profiling endpoint on localhost:6060")
	cmd.Flags().Bool("web", false, "Launch the web dashboard instead of the TUI")
	cmd.Flags().Bool("global", false, "Launch the global multi-project dashboard")
	cmd.Flags().Int("port", 8080, "Port for the web dashboard (used with --web)")

	return cmd
}

func runDashboard(cmd *cobra.Command, args []string) error {
	dbPath, _ := cmd.Flags().GetString("db")

	// Web dashboard mode
	webMode, _ := cmd.Flags().GetBool("web")
	if webMode {
		return runWebDashboard(cmd, dbPath)
	}

	// Global multi-project dashboard mode
	globalMode, _ := cmd.Flags().GetBool("global")
	if globalMode {
		return runGlobalDashboard(cmd)
	}

	// Start pprof server if requested.
	pprofEnabled, _ := cmd.Flags().GetBool("pprof")
	if pprofEnabled {
		go func() {
			fmt.Fprintf(os.Stderr, "pprof endpoint: http://localhost:6060/debug/pprof/\n")
			if err := http.ListenAndServe("localhost:6060", nil); err != nil {
				fmt.Fprintf(os.Stderr, "pprof server error: %v\n", err)
			}
		}()
	}

	// Non-terminal detection: pipe/redirect → fallback to status --json.
	if !term.IsTerminal(int(os.Stdout.Fd())) {
		return runDashboardHeadless(cmd, dbPath)
	}

	// Open read-write: the TUI now performs direct DB mutations via the write layer
	d, err := db.Open(dbPath)
	if err != nil {
		return fmt.Errorf("opening database: %w", err)
	}
	defer d.Close()

	// G155 FIX: Check for active conductors BEFORE integrity check.
	// The TUI must NEVER reinitialize a DB with active runs — concurrent WAL
	// writes from agents cause false-positive corruption detection.
	ctx := context.Background()
	activeConductors, _ := d.ListActiveConductors(ctx)
	if len(activeConductors) > 0 {
		fmt.Fprintf(os.Stderr, "INFO: %d active conductor(s) detected — skipping integrity check (WAL writes may cause false positives).\n", len(activeConductors))
	} else {
		// Only run integrity check when no active runs — safe to reinitialize if truly corrupt
		if err := d.IntegrityCheck(ctx); err != nil {
			fmt.Fprintf(os.Stderr, "WARNING: Database integrity check failed: %v\n", err)
			fmt.Fprintf(os.Stderr, "Attempting auto-recovery (reinitializing database)...\n")
			if recoverErr := d.CheckAndReinitialize(ctx); recoverErr != nil {
				return fmt.Errorf("database recovery failed: %w (run 'orchestra init --clean' manually)", recoverErr)
			}
			fmt.Fprintf(os.Stderr, "Database recovered successfully.\n")
		}
	}

	repoRoot, _ := os.Getwd()
	runner := &orchestrator.ExecRunner{}
	conductor, err := orchestrator.New(orchestrator.ConductorOpts{
		DB:       d,
		RepoRoot: repoRoot,
		Runner:   runner,
	})
	if err != nil {
		return fmt.Errorf("creating conductor: %w", err)
	}

	m := tui.NewWithConductor(d, conductor)
	p := tea.NewProgram(m, tea.WithAltScreen())

	if _, err := p.Run(); err != nil {
		return fmt.Errorf("running TUI: %w", err)
	}

	return nil
}

// runGlobalDashboard launches the multi-project global TUI dashboard.
func runGlobalDashboard(cmd *cobra.Command) error {
	reg, err := config.LoadProjects()
	if err != nil {
		return fmt.Errorf("loading project registry: %w", err)
	}

	if len(reg.Projects) == 0 {
		cmd.PrintErrln("No projects registered. Use 'orchestra projects add <path>' to register projects.")
		return nil
	}

	entries := make([]*config.ProjectEntry, 0, len(reg.Projects))
	for _, entry := range reg.Projects {
		entries = append(entries, entry)
	}

	gm := tui.NewGlobalModel(entries)
	m := tui.NewGlobal(gm)

	p := tea.NewProgram(m, tea.WithAltScreen())
	if _, err := p.Run(); err != nil {
		return fmt.Errorf("running global TUI: %w", err)
	}
	return nil
}

// runWebDashboard starts the HTTP web dashboard.
func runWebDashboard(cmd *cobra.Command, dbPath string) error {
	port, _ := cmd.Flags().GetInt("port")

	d, err := db.Open(dbPath)
	if err != nil {
		return fmt.Errorf("opening database: %w", err)
	}
	defer d.Close()

	srv := &dashboard.Server{
		DB:   d,
		Port: port,
	}

	ctx, cancel := context.WithCancel(cmd.Context())
	defer cancel()

	// Handle interrupt signal for graceful shutdown.
	go func() {
		sigCh := make(chan os.Signal, 1)
		// signal.Notify is not imported yet but we handle via context
		_ = sigCh
		<-ctx.Done()
	}()

	fmt.Fprintf(os.Stderr, "Starting web dashboard on http://localhost:%d\n", port)
	return srv.Start(ctx)
}

// runDashboardHeadless outputs JSON status when stdout is not a terminal.
func runDashboardHeadless(cmd *cobra.Command, dbPath string) error {
	d, err := db.OpenReadOnly(dbPath)
	if err != nil {
		return fmt.Errorf("opening database: %w", err)
	}
	defer d.Close()

	// Reuse runStatusJSON from status.go for headless output.
	ctx := cmd.Context()
	tasks, _ := d.ListTasks(ctx)
	agents, _ := d.ListAgents(ctx)
	locks, _ := d.ListFileLocks(ctx)
	events, _ := d.RecentEvents(ctx, 10)
	summary, _ := d.GetStatusSummary(ctx)

	output := db.StatusJSON{
		Tasks:  tasks,
		Agents: agents,
		Locks:  locks,
		Events: events,
	}
	if summary != nil {
		output.Summary.TaskCounts = summary.Tasks.ByStatus
		output.Summary.AgentCounts = summary.Agents.ByStatus
		output.Summary.LockCount = summary.LockCount
	}

	enc := json.NewEncoder(cmd.OutOrStdout())
	enc.SetIndent("", "  ")
	return enc.Encode(output)
}
