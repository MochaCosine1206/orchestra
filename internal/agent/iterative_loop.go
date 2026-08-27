package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/MochaCosine1206/orchestra/internal/db"
)

// IterativeLoopConfig defines convergence criteria for an iterative agent loop.
type IterativeLoopConfig struct {
	MaxRounds         int     // hard cap on iterations (default: 5)
	ConvergenceMetric string  // which metric to evaluate: "new_claim_rate", "score", "correction_count"
	Threshold         float64 // convergence threshold (e.g. 0.10 for 10% new-claim rate)
	MinRounds         int     // minimum rounds before convergence check (default: 2)
}

// IterativeRound represents one round of an iterative agent loop.
type IterativeRound struct {
	Round       int             `json:"round"`
	Output      json.RawMessage `json:"output"`
	MetricValue float64         `json:"metric_value"`
	Converged   bool            `json:"converged"`
	DurationMs  int64           `json:"duration_ms"`
}

// IterativeResult captures the full outcome of an iterative loop.
type IterativeResult struct {
	Rounds     []IterativeRound `json:"rounds"`
	Converged  bool             `json:"converged"`
	FinalRound int              `json:"final_round"`
	TotalMs    int64            `json:"total_ms"`
}

// DefaultLoopConfig returns role-specific loop configuration.
func DefaultLoopConfig(role Role) IterativeLoopConfig {
	switch role {
	case RoleDeepResearcher, RoleResearcher:
		return IterativeLoopConfig{
			MaxRounds:         3,
			ConvergenceMetric: "gaps_remaining",
			Threshold:         0,    // stop when 0 gaps remain from the plan
			MinRounds:         1,    // can stop after round 1 if plan fully executed
		}
	case RoleVisualReviewer:
		return IterativeLoopConfig{
			MaxRounds:         3,
			ConvergenceMetric: "correction_count",
			Threshold:         0, // stop when 0 corrections remain
			MinRounds:         1,
		}
	case RoleFunctionalTester:
		return IterativeLoopConfig{
			MaxRounds:         3,
			ConvergenceMetric: "failure_count",
			Threshold:         0, // stop when 0 failures remain
			MinRounds:         1,
		}
	default:
		return IterativeLoopConfig{
			MaxRounds: 1, // non-iterative roles: single pass
			MinRounds: 1,
		}
	}
}

// IsIterativeRole returns true if the role uses the iterative loop pattern.
func IsIterativeRole(role Role) bool {
	switch role {
	case RoleDeepResearcher, RoleResearcher, RoleVisualReviewer, RoleFunctionalTester:
		return true
	default:
		return false
	}
}

// --- Iterative Loop Execution ---

// RunIterativeLoop executes a multi-round agent loop with convergence checking.
// Each round calls claude -p with an updated prompt that includes prior round results.
// The loop stops when the convergence metric meets the threshold or MaxRounds is reached.
func RunIterativeLoop(ctx context.Context, d *db.DB, opts IterativeLoopOpts) (*IterativeResult, error) {
	config := opts.Config
	if config.MaxRounds == 0 {
		config.MaxRounds = 5
	}
	if config.MinRounds == 0 {
		config.MinRounds = 2
	}

	result := &IterativeResult{}
	startTime := time.Now()
	var accumulatedContext string

	for round := 1; round <= config.MaxRounds; round++ {
		roundStart := time.Now()

		// Build round-specific prompt
		prompt := buildRoundPrompt(opts, round, accumulatedContext)

		// Execute claude -p for this round — use same args as non-iterative spawner
		// so agents have full tool access (WebSearch, WebFetch, Read, Write, etc.)
		args := []string{
			"-p", prompt,
			"--output-format", "stream-json",
			"--verbose",
			"--model", opts.Model,
			"--permission-mode", "bypassPermissions",
		}
		if opts.MCPConfigPath != "" {
			args = append(args, "--mcp-config", opts.MCPConfigPath)
		}

		cmd := exec.CommandContext(ctx, "claude", args...)
		if opts.WorkDir != "" {
			cmd.Dir = opts.WorkDir
		}

		output, err := cmd.CombinedOutput()
		roundDuration := time.Since(roundStart).Milliseconds()

		if err != nil {
			// Log the failure but don't abort — try to extract partial results
			d.LogEvent(ctx, "iterative_round_failed", opts.AgentID, opts.TaskID,
				fmt.Sprintf(`{"round":%d,"error":"%s"}`, round, escapeForJSON(err.Error())))

			// If this is round 1 and it failed, return error
			if round == 1 {
				return nil, fmt.Errorf("iterative loop round 1 failed: %w", err)
			}
			// Otherwise, stop iterating and use what we have
			result.FinalRound = round - 1
			break
		}

		// Parse the output to extract the metric
		metricValue := extractMetric(output, config.ConvergenceMetric)

		roundResult := IterativeRound{
			Round:       round,
			Output:      json.RawMessage(output),
			MetricValue: metricValue,
			DurationMs:  roundDuration,
		}

		// Check convergence (only after MinRounds)
		converged := false
		if round >= config.MinRounds {
			converged = checkConvergence(metricValue, config)
		}
		roundResult.Converged = converged
		result.Rounds = append(result.Rounds, roundResult)

		// Log round results
		d.LogEvent(ctx, "iterative_round_complete", opts.AgentID, opts.TaskID,
			fmt.Sprintf(`{"round":%d,"metric":"%s","value":%.3f,"converged":%v,"duration_ms":%d}`,
				round, config.ConvergenceMetric, metricValue, converged, roundDuration))

		// Commit any file changes from this round so work isn't lost.
		// The iterative loop runs separate claude -p processes per round,
		// so agents don't get to their Delivery/commit step naturally.
		// Without this, validator sees 0 commits → marks task as failed
		// even when the agent produced valid research files.
		if opts.WorkDir != "" {
			statusOut, _ := exec.CommandContext(ctx, "git", "-C", opts.WorkDir, "status", "--porcelain").Output()
			if len(strings.TrimSpace(string(statusOut))) > 0 {
				exec.CommandContext(ctx, "git", "-C", opts.WorkDir, "add", "-A").Run()
				commitMsg := fmt.Sprintf("research round %d", round)
				if converged {
					commitMsg = fmt.Sprintf("research round %d (SATURATED)", round)
				}
				exec.CommandContext(ctx, "git", "-C", opts.WorkDir, "commit", "-m", commitMsg).Run()
			}
		}

		// Accumulate context for next round
		accumulatedContext = buildAccumulatedContext(accumulatedContext, output, round)

		if converged {
			result.Converged = true
			result.FinalRound = round
			break
		}

		result.FinalRound = round
	}

	result.TotalMs = time.Since(startTime).Milliseconds()

	// Log final summary
	d.LogEvent(ctx, "iterative_loop_complete", opts.AgentID, opts.TaskID,
		fmt.Sprintf(`{"rounds":%d,"converged":%v,"total_ms":%d,"metric":"%s","final_value":%.3f}`,
			result.FinalRound, result.Converged, result.TotalMs,
			config.ConvergenceMetric, lastMetricValue(result)))

	return result, nil
}

