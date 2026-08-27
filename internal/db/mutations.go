package db

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

// --- Conductor operations ---

// CreateConductor inserts a new conductor record.
func (d *DB) CreateConductor(ctx context.Context, c ConductorRecord) error {
	mergeReview := 0
	if c.MergeReview {
		mergeReview = 1
	}
	repoMap := 0
	if c.RepoMap {
		repoMap = 1
	}
	lenientDeps := 0
	if c.LenientDeps {
		lenientDeps = 1
	}
	mergeMode := c.MergeMode
	if mergeMode == "" {
		mergeMode = "local"
	}
	mergeStrategy := c.MergeStrategy
	if mergeStrategy == "" {
		mergeStrategy = "batch"
	}
	_, err := d.ExecContext(ctx,
		`INSERT OR IGNORE INTO conductors (id, pid, goal, status, staging_branch, base_branch,
		  max_parallel, test_cmd, merge_review, model_strategy, runtime,
		  repo_map, lenient_deps, file_enforcement, merge_mode, merge_strategy, phase_id, started_at, heartbeat_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, NULLIF(?, ''), ?, ?, ?, ?, ?, ?, ?, ?, NULLIF(?, ''), CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`,
		c.ID, c.PID, c.Goal, c.Status, c.StagingBranch, c.BaseBranch,
		c.MaxParallel, c.TestCmd.String, mergeReview, c.ModelStrategy, c.Runtime,
		repoMap, lenientDeps, c.FileEnforcement, mergeMode, mergeStrategy, c.PhaseID)
	if err != nil {
		return fmt.Errorf("creating conductor %s: %w", c.ID, err)
	}
	return nil
}

// UpdateConductorStatus updates the status of a conductor. Sets completed_at for terminal states.
func (d *DB) UpdateConductorStatus(ctx context.Context, id, status string) error {
	var query string
	if status == "completed" || status == "failed" || status == "abandoned" {
		query = `UPDATE conductors SET status = ?, completed_at = CURRENT_TIMESTAMP WHERE id = ?`
	} else {
		query = `UPDATE conductors SET status = ? WHERE id = ?`
	}
	res, err := d.ExecContext(ctx, query, status, id)
	if err != nil {
		return fmt.Errorf("updating conductor status %s: %w", id, err)
	}
	return checkRowsAffected(res, "conductor", id)
}

// SetMergeStatus updates the merge phase status for a conductor (B-145).
// Only sets merge_started_at on the first transition (COALESCE preserves existing value).
func (d *DB) SetMergeStatus(ctx context.Context, conductorID, status string) error {
	res, err := d.ExecContext(ctx,
		`UPDATE conductors
		 SET merge_status = ?,
		     merge_started_at = COALESCE(merge_started_at, CURRENT_TIMESTAMP)
		 WHERE id = ?`, status, conductorID)
	if err != nil {
		return fmt.Errorf("setting merge status %s on conductor %s: %w", status, conductorID, err)
	}
	return checkRowsAffected(res, "conductor", conductorID)
}

// AddMergedBranch appends a branch name to the conductor's merge_branches_done JSON array (B-145).
func (d *DB) AddMergedBranch(ctx context.Context, conductorID, branch string) error {
	res, err := d.ExecContext(ctx,
		`UPDATE conductors
		 SET merge_branches_done = json_insert(
		       COALESCE(merge_branches_done, '[]'),
		       '$[#]', ?)
		 WHERE id = ?`, branch, conductorID)
	if err != nil {
		return fmt.Errorf("adding merged branch %s to conductor %s: %w", branch, conductorID, err)
	}
	return checkRowsAffected(res, "conductor", conductorID)
}

// ClearMergeState resets all merge state fields to NULL (B-145).
func (d *DB) ClearMergeState(ctx context.Context, conductorID string) error {
	res, err := d.ExecContext(ctx,
		`UPDATE conductors
		 SET merge_status = NULL, merge_started_at = NULL, merge_branches_done = NULL
		 WHERE id = ?`, conductorID)
	if err != nil {
		return fmt.Errorf("clearing merge state for conductor %s: %w", conductorID, err)
	}
	return checkRowsAffected(res, "conductor", conductorID)
}

