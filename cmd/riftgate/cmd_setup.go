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
	"os/user"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/kuuji/riftgate/internal/config"
	"github.com/kuuji/riftgate/internal/deploy"
)

var setupCmd = &cobra.Command{
	Use:   "setup",
	Short: "Set up riftgate: deploy signaling server and configure this device",
	Long: `Interactive setup wizard that handles everything needed to get riftgate running:

  1. Deploy the signaling worker to Cloudflare (or detect existing deployment)
  2. Configure this device (name, WireGuard keys, tunnel address)
  3. Install the binary with required capabilities
  4. Optionally install the systemd service

If you have an invite code from another device, setup will use it to
retrieve the server configuration automatically — no Cloudflare account needed.

This command should be run with sudo:
  sudo riftgate setup`,
	RunE: runSetup,
}

func runSetup(cmd *cobra.Command, args []string) error {
	if runtime.GOOS != "linux" {
		return fmt.Errorf("setup is only supported on Linux")
	}

	if os.Getuid() != 0 {
		return fmt.Errorf("setup must be run as root (try: sudo riftgate setup)")
	}

	scanner := bufio.NewScanner(os.Stdin)
	ctx := context.Background()

	// Resolve the real (non-root) user who invoked sudo.
	realUser, err := resolveRealUser()
	if err != nil {
		return fmt.Errorf("resolving user: %w", err)
	}
	cfgPath := config.ConfigPathForUser(realUser.HomeDir)

	// Check for existing config.
	existingCfg, _ := config.LoadConfig(cfgPath)
	if existingCfg != nil {
		fmt.Fprintf(os.Stderr, "Existing config found: %s\n", cfgPath)
		if !promptYesNo(scanner, "Overwrite?", false) {
			fmt.Fprintln(os.Stderr, "Aborted.")
			return nil
		}
	}

	fmt.Fprintf(os.Stderr, "\nriftgate setup\n")
	fmt.Fprintf(os.Stderr, "%s\n\n", strings.Repeat("=", 14))

	// Ask whether the user has an invite code.
	hasInvite := promptYesNo(scanner, "Do you have an invite code from another device?", false)

	var cfg *config.Config
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

	// --- Save config (as real user) ---
	if err := config.SaveConfig(cfgPath, cfg); err != nil {
		return fmt.Errorf("saving config: %w", err)
	}
	chownForUser(cfgPath, realUser)
	fmt.Fprintf(os.Stderr, "  Config written to %s\n", cfgPath)

	// --- Install binary + capabilities ---
	fmt.Fprintf(os.Stderr, "\nInstallation\n")
	fmt.Fprintf(os.Stderr, "%s\n", strings.Repeat("-", 12))

	if err := installBinaryWithCaps("/usr/local"); err != nil {
		return fmt.Errorf("installing binary: %w", err)
	}

	// Optionally install systemd service.
	if promptYesNo(scanner, "Install systemd service?", true) {
		destPath := "/usr/local/bin/riftgate"
		if err := installSystemdService(destPath); err != nil {
			return fmt.Errorf("installing systemd service: %w", err)
		}
	}

	// --- Done ---
	fmt.Fprintf(os.Stderr, "\nSetup complete! Run 'riftgate up' to connect.\n")
	fmt.Fprintf(os.Stderr, "  Public key: %s\n", pubKey.String())
	fmt.Fprintf(os.Stderr, "\nTo add another device to this network, run on a connected device:\n")
	fmt.Fprintf(os.Stderr, "  riftgate invite\n")

	return nil
}

