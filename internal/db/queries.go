package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
)

// --- Conductor queries ---

// GetConductor returns a single conductor by ID, or nil if not found.
func (d *DB) GetConductor(ctx context.Context, id string) (*ConductorRecord, error) {
	var c ConductorRecord
	var mergeReview, repoMap, lenientDeps int
	var rawBranches, phaseID sql.NullString
	err := d.QueryRowContext(ctx,
		`SELECT id, pid, goal, status, staging_branch, base_branch,
		        max_parallel, test_cmd, merge_review, model_strategy, runtime,
		        repo_map, lenient_deps, file_enforcement, merge_mode, merge_strategy, phase_id,
		        started_at, heartbeat_at, completed_at,
		        merge_status, merge_started_at, merge_branches_done
		 FROM conductors WHERE id = ?`, id).
		Scan(&c.ID, &c.PID, &c.Goal, &c.Status, &c.StagingBranch, &c.BaseBranch,
			&c.MaxParallel, &c.TestCmd, &mergeReview, &c.ModelStrategy, &c.Runtime,
			&repoMap, &lenientDeps, &c.FileEnforcement, &c.MergeMode, &c.MergeStrategy, &phaseID,
			&c.StartedAt, &c.HeartbeatAt, &c.CompletedAt,
			&c.MergeStatus, &c.MergeStartedAt, &rawBranches)
	if err != nil {
		if err.Error() == "sql: no rows in result set" {
			return nil, nil
		}
		return nil, fmt.Errorf("getting conductor %s: %w", id, err)
	}
	c.MergeReview = mergeReview != 0
	c.RepoMap = repoMap != 0
	c.LenientDeps = lenientDeps != 0
	c.MergeBranchesDone = parseBranchesJSON(rawBranches)
	c.PhaseID = phaseID.String
	return &c, nil
}

// ListActiveConductors returns all conductors with status 'active'.
func (d *DB) ListActiveConductors(ctx context.Context) ([]ConductorRecord, error) {
	rows, err := d.QueryContext(ctx,
		`SELECT id, pid, goal, status, staging_branch, base_branch,
		        max_parallel, test_cmd, merge_review, model_strategy, runtime,
		        repo_map, lenient_deps, file_enforcement, merge_mode, merge_strategy, phase_id,
		        started_at, heartbeat_at, completed_at,
		        merge_status, merge_started_at, merge_branches_done
		 FROM conductors WHERE status = 'active' ORDER BY started_at DESC`)
	if err != nil {
		return nil, fmt.Errorf("listing active conductors: %w", err)
	}
	defer rows.Close()
	return scanConductorRows(rows)
}

// GetConductorByPID returns the active conductor with the given PID, or nil.
func (d *DB) GetConductorByPID(ctx context.Context, pid int) (*ConductorRecord, error) {
	var c ConductorRecord
	var mergeReview, repoMap, lenientDeps int
	var rawBranches, phaseID sql.NullString
	err := d.QueryRowContext(ctx,
		`SELECT id, pid, goal, status, staging_branch, base_branch,
		        max_parallel, test_cmd, merge_review, model_strategy, runtime,
		        repo_map, lenient_deps, file_enforcement, merge_mode, merge_strategy, phase_id,
		        started_at, heartbeat_at, completed_at,
		        merge_status, merge_started_at, merge_branches_done
		 FROM conductors WHERE pid = ? AND status = 'active'`, pid).
		Scan(&c.ID, &c.PID, &c.Goal, &c.Status, &c.StagingBranch, &c.BaseBranch,
			&c.MaxParallel, &c.TestCmd, &mergeReview, &c.ModelStrategy, &c.Runtime,
			&repoMap, &lenientDeps, &c.FileEnforcement, &c.MergeMode, &c.MergeStrategy, &phaseID,
			&c.StartedAt, &c.HeartbeatAt, &c.CompletedAt,
			&c.MergeStatus, &c.MergeStartedAt, &rawBranches)
	if err != nil {
		if err.Error() == "sql: no rows in result set" {
			return nil, nil
		}
		return nil, fmt.Errorf("getting conductor by PID %d: %w", pid, err)
	}
	c.MergeReview = mergeReview != 0
	c.RepoMap = repoMap != 0
	c.LenientDeps = lenientDeps != 0
	c.MergeBranchesDone = parseBranchesJSON(rawBranches)
	c.PhaseID = phaseID.String
	return &c, nil
}

// ListAllConductors returns all conductors ordered by started_at desc.
func (d *DB) ListAllConductors(ctx context.Context) ([]ConductorRecord, error) {
	rows, err := d.QueryContext(ctx,
		`SELECT id, pid, goal, status, staging_branch, base_branch,
		        max_parallel, test_cmd, merge_review, model_strategy, runtime,
		        repo_map, lenient_deps, file_enforcement, merge_mode, merge_strategy, phase_id,
		        started_at, heartbeat_at, completed_at,
		        merge_status, merge_started_at, merge_branches_done
		 FROM conductors ORDER BY started_at DESC`)
	if err != nil {
		return nil, fmt.Errorf("listing all conductors: %w", err)
	}
	defer rows.Close()
	return scanConductorRows(rows)
}

func scanConductorRows(rows interface {
	Next() bool
	Scan(dest ...interface{}) error
	Err() error
}) ([]ConductorRecord, error) {
	var conductors []ConductorRecord
	for rows.Next() {
		var c ConductorRecord
		var mergeReview, repoMap, lenientDeps int
		var rawBranches, phaseID sql.NullString
		if err := rows.Scan(&c.ID, &c.PID, &c.Goal, &c.Status, &c.StagingBranch, &c.BaseBranch,
			&c.MaxParallel, &c.TestCmd, &mergeReview, &c.ModelStrategy, &c.Runtime,
			&repoMap, &lenientDeps, &c.FileEnforcement, &c.MergeMode, &c.MergeStrategy, &phaseID,
			&c.StartedAt, &c.HeartbeatAt, &c.CompletedAt,
			&c.MergeStatus, &c.MergeStartedAt, &rawBranches); err != nil {
			return nil, fmt.Errorf("scanning conductor row: %w", err)
		}
		c.MergeReview = mergeReview != 0
		c.RepoMap = repoMap != 0
		c.LenientDeps = lenientDeps != 0
		c.MergeBranchesDone = parseBranchesJSON(rawBranches)
		c.PhaseID = phaseID.String
		conductors = append(conductors, c)
	}
	return conductors, rows.Err()
}

// parseBranchesJSON deserializes the merge_branches_done JSON TEXT column
// into a []string slice, handling sql.NullString nil/empty cases.
func parseBranchesJSON(raw sql.NullString) []string {
	if !raw.Valid || raw.String == "" {
		return nil
	}
	var branches []string
	if err := json.Unmarshal([]byte(raw.String), &branches); err != nil {
		return nil
	}
	return branches
}

