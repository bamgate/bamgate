package stun

import (
	"encoding/binary"
	"net"
	"testing"
)

func TestMessageType_RoundTrip(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		method int
		class  int
	}{
		{"Binding Request", MethodBinding, ClassRequest},
		{"Binding Success", MethodBinding, ClassSuccessResponse},
		{"Allocate Request", MethodAllocate, ClassRequest},
		{"Allocate Success", MethodAllocate, ClassSuccessResponse},
		{"Allocate Error", MethodAllocate, ClassErrorResponse},
		{"Refresh Request", MethodRefresh, ClassRequest},
		{"Send Indication", MethodSend, ClassIndication},
		{"Data Indication", MethodData, ClassIndication},
		{"CreatePermission Request", MethodCreatePermission, ClassRequest},
		{"ChannelBind Request", MethodChannelBind, ClassRequest},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			msgType := MessageType(tt.method, tt.class)
			gotMethod, gotClass := ParseType(msgType)
			if gotMethod != tt.method {
				t.Errorf("method: got %#x, want %#x", gotMethod, tt.method)
			}
			if gotClass != tt.class {
				t.Errorf("class: got %d, want %d", gotClass, tt.class)
			}
		})
	}
}

func TestParseAndBuild_BindingRequest(t *testing.T) {
	t.Parallel()

	txID := [12]byte{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12}
	built := NewBuilder(MethodBinding, ClassRequest, txID).Build(nil)

	if !IsSTUN(built) {
		t.Fatal("built message not recognized as STUN")
	}
	if IsChannelData(built) {
		t.Fatal("STUN message misidentified as ChannelData")
	}

	msg, err := Parse(built)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if msg.Method != MethodBinding {
		t.Errorf("method: got %#x, want %#x", msg.Method, MethodBinding)
	}
	if msg.Class != ClassRequest {
		t.Errorf("class: got %d, want %d", msg.Class, ClassRequest)
	}
	if msg.TransactionID != txID {
		t.Errorf("txID: got %v, want %v", msg.TransactionID, txID)
	}
}

func TestParseAndBuild_AllocateErrorResponse(t *testing.T) {
	t.Parallel()

	txID := [12]byte{0xAA, 0xBB, 0xCC, 0xDD}
	built := NewBuilder(MethodAllocate, ClassErrorResponse, txID).
		AddErrorCode(401, "Unauthorized").
		AddRealm("bamgate").
		AddNonce("test-nonce-123").
		Build(nil)

	msg, err := Parse(built)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if msg.Method != MethodAllocate || msg.Class != ClassErrorResponse {
		t.Fatalf("type: got method=%#x class=%d", msg.Method, msg.Class)
	}

	// Check error code.
	ec := msg.GetAttr(AttrErrorCode)
	if ec == nil {
		t.Fatal("missing ERROR-CODE")
	}
	code := int(ec[2])*100 + int(ec[3])
	if code != 401 {
		t.Errorf("error code: got %d, want 401", code)
	}

	if msg.GetRealm() != "bamgate" {
		t.Errorf("realm: got %q, want %q", msg.GetRealm(), "bamgate")
	}
	if msg.GetNonce() != "test-nonce-123" {
		t.Errorf("nonce: got %q, want %q", msg.GetNonce(), "test-nonce-123")
	}
}

func TestXORAddress_IPv4_RoundTrip(t *testing.T) {
	t.Parallel()

	txID := [12]byte{0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08, 0x09, 0x0A, 0x0B, 0x0C}
	addr := XORAddress{IP: net.ParseIP("192.168.1.1"), Port: 50000}

	built := NewBuilder(MethodAllocate, ClassSuccessResponse, txID).
		AddXORAddress(AttrXORRelayedAddress, addr).
		Build(nil)

	msg, err := Parse(built)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	relayedRaw := msg.GetAttr(AttrXORRelayedAddress)
	if relayedRaw == nil {
		t.Fatal("missing XOR-RELAYED-ADDRESS")
	}

	decoded := decodeXORAddress(relayedRaw, msg.TransactionID)
	if !decoded.IP.Equal(addr.IP) {
		t.Errorf("IP: got %v, want %v", decoded.IP, addr.IP)
	}
	if decoded.Port != addr.Port {
		t.Errorf("Port: got %d, want %d", decoded.Port, addr.Port)
	}
}