// UpdateConductorHeartbeat updates the heartbeat timestamp for a conductor.
func (d *DB) UpdateConductorHeartbeat(ctx context.Context, id string) error {
	res, err := d.ExecContext(ctx,
		`UPDATE conductors SET heartbeat_at = CURRENT_TIMESTAMP WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("updating conductor heartbeat %s: %w", id, err)
	}
	return checkRowsAffected(res, "conductor", id)
}

// --- Agent operations ---

// RegisterAgent inserts a new agent into the agents table.
func (d *DB) RegisterAgent(ctx context.Context, a Agent) error {
	_, err := d.ExecContext(ctx,
		`INSERT INTO agents (id, role, status, worktree, pid, current_task, heartbeat_at, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		a.ID, a.Role, a.Status, a.Worktree, a.PID, a.CurrentTask, a.HeartbeatAt, a.CreatedAt)
	if err != nil {
		return fmt.Errorf("registering agent %s: %w", a.ID, err)
	}
	return nil
}

// UpdateAgentPID updates the PID for an agent.
func (d *DB) UpdateAgentPID(ctx context.Context, agentID string, pid int) error {
	res, err := d.ExecContext(ctx,
		`UPDATE agents SET pid = ? WHERE id = ?`, pid, agentID)
	if err != nil {
		return fmt.Errorf("updating agent PID %s: %w", agentID, err)
	}
	return checkRowsAffected(res, "agent", agentID)
}

// UpdateAgentStatus updates the status for an agent.
func (d *DB) UpdateAgentStatus(ctx context.Context, agentID, status string) error {
	res, err := d.ExecContext(ctx,
		`UPDATE agents SET status = ? WHERE id = ?`, status, agentID)
	if err != nil {
		return fmt.Errorf("updating agent status %s: %w", agentID, err)
	}
	return checkRowsAffected(res, "agent", agentID)
}

// UpdateAgentHeartbeat updates the heartbeat timestamp for an agent.
func (d *DB) UpdateAgentHeartbeat(ctx context.Context, agentID string) error {
	res, err := d.ExecContext(ctx,
		`UPDATE agents SET heartbeat_at = CURRENT_TIMESTAMP WHERE id = ?`, agentID)
	if err != nil {
		return fmt.Errorf("updating agent heartbeat %s: %w", agentID, err)
	}
	return checkRowsAffected(res, "agent", agentID)
}

// SetAgentDead marks an agent as dead and clears its PID.
func (d *DB) SetAgentDead(ctx context.Context, agentID string) error {
	res, err := d.ExecContext(ctx,
		`UPDATE agents SET status = 'dead', pid = NULL WHERE id = ?`, agentID)
	if err != nil {
		return fmt.Errorf("setting agent dead %s: %w", agentID, err)
	}
	return checkRowsAffected(res, "agent", agentID)
}

// --- Task operations ---

// CreateTask inserts a new task into the tasks table.
func (d *DB) CreateTask(ctx context.Context, t Task) error {
	_, err := d.ExecContext(ctx,
		`INSERT INTO tasks (id, title, description, acceptance_criteria, status, priority, priority_label, role, assigned_to,
		  depends_on, blocked_by, worktree, branch, result, conductor_id, phase_id, feature_cluster, created_at, started_at, completed_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		t.ID, t.Title, t.Description, t.AcceptanceCriteria, t.Status, t.Priority, t.PriorityLabel, t.Role, t.AssignedTo,
		t.DependsOn, t.BlockedBy, t.Worktree, t.Branch, t.Result, t.ConductorID, t.PhaseID, t.FeatureCluster, t.CreatedAt,
		t.StartedAt, t.CompletedAt)
	if err != nil {
		return fmt.Errorf("creating task %s: %w", t.ID, err)
	}
	return nil
}

// UpdateTaskPriorityLabel updates the priority_label for a task.
func (d *DB) UpdateTaskPriorityLabel(ctx context.Context, taskID string, label sql.NullString) error {
	res, err := d.ExecContext(ctx,
		`UPDATE tasks SET priority_label = ? WHERE id = ?`, label, taskID)
	if err != nil {
		return fmt.Errorf("updating task priority_label %s: %w", taskID, err)
	}
	return checkRowsAffected(res, "task", taskID)
}

// AssignTask assigns a task to an agent with worktree and branch.
// Updates both the tasks table (assigned_to) and agents table (current_task).
func (d *DB) AssignTask(ctx context.Context, taskID, agentID, worktree, branch string) error {
	res, err := d.ExecContext(ctx,
		`UPDATE tasks SET assigned_to = ?, worktree = ?, branch = ?, status = 'assigned'
		 WHERE id = ?`, agentID, worktree, branch, taskID)
	if err != nil {
		return fmt.Errorf("assigning task %s: %w", taskID, err)
	}
	if err := checkRowsAffected(res, "task", taskID); err != nil {
		return err
	}
	// Also set current_task on the agent so the monitor can track it
	_, err = d.ExecContext(ctx,
		`UPDATE agents SET current_task = ? WHERE id = ?`, taskID, agentID)
	if err != nil {
		return fmt.Errorf("setting current_task on agent %s: %w", agentID, err)
	}
	return nil
}

// ClaimTask atomically assigns a task to an agent only if the task is still pending.
// Returns true if the claim succeeded, false if the task was already claimed by another agent.
// This is the concurrency-safe version of AssignTask for multi-conductor scenarios.
func (d *DB) ClaimTask(ctx context.Context, taskID, agentID, worktree, branch string) (bool, error) {
	res, err := d.ExecContext(ctx,
		`UPDATE tasks SET assigned_to = ?, worktree = ?, branch = ?, status = 'assigned'
		 WHERE id = ? AND status = 'pending'`, agentID, worktree, branch, taskID)
	if err != nil {
		return false, fmt.Errorf("claiming task %s: %w", taskID, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("checking claim result: %w", err)
	}
	if n == 0 {
		return false, nil // task was already claimed
	}
	// Set current_task on the agent.
	_, err = d.ExecContext(ctx,
		`UPDATE agents SET current_task = ? WHERE id = ?`, taskID, agentID)
	if err != nil {
		return true, fmt.Errorf("setting current_task on agent %s: %w", agentID, err)
	}
	return true, nil
}

// StartTask marks a task as running with current timestamp.
func (d *DB) StartTask(ctx context.Context, taskID string) error {
	res, err := d.ExecContext(ctx,
		`UPDATE tasks SET status = 'running', started_at = CURRENT_TIMESTAMP WHERE id = ?`, taskID)
	if err != nil {
		return fmt.Errorf("starting task %s: %w", taskID, err)
	}
	return checkRowsAffected(res, "task", taskID)
}

// CompleteTask marks a task as done with a result.
func (d *DB) CompleteTask(ctx context.Context, taskID, result string) error {
	res, err := d.ExecContext(ctx,
		`UPDATE tasks SET status = 'done', result = ?, completed_at = CURRENT_TIMESTAMP WHERE id = ?`,
		result, taskID)
	if err != nil {
		return fmt.Errorf("completing task %s: %w", taskID, err)
	}
	return checkRowsAffected(res, "task", taskID)
}

// FailTask marks a task as failed with a reason.
func (d *DB) FailTask(ctx context.Context, taskID, reason string) error {
	res, err := d.ExecContext(ctx,
		`UPDATE tasks SET status = 'failed', result = ?, completed_at = CURRENT_TIMESTAMP WHERE id = ?`,
		reason, taskID)
	if err != nil {
		return fmt.Errorf("failing task %s: %w", taskID, err)
	}
	return checkRowsAffected(res, "task", taskID)
}

// ResetTask resets a task to pending, clearing assignment and timestamps.
func (d *DB) ResetTask(ctx context.Context, taskID string) error {
	res, err := d.ExecContext(ctx,
		`UPDATE tasks SET status = 'pending', assigned_to = NULL, worktree = NULL,
		  branch = NULL, result = NULL, started_at = NULL, completed_at = NULL WHERE id = ?`, taskID)
	if err != nil {
		return fmt.Errorf("resetting task %s: %w", taskID, err)
	}
	return checkRowsAffected(res, "task", taskID)
}

// SoftResetTask resets a task to pending, keeping the agent assignment, worktree, and branch.
// Used by refinement respawn to preserve the existing worktree for the next attempt.
func (d *DB) SoftResetTask(ctx context.Context, taskID string) error {
	res, err := d.ExecContext(ctx,
		`UPDATE tasks SET status = 'pending', result = NULL, started_at = NULL, completed_at = NULL WHERE id = ?`, taskID)
	if err != nil {
		return fmt.Errorf("soft-resetting task %s: %w", taskID, err)
	}
	return checkRowsAffected(res, "task", taskID)
}

// ResetCascadeFailedDependents resets tasks cascade-failed due to the given blocker.
// Called when a blocker enters refinement — its dependents get a second chance.
func (d *DB) ResetCascadeFailedDependents(ctx context.Context, blockerTaskID string) (int, error) {
	rows, err := d.QueryContext(ctx,
		`SELECT t.id FROM tasks t
		 WHERE t.status = 'failed'
		   AND t.blocked_by IS NOT NULL
		   AND EXISTS (
		     SELECT 1 FROM json_each(t.blocked_by) je WHERE je.value = ?
		   )`, blockerTaskID)
	if err != nil {
		return 0, fmt.Errorf("query cascade-failed dependents of %s: %w", blockerTaskID, err)
	}
	defer rows.Close()

	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return 0, err
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return 0, err
	}

	reset := 0
	for _, depID := range ids {
		ft, _ := d.GetBlackboardValue(ctx, "failure_type:"+depID)
		if ft != "cascade_fail" {
			continue
		}
		res, err := d.ExecContext(ctx,
			`UPDATE tasks SET status = 'pending', result = NULL, completed_at = NULL
			 WHERE id = ? AND status = 'failed'`, depID)
		if err != nil {
			continue
		}
		if n, _ := res.RowsAffected(); n > 0 {
			d.DeleteBlackboard(ctx, "failure_type:"+depID)
			d.LogEvent(ctx, "cascade_fail_reversed", "", depID,
				fmt.Sprintf(`{"blocker":"%s"}`, blockerTaskID))
			reset++
		}
	}
	return reset, nil
}

// --- Blackboard operations ---

// SetBlackboard inserts or replaces a blackboard entry.
func (d *DB) SetBlackboard(ctx context.Context, key, value, writer string) error {
	_, err := d.ExecContext(ctx,
		`INSERT OR REPLACE INTO blackboard (key, value, written_by, updated_at)
		 VALUES (?, ?, ?, CURRENT_TIMESTAMP)`, key, value, writer)
	if err != nil {
		return fmt.Errorf("setting blackboard %q: %w", key, err)
	}
	return nil
}

// DeleteBlackboard removes a blackboard entry.
func (d *DB) DeleteBlackboard(ctx context.Context, key string) error {
	_, err := d.ExecContext(ctx,
		`DELETE FROM blackboard WHERE key = ?`, key)
	if err != nil {
		return fmt.Errorf("deleting blackboard %q: %w", key, err)
	}
	return nil
}

// AcquireBlackboardLock tries to atomically acquire a lock key. Returns true if acquired.
func (d *DB) AcquireBlackboardLock(ctx context.Context, key, writer string) (bool, error) {
	res, err := d.ExecContext(ctx,
		`INSERT OR IGNORE INTO blackboard (key, value, written_by, updated_at)
		 VALUES (?, 'locked', ?, CURRENT_TIMESTAMP)`, key, writer)
	if err != nil {
		return false, fmt.Errorf("acquiring blackboard lock %q: %w", key, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("checking rows affected: %w", err)
	}
	return n > 0, nil
}

// --- Event operations ---

// LogEvent inserts an event into the events table.
func (d *DB) LogEvent(ctx context.Context, eventType, agentID, taskID, payload string) error {
	_, err := d.ExecContext(ctx,
		`INSERT INTO events (event_type, agent_id, task_id, payload)
		 VALUES (?, NULLIF(?, ''), NULLIF(?, ''), NULLIF(?, ''))`,
		eventType, agentID, taskID, payload)
	if err != nil {
		return fmt.Errorf("logging event %q: %w", eventType, err)
	}
	return nil
}

// --- File lock operations ---

// CreateFileLock creates a file lock entry.
// Empty lockedBy is stored as NULL to satisfy the agents(id) foreign key constraint
// (agent may not be assigned yet at decomposition time).
func (d *DB) CreateFileLock(ctx context.Context, filePath, lockedBy, taskID string, expiresAt time.Time) error {
	_, err := d.ExecContext(ctx,
		`INSERT INTO file_locks (file_path, locked_by, task_id, locked_at, expires_at)
		 VALUES (?, NULLIF(?, ''), ?, CURRENT_TIMESTAMP, ?)`,
		filePath, lockedBy, taskID, expiresAt)
	if err != nil {
		return fmt.Errorf("creating file lock %q: %w", filePath, err)
	}
	return nil
}

// ReleaseFileLock removes a file lock entry.
func (d *DB) ReleaseFileLock(ctx context.Context, filePath string) error {
	_, err := d.ExecContext(ctx,
		`DELETE FROM file_locks WHERE file_path = ?`, filePath)
	if err != nil {
		return fmt.Errorf("releasing file lock %q: %w", filePath, err)
	}
	return nil
}

// CleanupExpiredLocks removes all expired file locks.
func (d *DB) CleanupExpiredLocks(ctx context.Context) error {
	_, err := d.ExecContext(ctx,
		`DELETE FROM file_locks WHERE expires_at IS NOT NULL AND expires_at < CURRENT_TIMESTAMP`)
	if err != nil {
		return fmt.Errorf("cleaning up expired locks: %w", err)
	}
	return nil
}

// ClearTaskAssignment clears a stale agent assignment on a pending task,
// allowing it to be re-claimed in the next auto-spawn cycle.
func (d *DB) ClearTaskAssignment(ctx context.Context, taskID string) error {
	_, err := d.ExecContext(ctx,
		`UPDATE tasks SET assigned_to = NULL WHERE id = ? AND status = 'pending'`, taskID)
	if err != nil {
		return fmt.Errorf("clearing task assignment %s: %w", taskID, err)
	}
	return nil
}

// --- Cleanup operations ---

// CleanupDeadAgents marks agents as dead if their heartbeat is older than ttlMinutes.
// Returns the number of agents cleaned up.
func (d *DB) CleanupDeadAgents(ctx context.Context, ttlMinutes int) (int64, error) {
	res, err := d.ExecContext(ctx,
		`UPDATE agents SET status = 'dead', pid = NULL
		 WHERE status IN ('idle', 'working', 'active')
		   AND heartbeat_at IS NOT NULL
		   AND heartbeat_at < datetime('now', ? || ' minutes')`,
		fmt.Sprintf("-%d", ttlMinutes))
	if err != nil {
		return 0, fmt.Errorf("cleaning up dead agents: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("checking rows affected: %w", err)
	}
	return n, nil
}

// DeleteBlackboardByPrefix removes all blackboard entries whose key starts with prefix.
func (d *DB) DeleteBlackboardByPrefix(ctx context.Context, prefix string) error {
	_, err := d.ExecContext(ctx,
		`DELETE FROM blackboard WHERE key LIKE ? || '%'`, prefix)
	if err != nil {
		return fmt.Errorf("deleting blackboard by prefix %q: %w", prefix, err)
	}
	return nil
}

// TruncateAll deletes all rows from every table. Keeps the schema and file intact
// so existing open connections (e.g. dashboard TUI) remain valid.
func (d *DB) TruncateAll(ctx context.Context) error {
	tables := []string{"eval_results", "eval_runs", "eval_versions", "eval_scenarios", "healing_log", "events", "file_locks", "blackboard", "drift_scores", "stall_scores", "plan_cache", "tasks", "agents", "conductors"}
	for _, t := range tables {
		if _, err := d.ExecContext(ctx, "DELETE FROM "+t); err != nil {
			return fmt.Errorf("truncating %s: %w", t, err)
		}
	}
	return d.WALCheckpoint(ctx)
}

// WALCheckpoint forces a WAL checkpoint to prevent unbounded WAL file growth.
func (d *DB) WALCheckpoint(ctx context.Context) error {
	_, err := d.ExecContext(ctx, `PRAGMA wal_checkpoint(TRUNCATE)`)
	if err != nil {
		return fmt.Errorf("WAL checkpoint: %w", err)
	}
	return nil
}

// --- Stall score operations (B-142) ---

// RecordStallScore inserts a stall score row with all signal values.
func (d *DB) RecordStallScore(ctx context.Context, s StallScore) error {
	_, err := d.ExecContext(ctx,
		`INSERT INTO stall_scores (task_id, agent_id, composite_score,
		  signal_fingerprint, signal_progress, signal_files, signal_errors, signal_readwrite, phase)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, NULLIF(?, ''))`,
		s.TaskID, s.AgentID, s.CompositeScore,
		s.SignalFingerprint, s.SignalProgress, s.SignalFiles, s.SignalErrors, s.SignalReadWrite, s.Phase.String)
	if err != nil {
		return fmt.Errorf("recording stall score for task %s: %w", s.TaskID, err)
	}
	return nil
}

// GetRecentStallScores returns the last N stall scores for a given task, ordered by created_at DESC.
func (d *DB) GetRecentStallScores(ctx context.Context, taskID string, limit int) ([]StallScore, error) {
	rows, err := d.QueryContext(ctx,
		`SELECT id, task_id, agent_id, composite_score,
		        signal_fingerprint, signal_progress, signal_files, signal_errors, signal_readwrite,
		        phase, created_at
		 FROM stall_scores
		 WHERE task_id = ?
		 ORDER BY created_at DESC
		 LIMIT ?`, taskID, limit)
	if err != nil {
		return nil, fmt.Errorf("querying recent stall scores for task %s: %w", taskID, err)
	}
	defer rows.Close()

	var scores []StallScore
	for rows.Next() {
		var s StallScore
		if err := rows.Scan(&s.ID, &s.TaskID, &s.AgentID, &s.CompositeScore,
			&s.SignalFingerprint, &s.SignalProgress, &s.SignalFiles, &s.SignalErrors, &s.SignalReadWrite,
			&s.Phase, &s.CreatedAt); err != nil {
			return nil, fmt.Errorf("scanning stall score row: %w", err)
		}
		scores = append(scores, s)
	}
	return scores, rows.Err()
}

// --- Drift score operations ---

// RecordDriftScore inserts a drift score row.
func (d *DB) RecordDriftScore(ctx context.Context, s DriftScore) error {
	_, err := d.ExecContext(ctx,
		`INSERT INTO drift_scores (session_id, cycle_number, score, explanation, action_taken)
		 VALUES (?, ?, ?, NULLIF(?, ''), NULLIF(?, ''))`,
		s.SessionID, s.CycleNumber, s.Score, s.Explanation.String, s.ActionTaken.String)
	if err != nil {
		return fmt.Errorf("recording drift score for session %s cycle %d: %w", s.SessionID, s.CycleNumber, err)
	}
	return nil
}

// SetBlackboardOnce inserts a blackboard entry only if the key does not already exist.
// Returns true if the row was inserted, false if it already existed.
func (d *DB) SetBlackboardOnce(ctx context.Context, key, value, writer string) (bool, error) {
	res, err := d.ExecContext(ctx,
		`INSERT OR IGNORE INTO blackboard (key, value, written_by, updated_at)
		 VALUES (?, ?, ?, CURRENT_TIMESTAMP)`, key, value, writer)
	if err != nil {
		return false, fmt.Errorf("setting blackboard once %q: %w", key, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("checking rows affected: %w", err)
	}
	return n > 0, nil
}

// --- Merge queue operations (B-280) ---

// CreateMergeQueueEntry inserts a new merge queue entry.
func (d *DB) CreateMergeQueueEntry(ctx context.Context, e MergeQueueEntry) error {
	_, err := d.ExecContext(ctx,
		`INSERT OR IGNORE INTO merge_queue_entries (id, conductor_id, task_id, branch, position, status, refinement_count)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		e.ID, e.ConductorID, e.TaskID, e.Branch, e.Position, e.Status, e.RefinementCount)
	if err != nil {
		return fmt.Errorf("creating merge queue entry %s: %w", e.ID, err)
	}
	return nil
}

