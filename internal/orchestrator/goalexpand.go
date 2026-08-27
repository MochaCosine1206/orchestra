package orchestrator

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

const (
	maxFileChars  = 10_000
	maxTotalChars = 50_000
)

// fileRefPattern matches @filepath references with a file extension.
// Requires dot + 1-4 char extension to avoid matching email addresses or @mentions.
var fileRefPattern = regexp.MustCompile(`@([\w./-]+\.\w{1,4})`)

// expandFileReferences scans goal text for @filepath references and replaces
// them with inline file contents. Paths are resolved relative to repoRoot.
// Uses default limits (10KB/file, 50KB total) suitable for decomposition goals.
// Format: @path/to/file.ext → contents inlined in XML-style tags.
// Errors reading files produce a warning inline (fail-open).
func expandFileReferences(goal string, repoRoot string) string {
	return expandFileReferencesWithLimits(goal, repoRoot, maxFileChars, maxTotalChars)
}

// expandFileReferencesUnlimited expands @file references with no size limits.
// Use for spec generation where the full file content is essential for fidelity.
func expandFileReferencesUnlimited(goal string, repoRoot string) string {
	return expandFileReferencesWithLimits(goal, repoRoot, 0, 0)
}

// ExpandFileReferencesUnlimited is the exported version for use by cmd package.
// Pre-expands @file references before passing to GenerateSpec, allowing callers
// to resolve paths relative to a different directory (e.g. CWD) than repoRoot.
func ExpandFileReferencesUnlimited(goal string, repoRoot string) string {
	return expandFileReferencesWithLimits(goal, repoRoot, 0, 0)
}

// expandFileReferencesWithLimits scans goal text for @filepath references and
// replaces them with inline file contents. Paths are resolved relative to repoRoot.
// Set maxPerFile or maxTotal to 0 for unlimited.
func expandFileReferencesWithLimits(goal string, repoRoot string, maxPerFile, maxTotal int) string {
	matches := fileRefPattern.FindAllStringSubmatchIndex(goal, -1)
	if len(matches) == 0 {
		return goal
	}

	totalExpanded := 0
	var result strings.Builder
	lastIdx := 0

	for _, match := range matches {
		fullStart, fullEnd := match[0], match[1]
		pathStart, pathEnd := match[2], match[3]
		relPath := goal[pathStart:pathEnd]

		// Skip email-like patterns: character before @ is alphanumeric
		if fullStart > 0 {
			prev := goal[fullStart-1]
			if (prev >= 'a' && prev <= 'z') || (prev >= 'A' && prev <= 'Z') || (prev >= '0' && prev <= '9') || prev == '.' || prev == '_' || prev == '-' {
				continue
			}
		}

		// Write text before this match
		result.WriteString(goal[lastIdx:fullStart])
		lastIdx = fullEnd

		// Check total expansion limit
		if maxTotal > 0 && totalExpanded >= maxTotal {
			result.WriteString(fmt.Sprintf("[WARNING: @%s skipped — expansion limit reached]", relPath))
			continue
		}

		absPath := relPath
		if !filepath.IsAbs(relPath) {
			absPath = filepath.Join(repoRoot, relPath)
		}
		content, err := os.ReadFile(absPath)
		if err != nil {
			result.WriteString(fmt.Sprintf("[WARNING: @%s not found]", relPath))
			continue
		}

		text := string(content)
		truncated := false
		if maxPerFile > 0 && len(text) > maxPerFile {
			text = text[:maxPerFile]
			truncated = true
		}

		totalExpanded += len(text)

		result.WriteString(fmt.Sprintf("<contents of %s>\n%s", relPath, text))
		if truncated {
			result.WriteString("\n[TRUNCATED]")
		}
		result.WriteString(fmt.Sprintf("\n</contents of %s>", relPath))
	}

	// Write remaining text after last match
	result.WriteString(goal[lastIdx:])

	return result.String()
}
