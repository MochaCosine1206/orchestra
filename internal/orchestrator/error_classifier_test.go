package orchestrator

import (
	"strings"
	"testing"
)

func TestClassifyError_GoMissingImport(t *testing.T) {
	output := `internal/orchestrator/foo.go:5:2: could not import "fmt/bar"`
	env := ClassifyError(output)
	if env == nil {
		t.Fatal("expected non-nil envelope")
	}
	if env.Triage != TriageHeal {
		t.Errorf("expected HEAL for missing import, got %s", env.Triage)
	}
	if env.ErrorType != "missing_import" {
		t.Errorf("expected missing_import, got %s", env.ErrorType)
	}
}

func TestClassifyError_GoTypeError(t *testing.T) {
	output := `internal/agent/spawner.go:42:10: cannot use x (variable of type int) as string in argument`
	env := ClassifyError(output)
	if env == nil {
		t.Fatal("expected non-nil envelope")
	}
	if env.Triage != TriageHeal {
		t.Errorf("expected HEAL for type error in 1 file, got %s", env.Triage)
	}
	if env.ErrorType != "type_error" {
		t.Errorf("expected type_error, got %s", env.ErrorType)
	}
	if len(env.FilesAffected) != 1 || env.FilesAffected[0] != "internal/agent/spawner.go" {
		t.Errorf("expected spawner.go in files affected, got %v", env.FilesAffected)
	}
	if len(env.LineNumbers) != 1 || env.LineNumbers[0] != 42 {
		t.Errorf("expected line 42, got %v", env.LineNumbers)
	}
}

func TestClassifyError_GoSyntaxError(t *testing.T) {
	output := `main.go:10:5: expected ';', found 'func'`
	env := ClassifyError(output)
	if env == nil {
		t.Fatal("expected non-nil envelope")
	}
	if env.Triage != TriageHeal {
		t.Errorf("expected HEAL for syntax error, got %s", env.Triage)
	}
	if env.ErrorType != "syntax_error" {
		t.Errorf("expected syntax_error, got %s", env.ErrorType)
	}
}

func TestClassifyError_GoMissingDep(t *testing.T) {
	output := `no required module provides package github.com/foo/bar; to add it: go get github.com/foo/bar`
	env := ClassifyError(output)
	if env == nil {
		t.Fatal("expected non-nil envelope")
	}
	if env.Triage != TriageHeal {
		t.Errorf("expected HEAL for missing dep, got %s", env.Triage)
	}
	if env.ErrorType != "missing_dep" {
		t.Errorf("expected missing_dep, got %s", env.ErrorType)
	}
}

func TestClassifyError_GoTestFailure(t *testing.T) {
	output := `--- FAIL: TestSpawnRun (0.01s)
    spawner_test.go:42: expected status 'done', got 'failed'
FAIL	github.com/example/project/internal/agent	0.123s`
	env := ClassifyError(output)
	if env == nil {
		t.Fatal("expected non-nil envelope")
	}
	if env.Triage != TriageRefine {
		t.Errorf("expected REFINE for test failure, got %s", env.Triage)
	}
}

func TestClassifyError_NpmModuleNotFound(t *testing.T) {
	output := `Error: Cannot find module 'express'`
	env := ClassifyError(output)
	if env == nil {
		t.Fatal("expected non-nil envelope")
	}
	if env.Triage != TriageHeal {
		t.Errorf("expected HEAL for npm missing module, got %s", env.Triage)
	}
	if env.ErrorType != "missing_dep" {
		t.Errorf("expected missing_dep, got %s", env.ErrorType)
	}
}

func TestClassifyError_NpmTestFailure(t *testing.T) {
	output := `FAIL src/components/App.test.tsx`
	env := ClassifyError(output)
	if env == nil {
		t.Fatal("expected non-nil envelope")
	}
	if env.Triage != TriageRefine {
		t.Errorf("expected REFINE for npm test failure, got %s", env.Triage)
	}
}

