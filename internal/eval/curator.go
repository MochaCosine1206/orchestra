package eval

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/MochaCosine1206/orchestra/internal/db"
)

// EvalScenario is a convenience alias for scenarios curated from the database.
// It bridges the db.EvalScenario model and the eval.Scenario domain model.
type EvalScenario = Scenario

// CurateFromDB extracts failed tasks from orchestrator.db as eval scenarios.
// It queries tasks with status='failed' and converts them into eval scenarios
// that can be used as regression tests.
func CurateFromDB(ctx context.Context, d *db.DB) ([]Scenario, error) {
	if d == nil {
		return nil, fmt.Errorf("database is nil")
	}

	tasks, err := d.ListTasks(ctx)
	if err != nil {
		return nil, fmt.Errorf("listing tasks: %w", err)
	}

	var scenarios []Scenario
	for _, t := range tasks {
		if t.Status != "failed" {
			continue
		}
		description := ""
		if t.Description.Valid {
			description = t.Description.String
		}
		expectedOutcome := "Task should complete successfully"
		if t.AcceptanceCriteria.Valid {
			expectedOutcome = t.AcceptanceCriteria.String
		}

		s := Scenario{
			ID:              fmt.Sprintf("curated-%s", t.ID),
			Role:            t.Role,
			Category:        "real_failure",
			Goal:            fmt.Sprintf("%s: %s", t.Title, description),
			ExpectedOutcome: expectedOutcome,
			Difficulty:      "medium",
			CreatedAt:       time.Now(),
		}
		scenarios = append(scenarios, s)
	}
	return scenarios, nil
}

// SeedDefaultScenarios creates a minimal holdout set from known patterns.
// These cover common failure modes observed in production orchestration.
func SeedDefaultScenarios(ctx context.Context, d *db.DB) error {
	if d == nil {
		return fmt.Errorf("database is nil")
	}

	defaults := []db.EvalScenario{
		{
			ID:              "seed-impl-basic",
			Role:            "implementer",
			Category:        sql.NullString{String: "synthetic", Valid: true},
			Goal:            "Add a new function to an existing Go file with proper error handling and tests",
			ExpectedOutcome: sql.NullString{String: "Function added, tests pass, no build errors", Valid: true},
			Difficulty:      sql.NullString{String: "easy", Valid: true},
		},
		{
			ID:              "seed-impl-multifile",
			Role:            "implementer",
			Category:        sql.NullString{String: "synthetic", Valid: true},
			Goal:            "Refactor a function across 3 files: extract interface, update callers, add tests",
			ExpectedOutcome: sql.NullString{String: "Interface extracted, all callers updated, tests pass", Valid: true},
			Difficulty:      sql.NullString{String: "medium", Valid: true},
		},
		{
			ID:              "seed-impl-merge",
			Role:            "implementer",
			Category:        sql.NullString{String: "edge_case", Valid: true},
			Goal:            "Implement feature that touches shared utility file likely to have merge conflicts",
			ExpectedOutcome: sql.NullString{String: "Feature implemented, merge conflicts resolved cleanly", Valid: true},
			Difficulty:      sql.NullString{String: "hard", Valid: true},
		},
		{
			ID:              "seed-reviewer-bugs",
			Role:            "reviewer",
			Category:        sql.NullString{String: "synthetic", Valid: true},
			Goal:            "Review a PR containing 2 real bugs and 1 style issue. Identify bugs without false positives",
			ExpectedOutcome: sql.NullString{String: "Both bugs detected, style issue noted, no false positives", Valid: true},
			Difficulty:      sql.NullString{String: "medium", Valid: true},
		},
		{
			ID:              "seed-reviewer-clean",
			Role:            "reviewer",
			Category:        sql.NullString{String: "edge_case", Valid: true},
			Goal:            "Review a clean PR with no bugs. Should approve without false positive findings",
			ExpectedOutcome: sql.NullString{String: "Approved with minimal or no findings, zero false positives", Valid: true},
			Difficulty:      sql.NullString{String: "medium", Valid: true},
		},
		{
			ID:              "seed-researcher-sources",
			Role:            "researcher",
			Category:        sql.NullString{String: "synthetic", Valid: true},
			Goal:            "Research a technical topic and produce a document with at least 5 cited sources",
			ExpectedOutcome: sql.NullString{String: "Document with 5+ sources, accurate citations, actionable conclusions", Valid: true},
			Difficulty:      sql.NullString{String: "medium", Valid: true},
		},
		{
			ID:              "seed-architect-decompose",
			Role:            "architect",
			Category:        sql.NullString{String: "synthetic", Valid: true},
			Goal:            "Decompose a 10-file feature into independent tasks with a valid dependency DAG",
			ExpectedOutcome: sql.NullString{String: "3-5 tasks, valid DAG, complete file coverage, no overlap", Valid: true},
			Difficulty:      sql.NullString{String: "medium", Valid: true},
		},
		{
			ID:              "seed-architect-overlap",
			Role:            "architect",
			Category:        sql.NullString{String: "edge_case", Valid: true},
			Goal:            "Decompose a feature where shared utility files make clean separation difficult",
			ExpectedOutcome: sql.NullString{String: "Tasks have minimal file overlap, shared files assigned to one owner", Valid: true},
			Difficulty:      sql.NullString{String: "hard", Valid: true},
		},
	}

	for _, s := range defaults {
		if err := d.InsertEvalScenario(ctx, s); err != nil {
			// Ignore duplicate key errors (scenario already seeded)
			if isDuplicateError(err) {
				continue
			}
			return fmt.Errorf("seeding scenario %s: %w", s.ID, err)
		}
	}
	return nil
}

// isDuplicateError checks if an error is a unique constraint violation.
func isDuplicateError(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	// SQLite unique constraint error patterns
	return contains(msg, "UNIQUE constraint failed") || contains(msg, "duplicate key")
}

// contains is a simple substring check to avoid importing strings.
func contains(s, sub string) bool {
	return len(s) >= len(sub) && searchSubstring(s, sub)
}

func searchSubstring(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
