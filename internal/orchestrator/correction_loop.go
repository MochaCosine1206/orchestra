package orchestrator

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// ansiPattern matches ANSI escape sequences (color codes, cursor movement, etc.)
var ansiPattern = regexp.MustCompile(`\x1b\[[0-9;]*[a-zA-Z]|\x1b\].*?\x07`)

// stripANSI removes all ANSI escape sequences from a string.
func stripANSI(s string) string {
	return ansiPattern.ReplaceAllString(s, "")
}

// CorrectionEntry represents a single file-level fix derived from gate output.
type CorrectionEntry struct {
	Title              string `json:"title" yaml:"title"`
	File               string `json:"file" yaml:"file"`
	Error              string `json:"error" yaml:"error"`
	Fix                string `json:"fix" yaml:"fix"`
	AcceptanceCriteria string `json:"acceptance_criteria" yaml:"acceptance_criteria"`
}

// CorrectionReport is the output of error analysis for a single correction iteration.
type CorrectionReport struct {
	Corrections []CorrectionEntry `json:"corrections" yaml:"corrections"`
	GateOutput  string            `json:"gate_output" yaml:"gate_output"`
	Iteration   int               `json:"iteration" yaml:"iteration"`
}

// CorrectionLoopResult captures the outcome of the correction loop.
type CorrectionLoopResult struct {
	Passed     bool
	Iterations int
	Reports    []CorrectionReport
	FinalGate  string // output from the last gate run
}

// AgentSpawnerFunc is the signature for spawning a correction agent. Callers
// provide an implementation that invokes Claude Code (or a mock in tests).
// The prompt contains the full correction context; repoRoot is the working directory.
type AgentSpawnerFunc func(ctx context.Context, repoRoot string, prompt string) error

// --- Regex patterns for error-to-file mapping ---

// Rust compiler: error[E0308]: mismatched types
//
//	--> src/main.rs:42:5
var rustFileLineRe = regexp.MustCompile(`-->\s+([^\s:]+\.rs):(\d+):\d+`)

// Rust compiler error message line (for context)
var rustErrorMsgRe = regexp.MustCompile(`error(?:\[E\d+\])?: (.+)`)

// TypeScript: src/app.ts(15,3): error TS2345: ...
var tsFileLineRe = regexp.MustCompile(`([^\s(]+\.tsx?)\((\d+),\d+\):\s*error\s+TS\d+:\s*(.+)`)

// Generic file:line:col pattern (Go, gcc, etc.)
var genericFileLineRe = regexp.MustCompile(`^([^\s:]+\.\w+):(\d+):\d+:\s*(.+)`)

// Test failure: FAIL path/to/package or FAIL path/to/file
var testFailRe = regexp.MustCompile(`^FAIL\s+(\S+)`)

// Playwright failure: indicates a test file with failures
// Pattern: "  1) test name" followed by "    at path/to/file.spec.ts:line:col"
var playwrightFailFileRe = regexp.MustCompile(`at\s+([^\s:]+\.(?:spec|test)\.\w+):(\d+):\d+`)

// Playwright test name line: "  1) description › test name"
var playwrightTestNameRe = regexp.MustCompile(`^\s+\d+\)\s+(.+)$`)

