package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestWalkUpDiscover_Found(t *testing.T) {
	// Create a temp project structure: root/.orchestra/orchestrator.db
	root := t.TempDir()
	orchDir := filepath.Join(root, ".orchestra")
	os.MkdirAll(orchDir, 0o755)
	os.WriteFile(filepath.Join(orchDir, "orchestrator.db"), []byte("fake"), 0o644)

	// Start from a subdirectory
	subdir := filepath.Join(root, "src", "pkg")
	os.MkdirAll(subdir, 0o755)

	got, err := WalkUpDiscover(subdir)
	if err != nil {
		t.Fatalf("WalkUpDiscover: %v", err)
	}
	if got != root {
		t.Errorf("WalkUpDiscover = %q, want %q", got, root)
	}
}

func TestWalkUpDiscover_NotFound(t *testing.T) {
	tmp := t.TempDir()
	got, err := WalkUpDiscover(tmp)
	if err != nil {
		t.Fatalf("WalkUpDiscover: %v", err)
	}
	if got != "" {
		t.Errorf("WalkUpDiscover = %q, want empty", got)
	}
}

func TestWalkUpDiscover_AtRoot(t *testing.T) {
	// Start from the project root itself
	root := t.TempDir()
	orchDir := filepath.Join(root, ".orchestra")
	os.MkdirAll(orchDir, 0o755)
	os.WriteFile(filepath.Join(orchDir, "orchestrator.db"), []byte("fake"), 0o644)

	got, err := WalkUpDiscover(root)
	if err != nil {
		t.Fatalf("WalkUpDiscover: %v", err)
	}
	if got != root {
		t.Errorf("WalkUpDiscover = %q, want %q", got, root)
	}
}

func TestScanDirectory(t *testing.T) {
	root := t.TempDir()

	// Create two projects
	proj1 := filepath.Join(root, "proj1")
	proj2 := filepath.Join(root, "sub", "proj2")
	for _, p := range []string{proj1, proj2} {
		os.MkdirAll(filepath.Join(p, ".orchestra"), 0o755)
		os.WriteFile(filepath.Join(p, ".orchestra", "orchestrator.db"), []byte("fake"), 0o644)
	}

	// Create a non-project dir
	os.MkdirAll(filepath.Join(root, "notaproject"), 0o755)

	projects, err := ScanDirectory(root, 4)
	if err != nil {
		t.Fatalf("ScanDirectory: %v", err)
	}
	if len(projects) != 2 {
		t.Fatalf("ScanDirectory found %d projects, want 2: %v", len(projects), projects)
	}
}

func TestScanDirectory_MaxDepth(t *testing.T) {
	root := t.TempDir()
	// Project at depth 5 — should be missed with maxDepth 4
	deep := filepath.Join(root, "a", "b", "c", "d", "e")
	os.MkdirAll(filepath.Join(deep, ".orchestra"), 0o755)
	os.WriteFile(filepath.Join(deep, ".orchestra", "orchestrator.db"), []byte("fake"), 0o644)

	projects, err := ScanDirectory(root, 4)
	if err != nil {
		t.Fatalf("ScanDirectory: %v", err)
	}
	if len(projects) != 0 {
		t.Errorf("ScanDirectory found %d projects at depth 5 with maxDepth 4", len(projects))
	}
}

func TestRegisterProject_Dedup(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("ORCHESTRA_CONFIG_DIR", tmp)

	proj := filepath.Join(t.TempDir(), "myproject")
	os.MkdirAll(proj, 0o755)

	added1, err := RegisterProject(proj, "myproject", false)
	if err != nil {
		t.Fatalf("RegisterProject first call: %v", err)
	}
	if !added1 {
		t.Error("expected newly added on first call")
	}

	added2, err := RegisterProject(proj, "myproject", false)
	if err != nil {
		t.Fatalf("RegisterProject second call: %v", err)
	}
	if added2 {
		t.Error("expected not added on second call (dedup)")
	}

	// Verify only one entry
	reg, err := LoadProjects()
	if err != nil {
		t.Fatalf("LoadProjects: %v", err)
	}
	if len(reg.Projects) != 1 {
		t.Errorf("expected 1 project, got %d", len(reg.Projects))
	}
}

func TestRegisterProject_DefaultName(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("ORCHESTRA_CONFIG_DIR", tmp)

	proj := filepath.Join(t.TempDir(), "cool-project")
	os.MkdirAll(proj, 0o755)

	_, err := RegisterProject(proj, "", false)
	if err != nil {
		t.Fatalf("RegisterProject: %v", err)
	}

	reg, err := LoadProjects()
	if err != nil {
		t.Fatalf("LoadProjects: %v", err)
	}
	absProj, _ := filepath.Abs(proj)
	entry := reg.Projects[absProj]
	if entry == nil {
		t.Fatal("project not found in registry")
	}
	if entry.Name != "cool-project" {
		t.Errorf("Name = %q, want cool-project", entry.Name)
	}
}
