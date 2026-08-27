// handlers.go — Bridge HTTP hook handlers (Telegram integration).
// Dashboard HTTP handlers live in internal/dashboard/handlers.go.
package bridge

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"
)

// handleStop handles POST /hooks/stop — session end notification + reply polling.
func (b *Bridge) handleStop(w http.ResponseWriter, r *http.Request) {
	b.touchActivity()

	body, err := io.ReadAll(r.Body)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, "bad request", "bad_request")
		return
	}

	var input StopHookInput
	if err := json.Unmarshal(body, &input); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid json", "invalid_json")
		return
	}

	cwd := input.Cwd
	if cwd == "" {
		writeJSON(w, HookResponse{})
		return
	}

	ctx := r.Context()
	slug, branch, topicID := b.ResolveProject(ctx, cwd)
	_ = branch

	// Register the project CWD
	ps := b.GetOrCreateProject(slug)
	ps.CWD = cwd
	ps.TopicID = topicID
	ps.Branch = gitBranch(cwd)

	// Build summary
	summary := ""
	readyForAction := false

	if s, ok := ReadSummaryFile(b.cfg.StateBase, slug); ok {
		summary = s
		readyForAction = true
	} else if s, ok := ReadLegacySummary(); ok {
		summary = s
		readyForAction = true
	} else {
		// Fallback to last_assistant_message
		summary = input.LastAssistantMessage
		if len(summary) > 2000 {
			summary = summary[len(summary)-2000:]
		}
	}

	if summary == "" {
		writeJSON(w, HookResponse{})
		return
	}

	// Dedup check
	hash := MD5Hash(summary)
	if ReadSummaryHash(b.cfg.StateBase, slug) == hash {
		writeJSON(w, HookResponse{})
		return
	}

	// Build and send message
	repoName := RepoDisplayName(slug)
	taskName := BranchToTaskName(ps.Branch)
	displayName := ComposeTopicName(repoName, taskName)

	replyTimeout := int(b.cfg.ReplyTimeout.Seconds())
	text := fmt.Sprintf("<b>%s</b>\n%s\n\n<i>Reply within %ds to continue...</i>",
		EscapeHTML(displayName), EscapeHTML(summary), replyTimeout)

	var sentMsgID int64
	if readyForAction {
		kb := GitActionKeyboard(cwd, slug)
		if kb != "" {
			sentMsgID, _ = b.tg.SendWithKeyboard(ctx, text, kb, topicID, "HTML")
		} else {
			sentMsgID, _ = b.tg.SendMessage(ctx, text, topicID, "HTML")
		}
	} else {
		sentMsgID, _ = b.tg.SendMessage(ctx, text, topicID, "HTML")
	}

	// Persist summary
	if readyForAction {
		WriteLastSummary(b.cfg.StateBase, slug, summary)
		RemoveSummaryFile(b.cfg.StateBase, slug)
		RemoveLegacySummary()
	}
	WriteSummaryHash(b.cfg.StateBase, slug, hash)

	// Wait for reply or button press
	update, ok := b.WaitForUpdate(slug, topicID, b.cfg.ReplyTimeout)
	if !ok {
		// Timeout — edit message to indicate session ended
		if sentMsgID > 0 {
			timeoutText := fmt.Sprintf("<b>%s</b>\n%s\n\n<i>Session ended (no reply within %ds)</i>",
				EscapeHTML(displayName), EscapeHTML(summary), replyTimeout)
			b.tg.EditMessage(ctx, sentMsgID, timeoutText, "HTML")
		}
		writeJSON(w, HookResponse{})
		return
	}

	// Process the response
	resp := b.processStopReply(ctx, update, slug, topicID, cwd)
	writeJSON(w, resp)
}

// processStopReply handles a TG update received during stop-hook wait.
func (b *Bridge) processStopReply(ctx context.Context, u TGUpdate, slug string, topicID int64, cwd string) HookResponse {
	// Callback query (button press)
	if u.CallbackQuery != nil {
		data := u.CallbackQuery.Data
		action := data
		if idx := strings.Index(data, ":"); idx >= 0 {
			action = data[:idx]
		}

		switch action {
		case "ship", "merge", "push", "pr":
			// Already handled by router — return empty to let session end
			return HookResponse{}
		}
	}

	// Text message → continue session
	if u.Message != nil && u.Message.Text != "" {
		text := u.Message.Text
		if strings.HasPrefix(text, "/") {
			// Bot command during stop wait — handle it, let session end
			b.HandleBotCommand(ctx, text, topicID)
			return HookResponse{}
		}

		b.tg.SendMessage(ctx, "Continuing in session...", topicID, "")
		return HookResponse{
			Decision: "block",
			Reason:   "TELEGRAM REPLY from user (iPhone): " + text,
		}
	}

	return HookResponse{}
}

