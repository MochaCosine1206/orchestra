package scaffold

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// MCPServer describes an MCP server to install.
type MCPServer struct {
	Name    string
	Command string
	Args    []string
}

// DefaultMCPServers returns the standard MCP server list with paths templated to the project.
func DefaultMCPServers(projectRoot, dbPath string) []MCPServer {
	return []MCPServer{
		{
			Name:    "sqlite",
			Command: "npx",
			Args:    []string{"-y", "mcp-server-sqlite-npx", dbPath},
		},
		{
			Name:    "memory",
			Command: "npx",
			Args:    []string{"-y", "@modelcontextprotocol/server-memory"},
		},
		{
			Name:    "git-worktree",
			Command: "npx",
			Args:    []string{"github:Mandalorian007/git-worktree-mcp"},
		},
		{
			Name:    "pm",
			Command: "npx",
			Args:    []string{"pm-mcp"},
		},
		{
			Name:    "filesystem",
			Command: "npx",
			Args:    []string{"-y", "@modelcontextprotocol/server-filesystem", projectRoot},
		},
		{
			Name:    "playwright",
			Command: "npx",
			Args:    []string{"-y", "@playwright/mcp@0.0.41", "--headless"},
		},
	}
}

// ClaudeStatus represents the result of checking Claude CLI availability.
type ClaudeStatus struct {
	Installed     bool
	Authenticated bool
	Version       string
	Error         string
}

// CheckClaude detects whether the Claude CLI is installed and authenticated.
func CheckClaude(ctx context.Context) ClaudeStatus {
	// Check if claude is on PATH
	claudePath, err := exec.LookPath("claude")
	if err != nil {
		return ClaudeStatus{Error: "claude CLI not found on PATH"}
	}

	// Get version to confirm it works
	cmd := exec.CommandContext(ctx, claudePath, "--version")
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	if err := cmd.Run(); err != nil {
		return ClaudeStatus{Installed: true, Error: fmt.Sprintf("claude --version failed: %v", err)}
	}
	version := strings.TrimSpace(out.String())

	// Check auth by running a simple command that requires authentication
	authCmd := exec.CommandContext(ctx, claudePath, "mcp", "list", "--scope", "project")
	var authOut bytes.Buffer
	authCmd.Stdout = &authOut
	authCmd.Stderr = &authOut
	if err := authCmd.Run(); err != nil {
		errStr := authOut.String()
		if strings.Contains(errStr, "login") || strings.Contains(errStr, "auth") || strings.Contains(errStr, "not logged in") {
			return ClaudeStatus{
				Installed: true,
				Version:   version,
				Error:     "not authenticated — run 'claude login' first",
			}
		}
		// Other error — might still be authenticated, mcp list might just have no servers
	}

	return ClaudeStatus{
		Installed:     true,
		Authenticated: true,
		Version:       version,
	}
}

// mcpJSON is the structure of .mcp.json
type mcpJSON struct {
	MCPServers map[string]mcpServerEntry `json:"mcpServers"`
}

type mcpServerEntry struct {
	Command string   `json:"command"`
	Args    []string `json:"args"`
}

// WriteMCPJSON writes .orchestra/mcp.json as a fallback for environments without the Claude CLI.
func WriteMCPJSON(projectRoot string, servers []MCPServer, force bool) (string, error) {
	path := filepath.Join(projectRoot, ".orchestra", "mcp.json")

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", fmt.Errorf("creating mcp.json directory: %w", err)
	}

	if !force {
		if _, err := os.Stat(path); err == nil {
			return path, nil // already exists, skip
		}
	}

	cfg := mcpJSON{MCPServers: make(map[string]mcpServerEntry)}
	for _, s := range servers {
		cfg.MCPServers[s.Name] = mcpServerEntry{
			Command: s.Command,
			Args:    s.Args,
		}
	}

	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return "", fmt.Errorf("marshaling mcp.json: %w", err)
	}

	if err := os.WriteFile(path, append(data, '\n'), 0o644); err != nil {
		return "", fmt.Errorf("writing mcp.json: %w", err)
	}

	return path, nil
}
