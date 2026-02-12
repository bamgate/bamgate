package main

import (
	"bufio"
	"fmt"
	"net/url"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/kuuji/riftgate/internal/config"
)

var initCmd = &cobra.Command{
	Use:   "init",
	Short: "Generate a new configuration file",
	Long: `Interactive setup wizard: generates a WireGuard key pair and creates
a config file with the required network and device settings.

If a config file already exists at the target path, you will be
prompted before overwriting it.`,
	RunE: runInit,
}

func runInit(cmd *cobra.Command, args []string) error {
	cfgPath := resolvedConfigPath()
	scanner := bufio.NewScanner(os.Stdin)

	// Check for existing config.
	if _, err := os.Stat(cfgPath); err == nil {
		fmt.Fprintf(os.Stderr, "Config file already exists: %s\n", cfgPath)
		if !promptYesNo(scanner, "Overwrite?", false) {
			fmt.Fprintln(os.Stderr, "Aborted.")
			return nil
		}
	}

	cfg := config.DefaultConfig()

	// Device name — default to hostname.
	hostname, _ := os.Hostname()
	cfg.Device.Name = promptString(scanner, "Device name", hostname)

	// Server URL.
	rawURL := promptString(scanner, "Signaling server URL (e.g. wss://riftgate.example.workers.dev/connect)", "")
	if rawURL == "" {
		return fmt.Errorf("server URL is required")
	}
	serverURL, err := normalizeServerURL(rawURL)
	if err != nil {
		return fmt.Errorf("invalid server URL: %w", err)
	}
	cfg.Network.ServerURL = serverURL

	// Auth token.
	cfg.Network.AuthToken = promptString(scanner, "Auth token", "")

	// Tunnel address.
	cfg.Device.Address = promptString(scanner, "Tunnel address (CIDR, e.g. 10.0.0.1/24)", "")
	if cfg.Device.Address == "" {
		return fmt.Errorf("tunnel address is required")
	}

	// Generate WireGuard key pair.
	privKey, err := config.GeneratePrivateKey()
	if err != nil {
		return fmt.Errorf("generating WireGuard key: %w", err)
	}
	cfg.Device.PrivateKey = privKey
	pubKey := config.PublicKey(privKey)

	// Network name — optional, default to "default".
	cfg.Network.Name = promptString(scanner, "Network name", "default")

	// Save config.
	if err := config.SaveConfig(cfgPath, cfg); err != nil {
		return fmt.Errorf("saving config: %w", err)
	}

	fmt.Fprintf(os.Stderr, "\nConfig written to: %s\n", cfgPath)
	fmt.Fprintf(os.Stderr, "Public key:        %s\n", pubKey.String())
	fmt.Fprintf(os.Stderr, "\nShare the public key with your other devices.\n")
	fmt.Fprintf(os.Stderr, "Run 'sudo riftgate up' to connect.\n")

	return nil
}

// promptString prompts the user for a string value. If the user enters
// nothing and a default is provided, the default is returned.
func promptString(scanner *bufio.Scanner, prompt, defaultVal string) string {
	if defaultVal != "" {
		fmt.Fprintf(os.Stderr, "%s [%s]: ", prompt, defaultVal)
	} else {
		fmt.Fprintf(os.Stderr, "%s: ", prompt)
	}

	if !scanner.Scan() {
		return defaultVal
	}

	val := strings.TrimSpace(scanner.Text())
	if val == "" {
		return defaultVal
	}
	return val
}

// promptYesNo prompts the user for a yes/no answer. Returns the default
// if the user enters nothing.
func promptYesNo(scanner *bufio.Scanner, prompt string, defaultYes bool) bool {
	suffix := " [y/N]: "
	if defaultYes {
		suffix = " [Y/n]: "
	}
	fmt.Fprintf(os.Stderr, "%s%s", prompt, suffix)

	if !scanner.Scan() {
		return defaultYes
	}

	val := strings.TrimSpace(strings.ToLower(scanner.Text()))
	switch val {
	case "y", "yes":
		return true
	case "n", "no":
		return false
	default:
		return defaultYes
	}
}

// normalizeServerURL ensures the server URL has a valid WebSocket scheme.
// If no scheme is provided, wss:// is prepended. http(s) schemes are
// converted to ws(s) for clarity (coder/websocket accepts both).
func normalizeServerURL(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", fmt.Errorf("empty URL")
	}

	// If there's no scheme at all, prepend wss://.
	if !strings.Contains(raw, "://") {
		raw = "wss://" + raw
	}

	u, err := url.Parse(raw)
	if err != nil {
		return "", fmt.Errorf("parsing URL: %w", err)
	}

	switch u.Scheme {
	case "wss", "ws":
		// Already correct.
	case "https":
		u.Scheme = "wss"
	case "http":
		u.Scheme = "ws"
	default:
		return "", fmt.Errorf("unsupported scheme %q (expected ws, wss, http, or https)", u.Scheme)
	}

	return u.String(), nil
}
