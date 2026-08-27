package bridge

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// TGClient is a thin Telegram Bot API client — no external dependencies.
type TGClient struct {
	token  string
	chatID string
	http   *http.Client
	apiURL string
}

// NewTGClient creates a new Telegram client.
func NewTGClient(token, chatID string) *TGClient {
	return &TGClient{
		token:  token,
		chatID: chatID,
		http: &http.Client{
			Timeout: 0, // no default timeout — callers set via context
		},
		apiURL: "https://api.telegram.org/bot" + token,
	}
}

// --- Telegram API Response Types ---

// TGResponse wraps all Bot API responses.
type TGResponse struct {
	OK     bool            `json:"ok"`
	Result json.RawMessage `json:"result"`
}

// TGMessage represents a Telegram message.
type TGMessage struct {
	MessageID       int64  `json:"message_id"`
	MessageThreadID int64  `json:"message_thread_id"`
	Chat            TGChat `json:"chat"`
	Text            string `json:"text"`
	From            TGUser `json:"from"`
}

// TGChat represents a Telegram chat.
type TGChat struct {
	ID int64 `json:"id"`
}

// TGUser represents a Telegram user.
type TGUser struct {
	ID       int64  `json:"id"`
	Username string `json:"username"`
}

// TGUpdate represents a single update from getUpdates.
type TGUpdate struct {
	UpdateID      int64       `json:"update_id"`
	Message       *TGMessage  `json:"message"`
	CallbackQuery *TGCallback `json:"callback_query"`
}

// TGCallback represents a callback query (inline button press).
type TGCallback struct {
	ID      string     `json:"id"`
	Data    string     `json:"data"`
	Message *TGMessage `json:"message"`
	From    TGUser     `json:"from"`
}

// TGForumTopic is the result of createForumTopic.
type TGForumTopic struct {
	MessageThreadID int64  `json:"message_thread_id"`
	Name            string `json:"name"`
}

// --- API Methods ---

// SendMessage sends a text message, optionally to a topic.
func (c *TGClient) SendMessage(ctx context.Context, text string, topicID int64, parseMode string) (int64, error) {
	params := url.Values{
		"chat_id": {c.chatID},
		"text":    {text},
	}
	if parseMode != "" {
		params.Set("parse_mode", parseMode)
	}
	if topicID > 0 {
		params.Set("message_thread_id", strconv.FormatInt(topicID, 10))
	}

	var msg TGMessage
	if err := c.post(ctx, "sendMessage", params, &msg); err != nil {
		return 0, err
	}
	return msg.MessageID, nil
}

// SendWithKeyboard sends a message with an inline keyboard.
func (c *TGClient) SendWithKeyboard(ctx context.Context, text, keyboardJSON string, topicID int64, parseMode string) (int64, error) {
	params := url.Values{
		"chat_id":      {c.chatID},
		"text":         {text},
		"parse_mode":   {parseMode},
		"reply_markup": {keyboardJSON},
	}
	if topicID > 0 {
		params.Set("message_thread_id", strconv.FormatInt(topicID, 10))
	}

	var msg TGMessage
	if err := c.post(ctx, "sendMessage", params, &msg); err != nil {
		return 0, err
	}
	return msg.MessageID, nil
}

// EditMessage edits an existing message's text.
func (c *TGClient) EditMessage(ctx context.Context, msgID int64, text, parseMode string) error {
	params := url.Values{
		"chat_id":    {c.chatID},
		"message_id": {strconv.FormatInt(msgID, 10)},
		"text":       {text},
	}
	if parseMode != "" {
		params.Set("parse_mode", parseMode)
	}
	return c.post(ctx, "editMessageText", params, nil)
}

// AnswerCallback responds to a callback query (suppresses loading spinner).
func (c *TGClient) AnswerCallback(ctx context.Context, cbID, text string) error {
	params := url.Values{
		"callback_query_id": {cbID},
	}
	if text != "" {
		params.Set("text", text)
	}
	return c.post(ctx, "answerCallbackQuery", params, nil)
}

