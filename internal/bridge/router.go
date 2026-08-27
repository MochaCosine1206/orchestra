package bridge

import (
	"context"
	"log/slog"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Waiter represents an HTTP handler waiting for a TG response.
type Waiter struct {
	Ch      chan TGUpdate
	TopicID int64
}

// ProjectState tracks per-project state for message routing.
type ProjectState struct {
	Slug    string
	TopicID int64
	Branch  string
	CWD     string // project directory path

	mu       sync.Mutex
	waiters  []Waiter
	msgQueue []TGUpdate // queued messages when no waiter
}

// AddWaiter registers a waiter channel for this project.
func (ps *ProjectState) AddWaiter(topicID int64) chan TGUpdate {
	ps.mu.Lock()
	defer ps.mu.Unlock()
	ch := make(chan TGUpdate, 1)
	ps.waiters = append(ps.waiters, Waiter{Ch: ch, TopicID: topicID})
	return ch
}

// RemoveWaiter removes a waiter channel.
func (ps *ProjectState) RemoveWaiter(ch chan TGUpdate) {
	ps.mu.Lock()
	defer ps.mu.Unlock()
	for i, w := range ps.waiters {
		if w.Ch == ch {
			ps.waiters = append(ps.waiters[:i], ps.waiters[i+1:]...)
			return
		}
	}
}

// DeliverOrQueue tries to deliver an update to a waiter; queues if none waiting.
func (ps *ProjectState) DeliverOrQueue(u TGUpdate) bool {
	ps.mu.Lock()
	defer ps.mu.Unlock()

	// Try to deliver to first waiter
	if len(ps.waiters) > 0 {
		w := ps.waiters[0]
		select {
		case w.Ch <- u:
			ps.waiters = ps.waiters[1:]
			return true
		default:
		}
	}

	// Queue for later (PostToolUse check)
	ps.msgQueue = append(ps.msgQueue, u)
	return false
}

// DrainQueue returns and clears all queued messages.
func (ps *ProjectState) DrainQueue() []TGUpdate {
	ps.mu.Lock()
	defer ps.mu.Unlock()
	msgs := ps.msgQueue
	ps.msgQueue = nil
	return msgs
}

// Router dispatches incoming TG updates to the correct project.
type Router struct {
	bridge *Bridge
	mu     sync.RWMutex
	// topicID → slug mapping for fast lookup
	topicToSlug map[int64]string
}

// NewRouter creates a router.
func NewRouter(b *Bridge) *Router {
	return &Router{
		bridge:      b,
		topicToSlug: make(map[int64]string),
	}
}

// RegisterTopic maps a topic ID to a slug.
func (r *Router) RegisterTopic(topicID int64, slug string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.topicToSlug[topicID] = slug
}

// Dispatch routes a TG update to the appropriate project or handles it as a command.
func (r *Router) Dispatch(ctx context.Context, u TGUpdate) {
	// Handle callback queries
	if u.CallbackQuery != nil {
		r.dispatchCallback(ctx, u)
		return
	}

	// Handle text messages
	if u.Message != nil && u.Message.Text != "" {
		r.dispatchMessage(ctx, u)
		return
	}
}

func (r *Router) dispatchCallback(ctx context.Context, u TGUpdate) {
	cb := u.CallbackQuery
	data := cb.Data

	// Extract action prefix
	action := data
	if idx := strings.Index(data, ":"); idx >= 0 {
		action = data[:idx]
	}

	// Git action callbacks (ship/merge/push/pr)
	switch action {
	case "ship", "merge", "push", "pr":
		r.handleGitCallback(ctx, u, action)
		return
	}

	// Approval callbacks (approve/deny/skip)
	switch action {
	case "approve", "deny", "skip":
		r.deliverToWaiter(ctx, u)
		return
	}

	// AskUser callbacks (aq:...)
	if strings.HasPrefix(data, "aq:") {
		r.deliverToWaiter(ctx, u)
		return
	}

	slog.Warn("unknown callback data", "data", data)
	r.bridge.tg.AnswerCallback(ctx, cb.ID, "Unknown action")
}

func (r *Router) handleGitCallback(ctx context.Context, u TGUpdate, action string) {
	cb := u.CallbackQuery

	// Answer callback immediately
	r.bridge.tg.AnswerCallback(ctx, cb.ID, ActionLabel(action))

	// Edit the original message
	if cb.Message != nil {
		r.bridge.tg.EditMessage(ctx, cb.Message.MessageID, ActionLabel(action), "HTML")
	}

	// Find the project from callback data (format: "action:slug")
	parts := strings.SplitN(cb.Data, ":", 2)
	if len(parts) < 2 {
		return
	}
	slug := parts[1]

	ps := r.bridge.GetOrCreateProject(slug)
	if ps.CWD == "" {
		slog.Warn("no CWD for slug, cannot execute git action", "slug", slug)
		topicID := int64(0)
		if cb.Message != nil {
			topicID = cb.Message.MessageThreadID
		}
		r.bridge.tg.SendMessage(ctx, "Cannot execute — no project directory known. Start a Claude session in this project first.", topicID, "HTML")
		return
	}

	topicID := int64(0)
	if cb.Message != nil {
		topicID = cb.Message.MessageThreadID
	}

	switch action {
	case "ship":
		r.bridge.HandleShip(ctx, topicID, slug, ps.CWD)
	case "merge":
		r.bridge.HandleMerge(ctx, topicID, slug, ps.CWD)
	case "push":
		r.bridge.HandlePush(ctx, topicID, slug, ps.CWD)
	case "pr":
		r.bridge.HandlePR(ctx, topicID, slug, ps.CWD)
	}

	// After ship, try to deliver to waiter (for the stop-hook poll loop)
	// Ship sends a Merge button, so keep the waiter alive
	if action != "ship" {
		r.deliverToWaiter(ctx, u)
	}
}

func (r *Router) dispatchMessage(ctx context.Context, u TGUpdate) {
	msg := u.Message
	text := msg.Text
	threadID := msg.MessageThreadID
	chatID := msg.Chat.ID

	// Verify it's from our chat
	expectedChatID, _ := strconv.ParseInt(r.bridge.cfg.ChatID, 10, 64)
	if chatID != expectedChatID {
		return
	}

	// Bot commands
	if strings.HasPrefix(text, "/") {
		r.bridge.HandleBotCommand(ctx, text, threadID)
		return
	}

	// Route by topic → project
	slug := r.resolveSlug(threadID)
	if slug == "" {
		slog.Info("no project found for topic, ignoring message", "topic_id", threadID)
		return
	}

	ps := r.bridge.GetOrCreateProject(slug)
	ps.DeliverOrQueue(u)
}

func (r *Router) deliverToWaiter(ctx context.Context, u TGUpdate) {
	// For callback queries, find project by topic ID
	var threadID int64
	if u.CallbackQuery != nil && u.CallbackQuery.Message != nil {
		threadID = u.CallbackQuery.Message.MessageThreadID
	} else if u.Message != nil {
		threadID = u.Message.MessageThreadID
	}

	slug := r.resolveSlug(threadID)
	if slug == "" {
		// Try from callback data
		if u.CallbackQuery != nil {
			parts := strings.SplitN(u.CallbackQuery.Data, ":", 3)
			if len(parts) >= 2 {
				// For approval callbacks, we can't derive slug from data
				// Deliver to ALL projects that have waiters (only one should match)
				r.bridge.mu.RLock()
				for _, ps := range r.bridge.projects {
					ps.DeliverOrQueue(u)
				}
				r.bridge.mu.RUnlock()
				return
			}
		}
		return
	}

	ps := r.bridge.GetOrCreateProject(slug)
	ps.DeliverOrQueue(u)
}

func (r *Router) resolveSlug(threadID int64) string {
	r.mu.RLock()
	slug, ok := r.topicToSlug[threadID]
	r.mu.RUnlock()
	if ok {
		return slug
	}

	// Fallback: check state files
	slug = FindSlugByTopicID(r.bridge.cfg.StateBase, threadID)
	if slug != "" {
		r.mu.Lock()
		r.topicToSlug[threadID] = slug
		r.mu.Unlock()
	}
	return slug
}

// WaitForUpdate blocks until a TG update arrives for the project or timeout.
func (b *Bridge) WaitForUpdate(slug string, topicID int64, timeout time.Duration) (TGUpdate, bool) {
	ps := b.GetOrCreateProject(slug)
	ch := ps.AddWaiter(topicID)
	defer ps.RemoveWaiter(ch)

	timer := time.NewTimer(timeout)
	defer timer.Stop()

	select {
	case u := <-ch:
		return u, true
	case <-timer.C:
		return TGUpdate{}, false
	}
}