func TestXORAddress_IPv6_RoundTrip(t *testing.T) {
	t.Parallel()

	txID := [12]byte{0xAA, 0xBB, 0xCC, 0xDD, 0xEE, 0xFF, 0x11, 0x22, 0x33, 0x44, 0x55, 0x66}
	addr := XORAddress{IP: net.ParseIP("2001:db8::1"), Port: 3478}

	built := NewBuilder(MethodAllocate, ClassSuccessResponse, txID).
		AddXORAddress(AttrXORRelayedAddress, addr).
		Build(nil)

	msg, err := Parse(built)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	relayedRaw := msg.GetAttr(AttrXORRelayedAddress)
	decoded := decodeXORAddress(relayedRaw, msg.TransactionID)
	if !decoded.IP.Equal(addr.IP) {
		t.Errorf("IP: got %v, want %v", decoded.IP, addr.IP)
	}
	if decoded.Port != addr.Port {
		t.Errorf("Port: got %d, want %d", decoded.Port, addr.Port)
	}
}

func TestMessageIntegrity_Valid(t *testing.T) {
	t.Parallel()

	txID := [12]byte{0x01}
	authKey := DeriveAuthKey("user", "realm", "pass")

	built := NewBuilder(MethodAllocate, ClassSuccessResponse, txID).
		AddLifetime(600).
		AddXORAddress(AttrXORRelayedAddress, XORAddress{IP: net.ParseIP("10.0.0.1"), Port: 50000}).
		Build(authKey)

	if err := CheckIntegrity(built, authKey); err != nil {
		t.Fatalf("valid integrity rejected: %v", err)
	}
}

func TestMessageIntegrity_Invalid(t *testing.T) {
	t.Parallel()

	txID := [12]byte{0x01}
	authKey := DeriveAuthKey("user", "realm", "pass")
	wrongKey := DeriveAuthKey("user", "realm", "wrong")

	built := NewBuilder(MethodAllocate, ClassSuccessResponse, txID).
		AddLifetime(600).
		Build(authKey)

	if err := CheckIntegrity(built, wrongKey); err == nil {
		t.Fatal("wrong key accepted")
	}
}

func TestFingerprint_Valid(t *testing.T) {
	t.Parallel()

	txID := [12]byte{0x42}
	built := NewBuilder(MethodBinding, ClassRequest, txID).Build(nil)

	if err := CheckFingerprint(built); err != nil {
		t.Fatalf("valid fingerprint rejected: %v", err)
	}
}

func TestFingerprint_Tampered(t *testing.T) {
	t.Parallel()

	txID := [12]byte{0x42}
	built := NewBuilder(MethodBinding, ClassRequest, txID).Build(nil)

	// Tamper with a byte in the body.
	built[HeaderSize] ^= 0xFF

	if err := CheckFingerprint(built); err == nil {
		t.Fatal("tampered fingerprint accepted")
	}
}

func TestChannelData_RoundTrip(t *testing.T) {
	t.Parallel()

	payload := []byte("hello, turn relay")
	var ch uint16 = 0x4000

	frame := BuildChannelData(ch, payload)

	if !IsChannelData(frame) {
		t.Fatal("not recognized as ChannelData")
	}
	if IsSTUN(frame) {
		t.Fatal("ChannelData misidentified as STUN")
	}

	parsed, err := ParseChannelData(frame)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if parsed.ChannelNumber != ch {
		t.Errorf("channel: got %#x, want %#x", parsed.ChannelNumber, ch)
	}
	if string(parsed.Data) != string(payload) {
		t.Errorf("data: got %q, want %q", parsed.Data, payload)
	}
}

func TestChannelData_Padding(t *testing.T) {
	t.Parallel()

	// Payload of 3 bytes should be padded to 4 bytes total.
	payload := []byte{0x01, 0x02, 0x03}
	frame := BuildChannelData(0x4001, payload)

	// Total frame: 4 (header) + 4 (padded payload) = 8 bytes.
	if len(frame) != 8 {
		t.Errorf("frame length: got %d, want 8", len(frame))
	}

	// Length field should still say 3.
	length := binary.BigEndian.Uint16(frame[2:4])
	if length != 3 {
		t.Errorf("length field: got %d, want 3", length)
	}

	parsed, err := ParseChannelData(frame)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(parsed.Data) != 3 {
		t.Errorf("parsed data length: got %d, want 3", len(parsed.Data))
	}
}

func TestIsSTUN_TooShort(t *testing.T) {
	t.Parallel()
	if IsSTUN([]byte{0, 0, 0}) {
		t.Error("too-short data recognized as STUN")
	}
}

