package healing

import (
	"context"
	"errors"
	"testing"
)

func TestRegistryTryFixNoFixAvailable(t *testing.T) {
	r := NewRegistry()
	err := r.TryFix(BuildError{Type: ErrorSyntax}, "/tmp")
	if !errors.Is(err, ErrNoFixAvailable) {
		t.Errorf("expected ErrNoFixAvailable for syntax_error, got %v", err)
	}
	err = r.TryFix(BuildError{Type: ErrorTypeError}, "/tmp")
	if !errors.Is(err, ErrNoFixAvailable) {
		t.Errorf("expected ErrNoFixAvailable for type_error, got %v", err)
	}
	err = r.TryFix(BuildError{Type: ErrorUnknown}, "/tmp")
	if !errors.Is(err, ErrNoFixAvailable) {
		t.Errorf("expected ErrNoFixAvailable for unknown, got %v", err)
	}
}

func TestRegistryTryFixMapped(t *testing.T) {
	called := false
	r := &Registry{
		fixes: map[ErrorType]FixFunc{
			ErrorMissingImport: func(ctx context.Context, path string) error {
				called = true
				if path != "/tmp/test" {
					t.Errorf("expected path /tmp/test, got %s", path)
				}
				return nil
			},
		},
	}
	err := r.TryFix(BuildError{Type: ErrorMissingImport}, "/tmp/test")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !called {
		t.Error("fix function was not called")
	}
}

func TestRegistryTryFixPropagatesError(t *testing.T) {
	errFix := errors.New("fix failed")
	r := &Registry{
		fixes: map[ErrorType]FixFunc{
			ErrorMissingDep: func(ctx context.Context, path string) error {
				return errFix
			},
		},
	}
	err := r.TryFix(BuildError{Type: ErrorMissingDep}, "/tmp")
	if !errors.Is(err, errFix) {
		t.Errorf("expected fix error to propagate, got %v", err)
	}
}

func TestRegistryTryFixTimeout(t *testing.T) {
	r := &Registry{
		fixes: map[ErrorType]FixFunc{
			ErrorMissingImport: func(ctx context.Context, path string) error {
				<-ctx.Done()
				return ctx.Err()
			},
		},
	}
	// The TryFix uses a 30s timeout internally; we verify the context is passed.
	// For a fast test, we use a custom registry with a function that checks ctx.
	// The real timeout test would take 30s, so we just verify ctx propagation.
	called := false
	r2 := &Registry{
		fixes: map[ErrorType]FixFunc{
			ErrorMissingImport: func(ctx context.Context, path string) error {
				if _, ok := ctx.Deadline(); !ok {
					t.Error("expected context to have a deadline")
				}
				called = true
				return nil
			},
		},
	}
	if err := r2.TryFix(BuildError{Type: ErrorMissingImport}, "/tmp"); err != nil {
		t.Fatal(err)
	}
	if !called {
		t.Error("fix function was not called")
	}
	_ = r // suppress unused
}
