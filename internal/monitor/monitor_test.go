package monitor

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

	"github.com/MochaCosine1206/orchestra/internal/agent"
	"github.com/MochaCosine1206/orchestra/internal/db"
)

func setupTestDB(t *testing.T) *db.DB {
	t.Helper()
	d, err := db.OpenMemory()
	if err != nil {
		t.Fatalf("OpenMemory: %v", err)
	}
	if err := d.InitSchema(context.Background()); err != nil {
		t.Fatalf("InitSchema: %v", err)
	}
	t.Cleanup(func() { d.Close() })
	return d
}

func setupMonitorTest(t *testing.T) (*Monitor, *db.DB, string) {
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

	gitInit(t, repoRoot)

	spawner := &agent.Spawner{
		DB:       d,
		RepoRoot: repoRoot,
		LogsDir:  logsDir,
		PidsDir:  pidsDir,
		SpawnCmd: "echo",
	}

	mon := &Monitor{
		DB:       d,
		Spawner:  spawner,
		RepoRoot: repoRoot,
		LogsDir:  logsDir,
		PidsDir:  pidsDir,
		Interval: 100 * time.Millisecond,
	}
	return mon, d, repoRoot
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

// --- Start/Stop Tests ---

func TestMonitorStartStop(t *testing.T) {
	mon, _, _ := setupMonitorTest(t)
	ctx := context.Background()

	if mon.Running() {
		t.Error("expected not running initially")
	}

	err := mon.Start(ctx)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if !mon.Running() {
		t.Error("expected running after Start")
	}

	// Double start should fail
	err = mon.Start(ctx)
	if err == nil {
		t.Error("expected error for double Start")
	}

	err = mon.Stop()
	if err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if mon.Running() {
		t.Error("expected not running after Stop")
	}
}

func TestMonitorStopIdempotent(t *testing.T) {
	mon, _, _ := setupMonitorTest(t)

	// Stop without start should not error
	err := mon.Stop()
	if err != nil {
		t.Fatalf("Stop without start: %v", err)
	}
}

// --- RunOnce Tests ---

func TestRunOnceEmpty(t *testing.T) {
	mon, _, _ := setupMonitorTest(t)
	ctx := context.Background()

	stats, err := mon.RunOnce(ctx)
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if stats.Checked != 0 || stats.Completed != 0 || stats.Failed != 0 {
		t.Errorf("expected zero stats, got checked=%d completed=%d failed=%d",
			stats.Checked, stats.Completed, stats.Failed)
	}
}

// --- Phase 0: Expired Waiters ---

func TestPhase0ExpiredWaiterResume(t *testing.T) {
	mon, d, repoRoot := setupMonitorTest(t)
	ctx := context.Background()

	// Set up a task with expired wait
	d.RegisterAgent(ctx, db.Agent{ID: "a1", Role: "implementer", Status: "working"})
	d.CreateTask(ctx, db.Task{ID: "t1", Title: "Test", Status: "running", Role: "implementer"})
	wt := filepath.Join(repoRoot, ".worktree", "t1")
	os.MkdirAll(wt, 0o755)
	d.AssignTask(ctx, "t1", "a1", wt, "feature/t1")
	d.SetBlackboard(ctx, "session_id:t1", "sess-123", "test")

	// Set wait_until in the past
	pastTime := time.Now().Unix() - 60
	d.SetBlackboard(ctx, "wait_until:t1", strconv.FormatInt(pastTime, 10), "test")
	d.SetBlackboard(ctx, "wait_reason:t1", "rate_limit", "test")

	stats, err := mon.RunOnce(ctx)
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if stats.WaitersResumed != 1 {
		t.Errorf("expected 1 waiter resumed, got %d", stats.WaitersResumed)
	}

	// wait_until should be cleaned up
	val, _ := d.GetBlackboardValue(ctx, "wait_until:t1")
	if val != "" {
		t.Error("expected wait_until to be cleaned up")
	}
}

func TestPhase0FutureWaiterNotResumed(t *testing.T) {
	mon, d, _ := setupMonitorTest(t)
	ctx := context.Background()

	// Set wait_until in the future
	futureTime := time.Now().Unix() + 3600
	d.SetBlackboard(ctx, "wait_until:t1", strconv.FormatInt(futureTime, 10), "test")
	d.SetBlackboard(ctx, "wait_reason:t1", "rate_limit", "test")

	stats, err := mon.RunOnce(ctx)
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if stats.WaitersResumed != 0 {
		t.Errorf("expected 0 waiters resumed, got %d", stats.WaitersResumed)
	}
}

// --- Phase 1: Check Agents ---

func TestPhase1DeadAgentWithSuccessResult(t *testing.T) {
	mon, d, repoRoot := setupMonitorTest(t)
	ctx := context.Background()

	// Set up git worktree with changes (setupMonitorTest already called gitInit)
	wt := filepath.Join(repoRoot, ".worktree", "t1")
	gitExec(t, repoRoot, "worktree", "add", wt, "-b", "feature/t1")

	// Make a real change
	os.WriteFile(filepath.Join(wt, "new-file.go"), []byte("package main"), 0o644)
	gitExec(t, wt, "add", ".")
	gitExec(t, wt, "commit", "-m", "add new file")

	// Register agent with a PID that doesn't exist, with CurrentTask set
	d.RegisterAgent(ctx, db.Agent{ID: "a1", Role: "implementer", Status: "working",
		CurrentTask: sql.NullString{String: "t1", Valid: true}})
	d.UpdateAgentPID(ctx, "a1", 999999) // PID that doesn't exist
	d.CreateTask(ctx, db.Task{ID: "t1", Title: "Test", Status: "running", Role: "implementer"})
	d.AssignTask(ctx, "t1", "a1", wt, "feature/t1")

	// Write a success result to log
	resultJSON := `{"type":"result","subtype":"success","result":"All done!"}`
	os.WriteFile(filepath.Join(mon.LogsDir, "t1.jsonl"), []byte(resultJSON+"\n"), 0o644)

	stats, err := mon.RunOnce(ctx)
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if stats.Checked != 1 {
		t.Errorf("expected 1 checked, got %d", stats.Checked)
	}
	if stats.Completed != 1 {
		t.Errorf("expected 1 completed, got %d", stats.Completed)
	}

	// Task should be done
	task, _ := d.GetTaskByID(ctx, "t1")
	if task.Status != "done" {
		t.Errorf("expected task status 'done', got %s", task.Status)
	}
}

func TestPhase1DeadAgentCrash(t *testing.T) {
	mon, d, repoRoot := setupMonitorTest(t)
	ctx := context.Background()

	d.RegisterAgent(ctx, db.Agent{ID: "a1", Role: "implementer", Status: "working",
		CurrentTask: sql.NullString{String: "t1", Valid: true}})
	d.UpdateAgentPID(ctx, "a1", 999999)
	d.CreateTask(ctx, db.Task{ID: "t1", Title: "Test", Status: "running", Role: "implementer"})
	wt := filepath.Join(repoRoot, ".worktree", "t1")
	os.MkdirAll(wt, 0o755)
	d.AssignTask(ctx, "t1", "a1", wt, "feature/t1")

	// No result in log — this is a crash
	os.WriteFile(filepath.Join(mon.LogsDir, "t1.jsonl"), []byte(`{"type":"init","session":"abc"}`+"\n"), 0o644)

	stats, err := mon.RunOnce(ctx)
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if stats.Failed != 1 {
		t.Errorf("expected 1 failed, got %d", stats.Failed)
	}

	// Failure type should be set
	ft, _ := d.GetBlackboardValue(ctx, "failure_type:t1")
	if ft == "" {
		t.Error("expected failure_type to be set")
	}
}

func TestPhase1AliveAgentTimeout(t *testing.T) {
	mon, d, _ := setupMonitorTest(t)
	ctx := context.Background()

	// Spawn a real process to have a valid alive PID
	sleepCmd := exec.Command("sleep", "60")
	sleepCmd.Start()
	pid := sleepCmd.Process.Pid
	defer func() {
		sleepCmd.Process.Kill()
		sleepCmd.Wait()
	}()

	d.RegisterAgent(ctx, db.Agent{ID: "a1", Role: "implementer", Status: "working",
		CurrentTask: sql.NullString{String: "t1", Valid: true}})
	d.UpdateAgentPID(ctx, "a1", pid)
	d.CreateTask(ctx, db.Task{ID: "t1", Title: "Test", Status: "running", Role: "implementer"})
	d.AssignTask(ctx, "t1", "a1", "", "")

	// Set timeout to 0 and spawn_time in the past
	d.SetBlackboard(ctx, "timeout:t1", "1", "test")
	d.SetBlackboard(ctx, "spawn_time:t1", strconv.FormatInt(time.Now().Unix()-100, 10), "test")

	stats, err := mon.RunOnce(ctx)
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if stats.TimedOut != 1 {
		t.Errorf("expected 1 timed out, got %d", stats.TimedOut)
	}

	// Task should be failed
	task, _ := d.GetTaskByID(ctx, "t1")
	if task.Status != "failed" {
		t.Errorf("expected task status 'failed', got %s", task.Status)
	}
}

func TestPhase1AliveAgentStuck(t *testing.T) {
	mon, d, _ := setupMonitorTest(t)
	ctx := context.Background()

	sleepCmd := exec.Command("sleep", "60")
	sleepCmd.Start()
	pid := sleepCmd.Process.Pid
	defer func() {
		sleepCmd.Process.Kill()
		sleepCmd.Wait()
	}()

	d.RegisterAgent(ctx, db.Agent{ID: "a1", Role: "implementer", Status: "working",
		CurrentTask: sql.NullString{String: "t1", Valid: true}})
	d.UpdateAgentPID(ctx, "a1", pid)
	d.CreateTask(ctx, db.Task{ID: "t1", Title: "Test", Status: "running", Role: "implementer"})
	d.AssignTask(ctx, "t1", "a1", "", "")

	// Create a log file with old modification time (11 min — past warning, below kill)
	logFile := filepath.Join(mon.LogsDir, "t1.jsonl")
	os.WriteFile(logFile, []byte(`{"type":"init"}`+"\n"), 0o644)
	oldTime := time.Now().Add(-11 * time.Minute)
	os.Chtimes(logFile, oldTime, oldTime)

	stats, err := mon.RunOnce(ctx)
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if stats.StuckWarned != 1 {
		t.Errorf("expected 1 stuck warning, got %d", stats.StuckWarned)
	}
}

func TestPhase1StuckEscalationKills(t *testing.T) {
	mon, d, _ := setupMonitorTest(t)
	ctx := context.Background()

	sleepCmd := exec.Command("sleep", "60")
	sleepCmd.Start()
	pid := sleepCmd.Process.Pid
	defer func() {
		sleepCmd.Process.Kill()
		sleepCmd.Wait()
	}()

	d.RegisterAgent(ctx, db.Agent{ID: "a1", Role: "implementer", Status: "working",
		CurrentTask: sql.NullString{String: "t1", Valid: true}})
	d.UpdateAgentPID(ctx, "a1", pid)
	d.CreateTask(ctx, db.Task{ID: "t1", Title: "Test", Status: "running", Role: "implementer"})
	d.AssignTask(ctx, "t1", "a1", "", "")

	// Create a log file with 31 min old modification time (past kill threshold)
	logFile := filepath.Join(mon.LogsDir, "t1.jsonl")
	os.WriteFile(logFile, []byte(`{"type":"init"}`+"\n"), 0o644)
	oldTime := time.Now().Add(-31 * time.Minute)
	os.Chtimes(logFile, oldTime, oldTime)

	stats, err := mon.RunOnce(ctx)
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}

	// Should have killed, not just warned
	if stats.StuckKilled != 1 {
		t.Errorf("expected 1 stuck kill, got %d", stats.StuckKilled)
	}
	if stats.StuckWarned != 0 {
		t.Errorf("expected 0 stuck warnings (kill supersedes warning), got %d", stats.StuckWarned)
	}

	// Task should be failed
	task, err := d.GetTaskByID(ctx, "t1")
	if err != nil {
		t.Fatalf("GetTaskByID: %v", err)
	}
	if task.Status != "failed" {
		t.Errorf("expected task status 'failed', got %q", task.Status)
	}
	if !strings.Contains(task.Result.String, "stuck_killed") {
		t.Errorf("expected result to contain 'stuck_killed', got %q", task.Result.String)
	}

	// Agent should be dead
	a, _ := d.GetAgentByTask(ctx, "t1")
	if a != nil && a.Status != "dead" {
		t.Errorf("expected agent status 'dead', got %q", a.Status)
	}

	// Blackboard markers should be set
	killed, _ := d.GetBlackboardValue(ctx, "stuck_killed:t1")
	if killed != "1" {
		t.Errorf("expected stuck_killed blackboard marker, got %q", killed)
	}
	failType, _ := d.GetBlackboardValue(ctx, "failure_type:t1")
	if failType != "stuck_killed" {
		t.Errorf("expected failure_type 'stuck_killed', got %q", failType)
	}
}

