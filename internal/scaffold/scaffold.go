// Package scaffold writes embedded assets and configuration to a target project,
// enabling orchestra to run in any directory — not just the source repo.
package scaffold

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/MochaCosine1206/orchestra/internal/assets"
)

// ProjectType identifies the detected project language/ecosystem.
type ProjectType string

const (
	ProjectGo         ProjectType = "go"
	ProjectTypeScript ProjectType = "typescript"
	ProjectPython     ProjectType = "python"
	ProjectRust       ProjectType = "rust"
	ProjectOther      ProjectType = "other"
)

// Options controls what the scaffold writer creates.
type Options struct {
	ProjectRoot string
	ProjectName string      // auto-detected or user-provided
	ProjectType ProjectType // detected or user-selected
	WriteClaude bool        // write .claude/ scaffolding
	WriteMCP    bool        // write .mcp.json + run claude mcp add
	WriteLoops  bool        // write .orchestra/ loop dirs
	Force       bool        // overwrite existing files
}

// FileAction describes what happened to a file during scaffolding.
type FileAction struct {
	Path    string
	Action  string // "created", "skipped", "overwritten"
}

// Result summarizes what the scaffold writer did.
type Result struct {
	Files       []FileAction
	MCPErrors   []error
	ClaudeCheck ClaudeStatus
}

// Scaffold writes files to a target project based on the given options.
func Scaffold(ctx context.Context, opts Options) (*Result, error) {
	result := &Result{}

	// Phase 1: Runtime dirs (always created, under .orchestra/)
	runtimeDirs := []string{".orchestra", ".orchestra/logs", ".orchestra/pids", ".orchestra/specs", ".worktree"}
	for _, dir := range runtimeDirs {
		path := filepath.Join(opts.ProjectRoot, dir)
		if err := os.MkdirAll(path, 0o755); err != nil {
			return nil, fmt.Errorf("creating %s: %w", dir, err)
		}
		result.Files = append(result.Files, FileAction{Path: path, Action: "created"})
	}

	// Phase 2: Claude Code scaffolding
	if opts.WriteClaude {
		if err := scaffoldClaude(opts, result); err != nil {
			return nil, fmt.Errorf("scaffolding .claude: %w", err)
		}
	}

	// Phase 3: MCP server installation
	if opts.WriteMCP {
		scaffoldMCP(ctx, opts, result)
	}

	// Phase 4: Loop scaffolding
	if opts.WriteLoops {
		if err := scaffoldLoops(opts, result); err != nil {
			return nil, fmt.Errorf("scaffolding .orchestra: %w", err)
		}
	}

	return result, nil
}

func scaffoldClaude(opts Options, result *Result) error {
	// Agent definitions (in .orchestra/ to avoid colliding with repo's .claude/)
	roles := assets.AllAgentRoles()
	agentDir := filepath.Join(opts.ProjectRoot, ".orchestra", "agents")
	if err := os.MkdirAll(agentDir, 0o755); err != nil {
		return fmt.Errorf("creating agents dir: %w", err)
	}

	for _, role := range roles {
		data, err := assets.AgentDef(role)
		if err != nil {
			return fmt.Errorf("reading embedded agent def %s: %w", role, err)
		}
		path := filepath.Join(agentDir, role+".md")
		action := writeFile(path, data, opts.Force)
		result.Files = append(result.Files, FileAction{Path: path, Action: action})
	}

	// Permission profiles (in .orchestra/ to avoid colliding with repo's .claude/)
	profileDir := filepath.Join(opts.ProjectRoot, ".orchestra", "profiles")
	if err := os.MkdirAll(profileDir, 0o755); err != nil {
		return fmt.Errorf("creating profiles dir: %w", err)
	}

	for _, role := range roles {
		data, err := assets.Profile(role)
		if err != nil {
			return fmt.Errorf("reading embedded profile %s: %w", role, err)
		}
		path := filepath.Join(profileDir, role+".json")
		action := writeFile(path, data, opts.Force)
		result.Files = append(result.Files, FileAction{Path: path, Action: action})
	}

	// Orchestra slash command
	cmdDir := filepath.Join(opts.ProjectRoot, ".claude", "commands")
	if err := os.MkdirAll(cmdDir, 0o755); err != nil {
		return fmt.Errorf("creating commands dir: %w", err)
	}

	cmdData, err := assets.Template("orchestra-command.md")
	if err != nil {
		return fmt.Errorf("reading orchestra command template: %w", err)
	}
	cmdPath := filepath.Join(cmdDir, "orchestra.md")
	action := writeFile(cmdPath, cmdData, opts.Force)
	result.Files = append(result.Files, FileAction{Path: cmdPath, Action: action})

	// CLAUDE.md skeleton (only if not exists, regardless of Force)
	claudeMDPath := filepath.Join(opts.ProjectRoot, "CLAUDE.md")
	if _, err := os.Stat(claudeMDPath); os.IsNotExist(err) {
		skeleton := generateClaudeMD(opts)
		if err := os.WriteFile(claudeMDPath, []byte(skeleton), 0o644); err != nil {
			return fmt.Errorf("writing CLAUDE.md: %w", err)
		}
		result.Files = append(result.Files, FileAction{Path: claudeMDPath, Action: "created"})
	} else {
		result.Files = append(result.Files, FileAction{Path: claudeMDPath, Action: "skipped"})
	}

	return nil
}