// UpdateMergeQueueEntryStatus updates the status of a merge queue entry.
func (d *DB) UpdateMergeQueueEntryStatus(ctx context.Context, id, status string) error {
	res, err := d.ExecContext(ctx,
		`UPDATE merge_queue_entries SET status = ? WHERE id = ?`, status, id)
	if err != nil {
		return fmt.Errorf("updating merge queue entry status %s: %w", id, err)
	}
	return checkRowsAffected(res, "merge_queue_entry", id)
}

// SetMergeQueueEntryMerged marks a merge queue entry as merged with timestamp.
func (d *DB) SetMergeQueueEntryMerged(ctx context.Context, id string) error {
	res, err := d.ExecContext(ctx,
		`UPDATE merge_queue_entries SET status = 'merged', merged_at = CURRENT_TIMESTAMP WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("setting merge queue entry merged %s: %w", id, err)
	}
	return checkRowsAffected(res, "merge_queue_entry", id)
}

// SetMergeQueueEntryFailed marks a merge queue entry as failed with an error message.
func (d *DB) SetMergeQueueEntryFailed(ctx context.Context, id, errorMsg string) error {
	res, err := d.ExecContext(ctx,
		`UPDATE merge_queue_entries SET status = 'failed', error_message = ? WHERE id = ?`, errorMsg, id)
	if err != nil {
		return fmt.Errorf("setting merge queue entry failed %s: %w", id, err)
	}
	return checkRowsAffected(res, "merge_queue_entry", id)
}

// IncrementMergeQueueRefinement increments the refinement count for a merge queue entry.
func (d *DB) IncrementMergeQueueRefinement(ctx context.Context, id string) error {
	res, err := d.ExecContext(ctx,
		`UPDATE merge_queue_entries SET refinement_count = refinement_count + 1 WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("incrementing merge queue refinement %s: %w", id, err)
	}
	return checkRowsAffected(res, "merge_queue_entry", id)
}

