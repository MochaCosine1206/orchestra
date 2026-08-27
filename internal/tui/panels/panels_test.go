package panels

import (
	"database/sql"
	"fmt"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/MochaCosine1206/orchestra/internal/db"
	"github.com/MochaCosine1206/orchestra/internal/orchestrator"
	"github.com/MochaCosine1206/orchestra/internal/tui/logstream"
)

// --- TaskPanel tests ---

func TestTaskPanelEmpty(t *testing.T) {
	p := NewTaskPanel()
	p.SetSize(80, 20)
	p.SetFocused(true)

	view := p.View()
	if !strings.Contains(view, "TASKS (0)") {
		t.Errorf("expected TASKS (0) header, got: %s", view)
	}
	if !strings.Contains(view, "(no tasks)") {
		t.Errorf("expected (no tasks), got: %s", view)
	}
}

func TestTaskPanelWithData(t *testing.T) {
	p := NewTaskPanel()
	p.SetSize(80, 20)
	p.SetFocused(true)

	tasks := []db.Task{
		{ID: "t1", Title: "Build auth", Status: "running", Role: "implementer"},
		{ID: "t2", Title: "Scout deps", Status: "done", Role: "scout"},
	}
	p.SetData(tasks)

	view := p.View()
	if !strings.Contains(view, "TASKS (2)") {
		t.Errorf("expected TASKS (2), got: %s", view)
	}
	if !strings.Contains(view, "Build auth") {
		t.Errorf("expected task title in view")
	}
}

func TestTaskPanelNavigation(t *testing.T) {
	p := NewTaskPanel()
	p.SetSize(80, 20)
	p.SetFocused(true)

	tasks := []db.Task{
		{ID: "t1", Title: "First", Status: "running", Role: "impl"},
		{ID: "t2", Title: "Second", Status: "pending", Role: "impl"},
		{ID: "t3", Title: "Third", Status: "done", Role: "impl"},
	}
	p.SetData(tasks)

	if p.SelectedTask().ID != "t1" {
		t.Error("initial selection should be first task")
	}

	p.MoveDown()
	if p.SelectedTask().ID != "t2" {
		t.Error("after MoveDown, should be on second task")
	}

	p.MoveDown()
	p.MoveDown() // should not go past end
	if p.SelectedTask().ID != "t3" {
		t.Error("should stay on last task")
	}

	p.MoveUp()
	if p.SelectedTask().ID != "t2" {
		t.Error("after MoveUp, should be on second task")
	}

	p.GoBottom()
	if p.SelectedTask().ID != "t3" {
		t.Error("GoBottom should select last task")
	}

	p.GoTop()
	if p.SelectedTask().ID != "t1" {
		t.Error("GoTop should select first task")
	}
}

func TestTaskPanelSelectedTaskEmpty(t *testing.T) {
	p := NewTaskPanel()
	if p.SelectedTask() != nil {
		t.Error("SelectedTask should be nil when empty")
	}
}

func TestTaskPanelElapsed(t *testing.T) {
	p := NewTaskPanel()
	p.SetSize(100, 20)
	p.SetFocused(false)

	started := time.Now().Add(-5 * time.Minute)
	tasks := []db.Task{
		{
			ID:        "t1",
			Title:     "Running task",
			Status:    "running",
			Role:      "impl",
			StartedAt: sql.NullTime{Time: started, Valid: true},
		},
	}
	p.SetData(tasks)

	view := p.View()
	if !strings.Contains(view, "5m") {
		t.Errorf("expected elapsed time ~5m in view, got: %s", view)
	}
}

// --- AgentPanel tests ---

func TestAgentPanelEmpty(t *testing.T) {
	p := NewAgentPanel()
	p.SetSize(80, 20)
	p.SetFocused(true)

	view := p.View()
	if !strings.Contains(view, "AGENTS (0)") {
		t.Errorf("expected AGENTS (0) header, got: %s", view)
	}
	if !strings.Contains(view, "(no agents)") {
		t.Errorf("expected (no agents), got: %s", view)
	}
}

func TestAgentPanelWithData(t *testing.T) {
	p := NewAgentPanel()
	p.SetSize(80, 20)
	p.SetFocused(false)

	agents := []db.Agent{
		{
			ID:     "a1",
			Role:   "implementer",
			Status: "active",
			HeartbeatAt: sql.NullTime{
				Time:  time.Now().Add(-30 * time.Second),
				Valid: true,
			},
			CreatedAt: time.Now(),
		},
	}
	p.SetData(agents)

	view := p.View()
	if !strings.Contains(view, "AGENTS (1)") {
		t.Errorf("expected AGENTS (1), got: %s", view)
	}
	if !strings.Contains(view, "implementer") {
		t.Errorf("expected agent role in view")
	}
}

func TestAgentPanelNavigation(t *testing.T) {
	p := NewAgentPanel()
	p.SetSize(80, 20)

	agents := []db.Agent{
		{ID: "a1", Role: "impl", Status: "active", CreatedAt: time.Now()},
		{ID: "a2", Role: "scout", Status: "idle", CreatedAt: time.Now()},
	}
	p.SetData(agents)

	p.MoveDown()
	p.MoveDown() // should not go past end

	p.GoTop()
	p.MoveUp() // should not go past start
}

func TestAgentPanelElapsed(t *testing.T) {
	p := NewAgentPanel()
	p.SetSize(100, 20)
	p.SetFocused(false)

	started := time.Now().Add(-5 * time.Minute)
	agents := []db.Agent{
		{
			ID:        "a1",
			Role:      "implementer",
			Status:    "working",
			CreatedAt: started,
		},
	}
	p.SetData(agents)

	view := p.View()
	if !strings.Contains(view, "working") {
		t.Errorf("expected 'working' in view, got: %s", view)
	}
	if !strings.Contains(view, "5m") {
		t.Errorf("expected elapsed time ~5m in view, got: %s", view)
	}
}

