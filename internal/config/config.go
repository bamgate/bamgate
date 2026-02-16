package config

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/BurntSushi/toml"
)

// DefaultSTUNServers are the public STUN servers used when none are configured.
var DefaultSTUNServers = []string{
	"stun:stun.cloudflare.com:3478",
	"stun:stun.l.google.com:19302",
}

// DefaultConfigDir is the system-wide config directory for bamgate.
const DefaultConfigDir = "/etc/bamgate"

// secretsFileName is the name of the secrets file within the config directory.
const secretsFileName = "secrets.toml"

// Config is the top-level configuration for bamgate.
// It is persisted as a TOML file at DefaultConfigPath().
type Config struct {
	Cloudflare CloudflareConfig `toml:"cloudflare"`
	Network    NetworkConfig    `toml:"network"`
	Device     DeviceConfig     `toml:"device"`
	STUN       STUNConfig       `toml:"stun"`
	WebRTC     WebRTCConfig     `toml:"webrtc"`
}

// CloudflareConfig stores Cloudflare account credentials used for deploying
// and managing the signaling worker. These fields are populated by `bamgate setup`.
type CloudflareConfig struct {
	// APIToken is the Cloudflare API token with Workers Scripts:Edit and
	// Account Settings:Read permissions.
	APIToken string `toml:"api_token,omitempty"`

	// AccountID is the Cloudflare account ID associated with the API token.
	AccountID string `toml:"account_id,omitempty"`

	// WorkerName is the name of the deployed Cloudflare Worker (default: "bamgate").
	WorkerName string `toml:"worker_name,omitempty"`
}

