//go:build android

package tunnel

// On Android, the VpnService.Builder configures addresses, routes, DNS, and
// MTU before creating the TUN file descriptor via establish(). The Go side
// receives a ready-to-use TUN FD and does not need to configure the interface.
//
// All network configuration functions are no-ops on Android.

// AddAddress is a no-op on Android — VpnService.Builder.addAddress() handles this.
func AddAddress(ifName string, cidr string) error { return nil }

// SetLinkUp is a no-op on Android — the interface is already up when the FD is received.
func SetLinkUp(ifName string) error { return nil }

// AddRoute is a no-op on Android — VpnService.Builder.addRoute() handles this.
func AddRoute(ifName string, cidr string) error { return nil }

// RemoveRoute is a no-op on Android — routes are removed when the VPN is stopped.
func RemoveRoute(ifName string, cidr string) error { return nil }

// GetForwarding always returns false on Android — IP forwarding is not applicable.
func GetForwarding(ifName string) (bool, error) { return false, nil }

// SetForwarding is a no-op on Android — IP forwarding is managed by the OS.
func SetForwarding(ifName string, enabled bool) error { return nil }

// SetDNS is a no-op on Android — DNS is configured via VpnService.Builder.addDnsServer().
func SetDNS(ifName string, servers []string, searchDomains []string) error { return nil }

// RevertDNS is a no-op on Android — DNS is removed when the VPN stops.
func RevertDNS(ifName string) error { return nil }
