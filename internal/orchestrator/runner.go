package orchestrator

import (
	"context"
	"encoding/json"
	"fmt"
	"math/rand"
	"os"
	"os/exec"
	"strings"
	"time"
)

// filterEnv returns a copy of env with the named variable removed.
func filterEnv(env []string, name string) []string {
	prefix := name + "="
	filtered := make([]string, 0, len(env))
	for _, e := range env {
		if !strings.HasPrefix(e, prefix) {
			filtered = append(filtered, e)
		}
	}
	return filtered
}

// ensureMaxOutputTokens sets CLAUDE_CODE_MAX_OUTPUT_TOKENS if not already
// present in the environment. Large prompts (planning docs, specs) and long
// agent runs routinely exceed the 32K default.
func ensureMaxOutputTokens(env []string) []string {
	const key = "CLAUDE_CODE_MAX_OUTPUT_TOKENS"
	for _, e := range env {
		if strings.HasPrefix(e, key+"=") {
			return env // already set by user, respect their value
		}
	}
	return append(env, key+"=128000")
}

// RunOpts configures a ClaudeRunner.Run() invocation.
type RunOpts struct {
	Prompt     string
	Model      string
	WorkDir    string
	Args       []string // additional CLI args
	JSONSchema string   // if set, use --output-format json --json-schema
}

// RunResult holds the output from a Claude CLI invocation.
type RunResult struct {
	Output    string // structured output (JSON) or plain text
	Reasoning string // LLM reasoning text (from "result" field in --json-schema mode)
}

// ClaudeRunner abstracts calls to `claude -p` for testability.
type ClaudeRunner interface {
	Run(ctx context.Context, opts RunOpts) (RunResult, error)
}

// ExecRunner implements ClaudeRunner using os/exec.
type ExecRunner struct {
	ClaudePath string          // default: "claude"
	Logger     func(string)    // optional logger for CLI invocations
}

// Run executes `claude -p <prompt>` and returns the output.
func (r *ExecRunner) Run(ctx context.Context, opts RunOpts) (RunResult, error) {
	claudeCmd := r.ClaudePath
	if claudeCmd == "" {
		claudeCmd = "claude"
	}

	var args []string
	if opts.JSONSchema != "" {
		args = []string{"-p", opts.Prompt, "--output-format", "json", "--json-schema", opts.JSONSchema}
	} else {
		args = []string{"-p", opts.Prompt, "--output-format", "text"}
	}
	if opts.Model != "" {
		args = append(args, "--model", opts.Model)
	}
	args = append(args, opts.Args...)

	cmd := exec.CommandContext(ctx, claudeCmd, args...)
	if opts.WorkDir != "" {
		cmd.Dir = opts.WorkDir
	}
	cmd.Env = filterEnv(os.Environ(), "CLAUDECODE")
	cmd.Env = ensureMaxOutputTokens(cmd.Env)

	// Log the CLI invocation for debugging
	if r.Logger != nil {
		schemaSnippet := ""
		if opts.JSONSchema != "" {
			schemaSnippet = fmt.Sprintf(" [schema:%d bytes]", len(opts.JSONSchema))
		}
		r.Logger(fmt.Sprintf("ExecRunner: %s -p [prompt:%d bytes] model=%s workdir=%s%s args=%v",
			claudeCmd, len(opts.Prompt), opts.Model, opts.WorkDir, schemaSnippet, args[len(args)-len(opts.Args):]))
	}

	out, err := cmd.CombinedOutput()
	if err != nil {
		return RunResult{Output: string(out)}, fmt.Errorf("claude command failed: %w: %s", err, string(out))
	}
	raw := strings.TrimSpace(string(out))

	// When using --json-schema, extract both structured output and reasoning
	if opts.JSONSchema != "" {
		extracted, reasoning, extractErr := ExtractStructuredOutput(raw)
		if extractErr != nil {
			if r.Logger != nil {
				r.Logger(fmt.Sprintf("ExecRunner: structured_output extraction failed: %v (raw response: %.500s)", extractErr, raw))
			}
			return RunResult{Output: raw}, fmt.Errorf("extracting structured output: %w", extractErr)
		}
		if r.Logger != nil {
			r.Logger(fmt.Sprintf("ExecRunner: structured_output=%d bytes, reasoning=%d bytes", len(extracted), len(reasoning)))
		}
		return RunResult{Output: extracted, Reasoning: reasoning}, nil
	}
	return RunResult{Output: raw}, nil
}

