package agent

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/MochaCosine1206/orchestra/internal/db"
)

func setupValidatorTest(t *testing.T) (*db.DB, string, string) {
	t.Helper()
	d := setupTestDB(t)
	repoRoot := t.TempDir()
	logsDir := filepath.Join(repoRoot, ".orchestra", "logs")
	os.MkdirAll(logsDir, 0o755)
	return d, repoRoot, logsDir
}

func TestValidateImplementerWithChanges(t *testing.T) {
	d, repoRoot, logsDir := setupValidatorTest(t)
	ctx := context.Background()

	// Set up git repo with worktree that has changes
	gitInit(t, repoRoot)
	wt := filepath.Join(repoRoot, ".worktree", "t1")
	gitExec(t, repoRoot, "worktree", "add", wt, "-b", "feature/t1")

	// Make a real change in worktree
	os.WriteFile(filepath.Join(wt, "new-file.go"), []byte("package main"), 0o644)
	gitExec(t, wt, "add", ".")
	gitExec(t, wt, "commit", "-m", "add new file")

	// Register agent and task
	d.RegisterAgent(ctx, db.Agent{ID: "a1", Role: "implementer", Status: "working"})
	d.CreateTask(ctx, db.Task{
		ID:     "t1",
		Title:  "Test task",
		Status: "running",
		Role:   "implementer",
	})
	d.AssignTask(ctx, "t1", "a1", wt, "feature/t1")

	result, err := ValidateTaskOutput(ctx, d, "t1", repoRoot, logsDir)
	if err != nil {
		t.Fatalf("ValidateTaskOutput: %v", err)
	}
	if !result.OK {
		t.Errorf("expected OK=true, got false: %s", result.Reason)
	}
	if result.ResultSummary == "" {
		t.Error("expected non-empty result summary")
	}
}

func TestValidateImplementerNoChanges(t *testing.T) {
	d, repoRoot, logsDir := setupValidatorTest(t)
	ctx := context.Background()

	gitInit(t, repoRoot)
	wt := filepath.Join(repoRoot, ".worktree", "t1")
	gitExec(t, repoRoot, "worktree", "add", wt, "-b", "feature/t1")

	// No changes made in worktree

	d.RegisterAgent(ctx, db.Agent{ID: "a1", Role: "implementer", Status: "working"})
	d.CreateTask(ctx, db.Task{ID: "t1", Title: "Test", Status: "running", Role: "implementer"})
	d.AssignTask(ctx, "t1", "a1", wt, "feature/t1")

	result, err := ValidateTaskOutput(ctx, d, "t1", repoRoot, logsDir)
	if err != nil {
		t.Fatalf("ValidateTaskOutput: %v", err)
	}
	if result.OK {
		t.Error("expected OK=false for no changes")
	}
	if result.Reason != "implementer_no_changes" {
		t.Errorf("expected reason 'implementer_no_changes', got %q", result.Reason)
	}
}

func TestValidateResearcherWithMarkdown(t *testing.T) {
	d, repoRoot, logsDir := setupValidatorTest(t)
	ctx := context.Background()

	gitInit(t, repoRoot)
	wt := filepath.Join(repoRoot, ".worktree", "t1")
	gitExec(t, repoRoot, "worktree", "add", wt, "-b", "feature/t1")

	// Create a quality markdown file
	md := strings.Repeat("Research content about important topics. ", 30) + "\n\n"
	md += "## Section 1\n\nContent for section 1.\n\n"
	md += "## Section 2\n\nContent for section 2.\n\n"
	md += "## Section 3\n\nMore content.\n\n"
	md += "Sources:\n- https://example.com/1\n- https://example.com/2\n- https://example.com/3\n"

	os.WriteFile(filepath.Join(wt, "research.md"), []byte(md), 0o644)
	gitExec(t, wt, "add", ".")
	gitExec(t, wt, "commit", "-m", "add research")

	d.RegisterAgent(ctx, db.Agent{ID: "a1", Role: "researcher", Status: "working"})
	d.CreateTask(ctx, db.Task{ID: "t1", Title: "Research", Status: "running", Role: "researcher"})
	d.AssignTask(ctx, "t1", "a1", wt, "feature/t1")

	result, err := ValidateTaskOutput(ctx, d, "t1", repoRoot, logsDir)
	if err != nil {
		t.Fatalf("ValidateTaskOutput: %v", err)
	}
	if !result.OK {
		t.Errorf("expected OK=true, got false: %s", result.Reason)
	}
}

