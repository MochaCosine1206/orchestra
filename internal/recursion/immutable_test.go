package recursion

import (
	"os"
	"path/filepath"
	"testing"
)

func TestImmutablePaths_NotEmpty(t *testing.T) {
	paths := ImmutablePaths()
	if len(paths) == 0 {
		t.Error("ImmutablePaths() returned empty slice")
	}
}

func TestImmutablePaths_ContainsRecursion(t *testing.T) {
	paths := ImmutablePaths()
	found := false
	for _, p := range paths {
		if p == "internal/recursion/" {
			found = true
		}
	}
	if !found {
		t.Error("ImmutablePaths() missing internal/recursion/")
	}
}

func TestLockPaths_SetsReadOnly(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "test.txt")
	os.WriteFile(f, []byte("hello"), 0644)

	if err := LockPaths([]string{f}); err != nil {
		t.Fatalf("LockPaths failed: %v", err)
	}
	defer UnlockPaths()

	info, _ := os.Stat(f)
	if info.Mode().Perm() != 0444 {
		t.Errorf("perm = %o, want 0444", info.Mode().Perm())
	}
}

func TestUnlockPaths_RestoresPermissions(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "test.txt")
	os.WriteFile(f, []byte("hello"), 0755)

	if err := LockPaths([]string{f}); err != nil {
		t.Fatalf("LockPaths failed: %v", err)
	}

	if err := UnlockPaths(); err != nil {
		t.Fatalf("UnlockPaths failed: %v", err)
	}

	info, _ := os.Stat(f)
	if info.Mode().Perm() != 0755 {
		t.Errorf("perm = %o, want 0755", info.Mode().Perm())
	}
}

func TestLockPaths_SkipsNonexistent(t *testing.T) {
	err := LockPaths([]string{"/nonexistent/file.txt"})
	if err != nil {
		t.Errorf("LockPaths should skip nonexistent files, got: %v", err)
	}
}
