package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"runtime"
	"strings"
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

Requires root privileges for TUN device creation:
  sudo bamgate up

Use -d/--daemon to start bamgate as a system service (systemd on Linux,
launchd on macOS). The service is enabled on boot and started immediately.
Requires 'sudo bamgate setup' first.`,
	RunE: runUp,
}

const systemdServicePath = "/etc/systemd/system/bamgate.service"

func init() {
	upCmd.Flags().BoolVarP(&upDaemon, "daemon", "d", false, "start as a system service (enable + start)")
	upCmd.Flags().BoolVar(&upAcceptRoutes, "accept-routes", false, "accept subnet routes advertised by peers")
}

func runUp(cmd *cobra.Command, args []string) error {
	if upDaemon {
		return runUpDaemon()
	}

	// Migrate from monolithic config.toml to split config.toml + secrets.toml.
	if err := config.MigrateConfigSplit(resolvedConfigPath()); err != nil {
		globalLogger.Warn("config split migration failed", "error", err)
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

	a := agent.New(cfg, globalLogger, agent.WithConfigPath(resolvedConfigPath()))

	globalLogger.Info("starting bamgate", "config", resolvedConfigPath())

	if err := a.Run(ctx); err != nil {
		if ctx.Err() != nil {
			// Context was cancelled (signal received) — clean shutdown.
			globalLogger.Info("bamgate stopped")
			return nil
		}
		// Provide actionable guidance for TUN permission errors.
		if strings.Contains(err.Error(), "operation not permitted") || strings.Contains(err.Error(), "not permitted") {
			return fmt.Errorf("agent error: %w\n\nTUN device creation requires root privileges.\nRun: sudo bamgate up", err)
		}
		return fmt.Errorf("agent error: %w", err)
	}

	return nil
}

// runUpDaemon starts bamgate as a system service (enable + start).
func runUpDaemon() error {
	if os.Getuid() != 0 {
		return fmt.Errorf("daemon mode requires root (try: sudo bamgate up -d)")
	}

	switch runtime.GOOS {
	case "linux":
		return runUpDaemonLinux()
	case "darwin":
		return runUpDaemonDarwin()
	default:
		return fmt.Errorf("daemon mode is not supported on %s", runtime.GOOS)
	}
}

func runUpDaemonLinux() error {
	if _, err := os.Stat(systemdServicePath); os.IsNotExist(err) {
		return fmt.Errorf("systemd service not installed; run 'sudo bamgate setup' first and choose to install the systemd service")
	}

	fmt.Fprintln(os.Stderr, "Enabling and starting bamgate service...")

	systemctl := exec.Command("systemctl", "enable", "--now", "bamgate")
	systemctl.Stdout = os.Stderr
	systemctl.Stderr = os.Stderr
	if err := systemctl.Run(); err != nil {
		return fmt.Errorf("systemctl enable --now bamgate: %w", err)
	}

	fmt.Fprintln(os.Stderr, "bamgate is running and enabled on boot.")
	fmt.Fprintln(os.Stderr, "Use 'bamgate status' to check connection state.")
	fmt.Fprintln(os.Stderr, "Use 'sudo bamgate down' to stop and disable.")

	return nil
}

func runUpDaemonDarwin() error {
	if _, err := os.Stat(launchdPlistPath); os.IsNotExist(err) {
		return fmt.Errorf("launchd service not installed; run 'sudo bamgate setup' first and choose to install the launchd service")
	}

	fmt.Fprintln(os.Stderr, "Loading and starting bamgate service...")

	// Bootstrap (load + start) the service.
	launchctl := exec.Command("launchctl", "load", "-w", launchdPlistPath)
	launchctl.Stdout = os.Stderr
	launchctl.Stderr = os.Stderr
	if err := launchctl.Run(); err != nil {
		return fmt.Errorf("launchctl load: %w", err)
	}

	fmt.Fprintln(os.Stderr, "bamgate is running and enabled on boot.")
	fmt.Fprintln(os.Stderr, "Use 'bamgate status' to check connection state.")
	fmt.Fprintln(os.Stderr, "Use 'sudo bamgate down' to stop and disable.")

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
// if set, otherwise the default system path (/etc/bamgate/config.toml).
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
