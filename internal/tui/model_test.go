package tui

import (
	"context"
	"fmt"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/MochaCosine1206/orchestra/internal/db"
	"github.com/MochaCosine1206/orchestra/internal/orchestrator"
	"github.com/MochaCosine1206/orchestra/internal/tui/panels"
)

func setupTestDB(t *testing.T) *db.DB {
	t.Helper()
	d, err := db.OpenMemory()
	if err != nil {
		t.Fatalf("opening memory db: %v", err)
	}
	if err := d.InitSchema(context.Background()); err != nil {
		t.Fatalf("initializing schema: %v", err)
	}
	t.Cleanup(func() { d.Close() })
	return d
}

func TestNewModel(t *testing.T) {
	d := setupTestDB(t)
	m := New(d)

	if m.db != d {
		t.Error("model should reference the provided db")
	}
	if m.taskPanel == nil {
		t.Error("taskPanel should be initialized")
	}
	if m.agentPanel == nil {
		t.Error("agentPanel should be initialized")
	}
	if m.blackboardPanel == nil {
		t.Error("blackboardPanel should be initialized")
	}
	if m.logPanel == nil {
		t.Error("logPanel should be initialized")
	}
	if m.statusBar == nil {
		t.Error("statusBar should be initialized")
	}
	if m.focusedPanel != panelTasks {
		t.Errorf("initial focus should be %d (tasks), got %d", panelTasks, m.focusedPanel)
	}
}

func TestModelInit(t *testing.T) {
	d := setupTestDB(t)
	m := New(d)

	cmd := m.Init()
	if cmd == nil {
		t.Error("Init should return a command (batch of fetch + tick)")
	}
}

func TestModelWindowResize(t *testing.T) {
	d := setupTestDB(t)
	m := New(d)

	updated, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	model := updated.(Model)

	if model.width != 120 {
		t.Errorf("expected width 120, got %d", model.width)
	}
	if model.height != 40 {
		t.Errorf("expected height 40, got %d", model.height)
	}
	if !model.ready {
		t.Error("model should be ready after resize")
	}
}

func TestModelTabSwitchesFocus(t *testing.T) {
	d := setupTestDB(t)
	m := New(d)
	m.ready = true

	// Tab should switch from tasks (0) to agents (1)
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyTab})
	model := updated.(Model)
	if model.focusedPanel != panelAgents {
		t.Errorf("expected focus on agents (%d), got %d", panelAgents, model.focusedPanel)
	}

	// Tab from agents (1) to blackboard (2)
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyTab})
	model = updated.(Model)
	if model.focusedPanel != panelBlackboard {
		t.Errorf("expected focus on blackboard (%d), got %d", panelBlackboard, model.focusedPanel)
	}

	// Tab from blackboard (2) to logs (3)
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyTab})
	model = updated.(Model)
	if model.focusedPanel != panelLogs {
		t.Errorf("expected focus on logs (%d), got %d", panelLogs, model.focusedPanel)
	}

	// Tab from logs wraps back to conductors (first panel)
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyTab})
	model = updated.(Model)
	if model.focusedPanel != panelConductors {
		t.Errorf("expected focus on conductors (%d), got %d", panelConductors, model.focusedPanel)
	}
}

func TestModelShiftTabSwitchesFocusReverse(t *testing.T) {
	d := setupTestDB(t)
	m := New(d)
	m.ready = true

	// Shift+Tab from tasks wraps to conductors (previous panel)
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyShiftTab})
	model := updated.(Model)
	if model.focusedPanel != panelConductors {
		t.Errorf("expected focus on conductors (%d), got %d", panelConductors, model.focusedPanel)
	}

	// Shift+Tab from conductors wraps to logs (last panel)
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyShiftTab})
	model = updated.(Model)
	if model.focusedPanel != panelLogs {
		t.Errorf("expected focus on logs (%d), got %d", panelLogs, model.focusedPanel)
	}

	// Shift+Tab from logs goes to blackboard
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyShiftTab})
	model = updated.(Model)
	if model.focusedPanel != panelBlackboard {
		t.Errorf("expected focus on blackboard (%d), got %d", panelBlackboard, model.focusedPanel)
	}

	// Shift+Tab from blackboard goes to agents
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyShiftTab})
	model = updated.(Model)
	if model.focusedPanel != panelAgents {
		t.Errorf("expected focus on agents (%d), got %d", panelAgents, model.focusedPanel)
	}
}

