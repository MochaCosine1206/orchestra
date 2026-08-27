// Poll loop pattern (interval, stopCh, RunOnce) is reused by the global daemon
// for its watchdog and scheduling cycle (Phase 2B).
package monitor

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/MochaCosine1206/orchestra/internal/agent"
	"github.com/MochaCosine1206/orchestra/internal/db"
	"github.com/MochaCosine1206/orchestra/internal/tui/logstream"
)

// CycleStats captures metrics from one monitor cycle.
type CycleStats struct {
	Checked        int
	Completed      int
	Failed         int
	Retried        int
	StuckWarned    int
	StuckKilled    int
	WaitersResumed int
	TimedOut       int
	Validated      int
	AutoSpawned    int
	Duration       time.Duration
	Errors         []error // phase errors accumulated during the cycle
}

// HasErrors returns true if any errors occurred during the cycle.
func (s *CycleStats) HasErrors() bool {
	return len(s.Errors) > 0
}

// MergeFunc is a callback for Go-native merge (avoids circular import with orchestrator).
// Parameters: ctx, testCmd, review. Returns error.
type MergeFunc func(ctx context.Context, testCmd string, review bool) error

// MergeFuncWithSkip is like MergeFunc but accepts branches to skip test gate (B-145 recovery).
type MergeFuncWithSkip func(ctx context.Context, testCmd string, review bool, skipBranches []string) error

// StagingMergeFunc merges the staging branch into dev (B-145 recovery).
type StagingMergeFunc func(ctx context.Context) error

// ReDecomposeFunc is called when a context-exhausted task should be split into subtasks.
// Parameters: ctx, taskID, checkpoint JSON. Returns new task IDs or error.
type ReDecomposeFunc func(ctx context.Context, taskID string, checkpoint string) ([]string, error)

// PRCreateFunc creates a GitHub PR from the staging branch (B-273).
type PRCreateFunc func(ctx context.Context) (*PRCreateResult, error)

// PRCreateResult is the monitor-side result of creating a staging PR.
type PRCreateResult struct {
	PRURL    string
	PRNumber int
	Branch   string
	Base     string
}

// HealFunc attempts to auto-fix a build failure for a task.
// Parameters: ctx, sessionID, taskID, errorType.
// Returns true if the fix was applied successfully, false otherwise.
type HealFunc func(ctx context.Context, sessionID, taskID, errorType string) (bool, error)

// QualityCheckFunc runs quality gates before merge. Returns nil for PROMOTE,
// non-nil error for HOLD or ROLLBACK. Wire to quality.CheckBeforeMerge.
type QualityCheckFunc func(ctx context.Context, projectPath, branch, sessionID string) error

// TaskGateFunc runs build+test validation on an agent's worktree before merge.
// Returns (passed, triageOutcome, errorOutput).
// triageOutcome is one of "heal", "refine", or "redo".
// Wire to task_gate.RunGate (built by another agent).
type TaskGateFunc func(ctx context.Context, worktreePath string, testCmd string) (bool, string, string)

// Monitor manages the agent lifecycle with a background polling loop.
type Monitor struct {
	DB                *db.DB
	Spawner           *agent.Spawner
	MergeFunc         MergeFunc         // optional: when set, uses Go merge in phase3 instead of bash
	MergeFuncWithSkip MergeFuncWithSkip // optional: merge with skip-branches for crash recovery (B-145)
	StagingMergeFunc  StagingMergeFunc  // optional: staging-to-dev merge for crash recovery (B-145)
	PRCreateFunc      PRCreateFunc      // optional: creates GitHub PR from staging branch (B-273)
	ReDecomposeFunc   ReDecomposeFunc   // optional: when set, splits context-exhausted tasks into subtasks
	HealFunc          HealFunc          // optional: when set, attempts auto-fix on build failures before retry
	QualityCheckFunc  QualityCheckFunc  // optional: when set, runs quality gates before merge (Phase 5)
	TaskGateFunc      TaskGateFunc      // optional: when set, runs build+test gate before marking task done
	RepoRoot          string
	LogsDir           string
	PidsDir           string
	Interval          time.Duration
	Log               func(string) // logging callback

	stopCh     chan struct{}
	mu         sync.Mutex
	running    bool
	cycleCount int // tracks cycles for throttling periodic phases
	watcher    *FileWatcher
}

const (
	defaultInterval    = 15 * time.Second
	stuckThreshold     = 600 * time.Second  // 10 minutes — warning (raised from 5min for long content tasks)
	stuckKillThreshold = 1800 * time.Second // 30 minutes — kill escalation (raised from 10min for long content tasks)
	killGracePeriod    = 10 * time.Second
)

func (m *Monitor) log(format string, args ...interface{}) {
	if m.Log != nil {
		m.Log(fmt.Sprintf(format, args...))
	}
}

func (m *Monitor) interval() time.Duration {
	if m.Interval > 0 {
		return m.Interval
	}
	return defaultInterval
}

// Start begins the background monitor loop in a goroutine.
func (m *Monitor) Start(ctx context.Context) error {
	m.mu.Lock()
	if m.running {
		m.mu.Unlock()
		return fmt.Errorf("monitor already running")
	}
	m.running = true
	m.stopCh = make(chan struct{})
	m.mu.Unlock()

	go m.loop(ctx)
	m.log("Monitor started (interval=%s)", m.interval())

	// Start file watcher if enabled via blackboard
	watcherFlag, _ := m.DB.GetBlackboardValue(ctx, "conductor:file_watcher")
	if watcherFlag == "1" {
		fw := NewFileWatcher(m.DB, func(s string) { m.log("%s", s) })
		if err := fw.Start(ctx); err != nil {
			m.log("WARN: file watcher failed to start: %v", err)
		} else {
			m.watcher = fw
		}
	}

	return nil
}

// Stop gracefully stops the monitor loop.
func (m *Monitor) Stop() error {
	m.mu.Lock()
	watcher := m.watcher
	m.mu.Unlock()

	if watcher != nil {
		watcher.Stop()
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.running {
		return nil
	}
	close(m.stopCh)
	m.running = false
	m.watcher = nil
	m.log("Monitor stopped")
	return nil
}

// Running returns whether the monitor loop is active.
func (m *Monitor) Running() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.running
}

func (m *Monitor) loop(ctx context.Context) {
	ticker := time.NewTicker(m.interval())
	defer ticker.Stop()

	for {
		select {
		case <-m.stopCh:
			return
		case <-ctx.Done():
			return
		case <-ticker.C:
			stats, err := m.RunOnce(ctx)
			if err != nil {
				m.log("Cycle error (%d phase failures): %v", len(stats.Errors), err)
			}
			m.log("CYCLE: checked=%d completed=%d failed=%d retried=%d stuck=%d stuck_killed=%d waiters=%d timeout=%d spawned=%d errors=%d dur=%s",
				stats.Checked, stats.Completed, stats.Failed, stats.Retried,
				stats.StuckWarned, stats.StuckKilled, stats.WaitersResumed, stats.TimedOut,
				stats.AutoSpawned, len(stats.Errors), stats.Duration)
		}
	}
}

// RunOnce executes a single monitor cycle (useful for testing and debug).
func (m *Monitor) RunOnce(ctx context.Context) (*CycleStats, error) {
	start := time.Now()
	stats := &CycleStats{}
	m.cycleCount++

	// Phase 0: Expired waiters
	resumed, err := m.phase0ExpiredWaiters(ctx)
	if err != nil {
		m.log("Phase 0 error: %v", err)
		stats.Errors = append(stats.Errors, fmt.Errorf("phase0: %w", err))
	}
	stats.WaitersResumed = resumed

	// Phase 1: Check live/dead agents
	p1Stats, err := m.phase1CheckAgents(ctx)
	if err != nil {
		m.log("Phase 1 error: %v", err)
		stats.Errors = append(stats.Errors, fmt.Errorf("phase1: %w", err))
	}
	stats.Checked = p1Stats.checked
	stats.Completed = p1Stats.completed
	stats.Failed = p1Stats.failed
	stats.Retried = p1Stats.retried
	stats.StuckWarned = p1Stats.stuck
	stats.StuckKilled = p1Stats.stuckKills
	stats.TimedOut = p1Stats.timedOut
	stats.Validated = p1Stats.validated

	// Phase 1.5: Cascade-fail blocked tasks whose blockers all terminated with at least one failure
	cascaded, err := m.phase1_5CascadeFailBlocked(ctx)
	if err != nil {
		m.log("Phase 1.5 error: %v", err)
		stats.Errors = append(stats.Errors, fmt.Errorf("phase1_5: %w", err))
	}
	stats.Failed += cascaded

	// Phase 2: Auto-spawn
	spawned, err := m.phase2AutoSpawn(ctx)
	if err != nil {
		m.log("Phase 2 error: %v", err)
		stats.Errors = append(stats.Errors, fmt.Errorf("phase2: %w", err))
	}
	stats.AutoSpawned = spawned

	// Phase 2.5: Predict merge conflicts
	if err := m.phase2_5PredictConflicts(ctx); err != nil {
		m.log("Phase 2.5 error: %v", err)
		stats.Errors = append(stats.Errors, fmt.Errorf("phase2_5: %w", err))
	}

	// Phase 3: Auto-merge
	if err := m.phase3AutoMerge(ctx); err != nil {
		m.log("Phase 3 error: %v", err)
		stats.Errors = append(stats.Errors, fmt.Errorf("phase3: %w", err))
	}

	// Cleanup
	m.cleanup(ctx)

	stats.Duration = time.Since(start)

	// Return combined error if any phases failed
	if len(stats.Errors) > 0 {
		return stats, fmt.Errorf("cycle had %d error(s): %v", len(stats.Errors), stats.Errors[0])
	}
	return stats, nil
}

