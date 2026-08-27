package bridge

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestGetMe(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/bottest-token/getMe" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		json.NewEncoder(w).Encode(map[string]interface{}{
			"ok": true,
			"result": map[string]interface{}{
				"id":         12345,
				"is_bot":     true,
				"first_name": "TestBot",
				"username":   "test_bot",
			},
		})
	}))
	defer srv.Close()

	c := &TGClient{
		token:  "test-token",
		http:   srv.Client(),
		apiURL: srv.URL + "/bottest-token",
	}

	info, err := c.GetMe(context.Background())
	if err != nil {
		t.Fatalf("GetMe failed: %v", err)
	}
	if info.Username != "test_bot" {
		t.Errorf("expected username test_bot, got %s", info.Username)
	}
	if !info.IsBot {
		t.Error("expected is_bot=true")
	}
}

func TestGetChat(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]interface{}{
			"ok": true,
			"result": map[string]interface{}{
				"id":       -100123456,
				"type":     "supergroup",
				"title":    "Test Forum",
				"is_forum": true,
			},
		})
	}))
	defer srv.Close()

	c := &TGClient{
		token:  "test-token",
		http:   srv.Client(),
		apiURL: srv.URL + "/bottest-token",
	}

	info, err := c.GetChat(context.Background(), "-100123456")
	if err != nil {
		t.Fatalf("GetChat failed: %v", err)
	}
	if !info.IsForum {
		t.Error("expected is_forum=true")
	}
	if info.Title != "Test Forum" {
		t.Errorf("expected title 'Test Forum', got %q", info.Title)
	}
}

func TestWriteAndLoadTelegramConfig(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()

	cfg := TelegramConfig{
		BotToken:    "123:ABC",
		BotUsername: "mybot",
		ChatID:      "-100999",
		IsForum:     true,
		ChatTitle:   "My Group",
		SetupAt:     "2026-03-27T00:00:00Z",
	}

	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		t.Fatal(err)
	}

	path := filepath.Join(tmpDir, "telegram.json")
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}

	// Read back
	readData, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	var loaded TelegramConfig
	if err := json.Unmarshal(readData, &loaded); err != nil {
		t.Fatal(err)
	}

	if loaded.BotToken != cfg.BotToken {
		t.Errorf("token mismatch: %s vs %s", loaded.BotToken, cfg.BotToken)
	}
	if loaded.ChatID != cfg.ChatID {
		t.Errorf("chat_id mismatch: %s vs %s", loaded.ChatID, cfg.ChatID)
	}
	if !loaded.IsForum {
		t.Error("expected is_forum=true")
	}
}

func TestInstallHooks(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	claudeDir := filepath.Join(tmpDir, ".claude")
	os.MkdirAll(claudeDir, 0755)

	// Write initial settings
	initial := map[string]interface{}{
		"permissions": map[string]interface{}{
			"allow": []interface{}{"Bash(git:*)"},
		},
	}
	data, _ := json.MarshalIndent(initial, "", "  ")
	os.WriteFile(filepath.Join(claudeDir, "settings.local.json"), data, 0644)

	wiz := &TelegramWizard{projDir: tmpDir}
	if err := wiz.installHooks(tmpDir); err != nil {
		t.Fatalf("installHooks failed: %v", err)
	}

	// Read back
	readData, err := os.ReadFile(filepath.Join(claudeDir, "settings.local.json"))
	if err != nil {
		t.Fatal(err)
	}

	var settings map[string]interface{}
	json.Unmarshal(readData, &settings)

	perms := settings["permissions"].(map[string]interface{})
	allow := perms["allow"].([]interface{})

	foundCurl := false
	foundGit := false
	for _, v := range allow {
		s := v.(string)
		if s == "Bash(curl:*)" {
			foundCurl = true
		}
		if s == "Bash(git:*)" {
			foundGit = true
		}
	}

	if !foundCurl {
		t.Error("expected Bash(curl:*) in allow list")
	}
	if !foundGit {
		t.Error("expected existing Bash(git:*) to be preserved")
	}
}

func TestInstallHooks_NoExistingFile(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()

	wiz := &TelegramWizard{projDir: tmpDir}
	if err := wiz.installHooks(tmpDir); err != nil {
		t.Fatalf("installHooks failed: %v", err)
	}

	readData, err := os.ReadFile(filepath.Join(tmpDir, ".claude", "settings.local.json"))
	if err != nil {
		t.Fatal(err)
	}

	var settings map[string]interface{}
	json.Unmarshal(readData, &settings)

	perms := settings["permissions"].(map[string]interface{})
	allow := perms["allow"].([]interface{})

	if len(allow) != 1 || allow[0].(string) != "Bash(curl:*)" {
		t.Errorf("expected [Bash(curl:*)], got %v", allow)
	}
}
