package tunnel

import (
	"encoding/hex"
	"strings"
	"testing"

	"github.com/kuuji/riftgate/internal/config"
)

func TestHexKey(t *testing.T) {
	t.Parallel()

	// Create a known key and verify hex encoding.
	var k config.Key
	for i := range k {
		k[i] = byte(i)
	}

	got := hexKey(k)
	want := hex.EncodeToString(k[:])

	if got != want {
		t.Errorf("hexKey() = %q, want %q", got, want)
	}

	// Must be 64 hex characters (32 bytes).
	if len(got) != 64 {
		t.Errorf("hexKey() length = %d, want 64", len(got))
	}
}

func TestBuildUAPIConfig_DeviceOnly(t *testing.T) {
	t.Parallel()

	privKey := mustGenerateKey(t)

	cfg := DeviceConfig{PrivateKey: privKey}
	uapi := BuildUAPIConfig(cfg, nil)

	assertContains(t, uapi, "private_key="+hexKey(privKey))
	assertContains(t, uapi, "listen_port=0")

	// Should not contain any peer configuration.
	if strings.Contains(uapi, "public_key=") {
		t.Error("expected no public_key in device-only config")
	}
}

func TestBuildUAPIConfig_WithPeers(t *testing.T) {
	t.Parallel()

	privKey := mustGenerateKey(t)
	peerKey1 := config.PublicKey(mustGenerateKey(t))
	peerKey2 := config.PublicKey(mustGenerateKey(t))

	cfg := DeviceConfig{PrivateKey: privKey}
	peers := []PeerConfig{
		{
			PublicKey:           peerKey1,
			AllowedIPs:          []string{"10.0.0.2/32"},
			PersistentKeepalive: 25,
		},
		{
			PublicKey:  peerKey2,
			AllowedIPs: []string{"10.0.0.3/32", "192.168.1.0/24"},
		},
	}

	uapi := BuildUAPIConfig(cfg, peers)

	// Device config.
	assertContains(t, uapi, "private_key="+hexKey(privKey))
	assertContains(t, uapi, "listen_port=0")

	// Peer 1.
	assertContains(t, uapi, "public_key="+hexKey(peerKey1))
	assertContains(t, uapi, "allowed_ip=10.0.0.2/32")
	assertContains(t, uapi, "persistent_keepalive_interval=25")

	// Peer 2.
	assertContains(t, uapi, "public_key="+hexKey(peerKey2))
	assertContains(t, uapi, "allowed_ip=10.0.0.3/32")
	assertContains(t, uapi, "allowed_ip=192.168.1.0/24")
}

func TestBuildUAPIConfig_PeerOrdering(t *testing.T) {
	t.Parallel()

	privKey := mustGenerateKey(t)
	peerKey1 := config.PublicKey(mustGenerateKey(t))
	peerKey2 := config.PublicKey(mustGenerateKey(t))

	cfg := DeviceConfig{PrivateKey: privKey}
	peers := []PeerConfig{
		{PublicKey: peerKey1, AllowedIPs: []string{"10.0.0.2/32"}},
		{PublicKey: peerKey2, AllowedIPs: []string{"10.0.0.3/32"}},
	}

	uapi := BuildUAPIConfig(cfg, peers)

	// private_key must come before any public_key.
	privIdx := strings.Index(uapi, "private_key=")
	pubIdx := strings.Index(uapi, "public_key=")
	if privIdx >= pubIdx {
		t.Error("private_key must appear before public_key in UAPI config")
	}

	// First peer's public_key must come before second peer's.
	firstPeerIdx := strings.Index(uapi, "public_key="+hexKey(peerKey1))
	secondPeerIdx := strings.Index(uapi, "public_key="+hexKey(peerKey2))
	if firstPeerIdx >= secondPeerIdx {
		t.Error("peers should appear in order")
	}
}

func TestBuildPeerUAPIConfig(t *testing.T) {
	t.Parallel()

	peerKey := config.PublicKey(mustGenerateKey(t))
	peer := PeerConfig{
		PublicKey:           peerKey,
		AllowedIPs:          []string{"10.0.0.5/32"},
		PersistentKeepalive: 15,
	}

	uapi := BuildPeerUAPIConfig(peer)

	assertContains(t, uapi, "public_key="+hexKey(peerKey))
	assertContains(t, uapi, "replace_allowed_ips=true")
	assertContains(t, uapi, "allowed_ip=10.0.0.5/32")
	assertContains(t, uapi, "persistent_keepalive_interval=15")

	// Should not contain device-level keys.
	if strings.Contains(uapi, "private_key=") {
		t.Error("peer config should not contain private_key")
	}
}

func TestBuildPeerUAPIConfig_NoKeepalive(t *testing.T) {
	t.Parallel()

	peerKey := config.PublicKey(mustGenerateKey(t))
	peer := PeerConfig{
		PublicKey:  peerKey,
		AllowedIPs: []string{"10.0.0.5/32"},
	}

	uapi := BuildPeerUAPIConfig(peer)

	if strings.Contains(uapi, "persistent_keepalive_interval") {
		t.Error("config with zero keepalive should not contain persistent_keepalive_interval")
	}
}

func TestBuildRemovePeerUAPIConfig(t *testing.T) {
	t.Parallel()

	peerKey := config.PublicKey(mustGenerateKey(t))
	uapi := BuildRemovePeerUAPIConfig(peerKey)

	assertContains(t, uapi, "public_key="+hexKey(peerKey))
	assertContains(t, uapi, "remove=true")
}

func TestHexKey_AllZeros(t *testing.T) {
	t.Parallel()

	var k config.Key
	got := hexKey(k)
	want := strings.Repeat("00", 32)
	if got != want {
		t.Errorf("hexKey(zero) = %q, want %q", got, want)
	}
}

func TestBuildUAPIConfig_MultipleAllowedIPs(t *testing.T) {
	t.Parallel()

	privKey := mustGenerateKey(t)
	peerKey := config.PublicKey(mustGenerateKey(t))

	cfg := DeviceConfig{PrivateKey: privKey}
	peers := []PeerConfig{
		{
			PublicKey:  peerKey,
			AllowedIPs: []string{"10.0.0.0/24", "192.168.0.0/16", "0.0.0.0/0"},
		},
	}

	uapi := BuildUAPIConfig(cfg, peers)

	// All three allowed IPs should be present.
	assertContains(t, uapi, "allowed_ip=10.0.0.0/24")
	assertContains(t, uapi, "allowed_ip=192.168.0.0/16")
	assertContains(t, uapi, "allowed_ip=0.0.0.0/0")
}

// --- helpers ---

func mustGenerateKey(t *testing.T) config.Key {
	t.Helper()
	k, err := config.GeneratePrivateKey()
	if err != nil {
		t.Fatalf("GeneratePrivateKey() error: %v", err)
	}
	return k
}

func assertContains(t *testing.T, s, substr string) {
	t.Helper()
	if !strings.Contains(s, substr) {
		t.Errorf("expected string to contain %q, got:\n%s", substr, s)
	}
}
