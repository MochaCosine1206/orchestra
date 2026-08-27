package orchestrator

import (
	"context"
	"database/sql"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/MochaCosine1206/orchestra/internal/db"
)

// AuditScope defines what to scan.
type AuditScope string

const (
	AuditScopeAll   AuditScope = "all"
	AuditScopeTests AuditScope = "tests"
	AuditScopeCode  AuditScope = "code"
	AuditScopeGaps  AuditScope = "gaps"
)

// validAuditScopes lists all recognized scope values.
var validAuditScopes = map[AuditScope]bool{
	AuditScopeAll:   true,
	AuditScopeTests: true,
	AuditScopeCode:  true,
	AuditScopeGaps:  true,
}

// AuditOpts configures an audit run.
type AuditOpts struct {
	Scope  AuditScope
	DryRun bool
}

// AuditFinding represents a single issue found during audit.
type AuditFinding struct {
	Category string // "test_failure", "syntax_error", "todo", "gap"
	File     string
	Line     int
	Message  string
}

// AuditResult captures the full audit output.
type AuditResult struct {
	Findings []AuditFinding
	Summary  string
	TaskIDs  []string // IDs of tasks created (non-dry-run only)
}

// Audit scans the codebase for issues based on scope.
func (c *Conductor) Audit(ctx context.Context, opts AuditOpts) (*AuditResult, error) {
	if !validAuditScopes[opts.Scope] {
		return nil, fmt.Errorf("unknown audit scope %q: must be one of all, tests, code, gaps", opts.Scope)
	}

	result := &AuditResult{}
	var sections []string

	if opts.Scope == AuditScopeAll || opts.Scope == AuditScopeTests {
		findings, summary := c.auditTests(ctx)
		result.Findings = append(result.Findings, findings...)
		sections = append(sections, summary)
	}

	if opts.Scope == AuditScopeAll || opts.Scope == AuditScopeCode {
		findings, summary := c.auditCode(ctx)
		result.Findings = append(result.Findings, findings...)
		sections = append(sections, summary)
	}

	if opts.Scope == AuditScopeAll || opts.Scope == AuditScopeGaps {
		findings, summary := c.auditGaps(ctx)
		result.Findings = append(result.Findings, findings...)
		sections = append(sections, summary)
	}

	result.Summary = strings.Join(sections, "\n")

	if opts.DryRun || len(result.Findings) == 0 {
		return result, nil
	}

	// Non-dry-run: create tasks from findings
	taskIDs, err := c.createTasksFromFindings(ctx, result.Findings)
	if err != nil {
		return result, fmt.Errorf("creating tasks from findings: %w", err)
	}
	result.TaskIDs = taskIDs

	return result, nil
}

// auditTests runs `go vet ./...` and `go test ./...` to find syntax errors and test failures.
func (c *Conductor) auditTests(ctx context.Context) ([]AuditFinding, string) {
	var findings []AuditFinding

	// Run go vet for syntax errors
	vetCmd := exec.CommandContext(ctx, "go", "vet", "./...")
	vetCmd.Dir = c.RepoRoot
	vetOut, vetErr := vetCmd.CombinedOutput()

	if vetErr != nil {
		for _, line := range strings.Split(string(vetOut), "\n") {
			line = strings.TrimSpace(line)
			if line == "" || strings.HasPrefix(line, "#") {
				continue
			}
			// go vet output: file.go:line:col: message
			parts := strings.SplitN(line, ":", 4)
			if len(parts) >= 4 {
				lineNum, _ := strconv.Atoi(parts[1])
				findings = append(findings, AuditFinding{
					Category: "syntax_error",
					File:     parts[0],
					Line:     lineNum,
					Message:  strings.TrimSpace(parts[3]),
				})
			} else {
				findings = append(findings, AuditFinding{
					Category: "syntax_error",
					Message:  line,
				})
			}
		}
	}

	// Run go test for test failures
	testCmd := exec.CommandContext(ctx, "go", "test", "./...", "-count=1")
	testCmd.Dir = c.RepoRoot
	testOut, testErr := testCmd.CombinedOutput()

	if testErr != nil {
		for _, line := range strings.Split(string(testOut), "\n") {
			line = strings.TrimSpace(line)
			if strings.HasPrefix(line, "--- FAIL:") {
				findings = append(findings, AuditFinding{
					Category: "test_failure",
					Message:  line,
				})
			}
			if strings.HasPrefix(line, "FAIL\t") {
				findings = append(findings, AuditFinding{
					Category: "test_failure",
					Message:  line,
				})
			}
		}
	}

	if len(findings) == 0 {
		return nil, "[tests] All tests passing, no syntax errors"
	}
	return findings, fmt.Sprintf("[tests] %d issues found", len(findings))
}

