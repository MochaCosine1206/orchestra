package onboard

import (
	"context"
	"fmt"
	"os/exec"
	"regexp"
	"strings"
	"sync"
	"time"
)

// Status represents the result of checking a dependency.
type Status string

const (
	StatusFound   Status = "found"
	StatusMissing Status = "missing"
	StatusTimeout Status = "timeout"
)

// Dependency describes an external tool that Orchestra may need.
type Dependency struct {
	Name         string
	Binary       string
	Required     bool
	MinVersion   string
	CheckCmd     []string // command + args to run (e.g. ["go", "version"])
	InstallHint  string
	Status       Status
	Version      string // parsed version, if available
	versionRegex *regexp.Regexp
}

// ScanResult holds the outcome of an environment scan.
type ScanResult struct {
	Dependencies    []Dependency
	AllRequired     bool
	MissingRequired int
	Warnings        []string
}

// CommandRunner abstracts exec.CommandContext for testability.
type CommandRunner interface {
	Run(ctx context.Context, name string, args ...string) ([]byte, error)
}

// ExecRunner is the default CommandRunner using os/exec.
type ExecRunner struct{}

// Run executes a command and returns combined output.
func (ExecRunner) Run(ctx context.Context, name string, args ...string) ([]byte, error) {
	return exec.CommandContext(ctx, name, args...).CombinedOutput()
}

var defaultDeps = []Dependency{
	{Name: "Go", Binary: "go", Required: true, CheckCmd: []string{"go", "version"}, InstallHint: "https://go.dev/dl/", versionRegex: regexp.MustCompile(`go(\d+\.\d+\.\d+)`)},
	{Name: "Git", Binary: "git", Required: true, CheckCmd: []string{"git", "version"}, InstallHint: "https://git-scm.com/downloads", versionRegex: regexp.MustCompile(`git version (\d+\.\d+\.\d+)`)},
	{Name: "Claude Code", Binary: "claude", Required: true, CheckCmd: []string{"claude", "--version"}, InstallHint: "https://claude.com/download"},
	{Name: "jq", Binary: "jq", Required: false, CheckCmd: []string{"jq", "--version"}, InstallHint: "brew install jq"},
	{Name: "curl", Binary: "curl", Required: false, CheckCmd: []string{"curl", "--version"}, InstallHint: "brew install curl", versionRegex: regexp.MustCompile(`curl (\d+\.\d+\.\d+)`)},
	{Name: "flock", Binary: "flock", Required: false, CheckCmd: []string{"flock", "--version"}, InstallHint: "brew install flock (Linux: util-linux)"},
	{Name: "Docker", Binary: "docker", Required: false, CheckCmd: []string{"docker", "--version"}, InstallHint: "https://docs.docker.com/get-docker/", versionRegex: regexp.MustCompile(`Docker version (\d+\.\d+\.\d+)`)},
	{Name: "SQLite3", Binary: "sqlite3", Required: false, CheckCmd: []string{"sqlite3", "--version"}, InstallHint: "brew install sqlite3", versionRegex: regexp.MustCompile(`(\d+\.\d+\.\d+)`)},
	{Name: "Node.js", Binary: "node", Required: false, CheckCmd: []string{"node", "--version"}, InstallHint: "https://nodejs.org/", versionRegex: regexp.MustCompile(`v(\d+\.\d+\.\d+)`)},
	{Name: "GitHub CLI", Binary: "gh", Required: false, CheckCmd: []string{"gh", "version"}, InstallHint: "brew install gh", versionRegex: regexp.MustCompile(`gh version (\d+\.\d+\.\d+)`)},
	{Name: "GoReleaser", Binary: "goreleaser", Required: false, CheckCmd: []string{"goreleaser", "--version"}, InstallHint: "brew install goreleaser"},
	{Name: "golangci-lint", Binary: "golangci-lint", Required: false, CheckCmd: []string{"golangci-lint", "--version"}, InstallHint: "brew install golangci-lint", versionRegex: regexp.MustCompile(`(\d+\.\d+\.\d+)`)},
}

// ScanEnvironment checks all known dependencies in parallel using the given runner.
func ScanEnvironment(ctx context.Context, runner CommandRunner) *ScanResult {
	deps := make([]Dependency, len(defaultDeps))
	copy(deps, defaultDeps)

	var wg sync.WaitGroup
	wg.Add(len(deps))

	for i := range deps {
		go func(idx int) {
			defer wg.Done()
			d := &deps[idx]
			checkCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
			defer cancel()

			out, err := runner.Run(checkCtx, d.CheckCmd[0], d.CheckCmd[1:]...)
			if err != nil {
				if checkCtx.Err() == context.DeadlineExceeded {
					d.Status = StatusTimeout
				} else {
					d.Status = StatusMissing
				}
				return
			}

			d.Status = StatusFound
			if d.versionRegex != nil {
				if m := d.versionRegex.FindSubmatch(out); len(m) > 1 {
					d.Version = string(m[1])
				}
			}
		}(i)
	}

	wg.Wait()

	result := &ScanResult{Dependencies: deps, AllRequired: true}
	for _, d := range deps {
		if d.Required && d.Status != StatusFound {
			result.AllRequired = false
			result.MissingRequired++
		}
		if !d.Required && d.Status != StatusFound {
			result.Warnings = append(result.Warnings, fmt.Sprintf("optional dependency %q not found: %s", d.Name, d.InstallHint))
		}
	}

	return result
}

// FormatTable returns a human-readable table of scan results with ANSI colors.
func FormatTable(result *ScanResult) string {
	var b strings.Builder
	b.WriteString(fmt.Sprintf("%-20s %-12s %-10s %s\n", "Dependency", "Status", "Version", ""))
	b.WriteString(strings.Repeat("─", 60) + "\n")

	for _, d := range result.Dependencies {
		var statusStr string
		switch d.Status {
		case StatusFound:
			statusStr = "\033[32m✓ found\033[0m"
		case StatusMissing:
			if d.Required {
				statusStr = "\033[31m✗ missing\033[0m"
			} else {
				statusStr = "\033[33m- missing\033[0m"
			}
		case StatusTimeout:
			if d.Required {
				statusStr = "\033[31m✗ timeout\033[0m"
			} else {
				statusStr = "\033[33m- timeout\033[0m"
			}
		}

		ver := d.Version
		if ver == "" {
			ver = "-"
		}

		req := ""
		if d.Required {
			req = "(required)"
		}

		b.WriteString(fmt.Sprintf("%-20s %-22s %-10s %s\n", d.Name, statusStr, ver, req))
	}

	return b.String()
}