// --- Phase 0: Expired Waiters ---

func (m *Monitor) phase0ExpiredWaiters(ctx context.Context) (int, error) {
	entries, err := m.DB.ListBlackboardByPrefix(ctx, "wait_until:")
	if err != nil {
		return 0, err
	}

	now := time.Now().Unix()
	resumed := 0

	for _, e := range entries {
		waitUntil, err := strconv.ParseInt(e.Value, 10, 64)
		if err != nil {
			continue
		}
		if waitUntil > now {
			continue // not yet expired
		}

		taskID := strings.TrimPrefix(e.Key, "wait_until:")
		reason, _ := m.DB.GetBlackboardValue(ctx, "wait_reason:"+taskID)

		// Cleanup wait keys
		m.DB.DeleteBlackboard(ctx, "wait_until:"+taskID)
		m.DB.DeleteBlackboard(ctx, "wait_reason:"+taskID)

		// Resume or respawn based on reason
		if reason == "session_limit" || reason == "rate_limit" || reason == "content_filter" {
			_, err = m.Spawner.Resume(ctx, taskID)
		} else {
			_, err = m.Spawner.Respawn(ctx, taskID)
		}
		if err != nil {
			m.log("Waiter resume failed for %s: %v", taskID, err)
		} else {
			m.DB.LogEvent(ctx, "waiter_resumed", "", taskID, reason)
			resumed++
		}
	}
	return resumed, nil
}

// --- Phase 1: Check Live/Dead Agents ---

type phase1Stats struct {
	checked    int
	completed  int
	failed     int
	retried    int
	stuck      int
	stuckKills int
	timedOut   int
	validated  int
}

func (m *Monitor) phase1CheckAgents(ctx context.Context) (phase1Stats, error) {
	var stats phase1Stats

	agents, err := m.DB.ListActiveAgentsWithPID(ctx)
	if err != nil {
		return stats, err
	}

	for _, a := range agents {
		if !a.PID.Valid || !a.CurrentTask.Valid {
			continue
		}
		stats.checked++
		pid := int(a.PID.Int64)
		taskID := a.CurrentTask.String

		if agent.PidAlive(pid) {
			m.handleAliveAgent(ctx, a, taskID, &stats)
		} else {
			m.handleDeadAgent(ctx, a, taskID, &stats)
		}
	}
	return stats, nil
}

func (m *Monitor) handleAliveAgent(ctx context.Context, a db.Agent, taskID string, stats *phase1Stats) {
	// Update heartbeat
	m.DB.UpdateAgentHeartbeat(ctx, a.ID)

	// Read self-reported checkpoint from worktree
	task, err := m.DB.GetTaskByID(ctx, taskID)
	if err == nil && task != nil && task.Worktree.Valid {
		cpFile := filepath.Join(task.Worktree.String, ".checkpoint.json")
		if data, err := os.ReadFile(cpFile); err == nil {
			m.DB.SetBlackboard(ctx, "self_checkpoint:"+taskID, string(data), "monitor")
		}

		// Register worktree with file watcher if running
		m.mu.Lock()
		watcher := m.watcher
		m.mu.Unlock()
		if watcher != nil {
			watcher.AddWorktree(ctx, taskID, task.Worktree.String)
		}
	}

	// Check timeout
	timeoutStr, _ := m.DB.GetBlackboardValue(ctx, "timeout:"+taskID)
	spawnTimeStr, _ := m.DB.GetBlackboardValue(ctx, "spawn_time:"+taskID)
	if timeoutStr != "" && spawnTimeStr != "" {
		timeoutSec, err1 := strconv.Atoi(timeoutStr)
		spawnTime, err2 := strconv.ParseInt(spawnTimeStr, 10, 64)
		if err1 != nil || err2 != nil {
			m.log("WARN: malformed timeout for %s (timeout=%q spawn=%q), skipping", taskID, timeoutStr, spawnTimeStr)
		} else if timeoutSec > 0 {
			elapsed := time.Now().Unix() - spawnTime
			if elapsed > int64(timeoutSec) {
				m.log("TIMEOUT: Task %s exceeded %ds", taskID, timeoutSec)
				// Docker: kill container first (if it exists)
				runtime, _ := m.DB.GetBlackboardValue(ctx, "conductor:runtime")
				if runtime == "docker" {
					agent.KillContainer(taskID)
				}
				pid := int(a.PID.Int64)
				agent.KillProcess(pid, false, killGracePeriod)
				if err := m.DB.FailTask(ctx, taskID, fmt.Sprintf("timeout after %ds", timeoutSec)); err != nil {
					m.log("CRITICAL: FailTask error for %s: %v", taskID, err)
				}
				if err := m.DB.SetAgentDead(ctx, a.ID); err != nil {
					m.log("CRITICAL: SetAgentDead error for %s: %v", a.ID, err)
				}
				m.DB.SetBlackboard(ctx, "failure_type:"+taskID, "timeout", "monitor")
				m.DB.LogEvent(ctx, "agent_timeout", a.ID, taskID, fmt.Sprintf(`{"elapsed":%d}`, elapsed))
				stats.timedOut++
				return
			}
		}
	}

	// --- Stall detection (B-142) ---
	// Rate-limited agents bypass stall detection entirely (G135).
	stallLogFile := filepath.Join(m.LogsDir, taskID+".jsonl")
	stderrFile := filepath.Join(m.LogsDir, taskID+".stderr")
	ft := agent.ClassifyFailure(stderrFile, stallLogFile)
	if ft.Kind != "rate_limit" && ft.Kind != "session_limit" && ft.Kind != "content_filter" {
		m.checkStallScore(ctx, a, taskID, stallLogFile, stats)
	}

	// Check stuck (log staleness) — preserved as last-resort fallback
	logFile := filepath.Join(m.LogsDir, taskID+".jsonl")
	if info, err := os.Stat(logFile); err == nil {
		staleness := time.Since(info.ModTime())

		// Escalation: kill at 10 min (configurable via blackboard)
		killThreshold := stuckKillThreshold
		if customStr, _ := m.DB.GetBlackboardValue(ctx, "conductor:stuck_kill_timeout"); customStr != "" {
			if customSec, err := strconv.Atoi(customStr); err == nil && customSec > 0 {
				killThreshold = time.Duration(customSec) * time.Second
			}
		}

		if staleness > killThreshold {
			killed, _ := m.DB.GetBlackboardValue(ctx, "stuck_killed:"+taskID)
			if killed == "" {
				// G135: Before killing, check if stderr/jsonl indicates rate_limit
				// or session_limit. If so, the agent is waiting on a cooldown, not
				// genuinely stuck. Let it exit naturally; handleCrashedAgent will
				// classify it correctly and set wait_until for retry.
				stderrFile := filepath.Join(m.LogsDir, taskID+".stderr")
				jsonlFile := filepath.Join(m.LogsDir, taskID+".jsonl")
				ft := agent.ClassifyFailure(stderrFile, jsonlFile)
				if ft.Kind == "rate_limit" || ft.Kind == "session_limit" {
					m.log("STUCK SKIP: Task %s stuck for %ds but stderr shows %s — not killing",
						taskID, int(staleness.Seconds()), ft.Kind)
					m.DB.LogEvent(ctx, "agent_stuck_skip_ratelimit", a.ID, taskID,
						fmt.Sprintf(`{"staleness_seconds":%d,"reason":"%s"}`, int(staleness.Seconds()), ft.Kind))
					stats.stuck++
					return
				}

				m.log("STUCK ESCALATION: Task %s stuck for %ds, killing agent %s",
					taskID, int(staleness.Seconds()), a.ID)

				// Kill the process
				pid := int(a.PID.Int64)
				runtime, _ := m.DB.GetBlackboardValue(ctx, "conductor:runtime")
				if runtime == "docker" {
					agent.KillContainer(taskID)
				}
				agent.KillProcess(pid, false, killGracePeriod)

				// Check if the agent produced any commits in its worktree
				hasOutput := false
				task, err := m.DB.GetTaskByID(ctx, taskID)
				if err == nil && task != nil && task.Worktree.Valid {
					hasOutput = worktreeHasCommits(task.Worktree.String)
				}

				if err := m.DB.SetAgentDead(ctx, a.ID); err != nil {
					m.log("CRITICAL: SetAgentDead error for %s: %v", a.ID, err)
				}
				reason := fmt.Sprintf("stuck_killed after %ds", int(staleness.Seconds()))
				if hasOutput {
					reason += " (has commits — salvageable)"
				}
				if err := m.DB.FailTask(ctx, taskID, reason); err != nil {
					m.log("CRITICAL: FailTask error for %s: %v", taskID, err)
				}
				m.DB.SetBlackboard(ctx, "stuck_killed:"+taskID, "1", "monitor")
				m.DB.SetBlackboard(ctx, "failure_type:"+taskID, "stuck_killed", "monitor")
				m.DB.LogEvent(ctx, "agent_stuck_escalation", a.ID, taskID,
					fmt.Sprintf(`{"staleness_seconds":%d,"has_output":%v}`, int(staleness.Seconds()), hasOutput))
				stats.stuckKills++
				return
			}
		} else if staleness > stuckThreshold {
			// Warning at 5 min
			logged, _ := m.DB.GetBlackboardValue(ctx, "compaction_logged:"+taskID)
			if logged == "" {
				m.DB.LogEvent(ctx, "agent_stuck_warning", a.ID, taskID,
					fmt.Sprintf(`{"staleness_seconds":%d}`, int(staleness.Seconds())))
				stats.stuck++
			}
		}
	}
}

