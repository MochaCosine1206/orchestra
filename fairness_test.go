package orchestra

import (
	"context"
	"testing"
	"time"
)

func TestMinimumAttentionBoost_NoRunsToday(t *testing.T) {
	d := openTestDaemonDB(t)
	ctx := context.Background()
	now := time.Date(2026, 3, 25, 14, 0, 0, 0, time.UTC)

	d.ExecContext(ctx, `INSERT INTO priority_queue (id, project_path, task_id, effective_priority) VALUES ('a', '/proj/alpha', 't1', 50)`)
	d.ExecContext(ctx, `INSERT INTO priority_queue (id, project_path, task_id, effective_priority) VALUES ('b', '/proj/beta', 't2', 30)`)

	f := &FairnessEngine{DB: d}
	n, err := f.MinimumAttentionBoost(ctx, now)
	if err != nil {
		t.Fatalf("MinimumAttentionBoost: %v", err)
	}
	if n != 2 {
		t.Errorf("expected 2 boosted, got %d", n)
	}

	// Verify priorities increased
	var prio float64
	d.QueryRowContext(ctx, `SELECT effective_priority FROM priority_queue WHERE id = 'a'`).Scan(&prio)
	if prio != 150 {
		t.Errorf("expected 150 for alpha, got %f", prio)
	}
	d.QueryRowContext(ctx, `SELECT effective_priority FROM priority_queue WHERE id = 'b'`).Scan(&prio)
	if prio != 130 {
		t.Errorf("expected 130 for beta, got %f", prio)
	}
}

func TestMinimumAttentionBoost_WithRunToday(t *testing.T) {
	d := openTestDaemonDB(t)
	ctx := context.Background()
	now := time.Date(2026, 3, 25, 14, 0, 0, 0, time.UTC)

	d.ExecContext(ctx, `INSERT INTO priority_queue (id, project_path, task_id, effective_priority) VALUES ('a', '/proj/alpha', 't1', 50)`)
	d.ExecContext(ctx, `INSERT INTO priority_queue (id, project_path, task_id, effective_priority) VALUES ('b', '/proj/beta', 't2', 30)`)
	// Alpha has a run today
	d.ExecContext(ctx, `INSERT INTO run_history (id, project_path, session_id, started_at, status) VALUES ('r1', '/proj/alpha', 's1', '2026-03-25 10:00:00', 'done')`)

	f := &FairnessEngine{DB: d}
	n, err := f.MinimumAttentionBoost(ctx, now)
	if err != nil {
		t.Fatalf("MinimumAttentionBoost: %v", err)
	}
	if n != 1 {
		t.Errorf("expected 1 boosted (only beta), got %d", n)
	}

	var prio float64
	d.QueryRowContext(ctx, `SELECT effective_priority FROM priority_queue WHERE id = 'a'`).Scan(&prio)
	if prio != 50 {
		t.Errorf("expected alpha unchanged at 50, got %f", prio)
	}
	d.QueryRowContext(ctx, `SELECT effective_priority FROM priority_queue WHERE id = 'b'`).Scan(&prio)
	if prio != 130 {
		t.Errorf("expected beta boosted to 130, got %f", prio)
	}
}

func TestMinimumAttentionBoost_EmptyQueue(t *testing.T) {
	d := openTestDaemonDB(t)
	ctx := context.Background()
	now := time.Date(2026, 3, 25, 14, 0, 0, 0, time.UTC)

	f := &FairnessEngine{DB: d}
	n, err := f.MinimumAttentionBoost(ctx, now)
	if err != nil {
		t.Fatalf("MinimumAttentionBoost: %v", err)
	}
	if n != 0 {
		t.Errorf("expected 0 boosted for empty queue, got %d", n)
	}
}

func TestDetectStarvation_NeverRun(t *testing.T) {
	d := openTestDaemonDB(t)
	ctx := context.Background()
	now := time.Date(2026, 3, 25, 14, 0, 0, 0, time.UTC)

	d.ExecContext(ctx, `INSERT INTO priority_queue (id, project_path, task_id, effective_priority) VALUES ('a', '/proj/alpha', 't1', 50)`)

	f := &FairnessEngine{DB: d}
	starved, err := f.DetectStarvation(ctx, now)
	if err != nil {
		t.Fatalf("DetectStarvation: %v", err)
	}
	if len(starved) != 1 {
		t.Fatalf("expected 1 starved, got %d", len(starved))
	}
	if starved[0].ProjectPath != "/proj/alpha" {
		t.Errorf("expected /proj/alpha, got %q", starved[0].ProjectPath)
	}
	if starved[0].HoursSince != -1 {
		t.Errorf("expected -1 (never run), got %f", starved[0].HoursSince)
	}
}

func TestDetectStarvation_OldRun(t *testing.T) {
	d := openTestDaemonDB(t)
	ctx := context.Background()
	now := time.Date(2026, 3, 25, 14, 0, 0, 0, time.UTC)

	d.ExecContext(ctx, `INSERT INTO priority_queue (id, project_path, task_id, effective_priority) VALUES ('a', '/proj/alpha', 't1', 50)`)
	// Last run was 30 hours ago
	d.ExecContext(ctx, `INSERT INTO run_history (id, project_path, session_id, started_at, status) VALUES ('r1', '/proj/alpha', 's1', '2026-03-24 08:00:00', 'done')`)

	f := &FairnessEngine{DB: d}
	starved, err := f.DetectStarvation(ctx, now)
	if err != nil {
		t.Fatalf("DetectStarvation: %v", err)
	}
	if len(starved) != 1 {
		t.Fatalf("expected 1 starved, got %d", len(starved))
	}
	if starved[0].HoursSince < 29 || starved[0].HoursSince > 31 {
		t.Errorf("expected ~30 hours since, got %f", starved[0].HoursSince)
	}
}

