package cmd

import (
	"bytes"
	"context"
	"database/sql"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MochaCosine1206/orchestra/internal/db"
)

func setupEvalTestDB(t *testing.T) (string, *db.DB) {
	t.Helper()
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "eval-test.db")

	d, err := db.Open(dbPath)
	if err != nil {
		t.Fatalf("opening db: %v", err)
	}
	if err := d.InitSchema(context.Background()); err != nil {
		t.Fatalf("init schema: %v", err)
	}
	t.Cleanup(func() { d.Close() })
	return dbPath, d
}

func TestEvalRunCmd_Help(t *testing.T) {
	root := NewRootCmd()
	buf := new(bytes.Buffer)
	root.SetOut(buf)
	root.SetArgs([]string{"eval", "run", "--help"})

	if err := root.Execute(); err != nil {
		t.Fatalf("eval run --help failed: %v", err)
	}

	out := buf.String()
	if !strings.Contains(out, "run") {
		t.Errorf("eval run help should contain 'run', got: %q", out)
	}
	if !strings.Contains(out, "eval scenarios") {
		t.Errorf("eval run help should contain usage text, got: %q", out)
	}
}

func TestEvalCompareCmd_Help(t *testing.T) {
	root := NewRootCmd()
	buf := new(bytes.Buffer)
	root.SetOut(buf)
	root.SetArgs([]string{"eval", "compare", "--help"})

	if err := root.Execute(); err != nil {
		t.Fatalf("eval compare --help failed: %v", err)
	}

	out := buf.String()
	if !strings.Contains(out, "compare") {
		t.Errorf("eval compare help should contain 'compare', got: %q", out)
	}
}

func TestEvalScenariosCmd_Help(t *testing.T) {
	root := NewRootCmd()
	buf := new(bytes.Buffer)
	root.SetOut(buf)
	root.SetArgs([]string{"eval", "scenarios", "--help"})

	if err := root.Execute(); err != nil {
		t.Fatalf("eval scenarios --help failed: %v", err)
	}

	out := buf.String()
	if !strings.Contains(out, "scenarios") {
		t.Errorf("eval scenarios help should contain 'scenarios', got: %q", out)
	}
}

func TestEvalReportCmd_Help(t *testing.T) {
	root := NewRootCmd()
	buf := new(bytes.Buffer)
	root.SetOut(buf)
	root.SetArgs([]string{"eval", "report", "--help"})

	if err := root.Execute(); err != nil {
		t.Fatalf("eval report --help failed: %v", err)
	}

	out := buf.String()
	if !strings.Contains(out, "report") {
		t.Errorf("eval report help should contain 'report', got: %q", out)
	}
}

func TestEvalScenariosListCmd(t *testing.T) {
	dbPath, d := setupEvalTestDB(t)

	// Insert a test scenario
	scenario := db.EvalScenario{
		ID:         "test-scenario-1",
		Role:       "implementer",
		Goal:       "Fix the authentication bug in login handler",
		Category:   sql.NullString{String: "bugfix", Valid: true},
		Difficulty: sql.NullString{String: "medium", Valid: true},
	}
	if err := d.InsertEvalScenario(context.Background(), scenario); err != nil {
		t.Fatalf("inserting scenario: %v", err)
	}
	d.Close()

	root := NewRootCmd()
	buf := new(bytes.Buffer)
	root.SetOut(buf)
	root.SetArgs([]string{"eval", "scenarios", "list", "--db", dbPath})

	if err := root.Execute(); err != nil {
		t.Fatalf("eval scenarios list failed: %v", err)
	}

	out := buf.String()
	if !strings.Contains(out, "Fix the authentication bug") {
		t.Errorf("eval scenarios list should contain scenario goal, got: %q", out)
	}
	if !strings.Contains(out, "implementer") {
		t.Errorf("eval scenarios list should contain role, got: %q", out)
	}
}

func TestEvalReportCmd(t *testing.T) {
	dbPath, d := setupEvalTestDB(t)

	ctx := context.Background()

	// Insert scenario
	scenario := db.EvalScenario{
		ID:   "test-scenario-report",
		Role: "implementer",
		Goal: "Test scenario for report",
	}
	if err := d.InsertEvalScenario(ctx, scenario); err != nil {
		t.Fatalf("inserting scenario: %v", err)
	}

	// Insert version
	version := db.EvalVersion{
		ID:     "test-version-1",
		Status: "candidate",
	}
	if err := d.InsertEvalVersion(ctx, version); err != nil {
		t.Fatalf("inserting version: %v", err)
	}

	// Insert run
	run := db.EvalRun{
		ID:         "test-run-1",
		VersionID:  "test-version-1",
		ScenarioID: "test-scenario-report",
		Status:     "completed",
	}
	if err := d.InsertEvalRun(ctx, run); err != nil {
		t.Fatalf("inserting run: %v", err)
	}

	// Insert result
	result := db.EvalResult{
		ID:      "test-result-1",
		RunID:   "test-run-1",
		Metric:  "correctness",
		Score:   0.95,
		Weight:  1.0,
		Details: sql.NullString{String: "All tests passed", Valid: true},
	}
	if err := d.InsertEvalResult(ctx, result); err != nil {
		t.Fatalf("inserting result: %v", err)
	}
	d.Close()

	root := NewRootCmd()
	buf := new(bytes.Buffer)
	root.SetOut(buf)
	root.SetArgs([]string{"eval", "report", "--run-id", "test-run-1", "--db", dbPath})

	if err := root.Execute(); err != nil {
		t.Fatalf("eval report failed: %v", err)
	}

	out := buf.String()
	if !strings.Contains(out, "correctness") {
		t.Errorf("eval report should contain metric name, got: %q", out)
	}
	if !strings.Contains(out, "0.95") {
		t.Errorf("eval report should contain score, got: %q", out)
	}
}
