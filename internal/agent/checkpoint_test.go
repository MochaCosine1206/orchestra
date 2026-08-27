package agent

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestExtractCheckpointNilOnMissingWorktree(t *testing.T) {
	d := setupTestDB(t)
	ctx := context.Background()

	cp := ExtractCheckpoint(ctx, d, "t1", "/nonexistent/path", t.TempDir())
	if cp != nil {
		t.Error("expected nil checkpoint for nonexistent worktree")
	}
}

func TestExtractCheckpointNilOnEmptyPath(t *testing.T) {
	d := setupTestDB(t)
	ctx := context.Background()

	cp := ExtractCheckpoint(ctx, d, "t1", "", t.TempDir())
	if cp != nil {
		t.Error("expected nil checkpoint for empty worktree path")
	}
}

func TestExtractCheckpointEarlyProgress(t *testing.T) {
	d := setupTestDB(t)
	ctx := context.Background()

	// Create a real git repo as worktree
	worktree := t.TempDir()
	initGitRepo(t, worktree)

	logsDir := t.TempDir()

	cp := ExtractCheckpoint(ctx, d, "t1", worktree, logsDir)
	if cp == nil {
		t.Fatal("expected non-nil checkpoint")
	}
	if cp.TaskID != "t1" {
		t.Errorf("expected task_id t1, got %s", cp.TaskID)
	}
	if cp.EstimatedProgress != "early" {
		t.Errorf("expected early progress with no commits, got %s", cp.EstimatedProgress)
	}
	if cp.TestsPassing != "unknown" {
		t.Errorf("expected tests_passing unknown, got %s", cp.TestsPassing)
	}
}

func TestExtractCheckpointWithCommits(t *testing.T) {
	d := setupTestDB(t)
	ctx := context.Background()

	worktree := t.TempDir()
	initGitRepoWithBranch(t, worktree)

	logsDir := t.TempDir()

	// Create some commits on a feature branch
	os.WriteFile(filepath.Join(worktree, "file1.go"), []byte("package main"), 0o644)
	gitExec(t, worktree, "add", "-A")
	gitExec(t, worktree, "commit", "-m", "first change")

	os.WriteFile(filepath.Join(worktree, "file2.go"), []byte("package main"), 0o644)
	gitExec(t, worktree, "add", "-A")
	gitExec(t, worktree, "commit", "-m", "second change")

	os.WriteFile(filepath.Join(worktree, "file3.go"), []byte("package main"), 0o644)
	gitExec(t, worktree, "add", "-A")
	gitExec(t, worktree, "commit", "-m", "third change")

	cp := ExtractCheckpoint(ctx, d, "t1", worktree, logsDir)
	if cp == nil {
		t.Fatal("expected non-nil checkpoint")
	}
	if len(cp.CommitHashes) != 3 {
		t.Errorf("expected 3 commits, got %d", len(cp.CommitHashes))
	}
	if cp.EstimatedProgress != "late" {
		t.Errorf("expected late progress with 3 commits, got %s", cp.EstimatedProgress)
	}
	if len(cp.FilesModified) < 3 {
		t.Errorf("expected at least 3 files modified, got %d", len(cp.FilesModified))
	}
}

func TestExtractCheckpointMidProgress(t *testing.T) {
	d := setupTestDB(t)
	ctx := context.Background()

	worktree := t.TempDir()
	initGitRepoWithBranch(t, worktree)

	logsDir := t.TempDir()

	os.WriteFile(filepath.Join(worktree, "file1.go"), []byte("package main"), 0o644)
	gitExec(t, worktree, "add", "-A")
	gitExec(t, worktree, "commit", "-m", "one commit")

	cp := ExtractCheckpoint(ctx, d, "t1", worktree, logsDir)
	if cp == nil {
		t.Fatal("expected non-nil checkpoint")
	}
	if cp.EstimatedProgress != "mid" {
		t.Errorf("expected mid progress with 1 commit, got %s", cp.EstimatedProgress)
	}
}

func TestExtractCheckpointUncommittedFiles(t *testing.T) {
	d := setupTestDB(t)
	ctx := context.Background()

	worktree := t.TempDir()
	initGitRepoWithBranch(t, worktree)

	logsDir := t.TempDir()

	// Create uncommitted files
	os.WriteFile(filepath.Join(worktree, "uncommitted.go"), []byte("package main"), 0o644)
	os.WriteFile(filepath.Join(worktree, "also_uncommitted.go"), []byte("package main"), 0o644)

	cp := ExtractCheckpoint(ctx, d, "t1", worktree, logsDir)
	if cp == nil {
		t.Fatal("expected non-nil checkpoint")
	}
	if cp.UncommittedFiles != 2 {
		t.Errorf("expected 2 uncommitted files, got %d", cp.UncommittedFiles)
	}
}