// GetConductorByPhaseID returns the most recently started conductor for a given
// phase_id that has reached a completed or active status. Used by B-287 to resolve
// the prior phase's staging branch when resuming with --start-phase.
func (d *DB) GetConductorByPhaseID(ctx context.Context, phaseID string) (*ConductorRecord, error) {
	var c ConductorRecord
	var mergeReview, repoMap, lenientDeps int
	var rawBranches, scannedPhaseID sql.NullString
	err := d.QueryRowContext(ctx,
		`SELECT id, pid, goal, status, staging_branch, base_branch,
		        max_parallel, test_cmd, merge_review, model_strategy, runtime,
		        repo_map, lenient_deps, file_enforcement, merge_mode, merge_strategy, phase_id,
		        started_at, heartbeat_at, completed_at,
		        merge_status, merge_started_at, merge_branches_done
		 FROM conductors
		 WHERE phase_id = ? AND status IN ('completed', 'active')
		 ORDER BY started_at DESC LIMIT 1`, phaseID).
		Scan(&c.ID, &c.PID, &c.Goal, &c.Status, &c.StagingBranch, &c.BaseBranch,
			&c.MaxParallel, &c.TestCmd, &mergeReview, &c.ModelStrategy, &c.Runtime,
			&repoMap, &lenientDeps, &c.FileEnforcement, &c.MergeMode, &c.MergeStrategy, &scannedPhaseID,
			&c.StartedAt, &c.HeartbeatAt, &c.CompletedAt,
			&c.MergeStatus, &c.MergeStartedAt, &rawBranches)
	if err != nil {
		if err.Error() == "sql: no rows in result set" {
			return nil, nil
		}
		return nil, fmt.Errorf("getting conductor by phase %s: %w", phaseID, err)
	}
	c.MergeReview = mergeReview != 0
	c.RepoMap = repoMap != 0
	c.LenientDeps = lenientDeps != 0
	c.MergeBranchesDone = parseBranchesJSON(rawBranches)
	c.PhaseID = scannedPhaseID.String
	return &c, nil
}

