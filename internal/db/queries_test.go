package db

import (
	"context"
	"fmt"
	"os"
	"testing"
)

func setupTestDB(t *testing.T) *DB {
	t.Helper()
	d, err := OpenMemory()
	if err != nil {
		t.Fatalf("opening memory db: %v", err)
	}
	if err := d.InitSchema(context.Background()); err != nil {
		t.Fatalf("initializing schema: %v", err)
	}
	t.Cleanup(func() { d.Close() })
	return d
}

func TestOpenMemory(t *testing.T) {
	d := setupTestDB(t)
	if d.Path() != ":memory:" {
		t.Errorf("expected path :memory:, got %s", d.Path())
	}
}

func TestInitSchemaCreatesAllTables(t *testing.T) {
	d := setupTestDB(t)
	ctx := context.Background()

	tables := []string{"agents", "tasks", "file_locks", "events", "blackboard", "ideas", "loops", "loop_steps"}
	for _, table := range tables {
		t.Run(table, func(t *testing.T) {
			var name string
			err := d.QueryRowContext(ctx,
				`SELECT name FROM sqlite_master WHERE type='table' AND name=?`, table).Scan(&name)
			if err != nil {
				t.Errorf("table %s not found: %v", table, err)
			}
		})
	}
}

func TestListTasksEmpty(t *testing.T) {
	d := setupTestDB(t)
	tasks, err := d.ListTasks(context.Background())
	if err != nil {
		t.Fatalf("listing tasks: %v", err)
	}
	if len(tasks) != 0 {
		t.Errorf("expected 0 tasks, got %d", len(tasks))
	}
}

func TestListTasksWithData(t *testing.T) {
	d := setupTestDB(t)
	ctx := context.Background()

	// Insert test tasks.
	_, err := d.ExecContext(ctx,
		`INSERT INTO tasks (id, title, status, role) VALUES
		 ('t1', 'Task One', 'running', 'implementer'),
		 ('t2', 'Task Two', 'pending', 'scout'),
		 ('t3', 'Task Three', 'done', 'researcher')`)
	if err != nil {
		t.Fatalf("inserting tasks: %v", err)
	}

	tasks, err := d.ListTasks(ctx)
	if err != nil {
		t.Fatalf("listing tasks: %v", err)
	}
	if len(tasks) != 3 {
		t.Fatalf("expected 3 tasks, got %d", len(tasks))
	}

	// Verify sort order: running → pending → done.
	if tasks[0].Status != "running" {
		t.Errorf("first task should be running, got %s", tasks[0].Status)
	}
	if tasks[1].Status != "pending" {
		t.Errorf("second task should be pending, got %s", tasks[1].Status)
	}
	if tasks[2].Status != "done" {
		t.Errorf("third task should be done, got %s", tasks[2].Status)
	}
}

func TestListAgentsEmpty(t *testing.T) {
	d := setupTestDB(t)
	agents, err := d.ListAgents(context.Background())
	if err != nil {
		t.Fatalf("listing agents: %v", err)
	}
	if len(agents) != 0 {
		t.Errorf("expected 0 agents, got %d", len(agents))
	}
}

func TestListAgentsWithData(t *testing.T) {
	d := setupTestDB(t)
	ctx := context.Background()

	_, err := d.ExecContext(ctx,
		`INSERT INTO agents (id, role, status) VALUES
		 ('a1', 'implementer', 'active'),
		 ('a2', 'scout', 'idle')`)
	if err != nil {
		t.Fatalf("inserting agents: %v", err)
	}

	agents, err := d.ListAgents(ctx)
	if err != nil {
		t.Fatalf("listing agents: %v", err)
	}
	if len(agents) != 2 {
		t.Errorf("expected 2 agents, got %d", len(agents))
	}
}

func TestTaskSummaryByStatus(t *testing.T) {
	d := setupTestDB(t)
	ctx := context.Background()

	_, err := d.ExecContext(ctx,
		`INSERT INTO tasks (id, title, status, role) VALUES
		 ('t1', 'A', 'running', 'implementer'),
		 ('t2', 'B', 'running', 'implementer'),
		 ('t3', 'C', 'pending', 'scout'),
		 ('t4', 'D', 'done', 'researcher'),
		 ('t5', 'E', 'failed', 'implementer')`)
	if err != nil {
		t.Fatalf("inserting tasks: %v", err)
	}

	counts, err := d.TaskSummaryByStatus(ctx)
	if err != nil {
		t.Fatalf("querying summary: %v", err)
	}

	if counts["running"] != 2 {
		t.Errorf("expected 2 running, got %d", counts["running"])
	}
	if counts["pending"] != 1 {
		t.Errorf("expected 1 pending, got %d", counts["pending"])
	}
	if counts["done"] != 1 {
		t.Errorf("expected 1 done, got %d", counts["done"])
	}
	if counts["failed"] != 1 {
		t.Errorf("expected 1 failed, got %d", counts["failed"])
	}
}

func TestListFileLocksEmpty(t *testing.T) {
	d := setupTestDB(t)
	locks, err := d.ListFileLocks(context.Background())
	if err != nil {
		t.Fatalf("listing locks: %v", err)
	}
	if len(locks) != 0 {
		t.Errorf("expected 0 locks, got %d", len(locks))
	}
}

func TestRecentEvents(t *testing.T) {
	d := setupTestDB(t)
	ctx := context.Background()

	_, err := d.ExecContext(ctx,
		`INSERT INTO events (event_type, payload) VALUES
		 ('task_created', '{"id":"t1"}'),
		 ('agent_spawned', '{"id":"a1"}'),
		 ('task_completed', '{"id":"t1"}')`)
	if err != nil {
		t.Fatalf("inserting events: %v", err)
	}

	events, err := d.RecentEvents(ctx, 2)
	if err != nil {
		t.Fatalf("listing events: %v", err)
	}
	if len(events) != 2 {
		t.Errorf("expected 2 events (limit), got %d", len(events))
	}
	// Most recent first.
	if events[0].EventType != "task_completed" {
		t.Errorf("first event should be task_completed, got %s", events[0].EventType)
	}
}

