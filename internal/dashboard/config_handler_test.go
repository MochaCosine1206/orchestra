package dashboard

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHandleConfigRendersAllSections(t *testing.T) {
	_, mux := setupTestServer(t)

	req := httptest.NewRequest("GET", "/config", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("GET /config: want 200, got %d; body: %s", w.Code, w.Body.String())
	}

	body := w.Body.String()
	sections := []string{
		"Auto Mode",
		"Orchestration Defaults",
		"Merge Settings",
		"Agent Roles",
		"Circuit Breakers",
		"Appearance",
		"Notification Channel",
	}
	for _, s := range sections {
		if !strings.Contains(body, s) {
			t.Errorf("config page missing section heading %q", s)
		}
	}
}

func TestHandleConfigThemeSwatches(t *testing.T) {
	_, mux := setupTestServer(t)

	req := httptest.NewRequest("GET", "/config", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("GET /config: want 200, got %d", w.Code)
	}

	body := w.Body.String()
	// 13 themes should produce 13 swatches
	swatches := themeSwatches()
	if len(swatches) != 13 {
		t.Errorf("expected 13 theme swatches, got %d", len(swatches))
	}
	for _, s := range swatches {
		if !strings.Contains(body, s.Name) {
			t.Errorf("config page missing theme swatch %q", s.Name)
		}
	}
	if !strings.Contains(body, "theme-swatch") {
		t.Error("config page missing theme-swatch class")
	}
}

func TestHandleConfigSectionPOST(t *testing.T) {
	_, mux := setupTestServer(t)

	req := httptest.NewRequest("POST", "/api/config/merge", strings.NewReader(`{"merge_mode":"PR"}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("POST /api/config/merge: want 200, got %d; body: %s", w.Code, w.Body.String())
	}

	body := w.Body.String()
	if !strings.Contains(body, `"status":"ok"`) {
		t.Errorf("expected status ok, got: %s", body)
	}
}

func TestHandleConfigSectionInvalidSection(t *testing.T) {
	_, mux := setupTestServer(t)

	req := httptest.NewRequest("POST", "/api/config/nonexistent", strings.NewReader(`{"key":"val"}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("POST /api/config/nonexistent: want 400, got %d; body: %s", w.Code, w.Body.String())
	}
}
