package cmd

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	orchestra "github.com/MochaCosine1206/orchestra"
	"github.com/MochaCosine1206/orchestra/internal/db"
	"github.com/spf13/cobra"
)

// NewQueueCmd creates the queue command group with subcommands for managing
// the daemon's priority queue.
func NewQueueCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "queue",
		Short: "Manage the daemon priority queue",
		Long:  `View, add, cancel, promote, demote entries in the daemon's priority queue, and view run history.`,
	}

	cmd.AddCommand(
		newQueueListCmd(),
		newQueueAddCmd(),
		newQueueCancelCmd(),
		newQueuePromoteCmd(),
		newQueueDemoteCmd(),
		newQueueHistoryCmd(),
	)

	return cmd
}

// openDaemonDBForQueue opens the daemon database, initializes schema, and returns it.
func openDaemonDBForQueue(ctx context.Context) (*db.DB, error) {
	d, err := orchestra.OpenDaemonDB()
	if err != nil {
		return nil, fmt.Errorf("opening daemon database: %w", err)
	}
	if err := orchestra.InitDaemonSchema(ctx, d); err != nil {
		d.Close()
		return nil, fmt.Errorf("initializing daemon schema: %w", err)
	}
	return d, nil
}

func newQueueListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "Show priority queue sorted by effective priority",
		RunE:  runQueueList,
	}
}

func runQueueList(cmd *cobra.Command, args []string) error {
	ctx := cmd.Context()
	d, err := openDaemonDBForQueue(ctx)
	if err != nil {
		return err
	}
	defer d.Close()

	rows, err := d.QueryContext(ctx,
		`SELECT id, project_path, task_id, effective_priority, computed_at
		 FROM priority_queue
		 ORDER BY effective_priority DESC`)
	if err != nil {
		return fmt.Errorf("querying priority queue: %w", err)
	}
	defer rows.Close()

	w := cmd.OutOrStdout()
	fmt.Fprintf(w, "%-25s %-30s %-20s %10s %s\n", "ID", "PROJECT", "TASK", "PRIORITY", "COMPUTED")
	fmt.Fprintf(w, "%-25s %-30s %-20s %10s %s\n", "---", "---", "---", "---", "---")

	count := 0
	for rows.Next() {
		var id, project, taskID, computedAt string
		var priority float64
		if err := rows.Scan(&id, &project, &taskID, &priority, &computedAt); err != nil {
			return fmt.Errorf("scanning row: %w", err)
		}
		fmt.Fprintf(w, "%-25s %-30s %-20s %10.1f %s\n",
			truncateStr(id, 25),
			truncateStr(filepath.Base(project), 30),
			truncateStr(taskID, 20),
			priority,
			computedAt)
		count++
	}

	if count == 0 {
		fmt.Fprintln(w, "(queue is empty)")
	}

	return nil
}

func newQueueAddCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "add <project-path> <goal>",
		Short: "Enqueue a project+goal with priority",
		Args:  cobra.ExactArgs(2),
		RunE:  runQueueAdd,
	}
	cmd.Flags().Float64P("priority", "p", 50, "Initial effective priority")
	return cmd
}

func runQueueAdd(cmd *cobra.Command, args []string) error {
	projectPath := args[0]
	goal := args[1]
	priority, _ := cmd.Flags().GetFloat64("priority")

	// Resolve to absolute path.
	absPath, err := filepath.Abs(projectPath)
	if err != nil {
		return fmt.Errorf("resolving path: %w", err)
	}
	if _, err := os.Stat(absPath); os.IsNotExist(err) {
		return fmt.Errorf("project path does not exist: %s", absPath)
	}

	ctx := cmd.Context()
	d, err := openDaemonDBForQueue(ctx)
	if err != nil {
		return err
	}
	defer d.Close()

	id := db.GenID("pq")
	_, err = d.ExecContext(ctx,
		`INSERT INTO priority_queue (id, project_path, task_id, effective_priority)
		 VALUES (?, ?, ?, ?)`,
		id, absPath, goal, priority)
	if err != nil {
		return fmt.Errorf("inserting queue entry: %w", err)
	}

	cmd.Printf("Enqueued %s (priority %.1f): %s\n", id, priority, goal)
	return nil
}

func newQueueCancelCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "cancel <queue-id>",
		Short: "Remove an entry from the priority queue",
		Args:  cobra.ExactArgs(1),
		RunE:  runQueueCancel,
	}
}