// ListTaskIDsByConductor returns task IDs associated with a specific conductor.
func (d *DB) ListTaskIDsByConductor(ctx context.Context, conductorID string) ([]string, error) {
	rows, err := d.QueryContext(ctx,
		`SELECT id FROM tasks WHERE conductor_id = ? ORDER BY created_at ASC`, conductorID)
	if err != nil {
		return nil, fmt.Errorf("listing task IDs for conductor %s: %w", conductorID, err)
	}
	defer rows.Close()

	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("scanning task ID: %w", err)
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// ListTaskIDsByPhase returns task IDs for a specific conductor + phase combination (G110).
// Used by Merge() to filter branches to only the current phase's tasks.
func (d *DB) ListTaskIDsByPhase(ctx context.Context, conductorID, phaseID string) ([]string, error) {
	rows, err := d.QueryContext(ctx,
		`SELECT id FROM tasks WHERE conductor_id = ? AND phase_id = ? ORDER BY created_at ASC`,
		conductorID, phaseID)
	if err != nil {
		return nil, fmt.Errorf("listing task IDs for conductor %s phase %s: %w", conductorID, phaseID, err)
	}
	defer rows.Close()

	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("scanning task ID: %w", err)
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// --- Merge queue queries (B-280) ---

// NextPendingMergeQueueEntry returns the next pending merge queue entry for a conductor,
// ordered by position (FIFO). Returns nil if no entries are pending.
func (d *DB) NextPendingMergeQueueEntry(ctx context.Context, conductorID string) (*MergeQueueEntry, error) {
	var e MergeQueueEntry
	err := d.QueryRowContext(ctx,
		`SELECT id, conductor_id, task_id, branch, position, status,
		        resolution_tier, conflict_files, test_result, error_message,
		        refinement_count, enqueued_at, merged_at
		 FROM merge_queue_entries
		 WHERE conductor_id = ? AND status = 'pending'
		 ORDER BY position ASC LIMIT 1`, conductorID).
		Scan(&e.ID, &e.ConductorID, &e.TaskID, &e.Branch, &e.Position, &e.Status,
			&e.ResolutionTier, &e.ConflictFiles, &e.TestResult, &e.ErrorMessage,
			&e.RefinementCount, &e.EnqueuedAt, &e.MergedAt)
	if err != nil {
		if err.Error() == "sql: no rows in result set" {
			return nil, nil
		}
		return nil, fmt.Errorf("getting next pending merge queue entry: %w", err)
	}
	return &e, nil
}

// ListMergeQueueEntries returns all merge queue entries for a conductor, ordered by position.
func (d *DB) ListMergeQueueEntries(ctx context.Context, conductorID string) ([]MergeQueueEntry, error) {
	rows, err := d.QueryContext(ctx,
		`SELECT id, conductor_id, task_id, branch, position, status,
		        resolution_tier, conflict_files, test_result, error_message,
		        refinement_count, enqueued_at, merged_at
		 FROM merge_queue_entries
		 WHERE conductor_id = ?
		 ORDER BY position ASC`, conductorID)
	if err != nil {
		return nil, fmt.Errorf("listing merge queue entries: %w", err)
	}
	defer rows.Close()

	var entries []MergeQueueEntry
	for rows.Next() {
		var e MergeQueueEntry
		if err := rows.Scan(&e.ID, &e.ConductorID, &e.TaskID, &e.Branch, &e.Position, &e.Status,
			&e.ResolutionTier, &e.ConflictFiles, &e.TestResult, &e.ErrorMessage,
			&e.RefinementCount, &e.EnqueuedAt, &e.MergedAt); err != nil {
			return nil, fmt.Errorf("scanning merge queue entry: %w", err)
		}
		entries = append(entries, e)
	}
	return entries, rows.Err()
}

// CountMergeQueueByStatus counts merge queue entries by status for a conductor.
func (d *DB) CountMergeQueueByStatus(ctx context.Context, conductorID string) (map[string]int, error) {
	rows, err := d.QueryContext(ctx,
		`SELECT status, COUNT(*) FROM merge_queue_entries WHERE conductor_id = ? GROUP BY status`, conductorID)
	if err != nil {
		return nil, fmt.Errorf("counting merge queue by status: %w", err)
	}
	defer rows.Close()

	counts := make(map[string]int)
	for rows.Next() {
		var status string
		var count int
		if err := rows.Scan(&status, &count); err != nil {
			return nil, fmt.Errorf("scanning merge queue count: %w", err)
		}
		counts[status] = count
	}
	return counts, rows.Err()
}

// GetMergeQueueEntryByTaskID returns the merge queue entry for a given task, or nil.
func (d *DB) GetMergeQueueEntryByTaskID(ctx context.Context, conductorID, taskID string) (*MergeQueueEntry, error) {
	var e MergeQueueEntry
	err := d.QueryRowContext(ctx,
		`SELECT id, conductor_id, task_id, branch, position, status,
		        resolution_tier, conflict_files, test_result, error_message,
		        refinement_count, enqueued_at, merged_at
		 FROM merge_queue_entries
		 WHERE conductor_id = ? AND task_id = ?`, conductorID, taskID).
		Scan(&e.ID, &e.ConductorID, &e.TaskID, &e.Branch, &e.Position, &e.Status,
			&e.ResolutionTier, &e.ConflictFiles, &e.TestResult, &e.ErrorMessage,
			&e.RefinementCount, &e.EnqueuedAt, &e.MergedAt)
	if err != nil {
		if err.Error() == "sql: no rows in result set" {
			return nil, nil
		}
		return nil, fmt.Errorf("getting merge queue entry by task %s: %w", taskID, err)
	}
	return &e, nil
}

// --- Agent queries ---

// ListAgents returns all agents ordered by created_at desc.
func (d *DB) ListAgents(ctx context.Context) ([]Agent, error) {
	rows, err := d.QueryContext(ctx,
		`SELECT id, role, status, worktree, pid, current_task, heartbeat_at, created_at
		 FROM agents ORDER BY created_at DESC`)
	if err != nil {
		return nil, fmt.Errorf("listing agents: %w", err)
	}
	defer rows.Close()

	var agents []Agent
	for rows.Next() {
		var a Agent
		if err := rows.Scan(&a.ID, &a.Role, &a.Status, &a.Worktree, &a.PID,
			&a.CurrentTask, &a.HeartbeatAt, &a.CreatedAt); err != nil {
			return nil, fmt.Errorf("scanning agent row: %w", err)
		}
		agents = append(agents, a)
	}
	return agents, rows.Err()
}

// ListTasks returns all tasks ordered by status priority then created_at.
func (d *DB) ListTasks(ctx context.Context) ([]Task, error) {
	rows, err := d.QueryContext(ctx,
		`SELECT id, title, description, acceptance_criteria, status, priority, priority_label, role, assigned_to,
		        depends_on, blocked_by, worktree, branch, result, conductor_id, phase_id, feature_cluster, created_at,
		        started_at, completed_at
		 FROM tasks
		 ORDER BY
		   CASE status
		     WHEN 'running' THEN 0
		     WHEN 'assigned' THEN 1
		     WHEN 'pending' THEN 2
		     WHEN 'done' THEN 3
		     WHEN 'failed' THEN 4
		     ELSE 5
		   END,
		   created_at ASC`)
	if err != nil {
		return nil, fmt.Errorf("listing tasks: %w", err)
	}
	defer rows.Close()

	var tasks []Task
	for rows.Next() {
		var t Task
		if err := rows.Scan(&t.ID, &t.Title, &t.Description, &t.AcceptanceCriteria, &t.Status, &t.Priority,
			&t.PriorityLabel, &t.Role, &t.AssignedTo, &t.DependsOn, &t.BlockedBy, &t.Worktree,
			&t.Branch, &t.Result, &t.ConductorID, &t.PhaseID, &t.FeatureCluster, &t.CreatedAt, &t.StartedAt, &t.CompletedAt); err != nil {
			return nil, fmt.Errorf("scanning task row: %w", err)
		}
		tasks = append(tasks, t)
	}
	return tasks, rows.Err()
}

// TaskSummaryByStatus returns task counts grouped by status.
func (d *DB) TaskSummaryByStatus(ctx context.Context) (map[string]int, error) {
	rows, err := d.QueryContext(ctx,
		`SELECT status, COUNT(*) FROM tasks GROUP BY status`)
	if err != nil {
		return nil, fmt.Errorf("querying task summary: %w", err)
	}
	defer rows.Close()

	counts := make(map[string]int)
	for rows.Next() {
		var status string
		var count int
		if err := rows.Scan(&status, &count); err != nil {
			return nil, fmt.Errorf("scanning task summary row: %w", err)
		}
		counts[status] = count
	}
	return counts, rows.Err()
}

// AgentSummaryByStatus returns agent counts grouped by status.
func (d *DB) AgentSummaryByStatus(ctx context.Context) (map[string]int, error) {
	rows, err := d.QueryContext(ctx,
		`SELECT status, COUNT(*) FROM agents GROUP BY status`)
	if err != nil {
		return nil, fmt.Errorf("querying agent summary: %w", err)
	}
	defer rows.Close()

	counts := make(map[string]int)
	for rows.Next() {
		var status string
		var count int
		if err := rows.Scan(&status, &count); err != nil {
			return nil, fmt.Errorf("scanning agent summary row: %w", err)
		}
		counts[status] = count
	}
	return counts, rows.Err()
}

// ListFileLocks returns all active file locks.
func (d *DB) ListFileLocks(ctx context.Context) ([]FileLock, error) {
	rows, err := d.QueryContext(ctx,
		`SELECT file_path, locked_by, task_id, locked_at, expires_at
		 FROM file_locks ORDER BY locked_at DESC`)
	if err != nil {
		return nil, fmt.Errorf("listing file locks: %w", err)
	}
	defer rows.Close()

	var locks []FileLock
	for rows.Next() {
		var l FileLock
		if err := rows.Scan(&l.FilePath, &l.LockedBy, &l.TaskID, &l.LockedAt, &l.ExpiresAt); err != nil {
			return nil, fmt.Errorf("scanning file lock row: %w", err)
		}
		locks = append(locks, l)
	}
	return locks, rows.Err()
}

// ListFileLocksForTask returns file locks owned by a specific task.
func (d *DB) ListFileLocksForTask(ctx context.Context, taskID string) ([]FileLock, error) {
	rows, err := d.QueryContext(ctx,
		`SELECT file_path, locked_by, task_id, locked_at, expires_at
		 FROM file_locks WHERE task_id = ? ORDER BY locked_at DESC`, taskID)
	if err != nil {
		return nil, fmt.Errorf("listing file locks for task %s: %w", taskID, err)
	}
	defer rows.Close()

	var locks []FileLock
	for rows.Next() {
		var l FileLock
		if err := rows.Scan(&l.FilePath, &l.LockedBy, &l.TaskID, &l.LockedAt, &l.ExpiresAt); err != nil {
			return nil, fmt.Errorf("scanning file lock row: %w", err)
		}
		locks = append(locks, l)
	}
	return locks, rows.Err()
}

// RecentEvents returns the N most recent events.
func (d *DB) RecentEvents(ctx context.Context, limit int) ([]Event, error) {
	rows, err := d.QueryContext(ctx,
		`SELECT id, timestamp, agent_id, task_id, event_type, payload
		 FROM events ORDER BY id DESC LIMIT ?`, limit)
	if err != nil {
		return nil, fmt.Errorf("listing events: %w", err)
	}
	defer rows.Close()

	var events []Event
	for rows.Next() {
		var e Event
		if err := rows.Scan(&e.ID, &e.Timestamp, &e.AgentID, &e.TaskID,
			&e.EventType, &e.Payload); err != nil {
			return nil, fmt.Errorf("scanning event row: %w", err)
		}
		events = append(events, e)
	}
	return events, rows.Err()
}

// GetToolCallHistory returns recent events for a given agent filtered by event type.
// Used by the stall detector to analyze agent activity patterns.
func (d *DB) GetToolCallHistory(ctx context.Context, agentID, eventType string, limit int) ([]Event, error) {
	rows, err := d.QueryContext(ctx,
		`SELECT id, timestamp, agent_id, task_id, event_type, payload
		 FROM events
		 WHERE agent_id = ? AND event_type = ?
		 ORDER BY timestamp DESC
		 LIMIT ?`, agentID, eventType, limit)
	if err != nil {
		return nil, fmt.Errorf("querying tool call history for agent %s: %w", agentID, err)
	}
	defer rows.Close()

	var events []Event
	for rows.Next() {
		var e Event
		if err := rows.Scan(&e.ID, &e.Timestamp, &e.AgentID, &e.TaskID,
			&e.EventType, &e.Payload); err != nil {
			return nil, fmt.Errorf("scanning tool call history row: %w", err)
		}
		events = append(events, e)
	}
	return events, rows.Err()
}

// GetBlackboardValue returns the value for a blackboard key, or empty string if not found.
func (d *DB) GetBlackboardValue(ctx context.Context, key string) (string, error) {
	var value string
	err := d.QueryRowContext(ctx,
		`SELECT value FROM blackboard WHERE key = ?`, key).Scan(&value)
	if err != nil {
		if err.Error() == "sql: no rows in result set" {
			return "", nil
		}
		return "", fmt.Errorf("querying blackboard key %q: %w", key, err)
	}
	return value, nil
}

// GetTaskByID returns a single task by ID, or nil if not found.
func (d *DB) GetTaskByID(ctx context.Context, taskID string) (*Task, error) {
	var t Task
	err := d.QueryRowContext(ctx,
		`SELECT id, title, description, acceptance_criteria, status, priority, priority_label, role, assigned_to,
		        depends_on, blocked_by, worktree, branch, result, conductor_id, phase_id, feature_cluster, created_at,
		        started_at, completed_at
		 FROM tasks WHERE id = ?`, taskID).
		Scan(&t.ID, &t.Title, &t.Description, &t.AcceptanceCriteria, &t.Status, &t.Priority,
			&t.PriorityLabel, &t.Role, &t.AssignedTo, &t.DependsOn, &t.BlockedBy, &t.Worktree,
			&t.Branch, &t.Result, &t.ConductorID, &t.PhaseID, &t.FeatureCluster, &t.CreatedAt, &t.StartedAt, &t.CompletedAt)
	if err != nil {
		if err.Error() == "sql: no rows in result set" {
			return nil, nil
		}
		return nil, fmt.Errorf("getting task %s: %w", taskID, err)
	}
	return &t, nil
}

// ListActiveAgentsWithPID returns agents with status idle/working and a non-null PID.
func (d *DB) ListActiveAgentsWithPID(ctx context.Context) ([]Agent, error) {
	rows, err := d.QueryContext(ctx,
		`SELECT id, role, status, worktree, pid, current_task, heartbeat_at, created_at
		 FROM agents
		 WHERE status IN ('idle', 'working', 'active') AND pid IS NOT NULL`)
	if err != nil {
		return nil, fmt.Errorf("listing active agents with PID: %w", err)
	}
	defer rows.Close()

	var agents []Agent
	for rows.Next() {
		var a Agent
		if err := rows.Scan(&a.ID, &a.Role, &a.Status, &a.Worktree, &a.PID,
			&a.CurrentTask, &a.HeartbeatAt, &a.CreatedAt); err != nil {
			return nil, fmt.Errorf("scanning active agent row: %w", err)
		}
		agents = append(agents, a)
	}
	return agents, rows.Err()
}

// ListUnblockedPendingTasks returns pending tasks whose blockers are all done.
// Failed blockers do NOT unblock dependents — the dependent needs its predecessor's output.
// If sessionTaskIDs is non-empty, only tasks in that set are considered.
func (d *DB) ListUnblockedPendingTasks(ctx context.Context, sessionTaskIDs []string, limit int) ([]Task, error) {
	query := `SELECT t.id, t.title, t.description, t.acceptance_criteria, t.status, t.priority, t.priority_label, t.role, t.assigned_to,
	                 t.depends_on, t.blocked_by, t.worktree, t.branch, t.result, t.conductor_id, t.phase_id, t.feature_cluster, t.created_at,
	                 t.started_at, t.completed_at
	          FROM tasks t
	          WHERE t.status = 'pending'
	            AND (t.blocked_by IS NULL OR t.blocked_by = '[]'
	                 OR NOT EXISTS (
	                   SELECT 1 FROM json_each(t.blocked_by) je
	                   JOIN tasks t2 ON t2.id = je.value
	                   WHERE t2.status <> 'done'
	                 ))`

	var args []interface{}

	if len(sessionTaskIDs) > 0 {
		placeholders := make([]string, len(sessionTaskIDs))
		for i, id := range sessionTaskIDs {
			placeholders[i] = "?"
			args = append(args, id)
		}
		query += fmt.Sprintf(" AND t.id IN (%s)", joinStrings(placeholders, ","))
	}

	query += " ORDER BY t.priority DESC, t.created_at ASC LIMIT ?"
	args = append(args, limit)

	rows, err := d.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("listing unblocked pending tasks: %w", err)
	}
	defer rows.Close()

	var tasks []Task
	for rows.Next() {
		var t Task
		if err := rows.Scan(&t.ID, &t.Title, &t.Description, &t.AcceptanceCriteria, &t.Status, &t.Priority,
			&t.PriorityLabel, &t.Role, &t.AssignedTo, &t.DependsOn, &t.BlockedBy, &t.Worktree,
			&t.Branch, &t.Result, &t.ConductorID, &t.PhaseID, &t.FeatureCluster, &t.CreatedAt, &t.StartedAt, &t.CompletedAt); err != nil {
			return nil, fmt.Errorf("scanning unblocked task row: %w", err)
		}
		tasks = append(tasks, t)
	}
	return tasks, rows.Err()
}

// CountTasksByStatuses counts tasks matching the given statuses.
// If sessionTaskIDs is non-empty, only tasks in that set are counted.
func (d *DB) CountTasksByStatuses(ctx context.Context, statuses []string, sessionTaskIDs []string) (int, error) {
	if len(statuses) == 0 {
		return 0, nil
	}
	placeholders := make([]string, len(statuses))
	var args []interface{}
	for i, s := range statuses {
		placeholders[i] = "?"
		args = append(args, s)
	}
	query := fmt.Sprintf(`SELECT COUNT(*) FROM tasks WHERE status IN (%s)`, joinStrings(placeholders, ","))

	if len(sessionTaskIDs) > 0 {
		sessionPlaceholders := make([]string, len(sessionTaskIDs))
		for i, id := range sessionTaskIDs {
			sessionPlaceholders[i] = "?"
			args = append(args, id)
		}
		query += fmt.Sprintf(" AND id IN (%s)", joinStrings(sessionPlaceholders, ","))
	}

	var count int
	if err := d.QueryRowContext(ctx, query, args...).Scan(&count); err != nil {
		return 0, fmt.Errorf("counting tasks by statuses: %w", err)
	}
	return count, nil
}

// ListBlackboardByPrefix returns all blackboard entries whose key starts with prefix.
func (d *DB) ListBlackboardByPrefix(ctx context.Context, prefix string) ([]BlackboardEntry, error) {
	rows, err := d.QueryContext(ctx,
		`SELECT key, value, written_by, updated_at FROM blackboard WHERE key LIKE ? || '%'`, prefix)
	if err != nil {
		return nil, fmt.Errorf("listing blackboard by prefix %q: %w", prefix, err)
	}
	defer rows.Close()

	var entries []BlackboardEntry
	for rows.Next() {
		var e BlackboardEntry
		if err := rows.Scan(&e.Key, &e.Value, &e.WrittenBy, &e.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scanning blackboard row: %w", err)
		}
		entries = append(entries, e)
	}
	return entries, rows.Err()
}

// GetPendingTasksWithAllBlockersTerminal returns pending tasks where every blocker
// is in a terminal state (done or failed) and at least one blocker failed.
// These tasks should be cascade-failed because their failed predecessor cannot provide output.
// If sessionTaskIDs is non-empty, only tasks in that set are considered.
func (d *DB) GetPendingTasksWithAllBlockersTerminal(ctx context.Context, sessionTaskIDs []string) ([]Task, error) {
	query := `SELECT t.id, t.title, t.description, t.acceptance_criteria, t.status, t.priority, t.priority_label, t.role, t.assigned_to,
	                 t.depends_on, t.blocked_by, t.worktree, t.branch, t.result, t.conductor_id, t.phase_id, t.feature_cluster, t.created_at,
	                 t.started_at, t.completed_at
	          FROM tasks t
	          WHERE t.status = 'pending'
	            AND t.blocked_by IS NOT NULL AND t.blocked_by <> '[]'
	            -- All blockers are terminal (done or failed)
	            AND NOT EXISTS (
	              SELECT 1 FROM json_each(t.blocked_by) je
	              JOIN tasks t2 ON t2.id = je.value
	              WHERE t2.status NOT IN ('done', 'failed')
	            )
	            -- At least one blocker failed
	            AND EXISTS (
	              SELECT 1 FROM json_each(t.blocked_by) je
	              JOIN tasks t2 ON t2.id = je.value
	              WHERE t2.status = 'failed'
	            )`

	var args []interface{}

	if len(sessionTaskIDs) > 0 {
		placeholders := make([]string, len(sessionTaskIDs))
		for i, id := range sessionTaskIDs {
			placeholders[i] = "?"
			args = append(args, id)
		}
		query += fmt.Sprintf(" AND t.id IN (%s)", joinStrings(placeholders, ","))
	}

	rows, err := d.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("listing pending tasks with all blockers terminal: %w", err)
	}
	defer rows.Close()

	var tasks []Task
	for rows.Next() {
		var t Task
		if err := rows.Scan(&t.ID, &t.Title, &t.Description, &t.AcceptanceCriteria, &t.Status, &t.Priority,
			&t.PriorityLabel, &t.Role, &t.AssignedTo, &t.DependsOn, &t.BlockedBy, &t.Worktree,
			&t.Branch, &t.Result, &t.ConductorID, &t.PhaseID, &t.FeatureCluster, &t.CreatedAt, &t.StartedAt, &t.CompletedAt); err != nil {
			return nil, fmt.Errorf("scanning cascade-fail candidate row: %w", err)
		}
		tasks = append(tasks, t)
	}
	return tasks, rows.Err()
}

