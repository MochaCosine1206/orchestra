package logstream

import (
	"encoding/json"
	"fmt"
	"hash/fnv"
	"strings"
)

// EntryKind classifies a parsed JSONL log entry.
type EntryKind int

const (
	KindInit       EntryKind = iota // system init
	KindText                        // assistant text
	KindToolUse                     // assistant tool_use
	KindToolResult                  // user tool_result
	KindResult                      // final result
	KindUnknown                     // unparseable
)

// LogEntry is a parsed representation of one stream-json JSONL line.
type LogEntry struct {
	Kind EntryKind
	Raw  string // original line for fallback

	// Init
	SessionID string

	// Text
	Text string

	// Tool use
	ToolName      string
	ToolID        string
	ToolInput     string // truncated summary
	ToolInputFull string // full JSON for Edit calls (old_string/new_string available)

	// Tool result
	ToolResult     string // truncated summary
	ToolResultFull string // full content for expandable results (e.g. diffs)
	IsError        bool

	// Result
	Success    bool
	ResultText string
}

// String returns a human-readable label for the entry kind,
// suitable for use in HTML rendering and CSS class names.
func (k EntryKind) String() string {
	switch k {
	case KindInit:
		return "init"
	case KindText:
		return "text"
	case KindToolUse:
		return "tool_use"
	case KindToolResult:
		return "tool_result"
	case KindResult:
		return "result"
	default:
		return "unknown"
	}
}

// CSSClass returns a CSS class name for the entry kind, suitable for styling log rows.
func (k EntryKind) CSSClass() string {
	switch k {
	case KindToolUse:
		return "log-tool-call"
	case KindToolResult:
		return "log-tool-result"
	case KindResult:
		return "log-result"
	default:
		return ""
	}
}

// ToolCallFingerprint is the result of fingerprinting a JSONL log line.
type ToolCallFingerprint struct {
	ToolName   string
	InputHash  uint64
	IsToolCall bool
}

// ParseToolCallFingerprint parses a raw JSONL line and returns a fingerprint.
// For tool_use entries, it computes an FNV-1a hash of ToolName + first 200 chars of ToolInput.
// For non-tool-use entries, IsToolCall is false.
func ParseToolCallFingerprint(raw string) ToolCallFingerprint {
	entry := ParseLine(raw)
	if entry.Kind != KindToolUse {
		return ToolCallFingerprint{}
	}

	input := entry.ToolInput
	if len(input) > maxFieldLen {
		input = input[:maxFieldLen]
	}

	h := fnv.New64a()
	h.Write([]byte(entry.ToolName))
	h.Write([]byte(input))

	return ToolCallFingerprint{
		ToolName:   entry.ToolName,
		InputHash:  h.Sum64(),
		IsToolCall: true,
	}
}

const maxFieldLen = 200

// ParseLine parses a single JSONL line from Claude's stream-json output.
func ParseLine(raw string) LogEntry {
	entry := LogEntry{Raw: raw, Kind: KindUnknown}

	var m map[string]any
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		return entry
	}

	typ, _ := m["type"].(string)

	switch typ {
	case "system":
		entry.Kind = KindInit
		entry.SessionID, _ = m["session_id"].(string)

	case "assistant":
		msg, ok := m["message"].(map[string]any)
		if !ok {
			return entry
		}
		// Real format: message.content is an array of content blocks
		block := firstContentBlock(msg)
		if block == nil {
			return entry
		}
		blockType, _ := block["type"].(string)
		switch blockType {
		case "text":
			entry.Kind = KindText
			entry.Text, _ = block["text"].(string)
		case "tool_use":
			entry.Kind = KindToolUse
			entry.ToolName, _ = block["name"].(string)
			entry.ToolID, _ = block["id"].(string)
			entry.ToolInput = summarizeInput(block["input"])
			// For Edit calls, store full input JSON so old_string/new_string are available
			if entry.ToolName == "Edit" {
				if inputMap, ok := block["input"].(map[string]any); ok {
					if b, err := json.Marshal(inputMap); err == nil {
						entry.ToolInputFull = string(b)
					}
				}
			}
		}

	case "user":
		msg, ok := m["message"].(map[string]any)
		if !ok {
			return entry
		}
		// Real format: message.content is an array of content blocks
		block := firstContentBlock(msg)
		if block == nil {
			return entry
		}
		blockType, _ := block["type"].(string)
		if blockType == "tool_result" {
			entry.Kind = KindToolResult
			entry.ToolID, _ = block["tool_use_id"].(string)
			entry.ToolResult = summarizeContent(block["content"])
			entry.IsError = boolField(block, "is_error")
			// Store full content for diff detection/rendering
			full := extractFullContent(block["content"])
			if full != "" && IsDiffContent(full) {
				entry.ToolResultFull = full
			}
		}

	case "result":
		entry.Kind = KindResult
		subtype, _ := m["subtype"].(string)
		entry.Success = subtype == "success"
		entry.ResultText = truncate(strField(m, "result"), 2000) // keep more for multi-line rendering
	}

	return entry
}

// firstContentBlock extracts the first content block from message.content[].
func firstContentBlock(msg map[string]any) map[string]any {
	content, ok := msg["content"].([]any)
	if !ok || len(content) == 0 {
		return nil
	}
	block, _ := content[0].(map[string]any)
	return block
}