func runQueueCancel(cmd *cobra.Command, args []string) error {
	queueID := args[0]

	ctx := cmd.Context()
	d, err := openDaemonDBForQueue(ctx)
	if err != nil {
		return err
	}
	defer d.Close()

	res, err := d.ExecContext(ctx,
		`DELETE FROM priority_queue WHERE id = ?`, queueID)
	if err != nil {
		return fmt.Errorf("deleting queue entry: %w", err)
	}

	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("no queue entry found with id %q", queueID)
	}

	cmd.Printf("Cancelled queue entry %s\n", queueID)
	return nil
}

func newQueuePromoteCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "promote <queue-id>",
		Short: "Boost priority by +50",
		Args:  cobra.ExactArgs(1),
		RunE:  runQueuePromote,
	}
}

func runQueuePromote(cmd *cobra.Command, args []string) error {
	return adjustPriority(cmd, args[0], 50)
}

func newQueueDemoteCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "demote <queue-id>",
		Short: "Reduce priority by -50",
		Args:  cobra.ExactArgs(1),
		RunE:  runQueueDemote,
	}
}

func runQueueDemote(cmd *cobra.Command, args []string) error {
	return adjustPriority(cmd, args[0], -50)
}

func adjustPriority(cmd *cobra.Command, queueID string, delta float64) error {
	ctx := cmd.Context()
	d, err := openDaemonDBForQueue(ctx)
	if err != nil {
		return err
	}
	defer d.Close()

	res, err := d.ExecContext(ctx,
		`UPDATE priority_queue
		 SET effective_priority = effective_priority + ?, computed_at = CURRENT_TIMESTAMP
		 WHERE id = ?`,
		delta, queueID)
	if err != nil {
		return fmt.Errorf("adjusting priority: %w", err)
	}

	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("no queue entry found with id %q", queueID)
	}

	// Read back the new priority.
	var newPriority float64
	err = d.QueryRowContext(ctx,
		`SELECT effective_priority FROM priority_queue WHERE id = ?`, queueID).Scan(&newPriority)
	if err != nil {
		return fmt.Errorf("reading updated priority: %w", err)
	}

	action := "Promoted"
	if delta < 0 {
		action = "Demoted"
	}
	cmd.Printf("%s %s: priority now %.1f (%+.0f)\n", action, queueID, newPriority, delta)
	return nil
}

func newQueueHistoryCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "history",
		Short: "Show run history with status, duration, and task counts",
		RunE:  runQueueHistory,
	}
	cmd.Flags().IntP("limit", "n", 20, "Number of history entries to show")
	return cmd
}

func runQueueHistory(cmd *cobra.Command, args []string) error {
	limit, _ := cmd.Flags().GetInt("limit")
	ctx := cmd.Context()
	d, err := openDaemonDBForQueue(ctx)
	if err != nil {
		return err
	}
	defer d.Close()

	rows, err := d.QueryContext(ctx,
		`SELECT project_path, session_id, status, tasks_done, tasks_failed, started_at, finished_at
		 FROM run_history
		 ORDER BY started_at DESC
		 LIMIT ?`, limit)
	if err != nil {
		return fmt.Errorf("querying run history: %w", err)
	}
	defer rows.Close()

	w := cmd.OutOrStdout()
	fmt.Fprintf(w, "%-25s %-10s %5s %5s %10s %s\n", "PROJECT", "STATUS", "DONE", "FAIL", "DURATION", "STARTED")
	fmt.Fprintf(w, "%-25s %-10s %5s %5s %10s %s\n", "---", "---", "---", "---", "---", "---")

	count := 0
	for rows.Next() {
		var project, sessionID, status, startedAt string
		var finishedAt *string
		var done, failed int
		if err := rows.Scan(&project, &sessionID, &status, &done, &failed, &startedAt, &finishedAt); err != nil {
			return fmt.Errorf("scanning row: %w", err)
		}

		duration := "-"
		if finishedAt != nil && *finishedAt != "" {
			startT, err1 := time.Parse("2006-01-02 15:04:05", startedAt)
			endT, err2 := time.Parse("2006-01-02 15:04:05", *finishedAt)
			if err1 == nil && err2 == nil {
				d := endT.Sub(startT)
				duration = d.Truncate(time.Second).String()
			}
		}

		fmt.Fprintf(w, "%-25s %-10s %5d %5d %10s %s\n",
			truncateStr(filepath.Base(project), 25),
			status,
			done,
			failed,
			duration,
			startedAt)
		count++
	}

	if count == 0 {
		fmt.Fprintln(w, "(no run history)")
	}

	return nil
}

// truncateStr truncates a string with ellipsis if it exceeds max length.
func truncateStr(s string, max int) string {
	if len(s) <= max {
		return s
	}
	if max <= 3 {
		return s[:max]
	}
	return s[:max-3] + "..."
}