// GetPendingTasksWithMixedBlockerOutcomes returns pending tasks where all blockers
// are terminal, at least one failed, but at least one succeeded (done).
// In lenient mode, these tasks can proceed with partial predecessor output.
// If sessionTaskIDs is non-empty, only tasks in that set are considered.
func (d *DB) GetPendingTasksWithMixedBlockerOutcomes(ctx context.Context, sessionTaskIDs []string) ([]Task, error) {
	query := `SELECT t.id, t.title, t.description, t.acceptance_criteria, t.status, t.priority, t.priority_label, t.role, t.assigned_to,
	                 t.depends_on, t.blocked_by, t.worktree, t.branch, t.result, t.conductor_id, t.phase_id, t.feature_cluster, t.created_at,
	                 t.started_at, t.completed_at
	          FROM tasks t
	          WHERE t.status = 'pending'
	            AND t.blocked_by IS NOT NULL AND t.blocked_by <> '[]'
	            -- All blockers are terminal (done or failed)
	            AND NOT EXISTS (
	              SELECT 1 FROM json_each(t.blocked_by) je
	              JOIN tasks t2 ON t2.id = je.value
	              WHERE t2.status NOT IN ('done', 'failed')
	            )
	            -- At least one blocker failed
	            AND EXISTS (
	              SELECT 1 FROM json_each(t.blocked_by) je
	              JOIN tasks t2 ON t2.id = je.value
	              WHERE t2.status = 'failed'
	            )
	            -- At least one blocker succeeded
	            AND EXISTS (
	              SELECT 1 FROM json_each(t.blocked_by) je
	              JOIN tasks t2 ON t2.id = je.value
	              WHERE t2.status = 'done'
	            )`

	var args []interface{}

	if len(sessionTaskIDs) > 0 {
		placeholders := make([]string, len(sessionTaskIDs))
		for i, id := range sessionTaskIDs {
			placeholders[i] = "?"
			args = append(args, id)
		}
		query += fmt.Sprintf(" AND t.id IN (%s)", joinStrings(placeholders, ","))
	}

	rows, err := d.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("listing pending tasks with mixed blocker outcomes: %w", err)
	}
	defer rows.Close()

	var tasks []Task
	for rows.Next() {
		var t Task
		if err := rows.Scan(&t.ID, &t.Title, &t.Description, &t.AcceptanceCriteria, &t.Status, &t.Priority,
			&t.PriorityLabel, &t.Role, &t.AssignedTo, &t.DependsOn, &t.BlockedBy, &t.Worktree,
			&t.Branch, &t.Result, &t.ConductorID, &t.PhaseID, &t.FeatureCluster, &t.CreatedAt, &t.StartedAt, &t.CompletedAt); err != nil {
			return nil, fmt.Errorf("scanning mixed-blocker task row: %w", err)
		}
		tasks = append(tasks, t)
	}
	return tasks, rows.Err()
}

