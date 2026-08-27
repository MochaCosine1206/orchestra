package priority

import "testing"

func TestClassifyWorkItem_Grant(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name        string
		title       string
		desc        string
		wantType    string
		wantContain string
	}{
		{"NSF POSE", "Apply for NSF POSE grant", "Pathways to Open-Source Ecosystems", "open-source", "NSF POSE"},
		{"SBIR", "SBIR Phase I application", "Small Business Innovation Research", "proprietary-ok", "SBIR"},
		{"NIH", "NIH R01 data sharing", "NIH funded research tool", "open-source", "NIH"},
		{"default grant", "Generic foundation grant", "private foundation funding", "open-source", "default open"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			item := &WorkItem{Source: SourceGrant, Title: tt.title, Description: tt.desc}
			ClassifyWorkItem(item)
			if item.LicenseType != tt.wantType {
				t.Errorf("got LicenseType=%q, want %q", item.LicenseType, tt.wantType)
			}
			if tt.wantContain != "" && !contains(item.LicenseReason, tt.wantContain) {
				t.Errorf("LicenseReason %q should contain %q", item.LicenseReason, tt.wantContain)
			}
		})
	}
}

func TestClassifyWorkItem_EthicalImperative(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		title string
	}{
		{"healthcare", "Local-first health record aggregator for patients"},
		{"education", "Free teacher productivity tool replacing $9.99/mo SaaS"},
		{"mental health", "Open-source CBT thought journal"},
		{"financial", "Tax scenario planner for low-income families"},
		{"accessibility", "ADHD task prioritizer toolkit"},
		{"price signal", "Replace $500/yr enterprise tool for individuals"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			item := &WorkItem{Source: SourceDiscovery, Title: tt.title}
			ClassifyWorkItem(item)
			if item.LicenseType != "open-source-free" {
				t.Errorf("got LicenseType=%q, want open-source-free for %q", item.LicenseType, tt.title)
			}
		})
	}
}

func TestClassifyWorkItem_Enterprise(t *testing.T) {
	t.Parallel()
	item := &WorkItem{Source: SourceDiscovery, Title: "Enterprise SSO integration for team management"}
	ClassifyWorkItem(item)
	if item.LicenseType != "proprietary" {
		t.Errorf("got LicenseType=%q, want proprietary", item.LicenseType)
	}
}

func TestClassifyWorkItem_Individual(t *testing.T) {
	t.Parallel()
	item := &WorkItem{Source: SourceDiscovery, Title: "Open-source podcast production tool"}
	ClassifyWorkItem(item)
	if item.LicenseType != "open-source" {
		t.Errorf("got LicenseType=%q, want open-source", item.LicenseType)
	}
}

func TestClassifyWorkItem_AlreadyClassified(t *testing.T) {
	t.Parallel()
	item := &WorkItem{LicenseType: "proprietary", LicenseReason: "manual"}
	ClassifyWorkItem(item)
	if item.LicenseType != "proprietary" {
		t.Errorf("should not override existing classification")
	}
}

func TestDetermineExecutionMode(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		item WorkItem
		want string
	}{
		{"researcher role", WorkItem{AgentRoles: []string{"researcher"}}, "research"},
		{"grant source", WorkItem{Source: SourceGrant}, "grant"},
		{"new project", WorkItem{IsNewProject: true}, "research"},
		{"implementer", WorkItem{AgentRoles: []string{"implementer", "reviewer"}}, "conduct"},
		{"default", WorkItem{}, "conduct"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := DetermineExecutionMode(&tt.item)
			if got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && containsStr(s, substr))
}

func containsStr(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
