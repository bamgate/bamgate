package main

import (
	"bufio"
	"context"
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/spf13/cobra"

	"github.com/kuuji/bamgate/internal/config"
	"github.com/kuuji/bamgate/internal/deploy"
)

var workerCmd = &cobra.Command{
	Use:   "worker",
	Short: "Manage the Cloudflare signaling worker",
	Long: `Commands for managing the bamgate Cloudflare Worker that provides
signaling and TURN relay services.

These commands require Cloudflare API credentials. If the credentials
are not in your config (e.g. you joined an existing network), you will
be prompted to provide them.`,
}

var workerInstallCmd = &cobra.Command{
	Use:   "install",
	Short: "Deploy the signaling worker to Cloudflare",
	Long: `Deploy the bamgate signaling worker to your Cloudflare account.
The worker assets (JS glue, Go/Wasm binary, Wasm runtime) are embedded
in the bamgate binary and uploaded via the Cloudflare API.

This is the same deployment that 'bamgate setup' performs when creating
a new network, but can be run independently.`,
	RunE: runWorkerInstall,
}

var workerUpdateCmd = &cobra.Command{
	Use:   "update",
	Short: "Update the signaling worker with the latest embedded assets",
	Long: `Re-deploy the worker assets from the current bamgate binary to
Cloudflare. This updates the JS glue, Go/Wasm binary, and Wasm runtime
without re-running the Durable Object migration.

Use this after updating bamgate to push the latest worker code.`,
	RunE: runWorkerUpdate,
}

var workerUninstallCmd = &cobra.Command{
	Use:   "uninstall",
	Short: "Delete the signaling worker from Cloudflare",
	Long: `Permanently delete the bamgate worker from your Cloudflare account.
This will disconnect all devices using this worker for signaling.

You will be asked to confirm before deletion.`,
	RunE: runWorkerUninstall,
}

var workerInfoCmd = &cobra.Command{
	Use:   "info",
	Short: "Show worker deployment status",
	Long:  `Display information about the deployed worker: name, URL, existence, and health.`,
	RunE:  runWorkerInfo,
}

func init() {
	workerCmd.AddCommand(workerInstallCmd)
	workerCmd.AddCommand(workerUpdateCmd)
	workerCmd.AddCommand(workerUninstallCmd)
	workerCmd.AddCommand(workerInfoCmd)
}

// ensureCFCredentials loads the config and ensures Cloudflare API credentials
// are available. If the credentials are missing from the config, it prompts
// the user interactively and persists them.
func ensureCFCredentials(cfgPath string) (*config.Config, *deploy.Client, error) {
	cfg, err := config.LoadConfig(cfgPath)
	if err != nil {
		return nil, nil, fmt.Errorf("loading config: %w\n\nRun 'sudo bamgate setup' first", err)
	}

	ctx := context.Background()
	scanner := bufio.NewScanner(os.Stdin)
	credentialsChanged := false

	// Prompt for API token if missing.
	if cfg.Cloudflare.APIToken == "" {
		fmt.Fprintf(os.Stderr, "Cloudflare API credentials are not in your config.\n\n")
		fmt.Fprintf(os.Stderr, "Create an API token at:\n")
		fmt.Fprintf(os.Stderr, "  https://dash.cloudflare.com/profile/api-tokens\n\n")
		fmt.Fprintf(os.Stderr, "Required permissions:\n")
		fmt.Fprintf(os.Stderr, "  - Account / Workers Scripts / Edit\n")
		fmt.Fprintf(os.Stderr, "  - Account / Account Settings / Read\n\n")

		apiToken := promptString(scanner, "Paste your API token", "")
		if apiToken == "" {
			return nil, nil, fmt.Errorf("API token is required")
		}
		cfg.Cloudflare.APIToken = apiToken
		credentialsChanged = true
	}

	cfClient := deploy.NewClient(cfg.Cloudflare.APIToken)

	if err := cfClient.VerifyToken(ctx); err != nil {
		return nil, nil, fmt.Errorf("invalid API token: %w", err)
	}

	// Resolve account ID if missing.
	if cfg.Cloudflare.AccountID == "" {
		accounts, err := cfClient.ListAccounts(ctx)
		if err != nil {
			return nil, nil, fmt.Errorf("listing accounts: %w", err)
		}
		if len(accounts) == 0 {
			return nil, nil, fmt.Errorf("no Cloudflare accounts found for this token")
		}

		account := accounts[0]
		if len(accounts) > 1 {
			fmt.Fprintf(os.Stderr, "\nMultiple accounts found:\n")
			for i, a := range accounts {
				fmt.Fprintf(os.Stderr, "  %d. %s (%s)\n", i+1, a.Name, a.ID)
			}
			fmt.Fprintf(os.Stderr, "Using: %s\n", account.Name)
		}

		cfg.Cloudflare.AccountID = account.ID
		credentialsChanged = true

		fmt.Fprintf(os.Stderr, "  Authenticated (account: %s)\n\n", account.Name)
	}

	// Resolve worker name if missing.
	if cfg.Cloudflare.WorkerName == "" {
		workerName := promptString(scanner, "Worker name", "bamgate")
		cfg.Cloudflare.WorkerName = workerName
		credentialsChanged = true
	}

	// Persist credentials if any were prompted.
	if credentialsChanged {
		if err := config.SaveConfig(cfgPath, cfg); err != nil {
			return nil, nil, fmt.Errorf("saving config: %w", err)
		}
		fmt.Fprintf(os.Stderr, "  Cloudflare credentials saved to config\n\n")
	}

	return cfg, cfClient, nil
}

