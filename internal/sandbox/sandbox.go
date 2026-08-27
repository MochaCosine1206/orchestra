package sandbox

import (
	"context"
	"fmt"
	"os/exec"
	"path/filepath"
)

// Sandbox orchestrates container lifecycle for agent spawning.
type Sandbox struct {
	// Enabled controls whether sandboxing is active. When false, Wrap is a no-op passthrough.
	Enabled   bool
	AuthToken string // Claude auth token to pass as ANTHROPIC_API_KEY inside containers
	RepoRoot  string // repository root — mounted into container so worktree .git pointers resolve
	manager   *ContainerManager
	network   *NetworkPolicy
	logsDir   string
}

// New creates a Sandbox instance. If enabled is false, the sandbox will bypass
// container creation and execute commands directly.
// authToken is passed as ANTHROPIC_API_KEY to containers (extracted from keychain on host).
// repoRoot is the main repo path — must be mounted for worktree git pointers to resolve.
func New(enabled bool, repoRoot, logsDir, authToken string) *Sandbox {
	return &Sandbox{
		Enabled:   enabled,
		AuthToken: authToken,
		RepoRoot:  repoRoot,
		manager:   NewContainerManager(),
		network:   NewNetworkPolicy("orchestra-sandbox"),
		logsDir:   logsDir,
	}
}

// Network returns the sandbox's network policy for customization.
func (s *Sandbox) Network() *NetworkPolicy {
	return s.network
}

// Wrap runs the command inside a Docker container using `docker run --rm`.
// Single process, clean lifecycle — no create/start/exec split.
// If Enabled is false, returns nil without executing.
func (s *Sandbox) Wrap(ctx context.Context, taskID, worktreePath string, cmd []string) ([]byte, error) {
	if !s.Enabled {
		return nil, nil
	}

	// Build docker run args
	var args []string
	args = append(args, "run", "--rm")
	args = append(args, "--name", "orchestra-sandbox-"+taskID)

	// Auth
	if s.AuthToken != "" {
		args = append(args, "-e", "ANTHROPIC_API_KEY="+s.AuthToken)
	}

	// Git identity — container has no global git config, agents fail on first commit without this
	args = append(args, "-e", "GIT_AUTHOR_NAME=Orchestra Agent")
	args = append(args, "-e", "GIT_AUTHOR_EMAIL=agent@orchestra.local")
	args = append(args, "-e", "GIT_COMMITTER_NAME=Orchestra Agent")
	args = append(args, "-e", "GIT_COMMITTER_EMAIL=agent@orchestra.local")

	// Mount strategy: repo root read-only for .git pointer resolution,
	// worktree read-write for agent modifications. Docker overlapping mounts
	// give precedence to the more specific (worktree) mount.
	// This prevents sandboxed agents from corrupting .git/ refs or objects.
	if s.RepoRoot != "" {
		args = append(args, "-v", s.RepoRoot+":"+s.RepoRoot+":ro")
		// Worktree must be writable (agent creates/edits files and commits)
		if worktreePath != "" {
			args = append(args, "-v", worktreePath+":"+worktreePath)
		}
		// .git/worktrees/<task> must be writable for git commit to update refs
		if worktreePath != "" && taskID != "" {
			gitWorktreeDir := filepath.Join(s.RepoRoot, ".git", "worktrees", "t-"+taskID)
			args = append(args, "-v", gitWorktreeDir+":"+gitWorktreeDir)
		}
	} else if worktreePath != "" {
		// Fallback: mount just worktree (won't resolve .git pointers)
		args = append(args, "-v", worktreePath+":"+worktreePath)
	}
	if s.logsDir != "" {
		args = append(args, "-v", s.logsDir+":"+s.logsDir)
	}

	// Tmpfs for /tmp (writable despite read-only root)
	args = append(args, "--tmpfs", "/tmp:rw,exec,size=512m")

	// Working directory inside container = worktree
	if worktreePath != "" {
		args = append(args, "-w", worktreePath)
	}

	// Image
	args = append(args, baseImage)

	// Command args: skip the first element (binary name like "claude")
	// because ENTRYPOINT in Dockerfile already provides it.
	// cmd is ["claude", "-p", "<prompt>", ...] → pass ["-p", "<prompt>", ...]
	if len(cmd) > 1 {
		args = append(args, cmd[1:]...)
	}

	// Run with agent timeout
	ctx, cancel := context.WithTimeout(ctx, execTimeout)
	defer cancel()

	dockerCmd := exec.CommandContext(ctx, "docker", args...)
	out, err := dockerCmd.CombinedOutput()
	if err != nil {
		return out, fmt.Errorf("sandbox: docker run: %w", err)
	}
	return out, nil
}

// CleanupAll stops and removes all tracked containers.
func (s *Sandbox) CleanupAll(ctx context.Context) error {
	var firstErr error
	for _, c := range s.manager.All() {
		if err := c.Stop(ctx); err != nil && firstErr == nil {
			firstErr = err
		}
		if err := c.Remove(ctx); err != nil && firstErr == nil {
			firstErr = err
		}
		s.manager.Remove(c.TaskID)
	}
	return firstErr
}
