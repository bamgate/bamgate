// Package agent is the top-level orchestrator that ties together signaling,
// WebRTC, the bridge (custom conn.Bind), and the WireGuard tunnel.
//
// The agent manages the full lifecycle:
//  1. Create kernel TUN device and WireGuard device with custom Bind
//  2. Configure the TUN interface IP address
//  3. Connect to the signaling server
//  4. Handle peer discovery, WebRTC connection setup, and data channel bridging
//  5. Manage WireGuard peers dynamically as WebRTC connections come and go
package agent

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"sync"
	"time"

	"github.com/pion/webrtc/v4"

	"github.com/kuuji/riftgate/internal/bridge"
	"github.com/kuuji/riftgate/internal/config"
	"github.com/kuuji/riftgate/internal/control"
	"github.com/kuuji/riftgate/internal/signaling"
	"github.com/kuuji/riftgate/internal/tunnel"
	rtcpkg "github.com/kuuji/riftgate/internal/webrtc"
	"github.com/kuuji/riftgate/pkg/protocol"
)

// Agent orchestrates the riftgate VPN tunnel. It connects to the signaling
// server, establishes WebRTC connections with peers, and bridges WireGuard
// traffic over data channels.
type Agent struct {
	cfg *config.Config
	log *slog.Logger

	bind      *bridge.Bind
	wgDevice  *tunnel.Device
	tunName   string // kernel interface name (e.g. "riftgate0")
	sigClient *signaling.Client
	ctrlSrv   *control.Server

	// Forwarding and NAT state for cleanup on shutdown.
	natManager      *tunnel.NATManager
	forwardingState []forwardingSave // interfaces whose forwarding state was changed

	startedAt time.Time
	mu        sync.Mutex
	peers     map[string]*peerState // peerID -> state
}

// forwardingSave records the previous forwarding state for an interface so it
// can be restored on shutdown.
type forwardingSave struct {
	ifName          string
	previousEnabled bool
}

const (
	// maxICERestarts is the maximum number of ICE restarts to attempt
	// before tearing down the peer connection entirely.
	maxICERestarts = 3

	// iceDisconnectGrace is how long to wait after an ICE disconnection
	// before triggering an ICE restart. ICE may recover on its own if the
	// network blip is short.
	iceDisconnectGrace = 5 * time.Second
)

// peerState tracks the state of a single remote peer.
type peerState struct {
	rtcPeer   *rtcpkg.Peer
	publicKey config.Key // WireGuard public key
	address   string     // WireGuard tunnel address (e.g. "10.0.0.3/24")
	routes    []string   // additional subnets reachable through this peer

	connectedAt time.Time // when the data channel opened

	// ICE restart tracking.
	iceRestarts    int         // number of restarts attempted
	disconnectTime time.Time   // when ICE entered disconnected state
	restartTimer   *time.Timer // grace period timer (nil = not running)
}

// New creates a new Agent with the given configuration.
func New(cfg *config.Config, logger *slog.Logger) *Agent {
	if logger == nil {
		logger = slog.Default()
	}
	return &Agent{
		cfg:   cfg,
		log:   logger.With("component", "agent"),
		peers: make(map[string]*peerState),
	}
}