func TestAgentPanelElapsedIdle(t *testing.T) {
	p := NewAgentPanel()
	p.SetSize(100, 20)
	p.SetFocused(false)

	agents := []db.Agent{
		{
			ID:        "a1",
			Role:      "scout",
			Status:    "idle",
			CreatedAt: time.Now().Add(-10 * time.Minute),
		},
	}
	p.SetData(agents)

	view := p.View()
	if !strings.Contains(view, "idle") {
		t.Errorf("expected 'idle' in view, got: %s", view)
	}
	if strings.Contains(view, "10m") {
		t.Errorf("idle agents should NOT show elapsed time, got: %s", view)
	}
}

// --- StatusBar tests ---

func TestStatusBarEmpty(t *testing.T) {
	s := NewStatusBar()
	s.SetWidth(80)
	s.Update(nil, nil)

	view := s.View()
	if !strings.Contains(view, "(no tasks)") {
		t.Errorf("expected (no tasks) in status bar, got: %s", view)
	}
}

func TestStatusBarWithSummary(t *testing.T) {
	s := NewStatusBar()
	s.SetWidth(80)

	summary := &db.StatusSummary{
		Tasks: db.StatusCounts{
			Total:    3,
			ByStatus: map[string]int{"running": 1, "pending": 1, "done": 1},
		},
		Agents: db.StatusCounts{
			Total:    2,
			ByStatus: map[string]int{"active": 2},
		},
		LockCount: 0,
	}
	s.Update(summary, nil)

	view := s.View()
	if !strings.Contains(view, "1 running") {
		t.Errorf("expected '1 running' in status bar, got: %s", view)
	}
	if !strings.Contains(view, "2 agents") {
		t.Errorf("expected '2 agents' in status bar, got: %s", view)
	}
}

func TestStatusBarError(t *testing.T) {
	s := NewStatusBar()
	s.SetWidth(80)

	s.Update(nil, fmt.Errorf("connection failed"))
	view := s.View()
	if !strings.Contains(view, "DB Error") {
		t.Errorf("expected DB Error in status bar, got: %s", view)
	}
}

// --- TaskPanel filter tests ---

func TestTaskPanelFilterActivation(t *testing.T) {
	p := NewTaskPanel()
	p.SetSize(80, 20)
	p.SetFocused(true)

	tasks := []db.Task{
		{ID: "t1", Title: "Build auth", Status: "running", Role: "implementer"},
		{ID: "t2", Title: "Scout deps", Status: "done", Role: "scout"},
		{ID: "t3", Title: "Review code", Status: "failed", Role: "reviewer"},
	}
	p.SetData(tasks)

	p.ActivateFilter()
	if !p.FilterActive() {
		t.Error("filter should be active after ActivateFilter()")
	}
}

func TestTaskPanelFilterRegex(t *testing.T) {
	p := NewTaskPanel()
	p.SetSize(80, 20)
	p.SetFocused(true)

	tasks := []db.Task{
		{ID: "t1", Title: "Build auth", Status: "running", Role: "implementer"},
		{ID: "t2", Title: "Scout deps", Status: "done", Role: "scout"},
		{ID: "t3", Title: "Review code", Status: "failed", Role: "reviewer"},
	}
	p.SetData(tasks)

	// Simulate typing "impl" into filter
	p.ActivateFilter()
	p.filterInput.SetValue("impl")
	p.applyFilter()

	if len(p.visibleTasks()) != 1 {
		t.Errorf("expected 1 filtered task, got %d", len(p.visibleTasks()))
	}
	if p.SelectedTask().ID != "t1" {
		t.Errorf("expected t1, got %s", p.SelectedTask().ID)
	}
}

func TestTaskPanelFilterHeaderIndicator(t *testing.T) {
	p := NewTaskPanel()
	p.SetSize(80, 20)
	p.SetFocused(true)

	tasks := []db.Task{
		{ID: "t1", Title: "Build auth", Status: "running", Role: "impl"},
		{ID: "t2", Title: "Scout deps", Status: "done", Role: "scout"},
	}
	p.SetData(tasks)

	p.ActivateFilter()
	p.filterInput.SetValue("impl")
	p.applyFilter()

	view := p.View()
	if !strings.Contains(view, "1/2") {
		t.Errorf("header should show filtered count, got: %s", view[:min(len(view), 100)])
	}
	if !strings.Contains(view, "/impl") {
		t.Errorf("header should show filter pattern, got: %s", view[:min(len(view), 100)])
	}
}

func TestTaskPanelFilterEscapeClears(t *testing.T) {
	p := NewTaskPanel()
	p.SetSize(80, 20)
	p.SetFocused(true)

	tasks := []db.Task{
		{ID: "t1", Title: "Build auth", Status: "running", Role: "impl"},
		{ID: "t2", Title: "Scout deps", Status: "done", Role: "scout"},
	}
	p.SetData(tasks)

	p.ActivateFilter()
	p.filterInput.SetValue("impl")
	p.applyFilter()

	// Simulate Escape
	p.Update(tea.KeyMsg{Type: tea.KeyEscape})

	if p.FilterActive() {
		t.Error("filter should be inactive after Escape")
	}
	if len(p.visibleTasks()) != 2 {
		t.Errorf("all tasks should be visible after Escape, got %d", len(p.visibleTasks()))
	}
}

func TestTaskPanelFilterInvalidRegexDegrades(t *testing.T) {
	p := NewTaskPanel()
	p.SetSize(80, 20)
	p.SetFocused(true)

	tasks := []db.Task{
		{ID: "t1", Title: "Build auth [module]", Status: "running", Role: "impl"},
		{ID: "t2", Title: "Scout deps", Status: "done", Role: "scout"},
	}
	p.SetData(tasks)

	p.ActivateFilter()
	// Invalid regex pattern with unclosed bracket
	p.filterInput.SetValue("[module")
	p.applyFilter()

	// Should degrade to literal substring match (finding t1)
	visible := p.visibleTasks()
	if len(visible) != 1 {
		t.Errorf("invalid regex should degrade to literal match, got %d tasks", len(visible))
	}
}

