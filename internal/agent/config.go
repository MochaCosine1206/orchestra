package agent

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/MochaCosine1206/orchestra/internal/assets"
)

// Role represents a valid agent role.
type Role string

const (
	RoleArchitect        Role = "architect"
	RoleImplementer      Role = "implementer"
	RoleReviewer         Role = "reviewer"
	RoleScout            Role = "scout"
	RoleResearcher       Role = "researcher"
	RoleEditor           Role = "editor"
	RoleIllustrator      Role = "illustrator"
	RoleVisualReviewer   Role = "visual-reviewer"
	RoleFunctionalTester Role = "functional-tester"
	RoleOrchestrator     Role = "orchestrator"    // B-029: spawns orchestra go instead of claude -p
	RoleDeepResearcher   Role = "deep-researcher" // recursive research with saturation-based stopping
	RolePiImplementer    Role = "pi-implementer"  // local LLM (pi/Ollama) for bounded implementation tasks
)

const (
	ModelOpus   = "claude-opus-4-6[1m]"
	ModelSonnet = "claude-sonnet-4-5-20250929"
	ModelHaiku  = "claude-haiku-4-5-20251001"
)

var validRoles = map[Role]bool{
	RoleArchitect:        true,
	RoleImplementer:      true,
	RoleReviewer:         true,
	RoleScout:            true,
	RoleResearcher:       true,
	RoleEditor:           true,
	RoleIllustrator:      true,
	RoleVisualReviewer:   true,
	RoleFunctionalTester: true,
	RoleOrchestrator:     true,
	RoleDeepResearcher:   true,
	RolePiImplementer:    true,
}

// IsValidRole returns true if the string is a recognized agent role.
func IsValidRole(s string) bool {
	return validRoles[Role(s)]
}

// AllRoles returns all valid roles.
func AllRoles() []Role {
	return []Role{RoleArchitect, RoleImplementer, RoleReviewer, RoleScout, RoleResearcher, RoleDeepResearcher, RoleEditor, RoleIllustrator, RoleVisualReviewer, RoleFunctionalTester, RolePiImplementer}
}

// ModelStrategy selects how models are assigned to roles.
type ModelStrategy string

const (
	StrategyAllOpus   ModelStrategy = "all-opus"
	StrategyPerRole   ModelStrategy = "per-role"
	StrategyAllSonnet ModelStrategy = "all-sonnet"
)

// ParseStrategy converts a string to a ModelStrategy.
// Unrecognized strings default to StrategyAllOpus.
func ParseStrategy(s string) ModelStrategy {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "per-role":
		return StrategyPerRole
	case "all-sonnet":
		return StrategyAllSonnet
	default:
		return StrategyAllOpus
	}
}

// NormalizeModelName maps short names ("opus", "sonnet", "haiku") to full model IDs.
// Full model IDs are passed through unchanged.
func NormalizeModelName(name string) string {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "opus":
		return ModelOpus
	case "sonnet":
		return ModelSonnet
	case "haiku":
		return ModelHaiku
	default:
		return name
	}
}

// ResolveModel determines the model for a spawn. Priority:
// 1. agentDefModel (from .orchestra/agents/{role}.md) if non-empty
// 2. DefaultModel(role, strategy)
func ResolveModel(role Role, strategy ModelStrategy, agentDefModel string) string {
	if agentDefModel != "" {
		return NormalizeModelName(agentDefModel)
	}
	return DefaultModel(role, strategy)
}

// DefaultTimeout returns the role-specific timeout duration.
func DefaultTimeout(role Role) time.Duration {
	switch role {
	case RoleScout:
		return 30 * time.Minute
	case RoleReviewer:
		return 40 * time.Minute
	case RoleVisualReviewer, RoleFunctionalTester:
		return 45 * time.Minute
	case RoleImplementer:
		return 60 * time.Minute
	case RoleArchitect:
		return 90 * time.Minute
	case RoleResearcher:
		return 120 * time.Minute
	case RoleDeepResearcher:
		return 180 * time.Minute // recursive multi-round research
	default:
		return 60 * time.Minute
	}
}