// handlePostToolUse handles POST /hooks/post-tool-use — mid-session TG message check.
func (b *Bridge) handlePostToolUse(w http.ResponseWriter, r *http.Request) {
	b.touchActivity()

	body, err := io.ReadAll(r.Body)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, "bad request", "bad_request")
		return
	}

	var input PostToolUseInput
	if err := json.Unmarshal(body, &input); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid json", "invalid_json")
		return
	}

	cwd := input.Cwd
	if cwd == "" {
		writeJSON(w, HookResponse{})
		return
	}

	ctx := r.Context()
	slug, _, topicID := b.ResolveProject(ctx, cwd)

	// Register CWD so poller can route messages to this project
	ps := b.GetOrCreateProject(slug)
	ps.CWD = cwd
	ps.TopicID = topicID

	if b.isRateLimited(slug) {
		writeJSON(w, HookResponse{})
		return
	}

	// Check for queued messages
	msgs := ps.DrainQueue()
	if len(msgs) == 0 {
		writeJSON(w, HookResponse{})
		return
	}

	// Aggregate text messages
	var texts []string
	for _, u := range msgs {
		if u.Message != nil && u.Message.Text != "" {
			text := u.Message.Text
			if strings.HasPrefix(text, "/") {
				// Handle commands inline
				ctx := r.Context()
				b.HandleBotCommand(ctx, text, u.Message.MessageThreadID)
				continue
			}
			texts = append(texts, text)
		}
		// Callback queries in the queue were already handled by router
	}

	if len(texts) == 0 {
		writeJSON(w, HookResponse{})
		return
	}

	// Acknowledge on TG
	b.tg.SendMessage(ctx, "Received mid-task, Claude will see this shortly...", topicID, "")

	combined := strings.Join(texts, "\n---\n")

	writeJSON(w, HookResponse{
		Decision: "block",
		Reason:   "TELEGRAM MESSAGE from user: " + combined + "\n\nIMPORTANT: Write your full response to the session-summary.txt file so it gets delivered back to Telegram. Do not condense or shorten — write the same text you would show in the conversation.",
	})
}

// handlePermission handles POST /hooks/permission — relay permission prompts to TG.
func (b *Bridge) handlePermission(w http.ResponseWriter, r *http.Request) {
	b.touchActivity()

	body, err := io.ReadAll(r.Body)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, "bad request", "bad_request")
		return
	}

	var input PermissionInput
	if err := json.Unmarshal(body, &input); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid json", "invalid_json")
		return
	}

	// Skip AskUserQuestion — handled by /hooks/ask-user
	if input.ToolName == "AskUserQuestion" {
		writeJSON(w, HookResponse{})
		return
	}

	cwd := input.Cwd
	if cwd == "" {
		writeJSON(w, HookResponse{})
		return
	}

	ctx := r.Context()
	slug, _, topicID := b.ResolveProject(ctx, cwd)

	// Register CWD
	ps := b.GetOrCreateProject(slug)
	ps.CWD = cwd

	// Build description
	description := buildPermDescription(input)

	requestID := GenerateRequestID()
	kb := ApprovalKeyboard(requestID)

	msgText := fmt.Sprintf("<b>Permission Request</b>\n\n%s\n\n<i>Approve to allow, or wait for terminal prompt.</i>", description)
	sentMsgID, _ := b.tg.SendWithKeyboard(ctx, msgText, kb, topicID, "HTML")

	// Wait for callback
	update, ok := b.WaitForUpdate(slug, topicID, DefaultPermWait)
	if !ok {
		// Timeout
		if sentMsgID > 0 {
			b.tg.EditMessage(ctx, sentMsgID, description+"\n\n<i>Timed out \u2014 prompting in terminal</i>", "HTML")
		}
		writeJSON(w, HookResponse{})
		return
	}

	// Process callback
	if update.CallbackQuery != nil {
		data := update.CallbackQuery.Data
		action := data
		if idx := strings.Index(data, ":"); idx >= 0 {
			action = data[:idx]
		}

		b.tg.AnswerCallback(ctx, update.CallbackQuery.ID, titleCase(action)+"d!")

		if sentMsgID > 0 {
			b.tg.EditMessage(ctx, sentMsgID, description+"\n\n<b>Decision:</b> "+action, "HTML")
		}

		if action == "approve" {
			writeJSON(w, HookResponse{
				HookSpecificOutput: &HookSpecificOutput{
					HookEventName: "PermissionRequest",
					Decision:      &PermissionDecision{Behavior: "allow"},
				},
			})
			return
		}
	}

	// Deny, skip, or text — fall through
	writeJSON(w, HookResponse{})
}

