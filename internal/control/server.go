// Package control provides a Unix socket HTTP server for querying the
// running riftgate agent. The agent starts the server as part of its
// lifecycle, and the "riftgate status" CLI command connects to it.
package control

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"time"
)

// ResolveSocketPath returns the best socket path for the current environment.
//
// On Linux, it checks in order:
//  1. /run/riftgate/ — if writable (systemd RuntimeDirectory= or root)
//  2. $XDG_RUNTIME_DIR/riftgate/ — user-writable runtime directory
//  3. /tmp/riftgate/ — fallback
//
// On macOS, it checks in order:
//  1. /var/run/riftgate/ — system runtime directory (requires root)
//  2. /tmp/riftgate/ — fallback
func ResolveSocketPath() string {
	if runtime.GOOS == "darwin" {
		// macOS: /var/run is the standard location for runtime data.
		if info, err := os.Stat("/var/run/riftgate"); err == nil && info.IsDir() {
			return "/var/run/riftgate/control.sock"
		}
		return "/tmp/riftgate/control.sock"
	}

	// Linux: check if the systemd-managed directory exists and is writable.
	if info, err := os.Stat("/run/riftgate"); err == nil && info.IsDir() {
		return "/run/riftgate/control.sock"
	}

	// Fall back to XDG_RUNTIME_DIR.
	if xdgDir := os.Getenv("XDG_RUNTIME_DIR"); xdgDir != "" {
		return filepath.Join(xdgDir, "riftgate", "control.sock")
	}

	// Last resort.
	return "/tmp/riftgate/control.sock"
}

// Status represents the overall agent status returned by the /status endpoint.
type Status struct {
	Device        string       `json:"device"`
	Address       string       `json:"address"`
	ServerURL     string       `json:"server_url"`
	UptimeSeconds float64      `json:"uptime_seconds"`
	Peers         []PeerStatus `json:"peers"`
}

// PeerStatus represents the status of a single connected peer.
type PeerStatus struct {
	ID             string    `json:"id"`
	Address        string    `json:"address"`
	State          string    `json:"state"`
	ICEType        string    `json:"ice_type"`
	Routes         []string  `json:"routes,omitempty"`
	ConnectedSince time.Time `json:"connected_since,omitempty"`
}

// StatusProvider is a function that returns the current agent status.
type StatusProvider func() Status

// Server is an HTTP server that listens on a Unix domain socket and
// serves the agent's status as JSON.
type Server struct {
	socketPath string
	provider   StatusProvider
	log        *slog.Logger
	listener   net.Listener
	httpServer *http.Server
}

// NewServer creates a new control server.
func NewServer(socketPath string, provider StatusProvider, logger *slog.Logger) *Server {
	if logger == nil {
		logger = slog.Default()
	}
	return &Server{
		socketPath: socketPath,
		provider:   provider,
		log:        logger.With("component", "control"),
	}
}

// Start begins listening on the Unix socket and serving HTTP requests.
// It returns immediately; the server runs in the background.
func (s *Server) Start() error {
	// Ensure the socket directory exists.
	dir := filepath.Dir(s.socketPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("creating socket directory %s: %w", dir, err)
	}

	// Remove stale socket file from a previous run.
	if err := os.Remove(s.socketPath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("removing stale socket %s: %w", s.socketPath, err)
	}

	ln, err := net.Listen("unix", s.socketPath)
	if err != nil {
		return fmt.Errorf("listening on %s: %w", s.socketPath, err)
	}
	s.listener = ln

	// Make the socket world-readable so non-root users can query status.
	if err := os.Chmod(s.socketPath, 0666); err != nil {
		s.log.Warn("setting socket permissions", "error", err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /status", s.handleStatus)

	s.httpServer = &http.Server{Handler: mux}

	go func() {
		if err := s.httpServer.Serve(ln); err != nil && err != http.ErrServerClosed {
			s.log.Error("control server error", "error", err)
		}
	}()

	s.log.Info("control server started", "socket", s.socketPath)
	return nil
}

// Stop gracefully shuts down the control server and removes the socket file.
func (s *Server) Stop() error {
	if s.httpServer != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		if err := s.httpServer.Shutdown(ctx); err != nil {
			s.log.Warn("control server shutdown", "error", err)
		}
	}

	// Clean up the socket file.
	if err := os.Remove(s.socketPath); err != nil && !os.IsNotExist(err) {
		s.log.Warn("removing socket file", "error", err)
	}

	s.log.Info("control server stopped")
	return nil
}

// handleStatus responds with the current agent status as JSON.
func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	status := s.provider()

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(status); err != nil {
		s.log.Error("encoding status response", "error", err)
	}
}

// FetchStatus connects to a running control server and returns the status.
// This is used by the "riftgate status" CLI command.
func FetchStatus(socketPath string) (*Status, error) {
	client := &http.Client{
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				return net.Dial("unix", socketPath)
			},
		},
		Timeout: 5 * time.Second,
	}

	resp, err := client.Get("http://riftgate/status")
	if err != nil {
		return nil, fmt.Errorf("connecting to control socket: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected status code: %d", resp.StatusCode)
	}

	var status Status
	if err := json.NewDecoder(resp.Body).Decode(&status); err != nil {
		return nil, fmt.Errorf("decoding status response: %w", err)
	}

	return &status, nil
}