// RequeueMergeQueueEntry sets an entry back to pending with a new position for refinement retry.
func (d *DB) RequeueMergeQueueEntry(ctx context.Context, id string, newPosition int) error {
	res, err := d.ExecContext(ctx,
		`UPDATE merge_queue_entries SET status = 'pending', position = ?, merged_at = NULL WHERE id = ?`,
		newPosition, id)
	if err != nil {
		return fmt.Errorf("requeuing merge queue entry %s: %w", id, err)
	}
	return checkRowsAffected(res, "merge_queue_entry", id)
}

// SetMergeStrategy updates the merge_strategy column on a conductor.
func (d *DB) SetMergeStrategy(ctx context.Context, conductorID, strategy string) error {
	res, err := d.ExecContext(ctx,
		`UPDATE conductors SET merge_strategy = ? WHERE id = ?`, strategy, conductorID)
	if err != nil {
		return fmt.Errorf("setting merge strategy on conductor %s: %w", conductorID, err)
	}
	return checkRowsAffected(res, "conductor", conductorID)
}

// --- Eval/healing operations ---

// InsertEvalScenario inserts a new eval scenario.
func (d *DB) InsertEvalScenario(ctx context.Context, s EvalScenario) error {
	_, err := d.ExecContext(ctx,
		`INSERT INTO eval_scenarios (id, role, category, repo_path, goal, expected_outcome, difficulty)
		 VALUES (?, ?, NULLIF(?, ''), NULLIF(?, ''), ?, NULLIF(?, ''), NULLIF(?, ''))`,
		s.ID, s.Role, s.Category.String, s.RepoPath.String, s.Goal, s.ExpectedOutcome.String, s.Difficulty.String)
	if err != nil {
		return fmt.Errorf("inserting eval scenario %s: %w", s.ID, err)
	}
	return nil
}

