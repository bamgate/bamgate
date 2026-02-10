package config

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
)

func TestDefaultConfig(t *testing.T) {
	t.Parallel()

	cfg := DefaultConfig()

	if cfg.WebRTC.Ordered {
		t.Error("default WebRTC.Ordered should be false")
	}
	if cfg.WebRTC.MaxRetransmits != 0 {
		t.Errorf("default WebRTC.MaxRetransmits = %d, want 0", cfg.WebRTC.MaxRetransmits)
	}
	if len(cfg.STUN.Servers) != len(DefaultSTUNServers) {
		t.Errorf("default STUN servers count = %d, want %d", len(cfg.STUN.Servers), len(DefaultSTUNServers))
	}
	for i, s := range cfg.STUN.Servers {
		if s != DefaultSTUNServers[i] {
			t.Errorf("STUN server[%d] = %q, want %q", i, s, DefaultSTUNServers[i])
		}
	}
}

func TestSaveAndLoadConfig_roundTrip(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "riftgate", "config.toml")

	priv, err := GeneratePrivateKey()
	if err != nil {
		t.Fatalf("GeneratePrivateKey() error: %v", err)
	}

	original := &Config{
		Network: NetworkConfig{
			Name:       "test-network",
			ServerURL:  "https://riftgate-test.workers.dev",
			AuthToken:  "secret-token-123",
			TURNSecret: "turn-secret-456",
		},
		Device: DeviceConfig{
			Name:       "home-server",
			PrivateKey: priv,
			Address:    "10.0.0.1/24",
		},
		STUN: STUNConfig{
			Servers: []string{
				"stun:stun.cloudflare.com:3478",
				"stun:stun.l.google.com:19302",
			},
		},
		WebRTC: WebRTCConfig{
			Ordered:        false,
			MaxRetransmits: 0,
		},
	}

	// Save.
	if err := SaveConfig(path, original); err != nil {
		t.Fatalf("SaveConfig() error: %v", err)
	}

	// Verify file exists with restricted permissions.
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("config file not created: %v", err)
	}
	perm := info.Mode().Perm()
	if perm != 0600 {
		t.Errorf("config file permissions = %o, want 0600", perm)
	}

	// Load.
	loaded, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig() error: %v", err)
	}

	// Verify all fields.
	if loaded.Network.Name != original.Network.Name {
		t.Errorf("Network.Name = %q, want %q", loaded.Network.Name, original.Network.Name)
	}
	if loaded.Network.ServerURL != original.Network.ServerURL {
		t.Errorf("Network.ServerURL = %q, want %q", loaded.Network.ServerURL, original.Network.ServerURL)
	}
	if loaded.Network.AuthToken != original.Network.AuthToken {
		t.Errorf("Network.AuthToken = %q, want %q", loaded.Network.AuthToken, original.Network.AuthToken)
	}
	if loaded.Network.TURNSecret != original.Network.TURNSecret {
		t.Errorf("Network.TURNSecret = %q, want %q", loaded.Network.TURNSecret, original.Network.TURNSecret)
	}
	if loaded.Device.Name != original.Device.Name {
		t.Errorf("Device.Name = %q, want %q", loaded.Device.Name, original.Device.Name)
	}
	if loaded.Device.PrivateKey != original.Device.PrivateKey {
		t.Errorf("Device.PrivateKey mismatch")
	}
	if loaded.Device.Address != original.Device.Address {
		t.Errorf("Device.Address = %q, want %q", loaded.Device.Address, original.Device.Address)
	}
	if len(loaded.STUN.Servers) != len(original.STUN.Servers) {
		t.Fatalf("STUN servers count = %d, want %d", len(loaded.STUN.Servers), len(original.STUN.Servers))
	}
	for i, s := range loaded.STUN.Servers {
		if s != original.STUN.Servers[i] {
			t.Errorf("STUN server[%d] = %q, want %q", i, s, original.STUN.Servers[i])
		}
	}
	if loaded.WebRTC.Ordered != original.WebRTC.Ordered {
		t.Errorf("WebRTC.Ordered = %v, want %v", loaded.WebRTC.Ordered, original.WebRTC.Ordered)
	}
	if loaded.WebRTC.MaxRetransmits != original.WebRTC.MaxRetransmits {
		t.Errorf("WebRTC.MaxRetransmits = %d, want %d", loaded.WebRTC.MaxRetransmits, original.WebRTC.MaxRetransmits)
	}
}

