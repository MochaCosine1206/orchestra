package bridge

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// HandleBotCommand processes /command messages from Telegram.
func (b *Bridge) HandleBotCommand(ctx context.Context, text string, threadID int64) {
	// Strip @botname suffix
	cmd := strings.Fields(text)[0]
	if idx := strings.Index(cmd, "@"); idx >= 0 {
		cmd = cmd[:idx]
	}

	switch cmd {
	case "/status":
		b.cmdStatus(ctx, threadID)
	case "/list":
		b.cmdList(ctx)
	case "/newproject":
		b.cmdNewProject(ctx, text, threadID)
	case "/close":
		b.cmdClose(ctx, threadID)
	case "/pause":
		b.cmdPause(ctx, threadID)
	case "/resume":
		b.cmdResume(ctx, threadID)
	case "/ship":
		b.cmdShip(ctx, threadID)
	case "/help":
		b.cmdHelp(ctx, threadID)
	default:
		// Unknown command — ignore
	}
}

func (b *Bridge) cmdStatus(ctx context.Context, threadID int64) {
	slug := FindSlugByTopicID(b.cfg.StateBase, threadID)
	if slug == "" {
		b.tg.SendMessage(ctx, "No project found for this topic.", threadID, "")
		return
	}

	statusText := "<b>Project Status: " + EscapeHTML(slug) + "</b>"

	if summary, ok := ReadLastSummary(b.cfg.StateBase, slug); ok {
		lines := strings.SplitN(summary, "\n", 3)
		short := strings.Join(lines, "\n")
		if len(short) > 200 {
			short = short[:200]
		}
		statusText += "\n<b>Last summary:</b> " + EscapeHTML(short)
	}

	b.mu.RLock()
	ps, exists := b.projects[slug]
	b.mu.RUnlock()
	if exists {
		statusText += fmt.Sprintf("\nBranch: %s", EscapeHTML(ps.Branch))
		statusText += fmt.Sprintf("\nWaiters: %d", len(ps.waiters))
		statusText += fmt.Sprintf("\nQueued messages: %d", len(ps.msgQueue))
	}

	b.tg.SendMessage(ctx, statusText, threadID, "HTML")
}

func (b *Bridge) cmdList(ctx context.Context) {
	slugs := ListProjectSlugs(b.cfg.StateBase)
	if len(slugs) == 0 {
		b.tg.SendMessage(ctx, "<b>Active Projects</b>\nNo projects with topics found.", 1, "HTML")
		return
	}

	text := "<b>Active Projects</b>"
	for _, slug := range slugs {
		topicID, _ := ReadTopicID(b.cfg.StateBase, slug)
		closed := ""
		if IsTopicClosed(b.cfg.StateBase, slug) {
			closed = " [closed]"
		}
		text += fmt.Sprintf("\n- <b>%s</b> (topic %d)%s", EscapeHTML(slug), topicID, closed)
	}

	// Send to General topic (id=1 in forum groups)
	b.tg.SendMessage(ctx, text, 1, "HTML")
}

func (b *Bridge) cmdNewProject(ctx context.Context, text string, threadID int64) {
	parts := strings.Fields(text)
	if len(parts) < 2 {
		b.tg.SendMessage(ctx, "Usage: /newproject &lt;name&gt;", threadID, "HTML")
		return
	}
	name := sanitizeSlug(parts[1])
	if name == "" {
		b.tg.SendMessage(ctx, "Invalid project name.", threadID, "")
		return
	}

	topicID, err := b.tg.CreateForumTopic(ctx, name)
	if err != nil {
		b.tg.SendMessage(ctx, "Failed to create topic: "+EscapeHTML(err.Error()), threadID, "HTML")
		return
	}
	WriteTopicID(b.cfg.StateBase, name, topicID)
	WriteTopicName(b.cfg.StateBase, name, name)
	b.router.RegisterTopic(topicID, name)

	b.tg.SendMessage(ctx, fmt.Sprintf("Created topic <b>%s</b> (thread %d)", EscapeHTML(name), topicID), threadID, "HTML")
}

