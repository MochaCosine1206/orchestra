package monitor

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/MochaCosine1206/orchestra/internal/db"
	"github.com/fsnotify/fsnotify"
)

const (
	debounceCooldown = 500 * time.Millisecond
)

// skipDirs are directories that should never be watched inside worktrees.
var skipDirs = map[string]bool{
	".git":              true,
	".claude":           true,
	".orchestra-hooks":  true,
	"node_modules":      true,
	".worktree":         true,
}

// FileWatcher monitors agent worktrees for file ownership violations in real-time.
type FileWatcher struct {
	DB  *db.DB
	Log func(string)

	mu         sync.Mutex
	watcher    *fsnotify.Watcher
	cancel     context.CancelFunc
	done       chan struct{}
	worktrees  map[string]string            // taskID → worktree path
	ownedFiles map[string]map[string]bool   // taskID → set of owned file paths
	debounce   map[string]time.Time         // abs file path → last event time
}

// NewFileWatcher creates a new FileWatcher. Call Start to begin watching.
func NewFileWatcher(database *db.DB, logFn func(string)) *FileWatcher {
	return &FileWatcher{
		DB:         database,
		Log:        logFn,
		worktrees:  make(map[string]string),
		ownedFiles: make(map[string]map[string]bool),
		debounce:   make(map[string]time.Time),
	}
}

func (fw *FileWatcher) log(format string, args ...interface{}) {
	if fw.Log != nil {
		fw.Log(fmt.Sprintf("[watcher] "+format, args...))
	}
}

// Start begins the file watching goroutine.
func (fw *FileWatcher) Start(ctx context.Context) error {
	w, err := fsnotify.NewWatcher()
	if err != nil {
		fw.log("failed to create fsnotify watcher: %v", err)
		return err
	}

	ctx, cancel := context.WithCancel(ctx)
	fw.mu.Lock()
	fw.watcher = w
	fw.cancel = cancel
	fw.done = make(chan struct{})
	fw.mu.Unlock()

	go fw.run(ctx)
	fw.log("started")
	return nil
}

// Stop terminates the file watching goroutine.
func (fw *FileWatcher) Stop() {
	fw.mu.Lock()
	cancel := fw.cancel
	done := fw.done
	fw.mu.Unlock()

	if cancel != nil {
		cancel()
	}
	if done != nil {
		<-done
	}

	fw.mu.Lock()
	if fw.watcher != nil {
		fw.watcher.Close()
		fw.watcher = nil
	}
	fw.mu.Unlock()

	fw.log("stopped")
}

// AddWorktree registers a task's worktree for file ownership monitoring.
func (fw *FileWatcher) AddWorktree(ctx context.Context, taskID, worktreePath string) {
	fw.mu.Lock()
	defer fw.mu.Unlock()

	fw.worktrees[taskID] = worktreePath

	// Load owned files from DB
	owned := make(map[string]bool)
	locks, err := fw.DB.ListFileLocksForTask(ctx, taskID)
	if err != nil {
		fw.log("failed to load file locks for %s: %v", taskID, err)
	} else {
		for _, lock := range locks {
			owned[lock.FilePath] = true
		}
	}
	fw.ownedFiles[taskID] = owned

	// Add worktree directories to fsnotify
	if fw.watcher != nil {
		fw.addWorktreeDirs(worktreePath)
	}

	fw.log("watching worktree for %s: %s (%d owned files)", taskID, worktreePath, len(owned))
}

// RemoveWorktree stops monitoring a task's worktree.
func (fw *FileWatcher) RemoveWorktree(taskID string) {
	fw.mu.Lock()
	defer fw.mu.Unlock()

	wtPath, exists := fw.worktrees[taskID]
	if !exists {
		return
	}

	// Remove fsnotify watches for this worktree
	if fw.watcher != nil {
		fw.removeWorktreeDirs(wtPath)
	}

	delete(fw.worktrees, taskID)
	delete(fw.ownedFiles, taskID)
	fw.log("stopped watching worktree for %s", taskID)
}

// addWorktreeDirs walks a worktree and adds each directory to fsnotify.
func (fw *FileWatcher) addWorktreeDirs(root string) {
	filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil // skip dirs we can't read
		}
		if !info.IsDir() {
			return nil
		}
		base := filepath.Base(path)
		if skipDirs[base] && path != root {
			return filepath.SkipDir
		}
		if err := fw.watcher.Add(path); err != nil {
			fw.log("failed to watch %s: %v", path, err)
		}
		return nil
	})
}