func TestValidateResearcherNoCommits(t *testing.T) {
	d, repoRoot, logsDir := setupValidatorTest(t)
	ctx := context.Background()

	gitInit(t, repoRoot)
	wt := filepath.Join(repoRoot, ".worktree", "t1")
	gitExec(t, repoRoot, "worktree", "add", wt, "-b", "feature/t1")

	d.RegisterAgent(ctx, db.Agent{ID: "a1", Role: "researcher", Status: "working"})
	d.CreateTask(ctx, db.Task{ID: "t1", Title: "Research", Status: "running", Role: "researcher"})
	d.AssignTask(ctx, "t1", "a1", wt, "feature/t1")

	result, err := ValidateTaskOutput(ctx, d, "t1", repoRoot, logsDir)
	if err != nil {
		t.Fatalf("ValidateTaskOutput: %v", err)
	}
	if result.OK {
		t.Error("expected OK=false for no commits")
	}
	if result.Reason != "researcher_no_commits" {
		t.Errorf("expected reason 'researcher_no_commits', got %q", result.Reason)
	}
}

func TestValidateResearcherShortMarkdown(t *testing.T) {
	d, repoRoot, logsDir := setupValidatorTest(t)
	ctx := context.Background()

	gitInit(t, repoRoot)
	wt := filepath.Join(repoRoot, ".worktree", "t1")
	gitExec(t, repoRoot, "worktree", "add", wt, "-b", "feature/t1")

	// Short markdown (< 500 chars)
	os.WriteFile(filepath.Join(wt, "research.md"), []byte("# Short\n\nToo short"), 0o644)
	gitExec(t, wt, "add", ".")
	gitExec(t, wt, "commit", "-m", "add short research")

	d.RegisterAgent(ctx, db.Agent{ID: "a1", Role: "researcher", Status: "working"})
	d.CreateTask(ctx, db.Task{ID: "t1", Title: "Research", Status: "running", Role: "researcher"})
	d.AssignTask(ctx, "t1", "a1", wt, "feature/t1")

	result, err := ValidateTaskOutput(ctx, d, "t1", repoRoot, logsDir)
	if err != nil {
		t.Fatalf("ValidateTaskOutput: %v", err)
	}
	if result.OK {
		t.Error("expected OK=false for short markdown")
	}
}

func TestValidateScoutWithResult(t *testing.T) {
	d, repoRoot, logsDir := setupValidatorTest(t)
	ctx := context.Background()

	// Create log file with a result
	resultJSON := `{"type":"result","subtype":"success","result":"` + strings.Repeat("x", 150) + `"}`
	os.WriteFile(filepath.Join(logsDir, "t1.jsonl"), []byte(resultJSON+"\n"), 0o644)

	d.RegisterAgent(ctx, db.Agent{ID: "a1", Role: "scout", Status: "working"})
	d.CreateTask(ctx, db.Task{ID: "t1", Title: "Scout", Status: "running", Role: "scout"})
	d.AssignTask(ctx, "t1", "a1", "", "")

	result, err := ValidateTaskOutput(ctx, d, "t1", repoRoot, logsDir)
	if err != nil {
		t.Fatalf("ValidateTaskOutput: %v", err)
	}
	if !result.OK {
		t.Errorf("expected OK=true, got false: %s", result.Reason)
	}
}

func TestValidateScoutShortResult(t *testing.T) {
	d, repoRoot, logsDir := setupValidatorTest(t)
	ctx := context.Background()

	// Short result
	os.WriteFile(filepath.Join(logsDir, "t1.jsonl"), []byte(`{"type":"result","result":"short"}`+"\n"), 0o644)

	d.RegisterAgent(ctx, db.Agent{ID: "a1", Role: "scout", Status: "working"})
	d.CreateTask(ctx, db.Task{ID: "t1", Title: "Scout", Status: "running", Role: "scout"})
	d.AssignTask(ctx, "t1", "a1", "", "")

	result, err := ValidateTaskOutput(ctx, d, "t1", repoRoot, logsDir)
	if err != nil {
		t.Fatalf("ValidateTaskOutput: %v", err)
	}
	if result.OK {
		t.Error("expected OK=false for short result")
	}
	if result.Reason != "scout_result_too_short" {
		t.Errorf("expected reason 'scout_result_too_short', got %q", result.Reason)
	}
}