func TestGetBlackboardValue(t *testing.T) {
	d := setupTestDB(t)
	ctx := context.Background()

	// Missing key returns empty string.
	val, err := d.GetBlackboardValue(ctx, "missing")
	if err != nil {
		t.Fatalf("querying missing key: %v", err)
	}
	if val != "" {
		t.Errorf("expected empty string for missing key, got %q", val)
	}

	// Insert a value.
	_, err = d.ExecContext(ctx,
		`INSERT INTO blackboard (key, value) VALUES ('conductor:active', '1')`)
	if err != nil {
		t.Fatalf("inserting blackboard: %v", err)
	}

	val, err = d.GetBlackboardValue(ctx, "conductor:active")
	if err != nil {
		t.Fatalf("querying key: %v", err)
	}
	if val != "1" {
		t.Errorf("expected '1', got %q", val)
	}
}

func TestGetStatusSummary(t *testing.T) {
	d := setupTestDB(t)
	ctx := context.Background()

	_, err := d.ExecContext(ctx,
		`INSERT INTO tasks (id, title, status, role) VALUES
		 ('t1', 'A', 'running', 'implementer'),
		 ('t2', 'B', 'pending', 'scout')`)
	if err != nil {
		t.Fatalf("inserting tasks: %v", err)
	}

	_, err = d.ExecContext(ctx,
		`INSERT INTO agents (id, role, status) VALUES ('a1', 'implementer', 'active')`)
	if err != nil {
		t.Fatalf("inserting agent: %v", err)
	}

	summary, err := d.GetStatusSummary(ctx)
	if err != nil {
		t.Fatalf("getting summary: %v", err)
	}

	if summary.Tasks.Total != 2 {
		t.Errorf("expected 2 tasks total, got %d", summary.Tasks.Total)
	}
	if summary.Tasks.ByStatus["running"] != 1 {
		t.Errorf("expected 1 running task, got %d", summary.Tasks.ByStatus["running"])
	}
	if summary.Agents.Total != 1 {
		t.Errorf("expected 1 agent total, got %d", summary.Agents.Total)
	}
	if summary.LockCount != 0 {
		t.Errorf("expected 0 locks, got %d", summary.LockCount)
	}
}

func TestGetTaskByID(t *testing.T) {
	d := setupTestDB(t)
	ctx := context.Background()

	// Missing task returns nil.
	task, err := d.GetTaskByID(ctx, "nonexistent")
	if err != nil {
		t.Fatalf("getting missing task: %v", err)
	}
	if task != nil {
		t.Errorf("expected nil for missing task, got %v", task)
	}

	// Insert and retrieve.
	_, err = d.ExecContext(ctx,
		`INSERT INTO tasks (id, title, status, role) VALUES ('t1', 'Task One', 'pending', 'implementer')`)
	if err != nil {
		t.Fatalf("inserting task: %v", err)
	}

	task, err = d.GetTaskByID(ctx, "t1")
	if err != nil {
		t.Fatalf("getting task: %v", err)
	}
	if task == nil {
		t.Fatal("expected task, got nil")
	}
	if task.ID != "t1" {
		t.Errorf("expected ID 't1', got %s", task.ID)
	}
	if task.Title != "Task One" {
		t.Errorf("expected title 'Task One', got %s", task.Title)
	}
}

func TestListActiveAgentsWithPID(t *testing.T) {
	d := setupTestDB(t)
	ctx := context.Background()

	// Empty result.
	agents, err := d.ListActiveAgentsWithPID(ctx)
	if err != nil {
		t.Fatalf("listing: %v", err)
	}
	if len(agents) != 0 {
		t.Errorf("expected 0 agents, got %d", len(agents))
	}

	// Insert agents with various statuses.
	_, err = d.ExecContext(ctx,
		`INSERT INTO agents (id, role, status, pid) VALUES
		 ('a1', 'implementer', 'active', 1001),
		 ('a2', 'scout', 'working', 1002),
		 ('a3', 'reviewer', 'idle', 1003),
		 ('a4', 'researcher', 'dead', 1004),
		 ('a5', 'architect', 'active', NULL)`)
	if err != nil {
		t.Fatalf("inserting agents: %v", err)
	}

	agents, err = d.ListActiveAgentsWithPID(ctx)
	if err != nil {
		t.Fatalf("listing: %v", err)
	}
	// Should get a1 (active+pid), a2 (working+pid), a3 (idle+pid). Not a4 (dead) or a5 (null pid).
	if len(agents) != 3 {
		t.Errorf("expected 3 agents, got %d", len(agents))
	}
}

func TestListUnblockedPendingTasks(t *testing.T) {
	d := setupTestDB(t)
	ctx := context.Background()

	// Create tasks with dependencies.
	_, err := d.ExecContext(ctx,
		`INSERT INTO tasks (id, title, status, role, blocked_by) VALUES
		 ('t1', 'A', 'pending', 'implementer', NULL),
		 ('t2', 'B', 'pending', 'implementer', '["t1"]'),
		 ('t3', 'C', 'pending', 'scout', '[]'),
		 ('t4', 'D', 'done', 'researcher', NULL)`)
	if err != nil {
		t.Fatalf("inserting tasks: %v", err)
	}

	// t1 (no blockers) and t3 (empty blockers) should be unblocked.
	// t2 blocked by t1 (pending) should NOT appear.
	tasks, err := d.ListUnblockedPendingTasks(ctx, nil, 10)
	if err != nil {
		t.Fatalf("listing: %v", err)
	}
	if len(tasks) != 2 {
		t.Fatalf("expected 2 unblocked tasks, got %d", len(tasks))
	}

	// Complete t1 — now t2 should become unblocked.
	_, err = d.ExecContext(ctx, `UPDATE tasks SET status = 'done' WHERE id = 't1'`)
	if err != nil {
		t.Fatalf("completing t1: %v", err)
	}

	tasks, err = d.ListUnblockedPendingTasks(ctx, nil, 10)
	if err != nil {
		t.Fatalf("listing after completion: %v", err)
	}
	if len(tasks) != 2 {
		t.Fatalf("expected 2 unblocked tasks (t2 + t3), got %d", len(tasks))
	}
}

