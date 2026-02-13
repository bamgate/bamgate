//go:build linux

package tunnel

// DefaultTUNName is the default name for the bamgate TUN interface on Linux.
// Linux allows arbitrary interface names.
const DefaultTUNName = "bamgate0"