// GetUpdates performs a long-poll for updates.
func (c *TGClient) GetUpdates(ctx context.Context, offset int64, timeoutSecs int) ([]TGUpdate, error) {
	params := url.Values{
		"offset":          {strconv.FormatInt(offset, 10)},
		"timeout":         {strconv.Itoa(timeoutSecs)},
		"allowed_updates": {`["message","callback_query"]`},
	}

	// Use a context timeout slightly longer than the TG timeout
	reqCtx, cancel := context.WithTimeout(ctx, time.Duration(timeoutSecs+10)*time.Second)
	defer cancel()

	reqURL := c.apiURL + "/getUpdates?" + params.Encode()
	req, err := http.NewRequestWithContext(reqCtx, "GET", reqURL, nil)
	if err != nil {
		return nil, err
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	var tgResp struct {
		OK     bool       `json:"ok"`
		Result []TGUpdate `json:"result"`
	}
	if err := json.Unmarshal(body, &tgResp); err != nil {
		return nil, fmt.Errorf("unmarshal getUpdates: %w", err)
	}
	if !tgResp.OK {
		return nil, fmt.Errorf("getUpdates not ok: %s", string(body))
	}
	return tgResp.Result, nil
}

// CreateForumTopic creates a new forum topic and returns the thread ID.
func (c *TGClient) CreateForumTopic(ctx context.Context, name string) (int64, error) {
	params := url.Values{
		"chat_id":    {c.chatID},
		"name":       {name},
		"icon_color": {"9367192"},
	}
	var topic TGForumTopic
	if err := c.post(ctx, "createForumTopic", params, &topic); err != nil {
		return 0, err
	}
	return topic.MessageThreadID, nil
}

// EditForumTopic renames a forum topic.
func (c *TGClient) EditForumTopic(ctx context.Context, topicID int64, name string) error {
	params := url.Values{
		"chat_id":           {c.chatID},
		"message_thread_id": {strconv.FormatInt(topicID, 10)},
		"name":              {name},
	}
	return c.post(ctx, "editForumTopic", params, nil)
}

// CloseForumTopic closes a forum topic.
func (c *TGClient) CloseForumTopic(ctx context.Context, topicID int64) error {
	params := url.Values{
		"chat_id":           {c.chatID},
		"message_thread_id": {strconv.FormatInt(topicID, 10)},
	}
	return c.post(ctx, "closeForumTopic", params, nil)
}

// ReopenForumTopic reopens a closed forum topic.
func (c *TGClient) ReopenForumTopic(ctx context.Context, topicID int64) error {
	params := url.Values{
		"chat_id":           {c.chatID},
		"message_thread_id": {strconv.FormatInt(topicID, 10)},
	}
	return c.post(ctx, "reopenForumTopic", params, nil)
}

// DeleteForumTopic deletes a forum topic.
func (c *TGClient) DeleteForumTopic(ctx context.Context, topicID int64) error {
	params := url.Values{
		"chat_id":           {c.chatID},
		"message_thread_id": {strconv.FormatInt(topicID, 10)},
	}
	return c.post(ctx, "deleteForumTopic", params, nil)
}

// FlushOffset performs getUpdates(offset=-1) to flush pending updates.
func (c *TGClient) FlushOffset(ctx context.Context) (int64, error) {
	updates, err := c.GetUpdates(ctx, -1, 0)
	if err != nil {
		return 0, err
	}
	if len(updates) > 0 {
		return updates[len(updates)-1].UpdateID + 1, nil
	}
	return 0, nil
}

// post is the internal helper for POST API calls.
func (c *TGClient) post(ctx context.Context, method string, params url.Values, result interface{}) error {
	reqCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	reqURL := c.apiURL + "/" + method
	req, err := http.NewRequestWithContext(reqCtx, "POST", reqURL, strings.NewReader(params.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}

	var tgResp TGResponse
	if err := json.Unmarshal(body, &tgResp); err != nil {
		return fmt.Errorf("unmarshal %s: %w", method, err)
	}
	if !tgResp.OK {
		return fmt.Errorf("%s failed: %s", method, string(body))
	}

	if result != nil && len(tgResp.Result) > 0 {
		if err := json.Unmarshal(tgResp.Result, result); err != nil {
			return fmt.Errorf("unmarshal %s result: %w", method, err)
		}
	}
	return nil
}

// EscapeHTML escapes &, <, > for Telegram HTML parse_mode.
func EscapeHTML(s string) string {
	s = strings.ReplaceAll(s, "&", "&amp;")
	s = strings.ReplaceAll(s, "<", "&lt;")
	s = strings.ReplaceAll(s, ">", "&gt;")
	return s
}

// IsSupergroup returns true if the chat ID looks like a supergroup (-100...).
func IsSupergroup(chatID string) bool {
	return strings.HasPrefix(chatID, "-100")
}

// --- Bot Info Types ---

// TGBotInfo is the result of getMe.
type TGBotInfo struct {
	ID        int64  `json:"id"`
	IsBot     bool   `json:"is_bot"`
	FirstName string `json:"first_name"`
	Username  string `json:"username"`
}

// TGChatInfo is the result of getChat.
type TGChatInfo struct {
	ID      int64  `json:"id"`
	Type    string `json:"type"`
	Title   string `json:"title"`
	IsForum bool   `json:"is_forum"`
}

// GetMe validates the bot token by calling getMe.
func (c *TGClient) GetMe(ctx context.Context) (*TGBotInfo, error) {
	var info TGBotInfo
	if err := c.post(ctx, "getMe", url.Values{}, &info); err != nil {
		return nil, fmt.Errorf("getMe: %w", err)
	}
	return &info, nil
}

// GetChat retrieves chat info for validation.
func (c *TGClient) GetChat(ctx context.Context, chatID string) (*TGChatInfo, error) {
	params := url.Values{"chat_id": {chatID}}
	var info TGChatInfo
	if err := c.post(ctx, "getChat", params, &info); err != nil {
		return nil, fmt.Errorf("getChat: %w", err)
	}
	return &info, nil
}

// --- Telegram Setup Wizard ---

// TelegramConfig is the persisted configuration written by the wizard.
type TelegramConfig struct {
	BotToken    string `json:"bot_token"`
	BotUsername string `json:"bot_username"`
	ChatID      string `json:"chat_id"`
	IsForum     bool   `json:"is_forum"`
	ChatTitle   string `json:"chat_title,omitempty"`
	SetupAt     string `json:"setup_at"`
}

// TelegramWizard guides the user through Telegram bot setup.
type TelegramWizard struct {
	reader  *bufio.Reader
	config  TelegramConfig
	client  *TGClient
	projDir string
}

// NewTelegramWizard creates a wizard for the given project directory.
func NewTelegramWizard(projectRoot string) *TelegramWizard {
	return &TelegramWizard{
		reader:  bufio.NewReader(os.Stdin),
		projDir: projectRoot,
	}
}

// Run executes the full wizard flow: token → validate → detect chat → write config → install hooks → test.
func (w *TelegramWizard) Run(ctx context.Context, projectRoot string) error {
	fmt.Println("\n🤖 Telegram Setup Wizard")
	fmt.Println("========================")

	// Step 1: Get bot token
	if err := w.promptToken(ctx); err != nil {
		return fmt.Errorf("token validation: %w", err)
	}

	// Step 2: Detect chat_id via getUpdates polling
	if err := w.detectChatID(ctx); err != nil {
		return fmt.Errorf("chat detection: %w", err)
	}

	// Step 3: Validate forum group
	if err := w.validateChat(ctx); err != nil {
		return fmt.Errorf("chat validation: %w", err)
	}

	// Step 4: Write config
	configPath, err := w.writeConfig()
	if err != nil {
		return fmt.Errorf("writing config: %w", err)
	}
	fmt.Printf("✅ Config written to %s\n", configPath)

	// Step 5: Install hooks
	if err := w.installHooks(projectRoot); err != nil {
		return fmt.Errorf("installing hooks: %w", err)
	}
	fmt.Println("✅ Hooks installed in .claude/settings.local.json")

	// Step 6: Send test message
	if err := w.sendTestMessage(ctx); err != nil {
		fmt.Printf("⚠  Test message failed: %v\n", err)
		fmt.Println("   Setup is complete — check your bot token and chat permissions.")
		return nil
	}
	fmt.Println("✅ Test message sent! Check your Telegram chat.")

	return nil
}

func (w *TelegramWizard) prompt(label string) (string, error) {
	fmt.Printf("%s: ", label)
	line, err := w.reader.ReadString('\n')
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(line), nil
}

func (w *TelegramWizard) promptToken(ctx context.Context) error {
	fmt.Println("\nStep 1: Bot Token")
	fmt.Println("  Create a bot via @BotFather on Telegram, then paste the token.")

	token, err := w.prompt("Bot token")
	if err != nil {
		return err
	}
	if token == "" {
		return fmt.Errorf("bot token is required")
	}

	// Validate via getMe
	w.client = NewTGClient(token, "")
	info, err := w.client.GetMe(ctx)
	if err != nil {
		return fmt.Errorf("invalid token — getMe failed: %w", err)
	}

	w.config.BotToken = token
	w.config.BotUsername = info.Username
	fmt.Printf("  ✓ Bot validated: @%s (%s)\n", info.Username, info.FirstName)
	return nil
}

func (w *TelegramWizard) detectChatID(ctx context.Context) error {
	fmt.Println("\nStep 2: Chat Detection")
	fmt.Printf("  Send any message to @%s in your target group/chat.\n", w.config.BotUsername)
	fmt.Println("  Waiting for a message (up to 60 seconds)...")

	// Flush old updates
	offset, err := w.client.FlushOffset(ctx)
	if err != nil {
		return fmt.Errorf("flushing updates: %w", err)
	}

	deadline := time.Now().Add(60 * time.Second)
	for time.Now().Before(deadline) {
		remaining := int(time.Until(deadline).Seconds())
		if remaining <= 0 {
			break
		}
		if remaining > 10 {
			remaining = 10
		}

		updates, err := w.client.GetUpdates(ctx, offset, remaining)
		if err != nil {
			return fmt.Errorf("polling: %w", err)
		}

		for _, u := range updates {
			offset = u.UpdateID + 1
			if u.Message != nil {
				chatID := strconv.FormatInt(u.Message.Chat.ID, 10)
				w.config.ChatID = chatID
				w.client = NewTGClient(w.config.BotToken, chatID)
				fmt.Printf("  ✓ Chat detected: %s\n", chatID)
				return nil
			}
		}
	}

	return fmt.Errorf("no message received within 60 seconds — add the bot to your group and send a message")
}

func (w *TelegramWizard) validateChat(ctx context.Context) error {
	fmt.Println("\nStep 3: Chat Validation")

	info, err := w.client.GetChat(ctx, w.config.ChatID)
	if err != nil {
		return fmt.Errorf("getChat failed: %w", err)
	}

	w.config.IsForum = info.IsForum
	w.config.ChatTitle = info.Title

	if info.IsForum {
		fmt.Printf("  ✓ Forum group detected: %s (topics enabled)\n", info.Title)
	} else if info.Type == "supergroup" || info.Type == "group" {
		fmt.Printf("  ✓ Group chat: %s\n", info.Title)
		fmt.Println("  ℹ  Tip: Enable Topics in group settings for per-project threads.")
	} else {
		fmt.Printf("  ✓ Chat type: %s\n", info.Type)
	}

	return nil
}

// ConfigDir returns the path to ~/.config/orchestra/, creating it if needed.
func ConfigDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("cannot determine home dir: %w", err)
	}
	dir := filepath.Join(home, ".config", "orchestra")
	if err := os.MkdirAll(dir, 0755); err != nil {
		return "", fmt.Errorf("creating config dir: %w", err)
	}
	return dir, nil
}

