package delegate

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/MochaCosine1206/orchestra/internal/db"
)

func setupTestDB(t *testing.T) *db.DB {
	t.Helper()
	d, err := db.OpenMemory()
	if err != nil {
		t.Fatalf("opening memory db: %v", err)
	}
	if err := d.InitSchema(context.Background()); err != nil {
		t.Fatalf("initializing schema: %v", err)
	}
	t.Cleanup(func() { d.Close() })
	return d
}

func TestNew(t *testing.T) {
	testDB := setupTestDB(t)
	d := New(testDB)
	if d.DB == nil {
		t.Error("expected DB to be set")
	}
}

func TestNewFull(t *testing.T) {
	testDB := setupTestDB(t)
	d := NewFull(testDB, nil, nil)
	if d.DB == nil {
		t.Error("expected DB to be set")
	}
}

func TestRetryTaskViaDB(t *testing.T) {
	testDB := setupTestDB(t)
	ctx := context.Background()

	err := testDB.CreateTask(ctx, db.Task{
		ID:     "t1",
		Title:  "Test task",
		Status: "failed",
		Role:   "implementer",
		Result: sql.NullString{String: "some error", Valid: true},
	})
	if err != nil {
		t.Fatalf("creating task: %v", err)
	}

	d := New(testDB)
	result := d.RetryTask(ctx, "t1")
	if result.Err != nil {
		t.Fatalf("unexpected error: %v", result.Err)
	}

	tasks, _ := testDB.ListTasks(ctx)
	if len(tasks) != 1 {
		t.Fatalf("expected 1 task, got %d", len(tasks))
	}
	if tasks[0].Status != "pending" {
		t.Errorf("expected status 'pending', got %q", tasks[0].Status)
	}
	if tasks[0].Result.Valid {
		t.Errorf("expected result to be cleared, got %q", tasks[0].Result.String)
	}
}

func TestRetryTaskNotFound(t *testing.T) {
	testDB := setupTestDB(t)
	d := New(testDB)

	result := d.RetryTask(context.Background(), "nonexistent")
	if result.Err == nil {
		t.Error("expected error for nonexistent task")
	}
}

func TestRetryTaskNoDB(t *testing.T) {
	d := &Delegate{}
	result := d.RetryTask(context.Background(), "t1")
	if result.Err == nil {
		t.Error("expected error when DB not configured")
	}
}

func TestKillTaskViaDB(t *testing.T) {
	testDB := setupTestDB(t)
	ctx := context.Background()

	err := testDB.RegisterAgent(ctx, db.Agent{
		ID:        "a1",
		Role:      "implementer",
		Status:    "active",
		PID:       sql.NullInt64{Int64: 99999, Valid: true},
		CreatedAt: time.Now(),
	})
	if err != nil {
		t.Fatalf("registering agent: %v", err)
	}

	err = testDB.CreateTask(ctx, db.Task{
		ID:     "t1",
		Title:  "Running task",
		Status: "running",
		Role:   "implementer",
	})
	if err != nil {
		t.Fatalf("creating task: %v", err)
	}

	err = testDB.AssignTask(ctx, "t1", "a1", ".worktree/t1", "task/t1")
	if err != nil {
		t.Fatalf("assigning task: %v", err)
	}

	d := New(testDB)
	result := d.KillTask(ctx, "t1")
	if result.Err != nil {
		t.Fatalf("unexpected error: %v", result.Err)
	}

	tasks, _ := testDB.ListTasks(ctx)
	if len(tasks) != 1 {
		t.Fatalf("expected 1 task, got %d", len(tasks))
	}
	if tasks[0].Status != "failed" {
		t.Errorf("expected status 'failed', got %q", tasks[0].Status)
	}
	if !tasks[0].Result.Valid || tasks[0].Result.String != "killed by user" {
		t.Errorf("expected result 'killed by user', got %q", tasks[0].Result.String)
	}

	agents, _ := testDB.ListAgents(ctx)
	if len(agents) != 1 {
		t.Fatalf("expected 1 agent, got %d", len(agents))
	}
	if agents[0].Status != "dead" {
		t.Errorf("expected agent status 'dead', got %q", agents[0].Status)
	}
}

func TestKillTaskNoAgent(t *testing.T) {
	testDB := setupTestDB(t)
	ctx := context.Background()

	err := testDB.CreateTask(ctx, db.Task{
		ID:     "t1",
		Title:  "Unassigned task",
		Status: "running",
		Role:   "implementer",
	})
	if err != nil {
		t.Fatalf("creating task: %v", err)
	}

	d := New(testDB)
	result := d.KillTask(ctx, "t1")
	if result.Err != nil {
		t.Fatalf("unexpected error: %v", result.Err)
	}

	tasks, _ := testDB.ListTasks(ctx)
	if tasks[0].Status != "failed" {
		t.Errorf("expected status 'failed', got %q", tasks[0].Status)
	}
}

func TestKillTaskNoDB(t *testing.T) {
	d := &Delegate{}
	result := d.KillTask(context.Background(), "t1")
	if result.Err == nil {
		t.Error("expected error when DB not configured")
	}
}

func TestSpawnTaskNoSpawner(t *testing.T) {
	testDB := setupTestDB(t)
	d := New(testDB)

	result := d.SpawnTask(context.Background(), "t1", "implementer")
	if result.Err == nil {
		t.Error("expected error when spawner not configured")
	}
}

func TestRespawnTaskNoSpawner(t *testing.T) {
	testDB := setupTestDB(t)
	d := New(testDB)

	result := d.RespawnTask(context.Background(), "t1")
	if result.Err == nil {
		t.Error("expected error when spawner not configured")
	}
}

func TestOrchestraGoNoConductor(t *testing.T) {
	testDB := setupTestDB(t)
	d := New(testDB)

	result := d.OrchestraGo(context.Background(), "test goal")
	if result.Err == nil {
		t.Error("expected error when conductor not configured")
	}
}
