package orchestrator

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// TokenUsage captures token counts for a single agent.
type TokenUsage struct {
	AgentID      string `json:"agent_id"`
	TaskID       string `json:"task_id"`
	Role         string `json:"role"`
	InputTokens  int64  `json:"input_tokens"`
	OutputTokens int64  `json:"output_tokens"`
	CacheRead    int64  `json:"cache_read_tokens"`
	CacheCreate  int64  `json:"cache_create_tokens"`
	TotalTokens  int64  `json:"total_tokens"`
}

// TokenSummary aggregates token usage across all agents.
type TokenSummary struct {
	ByRole  map[string]TokenUsage `json:"by_role"`
	Total   TokenUsage            `json:"total"`
	Agents  []TokenUsage          `json:"agents"`
}

// Tokens aggregates token usage from agent JSONL logs.
func (c *Conductor) Tokens(ctx context.Context) (*TokenSummary, error) {
	logsDir := filepath.Join(c.RepoRoot, ".orchestra", "logs")

	// Get all agents to map task IDs to roles
	agents, err := c.DB.ListAgents(ctx)
	if err != nil {
		return nil, fmt.Errorf("listing agents: %w", err)
	}

	agentMap := make(map[string]struct{ Role, AgentID string })
	for _, a := range agents {
		if a.CurrentTask.Valid {
			agentMap[a.CurrentTask.String] = struct{ Role, AgentID string }{a.Role, a.ID}
		}
	}

	// Scan JSONL log files for token usage events
	entries, err := os.ReadDir(logsDir)
	if err != nil {
		// No logs directory is not an error
		return &TokenSummary{ByRole: make(map[string]TokenUsage)}, nil
	}

	summary := &TokenSummary{
		ByRole: make(map[string]TokenUsage),
	}

	for _, entry := range entries {
		if !strings.HasSuffix(entry.Name(), ".jsonl") {
			continue
		}
		taskID := strings.TrimSuffix(entry.Name(), ".jsonl")
		info, ok := agentMap[taskID]
		if !ok {
			info = struct{ Role, AgentID string }{"unknown", ""}
		}

		usage := parseTokensFromLog(filepath.Join(logsDir, entry.Name()), taskID, info.AgentID, info.Role)
		summary.Agents = append(summary.Agents, usage)

		// Accumulate by role
		byRole := summary.ByRole[info.Role]
		byRole.Role = info.Role
		byRole.InputTokens += usage.InputTokens
		byRole.OutputTokens += usage.OutputTokens
		byRole.CacheRead += usage.CacheRead
		byRole.CacheCreate += usage.CacheCreate
		byRole.TotalTokens += usage.TotalTokens
		summary.ByRole[info.Role] = byRole

		// Accumulate total
		summary.Total.InputTokens += usage.InputTokens
		summary.Total.OutputTokens += usage.OutputTokens
		summary.Total.CacheRead += usage.CacheRead
		summary.Total.CacheCreate += usage.CacheCreate
		summary.Total.TotalTokens += usage.TotalTokens
	}

	return summary, nil
}

// FormatTokenSummary returns a human-readable table.
func FormatTokenSummary(s *TokenSummary) string {
	var b strings.Builder

	b.WriteString("Token Usage Summary\n")
	b.WriteString("═══════════════════════════════════════════════════════════════\n")
	b.WriteString(fmt.Sprintf("%-14s %10s %10s %10s %10s\n", "ROLE", "INPUT", "OUTPUT", "CACHE_R", "TOTAL"))
	b.WriteString("───────────────────────────────────────────────────────────────\n")

	for role, usage := range s.ByRole {
		b.WriteString(fmt.Sprintf("%-14s %10d %10d %10d %10d\n",
			role, usage.InputTokens, usage.OutputTokens, usage.CacheRead, usage.TotalTokens))
	}

	b.WriteString("───────────────────────────────────────────────────────────────\n")
	b.WriteString(fmt.Sprintf("%-14s %10d %10d %10d %10d\n",
		"TOTAL", s.Total.InputTokens, s.Total.OutputTokens, s.Total.CacheRead, s.Total.TotalTokens))

	if len(s.Agents) > 0 {
		b.WriteString("\nPer-Agent Breakdown\n")
		b.WriteString("───────────────────────────────────────────────────────────────\n")
		for _, a := range s.Agents {
			b.WriteString(fmt.Sprintf("  %s (%s): in=%d out=%d total=%d\n",
				a.TaskID, a.Role, a.InputTokens, a.OutputTokens, a.TotalTokens))
		}
	}

	return b.String()
}

// parseTokensFromLog reads a JSONL file and sums token usage events.
func parseTokensFromLog(logFile, taskID, agentID, role string) TokenUsage {
	usage := TokenUsage{
		AgentID: agentID,
		TaskID:  taskID,
		Role:    role,
	}

	data, err := os.ReadFile(logFile)
	if err != nil {
		return usage
	}

	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		var event map[string]interface{}
		if err := json.Unmarshal([]byte(line), &event); err != nil {
			continue
		}

		// Look for usage data in stream-json events
		usageData, ok := event["usage"].(map[string]interface{})
		if !ok {
			continue
		}

		usage.InputTokens += int64(jsonFloat(usageData, "input_tokens"))
		usage.OutputTokens += int64(jsonFloat(usageData, "output_tokens"))
		usage.CacheRead += int64(jsonFloat(usageData, "cache_read_input_tokens"))
		usage.CacheCreate += int64(jsonFloat(usageData, "cache_creation_input_tokens"))
	}

	usage.TotalTokens = usage.InputTokens + usage.OutputTokens
	return usage
}

func jsonFloat(m map[string]interface{}, key string) float64 {
	v, ok := m[key]
	if !ok {
		return 0
	}
	f, ok := v.(float64)
	if !ok {
		return 0
	}
	return f
}
