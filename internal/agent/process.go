// PID management patterns (WritePID, ReadPID, PidAlive, KillProcess) are reused
// by the global daemon for flock-based singleton enforcement (Phase 2B).
// Priority engine (internal/priority) uses PidAlive for trust-scored agent monitoring.
package agent

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/MochaCosine1206/orchestra/internal/db"
)

// PidAlive checks whether a process with the given PID is still running.
func PidAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	// Signal 0 tests existence without actually sending a signal.
	err = proc.Signal(syscall.Signal(0))
	return err == nil
}

// WritePID writes a PID file to pidsDir/taskID.pid.
func WritePID(pidsDir, taskID string, pid int) error {
	path := filepath.Join(pidsDir, taskID+".pid")
	return os.WriteFile(path, []byte(strconv.Itoa(pid)), 0o644)
}

// ReadPID reads the PID from pidsDir/taskID.pid. Returns 0 if the file doesn't exist.
func ReadPID(pidsDir, taskID string) (int, error) {
	path := filepath.Join(pidsDir, taskID+".pid")
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, fmt.Errorf("reading PID file %s: %w", path, err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		return 0, fmt.Errorf("parsing PID from %s: %w", path, err)
	}
	return pid, nil
}

// RemovePID removes the PID file for a task.
func RemovePID(pidsDir, taskID string) {
	path := filepath.Join(pidsDir, taskID+".pid")
	os.Remove(path)
}

// KillProcess sends SIGTERM, waits gracePeriod, then SIGKILL if still alive.
// If force is true, skips SIGTERM and sends SIGKILL immediately.
func KillProcess(pid int, force bool, gracePeriod time.Duration) error {
	if pid <= 0 {
		return fmt.Errorf("invalid PID: %d", pid)
	}
	proc, err := os.FindProcess(pid)
	if err != nil {
		return fmt.Errorf("finding process %d: %w", pid, err)
	}

	if force {
		return proc.Signal(syscall.SIGKILL)
	}

	// Graceful: SIGTERM → wait → SIGKILL
	if err := proc.Signal(syscall.SIGTERM); err != nil {
		// Process already dead — not an error.
		return nil
	}

	// Poll for process exit during grace period.
	deadline := time.Now().Add(gracePeriod)
	for time.Now().Before(deadline) {
		if !PidAlive(pid) {
			return nil
		}
		time.Sleep(200 * time.Millisecond)
	}

	// Still alive — force kill.
	if PidAlive(pid) {
		return proc.Signal(syscall.SIGKILL)
	}
	return nil
}

// KillTaskProcess is the full pipeline for killing a task's agent process.
// It finds the PID, kills the process, marks the agent dead, fails the task, and logs.
func KillTaskProcess(ctx context.Context, d *db.DB, taskID, pidsDir string, force bool) error {
	// Docker: kill container first (if it exists)
	runtime, _ := d.GetBlackboardValue(ctx, "conductor:runtime")
	if runtime == "docker" {
		KillContainer(taskID)
	}

	agent, err := d.GetAgentByTask(ctx, taskID)
	if err != nil {
		return fmt.Errorf("looking up agent for task %s: %w", taskID, err)
	}

	var pid int
	if agent != nil && agent.PID.Valid {
		pid = int(agent.PID.Int64)
	} else {
		// Try PID file
		pid, err = ReadPID(pidsDir, taskID)
		if err != nil {
			return fmt.Errorf("reading PID for task %s: %w", taskID, err)
		}
	}

	if pid > 0 {
		if killErr := KillProcess(pid, force, 10*time.Second); killErr != nil {
			d.LogEvent(ctx, "kill_error", "", taskID, killErr.Error())
		}
		RemovePID(pidsDir, taskID)
	}

	if agent != nil {
		if err := d.SetAgentDead(ctx, agent.ID); err != nil {
			return fmt.Errorf("setting agent dead: %w", err)
		}
	}

	if err := d.FailTask(ctx, taskID, "killed"); err != nil {
		return fmt.Errorf("failing task %s: %w", taskID, err)
	}

	d.LogEvent(ctx, "task_killed", "", taskID, "")
	return nil
}
