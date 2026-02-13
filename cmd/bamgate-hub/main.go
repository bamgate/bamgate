// Command bamgate-hub runs a standalone signaling server for local/LAN
// testing. It relays WebRTC signaling messages (SDP offers/answers, ICE
// candidates) between connected bamgate peers.
//
// Usage:
//
//	bamgate-hub -addr :8080
package main

import (
	"context"
	"flag"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"github.com/kuuji/bamgate/internal/signaling"
)

func main() {
	addr := flag.String("addr", ":8080", "listen address")
	flag.Parse()

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{
		Level: slog.LevelDebug,
	}))

	hub := signaling.NewHub(logger)

	srv := &http.Server{
		Addr:    *addr,
		Handler: hub,
	}

	// Graceful shutdown on SIGINT/SIGTERM.
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	go func() {
		<-ctx.Done()
		logger.Info("shutting down")
		hub.Close()
		if err := srv.Close(); err != nil {
			logger.Error("server close", "error", err)
		}
	}()

	logger.Info("signaling hub listening", "addr", *addr)
	if err := srv.ListenAndServe(); err != http.ErrServerClosed {
		logger.Error("server error", "error", err)
		os.Exit(1)
	}
}