// InsertEvalVersion inserts a new eval version.
func (d *DB) InsertEvalVersion(ctx context.Context, v EvalVersion) error {
	_, err := d.ExecContext(ctx,
		`INSERT INTO eval_versions (id, parent_id, branch, commit_hash, status)
		 VALUES (?, NULLIF(?, ''), NULLIF(?, ''), NULLIF(?, ''), ?)`,
		v.ID, v.ParentID.String, v.Branch.String, v.CommitHash.String, v.Status)
	if err != nil {
		return fmt.Errorf("inserting eval version %s: %w", v.ID, err)
	}
	return nil
}

// InsertEvalRun inserts a new eval run.
func (d *DB) InsertEvalRun(ctx context.Context, r EvalRun) error {
	_, err := d.ExecContext(ctx,
		`INSERT INTO eval_runs (id, version_id, scenario_id, status)
		 VALUES (?, ?, ?, ?)`,
		r.ID, r.VersionID, r.ScenarioID, r.Status)
	if err != nil {
		return fmt.Errorf("inserting eval run %s: %w", r.ID, err)
	}
	return nil
}

// InsertEvalResult inserts a new eval result.
func (d *DB) InsertEvalResult(ctx context.Context, r EvalResult) error {
	_, err := d.ExecContext(ctx,
		`INSERT INTO eval_results (id, run_id, metric, score, weight, details)
		 VALUES (?, ?, ?, ?, ?, NULLIF(?, ''))`,
		r.ID, r.RunID, r.Metric, r.Score, r.Weight, r.Details.String)
	if err != nil {
		return fmt.Errorf("inserting eval result %s: %w", r.ID, err)
	}
	return nil
}