// G135: Stuck escalation should NOT kill if stderr shows rate_limit.
func TestPhase1StuckEscalationSkipsRateLimit(t *testing.T) {
	mon, d, _ := setupMonitorTest(t)
	ctx := context.Background()

	sleepCmd := exec.Command("sleep", "60")
	sleepCmd.Start()
	pid := sleepCmd.Process.Pid
	defer func() {
		sleepCmd.Process.Kill()
		sleepCmd.Wait()
	}()

	d.RegisterAgent(ctx, db.Agent{ID: "a1", Role: "implementer", Status: "working",
		CurrentTask: sql.NullString{String: "t1", Valid: true}})
	d.UpdateAgentPID(ctx, "a1", pid)
	d.CreateTask(ctx, db.Task{ID: "t1", Title: "Test", Status: "running", Role: "implementer"})
	d.AssignTask(ctx, "t1", "a1", "", "")

	// Create stale log file (past kill threshold — 31 min)
	logFile := filepath.Join(mon.LogsDir, "t1.jsonl")
	os.WriteFile(logFile, []byte(`{"type":"init"}`+"\n"), 0o644)
	oldTime := time.Now().Add(-31 * time.Minute)
	os.Chtimes(logFile, oldTime, oldTime)

	// Write rate_limit error to stderr
	stderrFile := filepath.Join(mon.LogsDir, "t1.stderr")
	os.WriteFile(stderrFile, []byte("Error: 429 rate_limit_error: too many requests\n"), 0o644)

	stats, err := mon.RunOnce(ctx)
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}

	// Should NOT have killed — rate limit detected
	if stats.StuckKilled != 0 {
		t.Errorf("expected 0 stuck kills (rate_limit skip), got %d", stats.StuckKilled)
	}

	// Task should still be active (not failed)
	task, err := d.GetTaskByID(ctx, "t1")
	if err != nil {
		t.Fatalf("GetTaskByID: %v", err)
	}
	if task.Status == "failed" {
		t.Errorf("expected task NOT failed, got %q", task.Status)
	}

	// stuck_killed marker should NOT be set
	killed, _ := d.GetBlackboardValue(ctx, "stuck_killed:t1")
	if killed != "" {
		t.Errorf("expected no stuck_killed marker, got %q", killed)
	}
}

