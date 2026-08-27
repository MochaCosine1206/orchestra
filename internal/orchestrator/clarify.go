package orchestrator

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/MochaCosine1206/orchestra/internal/agent"
)

// ClarifyMode controls how ambiguity questions are resolved.
type ClarifyMode int

const (
	// ClarifyAuto uses default answers from extraction (headless/A/B testing).
	ClarifyAuto ClarifyMode = iota
	// ClarifyCLI prints questions and reads answers from stdin (deferred).
	ClarifyCLI
	// ClarifyTUI shows questions in a TUI panel (deferred).
	ClarifyTUI
)

// ClarifyQuestion represents a single ambiguity question extracted from a goal.
type ClarifyQuestion struct {
	ID       int      `json:"id"`
	Question string   `json:"question"`
	Default  string   `json:"default"`
	Context  string   `json:"context"`
	Options  []string `json:"options,omitempty"` // 2-4 selectable choices
	Answer   string   `json:"-"`                 // populated after resolution
}

// ClarifyResult captures the outcome of goal clarification.
type ClarifyResult struct {
	Questions     []ClarifyQuestion
	AugmentedGoal string
	Skipped       bool // true if no ambiguities found
	AutoAnswered  bool // true if defaults were used
	Duration      time.Duration
}

// ClarifyOpts configures a Clarify() invocation.
type ClarifyOpts struct {
	Goal   string
	Mode   ClarifyMode
	DryRun bool      // skip blackboard writes when true
	Stdin  io.Reader // input source for ClarifyCLI mode (defaults to os.Stdin)
}

const clarifyPromptTemplate = `Detect scope/intent ambiguities in this goal that could cause an agent to do the WRONG THING. Return [] if goal is clear enough to act on.

GOAL: %s

CODEBASE:
%s

RULES:
- ONLY ask when the goal is genuinely ambiguous about WHAT to do (e.g. "remove shaders" but there are 3 types)
- Return [] for goals that specify a clear action on a clear target (e.g. "fix typo in X", "add Y to Z")
- Do NOT ask about HOW to implement — only ask about WHAT the user wants
- Do NOT ask questions whose answers are obvious from the goal text
- 1-3 questions max, prefer fewer
- Each question MUST include 2-4 concrete options the user can pick from
- The first option should be the recommended default
- Defaults under 15 words, questions one sentence, options under 10 words each

OUTPUT (JSON array only, no other text):
[{"id":1,"question":"...","default":"...","context":"...","options":["option 1 (recommended)","option 2","option 3"]}]`

