package main

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/kuuji/bamgate/internal/config"
	"github.com/kuuji/bamgate/internal/deploy"
)

var setupForce bool

var setupCmd = &cobra.Command{
	Use:   "setup",
	Short: "Set up bamgate: deploy signaling server and configure this device",
	Long: `Interactive setup wizard that handles everything needed to get bamgate running:

  1. Deploy the signaling worker to Cloudflare (or detect existing deployment)
  2. Configure this device (name, WireGuard keys, tunnel address)
  3. Install a system service (systemd on Linux, launchd on macOS)

If you have an invite code from another device, setup will use it to
retrieve the server configuration automatically — no Cloudflare account needed.

If bamgate is already configured, setup will update the system service
if installed. Use --force to redo full setup.

This command must be run as root:
  sudo bamgate setup`,
	RunE: runSetup,
}

func init() {
	setupCmd.Flags().BoolVar(&setupForce, "force", false, "redo full setup even if already configured")
}

func runSetup(cmd *cobra.Command, args []string) error {
	if runtime.GOOS != "linux" && runtime.GOOS != "darwin" {
		return fmt.Errorf("setup is only supported on Linux and macOS")
	}

	if os.Getuid() != 0 {
		return fmt.Errorf("setup must be run as root (try: sudo bamgate setup)")
	}

	cfgPath := resolvedConfigPath()

	// Check for legacy config and migrate if needed.
	if err := migrateConfig(cfgPath); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: config migration check failed: %v\n", err)
	}

	// Check for existing config.
	existingCfg, _ := config.LoadConfig(cfgPath)
	if existingCfg != nil && !setupForce {
		return runSetupExisting(cfgPath)
	}

	if existingCfg != nil && setupForce {
		fmt.Fprintf(os.Stderr, "Existing config found: %s\n", cfgPath)
		fmt.Fprintf(os.Stderr, "Overwriting (--force).\n\n")
	}

	return runSetupFull(cfgPath)
}

// migrateConfig checks for a config file at the old user-level path
// (~/.config/bamgate/config.toml) and copies it to the new system path
// (/etc/bamgate/config.toml) if the new path doesn't already exist.
func migrateConfig(newPath string) error {
	// If the new path already has a config, nothing to do.
	if _, err := os.Stat(newPath); err == nil {
		return nil
	}

	// Check the legacy path for the user who invoked sudo.
	legacyPath, err := legacyConfigPath()
	if err != nil {
		return err
	}
	if _, err := os.Stat(legacyPath); err != nil {
		return nil // No legacy config either.
	}

	fmt.Fprintf(os.Stderr, "Found existing config at legacy path: %s\n", legacyPath)
	fmt.Fprintf(os.Stderr, "Migrating to %s\n\n", newPath)

	// Read and copy the config.
	data, err := os.ReadFile(legacyPath)
	if err != nil {
		return fmt.Errorf("reading legacy config: %w", err)
	}

	dir := filepath.Dir(newPath)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return fmt.Errorf("creating config directory: %w", err)
	}

	if err := os.WriteFile(newPath, data, 0600); err != nil {
		return fmt.Errorf("writing config: %w", err)
	}

	fmt.Fprintf(os.Stderr, "Config migrated. You can remove the old file:\n")
	fmt.Fprintf(os.Stderr, "  rm %s\n\n", legacyPath)

	return nil
}

// legacyConfigPath returns the old user-level config path, resolving the
// actual user when running via sudo.
func legacyConfigPath() (string, error) {
	// If running via sudo, check the real user's home.
	if sudoUser := os.Getenv("SUDO_USER"); sudoUser != "" {
		home := os.Getenv("SUDO_HOME")
		if home == "" {
			// Common fallback: /home/<user>
			home = filepath.Join("/home", sudoUser)
		}
		return config.LegacyConfigPathForUser(home), nil
	}
	return config.LegacyConfigPath()
}