// checkStallScore reads recent JSONL lines, computes a composite stall score,
// records it in the DB, and performs graduated response if sustained thresholds are exceeded.
func (m *Monitor) checkStallScore(ctx context.Context, a db.Agent, taskID, jsonlFile string, stats *phase1Stats) {
	activity := m.buildAgentActivity(ctx, taskID, jsonlFile)
	now := time.Now()

	score := ComputeStallScore(activity, now)
	phase := DetectPhase(activity)
	thresholds := PhaseThresholds(phase, 0.0) // default tier adjustment

	// Record score in DB
	dbScore := db.StallScore{
		TaskID:            taskID,
		AgentID:           a.ID,
		CompositeScore:    score.Composite,
		SignalFingerprint: score.ToolRepetition,
		SignalProgress:    score.ProgressDelta,
		SignalFiles:       score.FileCoverage,
		SignalErrors:      score.ErrorRepetition,
		SignalReadWrite:   score.ReadWriteDrift,
	}
	if phase != "" {
		dbScore.Phase.Valid = true
		dbScore.Phase.String = string(phase)
	}
	if err := m.DB.RecordStallScore(ctx, dbScore); err != nil {
		m.log("WARN: failed to record stall score for %s: %v", taskID, err)
	}

	// Get recent scores for sustained stall check
	recentScores, err := m.DB.GetRecentStallScores(ctx, taskID, 20)
	if err != nil {
		m.log("WARN: failed to get stall history for %s: %v", taskID, err)
		return
	}

	// Convert db.StallScore to StallRecord for sustained check
	history := make([]StallRecord, len(recentScores))
	for i, s := range recentScores {
		history[i] = StallRecord{Score: s.CompositeScore, Timestamp: s.CreatedAt}
	}

	monitorTriggered, resetTriggered := SustainedStallCheck(
		history, thresholds.Monitor, thresholds.Reset, now)

	if resetTriggered {
		m.handleStallReset(ctx, a, taskID, score.Composite, phase, stats)
	} else if monitorTriggered {
		m.log("STALL DETECTED: Task %s composite=%.2f phase=%s (monitor threshold)",
			taskID, score.Composite, phase)
		m.DB.LogEvent(ctx, "agent_stall_detected", a.ID, taskID,
			fmt.Sprintf(`{"composite":%.2f,"phase":"%s","threshold":%.2f}`,
				score.Composite, phase, thresholds.Monitor))
	}

	// Rabbit-hole detection: flag agents making many tool calls but touching very few unique files
	if rh := DetectRabbitHole(activity.RecentToolCalls); rh.Detected {
		m.log("RABBIT HOLE: Task %s ratio=%.3f tool_calls=%d",
			taskID, rh.Ratio, rh.CallCount)
		m.DB.LogEvent(ctx, "agent_rabbit_hole", a.ID, taskID,
			fmt.Sprintf(`{"task_id":"%s","agent_id":"%s","ratio":%.4f,"tool_call_count":%d}`,
				taskID, a.ID, rh.Ratio, rh.CallCount))
	}
}

// handleStallReset kills a stalled agent, salvages work, and respawns with anti-pattern injection.
// If a prior stall reset already occurred (second stall), marks the task for re-decomposition instead.
func (m *Monitor) handleStallReset(ctx context.Context, a db.Agent, taskID string, composite float64, phase Phase, stats *phase1Stats) {
	// Check if this is a second stall reset (prior reset already happened)
	priorReset, _ := m.DB.GetBlackboardValue(ctx, "stall_reset:"+taskID)
	if priorReset != "" {
		// Second reset failure → mark for re-decomposition
		m.log("STALL RE-DECOMPOSE: Task %s stalled again after reset — marking for re-decomposition", taskID)
		m.DB.SetBlackboard(ctx, "needs_redecompose:"+taskID, "stall", "monitor")
		m.DB.LogEvent(ctx, "agent_stall_redecompose", a.ID, taskID,
			fmt.Sprintf(`{"composite":%.2f,"phase":"%s"}`, composite, phase))

		// Kill and fail the task
		pid := int(a.PID.Int64)
		runtime, _ := m.DB.GetBlackboardValue(ctx, "conductor:runtime")
		if runtime == "docker" {
			agent.KillContainer(taskID)
		}
		agent.KillProcess(pid, false, killGracePeriod)
		m.DB.SetAgentDead(ctx, a.ID)
		m.DB.FailTask(ctx, taskID, fmt.Sprintf("stall_reset_failed: composite=%.2f", composite))
		m.DB.SetBlackboard(ctx, "failure_type:"+taskID, "stall_redecompose", "monitor")
		stats.stuckKills++
		return
	}

	m.log("STALL RESET: Task %s composite=%.2f phase=%s — killing and respawning with anti-pattern",
		taskID, composite, phase)

	// Kill the agent
	pid := int(a.PID.Int64)
	runtime, _ := m.DB.GetBlackboardValue(ctx, "conductor:runtime")
	if runtime == "docker" {
		agent.KillContainer(taskID)
	}
	agent.KillProcess(pid, false, killGracePeriod)

	// Salvage worktree changes
	task, err := m.DB.GetTaskByID(ctx, taskID)
	if err == nil && task != nil && task.Worktree.Valid {
		agent.SalvageWorktreeChanges(ctx, task.Worktree.String, taskID)
	}

	// Store anti-pattern critique for spec regeneration
	antiPattern := fmt.Sprintf("IMPORTANT: Previous attempt stalled on phase=%s with composite score %.2f. "+
		"Avoid repetitive tool calls and read-without-write loops. Make concrete progress on each file.", phase, composite)
	m.DB.SetBlackboard(ctx, "critique:"+taskID, antiPattern, "monitor")
	m.DB.SetBlackboard(ctx, "stall_reset:"+taskID, "1", "monitor")

	m.DB.LogEvent(ctx, "agent_stall_reset", a.ID, taskID,
		fmt.Sprintf(`{"composite":%.2f,"phase":"%s"}`, composite, phase))

	// Mark agent dead and respawn
	m.DB.SetAgentDead(ctx, a.ID)
	_, respawnErr := m.Spawner.RespawnForRefinement(ctx, taskID)
	if respawnErr != nil {
		m.log("STALL RESET FAILED: respawn for %s failed: %v", taskID, respawnErr)
		m.DB.FailTask(ctx, taskID, fmt.Sprintf("stall_reset_respawn_failed: %v", respawnErr))
		m.DB.SetBlackboard(ctx, "failure_type:"+taskID, "stall_reset_failed", "monitor")
	}
	stats.stuckKills++
}

// buildAgentActivity constructs an AgentActivity from the agent's JSONL log.
func (m *Monitor) buildAgentActivity(ctx context.Context, taskID, jsonlFile string) AgentActivity {
	var activity AgentActivity

	lines := readTailLines(jsonlFile, 50)
	if len(lines) == 0 {
		return activity
	}

	var toolCalls []ToolCall
	var errors []ErrorEntry
	var reads, writes int
	var hasEdits, runningTests bool

	for _, line := range lines {
		fp := logstream.ParseToolCallFingerprint(line)
		if !fp.IsToolCall {
			// Check for error results
			entry := logstream.ParseLine(line)
			if entry.Kind == logstream.KindToolResult && entry.IsError {
				errors = append(errors, ErrorEntry{
					Hash: FingerprintError(entry.ToolResult),
				})
			}
			continue
		}

		input := fp.ToolName
		entry := logstream.ParseLine(line)
		toolCalls = append(toolCalls, ToolCall{
			Name:  fp.ToolName,
			Input: entry.ToolInput,
		})

		// Classify tool calls for read/write tracking
		switch fp.ToolName {
		case "Read", "Glob", "Grep":
			reads++
		case "Edit", "Write":
			writes++
			hasEdits = true
		case "Bash":
			// Check if it's a test command
			if strings.Contains(entry.ToolInput, "test") || strings.Contains(entry.ToolInput, "pytest") ||
				strings.Contains(entry.ToolInput, "go test") {
				runningTests = true
			}
			writes++ // bash commands count as writes (side effects)
		}
		_ = input // used above via fp.ToolName
	}

	// Use last 10 tool calls for repetition analysis
	if len(toolCalls) > 10 {
		toolCalls = toolCalls[len(toolCalls)-10:]
	}
	activity.RecentToolCalls = toolCalls
	activity.Errors = errors
	activity.ReadWrite = ReadWriteStats{
		Reads:     reads,
		Writes:    writes,
		HasEdited: hasEdits,
	}
	activity.HasEdits = hasEdits
	activity.RunningTests = runningTests

	// File activity: count unique files from tool inputs
	task, err := m.DB.GetTaskByID(ctx, taskID)
	if err == nil && task != nil && task.Description.Valid {
		// Rough expected file count from task description
		activity.FileActivity.ExpectedFiles = countExpectedFiles(task.Description.String)
	}

	// Check for compaction event
	compactedStr, _ := m.DB.GetBlackboardValue(ctx, "compaction_logged:"+taskID)
	if compactedStr != "" {
		t := time.Now() // approximate — compaction time isn't precisely tracked
		activity.CompactedAt = &t
	}

	return activity
}

