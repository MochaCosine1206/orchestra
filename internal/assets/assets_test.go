package assets

import (
	"sort"
	"strings"
	"testing"
)

func TestAgentDefLoads(t *testing.T) {
	roles := []string{"architect", "implementer", "reviewer", "scout", "researcher", "editor"}
	for _, role := range roles {
		t.Run(role, func(t *testing.T) {
			data, err := AgentDef(role)
			if err != nil {
				t.Fatalf("AgentDef(%q): %v", role, err)
			}
			if len(data) == 0 {
				t.Errorf("AgentDef(%q) returned empty data", role)
			}
			if !strings.Contains(string(data), "---") {
				t.Errorf("AgentDef(%q) missing YAML frontmatter", role)
			}
		})
	}
}

func TestAgentDefMissing(t *testing.T) {
	_, err := AgentDef("nonexistent")
	if err == nil {
		t.Error("expected error for nonexistent agent def")
	}
}

func TestProfileLoads(t *testing.T) {
	roles := []string{"architect", "implementer", "reviewer", "scout", "researcher", "editor"}
	for _, role := range roles {
		t.Run(role, func(t *testing.T) {
			data, err := Profile(role)
			if err != nil {
				t.Fatalf("Profile(%q): %v", role, err)
			}
			if len(data) == 0 {
				t.Errorf("Profile(%q) returned empty data", role)
			}
			if !strings.Contains(string(data), "permissions") {
				t.Errorf("Profile(%q) missing permissions key", role)
			}
		})
	}
}

func TestProfileMissing(t *testing.T) {
	_, err := Profile("nonexistent")
	if err == nil {
		t.Error("expected error for nonexistent profile")
	}
}

func TestTemplateLoads(t *testing.T) {
	templates := []string{"task-spec.md", "orchestra-command.md"}
	for _, name := range templates {
		t.Run(name, func(t *testing.T) {
			data, err := Template(name)
			if err != nil {
				t.Fatalf("Template(%q): %v", name, err)
			}
			if len(data) == 0 {
				t.Errorf("Template(%q) returned empty data", name)
			}
		})
	}
}

func TestTemplateMissing(t *testing.T) {
	_, err := Template("nonexistent.md")
	if err == nil {
		t.Error("expected error for nonexistent template")
	}
}

func TestAllAgentRoles(t *testing.T) {
	roles := AllAgentRoles()
	if len(roles) != 11 {
		t.Fatalf("expected 11 roles, got %d: %v", len(roles), roles)
	}

	sort.Strings(roles)
	expected := []string{"architect", "deep-researcher", "editor", "functional-tester", "illustrator", "implementer", "pi-implementer", "researcher", "reviewer", "scout", "visual-reviewer"}
	for i, role := range roles {
		if role != expected[i] {
			t.Errorf("role[%d] = %q, want %q", i, role, expected[i])
		}
	}
}

func TestOrchestraCommandTemplateUsesGoBinary(t *testing.T) {
	data, err := Template("orchestra-command.md")
	if err != nil {
		t.Fatalf("loading orchestra command template: %v", err)
	}
	content := string(data)
	if strings.Contains(content, "scripts/orchestra.sh") {
		t.Error("orchestra command template still references bash scripts")
	}
	if !strings.Contains(content, "orchestra") {
		t.Error("orchestra command template doesn't reference orchestra binary")
	}
}
