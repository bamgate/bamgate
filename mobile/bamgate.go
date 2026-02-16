// Package mobile provides a gomobile-compatible API for the bamgate VPN
// tunnel. This package is compiled to an Android AAR via `gomobile bind`.
//
// All exported types and methods are designed to work within gomobile's type
// restrictions: only basic types (string, int, bool, []byte, error) and
// interfaces with methods using those types are supported at the boundary.
//
// Usage from Kotlin/Android:
//
//	val tunnel = Mobile.newTunnel(configTOML)
//	tunnel.setLogger(logCallback)
//	tunnel.setSocketProtector(protector)
//	tunnel.start(tunFD)  // blocks until stopped or error
//	tunnel.stop()
package mobile

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/kuuji/bamgate/internal/agent"
	"github.com/kuuji/bamgate/internal/auth"
	"github.com/kuuji/bamgate/internal/config"
)

// Logger receives log messages from the Go core. Implement this interface
// in Kotlin and pass it to Tunnel.SetLogger().
//
// Level values: 0=Debug, 1=Info, 2=Warn, 3=Error
type Logger interface {
	Log(level int, msg string)
}

// SocketProtector protects a socket file descriptor from VPN routing.
// Implement this interface in Kotlin by calling VpnService.protect(fd).
//
// Returns true if the socket was successfully protected.
type SocketProtector interface {
	Protect(fd int) bool
}

// RouteUpdateCallback is called when the Go agent discovers new subnet
// routes from a peer (e.g. the home server advertises "192.168.1.0/24").
// On Android, the VPN service should handle this by re-establishing the
// VPN interface with updated routes and restarting the tunnel.
//
// The routesJSON parameter is a JSON array of CIDR strings, e.g.
// '["192.168.1.0/24"]'. The callback must not block.
type RouteUpdateCallback interface {
	OnRoutesUpdated(routesJSON string)
}

// Tunnel represents a bamgate VPN tunnel instance. Create one with
// NewTunnel(), configure it, then call Start() to connect.
type Tunnel struct {
	cfg           *config.Config
	ag            *agent.Agent
	cancel        context.CancelFunc
	logger        Logger
	protector     SocketProtector
	routeCallback RouteUpdateCallback

	mu      sync.Mutex
	running bool
}

// NewTunnel creates a new Tunnel from a TOML configuration string.
// The TOML should contain the same structure as the bamgate config.toml file.
//
// Returns an error if the TOML is invalid or missing required fields.
func NewTunnel(configTOML string) (*Tunnel, error) {
	cfg, err := config.ParseTOML(configTOML)
	if err != nil {
		return nil, fmt.Errorf("parsing config: %w", err)
	}

	if cfg.Network.ServerURL == "" {
		return nil, fmt.Errorf("network.server_url is required")
	}
	if cfg.Network.DeviceID == "" {
		return nil, fmt.Errorf("network.device_id is required")
	}
	if cfg.Network.RefreshToken == "" {
		return nil, fmt.Errorf("network.refresh_token is required")
	}
	if cfg.Device.PrivateKey.IsZero() {
		return nil, fmt.Errorf("device.private_key is required")
	}
	if cfg.Device.Address == "" {
		return nil, fmt.Errorf("device.address is required")
	}

	return &Tunnel{cfg: cfg}, nil
}

// SetLogger sets a callback for log messages from the Go core.
// Must be called before Start().
func (t *Tunnel) SetLogger(logger Logger) {
	t.logger = logger
}

// SetSocketProtector sets a callback to protect sockets from VPN routing.
// On Android, this should call VpnService.protect(fd).
// Must be called before Start().
func (t *Tunnel) SetSocketProtector(protector SocketProtector) {
	t.protector = protector
}

// SetRouteUpdateCallback sets a callback that fires when the agent discovers
// new subnet routes from a peer. On Android, the VPN service should
// re-establish the VPN with updated routes and restart the tunnel.
// Must be called before Start().
func (t *Tunnel) SetRouteUpdateCallback(cb RouteUpdateCallback) {
	t.routeCallback = cb
}

