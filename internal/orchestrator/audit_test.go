package orchestrator

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAudit_DryRun_All(t *testing.T) {
	d := setupTestDB(t)
	c, _ := New(ConductorOpts{DB: d, RepoRoot: "/tmp/test"})
	ctx := context.Background()

	result, err := c.Audit(ctx, AuditOpts{Scope: AuditScopeAll, DryRun: true})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Scans run even in dry-run (against nonexistent dir, may have 0 findings or error findings)
	if result.Summary == "" {
		t.Error("summary should not be empty")
	}
	// Dry-run must never create tasks regardless of findings
	if len(result.TaskIDs) != 0 {
		t.Errorf("dry-run should create no tasks, got %d", len(result.TaskIDs))
	}
	tasks, err := d.ListTasks(ctx)
	if err != nil {
		t.Fatalf("listing tasks: %v", err)
	}
	if len(tasks) != 0 {
		t.Errorf("dry-run should not create tasks in DB, found %d", len(tasks))
	}
}

func TestAudit_DryRun_Tests(t *testing.T) {
	d := setupTestDB(t)
	c, _ := New(ConductorOpts{DB: d, RepoRoot: "/tmp/test"})
	ctx := context.Background()

	result, err := c.Audit(ctx, AuditOpts{Scope: AuditScopeTests, DryRun: true})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Summary == "" {
		t.Error("summary should not be empty")
	}
}

func TestAudit_DryRun_Code(t *testing.T) {
	d := setupTestDB(t)
	c, _ := New(ConductorOpts{DB: d, RepoRoot: "/tmp/test"})
	ctx := context.Background()

	result, err := c.Audit(ctx, AuditOpts{Scope: AuditScopeCode, DryRun: true})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Summary == "" {
		t.Error("summary should not be empty")
	}
}

func TestAudit_DryRun_Gaps(t *testing.T) {
	d := setupTestDB(t)
	c, _ := New(ConductorOpts{DB: d, RepoRoot: "/tmp/test"})
	ctx := context.Background()

	result, err := c.Audit(ctx, AuditOpts{Scope: AuditScopeGaps, DryRun: true})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Summary == "" {
		t.Error("summary should not be empty")
	}
}

func TestAudit_UnknownScope(t *testing.T) {
	d := setupTestDB(t)
	c, _ := New(ConductorOpts{DB: d, RepoRoot: "/tmp/test"})
	ctx := context.Background()

	_, err := c.Audit(ctx, AuditOpts{Scope: "invalid"})
	if err == nil {
		t.Fatal("expected error for unknown scope")
	}
	if !strings.Contains(err.Error(), "unknown audit scope") {
		t.Errorf("error should mention unknown scope, got: %v", err)
	}
}

func TestAudit_EmptyScope(t *testing.T) {
	d := setupTestDB(t)
	c, _ := New(ConductorOpts{DB: d, RepoRoot: "/tmp/test"})
	ctx := context.Background()

	_, err := c.Audit(ctx, AuditOpts{Scope: ""})
	if err == nil {
		t.Fatal("expected error for empty scope")
	}
}

func TestAudit_ScopeFiltering(t *testing.T) {
	d := setupTestDB(t)
	c, _ := New(ConductorOpts{DB: d, RepoRoot: "/tmp/nonexistent"})
	ctx := context.Background()

	// Tests scope should only scan tests (will fail on nonexistent dir, but no crash)
	result, err := c.Audit(ctx, AuditOpts{Scope: AuditScopeTests})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Summary == "" {
		t.Error("summary should not be empty even on failure")
	}
}

func TestAuditScope_Values(t *testing.T) {
	if AuditScopeAll != "all" {
		t.Errorf("AuditScopeAll = %q, want %q", AuditScopeAll, "all")
	}
	if AuditScopeTests != "tests" {
		t.Errorf("AuditScopeTests = %q, want %q", AuditScopeTests, "tests")
	}
	if AuditScopeCode != "code" {
		t.Errorf("AuditScopeCode = %q, want %q", AuditScopeCode, "code")
	}
	if AuditScopeGaps != "gaps" {
		t.Errorf("AuditScopeGaps = %q, want %q", AuditScopeGaps, "gaps")
	}
}

func TestAuditFinding_Structure(t *testing.T) {
	f := AuditFinding{
		Category: "test_failure",
		File:     "foo_test.go",
		Line:     42,
		Message:  "--- FAIL: TestFoo",
	}
	if f.Category != "test_failure" || f.File != "foo_test.go" || f.Line != 42 {
		t.Errorf("unexpected finding structure: %+v", f)
	}
}

