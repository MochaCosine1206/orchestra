package panels

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/MochaCosine1206/orchestra/internal/db"
	"github.com/MochaCosine1206/orchestra/internal/tui/logstream"
)

// LogMode determines what the log panel displays.
type LogMode int

const (
	// LogModeAgent shows agent log entries for a selected task (default).
	LogModeAgent LogMode = iota
	// LogModeConductor shows the conductor timeline.
	LogModeConductor
)

// Conductor phase color mapping.
var conductorPhaseColors = map[string]lipgloss.Color{
	"decompose":  lipgloss.Color("#06B6D4"), // cyan
	"clarify":    lipgloss.Color("#06B6D4"), // cyan
	"spawn":      lipgloss.Color("#3B82F6"), // blue
	"batch":      lipgloss.Color("#3B82F6"), // blue
	"merge":      lipgloss.Color("#22C55E"), // green
	"review":     lipgloss.Color("#F97316"), // orange
	"fail":       lipgloss.Color("#EF4444"), // red
	"cascade":    lipgloss.Color("#EF4444"), // red
	"refinement": lipgloss.Color("#A855F7"), // purple
	"defense":    lipgloss.Color("#EAB308"), // yellow
	"reconcile":  lipgloss.Color("#22C55E"), // green
	"goal":       lipgloss.Color("#06B6D4"), // cyan
	"auto":       lipgloss.Color("#3B82F6"), // blue
	"monitor":    lipgloss.Color("#6B7280"), // dim
}

// Compile-time interface check.
var _ Panel = (*LogPanel)(nil)

// Styles for log entry rendering.
var (
	logTextPrefix    = lipgloss.NewStyle().Foreground(lipgloss.Color("#06B6D4")) // cyan >
	logToolDim       = lipgloss.NewStyle().Foreground(lipgloss.Color("#6B7280"))
	logResultOk      = lipgloss.NewStyle().Foreground(lipgloss.Color("#22C55E")).Bold(true)
	logResultFail    = lipgloss.NewStyle().Foreground(lipgloss.Color("#EF4444")).Bold(true)
	logErrorText     = lipgloss.NewStyle().Foreground(lipgloss.Color("#EF4444"))
	logInitDim       = lipgloss.NewStyle().Foreground(lipgloss.Color("#6B7280"))
	logToolInput     = lipgloss.NewStyle().Foreground(lipgloss.Color("#9CA3AF")) // lighter dim for inputs
	logResultDim     = lipgloss.NewStyle().Foreground(lipgloss.Color("#6B7280"))
	logExpandedInput = lipgloss.NewStyle().Foreground(lipgloss.Color("#6B7280")).Faint(true)
	logSelectedBg    = lipgloss.NewStyle().Background(lipgloss.Color("#374151"))
	// Diff syntax highlighting styles
	diffAdded       = lipgloss.NewStyle().Foreground(lipgloss.Color("#22C55E"))
	diffRemoved     = lipgloss.NewStyle().Foreground(lipgloss.Color("#EF4444"))
	diffChunkHeader = lipgloss.NewStyle().Foreground(lipgloss.Color("#06B6D4"))
	diffContext     = lipgloss.NewStyle().Foreground(lipgloss.Color("#6B7280"))
)

// Tool color categories — semantic grouping by operation type.
var toolColorMap = map[string]lipgloss.Color{
	// File reading / search (blue — information gathering)
	"Read":         lipgloss.Color("#3B82F6"),
	"Glob":         lipgloss.Color("#3B82F6"),
	"Grep":         lipgloss.Color("#3B82F6"),
	"NotebookRead": lipgloss.Color("#3B82F6"),
	// File writing / editing (green — constructive changes)
	"Edit":         lipgloss.Color("#22C55E"),
	"Write":        lipgloss.Color("#22C55E"),
	"NotebookEdit": lipgloss.Color("#22C55E"),
	// Execution (orange — active operations)
	"Bash":  lipgloss.Color("#F97316"),
	"Task":  lipgloss.Color("#F97316"),
	"Skill": lipgloss.Color("#F97316"),
	// Web / external (purple — external data)
	"WebSearch": lipgloss.Color("#A855F7"),
	"WebFetch":  lipgloss.Color("#A855F7"),
	// Planning / meta (cyan — thinking/planning)
	"TodoWrite":       lipgloss.Color("#06B6D4"),
	"TodoRead":        lipgloss.Color("#06B6D4"),
	"EnterPlanMode":   lipgloss.Color("#06B6D4"),
	"ExitPlanMode":    lipgloss.Color("#06B6D4"),
	"AskUserQuestion": lipgloss.Color("#06B6D4"),
}