func scaffoldMCP(ctx context.Context, opts Options, result *Result) {
	dbPath := filepath.Join(opts.ProjectRoot, ".orchestra", "orchestrator.db")
	servers := DefaultMCPServers(opts.ProjectRoot, dbPath)

	// Always write .orchestra/mcp.json — agents receive it via --mcp-config flag
	mcpPath, err := WriteMCPJSON(opts.ProjectRoot, servers, opts.Force)
	if err != nil {
		result.MCPErrors = append(result.MCPErrors, err)
	} else {
		result.Files = append(result.Files, FileAction{Path: mcpPath, Action: "created"})
	}

	// Check Claude CLI status for informational messages
	status := CheckClaude(ctx)
	result.ClaudeCheck = status

	if !status.Installed {
		result.MCPErrors = append(result.MCPErrors,
			fmt.Errorf("claude CLI not installed — install from https://claude.com/download then run 'claude login'"))
	} else if !status.Authenticated {
		result.MCPErrors = append(result.MCPErrors,
			fmt.Errorf("claude CLI not authenticated — run 'claude login' to sign in"))
	}
}

func scaffoldLoops(opts Options, result *Result) error {
	loopDirs := []string{
		".orchestra/research",
		".orchestra/specs",
		".orchestra/ideas",
		".orchestra/creativity",
	}
	for _, dir := range loopDirs {
		path := filepath.Join(opts.ProjectRoot, dir)
		if err := os.MkdirAll(path, 0o755); err != nil {
			return fmt.Errorf("creating %s: %w", dir, err)
		}
		result.Files = append(result.Files, FileAction{Path: path, Action: "created"})
	}
	return nil
}

// writeFile writes data to path. Skips if file exists and force is false.
// Returns the action taken: "created", "skipped", or "overwritten".
func writeFile(path string, data []byte, force bool) string {
	if !force {
		if _, err := os.Stat(path); err == nil {
			return "skipped"
		}
	}

	action := "created"
	if _, err := os.Stat(path); err == nil {
		action = "overwritten"
	}

	os.WriteFile(path, data, 0o644)
	return action
}

func generateClaudeMD(opts Options) string {
	name := opts.ProjectName
	if name == "" {
		name = filepath.Base(opts.ProjectRoot)
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("# CLAUDE.md — %s\n\n", name))
	sb.WriteString("## Project Overview\n\n")
	sb.WriteString(fmt.Sprintf("%s — [describe your project here]\n\n", name))

	sb.WriteString("## Orchestra Integration\n\n")
	sb.WriteString("This project uses [Claude Orchestra](https://github.com/MochaCosine1206/orchestra) for multi-agent code orchestration.\n\n")
	sb.WriteString("### Quick Start\n\n")
	sb.WriteString("```bash\norchestra dashboard    # Launch TUI dashboard\n")
	sb.WriteString("orchestra go --goal \"your task\"  # Decompose and execute\n")
	sb.WriteString("orchestra status       # Check progress\n```\n\n")

	// Project-type-specific section
	testCmd := defaultTestCommand(opts.ProjectType)
	if testCmd != "" {
		sb.WriteString("## Development\n\n")
		sb.WriteString(fmt.Sprintf("### Test Command\n\n```bash\n%s\n```\n\n", testCmd))
	}

	sb.WriteString("## Development Guidelines\n\n")
	sb.WriteString("- [Add your project-specific conventions here]\n")

	return sb.String()
}

// defaultTestCommand returns the standard test command for a project type.
func defaultTestCommand(pt ProjectType) string {
	switch pt {
	case ProjectGo:
		return "go test ./..."
	case ProjectTypeScript:
		return "npm test"
	case ProjectPython:
		return "pytest"
	case ProjectRust:
		return "cargo test"
	default:
		return ""
	}
}

// DetectProjectType detects the project type from files in the root directory.
func DetectProjectType(root string) ProjectType {
	checks := []struct {
		file string
		pt   ProjectType
	}{
		{"go.mod", ProjectGo},
		{"package.json", ProjectTypeScript},
		{"Cargo.toml", ProjectRust},
		{"pyproject.toml", ProjectPython},
		{"requirements.txt", ProjectPython},
		{"setup.py", ProjectPython},
	}
	for _, c := range checks {
		if _, err := os.Stat(filepath.Join(root, c.file)); err == nil {
			return c.pt
		}
	}
	return ProjectOther
}

// AppendGitignore appends orchestra entries to an existing .gitignore or creates one.
func AppendGitignore(projectRoot string, force bool) (string, error) {
	path := filepath.Join(projectRoot, ".gitignore")
	entries := orchestraGitignoreEntries()

	existing, err := os.ReadFile(path)
	if err != nil {
		// No .gitignore exists, create one
		content := "# Orchestra\n" + strings.Join(entries, "\n") + "\n"
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			return "", fmt.Errorf("creating .gitignore: %w", err)
		}
		return "created", nil
	}

	// Check which entries are already present
	existingStr := string(existing)
	var missing []string
	for _, entry := range entries {
		if !strings.Contains(existingStr, entry) {
			missing = append(missing, entry)
		}
	}

	if len(missing) == 0 {
		return "skipped", nil
	}

	// Append missing entries
	addition := "\n# Orchestra\n" + strings.Join(missing, "\n") + "\n"
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return "", fmt.Errorf("opening .gitignore: %w", err)
	}
	defer f.Close()

	if _, err := f.WriteString(addition); err != nil {
		return "", fmt.Errorf("appending to .gitignore: %w", err)
	}

	return "updated", nil
}

func orchestraGitignoreEntries() []string {
	return []string{
		".orchestra/",
		".worktree/",
		".orchestra-hooks/",
	}
}

// DefaultTestCommand is exported for use by other packages.
func DefaultTestCommand(pt ProjectType) string {
	return defaultTestCommand(pt)
}
