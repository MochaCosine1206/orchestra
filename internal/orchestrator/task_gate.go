package orchestrator

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"
)

// TaskGateResult holds the outcome of a per-task build+test gate.
type TaskGateResult struct {
	Passed      bool            `json:"passed"`
	BuildOutput string          `json:"build_output"`
	TestOutput  string          `json:"test_output"`
	Errors      []ErrorEnvelope `json:"errors"`
	Triage      TriageOutcome   `json:"triage"` // overall triage if failed
}

// CommandRunner abstracts command execution for testing.
type CommandRunner interface {
	Run(ctx context.Context, dir string, name string, args ...string) (stdout string, stderr string, err error)
}

// defaultRunner executes commands via os/exec.
type defaultRunner struct{}

func (d *defaultRunner) Run(ctx context.Context, dir string, name string, args ...string) (string, string, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	return stdout.String(), stderr.String(), err
}

// RunTaskGate validates a task's worktree by running the test command and classifying errors.
// If testCmd is empty, DetectTestCommand is used to find an appropriate command.
func RunTaskGate(ctx context.Context, worktreePath string, testCmd string) *TaskGateResult {
	return RunTaskGateWithRunner(ctx, worktreePath, testCmd, &defaultRunner{})
}

// RunTaskGateWithRunner validates a task's worktree using a custom command runner.
func RunTaskGateWithRunner(ctx context.Context, worktreePath string, testCmd string, runner CommandRunner) *TaskGateResult {
	if testCmd == "" {
		testCmd = DetectTestCommand(worktreePath)
	}
	if testCmd == "" {
		// No test command detected — pass by default (non-testable project)
		return &TaskGateResult{Passed: true}
	}

	result := &TaskGateResult{}

	// Split testCmd on "&&" to run build and test phases separately
	phases := strings.Split(testCmd, "&&")
	var allOutput strings.Builder

	for _, phase := range phases {
		phase = strings.TrimSpace(phase)
		if phase == "" {
			continue
		}

		parts := strings.Fields(phase)
		if len(parts) == 0 {
			continue
		}

		stdout, stderr, err := runner.Run(ctx, worktreePath, parts[0], parts[1:]...)
		combined := stdout + stderr
		allOutput.WriteString(combined)
		allOutput.WriteString("\n")

		if err != nil {
			// Determine if this is a build or test phase
			if isBuildPhase(phase) {
				result.BuildOutput = combined
			} else {
				result.TestOutput = combined
			}

			// Parse and classify errors from this phase
			buildErrs := ParseBuildErrors(combined)
			testErrs := ParseTestFailures(combined)
			result.Errors = append(result.Errors, buildErrs...)
			result.Errors = append(result.Errors, testErrs...)

			// If no specific errors were parsed, create a generic one
			if len(buildErrs) == 0 && len(testErrs) == 0 {
				result.Errors = append(result.Errors, ErrorEnvelope{
					ErrorType: "build_error",
					RawOutput: combined,
					Triage:    TriageRefine,
					FixHint:   fmt.Sprintf("Command failed: %s", phase),
				})
			}
		} else {
			if isBuildPhase(phase) {
				result.BuildOutput = combined
			} else {
				result.TestOutput = combined
			}
		}
	}

	if len(result.Errors) == 0 {
		result.Passed = true
		return result
	}

	// Calculate overall triage
	result.Triage = aggregateTriage(result.Errors)
	return result
}

// isBuildPhase determines whether a command is a build phase (vs test phase).
func isBuildPhase(cmd string) bool {
	cmd = strings.ToLower(cmd)
	return strings.Contains(cmd, "build") ||
		strings.Contains(cmd, "install") ||
		strings.Contains(cmd, "compile") ||
		strings.Contains(cmd, "go build") ||
		strings.Contains(cmd, "cargo build") ||
		strings.Contains(cmd, "npm install") ||
		strings.Contains(cmd, "npm run build")
}