func runWorkerInstall(cmd *cobra.Command, args []string) error {
	cfgPath := resolvedConfigPath()
	cfg, cfClient, err := ensureCFCredentials(cfgPath)
	if err != nil {
		return err
	}

	ctx := context.Background()

	// Check if worker already exists.
	exists, err := cfClient.WorkerExists(ctx, cfg.Cloudflare.AccountID, cfg.Cloudflare.WorkerName)
	if err != nil {
		return fmt.Errorf("checking worker: %w", err)
	}
	if exists {
		return fmt.Errorf("worker %q already exists; use 'bamgate worker update' to re-deploy", cfg.Cloudflare.WorkerName)
	}

	fmt.Fprintf(os.Stderr, "Deploying worker %q...\n", cfg.Cloudflare.WorkerName)

	modules, err := deploy.WorkerModules()
	if err != nil {
		return fmt.Errorf("loading worker modules: %w", err)
	}

	if err := cfClient.DeployWorker(ctx, deploy.DeployWorkerInput{
		AccountID:        cfg.Cloudflare.AccountID,
		ScriptName:       cfg.Cloudflare.WorkerName,
		Modules:          modules,
		MainModule:       "worker.mjs",
		IncludeMigration: true,
	}); err != nil {
		return fmt.Errorf("deploying worker: %w", err)
	}

	if err := cfClient.EnableWorkerSubdomain(ctx, cfg.Cloudflare.AccountID, cfg.Cloudflare.WorkerName); err != nil {
		return fmt.Errorf("enabling workers.dev route: %w", err)
	}

	subdomain, err := cfClient.GetSubdomain(ctx, cfg.Cloudflare.AccountID)
	if err != nil {
		return fmt.Errorf("getting subdomain: %w", err)
	}

	workerURL := deploy.WorkerURL(cfg.Cloudflare.WorkerName, subdomain)
	fmt.Fprintf(os.Stderr, "  Worker deployed: %s\n", workerURL)

	if err := waitForWorkerReady(ctx, workerURL); err != nil {
		fmt.Fprintf(os.Stderr, "  Warning: worker not yet reachable (%v)\n", err)
		fmt.Fprintf(os.Stderr, "  It may take a minute for Cloudflare to propagate.\n")
	} else {
		fmt.Fprintf(os.Stderr, "  Worker is reachable and healthy.\n")
	}

	return nil
}

func runWorkerUpdate(cmd *cobra.Command, args []string) error {
	cfgPath := resolvedConfigPath()
	cfg, cfClient, err := ensureCFCredentials(cfgPath)
	if err != nil {
		return err
	}

	ctx := context.Background()

	// Check if worker exists.
	exists, err := cfClient.WorkerExists(ctx, cfg.Cloudflare.AccountID, cfg.Cloudflare.WorkerName)
	if err != nil {
		return fmt.Errorf("checking worker: %w", err)
	}
	if !exists {
		return fmt.Errorf("worker %q does not exist; use 'bamgate worker install' to deploy it first", cfg.Cloudflare.WorkerName)
	}

	fmt.Fprintf(os.Stderr, "Updating worker %q...\n", cfg.Cloudflare.WorkerName)

	modules, err := deploy.WorkerModules()
	if err != nil {
		return fmt.Errorf("loading worker modules: %w", err)
	}

	if err := cfClient.DeployWorker(ctx, deploy.DeployWorkerInput{
		AccountID:        cfg.Cloudflare.AccountID,
		ScriptName:       cfg.Cloudflare.WorkerName,
		Modules:          modules,
		MainModule:       "worker.mjs",
		IncludeMigration: false,
	}); err != nil {
		return fmt.Errorf("updating worker: %w", err)
	}

	subdomain, err := cfClient.GetSubdomain(ctx, cfg.Cloudflare.AccountID)
	if err != nil {
		fmt.Fprintf(os.Stderr, "  Worker updated (could not resolve URL: %v)\n", err)
		return nil
	}

	workerURL := deploy.WorkerURL(cfg.Cloudflare.WorkerName, subdomain)
	fmt.Fprintf(os.Stderr, "  Worker updated: %s\n", workerURL)

	return nil
}

