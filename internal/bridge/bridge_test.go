package bridge

import (
	"net"
	"sync"
	"testing"
	"time"

	"github.com/pion/webrtc/v4"
	"golang.zx2c4.com/wireguard/conn"
)

func TestBind_OpenAndReceive(t *testing.T) {
	t.Parallel()

	b := NewBind(nil)

	fns, port, err := b.Open(0)
	if err != nil {
		t.Fatalf("Open() error: %v", err)
	}
	if port != 0 {
		t.Errorf("Open() port = %d, want 0", port)
	}
	if len(fns) != 1 {
		t.Fatalf("Open() returned %d ReceiveFuncs, want 1", len(fns))
	}

	// Push a packet into the receive channel.
	ep := NewEndpoint("test-peer")
	b.recvCh <- receivedPacket{
		data: []byte("hello wireguard"),
		ep:   ep,
	}

	packets := make([][]byte, 1)
	packets[0] = make([]byte, 1500)
	sizes := make([]int, 1)
	eps := make([]conn.Endpoint, 1)

	n, err := fns[0](packets, sizes, eps)
	if err != nil {
		t.Fatalf("ReceiveFunc() error: %v", err)
	}
	if n != 1 {
		t.Fatalf("ReceiveFunc() n = %d, want 1", n)
	}
	if sizes[0] != len("hello wireguard") {
		t.Errorf("sizes[0] = %d, want %d", sizes[0], len("hello wireguard"))
	}

	gotEp, ok := eps[0].(*Endpoint)
	if !ok {
		t.Fatalf("endpoint type = %T, want *Endpoint", eps[0])
	}
	if gotEp.PeerID() != "test-peer" {
		t.Errorf("endpoint peer ID = %q, want %q", gotEp.PeerID(), "test-peer")
	}

	got := string(packets[0][:sizes[0]])
	if got != "hello wireguard" {
		t.Errorf("received = %q, want %q", got, "hello wireguard")
	}
}

