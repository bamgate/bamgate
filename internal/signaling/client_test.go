package signaling

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/kuuji/bamgate/pkg/protocol"
)

// startTestHub starts an httptest.Server running the signaling Hub and returns
// the server and a ws:// URL suitable for the signaling client.
func startTestHub(t *testing.T) (*httptest.Server, string) {
	t.Helper()
	hub := NewHub(nil)
	srv := httptest.NewServer(hub)
	t.Cleanup(func() {
		hub.Close()
		srv.Close()
	})

	// Convert http:// to ws://
	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http")
	return srv, wsURL
}

// receiveTimeout reads a message from the channel with a timeout.
func receiveTimeout(t *testing.T, ch <-chan protocol.Message, timeout time.Duration) protocol.Message {
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
func expectNoMessage(t *testing.T, ch <-chan protocol.Message, duration time.Duration) {
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
	peers, ok := msg.(*protocol.PeersMessage)
	if !ok {
		t.Fatalf("expected *protocol.PeersMessage, got %T", msg)
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
	peers, ok := msg.(*protocol.PeersMessage)
	if !ok {
		t.Fatalf("expected *protocol.PeersMessage, got %T", msg)
	}
	if len(peers.Peers) != 1 || peers.Peers[0].PeerID != "peer-a" {
		t.Errorf("unexpected peers list: %+v", peers.Peers)
	}

	// A receives a join notification for B.
	msg = receiveTimeout(t, clientA.Messages(), 2*time.Second)
	joinNotify, ok := msg.(*protocol.PeersMessage)
	if !ok {
		t.Fatalf("expected *protocol.PeersMessage (join notification), got %T", msg)
	}
	if len(joinNotify.Peers) != 1 || joinNotify.Peers[0].PeerID != "peer-b" {
		t.Errorf("unexpected join notification: %+v", joinNotify.Peers)
	}

	// A sends an offer to B.
	offer := &protocol.OfferMessage{From: "peer-a", To: "peer-b", SDP: "v=0\r\noffer-sdp"}
	if err := clientA.Send(ctx, offer); err != nil {
		t.Fatalf("Send(offer) error: %v", err)
	}

	// B receives the offer.
	msg = receiveTimeout(t, clientB.Messages(), 2*time.Second)
	gotOffer, ok := msg.(*protocol.OfferMessage)
	if !ok {
		t.Fatalf("expected *protocol.OfferMessage, got %T", msg)
	}
	if gotOffer.From != "peer-a" || gotOffer.SDP != "v=0\r\noffer-sdp" {
		t.Errorf("unexpected offer: %+v", gotOffer)
	}

	// B sends an answer back to A.
	answer := &protocol.AnswerMessage{From: "peer-b", To: "peer-a", SDP: "v=0\r\nanswer-sdp"}
	if err := clientB.Send(ctx, answer); err != nil {
		t.Fatalf("Send(answer) error: %v", err)
	}

	// A receives the answer.
	msg = receiveTimeout(t, clientA.Messages(), 2*time.Second)
	gotAnswer, ok := msg.(*protocol.AnswerMessage)
	if !ok {
		t.Fatalf("expected *protocol.AnswerMessage, got %T", msg)
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
	receiveTimeout(t, clientB.Messages(), 2*time.Second) // drain peers list for B
	receiveTimeout(t, clientA.Messages(), 2*time.Second) // drain B's join notification on A

	// A sends an ICE candidate to B.
	candidate := &protocol.ICECandidateMessage{
		From:      "peer-a",
		To:        "peer-b",
		Candidate: "candidate:1 1 udp 2130706431 192.168.1.1 5000 typ host",
	}
	if err := clientA.Send(ctx, candidate); err != nil {
		t.Fatalf("Send(ice-candidate) error: %v", err)
	}

	// B receives the ICE candidate.
	msg := receiveTimeout(t, clientB.Messages(), 2*time.Second)
	gotCandidate, ok := msg.(*protocol.ICECandidateMessage)
	if !ok {
		t.Fatalf("expected *protocol.ICECandidateMessage, got %T", msg)
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
	receiveTimeout(t, clientB.Messages(), 2*time.Second) // drain peers list for B
	receiveTimeout(t, clientA.Messages(), 2*time.Second) // drain B's join notification on A

	// Close B — A should receive a peer-left notification.
	clientB.Close()

	msg := receiveTimeout(t, clientA.Messages(), 2*time.Second)
	peerLeft, ok := msg.(*protocol.PeerLeftMessage)
	if !ok {
		t.Fatalf("expected *protocol.PeerLeftMessage, got %T", msg)
	}
	if peerLeft.PeerID != "peer-b" {
		t.Errorf("expected peer-b left, got %q", peerLeft.PeerID)
	}
}

func TestClient_Reconnect(t *testing.T) {
	t.Parallel()

	hub := NewHub(nil)
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
	hub.Close()
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
	err := client.Send(ctx, &protocol.JoinMessage{PeerID: "peer-a", PublicKey: "key-a"})
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

	// Connect three peers sequentially, draining all signaling messages
	// (initial peers list + join notifications to existing peers) after each.
	clients := make([]*Client, 3)

	// peer-0 joins — gets empty peers list, no one to notify.
	clients[0] = NewClient(ClientConfig{
		ServerURL: wsURL,
		PeerID:    "peer-0",
		PublicKey: "key-0",
	})
	if err := clients[0].Connect(ctx); err != nil {
		t.Fatalf("client[0].Connect() error: %v", err)
	}
	defer clients[0].Close()
	receiveTimeout(t, clients[0].Messages(), 2*time.Second) // peers list (empty)

	// peer-1 joins — gets peers list with peer-0. peer-0 gets join notification.
	clients[1] = NewClient(ClientConfig{
		ServerURL: wsURL,
		PeerID:    "peer-1",
		PublicKey: "key-1",
	})
	if err := clients[1].Connect(ctx); err != nil {
		t.Fatalf("client[1].Connect() error: %v", err)
	}
	defer clients[1].Close()
	receiveTimeout(t, clients[1].Messages(), 2*time.Second) // peers list for peer-1
	receiveTimeout(t, clients[0].Messages(), 2*time.Second) // peer-1 join notification on peer-0

	// peer-2 joins — gets peers list. peer-0 and peer-1 each get join notification.
	clients[2] = NewClient(ClientConfig{
		ServerURL: wsURL,
		PeerID:    "peer-2",
		PublicKey: "key-2",
	})
	if err := clients[2].Connect(ctx); err != nil {
		t.Fatalf("client[2].Connect() error: %v", err)
	}
	defer clients[2].Close()
	receiveTimeout(t, clients[2].Messages(), 2*time.Second) // peers list for peer-2
	receiveTimeout(t, clients[0].Messages(), 2*time.Second) // peer-2 join notification on peer-0
	receiveTimeout(t, clients[1].Messages(), 2*time.Second) // peer-2 join notification on peer-1

	// peer-0 sends an offer to peer-2.
	offer := &protocol.OfferMessage{From: "peer-0", To: "peer-2", SDP: "sdp-from-0-to-2"}
	if err := clients[0].Send(ctx, offer); err != nil {
		t.Fatalf("Send(offer) error: %v", err)
	}

	// peer-2 should receive it. peer-1 should NOT.
	msg := receiveTimeout(t, clients[2].Messages(), 2*time.Second)
	gotOffer, ok := msg.(*protocol.OfferMessage)
	if !ok {
		t.Fatalf("expected *protocol.OfferMessage, got %T", msg)
	}
	if gotOffer.From != "peer-0" || gotOffer.SDP != "sdp-from-0-to-2" {
		t.Errorf("unexpected offer: %+v", gotOffer)
	}

	// Verify peer-1 does not receive the message.
	expectNoMessage(t, clients[1].Messages(), 200*time.Millisecond)
}
