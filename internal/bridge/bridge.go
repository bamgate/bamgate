// Package bridge implements a custom conn.Bind that transports WireGuard's
// encrypted packets over WebRTC data channels instead of UDP.
//
// This is the critical glue in riftgate's architecture:
//
//	App → kernel TUN → wireguard-go encrypts → Bind.Send → WebRTC data channel
//	WebRTC data channel → Bind.ReceiveFunc → wireguard-go decrypts → kernel TUN → App
//
// The Bind manages a set of data channels, one per remote peer. WireGuard
// calls Send with an Endpoint identifying the target peer, and the Bind
// routes the encrypted packet to the correct data channel. Incoming packets
// from any data channel are queued into a shared receive channel, which
// wireguard-go polls via the ReceiveFunc.
package bridge

import (
	"errors"
	"log/slog"
	"net"
	"net/netip"
	"sync"

	"github.com/pion/webrtc/v4"
	"golang.zx2c4.com/wireguard/conn"
)

// receivedPacket holds a packet received from a data channel, tagged with
// the endpoint (peer) it came from.
type receivedPacket struct {
	data []byte
	ep   *Endpoint
}

// Bind implements conn.Bind by transporting WireGuard packets over WebRTC
// data channels. It is safe for concurrent use.
type Bind struct {
	mu    sync.RWMutex
	peers map[string]*peerChannel // peerID -> data channel + endpoint
	log   *slog.Logger

	recvCh    chan receivedPacket
	closeCh   chan struct{}
	closeOnce sync.Once
}

// peerChannel associates a WebRTC data channel with its endpoint.
type peerChannel struct {
	dc *webrtc.DataChannel
	ep *Endpoint
}

// NewBind creates a new Bind. Call SetDataChannel to register data channels
// for each peer as WebRTC connections are established.
func NewBind(logger *slog.Logger) *Bind {
	if logger == nil {
		logger = slog.Default()
	}
	return &Bind{
		peers:   make(map[string]*peerChannel),
		log:     logger.With("component", "bridge"),
		recvCh:  make(chan receivedPacket, 256),
		closeCh: make(chan struct{}),
	}
}

// Open implements conn.Bind. It returns a single ReceiveFunc that reads
// packets from the shared receive channel. The port parameter is ignored
// since we don't use real UDP.
//
// wireguard-go calls Close then Open during BindUpdate cycles, so Open
// must reset the close channel to allow the new ReceiveFunc to block.
func (b *Bind) Open(port uint16) ([]conn.ReceiveFunc, uint16, error) {
	// Reset the close state so the ReceiveFunc can block on the new closeCh.
	// This handles the Close→Open cycle that wireguard-go performs during BindUpdate.
	b.closeOnce = sync.Once{}
	b.closeCh = make(chan struct{})

	fn := func(packets [][]byte, sizes []int, eps []conn.Endpoint) (int, error) {
		select {
		case pkt, ok := <-b.recvCh:
			if !ok {
				return 0, net.ErrClosed
			}
			n := copy(packets[0], pkt.data)
			sizes[0] = n
			eps[0] = pkt.ep
			return 1, nil
		case <-b.closeCh:
			return 0, net.ErrClosed
		}
	}

	return []conn.ReceiveFunc{fn}, 0, nil
}

// Close implements conn.Bind. It signals all pending receives to unblock.
func (b *Bind) Close() error {
	b.closeOnce.Do(func() {
		close(b.closeCh)
	})
	return nil
}

// Send implements conn.Bind. It sends WireGuard encrypted packets to the
// peer identified by the endpoint, via their WebRTC data channel.
func (b *Bind) Send(bufs [][]byte, ep conn.Endpoint) error {
	endpoint, ok := ep.(*Endpoint)
	if !ok {
		return errors.New("invalid endpoint type")
	}

	b.mu.RLock()
	pc, exists := b.peers[endpoint.peerID]
	b.mu.RUnlock()

	if !exists {
		return errors.New("no data channel for peer: " + endpoint.peerID)
	}

	for _, buf := range bufs {
		if err := pc.dc.Send(buf); err != nil {
			return err
		}
	}

	return nil
}

// ParseEndpoint implements conn.Bind. It parses a peer ID string into an
// Endpoint. WireGuard calls this when configuring peer endpoints.
func (b *Bind) ParseEndpoint(s string) (conn.Endpoint, error) {
	return NewEndpoint(s), nil
}

// SetMark implements conn.Bind. No-op since we don't use real sockets.
func (b *Bind) SetMark(mark uint32) error {
	return nil
}

// BatchSize implements conn.Bind. We process one packet at a time.
func (b *Bind) BatchSize() int {
	return 1
}

// SetDataChannel registers a WebRTC data channel for a peer. Incoming
// messages on the data channel are queued into the receive channel for
// wireguard-go to process. This must be called when a data channel opens.
func (b *Bind) SetDataChannel(peerID string, dc *webrtc.DataChannel) {
	ep := NewEndpoint(peerID)

	b.mu.Lock()
	b.peers[peerID] = &peerChannel{dc: dc, ep: ep}
	b.mu.Unlock()

	dc.OnMessage(func(msg webrtc.DataChannelMessage) {
		// Copy the data — the underlying buffer may be reused by pion.
		data := make([]byte, len(msg.Data))
		copy(data, msg.Data)

		select {
		case b.recvCh <- receivedPacket{data: data, ep: ep}:
		case <-b.closeCh:
		default:
			// Drop packet if receive channel is full. This mimics UDP
			// behavior — WireGuard handles packet loss gracefully.
			b.log.Debug("dropping packet, receive buffer full", "peer_id", peerID)
		}
	})

	b.log.Info("data channel registered", "peer_id", peerID)
}

// RemoveDataChannel unregisters the data channel for a peer. Packets from
// this peer will no longer be delivered to wireguard-go.
func (b *Bind) RemoveDataChannel(peerID string) {
	b.mu.Lock()
	delete(b.peers, peerID)
	b.mu.Unlock()

	b.log.Info("data channel removed", "peer_id", peerID)
}

// Reset prepares the Bind for reuse after a Close. This is called
// automatically by Open, but is available for explicit use in tests.
func (b *Bind) Reset() {
	b.closeOnce = sync.Once{}
	b.closeCh = make(chan struct{})
}

// Endpoint implements conn.Endpoint for a WebRTC peer. It identifies the
// target peer by their peer ID string.
type Endpoint struct {
	peerID string
}

// NewEndpoint creates an Endpoint for the given peer ID.
func NewEndpoint(peerID string) *Endpoint {
	return &Endpoint{peerID: peerID}
}

// PeerID returns the peer identifier.
func (e *Endpoint) PeerID() string {
	return e.peerID
}

// ClearSrc implements conn.Endpoint. No-op.
func (e *Endpoint) ClearSrc() {}

// SrcToString implements conn.Endpoint. Returns empty — no source address
// concept for WebRTC transport.
func (e *Endpoint) SrcToString() string { return "" }

// DstToString implements conn.Endpoint. Returns the peer ID as the destination.
func (e *Endpoint) DstToString() string { return e.peerID }

// DstToBytes implements conn.Endpoint. Returns the peer ID as bytes (used
// for MAC2 cookie calculations — not relevant for our use case).
func (e *Endpoint) DstToBytes() []byte { return []byte(e.peerID) }

// DstIP implements conn.Endpoint. Returns a zero address — we don't use
// real IP endpoints.
func (e *Endpoint) DstIP() netip.Addr { return netip.Addr{} }

// SrcIP implements conn.Endpoint. Returns a zero address.
func (e *Endpoint) SrcIP() netip.Addr { return netip.Addr{} }