func TestExtractCheckpointJSONMarshal(t *testing.T) {
	d := setupTestDB(t)
	ctx := context.Background()

	worktree := t.TempDir()
	initGitRepoWithBranch(t, worktree)
	logsDir := t.TempDir()

	os.WriteFile(filepath.Join(worktree, "file1.go"), []byte("package main"), 0o644)
	gitExec(t, worktree, "add", "-A")
	gitExec(t, worktree, "commit", "-m", "test commit")

	cp := ExtractCheckpoint(ctx, d, "t1", worktree, logsDir)
	if cp == nil {
		t.Fatal("expected non-nil checkpoint")
	}

	data, err := json.Marshal(cp)
	if err != nil {
		t.Fatalf("failed to marshal checkpoint: %v", err)
	}
	if len(data) > 3000 {
		t.Errorf("checkpoint JSON exceeds 3000 chars: %d", len(data))
	}

	// Verify roundtrip
	var cp2 Checkpoint
	if err := json.Unmarshal(data, &cp2); err != nil {
		t.Fatalf("failed to unmarshal checkpoint: %v", err)
	}
	if cp2.TaskID != "t1" {
		t.Errorf("roundtrip task_id mismatch: %s", cp2.TaskID)
	}
}

func TestExtractCheckpointCustomBaseBranch(t *testing.T) {
	d := setupTestDB(t)
	ctx := context.Background()
	d.SetBlackboard(ctx, "base_branch", "main", "test")

	worktree := t.TempDir()
	initGitRepoWithNamedBranch(t, worktree, "main")
	logsDir := t.TempDir()

	os.WriteFile(filepath.Join(worktree, "file.go"), []byte("package main"), 0o644)
	gitExec(t, worktree, "add", "-A")
	gitExec(t, worktree, "commit", "-m", "commit on feature")

	cp := ExtractCheckpoint(ctx, d, "t1", worktree, logsDir)
	if cp == nil {
		t.Fatal("expected non-nil checkpoint")
	}
	if len(cp.CommitHashes) != 1 {
		t.Errorf("expected 1 commit ahead of main, got %d", len(cp.CommitHashes))
	}
}

func TestExtractLogSummaryEmpty(t *testing.T) {
	summary := extractLogSummary("/nonexistent/file.jsonl")
	if summary != "" {
		t.Errorf("expected empty summary for nonexistent file, got: %s", summary)
	}
}

func TestExtractLogSummaryWithToolUse(t *testing.T) {
	logDir := t.TempDir()
	logFile := filepath.Join(logDir, "test.jsonl")

	lines := []string{
		`{"type":"assistant","message":{"content":[{"type":"tool_use","name":"Read"}]}}`,
		`{"type":"assistant","message":{"content":[{"type":"tool_use","name":"Edit"}]}}`,
		`{"type":"assistant","message":{"content":[{"type":"tool_use","name":"Write"}]}}`,
		`{"type":"assistant","message":{"content":[{"type":"tool_use","name":"Bash"}]}}`,
	}
	os.WriteFile(logFile, []byte(joinLines(lines)), 0o644)

	summary := extractLogSummary(logFile)
	if summary == "" {
		t.Error("expected non-empty summary")
	}
	// Should keep last 3 only
	if count := countOccurrences(summary, "Used tool:"); count != 3 {
		t.Errorf("expected 3 tool summaries, got %d", count)
	}
	// Should NOT contain Read (the oldest)
	if containsString(summary, "Read") {
		t.Error("should not contain oldest tool (Read)")
	}
	// Should contain the last 3
	if !containsString(summary, "Edit") || !containsString(summary, "Write") || !containsString(summary, "Bash") {
		t.Error("should contain Edit, Write, and Bash")
	}
}

