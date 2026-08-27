package agent

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestIsValidRole(t *testing.T) {
	valid := []string{"architect", "implementer", "reviewer", "scout", "researcher", "editor", "illustrator", "visual-reviewer", "functional-tester"}
	for _, r := range valid {
		if !IsValidRole(r) {
			t.Errorf("expected %q to be valid", r)
		}
	}

	invalid := []string{"", "admin", "worker", "ARCHITECT"}
	for _, r := range invalid {
		if IsValidRole(r) {
			t.Errorf("expected %q to be invalid", r)
		}
	}
}

func TestAllRoles(t *testing.T) {
	roles := AllRoles()
	if len(roles) != 11 {
		t.Errorf("expected 11 roles, got %d", len(roles))
	}
}

func TestDefaultTimeout(t *testing.T) {
	tests := []struct {
		role    Role
		expect  time.Duration
	}{
		{RoleScout, 30 * time.Minute},
		{RoleReviewer, 40 * time.Minute},
		{RoleImplementer, 60 * time.Minute},
		{RoleArchitect, 90 * time.Minute},
		{RoleResearcher, 120 * time.Minute},
		{RoleVisualReviewer, 45 * time.Minute},
		{RoleFunctionalTester, 45 * time.Minute},
		{Role("unknown"), 60 * time.Minute},
	}
	for _, tt := range tests {
		t.Run(string(tt.role), func(t *testing.T) {
			got := DefaultTimeout(tt.role)
			if got != tt.expect {
				t.Errorf("DefaultTimeout(%q) = %v, want %v", tt.role, got, tt.expect)
			}
		})
	}
}

func TestDefaultModelAllOpus(t *testing.T) {
	for _, r := range AllRoles() {
		model := DefaultModel(r, StrategyAllOpus)
		if model != "claude-opus-4-6[1m]" {
			t.Errorf("DefaultModel(%q, all-opus) = %q, want opus", r, model)
		}
	}
}

func TestDefaultModelPerRole(t *testing.T) {
	tests := []struct {
		role   Role
		expect string
	}{
		{RoleScout, "claude-haiku-4-5-20251001"},
		{RoleReviewer, "claude-sonnet-4-5-20250929"},
		{RoleImplementer, "claude-sonnet-4-5-20250929"},
		{RoleArchitect, "claude-opus-4-6[1m]"},
		{RoleResearcher, "claude-opus-4-6[1m]"},
	}
	for _, tt := range tests {
		t.Run(string(tt.role), func(t *testing.T) {
			got := DefaultModel(tt.role, StrategyPerRole)
			if got != tt.expect {
				t.Errorf("DefaultModel(%q, per-role) = %q, want %q", tt.role, got, tt.expect)
			}
		})
	}
}

func TestNextFallbackModel(t *testing.T) {
	tests := []struct {
		current string
		expect  string
	}{
		{"claude-opus-4-6[1m]", "claude-sonnet-4-5-20250929"},
		{"claude-sonnet-4-5-20250929", "claude-haiku-4-5-20251001"},
		{"claude-haiku-4-5-20251001", ""},
		{"unknown-model", ""},
	}
	for _, tt := range tests {
		t.Run(tt.current, func(t *testing.T) {
			got := NextFallbackModel(tt.current)
			if got != tt.expect {
				t.Errorf("NextFallbackModel(%q) = %q, want %q", tt.current, got, tt.expect)
			}
		})
	}
}

func TestParseStrategy(t *testing.T) {
	tests := []struct {
		input  string
		expect ModelStrategy
	}{
		{"all-opus", StrategyAllOpus},
		{"per-role", StrategyPerRole},
		{"all-sonnet", StrategyAllSonnet},
		{"ALL-OPUS", StrategyAllOpus},
		{"Per-Role", StrategyPerRole},
		{"unknown", StrategyAllOpus},
		{"", StrategyAllOpus},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := ParseStrategy(tt.input)
			if got != tt.expect {
				t.Errorf("ParseStrategy(%q) = %q, want %q", tt.input, got, tt.expect)
			}
		})
	}
}

