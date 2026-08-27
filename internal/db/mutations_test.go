package db

import (
	"context"
	"database/sql"
	"strings"
	"testing"
	"time"
)

func TestGenID(t *testing.T) {
	id1 := GenID("agent")
	id2 := GenID("agent")

	// Verify format: PREFIX-YYYYMMDD-HHMMSS-XXXX
	parts := strings.Split(id1, "-")
	if len(parts) != 4 {
		t.Errorf("expected 4 parts separated by '-', got %d: %s", len(parts), id1)
	}
	if parts[0] != "agent" {
		t.Errorf("expected prefix 'agent', got %s", parts[0])
	}
	if len(parts[1]) != 8 {
		t.Errorf("expected date part of length 8, got %d: %s", len(parts[1]), parts[1])
	}
	if len(parts[2]) != 6 {
		t.Errorf("expected time part of length 6, got %d: %s", len(parts[2]), parts[2])
	}
	if len(parts[3]) != 4 {
		t.Errorf("expected random suffix of length 4, got %d: %s", len(parts[3]), parts[3])
	}

	// Verify uniqueness (very likely with random suffix)
	if id1 == id2 {
		t.Errorf("expected unique IDs, got identical: %s", id1)
	}
}

func TestWithTx(t *testing.T) {
	d := setupTestDB(t)
	ctx := context.Background()

	t.Run("commit on success", func(t *testing.T) {
		err := d.WithTx(ctx, func(tx *sql.Tx) error {
			_, err := tx.ExecContext(ctx,
				`INSERT INTO agents (id, role, status) VALUES ('a1', 'implementer', 'idle')`)
			return err
		})
		if err != nil {
			t.Fatalf("transaction failed: %v", err)
		}

		// Verify data committed.
		var count int
		if err := d.QueryRowContext(ctx, `SELECT COUNT(*) FROM agents WHERE id = 'a1'`).Scan(&count); err != nil {
			t.Fatalf("querying agents: %v", err)
		}
		if count != 1 {
			t.Errorf("expected 1 agent, got %d", count)
		}
	})

	t.Run("rollback on error", func(t *testing.T) {
		err := d.WithTx(ctx, func(tx *sql.Tx) error {
			_, err := tx.ExecContext(ctx,
				`INSERT INTO agents (id, role, status) VALUES ('a2', 'scout', 'idle')`)
			if err != nil {
				return err
			}
			return sql.ErrTxDone // Force rollback.
		})
		if err == nil {
			t.Error("expected error, got nil")
		}

		// Verify data not committed.
		var count int
		if err := d.QueryRowContext(ctx, `SELECT COUNT(*) FROM agents WHERE id = 'a2'`).Scan(&count); err != nil {
			t.Fatalf("querying agents: %v", err)
		}
		if count != 0 {
			t.Errorf("expected 0 agents (rolled back), got %d", count)
		}
	})
}

func TestWithTxReadOnly(t *testing.T) {
	d := setupTestDB(t)
	d.readOnly = true
	ctx := context.Background()

	err := d.WithTx(ctx, func(tx *sql.Tx) error {
		return nil
	})
	if err == nil {
		t.Error("expected error on read-only connection, got nil")
	}
	if !strings.Contains(err.Error(), "read-only") {
		t.Errorf("expected 'read-only' in error, got: %v", err)
	}
}

func TestRegisterAgent(t *testing.T) {
	d := setupTestDB(t)
	ctx := context.Background()

	a := Agent{
		ID:        "agent-20260212-120000-abcd",
		Role:      "implementer",
		Status:    "idle",
		CreatedAt: time.Now(),
	}

	if err := d.RegisterAgent(ctx, a); err != nil {
		t.Fatalf("registering agent: %v", err)
	}

	// Verify with ListAgents.
	agents, err := d.ListAgents(ctx)
	if err != nil {
		t.Fatalf("listing agents: %v", err)
	}
	if len(agents) != 1 {
		t.Fatalf("expected 1 agent, got %d", len(agents))
	}
	if agents[0].ID != a.ID {
		t.Errorf("expected ID %s, got %s", a.ID, agents[0].ID)
	}
	if agents[0].Role != a.Role {
		t.Errorf("expected role %s, got %s", a.Role, agents[0].Role)
	}
}

func TestUpdateAgentPID(t *testing.T) {
	d := setupTestDB(t)
	ctx := context.Background()

	a := Agent{
		ID:        "agent-test",
		Role:      "implementer",
		Status:    "idle",
		CreatedAt: time.Now(),
	}
	if err := d.RegisterAgent(ctx, a); err != nil {
		t.Fatalf("registering agent: %v", err)
	}

	if err := d.UpdateAgentPID(ctx, a.ID, 12345); err != nil {
		t.Fatalf("updating PID: %v", err)
	}

	// Verify.
	agents, err := d.ListAgents(ctx)
	if err != nil {
		t.Fatalf("listing agents: %v", err)
	}
	if !agents[0].PID.Valid {
		t.Fatal("expected PID to be set")
	}
	if agents[0].PID.Int64 != 12345 {
		t.Errorf("expected PID 12345, got %d", agents[0].PID.Int64)
	}
}

func TestUpdateAgentStatus(t *testing.T) {
	d := setupTestDB(t)
	ctx := context.Background()

	a := Agent{
		ID:        "agent-test",
		Role:      "implementer",
		Status:    "idle",
		CreatedAt: time.Now(),
	}
	if err := d.RegisterAgent(ctx, a); err != nil {
		t.Fatalf("registering agent: %v", err)
	}

	if err := d.UpdateAgentStatus(ctx, a.ID, "active"); err != nil {
		t.Fatalf("updating status: %v", err)
	}

	// Verify.
	agents, err := d.ListAgents(ctx)
	if err != nil {
		t.Fatalf("listing agents: %v", err)
	}
	if agents[0].Status != "active" {
		t.Errorf("expected status 'active', got %s", agents[0].Status)
	}
}

func TestUpdateAgentHeartbeat(t *testing.T) {
	d := setupTestDB(t)
	ctx := context.Background()

	a := Agent{
		ID:        "agent-test",
		Role:      "implementer",
		Status:    "idle",
		CreatedAt: time.Now(),
	}
	if err := d.RegisterAgent(ctx, a); err != nil {
		t.Fatalf("registering agent: %v", err)
	}

	if err := d.UpdateAgentHeartbeat(ctx, a.ID); err != nil {
		t.Fatalf("updating heartbeat: %v", err)
	}

	// Verify.
	agents, err := d.ListAgents(ctx)
	if err != nil {
		t.Fatalf("listing agents: %v", err)
	}
	if !agents[0].HeartbeatAt.Valid {
		t.Error("expected heartbeat to be set")
	}
}