func TestTaskPanelFilterCursorReset(t *testing.T) {
	p := NewTaskPanel()
	p.SetSize(80, 20)
	p.SetFocused(true)

	tasks := []db.Task{
		{ID: "t1", Title: "First", Status: "running", Role: "impl"},
		{ID: "t2", Title: "Second", Status: "done", Role: "scout"},
		{ID: "t3", Title: "Third", Status: "failed", Role: "impl"},
	}
	p.SetData(tasks)
	p.GoBottom() // cursor at t3

	// Filter to only 1 result
	p.ActivateFilter()
	p.filterInput.SetValue("scout")
	p.applyFilter()

	// Cursor should be clamped to valid range
	if p.cursor >= len(p.visibleTasks()) {
		t.Errorf("cursor should be clamped: cursor=%d, visible=%d", p.cursor, len(p.visibleTasks()))
	}
}

func TestTaskPanelFilterNoMatch(t *testing.T) {
	p := NewTaskPanel()
	p.SetSize(80, 20)
	p.SetFocused(true)

	tasks := []db.Task{
		{ID: "t1", Title: "Build auth", Status: "running", Role: "impl"},
	}
	p.SetData(tasks)

	p.ActivateFilter()
	p.filterInput.SetValue("nonexistent")
	p.applyFilter()

	view := p.View()
	if !strings.Contains(view, "no matching tasks") {
		t.Errorf("should show 'no matching tasks' when filter matches nothing, got: %s", view[:min(len(view), 200)])
	}
}

// --- LogPanel tests ---

func TestLogPanelEmptyState(t *testing.T) {
	p := NewLogPanel()
	p.SetSize(60, 15)
	p.SetFocused(false)

	view := p.View()
	if !strings.Contains(view, "LOGS") {
		t.Errorf("expected LOGS header, got: %s", view)
	}
	if !strings.Contains(view, "Select a task to view logs") {
		t.Errorf("expected empty state message, got: %s", view)
	}
}

func TestLogPanelWithTask(t *testing.T) {
	p := NewLogPanel()
	p.SetSize(60, 15)
	p.SetFocused(true)

	p.SetTask("t1", "Build auth module")
	view := p.View()
	if !strings.Contains(view, "t1") {
		t.Errorf("expected task ID in header, got: %s", view)
	}
	if !strings.Contains(view, "Build auth") {
		t.Errorf("expected task title in header, got: %s", view)
	}
	if !strings.Contains(view, "Waiting for log output") {
		t.Errorf("expected waiting message when no lines, got: %s", view)
	}
}

func TestLogPanelAppendEntries(t *testing.T) {
	p := NewLogPanel()
	p.SetSize(60, 15)
	p.SetFocused(true)
	p.SetTask("t1", "Test task")

	entries := []logstream.LogEntry{
		{Kind: logstream.KindText, Text: "analyzing the code"},
		{Kind: logstream.KindToolUse, ToolName: "Read", ToolInput: "main.go"},
		{Kind: logstream.KindToolResult, ToolResult: "(200 bytes)"},
	}
	p.AppendEntries(entries)
	view := p.View()
	if !strings.Contains(view, "analyzing the code") {
		t.Errorf("expected text entry in view, got: %s", view)
	}
	if !strings.Contains(view, "Read") {
		t.Errorf("expected tool name in view, got: %s", view)
	}
}

func TestLogPanelAutoScroll(t *testing.T) {
	p := NewLogPanel()
	p.SetSize(60, 6) // header(1) + divider(1) = 4 avail rows
	p.SetFocused(true)
	p.SetTask("t1", "Test")

	if !p.AutoScroll() {
		t.Error("auto-scroll should be on by default")
	}

	result := p.ToggleAutoScroll()
	if result {
		t.Error("toggle should return false after disabling")
	}
	if p.AutoScroll() {
		t.Error("auto-scroll should be off after toggle")
	}

	p.ToggleAutoScroll()
	if !p.AutoScroll() {
		t.Error("auto-scroll should be on after second toggle")
	}
}

func TestLogPanelClear(t *testing.T) {
	p := NewLogPanel()
	p.SetSize(60, 15)
	p.SetTask("t1", "Test")
	p.AppendEntries([]logstream.LogEntry{{Kind: logstream.KindText, Text: "data"}})
	p.Clear()

	view := p.View()
	if strings.Contains(view, "data") {
		t.Error("clear should remove all lines")
	}
}

func TestLogPanelScrollUpDisablesAutoScroll(t *testing.T) {
	p := NewLogPanel()
	p.SetSize(60, 5) // small height: header(1) + 3 visible rows
	p.SetTask("t1", "Test")

	// Add enough entries to require scrolling
	entries := make([]logstream.LogEntry, 10)
	for i := range entries {
		entries[i] = logstream.LogEntry{Kind: logstream.KindText, Text: fmt.Sprintf("line %d", i)}
	}
	p.AppendEntries(entries)

	// Auto-scroll should have us at the bottom
	if !p.AutoScroll() {
		t.Error("should be auto-scrolling")
	}

	// Scroll up should disable auto-scroll
	p.ScrollUp()
	if p.AutoScroll() {
		t.Error("scrolling up should disable auto-scroll")
	}
}

func TestLogPanelCursorNavigation(t *testing.T) {
	p := NewLogPanel()
	p.SetSize(60, 20)
	p.SetFocused(true)
	p.SetTask("t1", "Test")

	entries := []logstream.LogEntry{
		{Kind: logstream.KindText, Text: "first"},
		{Kind: logstream.KindToolUse, ToolName: "Read", ToolInput: "main.go"},
		{Kind: logstream.KindToolResult, ToolResult: "ok"},
	}
	p.AppendEntries(entries)

	// Auto-scroll puts cursor at last entry
	if p.Cursor() != 2 {
		t.Errorf("cursor should be at last entry (2), got %d", p.Cursor())
	}

	p.CursorUp()
	if p.Cursor() != 1 {
		t.Errorf("after CursorUp, cursor should be 1, got %d", p.Cursor())
	}

	p.CursorUp()
	if p.Cursor() != 0 {
		t.Errorf("after second CursorUp, cursor should be 0, got %d", p.Cursor())
	}

	p.CursorUp() // should not go below 0
	if p.Cursor() != 0 {
		t.Errorf("cursor should stay at 0, got %d", p.Cursor())
	}

	p.CursorDown()
	if p.Cursor() != 1 {
		t.Errorf("after CursorDown, cursor should be 1, got %d", p.Cursor())
	}

	p.CursorDown()
	p.CursorDown() // should not go past last entry
	if p.Cursor() != 2 {
		t.Errorf("cursor should stay at 2, got %d", p.Cursor())
	}
}