// runSetupExisting handles the case where bamgate is already configured.
// It updates the system service if installed.
func runSetupExisting(cfgPath string) error {
	fmt.Fprintf(os.Stderr, "bamgate is already configured: %s\n\n", cfgPath)

	if runtime.GOOS == "linux" {
		// Update systemd service if it exists.
		if _, err := os.Stat(systemdServicePath); err == nil {
			fmt.Fprintf(os.Stderr, "Updating systemd service...\n")
			if err := installSystemdService(); err != nil {
				return fmt.Errorf("updating systemd service: %w", err)
			}
		}
	} else if runtime.GOOS == "darwin" {
		// Update launchd service if it exists.
		if _, err := os.Stat(launchdPlistPath); err == nil {
			fmt.Fprintf(os.Stderr, "Updating launchd service...\n")
			if err := installLaunchdService(); err != nil {
				return fmt.Errorf("updating launchd service: %w", err)
			}
		}
	}

	fmt.Fprintf(os.Stderr, "\nSetup complete. Run 'sudo bamgate up' to connect.\n")
	fmt.Fprintf(os.Stderr, "Use --force to redo full setup.\n")

	return nil
}

// runSetupFull runs the full interactive setup wizard.
func runSetupFull(cfgPath string) error {
	scanner := bufio.NewScanner(os.Stdin)
	ctx := context.Background()

	fmt.Fprintf(os.Stderr, "\nbamgate setup\n")
	fmt.Fprintf(os.Stderr, "%s\n\n", strings.Repeat("=", 14))

	// Ask whether the user has an invite code.
	hasInvite := promptYesNo(scanner, "Do you have an invite code from another device?", false)

	var cfg *config.Config
	var err error
	if hasInvite {
		cfg, err = setupWithInvite(ctx, scanner)
	} else {
		cfg, err = setupWithCloudflare(ctx, scanner)
	}
	if err != nil {
		return err
	}

	// --- Device configuration ---
	fmt.Fprintf(os.Stderr, "\nDevice Configuration\n")
	fmt.Fprintf(os.Stderr, "%s\n", strings.Repeat("-", 20))

	hostname, _ := os.Hostname()
	cfg.Device.Name = promptString(scanner, "Device name", hostname)

	// Generate WireGuard key pair.
	privKey, err := config.GeneratePrivateKey()
	if err != nil {
		return fmt.Errorf("generating WireGuard key: %w", err)
	}
	cfg.Device.PrivateKey = privKey
	pubKey := config.PublicKey(privKey)

	// Network name.
	if cfg.Network.Name == "" {
		cfg.Network.Name = "default"
	}

	fmt.Fprintf(os.Stderr, "  WireGuard key pair generated\n")

	// --- Save config ---
	if err := config.SaveConfig(cfgPath, cfg); err != nil {
		return fmt.Errorf("saving config: %w", err)
	}
	fmt.Fprintf(os.Stderr, "  Config written to %s\n", cfgPath)

	// --- Install system service ---
	fmt.Fprintf(os.Stderr, "\nService Installation\n")
	fmt.Fprintf(os.Stderr, "%s\n", strings.Repeat("-", 20))

	if runtime.GOOS == "linux" {
		if promptYesNo(scanner, "Install systemd service?", true) {
			if err := installSystemdService(); err != nil {
				return fmt.Errorf("installing systemd service: %w", err)
			}
		}
	} else if runtime.GOOS == "darwin" {
		if promptYesNo(scanner, "Install launchd service?", true) {
			if err := installLaunchdService(); err != nil {
				return fmt.Errorf("installing launchd service: %w", err)
			}
		}
	}

	// --- Done ---
	fmt.Fprintf(os.Stderr, "\nSetup complete! Run 'sudo bamgate up' to connect.\n")
	fmt.Fprintf(os.Stderr, "  Public key: %s\n", pubKey.String())
	fmt.Fprintf(os.Stderr, "\nTo add another device to this network, run on a connected device:\n")
	fmt.Fprintf(os.Stderr, "  sudo bamgate invite\n")

	return nil
}