// ExtractStructuredOutput parses the JSON response from --json-schema mode
// and returns the structured_output field as a JSON string, plus the LLM's
// reasoning text from the "result" field.
// The Claude CLI returns: {"result":"...","structured_output":{...},"session_id":"..."}
// The structured_output field is a JSON object conforming to the provided schema.
// The result field contains the LLM's reasoning text that led to the structured output.
func ExtractStructuredOutput(jsonResponse string) (output string, reasoning string, err error) {
	var resp struct {
		StructuredOutput json.RawMessage `json:"structured_output"`
		Result           string          `json:"result"`
	}
	// Use Decoder instead of Unmarshal to tolerate trailing non-JSON text
	// (e.g. hook error messages appended to CombinedOutput by the CLI).
	dec := json.NewDecoder(strings.NewReader(jsonResponse))
	if err := dec.Decode(&resp); err != nil {
		return "", "", fmt.Errorf("parsing JSON response: %w", err)
	}
	// Prefer structured_output (present when --json-schema is used)
	if len(resp.StructuredOutput) > 0 && string(resp.StructuredOutput) != "null" {
		return string(resp.StructuredOutput), resp.Result, nil
	}
	// Fall back to result field (plain --output-format json without schema)
	if resp.Result != "" {
		return resp.Result, "", nil
	}
	return "", "", fmt.Errorf("neither structured_output nor result found in response")
}

// MockRunner records calls and returns preconfigured responses (for testing).
type MockRunner struct {
	Calls    []RunOpts
	Outputs  []string // returned in order; wraps around
	Errors   []error  // returned in order; wraps around
	callIdx  int
}

// Run records the call and returns the next output/error.
func (m *MockRunner) Run(_ context.Context, opts RunOpts) (RunResult, error) {
	m.Calls = append(m.Calls, opts)
	idx := m.callIdx
	m.callIdx++

	var output string
	var err error
	if len(m.Outputs) > 0 {
		output = m.Outputs[idx%len(m.Outputs)]
	}
	if len(m.Errors) > 0 {
		err = m.Errors[idx%len(m.Errors)]
	}
	return RunResult{Output: output}, err
}

// RetryRunner wraps a ClaudeRunner with rate-limit retry logic.
type RetryRunner struct {
	Inner      ClaudeRunner
	MaxRetries int // default: 3
}

// Run calls the inner runner with exponential backoff retry on rate limits.
func (r *RetryRunner) Run(ctx context.Context, opts RunOpts) (RunResult, error) {
	maxRetries := r.MaxRetries
	if maxRetries <= 0 {
		maxRetries = 3
	}

	var lastErr error
	for attempt := 0; attempt <= maxRetries; attempt++ {
		result, err := r.Inner.Run(ctx, opts)
		if err == nil {
			return result, nil
		}
		lastErr = err

		// Classify the failure to decide if retryable
		ft := classifyRunError(err.Error())
		if ft != "rate_limit" {
			return result, err // permanent failure
		}

		if attempt == maxRetries {
			break
		}

		// Exponential backoff with jitter
		backoff := time.Duration(1<<uint(attempt)) * time.Second
		jitter := time.Duration(rand.Intn(1000)) * time.Millisecond
		delay := backoff + jitter

		select {
		case <-ctx.Done():
			return RunResult{}, ctx.Err()
		case <-time.After(delay):
		}
	}
	return RunResult{}, fmt.Errorf("max retries exceeded: %w", lastErr)
}

// classifyRunError uses the agent.ClassifyFailure patterns inline.
func classifyRunError(errText string) string {
	// Reuse the compiled regexes from agent package via simple string matching
	lower := strings.ToLower(errText)
	if strings.Contains(lower, "429") ||
		strings.Contains(lower, "rate limit") ||
		strings.Contains(lower, "rate_limit") ||
		strings.Contains(lower, "too many requests") {
		return "rate_limit"
	}
	if strings.Contains(lower, "session limit") ||
		strings.Contains(lower, "usage limit") ||
		strings.Contains(lower, "quota exceed") {
		return "session_limit"
	}
	if strings.Contains(lower, "context limit") ||
		strings.Contains(lower, "context window") ||
		strings.Contains(lower, "token limit") {
		return "context_exhausted"
	}
	return "normal_failure"
}

// Ensure interface compliance at compile time.
var (
	_ ClaudeRunner = (*ExecRunner)(nil)
	_ ClaudeRunner = (*MockRunner)(nil)
	_ ClaudeRunner = (*RetryRunner)(nil)
)
