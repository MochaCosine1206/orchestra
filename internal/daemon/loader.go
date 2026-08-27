package daemon

import (
	"os"
	"time"

	"github.com/MochaCosine1206/orchestra/internal/config"
)

// Loader watches daemon.json for changes and reloads when the mtime changes.
type Loader struct {
	lastMtime time.Time
	Config    *config.DaemonConfig
	path      string
}

// NewLoader creates a Loader that tracks daemon.json changes.
func NewLoader() (*Loader, error) {
	p, err := config.DaemonConfigPath()
	if err != nil {
		return nil, err
	}
	cfg, err := config.LoadDaemonConfig()
	if err != nil {
		return nil, err
	}
	l := &Loader{
		Config: cfg,
		path:   p,
	}
	// Record initial mtime
	if info, err := os.Stat(p); err == nil {
		l.lastMtime = info.ModTime()
	}
	return l, nil
}

// Reload checks daemon.json mtime and reloads config only if it changed.
// Returns true if config was reloaded.
func (l *Loader) Reload() (bool, error) {
	info, err := os.Stat(l.path)
	if err != nil {
		if os.IsNotExist(err) {
			// File deleted — keep current config
			return false, nil
		}
		return false, err
	}

	if !info.ModTime().After(l.lastMtime) {
		return false, nil
	}

	cfg, err := config.LoadDaemonConfig()
	if err != nil {
		return false, err
	}

	l.Config = cfg
	l.lastMtime = info.ModTime()
	return true, nil
}