// InsertHealingLog inserts a new healing log entry.
func (d *DB) InsertHealingLog(ctx context.Context, h HealingLog) error {
	_, err := d.ExecContext(ctx,
		`INSERT INTO healing_log (id, session_id, task_id, error_type, fix_applied, success, rolled_back)
		 VALUES (?, ?, NULLIF(?, ''), NULLIF(?, ''), NULLIF(?, ''), ?, ?)`,
		h.ID, h.SessionID, h.TaskID.String, h.ErrorType.String, h.FixApplied.String, h.Success, h.RolledBack)
	if err != nil {
		return fmt.Errorf("inserting healing log %s: %w", h.ID, err)
	}
	return nil
}

// UpdateEvalRunStatus updates the status of an eval run. Sets completed_at for terminal states.
func (d *DB) UpdateEvalRunStatus(ctx context.Context, id, status string) error {
	var query string
	if status == "passed" || status == "failed" {
		query = `UPDATE eval_runs SET status = ?, completed_at = CURRENT_TIMESTAMP WHERE id = ?`
	} else if status == "running" {
		query = `UPDATE eval_runs SET status = ?, started_at = CURRENT_TIMESTAMP WHERE id = ?`
	} else {
		query = `UPDATE eval_runs SET status = ? WHERE id = ?`
	}
	res, err := d.ExecContext(ctx, query, status, id)
	if err != nil {
		return fmt.Errorf("updating eval run status %s: %w", id, err)
	}
	return checkRowsAffected(res, "eval_run", id)
}

