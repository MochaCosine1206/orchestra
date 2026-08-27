package bridge

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// RoutingConfig is the persisted mapping of topic IDs to project paths.
type RoutingConfig struct {
	Projects       map[string]ProjectMapping `json:"projects"`        // slug → mapping
	DefaultProject string                    `json:"default_project"` // current default slug
}

// ProjectMapping maps a Telegram forum topic to a project directory.
type ProjectMapping struct {
	TopicID     int64  `json:"topic_id"`
	ProjectPath string `json:"project_path"`
	DisplayName string `json:"display_name,omitempty"`
}

// ProjectRouter resolves Telegram forum topic IDs to project paths.
type ProjectRouter struct {
	mu     sync.RWMutex
	config RoutingConfig
	path   string // config file path
}

// NewProjectRouter loads or creates a routing config.
func NewProjectRouter() (*ProjectRouter, error) {
	cfgDir, err := ConfigDir()
	if err != nil {
		return nil, err
	}

	path := filepath.Join(cfgDir, "telegram-routing.json")
	r := &ProjectRouter{
		path: path,
		config: RoutingConfig{
			Projects: make(map[string]ProjectMapping),
		},
	}

	data, err := os.ReadFile(path)
	if err == nil {
		if err := json.Unmarshal(data, &r.config); err != nil {
			r.config.Projects = make(map[string]ProjectMapping)
		}
	}
	if r.config.Projects == nil {
		r.config.Projects = make(map[string]ProjectMapping)
	}

	return r, nil
}

// RouteMessage resolves a topic ID to a project path.
// Returns the project path and nil on success, or an error for unknown topics.
func (r *ProjectRouter) RouteMessage(topicID int64) (string, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	for _, m := range r.config.Projects {
		if m.TopicID == topicID {
			return m.ProjectPath, nil
		}
	}

	return "", fmt.Errorf("unknown topic ID %d — use /list to see registered projects", topicID)
}

// RegisterProject adds or updates a project's topic mapping and persists to disk.
func (r *ProjectRouter) RegisterProject(slug string, topicID int64, projectPath, displayName string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.config.Projects[slug] = ProjectMapping{
		TopicID:     topicID,
		ProjectPath: projectPath,
		DisplayName: displayName,
	}

	return r.save()
}

// SetDefault sets the default project context for cross-project commands.
func (r *ProjectRouter) SetDefault(slug string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if _, ok := r.config.Projects[slug]; !ok {
		return fmt.Errorf("project %q not registered — use /list to see available projects", slug)
	}

	r.config.DefaultProject = slug
	return r.save()
}

// GetDefault returns the default project slug.
func (r *ProjectRouter) GetDefault() string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.config.DefaultProject
}

// SlugForTopic returns the slug owning the given topic ID, or empty string.
func (r *ProjectRouter) SlugForTopic(topicID int64) string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	for slug, m := range r.config.Projects {
		if m.TopicID == topicID {
			return slug
		}
	}
	return ""
}

// ListProjects returns all registered project mappings.
func (r *ProjectRouter) ListProjects() map[string]ProjectMapping {
	r.mu.RLock()
	defer r.mu.RUnlock()

	cp := make(map[string]ProjectMapping, len(r.config.Projects))
	for k, v := range r.config.Projects {
		cp[k] = v
	}
	return cp
}

func (r *ProjectRouter) save() error {
	data, err := json.MarshalIndent(r.config, "", "  ")
	if err != nil {
		return fmt.Errorf("marshalling routing config: %w", err)
	}
	return os.WriteFile(r.path, append(data, '\n'), 0644)
}

// --- Cross-Project Command Handlers ---

// HandleCrossProjectCommand processes /status, /list, and /switch commands.
// Returns the response text and true if the command was handled.
func HandleCrossProjectCommand(cmd string, router *ProjectRouter) (string, bool) {
	cmd = strings.TrimSpace(cmd)

	switch {
	case cmd == "/status":
		return formatStatusAll(router), true
	case cmd == "/list":
		return formatProjectList(router), true
	case strings.HasPrefix(cmd, "/switch "):
		target := strings.TrimSpace(strings.TrimPrefix(cmd, "/switch "))
		return handleSwitch(router, target), true
	case cmd == "/switch":
		return "Usage: /switch <project-name>\n\nUse /list to see available projects.", true
	default:
		return "", false
	}
}

func formatStatusAll(router *ProjectRouter) string {
	projects := router.ListProjects()
	if len(projects) == 0 {
		return "No projects registered. Run `orchestra go` in a project with Telegram enabled to register it."
	}

	var b strings.Builder
	b.WriteString("<b>Project Status</b>\n\n")

	defaultSlug := router.GetDefault()
	for slug, m := range projects {
		marker := "  "
		if slug == defaultSlug {
			marker = "→ "
		}
		name := m.DisplayName
		if name == "" {
			name = slug
		}
		b.WriteString(fmt.Sprintf("%s<b>%s</b>\n", marker, EscapeHTML(name)))
		b.WriteString(fmt.Sprintf("   Path: <code>%s</code>\n", EscapeHTML(m.ProjectPath)))
		b.WriteString(fmt.Sprintf("   Topic: %d\n\n", m.TopicID))
	}

	return b.String()
}

func formatProjectList(router *ProjectRouter) string {
	projects := router.ListProjects()
	if len(projects) == 0 {
		return "No projects registered.\n\nRun <code>orchestra go</code> in a project with Telegram enabled to register it."
	}

	var b strings.Builder
	b.WriteString("<b>Registered Projects</b>\n\n")

	defaultSlug := router.GetDefault()
	for slug, m := range projects {
		name := m.DisplayName
		if name == "" {
			name = slug
		}
		if slug == defaultSlug {
			b.WriteString(fmt.Sprintf("• <b>%s</b> (default)\n", EscapeHTML(name)))
		} else {
			b.WriteString(fmt.Sprintf("• %s\n", EscapeHTML(name)))
		}
	}

	b.WriteString("\nUse <code>/switch &lt;name&gt;</code> to change the default project.")
	return b.String()
}

func handleSwitch(router *ProjectRouter, target string) string {
	// Try exact slug match first
	if err := router.SetDefault(target); err == nil {
		return fmt.Sprintf("Switched default project to <b>%s</b>.", EscapeHTML(target))
	}

	// Try case-insensitive partial match
	projects := router.ListProjects()
	target = strings.ToLower(target)
	for slug, m := range projects {
		name := m.DisplayName
		if name == "" {
			name = slug
		}
		if strings.ToLower(slug) == target || strings.Contains(strings.ToLower(name), target) {
			if err := router.SetDefault(slug); err != nil {
				return fmt.Sprintf("Error switching: %v", err)
			}
			return fmt.Sprintf("Switched default project to <b>%s</b>.", EscapeHTML(name))
		}
	}

	return fmt.Sprintf("Project %q not found. Use /list to see available projects.", target)
}

// HandleUnknownTopic returns a helpful error message for messages from unregistered topics.
func HandleUnknownTopic(topicID int64) string {
	return fmt.Sprintf(
		"Unknown topic (ID: %d).\n\nThis topic isn't linked to any project. Use /list to see registered projects, or run <code>orchestra go</code> in the project directory to register it.",
		topicID,
	)
}
