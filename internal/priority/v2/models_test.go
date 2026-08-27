package priority

import (
	"testing"
)

func TestTierBaseScore(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		tier     Tier
		expected float64
	}{
		{"TierUser", TierUser, 1000},
		{"TierRetry", TierRetry, 800},
		{"TierGoalImpl", TierGoalImpl, 600},
		{"TierGoalResearch", TierGoalResearch, 400},
		{"TierValidation", TierValidation, 300},
		{"TierTechDebt", TierTechDebt, 200},
		{"TierSelfImprove", TierSelfImprove, 100},
		{"TierExploratory", TierExploratory, 50},
		{"Unknown tier defaults to 50", Tier(99), 50},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := TierBaseScore(tt.tier)
			if got != tt.expected {
				t.Errorf("TierBaseScore(%d) = %f, want %f", tt.tier, got, tt.expected)
			}
		})
	}
}

func TestSourceConstantsDistinct(t *testing.T) {
	t.Parallel()

	sources := []Source{
		SourceUser, SourceBacklog, SourceDiscovery, SourceGrant,
		SourceResearch, SourceRetry, SourceStale, SourceOSS, SourceMarket,
	}

	seen := make(map[Source]bool, len(sources))
	for _, s := range sources {
		if seen[s] {
			t.Errorf("duplicate Source constant: %q", s)
		}
		seen[s] = true
	}
}

func TestStatusConstantsDistinct(t *testing.T) {
	t.Parallel()

	statuses := []string{
		StatusPending, StatusQueued, StatusRunning, StatusCompleted,
		StatusFailed, StatusBlocked, StatusStale, StatusCancelled,
	}

	seen := make(map[string]bool, len(statuses))
	for _, s := range statuses {
		if seen[s] {
			t.Errorf("duplicate Status constant: %q", s)
		}
		seen[s] = true
	}
}
