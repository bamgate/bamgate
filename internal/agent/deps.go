package agent

import (
	"context"
	"log/slog"

	"golang.zx2c4.com/wireguard/conn"
	"golang.zx2c4.com/wireguard/tun"

	"github.com/kuuji/bamgate/internal/auth"
	"github.com/kuuji/bamgate/internal/config"
	"github.com/kuuji/bamgate/internal/signaling"
	"github.com/kuuji/bamgate/internal/tunnel"
	"github.com/kuuji/bamgate/pkg/protocol"
)

// SignalingClient abstracts the signaling WebSocket connection for testability.
type SignalingClient interface {
	Connect(ctx context.Context) error
	Send(ctx context.Context, msg protocol.Message) error
	Messages() <-chan protocol.Message
	ForceReconnect()
	Close() error
}

// WireGuardDevice abstracts the WireGuard device for testability.
type WireGuardDevice interface {
	AddPeer(peer tunnel.PeerConfig) error
	RemovePeer(publicKey config.Key) error
	Close()
}

// NetworkManager abstracts kernel network operations (TUN configuration,
// routing, forwarding, DNS) for testability. On real systems these require
// CAP_NET_ADMIN; in tests they can be no-ops or recording fakes.
type NetworkManager interface {
	AddAddress(ifName string, cidr string) error
	SetLinkUp(ifName string) error
	AddRoute(ifName string, cidr string) error
	RemoveRoute(ifName string, cidr string) error
	GetForwarding(ifName string) (bool, error)
	SetForwarding(ifName string, enabled bool) error
	FindInterfaceForSubnet(cidr string) (string, error)
	SetDNS(ifName string, servers []string, searchDomains []string) error
	RevertDNS(ifName string) error
}

// NATSetup abstracts nftables/PF NAT management for testability.
type NATSetup interface {
	SetupMasquerade(wgSubnet string, outIface string) error
	TableExists() bool
	Cleanup() error
}

// AuthRefresher abstracts the token refresh HTTP call for testability.
type AuthRefresher interface {
	Refresh(ctx context.Context, serverURL, deviceID, refreshToken string) (*auth.RefreshResponse, error)
}

// ConfigPersister abstracts config file writes for testability.
type ConfigPersister interface {
	SaveSecrets(path string, cfg *config.Config) error
	SaveConfig(path string, cfg *config.Config) error
	MarshalTOML(cfg *config.Config) (string, error)
}

// TUNProvider abstracts TUN device creation for testability.
type TUNProvider interface {
	CreateTUN(name string, mtu int) (tun.Device, error)
	CreateTUNFromFD(fd int) (tun.Device, error)
}

// WireGuardProvider abstracts WireGuard device creation for testability.
type WireGuardProvider interface {
	NewDevice(cfg tunnel.DeviceConfig, tunDev tun.Device, bind conn.Bind, logger *slog.Logger) (WireGuardDevice, error)
}

// Deps holds all external dependencies the Agent needs. This allows tests
// to inject fakes for components that require root privileges or network
// access. Production code uses DefaultDeps().
type Deps struct {
	Network   NetworkManager
	NAT       NATSetup
	Auth      AuthRefresher
	Config    ConfigPersister
	TUN       TUNProvider
	WireGuard WireGuardProvider
	Signaling func(cfg signaling.ClientConfig) SignalingClient
}

// DefaultDeps returns the production implementations that call through
// to the real tunnel, auth, and config packages.
func DefaultDeps() Deps {
	return Deps{
		Network:   &realNetworkManager{},
		NAT:       nil, // created dynamically via tunnel.NewNATManager
		Auth:      &realAuthRefresher{},
		Config:    &realConfigPersister{},
		TUN:       &realTUNProvider{},
		WireGuard: &realWireGuardProvider{},
		Signaling: func(cfg signaling.ClientConfig) SignalingClient {
			return signaling.NewClient(cfg)
		},
	}
}

// --- Production implementations ---

type realNetworkManager struct{}

func (r *realNetworkManager) AddAddress(ifName string, cidr string) error {
	return tunnel.AddAddress(ifName, cidr)
}

func (r *realNetworkManager) SetLinkUp(ifName string) error {
	return tunnel.SetLinkUp(ifName)
}

func (r *realNetworkManager) AddRoute(ifName string, cidr string) error {
	return tunnel.AddRoute(ifName, cidr)
}

func (r *realNetworkManager) RemoveRoute(ifName string, cidr string) error {
	return tunnel.RemoveRoute(ifName, cidr)
}

func (r *realNetworkManager) GetForwarding(ifName string) (bool, error) {
	return tunnel.GetForwarding(ifName)
}

func (r *realNetworkManager) SetForwarding(ifName string, enabled bool) error {
	return tunnel.SetForwarding(ifName, enabled)
}

func (r *realNetworkManager) FindInterfaceForSubnet(cidr string) (string, error) {
	return tunnel.FindInterfaceForSubnet(cidr)
}

func (r *realNetworkManager) SetDNS(ifName string, servers []string, searchDomains []string) error {
	return tunnel.SetDNS(ifName, servers, searchDomains)
}

func (r *realNetworkManager) RevertDNS(ifName string) error {
	return tunnel.RevertDNS(ifName)
}

type realAuthRefresher struct{}

func (r *realAuthRefresher) Refresh(ctx context.Context, serverURL, deviceID, refreshToken string) (*auth.RefreshResponse, error) {
	return auth.Refresh(ctx, serverURL, deviceID, refreshToken)
}

type realConfigPersister struct{}

func (r *realConfigPersister) SaveSecrets(path string, cfg *config.Config) error {
	return config.SaveSecrets(path, cfg)
}

func (r *realConfigPersister) SaveConfig(path string, cfg *config.Config) error {
	return config.SaveConfig(path, cfg)
}

func (r *realConfigPersister) MarshalTOML(cfg *config.Config) (string, error) {
	return config.MarshalTOML(cfg)
}

type realTUNProvider struct{}

func (r *realTUNProvider) CreateTUN(name string, mtu int) (tun.Device, error) {
	return tunnel.CreateTUN(name, mtu)
}

func (r *realTUNProvider) CreateTUNFromFD(fd int) (tun.Device, error) {
	return tunnel.CreateTUNFromFD(fd)
}

type realWireGuardProvider struct{}

func (r *realWireGuardProvider) NewDevice(cfg tunnel.DeviceConfig, tunDev tun.Device, bind conn.Bind, logger *slog.Logger) (WireGuardDevice, error) {
	return tunnel.NewDevice(cfg, tunDev, bind, logger)
}
