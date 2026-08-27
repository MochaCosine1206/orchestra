package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// validSpecYAML is a minimal valid spec for testing the exec command.
const validSpecYAML = `
version: "1"
metadata:
  title: "Test Project"
  description: "A test spec"
phases:
  - id: "phase-1"
    name: "Foundation"
    description: "Build foundation"
    depends_on: []
    gate:
      test_cmd: "echo ok"
      acceptance:
        - "It compiles"
    tasks:
      - title: "Scaffold"
        role: "implementer"
        files:
          - "main.go"
        description: "Set up project"
        acceptance_criteria:
          - "Project compiles"
  - id: "phase-2"
    name: "API Layer"
    description: "Build API"
    depends_on: ["phase-1"]
    gate:
      test_cmd: "go test ./..."
      acceptance:
        - "Tests pass"
    tasks:
      - title: "Handlers"
        role: "implementer"
        files:
          - "handlers.go"
        description: "Implement handlers"
        acceptance_criteria:
          - "Endpoints work"
      - title: "Router"
        role: "architect"
        files:
          - "router.go"
        description: "Wire routes"
        acceptance_criteria:
          - "Routes registered"
`

func writeSpecFile(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, "spec.yaml")
	if err := os.WriteFile(p, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestExecCmd_MissingSpec(t *testing.T) {
	root := NewRootCmd()
	root.SetArgs([]string{"exec"})
	err := root.Execute()
	if err == nil {
		t.Fatal("expected error when --spec is missing")
	}
	if !strings.Contains(err.Error(), "required flag") {
		t.Errorf("expected 'required flag' error, got: %v", err)
	}
}

func TestExecCmd_InvalidSpecPath(t *testing.T) {
	root := NewRootCmd()
	root.SetArgs([]string{"exec", "--spec", "/nonexistent/path/spec.yaml", "--dry-run"})
	err := root.Execute()
	if err == nil {
		t.Fatal("expected error for nonexistent spec file")
	}
}

func TestExecCmd_DryRun(t *testing.T) {
	specPath := writeSpecFile(t, validSpecYAML)

	root := NewRootCmd()
	var out strings.Builder
	root.SetOut(&out)
	root.SetArgs([]string{"exec", "--spec", specPath, "--dry-run"})

	err := root.Execute()
	if err != nil {
		t.Fatalf("dry-run should succeed, got error: %v", err)
	}

	output := out.String()

	// Should print spec title
	if !strings.Contains(output, "Test Project") {
		t.Errorf("output should contain spec title, got:\n%s", output)
	}
	// Should print phase names
	if !strings.Contains(output, "Foundation") {
		t.Errorf("output should contain phase name 'Foundation', got:\n%s", output)
	}
	if !strings.Contains(output, "API Layer") {
		t.Errorf("output should contain phase name 'API Layer', got:\n%s", output)
	}
	// Should print phase IDs
	if !strings.Contains(output, "phase-1") {
		t.Errorf("output should contain phase ID 'phase-1', got:\n%s", output)
	}
	// Should print task count
	if !strings.Contains(output, "Tasks: 1") {
		t.Errorf("output should show task count, got:\n%s", output)
	}
	// Should print gate info
	if !strings.Contains(output, "echo ok") {
		t.Errorf("output should show gate command, got:\n%s", output)
	}
	// Should print dependency info for phase-2
	if !strings.Contains(output, "depends: phase-1") {
		t.Errorf("output should show dependency info, got:\n%s", output)
	}
}

func TestExecCmd_FlagDefaults(t *testing.T) {
	cmd := NewExecCmd()

	tests := []struct {
		name    string
		flag    string
		wantDef string
	}{
		{"max-parallel", "max-parallel", "8"},
		{"interval", "interval", "10"},
		{"review", "review", "false"},
		{"reconcile", "reconcile", "true"},
		{"repo-map", "repo-map", "true"},
		{"dry-run", "dry-run", "false"},
		{"runtime", "runtime", "local"},
		{"merge-mode", "merge-mode", "local"},
		{"base-branch", "base-branch", ""},
		{"start-phase", "start-phase", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := cmd.Flags().Lookup(tt.flag)
			if f == nil {
				t.Fatalf("flag %q not found", tt.flag)
			}
			if f.DefValue != tt.wantDef {
				t.Errorf("flag %q default = %q, want %q", tt.flag, f.DefValue, tt.wantDef)
			}
		})
	}
}

func TestExecCmd_DryRunInvalidSpec(t *testing.T) {
	specPath := writeSpecFile(t, `
version: "1"
metadata:
  title: "Bad Spec"
  description: "Missing phases"
phases: []
`)
	root := NewRootCmd()
	root.SetArgs([]string{"exec", "--spec", specPath, "--dry-run"})
	err := root.Execute()
	if err == nil {
		t.Fatal("expected validation error for empty phases")
	}
}