// Run starts the agent and blocks until the context is cancelled or a fatal
// error occurs. It creates the TUN device, WireGuard device, connects to
// signaling, and processes peer events.
func (a *Agent) Run(ctx context.Context) error {
	// 1. Create the bridge Bind.
	a.bind = bridge.NewBind(a.log)

	// 2. Create kernel TUN device.
	tunName := "riftgate0"
	tunDev, err := tunnel.CreateTUN(tunName, tunnel.DefaultMTU)
	if err != nil {
		return fmt.Errorf("creating TUN device: %w", err)
	}

	// Get the actual name (may differ if OS renames it).
	actualName, err := tunDev.Name()
	if err != nil {
		_ = tunDev.Close()
		return fmt.Errorf("getting TUN device name: %w", err)
	}
	a.tunName = actualName
	a.log.Info("TUN device created", "name", actualName)

	// 3. Create WireGuard device with our custom Bind.
	wgCfg := tunnel.DeviceConfig{
		PrivateKey: a.cfg.Device.PrivateKey,
	}
	a.wgDevice, err = tunnel.NewDevice(wgCfg, tunDev, a.bind, a.log)
	if err != nil {
		_ = tunDev.Close()
		return fmt.Errorf("creating WireGuard device: %w", err)
	}
	defer a.wgDevice.Close()
	defer a.cleanupForwardingAndNAT()

	// 4. Configure the TUN interface IP address and bring it up.
	if err := a.configureTUN(actualName); err != nil {
		return fmt.Errorf("configuring TUN interface: %w", err)
	}

	// 5. Start control server for "riftgate status".
	a.startedAt = time.Now()
	a.ctrlSrv = control.NewServer(control.ResolveSocketPath(), a.Status, a.log)
	if err := a.ctrlSrv.Start(); err != nil {
		a.log.Warn("control server failed to start (status command will be unavailable)", "error", err)
		// Non-fatal — agent can run without the control server.
	}

	// 6. Connect to signaling server.
	pubKey := config.PublicKey(a.cfg.Device.PrivateKey)
	a.sigClient = signaling.NewClient(signaling.ClientConfig{
		ServerURL: a.cfg.Network.ServerURL,
		PeerID:    a.cfg.Device.Name,
		PublicKey: pubKey.String(),
		Address:   a.cfg.Device.Address,
		Routes:    a.cfg.Device.Routes,
		AuthToken: a.cfg.Network.AuthToken,
		Logger:    a.log,
		Reconnect: signaling.ReconnectConfig{
			Enabled: true,
		},
	})

	if err := a.sigClient.Connect(ctx); err != nil {
		return fmt.Errorf("connecting to signaling server: %w", err)
	}

	a.log.Info("agent started",
		"device", a.cfg.Device.Name,
		"address", a.cfg.Device.Address,
		"server", a.cfg.Network.ServerURL,
	)

	// 6. Process signaling messages until context is cancelled.
	return a.processMessages(ctx)
}

// processMessages reads signaling messages and handles peer lifecycle events.
func (a *Agent) processMessages(ctx context.Context) error {
	for {
		select {
		case <-ctx.Done():
			a.shutdown()
			return ctx.Err()
		case msg, ok := <-a.sigClient.Messages():
			if !ok {
				a.shutdown()
				return fmt.Errorf("signaling connection closed")
			}
			if err := a.handleMessage(ctx, msg); err != nil {
				a.log.Error("handling signaling message", "error", err)
			}
		}
	}
}

// handleMessage dispatches a signaling message to the appropriate handler.
func (a *Agent) handleMessage(ctx context.Context, msg protocol.Message) error {
	switch m := msg.(type) {
	case *protocol.PeersMessage:
		return a.handlePeers(ctx, m)
	case *protocol.OfferMessage:
		return a.handleOffer(ctx, m)
	case *protocol.AnswerMessage:
		return a.handleAnswer(m)
	case *protocol.ICECandidateMessage:
		return a.handleICECandidate(m)
	case *protocol.PeerLeftMessage:
		return a.handlePeerLeft(m)
	default:
		a.log.Debug("ignoring unknown message type", "type", msg.MessageType())
		return nil
	}
}

