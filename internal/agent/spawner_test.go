package agent

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/MochaCosine1206/orchestra/internal/db"
)

func setupSpawnerTestDirect(t *testing.T) (*Spawner, *db.DB, string) {
	t.Helper()
	d := setupTestDB(t)
	repoRoot := t.TempDir()
	logsDir := filepath.Join(repoRoot, ".orchestra", "logs")
	pidsDir := filepath.Join(repoRoot, ".orchestra", "pids")
	os.MkdirAll(logsDir, 0o755)
	os.MkdirAll(pidsDir, 0o755)

	agentDir := filepath.Join(repoRoot, ".claude", "agents")
	os.MkdirAll(agentDir, 0o755)
	os.WriteFile(filepath.Join(agentDir, "implementer.md"), []byte("---\nname: Implementer\nmodel: opus\n---\nYou are an implementer."), 0o644)
	os.WriteFile(filepath.Join(agentDir, "scout.md"), []byte("---\nname: Scout\nmodel: opus\n---\nYou are a scout."), 0o644)

	// Init git repo
	gitInit(t, repoRoot)

	s := &Spawner{
		DB:       d,
		RepoRoot: repoRoot,
		LogsDir:  logsDir,
		PidsDir:  pidsDir,
		SpawnCmd: "echo",
	}
	return s, d, repoRoot
}

func gitInit(t *testing.T, dir string) {
	t.Helper()
	gitExec(t, dir, "init")
	gitExec(t, dir, "checkout", "-b", "dev")
	os.WriteFile(filepath.Join(dir, "README.md"), []byte("test"), 0o644)
	gitExec(t, dir, "add", ".")
	gitExec(t, dir, "commit", "-m", "init")
}

func gitExec(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=test",
		"GIT_AUTHOR_EMAIL=test@test.com",
		"GIT_COMMITTER_NAME=test",
		"GIT_COMMITTER_EMAIL=test@test.com",
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v: %s", args, err, string(out))
	}
}

