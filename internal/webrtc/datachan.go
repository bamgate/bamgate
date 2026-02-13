package webrtc

import (
	"github.com/pion/webrtc/v4"
)

const (
	// DataChannelLabel is the label used for the WireGuard tunnel data channel.
	DataChannelLabel = "bamgate"
)

// dataChannelConfig returns the pion DataChannelInit configured for
// unreliable, unordered delivery — mimicking raw UDP behavior.
// This is critical: WireGuard handles its own reliability, and reliable/ordered
// delivery would cause head-of-line blocking.
func dataChannelConfig() *webrtc.DataChannelInit {
	ordered := false
	maxRetransmits := uint16(0)
	return &webrtc.DataChannelInit{
		Ordered:        &ordered,
		MaxRetransmits: &maxRetransmits,
	}
}