func TestListUnblockedPendingTasksFailedBlocker(t *testing.T) {
	d := setupTestDB(t)
	ctx := context.Background()

	// t2 depends on t1. If t1 fails, t2 should stay blocked.
	_, err := d.ExecContext(ctx,
		`INSERT INTO tasks (id, title, status, role, blocked_by) VALUES
		 ('t1', 'Researcher', 'failed', 'researcher', NULL),
		 ('t2', 'Implementer', 'pending', 'implementer', '["t1"]')`)
	if err != nil {
		t.Fatalf("inserting tasks: %v", err)
	}

	tasks, err := d.ListUnblockedPendingTasks(ctx, nil, 10)
	if err != nil {
		t.Fatalf("listing: %v", err)
	}
	if len(tasks) != 0 {
		t.Fatalf("expected 0 unblocked tasks (t1 failed, t2 should stay blocked), got %d", len(tasks))
	}

	// Now complete t1 — t2 should become unblocked.
	_, err = d.ExecContext(ctx, `UPDATE tasks SET status = 'done' WHERE id = 't1'`)
	if err != nil {
		t.Fatalf("completing t1: %v", err)
	}

	tasks, err = d.ListUnblockedPendingTasks(ctx, nil, 10)
	if err != nil {
		t.Fatalf("listing after done: %v", err)
	}
	if len(tasks) != 1 {
		t.Fatalf("expected 1 unblocked task (t2), got %d", len(tasks))
	}
}

func TestListUnblockedPendingTasksWithSession(t *testing.T) {
	d := setupTestDB(t)
	ctx := context.Background()

	_, err := d.ExecContext(ctx,
		`INSERT INTO tasks (id, title, status, role) VALUES
		 ('t1', 'A', 'pending', 'implementer'),
		 ('t2', 'B', 'pending', 'scout'),
		 ('t3', 'C', 'pending', 'reviewer')`)
	if err != nil {
		t.Fatalf("inserting tasks: %v", err)
	}

	// With session filter, only t1 and t3.
	tasks, err := d.ListUnblockedPendingTasks(ctx, []string{"t1", "t3"}, 10)
	if err != nil {
		t.Fatalf("listing: %v", err)
	}
	if len(tasks) != 2 {
		t.Errorf("expected 2 tasks, got %d", len(tasks))
	}
}

func TestCountTasksByStatuses(t *testing.T) {
	d := setupTestDB(t)
	ctx := context.Background()

	_, err := d.ExecContext(ctx,
		`INSERT INTO tasks (id, title, status, role) VALUES
		 ('t1', 'A', 'running', 'implementer'),
		 ('t2', 'B', 'running', 'scout'),
		 ('t3', 'C', 'pending', 'reviewer'),
		 ('t4', 'D', 'done', 'researcher'),
		 ('t5', 'E', 'failed', 'implementer')`)
	if err != nil {
		t.Fatalf("inserting tasks: %v", err)
	}

	count, err := d.CountTasksByStatuses(ctx, []string{"running", "pending"}, nil)
	if err != nil {
		t.Fatalf("counting: %v", err)
	}
	if count != 3 {
		t.Errorf("expected 3, got %d", count)
	}

	// With session filter.
	count, err = d.CountTasksByStatuses(ctx, []string{"running"}, []string{"t1"})
	if err != nil {
		t.Fatalf("counting with session: %v", err)
	}
	if count != 1 {
		t.Errorf("expected 1, got %d", count)
	}

	// Empty statuses.
	count, err = d.CountTasksByStatuses(ctx, nil, nil)
	if err != nil {
		t.Fatalf("counting empty: %v", err)
	}
	if count != 0 {
		t.Errorf("expected 0, got %d", count)
	}
}

func TestListBlackboardByPrefix(t *testing.T) {
	d := setupTestDB(t)
	ctx := context.Background()

	_, err := d.ExecContext(ctx,
		`INSERT INTO blackboard (key, value) VALUES
		 ('wait_until:t1', '1234567890'),
		 ('wait_until:t2', '1234567891'),
		 ('wait_reason:t1', 'rate_limit'),
		 ('conductor:active', '1')`)
	if err != nil {
		t.Fatalf("inserting: %v", err)
	}

	entries, err := d.ListBlackboardByPrefix(ctx, "wait_until:")
	if err != nil {
		t.Fatalf("listing: %v", err)
	}
	if len(entries) != 2 {
		t.Errorf("expected 2 entries, got %d", len(entries))
	}

	entries, err = d.ListBlackboardByPrefix(ctx, "conductor:")
	if err != nil {
		t.Fatalf("listing conductor: %v", err)
	}
	if len(entries) != 1 {
		t.Errorf("expected 1 entry, got %d", len(entries))
	}
}

func TestCleanupDeadAgents(t *testing.T) {
	d := setupTestDB(t)
	ctx := context.Background()

	// Insert agents with old heartbeats.
	_, err := d.ExecContext(ctx,
		`INSERT INTO agents (id, role, status, pid, heartbeat_at) VALUES
		 ('a1', 'implementer', 'active', 1001, datetime('now', '-10 minutes')),
		 ('a2', 'scout', 'active', 1002, datetime('now', '-1 minutes')),
		 ('a3', 'reviewer', 'dead', 1003, datetime('now', '-10 minutes'))`)
	if err != nil {
		t.Fatalf("inserting: %v", err)
	}

	cleaned, err := d.CleanupDeadAgents(ctx, 5)
	if err != nil {
		t.Fatalf("cleanup: %v", err)
	}
	// Only a1 should be cleaned (active + stale heartbeat). a2 is fresh, a3 is already dead.
	if cleaned != 1 {
		t.Errorf("expected 1 cleaned, got %d", cleaned)
	}

	// Verify a1 is now dead.
	agents, _ := d.ListAgents(ctx)
	for _, a := range agents {
		if a.ID == "a1" && a.Status != "dead" {
			t.Errorf("expected a1 to be dead, got %s", a.Status)
		}
		if a.ID == "a2" && a.Status != "active" {
			t.Errorf("expected a2 to still be active, got %s", a.Status)
		}
	}
}