func TestAudit_CodeParsesLineNumbers(t *testing.T) {
	dir := t.TempDir()
	err := os.WriteFile(filepath.Join(dir, "example.go"), []byte("package main\n\n// TODO: fix this\nfunc main() {}\n"), 0644)
	if err != nil {
		t.Fatal(err)
	}

	d := setupTestDB(t)
	c, _ := New(ConductorOpts{DB: d, RepoRoot: dir})
	ctx := context.Background()

	result, err := c.Audit(ctx, AuditOpts{Scope: AuditScopeCode, DryRun: true})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(result.Findings) == 0 {
		t.Fatal("expected at least one finding")
	}

	found := false
	for _, f := range result.Findings {
		if f.Line == 3 && strings.Contains(f.Message, "TODO") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected finding with Line=3 and TODO message, got: %+v", result.Findings)
	}
}

func TestAudit_GapsMatchesActualFormat(t *testing.T) {
	dir := t.TempDir()
	gapsContent := `# Research Gaps

**G23: Single-Agent Distillation**
STATUS: OPEN
Some description here.

**G55: SIGPIPE Kills go When Piped**
STATUS: OPEN
Another description.

**G19: Model Fallback Chain**
STATUS: CLOSED (implemented in Phase 4)
Closed gap.
`
	err := os.WriteFile(filepath.Join(dir, "GAPS.md"), []byte(gapsContent), 0644)
	if err != nil {
		t.Fatal(err)
	}

	d := setupTestDB(t)
	c, _ := New(ConductorOpts{DB: d, RepoRoot: dir})
	ctx := context.Background()

	result, err := c.Audit(ctx, AuditOpts{Scope: AuditScopeGaps, DryRun: true})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(result.Findings) == 0 {
		t.Fatal("expected findings from GAPS.md")
	}

	var hasGapHeader, hasStatusOpen bool
	for _, f := range result.Findings {
		if strings.Contains(f.Message, "**G23:") || strings.Contains(f.Message, "**G55:") {
			hasGapHeader = true
		}
		if strings.Contains(f.Message, "STATUS: OPEN") {
			hasStatusOpen = true
		}
		if f.Line == 0 {
			t.Errorf("finding should have a line number, got 0: %+v", f)
		}
	}
	if !hasGapHeader {
		t.Error("expected to find gap header lines (e.g. **G23:)")
	}
	if !hasStatusOpen {
		t.Error("expected to find STATUS: OPEN lines")
	}
}

func TestAudit_GapsNoFile(t *testing.T) {
	dir := t.TempDir()

	d := setupTestDB(t)
	c, _ := New(ConductorOpts{DB: d, RepoRoot: dir})
	ctx := context.Background()

	result, err := c.Audit(ctx, AuditOpts{Scope: AuditScopeGaps, DryRun: true})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	t.Logf("Summary: %s, Findings: %d", result.Summary, len(result.Findings))
}

func TestAudit_CodeNoMatches(t *testing.T) {
	dir := t.TempDir()
	err := os.WriteFile(filepath.Join(dir, "clean.go"), []byte("package main\n\nfunc main() {}\n"), 0644)
	if err != nil {
		t.Fatal(err)
	}

	d := setupTestDB(t)
	c, _ := New(ConductorOpts{DB: d, RepoRoot: dir})
	ctx := context.Background()

	result, err := c.Audit(ctx, AuditOpts{Scope: AuditScopeCode, DryRun: true})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(result.Findings) != 0 {
		t.Errorf("expected no findings in clean file, got %d", len(result.Findings))
	}
	if !strings.Contains(result.Summary, "0 markers found") {
		t.Errorf("summary should say 0 markers, got: %s", result.Summary)
	}
}

func TestAudit_NonDryRunCreatesTasks(t *testing.T) {
	dir := t.TempDir()
	err := os.WriteFile(filepath.Join(dir, "example.go"), []byte("package main\n\n// TODO: fix this\nfunc main() {}\n"), 0644)
	if err != nil {
		t.Fatal(err)
	}

	d := setupTestDB(t)
	c, _ := New(ConductorOpts{DB: d, RepoRoot: dir})
	ctx := context.Background()

	result, err := c.Audit(ctx, AuditOpts{Scope: AuditScopeCode, DryRun: false})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(result.TaskIDs) == 0 {
		t.Fatal("expected tasks to be created in non-dry-run mode")
	}

	tasks, err := d.ListTasks(ctx)
	if err != nil {
		t.Fatalf("listing tasks: %v", err)
	}
	if len(tasks) == 0 {
		t.Fatal("expected task in DB")
	}

	foundTask := false
	for _, task := range tasks {
		if task.ID == result.TaskIDs[0] {
			foundTask = true
			if task.Status != "pending" {
				t.Errorf("task status = %q, want pending", task.Status)
			}
			if task.Role != "implementer" {
				t.Errorf("task role = %q, want implementer", task.Role)
			}
			if !strings.Contains(task.Title, "TODO/FIXME") {
				t.Errorf("task title should mention TODO/FIXME, got: %s", task.Title)
			}
		}
	}
	if !foundTask {
		t.Errorf("task %s not found in DB", result.TaskIDs[0])
	}
}

