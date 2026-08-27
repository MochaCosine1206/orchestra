package daemon

import (
	"context"
	"testing"
	"time"

	"github.com/MochaCosine1206/orchestra/internal/db"
)

func setupDaemonDB(t *testing.T) *db.DB {
	t.Helper()
	d, err := db.OpenMemory()
	if err != nil {
		t.Fatalf("open memory db: %v", err)
	}
	t.Cleanup(func() { d.Close() })

	// Apply daemon schema tables needed for preemption.
	_, err = d.ExecContext(context.Background(), `
		CREATE TABLE IF NOT EXISTS run_history (
			id TEXT PRIMARY KEY,
			project_path TEXT NOT NULL,
			session_id TEXT NOT NULL,
			started_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			finished_at DATETIME,
			status TEXT NOT NULL DEFAULT 'running',
			tasks_done INTEGER NOT NULL DEFAULT 0,
			tasks_failed INTEGER NOT NULL DEFAULT 0
		);
		CREATE TABLE IF NOT EXISTS priority_queue (
			id TEXT PRIMARY KEY,
			project_path TEXT NOT NULL,
			task_id TEXT NOT NULL,
			effective_priority REAL NOT NULL DEFAULT 0,
			computed_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			execution_mode TEXT DEFAULT 'conduct'
		);
	`)
	if err != nil {
		t.Fatalf("create schema: %v", err)
	}
	return d
}

func TestShouldPreempt_SufficientGapAndDuration(t *testing.T) {
	now := time.Now()
	running := PreemptionCandidate{
		TaskID:            "task-1",
		EffectivePriority: 50,
		StartedAt:         now.Add(-10 * time.Minute),
	}
	// Gap = 75 - 50 = 25 >= 20, duration = 10min >= 5min
	if !ShouldPreempt(75, running, now) {
		t.Error("expected preemption with gap=25, duration=10m")
	}
}

func TestShouldPreempt_InsufficientGap(t *testing.T) {
	now := time.Now()
	running := PreemptionCandidate{
		TaskID:            "task-1",
		EffectivePriority: 50,
		StartedAt:         now.Add(-10 * time.Minute),
	}
	// Gap = 65 - 50 = 15 < 20
	if ShouldPreempt(65, running, now) {
		t.Error("should not preempt with gap=15")
	}
}

func TestShouldPreempt_ExactGapThreshold(t *testing.T) {
	now := time.Now()
	running := PreemptionCandidate{
		TaskID:            "task-1",
		EffectivePriority: 50,
		StartedAt:         now.Add(-6 * time.Minute),
	}
	// Gap = 70 - 50 = 20 (exactly threshold)
	if !ShouldPreempt(70, running, now) {
		t.Error("expected preemption with gap=20 (at threshold)")
	}
}

func TestShouldPreempt_InsufficientRunDuration(t *testing.T) {
	now := time.Now()
	running := PreemptionCandidate{
		TaskID:            "task-1",
		EffectivePriority: 50,
		StartedAt:         now.Add(-3 * time.Minute),
	}
	// Gap = 80 - 50 = 30 >= 20, but duration = 3min < 5min
	if ShouldPreempt(80, running, now) {
		t.Error("should not preempt with duration=3m (< 5m minimum)")
	}
}

func TestShouldPreempt_ExactDurationThreshold(t *testing.T) {
	now := time.Now()
	running := PreemptionCandidate{
		TaskID:            "task-1",
		EffectivePriority: 50,
		StartedAt:         now.Add(-5 * time.Minute),
	}
	// Gap = 75 - 50 = 25 >= 20, duration exactly 5min
	if !ShouldPreempt(75, running, now) {
		t.Error("expected preemption with duration=5m (at threshold)")
	}
}

func TestAntiThrashing_AllowsPreemption(t *testing.T) {
	d := setupDaemonDB(t)
	ctx := context.Background()

	// No preemption records yet — should allow.
	allowed, err := AntiThrashing(ctx, d, "task-1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !allowed {
		t.Error("expected preemption to be allowed with 0 prior preemptions")
	}
}

func TestAntiThrashing_BlocksAfterMaxPreemptions(t *testing.T) {
	d := setupDaemonDB(t)
	ctx := context.Background()

	// Insert 3 preemption records for task-1.
	for i := 0; i < MaxPreemptions; i++ {
		id := db.GenID("run")
		_, err := d.ExecContext(ctx,
			`INSERT INTO run_history (id, project_path, session_id, status) VALUES (?, 'proj', ?, 'preempted')`,
			id, "task-1")
		if err != nil {
			t.Fatalf("inserting preemption record: %v", err)
		}
	}

	allowed, err := AntiThrashing(ctx, d, "task-1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if allowed {
		t.Error("expected preemption to be blocked after 3 preemptions")
	}
}

func TestAntiThrashing_AllowsUpToThreshold(t *testing.T) {
	d := setupDaemonDB(t)
	ctx := context.Background()

	// Insert 2 preemption records (below threshold).
	for i := 0; i < MaxPreemptions-1; i++ {
		id := db.GenID("run")
		_, err := d.ExecContext(ctx,
			`INSERT INTO run_history (id, project_path, session_id, status) VALUES (?, 'proj', ?, 'preempted')`,
			id, "task-1")
		if err != nil {
			t.Fatalf("inserting record: %v", err)
		}
	}

	allowed, err := AntiThrashing(ctx, d, "task-1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !allowed {
		t.Error("expected preemption to be allowed with 2 prior preemptions (< 3)")
	}
}