// Default tool color for unknown tools.
var defaultToolColor = lipgloss.Color("#EAB308") // yellow

// toolStyle returns a bold style with the appropriate color for the tool name.
func toolStyle(name string) lipgloss.Style {
	color, ok := toolColorMap[name]
	if !ok {
		color = defaultToolColor
	}
	return lipgloss.NewStyle().Foreground(color).Bold(true)
}

// toolDimStyle returns a dim style tinted with the tool's color family.
func toolDimStyle(name string) lipgloss.Style {
	color, ok := toolColorMap[name]
	if !ok {
		color = defaultToolColor
	}
	// Use the tool color but make it dimmer by keeping the hue
	return lipgloss.NewStyle().Foreground(color).Faint(true)
}

// LogPanel displays streaming log output for the selected task/agent.
type LogPanel struct {
	entries          []logstream.LogEntry
	renderedLines    []string // pre-rendered display lines
	entryLineOffsets []int    // entryLineOffsets[i] = first line index for entries[i]
	expanded         map[int]bool
	cursor           int // selected entry index
	taskID           string
	taskTitle        string
	width            int
	height           int
	focused          bool
	autoScroll       bool
	scrollPos        int // first visible line index

	// Conductor timeline mode
	mode            LogMode
	conductorLines  []string // rendered conductor timeline entries
	conductorScroll int      // scroll position for conductor view
}

// NewLogPanel creates a new log panel with auto-scroll enabled.
func NewLogPanel() *LogPanel {
	return &LogPanel{
		autoScroll: true,
		expanded:   make(map[int]bool),
	}
}

// SetSize updates the panel dimensions.
func (p *LogPanel) SetSize(w, h int) {
	p.width = w
	p.height = h
}

// SetFocused sets whether this panel has focus.
func (p *LogPanel) SetFocused(focused bool) {
	p.focused = focused
}

// Update handles Bubble Tea messages.
func (p *LogPanel) Update(msg tea.Msg) tea.Cmd {
	return nil
}

// AppendEntries adds parsed log entries, renders them, and auto-scrolls.
func (p *LogPanel) AppendEntries(entries []logstream.LogEntry) {
	for _, e := range entries {
		idx := len(p.entries)
		p.entries = append(p.entries, e)
		p.entryLineOffsets = append(p.entryLineOffsets, len(p.renderedLines))
		lines := p.renderEntryAt(idx)
		p.renderedLines = append(p.renderedLines, lines...)
	}
	if p.autoScroll {
		p.scrollToBottom()
		p.cursor = len(p.entries) - 1
		if p.cursor < 0 {
			p.cursor = 0
		}
	}
}

// Clear resets the log content.
func (p *LogPanel) Clear() {
	p.entries = nil
	p.renderedLines = nil
	p.entryLineOffsets = nil
	p.expanded = make(map[int]bool)
	p.cursor = 0
	p.scrollPos = 0
}

// SetTask updates the header to show which task is being viewed.
func (p *LogPanel) SetTask(id, title string) {
	p.taskID = id
	p.taskTitle = title
	p.Clear()
}

// ToggleAutoScroll toggles auto-scroll on/off. Returns the new state.
func (p *LogPanel) ToggleAutoScroll() bool {
	p.autoScroll = !p.autoScroll
	if p.autoScroll {
		p.scrollToBottom()
		if len(p.entries) > 0 {
			p.cursor = len(p.entries) - 1
		}
	}
	return p.autoScroll
}

