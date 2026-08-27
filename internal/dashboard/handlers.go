package dashboard

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"html/template"
	"io"
	"log/slog"

	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/MochaCosine1206/orchestra/internal/db"
)

// RegisterRoutes wires all dashboard routes onto the given mux.
func (s *Server) RegisterRoutes(mux *http.ServeMux) {
	// Static files
	mux.Handle("/static/", http.StripPrefix("/static/", StaticHandler()))

	// SSE event stream
	mux.HandleFunc("/events", s.handleSSE)

	// Page routes (GET)
	mux.HandleFunc("/", s.handleIndex)
	mux.HandleFunc("/project/", s.handleProject)
	mux.HandleFunc("/task/", s.handleTask)
	mux.HandleFunc("/agent/", s.handleAgent)
	mux.HandleFunc("/config", s.handleConfig)
	mux.HandleFunc("/artifacts", s.handleArtifacts)
	mux.HandleFunc("/git/", s.handleGit)
	mux.HandleFunc("/briefing", s.handleBriefing)

	// HTMX fragment endpoints (GET, 2s polling)
	mux.HandleFunc("/api/factory", s.handleAPIFactory)
	mux.HandleFunc("/api/project/", s.handleAPIProjectOrAction)
	mux.HandleFunc("/api/task/", s.handleAPITask)
	mux.HandleFunc("/api/agent/", s.handleAPIAgent)

	mux.HandleFunc("/api/briefing", s.handleAPIBriefing)
	mux.HandleFunc("/api/nav-status", s.handleNavStatus)

	// Config section POST endpoints
	mux.HandleFunc("/api/config/", s.handleConfigSection)

	// Global POST actions
	mux.HandleFunc("/api/factory/pause-all", s.handleFactoryPauseAll)
	mux.HandleFunc("/api/config/theme", s.handleConfigTheme)

	// HITL interaction routes
	s.registerHITLRoutes(mux)
}

// --- Page handlers ---

func (s *Server) handleIndex(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	// Build initial factory data so stat grid is visible on first load
	// (same logic as handleAPIFactory, but for the full page render)
	ctx := r.Context()
	data := s.buildFactoryData(ctx)
	s.renderTemplate(w, "index", data)
}

// --- Project detail view structs ---

// ProjectDetailData is the template data for the project detail page.
type ProjectDetailData struct {
	ID         string
	Name       string
	Goal       string
	DotClass   string
	Status     string
	Phase      string
	Tasks      []ProjectTaskRow
	Agents     []ProjectAgentRow
	Events     []ProjectEvent
	Decisions  []ProjectDecision
	Elapsed    string
	TasksDone  int
	TasksTotal int
}

// ProjectTaskRow is a single task displayed in the project task list.
type ProjectTaskRow struct {
	ID          string
	ShortID     string
	StatusClass string
	StatusIcon  string
	RoleLabel   string
	RoleClass   string
	Title       string
	Elapsed     string
}

// ProjectAgentRow is a single agent displayed in the project agent sidebar.
type ProjectAgentRow struct {
	ID          string
	Name        string
	Emoji       string
	EmojiFile   string // "/static/agents/{file}.webp"
	StatusColor string
	RoleColor   string
	Role        string
	Activity    string
	CtxPct      int
	CtxColor    string
}

// ProjectEvent is a single event in the project event sidebar.
type ProjectEvent struct {
	TimeAgo string
	Color   string
	Text    string
}

// ProjectDecision is a conductor decision displayed in the project view.
type ProjectDecision struct {
	Time string
	Text string
}

func (s *Server) handleProject(w http.ResponseWriter, r *http.Request) {
	id := extractID(r.URL.Path, "/project/")
	if id == "" {
		http.NotFound(w, r)
		return
	}
	data, err := s.buildProjectDetailData(r.Context(), id)
	if err != nil {
		writeJSONError(w, "internal error", "internal_error", http.StatusInternalServerError)
		return
	}
	if data == nil {
		http.NotFound(w, r)
		return
	}
	s.renderTemplate(w, "project", data)
}