// handlePeers processes the initial peer list. For each existing peer, we
// initiate a WebRTC connection if our peer ID is lexicographically smaller
// (to avoid both sides simultaneously offering).
func (a *Agent) handlePeers(ctx context.Context, msg *protocol.PeersMessage) error {
	a.log.Info("received peer list", "count", len(msg.Peers))
	for _, p := range msg.Peers {
		a.log.Info("discovered peer", "peer_id", p.PeerID, "public_key", p.PublicKey, "address", p.Address, "routes", p.Routes)

		// Determine who offers: the peer with the smaller ID.
		if a.cfg.Device.Name < p.PeerID {
			if err := a.initiateConnection(ctx, p.PeerID, p.PublicKey, p.Address, p.Routes); err != nil {
				a.log.Error("initiating connection", "peer_id", p.PeerID, "error", err)
			}
		} else {
			// We'll receive an offer from this peer. Pre-store their address
			// and routes so they're available when the data channel opens.
			a.mu.Lock()
			if ps, ok := a.peers[p.PeerID]; ok {
				ps.address = p.Address
				ps.routes = p.Routes
			} else {
				// Peer state not yet created; store address/routes for later.
				// createRTCPeer will be called when the offer arrives.
				a.peers[p.PeerID] = &peerState{address: p.Address, routes: p.Routes}
			}
			a.mu.Unlock()
		}
	}
	return nil
}

// handleOffer processes an incoming SDP offer from a remote peer.
func (a *Agent) handleOffer(ctx context.Context, msg *protocol.OfferMessage) error {
	a.log.Info("received offer", "from", msg.From)

	peer, err := a.createRTCPeer(ctx, msg.From)
	if err != nil {
		return fmt.Errorf("creating peer for offer: %w", err)
	}

	// Store the remote peer's WireGuard public key. The offer carries the
	// sender's public key so the answering side can configure WireGuard
	// before the data channel opens.
	if msg.PublicKey != "" {
		if wgPubKey, err := config.ParseKey(msg.PublicKey); err != nil {
			a.log.Warn("invalid public key in offer", "from", msg.From, "error", err)
		} else {
			a.mu.Lock()
			if ps, ok := a.peers[msg.From]; ok {
				ps.publicKey = wgPubKey
			}
			a.mu.Unlock()
		}
	}

	answerSDP, err := peer.HandleOffer(msg.SDP)
	if err != nil {
		return fmt.Errorf("handling offer: %w", err)
	}

	pubKey := config.PublicKey(a.cfg.Device.PrivateKey)
	return a.sigClient.Send(ctx, &protocol.AnswerMessage{
		From:      a.cfg.Device.Name,
		To:        msg.From,
		SDP:       answerSDP,
		PublicKey: pubKey.String(),
	})
}

// handleAnswer processes an incoming SDP answer from a remote peer.
func (a *Agent) handleAnswer(msg *protocol.AnswerMessage) error {
	a.log.Info("received answer", "from", msg.From)

	a.mu.Lock()
	ps, ok := a.peers[msg.From]
	if ok && ps.publicKey.IsZero() && msg.PublicKey != "" {
		if wgPubKey, err := config.ParseKey(msg.PublicKey); err == nil {
			ps.publicKey = wgPubKey
		}
	}
	a.mu.Unlock()

	if !ok {
		return fmt.Errorf("received answer from unknown peer: %s", msg.From)
	}

	return ps.rtcPeer.SetAnswer(msg.SDP)
}

// handleICECandidate processes an incoming ICE candidate from a remote peer.
func (a *Agent) handleICECandidate(msg *protocol.ICECandidateMessage) error {
	a.mu.Lock()
	ps, ok := a.peers[msg.From]
	a.mu.Unlock()

	if !ok {
		a.log.Debug("ICE candidate from unknown peer, ignoring", "from", msg.From)
		return nil
	}

	return ps.rtcPeer.AddICECandidate(msg.Candidate)
}

// handlePeerLeft tears down the WebRTC connection and removes the WireGuard
// peer when a remote peer disconnects.
func (a *Agent) handlePeerLeft(msg *protocol.PeerLeftMessage) error {
	a.log.Info("peer left", "peer_id", msg.PeerID)
	a.removePeer(msg.PeerID)
	return nil
}

