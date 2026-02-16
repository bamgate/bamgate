// Package protocol defines the signaling protocol message types used by
// bamgate clients and the Cloudflare Worker signaling server.
//
// All messages are JSON-encoded with a "type" discriminator field. This
// package is intentionally free of external dependencies so it can be
// compiled with both standard Go and TinyGo (for the Wasm worker).
package protocol

import (
	"encoding/json"
	"fmt"
)

// Message is the interface implemented by all signaling protocol messages.
// Each message type corresponds to a JSON object with a "type" discriminator field.
type Message interface {
	// MessageType returns the wire-format type string (e.g. "join", "offer").
	MessageType() string
}

// PeerInfo describes a connected peer, used in the PeersMessage.
type PeerInfo struct {
	PeerID    string            `json:"peerId"`
	PublicKey string            `json:"publicKey"`
	Address   string            `json:"address,omitempty"`
	Routes    []string          `json:"routes,omitempty"`
	Metadata  map[string]string `json:"metadata,omitempty"`
}

// Well-known metadata keys for peer capability advertisement.
const (
	// MetaKeyRoutes advertises subnet routes reachable through this peer.
	// Value is a JSON array of CIDR strings, e.g. `["10.96.0.0/12"]`.
	MetaKeyRoutes = "routes"

	// MetaKeyDNS advertises DNS servers available through this peer.
	// Value is a JSON array of IP strings, e.g. `["10.96.0.10"]`.
	MetaKeyDNS = "dns"

	// MetaKeyDNSSearch advertises DNS search domains available through this peer.
	// Value is a JSON array of domain strings, e.g. `["svc.cluster.local"]`.
	MetaKeyDNSSearch = "dns_search"
)

// JoinMessage is sent by a client to announce itself to the signaling hub.
type JoinMessage struct {
	PeerID    string            `json:"peerId"`
	PublicKey string            `json:"publicKey"`
	Address   string            `json:"address,omitempty"`
	Routes    []string          `json:"routes,omitempty"`
	Metadata  map[string]string `json:"metadata,omitempty"`
}

func (JoinMessage) MessageType() string { return "join" }

// OfferMessage carries an SDP offer from one peer to another.
type OfferMessage struct {
	From      string `json:"from"`
	To        string `json:"to"`
	SDP       string `json:"sdp"`
	PublicKey string `json:"publicKey,omitempty"`
}

func (OfferMessage) MessageType() string { return "offer" }

// AnswerMessage carries an SDP answer from one peer to another.
type AnswerMessage struct {
	From      string `json:"from"`
	To        string `json:"to"`
	SDP       string `json:"sdp"`
	PublicKey string `json:"publicKey,omitempty"`
}

func (AnswerMessage) MessageType() string { return "answer" }

// ICECandidateMessage carries a trickle ICE candidate from one peer to another.
type ICECandidateMessage struct {
	From      string `json:"from"`
	To        string `json:"to"`
	Candidate string `json:"candidate"`
}

func (ICECandidateMessage) MessageType() string { return "ice-candidate" }

// PeersMessage is sent by the server to a newly connected peer,
// listing all other peers currently in the network.
type PeersMessage struct {
	Peers []PeerInfo `json:"peers"`
}

func (PeersMessage) MessageType() string { return "peers" }

// PeerLeftMessage is broadcast by the server when a peer disconnects.
type PeerLeftMessage struct {
	PeerID string `json:"peerId"`
}

func (PeerLeftMessage) MessageType() string { return "peer-left" }

// messageTypes maps wire-format type strings to factory functions
// that produce zero-value pointers of the corresponding message type.
var messageTypes = map[string]func() Message{
	"join":          func() Message { return &JoinMessage{} },
	"offer":         func() Message { return &OfferMessage{} },
	"answer":        func() Message { return &AnswerMessage{} },
	"ice-candidate": func() Message { return &ICECandidateMessage{} },
	"peers":         func() Message { return &PeersMessage{} },
	"peer-left":     func() Message { return &PeerLeftMessage{} },
}

// Marshal serializes a Message to JSON, injecting the "type" discriminator field.
func Marshal(msg Message) ([]byte, error) {
	// First, marshal the message to get its fields as raw JSON.
	raw, err := json.Marshal(msg)
	if err != nil {
		return nil, fmt.Errorf("marshaling message payload: %w", err)
	}

	// Decode into a generic map so we can inject the "type" field.
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(raw, &obj); err != nil {
		return nil, fmt.Errorf("re-decoding message payload: %w", err)
	}

	typeBytes, err := json.Marshal(msg.MessageType())
	if err != nil {
		return nil, fmt.Errorf("marshaling message type: %w", err)
	}
	obj["type"] = typeBytes

	return json.Marshal(obj)
}

// Unmarshal deserializes a JSON message, using the "type" discriminator
// to decode into the correct concrete Message type.
func Unmarshal(data []byte) (Message, error) {
	// First pass: extract the type field.
	var env struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(data, &env); err != nil {
		return nil, fmt.Errorf("decoding message envelope: %w", err)
	}

	factory, ok := messageTypes[env.Type]
	if !ok {
		return nil, fmt.Errorf("unknown message type: %q", env.Type)
	}

	// Second pass: decode into the concrete type.
	msg := factory()
	if err := json.Unmarshal(data, msg); err != nil {
		return nil, fmt.Errorf("decoding %q message: %w", env.Type, err)
	}

	return msg, nil
}