func TestDeleteBlackboardByPrefix(t *testing.T) {
	d := setupTestDB(t)
	ctx := context.Background()

	_, err := d.ExecContext(ctx,
		`INSERT INTO blackboard (key, value) VALUES
		 ('wait_until:t1', '123'),
		 ('wait_until:t2', '456'),
		 ('conductor:active', '1')`)
	if err != nil {
		t.Fatalf("inserting: %v", err)
	}

	if err := d.DeleteBlackboardByPrefix(ctx, "wait_until:"); err != nil {
		t.Fatalf("deleting: %v", err)
	}

	// wait_until entries should be gone.
	entries, _ := d.ListBlackboardByPrefix(ctx, "wait_until:")
	if len(entries) != 0 {
		t.Errorf("expected 0 wait_until entries, got %d", len(entries))
	}

	// conductor entry should remain.
	val, _ := d.GetBlackboardValue(ctx, "conductor:active")
	if val != "1" {
		t.Errorf("expected conductor:active to remain, got %q", val)
	}
}

func TestWALCheckpoint(t *testing.T) {
	d := setupTestDB(t)
	ctx := context.Background()

	// Should not error on memory DB.
	if err := d.WALCheckpoint(ctx); err != nil {
		t.Fatalf("WAL checkpoint: %v", err)
	}
}

func TestInitSchemaReadOnlyFails(t *testing.T) {
	// We can't open a read-only in-memory DB easily, so test the flag directly.
	d := setupTestDB(t)
	d.readOnly = true
	err := d.InitSchema(context.Background())
	if err == nil {
		t.Error("InitSchema should fail on read-only connection")
	}
}

func TestIntegrityCheckHealthy(t *testing.T) {
	d := setupTestDB(t)
	if err := d.IntegrityCheck(context.Background()); err != nil {
		t.Errorf("expected healthy db, got: %v", err)
	}
}

func TestIntegrityCheckWithData(t *testing.T) {
	d := setupTestDB(t)
	ctx := context.Background()

	// Insert data then check integrity
	_, err := d.ExecContext(ctx,
		`INSERT INTO tasks (id, title, status, role) VALUES ('t1', 'Test', 'pending', 'implementer')`)
	if err != nil {
		t.Fatalf("inserting task: %v", err)
	}

	if err := d.IntegrityCheck(ctx); err != nil {
		t.Errorf("expected healthy db with data, got: %v", err)
	}
}

func TestListFileLocksForTask(t *testing.T) {
	d := setupTestDB(t)
	ctx := context.Background()

	// Set up required parent records.
	_, err := d.ExecContext(ctx,
		`INSERT INTO agents (id, role, status) VALUES ('a1', 'implementer', 'active'), ('a2', 'scout', 'active')`)
	if err != nil {
		t.Fatalf("inserting agents: %v", err)
	}
	_, err = d.ExecContext(ctx,
		`INSERT INTO tasks (id, title, status, role) VALUES ('t1', 'Task One', 'running', 'implementer'), ('t2', 'Task Two', 'running', 'scout')`)
	if err != nil {
		t.Fatalf("inserting tasks: %v", err)
	}

	// Insert locks for different tasks.
	_, err = d.ExecContext(ctx,
		`INSERT INTO file_locks (file_path, locked_by, task_id) VALUES
		 ('src/main.go', 'a1', 't1'),
		 ('src/util.go', 'a1', 't1'),
		 ('src/other.go', 'a2', 't2')`)
	if err != nil {
		t.Fatalf("inserting locks: %v", err)
	}

	locks, err := d.ListFileLocksForTask(ctx, "t1")
	if err != nil {
		t.Fatalf("listing locks for t1: %v", err)
	}
	if len(locks) != 2 {
		t.Errorf("expected 2 locks for t1, got %d", len(locks))
	}
	for _, l := range locks {
		if !l.TaskID.Valid || l.TaskID.String != "t1" {
			t.Errorf("expected task_id 't1', got %v", l.TaskID)
		}
	}
}

func TestListFileLocksForTaskEmpty(t *testing.T) {
	d := setupTestDB(t)
	ctx := context.Background()

	locks, err := d.ListFileLocksForTask(ctx, "nonexistent")
	if err != nil {
		t.Fatalf("listing locks: %v", err)
	}
	if len(locks) != 0 {
		t.Errorf("expected 0 locks, got %d", len(locks))
	}
}

func TestFindCrossConductorFileLock(t *testing.T) {
	d := setupTestDB(t)
	ctx := context.Background()

	// Create two conductors.
	_, err := d.ExecContext(ctx,
		`INSERT INTO conductors (id, pid, goal, status, staging_branch, base_branch, max_parallel, test_cmd, merge_review, model_strategy, runtime, repo_map, lenient_deps, file_enforcement)
		 VALUES ('c1', 100, 'goal1', 'active', 'staging-c1', 'dev', 3, '', 0, 'all-opus', 'local', 0, 0, ''),
		        ('c2', 200, 'goal2', 'active', 'staging-c2', 'dev', 3, '', 0, 'all-opus', 'local', 0, 0, '')`)
	if err != nil {
		t.Fatalf("inserting conductors: %v", err)
	}

	// Create tasks belonging to different conductors.
	_, err = d.ExecContext(ctx,
		`INSERT INTO tasks (id, title, status, role, conductor_id) VALUES
		 ('t1', 'Task 1', 'running', 'implementer', 'c1'),
		 ('t2', 'Task 2', 'running', 'implementer', 'c2'),
		 ('t3', 'Task 3', 'running', 'implementer', 'c1')`)
	if err != nil {
		t.Fatalf("inserting tasks: %v", err)
	}

	// File locked by t1 (conductor c1). Unique constraint means only one lock per file.
	_, err = d.ExecContext(ctx,
		`INSERT INTO file_locks (file_path, locked_by, task_id) VALUES
		 ('research/README.md', NULL, 't1'),
		 ('src/main.go', NULL, 't3')`)
	if err != nil {
		t.Fatalf("inserting file locks: %v", err)
	}

	// Cross-conductor overlap: from t2's perspective (conductor c2), research/README.md
	// is locked by t1 (conductor c1). This is the check done before t2 tries to lock it.
	taskID, conductorID, err := d.FindCrossConductorFileLock(ctx, "research/README.md", "t2", "c2")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if taskID != "t1" {
		t.Errorf("expected taskID 't1', got %q", taskID)
	}
	if conductorID != "c1" {
		t.Errorf("expected conductorID 'c1', got %q", conductorID)
	}

	// Same-conductor: from t1's perspective (conductor c1), src/main.go is locked
	// by t3 (also c1). Should return empty — same conductor, no cross-conductor overlap.
	taskID, conductorID, err = d.FindCrossConductorFileLock(ctx, "src/main.go", "t1", "c1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if taskID != "" || conductorID != "" {
		t.Errorf("expected no cross-conductor overlap for same conductor, got task=%q conductor=%q", taskID, conductorID)
	}

	// No overlap: file not locked by anyone.
	taskID, conductorID, err = d.FindCrossConductorFileLock(ctx, "nonexistent.go", "t1", "c1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if taskID != "" || conductorID != "" {
		t.Errorf("expected empty for unlocked file, got task=%q conductor=%q", taskID, conductorID)
	}
}

