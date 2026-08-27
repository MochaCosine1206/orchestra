package logstream

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func waitForLines(t *testing.T, s *Streamer, timeout time.Duration) []string {
	t.Helper()
	var all []string
	deadline := time.After(timeout)
	for {
		select {
		case batch := <-s.Lines:
			all = append(all, batch...)
			return all
		case <-deadline:
			t.Fatalf("timed out waiting for lines (got %d so far)", len(all))
			return all
		}
	}
}

func TestStreamerWriteAndRead(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.jsonl")
	if err := os.WriteFile(path, nil, 0o644); err != nil {
		t.Fatal(err)
	}

	s := New()
	s.SwitchFile(path)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s.Start(ctx)
	defer s.Stop()

	// Write lines to the file
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	f.WriteString("line1\n")
	f.WriteString("line2\n")
	f.Close()

	lines := waitForLines(t, s, 3*time.Second)
	if len(lines) < 2 {
		t.Fatalf("expected at least 2 lines, got %d", len(lines))
	}
	if lines[0] != "line1" {
		t.Errorf("expected 'line1', got %q", lines[0])
	}
	if lines[1] != "line2" {
		t.Errorf("expected 'line2', got %q", lines[1])
	}
}

func TestStreamerSwitchFile(t *testing.T) {
	dir := t.TempDir()
	path1 := filepath.Join(dir, "file1.jsonl")
	path2 := filepath.Join(dir, "file2.jsonl")
	os.WriteFile(path1, []byte("old\n"), 0o644)
	os.WriteFile(path2, nil, 0o644)

	s := New()
	s.SwitchFile(path1)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s.Start(ctx)
	defer s.Stop()

	// Read from file1
	lines := waitForLines(t, s, 3*time.Second)
	if len(lines) == 0 || lines[0] != "old" {
		t.Errorf("expected 'old' from file1, got %v", lines)
	}

	// Switch to file2
	s.SwitchFile(path2)

	// Write to file2
	f, _ := os.OpenFile(path2, os.O_APPEND|os.O_WRONLY, 0o644)
	f.WriteString("new\n")
	f.Close()

	lines = waitForLines(t, s, 3*time.Second)
	found := false
	for _, l := range lines {
		if l == "new" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected 'new' from file2, got %v", lines)
	}
}

func TestStreamerTruncationRecovery(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.jsonl")
	// Write long initial content so truncation is detectable
	os.WriteFile(path, []byte("this is a long initial line that will be truncated\n"), 0o644)

	s := New()
	s.SwitchFile(path)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s.Start(ctx)
	defer s.Stop()

	// Read initial line
	waitForLines(t, s, 3*time.Second)

	// Truncate and write shorter content — file size < offset triggers reset
	os.WriteFile(path, []byte("new\n"), 0o644)

	lines := waitForLines(t, s, 3*time.Second)
	found := false
	for _, l := range lines {
		if l == "new" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected 'new' after truncation recovery, got %v", lines)
	}
}

func TestStreamerCurrentFile(t *testing.T) {
	s := New()
	if s.CurrentFile() != "" {
		t.Error("expected empty path initially")
	}
	s.SwitchFile("/tmp/test.log")
	if s.CurrentFile() != "/tmp/test.log" {
		t.Errorf("expected /tmp/test.log, got %q", s.CurrentFile())
	}
}

func TestStreamerStopWithoutStart(t *testing.T) {
	s := New()
	// Should not panic
	s.Stop()
}

func TestStreamerEmptyFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "empty.jsonl")
	os.WriteFile(path, nil, 0o644)

	s := New()
	s.SwitchFile(path)
	ctx, cancel := context.WithCancel(context.Background())
	s.Start(ctx)

	// Give it a moment to attempt reading
	time.Sleep(200 * time.Millisecond)
	cancel()
	s.Stop()

	// Channel should be empty (no lines to read)
	select {
	case lines := <-s.Lines:
		t.Errorf("expected no lines from empty file, got %v", lines)
	default:
		// expected
	}
}

func TestStreamerBatchesLines(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "batch.jsonl")
	os.WriteFile(path, nil, 0o644)

	s := New()
	s.SwitchFile(path)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s.Start(ctx)
	defer s.Stop()

	// Write multiple lines at once — they should arrive as a batch
	f, _ := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	for i := 0; i < 5; i++ {
		f.WriteString("batch-line\n")
	}
	f.Close()

	lines := waitForLines(t, s, 3*time.Second)
	if len(lines) < 2 {
		t.Errorf("expected batched lines (got %d), batching should group multiple lines", len(lines))
	}
}