func TestLoadConfig_fileNotFound(t *testing.T) {
	t.Parallel()

	_, err := LoadConfig("/nonexistent/path/config.toml")
	if err == nil {
		t.Fatal("LoadConfig() expected error for missing file")
	}
	if !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("expected fs.ErrNotExist, got: %v", err)
	}
}

func TestLoadConfig_appliesDefaults(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")

	// Write a minimal config with no STUN servers.
	content := `
[network]
name = "minimal"

[device]
name = "test"
`
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatalf("writing minimal config: %v", err)
	}

	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig() error: %v", err)
	}

	// STUN servers should be filled with defaults.
	if len(cfg.STUN.Servers) != len(DefaultSTUNServers) {
		t.Errorf("STUN servers count = %d, want %d (defaults)", len(cfg.STUN.Servers), len(DefaultSTUNServers))
	}
}

func TestLoadConfig_preservesExplicitSTUN(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")

	content := `
[network]
name = "custom-stun"

[stun]
servers = ["stun:custom.example.com:3478"]
`
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatalf("writing config: %v", err)
	}

	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig() error: %v", err)
	}

	if len(cfg.STUN.Servers) != 1 || cfg.STUN.Servers[0] != "stun:custom.example.com:3478" {
		t.Errorf("STUN servers = %v, want [stun:custom.example.com:3478]", cfg.STUN.Servers)
	}
}

func TestConfig_PublicKey(t *testing.T) {
	t.Parallel()

	priv, err := GeneratePrivateKey()
	if err != nil {
		t.Fatalf("GeneratePrivateKey() error: %v", err)
	}

	cfg := &Config{
		Device: DeviceConfig{
			PrivateKey: priv,
		},
	}

	pub, err := cfg.PublicKey()
	if err != nil {
		t.Fatalf("PublicKey() error: %v", err)
	}

	expected := PublicKey(priv)
	if pub != expected {
		t.Errorf("PublicKey mismatch")
	}
}

func TestConfig_PublicKey_noPrivateKey(t *testing.T) {
	t.Parallel()

	cfg := &Config{}
	_, err := cfg.PublicKey()
	if err == nil {
		t.Fatal("PublicKey() expected error when private key is not set")
	}
}

func TestDefaultConfigPath(t *testing.T) {
	// Cannot use t.Parallel() with t.Setenv.
	t.Setenv("XDG_CONFIG_HOME", "/tmp/test-xdg")
	path, err := DefaultConfigPath()
	if err != nil {
		t.Fatalf("DefaultConfigPath() error: %v", err)
	}
	want := "/tmp/test-xdg/riftgate/config.toml"
	if path != want {
		t.Errorf("DefaultConfigPath() = %q, want %q", path, want)
	}
}

func TestDefaultConfigPath_fallback(t *testing.T) {
	// Cannot use t.Parallel() with t.Setenv.
	t.Setenv("XDG_CONFIG_HOME", "")
	path, err := DefaultConfigPath()
	if err != nil {
		t.Fatalf("DefaultConfigPath() error: %v", err)
	}

	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatalf("UserHomeDir() error: %v", err)
	}
	want := filepath.Join(home, ".config", "riftgate", "config.toml")
	if path != want {
		t.Errorf("DefaultConfigPath() = %q, want %q", path, want)
	}
}

func TestSaveConfig_createsParentDirs(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "deep", "nested", "config.toml")

	cfg := DefaultConfig()
	if err := SaveConfig(path, cfg); err != nil {
		t.Fatalf("SaveConfig() error: %v", err)
	}

	if _, err := os.Stat(path); err != nil {
		t.Fatalf("config file not created at nested path: %v", err)
	}
}

func TestKeyInTOML_roundTrip(t *testing.T) {
	t.Parallel()

	// Verify that a Key field survives a full TOML encode→decode cycle,
	// which exercises MarshalText and UnmarshalText.
	priv, err := GeneratePrivateKey()
	if err != nil {
		t.Fatalf("GeneratePrivateKey() error: %v", err)
	}

	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")

	cfg := DefaultConfig()
	cfg.Device.PrivateKey = priv

	if err := SaveConfig(path, cfg); err != nil {
		t.Fatalf("SaveConfig() error: %v", err)
	}

	loaded, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig() error: %v", err)
	}

	if loaded.Device.PrivateKey != priv {
		t.Errorf("Key TOML round-trip failed:\n got  %s\n want %s",
			loaded.Device.PrivateKey, priv)
	}
}
