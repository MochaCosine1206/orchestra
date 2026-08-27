package tui

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/MochaCosine1206/orchestra/internal/db"
	"github.com/MochaCosine1206/orchestra/internal/delegate"
	"github.com/MochaCosine1206/orchestra/internal/monitor"
	"github.com/MochaCosine1206/orchestra/internal/orchestrator"
	"github.com/MochaCosine1206/orchestra/internal/tui/logstream"
	"github.com/MochaCosine1206/orchestra/internal/tui/panels"
	"github.com/MochaCosine1206/orchestra/internal/version"
)

const (
	panelConductors = 0
	panelTasks      = 1
	panelAgents     = 2
	panelBlackboard = 3
	panelLogs       = 4
	numPanels       = 5

	pollInterval = 2 * time.Second
)

// Model is the root Bubble Tea model for the orchestra dashboard.
type Model struct {
	db *db.DB

	// Global mode: when true, delegates to GlobalModel for multi-project view.
	GlobalMode  bool
	globalModel GlobalModel

	// Panels
	conductorPanel  *panels.ConductorPanel
	taskPanel       *panels.TaskPanel
	agentPanel      *panels.AgentPanel
	blackboardPanel *panels.BlackboardPanel
	logPanel        *panels.LogPanel
	statusBar       *panels.StatusBar

	// Log streaming
	streamer *logstream.Streamer

	// Delegation
	delegate *delegate.Delegate

	// Monitor (optional background agent lifecycle manager)
	monitor *monitor.Monitor

	// Detail modal
	detailModal *panels.DetailModal
	showDetail  bool

	// Goal input
	inputPanel  *panels.InputPanel
	inputActive bool

	// Clarify panel
	clarifyPanel    *panels.ClarifyPanel
	clarifyActive   bool
	clarifyAnswerCh chan []orchestrator.ClarifyQuestion

	// Toasts
	toastManager *panels.ToastManager
	lastEventID  int64

	// Goal dispatch
	goalCancel   context.CancelFunc
	goalProgress chan goalProgressMsg

	// Conductor observability
	conductorLogCh    <-chan string
	lastConductorEvID int64
	sessionTaskIDs    []string // task IDs for the current session (for DB event polling)

	// Session tracking
	currentSessionID string

	// State
	focusedPanel int
	width        int
	height       int
	dbErr        error
	showHelp     bool
	ready        bool
}

// New creates a new TUI model connected to the given database.
func New(d *db.DB) Model {
	return Model{
		db:              d,
		conductorPanel:  panels.NewConductorPanel(),
		taskPanel:       panels.NewTaskPanel(),
		agentPanel:      panels.NewAgentPanel(),
		blackboardPanel: panels.NewBlackboardPanel(),
		logPanel:        panels.NewLogPanel(),
		statusBar:       panels.NewStatusBar(),
		streamer:        logstream.New(),
		delegate:        delegate.New(d),
		detailModal:     panels.NewDetailModal(),
		inputPanel:      panels.NewInputPanel(),
		clarifyPanel:    panels.NewClarifyPanel(),
		toastManager:    panels.NewToastManager(),
		focusedPanel:    panelTasks, // default focus on tasks panel
	}
}

// NewWithConductor creates a TUI model with a fully wired conductor for goal dispatch.
func NewWithConductor(d *db.DB, conductor *orchestrator.Conductor) Model {
	ip := panels.NewInputPanel()
	ip.SetRepoRoot(conductor.RepoRoot)
	ap := panels.NewAgentPanel()
	ap.SetLogsDir(filepath.Join(conductor.RepoRoot, ".orchestra", "logs"))

	// Create conductor log channel and wire it
	conductorCh := make(chan string, 64)
	conductor.ConductorLogCh = conductorCh

	lp := panels.NewLogPanel()
	lp.SetMode(panels.LogModeConductor) // start in conductor mode

	return Model{
		db:              d,
		conductorPanel:  panels.NewConductorPanel(),
		taskPanel:       panels.NewTaskPanel(),
		agentPanel:      ap,
		blackboardPanel: panels.NewBlackboardPanel(),
		logPanel:        lp,
		statusBar:       panels.NewStatusBar(),
		streamer:        logstream.New(),
		delegate:        delegate.NewFull(d, conductor.Spawner, conductor),
		detailModal:     panels.NewDetailModal(),
		inputPanel:      ip,
		clarifyPanel:    panels.NewClarifyPanel(),
		toastManager:    panels.NewToastManager(),
		conductorLogCh:  conductorCh,
		focusedPanel:    panelTasks,
	}
}

