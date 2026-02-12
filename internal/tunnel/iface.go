package tunnel

import (
	"fmt"
	"net"
)

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