func TestSetAgentDead(t *testing.T) {
	d := setupTestDB(t)
	ctx := context.Background()

	a := Agent{
		ID:        "agent-test",
		Role:      "implementer",
		Status:    "idle",
		PID:       sql.NullInt64{Valid: true, Int64: 99999},
		CreatedAt: time.Now(),
	}
	if err := d.RegisterAgent(ctx, a); err != nil {
		t.Fatalf("registering agent: %v", err)
	}

	if err := d.SetAgentDead(ctx, a.ID); err != nil {
		t.Fatalf("setting agent dead: %v", err)
	}

	// Verify status and PID.
	agents, err := d.ListAgents(ctx)
	if err != nil {
		t.Fatalf("listing agents: %v", err)
	}
	if agents[0].Status != "dead" {
		t.Errorf("expected status 'dead', got %s", agents[0].Status)
	}
	if agents[0].PID.Valid {
		t.Error("expected PID to be NULL")
	}
}

func TestCreateTask(t *testing.T) {
	d := setupTestDB(t)
	ctx := context.Background()

	task := Task{
		ID:          "task-test",
		Title:       "Test Task",
		Description: sql.NullString{Valid: true, String: "Test description"},
		Status:      "pending",
		Priority:    5,
		Role:        "implementer",
		CreatedAt:   time.Now(),
	}

	if err := d.CreateTask(ctx, task); err != nil {
		t.Fatalf("creating task: %v", err)
	}

	// Verify with ListTasks.
	tasks, err := d.ListTasks(ctx)
	if err != nil {
		t.Fatalf("listing tasks: %v", err)
	}
	if len(tasks) != 1 {
		t.Fatalf("expected 1 task, got %d", len(tasks))
	}
	if tasks[0].ID != task.ID {
		t.Errorf("expected ID %s, got %s", task.ID, tasks[0].ID)
	}
	if tasks[0].Title != task.Title {
		t.Errorf("expected title %s, got %s", task.Title, tasks[0].Title)
	}
}

func TestAssignTask(t *testing.T) {
	d := setupTestDB(t)
	ctx := context.Background()

	// Create agent first (foreign key requirement).
	agent := Agent{
		ID:        "agent-1",
		Role:      "implementer",
		Status:    "idle",
		CreatedAt: time.Now(),
	}
	if err := d.RegisterAgent(ctx, agent); err != nil {
		t.Fatalf("registering agent: %v", err)
	}

	task := Task{
		ID:        "task-test",
		Title:     "Test Task",
		Status:    "pending",
		Priority:  5,
		Role:      "implementer",
		CreatedAt: time.Now(),
	}
	if err := d.CreateTask(ctx, task); err != nil {
		t.Fatalf("creating task: %v", err)
	}

	if err := d.AssignTask(ctx, task.ID, "agent-1", "worktree-1", "feature-branch"); err != nil {
		t.Fatalf("assigning task: %v", err)
	}

	// Verify.
	tasks, err := d.ListTasks(ctx)
	if err != nil {
		t.Fatalf("listing tasks: %v", err)
	}
	if tasks[0].Status != "assigned" {
		t.Errorf("expected status 'assigned', got %s", tasks[0].Status)
	}
	if !tasks[0].AssignedTo.Valid || tasks[0].AssignedTo.String != "agent-1" {
		t.Errorf("expected assigned_to 'agent-1', got %v", tasks[0].AssignedTo)
	}
	if !tasks[0].Worktree.Valid || tasks[0].Worktree.String != "worktree-1" {
		t.Errorf("expected worktree 'worktree-1', got %v", tasks[0].Worktree)
	}
	if !tasks[0].Branch.Valid || tasks[0].Branch.String != "feature-branch" {
		t.Errorf("expected branch 'feature-branch', got %v", tasks[0].Branch)
	}
}

func TestStartTask(t *testing.T) {
	d := setupTestDB(t)
	ctx := context.Background()

	task := Task{
		ID:        "task-test",
		Title:     "Test Task",
		Status:    "assigned",
		Priority:  5,
		Role:      "implementer",
		CreatedAt: time.Now(),
	}
	if err := d.CreateTask(ctx, task); err != nil {
		t.Fatalf("creating task: %v", err)
	}

	if err := d.StartTask(ctx, task.ID); err != nil {
		t.Fatalf("starting task: %v", err)
	}

	// Verify.
	tasks, err := d.ListTasks(ctx)
	if err != nil {
		t.Fatalf("listing tasks: %v", err)
	}
	if tasks[0].Status != "running" {
		t.Errorf("expected status 'running', got %s", tasks[0].Status)
	}
	if !tasks[0].StartedAt.Valid {
		t.Error("expected started_at to be set")
	}
}

func TestCompleteTask(t *testing.T) {
	d := setupTestDB(t)
	ctx := context.Background()

	task := Task{
		ID:        "task-test",
		Title:     "Test Task",
		Status:    "running",
		Priority:  5,
		Role:      "implementer",
		CreatedAt: time.Now(),
	}
	if err := d.CreateTask(ctx, task); err != nil {
		t.Fatalf("creating task: %v", err)
	}

	result := "Successfully completed"
	if err := d.CompleteTask(ctx, task.ID, result); err != nil {
		t.Fatalf("completing task: %v", err)
	}

	// Verify.
	tasks, err := d.ListTasks(ctx)
	if err != nil {
		t.Fatalf("listing tasks: %v", err)
	}
	if tasks[0].Status != "done" {
		t.Errorf("expected status 'done', got %s", tasks[0].Status)
	}
	if !tasks[0].Result.Valid || tasks[0].Result.String != result {
		t.Errorf("expected result %q, got %v", result, tasks[0].Result)
	}
	if !tasks[0].CompletedAt.Valid {
		t.Error("expected completed_at to be set")
	}
}