// NewGlobal creates a Model that delegates entirely to the global multi-project dashboard.
func NewGlobal(gm GlobalModel) Model {
	return Model{
		GlobalMode:  true,
		globalModel: gm,
	}
}

// SetMonitor attaches a background monitor to the TUI model.
func (m *Model) SetMonitor(mon *monitor.Monitor) {
	m.monitor = mon
}

// Init starts the initial data fetch, polling timer, log streamer, and monitor.
func (m Model) Init() tea.Cmd {
	if m.GlobalMode {
		return m.globalModel.Init()
	}
	ctx := context.Background()
	m.streamer.Start(ctx)
	if m.monitor != nil {
		m.monitor.Start(ctx)
	}
	cmds := []tea.Cmd{
		fetchData(m.db),
		tea.Tick(pollInterval, func(time.Time) tea.Msg { return tickMsg{} }),
		tea.Tick(5*time.Second, func(time.Time) tea.Msg { return sessionCheckTickMsg{} }),
		waitForLogLines(m.streamer),
		checkActiveSession(m.db),
	}
	if m.conductorLogCh != nil {
		cmds = append(cmds, waitForConductorLog(m.conductorLogCh))
	}
	return tea.Batch(cmds...)
}

// waitForLogLines returns a tea.Cmd that blocks on the streamer's Lines channel,
// parses each line into a LogEntry, and sends them as a logLinesMsg.
func waitForLogLines(s *logstream.Streamer) tea.Cmd {
	return func() tea.Msg {
		lines, ok := <-s.Lines
		if !ok {
			return nil
		}
		entries := make([]logstream.LogEntry, len(lines))
		for i, line := range lines {
			entries[i] = logstream.ParseLine(line)
		}
		return logLinesMsg{entries: entries}
	}
}

