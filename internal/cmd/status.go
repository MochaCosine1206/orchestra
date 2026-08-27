package cmd

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	orchestra "github.com/MochaCosine1206/orchestra"
	"github.com/MochaCosine1206/orchestra/internal/config"
	"github.com/MochaCosine1206/orchestra/internal/db"
	"github.com/MochaCosine1206/orchestra/internal/version"
	"github.com/spf13/cobra"
)

// NewStatusCmd creates the status subcommand.
func NewStatusCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "status",
		Short: "Show orchestra status",
		Long:  "Display current task, agent, and lock status from the coordination database.",
		RunE:  runStatus,
	}

	cmd.Flags().Bool("json", false, "Machine-readable JSON output")
	cmd.Flags().Bool("violations", false, "Show file violation events")

	return cmd
}

func runStatus(cmd *cobra.Command, args []string) error {
	globalMode, _ := cmd.Flags().GetBool("global")
	jsonMode, _ := cmd.Flags().GetBool("json")

	if globalMode {
		return runGlobalStatus(cmd, jsonMode)
	}

	dbPath, _ := cmd.Flags().GetString("db")
	violations, _ := cmd.Flags().GetBool("violations")

	d, err := db.OpenReadOnly(dbPath)
	if err != nil {
		return fmt.Errorf("opening database: %w", err)
	}
	defer d.Close()

	ctx := context.Background()

	if jsonMode {
		return runStatusJSON(cmd, d, ctx, violations)
	}
	return runStatusDisplay(cmd, d, ctx, dbPath, violations)
}

func runGlobalStatus(cmd *cobra.Command, jsonMode bool) error {
	reg, err := config.LoadProjects()
	if err != nil {
		return fmt.Errorf("loading project registry: %w", err)
	}

	if len(reg.Projects) == 0 {
		cmd.Println("No projects registered. Use 'orchestra projects add <path>' to register projects.")
		return nil
	}

	var paths []string
	for _, entry := range reg.Projects {
		paths = append(paths, entry.Path)
	}

	ctx := context.Background()
	status, err := orchestra.GlobalStatusSummary(ctx, paths)
	if err != nil {
		return fmt.Errorf("querying global status: %w", err)
	}

	if jsonMode {
		enc := json.NewEncoder(cmd.OutOrStdout())
		enc.SetIndent("", "  ")
		return enc.Encode(status)
	}

	w := cmd.OutOrStdout()
	fmt.Fprintln(w, "============================================================================")
	fmt.Fprintf(w, "  CLAUDE ORCHESTRA — GLOBAL STATUS   %s\n", version.FullVersion())
	fmt.Fprintln(w, "============================================================================")
	fmt.Fprintf(w, "\nProjects: %d registered\n\n", len(status.Projects))

	fmt.Fprintf(w, "  %-20s %-10s %-8s %-8s %-12s %s\n",
		"PROJECT", "SESSION", "DONE", "PENDING", "AGENTS", "LAST ACTIVITY")
	fmt.Fprintln(w, "  "+strings.Repeat("-", 76))

	for _, p := range status.Projects {
		session := "-"
		if p.ActiveSession != "" {
			session = truncate(p.ActiveSession, 10)
		}
		activity := "-"
		if !p.LastActivity.IsZero() {
			age := time.Since(p.LastActivity)
			if age < time.Hour {
				activity = fmt.Sprintf("%dm ago", int(age.Minutes()))
			} else if age < 24*time.Hour {
				activity = fmt.Sprintf("%dh ago", int(age.Hours()))
			} else {
				activity = p.LastActivity.Format("2006-01-02")
			}
		}

		fmt.Fprintf(w, "  %-20s %-10s %-8d %-8d %-12d %s\n",
			truncate(p.Name, 20), session, p.TasksDone, p.TasksPending,
			p.AgentCount, activity)
	}

	fmt.Fprintln(w)
	fmt.Fprintln(w, "============================================================================")
	return nil
}

