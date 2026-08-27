package orchestrator

import (
	"bufio"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// TelegramCreds holds Telegram Bot API credentials.
type TelegramCreds struct {
	BotToken    string
	ChatID      string
	ForumChatID string // optional supergroup override
}

// LoadTelegramCreds reads ~/.telegram-notify for BOT_TOKEN and CHAT_ID.
// Returns nil if credentials are missing or the file doesn't exist.
func LoadTelegramCreds() *TelegramCreds {
	configPath := os.Getenv("TELEGRAM_NOTIFY_CONFIG")
	if configPath == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil
		}
		configPath = filepath.Join(home, ".telegram-notify")
	}

	f, err := os.Open(configPath)
	if err != nil {
		return nil
	}
	defer f.Close()

	creds := &TelegramCreds{}
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		// Parse KEY=VALUE or KEY="VALUE" or export KEY=VALUE
		line = strings.TrimPrefix(line, "export ")
		parts := strings.SplitN(line, "=", 2)
		if len(parts) != 2 {
			continue
		}
		key := strings.TrimSpace(parts[0])
		val := strings.TrimSpace(parts[1])
		val = strings.Trim(val, "\"'")

		switch key {
		case "TELEGRAM_BOT_TOKEN":
			creds.BotToken = val
		case "TELEGRAM_CHAT_ID":
			creds.ChatID = val
		case "TELEGRAM_FORUM_CHAT_ID":
			creds.ForumChatID = val
		}
	}

	if creds.BotToken == "" || creds.ChatID == "" {
		return nil
	}
	return creds
}

// ReadTopicID reads the topic_id file for a given project slug.
func ReadTopicID(slug string) string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	stateBase := os.Getenv("SESSION_STATE_BASE")
	if stateBase == "" {
		stateBase = filepath.Join(home, ".claude-session-state")
	}
	topicFile := filepath.Join(stateBase, slug, "telegram", "topic_id")
	data, err := os.ReadFile(topicFile)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

// notifyTelegram sends a message to the project's Telegram topic.
// Fire-and-forget: errors are logged but never block the conductor.
// Disabled when DisableNotify is set (e.g. during tests) or when
// the binary looks like a test runner.
func (c *Conductor) notifyTelegram(message string) {
	if c.DisableNotify {
		return
	}
	// Guard: don't send real notifications from test binaries
	if strings.HasSuffix(os.Args[0], ".test") {
		return
	}

	creds := LoadTelegramCreds()
	if creds == nil {
		return
	}

	slug := ProjectSlugFromPath(c.RepoRoot)
	topicID := ReadTopicID(slug)

	chatID := creds.ChatID
	if creds.ForumChatID != "" {
		chatID = creds.ForumChatID
	}

	go func() {
		SendTelegramMessage(creds.BotToken, chatID, topicID, message)
	}()
}

// SendTelegramMessage posts a message to the Telegram Bot API.
func SendTelegramMessage(botToken, chatID, topicID, text string) error {
	apiURL := fmt.Sprintf("https://api.telegram.org/bot%s/sendMessage", botToken)

	params := url.Values{}
	params.Set("chat_id", chatID)
	params.Set("text", text)
	params.Set("parse_mode", "HTML")
	if topicID != "" && topicID != "0" {
		params.Set("message_thread_id", topicID)
	}

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.PostForm(apiURL, params)
	if err != nil {
		return err
	}
	resp.Body.Close()
	return nil
}

// ProjectSlugFromPath derives a filesystem-safe slug from a directory path.
// Matches the shell get_project_slug() in state-helpers.sh.
func ProjectSlugFromPath(dir string) string {
	base := filepath.Base(dir)
	slug := strings.ToLower(base)
	slug = strings.ReplaceAll(slug, " ", "-")
	var out strings.Builder
	for _, r := range slug {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '.' || r == '_' || r == '-' {
			out.WriteRune(r)
		}
	}
	return out.String()
}

// formatTaskSummary returns a concise "X/Y done, Z failed" string.
func formatTaskSummary(done, failed, total int) string {
	if failed == 0 {
		return fmt.Sprintf("%d/%d tasks passed", done, total)
	}
	return fmt.Sprintf("%d/%d done, %d failed", done, total, failed)
}

// FormatDecomposeNotification formats the decomposition milestone message.
func FormatDecomposeNotification(taskCount int, roles map[string]int) string {
	parts := make([]string, 0, len(roles))
	for role, count := range roles {
		parts = append(parts, fmt.Sprintf("%d %s", count, role))
	}
	return fmt.Sprintf("Orchestra: Decomposed into %d tasks (%s)", taskCount, strings.Join(parts, ", "))
}

// FormatAgentCompleteNotification formats an agent completion message.
func FormatAgentCompleteNotification(taskID, role, status string, done, total int) string {
	icon := "done"
	if status == "failed" {
		icon = "failed"
	}
	return fmt.Sprintf("Orchestra: Agent %s [%s] %s (%d/%d)", taskID, role, icon, done, total)
}

// FormatMergeNotification formats the merge result message.
func FormatMergeNotification(merged, failed, total int) string {
	if failed == 0 {
		return fmt.Sprintf("Orchestra: Complete — %d/%d tasks passed, merge successful", merged, total)
	}
	return fmt.Sprintf("Orchestra: Complete — %d/%d tasks merged, %d failed", merged, total, failed)
}