// Update handles messages and returns updated model + commands.
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if m.GlobalMode {
		gm, cmd := m.globalModel.Update(msg)
		m.globalModel = gm.(GlobalModel)
		return m, cmd
	}

	var cmds []tea.Cmd

	switch msg := msg.(type) {

	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.ready = true
		m.recalcLayout()
		return m, nil

	case tickMsg:
		m.toastManager.Tick()
		cmds := []tea.Cmd{
			fetchData(m.db),
			tea.Tick(pollInterval, func(time.Time) tea.Msg { return tickMsg{} }),
		}
		// Always poll DB events for conductor timeline — covers both detached sessions
		// (external orchestra go) and in-process dispatch. The channel provides real-time
		// logs; DB polling provides structured events from any conductor process.
		cmds = append(cmds, fetchConductorEvents(m.db, m.lastConductorEvID, m.sessionTaskIDs))
		return m, tea.Batch(cmds...)

	case sessionCheckTickMsg:
		return m, tea.Batch(
			checkSession(m.db),
			tea.Tick(5*time.Second, func(time.Time) tea.Msg { return sessionCheckTickMsg{} }),
		)

	case sessionCheckMsg:
		if msg.sessionID != "" && msg.sessionID != m.currentSessionID {
			m.currentSessionID = msg.sessionID
			m.logPanel.Clear()
			m.toastManager.Add(fmt.Sprintf("New session detected: %s", msg.sessionID), panels.ToastInfo)
			return m, fetchData(m.db)
		}
		return m, nil

	case dataMsg:
		if msg.err != nil {
			m.dbErr = msg.err
			m.toastManager.Add(fmt.Sprintf("DB error: %v", msg.err), panels.ToastError)
		} else {
			m.dbErr = nil
			m.conductorPanel.SetData(msg.conductors)

			// Compute task counts per conductor for the panel
			conductorCounts := make(map[string][2]int)
			for _, c := range msg.conductors {
				var done, total int
				for _, t := range msg.tasks {
					if t.ConductorID.Valid && t.ConductorID.String == c.ID {
						total++
						if t.Status == "done" {
							done++
						}
					}
				}
				conductorCounts[c.ID] = [2]int{done, total}
			}
			m.conductorPanel.SetTaskCounts(conductorCounts)

			// Filter tasks/agents if a conductor is selected
			filteredTasks := msg.tasks
			if selID := m.conductorPanel.SelectedID(); selID != "" {
				var ft []db.Task
				for _, t := range msg.tasks {
					if t.ConductorID.Valid && t.ConductorID.String == selID {
						ft = append(ft, t)
					}
				}
				filteredTasks = ft
			}
			m.taskPanel.SetData(filteredTasks)
			m.agentPanel.SetData(msg.agents)
			m.blackboardPanel.SetData(msg.blackboard)
			m.statusBar.Update(msg.summary, nil)

			// Create toasts for new events
			for _, e := range msg.events {
				if e.ID > m.lastEventID {
					toastMsg := e.EventType
					if e.TaskID.Valid {
						toastMsg += " " + e.TaskID.String
					}
					if e.AgentID.Valid {
						toastMsg += " (" + e.AgentID.String + ")"
					}
					m.toastManager.Add(toastMsg, panels.ToastTypeForEvent(e.EventType))
				}
			}
			if len(msg.events) > 0 {
				m.lastEventID = msg.events[0].ID // events sorted by ID desc
			}
		}
		if m.dbErr != nil {
			m.statusBar.Update(nil, m.dbErr)
		}
		return m, nil

	case logLinesMsg:
		m.logPanel.AppendEntries(msg.entries)
		return m, waitForLogLines(m.streamer)

	case selectTaskMsg:
		m.logPanel.SetMode(panels.LogModeAgent)
		m.logPanel.SetTask(msg.taskID, msg.title)
		logPath := fmt.Sprintf(".orchestra/logs/%s.jsonl", msg.taskID)
		m.streamer.SwitchFile(logPath)
		return m, nil

	case actionResultMsg:
		if msg.err != nil {
			m.statusBar.SetActionMsg(fmt.Sprintf("%s %s failed: %v", msg.action, msg.taskID, msg.err))
		} else {
			m.statusBar.SetActionMsg(fmt.Sprintf("%s %s: %s", msg.action, msg.taskID, truncateOutput(msg.output, 60)))
		}
		return m, nil

	case showDetailMsg:
		if t := m.taskPanel.SelectedTask(); t != nil {
			m.detailModal.SetTask(t)
			m.detailModal.SetSize(m.width, m.height)
			m.showDetail = true
		}
		return m, nil

	case hideDetailMsg:
		m.showDetail = false
		return m, nil

	case panels.GoalSubmitMsg:
		if m.goalCancel != nil {
			m.inputActive = false
			m.toastManager.Add("Goal already in progress", panels.ToastError)
			return m, nil
		}
		m.inputActive = false
		m.statusBar.SetActionMsg(fmt.Sprintf("Dispatching goal: %s", truncateOutput(msg.Goal, 50)))
		goal := msg.Goal
		d := m.delegate

		// Create cancellable context so quitting kills the decompose process
		ctx, cancel := context.WithCancel(context.Background())
		m.goalCancel = cancel

		// Create progress channel and wire conductor's ProgressFunc
		progressCh := make(chan goalProgressMsg, 8)
		m.goalProgress = progressCh

		// Create clarify channels for TUI interactive mode
		questionCh := make(chan []orchestrator.ClarifyQuestion, 1)
		answerCh := make(chan []orchestrator.ClarifyQuestion, 1)
		m.clarifyAnswerCh = answerCh

		if d.Conductor != nil {
			// Suppress stderr logging inside TUI — conductor log channel handles UI delivery
			d.Conductor.Log = nil
			d.Conductor.ProgressFunc = func(phase, detail string) {
				select {
				case progressCh <- goalProgressMsg{phase: phase, detail: detail}:
				default:
				}
			}
			// Wire clarify channels for TUI mode
			d.Conductor.ClarifyQuestionCh = questionCh
			d.Conductor.ClarifyAnswerCh = answerCh
		}

		return m, tea.Batch(
			func() tea.Msg {
				result := d.OrchestraGoWithClarify(ctx, goal)
				close(progressCh)
				close(questionCh)
				return goalDispatchedMsg{goal: goal, output: result.Output, err: result.Err}
			},
			waitForProgress(progressCh),
			waitForClarify(questionCh),
		)

	case panels.InputCancelMsg:
		m.inputActive = false
		return m, nil

	case showClarifyMsg:
		m.clarifyActive = true
		m.clarifyPanel.SetQuestions(msg.questions)
		m.clarifyPanel.SetSize(m.width, m.height/2)
		m.clarifyPanel.Focus()
		m.statusBar.SetActionMsg(fmt.Sprintf("Clarify: %d questions — answer to continue", len(msg.questions)))
		return m, nil

	case panels.ClarifySubmitMsg:
		m.clarifyActive = false
		m.clarifyPanel.Blur()
		if m.clarifyAnswerCh != nil {
			m.clarifyAnswerCh <- msg.Questions
		}
		m.statusBar.SetActionMsg("Clarifications submitted, decomposing...")
		return m, nil

	case panels.ClarifySkipMsg:
		m.clarifyActive = false
		m.clarifyPanel.Blur()
		// Send original questions with empty answers — Clarify() will use defaults
		if m.clarifyAnswerCh != nil {
			m.clarifyAnswerCh <- nil
		}
		m.statusBar.SetActionMsg("Clarification skipped, using defaults...")
		return m, nil

	case goalDispatchedMsg:
		m.clarifyActive = false
		m.clarifyAnswerCh = nil
		m.goalCancel = nil
		m.goalProgress = nil
		if d := m.delegate; d != nil && d.Conductor != nil {
			d.Conductor.ProgressFunc = nil
			d.Conductor.ClarifyQuestionCh = nil
			d.Conductor.ClarifyAnswerCh = nil
		}
		if msg.err != nil {
			if strings.Contains(msg.err.Error(), "context canceled") {
				m.statusBar.SetActionMsg("Goal cancelled")
			} else {
				m.statusBar.SetActionMsg(fmt.Sprintf("Goal failed: %v", msg.err))
			}
		} else {
			m.statusBar.SetActionMsg(fmt.Sprintf("Goal dispatched: %s", truncateOutput(msg.goal, 40)))
		}
		return m, nil

	case goalProgressMsg:
		m.statusBar.SetActionMsg(fmt.Sprintf("[%s] %s", msg.phase, msg.detail))
		// Re-subscribe for next progress message
		if m.goalProgress != nil {
			return m, waitForProgress(m.goalProgress)
		}
		return m, nil

	case conductorLogMsg:
		m.logPanel.AppendConductorLog(msg.text)
		// Re-subscribe for next conductor log message
		if m.conductorLogCh != nil {
			return m, waitForConductorLog(m.conductorLogCh)
		}
		return m, nil

	case conductorEventsMsg:
		m.logPanel.AppendConductorEvents(msg.events)
		if len(msg.events) > 0 {
			m.lastConductorEvID = msg.events[len(msg.events)-1].ID
		}
		return m, nil

	case activeSessionMsg:
		if msg.sessionID != "" {
			m.currentSessionID = msg.sessionID
			m.sessionTaskIDs = msg.taskIDs
			detail := fmt.Sprintf("Active session: %s", msg.sessionID)
			if msg.goal != "" {
				detail += fmt.Sprintf(" — %s", truncateOutput(msg.goal, 40))
			}
			m.statusBar.SetActionMsg(detail)
			m.toastManager.Add(fmt.Sprintf("Resumed session %s", msg.sessionID), panels.ToastInfo)
		}
		return m, nil

	case focusPanelMsg:
		m.focusedPanel = int(msg) % numPanels
		m.updateFocus()
		return m, nil

	case tea.KeyMsg:
		return m.handleKey(msg)
	}

	// Broadcast to all panels
	if cmd := m.taskPanel.Update(msg); cmd != nil {
		cmds = append(cmds, cmd)
	}
	if cmd := m.agentPanel.Update(msg); cmd != nil {
		cmds = append(cmds, cmd)
	}
	if cmd := m.blackboardPanel.Update(msg); cmd != nil {
		cmds = append(cmds, cmd)
	}
	if cmd := m.logPanel.Update(msg); cmd != nil {
		cmds = append(cmds, cmd)
	}

	return m, tea.Batch(cmds...)
}

