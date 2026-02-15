//go:build android

package tunnel

import "log/slog"

// NATManager is a no-op on Android. The Android OS handles VPN routing and
// does not require explicit NAT/masquerade rules.
type NATManager struct{}

// NewNATManager returns a no-op NATManager for Android.
func NewNATManager(_ *slog.Logger) *NATManager {
	return &NATManager{}
}

// SetupMasquerade is a no-op on Android.
func (n *NATManager) SetupMasquerade(wgSubnet, outIface string) error {
	return nil
}

// TableExists always returns true on Android — no NAT table to monitor.
func (n *NATManager) TableExists() bool {
	return true
}

// Cleanup is a no-op on Android.
func (n *NATManager) Cleanup() error {
	return nil
}