func TestPhase1StuckEscalationCustomTimeout(t *testing.T) {
	mon, d, _ := setupMonitorTest(t)
	ctx := context.Background()

	sleepCmd := exec.Command("sleep", "60")
	sleepCmd.Start()
	pid := sleepCmd.Process.Pid
	defer func() {
		sleepCmd.Process.Kill()
		sleepCmd.Wait()
	}()

	d.RegisterAgent(ctx, db.Agent{ID: "a1", Role: "implementer", Status: "working",
		CurrentTask: sql.NullString{String: "t1", Valid: true}})
	d.UpdateAgentPID(ctx, "a1", pid)
	d.CreateTask(ctx, db.Task{ID: "t1", Title: "Test", Status: "running", Role: "implementer"})
	d.AssignTask(ctx, "t1", "a1", "", "")

	// Set custom kill timeout to 7 minutes (420 seconds)
	d.SetBlackboard(ctx, "conductor:stuck_kill_timeout", "420", "test")

	// Create a log file 8 min old (past custom 7min threshold, but below default 10min)
	logFile := filepath.Join(mon.LogsDir, "t1.jsonl")
	os.WriteFile(logFile, []byte(`{"type":"init"}`+"\n"), 0o644)
	oldTime := time.Now().Add(-8 * time.Minute)
	os.Chtimes(logFile, oldTime, oldTime)

	stats, err := mon.RunOnce(ctx)
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}

	// Custom threshold should have triggered the kill
	if stats.StuckKilled != 1 {
		t.Errorf("expected 1 stuck kill with custom timeout, got %d", stats.StuckKilled)
	}
}

func TestPhase1StuckEscalationIdempotent(t *testing.T) {
	mon, d, _ := setupMonitorTest(t)
	ctx := context.Background()

	sleepCmd := exec.Command("sleep", "60")
	sleepCmd.Start()
	pid := sleepCmd.Process.Pid
	defer func() {
		sleepCmd.Process.Kill()
		sleepCmd.Wait()
	}()

	d.RegisterAgent(ctx, db.Agent{ID: "a1", Role: "implementer", Status: "working",
		CurrentTask: sql.NullString{String: "t1", Valid: true}})
	d.UpdateAgentPID(ctx, "a1", pid)
	d.CreateTask(ctx, db.Task{ID: "t1", Title: "Test", Status: "running", Role: "implementer"})
	d.AssignTask(ctx, "t1", "a1", "", "")

	// Pre-set the stuck_killed marker (simulating a previous escalation)
	d.SetBlackboard(ctx, "stuck_killed:t1", "1", "test")

	logFile := filepath.Join(mon.LogsDir, "t1.jsonl")
	os.WriteFile(logFile, []byte(`{"type":"init"}`+"\n"), 0o644)
	oldTime := time.Now().Add(-31 * time.Minute)
	os.Chtimes(logFile, oldTime, oldTime)

	stats, err := mon.RunOnce(ctx)
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}

	// Should NOT kill again — idempotent
	if stats.StuckKilled != 0 {
		t.Errorf("expected 0 stuck kills (idempotent), got %d", stats.StuckKilled)
	}
}

func TestWorktreeHasCommits(t *testing.T) {
	dir := t.TempDir()
	gitExec(t, dir, "init")
	gitExec(t, dir, "checkout", "-b", "test-branch")
	os.WriteFile(filepath.Join(dir, "file.txt"), []byte("hello"), 0o644)
	gitExec(t, dir, "add", ".")
	gitExec(t, dir, "commit", "-m", "init")

	// After the initial commit, there's 1 commit not on remotes
	if !worktreeHasCommits(dir) {
		t.Error("expected worktreeHasCommits=true for repo with local commits")
	}
}

func TestWorktreeHasCommitsNoRepo(t *testing.T) {
	dir := t.TempDir()
	// Not a git repo
	if worktreeHasCommits(dir) {
		t.Error("expected worktreeHasCommits=false for non-git dir")
	}
}

// --- Phase 2: Auto-Spawn ---

func TestPhase2AutoSpawnDisabledWithoutConductor(t *testing.T) {
	mon, _, _ := setupMonitorTest(t)
	ctx := context.Background()

	// No conductor:active set
	stats, err := mon.RunOnce(ctx)
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if stats.AutoSpawned != 0 {
		t.Errorf("expected 0 auto-spawned without conductor, got %d", stats.AutoSpawned)
	}
}

func TestPhase2AutoSpawnWithConductor(t *testing.T) {
	mon, d, _ := setupMonitorTest(t)
	ctx := context.Background()

	d.SetBlackboard(ctx, "conductor:active", "1", "test")
	d.SetBlackboard(ctx, "conductor:max_parallel", "3", "test")
	d.SetBlackboard(ctx, "conductor:task_ids", `["t1","t2"]`, "test")

	d.CreateTask(ctx, db.Task{ID: "t1", Title: "Task 1", Status: "pending", Role: "implementer"})
	d.CreateTask(ctx, db.Task{ID: "t2", Title: "Task 2", Status: "pending", Role: "implementer"})

	stats, err := mon.RunOnce(ctx)
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if stats.AutoSpawned != 2 {
		t.Errorf("expected 2 auto-spawned, got %d", stats.AutoSpawned)
	}
}

func TestPhase2AutoSpawnRespectsMaxParallel(t *testing.T) {
	mon, d, _ := setupMonitorTest(t)
	ctx := context.Background()

	d.SetBlackboard(ctx, "conductor:active", "1", "test")
	d.SetBlackboard(ctx, "conductor:max_parallel", "2", "test")
	d.SetBlackboard(ctx, "conductor:task_ids", `["t-running","t1","t2"]`, "test")

	// One already running
	d.RegisterAgent(ctx, db.Agent{ID: "a1", Role: "implementer", Status: "working"})
	d.CreateTask(ctx, db.Task{ID: "t-running", Title: "Running", Status: "running", Role: "implementer"})

	d.CreateTask(ctx, db.Task{ID: "t1", Title: "Task 1", Status: "pending", Role: "implementer"})
	d.CreateTask(ctx, db.Task{ID: "t2", Title: "Task 2", Status: "pending", Role: "implementer"})

	stats, err := mon.RunOnce(ctx)
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	// max=2, 1 running → only 1 slot
	if stats.AutoSpawned != 1 {
		t.Errorf("expected 1 auto-spawned (1 slot), got %d", stats.AutoSpawned)
	}
}

func TestPhase2AutoSpawnSkipsBatchActive(t *testing.T) {
	mon, d, _ := setupMonitorTest(t)
	ctx := context.Background()

	d.SetBlackboard(ctx, "conductor:active", "1", "test")
	d.SetBlackboard(ctx, "batch_spawning:active", "1", "test")
	d.CreateTask(ctx, db.Task{ID: "t1", Title: "Task", Status: "pending", Role: "implementer"})

	stats, err := mon.RunOnce(ctx)
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if stats.AutoSpawned != 0 {
		t.Errorf("expected 0 auto-spawned during batch, got %d", stats.AutoSpawned)
	}
}

// --- Phase 3: Auto-Merge ---

func TestPhase3AutoMergeNotTriggeredWhenConductorAlive(t *testing.T) {
	mon, d, _ := setupMonitorTest(t)
	ctx := context.Background()

	sleepCmd := exec.Command("sleep", "60")
	sleepCmd.Start()
	defer func() {
		sleepCmd.Process.Kill()
		sleepCmd.Wait()
	}()

	d.SetBlackboard(ctx, "conductor:active", "1", "test")
	d.SetBlackboard(ctx, "conductor:pid", strconv.Itoa(sleepCmd.Process.Pid), "test")

	// Should not trigger merge
	stats, err := mon.RunOnce(ctx)
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	_ = stats // no merge stats to check, just verifying no panic
}

func TestPhase3AutoMergeNotTriggeredWithPendingTasks(t *testing.T) {
	mon, d, _ := setupMonitorTest(t)
	ctx := context.Background()

	d.SetBlackboard(ctx, "conductor:active", "1", "test")
	d.SetBlackboard(ctx, "conductor:pid", "999999", "test") // dead PID
	d.SetBlackboard(ctx, "conductor:task_ids", `["t1","t2"]`, "test")

	d.CreateTask(ctx, db.Task{ID: "t1", Title: "Done", Status: "done", Role: "implementer"})
	d.CreateTask(ctx, db.Task{ID: "t2", Title: "Pending", Status: "pending", Role: "implementer"})

	// Should not merge because there are pending tasks
	_, err := mon.RunOnce(ctx)
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}

	// conductor:active should still be set
	active, _ := d.GetBlackboardValue(ctx, "conductor:active")
	if active != "1" {
		t.Error("conductor should still be active with pending tasks")
	}
}

