package orchestrator

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// initTestGitRepo creates a real git repo in a temp dir for iterative tests.
func initTestGitRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	for _, args := range [][]string{
		{"init", "-b", "dev"},
		{"config", "user.email", "test@test.com"},
		{"config", "user.name", "Test"},
	} {
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %s (%v)", args, out, err)
		}
	}
	// Create initial commit so HEAD exists
	os.WriteFile(filepath.Join(dir, "README.md"), []byte("init"), 0o644)
	for _, args := range [][]string{
		{"add", "-A"},
		{"commit", "-m", "init"},
	} {
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %s (%v)", args, out, err)
		}
	}
	return dir
}

func TestIterative_SequentialRounds(t *testing.T) {
	d := setupTestDB(t)
	repoRoot := initTestGitRepo(t)
	runner := &MockRunner{
		Outputs: []string{"Round 1 work", "Round 2 work", "DONE"},
	}
	c, _ := New(ConductorOpts{DB: d, RepoRoot: repoRoot, Runner: runner})
	ctx := context.Background()

	result, err := c.Go(ctx, GoOpts{
		Goal:      "Iterative goal",
		Iterative: true,
		MaxTasks:  5,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.DoneCount == 0 {
		t.Error("expected at least one completed round")
	}
	if len(runner.Calls) == 0 {
		t.Fatal("runner should have been called")
	}
}

func TestIterative_NilRunner(t *testing.T) {
	d := setupTestDB(t)
	repoRoot := initTestGitRepo(t)
	c, _ := New(ConductorOpts{DB: d, RepoRoot: repoRoot})
	ctx := context.Background()

	_, err := c.Go(ctx, GoOpts{
		Goal:      "test",
		Iterative: true,
	})
	if err == nil {
		t.Fatal("expected error for nil runner in iterative mode")
	}
}

func TestIterative_MaxRoundsRespected(t *testing.T) {
	d := setupTestDB(t)
	repoRoot := initTestGitRepo(t)
	runner := &MockRunner{
		// Never signals completion, so maxRounds should stop it
		Outputs: []string{"working..."},
	}
	c, _ := New(ConductorOpts{DB: d, RepoRoot: repoRoot, Runner: runner})
	ctx := context.Background()

	result, err := c.Go(ctx, GoOpts{
		Goal:      "endless goal",
		Iterative: true,
		MaxTasks:  3, // used as maxRounds
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Should stop at maxRounds
	if result.TaskCount > 3 {
		t.Errorf("TaskCount = %d, should be <= 3", result.TaskCount)
	}
}

func TestIterative_CompletionSignal(t *testing.T) {
	d := setupTestDB(t)
	repoRoot := initTestGitRepo(t)
	runner := &MockRunner{
		Outputs: []string{"First round of work", "The goal is complete. DONE."},
	}
	c, _ := New(ConductorOpts{DB: d, RepoRoot: repoRoot, Runner: runner})
	ctx := context.Background()

	result, err := c.Go(ctx, GoOpts{
		Goal:      "quick goal",
		Iterative: true,
		MaxTasks:  10,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Should stop early due to completion signal
	if len(runner.Calls) > 5 {
		t.Errorf("expected early stop, got %d calls", len(runner.Calls))
	}
	_ = result
}

func TestContainsCompletionSignal(t *testing.T) {
	tests := []struct {
		input string
		want  bool
	}{
		{"DONE", true},
		{"The work is DONE now", true},
		{"COMPLETE", true},
		{"FINISHED all work", true},
		{"The goal is complete", true},
		{"Still working...", false},
		{"", false},
		{"working on the next step", false},
	}
	for _, tt := range tests {
		got := containsCompletionSignal(tt.input)
		if got != tt.want {
			t.Errorf("containsCompletionSignal(%q) = %v, want %v", tt.input, got, tt.want)
		}
	}
}

func TestContainsCI(t *testing.T) {
	tests := []struct {
		s, sub string
		want   bool
	}{
		{"Hello World", "hello", true},
		{"DONE", "done", true},
		{"some text", "OTHER", false},
		{"", "test", false},
	}
	for _, tt := range tests {
		got := containsCI(tt.s, tt.sub)
		if got != tt.want {
			t.Errorf("containsCI(%q, %q) = %v, want %v", tt.s, tt.sub, got, tt.want)
		}
	}
}

func TestIterative_RunnerError(t *testing.T) {
	d := setupTestDB(t)
	repoRoot := initTestGitRepo(t)
	runner := &MockRunner{
		Outputs: []string{""},
		Errors:  []error{nil}, // no error, but no git changes = converged
	}
	c, _ := New(ConductorOpts{DB: d, RepoRoot: repoRoot, Runner: runner})
	ctx := context.Background()

	result, err := c.Go(ctx, GoOpts{
		Goal:      "test",
		Iterative: true,
		MaxTasks:  2,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Should converge quickly since runner makes no commits
	if result.DoneCount == 0 {
		t.Error("expected at least one round")
	}
}
