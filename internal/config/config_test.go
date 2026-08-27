package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadMissingFile(t *testing.T) {
	t.Setenv("ORCHESTRA_CONFIG", filepath.Join(t.TempDir(), "nonexistent.json"))

	cfg, err := Load()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.DefaultProjectDir != "" {
		t.Errorf("expected empty DefaultProjectDir, got %q", cfg.DefaultProjectDir)
	}
}

func TestSaveAndLoad(t *testing.T) {
	t.Setenv("ORCHESTRA_CONFIG", filepath.Join(t.TempDir(), "orchestra", "config.json"))

	want := &GlobalConfig{DefaultProjectDir: "/home/user/Projects"}
	if err := Save(want); err != nil {
		t.Fatalf("Save: %v", err)
	}

	got, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got.DefaultProjectDir != want.DefaultProjectDir {
		t.Errorf("DefaultProjectDir = %q, want %q", got.DefaultProjectDir, want.DefaultProjectDir)
	}
}

func TestPathEnvOverride(t *testing.T) {
	custom := "/tmp/custom-orchestra.json"
	t.Setenv("ORCHESTRA_CONFIG", custom)

	if got := Path(); got != custom {
		t.Errorf("Path() = %q, want %q", got, custom)
	}
}

func TestPathXDG(t *testing.T) {
	t.Setenv("ORCHESTRA_CONFIG", "")

	got := Path()
	// Should end with orchestra/config.json regardless of platform
	if filepath.Base(got) != "config.json" {
		t.Errorf("Path() = %q, want basename config.json", got)
	}
	if filepath.Base(filepath.Dir(got)) != "orchestra" {
		t.Errorf("Path() parent = %q, want orchestra", filepath.Dir(got))
	}
}

func TestSaveCreatesParentDirs(t *testing.T) {
	deep := filepath.Join(t.TempDir(), "a", "b", "c", "config.json")
	t.Setenv("ORCHESTRA_CONFIG", deep)

	if err := Save(&GlobalConfig{DefaultProjectDir: "/x"}); err != nil {
		t.Fatalf("Save to deep path: %v", err)
	}
	if _, err := os.Stat(deep); err != nil {
		t.Fatalf("config file not created at %s: %v", deep, err)
	}
}

func TestLoadInvalidJSON(t *testing.T) {
	p := filepath.Join(t.TempDir(), "bad.json")
	t.Setenv("ORCHESTRA_CONFIG", p)
	os.WriteFile(p, []byte("{invalid"), 0o644)

	_, err := Load()
	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}
}

func TestGlobalDir_Default(t *testing.T) {
	t.Setenv("ORCHESTRA_CONFIG_DIR", "")
	dir, err := GlobalDir()
	if err != nil {
		t.Fatalf("GlobalDir: %v", err)
	}
	if filepath.Base(dir) != "orchestra" {
		t.Errorf("GlobalDir = %q, want basename orchestra", dir)
	}
	// Directory should exist
	info, err := os.Stat(dir)
	if err != nil {
		t.Fatalf("GlobalDir directory not created: %v", err)
	}
	if !info.IsDir() {
		t.Error("GlobalDir path is not a directory")
	}
}

func TestGlobalDir_EnvOverride(t *testing.T) {
	custom := filepath.Join(t.TempDir(), "custom-orchestra")
	t.Setenv("ORCHESTRA_CONFIG_DIR", custom)

	dir, err := GlobalDir()
	if err != nil {
		t.Fatalf("GlobalDir: %v", err)
	}
	if dir != custom {
		t.Errorf("GlobalDir = %q, want %q", dir, custom)
	}
	// Should be created
	if _, err := os.Stat(custom); err != nil {
		t.Fatalf("custom dir not created: %v", err)
	}
}

func TestLoadSaveProjects(t *testing.T) {
	t.Setenv("ORCHESTRA_CONFIG_DIR", t.TempDir())

	// Load from nonexistent should return empty
	reg, err := LoadProjects()
	if err != nil {
		t.Fatalf("LoadProjects: %v", err)
	}
	if len(reg.Projects) != 0 {
		t.Errorf("expected empty projects, got %d", len(reg.Projects))
	}

	// Save and reload
	reg.Projects["/foo"] = &ProjectEntry{Path: "/foo", Name: "foo"}
	if err := SaveProjects(reg); err != nil {
		t.Fatalf("SaveProjects: %v", err)
	}
	got, err := LoadProjects()
	if err != nil {
		t.Fatalf("LoadProjects after save: %v", err)
	}
	if len(got.Projects) != 1 {
		t.Errorf("expected 1 project, got %d", len(got.Projects))
	}
}

func TestLoadSaveDaemonConfig(t *testing.T) {
	t.Setenv("ORCHESTRA_CONFIG_DIR", t.TempDir())

	// Load from nonexistent returns defaults
	cfg, err := LoadDaemonConfig()
	if err != nil {
		t.Fatalf("LoadDaemonConfig: %v", err)
	}
	if cfg.Interval != "5m" {
		t.Errorf("default Interval = %q, want 5m", cfg.Interval)
	}

	// Save and reload
	cfg.Interval = "10m"
	if err := SaveDaemonConfig(cfg); err != nil {
		t.Fatalf("SaveDaemonConfig: %v", err)
	}
	got, err := LoadDaemonConfig()
	if err != nil {
		t.Fatalf("LoadDaemonConfig after save: %v", err)
	}
	if got.Interval != "10m" {
		t.Errorf("Interval = %q, want 10m", got.Interval)
	}
}