// setupWithInvite handles setup by redeeming an invite code from the worker.
func setupWithInvite(ctx context.Context, scanner *bufio.Scanner) (*config.Config, error) {
	fmt.Fprintf(os.Stderr, "\nInvite Setup\n")
	fmt.Fprintf(os.Stderr, "%s\n", strings.Repeat("-", 12))

	workerName := promptString(scanner, "Worker name", "riftgate")
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
		ServerURL string `json:"server_url"`
		AuthToken string `json:"auth_token"`
		Address   string `json:"address"`
		Subnet    string `json:"subnet"`
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
	workerName := "riftgate"
	exists, err := cfClient.WorkerExists(ctx, account.ID, workerName)
	if err != nil {
		return nil, err
	}

	var authToken string

	if exists {
		fmt.Fprintf(os.Stderr, "  Worker %q already deployed at %s\n", workerName, deploy.WorkerURL(workerName, subdomain))

		// Try to read back the auth token from bindings.
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
		cfg.Device.Address = address
		return cfg, nil
	}

	// --- First-time deploy ---
	fmt.Fprintf(os.Stderr, "\nSignaling Server\n")
	fmt.Fprintf(os.Stderr, "%s\n", strings.Repeat("-", 16))
	fmt.Fprintf(os.Stderr, "No existing riftgate worker found. Deploying...\n")

	workerName = promptString(scanner, "Worker name", "riftgate")

	// Generate auth token.
	authToken, err = generateAuthToken()
	if err != nil {
		return nil, fmt.Errorf("generating auth token: %w", err)
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
	fmt.Fprintf(os.Stderr, "  Auth token generated and configured\n")

	// First device gets the first address in the subnet.
	address := "10.0.0.1/24"
	fmt.Fprintf(os.Stderr, "  Tunnel address: %s (first device)\n", address)

	cfg := config.DefaultConfig()
	cfg.Cloudflare.APIToken = apiToken
	cfg.Cloudflare.AccountID = account.ID
	cfg.Cloudflare.WorkerName = workerName
	cfg.Network.ServerURL = deploy.WorkerWSURL(workerName, subdomain)
	cfg.Network.AuthToken = authToken
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
	return "rg_" + hex.EncodeToString(b), nil
}

// installBinaryWithCaps copies the running binary to the system path and
// sets the required Linux capabilities.
func installBinaryWithCaps(prefix string) error {
	self, err := os.Executable()
	if err != nil {
		return fmt.Errorf("finding current binary: %w", err)
	}
	self, err = filepath.EvalSymlinks(self)
	if err != nil {
		return fmt.Errorf("resolving binary path: %w", err)
	}

	destDir := filepath.Join(prefix, "bin")
	destPath := filepath.Join(destDir, "riftgate")

	if err := os.MkdirAll(destDir, 0755); err != nil {
		return fmt.Errorf("creating %s: %w", destDir, err)
	}

	if self == destPath {
		fmt.Fprintf(os.Stderr, "  Binary already at %s\n", destPath)
	} else {
		// Write to a temp file first, then atomically rename. This avoids
		// "text file busy" errors when the destination binary is currently running.
		tmpPath := destPath + ".tmp"
		if err := copyFile(self, tmpPath); err != nil {
			return fmt.Errorf("copying binary: %w", err)
		}
		if err := os.Rename(tmpPath, destPath); err != nil {
			os.Remove(tmpPath) //nolint:errcheck // best-effort cleanup
			return fmt.Errorf("installing binary: %w", err)
		}
		fmt.Fprintf(os.Stderr, "  Binary installed to %s\n", destPath)
	}

	// Set capabilities.
	setcap := exec.Command("setcap", "cap_net_admin,cap_net_raw+eip", destPath)
	setcap.Stdout = os.Stderr
	setcap.Stderr = os.Stderr
	if err := setcap.Run(); err != nil {
		return fmt.Errorf("setcap failed (is libcap installed?): %w", err)
	}
	fmt.Fprintf(os.Stderr, "  Capabilities set\n")

	return nil
}

// chownForUser sets file and parent directory ownership to the given user.
// This is used when running as root to ensure config files are owned by the
// real user who invoked sudo.
func chownForUser(path string, u *user.User) {
	uid, err := strconv.Atoi(u.Uid)
	if err != nil || uid == 0 {
		return
	}
	gid, err := strconv.Atoi(u.Gid)
	if err != nil {
		return
	}

	// Chown the file itself.
	_ = os.Chown(path, uid, gid)

	// Walk up through riftgate config directory.
	dir := filepath.Dir(path)
	_ = os.Chown(dir, uid, gid)
}