// buildProjectDetailData assembles all data for the project detail view.
// Returns nil if the conductor is not found.
func (s *Server) buildProjectDetailData(ctx context.Context, id string) (*ProjectDetailData, error) {
	conductor, err := s.DB.GetConductor(ctx, id)
	if err != nil {
		return nil, err
	}
	if conductor == nil {
		return nil, nil
	}

	// Query all tasks (needed for name extraction and task list)
	allTasks, _ := s.DB.ListTasks(ctx)

	// Extract name using the same logic as factory rows
	name := formatConductorName(*conductor, allTasks)

	// Determine dot class from status
	dotClass := "idle"
	switch conductor.Status {
	case "active":
		dotClass = "running"
	case "completed":
		dotClass = "completed"
	case "failed":
		dotClass = "failed"
	}
	var tasksDone, tasksTotal int
	var taskRows []ProjectTaskRow
	var conductorTaskIDs []string
	for _, t := range allTasks {
		if !t.ConductorID.Valid || t.ConductorID.String != id {
			continue
		}
		tasksTotal++
		if t.Status == "done" || t.Status == "merged" {
			tasksDone++
		}
		conductorTaskIDs = append(conductorTaskIDs, t.ID)

		shortID := t.ID
		if len(shortID) > 4 {
			shortID = shortID[len(shortID)-4:]
		}

		statusClass, statusIcon := taskStatusDisplay(t.Status)
		roleLabel, roleClass := roleDisplay(t.Role)

		elapsed := ""
		if t.StartedAt.Valid {
			end := time.Now()
			if t.CompletedAt.Valid {
				end = t.CompletedAt.Time
			}
			elapsed = formatElapsed(end.Sub(t.StartedAt.Time))
		}

		taskRows = append(taskRows, ProjectTaskRow{
			ID:          t.ID,
			ShortID:     shortID,
			StatusClass: statusClass,
			StatusIcon:  statusIcon,
			RoleLabel:   roleLabel,
			RoleClass:   roleClass,
			Title:       t.Title,
			Elapsed:     elapsed,
		})
	}

	// Query agents working on this conductor's tasks
	allAgents, _ := s.DB.ListAgents(ctx)
	conductorTaskSet := make(map[string]bool, len(conductorTaskIDs))
	for _, tid := range conductorTaskIDs {
		conductorTaskSet[tid] = true
	}
	var agentRows []ProjectAgentRow
	for _, a := range allAgents {
		if !a.CurrentTask.Valid || !conductorTaskSet[a.CurrentTask.String] {
			continue
		}
		emoji := roleEmoji(a.Role)
		statusColor := agentStatusColor(a.Status)
		roleColor := roleColorName(a.Role)

		activity := "idle"
		if a.HeartbeatAt.Valid {
			activity = formatTimeAgo(time.Since(a.HeartbeatAt.Time))
		}

		ctxPct := 0 // Context usage not available from DB alone
		ctxColor := "green"
		if ctxPct > 75 {
			ctxColor = "red"
		} else if ctxPct > 50 {
			ctxColor = "yellow"
		}

		// Generate short display name: "Agent-1" based on position
		agentName := fmt.Sprintf("Agent-%d", len(agentRows)+1)
		agentRows = append(agentRows, ProjectAgentRow{
			ID:          a.ID,
			Name:        agentName,
			Emoji:       emoji,
			EmojiFile:   AvatarSrc(a.Role, a.Status),
			StatusColor: statusColor,
			RoleColor:   roleColor,
			Role:        strings.ToUpper(a.Role),
			Activity:    activity,
			CtxPct:      ctxPct,
			CtxColor:    ctxColor,
		})
	}

	// Query recent events, filter by conductor task IDs
	allEvents, _ := s.DB.RecentEvents(ctx, 50)
	var eventRows []ProjectEvent
	for _, e := range allEvents {
		if !e.TaskID.Valid || !conductorTaskSet[e.TaskID.String] {
			continue
		}
		if len(eventRows) >= 15 {
			break
		}
		text := formatEventText(e.EventType, e.Payload.String)
		color := eventColorName(e.EventType)
		timeAgo := formatTimeAgo(time.Since(e.Timestamp))
		eventRows = append(eventRows, ProjectEvent{
			TimeAgo: timeAgo,
			Color:   color,
			Text:    text,
		})
	}

	// Phase from conductor's PhaseID
	phase := conductor.PhaseID
	if phase == "" {
		phase = "default"
	}

	goal := formatConductorGoal(*conductor)

	data := &ProjectDetailData{
		ID:         id,
		Name:       name,
		Goal:       goal,
		DotClass:   dotClass,
		Status:     conductor.Status,
		Phase:      phase,
		Tasks:      taskRows,
		Agents:     agentRows,
		Events:     eventRows,
		Elapsed:    formatElapsed(time.Since(conductor.StartedAt)),
		TasksDone:  tasksDone,
		TasksTotal: tasksTotal,
	}
	return data, nil
}

// ACItem represents a single acceptance criteria line item.
type ACItem struct {
	Text string
	Done bool
}

// TaskDetailData is the template data for the task detail page.
type TaskDetailData struct {
	ID                      string
	Title                   string
	Description             string
	DescriptionHTML         string
	AcceptanceCriteria      string
	AcceptanceCriteriaItems []ACItem
	Status                  string
	StatusClass             string
	RoleClass               string
	Role                    string
	RoleLabel               string
	AssignedTo              string
	Branch                  string
	Result                  string
	ResultHTML              string
	DependsOn               string
	Elapsed                 string
}

func (s *Server) handleTask(w http.ResponseWriter, r *http.Request) {
	id := extractID(r.URL.Path, "/task/")
	if id == "" {
		http.NotFound(w, r)
		return
	}
	task, err := s.DB.GetTaskByID(r.Context(), id)
	if err != nil {
		writeJSONError(w, "internal error", "internal_error", http.StatusInternalServerError)
		return
	}
	if task == nil {
		http.NotFound(w, r)
		return
	}

	statusClass, _ := taskStatusDisplay(task.Status)
	_, roleClass := roleDisplay(task.Role)

	elapsed := ""
	if task.StartedAt.Valid {
		end := time.Now()
		if task.CompletedAt.Valid {
			end = task.CompletedAt.Time
		}
		elapsed = formatElapsed(end.Sub(task.StartedAt.Time))
	}

	desc := nullStr(task.Description)
	result := nullStr(task.Result)
	ac := nullStr(task.AcceptanceCriteria)

	data := TaskDetailData{
		ID:                      task.ID,
		Title:                   task.Title,
		Description:             desc,
		DescriptionHTML:         formatTextToHTML(desc),
		AcceptanceCriteria:      ac,
		AcceptanceCriteriaItems: parseAcceptanceCriteria(ac),
		Status:                  task.Status,
		StatusClass:             statusClass,
		RoleClass:               roleClass,
		Role:                    task.Role,
		RoleLabel:               strings.ToUpper(task.Role),
		AssignedTo:              nullStr(task.AssignedTo),
		Branch:                  nullStr(task.Branch),
		Result:                  result,
		ResultHTML:              formatTextToHTML(result),
		DependsOn:               nullStr(task.DependsOn),
		Elapsed:                 elapsed,
	}
	s.renderTemplate(w, "task-detail", data)
}

// AgentDetailData is the template data for the agent detail page.
type AgentDetailData struct {
	ID          string
	Role        string
	RoleLabel   string
	RoleClass   string
	Emoji       string
	EmojiFile   string
	Status      string
	StatusColor string
	CurrentTask string
	TaskLink    string
	Heartbeat   string
	CtxPct      int
	CtxColor    string
}

