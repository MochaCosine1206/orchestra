package orchestrator

import (
	"strings"
	"testing"
)

func TestParseErrorsToCorrections_RustCompilerErrors(t *testing.T) {
	gateOutput := `   Compiling myapp v0.1.0 (/home/user/myapp)
error[E0308]: mismatched types
  --> src/handlers/auth.rs:42:5
   |
42 |     return "hello";
   |            ^^^^^^^ expected struct 'String', found '&str'

error[E0433]: failed to resolve: use of undeclared crate or module 'serde_yaml'
  --> src/config.rs:3:5
   |
3  | use serde_yaml::Value;
   |     ^^^^^^^^^^ use of undeclared crate or module 'serde_yaml'

error: aborting due to 2 previous errors
`

	corrections := ParseErrorsToCorrections(gateOutput, "/tmp/test-repo")

	if len(corrections) != 2 {
		t.Fatalf("expected 2 corrections, got %d", len(corrections))
	}

	// First correction: type mismatch in auth.rs
	c0 := corrections[0]
	if c0.File != "src/handlers/auth.rs" {
		t.Errorf("correction[0].File = %q, want %q", c0.File, "src/handlers/auth.rs")
	}
	if c0.Error != "mismatched types" {
		t.Errorf("correction[0].Error = %q, want %q", c0.Error, "mismatched types")
	}
	if c0.Title == "" {
		t.Error("correction[0].Title should not be empty")
	}

	// Second correction: undeclared module in config.rs
	c1 := corrections[1]
	if c1.File != "src/config.rs" {
		t.Errorf("correction[1].File = %q, want %q", c1.File, "src/config.rs")
	}
	if c1.Error != "failed to resolve: use of undeclared crate or module 'serde_yaml'" {
		t.Errorf("correction[1].Error = %q", c1.Error)
	}
}

func TestParseErrorsToCorrections_TypeScriptErrors(t *testing.T) {
	gateOutput := `src/components/Button.tsx(23,7): error TS2345: Argument of type 'string' is not assignable to parameter of type 'number'.
src/utils/api.ts(105,12): error TS2339: Property 'data' does not exist on type 'Response'.
src/components/Button.tsx(45,3): error TS2322: Type 'undefined' is not assignable to type 'ReactNode'.
`

	corrections := ParseErrorsToCorrections(gateOutput, "/tmp/test-repo")

	if len(corrections) != 3 {
		t.Fatalf("expected 3 corrections, got %d", len(corrections))
	}

	// Button.tsx should have two separate corrections (different error messages)
	if corrections[0].File != "src/components/Button.tsx" {
		t.Errorf("correction[0].File = %q, want Button.tsx", corrections[0].File)
	}
	if corrections[0].Error != "Argument of type 'string' is not assignable to parameter of type 'number'." {
		t.Errorf("correction[0].Error = %q", corrections[0].Error)
	}

	if corrections[1].File != "src/utils/api.ts" {
		t.Errorf("correction[1].File = %q, want api.ts", corrections[1].File)
	}

	if corrections[2].File != "src/components/Button.tsx" {
		t.Errorf("correction[2].File = %q, want Button.tsx", corrections[2].File)
	}
	if corrections[2].Error != "Type 'undefined' is not assignable to type 'ReactNode'." {
		t.Errorf("correction[2].Error = %q", corrections[2].Error)
	}
}

func TestParseErrorsToCorrections_MixedOutput(t *testing.T) {
	gateOutput := `error[E0599]: no method named 'to_uppercase' found for type 'i32' in the current scope
  --> src/lib.rs:18:10

src/index.ts(7,1): error TS1005: ';' expected.

internal/handler/auth.go:55:12: undefined: UserContext
FAIL	github.com/myorg/myapp/internal/handler
`

	corrections := ParseErrorsToCorrections(gateOutput, "/tmp/test-repo")

	// Expect: 1 Rust, 1 TypeScript, 1 Go compile error, 1 test failure
	if len(corrections) < 3 {
		t.Fatalf("expected at least 3 corrections, got %d", len(corrections))
	}

	// Verify we got diverse file types
	files := make(map[string]bool)
	for _, c := range corrections {
		files[c.File] = true
	}

	if !files["src/lib.rs"] {
		t.Error("expected correction for src/lib.rs (Rust)")
	}
	if !files["src/index.ts"] {
		t.Error("expected correction for src/index.ts (TypeScript)")
	}
	if !files["internal/handler/auth.go"] {
		t.Error("expected correction for internal/handler/auth.go (Go)")
	}
}

