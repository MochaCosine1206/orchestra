package dashboard

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHandleArtifactsEmptyState(t *testing.T) {
	_, mux := setupTestServer(t)

	req := httptest.NewRequest("GET", "/artifacts", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("GET /artifacts: want 200, got %d; body: %s", w.Code, w.Body.String())
	}

	body := w.Body.String()
	if !strings.Contains(body, "artifact-list") {
		t.Error("artifacts page missing artifact-list container")
	}
	if !strings.Contains(body, "artifact-filter-type") {
		t.Error("artifacts page missing filter controls")
	}
}

func TestHandleArtifactsWithSession(t *testing.T) {
	s, mux := setupTestServer(t)
	ctx := context.Background()

	// Seed a reconciliation report
	_ = s.DB.SetBlackboard(ctx, "reconcile:report:session-1", "## Reconciliation\n\nAll tasks complete.", "test")

	req := httptest.NewRequest("GET", "/artifacts", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("GET /artifacts: want 200, got %d", w.Code)
	}

	body := w.Body.String()
	if !strings.Contains(body, "session-1") {
		t.Error("artifacts page missing session-1")
	}
	if !strings.Contains(body, "Reconciliation Report") {
		t.Error("artifacts page missing reconciliation report title")
	}
}

func TestHandleArtifactsFilterByType(t *testing.T) {
	s, mux := setupTestServer(t)
	ctx := context.Background()

	// Seed both reconcile and review artifacts
	_ = s.DB.SetBlackboard(ctx, "reconcile:report:sess-a", "reconcile report content", "test")
	_ = s.DB.SetBlackboard(ctx, "review:report:task-1", "review report content", "test")

	// Filter by reconcile type
	req := httptest.NewRequest("GET", "/artifacts?type=reconcile", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("GET /artifacts?type=reconcile: want 200, got %d", w.Code)
	}

	body := w.Body.String()
	if !strings.Contains(body, "Reconciliation Report") {
		t.Error("filtered artifacts page missing reconcile report")
	}
	// Review report should be filtered out
	if strings.Contains(body, "Review Report") {
		t.Error("filtered artifacts page should not contain review report")
	}
}

func TestHandleArtifactsMarkdownPreview(t *testing.T) {
	s, mux := setupTestServer(t)
	ctx := context.Background()

	// Seed a markdown artifact via reconciliation report
	_ = s.DB.SetBlackboard(ctx, "reconcile:report:md-test", "# Hello World\n\nThis is **bold** text.", "test")

	req := httptest.NewRequest("GET", "/artifacts", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("GET /artifacts: want 200, got %d", w.Code)
	}

	body := w.Body.String()
	// Should contain rendered HTML from RenderMarkdown, not raw markdown
	if !strings.Contains(body, "<h1>Hello World</h1>") {
		t.Errorf("artifacts page should contain rendered markdown HTML, got: %s", body)
	}
	if !strings.Contains(body, "<strong>bold</strong>") {
		t.Error("artifacts page should contain rendered bold text")
	}
	// Should NOT contain raw markdown
	if strings.Contains(body, "# Hello World") {
		t.Error("artifacts page should not contain raw markdown")
	}
}