func runWorkerUninstall(cmd *cobra.Command, args []string) error {
	cfgPath := resolvedConfigPath()
	cfg, cfClient, err := ensureCFCredentials(cfgPath)
	if err != nil {
		return err
	}

	ctx := context.Background()

	// Check if worker exists.
	exists, err := cfClient.WorkerExists(ctx, cfg.Cloudflare.AccountID, cfg.Cloudflare.WorkerName)
	if err != nil {
		return fmt.Errorf("checking worker: %w", err)
	}
	if !exists {
		fmt.Fprintf(os.Stderr, "Worker %q does not exist. Nothing to delete.\n", cfg.Cloudflare.WorkerName)
		return nil
	}

	// Confirmation prompt.
	scanner := bufio.NewScanner(os.Stdin)
	fmt.Fprintf(os.Stderr, "This will permanently delete worker %q from your Cloudflare account.\n", cfg.Cloudflare.WorkerName)
	fmt.Fprintf(os.Stderr, "All devices using this worker for signaling will be disconnected.\n\n")

	if !promptYesNo(scanner, "Are you sure you want to delete the worker?", false) {
		fmt.Fprintf(os.Stderr, "Cancelled.\n")
		return nil
	}

	fmt.Fprintf(os.Stderr, "Deleting worker %q...\n", cfg.Cloudflare.WorkerName)

	if err := cfClient.DeleteWorker(ctx, cfg.Cloudflare.AccountID, cfg.Cloudflare.WorkerName); err != nil {
		return fmt.Errorf("deleting worker: %w", err)
	}

	fmt.Fprintf(os.Stderr, "  Worker deleted.\n")

	return nil
}

func runWorkerInfo(cmd *cobra.Command, args []string) error {
	cfgPath := resolvedConfigPath()
	cfg, cfClient, err := ensureCFCredentials(cfgPath)
	if err != nil {
		return err
	}

	ctx := context.Background()

	fmt.Fprintf(os.Stderr, "Worker: %s\n", cfg.Cloudflare.WorkerName)
	fmt.Fprintf(os.Stderr, "Account: %s\n", cfg.Cloudflare.AccountID)

	// Check if worker exists.
	exists, err := cfClient.WorkerExists(ctx, cfg.Cloudflare.AccountID, cfg.Cloudflare.WorkerName)
	if err != nil {
		return fmt.Errorf("checking worker: %w", err)
	}

	if !exists {
		fmt.Fprintf(os.Stderr, "Status: not deployed\n")
		return nil
	}

	fmt.Fprintf(os.Stderr, "Status: deployed\n")

	subdomain, err := cfClient.GetSubdomain(ctx, cfg.Cloudflare.AccountID)
	if err != nil {
		fmt.Fprintf(os.Stderr, "URL: (could not resolve subdomain: %v)\n", err)
		return nil
	}

	workerURL := deploy.WorkerURL(cfg.Cloudflare.WorkerName, subdomain)
	fmt.Fprintf(os.Stderr, "URL: %s\n", workerURL)

	// Health check.
	healthy := checkWorkerHealth(ctx, workerURL)
	if healthy {
		fmt.Fprintf(os.Stderr, "Health: ok\n")
	} else {
		fmt.Fprintf(os.Stderr, "Health: unreachable\n")
	}

	return nil
}

// checkWorkerHealth probes the worker's /status endpoint and returns whether
// it responds with HTTP 200.
func checkWorkerHealth(ctx context.Context, workerURL string) bool {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	statusURL := workerURL + "/status"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, statusURL, nil)
	if err != nil {
		return false
	}

	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()

	return resp.StatusCode == http.StatusOK
}
