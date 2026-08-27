package cmd

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
)

// initGitRepo creates a minimal git repo in dir with one commit on the given branch.
func initGitRepo(t *testing.T, dir, branch string) {
	t.Helper()
	cmds := [][]string{
		{"git", "init", "-b", branch},
		{"git", "config", "user.email", "test@test.com"},
		{"git", "config", "user.name", "Test"},
	}
	for _, args := range cmds {
		c := exec.Command(args[0], args[1:]...)
		c.Dir = dir
		if out, err := c.CombinedOutput(); err != nil {
			t.Fatalf("%v failed: %s", args, out)
		}
	}
	// Create a file and commit so HEAD exists.
	if err := os.WriteFile(dir+"/init.txt", []byte("init"), 0644); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"git", "add", "."},
		{"git", "commit", "-m", "initial"},
	} {
		c := exec.Command(args[0], args[1:]...)
		c.Dir = dir
		if out, err := c.CombinedOutput(); err != nil {
			t.Fatalf("%v failed: %s", args, out)
		}
	}
}

// chdirTo changes to dir and returns a cleanup that restores the original dir.
func chdirTo(t *testing.T, dir string) {
	t.Helper()
	orig, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chdir(orig) })
}

// mockGitRunner returns a gitRunner replacement that responds based on a map
// of command prefixes to (output, error) pairs. Unmatched commands return an error.
type mockGitResult struct {
	output string
	err    error
}

func newMockGitRunner(responses map[string]mockGitResult) func(...string) (string, error) {
	return func(args ...string) (string, error) {
		key := strings.Join(args, " ")
		// Try exact match first, then prefix match on first arg.
		if r, ok := responses[key]; ok {
			return r.output, r.err
		}
		if len(args) > 0 {
			if r, ok := responses[args[0]]; ok {
				return r.output, r.err
			}
		}
		return "", fmt.Errorf("mock: unhandled git %s", key)
	}
}

// setMockGit installs a mock gitRunner and restores the original on cleanup.
func setMockGit(t *testing.T, mock func(...string) (string, error)) {
	t.Helper()
	old := gitRunner
	gitRunner = mock
	t.Cleanup(func() { gitRunner = old })
}

func TestReleaseMissingVersion(t *testing.T) {
	root := NewRootCmd()
	buf := new(bytes.Buffer)
	root.SetOut(buf)
	root.SetErr(buf)
	root.SetArgs([]string{"release"})

	err := root.Execute()
	if err == nil {
		t.Fatal("expected error for missing version")
	}
	if !strings.Contains(err.Error(), "version required") {
		t.Errorf("expected 'version required' error, got: %v", err)
	}
}

func TestReleaseVersionAutoPrefix(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir, "main")
	chdirTo(t, dir)

	root := NewRootCmd()
	buf := new(bytes.Buffer)
	root.SetOut(buf)
	root.SetErr(buf)
	root.SetArgs([]string{"release", "1.0.0", "--skip-tests", "--dry-run"})

	err := root.Execute()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "v1.0.0") {
		t.Errorf("expected auto-prefixed 'v1.0.0' in output, got: %s", out)
	}
}

func TestReleaseSemverValidation(t *testing.T) {
	tests := []struct {
		name    string
		version string
		wantErr string
	}{
		{"bare_word", "abc", "invalid semver version"},
		{"missing_patch", "v1.2", "invalid semver version"},
		{"too_many_parts", "1.2.3.4", "invalid semver version"},
		{"empty_segments", "v1..3", "invalid semver version"},
		{"letters_in_version", "v1.two.3", "invalid semver version"},
		// Valid versions should not error (tested implicitly by other tests).
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			root := NewRootCmd()
			buf := new(bytes.Buffer)
			root.SetOut(buf)
			root.SetErr(buf)
			root.SetArgs([]string{"release", tc.version, "--dry-run", "--skip-tests"})

			err := root.Execute()
			if err == nil {
				t.Fatalf("expected error for version %q, got nil", tc.version)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("expected error containing %q, got: %v", tc.wantErr, err)
			}
		})
	}
}

