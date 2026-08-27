// Package quality implements Phase 5 quality gates for the Orchestra pipeline.
// Gates are composable checks (build, lint, test, security, coverage ratchet,
// mutation testing) that produce a ship/hold/rollback decision.
package quality

import (
	"context"
	"fmt"
	"os/exec"
	"time"
)

// ShipDecision represents the outcome of a quality gate evaluation.
type ShipDecision string

const (
	// PROMOTE means the artifact passed all gates and is ready for promotion.
	PROMOTE ShipDecision = "PROMOTE"
	// HOLD means one or more gates flagged warnings but no hard failures.
	HOLD ShipDecision = "HOLD"
	// ROLLBACK means a gate failed and the change should be reverted.
	ROLLBACK ShipDecision = "ROLLBACK"
)

// LayerResult captures the outcome of a single quality gate check.
type LayerResult struct {
	Layer    string        // Gate name (e.g., "build", "lint", "test")
	Passed   bool          // Whether the gate passed
	Score    float64       // Numeric score (e.g., coverage percentage, mutation score)
	Details  string        // Human-readable details or error output
	Duration time.Duration // How long the check took
}

// QualityReport aggregates results from all quality gates into a ship decision.
type QualityReport struct {
	Results   []LayerResult // Individual gate results
	Decision  ShipDecision  // Overall ship decision
	Reason    string        // Explanation for the decision
	Timestamp time.Time     // When the report was generated
}

// Gate is the interface that all quality gates implement.
type Gate interface {
	// Name returns a human-readable identifier for the gate.
	Name() string
	// Check runs the gate against the given project and branch, returning a result.
	Check(ctx context.Context, projectPath string, branch string) (*LayerResult, error)
}

// CommandRunner abstracts command execution for testability.
type CommandRunner interface {
	// Run executes a command in the current directory.
	Run(name string, args ...string) ([]byte, error)
	// RunDir executes a command in the specified directory.
	RunDir(dir string, name string, args ...string) ([]byte, error)
}

// DefaultCommandRunner implements CommandRunner using os/exec.
type DefaultCommandRunner struct{}

// Run executes a command in the current working directory.
func (r *DefaultCommandRunner) Run(name string, args ...string) ([]byte, error) {
	cmd := exec.Command(name, args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return out, fmt.Errorf("running %s: %w", name, err)
	}
	return out, nil
}

// RunDir executes a command in the specified directory.
func (r *DefaultCommandRunner) RunDir(dir string, name string, args ...string) ([]byte, error) {
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		return out, fmt.Errorf("running %s in %s: %w", name, dir, err)
	}
	return out, nil
}