func (m Model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	// Clarify mode: route all keys to clarify panel
	if m.clarifyActive {
		cmd := m.clarifyPanel.Update(msg)
		return m, cmd
	}

	// Input mode: route all keys to input panel
	if m.inputActive {
		cmd := m.inputPanel.Update(msg)
		return m, cmd
	}

	// Filter mode: route all keys to task panel
	if m.taskPanel.FilterActive() {
		cmd := m.taskPanel.Update(msg)
		return m, cmd
	}

	// Detail modal: Escape dismisses
	if m.showDetail {
		if key.Matches(msg, keys.Escape) {
			m.showDetail = false
		}
		return m, nil
	}

	// Help overlay toggle
	if key.Matches(msg, keys.Help) {
		m.showHelp = !m.showHelp
		return m, nil
	}

	// If help overlay is shown, any key dismisses it
	if m.showHelp {
		m.showHelp = false
		return m, nil
	}

	switch {
	case key.Matches(msg, keys.Quit):
		if m.goalCancel != nil {
			m.goalCancel()
		}
		if m.monitor != nil {
			m.monitor.Stop()
		}
		return m, tea.Quit

	case key.Matches(msg, keys.Tab):
		m.focusedPanel = (m.focusedPanel + 1) % numPanels
		m.updateFocus()

	case key.Matches(msg, keys.ShiftTab):
		m.focusedPanel = (m.focusedPanel - 1 + numPanels) % numPanels
		m.updateFocus()

	case key.Matches(msg, keys.Blackboard):
		m.focusedPanel = panelBlackboard
		m.updateFocus()

	case key.Matches(msg, keys.Up):
		switch m.focusedPanel {
		case panelConductors:
			m.conductorPanel.MoveUp()
		case panelTasks:
			m.taskPanel.MoveUp()
		case panelAgents:
			m.agentPanel.MoveUp()
		case panelBlackboard:
			m.blackboardPanel.MoveUp()
		case panelLogs:
			if m.logPanel.Mode() == panels.LogModeConductor {
				m.logPanel.ScrollUpConductor()
			} else {
				m.logPanel.CursorUp()
			}
		}

	case key.Matches(msg, keys.Down):
		switch m.focusedPanel {
		case panelConductors:
			m.conductorPanel.MoveDown()
		case panelTasks:
			m.taskPanel.MoveDown()
		case panelAgents:
			m.agentPanel.MoveDown()
		case panelBlackboard:
			m.blackboardPanel.MoveDown()
		case panelLogs:
			if m.logPanel.Mode() == panels.LogModeConductor {
				m.logPanel.ScrollDownConductor()
			} else {
				m.logPanel.CursorDown()
			}
		}

	case key.Matches(msg, keys.Top):
		switch m.focusedPanel {
		case panelTasks:
			m.taskPanel.GoTop()
		case panelAgents:
			m.agentPanel.GoTop()
		case panelBlackboard:
			m.blackboardPanel.GoTop()
		}

	case key.Matches(msg, keys.Bottom):
		switch m.focusedPanel {
		case panelTasks:
			m.taskPanel.GoBottom()
		case panelAgents:
			m.agentPanel.GoBottom()
		case panelBlackboard:
			m.blackboardPanel.GoBottom()
		}

	case key.Matches(msg, keys.Enter):
		switch m.focusedPanel {
		case panelConductors:
			m.conductorPanel.Select()
			return m, fetchData(m.db) // refresh to apply filter
		case panelTasks:
			if t := m.taskPanel.SelectedTask(); t != nil {
				return m, func() tea.Msg {
					return selectTaskMsg{taskID: t.ID, title: t.Title}
				}
			}
		case panelAgents:
			if a := m.agentPanel.SelectedAgent(); a != nil && a.CurrentTask.Valid {
				return m, func() tea.Msg {
					return selectTaskMsg{taskID: a.CurrentTask.String, title: a.Role + " agent"}
				}
			}
		case panelLogs:
			m.logPanel.ToggleExpand()
		}

	case key.Matches(msg, keys.Space):
		if m.focusedPanel == panelLogs {
			m.logPanel.ToggleAutoScroll()
		}

	case key.Matches(msg, keys.Retry):
		if m.focusedPanel == panelTasks {
			if t := m.taskPanel.SelectedTask(); t != nil && t.Status == "failed" {
				m.statusBar.SetActionMsg(fmt.Sprintf("Retrying task %s...", t.ID))
				taskID := t.ID
				d := m.delegate
				return m, func() tea.Msg {
					result := d.RetryTask(context.Background(), taskID)
					return actionResultMsg{action: "retry", taskID: taskID, output: result.Output, err: result.Err}
				}
			}
		}

	case key.Matches(msg, keys.Kill):
		if m.focusedPanel == panelTasks {
			if t := m.taskPanel.SelectedTask(); t != nil && (t.Status == "running" || t.Status == "assigned") {
				m.statusBar.SetActionMsg(fmt.Sprintf("Killing task %s...", t.ID))
				taskID := t.ID
				d := m.delegate
				return m, func() tea.Msg {
					result := d.KillTask(context.Background(), taskID)
					return actionResultMsg{action: "kill", taskID: taskID, output: result.Output, err: result.Err}
				}
			}
		}

	case key.Matches(msg, keys.Detail):
		if m.focusedPanel == panelTasks {
			if t := m.taskPanel.SelectedTask(); t != nil {
				m.detailModal.SetTask(t)
				m.detailModal.SetSize(m.width, m.height)
				m.showDetail = true
			}
		}

	case key.Matches(msg, keys.Filter):
		if m.focusedPanel == panelTasks {
			m.taskPanel.ActivateFilter()
		}
		return m, nil

	case key.Matches(msg, keys.Input):
		m.inputActive = true
		m.inputPanel.SetWidth(m.width)
		m.inputPanel.Focus()
		return m, nil

	case key.Matches(msg, keys.Escape):
		if m.focusedPanel == panelConductors && m.conductorPanel.SelectedID() != "" {
			m.conductorPanel.Deselect()
			return m, fetchData(m.db) // refresh to remove filter
		}
		m.statusBar.ClearActionMsg()

	default:
		// 'f' cycles blackboard filter when focused
		if msg.String() == "f" && m.focusedPanel == panelBlackboard {
			m.blackboardPanel.CycleFilter()
		}
		// 'c' toggles conductor/agent log mode when log panel focused
		if msg.String() == "c" && m.focusedPanel == panelLogs {
			if m.logPanel.Mode() == panels.LogModeConductor {
				m.logPanel.SetMode(panels.LogModeAgent)
			} else {
				m.logPanel.SetMode(panels.LogModeConductor)
			}
		}
	}

	return m, nil
}

