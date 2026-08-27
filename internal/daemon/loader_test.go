package daemon

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/MochaCosine1206/orchestra/internal/config"
)

func TestLoader_ReloadOnMtimeChange(t *testing.T) {
	// Create temp config dir
	dir := t.TempDir()
	t.Setenv("ORCHESTRA_CONFIG_DIR", dir)

	// Write initial config
	cfg := &config.DaemonConfig{
		Interval:      "10s",
		MaxConcurrent: 1,
	}
	writeConfig(t, dir, cfg)

	loader, err := NewLoader()
	if err != nil {
		t.Fatal(err)
	}

	if loader.Config.Interval != "10s" {
		t.Errorf("initial interval=%q, want 10s", loader.Config.Interval)
	}

	// Reload with no change — should return false
	reloaded, err := loader.Reload()
	if err != nil {
		t.Fatal(err)
	}
	if reloaded {
		t.Error("expected no reload when mtime unchanged")
	}

	// Wait a bit and write new config (different mtime)
	time.Sleep(50 * time.Millisecond)
	cfg.Interval = "30s"
	cfg.MaxConcurrent = 4
	writeConfig(t, dir, cfg)

	reloaded, err = loader.Reload()
	if err != nil {
		t.Fatal(err)
	}
	if !reloaded {
		t.Error("expected reload after mtime change")
	}
	if loader.Config.Interval != "30s" {
		t.Errorf("after reload interval=%q, want 30s", loader.Config.Interval)
	}
	if loader.Config.MaxConcurrent != 4 {
		t.Errorf("after reload max_concurrent=%d, want 4", loader.Config.MaxConcurrent)
	}
}

func TestLoader_MissingFile(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("ORCHESTRA_CONFIG_DIR", dir)

	loader, err := NewLoader()
	if err != nil {
		t.Fatal(err)
	}

	// No file exists — Reload should be no-op
	reloaded, err := loader.Reload()
	if err != nil {
		t.Fatal(err)
	}
	if reloaded {
		t.Error("expected no reload for missing file")
	}
}

func writeConfig(t *testing.T, dir string, cfg *config.DaemonConfig) {
	t.Helper()
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "daemon.json"), data, 0o644); err != nil {
		t.Fatal(err)
	}
}