// --- Helpers Tests ---

func TestGetSessionTaskIDs(t *testing.T) {
	mon, d, _ := setupMonitorTest(t)
	ctx := context.Background()

	d.SetBlackboard(ctx, "conductor:task_ids", `["t1","t2","t3"]`, "test")

	ids := mon.getSessionTaskIDs(ctx)
	if len(ids) != 3 {
		t.Fatalf("expected 3 IDs, got %d", len(ids))
	}
	if ids[0] != "t1" || ids[1] != "t2" || ids[2] != "t3" {
		t.Errorf("unexpected IDs: %v", ids)
	}
}

func TestGetSessionTaskIDsEmpty(t *testing.T) {
	mon, _, _ := setupMonitorTest(t)
	ctx := context.Background()

	ids := mon.getSessionTaskIDs(ctx)
	if ids != nil {
		t.Errorf("expected nil for empty, got %v", ids)
	}
}

func TestGetSessionTaskIDsEmptyArray(t *testing.T) {
	mon, d, _ := setupMonitorTest(t)
	ctx := context.Background()

	d.SetBlackboard(ctx, "conductor:task_ids", `[]`, "test")

	ids := mon.getSessionTaskIDs(ctx)
	if ids != nil {
		t.Errorf("expected nil for empty array, got %v", ids)
	}
}

func TestCheckLogResult(t *testing.T) {
	dir := t.TempDir()

	// Success result
	content := `{"type":"init","session":"abc"}
{"type":"assistant","content":"working..."}
{"type":"result","subtype":"success","result":"All done!"}
`
	os.WriteFile(filepath.Join(dir, "t1.jsonl"), []byte(content), 0o644)

	has, success := agent.CheckLogResult(dir, "t1")
	if !has {
		t.Error("expected hasResult=true")
	}
	if !success {
		t.Error("expected isSuccess=true")
	}
}

func TestCheckLogResultFailure(t *testing.T) {
	dir := t.TempDir()

	content := `{"type":"result","subtype":"error","error":"something went wrong"}
`
	os.WriteFile(filepath.Join(dir, "t1.jsonl"), []byte(content), 0o644)

	has, success := agent.CheckLogResult(dir, "t1")
	if !has {
		t.Error("expected hasResult=true")
	}
	if success {
		t.Error("expected isSuccess=false for error result")
	}
}

func TestCheckLogResultMissing(t *testing.T) {
	dir := t.TempDir()

	has, success := agent.CheckLogResult(dir, "nonexistent")
	if has {
		t.Error("expected hasResult=false for missing file")
	}
	if success {
		t.Error("expected isSuccess=false for missing file")
	}
}

func TestCheckLogResultNoResult(t *testing.T) {
	dir := t.TempDir()

	content := `{"type":"init","session":"abc"}
{"type":"assistant","content":"working..."}
`
	os.WriteFile(filepath.Join(dir, "t1.jsonl"), []byte(content), 0o644)

	has, _ := agent.CheckLogResult(dir, "t1")
	if has {
		t.Error("expected hasResult=false for no result line")
	}
}

// --- Integration: Full Cycle ---

func TestFullCycleDeadAgentCompleted(t *testing.T) {
	mon, d, repoRoot := setupMonitorTest(t)
	ctx := context.Background()

	// Set up worktree with changes
	wt := filepath.Join(repoRoot, ".worktree", "t1")
	gitExec(t, repoRoot, "worktree", "add", wt, "-b", "feature/t1")
	os.WriteFile(filepath.Join(wt, "main.go"), []byte("package main\nfunc main(){}"), 0o644)
	gitExec(t, wt, "add", ".")
	gitExec(t, wt, "commit", "-m", "impl")

	d.RegisterAgent(ctx, db.Agent{ID: "a1", Role: "implementer", Status: "working",
		CurrentTask: sql.NullString{String: "t1", Valid: true}})
	d.UpdateAgentPID(ctx, "a1", 999999)
	d.CreateTask(ctx, db.Task{ID: "t1", Title: "Build feature", Status: "running", Role: "implementer"})
	d.AssignTask(ctx, "t1", "a1", wt, "feature/t1")

	resultJSON := `{"type":"result","subtype":"success","result":"Implemented feature"}`
	os.WriteFile(filepath.Join(mon.LogsDir, "t1.jsonl"), []byte(resultJSON+"\n"), 0o644)

	stats, err := mon.RunOnce(ctx)
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}

	if stats.Checked != 1 {
		t.Errorf("expected 1 checked, got %d", stats.Checked)
	}
	if stats.Completed != 1 {
		t.Errorf("expected 1 completed, got %d", stats.Completed)
	}
	if stats.Validated != 1 {
		t.Errorf("expected 1 validated, got %d", stats.Validated)
	}

	// Verify task is done
	task, _ := d.GetTaskByID(ctx, "t1")
	if task.Status != "done" {
		t.Errorf("expected 'done', got %s", task.Status)
	}

	// Verify result_summary in blackboard
	rs, _ := d.GetBlackboardValue(ctx, "result_summary:t1")
	if rs == "" {
		t.Error("expected result_summary in blackboard")
	}
}

func TestFullCycleAutoSpawnAndComplete(t *testing.T) {
	mon, d, _ := setupMonitorTest(t)
	ctx := context.Background()

	// Conductor mode with pending task
	d.SetBlackboard(ctx, "conductor:active", "1", "test")
	d.SetBlackboard(ctx, "conductor:max_parallel", "5", "test")
	d.SetBlackboard(ctx, "conductor:task_ids", `["t1"]`, "test")

	d.CreateTask(ctx, db.Task{ID: "t1", Title: "Task 1", Status: "pending", Role: "implementer"})

	stats, err := mon.RunOnce(ctx)
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if stats.AutoSpawned != 1 {
		t.Errorf("expected 1 auto-spawned, got %d", stats.AutoSpawned)
	}

	// Verify task was started
	task, _ := d.GetTaskByID(ctx, "t1")
	if task.Status != "running" {
		t.Errorf("expected task status 'running', got %s", task.Status)
	}
}

func TestCleanup(t *testing.T) {
	mon, d, _ := setupMonitorTest(t)
	ctx := context.Background()

	mon.cleanup(ctx)

	// Verify heartbeat was written
	hb, _ := d.GetBlackboardValue(ctx, "monitor:heartbeat")
	if hb == "" {
		t.Error("expected monitor heartbeat in blackboard")
	}
}

func TestCycleStatsHasDuration(t *testing.T) {
	mon, _, _ := setupMonitorTest(t)
	ctx := context.Background()

	stats, err := mon.RunOnce(ctx)
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if stats.Duration == 0 {
		t.Error("expected non-zero duration")
	}
}

func TestPhase1DeadAgentValidationFailure(t *testing.T) {
	mon, d, repoRoot := setupMonitorTest(t)
	ctx := context.Background()

	// Create worktree but no changes (validation will fail for implementer)
	wt := filepath.Join(repoRoot, ".worktree", "t1")
	gitExec(t, repoRoot, "worktree", "add", wt, "-b", "feature/t1")

	d.RegisterAgent(ctx, db.Agent{ID: "a1", Role: "implementer", Status: "working",
		CurrentTask: sql.NullString{String: "t1", Valid: true}})
	d.UpdateAgentPID(ctx, "a1", 999999)
	d.CreateTask(ctx, db.Task{ID: "t1", Title: "Test", Status: "running", Role: "implementer"})
	d.AssignTask(ctx, "t1", "a1", wt, "feature/t1")

	// Write success result but no actual file changes — validation should fail
	resultJSON := `{"type":"result","subtype":"success","result":"Done"}`
	os.WriteFile(filepath.Join(mon.LogsDir, "t1.jsonl"), []byte(resultJSON+"\n"), 0o644)

	stats, err := mon.RunOnce(ctx)
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if stats.Failed != 1 {
		t.Errorf("expected 1 failed (validation), got %d", stats.Failed)
	}

	// failure_type should be set
	ft, _ := d.GetBlackboardValue(ctx, "failure_type:t1")
	if ft != "validation_failure" {
		t.Errorf("expected failure_type 'validation_failure', got %q", ft)
	}
}

