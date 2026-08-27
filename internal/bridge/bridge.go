package bridge

import (
	"context"
	"log/slog"
	"net/http"
	"sync"
	"time"
)

// Bridge is the central coordinator for the telegram-bridge server.
type Bridge struct {
	cfg       *Config
	tg        *TGClient
	router    *Router
	server    *http.Server
	offset    int64
	startTime time.Time

	mu       sync.RWMutex
	projects map[string]*ProjectState

	// Rate limiting for PostToolUse
	rateMu    sync.Mutex
	rateLimit map[string]time.Time // slug → last check time

	// Idle timeout
	activityMu   sync.Mutex
	lastActivity  time.Time
	idleCancel    context.CancelFunc
}

// New creates a new Bridge from config.
func New(cfg *Config) *Bridge {
	tg := NewTGClient(cfg.BotToken, cfg.ChatID)
	b := &Bridge{
		cfg:          cfg,
		tg:           tg,
		projects:     make(map[string]*ProjectState),
		rateLimit:    make(map[string]time.Time),
		startTime:    time.Now(),
		lastActivity: time.Now(),
	}
	b.router = NewRouter(b)
	return b
}

// Run starts the bridge (poller + HTTP server) and blocks until shutdown.
func (b *Bridge) Run(ctx context.Context) error {
	slog.Info("telegram-bridge starting", "port", b.cfg.Port, "idle", b.cfg.IdleTimeout)

	// Create idle timeout context
	idleCtx, idleCancel := context.WithCancel(ctx)
	b.idleCancel = idleCancel

	// Start idle watcher
	go b.idleWatcher(idleCtx)

	// Start poller
	b.StartPoller(idleCtx)

	// Start HTTP server (blocks until context done)
	return b.StartServer(idleCtx)
}

// GetOrCreateProject returns the ProjectState for a slug, creating if needed.
func (b *Bridge) GetOrCreateProject(slug string) *ProjectState {
	b.mu.RLock()
	ps, ok := b.projects[slug]
	b.mu.RUnlock()
	if ok {
		return ps
	}

	b.mu.Lock()
	defer b.mu.Unlock()
	// Double-check after lock
	if ps, ok := b.projects[slug]; ok {
		return ps
	}
	ps = &ProjectState{Slug: slug}
	b.projects[slug] = ps

	// Register topic mapping if we have one
	topicID, ok := ReadTopicID(b.cfg.StateBase, slug)
	if ok {
		b.router.RegisterTopic(topicID, slug)
		ps.TopicID = topicID
	}

	return ps
}

// touchActivity resets the idle timer.
func (b *Bridge) touchActivity() {
	b.activityMu.Lock()
	b.lastActivity = time.Now()
	b.activityMu.Unlock()
}

// isRateLimited returns true if PostToolUse check was <15s ago for this slug.
func (b *Bridge) isRateLimited(slug string) bool {
	b.rateMu.Lock()
	defer b.rateMu.Unlock()
	last, ok := b.rateLimit[slug]
	if ok && time.Since(last) < DefaultRateLimit {
		return true
	}
	b.rateLimit[slug] = time.Now()
	return false
}

// idleWatcher shuts down the bridge after IdleTimeout of inactivity.
func (b *Bridge) idleWatcher(ctx context.Context) {
	ticker := time.NewTicker(1 * time.Minute)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			b.activityMu.Lock()
			idle := time.Since(b.lastActivity)
			b.activityMu.Unlock()

			if idle > b.cfg.IdleTimeout {
				slog.Info("idle timeout reached, shutting down", "idle", idle.Round(time.Second))
				b.idleCancel()
				return
			}
		}
	}
}