// ListLenientPendingTasks returns pending tasks that have been annotated with
// lenient_deps:{taskID}=1 in the blackboard. These are tasks whose blockers have
// mixed outcomes (some done, some failed) and have been marked for lenient unblocking.
// If sessionTaskIDs is non-empty, only tasks in that set are considered.
func (d *DB) ListLenientPendingTasks(ctx context.Context, sessionTaskIDs []string, limit int) ([]Task, error) {
	query := `SELECT t.id, t.title, t.description, t.acceptance_criteria, t.status, t.priority, t.priority_label, t.role, t.assigned_to,
	                 t.depends_on, t.blocked_by, t.worktree, t.branch, t.result, t.conductor_id, t.phase_id, t.feature_cluster, t.created_at,
	                 t.started_at, t.completed_at
	          FROM tasks t
	          JOIN blackboard b ON b.key = 'lenient_deps:' || t.id AND b.value = '1'
	          WHERE t.status = 'pending'`

	var args []interface{}

	if len(sessionTaskIDs) > 0 {
		placeholders := make([]string, len(sessionTaskIDs))
		for i, id := range sessionTaskIDs {
			placeholders[i] = "?"
			args = append(args, id)
		}
		query += fmt.Sprintf(" AND t.id IN (%s)", joinStrings(placeholders, ","))
	}

	query += " ORDER BY t.priority DESC, t.created_at ASC LIMIT ?"
	args = append(args, limit)

	rows, err := d.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("listing lenient pending tasks: %w", err)
	}
	defer rows.Close()

	var tasks []Task
	for rows.Next() {
		var t Task
		if err := rows.Scan(&t.ID, &t.Title, &t.Description, &t.AcceptanceCriteria, &t.Status, &t.Priority,
			&t.PriorityLabel, &t.Role, &t.AssignedTo, &t.DependsOn, &t.BlockedBy, &t.Worktree,
			&t.Branch, &t.Result, &t.ConductorID, &t.PhaseID, &t.FeatureCluster, &t.CreatedAt, &t.StartedAt, &t.CompletedAt); err != nil {
			return nil, fmt.Errorf("scanning lenient pending task row: %w", err)
		}
		tasks = append(tasks, t)
	}
	return tasks, rows.Err()
}

