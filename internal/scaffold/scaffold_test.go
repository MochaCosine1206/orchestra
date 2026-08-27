package scaffold

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestScaffoldRuntimeDirs(t *testing.T) {
	dir := t.TempDir()
	result, err := Scaffold(context.Background(), Options{
		ProjectRoot: dir,
	})
	if err != nil {
		t.Fatalf("Scaffold: %v", err)
	}

	for _, d := range []string{".orchestra/logs", ".orchestra/pids", ".orchestra/specs", ".worktree"} {
		path := filepath.Join(dir, d)
		if _, err := os.Stat(path); os.IsNotExist(err) {
			t.Errorf("expected dir %s to exist", d)
		}
	}

	if len(result.Files) < 4 {
		t.Errorf("expected at least 4 file actions, got %d", len(result.Files))
	}
}

func TestScaffoldClaude(t *testing.T) {
	dir := t.TempDir()
	result, err := Scaffold(context.Background(), Options{
		ProjectRoot: dir,
		ProjectName: "test-project",
		WriteClaude: true,
	})
	if err != nil {
		t.Fatalf("Scaffold: %v", err)
	}

	// Check agent defs
	for _, role := range []string{"architect", "implementer", "reviewer", "scout", "researcher"} {
		path := filepath.Join(dir, ".orchestra", "agents", role+".md")
		if _, err := os.Stat(path); os.IsNotExist(err) {
			t.Errorf("expected agent def %s to exist", role)
		}
	}

	// Check profiles
	for _, role := range []string{"architect", "implementer", "reviewer", "scout", "researcher"} {
		path := filepath.Join(dir, ".orchestra", "profiles", role+".json")
		if _, err := os.Stat(path); os.IsNotExist(err) {
			t.Errorf("expected profile %s to exist", role)
		}
	}

	// Check orchestra command
	cmdPath := filepath.Join(dir, ".claude", "commands", "orchestra.md")
	if _, err := os.Stat(cmdPath); os.IsNotExist(err) {
		t.Error("expected orchestra.md command to exist")
	}

	// Check CLAUDE.md
	claudeMD := filepath.Join(dir, "CLAUDE.md")
	if _, err := os.Stat(claudeMD); os.IsNotExist(err) {
		t.Error("expected CLAUDE.md to exist")
	}
	data, _ := os.ReadFile(claudeMD)
	if !strings.Contains(string(data), "test-project") {
		t.Error("expected CLAUDE.md to contain project name")
	}

	// Verify result tracks all files
	created := 0
	for _, f := range result.Files {
		if f.Action == "created" {
			created++
		}
	}
	if created < 12 { // 4 runtime + 5 agents + 5 profiles + 1 command + 1 CLAUDE.md
		t.Errorf("expected at least 12 created files, got %d", created)
	}
}

func TestScaffoldSkipsExisting(t *testing.T) {
	dir := t.TempDir()

	// Pre-create an agent def
	agentDir := filepath.Join(dir, ".orchestra", "agents")
	os.MkdirAll(agentDir, 0o755)
	os.WriteFile(filepath.Join(agentDir, "architect.md"), []byte("custom"), 0o644)

	result, err := Scaffold(context.Background(), Options{
		ProjectRoot: dir,
		WriteClaude: true,
	})
	if err != nil {
		t.Fatalf("Scaffold: %v", err)
	}

	// Verify architect was skipped
	for _, f := range result.Files {
		if strings.HasSuffix(f.Path, "architect.md") && f.Action == "skipped" {
			// Verify content wasn't overwritten
			data, _ := os.ReadFile(f.Path)
			if string(data) != "custom" {
				t.Error("expected custom content to be preserved")
			}
			return
		}
	}
	t.Error("expected architect.md to be skipped")
}

func TestScaffoldForceOverwrites(t *testing.T) {
	dir := t.TempDir()

	// Pre-create an agent def
	agentDir := filepath.Join(dir, ".orchestra", "agents")
	os.MkdirAll(agentDir, 0o755)
	os.WriteFile(filepath.Join(agentDir, "architect.md"), []byte("custom"), 0o644)

	_, err := Scaffold(context.Background(), Options{
		ProjectRoot: dir,
		WriteClaude: true,
		Force:       true,
	})
	if err != nil {
		t.Fatalf("Scaffold: %v", err)
	}

	// Verify content was overwritten
	data, _ := os.ReadFile(filepath.Join(agentDir, "architect.md"))
	if string(data) == "custom" {
		t.Error("expected force mode to overwrite existing file")
	}
}

func TestScaffoldLoops(t *testing.T) {
	dir := t.TempDir()
	_, err := Scaffold(context.Background(), Options{
		ProjectRoot: dir,
		WriteLoops:  true,
	})
	if err != nil {
		t.Fatalf("Scaffold: %v", err)
	}

	for _, d := range []string{".orchestra/research", ".orchestra/specs", ".orchestra/ideas", ".orchestra/creativity"} {
		path := filepath.Join(dir, d)
		if _, err := os.Stat(path); os.IsNotExist(err) {
			t.Errorf("expected dir %s to exist", d)
		}
	}
}