func (s *Server) handleAgent(w http.ResponseWriter, r *http.Request) {
	id := extractID(r.URL.Path, "/agent/")
	if id == "" {
		http.NotFound(w, r)
		return
	}
	ctx := r.Context()
	agents, err := s.DB.ListAgents(ctx)
	if err != nil {
		writeJSONError(w, "internal error", "internal_error", http.StatusInternalServerError)
		return
	}
	var found *db.Agent
	for i := range agents {
		if agents[i].ID == id {
			found = &agents[i]
			break
		}
	}
	if found == nil {
		http.NotFound(w, r)
		return
	}

	heartbeat := "never"
	if found.HeartbeatAt.Valid {
		heartbeat = formatTimeAgo(time.Since(found.HeartbeatAt.Time))
	}

	currentTask := ""
	taskLink := ""
	if found.CurrentTask.Valid {
		currentTask = found.CurrentTask.String
		taskLink = "/task/" + currentTask
	}

	ctxPct := 0
	ctxColor := "green"
	if ctxPct > 75 {
		ctxColor = "red"
	} else if ctxPct > 50 {
		ctxColor = "yellow"
	}

	_, roleClass := roleDisplay(found.Role)

	data := AgentDetailData{
		ID:          found.ID,
		Role:        found.Role,
		RoleLabel:   strings.ToUpper(found.Role),
		RoleClass:   roleClass,
		Emoji:       roleEmoji(found.Role),
		EmojiFile:   AvatarSrc(found.Role, found.Status),
		Status:      found.Status,
		StatusColor: agentStatusColor(found.Status),
		CurrentTask: currentTask,
		TaskLink:    taskLink,
		Heartbeat:   heartbeat,
		CtxPct:      ctxPct,
		CtxColor:    ctxColor,
	}
	s.renderTemplate(w, "agent-detail", data)
}

func (s *Server) handleConfig(w http.ResponseWriter, r *http.Request) {
	s.handleConfigFull(w, r)
}

func (s *Server) handleArtifacts(w http.ResponseWriter, r *http.Request) {
	s.handleArtifactsFull(w, r)
}

// BranchLane represents a single task branch in the git flow diagram.
type BranchLane struct {
	TaskID       string
	Title        string
	Branch       string
	Status       string
	Role         string
	AssignedTo   string
	HasConflict  bool
	ConflictTier string // "auto" (tier 1-3), "llm" (tier 4), "failed"
}

// ClusterGroup groups branch lanes by feature cluster.
type ClusterGroup struct {
	Name  string
	Lanes []BranchLane
}

// GitViewData is the template data for the git visualization page.
type GitViewData struct {
	ConductorID    string
	BaseBranch     string
	StagingBranch  string
	MergeStatus    string // NULL/"merging"/"staging"/"done"
	Clusters       []ClusterGroup
	UngroupedLanes []BranchLane
	MergeQueue     []MergeQueueDisplay
	FileLocks      []FileLockDisplay
}

// MergeQueueDisplay is a template-friendly merge queue entry.
type MergeQueueDisplay struct {
	Position        int
	Branch          string
	TaskID          string
	Status          string
	ConflictFiles   string
	ResolutionTier  string
	TierClass       string // "tier-auto", "tier-llm", "tier-failed"
	RefinementCount int
	MaxRefinement   int
	TestResult      string
}

// FileLockDisplay is a template-friendly file lock entry.
type FileLockDisplay struct {
	FilePath string
	TaskID   string
	LockedBy string
	Status   string // "active", "expired"
}

func (s *Server) handleGit(w http.ResponseWriter, r *http.Request) {
	id := extractID(r.URL.Path, "/git/")
	if id == "" {
		http.NotFound(w, r)
		return
	}

	ctx := r.Context()

	// "latest" resolves to the most recently started conductor
	if id == "latest" {
		conductors, _ := s.DB.ListAllConductors(ctx)
		if len(conductors) == 0 {
			// Render an empty git page within the base template
			s.renderTemplate(w, "git-empty", nil)
			return
		}
		// Use the last conductor (most recently started)
		id = conductors[len(conductors)-1].ID
	}

	conductor, err := s.DB.GetConductor(ctx, id)
	if err != nil {
		writeJSONError(w, "internal error", "internal_error", http.StatusInternalServerError)
		return
	}
	if conductor == nil {
		http.NotFound(w, r)
		return
	}

	tasks, _ := s.DB.ListTasks(ctx)
	mergeEntries, _ := s.DB.ListMergeQueueEntries(ctx, id)
	fileLocks, _ := s.DB.ListFileLocks(ctx)
	clusters, _ := s.DB.ListDistinctClusters(ctx, id)

	// Build merge queue entry lookup by task ID for conflict info
	mqByTask := make(map[string]db.MergeQueueEntry)
	for _, mq := range mergeEntries {
		mqByTask[mq.TaskID] = mq
	}

	// Filter tasks for this conductor
	var conductorTasks []db.Task
	for _, t := range tasks {
		if t.ConductorID.Valid && t.ConductorID.String == id {
			conductorTasks = append(conductorTasks, t)
		}
	}

	// Build branch lanes from tasks
	taskLaneMap := make(map[string]BranchLane)
	for _, t := range conductorTasks {
		lane := BranchLane{
			TaskID: t.ID,
			Title:  t.Title,
			Status: t.Status,
			Role:   t.Role,
		}
		if t.Branch.Valid {
			lane.Branch = t.Branch.String
		}
		if t.AssignedTo.Valid {
			lane.AssignedTo = t.AssignedTo.String
		}
		// Check merge queue for conflict info
		if mq, ok := mqByTask[t.ID]; ok {
			if mq.ConflictFiles.Valid && mq.ConflictFiles.String != "" {
				lane.HasConflict = true
				lane.ConflictTier = resolutionTierClass(mq.ResolutionTier.String)
			}
		}
		taskLaneMap[t.ID] = lane
	}

	// Group by feature cluster
	clusterGroups := make([]ClusterGroup, 0, len(clusters))
	clusterTaskSet := make(map[string]bool)
	for _, clusterName := range clusters {
		group := ClusterGroup{Name: clusterName}
		for _, t := range conductorTasks {
			if t.FeatureCluster.Valid && t.FeatureCluster.String == clusterName {
				if lane, ok := taskLaneMap[t.ID]; ok {
					group.Lanes = append(group.Lanes, lane)
					clusterTaskSet[t.ID] = true
				}
			}
		}
		if len(group.Lanes) > 0 {
			clusterGroups = append(clusterGroups, group)
		}
	}

	// Ungrouped lanes (tasks without a cluster)
	var ungrouped []BranchLane
	for _, t := range conductorTasks {
		if !clusterTaskSet[t.ID] {
			if lane, ok := taskLaneMap[t.ID]; ok {
				ungrouped = append(ungrouped, lane)
			}
		}
	}

	// Build merge queue display
	mqDisplay := make([]MergeQueueDisplay, 0, len(mergeEntries))
	for _, mq := range mergeEntries {
		d := MergeQueueDisplay{
			Position:        mq.Position,
			Branch:          mq.Branch,
			TaskID:          mq.TaskID,
			Status:          mq.Status,
			RefinementCount: mq.RefinementCount,
			MaxRefinement:   2,
		}
		if mq.ConflictFiles.Valid {
			d.ConflictFiles = mq.ConflictFiles.String
		}
		if mq.ResolutionTier.Valid {
			d.ResolutionTier = mq.ResolutionTier.String
			d.TierClass = "tier-" + resolutionTierClass(mq.ResolutionTier.String)
		}
		if mq.TestResult.Valid {
			d.TestResult = mq.TestResult.String
		}
		mqDisplay = append(mqDisplay, d)
	}

	// Build file lock display
	flDisplay := make([]FileLockDisplay, 0, len(fileLocks))
	for _, fl := range fileLocks {
		d := FileLockDisplay{
			FilePath: fl.FilePath,
			Status:   "active",
		}
		if fl.TaskID.Valid {
			d.TaskID = fl.TaskID.String
		}
		if fl.LockedBy.Valid {
			d.LockedBy = fl.LockedBy.String
		}
		if fl.ExpiresAt.Valid && fl.ExpiresAt.Time.Before(time.Now()) {
			d.Status = "expired"
		}
		flDisplay = append(flDisplay, d)
	}

	mergeStatus := ""
	if conductor.MergeStatus.Valid {
		mergeStatus = conductor.MergeStatus.String
	}

	data := GitViewData{
		ConductorID:    id,
		BaseBranch:     conductor.BaseBranch,
		StagingBranch:  conductor.StagingBranch,
		MergeStatus:    mergeStatus,
		Clusters:       clusterGroups,
		UngroupedLanes: ungrouped,
		MergeQueue:     mqDisplay,
		FileLocks:      flDisplay,
	}

	s.renderTemplate(w, "git", data)
}

