package orchestrator

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MochaCosine1206/orchestra/internal/db"
)

func setupGoalPreprocessTest(t *testing.T) *Conductor {
	t.Helper()
	d, err := db.OpenMemory()
	if err != nil {
		t.Fatalf("opening memory db: %v", err)
	}
	if err := d.InitSchema(context.Background()); err != nil {
		t.Fatalf("initializing schema: %v", err)
	}
	t.Cleanup(func() { d.Close() })

	repoRoot := t.TempDir()
	runner := &MockRunner{}
	c, err := New(ConductorOpts{
		DB:       d,
		RepoRoot: repoRoot,
		Runner:   runner,
	})
	if err != nil {
		t.Fatalf("creating conductor: %v", err)
	}
	return c
}

func TestPreprocessSmallGoal(t *testing.T) {
	c := setupGoalPreprocessTest(t)
	ctx := context.Background()

	goal := "Implement a simple function"
	processed, path, err := c.PreprocessLargeGoal(ctx, goal)
	if err != nil {
		t.Fatalf("PreprocessLargeGoal: %v", err)
	}

	if processed != goal {
		t.Error("small goals should pass through unchanged")
	}
	if path != "" {
		t.Error("small goals should not create a file")
	}
}

func TestPreprocessLargeGoal(t *testing.T) {
	c := setupGoalPreprocessTest(t)
	ctx := context.Background()

	// Create a goal exceeding the threshold (5000 tokens = 20000 chars)
	goal := strings.Repeat("x", 25000)

	// Mock runner returns a summary
	runner := c.Runner.(*MockRunner)
	runner.Outputs = []string{"This is a concise summary of the goal."}

	processed, path, err := c.PreprocessLargeGoal(ctx, goal)
	if err != nil {
		t.Fatalf("PreprocessLargeGoal: %v", err)
	}

	if processed == goal {
		t.Error("large goals should be transformed")
	}
	if !strings.Contains(processed, "concise summary") {
		t.Error("expected summary content in processed goal")
	}
	if path == "" {
		t.Error("expected full goal file path")
	}
	if !strings.Contains(processed, path) {
		t.Error("processed goal should reference the full goal file path")
	}

	// Verify file was written
	if _, err := os.Stat(path); os.IsNotExist(err) {
		t.Error("full goal file should exist")
	}
}

func TestPreprocessLargeGoalFallbackOnRunnerError(t *testing.T) {
	c := setupGoalPreprocessTest(t)
	c.Runner = nil // no runner available
	ctx := context.Background()

	goal := strings.Repeat("x", 25000)

	processed, path, err := c.PreprocessLargeGoal(ctx, goal)
	if err != nil {
		t.Fatalf("PreprocessLargeGoal: %v", err)
	}

	// Should fall back to truncation
	if len(processed) >= len(goal) {
		t.Error("expected truncated goal when runner is unavailable")
	}
	if path == "" {
		t.Error("should still create full goal file")
	}
	if !strings.Contains(processed, "Full goal:") {
		t.Error("expected full goal reference in truncated output")
	}
}

func TestPreprocessLargeGoalFileCreation(t *testing.T) {
	c := setupGoalPreprocessTest(t)
	ctx := context.Background()

	goal := strings.Repeat("test content ", 2000) // ~26K chars
	runner := c.Runner.(*MockRunner)
	runner.Outputs = []string{"Summary text"}

	_, path, _ := c.PreprocessLargeGoal(ctx, goal)

	// Verify file contents
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading goal file: %v", err)
	}
	if string(data) != goal {
		t.Error("full goal file should contain the original goal")
	}

	// Verify directory structure
	dir := filepath.Dir(path)
	if !strings.Contains(dir, "goals") {
		t.Error("goal file should be in .orchestra/goals/")
	}
}

func TestTruncatePreservingSections(t *testing.T) {
	text := `# Section 1
This is the first section with some content.

# Section 2
This is the second section.

# Section 3
This is the third section with more details.

Some additional content here.`

	// Truncate to a small budget
	result := truncatePreservingSections(text, 80)
	if len(result) > 80 {
		t.Errorf("result exceeds budget: %d > 80", len(result))
	}

	// Headings should be preserved
	if !strings.Contains(result, "# Section 1") {
		t.Error("first heading should be preserved")
	}
}

func TestTruncatePreservingSectionsNoTruncation(t *testing.T) {
	text := "Short text"
	result := truncatePreservingSections(text, 1000)
	if result != text {
		t.Error("short text should pass through unchanged")
	}
}