// setupWithInvite handles setup by redeeming an invite code from the worker.
func setupWithInvite(ctx context.Context, scanner *bufio.Scanner) (*config.Config, error) {
	fmt.Fprintf(os.Stderr, "\nInvite Setup\n")
	fmt.Fprintf(os.Stderr, "%s\n", strings.Repeat("-", 12))

	workerName := promptString(scanner, "Worker name", "bamgate")
	subdomain := promptString(scanner, "Cloudflare subdomain", "")
	if subdomain == "" {
		return nil, fmt.Errorf("subdomain is required")
	}

	code := promptString(scanner, "Invite code", "")
	if code == "" {
		return nil, fmt.Errorf("invite code is required")
	}

	serverURL := fmt.Sprintf("https://%s.%s.workers.dev", workerName, subdomain)

	// Redeem the invite.
	redeemURL := fmt.Sprintf("%s/invite/%s", serverURL, code)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, redeemURL, nil)
	if err != nil {
		return nil, fmt.Errorf("creating invite request: %w", err)
	}

	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("redeeming invite: %w", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)

	if resp.StatusCode != http.StatusOK {
		var errResp struct {
			Error string `json:"error"`
		}
		if json.Unmarshal(body, &errResp) == nil && errResp.Error != "" {
			return nil, fmt.Errorf("invite failed: %s", errResp.Error)
		}
		return nil, fmt.Errorf("invite failed: HTTP %d", resp.StatusCode)
	}

	var invite struct {
		ServerURL  string `json:"server_url"`
		AuthToken  string `json:"auth_token"`
		TURNSecret string `json:"turn_secret"`
		Address    string `json:"address"`
		Subnet     string `json:"subnet"`
	}
	if err := json.Unmarshal(body, &invite); err != nil {
		return nil, fmt.Errorf("parsing invite response: %w", err)
	}

	fmt.Fprintf(os.Stderr, "  Connected to signaling server\n")
	fmt.Fprintf(os.Stderr, "  Configuration retrieved\n")
	fmt.Fprintf(os.Stderr, "  Tunnel address: %s (auto-assigned)\n", invite.Address)

	// Build config from invite data.
	wsURL, err := normalizeServerURL(invite.ServerURL + "/connect")
	if err != nil {
		return nil, fmt.Errorf("normalizing server URL: %w", err)
	}

	cfg := config.DefaultConfig()
	cfg.Network.ServerURL = wsURL
	cfg.Network.AuthToken = invite.AuthToken
	cfg.Network.TURNSecret = invite.TURNSecret
	cfg.Device.Address = invite.Address

	return cfg, nil
}

