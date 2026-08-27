package version

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestInstalledVersionReturnsString(t *testing.T) {
	t.Parallel()
	v := InstalledVersion()
	// In test/dev builds, expect "(devel)" or empty.
	if v != "(devel)" && v != "" {
		t.Logf("InstalledVersion() = %q (unexpected but not wrong)", v)
	}
}

func TestCheckStaleness_DevBuild(t *testing.T) {
	t.Parallel()
	// In dev/test builds InstalledVersion returns "(devel)" or "",
	// so CheckStaleness should produce no output.
	var buf bytes.Buffer
	CheckStaleness(&buf)
	if buf.Len() != 0 {
		t.Errorf("expected no output for dev build, got: %q", buf.String())
	}
}

func TestCheckStaleness_LatestVersionDev(t *testing.T) {
	t.Parallel()
	// When LatestVersion is "dev", no warning should be produced
	// regardless of installed version. Since LatestVersion is const "dev",
	// this test documents the expected behavior.
	if LatestVersion != "dev" {
		t.Skip("LatestVersion is not dev, skipping")
	}
	var buf bytes.Buffer
	CheckStaleness(&buf)
	if buf.Len() != 0 {
		t.Errorf("expected no output when LatestVersion is dev, got: %q", buf.String())
	}
}

func TestFullVersionContainsFields(t *testing.T) {
	t.Parallel()
	v := FullVersion()
	for _, want := range []string{"orchestra", "commit", "built"} {
		if !strings.Contains(v, want) {
			t.Errorf("FullVersion() should contain %q, got: %q", want, v)
		}
	}
}

func TestShortReturnsVersion(t *testing.T) {
	t.Parallel()
	v := Short()
	// When Version is "dev" and Commit is "none", Short() returns ""
	// (hides unhelpful "dev" from TUI). With ldflags, returns the version.
	if Version != "dev" && v != Version {
		t.Errorf("Short() = %q, want %q", v, Version)
	}
	if Version == "dev" && Commit == "none" && v != "" {
		t.Errorf("Short() = %q, want empty for dev+none", v)
	}
}

func TestScriptExecutePermissions(t *testing.T) {
	t.Parallel()
	// Find repo root by walking up from this test file's directory
	dir, _ := os.Getwd()
	for dir != "/" {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			break
		}
		dir = filepath.Dir(dir)
	}
	scriptsDir := filepath.Join(dir, "scripts")
	if _, err := os.Stat(scriptsDir); os.IsNotExist(err) {
		t.Skip("scripts/ directory not found")
	}

	// Check git index mode (100755) instead of filesystem permissions.
	// Worktrees with core.fileMode=false (used by defense mode) don't
	// preserve filesystem +x on checkout, making filesystem checks unreliable.
	cmd := exec.Command("git", "ls-files", "-s", "scripts/")
	cmd.Dir = dir
	lsOut, err := cmd.Output()
	if err != nil {
		// Fallback to filesystem check if git isn't available
		t.Logf("git ls-files failed, falling back to filesystem check: %v", err)
		var missing []string
		filepath.Walk(scriptsDir, func(path string, info os.FileInfo, walkErr error) error {
			if walkErr != nil || info.IsDir() {
				return walkErr
			}
			if strings.HasSuffix(path, ".sh") && info.Mode()&0111 == 0 {
				rel, _ := filepath.Rel(dir, path)
				missing = append(missing, rel)
			}
			return nil
		})
		if len(missing) > 0 {
			t.Errorf("scripts missing execute permission (G93 regression):\n  %s", strings.Join(missing, "\n  "))
		}
		return
	}

	var missing []string
	for _, line := range strings.Split(string(lsOut), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		// Format: "100644 <hash> <stage>\t<path>" or "100755 <hash> <stage>\t<path>"
		if strings.HasSuffix(line, ".sh") && strings.HasPrefix(line, "100644") {
			parts := strings.SplitN(line, "\t", 2)
			if len(parts) == 2 {
				missing = append(missing, parts[1])
			}
		}
	}
	if len(missing) > 0 {
		t.Errorf("scripts missing execute permission in git index (G93 regression):\n  %s", strings.Join(missing, "\n  "))
	}
}
