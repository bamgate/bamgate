package webrtc

import (
	"sync"
	"testing"
	"time"

	pionwebrtc "github.com/pion/webrtc/v4"
)

// localICEConfig returns an ICE config with no external STUN/TURN servers.
// pion can still establish connections between two local peers using
// host candidates alone.
func localICEConfig() ICEConfig {
	return ICEConfig{}
}

// TestPeer_OfferAnswer verifies that two peers can complete the SDP
// offer/answer exchange and open a data channel using local ICE candidates
// (no STUN/TURN required).
func TestPeer_OfferAnswer(t *testing.T) {
	t.Parallel()

	// Channels to relay ICE candidates between the two peers.
	candidatesForB := make(chan string, 32)
	candidatesForA := make(chan string, 32)

	// Channels to signal when data channels are open.
	dcOpenA := make(chan *pionwebrtc.DataChannel, 1)
	dcOpenB := make(chan *pionwebrtc.DataChannel, 1)

	// Create the offerer (peer A).
	peerA, err := NewPeer(PeerConfig{
		ICE:      localICEConfig(),
		LocalID:  "peer-a",
		RemoteID: "peer-b",
		OnICECandidate: func(candidate string) {
			candidatesForB <- candidate
		},
		OnDataChannel: func(dc *pionwebrtc.DataChannel) {
			dcOpenA <- dc
		},
	})
	if err != nil {
		t.Fatalf("NewPeer(A) error: %v", err)
	}
	defer peerA.Close()

	// Create the answerer (peer B).
	peerB, err := NewPeer(PeerConfig{
		ICE:      localICEConfig(),
		LocalID:  "peer-b",
		RemoteID: "peer-a",
		OnICECandidate: func(candidate string) {
			candidatesForA <- candidate
		},
		OnDataChannel: func(dc *pionwebrtc.DataChannel) {
			dcOpenB <- dc
		},
	})
	if err != nil {
		t.Fatalf("NewPeer(B) error: %v", err)
	}
	defer peerB.Close()

	// Peer A creates an offer.
	offerSDP, err := peerA.CreateOffer()
	if err != nil {
		t.Fatalf("CreateOffer() error: %v", err)
	}
	if offerSDP == "" {
		t.Fatal("CreateOffer() returned empty SDP")
	}

	// Peer B handles the offer and creates an answer.
	answerSDP, err := peerB.HandleOffer(offerSDP)
	if err != nil {
		t.Fatalf("HandleOffer() error: %v", err)
	}
	if answerSDP == "" {
		t.Fatal("HandleOffer() returned empty SDP")
	}

	// Peer A sets the answer.
	if err := peerA.SetAnswer(answerSDP); err != nil {
		t.Fatalf("SetAnswer() error: %v", err)
	}

	// Relay ICE candidates between peers in background goroutines.
	// We use a WaitGroup to ensure both relay goroutines finish.
	var wg sync.WaitGroup
	wg.Add(2)

	go func() {
		defer wg.Done()
		for candidate := range candidatesForB {
			if err := peerB.AddICECandidate(candidate); err != nil {
				t.Errorf("peerB.AddICECandidate() error: %v", err)
			}
		}
	}()

	go func() {
		defer wg.Done()
		for candidate := range candidatesForA {
			if err := peerA.AddICECandidate(candidate); err != nil {
				t.Errorf("peerA.AddICECandidate() error: %v", err)
			}
		}
	}()

	// Wait for both data channels to open.
	timeout := time.After(10 * time.Second)

	var dcA, dcB *pionwebrtc.DataChannel

	select {
	case dcA = <-dcOpenA:
	case <-timeout:
		t.Fatal("timed out waiting for data channel on peer A")
	}

	select {
	case dcB = <-dcOpenB:
	case <-timeout:
		t.Fatal("timed out waiting for data channel on peer B")
	}

	// Verify labels.
	if dcA.Label() != DataChannelLabel {
		t.Errorf("peer A data channel label = %q, want %q", dcA.Label(), DataChannelLabel)
	}
	if dcB.Label() != DataChannelLabel {
		t.Errorf("peer B data channel label = %q, want %q", dcB.Label(), DataChannelLabel)
	}

	// Stop relaying ICE candidates.
	close(candidatesForB)
	close(candidatesForA)
	wg.Wait()
}