// summarizeInput extracts a short summary from tool input.
func summarizeInput(v any) string {
	m, ok := v.(map[string]any)
	if !ok {
		return truncate(fmt.Sprintf("%v", v), maxFieldLen)
	}

	// Common tool patterns: extract the most useful field
	if fp, ok := m["file_path"].(string); ok {
		return truncate(fp, maxFieldLen)
	}
	if cmd, ok := m["command"].(string); ok {
		return truncate(cmd, maxFieldLen)
	}
	if pat, ok := m["pattern"].(string); ok {
		if path, ok := m["path"].(string); ok {
			return truncate(fmt.Sprintf("%s in %s", pat, path), maxFieldLen)
		}
		return truncate(pat, maxFieldLen)
	}
	if q, ok := m["query"].(string); ok {
		return truncate(q, maxFieldLen)
	}
	if url, ok := m["url"].(string); ok {
		return truncate(url, maxFieldLen)
	}

	// Fallback: JSON-encode truncated
	b, err := json.Marshal(m)
	if err != nil {
		return truncate(fmt.Sprintf("%v", v), maxFieldLen)
	}
	return truncate(string(b), maxFieldLen)
}

// summarizeContent extracts a short summary from tool result content.
func summarizeContent(v any) string {
	switch c := v.(type) {
	case string:
		return truncate(c, maxFieldLen)
	case []any:
		// Content blocks array — extract first text snippet + total size
		total := 0
		firstSnippet := ""
		for _, block := range c {
			if bm, ok := block.(map[string]any); ok {
				if text, ok := bm["text"].(string); ok {
					total += len(text)
					if firstSnippet == "" && len(text) > 0 {
						// Extract first meaningful line as a preview
						firstSnippet = firstLine(text)
					}
				}
			}
		}
		if total > 0 {
			size := formatBytes(total)
			if firstSnippet != "" {
				return fmt.Sprintf("%s — %s", size, truncate(firstSnippet, maxFieldLen-len(size)-3))
			}
			return size
		}
		return fmt.Sprintf("(%d blocks)", len(c))
	default:
		b, err := json.Marshal(v)
		if err != nil {
			return fmt.Sprintf("%v", v)
		}
		return truncate(string(b), maxFieldLen)
	}
}

// firstLine extracts the first non-empty line from a string.
func firstLine(s string) string {
	for _, line := range strings.Split(s, "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			return line
		}
	}
	return ""
}

// formatBytes returns a human-readable byte size.
func formatBytes(b int) string {
	if b < 1024 {
		return fmt.Sprintf("%d B", b)
	}
	kb := float64(b) / 1024
	if kb < 1024 {
		return fmt.Sprintf("%.1f KB", kb)
	}
	mb := kb / 1024
	return fmt.Sprintf("%.1f MB", mb)
}

func strField(m map[string]any, key string) string {
	v, _ := m[key].(string)
	return v
}

func boolField(m map[string]any, key string) bool {
	v, _ := m[key].(bool)
	return v
}

func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen-3] + "..."
}

// extractFullContent extracts the full text content from a tool result content field.
func extractFullContent(v any) string {
	switch c := v.(type) {
	case string:
		return c
	case []any:
		var sb strings.Builder
		for _, block := range c {
			if bm, ok := block.(map[string]any); ok {
				if text, ok := bm["text"].(string); ok {
					sb.WriteString(text)
				}
			}
		}
		return sb.String()
	default:
		return ""
	}
}

// IsDiffContent detects whether text contains unified diff markers.
// Returns true if lines starting with +, -, or @@ are found.
func IsDiffContent(text string) bool {
	for _, line := range strings.Split(text, "\n") {
		if len(line) == 0 {
			continue
		}
		switch {
		case strings.HasPrefix(line, "+"), strings.HasPrefix(line, "-"), strings.HasPrefix(line, "@@"):
			return true
		}
	}
	return false
}

// FormatDiffLines splits diff text into lines and returns the first maxLines
// lines plus the total line count.
func FormatDiffLines(text string, maxLines int) (displayed []string, totalLines int) {
	lines := strings.Split(text, "\n")
	// Remove trailing empty line from Split
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	totalLines = len(lines)
	if maxLines > 0 && totalLines > maxLines {
		displayed = lines[:maxLines]
	} else {
		displayed = lines
	}
	return
}

// DiffLineType classifies a diff line for CSS styling in the web dashboard.
func DiffLineType(line string) string {
	switch {
	case strings.HasPrefix(line, "+++"), strings.HasPrefix(line, "---"):
		return "diff-header"
	case strings.HasPrefix(line, "@@"):
		return "diff-hunk"
	case strings.HasPrefix(line, "+"):
		return "diff-add"
	case strings.HasPrefix(line, "-"):
		return "diff-del"
	default:
		return "diff-ctx"
	}
}

// WebCSSClass returns the CSS class for rendering a log entry in the web dashboard.
// Unlike CSSClass() (for TUI), this returns classes matching the dashboard design system.
func (e LogEntry) WebCSSClass() string {
	switch e.Kind {
	case KindToolUse:
		return "log-tool"
	case KindToolResult:
		if e.IsError {
			return "log-error"
		}
		return "log-tool-result-row"
	case KindResult:
		return "log-result"
	case KindInit:
		return "log-init"
	case KindText:
		return "log-text-entry"
	default:
		return ""
	}
}

// KindLabel returns a human-readable label for the entry kind in the web dashboard.
func (e LogEntry) KindLabel() string {
	switch e.Kind {
	case KindInit:
		return "init"
	case KindText:
		return "text"
	case KindToolUse:
		return e.ToolName
	case KindToolResult:
		if e.IsError {
			return "error"
		}
		return "result"
	case KindResult:
		if e.Success {
			return "success"
		}
		return "failure"
	default:
		return "unknown"
	}
}
