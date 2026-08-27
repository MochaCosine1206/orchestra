package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDefaultDockerConfig(t *testing.T) {
	cfg := DefaultDockerConfig()
	if cfg.Image != "orchestra-agent:latest" {
		t.Errorf("expected image orchestra-agent:latest, got %s", cfg.Image)
	}
	if cfg.CPUs != "1.5" {
		t.Errorf("expected CPUs 1.5, got %s", cfg.CPUs)
	}
	if cfg.Memory != "3g" {
		t.Errorf("expected Memory 3g, got %s", cfg.Memory)
	}
	if cfg.Network != "host" {
		t.Errorf("expected Network host, got %s", cfg.Network)
	}
}

func TestBuildDockerRunArgs(t *testing.T) {
	cfg := DefaultDockerConfig()
	taskID := "T-001"
	repoRoot := "/tmp/repo"
	worktree := "/tmp/repo/.worktree/T-001"
	logsDir := "/tmp/logs"
	claudeArgs := []string{"-p", "do something", "--output-format", "stream-json"}

	args := BuildDockerRunArgs(cfg, taskID, repoRoot, worktree, logsDir, claudeArgs, nil)

	// Verify structure
	if args[0] != "run" || args[1] != "--rm" {
		t.Errorf("expected 'run --rm' prefix, got %v", args[:2])
	}

	// Check container name
	nameIdx := sliceIndexOf(args, "--name")
	if nameIdx < 0 || args[nameIdx+1] != "orchestra-T-001" {
		t.Error("expected --name orchestra-T-001")
	}

	// Check repo root mount (same absolute path inside container)
	found := false
	for i, a := range args {
		if a == "-v" && i+1 < len(args) && args[i+1] == repoRoot+":"+repoRoot {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected repo root bind mount at same absolute path")
	}

	// Check working directory is the worktree path (not /workspace)
	wIdx := sliceIndexOf(args, "-w")
	if wIdx < 0 || args[wIdx+1] != worktree {
		t.Errorf("expected -w %s, got %s", worktree, args[wIdx+1])
	}

	// Check logs mount
	found = false
	for i, a := range args {
		if a == "-v" && i+1 < len(args) && args[i+1] == logsDir+":/logs" {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected logs bind mount")
	}

	// Check auth token env inheritance (--env NAME without =value)
	found = false
	for i, a := range args {
		if a == "-e" && i+1 < len(args) && args[i+1] == "CLAUDE_CODE_OAUTH_TOKEN" {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected CLAUDE_CODE_OAUTH_TOKEN env inheritance (without =value)")
	}

	// Check resource limits
	cpuIdx := sliceIndexOf(args, "--cpus")
	if cpuIdx < 0 || args[cpuIdx+1] != "1.5" {
		t.Error("expected --cpus 1.5")
	}
	memIdx := sliceIndexOf(args, "--memory")
	if memIdx < 0 || args[memIdx+1] != "3g" {
		t.Error("expected --memory 3g")
	}

	// Check claude args appended at end
	lastFour := args[len(args)-4:]
	if lastFour[0] != "-p" || lastFour[1] != "do something" ||
		lastFour[2] != "--output-format" || lastFour[3] != "stream-json" {
		t.Errorf("expected claude args at end, got %v", lastFour)
	}
}

func TestBuildDockerRunArgs_CustomConfig(t *testing.T) {
	cfg := DockerConfig{
		Image:   "custom-agent:v2",
		CPUs:    "4",
		Memory:  "8g",
		Network: "none",
	}

	args := BuildDockerRunArgs(cfg, "T-042", "/repo", "/repo/.worktree/T-042", "/l", []string{"-p", "hi"}, nil)

	// Check custom network
	netIdx := sliceIndexOf(args, "--network")
	if netIdx < 0 || args[netIdx+1] != "none" {
		t.Error("expected custom network")
	}

	// Custom image should appear before claude args
	found := false
	for _, a := range args {
		if a == "custom-agent:v2" {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected custom image name in args")
	}
}

func TestPreflightCheck_NoToken(t *testing.T) {
	// PreflightCheck requires docker to be running, so we only test the token check
	// by setting an empty token (the docker checks will fail first in CI without docker)
	t.Setenv("CLAUDE_CODE_OAUTH_TOKEN", "")

	cfg := DefaultDockerConfig()
	err := PreflightCheck(cfg)
	if err == nil {
		t.Skip("docker is available with token — skipping negative test")
	}
	// Either docker is not available or token is missing — both are valid failures
}

func TestBuildDockerRunArgs_EvalSymlinks(t *testing.T) {
	// Create a temp dir and a symlink to it to verify EvalSymlinks resolves paths.
	// On macOS, /var is itself a symlink to /private/var, so we must resolve
	// the "real" dir too to get the fully canonical path for comparison.
	rawDir := t.TempDir()
	realDir, err := filepath.EvalSymlinks(rawDir)
	if err != nil {
		t.Fatalf("failed to resolve temp dir: %v", err)
	}
	symlinkDir := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(rawDir, symlinkDir); err != nil {
		t.Skipf("cannot create symlink: %v", err)
	}

	cfg := DefaultDockerConfig()
	worktree := filepath.Join(symlinkDir, ".worktree", "T-001")
	logsDir := filepath.Join(symlinkDir, "logs")

	args := BuildDockerRunArgs(cfg, "T-001", symlinkDir, worktree, logsDir, []string{"-p", "test"}, nil)

	joined := strings.Join(args, " ")
	// After EvalSymlinks, the resolved (real) path should appear in the repo root mount
	if !strings.Contains(joined, realDir+":"+realDir) {
		t.Errorf("expected resolved repo mount %s:%s in args, got: %s", realDir, realDir, joined)
	}
}

func TestDetectSetupCommands_Python(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "requirements.txt"), []byte("flask\n"), 0o644)

	cmds := DetectSetupCommands(dir)
	if len(cmds) != 1 {
		t.Fatalf("expected 1 command, got %d: %v", len(cmds), cmds)
	}
	if !strings.Contains(cmds[0], "pip install -r requirements.txt") {
		t.Errorf("expected pip install command, got: %s", cmds[0])
	}
}

func TestDetectSetupCommands_PyProject(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "pyproject.toml"), []byte("[project]\n"), 0o644)

	cmds := DetectSetupCommands(dir)
	if len(cmds) != 1 {
		t.Fatalf("expected 1 command, got %d: %v", len(cmds), cmds)
	}
	if !strings.Contains(cmds[0], "pip install -e") {
		t.Errorf("expected pip install -e command, got: %s", cmds[0])
	}
}