func runStatusJSON(cmd *cobra.Command, d *db.DB, ctx context.Context, violations bool) error {
	tasks, err := d.ListTasks(ctx)
	if err != nil {
		return err
	}
	agents, err := d.ListAgents(ctx)
	if err != nil {
		return err
	}
	locks, err := d.ListFileLocks(ctx)
	if err != nil {
		return err
	}
	events, err := d.RecentEvents(ctx, 10)
	if err != nil {
		return err
	}
	summary, err := d.GetStatusSummary(ctx)
	if err != nil {
		return err
	}

	output := db.StatusJSON{
		Tasks:  tasks,
		Agents: agents,
		Locks:  locks,
		Events: events,
	}
	output.Summary.TaskCounts = summary.Tasks.ByStatus
	output.Summary.AgentCounts = summary.Agents.ByStatus
	output.Summary.LockCount = summary.LockCount

	if violations {
		allEvents, err := d.RecentEvents(ctx, 100)
		if err != nil {
			return err
		}
		for _, e := range allEvents {
			if e.EventType == "file_violation" {
				output.Violations = append(output.Violations, e)
			}
		}
	}

	enc := json.NewEncoder(cmd.OutOrStdout())
	enc.SetIndent("", "  ")
	return enc.Encode(output)
}

func runStatusDisplay(cmd *cobra.Command, d *db.DB, ctx context.Context, dbPath string, violations bool) error {
	w := cmd.OutOrStdout()

	fmt.Fprintln(w, "============================================================================")
	fmt.Fprintf(w, "  CLAUDE ORCHESTRA STATUS   %s\n", version.FullVersion())
	fmt.Fprintln(w, "============================================================================")
	fmt.Fprintf(w, "\nDatabase: %s\n\n", dbPath)

	// Section 1: Agents
	fmt.Fprintln(w, "--- AGENT STATUS --------------------------------------------------------")
	agents, err := d.ListAgents(ctx)
	if err != nil {
		return err
	}
	if len(agents) == 0 {
		fmt.Fprintln(w, "  (no agents)")
	} else {
		fmt.Fprintf(w, "  %-10s %-12s %-10s %-12s %-15s %s\n",
			"ID", "ROLE", "STATUS", "TASK", "HEARTBEAT", "CREATED")
		for _, a := range agents {
			hb := "never"
			if a.HeartbeatAt.Valid {
				age := time.Since(a.HeartbeatAt.Time)
				hb = fmt.Sprintf("%d min ago", int(age.Minutes()))
			}
			task := "-"
			if a.CurrentTask.Valid {
				task = truncate(a.CurrentTask.String, 12)
			}
			fmt.Fprintf(w, "  %-10s %-12s %-10s %-12s %-15s %s\n",
				truncate(a.ID, 10), a.Role, a.Status, task, hb,
				a.CreatedAt.Format("15:04:05"))
		}
	}
	fmt.Fprintln(w)

	// Section 2: Tasks
	fmt.Fprintln(w, "--- TASK PROGRESS ------------------------------------------------------")
	tasks, err := d.ListTasks(ctx)
	if err != nil {
		return err
	}

	// Summary counts
	summary, err := d.GetStatusSummary(ctx)
	if err != nil {
		return err
	}
	fmt.Fprintf(w, "  Summary: ")
	var parts []string
	for _, s := range []string{"running", "pending", "assigned", "done", "failed"} {
		if c, ok := summary.Tasks.ByStatus[s]; ok && c > 0 {
			parts = append(parts, fmt.Sprintf("%d %s", c, s))
		}
	}
	if len(parts) == 0 {
		fmt.Fprintln(w, "(no tasks)")
	} else {
		fmt.Fprintln(w, strings.Join(parts, " | "))
	}
	fmt.Fprintln(w)

	if len(tasks) > 0 {
		fmt.Fprintf(w, "  %-8s %-10s %-12s %-14s %-10s %-26s %s\n",
			"ID", "STATUS", "ROLE", "PRIORITY", "AGENT", "TITLE", "ELAPSED")
		limit := len(tasks)
		if limit > 20 {
			limit = 20
		}
		for _, t := range tasks[:limit] {
			elapsed := ""
			if t.StartedAt.Valid && !t.CompletedAt.Valid {
				dur := time.Since(t.StartedAt.Time)
				elapsed = formatDuration(dur)
			}
			agent := "-"
			if t.AssignedTo.Valid {
				agent = truncate(t.AssignedTo.String, 10)
			}
			priDisplay := fmt.Sprintf("%d", t.Priority)
			if t.PriorityLabel.Valid && t.PriorityLabel.String != "" {
				priDisplay = fmt.Sprintf("%d / %s", t.Priority, t.PriorityLabel.String)
			}
			fmt.Fprintf(w, "  %-8s %-10s %-12s %-14s %-10s %-26s %s\n",
				truncate(t.ID, 8), t.Status, t.Role, truncate(priDisplay, 14), agent,
				truncate(t.Title, 26), elapsed)
		}
	}
	fmt.Fprintln(w)

	// Section 3: Locks
	fmt.Fprintln(w, "--- FILE LOCKS ---------------------------------------------------------")
	locks, err := d.ListFileLocks(ctx)
	if err != nil {
		return err
	}
	if len(locks) == 0 {
		fmt.Fprintln(w, "  (no active locks)")
	} else {
		for _, l := range locks {
			age := time.Since(l.LockedAt)
			fmt.Fprintf(w, "  %s  locked_by=%s  task=%s  age=%d min\n",
				l.FilePath,
				nullString(l.LockedBy),
				nullString(l.TaskID),
				int(age.Minutes()))
		}
	}
	fmt.Fprintln(w)

	// Section 4: Recent Events
	fmt.Fprintln(w, "--- RECENT EVENTS ------------------------------------------------------")
	events, err := d.RecentEvents(ctx, 10)
	if err != nil {
		return err
	}
	if len(events) == 0 {
		fmt.Fprintln(w, "  (no events)")
	} else {
		for _, e := range events {
			payload := ""
			if e.Payload.Valid {
				payload = truncate(strings.ReplaceAll(e.Payload.String, "\n", " "), 50)
			}
			fmt.Fprintf(w, "  %s  %-20s  agent=%-8s  task=%-8s  %s\n",
				e.Timestamp.Format("15:04:05"),
				e.EventType,
				nullString(e.AgentID),
				nullString(e.TaskID),
				payload)
		}
	}
	fmt.Fprintln(w)

	// Section 5: File Violations (optional)
	if violations {
		fmt.Fprintln(w, "--- FILE VIOLATIONS ----------------------------------------------------")
		allEvents, err := d.RecentEvents(ctx, 100)
		if err != nil {
			return err
		}
		var violationEvents []db.Event
		for _, e := range allEvents {
			if e.EventType == "file_violation" {
				violationEvents = append(violationEvents, e)
			}
		}
		if len(violationEvents) == 0 {
			fmt.Fprintln(w, "  (no violations)")
		} else {
			for _, e := range violationEvents {
				filePath := ""
				if e.Payload.Valid {
					var p struct {
						FilePath string `json:"file_path"`
					}
					if json.Unmarshal([]byte(e.Payload.String), &p) == nil {
						filePath = p.FilePath
					}
				}
				fmt.Fprintf(w, "  %s  task=%-8s  file=%s\n",
					e.Timestamp.Format("15:04:05"),
					nullString(e.TaskID),
					filePath)
			}
		}
		fmt.Fprintln(w)
	}

	// Section 6: Process Status
	fmt.Fprintln(w, "--- PROCESS STATUS -----------------------------------------------------")
	printProcessStatus(w, d, ctx)

	fmt.Fprintln(w, "============================================================================")
	return nil
}

