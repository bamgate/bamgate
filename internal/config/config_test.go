package config

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BurntSushi/toml"
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
	path := filepath.Join(dir, "bamgate", "config.toml")
	secretsPath := filepath.Join(dir, "bamgate", "secrets.toml")

	priv, err := GeneratePrivateKey()
	if err != nil {
		t.Fatalf("GeneratePrivateKey() error: %v", err)
	}

	original := &Config{
		Cloudflare: CloudflareConfig{
			APIToken:   "cf-secret-token",
			AccountID:  "acc-123",
			WorkerName: "bamgate",
		},
		Network: NetworkConfig{
			Name:         "test-network",
			ServerURL:    "https://bamgate-test.workers.dev",
			TURNSecret:   "turn-secret-456",
			DeviceID:     "device-abc-123",
			RefreshToken: "refresh-token-789",
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

	// Verify config.toml exists with world-readable permissions.
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("config file not created: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0644 {
		t.Errorf("config.toml permissions = %o, want 0644", perm)
	}

	// Verify secrets.toml exists with restricted permissions.
	sInfo, err := os.Stat(secretsPath)
	if err != nil {
		t.Fatalf("secrets file not created: %v", err)
	}
	if perm := sInfo.Mode().Perm(); perm != 0640 {
		t.Errorf("secrets.toml permissions = %o, want 0640", perm)
	}

	// Verify config.toml does NOT contain secrets.
	cfgData, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading config.toml: %v", err)
	}
	cfgStr := string(cfgData)
	for _, secret := range []string{"turn-secret-456", "refresh-token-789", "cf-secret-token"} {
		if strings.Contains(cfgStr, secret) {
			t.Errorf("config.toml contains secret %q — should be in secrets.toml only", secret)
		}
	}

	// Verify secrets.toml DOES contain secrets.
	secData, err := os.ReadFile(secretsPath)
	if err != nil {
		t.Fatalf("reading secrets.toml: %v", err)
	}
	secStr := string(secData)
	for _, secret := range []string{"turn-secret-456", "refresh-token-789", "cf-secret-token"} {
		if !strings.Contains(secStr, secret) {
			t.Errorf("secrets.toml does not contain expected secret %q", secret)
		}
	}

	// Load and verify full round-trip.
	loaded, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig() error: %v", err)
	}

	// Verify all fields.
	if loaded.Cloudflare.APIToken != original.Cloudflare.APIToken {
		t.Errorf("Cloudflare.APIToken = %q, want %q", loaded.Cloudflare.APIToken, original.Cloudflare.APIToken)
	}
	if loaded.Cloudflare.AccountID != original.Cloudflare.AccountID {
		t.Errorf("Cloudflare.AccountID = %q, want %q", loaded.Cloudflare.AccountID, original.Cloudflare.AccountID)
	}
	if loaded.Cloudflare.WorkerName != original.Cloudflare.WorkerName {
		t.Errorf("Cloudflare.WorkerName = %q, want %q", loaded.Cloudflare.WorkerName, original.Cloudflare.WorkerName)
	}
	if loaded.Network.Name != original.Network.Name {
		t.Errorf("Network.Name = %q, want %q", loaded.Network.Name, original.Network.Name)
	}
	if loaded.Network.ServerURL != original.Network.ServerURL {
		t.Errorf("Network.ServerURL = %q, want %q", loaded.Network.ServerURL, original.Network.ServerURL)
	}
	if loaded.Network.DeviceID != original.Network.DeviceID {
		t.Errorf("Network.DeviceID = %q, want %q", loaded.Network.DeviceID, original.Network.DeviceID)
	}
	if loaded.Network.RefreshToken != original.Network.RefreshToken {
		t.Errorf("Network.RefreshToken = %q, want %q", loaded.Network.RefreshToken, original.Network.RefreshToken)
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
	t.Parallel()
	path, err := DefaultConfigPath()
	if err != nil {
		t.Fatalf("DefaultConfigPath() error: %v", err)
	}
	want := "/etc/bamgate/config.toml"
	if path != want {
		t.Errorf("DefaultConfigPath() = %q, want %q", path, want)
	}
}

func TestLegacyConfigPath(t *testing.T) {
	// Cannot use t.Parallel() with t.Setenv.
	t.Setenv("XDG_CONFIG_HOME", "/tmp/test-xdg")
	path, err := LegacyConfigPath()
	if err != nil {
		t.Fatalf("LegacyConfigPath() error: %v", err)
	}
	want := "/tmp/test-xdg/bamgate/config.toml"
	if path != want {
		t.Errorf("LegacyConfigPath() = %q, want %q", path, want)
	}
}

