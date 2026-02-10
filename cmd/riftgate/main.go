// Command riftgate is a WireGuard VPN tunnel over WebRTC. It allows a single
// user to access their home network from anywhere without exposing the home
// network's public IP.
//
// Usage:
//
//	sudo riftgate up [--config /path/to/config.toml]
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/kuuji/riftgate/internal/agent"
	"github.com/kuuji/riftgate/internal/config"
)

func main() {
	configPath := flag.String("config", "", "path to config file (default: ~/.config/riftgate/config.toml)")
	verbose := flag.Bool("v", false, "enable verbose/debug logging")
	flag.Parse()

	// Set up logger.
	level := slog.LevelInfo
	if *verbose {
		level = slog.LevelDebug
	}
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{
		Level: level,
	}))

	// Determine config path.
	cfgPath := *configPath
	if cfgPath == "" {
		var err error
		cfgPath, err = config.DefaultConfigPath()
		if err != nil {
			logger.Error("determining config path", "error", err)
			os.Exit(1)
		}
	}

	// Load config.
	cfg, err := config.LoadConfig(cfgPath)
	if err != nil {
		logger.Error("loading config", "path", cfgPath, "error", err)
		os.Exit(1)
	}

	// Validate required fields.
	if err := validateConfig(cfg); err != nil {
		logger.Error("invalid config", "error", err)
		os.Exit(1)
	}

	// Set up context with signal handling.
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	// Create and run the agent.
	a := agent.New(cfg, logger)

	logger.Info("starting riftgate", "config", cfgPath)

	if err := a.Run(ctx); err != nil {
		if ctx.Err() != nil {
			// Context was cancelled (signal received) — clean shutdown.
			logger.Info("riftgate stopped")
			return
		}
		logger.Error("agent error", "error", err)
		os.Exit(1)
	}
}

// validateConfig checks that all required configuration fields are present.
func validateConfig(cfg *config.Config) error {
	if cfg.Network.ServerURL == "" {
		return fmt.Errorf("network.server_url is required")
	}
	if cfg.Device.Name == "" {
		return fmt.Errorf("device.name is required")
	}
	if cfg.Device.PrivateKey.IsZero() {
		return fmt.Errorf("device.private_key is required")
	}
	if cfg.Device.Address == "" {
		return fmt.Errorf("device.address is required")
	}
	return nil
}
