package quality

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"testing"
)

// mockRunner implements CommandRunner for testing.
type mockRunner struct {
	output []byte
	err    error
}

func (m *mockRunner) Run(name string, args ...string) ([]byte, error) {
	return m.output, m.err
}

func (m *mockRunner) RunDir(dir string, name string, args ...string) ([]byte, error) {
	return m.output, m.err
}

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
}

func TestBuildGate(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		output     string
		err        error
		wantPassed bool
	}{
		{
			name:       "build succeeds",
			output:     "",
			err:        nil,
			wantPassed: true,
		},
		{
			name:       "build fails with errors",
			output:     "main.go:10:5: undefined: foo",
			err:        fmt.Errorf("running go: %w", errors.New("exit status 1")),
			wantPassed: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			gate := &BuildGate{
				Runner: &mockRunner{output: []byte(tt.output), err: tt.err},
				Logger: testLogger(),
			}
			result, err := gate.Check(context.Background(), "/tmp/project", "main")
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if result.Passed != tt.wantPassed {
				t.Errorf("Passed = %v, want %v", result.Passed, tt.wantPassed)
			}
			if result.Layer != "build" {
				t.Errorf("Layer = %q, want %q", result.Layer, "build")
			}
		})
	}
}

func TestLintGate(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		output     string
		err        error
		wantPassed bool
	}{
		{
			name:       "no lint issues",
			output:     "",
			err:        nil,
			wantPassed: true,
		},
		{
			name:       "lint issues found",
			output:     "main.go:5:1: exported function Foo should have comment",
			err:        fmt.Errorf("running golangci-lint: %w", errors.New("exit status 1")),
			wantPassed: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			gate := &LintGate{
				Runner: &mockRunner{output: []byte(tt.output), err: tt.err},
				Logger: testLogger(),
			}
			result, err := gate.Check(context.Background(), "/tmp/project", "main")
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if result.Passed != tt.wantPassed {
				t.Errorf("Passed = %v, want %v", result.Passed, tt.wantPassed)
			}
			if result.Layer != "lint" {
				t.Errorf("Layer = %q, want %q", result.Layer, "lint")
			}
		})
	}
}

func TestSecurityGate(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		output     string
		err        error
		wantPassed bool
		wantScore  float64
	}{
		{
			name:       "no security issues",
			output:     "",
			err:        nil,
			wantPassed: true,
			wantScore:  1.0,
		},
		{
			name:       "high severity finding",
			output:     "[/tmp/main.go:10] - Severity: HIGH - Confidence: HIGH\nG101: Potential hardcoded credentials",
			err:        fmt.Errorf("running gosec: %w", errors.New("exit status 1")),
			wantPassed: false,
			wantScore:  0.0,
		},
		{
			name:       "critical severity finding",
			output:     "[/tmp/main.go:20] - Severity: CRITICAL - Confidence: HIGH\nG401: Use of weak cryptographic primitive",
			err:        fmt.Errorf("running gosec: %w", errors.New("exit status 1")),
			wantPassed: false,
			wantScore:  0.0,
		},
		{
			name:       "low severity only (warnings)",
			output:     "[/tmp/main.go:30] - Severity: LOW - Confidence: MEDIUM\nG104: Errors unhandled",
			err:        fmt.Errorf("running gosec: %w", errors.New("exit status 1")),
			wantPassed: true,
			wantScore:  0.5,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			gate := &SecurityGate{
				Runner: &mockRunner{output: []byte(tt.output), err: tt.err},
				Logger: testLogger(),
			}
			result, err := gate.Check(context.Background(), "/tmp/project", "main")
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if result.Passed != tt.wantPassed {
				t.Errorf("Passed = %v, want %v", result.Passed, tt.wantPassed)
			}
			if result.Score != tt.wantScore {
				t.Errorf("Score = %v, want %v", result.Score, tt.wantScore)
			}
			if result.Layer != "security" {
				t.Errorf("Layer = %q, want %q", result.Layer, "security")
			}
		})
	}
}

func TestTestGate(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		output     string
		err        error
		wantPassed bool
		wantScore  float64
	}{
		{
			name:       "tests pass with coverage",
			output:     "ok  \tgithub.com/example/pkg\t0.5s\tcoverage: 85.3% of statements",
			err:        nil,
			wantPassed: true,
			wantScore:  85.3,
		},
		{
			name:       "tests fail",
			output:     "--- FAIL: TestFoo (0.00s)\nFAIL\tgithub.com/example/pkg\t0.5s",
			err:        fmt.Errorf("running go: %w", errors.New("exit status 1")),
			wantPassed: false,
			wantScore:  0.0,
		},
		{
			name:       "tests pass no coverage line",
			output:     "ok  \tgithub.com/example/pkg\t0.5s",
			err:        nil,
			wantPassed: true,
			wantScore:  0.0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			gate := &TestGate{
				Runner: &mockRunner{output: []byte(tt.output), err: tt.err},
				Logger: testLogger(),
			}
			result, err := gate.Check(context.Background(), "/tmp/project", "main")
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if result.Passed != tt.wantPassed {
				t.Errorf("Passed = %v, want %v", result.Passed, tt.wantPassed)
			}
			if result.Score != tt.wantScore {
				t.Errorf("Score = %v, want %v", result.Score, tt.wantScore)
			}
			if result.Layer != "test" {
				t.Errorf("Layer = %q, want %q", result.Layer, "test")
			}
		})
	}
}

func TestParseCoveragePercent(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		input string
		want  float64
	}{
		{
			name:  "standard coverage line",
			input: "coverage: 78.5% of statements",
			want:  78.5,
		},
		{
			name:  "100% coverage",
			input: "coverage: 100.0% of statements",
			want:  100.0,
		},
		{
			name:  "zero coverage",
			input: "coverage: 0.0% of statements",
			want:  0.0,
		},
		{
			name:  "no coverage line",
			input: "ok  github.com/example/pkg  0.5s",
			want:  0.0,
		},
		{
			name: "multiple packages, uses last",
			input: `ok  github.com/example/pkg1  0.3s  coverage: 60.0% of statements
ok  github.com/example/pkg2  0.2s  coverage: 90.5% of statements`,
			want: 90.5,
		},
		{
			name:  "integer coverage",
			input: "coverage: 42% of statements",
			want:  42.0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := parseCoveragePercent(tt.input)
			if got != tt.want {
				t.Errorf("parseCoveragePercent() = %v, want %v", got, tt.want)
			}
		})
	}
}
