package dashboard

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
)

// ConfigSetting represents a single configuration field.
type ConfigSetting struct {
	Key         string
	Label       string
	Value       string
	Type        string // "text", "number", "checkbox", "select", "range"
	Options     []string
	Tooltip     string
	Placeholder string
}

// ConfigSection groups settings under a named category.
type ConfigSection struct {
	ID       string
	Title    string
	Settings []ConfigSetting
}

// ConfigData holds all configuration data for the config template.
type ConfigData struct {
	Sections     []ConfigSection
	ThemeSwatches []ThemeSwatch
	SandboxEnv   string
	SandboxNote  string
}

// ThemeSwatch represents a clickable theme option.
type ThemeSwatch struct {
	Name  string
	Label string
	BG    string
	FG    string
}

// themeSwatches returns the 13 theme swatches matching style.css data-theme values.
func themeSwatches() []ThemeSwatch {
	return []ThemeSwatch{
		{Name: "clean", Label: "Clean", BG: "#131313", FG: "#e5e2e1"},
		{Name: "minimal", Label: "Minimal", BG: "#0a0a0a", FG: "#d4d0cf"},
		{Name: "terminal", Label: "Terminal", BG: "#0c0c0c", FG: "#33ff33"},
		{Name: "frosted", Label: "Frosted", BG: "#1a1a2e", FG: "#e0def4"},
		{Name: "pixel", Label: "Pixel", BG: "#2b2b2b", FG: "#f8f8f2"},
		{Name: "deep", Label: "Deep", BG: "#0d1117", FG: "#c9d1d9"},
		{Name: "electric", Label: "Electric", BG: "#0a0a1a", FG: "#e0e0ff"},
		{Name: "arctic", Label: "Arctic", BG: "#f0f4f8", FG: "#1a202c"},
		{Name: "warm", Label: "Warm", BG: "#1c1410", FG: "#e8ddd3"},
		{Name: "dusk", Label: "Dusk", BG: "#1a1625", FG: "#d4cfe0"},
		{Name: "pastel", Label: "Pastel", BG: "#faf5ff", FG: "#2d2040"},
		{Name: "neon-city", Label: "Neon City", BG: "#0a0a12", FG: "#e0e0f0"},
		{Name: "daylight", Label: "Daylight", BG: "#fafaf9", FG: "#1c1917"},
	}
}

