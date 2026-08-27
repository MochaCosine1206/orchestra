package dashboard

import (
	"net/http"
	"strings"

	orchestra "github.com/MochaCosine1206/orchestra"
)

// ArtifactType categorizes artifacts for filtering.
type ArtifactType string

const (
	ArtifactDiff       ArtifactType = "diff"
	ArtifactReview     ArtifactType = "review"
	ArtifactLog        ArtifactType = "log"
	ArtifactMarkdown   ArtifactType = "markdown"
	ArtifactResult     ArtifactType = "result"
	ArtifactReconcile  ArtifactType = "reconcile"
)

// Artifact represents a single artifact entry for display.
type Artifact struct {
	ID          string
	SessionID   string
	Type        ArtifactType
	Title       string
	Content     string // raw content
	PreviewHTML string // rendered HTML preview
}

// ArtifactSession groups artifacts by conductor session.
type ArtifactSession struct {
	SessionID string
	Goal      string
	Artifacts []Artifact
}

// ArtifactData holds all data for the artifacts template.
type ArtifactData struct {
	Sessions       []ArtifactSession
	FilterType     string
	FilterSession  string
	AllTypes       []string
	AllSessionIDs  []string
	HasArtifacts   bool
	PluginNote     string
}

// handleArtifactsFull renders the artifacts page with data from blackboard and DB.
func (s *Server) handleArtifactsFull(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	filterType := r.URL.Query().Get("type")
	filterSession := r.URL.Query().Get("session")

	// Collect artifacts from various sources
	var allArtifacts []Artifact

	// 1. Reconciliation reports from blackboard (reconcile:report:*)
	reconcileEntries, _ := s.DB.ListBlackboardByPrefix(ctx, "reconcile:report:")
	for _, entry := range reconcileEntries {
		sessionID := strings.TrimPrefix(entry.Key, "reconcile:report:")
		art := Artifact{
			ID:        entry.Key,
			SessionID: sessionID,
			Type:      ArtifactReconcile,
			Title:     "Reconciliation Report — " + sessionID,
			Content:   entry.Value,
		}
		art.PreviewHTML = orchestra.RenderMarkdown(entry.Value)
		allArtifacts = append(allArtifacts, art)
	}

	// 2. Task results from completed tasks
	tasks, _ := s.DB.ListTasks(ctx)
	for _, t := range tasks {
		if !t.Result.Valid || t.Result.String == "" {
			continue
		}
		sessionID := ""
		if t.ConductorID.Valid {
			sessionID = t.ConductorID.String
		}
		art := Artifact{
			ID:        "result:" + t.ID,
			SessionID: sessionID,
			Type:      ArtifactResult,
			Title:     "Task Result — " + t.Title,
			Content:   t.Result.String,
		}
		art.PreviewHTML = orchestra.RenderMarkdown(t.Result.String)
		allArtifacts = append(allArtifacts, art)
	}

	// 3. Review reports from blackboard (review:report:*)
	reviewEntries, _ := s.DB.ListBlackboardByPrefix(ctx, "review:report:")
	for _, entry := range reviewEntries {
		parts := strings.SplitN(entry.Key, ":", 3)
		taskID := ""
		if len(parts) >= 3 {
			taskID = parts[2]
		}
		art := Artifact{
			ID:        entry.Key,
			SessionID: taskID,
			Type:      ArtifactReview,
			Title:     "Review Report — " + taskID,
			Content:   entry.Value,
		}
		art.PreviewHTML = orchestra.RenderMarkdown(entry.Value)
		allArtifacts = append(allArtifacts, art)
	}

	// 4. Diff results from blackboard (result_summary:*)
	diffEntries, _ := s.DB.ListBlackboardByPrefix(ctx, "result_summary:")
	for _, entry := range diffEntries {
		taskID := strings.TrimPrefix(entry.Key, "result_summary:")
		sessionID := ""
		// Try to find the conductor for this task
		for _, t := range tasks {
			if t.ID == taskID && t.ConductorID.Valid {
				sessionID = t.ConductorID.String
				break
			}
		}
		art := Artifact{
			ID:        entry.Key,
			SessionID: sessionID,
			Type:      ArtifactDiff,
			Title:     "Merged Diff — " + taskID,
			Content:   entry.Value,
		}
		// Use diff renderer if content looks like a diff, otherwise markdown
		if strings.Contains(entry.Value, "@@") || strings.HasPrefix(entry.Value, "diff ") {
			art.PreviewHTML = orchestra.RenderDiffView(entry.Value)
		} else {
			art.PreviewHTML = orchestra.RenderMarkdown(entry.Value)
		}
		allArtifacts = append(allArtifacts, art)
	}

	// Apply filters
	var filtered []Artifact
	for _, a := range allArtifacts {
		if filterType != "" && string(a.Type) != filterType {
			continue
		}
		if filterSession != "" && a.SessionID != filterSession {
			continue
		}
		filtered = append(filtered, a)
	}

	// Group by session
	sessionMap := make(map[string]*ArtifactSession)
	var sessionOrder []string
	for _, a := range filtered {
		sid := a.SessionID
		if sid == "" {
			sid = "ungrouped"
		}
		if _, exists := sessionMap[sid]; !exists {
			sessionMap[sid] = &ArtifactSession{SessionID: sid}
			sessionOrder = append(sessionOrder, sid)
		}
		sessionMap[sid].Artifacts = append(sessionMap[sid].Artifacts, a)
	}

	// Fill in conductor goals
	conductors, _ := s.DB.ListAllConductors(ctx)
	for _, c := range conductors {
		if sess, ok := sessionMap[c.ID]; ok {
			sess.Goal = c.Goal
		}
	}

	var sessions []ArtifactSession
	for _, sid := range sessionOrder {
		sessions = append(sessions, *sessionMap[sid])
	}

	// Collect unique session IDs and types for filter dropdowns
	typeSet := make(map[string]bool)
	sessionIDSet := make(map[string]bool)
	for _, a := range allArtifacts {
		typeSet[string(a.Type)] = true
		if a.SessionID != "" {
			sessionIDSet[a.SessionID] = true
		}
	}
	var allTypes []string
	for t := range typeSet {
		allTypes = append(allTypes, t)
	}
	var allSessionIDs []string
	for s := range sessionIDSet {
		allSessionIDs = append(allSessionIDs, s)
	}

	data := ArtifactData{
		Sessions:      sessions,
		FilterType:    filterType,
		FilterSession: filterSession,
		AllTypes:      allTypes,
		AllSessionIDs: allSessionIDs,
		HasArtifacts:  len(allArtifacts) > 0,
		PluginNote:    "Plugin system: modular template blocks for custom artifact renderers",
	}

	s.renderTemplate(w, "artifacts", data)
}
