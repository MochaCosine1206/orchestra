package daemon

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/MochaCosine1206/orchestra/internal/db"
)

// setupScannerTestDB creates an in-memory SQLite DB with the work_items table.
func setupScannerTestDB(t *testing.T) *db.DB {
	t.Helper()
	database, err := db.OpenMemory()
	if err != nil {
		t.Fatalf("opening in-memory db: %v", err)
	}
	t.Cleanup(func() { database.Close() })

	ctx := context.Background()
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

	_, err = database.ExecContext(ctx, `
		CREATE UNIQUE INDEX IF NOT EXISTS idx_work_items_source_id ON work_items(source, source_id)`)
	if err != nil {
		t.Fatalf("creating source_id index: %v", err)
	}

	return database
}

func newTestScanner(t *testing.T, database *db.DB) *DiscoveryScanner {
	t.Helper()
	tmpDir := t.TempDir()
	return NewDiscoveryScanner(
		database,
		slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn})),
		filepath.Join(tmpDir, "last-scan.json"),
	)
}

func TestScannerContentHash(t *testing.T) {
	t.Run("deterministic", func(t *testing.T) {
		h1 := ContentHash("hackernews", "Show HN: My cool project")
		h2 := ContentHash("hackernews", "Show HN: My cool project")
		if h1 != h2 {
			t.Errorf("ContentHash not deterministic: %s != %s", h1, h2)
		}
	})

	t.Run("different inputs produce different hashes", func(t *testing.T) {
		h1 := ContentHash("hackernews", "Title A")
		h2 := ContentHash("hackernews", "Title B")
		if h1 == h2 {
			t.Errorf("different inputs produced same hash: %s", h1)
		}
	})

	t.Run("source matters", func(t *testing.T) {
		h1 := ContentHash("source-a", "Same Title")
		h2 := ContentHash("source-b", "Same Title")
		if h1 == h2 {
			t.Errorf("different sources produced same hash: %s", h1)
		}
	})
}

func TestScannerFilterNew(t *testing.T) {
	database := setupScannerTestDB(t)
	scanner := newTestScanner(t, database)
	ctx := context.Background()

	// Insert an existing discovery into work_items.
	existingHash := ContentHash("test-source", "Existing Item")
	_, err := database.ExecContext(ctx, `
		INSERT INTO work_items (id, source, source_id, title, description, status)
		VALUES ('existing-1', 'discovery', ?, 'Existing Item', 'desc', 'pending')`,
		existingHash,
	)
	if err != nil {
		t.Fatalf("inserting existing item: %v", err)
	}

	discoveries := []Discovery{
		{
			Title:       "Existing Item",
			Source:      "test-source",
			ContentHash: existingHash,
		},
		{
			Title:       "New Item",
			Source:      "test-source",
			ContentHash: ContentHash("test-source", "New Item"),
		},
	}

	filtered, err := scanner.FilterNew(ctx, discoveries)
	if err != nil {
		t.Fatalf("FilterNew: %v", err)
	}

	if len(filtered) != 1 {
		t.Fatalf("expected 1 new discovery, got %d", len(filtered))
	}
	if filtered[0].Title != "New Item" {
		t.Errorf("expected 'New Item', got %q", filtered[0].Title)
	}
}

func TestScannerFilterNewEmpty(t *testing.T) {
	database := setupScannerTestDB(t)
	scanner := newTestScanner(t, database)
	ctx := context.Background()

	filtered, err := scanner.FilterNew(ctx, nil)
	if err != nil {
		t.Fatalf("FilterNew on nil: %v", err)
	}
	if filtered != nil {
		t.Errorf("expected nil for empty input, got %v", filtered)
	}
}

