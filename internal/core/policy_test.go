package core

import (
	"context"
	"testing"

	"github.com/MochaCosine1206/orchestra/internal/db"
)

func openTestDaemonDB(t *testing.T) *db.DB {
	t.Helper()
	d, err := db.OpenMemory()
	if err != nil {
		t.Fatalf("open memory db: %v", err)
	}
	t.Cleanup(func() { d.Close() })
	if err := InitDaemonSchema(context.Background(), d); err != nil {
		t.Fatalf("init daemon schema: %v", err)
	}
	return d
}

func TestSelectNext_HighestPriority(t *testing.T) {
	d := openTestDaemonDB(t)
	ctx := context.Background()

	d.ExecContext(ctx, `INSERT INTO priority_queue (id, project_path, task_id, effective_priority) VALUES ('a', '/proj/alpha', 't1', 50)`)
	d.ExecContext(ctx, `INSERT INTO priority_queue (id, project_path, task_id, effective_priority) VALUES ('b', '/proj/beta', 't2', 100)`)
	d.ExecContext(ctx, `INSERT INTO priority_queue (id, project_path, task_id, effective_priority) VALUES ('c', '/proj/gamma', 't3', 75)`)

	s := &Scheduler{DB: d, MaxConcurrent: 5}
	entry, err := s.SelectNext(ctx)
	if err != nil {
		t.Fatalf("SelectNext: %v", err)
	}
	if entry == nil {
		t.Fatal("expected entry, got nil")
	}
	if entry.ID != "b" {
		t.Errorf("expected id 'b' (priority 100), got %q", entry.ID)
	}
	if entry.EffectivePriority != 100 {
		t.Errorf("expected priority 100, got %f", entry.EffectivePriority)
	}
}

func TestSelectNext_EmptyQueue(t *testing.T) {
	d := openTestDaemonDB(t)
	ctx := context.Background()

	s := &Scheduler{DB: d, MaxConcurrent: 5}
	entry, err := s.SelectNext(ctx)
	if err != nil {
		t.Fatalf("SelectNext: %v", err)
	}
	if entry != nil {
		t.Errorf("expected nil for empty queue, got %+v", entry)
	}
}

func TestSelectNext_MaxConcurrentBlocks(t *testing.T) {
	d := openTestDaemonDB(t)
	ctx := context.Background()

	d.ExecContext(ctx, `INSERT INTO priority_queue (id, project_path, task_id, effective_priority) VALUES ('a', '/proj/alpha', 't1', 50)`)
	// Insert 2 running sessions
	d.ExecContext(ctx, `INSERT INTO run_history (id, project_path, session_id, status) VALUES ('r1', '/proj/alpha', 's1', 'running')`)
	d.ExecContext(ctx, `INSERT INTO run_history (id, project_path, session_id, status) VALUES ('r2', '/proj/beta', 's2', 'running')`)

	s := &Scheduler{DB: d, MaxConcurrent: 2}
	entry, err := s.SelectNext(ctx)
	if err != nil {
		t.Fatalf("SelectNext: %v", err)
	}
	if entry != nil {
		t.Errorf("expected nil when at max concurrent, got %+v", entry)
	}
}

func TestSelectNext_ZeroMaxConcurrentNoLimit(t *testing.T) {
	d := openTestDaemonDB(t)
	ctx := context.Background()

	d.ExecContext(ctx, `INSERT INTO priority_queue (id, project_path, task_id, effective_priority) VALUES ('a', '/proj/alpha', 't1', 50)`)
	d.ExecContext(ctx, `INSERT INTO run_history (id, project_path, session_id, status) VALUES ('r1', '/proj/alpha', 's1', 'running')`)

	s := &Scheduler{DB: d, MaxConcurrent: 0}
	entry, err := s.SelectNext(ctx)
	if err != nil {
		t.Fatalf("SelectNext: %v", err)
	}
	if entry == nil {
		t.Fatal("expected entry with no max_concurrent limit, got nil")
	}
}

