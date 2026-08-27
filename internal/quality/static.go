package quality

import (
	"context"
	"fmt"
	"log/slog"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// BuildGate checks that the project compiles without errors.
type BuildGate struct {
	Runner CommandRunner
	Logger *slog.Logger
}

// Name returns the gate identifier.
func (g *BuildGate) Name() string { return "build" }

// Check runs `go build ./...` in the project directory.
func (g *BuildGate) Check(ctx context.Context, projectPath string, _ string) (*LayerResult, error) {
	start := time.Now()
	g.Logger.Info("running build gate", "project", projectPath)

	out, err := g.Runner.RunDir(projectPath, "go", "build", "./...")
	duration := time.Since(start)

	result := &LayerResult{
		Layer:    g.Name(),
		Duration: duration,
	}

	if err != nil {
		result.Passed = false
		result.Details = fmt.Sprintf("build failed: %s", string(out))
		return result, nil
	}

	result.Passed = true
	result.Score = 1.0
	result.Details = "build succeeded"
	return result, nil
}

// LintGate runs golangci-lint on the project.
type LintGate struct {
	Runner CommandRunner
	Logger *slog.Logger
}

// Name returns the gate identifier.
func (g *LintGate) Name() string { return "lint" }

// Check runs `golangci-lint run` in the project directory.
func (g *LintGate) Check(ctx context.Context, projectPath string, _ string) (*LayerResult, error) {
	start := time.Now()
	g.Logger.Info("running lint gate", "project", projectPath)

	out, err := g.Runner.RunDir(projectPath, "golangci-lint", "run")
	duration := time.Since(start)

	result := &LayerResult{
		Layer:    g.Name(),
		Duration: duration,
	}

	if err != nil {
		result.Passed = false
		result.Details = fmt.Sprintf("lint issues found: %s", string(out))
		return result, nil
	}

	result.Passed = true
	result.Score = 1.0
	result.Details = "no lint issues"
	return result, nil
}

// SecurityGate runs gosec and fails on HIGH or CRITICAL findings.
type SecurityGate struct {
	Runner CommandRunner
	Logger *slog.Logger
}

// Name returns the gate identifier.
func (g *SecurityGate) Name() string { return "security" }

// Check runs `gosec ./...` in the project directory.
func (g *SecurityGate) Check(ctx context.Context, projectPath string, _ string) (*LayerResult, error) {
	start := time.Now()
	g.Logger.Info("running security gate", "project", projectPath)

	out, err := g.Runner.RunDir(projectPath, "gosec", "./...")
	duration := time.Since(start)
	output := string(out)

	result := &LayerResult{
		Layer:    g.Name(),
		Duration: duration,
	}

	if err != nil {
		// gosec exits non-zero when findings exist — check severity
		if containsHighOrCritical(output) {
			result.Passed = false
			result.Details = fmt.Sprintf("HIGH/CRITICAL security issues found: %s", output)
			return result, nil
		}
		// Non-zero exit but no HIGH/CRITICAL — treat as pass with warnings
		result.Passed = true
		result.Score = 0.5
		result.Details = fmt.Sprintf("security warnings (no HIGH/CRITICAL): %s", output)
		return result, nil
	}

	result.Passed = true
	result.Score = 1.0
	result.Details = "no security issues"
	return result, nil
}

// containsHighOrCritical checks gosec output for HIGH or CRITICAL severity findings.
func containsHighOrCritical(output string) bool {
	upper := strings.ToUpper(output)
	return strings.Contains(upper, "SEVERITY: HIGH") || strings.Contains(upper, "SEVERITY: CRITICAL")
}

// TestGate runs go test with coverage and parses the coverage percentage.
type TestGate struct {
	Runner CommandRunner
	Logger *slog.Logger
}

// Name returns the gate identifier.
func (g *TestGate) Name() string { return "test" }

// Check runs `go test -coverprofile` and parses the coverage output.
func (g *TestGate) Check(ctx context.Context, projectPath string, _ string) (*LayerResult, error) {
	start := time.Now()
	g.Logger.Info("running test gate", "project", projectPath)

	out, err := g.Runner.RunDir(projectPath, "go", "test", "-coverprofile=coverage.out", "./...")
	duration := time.Since(start)
	output := string(out)

	result := &LayerResult{
		Layer:    g.Name(),
		Duration: duration,
	}

	if err != nil {
		result.Passed = false
		result.Details = fmt.Sprintf("tests failed: %s", output)
		return result, nil
	}

	coverage := parseCoveragePercent(output)
	result.Passed = true
	result.Score = coverage
	result.Details = fmt.Sprintf("tests passed, coverage: %.1f%%", coverage)
	return result, nil
}

// coverageRe matches go test coverage output like "coverage: 78.5% of statements".
var coverageRe = regexp.MustCompile(`coverage:\s+([\d.]+)%\s+of\s+statements`)

// parseCoveragePercent extracts the coverage percentage from go test output.
// If multiple packages report coverage, the last one is used.
// Returns 0.0 if no coverage line is found.
func parseCoveragePercent(output string) float64 {
	matches := coverageRe.FindAllStringSubmatch(output, -1)
	if len(matches) == 0 {
		return 0.0
	}
	// Use the last match (summary or single-package output)
	last := matches[len(matches)-1]
	val, err := strconv.ParseFloat(last[1], 64)
	if err != nil {
		return 0.0
	}
	return val
}