func TestPhase1DeadAgentValidationFailureTriggersRefinement(t *testing.T) {
	mon, d, repoRoot := setupMonitorTest(t)
	ctx := context.Background()

	// Create worktree but no changes (validation will fail for implementer)
	wt := filepath.Join(repoRoot, ".worktree", "t-ref-1")
	gitExec(t, repoRoot, "worktree", "add", wt, "-b", "feature/t-ref-1")

	d.RegisterAgent(ctx, db.Agent{ID: "a-ref-1", Role: "implementer", Status: "working",
		CurrentTask: sql.NullString{String: "t-ref-1", Valid: true}})
	d.UpdateAgentPID(ctx, "a-ref-1", 999999)
	d.CreateTask(ctx, db.Task{ID: "t-ref-1", Title: "Test Refinement", Status: "running", Role: "implementer",
		Description: sql.NullString{String: "Implement something", Valid: true}})
	d.AssignTask(ctx, "t-ref-1", "a-ref-1", wt, "feature/t-ref-1")

	// Write success result but no actual changes → implementer_no_changes
	resultJSON := `{"type":"result","subtype":"success","result":"Done"}`
	os.WriteFile(filepath.Join(mon.LogsDir, "t-ref-1.jsonl"), []byte(resultJSON+"\n"), 0o644)

	stats, err := mon.RunOnce(ctx)
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if stats.Failed != 1 {
		t.Errorf("expected 1 failed, got %d", stats.Failed)
	}

	// The refinement path should have been triggered, incrementing retried
	if stats.Retried != 1 {
		t.Errorf("expected 1 retried (via refinement), got %d", stats.Retried)
	}

	// Verify refinement counter was incremented in blackboard
	ref, _ := d.GetBlackboardValue(ctx, "refinement:t-ref-1")
	if ref != "1" {
		t.Errorf("expected refinement=1, got %q", ref)
	}

	// Verify critique was stored
	critique, _ := d.GetBlackboardValue(ctx, "critique:t-ref-1")
	if critique == "" {
		t.Error("expected critique to be stored in blackboard")
	}
}

func TestPhase1DeadAgentValidationFailureExhaustedRefinementFallsBack(t *testing.T) {
	mon, d, repoRoot := setupMonitorTest(t)
	ctx := context.Background()

	wt := filepath.Join(repoRoot, ".worktree", "t-ref-ex")
	gitExec(t, repoRoot, "worktree", "add", wt, "-b", "feature/t-ref-ex")

	d.RegisterAgent(ctx, db.Agent{ID: "a-ref-ex", Role: "implementer", Status: "working",
		CurrentTask: sql.NullString{String: "t-ref-ex", Valid: true}})
	d.UpdateAgentPID(ctx, "a-ref-ex", 999999)
	d.CreateTask(ctx, db.Task{ID: "t-ref-ex", Title: "Test Exhausted", Status: "running", Role: "implementer"})
	d.AssignTask(ctx, "t-ref-ex", "a-ref-ex", wt, "feature/t-ref-ex")

	// Set refinement count already at max
	d.SetBlackboard(ctx, "refinement:t-ref-ex", "2", "test")

	// Write success result but no changes
	resultJSON := `{"type":"result","subtype":"success","result":"Done"}`
	os.WriteFile(filepath.Join(mon.LogsDir, "t-ref-ex.jsonl"), []byte(resultJSON+"\n"), 0o644)

	stats, err := mon.RunOnce(ctx)
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}

	// Should still retry via the generic respawn path (not refinement)
	if stats.Failed != 1 {
		t.Errorf("expected 1 failed, got %d", stats.Failed)
	}
	if stats.Retried != 1 {
		t.Errorf("expected 1 retried (via generic respawn), got %d", stats.Retried)
	}

	// Refinement counter should NOT have been incremented beyond 2
	ref, _ := d.GetBlackboardValue(ctx, "refinement:t-ref-ex")
	if ref != "2" {
		t.Errorf("expected refinement=2 (unchanged), got %q", ref)
	}
}

func TestRetryOrRefineNonRefinableFailure(t *testing.T) {
	mon, d, _ := setupMonitorTest(t)
	ctx := context.Background()

	// Create a task that can be respawned
	d.CreateTask(ctx, db.Task{ID: "t-nonref", Title: "Non-refinable", Status: "failed", Role: "implementer"})

	stats := &phase1Stats{}
	mon.retryOrRefine(ctx, "t-nonref", "timeout", stats)

	// Should use generic respawn, not refinement
	if stats.retried != 1 {
		t.Errorf("expected 1 retried via generic path, got %d", stats.retried)
	}
}

func TestCrashedAgentRateLimitSetsWaiter(t *testing.T) {
	mon, d, repoRoot := setupMonitorTest(t)
	ctx := context.Background()

	d.RegisterAgent(ctx, db.Agent{ID: "a1", Role: "implementer", Status: "working",
		CurrentTask: sql.NullString{String: "t1", Valid: true}})
	d.UpdateAgentPID(ctx, "a1", 999999)
	d.CreateTask(ctx, db.Task{ID: "t1", Title: "Test", Status: "running", Role: "implementer"})
	wt := filepath.Join(repoRoot, ".worktree", "t1")
	os.MkdirAll(wt, 0o755)
	d.AssignTask(ctx, "t1", "a1", wt, "feature/t1")

	// Write stderr with rate limit pattern
	stderrContent := "Error: rate limit exceeded. Please wait 30 seconds before trying again."
	os.WriteFile(filepath.Join(mon.LogsDir, "t1.stderr"), []byte(stderrContent), 0o644)
	os.WriteFile(filepath.Join(mon.LogsDir, "t1.jsonl"), []byte(`{"type":"init"}`+"\n"), 0o644)

	_, err := mon.RunOnce(ctx)
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}

	// Check failure_type
	ft, _ := d.GetBlackboardValue(ctx, "failure_type:t1")
	if ft != "rate_limit" {
		t.Errorf("expected failure_type 'rate_limit', got %q", ft)
	}

	// Should have wait_until set
	wu, _ := d.GetBlackboardValue(ctx, "wait_until:t1")
	if wu == "" {
		t.Error("expected wait_until to be set for rate limit")
	}
}

// --- Error Propagation Tests ---

func TestCycleStatsHasErrors(t *testing.T) {
	stats := &CycleStats{}
	if stats.HasErrors() {
		t.Error("expected HasErrors=false for empty stats")
	}

	stats.Errors = append(stats.Errors, fmt.Errorf("test error"))
	if !stats.HasErrors() {
		t.Error("expected HasErrors=true after adding error")
	}
}

func TestRunOnceReturnsPhaseErrors(t *testing.T) {
	mon, d, _ := setupMonitorTest(t)
	ctx := context.Background()

	// Close the DB to force phase errors
	d.Close()

	stats, err := mon.RunOnce(ctx)
	if err == nil {
		t.Fatal("expected RunOnce to return error with closed DB")
	}
	if !stats.HasErrors() {
		t.Error("expected stats.Errors to be populated")
	}
	if len(stats.Errors) == 0 {
		t.Error("expected at least one error in stats.Errors")
	}
	if !strings.Contains(err.Error(), "error(s)") {
		t.Errorf("expected combined error message, got: %s", err.Error())
	}
}

