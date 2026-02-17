package main

import (
	"context"
	"fmt"
	"os"
	"runtime"
	"strings"

	"github.com/spf13/cobra"

	"github.com/kuuji/bamgate/internal/auth"
	"github.com/kuuji/bamgate/internal/config"
)

var loginCmd = &cobra.Command{
	Use:   "login",
	Short: "Re-authenticate with GitHub and refresh device credentials",
	Long: `Re-authenticate with GitHub without running the full setup wizard.
This is useful when your refresh token has expired and bamgate can no
longer connect.

Login performs a GitHub Device Authorization Grant, then re-registers
this device (by name) with the signaling server. This refreshes the
device_id, access token, refresh token, and TURN secret while preserving
all other configuration (WireGuard keys, routes, DNS, peer selections,
Cloudflare credentials, etc.).

If the bamgate daemon is running, it will be restarted automatically.

This command must be run as root:
  sudo bamgate login`,
	RunE: runLogin,
}

func runLogin(cmd *cobra.Command, args []string) error {
	if os.Getuid() != 0 {
		return fmt.Errorf("'bamgate login' requires root (try: sudo bamgate login)")
	}

	cfgPath := resolvedConfigPath()

	cfg, err := config.LoadConfig(cfgPath)
	if err != nil {
		return fmt.Errorf("loading config: %w\n\nRun 'sudo bamgate setup' first to create a config", err)
	}

	if cfg.Network.ServerURL == "" {
		return fmt.Errorf("server_url not configured — run 'sudo bamgate setup' first")
	}
	if cfg.Device.Name == "" {
		return fmt.Errorf("device name not configured — run 'sudo bamgate setup' first")
	}

	// --- GitHub authentication ---
	fmt.Fprintf(os.Stderr, "GitHub Authentication\n")
	fmt.Fprintf(os.Stderr, "%s\n", strings.Repeat("-", 21))
	fmt.Fprintf(os.Stderr, "bamgate uses your GitHub identity for authentication.\n\n")

	ctx := context.Background()

	ghResult, err := auth.DeviceAuth(ctx, func(userCode, verificationURI string) {
		fmt.Fprintf(os.Stderr, "  1. Open %s\n", verificationURI)
		fmt.Fprintf(os.Stderr, "  2. Enter code: %s\n\n", userCode)
		fmt.Fprintf(os.Stderr, "Waiting for authorization...\n")
	})
	if err != nil {
		return fmt.Errorf("GitHub authentication failed: %w", err)
	}
	fmt.Fprintf(os.Stderr, "  Authenticated with GitHub\n\n")

	// --- Re-register device ---
	// The server reclaims the device by name, returning fresh credentials.
	baseURL := httpBaseURL(cfg.Network.ServerURL)

	fmt.Fprintf(os.Stderr, "Re-registering device %q...\n", cfg.Device.Name)

	resp, err := auth.Register(ctx, baseURL, ghResult.AccessToken, cfg.Device.Name)
	if err != nil {
		return fmt.Errorf("re-registering device: %w", err)
	}

	// --- Update only auth-related fields ---
	cfg.Network.DeviceID = resp.DeviceID
	cfg.Network.RefreshToken = resp.RefreshToken
	cfg.Network.TURNSecret = resp.TURNSecret
	cfg.Device.Address = resp.Address

	if err := config.SaveConfig(cfgPath, cfg); err != nil {
		return fmt.Errorf("saving config: %w", err)
	}

	fmt.Fprintf(os.Stderr, "  Device re-registered\n")
	fmt.Fprintf(os.Stderr, "  Device ID: %s\n", resp.DeviceID)
	fmt.Fprintf(os.Stderr, "  Tunnel address: %s\n", resp.Address)

	// --- Restart service if running ---
	if runtime.GOOS == "linux" {
		restartSystemdIfActive()
	} else if runtime.GOOS == "darwin" {
		restartLaunchdIfActive()
	}

	fmt.Fprintf(os.Stderr, "\nLogin complete.\n")

	return nil
}