func TestLogPanelCursorUpDisablesAutoScroll(t *testing.T) {
	p := NewLogPanel()
	p.SetSize(60, 5)
	p.SetFocused(true)
	p.SetTask("t1", "Test")

	entries := make([]logstream.LogEntry, 10)
	for i := range entries {
		entries[i] = logstream.LogEntry{Kind: logstream.KindText, Text: fmt.Sprintf("line %d", i)}
	}
	p.AppendEntries(entries)

	if !p.AutoScroll() {
		t.Error("should be auto-scrolling after append")
	}

	p.CursorUp()
	if p.AutoScroll() {
		t.Error("CursorUp should disable auto-scroll")
	}
}

func TestLogPanelCursorDownReenablesAutoScroll(t *testing.T) {
	p := NewLogPanel()
	p.SetSize(60, 5)
	p.SetFocused(true)
	p.SetTask("t1", "Test")

	entries := make([]logstream.LogEntry, 5)
	for i := range entries {
		entries[i] = logstream.LogEntry{Kind: logstream.KindText, Text: fmt.Sprintf("line %d", i)}
	}
	p.AppendEntries(entries)

	p.CursorUp()
	p.CursorUp()
	if p.AutoScroll() {
		t.Error("should not be auto-scrolling after cursor up")
	}

	// Move cursor all the way down to last entry
	for i := 0; i < 5; i++ {
		p.CursorDown()
	}
	if !p.AutoScroll() {
		t.Error("auto-scroll should re-enable when cursor reaches bottom")
	}
}

func TestLogPanelToolUseCollapsed(t *testing.T) {
	p := NewLogPanel()
	p.SetSize(80, 15)
	p.SetFocused(true)
	p.SetTask("t1", "Test")

	entries := []logstream.LogEntry{
		{Kind: logstream.KindToolUse, ToolName: "Read", ToolInput: "main.go"},
	}
	p.AppendEntries(entries)

	view := p.View()
	if !strings.Contains(view, "▶") {
		t.Errorf("collapsed tool call should show ▶, got: %s", view)
	}
	if !strings.Contains(view, "Read") {
		t.Errorf("collapsed tool call should show tool name, got: %s", view)
	}
}

func TestLogPanelToggleExpand(t *testing.T) {
	p := NewLogPanel()
	p.SetSize(80, 20)
	p.SetFocused(true)
	p.SetTask("t1", "Test")

	entries := []logstream.LogEntry{
		{Kind: logstream.KindText, Text: "intro"},
		{Kind: logstream.KindToolUse, ToolName: "Bash", ToolInput: "echo hello world"},
	}
	p.AppendEntries(entries)

	// Move cursor to the tool use entry
	p.CursorUp()   // now at entry 0 (text)
	p.CursorDown() // now at entry 1 (tool use)
	// Actually with autoScroll, cursor starts at last entry (1)
	// So we don't need to move

	if p.IsExpanded(1) {
		t.Error("entry should start collapsed")
	}

	p.ToggleExpand()
	if !p.IsExpanded(1) {
		t.Error("entry should be expanded after toggle")
	}

	view := p.View()
	if !strings.Contains(view, "▼") {
		t.Errorf("expanded tool call should show ▼, got: %s", view)
	}

	p.ToggleExpand()
	if p.IsExpanded(1) {
		t.Error("entry should be collapsed after second toggle")
	}
}

func TestLogPanelToggleExpandOnlyToolUse(t *testing.T) {
	p := NewLogPanel()
	p.SetSize(80, 20)
	p.SetFocused(true)
	p.SetTask("t1", "Test")

	entries := []logstream.LogEntry{
		{Kind: logstream.KindText, Text: "just text"},
	}
	p.AppendEntries(entries)

	// Cursor is on a text entry
	p.ToggleExpand()
	if p.IsExpanded(0) {
		t.Error("toggle expand should have no effect on non-tool-use entries")
	}
}

func TestLogPanelClearResetsExpanded(t *testing.T) {
	p := NewLogPanel()
	p.SetSize(80, 20)
	p.SetFocused(true)
	p.SetTask("t1", "Test")

	entries := []logstream.LogEntry{
		{Kind: logstream.KindToolUse, ToolName: "Read", ToolInput: "file.go"},
	}
	p.AppendEntries(entries)
	p.ToggleExpand()

	if !p.IsExpanded(0) {
		t.Error("should be expanded")
	}

	p.Clear()
	if p.IsExpanded(0) {
		t.Error("clear should reset expanded state")
	}
	if p.Cursor() != 0 {
		t.Error("clear should reset cursor")
	}
}

func TestLogPanelSelectedHighlight(t *testing.T) {
	p := NewLogPanel()
	p.SetSize(60, 20)
	p.SetFocused(true)
	p.SetTask("t1", "Test")

	entries := []logstream.LogEntry{
		{Kind: logstream.KindText, Text: "line one"},
		{Kind: logstream.KindText, Text: "line two"},
	}
	p.AppendEntries(entries)

	// With focus, the selected entry should be visible in the view
	view := p.View()
	if view == "" {
		t.Error("view should not be empty")
	}
}

func TestLogPanelPanelInterface(t *testing.T) {
	// Verify LogPanel satisfies the Panel interface
	var p Panel = NewLogPanel()
	p.SetSize(60, 15)
	p.SetFocused(true)
	_ = p.View()
	_ = p.Update(nil)
}

func TestTaskPanelPanelInterface(t *testing.T) {
	var p Panel = NewTaskPanel()
	p.SetSize(60, 15)
	p.SetFocused(true)
	_ = p.View()
	_ = p.Update(nil)
}

func TestAgentPanelPanelInterface(t *testing.T) {
	var p Panel = NewAgentPanel()
	p.SetSize(60, 15)
	p.SetFocused(true)
	_ = p.View()
	_ = p.Update(nil)
}