func TestValidateReviewerWithResult(t *testing.T) {
	d, repoRoot, logsDir := setupValidatorTest(t)
	ctx := context.Background()

	resultJSON := `{"type":"result","subtype":"success","result":"` + strings.Repeat("y", 150) + `"}`
	os.WriteFile(filepath.Join(logsDir, "t1.jsonl"), []byte(resultJSON+"\n"), 0o644)

	d.RegisterAgent(ctx, db.Agent{ID: "a1", Role: "reviewer", Status: "working"})
	d.CreateTask(ctx, db.Task{ID: "t1", Title: "Review", Status: "running", Role: "reviewer"})
	d.AssignTask(ctx, "t1", "a1", "", "")

	result, err := ValidateTaskOutput(ctx, d, "t1", repoRoot, logsDir)
	if err != nil {
		t.Fatalf("ValidateTaskOutput: %v", err)
	}
	if !result.OK {
		t.Errorf("expected OK=true, got false: %s", result.Reason)
	}
}

func TestValidateReviewerNoOutput(t *testing.T) {
	d, repoRoot, logsDir := setupValidatorTest(t)
	ctx := context.Background()

	d.RegisterAgent(ctx, db.Agent{ID: "a1", Role: "reviewer", Status: "working"})
	d.CreateTask(ctx, db.Task{ID: "t1", Title: "Review", Status: "running", Role: "reviewer"})
	d.AssignTask(ctx, "t1", "a1", "", "")

	result, err := ValidateTaskOutput(ctx, d, "t1", repoRoot, logsDir)
	if err != nil {
		t.Fatalf("ValidateTaskOutput: %v", err)
	}
	if result.OK {
		t.Error("expected OK=false for no output")
	}
}

func TestValidateTaskNotFound(t *testing.T) {
	d, _, logsDir := setupValidatorTest(t)
	ctx := context.Background()

	_, err := ValidateTaskOutput(ctx, d, "nonexistent", "/fake", logsDir)
	if err == nil {
		t.Error("expected error for missing task")
	}
}

func TestExtractResultFromLog(t *testing.T) {
	dir := t.TempDir()
	logFile := filepath.Join(dir, "t1.jsonl")

	content := `{"type":"init","session":"abc"}
{"type":"assistant","content":"working..."}
{"type":"result","subtype":"success","result":"All done!"}
`
	os.WriteFile(logFile, []byte(content), 0o644)

	result := extractResultFromLog(dir, "t1")
	if !strings.Contains(result, `"type":"result"`) {
		t.Errorf("expected result line, got %q", result)
	}
}

func TestExtractResultFromLogMissing(t *testing.T) {
	dir := t.TempDir()
	result := extractResultFromLog(dir, "nonexistent")
	if result != "" {
		t.Errorf("expected empty result for missing log, got %q", result)
	}
}

func TestFilterChanges(t *testing.T) {
	stat := ` .orchestra/profiles/scout.json | 5 ++
 .mcp.json | 2 ++
 src/main.go | 10 ++++
 src/util.go | 3 +++
 3 files changed, 20 insertions(+)`

	filtered := filterChanges(stat)
	if strings.Contains(filtered, ".orchestra/") {
		t.Error("expected .orchestra/ to be filtered out")
	}
	if strings.Contains(filtered, ".claude/") {
		t.Error("expected .claude/ to be filtered out")
	}
	if strings.Contains(filtered, ".mcp.json") {
		t.Error("expected .mcp.json to be filtered out")
	}
	if !strings.Contains(filtered, "src/main.go") {
		t.Error("expected src/main.go to remain")
	}
}

func TestTruncate(t *testing.T) {
	short := "hello"
	if truncate(short, 10) != "hello" {
		t.Error("expected no truncation")
	}

	long := strings.Repeat("x", 100)
	result := truncate(long, 50)
	if len(result) != 50 {
		t.Errorf("expected 50 chars, got %d", len(result))
	}
}