// Clarify analyzes a goal for ambiguities and returns clarification questions.
// In Auto mode, defaults are used as answers. Fail-open: errors are returned
// to the caller, which should proceed with the original goal.
func (c *Conductor) Clarify(ctx context.Context, opts ClarifyOpts) (*ClarifyResult, error) {
	if opts.Goal == "" {
		return nil, fmt.Errorf("goal is required for clarification")
	}

	// Expand @file references in goal text
	opts.Goal = expandFileReferences(opts.Goal, c.RepoRoot)

	// Local pre-filter: skip the API call for goals that are obviously specific
	if goalIsObviouslySpecific(opts.Goal) {
		c.log("Clarify skipped (goal is specific enough)")
		return &ClarifyResult{
			Skipped:       true,
			AugmentedGoal: opts.Goal,
		}, nil
	}

	start := time.Now()
	c.progress("clarify", "Analyzing goal for ambiguities")

	if c.Runner == nil {
		return nil, fmt.Errorf("ClaudeRunner is required for clarification")
	}

	// Generate file tree summary
	treeSummary := fileTreeSummary(c.RepoRoot, 2)

	prompt := fmt.Sprintf(clarifyPromptTemplate, opts.Goal, treeSummary)

	// Use Opus with read-only tools so it can verify targets exist in the codebase.
	runResult, err := c.Runner.Run(ctx, RunOpts{
		Prompt:  prompt,
		Model:   agent.ModelOpus,
		WorkDir: c.RepoRoot,
		Args:    []string{"--allowedTools", "Read", "Glob", "Grep"},
	})
	if err != nil {
		return nil, fmt.Errorf("clarify call failed: %w", err)
	}

	questions, err := parseClarifyOutput(runResult.Output)
	if err != nil {
		return nil, fmt.Errorf("parsing clarify output: %w", err)
	}

	result := &ClarifyResult{
		Questions: questions,
		Duration:  time.Since(start),
	}

	// No ambiguities found — goal is clear
	if len(questions) == 0 {
		result.Skipped = true
		result.AugmentedGoal = opts.Goal
		c.progress("clarify", "Goal is clear, no clarification needed")
		return result, nil
	}

	c.progress("clarify", fmt.Sprintf("Found %d ambiguities, resolving", len(questions)))

	// Resolve questions based on mode
	switch opts.Mode {
	case ClarifyAuto:
		for i := range questions {
			questions[i].Answer = questions[i].Default
		}
		result.AutoAnswered = true
	case ClarifyCLI:
		reader := opts.Stdin
		if reader == nil {
			reader = os.Stdin
		}
		resolveCLI(questions, reader, c)
	case ClarifyTUI:
		if c.ClarifyQuestionCh != nil && c.ClarifyAnswerCh != nil {
			c.progress("clarify", "Waiting for user answers in TUI")
			c.ClarifyQuestionCh <- questions
			answered := <-c.ClarifyAnswerCh
			for i := range questions {
				if i < len(answered) && answered[i].Answer != "" {
					questions[i].Answer = answered[i].Answer
				} else {
					questions[i].Answer = questions[i].Default
				}
			}
		} else {
			// Channels not wired — fall back to auto
			for i := range questions {
				questions[i].Answer = questions[i].Default
			}
			result.AutoAnswered = true
		}
	}

	result.AugmentedGoal = buildAugmentedGoal(opts.Goal, questions)

	// Log individual questions for auditability
	for _, q := range questions {
		c.log("  Clarify Q%d: %s → %s", q.ID, q.Question, q.Answer)
	}

	// Store clarification in blackboard for observability (skip in dry-run)
	if c.DB != nil && !opts.DryRun {
		clarifyJSON, _ := json.Marshal(questions)
		_ = c.setBlackboard(ctx, "conductor:clarifications", string(clarifyJSON))
	}

	// Log event (skip in dry-run)
	if c.DB != nil && !opts.DryRun {
		c.DB.LogEvent(ctx, "goal_clarified", "", "",
			fmt.Sprintf(`{"question_count":%d,"auto_answered":%v,"duration_ms":%d}`,
				len(questions), result.AutoAnswered, result.Duration.Milliseconds()))
	}

	return result, nil
}

// resolveCLI prints questions to stdout, reads answers from the provided reader.
// Empty answers use the default. If reading fails, remaining questions get defaults.
func resolveCLI(questions []ClarifyQuestion, reader io.Reader, c *Conductor) {
	scanner := bufio.NewScanner(reader)
	fmt.Fprintf(os.Stderr, "\n  Goal has %d ambiguities to clarify:\n\n", len(questions))
	for i := range questions {
		q := &questions[i]
		if q.Context != "" {
			fmt.Fprintf(os.Stderr, "  [%d/%d] %s\n        Context: %s\n        Default: %s\n        Answer: ", i+1, len(questions), q.Question, q.Context, q.Default)
		} else {
			fmt.Fprintf(os.Stderr, "  [%d/%d] %s\n        Default: %s\n        Answer: ", i+1, len(questions), q.Question, q.Default)
		}
		if scanner.Scan() {
			answer := strings.TrimSpace(scanner.Text())
			if answer == "" {
				q.Answer = q.Default
			} else {
				q.Answer = answer
			}
		} else {
			// EOF or error — use default for this and remaining questions
			q.Answer = q.Default
			for j := i + 1; j < len(questions); j++ {
				questions[j].Answer = questions[j].Default
			}
			break
		}
	}
	fmt.Fprintln(os.Stderr)
}

