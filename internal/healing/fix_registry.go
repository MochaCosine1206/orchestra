package healing

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"time"
)

const fixTimeout = 30 * time.Second

// ErrNoFixAvailable is returned when no fix function is registered for an error type.
var ErrNoFixAvailable = errors.New("no fix available for this error type")

// FixFunc executes a fix in the given worktree directory.
type FixFunc func(ctx context.Context, worktreePath string) error

// Registry maps error types to fix functions.
type Registry struct {
	fixes map[ErrorType]FixFunc
}

// NewRegistry creates a Registry with default fix functions.
func NewRegistry() *Registry {
	return &Registry{
		fixes: map[ErrorType]FixFunc{
			ErrorMissingImport: FixMissingImport,
			ErrorMissingDep:    FixMissingDep,
		},
	}
}

// TryFix looks up the fix for the given error type and executes it with a 30-second timeout.
func (r *Registry) TryFix(be BuildError, worktreePath string) error {
	fn, ok := r.fixes[be.Type]
	if !ok {
		return ErrNoFixAvailable
	}
	ctx, cancel := context.WithTimeout(context.Background(), fixTimeout)
	defer cancel()
	return fn(ctx, worktreePath)
}

// FixMissingImport runs goimports on the worktree.
func FixMissingImport(ctx context.Context, worktreePath string) error {
	cmd := exec.CommandContext(ctx, "goimports", "-w", ".")
	cmd.Dir = worktreePath
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("goimports: %w: %s", err, out)
	}
	return nil
}

// FixMissingDep runs go get ./... in the worktree.
func FixMissingDep(ctx context.Context, worktreePath string) error {
	cmd := exec.CommandContext(ctx, "go", "get", "./...")
	cmd.Dir = worktreePath
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("go get: %w: %s", err, out)
	}
	return nil
}

// FixModTidy runs go mod tidy in the worktree.
func FixModTidy(ctx context.Context, worktreePath string) error {
	cmd := exec.CommandContext(ctx, "go", "mod", "tidy")
	cmd.Dir = worktreePath
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("go mod tidy: %w: %s", err, out)
	}
	return nil
}
