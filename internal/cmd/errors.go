package cmd

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
)

// Exit codes for structured CLI error reporting.
const (
	ExitSuccess        = 0
	ExitGeneral        = 1
	ExitUsage          = 2
	ExitDatabase       = 3
	ExitGit            = 4
	ExitPartialFailure = 5
	ExitTotalFailure   = 6
	ExitPreflight      = 7
)

// ErrorCategory classifies errors for AI consumers.
type ErrorCategory string

const (
	CategoryInput     ErrorCategory = "input"
	CategoryTransient ErrorCategory = "transient"
	CategoryPartial   ErrorCategory = "partial"
	CategoryFatal     ErrorCategory = "fatal"
)

// TaskFailure captures per-task failure details for structured output.
type TaskFailure struct {
	TaskID      string `json:"task_id"`
	Role        string `json:"role,omitempty"`
	Title       string `json:"title,omitempty"`
	FailureType string `json:"failure_type,omitempty"`
	Suggestion  string `json:"suggestion,omitempty"`
}

// OrchestraError is a structured error carrying exit code, category,
// recovery suggestions, and per-task failure details.
type OrchestraError struct {
	ExitCode    int           `json:"exit_code"`
	Category    ErrorCategory `json:"category"`
	Summary     string        `json:"error"`
	Detail      string        `json:"detail,omitempty"`
	Suggestions []string      `json:"suggestions,omitempty"`
	FailedTasks []TaskFailure `json:"failed_tasks,omitempty"`
	Cause       error         `json:"-"`
}

func (e *OrchestraError) Error() string {
	return e.Summary
}

func (e *OrchestraError) Unwrap() error {
	return e.Cause
}

// ExitCodeFor extracts the exit code from an error. OrchestraError uses its
// typed code; other errors fall back to heuristic string matching.
func ExitCodeFor(err error) int {
	if err == nil {
		return ExitSuccess
	}
	var oe *OrchestraError
	if errors.As(err, &oe) {
		return oe.ExitCode
	}
	return exitCodeHeuristic(err)
}

// exitCodeHeuristic maps common error substrings to exit codes.
func exitCodeHeuristic(err error) int {
	msg := err.Error()
	switch {
	case strings.Contains(msg, "not a git repository"),
		strings.Contains(msg, "git repo"),
		strings.Contains(msg, "worktree"):
		return ExitGit
	case strings.Contains(msg, "database"),
		strings.Contains(msg, "opening database"),
		strings.Contains(msg, "schema"):
		return ExitDatabase
	case strings.Contains(msg, "spec validation"),
		strings.Contains(msg, "parsing spec"),
		strings.Contains(msg, "required flag"):
		return ExitUsage
	default:
		return ExitGeneral
	}
}

// PrintFormattedError writes a human-readable structured error to w.
// Format: summary → detail → failed tasks → next steps.
func PrintFormattedError(w io.Writer, err error, verbose bool) {
	var oe *OrchestraError
	if !errors.As(err, &oe) {
		// Not an OrchestraError — use raw error with heuristic suggestions.
		fmt.Fprintf(w, "Error: %v\n", err)
		if suggestions := suggestFromRawError(err); len(suggestions) > 0 {
			fmt.Fprintln(w)
			fmt.Fprintln(w, "  Next steps:")
			for i, s := range suggestions {
				fmt.Fprintf(w, "    %d. %s\n", i+1, s)
			}
		}
		if verbose {
			printErrorChain(w, err)
		}
		return
	}

	fmt.Fprintf(w, "Error: %s\n", oe.Summary)

	if oe.Detail != "" {
		fmt.Fprintf(w, "\n  %s\n", oe.Detail)
	}

	if len(oe.FailedTasks) > 0 {
		fmt.Fprintln(w, "\n  Failed tasks:")
		for _, ft := range oe.FailedTasks {
			label := ft.TaskID
			if ft.Role != "" {
				label += " (" + ft.Role + ")"
			}
			fmt.Fprintf(w, "    %s", label)
			if ft.Title != "" {
				fmt.Fprintf(w, ": %s", ft.Title)
			}
			fmt.Fprintln(w)
			if ft.FailureType != "" {
				fmt.Fprintf(w, "      Cause: %s\n", ft.FailureType)
			}
			if ft.Suggestion != "" {
				fmt.Fprintf(w, "      Try: %s\n", ft.Suggestion)
			}
		}
	}

	if len(oe.Suggestions) > 0 {
		fmt.Fprintln(w, "\n  Next steps:")
		for i, s := range oe.Suggestions {
			fmt.Fprintf(w, "    %d. %s\n", i+1, s)
		}
	}

	if verbose {
		printErrorChain(w, oe.Cause)
	}
}

// PrintJSONError writes a JSON-formatted error to w.
func PrintJSONError(w io.Writer, err error) {
	var oe *OrchestraError
	if !errors.As(err, &oe) {
		oe = &OrchestraError{
			ExitCode:    exitCodeHeuristic(err),
			Category:    CategoryFatal,
			Summary:     err.Error(),
			Suggestions: suggestFromRawError(err),
		}
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	_ = enc.Encode(oe)
}

// printErrorChain shows the unwrap chain for verbose mode.
func printErrorChain(w io.Writer, err error) {
	if err == nil {
		return
	}
	fmt.Fprintln(w, "\n  Error chain:")
	for e := err; e != nil; e = errors.Unwrap(e) {
		fmt.Fprintf(w, "    → %v\n", e)
	}
}

// suggestFromRawError provides heuristic recovery suggestions for
// common raw error strings when OrchestraError is not available.
func suggestFromRawError(err error) []string {
	msg := err.Error()
	var suggestions []string

	switch {
	case strings.Contains(msg, "not a git repository"):
		suggestions = append(suggestions,
			`git init && git add -A && git commit -m "Initial commit"`)
	case strings.Contains(msg, "opening database"), strings.Contains(msg, "schema"):
		suggestions = append(suggestions, "orchestra init")
	case strings.Contains(msg, "worktree"):
		suggestions = append(suggestions, "orchestra reset  # clean stale worktrees")
	case strings.Contains(msg, "SQLITE_BUSY"), strings.Contains(msg, "database is locked"):
		suggestions = append(suggestions,
			"orchestra status  # check for running conductors",
			"orchestra reset  # force cleanup if stuck")
	case strings.Contains(msg, "required flag"):
		suggestions = append(suggestions, "orchestra go --help")
	}

	return suggestions
}

// TaskRecoverySuggestion maps agent failure classifier types to recovery commands.
func TaskRecoverySuggestion(failureType string) string {
	switch failureType {
	case "context_exhausted":
		return `orchestra go "same goal" --max-files-per-task 12`
	case "rate_limit":
		return "wait for rate limit reset, then: orchestra recover"
	case "session_limit":
		return "wait for session limit reset, then: orchestra recover"
	case "normal_failure":
		return "orchestra status --json  # inspect failure details"
	default:
		return ""
	}
}
