package main

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"runtime"

	"github.com/spf13/cobra"

	"github.com/kuuji/bamgate/internal/config"
)

var uninstallCmd = &cobra.Command{
	Use:   "uninstall",
	Short: "Remove bamgate from this system",
	Long: `Stop the bamgate service, remove the system service file, and
optionally remove the config and binary.

This command must be run as root:
  sudo bamgate uninstall`,
	RunE: runUninstall,
}

func runUninstall(cmd *cobra.Command, args []string) error {
	if os.Getuid() != 0 {
		return fmt.Errorf("uninstall must be run as root (try: sudo bamgate uninstall)")
	}

	scanner := bufio.NewScanner(os.Stdin)

	// Stop and remove the system service.
	if runtime.GOOS == "linux" {
		if _, err := os.Stat(systemdServicePath); err == nil {
			fmt.Fprintln(os.Stderr, "Stopping and removing systemd service...")

			// Stop and disable the service.
			_ = exec.Command("systemctl", "disable", "--now", "bamgate").Run()

			// Remove the service file.
			if err := os.Remove(systemdServicePath); err != nil {
				fmt.Fprintf(os.Stderr, "Warning: could not remove %s: %v\n", systemdServicePath, err)
			}

			// Reload systemd.
			_ = exec.Command("systemctl", "daemon-reload").Run()

			fmt.Fprintln(os.Stderr, "  systemd service removed")
		}
	} else if runtime.GOOS == "darwin" {
		if _, err := os.Stat(launchdPlistPath); err == nil {
			fmt.Fprintln(os.Stderr, "Stopping and removing launchd service...")

			// Unload the service.
			_ = exec.Command("launchctl", "unload", "-w", launchdPlistPath).Run()

			// Remove the plist.
			if err := os.Remove(launchdPlistPath); err != nil {
				fmt.Fprintf(os.Stderr, "Warning: could not remove %s: %v\n", launchdPlistPath, err)
			}

			fmt.Fprintln(os.Stderr, "  launchd service removed")
		}
	}

	// Remove config.
	if promptYesNo(scanner, "Remove config directory ("+config.DefaultConfigDir+")?", false) {
		if err := os.RemoveAll(config.DefaultConfigDir); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: could not remove %s: %v\n", config.DefaultConfigDir, err)
		} else {
			fmt.Fprintf(os.Stderr, "  Config removed: %s\n", config.DefaultConfigDir)
		}
	}

	// Remove binary.
	binaryPath, err := os.Executable()
	if err == nil {
		if promptYesNo(scanner, "Remove binary ("+binaryPath+")?", false) {
			if err := os.Remove(binaryPath); err != nil {
				fmt.Fprintf(os.Stderr, "Warning: could not remove %s: %v\n", binaryPath, err)
			} else {
				fmt.Fprintf(os.Stderr, "  Binary removed: %s\n", binaryPath)
			}
		}
	}

	fmt.Fprintln(os.Stderr, "\nbamgate has been uninstalled.")

	return nil
}
