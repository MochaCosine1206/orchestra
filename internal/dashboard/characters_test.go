package dashboard

import (
	"strings"
	"testing"
)

func TestAvatarForAgent(t *testing.T) {
	// State overrides take precedence
	got := AvatarForAgent("implementer", "working")
	if got != "hammer.webp" {
		t.Errorf("AvatarForAgent(implementer, working) = %q, want %q", got, "hammer.webp")
	}

	// Role fallback when no state match
	got = AvatarForAgent("architect", "idle")
	if got != "triangular-ruler.webp" {
		t.Errorf("AvatarForAgent(architect, idle) = %q, want %q", got, "triangular-ruler.webp")
	}

	// Default fallback
	got = AvatarForAgent("unknown", "unknown-state")
	if got != "robot-face.webp" {
		t.Errorf("AvatarForAgent(unknown, unknown) = %q, want %q", got, "robot-face.webp")
	}
}

func TestAvatarForAgentAllStates(t *testing.T) {
	for state, expected := range StateAvatar {
		t.Run(state, func(t *testing.T) {
			got := AvatarForAgent("implementer", state)
			if got != expected {
				t.Errorf("AvatarForAgent(implementer, %q) = %q, want %q", state, got, expected)
			}
		})
	}
}

func TestAvatarForAgentAllRoles(t *testing.T) {
	for role, expected := range RoleAvatar {
		t.Run(role, func(t *testing.T) {
			// Use a status that has no state avatar match
			got := AvatarForAgent(role, "idle")
			if got != expected {
				t.Errorf("AvatarForAgent(%q, idle) = %q, want %q", role, got, expected)
			}
		})
	}
}

func TestAvatarSrc(t *testing.T) {
	got := AvatarSrc("implementer", "working")
	if !strings.HasPrefix(got, "/static/agents/") {
		t.Errorf("AvatarSrc should start with /static/agents/, got %q", got)
	}
	if !strings.HasSuffix(got, ".webp") {
		t.Errorf("AvatarSrc should end with .webp, got %q", got)
	}
}

func TestStatusRingCSS(t *testing.T) {
	// Known statuses should return specific styles
	for status := range statusRingStyles {
		t.Run(status, func(t *testing.T) {
			css := StatusRingCSS(status)
			if !strings.Contains(css, "border-color:") {
				t.Errorf("StatusRingCSS(%q) missing border-color: %s", status, css)
			}
			if !strings.Contains(css, "box-shadow:") {
				t.Errorf("StatusRingCSS(%q) missing box-shadow: %s", status, css)
			}
		})
	}

	// Unknown status should fall back to idle
	css := StatusRingCSS("unknown")
	idleCSS := StatusRingCSS("idle")
	if css != idleCSS {
		t.Errorf("StatusRingCSS(unknown) = %q, want idle fallback %q", css, idleCSS)
	}
}

func TestRoleAvatarCompleteness(t *testing.T) {
	expectedRoles := []string{"architect", "implementer", "reviewer", "scout", "researcher", "editor", "conductor"}
	for _, role := range expectedRoles {
		if _, ok := RoleAvatar[role]; !ok {
			t.Errorf("RoleAvatar missing entry for %q", role)
		}
	}
}

func TestStateAvatarCompleteness(t *testing.T) {
	expectedStates := []string{"working", "reading", "writing", "testing", "thinking", "waiting", "success", "failed", "dead", "merging", "reviewing"}
	for _, state := range expectedStates {
		if _, ok := StateAvatar[state]; !ok {
			t.Errorf("StateAvatar missing entry for %q", state)
		}
	}
}