// initiateConnection creates a WebRTC peer and sends an SDP offer to the
// remote peer via signaling.
func (a *Agent) initiateConnection(ctx context.Context, peerID, publicKey, address string, routes []string) error {
	a.log.Info("initiating connection", "peer_id", peerID)

	// Store the public key so we can configure WireGuard when the data
	// channel opens.
	wgPubKey, err := config.ParseKey(publicKey)
	if err != nil {
		return fmt.Errorf("parsing peer public key: %w", err)
	}

	a.mu.Lock()
	// Check if we already have a connection to this peer.
	if _, exists := a.peers[peerID]; exists {
		a.mu.Unlock()
		a.log.Debug("already connected to peer, skipping", "peer_id", peerID)
		return nil
	}
	a.mu.Unlock()

	peer, err := a.createRTCPeer(ctx, peerID)
	if err != nil {
		return fmt.Errorf("creating RTC peer: %w", err)
	}

	// Store the WireGuard public key, tunnel address, and routes.
	a.mu.Lock()
	if ps, ok := a.peers[peerID]; ok {
		ps.publicKey = wgPubKey
		ps.address = address
		ps.routes = routes
	}
	a.mu.Unlock()

	offerSDP, err := peer.CreateOffer()
	if err != nil {
		a.removePeer(peerID)
		return fmt.Errorf("creating offer: %w", err)
	}

	pubKey := config.PublicKey(a.cfg.Device.PrivateKey)
	return a.sigClient.Send(ctx, &protocol.OfferMessage{
		From:      a.cfg.Device.Name,
		To:        peerID,
		SDP:       offerSDP,
		PublicKey: pubKey.String(),
	})
}

// createRTCPeer creates and registers a new WebRTC peer connection.
func (a *Agent) createRTCPeer(ctx context.Context, peerID string) (*rtcpkg.Peer, error) {
	iceConfig := rtcpkg.ICEConfig{
		STUNServers: a.cfg.STUN.Servers,
	}

	peer, err := rtcpkg.NewPeer(rtcpkg.PeerConfig{
		ICE:      iceConfig,
		LocalID:  a.cfg.Device.Name,
		RemoteID: peerID,
		Logger:   a.log,

		OnICECandidate: func(candidate string) {
			if err := a.sigClient.Send(ctx, &protocol.ICECandidateMessage{
				From:      a.cfg.Device.Name,
				To:        peerID,
				Candidate: candidate,
			}); err != nil {
				a.log.Error("sending ICE candidate", "error", err)
			}
		},

		OnDataChannel: func(dc *webrtc.DataChannel) {
			a.onDataChannelOpen(peerID, dc)
		},

		OnConnectionStateChange: func(state webrtc.ICEConnectionState) {
			a.handleICEStateChange(ctx, peerID, state)
		},
	})
	if err != nil {
		return nil, err
	}

	a.mu.Lock()
	if existing, ok := a.peers[peerID]; ok {
		// Preserve fields (address, publicKey) that may have been
		// pre-populated from the peers list.
		existing.rtcPeer = peer
	} else {
		a.peers[peerID] = &peerState{rtcPeer: peer}
	}
	a.mu.Unlock()

	return peer, nil
}

