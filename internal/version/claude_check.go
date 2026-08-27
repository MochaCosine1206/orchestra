package version

import (
	"fmt"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
)

// ClaudeVersionResult holds the result of a Claude CLI version check.
type ClaudeVersionResult struct {
	OK       bool   // whether the detected version meets the minimum
	Detected string // the version string detected from `claude --version`
	Err      error  // any error encountered during the check
}

// CheckClaudeVersion runs `claude --version`, parses the semver output,
// and checks whether it meets the given minVersion (major.minor.patch).
func CheckClaudeVersion(minVersion string) ClaudeVersionResult {
	out, err := exec.Command("claude", "--version").Output()
	if err != nil {
		if execErr, ok := err.(*exec.Error); ok {
			return ClaudeVersionResult{Err: fmt.Errorf("claude binary not found in PATH: %w", execErr)}
		}
		return ClaudeVersionResult{Err: fmt.Errorf("failed to run claude --version: %w", err)}
	}

	detected := extractSemver(strings.TrimSpace(string(out)))
	if detected == "" {
		return ClaudeVersionResult{Err: fmt.Errorf("could not parse semver from claude --version output: %q", string(out))}
	}

	minParts, err := parseSemver(minVersion)
	if err != nil {
		return ClaudeVersionResult{Detected: detected, Err: fmt.Errorf("invalid minVersion %q: %w", minVersion, err)}
	}

	detParts, err := parseSemver(detected)
	if err != nil {
		return ClaudeVersionResult{Detected: detected, Err: fmt.Errorf("invalid detected version %q: %w", detected, err)}
	}

	ok := compareSemver(detParts, minParts) >= 0
	return ClaudeVersionResult{OK: ok, Detected: detected}
}

var semverRe = regexp.MustCompile(`(\d+\.\d+\.\d+)`)

// extractSemver finds the first major.minor.patch pattern in s.
func extractSemver(s string) string {
	m := semverRe.FindString(s)
	return m
}

// parseSemver splits a "major.minor.patch" string into [3]int.
func parseSemver(v string) ([3]int, error) {
	v = strings.TrimPrefix(v, "v")
	parts := strings.SplitN(v, ".", 3)
	if len(parts) != 3 {
		return [3]int{}, fmt.Errorf("expected major.minor.patch, got %q", v)
	}
	var result [3]int
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil {
			return [3]int{}, fmt.Errorf("non-numeric segment %q: %w", p, err)
		}
		result[i] = n
	}
	return result, nil
}

// compareSemver returns -1, 0, or 1 comparing a to b.
func compareSemver(a, b [3]int) int {
	for i := 0; i < 3; i++ {
		if a[i] < b[i] {
			return -1
		}
		if a[i] > b[i] {
			return 1
		}
	}
	return 0
}