func TestAntiThrashing_IndependentPerTask(t *testing.T) {
	d := setupDaemonDB(t)
	ctx := context.Background()

	// Max out task-1 preemptions.
	for i := 0; i < MaxPreemptions; i++ {
		id := db.GenID("run")
		_, err := d.ExecContext(ctx,
			`INSERT INTO run_history (id, project_path, session_id, status) VALUES (?, 'proj', ?, 'preempted')`,
			id, "task-1")
		if err != nil {
			t.Fatalf("inserting record: %v", err)
		}
	}

	// task-2 should still be preemptable.
	allowed, err := AntiThrashing(ctx, d, "task-2")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !allowed {
		t.Error("task-2 preemption should be independent of task-1")
	}
}

func TestRecordPreemption_WritesHistoryAndBoost(t *testing.T) {
	d := setupDaemonDB(t)
	ctx := context.Background()

	// Seed priority_queue with a task entry.
	_, err := d.ExecContext(ctx,
		`INSERT INTO priority_queue (id, project_path, task_id, effective_priority)
		 VALUES ('pq-1', 'proj', 'task-1', 50.0)`)
	if err != nil {
		t.Fatalf("seeding priority_queue: %v", err)
	}

	if err := RecordPreemption(ctx, d, "task-1", "proj"); err != nil {
		t.Fatalf("RecordPreemption: %v", err)
	}

	// Verify run_history record.
	var count int
	err = d.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM run_history WHERE session_id = 'task-1' AND status = 'preempted'`).Scan(&count)
	if err != nil {
		t.Fatalf("querying run_history: %v", err)
	}
	if count != 1 {
		t.Errorf("expected 1 preemption record, got %d", count)
	}

	// Verify priority boost.
	var priority float64
	err = d.QueryRowContext(ctx,
		`SELECT effective_priority FROM priority_queue WHERE task_id = 'task-1'`).Scan(&priority)
	if err != nil {
		t.Fatalf("querying priority_queue: %v", err)
	}
	expected := 50.0 + float64(PreemptionBoostValue)
	if priority != expected {
		t.Errorf("expected priority %.1f, got %.1f", expected, priority)
	}
}

func TestRecordPreemption_AccumulatesBoost(t *testing.T) {
	d := setupDaemonDB(t)
	ctx := context.Background()

	_, err := d.ExecContext(ctx,
		`INSERT INTO priority_queue (id, project_path, task_id, effective_priority)
		 VALUES ('pq-1', 'proj', 'task-1', 50.0)`)
	if err != nil {
		t.Fatalf("seeding priority_queue: %v", err)
	}

	// Preempt twice.
	for i := 0; i < 2; i++ {
		if err := RecordPreemption(ctx, d, "task-1", "proj"); err != nil {
			t.Fatalf("RecordPreemption round %d: %v", i, err)
		}
	}

	var priority float64
	err = d.QueryRowContext(ctx,
		`SELECT effective_priority FROM priority_queue WHERE task_id = 'task-1'`).Scan(&priority)
	if err != nil {
		t.Fatalf("querying priority_queue: %v", err)
	}
	expected := 50.0 + 2*float64(PreemptionBoostValue)
	if priority != expected {
		t.Errorf("expected priority %.1f after 2 preemptions, got %.1f", expected, priority)
	}

	// Verify 2 history records.
	var count int
	err = d.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM run_history WHERE session_id = 'task-1' AND status = 'preempted'`).Scan(&count)
	if err != nil {
		t.Fatalf("querying run_history: %v", err)
	}
	if count != 2 {
		t.Errorf("expected 2 preemption records, got %d", count)
	}
}

func TestEvaluatePreemption_InsufficientGap(t *testing.T) {
	d := setupDaemonDB(t)
	ctx := context.Background()
	now := time.Now()

	running := PreemptionCandidate{
		TaskID:            "task-1",
		PID:               99999,
		EffectivePriority: 50,
		StartedAt:         now.Add(-10 * time.Minute),
	}

	result, err := EvaluatePreemption(ctx, d, 60, running, "proj", now)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Preempted {
		t.Error("should not preempt with gap=10")
	}
	if result.AntiThrashed {
		t.Error("should not be anti-thrashed")
	}
}

func TestEvaluatePreemption_AntiThrashingBlocks(t *testing.T) {
	d := setupDaemonDB(t)
	ctx := context.Background()
	now := time.Now()

	// Max out preemption count.
	for i := 0; i < MaxPreemptions; i++ {
		id := db.GenID("run")
		_, err := d.ExecContext(ctx,
			`INSERT INTO run_history (id, project_path, session_id, status) VALUES (?, 'proj', ?, 'preempted')`,
			id, "task-1")
		if err != nil {
			t.Fatalf("seeding: %v", err)
		}
	}

	running := PreemptionCandidate{
		TaskID:            "task-1",
		PID:               99999,
		EffectivePriority: 50,
		StartedAt:         now.Add(-10 * time.Minute),
	}

	result, err := EvaluatePreemption(ctx, d, 80, running, "proj", now)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Preempted {
		t.Error("should not preempt when anti-thrashed")
	}
	if !result.AntiThrashed {
		t.Error("expected AntiThrashed=true")
	}
}
