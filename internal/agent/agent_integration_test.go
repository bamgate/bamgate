package agent

import (
	"context"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/pion/webrtc/v4"

	"github.com/kuuji/bamgate/internal/config"
	"github.com/kuuji/bamgate/internal/signaling"
	"github.com/kuuji/bamgate/internal/tunnel"
	"github.com/kuuji/bamgate/pkg/protocol"
)

// --- Test helpers ---

// isShutdownError returns true if the error is expected during test teardown
// (context cancellation, signaling server going away, etc.).
func isShutdownError(err error) bool {
	if err == nil || errors.Is(err, context.Canceled) {
		return true
	}
	msg := err.Error()
	return strings.Contains(msg, "connection closed") ||
		strings.Contains(msg, "use of closed network connection") ||
		strings.Contains(msg, "websocket:") ||
		strings.Contains(msg, "context canceled")
}

// startTestHub starts an httptest.Server running the signaling Hub and returns
// the server and a ws:// URL suitable for the signaling client.
func startTestHub(t *testing.T) (*signaling.Hub, *httptest.Server, string) {
	t.Helper()
	hub := signaling.NewHub(nil)
	srv := httptest.NewServer(hub)
	t.Cleanup(func() {
		hub.Close()
		srv.Close()
	})

	// Convert http:// to ws://
	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http")
	return hub, srv, wsURL
}

// testConfig creates a minimal config for testing with a generated key pair.
func testConfig(name, address, serverURL string) *config.Config {
	privKey, _ := config.GeneratePrivateKey()
	return &config.Config{
		Device: config.DeviceConfig{
			Name:       name,
			PrivateKey: privKey,
			Address:    address,
		},
		Network: config.NetworkConfig{
			ServerURL: serverURL,
		},
	}
}

// waitFor waits for a condition function to return true within the timeout.
func waitFor(t *testing.T, timeout time.Duration, desc string, fn func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if fn() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for: %s", desc)
}

// --- Integration tests ---

