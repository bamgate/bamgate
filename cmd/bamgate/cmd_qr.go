package main

import (
	"fmt"
	"net/url"
	"os"

	"github.com/skip2/go-qrcode"
	"github.com/spf13/cobra"

	"github.com/kuuji/bamgate/internal/config"
)

var qrCmd = &cobra.Command{
	Use:   "qr",
	Short: "Display a QR code for the server hostname",
	Long: `Displays a QR code containing the signaling server hostname.
Other devices can scan this QR code during setup instead of typing
the server address manually.

Requires an existing configuration (run 'bamgate setup' first).`,
	RunE: runQR,
}

func runQR(cmd *cobra.Command, args []string) error {
	cfgPath := resolvedConfigPath()

	cfg, err := config.LoadPublicConfig(cfgPath)
	if err != nil {
		return fmt.Errorf("loading config: %w (run 'bamgate setup' first)", err)
	}

	if cfg.Network.ServerURL == "" {
		return fmt.Errorf("server_url not configured — run 'bamgate setup' first")
	}

	// Extract hostname from the WSS signaling URL.
	host, err := extractHostname(cfg.Network.ServerURL)
	if err != nil {
		return fmt.Errorf("parsing server URL: %w", err)
	}

	// Generate QR code as ASCII art for the terminal.
	qr, err := qrcode.New(host, qrcode.Medium)
	if err != nil {
		return fmt.Errorf("generating QR code: %w", err)
	}

	fmt.Fprintln(os.Stderr, qr.ToSmallString(false))
	fmt.Fprintf(os.Stderr, "Server: %s\n", host)
	fmt.Fprintln(os.Stderr, "Scan this QR code with the bamgate Android app during setup.")

	return nil
}

// extractHostname parses a URL and returns just the host (with port if non-standard).
func extractHostname(rawURL string) (string, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return "", err
	}
	if u.Host == "" {
		return "", fmt.Errorf("no host in URL %q", rawURL)
	}
	return u.Host, nil
}
