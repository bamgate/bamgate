package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"runtime"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/kuuji/bamgate/internal/agent"
	"github.com/kuuji/bamgate/internal/config"
)

var (
	upDaemon       bool
	upAcceptRoutes bool
)

var upCmd = &cobra.Command{
	Use:   "up",
	Short: "Connect to the network",
	Long: `Start the bamgate agent: create a WireGuard tunnel, connect to the
signaling server, and bridge traffic over WebRTC data channels.

Requires CAP_NET_ADMIN to create the TUN device and configure the
network interface. You can grant this in one of three ways:

  1. Run 'sudo bamgate install' once to set capabilities on the binary
     (then 'bamgate up' works without sudo)
  2. Run as a systemd service: sudo bamgate up -d
  3. Run directly with sudo: sudo bamgate up

Use -d/--daemon to start bamgate as a systemd service (enables on boot
and starts immediately). Requires a prior 'sudo bamgate install --systemd'.`,
	RunE: runUp,
}

const systemdServicePath = "/etc/systemd/system/bamgate.service"

func init() {
	upCmd.Flags().BoolVarP(&upDaemon, "daemon", "d", false, "start as a systemd service (enable + start)")
	upCmd.Flags().BoolVar(&upAcceptRoutes, "accept-routes", false, "accept subnet routes advertised by peers")
}

func runUp(cmd *cobra.Command, args []string) error {
	if upDaemon {
		return runUpDaemon()
	}

	cfg, err := loadConfig()
	if err != nil {
		return err
	}

	// CLI flag overrides config file.
	if upAcceptRoutes {
		cfg.Device.AcceptRoutes = true
	}

	if err := validateConfig(cfg); err != nil {
		return fmt.Errorf("invalid config: %w", err)
	}

	// Set up context with signal handling.
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	a := agent.New(cfg, globalLogger)

	globalLogger.Info("starting bamgate", "config", resolvedConfigPath())

	if err := a.Run(ctx); err != nil {
		if ctx.Err() != nil {
			// Context was cancelled (signal received) — clean shutdown.
			globalLogger.Info("bamgate stopped")
			return nil
		}
		return fmt.Errorf("agent error: %w", err)
	}

	return nil
}

// runUpDaemon starts bamgate as a systemd service (enable + start).
func runUpDaemon() error {
	if runtime.GOOS != "linux" {
		return fmt.Errorf("daemon mode (-d) requires systemd and is only supported on Linux; launchd support is not yet implemented\n\nRun 'sudo bamgate up' (without -d) to start in the foreground")
	}
	if _, err := os.Stat(systemdServicePath); os.IsNotExist(err) {
		return fmt.Errorf("systemd service not installed; run 'sudo bamgate install --systemd' first")
	}

	fmt.Fprintln(os.Stderr, "Enabling and starting bamgate service...")

	systemctl := exec.Command("sudo", "systemctl", "enable", "--now", "bamgate")
	systemctl.Stdout = os.Stderr
	systemctl.Stderr = os.Stderr
	if err := systemctl.Run(); err != nil {
		return fmt.Errorf("systemctl enable --now bamgate: %w", err)
	}

	fmt.Fprintln(os.Stderr, "bamgate is running and enabled on boot.")
	fmt.Fprintln(os.Stderr, "Use 'bamgate status' to check connection state.")
	fmt.Fprintln(os.Stderr, "Use 'bamgate down' to stop and disable.")

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