// readTailLines reads the last N lines from a file efficiently.
func readTailLines(path string, n int) []string {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()

	var lines []string
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 256*1024), 256*1024) // 256KB buffer for long JSONL lines
	for scanner.Scan() {
		lines = append(lines, scanner.Text())
	}

	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return lines
}

// countExpectedFiles counts file references in a task description (rough heuristic).
func countExpectedFiles(description string) int {
	count := 0
	for _, line := range strings.Split(description, "\n") {
		line = strings.TrimSpace(line)
		// Look for file-like patterns: paths with extensions
		if strings.Contains(line, ".go") || strings.Contains(line, ".ts") ||
			strings.Contains(line, ".py") || strings.Contains(line, ".sql") ||
			strings.Contains(line, ".js") || strings.Contains(line, ".md") {
			if strings.Contains(line, "/") {
				count++
			}
		}
	}
	if count == 0 {
		return 3 // reasonable default
	}
	return count
}

// worktreeHasCommits checks if a worktree has any commits beyond the fork point.
func worktreeHasCommits(worktreePath string) bool {
	// Check if the worktree has any commits that differ from its upstream/base
	cmd := exec.Command("git", "log", "--oneline", "-1", "HEAD")
	cmd.Dir = worktreePath
	out, err := cmd.Output()
	if err != nil || len(out) == 0 {
		return false
	}
	// Check for uncommitted changes or new commits vs the merge-base
	cmd = exec.Command("git", "rev-list", "--count", "HEAD", "--not", "--remotes")
	cmd.Dir = worktreePath
	out, err = cmd.Output()
	if err != nil {
		// Fallback: check if there are any staged/unstaged changes
		cmd = exec.Command("git", "status", "--porcelain")
		cmd.Dir = worktreePath
		out, err = cmd.Output()
		return err == nil && len(strings.TrimSpace(string(out))) > 0
	}
	count, err := strconv.Atoi(strings.TrimSpace(string(out)))
	if err != nil {
		return false
	}
	return count > 0
}

func (m *Monitor) handleDeadAgent(ctx context.Context, a db.Agent, taskID string, stats *phase1Stats) {
	// Terminal-state guard: spawner may have already handled this exit.
	task, err := m.DB.GetTaskByID(ctx, taskID)
	if err == nil && task != nil && (task.Status == "done" || task.Status == "failed") {
		if err := m.DB.SetAgentDead(ctx, a.ID); err != nil {
			m.log("CRITICAL: SetAgentDead error for %s: %v", a.ID, err)
		}
		return
	}

	// Check if result exists in log
	hasResult, isSuccess := agent.CheckLogResult(m.LogsDir, taskID)

	// Remove worktree from file watcher
	m.mu.Lock()
	watcher := m.watcher
	m.mu.Unlock()
	if watcher != nil {
		watcher.RemoveWorktree(taskID)
	}

	if hasResult && isSuccess {
		// Validate output
		result, err := agent.ValidateTaskOutput(ctx, m.DB, taskID, m.RepoRoot, m.LogsDir)
		if err != nil {
			m.log("Validation error for %s: %v", taskID, err)
			if err := m.DB.FailTask(ctx, taskID, "validation_error: "+err.Error()); err != nil {
				m.log("CRITICAL: FailTask error for %s: %v", taskID, err)
			}
			if err := m.DB.SetAgentDead(ctx, a.ID); err != nil {
				m.log("CRITICAL: SetAgentDead error for %s: %v", a.ID, err)
			}
			stats.failed++
			return
		}

		if result.OK {
			// V3 task gate: run build+test validation before marking done.
			if m.TaskGateFunc != nil {
				worktree := filepath.Join(m.RepoRoot, ".worktree", taskID)
				testCmd, _ := m.DB.GetBlackboardValue(ctx, "conductor:test_cmd")
				passed, triage, errOutput := m.TaskGateFunc(ctx, worktree, testCmd)
				if !passed {
					m.log("Task gate FAILED for %s: triage=%s", taskID, triage)
					m.DB.SetBlackboard(ctx, "gate_error:"+taskID, errOutput, "monitor")
					m.DB.SetBlackboard(ctx, "gate_triage:"+taskID, triage, "monitor")
					m.DB.LogEvent(ctx, "task_gate_failed", a.ID, taskID, triage)
					// Don't mark as done — mark as failed for refinement.
					if err := m.DB.FailTask(ctx, taskID, "gate_failed: "+triage); err != nil {
						m.log("CRITICAL: FailTask error for %s: %v", taskID, err)
					}
					m.DB.SetBlackboard(ctx, "failure_type:"+taskID, "gate_failure", "monitor")
					m.DB.SetBlackboard(ctx, "last_failure:"+taskID, "gate_failed: "+triage, "monitor")
					stats.failed++
					m.retryOrRefine(ctx, taskID, "gate_failed: "+triage, stats)
					return
				}
			}

			// Success path
			if err := m.DB.CompleteTask(ctx, taskID, "completed"); err != nil {
				m.log("CRITICAL: CompleteTask error for %s: %v", taskID, err)
			}
			m.DB.SetBlackboard(ctx, "validation:"+taskID, "passed", "monitor")
			if result.ResultSummary != "" {
				m.DB.SetBlackboard(ctx, "result_summary:"+taskID, result.ResultSummary, "monitor")
			}
			m.DB.LogEvent(ctx, "monitor_completed", a.ID, taskID, "")
			stats.completed++
			stats.validated++
		} else {
			// Validation failure — try refinement first, then fall back to blind retry
			if err := m.DB.FailTask(ctx, taskID, "validation_failed: "+result.Reason); err != nil {
				m.log("CRITICAL: FailTask error for %s: %v", taskID, err)
			}
			m.DB.SetBlackboard(ctx, "failure_type:"+taskID, "validation_failure", "monitor")
			m.DB.SetBlackboard(ctx, "last_failure:"+taskID, result.Reason, "monitor")
			m.DB.LogEvent(ctx, "validation_failed", a.ID, taskID, result.Reason)
			stats.failed++

			m.retryOrRefine(ctx, taskID, result.Reason, stats)
		}
	} else {
		// Agent crashed/failed
		m.handleCrashedAgent(ctx, a, taskID, stats)
	}

	if err := m.DB.SetAgentDead(ctx, a.ID); err != nil {
		m.log("CRITICAL: SetAgentDead error for %s: %v", a.ID, err)
	}
}

// retryOrRefine decides whether to use the refinement loop or blind respawn for
// a validation failure. Refinement is preferred for qualifying failures when the
// refinement budget has not been exhausted.
func (m *Monitor) retryOrRefine(ctx context.Context, taskID, reason string, stats *phase1Stats) {
	// Check if failure qualifies for refinement loop
	if agent.IsRefinableFailure(reason) {
		refinementStr, _ := m.DB.GetBlackboardValue(ctx, "refinement:"+taskID)
		refinementCount, _ := strconv.Atoi(refinementStr)
		if refinementCount < agent.MaxRefinements {
			m.log("Refinement triggered for %s (round %d): %s", taskID, refinementCount+1, reason)
			if _, err := m.Spawner.Refine(ctx, taskID); err == nil {
				stats.retried++
				return
			} else {
				m.log("Refinement failed for %s: %v, falling back to respawn", taskID, err)
			}
		}
	}

	// Generic retry (non-refinable failures, exhausted refinement budget, or refinement failure)
	retryStr, _ := m.DB.GetBlackboardValue(ctx, "retry:"+taskID)
	retryCount, _ := strconv.Atoi(retryStr)
	if retryCount < 3 {
		if _, err := m.Spawner.Respawn(ctx, taskID); err == nil {
			stats.retried++
		}
	}
}