// NetworkConfig identifies the bamgate network and its signaling server.
type NetworkConfig struct {
	// Name is a human-readable name for this network.
	Name string `toml:"name"`

	// ServerURL is the HTTPS/WSS URL of the Cloudflare Worker signaling server.
	ServerURL string `toml:"server_url"`

	// TURNSecret is the shared secret used to derive time-limited TURN credentials.
	// Received from the server during device registration.
	TURNSecret string `toml:"turn_secret"`

	// DeviceID is the unique identifier for this device, assigned by the server
	// during registration via POST /auth/register.
	DeviceID string `toml:"device_id"`

	// RefreshToken is the rolling refresh token used to obtain new JWTs.
	// It is rotated on every POST /auth/refresh call (30-day rolling window).
	RefreshToken string `toml:"refresh_token"`
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

	// ForceRelay forces all WebRTC connections to use the TURN relay,
	// bypassing direct (host/srflx) connectivity. Useful for testing
	// the TURN relay path or when direct connectivity is unreliable.
	ForceRelay bool `toml:"force_relay,omitempty"`
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

// configFile is the TOML representation for config.toml (world-readable, no secrets).
type configFile struct {
	Cloudflare cfConfigFile  `toml:"cloudflare"`
	Network    netConfigFile `toml:"network"`
	Device     devConfigFile `toml:"device"`
	STUN       STUNConfig    `toml:"stun"`
	WebRTC     WebRTCConfig  `toml:"webrtc"`
}

type cfConfigFile struct {
	AccountID  string `toml:"account_id,omitempty"`
	WorkerName string `toml:"worker_name,omitempty"`
}

type netConfigFile struct {
	Name      string `toml:"name"`
	ServerURL string `toml:"server_url"`
	DeviceID  string `toml:"device_id"`
}

type devConfigFile struct {
	Name         string   `toml:"name"`
	Address      string   `toml:"address"`
	Routes       []string `toml:"routes,omitempty"`
	AcceptRoutes bool     `toml:"accept_routes,omitempty"`
	ForceRelay   bool     `toml:"force_relay,omitempty"`
}

// secretsFile is the TOML representation for secrets.toml (0640, root + invoking user).
type secretsFile struct {
	Cloudflare cfSecretsFile  `toml:"cloudflare"`
	Network    netSecretsFile `toml:"network"`
	Device     devSecretsFile `toml:"device"`
}

type cfSecretsFile struct {
	APIToken string `toml:"api_token,omitempty"`
}

type netSecretsFile struct {
	TURNSecret   string `toml:"turn_secret"`
	RefreshToken string `toml:"refresh_token"`
}

type devSecretsFile struct {
	PrivateKey Key `toml:"private_key"`
}

// toConfigFile extracts the non-secret fields from a Config for config.toml.
func toConfigFile(cfg *Config) *configFile {
	return &configFile{
		Cloudflare: cfConfigFile{
			AccountID:  cfg.Cloudflare.AccountID,
			WorkerName: cfg.Cloudflare.WorkerName,
		},
		Network: netConfigFile{
			Name:      cfg.Network.Name,
			ServerURL: cfg.Network.ServerURL,
			DeviceID:  cfg.Network.DeviceID,
		},
		Device: devConfigFile{
			Name:         cfg.Device.Name,
			Address:      cfg.Device.Address,
			Routes:       cfg.Device.Routes,
			AcceptRoutes: cfg.Device.AcceptRoutes,
			ForceRelay:   cfg.Device.ForceRelay,
		},
		STUN:   cfg.STUN,
		WebRTC: cfg.WebRTC,
	}
}

// toSecretsFile extracts the secret fields from a Config for secrets.toml.
func toSecretsFile(cfg *Config) *secretsFile {
	return &secretsFile{
		Cloudflare: cfSecretsFile{
			APIToken: cfg.Cloudflare.APIToken,
		},
		Network: netSecretsFile{
			TURNSecret:   cfg.Network.TURNSecret,
			RefreshToken: cfg.Network.RefreshToken,
		},
		Device: devSecretsFile{
			PrivateKey: cfg.Device.PrivateKey,
		},
	}
}

// mergeSecrets overlays secret fields from a secretsFile onto a Config.
func mergeSecrets(cfg *Config, s *secretsFile) {
	cfg.Cloudflare.APIToken = s.Cloudflare.APIToken
	cfg.Network.TURNSecret = s.Network.TURNSecret
	cfg.Network.RefreshToken = s.Network.RefreshToken
	cfg.Device.PrivateKey = s.Device.PrivateKey
}

// DefaultConfig returns a Config populated with sensible defaults.
// Network-specific fields (name, server_url, auth_token, turn_secret) and
// device-specific fields (name, private_key, address) are left empty and
// must be filled in by the user or by `bamgate init`.
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

// DefaultConfigPath returns the default path for the bamgate config file.
// The config is stored at /etc/bamgate/config.toml since the daemon runs as root.
func DefaultConfigPath() (string, error) {
	return filepath.Join(DefaultConfigDir, "config.toml"), nil
}

// DefaultSecretsPath returns the default path for the bamgate secrets file.
// The secrets are stored at /etc/bamgate/secrets.toml with restricted permissions.
func DefaultSecretsPath() string {
	return filepath.Join(DefaultConfigDir, secretsFileName)
}

// SecretsPathFromConfig derives the secrets.toml path from a config.toml path.
// It replaces the filename, keeping secrets.toml alongside config.toml.
func SecretsPathFromConfig(configPath string) string {
	return filepath.Join(filepath.Dir(configPath), secretsFileName)
}

// LegacyConfigPath returns the old user-level config path (~/.config/bamgate/config.toml).
// This is used for migration detection when upgrading from older versions.
func LegacyConfigPath() (string, error) {
	dir := os.Getenv("XDG_CONFIG_HOME")
	if dir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("determining home directory: %w", err)
		}
		dir = filepath.Join(home, ".config")
	}
	return filepath.Join(dir, "bamgate", "config.toml"), nil
}

// LegacyConfigPathForUser returns the old user-level config path for a specific
// user's home directory. Used for migration detection during setup.
func LegacyConfigPathForUser(homeDir string) string {
	return filepath.Join(homeDir, ".config", "bamgate", "config.toml")
}