// buildConfigData constructs the full configuration data with defaults from backlog decisions.
func buildConfigData() ConfigData {
	sections := []ConfigSection{
		{
			ID:    "auto-mode",
			Title: "Auto Mode",
			Settings: []ConfigSetting{
				{Key: "auto_enabled", Label: "Enable Auto Mode", Value: "true", Type: "checkbox", Tooltip: "Autonomous cycle execution"},
				{Key: "max_cycles", Label: "Max Cycles", Value: "10", Type: "number", Tooltip: "Maximum autonomous cycles before pause"},
				{Key: "max_parallel", Label: "Max Parallel Agents", Value: "4", Type: "number", Tooltip: "Concurrent agent limit"},
				{Key: "poll_interval", Label: "Poll Interval (s)", Value: "5", Type: "number", Tooltip: "Seconds between status checks"},
				{Key: "review_gates", Label: "Review Gates", Value: "true", Type: "checkbox", Tooltip: "Require review before merge (fail-open per B-210)"},
			},
		},
		{
			ID:    "orchestration-defaults",
			Title: "Orchestration Defaults",
			Settings: []ConfigSetting{
				{Key: "max_tasks", Label: "Max Tasks", Value: "8", Type: "number", Tooltip: "Default max tasks per decomposition (B-236)"},
				{Key: "max_files_per_task", Label: "Max Files/Task", Value: "25", Type: "number", Tooltip: "File cap per task with directory proximity split (B-236)"},
				{Key: "model_strategy", Label: "Model Strategy", Value: "all-opus", Type: "select", Options: []string{"all-opus", "tiered"}, Tooltip: "Claude Max: all-opus for quality"},
				{Key: "cascade_routing", Label: "Cascade Routing", Value: "true", Type: "checkbox", Tooltip: "Auto-escalate tier on failure (B-189)"},
				{Key: "file_enforcement", Label: "File Enforcement", Value: "defense", Type: "select", Options: []string{"none", "defense", "strict"}, Tooltip: "Auto-enabled at 3+ tasks"},
			},
		},
		{
			ID:    "merge-settings",
			Title: "Merge Settings",
			Settings: []ConfigSetting{
				{Key: "merge_mode", Label: "Merge Mode", Value: "local", Type: "select", Options: []string{"local", "PR"}, Tooltip: "Local merge or create GitHub PR"},
				{Key: "merge_strategy", Label: "Merge Strategy", Value: "auto", Type: "select", Options: []string{"auto", "batch", "fifo"}, Tooltip: "Auto: FIFO + hierarchical at 3+ tasks (B-282)"},
				{Key: "test_command", Label: "Test Command", Value: "go test ./...", Type: "text", Placeholder: "e.g. go test ./..."},
				{Key: "test_failure_mode", Label: "Test Failure Mode", Value: "block", Type: "select", Options: []string{"block", "warn", "ignore"}, Tooltip: "How to handle test failures during merge"},
				{Key: "auto_resolve", Label: "Auto-Resolve Conflicts", Value: "true", Type: "checkbox", Tooltip: "4-tier auto-resolution: import/non-overlapping/complex/LLM"},
			},
		},
		{
			ID:    "agent-roles",
			Title: "Agent Roles",
			Settings: []ConfigSetting{
				{Key: "timeout_architect", Label: "Architect Timeout (m)", Value: "30", Type: "number", Tooltip: "Minutes before timeout"},
				{Key: "timeout_implementer", Label: "Implementer Timeout (m)", Value: "45", Type: "number", Tooltip: "Minutes before timeout"},
				{Key: "timeout_reviewer", Label: "Reviewer Timeout (m)", Value: "20", Type: "number", Tooltip: "Minutes before timeout"},
				{Key: "timeout_scout", Label: "Scout Timeout (m)", Value: "10", Type: "number", Tooltip: "Minutes before timeout"},
				{Key: "timeout_researcher", Label: "Researcher Timeout (m)", Value: "30", Type: "number", Tooltip: "Minutes before timeout"},
			},
		},
		{
			ID:    "circuit-breakers",
			Title: "Circuit Breakers",
			Settings: []ConfigSetting{
				{Key: "empty_cycles", Label: "Empty Cycle Limit", Value: "2", Type: "number", Tooltip: "Pause after N empty cycles (B-210)"},
				{Key: "all_fail_limit", Label: "All-Fail Limit", Value: "3", Type: "number", Tooltip: "Stop after N all-fail rounds (B-210)"},
				{Key: "session_timeout", Label: "Session Timeout (h)", Value: "6", Type: "number", Tooltip: "Max session duration (B-210)"},
			},
		},
		{
			ID:    "appearance",
			Title: "Appearance",
		},
		{
			ID:    "notification-channel",
			Title: "Notification Channel",
			Settings: []ConfigSetting{
				{Key: "notification_channel", Label: "Channel", Value: "none", Type: "select", Options: []string{"none", "telegram", "claude"}, Tooltip: "Where to send status notifications"},
			},
		},
	}

	return ConfigData{
		Sections:      sections,
		ThemeSwatches: themeSwatches(),
		SandboxEnv:    "Execution environment: local process",
		SandboxNote:   "See B-202 for sandboxed execution environment roadmap",
	}
}

// handleConfig renders the full configuration page.
func (s *Server) handleConfigFull(w http.ResponseWriter, r *http.Request) {
	data := buildConfigData()
	s.renderTemplate(w, "config", data)
}

// handleConfigSection handles POST /api/config/{section} for updating settings.
func (s *Server) handleConfigSection(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Extract section from path: /api/config/{section}
	section := strings.TrimPrefix(r.URL.Path, "/api/config/")
	section = strings.TrimSuffix(section, "/")

	// theme is handled by the existing handler
	if section == "theme" {
		return
	}

	validSections := map[string]bool{
		"auto-mode": true, "orchestration-defaults": true,
		"merge-settings": true, "agent-roles": true,
		"circuit-breakers": true, "appearance": true,
		"notification-channel": true,
	}

	// Also accept short names like "merge"
	sectionAliases := map[string]string{
		"auto":          "auto-mode",
		"orchestration": "orchestration-defaults",
		"merge":         "merge-settings",
		"roles":         "agent-roles",
		"circuit":       "circuit-breakers",
		"notification":  "notification-channel",
	}
	if alias, ok := sectionAliases[section]; ok {
		section = alias
	}

	if !validSections[section] {
		http.Error(w, "invalid config section", http.StatusBadRequest)
		return
	}

	body, err := io.ReadAll(io.LimitReader(r.Body, 4096))
	if err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	var payload map[string]interface{}
	if err := json.Unmarshal(body, &payload); err != nil {
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return
	}

	// Store each setting in the blackboard
	ctx := r.Context()
	for key, val := range payload {
		var strVal string
		switch v := val.(type) {
		case string:
			strVal = v
		case bool:
			if v {
				strVal = "true"
			} else {
				strVal = "false"
			}
		default:
			b, _ := json.Marshal(v)
			strVal = string(b)
		}
		bbKey := "config:" + section + ":" + key
		_ = s.DB.SetBlackboard(ctx, bbKey, strVal, "dashboard")
	}

	writeJSON(w, map[string]string{"status": "ok", "section": section})
}