func (w *TelegramWizard) writeConfig() (string, error) {
	cfgDir, err := ConfigDir()
	if err != nil {
		return "", err
	}

	w.config.SetupAt = time.Now().UTC().Format(time.RFC3339)

	data, err := json.MarshalIndent(w.config, "", "  ")
	if err != nil {
		return "", fmt.Errorf("marshalling config: %w", err)
	}

	path := filepath.Join(cfgDir, "telegram.json")
	if err := os.WriteFile(path, append(data, '\n'), 0600); err != nil {
		return "", fmt.Errorf("writing %s: %w", path, err)
	}
	return path, nil
}

func (w *TelegramWizard) installHooks(projectRoot string) error {
	settingsPath := filepath.Join(projectRoot, ".claude", "settings.local.json")

	// Read existing settings or start fresh
	var settings map[string]interface{}
	data, err := os.ReadFile(settingsPath)
	if err == nil {
		if err := json.Unmarshal(data, &settings); err != nil {
			settings = make(map[string]interface{})
		}
	} else {
		settings = make(map[string]interface{})
	}

	// Ensure permissions.allow includes Bash(curl:*) for Telegram API calls
	perms, _ := settings["permissions"].(map[string]interface{})
	if perms == nil {
		perms = make(map[string]interface{})
		settings["permissions"] = perms
	}
	allow, _ := perms["allow"].([]interface{})
	hasCurl := false
	for _, v := range allow {
		if s, ok := v.(string); ok && s == "Bash(curl:*)" {
			hasCurl = true
			break
		}
	}
	if !hasCurl {
		allow = append(allow, "Bash(curl:*)")
		perms["allow"] = allow
	}

	// Ensure .claude directory exists
	if err := os.MkdirAll(filepath.Join(projectRoot, ".claude"), 0755); err != nil {
		return fmt.Errorf("creating .claude dir: %w", err)
	}

	out, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return fmt.Errorf("marshalling settings: %w", err)
	}

	if err := os.WriteFile(settingsPath, append(out, '\n'), 0644); err != nil {
		return fmt.Errorf("writing settings: %w", err)
	}
	return nil
}

func (w *TelegramWizard) sendTestMessage(ctx context.Context) error {
	text := fmt.Sprintf("🎵 Orchestra connected! Bot: @%s", w.config.BotUsername)
	_, err := w.client.SendMessage(ctx, text, 0, "")
	return err
}

// LoadTelegramConfig reads the persisted telegram config from ~/.config/orchestra/telegram.json.
func LoadTelegramConfig() (*TelegramConfig, error) {
	cfgDir, err := ConfigDir()
	if err != nil {
		return nil, err
	}

	data, err := os.ReadFile(filepath.Join(cfgDir, "telegram.json"))
	if err != nil {
		return nil, fmt.Errorf("reading telegram config: %w", err)
	}

	var cfg TelegramConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parsing telegram config: %w", err)
	}
	return &cfg, nil
}
