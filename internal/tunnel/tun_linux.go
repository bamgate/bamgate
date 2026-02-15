//go:build linux && !android

package tunnel

import (
	"fmt"
	"os"

	"golang.zx2c4.com/wireguard/tun"
)

// DefaultTUNName is the default name for the bamgate TUN interface on Linux.
// Linux allows arbitrary interface names.
const DefaultTUNName = "bamgate0"

// CreateTUNFromFD wraps an existing TUN file descriptor into a wireguard-go
// tun.Device. On Linux (non-Android), this uses the full CreateTUNFromFile
// which sets up netlink monitoring and MTU configuration.
func CreateTUNFromFD(fd int) (tun.Device, error) {
	if fd < 0 {
		return nil, fmt.Errorf("invalid TUN file descriptor: %d", fd)
	}

	file := os.NewFile(uintptr(fd), "/dev/tun")
	if file == nil {
		return nil, fmt.Errorf("failed to create file from TUN fd %d", fd)
	}

	dev, err := tun.CreateTUNFromFile(file, DefaultMTU)
	if err != nil {
		_ = file.Close()
		return nil, fmt.Errorf("creating TUN device from fd %d: %w", fd, err)
	}

	return dev, nil
}
