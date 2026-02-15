package main

import (
	"fmt"
	"os"
	"os/exec"
	"runtime"

	"github.com/spf13/cobra"
)

var downCmd = &cobra.Command{
	Use:   "down",
	Short: "Stop the network connection",
	Long: `Stop the bamgate system service and disable it from starting on boot.

This is the counterpart to 'sudo bamgate up -d'.
If bamgate is running in the foreground, press Ctrl+C to stop it instead.`,
	RunE: runDown,
}

func runDown(cmd *cobra.Command, args []string) error {
	if os.Getuid() != 0 {
		return fmt.Errorf("'bamgate down' requires root (try: sudo bamgate down)")
	}

	switch runtime.GOOS {
	case "linux":
		return runDownLinux()
	case "darwin":
		return runDownDarwin()
	default:
		return fmt.Errorf("'bamgate down' is not supported on %s", runtime.GOOS)
	}
}

func runDownLinux() error {
	if _, err := os.Stat(systemdServicePath); os.IsNotExist(err) {
		return fmt.Errorf("systemd service not installed; nothing to stop")
	}

	fmt.Fprintln(os.Stderr, "Stopping and disabling bamgate service...")

	systemctl := exec.Command("systemctl", "disable", "--now", "bamgate")
	systemctl.Stdout = os.Stderr
	systemctl.Stderr = os.Stderr
	if err := systemctl.Run(); err != nil {
		return fmt.Errorf("systemctl disable --now bamgate: %w", err)
	}

	fmt.Fprintln(os.Stderr, "bamgate stopped and disabled.")

	return nil
}

func runDownDarwin() error {
	if _, err := os.Stat(launchdPlistPath); os.IsNotExist(err) {
		return fmt.Errorf("launchd service not installed; nothing to stop")
	}

	fmt.Fprintln(os.Stderr, "Stopping and unloading bamgate service...")

	launchctl := exec.Command("launchctl", "unload", "-w", launchdPlistPath)
	launchctl.Stdout = os.Stderr
	launchctl.Stderr = os.Stderr
	if err := launchctl.Run(); err != nil {
		return fmt.Errorf("launchctl unload: %w", err)
	}

	fmt.Fprintln(os.Stderr, "bamgate stopped and disabled.")

	return nil
}