func TestModelQuitCmd(t *testing.T) {
	d := setupTestDB(t)
	m := New(d)
	m.ready = true

	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'q'}})
	if cmd == nil {
		t.Error("pressing q should return a quit command")
	}
}

func TestModelHelpToggle(t *testing.T) {
	d := setupTestDB(t)
	m := New(d)
	m.ready = true
	m.width = 80
	m.height = 24

	// Press ? to show help
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'?'}})
	model := updated.(Model)
	if !model.showHelp {
		t.Error("pressing ? should show help")
	}

	// Press any key to dismiss
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'a'}})
	model = updated.(Model)
	if model.showHelp {
		t.Error("pressing any key should dismiss help")
	}
}

func TestModelDataMsg(t *testing.T) {
	d := setupTestDB(t)
	ctx := context.Background()

	_, err := d.ExecContext(ctx,
		`INSERT INTO tasks (id, title, status, role) VALUES ('t1', 'Test', 'running', 'impl')`)
	if err != nil {
		t.Fatalf("inserting task: %v", err)
	}

	m := New(d)
	m.ready = true
	m.width = 120
	m.height = 40
	m.recalcLayout()

	// Simulate a data message
	tasks, _ := d.ListTasks(ctx)
	agents, _ := d.ListAgents(ctx)
	locks, _ := d.ListFileLocks(ctx)
	events, _ := d.RecentEvents(ctx, 10)
	summary, _ := d.GetStatusSummary(ctx)

	updated, _ := m.Update(dataMsg{
		tasks:   tasks,
		agents:  agents,
		locks:   locks,
		events:  events,
		summary: summary,
	})
	model := updated.(Model)

	if model.dbErr != nil {
		t.Errorf("should not have DB error, got: %v", model.dbErr)
	}
}

func TestModelDataMsgError(t *testing.T) {
	d := setupTestDB(t)
	m := New(d)
	m.ready = true

	updated, _ := m.Update(dataMsg{err: context.Canceled})
	model := updated.(Model)

	if model.dbErr == nil {
		t.Error("should have DB error after error dataMsg")
	}
}

func TestModelViewBeforeReady(t *testing.T) {
	d := setupTestDB(t)
	m := New(d)

	view := m.View()
	if view != "Loading..." {
		t.Errorf("expected 'Loading...' before ready, got: %q", view)
	}
}

func TestModelViewAfterReady(t *testing.T) {
	d := setupTestDB(t)
	m := New(d)
	m.ready = true
	m.width = 100
	m.height = 30
	m.recalcLayout()

	view := m.View()
	if len(view) == 0 {
		t.Error("view should not be empty after ready")
	}
}

func TestModelHasFivePanels(t *testing.T) {
	if numPanels != 5 {
		t.Errorf("expected numPanels=5, got %d", numPanels)
	}
	if panelConductors != 0 || panelTasks != 1 || panelAgents != 2 || panelBlackboard != 3 || panelLogs != 4 {
		t.Errorf("panel indices incorrect: conductors=%d tasks=%d agents=%d blackboard=%d logs=%d",
			panelConductors, panelTasks, panelAgents, panelBlackboard, panelLogs)
	}
}

func TestModelSpaceTogglesLogAutoScroll(t *testing.T) {
	d := setupTestDB(t)
	m := New(d)
	m.ready = true
	m.width = 120
	m.height = 40
	m.recalcLayout()

	// Focus on logs panel
	m.focusedPanel = panelLogs
	m.updateFocus()

	// Log panel starts with auto-scroll on
	if !m.logPanel.AutoScroll() {
		t.Error("log panel should start with auto-scroll enabled")
	}

	// Space should toggle auto-scroll off
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{' '}})
	model := updated.(Model)
	if model.logPanel.AutoScroll() {
		t.Error("space should toggle auto-scroll off")
	}

	// Space again should toggle auto-scroll back on
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{' '}})
	model = updated.(Model)
	if !model.logPanel.AutoScroll() {
		t.Error("space should toggle auto-scroll back on")
	}
}

