package config

import (
	"os"
	"path/filepath"
	"strings"
)

// Mode represents whether Orchestra is running in project or global context.
type Mode int

const (
	ProjectMode Mode = iota
	GlobalMode
)

// String returns a human-readable mode name.
func (m Mode) String() string {
	switch m {
	case ProjectMode:
		return "project"
	case GlobalMode:
		return "global"
	default:
		return "unknown"
	}
}

// DetectMode determines whether the current working directory is inside a
// registered Orchestra project. Returns ProjectMode and the project root
// if found, GlobalMode otherwise.
func DetectMode() (Mode, string) {
	cwd, err := os.Getwd()
	if err != nil {
		return GlobalMode, ""
	}
	return DetectModeFrom(cwd)
}

// DetectModeFrom determines the mode for a given directory.
func DetectModeFrom(dir string) (Mode, string) {
	// First try walk-up discovery
	projectRoot, err := WalkUpDiscover(dir)
	if err == nil && projectRoot != "" {
		return ProjectMode, projectRoot
	}

	// Check if dir is inside any registered project
	absDir, err := filepath.Abs(dir)
	if err != nil {
		return GlobalMode, ""
	}
	reg, err := LoadProjects()
	if err != nil {
		return GlobalMode, ""
	}
	for path := range reg.Projects {
		if absDir == path || strings.HasPrefix(absDir, path+string(filepath.Separator)) {
			return ProjectMode, path
		}
	}

	return GlobalMode, ""
}

// ResolveDB returns the correct database path based on the current mode.
// If dbFlag is non-empty, it takes precedence. Otherwise, project mode uses
// the project's orchestrator.db and global mode returns empty string.
func ResolveDB(dbFlag string) string {
	if dbFlag != "" {
		return dbFlag
	}
	mode, projectRoot := DetectMode()
	if mode == ProjectMode {
		return filepath.Join(projectRoot, ".orchestra", "orchestrator.db")
	}
	return ""
}

// ResolveProjectByName looks up a registered project by name and returns its
// absolute path and DB path. Returns empty strings if not found.
func ResolveProjectByName(name string) (projectPath, dbPath string) {
	if name == "" {
		return "", ""
	}
	reg, err := LoadProjects()
	if err != nil {
		return "", ""
	}
	// Exact name match (case-insensitive)
	lower := strings.ToLower(name)
	for path, entry := range reg.Projects {
		if strings.ToLower(entry.Name) == lower {
			return path, filepath.Join(path, ".orchestra", "orchestrator.db")
		}
	}
	// Fallback: treat as path
	absPath, err := filepath.Abs(name)
	if err != nil {
		return "", ""
	}
	if entry, ok := reg.Projects[absPath]; ok {
		return entry.Path, filepath.Join(entry.Path, ".orchestra", "orchestrator.db")
	}
	return "", ""
}