// ParseErrorsToCorrections analyzes gate output and maps errors to file-level
// correction entries. It handles Rust, TypeScript, Go, generic compiler output,
// test failures, and Playwright test failures.
func ParseErrorsToCorrections(gateOutput string, repoRoot string) []CorrectionEntry {
	var corrections []CorrectionEntry
	seen := make(map[string]bool) // dedupe by file+error

	lines := strings.Split(gateOutput, "\n")

	// Track pending Rust error context: the error message line comes before the --> file line.
	var pendingRustError string

	// Track pending Playwright test name for mapping to file
	var pendingPlaywrightTest string

	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}

		// Rust compiler errors: error line then --> file:line:col
		if m := rustErrorMsgRe.FindStringSubmatch(trimmed); m != nil {
			pendingRustError = m[1]
			continue
		}

		if m := rustFileLineRe.FindStringSubmatch(trimmed); m != nil {
			file := m[1]
			errorMsg := pendingRustError
			if errorMsg == "" {
				errorMsg = "Rust compiler error"
			}
			key := file + "|" + errorMsg
			if !seen[key] {
				seen[key] = true
				corrections = append(corrections, CorrectionEntry{
					Title:              fmt.Sprintf("Fix Rust error in %s", filepath.Base(file)),
					File:               file,
					Error:              errorMsg,
					Fix:                fmt.Sprintf("Fix the Rust compiler error: %s", errorMsg),
					AcceptanceCriteria: fmt.Sprintf("cargo build completes without errors in %s", file),
				})
			}
			pendingRustError = ""
			continue
		}

		// TypeScript errors: file(line,col): error TS####: message
		if m := tsFileLineRe.FindStringSubmatch(trimmed); m != nil {
			file := m[1]
			errorMsg := m[3]
			key := file + "|" + errorMsg
			if !seen[key] {
				seen[key] = true
				corrections = append(corrections, CorrectionEntry{
					Title:              fmt.Sprintf("Fix TypeScript error in %s", filepath.Base(file)),
					File:               file,
					Error:              errorMsg,
					Fix:                fmt.Sprintf("Fix the TypeScript error: %s", errorMsg),
					AcceptanceCriteria: fmt.Sprintf("tsc compiles %s without errors", file),
				})
			}
			continue
		}

		// Generic file:line:col pattern (Go, gcc, etc.)
		if m := genericFileLineRe.FindStringSubmatch(trimmed); m != nil {
			file := m[1]
			errorMsg := m[3]
			key := file + "|" + errorMsg
			if !seen[key] {
				seen[key] = true
				corrections = append(corrections, CorrectionEntry{
					Title:              fmt.Sprintf("Fix error in %s", filepath.Base(file)),
					File:               file,
					Error:              errorMsg,
					Fix:                fmt.Sprintf("Fix the compiler error: %s", errorMsg),
					AcceptanceCriteria: fmt.Sprintf("Build completes without errors in %s", file),
				})
			}
			continue
		}

		// Playwright test failures: look for "at file.spec.ts:line:col"
		if m := playwrightTestNameRe.FindStringSubmatch(line); m != nil {
			pendingPlaywrightTest = m[1]
			continue
		}

		if m := playwrightFailFileRe.FindStringSubmatch(trimmed); m != nil {
			file := m[1]
			testName := pendingPlaywrightTest
			if testName == "" {
				testName = "Playwright test"
			}
			key := file + "|" + testName
			if !seen[key] {
				seen[key] = true
				corrections = append(corrections, CorrectionEntry{
					Title:              fmt.Sprintf("Fix Playwright test in %s", filepath.Base(file)),
					File:               file,
					Error:              fmt.Sprintf("Test failed: %s", testName),
					Fix:                fmt.Sprintf("Fix the failing Playwright test: %s", testName),
					AcceptanceCriteria: fmt.Sprintf("npx playwright test %s passes", file),
				})
			}
			pendingPlaywrightTest = ""
			continue
		}

		// Test failure: FAIL package/path
		if m := testFailRe.FindStringSubmatch(trimmed); m != nil {
			pkg := m[1]
			// Try to resolve package to a directory relative to repoRoot
			file := resolveTestPackageToFile(repoRoot, pkg)
			key := file + "|test_failure"
			if !seen[key] {
				seen[key] = true
				corrections = append(corrections, CorrectionEntry{
					Title:              fmt.Sprintf("Fix test failure in %s", pkg),
					File:               file,
					Error:              fmt.Sprintf("Tests failed in %s", pkg),
					Fix:                gatherTestFailureContext(lines, i, pkg),
					AcceptanceCriteria: fmt.Sprintf("All tests pass in %s", pkg),
				})
			}
			continue
		}
	}

	return corrections
}

// resolveTestPackageToFile attempts to map a Go package path or test identifier
// to a file path relative to repoRoot. Falls back to the raw package string.
func resolveTestPackageToFile(repoRoot, pkg string) string {
	// If it looks like a file path already, return as-is
	if strings.HasSuffix(pkg, ".go") || strings.HasSuffix(pkg, ".ts") ||
		strings.HasSuffix(pkg, ".js") || strings.HasSuffix(pkg, ".rs") {
		return pkg
	}

	// Try treating as a Go package: strip module prefix and check if directory exists
	// e.g., "github.com/org/repo/internal/foo" → "internal/foo"
	parts := strings.Split(pkg, "/")
	for i := range parts {
		candidate := filepath.Join(parts[i:]...)
		dir := filepath.Join(repoRoot, candidate)
		if info, err := os.Stat(dir); err == nil && info.IsDir() {
			return candidate
		}
	}

	return pkg
}