func printProcessStatus(w io.Writer, d *db.DB, ctx context.Context) {
	pidFile := filepath.Join(".orchestra", "pids", "monitor.pid")
	data, err := os.ReadFile(pidFile)
	if err != nil {
		fmt.Fprintln(w, "  Monitor:  STOPPED")
	} else {
		pidStr := strings.TrimSpace(string(data))
		pid, err := strconv.Atoi(pidStr)
		if err != nil {
			fmt.Fprintf(w, "  Monitor:  INVALID PID (%s)\n", pidStr)
		} else if isProcessAlive(pid) {
			fmt.Fprintf(w, "  Monitor:  RUNNING (PID: %d)\n", pid)
		} else {
			fmt.Fprintf(w, "  Monitor:  DEAD (stale PID: %d)\n", pid)
		}
	}

	hb, _ := d.GetBlackboardValue(ctx, "monitor:heartbeat")
	if hb == "" {
		hb = "never"
	}
	fmt.Fprintf(w, "  Last heartbeat: %s\n\n", hb)
}

func isProcessAlive(pid int) bool {
	return syscall.Kill(pid, 0) == nil
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	if max <= 3 {
		return s[:max]
	}
	return s[:max-3] + "..."
}

func formatDuration(d time.Duration) string {
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
	if d < time.Hour {
		return fmt.Sprintf("%dm %ds", int(d.Minutes()), int(d.Seconds())%60)
	}
	return fmt.Sprintf("%dh %dm", int(d.Hours()), int(d.Minutes())%60)
}

func nullString(ns sql.NullString) string {
	if ns.Valid {
		return ns.String
	}
	return "-"
}