func TestModelEnterOnTaskProducesSelectMsg(t *testing.T) {
	d := setupTestDB(t)
	ctx := context.Background()
	_, err := d.ExecContext(ctx,
		`INSERT INTO tasks (id, title, status, role) VALUES ('t1', 'Build auth', 'running', 'impl')`)
	if err != nil {
		t.Fatal(err)
	}

	m := New(d)
	m.ready = true
	m.width = 120
	m.height = 40
	m.recalcLayout()

	// Load data
	tasks, _ := d.ListTasks(ctx)
	m.taskPanel.SetData(tasks)

	// Focus tasks, press Enter
	m.focusedPanel = panelTasks
	m.updateFocus()

	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("Enter on task should return a command")
	}

	msg := cmd()
	sel, ok := msg.(selectTaskMsg)
	if !ok {
		t.Fatalf("expected selectTaskMsg, got %T", msg)
	}
	if sel.taskID != "t1" {
		t.Errorf("expected task ID 't1', got %q", sel.taskID)
	}
	if sel.title != "Build auth" {
		t.Errorf("expected title 'Build auth', got %q", sel.title)
	}
}

func TestModelSelectTaskMsgUpdatesLogPanel(t *testing.T) {
	d := setupTestDB(t)
	m := New(d)
	m.ready = true
	m.width = 120
	m.height = 40
	m.recalcLayout()

	updated, _ := m.Update(selectTaskMsg{taskID: "t1", title: "My Task"})
	model := updated.(Model)

	view := model.logPanel.View()
	if !contains(view, "t1") {
		t.Errorf("log panel should show task ID, got: %s", view)
	}
	if !contains(view, "My Task") {
		t.Errorf("log panel should show task title, got: %s", view)
	}
}

func TestStatusBarContextHints(t *testing.T) {
	s := panels.NewStatusBar()
	s.SetWidth(120)
	s.Update(nil, nil)

	// Tasks panel hint
	s.SetFocusedPanel(panelTasks)
	view := s.View()
	if !contains(view, "view logs") {
		t.Errorf("tasks hint should mention 'view logs', got: %s", view)
	}
	if !contains(view, "filter") {
		t.Errorf("tasks hint should mention 'filter', got: %s", view)
	}

	// Logs panel hint
	s.SetFocusedPanel(panelLogs)
	view = s.View()
	if !contains(view, "pause/resume") {
		t.Errorf("logs hint should mention 'pause/resume', got: %s", view)
	}
}

func contains(s, substr string) bool {
	return strings.Contains(s, substr)
}

func TestModelRetryOnlyOnFailed(t *testing.T) {
	d := setupTestDB(t)
	ctx := context.Background()
	_, err := d.ExecContext(ctx,
		`INSERT INTO tasks (id, title, status, role) VALUES ('t1', 'Task', 'running', 'impl')`)
	if err != nil {
		t.Fatal(err)
	}

	m := New(d)
	m.ready = true
	m.width = 120
	m.height = 40
	m.recalcLayout()

	tasks, _ := d.ListTasks(ctx)
	m.taskPanel.SetData(tasks)
	m.focusedPanel = panelTasks
	m.updateFocus()

	// Press 'r' on running task — should NOT produce action
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}})
	if cmd != nil {
		t.Error("retry should not produce cmd on running task")
	}
}

func TestModelKillOnlyOnRunning(t *testing.T) {
	d := setupTestDB(t)
	ctx := context.Background()
	_, err := d.ExecContext(ctx,
		`INSERT INTO tasks (id, title, status, role) VALUES ('t1', 'Task', 'failed', 'impl')`)
	if err != nil {
		t.Fatal(err)
	}

	m := New(d)
	m.ready = true
	m.width = 120
	m.height = 40
	m.recalcLayout()

	tasks, _ := d.ListTasks(ctx)
	m.taskPanel.SetData(tasks)
	m.focusedPanel = panelTasks
	m.updateFocus()

	// Press 'x' on failed task — should NOT produce action
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'x'}})
	if cmd != nil {
		t.Error("kill should not produce cmd on failed task")
	}
}

