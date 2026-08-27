package daemon

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

func TestWatchdog_AliveProcess(t *testing.T) {
	dir := t.TempDir()
	pidsDir := filepath.Join(dir, "pids")
	os.MkdirAll(pidsDir, 0o755)

	// Write our own PID — we know it's alive
	pid := os.Getpid()
	os.WriteFile(filepath.Join(pidsDir, "conductor.pid"), []byte(strconv.Itoa(pid)), 0o644)

	w := NewWatchdog(nil)
	results := w.Check([]ProjectInfo{{Path: dir, PidsDir: pidsDir}})

	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if !results[0].Alive {
		t.Error("expected Alive=true for running process")
	}
	if results[0].PID != pid {
		t.Errorf("expected PID=%d, got %d", pid, results[0].PID)
	}
}

func TestWatchdog_DeadProcess(t *testing.T) {
	dir := t.TempDir()
	pidsDir := filepath.Join(dir, "pids")
	os.MkdirAll(pidsDir, 0o755)

	// Write a PID that's almost certainly not running
	os.WriteFile(filepath.Join(pidsDir, "conductor.pid"), []byte("999999999"), 0o644)

	var logs []string
	w := NewWatchdog(func(s string) { logs = append(logs, s) })
	results := w.Check([]ProjectInfo{{Path: dir, PidsDir: pidsDir}})

	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].Alive {
		t.Error("expected Alive=false for dead process")
	}
	if w.ConsecutiveFailures(dir) != 1 {
		t.Errorf("expected 1 failure, got %d", w.ConsecutiveFailures(dir))
	}
}

func TestWatchdog_CircuitBreaker(t *testing.T) {
	dir := t.TempDir()
	pidsDir := filepath.Join(dir, "pids")
	os.MkdirAll(pidsDir, 0o755)
	os.WriteFile(filepath.Join(pidsDir, "conductor.pid"), []byte("999999999"), 0o644)

	w := NewWatchdog(func(string) {})
	projects := []ProjectInfo{{Path: dir, PidsDir: pidsDir}}

	// Trip the circuit breaker (3 failures + 1 more to trip)
	for i := 0; i < maxConsecutiveFailures+1; i++ {
		w.Check(projects)
	}

	if w.ConsecutiveFailures(dir) != maxConsecutiveFailures+1 {
		t.Errorf("expected %d failures, got %d", maxConsecutiveFailures+1, w.ConsecutiveFailures(dir))
	}

	results := w.Check(projects)
	if !results[0].Tripped {
		t.Error("expected circuit breaker to be tripped")
	}
}

func TestWatchdog_ResetCircuitBreaker(t *testing.T) {
	w := NewWatchdog(nil)
	w.mu.Lock()
	w.failures["/test"] = 5
	w.mu.Unlock()

	w.ResetCircuitBreaker("/test")
	if w.ConsecutiveFailures("/test") != 0 {
		t.Errorf("expected 0 failures after reset, got %d", w.ConsecutiveFailures("/test"))
	}
}

func TestWatchdog_NoPidFile(t *testing.T) {
	dir := t.TempDir()
	pidsDir := filepath.Join(dir, "pids")
	os.MkdirAll(pidsDir, 0o755)
	// No conductor.pid file

	w := NewWatchdog(nil)
	results := w.Check([]ProjectInfo{{Path: dir, PidsDir: pidsDir}})

	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].Alive {
		t.Error("expected Alive=false when no PID file")
	}
	// No failure should be counted for missing PID
	if w.ConsecutiveFailures(dir) != 0 {
		t.Errorf("expected 0 failures for missing PID, got %d", w.ConsecutiveFailures(dir))
	}
}