// resolutionTierClass maps a resolution tier string to a CSS class category.
func resolutionTierClass(tier string) string {
	switch tier {
	case "1", "2", "3", "import", "non-overlapping", "trivial":
		return "auto"
	case "4", "llm":
		return "llm"
	default:
		return "failed"
	}
}

func (s *Server) handleBriefing(w http.ResponseWriter, r *http.Request) {
	data, lastSeenID := s.buildBriefingData(r)

	// Update last-seen cookie to latest event on full page load
	latestEvents, _ := s.DB.RecentEvents(r.Context(), 1)
	if len(latestEvents) > 0 {
		http.SetCookie(w, &http.Cookie{
			Name:     "orchestra_last_seen",
			Value:    strconv.FormatInt(latestEvents[0].ID, 10),
			Path:     "/",
			MaxAge:   86400 * 30,
			HttpOnly: true,
			SameSite: http.SameSiteLaxMode,
		})
	}

	data["lastSeenID"] = lastSeenID
	s.renderTemplate(w, "briefing", data)
}

// handleAPIBriefing serves the briefing fragment for HTMX polling.
func (s *Server) handleAPIBriefing(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSONError(w, "method not allowed", "method_not_allowed", http.StatusMethodNotAllowed)
		return
	}
	data, _ := s.buildBriefingData(r)
	s.renderTemplate(w, "briefing-fragment", data)
}

// BriefingCard is a per-conductor summary for the briefing page.
type BriefingCard struct {
	ConductorID string
	Name        string
	Goal        string
	Status      string
	TasksDone   int
	TasksFailed int
	TasksTotal  int
	AgentCount  int
	ActionItems []string
	Events      []CoalescedEvent
}

// buildBriefingData assembles all data for the briefing view.
func (s *Server) buildBriefingData(r *http.Request) (map[string]interface{}, int64) {
	ctx := r.Context()
	conductors, _ := s.DB.ListAllConductors(ctx)
	tasks, _ := s.DB.ListTasks(ctx)
	agents, _ := s.DB.ListAgents(ctx)

	// Read last-seen event ID from cookie for "since last activity" tracking
	var lastSeenID int64
	if c, err := r.Cookie("orchestra_last_seen"); err == nil {
		lastSeenID, _ = strconv.ParseInt(c.Value, 10, 64)
	}

	var cards []BriefingCard
	var totalDone, totalFailed, totalTasks int

	for _, cond := range conductors {
		// Extract readable name from goal
		name := cond.ID
		if cond.Goal != "" {
			firstLine := strings.SplitN(cond.Goal, "\n", 2)[0]
			if len(firstLine) > 60 {
				firstLine = firstLine[:57] + "..."
			}
			name = firstLine
		}
		card := BriefingCard{
			ConductorID: cond.ID,
			Name:        name,
			Goal:        cond.Goal,
			Status:      cond.Status,
		}

		// Count tasks per conductor
		taskIDs, _ := s.DB.ListTaskIDsByConductor(ctx, cond.ID)
		card.TasksTotal = len(taskIDs)
		totalTasks += len(taskIDs)

		for _, t := range tasks {
			if t.ConductorID.Valid && t.ConductorID.String == cond.ID {
				switch t.Status {
				case "done", "merged":
					card.TasksDone++
					totalDone++
				case "failed":
					card.TasksFailed++
					totalFailed++
				}
			}
		}

		// Count agents working on this conductor's tasks
		for _, a := range agents {
			if a.CurrentTask.Valid {
				for _, tid := range taskIDs {
					if a.CurrentTask.String == tid {
						card.AgentCount++
						break
					}
				}
			}
		}

		// Get events since last seen for this conductor
		if lastSeenID > 0 {
			events, _ := s.DB.EventsSinceForSession(ctx, lastSeenID, taskIDs)
			card.Events = coalesceEvents(events)
			// Derive action items from failure events
			for _, e := range events {
				if e.EventType == "task_failed" || e.EventType == "circuit_breaker" {
					msg := e.EventType
					if e.Payload.Valid {
						msg = e.Payload.String
					}
					card.ActionItems = append(card.ActionItems, msg)
				}
			}
		}

		cards = append(cards, card)
	}

	return map[string]interface{}{
		"conductors":  conductors,
		"cards":       cards,
		"tasks":       tasks,
		"agents":      agents,
		"totalDone":   totalDone,
		"totalFailed": totalFailed,
		"totalTasks":  totalTasks,
	}, lastSeenID
}

// --- HTMX fragment handlers ---