func (m *Model) updateFocus() {
	m.conductorPanel.SetFocused(m.focusedPanel == panelConductors)
	m.taskPanel.SetFocused(m.focusedPanel == panelTasks)
	m.agentPanel.SetFocused(m.focusedPanel == panelAgents)
	m.blackboardPanel.SetFocused(m.focusedPanel == panelBlackboard)
	m.logPanel.SetFocused(m.focusedPanel == panelLogs)
	m.statusBar.SetFocusedPanel(m.focusedPanel)
}

func (m *Model) recalcLayout() {
	// Status bar takes 2 rows, title takes 1 row
	contentHeight := m.height - 3 // title(1) + status bar(2)
	if contentHeight < 4 {
		contentHeight = 4
	}

	// Left panel (tasks): 45%, right column: 55%
	leftWidth := m.width * 45 / 100
	rightWidth := m.width - leftWidth
	if leftWidth < 20 {
		leftWidth = 20
	}
	if rightWidth < 20 {
		rightWidth = 20
	}

	// Right column split: agents (30%), blackboard (30%), logs (40%)
	agentHeight := contentHeight * 30 / 100
	bbHeight := contentHeight * 30 / 100
	logHeight := contentHeight - agentHeight - bbHeight
	if agentHeight < 4 {
		agentHeight = 4
	}
	if bbHeight < 4 {
		bbHeight = 4
	}
	if logHeight < 4 {
		logHeight = 4
	}

	// Subtract border space (2 per panel for rounded border)
	m.taskPanel.SetSize(leftWidth-2, contentHeight-2)
	m.agentPanel.SetSize(rightWidth-2, agentHeight-2)
	m.blackboardPanel.SetSize(rightWidth-2, bbHeight-2)
	m.logPanel.SetSize(rightWidth-2, logHeight-2)
	m.statusBar.SetWidth(m.width)
}