// --- Step 2: Validator False Positive Tests ---

func TestValidateResearcherInternalAuditNoURLs(t *testing.T) {
	d, repoRoot, logsDir := setupValidatorTest(t)
	ctx := context.Background()

	gitInit(t, repoRoot)
	wt := filepath.Join(repoRoot, ".worktree", "t1")
	gitExec(t, repoRoot, "worktree", "add", wt, "-b", "feature/t1")

	// Create markdown with code fences but no URLs (internal audit output)
	md := strings.Repeat("Internal codebase audit findings and analysis. ", 25) + "\n\n"
	md += "## Gap Analysis\n\nFindings from the internal audit.\n\n"
	md += "## Recommendations\n\nCode improvements needed.\n\n"
	md += "## Implementation\n\n```go\nfunc example() {}\n```\n"

	os.WriteFile(filepath.Join(wt, "audit.md"), []byte(md), 0o644)
	gitExec(t, wt, "add", ".")
	gitExec(t, wt, "commit", "-m", "add internal audit")

	d.RegisterAgent(ctx, db.Agent{ID: "a1", Role: "researcher", Status: "working"})
	d.CreateTask(ctx, db.Task{
		ID:          "t1",
		Title:       "Internal Audit",
		Description: sql.NullString{String: "Audit the internal codebase for gaps", Valid: true},
		Status:      "running",
		Role:        "researcher",
	})
	d.AssignTask(ctx, "t1", "a1", wt, "feature/t1")

	result, err := ValidateTaskOutput(ctx, d, "t1", repoRoot, logsDir)
	if err != nil {
		t.Fatalf("ValidateTaskOutput: %v", err)
	}
	if !result.OK {
		t.Errorf("expected OK=true for internal audit (no URLs required), got false: %s", result.Reason)
	}
}

func TestValidateResearcherExternalResearchStillRequiresURLs(t *testing.T) {
	d, repoRoot, logsDir := setupValidatorTest(t)
	ctx := context.Background()

	gitInit(t, repoRoot)
	wt := filepath.Join(repoRoot, ".worktree", "t1")
	gitExec(t, repoRoot, "worktree", "add", wt, "-b", "feature/t1")

	// Create markdown without URLs and without internal keywords or code fences
	md := strings.Repeat("Research on distributed systems and coordination. ", 25) + "\n\n"
	md += "## Background\n\nSome background information.\n\n"
	md += "## Findings\n\nSome findings from the research.\n\n"
	md += "## Conclusion\n\nFinal thoughts.\n"

	os.WriteFile(filepath.Join(wt, "research.md"), []byte(md), 0o644)
	gitExec(t, wt, "add", ".")
	gitExec(t, wt, "commit", "-m", "add research")

	d.RegisterAgent(ctx, db.Agent{ID: "a1", Role: "researcher", Status: "working"})
	d.CreateTask(ctx, db.Task{
		ID:          "t1",
		Title:       "Research distributed systems",
		Description: sql.NullString{String: "Research state of the art in distributed consensus", Valid: true},
		Status:      "running",
		Role:        "researcher",
	})
	d.AssignTask(ctx, "t1", "a1", wt, "feature/t1")

	result, err := ValidateTaskOutput(ctx, d, "t1", repoRoot, logsDir)
	if err != nil {
		t.Fatalf("ValidateTaskOutput: %v", err)
	}
	if result.OK {
		t.Error("expected OK=false for external research without URLs")
	}
	if result.Reason != "researcher_insufficient_urls" {
		t.Errorf("expected reason 'researcher_insufficient_urls', got %q", result.Reason)
	}
}