func (b *Bridge) cmdClose(ctx context.Context, threadID int64) {
	slug := FindSlugByTopicID(b.cfg.StateBase, threadID)
	if slug == "" {
		b.tg.SendMessage(ctx, "No project found for this topic.", threadID, "")
		return
	}
	b.TopicArchive(ctx, slug, "Done")
	b.tg.SendMessage(ctx, fmt.Sprintf("Project <b>%s</b> archived as [Done]", EscapeHTML(slug)), threadID, "HTML")
}

func (b *Bridge) cmdPause(ctx context.Context, threadID int64) {
	slug := FindSlugByTopicID(b.cfg.StateBase, threadID)
	if slug == "" {
		b.tg.SendMessage(ctx, "No project found for this topic.", threadID, "")
		return
	}
	b.TopicArchive(ctx, slug, "Paused")
	b.tg.SendMessage(ctx, fmt.Sprintf("Project <b>%s</b> marked as [Paused]", EscapeHTML(slug)), threadID, "HTML")
}

func (b *Bridge) cmdResume(ctx context.Context, threadID int64) {
	slug := FindSlugByTopicID(b.cfg.StateBase, threadID)
	if slug == "" {
		b.tg.SendMessage(ctx, "No project found for this topic.", threadID, "")
		return
	}
	b.TopicReopen(ctx, slug)
	b.tg.SendMessage(ctx, fmt.Sprintf("Project <b>%s</b> resumed", EscapeHTML(slug)), threadID, "HTML")
}

func (b *Bridge) cmdShip(ctx context.Context, threadID int64) {
	slug := FindSlugByTopicID(b.cfg.StateBase, threadID)
	if slug == "" {
		b.tg.SendMessage(ctx, "No project found for this topic.", threadID, "")
		return
	}

	b.mu.RLock()
	ps, exists := b.projects[slug]
	b.mu.RUnlock()
	if !exists || ps.CWD == "" {
		b.tg.SendMessage(ctx, "No active session for this project. Start a Claude session first.", threadID, "")
		return
	}

	b.HandleShip(ctx, threadID, slug, ps.CWD)
}

func (b *Bridge) cmdHelp(ctx context.Context, threadID int64) {
	help := `<b>Claude Orchestra Bot Commands</b>

/status — Show current session status
/list — List active project topics
/newproject &lt;name&gt; — Create a new project topic
/close — Close current project topic
/pause — Mark project as paused
/resume — Resume paused project
/ship — Commit, push, and create PR
/help — Show this help message

Send any other text to relay to the active Claude session.`

	b.tg.SendMessage(ctx, help, threadID, "HTML")
}

func sanitizeSlug(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	s = strings.ReplaceAll(s, " ", "-")
	var b strings.Builder
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '.' || r == '_' || r == '-' {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// ReadLegacySummary checks the legacy /tmp path for summaries.
func ReadLegacySummary() (string, bool) {
	data, err := os.ReadFile("/tmp/claude-session-summary.txt")
	if err != nil {
		return "", false
	}
	return string(data), true
}

// RemoveLegacySummary removes the legacy summary file.
func RemoveLegacySummary() {
	os.Remove("/tmp/claude-session-summary.txt")
}

// ProjectDirFromSlug attempts to find the project directory from state files.
// Checks common locations.
func ProjectDirFromSlug(slug string) string {
	// Check Symphonies dir
	candidates := []string{
		filepath.Join(os.Getenv("HOME"), "Symphonies", slug),
		filepath.Join(os.Getenv("HOME"), "Documents", "personal-projects", "ai-projects", slug),
	}
	for _, c := range candidates {
		if info, err := os.Stat(c); err == nil && info.IsDir() {
			return c
		}
	}
	return ""
}

