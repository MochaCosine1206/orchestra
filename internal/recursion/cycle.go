package recursion

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

var lockDir = filepath.Join(os.TempDir(), "orchestra-locks")

// AcquireLock acquires an exclusive flock for the given repoPath+depth combination.
// Returns a cleanup function that releases the lock and removes the lock file.
// Returns an error if the lock is already held.
//
// B-030: At depth 0, parallel conductors are allowed (lock is session-scoped).
// At depth > 0, the lock prevents recursive Orchestra-calling-Orchestra loops.
// The sessionID parameter scopes the lock at depth 0 to allow parallelism.
func AcquireLock(repoPath string, depth int, sessionIDs ...string) (func(), error) {
	if err := os.MkdirAll(lockDir, 0755); err != nil {
		return nil, fmt.Errorf("create lock dir: %w", err)
	}

	// At depth 0, scope lock by session to allow parallel conductors.
	// At depth > 0, scope by repo+depth to prevent recursive loops.
	var lockKey string
	if depth == 0 && len(sessionIDs) > 0 && sessionIDs[0] != "" {
		lockKey = fmt.Sprintf("%s:session:%s", repoPath, sessionIDs[0])
	} else {
		lockKey = fmt.Sprintf("%s:%d", repoPath, depth)
	}

	h := sha256.Sum256([]byte(lockKey))
	name := hex.EncodeToString(h[:16]) + ".lock"
	lockPath := filepath.Join(lockDir, name)

	f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, fmt.Errorf("open lock file: %w", err)
	}

	err = syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
	if err != nil {
		f.Close()
		return nil, fmt.Errorf("lock already held for %s at depth %d", repoPath, depth)
	}

	cleanup := func() {
		syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		f.Close()
		os.Remove(lockPath)
	}

	return cleanup, nil
}