func TestScannerIngestDiscoveries(t *testing.T) {
	database := setupScannerTestDB(t)
	scanner := newTestScanner(t, database)
	ctx := context.Background()

	discoveries := []Discovery{
		{
			Title:        "AI Tool Launch",
			Description:  "A new AI tool was launched",
			URL:          "https://example.com/ai-tool",
			Source:       "test-feed",
			Category:     "ai-devtools",
			DiscoveredAt: time.Now(),
			ContentHash:  ContentHash("test-feed", "AI Tool Launch"),
		},
		{
			Title:        "Research Paper",
			Description:  "Interesting paper on LLMs",
			URL:          "https://example.com/paper",
			Source:       "test-feed",
			Category:     "academic",
			DiscoveredAt: time.Now(),
			ContentHash:  ContentHash("test-feed", "Research Paper"),
		},
	}

	count, err := scanner.IngestDiscoveries(ctx, discoveries)
	if err != nil {
		t.Fatalf("IngestDiscoveries: %v", err)
	}
	if count != 2 {
		t.Errorf("expected 2 ingested, got %d", count)
	}

	// Verify they exist in the DB.
	var dbCount int
	err = database.QueryRowContext(ctx, `SELECT COUNT(*) FROM work_items WHERE source = 'discovery'`).Scan(&dbCount)
	if err != nil {
		t.Fatalf("counting work_items: %v", err)
	}
	if dbCount != 2 {
		t.Errorf("expected 2 work_items in DB, got %d", dbCount)
	}

	// Verify status is pending.
	var status string
	err = database.QueryRowContext(ctx, `SELECT status FROM work_items WHERE source = 'discovery' LIMIT 1`).Scan(&status)
	if err != nil {
		t.Fatalf("querying status: %v", err)
	}
	if status != "pending" {
		t.Errorf("expected status 'pending', got %q", status)
	}
}

func TestScannerIngestDiscoveriesEmpty(t *testing.T) {
	database := setupScannerTestDB(t)
	scanner := newTestScanner(t, database)
	ctx := context.Background()

	count, err := scanner.IngestDiscoveries(ctx, nil)
	if err != nil {
		t.Fatalf("IngestDiscoveries on nil: %v", err)
	}
	if count != 0 {
		t.Errorf("expected 0, got %d", count)
	}
}

func TestScannerLastScanTimesRoundTrip(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "scan-times.json")

	now := time.Now().Truncate(time.Second)
	original := map[string]time.Time{
		"hackernews": now,
		"arxiv-rss":  now.Add(-24 * time.Hour),
	}

	if err := SaveLastScanTimes(path, original); err != nil {
		t.Fatalf("SaveLastScanTimes: %v", err)
	}

	loaded, err := LoadLastScanTimes(path)
	if err != nil {
		t.Fatalf("LoadLastScanTimes: %v", err)
	}

	for k, v := range original {
		got, ok := loaded[k]
		if !ok {
			t.Errorf("missing key %q after round-trip", k)
			continue
		}
		if !got.Equal(v) {
			t.Errorf("key %q: expected %v, got %v", k, v, got)
		}
	}
}

func TestScannerLastScanTimesNonexistent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "does-not-exist.json")
	times, err := LoadLastScanTimes(path)
	if err != nil {
		t.Fatalf("LoadLastScanTimes on nonexistent: %v", err)
	}
	if len(times) != 0 {
		t.Errorf("expected empty map, got %v", times)
	}
}

func TestScannerIngestDedup(t *testing.T) {
	database := setupScannerTestDB(t)
	scanner := newTestScanner(t, database)
	ctx := context.Background()

	d := Discovery{
		Title:        "Duplicate Item",
		Description:  "Same item twice",
		URL:          "https://example.com/dup",
		Source:       "test",
		DiscoveredAt: time.Now(),
		ContentHash:  ContentHash("test", "Duplicate Item"),
	}

	// Ingest once.
	count1, err := scanner.IngestDiscoveries(ctx, []Discovery{d})
	if err != nil {
		t.Fatalf("first ingest: %v", err)
	}
	if count1 != 1 {
		t.Errorf("first ingest: expected 1, got %d", count1)
	}

	// Ingest again — INSERT OR IGNORE should silently skip.
	_, err = scanner.IngestDiscoveries(ctx, []Discovery{d})
	if err != nil {
		t.Fatalf("second ingest: %v", err)
	}

	// Verify only 1 row in DB.
	var dbCount int
	err = database.QueryRowContext(ctx, `SELECT COUNT(*) FROM work_items WHERE source = 'discovery'`).Scan(&dbCount)
	if err != nil {
		t.Fatalf("counting: %v", err)
	}
	if dbCount != 1 {
		t.Errorf("expected 1 work_item after dedup, got %d", dbCount)
	}
}
