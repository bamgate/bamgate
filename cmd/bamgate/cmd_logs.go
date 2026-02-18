package main

import (
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strconv"

	"github.com/spf13/cobra"
)

// macOS launchd plist redirects stdout/stderr here.
const logFilePath = "/var/log/bamgate.log"

var (
	logsFollow bool
	logsLines  int
)

var logsCmd = &cobra.Command{
	Use:   "logs",
	Short: "View bamgate service logs",
	Long: `View logs from the bamgate background service.

On Linux, this reads from the systemd journal (journalctl).
On macOS, this reads from /var/log/bamgate.log.

Use -f to follow (stream) logs in real-time.`,
	RunE: runLogs,
}

func init() {
	logsCmd.Flags().BoolVarP(&logsFollow, "follow", "f", false, "stream logs in real-time")
	logsCmd.Flags().IntVarP(&logsLines, "lines", "n", 100, "number of recent log lines to show")
}

func runLogs(cmd *cobra.Command, args []string) error {
	switch runtime.GOOS {
	case "linux":
		return runLogsLinux()
	case "darwin":
		return runLogsDarwin()
	default:
		return fmt.Errorf("bamgate logs is not supported on %s", runtime.GOOS)
	}
}

// runLogsLinux execs journalctl to read the bamgate systemd unit logs.
func runLogsLinux() error {
	journalctl, err := exec.LookPath("journalctl")
	if err != nil {
		return fmt.Errorf("journalctl not found — are you running systemd? %w", err)
	}

	cmdArgs := []string{journalctl, "-u", "bamgate", "-n", strconv.Itoa(logsLines), "--no-pager"}
	if logsFollow {
		cmdArgs = append(cmdArgs, "--follow")
	}

	// Replace the current process so the user gets native journalctl output.
	return syscallExec(journalctl, cmdArgs, os.Environ())
}

// runLogsDarwin execs tail to read the bamgate log file.
func runLogsDarwin() error {
	if _, err := os.Stat(logFilePath); os.IsNotExist(err) {
		return fmt.Errorf("log file %s not found — has bamgate been set up? run: sudo bamgate setup", logFilePath)
	}

	tailBin, err := exec.LookPath("tail")
	if err != nil {
		return fmt.Errorf("tail not found: %w", err)
	}

	cmdArgs := []string{tailBin, "-n", strconv.Itoa(logsLines)}
	if logsFollow {
		cmdArgs = append(cmdArgs, "-f")
	}
	cmdArgs = append(cmdArgs, logFilePath)

	// Replace the current process so output flows naturally.
	return syscallExec(tailBin, cmdArgs, os.Environ())
}
