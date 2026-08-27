package orchestrator

import (
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/MochaCosine1206/orchestra/internal/db"
)

func setupTestConductor(t *testing.T) (*Conductor, func()) {
	t.Helper()
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")
	d, err := db.Open(dbPath)
	if err != nil {
		t.Fatalf("opening test db: %v", err)
	}
	if err := d.InitSchema(context.Background()); err != nil {
		t.Fatalf("init schema: %v", err)
	}
	c := &Conductor{
		DB:       d,
		RepoRoot: tmpDir,
		Log:      func(string) {},
	}
	return c, func() { d.Close() }
}

func TestNormalizeGoalHash(t *testing.T) {
	// Same content with different whitespace/casing should produce same hash
	h1 := NormalizeGoalHash("Fix  the   bug  in  main.go")
	h2 := NormalizeGoalHash("fix the bug in main.go")
	h3 := NormalizeGoalHash("  FIX  THE  BUG  IN  MAIN.GO  ")
	if h1 != h2 || h2 != h3 {
		t.Errorf("expected identical hashes, got %s, %s, %s", h1, h2, h3)
	}

	// Different content should produce different hash
	h4 := NormalizeGoalHash("add logging to server.go")
	if h1 == h4 {
		t.Error("different goals should produce different hashes")
	}
}

func TestExtractW5H2Key(t *testing.T) {
	tests := []struct {
		goal       string
		wantAction string
		wantTarget string
	}{
		{"fix the bug in internal/db/queries.go", "fix", "internal/db/queries.go"},
		{"Add logging to server/handler.go", "add", "server/handler.go"},
		{"refactor the auth module in pkg/auth", "refactor", "pkg/auth"},
		{"remove unused imports from main.go", "remove", "main.go"},
		{"update the README", "update", ""},
		{"do something vague", "", ""},
	}
	for _, tt := range tests {
		action, target := ExtractW5H2Key(tt.goal)
		if action != tt.wantAction {
			t.Errorf("ExtractW5H2Key(%q) action = %q, want %q", tt.goal, action, tt.wantAction)
		}
		if target != tt.wantTarget {
			t.Errorf("ExtractW5H2Key(%q) target = %q, want %q", tt.goal, target, tt.wantTarget)
		}
	}
}

func TestKeywordJaccard(t *testing.T) {
	// Identical sets
	a := map[string]bool{"fix": true, "bug": true, "main": true}
	b := map[string]bool{"fix": true, "bug": true, "main": true}
	if score := KeywordJaccard(a, b); score != 1.0 {
		t.Errorf("identical sets: got %f, want 1.0", score)
	}

	// Disjoint sets
	c := map[string]bool{"alpha": true, "beta": true}
	d := map[string]bool{"gamma": true, "delta": true}
	if score := KeywordJaccard(c, d); score != 0.0 {
		t.Errorf("disjoint sets: got %f, want 0.0", score)
	}

	// Partial overlap: {fix, bug, main} ∩ {fix, bug, server} = {fix, bug}, union=4
	e := map[string]bool{"fix": true, "bug": true, "server": true}
	score := KeywordJaccard(a, e)
	expected := 2.0 / 4.0
	if score != expected {
		t.Errorf("partial overlap: got %f, want %f", score, expected)
	}

	// Both empty
	if score := KeywordJaccard(map[string]bool{}, map[string]bool{}); score != 0.0 {
		t.Errorf("both empty: got %f, want 0.0", score)
	}
}

func TestExtractKeywords(t *testing.T) {
	kw := extractKeywords("Fix the bug in main.go, please!")
	if kw["the"] || kw["in"] {
		t.Error("stop words should be excluded")
	}
	if !kw["fix"] || !kw["bug"] || !kw["please"] {
		t.Errorf("expected non-stop words, got: %v", kw)
	}
	if !kw["main.go"] {
		t.Error("expected main.go after punctuation stripping")
	}
}

func TestActionTTL(t *testing.T) {
	if ttl := actionTTL("fix"); ttl != 7 {
		t.Errorf("fix TTL = %d, want 7", ttl)
	}
	if ttl := actionTTL("add"); ttl != 14 {
		t.Errorf("add TTL = %d, want 14", ttl)
	}
	if ttl := actionTTL("refactor"); ttl != 14 {
		t.Errorf("refactor TTL = %d, want 14", ttl)
	}
	if ttl := actionTTL("docs"); ttl != 30 {
		t.Errorf("docs TTL = %d, want 30", ttl)
	}
	if ttl := actionTTL("unknown"); ttl != 14 {
		t.Errorf("default TTL = %d, want 14", ttl)
	}
}

func TestTierMatches(t *testing.T) {
	entry := &db.PlanCacheEntry{Tier: sql.NullInt64{Int64: 2, Valid: true}}
	if !tierMatches(entry, 2) {
		t.Error("should match same tier")
	}
	if tierMatches(entry, 1) {
		t.Error("should not match different tier")
	}

	// Null tier matches anything
	nullEntry := &db.PlanCacheEntry{Tier: sql.NullInt64{Valid: false}}
	if !tierMatches(nullEntry, 3) {
		t.Error("null tier should match any estimated tier")
	}
}

