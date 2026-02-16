// Package control provides a Unix socket HTTP server for querying the
// running bamgate agent. The agent starts the server as part of its
// lifecycle, and the "bamgate status" CLI command connects to it.
package control

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// ResolveSocketPath returns the socket path for the control server.
//
// Since bamgate runs as root, the socket is placed in the system runtime
// directory. On Linux, systemd's RuntimeDirectory= creates /run/bamgate
// automatically. On macOS, /var/run/bamgate is used.
//
// Falls back to /tmp/bamgate if the system directory doesn't exist yet
// (e.g. running outside of a service).
func ResolveSocketPath() string {
	if runtime.GOOS == "darwin" {
		if info, err := os.Stat("/var/run/bamgate"); err == nil && info.IsDir() {
			return "/var/run/bamgate/control.sock"
		}
		return "/tmp/bamgate/control.sock"
	}

	// Linux: prefer the systemd-managed directory.
	if info, err := os.Stat("/run/bamgate"); err == nil && info.IsDir() {
		return "/run/bamgate/control.sock"
	}

	return "/tmp/bamgate/control.sock"
}

// Status represents the overall agent status returned by the /status endpoint.
type Status struct {
	Device        string       `json:"device"`
	Address       string       `json:"address"`
	Routes        []string     `json:"routes,omitempty"`
	ServerURL     string       `json:"server_url"`
	UptimeSeconds float64      `json:"uptime_seconds"`
	Peers         []PeerStatus `json:"peers"`
}

// PeerStatus represents the status of a single connected peer.
type PeerStatus struct {
	ID             string            `json:"id"`
	Address        string            `json:"address"`
	State          string            `json:"state"`
	ICEType        string            `json:"ice_type"`
	Routes         []string          `json:"routes,omitempty"`
	Metadata       map[string]string `json:"metadata,omitempty"`
	ConnectedSince time.Time         `json:"connected_since,omitempty"`
}

// StatusProvider is a function that returns the current agent status.
type StatusProvider func() Status

// PeerOfferings describes what a connected peer advertises and what the
// local user has currently accepted. Used by the /peers/offerings endpoint
// and the `bamgate peers` CLI command.
type PeerOfferings struct {
	// PeerID is the peer's name/identifier.
	PeerID string `json:"peer_id"`

	// Address is the peer's WireGuard tunnel address.
	Address string `json:"address,omitempty"`

	// State is the current ICE connection state.
	State string `json:"state"`

	// Advertised is what the peer offers.
	Advertised PeerCapabilities `json:"advertised"`

	// Accepted is what the local user has chosen to accept (from config).
	Accepted PeerCapabilities `json:"accepted"`
}

// PeerCapabilities holds the routes, DNS servers, and search domains
// that a peer either advertises or that a user has accepted.
type PeerCapabilities struct {
	Routes    []string `json:"routes,omitempty"`
	DNS       []string `json:"dns,omitempty"`
	DNSSearch []string `json:"dns_search,omitempty"`
}

// OfferingsProvider is a function that returns peer offerings with current
// selection state.
type OfferingsProvider func() []PeerOfferings

// ConfigureRequest is the JSON body for POST /peers/configure.
type ConfigureRequest struct {
	// PeerID is the peer to configure.
	PeerID string `json:"peer_id"`

	// Selections is what the user wants to accept from this peer.
	Selections PeerCapabilities `json:"selections"`
}

// ConfigureFunc applies per-peer selections and persists them to config.
type ConfigureFunc func(req ConfigureRequest) error

// Server is an HTTP server that listens on a Unix domain socket and
// serves the agent's status as JSON.
type Server struct {
	socketPath  string
	provider    StatusProvider
	offerings   OfferingsProvider
	configureFn ConfigureFunc
	log         *slog.Logger
	listener    net.Listener
	httpServer  *http.Server
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

// SetOfferingsProvider sets the function used to serve GET /peers/offerings.
func (s *Server) SetOfferingsProvider(fn OfferingsProvider) {
	s.offerings = fn
}

// SetConfigureFunc sets the function used to handle POST /peers/configure.
func (s *Server) SetConfigureFunc(fn ConfigureFunc) {
	s.configureFn = fn
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
	mux.HandleFunc("GET /peers/offerings", s.handlePeerOfferings)
	mux.HandleFunc("POST /peers/configure", s.handlePeerConfigure)

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

// handlePeerOfferings responds with peer capability offerings and selections.
func (s *Server) handlePeerOfferings(w http.ResponseWriter, r *http.Request) {
	if s.offerings == nil {
		http.Error(w, "offerings not available", http.StatusNotImplemented)
		return
	}

	offerings := s.offerings()

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(offerings); err != nil {
		s.log.Error("encoding offerings response", "error", err)
	}
}

// handlePeerConfigure applies per-peer selections.
func (s *Server) handlePeerConfigure(w http.ResponseWriter, r *http.Request) {
	if s.configureFn == nil {
		http.Error(w, "configure not available", http.StatusNotImplemented)
		return
	}

	var req ConfigureRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, fmt.Sprintf("invalid request: %s", err), http.StatusBadRequest)
		return
	}

	if req.PeerID == "" {
		http.Error(w, "peer_id is required", http.StatusBadRequest)
		return
	}

	if err := s.configureFn(req); err != nil {
		http.Error(w, fmt.Sprintf("applying configuration: %s", err), http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(`{"ok":true}`))
}

// FetchStatus connects to a running control server and returns the status.
// This is used by the "bamgate status" CLI command.
func FetchStatus(socketPath string) (*Status, error) {
	client := &http.Client{
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				return net.Dial("unix", socketPath)
			},
		},
		Timeout: 5 * time.Second,
	}

	resp, err := client.Get("http://bamgate/status")
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

// FetchOfferings connects to a running control server and returns peer
// offerings. This is used by the "bamgate peers" CLI command.
func FetchOfferings(socketPath string) ([]PeerOfferings, error) {
	client := &http.Client{
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				return net.Dial("unix", socketPath)
			},
		},
		Timeout: 5 * time.Second,
	}

	resp, err := client.Get("http://bamgate/peers/offerings")
	if err != nil {
		return nil, fmt.Errorf("connecting to control socket: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected status code: %d", resp.StatusCode)
	}

	var offerings []PeerOfferings
	if err := json.NewDecoder(resp.Body).Decode(&offerings); err != nil {
		return nil, fmt.Errorf("decoding offerings response: %w", err)
	}

	return offerings, nil
}

// SendConfigure sends a peer configuration request to the running agent.
// This is used by the "bamgate peers configure" CLI command.
func SendConfigure(socketPath string, req ConfigureRequest) error {
	client := &http.Client{
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				return net.Dial("unix", socketPath)
			},
		},
		Timeout: 5 * time.Second,
	}

	body, err := json.Marshal(req)
	if err != nil {
		return fmt.Errorf("encoding request: %w", err)
	}

	resp, err := client.Post("http://bamgate/peers/configure", "application/json",
		bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("connecting to control socket: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("configure failed (status %d): %s", resp.StatusCode, strings.TrimSpace(string(respBody)))
	}

	return nil
}
