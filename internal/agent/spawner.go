package agent

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/MochaCosine1206/orchestra/internal/db"
	"github.com/MochaCosine1206/orchestra/internal/recursion"
	"github.com/MochaCosine1206/orchestra/internal/sandbox"
)

// filterEnv returns a copy of env with the named variable removed.
func filterEnv(env []string, name string) []string {
	prefix := name + "="
	filtered := make([]string, 0, len(env))
	for _, e := range env {
		if !strings.HasPrefix(e, prefix) {
			filtered = append(filtered, e)
		}
	}
	return filtered
}

// ensureMaxOutputTokens sets CLAUDE_CODE_MAX_OUTPUT_TOKENS if not already
// present in the environment. Large prompts (planning docs, specs) and long
// agent runs routinely exceed the 32K default.
func ensureMaxOutputTokens(env []string) []string {
	const key = "CLAUDE_CODE_MAX_OUTPUT_TOKENS"
	for _, e := range env {
		if strings.HasPrefix(e, key+"=") {
			return env // already set by user, respect their value
		}
	}
	return append(env, key+"=128000")
}

// Spawner manages the lifecycle of agent processes.
type Spawner struct {
	DB            *db.DB
	RepoRoot      string
	LogsDir       string
	PidsDir       string
	SpawnCmd      string             // "claude" default, overridable for tests
	ModelStrategy ModelStrategy      // override strategy (empty = use blackboard or default)
	Runtime       string             // "local" (default) or "docker"
	Docker        DockerConfig       // Docker settings (only used when Runtime == "docker")
	Sandbox       *sandbox.Sandbox   // non-nil when sandbox mode is enabled
	LocalRunner   *LocalRunnerConfig // non-nil when local LLM routing is enabled

	// OnTaskCompleted is called after a task transitions to done.
	// The conductor sets this to trigger immediate spawning of dependent tasks.
	OnTaskCompleted func(ctx context.Context, taskID string)
}

// SpawnOpts configures a single Run invocation.
type SpawnOpts struct {
	TaskID  string
	Role    Role
	Model   string // override model (empty = default)
	Timeout time.Duration
}

// SpawnResult captures the outcome of a successful spawn.
type SpawnResult struct {
	AgentID  string
	PID      int
	Worktree string
	Model    string
}

func (s *Spawner) spawnCmd() string {
	if s.SpawnCmd != "" {
		return s.SpawnCmd
	}
	return "claude"
}

// getStagingBranch returns the staging branch for the current conductor session.
// If a conductor record exists with a staging branch, agents will fork from it
// instead of HEAD. Returns empty string if no staging branch is configured.
func (s *Spawner) getStagingBranch(ctx context.Context) string {
	// Check conductors table for active conductor with staging branch
	conductors, err := s.DB.ListActiveConductors(ctx)
	if err == nil && len(conductors) > 0 {
		// Use the most recently started active conductor's staging branch
		return conductors[0].StagingBranch
	}
	return ""
}

// getClusterBranchOverride returns the cluster branch if the task has a feature_cluster
// and the cluster branch exists. Otherwise returns the original startPoint unchanged.
// B-281: In hierarchical mode, agents branch from their cluster branch (not staging).
func (s *Spawner) getClusterBranchOverride(ctx context.Context, taskID, startPoint string) string {
	task, err := s.DB.GetTaskByID(ctx, taskID)
	if err != nil || task == nil {
		return startPoint
	}
	if !task.FeatureCluster.Valid || task.FeatureCluster.String == "" {
		return startPoint
	}
	if !task.ConductorID.Valid || task.ConductorID.String == "" {
		return startPoint
	}

	clusterBranch := fmt.Sprintf("cluster/%s/%s", task.ConductorID.String, task.FeatureCluster.String)

	// Verify the cluster branch exists
	cmd := exec.CommandContext(ctx, "git", "rev-parse", "--verify", clusterBranch)
	cmd.Dir = s.RepoRoot
	if err := cmd.Run(); err != nil {
		return startPoint // cluster branch doesn't exist yet — fall back to staging
	}

	return clusterBranch
}

// isDockerRuntime returns true if Docker execution mode is active.
// Checks the struct field first, then falls back to the blackboard value.
func (s *Spawner) isDockerRuntime(ctx context.Context) bool {
	if s.Runtime == "docker" {
		return true
	}
	val, _ := s.DB.GetBlackboardValue(ctx, "conductor:runtime")
	return val == "docker"
}

// buildExecCmd creates the exec.Command for an agent invocation.
// When runtime is "docker", wraps the args in docker run.
// cmdOverride, if non-empty, replaces the default spawn command (e.g., "pi" instead of "claude").
func (s *Spawner) buildExecCmd(ctx context.Context, taskID string, claudeArgs []string, worktreePath string, cmdOverride ...string) *exec.Cmd {
	if s.isDockerRuntime(ctx) {
		cfg := s.Docker
		if cfg.Image == "" {
			cfg = DefaultDockerConfig()
		}
		setupCmds := DetectSetupCommands(s.RepoRoot)
		dockerArgs := BuildDockerRunArgs(cfg, taskID, s.RepoRoot, worktreePath, s.LogsDir, claudeArgs, setupCmds)
		cmd := exec.Command("docker", dockerArgs...)
		cmd.Dir = s.RepoRoot
		cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
		// Inherit CLAUDE_CODE_OAUTH_TOKEN from parent environment so docker
		// --env NAME picks it up without exposing the value in the process list.
		cmd.Env = ensureMaxOutputTokens(os.Environ())
		return cmd
	}

	// Local execution (default)
	spawnBin := s.spawnCmd()
	if len(cmdOverride) > 0 && cmdOverride[0] != "" {
		spawnBin = cmdOverride[0]
	}
	cmd := exec.Command(spawnBin, claudeArgs...)
	cmd.Dir = worktreePath
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Env = filterEnv(os.Environ(), "CLAUDECODE")
	cmd.Env = ensureMaxOutputTokens(cmd.Env)
	cmd.Env = append(cmd.Env, s.orchestraEnv(taskID)...)
	return cmd
}

// projectSlug returns a filesystem-safe slug derived from the RepoRoot basename.
// Matches the shell equivalent: basename | lowercase | spaces→hyphens | strip non-safe.
func (s *Spawner) projectSlug() string {
	base := filepath.Base(s.RepoRoot)
	slug := strings.ToLower(base)
	slug = strings.ReplaceAll(slug, " ", "-")
	var out strings.Builder
	for _, r := range slug {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '.' || r == '_' || r == '-' {
			out.WriteRune(r)
		}
	}
	return out.String()
}

// orchestraEnv returns ORCHESTRA_* environment variables for agent processes.
// These allow user-level hooks to detect agent mode and suppress interactive features.
func (s *Spawner) orchestraEnv(taskID string) []string {
	return []string{
		"ORCHESTRA_AGENT=1",
		"ORCHESTRA_TASK_ID=" + taskID,
		"ORCHESTRA_PARENT_SLUG=" + s.projectSlug(),
		fmt.Sprintf("ORCHESTRA_DEPTH=%d", recursion.CurrentDepth()+1),
	}
}

