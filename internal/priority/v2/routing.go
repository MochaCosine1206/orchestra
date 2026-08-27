package priority

import (
	"strings"
)

// RouteWorkItem determines execution routing for a scored work item.
// Step 1: resolve target repo from SourceRepo or repo hint.
// Step 2: determine IsNewProject based on source type.
// Step 3: assign AgentRoles based on tier and project type.
func RouteWorkItem(item *WorkItem, registeredRepos []string) {
	// Step 1: Determine target repo
	if item.Source == SourceUser && item.TargetRepo == "" && item.SourceRepo != "" {
		// User items use SourceRepo as a hint (e.g., "orchestra"), not a full path.
		// Match against registered repos to resolve to a full path.
		for _, repo := range registeredRepos {
			if strings.Contains(strings.ToLower(repo), strings.ToLower(item.SourceRepo)) {
				item.TargetRepo = repo
				break
			}
		}
	}
	if item.TargetRepo == "" && item.SourceRepo != "" {
		item.TargetRepo = item.SourceRepo
	}

	// Step 2: New project vs improvement
	if item.Source == SourceBacklog || item.Source == SourceRetry || item.Source == SourceOSS {
		item.IsNewProject = false
	}
	if item.Source == SourceDiscovery && item.TargetRepo == "" {
		item.IsNewProject = true
	}

	// Step 3: Classify license/ethics
	ClassifyWorkItem(item)

	// Step 4: Agent roles
	if item.AgentRoles == nil {
		switch {
		case item.Tier == TierGoalResearch || item.Tier == TierExploratory || item.Tier == TierSelfImprove:
			item.AgentRoles = []string{"researcher"}
		case item.Tier == TierValidation:
			item.AgentRoles = []string{"implementer", "reviewer"}
		case item.Tier == TierTechDebt:
			item.AgentRoles = []string{"implementer"}
		case item.IsNewProject:
			item.AgentRoles = []string{"architect", "implementer", "reviewer"}
		default:
			item.AgentRoles = []string{"implementer", "reviewer"}
		}
	}
}
