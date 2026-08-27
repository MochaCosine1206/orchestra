package recursion

import (
	"os"
	"testing"
)

func TestGuard_Enter_Success(t *testing.T) {
	os.Unsetenv("ORCHESTRA_DEPTH")
	g := NewGuard()
	cleanup, err := g.Enter("/tmp/test-guard-enter")
	if err != nil {
		t.Fatalf("Guard.Enter failed: %v", err)
	}
	defer cleanup()
}

func TestGuard_Enter_MaxDepthExceeded(t *testing.T) {
	t.Setenv("ORCHESTRA_DEPTH", "3")
	g := NewGuard()
	_, err := g.Enter("/tmp/test-guard-depth")
	if err == nil {
		t.Error("Guard.Enter should fail when max depth exceeded")
	}
}

func TestGuard_Enter_DoubleLockFails(t *testing.T) {
	os.Unsetenv("ORCHESTRA_DEPTH")
	g := NewGuard()
	cleanup, err := g.Enter("/tmp/test-guard-double")
	if err != nil {
		t.Fatalf("first Guard.Enter failed: %v", err)
	}
	defer cleanup()

	_, err = g.Enter("/tmp/test-guard-double")
	if err == nil {
		t.Error("second Guard.Enter should fail")
	}
}

func TestGuard_MaxAgentsForCurrentDepth_Zero(t *testing.T) {
	os.Unsetenv("ORCHESTRA_DEPTH")
	g := NewGuard()
	if got := g.MaxAgentsForCurrentDepth(); got != 8 {
		t.Errorf("MaxAgentsForCurrentDepth() = %d, want 8", got)
	}
}

func TestGuard_MaxAgentsForCurrentDepth_One(t *testing.T) {
	t.Setenv("ORCHESTRA_DEPTH", "1")
	g := NewGuard()
	if got := g.MaxAgentsForCurrentDepth(); got != 3 {
		t.Errorf("MaxAgentsForCurrentDepth() = %d, want 3", got)
	}
}
