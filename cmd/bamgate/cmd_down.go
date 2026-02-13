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
	Long: `Stop the bamgate systemd service and disable it from starting on boot.

This is the counterpart to 'bamgate up -d'.`,
	RunE: runDown,
}

func runDown(cmd *cobra.Command, args []string) error {
	if runtime.GOOS != "linux" {
		return fmt.Errorf("'bamgate down' requires systemd and is only supported on Linux; launchd support is not yet implemented\n\nIf bamgate is running in the foreground, press Ctrl+C to stop it")
	}
	if _, err := os.Stat(systemdServicePath); os.IsNotExist(err) {
		return fmt.Errorf("systemd service not installed; nothing to stop")
	}

	fmt.Fprintln(os.Stderr, "Stopping and disabling bamgate service...")

	systemctl := exec.Command("sudo", "systemctl", "disable", "--now", "bamgate")
	systemctl.Stdout = os.Stderr
	systemctl.Stderr = os.Stderr
	if err := systemctl.Run(); err != nil {
		return fmt.Errorf("systemctl disable --now bamgate: %w", err)
	}

	fmt.Fprintln(os.Stderr, "bamgate stopped and disabled.")

	return nil
}