func TestScaffoldCLAUDEMDNotOverwritten(t *testing.T) {
	dir := t.TempDir()
	claudeMD := filepath.Join(dir, "CLAUDE.md")
	os.WriteFile(claudeMD, []byte("# My Existing CLAUDE.md"), 0o644)

	_, err := Scaffold(context.Background(), Options{
		ProjectRoot: dir,
		WriteClaude: true,
		Force:       true, // Force is true, but CLAUDE.md should still be preserved
	})
	if err != nil {
		t.Fatalf("Scaffold: %v", err)
	}

	data, _ := os.ReadFile(claudeMD)
	if !strings.Contains(string(data), "My Existing CLAUDE.md") {
		t.Error("expected existing CLAUDE.md to be preserved even with Force")
	}
}

func TestScaffoldResultTracksActions(t *testing.T) {
	dir := t.TempDir()
	result, err := Scaffold(context.Background(), Options{
		ProjectRoot: dir,
		ProjectName: "test",
		WriteClaude: true,
		WriteLoops:  true,
	})
	if err != nil {
		t.Fatalf("Scaffold: %v", err)
	}

	if len(result.Files) == 0 {
		t.Error("expected non-empty file actions")
	}

	actions := make(map[string]int)
	for _, f := range result.Files {
		actions[f.Action]++
	}
	if actions["created"] == 0 {
		t.Error("expected some files to be created")
	}
}

func TestDetectProjectType(t *testing.T) {
	tests := []struct {
		file   string
		expect ProjectType
	}{
		{"go.mod", ProjectGo},
		{"package.json", ProjectTypeScript},
		{"Cargo.toml", ProjectRust},
		{"pyproject.toml", ProjectPython},
		{"requirements.txt", ProjectPython},
	}

	for _, tt := range tests {
		t.Run(tt.file, func(t *testing.T) {
			dir := t.TempDir()
			os.WriteFile(filepath.Join(dir, tt.file), []byte(""), 0o644)
			got := DetectProjectType(dir)
			if got != tt.expect {
				t.Errorf("DetectProjectType with %s = %q, want %q", tt.file, got, tt.expect)
			}
		})
	}
}

func TestDetectProjectTypeOther(t *testing.T) {
	dir := t.TempDir()
	got := DetectProjectType(dir)
	if got != ProjectOther {
		t.Errorf("expected 'other' for empty dir, got %q", got)
	}
}

func TestDefaultTestCommand(t *testing.T) {
	tests := []struct {
		pt     ProjectType
		expect string
	}{
		{ProjectGo, "go test ./..."},
		{ProjectTypeScript, "npm test"},
		{ProjectPython, "pytest"},
		{ProjectRust, "cargo test"},
		{ProjectOther, ""},
	}
	for _, tt := range tests {
		t.Run(string(tt.pt), func(t *testing.T) {
			got := DefaultTestCommand(tt.pt)
			if got != tt.expect {
				t.Errorf("DefaultTestCommand(%q) = %q, want %q", tt.pt, got, tt.expect)
			}
		})
	}
}

func TestAppendGitignoreCreate(t *testing.T) {
	dir := t.TempDir()
	action, err := AppendGitignore(dir, false)
	if err != nil {
		t.Fatalf("AppendGitignore: %v", err)
	}
	if action != "created" {
		t.Errorf("expected 'created', got %q", action)
	}

	data, _ := os.ReadFile(filepath.Join(dir, ".gitignore"))
	if !strings.Contains(string(data), ".orchestra/") {
		t.Error("expected .orchestra/ in new .gitignore")
	}
}

func TestAppendGitignoreAppend(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, ".gitignore"), []byte("node_modules/\n"), 0o644)

	action, err := AppendGitignore(dir, false)
	if err != nil {
		t.Fatalf("AppendGitignore: %v", err)
	}
	if action != "updated" {
		t.Errorf("expected 'updated', got %q", action)
	}

	data, _ := os.ReadFile(filepath.Join(dir, ".gitignore"))
	content := string(data)
	if !strings.Contains(content, "node_modules/") {
		t.Error("expected existing content to be preserved")
	}
	if !strings.Contains(content, ".orchestra/") {
		t.Error("expected .orchestra/ to be appended")
	}
	if !strings.Contains(content, ".orchestra-hooks/") {
		t.Error("expected .orchestra-hooks/ to be appended")
	}
}

func TestAppendGitignoreSkip(t *testing.T) {
	dir := t.TempDir()
	content := "# Existing\n" + strings.Join(orchestraGitignoreEntries(), "\n") + "\n"
	os.WriteFile(filepath.Join(dir, ".gitignore"), []byte(content), 0o644)

	action, err := AppendGitignore(dir, false)
	if err != nil {
		t.Fatalf("AppendGitignore: %v", err)
	}
	if action != "skipped" {
		t.Errorf("expected 'skipped', got %q", action)
	}
}

func TestOrchHooksGitignore(t *testing.T) {
	entries := orchestraGitignoreEntries()
	found := false
	for _, e := range entries {
		if e == ".orchestra-hooks/" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected .orchestra-hooks/ in gitignore entries, got %v", entries)
	}
}

func TestGenerateClaudeMDWithProjectType(t *testing.T) {
	md := generateClaudeMD(Options{
		ProjectRoot: "/test",
		ProjectName: "my-app",
		ProjectType: ProjectGo,
	})

	if !strings.Contains(md, "my-app") {
		t.Error("expected project name in CLAUDE.md")
	}
	if !strings.Contains(md, "go test ./...") {
		t.Error("expected Go test command in CLAUDE.md")
	}
}
