package config

import "time"

// ProjectEntry represents a single registered Orchestra project.
type ProjectEntry struct {
	Path           string    `json:"path"`
	Name           string    `json:"name"`
	LastActive     time.Time `json:"last_active"`
	AutoRegistered bool      `json:"auto_registered"`
	Tags           []string  `json:"tags,omitempty"`
}

// ProjectsRegistry holds all registered projects.
type ProjectsRegistry struct {
	Projects map[string]*ProjectEntry `json:"projects"`
	Version  int                      `json:"version"`
}

// NewProjectsRegistry returns an empty registry.
func NewProjectsRegistry() *ProjectsRegistry {
	return &ProjectsRegistry{
		Projects: make(map[string]*ProjectEntry),
		Version:  1,
	}
}

// DaemonConfig holds daemon-specific settings.
type DaemonConfig struct {
	Interval      string            `json:"interval"`
	QuietHours    *QuietHoursConfig `json:"quiet_hours,omitempty"`
	MaxConcurrent int               `json:"max_concurrent"`
	Sandbox       bool              `json:"sandbox,omitempty"`
	Schedules     []ScheduleEntry   `json:"schedules,omitempty"`
}

// QuietHoursConfig defines when the daemon should avoid spawning work.
type QuietHoursConfig struct {
	Start string `json:"start"`
	End   string `json:"end"`
}

// ScheduleEntry defines a cron-like schedule for a project.
type ScheduleEntry struct {
	Project  string `json:"project"`
	Cron     string `json:"cron"`
	Goal     string `json:"goal,omitempty"`
	Priority string `json:"priority,omitempty"` // "background" (tier 6, idle), "daily" (deadline=today), "critical" (bypass queue), "fix" (tier 2, retry-level)
	Disabled bool   `json:"disabled,omitempty"`
}

// GitHubAppConfig holds credentials for GitHub App authentication.
// Loaded from ~/.config/orchestra/github-app.json.
type GitHubAppConfig struct {
	AppID          int64  `json:"app_id"`
	PrivateKeyPath string `json:"private_key_path"`
	InstallationID int64  `json:"installation_id"`
}

// DefaultDaemonConfig returns sensible defaults.
func DefaultDaemonConfig() *DaemonConfig {
	return &DaemonConfig{
		Interval:      "5m",
		MaxConcurrent: 2,
	}
}
