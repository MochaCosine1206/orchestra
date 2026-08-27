package orchestrator

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/MochaCosine1206/orchestra/internal/agent"
	"github.com/MochaCosine1206/orchestra/internal/config"
	"github.com/MochaCosine1206/orchestra/internal/db"
	"github.com/MochaCosine1206/orchestra/internal/healing"
	"github.com/MochaCosine1206/orchestra/internal/monitor"
)

// Conductor orchestrates the full goal-to-merge workflow.
type Conductor struct {
	DB           *db.DB
	Spawner      *agent.Spawner
	Monitor      *monitor.Monitor
	Runner       ClaudeRunner
	RepoRoot     string
	SessionID    string
	ConductorID  string // ID in the conductors table (same as SessionID for new sessions)
	Log          func(string)
	ProgressFunc func(phase, detail string) // optional callback for phased progress updates

	// ConductorLogCh receives all conductor log messages for TUI display.
	// If non-nil, log() sends to this channel (non-blocking).
	ConductorLogCh chan<- string

	// TUI clarification channels (set by TUI before goal dispatch)
	ClarifyQuestionCh chan<- []ClarifyQuestion // conductor sends questions
	ClarifyAnswerCh   <-chan []ClarifyQuestion // conductor receives answered questions

	DisableNotify   bool     // suppress Telegram notifications (for tests)
	conductorActive bool     // guards against double-activation (G103)
	logFile         *os.File // persistent conductor log file (G105)
	currentPhaseID  string   // current phase ID for multi-phase GoSpec() (G110)
	keepStaging     bool     // preserve staging branch after Go() (G111)
}

// ConductorOpts configures a new Conductor.
type ConductorOpts struct {
	DB            *db.DB
	Runner        ClaudeRunner
	RepoRoot      string
	ModelStrategy agent.ModelStrategy
	SessionID     string // optional: if non-empty, used instead of generating a new one
}

// IsGitRepo checks whether the given directory is inside a git repository.
func IsGitRepo(dir string) bool {
	_, err := os.Stat(filepath.Join(dir, ".git"))
	return err == nil
}

// New creates a Conductor from the given options.
func New(opts ConductorOpts) (*Conductor, error) {
	if opts.DB == nil {
		return nil, fmt.Errorf("DB is required")
	}
	if opts.RepoRoot == "" {
		return nil, fmt.Errorf("RepoRoot is required")
	}

	logsDir := filepath.Join(opts.RepoRoot, ".orchestra", "logs")
	pidsDir := filepath.Join(opts.RepoRoot, ".orchestra", "pids")

	// Load local runner config: project-level first, then global config.
	// System-level config means pi is available on any project without per-project setup.
	var localRunner *agent.LocalRunnerConfig
	localRunnerPath := filepath.Join(opts.RepoRoot, ".orchestra", "local-runner.json")
	if data, err := os.ReadFile(localRunnerPath); err == nil {
		var cfg agent.LocalRunnerConfig
		if json.Unmarshal(data, &cfg) == nil && cfg.Enabled {
			localRunner = &cfg
			slog.Info("local runner enabled (project)", "command", cfg.Command, "model", cfg.Model, "max_concurrent", cfg.MaxConcurrent)
		}
	}
	if localRunner == nil {
		if globalCfg, err := config.Load(); err == nil && globalCfg.LocalRunner != nil && globalCfg.LocalRunner.Enabled {
			lr := globalCfg.LocalRunner
			localRunner = &agent.LocalRunnerConfig{
				Enabled:       lr.Enabled,
				Command:       lr.Command,
				Model:         lr.Model,
				MaxConcurrent: lr.MaxConcurrent,
				MaxFiles:      lr.MaxFiles,
			}
			slog.Info("local runner enabled (global)", "command", lr.Command, "model", lr.Model, "max_concurrent", lr.MaxConcurrent)
		}
	}

	spawner := &agent.Spawner{
		DB:            opts.DB,
		RepoRoot:      opts.RepoRoot,
		LogsDir:       logsDir,
		PidsDir:       pidsDir,
		ModelStrategy: opts.ModelStrategy,
		LocalRunner:   localRunner,
	}

	mon := &monitor.Monitor{
		DB:       opts.DB,
		Spawner:  spawner,
		RepoRoot: opts.RepoRoot,
		LogsDir:  logsDir,
		PidsDir:  pidsDir,
	}

	sessionID := opts.SessionID
	if sessionID == "" {
		sessionID = db.GenID("s")
	}

	c := &Conductor{
		DB:        opts.DB,
		Spawner:   spawner,
		Monitor:   mon,
		Runner:    opts.Runner,
		RepoRoot:  opts.RepoRoot,
		SessionID: sessionID,
	}

	// Wire Go merge into monitor for auto-merge phase
	mon.MergeFunc = func(ctx context.Context, testCmd string, review bool) error {
		_, err := c.Merge(ctx, MergeOpts{TestCmd: testCmd, Review: review})
		return err
	}

	// B-145: Wire merge with skip-branches support for crash recovery.
	mon.MergeFuncWithSkip = func(ctx context.Context, testCmd string, review bool, skipBranches []string) error {
		_, err := c.Merge(ctx, MergeOpts{TestCmd: testCmd, Review: review, SkipBranches: skipBranches})
		return err
	}
	mon.StagingMergeFunc = func(ctx context.Context) error {
		return c.MergeStagingToDev(ctx)
	}
	mon.PRCreateFunc = func(ctx context.Context) (*monitor.PRCreateResult, error) {
		pr, err := c.CreateStagingPR(ctx)
		if err != nil || pr == nil {
			return nil, err
		}
		return &monitor.PRCreateResult{
			PRURL:    pr.PRURL,
			PRNumber: pr.PRNumber,
			Branch:   pr.Branch,
			Base:     pr.Base,
		}, nil
	}

	// V3: Wire task gate for pre-merge build+test validation.
	mon.TaskGateFunc = func(ctx context.Context, worktreePath string, testCmd string) (bool, string, string) {
		result := RunTaskGate(ctx, worktreePath, testCmd)
		if result.Passed {
			return true, "", ""
		}
		// Combine build and test output for error context
		errOutput := result.BuildOutput
		if result.TestOutput != "" {
			if errOutput != "" {
				errOutput += "\n"
			}
			errOutput += result.TestOutput
		}
		return false, string(result.Triage), errOutput
	}

	// Wire healing for build failures
	mon.HealFunc = func(ctx context.Context, sessID, taskID, errorType string) (bool, error) {
		healer := healing.NewHealer(sessID, opts.DB)
		defer healer.Close()
		task, err := opts.DB.GetTaskByID(ctx, taskID)
		if err != nil || task == nil {
			return false, fmt.Errorf("task %s not found for healing", taskID)
		}
		wt := ""
		if task.Worktree.Valid {
			wt = task.Worktree.String
		}
		result := healer.Heal(ctx, taskID, errorType, wt, nil, nil)
		if result.Fixed {
			healer.Confirm(result.FixID)
			return true, nil
		}
		if result.Err != nil {
			return false, result.Err
		}
		return false, nil
	}

	// Wire runner logger so CLI invocations are traceable
	if er, ok := opts.Runner.(*ExecRunner); ok && er.Logger == nil {
		er.Logger = func(msg string) { c.log("%s", msg) }
	}

	// Wire re-decomposition for context-exhausted tasks
	mon.ReDecomposeFunc = func(ctx context.Context, taskID, checkpoint string) ([]string, error) {
		return c.ReDecompose(ctx, taskID, checkpoint)
	}

	// Wire immediate spawn of dependent tasks when a task completes.
	// This avoids the 15s monitor delay for handoffs (e.g. researcher → implementer).
	spawner.OnTaskCompleted = func(ctx context.Context, completedTaskID string) {
		taskIDs := c.sessionTaskIDs(ctx)
		if len(taskIDs) == 0 {
			return
		}
		if _, err := spawner.Batch(ctx, 5, 0, taskIDs); err != nil {
			c.log("Immediate spawn after %s: %v", completedTaskID, err)
		}
	}

	return c, nil
}