func TestValidateImplementerPartialWork(t *testing.T) {
	d, repoRoot, logsDir := setupValidatorTest(t)
	ctx := context.Background()

	gitInit(t, repoRoot)
	wt := filepath.Join(repoRoot, ".worktree", "t1")
	gitExec(t, repoRoot, "worktree", "add", wt, "-b", "feature/t1")

	// Register agent and task first (file_locks FK references tasks)
	d.RegisterAgent(ctx, db.Agent{ID: "a1", Role: "implementer", Status: "working"})
	d.CreateTask(ctx, db.Task{ID: "t1", Title: "Add DB fields", Status: "running", Role: "implementer"})
	d.AssignTask(ctx, "t1", "a1", wt, "feature/t1")

	// Create file locks for 3 files
	d.CreateFileLock(ctx, "internal/db/models.go", "", "t1", time.Time{})
	d.CreateFileLock(ctx, "internal/db/queries.go", "", "t1", time.Time{})
	d.CreateFileLock(ctx, "internal/db/mutations.go", "", "t1", time.Time{})

	// Only modify 1 of the 3 owned files
	os.MkdirAll(filepath.Join(wt, "internal", "db"), 0o755)
	os.WriteFile(filepath.Join(wt, "internal", "db", "models.go"), []byte("package db\n// modified"), 0o644)
	gitExec(t, wt, "add", ".")
	gitExec(t, wt, "commit", "-m", "partial work")

	result, err := ValidateTaskOutput(ctx, d, "t1", repoRoot, logsDir)
	if err != nil {
		t.Fatalf("ValidateTaskOutput: %v", err)
	}
	if result.OK {
		t.Error("expected OK=false for partial work (1/3 files modified)")
	}
	if result.Reason != "implementer_partial_work" {
		t.Errorf("expected reason 'implementer_partial_work', got %q", result.Reason)
	}
	if !strings.Contains(result.ResultSummary, "queries.go") {
		t.Error("expected ResultSummary to mention unmodified files")
	}
}

func TestValidateImplementerAllFilesModified(t *testing.T) {
	d, repoRoot, logsDir := setupValidatorTest(t)
	ctx := context.Background()

	gitInit(t, repoRoot)
	wt := filepath.Join(repoRoot, ".worktree", "t1")
	gitExec(t, repoRoot, "worktree", "add", wt, "-b", "feature/t1")

	// Register agent and task first (file_locks FK references tasks)
	d.RegisterAgent(ctx, db.Agent{ID: "a1", Role: "implementer", Status: "working"})
	d.CreateTask(ctx, db.Task{ID: "t1", Title: "Add DB fields", Status: "running", Role: "implementer"})
	d.AssignTask(ctx, "t1", "a1", wt, "feature/t1")

	// Create file locks for 2 files
	d.CreateFileLock(ctx, "internal/db/models.go", "", "t1", time.Time{})
	d.CreateFileLock(ctx, "internal/db/queries.go", "", "t1", time.Time{})

	// Modify ALL owned files
	os.MkdirAll(filepath.Join(wt, "internal", "db"), 0o755)
	os.WriteFile(filepath.Join(wt, "internal", "db", "models.go"), []byte("package db\n// models modified"), 0o644)
	os.WriteFile(filepath.Join(wt, "internal", "db", "queries.go"), []byte("package db\n// queries modified"), 0o644)
	gitExec(t, wt, "add", ".")
	gitExec(t, wt, "commit", "-m", "complete work")

	result, err := ValidateTaskOutput(ctx, d, "t1", repoRoot, logsDir)
	if err != nil {
		t.Fatalf("ValidateTaskOutput: %v", err)
	}
	if !result.OK {
		t.Errorf("expected OK=true when all files modified, got false: %s", result.Reason)
	}
}

func TestValidateImplementerNoLocksStillPasses(t *testing.T) {
	// When no file locks exist, skip the completeness check (backward compat)
	d, repoRoot, logsDir := setupValidatorTest(t)
	ctx := context.Background()

	gitInit(t, repoRoot)
	wt := filepath.Join(repoRoot, ".worktree", "t1")
	gitExec(t, repoRoot, "worktree", "add", wt, "-b", "feature/t1")

	// No file locks created — just make some changes
	os.WriteFile(filepath.Join(wt, "new-file.go"), []byte("package main"), 0o644)
	gitExec(t, wt, "add", ".")
	gitExec(t, wt, "commit", "-m", "add file")

	d.RegisterAgent(ctx, db.Agent{ID: "a1", Role: "implementer", Status: "working"})
	d.CreateTask(ctx, db.Task{ID: "t1", Title: "Test task", Status: "running", Role: "implementer"})
	d.AssignTask(ctx, "t1", "a1", wt, "feature/t1")

	result, err := ValidateTaskOutput(ctx, d, "t1", repoRoot, logsDir)
	if err != nil {
		t.Fatalf("ValidateTaskOutput: %v", err)
	}
	if !result.OK {
		t.Errorf("expected OK=true with no locks, got false: %s", result.Reason)
	}
}