// Start begins the VPN connection using the given TUN file descriptor.
// The TUN FD should come from Android's VpnService.Builder.establish().
//
// This method blocks until Stop() is called or a fatal error occurs.
// Call it from a background thread/coroutine.
func (t *Tunnel) Start(tunFD int) error {
	t.mu.Lock()
	if t.running {
		t.mu.Unlock()
		return fmt.Errorf("tunnel is already running")
	}
	t.running = true
	t.mu.Unlock()

	defer func() {
		t.mu.Lock()
		t.running = false
		t.mu.Unlock()
	}()

	// Build the slog handler.
	var logger *slog.Logger
	if t.logger != nil {
		logger = slog.New(&mobileLogHandler{callback: t.logger})
	} else {
		logger = slog.Default()
	}

	// Build agent options.
	opts := []agent.Option{
		agent.WithTunFD(tunFD),
	}

	if t.protector != nil {
		opts = append(opts, agent.WithSocketProtector(&protectorAdapter{t.protector}))
	}

	if t.routeCallback != nil {
		cb := t.routeCallback // capture for closure
		opts = append(opts, agent.WithRouteUpdateCallback(func(routes []string) {
			routesJSON, err := json.Marshal(routes)
			if err != nil {
				return
			}
			cb.OnRoutesUpdated(string(routesJSON))
		}))
	}

	t.ag = agent.New(t.cfg, logger, opts...)

	ctx, cancel := context.WithCancel(context.Background())
	t.cancel = cancel

	err := t.ag.Run(ctx)
	if err != nil && ctx.Err() != nil {
		// Context was cancelled — normal shutdown.
		return nil
	}
	return err
}

// Stop gracefully shuts down the tunnel. Safe to call from any thread.
func (t *Tunnel) Stop() {
	if t.cancel != nil {
		t.cancel()
	}
}

// IsRunning returns whether the tunnel is currently active.
func (t *Tunnel) IsRunning() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.running
}

// GetStatus returns a JSON-encoded status string with tunnel and peer
// information. Returns an empty JSON object "{}" if the tunnel is not running.
func (t *Tunnel) GetStatus() string {
	if t.ag == nil {
		return "{}"
	}
	status := t.ag.Status()
	data, err := json.Marshal(status)
	if err != nil {
		return "{}"
	}
	return string(data)
}

// GetTunnelAddress returns the tunnel address from the config (e.g. "10.0.0.2/24").
// The Android VpnService needs this to configure VpnService.Builder.addAddress().
func (t *Tunnel) GetTunnelAddress() string {
	return t.cfg.Device.Address
}

// GetDeviceName returns the device name from the config.
func (t *Tunnel) GetDeviceName() string {
	return t.cfg.Device.Name
}

// GetMTU returns the MTU value to use for the TUN interface (1420).
func (t *Tunnel) GetMTU() int {
	return 1420
}

// GetTunnelSubnet returns the network CIDR for the tunnel subnet, derived
// from the device address. For example, if the address is "10.0.0.2/24",
// this returns "10.0.0.0/24". The Android VPN builder uses this as the
// initial route before peer routes are discovered.
func (t *Tunnel) GetTunnelSubnet() string {
	_, ipNet, err := net.ParseCIDR(t.cfg.Device.Address)
	if err != nil {
		// Fallback: return the address as-is.
		return t.cfg.Device.Address
	}
	return ipNet.String()
}

// GetAcceptRoutes returns whether accept_routes is enabled in the config.
//
// Deprecated: Use per-peer selections via GetPeerOfferings/ConfigurePeer instead.
func (t *Tunnel) GetAcceptRoutes() bool {
	return t.cfg.Device.AcceptRoutes //nolint:staticcheck // backward compat
}

// GetForceRelay returns whether force_relay is enabled in the config.
func (t *Tunnel) GetForceRelay() bool {
	return t.cfg.Device.ForceRelay
}

// GetPeerOfferings returns a JSON-encoded array of peer offerings, including
// what each peer advertises and what the user has accepted. Returns "[]" if
// the tunnel is not running.
//
// This is used by the Android UI to show a peer management screen where
// the user can opt in to routes, DNS, and search domains from each peer.
func (t *Tunnel) GetPeerOfferings() string {
	if t.ag == nil {
		return "[]"
	}
	offerings := t.ag.PeerOfferings()
	data, err := json.Marshal(offerings)
	if err != nil {
		return "[]"
	}
	return string(data)
}

