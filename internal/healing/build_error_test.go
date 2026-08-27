package healing

import (
	"testing"
)

func TestParseBuildError(t *testing.T) {
	tests := []struct {
		name     string
		stderr   string
		wantLen  int
		wantType ErrorType
	}{
		{
			name:     "missing_import",
			stderr:   `could not import "fmt"`,
			wantLen:  1,
			wantType: ErrorMissingImport,
		},
		{
			name:     "missing_dep",
			stderr:   `cannot find module providing package foo`,
			wantLen:  1,
			wantType: ErrorMissingDep,
		},
		{
			name:     "missing_dep_gosum",
			stderr:   `missing go.sum entry for module providing package bar`,
			wantLen:  1,
			wantType: ErrorMissingDep,
		},
		{
			name:     "syntax_error",
			stderr:   `syntax error: unexpected semicolon`,
			wantLen:  1,
			wantType: ErrorSyntax,
		},
		{
			name:     "type_error_cannot_use",
			stderr:   `cannot use x (type int) as type string in argument`,
			wantLen:  1,
			wantType: ErrorTypeError,
		},
		{
			name:     "type_error_have_want",
			stderr:   `have (int) want (string)`,
			wantLen:  1,
			wantType: ErrorTypeError,
		},
		{
			name:    "empty_input",
			stderr:  "",
			wantLen: 0,
		},
		{
			name:    "no_match",
			stderr:  "some random compiler warning\nanother line",
			wantLen: 0,
		},
		{
			name:   "mixed_errors",
			stderr: "could not import \"os\"\nsyntax error: unexpected }\ncannot use 5 as string in return",
			wantLen: 3,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ParseBuildError(tt.stderr)
			if len(got) != tt.wantLen {
				t.Fatalf("ParseBuildError() returned %d errors, want %d", len(got), tt.wantLen)
			}
			if tt.wantLen == 1 && got[0].Type != tt.wantType {
				t.Errorf("got type %q, want %q", got[0].Type, tt.wantType)
			}
		})
	}
}
