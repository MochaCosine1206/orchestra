package config

import (
	"os"
	"path/filepath"
	"time"
)

const orchestraDBMarker = ".orchestra/orchestrator.db"

// WalkUpDiscover walks from startDir toward the filesystem root looking for
// a directory containing .orchestra/orchestrator.db. Returns the project root
// path, or empty string if not found.
func WalkUpDiscover(startDir string) (string, error) {
	dir, err := filepath.Abs(startDir)
	if err != nil {
		return "", err
	}
	for {
		candidate := filepath.Join(dir, orchestraDBMarker)
		if _, err := os.Stat(candidate); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", nil
		}
		dir = parent
	}
}

// ScanDirectory recursively scans root for Orchestra projects up to maxDepth.
// Returns a list of absolute paths that contain .orchestra/orchestrator.db.
func ScanDirectory(root string, maxDepth int) ([]string, error) {
	root, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	var projects []string
	err = scanDir(root, 0, maxDepth, &projects)
	return projects, err
}

func scanDir(dir string, depth, maxDepth int, results *[]string) error {
	if depth > maxDepth {
		return nil
	}
	candidate := filepath.Join(dir, orchestraDBMarker)
	if _, err := os.Stat(candidate); err == nil {
		*results = append(*results, dir)
		return nil // don't recurse into discovered projects
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil // skip unreadable directories
	}
	for _, e := range entries {
		if !e.IsDir() || e.Name()[0] == '.' {
			continue
		}
		if err := scanDir(filepath.Join(dir, e.Name()), depth+1, maxDepth, results); err != nil {
			return err
		}
	}
	return nil
}

// RegisterProject adds a project to the registry, deduplicating by absolute path.
// Returns true if the project was newly added, false if it already existed.
func RegisterProject(absPath, name string, autoRegistered bool) (bool, error) {
	absPath, err := filepath.Abs(absPath)
	if err != nil {
		return false, err
	}
	reg, err := LoadProjects()
	if err != nil {
		return false, err
	}
	if _, exists := reg.Projects[absPath]; exists {
		// Update last_active on re-registration
		reg.Projects[absPath].LastActive = time.Now()
		return false, SaveProjects(reg)
	}
	if name == "" {
		name = filepath.Base(absPath)
	}
	reg.Projects[absPath] = &ProjectEntry{
		Path:           absPath,
		Name:           name,
		LastActive:     time.Now(),
		AutoRegistered: autoRegistered,
	}
	return true, SaveProjects(reg)
}

// AutoRegister silently registers the current project if it contains
// .orchestra/orchestrator.db and is not already in the registry.
func AutoRegister() error {
	cwd, err := os.Getwd()
	if err != nil {
		return nil // fail silently
	}
	projectRoot, err := WalkUpDiscover(cwd)
	if err != nil || projectRoot == "" {
		return nil // not in a project
	}
	_, err = RegisterProject(projectRoot, "", true)
	return err
}