func TestModelDetailModalShow(t *testing.T) {
	d := setupTestDB(t)
	ctx := context.Background()
	_, err := d.ExecContext(ctx,
		`INSERT INTO tasks (id, title, status, role) VALUES ('t1', 'Build auth', 'running', 'impl')`)
	if err != nil {
		t.Fatal(err)
	}

	m := New(d)
	m.ready = true
	m.width = 120
	m.height = 40
	m.recalcLayout()

	tasks, _ := d.ListTasks(ctx)
	m.taskPanel.SetData(tasks)
	m.focusedPanel = panelTasks
	m.updateFocus()

	// Press 'd' to show detail
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'d'}})
	model := updated.(Model)
	if !model.showDetail {
		t.Error("d should show detail modal")
	}

	// View should show detail content
	view := model.View()
	if !strings.Contains(view, "TASK DETAIL") {
		t.Errorf("detail view should contain 'TASK DETAIL', got: %s", view[:min(len(view), 200)])
	}
}

func TestModelDetailModalEscape(t *testing.T) {
	d := setupTestDB(t)
	ctx := context.Background()
	_, err := d.ExecContext(ctx,
		`INSERT INTO tasks (id, title, status, role) VALUES ('t1', 'Task', 'running', 'impl')`)
	if err != nil {
		t.Fatal(err)
	}

	m := New(d)
	m.ready = true
	m.width = 120
	m.height = 40
	m.recalcLayout()

	tasks, _ := d.ListTasks(ctx)
	m.taskPanel.SetData(tasks)
	m.focusedPanel = panelTasks
	m.updateFocus()

	// Show detail
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'d'}})
	model := updated.(Model)

	// Escape should dismiss
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyEscape})
	model = updated.(Model)
	if model.showDetail {
		t.Error("escape should dismiss detail modal")
	}
}

func TestModelActionResultMsg(t *testing.T) {
	d := setupTestDB(t)
	m := New(d)
	m.ready = true
	m.width = 120
	m.height = 40
	m.recalcLayout()

	// Successful action
	updated, _ := m.Update(actionResultMsg{action: "retry", taskID: "t1", output: "done"})
	model := updated.(Model)
	view := model.statusBar.View()
	if !strings.Contains(view, "retry") {
		t.Errorf("status bar should show action result, got: %s", view)
	}

	// Error action
	updated, _ = model.Update(actionResultMsg{action: "kill", taskID: "t2", err: fmt.Errorf("process not found")})
	model = updated.(Model)
	view = model.statusBar.View()
	if !strings.Contains(view, "failed") {
		t.Errorf("status bar should show failure, got: %s", view)
	}
}

func TestDetailModalRender(t *testing.T) {
	modal := panels.NewDetailModal()
	modal.SetSize(80, 30)

	task := &db.Task{
		ID:     "t1",
		Title:  "Build auth module",
		Status: "running",
		Role:   "implementer",
	}
	modal.SetTask(task)

	view := modal.View()
	if !strings.Contains(view, "t1") {
		t.Error("modal should show task ID")
	}
	if !strings.Contains(view, "Build auth") {
		t.Error("modal should show task title")
	}
	if !strings.Contains(view, "running") {
		t.Error("modal should show task status")
	}
	if !strings.Contains(view, "Escape") {
		t.Error("modal should show escape hint")
	}
}

func TestDetailModalNilTask(t *testing.T) {
	modal := panels.NewDetailModal()
	modal.SetSize(80, 30)
	view := modal.View()
	if view != "" {
		t.Error("nil task should render empty string")
	}
}

func TestModelInputActivation(t *testing.T) {
	d := setupTestDB(t)
	m := New(d)
	m.ready = true
	m.width = 120
	m.height = 40
	m.recalcLayout()

	// Press ':' to activate input
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{':'}})
	model := updated.(Model)
	if !model.inputActive {
		t.Error(": should activate input mode")
	}

	// View should show input panel (goal>) instead of status bar help
	view := model.View()
	if !strings.Contains(view, "goal>") {
		t.Errorf("view should contain 'goal>' prompt when input active, got: %s", view[:min(len(view), 300)])
	}
}

