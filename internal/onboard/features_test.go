package onboard

import (
	"context"
	"testing"
)

// buildScanResult constructs a ScanResult with the given binaries marked as found.
// All other default deps are marked missing.
func buildScanResult(foundBinaries ...string) *ScanResult {
	found := make(map[string]bool)
	for _, b := range foundBinaries {
		found[b] = true
	}

	deps := make([]Dependency, len(defaultDeps))
	copy(deps, defaultDeps)
	for i := range deps {
		if found[deps[i].Binary] {
			deps[i].Status = StatusFound
		} else {
			deps[i].Status = StatusMissing
		}
	}

	result := &ScanResult{Dependencies: deps, AllRequired: true}
	for _, d := range deps {
		if d.Required && d.Status != StatusFound {
			result.AllRequired = false
			result.MissingRequired++
		}
	}
	return result
}

func TestFeatureGatingMissingDeps(t *testing.T) {
	t.Parallel()

	// ScanResult with docker missing
	scan := buildScanResult("go", "git", "claude", "curl")

	features := DefaultFeatures()
	avail := CheckFeatureAvailability(features, scan)

	for _, fa := range avail {
		switch fa.Feature.Name {
		case "docker-sandbox":
			if fa.Available {
				t.Errorf("docker-sandbox should be unavailable when docker is missing")
			}
			if len(fa.Missing) == 0 || fa.Missing[0] != "docker" {
				t.Errorf("docker-sandbox missing deps should include 'docker', got %v", fa.Missing)
			}
		case "core":
			if !fa.Available {
				t.Error("core should be available when go+git are present")
			}
		case "claude-integration", "mcp-servers":
			if !fa.Available {
				t.Errorf("%s should be available when claude is present", fa.Feature.Name)
			}
		case "telegram":
			if !fa.Available {
				t.Error("telegram should be available when curl is present")
			}
		case "loops":
			if !fa.Available {
				t.Error("loops should be available when go+claude are present")
			}
		}
	}
}

func TestFeatureGatingSatisfiedDeps(t *testing.T) {
	t.Parallel()

	// All deps present
	scan := buildScanResult("go", "git", "claude", "curl", "docker")

	features := DefaultFeatures()
	avail := CheckFeatureAvailability(features, scan)

	for _, fa := range avail {
		if !fa.Available {
			t.Errorf("feature %q should be available when all deps are present, missing: %v",
				fa.Feature.Name, fa.Missing)
		}
	}
}

func TestNonInteractiveMode(t *testing.T) {
	t.Parallel()

	// docker missing — docker-sandbox should be excluded
	scan := buildScanResult("go", "git", "claude", "curl")

	features := DefaultFeatures()
	selected := SelectNonInteractive(features, scan)

	names := make(map[string]bool)
	for _, f := range selected {
		names[f.Name] = true
	}

	// Should include features with satisfied deps
	for _, want := range []string{"core", "claude-integration", "mcp-servers", "telegram", "loops"} {
		if !names[want] {
			t.Errorf("non-interactive should include %q", want)
		}
	}

	// Should exclude docker-sandbox
	if names["docker-sandbox"] {
		t.Error("non-interactive should exclude docker-sandbox when docker is missing")
	}
}

func TestNonInteractiveMode_AllMissing(t *testing.T) {
	t.Parallel()

	// Nothing installed
	scan := buildScanResult()

	features := DefaultFeatures()
	selected := SelectNonInteractive(features, scan)

	if len(selected) != 0 {
		var names []string
		for _, f := range selected {
			names = append(names, f.Name)
		}
		t.Errorf("expected no features selected when all deps missing, got %v", names)
	}
}

func TestBackwardCompatibleFlags(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		claudeCode bool
		mcp        bool
		loops      bool
		all        bool
		wantNames  []string
	}{
		{
			name:      "all flag enables claude+mcp+loops",
			all:       true,
			wantNames: []string{"core", "claude-integration", "mcp-servers", "loops"},
		},
		{
			name:       "claude-code flag",
			claudeCode: true,
			wantNames:  []string{"core", "claude-integration"},
		},
		{
			name:      "mcp flag",
			mcp:       true,
			wantNames: []string{"core", "mcp-servers"},
		},
		{
			name:      "loops flag",
			loops:     true,
			wantNames: []string{"core", "loops"},
		},
		{
			name:      "no flags — core only",
			wantNames: []string{"core"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			selected := SelectFromFlags(tt.claudeCode, tt.mcp, tt.loops, tt.all)
			names := make(map[string]bool)
			for _, f := range selected {
				names[f.Name] = true
			}

			for _, want := range tt.wantNames {
				if !names[want] {
					t.Errorf("expected feature %q to be selected", want)
				}
			}

			if len(selected) != len(tt.wantNames) {
				var got []string
				for _, f := range selected {
					got = append(got, f.Name)
				}
				t.Errorf("expected %d features %v, got %d %v", len(tt.wantNames), tt.wantNames, len(selected), got)
			}
		})
	}
}

func TestFeaturesToScaffoldFlags(t *testing.T) {
	t.Parallel()

	selected := []Feature{
		{Name: "core"},
		{Name: "claude-integration"},
		{Name: "mcp-servers"},
		{Name: "loops"},
	}

	wc, wm, wl := FeaturesToScaffoldFlags(selected)
	if !wc {
		t.Error("expected writeClaude=true")
	}
	if !wm {
		t.Error("expected writeMCP=true")
	}
	if !wl {
		t.Error("expected writeLoops=true")
	}
}

func TestRunFeatureSetup(t *testing.T) {
	t.Parallel()

	var order []string
	makeSetup := func(name string) func(context.Context, string) error {
		return func(_ context.Context, _ string) error {
			order = append(order, name)
			return nil
		}
	}

	features := []Feature{
		{Name: "a", SetupFunc: makeSetup("a")},
		{Name: "b"}, // nil SetupFunc — should be skipped
		{Name: "c", SetupFunc: makeSetup("c")},
	}

	err := RunFeatureSetup(context.Background(), "/tmp/test", features)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(order) != 2 || order[0] != "a" || order[1] != "c" {
		t.Errorf("expected setup order [a, c], got %v", order)
	}
}