func TestRunOnceErrorsContainPhaseLabels(t *testing.T) {
	mon, d, _ := setupMonitorTest(t)
	ctx := context.Background()

	// Close DB to trigger phase0 error (ListBlackboardByPrefix fails)
	d.Close()

	stats, _ := mon.RunOnce(ctx)
	if len(stats.Errors) == 0 {
		t.Fatal("expected phase errors")
	}
	// First error should be from phase0
	if !strings.HasPrefix(stats.Errors[0].Error(), "phase0:") {
		t.Errorf("expected first error to start with 'phase0:', got: %s", stats.Errors[0].Error())
	}
}

func TestAutoMergeFailurePreservesConductorState(t *testing.T) {
	mon, d, _ := setupMonitorTest(t)
	ctx := context.Background()

	// Set up conductor as dead with all tasks done
	d.SetBlackboard(ctx, "conductor:active", "1", "test")
	d.SetBlackboard(ctx, "conductor:pid", "999999", "test") // dead PID
	d.SetBlackboard(ctx, "conductor:task_ids", `["t1"]`, "test")

	d.CreateTask(ctx, db.Task{ID: "t1", Title: "Done Task", Status: "done", Role: "implementer"})

	// Set MergeFunc to always fail
	mon.MergeFunc = func(ctx context.Context, testCmd string, review bool) error {
		return fmt.Errorf("merge conflict in main.go")
	}

	stats, err := mon.RunOnce(ctx)
	if err == nil {
		t.Fatal("expected RunOnce to return error when merge fails")
	}
	if !stats.HasErrors() {
		t.Error("expected stats.Errors to be populated")
	}

	// conductor:active should still be set (NOT deactivated)
	active, _ := d.GetBlackboardValue(ctx, "conductor:active")
	if active != "1" {
		t.Error("conductor should still be active after merge failure")
	}

	// merge:failed flag should be set
	mergeFailed, _ := d.GetBlackboardValue(ctx, "merge:failed")
	if mergeFailed == "" {
		t.Error("expected merge:failed blackboard entry to be set")
	}
	if !strings.Contains(mergeFailed, "merge conflict") {
		t.Errorf("expected merge:failed to contain error detail, got: %s", mergeFailed)
	}
}

func TestAutoMergeSuccessDeactivatesConductor(t *testing.T) {
	mon, d, _ := setupMonitorTest(t)
	ctx := context.Background()

	d.SetBlackboard(ctx, "conductor:active", "1", "test")
	d.SetBlackboard(ctx, "conductor:pid", "999999", "test")
	d.SetBlackboard(ctx, "conductor:task_ids", `["t1"]`, "test")

	d.CreateTask(ctx, db.Task{ID: "t1", Title: "Done Task", Status: "done", Role: "implementer"})

	// Set MergeFunc to succeed
	mon.MergeFunc = func(ctx context.Context, testCmd string, review bool) error {
		return nil
	}

	stats, err := mon.RunOnce(ctx)
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if stats.HasErrors() {
		t.Errorf("expected no errors, got: %v", stats.Errors)
	}

	// conductor:active should be deactivated
	active, _ := d.GetBlackboardValue(ctx, "conductor:active")
	if active == "1" {
		t.Error("conductor should be deactivated after successful merge")
	}
}

func TestAutoMergeFailedEventLogged(t *testing.T) {
	mon, d, _ := setupMonitorTest(t)
	ctx := context.Background()

	d.SetBlackboard(ctx, "conductor:active", "1", "test")
	d.SetBlackboard(ctx, "conductor:pid", "999999", "test")
	d.SetBlackboard(ctx, "conductor:task_ids", `["t1"]`, "test")

	d.CreateTask(ctx, db.Task{ID: "t1", Title: "Done Task", Status: "done", Role: "implementer"})

	mon.MergeFunc = func(ctx context.Context, testCmd string, review bool) error {
		return fmt.Errorf("test failure")
	}

	mon.RunOnce(ctx)

	// Check that auto_merge_failed event was logged
	events, err := d.RecentEvents(ctx, 20)
	if err != nil {
		t.Fatalf("RecentEvents: %v", err)
	}
	found := false
	for _, e := range events {
		if e.EventType == "auto_merge_failed" {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected auto_merge_failed event to be logged")
	}
}

func TestRunOnceStatsPopulatedEvenWithErrors(t *testing.T) {
	mon, d, _ := setupMonitorTest(t)
	ctx := context.Background()

	// Set up conductor with done tasks to trigger auto-merge
	d.SetBlackboard(ctx, "conductor:active", "1", "test")
	d.SetBlackboard(ctx, "conductor:pid", "999999", "test")
	d.SetBlackboard(ctx, "conductor:task_ids", `["t1"]`, "test")
	d.CreateTask(ctx, db.Task{ID: "t1", Title: "Done", Status: "done", Role: "implementer"})

	// Merge will fail
	mon.MergeFunc = func(ctx context.Context, testCmd string, review bool) error {
		return fmt.Errorf("conflict")
	}

	stats, err := mon.RunOnce(ctx)
	if err == nil {
		t.Fatal("expected error from RunOnce")
	}

	// stats should still have duration and other fields populated
	if stats.Duration == 0 {
		t.Error("expected non-zero duration even with errors")
	}
	// The error count should be 1 (only phase3)
	if len(stats.Errors) != 1 {
		t.Errorf("expected 1 error, got %d", len(stats.Errors))
	}
}

// --- Step 1: strconv Kill Bug Tests ---

func TestPhase1AliveAgentMalformedTimeout(t *testing.T) {
	mon, d, _ := setupMonitorTest(t)
	ctx := context.Background()

	// Spawn a real process to have a valid alive PID
	sleepCmd := exec.Command("sleep", "60")
	sleepCmd.Start()
	pid := sleepCmd.Process.Pid
	defer func() {
		sleepCmd.Process.Kill()
		sleepCmd.Wait()
	}()

	d.RegisterAgent(ctx, db.Agent{ID: "a1", Role: "implementer", Status: "working",
		CurrentTask: sql.NullString{String: "t1", Valid: true}})
	d.UpdateAgentPID(ctx, "a1", pid)
	d.CreateTask(ctx, db.Task{ID: "t1", Title: "Test", Status: "running", Role: "implementer"})
	d.AssignTask(ctx, "t1", "a1", "", "")

	// Set malformed timeout — this must NOT kill the agent
	d.SetBlackboard(ctx, "timeout:t1", "abc", "test")
	d.SetBlackboard(ctx, "spawn_time:t1", "not-a-number", "test")

	stats, err := mon.RunOnce(ctx)
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if stats.TimedOut != 0 {
		t.Errorf("expected 0 timed out (malformed timeout should be skipped), got %d", stats.TimedOut)
	}

	// Task should NOT be failed (AssignTask sets status to 'assigned')
	task, _ := d.GetTaskByID(ctx, "t1")
	if task.Status == "failed" {
		t.Error("task should NOT be failed — malformed timeout must not kill the agent")
	}
}

func TestPhase1AliveAgentZeroTimeout(t *testing.T) {
	mon, d, _ := setupMonitorTest(t)
	ctx := context.Background()

	sleepCmd := exec.Command("sleep", "60")
	sleepCmd.Start()
	pid := sleepCmd.Process.Pid
	defer func() {
		sleepCmd.Process.Kill()
		sleepCmd.Wait()
	}()

	d.RegisterAgent(ctx, db.Agent{ID: "a1", Role: "implementer", Status: "working",
		CurrentTask: sql.NullString{String: "t1", Valid: true}})
	d.UpdateAgentPID(ctx, "a1", pid)
	d.CreateTask(ctx, db.Task{ID: "t1", Title: "Test", Status: "running", Role: "implementer"})
	d.AssignTask(ctx, "t1", "a1", "", "")

	// Set timeout to 0 — this must NOT kill the agent (zero-value guard)
	d.SetBlackboard(ctx, "timeout:t1", "0", "test")
	d.SetBlackboard(ctx, "spawn_time:t1", strconv.FormatInt(time.Now().Unix()-100, 10), "test")

	stats, err := mon.RunOnce(ctx)
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if stats.TimedOut != 0 {
		t.Errorf("expected 0 timed out (zero timeout should be skipped), got %d", stats.TimedOut)
	}

	// Task should NOT be failed (AssignTask sets status to 'assigned')
	task, _ := d.GetTaskByID(ctx, "t1")
	if task.Status == "failed" {
		t.Error("task should NOT be failed — zero timeout must not kill the agent")
	}
}

// --- Step 3: Cascade Fail Tests ---

func TestPhase1_5CascadeFailBlockedTasks(t *testing.T) {
	mon, d, _ := setupMonitorTest(t)
	ctx := context.Background()

	d.SetBlackboard(ctx, "conductor:active", "1", "test")
	d.SetBlackboard(ctx, "conductor:task_ids", `["t1","t2"]`, "test")

	// t1 failed, t2 blocked by t1
	d.CreateTask(ctx, db.Task{ID: "t1", Title: "Researcher", Status: "failed", Role: "researcher",
		BlockedBy: sql.NullString{String: "[]", Valid: true}})
	d.CreateTask(ctx, db.Task{ID: "t2", Title: "Implementer", Status: "pending", Role: "implementer",
		BlockedBy: sql.NullString{String: `["t1"]`, Valid: true}})

	stats, err := mon.RunOnce(ctx)
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}

	// t2 should be cascade-failed
	task, _ := d.GetTaskByID(ctx, "t2")
	if task.Status != "failed" {
		t.Errorf("expected t2 to be cascade-failed, got status %s", task.Status)
	}

	ft, _ := d.GetBlackboardValue(ctx, "failure_type:t2")
	if ft != "cascade_fail" {
		t.Errorf("expected failure_type 'cascade_fail', got %q", ft)
	}

	// Stats should reflect the cascade
	if stats.Failed != 1 {
		t.Errorf("expected 1 cascade-failed task in stats, got %d", stats.Failed)
	}
}