func TestExtractResultSummary(t *testing.T) {
	dir := t.TempDir()
	logsDir := filepath.Join(dir, ".orchestra", "logs")
	os.MkdirAll(logsDir, 0o755)

	resultJSON := `{"type":"result","subtype":"success","result":"scout found stuff"}`
	os.WriteFile(filepath.Join(logsDir, "t1.jsonl"), []byte(resultJSON+"\n"), 0o644)

	summary := ExtractResultSummary(RoleScout, "", "dev", logsDir, "t1")
	if summary == "" {
		t.Error("expected non-empty summary for scout")
	}
}

// --- B-207: extractSkipReasons tests ---

func TestExtractSkipReasons_FileReadButNotModified(t *testing.T) {
	dir := t.TempDir()
	logContent := `{"type":"tool_use","name":"Read","input":{"file_path":"/worktree/internal/agent/spawner.go"}}
{"type":"tool_result","content":"package agent\nfunc SpawnAgent..."}
{"type":"assistant","content":"The spawner looks correct, no changes needed."}
`
	os.WriteFile(filepath.Join(dir, "t1.jsonl"), []byte(logContent), 0o644)

	unmodified := []string{"internal/agent/spawner.go", "internal/orchestrator/iterative.go"}
	reports := extractSkipReasons(dir, "t1", unmodified)

	if len(reports) != 2 {
		t.Fatalf("expected 2 skip reports, got %d", len(reports))
	}

	// spawner.go was mentioned in the log
	if reports[0].FilePath != "internal/agent/spawner.go" {
		t.Errorf("reports[0].FilePath = %q, want spawner.go", reports[0].FilePath)
	}
	if !strings.Contains(reports[0].Reason, "read but not modified") {
		t.Errorf("expected 'read but not modified' reason for accessed file, got %q", reports[0].Reason)
	}

	// iterative.go was NOT mentioned in the log
	if reports[1].FilePath != "internal/orchestrator/iterative.go" {
		t.Errorf("reports[1].FilePath = %q, want iterative.go", reports[1].FilePath)
	}
	if !strings.Contains(reports[1].Reason, "not accessed") {
		t.Errorf("expected 'not accessed' reason for unaccessed file, got %q", reports[1].Reason)
	}
}

func TestExtractSkipReasons_NoLogFile(t *testing.T) {
	dir := t.TempDir()
	reports := extractSkipReasons(dir, "nonexistent", []string{"a.go"})
	if len(reports) != 0 {
		t.Errorf("expected no reports when log missing, got %d", len(reports))
	}
}

func TestExtractSkipReasons_EmptyUnmodified(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "t1.jsonl"), []byte("{}"), 0o644)
	reports := extractSkipReasons(dir, "t1", nil)
	if len(reports) != 0 {
		t.Errorf("expected no reports for nil unmodified, got %d", len(reports))
	}
}

// --- E.3: runBuildAndTest Tests (G112) ---

func TestRunBuildAndTest_BuildFails(t *testing.T) {
	dir := t.TempDir()
	// Create a minimal Go module with a syntax error
	os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module testproject\n\ngo 1.21\n"), 0o644)
	os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\n\nfunc main() {\n\tfmt.Println(\"hello\"\n}\n"), 0o644)

	ok, output := runBuildAndTest(dir, "")
	if ok {
		t.Error("expected build to fail with syntax error")
	}
	if !strings.Contains(output, "go build failed") {
		t.Errorf("expected 'go build failed' in output, got: %s", output)
	}
}