// setupWithCloudflare handles setup via the Cloudflare API (first device or
// additional device with CF API token).
func setupWithCloudflare(ctx context.Context, scanner *bufio.Scanner) (*config.Config, error) {
	fmt.Fprintf(os.Stderr, "Cloudflare Account\n")
	fmt.Fprintf(os.Stderr, "%s\n", strings.Repeat("-", 18))
	fmt.Fprintf(os.Stderr, "Create an API token at:\n")
	fmt.Fprintf(os.Stderr, "  https://dash.cloudflare.com/profile/api-tokens\n\n")
	fmt.Fprintf(os.Stderr, "Required permissions:\n")
	fmt.Fprintf(os.Stderr, "  - Account / Workers Scripts / Edit\n")
	fmt.Fprintf(os.Stderr, "  - Account / Account Settings / Read\n\n")

	apiToken := promptString(scanner, "Paste your API token", "")
	if apiToken == "" {
		return nil, fmt.Errorf("API token is required")
	}

	// Verify token and get account.
	cfClient := deploy.NewClient(apiToken)

	if err := cfClient.VerifyToken(ctx); err != nil {
		return nil, fmt.Errorf("invalid API token: %w", err)
	}

	accounts, err := cfClient.ListAccounts(ctx)
	if err != nil {
		return nil, err
	}
	if len(accounts) == 0 {
		return nil, fmt.Errorf("no Cloudflare accounts found for this token")
	}

	account := accounts[0]
	if len(accounts) > 1 {
		fmt.Fprintf(os.Stderr, "\nMultiple accounts found:\n")
		for i, a := range accounts {
			fmt.Fprintf(os.Stderr, "  %d. %s (%s)\n", i+1, a.Name, a.ID)
		}
		fmt.Fprintf(os.Stderr, "Using: %s\n", account.Name)
	}

	fmt.Fprintf(os.Stderr, "  Authenticated (account: %s)\n", account.Name)

	// Get workers.dev subdomain.
	subdomain, err := cfClient.GetSubdomain(ctx, account.ID)
	if err != nil {
		return nil, fmt.Errorf("getting workers subdomain: %w\nMake sure you have a workers.dev subdomain configured at https://dash.cloudflare.com", err)
	}

	// Check if worker already exists.
	workerName := "bamgate"
	exists, err := cfClient.WorkerExists(ctx, account.ID, workerName)
	if err != nil {
		return nil, err
	}

	var authToken string
	var turnSecret string

	if exists {
		fmt.Fprintf(os.Stderr, "  Worker %q already deployed at %s\n", workerName, deploy.WorkerURL(workerName, subdomain))

		// Try to read back the auth token and TURN secret from bindings.
		bindings, err := cfClient.GetWorkerBindings(ctx, account.ID, workerName)
		if err != nil {
			return nil, fmt.Errorf("reading worker bindings: %w", err)
		}

		token, ok := deploy.GetAuthTokenFromBindings(bindings)
		if ok && token != "" {
			authToken = token
			fmt.Fprintf(os.Stderr, "  Auth token retrieved\n")
		} else {
			fmt.Fprintf(os.Stderr, "  Warning: could not retrieve auth token from worker bindings.\n")
			authToken = promptString(scanner, "Enter the auth token manually", "")
			if authToken == "" {
				return nil, fmt.Errorf("auth token is required")
			}
		}

		ts, ok := deploy.GetTURNSecretFromBindings(bindings)
		if ok && ts != "" {
			turnSecret = ts
			fmt.Fprintf(os.Stderr, "  TURN secret retrieved\n")
		} else {
			fmt.Fprintf(os.Stderr, "  Warning: TURN secret not found in worker bindings (TURN relay disabled).\n")
		}

		// Get network info for auto-address assignment.
		address, err := fetchNextAddress(ctx, deploy.WorkerURL(workerName, subdomain), authToken)
		if err != nil {
			// Fallback to manual.
			fmt.Fprintf(os.Stderr, "  Could not auto-assign address: %v\n", err)
			address = promptString(scanner, "Tunnel address (CIDR, e.g. 10.0.0.2/24)", "")
			if address == "" {
				return nil, fmt.Errorf("tunnel address is required")
			}
		} else {
			fmt.Fprintf(os.Stderr, "  Tunnel address: %s (auto-assigned)\n", address)
		}

		cfg := config.DefaultConfig()
		cfg.Cloudflare.APIToken = apiToken
		cfg.Cloudflare.AccountID = account.ID
		cfg.Cloudflare.WorkerName = workerName
		cfg.Network.ServerURL = deploy.WorkerWSURL(workerName, subdomain)
		cfg.Network.AuthToken = authToken
		cfg.Network.TURNSecret = turnSecret
		cfg.Device.Address = address
		return cfg, nil
	}

	// --- First-time deploy ---
	fmt.Fprintf(os.Stderr, "\nSignaling Server\n")
	fmt.Fprintf(os.Stderr, "%s\n", strings.Repeat("-", 16))
	fmt.Fprintf(os.Stderr, "No existing bamgate worker found. Deploying...\n")

	workerName = promptString(scanner, "Worker name", "bamgate")

	// Generate auth token and TURN secret.
	authToken, err = generateAuthToken()
	if err != nil {
		return nil, fmt.Errorf("generating auth token: %w", err)
	}
	turnSecret, err = generateAuthToken() // Same format works for TURN secret.
	if err != nil {
		return nil, fmt.Errorf("generating TURN secret: %w", err)
	}

	// Load embedded worker modules.
	modules, err := deploy.WorkerModules()
	if err != nil {
		return nil, fmt.Errorf("loading worker modules: %w", err)
	}

	// Deploy.
	if err := cfClient.DeployWorker(ctx, deploy.DeployWorkerInput{
		AccountID:        account.ID,
		ScriptName:       workerName,
		Modules:          modules,
		MainModule:       "worker.mjs",
		AuthToken:        authToken,
		TURNSecret:       turnSecret,
		IncludeMigration: true,
	}); err != nil {
		return nil, fmt.Errorf("deploying worker: %w", err)
	}

	// Ensure workers.dev subdomain is enabled for this worker.
	if err := cfClient.EnableWorkerSubdomain(ctx, account.ID, workerName); err != nil {
		// Non-fatal — it might already be enabled.
		fmt.Fprintf(os.Stderr, "  Warning: could not enable workers.dev route: %v\n", err)
	}

	workerURL := deploy.WorkerURL(workerName, subdomain)
	fmt.Fprintf(os.Stderr, "  Worker deployed: %s\n", workerURL)
	fmt.Fprintf(os.Stderr, "  Auth token and TURN secret generated and configured\n")

	// First device gets the first address in the subnet.
	address := "10.0.0.1/24"
	fmt.Fprintf(os.Stderr, "  Tunnel address: %s (first device)\n", address)

	cfg := config.DefaultConfig()
	cfg.Cloudflare.APIToken = apiToken
	cfg.Cloudflare.AccountID = account.ID
	cfg.Cloudflare.WorkerName = workerName
	cfg.Network.ServerURL = deploy.WorkerWSURL(workerName, subdomain)
	cfg.Network.AuthToken = authToken
	cfg.Network.TURNSecret = turnSecret
	cfg.Device.Address = address
	return cfg, nil
}