func TestSpawnerRunMock(t *testing.T) {
	s, d, _ := setupSpawnerTestDirect(t)
	ctx := context.Background()

	err := d.CreateTask(ctx, db.Task{
		ID:     "t1",
		Title:  "Test task",
		Status: "pending",
		Role:   "implementer",
	})
	if err != nil {
		t.Fatalf("creating task: %v", err)
	}

	result, err := s.Run(ctx, SpawnOpts{
		TaskID: "t1",
		Role:   RoleImplementer,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if result.AgentID == "" {
		t.Error("expected non-empty agent ID")
	}
	if result.PID == 0 {
		t.Error("expected non-zero PID")
	}
	if result.Worktree == "" {
		t.Error("expected non-empty worktree")
	}
	if result.Model != "claude-opus-4-6[1m]" {
		t.Errorf("expected opus model, got %s", result.Model)
	}

	// Verify task was started
	task, _ := d.GetTaskByID(ctx, "t1")
	if task.Status != "running" {
		t.Errorf("expected status 'running', got %s", task.Status)
	}

	// Verify PID file
	pid, _ := ReadPID(s.PidsDir, "t1")
	if pid == 0 {
		t.Error("expected PID file to exist")
	}

	// Verify blackboard entries
	timeout, _ := d.GetBlackboardValue(ctx, "timeout:t1")
	if timeout == "" {
		t.Error("expected timeout in blackboard")
	}
	spawnTime, _ := d.GetBlackboardValue(ctx, "spawn_time:t1")
	if spawnTime == "" {
		t.Error("expected spawn_time in blackboard")
	}
}

func TestSpawnSkipsDuringGlobalCooldown(t *testing.T) {
	s, d, _ := setupSpawnerTestDirect(t)
	ctx := context.Background()

	// Set a global cooldown far in the future.
	futureEpoch := time.Now().Unix() + 3600 // 1 hour from now
	d.SetBlackboard(ctx, "conductor:rate_limit_cooldown_until",
		strconv.FormatInt(futureEpoch, 10), "test")

	d.CreateTask(ctx, db.Task{
		ID:     "t1",
		Title:  "Test task",
		Status: "pending",
		Role:   "implementer",
	})

	_, err := s.Run(ctx, SpawnOpts{TaskID: "t1", Role: RoleImplementer})
	if err == nil {
		t.Fatal("expected error when global cooldown is active")
	}
	if !strings.Contains(err.Error(), "global rate limit cooldown") {
		t.Errorf("expected cooldown error, got: %v", err)
	}
}

func TestSpawnAllowedAfterCooldownExpires(t *testing.T) {
	s, d, _ := setupSpawnerTestDirect(t)
	ctx := context.Background()

	// Set a cooldown in the past — should NOT block.
	pastEpoch := time.Now().Unix() - 60
	d.SetBlackboard(ctx, "conductor:rate_limit_cooldown_until",
		strconv.FormatInt(pastEpoch, 10), "test")

	d.CreateTask(ctx, db.Task{
		ID:     "t1",
		Title:  "Test task",
		Status: "pending",
		Role:   "implementer",
	})

	result, err := s.Run(ctx, SpawnOpts{TaskID: "t1", Role: RoleImplementer})
	if err != nil {
		t.Fatalf("Run should succeed after cooldown expires: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}
}

func TestSpawnerRunTaskNotPending(t *testing.T) {
	s, d, _ := setupSpawnerTestDirect(t)
	ctx := context.Background()

	d.CreateTask(ctx, db.Task{
		ID:     "t1",
		Title:  "Running task",
		Status: "running",
		Role:   "implementer",
	})

	_, err := s.Run(ctx, SpawnOpts{TaskID: "t1", Role: RoleImplementer})
	if err == nil {
		t.Error("expected error for non-pending task")
	}
}

func TestSpawnerRunTaskNotFound(t *testing.T) {
	s, _, _ := setupSpawnerTestDirect(t)
	ctx := context.Background()

	_, err := s.Run(ctx, SpawnOpts{TaskID: "nonexistent", Role: RoleImplementer})
	if err == nil {
		t.Error("expected error for missing task")
	}
}

func TestSpawnerRunDedupGuard(t *testing.T) {
	s, d, _ := setupSpawnerTestDirect(t)
	ctx := context.Background()

	d.RegisterAgent(ctx, db.Agent{ID: "a1", Role: "implementer", Status: "idle"})
	d.CreateTask(ctx, db.Task{
		ID:         "t1",
		Title:      "Already assigned",
		Status:     "pending",
		Role:       "implementer",
		AssignedTo: sql.NullString{String: "a1", Valid: true},
	})

	_, err := s.Run(ctx, SpawnOpts{TaskID: "t1", Role: RoleImplementer})
	if err == nil {
		t.Error("expected error for already-assigned task")
	}
}

func TestSpawnerRunCustomModel(t *testing.T) {
	s, d, _ := setupSpawnerTestDirect(t)
	ctx := context.Background()

	d.CreateTask(ctx, db.Task{
		ID:     "t1",
		Title:  "Test task",
		Status: "pending",
		Role:   "scout",
	})

	result, err := s.Run(ctx, SpawnOpts{
		TaskID: "t1",
		Role:   RoleScout,
		Model:  "claude-haiku-4-5-20251001",
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.Model != "claude-haiku-4-5-20251001" {
		t.Errorf("expected custom model, got %s", result.Model)
	}
}

func TestSpawnerRunFallbackModel(t *testing.T) {
	s, d, _ := setupSpawnerTestDirect(t)
	ctx := context.Background()

	d.SetBlackboard(ctx, "fallback_model:t1", "claude-sonnet-4-5-20250929", "test")
	d.CreateTask(ctx, db.Task{
		ID:     "t1",
		Title:  "Test task",
		Status: "pending",
		Role:   "implementer",
	})

	result, err := s.Run(ctx, SpawnOpts{TaskID: "t1", Role: RoleImplementer})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.Model != "claude-sonnet-4-5-20250929" {
		t.Errorf("expected fallback model sonnet, got %s", result.Model)
	}
}

func TestSpawnerRespawnCircuitBreaker(t *testing.T) {
	s, d, _ := setupSpawnerTestDirect(t)
	ctx := context.Background()

	d.CreateTask(ctx, db.Task{
		ID:     "t1",
		Title:  "Test task",
		Status: "failed",
		Role:   "implementer",
	})
	d.SetBlackboard(ctx, "retry:t1", "3", "test")

	_, err := s.Respawn(ctx, "t1")
	if err == nil {
		t.Error("expected circuit breaker error")
	}
}

func TestSpawnerRespawnInfraBypass(t *testing.T) {
	s, d, _ := setupSpawnerTestDirect(t)
	ctx := context.Background()

	d.CreateTask(ctx, db.Task{
		ID:     "t1",
		Title:  "Test task",
		Status: "failed",
		Role:   "implementer",
	})
	d.SetBlackboard(ctx, "failure_type:t1", "rate_limit", "test")
	d.SetBlackboard(ctx, "retry:t1", "0", "test")

	result, err := s.Respawn(ctx, "t1")
	if err != nil {
		t.Fatalf("Respawn: %v", err)
	}
	if result == nil {
		t.Fatal("expected spawn result")
	}

	// Retry count should still be 0 (infra bypass)
	retryStr, _ := d.GetBlackboardValue(ctx, "retry:t1")
	if retryStr != "0" {
		t.Errorf("expected retry count 0 for infra failure, got %s", retryStr)
	}
}

func TestSpawnerBatch(t *testing.T) {
	s, d, _ := setupSpawnerTestDirect(t)
	ctx := context.Background()

	for i := 1; i <= 3; i++ {
		d.CreateTask(ctx, db.Task{
			ID:     fmt.Sprintf("t%d", i),
			Title:  fmt.Sprintf("Task %d", i),
			Status: "pending",
			Role:   "implementer",
		})
	}

	results, err := s.Batch(ctx, 5, 0, nil)
	if err != nil {
		t.Fatalf("Batch: %v", err)
	}
	if len(results) != 3 {
		t.Errorf("expected 3 results, got %d", len(results))
	}
}

func TestSpawnerBatchRespectsConcurrency(t *testing.T) {
	s, d, _ := setupSpawnerTestDirect(t)
	ctx := context.Background()

	d.CreateTask(ctx, db.Task{ID: "t-running", Title: "Running", Status: "running", Role: "implementer"})
	d.CreateTask(ctx, db.Task{ID: "t1", Title: "Pending 1", Status: "pending", Role: "implementer"})
	d.CreateTask(ctx, db.Task{ID: "t2", Title: "Pending 2", Status: "pending", Role: "implementer"})

	results, err := s.Batch(ctx, 2, 0, nil)
	if err != nil {
		t.Fatalf("Batch: %v", err)
	}
	// maxConcurrent=2, 1 active → only 1 slot
	if len(results) != 1 {
		t.Errorf("expected 1 result (1 slot), got %d", len(results))
	}
}

func TestSpawnerResumeMissingSession(t *testing.T) {
	s, d, _ := setupSpawnerTestDirect(t)
	ctx := context.Background()

	d.CreateTask(ctx, db.Task{ID: "t1", Title: "Test", Status: "running", Role: "implementer"})

	_, err := s.Resume(ctx, "t1")
	if err == nil {
		t.Error("expected error for missing session_id")
	}
}

func TestSpawnerResumeWithSession(t *testing.T) {
	s, d, _ := setupSpawnerTestDirect(t)
	ctx := context.Background()

	d.RegisterAgent(ctx, db.Agent{ID: "a1", Role: "implementer", Status: "working"})
	d.CreateTask(ctx, db.Task{ID: "t1", Title: "Test", Status: "running", Role: "implementer"})
	d.AssignTask(ctx, "t1", "a1", filepath.Join(s.RepoRoot, ".worktree", "t1"), "feature/t1")
	d.SetBlackboard(ctx, "session_id:t1", "sess-abc-123", "test")

	wt := filepath.Join(s.RepoRoot, ".worktree", "t1")
	os.MkdirAll(wt, 0o755)

	result, err := s.Resume(ctx, "t1")
	if err != nil {
		t.Fatalf("Resume: %v", err)
	}
	if result.AgentID != "a1" {
		t.Errorf("expected agent a1, got %s", result.AgentID)
	}
}

func TestSpawnerBatchEmpty(t *testing.T) {
	s, _, _ := setupSpawnerTestDirect(t)
	ctx := context.Background()

	results, err := s.Batch(ctx, 5, 0, nil)
	if err != nil {
		t.Fatalf("Batch: %v", err)
	}
	if results != nil {
		t.Errorf("expected nil results for empty batch, got %d", len(results))
	}
}

func TestSpawnerBatchNoSlots(t *testing.T) {
	s, d, _ := setupSpawnerTestDirect(t)
	ctx := context.Background()

	// Fill up all slots with running tasks
	d.CreateTask(ctx, db.Task{ID: "t-running1", Title: "Running", Status: "running", Role: "implementer"})
	d.CreateTask(ctx, db.Task{ID: "t-running2", Title: "Running 2", Status: "running", Role: "scout"})
	d.CreateTask(ctx, db.Task{ID: "t-pending", Title: "Pending", Status: "pending", Role: "implementer"})

	results, err := s.Batch(ctx, 2, 0, nil)
	if err != nil {
		t.Fatalf("Batch: %v", err)
	}
	if results != nil {
		t.Errorf("expected nil results when no slots, got %d", len(results))
	}
}

func TestSpawnerGenerateSpec(t *testing.T) {
	s, d, _ := setupSpawnerTestDirect(t)
	ctx := context.Background()

	d.CreateTask(ctx, db.Task{
		ID:          "t1",
		Title:       "My Task",
		Description: sql.NullString{String: "Do something cool", Valid: true},
		Status:      "pending",
		Role:        "implementer",
	})

	spec, err := s.generateSpec(ctx, "t1")
	if err != nil {
		t.Fatalf("generateSpec: %v", err)
	}
	if spec == "" {
		t.Error("expected non-empty spec")
	}
	// Verify Go-native output contains overview table + description
	if !strings.Contains(spec, "## Overview") {
		t.Error("expected overview table in spec")
	}
	if !strings.Contains(spec, "| **Task ID** | `t1` |") {
		t.Error("expected task ID in overview table")
	}
	if !strings.Contains(spec, "Do something cool") {
		t.Error("expected description in spec")
	}
}

// --- Refinement Tests ---

func TestExtractCritiqueFromLogResult(t *testing.T) {
	dir := t.TempDir()
	logFile := filepath.Join(dir, "test.jsonl")

	content := `{"type":"init","session_id":"abc"}
{"type":"assistant","content":"analyzing..."}
{"type":"result","subtype":"success","result":"The agent failed to commit its changes. It wrote files but never ran git add/commit."}
`
	os.WriteFile(logFile, []byte(content), 0o644)

	critique := extractCritiqueFromLog(logFile)
	if !strings.Contains(critique, "failed to commit") {
		t.Errorf("expected critique from result, got: %s", critique)
	}
}

func TestExtractCritiqueFromLogAssistantStreamJSON(t *testing.T) {
	dir := t.TempDir()
	logFile := filepath.Join(dir, "test.jsonl")

	// stream-json format: {"type":"assistant","message":{"type":"text","text":"..."}}
	content := `{"type":"init","session_id":"abc"}
{"type":"assistant","message":{"type":"text","text":"The agent should have committed its work."}}
`
	os.WriteFile(logFile, []byte(content), 0o644)

	critique := extractCritiqueFromLog(logFile)
	if !strings.Contains(critique, "committed its work") {
		t.Errorf("expected critique from stream-json assistant message, got: %s", critique)
	}
}

func TestExtractCritiqueFromLogAssistantContentBlocks(t *testing.T) {
	dir := t.TempDir()
	logFile := filepath.Join(dir, "test.jsonl")

	// Content blocks format: {"type":"assistant","message":{"content":[{"type":"text","text":"..."}]}}
	content := `{"type":"init","session_id":"abc"}
{"type":"assistant","message":{"content":[{"type":"text","text":"The files were modified but never committed."}]}}
`
	os.WriteFile(logFile, []byte(content), 0o644)

	critique := extractCritiqueFromLog(logFile)
	if !strings.Contains(critique, "never committed") {
		t.Errorf("expected critique from content blocks format, got: %s", critique)
	}
}

func TestExtractCritiqueFromLogResultTakesPrecedence(t *testing.T) {
	dir := t.TempDir()
	logFile := filepath.Join(dir, "test.jsonl")

	// When a result line exists, it should be returned directly (not concatenated with assistant msgs)
	content := `{"type":"init","session_id":"abc"}
{"type":"assistant","message":{"type":"text","text":"Let me analyze..."}}
{"type":"result","subtype":"success","result":"The definitive critique."}
`
	os.WriteFile(logFile, []byte(content), 0o644)

	critique := extractCritiqueFromLog(logFile)
	if critique != "The definitive critique." {
		t.Errorf("expected result to take precedence, got: %s", critique)
	}
}

func TestExtractCritiqueFromLogEmpty(t *testing.T) {
	dir := t.TempDir()
	logFile := filepath.Join(dir, "test.jsonl")

	os.WriteFile(logFile, []byte(""), 0o644)

	critique := extractCritiqueFromLog(logFile)
	if critique != "" {
		t.Errorf("expected empty critique for empty log, got: %s", critique)
	}
}

func TestExtractCritiqueFromLogMissing(t *testing.T) {
	critique := extractCritiqueFromLog("/nonexistent/path.jsonl")
	if critique != "" {
		t.Errorf("expected empty critique for missing file, got: %s", critique)
	}
}

func TestSpawnerRefineCircuitBreaker(t *testing.T) {
	s, d, _ := setupSpawnerTestDirect(t)
	ctx := context.Background()

	d.CreateTask(ctx, db.Task{
		ID:          "t1",
		Title:       "Test task",
		Status:      "failed",
		Role:        "implementer",
		Description: sql.NullString{String: "Implement something", Valid: true},
	})
	d.SetBlackboard(ctx, "refinement:t1", "2", "test") // Already at max

	_, err := s.Refine(ctx, "t1")
	if err == nil {
		t.Error("expected error when refinement count >= MaxRefinements")
	}
	if !strings.Contains(err.Error(), "exceeded max refinements") {
		t.Errorf("expected max refinements error, got: %v", err)
	}
}

func TestSpawnerRefineStoresCritique(t *testing.T) {
	s, d, repoRoot := setupSpawnerTestDirect(t)
	ctx := context.Background()

	taskID := "t1"
	worktreePath := filepath.Join(repoRoot, ".worktree", taskID)
	branch := "feature/" + taskID

	// Create worktree (simulating a prior Run)
	gitExec(t, repoRoot, "worktree", "add", worktreePath, "-b", branch)

	// Register agent and create task already assigned (simulating post-Run state)
	d.RegisterAgent(ctx, db.Agent{ID: "a1", Role: "implementer", Status: "working"})
	d.CreateTask(ctx, db.Task{
		ID:          taskID,
		Title:       "Test task",
		Status:      "failed",
		Role:        "implementer",
		Description: sql.NullString{String: "Implement something", Valid: true},
	})
	d.AssignTask(ctx, taskID, "a1", worktreePath, branch)
	d.SetBlackboard(ctx, "last_failure:"+taskID, "implementer_no_changes", "test")

	// Write a log file so Refine has something to read
	logFile := filepath.Join(s.LogsDir, taskID+".jsonl")
	os.WriteFile(logFile, []byte(`{"type":"result","subtype":"success","result":"done"}`+"\n"), 0o644)

	// With SpawnCmd="echo", the reviewer will finish immediately with no output
	// The Refine method should store a fallback critique and call RespawnForRefinement
	result, err := s.Refine(ctx, taskID)
	if err != nil {
		t.Fatalf("Refine: %v", err)
	}
	if result == nil {
		t.Fatal("expected spawn result from Refine")
	}

	// Check critique was stored
	critique, _ := d.GetBlackboardValue(ctx, "critique:"+taskID)
	if critique == "" {
		t.Error("expected critique to be stored in blackboard")
	}

	// Check refinement counter was incremented
	ref, _ := d.GetBlackboardValue(ctx, "refinement:"+taskID)
	if ref != "1" {
		t.Errorf("expected refinement=1, got %s", ref)
	}
}

func TestSpawnerRefineTaskNotFound(t *testing.T) {
	s, _, _ := setupSpawnerTestDirect(t)
	ctx := context.Background()

	_, err := s.Refine(ctx, "nonexistent")
	if err == nil {
		t.Error("expected error for nonexistent task")
	}
}

func TestSpawnerGenerateSpecWithBlackboard(t *testing.T) {
	s, d, _ := setupSpawnerTestDirect(t)
	ctx := context.Background()

	d.CreateTask(ctx, db.Task{
		ID:          "t1",
		Title:       "Task With Context",
		Description: sql.NullString{String: "Build the widget", Valid: true},
		Status:      "pending",
		Role:        "implementer",
	})
	d.SetBlackboard(ctx, "schema:t1", "CREATE TABLE widgets (id TEXT);", "test")
	d.SetBlackboard(ctx, "contract:t1", "Must export BuildWidget() function.", "test")

	spec, err := s.generateSpec(ctx, "t1")
	if err != nil {
		t.Fatalf("generateSpec: %v", err)
	}
	if !strings.Contains(spec, "CREATE TABLE widgets") {
		t.Error("expected schema in spec")
	}
	if !strings.Contains(spec, "Must export BuildWidget()") {
		t.Error("expected contract in spec")
	}
}

func TestRespawnForRefinementPreservesWorktree(t *testing.T) {
	s, d, repoRoot := setupSpawnerTestDirect(t)
	ctx := context.Background()

	taskID := "t1"
	worktreePath := filepath.Join(repoRoot, ".worktree", taskID)
	branch := "feature/" + taskID

	// Create worktree manually (simulating a prior Run)
	gitExec(t, repoRoot, "worktree", "add", worktreePath, "-b", branch)

	// Register agent and create task already assigned (simulating post-Run state)
	d.RegisterAgent(ctx, db.Agent{ID: "a1", Role: "implementer", Status: "working"})
	d.CreateTask(ctx, db.Task{
		ID:     taskID,
		Title:  "Test task",
		Status: "failed",
		Role:   "implementer",
	})
	d.AssignTask(ctx, taskID, "a1", worktreePath, branch)

	// Create a marker file in the worktree to verify preservation
	markerFile := filepath.Join(worktreePath, "refinement-marker.txt")
	os.WriteFile(markerFile, []byte("preserved"), 0o644)

	// RespawnForRefinement should preserve the worktree
	result, err := s.RespawnForRefinement(ctx, taskID)
	if err != nil {
		t.Fatalf("RespawnForRefinement: %v", err)
	}
	if result == nil {
		t.Fatal("expected spawn result")
	}

	// Verify the worktree still exists (not destroyed)
	if _, err := os.Stat(worktreePath); os.IsNotExist(err) {
		t.Error("worktree should be preserved after RespawnForRefinement")
	}

	// Verify the marker file is gone — refinement resets to fork point to
	// discard bad commits. The worktree directory is preserved but contents
	// are clean so the second agent starts fresh with critique guidance.
	if _, err := os.Stat(markerFile); err == nil {
		t.Error("marker file should be removed — refinement resets branch to fork point")
	}

	// Verify retry count was NOT incremented
	retryStr, _ := d.GetBlackboardValue(ctx, "retry:"+taskID)
	if retryStr != "" && retryStr != "0" {
		t.Errorf("retry count should not be incremented by RespawnForRefinement, got %s", retryStr)
	}
}

func TestRespawnForRefinementRetryCircuitBreaker(t *testing.T) {
	s, d, _ := setupSpawnerTestDirect(t)
	ctx := context.Background()

	d.CreateTask(ctx, db.Task{
		ID:     "t1",
		Title:  "Test task",
		Status: "failed",
		Role:   "implementer",
	})
	d.SetBlackboard(ctx, "retry:t1", "3", "test")

	_, err := s.RespawnForRefinement(ctx, "t1")
	if err == nil {
		t.Error("expected circuit breaker error")
	}
	if !strings.Contains(err.Error(), "exceeded max retries") {
		t.Errorf("expected max retries error, got: %v", err)
	}
}

func TestRefineAcquiresConcurrencyLock(t *testing.T) {
	s, d, _ := setupSpawnerTestDirect(t)
	ctx := context.Background()

	d.CreateTask(ctx, db.Task{
		ID:          "t1",
		Title:       "Test task",
		Status:      "failed",
		Role:        "implementer",
		Description: sql.NullString{String: "Implement something", Valid: true},
	})
	d.SetBlackboard(ctx, "last_failure:t1", "implementer_no_changes", "test")

	// Simulate another process already holding the lock
	d.SetBlackboard(ctx, "refinement_lock:t1", "1", "test")

	_, err := s.Refine(ctx, "t1")
	if err == nil {
		t.Error("expected error when refinement lock is held")
	}
	if !strings.Contains(err.Error(), "already in progress") {
		t.Errorf("expected 'already in progress' error, got: %v", err)
	}
}

func TestSpawner_MCPConfigArgs_SkippedWhenFlagged(t *testing.T) {
	s, d, repoRoot := setupSpawnerTestDirect(t)
	ctx := context.Background()

	// Create .orchestra/mcp.json
	orchestraDir := filepath.Join(repoRoot, ".orchestra")
	os.MkdirAll(orchestraDir, 0o755)
	os.WriteFile(filepath.Join(orchestraDir, "mcp.json"), []byte(`{"servers":{}}`), 0o644)

	// Set the skip_mcp flag
	d.SetBlackboard(ctx, "conductor:skip_mcp", "1", "test")

	args := s.mcpConfigArgs(ctx)
	if args != nil {
		t.Errorf("expected nil args when skip_mcp=1, got %v", args)
	}
}

func TestSpawner_MCPConfigArgs_ReturnsFlagsWhenPresent(t *testing.T) {
	s, _, repoRoot := setupSpawnerTestDirect(t)
	ctx := context.Background()

	// Create .orchestra/mcp.json
	orchestraDir := filepath.Join(repoRoot, ".orchestra")
	os.MkdirAll(orchestraDir, 0o755)
	mcpPath := filepath.Join(orchestraDir, "mcp.json")
	os.WriteFile(mcpPath, []byte(`{"servers":{}}`), 0o644)

	args := s.mcpConfigArgs(ctx)
	if len(args) != 2 {
		t.Fatalf("expected 2 args, got %d: %v", len(args), args)
	}
	if args[0] != "--mcp-config" {
		t.Errorf("expected --mcp-config flag, got %s", args[0])
	}
	if args[1] != mcpPath {
		t.Errorf("expected path %s, got %s", mcpPath, args[1])
	}
}

func TestSpawner_MCPConfigArgs_NilWhenFileAbsent(t *testing.T) {
	s, _, _ := setupSpawnerTestDirect(t)
	ctx := context.Background()

	// No .orchestra/mcp.json created
	args := s.mcpConfigArgs(ctx)
	if args != nil {
		t.Errorf("expected nil args when mcp.json absent, got %v", args)
	}
}

// --- File Ownership Enforcement Tests ---

func TestSetFilePermissions(t *testing.T) {
	s, d, _ := setupSpawnerTestDirect(t)
	ctx := context.Background()

	// Create a worktree-like directory with 3 files
	worktree := t.TempDir()
	os.WriteFile(filepath.Join(worktree, "models.go"), []byte("package db"), 0o644)
	os.WriteFile(filepath.Join(worktree, "queries.go"), []byte("package db"), 0o644)
	os.WriteFile(filepath.Join(worktree, "mutations.go"), []byte("package db"), 0o644)

	// Create a .git directory that should be skipped
	gitDir := filepath.Join(worktree, ".git")
	os.MkdirAll(gitDir, 0o755)
	os.WriteFile(filepath.Join(gitDir, "config"), []byte("gitconfig"), 0o644)

	// Lock only models.go to task t1
	d.CreateTask(ctx, db.Task{ID: "t1", Title: "Test", Status: "pending", Role: "implementer"})
	d.CreateFileLock(ctx, "models.go", "", "t1", time.Time{})

	s.setFilePermissions(ctx, worktree, "t1")

	// models.go should remain writable (owned by t1)
	info, _ := os.Stat(filepath.Join(worktree, "models.go"))
	if info.Mode().Perm()&0o200 == 0 {
		t.Error("models.go should be writable (owned by task)")
	}

	// queries.go and mutations.go should be read-only
	info, _ = os.Stat(filepath.Join(worktree, "queries.go"))
	if info.Mode().Perm()&0o200 != 0 {
		t.Error("queries.go should be read-only (not owned)")
	}
	info, _ = os.Stat(filepath.Join(worktree, "mutations.go"))
	if info.Mode().Perm()&0o200 != 0 {
		t.Error("mutations.go should be read-only (not owned)")
	}

	// .git/config should not be affected
	info, _ = os.Stat(filepath.Join(gitDir, "config"))
	if info.Mode().Perm()&0o200 == 0 {
		t.Error(".git/config should not be changed to read-only")
	}

	// Executable files should get 0555 (read-only + execute), not 0444
	shFile := filepath.Join(worktree, "script.sh")
	os.WriteFile(shFile, []byte("#!/bin/bash\n"), 0o755)
	s.setFilePermissions(ctx, worktree, "t1")
	info, _ = os.Stat(shFile)
	if info.Mode().Perm() != 0o555 {
		t.Errorf("executable .sh should be 0555 (read-only+exec), got %o", info.Mode().Perm())
	}

	// Non-executable unowned file should still be 0444
	info, _ = os.Stat(filepath.Join(worktree, "queries.go"))
	if info.Mode().Perm() != 0o444 {
		t.Errorf("non-executable unowned file should be 0444, got %o", info.Mode().Perm())
	}
}

func TestInstallFileOwnershipHook(t *testing.T) {
	s, _, repoRoot := setupSpawnerTestDirect(t)
	ctx := context.Background()

	// Create a worktree directory
	worktree := filepath.Join(repoRoot, ".worktree", "t1")
	os.MkdirAll(worktree, 0o755)

	// Initialize git in the worktree so config can be set
	gitExec(t, worktree, "init")

	err := s.installFileOwnershipHook(ctx, worktree, "t1")
	if err != nil {
		t.Fatalf("installFileOwnershipHook: %v", err)
	}

	// Verify hook file exists
	hookPath := filepath.Join(worktree, ".orchestra-hooks", "pre-commit")
	info, err := os.Stat(hookPath)
	if err != nil {
		t.Fatalf("hook file not found: %v", err)
	}

	// Verify hook is executable
	if info.Mode().Perm()&0o111 == 0 {
		t.Error("hook should be executable")
	}

	// Verify hook content includes task ID
	content, _ := os.ReadFile(hookPath)
	if !strings.Contains(string(content), `TASK_ID="t1"`) {
		t.Error("hook should contain the task ID")
	}
}

func TestInstallFileOwnershipHook_CreatesGitignore(t *testing.T) {
	s, _, repoRoot := setupSpawnerTestDirect(t)
	ctx := context.Background()

	worktree := filepath.Join(repoRoot, ".worktree", "t1")
	os.MkdirAll(worktree, 0o755)
	gitExec(t, worktree, "init")

	err := s.installFileOwnershipHook(ctx, worktree, "t1")
	if err != nil {
		t.Fatalf("installFileOwnershipHook: %v", err)
	}

	// Verify .gitignore exists inside .orchestra-hooks/
	giPath := filepath.Join(worktree, ".orchestra-hooks", ".gitignore")
	data, err := os.ReadFile(giPath)
	if err != nil {
		t.Fatalf(".orchestra-hooks/.gitignore not found: %v", err)
	}
	if !strings.Contains(string(data), "*") {
		t.Errorf("expected .gitignore to contain '*', got %q", string(data))
	}
}

func TestWaitForFileLocks_NoContention(t *testing.T) {
	s, d, _ := setupSpawnerTestDirect(t)
	ctx := context.Background()

	d.CreateTask(ctx, db.Task{ID: "t1", Title: "Test", Status: "pending", Role: "implementer"})
	d.CreateFileLock(ctx, "models.go", "", "t1", time.Time{})

	// No competing locks → should return immediately
	start := time.Now()
	err := s.waitForFileLocks(ctx, "t1", 10*time.Second)
	elapsed := time.Since(start)

	if err != nil {
		t.Fatalf("waitForFileLocks: %v", err)
	}
	if elapsed > 2*time.Second {
		t.Errorf("should have returned immediately, took %s", elapsed)
	}
}

func TestWaitForFileLocks_WithContention(t *testing.T) {
	s, d, _ := setupSpawnerTestDirect(t)
	ctx := context.Background()

	// t1 owns models.go, t2 also owns models.go but is running
	d.CreateTask(ctx, db.Task{ID: "t1", Title: "Task 1", Status: "pending", Role: "implementer"})
	d.CreateTask(ctx, db.Task{ID: "t2", Title: "Task 2", Status: "running", Role: "implementer"})
	d.CreateFileLock(ctx, "models.go", "", "t1", time.Time{})
	d.CreateFileLock(ctx, "queries.go", "", "t2", time.Time{})

	// t1's file (models.go) is only locked by t1, not by a running task
	// So there's no contention — should return immediately
	start := time.Now()
	err := s.waitForFileLocks(ctx, "t1", 5*time.Second)
	elapsed := time.Since(start)

	if err != nil {
		t.Fatalf("waitForFileLocks: %v", err)
	}
	if elapsed > 2*time.Second {
		t.Errorf("no real contention, should return quickly, took %s", elapsed)
	}
}

func TestSalvagePreservesFilePermissions(t *testing.T) {
	// 1. Create a temporary git repo with an initial commit containing a .sh file with mode 100755.
	repoDir := t.TempDir()
	gitExec(t, repoDir, "init")
	gitExec(t, repoDir, "checkout", "-b", "main")

	shFile := filepath.Join(repoDir, "run.sh")
	os.WriteFile(shFile, []byte("#!/bin/bash\necho hello\n"), 0o755)
	gitExec(t, repoDir, "add", ".")
	gitExec(t, repoDir, "commit", "-m", "initial commit with executable .sh")

	// Verify the initial commit recorded mode 100755.
	lsOut := gitOutput(t, repoDir, "ls-tree", "HEAD", "run.sh")
	if !strings.Contains(lsOut, "100755") {
		t.Fatalf("expected initial .sh to be 100755, got: %s", lsOut)
	}

	// 2. Create a git worktree from that repo.
	worktreeDir := filepath.Join(repoDir, "wt")
	gitExec(t, repoDir, "worktree", "add", worktreeDir, "-b", "feature/salvage-test")

	// 3. In the worktree: chmod 644 the .sh file (permission loss) AND modify content.
	wtShFile := filepath.Join(worktreeDir, "run.sh")
	os.Chmod(wtShFile, 0o644)
	os.WriteFile(wtShFile, []byte("#!/bin/bash\necho modified\n"), 0o644)

	// 4. Call SalvageWorktreeChanges.
	ctx := context.Background()
	SalvageWorktreeChanges(ctx, worktreeDir, "test-salvage")

	// 5. Inspect the salvage commit with git diff-tree.
	diffOut := gitOutput(t, worktreeDir, "diff-tree", "--no-commit-id", "-r", "HEAD")

	// Each diff-tree line looks like: :oldmode newmode oldhash newhash status\tpath
	// A mode-only change would show different modes (e.g., 100755 100644).
	// A content change shows the same mode but different hashes.
	lines := strings.Split(strings.TrimSpace(diffOut), "\n")
	for _, line := range lines {
		if line == "" {
			continue
		}
		if !strings.Contains(line, "run.sh") {
			continue
		}
		// Line format: :100755 100755 <oldhash> <newhash> M\trun.sh
		// If modes differ (e.g., :100755 100644) that's a mode change — should not happen.
		parts := strings.Fields(line)
		if len(parts) < 2 {
			continue
		}
		oldMode := strings.TrimPrefix(parts[0], ":")
		newMode := parts[1]
		if oldMode != newMode {
			t.Errorf("salvage commit contains a mode change for run.sh: %s → %s (line: %s)", oldMode, newMode, line)
		}
	}

	// Verify content changes ARE present in the salvage commit.
	found := false
	for _, line := range lines {
		if strings.Contains(line, "run.sh") {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected content change for run.sh in salvage commit, but run.sh not in diff-tree output")
	}

	// Double-check: the committed file should contain the modified content.
	showOut := gitOutput(t, worktreeDir, "show", "HEAD:run.sh")
	if !strings.Contains(showOut, "echo modified") {
		t.Errorf("expected salvaged content to contain 'echo modified', got: %s", showOut)
	}
}

// gitOutput runs a git command and returns its stdout. Fails the test on error.
func gitOutput(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=test",
		"GIT_AUTHOR_EMAIL=test@test.com",
		"GIT_COMMITTER_NAME=test",
		"GIT_COMMITTER_EMAIL=test@test.com",
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v: %s", args, err, string(out))
	}
	return string(out)
}

func TestRefineCleansUpLockOnCompletion(t *testing.T) {
	s, d, repoRoot := setupSpawnerTestDirect(t)
	ctx := context.Background()

	taskID := "t1"
	worktreePath := filepath.Join(repoRoot, ".worktree", taskID)
	branch := "feature/" + taskID

	// Create worktree (simulating a prior Run)
	gitExec(t, repoRoot, "worktree", "add", worktreePath, "-b", branch)

	// Register agent and create task already assigned
	d.RegisterAgent(ctx, db.Agent{ID: "a1", Role: "implementer", Status: "working"})
	d.CreateTask(ctx, db.Task{
		ID:          taskID,
		Title:       "Test task",
		Status:      "failed",
		Role:        "implementer",
		Description: sql.NullString{String: "Implement something", Valid: true},
	})
	d.AssignTask(ctx, taskID, "a1", worktreePath, branch)
	d.SetBlackboard(ctx, "last_failure:"+taskID, "implementer_no_changes", "test")

	// Write a log file so Refine has something to read
	logFile := filepath.Join(s.LogsDir, taskID+".jsonl")
	os.WriteFile(logFile, []byte(`{"type":"result","subtype":"success","result":"done"}`+"\n"), 0o644)

	// Run Refine — it should acquire and release the lock
	_, err := s.Refine(ctx, taskID)
	if err != nil {
		t.Fatalf("Refine: %v", err)
	}

	// The lock should be cleaned up (defer DeleteBlackboard)
	lockVal, _ := d.GetBlackboardValue(ctx, "refinement_lock:"+taskID)
	if lockVal != "" {
		t.Error("refinement lock should be cleaned up after Refine completes")
	}
}

func TestOrchestraEnvIncludesDepth(t *testing.T) {
	// At depth 0, spawned agents should get ORCHESTRA_DEPTH=1.
	os.Unsetenv("ORCHESTRA_DEPTH")

	s, _, _ := setupSpawnerTestDirect(t)
	env := s.orchestraEnv("task-123")

	var found bool
	for _, e := range env {
		if e == "ORCHESTRA_DEPTH=1" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("orchestraEnv missing ORCHESTRA_DEPTH=1, got: %v", env)
	}
}

func TestOrchestraEnvIncrementsDepth(t *testing.T) {
	// At depth 2, spawned agents should get ORCHESTRA_DEPTH=3.
	os.Setenv("ORCHESTRA_DEPTH", "2")
	defer os.Unsetenv("ORCHESTRA_DEPTH")

	s, _, _ := setupSpawnerTestDirect(t)
	env := s.orchestraEnv("task-456")

	var found bool
	for _, e := range env {
		if e == "ORCHESTRA_DEPTH=3" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("orchestraEnv at depth 2 should have ORCHESTRA_DEPTH=3, got: %v", env)
	}
}