// ProjectRow represents a conductor displayed in the factory project list.
type ProjectRow struct {
	ID         string
	Name       string
	Goal       string
	Status     string
	DotClass   string
	TasksDone  int
	TasksTotal int
	AgentCount int
	Elapsed    string
}

// Alert represents a failure/circuit-breaker event shown in the alert banner.
type Alert struct {
	Message   string
	TaskID    string
	ProjectID string
}

// buildFactoryData queries the DB and assembles all data needed for the
// factory stat grid, project list, events, and alerts.
func (s *Server) buildFactoryData(ctx context.Context) map[string]interface{} {
	conductors, _ := s.DB.ListAllConductors(ctx)
	taskSummary, _ := s.DB.TaskSummaryByStatus(ctx)
	agentSummary, _ := s.DB.AgentSummaryByStatus(ctx)
	tasks, _ := s.DB.ListTasks(ctx)
	agents, _ := s.DB.ListAgents(ctx)
	events, _ := s.DB.RecentEvents(ctx, 20)

	// Compute stat values
	activeConductors := 0
	idleConductors := 0
	for _, c := range conductors {
		if c.Status == "active" {
			activeConductors++
		} else {
			idleConductors++
		}
	}

	totalAgents := 0
	workingAgents := 0
	idleAgents := 0
	for k, v := range agentSummary {
		totalAgents += v
		if k == "active" || k == "working" {
			workingAgents += v
		} else {
			idleAgents += v
		}
	}

	tasksDone := taskSummary["done"] + taskSummary["merged"]
	tasksRunning := taskSummary["in_progress"] + taskSummary["assigned"]
	tasksFailed := taskSummary["failed"]
	totalTasks := 0
	for _, v := range taskSummary {
		totalTasks += v
	}

	// Success rate as range for small samples
	successRate := computeSuccessRate(tasksDone, tasksFailed)

	// Build per-conductor project rows
	var projects []ProjectRow
	for _, c := range conductors {
		// Generate a descriptive name from conductor context
		name := formatConductorName(c, tasks)
		goal := formatConductorGoal(c)
		row := ProjectRow{
			ID:     c.ID,
			Name:   name,
			Goal:   goal,
			Status: c.Status,
		}
		// Determine dot class based on status
		switch c.Status {
		case "active":
			row.DotClass = "running"
		case "completed":
			row.DotClass = "completed"
		case "failed":
			row.DotClass = "failed"
		default:
			row.DotClass = "idle"
		}
		// Count tasks for this conductor
		for _, t := range tasks {
			if t.ConductorID.Valid && t.ConductorID.String == c.ID {
				row.TasksTotal++
				if t.Status == "done" || t.Status == "merged" {
					row.TasksDone++
				}
			}
		}
		// Count agents for this conductor
		taskIDs, _ := s.DB.ListTaskIDsByConductor(ctx, c.ID)
		for _, a := range agents {
			if a.CurrentTask.Valid {
				for _, tid := range taskIDs {
					if a.CurrentTask.String == tid {
						row.AgentCount++
						break
					}
				}
			}
		}
		// Elapsed time
		row.Elapsed = formatElapsed(time.Since(c.StartedAt))
		projects = append(projects, row)
	}

	// Coalesce events
	coalescedEvents := coalesceEvents(events)

	// Build alerts: failed tasks with circuit breaker trips
	var alerts []Alert
	for _, e := range events {
		if e.EventType == "circuit_breaker" || e.EventType == "task_failed" {
			msg := e.EventType
			if e.Payload.Valid && e.Payload.String != "" {
				msg = e.Payload.String
			}
			tid := ""
			pid := ""
			if e.TaskID.Valid {
				tid = e.TaskID.String
				if task, err := s.DB.GetTaskByID(ctx, tid); err == nil && task.ConductorID.Valid {
					pid = task.ConductorID.String
				}
			}
			alerts = append(alerts, Alert{Message: msg, TaskID: tid, ProjectID: pid})
		}
	}

	return map[string]interface{}{
		"activeConductors": activeConductors,
		"idleConductors":   idleConductors,
		"totalAgents":      totalAgents,
		"workingAgents":    workingAgents,
		"idleAgents":       idleAgents,
		"tasksDone":        tasksDone,
		"tasksRunning":     tasksRunning,
		"tasksFailed":      tasksFailed,
		"totalTasks":       totalTasks,
		"successRate":      successRate,
		"projects":         projects,
		"events":           coalescedEvents,
		"alerts":           alerts,
	}
}

func (s *Server) handleAPIFactory(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSONError(w, "method not allowed", "method_not_allowed", http.StatusMethodNotAllowed)
		return
	}
	data := s.buildFactoryData(r.Context())
	s.renderTemplate(w, "factory-fragment", data)
}

// handleAPIProjectOrAction dispatches GET /api/project/{id} fragments
// and POST /api/project/{id}/{action} commands.
func (s *Server) handleAPIProjectOrAction(w http.ResponseWriter, r *http.Request) {
	// Strip the prefix to get: {id} or {id}/{action} or {id}/{action}/{subId}
	rest := strings.TrimPrefix(r.URL.Path, "/api/project/")
	parts := strings.SplitN(rest, "/", 3)

	if len(parts) == 0 || parts[0] == "" {
		http.NotFound(w, r)
		return
	}

	projectID := parts[0]

	// GET /api/project/{id} — HTMX fragment
	if r.Method == http.MethodGet && len(parts) == 1 {
		s.handleAPIProjectFragment(w, r, projectID)
		return
	}

	// POST /api/project/{id}/{action}[/{subId}]
	if r.Method == http.MethodPost && len(parts) >= 2 {
		action := parts[1]
		subID := ""
		if len(parts) == 3 {
			subID = parts[2]
		}
		s.handleProjectAction(w, r, projectID, action, subID)
		return
	}

	http.NotFound(w, r)
}

func (s *Server) handleAPIProjectFragment(w http.ResponseWriter, r *http.Request, projectID string) {
	data, err := s.buildProjectDetailData(r.Context(), projectID)
	if err != nil {
		writeJSONError(w, "internal error", "internal_error", http.StatusInternalServerError)
		return
	}
	if data == nil {
		http.NotFound(w, r)
		return
	}
	s.renderTemplate(w, "project-fragment", data)
}