// handleAskUser handles POST /hooks/ask-user — relay questions with option buttons.
func (b *Bridge) handleAskUser(w http.ResponseWriter, r *http.Request) {
	b.touchActivity()

	body, err := io.ReadAll(r.Body)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, "bad request", "bad_request")
		return
	}

	var input PermissionInput
	if err := json.Unmarshal(body, &input); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid json", "invalid_json")
		return
	}

	if input.ToolName != "AskUserQuestion" {
		writeJSON(w, HookResponse{})
		return
	}

	cwd := input.Cwd
	if cwd == "" {
		writeJSON(w, HookResponse{})
		return
	}

	ctx := r.Context()
	slug, _, topicID := b.ResolveProject(ctx, cwd)
	ps := b.GetOrCreateProject(slug)
	ps.CWD = cwd

	// Parse questions
	var toolInput AskUserQuestionInput
	if err := json.Unmarshal(input.ToolInput, &toolInput); err != nil {
		writeJSON(w, HookResponse{})
		return
	}
	if len(toolInput.Questions) == 0 {
		writeJSON(w, HookResponse{})
		return
	}

	requestID := GenerateRequestID()
	answers := make(map[string]string)

	for i, q := range toolInput.Questions {
		if q.Question == "" {
			continue
		}

		kb := AskUserKeyboard(requestID, i, q.Options)
		header := q.Header
		if header == "" {
			header = "Question"
		}

		msgText := fmt.Sprintf("<b>%s</b>\n%s", EscapeHTML(header), EscapeHTML(q.Question))

		// Add option descriptions
		for _, opt := range q.Options {
			if opt.Description != "" {
				msgText += fmt.Sprintf("\n  <b>%s</b>: %s", EscapeHTML(opt.Label), EscapeHTML(opt.Description))
			}
		}
		msgText += "\n\n<i>Tap to select, or type a reply for Other.</i>"

		sentMsgID, _ := b.tg.SendWithKeyboard(ctx, msgText, kb, topicID, "HTML")

		// Poll for answer
		selectedLabel := b.waitForAskUserAnswer(ctx, slug, topicID, requestID, i, q, sentMsgID, msgText)
		if selectedLabel != "" {
			answers[q.Question] = selectedLabel
		}
	}

	// Return answer injection response
	writeJSON(w, HookResponse{
		HookSpecificOutput: &HookSpecificOutput{
			HookEventName: "PermissionRequest",
			Decision: &PermissionDecision{
				Behavior: "allow",
				UpdatedInput: &UpdatedInput{
					Questions: toolInput.Questions,
					Answers:   answers,
				},
			},
		},
	})
}

