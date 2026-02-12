package main

import (
	"context"
	"fmt"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/kuuji/riftgate/internal/agent"
	"github.com/kuuji/riftgate/internal/config"
)

var upCmd = &cobra.Command{
	Use:   "up",
	Short: "Connect to the network",
	Long: `Start the riftgate agent: create a WireGuard tunnel, connect to the
signaling server, and bridge traffic over WebRTC data channels.

Requires CAP_NET_ADMIN to create the TUN device and configure the
network interface. You can grant this in one of three ways:

  1. Run 'sudo riftgate install' once to set capabilities on the binary
     (then 'riftgate up' works without sudo)
  2. Run via the systemd service: sudo systemctl start riftgate
  3. Run directly with sudo: sudo riftgate up`,
	RunE: runUp,
}

func runUp(cmd *cobra.Command, args []string) error {
	cfg, err := loadConfig()
	if err != nil {
		return err
	}

	if err := validateConfig(cfg); err != nil {
		return fmt.Errorf("invalid config: %w", err)
	}

	// Set up context with signal handling.
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	a := agent.New(cfg, globalLogger)

	globalLogger.Info("starting riftgate", "config", resolvedConfigPath())

	if err := a.Run(ctx); err != nil {
		if ctx.Err() != nil {
			// Context was cancelled (signal received) — clean shutdown.
			globalLogger.Info("riftgate stopped")
			return nil
		}
		return fmt.Errorf("agent error: %w", err)
	}

	return nil
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

// loadConfig loads the TOML config from the resolved path.
func loadConfig() (*config.Config, error) {
	cfgPath := resolvedConfigPath()
	cfg, err := config.LoadConfig(cfgPath)
	if err != nil {
		return nil, fmt.Errorf("loading config from %s: %w", cfgPath, err)
	}
	return cfg, nil
}

// resolvedConfigPath returns the config file path, using the global flag
// if set, otherwise the default XDG path.
func resolvedConfigPath() string {
	if globalConfigPath != "" {
		return globalConfigPath
	}
	p, err := config.DefaultConfigPath()
	if err != nil {
		// Fallback — this shouldn't happen in practice.
		return "config.toml"
	}
	return p
}