// AutoScroll returns whether auto-scroll is enabled.
func (p *LogPanel) AutoScroll() bool {
	return p.autoScroll
}

// CursorUp moves the cursor up by one entry and scrolls to keep it visible.
func (p *LogPanel) CursorUp() {
	if p.cursor > 0 {
		p.cursor--
		p.autoScroll = false
		p.ensureCursorVisible()
	}
}

// CursorDown moves the cursor down by one entry and scrolls to keep it visible.
func (p *LogPanel) CursorDown() {
	if p.cursor < len(p.entries)-1 {
		p.cursor++
		p.ensureCursorVisible()
	}
	// If cursor is at the last entry, re-enable auto-scroll
	if p.cursor >= len(p.entries)-1 {
		p.autoScroll = true
	}
}

// ToggleExpand toggles the expanded state for the entry at the cursor.
// Affects KindToolUse entries and KindToolResult entries that contain diffs.
func (p *LogPanel) ToggleExpand() {
	if p.cursor < 0 || p.cursor >= len(p.entries) {
		return
	}
	e := p.entries[p.cursor]
	switch e.Kind {
	case logstream.KindToolUse:
		// Always expandable
	case logstream.KindToolResult:
		// Only expandable if it's a diff result following an Edit tool call
		if p.cursor == 0 || p.entries[p.cursor-1].Kind != logstream.KindToolUse ||
			p.entries[p.cursor-1].ToolName != "Edit" || e.ToolResultFull == "" {
			return
		}
	default:
		return
	}
	p.expanded[p.cursor] = !p.expanded[p.cursor]
	p.rebuildRendered()
	p.ensureCursorVisible()
}

// ScrollUp moves the viewport up by one line.
func (p *LogPanel) ScrollUp() {
	if p.scrollPos > 0 {
		p.scrollPos--
		p.autoScroll = false
	}
}

// ScrollDown moves the viewport down by one line.
func (p *LogPanel) ScrollDown() {
	maxScroll := p.maxScroll()
	if p.scrollPos < maxScroll {
		p.scrollPos++
	}
	if p.scrollPos >= maxScroll {
		p.autoScroll = true
	}
}

// Cursor returns the current cursor position (entry index).
func (p *LogPanel) Cursor() int {
	return p.cursor
}

// IsExpanded returns whether the entry at the given index is expanded.
func (p *LogPanel) IsExpanded(idx int) bool {
	return p.expanded[idx]
}

func (p *LogPanel) scrollToBottom() {
	p.scrollPos = p.maxScroll()
}

func (p *LogPanel) maxScroll() int {
	avail := p.availRows()
	if len(p.renderedLines) <= avail {
		return 0
	}
	return len(p.renderedLines) - avail
}

func (p *LogPanel) availRows() int {
	// height minus header line
	avail := p.height - 2
	if avail < 1 {
		avail = 1
	}
	return avail
}

// ensureCursorVisible adjusts scrollPos so the cursor entry is within the viewport.
func (p *LogPanel) ensureCursorVisible() {
	if p.cursor < 0 || p.cursor >= len(p.entryLineOffsets) {
		return
	}
	entryStart := p.entryLineOffsets[p.cursor]
	entryEnd := p.entryEnd(p.cursor)
	avail := p.availRows()

	// If entry starts above viewport, scroll up
	if entryStart < p.scrollPos {
		p.scrollPos = entryStart
	}
	// If entry ends below viewport, scroll down
	if entryEnd > p.scrollPos+avail {
		p.scrollPos = entryEnd - avail
	}
}

// entryEnd returns the line index just past the last line of entry at idx.
func (p *LogPanel) entryEnd(idx int) int {
	if idx+1 < len(p.entryLineOffsets) {
		return p.entryLineOffsets[idx+1]
	}
	return len(p.renderedLines)
}

// rebuildRendered re-renders all entries from scratch, updating offsets.
func (p *LogPanel) rebuildRendered() {
	p.renderedLines = nil
	p.entryLineOffsets = make([]int, len(p.entries))
	for i := range p.entries {
		p.entryLineOffsets[i] = len(p.renderedLines)
		lines := p.renderEntryAt(i)
		p.renderedLines = append(p.renderedLines, lines...)
	}
}