// waitForAskUserAnswer waits for the user to select an option or type a reply.
func (b *Bridge) waitForAskUserAnswer(ctx context.Context, slug string, topicID int64, requestID string, qIdx int, q AskUserQ, sentMsgID int64, msgText string) string {
	prefix := fmt.Sprintf("aq:%s:%d:", requestID, qIdx)
	timeout := DefaultAskUserWait

	ps := b.GetOrCreateProject(slug)
	ch := ps.AddWaiter(topicID)
	defer ps.RemoveWaiter(ch)

	timer := time.NewTimer(timeout)
	defer timer.Stop()

	for {
		select {
		case u := <-ch:
			// Callback query
			if u.CallbackQuery != nil && strings.HasPrefix(u.CallbackQuery.Data, prefix) {
				data := u.CallbackQuery.Data
				optPart := strings.TrimPrefix(data, prefix)

				if optPart == "other" {
					b.tg.AnswerCallback(ctx, u.CallbackQuery.ID, "Type your answer...")
					if sentMsgID > 0 {
						b.tg.EditMessage(ctx, sentMsgID, msgText+"\n\n<i>Type your answer as a reply...</i>", "HTML")
					}
					// Wait for text reply
					return b.waitForFreeText(ch, timer, slug, topicID, q, sentMsgID, msgText)
				}

				// Direct option selection
				var optIdx int
				fmt.Sscanf(optPart, "%d", &optIdx)
				label := ""
				if optIdx >= 0 && optIdx < len(q.Options) {
					label = q.Options[optIdx].Label
				}
				b.tg.AnswerCallback(ctx, u.CallbackQuery.ID, "Selected: "+label)
				if sentMsgID > 0 {
					b.tg.EditMessage(ctx, sentMsgID, msgText+"\n\n<b>Selected:</b> "+EscapeHTML(label), "HTML")
				}
				return label
			}

			// Unsolicited text message as "Other" answer
			if u.Message != nil && u.Message.Text != "" && !strings.HasPrefix(u.Message.Text, "/") {
				label := u.Message.Text
				if sentMsgID > 0 {
					b.tg.EditMessage(ctx, sentMsgID, msgText+"\n\n<b>Answered:</b> "+EscapeHTML(label), "HTML")
				}
				return label
			}

		case <-timer.C:
			// Timeout — pick first option
			label := ""
			if len(q.Options) > 0 {
				label = q.Options[0].Label
			}
			if sentMsgID > 0 {
				b.tg.EditMessage(ctx, sentMsgID, msgText+"\n\n<b>Timed out \u2014 auto-selected:</b> "+EscapeHTML(label), "HTML")
			}
			return label
		}
	}
}

func (b *Bridge) waitForFreeText(ch chan TGUpdate, timer *time.Timer, slug string, topicID int64, q AskUserQ, sentMsgID int64, msgText string) string {
	for {
		select {
		case u := <-ch:
			if u.Message != nil && u.Message.Text != "" {
				label := u.Message.Text
				if sentMsgID > 0 {
					b.tg.EditMessage(context.Background(), sentMsgID, msgText+"\n\n<b>Answered:</b> "+EscapeHTML(label), "HTML")
				}
				return label
			}
		case <-timer.C:
			label := ""
			if len(q.Options) > 0 {
				label = q.Options[0].Label
			}
			return label
		}
	}
}

// handlePreToolUse handles POST /hooks/pre-tool-use — branch guard.
func (b *Bridge) handlePreToolUse(w http.ResponseWriter, r *http.Request) {
	b.touchActivity()

	body, err := io.ReadAll(r.Body)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, "bad request", "bad_request")
		return
	}

	var input PreToolUseInput
	if err := json.Unmarshal(body, &input); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid json", "invalid_json")
		return
	}

	if input.ToolName != "Bash" {
		writeJSON(w, HookResponse{})
		return
	}

	var bashInput BashToolInput
	if err := json.Unmarshal(input.ToolInput, &bashInput); err != nil {
		writeJSON(w, HookResponse{})
		return
	}

	cmd := bashInput.Command
	cwd := input.Cwd

	// Branch guard: block git commit and git add on dev/main/master
	if cwd != "" && isGitGuarded(cmd) {
		branch := gitBranch(cwd)
		if branch == "dev" || branch == "main" || branch == "master" {
			writeJSON(w, map[string]interface{}{
				"decision": "block",
				"reason":   fmt.Sprintf("Direct commits to '%s' are not allowed by project workflow.", branch),
				"hookSpecificOutput": map[string]interface{}{
					"hookEventName":            "PreToolUse",
					"permissionDecision":       "deny",
					"permissionDecisionReason": fmt.Sprintf("Branch protection: '%s' is a protected branch.", branch),
					"additionalContext":        fmt.Sprintf("BRANCH PROTECTION: You are on '%s' which is a protected branch. You MUST create a feature branch before making any commits. Run: git checkout -b feature/<descriptive-name> — then retry your commit on the new branch. All work must happen on feature branches per project workflow (feature branch → PR → merge to dev).", branch),
				},
			})
			return
		}
	}

	writeJSON(w, HookResponse{})
}

