package bridge

import (
	"context"
	"fmt"
	"log/slog"
	"os/exec"
	"strings"
)

// TopicEnsureNamed ensures a topic exists for the project, creating if needed,
// renaming if the display name changed. Returns the topic ID (0 if private chat).
func (b *Bridge) TopicEnsureNamed(ctx context.Context, slug, desiredName, branch string) int64 {
	// Private chats don't support forum topics
	if !IsSupergroup(b.cfg.ChatID) {
		return 0
	}

	// Check cache
	topicID, ok := ReadTopicID(b.cfg.StateBase, slug)
	if !ok {
		// Create new topic
		var err error
		topicID, err = b.tg.CreateForumTopic(ctx, desiredName)
		if err != nil {
			slog.Warn("failed to create topic", "slug", slug, "err", err)
			return 0
		}
		WriteTopicID(b.cfg.StateBase, slug, topicID)
		WriteTopicName(b.cfg.StateBase, slug, desiredName)
		slog.Info("created topic", "topic_id", topicID, "slug", slug, "name", desiredName)
	}

	// Store branch
	if branch != "" {
		WriteBranch(b.cfg.StateBase, slug, branch)
	}

	// Rename if needed
	cachedName := ReadTopicName(b.cfg.StateBase, slug)
	if cachedName != desiredName && desiredName != "" {
		if err := b.tg.EditForumTopic(ctx, topicID, desiredName); err != nil {
			slog.Warn("failed to rename topic", "topic_id", topicID, "err", err)
		} else {
			WriteTopicName(b.cfg.StateBase, slug, desiredName)
		}
	}

	return topicID
}

// TopicArchive renames to [status] slug then closes.
func (b *Bridge) TopicArchive(ctx context.Context, slug, status string) {
	topicID, ok := ReadTopicID(b.cfg.StateBase, slug)
	if !ok {
		return
	}
	name := fmt.Sprintf("[%s] %s", status, slug)
	b.tg.EditForumTopic(ctx, topicID, name)
	WriteTopicName(b.cfg.StateBase, slug, name)
	b.tg.CloseForumTopic(ctx, topicID)
	MarkTopicClosed(b.cfg.StateBase, slug)
}

// TopicReopen reopens a closed topic.
func (b *Bridge) TopicReopen(ctx context.Context, slug string) {
	topicID, ok := ReadTopicID(b.cfg.StateBase, slug)
	if !ok {
		return
	}
	b.tg.ReopenForumTopic(ctx, topicID)
	MarkTopicOpen(b.cfg.StateBase, slug)
}

// ResolveProject resolves cwd to slug, branch, display name, and topic ID.
// This is the common setup used by all HTTP handlers.
func (b *Bridge) ResolveProject(ctx context.Context, cwd string) (slug, branch string, topicID int64) {
	slug = ProjectSlug(cwd)
	branch = gitBranch(cwd)
	repoName := RepoDisplayName(slug)
	taskName := BranchToTaskName(branch)
	displayName := ComposeTopicName(repoName, taskName)
	topicID = b.TopicEnsureNamed(ctx, slug, displayName, branch)
	return
}

// gitBranch returns the current branch for a directory.
func gitBranch(dir string) string {
	cmd := exec.Command("git", "branch", "--show-current")
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}
