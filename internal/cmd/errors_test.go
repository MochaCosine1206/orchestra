package cmd

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
)

func TestOrchestraErrorImplementsError(t *testing.T) {
	oe := &OrchestraError{
		ExitCode: ExitPartialFailure,
		Category: CategoryPartial,
		Summary:  "2 of 5 tasks failed",
	}
	if oe.Error() != "2 of 5 tasks failed" {
		t.Errorf("Error() = %q, want %q", oe.Error(), "2 of 5 tasks failed")
	}
}

func TestOrchestraErrorUnwrap(t *testing.T) {
	cause := errors.New("root cause")
	oe := &OrchestraError{
		ExitCode: ExitGeneral,
		Category: CategoryFatal,
		Summary:  "wrapped",
		Cause:    cause,
	}
	if !errors.Is(oe, cause) {
		t.Error("Unwrap() should allow errors.Is to find the cause")
	}
}

func TestExitCodeForTypedError(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want int
	}{
		{"nil", nil, ExitSuccess},
		{"partial", &OrchestraError{ExitCode: ExitPartialFailure}, ExitPartialFailure},
		{"total", &OrchestraError{ExitCode: ExitTotalFailure}, ExitTotalFailure},
		{"git", &OrchestraError{ExitCode: ExitGit}, ExitGit},
		{"database", &OrchestraError{ExitCode: ExitDatabase}, ExitDatabase},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ExitCodeFor(tt.err)
			if got != tt.want {
				t.Errorf("ExitCodeFor() = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestExitCodeHeuristic(t *testing.T) {
	tests := []struct {
		msg  string
		want int
	}{
		{"not a git repository", ExitGit},
		{"failed creating worktree", ExitGit},
		{"opening database: file not found", ExitDatabase},
		{"schema migration failed", ExitDatabase},
		{"spec validation failed: missing phases", ExitUsage},
		{"required flag \"goal\" not set", ExitUsage},
		{"some unknown error", ExitGeneral},
	}
	for _, tt := range tests {
		t.Run(tt.msg, func(t *testing.T) {
			got := ExitCodeFor(errors.New(tt.msg))
			if got != tt.want {
				t.Errorf("ExitCodeFor(%q) = %d, want %d", tt.msg, got, tt.want)
			}
		})
	}
}

func TestPrintFormattedErrorBasic(t *testing.T) {
	oe := &OrchestraError{
		ExitCode:    ExitGit,
		Category:    CategoryInput,
		Summary:     "not a git repository: /tmp/foo",
		Suggestions: []string{`git init && git add -A && git commit -m "Initial commit"`},
	}
	var buf bytes.Buffer
	PrintFormattedError(&buf, oe, false)
	out := buf.String()

	if !strings.Contains(out, "Error: not a git repository") {
		t.Errorf("missing summary in output:\n%s", out)
	}
	if !strings.Contains(out, "git init") {
		t.Errorf("missing suggestion in output:\n%s", out)
	}
}

func TestPrintFormattedErrorWithTasks(t *testing.T) {
	oe := &OrchestraError{
		ExitCode: ExitPartialFailure,
		Category: CategoryPartial,
		Summary:  "2 of 5 tasks failed",
		FailedTasks: []TaskFailure{
			{TaskID: "t-001", Role: "implementer", Title: "Add auth", FailureType: "context_exhausted", Suggestion: "reduce files"},
			{TaskID: "t-002", Role: "implementer", Title: "Wire DB", FailureType: "normal_failure"},
		},
		Suggestions: []string{"orchestra status --json", "orchestra reset"},
	}
	var buf bytes.Buffer
	PrintFormattedError(&buf, oe, false)
	out := buf.String()

	if !strings.Contains(out, "t-001 (implementer): Add auth") {
		t.Errorf("missing first task in output:\n%s", out)
	}
	if !strings.Contains(out, "Cause: context_exhausted") {
		t.Errorf("missing failure type in output:\n%s", out)
	}
	if !strings.Contains(out, "Try: reduce files") {
		t.Errorf("missing task suggestion in output:\n%s", out)
	}
	if !strings.Contains(out, "orchestra reset") {
		t.Errorf("missing next steps in output:\n%s", out)
	}
}

func TestPrintFormattedErrorVerbose(t *testing.T) {
	inner := errors.New("root cause")
	mid := fmt.Errorf("middle: %w", inner)
	oe := &OrchestraError{
		ExitCode: ExitGeneral,
		Category: CategoryFatal,
		Summary:  "something broke",
		Cause:    mid,
	}
	var buf bytes.Buffer
	PrintFormattedError(&buf, oe, true)
	out := buf.String()

	if !strings.Contains(out, "Error chain:") {
		t.Errorf("verbose mode should show error chain:\n%s", out)
	}
	if !strings.Contains(out, "root cause") {
		t.Errorf("verbose mode should show root cause:\n%s", out)
	}
}

func TestPrintFormattedErrorRawError(t *testing.T) {
	err := errors.New("not a git repository: /tmp/foo")
	var buf bytes.Buffer
	PrintFormattedError(&buf, err, false)
	out := buf.String()

	if !strings.Contains(out, "Error: not a git repository") {
		t.Errorf("should print raw error:\n%s", out)
	}
	if !strings.Contains(out, "git init") {
		t.Errorf("should include heuristic suggestion:\n%s", out)
	}
}

func TestPrintJSONError(t *testing.T) {
	oe := &OrchestraError{
		ExitCode:    ExitPartialFailure,
		Category:    CategoryPartial,
		Summary:     "2 of 5 tasks failed",
		Suggestions: []string{"orchestra status --json"},
		FailedTasks: []TaskFailure{
			{TaskID: "t-001", FailureType: "context_exhausted"},
		},
	}
	var buf bytes.Buffer
	PrintJSONError(&buf, oe)

	var parsed map[string]interface{}
	if err := json.Unmarshal(buf.Bytes(), &parsed); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, buf.String())
	}
	if parsed["error"] != "2 of 5 tasks failed" {
		t.Errorf("JSON error field = %v", parsed["error"])
	}
	if int(parsed["exit_code"].(float64)) != ExitPartialFailure {
		t.Errorf("JSON exit_code = %v", parsed["exit_code"])
	}
	if parsed["category"] != "partial" {
		t.Errorf("JSON category = %v", parsed["category"])
	}
	tasks := parsed["failed_tasks"].([]interface{})
	if len(tasks) != 1 {
		t.Errorf("JSON failed_tasks count = %d, want 1", len(tasks))
	}
}