// onDataChannelOpen is called when a data channel opens with a peer.
// It registers the data channel in the bridge Bind and adds the peer's
// WireGuard configuration.
func (a *Agent) onDataChannelOpen(peerID string, dc *webrtc.DataChannel) {
	a.log.Info("data channel open, bridging WireGuard", "peer_id", peerID)

	// Register the data channel in our custom Bind.
	a.bind.SetDataChannel(peerID, dc)

	// Track when this peer's data channel opened.
	a.mu.Lock()
	if ps, ok := a.peers[peerID]; ok {
		ps.connectedAt = time.Now()
	}
	a.mu.Unlock()

	// Look up the peer's WireGuard public key.
	a.mu.Lock()
	ps, ok := a.peers[peerID]
	a.mu.Unlock()

	if !ok || ps.publicKey.IsZero() {
		a.log.Warn("data channel opened but no WireGuard public key for peer",
			"peer_id", peerID)
		return
	}

	// Determine the peer's WireGuard allowed IPs from their tunnel address
	// and any additional routes they advertise.
	// The address comes from the peers list (e.g. "10.0.0.3/24"). We extract
	// the host IP and use a /32 so each peer only routes its own address.
	allowedIPs := []string{"0.0.0.0/0", "::/0"} // fallback if no address
	if ps.address != "" {
		ip, _, err := net.ParseCIDR(ps.address)
		if err != nil {
			a.log.Warn("invalid peer address, using default AllowedIPs",
				"peer_id", peerID, "address", ps.address, "error", err)
		} else {
			allowedIPs = []string{ip.String() + "/32"}

			// Append validated advertised routes from the peer if accept_routes is enabled.
			if a.cfg.Device.AcceptRoutes {
				for _, route := range ps.routes {
					if !isValidRoute(route) {
						a.log.Warn("ignoring invalid or dangerous route from peer",
							"peer_id", peerID, "route", route)
						continue
					}
					allowedIPs = append(allowedIPs, route)
				}
			} else if len(ps.routes) > 0 {
				a.log.Info("ignoring advertised routes from peer (accept_routes not enabled)",
					"peer_id", peerID, "routes", ps.routes)
			}

			a.log.Info("using peer-specific AllowedIPs",
				"peer_id", peerID, "allowed_ips", allowedIPs)
		}
	} else {
		a.log.Warn("peer has no tunnel address, using default AllowedIPs",
			"peer_id", peerID)
	}

	peerCfg := tunnel.PeerConfig{
		PublicKey:           ps.publicKey,
		Endpoint:            peerID,
		AllowedIPs:          allowedIPs,
		PersistentKeepalive: 25,
	}

	if err := a.wgDevice.AddPeer(peerCfg); err != nil {
		a.log.Error("adding WireGuard peer", "peer_id", peerID, "error", err)
	}

	// Add kernel routes for the peer's advertised subnets so the kernel
	// directs matching traffic into the TUN interface. Without these routes,
	// WireGuard has the AllowedIPs but the kernel doesn't know to send
	// packets to riftgate0.
	if a.cfg.Device.AcceptRoutes {
		for _, route := range ps.routes {
			if !isValidRoute(route) {
				continue
			}
			if err := tunnel.AddRoute(a.tunName, route); err != nil {
				a.log.Warn("adding route for peer", "peer_id", peerID, "route", route, "error", err)
			} else {
				a.log.Info("added route", "peer_id", peerID, "route", route, "dev", a.tunName)
			}
		}
	}
}

// removePeer tears down the WebRTC connection and WireGuard peer state.
func (a *Agent) removePeer(peerID string) {
	a.mu.Lock()
	ps, ok := a.peers[peerID]
	if !ok {
		a.mu.Unlock()
		return
	}
	// Stop any pending ICE restart timer.
	if ps.restartTimer != nil {
		ps.restartTimer.Stop()
		ps.restartTimer = nil
	}
	delete(a.peers, peerID)
	a.mu.Unlock()

	// Remove kernel routes for the peer's advertised subnets.
	for _, route := range ps.routes {
		if !isValidRoute(route) {
			continue
		}
		if err := tunnel.RemoveRoute(a.tunName, route); err != nil {
			a.log.Warn("removing route for peer", "peer_id", peerID, "route", route, "error", err)
		} else {
			a.log.Info("removed route", "peer_id", peerID, "route", route, "dev", a.tunName)
		}
	}

	// Remove the data channel from the bridge.
	a.bind.RemoveDataChannel(peerID)

	// Remove the WireGuard peer if we have their public key.
	if !ps.publicKey.IsZero() {
		if err := a.wgDevice.RemovePeer(ps.publicKey); err != nil {
			a.log.Error("removing WireGuard peer", "peer_id", peerID, "error", err)
		}
	}

	// Close the WebRTC peer connection.
	if ps.rtcPeer != nil {
		if err := ps.rtcPeer.Close(); err != nil {
			a.log.Error("closing WebRTC peer", "peer_id", peerID, "error", err)
		}
	}
}

