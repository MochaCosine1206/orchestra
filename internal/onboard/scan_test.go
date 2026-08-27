package onboard

import (
	"context"
	"fmt"
	"testing"
	"time"
)

// mockRunner implements CommandRunner for testing.
type mockRunner struct {
	results map[string]mockResult
}

type mockResult struct {
	output []byte
	err    error
	delay  time.Duration
}

func (m *mockRunner) Run(ctx context.Context, name string, args ...string) ([]byte, error) {
	r, ok := m.results[name]
	if !ok {
		return nil, fmt.Errorf("command not found: %s", name)
	}
	if r.delay > 0 {
		select {
		case <-time.After(r.delay):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	return r.output, r.err
}

func newAllFoundRunner() *mockRunner {
	return &mockRunner{results: map[string]mockResult{
		"go":            {output: []byte("go version go1.22.0 linux/amd64")},
		"git":           {output: []byte("git version 2.43.0")},
		"claude":        {output: []byte("claude 1.0.0")},
		"jq":            {output: []byte("jq-1.7")},
		"curl":          {output: []byte("curl 8.4.0")},
		"flock":         {output: []byte("flock 2.39")},
		"docker":        {output: []byte("Docker version 24.0.7, build afdd53b")},
		"sqlite3":       {output: []byte("3.43.2 2023-10-10")},
		"node":          {output: []byte("v20.10.0")},
		"gh":            {output: []byte("gh version 2.40.1 (2023-12-13)")},
		"goreleaser":    {output: []byte("goreleaser version 1.23.0")},
		"golangci-lint": {output: []byte("golangci-lint has version 1.55.2")},
	}}
}

func TestScanEnvironment_AllFound(t *testing.T) {
	t.Parallel()
	runner := newAllFoundRunner()
	result := ScanEnvironment(context.Background(), runner)

	if !result.AllRequired {
		t.Error("expected AllRequired=true when all deps are found")
	}
	if result.MissingRequired != 0 {
		t.Errorf("expected MissingRequired=0, got %d", result.MissingRequired)
	}
	if len(result.Dependencies) < 12 {
		t.Errorf("expected at least 12 dependencies, got %d", len(result.Dependencies))
	}
	for _, d := range result.Dependencies {
		if d.Status != StatusFound {
			t.Errorf("dependency %q: expected found, got %s", d.Name, d.Status)
		}
	}
}

func TestScanEnvironment_MissingRequired(t *testing.T) {
	t.Parallel()
	runner := newAllFoundRunner()
	runner.results["go"] = mockResult{err: fmt.Errorf("not found")}

	result := ScanEnvironment(context.Background(), runner)

	if result.AllRequired {
		t.Error("expected AllRequired=false when 'go' is missing")
	}
	if result.MissingRequired < 1 {
		t.Errorf("expected MissingRequired>=1, got %d", result.MissingRequired)
	}

	for _, d := range result.Dependencies {
		if d.Name == "Go" && d.Status != StatusMissing {
			t.Errorf("Go dep: expected missing, got %s", d.Status)
		}
	}
}

func TestScanEnvironment_Timeout(t *testing.T) {
	t.Parallel()
	runner := newAllFoundRunner()
	// Make docker take 5 seconds — exceeds the 2s timeout
	runner.results["docker"] = mockResult{delay: 5 * time.Second}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	start := time.Now()
	result := ScanEnvironment(ctx, runner)
	elapsed := time.Since(start)

	// Scan should complete well under 10s (the per-dep timeout is 2s)
	if elapsed > 5*time.Second {
		t.Errorf("scan took too long: %v (expected <5s due to parallel 2s timeouts)", elapsed)
	}

	for _, d := range result.Dependencies {
		if d.Name == "Docker" {
			if d.Status != StatusTimeout {
				t.Errorf("Docker dep: expected timeout, got %s", d.Status)
			}
			return
		}
	}
	t.Error("Docker dependency not found in results")
}

func TestScanEnvironment_VersionParsing(t *testing.T) {
	t.Parallel()
	runner := newAllFoundRunner()
	result := ScanEnvironment(context.Background(), runner)

	expected := map[string]string{
		"Go":         "1.22.0",
		"Git":        "2.43.0",
		"curl":       "8.4.0",
		"Docker":     "24.0.7",
		"SQLite3":    "3.43.2",
		"Node.js":    "20.10.0",
		"GitHub CLI": "2.40.1",
	}

	for _, d := range result.Dependencies {
		if want, ok := expected[d.Name]; ok {
			if d.Version != want {
				t.Errorf("%s: expected version %q, got %q", d.Name, want, d.Version)
			}
		}
	}
}