// renderEntryAt converts entry at index into display lines, respecting expanded state.
func (p *LogPanel) renderEntryAt(idx int) []string {
	e := p.entries[idx]
	contentWidth := p.width - 4 // margin + prefix space
	if contentWidth < 10 {
		contentWidth = 10
	}

	switch e.Kind {
	case logstream.KindInit:
		s := logInitDim.Render("─── Session: " + e.SessionID + " ───")
		return []string{s}

	case logstream.KindText:
		prefix := logTextPrefix.Render("> ")
		text := e.Text
		// Word-wrap long text across multiple lines instead of truncating
		lines := wrapText(text, contentWidth-2) // -2 for "> " prefix
		var result []string
		for i, line := range lines {
			if i == 0 {
				result = append(result, prefix+line)
			} else {
				result = append(result, "  "+line) // indent continuation
			}
		}
		return result

	case logstream.KindToolUse:
		if p.expanded[idx] {
			return p.renderToolUseExpanded(e, contentWidth)
		}
		return p.renderToolUseCollapsed(e, contentWidth)

	case logstream.KindToolResult:
		if e.IsError {
			errText := strings.ReplaceAll(e.ToolResult, "\n", " ")
			line := logErrorText.Render("  ✗ " + truncateStr(errText, contentWidth-4))
			return []string{line}
		}
		// Check if this is a result for an Edit tool call with diff content
		if idx > 0 && p.entries[idx-1].Kind == logstream.KindToolUse &&
			p.entries[idx-1].ToolName == "Edit" && e.ToolResultFull != "" {
			return p.renderDiffResult(idx, e, contentWidth)
		}
		summary := strings.ReplaceAll(e.ToolResult, "\n", " ")
		if summary == "" {
			summary = "ok"
		}
		line := logResultDim.Render("  ↳ " + truncateStr(summary, contentWidth-4))
		return []string{line}

	case logstream.KindResult:
		return p.renderResult(e, contentWidth)

	default:
		// KindUnknown: show raw line dimmed
		return []string{logInitDim.Render(truncateStr(e.Raw, contentWidth))}
	}
}

// renderToolUseCollapsed renders a collapsed tool call: ▶ [ToolName] <truncated input>
func (p *LogPanel) renderToolUseCollapsed(e logstream.LogEntry, contentWidth int) []string {
	style := toolStyle(e.ToolName)
	name := style.Render(fmt.Sprintf("▶ [%s]", e.ToolName))
	maxInput := contentWidth - len(e.ToolName) - 5 // ▶ + [ + ] + space + margin
	if maxInput < 1 {
		maxInput = 1
	}
	input := logToolInput.Render(" " + truncateStr(e.ToolInput, maxInput))
	return []string{name + input}
}

// renderToolUseExpanded renders an expanded tool call with full input.
func (p *LogPanel) renderToolUseExpanded(e logstream.LogEntry, contentWidth int) []string {
	style := toolStyle(e.ToolName)
	header := style.Render(fmt.Sprintf("▼ [%s]", e.ToolName))
	result := []string{header}

	// Word-wrap the full tool input, indented 4 spaces
	indentWidth := contentWidth - 4
	if indentWidth < 10 {
		indentWidth = 10
	}
	inputLines := wrapTextFull(e.ToolInput, indentWidth)
	for _, line := range inputLines {
		result = append(result, logExpandedInput.Render("    "+line))
	}

	return result
}

