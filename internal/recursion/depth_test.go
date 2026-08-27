package recursion

import (
	"os"
	"strings"
	"testing"
)

func TestCurrentDepth_Unset(t *testing.T) {
	os.Unsetenv("ORCHESTRA_DEPTH")
	if got := CurrentDepth(); got != 0 {
		t.Errorf("CurrentDepth() = %d, want 0", got)
	}
}

func TestCurrentDepth_Set(t *testing.T) {
	t.Setenv("ORCHESTRA_DEPTH", "2")
	if got := CurrentDepth(); got != 2 {
		t.Errorf("CurrentDepth() = %d, want 2", got)
	}
}

func TestCurrentDepth_Invalid(t *testing.T) {
	t.Setenv("ORCHESTRA_DEPTH", "abc")
	if got := CurrentDepth(); got != 0 {
		t.Errorf("CurrentDepth() = %d, want 0", got)
	}
}

func TestIncrementedEnv_Unset(t *testing.T) {
	os.Unsetenv("ORCHESTRA_DEPTH")
	env := IncrementedEnv()
	found := false
	for _, e := range env {
		if e == "ORCHESTRA_DEPTH=1" {
			found = true
			break
		}
	}
	if !found {
		t.Error("IncrementedEnv() missing ORCHESTRA_DEPTH=1")
	}
}

func TestIncrementedEnv_Replaces(t *testing.T) {
	t.Setenv("ORCHESTRA_DEPTH", "1")
	env := IncrementedEnv()
	count := 0
	for _, e := range env {
		if strings.HasPrefix(e, "ORCHESTRA_DEPTH=") {
			count++
			if e != "ORCHESTRA_DEPTH=2" {
				t.Errorf("got %s, want ORCHESTRA_DEPTH=2", e)
			}
		}
	}
	if count != 1 {
		t.Errorf("found %d ORCHESTRA_DEPTH entries, want 1", count)
	}
}

func TestMaxDepthExceeded_AtTwo(t *testing.T) {
	t.Setenv("ORCHESTRA_DEPTH", "2")
	if MaxDepthExceeded() {
		t.Error("MaxDepthExceeded() = true at depth 2, want false")
	}
}

func TestMaxDepthExceeded_AtThree(t *testing.T) {
	t.Setenv("ORCHESTRA_DEPTH", "3")
	if !MaxDepthExceeded() {
		t.Error("MaxDepthExceeded() = false at depth 3, want true")
	}
}