// Status returns the current agent status for the control server.
func (a *Agent) Status() control.Status {
	a.mu.Lock()
	defer a.mu.Unlock()

	peers := make([]control.PeerStatus, 0, len(a.peers))
	for id, ps := range a.peers {
		peerStatus := control.PeerStatus{
			ID:      id,
			Address: ps.address,
			Routes:  ps.routes,
		}

		if ps.rtcPeer != nil {
			peerStatus.State = ps.rtcPeer.ConnectionState().String()
			peerStatus.ICEType = ps.rtcPeer.ICECandidateType()
		} else {
			peerStatus.State = "initializing"
		}

		if !ps.connectedAt.IsZero() {
			peerStatus.ConnectedSince = ps.connectedAt
		}

		peers = append(peers, peerStatus)
	}

	return control.Status{
		Device:        a.cfg.Device.Name,
		Address:       a.cfg.Device.Address,
		ServerURL:     a.cfg.Network.ServerURL,
		UptimeSeconds: time.Since(a.startedAt).Seconds(),
		Peers:         peers,
	}
}

// handleICEStateChange reacts to ICE connection state transitions for a peer.
// Instead of immediately removing a peer on failure, it attempts ICE restarts
// with a grace period for transient disconnections.
func (a *Agent) handleICEStateChange(ctx context.Context, peerID string, state webrtc.ICEConnectionState) {
	a.mu.Lock()
	ps, ok := a.peers[peerID]
	if !ok {
		a.mu.Unlock()
		return
	}

	switch state {
	case webrtc.ICEConnectionStateConnected, webrtc.ICEConnectionStateCompleted:
		// Connection (re-)established. Reset restart counter and cancel any
		// pending grace timer.
		ps.iceRestarts = 0
		if ps.restartTimer != nil {
			ps.restartTimer.Stop()
			ps.restartTimer = nil
		}
		a.mu.Unlock()
		a.log.Info("ICE connection established", "peer_id", peerID, "state", state.String())

	case webrtc.ICEConnectionStateDisconnected:
		// Transient disconnection — start a grace timer. ICE may reconnect
		// on its own (e.g. after a brief wifi dropout).
		if ps.restartTimer != nil {
			// Timer already running.
			a.mu.Unlock()
			return
		}
		ps.disconnectTime = time.Now()
		a.log.Warn("ICE disconnected, starting grace period",
			"peer_id", peerID, "grace", iceDisconnectGrace)
		ps.restartTimer = time.AfterFunc(iceDisconnectGrace, func() {
			a.attemptICERestart(ctx, peerID)
		})
		a.mu.Unlock()

	case webrtc.ICEConnectionStateFailed:
		// Hard failure — attempt restart immediately.
		if ps.restartTimer != nil {
			ps.restartTimer.Stop()
			ps.restartTimer = nil
		}
		a.mu.Unlock()
		a.log.Warn("ICE connection failed, attempting restart", "peer_id", peerID)
		a.attemptICERestart(ctx, peerID)

	default:
		a.mu.Unlock()
	}
}