func TestWeightedFairSelect_SingleProject(t *testing.T) {
	d := openTestDaemonDB(t)
	ctx := context.Background()

	d.ExecContext(ctx, `INSERT INTO priority_queue (id, project_path, task_id, effective_priority) VALUES ('a', '/proj/alpha', 't1', 50)`)

	s := &Scheduler{DB: d}
	entry, err := s.WeightedFairSelect(ctx, nil)
	if err != nil {
		t.Fatalf("WeightedFairSelect: %v", err)
	}
	if entry == nil {
		t.Fatal("expected entry, got nil")
	}
	if entry.ProjectPath != "/proj/alpha" {
		t.Errorf("expected /proj/alpha, got %q", entry.ProjectPath)
	}
}

func TestWeightedFairSelect_EmptyQueue(t *testing.T) {
	d := openTestDaemonDB(t)
	ctx := context.Background()

	s := &Scheduler{DB: d}
	entry, err := s.WeightedFairSelect(ctx, nil)
	if err != nil {
		t.Fatalf("WeightedFairSelect: %v", err)
	}
	if entry != nil {
		t.Errorf("expected nil for empty queue, got %+v", entry)
	}
}

func TestWeightedFairSelect_WeightDistribution(t *testing.T) {
	d := openTestDaemonDB(t)
	ctx := context.Background()

	// Insert entries for two projects
	d.ExecContext(ctx, `INSERT INTO priority_queue (id, project_path, task_id, effective_priority) VALUES ('a', '/proj/alpha', 't1', 50)`)
	d.ExecContext(ctx, `INSERT INTO priority_queue (id, project_path, task_id, effective_priority) VALUES ('b', '/proj/beta', 't2', 50)`)

	weights := map[string]float64{
		"/proj/alpha": 9.0,
		"/proj/beta":  1.0,
	}

	s := &Scheduler{DB: d}
	counts := map[string]int{}
	iterations := 1000
	for i := 0; i < iterations; i++ {
		entry, err := s.WeightedFairSelect(ctx, weights)
		if err != nil {
			t.Fatalf("WeightedFairSelect: %v", err)
		}
		if entry != nil {
			counts[entry.ProjectPath]++
		}
	}

	alphaRatio := float64(counts["/proj/alpha"]) / float64(iterations)
	// With 9:1 weights, alpha should get ~90%. Allow wide tolerance for randomness.
	if alphaRatio < 0.7 || alphaRatio > 0.99 {
		t.Errorf("expected alpha ~90%%, got %.1f%% (alpha=%d, beta=%d)",
			alphaRatio*100, counts["/proj/alpha"], counts["/proj/beta"])
	}
}

func TestWeightedFairSelect_MaxConcurrentBlocks(t *testing.T) {
	d := openTestDaemonDB(t)
	ctx := context.Background()

	d.ExecContext(ctx, `INSERT INTO priority_queue (id, project_path, task_id, effective_priority) VALUES ('a', '/proj/alpha', 't1', 50)`)
	d.ExecContext(ctx, `INSERT INTO run_history (id, project_path, session_id, status) VALUES ('r1', '/proj/alpha', 's1', 'running')`)

	s := &Scheduler{DB: d, MaxConcurrent: 1}
	entry, err := s.WeightedFairSelect(ctx, nil)
	if err != nil {
		t.Fatalf("WeightedFairSelect: %v", err)
	}
	if entry != nil {
		t.Errorf("expected nil when at max concurrent, got %+v", entry)
	}
}

func TestWeightedFairSelect_HighestPriorityWithinProject(t *testing.T) {
	d := openTestDaemonDB(t)
	ctx := context.Background()

	// Two entries for same project, different priorities
	d.ExecContext(ctx, `INSERT INTO priority_queue (id, project_path, task_id, effective_priority) VALUES ('a', '/proj/alpha', 't1', 30)`)
	d.ExecContext(ctx, `INSERT INTO priority_queue (id, project_path, task_id, effective_priority) VALUES ('b', '/proj/alpha', 't2', 80)`)

	s := &Scheduler{DB: d}
	entry, err := s.WeightedFairSelect(ctx, nil)
	if err != nil {
		t.Fatalf("WeightedFairSelect: %v", err)
	}
	if entry == nil {
		t.Fatal("expected entry, got nil")
	}
	if entry.ID != "b" {
		t.Errorf("expected highest priority entry 'b', got %q", entry.ID)
	}
}
