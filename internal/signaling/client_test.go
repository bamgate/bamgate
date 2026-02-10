package signaling

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
)

// testHub is an in-memory signaling hub for testing. It accepts WebSocket
// connections, tracks connected peers, and relays signaling messages between them.
type testHub struct {
	mu     sync.Mutex
	peers  map[string]*testPeer // peerId -> peer
	ctx    context.Context
	cancel context.CancelFunc
}

type testPeer struct {
	id        string
	publicKey string
	conn      *websocket.Conn
}

func newTestHub() *testHub {
	ctx, cancel := context.WithCancel(context.Background())
	return &testHub{
		peers:  make(map[string]*testPeer),
		ctx:    ctx,
		cancel: cancel,
	}
}

// CloseAllConnections forcefully closes all peer WebSocket connections,
// causing client reads to error immediately.
func (h *testHub) CloseAllConnections() {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, p := range h.peers {
		p.conn.Close(websocket.StatusGoingAway, "server shutting down")
	}
	h.cancel()
}

func (h *testHub) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	conn, err := websocket.Accept(w, r, nil)
	if err != nil {
		return
	}
	defer conn.Close(websocket.StatusNormalClosure, "")

	// Use the hub's context so we can force-cancel all reads on shutdown.
	ctx := h.ctx

	// Read the first message, which must be a join.
	_, data, err := conn.Read(ctx)
	if err != nil {
		return
	}

	msg, err := Unmarshal(data)
	if err != nil {
		return
	}

	join, ok := msg.(*JoinMessage)
	if !ok {
		return
	}

	peer := &testPeer{
		id:        join.PeerID,
		publicKey: join.PublicKey,
		conn:      conn,
	}

	// Send the current peers list to the new peer.
	h.mu.Lock()
	var peerInfos []PeerInfo
	for _, p := range h.peers {
		peerInfos = append(peerInfos, PeerInfo{PeerID: p.id, PublicKey: p.publicKey})
	}
	h.peers[peer.id] = peer
	h.mu.Unlock()

	peersMsg := &PeersMessage{Peers: peerInfos}
	if pData, err := Marshal(peersMsg); err == nil {
		_ = conn.Write(ctx, websocket.MessageText, pData)
	}

	// Handle messages until disconnect.
	defer func() {
		h.mu.Lock()
		delete(h.peers, peer.id)
		remaining := make([]*testPeer, 0, len(h.peers))
		for _, p := range h.peers {
			remaining = append(remaining, p)
		}
		h.mu.Unlock()

		// Notify remaining peers about the departure.
		leftMsg := &PeerLeftMessage{PeerID: peer.id}
		leftData, err := Marshal(leftMsg)
		if err != nil {
			return
		}
		for _, p := range remaining {
			_ = p.conn.Write(context.Background(), websocket.MessageText, leftData)
		}
	}()

	for {
		_, data, err := conn.Read(ctx)
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
			}
		}
	}
}

// startTestHub starts an httptest.Server running the test hub and returns
// the server and a ws:// URL suitable for the signaling client.
func startTestHub(t *testing.T) (*httptest.Server, string) {
	t.Helper()
	hub := newTestHub()
	srv := httptest.NewServer(hub)
	t.Cleanup(func() {
		hub.CloseAllConnections()
		srv.Close()
	})

	// Convert http:// to ws://
	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http")
	return srv, wsURL
}

// receiveTimeout reads a message from the channel with a timeout.
func receiveTimeout(t *testing.T, ch <-chan Message, timeout time.Duration) Message {
	t.Helper()
	select {
	case msg, ok := <-ch:
		if !ok {
			t.Fatal("message channel closed unexpectedly")
		}
		return msg
	case <-time.After(timeout):
		t.Fatal("timed out waiting for message")
		return nil
	}
}