// attemptICERestart tries to restart the ICE transport for a peer. If the
// maximum number of restart attempts is exceeded, the peer is removed.
func (a *Agent) attemptICERestart(ctx context.Context, peerID string) {
	a.mu.Lock()
	ps, ok := a.peers[peerID]
	if !ok {
		a.mu.Unlock()
		return
	}

	ps.iceRestarts++
	attempt := ps.iceRestarts
	ps.restartTimer = nil

	if attempt > maxICERestarts {
		a.mu.Unlock()
		a.log.Error("ICE restart attempts exhausted, removing peer",
			"peer_id", peerID, "attempts", attempt-1)
		a.removePeer(peerID)
		return
	}

	rtcPeer := ps.rtcPeer
	a.mu.Unlock()

	if rtcPeer == nil {
		return
	}

	a.log.Info("ICE restart attempt", "peer_id", peerID, "attempt", attempt, "max", maxICERestarts)

	offerSDP, err := rtcPeer.RestartICE()
	if err != nil {
		a.log.Error("ICE restart failed", "peer_id", peerID, "error", err)
		a.removePeer(peerID)
		return
	}

	// Send the restart offer through signaling.
	pubKey := config.PublicKey(a.cfg.Device.PrivateKey)
	if err := a.sigClient.Send(ctx, &protocol.OfferMessage{
		From:      a.cfg.Device.Name,
		To:        peerID,
		SDP:       offerSDP,
		PublicKey: pubKey.String(),
	}); err != nil {
		a.log.Error("sending ICE restart offer", "peer_id", peerID, "error", err)
		// Don't remove peer — signaling might reconnect and we can retry.
	}
}

// shutdown tears down all peer connections.
func (a *Agent) shutdown() {
	a.log.Info("shutting down agent")

	// Stop control server.
	if a.ctrlSrv != nil {
		if err := a.ctrlSrv.Stop(); err != nil {
			a.log.Error("stopping control server", "error", err)
		}
	}

	// Close signaling client.
	if a.sigClient != nil {
		if err := a.sigClient.Close(); err != nil {
			a.log.Error("closing signaling client", "error", err)
		}
	}

	// Close all peer connections.
	a.mu.Lock()
	peerIDs := make([]string, 0, len(a.peers))
	for id := range a.peers {
		peerIDs = append(peerIDs, id)
	}
	a.mu.Unlock()

	for _, id := range peerIDs {
		a.removePeer(id)
	}

	// Close the bridge bind.
	if a.bind != nil {
		if err := a.bind.Close(); err != nil {
			a.log.Error("closing bridge bind", "error", err)
		}
	}
}

// configureTUN configures the TUN interface with an IP address and brings it up.
// If the device has routes configured (advertising LAN subnets), it also enables
// IP forwarding and sets up NAT masquerading.
// Uses raw netlink syscalls so there is no dependency on the `ip` binary.
// Requires CAP_NET_ADMIN.
func (a *Agent) configureTUN(ifName string) error {
	addr := a.cfg.Device.Address
	if addr == "" {
		return fmt.Errorf("device address is not configured")
	}

	// Validate CIDR format.
	_, _, err := net.ParseCIDR(addr)
	if err != nil {
		return fmt.Errorf("invalid device address %q: %w", addr, err)
	}

	if err := tunnel.AddAddress(ifName, addr); err != nil {
		return fmt.Errorf("adding address to %s: %w", ifName, err)
	}

	if err := tunnel.SetLinkUp(ifName); err != nil {
		return fmt.Errorf("bringing up %s: %w", ifName, err)
	}

	a.log.Info("TUN interface configured", "name", ifName, "address", addr)

	// If this device advertises routes (e.g., 192.168.1.0/24), set up IP
	// forwarding and NAT so remote peers can reach devices on those subnets.
	if len(a.cfg.Device.Routes) > 0 {
		if err := a.setupForwardingAndNAT(ifName); err != nil {
			a.log.Warn("failed to set up forwarding/NAT (subnet routing may not work for remote peers)",
				"error", err)
			// Non-fatal: the tunnel itself still works for direct peer-to-peer traffic.
		}
	}

	return nil
}

