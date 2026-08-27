package agent

import (
	"fmt"
	"os"
)

const (
	// MaxContextTokens is the Claude context window size.
	MaxContextTokens = 200_000
	// BudgetThreshold is the fraction of context at which we warn.
	BudgetThreshold = 0.80 // 80% = 160K tokens
	// CharsPerToken is a conservative estimate for English text.
	CharsPerToken = 4
	// RepoOverheadTokens accounts for CLAUDE.md, .mcp.json, and repo indexing.
	RepoOverheadTokens = 15_000
	// ToolOverheadFactor adds buffer for tool call overhead during execution.
	ToolOverheadFactor = 1.30
)

// EstimateContextBudget estimates total token consumption for a task.
// Returns (estimated tokens, over budget bool, details string).
func EstimateContextBudget(spec string, agentSystemPrompt string) (int, bool, string) {
	specTokens := len(spec) / CharsPerToken
	promptTokens := len(agentSystemPrompt) / CharsPerToken
	baseTokens := specTokens + promptTokens + RepoOverheadTokens
	totalEstimate := int(float64(baseTokens) * ToolOverheadFactor)
	threshold := int(float64(MaxContextTokens) * BudgetThreshold)
	overBudget := totalEstimate > threshold
	details := fmt.Sprintf("spec=%d, prompt=%d, repo=%d, total=%d (threshold=%d)",
		specTokens, promptTokens, RepoOverheadTokens, totalEstimate, threshold)
	return totalEstimate, overBudget, details
}

// EstimateContextUsage estimates how much context an agent has consumed
// based on its JSONL log file size. Returns (estimated tokens, percentage of max).
// JSONL output is verbose (~10x the actual token content), so file bytes / 40 ≈ tokens.
func EstimateContextUsage(logFile string) (int, float64) {
	info, err := os.Stat(logFile)
	if err != nil {
		return 0, 0
	}
	estimatedTokens := int(info.Size()) / 40
	percentage := float64(estimatedTokens) / float64(MaxContextTokens) * 100
	return estimatedTokens, percentage
}
