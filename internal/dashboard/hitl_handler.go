package dashboard

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// registerHITLRoutes adds all HITL interaction routes to the mux.
func (s *Server) registerHITLRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/api/hitl/plan/", s.handleHITLPlan)
	mux.HandleFunc("/api/hitl/clarify/", s.handleHITLClarify)
	mux.HandleFunc("/api/hitl/merge/", s.handleHITLMerge)
	mux.HandleFunc("/api/hitl/failure/", s.handleHITLFailure)
	mux.HandleFunc("/api/hitl/priority/", s.handleHITLPriority)
	mux.HandleFunc("/api/factory/emergency-stop", s.handleEmergencyStop)
}

// --- Plan Approval ---

func (s *Server) handleHITLPlan(w http.ResponseWriter, r *http.Request) {
	conductorID := extractID(r.URL.Path, "/api/hitl/plan/")
	if conductorID == "" {
		http.NotFound(w, r)
		return
	}
	ctx := r.Context()

	switch r.Method {
	case http.MethodGet:
		key := fmt.Sprintf("hitl:plan_approval:%s", conductorID)
		val, err := s.DB.GetBlackboardValue(ctx, key)
		if err != nil || val == "" {
			s.renderTemplate(w, "hitl-plan-approval", map[string]interface{}{
				"ConductorID": conductorID,
				"HasPlan":     false,
			})
			return
		}

		// Parse the plan JSON from blackboard
		var plan []map[string]interface{}
		if err := json.Unmarshal([]byte(val), &plan); err != nil {
			// Treat non-JSON value as a single-item plan description
			plan = []map[string]interface{}{{"title": val}}
		}

		s.renderTemplate(w, "hitl-plan-approval", map[string]interface{}{
			"ConductorID": conductorID,
			"HasPlan":     true,
			"Tasks":       plan,
		})

	case http.MethodPost:
		body, err := io.ReadAll(io.LimitReader(r.Body, 8192))
		if err != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		var payload struct {
			Action string `json:"action"`
			Notes  string `json:"notes"`
		}
		if err := json.Unmarshal(body, &payload); err != nil {
			http.Error(w, "invalid JSON", http.StatusBadRequest)
			return
		}

		writer := "dashboard-hitl"
		key := fmt.Sprintf("hitl:plan_approval:%s", conductorID)

		switch payload.Action {
		case "approve":
			_ = s.DB.DeleteBlackboard(ctx, key)
			_ = s.DB.SetBlackboard(ctx, fmt.Sprintf("hitl:plan_approved:%s", conductorID), "approved", writer)
			writeJSON(w, map[string]string{"status": "ok", "action": "approved"})
		case "reject":
			_ = s.DB.DeleteBlackboard(ctx, key)
			_ = s.DB.SetBlackboard(ctx, fmt.Sprintf("hitl:plan_rejected:%s", conductorID), payload.Notes, writer)
			writeJSON(w, map[string]string{"status": "ok", "action": "rejected"})
		default:
			http.Error(w, "invalid action", http.StatusBadRequest)
		}

	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

// --- Clarification ---

func (s *Server) handleHITLClarify(w http.ResponseWriter, r *http.Request) {
	conductorID := extractID(r.URL.Path, "/api/hitl/clarify/")
	if conductorID == "" {
		http.NotFound(w, r)
		return
	}
	ctx := r.Context()

	switch r.Method {
	case http.MethodGet:
		key := fmt.Sprintf("hitl:clarify:%s", conductorID)
		val, err := s.DB.GetBlackboardValue(ctx, key)
		if err != nil || val == "" {
			s.renderTemplate(w, "hitl-clarify", map[string]interface{}{
				"ConductorID": conductorID,
				"HasQuestion": false,
			})
			return
		}

		// Parse clarification request: {"question":"...", "options":["a","b","c"]}
		var clarify struct {
			Question string   `json:"question"`
			Options  []string `json:"options"`
		}
		if err := json.Unmarshal([]byte(val), &clarify); err != nil {
			clarify.Question = val
		}

		s.renderTemplate(w, "hitl-clarify", map[string]interface{}{
			"ConductorID": conductorID,
			"HasQuestion": true,
			"Question":    clarify.Question,
			"Options":     clarify.Options,
		})

	case http.MethodPost:
		body, err := io.ReadAll(io.LimitReader(r.Body, 4096))
		if err != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		var payload struct {
			Response string `json:"response"`
		}
		if err := json.Unmarshal(body, &payload); err != nil {
			http.Error(w, "invalid JSON", http.StatusBadRequest)
			return
		}

		writer := "dashboard-hitl"
		_ = s.DB.DeleteBlackboard(ctx, fmt.Sprintf("hitl:clarify:%s", conductorID))
		_ = s.DB.SetBlackboard(ctx, fmt.Sprintf("hitl:clarify_response:%s", conductorID), payload.Response, writer)
		writeJSON(w, map[string]string{"status": "ok", "action": "clarified"})

	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

// --- Merge Approval ---

func (s *Server) handleHITLMerge(w http.ResponseWriter, r *http.Request) {
	taskID := extractID(r.URL.Path, "/api/hitl/merge/")
	if taskID == "" {
		http.NotFound(w, r)
		return
	}
	ctx := r.Context()

	switch r.Method {
	case http.MethodGet:
		key := fmt.Sprintf("hitl:merge_approval:%s", taskID)
		val, err := s.DB.GetBlackboardValue(ctx, key)
		if err != nil || val == "" {
			s.renderTemplate(w, "hitl-merge", map[string]interface{}{
				"TaskID":   taskID,
				"HasMerge": false,
			})
			return
		}

		// Parse merge data: {"summary":"...", "diff":"..."}
		var merge struct {
			Summary string `json:"summary"`
			Diff    string `json:"diff"`
		}
		if err := json.Unmarshal([]byte(val), &merge); err != nil {
			merge.Summary = val
		}

		s.renderTemplate(w, "hitl-merge", map[string]interface{}{
			"TaskID":   taskID,
			"HasMerge": true,
			"Summary":  merge.Summary,
			"Diff":     merge.Diff,
		})

	case http.MethodPost:
		body, err := io.ReadAll(io.LimitReader(r.Body, 4096))
		if err != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		var payload struct {
			Action string `json:"action"`
			Notes  string `json:"notes"`
		}
		if err := json.Unmarshal(body, &payload); err != nil {
			http.Error(w, "invalid JSON", http.StatusBadRequest)
			return
		}

		writer := "dashboard-hitl"
		key := fmt.Sprintf("hitl:merge_approval:%s", taskID)

		switch payload.Action {
		case "approve":
			_ = s.DB.DeleteBlackboard(ctx, key)
			_ = s.DB.SetBlackboard(ctx, fmt.Sprintf("hitl:merge_approved:%s", taskID), "approved", writer)
			writeJSON(w, map[string]string{"status": "ok", "action": "approved"})
		case "reject":
			_ = s.DB.DeleteBlackboard(ctx, key)
			_ = s.DB.SetBlackboard(ctx, fmt.Sprintf("hitl:merge_rejected:%s", taskID), payload.Notes, writer)
			writeJSON(w, map[string]string{"status": "ok", "action": "rejected"})
		default:
			http.Error(w, "invalid action", http.StatusBadRequest)
		}

	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

// --- Failure Triage ---

func (s *Server) handleHITLFailure(w http.ResponseWriter, r *http.Request) {
	taskID := extractID(r.URL.Path, "/api/hitl/failure/")
	if taskID == "" {
		http.NotFound(w, r)
		return
	}
	ctx := r.Context()

	switch r.Method {
	case http.MethodGet:
		task, err := s.DB.GetTaskByID(ctx, taskID)
		if err != nil || task == nil {
			http.NotFound(w, r)
			return
		}

		classification := ""
		if v, err := s.DB.GetBlackboardValue(ctx, "failure_type:"+taskID); err == nil {
			classification = v
		}

		retryCount := ""
		if v, err := s.DB.GetBlackboardValue(ctx, "retry:"+taskID); err == nil {
			retryCount = v
		}

		errorMsg := ""
		if task.Result.Valid {
			errorMsg = task.Result.String
		}

		s.renderTemplate(w, "hitl-failure", map[string]interface{}{
			"TaskID":         taskID,
			"TaskTitle":      task.Title,
			"Classification": classification,
			"RetryCount":     retryCount,
			"ErrorMessage":   errorMsg,
			"Status":         task.Status,
		})

	case http.MethodPost:
		body, err := io.ReadAll(io.LimitReader(r.Body, 4096))
		if err != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		var payload struct {
			Action string `json:"action"`
		}
		if err := json.Unmarshal(body, &payload); err != nil {
			http.Error(w, "invalid JSON", http.StatusBadRequest)
			return
		}

		writer := "dashboard-hitl"
		switch payload.Action {
		case "retry":
			_ = s.DB.SetBlackboard(ctx, fmt.Sprintf("auto:retry:%s", taskID), "requested", writer)
			writeJSON(w, map[string]string{"status": "ok", "action": "retry"})
		case "skip":
			_ = s.DB.SetBlackboard(ctx, fmt.Sprintf("auto:skip:%s", taskID), "requested", writer)
			writeJSON(w, map[string]string{"status": "ok", "action": "skip"})
		case "replan":
			task, _ := s.DB.GetTaskByID(ctx, taskID)
			conductorID := ""
			if task != nil && task.ConductorID.Valid {
				conductorID = task.ConductorID.String
			}
			_ = s.DB.SetBlackboard(ctx, "auto:replan", conductorID, writer)
			writeJSON(w, map[string]string{"status": "ok", "action": "replan"})
		default:
			http.Error(w, "invalid action", http.StatusBadRequest)
		}

	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

// --- Priority Override ---

func (s *Server) handleHITLPriority(w http.ResponseWriter, r *http.Request) {
	conductorID := extractID(r.URL.Path, "/api/hitl/priority/")
	if conductorID == "" {
		http.NotFound(w, r)
		return
	}

	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	body, err := io.ReadAll(io.LimitReader(r.Body, 8192))
	if err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	var payload struct {
		TaskOrder []string `json:"task_order"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return
	}

	writer := "dashboard-hitl"
	orderJSON, _ := json.Marshal(payload.TaskOrder)
	_ = s.DB.SetBlackboard(r.Context(), fmt.Sprintf("hitl:priority_order:%s", conductorID), string(orderJSON), writer)
	writeJSON(w, map[string]string{"status": "ok", "action": "priority-updated", "count": fmt.Sprintf("%d", len(payload.TaskOrder))})
}

// --- Emergency Stop ---

func (s *Server) handleEmergencyStop(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	ctx := r.Context()
	conductors, err := s.DB.ListAllConductors(ctx)
	if err != nil {
		writeJSON(w, map[string]string{"error": err.Error()})
		return
	}

	writer := "dashboard-hitl"
	stopped := 0
	for _, c := range conductors {
		if err := s.DB.SetBlackboard(ctx, fmt.Sprintf("auto:stop:%s", c.ID), "emergency", writer); err == nil {
			stopped++
		}
	}

	// Also write a global stop signal
	_ = s.DB.SetBlackboard(ctx, "auto:emergency_stop", "true", writer)

	writeJSON(w, map[string]string{
		"status": "ok",
		"action": "emergency-stop",
		"count":  fmt.Sprintf("%d", stopped),
	})
}

// extractFilesOwned extracts file assignments from a plan task entry.
func extractFilesOwned(task map[string]interface{}) string {
	if files, ok := task["files_owned"]; ok {
		switch v := files.(type) {
		case []interface{}:
			var names []string
			for _, f := range v {
				names = append(names, fmt.Sprintf("%v", f))
			}
			return strings.Join(names, ", ")
		case string:
			return v
		}
	}
	return ""
}