func (m *Monitor) handleCrashedAgent(ctx context.Context, a db.Agent, taskID string, stats *phase1Stats) {
	stderrFile := filepath.Join(m.LogsDir, taskID+".stderr")
	jsonlFile := filepath.Join(m.LogsDir, taskID+".jsonl")

	ft := agent.ClassifyFailure(stderrFile, jsonlFile)
	m.DB.SetBlackboard(ctx, "failure_type:"+taskID, ft.Kind, "monitor")
	if err := m.DB.FailTask(ctx, taskID, "agent_crash: "+ft.Kind); err != nil {
		m.log("CRITICAL: FailTask error for %s: %v", taskID, err)
	}
	stats.failed++

	switch ft.Kind {
	case "rate_limit":
		waitUntil := time.Now().Unix() + int64(ft.WaitSeconds)
		m.DB.SetBlackboard(ctx, "wait_until:"+taskID, strconv.FormatInt(waitUntil, 10), "monitor")
		m.DB.SetBlackboard(ctx, "wait_reason:"+taskID, "rate_limit", "monitor")
		m.DB.LogEvent(ctx, "rate_limited", a.ID, taskID,
			fmt.Sprintf(`{"wait_seconds":%d}`, ft.WaitSeconds))

	case "session_limit":
		waitUntil := ft.ResetEpoch
		if waitUntil == 0 {
			waitUntil = time.Now().Unix() + 1800
		}
		m.DB.SetBlackboard(ctx, "wait_until:"+taskID, strconv.FormatInt(waitUntil, 10), "monitor")
		m.DB.SetBlackboard(ctx, "wait_reason:"+taskID, "session_limit", "monitor")
		// G139: Set global rate limit cooldown so other agents don't burn sessions.
		m.DB.SetBlackboard(ctx, "conductor:rate_limit_cooldown_until",
			strconv.FormatInt(waitUntil, 10), "monitor")
		m.DB.LogEvent(ctx, "session_limited", a.ID, taskID,
			fmt.Sprintf(`{"reset_epoch":%d}`, waitUntil))

	case "context_exhausted":
		// Extract partial results before respawn
		m.extractPartialResults(ctx, a, taskID)
		checkpoint, _ := m.DB.GetBlackboardValue(ctx, "checkpoint:"+taskID)

		// Try re-decomposition if callback is set and task has remaining work
		redecomposed := false
		if m.ReDecomposeFunc != nil && checkpoint != "" {
			var cp agent.Checkpoint
			if err := json.Unmarshal([]byte(checkpoint), &cp); err == nil {
				newIDs, err := m.ReDecomposeFunc(ctx, taskID, checkpoint)
				if err == nil && len(newIDs) > 0 {
					m.DB.LogEvent(ctx, "context_redecomposed", a.ID, taskID,
						fmt.Sprintf(`{"new_tasks":%d}`, len(newIDs)))
					stats.retried += len(newIDs)
					redecomposed = true
				} else if err != nil {
					m.log("Re-decomposition failed for %s: %v, falling back to respawn", taskID, err)
				}
			}
		}

		if !redecomposed {
			// Fallback: respawn
			if _, err := m.Spawner.Respawn(ctx, taskID); err == nil {
				stats.retried++
			}
		}
		m.DB.LogEvent(ctx, "context_exhausted", a.ID, taskID, "")

	default: // normal_failure
		// Attempt healing if HealFunc is set and failure looks like a build error
		healed := false
		if m.HealFunc != nil {
			sessionID, _ := m.DB.GetBlackboardValue(ctx, "conductor:session_id")
			fixed, healErr := m.HealFunc(ctx, sessionID, taskID, ft.Kind)
			if healErr != nil {
				m.log("HEAL: error attempting fix for %s: %v", taskID, healErr)
			} else if fixed {
				m.log("HEAL: auto-fix applied for %s", taskID)
				m.DB.LogEvent(ctx, "healing_applied", a.ID, taskID, ft.Kind)
				healed = true
			}
		}

		retryStr, _ := m.DB.GetBlackboardValue(ctx, "retry:"+taskID)
		retryCount, _ := strconv.Atoi(retryStr)
		if retryCount < 3 && !healed {
			// Check if we should resume instead of respawn
			waitReason, _ := m.DB.GetBlackboardValue(ctx, "wait_reason:"+taskID)
			if waitReason == "rate_limit" || waitReason == "session_limit" {
				if _, err := m.Spawner.Resume(ctx, taskID); err == nil {
					stats.retried++
				}
			} else {
				if _, err := m.Spawner.Respawn(ctx, taskID); err == nil {
					stats.retried++
				}
			}
		}
		m.DB.LogEvent(ctx, "agent_crashed", a.ID, taskID, ft.Kind)
	}
}

func (m *Monitor) extractPartialResults(ctx context.Context, a db.Agent, taskID string) {
	task, err := m.DB.GetTaskByID(ctx, taskID)
	if err != nil || task == nil || !task.Worktree.Valid {
		return
	}
	wt := task.Worktree.String
	baseBranch := m.getBaseBranch(ctx)

	// Extract structured checkpoint
	cp := agent.ExtractCheckpoint(ctx, m.DB, taskID, wt, m.LogsDir)
	if cp != nil {
		cpJSON, err := json.Marshal(cp)
		if err == nil {
			m.DB.SetBlackboard(ctx, "checkpoint:"+taskID, string(cpJSON), "monitor")
		}

		// Also store a human-readable summary for backwards compat (predecessor display)
		var summary strings.Builder
		if len(cp.CommitHashes) > 0 {
			logOutput, _ := gitCmdOutput(wt, "log", baseBranch+"..HEAD", "--oneline")
			summary.WriteString(logOutput)
		}
		diffStat, _ := gitCmdOutput(wt, "diff", "--stat")
		if diffStat != "" {
			if summary.Len() > 0 {
				summary.WriteString("\n---\n")
			}
			summary.WriteString(diffStat)
		}
		s := summary.String()
		if len(s) > 2000 {
			s = s[:2000]
		}
		if s != "" {
			m.DB.SetBlackboard(ctx, "result_summary:"+taskID, s, "monitor")
		}
		return
	}

	// Fallback: raw git output if checkpoint extraction returned nil
	logOutput, _ := gitCmdOutput(wt, "log", baseBranch+"..HEAD", "--oneline")
	diffStat, _ := gitCmdOutput(wt, "diff", "--stat")
	s := logOutput
	if diffStat != "" {
		s += "\n---\n" + diffStat
	}
	if len(s) > 2000 {
		s = s[:2000]
	}
	if s != "" {
		m.DB.SetBlackboard(ctx, "result_summary:"+taskID, s, "monitor")
	}
}

// --- Phase 1.5: Cascade-Fail Blocked Tasks ---

func (m *Monitor) phase1_5CascadeFailBlocked(ctx context.Context) (int, error) {
	sessionTaskIDs := m.getSessionTaskIDs(ctx)

	// Check for lenient dependency mode
	lenient, _ := m.DB.GetBlackboardValue(ctx, "conductor:lenient_deps")
	if lenient == "1" {
		return m.phase1_5LenientCascade(ctx, sessionTaskIDs)
	}

	candidates, err := m.DB.GetPendingTasksWithAllBlockersTerminal(ctx, sessionTaskIDs)
	if err != nil {
		return 0, err
	}

	cascaded := 0
	for _, t := range candidates {
		if m.hasBlockerInRefinement(ctx, t) {
			m.log("CASCADE DEFERRED: %s has blocker(s) in refinement", t.ID)
			continue
		}
		reason := "cascade_fail: blocker(s) failed"
		if err := m.DB.FailTask(ctx, t.ID, reason); err != nil {
			m.log("CRITICAL: cascade FailTask error for %s: %v", t.ID, err)
			continue
		}
		m.DB.SetBlackboard(ctx, "failure_type:"+t.ID, "cascade_fail", "monitor")
		m.DB.LogEvent(ctx, "cascade_fail", "", t.ID, reason)
		m.log("CASCADE: Task %s failed (blocker(s) terminal with failure)", t.ID)
		cascaded++
	}
	return cascaded, nil
}

// phase1_5LenientCascade handles cascade logic in lenient dependency mode.
// Tasks with mixed blocker outcomes (some succeeded, some failed) proceed with
// available predecessor output instead of being cascade-failed.
func (m *Monitor) phase1_5LenientCascade(ctx context.Context, sessionTaskIDs []string) (int, error) {
	// Get tasks with mixed outcomes (some blockers succeeded, some failed)
	mixed, err := m.DB.GetPendingTasksWithMixedBlockerOutcomes(ctx, sessionTaskIDs)
	if err != nil {
		return 0, err
	}

	// Unblock mixed-outcome tasks — they can proceed with partial predecessor output
	for _, t := range mixed {
		// Annotate that this task has missing dependencies
		m.DB.SetBlackboard(ctx, "lenient_deps:"+t.ID, "1", "monitor")
		m.DB.LogEvent(ctx, "lenient_unblock", "", t.ID,
			"proceeding with partial predecessor output (lenient mode)")
		m.log("LENIENT: Task %s unblocked with partial predecessor output", t.ID)
	}

	// For tasks where ALL blockers failed (none succeeded), still cascade-fail
	allFailed, err := m.DB.GetPendingTasksWithAllBlockersTerminal(ctx, sessionTaskIDs)
	if err != nil {
		return 0, err
	}

	// Filter out mixed-outcome tasks (they were already handled above)
	mixedSet := make(map[string]bool)
	for _, t := range mixed {
		mixedSet[t.ID] = true
	}

	cascaded := 0
	for _, t := range allFailed {
		if mixedSet[t.ID] {
			continue // already handled as lenient unblock
		}
		if m.hasBlockerInRefinement(ctx, t) {
			m.log("CASCADE DEFERRED (lenient): %s has blocker in refinement", t.ID)
			continue
		}
		reason := "cascade_fail: all blocker(s) failed"
		if err := m.DB.FailTask(ctx, t.ID, reason); err != nil {
			m.log("CRITICAL: cascade FailTask error for %s: %v", t.ID, err)
			continue
		}
		m.DB.SetBlackboard(ctx, "failure_type:"+t.ID, "cascade_fail", "monitor")
		m.DB.LogEvent(ctx, "cascade_fail", "", t.ID, reason)
		m.log("CASCADE: Task %s failed (all blocker(s) failed)", t.ID)
		cascaded++
	}
	return cascaded, nil
}

