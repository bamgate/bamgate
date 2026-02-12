package webrtc

import (
	"fmt"
	"log/slog"
	"sync"

	"github.com/pion/webrtc/v4"
)

// PeerConfig holds configuration for creating a Peer.
type PeerConfig struct {
	// ICE contains the STUN/TURN server configuration.
	ICE ICEConfig

	// LocalID is this peer's identifier (used for logging).
	LocalID string

	// RemoteID is the remote peer's identifier (used for logging).
	RemoteID string

	// Logger is the structured logger. If nil, slog.Default() is used.
	Logger *slog.Logger

	// OnICECandidate is called when a local ICE candidate is gathered.
	// The caller should relay the candidate string to the remote peer
	// via the signaling channel. A nil candidate signals that gathering
	// is complete.
	OnICECandidate func(candidate string)

	// OnDataChannel is called when the data channel is ready for use.
	// For the offerer, this fires when the channel it created opens.
	// For the answerer, this fires when the remote data channel arrives
	// and opens.
	OnDataChannel func(dc *webrtc.DataChannel)

	// OnConnectionStateChange is called when the ICE connection state changes.
	// Useful for logging whether the connection is direct or relayed.
	OnConnectionStateChange func(state webrtc.ICEConnectionState)
}

// Peer wraps a pion RTCPeerConnection and manages the SDP offer/answer
// exchange, ICE candidate trickle, and data channel lifecycle.
type Peer struct {
	cfg  PeerConfig
	log  *slog.Logger
	pc   *webrtc.PeerConnection
	done chan struct{}

	mu sync.Mutex
	dc *webrtc.DataChannel
}

// NewPeer creates a new RTCPeerConnection with the given ICE configuration.
// It does NOT create the SDP offer or data channel — call CreateOffer (offerer)
// or HandleOffer (answerer) to proceed with the signaling exchange.
func NewPeer(cfg PeerConfig) (*Peer, error) {
	log := cfg.Logger
	if log == nil {
		log = slog.Default()
	}
	log = log.With("local_id", cfg.LocalID, "remote_id", cfg.RemoteID)

	pc, err := webrtc.NewPeerConnection(webrtc.Configuration{
		ICEServers: cfg.ICE.pionICEServers(),
	})
	if err != nil {
		return nil, fmt.Errorf("creating peer connection: %w", err)
	}

	p := &Peer{
		cfg:  cfg,
		log:  log,
		pc:   pc,
		done: make(chan struct{}),
	}

	// Register ICE candidate callback — relay gathered candidates to the
	// remote peer via the signaling channel.
	pc.OnICECandidate(func(c *webrtc.ICECandidate) {
		if c == nil {
			// Gathering complete.
			p.log.Debug("ICE gathering complete")
			return
		}
		p.log.Debug("ICE candidate gathered", "candidate", c.String())
		if p.cfg.OnICECandidate != nil {
			p.cfg.OnICECandidate(c.ToJSON().Candidate)
		}
	})

	// Log ICE connection state changes.
	pc.OnICEConnectionStateChange(func(state webrtc.ICEConnectionState) {
		p.log.Info("ICE connection state changed", "state", state.String())
		if p.cfg.OnConnectionStateChange != nil {
			p.cfg.OnConnectionStateChange(state)
		}
		if state == webrtc.ICEConnectionStateFailed ||
			state == webrtc.ICEConnectionStateClosed {
			p.mu.Lock()
			select {
			case <-p.done:
			default:
				close(p.done)
			}
			p.mu.Unlock()
		}
	})

	// For the answerer: handle the data channel created by the offerer.
	pc.OnDataChannel(func(dc *webrtc.DataChannel) {
		p.log.Info("remote data channel received", "label", dc.Label())
		p.setupDataChannel(dc)
	})

	return p, nil
}

// CreateOffer creates a data channel, generates an SDP offer, and sets it
// as the local description. The caller should send the returned SDP string
// to the remote peer via the signaling channel.
func (p *Peer) CreateOffer() (string, error) {
	dc, err := p.pc.CreateDataChannel(DataChannelLabel, dataChannelConfig())
	if err != nil {
		return "", fmt.Errorf("creating data channel: %w", err)
	}
	p.setupDataChannel(dc)

	offer, err := p.pc.CreateOffer(nil)
	if err != nil {
		return "", fmt.Errorf("creating SDP offer: %w", err)
	}

	if err := p.pc.SetLocalDescription(offer); err != nil {
		return "", fmt.Errorf("setting local description: %w", err)
	}

	p.log.Debug("SDP offer created")
	return offer.SDP, nil
}

