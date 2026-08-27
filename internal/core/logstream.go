package core

import (
	"bufio"
	"fmt"
	"html"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/MochaCosine1206/orchestra/internal/tui/logstream"
)

// LogStreamRenderer renders parsed JSONL log entries as HTML for the web dashboard.
type LogStreamRenderer struct {
	LogDir string // path to .orchestra/logs/ directory
}

// RenderLogStream reads a task's JSONL log file and returns styled HTML.
func (r *LogStreamRenderer) RenderLogStream(taskID string) (string, error) {
	path := filepath.Join(r.LogDir, fmt.Sprintf("task-%s.jsonl", taskID))
	entries, err := ParseLogFile(path)
	if err != nil {
		return "", err
	}
	return RenderLogEntries(entries), nil
}

// ParseLogFile reads a JSONL log file and returns parsed entries.
func ParseLogFile(path string) ([]logstream.LogEntry, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var entries []logstream.LogEntry
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 1024*1024), 1024*1024)
	for scanner.Scan() {
		line := scanner.Text()
		if strings.TrimSpace(line) == "" {
			continue
		}
		entries = append(entries, logstream.ParseLine(line))
	}
	return entries, scanner.Err()
}

// RenderLogEntries converts parsed log entries to styled HTML rows.
func RenderLogEntries(entries []logstream.LogEntry) string {
	if len(entries) == 0 {
		return `<div class="log-empty">No log entries</div>`
	}

	var sb strings.Builder
	sb.WriteString(`<div class="log-stream log-stream-detail">`)
	for _, e := range entries {
		renderLogEntry(&sb, e)
	}
	sb.WriteString(`</div>`)
	return sb.String()
}

func renderLogEntry(sb *strings.Builder, e logstream.LogEntry) {
	cssClass := logEntryCSSClass(e)
	kindIcon := logEntryIcon(e)

	sb.WriteString(fmt.Sprintf(`<div class="log-entry-row %s">`, cssClass))
	sb.WriteString(fmt.Sprintf(`<span class="log-kind-icon">%s</span>`, kindIcon))
	sb.WriteString(`<div class="log-entry-content">`)

	switch e.Kind {
	case logstream.KindInit:
		sb.WriteString(fmt.Sprintf(`<span class="log-session-id">Session: %s</span>`, html.EscapeString(e.SessionID)))

	case logstream.KindText:
		text := e.Text
		if len(text) > 300 {
			sb.WriteString(fmt.Sprintf(`<span class="log-text-preview">%s</span>`, html.EscapeString(text[:300])))
			sb.WriteString(`<button class="log-expand-btn" onclick="this.parentElement.querySelector('.log-text-full').style.display='block';this.style.display='none'">Show more</button>`)
			sb.WriteString(fmt.Sprintf(`<span class="log-text-full" style="display:none">%s</span>`, html.EscapeString(text)))
		} else {
			sb.WriteString(fmt.Sprintf(`<span class="log-text">%s</span>`, html.EscapeString(text)))
		}

	case logstream.KindToolUse:
		sb.WriteString(fmt.Sprintf(`<span class="log-tool-name">%s</span>`, html.EscapeString(e.ToolName)))
		if e.ToolInput != "" {
			sb.WriteString(fmt.Sprintf(`<span class="log-tool-input">%s</span>`, html.EscapeString(e.ToolInput)))
		}

	case logstream.KindToolResult:
		if e.IsError {
			sb.WriteString(fmt.Sprintf(`<span class="log-tool-error">%s</span>`, html.EscapeString(e.ToolResult)))
		} else {
			sb.WriteString(fmt.Sprintf(`<span class="log-tool-output">%s</span>`, html.EscapeString(e.ToolResult)))
		}
		if e.ToolResultFull != "" && logstream.IsDiffContent(e.ToolResultFull) {
			sb.WriteString(RenderDiffView(e.ToolResultFull))
		}

	case logstream.KindResult:
		status := "success"
		if !e.Success {
			status = "failure"
		}
		sb.WriteString(fmt.Sprintf(`<span class="log-result-status log-result-%s">%s</span>`, status, status))
		if e.ResultText != "" {
			sb.WriteString(fmt.Sprintf(`<pre class="log-result-text">%s</pre>`, html.EscapeString(e.ResultText)))
		}

	default:
		sb.WriteString(fmt.Sprintf(`<span class="log-raw">%s</span>`, html.EscapeString(e.Raw)))
	}

	sb.WriteString(`</div></div>`)
}