// renderResult renders a completion/failure result with multi-line markdown parsing.
func (p *LogPanel) renderResult(e logstream.LogEntry, contentWidth int) []string {
	var lines []string
	lines = append(lines, "") // blank line before result

	if e.Success {
		lines = append(lines, logResultOk.Render("✓ Completed"))
	} else {
		lines = append(lines, logResultFail.Render("✗ Failed"))
	}

	if e.ResultText == "" {
		return lines
	}

	// Parse the markdown result into presentable sections.
	// Typical format: "## Task Complete: t-xxx\n### Changes Made\n- file: desc\n..."
	style := logToolDim
	if !e.Success {
		style = logErrorText
	}

	for _, rawLine := range strings.Split(e.ResultText, "\n") {
		trimmed := strings.TrimSpace(rawLine)
		if trimmed == "" {
			continue
		}
		// Strip markdown headers, render as styled labels
		if strings.HasPrefix(trimmed, "### ") {
			label := strings.TrimPrefix(trimmed, "### ")
			lines = append(lines, logTextPrefix.Render("  "+label))
		} else if strings.HasPrefix(trimmed, "## ") {
			// Skip top-level "## Task Complete: t-xxx" — redundant with "Completed" line
			continue
		} else if strings.HasPrefix(trimmed, "- ") {
			// File change bullets — word-wrap with indent
			bullet := "  " + trimmed
			wrapped := wrapText(bullet, contentWidth-2)
			for i, w := range wrapped {
				if i == 0 {
					lines = append(lines, style.Render(w))
				} else {
					lines = append(lines, style.Render("    "+w))
				}
			}
		} else {
			// Other text — word-wrap
			wrapped := wrapText("  "+trimmed, contentWidth-2)
			for _, w := range wrapped {
				lines = append(lines, style.Render(w))
			}
		}
	}

	return lines
}

const diffMaxCollapsedLines = 20

// renderDiffResult renders a tool result containing diff content with syntax highlighting.
func (p *LogPanel) renderDiffResult(idx int, e logstream.LogEntry, contentWidth int) []string {
	expanded := p.expanded[idx]
	maxLines := diffMaxCollapsedLines
	if expanded {
		maxLines = 0 // no limit
	}
	displayed, totalLines := logstream.FormatDiffLines(e.ToolResultFull, maxLines)

	var result []string
	for _, line := range displayed {
		styled := styleDiffLine(line, contentWidth-4)
		result = append(result, "  "+styled)
	}
	if !expanded && totalLines > diffMaxCollapsedLines {
		more := totalLines - diffMaxCollapsedLines
		indicator := diffContext.Faint(true).Render(fmt.Sprintf("  ... %d more lines (Enter to expand)", more))
		result = append(result, indicator)
	}
	return result
}

// styleDiffLine applies syntax highlighting to a single diff line.
func styleDiffLine(line string, maxWidth int) string {
	display := truncateStr(line, maxWidth)
	switch {
	case strings.HasPrefix(line, "@@"):
		return diffChunkHeader.Render(display)
	case strings.HasPrefix(line, "+"):
		return diffAdded.Render(display)
	case strings.HasPrefix(line, "-"):
		return diffRemoved.Render(display)
	default:
		return diffContext.Render(display)
	}
}

// wrapTextFull wraps text to fit within maxWidth, with no line limit.
func wrapTextFull(text string, maxWidth int) []string {
	text = strings.ReplaceAll(text, "\n", " ")
	text = strings.TrimSpace(text)
	if maxWidth <= 0 {
		maxWidth = 40
	}
	if text == "" {
		return []string{"(empty)"}
	}
	if len(text) <= maxWidth {
		return []string{text}
	}

	var lines []string
	remaining := text
	for len(remaining) > 0 {
		if len(remaining) <= maxWidth {
			lines = append(lines, remaining)
			break
		}
		cut := maxWidth
		for cut > maxWidth/2 {
			if remaining[cut] == ' ' {
				break
			}
			cut--
		}
		if cut <= maxWidth/2 {
			cut = maxWidth
		}
		lines = append(lines, remaining[:cut])
		remaining = strings.TrimLeft(remaining[cut:], " ")
	}
	return lines
}