func TestNormalizeModelName(t *testing.T) {
	tests := []struct {
		input  string
		expect string
	}{
		{"opus", "claude-opus-4-6[1m]"},
		{"sonnet", "claude-sonnet-4-5-20250929"},
		{"haiku", "claude-haiku-4-5-20251001"},
		{"Opus", "claude-opus-4-6[1m]"},
		{"claude-opus-4-6[1m]", "claude-opus-4-6[1m]"},
		{"claude-sonnet-4-5-20250929", "claude-sonnet-4-5-20250929"},
		{"custom-model", "custom-model"},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := NormalizeModelName(tt.input)
			if got != tt.expect {
				t.Errorf("NormalizeModelName(%q) = %q, want %q", tt.input, got, tt.expect)
			}
		})
	}
}

func TestResolveModelAgentDefOverride(t *testing.T) {
	// Agent def model should take priority over strategy
	got := ResolveModel(RoleScout, StrategyAllOpus, "sonnet")
	expect := "claude-sonnet-4-5-20250929"
	if got != expect {
		t.Errorf("ResolveModel with agent def override = %q, want %q", got, expect)
	}
}

func TestResolveModelFallbackToStrategy(t *testing.T) {
	// No agent def model: fall back to strategy
	got := ResolveModel(RoleScout, StrategyPerRole, "")
	expect := "claude-haiku-4-5-20251001"
	if got != expect {
		t.Errorf("ResolveModel with per-role fallback = %q, want %q", got, expect)
	}
}

func TestDefaultModelAllSonnet(t *testing.T) {
	for _, r := range AllRoles() {
		model := DefaultModel(r, StrategyAllSonnet)
		if model != "claude-sonnet-4-5-20250929" {
			t.Errorf("DefaultModel(%q, all-sonnet) = %q, want sonnet", r, model)
		}
	}
}

func TestLoadProfile(t *testing.T) {
	dir := t.TempDir()
	profileDir := filepath.Join(dir, ".orchestra", "profiles")
	os.MkdirAll(profileDir, 0o755)

	profileJSON := `{
  "permissions": {
    "allow": ["Read", "Glob", "Grep"],
    "deny": ["Edit", "Write"]
  }
}`
	os.WriteFile(filepath.Join(profileDir, "scout.json"), []byte(profileJSON), 0o644)

	p, err := LoadProfile(dir, RoleScout)
	if err != nil {
		t.Fatalf("LoadProfile: %v", err)
	}
	if len(p.Permissions.Allow) != 3 {
		t.Errorf("expected 3 allow rules, got %d", len(p.Permissions.Allow))
	}
	if len(p.Permissions.Deny) != 2 {
		t.Errorf("expected 2 deny rules, got %d", len(p.Permissions.Deny))
	}
}

func TestLoadProfileEmbeddedFallback(t *testing.T) {
	// No filesystem profile exists, should fall back to embedded
	dir := t.TempDir()
	p, err := LoadProfile(dir, RoleScout)
	if err != nil {
		t.Fatalf("expected embedded fallback to work: %v", err)
	}
	if len(p.Permissions.Allow) == 0 {
		t.Error("expected non-empty allow rules from embedded profile")
	}
}

func TestLoadProfileFilesystemOverridesEmbedded(t *testing.T) {
	dir := t.TempDir()
	profileDir := filepath.Join(dir, ".orchestra", "profiles")
	os.MkdirAll(profileDir, 0o755)

	// Write a custom profile with different allow rules
	customJSON := `{
  "permissions": {
    "allow": ["CustomTool"],
    "deny": []
  }
}`
	os.WriteFile(filepath.Join(profileDir, "scout.json"), []byte(customJSON), 0o644)

	p, err := LoadProfile(dir, RoleScout)
	if err != nil {
		t.Fatalf("LoadProfile: %v", err)
	}
	if len(p.Permissions.Allow) != 1 || p.Permissions.Allow[0] != "CustomTool" {
		t.Errorf("expected filesystem override to take precedence, got allow=%v", p.Permissions.Allow)
	}
}

