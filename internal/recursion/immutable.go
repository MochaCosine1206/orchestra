package recursion

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// ImmutablePaths returns the default set of protected paths.
func ImmutablePaths() []string {
	return []string{
		"internal/recursion/",
		"internal/agent/classifier.go",
		"internal/monitor/",
		"internal/db/schema.sql",
		"scripts/eval/",
		"scripts/safety/",
	}
}

type permRecord struct {
	Path string      `json:"path"`
	Mode os.FileMode `json:"mode"`
}

var manifestPath = filepath.Join(os.TempDir(), "orchestra-immutable-manifest.json")

// LockPaths sets the given files to read-only (0444), storing original
// permissions in a temp manifest so UnlockPaths can restore them.
func LockPaths(paths []string) error {
	var records []permRecord
	for _, p := range paths {
		info, err := os.Stat(p)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return fmt.Errorf("stat %s: %w", p, err)
		}
		if info.IsDir() {
			continue
		}
		records = append(records, permRecord{Path: p, Mode: info.Mode()})
		if err := os.Chmod(p, 0444); err != nil {
			return fmt.Errorf("chmod %s: %w", p, err)
		}
	}

	data, err := json.Marshal(records)
	if err != nil {
		return fmt.Errorf("marshal manifest: %w", err)
	}
	if err := os.WriteFile(manifestPath, data, 0600); err != nil {
		return fmt.Errorf("write manifest: %w", err)
	}
	return nil
}

// UnlockPaths restores file permissions from the temp manifest written by LockPaths.
func UnlockPaths() error {
	data, err := os.ReadFile(manifestPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("read manifest: %w", err)
	}

	var records []permRecord
	if err := json.Unmarshal(data, &records); err != nil {
		return fmt.Errorf("unmarshal manifest: %w", err)
	}

	for _, r := range records {
		if err := os.Chmod(r.Path, r.Mode); err != nil {
			if !os.IsNotExist(err) {
				return fmt.Errorf("restore %s: %w", r.Path, err)
			}
		}
	}

	return os.Remove(manifestPath)
}