// modelStrategy returns the effective model strategy. Priority:
// 1. Spawner.ModelStrategy field (set by conductor)
// 2. conductor:model_strategy blackboard key
// 3. StrategyAllOpus default
func (s *Spawner) modelStrategy(ctx context.Context) ModelStrategy {
	if s.ModelStrategy != "" {
		return s.ModelStrategy
	}
	if bbStrategy, _ := s.DB.GetBlackboardValue(ctx, "conductor:model_strategy"); bbStrategy != "" {
		return ParseStrategy(bbStrategy)
	}
	return StrategyAllOpus
}

// mcpConfigArgs returns --mcp-config flags if .orchestra/mcp.json exists and MCP is not disabled.
func (s *Spawner) mcpConfigArgs(ctx context.Context) []string {
	skipMCP, _ := s.DB.GetBlackboardValue(ctx, "conductor:skip_mcp")
	if skipMCP == "1" {
		return nil
	}
	mcpPath := filepath.Join(s.RepoRoot, ".orchestra", "mcp.json")
	if _, err := os.Stat(mcpPath); err != nil {
		return nil
	}
	return []string{"--mcp-config", mcpPath}
}

// setFilePermissions sets unowned files to read-only in a worktree.
// Files with an execute bit get 0555 (preserving git's 100755 mode);
// files without get 0444 (preserving git's 100644 mode).
// This prevents agents from modifying files that belong to other tasks.
// Fails open: errors in permission setting are silently ignored.
func (s *Spawner) setFilePermissions(ctx context.Context, worktreePath, taskID string) {
	locks, err := s.DB.ListFileLocksForTask(ctx, taskID)
	if err != nil {
		return // fail-open
	}
	ownedFiles := make(map[string]bool)
	for _, lock := range locks {
		ownedFiles[lock.FilePath] = true
	}
	filepath.Walk(worktreePath, func(path string, info os.FileInfo, walkErr error) error {
		if walkErr != nil || info.IsDir() {
			return nil
		}
		relPath, _ := filepath.Rel(worktreePath, path)
		if strings.HasPrefix(relPath, ".git") || strings.HasPrefix(relPath, ".claude") ||
			strings.HasPrefix(relPath, ".orchestra-hooks") {
			return nil
		}
		if !ownedFiles[relPath] {
			if info.Mode()&0o111 != 0 {
				os.Chmod(path, 0o555) // read-only + execute (preserves git's 100755)
			} else {
				os.Chmod(path, 0o444) // read-only, no execute (preserves git's 100644)
			}
		}
		return nil
	})
}

// installFileOwnershipHook installs a pre-commit hook that rejects commits
// modifying files owned by other tasks. The hook queries the SQLite DB directly.
func (s *Spawner) installFileOwnershipHook(ctx context.Context, worktreePath, taskID string) error {
	hookDir := filepath.Join(worktreePath, ".orchestra-hooks")
	os.MkdirAll(hookDir, 0o755)
	dbPath := filepath.Join(s.RepoRoot, ".orchestra", "orchestrator.db")

	hookScript := fmt.Sprintf(`#!/bin/bash
TASK_ID="%s"
DB_PATH="%s"
if ! command -v sqlite3 &>/dev/null; then exit 0; fi
VIOLATIONS=""
for file in $(git diff --cached --name-only); do
  OWNER=$(sqlite3 "$DB_PATH" "SELECT task_id FROM file_locks WHERE file_path='$file'" 2>/dev/null)
  if [ -n "$OWNER" ] && [ "$OWNER" != "$TASK_ID" ]; then
    VIOLATIONS="${VIOLATIONS}\n  $file (owned by $OWNER)"
  fi
done
if [ -n "$VIOLATIONS" ]; then
  echo "BLOCKED: File ownership violation. Task $TASK_ID modified files owned by other tasks:" >&2
  echo -e "$VIOLATIONS" >&2
  exit 1
fi
exit 0
`, taskID, dbPath)

	if err := os.WriteFile(filepath.Join(hookDir, "pre-commit"), []byte(hookScript), 0o755); err != nil {
		return err
	}

	// Write a .gitignore inside .orchestra-hooks/ so git never stages hook files,
	// even if the top-level .gitignore is missing or incomplete (G98 defense-in-depth).
	os.WriteFile(filepath.Join(hookDir, ".gitignore"), []byte("*\n"), 0o644)

	gitCfg := exec.CommandContext(ctx, "git", "-C", worktreePath, "config", "core.hooksPath", hookDir)
	_, err := gitCfg.CombinedOutput()
	return err
}

// waitForFileLocks blocks until files assigned to this task are no longer held
// by running tasks. This serializes work on overlapping file sets.
// Fails open: returns nil after timeout or on errors.
func (s *Spawner) waitForFileLocks(ctx context.Context, taskID string, timeout time.Duration) error {
	locks, err := s.DB.ListFileLocksForTask(ctx, taskID)
	if err != nil || len(locks) == 0 {
		return nil
	}

	ownedFiles := make(map[string]bool)
	for _, lock := range locks {
		ownedFiles[lock.FilePath] = true
	}

	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		allLocks, err := s.DB.ListFileLocks(ctx)
		if err != nil {
			return nil // fail-open
		}

		blocked := false
		for _, lock := range allLocks {
			if !lock.TaskID.Valid || lock.TaskID.String == taskID {
				continue
			}
			if !ownedFiles[lock.FilePath] {
				continue
			}
			// Check if the competing task is still running
			task, _ := s.DB.GetTaskByID(ctx, lock.TaskID.String)
			if task != nil && (task.Status == "running" || task.Status == "assigned") {
				s.DB.LogEvent(ctx, "file_lock_wait", "", taskID,
					fmt.Sprintf(`{"file":"%s","held_by":"%s"}`, lock.FilePath, lock.TaskID.String))
				blocked = true
				break
			}
		}

		if !blocked {
			return nil
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(30 * time.Second):
			// Poll again
		}
	}

	s.DB.LogEvent(ctx, "file_lock_timeout", "", taskID, "")
	return nil // fail-open after timeout
}