// --- Drift score queries ---

// GetDriftHistory returns drift scores for a session ordered by cycle_number.
func (d *DB) GetDriftHistory(ctx context.Context, sessionID string) ([]DriftScore, error) {
	rows, err := d.QueryContext(ctx,
		`SELECT id, session_id, cycle_number, score, explanation, action_taken, created_at
		 FROM drift_scores
		 WHERE session_id = ?
		 ORDER BY cycle_number`, sessionID)
	if err != nil {
		return nil, fmt.Errorf("querying drift history for session %s: %w", sessionID, err)
	}
	defer rows.Close()

	var scores []DriftScore
	for rows.Next() {
		var s DriftScore
		if err := rows.Scan(&s.ID, &s.SessionID, &s.CycleNumber, &s.Score,
			&s.Explanation, &s.ActionTaken, &s.CreatedAt); err != nil {
			return nil, fmt.Errorf("scanning drift score row: %w", err)
		}
		scores = append(scores, s)
	}
	return scores, rows.Err()
}

func joinStrings(strs []string, sep string) string {
	result := ""
	for i, s := range strs {
		if i > 0 {
			result += sep
		}
		result += s
	}
	return result
}

// EventsSinceForSession returns events with id > sinceID that are either conductor-level
// (task_id IS NULL) or belong to the given session task IDs. Ordered by id ASC, limit 100.
// Used by the TUI dashboard to poll conductor activity for detached sessions.
func (d *DB) EventsSinceForSession(ctx context.Context, sinceID int64, sessionTaskIDs []string) ([]Event, error) {
	query := `SELECT id, timestamp, agent_id, task_id, event_type, payload
	          FROM events
	          WHERE id > ?`
	args := []interface{}{sinceID}

	if len(sessionTaskIDs) > 0 {
		placeholders := make([]string, len(sessionTaskIDs))
		for i, id := range sessionTaskIDs {
			placeholders[i] = "?"
			args = append(args, id)
		}
		query += fmt.Sprintf(" AND (task_id IS NULL OR task_id IN (%s))", joinStrings(placeholders, ","))
	} else {
		query += " AND task_id IS NULL"
	}

	query += " ORDER BY id ASC LIMIT 100"

	rows, err := d.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("querying events since %d: %w", sinceID, err)
	}
	defer rows.Close()

	var events []Event
	for rows.Next() {
		var e Event
		if err := rows.Scan(&e.ID, &e.Timestamp, &e.AgentID, &e.TaskID,
			&e.EventType, &e.Payload); err != nil {
			return nil, fmt.Errorf("scanning event row: %w", err)
		}
		events = append(events, e)
	}
	return events, rows.Err()
}

// FindCrossConductorFileLock checks whether a file is locked by a task belonging
// to a different conductor. Returns the task ID and conductor ID of the existing
// lock holder, or empty strings if no cross-conductor overlap exists.
func (d *DB) FindCrossConductorFileLock(ctx context.Context, filePath, excludeTaskID, excludeConductorID string) (taskID, conductorID string, err error) {
	err = d.QueryRowContext(ctx,
		`SELECT fl.task_id, t.conductor_id
		 FROM file_locks fl
		 JOIN tasks t ON t.id = fl.task_id
		 WHERE fl.file_path = ?
		   AND fl.task_id != ?
		   AND t.conductor_id IS NOT NULL
		   AND t.conductor_id != ?
		 LIMIT 1`, filePath, excludeTaskID, excludeConductorID).
		Scan(&taskID, &conductorID)
	if err != nil {
		if err.Error() == "sql: no rows in result set" {
			return "", "", nil
		}
		return "", "", fmt.Errorf("finding cross-conductor file lock for %s: %w", filePath, err)
	}
	return taskID, conductorID, nil
}

