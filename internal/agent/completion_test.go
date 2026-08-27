package agent

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/MochaCosine1206/orchestra/internal/db"
)

func TestCheckLogResultSuccess(t *testing.T) {
	dir := t.TempDir()
	taskID := "test-task-1"
	logFile := filepath.Join(dir, taskID+".jsonl")

	content := `{"type":"init","session_id":"abc123"}
{"type":"assistant","message":"working"}
{"type":"result","subtype":"success","result":"all done"}
`
	os.WriteFile(logFile, []byte(content), 0o644)

	hasResult, isSuccess := CheckLogResult(dir, taskID)
	if !hasResult {
		t.Error("expected hasResult=true")
	}
	if !isSuccess {
		t.Error("expected isSuccess=true")
	}
}

func TestCheckLogResultNoResult(t *testing.T) {
	dir := t.TempDir()
	taskID := "test-task-2"
	logFile := filepath.Join(dir, taskID+".jsonl")

	content := `{"type":"init","session_id":"abc123"}
{"type":"assistant","message":"working"}
`
	os.WriteFile(logFile, []byte(content), 0o644)

	hasResult, _ := CheckLogResult(dir, taskID)
	if hasResult {
		t.Error("expected hasResult=false for log without result line")
	}
}

func TestCheckLogResultMissingFile(t *testing.T) {
	dir := t.TempDir()
	hasResult, _ := CheckLogResult(dir, "nonexistent")
	if hasResult {
		t.Error("expected hasResult=false for missing file")
	}
}

func TestCheckLogResult_IsErrorTrue(t *testing.T) {
	dir := t.TempDir()
	taskID := "test-task-iserror"
	logFile := filepath.Join(dir, taskID+".jsonl")

	// Rate-limited agent: subtype:"success" but is_error:true — must NOT be treated as success.
	content := `{"type":"init","session_id":"abc123"}
{"type":"rate_limit_event","rate_limit_info":{"status":"rejected","resetsAt":1772902800,"rateLimitType":"five_hour"}}
{"type":"result","subtype":"success","is_error":true,"result":"You've hit your limit · resets 12pm (America/New_York)","num_turns":1,"usage":{"output_tokens":0}}
`
	os.WriteFile(logFile, []byte(content), 0o644)

	hasResult, isSuccess := CheckLogResult(dir, taskID)
	if !hasResult {
		t.Error("expected hasResult=true (result line exists)")
	}
	if isSuccess {
		t.Error("expected isSuccess=false when is_error:true is present")
	}
}

func TestCheckLogResult_IsErrorFalse(t *testing.T) {
	dir := t.TempDir()
	taskID := "test-task-noerror"
	logFile := filepath.Join(dir, taskID+".jsonl")

	// Normal success: is_error is absent or false — should be treated as success.
	content := `{"type":"result","subtype":"success","is_error":false,"result":"all done"}
`
	os.WriteFile(logFile, []byte(content), 0o644)

	hasResult, isSuccess := CheckLogResult(dir, taskID)
	if !hasResult {
		t.Error("expected hasResult=true")
	}
	if !isSuccess {
		t.Error("expected isSuccess=true when is_error is false")
	}
}

func TestHandleAgentExitRateLimited(t *testing.T) {
	ctx := context.Background()
	d := setupTestDB(t)
	logsDir := t.TempDir()
	pidsDir := t.TempDir()
	repoRoot := t.TempDir()

	s := &Spawner{
		DB:       d,
		RepoRoot: repoRoot,
		LogsDir:  logsDir,
		PidsDir:  pidsDir,
	}

	taskID := "t-ratelimit-1"
	agentID := "a-ratelimit-1"
	d.CreateTask(ctx, db.Task{ID: taskID, Title: "rate limited task", Role: "implementer", Status: "running"})
	d.RegisterAgent(ctx, db.Agent{ID: agentID, Role: "implementer", Status: "working"})
	d.ExecContext(ctx, `UPDATE agents SET current_task = ? WHERE id = ?`, taskID, agentID)
	d.ExecContext(ctx, `UPDATE tasks SET assigned_to = ? WHERE id = ?`, agentID, taskID)

	// Write JSONL with rate_limit_event + is_error:true result (exact format from production).
	logContent := `{"type":"init","session_id":"abc123"}
{"type":"system","message":"starting"}
{"type":"assistant","message":"working"}
{"type":"rate_limit_event","rate_limit_info":{"status":"rejected","resetsAt":1772902800,"rateLimitType":"five_hour"}}
{"type":"result","subtype":"success","is_error":true,"result":"You've hit your limit · resets 12pm (America/New_York)","num_turns":1,"usage":{"output_tokens":0}}
`
	os.WriteFile(filepath.Join(logsDir, taskID+".jsonl"), []byte(logContent), 0o644)
	os.WriteFile(filepath.Join(logsDir, taskID+".stderr"), []byte(""), 0o644)
	WritePID(pidsDir, taskID, 99997)

	s.handleAgentExit(ctx, taskID, agentID)

	// Task should be failed (not done — it was rate limited, not successful).
	task, _ := d.GetTaskByID(ctx, taskID)
	if task.Status != "failed" {
		t.Errorf("expected task status 'failed', got %q", task.Status)
	}

	// Failure type should be session_limit (detected via DetectRateLimitEvent).
	ft, _ := d.GetBlackboardValue(ctx, "failure_type:"+taskID)
	if ft != "session_limit" {
		t.Errorf("expected failure_type 'session_limit', got %q", ft)
	}

	// Global cooldown should be set.
	cooldown, _ := d.GetBlackboardValue(ctx, "conductor:rate_limit_cooldown_until")
	if cooldown != "1772902800" {
		t.Errorf("expected global cooldown epoch 1772902800, got %q", cooldown)
	}
}

