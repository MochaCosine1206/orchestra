package orchestrator

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestProjectSlugFromPath(t *testing.T) {
	tests := []struct {
		repoRoot string
		want     string
	}{
		{"/Users/steve/projects/claude-orchestra", "claude-orchestra"},
		{"/home/user/My Project", "my-project"},
		{"/tmp/UPPER_CASE.dir", "upper_case.dir"},
		{"/foo/bar/special!@#chars", "specialchars"},
		{"/simple", "simple"},
	}

	for _, tt := range tests {
		got := ProjectSlugFromPath(tt.repoRoot)
		if got != tt.want {
			t.Errorf("ProjectSlugFromPath(%q) = %q, want %q", tt.repoRoot, got, tt.want)
		}
	}
}

func TestFormatTaskSummary(t *testing.T) {
	tests := []struct {
		done, failed, total int
		want                string
	}{
		{3, 0, 3, "3/3 tasks passed"},
		{2, 1, 3, "2/3 done, 1 failed"},
		{0, 3, 3, "0/3 done, 3 failed"},
		{5, 0, 5, "5/5 tasks passed"},
	}

	for _, tt := range tests {
		got := formatTaskSummary(tt.done, tt.failed, tt.total)
		if got != tt.want {
			t.Errorf("formatTaskSummary(%d, %d, %d) = %q, want %q",
				tt.done, tt.failed, tt.total, got, tt.want)
		}
	}
}

func TestLoadTelegramCreds_MissingFile(t *testing.T) {
	t.Setenv("TELEGRAM_NOTIFY_CONFIG", filepath.Join(t.TempDir(), "nonexistent"))
	cfg := LoadTelegramCreds()
	if cfg != nil {
		t.Error("expected nil config when file missing")
	}
}

func TestLoadTelegramCreds_WithFile(t *testing.T) {
	tmpDir := t.TempDir()
	configFile := filepath.Join(tmpDir, "telegram-notify")
	configContent := `TELEGRAM_BOT_TOKEN="123:ABC"
TELEGRAM_CHAT_ID="456"
TELEGRAM_FORUM_CHAT_ID="-1001234567890"
`
	os.WriteFile(configFile, []byte(configContent), 0o644)
	t.Setenv("TELEGRAM_NOTIFY_CONFIG", configFile)

	cfg := LoadTelegramCreds()
	if cfg == nil {
		t.Fatal("expected non-nil config")
	}
	if cfg.BotToken != "123:ABC" {
		t.Errorf("BotToken = %q, want %q", cfg.BotToken, "123:ABC")
	}
	if cfg.ChatID != "456" {
		t.Errorf("ChatID = %q, want %q", cfg.ChatID, "456")
	}
	if cfg.ForumChatID != "-1001234567890" {
		t.Errorf("ForumChatID = %q, want %q", cfg.ForumChatID, "-1001234567890")
	}
}

func TestLoadTelegramCreds_ExportFormat(t *testing.T) {
	tmpDir := t.TempDir()
	configFile := filepath.Join(tmpDir, "telegram-notify")
	configContent := `export TELEGRAM_BOT_TOKEN="token123"
export TELEGRAM_CHAT_ID="chat456"
# comment line
`
	os.WriteFile(configFile, []byte(configContent), 0o644)
	t.Setenv("TELEGRAM_NOTIFY_CONFIG", configFile)

	cfg := LoadTelegramCreds()
	if cfg == nil {
		t.Fatal("expected non-nil config")
	}
	if cfg.BotToken != "token123" {
		t.Errorf("BotToken = %q, want %q", cfg.BotToken, "token123")
	}
	if cfg.ChatID != "chat456" {
		t.Errorf("ChatID = %q, want %q", cfg.ChatID, "chat456")
	}
}

func TestReadTopicID(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("SESSION_STATE_BASE", tmpDir)

	// No file — should return empty
	got := ReadTopicID("nonexistent")
	if got != "" {
		t.Errorf("ReadTopicID(nonexistent) = %q, want empty", got)
	}

	// Create state dir with topic_id
	stateDir := filepath.Join(tmpDir, "test-project", "telegram")
	os.MkdirAll(stateDir, 0o755)
	os.WriteFile(filepath.Join(stateDir, "topic_id"), []byte("98765\n"), 0o644)

	got = ReadTopicID("test-project")
	if got != "98765" {
		t.Errorf("ReadTopicID(test-project) = %q, want %q", got, "98765")
	}
}

func TestFormatDecomposeNotification(t *testing.T) {
	roles := map[string]int{"implementer": 2, "reviewer": 1}
	got := FormatDecomposeNotification(3, roles)
	if got == "" {
		t.Error("expected non-empty notification")
	}
	if !strings.Contains(got, "3 tasks") {
		t.Errorf("missing task count in %q", got)
	}
}

func TestFormatAgentCompleteNotification(t *testing.T) {
	got := FormatAgentCompleteNotification("t-123", "implementer", "done", 2, 3)
	if !strings.Contains(got, "t-123") || !strings.Contains(got, "implementer") || !strings.Contains(got, "2/3") {
		t.Errorf("unexpected format: %q", got)
	}

	got = FormatAgentCompleteNotification("t-456", "reviewer", "failed", 1, 3)
	if !strings.Contains(got, "failed") {
		t.Errorf("expected 'failed' in: %q", got)
	}
}