// SetupLogFile creates a persistent log file for the conductor session.
// All log() calls will also write to this file, providing forensics if the conductor dies.
// ReloadLocalRunner re-reads .orchestra/local-runner.json and updates the spawner.
// Called before execution in orchestra new --execute, since the config may have
// been injected after the conductor was created (e.g., by a background script or
// the scaffold step).
func (c *Conductor) ReloadLocalRunner() {
	// Check project .orchestra/ first, then global config
	localRunnerPath := filepath.Join(c.RepoRoot, ".orchestra", "local-runner.json")
	if data, err := os.ReadFile(localRunnerPath); err == nil {
		var cfg agent.LocalRunnerConfig
		if json.Unmarshal(data, &cfg) == nil && cfg.Enabled {
			c.Spawner.LocalRunner = &cfg
			slog.Info("local runner reloaded (project)", "command", cfg.Command, "model", cfg.Model)
			return
		}
	}
	// Fall back to global config
	if globalCfg, err := config.Load(); err == nil && globalCfg.LocalRunner != nil && globalCfg.LocalRunner.Enabled {
		lr := globalCfg.LocalRunner
		c.Spawner.LocalRunner = &agent.LocalRunnerConfig{
			Enabled:       lr.Enabled,
			Command:       lr.Command,
			Model:         lr.Model,
			MaxConcurrent: lr.MaxConcurrent,
			MaxFiles:      lr.MaxFiles,
		}
		slog.Info("local runner reloaded (global)", "command", lr.Command, "model", lr.Model)
	}
}

func (c *Conductor) SetupLogFile() error {
	logsDir := filepath.Join(c.RepoRoot, ".orchestra", "logs")
	if err := os.MkdirAll(logsDir, 0o755); err != nil {
		return fmt.Errorf("creating logs dir: %w", err)
	}
	f, err := os.OpenFile(
		filepath.Join(logsDir, fmt.Sprintf("conductor-%s.log", c.SessionID)),
		os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	c.logFile = f
	return nil
}

// CloseLogFile closes the persistent conductor log file.
func (c *Conductor) CloseLogFile() {
	if c.logFile != nil {
		c.logFile.Close()
		c.logFile = nil
	}
}

func (c *Conductor) log(format string, args ...interface{}) {
	msg := fmt.Sprintf(format, args...)
	if c.ConductorLogCh != nil {
		select {
		case c.ConductorLogCh <- msg:
		default: // non-blocking drop if full
		}
	}
	if c.logFile != nil {
		fmt.Fprintf(c.logFile, "%s %s\n", time.Now().Format("15:04:05"), msg)
	}
	if c.Log != nil {
		c.Log(msg)
	} else {
		fmt.Fprintf(os.Stderr, "%s\n", msg)
	}
}

// progress reports a phased progress update to the ProgressFunc callback if set.
func (c *Conductor) progress(phase, detail string) {
	c.log("[%s] %s", phase, detail)
	if c.ProgressFunc != nil {
		c.ProgressFunc(phase, detail)
	}
}