// TestPeer_BidirectionalData verifies that two peers can send and receive
// arbitrary bytes over the data channel in both directions.
func TestPeer_BidirectionalData(t *testing.T) {
	t.Parallel()

	candidatesForB := make(chan string, 32)
	candidatesForA := make(chan string, 32)
	dcOpenA := make(chan *pionwebrtc.DataChannel, 1)
	dcOpenB := make(chan *pionwebrtc.DataChannel, 1)

	peerA, err := NewPeer(PeerConfig{
		ICE:      localICEConfig(),
		LocalID:  "peer-a",
		RemoteID: "peer-b",
		OnICECandidate: func(candidate string) {
			candidatesForB <- candidate
		},
		OnDataChannel: func(dc *pionwebrtc.DataChannel) {
			dcOpenA <- dc
		},
	})
	if err != nil {
		t.Fatalf("NewPeer(A) error: %v", err)
	}
	defer peerA.Close()

	peerB, err := NewPeer(PeerConfig{
		ICE:      localICEConfig(),
		LocalID:  "peer-b",
		RemoteID: "peer-a",
		OnICECandidate: func(candidate string) {
			candidatesForA <- candidate
		},
		OnDataChannel: func(dc *pionwebrtc.DataChannel) {
			dcOpenB <- dc
		},
	})
	if err != nil {
		t.Fatalf("NewPeer(B) error: %v", err)
	}
	defer peerB.Close()

	// SDP exchange.
	offerSDP, err := peerA.CreateOffer()
	if err != nil {
		t.Fatalf("CreateOffer() error: %v", err)
	}
	answerSDP, err := peerB.HandleOffer(offerSDP)
	if err != nil {
		t.Fatalf("HandleOffer() error: %v", err)
	}
	if err := peerA.SetAnswer(answerSDP); err != nil {
		t.Fatalf("SetAnswer() error: %v", err)
	}

	// Relay ICE candidates.
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		for c := range candidatesForB {
			_ = peerB.AddICECandidate(c)
		}
	}()
	go func() {
		defer wg.Done()
		for c := range candidatesForA {
			_ = peerA.AddICECandidate(c)
		}
	}()

	timeout := time.After(10 * time.Second)

	var dcA, dcB *pionwebrtc.DataChannel
	select {
	case dcA = <-dcOpenA:
	case <-timeout:
		t.Fatal("timed out waiting for data channel on peer A")
	}
	select {
	case dcB = <-dcOpenB:
	case <-timeout:
		t.Fatal("timed out waiting for data channel on peer B")
	}

	// Send data from A to B.
	msgAtoB := []byte("hello from A")
	receivedByB := make(chan []byte, 1)
	dcB.OnMessage(func(msg pionwebrtc.DataChannelMessage) {
		receivedByB <- msg.Data
	})

	if err := dcA.Send(msgAtoB); err != nil {
		t.Fatalf("dcA.Send() error: %v", err)
	}

	select {
	case got := <-receivedByB:
		if string(got) != string(msgAtoB) {
			t.Errorf("B received %q, want %q", got, msgAtoB)
		}
	case <-timeout:
		t.Fatal("timed out waiting for message on peer B")
	}

	// Send data from B to A.
	msgBtoA := []byte("hello from B")
	receivedByA := make(chan []byte, 1)
	dcA.OnMessage(func(msg pionwebrtc.DataChannelMessage) {
		receivedByA <- msg.Data
	})

	if err := dcB.Send(msgBtoA); err != nil {
		t.Fatalf("dcB.Send() error: %v", err)
	}

	select {
	case got := <-receivedByA:
		if string(got) != string(msgBtoA) {
			t.Errorf("A received %q, want %q", got, msgBtoA)
		}
	case <-timeout:
		t.Fatal("timed out waiting for message on peer A")
	}

	// Cleanup.
	close(candidatesForB)
	close(candidatesForA)
	wg.Wait()
}