func TestLoadProfileBothMissing(t *testing.T) {
	dir := t.TempDir()
	_, err := LoadProfile(dir, Role("nonexistent_role"))
	if err == nil {
		t.Error("expected error when both filesystem and embedded are missing")
	}
}

func TestLoadProfileInvalid(t *testing.T) {
	dir := t.TempDir()
	profileDir := filepath.Join(dir, ".orchestra", "profiles")
	os.MkdirAll(profileDir, 0o755)
	os.WriteFile(filepath.Join(profileDir, "scout.json"), []byte("not json"), 0o644)

	_, err := LoadProfile(dir, RoleScout)
	if err == nil {
		t.Error("expected error for invalid JSON")
	}
}

func TestParseAgentDef(t *testing.T) {
	dir := t.TempDir()
	agentDir := filepath.Join(dir, ".orchestra", "agents")
	os.MkdirAll(agentDir, 0o755)

	defContent := `---
name: Implementer
model: opus
description: Focused code implementation agent
allowedTools:
  - Read
  - Edit
  - Write
---

# Implementer Agent

You are a code implementation specialist.`

	os.WriteFile(filepath.Join(agentDir, "implementer.md"), []byte(defContent), 0o644)

	def, err := ParseAgentDef(dir, RoleImplementer)
	if err != nil {
		t.Fatalf("ParseAgentDef: %v", err)
	}
	if def.Name != "Implementer" {
		t.Errorf("expected name 'Implementer', got %q", def.Name)
	}
	if def.Model != "opus" {
		t.Errorf("expected model 'opus', got %q", def.Model)
	}
	if def.Description != "Focused code implementation agent" {
		t.Errorf("expected description, got %q", def.Description)
	}
	if len(def.AllowedTools) != 3 {
		t.Errorf("expected 3 allowed tools, got %d: %v", len(def.AllowedTools), def.AllowedTools)
	}
	if def.SystemPrompt == "" {
		t.Error("expected non-empty system prompt")
	}
}

func TestParseAgentDefEmbeddedFallback(t *testing.T) {
	// No filesystem agent def exists, should fall back to embedded
	dir := t.TempDir()
	def, err := ParseAgentDef(dir, RoleScout)
	if err != nil {
		t.Fatalf("expected embedded fallback to work: %v", err)
	}
	if def.Name != "Scout" {
		t.Errorf("expected name 'Scout' from embedded, got %q", def.Name)
	}
	if def.SystemPrompt == "" {
		t.Error("expected non-empty system prompt from embedded")
	}
}

func TestParseAgentDefFilesystemOverridesEmbedded(t *testing.T) {
	dir := t.TempDir()
	agentDir := filepath.Join(dir, ".orchestra", "agents")
	os.MkdirAll(agentDir, 0o755)

	customDef := `---
name: CustomScout
model: haiku
description: My custom scout
allowedTools:
  - Read
---

# Custom Scout

Custom system prompt.`
	os.WriteFile(filepath.Join(agentDir, "scout.md"), []byte(customDef), 0o644)

	def, err := ParseAgentDef(dir, RoleScout)
	if err != nil {
		t.Fatalf("ParseAgentDef: %v", err)
	}
	if def.Name != "CustomScout" {
		t.Errorf("expected filesystem override name 'CustomScout', got %q", def.Name)
	}
}

func TestParseAgentDefBothMissing(t *testing.T) {
	dir := t.TempDir()
	_, err := ParseAgentDef(dir, Role("nonexistent_role"))
	if err == nil {
		t.Error("expected error when both filesystem and embedded are missing")
	}
}

func TestParseAgentDefNoFrontmatter(t *testing.T) {
	dir := t.TempDir()
	agentDir := filepath.Join(dir, ".orchestra", "agents")
	os.MkdirAll(agentDir, 0o755)
	os.WriteFile(filepath.Join(agentDir, "scout.md"), []byte("# Scout\nJust a prompt"), 0o644)

	def, err := ParseAgentDef(dir, RoleScout)
	if err != nil {
		t.Fatalf("ParseAgentDef: %v", err)
	}
	if def.SystemPrompt == "" {
		t.Error("expected system prompt to be the entire content")
	}
}