// --- Toast tests ---

func TestToastCreation(t *testing.T) {
	tm := NewToastManager()
	tm.Add("Task completed", ToastSuccess)

	if tm.Count() != 1 {
		t.Errorf("expected 1 toast, got %d", tm.Count())
	}

	view := tm.View(40)
	if !strings.Contains(view, "Task completed") {
		t.Errorf("toast should contain message, got: %s", view)
	}
}

func TestToastAutoExpire(t *testing.T) {
	tm := NewToastManager()
	// Create a toast with expired timestamp
	tm.toasts = append(tm.toasts, Toast{
		Message:   "old event",
		Type:      ToastInfo,
		CreatedAt: time.Now().Add(-10 * time.Second), // older than 5s
	})

	tm.Tick()

	if tm.Count() != 0 {
		t.Errorf("expired toast should be removed, got %d", tm.Count())
	}
}

func TestToastQueueLimit(t *testing.T) {
	tm := NewToastManager()
	tm.Add("one", ToastInfo)
	tm.Add("two", ToastSuccess)
	tm.Add("three", ToastError)
	tm.Add("four", ToastInfo) // should push out "one"

	if tm.Count() != 3 {
		t.Errorf("expected max 3 toasts, got %d", tm.Count())
	}

	view := tm.View(40)
	if strings.Contains(view, "one") {
		t.Error("oldest toast should be evicted")
	}
	if !strings.Contains(view, "four") {
		t.Error("newest toast should be present")
	}
}

func TestToastNoDuplicateStale(t *testing.T) {
	tm := NewToastManager()
	tm.Add("event A", ToastInfo)
	if tm.Count() != 1 {
		t.Errorf("expected 1 toast, got %d", tm.Count())
	}

	// Adding a different message should work
	tm.Add("event B", ToastSuccess)
	if tm.Count() != 2 {
		t.Errorf("expected 2 toasts, got %d", tm.Count())
	}
}

func TestToastEmptyView(t *testing.T) {
	tm := NewToastManager()
	view := tm.View(40)
	if view != "" {
		t.Errorf("empty toast manager should render empty string, got: %q", view)
	}
}

func TestToastTypeForEvent(t *testing.T) {
	cases := []struct {
		event string
		want  ToastType
	}{
		{"agent_spawned", ToastInfo},
		{"task_completed", ToastSuccess},
		{"task_failed", ToastError},
		{"unknown_event", ToastInfo},
	}
	for _, tc := range cases {
		got := ToastTypeForEvent(tc.event)
		if got != tc.want {
			t.Errorf("ToastTypeForEvent(%q) = %d, want %d", tc.event, got, tc.want)
		}
	}
}

func TestToastColors(t *testing.T) {
	tm := NewToastManager()
	tm.Add("success", ToastSuccess)
	tm.Add("error", ToastError)
	tm.Add("info", ToastInfo)

	// Just verify View doesn't panic with different types
	view := tm.View(40)
	if view == "" {
		t.Error("view should not be empty with 3 toasts")
	}
}

func TestToastTickKeepsLive(t *testing.T) {
	tm := NewToastManager()
	tm.Add("recent", ToastInfo) // just created, should survive tick

	tm.Tick()

	if tm.Count() != 1 {
		t.Errorf("recent toast should survive tick, got %d", tm.Count())
	}
}

// --- Helper tests ---

func TestTruncateStr(t *testing.T) {
	cases := []struct {
		input string
		max   int
		want  string
	}{
		{"hello", 10, "hello"},
		{"hello world", 8, "hello..."},
		{"hi", 2, "hi"},
		{"abc", 3, "abc"},
		{"abcd", 3, "abc"},
		{"", 5, ""},
	}
	for _, tc := range cases {
		got := truncateStr(tc.input, tc.max)
		if got != tc.want {
			t.Errorf("truncateStr(%q, %d) = %q, want %q", tc.input, tc.max, got, tc.want)
		}
	}
}

func TestPadRight(t *testing.T) {
	got := padRight("hi", 5)
	if got != "hi   " {
		t.Errorf("padRight(hi, 5) = %q, want %q", got, "hi   ")
	}
	got = padRight("hello", 3)
	if got != "hello" {
		t.Errorf("padRight should not truncate")
	}
}

// --- Diff rendering tests ---

func TestLogPanelDiffRendering(t *testing.T) {
	p := NewLogPanel()
	p.SetSize(80, 30)
	p.SetFocused(true)
	p.SetTask("t1", "Test diff")

	diffContent := "+added line\n-removed line\n context line"
	entries := []logstream.LogEntry{
		{Kind: logstream.KindToolUse, ToolName: "Edit", ToolInput: "main.go"},
		{Kind: logstream.KindToolResult, ToolResult: "ok", ToolResultFull: diffContent},
	}
	p.AppendEntries(entries)

	view := p.View()
	// Should contain diff lines, not the "ok" summary
	if !strings.Contains(view, "added line") {
		t.Errorf("expected diff content in view, got: %s", view)
	}
	if !strings.Contains(view, "removed line") {
		t.Errorf("expected diff removed line in view, got: %s", view)
	}
}

func TestLogPanelDiffOnlyForEdit(t *testing.T) {
	p := NewLogPanel()
	p.SetSize(80, 30)
	p.SetFocused(true)
	p.SetTask("t1", "Test")

	diffContent := "+added line\n-removed line"
	entries := []logstream.LogEntry{
		{Kind: logstream.KindToolUse, ToolName: "Read", ToolInput: "main.go"},
		{Kind: logstream.KindToolResult, ToolResult: "ok", ToolResultFull: diffContent},
	}
	p.AppendEntries(entries)

	view := p.View()
	// Should NOT render as diff since the preceding tool is Read, not Edit
	if strings.Contains(view, "added line") {
		t.Errorf("should not render diff for non-Edit tool result, got: %s", view)
	}
}

