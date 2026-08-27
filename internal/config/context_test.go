package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDetectModeFrom_ProjectMode(t *testing.T) {
	root := t.TempDir()
	orchDir := filepath.Join(root, ".orchestra")
	os.MkdirAll(orchDir, 0o755)
	os.WriteFile(filepath.Join(orchDir, "orchestrator.db"), []byte("fake"), 0o644)

	mode, projectRoot := DetectModeFrom(root)
	if mode != ProjectMode {
		t.Errorf("mode = %v, want ProjectMode", mode)
	}
	if projectRoot != root {
		t.Errorf("projectRoot = %q, want %q", projectRoot, root)
	}
}

func TestDetectModeFrom_ProjectSubdir(t *testing.T) {
	root := t.TempDir()
	orchDir := filepath.Join(root, ".orchestra")
	os.MkdirAll(orchDir, 0o755)
	os.WriteFile(filepath.Join(orchDir, "orchestrator.db"), []byte("fake"), 0o644)

	subdir := filepath.Join(root, "src", "pkg")
	os.MkdirAll(subdir, 0o755)

	mode, projectRoot := DetectModeFrom(subdir)
	if mode != ProjectMode {
		t.Errorf("mode = %v, want ProjectMode", mode)
	}
	if projectRoot != root {
		t.Errorf("projectRoot = %q, want %q", projectRoot, root)
	}
}

func TestDetectModeFrom_GlobalMode(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("ORCHESTRA_CONFIG_DIR", filepath.Join(tmp, "config"))

	mode, projectRoot := DetectModeFrom(tmp)
	if mode != GlobalMode {
		t.Errorf("mode = %v, want GlobalMode", mode)
	}
	if projectRoot != "" {
		t.Errorf("projectRoot = %q, want empty", projectRoot)
	}
}

func TestDetectModeFrom_RegisteredProject(t *testing.T) {
	configDir := t.TempDir()
	t.Setenv("ORCHESTRA_CONFIG_DIR", configDir)

	// Create a project dir (with .orchestra marker) and register it
	projDir := t.TempDir()
	orchDir := filepath.Join(projDir, ".orchestra")
	os.MkdirAll(orchDir, 0o755)
	os.WriteFile(filepath.Join(orchDir, "orchestrator.db"), []byte("fake"), 0o644)

	_, err := RegisterProject(projDir, "testproj", false)
	if err != nil {
		t.Fatalf("RegisterProject: %v", err)
	}

	// A subdirectory should detect project mode via walk-up
	subdir := filepath.Join(projDir, "deep", "sub")
	os.MkdirAll(subdir, 0o755)

	mode, projectRoot := DetectModeFrom(subdir)
	if mode != ProjectMode {
		t.Errorf("mode = %v, want ProjectMode", mode)
	}
	if projectRoot != projDir {
		t.Errorf("projectRoot = %q, want %q", projectRoot, projDir)
	}
}

func TestResolveDB_WithFlag(t *testing.T) {
	got := ResolveDB("/custom/path.db")
	if got != "/custom/path.db" {
		t.Errorf("ResolveDB = %q, want /custom/path.db", got)
	}
}

func TestResolveDB_ProjectMode(t *testing.T) {
	root := t.TempDir()
	// Resolve symlinks (macOS /tmp -> /private/var/...)
	root, _ = filepath.EvalSymlinks(root)
	orchDir := filepath.Join(root, ".orchestra")
	os.MkdirAll(orchDir, 0o755)
	os.WriteFile(filepath.Join(orchDir, "orchestrator.db"), []byte("fake"), 0o644)

	// Change to project dir
	orig, _ := os.Getwd()
	os.Chdir(root)
	defer os.Chdir(orig)

	got := ResolveDB("")
	want := filepath.Join(root, ".orchestra", "orchestrator.db")
	if got != want {
		t.Errorf("ResolveDB = %q, want %q", got, want)
	}
}

