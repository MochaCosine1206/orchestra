package orchestrator

import (
	"fmt"
	"strings"
)

// BuildRefinementSpec takes the original task spec and injects error context
// appropriate to the triage outcome, producing a spec for the next attempt.
func BuildRefinementSpec(originalSpec string, errors []ErrorEnvelope, triage TriageOutcome) string {
	switch triage {
	case TriageHeal:
		return buildHealSpec(originalSpec, errors)
	case TriageRefine:
		return buildRefineSpec(originalSpec, errors)
	case TriageRedo:
		return buildRedoSpec(originalSpec)
	default:
		return originalSpec
	}
}

// buildHealSpec appends a concise fix section with specific errors to address.
func buildHealSpec(originalSpec string, errors []ErrorEnvelope) string {
	var b strings.Builder
	b.WriteString(originalSpec)
	b.WriteString("\n\n## Fix Required\nThe following errors must be fixed:\n\n")

	for i, e := range errors {
		if i >= 5 {
			b.WriteString(fmt.Sprintf("\n... and %d more errors\n", len(errors)-5))
			break
		}
		b.WriteString(fmt.Sprintf("- **%s**", e.ErrorType))
		if len(e.FilesAffected) > 0 {
			b.WriteString(fmt.Sprintf(" in `%s`", strings.Join(e.FilesAffected, "`, `")))
		}
		if len(e.LineNumbers) > 0 {
			b.WriteString(fmt.Sprintf(" (line %s)", formatLineNumbers(e.LineNumbers)))
		}
		b.WriteString(": ")
		b.WriteString(truncate(e.FixHint, 200))
		b.WriteString("\n")
	}

	return b.String()
}

// buildRefineSpec includes the full error output so the agent can understand
// what went wrong while keeping working parts intact.
func buildRefineSpec(originalSpec string, errors []ErrorEnvelope) string {
	var b strings.Builder
	b.WriteString(originalSpec)
	b.WriteString("\n\n## Previous Attempt Failed\n")
	b.WriteString("The previous attempt had the right approach but produced errors. ")
	b.WriteString("Fix the issues while keeping the working parts.\n\n")
	b.WriteString("### Error Details\n\n```\n")

	for i, e := range errors {
		if i >= 5 {
			b.WriteString(fmt.Sprintf("\n... and %d more errors (truncated)\n", len(errors)-5))
			break
		}
		b.WriteString(truncate(e.RawOutput, 200))
		b.WriteString("\n")
	}

	b.WriteString("```\n\n### Fix Hints\n\n")
	for i, e := range errors {
		if i >= 5 {
			break
		}
		b.WriteString(fmt.Sprintf("- %s\n", truncate(e.FixHint, 200)))
	}

	return b.String()
}

// buildRedoSpec signals that the previous attempt was fundamentally wrong.
func buildRedoSpec(originalSpec string) string {
	var b strings.Builder
	b.WriteString(originalSpec)
	b.WriteString("\n\n## IMPORTANT: Previous attempt was fundamentally wrong. Start fresh.\n")
	b.WriteString("Do not reuse any patterns from the previous attempt. ")
	b.WriteString("Re-read the requirements carefully and implement from scratch.\n")
	return b.String()
}

// formatLineNumbers converts a slice of ints to a comma-separated string.
func formatLineNumbers(nums []int) string {
	if len(nums) == 0 {
		return ""
	}
	parts := make([]string, len(nums))
	for i, n := range nums {
		parts[i] = fmt.Sprintf("%d", n)
	}
	return strings.Join(parts, ", ")
}

// truncate is defined in reconcile.go — reused here.
