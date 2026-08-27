package priority

import (
	"testing"
)

func TestRouteWorkItem_AgentRoles(t *testing.T) {
	tests := []struct {
		name          string
		item          WorkItem
		expectedRoles []string
	}{
		{
			name:          "TierGoalResearch gets researcher",
			item:          WorkItem{Tier: TierGoalResearch},
			expectedRoles: []string{"researcher"},
		},
		{
			name:          "TierExploratory gets researcher",
			item:          WorkItem{Tier: TierExploratory},
			expectedRoles: []string{"researcher"},
		},
		{
			name:          "TierSelfImprove gets researcher",
			item:          WorkItem{Tier: TierSelfImprove},
			expectedRoles: []string{"researcher"},
		},
		{
			name:          "TierValidation gets implementer+reviewer",
			item:          WorkItem{Tier: TierValidation},
			expectedRoles: []string{"implementer", "reviewer"},
		},
		{
			name:          "TierTechDebt gets implementer",
			item:          WorkItem{Tier: TierTechDebt},
			expectedRoles: []string{"implementer"},
		},
		{
			name:          "new project gets architect+implementer+reviewer",
			item:          WorkItem{Tier: TierGoalImpl, IsNewProject: true},
			expectedRoles: []string{"architect", "implementer", "reviewer"},
		},
		{
			name:          "default gets implementer+reviewer",
			item:          WorkItem{Tier: TierGoalImpl},
			expectedRoles: []string{"implementer", "reviewer"},
		},
		{
			name:          "TierUser default gets implementer+reviewer",
			item:          WorkItem{Tier: TierUser},
			expectedRoles: []string{"implementer", "reviewer"},
		},
		{
			name:          "existing roles are preserved",
			item:          WorkItem{Tier: TierGoalResearch, AgentRoles: []string{"scout"}},
			expectedRoles: []string{"scout"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			RouteWorkItem(&tt.item, nil)
			if len(tt.item.AgentRoles) != len(tt.expectedRoles) {
				t.Fatalf("expected roles %v, got %v", tt.expectedRoles, tt.item.AgentRoles)
			}
			for i, role := range tt.expectedRoles {
				if tt.item.AgentRoles[i] != role {
					t.Errorf("role[%d]: expected %q, got %q", i, role, tt.item.AgentRoles[i])
				}
			}
		})
	}
}

func TestRouteWorkItem_IsNewProject(t *testing.T) {
	tests := []struct {
		name     string
		item     WorkItem
		expected bool
	}{
		{
			name:     "backlog source is not new project",
			item:     WorkItem{Source: SourceBacklog, IsNewProject: true},
			expected: false,
		},
		{
			name:     "retry source is not new project",
			item:     WorkItem{Source: SourceRetry, IsNewProject: true},
			expected: false,
		},
		{
			name:     "oss source is not new project",
			item:     WorkItem{Source: SourceOSS, IsNewProject: true},
			expected: false,
		},
		{
			name:     "discovery with no target repo is new project",
			item:     WorkItem{Source: SourceDiscovery},
			expected: true,
		},
		{
			name:     "discovery with target repo is not new project",
			item:     WorkItem{Source: SourceDiscovery, TargetRepo: "/repos/existing"},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			RouteWorkItem(&tt.item, nil)
			if tt.item.IsNewProject != tt.expected {
				t.Errorf("expected IsNewProject=%v, got %v", tt.expected, tt.item.IsNewProject)
			}
		})
	}
}

func TestRouteWorkItem_TargetRepo(t *testing.T) {
	t.Run("copies SourceRepo to TargetRepo", func(t *testing.T) {
		item := WorkItem{SourceRepo: "/repos/myrepo"}
		RouteWorkItem(&item, nil)
		if item.TargetRepo != "/repos/myrepo" {
			t.Errorf("expected TargetRepo=/repos/myrepo, got %q", item.TargetRepo)
		}
	})

	t.Run("preserves existing TargetRepo", func(t *testing.T) {
		item := WorkItem{SourceRepo: "/repos/source", TargetRepo: "/repos/target"}
		RouteWorkItem(&item, nil)
		if item.TargetRepo != "/repos/target" {
			t.Errorf("expected TargetRepo=/repos/target, got %q", item.TargetRepo)
		}
	})

	t.Run("user source matches against registered repos", func(t *testing.T) {
		item := WorkItem{Source: SourceUser, SourceRepo: "orchestra"}
		repos := []string{"/home/user/symphonies/Claude-Orchestra", "/home/user/symphonies/hitl"}
		RouteWorkItem(&item, repos)
		if item.TargetRepo != "/home/user/symphonies/Claude-Orchestra" {
			t.Errorf("expected match to Claude-Orchestra, got %q", item.TargetRepo)
		}
	})
}
