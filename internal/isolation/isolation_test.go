package isolation

import (
	"os"
	"path/filepath"
	"testing"
)

func TestAcquireAndReleaseLock(t *testing.T) {
	// Use a temp dir to avoid polluting the real config.
	origDir := configDir
	configDir = filepath.Join(t.TempDir(), "orchestra")
	t.Cleanup(func() { configDir = origDir })

	lock, err := AcquireLock("test-project")
	if err != nil {
		t.Fatalf("AcquireLock: %v", err)
	}

	// PID file should exist with our PID.
	pidPath := filepath.Join(configDir, "pids", "test-project.pid")
	data, err := os.ReadFile(pidPath)
	if err != nil {
		t.Fatalf("reading pid file: %v", err)
	}
	if len(data) == 0 {
		t.Fatal("pid file is empty")
	}

	// IsLocked should report true.
	locked, pid, err := IsLocked("test-project")
	if err != nil {
		t.Fatalf("IsLocked: %v", err)
	}
	if !locked {
		t.Fatal("expected lock to be held")
	}
	if pid != os.Getpid() {
		t.Fatalf("expected PID %d, got %d", os.Getpid(), pid)
	}

	lock.ReleaseLock()

	// After release, IsLocked should report false.
	locked, _, err = IsLocked("test-project")
	if err != nil {
		t.Fatalf("IsLocked after release: %v", err)
	}
	if locked {
		t.Fatal("expected lock to be released")
	}
}

func TestAcquireLock_AlreadyHeld(t *testing.T) {
	origDir := configDir
	configDir = filepath.Join(t.TempDir(), "orchestra")
	t.Cleanup(func() { configDir = origDir })

	lock1, err := AcquireLock("dup-project")
	if err != nil {
		t.Fatalf("first AcquireLock: %v", err)
	}
	defer lock1.ReleaseLock()

	_, err = AcquireLock("dup-project")
	if err == nil {
		t.Fatal("expected error acquiring lock twice")
	}
}

func TestIsLocked_NoFile(t *testing.T) {
	origDir := configDir
	configDir = filepath.Join(t.TempDir(), "orchestra")
	t.Cleanup(func() { configDir = origDir })

	locked, pid, err := IsLocked("nonexistent")
	if err != nil {
		t.Fatalf("IsLocked: %v", err)
	}
	if locked {
		t.Fatal("expected not locked")
	}
	if pid != 0 {
		t.Fatalf("expected PID 0, got %d", pid)
	}
}

func TestSharedRateLimiter_TryConsume(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "rate-limits.db")
	origDir := configDir
	configDir = filepath.Dir(dbPath)
	t.Cleanup(func() { configDir = origDir })

	// Limit of 5, window of 3600s — threshold is 4 (80%).
	rl, err := NewSharedRateLimiter(5, 3600)
	if err != nil {
		t.Fatalf("NewSharedRateLimiter: %v", err)
	}
	defer rl.Close()

	for i := 0; i < 4; i++ {
		if !rl.TryConsume("proj-a") {
			t.Fatalf("TryConsume should succeed on call %d", i+1)
		}
	}

	// 5th call should fail (4 consumed = 80% threshold reached).
	if rl.TryConsume("proj-b") {
		t.Fatal("TryConsume should fail at threshold")
	}
}

func TestSharedRateLimiter_MultipleProjects(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "rate-limits.db")
	origDir := configDir
	configDir = filepath.Dir(dbPath)
	t.Cleanup(func() { configDir = origDir })

	rl, err := NewSharedRateLimiter(10, 3600)
	if err != nil {
		t.Fatalf("NewSharedRateLimiter: %v", err)
	}
	defer rl.Close()

	// Consume 4 from proj-a, 4 from proj-b = 8 total. Threshold is 8 (80% of 10).
	for i := 0; i < 4; i++ {
		if !rl.TryConsume("proj-a") {
			t.Fatalf("proj-a consume %d failed", i+1)
		}
	}
	for i := 0; i < 4; i++ {
		if !rl.TryConsume("proj-b") {
			t.Fatalf("proj-b consume %d failed", i+1)
		}
	}

	// 9th call should fail — 8 consumed, threshold is 8.
	if rl.TryConsume("proj-c") {
		t.Fatal("should fail at global threshold")
	}
}

func TestReleaseLock_Idempotent(t *testing.T) {
	origDir := configDir
	configDir = filepath.Join(t.TempDir(), "orchestra")
	t.Cleanup(func() { configDir = origDir })

	lock, err := AcquireLock("idempotent-project")
	if err != nil {
		t.Fatalf("AcquireLock: %v", err)
	}

	lock.ReleaseLock()
	lock.ReleaseLock() // should not panic
}
