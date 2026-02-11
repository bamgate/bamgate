package signaling

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"sync"

	"github.com/coder/websocket"
)

// Hub is a signaling server that relays WebRTC signaling messages between
// connected peers. It accepts WebSocket connections, tracks peer presence,
// and forwards SDP offers/answers and ICE candidates between peers.
//
// Hub implements http.Handler and can be used with any HTTP server.
// It is used for local/LAN testing (via cmd/riftgate-hub) and will be
// replaced by the Cloudflare Worker in production.
type Hub struct {
	mu     sync.Mutex
	peers  map[string]*hubPeer
	log    *slog.Logger
	ctx    context.Context
	cancel context.CancelFunc
}

type hubPeer struct {
	id        string
	publicKey string
	conn      *websocket.Conn
}

// NewHub creates a new signaling Hub.
func NewHub(logger *slog.Logger) *Hub {
	if logger == nil {
		logger = slog.Default()
	}
	ctx, cancel := context.WithCancel(context.Background())
	return &Hub{
		peers:  make(map[string]*hubPeer),
		log:    logger.With("component", "hub"),
		ctx:    ctx,
		cancel: cancel,
	}
}

// Close shuts down the hub, forcefully closing all peer connections.
func (h *Hub) Close() {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, p := range h.peers {
		// Ignore close errors — peers may already be disconnected.
		_ = p.conn.Close(websocket.StatusGoingAway, "server shutting down")
	}
	h.cancel()
}

// ServeHTTP implements http.Handler. Each request is expected to be a
// WebSocket upgrade. The first message must be a JoinMessage.
func (h *Hub) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	c, err := websocket.Accept(w, r, nil)
	if err != nil {
		h.log.Warn("WebSocket accept failed", "error", err)
		return
	}
	defer func() {
		_ = c.Close(websocket.StatusNormalClosure, "")
	}()

	ctx := h.ctx

	// Read the first message, which must be a join.
	_, data, err := c.Read(ctx)
	if err != nil {
		return
	}

	msg, err := Unmarshal(data)
	if err != nil {
		h.log.Warn("malformed join message", "error", err)
		return
	}

	join, ok := msg.(*JoinMessage)
	if !ok {
		h.log.Warn("first message is not join", "type", msg.MessageType())
		return
	}

	peer := &hubPeer{
		id:        join.PeerID,
		publicKey: join.PublicKey,
		conn:      c,
	}

	h.log.Info("peer joined", "peer_id", peer.id)

	// Send the current peers list to the new peer.
	h.mu.Lock()
	var peerInfos []PeerInfo
	for _, p := range h.peers {
		peerInfos = append(peerInfos, PeerInfo{PeerID: p.id, PublicKey: p.publicKey})
	}
	h.peers[peer.id] = peer
	h.mu.Unlock()

	peersMsg := &PeersMessage{Peers: peerInfos}
	if pData, mErr := Marshal(peersMsg); mErr == nil {
		_ = c.Write(ctx, websocket.MessageText, pData)
	}

	// Notify existing peers about the new arrival. We send a PeersMessage
	// containing only the new peer so existing agents learn its ID and
	// public key and can initiate a WebRTC connection.
	newPeerMsg := &PeersMessage{Peers: []PeerInfo{{PeerID: peer.id, PublicKey: peer.publicKey}}}
	if npData, mErr := Marshal(newPeerMsg); mErr == nil {
		h.mu.Lock()
		for _, p := range h.peers {
			if p.id == peer.id {
				continue
			}
			_ = p.conn.Write(ctx, websocket.MessageText, npData)
		}
		h.mu.Unlock()
	}

	// Handle messages until disconnect.
	defer func() {
		h.mu.Lock()
		delete(h.peers, peer.id)
		remaining := make([]*hubPeer, 0, len(h.peers))
		for _, p := range h.peers {
			remaining = append(remaining, p)
		}
		h.mu.Unlock()

		h.log.Info("peer left", "peer_id", peer.id)

		// Notify remaining peers about the departure.
		leftMsg := &PeerLeftMessage{PeerID: peer.id}
		leftData, mErr := Marshal(leftMsg)
		if mErr != nil {
			return
		}
		for _, p := range remaining {
			_ = p.conn.Write(context.Background(), websocket.MessageText, leftData)
		}
	}()

	for {
		_, data, err := c.Read(ctx)
		if err != nil {
			return
		}

		// Parse the message to find the target peer.
		var env struct {
			Type string `json:"type"`
			To   string `json:"to"`
		}
		if err := json.Unmarshal(data, &env); err != nil {
			continue
		}

		switch env.Type {
		case "offer", "answer", "ice-candidate":
			h.mu.Lock()
			target, ok := h.peers[env.To]
			h.mu.Unlock()
			if ok {
				_ = target.conn.Write(ctx, websocket.MessageText, data)
			} else {
				h.log.Debug("target peer not found", "type", env.Type, "to", env.To)
			}
		}
	}
}