// ConfigurePeer applies per-peer selections for the given peer. The
// selectionsJSON parameter is a JSON object with optional "routes", "dns",
// and "dns_search" arrays. Example:
//
//	{"routes": ["10.96.0.0/12"], "dns": ["10.96.0.10"], "dns_search": ["svc.cluster.local"]}
//
// Selections are persisted to the in-memory config. The caller should persist
// the config via UpdateConfig after making changes.
func (t *Tunnel) ConfigurePeer(peerID string, selectionsJSON string) error {
	if t.ag == nil {
		return fmt.Errorf("tunnel is not running")
	}

	var caps struct {
		Routes    []string `json:"routes"`
		DNS       []string `json:"dns"`
		DNSSearch []string `json:"dns_search"`
	}
	if err := json.Unmarshal([]byte(selectionsJSON), &caps); err != nil {
		return fmt.Errorf("parsing selections: %w", err)
	}

	t.cfg.SetPeerSelection(peerID, config.PeerSelections{
		Routes:    caps.Routes,
		DNS:       caps.DNS,
		DNSSearch: caps.DNSSearch,
	})

	return nil
}

// GetDNSServers returns the DNS servers that should be used for the VPN
// interface, based on the user's per-peer selections. Returns a JSON array
// of IP strings. If no per-peer DNS is configured, returns the device's
// configured DNS servers. Falls back to Google DNS if nothing is configured.
func (t *Tunnel) GetDNSServers() string {
	var servers []string

	// Collect DNS servers from all per-peer selections.
	if t.cfg.Peers != nil {
		for _, sel := range t.cfg.Peers {
			servers = append(servers, sel.DNS...)
		}
	}

	// Fallback to device-level DNS config.
	if len(servers) == 0 {
		servers = t.cfg.Device.DNS
	}

	// Fallback to Google DNS.
	if len(servers) == 0 {
		servers = []string{"8.8.8.8", "8.8.4.4"}
	}

	data, _ := json.Marshal(servers)
	return string(data)
}

// GetDNSSearchDomains returns the DNS search domains for the VPN interface,
// based on the user's per-peer selections. Returns a JSON array of domain
// strings.
func (t *Tunnel) GetDNSSearchDomains() string {
	var domains []string

	// Collect search domains from all per-peer selections.
	if t.cfg.Peers != nil {
		for _, sel := range t.cfg.Peers {
			domains = append(domains, sel.DNSSearch...)
		}
	}

	// Fallback to device-level config.
	if len(domains) == 0 {
		domains = t.cfg.Device.DNSSearch
	}

	if len(domains) == 0 {
		return "[]"
	}

	data, _ := json.Marshal(domains)
	return string(data)
}

// GetServerURL returns the signaling server URL from the config.
func (t *Tunnel) GetServerURL() string {
	return t.cfg.Network.ServerURL
}

// UpdateConfig applies a new TOML configuration to the tunnel. The new config
// is parsed and validated, then the tunnel's internal config is replaced.
// Returns the re-marshaled TOML string (canonical form) for the caller to
// persist. The tunnel must be restarted for changes to take effect.
func (t *Tunnel) UpdateConfig(tomlStr string) (string, error) {
	newCfg, err := config.ParseTOML(tomlStr)
	if err != nil {
		return "", fmt.Errorf("parsing updated config: %w", err)
	}

	// Validate required fields.
	if newCfg.Network.ServerURL == "" {
		return "", fmt.Errorf("network.server_url is required")
	}
	if newCfg.Network.DeviceID == "" {
		return "", fmt.Errorf("network.device_id is required")
	}
	if newCfg.Network.RefreshToken == "" {
		return "", fmt.Errorf("network.refresh_token is required")
	}
	if newCfg.Device.PrivateKey.IsZero() {
		return "", fmt.Errorf("device.private_key is required")
	}
	if newCfg.Device.Address == "" {
		return "", fmt.Errorf("device.address is required")
	}

	t.cfg = newCfg

	// Re-marshal to canonical TOML for the caller to save.
	canonical, err := config.MarshalTOML(newCfg)
	if err != nil {
		return "", fmt.Errorf("marshaling updated config: %w", err)
	}
	return canonical, nil
}

// --- Device registration (called before tunnel is created) ---

// RegisterResult holds the result of registering a device via GitHub OAuth.
// gomobile can export structs with exported fields of basic types.
type RegisterResult struct {
	// ConfigTOML is the complete TOML config string ready to save and use.
	ConfigTOML string

	// TunnelAddress is the auto-assigned tunnel address (e.g. "10.0.0.2/24").
	TunnelAddress string

	// DeviceName is the device name used in the config.
	DeviceName string
}