// hasBlockerInRefinement checks if any failed blocker of this task is currently
// in the refinement pipeline (has been refined but not yet exhausted max rounds).
func (m *Monitor) hasBlockerInRefinement(ctx context.Context, t db.Task) bool {
	if !t.BlockedBy.Valid || t.BlockedBy.String == "" || t.BlockedBy.String == "[]" {
		return false
	}
	var blockerIDs []string
	json.Unmarshal([]byte(t.BlockedBy.String), &blockerIDs)

	for _, bID := range blockerIDs {
		blocker, err := m.DB.GetTaskByID(ctx, bID)
		if err != nil || blocker == nil || blocker.Status != "failed" {
			continue
		}
		refStr, _ := m.DB.GetBlackboardValue(ctx, "refinement:"+bID)
		if refStr == "" {
			continue // never entered refinement — not deferred
		}
		refCount, _ := strconv.Atoi(refStr)
		if refCount < agent.MaxRefinements {
			return true
		}
	}
	return false
}

// --- Phase 2: Auto-Spawn ---

func (m *Monitor) phase2AutoSpawn(ctx context.Context) (int, error) {
	// Try conductors table first (Phase 4: multi-conductor support)
	conductors, _ := m.DB.ListActiveConductors(ctx)
	if len(conductors) == 0 {
		// Fallback to blackboard for backward compat
		active, _ := m.DB.GetBlackboardValue(ctx, "conductor:active")
		if active != "1" {
			return 0, nil
		}
		// Single-conductor legacy path
		return m.phase2AutoSpawnForSession(ctx, m.getSessionTaskIDs(ctx), m.getMaxParallel(ctx))
	}

	// Multi-conductor path: iterate over each active conductor
	totalSpawned := 0
	for _, cond := range conductors {
		taskIDs, err := m.DB.ListTaskIDsByConductor(ctx, cond.ID)
		if err != nil || len(taskIDs) == 0 {
			// Fallback to blackboard session task IDs
			taskIDs = m.getSessionTaskIDs(ctx)
		}
		spawned, err := m.phase2AutoSpawnForSession(ctx, taskIDs, cond.MaxParallel)
		if err != nil {
			m.log("Auto-spawn error for conductor %s: %v", cond.ID, err)
		}
		totalSpawned += spawned
	}
	return totalSpawned, nil
}

// phase2AutoSpawnForSession handles auto-spawning for a single conductor session.
func (m *Monitor) phase2AutoSpawnForSession(ctx context.Context, sessionTaskIDs []string, maxParallel int) (int, error) {
	// Check batch spawning lock
	batchActive, _ := m.DB.GetBlackboardValue(ctx, "batch_spawning:active")
	if batchActive == "1" {
		return 0, nil
	}

	// Acquire spawn lock
	acquired, err := m.DB.AcquireBlackboardLock(ctx, "spawn_lock:active", "monitor")
	if err != nil || !acquired {
		return 0, nil
	}
	defer m.DB.DeleteBlackboard(ctx, "spawn_lock:active")

	if maxParallel <= 0 {
		maxParallel = 8
	}

	activeCount, _ := m.DB.CountTasksByStatuses(ctx, []string{"assigned", "running"}, sessionTaskIDs)
	slots := maxParallel - activeCount
	if slots <= 0 {
		return 0, nil
	}

	// Get unblocked tasks
	tasks, err := m.DB.ListUnblockedPendingTasks(ctx, sessionTaskIDs, slots)
	if err != nil {
		return 0, err
	}

	// Also get lenient-unblocked tasks (annotated by phase 1.5)
	remainingSlots := slots - len(tasks)
	if remainingSlots > 0 {
		lenientTasks, err := m.DB.ListLenientPendingTasks(ctx, sessionTaskIDs, remainingSlots)
		if err != nil {
			m.log("ListLenientPendingTasks error: %v", err)
		} else if len(lenientTasks) > 0 {
			// Dedup: skip lenient tasks already in the unblocked set
			seen := make(map[string]bool, len(tasks))
			for _, t := range tasks {
				seen[t.ID] = true
			}
			for _, lt := range lenientTasks {
				if !seen[lt.ID] {
					tasks = append(tasks, lt)
				}
			}
		}
	}

	spawned := 0
	for _, t := range tasks {
		// Re-check active count before each spawn
		currentActive, _ := m.DB.CountTasksByStatuses(ctx, []string{"assigned", "running"}, sessionTaskIDs)
		if currentActive >= maxParallel {
			break
		}

		_, err := m.Spawner.Run(ctx, agent.SpawnOpts{
			TaskID: t.ID,
			Role:   agent.Role(t.Role),
		})
		if err != nil {
			m.log("Auto-spawn failed for %s: %v", t.ID, err)
			m.DB.LogEvent(ctx, "auto_spawn_error", "", t.ID, err.Error())
			// Clear stale assignment so the task can be retried next cycle
			if strings.Contains(err.Error(), "already assigned") {
				m.DB.ClearTaskAssignment(ctx, t.ID)
			}
			continue
		}
		m.DB.LogEvent(ctx, "auto_spawned", "", t.ID, "")
		spawned++
	}
	return spawned, nil
}

// getMaxParallel reads the max_parallel setting from blackboard (legacy path).
func (m *Monitor) getMaxParallel(ctx context.Context) int {
	maxParallelStr, _ := m.DB.GetBlackboardValue(ctx, "conductor:max_parallel")
	if n, err := strconv.Atoi(maxParallelStr); err == nil && n > 0 {
		return n
	}
	return 8
}

// --- Phase 2.5: Predict Merge Conflicts ---

func (m *Monitor) phase2_5PredictConflicts(ctx context.Context) error {
	// Only run every 4th cycle (~60s at 15s interval)
	if m.cycleCount%4 != 0 {
		return nil
	}

	// Only in conductor mode
	active, _ := m.DB.GetBlackboardValue(ctx, "conductor:active")
	if active != "1" {
		return nil
	}

	// Get base branch
	baseBranch := m.getBaseBranch(ctx)

	// Get all running/assigned tasks with branches
	sessionTaskIDs := m.getSessionTaskIDs(ctx)
	tasks, err := m.DB.ListTasks(ctx)
	if err != nil {
		return fmt.Errorf("listing tasks: %w", err)
	}

	sessionSet := make(map[string]bool)
	for _, id := range sessionTaskIDs {
		sessionSet[id] = true
	}

	var branches []struct{ taskID, branch string }
	for _, t := range tasks {
		if t.Status != "assigned" && t.Status != "running" {
			continue
		}
		if len(sessionSet) > 0 && !sessionSet[t.ID] {
			continue
		}
		if !t.Branch.Valid || t.Branch.String == "" {
			continue
		}
		branches = append(branches, struct{ taskID, branch string }{t.ID, t.Branch.String})
	}

	if len(branches) < 2 {
		return nil
	}

	// For each pair of active branches, run git merge-tree
	baseRef, err := gitCmdOutput(m.RepoRoot, "rev-parse", baseBranch)
	if err != nil {
		return nil // fail-open: can't resolve base
	}
	baseRef = strings.TrimSpace(baseRef)

	for i := 0; i < len(branches); i++ {
		for j := i + 1; j < len(branches); j++ {
			b1 := branches[i]
			b2 := branches[j]

			cmd := exec.Command("git", "merge-tree", "--write-tree", b1.branch, b2.branch)
			cmd.Dir = m.RepoRoot
			out, err := cmd.CombinedOutput()
			if err != nil {
				// Non-zero exit = conflicts exist
				outStr := strings.TrimSpace(string(out))
				// Extract conflicting file names from merge-tree output
				var conflictFiles []string
				for _, line := range strings.Split(outStr, "\n") {
					// merge-tree output includes lines with file paths for conflicts
					line = strings.TrimSpace(line)
					if strings.HasSuffix(line, ".go") || strings.HasSuffix(line, ".sql") ||
						strings.HasSuffix(line, ".md") || strings.Contains(line, "/") {
						if !strings.Contains(line, " ") || strings.HasPrefix(line, "CONFLICT") {
							conflictFiles = append(conflictFiles, line)
						}
					}
				}

				payload := fmt.Sprintf(`{"branch1":%q,"branch2":%q,"task1":%q,"task2":%q,"files":%d}`,
					b1.branch, b2.branch, b1.taskID, b2.taskID, len(conflictFiles))
				m.DB.LogEvent(ctx, "merge_conflict_predicted", "", b1.taskID, payload)
				m.log("CONFLICT PREDICTED: %s vs %s (%d files)", b1.branch, b2.branch, len(conflictFiles))
			}
		}
	}

	return nil
}

