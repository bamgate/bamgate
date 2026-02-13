package tunnel

import (
	"encoding/hex"
	"fmt"
	"strings"

	"github.com/kuuji/bamgate/internal/config"
)

// DeviceConfig holds the WireGuard device-level configuration.
type DeviceConfig struct {
	// PrivateKey is the device's WireGuard private key.
	PrivateKey config.Key
}

// PeerConfig holds the WireGuard configuration for a single peer.
type PeerConfig struct {
	// PublicKey is the peer's WireGuard public key.
	PublicKey config.Key

	// Endpoint is the peer's endpoint string. For the bridge Bind, this is
	// the peer ID — Bind.ParseEndpoint converts it to a bridge.Endpoint
	// that routes Send calls to the correct data channel.
	Endpoint string

	// AllowedIPs is the list of IP prefixes routed through this peer
	// (CIDR notation, e.g. "10.0.0.2/32", "0.0.0.0/0").
	AllowedIPs []string

	// PersistentKeepalive is the keepalive interval in seconds. Zero disables it.
	PersistentKeepalive int
}

// hexKey returns the hex-encoded string of a WireGuard key.
// The UAPI/IPC format requires hex encoding (not base64).
func hexKey(k config.Key) string {
	return hex.EncodeToString(k[:])
}

// BuildUAPIConfig generates the UAPI/IPC configuration string for wireguard-go's
// Device.IpcSet method. The format is newline-delimited key=value pairs.
//
// Device-level keys come first, then peer sections (each starting with public_key=).
func BuildUAPIConfig(device DeviceConfig, peers []PeerConfig) string {
	var b strings.Builder

	// Device-level configuration.
	fmt.Fprintf(&b, "private_key=%s\n", hexKey(device.PrivateKey))

	// Listen port is 0 — we use a custom conn.Bind, not real UDP.
	b.WriteString("listen_port=0\n")

	// Peer configuration.
	for _, p := range peers {
		fmt.Fprintf(&b, "public_key=%s\n", hexKey(p.PublicKey))

		if p.Endpoint != "" {
			fmt.Fprintf(&b, "endpoint=%s\n", p.Endpoint)
		}

		b.WriteString("replace_allowed_ips=true\n")

		for _, ip := range p.AllowedIPs {
			fmt.Fprintf(&b, "allowed_ip=%s\n", ip)
		}

		if p.PersistentKeepalive > 0 {
			fmt.Fprintf(&b, "persistent_keepalive_interval=%d\n", p.PersistentKeepalive)
		}
	}

	return b.String()
}

// BuildPeerUAPIConfig generates the UAPI configuration for adding a single peer.
func BuildPeerUAPIConfig(peer PeerConfig) string {
	var b strings.Builder

	fmt.Fprintf(&b, "public_key=%s\n", hexKey(peer.PublicKey))

	if peer.Endpoint != "" {
		fmt.Fprintf(&b, "endpoint=%s\n", peer.Endpoint)
	}

	b.WriteString("replace_allowed_ips=true\n")

	for _, ip := range peer.AllowedIPs {
		fmt.Fprintf(&b, "allowed_ip=%s\n", ip)
	}

	if peer.PersistentKeepalive > 0 {
		fmt.Fprintf(&b, "persistent_keepalive_interval=%d\n", peer.PersistentKeepalive)
	}

	return b.String()
}

// BuildRemovePeerUAPIConfig generates the UAPI configuration for removing a peer.
func BuildRemovePeerUAPIConfig(publicKey config.Key) string {
	return fmt.Sprintf("public_key=%s\nremove=true\n", hexKey(publicKey))
}
