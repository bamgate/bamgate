package main

import (
	"archive/tar"
	"compress/gzip"
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
)

const (
	githubRepo   = "bamgate/bamgate"
	githubAPIURL = "https://api.github.com/repos/" + githubRepo + "/releases/latest"
	binaryName   = "bamgate"
)

var updateCmd = &cobra.Command{
	Use:   "update",
	Short: "Update bamgate to the latest version",
	Long: `Check GitHub for the latest release and update the binary in place.

If a systemd or launchd service is running, it will be restarted
after the update.

This command must be run as root:
  sudo bamgate update`,
	RunE: runUpdate,
}

func runUpdate(cmd *cobra.Command, args []string) error {
	if os.Getuid() != 0 {
		return fmt.Errorf("update must be run as root (try: sudo bamgate update)")
	}

	// Fetch latest release info from GitHub.
	fmt.Fprintf(os.Stderr, "Checking for updates...\n")

	release, err := fetchLatestRelease()
	if err != nil {
		return fmt.Errorf("checking for updates: %w", err)
	}

	latestVersion := strings.TrimPrefix(release.TagName, "v")
	currentVersion := strings.TrimPrefix(version, "v")

	if latestVersion == currentVersion {
		fmt.Fprintf(os.Stderr, "Already up to date (v%s).\n", currentVersion)
		return nil
	}

	fmt.Fprintf(os.Stderr, "Current version: v%s\n", currentVersion)
	fmt.Fprintf(os.Stderr, "Latest version:  v%s\n", latestVersion)
	fmt.Fprintf(os.Stderr, "Updating...\n")

	// Find the right asset for this OS/arch.
	assetName := fmt.Sprintf("bamgate_%s_%s_%s.tar.gz", latestVersion, runtime.GOOS, goArchToRelease())
	var downloadURL string
	for _, asset := range release.Assets {
		if asset.Name == assetName {
			downloadURL = asset.BrowserDownloadURL
			break
		}
	}

	if downloadURL == "" {
		return fmt.Errorf("no release found for %s/%s (looking for %s)", runtime.GOOS, runtime.GOARCH, assetName)
	}

	// Download the tarball.
	fmt.Fprintf(os.Stderr, "Downloading %s...\n", assetName)

	binaryData, err := downloadAndExtractBinary(downloadURL)
	if err != nil {
		return fmt.Errorf("downloading update: %w", err)
	}

	// Determine install path.
	installPath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("finding current binary: %w", err)
	}
	installPath, err = filepath.EvalSymlinks(installPath)
	if err != nil {
		return fmt.Errorf("resolving binary path: %w", err)
	}

	// Atomic replace: write to temp file, then rename.
	dir := filepath.Dir(installPath)
	tmp, err := os.CreateTemp(dir, ".bamgate-update-*")
	if err != nil {
		return fmt.Errorf("creating temp file: %w", err)
	}
	tmpPath := tmp.Name()
	defer func() {
		if tmpPath != "" {
			os.Remove(tmpPath)
		}
	}()

	if _, err := tmp.Write(binaryData); err != nil {
		tmp.Close()
		return fmt.Errorf("writing update: %w", err)
	}
	if err := tmp.Chmod(0755); err != nil {
		tmp.Close()
		return fmt.Errorf("setting permissions: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("closing temp file: %w", err)
	}

	if err := os.Rename(tmpPath, installPath); err != nil {
		return fmt.Errorf("replacing binary: %w", err)
	}
	tmpPath = "" // Prevent deferred removal.

	fmt.Fprintf(os.Stderr, "Updated to v%s at %s\n", latestVersion, installPath)

	// Restart service if running.
	if runtime.GOOS == "linux" {
		restartSystemdIfActive()
	} else if runtime.GOOS == "darwin" {
		restartLaunchdIfActive()
	}

	// Remove quarantine on macOS.
	if runtime.GOOS == "darwin" {
		// Ignore error — xattr may not exist or quarantine may not be set.
		_ = exec.Command("xattr", "-dr", "com.apple.quarantine", installPath).Run()
	}

	return nil
}

// githubRelease is a subset of the GitHub API release response.
type githubRelease struct {
	TagName string        `json:"tag_name"`
	Assets  []githubAsset `json:"assets"`
}

type githubAsset struct {
	Name               string `json:"name"`
	BrowserDownloadURL string `json:"browser_download_url"`
}

func fetchLatestRelease() (*githubRelease, error) {
	client := &http.Client{Timeout: 15 * time.Second}
	req, err := http.NewRequest(http.MethodGet, githubAPIURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("GitHub API returned %d: %s", resp.StatusCode, string(body))
	}

	var release githubRelease
	if err := json.NewDecoder(resp.Body).Decode(&release); err != nil {
		return nil, fmt.Errorf("parsing release: %w", err)
	}

	return &release, nil
}

// downloadAndExtractBinary downloads a tarball and extracts the bamgate binary.
func downloadAndExtractBinary(url string) ([]byte, error) {
	client := &http.Client{Timeout: 120 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("download failed: HTTP %d", resp.StatusCode)
	}

	gz, err := gzip.NewReader(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("gzip reader: %w", err)
	}
	defer gz.Close()

	tr := tar.NewReader(gz)
	for {
		header, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("reading tar: %w", err)
		}

		// Look for the bamgate binary (may be at top level or in a directory).
		if header.Typeflag == tar.TypeReg && filepath.Base(header.Name) == binaryName {
			data, err := io.ReadAll(tr)
			if err != nil {
				return nil, fmt.Errorf("extracting binary: %w", err)
			}
			return data, nil
		}
	}

	return nil, fmt.Errorf("binary %q not found in tarball", binaryName)
}

// goArchToRelease maps GOARCH to GoReleaser archive naming.
func goArchToRelease() string {
	switch runtime.GOARCH {
	case "amd64":
		return "amd64"
	case "arm64":
		return "arm64"
	default:
		return runtime.GOARCH
	}
}

func restartSystemdIfActive() {
	// Check if the service is active.
	check := exec.Command("systemctl", "is-active", "--quiet", "bamgate")
	if check.Run() != nil {
		return // Not active.
	}

	fmt.Fprintf(os.Stderr, "Restarting systemd service...\n")
	restart := exec.Command("systemctl", "restart", "bamgate")
	restart.Stdout = os.Stderr
	restart.Stderr = os.Stderr
	if err := restart.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: failed to restart service: %v\n", err)
	} else {
		fmt.Fprintf(os.Stderr, "Service restarted.\n")
	}
}

func restartLaunchdIfActive() {
	if _, err := os.Stat(launchdPlistPath); os.IsNotExist(err) {
		return // Not installed.
	}

	// Check if the service is loaded.
	check := exec.Command("launchctl", "list", "com.bamgate.bamgate")
	if check.Run() != nil {
		return // Not loaded.
	}

	fmt.Fprintf(os.Stderr, "Restarting launchd service...\n")
	// Stop and re-load the service.
	_ = exec.Command("launchctl", "unload", launchdPlistPath).Run()
	load := exec.Command("launchctl", "load", "-w", launchdPlistPath)
	load.Stdout = os.Stderr
	load.Stderr = os.Stderr
	if err := load.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: failed to restart service: %v\n", err)
	} else {
		fmt.Fprintf(os.Stderr, "Service restarted.\n")
	}
}
