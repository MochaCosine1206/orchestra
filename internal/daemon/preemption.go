package daemon

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/MochaCosine1206/orchestra/internal/agent"
	"github.com/MochaCosine1206/orchestra/internal/db"
)

const (
	// MinPriorityGap is the minimum effective priority difference required
	// for a pending task to preempt a running task.
	MinPriorityGap = 20.0

	// MinRunDuration is the minimum time a task must run before it can be
	// preempted, protecting against immediate preemption of newly started tasks.
	MinRunDuration = 5 * time.Minute

	// MaxPreemptions is the maximum number of times a single task can be
	// preempted before anti-thrashing forces it to completion.
	MaxPreemptions = 3

	// GracefulTimeout is how long GracefulPreempt waits after SIGTERM
	// before sending SIGKILL.
	GracefulTimeout = 60 * time.Second

	// PreemptionBoostValue is the priority boost applied to a preempted task
	// for its next scheduling round.
	PreemptionBoostValue = 10
)

// PreemptionCandidate represents a running task that may be preempted.
type PreemptionCandidate struct {
	TaskID            string
	PID               int
	EffectivePriority float64
	StartedAt         time.Time
	PreemptionCount   int
}

// PreemptionResult records the outcome of a preemption decision or action.
type PreemptionResult struct {
	Preempted    bool
	TaskID       string
	Reason       string
	PriorityGap  float64
	RunDuration  time.Duration
	BoostedTo    float64
	AntiThrashed bool
}

// ShouldPreempt returns true when a pending task's effective priority exceeds
// the running task's priority by >= MinPriorityGap AND the running task has
// been active for >= MinRunDuration.
func ShouldPreempt(pendingPriority float64, running PreemptionCandidate, now time.Time) bool {
	gap := pendingPriority - running.EffectivePriority
	if gap < MinPriorityGap {
		return false
	}
	runDuration := now.Sub(running.StartedAt)
	return runDuration >= MinRunDuration
}

// AntiThrashing checks the preemption count for a task and returns false
// (blocking preemption) when the count >= MaxPreemptions. This forces the
// task to run to completion rather than being repeatedly preempted.
func AntiThrashing(ctx context.Context, d *db.DB, taskID string) (bool, error) {
	var count int
	err := d.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM run_history WHERE session_id = ? AND status = 'preempted'`,
		taskID).Scan(&count)
	if err != nil {
		return true, fmt.Errorf("querying preemption count for %s: %w", taskID, err)
	}
	if count >= MaxPreemptions {
		return false, nil
	}
	return true, nil
}

// GracefulPreempt sends SIGTERM to the running conductor process, waits up to
// GracefulTimeout for graceful shutdown, then sends SIGKILL if still alive.
// It reuses the signal patterns from agent/process.go.
func GracefulPreempt(pid int) error {
	return agent.KillProcess(pid, false, GracefulTimeout)
}

// RecordPreemption records a preemption event in run_history and applies
// PreemptionBoostValue to the preempted task's priority in priority_queue.
func RecordPreemption(ctx context.Context, d *db.DB, taskID, projectPath string) error {
	return d.WithTx(ctx, func(tx *sql.Tx) error {
		// Record the preemption event in run_history.
		id := db.GenID("run")
		_, err := tx.ExecContext(ctx,
			`INSERT INTO run_history (id, project_path, session_id, started_at, finished_at, status, tasks_done, tasks_failed)
			 VALUES (?, ?, ?, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP, 'preempted', 0, 0)`,
			id, projectPath, taskID)
		if err != nil {
			return fmt.Errorf("recording preemption event: %w", err)
		}

		// Apply preemption boost to the task's priority in priority_queue.
		_, err = tx.ExecContext(ctx,
			`UPDATE priority_queue
			 SET effective_priority = effective_priority + ?, computed_at = CURRENT_TIMESTAMP
			 WHERE task_id = ?`,
			PreemptionBoostValue, taskID)
		if err != nil {
			return fmt.Errorf("applying preemption boost: %w", err)
		}

		return nil
	})
}

// EvaluatePreemption performs the full preemption decision flow:
// 1. Check if priority gap is sufficient and minimum run time has elapsed
// 2. Check anti-thrashing (max preemptions not exceeded)
// 3. If preemption is warranted, execute graceful preemption
// 4. Record the event and apply priority boost
func EvaluatePreemption(ctx context.Context, d *db.DB, pendingPriority float64, running PreemptionCandidate, projectPath string, now time.Time) (*PreemptionResult, error) {
	result := &PreemptionResult{
		TaskID:      running.TaskID,
		PriorityGap: pendingPriority - running.EffectivePriority,
		RunDuration: now.Sub(running.StartedAt),
	}

	if !ShouldPreempt(pendingPriority, running, now) {
		result.Reason = "insufficient priority gap or minimum run time not met"
		return result, nil
	}

	canPreempt, err := AntiThrashing(ctx, d, running.TaskID)
	if err != nil {
		return result, fmt.Errorf("anti-thrashing check: %w", err)
	}
	if !canPreempt {
		result.AntiThrashed = true
		result.Reason = fmt.Sprintf("anti-thrashing: task %s preempted %d times, forcing completion", running.TaskID, MaxPreemptions)
		return result, nil
	}

	if err := GracefulPreempt(running.PID); err != nil {
		return result, fmt.Errorf("graceful preempt PID %d: %w", running.PID, err)
	}

	if err := RecordPreemption(ctx, d, running.TaskID, projectPath); err != nil {
		return result, fmt.Errorf("recording preemption: %w", err)
	}

	result.Preempted = true
	result.BoostedTo = running.EffectivePriority + PreemptionBoostValue
	result.Reason = fmt.Sprintf("preempted: gap=%.1f, run=%s", result.PriorityGap, result.RunDuration.Truncate(time.Second))

	return result, nil
}
