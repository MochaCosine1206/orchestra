package priority

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestParsePrioritiesFile_WithItems(t *testing.T) {
	t.Parallel()

	content := `# My Priorities

> Ship the v2 platform this quarter
> Prioritise customer-facing work first

## Active Priorities

1. Finish the onboarding flow — web-app
2. Draft the integration proposal — orchestra
3. Fix notification routing for multi-project — orchestra
4. Research local inference options
5. Knowledge management tool -- internal-kb
`

	dir := t.TempDir()
	path := filepath.Join(dir, "priorities.md")
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	goal, priorities, err := ParsePrioritiesFile(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if goal != "Ship the v2 platform this quarter Prioritise customer-facing work first" {
		t.Errorf("goal = %q, want preamble text", goal)
	}

	if len(priorities) != 5 {
		t.Fatalf("got %d priorities, want 5", len(priorities))
	}

	// Verify ranks
	for i, p := range priorities {
		if p.Rank != i+1 {
			t.Errorf("priorities[%d].Rank = %d, want %d", i, p.Rank, i+1)
		}
		if !p.Active {
			t.Errorf("priorities[%d].Active = false, want true", i)
		}
	}

	// Verify repo hints
	tests := []struct {
		idx      int
		title    string
		repoHint string
	}{
		{0, "Finish the onboarding flow", "web-app"},
		{1, "Draft the integration proposal", "orchestra"},
		{2, "Fix notification routing for multi-project", "orchestra"},
		{3, "Research local inference options", ""},
		{4, "Knowledge management tool", "internal-kb"},
	}

	for _, tt := range tests {
		p := priorities[tt.idx]
		if p.Title != tt.title {
			t.Errorf("priorities[%d].Title = %q, want %q", tt.idx, p.Title, tt.title)
		}
		if p.RepoHint != tt.repoHint {
			t.Errorf("priorities[%d].RepoHint = %q, want %q", tt.idx, p.RepoHint, tt.repoHint)
		}
	}
}

func TestParsePrioritiesFile_MissingFile(t *testing.T) {
	t.Parallel()

	goal, priorities, err := ParsePrioritiesFile("/nonexistent/path/priorities.md")
	if err != nil {
		t.Fatalf("expected nil error for missing file, got: %v", err)
	}
	if goal != "" {
		t.Errorf("expected empty goal, got %q", goal)
	}
	if priorities != nil {
		t.Errorf("expected nil priorities, got %v", priorities)
	}
}

func TestUserCollector_Collect(t *testing.T) {
	t.Parallel()

	content := `# Priorities

## Active Priorities

1. Build the background daemon — orchestra
2. Write the design proposal — orchestra
3. Ship the reporting module — web-app
`

	dir := t.TempDir()
	path := filepath.Join(dir, "priorities.md")
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	collector := NewUserCollector(path)

	// Verify it satisfies the Collector interface
	var _ Collector = collector

	if collector.Name() != SourceUser {
		t.Errorf("Name() = %q, want %q", collector.Name(), SourceUser)
	}

	items, err := collector.Collect(context.Background())
	if err != nil {
		t.Fatalf("Collect error: %v", err)
	}

	if len(items) != 3 {
		t.Fatalf("got %d items, want 3", len(items))
	}

	for i, item := range items {
		if item.Tier != TierUser {
			t.Errorf("items[%d].Tier = %d, want TierUser (%d)", i, item.Tier, TierUser)
		}
		if item.Source != SourceUser {
			t.Errorf("items[%d].Source = %q, want %q", i, item.Source, SourceUser)
		}
		if item.UserPriorityRank == nil {
			t.Fatalf("items[%d].UserPriorityRank is nil", i)
		}
		if *item.UserPriorityRank != i+1 {
			t.Errorf("items[%d].UserPriorityRank = %d, want %d", i, *item.UserPriorityRank, i+1)
		}
	}

	// Verify source IDs are slugified
	if items[0].SourceID != "user-build-the-background-daemon" {
		t.Errorf("items[0].SourceID = %q, want %q", items[0].SourceID, "user-build-the-background-daemon")
	}
}

func TestSlugify(t *testing.T) {
	t.Parallel()

	tests := []struct {
		input string
		want  string
	}{
		{"Build the background daemon", "build-the-background-daemon"},
		{"Fix notification routing for multi-project", "fix-notification-routing-for-multi-project"},
		{"Local + remote LLM hub", "local-remote-llm-hub"},
		{"", ""},
	}

	for _, tt := range tests {
		got := slugify(tt.input)
		if got != tt.want {
			t.Errorf("slugify(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}