func TestAudit_DryRunDoesNotCreateTasks(t *testing.T) {
	dir := t.TempDir()
	err := os.WriteFile(filepath.Join(dir, "example.go"), []byte("package main\n\n// TODO: fix this\nfunc main() {}\n"), 0644)
	if err != nil {
		t.Fatal(err)
	}

	d := setupTestDB(t)
	c, _ := New(ConductorOpts{DB: d, RepoRoot: dir})
	ctx := context.Background()

	result, err := c.Audit(ctx, AuditOpts{Scope: AuditScopeCode, DryRun: true})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(result.Findings) == 0 {
		t.Fatal("expected findings")
	}
	if len(result.TaskIDs) != 0 {
		t.Errorf("dry-run should not create tasks, got %d", len(result.TaskIDs))
	}

	tasks, err := d.ListTasks(ctx)
	if err != nil {
		t.Fatalf("listing tasks: %v", err)
	}
	if len(tasks) != 0 {
		t.Errorf("dry-run should not create tasks in DB, found %d", len(tasks))
	}
}

func TestAudit_RealRepo(t *testing.T) {
	d := setupTestDB(t)
	c, _ := New(ConductorOpts{DB: d, RepoRoot: "."})
	ctx := context.Background()

	result, err := c.Audit(ctx, AuditOpts{Scope: AuditScopeCode, DryRun: true})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	t.Logf("Found %d code markers", len(result.Findings))
}

func TestAuditCategoryTitle(t *testing.T) {
	tests := []struct {
		category string
		count    int
		want     string
	}{
		{"test_failure", 3, "Fix 3 test failure(s)"},
		{"syntax_error", 1, "Fix 1 syntax error(s)"},
		{"todo", 5, "Address 5 TODO/FIXME marker(s)"},
		{"gap", 2, "Investigate 2 open gap(s)"},
		{"unknown", 1, "Address 1 unknown finding(s)"},
	}
	for _, tt := range tests {
		got := auditCategoryTitle(tt.category, tt.count)
		if got != tt.want {
			t.Errorf("auditCategoryTitle(%q, %d) = %q, want %q", tt.category, tt.count, got, tt.want)
		}
	}
}

func TestAuditCategoryRole(t *testing.T) {
	tests := []struct {
		category string
		want     string
	}{
		{"test_failure", "implementer"},
		{"syntax_error", "implementer"},
		{"todo", "implementer"},
		{"gap", "researcher"},
		{"unknown", "implementer"},
	}
	for _, tt := range tests {
		got := auditCategoryRole(tt.category)
		if got != tt.want {
			t.Errorf("auditCategoryRole(%q) = %q, want %q", tt.category, got, tt.want)
		}
	}
}

func TestAuditCategoryPriority(t *testing.T) {
	tests := []struct {
		category string
		want     int
	}{
		{"test_failure", 5},
		{"syntax_error", 5},
		{"todo", 2},
		{"gap", 3},
		{"unknown", 3},
	}
	for _, tt := range tests {
		got := auditCategoryPriority(tt.category)
		if got != tt.want {
			t.Errorf("auditCategoryPriority(%q) = %d, want %d", tt.category, got, tt.want)
		}
	}
}

func TestAuditCategoryDescription(t *testing.T) {
	findings := []AuditFinding{
		{Category: "todo", File: "main.go", Line: 10, Message: "TODO: fix"},
		{Category: "todo", File: "other.go", Line: 0, Message: "FIXME: later"},
		{Category: "todo", Message: "bare message"},
	}

	desc := auditCategoryDescription("todo", findings)
	if !strings.Contains(desc, "3 todo issue(s)") {
		t.Errorf("description should mention count, got: %s", desc)
	}
	if !strings.Contains(desc, "main.go:10:") {
		t.Errorf("description should include file:line, got: %s", desc)
	}
	if !strings.Contains(desc, "other.go:") {
		t.Errorf("description should include file without line, got: %s", desc)
	}
	if !strings.Contains(desc, "- bare message") {
		t.Errorf("description should include bare message, got: %s", desc)
	}
}

func TestAuditCategoryDescription_Truncation(t *testing.T) {
	var findings []AuditFinding
	for i := 0; i < 25; i++ {
		findings = append(findings, AuditFinding{
			Category: "todo",
			Message:  "item",
		})
	}

	desc := auditCategoryDescription("todo", findings)
	if !strings.Contains(desc, "... and 5 more") {
		t.Errorf("description should truncate at 20 items, got: %s", desc)
	}
}

func TestValidAuditScopes(t *testing.T) {
	for _, scope := range []AuditScope{AuditScopeAll, AuditScopeTests, AuditScopeCode, AuditScopeGaps} {
		if !validAuditScopes[scope] {
			t.Errorf("expected %q to be valid", scope)
		}
	}
	if validAuditScopes["invalid"] {
		t.Error("expected 'invalid' to be invalid")
	}
}
