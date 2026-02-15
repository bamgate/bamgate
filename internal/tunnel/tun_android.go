//go:build android

package tunnel

import (
	"fmt"

	"golang.zx2c4.com/wireguard/tun"
)

// DefaultTUNName is not used on Android — VpnService names the interface.
// Defined here to satisfy the per-platform constant requirement.
const DefaultTUNName = "tun0"

// CreateTUNFromFD wraps an existing TUN file descriptor into a wireguard-go
// tun.Device. This is used on Android where the VpnService creates the TUN
// interface and passes the file descriptor to the Go layer.
//
// Uses CreateUnmonitoredTUNFromFD which skips netlink socket creation and
// MTU configuration — both of which are blocked by SELinux on Android.
// The Android VpnService already configured the interface (MTU, addresses,
// routes) before handing us the fd.
//
// The returned Device takes ownership of the file descriptor.
func CreateTUNFromFD(fd int) (tun.Device, error) {
	if fd < 0 {
		return nil, fmt.Errorf("invalid TUN file descriptor: %d", fd)
	}

	dev, name, err := tun.CreateUnmonitoredTUNFromFD(fd)
	if err != nil {
		return nil, fmt.Errorf("creating TUN device from fd %d: %w", fd, err)
	}

	_ = name // Android VpnService names the interface; we don't need it.
	return dev, nil
}