func TestLegacyConfigPath_fallback(t *testing.T) {
	// Cannot use t.Parallel() with t.Setenv.
	t.Setenv("XDG_CONFIG_HOME", "")
	path, err := LegacyConfigPath()
	if err != nil {
		t.Fatalf("LegacyConfigPath() error: %v", err)
	}

	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatalf("UserHomeDir() error: %v", err)
	}
	want := filepath.Join(home, ".config", "bamgate", "config.toml")
	if path != want {
		t.Errorf("LegacyConfigPath() = %q, want %q", path, want)
	}
}

func TestLegacyConfigPathForUser(t *testing.T) {
	t.Parallel()
	path := LegacyConfigPathForUser("/home/testuser")
	want := "/home/testuser/.config/bamgate/config.toml"
	if path != want {
		t.Errorf("LegacyConfigPathForUser() = %q, want %q", path, want)
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

func TestLoadPublicConfig_noSecrets(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")

	priv, err := GeneratePrivateKey()
	if err != nil {
		t.Fatalf("GeneratePrivateKey() error: %v", err)
	}

	original := &Config{
		Network: NetworkConfig{
			Name:         "test-network",
			ServerURL:    "https://bamgate.workers.dev",
			TURNSecret:   "secret-turn",
			DeviceID:     "dev-1",
			RefreshToken: "refresh-tok",
		},
		Device: DeviceConfig{
			Name:       "laptop",
			PrivateKey: priv,
			Address:    "10.0.0.2/24",
		},
	}

	if err := SaveConfig(path, original); err != nil {
		t.Fatalf("SaveConfig() error: %v", err)
	}

	// LoadPublicConfig should get non-secret fields but NOT secrets.
	cfg, err := LoadPublicConfig(path)
	if err != nil {
		t.Fatalf("LoadPublicConfig() error: %v", err)
	}

	if cfg.Network.ServerURL != original.Network.ServerURL {
		t.Errorf("ServerURL = %q, want %q", cfg.Network.ServerURL, original.Network.ServerURL)
	}
	if cfg.Network.DeviceID != original.Network.DeviceID {
		t.Errorf("DeviceID = %q, want %q", cfg.Network.DeviceID, original.Network.DeviceID)
	}
	if cfg.Device.Name != original.Device.Name {
		t.Errorf("Device.Name = %q, want %q", cfg.Device.Name, original.Device.Name)
	}

	// Secret fields should be zero-valued since they're only in secrets.toml.
	if cfg.Network.TURNSecret != "" {
		t.Errorf("LoadPublicConfig() TURNSecret = %q, want empty", cfg.Network.TURNSecret)
	}
	if cfg.Network.RefreshToken != "" {
		t.Errorf("LoadPublicConfig() RefreshToken = %q, want empty", cfg.Network.RefreshToken)
	}
	if !cfg.Device.PrivateKey.IsZero() {
		t.Errorf("LoadPublicConfig() PrivateKey should be zero")
	}
}

func TestSaveSecrets_onlyWritesSecrets(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	secretsPath := filepath.Join(dir, "secrets.toml")

	cfg := DefaultConfig()
	cfg.Network.TURNSecret = "original-secret"
	cfg.Network.RefreshToken = "original-refresh"

	// Initial save writes both files.
	if err := SaveConfig(path, cfg); err != nil {
		t.Fatalf("SaveConfig() error: %v", err)
	}

	// Modify only the refresh token and save secrets.
	cfg.Network.RefreshToken = "rotated-refresh"
	if err := SaveSecrets(path, cfg); err != nil {
		t.Fatalf("SaveSecrets() error: %v", err)
	}

	// Verify secrets.toml has the rotated token.
	secData, err := os.ReadFile(secretsPath)
	if err != nil {
		t.Fatalf("reading secrets.toml: %v", err)
	}
	if !strings.Contains(string(secData), "rotated-refresh") {
		t.Error("secrets.toml should contain rotated refresh token")
	}

	// Full load should return the rotated token.
	loaded, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig() error: %v", err)
	}
	if loaded.Network.RefreshToken != "rotated-refresh" {
		t.Errorf("RefreshToken = %q, want %q", loaded.Network.RefreshToken, "rotated-refresh")
	}
}

func TestMigrateConfigSplit_monolithicToSplit(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	secretsPath := filepath.Join(dir, "secrets.toml")

	priv, err := GeneratePrivateKey()
	if err != nil {
		t.Fatalf("GeneratePrivateKey() error: %v", err)
	}

	// Write an old-style monolithic config with secrets embedded.
	monolithic := &Config{
		Network: NetworkConfig{
			Name:         "my-net",
			ServerURL:    "https://bamgate.workers.dev",
			TURNSecret:   "turn-s3cret",
			DeviceID:     "dev-42",
			RefreshToken: "refresh-xyz",
		},
		Device: DeviceConfig{
			Name:       "home",
			PrivateKey: priv,
			Address:    "10.0.0.1/24",
		},
		Cloudflare: CloudflareConfig{
			APIToken:   "cf-tok",
			AccountID:  "acc-1",
			WorkerName: "bamgate",
		},
	}

	// Write as a monolithic file (old format) using raw TOML encoding.
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0600)
	if err != nil {
		t.Fatalf("creating monolithic config: %v", err)
	}
	if err := toml.NewEncoder(f).Encode(monolithic); err != nil {
		f.Close()
		t.Fatalf("encoding monolithic config: %v", err)
	}
	f.Close()

	// Verify secrets.toml does not exist yet.
	if _, err := os.Stat(secretsPath); err == nil {
		t.Fatal("secrets.toml should not exist before migration")
	}

	// Run migration.
	if err := MigrateConfigSplit(path); err != nil {
		t.Fatalf("MigrateConfigSplit() error: %v", err)
	}

	// Verify secrets.toml was created.
	if _, err := os.Stat(secretsPath); err != nil {
		t.Fatalf("secrets.toml not created by migration: %v", err)
	}

	// Verify config.toml no longer contains secrets.
	cfgData, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading config.toml: %v", err)
	}
	cfgStr := string(cfgData)
	if strings.Contains(cfgStr, "turn-s3cret") {
		t.Error("config.toml still contains turn_secret after migration")
	}
	if strings.Contains(cfgStr, "cf-tok") {
		t.Error("config.toml still contains api_token after migration")
	}

	// Verify config.toml permissions are now 0644.
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat config.toml: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0644 {
		t.Errorf("config.toml permissions after migration = %o, want 0644", perm)
	}

	// Verify full round-trip load works.
	loaded, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig() after migration: %v", err)
	}
	if loaded.Network.TURNSecret != "turn-s3cret" {
		t.Errorf("TURNSecret = %q, want %q", loaded.Network.TURNSecret, "turn-s3cret")
	}
	if loaded.Cloudflare.APIToken != "cf-tok" {
		t.Errorf("APIToken = %q, want %q", loaded.Cloudflare.APIToken, "cf-tok")
	}
	if loaded.Device.PrivateKey != priv {
		t.Error("PrivateKey mismatch after migration")
	}
	if loaded.Network.ServerURL != "https://bamgate.workers.dev" {
		t.Errorf("ServerURL = %q, want %q", loaded.Network.ServerURL, "https://bamgate.workers.dev")
	}
}