func TestLogPanelDiffCollapsedTruncation(t *testing.T) {
	p := NewLogPanel()
	p.SetSize(80, 40)
	p.SetFocused(true)
	p.SetTask("t1", "Test diff truncation")

	// Create diff with more than 20 lines
	var lines []string
	for i := 0; i < 30; i++ {
		lines = append(lines, fmt.Sprintf("+line %d", i))
	}
	diffContent := strings.Join(lines, "\n")

	entries := []logstream.LogEntry{
		{Kind: logstream.KindToolUse, ToolName: "Edit", ToolInput: "main.go"},
		{Kind: logstream.KindToolResult, ToolResult: "ok", ToolResultFull: diffContent},
	}
	p.AppendEntries(entries)

	view := p.View()
	// Should show "more lines" indicator
	if !strings.Contains(view, "more lines") {
		t.Errorf("expected 'more lines' indicator for long diff, got: %s", view)
	}
}

func TestLogPanelToggleExpandDiffResult(t *testing.T) {
	p := NewLogPanel()
	p.SetSize(80, 60)
	p.SetFocused(true)
	p.SetTask("t1", "Test expand")

	// Create diff with more than 20 lines
	var lines []string
	for i := 0; i < 25; i++ {
		lines = append(lines, fmt.Sprintf("+line %d", i))
	}
	diffContent := strings.Join(lines, "\n")

	entries := []logstream.LogEntry{
		{Kind: logstream.KindToolUse, ToolName: "Edit", ToolInput: "main.go"},
		{Kind: logstream.KindToolResult, ToolResult: "ok", ToolResultFull: diffContent},
	}
	p.AppendEntries(entries)

	// Cursor should be at last entry (index 1 = tool result) due to auto-scroll
	if p.Cursor() != 1 {
		t.Fatalf("cursor should be at entry 1, got %d", p.Cursor())
	}

	// Should be expandable since it's a diff result after Edit
	if p.IsExpanded(1) {
		t.Error("should start collapsed")
	}

	p.ToggleExpand()
	if !p.IsExpanded(1) {
		t.Error("should be expanded after toggle")
	}

	view := p.View()
	// After expanding, should show all 25 lines (no "more lines" indicator)
	if strings.Contains(view, "more lines") {
		t.Errorf("expanded diff should show all lines, got: %s", view)
	}
	// Should contain the last line
	if !strings.Contains(view, "line 24") {
		t.Errorf("expanded diff should show last line, got: %s", view)
	}

	p.ToggleExpand()
	if p.IsExpanded(1) {
		t.Error("should be collapsed after second toggle")
	}
}

func TestLogPanelToggleExpandNonDiffToolResult(t *testing.T) {
	p := NewLogPanel()
	p.SetSize(80, 20)
	p.SetFocused(true)
	p.SetTask("t1", "Test")

	entries := []logstream.LogEntry{
		{Kind: logstream.KindToolUse, ToolName: "Edit", ToolInput: "main.go"},
		{Kind: logstream.KindToolResult, ToolResult: "some normal result"},
	}
	p.AppendEntries(entries)

	// Cursor at entry 1 (tool result without ToolResultFull)
	p.ToggleExpand()
	if p.IsExpanded(1) {
		t.Error("non-diff tool result should not be expandable")
	}
}

// --- ClarifyPanel tests ---

func TestClarifyPanelEmpty(t *testing.T) {
	p := NewClarifyPanel()
	if p.Active() {
		t.Error("empty panel should not be active")
	}
	if p.View() != "" {
		t.Error("empty panel should render empty string")
	}
}

func TestClarifyPanelSetQuestions(t *testing.T) {
	p := NewClarifyPanel()
	p.SetSize(80, 20)

	questions := []orchestrator.ClarifyQuestion{
		{ID: 1, Question: "Which scope?", Default: "all files", Context: "Goal is ambiguous",
			Options: []string{"all files", "src/ only", "tests only"}},
		{ID: 2, Question: "Include tests?", Default: "yes",
			Options: []string{"yes", "no"}},
	}
	p.SetQuestions(questions)

	if !p.Active() {
		t.Error("panel should be active after SetQuestions")
	}

	view := p.View()
	if !strings.Contains(view, "Goal Clarification (2 questions)") {
		t.Errorf("expected header with question count, got: %s", view[:min(len(view), 200)])
	}
	if !strings.Contains(view, "Which scope?") {
		t.Error("view should contain first question")
	}
	if !strings.Contains(view, "all files") {
		t.Error("view should show first option")
	}
	if !strings.Contains(view, "src/ only") {
		t.Error("view should show second option")
	}
	if !strings.Contains(view, "Goal is ambiguous") {
		t.Error("view should show context when present")
	}
	if !strings.Contains(view, "Other") {
		t.Error("view should show 'Other' option")
	}
}

func TestClarifyPanelOptionNavigation(t *testing.T) {
	p := NewClarifyPanel()
	p.SetSize(80, 20)
	p.Focus()

	questions := []orchestrator.ClarifyQuestion{
		{ID: 1, Question: "Scope?", Default: "all",
			Options: []string{"all", "src/", "tests"}},
	}
	p.SetQuestions(questions)

	// Start at option 0
	if p.option != 0 {
		t.Errorf("should start at option 0, got %d", p.option)
	}

	// Down arrow moves to next option
	p.Update(tea.KeyMsg{Type: tea.KeyDown})
	if p.option != 1 {
		t.Errorf("after Down, should be at option 1, got %d", p.option)
	}

	// Down again to option 2
	p.Update(tea.KeyMsg{Type: tea.KeyDown})
	if p.option != 2 {
		t.Errorf("after second Down, should be at option 2, got %d", p.option)
	}

	// Down to "Other" (index 3)
	p.Update(tea.KeyMsg{Type: tea.KeyDown})
	if p.option != 3 {
		t.Errorf("should be on Other (3), got %d", p.option)
	}

	// Down at end stays
	p.Update(tea.KeyMsg{Type: tea.KeyDown})
	if p.option != 3 {
		t.Errorf("Down at end should stay at 3, got %d", p.option)
	}

	// Up goes back
	p.Update(tea.KeyMsg{Type: tea.KeyUp})
	if p.option != 2 {
		t.Errorf("after Up, should be at 2, got %d", p.option)
	}
}