func TestHandleAgentExitSuccess(t *testing.T) {
	ctx := context.Background()
	d := setupTestDB(t)
	logsDir := t.TempDir()
	pidsDir := t.TempDir()
	repoRoot := t.TempDir()

	s := &Spawner{
		DB:       d,
		RepoRoot: repoRoot,
		LogsDir:  logsDir,
		PidsDir:  pidsDir,
	}

	// Create task and agent — use scout role which validates log content length
	taskID := "t-success-1"
	agentID := "a-success-1"
	d.CreateTask(ctx, db.Task{ID: taskID, Title: "test task", Role: "scout", Status: "running"})
	d.RegisterAgent(ctx, db.Agent{ID: agentID, Role: "scout", Status: "working"})
	// Use raw SQL to set current_task without changing task status (AssignTask forces 'assigned')
	d.ExecContext(ctx, `UPDATE agents SET current_task = ? WHERE id = ?`, taskID, agentID)
	d.ExecContext(ctx, `UPDATE tasks SET assigned_to = ? WHERE id = ?`, agentID, taskID)

	// Write a success result log — must be >= 100 chars for scout validation
	longResult := `{"type":"result","subtype":"success","result":"Found 5 relevant files in the codebase: main.go, config.go, spawner.go, monitor.go, conductor.go. Analysis complete with dependency mapping."}`
	logContent := longResult + "\n"
	os.WriteFile(filepath.Join(logsDir, taskID+".jsonl"), []byte(logContent), 0o644)

	// Write PID file to verify cleanup
	WritePID(pidsDir, taskID, 99999)

	s.handleAgentExit(ctx, taskID, agentID)

	// Task should be done
	task, _ := d.GetTaskByID(ctx, taskID)
	if task.Status != "done" {
		t.Errorf("expected task status 'done', got %q", task.Status)
	}

	// PID file should be removed
	if _, err := os.Stat(filepath.Join(pidsDir, taskID+".pid")); !os.IsNotExist(err) {
		t.Error("expected PID file to be removed")
	}
}

func TestHandleAgentExitCrash(t *testing.T) {
	ctx := context.Background()
	d := setupTestDB(t)
	logsDir := t.TempDir()
	pidsDir := t.TempDir()
	repoRoot := t.TempDir()

	s := &Spawner{
		DB:       d,
		RepoRoot: repoRoot,
		LogsDir:  logsDir,
		PidsDir:  pidsDir,
	}

	taskID := "t-crash-1"
	agentID := "a-crash-1"
	d.CreateTask(ctx, db.Task{ID: taskID, Title: "test task", Role: "implementer", Status: "running"})
	d.RegisterAgent(ctx, db.Agent{ID: agentID, Role: "implementer", Status: "working"})
	d.ExecContext(ctx, `UPDATE agents SET current_task = ? WHERE id = ?`, taskID, agentID)
	d.ExecContext(ctx, `UPDATE tasks SET assigned_to = ? WHERE id = ?`, agentID, taskID)

	// No log file — agent crashed before writing anything
	WritePID(pidsDir, taskID, 99998)

	s.handleAgentExit(ctx, taskID, agentID)

	// Task should be failed
	task, _ := d.GetTaskByID(ctx, taskID)
	if task.Status != "failed" {
		t.Errorf("expected task status 'failed', got %q", task.Status)
	}

	// Failure type should be set
	ft, _ := d.GetBlackboardValue(ctx, "failure_type:"+taskID)
	if ft == "" {
		t.Error("expected failure_type blackboard key to be set")
	}
}

