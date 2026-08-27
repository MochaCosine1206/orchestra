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

> Build the best AI education platform this year
> Focus on grant-funded work first

## Active Priorities

1. Finish HITL v11 homework system — ai-agent-hitl-education-v11
2. Grant application for UNESCO education AI — Claude-Orchestra
3. Fix Telegram routing for multi-project — Claude-Orchestra
4. Research LoRa + local LLM hub opportunities
5. Enterprise onboarding knowledge management tool -- enterprise-kb
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

	if goal != "Build the best AI education platform this year Focus on grant-funded work first" {
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
		{0, "Finish HITL v11 homework system", "ai-agent-hitl-education-v11"},
		{1, "Grant application for UNESCO education AI", "Claude-Orchestra"},
		{2, "Fix Telegram routing for multi-project", "Claude-Orchestra"},
		{3, "Research LoRa + local LLM hub opportunities", ""},
		{4, "Enterprise onboarding knowledge management tool", "enterprise-kb"},
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

1. Build dark factory daemon — Claude-Orchestra
2. Write grant proposal — Claude-Orchestra
3. Ship HITL homework module — ai-agent-hitl-education-v11
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
	if items[0].SourceID != "user-build-dark-factory-daemon" {
		t.Errorf("items[0].SourceID = %q, want %q", items[0].SourceID, "user-build-dark-factory-daemon")
	}
}

func TestSlugify(t *testing.T) {
	t.Parallel()

	tests := []struct {
		input string
		want  string
	}{
		{"Build dark factory daemon", "build-dark-factory-daemon"},
		{"Fix Telegram routing for multi-project", "fix-telegram-routing-for-multi-project"},
		{"LoRa + local LLM hub", "lora-local-llm-hub"},
		{"", ""},
	}

	for _, tt := range tests {
		got := slugify(tt.input)
		if got != tt.want {
			t.Errorf("slugify(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}
