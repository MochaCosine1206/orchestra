package tui

import (
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/MochaCosine1206/orchestra/internal/config"
	"github.com/MochaCosine1206/orchestra/internal/core"
)

func TestGlobalModel_InitReturnsCmd(t *testing.T) {
	t.Parallel()
	projects := []*config.ProjectEntry{
		{Path: "/tmp/test-proj", Name: "test-proj"},
	}
	gm := NewGlobalModel(projects)
	cmd := gm.Init()
	if cmd == nil {
		t.Fatal("Init() should return a batch command")
	}
}

func TestGlobalModel_NavigateUpDown(t *testing.T) {
	t.Parallel()
	projects := []*config.ProjectEntry{
		{Path: "/tmp/a", Name: "alpha"},
		{Path: "/tmp/b", Name: "beta"},
		{Path: "/tmp/c", Name: "gamma"},
	}
	gm := NewGlobalModel(projects)
	// Simulate status data so projectCount works
	gm.status = &core.GlobalStatus{
		Projects: []core.ProjectSummary{
			{Name: "alpha", Path: "/tmp/a"},
			{Name: "beta", Path: "/tmp/b"},
			{Name: "gamma", Path: "/tmp/c"},
		},
	}

	if gm.selected != 0 {
		t.Fatalf("expected selected=0, got %d", gm.selected)
	}

	// Navigate down
	m, _ := gm.Update(tea.KeyMsg{Type: tea.KeyDown})
	gm = m.(GlobalModel)
	if gm.selected != 1 {
		t.Fatalf("expected selected=1 after down, got %d", gm.selected)
	}

	// Navigate down again
	m, _ = gm.Update(tea.KeyMsg{Type: tea.KeyDown})
	gm = m.(GlobalModel)
	if gm.selected != 2 {
		t.Fatalf("expected selected=2 after second down, got %d", gm.selected)
	}

	// Navigate down at boundary — should stay at 2
	m, _ = gm.Update(tea.KeyMsg{Type: tea.KeyDown})
	gm = m.(GlobalModel)
	if gm.selected != 2 {
		t.Fatalf("expected selected=2 at boundary, got %d", gm.selected)
	}

	// Navigate up
	m, _ = gm.Update(tea.KeyMsg{Type: tea.KeyUp})
	gm = m.(GlobalModel)
	if gm.selected != 1 {
		t.Fatalf("expected selected=1 after up, got %d", gm.selected)
	}
}

func TestGlobalModel_NumberKeySelection(t *testing.T) {
	t.Parallel()
	gm := NewGlobalModel(nil)
	gm.status = &core.GlobalStatus{
		Projects: []core.ProjectSummary{
			{Name: "alpha"},
			{Name: "beta"},
			{Name: "gamma"},
		},
	}

	// Press '2' to select second project
	m, _ := gm.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'2'}})
	gm = m.(GlobalModel)
	if gm.selected != 1 {
		t.Fatalf("expected selected=1 after pressing '2', got %d", gm.selected)
	}

	// Press '9' — out of range, should not change
	m, _ = gm.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'9'}})
	gm = m.(GlobalModel)
	if gm.selected != 1 {
		t.Fatalf("expected selected=1 after pressing '9' (out of range), got %d", gm.selected)
	}
}

func TestGlobalModel_ViewRendersProjectList(t *testing.T) {
	t.Parallel()
	gm := NewGlobalModel(nil)
	gm.width = 120
	gm.height = 40
	gm.ready = true
	gm.status = &core.GlobalStatus{
		Projects: []core.ProjectSummary{
			{Name: "alpha", Path: "/tmp/a", ActiveSession: "s-123", AgentCount: 2, TasksDone: 5, TasksPending: 3},
			{Name: "beta", Path: "/tmp/b", AgentCount: 0, TasksDone: 10, TasksPending: 0},
		},
	}

	view := gm.View()
	if view == "" {
		t.Fatal("View() returned empty string")
	}
	if !contains(view, "GLOBAL DASHBOARD") {
		t.Error("View should contain 'GLOBAL DASHBOARD'")
	}
	if !contains(view, "alpha") {
		t.Error("View should contain project name 'alpha'")
	}
	if !contains(view, "beta") {
		t.Error("View should contain project name 'beta'")
	}
	if !contains(view, "2 projects") {
		t.Error("View summary bar should show '2 projects'")
	}
}

func TestGlobalModel_SummaryBarAggregates(t *testing.T) {
	t.Parallel()
	gm := NewGlobalModel(nil)
	gm.width = 100
	gm.status = &core.GlobalStatus{
		Projects: []core.ProjectSummary{
			{AgentCount: 3, TasksDone: 10, TasksPending: 2, ActiveSession: "s1"},
			{AgentCount: 1, TasksDone: 5, TasksPending: 0},
		},
	}

	bar := gm.summaryBar()
	if !contains(bar, "4 agents") {
		t.Errorf("summary should show 4 agents, got: %s", bar)
	}
	if !contains(bar, "15 tasks done") {
		t.Errorf("summary should show 15 tasks done, got: %s", bar)
	}
	if !contains(bar, "1 active") {
		t.Errorf("summary should show 1 active, got: %s", bar)
	}
}

func TestGlobalModel_DataMsgUpdatesStatus(t *testing.T) {
	t.Parallel()
	gm := NewGlobalModel(nil)
	status := &core.GlobalStatus{
		Projects: []core.ProjectSummary{
			{Name: "proj1", LastActivity: time.Now()},
		},
	}

	m, _ := gm.Update(globalDataMsg{status: status})
	gm = m.(GlobalModel)
	if gm.status == nil {
		t.Fatal("status should be set after globalDataMsg")
	}
	if len(gm.status.Projects) != 1 {
		t.Fatalf("expected 1 project, got %d", len(gm.status.Projects))
	}
}

func TestNewGlobal_DelegatesToGlobalModel(t *testing.T) {
	t.Parallel()
	projects := []*config.ProjectEntry{
		{Path: "/tmp/test", Name: "test"},
	}
	gm := NewGlobalModel(projects)
	m := NewGlobal(gm)

	if !m.GlobalMode {
		t.Fatal("NewGlobal should set GlobalMode=true")
	}

	// View should delegate to global model
	view := m.View()
	if view != "Loading..." {
		// GlobalModel is not ready, so it should show Loading...
		t.Errorf("expected 'Loading...', got: %s", view)
	}
}

// contains and searchString are defined in model_test.go