func TestClassifyError_CargoMissingCrate(t *testing.T) {
	output := `error[E0432]: unresolved import 'tokio'
 --> src/main.rs:1:5`
	env := ClassifyError(output)
	if env == nil {
		t.Fatal("expected non-nil envelope")
	}
	if env.Triage != TriageHeal {
		t.Errorf("expected HEAL for cargo missing crate, got %s", env.Triage)
	}
	if env.ErrorType != "missing_dep" {
		t.Errorf("expected missing_dep, got %s", env.ErrorType)
	}
}

func TestClassifyError_CargoTestFailure(t *testing.T) {
	output := `test result: FAILED. 1 passed; 1 failed
test tests::test_parse ... FAILED`
	env := ClassifyError(output)
	if env == nil {
		t.Fatal("expected non-nil envelope")
	}
	if env.Triage != TriageRefine {
		t.Errorf("expected REFINE for cargo test failure, got %s", env.Triage)
	}
}

func TestClassifyError_RedoEscalation(t *testing.T) {
	// Generate >10 errors across >5 files
	var lines []string
	for i := 0; i < 12; i++ {
		lines = append(lines, strings.ReplaceAll(
			"file_PLACEHOLDER.go:10:5: undefined: SomeFunc",
			"PLACEHOLDER",
			string(rune('a'+i)),
		))
	}
	output := strings.Join(lines, "\n")

	env := ClassifyError(output)
	if env == nil {
		t.Fatal("expected non-nil envelope")
	}
	if env.Triage != TriageRedo {
		t.Errorf("expected REDO for >10 errors across >5 files, got %s", env.Triage)
	}
}

func TestClassifyError_NilOnCleanOutput(t *testing.T) {
	output := `ok  	github.com/example/project/internal/agent	0.123s`
	env := ClassifyError(output)
	if env != nil {
		t.Errorf("expected nil envelope for clean output, got %+v", env)
	}
}

func TestClassifyError_BuildSuccessTestFail(t *testing.T) {
	// Build succeeds but tests fail -> REFINE
	output := `--- FAIL: TestSomething (0.00s)
    something_test.go:15: assertion failed`
	env := ClassifyError(output)
	if env == nil {
		t.Fatal("expected non-nil envelope")
	}
	if env.Triage != TriageRefine {
		t.Errorf("expected REFINE when build succeeds but tests fail, got %s", env.Triage)
	}
}

func TestParseBuildErrors_Multiple(t *testing.T) {
	output := `internal/foo.go:5:2: could not import "bar"
internal/baz.go:10:3: cannot use x as y in argument`
	errs := ParseBuildErrors(output)
	if len(errs) != 2 {
		t.Fatalf("expected 2 errors, got %d", len(errs))
	}
	if errs[0].ErrorType != "missing_import" {
		t.Errorf("first error: expected missing_import, got %s", errs[0].ErrorType)
	}
	if errs[1].ErrorType != "type_error" {
		t.Errorf("second error: expected type_error, got %s", errs[1].ErrorType)
	}
}

func TestParseTestFailures_GoMultiple(t *testing.T) {
	output := `--- FAIL: TestA (0.00s)
    a_test.go:10: wrong
--- FAIL: TestB (0.00s)
    b_test.go:20: also wrong
FAIL	github.com/x/y	0.5s`
	errs := ParseTestFailures(output)
	if len(errs) != 2 {
		t.Fatalf("expected 2 test failures, got %d", len(errs))
	}
	for _, e := range errs {
		if e.Triage != TriageRefine {
			t.Errorf("expected REFINE for test failure, got %s", e.Triage)
		}
	}
}

func TestParseTestFailures_RuntimePanic(t *testing.T) {
	output := `panic: runtime error: index out of range [5] with length 3`
	errs := ParseTestFailures(output)
	if len(errs) != 1 {
		t.Fatalf("expected 1 error for panic, got %d", len(errs))
	}
	if errs[0].ErrorType != "runtime_error" {
		t.Errorf("expected runtime_error, got %s", errs[0].ErrorType)
	}
}
