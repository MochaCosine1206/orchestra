package agent

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// DockerConfig holds Docker runtime settings for containerized agents.
type DockerConfig struct {
	Image   string // default: "orchestra-agent:latest"
	CPUs    string // default: "1.5"
	Memory  string // default: "3g"
	Network string // default: "host" (needs API access)
}

// DefaultDockerConfig returns sensible defaults for Docker agent containers.
func DefaultDockerConfig() DockerConfig {
	return DockerConfig{
		Image:   "orchestra-agent:latest",
		CPUs:    "1.5",
		Memory:  "3g",
		Network: "host",
	}
}

// BuildDockerRunArgs wraps claude args in docker run args.
// Returns the full args slice for exec.Command("docker", args...).
// The repo root is mounted at the same absolute path inside the container
// so that git worktree .git pointer files resolve correctly.
// The auth token is inherited from the parent environment via --env NAME
// (without =value) to avoid exposing it in the process list.
//
// Paths are resolved via filepath.EvalSymlinks to handle macOS /tmp → /private/tmp
// symlinks (L-016). Without this, the worktree's .git pointer file references
// /private/tmp/... but the container mount uses /tmp/..., breaking git.
//
// When setupCmds is non-empty, the entrypoint is overridden to run setup
// commands (dependency installation) before launching claude.
func BuildDockerRunArgs(cfg DockerConfig, taskID, repoRoot, worktreePath, logsDir string, claudeArgs []string, setupCmds []string) []string {
	// Resolve symlinks (L-016: /tmp → /private/tmp on macOS)
	if resolved, err := filepath.EvalSymlinks(repoRoot); err == nil {
		repoRoot = resolved
	}
	if resolved, err := filepath.EvalSymlinks(worktreePath); err == nil {
		worktreePath = resolved
	}
	if resolved, err := filepath.EvalSymlinks(logsDir); err == nil {
		logsDir = resolved
	}

	args := []string{
		"run", "--rm",
		"--name", "orchestra-" + taskID,
		"-v", repoRoot + ":" + repoRoot,
		"-v", logsDir + ":/logs",
		"-e", "CLAUDE_CODE_OAUTH_TOKEN",
		"-w", worktreePath,
		"--cpus", cfg.CPUs,
		"--memory", cfg.Memory,
		"--network", cfg.Network,
	}

	if len(setupCmds) > 0 {
		// Override entrypoint to run setup, then exec claude with proper arg quoting.
		// The "sh -c '... && exec "$@"' sh" pattern passes claude args as positional
		// parameters, preserving word boundaries (critical for -p "long prompt...").
		setupStr := strings.Join(setupCmds, " && ")
		args = append(args, "--entrypoint", "sh")
		args = append(args, cfg.Image)
		args = append(args, "-c", setupStr+` && exec "$@"`)
		args = append(args, "sh")     // becomes $0
		args = append(args, "claude") // becomes $1 — the command exec runs
		args = append(args, claudeArgs...)
	} else {
		args = append(args, cfg.Image)
		args = append(args, claudeArgs...)
	}
	return args
}

// DetectSetupCommands inspects the repo root for lockfiles and returns
// shell commands to install project dependencies. Follows the Codex pattern
// of auto-detecting the package manager from lockfiles.
// All commands use "2>/dev/null || true" to be non-fatal — setup failure
// should not prevent the agent from running.
func DetectSetupCommands(repoRoot string) []string {
	var cmds []string

	// Python
	if fileExists(repoRoot, "requirements.txt") {
		cmds = append(cmds, "pip install -r requirements.txt 2>/dev/null || true")
	} else if fileExists(repoRoot, "pyproject.toml") {
		cmds = append(cmds, "pip install -e '.[dev,test]' 2>/dev/null || pip install -e . 2>/dev/null || true")
	} else if fileExists(repoRoot, "setup.py") {
		cmds = append(cmds, "pip install -e . 2>/dev/null || true")
	}

	// Node
	if fileExists(repoRoot, "package.json") {
		if fileExists(repoRoot, "yarn.lock") {
			cmds = append(cmds, "yarn install --frozen-lockfile 2>/dev/null || true")
		} else if fileExists(repoRoot, "pnpm-lock.yaml") {
			cmds = append(cmds, "pnpm install --frozen-lockfile 2>/dev/null || true")
		} else {
			cmds = append(cmds, "npm install 2>/dev/null || true")
		}
	}

	// Go
	if fileExists(repoRoot, "go.mod") {
		cmds = append(cmds, "go mod download 2>/dev/null || true")
	}

	return cmds
}

// fileExists checks if a file exists at repoRoot/name.
func fileExists(repoRoot, name string) bool {
	_, err := os.Stat(filepath.Join(repoRoot, name))
	return err == nil
}

// KillContainer stops a Docker container by name (best-effort).
func KillContainer(taskID string) error {
	name := "orchestra-" + taskID
	cmd := exec.Command("docker", "kill", name)
	cmd.Run() // best-effort
	return nil
}

// ContainerRunning checks if an orchestra container is alive.
func ContainerRunning(taskID string) bool {
	name := "orchestra-" + taskID
	cmd := exec.Command("docker", "inspect", "-f", "{{.State.Running}}", name)
	out, err := cmd.Output()
	return err == nil && strings.TrimSpace(string(out)) == "true"
}

// KillAllOrchestraContainers kills all containers matching the orchestra- prefix.
func KillAllOrchestraContainers() {
	cmd := exec.Command("sh", "-c", `docker ps -q -f name=orchestra- | xargs -r docker kill`)
	cmd.Run() // best-effort
}

// PreflightCheck validates Docker is available, image exists, and auth token is set.
func PreflightCheck(cfg DockerConfig) error {
	// 1. Docker daemon running
	if err := exec.Command("docker", "info").Run(); err != nil {
		return fmt.Errorf("docker daemon not running: %w", err)
	}
	// 2. Image exists
	inspect := exec.Command("docker", "image", "inspect", cfg.Image)
	if err := inspect.Run(); err != nil {
		return fmt.Errorf("docker image %q not found — run 'orchestra docker build' first", cfg.Image)
	}
	// 3. Auth token
	if os.Getenv("CLAUDE_CODE_OAUTH_TOKEN") == "" {
		return fmt.Errorf("CLAUDE_CODE_OAUTH_TOKEN not set — run 'claude setup-token' and export the token")
	}
	return nil
}
