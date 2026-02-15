package tunnel

import (
	"fmt"

	"golang.zx2c4.com/wireguard/tun"
)

const (
	// DefaultMTU is the default MTU for the WireGuard TUN interface.
	// WireGuard standard is 1420, leaving room for encapsulation overhead.
	DefaultMTU = 1420
)

// DefaultTUNName is defined per-platform:
//   - Linux: "bamgate0" (in tun_linux.go)
//   - macOS: "utun" (in tun_darwin.go) — kernel auto-assigns next available utun
//   - Android: "tun0" (in tun_android.go) — not used, VpnService names the interface

// CreateTUN creates a kernel TUN device with the given name and MTU.
// On Linux, requires CAP_NET_ADMIN. On macOS, requires root privileges.
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

// CreateTUNFromFD is defined per-platform:
//   - Android: tun_android.go (uses CreateUnmonitoredTUNFromFD)
//   - Linux: tun_linux.go (uses CreateTUNFromFile)