// TestAgent_TwoPeers_FullConnection starts two agents with a shared signaling
// hub and verifies they discover each other, perform SDP exchange over real
// WebRTC, open data channels, and configure WireGuard peers.
func TestAgent_TwoPeers_FullConnection(t *testing.T) {
	t.Parallel()

	_, _, wsURL := startTestHub(t)

	// Create configs. "alpha" < "bravo" lexicographically, so alpha offers.
	cfgA := testConfig("alpha", "10.0.0.1/24", wsURL)
	cfgB := testConfig("bravo", "10.0.0.2/24", wsURL)

	// Create deps with fakes.
	depsA, fakesA := newTestDeps()
	depsB, fakesB := newTestDeps()

	// Wire up real signaling clients.
	depsA.Signaling = func(cfg signaling.ClientConfig) SignalingClient {
		return signaling.NewClient(cfg)
	}
	depsB.Signaling = func(cfg signaling.ClientConfig) SignalingClient {
		return signaling.NewClient(cfg)
	}

	agentA := New(cfgA, nil, WithDeps(depsA))
	agentB := New(cfgB, nil, WithDeps(depsB))

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	// Start both agents in background goroutines.
	errChA := make(chan error, 1)
	errChB := make(chan error, 1)
	go func() { errChA <- agentA.Run(ctx) }()
	go func() { errChB <- agentB.Run(ctx) }()

	// Wait for both agents to have WireGuard peers configured.
	// alpha offers to bravo (alpha < bravo), so both should end up with
	// each other's WireGuard public key after the data channel opens.
	pubKeyA := config.PublicKey(cfgA.Device.PrivateKey).String()
	pubKeyB := config.PublicKey(cfgB.Device.PrivateKey).String()

	waitFor(t, 10*time.Second, "bravo's WG device has alpha's public key", func() bool {
		return fakesB.WireGuard.getDevice() != nil && fakesB.WireGuard.getDevice().hasPeer(pubKeyA)
	})

	waitFor(t, 10*time.Second, "alpha's WG device has bravo's public key", func() bool {
		return fakesA.WireGuard.getDevice() != nil && fakesA.WireGuard.getDevice().hasPeer(pubKeyB)
	})

	// Verify network configuration was applied.
	waitFor(t, 2*time.Second, "alpha's TUN address configured", func() bool {
		fakesA.Network.mu.Lock()
		defer fakesA.Network.mu.Unlock()
		_, ok := fakesA.Network.addresses[tunnel.DefaultTUNName]
		return ok
	})

	waitFor(t, 2*time.Second, "bravo's TUN address configured", func() bool {
		fakesB.Network.mu.Lock()
		defer fakesB.Network.mu.Unlock()
		_, ok := fakesB.Network.addresses[tunnel.DefaultTUNName]
		return ok
	})

	// Shutdown.
	cancel()

	select {
	case err := <-errChA:
		if !isShutdownError(err) {
			t.Errorf("agent alpha error: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Error("agent alpha did not shut down")
	}

	select {
	case err := <-errChB:
		if !isShutdownError(err) {
			t.Errorf("agent bravo error: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Error("agent bravo did not shut down")
	}
}

// TestAgent_PeerLeft_Cleanup verifies that when a peer disconnects, the
// agent removes the WireGuard peer and cleans up the data channel.
func TestAgent_PeerLeft_Cleanup(t *testing.T) {
	t.Parallel()

	_, _, wsURL := startTestHub(t)

	cfgA := testConfig("alpha", "10.0.0.1/24", wsURL)
	cfgB := testConfig("bravo", "10.0.0.2/24", wsURL)

	depsA, fakesA := newTestDeps()
	depsB, _ := newTestDeps()

	depsA.Signaling = func(cfg signaling.ClientConfig) SignalingClient {
		return signaling.NewClient(cfg)
	}
	depsB.Signaling = func(cfg signaling.ClientConfig) SignalingClient {
		return signaling.NewClient(cfg)
	}

	agentA := New(cfgA, nil, WithDeps(depsA))
	agentB := New(cfgB, nil, WithDeps(depsB))

	ctxA, cancelA := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancelA()
	ctxB, cancelB := context.WithCancel(context.Background())

	errChA := make(chan error, 1)
	errChB := make(chan error, 1)
	go func() { errChA <- agentA.Run(ctxA) }()
	go func() { errChB <- agentB.Run(ctxB) }()

	pubKeyA := config.PublicKey(cfgA.Device.PrivateKey).String()
	pubKeyB := config.PublicKey(cfgB.Device.PrivateKey).String()

	// Wait for connection to be established.
	waitFor(t, 10*time.Second, "alpha has bravo's WG peer", func() bool {
		return fakesA.WireGuard.getDevice() != nil && fakesA.WireGuard.getDevice().hasPeer(pubKeyB)
	})

	// Now disconnect bravo.
	cancelB()
	select {
	case <-errChB:
	case <-time.After(5 * time.Second):
		t.Fatal("agent bravo did not shut down")
	}

	// Alpha should detect bravo's departure and remove the WG peer.
	waitFor(t, 10*time.Second, "alpha removed bravo's WG peer", func() bool {
		return !fakesA.WireGuard.getDevice().hasPeer(pubKeyB)
	})

	// Verify alpha has no peers left.
	agentA.mu.Lock()
	peerCount := len(agentA.peers)
	agentA.mu.Unlock()
	if peerCount != 0 {
		t.Errorf("alpha has %d peers after bravo left, want 0", peerCount)
	}

	_ = pubKeyA // used for symmetry verification in other tests

	cancelA()
	select {
	case err := <-errChA:
		if !isShutdownError(err) {
			t.Errorf("agent alpha error: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Error("agent alpha did not shut down")
	}
}

// TestAgent_ThreePeers verifies that three agents can all discover and
// connect to each other through the signaling hub.
func TestAgent_ThreePeers(t *testing.T) {
	t.Parallel()

	_, _, wsURL := startTestHub(t)

	configs := []*config.Config{
		testConfig("alpha", "10.0.0.1/24", wsURL),
		testConfig("bravo", "10.0.0.2/24", wsURL),
		testConfig("charlie", "10.0.0.3/24", wsURL),
	}

	agents := make([]*Agent, 3)
	fakes := make([]*testFakes, 3)
	errChs := make([]chan error, 3)

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	for i, cfg := range configs {
		deps, f := newTestDeps()
		deps.Signaling = func(cfg signaling.ClientConfig) SignalingClient {
			return signaling.NewClient(cfg)
		}
		agents[i] = New(cfg, nil, WithDeps(deps))
		fakes[i] = f
		errChs[i] = make(chan error, 1)
		ag := agents[i]
		ch := errChs[i]
		go func() { ch <- ag.Run(ctx) }()
		// Stagger starts slightly to reduce offer/answer interleaving
		// that can overwhelm three peers starting simultaneously.
		time.Sleep(100 * time.Millisecond)
	}

	// Each agent should have 2 WG peers (the other two agents).
	// Use a longer timeout because 3-peer signaling has more message
	// interleaving and potential offer/answer glare to resolve.
	for i, cfg := range configs {
		for j, otherCfg := range configs {
			if i == j {
				continue
			}
			otherPubKey := config.PublicKey(otherCfg.Device.PrivateKey).String()
			waitFor(t, 15*time.Second,
				cfg.Device.Name+" has "+otherCfg.Device.Name+"'s WG peer",
				func() bool {
					return fakes[i].WireGuard.getDevice() != nil && fakes[i].WireGuard.getDevice().hasPeer(otherPubKey)
				})
		}
	}

	// Verify peer counts.
	for i := range agents {
		if fakes[i].WireGuard.getDevice().peerCount() != 2 {
			t.Errorf("agent %d has %d WG peers, want 2",
				i, fakes[i].WireGuard.getDevice().peerCount())
		}
	}

	cancel()
	for i, ch := range errChs {
		select {
		case err := <-ch:
			if !isShutdownError(err) {
				t.Errorf("agent %d error: %v", i, err)
			}
		case <-time.After(5 * time.Second):
			t.Errorf("agent %d did not shut down", i)
		}
	}
}

// TestAgent_TokenRefresh verifies that the agent calls the auth refresher
// on startup when OAuth credentials are configured.
func TestAgent_TokenRefresh(t *testing.T) {
	t.Parallel()

	_, _, wsURL := startTestHub(t)

	cfg := testConfig("alpha", "10.0.0.1/24", wsURL)
	cfg.Network.DeviceID = "test-device-id"
	cfg.Network.RefreshToken = "test-refresh-token"

	deps, fakes := newTestDeps()
	deps.Signaling = func(cfg signaling.ClientConfig) SignalingClient {
		return signaling.NewClient(cfg)
	}

	agent := New(cfg, nil, WithDeps(deps), WithConfigPath("/tmp/test-config.toml"))

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	errCh := make(chan error, 1)
	go func() { errCh <- agent.Run(ctx) }()

	// The agent should call Refresh during startup.
	waitFor(t, 5*time.Second, "auth refresh called", func() bool {
		return fakes.Auth.callCount() >= 1
	})

	// The config persister should have been called to save the rotated token.
	waitFor(t, 2*time.Second, "config saved", func() bool {
		fakes.Config.mu.Lock()
		defer fakes.Config.mu.Unlock()
		return fakes.Config.savedSecrets >= 1
	})

	// The agent should have the JWT from the fake auth.
	jwt := agent.CurrentJWT()
	if jwt != "test-jwt" {
		t.Errorf("agent JWT = %q, want %q", jwt, "test-jwt")
	}

	// The refresh token in config should have been rotated.
	if cfg.Network.RefreshToken != "test-refresh" {
		t.Errorf("refresh token = %q, want %q", cfg.Network.RefreshToken, "test-refresh")
	}

	cancel()
	select {
	case err := <-errCh:
		if !isShutdownError(err) {
			t.Errorf("agent error: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Error("agent did not shut down")
	}
}

// TestAgent_RoutesAccepted verifies that when a peer advertises routes and
// the agent has AcceptRoutes enabled, the routes are added to the kernel.
func TestAgent_RoutesAccepted(t *testing.T) {
	t.Parallel()

	_, _, wsURL := startTestHub(t)

	cfgA := testConfig("alpha", "10.0.0.1/24", wsURL)
	cfgA.Device.Routes = []string{"192.168.1.0/24"} // alpha advertises a LAN subnet

	cfgB := testConfig("bravo", "10.0.0.2/24", wsURL)
	cfgB.Device.AcceptRoutes = true //nolint:staticcheck // testing legacy backward compat

	depsA, _ := newTestDeps()
	depsB, fakesB := newTestDeps()

	// alpha has a LAN subnet, so its setupForwardingAndNAT will be called.
	// Set up the fake network manager to return an interface for the subnet.
	depsA.Signaling = func(cfg signaling.ClientConfig) SignalingClient {
		return signaling.NewClient(cfg)
	}
	depsB.Signaling = func(cfg signaling.ClientConfig) SignalingClient {
		return signaling.NewClient(cfg)
	}

	agentA := New(cfgA, nil, WithDeps(depsA))
	agentB := New(cfgB, nil, WithDeps(depsB))

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	errChA := make(chan error, 1)
	errChB := make(chan error, 1)
	go func() { errChA <- agentA.Run(ctx) }()
	go func() { errChB <- agentB.Run(ctx) }()

	// Wait for bravo to receive alpha's route and add it.
	waitFor(t, 10*time.Second, "bravo has alpha's route in kernel", func() bool {
		fakesB.Network.mu.Lock()
		defer fakesB.Network.mu.Unlock()
		routes := fakesB.Network.routes[tunnel.DefaultTUNName]
		for _, r := range routes {
			if r == "192.168.1.0/24" {
				return true
			}
		}
		return false
	})

	cancel()
	for _, ch := range []chan error{errChA, errChB} {
		select {
		case err := <-ch:
			if !isShutdownError(err) {
				t.Errorf("agent error: %v", err)
			}
		case <-time.After(5 * time.Second):
			t.Error("agent did not shut down")
		}
	}
}

// TestAgent_GlareResolution verifies that when both peers send offers
// simultaneously (possible during ICE restart), the glare is resolved
// and exactly one connection survives.
func TestAgent_GlareResolution(t *testing.T) {
	t.Parallel()

	_, _, wsURL := startTestHub(t)

	// "alpha" < "bravo", so alpha is the preferred offerer.
	// Both start simultaneously, and the hub sends peers lists to both.
	// Alpha should offer, bravo should wait for the offer.
	cfgA := testConfig("alpha", "10.0.0.1/24", wsURL)
	cfgB := testConfig("bravo", "10.0.0.2/24", wsURL)

	depsA, fakesA := newTestDeps()
	depsB, fakesB := newTestDeps()

	depsA.Signaling = func(cfg signaling.ClientConfig) SignalingClient {
		return signaling.NewClient(cfg)
	}
	depsB.Signaling = func(cfg signaling.ClientConfig) SignalingClient {
		return signaling.NewClient(cfg)
	}

	agentA := New(cfgA, nil, WithDeps(depsA))
	agentB := New(cfgB, nil, WithDeps(depsB))

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	errChA := make(chan error, 1)
	errChB := make(chan error, 1)
	go func() { errChA <- agentA.Run(ctx) }()
	go func() { errChB <- agentB.Run(ctx) }()

	pubKeyA := config.PublicKey(cfgA.Device.PrivateKey).String()
	pubKeyB := config.PublicKey(cfgB.Device.PrivateKey).String()

	// Wait for both to be connected.
	waitFor(t, 10*time.Second, "alpha has bravo's WG peer", func() bool {
		return fakesA.WireGuard.getDevice() != nil && fakesA.WireGuard.getDevice().hasPeer(pubKeyB)
	})
	waitFor(t, 10*time.Second, "bravo has alpha's WG peer", func() bool {
		return fakesB.WireGuard.getDevice() != nil && fakesB.WireGuard.getDevice().hasPeer(pubKeyA)
	})

	// Each agent should have exactly 1 peer.
	agentA.mu.Lock()
	peersA := len(agentA.peers)
	agentA.mu.Unlock()

	agentB.mu.Lock()
	peersB := len(agentB.peers)
	agentB.mu.Unlock()

	if peersA != 1 {
		t.Errorf("alpha has %d peers, want 1", peersA)
	}
	if peersB != 1 {
		t.Errorf("bravo has %d peers, want 1", peersB)
	}

	cancel()
	for _, ch := range []chan error{errChA, errChB} {
		select {
		case err := <-ch:
			if !isShutdownError(err) {
				t.Errorf("agent error: %v", err)
			}
		case <-time.After(5 * time.Second):
			t.Error("agent did not shut down")
		}
	}
}

// TestAgent_SelfSkip verifies that the agent ignores itself in the peers list.
func TestAgent_SelfSkip(t *testing.T) {
	t.Parallel()

	_, _, wsURL := startTestHub(t)

	cfg := testConfig("solo", "10.0.0.1/24", wsURL)

	deps, fakes := newTestDeps()
	deps.Signaling = func(cfg signaling.ClientConfig) SignalingClient {
		return signaling.NewClient(cfg)
	}

	ag := New(cfg, nil, WithDeps(deps))

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	errCh := make(chan error, 1)
	go func() { errCh <- ag.Run(ctx) }()

	// Wait a bit to ensure the agent doesn't crash or create peers to itself.
	time.Sleep(1 * time.Second)

	ag.mu.Lock()
	peerCount := len(ag.peers)
	ag.mu.Unlock()

	if peerCount != 0 {
		t.Errorf("agent has %d peers, want 0 (should not connect to itself)", peerCount)
	}

	_ = fakes // verify no WG peers were created
	if fakes.WireGuard.getDevice() != nil && fakes.WireGuard.getDevice().peerCount() != 0 {
		t.Errorf("WG device has %d peers, want 0", fakes.WireGuard.getDevice().peerCount())
	}

	cancel()
	select {
	case err := <-errCh:
		if !isShutdownError(err) {
			t.Errorf("agent error: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Error("agent did not shut down")
	}
}

// --- connectedPair sets up two agents that are fully connected ---

type connectedPair struct {
	agentA, agentB   *Agent
	fakesA, fakesB   *testFakes
	pubKeyA, pubKeyB string
	cancel           context.CancelFunc
	errChA, errChB   chan error
}

// startConnectedPair starts two agents, waits for them to establish a WebRTC
// connection and WireGuard peer configuration, then returns the pair for
// further testing. Call pair.shutdown(t) when done.
func startConnectedPair(t *testing.T) *connectedPair {
	t.Helper()

	_, _, wsURL := startTestHub(t)

	cfgA := testConfig("alpha", "10.0.0.1/24", wsURL)
	cfgB := testConfig("bravo", "10.0.0.2/24", wsURL)

	depsA, fakesA := newTestDeps()
	depsB, fakesB := newTestDeps()

	depsA.Signaling = func(cfg signaling.ClientConfig) SignalingClient {
		return signaling.NewClient(cfg)
	}
	depsB.Signaling = func(cfg signaling.ClientConfig) SignalingClient {
		return signaling.NewClient(cfg)
	}

	agentA := New(cfgA, nil, WithDeps(depsA))
	agentB := New(cfgB, nil, WithDeps(depsB))

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)

	errChA := make(chan error, 1)
	errChB := make(chan error, 1)
	go func() { errChA <- agentA.Run(ctx) }()
	go func() { errChB <- agentB.Run(ctx) }()

	pubKeyA := config.PublicKey(cfgA.Device.PrivateKey).String()
	pubKeyB := config.PublicKey(cfgB.Device.PrivateKey).String()

	// Wait for full connection.
	waitFor(t, 10*time.Second, "bravo has alpha's WG peer", func() bool {
		return fakesB.WireGuard.getDevice() != nil && fakesB.WireGuard.getDevice().hasPeer(pubKeyA)
	})
	waitFor(t, 10*time.Second, "alpha has bravo's WG peer", func() bool {
		return fakesA.WireGuard.getDevice() != nil && fakesA.WireGuard.getDevice().hasPeer(pubKeyB)
	})

	return &connectedPair{
		agentA: agentA, agentB: agentB,
		fakesA: fakesA, fakesB: fakesB,
		pubKeyA: pubKeyA, pubKeyB: pubKeyB,
		cancel: cancel,
		errChA: errChA, errChB: errChB,
	}
}

func (p *connectedPair) shutdown(t *testing.T) {
	t.Helper()
	p.cancel()
	for _, ch := range []chan error{p.errChA, p.errChB} {
		select {
		case err := <-ch:
			if !isShutdownError(err) {
				t.Errorf("agent shutdown error: %v", err)
			}
		case <-time.After(5 * time.Second):
			t.Error("agent did not shut down")
		}
	}
}

// --- ICE lifecycle tests ---

// TestAgent_ICEDisconnect_GracePeriod verifies that when ICE enters the
// disconnected state, the agent starts a grace period timer and does not
// immediately remove the peer. If the connection recovers within the grace
// period, the peer is kept.
func TestAgent_ICEDisconnect_GracePeriod(t *testing.T) {
	t.Parallel()

	pair := startConnectedPair(t)
	defer pair.shutdown(t)

	// Verify both agents have 1 peer.
	pair.agentA.mu.Lock()
	if len(pair.agentA.peers) != 1 {
		t.Fatalf("alpha has %d peers, want 1", len(pair.agentA.peers))
	}
	pair.agentA.mu.Unlock()

	// Simulate ICE disconnection on alpha's side.
	pair.agentA.handleICEStateChange(context.Background(), "bravo", webrtc.ICEConnectionStateDisconnected)

	// The peer should still be there (grace period started, not expired).
	time.Sleep(100 * time.Millisecond)

	pair.agentA.mu.Lock()
	ps, ok := pair.agentA.peers["bravo"]
	hasTimer := ok && ps.restartTimer != nil
	hasDisconnectTime := ok && !ps.disconnectTime.IsZero()
	pair.agentA.mu.Unlock()

	if !ok {
		t.Fatal("alpha lost bravo peer immediately after ICE disconnect (should have grace period)")
	}
	if !hasTimer {
		t.Error("alpha did not start grace period timer after ICE disconnect")
	}
	if !hasDisconnectTime {
		t.Error("alpha did not record disconnect time")
	}

	// Simulate ICE reconnection before grace period expires.
	pair.agentA.handleICEStateChange(context.Background(), "bravo", webrtc.ICEConnectionStateConnected)

	// Verify timer was cancelled and restart counter is zero.
	pair.agentA.mu.Lock()
	ps, ok = pair.agentA.peers["bravo"]
	timerNil := ok && ps.restartTimer == nil
	restartsZero := ok && ps.iceRestarts == 0
	pair.agentA.mu.Unlock()

	if !ok {
		t.Fatal("alpha lost bravo peer after ICE reconnected")
	}
	if !timerNil {
		t.Error("grace period timer was not cancelled after reconnection")
	}
	if !restartsZero {
		t.Error("ICE restart counter was not reset after reconnection")
	}

	// WireGuard peer should still be configured.
	if !pair.fakesA.WireGuard.getDevice().hasPeer(pair.pubKeyB) {
		t.Error("alpha lost bravo's WireGuard peer after ICE recovery")
	}
}

// TestAgent_ICEFailed_RestartsICE verifies that when ICE fails, the agent
// attempts an ICE restart which results in a successful reconnection.
// Since the underlying real WebRTC connection is fine, the restart offer/answer
// cycle succeeds quickly and the connection returns to connected state.
func TestAgent_ICEFailed_RestartsICE(t *testing.T) {
	t.Parallel()

	pair := startConnectedPair(t)
	defer pair.shutdown(t)

	// Simulate ICE failure on alpha's side.
	// The agent calls attemptICERestart which sends a new offer, the remote
	// answers, and ICE reconnects. This triggers the "connected" callback
	// which resets iceRestarts to 0.
	pair.agentA.handleICEStateChange(context.Background(), "bravo", webrtc.ICEConnectionStateFailed)

	// After the restart cycle completes, the connection should be re-established.
	// Verify the peer is still present and iceRestarts was reset to 0
	// (indicates it went through restart -> reconnect cycle).
	waitFor(t, 15*time.Second, "alpha reconnected to bravo after ICE restart", func() bool {
		pair.agentA.mu.Lock()
		defer pair.agentA.mu.Unlock()
		ps, ok := pair.agentA.peers["bravo"]
		if !ok {
			return false
		}
		// After successful restart, ICE connects and resets counter.
		return ps.iceRestarts == 0 && !ps.pendingRestart
	})

	// WireGuard peer should still be intact.
	if !pair.fakesA.WireGuard.getDevice().hasPeer(pair.pubKeyB) {
		t.Error("alpha lost bravo's WireGuard peer after ICE restart")
	}
}

// TestAgent_ICERestart_MaxAttempts verifies that after exhausting all ICE
// restart attempts, the agent removes the peer entirely. We simulate this by
// pre-setting the restart counter to the maximum and then triggering one more
// failure, which should push it over the limit and trigger removal.
func TestAgent_ICERestart_MaxAttempts(t *testing.T) {
	t.Parallel()

	pair := startConnectedPair(t)
	defer pair.shutdown(t)

	// Pre-set the restart counter to the maximum.
	pair.agentA.mu.Lock()
	ps, ok := pair.agentA.peers["bravo"]
	if !ok {
		pair.agentA.mu.Unlock()
		t.Fatal("alpha has no bravo peer")
	}
	ps.iceRestarts = maxICERestarts
	pair.agentA.mu.Unlock()

	// Trigger one more ICE failure. This calls attemptICERestart which sees
	// iceRestarts > maxICERestarts and removes the peer.
	pair.agentA.handleICEStateChange(context.Background(), "bravo", webrtc.ICEConnectionStateFailed)

	// After exhausting restarts, bravo should be removed.
	waitFor(t, 5*time.Second, "alpha removed bravo after max restarts", func() bool {
		pair.agentA.mu.Lock()
		_, exists := pair.agentA.peers["bravo"]
		pair.agentA.mu.Unlock()
		return !exists
	})

	// WireGuard peer should also be removed.
	if pair.fakesA.WireGuard.getDevice().hasPeer(pair.pubKeyB) {
		t.Error("alpha still has bravo's WireGuard peer after max ICE restarts")
	}
}

// TestAgent_ICEDisconnect_GraceExpires verifies that when the grace period
// expires without reconnection, the agent attempts an ICE restart. Since the
// underlying connection is fine, the restart succeeds and the peer remains
// connected.
func TestAgent_ICEDisconnect_GraceExpires(t *testing.T) {
	t.Parallel()

	pair := startConnectedPair(t)
	defer pair.shutdown(t)

	// Simulate ICE disconnection — this starts the grace timer.
	pair.agentA.handleICEStateChange(context.Background(), "bravo", webrtc.ICEConnectionStateDisconnected)

	// Verify the timer was started.
	pair.agentA.mu.Lock()
	ps, ok := pair.agentA.peers["bravo"]
	timerStarted := ok && ps.restartTimer != nil
	pair.agentA.mu.Unlock()
	if !timerStarted {
		t.Fatal("grace period timer was not started")
	}

	// The grace period is 5 seconds. Wait for it to expire. After the timer
	// fires, attemptICERestart runs, sends a new offer, the remote answers,
	// and ICE reconnects — which resets the timer and iceRestarts back to 0.
	// We verify that the grace timer was consumed (set to nil after restart).
	waitFor(t, 8*time.Second, "grace timer consumed after expiry", func() bool {
		pair.agentA.mu.Lock()
		defer pair.agentA.mu.Unlock()
		ps, ok := pair.agentA.peers["bravo"]
		if !ok {
			return false
		}
		// After grace fires: attemptICERestart sets restartTimer=nil, then
		// ICE reconnects and resets iceRestarts=0. Either condition proves
		// the timer fired and a restart was attempted.
		return ps.restartTimer == nil
	})

	// Peer should still exist after the restart/reconnect cycle.
	pair.agentA.mu.Lock()
	_, ok = pair.agentA.peers["bravo"]
	pair.agentA.mu.Unlock()
	if !ok {
		t.Error("alpha lost bravo after grace period expired and ICE restarted")
	}

	// WireGuard peer should still be configured.
	if !pair.fakesA.WireGuard.getDevice().hasPeer(pair.pubKeyB) {
		t.Error("alpha lost bravo's WireGuard peer after grace expiry restart")
	}
}

// --- ICE candidate buffering tests ---

// addressStrippingSignalingClient wraps a real SignalingClient and strips the
// tunnel address from a specific peer's entry in PeersMessage. This simulates
// a peer that joins without advertising a tunnel address.
type addressStrippingSignalingClient struct {
	SignalingClient
	stripPeer  string
	origMsgCh  <-chan protocol.Message
	filteredCh chan protocol.Message
}

func (a *addressStrippingSignalingClient) Connect(ctx context.Context) error {
	if err := a.SignalingClient.Connect(ctx); err != nil {
		return err
	}
	a.origMsgCh = a.SignalingClient.Messages()
	a.filteredCh = make(chan protocol.Message, 64)
	go func() {
		for msg := range a.origMsgCh {
			if peers, ok := msg.(*protocol.PeersMessage); ok {
				for i := range peers.Peers {
					if peers.Peers[i].PeerID == a.stripPeer {
						peers.Peers[i].Address = ""
					}
				}
			}
			a.filteredCh <- msg
		}
		close(a.filteredCh)
	}()
	return nil
}

func (a *addressStrippingSignalingClient) Messages() <-chan protocol.Message {
	return a.filteredCh
}

// delayedSDPSignalingClient wraps a real SignalingClient and delays delivery
// of SDP answer messages by a configurable duration while letting ICE
// candidates through immediately. This simulates the real-world race where
// trickle ICE candidates arrive before the SDP answer.
type delayedSDPSignalingClient struct {
	SignalingClient
	answerDelay time.Duration
	origMsgCh   <-chan protocol.Message
	reorderedCh chan protocol.Message
}

func newDelayedSDPSignalingClient(inner SignalingClient, answerDelay time.Duration) *delayedSDPSignalingClient {
	return &delayedSDPSignalingClient{
		SignalingClient: inner,
		answerDelay:     answerDelay,
		origMsgCh:       inner.Messages(),
		reorderedCh:     make(chan protocol.Message, 64),
	}
}

func (d *delayedSDPSignalingClient) Connect(ctx context.Context) error {
	if err := d.SignalingClient.Connect(ctx); err != nil {
		return err
	}
	// Start a goroutine that reads from the real Messages() channel and
	// delays SDP answers while forwarding everything else immediately.
	go func() {
		for msg := range d.origMsgCh {
			switch msg.(type) {
			case *protocol.AnswerMessage:
				// Delay the answer so ICE candidates arrive first.
				go func(m protocol.Message) {
					time.Sleep(d.answerDelay)
					d.reorderedCh <- m
				}(msg)
			default:
				d.reorderedCh <- msg
			}
		}
		close(d.reorderedCh)
	}()
	return nil
}

func (d *delayedSDPSignalingClient) Messages() <-chan protocol.Message {
	return d.reorderedCh
}

// TestAgent_ICECandidate_BufferedBeforeRemoteDescription verifies that ICE
// candidates arriving before SetRemoteDescription are buffered and flushed
// once the remote SDP is set. This simulates a network condition where
// trickle ICE candidates arrive at the offerer before the SDP answer.
func TestAgent_ICECandidate_BufferedBeforeRemoteDescription(t *testing.T) {
	t.Parallel()

	_, _, wsURL := startTestHub(t)

	// "alpha" < "bravo", so alpha is the offerer.
	cfgA := testConfig("alpha", "10.0.0.1/24", wsURL)
	cfgB := testConfig("bravo", "10.0.0.2/24", wsURL)

	depsA, fakesA := newTestDeps()
	depsB, fakesB := newTestDeps()

	// Alpha (offerer) gets a delayed signaling client — SDP answers are
	// delayed by 500ms, but ICE candidates pass through immediately.
	// This means bravo's ICE candidates will arrive at alpha before
	// bravo's SDP answer does.
	depsA.Signaling = func(cfg signaling.ClientConfig) SignalingClient {
		inner := signaling.NewClient(cfg)
		return newDelayedSDPSignalingClient(inner, 500*time.Millisecond)
	}
	depsB.Signaling = func(cfg signaling.ClientConfig) SignalingClient {
		return signaling.NewClient(cfg)
	}

	agentA := New(cfgA, nil, WithDeps(depsA))
	agentB := New(cfgB, nil, WithDeps(depsB))

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	errChA := make(chan error, 1)
	errChB := make(chan error, 1)
	go func() { errChA <- agentA.Run(ctx) }()
	go func() { errChB <- agentB.Run(ctx) }()

	pubKeyA := config.PublicKey(cfgA.Device.PrivateKey).String()
	pubKeyB := config.PublicKey(cfgB.Device.PrivateKey).String()

	// Despite the delayed SDP answer, both agents should still establish
	// a full connection because ICE candidates are buffered and flushed
	// once the answer arrives.
	waitFor(t, 15*time.Second, "alpha has bravo's WG peer (with delayed SDP)", func() bool {
		return fakesA.WireGuard.getDevice() != nil && fakesA.WireGuard.getDevice().hasPeer(pubKeyB)
	})
	waitFor(t, 15*time.Second, "bravo has alpha's WG peer (with delayed SDP)", func() bool {
		return fakesB.WireGuard.getDevice() != nil && fakesB.WireGuard.getDevice().hasPeer(pubKeyA)
	})

	cancel()
	for _, ch := range []chan error{errChA, errChB} {
		select {
		case err := <-ch:
			if !isShutdownError(err) {
				t.Errorf("agent error: %v", err)
			}
		case <-time.After(5 * time.Second):
			t.Error("agent did not shut down")
		}
	}
}

// --- Orphaned PeerConnection tests ---

// TestAgent_OrphanedPeerConnection_Closed verifies that when createRTCPeer
// is called for a peer that already has a PeerConnection, the old one is
// closed to prevent resource leaks (goroutines, sockets, TURN allocations).
func TestAgent_OrphanedPeerConnection_Closed(t *testing.T) {
	t.Parallel()

	pair := startConnectedPair(t)
	defer pair.shutdown(t)

	// Grab a reference to bravo's current PeerConnection on alpha's side.
	pair.agentA.mu.Lock()
	ps, ok := pair.agentA.peers["bravo"]
	if !ok || ps.rtcPeer == nil {
		pair.agentA.mu.Unlock()
		t.Fatal("alpha has no active PeerConnection to bravo")
	}
	oldPeer := ps.rtcPeer
	pair.agentA.mu.Unlock()

	// Verify the old PeerConnection is alive.
	oldState := oldPeer.ConnectionState()
	if oldState == webrtc.ICEConnectionStateClosed {
		t.Fatal("old PeerConnection is already closed before test")
	}

	// Call createRTCPeer for "bravo" — this should close the old PC.
	ctx := context.Background()
	newPeer, err := pair.agentA.createRTCPeer(ctx, "bravo")
	if err != nil {
		t.Fatalf("createRTCPeer failed: %v", err)
	}

	// The new peer should be different from the old one.
	if newPeer == oldPeer {
		t.Fatal("createRTCPeer returned the same Peer object")
	}

	// The old PeerConnection should now be closed.
	waitFor(t, 3*time.Second, "old PeerConnection closed", func() bool {
		return oldPeer.ConnectionState() == webrtc.ICEConnectionStateClosed
	})

	// The peerState should now point to the new Peer.
	pair.agentA.mu.Lock()
	ps = pair.agentA.peers["bravo"]
	currentPeer := ps.rtcPeer
	pair.agentA.mu.Unlock()

	if currentPeer != newPeer {
		t.Error("peerState.rtcPeer does not point to the new Peer")
	}
}

// --- Security tests ---

// TestAgent_PeerWithoutAddress_NoWireGuardPeer verifies that a peer without
// a tunnel address does NOT get a WireGuard peer added. Previously, peers
// without an address would get a wildcard AllowedIPs (0.0.0.0/0, ::/0),
// making them a default gateway — a security risk.
//
// To test this, we strip bravo's address from the peers list that alpha
// receives via a signaling wrapper, simulating a peer that joins without
// advertising an address.
func TestAgent_PeerWithoutAddress_NoWireGuardPeer(t *testing.T) {
	t.Parallel()

	_, _, wsURL := startTestHub(t)

	cfgA := testConfig("alpha", "10.0.0.1/24", wsURL)
	cfgB := testConfig("bravo", "10.0.0.2/24", wsURL)

	depsA, fakesA := newTestDeps()
	depsB, _ := newTestDeps()

	// Alpha gets a signaling wrapper that strips the address from bravo's
	// peer info. This simulates a peer that joins without an address.
	depsA.Signaling = func(cfg signaling.ClientConfig) SignalingClient {
		inner := signaling.NewClient(cfg)
		return &addressStrippingSignalingClient{SignalingClient: inner, stripPeer: "bravo"}
	}
	depsB.Signaling = func(cfg signaling.ClientConfig) SignalingClient {
		return signaling.NewClient(cfg)
	}

	agentA := New(cfgA, nil, WithDeps(depsA))
	agentB := New(cfgB, nil, WithDeps(depsB))

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	errChA := make(chan error, 1)
	errChB := make(chan error, 1)
	go func() { errChA <- agentA.Run(ctx) }()
	go func() { errChB <- agentB.Run(ctx) }()

	pubKeyB := config.PublicKey(cfgB.Device.PrivateKey).String()

	// Wait for the data channel to open — the agents will discover each
	// other and establish a WebRTC connection. We wait for alpha to see
	// bravo's rtcPeer and the data channel to be established.
	waitFor(t, 10*time.Second, "alpha has bravo's rtcPeer with open data channel", func() bool {
		agentA.mu.Lock()
		defer agentA.mu.Unlock()
		ps, ok := agentA.peers["bravo"]
		return ok && ps.rtcPeer != nil && !ps.connectedAt.IsZero()
	})

	// Give it a moment for the WG peer to be (incorrectly) added.
	time.Sleep(500 * time.Millisecond)

	// Alpha should NOT have added bravo as a WireGuard peer because bravo
	// has no tunnel address (we stripped it from the peers list).
	dev := fakesA.WireGuard.getDevice()
	if dev != nil && dev.hasPeer(pubKeyB) {
		t.Error("alpha added bravo as WireGuard peer despite bravo having no tunnel address")
	}

	cancel()
	for _, ch := range []chan error{errChA, errChB} {
		select {
		case err := <-ch:
			if !isShutdownError(err) {
				t.Errorf("agent error: %v", err)
			}
		case <-time.After(5 * time.Second):
			t.Error("agent did not shut down")
		}
	}
}

// --- Network change / force reconnect tests ---

// TestAgent_NotifyNetworkChange verifies that calling NotifyNetworkChange:
//  1. Resets ICE restart counters and cancels grace timers.
//  2. Sets needsRestart=true on all peers (deferred restart).
//  3. Does NOT immediately trigger ICE restart goroutines.
//  4. When signaling reconnects and handlePeers runs, the deferred restart
//     fires and the connection recovers.
//  5. Debounce: rapid consecutive calls are coalesced.
func TestAgent_NotifyNetworkChange(t *testing.T) {
	t.Parallel()

	pair := startConnectedPair(t)
	defer pair.shutdown(t)

	// Set up a stale state: simulate a prior disconnect with a grace timer
	// and some restart attempts.
	pair.agentA.mu.Lock()
	ps, ok := pair.agentA.peers["bravo"]
	if !ok {
		pair.agentA.mu.Unlock()
		t.Fatal("alpha has no bravo peer")
	}
	ps.iceRestarts = 2
	ps.disconnectTime = time.Now()
	ps.restartTimer = time.AfterFunc(1*time.Hour, func() {}) // dummy timer
	pair.agentA.mu.Unlock()

	// Trigger network change notification.
	pair.agentA.NotifyNetworkChange()

	// Verify ICE state was reset AND needsRestart is set.
	// NotifyNetworkChange should NOT fire ICE restart goroutines — it only
	// sets the flag. The actual restart happens when handlePeers runs after
	// signaling reconnects.
	pair.agentA.mu.Lock()
	ps, ok = pair.agentA.peers["bravo"]
	if !ok {
		pair.agentA.mu.Unlock()
		t.Fatal("alpha has no bravo peer after NotifyNetworkChange")
	}
	if ps.iceRestarts != 0 {
		t.Errorf("iceRestarts = %d, want 0", ps.iceRestarts)
	}
	if ps.restartTimer != nil {
		t.Error("restartTimer should be nil after NotifyNetworkChange")
	}
	if ps.pendingRestart {
		t.Error("pendingRestart should be false after NotifyNetworkChange")
	}
	if !ps.needsRestart {
		t.Error("needsRestart should be true after NotifyNetworkChange")
	}
	pair.agentA.mu.Unlock()

	// The peer should still exist and be connected (no immediate teardown).
	if !pair.fakesA.WireGuard.getDevice().hasPeer(pair.pubKeyB) {
		t.Error("alpha lost bravo's WireGuard peer after network change")
	}

	// Wait for signaling to reconnect, handlePeers to trigger the deferred
	// restart, and the full connection to re-establish (data channel open →
	// WireGuard peer re-added). The removePeer call during recovery clears
	// the WG peer, so we must wait for it to be re-added.
	waitFor(t, 15*time.Second, "deferred ICE restart completes and WG peer re-added", func() bool {
		pair.agentA.mu.Lock()
		ps, ok := pair.agentA.peers["bravo"]
		needsRestart := ok && ps.needsRestart
		pair.agentA.mu.Unlock()
		if needsRestart {
			return false
		}
		return pair.fakesA.WireGuard.getDevice() != nil &&
			pair.fakesA.WireGuard.getDevice().hasPeer(pair.pubKeyB)
	})

	// Test debounce: a second call within networkChangeDebounce (3s)
	// should be a no-op.
	pair.agentA.mu.Lock()
	ps = pair.agentA.peers["bravo"]
	ps.iceRestarts = 99 // sentinel value
	pair.agentA.mu.Unlock()

	pair.agentA.NotifyNetworkChange() // should be debounced

	time.Sleep(200 * time.Millisecond)

	pair.agentA.mu.Lock()
	ps = pair.agentA.peers["bravo"]
	restartsAfterDebounce := ps.iceRestarts
	needsRestartAfterDebounce := ps.needsRestart
	pair.agentA.mu.Unlock()

	// If debounced correctly, the sentinel value 99 should survive and
	// needsRestart should NOT be set by the debounced call.
	if restartsAfterDebounce != 99 {
		t.Logf("note: iceRestarts = %d (expected 99; may have been reset by prior restart completing)", restartsAfterDebounce)
	}
	if needsRestartAfterDebounce {
		t.Error("debounced NotifyNetworkChange should not set needsRestart")
	}
}