func TestClarifyPanelEnterSelectsOption(t *testing.T) {
	p := NewClarifyPanel()
	p.SetSize(80, 20)
	p.Focus()

	questions := []orchestrator.ClarifyQuestion{
		{ID: 1, Question: "Scope?", Default: "all",
			Options: []string{"all", "src/ only"}},
	}
	p.SetQuestions(questions)

	// Move to second option
	p.Update(tea.KeyMsg{Type: tea.KeyDown})

	// Enter on last question should submit
	cmd := p.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("Enter on last question should return submit cmd")
	}

	msg := cmd()
	submit, ok := msg.(ClarifySubmitMsg)
	if !ok {
		t.Fatalf("expected ClarifySubmitMsg, got %T", msg)
	}
	if submit.Questions[0].Answer != "src/ only" {
		t.Errorf("answer should be selected option, got %q", submit.Questions[0].Answer)
	}
}

func TestClarifyPanelEnterOnOtherStartsTyping(t *testing.T) {
	p := NewClarifyPanel()
	p.SetSize(80, 20)
	p.Focus()

	questions := []orchestrator.ClarifyQuestion{
		{ID: 1, Question: "Scope?", Default: "all",
			Options: []string{"all", "src/"}},
	}
	p.SetQuestions(questions)

	// Navigate to "Other" (index 2 = after 2 options)
	p.Update(tea.KeyMsg{Type: tea.KeyDown})
	p.Update(tea.KeyMsg{Type: tea.KeyDown})

	// Enter on "Other" should start typing mode
	p.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if !p.typing {
		t.Error("Enter on Other should start typing mode")
	}
}

func TestClarifyPanelDefaultAnswer(t *testing.T) {
	p := NewClarifyPanel()
	p.SetSize(80, 20)
	p.Focus()

	questions := []orchestrator.ClarifyQuestion{
		{ID: 1, Question: "Scope?", Default: "all",
			Options: []string{"all", "src/"}},
	}
	p.SetQuestions(questions)

	// Enter immediately (first option pre-selected)
	cmd := p.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("expected submit cmd")
	}
	msg := cmd()
	submit := msg.(ClarifySubmitMsg)
	if submit.Questions[0].Answer != "all" {
		t.Errorf("default should be first option, got %q", submit.Questions[0].Answer)
	}
}

func TestClarifyPanelMultiQuestionAdvance(t *testing.T) {
	p := NewClarifyPanel()
	p.SetSize(80, 20)
	p.Focus()

	questions := []orchestrator.ClarifyQuestion{
		{ID: 1, Question: "Q1?", Default: "D1", Options: []string{"D1", "alt1"}},
		{ID: 2, Question: "Q2?", Default: "D2", Options: []string{"D2", "alt2"}},
	}
	p.SetQuestions(questions)

	// Enter on first question advances to second
	cmd := p.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd != nil {
		t.Error("Enter on non-last question should not submit")
	}
	if p.current != 1 {
		t.Errorf("should advance to question 1, got %d", p.current)
	}
	if p.option != 0 {
		t.Errorf("option should reset to 0 for new question, got %d", p.option)
	}
}

func TestClarifyPanelEscapeSkips(t *testing.T) {
	p := NewClarifyPanel()
	p.SetSize(80, 20)
	p.Focus()

	questions := []orchestrator.ClarifyQuestion{
		{ID: 1, Question: "Q1?", Default: "D1", Options: []string{"D1"}},
	}
	p.SetQuestions(questions)

	cmd := p.Update(tea.KeyMsg{Type: tea.KeyEscape})
	if cmd == nil {
		t.Fatal("Escape should return skip cmd")
	}

	msg := cmd()
	if _, ok := msg.(ClarifySkipMsg); !ok {
		t.Fatalf("expected ClarifySkipMsg, got %T", msg)
	}
}

func TestClarifyPanelBlurredIgnoresKeys(t *testing.T) {
	p := NewClarifyPanel()
	p.SetSize(80, 20)
	// Not focused

	questions := []orchestrator.ClarifyQuestion{
		{ID: 1, Question: "Q1?", Default: "D1", Options: []string{"D1"}},
	}
	p.SetQuestions(questions)

	cmd := p.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd != nil {
		t.Error("blurred panel should ignore key input")
	}
}

func TestClarifyPanelFooter(t *testing.T) {
	p := NewClarifyPanel()
	p.SetSize(80, 20)

	questions := []orchestrator.ClarifyQuestion{
		{ID: 1, Question: "Q1?", Default: "D1", Options: []string{"D1"}},
	}
	p.SetQuestions(questions)

	view := p.View()
	if !strings.Contains(view, "Enter: confirm") {
		t.Error("view should show keybind footer")
	}
	if !strings.Contains(view, "Esc: skip") {
		t.Error("view should show escape hint")
	}
}

func TestClarifyPanelShowsCheckmarkForAnswered(t *testing.T) {
	p := NewClarifyPanel()
	p.SetSize(80, 20)
	p.Focus()

	questions := []orchestrator.ClarifyQuestion{
		{ID: 1, Question: "Q1?", Default: "D1", Options: []string{"D1", "alt1"}},
		{ID: 2, Question: "Q2?", Default: "D2", Options: []string{"D2", "alt2"}},
	}
	p.SetQuestions(questions)

	// Answer first question (advances to second)
	p.Update(tea.KeyMsg{Type: tea.KeyEnter})

	view := p.View()
	if !strings.Contains(view, "✓") {
		t.Error("answered question should show checkmark")
	}
}

func TestClarifyPanelContextWraps(t *testing.T) {
	p := NewClarifyPanel()
	p.SetSize(50, 20) // narrow width to trigger wrapping
	p.Focus()

	questions := []orchestrator.ClarifyQuestion{
		{ID: 1, Question: "Q?", Default: "D",
			Context: "This is a very long context string that should wrap to multiple lines when the panel is narrow enough",
			Options: []string{"D"}},
	}
	p.SetQuestions(questions)

	view := p.View()
	// Context should be present (wrapped)
	if !strings.Contains(view, "very long context") {
		t.Error("view should contain wrapped context text")
	}
}

// --- Conductor Timeline tests ---