// handleHealth handles GET /health.
func (b *Bridge) handleHealth(w http.ResponseWriter, r *http.Request) {
	b.mu.RLock()
	projectCount := len(b.projects)
	b.mu.RUnlock()

	uptime := time.Since(b.startTime).Round(time.Second)
	resp := map[string]interface{}{
		"status":   "ok",
		"uptime":   uptime.String(),
		"projects": projectCount,
		"poller":   "running",
		"offset":   b.offset,
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

// --- Helpers ---

func buildPermDescription(input PermissionInput) string {
	switch input.ToolName {
	case "ExitPlanMode":
		return "<b>Plan Ready for Review</b>"
	case "Bash":
		var bash BashToolInput
		json.Unmarshal(input.ToolInput, &bash)
		cmd := bash.Command
		if len(cmd) > 300 {
			cmd = cmd[:300] + "..."
		}
		return "<b>Run command:</b>\n<code>" + EscapeHTML(cmd) + "</code>"
	case "Edit":
		var edit EditToolInput
		json.Unmarshal(input.ToolInput, &edit)
		return "<b>Edit file:</b> <code>" + EscapeHTML(edit.FilePath) + "</code>"
	case "Write":
		var write WriteToolInput
		json.Unmarshal(input.ToolInput, &write)
		return "<b>Write file:</b> <code>" + EscapeHTML(write.FilePath) + "</code>"
	default:
		toolInput := string(input.ToolInput)
		if len(toolInput) > 200 {
			toolInput = toolInput[:200]
		}
		return "<b>Tool:</b> " + EscapeHTML(input.ToolName) + "\n<code>" + EscapeHTML(toolInput) + "</code>"
	}
}

// handlePending handles GET /pending/<slug> — returns queued TG messages as plain text.
// Called by session-start-inject.sh to inject pending messages at session start
// without competing with the bridge's poller for getUpdates.
func (b *Bridge) handlePending(w http.ResponseWriter, r *http.Request) {
	b.touchActivity()

	// Extract slug from URL: /pending/claude-orchestra → claude-orchestra
	slug := strings.TrimPrefix(r.URL.Path, "/pending/")
	if slug == "" {
		writeJSONError(w, http.StatusBadRequest, "missing slug", "missing_slug")
		return
	}

	ps := b.GetOrCreateProject(slug)
	msgs := ps.DrainQueue()
	if len(msgs) == 0 {
		w.WriteHeader(http.StatusNoContent)
		return
	}

	// Return as plain text (session-start-inject.sh outputs to stdout)
	w.Header().Set("Content-Type", "text/plain")
	for _, u := range msgs {
		if u.Message != nil && u.Message.Text != "" {
			fmt.Fprintf(w, "%s\n", u.Message.Text)
		}
	}
}

// handleHITL handles POST /hooks/hitl — receives HITL interaction requests from agents
// and logs them for dashboard pickup. Agents write HITL items to the blackboard directly;
// this endpoint acknowledges receipt so the bridge can track HITL activity.
func (b *Bridge) handleHITL(w http.ResponseWriter, r *http.Request) {
	b.touchActivity()

	if r.Method != http.MethodPost {
		writeJSONError(w, http.StatusMethodNotAllowed, "method not allowed", "method_not_allowed")
		return
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, "bad request", "bad_request")
		return
	}

	var input struct {
		ConductorID string `json:"conductor_id"`
		Type        string `json:"type"` // "plan", "question", "failure"
		Message     string `json:"message"`
	}
	if err := json.Unmarshal(body, &input); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid json", "invalid_json")
		return
	}

	slog.Info("HITL event", "conductor", input.ConductorID, "type", input.Type)

	writeJSON(w, HookResponse{})
}

func isGitGuarded(cmd string) bool {
	return strings.Contains(cmd, "git commit") || strings.Contains(cmd, "git add")
}

func writeJSON(w http.ResponseWriter, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(v); err != nil {
		slog.Error("failed to encode JSON response", "err", err)
	}
}

func writeJSONError(w http.ResponseWriter, status int, message string, code string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(map[string]string{"error": message, "code": code})
}