func TestModeString(t *testing.T) {
	if ProjectMode.String() != "project" {
		t.Errorf("ProjectMode.String() = %q", ProjectMode.String())
	}
	if GlobalMode.String() != "global" {
		t.Errorf("GlobalMode.String() = %q", GlobalMode.String())
	}
}

func TestResolveProjectByName_Found(t *testing.T) {
	configDir := t.TempDir()
	t.Setenv("ORCHESTRA_CONFIG_DIR", configDir)

	projDir := t.TempDir()
	projDir, _ = filepath.EvalSymlinks(projDir)
	orchDir := filepath.Join(projDir, ".orchestra")
	os.MkdirAll(orchDir, 0o755)
	os.WriteFile(filepath.Join(orchDir, "orchestrator.db"), []byte("fake"), 0o644)

	_, err := RegisterProject(projDir, "my-project", false)
	if err != nil {
		t.Fatalf("RegisterProject: %v", err)
	}

	path, dbPath := ResolveProjectByName("my-project")
	if path != projDir {
		t.Errorf("path = %q, want %q", path, projDir)
	}
	wantDB := filepath.Join(projDir, ".orchestra", "orchestrator.db")
	if dbPath != wantDB {
		t.Errorf("dbPath = %q, want %q", dbPath, wantDB)
	}
}

func TestResolveProjectByName_CaseInsensitive(t *testing.T) {
	configDir := t.TempDir()
	t.Setenv("ORCHESTRA_CONFIG_DIR", configDir)

	projDir := t.TempDir()
	projDir, _ = filepath.EvalSymlinks(projDir)
	orchDir := filepath.Join(projDir, ".orchestra")
	os.MkdirAll(orchDir, 0o755)
	os.WriteFile(filepath.Join(orchDir, "orchestrator.db"), []byte("fake"), 0o644)

	RegisterProject(projDir, "MyProject", false)

	path, _ := ResolveProjectByName("myproject")
	if path != projDir {
		t.Errorf("case-insensitive lookup failed: got %q, want %q", path, projDir)
	}
}

func TestResolveProjectByName_NotFound(t *testing.T) {
	configDir := t.TempDir()
	t.Setenv("ORCHESTRA_CONFIG_DIR", configDir)

	path, dbPath := ResolveProjectByName("nonexistent")
	if path != "" || dbPath != "" {
		t.Errorf("expected empty results for nonexistent project, got %q, %q", path, dbPath)
	}
}

func TestResolveProjectByName_Empty(t *testing.T) {
	path, dbPath := ResolveProjectByName("")
	if path != "" || dbPath != "" {
		t.Errorf("expected empty results for empty name, got %q, %q", path, dbPath)
	}
}

func TestAutoRegister_EndToEnd(t *testing.T) {
	configDir := t.TempDir()
	t.Setenv("ORCHESTRA_CONFIG_DIR", configDir)

	// Create a project dir
	projDir := t.TempDir()
	projDir, _ = filepath.EvalSymlinks(projDir)
	orchDir := filepath.Join(projDir, ".orchestra")
	os.MkdirAll(orchDir, 0o755)
	os.WriteFile(filepath.Join(orchDir, "orchestrator.db"), []byte("fake"), 0o644)

	// cd into project
	orig, _ := os.Getwd()
	os.Chdir(projDir)
	defer os.Chdir(orig)

	// AutoRegister should find and register
	err := AutoRegister()
	if err != nil {
		t.Fatalf("AutoRegister: %v", err)
	}

	// Should now be resolvable by name
	reg, _ := LoadProjects()
	if len(reg.Projects) != 1 {
		t.Fatalf("expected 1 project, got %d", len(reg.Projects))
	}
	entry := reg.Projects[projDir]
	if entry == nil {
		t.Fatal("project not found in registry")
	}
	if !entry.AutoRegistered {
		t.Error("expected AutoRegistered=true")
	}

	// ResolveProjectByName should find it
	path, _ := ResolveProjectByName(entry.Name)
	if path != projDir {
		t.Errorf("ResolveProjectByName = %q, want %q", path, projDir)
	}
}