func logEntryCSSClass(e logstream.LogEntry) string {
	switch e.Kind {
	case logstream.KindToolUse:
		return "log-tool"
	case logstream.KindToolResult:
		if e.IsError {
			return "log-error"
		}
		return "log-tool-result-row"
	case logstream.KindResult:
		return "log-result"
	default:
		return ""
	}
}

func logEntryIcon(e logstream.LogEntry) string {
	switch e.Kind {
	case logstream.KindInit:
		return "⚡"
	case logstream.KindText:
		return "💬"
	case logstream.KindToolUse:
		return "🔧"
	case logstream.KindToolResult:
		if e.IsError {
			return "❌"
		}
		return "📄"
	case logstream.KindResult:
		if e.Success {
			return "✅"
		}
		return "❌"
	default:
		return "·"
	}
}

// RenderDiffView renders unified diff content as styled HTML with line numbers.
func RenderDiffView(diffText string) string {
	lines, _ := logstream.FormatDiffLines(diffText, 0)
	if len(lines) == 0 {
		return ""
	}

	var sb strings.Builder
	sb.WriteString(`<div class="diff-view diff-view-detail"><pre><code>`)

	oldLine, newLine := 0, 0
	for _, line := range lines {
		cssClass := DiffLineClass(line)
		oldNum, newNum := "", ""

		switch {
		case strings.HasPrefix(line, "@@"):
			oldLine, newLine = parseDiffHunkHeader(line)
			oldNum, newNum = "···", "···"
		case strings.HasPrefix(line, "---"), strings.HasPrefix(line, "+++"):
			// file header — no line numbers
		case strings.HasPrefix(line, "+"):
			newLine++
			newNum = fmt.Sprintf("%d", newLine)
		case strings.HasPrefix(line, "-"):
			oldLine++
			oldNum = fmt.Sprintf("%d", oldLine)
		default:
			oldLine++
			newLine++
			oldNum = fmt.Sprintf("%d", oldLine)
			newNum = fmt.Sprintf("%d", newLine)
		}

		sb.WriteString(fmt.Sprintf(
			`<span class="diff-line %s"><span class="diff-line-num">%s</span><span class="diff-line-num">%s</span><span class="diff-line-content">%s</span></span>`,
			cssClass, oldNum, newNum, html.EscapeString(line),
		))
		sb.WriteByte('\n')
	}

	sb.WriteString(`</code></pre></div>`)
	return sb.String()
}

