package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

var inviteCmd = &cobra.Command{
	Use:   "invite",
	Short: "Generate an invite code for a new device",
	Long: `Generate a short-lived invite code that another device can use to join
this riftgate network. The invite code is valid for 10 minutes and can
be used once.

The new device runs 'sudo riftgate setup' and enters the invite code
when prompted. No Cloudflare account is needed on the new device.

This command reads the server URL and auth token from your config file.`,
	RunE: runInvite,
}

func runInvite(cmd *cobra.Command, args []string) error {
	cfg, err := loadConfig()
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}

	if cfg.Network.ServerURL == "" {
		return fmt.Errorf("no server URL configured — run 'sudo riftgate setup' first")
	}
	if cfg.Network.AuthToken == "" {
		return fmt.Errorf("no auth token configured — run 'sudo riftgate setup' first")
	}

	// Derive the HTTPS base URL from the WSS signaling URL.
	baseURL, err := wsToHTTPBaseURL(cfg.Network.ServerURL)
	if err != nil {
		return fmt.Errorf("parsing server URL: %w", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	inviteURL := baseURL + "/invite"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, inviteURL, nil)
	if err != nil {
		return fmt.Errorf("creating request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+cfg.Network.AuthToken)

	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("creating invite: %w", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("creating invite: HTTP %d: %s", resp.StatusCode, string(body))
	}

	var result struct {
		Code      string `json:"code"`
		ExpiresIn int    `json:"expires_in"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return fmt.Errorf("parsing invite response: %w", err)
	}

	fmt.Fprintf(os.Stderr, "\nInvite code created!\n\n")
	fmt.Fprintf(os.Stderr, "  Code:       %s\n", result.Code)
	fmt.Fprintf(os.Stderr, "  Server:     %s\n", baseURL)
	fmt.Fprintf(os.Stderr, "  Expires in: %d minutes\n", result.ExpiresIn/60)
	fmt.Fprintf(os.Stderr, "\nOn the new device, run:\n")
	fmt.Fprintf(os.Stderr, "  sudo riftgate setup\n\n")
	fmt.Fprintf(os.Stderr, "When prompted, enter the invite code and server URL above.\n")

	return nil
}

// wsToHTTPBaseURL converts a WebSocket URL (wss://host/connect) to an HTTPS
// base URL (https://host).
func wsToHTTPBaseURL(wsURL string) (string, error) {
	u, err := url.Parse(wsURL)
	if err != nil {
		return "", err
	}

	switch u.Scheme {
	case "wss":
		u.Scheme = "https"
	case "ws":
		u.Scheme = "http"
	}

	// Strip the /connect path suffix.
	u.Path = strings.TrimSuffix(u.Path, "/connect")
	u.Path = strings.TrimSuffix(u.Path, "/")

	return u.String(), nil
}
