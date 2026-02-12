package config

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/BurntSushi/toml"
)

// DefaultSTUNServers are the public STUN servers used when none are configured.
var DefaultSTUNServers = []string{
	"stun:stun.cloudflare.com:3478",
	"stun:stun.l.google.com:19302",
}

// Config is the top-level configuration for riftgate.
// It is persisted as a TOML file at DefaultConfigPath().
type Config struct {
	Cloudflare CloudflareConfig `toml:"cloudflare"`
	Network    NetworkConfig    `toml:"network"`
	Device     DeviceConfig     `toml:"device"`
	STUN       STUNConfig       `toml:"stun"`
	WebRTC     WebRTCConfig     `toml:"webrtc"`
}

// CloudflareConfig stores Cloudflare account credentials used for deploying
// and managing the signaling worker. These fields are populated by `riftgate setup`.
type CloudflareConfig struct {
	// APIToken is the Cloudflare API token with Workers Scripts:Edit and
	// Account Settings:Read permissions.
	APIToken string `toml:"api_token,omitempty"`

	// AccountID is the Cloudflare account ID associated with the API token.
	AccountID string `toml:"account_id,omitempty"`

	// WorkerName is the name of the deployed Cloudflare Worker (default: "riftgate").
	WorkerName string `toml:"worker_name,omitempty"`
}

// NetworkConfig identifies the riftgate network and its signaling server.
type NetworkConfig struct {
	// Name is a human-readable name for this network.
	Name string `toml:"name"`

	// ServerURL is the HTTPS/WSS URL of the Cloudflare Worker signaling server.
	ServerURL string `toml:"server_url"`

	// AuthToken is the bearer token used to authenticate with the signaling server.
	AuthToken string `toml:"auth_token"`

	// TURNSecret is the shared secret used to derive time-limited TURN credentials.
	TURNSecret string `toml:"turn_secret"`
}

// DeviceConfig identifies this device within the network.
type DeviceConfig struct {
	// Name is a human-readable name for this device (e.g. "home-server", "laptop").
	Name string `toml:"name"`

	// PrivateKey is the WireGuard Curve25519 private key for this device.
	// It is stored as base64 and decoded via Key.UnmarshalText.
	PrivateKey Key `toml:"private_key"`

	// Address is the WireGuard interface address in CIDR notation (e.g. "10.0.0.1/24").
	Address string `toml:"address"`

	// Routes is a list of additional subnets (CIDR notation) reachable through
	// this device. These are advertised to peers via signaling and added as
	// WireGuard AllowedIPs on remote peers. For example, a home server might
	// advertise ["192.168.1.0/24"] so remote peers can reach the home LAN.
	Routes []string `toml:"routes,omitempty"`

	// AcceptRoutes controls whether this device installs subnet routes
	// advertised by remote peers. When false (the default), only the peer's
	// /32 tunnel address is added to WireGuard AllowedIPs — advertised LAN
	// subnets are ignored. Set to true only when you know the remote subnets
	// do not conflict with your local network.
	AcceptRoutes bool `toml:"accept_routes,omitempty"`
}

// STUNConfig lists the STUN servers used for ICE NAT traversal.
type STUNConfig struct {
	// Servers is a list of STUN server URIs (e.g. "stun:stun.cloudflare.com:3478").
	Servers []string `toml:"servers"`
}

// WebRTCConfig controls data channel behavior.
type WebRTCConfig struct {
	// Ordered controls whether the data channel delivers messages in order.
	// Must be false for WireGuard (UDP-like behavior).
	Ordered bool `toml:"ordered"`

	// MaxRetransmits is the maximum number of retransmission attempts for the
	// data channel. Must be 0 for WireGuard (unreliable delivery).
	MaxRetransmits int `toml:"max_retransmits"`
}

// DefaultConfig returns a Config populated with sensible defaults.
// Network-specific fields (name, server_url, auth_token, turn_secret) and
// device-specific fields (name, private_key, address) are left empty and
// must be filled in by the user or by `riftgate init`.
func DefaultConfig() *Config {
	return &Config{
		STUN: STUNConfig{
			Servers: append([]string(nil), DefaultSTUNServers...),
		},
		WebRTC: WebRTCConfig{
			Ordered:        false,
			MaxRetransmits: 0,
		},
	}
}

// DefaultConfigPath returns the default path for the riftgate config file.
// It respects $XDG_CONFIG_HOME if set, otherwise falls back to ~/.config.
func DefaultConfigPath() (string, error) {
	dir := os.Getenv("XDG_CONFIG_HOME")
	if dir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("determining home directory: %w", err)
		}
		dir = filepath.Join(home, ".config")
	}
	return filepath.Join(dir, "riftgate", "config.toml"), nil
}

// ConfigPathForUser returns the config path for a specific user's home directory.
// This is used when running as root (via sudo) to write config to the real user's
// home directory instead of root's.
func ConfigPathForUser(homeDir string) string {
	return filepath.Join(homeDir, ".config", "riftgate", "config.toml")
}

// LoadConfig reads and decodes a TOML config file from the given path.
// If the file does not exist, it returns an error wrapping fs.ErrNotExist.
// After loading, defaults are applied for any unset optional fields.
func LoadConfig(path string) (*Config, error) {
	cfg := DefaultConfig()
	if _, err := toml.DecodeFile(path, cfg); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, fmt.Errorf("config file not found: %w", err)
		}
		return nil, fmt.Errorf("reading config file %s: %w", path, err)
	}
	applyDefaults(cfg)
	return cfg, nil
}

// SaveConfig encodes the config as TOML and writes it to the given path.
// Parent directories are created if they don't exist. The file is written
// with mode 0600 (owner-only read/write) since it contains secrets.
func SaveConfig(path string, cfg *Config) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return fmt.Errorf("creating config directory %s: %w", dir, err)
	}

	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0600)
	if err != nil {
		return fmt.Errorf("creating config file %s: %w", path, err)
	}
	defer f.Close()

	enc := toml.NewEncoder(f)
	if err := enc.Encode(cfg); err != nil {
		return fmt.Errorf("encoding config: %w", err)
	}

	return nil
}

// PublicKey derives the WireGuard public key from the device's private key.
// Returns an error if the private key is not set.
func (c *Config) PublicKey() (Key, error) {
	if c.Device.PrivateKey.IsZero() {
		return Key{}, errors.New("device private key is not set")
	}
	return PublicKey(c.Device.PrivateKey), nil
}

// applyDefaults fills in default values for optional fields that are
// zero-valued after TOML decoding.
func applyDefaults(cfg *Config) {
	if len(cfg.STUN.Servers) == 0 {
		cfg.STUN.Servers = append([]string(nil), DefaultSTUNServers...)
	}
}