func TestReinitialize(t *testing.T) {
	d := setupTestDB(t)
	ctx := context.Background()

	// Insert data, then reinitialize — tables should exist but data gone.
	_, err := d.ExecContext(ctx,
		`INSERT INTO tasks (id, title, status, role) VALUES ('t1', 'Test', 'pending', 'implementer')`)
	if err != nil {
		t.Fatalf("inserting: %v", err)
	}

	if err := d.Reinitialize(ctx); err != nil {
		t.Fatalf("reinitialize: %v", err)
	}

	// Tables should exist.
	var name string
	err = d.QueryRowContext(ctx,
		`SELECT name FROM sqlite_master WHERE type='table' AND name='tasks'`).Scan(&name)
	if err != nil {
		t.Fatalf("tasks table missing after reinitialize: %v", err)
	}

	// Data should be gone.
	var count int
	if err := d.QueryRowContext(ctx, `SELECT COUNT(*) FROM tasks`).Scan(&count); err != nil {
		t.Fatalf("counting tasks: %v", err)
	}
	if count != 0 {
		t.Errorf("expected 0 tasks after reinitialize, got %d", count)
	}
}

func TestReinitializePreservesPath(t *testing.T) {
	d := setupTestDB(t)
	pathBefore := d.Path()

	if err := d.Reinitialize(context.Background()); err != nil {
		t.Fatalf("reinitialize: %v", err)
	}

	if d.Path() != pathBefore {
		t.Errorf("path changed: %q → %q", pathBefore, d.Path())
	}
}

func TestReinitializeMemoryDB(t *testing.T) {
	d := setupTestDB(t)
	ctx := context.Background()

	if d.Path() != ":memory:" {
		t.Skip("test requires in-memory DB")
	}

	if err := d.Reinitialize(ctx); err != nil {
		t.Fatalf("reinitialize: %v", err)
	}

	// Should still work.
	var name string
	err := d.QueryRowContext(ctx,
		`SELECT name FROM sqlite_master WHERE type='table' AND name='tasks'`).Scan(&name)
	if err != nil {
		t.Fatalf("tasks table missing after memory reinitialize: %v", err)
	}
}

func TestCheckAndReinitializeHealthy(t *testing.T) {
	d := setupTestDB(t)
	ctx := context.Background()

	// Insert data — should survive (no reinit on healthy DB).
	_, err := d.ExecContext(ctx,
		`INSERT INTO tasks (id, title, status, role) VALUES ('t1', 'Test', 'pending', 'implementer')`)
	if err != nil {
		t.Fatalf("inserting: %v", err)
	}

	if err := d.CheckAndReinitialize(ctx); err != nil {
		t.Fatalf("check and reinitialize: %v", err)
	}

	var count int
	if err := d.QueryRowContext(ctx, `SELECT COUNT(*) FROM tasks`).Scan(&count); err != nil {
		t.Fatalf("counting: %v", err)
	}
	if count != 1 {
		t.Errorf("expected 1 task (no reinit on healthy), got %d", count)
	}
}

func TestCheckAndReinitializeCorrupt(t *testing.T) {
	// Use a file-backed DB so we can corrupt it.
	dir := t.TempDir()
	dbPath := dir + "/test.db"

	d, err := Open(dbPath)
	if err != nil {
		t.Fatalf("opening: %v", err)
	}
	if err := d.InitSchema(context.Background()); err != nil {
		t.Fatalf("init schema: %v", err)
	}

	// Close and corrupt the file.
	d.Close()
	f, err := os.OpenFile(dbPath, os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatalf("opening file for corruption: %v", err)
	}
	f.WriteAt([]byte("CORRUPT"), 0)
	f.Close()

	// Reopen — Ping should still work (driver may not detect corruption until query).
	d2, err := Open(dbPath)
	if err != nil {
		// If Open itself fails due to corruption, reinitialize from scratch.
		d2 = &DB{path: dbPath}
		if err := d2.Reinitialize(context.Background()); err != nil {
			t.Fatalf("reinitialize after open failure: %v", err)
		}
	} else {
		if err := d2.CheckAndReinitialize(context.Background()); err != nil {
			t.Fatalf("check and reinitialize: %v", err)
		}
	}
	defer d2.Close()

	// DB should now be healthy with all tables.
	ctx := context.Background()
	var name string
	err = d2.QueryRowContext(ctx,
		`SELECT name FROM sqlite_master WHERE type='table' AND name='tasks'`).Scan(&name)
	if err != nil {
		t.Fatalf("tasks table missing after recovery: %v", err)
	}
}

// --- Step 3a: Cascade Fail Query Tests ---