// UpdateEvalVersionStatus updates the status of an eval version.
func (d *DB) UpdateEvalVersionStatus(ctx context.Context, id, status string) error {
	res, err := d.ExecContext(ctx,
		`UPDATE eval_versions SET status = ? WHERE id = ?`, status, id)
	if err != nil {
		return fmt.Errorf("updating eval version status %s: %w", id, err)
	}
	return checkRowsAffected(res, "eval_version", id)
}

// --- Plan cache operations (I-039) ---

// StorePlanCache inserts or replaces a plan cache entry.
// expires_at is computed as created_at + ttl_days.
func (d *DB) StorePlanCache(ctx context.Context, goalHash, goalText, w5h2Key, keywords, planJSON, actionType string, tier, ttlDays int, fileMtimes string) error {
	_, err := d.ExecContext(ctx,
		`INSERT OR REPLACE INTO plan_cache
		   (goal_hash, goal_text, w5h2_key, keywords, plan_json, action_type, tier, ttl_days, fail_count, hit_count, file_mtimes, created_at, expires_at)
		 VALUES (?, ?, NULLIF(?, ''), NULLIF(?, ''), ?, NULLIF(?, ''), NULLIF(?, 0), ?,
		         0, 0, NULLIF(?, ''),
		         CURRENT_TIMESTAMP,
		         datetime(CURRENT_TIMESTAMP, '+' || ? || ' days'))`,
		goalHash, goalText, w5h2Key, keywords, planJSON, actionType, tier, ttlDays,
		fileMtimes, ttlDays)
	if err != nil {
		return fmt.Errorf("storing plan cache for hash %s: %w", goalHash, err)
	}
	return nil
}