// expectNoMessage asserts that no message arrives within the given duration.
func expectNoMessage(t *testing.T, ch <-chan Message, duration time.Duration) {
	t.Helper()
	select {
	case msg := <-ch:
		t.Fatalf("unexpected message: %T %+v", msg, msg)
	case <-time.After(duration):
		// OK — no message received.
	}
}

func TestClient_ConnectAndJoin(t *testing.T) {
	t.Parallel()

	_, wsURL := startTestHub(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	client := NewClient(ClientConfig{
		ServerURL: wsURL,
		PeerID:    "peer-a",
		PublicKey: "key-a",
	})

	if err := client.Connect(ctx); err != nil {
		t.Fatalf("Connect() error: %v", err)
	}
	defer client.Close()

	// The hub should send a peers message (empty list since we're the first).
	msg := receiveTimeout(t, client.Messages(), 2*time.Second)
	peers, ok := msg.(*PeersMessage)
	if !ok {
		t.Fatalf("expected *PeersMessage, got %T", msg)
	}
	if len(peers.Peers) != 0 {
		t.Errorf("expected 0 peers, got %d", len(peers.Peers))
	}
}

func TestClient_TwoPeers_ExchangeOffer(t *testing.T) {
	t.Parallel()

	_, wsURL := startTestHub(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Connect peer A.
	clientA := NewClient(ClientConfig{
		ServerURL: wsURL,
		PeerID:    "peer-a",
		PublicKey: "key-a",
	})
	if err := clientA.Connect(ctx); err != nil {
		t.Fatalf("clientA.Connect() error: %v", err)
	}
	defer clientA.Close()

	// Drain the initial peers message for A (empty list).
	receiveTimeout(t, clientA.Messages(), 2*time.Second)

	// Connect peer B.
	clientB := NewClient(ClientConfig{
		ServerURL: wsURL,
		PeerID:    "peer-b",
		PublicKey: "key-b",
	})
	if err := clientB.Connect(ctx); err != nil {
		t.Fatalf("clientB.Connect() error: %v", err)
	}
	defer clientB.Close()

	// B receives a peers list containing A.
	msg := receiveTimeout(t, clientB.Messages(), 2*time.Second)
	peers, ok := msg.(*PeersMessage)
	if !ok {
		t.Fatalf("expected *PeersMessage, got %T", msg)
	}
	if len(peers.Peers) != 1 || peers.Peers[0].PeerID != "peer-a" {
		t.Errorf("unexpected peers list: %+v", peers.Peers)
	}

	// A sends an offer to B.
	offer := &OfferMessage{From: "peer-a", To: "peer-b", SDP: "v=0\r\noffer-sdp"}
	if err := clientA.Send(ctx, offer); err != nil {
		t.Fatalf("Send(offer) error: %v", err)
	}

	// B receives the offer.
	msg = receiveTimeout(t, clientB.Messages(), 2*time.Second)
	gotOffer, ok := msg.(*OfferMessage)
	if !ok {
		t.Fatalf("expected *OfferMessage, got %T", msg)
	}
	if gotOffer.From != "peer-a" || gotOffer.SDP != "v=0\r\noffer-sdp" {
		t.Errorf("unexpected offer: %+v", gotOffer)
	}

	// B sends an answer back to A.
	answer := &AnswerMessage{From: "peer-b", To: "peer-a", SDP: "v=0\r\nanswer-sdp"}
	if err := clientB.Send(ctx, answer); err != nil {
		t.Fatalf("Send(answer) error: %v", err)
	}

	// A receives the answer.
	msg = receiveTimeout(t, clientA.Messages(), 2*time.Second)
	gotAnswer, ok := msg.(*AnswerMessage)
	if !ok {
		t.Fatalf("expected *AnswerMessage, got %T", msg)
	}
	if gotAnswer.From != "peer-b" || gotAnswer.SDP != "v=0\r\nanswer-sdp" {
		t.Errorf("unexpected answer: %+v", gotAnswer)
	}
}

func TestClient_TwoPeers_ExchangeICECandidate(t *testing.T) {
	t.Parallel()

	_, wsURL := startTestHub(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	clientA := NewClient(ClientConfig{
		ServerURL: wsURL,
		PeerID:    "peer-a",
		PublicKey: "key-a",
	})
	if err := clientA.Connect(ctx); err != nil {
		t.Fatalf("clientA.Connect() error: %v", err)
	}
	defer clientA.Close()
	receiveTimeout(t, clientA.Messages(), 2*time.Second) // drain peers

	clientB := NewClient(ClientConfig{
		ServerURL: wsURL,
		PeerID:    "peer-b",
		PublicKey: "key-b",
	})
	if err := clientB.Connect(ctx); err != nil {
		t.Fatalf("clientB.Connect() error: %v", err)
	}
	defer clientB.Close()
	receiveTimeout(t, clientB.Messages(), 2*time.Second) // drain peers

	// A sends an ICE candidate to B.
	candidate := &ICECandidateMessage{
		From:      "peer-a",
		To:        "peer-b",
		Candidate: "candidate:1 1 udp 2130706431 192.168.1.1 5000 typ host",
	}
	if err := clientA.Send(ctx, candidate); err != nil {
		t.Fatalf("Send(ice-candidate) error: %v", err)
	}

	// B receives the ICE candidate.
	msg := receiveTimeout(t, clientB.Messages(), 2*time.Second)
	gotCandidate, ok := msg.(*ICECandidateMessage)
	if !ok {
		t.Fatalf("expected *ICECandidateMessage, got %T", msg)
	}
	if gotCandidate.From != "peer-a" || gotCandidate.Candidate != candidate.Candidate {
		t.Errorf("unexpected ICE candidate: %+v", gotCandidate)
	}
}

func TestClient_PeerLeft(t *testing.T) {
	t.Parallel()

	_, wsURL := startTestHub(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	clientA := NewClient(ClientConfig{
		ServerURL: wsURL,
		PeerID:    "peer-a",
		PublicKey: "key-a",
	})
	if err := clientA.Connect(ctx); err != nil {
		t.Fatalf("clientA.Connect() error: %v", err)
	}
	defer clientA.Close()
	receiveTimeout(t, clientA.Messages(), 2*time.Second) // drain peers

	clientB := NewClient(ClientConfig{
		ServerURL: wsURL,
		PeerID:    "peer-b",
		PublicKey: "key-b",
	})
	if err := clientB.Connect(ctx); err != nil {
		t.Fatalf("clientB.Connect() error: %v", err)
	}
	receiveTimeout(t, clientB.Messages(), 2*time.Second) // drain peers

	// Close B — A should receive a peer-left notification.
	clientB.Close()

	msg := receiveTimeout(t, clientA.Messages(), 2*time.Second)
	peerLeft, ok := msg.(*PeerLeftMessage)
	if !ok {
		t.Fatalf("expected *PeerLeftMessage, got %T", msg)
	}
	if peerLeft.PeerID != "peer-b" {
		t.Errorf("expected peer-b left, got %q", peerLeft.PeerID)
	}
}

func TestClient_Reconnect(t *testing.T) {
	t.Parallel()

	hub := newTestHub()
	srv := httptest.NewServer(hub)
	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http")

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	client := NewClient(ClientConfig{
		ServerURL:   wsURL,
		PeerID:      "peer-a",
		PublicKey:   "key-a",
		DialTimeout: 500 * time.Millisecond,
		Reconnect: ReconnectConfig{
			Enabled:      true,
			InitialDelay: 50 * time.Millisecond,
			MaxDelay:     200 * time.Millisecond,
			MaxAttempts:  3,
		},
	})

	if err := client.Connect(ctx); err != nil {
		t.Fatalf("Connect() error: %v", err)
	}
	defer client.Close()

	// Drain the initial peers message.
	receiveTimeout(t, client.Messages(), 2*time.Second)

	// Force-close all connections, then shut down the server.
	// This ensures the client's Read() returns an error immediately.
	hub.CloseAllConnections()
	srv.Close()

	// The client should detect the disconnect, attempt reconnection
	// (which will fail since the server is down), and eventually
	// exhaust its attempts and close the message channel.
	select {
	case _, ok := <-client.Messages():
		if ok {
			// Drain any remaining messages.
			for range client.Messages() {
			}
		}
		// Channel closed — reconnection was attempted and exhausted.
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for client to exhaust reconnection attempts")
	}
}

func TestClient_ContextCancellation(t *testing.T) {
	t.Parallel()

	_, wsURL := startTestHub(t)
	ctx, cancel := context.WithCancel(context.Background())

	client := NewClient(ClientConfig{
		ServerURL: wsURL,
		PeerID:    "peer-a",
		PublicKey: "key-a",
	})

	if err := client.Connect(ctx); err != nil {
		t.Fatalf("Connect() error: %v", err)
	}

	// Drain the initial peers message.
	receiveTimeout(t, client.Messages(), 2*time.Second)

	// Cancel the context — the client should shut down gracefully.
	cancel()

	// The message channel should be closed.
	select {
	case _, ok := <-client.Messages():
		if ok {
			// Drain any remaining messages.
			for range client.Messages() {
			}
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for message channel to close after context cancellation")
	}

	// Close should return immediately since the receive loop already stopped.
	if err := client.Close(); err != nil {
		t.Fatalf("Close() error: %v", err)
	}
}

func TestClient_SendWithoutConnect(t *testing.T) {
	t.Parallel()

	client := NewClient(ClientConfig{
		ServerURL: "ws://localhost:0/bogus",
		PeerID:    "peer-a",
		PublicKey: "key-a",
	})

	ctx := context.Background()
	err := client.Send(ctx, &JoinMessage{PeerID: "peer-a", PublicKey: "key-a"})
	if err == nil {
		t.Fatal("expected error sending without connection, got nil")
	}
}

func TestClient_ConnectToUnreachableServer(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	client := NewClient(ClientConfig{
		ServerURL: "ws://127.0.0.1:1/bogus",
		PeerID:    "peer-a",
		PublicKey: "key-a",
	})

	err := client.Connect(ctx)
	if err == nil {
		t.Fatal("expected error connecting to unreachable server, got nil")
	}
}

func TestClient_MultiplePeers_FullExchange(t *testing.T) {
	t.Parallel()

	_, wsURL := startTestHub(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Connect three peers.
	clients := make([]*Client, 3)
	for i := range clients {
		clients[i] = NewClient(ClientConfig{
			ServerURL: wsURL,
			PeerID:    fmt.Sprintf("peer-%d", i),
			PublicKey: fmt.Sprintf("key-%d", i),
		})
		if err := clients[i].Connect(ctx); err != nil {
			t.Fatalf("client[%d].Connect() error: %v", i, err)
		}
		defer clients[i].Close()

		// Drain the initial peers message.
		receiveTimeout(t, clients[i].Messages(), 2*time.Second)
	}

	// peer-0 sends an offer to peer-2.
	offer := &OfferMessage{From: "peer-0", To: "peer-2", SDP: "sdp-from-0-to-2"}
	if err := clients[0].Send(ctx, offer); err != nil {
		t.Fatalf("Send(offer) error: %v", err)
	}

	// peer-2 should receive it. peer-1 should NOT.
	msg := receiveTimeout(t, clients[2].Messages(), 2*time.Second)
	gotOffer, ok := msg.(*OfferMessage)
	if !ok {
		t.Fatalf("expected *OfferMessage, got %T", msg)
	}
	if gotOffer.From != "peer-0" || gotOffer.SDP != "sdp-from-0-to-2" {
		t.Errorf("unexpected offer: %+v", gotOffer)
	}

	// Verify peer-1 does not receive the message.
	expectNoMessage(t, clients[1].Messages(), 200*time.Millisecond)
}
