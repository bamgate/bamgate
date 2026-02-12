//go:build darwin

package tunnel

import (
	"fmt"
	"net"
	"os/exec"
	"strings"
)

// AddAddress assigns an IP address in CIDR notation to a network interface.
// On macOS, this calls `ifconfig <ifName> inet <ip> <ip> netmask <mask>`.
// Requires root privileges.
func AddAddress(ifName string, cidr string) error {
	ip, ipNet, err := net.ParseCIDR(cidr)
	if err != nil {
		return fmt.Errorf("parsing CIDR %q: %w", cidr, err)
	}

	ipv4 := ip.To4()
	if ipv4 == nil {
		return fmt.Errorf("only IPv4 addresses are supported on macOS, got %q", cidr)
	}

	mask := net.IP(ipNet.Mask).String()
	prefixLen, _ := ipNet.Mask.Size()

	// macOS ifconfig for point-to-point TUN:
	//   ifconfig <utunN> inet <local_ip> <local_ip> netmask <mask>
	// The second address is the "destination" for a point-to-point link;
	// using the same address as local works for our use case.
	cmd := exec.Command("ifconfig", ifName, "inet", ip.String(), ip.String(), "netmask", mask)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("ifconfig add address %s on %s: %w (output: %s)",
			cidr, ifName, err, strings.TrimSpace(string(out)))
	}

	// macOS point-to-point TUN interfaces don't automatically create a
	// connected route for the subnet (unlike Linux). Add it explicitly
	// so the kernel knows to route the entire subnet through this interface.
	subnetRoute := fmt.Sprintf("%s/%d", ipNet.IP.String(), prefixLen)
	routeCmd := exec.Command("route", "-n", "add", "-net", subnetRoute, "-interface", ifName)
	if out, err := routeCmd.CombinedOutput(); err != nil {
		return fmt.Errorf("adding subnet route %s on %s: %w (output: %s)",
			subnetRoute, ifName, err, strings.TrimSpace(string(out)))
	}

	return nil
}

// SetLinkUp brings a network interface into the UP state.
// On macOS, this calls `ifconfig <ifName> up`.
// Requires root privileges.
func SetLinkUp(ifName string) error {
	cmd := exec.Command("ifconfig", ifName, "up")
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("ifconfig set %s up: %w (output: %s)",
			ifName, err, strings.TrimSpace(string(out)))
	}
	return nil
}

// AddRoute adds a kernel route for the given destination subnet via the named
// interface. On macOS, this calls `route -n add -net <cidr> -interface <ifName>`.
// Requires root privileges.
func AddRoute(ifName string, cidr string) error {
	_, _, err := net.ParseCIDR(cidr)
	if err != nil {
		return fmt.Errorf("parsing CIDR %q: %w", cidr, err)
	}

	cmd := exec.Command("route", "-n", "add", "-net", cidr, "-interface", ifName)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("route add %s via %s: %w (output: %s)",
			cidr, ifName, err, strings.TrimSpace(string(out)))
	}
	return nil
}

// RemoveRoute removes a kernel route for the given destination subnet.
// On macOS, this calls `route -n delete -net <cidr>`.
// Requires root privileges.
func RemoveRoute(ifName string, cidr string) error {
	_, _, err := net.ParseCIDR(cidr)
	if err != nil {
		return fmt.Errorf("parsing CIDR %q: %w", cidr, err)
	}

	cmd := exec.Command("route", "-n", "delete", "-net", cidr)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("route delete %s: %w (output: %s)",
			cidr, err, strings.TrimSpace(string(out)))
	}
	return nil
}

// GetForwarding reads the current global IPv4 forwarding state.
// On macOS, forwarding is a global setting (not per-interface like Linux).
// The ifName parameter is accepted for API compatibility but ignored.
func GetForwarding(_ string) (bool, error) {
	cmd := exec.Command("sysctl", "-n", "net.inet.ip.forwarding")
	out, err := cmd.Output()
	if err != nil {
		return false, fmt.Errorf("reading forwarding state: %w", err)
	}
	return strings.TrimSpace(string(out)) == "1", nil
}

// SetForwarding enables or disables global IPv4 forwarding.
// On macOS, forwarding is a global setting (not per-interface like Linux).
// The ifName parameter is accepted for API compatibility but ignored.
// Requires root privileges.
func SetForwarding(_ string, enabled bool) error {
	val := "0"
	if enabled {
		val = "1"
	}

	cmd := exec.Command("sysctl", "-w", "net.inet.ip.forwarding="+val)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("setting forwarding to %s: %w (output: %s)",
			val, err, strings.TrimSpace(string(out)))
	}
	return nil
}