func TestHandleAgentExitValidationFailureSetsKeys(t *testing.T) {
	ctx := context.Background()
	d := setupTestDB(t)
	logsDir := t.TempDir()
	pidsDir := t.TempDir()
	repoRoot := t.TempDir()

	// Init git repo with worktree so validation can run
	gitInit(t, repoRoot)
	wt := filepath.Join(repoRoot, ".worktree", "t-val-1")
	gitExec(t, repoRoot, "worktree", "add", wt, "-b", "feature/t-val-1")
	t.Cleanup(func() {
		// Remove worktree before TempDir cleanup to avoid "directory not empty" errors
		gitExec(t, repoRoot, "worktree", "remove", "--force", wt)
	})

	os.MkdirAll(logsDir, 0o755)
	os.MkdirAll(pidsDir, 0o755)

	// Agent definition files so Refine can resolve the reviewer model
	agentDir := filepath.Join(repoRoot, ".claude", "agents")
	os.MkdirAll(agentDir, 0o755)
	os.WriteFile(filepath.Join(agentDir, "implementer.md"), []byte("---\nname: Implementer\nmodel: opus\n---\nYou are an implementer."), 0o644)

	s := &Spawner{
		DB:       d,
		RepoRoot: repoRoot,
		LogsDir:  logsDir,
		PidsDir:  pidsDir,
		SpawnCmd: "echo", // mock
	}

	taskID := "t-val-1"
	agentID := "a-val-1"
	d.CreateTask(ctx, db.Task{ID: taskID, Title: "test task", Role: "implementer", Status: "running"})
	d.RegisterAgent(ctx, db.Agent{ID: agentID, Role: "implementer", Status: "working"})
	d.ExecContext(ctx, `UPDATE agents SET current_task = ? WHERE id = ?`, taskID, agentID)
	d.ExecContext(ctx, `UPDATE tasks SET assigned_to = ?, worktree = ? WHERE id = ?`, agentID, wt, taskID)

	// Write a success result but no file changes — validation will fail with implementer_no_changes
	logContent := `{"type":"result","subtype":"success","result":"Done"}` + "\n"
	os.WriteFile(filepath.Join(logsDir, taskID+".jsonl"), []byte(logContent), 0o644)
	WritePID(pidsDir, taskID, 99999)

	s.handleAgentExit(ctx, taskID, agentID)

	// Task should be failed (validation failure)
	task, _ := d.GetTaskByID(ctx, taskID)
	if task.Status != "failed" {
		t.Errorf("expected task status 'failed', got %q", task.Status)
	}

	// failure_type and last_failure should be set
	ft, _ := d.GetBlackboardValue(ctx, "failure_type:"+taskID)
	if ft != "validation_failure" {
		t.Errorf("expected failure_type 'validation_failure', got %q", ft)
	}
	lf, _ := d.GetBlackboardValue(ctx, "last_failure:"+taskID)
	if lf == "" {
		t.Error("expected last_failure to be set")
	}
	// Verify it's a refinable failure
	if !IsRefinableFailure(lf) {
		t.Errorf("expected a refinable failure reason, got %q", lf)
	}
}

func TestHandleAgentExitIdempotent(t *testing.T) {
	ctx := context.Background()
	d := setupTestDB(t)
	logsDir := t.TempDir()
	pidsDir := t.TempDir()
	repoRoot := t.TempDir()

	s := &Spawner{
		DB:       d,
		RepoRoot: repoRoot,
		LogsDir:  logsDir,
		PidsDir:  pidsDir,
	}

	taskID := "t-idem-1"
	agentID := "a-idem-1"
	d.CreateTask(ctx, db.Task{ID: taskID, Title: "test task", Role: "scout", Status: "done"})
	d.RegisterAgent(ctx, db.Agent{ID: agentID, Role: "scout", Status: "dead"})
	// Wire agent to task without calling AssignTask (which would reset status)
	d.ExecContext(ctx, `UPDATE agents SET current_task = ? WHERE id = ?`, taskID, agentID)
	d.ExecContext(ctx, `UPDATE tasks SET assigned_to = ? WHERE id = ?`, agentID, taskID)

	// Calling handleAgentExit on an already-terminal task should not panic or change state.
	s.handleAgentExit(ctx, taskID, agentID)

	task, _ := d.GetTaskByID(ctx, taskID)
	if task.Status != "done" {
		t.Errorf("expected task status to remain 'done', got %q", task.Status)
	}
}