// TestPeer_DataChannelUnreliableUnordered verifies that the data channel
// is configured with ordered=false and maxRetransmits=0.
func TestPeer_DataChannelUnreliableUnordered(t *testing.T) {
	t.Parallel()

	dcOpenB := make(chan *pionwebrtc.DataChannel, 1)
	candidatesForB := make(chan string, 32)
	candidatesForA := make(chan string, 32)

	peerA, err := NewPeer(PeerConfig{
		ICE:      localICEConfig(),
		LocalID:  "peer-a",
		RemoteID: "peer-b",
		OnICECandidate: func(candidate string) {
			candidatesForB <- candidate
		},
	})
	if err != nil {
		t.Fatalf("NewPeer(A) error: %v", err)
	}
	defer peerA.Close()

	peerB, err := NewPeer(PeerConfig{
		ICE:      localICEConfig(),
		LocalID:  "peer-b",
		RemoteID: "peer-a",
		OnICECandidate: func(candidate string) {
			candidatesForA <- candidate
		},
		OnDataChannel: func(dc *pionwebrtc.DataChannel) {
			dcOpenB <- dc
		},
	})
	if err != nil {
		t.Fatalf("NewPeer(B) error: %v", err)
	}
	defer peerB.Close()

	offerSDP, err := peerA.CreateOffer()
	if err != nil {
		t.Fatalf("CreateOffer() error: %v", err)
	}
	answerSDP, err := peerB.HandleOffer(offerSDP)
	if err != nil {
		t.Fatalf("HandleOffer() error: %v", err)
	}
	if err := peerA.SetAnswer(answerSDP); err != nil {
		t.Fatalf("SetAnswer() error: %v", err)
	}

	// Relay ICE candidates.
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		for c := range candidatesForB {
			_ = peerB.AddICECandidate(c)
		}
	}()
	go func() {
		defer wg.Done()
		for c := range candidatesForA {
			_ = peerA.AddICECandidate(c)
		}
	}()

	timeout := time.After(10 * time.Second)

	// Check the offerer's data channel config directly.
	dcA := peerA.DataChannel()
	if dcA == nil {
		t.Fatal("peer A data channel is nil after CreateOffer")
	}
	if dcA.Ordered() {
		t.Error("peer A data channel ordered = true, want false")
	}
	maxRetransmits := dcA.MaxRetransmits()
	if maxRetransmits == nil || *maxRetransmits != 0 {
		t.Errorf("peer A data channel maxRetransmits = %v, want 0", maxRetransmits)
	}

	// Check the answerer's data channel config (received from remote).
	select {
	case dcB := <-dcOpenB:
		if dcB.Ordered() {
			t.Error("peer B data channel ordered = true, want false")
		}
		// Note: MaxRetransmits on the receiving side may report 0 or the
		// negotiated value. We primarily verify via the offerer's config.
	case <-timeout:
		t.Fatal("timed out waiting for data channel on peer B")
	}

	close(candidatesForB)
	close(candidatesForA)
	wg.Wait()
}

// TestPeer_ConnectionStateCallback verifies that the OnConnectionStateChange
// callback is invoked during the connection lifecycle.
func TestPeer_ConnectionStateCallback(t *testing.T) {
	t.Parallel()

	candidatesForB := make(chan string, 32)
	candidatesForA := make(chan string, 32)

	statesA := make(chan pionwebrtc.ICEConnectionState, 8)
	statesB := make(chan pionwebrtc.ICEConnectionState, 8)

	peerA, err := NewPeer(PeerConfig{
		ICE:      localICEConfig(),
		LocalID:  "peer-a",
		RemoteID: "peer-b",
		OnICECandidate: func(candidate string) {
			candidatesForB <- candidate
		},
		OnConnectionStateChange: func(state pionwebrtc.ICEConnectionState) {
			statesA <- state
		},
	})
	if err != nil {
		t.Fatalf("NewPeer(A) error: %v", err)
	}
	defer peerA.Close()

	peerB, err := NewPeer(PeerConfig{
		ICE:      localICEConfig(),
		LocalID:  "peer-b",
		RemoteID: "peer-a",
		OnICECandidate: func(candidate string) {
			candidatesForA <- candidate
		},
		OnConnectionStateChange: func(state pionwebrtc.ICEConnectionState) {
			statesB <- state
		},
	})
	if err != nil {
		t.Fatalf("NewPeer(B) error: %v", err)
	}
	defer peerB.Close()

	offerSDP, err := peerA.CreateOffer()
	if err != nil {
		t.Fatalf("CreateOffer() error: %v", err)
	}
	answerSDP, err := peerB.HandleOffer(offerSDP)
	if err != nil {
		t.Fatalf("HandleOffer() error: %v", err)
	}
	if err := peerA.SetAnswer(answerSDP); err != nil {
		t.Fatalf("SetAnswer() error: %v", err)
	}

	// Relay ICE candidates.
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		for c := range candidatesForB {
			_ = peerB.AddICECandidate(c)
		}
	}()
	go func() {
		defer wg.Done()
		for c := range candidatesForA {
			_ = peerA.AddICECandidate(c)
		}
	}()

	// Wait until we see a "connected" state from at least peer A.
	timeout := time.After(10 * time.Second)
	gotConnected := false
	for !gotConnected {
		select {
		case state := <-statesA:
			if state == pionwebrtc.ICEConnectionStateConnected {
				gotConnected = true
			}
		case <-timeout:
			t.Fatal("timed out waiting for ICEConnectionStateConnected on peer A")
		}
	}

	close(candidatesForB)
	close(candidatesForA)
	wg.Wait()
}