func TestGetPendingTasksWithAllBlockersTerminal(t *testing.T) {
	d := setupTestDB(t)
	ctx := context.Background()

	// t1 failed, t2 blocked_by t1 → t2 should be returned
	_, err := d.ExecContext(ctx,
		`INSERT INTO tasks (id, title, status, role, blocked_by) VALUES
		 ('t1', 'Task 1', 'failed', 'researcher', NULL),
		 ('t2', 'Task 2', 'pending', 'implementer', '["t1"]')`)
	if err != nil {
		t.Fatalf("inserting tasks: %v", err)
	}

	tasks, err := d.GetPendingTasksWithAllBlockersTerminal(ctx, nil)
	if err != nil {
		t.Fatalf("querying: %v", err)
	}
	if len(tasks) != 1 {
		t.Fatalf("expected 1 cascade-fail candidate, got %d", len(tasks))
	}
	if tasks[0].ID != "t2" {
		t.Errorf("expected task t2, got %s", tasks[0].ID)
	}
}

func TestGetPendingTasksWithMixedBlockers(t *testing.T) {
	d := setupTestDB(t)
	ctx := context.Background()

	// t1 failed, t2 running, t3 blocked by both → t3 NOT returned (t2 still running)
	_, err := d.ExecContext(ctx,
		`INSERT INTO tasks (id, title, status, role, blocked_by) VALUES
		 ('t1', 'Task 1', 'failed', 'researcher', NULL),
		 ('t2', 'Task 2', 'running', 'researcher', NULL),
		 ('t3', 'Task 3', 'pending', 'implementer', '["t1","t2"]')`)
	if err != nil {
		t.Fatalf("inserting tasks: %v", err)
	}

	tasks, err := d.GetPendingTasksWithAllBlockersTerminal(ctx, nil)
	if err != nil {
		t.Fatalf("querying: %v", err)
	}
	if len(tasks) != 0 {
		t.Fatalf("expected 0 candidates (t2 still running), got %d", len(tasks))
	}
}

func TestGetPendingTasksWithAllBlockersDoneNoCascade(t *testing.T) {
	d := setupTestDB(t)
	ctx := context.Background()

	// t1 done, t2 blocked_by t1 → t2 NOT returned (no failure in blockers)
	_, err := d.ExecContext(ctx,
		`INSERT INTO tasks (id, title, status, role, blocked_by) VALUES
		 ('t1', 'Task 1', 'done', 'researcher', NULL),
		 ('t2', 'Task 2', 'pending', 'implementer', '["t1"]')`)
	if err != nil {
		t.Fatalf("inserting tasks: %v", err)
	}

	tasks, err := d.GetPendingTasksWithAllBlockersTerminal(ctx, nil)
	if err != nil {
		t.Fatalf("querying: %v", err)
	}
	if len(tasks) != 0 {
		t.Fatalf("expected 0 candidates (all blockers done, no failure), got %d", len(tasks))
	}
}

func TestGetPendingTasksWithSessionFilter(t *testing.T) {
	d := setupTestDB(t)
	ctx := context.Background()

	// t1 failed, t2 and t3 both blocked by t1; only t2 in session
	_, err := d.ExecContext(ctx,
		`INSERT INTO tasks (id, title, status, role, blocked_by) VALUES
		 ('t1', 'Task 1', 'failed', 'researcher', NULL),
		 ('t2', 'Task 2', 'pending', 'implementer', '["t1"]'),
		 ('t3', 'Task 3', 'pending', 'implementer', '["t1"]')`)
	if err != nil {
		t.Fatalf("inserting tasks: %v", err)
	}

	tasks, err := d.GetPendingTasksWithAllBlockersTerminal(ctx, []string{"t1", "t2"})
	if err != nil {
		t.Fatalf("querying: %v", err)
	}
	if len(tasks) != 1 {
		t.Fatalf("expected 1 candidate (session filtered), got %d", len(tasks))
	}
	if tasks[0].ID != "t2" {
		t.Errorf("expected t2, got %s", tasks[0].ID)
	}
}

// --- B-174: ListLenientPendingTasks Tests ---

func TestListLenientPendingTasks_Basic(t *testing.T) {
	d := setupTestDB(t)
	ctx := context.Background()

	// t1 failed, t2 done, t3 pending blocked_by=[t1,t2] with lenient annotation
	// t4 pending blocked_by=[t1] without lenient annotation
	_, err := d.ExecContext(ctx,
		`INSERT INTO tasks (id, title, status, role, blocked_by) VALUES
		 ('t1', 'Task 1', 'failed', 'researcher', NULL),
		 ('t2', 'Task 2', 'done', 'researcher', NULL),
		 ('t3', 'Task 3', 'pending', 'implementer', '["t1","t2"]'),
		 ('t4', 'Task 4', 'pending', 'implementer', '["t1"]')`)
	if err != nil {
		t.Fatalf("inserting tasks: %v", err)
	}

	// Annotate t3 as lenient-unblocked, but NOT t4
	d.SetBlackboard(ctx, "lenient_deps:t3", "1", "test")

	tasks, err := d.ListLenientPendingTasks(ctx, nil, 10)
	if err != nil {
		t.Fatalf("listing: %v", err)
	}
	if len(tasks) != 1 {
		t.Fatalf("expected 1 lenient pending task, got %d", len(tasks))
	}
	if tasks[0].ID != "t3" {
		t.Errorf("expected task t3, got %s", tasks[0].ID)
	}
}

func TestListLenientPendingTasks_SessionFilter(t *testing.T) {
	d := setupTestDB(t)
	ctx := context.Background()

	_, err := d.ExecContext(ctx,
		`INSERT INTO tasks (id, title, status, role, blocked_by) VALUES
		 ('t1', 'Task 1', 'failed', 'researcher', NULL),
		 ('t2', 'Task 2', 'done', 'researcher', NULL),
		 ('t3', 'Task 3', 'pending', 'implementer', '["t1","t2"]'),
		 ('t4', 'Task 4', 'pending', 'implementer', '["t1","t2"]')`)
	if err != nil {
		t.Fatalf("inserting tasks: %v", err)
	}

	// Both annotated as lenient
	d.SetBlackboard(ctx, "lenient_deps:t3", "1", "test")
	d.SetBlackboard(ctx, "lenient_deps:t4", "1", "test")

	// Session filter includes only t1, t2, t3 — not t4
	tasks, err := d.ListLenientPendingTasks(ctx, []string{"t1", "t2", "t3"}, 10)
	if err != nil {
		t.Fatalf("listing: %v", err)
	}
	if len(tasks) != 1 {
		t.Fatalf("expected 1 lenient pending task (session filtered), got %d", len(tasks))
	}
	if tasks[0].ID != "t3" {
		t.Errorf("expected task t3, got %s", tasks[0].ID)
	}
}

