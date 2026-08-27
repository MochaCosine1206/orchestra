package recursion

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

const depthEnvKey = "ORCHESTRA_DEPTH"

// CurrentDepth reads the ORCHESTRA_DEPTH env var and returns it as an int.
// Returns 0 if unset or unparseable.
func CurrentDepth() int {
	v := os.Getenv(depthEnvKey)
	if v == "" {
		return 0
	}
	d, err := strconv.Atoi(v)
	if err != nil {
		return 0
	}
	return d
}

// IncrementedEnv returns os.Environ() with ORCHESTRA_DEPTH set to current+1.
// If ORCHESTRA_DEPTH already exists, it is replaced; otherwise it is appended.
func IncrementedEnv() []string {
	next := fmt.Sprintf("%s=%d", depthEnvKey, CurrentDepth()+1)
	env := os.Environ()
	prefix := depthEnvKey + "="
	for i, e := range env {
		if strings.HasPrefix(e, prefix) {
			env[i] = next
			return env
		}
	}
	return append(env, next)
}

// MaxDepthExceeded returns true when the current depth exceeds 2.
func MaxDepthExceeded() bool {
	return CurrentDepth() > 2
}
