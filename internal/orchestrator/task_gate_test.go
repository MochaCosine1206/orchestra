package orchestrator

import (
	"context"
	"fmt"
	"testing"
)

// mockRunner implements CommandRunner for testing.
type mockRunner struct {
	// responses maps "name arg1 arg2..." to (stdout, stderr, error)
	responses map[string]mockResponse
	// fallback is used when no matching key is found
	fallback *mockResponse
}

type mockResponse struct {
	stdout string
	stderr string
	err    error
}

func (m *mockRunner) Run(ctx context.Context, dir string, name string, args ...string) (string, string, error) {
	key := name
	if len(args) > 0 {
		key = name + " " + joinArgs(args)
	}
	if resp, ok := m.responses[key]; ok {
		return resp.stdout, resp.stderr, resp.err
	}
	if m.fallback != nil {
		return m.fallback.stdout, m.fallback.stderr, m.fallback.err
	}
	return "", "", nil
}

func joinArgs(args []string) string {
	result := ""
	for i, a := range args {
		if i > 0 {
			result += " "
		}
		result += a
	}
	return result
}

func TestTaskGate_AllPass(t *testing.T) {
	runner := &mockRunner{
		fallback: &mockResponse{stdout: "ok", stderr: "", err: nil},
	}

	result := RunTaskGateWithRunner(context.Background(), "/tmp/test", "go build ./... && go test ./...", runner)
	if !result.Passed {
		t.Error("expected gate to pass when all commands succeed")
	}
	if len(result.Errors) != 0 {
		t.Errorf("expected no errors, got %d", len(result.Errors))
	}
}

func TestTaskGate_BuildFails(t *testing.T) {
	runner := &mockRunner{
		responses: map[string]mockResponse{
			"go build ./...": {
				stdout: "",
				stderr: "internal/foo.go:5:2: could not import \"bar\"\n",
				err:    fmt.Errorf("exit status 1"),
			},
			"go test ./...": {
				stdout: "ok",
				stderr: "",
				err:    nil,
			},
		},
	}

	result := RunTaskGateWithRunner(context.Background(), "/tmp/test", "go build ./... && go test ./...", runner)
	if result.Passed {
		t.Error("expected gate to fail when build fails")
	}
	if len(result.Errors) == 0 {
		t.Error("expected errors from failed build")
	}
	if result.Triage != TriageHeal {
		t.Errorf("expected HEAL triage for missing import, got %s", result.Triage)
	}
}

func TestTaskGate_TestsFail(t *testing.T) {
	runner := &mockRunner{
		responses: map[string]mockResponse{
			"go build ./...": {
				stdout: "",
				stderr: "",
				err:    nil,
			},
			"go test ./...": {
				stdout: "--- FAIL: TestFoo (0.01s)\n    foo_test.go:10: wrong\nFAIL\n",
				stderr: "",
				err:    fmt.Errorf("exit status 1"),
			},
		},
	}

	result := RunTaskGateWithRunner(context.Background(), "/tmp/test", "go build ./... && go test ./...", runner)
	if result.Passed {
		t.Error("expected gate to fail when tests fail")
	}
	if result.Triage != TriageRefine {
		t.Errorf("expected REFINE triage for test failure, got %s", result.Triage)
	}
}

func TestTaskGate_NoTestCmd(t *testing.T) {
	// Empty worktree with no project files — should pass by default
	dir := t.TempDir()
	runner := &mockRunner{
		fallback: &mockResponse{stdout: "", stderr: "", err: nil},
	}

	result := RunTaskGateWithRunner(context.Background(), dir, "", runner)
	if !result.Passed {
		t.Error("expected gate to pass when no test command is detected")
	}
}

func TestTaskGate_GenericFailure(t *testing.T) {
	runner := &mockRunner{
		responses: map[string]mockResponse{
			"make test": {
				stdout: "some random error output\n",
				stderr: "",
				err:    fmt.Errorf("exit status 2"),
			},
		},
	}

	result := RunTaskGateWithRunner(context.Background(), "/tmp/test", "make test", runner)
	if result.Passed {
		t.Error("expected gate to fail")
	}
	if len(result.Errors) == 0 {
		t.Error("expected at least one generic error envelope")
	}
	// Generic failures should default to REFINE
	if result.Triage != TriageRefine {
		t.Errorf("expected REFINE for generic failure, got %s", result.Triage)
	}
}

func TestTaskGate_MixedBuildAndTestErrors(t *testing.T) {
	runner := &mockRunner{
		responses: map[string]mockResponse{
			"npm install": {
				stdout: "",
				stderr: "",
				err:    nil,
			},
			"npm run build": {
				stdout: "",
				stderr: "Error: Cannot find module 'lodash'\n",
				err:    fmt.Errorf("exit status 1"),
			},
			"npm test": {
				stdout: "FAIL src/App.test.tsx\n",
				stderr: "",
				err:    fmt.Errorf("exit status 1"),
			},
		},
	}

	result := RunTaskGateWithRunner(context.Background(), "/tmp/test", "npm install && npm run build && npm test", runner)
	if result.Passed {
		t.Error("expected gate to fail")
	}
	// Should have both build and test errors
	if len(result.Errors) < 2 {
		t.Errorf("expected at least 2 errors, got %d", len(result.Errors))
	}
}

func TestTaskGate_TriageEscalation(t *testing.T) {
	// If any error is REDO, overall should be REDO
	runner := &mockRunner{
		fallback: &mockResponse{
			stdout: "",
			stderr: "",
			err:    nil,
		},
	}

	// Simulate >10 errors across >5 files (REDO escalation)
	var errorLines string
	for i := 0; i < 12; i++ {
		errorLines += fmt.Sprintf("file_%c.go:10:5: undefined: Missing%d\n", rune('a'+i), i)
	}

	runner.responses = map[string]mockResponse{
		"go build ./...": {
			stdout: "",
			stderr: errorLines,
			err:    fmt.Errorf("exit status 1"),
		},
		"go test ./...": {
			stdout: "",
			stderr: "",
			err:    nil,
		},
	}

	result := RunTaskGateWithRunner(context.Background(), "/tmp/test", "go build ./... && go test ./...", runner)
	if result.Passed {
		t.Error("expected gate to fail")
	}
	if result.Triage != TriageRedo {
		t.Errorf("expected REDO for massive errors, got %s", result.Triage)
	}
}
