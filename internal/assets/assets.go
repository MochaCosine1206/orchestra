// Package assets provides embedded static files bundled into the orchestra binary.
// These include agent definitions, permission profiles, and templates that are
// used as defaults when no filesystem override exists.
package assets

import (
	"embed"
	"fmt"
	"io/fs"
	"path/filepath"
	"strings"
)

//go:embed agents/*.md
var Agents embed.FS

//go:embed profiles/*.json
var Profiles embed.FS

//go:embed templates/*
var Templates embed.FS

//go:embed examples/*.yaml
var Examples embed.FS

// AgentDef returns the embedded agent definition for a role (e.g. "architect").
func AgentDef(role string) ([]byte, error) {
	data, err := Agents.ReadFile(filepath.Join("agents", role+".md"))
	if err != nil {
		return nil, fmt.Errorf("embedded agent def %q: %w", role, err)
	}
	return data, nil
}

// Profile returns the embedded permission profile for a role (e.g. "architect").
func Profile(role string) ([]byte, error) {
	data, err := Profiles.ReadFile(filepath.Join("profiles", role+".json"))
	if err != nil {
		return nil, fmt.Errorf("embedded profile %q: %w", role, err)
	}
	return data, nil
}

// Template returns the embedded template by name (e.g. "task-spec.md").
func Template(name string) ([]byte, error) {
	data, err := Templates.ReadFile(filepath.Join("templates", name))
	if err != nil {
		return nil, fmt.Errorf("embedded template %q: %w", name, err)
	}
	return data, nil
}

// ExampleSpec returns an embedded example spec by name (e.g. "billing-api-spec.yaml").
func ExampleSpec(name string) ([]byte, error) {
	data, err := Examples.ReadFile(filepath.Join("examples", name))
	if err != nil {
		return nil, fmt.Errorf("embedded example spec %q: %w", name, err)
	}
	return data, nil
}

// AllAgentRoles returns the list of embedded agent role names (without extension).
func AllAgentRoles() []string {
	var roles []string
	fs.WalkDir(Agents, "agents", func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		name := d.Name()
		if strings.HasSuffix(name, ".md") {
			roles = append(roles, strings.TrimSuffix(name, ".md"))
		}
		return nil
	})
	return roles
}