func TestPhase1_5CascadeDoesNotFireWhenBlockerStillRunning(t *testing.T) {
	mon, d, _ := setupMonitorTest(t)
	ctx := context.Background()

	d.SetBlackboard(ctx, "conductor:active", "1", "test")
	d.SetBlackboard(ctx, "conductor:task_ids", `["t1","t2","t3"]`, "test")

	// t1 failed, t2 running, t3 blocked by both t1 and t2
	d.CreateTask(ctx, db.Task{ID: "t1", Title: "Task 1", Status: "failed", Role: "researcher"})
	d.CreateTask(ctx, db.Task{ID: "t2", Title: "Task 2", Status: "running", Role: "researcher"})
	d.CreateTask(ctx, db.Task{ID: "t3", Title: "Task 3", Status: "pending", Role: "implementer",
		BlockedBy: sql.NullString{String: `["t1","t2"]`, Valid: true}})

	_, err := mon.RunOnce(ctx)
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}

	// t3 should NOT be cascade-failed because t2 is still running
	task, _ := d.GetTaskByID(ctx, "t3")
	if task.Status != "pending" {
		t.Errorf("expected t3 to remain pending (t2 still running), got status %s", task.Status)
	}
}

func TestPhase1_5CascadeDeferredWhenBlockerInRefinement(t *testing.T) {
	mon, d, _ := setupMonitorTest(t)
	ctx := context.Background()

	d.SetBlackboard(ctx, "conductor:active", "1", "test")
	d.SetBlackboard(ctx, "conductor:task_ids", `["t1","t2"]`, "test")

	// t1 failed, t2 blocked by t1
	d.CreateTask(ctx, db.Task{ID: "t1", Title: "Task 1", Status: "failed", Role: "researcher",
		BlockedBy: sql.NullString{String: "[]", Valid: true}})
	d.CreateTask(ctx, db.Task{ID: "t2", Title: "Task 2", Status: "pending", Role: "implementer",
		BlockedBy: sql.NullString{String: `["t1"]`, Valid: true}})

	// t1 is in refinement (count=1, max=2)
	d.SetBlackboard(ctx, "refinement:t1", "1", "test")

	_, err := mon.RunOnce(ctx)
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}

	// t2 should NOT be cascade-failed because t1 is still eligible for refinement
	task, _ := d.GetTaskByID(ctx, "t2")
	if task.Status != "pending" {
		t.Errorf("expected t2 to remain pending (blocker in refinement), got status %s", task.Status)
	}
}

// --- Step 5: Worktree Cleanup Tests ---

func TestCleanupSessionWorktreesRemovesDoneAndFailed(t *testing.T) {
	mon, d, repoRoot := setupMonitorTest(t)
	ctx := context.Background()

	// Create worktrees for a done and failed task
	wtDone := filepath.Join(repoRoot, ".worktree", "t-done")
	wtFailed := filepath.Join(repoRoot, ".worktree", "t-failed")
	gitExec(t, repoRoot, "worktree", "add", wtDone, "-b", "feature/t-done")
	gitExec(t, repoRoot, "worktree", "add", wtFailed, "-b", "feature/t-failed")

	d.CreateTask(ctx, db.Task{ID: "t-done", Title: "Done Task", Status: "done", Role: "implementer",
		Worktree: sql.NullString{String: wtDone, Valid: true},
		Branch:   sql.NullString{String: "feature/t-done", Valid: true}})
	d.CreateTask(ctx, db.Task{ID: "t-failed", Title: "Failed Task", Status: "failed", Role: "implementer",
		Worktree: sql.NullString{String: wtFailed, Valid: true},
		Branch:   sql.NullString{String: "feature/t-failed", Valid: true}})

	mon.cleanupSessionWorktrees(ctx, []string{"t-done", "t-failed"})

	// Both worktrees should be gone
	if _, err := os.Stat(wtDone); err == nil {
		t.Error("expected done worktree to be removed")
	}
	if _, err := os.Stat(wtFailed); err == nil {
		t.Error("expected failed worktree to be removed")
	}
}

func TestCleanupSessionWorktreesPreservesRunning(t *testing.T) {
	mon, d, repoRoot := setupMonitorTest(t)
	ctx := context.Background()

	// Create worktree for a running task
	wtRunning := filepath.Join(repoRoot, ".worktree", "t-running")
	gitExec(t, repoRoot, "worktree", "add", wtRunning, "-b", "feature/t-running")

	d.CreateTask(ctx, db.Task{ID: "t-running", Title: "Running Task", Status: "running", Role: "implementer",
		Worktree: sql.NullString{String: wtRunning, Valid: true},
		Branch:   sql.NullString{String: "feature/t-running", Valid: true}})

	mon.cleanupSessionWorktrees(ctx, []string{"t-running"})

	// Running worktree should still exist
	if _, err := os.Stat(wtRunning); err != nil {
		t.Error("expected running worktree to be preserved")
	}
}

