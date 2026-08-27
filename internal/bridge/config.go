package bridge

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	DefaultPort        = 19225
	DefaultIdleTimeout = 30 * time.Minute
	DefaultPollTimeout = 30 // seconds for TG long-poll
	DefaultReplyWait   = 120 * time.Second
	DefaultPermWait    = 300 * time.Second
	DefaultAskUserWait = 24 * time.Hour
	DefaultRateLimit   = 15 * time.Second
)

// Config holds all runtime configuration for the bridge.
type Config struct {
	Port            int
	IdleTimeout     time.Duration
	BotToken        string
	ChatID          string // raw string, may be negative
	ForumChatID     string // supergroup with topics; overrides ChatID when set
	ReplyTimeout    time.Duration
	StateBase       string // ~/.claude-session-state
	OffsetFile      string // persistent offset path
	LogFile         string
	PollTimeoutSecs int
}

// LoadConfig reads configuration from environment / ~/.telegram-notify.
func LoadConfig() (*Config, error) {
	cfg := &Config{
		Port:            DefaultPort,
		IdleTimeout:     DefaultIdleTimeout,
		ReplyTimeout:    DefaultReplyWait,
		PollTimeoutSecs: DefaultPollTimeout,
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("cannot determine home dir: %w", err)
	}
	cfg.StateBase = filepath.Join(home, ".claude-session-state")
	cfg.OffsetFile = filepath.Join(cfg.StateBase, "telegram-bridge-offset")
	cfg.LogFile = filepath.Join(cfg.StateBase, "telegram-bridge.log")

	// Source ~/.telegram-notify (bash-style key=value)
	notifyFile := filepath.Join(home, ".telegram-notify")
	if err := loadEnvFile(notifyFile); err == nil {
		// loaded
	}

	cfg.BotToken = os.Getenv("TELEGRAM_BOT_TOKEN")
	cfg.ChatID = os.Getenv("TELEGRAM_CHAT_ID")
	cfg.ForumChatID = os.Getenv("TELEGRAM_FORUM_CHAT_ID")

	if cfg.BotToken == "" {
		return nil, fmt.Errorf("TELEGRAM_BOT_TOKEN not set (check ~/.telegram-notify)")
	}
	if cfg.ChatID == "" && cfg.ForumChatID == "" {
		return nil, fmt.Errorf("TELEGRAM_CHAT_ID or TELEGRAM_FORUM_CHAT_ID not set")
	}

	// Forum chat overrides regular chat
	if cfg.ForumChatID != "" {
		cfg.ChatID = cfg.ForumChatID
	}

	return cfg, nil
}

// loadEnvFile parses a simple bash-style env file (export KEY="VALUE" or KEY=VALUE).
func loadEnvFile(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		line = strings.TrimPrefix(line, "export ")
		parts := strings.SplitN(line, "=", 2)
		if len(parts) != 2 {
			continue
		}
		key := strings.TrimSpace(parts[0])
		val := strings.TrimSpace(parts[1])
		val = strings.Trim(val, `"'`)
		if os.Getenv(key) == "" {
			os.Setenv(key, val)
		}
	}
	return nil
}