func TestPrintJSONErrorRawError(t *testing.T) {
	err := errors.New("opening database: file not found")
	var buf bytes.Buffer
	PrintJSONError(&buf, err)

	var parsed map[string]interface{}
	if err := json.Unmarshal(buf.Bytes(), &parsed); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, buf.String())
	}
	if int(parsed["exit_code"].(float64)) != ExitDatabase {
		t.Errorf("heuristic exit_code = %v, want %d", parsed["exit_code"], ExitDatabase)
	}
}

func TestSuggestFromRawError(t *testing.T) {
	tests := []struct {
		msg      string
		contains string
	}{
		{"not a git repository", "git init"},
		{"opening database: no such file", "orchestra init"},
		{"stale worktree detected", "orchestra reset"},
		{"SQLITE_BUSY: database is locked", "orchestra status"},
		{"required flag \"goal\" not set", "orchestra go --help"},
	}
	for _, tt := range tests {
		t.Run(tt.msg, func(t *testing.T) {
			suggestions := suggestFromRawError(errors.New(tt.msg))
			found := false
			for _, s := range suggestions {
				if strings.Contains(s, tt.contains) {
					found = true
					break
				}
			}
			if !found {
				t.Errorf("suggestFromRawError(%q) should contain %q, got %v", tt.msg, tt.contains, suggestions)
			}
		})
	}
}

func TestTaskRecoverySuggestion(t *testing.T) {
	tests := []struct {
		failureType string
		contains    string
	}{
		{"context_exhausted", "max-files-per-task"},
		{"rate_limit", "recover"},
		{"session_limit", "recover"},
		{"normal_failure", "status --json"},
		{"unknown_type", ""},
	}
	for _, tt := range tests {
		t.Run(tt.failureType, func(t *testing.T) {
			got := TaskRecoverySuggestion(tt.failureType)
			if tt.contains == "" && got != "" {
				t.Errorf("TaskRecoverySuggestion(%q) = %q, want empty", tt.failureType, got)
			}
			if tt.contains != "" && !strings.Contains(got, tt.contains) {
				t.Errorf("TaskRecoverySuggestion(%q) = %q, want containing %q", tt.failureType, got, tt.contains)
			}
		})
	}
}