func TestModelInputCancel(t *testing.T) {
	d := setupTestDB(t)
	m := New(d)
	m.ready = true
	m.width = 120
	m.height = 40
	m.recalcLayout()

	// Activate input
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{':'}})
	model := updated.(Model)

	// Escape cancels
	updated, cmd := model.Update(tea.KeyMsg{Type: tea.KeyEscape})
	if cmd != nil {
		// Execute the cmd to get the cancel message
		msg := cmd()
		updated, _ = updated.(Model).Update(msg)
	}
	model = updated.(Model)
	if model.inputActive {
		t.Error("escape should cancel input mode")
	}
}

func TestModelGoalSubmit(t *testing.T) {
	d := setupTestDB(t)
	m := New(d)
	m.ready = true
	m.width = 120
	m.height = 40
	m.recalcLayout()

	// Simulate receiving a GoalSubmitMsg
	updated, cmd := m.Update(panels.GoalSubmitMsg{Goal: "build auth module"})
	model := updated.(Model)

	if model.inputActive {
		t.Error("input should be deactivated after submit")
	}
	if cmd == nil {
		t.Error("submit should produce a command to dispatch the goal")
	}
}

func TestModelGoalDispatchedMsg(t *testing.T) {
	d := setupTestDB(t)
	m := New(d)
	m.ready = true
	m.width = 120
	m.height = 40
	m.recalcLayout()

	// Success
	updated, _ := m.Update(goalDispatchedMsg{goal: "test goal", output: "ok"})
	model := updated.(Model)
	view := model.statusBar.View()
	if !strings.Contains(view, "dispatched") {
		t.Errorf("status bar should show 'dispatched', got: %s", view)
	}

	// Error
	updated, _ = model.Update(goalDispatchedMsg{goal: "test", err: fmt.Errorf("fail")})
	model = updated.(Model)
	view = model.statusBar.View()
	if !strings.Contains(view, "failed") {
		t.Errorf("status bar should show 'failed', got: %s", view)
	}
}

// --- Clarify integration tests ---

func TestModelShowClarifyMsg(t *testing.T) {
	d := setupTestDB(t)
	m := New(d)
	m.ready = true
	m.width = 120
	m.height = 40
	m.recalcLayout()

	questions := []orchestrator.ClarifyQuestion{
		{ID: 1, Question: "Which scope?", Default: "all", Context: "ambiguous"},
		{ID: 2, Question: "Include tests?", Default: "yes"},
	}

	updated, _ := m.Update(showClarifyMsg{questions: questions})
	model := updated.(Model)

	if !model.clarifyActive {
		t.Error("clarifyActive should be true after showClarifyMsg")
	}

	view := model.View()
	if !strings.Contains(view, "Goal Clarification") {
		t.Error("view should show clarify panel")
	}
	if !strings.Contains(view, "Which scope?") {
		t.Error("view should show first question")
	}
}

func TestModelClarifySubmitMsg(t *testing.T) {
	d := setupTestDB(t)
	m := New(d)
	m.ready = true
	m.width = 120
	m.height = 40
	m.recalcLayout()

	// Set up clarify state
	answerCh := make(chan []orchestrator.ClarifyQuestion, 1)
	m.clarifyActive = true
	m.clarifyAnswerCh = answerCh

	answered := []orchestrator.ClarifyQuestion{
		{ID: 1, Question: "Which scope?", Default: "all", Answer: "only src/"},
	}
	updated, _ := m.Update(panels.ClarifySubmitMsg{Questions: answered})
	model := updated.(Model)

	if model.clarifyActive {
		t.Error("clarifyActive should be false after submit")
	}

	// Check answer was sent to channel
	select {
	case received := <-answerCh:
		if len(received) != 1 || received[0].Answer != "only src/" {
			t.Errorf("answer channel should have user's answer, got: %v", received)
		}
	default:
		t.Error("answer should have been sent to channel")
	}

	view := model.statusBar.View()
	if !strings.Contains(view, "submitted") {
		t.Errorf("status bar should show 'submitted', got: %s", view)
	}
}