// GetStatusSummary returns aggregate counts for status display.
func (d *DB) GetStatusSummary(ctx context.Context) (*StatusSummary, error) {
	// Single combined query instead of 4 separate round-trips (B-173 optimization).
	// Uses idx_tasks_status and idx_agents_status indexes for GROUP BY.
	rows, err := d.QueryContext(ctx, `
		SELECT 'task' AS source, status, COUNT(*) AS cnt FROM tasks GROUP BY status
		UNION ALL
		SELECT 'agent' AS source, status, COUNT(*) AS cnt FROM agents GROUP BY status
		UNION ALL
		SELECT 'lock' AS source, 'count' AS status, COUNT(*) AS cnt FROM file_locks
		UNION ALL
		SELECT 'event' AS source, 'count' AS status, COUNT(*) AS cnt FROM events`)
	if err != nil {
		return nil, fmt.Errorf("querying status summary: %w", err)
	}
	defer rows.Close()

	taskCounts := make(map[string]int)
	agentCounts := make(map[string]int)
	var lockCount, eventCount int

	for rows.Next() {
		var source, status string
		var count int
		if err := rows.Scan(&source, &status, &count); err != nil {
			return nil, fmt.Errorf("scanning summary row: %w", err)
		}
		switch source {
		case "task":
			taskCounts[status] = count
		case "agent":
			agentCounts[status] = count
		case "lock":
			lockCount = count
		case "event":
			eventCount = count
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating summary rows: %w", err)
	}

	taskTotal := 0
	for _, c := range taskCounts {
		taskTotal += c
	}
	agentTotal := 0
	for _, c := range agentCounts {
		agentTotal += c
	}

	return &StatusSummary{
		Tasks:      StatusCounts{Total: taskTotal, ByStatus: taskCounts},
		Agents:     StatusCounts{Total: agentTotal, ByStatus: agentCounts},
		LockCount:  lockCount,
		EventCount: eventCount,
	}, nil
}

// --- Feature Cluster queries (B-281) ---

// ListTasksByCluster returns all tasks belonging to a specific feature cluster for a conductor.
func (d *DB) ListTasksByCluster(ctx context.Context, conductorID, cluster string) ([]Task, error) {
	rows, err := d.QueryContext(ctx,
		`SELECT id, title, description, acceptance_criteria, status, priority, priority_label, role, assigned_to,
		        depends_on, blocked_by, worktree, branch, result, conductor_id, phase_id, feature_cluster, created_at,
		        started_at, completed_at
		 FROM tasks
		 WHERE conductor_id = ? AND feature_cluster = ?
		 ORDER BY priority DESC, created_at ASC`, conductorID, cluster)
	if err != nil {
		return nil, fmt.Errorf("listing tasks by cluster: %w", err)
	}
	defer rows.Close()

	var tasks []Task
	for rows.Next() {
		var t Task
		if err := rows.Scan(&t.ID, &t.Title, &t.Description, &t.AcceptanceCriteria, &t.Status, &t.Priority,
			&t.PriorityLabel, &t.Role, &t.AssignedTo, &t.DependsOn, &t.BlockedBy, &t.Worktree,
			&t.Branch, &t.Result, &t.ConductorID, &t.PhaseID, &t.FeatureCluster, &t.CreatedAt, &t.StartedAt, &t.CompletedAt); err != nil {
			return nil, fmt.Errorf("scanning task row: %w", err)
		}
		tasks = append(tasks, t)
	}
	return tasks, rows.Err()
}

// ListDistinctClusters returns the unique non-empty feature cluster names for a conductor.
func (d *DB) ListDistinctClusters(ctx context.Context, conductorID string) ([]string, error) {
	rows, err := d.QueryContext(ctx,
		`SELECT DISTINCT feature_cluster
		 FROM tasks
		 WHERE conductor_id = ? AND feature_cluster IS NOT NULL AND feature_cluster != ''
		 ORDER BY feature_cluster`, conductorID)
	if err != nil {
		return nil, fmt.Errorf("listing distinct clusters: %w", err)
	}
	defer rows.Close()

	var clusters []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, fmt.Errorf("scanning cluster name: %w", err)
		}
		clusters = append(clusters, name)
	}
	return clusters, rows.Err()
}

// DashboardSummary returns a combined summary of conductors, tasks, agents, and recent events
// suitable for the web dashboard initial load.
type DashboardSummary struct {
	Conductors []ConductorRecord
	Tasks      []Task
	Agents     []Agent
	Events     []Event
	Status     *StatusSummary
}

// GetDashboardSummary fetches all data needed for the dashboard in a single call.
func (d *DB) GetDashboardSummary(ctx context.Context) (*DashboardSummary, error) {
	conductors, err := d.ListActiveConductors(ctx)
	if err != nil {
		return nil, fmt.Errorf("dashboard summary: conductors: %w", err)
	}
	tasks, err := d.ListTasks(ctx)
	if err != nil {
		return nil, fmt.Errorf("dashboard summary: tasks: %w", err)
	}
	agents, err := d.ListAgents(ctx)
	if err != nil {
		return nil, fmt.Errorf("dashboard summary: agents: %w", err)
	}
	events, err := d.RecentEvents(ctx, 50)
	if err != nil {
		return nil, fmt.Errorf("dashboard summary: events: %w", err)
	}
	status, err := d.GetStatusSummary(ctx)
	if err != nil {
		return nil, fmt.Errorf("dashboard summary: status: %w", err)
	}
	return &DashboardSummary{
		Conductors: conductors,
		Tasks:      tasks,
		Agents:     agents,
		Events:     events,
		Status:     status,
	}, nil
}

// --- Plan cache queries (I-039) ---

// LookupPlanCacheExact returns a cached plan by exact goal hash, or nil if not found or expired.
func (d *DB) LookupPlanCacheExact(ctx context.Context, goalHash string) (*PlanCacheEntry, error) {
	var e PlanCacheEntry
	err := d.QueryRowContext(ctx,
		`SELECT id, goal_hash, goal_text, w5h2_key, keywords, plan_json, action_type, tier,
		        ttl_days, fail_count, hit_count, file_mtimes, created_at, expires_at
		 FROM plan_cache
		 WHERE goal_hash = ? AND expires_at > datetime('now')`, goalHash).
		Scan(&e.ID, &e.GoalHash, &e.GoalText, &e.W5H2Key, &e.Keywords, &e.PlanJSON,
			&e.ActionType, &e.Tier, &e.TTLDays, &e.FailCount, &e.HitCount,
			&e.FileMtimes, &e.CreatedAt, &e.ExpiresAt)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, fmt.Errorf("looking up plan cache by hash %s: %w", goalHash, err)
	}
	return &e, nil
}

// LookupPlanCacheW5H2 returns a cached plan by W5H2 key, or nil if not found or expired.
func (d *DB) LookupPlanCacheW5H2(ctx context.Context, w5h2Key string) (*PlanCacheEntry, error) {
	var e PlanCacheEntry
	err := d.QueryRowContext(ctx,
		`SELECT id, goal_hash, goal_text, w5h2_key, keywords, plan_json, action_type, tier,
		        ttl_days, fail_count, hit_count, file_mtimes, created_at, expires_at
		 FROM plan_cache
		 WHERE w5h2_key = ? AND expires_at > datetime('now')`, w5h2Key).
		Scan(&e.ID, &e.GoalHash, &e.GoalText, &e.W5H2Key, &e.Keywords, &e.PlanJSON,
			&e.ActionType, &e.Tier, &e.TTLDays, &e.FailCount, &e.HitCount,
			&e.FileMtimes, &e.CreatedAt, &e.ExpiresAt)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, fmt.Errorf("looking up plan cache by w5h2 key %s: %w", w5h2Key, err)
	}
	return &e, nil
}