func (s *Server) handleProjectAction(w http.ResponseWriter, r *http.Request, projectID, action, subID string) {
	ctx := r.Context()
	writer := "dashboard"

	switch action {
	case "pause":
		err := s.DB.SetBlackboard(ctx, fmt.Sprintf("conductor:%s:signal", projectID), "pause", writer)
		if err != nil {
			writeJSON(w, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, map[string]string{"status": "ok", "action": "pause"})

	case "resume":
		err := s.DB.SetBlackboard(ctx, fmt.Sprintf("conductor:%s:signal", projectID), "resume", writer)
		if err != nil {
			writeJSON(w, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, map[string]string{"status": "ok", "action": "resume"})

	case "stop":
		err := s.DB.SetBlackboard(ctx, fmt.Sprintf("conductor:%s:signal", projectID), "stop", writer)
		if err != nil {
			writeJSON(w, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, map[string]string{"status": "ok", "action": "stop"})

	case "retry":
		if subID == "" {
			writeJSONError(w, "task ID required", "bad_request", http.StatusBadRequest)
			return
		}
		err := s.DB.SetBlackboard(ctx, fmt.Sprintf("auto:retry:%s", subID), "requested", writer)
		if err != nil {
			writeJSON(w, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, map[string]string{"status": "ok", "action": "retry", "taskId": subID})

	case "skip":
		if subID == "" {
			writeJSONError(w, "task ID required", "bad_request", http.StatusBadRequest)
			return
		}
		err := s.DB.SetBlackboard(ctx, fmt.Sprintf("auto:skip:%s", subID), "requested", writer)
		if err != nil {
			writeJSON(w, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, map[string]string{"status": "ok", "action": "skip", "taskId": subID})

	case "replan":
		err := s.DB.SetBlackboard(ctx, "auto:replan", projectID, writer)
		if err != nil {
			writeJSON(w, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, map[string]string{"status": "ok", "action": "replan"})

	case "add-task":
		body, err := io.ReadAll(r.Body)
		if err != nil {
			writeJSONError(w, "bad request", "bad_request", http.StatusBadRequest)
			return
		}
		spec := string(body)
		if spec == "" {
			writeJSONError(w, "task spec required in body", "bad_request", http.StatusBadRequest)
			return
		}
		err = s.DB.SetBlackboard(ctx, fmt.Sprintf("auto:add-task:%s", spec), projectID, writer)
		if err != nil {
			writeJSON(w, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, map[string]string{"status": "ok", "action": "add-task"})

	default:
		http.NotFound(w, r)
	}
}

func (s *Server) handleAPITask(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSONError(w, "method not allowed", "method_not_allowed", http.StatusMethodNotAllowed)
		return
	}
	id := extractID(r.URL.Path, "/api/task/")
	if id == "" {
		http.NotFound(w, r)
		return
	}
	task, err := s.DB.GetTaskByID(r.Context(), id)
	if err != nil {
		writeJSON(w, map[string]string{"error": err.Error()})
		return
	}
	if task == nil {
		http.NotFound(w, r)
		return
	}
	writeJSON(w, task)
}

func (s *Server) handleAPIAgent(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSONError(w, "method not allowed", "method_not_allowed", http.StatusMethodNotAllowed)
		return
	}
	id := extractID(r.URL.Path, "/api/agent/")
	if id == "" {
		http.NotFound(w, r)
		return
	}
	agents, err := s.DB.ListAgents(r.Context())
	if err != nil {
		writeJSON(w, map[string]string{"error": err.Error()})
		return
	}
	for _, a := range agents {
		if a.ID == id {
			writeJSON(w, a)
			return
		}
	}
	http.NotFound(w, r)
}

// --- Nav status fragment (polled by HTMX in layout) ---

func (s *Server) handleNavStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSONError(w, "method not allowed", "method_not_allowed", http.StatusMethodNotAllowed)
		return
	}
	ctx := r.Context()

	// Count action items: failed tasks + circuit breaker events
	taskSummary, _ := s.DB.TaskSummaryByStatus(ctx)
	actionCount := taskSummary["failed"]

	// Find most recent event time to compute "since last check"
	events, _ := s.DB.RecentEvents(ctx, 1)
	lastActivity := ""
	if len(events) > 0 {
		lastActivity = formatTimeAgo(time.Since(events[0].Timestamp))
	}

	s.renderTemplate(w, "nav-status-fragment", map[string]interface{}{
		"actionCount":  actionCount,
		"lastActivity": lastActivity,
	})
}

// --- Global POST handlers ---

func (s *Server) handleFactoryPauseAll(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSONError(w, "method not allowed", "method_not_allowed", http.StatusMethodNotAllowed)
		return
	}
	ctx := r.Context()
	conductors, err := s.DB.ListActiveConductors(ctx)
	if err != nil {
		writeJSON(w, map[string]string{"error": err.Error()})
		return
	}
	for _, c := range conductors {
		_ = s.DB.SetBlackboard(ctx, fmt.Sprintf("conductor:%s:signal", c.ID), "pause", "dashboard")
	}
	writeJSON(w, map[string]string{"status": "ok", "action": "pause-all", "count": fmt.Sprintf("%d", len(conductors))})
}

func (s *Server) handleConfigTheme(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSONError(w, "method not allowed", "method_not_allowed", http.StatusMethodNotAllowed)
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, 1024))
	if err != nil {
		writeJSONError(w, "bad request", "bad_request", http.StatusBadRequest)
		return
	}
	var payload struct {
		Theme string `json:"theme"`
	}
	if err := json.Unmarshal(body, &payload); err != nil || payload.Theme == "" {
		writeJSONError(w, "invalid theme payload", "bad_request", http.StatusBadRequest)
		return
	}
	err = s.DB.SetBlackboard(r.Context(), "config:theme", payload.Theme, "dashboard")
	if err != nil {
		writeJSON(w, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, map[string]string{"status": "ok", "theme": payload.Theme})
}

// --- Helpers ---

func extractID(path, prefix string) string {
	rest := strings.TrimPrefix(path, prefix)
	// Remove trailing slash
	rest = strings.TrimSuffix(rest, "/")
	// Take first path segment only
	if idx := strings.Index(rest, "/"); idx >= 0 {
		rest = rest[:idx]
	}
	return rest
}

func (s *Server) renderTemplate(w http.ResponseWriter, name string, data interface{}) {
	tmpl, err := Templates()
	if err != nil {
		slog.Error("parsing templates", "err", err)
		writeJSONError(w, "template error", "template_error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := tmpl.ExecuteTemplate(w, name, data); err != nil {
		slog.Error("executing template", "template", name, "err", err)
	}
}

func writeJSON(w http.ResponseWriter, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(v); err != nil {
		slog.Error("encoding JSON response", "err", err)
	}
}

func writeJSONError(w http.ResponseWriter, message string, code string, status int) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{
		"error": message,
		"code":  code,
	})
}

