//go:build darwin

package tunnel

import (
	"fmt"
	"log/slog"
	"net"
	"os/exec"
	"strings"
)

const (
	// pfAnchorName is the PF anchor name used by bamgate.
	// All NAT rules are scoped to this anchor so they don't interfere
	// with other PF rules on the system.
	pfAnchorName = "com.bamgate"
)

// NATManager manages PF (Packet Filter) NAT rules for masquerading traffic
// from the WireGuard tunnel to local network subnets on macOS.
//
// Requires root privileges.
type NATManager struct {
	log     *slog.Logger
	anchors []string // anchor rules that have been loaded
}

// NewNATManager creates a new NATManager.
func NewNATManager(logger *slog.Logger) *NATManager {
	return &NATManager{
		log: logger.With("component", "nat"),
	}
}

// SetupMasquerade creates PF NAT rules to masquerade traffic from the
// WireGuard subnet going out through the specified interface.
//
// This is equivalent to loading the following PF rule under an anchor:
//
//	nat on <outIface> from <wgSubnet> to any -> (<outIface>)
//
// The wgSubnet should be in CIDR notation (e.g., "10.0.0.0/24").
// The outIface is the network interface to masquerade on (e.g., "en0").
func (n *NATManager) SetupMasquerade(wgSubnet string, outIface string) error {
	ip, _, err := net.ParseCIDR(wgSubnet)
	if err != nil {
		return fmt.Errorf("parsing WireGuard subnet %q: %w", wgSubnet, err)
	}

	// Only support IPv4 for now.
	if ip.To4() == nil {
		return fmt.Errorf("only IPv4 subnets are supported for masquerade, got %q", wgSubnet)
	}

	// Build the NAT rule. Using parentheses around the interface means PF
	// will dynamically resolve the interface address (handles DHCP changes).
	rule := fmt.Sprintf("nat on %s from %s to any -> (%s)\n", outIface, wgSubnet, outIface)

	// Load the rule into a PF anchor via pfctl.
	// Using -a <anchor> scopes our rules so they don't interfere with
	// the system's main PF configuration.
	cmd := exec.Command("pfctl", "-a", pfAnchorName, "-f", "-")
	cmd.Stdin = strings.NewReader(rule)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("loading PF NAT rule: %w (output: %s)",
			err, strings.TrimSpace(string(out)))
	}

	n.anchors = append(n.anchors, pfAnchorName)

	// Ensure PF is enabled. This is idempotent — if PF is already enabled,
	// pfctl -e returns exit code 1 but that's fine.
	enableCmd := exec.Command("pfctl", "-e")
	_ = enableCmd.Run() // Ignore error: already-enabled returns non-zero.

	n.log.Info("PF NAT masquerade rule added",
		"anchor", pfAnchorName,
		"subnet", wgSubnet,
		"out_iface", outIface,
	)

	return nil
}

// Cleanup removes all PF rules loaded by bamgate.
// This is safe to call even if SetupMasquerade was never called.
func (n *NATManager) Cleanup() error {
	// Flush all rules in the bamgate anchor.
	cmd := exec.Command("pfctl", "-a", pfAnchorName, "-F", "all")
	if out, err := cmd.CombinedOutput(); err != nil {
		// Anchor may not exist, which is fine.
		n.log.Debug("PF cleanup (anchor may not have existed)",
			"error", err, "output", strings.TrimSpace(string(out)))
		return nil
	}

	n.anchors = nil
	n.log.Info("PF bamgate anchor flushed")
	return nil
}
