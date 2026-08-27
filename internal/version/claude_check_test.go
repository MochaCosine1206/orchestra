package version

import (
	"testing"
)

func TestExtractSemver(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"claude 2.1.83", "2.1.83"},
		{"Claude Code v2.2.0", "2.2.0"},
		{"3.0.0", "3.0.0"},
		{"some output 1.2.3-beta", "1.2.3"},
		{"no version here", ""},
		{"", ""},
	}
	for _, tt := range tests {
		got := extractSemver(tt.input)
		if got != tt.want {
			t.Errorf("extractSemver(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestParseSemver(t *testing.T) {
	tests := []struct {
		input   string
		want    [3]int
		wantErr bool
	}{
		{"2.1.83", [3]int{2, 1, 83}, false},
		{"v3.0.0", [3]int{3, 0, 0}, false},
		{"0.0.1", [3]int{0, 0, 1}, false},
		{"1.2", [3]int{}, true},
		{"abc.def.ghi", [3]int{}, true},
		{"", [3]int{}, true},
	}
	for _, tt := range tests {
		got, err := parseSemver(tt.input)
		if (err != nil) != tt.wantErr {
			t.Errorf("parseSemver(%q) error = %v, wantErr %v", tt.input, err, tt.wantErr)
			continue
		}
		if got != tt.want {
			t.Errorf("parseSemver(%q) = %v, want %v", tt.input, got, tt.want)
		}
	}
}

func TestCompareSemver(t *testing.T) {
	tests := []struct {
		a, b [3]int
		want int
	}{
		{[3]int{2, 1, 83}, [3]int{2, 2, 0}, -1},
		{[3]int{2, 2, 0}, [3]int{2, 1, 83}, 1},
		{[3]int{3, 0, 0}, [3]int{2, 9, 99}, 1},
		{[3]int{1, 0, 0}, [3]int{1, 0, 0}, 0},
		{[3]int{1, 0, 0}, [3]int{1, 0, 1}, -1},
		{[3]int{0, 0, 1}, [3]int{0, 0, 0}, 1},
	}
	for _, tt := range tests {
		got := compareSemver(tt.a, tt.b)
		if got != tt.want {
			t.Errorf("compareSemver(%v, %v) = %d, want %d", tt.a, tt.b, got, tt.want)
		}
	}
}

func TestCheckClaudeVersion_InvalidMinVersion(t *testing.T) {
	result := CheckClaudeVersion("not-a-version")
	if result.Err == nil {
		t.Error("expected error for invalid minVersion")
	}
}