func TestLookupCache_ExactMatch(t *testing.T) {
	c, cleanup := setupTestConductor(t)
	defer cleanup()
	ctx := context.Background()

	goal := "fix the bug in internal/db/queries.go"
	plan := []DecomposedTask{{ID: "t1", Title: "Fix query", Role: "implementer"}}
	planJSON, _ := json.Marshal(plan)
	hash := NormalizeGoalHash(goal)

	err := c.DB.StorePlanCache(ctx, hash, goal, "fix:internal/db/queries.go", "fix bug queries.go",
		string(planJSON), "fix", 1, 7, "")
	if err != nil {
		t.Fatalf("storing plan cache: %v", err)
	}

	hit, err := c.LookupCache(ctx, goal, 1)
	if err != nil {
		t.Fatalf("LookupCache: %v", err)
	}
	if hit == nil {
		t.Fatal("expected cache hit, got nil")
	}
	if hit.Score != 1.0 {
		t.Errorf("exact match score = %f, want 1.0", hit.Score)
	}
	if len(hit.Plan) != 1 || hit.Plan[0].ID != "t1" {
		t.Errorf("unexpected plan: %+v", hit.Plan)
	}
}

func TestLookupCache_W5H2Match(t *testing.T) {
	c, cleanup := setupTestConductor(t)
	defer cleanup()
	ctx := context.Background()

	// Store with a different goal text but same W5H2 key
	storedGoal := "fix the old bug in internal/db/queries.go"
	plan := []DecomposedTask{{ID: "t2", Title: "Fix query v2", Role: "implementer"}}
	planJSON, _ := json.Marshal(plan)
	hash := NormalizeGoalHash(storedGoal)

	err := c.DB.StorePlanCache(ctx, hash, storedGoal, "fix:internal/db/queries.go", "fix old bug queries.go",
		string(planJSON), "fix", 1, 7, "")
	if err != nil {
		t.Fatalf("storing plan cache: %v", err)
	}

	// Look up with a different goal that has same W5H2 key
	lookupGoal := "fix the new issue in internal/db/queries.go"
	hit, err := c.LookupCache(ctx, lookupGoal, 1)
	if err != nil {
		t.Fatalf("LookupCache: %v", err)
	}
	if hit == nil {
		t.Fatal("expected W5H2 cache hit, got nil")
	}
	if hit.Score != 0.9 {
		t.Errorf("W5H2 match score = %f, want 0.9", hit.Score)
	}
}

func TestLookupCache_JaccardMatch(t *testing.T) {
	c, cleanup := setupTestConductor(t)
	defer cleanup()
	ctx := context.Background()

	// Store a plan with keywords that will partially overlap
	storedGoal := "implement logging middleware server handler"
	plan := []DecomposedTask{{ID: "t3", Title: "Add logging", Role: "implementer"}}
	planJSON, _ := json.Marshal(plan)
	hash := NormalizeGoalHash(storedGoal)

	err := c.DB.StorePlanCache(ctx, hash, storedGoal, "", "implement logging middleware server handler",
		string(planJSON), "implement", 2, 14, "")
	if err != nil {
		t.Fatalf("storing plan cache: %v", err)
	}

	// Lookup with high overlap goal (same keywords, slightly different)
	lookupGoal := "implement logging middleware server request"
	hit, err := c.LookupCache(ctx, lookupGoal, 2)
	if err != nil {
		t.Fatalf("LookupCache: %v", err)
	}
	if hit == nil {
		t.Fatal("expected Jaccard cache hit, got nil")
	}
	// 4 shared out of 6 union = 0.667
	if hit.Score < 0.6 {
		t.Errorf("Jaccard score = %f, want >= 0.6", hit.Score)
	}
}

func TestLookupCache_NoMatch(t *testing.T) {
	c, cleanup := setupTestConductor(t)
	defer cleanup()
	ctx := context.Background()

	hit, err := c.LookupCache(ctx, "completely novel goal", 1)
	if err != nil {
		t.Fatalf("LookupCache: %v", err)
	}
	if hit != nil {
		t.Errorf("expected nil for no match, got %+v", hit)
	}
}

func TestLookupCache_TierMismatch(t *testing.T) {
	c, cleanup := setupTestConductor(t)
	defer cleanup()
	ctx := context.Background()

	goal := "fix the bug in internal/db/queries.go"
	plan := []DecomposedTask{{ID: "t1", Title: "Fix query", Role: "implementer"}}
	planJSON, _ := json.Marshal(plan)
	hash := NormalizeGoalHash(goal)

	// Store as tier 1
	err := c.DB.StorePlanCache(ctx, hash, goal, "fix:internal/db/queries.go", "fix bug queries.go",
		string(planJSON), "fix", 1, 7, "")
	if err != nil {
		t.Fatalf("storing plan cache: %v", err)
	}

	// Lookup with tier 2 — should miss
	hit, err := c.LookupCache(ctx, goal, 2)
	if err != nil {
		t.Fatalf("LookupCache: %v", err)
	}
	if hit != nil {
		t.Errorf("expected nil for tier mismatch, got %+v", hit)
	}
}