// removeWorktreeDirs removes fsnotify watches for a worktree's directories.
func (fw *FileWatcher) removeWorktreeDirs(root string) {
	filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if !info.IsDir() {
			return nil
		}
		base := filepath.Base(path)
		if skipDirs[base] && path != root {
			return filepath.SkipDir
		}
		fw.watcher.Remove(path) // ignore errors on cleanup
		return nil
	})
}

func (fw *FileWatcher) run(ctx context.Context) {
	defer func() {
		if r := recover(); r != nil {
			fw.log("panic in watcher: %v", r)
		}
		close(fw.done)
	}()

	for {
		select {
		case <-ctx.Done():
			return

		case event, ok := <-fw.watcher.Events:
			if !ok {
				return
			}
			// Only care about Write and Create events
			if event.Op&(fsnotify.Write|fsnotify.Create) == 0 {
				continue
			}
			fw.handleEvent(ctx, event)

		case err, ok := <-fw.watcher.Errors:
			if !ok {
				return
			}
			fw.log("fsnotify error: %v", err)
		}
	}
}

func (fw *FileWatcher) handleEvent(ctx context.Context, event fsnotify.Event) {
	absPath := event.Name

	// Skip hidden/system directories
	for _, part := range strings.Split(absPath, string(filepath.Separator)) {
		if skipDirs[part] {
			return
		}
	}

	// Debounce: skip if we saw this file recently
	fw.mu.Lock()
	if last, ok := fw.debounce[absPath]; ok && time.Since(last) < debounceCooldown {
		fw.mu.Unlock()
		return
	}
	fw.debounce[absPath] = time.Now()
	fw.mu.Unlock()

	// Resolve which task owns this path
	taskID := fw.resolveTaskForPath(absPath)
	if taskID == "" {
		return // file not in any watched worktree
	}

	// Get the relative path within the worktree
	fw.mu.Lock()
	wtPath := fw.worktrees[taskID]
	owned := fw.ownedFiles[taskID]
	fw.mu.Unlock()

	relPath, err := filepath.Rel(wtPath, absPath)
	if err != nil {
		return
	}

	// Check if file is owned by this task
	if owned[relPath] {
		return // modification is within owned files, no violation
	}

	// Role-aware zero-lock behavior (doc 021, Gap 3):
	// Scout/researcher roles legitimately have zero locks — skip them.
	// Implementer tasks with zero locks indicate a decomposer failure — log a warning
	// but still skip enforcement (we can't block without knowing which files are valid).
	if len(owned) == 0 {
		task, dbErr := fw.DB.GetTaskByID(ctx, taskID)
		if dbErr == nil && task != nil && task.Role == "implementer" {
			fw.log("WARNING: implementer task %s has zero file locks but modified %s — likely a decomposer failure (file not in goal enum)", taskID, relPath)
			fw.DB.LogEvent(ctx, "zero_lock_implementer_write", "", taskID,
				fmt.Sprintf(`{"task_id":%q,"file":%q,"event":%q}`, taskID, relPath, event.Op.String()))
		}
		return
	}

	// File ownership violation detected
	fw.log("VIOLATION: task %s modified %s (not in owned files)", taskID, relPath)
	payload := fmt.Sprintf(`{"task_id":%q,"file":%q,"event":%q}`, taskID, relPath, event.Op.String())
	fw.DB.LogEvent(ctx, "file_ownership_violation_detected", "", taskID, payload)
	fw.DB.SetBlackboard(ctx, "file_violation:"+taskID+":"+relPath, time.Now().Format(time.RFC3339), "watcher")
}

// resolveTaskForPath finds which task's worktree contains the given absolute path.
func (fw *FileWatcher) resolveTaskForPath(absPath string) string {
	fw.mu.Lock()
	defer fw.mu.Unlock()

	for taskID, wtPath := range fw.worktrees {
		if strings.HasPrefix(absPath, wtPath+string(filepath.Separator)) {
			return taskID
		}
	}
	return ""
}

// WatchedTaskCount returns the number of tasks currently being watched.
func (fw *FileWatcher) WatchedTaskCount() int {
	fw.mu.Lock()
	defer fw.mu.Unlock()
	return len(fw.worktrees)
}