// gatherTestFailureContext collects a few lines after a FAIL line to provide
// context about what failed.
func gatherTestFailureContext(lines []string, failIdx int, pkg string) string {
	var context []string
	context = append(context, fmt.Sprintf("Fix failing tests in %s.", pkg))

	// Collect up to 5 lines of context after the FAIL line
	end := failIdx + 6
	if end > len(lines) {
		end = len(lines)
	}
	for j := failIdx + 1; j < end; j++ {
		trimmed := strings.TrimSpace(lines[j])
		if trimmed != "" {
			context = append(context, trimmed)
		}
	}

	return strings.Join(context, "\n")
}

// RunCorrectionLoop executes a gate command, and if it fails, gives an LLM agent
// the error output and lets it fix the issue. Loops until gate passes or maxIterations.
// No regex parsing — the LLM reads the text and figures out what's wrong.
func RunCorrectionLoop(
	ctx context.Context,
	repoRoot string,
	gateCmd string,
	maxIterations int,
	spawner AgentSpawnerFunc,
) (*CorrectionLoopResult, error) {
	if maxIterations <= 0 {
		maxIterations = 3
	}

	result := &CorrectionLoopResult{}

	for iteration := 1; iteration <= maxIterations; iteration++ {
		if err := ctx.Err(); err != nil {
			return result, fmt.Errorf("correction loop cancelled: %w", err)
		}

		result.Iterations = iteration

		// Run the gate command
		gateOutput, gateErr := runTestCmd(repoRoot, gateCmd, 5*time.Minute)
		result.FinalGate = gateOutput

		// Gate passed — we're done
		if gateErr == nil {
			result.Passed = true
			return result, nil
		}

		// Strip ANSI for cleaner output, but give the LLM the full text either way
		cleanOutput := stripANSI(gateOutput)

		// Give the LLM the gate output and let it fix whatever is wrong
		prompt := fmt.Sprintf(`A build/test gate command failed. Fix the errors.

Gate command: %s
Working directory: %s

Error output:
%s

Read the error output, find the file(s) causing the failure, fix them, and verify the fix compiles.
Do not add features or refactor — only fix what the gate output says is broken.`, gateCmd, repoRoot, truncateOutput(cleanOutput, 8000))

		fmt.Fprintf(os.Stderr, "[correction-loop] Iteration %d: gate failed, spawning agent to fix\n", iteration)
		if err := spawner(ctx, repoRoot, prompt); err != nil {
			fmt.Fprintf(os.Stderr, "[correction-loop] Correction agent failed: %v\n", err)
		}
	}

	// Final gate check after last iteration's fixes
	gateOutput, gateErr := runTestCmd(repoRoot, gateCmd, 5*time.Minute)
	result.FinalGate = gateOutput
	if gateErr == nil {
		result.Passed = true
		return result, nil
	}

	return result, fmt.Errorf("correction loop exhausted after %d iterations: %s", maxIterations, truncateOutput(gateOutput, 500))
}

// buildCorrectionPrompt creates a focused prompt for a correction agent.
func buildCorrectionPrompt(c CorrectionEntry, repoRoot string) string {
	var b strings.Builder
	b.WriteString("You are fixing a specific error found by a phase gate test.\n\n")
	b.WriteString("## Error\n")
	b.WriteString(c.Error)
	b.WriteString("\n\n## File to fix\n")
	b.WriteString(filepath.Join(repoRoot, c.File))
	b.WriteString("\n\n## Required fix\n")
	b.WriteString(c.Fix)
	b.WriteString("\n\n## Acceptance criteria\n")
	b.WriteString(c.AcceptanceCriteria)
	b.WriteString("\n\n## Instructions\n")
	b.WriteString("1. Read the file listed above\n")
	b.WriteString("2. Understand the error in context\n")
	b.WriteString("3. Apply the minimal fix that resolves the error\n")
	b.WriteString("4. Do NOT refactor or change unrelated code\n")
	b.WriteString("5. Verify the fix addresses the acceptance criteria\n")
	return b.String()
}

// writeCorrectionReport persists a correction report as YAML.
func writeCorrectionReport(dir string, report CorrectionReport) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("creating corrections directory: %w", err)
	}

	filename := fmt.Sprintf("iteration-%d.yaml", report.Iteration)
	path := filepath.Join(dir, filename)

	data, err := yaml.Marshal(report)
	if err != nil {
		return fmt.Errorf("marshaling correction report: %w", err)
	}

	return os.WriteFile(path, data, 0o644)
}

// truncateOutput returns the last n bytes of a string, prefixed with "..." if truncated.
func truncateOutput(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return "..." + s[len(s)-n:]
}