func TestStoreCache(t *testing.T) {
	c, cleanup := setupTestConductor(t)
	defer cleanup()
	ctx := context.Background()

	goal := "fix the bug in internal/db/queries.go"
	plan := []DecomposedTask{{ID: "t1", Title: "Fix query", Role: "implementer"}}

	if err := c.StoreCache(ctx, goal, plan, 1); err != nil {
		t.Fatalf("StoreCache: %v", err)
	}

	// Verify it can be looked up
	hit, err := c.LookupCache(ctx, goal, 1)
	if err != nil {
		t.Fatalf("LookupCache after store: %v", err)
	}
	if hit == nil {
		t.Fatal("expected cache hit after store")
	}
	if hit.Score != 1.0 {
		t.Errorf("score = %f, want 1.0", hit.Score)
	}
}

func TestInvalidateStaleEntries(t *testing.T) {
	c, cleanup := setupTestConductor(t)
	defer cleanup()
	ctx := context.Background()

	// Create a temp file to track
	tmpFile := filepath.Join(c.RepoRoot, "test.go")
	if err := os.WriteFile(tmpFile, []byte("package main"), 0644); err != nil {
		t.Fatalf("creating temp file: %v", err)
	}

	// Record mtime slightly in the future so it appears stale after we modify
	info, _ := os.Stat(tmpFile)
	pastTime := info.ModTime().Add(-1 * time.Second).UTC().Format(time.RFC3339)
	mtimes := map[string]string{"test.go": pastTime}
	mtimesJSON, _ := json.Marshal(mtimes)

	goal := "fix test.go"
	plan := []DecomposedTask{{ID: "t1", Title: "Fix", Role: "implementer"}}
	planJSON, _ := json.Marshal(plan)
	hash := NormalizeGoalHash(goal)

	err := c.DB.StorePlanCache(ctx, hash, goal, "", "fix test.go",
		string(planJSON), "fix", 1, 7, string(mtimesJSON))
	if err != nil {
		t.Fatalf("storing plan cache: %v", err)
	}

	// Backdate entry to > 1 hour ago so invalidation grace period doesn't skip it
	c.DB.ExecContext(ctx, `UPDATE plan_cache SET created_at = datetime('now', '-2 hours') WHERE goal_hash = ?`, hash)

	// Verify it exists
	hit, _ := c.LookupCache(ctx, goal, 1)
	if hit == nil {
		t.Fatal("expected cache hit before invalidation")
	}

	// Invalidate stale entries — file mtime is after recorded time
	if err := c.InvalidateStaleEntries(ctx); err != nil {
		t.Fatalf("InvalidateStaleEntries: %v", err)
	}

	// Should be gone now
	hit, _ = c.LookupCache(ctx, goal, 1)
	if hit != nil {
		t.Error("expected nil after invalidation of stale entry")
	}
}

func TestInvalidateStaleEntries_DeletedFile(t *testing.T) {
	c, cleanup := setupTestConductor(t)
	defer cleanup()
	ctx := context.Background()

	mtimes := map[string]string{"nonexistent.go": time.Now().UTC().Format(time.RFC3339)}
	mtimesJSON, _ := json.Marshal(mtimes)

	goal := "fix nonexistent.go"
	plan := []DecomposedTask{{ID: "t1", Title: "Fix", Role: "implementer"}}
	planJSON, _ := json.Marshal(plan)
	hash := NormalizeGoalHash(goal)

	c.DB.StorePlanCache(ctx, hash, goal, "", "fix nonexistent.go",
		string(planJSON), "fix", 1, 7, string(mtimesJSON))

	// Backdate entry to > 1 hour ago so invalidation grace period doesn't skip it
	c.DB.ExecContext(ctx, `UPDATE plan_cache SET created_at = datetime('now', '-2 hours') WHERE goal_hash = ?`, hash)

	if err := c.InvalidateStaleEntries(ctx); err != nil {
		t.Fatalf("InvalidateStaleEntries: %v", err)
	}

	hit, _ := c.LookupCache(ctx, goal, 1)
	if hit != nil {
		t.Error("expected nil after invalidation for deleted file")
	}
}

func TestTierString(t *testing.T) {
	if s := tierString(1); s != "simple" {
		t.Errorf("tierString(1) = %q, want simple", s)
	}
	if s := tierString(2); s != "medium" {
		t.Errorf("tierString(2) = %q, want medium", s)
	}
	if s := tierString(3); s != "complex" {
		t.Errorf("tierString(3) = %q, want complex", s)
	}
	if s := tierString(99); s != "tier-99" {
		t.Errorf("tierString(99) = %q, want tier-99", s)
	}
}
