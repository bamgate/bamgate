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

	// API is an optional custom webrtc.API instance (e.g. with a SettingEngine
	// that has a proxy dialer configured for TURN-over-WebSocket). If nil,
	// the default pion API is used.
	API *webrtc.API

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

	mu              sync.Mutex
	dc              *webrtc.DataChannel
	suppressTrickle bool // when true, OnICECandidate callback is suppressed (used during ICE restart)
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

	rtcConfig := webrtc.Configuration{
		ICEServers: cfg.ICE.pionICEServers(),
	}
	if cfg.ICE.ForceRelay {
		rtcConfig.ICETransportPolicy = webrtc.ICETransportPolicyRelay
		log.Info("ICE transport policy set to relay-only (force_relay enabled)")
	}

	var (
		pc  *webrtc.PeerConnection
		err error
	)
	if cfg.API != nil {
		pc, err = cfg.API.NewPeerConnection(rtcConfig)
	} else {
		pc, err = webrtc.NewPeerConnection(rtcConfig)
	}
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
		p.mu.Lock()
		suppress := p.suppressTrickle
		p.mu.Unlock()
		if suppress {
			p.log.Debug("ICE candidate gathered (suppressed, will be in SDP)", "candidate", c.String())
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

// HandleOfferFullICE is like HandleOffer but waits for ICE gathering to
// complete before returning. The returned SDP contains all gathered candidates
// as a= lines, avoiding trickle ICE. This is used for ICE restart offers
// where trickle candidates would race with the SDP and get dropped due to
// ufrag mismatch.
func (p *Peer) HandleOfferFullICE(sdp string) (string, error) {
	offer := webrtc.SessionDescription{
		Type: webrtc.SDPTypeOffer,
		SDP:  sdp,
	}
	if err := p.pc.SetRemoteDescription(offer); err != nil {
		return "", fmt.Errorf("setting remote offer: %w", err)
	}

	// Suppress trickle ICE — candidates will be embedded in the SDP.
	p.mu.Lock()
	p.suppressTrickle = true
	p.mu.Unlock()

	gatherComplete := webrtc.GatheringCompletePromise(p.pc)

	answer, err := p.pc.CreateAnswer(nil)
	if err != nil {
		p.mu.Lock()
		p.suppressTrickle = false
		p.mu.Unlock()
		return "", fmt.Errorf("creating SDP answer: %w", err)
	}

	if err := p.pc.SetLocalDescription(answer); err != nil {
		p.mu.Lock()
		p.suppressTrickle = false
		p.mu.Unlock()
		return "", fmt.Errorf("setting local description: %w", err)
	}

	<-gatherComplete

	p.mu.Lock()
	p.suppressTrickle = false
	p.mu.Unlock()

	finalSDP := p.pc.LocalDescription().SDP

	p.log.Debug("SDP answer created (full ICE gathering complete)")
	return finalSDP, nil
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
//
// Unlike the initial offer (which uses trickle ICE), the restart offer waits
// for ICE gathering to complete before returning. This ensures all candidates
// are embedded in the SDP, avoiding the race where trickle candidates arrive
// at the remote peer before the restart offer and get dropped due to ufrag
// mismatch.
//
// If the PeerConnection is already in the "have-local-offer" state (e.g. from
// a previous ICE restart whose answer never arrived because signaling was down),
// the pending offer is rolled back to "stable" before creating the new one.
func (p *Peer) RestartICE() (string, error) {
	// If we're stuck in have-local-offer (previous restart offer was never
	// answered), roll back to stable so we can create a fresh offer.
	if p.pc.SignalingState() == webrtc.SignalingStateHaveLocalOffer {
		p.log.Info("rolling back pending offer before ICE restart")
		if err := p.pc.SetLocalDescription(webrtc.SessionDescription{
			Type: webrtc.SDPTypeRollback,
		}); err != nil {
			return "", fmt.Errorf("rolling back local description: %w", err)
		}
	}

	// Suppress trickle ICE during restart — candidates will be embedded
	// in the SDP instead.
	p.mu.Lock()
	p.suppressTrickle = true
	p.mu.Unlock()

	// Must be called before SetLocalDescription to avoid a race where
	// gathering completes between SLD and this call.
	gatherComplete := webrtc.GatheringCompletePromise(p.pc)

	offer, err := p.pc.CreateOffer(&webrtc.OfferOptions{ICERestart: true})
	if err != nil {
		p.mu.Lock()
		p.suppressTrickle = false
		p.mu.Unlock()
		return "", fmt.Errorf("creating ICE restart offer: %w", err)
	}

	if err := p.pc.SetLocalDescription(offer); err != nil {
		p.mu.Lock()
		p.suppressTrickle = false
		p.mu.Unlock()
		return "", fmt.Errorf("setting local description for ICE restart: %w", err)
	}

	// Wait for all ICE candidates to be gathered.
	<-gatherComplete

	p.mu.Lock()
	p.suppressTrickle = false
	p.mu.Unlock()

	// Return the final local description which includes all gathered
	// candidates as a= lines in the SDP.
	finalSDP := p.pc.LocalDescription().SDP

	p.log.Info("ICE restart initiated (full ICE gathering complete)")
	return finalSDP, nil
}

// HasRemoteDescription returns true if a remote SDP description has been set
// on the underlying PeerConnection. This is used by the agent to decide
// whether to buffer incoming ICE candidates (pion rejects AddICECandidate
// calls before SetRemoteDescription).
func (p *Peer) HasRemoteDescription() bool {
	return p.pc.RemoteDescription() != nil
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
