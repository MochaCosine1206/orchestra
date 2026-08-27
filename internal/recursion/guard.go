package recursion

import "fmt"

// Guard composes depth tracking, cycle detection, and resource caps
// into a single entry point for recursion safety.
type Guard struct{}

// NewGuard creates a new Guard.
func NewGuard() *Guard {
	return &Guard{}
}

// Enter performs all recursion safety checks and acquires the repo lock.
// Returns a cleanup function that must be called when done.
// B-030: sessionID scopes the lock at depth 0 to allow parallel conductors.
func (g *Guard) Enter(repoPath string, sessionIDs ...string) (func(), error) {
	if MaxDepthExceeded() {
		return nil, fmt.Errorf("max recursion depth exceeded (current depth: %d)", CurrentDepth())
	}

	depth := CurrentDepth()
	cleanup, err := AcquireLock(repoPath, depth, sessionIDs...)
	if err != nil {
		return nil, fmt.Errorf("acquire lock: %w", err)
	}

	return cleanup, nil
}

// MaxAgentsForCurrentDepth returns the agent cap for the current depth.
func (g *Guard) MaxAgentsForCurrentDepth() int {
	return MaxAgents(CurrentDepth())
}