// --- Phase 3: Auto-Merge ---

func (m *Monitor) phase3AutoMerge(ctx context.Context) error {
	// Try conductors table first (Phase 4: multi-conductor support)
	conductors, _ := m.DB.ListActiveConductors(ctx)
	if len(conductors) > 0 {
		for _, cond := range conductors {
			if err := m.phase3AutoMergeForConductor(ctx, cond); err != nil {
				m.log("Auto-merge error for conductor %s: %v", cond.ID, err)
			}
		}
		return nil
	}

	// Fallback to blackboard for backward compat
	active, _ := m.DB.GetBlackboardValue(ctx, "conductor:active")
	if active != "1" {
		return nil
	}

	// Legacy single-conductor path
	conductorPIDStr, _ := m.DB.GetBlackboardValue(ctx, "conductor:pid")
	if conductorPIDStr != "" {
		pid, _ := strconv.Atoi(conductorPIDStr)
		if pid > 0 && agent.PidAlive(pid) {
			return nil
		}
	}

	sessionTaskIDs := m.getSessionTaskIDs(ctx)
	return m.phase3AutoMergeLegacy(ctx, sessionTaskIDs)
}

// phase3AutoMergeForConductor handles auto-merge for a single conductor from the DB.
func (m *Monitor) phase3AutoMergeForConductor(ctx context.Context, cond db.ConductorRecord) error {
	// Check if conductor is alive
	if cond.PID > 0 && agent.PidAlive(cond.PID) {
		return nil // conductor is alive, nothing to do
	}

	// G113: Skip terminal conductors. Only auto-merge for "active" conductors
	// with dead PIDs (crash recovery). "abandoned"/"completed"/"failed" conductors
	// have already been handled or intentionally stopped.
	if cond.Status != "active" {
		return nil
	}

	// B-145: Check for interrupted merge state before checking terminal tasks.
	// If the conductor died mid-merge, resume from where it left off.
	if cond.MergeStatus.Valid && cond.MergeStatus.String != "" {
		return m.recoverMerge(ctx, cond)
	}

	// Conductor is dead — check if all tasks are terminal
	taskIDs, _ := m.DB.ListTaskIDsByConductor(ctx, cond.ID)
	if len(taskIDs) == 0 {
		return nil
	}

	pending, _ := m.DB.CountTasksByStatuses(ctx, []string{"pending"}, taskIDs)
	active, _ := m.DB.CountTasksByStatuses(ctx, []string{"assigned", "running", "review"}, taskIDs)
	done, _ := m.DB.CountTasksByStatuses(ctx, []string{"done"}, taskIDs)

	if pending > 0 || active > 0 || done == 0 {
		return nil
	}

	// B-280: FIFO merge strategy — if conductor died mid-FIFO, fall through to batch
	// merge which will process all remaining done branches. The merge_queue_entries
	// table retains state for forensics but batch merge is correct for recovery.
	if cond.MergeStrategy == "fifo" {
		m.log("Auto-merge for FIFO conductor %s: falling back to batch merge for recovery", cond.ID)
	}

	m.log("Auto-merge triggered for conductor %s: conductor dead, all tasks terminal", cond.ID)
	m.DB.LogEvent(ctx, "auto_merge_triggered", "", "", fmt.Sprintf(`{"conductor":"%s","merge_strategy":"%s"}`, cond.ID, cond.MergeStrategy))

	testCmd := ""
	if cond.TestCmd.Valid {
		testCmd = cond.TestCmd.String
	}

	// Phase 5: Run quality gates before merge (optional, fail-open).
	if m.QualityCheckFunc != nil {
		if qErr := m.QualityCheckFunc(ctx, m.RepoRoot, "dev", cond.ID); qErr != nil {
			m.log("Quality gate check: %v", qErr)
			m.DB.LogEvent(ctx, "quality_gate_result", "", "", qErr.Error())
			// Log but don't block — quality gates are advisory in Phase 5.
			// Phase 6+ can make this a hard gate.
		}
	}

	var mergeErr error
	if m.MergeFunc != nil {
		mergeErr = m.MergeFunc(ctx, testCmd, cond.MergeReview)
		if mergeErr != nil {
			m.log("Go merge error for conductor %s: %v", cond.ID, mergeErr)
		}
	} else {
		m.log("WARN: MergeFunc not set — skipping auto-merge for conductor %s", cond.ID)
		return nil
	}

	if mergeErr != nil {
		m.DB.SetBlackboard(ctx, "merge:failed", mergeErr.Error(), "monitor")
		m.DB.LogEvent(ctx, "auto_merge_failed", "", "", mergeErr.Error())
		return fmt.Errorf("auto-merge failed: %w", mergeErr)
	}

	// B-273: After successful branch merge, route based on merge_mode.
	if cond.MergeMode == "pr" && m.PRCreateFunc != nil {
		m.log("Auto-merge PR mode: creating PR for conductor %s", cond.ID)
		if _, prErr := m.PRCreateFunc(ctx); prErr != nil {
			m.log("Auto-merge PR creation failed for conductor %s: %v", cond.ID, prErr)
			m.DB.LogEvent(ctx, "auto_merge_pr_failed", "", "",
				fmt.Sprintf(`{"conductor":"%s","error":%q}`, cond.ID, prErr.Error()))
		}
		// Skip staging branch deletion — PR references it
	}

	// Cleanup worktrees for all terminal tasks
	m.cleanupSessionWorktrees(ctx, taskIDs)

	// Kill Docker containers if running in Docker mode
	if cond.Runtime == "docker" {
		agent.KillAllOrchestraContainers()
	}

	// Update conductor status to abandoned (conductor died, we cleaned up)
	m.DB.UpdateConductorStatus(ctx, cond.ID, "abandoned")

	// Deactivate blackboard keys for backward compat
	for _, key := range []string{"conductor:active", "conductor:pid", "conductor:test_cmd", "conductor:merge_review", "conductor:max_parallel", "conductor:merge_complete", "conductor:runtime", "conductor:merge_mode"} {
		m.DB.DeleteBlackboard(ctx, key)
	}

	return nil
}

// phase3AutoMergeLegacy handles auto-merge using blackboard keys (pre-Phase 4 compat).
func (m *Monitor) phase3AutoMergeLegacy(ctx context.Context, sessionTaskIDs []string) error {
	pending, _ := m.DB.CountTasksByStatuses(ctx, []string{"pending"}, sessionTaskIDs)
	active, _ := m.DB.CountTasksByStatuses(ctx, []string{"assigned", "running", "review"}, sessionTaskIDs)
	done, _ := m.DB.CountTasksByStatuses(ctx, []string{"done"}, sessionTaskIDs)

	if pending > 0 || active > 0 || done == 0 {
		return nil
	}

	m.log("Auto-merge triggered: conductor dead, all tasks terminal")
	m.DB.LogEvent(ctx, "auto_merge_triggered", "", "", "")

	testCmd, _ := m.DB.GetBlackboardValue(ctx, "conductor:test_cmd")
	mergeReview, _ := m.DB.GetBlackboardValue(ctx, "conductor:merge_review")

	var mergeErr error
	if m.MergeFunc != nil {
		mergeErr = m.MergeFunc(ctx, testCmd, mergeReview == "1")
		if mergeErr != nil {
			m.log("Go merge error: %v", mergeErr)
		}
	} else {
		m.log("WARN: MergeFunc not set — skipping auto-merge.")
		return nil
	}

	if mergeErr != nil {
		m.DB.SetBlackboard(ctx, "merge:failed", mergeErr.Error(), "monitor")
		m.DB.LogEvent(ctx, "auto_merge_failed", "", "", mergeErr.Error())
		return fmt.Errorf("auto-merge failed: %w", mergeErr)
	}

	m.cleanupSessionWorktrees(ctx, sessionTaskIDs)

	runtime, _ := m.DB.GetBlackboardValue(ctx, "conductor:runtime")
	if runtime == "docker" {
		agent.KillAllOrchestraContainers()
	}

	for _, key := range []string{"conductor:active", "conductor:pid", "conductor:test_cmd", "conductor:merge_review", "conductor:max_parallel", "conductor:merge_complete", "conductor:runtime"} {
		m.DB.DeleteBlackboard(ctx, key)
	}

	return nil
}