// LookupPlanCacheKeyword returns all non-expired plan cache entries (up to 100) for client-side
// Jaccard similarity matching against keywords.
func (d *DB) LookupPlanCacheKeyword(ctx context.Context) ([]PlanCacheEntry, error) {
	rows, err := d.QueryContext(ctx,
		`SELECT id, goal_hash, goal_text, w5h2_key, keywords, plan_json, action_type, tier,
		        ttl_days, fail_count, hit_count, file_mtimes, created_at, expires_at
		 FROM plan_cache
		 WHERE expires_at > datetime('now')
		 LIMIT 100`)
	if err != nil {
		return nil, fmt.Errorf("looking up plan cache for keyword matching: %w", err)
	}
	defer rows.Close()

	var entries []PlanCacheEntry
	for rows.Next() {
		var e PlanCacheEntry
		if err := rows.Scan(&e.ID, &e.GoalHash, &e.GoalText, &e.W5H2Key, &e.Keywords, &e.PlanJSON,
			&e.ActionType, &e.Tier, &e.TTLDays, &e.FailCount, &e.HitCount,
			&e.FileMtimes, &e.CreatedAt, &e.ExpiresAt); err != nil {
			return nil, fmt.Errorf("scanning plan cache row: %w", err)
		}
		entries = append(entries, e)
	}
	return entries, rows.Err()
}

// --- Eval/healing queries ---

// ListEvalScenarios returns all eval scenarios.
func (d *DB) ListEvalScenarios(ctx context.Context) ([]EvalScenario, error) {
	rows, err := d.QueryContext(ctx,
		`SELECT id, role, category, repo_path, goal, expected_outcome, difficulty, created_at
		 FROM eval_scenarios ORDER BY created_at DESC`)
	if err != nil {
		return nil, fmt.Errorf("listing eval scenarios: %w", err)
	}
	defer rows.Close()

	var scenarios []EvalScenario
	for rows.Next() {
		var s EvalScenario
		if err := rows.Scan(&s.ID, &s.Role, &s.Category, &s.RepoPath, &s.Goal,
			&s.ExpectedOutcome, &s.Difficulty, &s.CreatedAt); err != nil {
			return nil, fmt.Errorf("scanning eval scenario row: %w", err)
		}
		scenarios = append(scenarios, s)
	}
	return scenarios, rows.Err()
}

// ListEvalScenariosForRole returns eval scenarios filtered by role.
func (d *DB) ListEvalScenariosForRole(ctx context.Context, role string) ([]EvalScenario, error) {
	rows, err := d.QueryContext(ctx,
		`SELECT id, role, category, repo_path, goal, expected_outcome, difficulty, created_at
		 FROM eval_scenarios WHERE role = ? ORDER BY created_at DESC`, role)
	if err != nil {
		return nil, fmt.Errorf("listing eval scenarios for role %s: %w", role, err)
	}
	defer rows.Close()

	var scenarios []EvalScenario
	for rows.Next() {
		var s EvalScenario
		if err := rows.Scan(&s.ID, &s.Role, &s.Category, &s.RepoPath, &s.Goal,
			&s.ExpectedOutcome, &s.Difficulty, &s.CreatedAt); err != nil {
			return nil, fmt.Errorf("scanning eval scenario row: %w", err)
		}
		scenarios = append(scenarios, s)
	}
	return scenarios, rows.Err()
}

// GetEvalVersion returns a single eval version by ID, or nil if not found.
func (d *DB) GetEvalVersion(ctx context.Context, id string) (*EvalVersion, error) {
	var v EvalVersion
	err := d.QueryRowContext(ctx,
		`SELECT id, parent_id, branch, commit_hash, created_at, status
		 FROM eval_versions WHERE id = ?`, id).
		Scan(&v.ID, &v.ParentID, &v.Branch, &v.CommitHash, &v.CreatedAt, &v.Status)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, fmt.Errorf("getting eval version %s: %w", id, err)
	}
	return &v, nil
}

// ListEvalRunsForVersion returns all eval runs for a given version.
func (d *DB) ListEvalRunsForVersion(ctx context.Context, versionID string) ([]EvalRun, error) {
	rows, err := d.QueryContext(ctx,
		`SELECT id, version_id, scenario_id, started_at, completed_at, status, raw_output
		 FROM eval_runs WHERE version_id = ? ORDER BY started_at DESC`, versionID)
	if err != nil {
		return nil, fmt.Errorf("listing eval runs for version %s: %w", versionID, err)
	}
	defer rows.Close()

	var runs []EvalRun
	for rows.Next() {
		var r EvalRun
		if err := rows.Scan(&r.ID, &r.VersionID, &r.ScenarioID, &r.StartedAt,
			&r.CompletedAt, &r.Status, &r.RawOutput); err != nil {
			return nil, fmt.Errorf("scanning eval run row: %w", err)
		}
		runs = append(runs, r)
	}
	return runs, rows.Err()
}

// ListEvalResultsForRun returns all eval results for a given run.
func (d *DB) ListEvalResultsForRun(ctx context.Context, runID string) ([]EvalResult, error) {
	rows, err := d.QueryContext(ctx,
		`SELECT id, run_id, metric, score, weight, details
		 FROM eval_results WHERE run_id = ? ORDER BY metric`, runID)
	if err != nil {
		return nil, fmt.Errorf("listing eval results for run %s: %w", runID, err)
	}
	defer rows.Close()

	var results []EvalResult
	for rows.Next() {
		var r EvalResult
		if err := rows.Scan(&r.ID, &r.RunID, &r.Metric, &r.Score, &r.Weight, &r.Details); err != nil {
			return nil, fmt.Errorf("scanning eval result row: %w", err)
		}
		results = append(results, r)
	}
	return results, rows.Err()
}

// ListHealingLogForSession returns all healing log entries for a given session.
func (d *DB) ListHealingLogForSession(ctx context.Context, sessionID string) ([]HealingLog, error) {
	rows, err := d.QueryContext(ctx,
		`SELECT id, session_id, task_id, error_type, fix_applied, success, rolled_back, created_at
		 FROM healing_log WHERE session_id = ? ORDER BY created_at DESC`, sessionID)
	if err != nil {
		return nil, fmt.Errorf("listing healing log for session %s: %w", sessionID, err)
	}
	defer rows.Close()

	var logs []HealingLog
	for rows.Next() {
		var h HealingLog
		if err := rows.Scan(&h.ID, &h.SessionID, &h.TaskID, &h.ErrorType, &h.FixApplied,
			&h.Success, &h.RolledBack, &h.CreatedAt); err != nil {
			return nil, fmt.Errorf("scanning healing log row: %w", err)
		}
		logs = append(logs, h)
	}
	return logs, rows.Err()
}

// GetHealingLogCount returns the number of healing log entries for a session.
// Used for budget enforcement (10-fix-per-session cap).
func (d *DB) GetHealingLogCount(ctx context.Context, sessionID string) (int, error) {
	var count int
	err := d.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM healing_log WHERE session_id = ?`, sessionID).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("counting healing log for session %s: %w", sessionID, err)
	}
	return count, nil
}