// fetchNextAddress queries the worker's /network-info endpoint for the next
// available tunnel address.
func fetchNextAddress(ctx context.Context, workerURL, authToken string) (string, error) {
	infoURL := workerURL + "/network-info"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, infoURL, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+authToken)

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(body))
	}

	var info struct {
		NextAddress string `json:"next_address"`
	}
	if err := json.Unmarshal(body, &info); err != nil {
		return "", fmt.Errorf("parsing response: %w", err)
	}

	if info.NextAddress == "" {
		return "", fmt.Errorf("no addresses available")
	}

	return info.NextAddress, nil
}

// generateAuthToken creates a random auth token with a recognizable prefix.
func generateAuthToken() (string, error) {
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return "bg_" + hex.EncodeToString(b), nil
}

// installSystemdService writes the systemd service file for bamgate.
// The service runs as root with full network capabilities.
func installSystemdService() error {
	serviceContent := `[Unit]
Description=bamgate - WireGuard VPN tunnel over WebRTC
Documentation=https://github.com/bamgate/bamgate
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
ExecStart=/usr/local/bin/bamgate up
Restart=on-failure
RestartSec=5

# Runtime directory for the control socket.
RuntimeDirectory=bamgate
RuntimeDirectoryMode=0755

# State directory for persistent data.
StateDirectory=bamgate
StateDirectoryMode=0700

# Security hardening.
ProtectSystem=strict
ReadWritePaths=/run/bamgate /etc/bamgate
PrivateTmp=yes
NoNewPrivileges=yes

# Network access (required).
RestrictAddressFamilies=AF_UNIX AF_INET AF_INET6 AF_NETLINK

# System call filtering.
SystemCallArchitectures=native
LockPersonality=yes
ProtectClock=yes
ProtectHostname=yes
ProtectKernelLogs=yes
ProtectKernelModules=yes
ProtectKernelTunables=yes
RestrictRealtime=yes
RestrictSUIDSGID=yes

[Install]
WantedBy=multi-user.target
`

	fmt.Fprintf(os.Stderr, "Installing systemd service to %s\n", systemdServicePath)

	if err := os.WriteFile(systemdServicePath, []byte(serviceContent), 0644); err != nil {
		return fmt.Errorf("writing service file: %w", err)
	}

	// Reload systemd.
	reload := exec.Command("systemctl", "daemon-reload")
	reload.Stdout = os.Stderr
	reload.Stderr = os.Stderr
	if err := reload.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: systemctl daemon-reload failed: %v\n", err)
	}

	fmt.Fprintf(os.Stderr, "  systemd service installed\n")

	return nil
}

// launchdPlistPath is the path for the launchd plist on macOS.
const launchdPlistPath = "/Library/LaunchDaemons/com.bamgate.bamgate.plist"

// installLaunchdService writes the launchd plist for bamgate on macOS.
// The service runs as root at boot.
func installLaunchdService() error {
	plistContent := `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key>
  <string>com.bamgate.bamgate</string>
  <key>ProgramArguments</key>
  <array>
    <string>/usr/local/bin/bamgate</string>
    <string>up</string>
  </array>
  <key>RunAtLoad</key>
  <true/>
  <key>KeepAlive</key>
  <dict>
    <key>SuccessfulExit</key>
    <false/>
  </dict>
  <key>StandardOutPath</key>
  <string>/var/log/bamgate.log</string>
  <key>StandardErrorPath</key>
  <string>/var/log/bamgate.log</string>
</dict>
</plist>
`

	fmt.Fprintf(os.Stderr, "Installing launchd service to %s\n", launchdPlistPath)

	if err := os.WriteFile(launchdPlistPath, []byte(plistContent), 0644); err != nil {
		return fmt.Errorf("writing plist file: %w", err)
	}

	fmt.Fprintf(os.Stderr, "  launchd service installed\n")

	return nil
}