// InvalidatePlanCache deletes a plan cache entry by goal hash.
func (d *DB) InvalidatePlanCache(ctx context.Context, goalHash string) error {
	_, err := d.ExecContext(ctx,
		`DELETE FROM plan_cache WHERE goal_hash = ?`, goalHash)
	if err != nil {
		return fmt.Errorf("invalidating plan cache for hash %s: %w", goalHash, err)
	}
	return nil
}

// RecordCacheOutcome increments hit_count or fail_count for a plan cache entry.
// If fail_count reaches 2 or more after increment, the row is automatically deleted.
// field must be "hit_count" or "fail_count".
func (d *DB) RecordCacheOutcome(ctx context.Context, goalHash, field string) error {
	if field != "hit_count" && field != "fail_count" {
		return fmt.Errorf("invalid cache outcome field %q: must be hit_count or fail_count", field)
	}
	_, err := d.ExecContext(ctx,
		fmt.Sprintf(`UPDATE plan_cache SET %s = %s + 1 WHERE goal_hash = ?`, field, field),
		goalHash)
	if err != nil {
		return fmt.Errorf("recording cache outcome %s for hash %s: %w", field, goalHash, err)
	}
	if field == "fail_count" {
		_, err = d.ExecContext(ctx,
			`DELETE FROM plan_cache WHERE goal_hash = ? AND fail_count >= 2`, goalHash)
		if err != nil {
			return fmt.Errorf("auto-deleting failed plan cache for hash %s: %w", goalHash, err)
		}
	}
	return nil
}

// --- Helpers ---

// GetAgentByTask looks up the agent assigned to a task and returns the agent row.
func (d *DB) GetAgentByTask(ctx context.Context, taskID string) (*Agent, error) {
	var a Agent
	err := d.QueryRowContext(ctx,
		`SELECT a.id, a.role, a.status, a.worktree, a.pid, a.current_task, a.heartbeat_at, a.created_at
		 FROM agents a JOIN tasks t ON t.assigned_to = a.id WHERE t.id = ?`, taskID).
		Scan(&a.ID, &a.Role, &a.Status, &a.Worktree, &a.PID, &a.CurrentTask, &a.HeartbeatAt, &a.CreatedAt)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, fmt.Errorf("getting agent by task %s: %w", taskID, err)
	}
	return &a, nil
}

func checkRowsAffected(res sql.Result, entity, id string) error {
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("checking rows affected: %w", err)
	}
	if n == 0 {
		return fmt.Errorf("%s %q not found", entity, id)
	}
	return nil
}