// CoalescedEvent groups repetitive events into a single entry with a count.
type CoalescedEvent struct {
	EventType string
	Text      string
	DotColor  string
	TimeAgo   string
	Count     int
}

// coalesceEvents groups consecutive events of the same type, producing ×N badges.
func coalesceEvents(events []db.Event) []CoalescedEvent {
	var result []CoalescedEvent
	for _, e := range events {
		text := e.EventType
		if e.Payload.Valid && e.Payload.String != "" {
			text = e.EventType + ": " + e.Payload.String
		}
		dotColor := eventDotColor(e.EventType)
		timeAgo := formatTimeAgo(time.Since(e.Timestamp))

		// Coalesce with previous if same event type
		if len(result) > 0 && result[len(result)-1].EventType == e.EventType {
			result[len(result)-1].Count++
			continue
		}
		result = append(result, CoalescedEvent{
			EventType: e.EventType,
			Text:      text,
			DotColor:  dotColor,
			TimeAgo:   timeAgo,
			Count:     1,
		})
	}
	return result
}

func eventDotColor(eventType string) string {
	switch {
	case strings.Contains(eventType, "done") || strings.Contains(eventType, "merged") || strings.Contains(eventType, "success"):
		return "var(--green)"
	case strings.Contains(eventType, "fail") || strings.Contains(eventType, "circuit"):
		return "var(--red)"
	case strings.Contains(eventType, "spawn") || strings.Contains(eventType, "assign") || strings.Contains(eventType, "merge"):
		return "var(--blue)"
	case strings.Contains(eventType, "rate_limit") || strings.Contains(eventType, "warn") || strings.Contains(eventType, "idle"):
		return "var(--yellow)"
	default:
		return "var(--dim)"
	}
}

func formatTimeAgo(d time.Duration) string {
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds ago", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd ago", int(d.Hours()/24))
	}
}

func formatElapsed(d time.Duration) string {
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
	if d < time.Hour {
		return fmt.Sprintf("%dm %ds", int(d.Minutes()), int(d.Seconds())%60)
	}
	return fmt.Sprintf("%dh %dm", int(d.Hours()), int(d.Minutes())%60)
}

// --- Detail view helper functions ---

// taskStatusDisplay returns a CSS class and icon for a task status.
func taskStatusDisplay(status string) (class, icon string) {
	switch status {
	case "done", "merged":
		return "done", "\u2713"
	case "in_progress", "assigned", "running":
		return "running", "\u25cf"
	case "pending":
		return "pending", ""
	case "failed":
		return "failed", "\u25cf"
	default:
		return "pending", ""
	}
}

// roleDisplay returns an uppercase label and CSS class for a role.
func roleDisplay(role string) (label, class string) {
	label = strings.ToUpper(role)
	switch role {
	case "implementer":
		return label, "impl"
	case "architect":
		return label, "arch"
	case "reviewer":
		return label, "rev"
	case "scout":
		return label, "scout"
	case "researcher":
		return label, "research"
	case "editor":
		return label, "editor"
	default:
		return label, "impl"
	}
}

// roleEmoji returns a Noto-friendly emoji for an agent role.
func roleEmoji(role string) string {
	switch role {
	case "architect":
		return "\U0001f4d0"
	case "implementer":
		return "\U0001f916"
	case "reviewer":
		return "\U0001f50d"
	case "scout":
		return "\U0001f9ed"
	case "researcher":
		return "\U0001f4da"
	case "editor":
		return "\u270d\ufe0f"
	default:
		return "\U0001f916"
	}
}

// agentStatusColor returns a CSS variable color name for an agent status.
func agentStatusColor(status string) string {
	switch status {
	case "active", "working":
		return "green"
	case "idle":
		return "dim"
	case "failed", "dead":
		return "red"
	default:
		return "yellow"
	}
}

// roleColorName returns a CSS variable color name for a role.
func roleColorName(role string) string {
	switch role {
	case "implementer":
		return "green"
	case "architect":
		return "blue"
	case "reviewer":
		return "yellow"
	case "scout":
		return "accent"
	case "researcher":
		return "blue"
	case "editor":
		return "dim"
	default:
		return "dim"
	}
}

// eventColorName returns a CSS variable color name for an event type.
func eventColorName(eventType string) string {
	switch {
	case strings.Contains(eventType, "done") || strings.Contains(eventType, "merged") || strings.Contains(eventType, "success"):
		return "green"
	case strings.Contains(eventType, "spawn") || strings.Contains(eventType, "assign") || strings.Contains(eventType, "merge"):
		return "blue"
	case strings.Contains(eventType, "fail") || strings.Contains(eventType, "circuit"):
		return "red"
	default:
		return "dim"
	}
}

// nullStr extracts the string from a sql.NullString, returning "" if null.
func nullStr(ns sql.NullString) string {
	if ns.Valid {
		return ns.String
	}
	return ""
}

// computeSuccessRate returns a display string for success rate.
// Shows as a range (e.g. "78–85%") when sample size < 20, exact when >= 20.
func computeSuccessRate(done, failed int) string {
	total := done + failed
	if total == 0 {
		return "—"
	}
	rate := float64(done) / float64(total) * 100
	return fmt.Sprintf("%.0f%%", rate)
}

