package orchestrator

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// TriageOutcome represents the three-outcome error triage decision.
type TriageOutcome string

const (
	// TriageHeal indicates a small mechanical fix (missing import, typo, wrong dep).
	TriageHeal TriageOutcome = "heal"
	// TriageRefine indicates the approach is mostly right but needs correction.
	TriageRefine TriageOutcome = "refine"
	// TriageRedo indicates the approach is fundamentally wrong — start over.
	TriageRedo TriageOutcome = "redo"
)

// ErrorEnvelope captures a single classified error from build or test output.
type ErrorEnvelope struct {
	TaskID        string        `json:"task_id"`
	ErrorType     string        `json:"error_type"`      // "build_error", "test_failure", "missing_dep", "type_error", "runtime_error", "missing_import", "syntax_error"
	RawOutput     string        `json:"raw_output"`       // full build/test output
	FilesAffected []string      `json:"files_affected"`   // which files have errors
	LineNumbers   []int         `json:"line_numbers"`     // error line numbers if parseable
	Triage        TriageOutcome `json:"triage"`
	FixHint       string        `json:"fix_hint"`         // actionable hint for the agent
}

// --- Regex patterns for various build systems ---

// Go patterns
var (
	goCompileErrorRe  = regexp.MustCompile(`^(.+\.go):(\d+):\d+: (.+)$`)
	goImportErrorRe   = regexp.MustCompile(`could not import|imported and not used|cannot find package|no required module provides`)
	goTypeErrorRe     = regexp.MustCompile(`cannot use .+ as .+ in|incompatible type|undefined:|cannot convert|has no field or method`)
	goSyntaxErrorRe   = regexp.MustCompile(`expected .+, found|syntax error|unexpected`)
	goTestFailRe      = regexp.MustCompile(`^--- FAIL: (\S+)\s`)
	goTestPkgFailRe   = regexp.MustCompile(`^FAIL\s+(\S+)`)
	goTestOutputRe    = regexp.MustCompile(`^\s+(.+\.go):(\d+): (.+)$`)
)

// npm / Node.js patterns
var (
	npmModuleNotFoundRe = regexp.MustCompile(`Cannot find module '([^']+)'|Module not found.*'([^']+)'`)
	npmSyntaxErrorRe    = regexp.MustCompile(`SyntaxError:|Unexpected token`)
	npmTypeErrorRe      = regexp.MustCompile(`TypeError:|is not a function|is not defined|Property .+ does not exist`)
	npmTestFailRe       = regexp.MustCompile(`(?:FAIL|✕|✗|failing)\s+(.+)`)
	npmFileLineRe       = regexp.MustCompile(`at .+[/\\](.+\.(?:js|ts|tsx|jsx)):(\d+)`)
)

// Cargo / Rust patterns
var (
	cargoErrorRe      = regexp.MustCompile(`error\[E\d+\]: (.+)`)
	cargoFileLineRe   = regexp.MustCompile(`--> (.+\.rs):(\d+):\d+`)
	cargoMissingCrate = regexp.MustCompile(`can't find crate|unresolved import`)
	cargoTypeErrorRe  = regexp.MustCompile(`mismatched types|expected .+, found`)
	cargoTestFailRe   = regexp.MustCompile(`test .+ \.\.\. FAILED`)
)

// ClassifyError parses raw build/test output and returns a single ErrorEnvelope
// with the highest-severity triage outcome.
func ClassifyError(rawOutput string) *ErrorEnvelope {
	buildErrs := ParseBuildErrors(rawOutput)
	testErrs := ParseTestFailures(rawOutput)
	all := append(buildErrs, testErrs...)

	if len(all) == 0 {
		return nil
	}

	// Aggregate into a single envelope with overall triage
	env := &ErrorEnvelope{
		RawOutput: rawOutput,
		Triage:    aggregateTriage(all),
	}

	seen := make(map[string]bool)
	for _, e := range all {
		for _, f := range e.FilesAffected {
			if !seen[f] {
				seen[f] = true
				env.FilesAffected = append(env.FilesAffected, f)
			}
		}
		env.LineNumbers = append(env.LineNumbers, e.LineNumbers...)
		if env.ErrorType == "" {
			env.ErrorType = e.ErrorType
		}
		if env.FixHint == "" {
			env.FixHint = e.FixHint
		}
	}

	return env
}