// DiffLineClass returns the CSS class for a diff line based on its prefix.
func DiffLineClass(line string) string {
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

func parseDiffHunkHeader(line string) (oldStart, newStart int) {
	// Parse @@ -oldStart,count +newStart,count @@
	fmt.Sscanf(line, "@@ -%d", &oldStart)
	if idx := strings.Index(line, "+"); idx >= 0 {
		fmt.Sscanf(line[idx:], "+%d", &newStart)
	}
	if oldStart > 0 {
		oldStart--
	}
	if newStart > 0 {
		newStart--
	}
	return
}

// RenderTaskDetail generates the HTML for a task detail slide-out panel.
func RenderTaskDetail(t TaskDetailData) string {
	var sb strings.Builder

	// Header
	sb.WriteString(`<div class="detail-header">`)
	sb.WriteString(fmt.Sprintf(`<span class="status-dot status-%s"></span>`, html.EscapeString(t.Status)))
	sb.WriteString(fmt.Sprintf(`<h2 class="detail-title">%s</h2>`, html.EscapeString(t.Title)))
	sb.WriteString(fmt.Sprintf(`<span class="role-badge">%s</span>`, html.EscapeString(t.Role)))
	sb.WriteString(`<button class="detail-close" onclick="closeDetailPanel()" aria-label="Close">&times;</button>`)
	sb.WriteString(`</div>`)

	// Meta pills
	sb.WriteString(`<div class="detail-meta">`)
	sb.WriteString(fmt.Sprintf(`<span class="meta-pill">%s</span>`, html.EscapeString(t.ID)))
	sb.WriteString(fmt.Sprintf(`<span class="meta-pill">P%d</span>`, t.Priority))
	if t.Elapsed != "" {
		sb.WriteString(fmt.Sprintf(`<span class="meta-pill">%s</span>`, html.EscapeString(t.Elapsed)))
	}
	sb.WriteString(`</div>`)

	// Description
	if t.Description != "" {
		sb.WriteString(`<div class="detail-section"><h3 class="detail-section-title">Description</h3>`)
		sb.WriteString(fmt.Sprintf(`<p class="detail-description">%s</p>`, html.EscapeString(t.Description)))
		sb.WriteString(`</div>`)
	}

	// Acceptance criteria
	if len(t.AcceptanceCriteria) > 0 {
		sb.WriteString(`<div class="detail-section"><h3 class="detail-section-title">Acceptance Criteria</h3>`)
		sb.WriteString(`<ul class="ac-checklist">`)
		for _, ac := range t.AcceptanceCriteria {
			checked := ""
			checkClass := "ac-unchecked"
			if ac.Passed {
				checked = " checked disabled"
				checkClass = "ac-checked"
			}
			sb.WriteString(fmt.Sprintf(`<li class="ac-item %s"><input type="checkbox"%s><span>%s</span></li>`,
				checkClass, checked, html.EscapeString(ac.Text)))
		}
		sb.WriteString(`</ul></div>`)
	}

	// Dependencies
	if len(t.DependsOn) > 0 || len(t.BlockedBy) > 0 {
		sb.WriteString(`<div class="detail-section"><h3 class="detail-section-title">Dependencies</h3>`)
		sb.WriteString(`<div class="dep-pills">`)
		for _, dep := range t.DependsOn {
			sb.WriteString(fmt.Sprintf(`<a href="/task/%s" class="dep-pill dep-depends">%s</a>`, html.EscapeString(dep), html.EscapeString(dep)))
		}
		for _, dep := range t.BlockedBy {
			sb.WriteString(fmt.Sprintf(`<a href="/task/%s" class="dep-pill dep-blocked">%s</a>`, html.EscapeString(dep), html.EscapeString(dep)))
		}
		sb.WriteString(`</div></div>`)
	}

	// Assignment
	if t.AgentID != "" {
		sb.WriteString(`<div class="detail-section"><h3 class="detail-section-title">Assignment</h3>`)
		sb.WriteString(fmt.Sprintf(`<a href="/agent/%s" class="agent-link">%s</a>`, html.EscapeString(t.AgentID), html.EscapeString(t.AgentID)))
		if t.Branch != "" {
			sb.WriteString(fmt.Sprintf(`<div class="detail-mono">Branch: %s</div>`, html.EscapeString(t.Branch)))
		}
		if t.Worktree != "" {
			sb.WriteString(fmt.Sprintf(`<div class="detail-mono">Worktree: %s</div>`, html.EscapeString(t.Worktree)))
		}
		sb.WriteString(`</div>`)
	}

	// Result summary
	if t.Result != "" {
		sb.WriteString(`<div class="detail-section"><h3 class="detail-section-title">Result</h3>`)
		sb.WriteString(fmt.Sprintf(`<div class="result-text">%s</div>`, html.EscapeString(t.Result)))
		sb.WriteString(`</div>`)
	}

	// Log stream
	if t.LogHTML != "" {
		sb.WriteString(`<div class="detail-section"><h3 class="detail-section-title">Log Stream</h3>`)
		sb.WriteString(t.LogHTML)
		sb.WriteString(`</div>`)
	}

	// Failure info
	if t.Status == "failed" && t.FailureInfo != nil {
		sb.WriteString(`<div class="detail-section failure-info">`)
		sb.WriteString(`<h3 class="detail-section-title">Failure Info</h3>`)
		sb.WriteString(fmt.Sprintf(`<span class="failure-class-badge">%s</span>`, html.EscapeString(t.FailureInfo.Classification)))
		sb.WriteString(fmt.Sprintf(`<span class="failure-retry-count">Retries: %d</span>`, t.FailureInfo.RetryCount))
		if t.FailureInfo.ErrorMessage != "" {
			sb.WriteString(fmt.Sprintf(`<pre class="failure-error">%s</pre>`, html.EscapeString(t.FailureInfo.ErrorMessage)))
		}
		sb.WriteString(`</div>`)
	}

	return sb.String()
}

// RenderAgentDetail generates the HTML for an agent detail slide-out panel.
func RenderAgentDetail(a AgentDetailData) string {
	var sb strings.Builder

	// Header
	sb.WriteString(`<div class="detail-header">`)
	sb.WriteString(fmt.Sprintf(`<div class="noto-ring noto-ring-lg" data-role="%s" data-status="%s"></div>`,
		html.EscapeString(a.Role), html.EscapeString(a.Status)))
	sb.WriteString(fmt.Sprintf(`<h2 class="detail-title">%s</h2>`, html.EscapeString(a.Role)))
	sb.WriteString(fmt.Sprintf(`<span class="status-badge status-%s">%s</span>`, html.EscapeString(a.Status), html.EscapeString(a.Status)))
	sb.WriteString(`<button class="detail-close" onclick="closeDetailPanel()" aria-label="Close">&times;</button>`)
	sb.WriteString(`</div>`)

	// Context usage gauge
	sb.WriteString(`<div class="detail-section">`)
	sb.WriteString(`<h3 class="detail-section-title">Context Usage</h3>`)
	pct := 0
	if a.ContextMax > 0 {
		pct = int(float64(a.ContextUsed) / float64(a.ContextMax) * 100)
	}
	colorClass := contextColorClass(pct)
	sb.WriteString(fmt.Sprintf(`<div class="ctx-gauge"><div class="ctx-bar-lg"><div class="ctx-bar-fill %s" style="width:%d%%"></div></div>`,
		colorClass, pct))
	sb.WriteString(fmt.Sprintf(`<div class="ctx-label">%s / %s — %d%%</div>`,
		formatTokens(a.ContextUsed), formatTokens(a.ContextMax), pct))
	sb.WriteString(`</div></div>`)

	// Heartbeat
	sb.WriteString(`<div class="detail-section">`)
	sb.WriteString(`<h3 class="detail-section-title">Heartbeat</h3>`)
	staleness := time.Since(a.HeartbeatAt)
	hbClass := heartbeatClass(staleness)
	sb.WriteString(fmt.Sprintf(`<span class="heartbeat-indicator %s">%s ago</span>`,
		hbClass, formatDuration(staleness)))
	sb.WriteString(`</div>`)

	// Current task
	if a.CurrentTask != "" {
		sb.WriteString(`<div class="detail-section">`)
		sb.WriteString(`<h3 class="detail-section-title">Current Task</h3>`)
		sb.WriteString(fmt.Sprintf(`<a href="/task/%s" class="task-link">%s</a>`,
			html.EscapeString(a.CurrentTask), html.EscapeString(a.CurrentTask)))
		if a.CurrentTaskTitle != "" {
			sb.WriteString(fmt.Sprintf(`<span class="task-link-title">%s</span>`, html.EscapeString(a.CurrentTaskTitle)))
		}
		sb.WriteString(`</div>`)
	}

	// Log stream
	if a.LogHTML != "" {
		sb.WriteString(`<div class="detail-section"><h3 class="detail-section-title">Log Stream</h3>`)
		sb.WriteString(a.LogHTML)
		sb.WriteString(`</div>`)
	}

	// Tool call history
	if len(a.ToolCalls) > 0 {
		sb.WriteString(`<div class="detail-section"><h3 class="detail-section-title">Tool Call History</h3>`)
		sb.WriteString(`<div class="tool-call-list">`)
		for _, tc := range a.ToolCalls {
			statusClass := "tool-ok"
			if tc.IsError {
				statusClass = "tool-err"
			}
			sb.WriteString(fmt.Sprintf(`<div class="tool-call-item %s">`, statusClass))
			sb.WriteString(fmt.Sprintf(`<span class="tool-call-name">%s</span>`, html.EscapeString(tc.Name)))
			if tc.Input != "" {
				sb.WriteString(fmt.Sprintf(`<span class="tool-call-input">%s</span>`, html.EscapeString(truncate(tc.Input, 120))))
			}
			sb.WriteString(fmt.Sprintf(`<span class="tool-call-status %s">%s</span>`, statusClass, statusLabel(tc.IsError)))
			sb.WriteString(`</div>`)
		}
		sb.WriteString(`</div></div>`)
	}

	return sb.String()
}

// TaskDetailData holds all data needed to render a task detail panel.
type TaskDetailData struct {
	ID                 string
	Title              string
	Status             string
	Role               string
	Priority           int
	Elapsed            string
	Description        string
	AcceptanceCriteria []ACItem
	DependsOn          []string
	BlockedBy          []string
	AgentID            string
	Branch             string
	Worktree           string
	Result             string
	LogHTML            string
	FailureInfo        *FailureInfoData
}

// ACItem represents an acceptance criteria checklist item.
type ACItem struct {
	Text   string
	Passed bool
}

// FailureInfoData holds failure classification details.
type FailureInfoData struct {
	Classification string
	RetryCount     int
	ErrorMessage   string
}

// AgentDetailData holds all data needed to render an agent detail panel.
type AgentDetailData struct {
	ID               string
	Role             string
	Status           string
	ContextUsed      int
	ContextMax       int
	HeartbeatAt      time.Time
	CurrentTask      string
	CurrentTaskTitle string
	LogHTML          string
	ToolCalls        []ToolCallItem
}

// ToolCallItem represents a tool call in the agent's history.
type ToolCallItem struct {
	Name    string
	Input   string
	IsError bool
}

// ParseAcceptanceCriteria splits AC text into items and checks against result summary.
func ParseAcceptanceCriteria(acText, resultSummary string) []ACItem {
	if acText == "" {
		return nil
	}
	var items []ACItem
	resultLower := strings.ToLower(resultSummary)
	for _, line := range strings.Split(acText, "\n") {
		line = strings.TrimSpace(line)
		line = strings.TrimLeft(line, "- []x*•")
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		passed := resultSummary != "" && strings.Contains(resultLower, strings.ToLower(firstWords(line, 5)))
		items = append(items, ACItem{Text: line, Passed: passed})
	}
	return items
}

// ExtractToolCalls extracts tool use/result pairs from log entries.
func ExtractToolCalls(entries []logstream.LogEntry) []ToolCallItem {
	var calls []ToolCallItem
	for _, e := range entries {
		if e.Kind == logstream.KindToolUse {
			calls = append(calls, ToolCallItem{
				Name:  e.ToolName,
				Input: e.ToolInput,
			})
		}
		if e.Kind == logstream.KindToolResult && e.IsError && len(calls) > 0 {
			calls[len(calls)-1].IsError = true
		}
	}
	return calls
}

func contextColorClass(pct int) string {
	switch {
	case pct >= 90:
		return "ctx-red"
	case pct >= 75:
		return "ctx-orange"
	case pct >= 50:
		return "ctx-yellow"
	default:
		return "ctx-green"
	}
}

func heartbeatClass(d time.Duration) string {
	switch {
	case d > 2*time.Minute:
		return "hb-stale"
	case d > 30*time.Second:
		return "hb-warn"
	default:
		return "hb-ok"
	}
}

func formatTokens(n int) string {
	if n >= 1000 {
		return fmt.Sprintf("%d,%03d", n/1000, n%1000)
	}
	return fmt.Sprintf("%d", n)
}

func formatDuration(d time.Duration) string {
	if d < time.Second {
		return "<1s"
	}
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
	if d < time.Hour {
		return fmt.Sprintf("%dm%ds", int(d.Minutes()), int(d.Seconds())%60)
	}
	return fmt.Sprintf("%dh%dm", int(d.Hours()), int(d.Minutes())%60)
}

func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen-3] + "..."
}

func statusLabel(isError bool) string {
	if isError {
		return "error"
	}
	return "ok"
}

func firstWords(s string, n int) string {
	words := strings.Fields(s)
	if len(words) > n {
		words = words[:n]
	}
	return strings.Join(words, " ")
}
