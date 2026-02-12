package main

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"runtime"

	"github.com/spf13/cobra"
)

var (
	installPrefix  string
	installSystemd bool
)

var installCmd = &cobra.Command{
	Use:   "install",
	Short: "Install riftgate with required capabilities",
	Long: `Copy the riftgate binary to a system path and set Linux capabilities
so it can create TUN devices without running as root.

This command must be run with sudo:
  sudo riftgate install

What it does:
  1. Copies the binary to /usr/local/bin/riftgate (or --prefix)
  2. Sets CAP_NET_ADMIN and CAP_NET_RAW capabilities on the binary
  3. Optionally installs the systemd service file (--systemd)

After installation, any user can run 'riftgate up' without sudo.`,
	RunE: runInstall,
}

func init() {
	installCmd.Flags().StringVar(&installPrefix, "prefix", "/usr/local", "installation prefix (binary goes to <prefix>/bin/)")
	installCmd.Flags().BoolVar(&installSystemd, "systemd", false, "also install the systemd service file")
}

func runInstall(cmd *cobra.Command, args []string) error {
	if runtime.GOOS != "linux" {
		return fmt.Errorf("install is only supported on Linux")
	}

	if os.Getuid() != 0 {
		return fmt.Errorf("install must be run as root (try: sudo riftgate install)")
	}

	// Resolve the current binary path.
	self, err := os.Executable()
	if err != nil {
		return fmt.Errorf("finding current binary: %w", err)
	}
	self, err = filepath.EvalSymlinks(self)
	if err != nil {
		return fmt.Errorf("resolving binary path: %w", err)
	}

	destDir := filepath.Join(installPrefix, "bin")
	destPath := filepath.Join(destDir, "riftgate")

	// Create destination directory if needed.
	if err := os.MkdirAll(destDir, 0755); err != nil {
		return fmt.Errorf("creating %s: %w", destDir, err)
	}

	// Copy binary.
	if self == destPath {
		fmt.Fprintf(os.Stderr, "Binary already at %s, skipping copy.\n", destPath)
	} else {
		fmt.Fprintf(os.Stderr, "Copying %s -> %s\n", self, destPath)
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
	}

	// Set capabilities.
	fmt.Fprintf(os.Stderr, "Setting capabilities on %s\n", destPath)
	setcap := exec.Command("setcap", "cap_net_admin,cap_net_raw+eip", destPath)
	setcap.Stdout = os.Stderr
	setcap.Stderr = os.Stderr
	if err := setcap.Run(); err != nil {
		return fmt.Errorf("setcap failed (is libcap installed?): %w", err)
	}

	// Optionally install systemd service.
	if installSystemd {
		if err := installSystemdService(destPath); err != nil {
			return err
		}
	}

	fmt.Fprintf(os.Stderr, "\nInstallation complete.\n")
	fmt.Fprintf(os.Stderr, "You can now run 'riftgate up' without sudo.\n")
	if installSystemd {
		fmt.Fprintf(os.Stderr, "To enable the service: sudo systemctl enable --now riftgate\n")
	}

	return nil
}

// copyFile copies a file preserving permissions, then sets 0755 on the destination.
func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0755)
	if err != nil {
		return err
	}
	defer out.Close()

	if _, err := io.Copy(out, in); err != nil {
		return err
	}

	return out.Close()
}

// resolveRealUser returns the non-root user who invoked sudo.
// Falls back to the current user if SUDO_USER is not set.
func resolveRealUser() (*user.User, error) {
	username := os.Getenv("SUDO_USER")
	if username == "" {
		return user.Current()
	}
	return user.Lookup(username)
}

// installSystemdService writes the service file and updates the ExecStart path.
// The service runs as the real user (from SUDO_USER), not root — capabilities
// are granted via AmbientCapabilities.
func installSystemdService(binaryPath string) error {
	u, err := resolveRealUser()
	if err != nil {
		return fmt.Errorf("resolving user for systemd service: %w", err)
	}

	// Look up the user's primary group name.
	grp, err := user.LookupGroupId(u.Gid)
	if err != nil {
		return fmt.Errorf("resolving group for uid %s: %w", u.Gid, err)
	}

	fmt.Fprintf(os.Stderr, "Service will run as user=%s group=%s\n", u.Username, grp.Name)

	serviceContent := fmt.Sprintf(`[Unit]
Description=riftgate - WireGuard VPN tunnel over WebRTC
Documentation=https://github.com/kuuji/riftgate
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
ExecStart=%s up
Restart=on-failure
RestartSec=5

# Run as the installing user, not root. Capabilities are granted below.
User=%s
Group=%s

# Runtime directory for the control socket.
RuntimeDirectory=riftgate
RuntimeDirectoryMode=0755

# Security hardening.
# riftgate needs CAP_NET_ADMIN to create TUN devices and configure interfaces,
# and CAP_NET_RAW for raw socket operations used by WireGuard.
AmbientCapabilities=CAP_NET_ADMIN CAP_NET_RAW
CapabilityBoundingSet=CAP_NET_ADMIN CAP_NET_RAW
NoNewPrivileges=yes

# Filesystem restrictions.
ProtectSystem=strict
ProtectHome=read-only
ReadWritePaths=/run/riftgate
PrivateTmp=yes

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
`, binaryPath, u.Username, grp.Name)

	servicePath := "/etc/systemd/system/riftgate.service"
	fmt.Fprintf(os.Stderr, "Installing systemd service to %s\n", servicePath)

	if err := os.WriteFile(servicePath, []byte(serviceContent), 0644); err != nil {
		return fmt.Errorf("writing service file: %w", err)
	}

	// Reload systemd.
	reload := exec.Command("systemctl", "daemon-reload")
	reload.Stdout = os.Stderr
	reload.Stderr = os.Stderr
	if err := reload.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: systemctl daemon-reload failed: %v\n", err)
	}

	return nil
}
