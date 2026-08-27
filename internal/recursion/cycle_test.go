package recursion

import (
	"os"
	"path/filepath"
	"testing"
)

func TestAcquireLock_Basic(t *testing.T) {
	cleanup, err := AcquireLock("/tmp/test-repo-basic", 0)
	if err != nil {
		t.Fatalf("AcquireLock failed: %v", err)
	}
	defer cleanup()
}

func TestAcquireLock_DoubleLockFails(t *testing.T) {
	cleanup1, err := AcquireLock("/tmp/test-repo-double", 0)
	if err != nil {
		t.Fatalf("first AcquireLock failed: %v", err)
	}
	defer cleanup1()

	_, err = AcquireLock("/tmp/test-repo-double", 0)
	if err == nil {
		t.Error("second AcquireLock should have failed")
	}
}

func TestAcquireLock_DifferentDepthsOK(t *testing.T) {
	cleanup1, err := AcquireLock("/tmp/test-repo-depths", 0)
	if err != nil {
		t.Fatalf("AcquireLock depth 0 failed: %v", err)
	}
	defer cleanup1()

	cleanup2, err := AcquireLock("/tmp/test-repo-depths", 1)
	if err != nil {
		t.Fatalf("AcquireLock depth 1 failed: %v", err)
	}
	defer cleanup2()
}

func TestAcquireLock_ReleaseAndReacquire(t *testing.T) {
	cleanup, err := AcquireLock("/tmp/test-repo-release", 0)
	if err != nil {
		t.Fatalf("first AcquireLock failed: %v", err)
	}
	cleanup()

	cleanup2, err := AcquireLock("/tmp/test-repo-release", 0)
	if err != nil {
		t.Fatalf("re-AcquireLock failed: %v", err)
	}
	defer cleanup2()
}

func TestAcquireLock_CleanupRemovesFile(t *testing.T) {
	cleanup, err := AcquireLock("/tmp/test-repo-cleanup", 0)
	if err != nil {
		t.Fatalf("AcquireLock failed: %v", err)
	}

	// Find the lock file
	entries, _ := os.ReadDir(lockDir)
	found := false
	for _, e := range entries {
		if filepath.Ext(e.Name()) == ".lock" {
			found = true
		}
	}
	if !found {
		t.Error("lock file not found before cleanup")
	}

	cleanup()

	// After cleanup, our specific lock should be gone
	cleanup2, err := AcquireLock("/tmp/test-repo-cleanup", 0)
	if err != nil {
		t.Fatalf("post-cleanup AcquireLock failed: %v", err)
	}
	defer cleanup2()
}
