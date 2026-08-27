package orchestrator

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MochaCosine1206/orchestra/internal/db"
)

func setupTestDB(t *testing.T) *db.DB {
	t.Helper()
	d, err := db.OpenMemory()
	if err != nil {
		t.Fatalf("failed to open in-memory DB: %v", err)
	}
	ctx := context.Background()
	if err := d.InitSchema(ctx); err != nil {
		t.Fatalf("failed to init schema: %v", err)
	}
	t.Cleanup(func() { d.Close() })
	return d
}

func TestNew_ValidOpts(t *testing.T) {
	d := setupTestDB(t)
	c, err := New(ConductorOpts{
		DB:       d,
		RepoRoot: "/tmp/test",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if c.DB != d {
		t.Error("DB not set correctly")
	}
	if c.RepoRoot != "/tmp/test" {
		t.Errorf("RepoRoot = %q, want /tmp/test", c.RepoRoot)
	}
	if c.SessionID == "" {
		t.Error("SessionID should be generated")
	}
	if c.Spawner == nil {
		t.Error("Spawner should be initialized")
	}
	if c.Monitor == nil {
		t.Error("Monitor should be initialized")
	}
}

func TestNew_SessionIDPassthrough(t *testing.T) {
	d := setupTestDB(t)
	c, err := New(ConductorOpts{
		DB:        d,
		RepoRoot:  "/tmp/test",
		SessionID: "s-custom-123",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if c.SessionID != "s-custom-123" {
		t.Errorf("SessionID = %q, want %q", c.SessionID, "s-custom-123")
	}
}

func TestNew_NilDB(t *testing.T) {
	_, err := New(ConductorOpts{RepoRoot: "/tmp/test"})
	if err == nil {
		t.Fatal("expected error for nil DB")
	}
}

func TestNew_EmptyRepoRoot(t *testing.T) {
	d := setupTestDB(t)
	_, err := New(ConductorOpts{DB: d})
	if err == nil {
		t.Fatal("expected error for empty RepoRoot")
	}
}

func TestSessionTaskIDs_Empty(t *testing.T) {
	d := setupTestDB(t)
	c, _ := New(ConductorOpts{DB: d, RepoRoot: "/tmp/test"})
	ctx := context.Background()

	ids := c.sessionTaskIDs(ctx)
	if len(ids) != 0 {
		t.Errorf("expected empty, got %v", ids)
	}
}

func TestSessionTaskIDs_Stored(t *testing.T) {
	d := setupTestDB(t)
	c, _ := New(ConductorOpts{DB: d, RepoRoot: "/tmp/test"})
	ctx := context.Background()

	d.SetBlackboard(ctx, "conductor:task_ids", `["T-001","T-002","T-003"]`, "test")
	ids := c.sessionTaskIDs(ctx)
	if len(ids) != 3 {
		t.Errorf("expected 3 IDs, got %d: %v", len(ids), ids)
	}
}

func TestStoreSessionTaskIDs(t *testing.T) {
	d := setupTestDB(t)
	c, _ := New(ConductorOpts{DB: d, RepoRoot: "/tmp/test"})
	ctx := context.Background()

	err := c.storeSessionTaskIDs(ctx, []string{"T-001", "T-002"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	ids := c.sessionTaskIDs(ctx)
	if len(ids) != 2 || ids[0] != "T-001" || ids[1] != "T-002" {
		t.Errorf("round-trip failed: got %v", ids)
	}
}

func TestSetGetBlackboard(t *testing.T) {
	d := setupTestDB(t)
	c, _ := New(ConductorOpts{DB: d, RepoRoot: "/tmp/test"})
	ctx := context.Background()

	err := c.setBlackboard(ctx, "test:key", "test-value")
	if err != nil {
		t.Fatalf("setBlackboard error: %v", err)
	}

	val := c.getBlackboard(ctx, "test:key")
	if val != "test-value" {
		t.Errorf("getBlackboard = %q, want %q", val, "test-value")
	}

	// Missing key returns empty
	val = c.getBlackboard(ctx, "nonexistent")
	if val != "" {
		t.Errorf("expected empty for missing key, got %q", val)
	}
}

func TestConductorLogFile(t *testing.T) {
	d := setupTestDB(t)
	repoRoot := t.TempDir()
	c, err := New(ConductorOpts{DB: d, RepoRoot: repoRoot})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// SetupLogFile should create the log file
	if err := c.SetupLogFile(); err != nil {
		t.Fatalf("SetupLogFile failed: %v", err)
	}
	defer c.CloseLogFile()

	logPath := filepath.Join(repoRoot, ".orchestra", "logs", "conductor-"+c.SessionID+".log")
	if _, err := os.Stat(logPath); err != nil {
		t.Fatalf("log file should exist at %s: %v", logPath, err)
	}

	// Log a message and verify it appears in the file
	c.log("test message %d", 42)
	c.CloseLogFile() // flush

	content, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("reading log file: %v", err)
	}
	if !strings.Contains(string(content), "test message 42") {
		t.Errorf("log file should contain 'test message 42', got: %s", string(content))
	}
	// Verify timestamp format (HH:MM:SS prefix)
	lines := strings.TrimSpace(string(content))
	if len(lines) < 8 || lines[2] != ':' || lines[5] != ':' {
		t.Errorf("log line should start with HH:MM:SS timestamp, got: %s", lines)
	}
}

func TestDeactivateConductorWithCancelledContext(t *testing.T) {
	// G105: deactivateConductor must update DB status even when the caller's context is cancelled.
	d := setupTestDB(t)
	repoRoot := t.TempDir()
	os.MkdirAll(filepath.Join(repoRoot, ".git"), 0o755)

	c, _ := New(ConductorOpts{DB: d, RepoRoot: repoRoot})
	ctx := context.Background()

	// Activate conductor
	opts := activateOpts{
		PID:           12345,
		MaxParallel:   3,
		Goal:          "Test G105 cancelled context",
		SessionID:     c.SessionID,
		ModelStrategy: "all-opus",
		BaseBranch:    "dev",
		Runtime:       "local",
	}
	if err := c.activateConductor(ctx, opts); err != nil {
		t.Fatalf("activateConductor failed: %v", err)
	}

	// Verify conductor is active
	rec, err := d.GetConductor(ctx, c.ConductorID)
	if err != nil {
		t.Fatalf("GetConductor: %v", err)
	}
	if rec.Status != "active" {
		t.Fatalf("expected active, got %s", rec.Status)
	}

	// Cancel the context BEFORE calling deactivateConductor
	cancelledCtx, cancel := context.WithCancel(ctx)
	cancel() // immediately cancel

	// deactivateConductor should still update status via background context
	c.deactivateConductor(cancelledCtx)

	// Verify conductor status is terminal (not stuck on "active")
	rec, err = d.GetConductor(context.Background(), c.ConductorID)
	if err != nil {
		t.Fatalf("GetConductor after deactivate: %v", err)
	}
	if rec.Status == "active" {
		t.Error("conductor status should NOT be 'active' after deactivation with cancelled context")
	}
	if rec.Status != "completed" {
		t.Errorf("expected 'completed' (no tasks = no failures), got %q", rec.Status)
	}
}

func TestDeactivateConductorRestoresBranch(t *testing.T) {
	// G106: deactivateConductor should checkout base branch before deleting staging,
	// so the user isn't left on a stale conductor staging branch.
	d := setupTestDB(t)
	repoRoot := t.TempDir()

	// Create a real git repo with a dev branch
	for _, args := range [][]string{
		{"init"},
		{"checkout", "-b", "dev"},
		{"commit", "--allow-empty", "-m", "init"},
	} {
		cmd := exec.Command("git", args...)
		cmd.Dir = repoRoot
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v failed: %v\n%s", args, err, out)
		}
	}

	c, _ := New(ConductorOpts{DB: d, RepoRoot: repoRoot})
	ctx := context.Background()

	// Activate conductor (creates staging branch)
	opts := activateOpts{
		PID:           12345,
		MaxParallel:   3,
		Goal:          "Test G106 branch restore",
		SessionID:     c.SessionID,
		ModelStrategy: "all-opus",
		BaseBranch:    "dev",
		Runtime:       "local",
	}
	if err := c.activateConductor(ctx, opts); err != nil {
		t.Fatalf("activateConductor failed: %v", err)
	}

	// Simulate what Merge() does: checkout the staging branch
	stagingBranch := "conductor/" + c.SessionID
	cmd := exec.Command("git", "checkout", stagingBranch)
	cmd.Dir = repoRoot
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("checkout staging branch: %v\n%s", err, out)
	}

	// Verify we're on the staging branch
	head, _ := exec.Command("git", "-C", repoRoot, "rev-parse", "--abbrev-ref", "HEAD").Output()
	if strings.TrimSpace(string(head)) != stagingBranch {
		t.Fatalf("expected to be on %s, got %s", stagingBranch, strings.TrimSpace(string(head)))
	}

	// Deactivate — should switch to dev before deleting staging
	c.deactivateConductor(ctx)

	// Verify we're back on dev
	head, _ = exec.Command("git", "-C", repoRoot, "rev-parse", "--abbrev-ref", "HEAD").Output()
	currentBranch := strings.TrimSpace(string(head))
	if currentBranch == stagingBranch {
		t.Errorf("still on staging branch %s after deactivation — should be on dev", stagingBranch)
	}
	if currentBranch != "dev" {
		t.Errorf("expected to be on 'dev', got %q", currentBranch)
	}

	// Verify staging branch was deleted
	cmd = exec.Command("git", "branch", "--list", stagingBranch)
	cmd.Dir = repoRoot
	out, _ := cmd.CombinedOutput()
	if strings.TrimSpace(string(out)) != "" {
		t.Errorf("staging branch %s should have been deleted", stagingBranch)
	}
}

func TestUpdateRefStaleIndex(t *testing.T) {
	// G107: After Merge() restores the branch and MergeStagingToDev() runs
	// update-ref, the index/working tree are stale. Verify that git reset --hard
	// after update-ref produces a clean status.
	repoRoot := t.TempDir()

	// Create a real git repo with a base branch and a file
	for _, args := range [][]string{
		{"init"},
		{"checkout", "-b", "base"},
		{"config", "user.email", "test@test.com"},
		{"config", "user.name", "test"},
	} {
		cmd := exec.Command("git", args...)
		cmd.Dir = repoRoot
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v failed: %v\n%s", args, err, out)
		}
	}

	// Create initial file and commit
	if err := os.WriteFile(filepath.Join(repoRoot, "file.txt"), []byte("line1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"add", "file.txt"},
		{"commit", "-m", "init"},
	} {
		cmd := exec.Command("git", args...)
		cmd.Dir = repoRoot
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v failed: %v\n%s", args, err, out)
		}
	}

	// Create staging branch, add a change, commit
	for _, args := range [][]string{
		{"branch", "staging", "base"},
		{"checkout", "staging"},
	} {
		cmd := exec.Command("git", args...)
		cmd.Dir = repoRoot
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v failed: %v\n%s", args, err, out)
		}
	}
	if err := os.WriteFile(filepath.Join(repoRoot, "file.txt"), []byte("line1\nagent-change\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"add", "file.txt"},
		{"commit", "-m", "agent merge"},
	} {
		cmd := exec.Command("git", args...)
		cmd.Dir = repoRoot
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v failed: %v\n%s", args, err, out)
		}
	}

	// Simulate Merge() G106 defer: checkout back to base
	cmd := exec.Command("git", "checkout", "base")
	cmd.Dir = repoRoot
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("checkout base failed: %v\n%s", err, out)
	}

	// Status should be clean at this point
	statusOut, _ := exec.Command("git", "-C", repoRoot, "status", "--porcelain").Output()
	if strings.TrimSpace(string(statusOut)) != "" {
		t.Fatalf("expected clean status before update-ref, got: %s", statusOut)
	}

	// Simulate MergeStagingToDev: update-ref advances base to staging SHA
	stagingSHA, _ := exec.Command("git", "-C", repoRoot, "rev-parse", "staging").Output()
	cmd = exec.Command("git", "update-ref", "refs/heads/base", strings.TrimSpace(string(stagingSHA)))
	cmd.Dir = repoRoot
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("update-ref failed: %v\n%s", err, out)
	}

	// BUG: Status is now dirty — index is stale relative to new HEAD
	statusOut, _ = exec.Command("git", "-C", repoRoot, "status", "--porcelain").Output()
	if strings.TrimSpace(string(statusOut)) == "" {
		t.Skip("update-ref didn't produce stale index — git behavior may have changed")
	}

	// G107 FIX: git reset --hard HEAD syncs index + working tree to new HEAD
	cmd = exec.Command("git", "reset", "--hard", "HEAD")
	cmd.Dir = repoRoot
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git reset --hard failed: %v\n%s", err, out)
	}

	// Verify status is clean after reset
	statusOut, _ = exec.Command("git", "-C", repoRoot, "status", "--porcelain").Output()
	if strings.TrimSpace(string(statusOut)) != "" {
		t.Errorf("expected clean status after git reset --hard, got: %s", statusOut)
	}

	// Verify working tree has the new content
	content, _ := os.ReadFile(filepath.Join(repoRoot, "file.txt"))
	if !strings.Contains(string(content), "agent-change") {
		t.Errorf("working tree should contain 'agent-change' after reset, got: %s", content)
	}
}

func TestParseTaskIDsJSON(t *testing.T) {
	tests := []struct {
		input string
		want  int
	}{
		{"", 0},
		{"[]", 0},
		{`["T-001"]`, 1},
		{`["T-001","T-002"]`, 2},
		{`["T-001", "T-002", "T-003"]`, 3},
	}
	for _, tt := range tests {
		got := parseTaskIDsJSON(tt.input)
		if len(got) != tt.want {
			t.Errorf("parseTaskIDsJSON(%q) = len %d, want %d", tt.input, len(got), tt.want)
		}
	}
}
