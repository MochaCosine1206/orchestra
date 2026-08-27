package cage

import (
	"context"
	"database/sql"
	"log/slog"
	"testing"

	_ "github.com/ncruces/go-sqlite3/driver"
	_ "github.com/ncruces/go-sqlite3/embed"
)

func openTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("opening test db: %v", err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { db.Close() })
	return db
}

func newTestEnforcer(t *testing.T, cfg CageConfig) *CageEnforcer {
	t.Helper()
	db := openTestDB(t)
	logger := slog.Default()
	ce := NewCageEnforcer(cfg, db, logger)
	if err := ce.Init(context.Background()); err != nil {
		t.Fatalf("init: %v", err)
	}
	return ce
}

func TestCheckRepoAccess_WithinLimit(t *testing.T) {
	cfg := CageConfig{MaxRepos: 2, AllowedBranches: []string{"*"}, MaxFileOps: 100, MaxSpendTokens: 1000}
	ce := newTestEnforcer(t, cfg)
	ctx := context.Background()

	ok, reason := ce.CheckRepoAccess(ctx, "/tmp/repo1")
	if !ok {
		t.Errorf("expected allowed, got denied: %s", reason)
	}

	ok, reason = ce.CheckRepoAccess(ctx, "/tmp/repo2")
	if !ok {
		t.Errorf("expected allowed for second repo, got denied: %s", reason)
	}

	// Same repo again should be fine (already known).
	ok, reason = ce.CheckRepoAccess(ctx, "/tmp/repo1")
	if !ok {
		t.Errorf("expected allowed for known repo, got denied: %s", reason)
	}
}

func TestCheckRepoAccess_ExceedsLimit(t *testing.T) {
	cfg := CageConfig{MaxRepos: 1, AllowedBranches: []string{"*"}, MaxFileOps: 100, MaxSpendTokens: 1000}
	ce := newTestEnforcer(t, cfg)
	ctx := context.Background()

	ok, _ := ce.CheckRepoAccess(ctx, "/tmp/repo1")
	if !ok {
		t.Fatal("first repo should be allowed")
	}

	ok, reason := ce.CheckRepoAccess(ctx, "/tmp/repo2")
	if ok {
		t.Error("expected denial for second repo when limit is 1")
	}
	if reason == "" {
		t.Error("expected non-empty reason for denial")
	}
}

func TestCheckBranchAccess(t *testing.T) {
	cfg := CageConfig{
		MaxRepos:        5,
		AllowedBranches: []string{"feature/*", "fix/*", "main"},
		MaxFileOps:      100,
		MaxSpendTokens:  1000,
	}
	ce := newTestEnforcer(t, cfg)
	ctx := context.Background()

	tests := []struct {
		branch string
		want   bool
	}{
		{"feature/dead-mans-switch", true},
		{"fix/typo", true},
		{"main", true},
		{"dev", false},
		{"experiment/test", false},
	}
	for _, tt := range tests {
		t.Run(tt.branch, func(t *testing.T) {
			ok, _ := ce.CheckBranchAccess(ctx, tt.branch)
			if ok != tt.want {
				t.Errorf("CheckBranchAccess(%q) = %v, want %v", tt.branch, ok, tt.want)
			}
		})
	}
}

func TestCheckFileOps(t *testing.T) {
	cfg := CageConfig{MaxRepos: 5, AllowedBranches: []string{"*"}, MaxFileOps: 10, MaxSpendTokens: 1000}
	ce := newTestEnforcer(t, cfg)
	ctx := context.Background()

	ok, _ := ce.CheckFileOps(ctx, 5)
	if !ok {
		t.Error("5 ops should be within limit of 10")
	}

	ok, _ = ce.CheckFileOps(ctx, 5)
	if !ok {
		t.Error("10 total ops should be within limit of 10")
	}

	ok, reason := ce.CheckFileOps(ctx, 1)
	if ok {
		t.Error("11 total ops should exceed limit of 10")
	}
	if reason == "" {
		t.Error("expected non-empty reason for file ops denial")
	}
}

func TestCheckSpend(t *testing.T) {
	cfg := CageConfig{MaxRepos: 5, AllowedBranches: []string{"*"}, MaxFileOps: 100, MaxSpendTokens: 1000}
	ce := newTestEnforcer(t, cfg)
	ctx := context.Background()

	ok, _ := ce.CheckSpend(ctx, 500)
	if !ok {
		t.Error("500 tokens should be within 1000 limit")
	}

	ok, _ = ce.CheckSpend(ctx, 500)
	if !ok {
		t.Error("1000 total tokens should be within 1000 limit")
	}

	ok, reason := ce.CheckSpend(ctx, 1)
	if ok {
		t.Error("1001 total tokens should exceed 1000 limit")
	}
	if reason == "" {
		t.Error("expected non-empty reason for spend denial")
	}
}

func TestViolationsLoggedToSQLite(t *testing.T) {
	db := openTestDB(t)
	logger := slog.Default()
	cfg := CageConfig{MaxRepos: 0, AllowedBranches: []string{}, MaxFileOps: 0, MaxSpendTokens: 0}
	ce := NewCageEnforcer(cfg, db, logger)
	ctx := context.Background()

	if err := ce.Init(ctx); err != nil {
		t.Fatalf("init: %v", err)
	}

	// Trigger violations of each type.
	ce.CheckRepoAccess(ctx, "/tmp/repo1")
	ce.CheckBranchAccess(ctx, "main")
	ce.CheckFileOps(ctx, 1)
	ce.CheckSpend(ctx, 1)

	var count int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM cage_violations`).Scan(&count); err != nil {
		t.Fatalf("counting violations: %v", err)
	}
	if count != 4 {
		t.Errorf("expected 4 violations logged, got %d", count)
	}
}