func TestSelfCheckpointMerge(t *testing.T) {
	d := setupTestDB(t)
	ctx := context.Background()

	worktree := t.TempDir()
	initGitRepoWithBranch(t, worktree)
	logsDir := t.TempDir()

	// Create a commit so checkpoint has something
	os.WriteFile(filepath.Join(worktree, "file.go"), []byte("package main"), 0o644)
	gitExec(t, worktree, "add", "-A")
	gitExec(t, worktree, "commit", "-m", "work")

	// Write a self-reported checkpoint file
	selfCP := `{"completed":["task A","task B"],"remaining":["task C"],"notes":"almost done"}`
	os.WriteFile(filepath.Join(worktree, ".checkpoint.json"), []byte(selfCP), 0o644)

	cp := ExtractCheckpoint(ctx, d, "t1", worktree, logsDir)
	if cp == nil {
		t.Fatal("expected non-nil checkpoint")
	}

	// Verify self-reported data was merged
	if !containsString(cp.LogTailSummary, "Self-reported completed: 2 items") {
		t.Errorf("expected completed items in log tail, got: %s", cp.LogTailSummary)
	}
	if !containsString(cp.LogTailSummary, "Self-reported remaining: 1 items") {
		t.Errorf("expected remaining items in log tail, got: %s", cp.LogTailSummary)
	}
	if !containsString(cp.LogTailSummary, "Notes: almost done") {
		t.Errorf("expected notes in log tail, got: %s", cp.LogTailSummary)
	}
}

func TestSelfCheckpointMergeNoFile(t *testing.T) {
	d := setupTestDB(t)
	ctx := context.Background()

	worktree := t.TempDir()
	initGitRepoWithBranch(t, worktree)
	logsDir := t.TempDir()

	// No .checkpoint.json file — should work fine
	cp := ExtractCheckpoint(ctx, d, "t1", worktree, logsDir)
	if cp == nil {
		t.Fatal("expected non-nil checkpoint")
	}
	// LogTailSummary should not mention self-reported data
	if containsString(cp.LogTailSummary, "Self-reported") {
		t.Error("should not contain self-reported data when no checkpoint file exists")
	}
}

func TestSelfCheckpointMergeInvalidJSON(t *testing.T) {
	d := setupTestDB(t)
	ctx := context.Background()

	worktree := t.TempDir()
	initGitRepoWithBranch(t, worktree)
	logsDir := t.TempDir()

	// Write invalid JSON — should be silently ignored
	os.WriteFile(filepath.Join(worktree, ".checkpoint.json"), []byte("not json"), 0o644)

	cp := ExtractCheckpoint(ctx, d, "t1", worktree, logsDir)
	if cp == nil {
		t.Fatal("expected non-nil checkpoint")
	}
	// Should not contain self-reported data
	if containsString(cp.LogTailSummary, "Self-reported") {
		t.Error("should not contain self-reported data for invalid JSON")
	}
}

func TestTruncateToFit(t *testing.T) {
	s := "hello world this is a test string"
	result := truncateToFit(s, 10)
	if len(result) >= len(s) {
		t.Error("expected truncation")
	}

	// Zero excess should return original
	result = truncateToFit(s, 0)
	if result != s {
		t.Error("expected no truncation for zero excess")
	}

	// Empty string
	result = truncateToFit("", 10)
	if result != "" {
		t.Error("expected empty string")
	}

	// Large excess should return empty
	result = truncateToFit("short", 100)
	if result != "" {
		t.Errorf("expected empty string for large excess, got: %s", result)
	}
}

// --- helpers ---

func initGitRepo(t *testing.T, dir string) {
	t.Helper()
	gitExec(t, dir, "init")
	os.WriteFile(filepath.Join(dir, "README.md"), []byte("init"), 0o644)
	gitExec(t, dir, "add", "-A")
	gitExec(t, dir, "commit", "-m", "init")
}

func initGitRepoWithBranch(t *testing.T, dir string) {
	t.Helper()
	initGitRepoWithNamedBranch(t, dir, "dev")
}

func initGitRepoWithNamedBranch(t *testing.T, dir string, baseBranch string) {
	t.Helper()
	gitExec(t, dir, "init", "-b", baseBranch)
	os.WriteFile(filepath.Join(dir, "README.md"), []byte("init"), 0o644)
	gitExec(t, dir, "add", "-A")
	gitExec(t, dir, "commit", "-m", "init")
	gitExec(t, dir, "checkout", "-b", "feature/t1")
}

// gitExec is defined in spawner_test.go — reused here

func joinLines(lines []string) string {
	result := ""
	for _, l := range lines {
		result += l + "\n"
	}
	return result
}

func countOccurrences(s, substr string) int {
	count := 0
	idx := 0
	for {
		i := indexOf(s[idx:], substr)
		if i < 0 {
			break
		}
		count++
		idx += i + len(substr)
	}
	return count
}

func indexOf(s, substr string) int {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return i
		}
	}
	return -1
}

func containsString(s, substr string) bool {
	return indexOf(s, substr) >= 0
}
