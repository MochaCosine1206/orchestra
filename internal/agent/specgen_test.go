package agent

import (
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MochaCosine1206/orchestra/internal/db"
)

func setupSpecGenTest(t *testing.T) (*db.DB, string) {
	t.Helper()
	d := setupTestDB(t)
	repoRoot := t.TempDir()

	// Create agent definition files
	agentDir := filepath.Join(repoRoot, ".orchestra", "agents")
	os.MkdirAll(agentDir, 0o755)
	os.WriteFile(filepath.Join(agentDir, "implementer.md"), []byte("---\nname: Implementer\nmodel: opus\n---\nYou are an implementer."), 0o644)
	os.WriteFile(filepath.Join(agentDir, "researcher.md"), []byte("---\nname: Researcher\nmodel: opus\n---\nYou are a researcher."), 0o644)
	os.WriteFile(filepath.Join(agentDir, "architect.md"), []byte("---\nname: Architect\nmodel: opus\n---\nYou are an architect."), 0o644)
	os.WriteFile(filepath.Join(agentDir, "scout.md"), []byte("---\nname: Scout\nmodel: opus\n---\nYou are a scout."), 0o644)
	os.WriteFile(filepath.Join(agentDir, "reviewer.md"), []byte("---\nname: Reviewer\nmodel: opus\n---\nYou are a reviewer."), 0o644)
	os.WriteFile(filepath.Join(agentDir, "illustrator.md"), []byte("---\nname: Illustrator\nmodel: opus\n---\nYou are an illustrator."), 0o644)

	return d, repoRoot
}

// 1. Basic spec generation
func TestGenerateSpecBasic(t *testing.T) {
	d, repoRoot := setupSpecGenTest(t)
	ctx := context.Background()

	d.CreateTask(ctx, db.Task{
		ID: "t1", Title: "Test Task", Status: "pending", Role: "implementer",
		Description: sql.NullString{String: "Do the thing.", Valid: true},
	})

	spec, err := GenerateSpec(ctx, SpecGenOpts{DB: d, RepoRoot: repoRoot, TaskID: "t1"})
	if err != nil {
		t.Fatalf("GenerateSpec: %v", err)
	}
	if !strings.HasPrefix(spec, "---\n") {
		t.Error("spec should start with YAML frontmatter")
	}
	if !strings.Contains(spec, "# Task Specification: t1") {
		t.Error("missing header")
	}
	if !strings.Contains(spec, "## Overview") {
		t.Error("missing overview")
	}
	if !strings.Contains(spec, "## Description") {
		t.Error("missing description")
	}
	if !strings.Contains(spec, "## Delivery") {
		t.Error("missing delivery")
	}
}

// 2. Task not found
func TestGenerateSpecTaskNotFound(t *testing.T) {
	d, repoRoot := setupSpecGenTest(t)
	ctx := context.Background()

	_, err := GenerateSpec(ctx, SpecGenOpts{DB: d, RepoRoot: repoRoot, TaskID: "nonexistent"})
	if err == nil {
		t.Error("expected error for nonexistent task")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("expected 'not found' in error, got: %v", err)
	}
}

// 3. Overview fields present
func TestGenerateSpecOverviewFields(t *testing.T) {
	d, repoRoot := setupSpecGenTest(t)
	ctx := context.Background()

	d.CreateTask(ctx, db.Task{
		ID: "t1", Title: "My Title", Status: "pending", Role: "implementer",
		Priority:  3,
		DependsOn: sql.NullString{String: `["t0"]`, Valid: true},
		BlockedBy: sql.NullString{String: `["t0"]`, Valid: true},
		Worktree:  sql.NullString{String: ".worktree/t1", Valid: true},
		Branch:    sql.NullString{String: "feature/t1", Valid: true},
	})
	// Add predecessor task so predecessor section works
	d.CreateTask(ctx, db.Task{ID: "t0", Title: "Prereq", Status: "done", Role: "implementer"})

	spec, err := GenerateSpec(ctx, SpecGenOpts{DB: d, RepoRoot: repoRoot, TaskID: "t1"})
	if err != nil {
		t.Fatalf("GenerateSpec: %v", err)
	}

	checks := []string{
		"| **Task ID** | `t1` |",
		"| **Title** | My Title |",
		"| **Assigned Role** | implementer |",
		"| **Priority** | 3 |",
		`["t0"]`,
		"| **Worktree** | .worktree/t1 |",
		"| **Branch** | feature/t1 |",
	}
	for _, c := range checks {
		if !strings.Contains(spec, c) {
			t.Errorf("missing overview field: %s", c)
		}
	}
}

// 4. Description present
func TestGenerateSpecDescription(t *testing.T) {
	d, repoRoot := setupSpecGenTest(t)
	ctx := context.Background()

	d.CreateTask(ctx, db.Task{
		ID: "t1", Title: "Test", Status: "pending", Role: "implementer",
		Description: sql.NullString{String: "Implement the frobnicator.", Valid: true},
	})

	spec, _ := GenerateSpec(ctx, SpecGenOpts{DB: d, RepoRoot: repoRoot, TaskID: "t1"})
	if !strings.Contains(spec, "## Description") {
		t.Error("missing description section")
	}
	if !strings.Contains(spec, "Implement the frobnicator.") {
		t.Error("description content missing")
	}
}

// 5. Empty description fallback
func TestGenerateSpecDescriptionEmpty(t *testing.T) {
	d, repoRoot := setupSpecGenTest(t)
	ctx := context.Background()

	d.CreateTask(ctx, db.Task{
		ID: "t1", Title: "Test", Status: "pending", Role: "implementer",
	})

	spec, _ := GenerateSpec(ctx, SpecGenOpts{DB: d, RepoRoot: repoRoot, TaskID: "t1"})
	if !strings.Contains(spec, "No description provided.") {
		t.Error("expected fallback description")
	}
}

