//go:build darwin

package tunnel

import (
	"fmt"
	"os"

	"golang.zx2c4.com/wireguard/tun"
)

// DefaultTUNName is the default name for the bamgate TUN interface on macOS.
// macOS requires utun* names. Passing "utun" (without a number) tells the
// kernel to auto-assign the next available utun interface (e.g., utun3).
const DefaultTUNName = "utun"

// CreateTUNFromFD wraps an existing TUN file descriptor into a wireguard-go
// tun.Device. On macOS, this uses CreateTUNFromFile.
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
