package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"syscall"

	"github.com/MochaCosine1206/orchestra/internal/bridge"
)

func main() {
	port := flag.Int("port", bridge.DefaultPort, "HTTP server port")
	idleTimeout := flag.Duration("idle-timeout", bridge.DefaultIdleTimeout, "Shutdown after this duration of inactivity")
	flag.Parse()

	// Handle "stop" subcommand
	if flag.NArg() > 0 && flag.Arg(0) == "stop" {
		stopBridge(*port)
		return
	}

	// Load configuration
	cfg, err := bridge.LoadConfig()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Configuration error: %v\n", err)
		os.Exit(1)
	}
	cfg.Port = *port
	cfg.IdleTimeout = *idleTimeout

	// Ensure state dir exists
	os.MkdirAll(cfg.StateBase, 0755)

	// Set up logging to both stderr and file
	logFile, err := os.OpenFile(cfg.LogFile, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Cannot open log file %s: %v\n", cfg.LogFile, err)
		os.Exit(1)
	}
	defer logFile.Close()

	multiWriter := io.MultiWriter(os.Stderr, logFile)
	slog.SetDefault(slog.New(slog.NewTextHandler(multiWriter, &slog.HandlerOptions{
		AddSource: true,
	})))

	// Context with signal handling
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		sig := <-sigCh
		slog.Info("Received signal, shutting down", "signal", sig)
		cancel()
	}()

	// Create and run bridge
	b := bridge.New(cfg)
	if err := b.Run(ctx); err != nil {
		slog.Error("Bridge stopped", "error", err)
	}
	slog.Info("telegram-bridge stopped")
}

// stopBridge finds and kills the process listening on the bridge port.
func stopBridge(port int) {
	fmt.Printf("Stopping telegram-bridge on port %d...\n", port)

	cmd := exec.Command("lsof", "-ti", fmt.Sprintf(":%d", port))
	out, err := cmd.Output()
	if err != nil || strings.TrimSpace(string(out)) == "" {
		fmt.Println("No telegram-bridge process found.")
		return
	}

	pid := strings.TrimSpace(string(out))
	killCmd := exec.Command("kill", pid)
	if err := killCmd.Run(); err != nil {
		fmt.Printf("Failed to stop process %s: %v\n", pid, err)
		os.Exit(1)
	}
	fmt.Printf("Stopped (PID %s).\n", pid)
}