// HandleOffer sets the remote SDP offer, creates an SDP answer, and sets it
// as the local description. The caller should send the returned SDP string
// back to the offerer via the signaling channel.
func (p *Peer) HandleOffer(sdp string) (string, error) {
	offer := webrtc.SessionDescription{
		Type: webrtc.SDPTypeOffer,
		SDP:  sdp,
	}
	if err := p.pc.SetRemoteDescription(offer); err != nil {
		return "", fmt.Errorf("setting remote offer: %w", err)
	}

	answer, err := p.pc.CreateAnswer(nil)
	if err != nil {
		return "", fmt.Errorf("creating SDP answer: %w", err)
	}

	if err := p.pc.SetLocalDescription(answer); err != nil {
		return "", fmt.Errorf("setting local description: %w", err)
	}

	p.log.Debug("SDP answer created")
	return answer.SDP, nil
}

// SetAnswer sets the remote SDP answer on the peer connection. Called by the
// offerer after receiving the answer from the remote peer via signaling.
func (p *Peer) SetAnswer(sdp string) error {
	answer := webrtc.SessionDescription{
		Type: webrtc.SDPTypeAnswer,
		SDP:  sdp,
	}
	if err := p.pc.SetRemoteDescription(answer); err != nil {
		return fmt.Errorf("setting remote answer: %w", err)
	}

	p.log.Debug("remote SDP answer set")
	return nil
}

// RestartICE creates a new SDP offer with the ICE restart flag set. The
// existing data channel and SCTP association survive — only the ICE transport
// is renegotiated. The caller should send the returned SDP to the remote peer,
// then apply the answer via SetAnswer.
func (p *Peer) RestartICE() (string, error) {
	offer, err := p.pc.CreateOffer(&webrtc.OfferOptions{ICERestart: true})
	if err != nil {
		return "", fmt.Errorf("creating ICE restart offer: %w", err)
	}

	if err := p.pc.SetLocalDescription(offer); err != nil {
		return "", fmt.Errorf("setting local description for ICE restart: %w", err)
	}

	p.log.Info("ICE restart initiated")
	return offer.SDP, nil
}

// AddICECandidate adds a remote ICE candidate received via the signaling channel.
func (p *Peer) AddICECandidate(candidate string) error {
	if err := p.pc.AddICECandidate(webrtc.ICECandidateInit{
		Candidate: candidate,
	}); err != nil {
		return fmt.Errorf("adding ICE candidate: %w", err)
	}

	p.log.Debug("remote ICE candidate added", "candidate", candidate)
	return nil
}

// DataChannel returns the current data channel, or nil if not yet established.
func (p *Peer) DataChannel() *webrtc.DataChannel {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.dc
}

// ICECandidateType returns the type of the selected local ICE candidate
// (e.g. "host", "srflx", "relay") or "unknown" if no pair is selected.
// This indicates whether the connection is direct or relayed.
func (p *Peer) ICECandidateType() string {
	pair, err := p.pc.SCTP().Transport().ICETransport().GetSelectedCandidatePair()
	if err != nil || pair == nil {
		return "unknown"
	}
	return pair.Local.Typ.String()
}

// ConnectionState returns the current ICE connection state.
func (p *Peer) ConnectionState() webrtc.ICEConnectionState {
	return p.pc.ICEConnectionState()
}

// Done returns a channel that is closed when the peer connection is
// failed or closed.
func (p *Peer) Done() <-chan struct{} {
	return p.done
}

// Close gracefully closes the peer connection and data channel.
func (p *Peer) Close() error {
	p.mu.Lock()
	select {
	case <-p.done:
	default:
		close(p.done)
	}
	dc := p.dc
	p.mu.Unlock()

	if dc != nil {
		if err := dc.Close(); err != nil {
			p.log.Warn("closing data channel", "error", err)
		}
	}

	if err := p.pc.Close(); err != nil {
		return fmt.Errorf("closing peer connection: %w", err)
	}

	p.log.Info("peer connection closed")
	return nil
}

// setupDataChannel registers callbacks on the data channel and stores it.
func (p *Peer) setupDataChannel(dc *webrtc.DataChannel) {
	p.mu.Lock()
	p.dc = dc
	p.mu.Unlock()

	dc.OnOpen(func() {
		p.log.Info("data channel open", "label", dc.Label())
		if p.cfg.OnDataChannel != nil {
			p.cfg.OnDataChannel(dc)
		}
	})

	dc.OnClose(func() {
		p.log.Info("data channel closed", "label", dc.Label())
	})

	dc.OnError(func(err error) {
		p.log.Error("data channel error", "label", dc.Label(), "error", err)
	})
}
