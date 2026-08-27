package orchestrator

import (
	"context"
	"fmt"
	"time"

	"github.com/MochaCosine1206/orchestra/internal/agent"
)

// IterativeOpts configures iterative session-cycling mode.
type IterativeOpts struct {
	Goal      string
	TestCmd   string
	MaxRounds int
}

const defaultMaxRounds = 5

// goIterative runs the goal in iterative session-cycling mode.
// Each round spawns a single Claude session that sees prior commits via git.
func (c *Conductor) goIterative(ctx context.Context, opts GoOpts) (*GoResult, error) {
	start := time.Now()

	if c.Runner == nil {
		return nil, fmt.Errorf("ClaudeRunner is required for iterative mode")
	}

	maxRounds := defaultMaxRounds
	if opts.MaxTasks > 0 && opts.MaxTasks <= defaultMaxRounds {
		maxRounds = opts.MaxTasks
	}
	// Cap iterative rounds at defaultMaxRounds — the raised MaxTasks default (8)
	// is for multi-agent decomposition, not for iterative session cycling
	if maxRounds > defaultMaxRounds {
		maxRounds = defaultMaxRounds
	}

	result := &GoResult{SessionID: c.SessionID}

	c.log("Iterative mode: up to %d rounds", maxRounds)

	for round := 1; round <= maxRounds; round++ {
		c.log("Round %d/%d", round, maxRounds)

		// Get current git state before this round
		beforeHash, _ := gitExec(c.RepoRoot, "rev-parse", "HEAD")

		// Run Claude with the goal + context about prior rounds
		prompt := fmt.Sprintf(`You are working on iteration %d of %d for the following goal.

GOAL: %s

Previous rounds have already made commits that you can see in the git history.
Review what has been done so far and continue working toward the goal.
Focus on what still needs to be done. If the goal is complete, say "DONE" and stop.

Commit your changes before finishing.`, round, maxRounds, opts.Goal)

		runResult, err := c.Runner.Run(ctx, RunOpts{
			Prompt:  prompt,
			Model:   agent.ModelOpus,
			WorkDir: c.RepoRoot,
		})
		if err != nil {
			c.log("Round %d error: %v", round, err)
			result.FailedCount++
			continue
		}
		result.DoneCount++

		// Check if any new commits were made
		afterHash, _ := gitExec(c.RepoRoot, "rev-parse", "HEAD")
		if beforeHash == afterHash {
			c.log("Round %d: no new commits, converged", round)
			break
		}

		// Run test command between rounds if provided
		if opts.TestCmd != "" {
			timeout := 5 * time.Minute
			if opts.TestCmdTimeout > 0 {
				timeout = time.Duration(opts.TestCmdTimeout) * time.Second
			}
			if _, err := runTestCmd(c.RepoRoot, opts.TestCmd, timeout); err != nil {
				c.log("Round %d: test failed: %v", round, err)
			}
		}

		// Check for completion signal
		if containsCompletionSignal(runResult.Output) {
			c.log("Round %d: completion signal detected", round)
			break
		}
	}

	result.TaskCount = result.DoneCount + result.FailedCount
	result.Duration = time.Since(start)

	c.log("Iterative mode completed in %s (%d rounds)", result.Duration.Round(time.Second), result.DoneCount)
	return result, nil
}

func containsCompletionSignal(output string) bool {
	for _, signal := range []string{"DONE", "COMPLETE", "FINISHED", "goal is complete"} {
		if containsCI(output, signal) {
			return true
		}
	}
	return false
}

func containsCI(s, substr string) bool {
	sLower := make([]byte, len(s))
	subLower := make([]byte, len(substr))
	for i := range s {
		if s[i] >= 'A' && s[i] <= 'Z' {
			sLower[i] = s[i] + 32
		} else {
			sLower[i] = s[i]
		}
	}
	for i := range substr {
		if substr[i] >= 'A' && substr[i] <= 'Z' {
			subLower[i] = substr[i] + 32
		} else {
			subLower[i] = substr[i]
		}
	}
	return bytesContains(sLower, subLower)
}

func bytesContains(s, sub []byte) bool {
	if len(sub) > len(s) {
		return false
	}
	for i := 0; i <= len(s)-len(sub); i++ {
		match := true
		for j := range sub {
			if s[i+j] != sub[j] {
				match = false
				break
			}
		}
		if match {
			return true
		}
	}
	return false
}