func TestDetectSetupCommands_Node(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "package.json"), []byte("{}"), 0o644)

	cmds := DetectSetupCommands(dir)
	if len(cmds) != 1 {
		t.Fatalf("expected 1 command, got %d: %v", len(cmds), cmds)
	}
	if !strings.Contains(cmds[0], "npm install") {
		t.Errorf("expected npm install command, got: %s", cmds[0])
	}
}

func TestDetectSetupCommands_Yarn(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "package.json"), []byte("{}"), 0o644)
	os.WriteFile(filepath.Join(dir, "yarn.lock"), []byte(""), 0o644)

	cmds := DetectSetupCommands(dir)
	if len(cmds) != 1 {
		t.Fatalf("expected 1 command, got %d: %v", len(cmds), cmds)
	}
	if !strings.Contains(cmds[0], "yarn install") {
		t.Errorf("expected yarn install command, got: %s", cmds[0])
	}
}

func TestDetectSetupCommands_Pnpm(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "package.json"), []byte("{}"), 0o644)
	os.WriteFile(filepath.Join(dir, "pnpm-lock.yaml"), []byte(""), 0o644)

	cmds := DetectSetupCommands(dir)
	if len(cmds) != 1 {
		t.Fatalf("expected 1 command, got %d: %v", len(cmds), cmds)
	}
	if !strings.Contains(cmds[0], "pnpm install") {
		t.Errorf("expected pnpm install command, got: %s", cmds[0])
	}
}