// parseClarifyOutput extracts clarification questions from Claude's JSON output.
func parseClarifyOutput(output string) ([]ClarifyQuestion, error) {
	output = strings.TrimSpace(output)

	jsonStr := extractJSONArray(output)
	if jsonStr == "" {
		return nil, fmt.Errorf("no JSON array found in clarify output")
	}

	var questions []ClarifyQuestion
	if err := json.Unmarshal([]byte(jsonStr), &questions); err != nil {
		return nil, fmt.Errorf("parsing clarify JSON: %w: output=%q", err, jsonStr)
	}

	// Cap at 3 questions (prompt asks for 1-3, enforce as guardrail)
	if len(questions) > 3 {
		questions = questions[:3]
	}

	return questions, nil
}

// buildAugmentedGoal appends a CLARIFICATIONS section to the original goal.
func buildAugmentedGoal(goal string, questions []ClarifyQuestion) string {
	if len(questions) == 0 {
		return goal
	}

	var b strings.Builder
	b.WriteString(goal)
	b.WriteString("\n\nCLARIFICATIONS:\n")
	for _, q := range questions {
		b.WriteString(fmt.Sprintf("- Q: %s\n  A: %s\n", q.Question, q.Answer))
	}
	return b.String()
}

// fileTreeSummary generates a truncated directory tree for the clarification prompt.
// maxDepth controls recursion depth. Hidden directories (starting with .) are skipped.
// Output is capped at 1000 characters to keep the prompt lean.
func fileTreeSummary(root string, maxDepth int) string {
	if root == "" {
		return "(no codebase root)"
	}

	var lines []string
	walkTree(root, root, 0, maxDepth, &lines)

	result := strings.Join(lines, "\n")
	if len(result) > 1000 {
		result = result[:1000] + "\n... (truncated)"
	}
	return result
}

func walkTree(base, dir string, depth, maxDepth int, lines *[]string) {
	if depth > maxDepth {
		return
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}

	// Sort entries: dirs first, then files
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].IsDir() != entries[j].IsDir() {
			return entries[i].IsDir()
		}
		return entries[i].Name() < entries[j].Name()
	})

	for _, entry := range entries {
		name := entry.Name()
		// Skip hidden directories and common noise
		if strings.HasPrefix(name, ".") {
			continue
		}
		if name == "node_modules" || name == "vendor" || name == "__pycache__" {
			continue
		}

		indent := strings.Repeat("  ", depth)
		rel, _ := filepath.Rel(base, filepath.Join(dir, name))
		if rel == "" {
			rel = name
		}

		if entry.IsDir() {
			*lines = append(*lines, fmt.Sprintf("%s%s/", indent, name))
			walkTree(base, filepath.Join(dir, name), depth+1, maxDepth, lines)
		} else {
			*lines = append(*lines, fmt.Sprintf("%s%s", indent, name))
		}
	}
}

// goalIsObviouslySpecific returns true if the goal contains signals that it's
// specific enough to skip clarification entirely (no API call needed).
// Signals: file paths, line numbers, flag names, function/type names with parens.
func goalIsObviouslySpecific(goal string) bool {
	// File path patterns: contains / with an extension, or common path prefixes
	if filePathPattern.MatchString(goal) {
		return true
	}
	// Line number references: "line 42", "L42", ":42"
	if lineRefPattern.MatchString(goal) {
		return true
	}
	// CLI flag references: --flag-name
	if flagPattern.MatchString(goal) {
		return true
	}
	return false
}

var (
	// Matches paths like "internal/foo/bar.go", "src/app.ts", "CLAUDE.md"
	filePathPattern = regexp.MustCompile(`\b[\w.-]+/[\w.-]+\.\w+\b|^\s*\w+\.\w{1,4}\b`)
	// Matches "line 42", "L42", ":42" (line number references)
	lineRefPattern = regexp.MustCompile(`(?i)\bline\s+\d+|[:\s]L?\d{2,}\b`)
	// Matches "--flag-name" or "--flag"
	flagPattern = regexp.MustCompile(`--[\w-]{2,}`)
)