func TestMigrateConfigSplit_alreadyMigrated(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")

	cfg := DefaultConfig()
	cfg.Network.TURNSecret = "secret"

	// Save using the new split format (creates both files).
	if err := SaveConfig(path, cfg); err != nil {
		t.Fatalf("SaveConfig() error: %v", err)
	}

	// Migration should be a no-op.
	if err := MigrateConfigSplit(path); err != nil {
		t.Fatalf("MigrateConfigSplit() error: %v", err)
	}
}

func TestMigrateConfigSplit_noConfig(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "nonexistent", "config.toml")

	// Should not error when no config exists.
	if err := MigrateConfigSplit(path); err != nil {
		t.Fatalf("MigrateConfigSplit() error: %v", err)
	}
}

func TestLoadConfig_backwardCompatible_monolithic(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")

	// Write a monolithic config (old format) that has secrets embedded.
	content := `
[cloudflare]
api_token = "cf-secret"
account_id = "acc-1"

[network]
name = "test"
server_url = "https://bamgate.workers.dev"
turn_secret = "turn-secret"
device_id = "dev-1"
refresh_token = "refresh-tok"

[device]
name = "laptop"
address = "10.0.0.1/24"
`
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatalf("writing monolithic config: %v", err)
	}

	// LoadConfig should work even without secrets.toml (backward compatible).
	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig() error: %v", err)
	}

	// Non-secret fields should be loaded.
	if cfg.Network.ServerURL != "https://bamgate.workers.dev" {
		t.Errorf("ServerURL = %q, want %q", cfg.Network.ServerURL, "https://bamgate.workers.dev")
	}

	// Secret fields from the monolithic file should also be loaded
	// (backward compatibility — config.toml still has full Config TOML tags).
	if cfg.Network.TURNSecret != "turn-secret" {
		t.Errorf("TURNSecret = %q, want %q", cfg.Network.TURNSecret, "turn-secret")
	}
	if cfg.Cloudflare.APIToken != "cf-secret" {
		t.Errorf("APIToken = %q, want %q", cfg.Cloudflare.APIToken, "cf-secret")
	}
}

func TestSecretsPathFromConfig(t *testing.T) {
	t.Parallel()

	tests := []struct {
		input string
		want  string
	}{
		{"/etc/bamgate/config.toml", "/etc/bamgate/secrets.toml"},
		{"/tmp/test/config.toml", "/tmp/test/secrets.toml"},
		{"config.toml", "secrets.toml"},
	}

	for _, tt := range tests {
		got := SecretsPathFromConfig(tt.input)
		if got != tt.want {
			t.Errorf("SecretsPathFromConfig(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}