// DefaultModel returns the model for a given role and strategy.
func DefaultModel(role Role, strategy ModelStrategy) string {
	switch strategy {
	case StrategyPerRole:
		switch role {
		case RoleScout:
			return ModelHaiku
		case RoleReviewer, RoleImplementer:
			return ModelSonnet
		case RoleArchitect, RoleResearcher, RoleVisualReviewer, RoleFunctionalTester:
			return ModelOpus
		}
	case StrategyAllSonnet:
		return ModelSonnet
	}
	// all-opus (default)
	return ModelOpus
}

// NextFallbackModel returns the next model in the fallback chain: opus → sonnet → haiku.
// Returns empty string if no further fallback is available.
func NextFallbackModel(current string) string {
	switch {
	case strings.Contains(current, "opus"):
		return ModelSonnet
	case strings.Contains(current, "sonnet"):
		return ModelHaiku
	default:
		return ""
	}
}

// PermissionProfile represents the JSON structure of .orchestra/profiles/{role}.json.
type PermissionProfile struct {
	Permissions struct {
		Allow []string `json:"allow"`
		Deny  []string `json:"deny"`
	} `json:"permissions"`
}

// LoadProfile reads the permission profile for a given role.
// It checks the filesystem first (user overrides), then falls back to embedded defaults.
func LoadProfile(repoRoot string, role Role) (*PermissionProfile, error) {
	path := filepath.Join(repoRoot, ".orchestra", "profiles", string(role)+".json")
	data, err := os.ReadFile(path)
	if err != nil {
		// Fall back to embedded asset
		data, err = assets.Profile(string(role))
		if err != nil {
			return nil, fmt.Errorf("no profile for %s (checked %s and embedded): %w", role, path, err)
		}
	}
	var p PermissionProfile
	if err := json.Unmarshal(data, &p); err != nil {
		return nil, fmt.Errorf("parsing profile for %s: %w", role, err)
	}
	return &p, nil
}

// AgentDef represents a parsed .orchestra/agents/{role}.md file.
type AgentDef struct {
	Name         string
	Model        string
	Description  string
	AllowedTools []string
	SystemPrompt string // the body after frontmatter
}

// ParseAgentDef reads and parses an agent definition file.
// It checks the filesystem first (user overrides), then falls back to embedded defaults.
func ParseAgentDef(repoRoot string, role Role) (*AgentDef, error) {
	path := filepath.Join(repoRoot, ".orchestra", "agents", string(role)+".md")
	data, err := os.ReadFile(path)
	if err != nil {
		// Fall back to embedded asset
		data, err = assets.AgentDef(string(role))
		if err != nil {
			return nil, fmt.Errorf("no agent def for %s (checked %s and embedded): %w", role, path, err)
		}
	}

	content := string(data)
	def := &AgentDef{}

	// Parse YAML frontmatter between --- markers
	if !strings.HasPrefix(content, "---") {
		def.SystemPrompt = content
		return def, nil
	}

	parts := strings.SplitN(content[3:], "---", 2)
	if len(parts) < 2 {
		def.SystemPrompt = content
		return def, nil
	}

	frontmatter := parts[0]
	def.SystemPrompt = strings.TrimSpace(parts[1])

	// Simple YAML key: value parsing (no full YAML dependency needed)
	for _, line := range strings.Split(frontmatter, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		// Handle list items under allowedTools/tools
		if strings.HasPrefix(line, "- ") {
			tool := strings.TrimPrefix(line, "- ")
			tool = strings.TrimSpace(tool)
			def.AllowedTools = append(def.AllowedTools, tool)
			continue
		}

		kv := strings.SplitN(line, ":", 2)
		if len(kv) != 2 {
			continue
		}
		key := strings.TrimSpace(kv[0])
		val := strings.TrimSpace(kv[1])
		switch key {
		case "name":
			def.Name = val
		case "model":
			def.Model = val
		case "description":
			def.Description = val
		case "allowedTools", "tools":
			// Values follow as list items; reset to capture fresh
			def.AllowedTools = nil
		}
	}

	return def, nil
}