func TestFailTask(t *testing.T) {
	d := setupTestDB(t)
	ctx := context.Background()

	task := Task{
		ID:        "task-test",
		Title:     "Test Task",
		Status:    "running",
		Priority:  5,
		Role:      "implementer",
		CreatedAt: time.Now(),
	}
	if err := d.CreateTask(ctx, task); err != nil {
		t.Fatalf("creating task: %v", err)
	}

	reason := "Test failed"
	if err := d.FailTask(ctx, task.ID, reason); err != nil {
		t.Fatalf("failing task: %v", err)
	}

	// Verify.
	tasks, err := d.ListTasks(ctx)
	if err != nil {
		t.Fatalf("listing tasks: %v", err)
	}
	if tasks[0].Status != "failed" {
		t.Errorf("expected status 'failed', got %s", tasks[0].Status)
	}
	if !tasks[0].Result.Valid || tasks[0].Result.String != reason {
		t.Errorf("expected result %q, got %v", reason, tasks[0].Result)
	}
	if !tasks[0].CompletedAt.Valid {
		t.Error("expected completed_at to be set")
	}
}

func TestResetTask(t *testing.T) {
	d := setupTestDB(t)
	ctx := context.Background()

	// Create agent first (foreign key requirement).
	agent := Agent{
		ID:        "agent-1",
		Role:      "implementer",
		Status:    "idle",
		CreatedAt: time.Now(),
	}
	if err := d.RegisterAgent(ctx, agent); err != nil {
		t.Fatalf("registering agent: %v", err)
	}

	task := Task{
		ID:          "task-test",
		Title:       "Test Task",
		Status:      "failed",
		Priority:    5,
		Role:        "implementer",
		AssignedTo:  sql.NullString{Valid: true, String: "agent-1"},
		Worktree:    sql.NullString{Valid: true, String: "worktree-1"},
		Branch:      sql.NullString{Valid: true, String: "feature-branch"},
		Result:      sql.NullString{Valid: true, String: "Failed"},
		CreatedAt:   time.Now(),
		StartedAt:   sql.NullTime{Valid: true, Time: time.Now()},
		CompletedAt: sql.NullTime{Valid: true, Time: time.Now()},
	}
	if err := d.CreateTask(ctx, task); err != nil {
		t.Fatalf("creating task: %v", err)
	}

	if err := d.ResetTask(ctx, task.ID); err != nil {
		t.Fatalf("resetting task: %v", err)
	}

	// Verify.
	tasks, err := d.ListTasks(ctx)
	if err != nil {
		t.Fatalf("listing tasks: %v", err)
	}
	if tasks[0].Status != "pending" {
		t.Errorf("expected status 'pending', got %s", tasks[0].Status)
	}
	if tasks[0].AssignedTo.Valid {
		t.Error("expected assigned_to to be NULL")
	}
	if tasks[0].Worktree.Valid {
		t.Error("expected worktree to be NULL")
	}
	if tasks[0].Branch.Valid {
		t.Error("expected branch to be NULL")
	}
	if tasks[0].Result.Valid {
		t.Error("expected result to be NULL")
	}
	if tasks[0].StartedAt.Valid {
		t.Error("expected started_at to be NULL")
	}
	if tasks[0].CompletedAt.Valid {
		t.Error("expected completed_at to be NULL")
	}
}

func TestSetBlackboard(t *testing.T) {
	d := setupTestDB(t)
	ctx := context.Background()

	if err := d.SetBlackboard(ctx, "test-key", "test-value", "conductor"); err != nil {
		t.Fatalf("setting blackboard: %v", err)
	}

	// Read back.
	val, err := d.GetBlackboardValue(ctx, "test-key")
	if err != nil {
		t.Fatalf("getting blackboard value: %v", err)
	}
	if val != "test-value" {
		t.Errorf("expected value 'test-value', got %q", val)
	}

	// Test replacement.
	if err := d.SetBlackboard(ctx, "test-key", "new-value", "conductor"); err != nil {
		t.Fatalf("replacing blackboard value: %v", err)
	}
	val, err = d.GetBlackboardValue(ctx, "test-key")
	if err != nil {
		t.Fatalf("getting blackboard value: %v", err)
	}
	if val != "new-value" {
		t.Errorf("expected value 'new-value', got %q", val)
	}
}

func TestDeleteBlackboard(t *testing.T) {
	d := setupTestDB(t)
	ctx := context.Background()

	if err := d.SetBlackboard(ctx, "test-key", "test-value", "conductor"); err != nil {
		t.Fatalf("setting blackboard: %v", err)
	}

	if err := d.DeleteBlackboard(ctx, "test-key"); err != nil {
		t.Fatalf("deleting blackboard: %v", err)
	}

	// Verify gone.
	val, err := d.GetBlackboardValue(ctx, "test-key")
	if err != nil {
		t.Fatalf("getting blackboard value: %v", err)
	}
	if val != "" {
		t.Errorf("expected empty string (deleted), got %q", val)
	}
}

func TestAcquireBlackboardLock(t *testing.T) {
	d := setupTestDB(t)
	ctx := context.Background()

	// First acquire should succeed.
	acquired, err := d.AcquireBlackboardLock(ctx, "lock-key", "agent-1")
	if err != nil {
		t.Fatalf("acquiring lock: %v", err)
	}
	if !acquired {
		t.Error("expected first acquire to succeed")
	}

	// Second acquire should fail (key already exists).
	acquired, err = d.AcquireBlackboardLock(ctx, "lock-key", "agent-2")
	if err != nil {
		t.Fatalf("acquiring lock: %v", err)
	}
	if acquired {
		t.Error("expected second acquire to fail")
	}

	// Verify lock value.
	val, err := d.GetBlackboardValue(ctx, "lock-key")
	if err != nil {
		t.Fatalf("getting lock value: %v", err)
	}
	if val != "locked" {
		t.Errorf("expected value 'locked', got %q", val)
	}
}