func TestCleanupSessionWorktrees_PreservesUnmergedBranch(t *testing.T) {
	mon, d, repoRoot := setupMonitorTest(t)
	ctx := context.Background()

	// Create a worktree with commits NOT merged into dev
	wt := filepath.Join(repoRoot, ".worktree", "t-unmerged")
	gitExec(t, repoRoot, "worktree", "add", wt, "-b", "feature/t-unmerged")
	os.WriteFile(filepath.Join(wt, "unmerged.go"), []byte("package unmerged"), 0o644)
	gitExec(t, wt, "add", ".")
	gitExec(t, wt, "commit", "-m", "unmerged work")

	d.CreateTask(ctx, db.Task{
		ID:       "t-unmerged",
		Title:    "Unmerged Task",
		Status:   "done",
		Role:     "implementer",
		Worktree: sql.NullString{String: wt, Valid: true},
		Branch:   sql.NullString{String: "feature/t-unmerged", Valid: true},
	})

	// Set base branch for the monitor
	d.SetBlackboard(ctx, "conductor:base_branch", "dev", "test")

	mon.cleanupSessionWorktrees(ctx, []string{"t-unmerged"})

	// Branch should NOT be deleted — it has unmerged commits
	branchCmd := exec.Command("git", "rev-parse", "--verify", "feature/t-unmerged")
	branchCmd.Dir = repoRoot
	if out, err := branchCmd.CombinedOutput(); err != nil {
		t.Errorf("branch should still exist (unmerged), got error: %v: %s", err, string(out))
	}

	// Backup ref should exist
	refCmd := exec.Command("git", "rev-parse", "--verify", "refs/orchestra-backup/t-unmerged")
	refCmd.Dir = repoRoot
	if out, err := refCmd.CombinedOutput(); err != nil {
		t.Errorf("backup ref should exist, got error: %v: %s", err, string(out))
	}

	// Verify branch_preserved event was logged
	events, _ := d.RecentEvents(ctx, 10)
	found := false
	for _, e := range events {
		if e.EventType == "branch_preserved" {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected branch_preserved event to be logged")
	}
}

// --- B-273: PR Mode tests ---

func TestAutoMerge_PRMode(t *testing.T) {
	// Verify monitor calls PRCreateFunc when merge_mode is "pr" and conductor is dead.
	d := setupTestDB(t)
	ctx := context.Background()

	// Create a dead conductor with merge_mode=pr
	d.CreateConductor(ctx, db.ConductorRecord{
		ID:            "s-pr-auto",
		PID:           99999, // dead PID
		Goal:          "test PR auto-merge",
		Status:        "active",
		StagingBranch: "conductor/s-pr-auto",
		BaseBranch:    "dev",
		MaxParallel:   1,
		ModelStrategy: "all-opus",
		Runtime:       "local",
		MergeMode:     "pr",
	})

	// Create a done task for this conductor
	d.CreateTask(ctx, db.Task{
		ID:          "T-PR-AUTO",
		Title:       "Test task",
		Status:      "done",
		Role:        "implementer",
		ConductorID: sql.NullString{Valid: true, String: "s-pr-auto"},
	})

	mergeCalled := false
	prCreated := false
	m := &Monitor{
		DB:       d,
		RepoRoot: t.TempDir(),
	}
	m.MergeFunc = func(ctx context.Context, testCmd string, review bool) error {
		mergeCalled = true
		return nil
	}
	m.PRCreateFunc = func(ctx context.Context) (*PRCreateResult, error) {
		prCreated = true
		return &PRCreateResult{
			PRURL:    "https://github.com/org/repo/pull/99",
			PRNumber: 99,
			Branch:   "conductor/s-pr-auto",
			Base:     "dev",
		}, nil
	}

	cond, _ := d.GetConductor(ctx, "s-pr-auto")
	err := m.phase3AutoMergeForConductor(ctx, *cond)
	if err != nil {
		t.Fatalf("auto-merge error: %v", err)
	}

	if !mergeCalled {
		t.Error("expected MergeFunc to be called for branch merging")
	}
	if !prCreated {
		t.Error("expected PRCreateFunc to be called for PR mode conductor")
	}
}

func TestAutoMerge_LocalMode_NoPRCreate(t *testing.T) {
	// Verify monitor does NOT call PRCreateFunc when merge_mode is "local".
	d := setupTestDB(t)
	ctx := context.Background()

	d.CreateConductor(ctx, db.ConductorRecord{
		ID:            "s-local-auto",
		PID:           99999, // dead PID
		Goal:          "test local auto-merge",
		Status:        "active",
		StagingBranch: "conductor/s-local-auto",
		BaseBranch:    "dev",
		MaxParallel:   1,
		ModelStrategy: "all-opus",
		Runtime:       "local",
		MergeMode:     "local",
	})

	d.CreateTask(ctx, db.Task{
		ID:          "T-LOCAL-AUTO",
		Title:       "Test task",
		Status:      "done",
		Role:        "implementer",
		ConductorID: sql.NullString{Valid: true, String: "s-local-auto"},
	})

	prCreated := false
	m := &Monitor{
		DB:       d,
		RepoRoot: t.TempDir(),
	}
	m.MergeFunc = func(ctx context.Context, testCmd string, review bool) error {
		return nil
	}
	m.PRCreateFunc = func(ctx context.Context) (*PRCreateResult, error) {
		prCreated = true
		return nil, nil
	}

	cond, _ := d.GetConductor(ctx, "s-local-auto")
	err := m.phase3AutoMergeForConductor(ctx, *cond)
	if err != nil {
		t.Fatalf("auto-merge error: %v", err)
	}

	if prCreated {
		t.Error("PRCreateFunc should NOT be called for local mode conductor")
	}
}

func TestRecoverMerge_PRMode_Staging(t *testing.T) {
	// Verify recovery creates PR instead of local merge when merge_mode is "pr" and status is "staging".
	d := setupTestDB(t)
	ctx := context.Background()

	d.CreateConductor(ctx, db.ConductorRecord{
		ID:            "s-pr-recover",
		PID:           99999,
		Goal:          "test PR recovery",
		Status:        "active",
		StagingBranch: "conductor/s-pr-recover",
		BaseBranch:    "dev",
		MaxParallel:   1,
		ModelStrategy: "all-opus",
		Runtime:       "local",
		MergeMode:     "pr",
	})
	d.SetMergeStatus(ctx, "s-pr-recover", "staging")

	prCreated := false
	stagingMergeCalled := false
	m := &Monitor{
		DB:       d,
		RepoRoot: t.TempDir(),
	}
	m.StagingMergeFunc = func(ctx context.Context) error {
		stagingMergeCalled = true
		return nil
	}
	m.PRCreateFunc = func(ctx context.Context) (*PRCreateResult, error) {
		prCreated = true
		return &PRCreateResult{
			PRURL:    "https://github.com/org/repo/pull/100",
			PRNumber: 100,
			Branch:   "conductor/s-pr-recover",
			Base:     "dev",
		}, nil
	}

	cond, _ := d.GetConductor(ctx, "s-pr-recover")
	err := m.recoverMerge(ctx, *cond)
	if err != nil {
		t.Fatalf("recover merge error: %v", err)
	}

	if !prCreated {
		t.Error("expected PRCreateFunc to be called for PR mode recovery")
	}
	if stagingMergeCalled {
		t.Error("StagingMergeFunc should NOT be called when merge_mode is 'pr'")
	}
}

func TestRecoverMerge_LocalMode_Staging(t *testing.T) {
	// Verify recovery calls StagingMergeFunc for local mode with status "staging".
	d := setupTestDB(t)
	ctx := context.Background()

	d.CreateConductor(ctx, db.ConductorRecord{
		ID:            "s-local-recover",
		PID:           99999,
		Goal:          "test local recovery",
		Status:        "active",
		StagingBranch: "conductor/s-local-recover",
		BaseBranch:    "dev",
		MaxParallel:   1,
		ModelStrategy: "all-opus",
		Runtime:       "local",
		MergeMode:     "local",
	})
	d.SetMergeStatus(ctx, "s-local-recover", "staging")

	prCreated := false
	stagingMergeCalled := false
	m := &Monitor{
		DB:       d,
		RepoRoot: t.TempDir(),
	}
	m.StagingMergeFunc = func(ctx context.Context) error {
		stagingMergeCalled = true
		return nil
	}
	m.PRCreateFunc = func(ctx context.Context) (*PRCreateResult, error) {
		prCreated = true
		return nil, nil
	}

	cond, _ := d.GetConductor(ctx, "s-local-recover")
	err := m.recoverMerge(ctx, *cond)
	if err != nil {
		t.Fatalf("recover merge error: %v", err)
	}

	if prCreated {
		t.Error("PRCreateFunc should NOT be called for local mode recovery")
	}
	if !stagingMergeCalled {
		t.Error("expected StagingMergeFunc to be called for local mode recovery")
	}
}

// Suppress unused imports warning
var _ = fmt.Sprintf
var _ = strings.TrimSpace