// LoadConfig reads config.toml and secrets.toml from the config directory,
// merging them into a single Config. If config.toml does not exist, it returns
// an error wrapping fs.ErrNotExist. If secrets.toml does not exist, the secret
// fields are left at their zero values (this supports loading on machines that
// joined a network but have not yet been fully set up, or for commands that
// only need non-secret fields).
//
// For commands that explicitly do not need secrets (and should work without
// root), use LoadPublicConfig instead.
func LoadConfig(path string) (*Config, error) {
	cfg, err := LoadPublicConfig(path)
	if err != nil {
		return nil, err
	}

	// Load secrets from the companion file.
	secretsPath := SecretsPathFromConfig(path)
	var sec secretsFile
	if _, err := toml.DecodeFile(secretsPath, &sec); err != nil {
		if !errors.Is(err, fs.ErrNotExist) {
			return nil, fmt.Errorf("reading secrets file %s: %w", secretsPath, err)
		}
		// secrets.toml missing — leave secret fields at zero values.
	} else {
		mergeSecrets(cfg, &sec)
	}

	return cfg, nil
}

// LoadPublicConfig reads only config.toml (the world-readable, non-secret
// portion of the configuration). Use this for commands that do not need
// secrets and should work without root (e.g. "bamgate qr").
func LoadPublicConfig(path string) (*Config, error) {
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

// SaveConfig writes both config.toml and secrets.toml to the directory
// containing path. Parent directories are created with mode 0755 if they
// don't exist.
//
// When running via sudo, both files are chowned to root:<invoking-user-gid>
// so the invoking user can read and write them without sudo:
//   - config.toml:  0664 (world-readable, group-writable — no secrets)
//   - secrets.toml: 0660 (group-readable + group-writable — contains secrets)
func SaveConfig(path string, cfg *Config) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("creating config directory %s: %w", dir, err)
	}
	// Ensure directory is world-readable even if it existed with old 0700 perms.
	if err := os.Chmod(dir, 0755); err != nil {
		return fmt.Errorf("setting directory permissions on %s: %w", dir, err)
	}

	// Write config.toml (world-readable, group-writable — no secrets).
	if err := writeFile(path, 0664, toConfigFile(cfg)); err != nil {
		return fmt.Errorf("writing config file: %w", err)
	}
	applyUserOwnership(path)

	// Write secrets.toml (group-readable + group-writable — contains secrets).
	secretsPath := SecretsPathFromConfig(path)
	if err := writeFile(secretsPath, 0660, toSecretsFile(cfg)); err != nil {
		return fmt.Errorf("writing secrets file: %w", err)
	}
	applyUserOwnership(secretsPath)

	return nil
}

// SaveSecrets writes only the secrets.toml file for the given config path.
// Use this when only secret fields have changed (e.g. refresh token rotation)
// and re-writing config.toml is unnecessary.
func SaveSecrets(configPath string, cfg *Config) error {
	secretsPath := SecretsPathFromConfig(configPath)
	if err := writeFile(secretsPath, 0660, toSecretsFile(cfg)); err != nil {
		return fmt.Errorf("writing secrets file: %w", err)
	}
	applyUserOwnership(secretsPath)
	return nil
}

// applyUserOwnership sets group ownership on a config file so the user who
// ran sudo can read and write it without elevation. When running as root via
// sudo, the SUDO_GID environment variable identifies the invoking user's
// primary group. The file is chowned to root:<sudo-gid>.
//
// This is a best-effort operation — errors are silently ignored because the
// file is already written successfully and root can always access it.
func applyUserOwnership(path string) {
	// Only relevant when running as root.
	if os.Getuid() != 0 {
		return
	}

	gidStr := os.Getenv("SUDO_GID")
	if gidStr == "" {
		return
	}

	gid, err := strconv.Atoi(gidStr)
	if err != nil {
		return
	}

	// chown root:<sudo-user-gid>
	// 0 keeps root as owner; gid grants group access to the invoking user.
	_ = os.Chown(path, 0, gid)
}