// ParseBuildErrors extracts build-related errors from raw output.
func ParseBuildErrors(output string) []ErrorEnvelope {
	var results []ErrorEnvelope
	lines := strings.Split(output, "\n")

	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		// Go compile errors
		if m := goCompileErrorRe.FindStringSubmatch(line); m != nil {
			file := m[1]
			lineNum, _ := strconv.Atoi(m[2])
			msg := m[3]

			env := ErrorEnvelope{
				RawOutput:     line,
				FilesAffected: []string{file},
				LineNumbers:   []int{lineNum},
			}

			switch {
			case goImportErrorRe.MatchString(msg):
				env.ErrorType = "missing_import"
				env.Triage = TriageHeal
				env.FixHint = fmt.Sprintf("Fix import in %s: %s", file, msg)
			case goSyntaxErrorRe.MatchString(msg):
				env.ErrorType = "syntax_error"
				env.Triage = TriageHeal
				env.FixHint = fmt.Sprintf("Fix syntax error in %s:%d: %s", file, lineNum, msg)
			case goTypeErrorRe.MatchString(msg):
				env.ErrorType = "type_error"
				env.Triage = TriageHeal
				env.FixHint = fmt.Sprintf("Fix type error in %s:%d: %s", file, lineNum, msg)
			default:
				env.ErrorType = "build_error"
				env.Triage = TriageRefine
				env.FixHint = fmt.Sprintf("Build error in %s:%d: %s", file, lineNum, msg)
			}

			results = append(results, env)
			continue
		}

		// npm / Node.js errors
		if npmModuleNotFoundRe.MatchString(line) {
			mod := npmModuleNotFoundRe.FindStringSubmatch(line)
			name := mod[1]
			if name == "" {
				name = mod[2]
			}
			results = append(results, ErrorEnvelope{
				ErrorType: "missing_dep",
				RawOutput: line,
				Triage:    TriageHeal,
				FixHint:   fmt.Sprintf("Install missing module: %s", name),
			})
			continue
		}

		if npmSyntaxErrorRe.MatchString(line) {
			env := ErrorEnvelope{
				ErrorType: "syntax_error",
				RawOutput: line,
				Triage:    TriageHeal,
				FixHint:   "Fix JavaScript/TypeScript syntax error",
			}
			// Try to extract file info from nearby lines
			if m := npmFileLineRe.FindStringSubmatch(line); m != nil {
				env.FilesAffected = []string{m[1]}
				ln, _ := strconv.Atoi(m[2])
				env.LineNumbers = []int{ln}
			}
			results = append(results, env)
			continue
		}

		if npmTypeErrorRe.MatchString(line) {
			env := ErrorEnvelope{
				ErrorType: "type_error",
				RawOutput: line,
				Triage:    TriageHeal,
				FixHint:   "Fix type error: " + line,
			}
			results = append(results, env)
			continue
		}

		// Cargo / Rust errors
		if m := cargoErrorRe.FindStringSubmatch(line); m != nil {
			env := ErrorEnvelope{
				ErrorType: "build_error",
				RawOutput: line,
			}
			msg := m[1]

			switch {
			case cargoMissingCrate.MatchString(msg):
				env.ErrorType = "missing_dep"
				env.Triage = TriageHeal
				env.FixHint = "Add missing crate to Cargo.toml"
			case cargoTypeErrorRe.MatchString(msg):
				env.ErrorType = "type_error"
				env.Triage = TriageHeal
				env.FixHint = fmt.Sprintf("Fix Rust type error: %s", msg)
			default:
				env.Triage = TriageRefine
				env.FixHint = fmt.Sprintf("Fix Rust build error: %s", msg)
			}

			results = append(results, env)
			continue
		}

		// Cargo file:line for context enrichment (attach to previous error)
		if m := cargoFileLineRe.FindStringSubmatch(line); m != nil && len(results) > 0 {
			last := &results[len(results)-1]
			last.FilesAffected = append(last.FilesAffected, m[1])
			ln, _ := strconv.Atoi(m[2])
			last.LineNumbers = append(last.LineNumbers, ln)
		}

		// Go missing dep (go.mod level)
		if strings.Contains(line, "no required module provides package") ||
			strings.Contains(line, "missing go.sum entry") {
			results = append(results, ErrorEnvelope{
				ErrorType: "missing_dep",
				RawOutput: line,
				Triage:    TriageHeal,
				FixHint:   "Run 'go mod tidy' to resolve missing dependency",
			})
		}
	}

	// Apply REDO escalation: >10 errors across >5 files
	results = applyRedoEscalation(results)

	return results
}

