//go:build linux

package tunnel

// DefaultTUNName is the default name for the riftgate TUN interface on Linux.
// Linux allows arbitrary interface names.
const DefaultTUNName = "riftgate0"
