package cmd

import (
	"os"
	"regexp"
	"testing"

	"github.com/MochaCosine1206/orchestra/internal/db"
	"github.com/MochaCosine1206/orchestra/internal/recursion"
)

func TestSessionLogFilenamePattern(t *testing.T) {
	sessionID := db.GenID("s")
	logFilename := "conductor-" + sessionID + ".log"

	// Pattern: conductor-s-YYYYMMDD-HHMMSS-XXXX.log
	pattern := `^conductor-s-\d{8}-\d{6}-[0-9a-f]{4}\.log$`
	matched, err := regexp.MatchString(pattern, logFilename)
	if err != nil {
		t.Fatalf("regex error: %v", err)
	}
	if !matched {
		t.Errorf("log filename %q does not match expected pattern %s", logFilename, pattern)
	}
}

func TestSessionLogFilenameUniqueness(t *testing.T) {
	seen := make(map[string]bool)
	for i := 0; i < 100; i++ {
		id := db.GenID("s")
		name := "conductor-" + id + ".log"
		if seen[name] {
			t.Fatalf("duplicate log filename after %d iterations: %s", i, name)
		}
		seen[name] = true
	}
}

func TestRecursionGuardBlocksExcessiveDepth(t *testing.T) {
	// Set depth to 3 (exceeds max of 2).
	os.Setenv("ORCHESTRA_DEPTH", "3")
	defer os.Unsetenv("ORCHESTRA_DEPTH")

	guard := recursion.NewGuard()
	_, err := guard.Enter(t.TempDir())
	if err == nil {
		t.Fatal("expected error when depth exceeds max, got nil")
	}
	if got := err.Error(); !containsAll(got, "max recursion depth") {
		t.Errorf("expected error to mention 'max recursion depth', got: %s", got)
	}
}

func TestRecursionGuardCapsMaxParallel(t *testing.T) {
	os.Setenv("ORCHESTRA_DEPTH", "1")
	defer os.Unsetenv("ORCHESTRA_DEPTH")

	guard := recursion.NewGuard()
	cap := guard.MaxAgentsForCurrentDepth()
	if cap != 3 {
		t.Errorf("expected depth-1 cap of 3, got %d", cap)
	}

	// Simulate the capping logic from runForeground.
	maxParallel := 8
	if maxParallel > cap {
		maxParallel = cap
	}
	if maxParallel != 3 {
		t.Errorf("expected maxParallel capped to 3, got %d", maxParallel)
	}
}

func TestRecursionGuardAllowsDepthZero(t *testing.T) {
	os.Unsetenv("ORCHESTRA_DEPTH")

	guard := recursion.NewGuard()
	cleanup, err := guard.Enter(t.TempDir())
	if err != nil {
		t.Fatalf("expected no error at depth 0, got: %v", err)
	}
	defer cleanup()

	cap := guard.MaxAgentsForCurrentDepth()
	if cap != 8 {
		t.Errorf("expected depth-0 cap of 8, got %d", cap)
	}
}

func containsAll(s string, substrs ...string) bool {
	for _, sub := range substrs {
		if !regexp.MustCompile(regexp.QuoteMeta(sub)).MatchString(s) {
			return false
		}
	}
	return true
}

func TestConductorRunHasSessionIDFlag(t *testing.T) {
	cmd := NewConductorRunCmd()
	f := cmd.Flags().Lookup("session-id")
	if f == nil {
		t.Fatal("conductor-run command missing --session-id flag")
	}
	if f.DefValue != "" {
		t.Errorf("--session-id default should be empty, got %q", f.DefValue)
	}
}