// Run executes the full spawn pipeline for a task.
func (s *Spawner) Run(ctx context.Context, opts SpawnOpts) (*SpawnResult, error) {
	taskID := opts.TaskID
	role := opts.Role

	// G139: Global rate limit cooldown — if any agent hit the session limit,
	// all agents share the same Claude Max pool, so skip spawning until reset.
	if cooldownStr, _ := s.DB.GetBlackboardValue(ctx, "conductor:rate_limit_cooldown_until"); cooldownStr != "" {
		if cooldownEpoch, err := strconv.ParseInt(cooldownStr, 10, 64); err == nil && time.Now().Unix() < cooldownEpoch {
			return nil, fmt.Errorf("global rate limit cooldown active until epoch %d (task %s)", cooldownEpoch, taskID)
		}
	}

	// 0. Pessimistic checkout: wait for file contention before creating worktree
	enforcement, _ := s.DB.GetBlackboardValue(ctx, "conductor:file_enforcement")
	if enforcement == "pessimistic" {
		s.waitForFileLocks(ctx, taskID, 5*time.Minute)
	}

	// 1. Validate task is pending + blockers done
	task, err := s.DB.GetTaskByID(ctx, taskID)
	if err != nil {
		return nil, fmt.Errorf("looking up task %s: %w", taskID, err)
	}
	if task == nil {
		return nil, fmt.Errorf("task %s not found", taskID)
	}
	if task.Status != "pending" {
		return nil, fmt.Errorf("task %s has status %q, expected pending", taskID, task.Status)
	}

	// Fall back to task's role if not provided
	if role == "" && task.Role != "" {
		role = Role(task.Role)
	}

	// 2. Dedup guard
	if task.AssignedTo.Valid {
		return nil, fmt.Errorf("task %s already assigned to %s", taskID, task.AssignedTo.String)
	}

	// 3. Check git repo exists before creating worktree
	if _, err := os.Stat(filepath.Join(s.RepoRoot, ".git")); os.IsNotExist(err) {
		return nil, fmt.Errorf("not a git repository: %s — run 'git init' first", s.RepoRoot)
	}

	// 4. Create worktree
	// Agents branch from the staging branch (conductor/<session-id>) if one exists,
	// otherwise from the current HEAD (backward compat with single-conductor mode).
	// B-281: If the task has a feature_cluster, branch from the cluster branch instead.
	worktreePath := filepath.Join(s.RepoRoot, ".worktree", taskID)
	branch := "feature/" + taskID

	// Defense-in-depth: clean up stale worktree/branch from a previous attempt.
	// Gate retry and other recovery paths should clean these up, but if they
	// didn't (e.g., crash), handle it here to prevent "branch already exists" errors.
	if _, statErr := os.Stat(worktreePath); statErr == nil {
		SalvageWorktreeChanges(ctx, worktreePath, taskID)
		rmCmd := exec.CommandContext(ctx, "git", "worktree", "remove", worktreePath, "--force")
		rmCmd.Dir = s.RepoRoot
		rmCmd.CombinedOutput() // best-effort
	}
	checkBranch := exec.CommandContext(ctx, "git", "rev-parse", "--verify", branch)
	checkBranch.Dir = s.RepoRoot
	if checkBranch.Run() == nil {
		delBranch := exec.CommandContext(ctx, "git", "branch", "-D", branch)
		delBranch.Dir = s.RepoRoot
		delBranch.CombinedOutput() // best-effort
	}

	startPoint := s.getStagingBranch(ctx)
	startPoint = s.getClusterBranchOverride(ctx, taskID, startPoint)
	var gitCmd *exec.Cmd
	if startPoint != "" {
		gitCmd = exec.CommandContext(ctx, "git", "worktree", "add", "-b", branch, worktreePath, startPoint)
	} else {
		gitCmd = exec.CommandContext(ctx, "git", "worktree", "add", worktreePath, "-b", branch)
	}
	gitCmd.Dir = s.RepoRoot
	if out, err := gitCmd.CombinedOutput(); err != nil {
		return nil, fmt.Errorf("creating worktree: %w: %s", err, string(out))
	}

	// 4b. Disable file mode tracking in worktree to prevent permission-only diffs.
	// Defense mode's setFilePermissions changes modes for access control, but those
	// changes should never be committed. core.fileMode=false prevents git from
	// staging mode-only changes.
	fmCmd := exec.CommandContext(ctx, "git", "-C", worktreePath, "config", "core.fileMode", "false")
	fmCmd.CombinedOutput() // best-effort

	// 5. Protect .claude/settings.json from agent modification.
	// Agents run with --permission-mode bypassPermissions so they don't need
	// a settings.json. But Claude Code may create/modify one during the session.
	// If that gets staged by `git add -A` and merged, it overwrites the user's
	// actual settings. Write a worktree-local .gitignore to prevent this.
	claudeDir := filepath.Join(worktreePath, ".claude")
	os.MkdirAll(claudeDir, 0o755)
	os.WriteFile(filepath.Join(claudeDir, ".gitignore"), []byte("settings.json\nsettings.local.json\n"), 0o644)

	// 5b. File ownership enforcement (defense-in-depth or pessimistic)
	if enforcement == "defense" || enforcement == "pessimistic" {
		s.setFilePermissions(ctx, worktreePath, taskID)
		if err := s.installFileOwnershipHook(ctx, worktreePath, taskID); err != nil {
			s.DB.LogEvent(ctx, "hook_install_warning", "", taskID, err.Error())
		}
	}

	// 6. Parse agent definition (needed for model resolution)
	agentDef, err := ParseAgentDef(s.RepoRoot, role)
	if err != nil {
		// Non-fatal: use spec only
		agentDef = &AgentDef{SystemPrompt: ""}
	}

	// 7. Resolve model
	model := opts.Model
	if model == "" {
		// Check blackboard for fallback model
		if bbModel, _ := s.DB.GetBlackboardValue(ctx, "fallback_model:"+taskID); bbModel != "" {
			model = bbModel
		} else {
			model = ResolveModel(role, s.modelStrategy(ctx), agentDef.Model)
		}
	}

	// 8. Register agent + assign task
	prefix := "a" // default prefix
	if len(role) > 0 {
		prefix = string(role)[0:1]
	}
	agentID := db.GenID(prefix)
	timeout := opts.Timeout
	if timeout == 0 {
		timeout = DefaultTimeout(role)
	}

	err = s.DB.RegisterAgent(ctx, db.Agent{
		ID:     agentID,
		Role:   string(role),
		Status: "idle",
	})
	if err != nil {
		return nil, fmt.Errorf("registering agent: %w", err)
	}

	claimed, err := s.DB.ClaimTask(ctx, taskID, agentID, worktreePath, branch)
	if err != nil {
		return nil, fmt.Errorf("claiming task: %w", err)
	}
	if !claimed {
		return nil, fmt.Errorf("task %s was already claimed by another agent", taskID)
	}

	// 9. Generate spec
	spec, err := s.generateSpec(ctx, taskID)
	if err != nil {
		return nil, fmt.Errorf("generating spec: %w", err)
	}

	// Store spec in blackboard for downstream consumers (e.g. spec-anchored review)
	s.DB.SetBlackboard(ctx, "spec:"+taskID, spec, "spawner")

	// 10. Build full prompt
	// CRITICAL: claude -p treats "---" anywhere in the prompt as a CLI option
	// when it appears on its own line. Replace all standalone "---" lines with
	// a markdown horizontal rule "***" which renders the same but doesn't
	// trigger the CLI parser.
	cleanSpec := strings.ReplaceAll(spec, "\n---\n", "\n***\n")
	if strings.HasPrefix(cleanSpec, "---\n") {
		cleanSpec = "***\n" + cleanSpec[4:]
	}
	prompt := cleanSpec
	if agentDef.SystemPrompt != "" {
		prompt = agentDef.SystemPrompt + "\n\n***\n\n" + cleanSpec
	}

	// 10b. Context budget check
	estimated, overBudget, budgetDetails := EstimateContextBudget(spec, agentDef.SystemPrompt)
	s.DB.LogEvent(ctx, "context_budget_check", agentID, taskID,
		fmt.Sprintf(`{"estimated_tokens":%d,"over_budget":%v,"details":"%s"}`, estimated, overBudget, budgetDetails))
	if overBudget {
		s.DB.LogEvent(ctx, "context_budget_exceeded", agentID, taskID, budgetDetails)
	}

	// 10c. Iterative role dispatch — deep-researcher, visual-reviewer, functional-tester
	// These roles use multi-round loops with convergence checking instead of a single claude -p call.
	if IsIterativeRole(role) {
		loopConfig := DefaultLoopConfig(role)
		s.DB.LogEvent(ctx, "iterative_loop_start", agentID, taskID,
			fmt.Sprintf(`{"role":"%s","max_rounds":%d,"metric":"%s","threshold":%.2f}`,
				role, loopConfig.MaxRounds, loopConfig.ConvergenceMetric, loopConfig.Threshold))

		s.DB.UpdateAgentStatus(ctx, agentID, "working")
		s.DB.StartTask(ctx, taskID)

		// Build role-specific iterative prompts
		var planPrompt, roundPrompt string
		if role == RoleDeepResearcher || role == RoleResearcher {
			planPrompt = `PHASE 1 — PLAN: Read the task description and any existing files in your worktree.
Identify specific knowledge gaps (max 10). Each gap must be a concrete, answerable question — not open-ended.

List the gaps as a numbered list. Do NOT research yet. Only identify what's missing.`

			roundPrompt = `PHASE 2 — EXECUTE: You have a research plan from the previous round.

{{.PriorContext}}

For each gap in the plan:
1. Research the answer using WebSearch and WebFetch
2. Edit the research document(s) in your worktree to add findings with sources

If filling a gap revealed a critical NEW gap (something that would make the document misleading without it), research that too (max 2 additions).

When all gaps are filled, output the word SATURATED.
If gaps remain, describe what is still missing.`
		}

		// Pass MCP config so iterative agents have WebSearch/WebFetch access
		mcpArgs := s.mcpConfigArgs(ctx)
		var mcpPath string
		for i, a := range mcpArgs {
			if a == "--mcp-config" && i+1 < len(mcpArgs) {
				mcpPath = mcpArgs[i+1]
			}
		}

		loopResult, err := RunIterativeLoop(ctx, s.DB, IterativeLoopOpts{
			TaskID:        taskID,
			AgentID:       agentID,
			Role:          role,
			Config:        loopConfig,
			Model:         model,
			WorkDir:       worktreePath,
			MCPConfigPath: mcpPath,
			BasePrompt:    prompt,
			PlanPrompt:    planPrompt,
			RoundPrompt:   roundPrompt,
		})
		if err != nil {
			s.DB.UpdateAgentStatus(ctx, agentID, "dead")
			return nil, fmt.Errorf("iterative loop: %w", err)
		}

		// Store loop result in blackboard for observability
		if resultJSON, err := json.Marshal(loopResult); err == nil {
			s.DB.SetBlackboard(ctx, "iterative_result:"+taskID, string(resultJSON), "spawner")
		}

		// For converged iterative loops: mark task done directly.
		// The monitor may race with handleAgentExit (PID=0 looks dead),
		// so we set the terminal status here before the monitor can interfere.
		if loopResult.Converged {
			s.DB.CompleteTask(ctx, taskID, "completed")
			s.DB.UpdateAgentStatus(ctx, agentID, "done")
			s.DB.LogEvent(ctx, "iterative_converged", agentID, taskID,
				fmt.Sprintf(`{"rounds":%d}`, loopResult.FinalRound))
		} else {
			s.DB.FailTask(ctx, taskID, fmt.Sprintf("iterative loop exhausted after %d rounds", loopResult.FinalRound))
			s.DB.UpdateAgentStatus(ctx, agentID, "dead")
		}

		// Trigger exit handling (validation, merge queue, etc.)
		s.handleAgentExit(ctx, taskID, agentID)

		return &SpawnResult{
			AgentID:  agentID,
			PID:      0, // no persistent PID for iterative loops
			Worktree: worktreePath,
			Model:    model,
		}, nil
	}

	// 10b. B-029: Orchestrator role spawns orchestra go instead of claude -p.
	// This enables loops-as-sub-orchestrators — a conductor can decompose work
	// that includes sub-orchestration tasks handled by a child conductor.
	if Role(role) == RoleOrchestrator {
		logFile := filepath.Join(s.LogsDir, taskID+".jsonl")
		orchBin := filepath.Join(s.RepoRoot, "bin", "orchestra")
		if _, err := os.Stat(orchBin); os.IsNotExist(err) {
			orchBin = "orchestra"
		}
		orchArgs := []string{"go",
			"--goal", prompt,
			"--foreground",
			"--reconcile",
		}
		cmd := exec.CommandContext(ctx, orchBin, orchArgs...)
		cmd.Dir = worktreePath
		cmd.Env = append(os.Environ(), s.orchestraEnv(taskID)...)
		logFd, _ := os.Create(logFile)
		cmd.Stdout = logFd
		cmd.Stderr = logFd
		go func() {
			defer logFd.Close()
			if err := cmd.Run(); err != nil {
				slog.Warn("orchestrator task failed", "task", taskID, "error", err)
			}
			s.handleAgentExit(context.Background(), taskID, agentID)
		}()
		return &SpawnResult{
			AgentID:  agentID,
			PID:      cmd.Process.Pid,
			Worktree: worktreePath,
			Model:    model,
		}, nil
	}

	// 11. Check local runner routing before spawning
	useLocalRunner := false
	if s.LocalRunner != nil && s.LocalRunner.Enabled {
		// Count files and acceptance criteria from the spec to decide routing
		fileCount := strings.Count(spec, "| `") // files in ownership table
		acCount := strings.Count(spec, "- [ ]") // acceptance criteria checkboxes
		useLocalRunner = s.LocalRunner.ShouldRouteLocal(role, fileCount, acCount)
		if useLocalRunner {
			s.DB.LogEvent(ctx, "local_runner_routed", agentID, taskID,
				fmt.Sprintf(`{"runner":"%s","model":"%s","files":%d,"ac":%d}`,
					s.LocalRunner.Command, s.LocalRunner.Model, fileCount, acCount))
		}
	}

	logFile := filepath.Join(s.LogsDir, taskID+".jsonl")
	stderrFile := filepath.Join(s.LogsDir, taskID+".stderr")

	var args []string
	if useLocalRunner {
		args = s.LocalRunner.BuildArgs(prompt)
	} else {
		args = []string{
			"-p", prompt,
			"--output-format", "stream-json",
			"--verbose",
			"--model", model,
			"--permission-mode", "bypassPermissions",
		}
		args = append(args, s.mcpConfigArgs(ctx)...)
	}

	// 11b. Sandbox mode: wrap execution through sandboxed container
	if s.Sandbox != nil && s.Sandbox.Enabled {
		s.DB.StartTask(ctx, taskID)
		s.DB.UpdateAgentStatus(ctx, agentID, "working")

		now := time.Now()
		s.DB.SetBlackboard(ctx, "timeout:"+taskID, strconv.Itoa(int(timeout.Seconds())), "spawner")
		s.DB.SetBlackboard(ctx, "spawn_time:"+taskID, strconv.FormatInt(now.Unix(), 10), "spawner")
		s.DB.LogEvent(ctx, "agent_spawned_sandbox", agentID, taskID,
			fmt.Sprintf(`{"model":"%s","sandbox":true}`, model))

		// Build full command: claude <args>
		sandboxCmd := append([]string{s.spawnCmd()}, args...)

		go func() {
			slog.Info("sandbox: starting Wrap", "task", taskID, "worktree", worktreePath)
			slog.Info("sandbox: command", "cmd", fmt.Sprint(sandboxCmd))
			out, wrapErr := s.Sandbox.Wrap(ctx, taskID, worktreePath, sandboxCmd)
			if wrapErr != nil {
				slog.Error("sandbox wrap error", "task", taskID, "err", wrapErr)
				// Write error details to log file for debugging
				errLog := fmt.Sprintf("SANDBOX ERROR: %v\n\nCommand: %v\n\nOutput (%d bytes):\n%s",
					wrapErr, sandboxCmd, len(out), string(out))
				os.WriteFile(logFile, []byte(errLog), 0o644)
				s.DB.LogEvent(ctx, "sandbox_error", agentID, taskID,
					fmt.Sprintf(`{"error":"%s","output_bytes":%d}`,
						strings.ReplaceAll(wrapErr.Error(), `"`, `\"`), len(out)))
			} else {
				slog.Info("sandbox: Wrap completed successfully", "task", taskID, "output_bytes", len(out))
			}
			// Write output to log file for consistency with normal path
			if len(out) > 0 && wrapErr == nil {
				os.WriteFile(logFile, out, 0o644)
			}
			s.handleAgentExit(context.Background(), taskID, agentID)
		}()

		return &SpawnResult{
			AgentID:  agentID,
			PID:      0, // no direct PID for sandboxed containers
			Worktree: worktreePath,
			Model:    model,
		}, nil
	}

	// Route to local runner or claude
	var runnerCmd string
	var localRelease func()
	if useLocalRunner {
		runnerCmd = s.LocalRunner.Command
		localRelease = s.LocalRunner.Acquire()
		slog.Info("local runner: acquired slot", "task", taskID, "runner", runnerCmd)
	}
	cmd := s.buildExecCmd(ctx, taskID, args, worktreePath, runnerCmd)

	// Set up log file output
	logFd, err := os.Create(logFile)
	if err != nil {
		return nil, fmt.Errorf("creating log file: %w", err)
	}

	stderrFd, err := os.Create(stderrFile)
	if err != nil {
		logFd.Close()
		return nil, fmt.Errorf("creating stderr file: %w", err)
	}

	cmd.Stdout = logFd
	cmd.Stderr = stderrFd

	if err := cmd.Start(); err != nil {
		logFd.Close()
		stderrFd.Close()
		return nil, fmt.Errorf("starting agent process: %w", err)
	}

	pid := cmd.Process.Pid

	// 12. Write PID file + store metadata in blackboard
	WritePID(s.PidsDir, taskID, pid)
	s.DB.UpdateAgentPID(ctx, agentID, pid)
	s.DB.StartTask(ctx, taskID)
	s.DB.UpdateAgentStatus(ctx, agentID, "working")

	now := time.Now()
	s.DB.SetBlackboard(ctx, "timeout:"+taskID, strconv.Itoa(int(timeout.Seconds())), "spawner")
	s.DB.SetBlackboard(ctx, "spawn_time:"+taskID, strconv.FormatInt(now.Unix(), 10), "spawner")
	s.DB.LogEvent(ctx, "agent_spawned", agentID, taskID,
		fmt.Sprintf(`{"model":"%s","pid":%d}`, model, pid))

	// 13. Background goroutine: capture session_id from first JSONL line, wait for exit
	go func() {
		defer logFd.Close()
		defer stderrFd.Close()
		if localRelease != nil {
			defer localRelease()
		}
		s.captureSessionID(taskID, logFile)
		cmd.Wait()
		s.handleAgentExit(context.Background(), taskID, agentID)
	}()

	return &SpawnResult{
		AgentID:  agentID,
		PID:      pid,
		Worktree: worktreePath,
		Model:    model,
	}, nil
}