// formatConductorName generates a human-readable name for a conductor session.
// Priority: spec title from goal > first task title > formatted session ID.
func formatConductorName(c db.ConductorRecord, allTasks []db.Task) string {
	// Try to extract a meaningful title from the goal
	if c.Goal != "" {
		for _, line := range strings.Split(c.Goal, "\n") {
			line = strings.TrimSpace(line)
			if strings.HasPrefix(line, "Phase:") {
				name := strings.TrimPrefix(line, "Phase:")
				name = strings.TrimSpace(strings.SplitN(name, "—", 2)[0])
				if len(name) > 50 {
					name = name[:47] + "..."
				}
				return name
			}
			if strings.HasPrefix(line, "Project:") {
				name := strings.TrimPrefix(line, "Project:")
				name = strings.TrimSpace(name)
				if len(name) > 50 {
					name = name[:47] + "..."
				}
				return name
			}
		}
	}

	// Fall back to first task title for this conductor
	for _, t := range allTasks {
		if t.ConductorID.Valid && t.ConductorID.String == c.ID {
			title := t.Title
			if len(title) > 50 {
				title = title[:47] + "..."
			}
			return title
		}
	}

	// Last resort: format the session ID nicely
	// s-20260323-230019-9852 -> "Session Mar 23 23:00"
	parts := strings.Split(c.ID, "-")
	if len(parts) >= 3 && parts[0] == "s" {
		dateStr := parts[1] // 20260323
		timeStr := parts[2] // 230019
		if len(dateStr) == 8 && len(timeStr) >= 4 {
			return fmt.Sprintf("Session %s/%s %s:%s", dateStr[4:6], dateStr[6:8], timeStr[0:2], timeStr[2:4])
		}
	}
	return c.ID
}

// formatTextToHTML converts plain text into simple HTML.
// Splits on newlines, wraps in <p> tags, detects fenced code blocks.
// Truncates to ~500 visible chars with a "more" indicator.
func formatTextToHTML(text string) string {
	if text == "" {
		return ""
	}
	lines := strings.Split(text, "\n")
	var buf strings.Builder
	inCode := false
	charCount := 0
	truncated := false

	for _, line := range lines {
		if charCount > 500 {
			truncated = true
			break
		}
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "```") {
			if inCode {
				buf.WriteString("</code></pre>")
				inCode = false
			} else {
				lang := strings.TrimPrefix(trimmed, "```")
				buf.WriteString("<pre><code")
				if lang != "" {
					buf.WriteString(fmt.Sprintf(" class=\"lang-%s\"", template.HTMLEscapeString(lang)))
				}
				buf.WriteString(">")
				inCode = true
			}
			continue
		}
		if inCode {
			buf.WriteString(template.HTMLEscapeString(line))
			buf.WriteString("\n")
			charCount += len(line)
			continue
		}
		if trimmed == "" {
			continue
		}
		buf.WriteString("<p>")
		buf.WriteString(template.HTMLEscapeString(trimmed))
		buf.WriteString("</p>")
		charCount += len(trimmed)
	}
	if inCode {
		buf.WriteString("</code></pre>")
	}
	if truncated {
		buf.WriteString("<p style=\"color:var(--muted);font-style:italic\">... (truncated)</p>")
	}
	return buf.String()
}

// parseAcceptanceCriteria splits acceptance criteria text into checklist items.
func parseAcceptanceCriteria(text string) []ACItem {
	if text == "" {
		return nil
	}
	var items []ACItem
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		done := false
		// Detect checkbox patterns: [x], [X], - [x]
		if strings.HasPrefix(line, "- [x]") || strings.HasPrefix(line, "- [X]") || strings.HasPrefix(line, "[x]") || strings.HasPrefix(line, "[X]") {
			done = true
			line = strings.TrimPrefix(line, "- ")
			line = strings.TrimPrefix(line, "[x] ")
			line = strings.TrimPrefix(line, "[X] ")
		} else {
			// Strip leading markers: "- [ ]", "- ", "* ", numbered
			line = strings.TrimPrefix(line, "- [ ] ")
			line = strings.TrimPrefix(line, "- ")
			line = strings.TrimPrefix(line, "* ")
			// Strip leading number+dot
			for i, c := range line {
				if c == '.' && i > 0 {
					trimmed := strings.TrimSpace(line[i+1:])
					if trimmed != "" {
						line = trimmed
					}
					break
				}
				if c < '0' || c > '9' {
					break
				}
			}
		}
		line = strings.TrimSpace(line)
		if line != "" {
			items = append(items, ACItem{Text: line, Done: done})
		}
	}
	return items
}

// formatConductorGoal extracts a short description from the conductor's goal text.
func formatConductorGoal(c db.ConductorRecord) string {
	if c.Goal == "" {
		return ""
	}
	// Find first meaningful line (skip markers, comments, empty lines, bare YAML keys)
	for _, line := range strings.Split(c.Goal, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, "<") || strings.HasPrefix(line, "{") {
			continue
		}
		if strings.HasPrefix(line, "Phase:") || strings.HasPrefix(line, "Project:") || strings.HasPrefix(line, "Description:") {
			continue // Skip metadata lines, we used them for name
		}
		// Skip bare YAML keys like "corrections:" or "tasks:"
		if strings.HasSuffix(line, ":") && !strings.Contains(line, " ") {
			continue
		}
		// Found a content line
		if len(line) > 80 {
			line = line[:77] + "..."
		}
		return line
	}
	return ""
}

// formatEventText converts raw event type + JSON payload to human-readable text.
func formatEventText(eventType, payload string) string {
	switch eventType {
	case "branch_merged":
		// Extract branch name from JSON
		if strings.Contains(payload, "branch") {
			if i := strings.Index(payload, "feature/"); i >= 0 {
				end := strings.IndexAny(payload[i:], `"}`+",")
				if end > 0 {
					return "Merged " + payload[i:i+end]
				}
			}
		}
		return "Branch merged"
	case "spawner_completed":
		return "Agent completed"
	case "agent_spawned":
		return "Agent spawned"
	case "agent_respawning":
		return "Agent respawning"
	case "task_killed":
		return "Task killed"
	case "spec_generated":
		return "Spec generated"
	case "spec_size_warning":
		return "Spec size warning"
	case "context_budget_check":
		return "Context budget check"
	case "merge_queue_enqueued":
		return "Queued for merge"
	case "staging_merged_to_dev":
		return "Staging merged to dev"
	case "conductor_completed":
		return "Session completed"
	case "validation_failed":
		return "Validation failed"
	case "spawner_validation_failed":
		return "Build/test failed"
	default:
		// Clean up: replace underscores with spaces, truncate
		text := strings.ReplaceAll(eventType, "_", " ")
		if len(text) > 40 {
			text = text[:37] + "..."
		}
		return text
	}
}