// wrapText wraps text to fit within maxWidth, breaking on word boundaries.
// Returns at most 3 lines to prevent any single entry from dominating the panel.
func wrapText(text string, maxWidth int) []string {
	// Replace embedded newlines with spaces to prevent visual overflow
	text = strings.ReplaceAll(text, "\n", " ")
	text = strings.TrimSpace(text)
	if maxWidth <= 0 {
		maxWidth = 40
	}
	if len(text) <= maxWidth {
		return []string{text}
	}

	var lines []string
	remaining := text
	for len(remaining) > 0 && len(lines) < 3 {
		if len(remaining) <= maxWidth {
			lines = append(lines, remaining)
			break
		}
		// Find last space within maxWidth
		cut := maxWidth
		for cut > maxWidth/2 {
			if remaining[cut] == ' ' {
				break
			}
			cut--
		}
		if cut <= maxWidth/2 {
			cut = maxWidth // no good break point, hard cut
		}
		line := remaining[:cut]
		remaining = strings.TrimLeft(remaining[cut:], " ")
		if len(lines) == 2 && len(remaining) > 0 {
			// Last allowed line — truncate with ellipsis
			line = truncateStr(line+"  "+remaining, maxWidth)
			remaining = ""
		}
		lines = append(lines, line)
	}
	return lines
}

// entryForLine returns which entry index owns the given rendered line index.
func (p *LogPanel) entryForLine(lineIdx int) int {
	for i := len(p.entryLineOffsets) - 1; i >= 0; i-- {
		if p.entryLineOffsets[i] <= lineIdx {
			return i
		}
	}
	return 0
}

// SetMode switches between agent log and conductor timeline mode.
func (p *LogPanel) SetMode(mode LogMode) {
	p.mode = mode
}

// Mode returns the current log panel mode.
func (p *LogPanel) Mode() LogMode {
	return p.mode
}

// AppendConductorLog appends a conductor log message to the timeline.
func (p *LogPanel) AppendConductorLog(text string) {
	styled := renderConductorLine(text, p.width-4)
	p.conductorLines = append(p.conductorLines, styled)
	if p.autoScroll && p.mode == LogModeConductor {
		p.conductorScrollToBottom()
	}
}

// AppendConductorEvents renders DB events into the conductor timeline.
func (p *LogPanel) AppendConductorEvents(events []db.Event) {
	for _, e := range events {
		line := fmt.Sprintf("[%s] %s", e.EventType, eventSummary(e))
		styled := renderConductorLine(line, p.width-4)
		p.conductorLines = append(p.conductorLines, styled)
	}
	if p.autoScroll && p.mode == LogModeConductor {
		p.conductorScrollToBottom()
	}
}

// ConductorLineCount returns the number of conductor timeline entries.
func (p *LogPanel) ConductorLineCount() int {
	return len(p.conductorLines)
}

func (p *LogPanel) conductorScrollToBottom() {
	avail := p.availRows()
	if len(p.conductorLines) > avail {
		p.conductorScroll = len(p.conductorLines) - avail
	} else {
		p.conductorScroll = 0
	}
}

// ScrollUpConductor scrolls the conductor timeline up.
func (p *LogPanel) ScrollUpConductor() {
	if p.conductorScroll > 0 {
		p.conductorScroll--
		p.autoScroll = false
	}
}

// ScrollDownConductor scrolls the conductor timeline down.
func (p *LogPanel) ScrollDownConductor() {
	avail := p.availRows()
	maxScroll := len(p.conductorLines) - avail
	if maxScroll < 0 {
		maxScroll = 0
	}
	if p.conductorScroll < maxScroll {
		p.conductorScroll++
	}
	if p.conductorScroll >= maxScroll {
		p.autoScroll = true
	}
}

// renderConductorLine applies phase-aware coloring to a conductor log line.
func renderConductorLine(text string, maxWidth int) string {
	if maxWidth < 10 {
		maxWidth = 10
	}

	// Extract phase from [phase] prefix
	phase := ""
	if len(text) > 1 && text[0] == '[' {
		end := strings.Index(text, "]")
		if end > 0 {
			phase = strings.ToLower(text[1:end])
		}
	}

	color, ok := conductorPhaseColors[phase]
	if !ok {
		color = lipgloss.Color("#9CA3AF") // default gray
	}

	style := lipgloss.NewStyle().Foreground(color)
	return style.Render(truncateStr(text, maxWidth))
}