// auditCode scans for TODO/FIXME markers.
func (c *Conductor) auditCode(ctx context.Context) ([]AuditFinding, string) {
	var findings []AuditFinding

	cmd := exec.CommandContext(ctx, "grep", "-rn", "-E", "TODO|FIXME|HACK|XXX",
		"--include=*.go", "--include=*.sh", c.RepoRoot)
	out, err := cmd.CombinedOutput()

	// grep returns exit code 1 for no matches — that's not an error
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok && exitErr.ExitCode() == 1 {
			return nil, "[code] 0 markers found"
		}
		// Real error (e.g., grep not found, permission denied)
		return []AuditFinding{{
			Category: "todo",
			Message:  fmt.Sprintf("grep failed: %v", err),
		}}, "[code] grep scan failed"
	}

	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		// Parse file:line:content from grep -rn output
		parts := strings.SplitN(line, ":", 3)
		if len(parts) >= 3 {
			lineNum, _ := strconv.Atoi(parts[1])
			findings = append(findings, AuditFinding{
				Category: "todo",
				File:     parts[0],
				Line:     lineNum,
				Message:  strings.TrimSpace(parts[2]),
			})
		}
	}

	return findings, fmt.Sprintf("[code] %d markers found", len(findings))
}

// auditGaps scans GAPS.md for open gaps.
func (c *Conductor) auditGaps(ctx context.Context) ([]AuditFinding, string) {
	var findings []AuditFinding

	// Actual GAPS.md format uses **G19: Title** for gap IDs and STATUS: OPEN for open items
	cmd := exec.CommandContext(ctx, "grep", "-n", "-E",
		`\*\*G[0-9]+:.*\*\*|STATUS: OPEN`,
		c.RepoRoot+"/GAPS.md")
	out, err := cmd.CombinedOutput()

	// grep returns exit code 1 for no matches — that's not an error
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok && exitErr.ExitCode() == 1 {
			return nil, "[gaps] 0 open gaps found"
		}
		return []AuditFinding{{
			Category: "gap",
			File:     "GAPS.md",
			Message:  fmt.Sprintf("grep failed: %v", err),
		}}, "[gaps] grep scan failed"
	}

	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		// Parse line_number:content from grep -n output
		parts := strings.SplitN(line, ":", 2)
		if len(parts) >= 2 {
			lineNum, _ := strconv.Atoi(parts[0])
			findings = append(findings, AuditFinding{
				Category: "gap",
				File:     "GAPS.md",
				Line:     lineNum,
				Message:  strings.TrimSpace(parts[1]),
			})
		}
	}

	return findings, fmt.Sprintf("[gaps] %d open gaps found", len(findings))
}

// createTasksFromFindings groups audit findings by category and creates
// one task per category, following the same pattern as Decompose task creation.
func (c *Conductor) createTasksFromFindings(ctx context.Context, findings []AuditFinding) ([]string, error) {
	groups := make(map[string][]AuditFinding)
	for _, f := range findings {
		groups[f.Category] = append(groups[f.Category], f)
	}

	now := time.Now()
	var taskIDs []string

	for category, categoryFindings := range groups {
		taskID := db.GenID("t")
		taskIDs = append(taskIDs, taskID)

		title := auditCategoryTitle(category, len(categoryFindings))
		description := auditCategoryDescription(category, categoryFindings)

		err := c.DB.CreateTask(ctx, db.Task{
			ID:          taskID,
			Title:       title,
			Description: sql.NullString{String: description, Valid: true},
			Status:      "pending",
			Priority:    auditCategoryPriority(category),
			Role:        auditCategoryRole(category),
			BlockedBy:   sql.NullString{String: "[]", Valid: true},
			CreatedAt:   now,
		})
		if err != nil {
			return taskIDs, fmt.Errorf("creating task for %s findings: %w", category, err)
		}

		c.DB.LogEvent(ctx, "audit_task_created", "", taskID,
			fmt.Sprintf(`{"category":"%s","count":%d}`, escapeJSON(category), len(categoryFindings)))
	}

	if err := c.storeSessionTaskIDs(ctx, taskIDs); err != nil {
		return taskIDs, fmt.Errorf("storing session task IDs: %w", err)
	}

	return taskIDs, nil
}

func auditCategoryTitle(category string, count int) string {
	switch category {
	case "test_failure":
		return fmt.Sprintf("Fix %d test failure(s)", count)
	case "syntax_error":
		return fmt.Sprintf("Fix %d syntax error(s)", count)
	case "todo":
		return fmt.Sprintf("Address %d TODO/FIXME marker(s)", count)
	case "gap":
		return fmt.Sprintf("Investigate %d open gap(s)", count)
	default:
		return fmt.Sprintf("Address %d %s finding(s)", count, category)
	}
}

func auditCategoryDescription(category string, findings []AuditFinding) string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Audit found %d %s issue(s):\n\n", len(findings), category))
	for i, f := range findings {
		if i >= 20 {
			sb.WriteString(fmt.Sprintf("\n... and %d more\n", len(findings)-20))
			break
		}
		if f.File != "" {
			if f.Line > 0 {
				sb.WriteString(fmt.Sprintf("- %s:%d: %s\n", f.File, f.Line, f.Message))
			} else {
				sb.WriteString(fmt.Sprintf("- %s: %s\n", f.File, f.Message))
			}
		} else {
			sb.WriteString(fmt.Sprintf("- %s\n", f.Message))
		}
	}
	return sb.String()
}

func auditCategoryRole(category string) string {
	switch category {
	case "test_failure", "syntax_error":
		return "implementer"
	case "gap":
		return "researcher"
	default:
		return "implementer"
	}
}

func auditCategoryPriority(category string) int {
	switch category {
	case "test_failure", "syntax_error":
		return 5
	case "todo":
		return 2
	case "gap":
		return 3
	default:
		return 3
	}
}