// RegisterDevice registers a new device with a bamgate signaling server using
// a GitHub access token. The Android app handles the GitHub OAuth Device Auth
// flow in Kotlin and passes the resulting token here.
//
// Parameters:
//   - serverHost: the workers.dev hostname (e.g. "bamgate.ag94441.workers.dev")
//   - githubToken: transient GitHub access token from Device Auth flow
//   - deviceName: name for this device (e.g. "pixel-phone")
func RegisterDevice(serverHost, githubToken, deviceName string) (*RegisterResult, error) {
	if serverHost == "" {
		return nil, fmt.Errorf("server host is required")
	}
	if githubToken == "" {
		return nil, fmt.Errorf("GitHub token is required")
	}
	if deviceName == "" {
		return nil, fmt.Errorf("device name is required")
	}

	serverURL := "https://" + serverHost

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	resp, err := auth.Register(ctx, serverURL, githubToken, deviceName)
	if err != nil {
		return nil, fmt.Errorf("registering device: %w", err)
	}

	// Normalize the server URL to WSS for signaling.
	wsURL, err := normalizeServerURL(resp.ServerURL + "/connect")
	if err != nil {
		return nil, fmt.Errorf("normalizing server URL: %w", err)
	}

	// Generate a WireGuard key pair for this device.
	privateKey, err := config.GeneratePrivateKey()
	if err != nil {
		return nil, fmt.Errorf("generating private key: %w", err)
	}

	// Build the config.
	cfg := config.DefaultConfig()
	cfg.Network.ServerURL = wsURL
	cfg.Network.TURNSecret = resp.TURNSecret
	cfg.Network.DeviceID = resp.DeviceID
	cfg.Network.RefreshToken = resp.RefreshToken
	cfg.Device.Name = deviceName
	cfg.Device.PrivateKey = privateKey
	cfg.Device.Address = resp.Address
	cfg.Device.AcceptRoutes = true //nolint:staticcheck // legacy default for backward compat

	// Serialize to TOML.
	tomlStr, err := config.MarshalTOML(cfg)
	if err != nil {
		return nil, fmt.Errorf("serializing config: %w", err)
	}

	return &RegisterResult{
		ConfigTOML:    tomlStr,
		TunnelAddress: resp.Address,
		DeviceName:    deviceName,
	}, nil
}

// --- Internal helpers ---

// protectorAdapter bridges the mobile.SocketProtector interface to the
// agent.SocketProtector interface. Both have the same method signature
// but are different types (mobile package vs agent package).
type protectorAdapter struct {
	p SocketProtector
}

func (a *protectorAdapter) Protect(fd int) bool {
	return a.p.Protect(fd)
}

// mobileLogHandler adapts Go's slog to the mobile Logger callback.
type mobileLogHandler struct {
	callback Logger
	attrs    []slog.Attr
	groups   []string
}

func (h *mobileLogHandler) Enabled(_ context.Context, _ slog.Level) bool {
	return true
}

func (h *mobileLogHandler) Handle(_ context.Context, r slog.Record) error {
	// Map slog levels to simple int: Debug=0, Info=1, Warn=2, Error=3
	var level int
	switch {
	case r.Level < slog.LevelInfo:
		level = 0
	case r.Level < slog.LevelWarn:
		level = 1
	case r.Level < slog.LevelError:
		level = 2
	default:
		level = 3
	}

	// Build a simple log message with key=value pairs.
	msg := r.Message
	r.Attrs(func(a slog.Attr) bool {
		msg += " " + a.Key + "=" + a.Value.String()
		return true
	})
	for _, a := range h.attrs {
		msg += " " + a.Key + "=" + a.Value.String()
	}

	h.callback.Log(level, msg)
	return nil
}

func (h *mobileLogHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &mobileLogHandler{
		callback: h.callback,
		attrs:    append(h.attrs, attrs...),
		groups:   h.groups,
	}
}

func (h *mobileLogHandler) WithGroup(name string) slog.Handler {
	return &mobileLogHandler{
		callback: h.callback,
		attrs:    h.attrs,
		groups:   append(h.groups, name),
	}
}

// normalizeServerURL ensures the URL has a wss:// scheme for signaling.
func normalizeServerURL(rawURL string) (string, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return "", err
	}

	switch u.Scheme {
	case "wss", "ws":
		// Already a WebSocket URL.
	case "https":
		u.Scheme = "wss"
	case "http":
		u.Scheme = "ws"
	case "":
		u.Scheme = "wss"
	default:
		return "", fmt.Errorf("unsupported URL scheme: %s", u.Scheme)
	}

	// Ensure the path ends with /connect.
	if !strings.HasSuffix(u.Path, "/connect") {
		u.Path = strings.TrimSuffix(u.Path, "/") + "/connect"
	}

	return u.String(), nil
}