// TestReleaseGateFailures covers gates 1, 2, and 4 using table-driven subtests.
func TestReleaseGateFailures(t *testing.T) {
	tests := []struct {
		name    string
		setup   func(t *testing.T, dir string) // extra setup after initGitRepo
		branch  string
		version string
		flags   []string
		wantErr string
	}{
		{
			name:    "gate1_dirty_tree",
			branch:  "main",
			version: "v1.0.0",
			setup: func(t *testing.T, dir string) {
				os.WriteFile(dir+"/dirty.txt", []byte("dirty"), 0644)
			},
			wantErr: "uncommitted changes",
		},
		{
			name:    "gate2_wrong_branch",
			branch:  "feature-branch",
			version: "v1.0.0",
			wantErr: "must be on dev or main",
		},
		{
			name:    "gate4_existing_tag",
			branch:  "main",
			version: "v1.0.0",
			flags:   []string{"--skip-tests"},
			setup: func(t *testing.T, dir string) {
				c := exec.Command("git", "tag", "v1.0.0")
				c.Dir = dir
				if out, err := c.CombinedOutput(); err != nil {
					t.Fatalf("git tag failed: %s", out)
				}
			},
			wantErr: "already exists",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			initGitRepo(t, dir, tc.branch)
			chdirTo(t, dir)
			if tc.setup != nil {
				tc.setup(t, dir)
			}

			root := NewRootCmd()
			buf := new(bytes.Buffer)
			root.SetOut(buf)
			root.SetErr(buf)
			args := []string{"release", tc.version}
			args = append(args, tc.flags...)
			root.SetArgs(args)

			err := root.Execute()
			if err == nil {
				t.Fatalf("expected error containing %q, got nil", tc.wantErr)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("expected error containing %q, got: %v", tc.wantErr, err)
			}
		})
	}
}

func TestReleaseGate3SkipTests(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir, "main")
	chdirTo(t, dir)

	root := NewRootCmd()
	buf := new(bytes.Buffer)
	root.SetOut(buf)
	root.SetErr(buf)
	root.SetArgs([]string{"release", "v1.0.0", "--skip-tests", "--dry-run"})

	err := root.Execute()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "SKIPPED") {
		t.Errorf("expected 'SKIPPED' in output for --skip-tests, got: %s", out)
	}
}

func TestReleaseDryRun(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir, "dev")
	chdirTo(t, dir)

	root := NewRootCmd()
	buf := new(bytes.Buffer)
	root.SetOut(buf)
	root.SetErr(buf)
	root.SetArgs([]string{"release", "v2.0.0", "--skip-tests", "--dry-run"})

	err := root.Execute()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "Dry run") {
		t.Errorf("expected 'Dry run' in output, got: %s", out)
	}

	// Verify no tag was created.
	c := exec.Command("git", "rev-parse", "v2.0.0")
	c.Dir = dir
	if err := c.Run(); err == nil {
		t.Error("tag v2.0.0 should not exist after dry run")
	}
}

func TestReleaseGate5GoreleaserCheck(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir, "main")
	chdirTo(t, dir)

	root := NewRootCmd()
	buf := new(bytes.Buffer)
	root.SetOut(buf)
	root.SetErr(buf)
	root.SetArgs([]string{"release", "v3.0.0", "--skip-tests", "--dry-run"})

	err := root.Execute()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	out := buf.String()
	// Gate 5 should either PASS or SKIP depending on whether goreleaser is installed.
	if !strings.Contains(out, "GoReleaser config") {
		t.Errorf("expected gate 5 (GoReleaser config) in output, got: %s", out)
	}
	if !strings.Contains(out, "PASS") && !strings.Contains(out, "SKIP") {
		t.Errorf("expected PASS or SKIP for goreleaser gate, got: %s", out)
	}
}

func TestGoreleaserConfigPath(t *testing.T) {
	if goreleaserConfig != ".goreleaser.yaml" {
		t.Errorf("expected goreleaserConfig to be .goreleaser.yaml, got %q", goreleaserConfig)
	}
}

