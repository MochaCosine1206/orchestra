package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestEstimateContextBudgetUnderThreshold(t *testing.T) {
	spec := strings.Repeat("x", 1000)  // ~250 tokens
	prompt := strings.Repeat("y", 500) // ~125 tokens
	estimated, overBudget, details := EstimateContextBudget(spec, prompt)

	if overBudget {
		t.Errorf("expected under budget for small spec, got estimated=%d", estimated)
	}
	if details == "" {
		t.Error("expected non-empty details")
	}
	if estimated <= 0 {
		t.Errorf("expected positive estimate, got %d", estimated)
	}
}

func TestEstimateContextBudgetOverThreshold(t *testing.T) {
	// 160K tokens * 4 chars/token = 640K chars needed at base.
	// With repo overhead (15K tokens) and tool overhead (1.3x),
	// we need: (X + 15000) * 1.3 > 160000
	// X > 160000/1.3 - 15000 = 108077 tokens = 432308 chars
	spec := strings.Repeat("x", 500000)
	prompt := ""
	estimated, overBudget, details := EstimateContextBudget(spec, prompt)

	if !overBudget {
		t.Errorf("expected over budget for 500K char spec, estimated=%d", estimated)
	}
	if !strings.Contains(details, "threshold=") {
		t.Error("expected threshold in details")
	}
}

func TestEstimateContextBudgetConstants(t *testing.T) {
	// Verify constants are reasonable
	if MaxContextTokens != 200_000 {
		t.Errorf("unexpected MaxContextTokens: %d", MaxContextTokens)
	}
	if BudgetThreshold != 0.80 {
		t.Errorf("unexpected BudgetThreshold: %f", BudgetThreshold)
	}
	if CharsPerToken != 4 {
		t.Errorf("unexpected CharsPerToken: %d", CharsPerToken)
	}
}

func TestEstimateContextBudgetEmptyInputs(t *testing.T) {
	estimated, overBudget, _ := EstimateContextBudget("", "")

	if overBudget {
		t.Error("expected under budget for empty inputs")
	}
	// Should still have repo overhead: 15000 * 1.3 = 19500
	if estimated != 19500 {
		t.Errorf("expected 19500 for empty inputs (repo overhead * tool factor), got %d", estimated)
	}
}

func TestEstimateContextBudgetDetailsFormat(t *testing.T) {
	spec := strings.Repeat("a", 400)
	prompt := strings.Repeat("b", 200)
	_, _, details := EstimateContextBudget(spec, prompt)

	if !strings.Contains(details, "spec=") {
		t.Error("details should contain spec=")
	}
	if !strings.Contains(details, "prompt=") {
		t.Error("details should contain prompt=")
	}
	if !strings.Contains(details, "repo=") {
		t.Error("details should contain repo=")
	}
	if !strings.Contains(details, "total=") {
		t.Error("details should contain total=")
	}
	if !strings.Contains(details, "threshold=") {
		t.Error("details should contain threshold=")
	}
}

func TestEstimateContextUsage(t *testing.T) {
	dir := t.TempDir()
	logFile := filepath.Join(dir, "test.jsonl")

	// Create a log file of known size
	// 40000 bytes / 40 = 1000 tokens → 1000/200000 * 100 = 0.5%
	data := strings.Repeat("x", 40000)
	os.WriteFile(logFile, []byte(data), 0o644)

	tokens, pct := EstimateContextUsage(logFile)
	if tokens != 1000 {
		t.Errorf("expected 1000 tokens, got %d", tokens)
	}
	if pct != 0.5 {
		t.Errorf("expected 0.5%%, got %f", pct)
	}
}

func TestEstimateContextUsageLargeFile(t *testing.T) {
	dir := t.TempDir()
	logFile := filepath.Join(dir, "large.jsonl")

	// 8MB file → 8000000/40 = 200000 tokens → 100%
	data := strings.Repeat("y", 8_000_000)
	os.WriteFile(logFile, []byte(data), 0o644)

	tokens, pct := EstimateContextUsage(logFile)
	if tokens != 200000 {
		t.Errorf("expected 200000 tokens, got %d", tokens)
	}
	if pct != 100.0 {
		t.Errorf("expected 100%%, got %f", pct)
	}
}

func TestEstimateContextUsageMissingFile(t *testing.T) {
	tokens, pct := EstimateContextUsage("/nonexistent/file.jsonl")
	if tokens != 0 || pct != 0 {
		t.Error("expected zero for missing file")
	}
}

func TestEstimateContextUsageEmptyFile(t *testing.T) {
	dir := t.TempDir()
	logFile := filepath.Join(dir, "empty.jsonl")
	os.WriteFile(logFile, []byte(""), 0o644)

	tokens, pct := EstimateContextUsage(logFile)
	if tokens != 0 || pct != 0 {
		t.Error("expected zero for empty file")
	}
}
