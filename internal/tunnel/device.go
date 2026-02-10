package tunnel

import (
	"fmt"
	"log/slog"

	"golang.zx2c4.com/wireguard/conn"
	"golang.zx2c4.com/wireguard/device"
	"golang.zx2c4.com/wireguard/tun"

	"github.com/kuuji/riftgate/internal/config"
)

// Device wraps a wireguard-go device and its associated TUN interface.
// It manages the lifecycle: configuration, start, peer management, and shutdown.
type Device struct {
	tunDev tun.Device
	wgDev  *device.Device
	log    *slog.Logger
}

// NewDevice creates a new WireGuard device with the given TUN device and conn.Bind.
// It configures the device with the provided private key and brings it up.
//
// The bind parameter is the transport layer for WireGuard packets — in riftgate
// this is a custom conn.Bind that sends packets over WebRTC data channels instead
// of UDP.
func NewDevice(cfg DeviceConfig, tunDev tun.Device, bind conn.Bind, logger *slog.Logger) (*Device, error) {
	if logger == nil {
		logger = slog.Default()
	}

	// Adapt slog to wireguard-go's Logger format.
	wgLogger := &device.Logger{
		Verbosef: func(format string, args ...any) {
			logger.Debug(fmt.Sprintf(format, args...), "component", "wireguard")
		},
		Errorf: func(format string, args ...any) {
			logger.Error(fmt.Sprintf(format, args...), "component", "wireguard")
		},
	}

	wgDev := device.NewDevice(tunDev, bind, wgLogger)

	// Apply the device-level configuration (private key, listen port).
	uapi := BuildUAPIConfig(cfg, nil)
	if err := wgDev.IpcSet(uapi); err != nil {
		wgDev.Close()
		return nil, fmt.Errorf("configuring WireGuard device: %w", err)
	}

	// Bring the device up.
	if err := wgDev.Up(); err != nil {
		wgDev.Close()
		return nil, fmt.Errorf("bringing up WireGuard device: %w", err)
	}

	logger.Info("WireGuard device started")

	return &Device{
		tunDev: tunDev,
		wgDev:  wgDev,
		log:    logger,
	}, nil
}

// AddPeer adds a WireGuard peer with the given configuration.
func (d *Device) AddPeer(peer PeerConfig) error {
	uapi := BuildPeerUAPIConfig(peer)
	if err := d.wgDev.IpcSet(uapi); err != nil {
		return fmt.Errorf("adding WireGuard peer: %w", err)
	}

	d.log.Info("WireGuard peer added", "public_key", peer.PublicKey.String())
	return nil
}

// RemovePeer removes a WireGuard peer by its public key.
func (d *Device) RemovePeer(publicKey config.Key) error {
	uapi := BuildRemovePeerUAPIConfig(publicKey)
	if err := d.wgDev.IpcSet(uapi); err != nil {
		return fmt.Errorf("removing WireGuard peer: %w", err)
	}

	d.log.Info("WireGuard peer removed", "public_key", publicKey.String())
	return nil
}

// Wait returns a channel that is closed when the WireGuard device shuts down.
func (d *Device) Wait() chan struct{} {
	return d.wgDev.Wait()
}

// Close shuts down the WireGuard device and closes the TUN interface.
func (d *Device) Close() {
	d.wgDev.Close()
	// TUN device is closed by wireguard-go's Device.Close, but close
	// it explicitly to be safe. Double-close on the TUN is harmless.
	if err := d.tunDev.Close(); err != nil {
		d.log.Debug("closing TUN device", "error", err)
	}
	d.log.Info("WireGuard device stopped")
}