// IterativeLoopOpts configures a single iterative loop execution.
type IterativeLoopOpts struct {
	TaskID        string
	AgentID       string
	Role          Role
	Config        IterativeLoopConfig
	Model         string
	WorkDir       string
	JSONSchema    string
	MCPConfigPath string // path to MCP config for WebSearch/WebFetch access
	BasePrompt    string // the task spec / system prompt
	PlanPrompt    string // PLAN phase prompt (round 0)
	RoundPrompt   string // template for SEARCH/VERIFY rounds, with {{.Round}} and {{.PriorContext}}
	SynthPrompt   string // SYNTHESIZE phase prompt
}

// --- Prompt Building ---

func buildRoundPrompt(opts IterativeLoopOpts, round int, priorContext string) string {
	if round == 1 && opts.PlanPrompt != "" {
		// First round: planning phase
		return opts.BasePrompt + "\n\n---\n\n" + opts.PlanPrompt
	}

	// Middle rounds: search/verify with accumulated context
	prompt := opts.BasePrompt + "\n\n---\n\n"
	if opts.RoundPrompt != "" {
		rp := strings.ReplaceAll(opts.RoundPrompt, "{{.Round}}", fmt.Sprintf("%d", round))
		rp = strings.ReplaceAll(rp, "{{.PriorContext}}", priorContext)
		prompt += rp
	} else {
		prompt += fmt.Sprintf("Round %d. Prior findings:\n%s\n\nContinue searching for new information. Classify each finding as 'new', 'duplicate', or 'conflict'.", round, priorContext)
	}

	return prompt
}

func buildAccumulatedContext(prior string, roundOutput []byte, round int) string {
	// Extract text content from the JSON output
	var parsed struct {
		Result string `json:"result"`
	}
	if err := json.Unmarshal(roundOutput, &parsed); err == nil && parsed.Result != "" {
		if prior == "" {
			return fmt.Sprintf("=== Round %d ===\n%s", round, parsed.Result)
		}
		return prior + fmt.Sprintf("\n\n=== Round %d ===\n%s", round, parsed.Result)
	}

	// Fallback: use raw output (truncated)
	raw := string(roundOutput)
	if len(raw) > 5000 {
		raw = raw[:5000] + "... [truncated]"
	}
	if prior == "" {
		return fmt.Sprintf("=== Round %d ===\n%s", round, raw)
	}
	return prior + fmt.Sprintf("\n\n=== Round %d ===\n%s", round, raw)
}

// --- Metric Extraction ---

// extractMetric checks if the agent's output indicates convergence.
// We look for "SATURATED" — our explicit convergence keyword that the
// deep-researcher prompt tells the agent to output when all gaps are filled.
//
// We do NOT check for "COMPLETE" because Claude's result envelope always
// contains "terminal_reason":"completed" which is a false positive.
// V16 traced: every round 1 falsely converged because of this.
func extractMetric(output []byte, metric string) float64 {
	text := strings.ToUpper(string(output))
	if strings.Contains(text, "SATURATED") {
		return 0 // converged
	}
	return 1.0 // not converged
}

func checkConvergence(metricValue float64, config IterativeLoopConfig) bool {
	switch config.ConvergenceMetric {
	case "new_claim_rate":
		// Converged when new claim rate drops below threshold
		return metricValue < config.Threshold
	case "correction_count", "failure_count", "gaps_remaining":
		// Converged when count reaches threshold (usually 0)
		return metricValue <= config.Threshold
	case "score":
		// Converged when score exceeds threshold
		return metricValue >= config.Threshold
	default:
		return false
	}
}

func lastMetricValue(result *IterativeResult) float64 {
	if len(result.Rounds) == 0 {
		return 0
	}
	return result.Rounds[len(result.Rounds)-1].MetricValue
}

func escapeForJSON(s string) string {
	b, _ := json.Marshal(s)
	// Remove surrounding quotes
	if len(b) >= 2 {
		return string(b[1 : len(b)-1])
	}
	return s
}