// setupForwardingAndNAT enables IP forwarding on the TUN interface and the
// outgoing LAN interface for each advertised route, then sets up nftables
// MASQUERADE rules so forwarded traffic has the correct source address.
func (a *Agent) setupForwardingAndNAT(tunIface string) error {
	// Enable forwarding on the TUN interface.
	if err := a.enableForwarding(tunIface); err != nil {
		return fmt.Errorf("enabling forwarding on %s: %w", tunIface, err)
	}

	// Set up NAT manager.
	a.natManager = tunnel.NewNATManager(a.log)

	// For each advertised route, find the outgoing interface and set up
	// forwarding + masquerade.
	for _, route := range a.cfg.Device.Routes {
		if !isValidRoute(route) {
			a.log.Warn("skipping invalid route for forwarding setup", "route", route)
			continue
		}

		outIface, err := tunnel.FindInterfaceForSubnet(route)
		if err != nil {
			a.log.Warn("cannot find outgoing interface for route (masquerade not set up)",
				"route", route, "error", err)
			continue
		}

		// Enable forwarding on the outgoing interface.
		if err := a.enableForwarding(outIface); err != nil {
			a.log.Warn("enabling forwarding on outgoing interface",
				"interface", outIface, "route", route, "error", err)
			continue
		}

		// Set up masquerade: traffic from the WireGuard subnet going out
		// through the LAN interface gets source NAT'd.
		if err := a.natManager.SetupMasquerade(a.cfg.Device.Address, outIface); err != nil {
			return fmt.Errorf("setting up masquerade for %s via %s: %w", route, outIface, err)
		}

		a.log.Info("forwarding and NAT configured for route",
			"route", route, "out_iface", outIface, "tun_iface", tunIface)
	}

	return nil
}

// enableForwarding enables IPv4 forwarding on an interface, saving the previous
// state so it can be restored on shutdown.
func (a *Agent) enableForwarding(ifName string) error {
	// Check if we already saved state for this interface (avoid duplicates).
	for _, s := range a.forwardingState {
		if s.ifName == ifName {
			return nil // already handled
		}
	}

	// Read current state before changing it.
	wasEnabled, err := tunnel.GetForwarding(ifName)
	if err != nil {
		return fmt.Errorf("reading forwarding state for %s: %w", ifName, err)
	}

	a.forwardingState = append(a.forwardingState, forwardingSave{
		ifName:          ifName,
		previousEnabled: wasEnabled,
	})

	if wasEnabled {
		a.log.Debug("forwarding already enabled", "interface", ifName)
		return nil
	}

	if err := tunnel.SetForwarding(ifName, true); err != nil {
		return fmt.Errorf("enabling forwarding on %s: %w", ifName, err)
	}

	a.log.Info("enabled IPv4 forwarding", "interface", ifName)
	return nil
}

// cleanupForwardingAndNAT restores forwarding state and removes nftables rules.
func (a *Agent) cleanupForwardingAndNAT() {
	// Restore forwarding state for all modified interfaces.
	for _, s := range a.forwardingState {
		if s.previousEnabled {
			continue // was already enabled, don't disable
		}
		if err := tunnel.SetForwarding(s.ifName, false); err != nil {
			a.log.Warn("restoring forwarding state",
				"interface", s.ifName, "error", err)
		} else {
			a.log.Info("restored IPv4 forwarding state",
				"interface", s.ifName, "forwarding", false)
		}
	}
	a.forwardingState = nil

	// Remove nftables rules.
	if a.natManager != nil {
		if err := a.natManager.Cleanup(); err != nil {
			a.log.Warn("cleaning up nftables rules", "error", err)
		}
		a.natManager = nil
	}
}

// dangerousRoutes are CIDR prefixes that peers should never be allowed to
// advertise. Accepting 0.0.0.0/0 or ::/0 from a peer would override the
// default route, which is almost certainly unintended in a mesh VPN.
var dangerousRoutes = map[string]bool{
	"0.0.0.0/0": true,
	"::/0":      true,
}

// isValidRoute checks that a route string is a valid CIDR and not a
// dangerous catch-all route.
func isValidRoute(route string) bool {
	if dangerousRoutes[route] {
		return false
	}
	_, _, err := net.ParseCIDR(route)
	return err == nil
}
