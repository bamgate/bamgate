package main

import (
	"fmt"
	"os"
	"os/exec"
	"runtime"

	"github.com/spf13/cobra"
)

var restartCmd = &cobra.Command{
	Use:   "restart",
	Short: "Restart the bamgate daemon",
	Long: `Restart the bamgate system service. This is equivalent to running
'sudo bamgate down' followed by 'sudo bamgate up -d', but in a single step.

This command must be run as root:
  sudo bamgate restart`,
	RunE: runRestart,
}

func runRestart(cmd *cobra.Command, args []string) error {
	if os.Getuid() != 0 {
		return fmt.Errorf("'bamgate restart' requires root (try: sudo bamgate restart)")
	}

	switch runtime.GOOS {
	case "linux":
		return runRestartLinux()
	case "darwin":
		return runRestartDarwin()
	default:
		return fmt.Errorf("'bamgate restart' is not supported on %s", runtime.GOOS)
	}
}

func runRestartLinux() error {
	if _, err := os.Stat(systemdServicePath); os.IsNotExist(err) {
		return fmt.Errorf("systemd service not installed; run 'sudo bamgate setup' first")
	}

	// Check if the service is active.
	check := exec.Command("systemctl", "is-active", "--quiet", "bamgate")
	if err := check.Run(); err != nil {
		return fmt.Errorf("bamgate service is not running; use 'sudo bamgate up -d' to start it")
	}

	fmt.Fprintln(os.Stderr, "Restarting bamgate service...")

	restart := exec.Command("systemctl", "restart", "bamgate")
	restart.Stdout = os.Stderr
	restart.Stderr = os.Stderr
	if err := restart.Run(); err != nil {
		return fmt.Errorf("systemctl restart bamgate: %w", err)
	}

	fmt.Fprintln(os.Stderr, "bamgate restarted.")
	fmt.Fprintln(os.Stderr, "Use 'bamgate status' to check connection state.")

	return nil
}

func runRestartDarwin() error {
	if _, err := os.Stat(launchdPlistPath); os.IsNotExist(err) {
		return fmt.Errorf("launchd service not installed; run 'sudo bamgate setup' first")
	}

	// Check if the service is loaded.
	check := exec.Command("launchctl", "list", "com.bamgate.bamgate")
	if err := check.Run(); err != nil {
		return fmt.Errorf("bamgate service is not running; use 'sudo bamgate up -d' to start it")
	}

	fmt.Fprintln(os.Stderr, "Restarting bamgate service...")

	unload := exec.Command("launchctl", "unload", launchdPlistPath)
	unload.Stdout = os.Stderr
	unload.Stderr = os.Stderr
	if err := unload.Run(); err != nil {
		return fmt.Errorf("launchctl unload: %w", err)
	}

	load := exec.Command("launchctl", "load", "-w", launchdPlistPath)
	load.Stdout = os.Stderr
	load.Stderr = os.Stderr
	if err := load.Run(); err != nil {
		return fmt.Errorf("launchctl load: %w", err)
	}

	fmt.Fprintln(os.Stderr, "bamgate restarted.")
	fmt.Fprintln(os.Stderr, "Use 'bamgate status' to check connection state.")

	return nil
}
