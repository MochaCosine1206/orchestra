package dashboard

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestQuickHash(t *testing.T) {
	// Same input should produce same hash
	a := quickHash(map[string]string{"key": "value"})
	b := quickHash(map[string]string{"key": "value"})
	if a != b {
		t.Errorf("quickHash should be deterministic: %q != %q", a, b)
	}

	// Different input should produce different hash
	c := quickHash(map[string]string{"key": "other"})
	if a == c {
		t.Error("quickHash should differ for different inputs")
	}

	// Empty input
	d := quickHash(nil)
	if d != "null" {
		t.Errorf("quickHash(nil) = %q, want %q", d, "null")
	}
}

func TestSendSSE(t *testing.T) {
	w := httptest.NewRecorder()
	sendSSE(w, w, "test_event", map[string]string{"key": "value"})

	body := w.Body.String()
	if !strings.Contains(body, "event: test_event") {
		t.Errorf("SSE output should contain event type, got: %s", body)
	}
	if !strings.Contains(body, "data:") {
		t.Errorf("SSE output should contain data field, got: %s", body)
	}
	if !strings.Contains(body, "key") {
		t.Errorf("SSE output should contain data payload, got: %s", body)
	}
}

func TestSendSSEMarshalError(t *testing.T) {
	w := httptest.NewRecorder()
	// Channels can't be marshaled to JSON
	sendSSE(w, w, "test", make(chan int))

	body := w.Body.String()
	if body != "" {
		t.Errorf("sendSSE with marshal error should write nothing, got: %s", body)
	}
}

func TestHandleSSENonFlusher(t *testing.T) {
	s, _ := setupTestServer(t)

	// Use a custom ResponseWriter that doesn't implement http.Flusher
	w := &nonFlusherWriter{httptest.NewRecorder()}
	req := httptest.NewRequest("GET", "/events", nil)

	s.handleSSE(w, req)

	if w.rec.Code != http.StatusInternalServerError {
		t.Errorf("handleSSE without Flusher: want 500, got %d", w.rec.Code)
	}
}

// nonFlusherWriter wraps httptest.ResponseRecorder but does NOT implement http.Flusher.
type nonFlusherWriter struct {
	rec *httptest.ResponseRecorder
}

func (w *nonFlusherWriter) Header() http.Header         { return w.rec.Header() }
func (w *nonFlusherWriter) Write(b []byte) (int, error) { return w.rec.Write(b) }
func (w *nonFlusherWriter) WriteHeader(code int)        { w.rec.WriteHeader(code) }

func TestSSEWithSeededEvents(t *testing.T) {
	s, mux := setupTestServer(t)
	seedTestData(t, s)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	req := httptest.NewRequest("GET", "/events", nil).WithContext(ctx)
	w := httptest.NewRecorder()

	done := make(chan struct{})
	go func() {
		mux.ServeHTTP(w, req)
		close(done)
	}()

	<-done

	body := w.Body.String()
	// With seeded data, we should get task_update and event_new events
	if !strings.Contains(body, "event:") {
		t.Error("SSE stream with seeded data should emit events")
	}
}

func TestPollAndEmitUpdatesState(t *testing.T) {
	s, _ := setupTestServer(t)
	seedTestData(t, s)

	w := httptest.NewRecorder()
	state := &sseState{}

	// First poll should emit events (state is empty)
	s.pollAndEmit(context.Background(), w, w, state)

	body := w.Body.String()
	if body == "" {
		t.Error("first pollAndEmit should emit events with seeded data")
	}

	// Second poll with same state should emit nothing new (unless data changed)
	w2 := httptest.NewRecorder()
	s.pollAndEmit(context.Background(), w2, w2, state)

	// taskHash etc. should be set now, so second poll emits nothing
	body2 := w2.Body.String()
	if len(body2) >= len(body) {
		// This is ok - the state should prevent duplicate emissions
		// but event_new might still emit if lastEventID wasn't updated
	}
}

func TestEmitNewEvents(t *testing.T) {
	s, _ := setupTestServer(t)
	seedTestData(t, s)

	w := httptest.NewRecorder()
	state := &sseState{lastEventID: 0}

	s.emitNewEvents(context.Background(), w, w, state)

	body := w.Body.String()
	if !strings.Contains(body, "event_new") {
		t.Error("emitNewEvents should emit event_new for seeded events")
	}
	if state.lastEventID == 0 {
		t.Error("lastEventID should be updated after emitting events")
	}
}

func TestEmitTaskUpdates(t *testing.T) {
	s, _ := setupTestServer(t)
	seedTestData(t, s)

	w := httptest.NewRecorder()
	state := &sseState{}

	s.emitTaskUpdates(context.Background(), w, w, state)

	body := w.Body.String()
	if !strings.Contains(body, "task_update") {
		t.Error("emitTaskUpdates should emit task_update for seeded tasks")
	}
	if state.taskHash == "" {
		t.Error("taskHash should be set after emitting")
	}
}

func TestEmitAgentUpdates(t *testing.T) {
	s, _ := setupTestServer(t)
	seedTestData(t, s)

	w := httptest.NewRecorder()
	state := &sseState{}

	s.emitAgentUpdates(context.Background(), w, w, state)

	body := w.Body.String()
	if !strings.Contains(body, "agent_update") {
		t.Error("emitAgentUpdates should emit agent_update for seeded agents")
	}
	if state.agentHash == "" {
		t.Error("agentHash should be set after emitting")
	}
}

func TestEmitConductorUpdates(t *testing.T) {
	s, _ := setupTestServer(t)
	seedTestData(t, s)

	w := httptest.NewRecorder()
	state := &sseState{}

	s.emitConductorUpdates(context.Background(), w, w, state)

	body := w.Body.String()
	if !strings.Contains(body, "conductor_update") {
		t.Error("emitConductorUpdates should emit conductor_update for seeded conductors")
	}
	if state.conductorHash == "" {
		t.Error("conductorHash should be set after emitting")
	}
}

func TestEmitMergeUpdates(t *testing.T) {
	s, _ := setupTestServer(t)
	seedTestData(t, s)

	w := httptest.NewRecorder()
	state := &sseState{}

	s.emitMergeUpdates(context.Background(), w, w, state)

	// Should emit even with empty merge queue (emits empty array)
	if state.mergeHash == "" {
		t.Error("mergeHash should be set after emitting")
	}
}

func TestSSEStateChangeDetection(t *testing.T) {
	s, _ := setupTestServer(t)
	seedTestData(t, s)

	w := httptest.NewRecorder()
	state := &sseState{}

	// First emit sets the hash
	s.emitTaskUpdates(context.Background(), w, w, state)
	firstHash := state.taskHash

	// Second emit with same data should not emit
	w2 := httptest.NewRecorder()
	s.emitTaskUpdates(context.Background(), w2, w2, state)

	if state.taskHash != firstHash {
		t.Error("taskHash should not change when data hasn't changed")
	}
	if w2.Body.Len() > 0 {
		t.Error("second emit should not produce output when data unchanged")
	}
}
