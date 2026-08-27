package agent

import (
	"fmt"
	"sync"
)

// LocalRunnerConfig configures the local LLM runner (e.g., pi with Ollama).
type LocalRunnerConfig struct {
	// Enabled turns on local model routing for eligible tasks.
	Enabled bool `json:"enabled"`

	// Command is the CLI binary name (e.g., "pi").
	Command string `json:"command"`

	// Model is the Ollama model name (e.g., "qwen3.5:35b-a3b-coding-nvfp4").
	Model string `json:"model"`

	// MaxConcurrent limits how many local runner agents can run simultaneously.
	// Ollama serializes requests, so more agents means longer queue times.
	// Recommended: 2 for 35B models on 64GB systems.
	MaxConcurrent int `json:"max_concurrent"`

	// MaxFiles is the maximum number of task files for a task to be eligible
	// for local routing. Tasks with more files need claude's broader reasoning.
	MaxFiles int `json:"max_files"`

	// sem is the concurrency semaphore, initialized lazily.
	sem  chan struct{}
	once sync.Once
}

// Acquire blocks until a local runner slot is available. Returns a release function.
// Must be called before spawning a local runner agent.
func (c *LocalRunnerConfig) Acquire() func() {
	c.once.Do(func() {
		max := c.MaxConcurrent
		if max <= 0 {
			max = 2
		}
		c.sem = make(chan struct{}, max)
	})
	c.sem <- struct{}{}
	return func() { <-c.sem }
}

// ShouldRouteLocal decides whether a task should use the local runner
// instead of claude. Only tasks explicitly assigned the pi-implementer role
// by the decomposer are routed locally. No runtime heuristics — the decomposer
// decides based on task nature (single functions, JSON generation, file edits,
// content generation, test writing). See notes/2026-04-03-pi-agent-profile.md.
func (c *LocalRunnerConfig) ShouldRouteLocal(role Role, fileCount int, acCount int) bool {
	if !c.Enabled || c.Command == "" {
		return false
	}

	// Only pi-implementer role goes to local — decomposer decides, not heuristics
	return role == RolePiImplementer
}

// BuildArgs creates the CLI arguments for a local runner invocation.
// Different from claude args — pi uses different flags.
func (c *LocalRunnerConfig) BuildArgs(prompt string) []string {
	args := []string{
		"-p", prompt,
		"--no-session",
	}
	if c.Model != "" {
		args = append(args, "--model", c.Model)
	}
	return args
}

// String returns a human-readable description of the routing decision.
func (c *LocalRunnerConfig) String() string {
	if !c.Enabled {
		return "local runner: disabled"
	}
	return fmt.Sprintf("local runner: %s (model=%s, max_concurrent=%d, max_files=%d)",
		c.Command, c.Model, c.MaxConcurrent, c.MaxFiles)
}