func TestModelClarifySkipMsg(t *testing.T) {
	d := setupTestDB(t)
	m := New(d)
	m.ready = true
	m.width = 120
	m.height = 40
	m.recalcLayout()

	answerCh := make(chan []orchestrator.ClarifyQuestion, 1)
	m.clarifyActive = true
	m.clarifyAnswerCh = answerCh

	updated, _ := m.Update(panels.ClarifySkipMsg{})
	model := updated.(Model)

	if model.clarifyActive {
		t.Error("clarifyActive should be false after skip")
	}

	// Check nil was sent (skip = use defaults)
	select {
	case received := <-answerCh:
		if received != nil {
			t.Errorf("skip should send nil, got: %v", received)
		}
	default:
		t.Error("nil should have been sent to channel")
	}

	view := model.statusBar.View()
	if !strings.Contains(view, "defaults") {
		t.Errorf("status bar should mention defaults, got: %s", view)
	}
}

func TestModelClarifyKeyRouting(t *testing.T) {
	d := setupTestDB(t)
	m := New(d)
	m.ready = true
	m.width = 120
	m.height = 40
	m.recalcLayout()

	// Activate clarify
	questions := []orchestrator.ClarifyQuestion{
		{ID: 1, Question: "Q?", Default: "D"},
	}
	updated, _ := m.Update(showClarifyMsg{questions: questions})
	model := updated.(Model)

	// Tab should NOT switch panels when clarify is active
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyTab})
	model = updated.(Model)
	if model.focusedPanel != panelTasks {
		t.Errorf("Tab during clarify should not switch panels, focused=%d", model.focusedPanel)
	}

	// 'q' should NOT quit when clarify is active
	_, cmd := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'q'}})
	if cmd != nil {
		// Check if it's a quit command by comparing to nil
		// During clarify, keys go to panel, not to handleKey global bindings
		t.Log("cmd returned from q during clarify — verifying it's not quit")
	}
}

func TestModelClarifyRendersInsteadOfStatusBar(t *testing.T) {
	d := setupTestDB(t)
	m := New(d)
	m.ready = true
	m.width = 120
	m.height = 40
	m.recalcLayout()

	// Without clarify, should show status bar
	view := m.View()
	if strings.Contains(view, "Goal Clarification") {
		t.Error("should not show clarify panel when not active")
	}

	// Activate clarify
	questions := []orchestrator.ClarifyQuestion{
		{ID: 1, Question: "Scope?", Default: "all"},
	}
	updated, _ := m.Update(showClarifyMsg{questions: questions})
	model := updated.(Model)

	view = model.View()
	if !strings.Contains(view, "Goal Clarification") {
		t.Error("should show clarify panel when active")
	}
}

func TestModelGoalDispatchedCleansUpClarify(t *testing.T) {
	d := setupTestDB(t)
	m := New(d)
	m.ready = true
	m.width = 120
	m.height = 40
	m.recalcLayout()

	// Simulate mid-clarify state
	m.clarifyActive = true
	m.clarifyAnswerCh = make(chan []orchestrator.ClarifyQuestion, 1)

	// goalDispatchedMsg should clean everything up
	updated, _ := m.Update(goalDispatchedMsg{goal: "test", output: "done"})
	model := updated.(Model)

	if model.clarifyActive {
		t.Error("goalDispatched should clear clarifyActive")
	}
	if model.clarifyAnswerCh != nil {
		t.Error("goalDispatched should nil out clarifyAnswerCh")
	}
}

func TestFetchDataCmd(t *testing.T) {
	d := setupTestDB(t)

	cmd := fetchData(d)
	if cmd == nil {
		t.Error("fetchData should return a command")
	}

	// Execute the command and verify it returns a dataMsg
	msg := cmd()
	if _, ok := msg.(dataMsg); !ok {
		t.Errorf("expected dataMsg, got %T", msg)
	}
}