func TestReleaseCommandFlags(t *testing.T) {
	cmd := NewReleaseCmd()

	dryRun := cmd.Flags().Lookup("dry-run")
	if dryRun == nil {
		t.Error("--dry-run flag not registered")
	}

	skipTests := cmd.Flags().Lookup("skip-tests")
	if skipTests == nil {
		t.Error("--skip-tests flag not registered")
	}
}

func TestReleaseDevBranch(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir, "dev")
	chdirTo(t, dir)

	root := NewRootCmd()
	buf := new(bytes.Buffer)
	root.SetOut(buf)
	root.SetErr(buf)
	root.SetArgs([]string{"release", "v1.0.0", "--skip-tests", "--dry-run"})

	err := root.Execute()
	if err != nil {
		t.Fatalf("expected release on dev branch to succeed, got: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "PASS (dev)") {
		t.Errorf("expected 'PASS (dev)' for branch gate, got: %s", out)
	}
}

func TestReleaseAllGatesPass(t *testing.T) {
	// Use mock gitRunner so we don't need a real repo for all gates.
	mock := newMockGitRunner(map[string]mockGitResult{
		"status --porcelain":    {output: ""},
		"branch --show-current": {output: "main"},
		"rev-parse v1.0.0":      {output: "", err: fmt.Errorf("not a tag")},
	})
	setMockGit(t, mock)

	root := NewRootCmd()
	buf := new(bytes.Buffer)
	root.SetOut(buf)
	root.SetErr(buf)
	root.SetArgs([]string{"release", "v1.0.0", "--skip-tests", "--dry-run"})

	err := root.Execute()
	if err != nil {
		t.Fatalf("expected all gates to pass, got: %v", err)
	}

	out := buf.String()
	// Verify all 5 gate lines appear with PASS or SKIP.
	gates := []string{
		"[1/5]", "[2/5]", "[3/5]", "[4/5]", "[5/5]",
	}
	for _, g := range gates {
		idx := strings.Index(out, g)
		if idx == -1 {
			t.Errorf("gate %s not found in output", g)
			continue
		}
		// Get the line containing this gate.
		lineEnd := strings.Index(out[idx:], "\n")
		if lineEnd == -1 {
			lineEnd = len(out) - idx
		}
		line := out[idx : idx+lineEnd]
		if !strings.Contains(line, "PASS") && !strings.Contains(line, "SKIP") {
			t.Errorf("gate %s missing PASS or SKIP: %q", g, line)
		}
	}
}

func TestReleaseGate3TestFailure(t *testing.T) {
	// Mock git gates to pass, but the real test command will fail because
	// we're in a temp dir with no Go module. We need to use a real repo
	// that has a failing test. Instead, use a mock approach: create a
	// minimal Go project with a failing test.
	dir := t.TempDir()
	initGitRepo(t, dir, "main")
	chdirTo(t, dir)

	// Create a minimal go.mod and failing test.
	os.WriteFile(dir+"/go.mod", []byte("module testfail\n\ngo 1.21\n"), 0644)
	os.WriteFile(dir+"/fail_test.go", []byte(`package testfail

import "testing"

func TestAlwaysFail(t *testing.T) {
	t.Fatal("intentional failure")
}
`), 0644)
	// Commit so working tree is clean for gate 1.
	for _, args := range [][]string{
		{"git", "add", "."},
		{"git", "commit", "-m", "add failing test"},
	} {
		c := exec.Command(args[0], args[1:]...)
		c.Dir = dir
		if out, err := c.CombinedOutput(); err != nil {
			t.Fatalf("%v failed: %s", args, out)
		}
	}

	root := NewRootCmd()
	buf := new(bytes.Buffer)
	root.SetOut(buf)
	root.SetErr(buf)
	// Do NOT pass --skip-tests so gate 3 actually runs.
	root.SetArgs([]string{"release", "v1.0.0"})

	err := root.Execute()
	if err == nil {
		t.Fatal("expected error from failing tests, got nil")
	}
	if !strings.Contains(err.Error(), "tests failed") {
		t.Errorf("expected 'tests failed' error, got: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "[3/5]") || !strings.Contains(out, "FAIL") {
		t.Errorf("expected gate 3 FAIL in output, got: %s", out)
	}
}
