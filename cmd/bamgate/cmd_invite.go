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

	qrcode "github.com/skip2/go-qrcode"
	"github.com/spf13/cobra"
)

var inviteCmd = &cobra.Command{
	Use:   "invite",
	Short: "Generate an invite code for a new device",
	Long: `Generate a short-lived invite code that another device can use to join
this bamgate network. The invite code is valid for 10 minutes and can
be used once.

The new device runs 'sudo bamgate setup' and enters the invite code
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
		return fmt.Errorf("no server URL configured — run 'sudo bamgate setup' first")
	}
	if cfg.Network.AuthToken == "" {
		return fmt.Errorf("no auth token configured — run 'sudo bamgate setup' first")
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

	// Extract worker name and subdomain from the server URL for display.
	workerName, subdomain, _ := parseWorkersDevURL(cfg.Network.ServerURL)

	fmt.Fprintf(os.Stderr, "\nInvite code created!\n\n")
	fmt.Fprintf(os.Stderr, "  Code:        %s\n", result.Code)
	if workerName != "" && subdomain != "" {
		fmt.Fprintf(os.Stderr, "  Worker name: %s\n", workerName)
		fmt.Fprintf(os.Stderr, "  Subdomain:   %s\n", subdomain)
	} else {
		fmt.Fprintf(os.Stderr, "  Server:      %s\n", baseURL)
	}
	fmt.Fprintf(os.Stderr, "  Expires in:  %d minutes\n", result.ExpiresIn/60)

	// Build the invite URL for QR code scanning (mobile app).
	u, _ := url.Parse(baseURL)
	inviteDeepLink := fmt.Sprintf("bamgate://invite?server=%s&code=%s", u.Host, result.Code)

	// Display QR code in terminal for the mobile app.
	qr, err := qrcode.New(inviteDeepLink, qrcode.Medium)
	if err == nil {
		fmt.Fprintf(os.Stderr, "\nScan with the bamgate app:\n\n")
		fmt.Fprint(os.Stderr, qr.ToSmallString(false))
	}

	fmt.Fprintf(os.Stderr, "\nFor CLI devices, run:\n")
	fmt.Fprintf(os.Stderr, "  sudo bamgate setup\n\n")
	fmt.Fprintf(os.Stderr, "When prompted, enter the invite code")
	if workerName != "" && subdomain != "" {
		fmt.Fprintf(os.Stderr, ", worker name, and subdomain above.\n")
	} else {
		fmt.Fprintf(os.Stderr, " and server URL above.\n")
	}

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

// parseWorkersDevURL extracts the worker name and subdomain from a workers.dev URL.
// e.g. "wss://bamgate.ag94441.workers.dev/connect" → ("bamgate", "ag94441", nil)
func parseWorkersDevURL(rawURL string) (workerName, subdomain string, err error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return "", "", err
	}

	host := u.Hostname()
	if !strings.HasSuffix(host, ".workers.dev") {
		return "", "", fmt.Errorf("not a workers.dev URL: %s", host)
	}

	// Strip ".workers.dev" and split on ".".
	prefix := strings.TrimSuffix(host, ".workers.dev")
	parts := strings.SplitN(prefix, ".", 2)
	if len(parts) != 2 {
		return "", "", fmt.Errorf("unexpected workers.dev hostname format: %s", host)
	}

	return parts[0], parts[1], nil
}
