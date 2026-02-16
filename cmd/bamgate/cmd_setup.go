package main

import (
	"bufio"
	"context"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/kuuji/bamgate/internal/auth"
	"github.com/kuuji/bamgate/internal/config"
	"github.com/kuuji/bamgate/internal/deploy"
)

var setupForce bool

var setupCmd = &cobra.Command{
	Use:   "setup",
	Short: "Set up bamgate: authenticate, deploy worker, and configure this device",
	Long: `Interactive setup wizard that handles everything needed to get bamgate running:

  1. Authenticate with GitHub (Device Authorization Grant)
  2. Deploy the signaling worker to Cloudflare (new network) or join an existing one
  3. Configure this device (name, WireGuard keys, tunnel address)
  4. Install a system service (systemd on Linux, launchd on macOS)

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

	// Migrate from monolithic config.toml to split config.toml + secrets.toml.
	if err := config.MigrateConfigSplit(cfgPath); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: config split migration failed: %v\n", err)
	}

	// Fix directory and file permissions for the split config model.
	if err := config.FixPermissions(cfgPath); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: fixing config permissions failed: %v\n", err)
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
	if _, err := os.Stat(newPath); err == nil {
		return nil
	}

	legacyPath, err := legacyConfigPath()
	if err != nil {
		return err
	}
	if _, err := os.Stat(legacyPath); err != nil {
		return nil
	}

	fmt.Fprintf(os.Stderr, "Found existing config at legacy path: %s\n", legacyPath)
	fmt.Fprintf(os.Stderr, "Migrating to %s\n\n", newPath)

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

	// Split the migrated monolithic config into config.toml + secrets.toml.
	if err := config.MigrateConfigSplit(newPath); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: could not split migrated config: %v\n", err)
	}

	fmt.Fprintf(os.Stderr, "Config migrated. You can remove the old file:\n")
	fmt.Fprintf(os.Stderr, "  rm %s\n\n", legacyPath)

	return nil
}

// legacyConfigPath returns the old user-level config path, resolving the
// actual user when running via sudo.
func legacyConfigPath() (string, error) {
	if sudoUser := os.Getenv("SUDO_USER"); sudoUser != "" {
		home := os.Getenv("SUDO_HOME")
		if home == "" {
			home = filepath.Join("/home", sudoUser)
		}
		return config.LegacyConfigPathForUser(home), nil
	}
	return config.LegacyConfigPath()
}

// runSetupExisting handles the case where bamgate is already configured.
func runSetupExisting(cfgPath string) error {
	fmt.Fprintf(os.Stderr, "bamgate is already configured: %s\n\n", cfgPath)

	if runtime.GOOS == "linux" {
		if _, err := os.Stat(systemdServicePath); err == nil {
			fmt.Fprintf(os.Stderr, "Updating systemd service...\n")
			if err := installSystemdService(); err != nil {
				return fmt.Errorf("updating systemd service: %w", err)
			}
		}
	} else if runtime.GOOS == "darwin" {
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

	// --- Step 1: GitHub authentication ---
	fmt.Fprintf(os.Stderr, "GitHub Authentication\n")
	fmt.Fprintf(os.Stderr, "%s\n", strings.Repeat("-", 21))
	fmt.Fprintf(os.Stderr, "bamgate uses your GitHub identity for authentication.\n\n")

	ghResult, err := auth.DeviceAuth(ctx, func(userCode, verificationURI string) {
		fmt.Fprintf(os.Stderr, "  1. Open %s\n", verificationURI)
		fmt.Fprintf(os.Stderr, "  2. Enter code: %s\n\n", userCode)
		fmt.Fprintf(os.Stderr, "Waiting for authorization...\n")
	})
	if err != nil {
		return fmt.Errorf("GitHub authentication failed: %w", err)
	}
	fmt.Fprintf(os.Stderr, "  Authenticated with GitHub\n\n")

	// --- Step 2: New network or join existing? ---
	fmt.Fprintf(os.Stderr, "Network Setup\n")
	fmt.Fprintf(os.Stderr, "%s\n", strings.Repeat("-", 13))

	isNewNetwork := !promptYesNo(scanner, "Are you joining an existing bamgate network?", false)

	var cfg *config.Config
	if isNewNetwork {
		cfg, err = setupNewNetwork(ctx, scanner, ghResult.AccessToken)
	} else {
		cfg, err = setupJoinNetwork(ctx, scanner, ghResult.AccessToken)
	}
	if err != nil {
		return err
	}

	// --- Step 3: Device configuration ---
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

	if cfg.Network.Name == "" {
		cfg.Network.Name = "default"
	}

	fmt.Fprintf(os.Stderr, "  WireGuard key pair generated\n")

	// --- Step 4: Save config ---
	if err := config.SaveConfig(cfgPath, cfg); err != nil {
		return fmt.Errorf("saving config: %w", err)
	}
	fmt.Fprintf(os.Stderr, "  Config written to %s\n", cfgPath)

	// --- Step 5: Install system service ---
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
	fmt.Fprintf(os.Stderr, "  Tunnel address: %s\n", cfg.Device.Address)
	fmt.Fprintf(os.Stderr, "  Device ID: %s\n", cfg.Network.DeviceID)
	fmt.Fprintf(os.Stderr, "\nTo add another device, run 'sudo bamgate setup' on it\n")
	fmt.Fprintf(os.Stderr, "and authenticate with the same GitHub account.\n")

	return nil
}

// setupNewNetwork deploys a new Cloudflare Worker and registers the first device.
func setupNewNetwork(ctx context.Context, scanner *bufio.Scanner, githubToken string) (*config.Config, error) {
	fmt.Fprintf(os.Stderr, "\nCloudflare Account\n")
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

	subdomain, err := cfClient.GetSubdomain(ctx, account.ID)
	if err != nil {
		return nil, fmt.Errorf("getting workers subdomain: %w\nMake sure you have a workers.dev subdomain configured at https://dash.cloudflare.com", err)
	}

	// --- Deploy Worker ---
	fmt.Fprintf(os.Stderr, "\nSignaling Server\n")
	fmt.Fprintf(os.Stderr, "%s\n", strings.Repeat("-", 16))

	workerName := promptString(scanner, "Worker name", "bamgate")

	// Check if worker already exists.
	exists, err := cfClient.WorkerExists(ctx, account.ID, workerName)
	if err != nil {
		return nil, err
	}

	if !exists {
		fmt.Fprintf(os.Stderr, "Deploying worker...\n")

		modules, err := deploy.WorkerModules()
		if err != nil {
			return nil, fmt.Errorf("loading worker modules: %w", err)
		}

		if err := cfClient.DeployWorker(ctx, deploy.DeployWorkerInput{
			AccountID:        account.ID,
			ScriptName:       workerName,
			Modules:          modules,
			MainModule:       "worker.mjs",
			IncludeMigration: true,
		}); err != nil {
			return nil, fmt.Errorf("deploying worker: %w", err)
		}

		if err := cfClient.EnableWorkerSubdomain(ctx, account.ID, workerName); err != nil {
			return nil, fmt.Errorf("enabling workers.dev route: %w", err)
		}

		fmt.Fprintf(os.Stderr, "  Worker deployed: %s\n", deploy.WorkerURL(workerName, subdomain))

		if err := waitForWorkerReady(ctx, deploy.WorkerURL(workerName, subdomain)); err != nil {
			return nil, fmt.Errorf("waiting for worker: %w", err)
		}
	} else {
		fmt.Fprintf(os.Stderr, "  Worker %q already deployed at %s\n", workerName, deploy.WorkerURL(workerName, subdomain))
	}

	// --- Register device ---
	serverURL := deploy.WorkerURL(workerName, subdomain)
	return registerDevice(ctx, scanner, serverURL, githubToken, apiToken, account.ID, workerName)
}

// setupJoinNetwork registers a device on an existing bamgate network.
func setupJoinNetwork(ctx context.Context, scanner *bufio.Scanner, githubToken string) (*config.Config, error) {
	fmt.Fprintf(os.Stderr, "\nExisting Network\n")
	fmt.Fprintf(os.Stderr, "%s\n", strings.Repeat("-", 16))

	workerName := promptString(scanner, "Worker name", "bamgate")
	subdomain := promptString(scanner, "Cloudflare subdomain", "")
	if subdomain == "" {
		return nil, fmt.Errorf("subdomain is required")
	}

	serverURL := deploy.WorkerURL(workerName, subdomain)
	return registerDevice(ctx, scanner, serverURL, githubToken, "", "", "")
}

// waitForWorkerReady polls the worker's /status endpoint until it returns 200
// or the timeout is reached. This handles Cloudflare propagation delay after a
// fresh deploy.
func waitForWorkerReady(ctx context.Context, serverURL string) error {
	const (
		pollInterval = 2 * time.Second
		timeout      = 30 * time.Second
	)

	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	client := &http.Client{Timeout: 5 * time.Second}
	statusURL := serverURL + "/status"

	fmt.Fprintf(os.Stderr, "  Waiting for worker to become reachable...\n")

	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()

	// Try immediately, then on each tick.
	for {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, statusURL, nil)
		if err != nil {
			return fmt.Errorf("creating status request: %w", err)
		}

		resp, err := client.Do(req)
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return nil
			}
		}

		select {
		case <-ctx.Done():
			return fmt.Errorf("worker not reachable after %s (last probe to %s)", timeout, statusURL)
		case <-ticker.C:
		}
	}
}

// registerDevice calls POST /auth/register to register this device with the
// signaling server and builds the config from the response.
func registerDevice(ctx context.Context, scanner *bufio.Scanner, serverURL, githubToken, apiToken, accountID, workerName string) (*config.Config, error) {
	fmt.Fprintf(os.Stderr, "\nRegistering device...\n")

	hostname, _ := os.Hostname()
	deviceName := hostname

	resp, err := auth.Register(ctx, serverURL, githubToken, deviceName)
	if err != nil {
		return nil, fmt.Errorf("registering device: %w", err)
	}

	fmt.Fprintf(os.Stderr, "  Device registered\n")
	fmt.Fprintf(os.Stderr, "  Tunnel address: %s (auto-assigned)\n", resp.Address)

	// Build the signaling WebSocket URL.
	wsURL, err := normalizeServerURL(serverURL + "/connect")
	if err != nil {
		return nil, fmt.Errorf("normalizing server URL: %w", err)
	}

	cfg := config.DefaultConfig()
	cfg.Network.ServerURL = wsURL
	cfg.Network.TURNSecret = resp.TURNSecret
	cfg.Network.DeviceID = resp.DeviceID
	cfg.Network.RefreshToken = resp.RefreshToken
	cfg.Device.Address = resp.Address

	// Save Cloudflare credentials if this was a new network deploy.
	if apiToken != "" {
		cfg.Cloudflare.APIToken = apiToken
		cfg.Cloudflare.AccountID = accountID
		cfg.Cloudflare.WorkerName = workerName
	}

	return cfg, nil
}

// installSystemdService writes the systemd service file for bamgate.
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
