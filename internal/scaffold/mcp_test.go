package scaffold

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDefaultMCPServers(t *testing.T) {
	servers := DefaultMCPServers("/my/project", "/my/project/orchestrator.db")
	if len(servers) != 6 {
		t.Fatalf("expected 6 servers, got %d", len(servers))
	}

	names := make(map[string]bool)
	for _, s := range servers {
		names[s.Name] = true
	}
	for _, expected := range []string{"sqlite", "memory", "git-worktree", "pm", "filesystem", "playwright"} {
		if !names[expected] {
			t.Errorf("missing server %q", expected)
		}
	}

	// Check paths are templated
	for _, s := range servers {
		if s.Name == "sqlite" {
			found := false
			for _, arg := range s.Args {
				if strings.Contains(arg, "orchestrator.db") {
					found = true
				}
			}
			if !found {
				t.Error("sqlite server should have db path in args")
			}
		}
		if s.Name == "filesystem" {
			found := false
			for _, arg := range s.Args {
				if arg == "/my/project" {
					found = true
				}
			}
			if !found {
				t.Error("filesystem server should have project root in args")
			}
		}
		if s.Name == "playwright" {
			found := false
			for _, arg := range s.Args {
				if arg == "--headless" {
					found = true
				}
			}
			if !found {
				t.Error("playwright server should have --headless flag")
			}
		}
	}
}

func TestWriteMCPJSON(t *testing.T) {
	dir := t.TempDir()
	servers := DefaultMCPServers(dir, filepath.Join(dir, "orchestrator.db"))

	path, err := WriteMCPJSON(dir, servers, false)
	if err != nil {
		t.Fatalf("WriteMCPJSON: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading mcp.json: %v", err)
	}

	var cfg mcpJSON
	if err := json.Unmarshal(data, &cfg); err != nil {
		t.Fatalf("parsing mcp.json: %v", err)
	}

	if len(cfg.MCPServers) != 6 {
		t.Errorf("expected 6 servers in mcp.json, got %d", len(cfg.MCPServers))
	}

	// Verify path is under .orchestra/
	expectedPath := filepath.Join(dir, ".orchestra", "mcp.json")
	if path != expectedPath {
		t.Errorf("expected path %q, got %q", expectedPath, path)
	}
}

func TestWriteMCPJSONSkipsExisting(t *testing.T) {
	dir := t.TempDir()
	orchDir := filepath.Join(dir, ".orchestra")
	os.MkdirAll(orchDir, 0o755)
	path := filepath.Join(orchDir, "mcp.json")
	os.WriteFile(path, []byte(`{"existing": true}`), 0o644)

	_, err := WriteMCPJSON(dir, DefaultMCPServers(dir, "db"), false)
	if err != nil {
		t.Fatalf("WriteMCPJSON: %v", err)
	}

	data, _ := os.ReadFile(path)
	if !strings.Contains(string(data), "existing") {
		t.Error("expected existing mcp.json to be preserved")
	}
}

func TestWriteMCPJSONForceOverwrites(t *testing.T) {
	dir := t.TempDir()
	orchDir := filepath.Join(dir, ".orchestra")
	os.MkdirAll(orchDir, 0o755)
	path := filepath.Join(orchDir, "mcp.json")
	os.WriteFile(path, []byte(`{"existing": true}`), 0o644)

	_, err := WriteMCPJSON(dir, DefaultMCPServers(dir, "db"), true)
	if err != nil {
		t.Fatalf("WriteMCPJSON: %v", err)
	}

	data, _ := os.ReadFile(path)
	if strings.Contains(string(data), "existing") {
		t.Error("expected force mode to overwrite")
	}
}

func TestWriteMCPJSONCreatesDirectory(t *testing.T) {
	dir := t.TempDir()
	// Don't pre-create .orchestra/ — WriteMCPJSON should create it
	servers := DefaultMCPServers(dir, filepath.Join(dir, "orchestrator.db"))

	path, err := WriteMCPJSON(dir, servers, false)
	if err != nil {
		t.Fatalf("WriteMCPJSON: %v", err)
	}

	if _, err := os.Stat(path); os.IsNotExist(err) {
		t.Error("expected mcp.json to be created")
	}
}
