// Package config provides read/write access to the global Orchestra configuration
// stored at ~/.config/orchestra/config.json (XDG-compliant).
package config

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// GlobalConfig holds user-level Orchestra settings that persist across projects.
type GlobalConfig struct {
	DefaultProjectDir string            `json:"default_project_dir,omitempty"`
	MaxTasks          int               `json:"max_tasks,omitempty"`
	MaxFilesPerTask   int               `json:"max_files_per_task,omitempty"`
	ModelStrategy     string            `json:"model_strategy,omitempty"`
	ReviewEnabled     *bool             `json:"review_enabled,omitempty"`
	LocalRunner       *LocalRunnerEntry `json:"local_runner,omitempty"`
}

// LocalRunnerEntry configures the local LLM runner (pi/Ollama) at the system level.
// When set, all Orchestra projects can route eligible tasks to the local model.
type LocalRunnerEntry struct {
	Enabled       bool   `json:"enabled"`
	Command       string `json:"command"`
	Model         string `json:"model"`
	MaxConcurrent int    `json:"max_concurrent"`
	MaxFiles      int    `json:"max_files"`
}

// GlobalDir returns the path to the global Orchestra config directory.
// Respects ORCHESTRA_CONFIG_DIR env var, otherwise uses ~/.config/orchestra/.
// Creates the directory if it does not exist.
func GlobalDir() (string, error) {
	dir := os.Getenv("ORCHESTRA_CONFIG_DIR")
	if dir == "" {
		base, err := os.UserConfigDir()
		if err != nil {
			base = filepath.Join(os.Getenv("HOME"), ".config")
		}
		dir = filepath.Join(base, "orchestra")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	return dir, nil
}

// Path returns the path to the global config file.
// Uses ORCHESTRA_CONFIG env var if set, otherwise os.UserConfigDir()/orchestra/config.json.
func Path() string {
	if p := os.Getenv("ORCHESTRA_CONFIG"); p != "" {
		return p
	}
	dir, err := os.UserConfigDir()
	if err != nil {
		dir = filepath.Join(os.Getenv("HOME"), ".config")
	}
	return filepath.Join(dir, "orchestra", "config.json")
}

// Load reads the global config from disk.
// Returns a zero-value config (not an error) if the file does not exist.
func Load() (*GlobalConfig, error) {
	data, err := os.ReadFile(Path())
	if err != nil {
		if os.IsNotExist(err) {
			return &GlobalConfig{}, nil
		}
		return nil, err
	}
	var cfg GlobalConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, err
	}
	return &cfg, nil
}

// Save writes the global config to disk, creating parent directories as needed.
func Save(cfg *GlobalConfig) error {
	p := Path()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(p, append(data, '\n'), 0o644)
}

// ProjectsPath returns the path to the projects registry file.
func ProjectsPath() (string, error) {
	dir, err := GlobalDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "projects.json"), nil
}

// LoadProjects reads the project registry from disk.
// Returns an empty registry if the file does not exist.
func LoadProjects() (*ProjectsRegistry, error) {
	p, err := ProjectsPath()
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(p)
	if err != nil {
		if os.IsNotExist(err) {
			return NewProjectsRegistry(), nil
		}
		return nil, err
	}
	var reg ProjectsRegistry
	if err := json.Unmarshal(data, &reg); err != nil {
		return nil, err
	}
	if reg.Projects == nil {
		reg.Projects = make(map[string]*ProjectEntry)
	}
	return &reg, nil
}

// SaveProjects writes the project registry to disk.
func SaveProjects(reg *ProjectsRegistry) error {
	p, err := ProjectsPath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(reg, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(p, append(data, '\n'), 0o644)
}

// DaemonConfigPath returns the path to the daemon config file.
func DaemonConfigPath() (string, error) {
	dir, err := GlobalDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "daemon.json"), nil
}

// LoadDaemonConfig reads the daemon config from disk.
// Returns defaults if the file does not exist.
func LoadDaemonConfig() (*DaemonConfig, error) {
	p, err := DaemonConfigPath()
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(p)
	if err != nil {
		if os.IsNotExist(err) {
			return DefaultDaemonConfig(), nil
		}
		return nil, err
	}
	var cfg DaemonConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, err
	}
	return &cfg, nil
}

// GitHubAppConfigPath returns the path to the GitHub App config file.
func GitHubAppConfigPath() (string, error) {
	dir, err := GlobalDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "github-app.json"), nil
}

// LoadGitHubAppConfig reads the GitHub App config from disk.
// Returns nil (not an error) if the file does not exist — caller should fall back to existing gh auth.
func LoadGitHubAppConfig() (*GitHubAppConfig, error) {
	p, err := GitHubAppConfigPath()
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(p)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var cfg GitHubAppConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, err
	}
	return &cfg, nil
}

// SaveDaemonConfig writes the daemon config to disk.
func SaveDaemonConfig(cfg *DaemonConfig) error {
	p, err := DaemonConfigPath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(p, append(data, '\n'), 0o644)
}
