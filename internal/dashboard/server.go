package dashboard

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/MochaCosine1206/orchestra/internal/db"
)

// Server is the HTTP dashboard server backed by SQLite.
// Routes: page views (/, /config, /artifacts, /briefing, /project/, /task/, /agent/, /git/),
// HTMX fragments (/api/factory, /api/project/, /api/task/, /api/agent/),
// config API (/api/config/{section}), SSE (/events), and static assets (/static/).
type Server struct {
	DB       *db.DB
	Port     int
	server   *http.Server
	listener net.Listener
	mu       sync.Mutex
}

// Start registers all routes and begins serving HTTP.
// It blocks until ctx is cancelled, then performs graceful shutdown.
func (s *Server) Start(ctx context.Context) error {
	mux := http.NewServeMux()
	s.RegisterRoutes(mux)

	s.mu.Lock()
	s.server = &http.Server{Handler: mux}
	if s.listener == nil {
		ln, err := net.Listen("tcp", fmt.Sprintf(":%d", s.Port))
		if err != nil {
			s.mu.Unlock()
			return fmt.Errorf("listening on port %d: %w", s.Port, err)
		}
		s.listener = ln
	}
	s.mu.Unlock()

	go func() {
		slog.Info("Dashboard server listening", "addr", s.listener.Addr())
		if err := s.server.Serve(s.listener); err != nil && err != http.ErrServerClosed {
			slog.Error("Dashboard server error", "err", err)
		}
	}()
	<-ctx.Done()
	shutCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return s.server.Shutdown(shutCtx)
}

// Shutdown performs graceful shutdown of the server.
func (s *Server) Shutdown(ctx context.Context) error {
	s.mu.Lock()
	srv := s.server
	s.mu.Unlock()
	if srv == nil {
		return nil
	}
	return srv.Shutdown(ctx)
}

// Addr returns the listener address, or empty string if not listening.
func (s *Server) Addr() string {
	if s.listener != nil {
		return s.listener.Addr().String()
	}
	return ""
}

// ListenOn sets a pre-created listener (useful for tests).
func (s *Server) ListenOn(ln net.Listener) { s.listener = ln }
