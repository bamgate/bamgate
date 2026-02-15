package main

import (
	"bufio"
	"fmt"
	"net/url"
	"os"
	"strings"
)

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
