//go:build darwin

package tunnel

// DefaultTUNName is the default name for the bamgate TUN interface on macOS.
// macOS requires utun* names. Passing "utun" (without a number) tells the
// kernel to auto-assign the next available utun interface (e.g., utun3).
const DefaultTUNName = "utun"