func TestListLenientPendingTasks_NoAnnotation(t *testing.T) {
	d := setupTestDB(t)
	ctx := context.Background()

	// Mixed-outcome task WITHOUT lenient_deps annotation — should NOT be returned
	_, err := d.ExecContext(ctx,
		`INSERT INTO tasks (id, title, status, role, blocked_by) VALUES
		 ('t1', 'Task 1', 'done', 'researcher', NULL),
		 ('t2', 'Task 2', 'failed', 'researcher', NULL),
		 ('t3', 'Task 3', 'pending', 'implementer', '["t1","t2"]')`)
	if err != nil {
		t.Fatalf("inserting tasks: %v", err)
	}

	// No lenient_deps annotation for t3
	tasks, err := d.ListLenientPendingTasks(ctx, nil, 10)
	if err != nil {
		t.Fatalf("listing: %v", err)
	}
	if len(tasks) != 0 {
		t.Fatalf("expected 0 lenient pending tasks (no annotation), got %d", len(tasks))
	}
}

// --- EventsSinceForSession Tests ---

func TestEventsSinceForSession_Basic(t *testing.T) {
	d := setupTestDB(t)
	ctx := context.Background()

	// Insert events: some with task_id, some without (conductor-level)
	_, err := d.ExecContext(ctx,
		`INSERT INTO events (event_type, task_id, payload) VALUES
		 ('goal_started', NULL, '{"goal":"test"}'),
		 ('task_created', 't1', '{"id":"t1"}'),
		 ('agent_spawned', NULL, '{"id":"a1"}'),
		 ('task_completed', 't1', '{"id":"t1"}'),
		 ('task_created', 't2', '{"id":"t2"}')`)
	if err != nil {
		t.Fatalf("inserting events: %v", err)
	}

	// Get all events since ID 0 for session with t1
	events, err := d.EventsSinceForSession(ctx, 0, []string{"t1"})
	if err != nil {
		t.Fatalf("querying: %v", err)
	}
	// Should get: goal_started (null task), task_created t1, agent_spawned (null), task_completed t1
	// Should NOT get: task_created t2 (not in session)
	if len(events) != 4 {
		t.Fatalf("expected 4 events, got %d", len(events))
	}
	// Verify ASC ordering
	if events[0].EventType != "goal_started" {
		t.Errorf("first event should be goal_started, got %s", events[0].EventType)
	}
}

func TestEventsSinceForSession_SinceID(t *testing.T) {
	d := setupTestDB(t)
	ctx := context.Background()

	_, err := d.ExecContext(ctx,
		`INSERT INTO events (event_type, payload) VALUES
		 ('event_1', NULL),
		 ('event_2', NULL),
		 ('event_3', NULL)`)
	if err != nil {
		t.Fatalf("inserting events: %v", err)
	}

	// Get events since ID 1 (should skip first)
	events, err := d.EventsSinceForSession(ctx, 1, nil)
	if err != nil {
		t.Fatalf("querying: %v", err)
	}
	if len(events) != 2 {
		t.Fatalf("expected 2 events since ID 1, got %d", len(events))
	}
	if events[0].EventType != "event_2" {
		t.Errorf("first event should be event_2, got %s", events[0].EventType)
	}
}