func TestDetectSetupCommands_Go(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module test\n"), 0o644)

	cmds := DetectSetupCommands(dir)
	if len(cmds) != 1 {
		t.Fatalf("expected 1 command, got %d: %v", len(cmds), cmds)
	}
	if !strings.Contains(cmds[0], "go mod download") {
		t.Errorf("expected go mod download command, got: %s", cmds[0])
	}
}

func TestDetectSetupCommands_MultipleLanguages(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "requirements.txt"), []byte("flask\n"), 0o644)
	os.WriteFile(filepath.Join(dir, "package.json"), []byte("{}"), 0o644)
	os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module test\n"), 0o644)

	cmds := DetectSetupCommands(dir)
	if len(cmds) != 3 {
		t.Fatalf("expected 3 commands, got %d: %v", len(cmds), cmds)
	}
}

func TestDetectSetupCommands_Empty(t *testing.T) {
	dir := t.TempDir()
	cmds := DetectSetupCommands(dir)
	if len(cmds) != 0 {
		t.Errorf("expected 0 commands for empty dir, got %d: %v", len(cmds), cmds)
	}
}

func TestDetectSetupCommands_PythonPriority(t *testing.T) {
	// requirements.txt takes priority over pyproject.toml
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "requirements.txt"), []byte("flask\n"), 0o644)
	os.WriteFile(filepath.Join(dir, "pyproject.toml"), []byte("[project]\n"), 0o644)

	cmds := DetectSetupCommands(dir)
	if len(cmds) != 1 {
		t.Fatalf("expected 1 command (requirements.txt wins), got %d: %v", len(cmds), cmds)
	}
	if !strings.Contains(cmds[0], "requirements.txt") {
		t.Errorf("expected requirements.txt to take priority, got: %s", cmds[0])
	}
}

func TestBuildDockerRunArgs_WithSetupCommands(t *testing.T) {
	cfg := DefaultDockerConfig()
	setupCmds := []string{
		"pip install -r requirements.txt 2>/dev/null || true",
		"npm install 2>/dev/null || true",
	}
	claudeArgs := []string{"-p", "do something with spaces", "--output-format", "stream-json"}

	args := BuildDockerRunArgs(cfg, "T-001", "/repo", "/repo/.worktree/T-001", "/logs", claudeArgs, setupCmds)

	// Should have --entrypoint sh (not bash — sh is more portable)
	epIdx := sliceIndexOf(args, "--entrypoint")
	if epIdx < 0 || args[epIdx+1] != "sh" {
		t.Errorf("expected --entrypoint sh when setup commands present, got %s", args[epIdx+1])
	}

	// Find -c flag — it should contain setup commands and exec "$@"
	cIdx := sliceIndexOf(args, "-c")
	if cIdx < 0 {
		t.Fatal("expected -c flag in args")
	}
	cmdStr := args[cIdx+1]
	if !strings.Contains(cmdStr, "pip install") {
		t.Errorf("expected setup command in -c arg, got: %s", cmdStr)
	}
	if !strings.Contains(cmdStr, `exec "$@"`) {
		t.Errorf("expected 'exec \"$@\"' in -c arg, got: %s", cmdStr)
	}

	// After -c string, should have "sh" ($0), "claude" ($1), then claude args
	// as separate elements. This preserves word boundaries so prompts with
	// spaces aren't split by bash.
	afterC := args[cIdx+2:]
	if len(afterC) < 6 {
		t.Fatalf("expected sh + claude + 4 claude args after -c string, got %d args: %v", len(afterC), afterC)
	}
	if afterC[0] != "sh" {
		t.Errorf("expected 'sh' as $0, got: %s", afterC[0])
	}
	if afterC[1] != "claude" {
		t.Errorf("expected 'claude' as $1 (exec target), got: %s", afterC[1])
	}
	if afterC[2] != "-p" || afterC[3] != "do something with spaces" {
		t.Errorf("expected claude args preserved as separate elements, got: %v", afterC[2:4])
	}
}

// sliceIndexOf returns the index of needle in a string slice, or -1 if not found.
func sliceIndexOf(haystack []string, needle string) int {
	for i, s := range haystack {
		if s == needle {
			return i
		}
	}
	return -1
}
