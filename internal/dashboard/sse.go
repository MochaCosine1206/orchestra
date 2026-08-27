package dashboard

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"time"
)

const (
	ssePollInterval      = 2 * time.Second
	sseKeepaliveInterval = 15 * time.Second
)

// sseEvent is a JSON payload sent over the SSE stream.
type sseEvent struct {
	Type string      `json:"type"`
	Data interface{} `json:"data"`
}

// sseState tracks what was last sent so we can detect changes.
type sseState struct {
	lastEventID   int64
	taskHash      string
	agentHash     string
	conductorHash string
	mergeHash     string
}

// handleSSE implements Server-Sent Events via http.Flusher.
// Polls SQLite every 2s for changes and emits typed JSON events.
// Sends keepalive comments every 15s.
func (s *Server) handleSSE(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	flusher.Flush()

	ctx := r.Context()
	pollTicker := time.NewTicker(ssePollInterval)
	keepaliveTicker := time.NewTicker(sseKeepaliveInterval)
	defer pollTicker.Stop()
	defer keepaliveTicker.Stop()

	var state sseState

	// Initial poll
	s.pollAndEmit(ctx, w, flusher, &state)

	for {
		select {
		case <-ctx.Done():
			return
		case <-pollTicker.C:
			s.pollAndEmit(ctx, w, flusher, &state)
		case <-keepaliveTicker.C:
			fmt.Fprintf(w, ": keepalive\n\n")
			flusher.Flush()
		}
	}
}

func (s *Server) pollAndEmit(ctx context.Context, w http.ResponseWriter, flusher http.Flusher, state *sseState) {
	// Check for new events
	s.emitNewEvents(ctx, w, flusher, state)

	// Check task changes
	s.emitTaskUpdates(ctx, w, flusher, state)

	// Check agent changes
	s.emitAgentUpdates(ctx, w, flusher, state)

	// Check conductor changes
	s.emitConductorUpdates(ctx, w, flusher, state)

	// Check merge queue changes
	s.emitMergeUpdates(ctx, w, flusher, state)
}

func (s *Server) emitNewEvents(ctx context.Context, w http.ResponseWriter, flusher http.Flusher, state *sseState) {
	events, err := s.DB.RecentEvents(ctx, 50)
	if err != nil {
		return
	}
	for i := len(events) - 1; i >= 0; i-- {
		ev := events[i]
		if ev.ID > state.lastEventID {
			sendSSE(w, flusher, "event_new", ev)
			state.lastEventID = ev.ID
		}
	}
}

func (s *Server) emitTaskUpdates(ctx context.Context, w http.ResponseWriter, flusher http.Flusher, state *sseState) {
	tasks, err := s.DB.ListTasks(ctx)
	if err != nil {
		return
	}
	hash := quickHash(tasks)
	if hash != state.taskHash {
		state.taskHash = hash
		sendSSE(w, flusher, "task_update", tasks)
	}
}

func (s *Server) emitAgentUpdates(ctx context.Context, w http.ResponseWriter, flusher http.Flusher, state *sseState) {
	agents, err := s.DB.ListAgents(ctx)
	if err != nil {
		return
	}
	hash := quickHash(agents)
	if hash != state.agentHash {
		state.agentHash = hash
		sendSSE(w, flusher, "agent_update", agents)
	}
}

func (s *Server) emitConductorUpdates(ctx context.Context, w http.ResponseWriter, flusher http.Flusher, state *sseState) {
	conductors, err := s.DB.ListActiveConductors(ctx)
	if err != nil {
		return
	}
	hash := quickHash(conductors)
	if hash != state.conductorHash {
		state.conductorHash = hash
		sendSSE(w, flusher, "conductor_update", conductors)
	}
}

func (s *Server) emitMergeUpdates(ctx context.Context, w http.ResponseWriter, flusher http.Flusher, state *sseState) {
	conductors, err := s.DB.ListActiveConductors(ctx)
	if err != nil {
		return
	}
	var allEntries interface{}
	for _, c := range conductors {
		entries, err := s.DB.ListMergeQueueEntries(ctx, c.ID)
		if err != nil {
			continue
		}
		if len(entries) > 0 {
			allEntries = entries
			break
		}
	}
	if allEntries == nil {
		allEntries = []struct{}{}
	}
	hash := quickHash(allEntries)
	if hash != state.mergeHash {
		state.mergeHash = hash
		sendSSE(w, flusher, "merge_update", allEntries)
	}
}

// sendSSE writes a single SSE event with the given type and data.
func sendSSE(w http.ResponseWriter, flusher http.Flusher, eventType string, data interface{}) {
	payload, err := json.Marshal(sseEvent{
		Type: eventType,
		Data: data,
	})
	if err != nil {
		slog.Error("marshaling SSE event", "event", eventType, "err", err)
		return
	}
	fmt.Fprintf(w, "event: %s\ndata: %s\n\n", eventType, payload)
	flusher.Flush()
}

// quickHash produces a simple JSON-based hash for change detection.
func quickHash(v interface{}) string {
	b, err := json.Marshal(v)
	if err != nil {
		return ""
	}
	return string(b)
}
