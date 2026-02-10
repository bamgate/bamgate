package signaling

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestMarshalUnmarshal_RoundTrip(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		msg     Message
		wantTyp string
	}{
		{
			name:    "join",
			msg:     &JoinMessage{PeerID: "home-server", PublicKey: "abc123"},
			wantTyp: "join",
		},
		{
			name:    "offer",
			msg:     &OfferMessage{From: "laptop", To: "home-server", SDP: "v=0\r\noffer"},
			wantTyp: "offer",
		},
		{
			name:    "answer",
			msg:     &AnswerMessage{From: "home-server", To: "laptop", SDP: "v=0\r\nanswer"},
			wantTyp: "answer",
		},
		{
			name:    "ice-candidate",
			msg:     &ICECandidateMessage{From: "laptop", To: "home-server", Candidate: "candidate:1 1 udp 2130706431 192.168.1.1 5000 typ host"},
			wantTyp: "ice-candidate",
		},
		{
			name: "peers",
			msg: &PeersMessage{Peers: []PeerInfo{
				{PeerID: "home-server", PublicKey: "key1"},
				{PeerID: "laptop", PublicKey: "key2"},
			}},
			wantTyp: "peers",
		},
		{
			name:    "peers/empty",
			msg:     &PeersMessage{Peers: []PeerInfo{}},
			wantTyp: "peers",
		},
		{
			name:    "peer-left",
			msg:     &PeerLeftMessage{PeerID: "home-server"},
			wantTyp: "peer-left",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			// Marshal
			data, err := Marshal(tt.msg)
			if err != nil {
				t.Fatalf("Marshal() error: %v", err)
			}

			// Verify the "type" field is present in the JSON.
			var raw map[string]json.RawMessage
			if err := json.Unmarshal(data, &raw); err != nil {
				t.Fatalf("unmarshaling raw JSON: %v", err)
			}
			typeVal, ok := raw["type"]
			if !ok {
				t.Fatal("marshaled JSON missing \"type\" field")
			}
			var gotType string
			if err := json.Unmarshal(typeVal, &gotType); err != nil {
				t.Fatalf("decoding type field: %v", err)
			}
			if gotType != tt.wantTyp {
				t.Errorf("type = %q, want %q", gotType, tt.wantTyp)
			}

			// Unmarshal back.
			got, err := Unmarshal(data)
			if err != nil {
				t.Fatalf("Unmarshal() error: %v", err)
			}

			// Verify the round-tripped message matches.
			// Re-marshal both and compare JSON to avoid reflect.DeepEqual on pointer types.
			gotData, err := Marshal(got)
			if err != nil {
				t.Fatalf("re-marshaling: %v", err)
			}

			// Normalize: unmarshal both to generic maps and compare.
			var origMap, gotMap map[string]any
			if err := json.Unmarshal(data, &origMap); err != nil {
				t.Fatalf("decoding original: %v", err)
			}
			if err := json.Unmarshal(gotData, &gotMap); err != nil {
				t.Fatalf("decoding round-tripped: %v", err)
			}

			origJSON, _ := json.Marshal(origMap)
			gotJSON, _ := json.Marshal(gotMap)
			if string(origJSON) != string(gotJSON) {
				t.Errorf("round-trip mismatch:\n  original:     %s\n  round-tripped: %s", origJSON, gotJSON)
			}
		})
	}
}

func TestUnmarshal_UnknownType(t *testing.T) {
	t.Parallel()

	data := []byte(`{"type":"unknown-type","foo":"bar"}`)
	_, err := Unmarshal(data)
	if err == nil {
		t.Fatal("expected error for unknown message type, got nil")
	}
	if !strings.Contains(err.Error(), "unknown message type") {
		t.Errorf("error = %q, want it to contain \"unknown message type\"", err.Error())
	}
}

func TestUnmarshal_MalformedJSON(t *testing.T) {
	t.Parallel()

	_, err := Unmarshal([]byte(`{not json`))
	if err == nil {
		t.Fatal("expected error for malformed JSON, got nil")
	}
}

func TestUnmarshal_MissingType(t *testing.T) {
	t.Parallel()

	data := []byte(`{"peerId":"home-server","publicKey":"abc"}`)
	_, err := Unmarshal(data)
	if err == nil {
		t.Fatal("expected error for missing type field, got nil")
	}
	// Empty string is not a known type, so it should fail with "unknown message type".
	if !strings.Contains(err.Error(), "unknown message type") {
		t.Errorf("error = %q, want it to contain \"unknown message type\"", err.Error())
	}
}

func TestMessageType_Values(t *testing.T) {
	t.Parallel()

	tests := []struct {
		msg     Message
		wantTyp string
	}{
		{&JoinMessage{}, "join"},
		{&OfferMessage{}, "offer"},
		{&AnswerMessage{}, "answer"},
		{&ICECandidateMessage{}, "ice-candidate"},
		{&PeersMessage{}, "peers"},
		{&PeerLeftMessage{}, "peer-left"},
	}

	for _, tt := range tests {
		if got := tt.msg.MessageType(); got != tt.wantTyp {
			t.Errorf("%T.MessageType() = %q, want %q", tt.msg, got, tt.wantTyp)
		}
	}
}
