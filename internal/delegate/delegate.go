package delegate

import (
	"context"
	"fmt"
	"os"
	"syscall"

	"github.com/MochaCosine1206/orchestra/internal/agent"
	"github.com/MochaCosine1206/orchestra/internal/db"
	"github.com/MochaCosine1206/orchestra/internal/orchestrator"
)

// Delegate wraps write operations using Go-native implementations.
// All bash script fallbacks have been removed — the Go binary is the primary interface.
type Delegate struct {
	DB        *db.DB                  // required: Go DB mutations
	Spawner   *agent.Spawner          // optional: Go spawner for spawn/respawn
	Conductor *orchestrator.Conductor // optional: Go conductor for orchestra go
}

// New creates a new Delegate with a database connection.
func New(d *db.DB) *Delegate {
	return &Delegate{DB: d}
}

// NewFull creates a Delegate with all Go-native components.
func NewFull(d *db.DB, spawner *agent.Spawner, conductor *orchestrator.Conductor) *Delegate {
	return &Delegate{DB: d, Spawner: spawner, Conductor: conductor}
}

// Result captures the output of a delegated command.
type Result struct {
	Output string
	Err    error
}

// KillTask terminates a running task's agent process via Go mutation + SIGTERM.
func (d *Delegate) KillTask(ctx context.Context, taskID string) Result {
	if d.DB == nil {
		return Result{Err: fmt.Errorf("database not configured")}
	}

	// Look up the agent assigned to this task to get its PID
	ag, err := d.DB.GetAgentByTask(ctx, taskID)
	if err != nil {
		return Result{Err: fmt.Errorf("looking up agent for task %s: %w", taskID, err)}
	}

	// Send SIGTERM to agent process if it has a valid PID
	if ag != nil && ag.PID.Valid && ag.PID.Int64 > 0 {
		pid := int(ag.PID.Int64)
		if proc, err := os.FindProcess(pid); err == nil {
			proc.Signal(syscall.SIGTERM)
		}
		d.DB.SetAgentDead(ctx, ag.ID)
	}

	// Mark the task as failed
	if err := d.DB.FailTask(ctx, taskID, "killed by user"); err != nil {
		return Result{Err: fmt.Errorf("failing task %s: %w", taskID, err)}
	}

	d.DB.LogEvent(ctx, "task_killed", "", taskID, "")
	return Result{Output: fmt.Sprintf("task %s killed", taskID)}
}

// RetryTask resets a failed task for re-execution via Go mutation.
func (d *Delegate) RetryTask(ctx context.Context, taskID string) Result {
	if d.DB == nil {
		return Result{Err: fmt.Errorf("database not configured")}
	}
	if err := d.DB.ResetTask(ctx, taskID); err != nil {
		return Result{Err: fmt.Errorf("resetting task %s: %w", taskID, err)}
	}
	d.DB.LogEvent(ctx, "task_reset", "", taskID, "")
	return Result{Output: fmt.Sprintf("task %s reset to pending", taskID)}
}

// SpawnTask spawns an agent for a pending task via Go spawner.
func (d *Delegate) SpawnTask(ctx context.Context, taskID string, role string) Result {
	if d.Spawner == nil {
		return Result{Err: fmt.Errorf("spawner not configured")}
	}
	result, err := d.Spawner.Run(ctx, agent.SpawnOpts{
		TaskID: taskID,
		Role:   agent.Role(role),
	})
	if err != nil {
		return Result{Err: fmt.Errorf("spawning task %s: %w", taskID, err)}
	}
	return Result{Output: fmt.Sprintf("spawned agent %s (PID=%d)", result.AgentID, result.PID)}
}

// RespawnTask respawns a failed task with circuit breaker via Go spawner.
func (d *Delegate) RespawnTask(ctx context.Context, taskID string) Result {
	if d.Spawner == nil {
		return Result{Err: fmt.Errorf("spawner not configured")}
	}
	result, err := d.Spawner.Respawn(ctx, taskID)
	if err != nil {
		return Result{Err: fmt.Errorf("respawning task %s: %w", taskID, err)}
	}
	return Result{Output: fmt.Sprintf("respawned agent %s (PID=%d)", result.AgentID, result.PID)}
}

// OrchestraGo dispatches a new goal to the Go conductor.
func (d *Delegate) OrchestraGo(ctx context.Context, goal string) Result {
	if d.Conductor == nil {
		return Result{Err: fmt.Errorf("conductor not configured")}
	}
	goResult, err := d.Conductor.Go(ctx, orchestrator.GoOpts{Goal: goal})
	if err != nil {
		return Result{Err: fmt.Errorf("conductor go: %w", err)}
	}
	return Result{Output: fmt.Sprintf("session=%s tasks=%d done=%d failed=%d",
		goResult.SessionID, goResult.TaskCount, goResult.DoneCount, goResult.FailedCount)}
}

// OrchestraGoWithClarify dispatches a goal with TUI clarification enabled.
func (d *Delegate) OrchestraGoWithClarify(ctx context.Context, goal string) Result {
	if d.Conductor == nil {
		return Result{Err: fmt.Errorf("conductor not configured")}
	}
	goResult, err := d.Conductor.Go(ctx, orchestrator.GoOpts{
		Goal:        goal,
		Clarify:     true,
		ClarifyMode: orchestrator.ClarifyTUI,
	})
	if err != nil {
		return Result{Err: fmt.Errorf("conductor go: %w", err)}
	}
	return Result{Output: fmt.Sprintf("session=%s tasks=%d done=%d failed=%d",
		goResult.SessionID, goResult.TaskCount, goResult.DoneCount, goResult.FailedCount)}
}