// Launch re-launches an already assigned task (no new worktree/agent).
func (s *Spawner) Launch(ctx context.Context, taskID string) (*SpawnResult, error) {
	task, err := s.DB.GetTaskByID(ctx, taskID)
	if err != nil || task == nil {
		return nil, fmt.Errorf("task %s not found", taskID)
	}

	agent, err := s.DB.GetAgentByTask(ctx, taskID)
	if err != nil || agent == nil {
		return nil, fmt.Errorf("no agent assigned to task %s", taskID)
	}

	role := Role(agent.Role)
	worktree := ""
	if task.Worktree.Valid {
		worktree = task.Worktree.String
	}

	spec, err := s.generateSpec(ctx, taskID)
	if err != nil {
		return nil, fmt.Errorf("generating spec: %w", err)
	}

	agentDef, _ := ParseAgentDef(s.RepoRoot, role)
	agentDefModel := ""
	if agentDef != nil {
		agentDefModel = agentDef.Model
	}
	model := ResolveModel(role, s.modelStrategy(ctx), agentDefModel)

	// Context budget check
	systemPrompt := ""
	if agentDef != nil {
		systemPrompt = agentDef.SystemPrompt
	}
	estimated, overBudget, budgetDetails := EstimateContextBudget(spec, systemPrompt)
	s.DB.LogEvent(ctx, "context_budget_check", agent.ID, taskID,
		fmt.Sprintf(`{"estimated_tokens":%d,"over_budget":%v,"details":"%s"}`, estimated, overBudget, budgetDetails))
	if overBudget {
		s.DB.LogEvent(ctx, "context_budget_exceeded", agent.ID, taskID, budgetDetails)
	}

	logFile := filepath.Join(s.LogsDir, taskID+".jsonl")
	stderrFile := filepath.Join(s.LogsDir, taskID+".stderr")

	// Clean --- from spec to prevent claude -p CLI parsing issues
	cleanSpec := strings.ReplaceAll(spec, "\n---\n", "\n***\n")
	if strings.HasPrefix(cleanSpec, "---\n") {
		cleanSpec = "***\n" + cleanSpec[4:]
	}
	args := []string{"-p", cleanSpec, "--output-format", "stream-json", "--verbose", "--model", model, "--permission-mode", "bypassPermissions"}
	args = append(args, s.mcpConfigArgs(ctx)...)

	cmd := s.buildExecCmd(ctx, taskID, args, worktree)

	logFd, _ := os.Create(logFile)
	stderrFd, _ := os.Create(stderrFile)
	cmd.Stdout = logFd
	cmd.Stderr = stderrFd

	if err := cmd.Start(); err != nil {
		logFd.Close()
		stderrFd.Close()
		return nil, fmt.Errorf("starting agent process: %w", err)
	}

	pid := cmd.Process.Pid
	WritePID(s.PidsDir, taskID, pid)
	s.DB.UpdateAgentPID(ctx, agent.ID, pid)
	s.DB.StartTask(ctx, taskID)
	s.DB.UpdateAgentStatus(ctx, agent.ID, "working")
	s.DB.LogEvent(ctx, "agent_launched", agent.ID, taskID, fmt.Sprintf(`{"pid":%d}`, pid))

	go func() {
		defer logFd.Close()
		defer stderrFd.Close()
		cmd.Wait()
		s.handleAgentExit(context.Background(), taskID, agent.ID)
	}()

	return &SpawnResult{AgentID: agent.ID, PID: pid, Worktree: worktree, Model: model}, nil
}