func TestLogEvent(t *testing.T) {
	d := setupTestDB(t)
	ctx := context.Background()

	if err := d.LogEvent(ctx, "task_created", "agent-1", "task-1", `{"title":"Test"}`); err != nil {
		t.Fatalf("logging event: %v", err)
	}

	// Verify with RecentEvents.
	events, err := d.RecentEvents(ctx, 10)
	if err != nil {
		t.Fatalf("getting recent events: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	if events[0].EventType != "task_created" {
		t.Errorf("expected event_type 'task_created', got %s", events[0].EventType)
	}
	if !events[0].AgentID.Valid || events[0].AgentID.String != "agent-1" {
		t.Errorf("expected agent_id 'agent-1', got %v", events[0].AgentID)
	}
	if !events[0].TaskID.Valid || events[0].TaskID.String != "task-1" {
		t.Errorf("expected task_id 'task-1', got %v", events[0].TaskID)
	}
}

func TestCreateFileLock(t *testing.T) {
	d := setupTestDB(t)
	ctx := context.Background()

	// Create agent first (foreign key requirement).
	agent := Agent{
		ID:        "agent-1",
		Role:      "implementer",
		Status:    "idle",
		CreatedAt: time.Now(),
	}
	if err := d.RegisterAgent(ctx, agent); err != nil {
		t.Fatalf("registering agent: %v", err)
	}

	// Create task (foreign key requirement).
	task := Task{
		ID:        "task-1",
		Title:     "Test Task",
		Status:    "pending",
		Priority:  5,
		Role:      "implementer",
		CreatedAt: time.Now(),
	}
	if err := d.CreateTask(ctx, task); err != nil {
		t.Fatalf("creating task: %v", err)
	}

	expiresAt := time.Now().Add(1 * time.Hour)
	if err := d.CreateFileLock(ctx, "/path/to/file", "agent-1", "task-1", expiresAt); err != nil {
		t.Fatalf("creating file lock: %v", err)
	}

	// Verify.
	locks, err := d.ListFileLocks(ctx)
	if err != nil {
		t.Fatalf("listing locks: %v", err)
	}
	if len(locks) != 1 {
		t.Fatalf("expected 1 lock, got %d", len(locks))
	}
	if locks[0].FilePath != "/path/to/file" {
		t.Errorf("expected file_path '/path/to/file', got %s", locks[0].FilePath)
	}
	if !locks[0].LockedBy.Valid || locks[0].LockedBy.String != "agent-1" {
		t.Errorf("expected locked_by 'agent-1', got %v", locks[0].LockedBy)
	}
}

func TestReleaseFileLock(t *testing.T) {
	d := setupTestDB(t)
	ctx := context.Background()

	// Create agent first (foreign key requirement).
	agent := Agent{
		ID:        "agent-1",
		Role:      "implementer",
		Status:    "idle",
		CreatedAt: time.Now(),
	}
	if err := d.RegisterAgent(ctx, agent); err != nil {
		t.Fatalf("registering agent: %v", err)
	}

	// Create task (foreign key requirement).
	task := Task{
		ID:        "task-1",
		Title:     "Test Task",
		Status:    "pending",
		Priority:  5,
		Role:      "implementer",
		CreatedAt: time.Now(),
	}
	if err := d.CreateTask(ctx, task); err != nil {
		t.Fatalf("creating task: %v", err)
	}

	expiresAt := time.Now().Add(1 * time.Hour)
	if err := d.CreateFileLock(ctx, "/path/to/file", "agent-1", "task-1", expiresAt); err != nil {
		t.Fatalf("creating file lock: %v", err)
	}

	if err := d.ReleaseFileLock(ctx, "/path/to/file"); err != nil {
		t.Fatalf("releasing file lock: %v", err)
	}

	// Verify gone.
	locks, err := d.ListFileLocks(ctx)
	if err != nil {
		t.Fatalf("listing locks: %v", err)
	}
	if len(locks) != 0 {
		t.Errorf("expected 0 locks, got %d", len(locks))
	}
}

func TestCleanupExpiredLocks(t *testing.T) {
	d := setupTestDB(t)
	ctx := context.Background()

	// Create agents (foreign key requirement).
	agent1 := Agent{
		ID:        "agent-1",
		Role:      "implementer",
		Status:    "idle",
		CreatedAt: time.Now(),
	}
	if err := d.RegisterAgent(ctx, agent1); err != nil {
		t.Fatalf("registering agent1: %v", err)
	}

	agent2 := Agent{
		ID:        "agent-2",
		Role:      "scout",
		Status:    "idle",
		CreatedAt: time.Now(),
	}
	if err := d.RegisterAgent(ctx, agent2); err != nil {
		t.Fatalf("registering agent2: %v", err)
	}

	// Create tasks (foreign key requirement).
	task1 := Task{
		ID:        "task-1",
		Title:     "Task 1",
		Status:    "pending",
		Priority:  5,
		Role:      "implementer",
		CreatedAt: time.Now(),
	}
	if err := d.CreateTask(ctx, task1); err != nil {
		t.Fatalf("creating task1: %v", err)
	}

	task2 := Task{
		ID:        "task-2",
		Title:     "Task 2",
		Status:    "pending",
		Priority:  5,
		Role:      "scout",
		CreatedAt: time.Now(),
	}
	if err := d.CreateTask(ctx, task2); err != nil {
		t.Fatalf("creating task2: %v", err)
	}

	// Create an expired lock with a much older timestamp.
	expiresAt := time.Now().Add(-24 * time.Hour)
	if err := d.CreateFileLock(ctx, "/path/to/expired", "agent-1", "task-1", expiresAt); err != nil {
		t.Fatalf("creating expired lock: %v", err)
	}

	// Create a non-expired lock.
	expiresAt2 := time.Now().Add(24 * time.Hour)
	if err := d.CreateFileLock(ctx, "/path/to/active", "agent-2", "task-2", expiresAt2); err != nil {
		t.Fatalf("creating active lock: %v", err)
	}

	// Give SQLite a moment to ensure CURRENT_TIMESTAMP is later.
	time.Sleep(10 * time.Millisecond)

	if err := d.CleanupExpiredLocks(ctx); err != nil {
		t.Fatalf("cleaning up expired locks: %v", err)
	}

	// Verify only non-expired lock remains.
	locks, err := d.ListFileLocks(ctx)
	if err != nil {
		t.Fatalf("listing locks: %v", err)
	}
	if len(locks) != 1 {
		t.Fatalf("expected 1 lock, got %d", len(locks))
	}
	if locks[0].FilePath != "/path/to/active" {
		t.Errorf("expected active lock, got %s", locks[0].FilePath)
	}
}

func TestUpdateNonexistentAgent(t *testing.T) {
	d := setupTestDB(t)
	ctx := context.Background()

	err := d.UpdateAgentPID(ctx, "nonexistent", 12345)
	if err == nil {
		t.Error("expected error updating nonexistent agent, got nil")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("expected 'not found' in error, got: %v", err)
	}
}

func TestGetAgentByTask(t *testing.T) {
	d := setupTestDB(t)
	ctx := context.Background()

	// Create agent.
	a := Agent{
		ID:        "agent-1",
		Role:      "implementer",
		Status:    "active",
		CreatedAt: time.Now(),
	}
	if err := d.RegisterAgent(ctx, a); err != nil {
		t.Fatalf("registering agent: %v", err)
	}

	// Create task.
	task := Task{
		ID:        "task-1",
		Title:     "Test Task",
		Status:    "pending",
		Priority:  5,
		Role:      "implementer",
		CreatedAt: time.Now(),
	}
	if err := d.CreateTask(ctx, task); err != nil {
		t.Fatalf("creating task: %v", err)
	}

	// Assign task to agent.
	if err := d.AssignTask(ctx, task.ID, a.ID, "worktree-1", "branch-1"); err != nil {
		t.Fatalf("assigning task: %v", err)
	}

	// Lookup agent by task.
	agent, err := d.GetAgentByTask(ctx, task.ID)
	if err != nil {
		t.Fatalf("getting agent by task: %v", err)
	}
	if agent == nil {
		t.Fatal("expected agent, got nil")
	}
	if agent.ID != a.ID {
		t.Errorf("expected agent ID %s, got %s", a.ID, agent.ID)
	}
	if agent.Role != a.Role {
		t.Errorf("expected role %s, got %s", a.Role, agent.Role)
	}

	// Test unassigned task.
	task2 := Task{
		ID:        "task-2",
		Title:     "Unassigned Task",
		Status:    "pending",
		Priority:  5,
		Role:      "implementer",
		CreatedAt: time.Now(),
	}
	if err := d.CreateTask(ctx, task2); err != nil {
		t.Fatalf("creating task2: %v", err)
	}
	agent, err = d.GetAgentByTask(ctx, task2.ID)
	if err != nil {
		t.Fatalf("getting agent by unassigned task: %v", err)
	}
	if agent != nil {
		t.Errorf("expected nil agent for unassigned task, got %v", agent)
	}
}

// --- Conductor tests ---

func TestCreateConductor(t *testing.T) {
	d := setupTestDB(t)
	ctx := context.Background()

	c := ConductorRecord{
		ID:              "s-20260228-120000-abcd",
		PID:             12345,
		Goal:            "Implement feature X",
		Status:          "active",
		StagingBranch:   "conductor/s-20260228-120000-abcd",
		BaseBranch:      "dev",
		MaxParallel:     3,
		TestCmd:         sql.NullString{Valid: true, String: "go test ./..."},
		MergeReview:     true,
		ModelStrategy:   "all-opus",
		Runtime:         "local",
		RepoMap:         true,
		LenientDeps:     false,
		FileEnforcement: "defense",
	}

	if err := d.CreateConductor(ctx, c); err != nil {
		t.Fatalf("creating conductor: %v", err)
	}

	// Verify with GetConductor
	got, err := d.GetConductor(ctx, c.ID)
	if err != nil {
		t.Fatalf("getting conductor: %v", err)
	}
	if got == nil {
		t.Fatal("expected conductor, got nil")
	}
	if got.ID != c.ID {
		t.Errorf("expected ID %s, got %s", c.ID, got.ID)
	}
	if got.PID != c.PID {
		t.Errorf("expected PID %d, got %d", c.PID, got.PID)
	}
	if got.Goal != c.Goal {
		t.Errorf("expected goal %q, got %q", c.Goal, got.Goal)
	}
	if got.Status != "active" {
		t.Errorf("expected status 'active', got %s", got.Status)
	}
	if got.StagingBranch != c.StagingBranch {
		t.Errorf("expected staging_branch %s, got %s", c.StagingBranch, got.StagingBranch)
	}
	if got.BaseBranch != c.BaseBranch {
		t.Errorf("expected base_branch %s, got %s", c.BaseBranch, got.BaseBranch)
	}
	if got.MaxParallel != 3 {
		t.Errorf("expected max_parallel 3, got %d", got.MaxParallel)
	}
	if !got.TestCmd.Valid || got.TestCmd.String != "go test ./..." {
		t.Errorf("expected test_cmd 'go test ./...', got %v", got.TestCmd)
	}
	if !got.MergeReview {
		t.Error("expected merge_review true")
	}
	if got.ModelStrategy != "all-opus" {
		t.Errorf("expected model_strategy 'all-opus', got %s", got.ModelStrategy)
	}
	if got.Runtime != "local" {
		t.Errorf("expected runtime 'local', got %s", got.Runtime)
	}
	if !got.RepoMap {
		t.Error("expected repo_map true")
	}
	if got.LenientDeps {
		t.Error("expected lenient_deps false")
	}
	if got.FileEnforcement != "defense" {
		t.Errorf("expected file_enforcement 'defense', got %s", got.FileEnforcement)
	}
}

func TestUpdateConductorStatus(t *testing.T) {
	d := setupTestDB(t)
	ctx := context.Background()

	c := ConductorRecord{
		ID:            "s-test-status",
		PID:           1234,
		Goal:          "Test goal",
		Status:        "active",
		StagingBranch: "conductor/s-test-status",
		BaseBranch:    "dev",
		MaxParallel:   3,
		ModelStrategy: "all-opus",
		Runtime:       "local",
	}
	if err := d.CreateConductor(ctx, c); err != nil {
		t.Fatalf("creating conductor: %v", err)
	}

	// Update to completed — should set completed_at
	if err := d.UpdateConductorStatus(ctx, c.ID, "completed"); err != nil {
		t.Fatalf("updating status: %v", err)
	}

	got, err := d.GetConductor(ctx, c.ID)
	if err != nil {
		t.Fatalf("getting conductor: %v", err)
	}
	if got.Status != "completed" {
		t.Errorf("expected status 'completed', got %s", got.Status)
	}
	if !got.CompletedAt.Valid {
		t.Error("expected completed_at to be set for terminal status")
	}
}

func TestUpdateConductorHeartbeat(t *testing.T) {
	d := setupTestDB(t)
	ctx := context.Background()

	c := ConductorRecord{
		ID:            "s-test-hb",
		PID:           1234,
		Goal:          "Test goal",
		Status:        "active",
		StagingBranch: "conductor/s-test-hb",
		BaseBranch:    "dev",
		MaxParallel:   3,
		ModelStrategy: "all-opus",
		Runtime:       "local",
	}
	if err := d.CreateConductor(ctx, c); err != nil {
		t.Fatalf("creating conductor: %v", err)
	}

	if err := d.UpdateConductorHeartbeat(ctx, c.ID); err != nil {
		t.Fatalf("updating heartbeat: %v", err)
	}

	// Just verify no error — heartbeat is CURRENT_TIMESTAMP
	got, err := d.GetConductor(ctx, c.ID)
	if err != nil {
		t.Fatalf("getting conductor: %v", err)
	}
	if got == nil {
		t.Fatal("expected conductor, got nil")
	}
}

func TestListActiveConductors(t *testing.T) {
	d := setupTestDB(t)
	ctx := context.Background()

	// Create two active conductors
	for _, id := range []string{"s-1", "s-2"} {
		c := ConductorRecord{
			ID:            id,
			PID:           1000,
			Goal:          "Goal " + id,
			Status:        "active",
			StagingBranch: "conductor/" + id,
			BaseBranch:    "dev",
			MaxParallel:   3,
			ModelStrategy: "all-opus",
			Runtime:       "local",
		}
		if err := d.CreateConductor(ctx, c); err != nil {
			t.Fatalf("creating conductor %s: %v", id, err)
		}
	}

	// Create one completed conductor
	c3 := ConductorRecord{
		ID:            "s-3",
		PID:           1001,
		Goal:          "Goal s-3",
		Status:        "active",
		StagingBranch: "conductor/s-3",
		BaseBranch:    "dev",
		MaxParallel:   3,
		ModelStrategy: "all-opus",
		Runtime:       "local",
	}
	if err := d.CreateConductor(ctx, c3); err != nil {
		t.Fatalf("creating conductor s-3: %v", err)
	}
	if err := d.UpdateConductorStatus(ctx, "s-3", "completed"); err != nil {
		t.Fatalf("completing conductor: %v", err)
	}

	// ListActiveConductors should return only the 2 active ones
	active, err := d.ListActiveConductors(ctx)
	if err != nil {
		t.Fatalf("listing active conductors: %v", err)
	}
	if len(active) != 2 {
		t.Errorf("expected 2 active conductors, got %d", len(active))
	}
}

func TestGetConductorByPID(t *testing.T) {
	d := setupTestDB(t)
	ctx := context.Background()

	c := ConductorRecord{
		ID:            "s-pid-test",
		PID:           99999,
		Goal:          "Test goal",
		Status:        "active",
		StagingBranch: "conductor/s-pid-test",
		BaseBranch:    "dev",
		MaxParallel:   3,
		ModelStrategy: "all-opus",
		Runtime:       "local",
	}
	if err := d.CreateConductor(ctx, c); err != nil {
		t.Fatalf("creating conductor: %v", err)
	}

	got, err := d.GetConductorByPID(ctx, 99999)
	if err != nil {
		t.Fatalf("getting conductor by PID: %v", err)
	}
	if got == nil {
		t.Fatal("expected conductor, got nil")
	}
	if got.ID != c.ID {
		t.Errorf("expected ID %s, got %s", c.ID, got.ID)
	}

	// Non-existent PID
	got, err = d.GetConductorByPID(ctx, 11111)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != nil {
		t.Errorf("expected nil for non-existent PID, got %v", got)
	}
}

func TestGetConductorNotFound(t *testing.T) {
	d := setupTestDB(t)
	ctx := context.Background()

	got, err := d.GetConductor(ctx, "nonexistent")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != nil {
		t.Errorf("expected nil, got %v", got)
	}
}

func TestMigration002Idempotent(t *testing.T) {
	d := setupTestDB(t) // calls InitSchema once
	ctx := context.Background()

	// Run InitSchema again — migration 002 should be idempotent
	if err := d.InitSchema(ctx); err != nil {
		t.Fatalf("second InitSchema call should be idempotent: %v", err)
	}

	// Verify conductors table exists
	var name string
	err := d.QueryRowContext(ctx,
		`SELECT name FROM sqlite_master WHERE type='table' AND name='conductors'`).Scan(&name)
	if err != nil {
		t.Fatalf("conductors table not found: %v", err)
	}

	// Verify conductor_id columns exist on tasks, agents, file_locks
	for _, table := range []string{"tasks", "agents", "file_locks"} {
		var colExists int
		err := d.QueryRowContext(ctx,
			`SELECT COUNT(*) FROM pragma_table_info(?) WHERE name = 'conductor_id'`, table).Scan(&colExists)
		if err != nil {
			t.Fatalf("checking conductor_id on %s: %v", table, err)
		}
		if colExists == 0 {
			t.Errorf("expected conductor_id column on %s", table)
		}
	}
}

func TestTruncateAllIncludesConductors(t *testing.T) {
	d := setupTestDB(t)
	ctx := context.Background()

	// Create a conductor
	c := ConductorRecord{
		ID:            "s-truncate-test",
		PID:           1234,
		Goal:          "Test goal",
		Status:        "active",
		StagingBranch: "conductor/s-truncate-test",
		BaseBranch:    "dev",
		MaxParallel:   3,
		ModelStrategy: "all-opus",
		Runtime:       "local",
	}
	if err := d.CreateConductor(ctx, c); err != nil {
		t.Fatalf("creating conductor: %v", err)
	}

	// Truncate all
	if err := d.TruncateAll(ctx); err != nil {
		t.Fatalf("truncating: %v", err)
	}

	// Verify conductors are gone
	all, err := d.ListAllConductors(ctx)
	if err != nil {
		t.Fatalf("listing conductors: %v", err)
	}
	if len(all) != 0 {
		t.Errorf("expected 0 conductors after truncate, got %d", len(all))
	}
}

func TestResetCascadeFailedDependents(t *testing.T) {
	d := setupTestDB(t)
	ctx := context.Background()

	// Blocker task (failed)
	d.CreateTask(ctx, Task{ID: "blocker", Title: "Blocker", Status: "failed", Role: "researcher", CreatedAt: time.Now()})

	// Cascade-failed dependent
	d.CreateTask(ctx, Task{
		ID: "dep1", Title: "Dependent 1", Status: "failed", Role: "implementer",
		BlockedBy: sql.NullString{String: `["blocker"]`, Valid: true},
		CreatedAt: time.Now(),
	})
	d.SetBlackboard(ctx, "failure_type:dep1", "cascade_fail", "monitor")

	// Genuinely failed task (also blocked by same blocker, but failed on its own)
	d.CreateTask(ctx, Task{
		ID: "dep2", Title: "Dependent 2", Status: "failed", Role: "implementer",
		BlockedBy: sql.NullString{String: `["blocker"]`, Valid: true},
		CreatedAt: time.Now(),
	})
	d.SetBlackboard(ctx, "failure_type:dep2", "test_failure", "monitor")

	n, err := d.ResetCascadeFailedDependents(ctx, "blocker")
	if err != nil {
		t.Fatalf("ResetCascadeFailedDependents: %v", err)
	}

	// Only dep1 (cascade_fail) should be reset, not dep2 (genuine failure)
	if n != 1 {
		t.Errorf("expected 1 reset, got %d", n)
	}

	dep1, _ := d.GetTaskByID(ctx, "dep1")
	if dep1.Status != "pending" {
		t.Errorf("dep1: expected pending, got %s", dep1.Status)
	}

	dep2, _ := d.GetTaskByID(ctx, "dep2")
	if dep2.Status != "failed" {
		t.Errorf("dep2: expected still failed, got %s", dep2.Status)
	}

	// failure_type should be cleaned up for dep1
	ft, _ := d.GetBlackboardValue(ctx, "failure_type:dep1")
	if ft != "" {
		t.Errorf("expected failure_type:dep1 cleared, got %q", ft)
	}

	// failure_type for dep2 should be untouched
	ft2, _ := d.GetBlackboardValue(ctx, "failure_type:dep2")
	if ft2 != "test_failure" {
		t.Errorf("expected failure_type:dep2 unchanged, got %q", ft2)
	}
}

func TestCreateConductorDuplicate(t *testing.T) {
	// G103 defense-in-depth: INSERT OR IGNORE should silently skip duplicates.
	d := setupTestDB(t)
	ctx := context.Background()

	c := ConductorRecord{
		ID:            "s-dup-test",
		PID:           12345,
		Goal:          "Test duplicate insert",
		Status:        "active",
		StagingBranch: "conductor/s-dup-test",
		BaseBranch:    "dev",
		MaxParallel:   3,
		ModelStrategy: "all-opus",
		Runtime:       "local",
	}

	// First insert should succeed
	if err := d.CreateConductor(ctx, c); err != nil {
		t.Fatalf("first CreateConductor failed: %v", err)
	}

	// Second insert with same ID should not error (INSERT OR IGNORE)
	if err := d.CreateConductor(ctx, c); err != nil {
		t.Fatalf("second CreateConductor should not error with INSERT OR IGNORE, got: %v", err)
	}

	// Verify only 1 record exists
	all, err := d.ListAllConductors(ctx)
	if err != nil {
		t.Fatalf("listing conductors: %v", err)
	}
	if len(all) != 1 {
		t.Errorf("expected 1 conductor after duplicate insert, got %d", len(all))
	}
}

// --- B-273: MergeMode tests ---

func TestMigrate004MergeMode(t *testing.T) {
	// Verify the merge_mode column exists after migration.
	d := setupTestDB(t)
	ctx := context.Background()

	// Create a conductor and verify merge_mode defaults to "local".
	c := ConductorRecord{
		ID:            "s-migrate004-test",
		PID:           1,
		Goal:          "test",
		Status:        "active",
		StagingBranch: "conductor/s-migrate004-test",
		BaseBranch:    "dev",
		MaxParallel:   1,
		ModelStrategy: "all-opus",
		Runtime:       "local",
	}
	if err := d.CreateConductor(ctx, c); err != nil {
		t.Fatalf("creating conductor: %v", err)
	}

	got, err := d.GetConductor(ctx, c.ID)
	if err != nil {
		t.Fatalf("getting conductor: %v", err)
	}
	if got.MergeMode != "local" {
		t.Errorf("expected default merge_mode='local', got %q", got.MergeMode)
	}
}

func TestCreateConductorWithMergeMode(t *testing.T) {
	// Verify merge_mode is persisted and read back.
	d := setupTestDB(t)
	ctx := context.Background()

	c := ConductorRecord{
		ID:            "s-mergemode-pr",
		PID:           1,
		Goal:          "test PR mode",
		Status:        "active",
		StagingBranch: "conductor/s-mergemode-pr",
		BaseBranch:    "dev",
		MaxParallel:   1,
		ModelStrategy: "all-opus",
		Runtime:       "local",
		MergeMode:     "pr",
	}
	if err := d.CreateConductor(ctx, c); err != nil {
		t.Fatalf("creating conductor: %v", err)
	}

	got, err := d.GetConductor(ctx, c.ID)
	if err != nil {
		t.Fatalf("getting conductor: %v", err)
	}
	if got.MergeMode != "pr" {
		t.Errorf("expected merge_mode='pr', got %q", got.MergeMode)
	}

	// Also verify via ListAllConductors
	all, err := d.ListAllConductors(ctx)
	if err != nil {
		t.Fatalf("listing conductors: %v", err)
	}
	foundPR := false
	for _, cond := range all {
		if cond.ID == "s-mergemode-pr" && cond.MergeMode == "pr" {
			foundPR = true
		}
	}
	if !foundPR {
		t.Error("expected to find conductor with merge_mode='pr' in ListAllConductors")
	}
}

func TestInsertEvalScenario(t *testing.T) {
	d := setupTestDB(t)
	ctx := context.Background()

	s := EvalScenario{
		ID:              "es-001",
		Role:            "implementer",
		Category:        sql.NullString{String: "unit-test", Valid: true},
		RepoPath:        sql.NullString{String: "/tmp/repo", Valid: true},
		Goal:            "Implement a sorting function",
		ExpectedOutcome: sql.NullString{String: "Tests pass", Valid: true},
		Difficulty:      sql.NullString{String: "easy", Valid: true},
	}
	if err := d.InsertEvalScenario(ctx, s); err != nil {
		t.Fatalf("inserting eval scenario: %v", err)
	}

	scenarios, err := d.ListEvalScenarios(ctx)
	if err != nil {
		t.Fatalf("listing scenarios: %v", err)
	}
	if len(scenarios) != 1 {
		t.Fatalf("expected 1 scenario, got %d", len(scenarios))
	}
	if scenarios[0].ID != "es-001" || scenarios[0].Role != "implementer" {
		t.Errorf("unexpected scenario: %+v", scenarios[0])
	}
}

func TestInsertEvalVersion(t *testing.T) {
	d := setupTestDB(t)
	ctx := context.Background()

	v := EvalVersion{
		ID:         "ev-001",
		ParentID:   sql.NullString{String: "", Valid: false},
		Branch:     sql.NullString{String: "main", Valid: true},
		CommitHash: sql.NullString{String: "abc123", Valid: true},
		Status:     "candidate",
	}
	if err := d.InsertEvalVersion(ctx, v); err != nil {
		t.Fatalf("inserting eval version: %v", err)
	}

	got, err := d.GetEvalVersion(ctx, "ev-001")
	if err != nil {
		t.Fatalf("getting eval version: %v", err)
	}
	if got == nil {
		t.Fatal("expected eval version, got nil")
	}
	if got.Status != "candidate" {
		t.Errorf("expected status 'candidate', got %q", got.Status)
	}
}

func TestInsertEvalRun(t *testing.T) {
	d := setupTestDB(t)
	ctx := context.Background()

	// Insert prerequisites.
	if err := d.InsertEvalScenario(ctx, EvalScenario{ID: "es-r1", Role: "implementer", Goal: "goal"}); err != nil {
		t.Fatal(err)
	}
	if err := d.InsertEvalVersion(ctx, EvalVersion{ID: "ev-r1", Status: "candidate"}); err != nil {
		t.Fatal(err)
	}

	r := EvalRun{ID: "er-001", VersionID: "ev-r1", ScenarioID: "es-r1", Status: "pending"}
	if err := d.InsertEvalRun(ctx, r); err != nil {
		t.Fatalf("inserting eval run: %v", err)
	}

	runs, err := d.ListEvalRunsForVersion(ctx, "ev-r1")
	if err != nil {
		t.Fatalf("listing runs: %v", err)
	}
	if len(runs) != 1 || runs[0].ID != "er-001" {
		t.Errorf("unexpected runs: %+v", runs)
	}
}

func TestInsertEvalResult(t *testing.T) {
	d := setupTestDB(t)
	ctx := context.Background()

	// Insert prerequisites.
	if err := d.InsertEvalScenario(ctx, EvalScenario{ID: "es-res1", Role: "implementer", Goal: "goal"}); err != nil {
		t.Fatal(err)
	}
	if err := d.InsertEvalVersion(ctx, EvalVersion{ID: "ev-res1", Status: "candidate"}); err != nil {
		t.Fatal(err)
	}
	if err := d.InsertEvalRun(ctx, EvalRun{ID: "er-res1", VersionID: "ev-res1", ScenarioID: "es-res1", Status: "passed"}); err != nil {
		t.Fatal(err)
	}

	result := EvalResult{
		ID:      "eres-001",
		RunID:   "er-res1",
		Metric:  "completion",
		Score:   0.95,
		Weight:  1.0,
		Details: sql.NullString{String: "all tests pass", Valid: true},
	}
	if err := d.InsertEvalResult(ctx, result); err != nil {
		t.Fatalf("inserting eval result: %v", err)
	}

	results, err := d.ListEvalResultsForRun(ctx, "er-res1")
	if err != nil {
		t.Fatalf("listing results: %v", err)
	}
	if len(results) != 1 || results[0].Score != 0.95 {
		t.Errorf("unexpected results: %+v", results)
	}
}

func TestInsertHealingLog(t *testing.T) {
	d := setupTestDB(t)
	ctx := context.Background()

	h := HealingLog{
		ID:         "hl-001",
		SessionID:  "sess-001",
		TaskID:     sql.NullString{String: "t-001", Valid: true},
		ErrorType:  sql.NullString{String: "test_failure", Valid: true},
		FixApplied: sql.NullString{String: "added missing import", Valid: true},
		Success:    1,
		RolledBack: 0,
	}
	if err := d.InsertHealingLog(ctx, h); err != nil {
		t.Fatalf("inserting healing log: %v", err)
	}

	logs, err := d.ListHealingLogForSession(ctx, "sess-001")
	if err != nil {
		t.Fatalf("listing healing logs: %v", err)
	}
	if len(logs) != 1 || logs[0].ID != "hl-001" {
		t.Errorf("unexpected logs: %+v", logs)
	}
}

func TestUpdateEvalRunStatus(t *testing.T) {
	d := setupTestDB(t)
	ctx := context.Background()

	if err := d.InsertEvalScenario(ctx, EvalScenario{ID: "es-urs", Role: "implementer", Goal: "goal"}); err != nil {
		t.Fatal(err)
	}
	if err := d.InsertEvalVersion(ctx, EvalVersion{ID: "ev-urs", Status: "candidate"}); err != nil {
		t.Fatal(err)
	}
	if err := d.InsertEvalRun(ctx, EvalRun{ID: "er-urs", VersionID: "ev-urs", ScenarioID: "es-urs", Status: "pending"}); err != nil {
		t.Fatal(err)
	}

	// Transition to running (should set started_at).
	if err := d.UpdateEvalRunStatus(ctx, "er-urs", "running"); err != nil {
		t.Fatalf("updating to running: %v", err)
	}
	runs, _ := d.ListEvalRunsForVersion(ctx, "ev-urs")
	if runs[0].Status != "running" {
		t.Errorf("expected 'running', got %q", runs[0].Status)
	}
	if !runs[0].StartedAt.Valid {
		t.Error("expected started_at to be set")
	}

	// Transition to passed (should set completed_at).
	if err := d.UpdateEvalRunStatus(ctx, "er-urs", "passed"); err != nil {
		t.Fatalf("updating to passed: %v", err)
	}
	runs, _ = d.ListEvalRunsForVersion(ctx, "ev-urs")
	if runs[0].Status != "passed" {
		t.Errorf("expected 'passed', got %q", runs[0].Status)
	}
	if !runs[0].CompletedAt.Valid {
		t.Error("expected completed_at to be set")
	}
}

func TestUpdateEvalVersionStatus(t *testing.T) {
	d := setupTestDB(t)
	ctx := context.Background()

	if err := d.InsertEvalVersion(ctx, EvalVersion{ID: "ev-uvs", Status: "candidate"}); err != nil {
		t.Fatal(err)
	}

	if err := d.UpdateEvalVersionStatus(ctx, "ev-uvs", "promoted"); err != nil {
		t.Fatalf("updating version status: %v", err)
	}

	got, _ := d.GetEvalVersion(ctx, "ev-uvs")
	if got.Status != "promoted" {
		t.Errorf("expected 'promoted', got %q", got.Status)
	}
}
