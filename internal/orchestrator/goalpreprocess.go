package orchestrator

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/MochaCosine1206/orchestra/internal/agent"
)

const (
	// GoalTokenThreshold is the token count above which goals get summarized.
	GoalTokenThreshold = 5_000 // ~20K chars
	// GoalCharsPerToken is the character-to-token conversion factor.
	GoalCharsPerToken = 4
)

// PreprocessLargeGoal summarizes a goal if it exceeds the token threshold.
// Returns (processed goal, full goal file path, error).
func (c *Conductor) PreprocessLargeGoal(ctx context.Context, goal string) (string, string, error) {
	tokens := len(goal) / GoalCharsPerToken
	if tokens <= GoalTokenThreshold {
		return goal, "", nil
	}

	// Store full goal to a file agents can reference
	fullGoalPath := filepath.Join(c.RepoRoot, ".orchestra", "goals", c.SessionID+".md")
	os.MkdirAll(filepath.Dir(fullGoalPath), 0o755)
	if err := os.WriteFile(fullGoalPath, []byte(goal), 0o644); err != nil {
		return goal, "", fmt.Errorf("writing full goal: %w", err)
	}

	if c.Runner == nil {
		// No runner: truncate with section preservation
		return truncatePreservingSections(goal, GoalTokenThreshold*GoalCharsPerToken) +
			fmt.Sprintf("\n\n[Full goal: %s]", fullGoalPath), fullGoalPath, nil
	}

	// Summarize using a fast Claude call
	summary, err := c.summarizeGoal(ctx, goal)
	if err != nil {
		// Fallback: truncate with section preservation
		return truncatePreservingSections(goal, GoalTokenThreshold*GoalCharsPerToken) +
			fmt.Sprintf("\n\n[Full goal: %s]", fullGoalPath), fullGoalPath, nil
	}

	return summary + fmt.Sprintf("\n\n[Full goal stored at: %s — read this file if you need the complete context]", fullGoalPath),
		fullGoalPath, nil
}

// summarizeGoal calls Claude to produce a concise summary of a large goal.
func (c *Conductor) summarizeGoal(ctx context.Context, goal string) (string, error) {
	prompt := fmt.Sprintf(`Summarize the following goal description into a concise version (under 2000 words) that preserves:
1. All specific technical requirements
2. File names, function names, and API specifications
3. Architecture decisions and constraints
4. Acceptance criteria

Keep the summary structured with markdown headings. Remove verbose explanations and examples but retain all actionable details.

GOAL:
%s

OUTPUT: The summarized goal (markdown format, no preamble).`, goal)

	runResult, err := c.Runner.Run(ctx, RunOpts{
		Prompt:  prompt,
		Model:   agent.ModelSonnet,
		WorkDir: c.RepoRoot,
	})
	if err != nil {
		return "", fmt.Errorf("summarize goal: %w", err)
	}

	return strings.TrimSpace(runResult.Output), nil
}

// truncatePreservingSections truncates text while preserving markdown headings
// and the first line of each section.
func truncatePreservingSections(text string, maxChars int) string {
	if len(text) <= maxChars {
		return text
	}

	lines := strings.Split(text, "\n")
	var result []string
	totalChars := 0
	inSection := false

	for _, line := range lines {
		isHeading := strings.HasPrefix(line, "#")
		if isHeading {
			// Always include headings
			if totalChars+len(line)+1 > maxChars {
				break
			}
			result = append(result, line)
			totalChars += len(line) + 1
			inSection = true
			continue
		}

		if inSection && strings.TrimSpace(line) != "" {
			// Include first non-empty line after heading
			if totalChars+len(line)+1 > maxChars {
				break
			}
			result = append(result, line)
			totalChars += len(line) + 1
			inSection = false
			continue
		}

		// Include other lines until budget exhausted
		if totalChars+len(line)+1 > maxChars {
			break
		}
		result = append(result, line)
		totalChars += len(line) + 1
	}

	return strings.Join(result, "\n")
}