// recoverMerge resumes an interrupted merge from the state recorded in the conductor (B-145).
func (m *Monitor) recoverMerge(ctx context.Context, cond db.ConductorRecord) error {
	status := cond.MergeStatus.String
	m.log("RECOVER MERGE: conductor %s died mid-merge (status=%s, branches_done=%v)",
		cond.ID, status, cond.MergeBranchesDone)
	m.DB.LogEvent(ctx, "merge_recovery_started", "", "",
		fmt.Sprintf(`{"conductor":"%s","status":"%s","branches_done":%d}`,
			cond.ID, status, len(cond.MergeBranchesDone)))

	testCmd := ""
	if cond.TestCmd.Valid {
		testCmd = cond.TestCmd.String
	}

	switch status {
	case "done":
		// Merge completed but conductor died before cleanup — skip straight to cleanup.
		m.log("RECOVER: Merge already done, proceeding to cleanup")

	case "staging":
		// Branch merges done, staging-to-dev was interrupted.
		// B-273: If merge_mode is "pr", create a PR instead of local staging merge.
		if cond.MergeMode == "pr" && m.PRCreateFunc != nil {
			if _, prErr := m.PRCreateFunc(ctx); prErr != nil {
				m.log("RECOVER: PR creation failed: %v", prErr)
				m.DB.LogEvent(ctx, "merge_recovery_failed", "", "",
					fmt.Sprintf(`{"phase":"staging_pr","error":%q}`, prErr.Error()))
				return fmt.Errorf("recovery PR creation: %w", prErr)
			}
		} else if m.StagingMergeFunc != nil {
			if err := m.StagingMergeFunc(ctx); err != nil {
				m.log("RECOVER: Staging merge failed: %v", err)
				m.DB.LogEvent(ctx, "merge_recovery_failed", "", "",
					fmt.Sprintf(`{"phase":"staging","error":%q}`, err.Error()))
				return fmt.Errorf("recovery staging merge: %w", err)
			}
		} else if m.MergeFunc != nil {
			// Fallback: re-run full merge (branches already merged, will be no-ops)
			if err := m.MergeFunc(ctx, testCmd, cond.MergeReview); err != nil {
				return fmt.Errorf("recovery fallback merge: %w", err)
			}
		}

	case "merging":
		// Branch merge loop was interrupted — resume with skip list.
		if m.MergeFuncWithSkip != nil {
			if err := m.MergeFuncWithSkip(ctx, testCmd, cond.MergeReview, cond.MergeBranchesDone); err != nil {
				m.log("RECOVER: Merge with skip failed: %v", err)
				return fmt.Errorf("recovery merge with skip: %w", err)
			}
		} else if m.MergeFunc != nil {
			// Fallback: re-run full merge
			if err := m.MergeFunc(ctx, testCmd, cond.MergeReview); err != nil {
				return fmt.Errorf("recovery fallback merge: %w", err)
			}
		}

	default:
		// Unknown status — re-run full merge
		m.log("RECOVER: Unknown merge status %q, running full merge", status)
		if m.MergeFunc != nil {
			if err := m.MergeFunc(ctx, testCmd, cond.MergeReview); err != nil {
				return fmt.Errorf("recovery full merge: %w", err)
			}
		}
	}

	// Cleanup: worktrees, docker, status update, blackboard cleanup
	taskIDs, _ := m.DB.ListTaskIDsByConductor(ctx, cond.ID)
	m.cleanupSessionWorktrees(ctx, taskIDs)

	if cond.Runtime == "docker" {
		agent.KillAllOrchestraContainers()
	}

	m.DB.UpdateConductorStatus(ctx, cond.ID, "abandoned")
	for _, key := range []string{"conductor:active", "conductor:pid", "conductor:test_cmd", "conductor:merge_review", "conductor:max_parallel", "conductor:merge_complete", "conductor:runtime"} {
		m.DB.DeleteBlackboard(ctx, key)
	}

	m.DB.LogEvent(ctx, "merge_recovery_completed", "", "",
		fmt.Sprintf(`{"conductor":"%s","recovered_status":"%s"}`, cond.ID, status))
	return nil
}

func (m *Monitor) cleanupSessionWorktrees(ctx context.Context, sessionTaskIDs []string) {
	sessionSet := make(map[string]bool)
	for _, id := range sessionTaskIDs {
		sessionSet[id] = true
	}

	tasks, _ := m.DB.ListTasks(ctx)
	for _, t := range tasks {
		// Only clean up terminal tasks (done or failed)
		if t.Status != "done" && t.Status != "failed" {
			continue
		}
		if !t.Worktree.Valid {
			continue
		}
		// If session filter is set, only clean session tasks
		if len(sessionSet) > 0 && !sessionSet[t.ID] {
			continue
		}

		// Verify "done" branches were actually merged before cleanup.
		// Uses `git branch --merged` instead of event scanning — more reliable.
		if t.Status == "done" && t.Branch.Valid {
			baseBranch := m.getBaseBranch(ctx)
			if !m.isBranchMerged(t.Branch.String, baseBranch) {
				// Branch not merged — create backup ref and skip cleanup
				sha := m.getBranchSHA(t.Branch.String, t.Worktree.String)
				if sha != "" {
					runBash(m.RepoRoot, "git", "update-ref", "refs/orchestra-backup/"+t.ID, sha)
					m.DB.LogEvent(ctx, "branch_preserved", "", t.ID,
						fmt.Sprintf(`{"ref":"refs/orchestra-backup/%s","sha":"%s"}`, t.ID, sha))
				}
				m.log("PRESERVE: Branch %s not merged into %s — backup ref created, skipping cleanup for %s",
					t.Branch.String, baseBranch, t.ID)
				continue
			}
		}

		wt := t.Worktree.String
		if _, err := os.Stat(wt); err == nil {
			// Salvage uncommitted changes before removal (failed tasks may have partial work)
			if t.Status == "failed" {
				agent.SalvageWorktreeChanges(ctx, wt, t.ID)
			}
			runBash(m.RepoRoot, "git", "worktree", "remove", "--force", wt)
		}
		if t.Branch.Valid {
			runBash(m.RepoRoot, "git", "branch", "-D", t.Branch.String)
		}
	}
}

// --- Cleanup ---

func (m *Monitor) cleanup(ctx context.Context) {
	m.DB.CleanupDeadAgents(ctx, 2)
	m.DB.CleanupExpiredLocks(ctx)
	m.DB.WALCheckpoint(ctx)
	m.DB.SetBlackboard(ctx, "monitor:heartbeat", time.Now().UTC().Format(time.RFC3339), "monitor")
}

// --- Helpers ---

func (m *Monitor) getSessionTaskIDs(ctx context.Context) []string {
	taskIDsJSON, _ := m.DB.GetBlackboardValue(ctx, "conductor:task_ids")
	if taskIDsJSON == "" || taskIDsJSON == "[]" {
		return nil
	}
	// Simple JSON array parsing without encoding/json
	taskIDsJSON = strings.Trim(taskIDsJSON, "[]")
	if taskIDsJSON == "" {
		return nil
	}
	var ids []string
	for _, part := range strings.Split(taskIDsJSON, ",") {
		id := strings.Trim(strings.TrimSpace(part), `"`)
		if id != "" {
			ids = append(ids, id)
		}
	}
	return ids
}

// getBaseBranch returns the base branch from blackboard or auto-detects from the repo.
func (m *Monitor) getBaseBranch(ctx context.Context) string {
	if base, _ := m.DB.GetBlackboardValue(ctx, "conductor:base_branch"); base != "" {
		return base
	}
	return agent.DetectBaseBranch(m.RepoRoot)
}

// isBranchMerged checks if a branch has been merged into the base branch
// using `git branch --merged <base>`.
func (m *Monitor) isBranchMerged(branch, base string) bool {
	out, err := gitCmdOutput(m.RepoRoot, "branch", "--merged", base)
	if err != nil {
		return false // fail-open: assume not merged to preserve the branch
	}
	for _, line := range strings.Split(out, "\n") {
		// git branch output uses: "* " for current, "+ " for worktree branches
		name := strings.TrimSpace(line)
		name = strings.TrimPrefix(name, "* ")
		name = strings.TrimPrefix(name, "+ ")
		name = strings.TrimSpace(name)
		if name == branch {
			return true
		}
	}
	return false
}

// getBranchSHA returns the SHA of a branch tip. Tries the worktree first (if it
// still exists), then falls back to resolving the branch ref directly.
func (m *Monitor) getBranchSHA(branch, worktree string) string {
	// Try worktree HEAD first
	if worktree != "" {
		if sha, err := gitCmdOutput(worktree, "rev-parse", "HEAD"); err == nil {
			return strings.TrimSpace(sha)
		}
	}
	// Fall back to branch ref
	if sha, err := gitCmdOutput(m.RepoRoot, "rev-parse", branch); err == nil {
		return strings.TrimSpace(sha)
	}
	return ""
}

func gitCmdOutput(dir string, args ...string) (string, error) {
	return agent.GitCmd(dir, args...)
}

func runBash(dir string, args ...string) error {
	// os/exec helper for bash commands
	if len(args) == 0 {
		return fmt.Errorf("no command provided")
	}
	cmd := exec.Command(args[0], args[1:]...)
	cmd.Dir = dir
	_, err := cmd.CombinedOutput()
	return err
}
