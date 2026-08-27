package dashboard

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/MochaCosine1206/orchestra/internal/db"
)

func setupTestDB(t *testing.T) *db.DB {
	t.Helper()
	d, err := db.Open(t.TempDir() + "/test.db")
	if err != nil {
		t.Fatal(err)
	}
	if err := d.InitSchema(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	return d
}

func setupTestServer(t *testing.T) (*Server, *http.ServeMux) {
	t.Helper()
	d := setupTestDB(t)
	s := &Server{DB: d, Port: 0}
	mux := http.NewServeMux()
	s.RegisterRoutes(mux)
	return s, mux
}

func TestTemplatesParse(t *testing.T) {
	tmpl, err := Templates()
	if err != nil {
		t.Fatalf("Templates() failed: %v", err)
	}
	expected := []string{"index", "project", "task-detail", "agent-detail", "config", "artifacts", "git", "briefing", "factory-fragment", "head", "foot"}
	for _, name := range expected {
		if tmpl.Lookup(name) == nil {
			t.Errorf("template %q not found", name)
		}
	}
}

func TestStaticHandler(t *testing.T) {
	h := StaticHandler()
	if h == nil {
		t.Fatal("StaticHandler() returned nil")
	}
}

func TestGETPageRoutes(t *testing.T) {
	_, mux := setupTestServer(t)

	routes := []struct {
		path string
		code int
	}{
		{"/", 200},
		{"/config", 200},
		{"/artifacts", 200},
		{"/briefing", 200},
	}

	for _, tc := range routes {
		t.Run(tc.path, func(t *testing.T) {
			req := httptest.NewRequest("GET", tc.path, nil)
			w := httptest.NewRecorder()
			mux.ServeHTTP(w, req)
			if w.Code != tc.code {
				t.Errorf("GET %s: want %d, got %d; body: %s", tc.path, tc.code, w.Code, w.Body.String())
			}
			if w.Code == 200 && !strings.Contains(w.Header().Get("Content-Type"), "text/html") {
				t.Errorf("GET %s: expected text/html content type, got %s", tc.path, w.Header().Get("Content-Type"))
			}
		})
	}
}

func TestGETPageRoutes404(t *testing.T) {
	_, mux := setupTestServer(t)

	routes := []string{"/project/", "/task/", "/agent/"}
	for _, path := range routes {
		t.Run(path, func(t *testing.T) {
			req := httptest.NewRequest("GET", path, nil)
			w := httptest.NewRecorder()
			mux.ServeHTTP(w, req)
			if w.Code != 404 {
				t.Errorf("GET %s with no ID: want 404, got %d", path, w.Code)
			}
		})
	}
}

func TestPOSTProjectActions(t *testing.T) {
	_, mux := setupTestServer(t)

	actions := []struct {
		path string
		body string
		code int
	}{
		{"/api/project/test-id/pause", "", 200},
		{"/api/project/test-id/resume", "", 200},
		{"/api/project/test-id/stop", "", 200},
		{"/api/project/test-id/replan", "", 200},
		{"/api/project/test-id/retry/task-1", "", 200},
		{"/api/project/test-id/skip/task-1", "", 200},
		{"/api/project/test-id/add-task", "implement feature X", 200},
	}

	for _, tc := range actions {
		t.Run(tc.path, func(t *testing.T) {
			var body io.Reader
			if tc.body != "" {
				body = strings.NewReader(tc.body)
			}
			req := httptest.NewRequest("POST", tc.path, body)
			w := httptest.NewRecorder()
			mux.ServeHTTP(w, req)
			if w.Code != tc.code {
				t.Errorf("POST %s: want %d, got %d; body: %s", tc.path, tc.code, w.Code, w.Body.String())
			}
		})
	}
}

func TestPOSTFactoryPauseAll(t *testing.T) {
	_, mux := setupTestServer(t)

	req := httptest.NewRequest("POST", "/api/factory/pause-all", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != 200 {
		t.Errorf("POST /api/factory/pause-all: want 200, got %d", w.Code)
	}
}

func TestPOSTConfigTheme(t *testing.T) {
	_, mux := setupTestServer(t)

	req := httptest.NewRequest("POST", "/api/config/theme", strings.NewReader(`{"theme":"light"}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != 200 {
		t.Errorf("POST /api/config/theme: want 200, got %d; body: %s", w.Code, w.Body.String())
	}
}

func TestHTMXFragmentEndpoints(t *testing.T) {
	_, mux := setupTestServer(t)

	routes := []string{"/api/factory", "/api/task/test-id", "/api/agent/test-id"}
	for _, path := range routes {
		t.Run(path, func(t *testing.T) {
			req := httptest.NewRequest("GET", path, nil)
			w := httptest.NewRecorder()
			mux.ServeHTTP(w, req)
			// /api/task/test-id and /api/agent/test-id will 404 since no data,
			// /api/factory should return 200
			if path == "/api/factory" && w.Code != 200 {
				t.Errorf("GET %s: want 200, got %d", path, w.Code)
			}
		})
	}
}

func TestSSEEndpoint(t *testing.T) {
	_, mux := setupTestServer(t)

	// Use httptest.ResponseRecorder approach with a context timeout
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	req := httptest.NewRequest("GET", "/events", nil).WithContext(ctx)
	w := httptest.NewRecorder()

	done := make(chan struct{})
	go func() {
		mux.ServeHTTP(w, req)
		close(done)
	}()

	// Wait for context to cancel (which stops the SSE handler)
	<-done

	resp := w.Result()
	if resp.Header.Get("Content-Type") != "text/event-stream" {
		t.Errorf("SSE Content-Type: want text/event-stream, got %s", resp.Header.Get("Content-Type"))
	}

	body, _ := io.ReadAll(resp.Body)
	data := string(body)

	// Should contain at least one SSE event from the initial poll
	if !strings.Contains(data, "event:") && !strings.Contains(data, "data:") {
		t.Errorf("SSE stream did not emit events, got: %s", data)
	}
}

func TestServerStartAndShutdown(t *testing.T) {
	d := setupTestDB(t)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}

	s := &Server{DB: d, Port: 0}
	s.ListenOn(ln)

	addr := ln.Addr().String()
	if s.Addr() != addr {
		t.Errorf("Addr() = %q, want %q", s.Addr(), addr)
	}

	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() { errCh <- s.Start(ctx) }()

	// Wait for server to be ready
	time.Sleep(50 * time.Millisecond)

	// Hit the index page
	resp, err := http.Get("http://" + addr + "/")
	if err != nil {
		t.Fatalf("GET /: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Errorf("GET /: want 200, got %d", resp.StatusCode)
	}

	cancel()
	if err := <-errCh; err != nil {
		t.Errorf("Start returned error: %v", err)
	}
}
