package tunnel

import (
	"fmt"

	"golang.zx2c4.com/wireguard/tun"
)

const (
	// DefaultTUNName is the default name for the riftgate TUN interface.
	DefaultTUNName = "riftgate0"

	// DefaultMTU is the default MTU for the WireGuard TUN interface.
	// WireGuard standard is 1420, leaving room for encapsulation overhead.
	DefaultMTU = 1420
)

// CreateTUN creates a kernel TUN device with the given name and MTU.
// Requires CAP_NET_ADMIN (typically root).
func CreateTUN(name string, mtu int) (tun.Device, error) {
	if name == "" {
		name = DefaultTUNName
	}
	if mtu <= 0 {
		mtu = DefaultMTU
	}

	dev, err := tun.CreateTUN(name, mtu)
	if err != nil {
		return nil, fmt.Errorf("creating TUN device %q: %w", name, err)
	}

	return dev, nil
}