func TestDetectStarvation_RecentRunNotStarved(t *testing.T) {
	d := openTestDaemonDB(t)
	ctx := context.Background()
	now := time.Date(2026, 3, 25, 14, 0, 0, 0, time.UTC)

	d.ExecContext(ctx, `INSERT INTO priority_queue (id, project_path, task_id, effective_priority) VALUES ('a', '/proj/alpha', 't1', 50)`)
	// Last run was 2 hours ago
	d.ExecContext(ctx, `INSERT INTO run_history (id, project_path, session_id, started_at, status) VALUES ('r1', '/proj/alpha', 's1', '2026-03-25 12:00:00', 'done')`)

	f := &FairnessEngine{DB: d}
	starved, err := f.DetectStarvation(ctx, now)
	if err != nil {
		t.Fatalf("DetectStarvation: %v", err)
	}
	if len(starved) != 0 {
		t.Errorf("expected 0 starved (recent run), got %d", len(starved))
	}
}

func TestDetectStarvation_EmptyQueue(t *testing.T) {
	d := openTestDaemonDB(t)
	ctx := context.Background()
	now := time.Date(2026, 3, 25, 14, 0, 0, 0, time.UTC)

	f := &FairnessEngine{DB: d}
	starved, err := f.DetectStarvation(ctx, now)
	if err != nil {
		t.Fatalf("DetectStarvation: %v", err)
	}
	if len(starved) != 0 {
		t.Errorf("expected 0 for empty queue, got %d", len(starved))
	}
}

func TestStarvationBoost_AllStarved(t *testing.T) {
	d := openTestDaemonDB(t)
	ctx := context.Background()
	now := time.Date(2026, 3, 25, 14, 0, 0, 0, time.UTC)

	d.ExecContext(ctx, `INSERT INTO priority_queue (id, project_path, task_id, effective_priority) VALUES ('a', '/proj/alpha', 't1', 50)`)
	d.ExecContext(ctx, `INSERT INTO priority_queue (id, project_path, task_id, effective_priority) VALUES ('b', '/proj/beta', 't2', 30)`)

	f := &FairnessEngine{DB: d}
	n, err := f.StarvationBoost(ctx, now)
	if err != nil {
		t.Fatalf("StarvationBoost: %v", err)
	}
	if n != 2 {
		t.Errorf("expected 2 boosted, got %d", n)
	}

	var prio float64
	d.QueryRowContext(ctx, `SELECT effective_priority FROM priority_queue WHERE id = 'a'`).Scan(&prio)
	if prio != 250 {
		t.Errorf("expected 250, got %f", prio)
	}
	d.QueryRowContext(ctx, `SELECT effective_priority FROM priority_queue WHERE id = 'b'`).Scan(&prio)
	if prio != 230 {
		t.Errorf("expected 230, got %f", prio)
	}
}

func TestStarvationBoost_MixedStarvation(t *testing.T) {
	d := openTestDaemonDB(t)
	ctx := context.Background()
	now := time.Date(2026, 3, 25, 14, 0, 0, 0, time.UTC)

	d.ExecContext(ctx, `INSERT INTO priority_queue (id, project_path, task_id, effective_priority) VALUES ('a', '/proj/alpha', 't1', 50)`)
	d.ExecContext(ctx, `INSERT INTO priority_queue (id, project_path, task_id, effective_priority) VALUES ('b', '/proj/beta', 't2', 30)`)
	// Alpha ran recently, beta never ran
	d.ExecContext(ctx, `INSERT INTO run_history (id, project_path, session_id, started_at, status) VALUES ('r1', '/proj/alpha', 's1', '2026-03-25 12:00:00', 'done')`)

	f := &FairnessEngine{DB: d}
	n, err := f.StarvationBoost(ctx, now)
	if err != nil {
		t.Fatalf("StarvationBoost: %v", err)
	}
	if n != 1 {
		t.Errorf("expected 1 boosted (only beta), got %d", n)
	}

	var prio float64
	d.QueryRowContext(ctx, `SELECT effective_priority FROM priority_queue WHERE id = 'a'`).Scan(&prio)
	if prio != 50 {
		t.Errorf("expected alpha unchanged at 50, got %f", prio)
	}
	d.QueryRowContext(ctx, `SELECT effective_priority FROM priority_queue WHERE id = 'b'`).Scan(&prio)
	if prio != 230 {
		t.Errorf("expected beta boosted to 230, got %f", prio)
	}
}

func TestStarvationBoost_EmptyQueue(t *testing.T) {
	d := openTestDaemonDB(t)
	ctx := context.Background()
	now := time.Date(2026, 3, 25, 14, 0, 0, 0, time.UTC)

	f := &FairnessEngine{DB: d}
	n, err := f.StarvationBoost(ctx, now)
	if err != nil {
		t.Fatalf("StarvationBoost: %v", err)
	}
	if n != 0 {
		t.Errorf("expected 0 for empty queue, got %d", n)
	}
}