func TestIsChannelData_TooShort(t *testing.T) {
	t.Parallel()
	if IsChannelData([]byte{0x40}) {
		t.Error("too-short data recognized as ChannelData")
	}
}

func TestParse_TooShort(t *testing.T) {
	t.Parallel()
	_, err := Parse([]byte{0, 0})
	if err == nil {
		t.Error("parsing too-short message succeeded")
	}
}

func TestGetAttrs_Multiple(t *testing.T) {
	t.Parallel()

	txID := [12]byte{0x01}
	addr1 := XORAddress{IP: net.ParseIP("10.0.0.1"), Port: 3478}
	addr2 := XORAddress{IP: net.ParseIP("10.0.0.2"), Port: 3479}

	built := NewBuilder(MethodCreatePermission, ClassRequest, txID).
		AddXORAddress(AttrXORPeerAddress, addr1).
		AddXORAddress(AttrXORPeerAddress, addr2).
		Build(nil)

	msg, err := Parse(built)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	addrs := msg.GetXORPeerAddresses()
	if len(addrs) != 2 {
		t.Fatalf("peer addresses: got %d, want 2", len(addrs))
	}
	if !addrs[0].IP.Equal(addr1.IP) || addrs[0].Port != addr1.Port {
		t.Errorf("addr[0]: got %v:%d, want %v:%d", addrs[0].IP, addrs[0].Port, addr1.IP, addr1.Port)
	}
	if !addrs[1].IP.Equal(addr2.IP) || addrs[1].Port != addr2.Port {
		t.Errorf("addr[1]: got %v:%d, want %v:%d", addrs[1].IP, addrs[1].Port, addr2.IP, addr2.Port)
	}
}

func TestLifetime_RoundTrip(t *testing.T) {
	t.Parallel()

	txID := [12]byte{}
	built := NewBuilder(MethodRefresh, ClassRequest, txID).
		AddLifetime(3600).
		Build(nil)

	msg, err := Parse(built)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if msg.GetLifetime() != 3600 {
		t.Errorf("lifetime: got %d, want 3600", msg.GetLifetime())
	}
}

func TestChannelNumber_RoundTrip(t *testing.T) {
	t.Parallel()

	txID := [12]byte{}
	built := NewBuilder(MethodChannelBind, ClassRequest, txID).
		AddChannelNumber(0x4000).
		Build(nil)

	msg, err := Parse(built)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if msg.GetChannelNumber() != 0x4000 {
		t.Errorf("channel number: got %#x, want 0x4000", msg.GetChannelNumber())
	}
}

func TestData_RoundTrip(t *testing.T) {
	t.Parallel()

	txID := [12]byte{}
	payload := []byte{0xDE, 0xAD, 0xBE, 0xEF, 0xCA, 0xFE}
	built := NewBuilder(MethodSend, ClassIndication, txID).
		AddData(payload).
		AddXORAddress(AttrXORPeerAddress, XORAddress{IP: net.ParseIP("10.0.0.1"), Port: 50000}).
		BuildNoFingerprint(nil)

	msg, err := Parse(built)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	data := msg.GetData()
	if len(data) != len(payload) {
		t.Fatalf("data length: got %d, want %d", len(data), len(payload))
	}
	for i, b := range data {
		if b != payload[i] {
			t.Errorf("data[%d]: got %#x, want %#x", i, b, payload[i])
		}
	}
}

