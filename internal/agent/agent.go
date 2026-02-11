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
	"os/exec"
	"strings"
	"sync"

	"github.com/pion/webrtc/v4"

	"github.com/kuuji/riftgate/internal/bridge"
	"github.com/kuuji/riftgate/internal/config"
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
	sigClient *signaling.Client

	mu    sync.Mutex
	peers map[string]*peerState // peerID -> state
}

// peerState tracks the state of a single remote peer.
type peerState struct {
	rtcPeer   *rtcpkg.Peer
	publicKey config.Key // WireGuard public key
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

	// 4. Configure the TUN interface IP address and bring it up.
	if err := a.configureTUN(actualName); err != nil {
		return fmt.Errorf("configuring TUN interface: %w", err)
	}

	// 5. Connect to signaling server.
	pubKey := config.PublicKey(a.cfg.Device.PrivateKey)
	a.sigClient = signaling.NewClient(signaling.ClientConfig{
		ServerURL: a.cfg.Network.ServerURL,
		PeerID:    a.cfg.Device.Name,
		PublicKey: pubKey.String(),
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
		a.log.Info("discovered peer", "peer_id", p.PeerID, "public_key", p.PublicKey)

		// Determine who offers: the peer with the smaller ID.
		if a.cfg.Device.Name < p.PeerID {
			if err := a.initiateConnection(ctx, p.PeerID, p.PublicKey); err != nil {
				a.log.Error("initiating connection", "peer_id", p.PeerID, "error", err)
			}
		}
		// Otherwise, the remote peer will send us an offer.
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
func (a *Agent) initiateConnection(ctx context.Context, peerID, publicKey string) error {
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

	// Store the WireGuard public key.
	a.mu.Lock()
	if ps, ok := a.peers[peerID]; ok {
		ps.publicKey = wgPubKey
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
			if state == webrtc.ICEConnectionStateFailed ||
				state == webrtc.ICEConnectionStateDisconnected {
				a.log.Warn("ICE connection failed/disconnected, removing peer",
					"peer_id", peerID, "state", state.String())
				a.removePeer(peerID)
			}
		},
	})
	if err != nil {
		return nil, err
	}

	a.mu.Lock()
	a.peers[peerID] = &peerState{rtcPeer: peer}
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

	// Look up the peer's WireGuard public key.
	a.mu.Lock()
	ps, ok := a.peers[peerID]
	a.mu.Unlock()

	if !ok || ps.publicKey.IsZero() {
		a.log.Warn("data channel opened but no WireGuard public key for peer",
			"peer_id", peerID)
		return
	}

	// Determine the peer's WireGuard allowed IPs. For now, we allow the
	// peer's specific address. The address is derived from the peer's
	// position in the config or could be exchanged via signaling.
	// For Phase 2 simplicity, we allow all traffic (0.0.0.0/0) and
	// the tunnel subnet. In production this should be more restrictive.
	peerCfg := tunnel.PeerConfig{
		PublicKey:           ps.publicKey,
		Endpoint:            peerID,
		AllowedIPs:          []string{"0.0.0.0/0", "::/0"},
		PersistentKeepalive: 25,
	}

	if err := a.wgDevice.AddPeer(peerCfg); err != nil {
		a.log.Error("adding WireGuard peer", "peer_id", peerID, "error", err)
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
	delete(a.peers, peerID)
	a.mu.Unlock()

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

// shutdown tears down all peer connections.
func (a *Agent) shutdown() {
	a.log.Info("shutting down agent")

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
// It shells out to the `ip` command since this runs as root anyway (TUN creation
// requires CAP_NET_ADMIN). A netlink dependency can replace this later.
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

	// ip addr add <address> dev <ifName>
	if out, err := exec.Command("ip", "addr", "add", addr, "dev", ifName).CombinedOutput(); err != nil {
		return fmt.Errorf("ip addr add: %w: %s", err, strings.TrimSpace(string(out)))
	}

	// ip link set <ifName> up
	if out, err := exec.Command("ip", "link", "set", ifName, "up").CombinedOutput(); err != nil {
		return fmt.Errorf("ip link set up: %w: %s", err, strings.TrimSpace(string(out)))
	}

	a.log.Info("TUN interface configured", "name", ifName, "address", addr)
	return nil
}