// Resume continues a task's session using `claude --resume SESSION_ID`.
func (s *Spawner) Resume(ctx context.Context, taskID string) (*SpawnResult, error) {
	sessionID, _ := s.DB.GetBlackboardValue(ctx, "session_id:"+taskID)
	if sessionID == "" {
		return nil, fmt.Errorf("no session_id for task %s, cannot resume", taskID)
	}

	task, err := s.DB.GetTaskByID(ctx, taskID)
	if err != nil || task == nil {
		return nil, fmt.Errorf("task %s not found", taskID)
	}

	agent, err := s.DB.GetAgentByTask(ctx, taskID)
	if err != nil || agent == nil {
		return nil, fmt.Errorf("no agent for task %s", taskID)
	}

	worktree := ""
	if task.Worktree.Valid {
		worktree = task.Worktree.String
	}
	agentDef, _ := ParseAgentDef(s.RepoRoot, Role(agent.Role))
	agentDefModel := ""
	if agentDef != nil {
		agentDefModel = agentDef.Model
	}
	model := ResolveModel(Role(agent.Role), s.modelStrategy(ctx), agentDefModel)

	logFile := filepath.Join(s.LogsDir, taskID+".jsonl")
	stderrFile := filepath.Join(s.LogsDir, taskID+".stderr")

	args := []string{
		"--resume", sessionID,
		"-p", "Continue from where you left off. Complete the remaining work.",
		"--output-format", "stream-json",
		"--verbose",
		"--model", model,
		"--permission-mode", "bypassPermissions",
	}
	args = append(args, s.mcpConfigArgs(ctx)...)

	cmd := s.buildExecCmd(ctx, taskID, args, worktree)

	logFd, _ := os.OpenFile(logFile, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	stderrFd, _ := os.OpenFile(stderrFile, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	cmd.Stdout = logFd
	cmd.Stderr = stderrFd

	if err := cmd.Start(); err != nil {
		logFd.Close()
		stderrFd.Close()
		return nil, fmt.Errorf("starting resume: %w", err)
	}

	pid := cmd.Process.Pid
	WritePID(s.PidsDir, taskID, pid)
	s.DB.UpdateAgentPID(ctx, agent.ID, pid)
	s.DB.UpdateAgentStatus(ctx, agent.ID, "working")
	s.DB.SetBlackboard(ctx, "resume_attempted:"+taskID, "1", "spawner")
	s.DB.LogEvent(ctx, "agent_resumed", agent.ID, taskID, fmt.Sprintf(`{"session":"%s","pid":%d}`, sessionID, pid))

	go func() {
		defer logFd.Close()
		defer stderrFd.Close()
		cmd.Wait()
		s.handleAgentExit(context.Background(), taskID, agent.ID)
	}()

	return &SpawnResult{AgentID: agent.ID, PID: pid, Worktree: worktree, Model: model}, nil
}

// Respawn kills the current process (if any) and re-runs with circuit breaker + model fallback.
func (s *Spawner) Respawn(ctx context.Context, taskID string) (*SpawnResult, error) {
	// Kill existing process
	KillTaskProcess(ctx, s.DB, taskID, s.PidsDir, true)

	// Circuit breaker check
	retryStr, _ := s.DB.GetBlackboardValue(ctx, "retry:"+taskID)
	retryCount, _ := strconv.Atoi(retryStr)
	if retryCount >= 3 {
		return nil, fmt.Errorf("task %s exceeded max retries (3)", taskID)
	}

	// Model fallback
	failureType, _ := s.DB.GetBlackboardValue(ctx, "failure_type:"+taskID)
	modelFailStr, _ := s.DB.GetBlackboardValue(ctx, "model_failures:"+taskID)
	modelFails, _ := strconv.Atoi(modelFailStr)

	task, err := s.DB.GetTaskByID(ctx, taskID)
	if err != nil || task == nil {
		return nil, fmt.Errorf("task %s not found", taskID)
	}

	role := Role(task.Role)
	agentDef, _ := ParseAgentDef(s.RepoRoot, role)
	agentDefModel := ""
	if agentDef != nil {
		agentDefModel = agentDef.Model
	}
	model := ResolveModel(role, s.modelStrategy(ctx), agentDefModel)

	// After 2+ model-specific failures, try fallback
	if modelFails >= 2 {
		if fb, _ := s.DB.GetBlackboardValue(ctx, "fallback_model:"+taskID); fb != "" {
			next := NextFallbackModel(fb)
			if next != "" {
				model = next
				s.DB.SetBlackboard(ctx, "fallback_model:"+taskID, model, "spawner")
			}
		} else {
			fb := NextFallbackModel(model)
			if fb != "" {
				model = fb
				s.DB.SetBlackboard(ctx, "fallback_model:"+taskID, model, "spawner")
			}
		}
	}

	// Infrastructure failures don't increment retry counter
	isInfra := failureType == "rate_limit" || failureType == "session_limit" || failureType == "context_exhausted"
	if !isInfra {
		s.DB.SetBlackboard(ctx, "retry:"+taskID, strconv.Itoa(retryCount+1), "spawner")
	}

	// Reset task to pending
	if err := s.DB.ResetTask(ctx, taskID); err != nil {
		return nil, fmt.Errorf("resetting task: %w", err)
	}

	// Salvage uncommitted work before destroying the worktree.
	worktreePath := filepath.Join(s.RepoRoot, ".worktree", taskID)
	branch := "feature/" + taskID
	SalvageWorktreeChanges(ctx, worktreePath, taskID)

	// Clean up old worktree and branch before re-spawning
	rmCmd := exec.CommandContext(ctx, "git", "worktree", "remove", worktreePath, "--force")
	rmCmd.Dir = s.RepoRoot
	rmCmd.CombinedOutput() // best-effort
	delBranch := exec.CommandContext(ctx, "git", "branch", "-D", branch)
	delBranch.Dir = s.RepoRoot
	delBranch.CombinedOutput() // best-effort

	s.DB.LogEvent(ctx, "agent_respawning", "", taskID,
		fmt.Sprintf(`{"retry":%d,"model":"%s","failure_type":"%s"}`, retryCount+1, model, failureType))

	return s.Run(ctx, SpawnOpts{
		TaskID: taskID,
		Role:   role,
		Model:  model,
	})
}

// Refine runs the post-failure refinement loop for a task that failed validation.
// It spawns a lightweight reviewer to critique the failed attempt, stores the
// critique in the blackboard, increments the refinement counter, and respawns
// the original agent with enhanced context.
func (s *Spawner) Refine(ctx context.Context, taskID string) (*SpawnResult, error) {
	// 0. Acquire concurrency lock to prevent spawner + monitor from both triggering refinement
	lockKey := "refinement_lock:" + taskID
	existing, _ := s.DB.GetBlackboardValue(ctx, lockKey)
	if existing != "" {
		return nil, fmt.Errorf("refinement already in progress for %s", taskID)
	}
	s.DB.SetBlackboard(ctx, lockKey, "1", "spawner")
	defer s.DB.DeleteBlackboard(ctx, lockKey)

	// 1. Check refinement eligibility
	refinementStr, _ := s.DB.GetBlackboardValue(ctx, "refinement:"+taskID)
	refinementCount, _ := strconv.Atoi(refinementStr)
	if refinementCount >= MaxRefinements {
		return nil, fmt.Errorf("task %s exceeded max refinements (%d)", taskID, MaxRefinements)
	}

	// 2. Read the failed task's JSONL log for failure context
	logFile := filepath.Join(s.LogsDir, taskID+".jsonl")
	logTail := readFileTail(logFile, 50)
	if logTail == "" {
		logTail = "(no log output available)"
	}
	if len(logTail) > 3000 {
		logTail = logTail[len(logTail)-3000:]
	}

	// Get the failure reason
	failureReason, _ := s.DB.GetBlackboardValue(ctx, "last_failure:"+taskID)
	if failureReason == "" {
		failureReason = "unknown"
	}

	// Get the task description for context
	task, err := s.DB.GetTaskByID(ctx, taskID)
	if err != nil || task == nil {
		return nil, fmt.Errorf("task %s not found", taskID)
	}
	taskDesc := task.Description.String
	if len(taskDesc) > 1000 {
		taskDesc = taskDesc[:1000] + "..."
	}

	// 3. Build critique prompt
	critiquePrompt := fmt.Sprintf(`You are a code review specialist. An agent attempted the following task and failed validation.

## Task Description (summary)
%s

## Validation Failure Reason
%s

## Agent Log Output (tail)
%s

## Your Job
Analyze why the agent failed and provide a concise, actionable critique (max 500 words).
Focus on:
1. What the agent did wrong or failed to do
2. Specific steps the next agent should take to succeed
3. Common pitfalls to avoid for this type of failure

Output ONLY the critique text — no markdown headers, no preamble.`, taskDesc, failureReason, logTail)

	// 3b. Append merge feedback if present (test output or review critique)
	mergeFeedback, _ := s.DB.GetBlackboardValue(ctx, "merge_feedback:"+taskID)
	if mergeFeedback != "" {
		if len(mergeFeedback) > 3000 {
			mergeFeedback = mergeFeedback[len(mergeFeedback)-3000:]
		}
		if strings.Contains(failureReason, "test_cmd_failed") {
			critiquePrompt += fmt.Sprintf("\n\n## Test Output (merge verification)\n```\n%s\n```\n", mergeFeedback)
		} else if strings.Contains(failureReason, "review_rejected") {
			critiquePrompt += fmt.Sprintf("\n\n## Review Feedback\n%s\n", mergeFeedback)
		}
	}

	// 4. Spawn a lightweight reviewer with a 2-minute timeout
	reviewerTimeout := 2 * time.Minute
	args := []string{
		"-p", critiquePrompt,
		"--output-format", "stream-json",
		"--verbose",
		"--model", ModelSonnet,
		"--max-turns", "1",
		"--permission-mode", "dontAsk",
		"--allowedTools", "Read", "Glob", "Grep",
	}

	critiqueLogFile := filepath.Join(s.LogsDir, taskID+".critique.jsonl")

	cmd := s.buildExecCmd(ctx, taskID+"-critique", args, s.RepoRoot)

	logFd, err := os.Create(critiqueLogFile)
	if err != nil {
		return nil, fmt.Errorf("creating critique log: %w", err)
	}
	defer logFd.Close()

	cmd.Stdout = logFd
	cmd.Stderr = logFd

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("starting reviewer: %w", err)
	}

	// 5. Wait with timeout
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()

	select {
	case <-done:
		// Reviewer completed
	case <-time.After(reviewerTimeout):
		cmd.Process.Kill()
		s.DB.LogEvent(ctx, "refinement_reviewer_timeout", "", taskID, "")
	}
	logFd.Close()

	// 6. Extract critique from reviewer's result
	critique := extractCritiqueFromLog(critiqueLogFile)
	if critique == "" {
		critique = fmt.Sprintf("Reviewer did not produce output. Original failure: %s. Ensure you address this failure reason directly.", failureReason)
	}
	if len(critique) > 2000 {
		critique = critique[:2000]
	}

	// 7. Store critique and increment refinement counter
	s.DB.SetBlackboard(ctx, "critique:"+taskID, critique, "spawner")
	s.DB.SetBlackboard(ctx, "refinement:"+taskID, strconv.Itoa(refinementCount+1), "spawner")
	s.DB.LogEvent(ctx, "refinement_critique_stored", "", taskID,
		fmt.Sprintf(`{"refinement":%d,"critique_len":%d}`, refinementCount+1, len(critique)))

	// 8. Respawn the original agent with enhanced context (preserving worktree)
	return s.RespawnForRefinement(ctx, taskID)
}

// RespawnForRefinement re-launches a task while preserving the worktree.
// Unlike Respawn, this does not increment retry count or destroy the worktree.
// The refinement counter (refinement:{task-id}) is managed by Refine/TriggerRefinement.
func (s *Spawner) RespawnForRefinement(ctx context.Context, taskID string) (*SpawnResult, error) {
	// Kill existing process (if still running)
	KillTaskProcess(ctx, s.DB, taskID, s.PidsDir, true)

	// Check retry circuit breaker (existing retries, not refinement count)
	retryStr, _ := s.DB.GetBlackboardValue(ctx, "retry:"+taskID)
	retryCount, _ := strconv.Atoi(retryStr)
	if retryCount >= 3 {
		return nil, fmt.Errorf("task %s exceeded max retries (3)", taskID)
	}

	// Reset task to pending but PRESERVE worktree, branch, and agent assignment.
	// Unlike ResetTask which clears everything, we only reset status so that
	// Launch can re-use the existing worktree and agent.
	if err := s.DB.SoftResetTask(ctx, taskID); err != nil {
		return nil, fmt.Errorf("soft-resetting task: %w", err)
	}

	// G97: Reverse cascade failures that occurred during the refinement window.
	if n, err := s.DB.ResetCascadeFailedDependents(ctx, taskID); err != nil {
		slog.Warn("failed to reset cascade-failed dependents", "task", taskID, "err", err)
	} else if n > 0 {
		slog.Info("reversed cascade-failed dependents", "task", taskID, "count", n)
		s.DB.LogEvent(ctx, "cascade_reversal", "", taskID, fmt.Sprintf(`{"reversed":%d}`, n))
	}

	// V3 gate triage: "heal" and "refine" preserve the worktree so the agent
	// can iterate on its existing work. Only "redo" (or no gate triage) resets
	// to the fork point.
	gateTriage, _ := s.DB.GetBlackboardValue(ctx, "gate_triage:"+taskID)
	skipReset := gateTriage == "heal" || gateTriage == "refine"

	worktreePath := filepath.Join(s.RepoRoot, ".worktree", taskID)

	if skipReset {
		// Preserve worktree — agent will fix in place with gate error context.
		slog.Info("gate triage preserving worktree", "task", taskID, "triage", gateTriage)
		SalvageWorktreeChanges(ctx, worktreePath, taskID)
	} else {
		// Salvage any uncommitted work, then revert all agent commits so the
		// second attempt starts from a clean fork point. Without this, bad commits
		// from the first attempt contaminate the branch and merge brings them back.
		SalvageWorktreeChanges(ctx, worktreePath, taskID)

		// Reset worktree branch to fork point (merge-base with base branch).
		baseBranch, _ := s.DB.GetBlackboardValue(ctx, "base_branch")
		if baseBranch == "" {
			baseBranch = DetectBaseBranch(s.RepoRoot)
		}
		mergeBaseCmd := exec.CommandContext(ctx, "git", "merge-base", "HEAD", baseBranch)
		mergeBaseCmd.Dir = worktreePath
		if mergeBaseOut, err := mergeBaseCmd.Output(); err == nil {
			forkPoint := strings.TrimSpace(string(mergeBaseOut))
			resetCmd := exec.CommandContext(ctx, "git", "reset", "--hard", forkPoint)
			resetCmd.Dir = worktreePath
			if resetOut, resetErr := resetCmd.CombinedOutput(); resetErr != nil {
				slog.Warn("failed to reset worktree to fork point", "err", resetErr, "output", string(resetOut))
			} else {
				slog.Info("reset worktree to fork point", "task", taskID, "fork_point", forkPoint[:8])
			}
		}
	}

	s.DB.LogEvent(ctx, "agent_respawning_refinement", "", taskID,
		fmt.Sprintf(`{"retry":%d,"gate_triage":"%s"}`, retryCount, gateTriage))

	// Re-launch in existing worktree (not a full Run which creates a new worktree)
	result, err := s.Launch(ctx, taskID)
	if err != nil {
		// Launch failed — clear stale assignment so monitor can retry on next cycle.
		// SoftResetTask preserves assigned_to; if Launch then fails, the task stays
		// pending with a dead agent reference, blocking auto-spawn indefinitely.
		_ = s.DB.ResetTask(ctx, taskID)
		return nil, err
	}
	return result, nil
}

// extractCritiqueFromLog reads a JSONL log file and extracts text content
// from assistant messages or the result. Used to get the reviewer's critique.
func extractCritiqueFromLog(logFile string) string {
	data, err := os.ReadFile(logFile)
	if err != nil {
		return ""
	}

	var parts []string
	lines := strings.Split(string(data), "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		var msg map[string]interface{}
		if err := json.Unmarshal([]byte(line), &msg); err != nil {
			continue
		}

		msgType, _ := msg["type"].(string)

		// Extract from result
		if msgType == "result" {
			if result, ok := msg["result"].(string); ok && result != "" {
				return result
			}
		}

		// Extract from assistant messages.
		// stream-json format: {"type":"assistant","message":{"type":"text","text":"..."}}
		// or with content blocks: {"type":"assistant","message":{"content":[{"type":"text","text":"..."}]}}
		if msgType == "assistant" {
			if messageObj, ok := msg["message"].(map[string]interface{}); ok {
				// Direct text field (common stream-json format)
				if text, ok := messageObj["text"].(string); ok && text != "" {
					parts = append(parts, text)
				}
				// Content blocks array
				if contentArr, ok := messageObj["content"].([]interface{}); ok {
					for _, block := range contentArr {
						if blockMap, ok := block.(map[string]interface{}); ok {
							if text, ok := blockMap["text"].(string); ok && text != "" {
								parts = append(parts, text)
							}
						}
					}
				}
			}
		}
	}

	return strings.Join(parts, "\n")
}

// Batch spawns unblocked pending tasks up to maxConcurrent with stagger delay.
func (s *Spawner) Batch(ctx context.Context, maxConcurrent int, stagger time.Duration, sessionTaskIDs []string) ([]SpawnResult, error) {
	// Count active tasks
	active, err := s.DB.CountTasksByStatuses(ctx, []string{"assigned", "running"}, sessionTaskIDs)
	if err != nil {
		return nil, fmt.Errorf("counting active tasks: %w", err)
	}

	slots := maxConcurrent - active
	if slots <= 0 {
		return nil, nil
	}

	tasks, err := s.DB.ListUnblockedPendingTasks(ctx, sessionTaskIDs, slots)
	if err != nil {
		return nil, fmt.Errorf("listing unblocked tasks: %w", err)
	}

	// Also get lenient-unblocked tasks (annotated by monitor phase 1.5)
	remainingSlots := slots - len(tasks)
	if remainingSlots > 0 {
		lenientTasks, lErr := s.DB.ListLenientPendingTasks(ctx, sessionTaskIDs, remainingSlots)
		if lErr == nil && len(lenientTasks) > 0 {
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

	var results []SpawnResult
	for i, t := range tasks {
		if i > 0 && stagger > 0 {
			time.Sleep(stagger)
		}

		result, err := s.Run(ctx, SpawnOpts{
			TaskID: t.ID,
			Role:   Role(t.Role),
		})
		if err != nil {
			s.DB.LogEvent(ctx, "batch_spawn_error", "", t.ID, err.Error())
			continue
		}
		results = append(results, *result)
	}

	return results, nil
}

// generateSpec generates a task specification using the Go-native GenerateSpec.
func (s *Spawner) generateSpec(ctx context.Context, taskID string) (string, error) {
	return GenerateSpec(ctx, SpecGenOpts{
		DB:       s.DB,
		RepoRoot: s.RepoRoot,
		TaskID:   taskID,
	})
}

// SalvageWorktreeChanges commits any uncommitted work in a worktree before it gets destroyed.
// This preserves agent work product (research docs, code) that would otherwise be lost on respawn.
// Sets core.fileMode=false before staging to prevent file-mode-only changes (e.g., 100755→100644
// on .sh files) from being included in salvage commits.
func SalvageWorktreeChanges(ctx context.Context, worktreePath, taskID string) {
	if _, err := os.Stat(worktreePath); os.IsNotExist(err) {
		return
	}

	// Check for uncommitted changes (staged + unstaged + untracked).
	statusCmd := exec.CommandContext(ctx, "git", "status", "--porcelain")
	statusCmd.Dir = worktreePath
	out, err := statusCmd.Output()
	if err != nil || len(out) == 0 {
		return // no changes or git error
	}

	// Save the current core.fileMode value so we can restore it after the commit.
	getCmd := exec.CommandContext(ctx, "git", "config", "--local", "core.fileMode")
	getCmd.Dir = worktreePath
	prevOut, prevErr := getCmd.Output()
	hadPrevValue := prevErr == nil
	prevValue := strings.TrimSpace(string(prevOut))

	// Set core.fileMode=false to ignore permission-only changes during salvage.
	setCmd := exec.CommandContext(ctx, "git", "config", "--local", "core.fileMode", "false")
	setCmd.Dir = worktreePath
	setCmd.CombinedOutput() // best-effort; if this fails, we still try to salvage

	// Restore core.fileMode after we're done (commit or early return).
	defer func() {
		if hadPrevValue {
			restoreCmd := exec.CommandContext(ctx, "git", "config", "--local", "core.fileMode", prevValue)
			restoreCmd.Dir = worktreePath
			restoreCmd.CombinedOutput()
		} else {
			unsetCmd := exec.CommandContext(ctx, "git", "config", "--local", "--unset", "core.fileMode")
			unsetCmd.Dir = worktreePath
			unsetCmd.CombinedOutput()
		}
	}()

	// Stage everything and commit with a salvage marker.
	addCmd := exec.CommandContext(ctx, "git", "add", "-A")
	addCmd.Dir = worktreePath
	if _, err := addCmd.CombinedOutput(); err != nil {
		return
	}

	commitMsg := fmt.Sprintf("salvage: preserve uncommitted work from dead agent (%s)", taskID)
	commitCmd := exec.CommandContext(ctx, "git", "commit", "-m", commitMsg, "--no-verify")
	commitCmd.Dir = worktreePath
	commitCmd.CombinedOutput() // best-effort
}

// captureSessionID reads the first few lines of a JSONL log file looking for a session_id.
func (s *Spawner) captureSessionID(taskID, logFile string) {
	// Wait a moment for the file to be written to.
	time.Sleep(500 * time.Millisecond)

	f, err := os.Open(logFile)
	if err != nil {
		return
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for i := 0; i < 20 && scanner.Scan(); i++ {
		line := scanner.Text()
		var data map[string]interface{}
		if err := json.Unmarshal([]byte(line), &data); err != nil {
			continue
		}
		if sid, ok := data["session_id"].(string); ok && sid != "" {
			ctx := context.Background()
			s.DB.SetBlackboard(ctx, "session_id:"+taskID, sid, "spawner")
			return
		}
	}
}