// 6. Description truncation
func TestGenerateSpecDescriptionTruncation(t *testing.T) {
	d, repoRoot := setupSpecGenTest(t)
	ctx := context.Background()

	longDesc := strings.Repeat("x", 4000)
	d.CreateTask(ctx, db.Task{
		ID: "t1", Title: "Test", Status: "pending", Role: "implementer",
		Description: sql.NullString{String: longDesc, Valid: true},
	})

	spec, _ := GenerateSpec(ctx, SpecGenOpts{DB: d, RepoRoot: repoRoot, TaskID: "t1"})
	if !strings.Contains(spec, "[TRUNCATED") {
		t.Error("expected truncation marker")
	}
	// Verify event was logged
	events, _ := d.RecentEvents(ctx, 10)
	found := false
	for _, e := range events {
		if e.EventType == "spec_truncated" {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected spec_truncated event")
	}
}

// 7-12. Retry section tests
func TestGenerateSpecRetryNoChanges(t *testing.T) {
	d, repoRoot := setupSpecGenTest(t)
	ctx := context.Background()

	d.CreateTask(ctx, db.Task{ID: "t1", Title: "Test", Status: "pending", Role: "implementer"})
	d.SetBlackboard(ctx, "retry:t1", "1", "test")
	d.SetBlackboard(ctx, "last_failure:t1", "implementer_no_changes", "test")

	spec, _ := GenerateSpec(ctx, SpecGenOpts{DB: d, RepoRoot: repoRoot, TaskID: "t1"})
	if !strings.Contains(spec, "## Previous Attempt Failed") {
		t.Error("missing retry section")
	}
	if !strings.Contains(spec, "without producing any committed file changes") {
		t.Error("missing no_changes guidance")
	}
}

func TestGenerateSpecRetryNoCommits(t *testing.T) {
	d, repoRoot := setupSpecGenTest(t)
	ctx := context.Background()

	d.CreateTask(ctx, db.Task{ID: "t1", Title: "Test", Status: "pending", Role: "researcher"})
	d.SetBlackboard(ctx, "retry:t1", "2", "test")
	d.SetBlackboard(ctx, "last_failure:t1", "researcher_no_commits", "test")

	spec, _ := GenerateSpec(ctx, SpecGenOpts{DB: d, RepoRoot: repoRoot, TaskID: "t1"})
	if !strings.Contains(spec, "without committing any work") {
		t.Error("missing no_commits guidance")
	}
}

func TestGenerateSpecRetryScout(t *testing.T) {
	d, repoRoot := setupSpecGenTest(t)
	ctx := context.Background()

	d.CreateTask(ctx, db.Task{ID: "t1", Title: "Test", Status: "pending", Role: "scout"})
	d.SetBlackboard(ctx, "retry:t1", "1", "test")
	d.SetBlackboard(ctx, "last_failure:t1", "scout_empty_result", "test")

	spec, _ := GenerateSpec(ctx, SpecGenOpts{DB: d, RepoRoot: repoRoot, TaskID: "t1"})
	if !strings.Contains(spec, "no substantial output") {
		t.Error("missing scout guidance")
	}
}

func TestGenerateSpecRetryReviewer(t *testing.T) {
	d, repoRoot := setupSpecGenTest(t)
	ctx := context.Background()

	d.CreateTask(ctx, db.Task{ID: "t1", Title: "Test", Status: "pending", Role: "reviewer"})
	d.SetBlackboard(ctx, "retry:t1", "1", "test")
	d.SetBlackboard(ctx, "last_failure:t1", "reviewer_no_output", "test")

	spec, _ := GenerateSpec(ctx, SpecGenOpts{DB: d, RepoRoot: repoRoot, TaskID: "t1"})
	if !strings.Contains(spec, "no review output") {
		t.Error("missing reviewer guidance")
	}
}

func TestGenerateSpecRetryGeneric(t *testing.T) {
	d, repoRoot := setupSpecGenTest(t)
	ctx := context.Background()

	d.CreateTask(ctx, db.Task{ID: "t1", Title: "Test", Status: "pending", Role: "implementer"})
	d.SetBlackboard(ctx, "retry:t1", "1", "test")
	d.SetBlackboard(ctx, "last_failure:t1", "some_other_failure", "test")

	spec, _ := GenerateSpec(ctx, SpecGenOpts{DB: d, RepoRoot: repoRoot, TaskID: "t1"})
	if !strings.Contains(spec, "Review the failure reason above") {
		t.Error("missing generic retry guidance")
	}
}

func TestGenerateSpecRetryZeroSkip(t *testing.T) {
	d, repoRoot := setupSpecGenTest(t)
	ctx := context.Background()

	d.CreateTask(ctx, db.Task{ID: "t1", Title: "Test", Status: "pending", Role: "implementer"})
	d.SetBlackboard(ctx, "retry:t1", "0", "test")

	spec, _ := GenerateSpec(ctx, SpecGenOpts{DB: d, RepoRoot: repoRoot, TaskID: "t1"})
	if strings.Contains(spec, "## Previous Attempt Failed") {
		t.Error("retry section should not appear for retry=0")
	}
}

// 13-14. File locks tests
func TestGenerateSpecFileLocksPopulated(t *testing.T) {
	d, repoRoot := setupSpecGenTest(t)
	ctx := context.Background()

	d.RegisterAgent(ctx, db.Agent{ID: "a1", Role: "implementer", Status: "active"})
	d.CreateTask(ctx, db.Task{ID: "t1", Title: "Test", Status: "pending", Role: "implementer"})
	d.CreateFileLock(ctx, "src/main.go", "a1", "t1", sql.NullTime{}.Time)
	d.CreateFileLock(ctx, "src/util.go", "a1", "t1", sql.NullTime{}.Time)

	spec, _ := GenerateSpec(ctx, SpecGenOpts{DB: d, RepoRoot: repoRoot, TaskID: "t1"})
	if !strings.Contains(spec, "`src/main.go`") {
		t.Error("missing file lock src/main.go")
	}
	if !strings.Contains(spec, "`src/util.go`") {
		t.Error("missing file lock src/util.go")
	}
}

func TestGenerateSpecFileLocksEmpty(t *testing.T) {
	d, repoRoot := setupSpecGenTest(t)
	ctx := context.Background()

	d.CreateTask(ctx, db.Task{ID: "t1", Title: "Test", Status: "pending", Role: "implementer"})

	spec, _ := GenerateSpec(ctx, SpecGenOpts{DB: d, RepoRoot: repoRoot, TaskID: "t1"})
	if !strings.Contains(spec, "no files locked yet") {
		t.Error("expected empty lock fallback")
	}
}

// 15-16. Schema tests
func TestGenerateSpecSchemaPresent(t *testing.T) {
	d, repoRoot := setupSpecGenTest(t)
	ctx := context.Background()

	d.CreateTask(ctx, db.Task{ID: "t1", Title: "Test", Status: "pending", Role: "implementer"})
	d.SetBlackboard(ctx, "schema:t1", "CREATE TABLE tasks (id TEXT PRIMARY KEY);", "test")

	spec, _ := GenerateSpec(ctx, SpecGenOpts{DB: d, RepoRoot: repoRoot, TaskID: "t1"})
	if !strings.Contains(spec, "CREATE TABLE tasks") {
		t.Error("schema not rendered")
	}
	if strings.Contains(spec, "INSUFFICIENT_SCHEMA") {
		t.Error("warning should not appear when schema exists")
	}
}

func TestGenerateSpecSchemaMissing(t *testing.T) {
	d, repoRoot := setupSpecGenTest(t)
	ctx := context.Background()

	d.CreateTask(ctx, db.Task{ID: "t1", Title: "Test", Status: "pending", Role: "implementer"})

	spec, _ := GenerateSpec(ctx, SpecGenOpts{DB: d, RepoRoot: repoRoot, TaskID: "t1"})
	if !strings.Contains(spec, "INSUFFICIENT_SCHEMA") {
		t.Error("expected schema warning")
	}
}

// 17-18. Contract tests
func TestGenerateSpecContractsPresent(t *testing.T) {
	d, repoRoot := setupSpecGenTest(t)
	ctx := context.Background()

	d.CreateTask(ctx, db.Task{ID: "t1", Title: "Test", Status: "pending", Role: "implementer"})
	d.SetBlackboard(ctx, "contract:t1", "Must export FrobAPI() function.", "test")

	spec, _ := GenerateSpec(ctx, SpecGenOpts{DB: d, RepoRoot: repoRoot, TaskID: "t1"})
	if !strings.Contains(spec, "Must export FrobAPI() function.") {
		t.Error("contract not rendered")
	}
}

func TestGenerateSpecContractsSelfContained(t *testing.T) {
	d, repoRoot := setupSpecGenTest(t)
	ctx := context.Background()

	d.CreateTask(ctx, db.Task{ID: "t1", Title: "Test", Status: "pending", Role: "implementer"})

	spec, _ := GenerateSpec(ctx, SpecGenOpts{DB: d, RepoRoot: repoRoot, TaskID: "t1"})
	if !strings.Contains(spec, "self-contained") {
		t.Error("expected self-contained fallback")
	}
}

// 19-24. Predecessor tests
func TestGenerateSpecPredecessorSingle(t *testing.T) {
	d, repoRoot := setupSpecGenTest(t)
	ctx := context.Background()

	d.CreateTask(ctx, db.Task{ID: "t0", Title: "Prereq Task", Status: "done", Role: "implementer"})
	d.CreateTask(ctx, db.Task{
		ID: "t1", Title: "Test", Status: "pending", Role: "implementer",
		BlockedBy: sql.NullString{String: `["t0"]`, Valid: true},
	})
	d.SetBlackboard(ctx, "result_summary:t0", "Changed 3 files, added API endpoint.", "test")

	spec, _ := GenerateSpec(ctx, SpecGenOpts{DB: d, RepoRoot: repoRoot, TaskID: "t1"})
	if !strings.Contains(spec, "## Predecessor Results") {
		t.Error("missing predecessor section")
	}
	if !strings.Contains(spec, "Prereq Task") {
		t.Error("missing predecessor title")
	}
	if !strings.Contains(spec, "Changed 3 files") {
		t.Error("missing predecessor summary")
	}
}

func TestGenerateSpecPredecessorMultiple(t *testing.T) {
	d, repoRoot := setupSpecGenTest(t)
	ctx := context.Background()

	d.CreateTask(ctx, db.Task{ID: "t0", Title: "First", Status: "done", Role: "implementer"})
	d.CreateTask(ctx, db.Task{ID: "t00", Title: "Second", Status: "done", Role: "scout"})
	d.CreateTask(ctx, db.Task{
		ID: "t1", Title: "Test", Status: "pending", Role: "implementer",
		BlockedBy: sql.NullString{String: `["t0","t00"]`, Valid: true},
	})
	d.SetBlackboard(ctx, "result_summary:t0", "Result A", "test")
	d.SetBlackboard(ctx, "result_summary:t00", "Result B", "test")

	spec, _ := GenerateSpec(ctx, SpecGenOpts{DB: d, RepoRoot: repoRoot, TaskID: "t1"})
	if !strings.Contains(spec, "Result A") || !strings.Contains(spec, "Result B") {
		t.Error("missing one or both predecessor summaries")
	}
}

func TestGenerateSpecPredecessorNoSummary(t *testing.T) {
	d, repoRoot := setupSpecGenTest(t)
	ctx := context.Background()

	d.CreateTask(ctx, db.Task{ID: "t0", Title: "Prereq", Status: "done", Role: "implementer"})
	d.CreateTask(ctx, db.Task{
		ID: "t1", Title: "Test", Status: "pending", Role: "implementer",
		BlockedBy: sql.NullString{String: `["t0"]`, Valid: true},
	})

	spec, _ := GenerateSpec(ctx, SpecGenOpts{DB: d, RepoRoot: repoRoot, TaskID: "t1"})
	if !strings.Contains(spec, "No result summary available") {
		t.Error("missing no-summary fallback")
	}
}

func TestGenerateSpecPredecessorTruncation(t *testing.T) {
	d, repoRoot := setupSpecGenTest(t)
	ctx := context.Background()

	d.CreateTask(ctx, db.Task{ID: "t0", Title: "Prereq", Status: "done", Role: "implementer"})
	d.CreateTask(ctx, db.Task{
		ID: "t1", Title: "Test", Status: "pending", Role: "implementer",
		BlockedBy: sql.NullString{String: `["t0"]`, Valid: true},
	})
	d.SetBlackboard(ctx, "result_summary:t0", strings.Repeat("y", 2000), "test")

	spec, _ := GenerateSpec(ctx, SpecGenOpts{DB: d, RepoRoot: repoRoot, TaskID: "t1"})
	lower := strings.ToLower(spec)
	if !strings.Contains(lower, "truncat") {
		t.Error("expected truncation marker in predecessor result")
	}
}

func TestGenerateSpecPredecessorSectionCap(t *testing.T) {
	d, repoRoot := setupSpecGenTest(t)
	ctx := context.Background()

	// Create many predecessors with summaries to exceed 1500 chars total
	var ids []string
	for i := 0; i < 5; i++ {
		id := "p" + string(rune('0'+i))
		d.CreateTask(ctx, db.Task{ID: id, Title: "Pred " + id, Status: "done", Role: "implementer"})
		d.SetBlackboard(ctx, "result_summary:"+id, strings.Repeat("z", 400), "test")
		ids = append(ids, `"`+id+`"`)
	}
	blockedBy := "[" + strings.Join(ids, ",") + "]"
	d.CreateTask(ctx, db.Task{
		ID: "t1", Title: "Test", Status: "pending", Role: "implementer",
		BlockedBy: sql.NullString{String: blockedBy, Valid: true},
	})

	spec, _ := GenerateSpec(ctx, SpecGenOpts{DB: d, RepoRoot: repoRoot, TaskID: "t1"})
	if !strings.Contains(spec, "TRUNCATED") {
		t.Error("expected section-level truncation")
	}
}

func TestGenerateSpecPredecessorEmptyBlockedBy(t *testing.T) {
	d, repoRoot := setupSpecGenTest(t)
	ctx := context.Background()

	// Various empty/null blocked_by values
	for _, val := range []sql.NullString{
		{Valid: false},
		{String: "[]", Valid: true},
		{String: "null", Valid: true},
		{String: "", Valid: true},
	} {
		d.CreateTask(ctx, db.Task{
			ID: "t-" + val.String, Title: "Test", Status: "pending", Role: "implementer",
			BlockedBy: val,
		})
		spec, _ := GenerateSpec(ctx, SpecGenOpts{DB: d, RepoRoot: repoRoot, TaskID: "t-" + val.String})
		if strings.Contains(spec, "## Predecessor Results") {
			t.Errorf("predecessor section should not appear for blocked_by=%v", val)
		}
	}
}

// 25-27. Quality section tests
func TestGenerateSpecQualityResearcher(t *testing.T) {
	d, repoRoot := setupSpecGenTest(t)
	ctx := context.Background()

	d.CreateTask(ctx, db.Task{ID: "t1", Title: "Test", Status: "pending", Role: "researcher"})

	spec, _ := GenerateSpec(ctx, SpecGenOpts{DB: d, RepoRoot: repoRoot, TaskID: "t1"})
	if !strings.Contains(spec, "## Quality Standards") {
		t.Error("expected quality section for researcher")
	}
	if !strings.Contains(spec, "Methodology") {
		t.Error("missing quality dimension")
	}
}

func TestGenerateSpecQualityArchitect(t *testing.T) {
	d, repoRoot := setupSpecGenTest(t)
	ctx := context.Background()

	d.CreateTask(ctx, db.Task{ID: "t1", Title: "Test", Status: "pending", Role: "architect"})

	spec, _ := GenerateSpec(ctx, SpecGenOpts{DB: d, RepoRoot: repoRoot, TaskID: "t1"})
	if !strings.Contains(spec, "## Quality Standards") {
		t.Error("expected quality section for architect")
	}
}

func TestGenerateSpecQualityImplementerAbsent(t *testing.T) {
	d, repoRoot := setupSpecGenTest(t)
	ctx := context.Background()

	d.CreateTask(ctx, db.Task{ID: "t1", Title: "Test", Status: "pending", Role: "implementer"})

	spec, _ := GenerateSpec(ctx, SpecGenOpts{DB: d, RepoRoot: repoRoot, TaskID: "t1"})
	if strings.Contains(spec, "## Quality Standards") {
		t.Error("quality section should not appear for implementer")
	}
}

// 28. Size warning
func TestGenerateSpecSizeWarning(t *testing.T) {
	d, repoRoot := setupSpecGenTest(t)
	ctx := context.Background()

	// Create a task with a long description (under 3000 so it's not truncated)
	// plus schema + contracts to push total >5000
	d.CreateTask(ctx, db.Task{
		ID: "t1", Title: "Test", Status: "pending", Role: "implementer",
		Description: sql.NullString{String: strings.Repeat("w", 2500), Valid: true},
	})
	d.SetBlackboard(ctx, "schema:t1", strings.Repeat("CREATE TABLE x;", 100), "test")
	d.SetBlackboard(ctx, "contract:t1", strings.Repeat("Must do thing. ", 100), "test")

	GenerateSpec(ctx, SpecGenOpts{DB: d, RepoRoot: repoRoot, TaskID: "t1"})

	events, _ := d.RecentEvents(ctx, 20)
	found := false
	for _, e := range events {
		if e.EventType == "spec_size_warning" {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected spec_size_warning event")
	}
}

// 29-30. Base branch tests
func TestGenerateSpecBaseBranchFromBlackboard(t *testing.T) {
	d, repoRoot := setupSpecGenTest(t)
	ctx := context.Background()

	d.CreateTask(ctx, db.Task{ID: "t1", Title: "Test", Status: "pending", Role: "implementer"})
	d.SetBlackboard(ctx, "base_branch", "main", "test")

	spec, _ := GenerateSpec(ctx, SpecGenOpts{DB: d, RepoRoot: repoRoot, TaskID: "t1"})
	if !strings.Contains(spec, "commits ahead of main") {
		t.Error("expected base branch 'main' in delivery section")
	}
}

func TestGenerateSpecBaseBranchDefault(t *testing.T) {
	d, repoRoot := setupSpecGenTest(t)
	ctx := context.Background()

	d.CreateTask(ctx, db.Task{ID: "t1", Title: "Test", Status: "pending", Role: "implementer"})

	spec, _ := GenerateSpec(ctx, SpecGenOpts{DB: d, RepoRoot: repoRoot, TaskID: "t1"})
	if !strings.Contains(spec, "commits ahead of dev") {
		t.Error("expected default base branch 'dev' in delivery section")
	}
}

// 31-32. Role resolution tests
func TestGenerateSpecRoleFromAgent(t *testing.T) {
	d, repoRoot := setupSpecGenTest(t)
	ctx := context.Background()

	d.RegisterAgent(ctx, db.Agent{ID: "a1", Role: "researcher", Status: "active"})
	d.CreateTask(ctx, db.Task{
		ID: "t1", Title: "Test", Status: "pending", Role: "implementer",
		AssignedTo: sql.NullString{String: "a1", Valid: true},
	})
	d.AssignTask(ctx, "t1", "a1", ".worktree/t1", "feature/t1")

	spec, _ := GenerateSpec(ctx, SpecGenOpts{DB: d, RepoRoot: repoRoot, TaskID: "t1"})
	if !strings.Contains(spec, "| **Assigned Role** | researcher |") {
		t.Error("expected role from agent, not task")
	}
}

func TestGenerateSpecRoleFallbackToTask(t *testing.T) {
	d, repoRoot := setupSpecGenTest(t)
	ctx := context.Background()

	d.CreateTask(ctx, db.Task{ID: "t1", Title: "Test", Status: "pending", Role: "scout"})

	spec, _ := GenerateSpec(ctx, SpecGenOpts{DB: d, RepoRoot: repoRoot, TaskID: "t1"})
	if !strings.Contains(spec, "| **Assigned Role** | scout |") {
		t.Error("expected role from task when no agent assigned")
	}
}

// 33. IsRefinableFailure tests
func TestIsRefinableFailure(t *testing.T) {
	refinable := []string{
		"implementer_no_changes",
		"implementer_writes_gitignored",
		"researcher_no_commits",
		"researcher_no_markdown",
		"researcher_markdown_too_short",
		"researcher_insufficient_headings",
		"researcher_insufficient_urls",
		"architect_no_commits",
		"architect_no_markdown",
		"architect_markdown_too_short",
		"architect_insufficient_headings",
		"architect_insufficient_urls",
	}
	for _, reason := range refinable {
		if !IsRefinableFailure(reason) {
			t.Errorf("expected %s to be refinable", reason)
		}
	}

	// Infrastructure failures and role-specific paths should NOT be refinable
	nonRefinable := []string{
		"implementer_no_worktree",  // infra failure — critique can't fix missing worktree
		"architect_no_worktree",    // infra failure — same reason
		"researcher_no_worktree",   // infra failure — same reason
		"reviewer_no_output",       // reviewer role path, different handling
		"scout_result_too_short",   // scout role path, different handling
		"rate_limit",
		"session_limit",
		"context_exhausted",
		"normal_failure",
		"timeout",
		"killed",
		"unknown",
		"",
	}
	for _, reason := range nonRefinable {
		if IsRefinableFailure(reason) {
			t.Errorf("expected %s to NOT be refinable", reason)
		}
	}
}

// 34. Refinement section tests
func TestBuildRefinementSectionPresent(t *testing.T) {
	d, repoRoot := setupSpecGenTest(t)
	ctx := context.Background()

	d.CreateTask(ctx, db.Task{ID: "t1", Title: "Test", Status: "pending", Role: "implementer"})
	d.SetBlackboard(ctx, "refinement:t1", "1", "test")
	d.SetBlackboard(ctx, "critique:t1", "The agent failed to commit changes. It modified files but forgot git add/commit.", "test")
	d.SetBlackboard(ctx, "last_failure:t1", "implementer_no_changes", "test")

	spec, _ := GenerateSpec(ctx, SpecGenOpts{DB: d, RepoRoot: repoRoot, TaskID: "t1"})
	if !strings.Contains(spec, "## Previous Attempt — Reviewer Critique") {
		t.Error("missing refinement section")
	}
	if !strings.Contains(spec, "forgot git add/commit") {
		t.Error("missing critique content")
	}
	if !strings.Contains(spec, "Remediation Guidance") {
		t.Error("missing remediation guidance")
	}
	// Should NOT contain the generic retry section
	if strings.Contains(spec, "## Previous Attempt Failed") {
		t.Error("generic retry section should not appear when refinement section is present")
	}
}

func TestBuildRefinementSectionOverridesRetry(t *testing.T) {
	d, repoRoot := setupSpecGenTest(t)
	ctx := context.Background()

	d.CreateTask(ctx, db.Task{ID: "t1", Title: "Test", Status: "pending", Role: "implementer"})
	// Both retry and refinement set — refinement should win
	d.SetBlackboard(ctx, "retry:t1", "1", "test")
	d.SetBlackboard(ctx, "last_failure:t1", "implementer_no_changes", "test")
	d.SetBlackboard(ctx, "refinement:t1", "1", "test")
	d.SetBlackboard(ctx, "critique:t1", "Agent failed to produce output.", "test")

	spec, _ := GenerateSpec(ctx, SpecGenOpts{DB: d, RepoRoot: repoRoot, TaskID: "t1"})
	if !strings.Contains(spec, "Reviewer Critique") {
		t.Error("expected refinement section to override retry section")
	}
	if strings.Contains(spec, "## Previous Attempt Failed (Retry") {
		t.Error("retry section should not appear when refinement is present")
	}
}

func TestBuildRefinementSectionAbsentNoRefinement(t *testing.T) {
	d, repoRoot := setupSpecGenTest(t)
	ctx := context.Background()

	d.CreateTask(ctx, db.Task{ID: "t1", Title: "Test", Status: "pending", Role: "implementer"})
	// No refinement key set

	spec, _ := GenerateSpec(ctx, SpecGenOpts{DB: d, RepoRoot: repoRoot, TaskID: "t1"})
	if strings.Contains(spec, "Reviewer Critique") {
		t.Error("refinement section should not appear without refinement key")
	}
}

func TestBuildRefinementSectionAbsentNoCritique(t *testing.T) {
	d, repoRoot := setupSpecGenTest(t)
	ctx := context.Background()

	d.CreateTask(ctx, db.Task{ID: "t1", Title: "Test", Status: "pending", Role: "implementer"})
	d.SetBlackboard(ctx, "refinement:t1", "1", "test")
	// No critique key set — should fall back to retry section

	spec, _ := GenerateSpec(ctx, SpecGenOpts{DB: d, RepoRoot: repoRoot, TaskID: "t1"})
	if strings.Contains(spec, "Reviewer Critique") {
		t.Error("refinement section should not appear without critique")
	}
}

func TestBuildRefinementSectionGuidanceVariants(t *testing.T) {
	d, repoRoot := setupSpecGenTest(t)
	ctx := context.Background()

	tests := []struct {
		failure  string
		contains string
	}{
		{"implementer_no_changes", "MUST create or modify tracked files"},
		{"researcher_no_commits", "MUST commit all deliverables"},
		{"architect_no_markdown", "MUST produce at least one .md file"},
		{"researcher_markdown_too_short", "too short"},
		{"researcher_insufficient_headings", "at least 2 section headings"},
		{"researcher_insufficient_urls", "at least 3 URLs"},
		{"reviewer_no_output", "substantial output"},
		{"scout_result_too_short", "substantial output"},
	}

	for _, tc := range tests {
		d.CreateTask(ctx, db.Task{ID: "t-" + tc.failure, Title: "Test", Status: "pending", Role: "implementer"})
		d.SetBlackboard(ctx, "refinement:t-"+tc.failure, "1", "test")
		d.SetBlackboard(ctx, "critique:t-"+tc.failure, "Test critique for "+tc.failure, "test")
		d.SetBlackboard(ctx, "last_failure:t-"+tc.failure, tc.failure, "test")

		spec, _ := GenerateSpec(ctx, SpecGenOpts{DB: d, RepoRoot: repoRoot, TaskID: "t-" + tc.failure})
		if !strings.Contains(spec, tc.contains) {
			t.Errorf("failure %s: expected guidance containing %q", tc.failure, tc.contains)
		}
	}
}

// 35. Worktree paths in delivery + working directory
func TestGenerateSpecWorktreePaths(t *testing.T) {
	d, repoRoot := setupSpecGenTest(t)
	ctx := context.Background()

	worktreePath := repoRoot + "/.worktree/t1"
	d.CreateTask(ctx, db.Task{
		ID: "t1", Title: "Test", Status: "pending", Role: "implementer",
		Worktree: sql.NullString{String: worktreePath, Valid: true},
	})

	spec, _ := GenerateSpec(ctx, SpecGenOpts{DB: d, RepoRoot: repoRoot, TaskID: "t1"})

	// Check delivery section has correct worktree path
	expectedPath := worktreePath
	if !strings.Contains(spec, "git -C "+expectedPath+" add -A") {
		t.Errorf("expected worktree path in delivery git command, looking for: git -C %s add -A", expectedPath)
	}

	// Check working directory section
	if !strings.Contains(spec, "worktree root is: `"+expectedPath+"`") {
		t.Errorf("expected worktree path in working directory section")
	}
}

// 36. Self-check section tests
func TestBuildSelfCheckSectionImplementer(t *testing.T) {
	s := buildSelfCheckSection("implementer")
	if !strings.Contains(s, "## Self-Check") {
		t.Error("missing self-check heading for implementer")
	}
	if !strings.Contains(s, "All acceptance criteria") {
		t.Error("missing acceptance criteria check")
	}
	if !strings.Contains(s, "Tests pass") {
		t.Error("missing tests pass check")
	}
	if !strings.Contains(s, "Only files listed in Files Owned") {
		t.Error("missing files owned check")
	}
	if !strings.Contains(s, "Code compiles") {
		t.Error("missing code compiles check")
	}
}

func TestBuildSelfCheckSectionResearcher(t *testing.T) {
	s := buildSelfCheckSection("researcher")
	if !strings.Contains(s, "## Self-Check") {
		t.Error("missing self-check heading for researcher")
	}
	if !strings.Contains(s, "5 distinct sources") {
		t.Error("missing sources check")
	}
	if !strings.Contains(s, "Limitations and gaps") {
		t.Error("missing limitations check")
	}
	if !strings.Contains(s, "Raw data/quotes") {
		t.Error("missing raw data check")
	}
}

func TestBuildSelfCheckSectionArchitect(t *testing.T) {
	s := buildSelfCheckSection("architect")
	if !strings.Contains(s, "## Self-Check") {
		t.Error("missing self-check heading for architect")
	}
	if !strings.Contains(s, "No task overlaps") {
		t.Error("missing task overlaps check")
	}
	if !strings.Contains(s, "Dependencies are acyclic") {
		t.Error("missing acyclic check")
	}
	if !strings.Contains(s, "Interface contracts") {
		t.Error("missing interface contracts check")
	}
	if !strings.Contains(s, "File ownership") {
		t.Error("missing file ownership check")
	}
}

func TestBuildSelfCheckSectionReviewerEmpty(t *testing.T) {
	if s := buildSelfCheckSection("reviewer"); s != "" {
		t.Error("expected empty string for reviewer")
	}
}

func TestBuildSelfCheckSectionScoutEmpty(t *testing.T) {
	if s := buildSelfCheckSection("scout"); s != "" {
		t.Error("expected empty string for scout")
	}
}

func TestBuildSelfCheckSectionUnknownEmpty(t *testing.T) {
	if s := buildSelfCheckSection("unknown"); s != "" {
		t.Error("expected empty string for unknown role")
	}
}

func TestGenerateSpecSelfCheckBeforeDelivery(t *testing.T) {
	d, repoRoot := setupSpecGenTest(t)
	ctx := context.Background()

	d.CreateTask(ctx, db.Task{ID: "t1", Title: "Test", Status: "pending", Role: "implementer"})

	spec, _ := GenerateSpec(ctx, SpecGenOpts{DB: d, RepoRoot: repoRoot, TaskID: "t1"})
	selfCheckIdx := strings.Index(spec, "## Self-Check")
	deliveryIdx := strings.Index(spec, "## Delivery")
	if selfCheckIdx == -1 {
		t.Fatal("missing self-check section for implementer")
	}
	if deliveryIdx == -1 {
		t.Fatal("missing delivery section")
	}
	if selfCheckIdx >= deliveryIdx {
		t.Error("self-check section should appear before delivery section")
	}
}

func TestGenerateSpecSelfCheckAbsentForScout(t *testing.T) {
	d, repoRoot := setupSpecGenTest(t)
	ctx := context.Background()

	d.CreateTask(ctx, db.Task{ID: "t1", Title: "Test", Status: "pending", Role: "scout"})

	spec, _ := GenerateSpec(ctx, SpecGenOpts{DB: d, RepoRoot: repoRoot, TaskID: "t1"})
	if strings.Contains(spec, "## Self-Check") {
		t.Error("self-check section should not appear for scout")
	}
}

func TestGenerateSpecSelfCheckAbsentForReviewer(t *testing.T) {
	d, repoRoot := setupSpecGenTest(t)
	ctx := context.Background()

	d.CreateTask(ctx, db.Task{ID: "t1", Title: "Test", Status: "pending", Role: "reviewer"})

	spec, _ := GenerateSpec(ctx, SpecGenOpts{DB: d, RepoRoot: repoRoot, TaskID: "t1"})
	if strings.Contains(spec, "## Self-Check") {
		t.Error("self-check section should not appear for reviewer")
	}
}

// B-057: test_cmd_failed and review_rejected refinable failures
func TestIsRefinableFailure_TestCmdFailed(t *testing.T) {
	if !IsRefinableFailure("test_cmd_failed") {
		t.Error("expected test_cmd_failed to be refinable")
	}
}

func TestIsRefinableFailure_ReviewRejected(t *testing.T) {
	if !IsRefinableFailure("review_rejected") {
		t.Error("expected review_rejected to be refinable")
	}
}

func TestBuildRefinementSection_TestOutput(t *testing.T) {
	d, repoRoot := setupSpecGenTest(t)
	ctx := context.Background()

	d.CreateTask(ctx, db.Task{ID: "t-test-fail", Title: "Test", Status: "pending", Role: "implementer"})
	d.SetBlackboard(ctx, "refinement:t-test-fail", "1", "test")
	d.SetBlackboard(ctx, "critique:t-test-fail", "The tests failed.", "test")
	d.SetBlackboard(ctx, "last_failure:t-test-fail", "test_cmd_failed", "test")
	d.SetBlackboard(ctx, "merge_feedback:t-test-fail", "FAIL: TestFoo expected 1 got 2", "test")

	spec, _ := GenerateSpec(ctx, SpecGenOpts{DB: d, RepoRoot: repoRoot, TaskID: "t-test-fail"})
	if !strings.Contains(spec, "### Test Output") {
		t.Error("expected Test Output section in spec")
	}
	if !strings.Contains(spec, "FAIL: TestFoo expected 1 got 2") {
		t.Error("expected test output content in spec")
	}
	// Should be in a code block
	if !strings.Contains(spec, "```\nFAIL: TestFoo") {
		t.Error("expected test output in code block")
	}
}

func TestBuildRefinementSection_ReviewCritique(t *testing.T) {
	d, repoRoot := setupSpecGenTest(t)
	ctx := context.Background()

	d.CreateTask(ctx, db.Task{ID: "t-review-rej", Title: "Test", Status: "pending", Role: "implementer"})
	d.SetBlackboard(ctx, "refinement:t-review-rej", "1", "test")
	d.SetBlackboard(ctx, "critique:t-review-rej", "Code was rejected.", "test")
	d.SetBlackboard(ctx, "last_failure:t-review-rej", "review_rejected", "test")
	d.SetBlackboard(ctx, "merge_feedback:t-review-rej", "Security issue: SQL injection vulnerability", "test")

	spec, _ := GenerateSpec(ctx, SpecGenOpts{DB: d, RepoRoot: repoRoot, TaskID: "t-review-rej"})
	if !strings.Contains(spec, "### Review Feedback") {
		t.Error("expected Review Feedback section in spec")
	}
	if !strings.Contains(spec, "SQL injection vulnerability") {
		t.Error("expected review feedback content in spec")
	}
}

func TestBuildRefinementSection_TestCmdGuidance(t *testing.T) {
	d, repoRoot := setupSpecGenTest(t)
	ctx := context.Background()

	d.CreateTask(ctx, db.Task{ID: "t-tcg", Title: "Test", Status: "pending", Role: "implementer"})
	d.SetBlackboard(ctx, "refinement:t-tcg", "1", "test")
	d.SetBlackboard(ctx, "critique:t-tcg", "Tests failed after merge.", "test")
	d.SetBlackboard(ctx, "last_failure:t-tcg", "test_cmd_failed", "test")

	spec, _ := GenerateSpec(ctx, SpecGenOpts{DB: d, RepoRoot: repoRoot, TaskID: "t-tcg"})
	if !strings.Contains(spec, "test command FAILED") {
		t.Error("expected test_cmd_failed guidance")
	}
	if !strings.Contains(spec, "Fix the code causing test failures") {
		t.Error("expected fix guidance for test failures")
	}
}

func TestBuildRefinementSection_ReviewGuidance(t *testing.T) {
	d, repoRoot := setupSpecGenTest(t)
	ctx := context.Background()

	d.CreateTask(ctx, db.Task{ID: "t-rg", Title: "Test", Status: "pending", Role: "implementer"})
	d.SetBlackboard(ctx, "refinement:t-rg", "1", "test")
	d.SetBlackboard(ctx, "critique:t-rg", "Review rejected.", "test")
	d.SetBlackboard(ctx, "last_failure:t-rg", "review_rejected", "test")

	spec, _ := GenerateSpec(ctx, SpecGenOpts{DB: d, RepoRoot: repoRoot, TaskID: "t-rg"})
	if !strings.Contains(spec, "REJECTED during review") {
		t.Error("expected review_rejected guidance")
	}
	if !strings.Contains(spec, "Address ALL reviewer concerns") {
		t.Error("expected address reviewer concerns guidance")
	}
}

// --- Continuation Section Tests ---

func TestBuildContinuationSectionPresent(t *testing.T) {
	d, repoRoot := setupSpecGenTest(t)
	ctx := context.Background()

	d.CreateTask(ctx, db.Task{ID: "t1", Title: "Test", Status: "pending", Role: "implementer"})

	cp := Checkpoint{
		TaskID:            "t1",
		CommitHashes:      []string{"abc123", "def456"},
		FilesModified:     []string{"main.go", "util.go"},
		TestsPassing:      "unknown",
		UncommittedFiles:  1,
		EstimatedProgress: "mid",
		LogTailSummary:    "- Used tool: Edit\n- Used tool: Bash",
	}
	cpJSON, _ := json.Marshal(cp)
	d.SetBlackboard(ctx, "checkpoint:t1", string(cpJSON), "test")

	spec, _ := GenerateSpec(ctx, SpecGenOpts{DB: d, RepoRoot: repoRoot, TaskID: "t1"})
	if !strings.Contains(spec, "## Continuation Context") {
		t.Error("missing continuation context section")
	}
	if !strings.Contains(spec, "previous agent worked on this task") {
		t.Error("missing continuation explanation")
	}
	if !strings.Contains(spec, "| Commits made | 2 |") {
		t.Error("missing commit count")
	}
	if !strings.Contains(spec, "main.go, util.go") {
		t.Error("missing modified files")
	}
	if !strings.Contains(spec, "| Estimated progress | mid |") {
		t.Error("missing progress estimate")
	}
	if !strings.Contains(spec, "### Last Actions") {
		t.Error("missing last actions section")
	}
	if !strings.Contains(spec, "Do NOT repeat completed work") {
		t.Error("missing anti-repeat instruction")
	}
}

func TestBuildContinuationSectionAbsent(t *testing.T) {
	d, repoRoot := setupSpecGenTest(t)
	ctx := context.Background()

	d.CreateTask(ctx, db.Task{ID: "t1", Title: "Test", Status: "pending", Role: "implementer"})

	spec, _ := GenerateSpec(ctx, SpecGenOpts{DB: d, RepoRoot: repoRoot, TaskID: "t1"})
	if strings.Contains(spec, "## Continuation Context") {
		t.Error("continuation section should not appear without checkpoint")
	}
}

func TestBuildContinuationSectionRawFallback(t *testing.T) {
	d, repoRoot := setupSpecGenTest(t)
	ctx := context.Background()

	d.CreateTask(ctx, db.Task{ID: "t1", Title: "Test", Status: "pending", Role: "implementer"})
	d.SetBlackboard(ctx, "checkpoint:t1", "not-valid-json", "test")

	spec, _ := GenerateSpec(ctx, SpecGenOpts{DB: d, RepoRoot: repoRoot, TaskID: "t1"})
	if !strings.Contains(spec, "## Continuation Context") {
		t.Error("missing continuation section with raw fallback")
	}
	if !strings.Contains(spec, "not-valid-json") {
		t.Error("raw text fallback missing")
	}
}

func TestBuildContinuationSectionNoLogTail(t *testing.T) {
	d, repoRoot := setupSpecGenTest(t)
	ctx := context.Background()

	d.CreateTask(ctx, db.Task{ID: "t1", Title: "Test", Status: "pending", Role: "implementer"})

	cp := Checkpoint{
		TaskID:            "t1",
		CommitHashes:      []string{"abc123"},
		EstimatedProgress: "mid",
	}
	cpJSON, _ := json.Marshal(cp)
	d.SetBlackboard(ctx, "checkpoint:t1", string(cpJSON), "test")

	spec, _ := GenerateSpec(ctx, SpecGenOpts{DB: d, RepoRoot: repoRoot, TaskID: "t1"})
	if !strings.Contains(spec, "## Continuation Context") {
		t.Error("missing continuation context section")
	}
	if strings.Contains(spec, "### Last Actions") {
		t.Error("last actions should not appear when log tail is empty")
	}
}

// --- Acceptance Criteria Tests ---

func TestGenerateSpecAcceptanceCriteriaPresent(t *testing.T) {
	d, repoRoot := setupSpecGenTest(t)
	ctx := context.Background()

	d.CreateTask(ctx, db.Task{
		ID: "t1", Title: "Add field", Status: "pending", Role: "implementer",
		AcceptanceCriteria: sql.NullString{
			String: "- [ ] Task struct has PriorityLabel field\n- [ ] Field type is sql.NullString",
			Valid:  true,
		},
	})

	spec, err := GenerateSpec(ctx, SpecGenOpts{DB: d, RepoRoot: repoRoot, TaskID: "t1"})
	if err != nil {
		t.Fatalf("GenerateSpec: %v", err)
	}

	// Task-specific ACs should appear
	if !strings.Contains(spec, "PriorityLabel field") {
		t.Error("expected task-specific AC in spec")
	}
	if !strings.Contains(spec, "sql.NullString") {
		t.Error("expected task-specific AC detail in spec")
	}

	// Generic ACs should also appear after task-specific
	if !strings.Contains(spec, "Implementation matches the description above") {
		t.Error("expected generic AC in spec")
	}
	if !strings.Contains(spec, "All tests pass") {
		t.Error("expected generic AC in spec")
	}

	// Task-specific should appear before generic
	specificIdx := strings.Index(spec, "PriorityLabel field")
	genericIdx := strings.Index(spec, "Implementation matches the description above")
	if specificIdx > genericIdx {
		t.Error("task-specific ACs should appear before generic ACs")
	}
}

func TestGenerateSpecAcceptanceCriteriaAbsent(t *testing.T) {
	d, repoRoot := setupSpecGenTest(t)
	ctx := context.Background()

	d.CreateTask(ctx, db.Task{
		ID: "t1", Title: "Fix bug", Status: "pending", Role: "implementer",
	})

	spec, err := GenerateSpec(ctx, SpecGenOpts{DB: d, RepoRoot: repoRoot, TaskID: "t1"})
	if err != nil {
		t.Fatalf("GenerateSpec: %v", err)
	}

	// Generic ACs should still appear
	if !strings.Contains(spec, "## Acceptance Criteria") {
		t.Error("expected Acceptance Criteria section")
	}
	if !strings.Contains(spec, "Implementation matches the description above") {
		t.Error("expected generic AC when no task-specific AC")
	}
}

// --- Context Management Section Tests ---

func TestBuildContextManagementSection(t *testing.T) {
	s := buildContextManagementSection()
	if !strings.Contains(s, "## Context Management") {
		t.Error("missing context management heading")
	}
	if !strings.Contains(s, "commit your work immediately") {
		t.Error("missing commit instruction")
	}
	if !strings.Contains(s, "commit after each file") {
		t.Error("missing per-file commit instruction")
	}
}

func TestGenerateSpecContextManagementBeforeTokenBudget(t *testing.T) {
	d, repoRoot := setupSpecGenTest(t)
	ctx := context.Background()

	d.CreateTask(ctx, db.Task{ID: "t1", Title: "Test", Status: "pending", Role: "implementer"})

	spec, _ := GenerateSpec(ctx, SpecGenOpts{DB: d, RepoRoot: repoRoot, TaskID: "t1"})

	cmIdx := strings.Index(spec, "## Context Management")
	tbIdx := strings.Index(spec, "## Token Budget")
	if cmIdx == -1 {
		t.Fatal("missing Context Management section")
	}
	if tbIdx == -1 {
		t.Fatal("missing Token Budget section")
	}
	if cmIdx >= tbIdx {
		t.Error("Context Management should appear before Token Budget")
	}
}

func TestGenerateSpecContinuationAfterPredecessor(t *testing.T) {
	d, repoRoot := setupSpecGenTest(t)
	ctx := context.Background()

	d.CreateTask(ctx, db.Task{ID: "t0", Title: "Prereq", Status: "done", Role: "implementer"})
	d.CreateTask(ctx, db.Task{
		ID: "t1", Title: "Test", Status: "pending", Role: "implementer",
		BlockedBy: sql.NullString{String: `["t0"]`, Valid: true},
	})
	d.SetBlackboard(ctx, "result_summary:t0", "Did something.", "test")

	cp := Checkpoint{TaskID: "t1", EstimatedProgress: "early"}
	cpJSON, _ := json.Marshal(cp)
	d.SetBlackboard(ctx, "checkpoint:t1", string(cpJSON), "test")

	spec, _ := GenerateSpec(ctx, SpecGenOpts{DB: d, RepoRoot: repoRoot, TaskID: "t1"})

	predIdx := strings.Index(spec, "## Predecessor Results")
	contIdx := strings.Index(spec, "## Continuation Context")
	if predIdx == -1 {
		t.Fatal("missing predecessor section")
	}
	if contIdx == -1 {
		t.Fatal("missing continuation section")
	}
	if contIdx < predIdx {
		t.Error("continuation section should appear after predecessor section")
	}
}

// --- YAML Frontmatter Tests ---

func TestGenerateSpec_HasYAMLFrontmatter(t *testing.T) {
	d, repoRoot := setupSpecGenTest(t)
	ctx := context.Background()

	d.CreateTask(ctx, db.Task{
		ID: "t1", Title: "Add auth middleware", Status: "pending", Role: "implementer",
		Description: sql.NullString{String: "Add auth.", Valid: true},
	})

	spec, err := GenerateSpec(ctx, SpecGenOpts{DB: d, RepoRoot: repoRoot, TaskID: "t1"})
	if err != nil {
		t.Fatalf("GenerateSpec: %v", err)
	}

	// Must start with ---
	if !strings.HasPrefix(spec, "---\n") {
		t.Error("spec must start with YAML frontmatter delimiter ---")
	}

	// Must have closing --- before markdown body
	afterFirst := spec[4:] // skip opening ---\n
	closingIdx := strings.Index(afterFirst, "---\n")
	if closingIdx == -1 {
		t.Fatal("missing closing --- for YAML frontmatter")
	}

	// Markdown body follows frontmatter
	body := afterFirst[closingIdx+4:]
	if !strings.HasPrefix(body, "# Task Specification: t1") {
		t.Errorf("markdown body should start with # Task Specification, got: %s", body[:60])
	}
}

func TestFrontmatter_Fields(t *testing.T) {
	d, repoRoot := setupSpecGenTest(t)
	ctx := context.Background()

	d.RegisterAgent(ctx, db.Agent{ID: "a1", Role: "implementer", Status: "active"})
	d.CreateTask(ctx, db.Task{
		ID: "t1", Title: "Add auth middleware", Status: "pending", Role: "implementer",
		DependsOn: sql.NullString{String: `["t0"]`, Valid: true},
	})
	d.CreateFileLock(ctx, "internal/api/auth.go", "a1", "t1", sql.NullTime{}.Time)
	d.SetBlackboard(ctx, "conductor:session_id", "s-test-123", "test")

	spec, err := GenerateSpec(ctx, SpecGenOpts{DB: d, RepoRoot: repoRoot, TaskID: "t1"})
	if err != nil {
		t.Fatalf("GenerateSpec: %v", err)
	}

	// Extract frontmatter
	afterFirst := spec[4:]
	closingIdx := strings.Index(afterFirst, "---\n")
	if closingIdx == -1 {
		t.Fatal("missing closing ---")
	}
	fm := afterFirst[:closingIdx]

	checks := []struct {
		field    string
		contains string
	}{
		{"task_id", "task_id: t1"},
		{"title", "title: Add auth middleware"},
		{"role", "role: implementer"},
		{"model", "model: opus"},
		{"session_id", "session_id: s-test-123"},
		{"depends_on", `depends_on: ["t0"]`},
		{"files", "files: [internal/api/auth.go]"},
		{"generated_at", "generated_at: "},
	}
	for _, c := range checks {
		if !strings.Contains(fm, c.contains) {
			t.Errorf("frontmatter missing %s: expected %q in:\n%s", c.field, c.contains, fm)
		}
	}
}

func TestFrontmatter_OmitsEmptyOptionalFields(t *testing.T) {
	d, repoRoot := setupSpecGenTest(t)
	ctx := context.Background()

	d.CreateTask(ctx, db.Task{
		ID: "t1", Title: "Simple task", Status: "pending", Role: "implementer",
	})

	spec, _ := GenerateSpec(ctx, SpecGenOpts{DB: d, RepoRoot: repoRoot, TaskID: "t1"})

	afterFirst := spec[4:]
	closingIdx := strings.Index(afterFirst, "---\n")
	fm := afterFirst[:closingIdx]

	if strings.Contains(fm, "session_id:") {
		t.Error("session_id should be omitted when not set")
	}
	if strings.Contains(fm, "depends_on:") {
		t.Error("depends_on should be omitted when not set")
	}
	if strings.Contains(fm, "files:") {
		t.Error("files should be omitted when no locks")
	}
}

// --- Illustrator Self-Check Tests ---

func TestBuildSelfCheckSectionIllustrator(t *testing.T) {
	s := buildSelfCheckSection("illustrator")
	if !strings.Contains(s, "## Self-Check") {
		t.Error("missing self-check heading for illustrator")
	}
	if !strings.Contains(s, "figure descriptions") {
		t.Error("missing figure descriptions check")
	}
	if !strings.Contains(s, "PNG (600 DPI) and SVG") {
		t.Error("missing PNG/SVG check")
	}
	if !strings.Contains(s, "figures/chNN-figNN") {
		t.Error("missing naming convention check")
	}
	if !strings.Contains(s, "self-contained") {
		t.Error("missing self-contained check")
	}
	if !strings.Contains(s, "committed to the worktree") {
		t.Error("missing commit check")
	}
}

func TestGenerateSpecSelfCheckIllustrator(t *testing.T) {
	d, repoRoot := setupSpecGenTest(t)
	ctx := context.Background()

	d.CreateTask(ctx, db.Task{ID: "t1", Title: "Generate figures", Status: "pending", Role: "illustrator"})

	spec, _ := GenerateSpec(ctx, SpecGenOpts{DB: d, RepoRoot: repoRoot, TaskID: "t1"})
	if !strings.Contains(spec, "## Self-Check") {
		t.Error("expected self-check section for illustrator")
	}
	if !strings.Contains(spec, "figure descriptions") {
		t.Error("expected illustrator-specific self-check content")
	}
}