// ParseTestFailures extracts test failure errors from raw output.
func ParseTestFailures(output string) []ErrorEnvelope {
	var results []ErrorEnvelope
	lines := strings.Split(output, "\n")

	for i, line := range lines {
		line = strings.TrimSpace(line)

		// Go test failures: --- FAIL: TestName
		if m := goTestFailRe.FindStringSubmatch(line); m != nil {
			env := ErrorEnvelope{
				ErrorType: "test_failure",
				RawOutput: line,
				Triage:    TriageRefine,
				FixHint:   fmt.Sprintf("Test %s failed — fix the implementation or the test", m[1]),
			}

			// Look ahead for file:line info
			for j := i + 1; j < len(lines) && j < i+10; j++ {
				if tm := goTestOutputRe.FindStringSubmatch(strings.TrimSpace(lines[j])); tm != nil {
					env.FilesAffected = append(env.FilesAffected, tm[1])
					ln, _ := strconv.Atoi(tm[2])
					env.LineNumbers = append(env.LineNumbers, ln)
				}
			}

			results = append(results, env)
			continue
		}

		// Go package-level FAIL
		if m := goTestPkgFailRe.FindStringSubmatch(line); m != nil {
			// Only add if we haven't already captured individual test failures for this pkg
			alreadyCovered := false
			for _, r := range results {
				if r.ErrorType == "test_failure" {
					alreadyCovered = true
					break
				}
			}
			if !alreadyCovered {
				results = append(results, ErrorEnvelope{
					ErrorType: "test_failure",
					RawOutput: line,
					Triage:    TriageRefine,
					FixHint:   fmt.Sprintf("Tests failed in package %s", m[1]),
				})
			}
			continue
		}

		// npm test failures
		if npmTestFailRe.MatchString(line) {
			results = append(results, ErrorEnvelope{
				ErrorType: "test_failure",
				RawOutput: line,
				Triage:    TriageRefine,
				FixHint:   "Fix failing test: " + line,
			})
			continue
		}

		// Cargo test failures
		if cargoTestFailRe.MatchString(line) {
			results = append(results, ErrorEnvelope{
				ErrorType: "test_failure",
				RawOutput: line,
				Triage:    TriageRefine,
				FixHint:   "Fix failing Rust test: " + line,
			})
			continue
		}

		// Runtime errors (panic, segfault, etc.)
		if strings.Contains(line, "panic:") || strings.Contains(line, "runtime error:") ||
			strings.Contains(line, "SIGSEGV") || strings.Contains(line, "fatal error:") {
			results = append(results, ErrorEnvelope{
				ErrorType: "runtime_error",
				RawOutput: line,
				Triage:    TriageRefine,
				FixHint:   "Fix runtime error: " + line,
			})
		}
	}

	return results
}

// aggregateTriage determines the overall triage outcome from a list of errors.
// Priority: REDO > REFINE > HEAL.
func aggregateTriage(errors []ErrorEnvelope) TriageOutcome {
	if len(errors) == 0 {
		return TriageHeal
	}

	hasRedo := false
	hasRefine := false

	for _, e := range errors {
		switch e.Triage {
		case TriageRedo:
			hasRedo = true
		case TriageRefine:
			hasRefine = true
		}
	}

	if hasRedo {
		return TriageRedo
	}
	if hasRefine {
		return TriageRefine
	}
	return TriageHeal
}

// applyRedoEscalation checks if the error set warrants escalation to REDO:
// >10 errors across >5 files means the approach is fundamentally wrong.
func applyRedoEscalation(errors []ErrorEnvelope) []ErrorEnvelope {
	if len(errors) <= 10 {
		return errors
	}

	fileSet := make(map[string]bool)
	for _, e := range errors {
		for _, f := range e.FilesAffected {
			fileSet[f] = true
		}
	}

	if len(fileSet) > 5 {
		for i := range errors {
			errors[i].Triage = TriageRedo
		}
	}

	return errors
}
