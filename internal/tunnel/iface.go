package tunnel

import (
	"fmt"
	"net"
	"strings"
)

// SubnetInfo describes a local network subnet discovered on a host interface.
type SubnetInfo struct {
	CIDR      string // network CIDR, e.g. "192.168.1.0/24"
	Interface string // interface name, e.g. "wlan0"
}

// virtualPrefixes are interface name prefixes for virtual/container network
// interfaces that are generally not useful to advertise as VPN routes.
var virtualPrefixes = []string{
	"docker", "veth", "br-", "virbr", "lxc", "lxd",
	"cni", "flannel", "calico", "weave",
	"tun", "wg", "tailscale", "utun",
	"podman", "cali", "vxlan",
}

// DiscoverLocalSubnets enumerates all network interfaces and returns the
// IPv4 subnets that are likely to be real, physical/routable networks.
//
// It filters out:
//   - Loopback interfaces
//   - Down interfaces
//   - Link-local addresses (169.254.0.0/16, fe80::/10)
//   - IPv6 addresses (VPN routes are IPv4-only for now)
//   - Virtual/container interfaces (docker, veth, br-, virbr, etc.)
//
// The optional excludeCIDR parameter allows filtering out a specific subnet
// (e.g. the WireGuard tunnel subnet that was just assigned).
func DiscoverLocalSubnets(excludeCIDR string) ([]SubnetInfo, error) {
	ifaces, err := net.Interfaces()
	if err != nil {
		return nil, fmt.Errorf("listing interfaces: %w", err)
	}

	var exclude *net.IPNet
	if excludeCIDR != "" {
		_, exclude, _ = net.ParseCIDR(excludeCIDR)
	}

	seen := make(map[string]bool)
	var results []SubnetInfo

	for _, iface := range ifaces {
		if shouldSkipInterface(iface) {
			continue
		}

		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}

		for _, addr := range addrs {
			ip, ipNet, err := net.ParseCIDR(addr.String())
			if err != nil {
				continue
			}

			// IPv4 only for now.
			if ip.To4() == nil {
				continue
			}

			// Skip link-local (169.254.0.0/16).
			if ip[0] == 169 && ip[1] == 254 {
				continue
			}

			// Compute the network address (mask the IP).
			networkIP := ip.Mask(ipNet.Mask)
			ones, bits := ipNet.Mask.Size()
			cidr := fmt.Sprintf("%s/%d", networkIP, ones)

			// Skip host routes (/32).
			if ones == bits {
				continue
			}

			// Skip the excluded subnet (e.g. WireGuard tunnel).
			if exclude != nil && exclude.String() == cidr {
				continue
			}

			// Deduplicate by CIDR (multiple IPs on same subnet).
			if seen[cidr] {
				continue
			}
			seen[cidr] = true

			results = append(results, SubnetInfo{
				CIDR:      cidr,
				Interface: iface.Name,
			})
		}
	}

	return results, nil
}

// shouldSkipInterface returns true if the interface should be excluded from
// subnet discovery (loopback, down, or virtual/container interface).
func shouldSkipInterface(iface net.Interface) bool {
	if iface.Flags&net.FlagLoopback != 0 {
		return true
	}
	if iface.Flags&net.FlagUp == 0 {
		return true
	}

	name := strings.ToLower(iface.Name)
	for _, prefix := range virtualPrefixes {
		if strings.HasPrefix(name, prefix) {
			return true
		}
	}

	return false
}

// FindInterfaceForSubnet returns the name of the network interface that has an
// IP address within the given CIDR subnet. This is used to determine which
// interface to masquerade on when forwarding traffic for an advertised route.
//
// For example, if the subnet is "192.168.1.0/24" and the host has
// 192.168.1.233 on "wlan0", this returns "wlan0".
func FindInterfaceForSubnet(cidr string) (string, error) {
	_, subnet, err := net.ParseCIDR(cidr)
	if err != nil {
		return "", fmt.Errorf("parsing CIDR %q: %w", cidr, err)
	}

	ifaces, err := net.Interfaces()
	if err != nil {
		return "", fmt.Errorf("listing interfaces: %w", err)
	}

	for _, iface := range ifaces {
		// Skip loopback and down interfaces.
		if iface.Flags&net.FlagLoopback != 0 || iface.Flags&net.FlagUp == 0 {
			continue
		}
		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, addr := range addrs {
			ip, _, err := net.ParseCIDR(addr.String())
			if err != nil {
				continue
			}
			if subnet.Contains(ip) {
				return iface.Name, nil
			}
		}
	}

	return "", fmt.Errorf("no interface found with address in subnet %s", cidr)
}