// View renders the full TUI.
func (m Model) View() string {
	if m.GlobalMode {
		return m.globalModel.View()
	}

	if !m.ready {
		return "Loading..."
	}

	if m.showDetail {
		return m.detailModal.View()
	}

	if m.showHelp {
		return m.helpView()
	}

	var b strings.Builder

	// Title bar
	title := fmt.Sprintf("  CLAUDE ORCHESTRA %s", version.Short())
	titleRendered := titleStyle.Width(m.width).Render(title)
	b.WriteString(titleRendered)
	b.WriteString("\n")

	// Layout dimensions
	leftWidth := m.width * 45 / 100
	rightWidth := m.width - leftWidth
	if leftWidth < 20 {
		leftWidth = 20
	}
	if rightWidth < 20 {
		rightWidth = 20
	}

	// Reserve space for autocomplete popup when active
	acHeight := 0
	if m.inputActive {
		acHeight = m.inputPanel.PopupHeight()
	}

	contentHeight := m.height - 3 - acHeight
	if contentHeight < 4 {
		contentHeight = 4
	}

	// Conductor panel height (compact: only shown if conductors exist)
	conductorHeight := 0
	hasConductors := len(m.conductorPanel.View()) > 30 // has content beyond title
	if hasConductors {
		conductorHeight = contentHeight * 15 / 100
		if conductorHeight < 4 {
			conductorHeight = 4
		}
		if conductorHeight > 8 {
			conductorHeight = 8
		}
	}

	remainingRight := contentHeight - conductorHeight
	agentHeight := remainingRight * 30 / 100
	bbHeight := remainingRight * 30 / 100
	logHeight := remainingRight - agentHeight - bbHeight
	if agentHeight < 4 {
		agentHeight = 4
	}
	if bbHeight < 4 {
		bbHeight = 4
	}
	if logHeight < 4 {
		logHeight = 4
	}

	// Left panel: full height tasks
	leftStyle := panelStyle.Width(leftWidth - 2).Height(contentHeight - 2).MaxHeight(contentHeight)
	if m.focusedPanel == panelTasks {
		leftStyle = focusedPanelStyle.Width(leftWidth - 2).Height(contentHeight - 2).MaxHeight(contentHeight)
	}

	// Right: conductor panel (if active)
	var conductorPanel string
	if hasConductors {
		condStyle := panelStyle.Width(rightWidth - 2).Height(conductorHeight - 2).MaxHeight(conductorHeight)
		if m.focusedPanel == panelConductors {
			condStyle = focusedPanelStyle.Width(rightWidth - 2).Height(conductorHeight - 2).MaxHeight(conductorHeight)
		}
		conductorPanel = condStyle.Render(m.conductorPanel.View())
	}

	// Right top: agents
	agentStyle := panelStyle.Width(rightWidth - 2).Height(agentHeight - 2).MaxHeight(agentHeight)
	if m.focusedPanel == panelAgents {
		agentStyle = focusedPanelStyle.Width(rightWidth - 2).Height(agentHeight - 2).MaxHeight(agentHeight)
	}

	// Right middle: blackboard
	bbStyle := panelStyle.Width(rightWidth - 2).Height(bbHeight - 2).MaxHeight(bbHeight)
	if m.focusedPanel == panelBlackboard {
		bbStyle = focusedPanelStyle.Width(rightWidth - 2).Height(bbHeight - 2).MaxHeight(bbHeight)
	}

	// Right bottom: logs
	logStyle := panelStyle.Width(rightWidth - 2).Height(logHeight - 2).MaxHeight(logHeight)
	if m.focusedPanel == panelLogs {
		logStyle = focusedPanelStyle.Width(rightWidth - 2).Height(logHeight - 2).MaxHeight(logHeight)
	}

	leftPanel := leftStyle.Render(m.taskPanel.View())
	agentPanel := agentStyle.Render(m.agentPanel.View())
	bbPanel := bbStyle.Render(m.blackboardPanel.View())
	logPanel := logStyle.Render(m.logPanel.View())

	// Right column: conductors (optional), agents, blackboard, logs stacked vertically
	var rightParts []string
	if conductorPanel != "" {
		rightParts = append(rightParts, conductorPanel)
	}
	rightParts = append(rightParts, agentPanel, bbPanel, logPanel)
	rightColumn := lipgloss.JoinVertical(lipgloss.Left, rightParts...)

	mainContent := lipgloss.JoinHorizontal(lipgloss.Top, leftPanel, rightColumn)

	// Toast overlay (rendered on top of main content)
	toastView := m.toastManager.View(40)
	if toastView != "" {
		// Position toasts at top-right using lipgloss
		toastBlock := lipgloss.NewStyle().
			Width(40).
			Align(lipgloss.Right).
			Render(toastView)

		// Place toast at right side of screen
		toastPlaced := lipgloss.PlaceHorizontal(m.width, lipgloss.Right, toastBlock)
		b.WriteString(toastPlaced)
		// Re-render main content below if toasts take space,
		// but for simplicity, just overlay by printing both
		b.WriteString(mainContent)
	} else {
		b.WriteString(mainContent)
	}
	b.WriteString("\n")

	// Status bar, input line, or clarify panel
	if m.clarifyActive {
		b.WriteString(m.clarifyPanel.View())
	} else if m.inputActive {
		b.WriteString(m.inputPanel.View())
	} else {
		b.WriteString(m.statusBar.View())
	}

	return b.String()
}