// eventSummary creates a short description from a DB event for conductor timeline display.
func eventSummary(e db.Event) string {
	parts := []string{}
	if e.TaskID.Valid {
		parts = append(parts, e.TaskID.String)
	}
	if e.AgentID.Valid {
		parts = append(parts, e.AgentID.String)
	}
	if e.Payload.Valid && len(e.Payload.String) > 0 && len(e.Payload.String) < 60 {
		parts = append(parts, e.Payload.String)
	}
	if len(parts) == 0 {
		return ""
	}
	return strings.Join(parts, " ")
}

// View renders the log panel.
func (p *LogPanel) View() string {
	if p.width < 10 || p.height < 3 {
		return ""
	}

	if p.mode == LogModeConductor {
		return p.viewConductor()
	}

	var b strings.Builder

	// Header
	headerStyle := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#06B6D4"))
	dimStyle := lipgloss.NewStyle().Foreground(colorDim)

	if p.taskID == "" && len(p.conductorLines) == 0 {
		b.WriteString(headerStyle.Render(" LOGS"))
		b.WriteString("\n")
		b.WriteString(dimStyle.Render(" Select a task to view logs"))
		b.WriteString("\n")
		return b.String()
	}

	if p.taskID == "" {
		// No task selected but conductor has data — show conductor mode
		return p.viewConductor()
	}

	// Show task context in header
	scrollIndicator := ""
	if !p.autoScroll {
		scrollIndicator = " [PAUSED]"
	}
	header := fmt.Sprintf(" LOGS — %s: %s%s",
		truncateStr(p.taskID, 12),
		truncateStr(p.taskTitle, p.width-28),
		scrollIndicator)
	b.WriteString(headerStyle.Render(truncateStr(header, p.width-2)))
	b.WriteString("\n")

	if len(p.renderedLines) == 0 {
		b.WriteString(dimStyle.Render(" Waiting for log output..."))
		b.WriteString("\n")
		return b.String()
	}

	// Determine which lines belong to the selected entry
	cursorStart := -1
	cursorEnd := -1
	if p.cursor >= 0 && p.cursor < len(p.entryLineOffsets) {
		cursorStart = p.entryLineOffsets[p.cursor]
		cursorEnd = p.entryEnd(p.cursor)
	}

	// Render visible lines
	avail := p.availRows()
	start := p.scrollPos
	end := start + avail
	if end > len(p.renderedLines) {
		end = len(p.renderedLines)
	}

	for i := start; i < end; i++ {
		line := p.renderedLines[i]
		if p.focused && i >= cursorStart && i < cursorEnd {
			// Highlight selected entry lines
			padded := padRight(line, p.width-2)
			b.WriteString(logSelectedBg.Render(padded))
		} else {
			b.WriteString(" ")
			b.WriteString(line)
		}
		b.WriteString("\n")
	}

	return b.String()
}

// viewConductor renders the conductor timeline view.
func (p *LogPanel) viewConductor() string {
	var b strings.Builder

	headerStyle := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#F97316")) // orange for conductor
	dimStyle := lipgloss.NewStyle().Foreground(colorDim)

	scrollIndicator := ""
	if !p.autoScroll {
		scrollIndicator = " [PAUSED]"
	}
	header := fmt.Sprintf(" CONDUCTOR%s", scrollIndicator)
	b.WriteString(headerStyle.Render(truncateStr(header, p.width-2)))
	b.WriteString("\n")

	if len(p.conductorLines) == 0 {
		b.WriteString(dimStyle.Render(" Waiting for conductor activity..."))
		b.WriteString("\n")
		return b.String()
	}

	avail := p.availRows()
	start := p.conductorScroll
	end := start + avail
	if end > len(p.conductorLines) {
		end = len(p.conductorLines)
	}

	for i := start; i < end; i++ {
		b.WriteString(" ")
		b.WriteString(p.conductorLines[i])
		b.WriteString("\n")
	}

	return b.String()
}