func TestBind_Close_UnblocksReceive(t *testing.T) {
	t.Parallel()

	b := NewBind(nil)

	fns, _, err := b.Open(0)
	if err != nil {
		t.Fatalf("Open() error: %v", err)
	}

	done := make(chan error, 1)
	go func() {
		packets := make([][]byte, 1)
		packets[0] = make([]byte, 1500)
		sizes := make([]int, 1)
		eps := make([]conn.Endpoint, 1)
		_, err := fns[0](packets, sizes, eps)
		done <- err
	}()

	// Give the goroutine time to block.
	time.Sleep(50 * time.Millisecond)

	if err := b.Close(); err != nil {
		t.Fatalf("Close() error: %v", err)
	}

	select {
	case err := <-done:
		if err != net.ErrClosed {
			t.Errorf("ReceiveFunc() error = %v, want net.ErrClosed", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("ReceiveFunc() did not unblock after Close()")
	}
}

func TestBind_Send(t *testing.T) {
	t.Parallel()

	b := NewBind(nil)

	// Create a real WebRTC data channel pair for testing Send.
	dc1, dc2 := createDataChannelPair(t)

	b.SetDataChannel("peer-b", dc1)

	// Set up a receiver on dc2.
	received := make(chan []byte, 1)
	dc2.OnMessage(func(msg webrtc.DataChannelMessage) {
		data := make([]byte, len(msg.Data))
		copy(data, msg.Data)
		received <- data
	})

	ep := NewEndpoint("peer-b")
	payload := []byte("encrypted wg packet")

	err := b.Send([][]byte{payload}, ep)
	if err != nil {
		t.Fatalf("Send() error: %v", err)
	}

	select {
	case got := <-received:
		if string(got) != string(payload) {
			t.Errorf("received = %q, want %q", got, payload)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for message on data channel")
	}
}

func TestBind_Send_UnknownPeer(t *testing.T) {
	t.Parallel()

	b := NewBind(nil)

	ep := NewEndpoint("nonexistent-peer")
	err := b.Send([][]byte{[]byte("data")}, ep)
	if err == nil {
		t.Fatal("Send() to unknown peer should return error")
	}
}

func TestBind_ParseEndpoint(t *testing.T) {
	t.Parallel()

	b := NewBind(nil)

	ep, err := b.ParseEndpoint("my-peer-id")
	if err != nil {
		t.Fatalf("ParseEndpoint() error: %v", err)
	}

	endpoint, ok := ep.(*Endpoint)
	if !ok {
		t.Fatalf("ParseEndpoint() returned %T, want *Endpoint", ep)
	}
	if endpoint.PeerID() != "my-peer-id" {
		t.Errorf("PeerID() = %q, want %q", endpoint.PeerID(), "my-peer-id")
	}
}

func TestBind_BatchSize(t *testing.T) {
	t.Parallel()

	b := NewBind(nil)
	if got := b.BatchSize(); got != 1 {
		t.Errorf("BatchSize() = %d, want 1", got)
	}
}

func TestBind_SetMark(t *testing.T) {
	t.Parallel()

	b := NewBind(nil)
	if err := b.SetMark(42); err != nil {
		t.Errorf("SetMark() error: %v", err)
	}
}

func TestBind_RemoveDataChannel(t *testing.T) {
	t.Parallel()

	b := NewBind(nil)

	dc1, _ := createDataChannelPair(t)
	b.SetDataChannel("peer-x", dc1)

	// Should be sendable.
	ep := NewEndpoint("peer-x")
	err := b.Send([][]byte{[]byte("test")}, ep)
	if err != nil {
		t.Fatalf("Send() before remove error: %v", err)
	}

	b.RemoveDataChannel("peer-x")

	// Should fail after removal.
	err = b.Send([][]byte{[]byte("test")}, ep)
	if err == nil {
		t.Error("Send() after RemoveDataChannel should return error")
	}
}

func TestBind_MultiplePeers(t *testing.T) {
	t.Parallel()

	b := NewBind(nil)

	dc1a, dc1b := createDataChannelPair(t)
	dc2a, dc2b := createDataChannelPair(t)

	b.SetDataChannel("peer-1", dc1a)
	b.SetDataChannel("peer-2", dc2a)

	// Set up receivers.
	recv1 := make(chan []byte, 1)
	dc1b.OnMessage(func(msg webrtc.DataChannelMessage) {
		data := make([]byte, len(msg.Data))
		copy(data, msg.Data)
		recv1 <- data
	})

	recv2 := make(chan []byte, 1)
	dc2b.OnMessage(func(msg webrtc.DataChannelMessage) {
		data := make([]byte, len(msg.Data))
		copy(data, msg.Data)
		recv2 <- data
	})

	// Send to peer-1.
	ep1 := NewEndpoint("peer-1")
	if err := b.Send([][]byte{[]byte("to-peer-1")}, ep1); err != nil {
		t.Fatalf("Send to peer-1: %v", err)
	}

	// Send to peer-2.
	ep2 := NewEndpoint("peer-2")
	if err := b.Send([][]byte{[]byte("to-peer-2")}, ep2); err != nil {
		t.Fatalf("Send to peer-2: %v", err)
	}

	// Verify each peer received the correct message.
	select {
	case got := <-recv1:
		if string(got) != "to-peer-1" {
			t.Errorf("peer-1 received %q, want %q", got, "to-peer-1")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for peer-1 message")
	}

	select {
	case got := <-recv2:
		if string(got) != "to-peer-2" {
			t.Errorf("peer-2 received %q, want %q", got, "to-peer-2")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for peer-2 message")
	}
}

func TestBind_DataChannelReceive(t *testing.T) {
	t.Parallel()

	b := NewBind(nil)

	fns, _, err := b.Open(0)
	if err != nil {
		t.Fatalf("Open() error: %v", err)
	}

	dc1a, dc1b := createDataChannelPair(t)
	b.SetDataChannel("peer-alpha", dc1a)

	// Send a message from dc1b (the remote side) — it should arrive
	// in our Bind's receive channel via dc1a's OnMessage handler.
	if err := dc1b.Send([]byte("incoming wg packet")); err != nil {
		t.Fatalf("dc1b.Send() error: %v", err)
	}

	packets := make([][]byte, 1)
	packets[0] = make([]byte, 1500)
	sizes := make([]int, 1)
	eps := make([]conn.Endpoint, 1)

	// The packet should be received within a reasonable time.
	done := make(chan struct{})
	go func() {
		defer close(done)
		n, recvErr := fns[0](packets, sizes, eps)
		if recvErr != nil {
			t.Errorf("ReceiveFunc() error: %v", recvErr)
			return
		}
		if n != 1 {
			t.Errorf("ReceiveFunc() n = %d, want 1", n)
			return
		}
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for packet via data channel")
	}

	got := string(packets[0][:sizes[0]])
	if got != "incoming wg packet" {
		t.Errorf("received = %q, want %q", got, "incoming wg packet")
	}

	gotEp, ok := eps[0].(*Endpoint)
	if !ok {
		t.Fatalf("endpoint type = %T, want *Endpoint", eps[0])
	}
	if gotEp.PeerID() != "peer-alpha" {
		t.Errorf("endpoint peer ID = %q, want %q", gotEp.PeerID(), "peer-alpha")
	}
}

func TestEndpoint_Methods(t *testing.T) {
	t.Parallel()

	ep := NewEndpoint("test-id")

	if ep.PeerID() != "test-id" {
		t.Errorf("PeerID() = %q, want %q", ep.PeerID(), "test-id")
	}
	if ep.DstToString() != "test-id" {
		t.Errorf("DstToString() = %q, want %q", ep.DstToString(), "test-id")
	}
	if ep.SrcToString() != "" {
		t.Errorf("SrcToString() = %q, want empty", ep.SrcToString())
	}
	if string(ep.DstToBytes()) != "test-id" {
		t.Errorf("DstToBytes() = %q, want %q", ep.DstToBytes(), "test-id")
	}

	// Zero addresses.
	if ep.DstIP().IsValid() {
		t.Errorf("DstIP() should be zero addr")
	}
	if ep.SrcIP().IsValid() {
		t.Errorf("SrcIP() should be zero addr")
	}

	// ClearSrc should not panic.
	ep.ClearSrc()
}

func TestBind_Reset(t *testing.T) {
	t.Parallel()

	b := NewBind(nil)

	// Open, close, then reset and open again.
	_, _, err := b.Open(0)
	if err != nil {
		t.Fatalf("first Open() error: %v", err)
	}

	if err := b.Close(); err != nil {
		t.Fatalf("Close() error: %v", err)
	}

	b.Reset()

	fns, _, err := b.Open(0)
	if err != nil {
		t.Fatalf("second Open() after Reset() error: %v", err)
	}

	// Should be able to receive again.
	ep := NewEndpoint("peer-after-reset")
	b.recvCh <- receivedPacket{data: []byte("post-reset"), ep: ep}

	packets := make([][]byte, 1)
	packets[0] = make([]byte, 1500)
	sizes := make([]int, 1)
	eps := make([]conn.Endpoint, 1)

	n, err := fns[0](packets, sizes, eps)
	if err != nil {
		t.Fatalf("ReceiveFunc after Reset() error: %v", err)
	}
	if n != 1 {
		t.Fatalf("n = %d, want 1", n)
	}
	if string(packets[0][:sizes[0]]) != "post-reset" {
		t.Errorf("received = %q, want %q", packets[0][:sizes[0]], "post-reset")
	}
}

// --- helpers ---

// createDataChannelPair creates two connected WebRTC peer connections with
// open data channels for testing. Returns (dc on peer A, dc on peer B).
func createDataChannelPair(t *testing.T) (*webrtc.DataChannel, *webrtc.DataChannel) {
	t.Helper()

	// Create two peer connections.
	pcA, err := webrtc.NewPeerConnection(webrtc.Configuration{})
	if err != nil {
		t.Fatalf("NewPeerConnection(A) error: %v", err)
	}
	t.Cleanup(func() {
		if err := pcA.Close(); err != nil {
			t.Logf("pcA.Close() error: %v", err)
		}
	})

	pcB, err := webrtc.NewPeerConnection(webrtc.Configuration{})
	if err != nil {
		t.Fatalf("NewPeerConnection(B) error: %v", err)
	}
	t.Cleanup(func() {
		if err := pcB.Close(); err != nil {
			t.Logf("pcB.Close() error: %v", err)
		}
	})

	// A creates a data channel.
	dcA, err := pcA.CreateDataChannel("test", nil)
	if err != nil {
		t.Fatalf("CreateDataChannel() error: %v", err)
	}

	// B waits for the data channel.
	dcBCh := make(chan *webrtc.DataChannel, 1)
	pcB.OnDataChannel(func(dc *webrtc.DataChannel) {
		dc.OnOpen(func() {
			dcBCh <- dc
		})
	})

	// Wait for A's data channel to open.
	dcAOpen := make(chan struct{})
	dcA.OnOpen(func() {
		close(dcAOpen)
	})

	// Exchange ICE candidates.
	var candidatesA []webrtc.ICECandidateInit
	var candidatesB []webrtc.ICECandidateInit
	var muA, muB sync.Mutex

	pcA.OnICECandidate(func(c *webrtc.ICECandidate) {
		if c == nil {
			return
		}
		muA.Lock()
		candidatesA = append(candidatesA, c.ToJSON())
		muA.Unlock()
	})

	pcB.OnICECandidate(func(c *webrtc.ICECandidate) {
		if c == nil {
			return
		}
		muB.Lock()
		candidatesB = append(candidatesB, c.ToJSON())
		muB.Unlock()
	})

	// SDP exchange.
	offer, err := pcA.CreateOffer(nil)
	if err != nil {
		t.Fatalf("CreateOffer() error: %v", err)
	}
	if err := pcA.SetLocalDescription(offer); err != nil {
		t.Fatalf("SetLocalDescription(offer) error: %v", err)
	}

	if err := pcB.SetRemoteDescription(offer); err != nil {
		t.Fatalf("SetRemoteDescription(offer) error: %v", err)
	}
	answer, err := pcB.CreateAnswer(nil)
	if err != nil {
		t.Fatalf("CreateAnswer() error: %v", err)
	}
	if err := pcB.SetLocalDescription(answer); err != nil {
		t.Fatalf("SetLocalDescription(answer) error: %v", err)
	}
	if err := pcA.SetRemoteDescription(answer); err != nil {
		t.Fatalf("SetRemoteDescription(answer) error: %v", err)
	}

	// Wait for ICE gathering then exchange candidates.
	waitGathering(t, pcA)
	waitGathering(t, pcB)

	muA.Lock()
	for _, c := range candidatesA {
		if err := pcB.AddICECandidate(c); err != nil {
			t.Fatalf("AddICECandidate(B) error: %v", err)
		}
	}
	muA.Unlock()

	muB.Lock()
	for _, c := range candidatesB {
		if err := pcA.AddICECandidate(c); err != nil {
			t.Fatalf("AddICECandidate(A) error: %v", err)
		}
	}
	muB.Unlock()

	// Wait for data channels to open.
	select {
	case <-dcAOpen:
	case <-time.After(10 * time.Second):
		t.Fatal("timed out waiting for dcA to open")
	}

	var dcB *webrtc.DataChannel
	select {
	case dcB = <-dcBCh:
	case <-time.After(10 * time.Second):
		t.Fatal("timed out waiting for dcB to open")
	}

	return dcA, dcB
}

// waitGathering waits for ICE gathering to complete on a peer connection.
func waitGathering(t *testing.T, pc *webrtc.PeerConnection) {
	t.Helper()

	if pc.ICEGatheringState() == webrtc.ICEGatheringStateComplete {
		return
	}

	done := make(chan struct{})
	pc.OnICEGatheringStateChange(func(state webrtc.ICEGatheringState) {
		if state == webrtc.ICEGatheringStateComplete {
			select {
			case <-done:
			default:
				close(done)
			}
		}
	})

	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("timed out waiting for ICE gathering")
	}
}
