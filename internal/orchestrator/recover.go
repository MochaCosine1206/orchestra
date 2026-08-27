package orchestrator

import (
	"context"
	"fmt"
	"os"
	"strconv"

	"github.com/MochaCosine1206/orchestra/internal/agent"
)

// RecoverResult describes the outcome of a session recovery attempt.
type RecoverResult struct {
	SessionID    string
	Goal         string
	TaskIDs      []string
	RunningCount int
	PendingCount int
	DoneCount    int
	FailedCount  int
	Adopted      bool // true if this conductor adopted the orphan session
}

// Recover checks for an orphan conductor session and adopts it.
// Returns nil result if there is nothing to recover.
func (c *Conductor) Recover(ctx context.Context) (*RecoverResult, error) {
	// Try conductors table first (Phase 4: multi-conductor support)
	conductors, _ := c.DB.ListActiveConductors(ctx)
	for _, cond := range conductors {
		if cond.PID > 0 && agent.PidAlive(cond.PID) {
			// Live conductor — skip it (parallel conductors are fine)
			continue
		}
		// Found an orphan conductor — adopt it
		taskIDs, _ := c.DB.ListTaskIDsByConductor(ctx, cond.ID)
		if len(taskIDs) == 0 {
			// No tasks — clean up stale conductor
			c.DB.UpdateConductorStatus(ctx, cond.ID, "abandoned")
			c.deactivateConductor(ctx)
			c.DB.LogEvent(ctx, "recover_stale_cleanup", "", "", fmt.Sprintf("conductor %s has no tasks", cond.ID))
			continue
		}

		result := &RecoverResult{
			SessionID: cond.ID,
			Goal:      cond.Goal,
			TaskIDs:   taskIDs,
		}

		running, _ := c.DB.CountTasksByStatuses(ctx, []string{"assigned", "running"}, taskIDs)
		pending, _ := c.DB.CountTasksByStatuses(ctx, []string{"pending"}, taskIDs)
		done, _ := c.DB.CountTasksByStatuses(ctx, []string{"done"}, taskIDs)
		failed, _ := c.DB.CountTasksByStatuses(ctx, []string{"failed"}, taskIDs)

		result.RunningCount = running
		result.PendingCount = pending
		result.DoneCount = done
		result.FailedCount = failed

		// Adopt: update conductor PID and session state
		c.DB.UpdateConductorHeartbeat(ctx, cond.ID)
		c.setBlackboard(ctx, "conductor:pid", fmt.Sprintf("%d", os.Getpid()))
		c.SessionID = cond.ID
		c.ConductorID = cond.ID

		result.Adopted = true
		c.DB.LogEvent(ctx, "session_recovered", "", "", fmt.Sprintf(
			`{"session":"%s","running":%d,"pending":%d,"done":%d,"failed":%d}`,
			cond.ID, running, pending, done, failed))

		return result, nil
	}

	// Fallback to blackboard for backward compat (pre-Phase 4 sessions)
	active := c.getBlackboard(ctx, "conductor:active")
	if active != "1" {
		return nil, nil // nothing to recover
	}

	pidStr := c.getBlackboard(ctx, "conductor:pid")
	if pidStr != "" {
		pid, _ := strconv.Atoi(pidStr)
		if pid > 0 && agent.PidAlive(pid) {
			// Live conductor — nothing to recover (parallel conductors are fine)
			return nil, nil
		}
	}

	sessionID := c.getBlackboard(ctx, "conductor:session_id")
	goal := c.getBlackboard(ctx, "conductor:goal")
	taskIDs := c.sessionTaskIDs(ctx)

	result := &RecoverResult{
		SessionID: sessionID,
		Goal:      goal,
		TaskIDs:   taskIDs,
	}

	if len(taskIDs) == 0 {
		c.deactivateConductor(ctx)
		c.DB.LogEvent(ctx, "recover_stale_cleanup", "", "", "no task_ids found")
		return result, nil
	}

	running, _ := c.DB.CountTasksByStatuses(ctx, []string{"assigned", "running"}, taskIDs)
	pending, _ := c.DB.CountTasksByStatuses(ctx, []string{"pending"}, taskIDs)
	done, _ := c.DB.CountTasksByStatuses(ctx, []string{"done"}, taskIDs)
	failed, _ := c.DB.CountTasksByStatuses(ctx, []string{"failed"}, taskIDs)

	result.RunningCount = running
	result.PendingCount = pending
	result.DoneCount = done
	result.FailedCount = failed

	c.setBlackboard(ctx, "conductor:pid", fmt.Sprintf("%d", os.Getpid()))
	if sessionID != "" {
		c.SessionID = sessionID
	}

	result.Adopted = true
	c.DB.LogEvent(ctx, "session_recovered", "", "", fmt.Sprintf(
		`{"session":"%s","running":%d,"pending":%d,"done":%d,"failed":%d}`,
		sessionID, running, pending, done, failed))

	return result, nil
}
