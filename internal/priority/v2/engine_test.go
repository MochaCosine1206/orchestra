package priority

import (
	"context"
	"fmt"
	"testing"

	"github.com/MochaCosine1206/orchestra/internal/db"
)

// testCollector is a mock collector for testing.
type testCollector struct {
	source Source
	items  []WorkItem
	err    error
}

func (c *testCollector) Name() Source                                   { return c.source }
func (c *testCollector) Collect(_ context.Context) ([]WorkItem, error) { return c.items, c.err }

func setupTestDB(t *testing.T) *db.DB {
	t.Helper()
	database, err := db.OpenMemory()
	if err != nil {
		t.Fatalf("opening in-memory db: %v", err)
	}
	t.Cleanup(func() { database.Close() })

	ctx := context.Background()
	// Create work_items table
	_, err = database.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS work_items (
			id TEXT PRIMARY KEY,
			source TEXT NOT NULL,
			source_id TEXT,
			source_repo TEXT,
			title TEXT NOT NULL,
			description TEXT NOT NULL DEFAULT '',
			tier INTEGER NOT NULL DEFAULT 4,
			base_priority INTEGER NOT NULL DEFAULT 50,
			feasibility REAL DEFAULT NULL,
			impact REAL DEFAULT NULL,
			uniqueness REAL DEFAULT NULL,
			deadline DATETIME DEFAULT NULL,
			deadline_type TEXT DEFAULT NULL,
			effort_hours REAL DEFAULT NULL,
			effort_confidence REAL DEFAULT NULL,
			blocked_by TEXT DEFAULT NULL,
			blocks TEXT DEFAULT NULL,
			user_picked INTEGER NOT NULL DEFAULT 0,
			user_picked_at DATETIME DEFAULT NULL,
			user_priority_rank INTEGER DEFAULT NULL,
			market_signal_ids TEXT DEFAULT NULL,
			competitor_shipped INTEGER NOT NULL DEFAULT 0,
			target_repo TEXT DEFAULT NULL,
			is_new_project INTEGER NOT NULL DEFAULT 0,
			agent_roles TEXT DEFAULT NULL,
			goal_alignment REAL DEFAULT NULL,
			effective_priority REAL NOT NULL DEFAULT 0,
			priority_explanation TEXT DEFAULT NULL,
			license_type TEXT DEFAULT 'open-source',
			license_reason TEXT DEFAULT '',
			status TEXT NOT NULL DEFAULT 'pending',
			retry_count INTEGER NOT NULL DEFAULT 0,
			last_failure_reason TEXT DEFAULT NULL,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			updated_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			scored_at DATETIME DEFAULT NULL,
			stale_after DATETIME DEFAULT NULL,
			last_validated_at DATETIME DEFAULT NULL
		)`)
	if err != nil {
		t.Fatalf("creating work_items: %v", err)
	}

	// Unique index for upsert dedup
	_, err = database.ExecContext(ctx, `
		CREATE UNIQUE INDEX IF NOT EXISTS idx_work_items_source_id ON work_items(source, source_id)`)
	if err != nil {
		t.Fatalf("creating source_id index: %v", err)
	}

	// Create priority_history table
	_, err = database.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS priority_history (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			work_item_id TEXT NOT NULL REFERENCES work_items(id),
			old_priority REAL,
			new_priority REAL NOT NULL,
			reason TEXT NOT NULL,
			scored_at DATETIME DEFAULT CURRENT_TIMESTAMP
		)`)
	if err != nil {
		t.Fatalf("creating priority_history: %v", err)
	}

	// Create priority_queue table (v1 bridge)
	_, err = database.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS priority_queue (
			id TEXT PRIMARY KEY,
			project_path TEXT NOT NULL,
			task_id TEXT NOT NULL,
			effective_priority REAL NOT NULL DEFAULT 0,
			computed_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			execution_mode TEXT DEFAULT 'conduct'
		)`)
	if err != nil {
		t.Fatalf("creating priority_queue: %v", err)
	}

	return database
}

func TestCollectAll(t *testing.T) {
	t.Run("collects from multiple sources", func(t *testing.T) {
		database := setupTestDB(t)
		ctx := context.Background()

		collectors := []Collector{
			&testCollector{
				source: SourceBacklog,
				items: []WorkItem{
					{Source: SourceBacklog, SourceID: "B-001", Title: "Fix bug", Tier: TierTechDebt, Status: StatusPending},
					{Source: SourceBacklog, SourceID: "B-002", Title: "Add feature", Tier: TierGoalImpl, Status: StatusPending},
				},
			},
			&testCollector{
				source: SourceUser,
				items: []WorkItem{
					{Source: SourceUser, SourceID: "user-hitl", Title: "Finish HITL", Tier: TierUser, Status: StatusPending},
				},
			},
		}

		engine := NewScoringEngine(database, nil, collectors)
		count, err := engine.CollectAll(ctx)
		if err != nil {
			t.Fatalf("CollectAll: %v", err)
		}
		if count != 3 {
			t.Errorf("expected 3 items, got %d", count)
		}

		var dbCount int
		database.QueryRowContext(ctx, `SELECT COUNT(*) FROM work_items`).Scan(&dbCount)
		if dbCount != 3 {
			t.Errorf("expected 3 rows in DB, got %d", dbCount)
		}
	})

	t.Run("fail-open on collector error", func(t *testing.T) {
		database := setupTestDB(t)
		ctx := context.Background()

		collectors := []Collector{
			&testCollector{source: SourceBacklog, err: fmt.Errorf("backlog unavailable")},
			&testCollector{
				source: SourceUser,
				items:  []WorkItem{{Source: SourceUser, SourceID: "user-1", Title: "Task", Tier: TierUser, Status: StatusPending}},
			},
		}

		engine := NewScoringEngine(database, nil, collectors)
		count, err := engine.CollectAll(ctx)
		if err != nil {
			t.Fatalf("CollectAll should not error: %v", err)
		}
		if count != 1 {
			t.Errorf("expected 1 item (skipped failing collector), got %d", count)
		}
	})
}

func TestUpsertDedup(t *testing.T) {
	database := setupTestDB(t)
	ctx := context.Background()

	collectors := []Collector{
		&testCollector{
			source: SourceBacklog,
			items: []WorkItem{
				{Source: SourceBacklog, SourceID: "B-001", Title: "Original title", Tier: TierTechDebt, Status: StatusPending},
			},
		},
	}

	engine := NewScoringEngine(database, nil, collectors)

	// First collect
	count, _ := engine.CollectAll(ctx)
	if count != 1 {
		t.Fatalf("first collect: expected 1, got %d", count)
	}

	// Update the title and collect again
	collectors[0].(*testCollector).items[0].Title = "Updated title"
	count, _ = engine.CollectAll(ctx)
	if count != 1 {
		t.Fatalf("second collect: expected 1, got %d", count)
	}

	// Should still be 1 row, with updated title
	var dbCount int
	database.QueryRowContext(ctx, `SELECT COUNT(*) FROM work_items`).Scan(&dbCount)
	if dbCount != 1 {
		t.Errorf("expected 1 row after dedup, got %d", dbCount)
	}

	var title string
	database.QueryRowContext(ctx, `SELECT title FROM work_items WHERE source_id = 'B-001'`).Scan(&title)
	if title != "Updated title" {
		t.Errorf("expected 'Updated title', got %q", title)
	}
}

func TestScoreAll(t *testing.T) {
	database := setupTestDB(t)
	ctx := context.Background()

	collectors := []Collector{
		&testCollector{
			source: SourceBacklog,
			items: []WorkItem{
				{Source: SourceBacklog, SourceID: "B-010", Title: "High tier", Tier: TierUser, Status: StatusPending},
				{Source: SourceBacklog, SourceID: "B-020", Title: "Low tier", Tier: TierExploratory, Status: StatusPending},
			},
		},
	}

	engine := NewScoringEngine(database, nil, collectors)
	engine.CollectAll(ctx)

	scored, err := engine.ScoreAll(ctx)
	if err != nil {
		t.Fatalf("ScoreAll: %v", err)
	}
	if scored != 2 {
		t.Errorf("expected 2 scored items, got %d", scored)
	}

	// Verify scores are different and user tier > exploratory
	var highScore, lowScore float64
	database.QueryRowContext(ctx, `SELECT effective_priority FROM work_items WHERE source_id = 'B-010'`).Scan(&highScore)
	database.QueryRowContext(ctx, `SELECT effective_priority FROM work_items WHERE source_id = 'B-020'`).Scan(&lowScore)

	if highScore <= lowScore {
		t.Errorf("user tier (%.0f) should score higher than exploratory (%.0f)", highScore, lowScore)
	}
	if highScore == 0 {
		t.Error("high tier item should have non-zero score")
	}

	// Verify priority_history was recorded
	var historyCount int
	database.QueryRowContext(ctx, `SELECT COUNT(*) FROM priority_history`).Scan(&historyCount)
	if historyCount != 2 {
		t.Errorf("expected 2 priority_history records, got %d", historyCount)
	}
}

func TestScoreAllBlockedDependency(t *testing.T) {
	database := setupTestDB(t)
	ctx := context.Background()

	// Insert a blocker that is NOT completed
	database.ExecContext(ctx, `
		INSERT INTO work_items (id, source, source_id, title, tier, status)
		VALUES ('blocker-1', 'backlog', 'B-BLOCK', 'Blocker', 4, 'pending')`)

	collectors := []Collector{
		&testCollector{
			source: SourceBacklog,
			items: []WorkItem{
				{
					Source:    SourceBacklog,
					SourceID:  "B-BLOCKED",
					Title:     "Blocked item",
					Tier:      TierGoalImpl,
					Status:    StatusPending,
					BlockedBy: []string{"blocker-1"},
				},
			},
		},
	}

	engine := NewScoringEngine(database, nil, collectors)
	engine.CollectAll(ctx)
	engine.ScoreAll(ctx)

	var status string
	database.QueryRowContext(ctx, `SELECT status FROM work_items WHERE source_id = 'B-BLOCKED'`).Scan(&status)
	if status != StatusBlocked {
		t.Errorf("expected status 'blocked', got %q", status)
	}
}

func TestPopulateQueue(t *testing.T) {
	database := setupTestDB(t)
	ctx := context.Background()

	collectors := []Collector{
		&testCollector{
			source: SourceBacklog,
			items: []WorkItem{
				{Source: SourceBacklog, SourceID: "B-100", Title: "Task A", Tier: TierUser, SourceRepo: "/repos/alpha", Status: StatusPending},
				{Source: SourceBacklog, SourceID: "B-200", Title: "Task B", Tier: TierGoalImpl, SourceRepo: "/repos/beta", Status: StatusPending},
				{Source: SourceBacklog, SourceID: "B-300", Title: "Task C", Tier: TierExploratory, SourceRepo: "/repos/gamma", Status: StatusPending},
			},
		},
	}

	engine := NewScoringEngine(database, nil, collectors)
	engine.CollectAll(ctx)
	engine.ScoreAll(ctx)

	err := engine.PopulateQueue(ctx, 2, []string{"/repos/alpha", "/repos/beta", "/repos/gamma"})
	if err != nil {
		t.Fatalf("PopulateQueue: %v", err)
	}

	// Should have exactly 2 items in priority_queue
	var queueCount int
	database.QueryRowContext(ctx, `SELECT COUNT(*) FROM priority_queue`).Scan(&queueCount)
	if queueCount != 2 {
		t.Errorf("expected 2 items in priority_queue, got %d", queueCount)
	}

	// Queued items should have status=queued in work_items
	var queuedCount int
	database.QueryRowContext(ctx, `SELECT COUNT(*) FROM work_items WHERE status = 'queued'`).Scan(&queuedCount)
	if queuedCount != 2 {
		t.Errorf("expected 2 queued work items, got %d", queuedCount)
	}

	// Second call should clear and repopulate
	err = engine.PopulateQueue(ctx, 1, nil)
	if err != nil {
		t.Fatalf("PopulateQueue second call: %v", err)
	}

	database.QueryRowContext(ctx, `SELECT COUNT(*) FROM priority_queue`).Scan(&queueCount)
	if queueCount != 1 {
		t.Errorf("expected 1 item after re-populate, got %d", queueCount)
	}
}

func TestTopN(t *testing.T) {
	database := setupTestDB(t)
	ctx := context.Background()

	// Insert items with known scores
	for i := 1; i <= 5; i++ {
		database.ExecContext(ctx, `
			INSERT INTO work_items (id, source, source_id, title, tier, effective_priority, status)
			VALUES (?, 'backlog', ?, ?, 4, ?, 'pending')`,
			fmt.Sprintf("wi-%d", i),
			fmt.Sprintf("B-%03d", i),
			fmt.Sprintf("Task %d", i),
			float64(i*100),
		)
	}

	engine := NewScoringEngine(database, nil, nil)
	items, err := engine.TopN(ctx, 3)
	if err != nil {
		t.Fatalf("TopN: %v", err)
	}
	if len(items) != 3 {
		t.Fatalf("expected 3 items, got %d", len(items))
	}

	// Verify descending order
	if items[0].EffectivePriority < items[1].EffectivePriority {
		t.Error("items should be in descending priority order")
	}
	if items[0].EffectivePriority != 500 {
		t.Errorf("top item should have priority 500, got %.0f", items[0].EffectivePriority)
	}
}