func TestLogPanelConductorModeDefault(t *testing.T) {
	p := NewLogPanel()
	if p.Mode() != LogModeAgent {
		t.Error("default mode should be LogModeAgent")
	}
}

func TestLogPanelSetMode(t *testing.T) {
	p := NewLogPanel()
	p.SetMode(LogModeConductor)
	if p.Mode() != LogModeConductor {
		t.Error("mode should be LogModeConductor after SetMode")
	}
	p.SetMode(LogModeAgent)
	if p.Mode() != LogModeAgent {
		t.Error("mode should be LogModeAgent after SetMode")
	}
}

func TestLogPanelConductorEmptyView(t *testing.T) {
	p := NewLogPanel()
	p.SetSize(60, 15)
	p.SetFocused(true)
	p.SetMode(LogModeConductor)

	view := p.View()
	if !strings.Contains(view, "CONDUCTOR") {
		t.Errorf("conductor mode should show CONDUCTOR header, got: %s", view)
	}
	if !strings.Contains(view, "Waiting for conductor activity") {
		t.Errorf("empty conductor should show waiting message, got: %s", view)
	}
}

func TestLogPanelAppendConductorLog(t *testing.T) {
	p := NewLogPanel()
	p.SetSize(80, 20)
	p.SetFocused(true)
	p.SetMode(LogModeConductor)

	p.AppendConductorLog("[decompose] Splitting goal into 3 tasks")
	p.AppendConductorLog("[spawn] Starting agent for t-001")

	if p.ConductorLineCount() != 2 {
		t.Errorf("expected 2 conductor lines, got %d", p.ConductorLineCount())
	}

	view := p.View()
	if !strings.Contains(view, "Splitting goal") {
		t.Errorf("conductor view should show log text, got: %s", view)
	}
	if !strings.Contains(view, "Starting agent") {
		t.Errorf("conductor view should show second log, got: %s", view)
	}
}

func TestLogPanelAppendConductorEvents(t *testing.T) {
	p := NewLogPanel()
	p.SetSize(80, 20)
	p.SetFocused(true)
	p.SetMode(LogModeConductor)

	events := []db.Event{
		{ID: 1, EventType: "task_created", TaskID: sql.NullString{String: "t-001", Valid: true}},
		{ID: 2, EventType: "agent_spawned", AgentID: sql.NullString{String: "a-001", Valid: true}},
	}
	p.AppendConductorEvents(events)

	if p.ConductorLineCount() != 2 {
		t.Errorf("expected 2 conductor lines from events, got %d", p.ConductorLineCount())
	}

	view := p.View()
	if !strings.Contains(view, "task_created") {
		t.Errorf("conductor view should show event type, got: %s", view)
	}
}

func TestLogPanelConductorPhaseColoring(t *testing.T) {
	p := NewLogPanel()
	p.SetSize(80, 20)
	p.SetMode(LogModeConductor)

	// Test different phases — just verify no panics and lines are added
	phases := []string{
		"[decompose] analyzing goal",
		"[spawn] starting agent",
		"[merge] combining branches",
		"[fail] task t-001 failed",
		"[review] checking quality",
		"[refinement] retrying task",
		"plain log without phase prefix",
	}
	for _, ph := range phases {
		p.AppendConductorLog(ph)
	}

	if p.ConductorLineCount() != len(phases) {
		t.Errorf("expected %d lines, got %d", len(phases), p.ConductorLineCount())
	}
}

func TestLogPanelConductorScrolling(t *testing.T) {
	p := NewLogPanel()
	p.SetSize(60, 6) // small height: header(1) + ~3 avail rows
	p.SetMode(LogModeConductor)

	// Add many lines to trigger scrolling
	for i := 0; i < 20; i++ {
		p.AppendConductorLog(fmt.Sprintf("[spawn] agent %d", i))
	}

	// Should be auto-scrolled to bottom
	if !p.AutoScroll() {
		t.Error("should be auto-scrolling")
	}

	// Scroll up should disable auto-scroll
	p.ScrollUpConductor()
	if p.AutoScroll() {
		t.Error("scrolling up should disable auto-scroll")
	}
}

func TestLogPanelModeSwitchPreservesData(t *testing.T) {
	p := NewLogPanel()
	p.SetSize(80, 20)
	p.SetFocused(true)

	// Add conductor data
	p.AppendConductorLog("[decompose] planning")

	// Add agent data
	p.SetTask("t1", "Test task")
	entries := []logstream.LogEntry{
		{Kind: logstream.KindText, Text: "agent working"},
	}
	p.AppendEntries(entries)

	// Switch to conductor mode
	p.SetMode(LogModeConductor)
	view := p.View()
	if !strings.Contains(view, "CONDUCTOR") {
		t.Error("should show conductor header")
	}
	if !strings.Contains(view, "planning") {
		t.Error("should show conductor data")
	}

	// Switch back to agent mode
	p.SetMode(LogModeAgent)
	view = p.View()
	if !strings.Contains(view, "agent working") {
		t.Error("should show agent data after switching back")
	}
}

func TestLogPanelAutoShowConductorWhenNoTask(t *testing.T) {
	p := NewLogPanel()
	p.SetSize(60, 15)
	p.SetFocused(true)

	// No task selected, no conductor data — show default empty state
	view := p.View()
	if !strings.Contains(view, "Select a task") {
		t.Errorf("with no data should show default message, got: %s", view)
	}

	// Add conductor data without selecting a task
	p.AppendConductorLog("[decompose] starting")
	view = p.View()
	// Should auto-show conductor view when no task selected but conductor has data
	if !strings.Contains(view, "CONDUCTOR") {
		t.Errorf("should auto-show conductor when no task selected, got: %s", view)
	}
}

func TestFmtDur(t *testing.T) {
	cases := []struct {
		dur  time.Duration
		want string
	}{
		{30 * time.Second, "30s"},
		{5 * time.Minute, "5m"},
		{2*time.Hour + 15*time.Minute, "2h15m"},
	}
	for _, tc := range cases {
		got := fmtDur(tc.dur)
		if got != tc.want {
			t.Errorf("fmtDur(%v) = %q, want %q", tc.dur, got, tc.want)
		}
	}
}
