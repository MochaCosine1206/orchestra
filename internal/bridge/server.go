// server.go — Bridge HTTP server for Telegram webhook/hook endpoints.
// Dashboard HTTP server lives in internal/dashboard/server.go.
package bridge

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"time"
)

// StartServer starts the HTTP server on the configured port.
func (b *Bridge) StartServer(ctx context.Context) error {
	mux := http.NewServeMux()

	// Hook endpoints
	mux.HandleFunc("/hooks/stop", b.handleStop)
	mux.HandleFunc("/hooks/post-tool-use", b.handlePostToolUse)
	mux.HandleFunc("/hooks/permission", b.handlePermission)
	mux.HandleFunc("/hooks/ask-user", b.handleAskUser)
	mux.HandleFunc("/hooks/pre-tool-use", b.handlePreToolUse)

	// HITL forwarding endpoint
	mux.HandleFunc("/hooks/hitl", b.handleHITL)

	// Utility endpoints
	mux.HandleFunc("/health", b.handleHealth)
	mux.HandleFunc("/pending/", b.handlePending) // GET /pending/<slug>

	b.server = &http.Server{
		Addr:    fmt.Sprintf(":%d", b.cfg.Port),
		Handler: mux,
	}

	// Start server in goroutine
	go func() {
		slog.Info("HTTP server listening", "port", b.cfg.Port)
		if err := b.server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			slog.Error("HTTP server error", "err", err)
		}
	}()

	// Wait for context cancellation
	<-ctx.Done()
	shutCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return b.server.Shutdown(shutCtx)
}