func TestRunBuildAndTest_TestFails(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module testproject\n\ngo 1.21\n"), 0o644)
	os.WriteFile(filepath.Join(dir, "lib.go"), []byte("package testproject\n\nfunc Add(a, b int) int { return a + b }\n"), 0o644)
	os.WriteFile(filepath.Join(dir, "lib_test.go"), []byte("package testproject\n\nimport \"testing\"\n\nfunc TestAdd(t *testing.T) {\n\tif Add(1, 2) != 4 {\n\t\tt.Error(\"expected 4\")\n\t}\n}\n"), 0o644)

	ok, output := runBuildAndTest(dir, "")
	if ok {
		t.Error("expected test to fail")
	}
	if !strings.Contains(output, "go test failed") {
		t.Errorf("expected 'go test failed' in output, got: %s", output)
	}
}

func TestRunBuildAndTest_BuildAndTestPass(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module testproject\n\ngo 1.21\n"), 0o644)
	os.WriteFile(filepath.Join(dir, "lib.go"), []byte("package testproject\n\nfunc Add(a, b int) int { return a + b }\n"), 0o644)
	os.WriteFile(filepath.Join(dir, "lib_test.go"), []byte("package testproject\n\nimport \"testing\"\n\nfunc TestAdd(t *testing.T) {\n\tif Add(1, 2) != 3 {\n\t\tt.Error(\"expected 3\")\n\t}\n}\n"), 0o644)

	ok, output := runBuildAndTest(dir, "")
	if !ok {
		t.Errorf("expected build+test to pass, got failure: %s", output)
	}
}

func TestRunBuildAndTest_OverrideCmd(t *testing.T) {
	dir := t.TempDir()
	// No go.mod — would skip default verification. But override should still run.
	os.WriteFile(filepath.Join(dir, "hello.txt"), []byte("world"), 0o644)

	// Override with a passing command
	ok, _ := runBuildAndTest(dir, "test -f hello.txt")
	if !ok {
		t.Error("expected override command to pass")
	}

	// Override with a failing command
	ok, output := runBuildAndTest(dir, "test -f nonexistent.txt")
	if ok {
		t.Error("expected override command to fail")
	}
	if !strings.Contains(output, "test_cmd failed") {
		t.Errorf("expected 'test_cmd failed' in output, got: %s", output)
	}
}

func TestValidateImplementerPartialWork_WithSkipReports(t *testing.T) {
	d, repoRoot, logsDir := setupValidatorTest(t)
	ctx := context.Background()

	gitInit(t, repoRoot)
	wt := filepath.Join(repoRoot, ".worktree", "t1")
	gitExec(t, repoRoot, "worktree", "add", wt, "-b", "feature/t1")

	d.RegisterAgent(ctx, db.Agent{ID: "a1", Role: "implementer", Status: "working"})
	d.CreateTask(ctx, db.Task{ID: "t1", Title: "Test", Status: "running", Role: "implementer"})
	d.AssignTask(ctx, "t1", "a1", wt, "feature/t1")

	d.CreateFileLock(ctx, "internal/db/models.go", "", "t1", time.Time{})
	d.CreateFileLock(ctx, "internal/agent/spawner.go", "", "t1", time.Time{})

	// Only modify models.go
	os.MkdirAll(filepath.Join(wt, "internal", "db"), 0o755)
	os.WriteFile(filepath.Join(wt, "internal", "db", "models.go"), []byte("package db\n// modified"), 0o644)
	gitExec(t, wt, "add", ".")
	gitExec(t, wt, "commit", "-m", "partial")

	// Write a log file that shows spawner.go was read
	logContent := `{"type":"tool_use","name":"Read","input":{"file_path":"spawner.go"}}
{"type":"tool_result","content":"package agent"}
`
	os.WriteFile(filepath.Join(logsDir, "t1.jsonl"), []byte(logContent), 0o644)

	result, err := ValidateTaskOutput(ctx, d, "t1", repoRoot, logsDir)
	if err != nil {
		t.Fatalf("ValidateTaskOutput: %v", err)
	}
	if result.OK {
		t.Error("expected OK=false for partial work")
	}
	if result.Reason != "implementer_partial_work" {
		t.Errorf("expected partial_work, got %q", result.Reason)
	}
	if len(result.SkipReports) != 1 {
		t.Fatalf("expected 1 skip report, got %d", len(result.SkipReports))
	}
	if result.SkipReports[0].FilePath != "internal/agent/spawner.go" {
		t.Errorf("skip report file = %q, want spawner.go", result.SkipReports[0].FilePath)
	}
}