func TestEventsSinceForSession_EmptySession(t *testing.T) {
	d := setupTestDB(t)
	ctx := context.Background()

	// Insert events with task_id set
	_, err := d.ExecContext(ctx,
		`INSERT INTO events (event_type, task_id, payload) VALUES
		 ('task_created', 't1', NULL),
		 ('conductor_log', NULL, '{"msg":"test"}')`)
	if err != nil {
		t.Fatalf("inserting events: %v", err)
	}

	// With empty session IDs, should only get conductor-level events (task_id IS NULL)
	events, err := d.EventsSinceForSession(ctx, 0, nil)
	if err != nil {
		t.Fatalf("querying: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("expected 1 conductor event, got %d", len(events))
	}
	if events[0].EventType != "conductor_log" {
		t.Errorf("expected conductor_log, got %s", events[0].EventType)
	}
}

func TestEventsSinceForSession_NoResults(t *testing.T) {
	d := setupTestDB(t)
	ctx := context.Background()

	events, err := d.EventsSinceForSession(ctx, 0, nil)
	if err != nil {
		t.Fatalf("querying: %v", err)
	}
	if len(events) != 0 {
		t.Errorf("expected 0 events, got %d", len(events))
	}
}

func TestDSNContainsDurabilityPragmas(t *testing.T) {
	// Verify that Open() sets fullfsync and synchronous via DSN.
	// We can check by querying the pragmas on an in-memory DB.
	d := setupTestDB(t)
	ctx := context.Background()

	var syncMode string
	if err := d.QueryRowContext(ctx, `PRAGMA synchronous`).Scan(&syncMode); err != nil {
		t.Fatalf("querying synchronous: %v", err)
	}
	// FULL = 2
	if syncMode != "2" {
		t.Errorf("expected synchronous=2 (FULL), got %s", syncMode)
	}
}

// TestListTaskIDsByPhase verifies that ListTaskIDsByPhase returns only tasks
// matching both the conductor_id and phase_id (G110).
func TestListTaskIDsByPhase(t *testing.T) {
	d := setupTestDB(t)
	ctx := context.Background()

	// Create conductor
	_, err := d.ExecContext(ctx,
		`INSERT INTO conductors (id, pid, goal, status, staging_branch, base_branch, max_parallel, model_strategy, runtime, started_at, heartbeat_at)
		 VALUES ('cond-1', 1, 'test goal', 'active', 'conductor/cond-1', 'dev', 4, 'all-opus', 'local', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`)
	if err != nil {
		t.Fatalf("creating conductor: %v", err)
	}

	// Create tasks with different phase IDs
	tasks := []struct {
		id    string
		phase string
	}{
		{"t1", "phase-scaffold"},
		{"t2", "phase-scaffold"},
		{"t3", "phase-implement"},
		{"t4", "phase-implement"},
		{"t5", ""}, // no phase (single-phase mode)
	}
	for _, task := range tasks {
		phaseVal := "NULL"
		if task.phase != "" {
			phaseVal = "'" + task.phase + "'"
		}
		_, err := d.ExecContext(ctx,
			fmt.Sprintf(`INSERT INTO tasks (id, title, status, role, conductor_id, phase_id) VALUES ('%s', 'Task', 'done', 'implementer', 'cond-1', %s)`,
				task.id, phaseVal))
		if err != nil {
			t.Fatalf("inserting task %s: %v", task.id, err)
		}
	}

	// Query phase-scaffold — should return t1, t2
	ids, err := d.ListTaskIDsByPhase(ctx, "cond-1", "phase-scaffold")
	if err != nil {
		t.Fatalf("ListTaskIDsByPhase scaffold: %v", err)
	}
	if len(ids) != 2 {
		t.Errorf("expected 2 scaffold tasks, got %d", len(ids))
	}

	// Query phase-implement — should return t3, t4
	ids, err = d.ListTaskIDsByPhase(ctx, "cond-1", "phase-implement")
	if err != nil {
		t.Fatalf("ListTaskIDsByPhase implement: %v", err)
	}
	if len(ids) != 2 {
		t.Errorf("expected 2 implement tasks, got %d", len(ids))
	}

	// Query nonexistent phase — should return empty
	ids, err = d.ListTaskIDsByPhase(ctx, "cond-1", "phase-nonexistent")
	if err != nil {
		t.Fatalf("ListTaskIDsByPhase nonexistent: %v", err)
	}
	if len(ids) != 0 {
		t.Errorf("expected 0 tasks for nonexistent phase, got %d", len(ids))
	}
}

func TestListEvalScenariosForRole(t *testing.T) {
	d := setupTestDB(t)
	ctx := context.Background()

	// Insert scenarios for multiple roles.
	scenarios := []EvalScenario{
		{ID: "es-impl-1", Role: "implementer", Goal: "implement feature A"},
		{ID: "es-impl-2", Role: "implementer", Goal: "implement feature B"},
		{ID: "es-rev-1", Role: "reviewer", Goal: "review PR"},
		{ID: "es-arch-1", Role: "architect", Goal: "design system"},
	}
	for _, s := range scenarios {
		if err := d.InsertEvalScenario(ctx, s); err != nil {
			t.Fatalf("inserting scenario %s: %v", s.ID, err)
		}
	}

	// Query by role "implementer" — should return 2.
	impl, err := d.ListEvalScenariosForRole(ctx, "implementer")
	if err != nil {
		t.Fatalf("listing implementer scenarios: %v", err)
	}
	if len(impl) != 2 {
		t.Errorf("expected 2 implementer scenarios, got %d", len(impl))
	}
	for _, s := range impl {
		if s.Role != "implementer" {
			t.Errorf("expected role 'implementer', got %q", s.Role)
		}
	}

	// Query by role "reviewer" — should return 1.
	rev, err := d.ListEvalScenariosForRole(ctx, "reviewer")
	if err != nil {
		t.Fatalf("listing reviewer scenarios: %v", err)
	}
	if len(rev) != 1 {
		t.Errorf("expected 1 reviewer scenario, got %d", len(rev))
	}

	// Query by nonexistent role — should return 0.
	none, err := d.ListEvalScenariosForRole(ctx, "nonexistent")
	if err != nil {
		t.Fatalf("listing nonexistent role scenarios: %v", err)
	}
	if len(none) != 0 {
		t.Errorf("expected 0 scenarios for nonexistent role, got %d", len(none))
	}

	// ListEvalScenarios (all) should return all 4.
	all, err := d.ListEvalScenarios(ctx)
	if err != nil {
		t.Fatalf("listing all scenarios: %v", err)
	}
	if len(all) != 4 {
		t.Errorf("expected 4 total scenarios, got %d", len(all))
	}
}

func TestGetHealingLogCount(t *testing.T) {
	d := setupTestDB(t)
	ctx := context.Background()

	// Count for empty session should be 0.
	count, err := d.GetHealingLogCount(ctx, "sess-count")
	if err != nil {
		t.Fatalf("getting healing log count: %v", err)
	}
	if count != 0 {
		t.Errorf("expected 0, got %d", count)
	}

	// Insert 3 entries for sess-count.
	for i := 1; i <= 3; i++ {
		h := HealingLog{
			ID:        fmt.Sprintf("hl-cnt-%d", i),
			SessionID: "sess-count",
			Success:   1,
		}
		if err := d.InsertHealingLog(ctx, h); err != nil {
			t.Fatalf("inserting healing log %d: %v", i, err)
		}
	}

	// Insert 1 entry for a different session.
	if err := d.InsertHealingLog(ctx, HealingLog{ID: "hl-other", SessionID: "sess-other", Success: 0}); err != nil {
		t.Fatalf("inserting other healing log: %v", err)
	}

	// Count for sess-count should be 3.
	count, err = d.GetHealingLogCount(ctx, "sess-count")
	if err != nil {
		t.Fatalf("getting healing log count: %v", err)
	}
	if count != 3 {
		t.Errorf("expected 3, got %d", count)
	}

	// Count for sess-other should be 1.
	count, err = d.GetHealingLogCount(ctx, "sess-other")
	if err != nil {
		t.Fatalf("getting healing log count: %v", err)
	}
	if count != 1 {
		t.Errorf("expected 1, got %d", count)
	}
}

func TestGetEvalVersionNotFound(t *testing.T) {
	d := setupTestDB(t)
	ctx := context.Background()

	got, err := d.GetEvalVersion(ctx, "nonexistent")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != nil {
		t.Errorf("expected nil for nonexistent version, got %+v", got)
	}
}