// writeFile encodes v as TOML and writes it to path with the given file mode.
// If the file already exists with different permissions (e.g. during migration
// from the old monolithic 0600 format), the permissions are corrected.
func writeFile(path string, mode os.FileMode, v interface{}) error {
	var buf bytes.Buffer
	if err := toml.NewEncoder(&buf).Encode(v); err != nil {
		return fmt.Errorf("encoding TOML: %w", err)
	}

	if err := os.WriteFile(path, buf.Bytes(), mode); err != nil {
		return fmt.Errorf("writing %s: %w", path, err)
	}

	// Ensure permissions are correct even if the file already existed
	// with different permissions (WriteFile only sets mode on creation).
	if err := os.Chmod(path, mode); err != nil {
		return fmt.Errorf("setting permissions on %s: %w", path, err)
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

// ParseTOML decodes a TOML config from a string. This is used by the mobile
// binding layer where configs are passed as strings rather than file paths.
func ParseTOML(s string) (*Config, error) {
	cfg := DefaultConfig()
	if _, err := toml.Decode(s, cfg); err != nil {
		return nil, fmt.Errorf("decoding TOML config: %w", err)
	}
	applyDefaults(cfg)
	return cfg, nil
}

// MarshalTOML encodes a Config to a TOML string.
func MarshalTOML(cfg *Config) (string, error) {
	var buf bytes.Buffer
	enc := toml.NewEncoder(&buf)
	if err := enc.Encode(cfg); err != nil {
		return "", fmt.Errorf("encoding TOML config: %w", err)
	}
	return strings.TrimSpace(buf.String()), nil
}

// FixPermissions ensures the config directory and files have the correct
// permissions for the split config model. This should be called from commands
// that run as root (setup, up) to fix permissions from older versions.
//
// Directory: 0755 (world-traversable so non-root users can read config.toml).
// config.toml: 0664 (world-readable, group-writable).
// secrets.toml: 0660 (group-readable + group-writable).
// Both files chowned to root:<SUDO_GID> when running via sudo.
func FixPermissions(configPath string) error {
	dir := filepath.Dir(configPath)

	// Fix directory permissions (old versions used 0700).
	if info, err := os.Stat(dir); err == nil && info.IsDir() {
		if err := os.Chmod(dir, 0755); err != nil {
			return fmt.Errorf("setting directory permissions on %s: %w", dir, err)
		}
	}

	// Fix file permissions and ownership.
	if _, err := os.Stat(configPath); err == nil {
		_ = os.Chmod(configPath, 0664)
		applyUserOwnership(configPath)
	}
	secretsPath := SecretsPathFromConfig(configPath)
	if _, err := os.Stat(secretsPath); err == nil {
		_ = os.Chmod(secretsPath, 0660)
		applyUserOwnership(secretsPath)
	}

	return nil
}

// MigrateConfigSplit checks whether the config directory still uses the old
// monolithic format (secrets embedded in config.toml, no secrets.toml) and
// migrates to the split format by re-writing both files. This should be called
// from commands that run as root (setup, up) to handle upgrades transparently.
//
// If secrets.toml already exists, this is a no-op.
func MigrateConfigSplit(configPath string) error {
	secretsPath := SecretsPathFromConfig(configPath)

	// If secrets.toml already exists, migration is done.
	if _, err := os.Stat(secretsPath); err == nil {
		return nil
	}

	// Load the old monolithic config (which has everything in one file).
	cfg := DefaultConfig()
	if _, err := toml.DecodeFile(configPath, cfg); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil // No config at all — nothing to migrate.
		}
		return fmt.Errorf("reading config for migration: %w", err)
	}
	applyDefaults(cfg)

	// Check if there are actually any secrets in the old file. If not,
	// there's nothing to migrate — this might be a fresh install.
	if cfg.Device.PrivateKey.IsZero() && cfg.Network.TURNSecret == "" &&
		cfg.Network.RefreshToken == "" && cfg.Cloudflare.APIToken == "" {
		return nil
	}

	// Re-save using the split format.
	return SaveConfig(configPath, cfg)
}

// applyDefaults fills in default values for optional fields that are
// zero-valued after TOML decoding.
func applyDefaults(cfg *Config) {
	if len(cfg.STUN.Servers) == 0 {
		cfg.STUN.Servers = append([]string(nil), DefaultSTUNServers...)
	}
}