func truncateOutput(s string, maxLen int) string {
	s = strings.TrimSpace(s)
	s = strings.ReplaceAll(s, "\n", " ")
	if len(s) > maxLen {
		return s[:maxLen-3] + "..."
	}
	return s
}

func (m Model) helpView() string {
	helpContent := `
  CLAUDE ORCHESTRA — Keyboard Reference

  Navigation
  ──────────────────────────────────
  Tab / Shift+Tab    Switch panels
  j / ↓              Move down
  k / ↑              Move up
  g                  Go to top
  G                  Go to bottom
  Enter              Select / expand tool call
  Space              Pause/resume log scroll

  Actions (Tasks panel)
  ──────────────────────────────────
  r                  Retry failed task
  x                  Kill running task
  d                  Task detail modal
  /                  Filter tasks
  :                  Goal input
  b                  Blackboard panel
  c                  Toggle conductor/agent logs
  f                  Cycle blackboard filter

  Goal Input (: mode)
  ──────────────────────────────────
  @                  File autocomplete
  ↑ / ↓              Navigate suggestions
  Tab / Enter        Accept suggestion
  Esc                Dismiss autocomplete
  Enter              Submit goal (when no popup)

  General
  ──────────────────────────────────
  Escape             Close modal / clear
  ?                  Toggle this help
  q / Ctrl+C         Quit

  Press any key to dismiss...
`

	style := lipgloss.NewStyle().
		Width(m.width).
		Height(m.height).
		Align(lipgloss.Center, lipgloss.Center).
		Foreground(lipgloss.Color("#FFFFFF")).
		Background(lipgloss.Color("#111827"))

	return style.Render(helpContent)
}