func TestAllocateDance(t *testing.T) {
	t.Parallel()

	// Simulate the full TURN Allocate two-phase dance.
	// Phase 1: Client sends unauthenticated Allocate.
	txID1 := [12]byte{0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08, 0x09, 0x0A, 0x0B, 0x0C}
	req1 := NewBuilder(MethodAllocate, ClassRequest, txID1).
		AddRaw(AttrRequestedTransport, []byte{0x11, 0, 0, 0}). // UDP
		Build(nil)

	msg1, err := Parse(req1)
	if err != nil {
		t.Fatalf("parse phase 1 request: %v", err)
	}
	if msg1.Method != MethodAllocate || msg1.Class != ClassRequest {
		t.Fatalf("phase 1: wrong type")
	}
	if msg1.GetUsername() != "" {
		t.Error("phase 1 should have no USERNAME")
	}

	// Server responds with 401.
	resp1 := NewResponse(&msg1, ClassErrorResponse).
		AddErrorCode(401, "Unauthorized").
		AddRealm("bamgate").
		AddNonce("test-nonce-abc").
		Build(nil)

	respMsg1, err := Parse(resp1)
	if err != nil {
		t.Fatalf("parse phase 1 response: %v", err)
	}
	if respMsg1.Class != ClassErrorResponse {
		t.Fatalf("phase 1 response: wrong class %d", respMsg1.Class)
	}
	ec := respMsg1.GetAttr(AttrErrorCode)
	if ec == nil {
		t.Fatal("missing ERROR-CODE")
	}
	code := int(ec[2])*100 + int(ec[3])
	if code != 401 {
		t.Errorf("error code: got %d, want 401", code)
	}
	if respMsg1.GetRealm() != "bamgate" {
		t.Errorf("realm: got %q", respMsg1.GetRealm())
	}
	if respMsg1.GetNonce() != "test-nonce-abc" {
		t.Errorf("nonce: got %q", respMsg1.GetNonce())
	}

	// Phase 2: Client sends authenticated Allocate.
	authKey := DeriveAuthKey("user123", "bamgate", "password123")
	txID2 := [12]byte{0x11, 0x12, 0x13, 0x14, 0x15, 0x16, 0x17, 0x18, 0x19, 0x1A, 0x1B, 0x1C}
	req2 := NewBuilder(MethodAllocate, ClassRequest, txID2).
		AddRaw(AttrRequestedTransport, []byte{0x11, 0, 0, 0}).
		AddUsername("user123").
		AddRealm("bamgate").
		AddNonce("test-nonce-abc").
		Build(authKey)

	// Verify MESSAGE-INTEGRITY and FINGERPRINT.
	if err := CheckIntegrity(req2, authKey); err != nil {
		t.Fatalf("phase 2 integrity check: %v", err)
	}
	if err := CheckFingerprint(req2); err != nil {
		t.Fatalf("phase 2 fingerprint check: %v", err)
	}

	msg2, err := Parse(req2)
	if err != nil {
		t.Fatalf("parse phase 2 request: %v", err)
	}
	if msg2.GetUsername() != "user123" {
		t.Errorf("username: got %q", msg2.GetUsername())
	}

	// Server responds with success + relay address.
	relayAddr := XORAddress{IP: net.ParseIP("10.255.0.1"), Port: 50000}
	resp2 := NewResponse(&msg2, ClassSuccessResponse).
		AddXORAddress(AttrXORRelayedAddress, relayAddr).
		AddXORAddress(AttrXORMappedAddress, XORAddress{IP: net.ParseIP("203.0.113.1"), Port: 12345}).
		AddLifetime(600).
		Build(authKey)

	if err := CheckIntegrity(resp2, authKey); err != nil {
		t.Fatalf("phase 2 response integrity: %v", err)
	}

	respMsg2, err := Parse(resp2)
	if err != nil {
		t.Fatalf("parse phase 2 response: %v", err)
	}
	if respMsg2.Class != ClassSuccessResponse {
		t.Fatalf("phase 2 response: wrong class")
	}
	if respMsg2.GetLifetime() != 600 {
		t.Errorf("lifetime: got %d", respMsg2.GetLifetime())
	}

	// Verify relay address is correctly encoded and decoded.
	relayRaw := respMsg2.GetAttr(AttrXORRelayedAddress)
	if relayRaw == nil {
		t.Fatal("missing relay address")
	}
	decoded := decodeXORAddress(relayRaw, respMsg2.TransactionID)
	if !decoded.IP.Equal(relayAddr.IP) || decoded.Port != relayAddr.Port {
		t.Errorf("relay addr: got %v:%d, want %v:%d", decoded.IP, decoded.Port, relayAddr.IP, relayAddr.Port)
	}
}

func TestBuildNoFingerprint(t *testing.T) {
	t.Parallel()

	txID := [12]byte{0x42}
	built := NewBuilder(MethodSend, ClassIndication, txID).
		AddData([]byte("test")).
		BuildNoFingerprint(nil)

	// Should not have FINGERPRINT at the end.
	if len(built) >= 8 {
		lastAttrType := binary.BigEndian.Uint16(built[len(built)-8 : len(built)-6])
		if lastAttrType == AttrFingerprint {
			t.Error("BuildNoFingerprint produced a message with FINGERPRINT")
		}
	}

	// Should still be parseable as STUN.
	msg, err := Parse(built)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if msg.Method != MethodSend || msg.Class != ClassIndication {
		t.Errorf("type: method=%#x class=%d", msg.Method, msg.Class)
	}
}