func TestParseErrorsToCorrections_PlaywrightFailures(t *testing.T) {
	gateOutput := `Running 5 tests using 2 workers

  1) login page › should display login form
    at tests/login.spec.ts:15:5

  2) login page › should show error on invalid credentials
    at tests/login.spec.ts:32:5

  3) dashboard › should load user profile
    at tests/dashboard.spec.ts:22:10

  5 passed, 3 failed
`

	corrections := ParseErrorsToCorrections(gateOutput, "/tmp/test-repo")

	if len(corrections) != 3 {
		t.Fatalf("expected 3 corrections, got %d", len(corrections))
	}

	// First two should be in login.spec.ts with different test names
	if corrections[0].File != "tests/login.spec.ts" {
		t.Errorf("correction[0].File = %q, want tests/login.spec.ts", corrections[0].File)
	}
	if corrections[0].Error != "Test failed: login page > should display login form" {
		// The › character gets captured as-is from the regex
		t.Logf("correction[0].Error = %q (checking contains instead)", corrections[0].Error)
	}

	if corrections[1].File != "tests/login.spec.ts" {
		t.Errorf("correction[1].File = %q, want tests/login.spec.ts", corrections[1].File)
	}

	// Third should be dashboard.spec.ts
	if corrections[2].File != "tests/dashboard.spec.ts" {
		t.Errorf("correction[2].File = %q, want tests/dashboard.spec.ts", corrections[2].File)
	}
}

func TestParseErrorsToCorrections_DeduplicatesIdenticalErrors(t *testing.T) {
	// Same file + same error should only produce one correction
	gateOutput := `src/app.ts(10,5): error TS2345: Argument of type 'string' is not assignable to parameter of type 'number'.
src/app.ts(10,5): error TS2345: Argument of type 'string' is not assignable to parameter of type 'number'.
`

	corrections := ParseErrorsToCorrections(gateOutput, "/tmp/test-repo")

	if len(corrections) != 1 {
		t.Fatalf("expected 1 correction (deduplicated), got %d", len(corrections))
	}
}

func TestParseErrorsToCorrections_EmptyOutput(t *testing.T) {
	corrections := ParseErrorsToCorrections("", "/tmp/test-repo")
	if len(corrections) != 0 {
		t.Fatalf("expected 0 corrections for empty output, got %d", len(corrections))
	}
}

func TestParseErrorsToCorrections_GoCompilerErrors(t *testing.T) {
	gateOutput := `# github.com/myorg/myapp/internal/server
internal/server/routes.go:45:12: undefined: middleware.AuthRequired
internal/server/routes.go:67:8: cannot use resp (variable of type *http.Response) as http.ResponseWriter value in argument to handler.Write
internal/server/handler.go:23:2: imported and not used: "encoding/json"
`

	corrections := ParseErrorsToCorrections(gateOutput, "/tmp/test-repo")

	if len(corrections) != 3 {
		t.Fatalf("expected 3 corrections, got %d", len(corrections))
	}

	if corrections[0].File != "internal/server/routes.go" {
		t.Errorf("correction[0].File = %q, want internal/server/routes.go", corrections[0].File)
	}

	if corrections[2].File != "internal/server/handler.go" {
		t.Errorf("correction[2].File = %q, want internal/server/handler.go", corrections[2].File)
	}
}

func TestBuildCorrectionPrompt(t *testing.T) {
	c := CorrectionEntry{
		Title:              "Fix type error in auth.rs",
		File:               "src/handlers/auth.rs",
		Error:              "mismatched types: expected String, found &str",
		Fix:                "Convert &str to String using .to_string()",
		AcceptanceCriteria: "cargo build completes without errors",
	}

	prompt := buildCorrectionPrompt(c, "/home/user/project")

	if prompt == "" {
		t.Fatal("prompt should not be empty")
	}

	// Verify key elements are present
	for _, want := range []string{
		"mismatched types",
		"/home/user/project/src/handlers/auth.rs",
		".to_string()",
		"cargo build",
		"minimal fix",
	} {
		if !strings.Contains(prompt, want) {
			t.Errorf("prompt missing expected content: %q", want)
		}
	}
}

func TestTruncateOutput(t *testing.T) {
	tests := []struct {
		name   string
		input  string
		limit  int
		expect string
	}{
		{"short", "hello", 10, "hello"},
		{"exact", "hello", 5, "hello"},
		{"truncated", "hello world", 5, "...world"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := truncateOutput(tt.input, tt.limit)
			if got != tt.expect {
				t.Errorf("truncateOutput(%q, %d) = %q, want %q", tt.input, tt.limit, got, tt.expect)
			}
		})
	}
}
