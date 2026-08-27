package cmd

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/MochaCosine1206/orchestra/internal/onboard"
)

// doctorMockRunner implements onboard.CommandRunner for doctor cmd tests.
type doctorMockRunner struct {
	results map[string]doctorMockResult
}

type doctorMockResult struct {
	output []byte
	err    error
}

func (m *doctorMockRunner) Run(ctx context.Context, name string, args ...string) ([]byte, error) {
	r, ok := m.results[name]
	if !ok {
		return nil, fmt.Errorf("command not found: %s", name)
	}
	return r.output, r.err
}

func newDoctorAllFoundRunner() *doctorMockRunner {
	return &doctorMockRunner{results: map[string]doctorMockResult{
		"go":            {output: []byte("go version go1.22.0 linux/amd64")},
		"git":           {output: []byte("git version 2.43.0")},
		"claude":        {output: []byte("claude 1.0.0")},
		"jq":            {output: []byte("jq-1.7")},
		"curl":          {output: []byte("curl 8.4.0")},
		"flock":         {output: []byte("flock 2.39")},
		"docker":        {output: []byte("Docker version 24.0.7")},
		"sqlite3":       {output: []byte("3.43.2")},
		"node":          {output: []byte("v20.10.0")},
		"gh":            {output: []byte("gh version 2.40.1")},
		"goreleaser":    {output: []byte("goreleaser version 1.23.0")},
		"golangci-lint": {output: []byte("golangci-lint has version 1.55.2")},
	}}
}

func executeDoctor(t *testing.T, runner onboard.CommandRunner) error {
	t.Helper()
	old := scanRunner
	scanRunner = runner
	t.Cleanup(func() { scanRunner = old })

	root := NewRootCmd()
	root.SetArgs([]string{"doctor"})
	return root.Execute()
}

func TestDoctorCommand_Exists(t *testing.T) {
	old := scanRunner
	scanRunner = newDoctorAllFoundRunner()
	defer func() { scanRunner = old }()

	root := NewRootCmd()
	root.SetArgs([]string{"doctor", "--help"})
	if err := root.Execute(); err != nil {
		t.Fatalf("doctor --help failed: %v", err)
	}
}

func TestDoctorCommand_AllFound(t *testing.T) {
	runner := newDoctorAllFoundRunner()
	if err := executeDoctor(t, runner); err != nil {
		t.Fatalf("expected no error when all deps found, got: %v", err)
	}
}

func TestDoctorCommand_ExitCode(t *testing.T) {
	runner := newDoctorAllFoundRunner()
	runner.results["go"] = doctorMockResult{err: fmt.Errorf("not found")}

	err := executeDoctor(t, runner)
	if err == nil {
		t.Fatal("expected error when required dep is missing, got nil")
	}
}

func TestDoctorCommand_Timeout(t *testing.T) {
	runner := &slowRunner{base: newDoctorAllFoundRunner()}
	start := time.Now()
	_ = executeDoctor(t, runner)
	if time.Since(start) > 10*time.Second {
		t.Error("doctor command took too long — possible hang")
	}
}

// slowRunner wraps a mock but makes "docker" take 5s.
type slowRunner struct {
	base *doctorMockRunner
}

func (s *slowRunner) Run(ctx context.Context, name string, args ...string) ([]byte, error) {
	if name == "docker" {
		select {
		case <-time.After(5 * time.Second):
			return []byte("Docker version 24.0.7"), nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	return s.base.Run(ctx, name, args...)
}
