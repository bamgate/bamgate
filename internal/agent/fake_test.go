package agent

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"sync"

	"golang.zx2c4.com/wireguard/conn"
	"golang.zx2c4.com/wireguard/tun"

	"github.com/kuuji/bamgate/internal/auth"
	"github.com/kuuji/bamgate/internal/config"
	"github.com/kuuji/bamgate/internal/tunnel"
)

// --- Fake TUN device ---

// fakeTUNDevice implements tun.Device with in-memory buffers. Reads block
// until data is written or the device is closed.
type fakeTUNDevice struct {
	name    string
	readCh  chan []byte
	closeCh chan struct{}
	once    sync.Once
	events  chan tun.Event
}

func newFakeTUNDevice(name string) *fakeTUNDevice {
	events := make(chan tun.Event, 1)
	events <- tun.EventUp
	return &fakeTUNDevice{
		name:    name,
		readCh:  make(chan []byte, 64),
		closeCh: make(chan struct{}),
		events:  events,
	}
}

func (f *fakeTUNDevice) File() *os.File           { return nil }
func (f *fakeTUNDevice) Name() (string, error)    { return f.name, nil }
func (f *fakeTUNDevice) MTU() (int, error)        { return 1420, nil }
func (f *fakeTUNDevice) Events() <-chan tun.Event { return f.events }
func (f *fakeTUNDevice) BatchSize() int           { return 1 }

func (f *fakeTUNDevice) Read(bufs [][]byte, sizes []int, offset int) (int, error) {
	select {
	case data := <-f.readCh:
		n := copy(bufs[0][offset:], data)
		sizes[0] = n
		return 1, nil
	case <-f.closeCh:
		return 0, os.ErrClosed
	}
}

func (f *fakeTUNDevice) Write(bufs [][]byte, offset int) (int, error) {
	return len(bufs), nil
}

func (f *fakeTUNDevice) Close() error {
	f.once.Do(func() { close(f.closeCh) })
	return nil
}

// --- Fake WireGuard device ---

// fakeWireGuardDevice records AddPeer/RemovePeer calls without actually
// creating a WireGuard device. Thread-safe.
type fakeWireGuardDevice struct {
	mu     sync.Mutex
	peers  map[string]tunnel.PeerConfig // publicKey.String() -> config
	closed bool
}

func newFakeWireGuardDevice() *fakeWireGuardDevice {
	return &fakeWireGuardDevice{
		peers: make(map[string]tunnel.PeerConfig),
	}
}

func (f *fakeWireGuardDevice) AddPeer(peer tunnel.PeerConfig) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.peers[peer.PublicKey.String()] = peer
	return nil
}

func (f *fakeWireGuardDevice) RemovePeer(publicKey config.Key) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.peers, publicKey.String())
	return nil
}

func (f *fakeWireGuardDevice) Close() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.closed = true
}

func (f *fakeWireGuardDevice) hasPeer(publicKey string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	_, ok := f.peers[publicKey]
	return ok
}

func (f *fakeWireGuardDevice) peerCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.peers)
}

// --- Fake Network Manager ---

// fakeNetworkManager records all network operations without touching the kernel.
type fakeNetworkManager struct {
	mu         sync.Mutex
	addresses  map[string]string   // ifName -> cidr
	linksUp    map[string]bool     // ifName -> true
	routes     map[string][]string // ifName -> list of cidrs
	forwarding map[string]bool     // ifName -> enabled
	dns        map[string][]string // ifName -> servers
	dnsSearch  map[string][]string // ifName -> search domains
	subnets    map[string]string   // cidr -> ifName (for FindInterfaceForSubnet)
}

func newFakeNetworkManager() *fakeNetworkManager {
	return &fakeNetworkManager{
		addresses:  make(map[string]string),
		linksUp:    make(map[string]bool),
		routes:     make(map[string][]string),
		forwarding: make(map[string]bool),
		dns:        make(map[string][]string),
		dnsSearch:  make(map[string][]string),
		subnets:    make(map[string]string),
	}
}

func (f *fakeNetworkManager) AddAddress(ifName string, cidr string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.addresses[ifName] = cidr
	return nil
}

func (f *fakeNetworkManager) SetLinkUp(ifName string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.linksUp[ifName] = true
	return nil
}

func (f *fakeNetworkManager) AddRoute(ifName string, cidr string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.routes[ifName] = append(f.routes[ifName], cidr)
	return nil
}

func (f *fakeNetworkManager) RemoveRoute(ifName string, cidr string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	routes := f.routes[ifName]
	for i, r := range routes {
		if r == cidr {
			f.routes[ifName] = append(routes[:i], routes[i+1:]...)
			break
		}
	}
	return nil
}

func (f *fakeNetworkManager) GetForwarding(ifName string) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.forwarding[ifName], nil
}

func (f *fakeNetworkManager) SetForwarding(ifName string, enabled bool) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.forwarding[ifName] = enabled
	return nil
}

func (f *fakeNetworkManager) FindInterfaceForSubnet(cidr string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if ifName, ok := f.subnets[cidr]; ok {
		return ifName, nil
	}
	return "", fmt.Errorf("no interface for subnet %s", cidr)
}

func (f *fakeNetworkManager) SetDNS(ifName string, servers []string, searchDomains []string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.dns[ifName] = servers
	f.dnsSearch[ifName] = searchDomains
	return nil
}

func (f *fakeNetworkManager) RevertDNS(ifName string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.dns, ifName)
	delete(f.dnsSearch, ifName)
	return nil
}

// --- Fake NAT setup ---

// fakeNATSetup records masquerade calls without touching nftables.
type fakeNATSetup struct {
	mu          sync.Mutex
	rules       []masqueradeEntry
	tableExists bool
}

func newFakeNATSetup() *fakeNATSetup {
	return &fakeNATSetup{tableExists: true}
}

func (f *fakeNATSetup) SetupMasquerade(wgSubnet string, outIface string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.rules = append(f.rules, masqueradeEntry{wgSubnet: wgSubnet, outIface: outIface})
	return nil
}

func (f *fakeNATSetup) TableExists() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.tableExists
}

func (f *fakeNATSetup) Cleanup() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.rules = nil
	return nil
}

// --- Fake Auth Refresher ---

// fakeAuthRefresher returns preconfigured responses or errors.
type fakeAuthRefresher struct {
	mu       sync.Mutex
	response *auth.RefreshResponse
	err      error
	calls    int
}

func newFakeAuthRefresher(accessToken, refreshToken string) *fakeAuthRefresher {
	return &fakeAuthRefresher{
		response: &auth.RefreshResponse{
			AccessToken:  accessToken,
			RefreshToken: refreshToken,
			ExpiresIn:    3600,
		},
	}
}

func (f *fakeAuthRefresher) Refresh(_ context.Context, _, _, _ string) (*auth.RefreshResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	if f.err != nil {
		return nil, f.err
	}
	return f.response, nil
}

func (f *fakeAuthRefresher) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

// --- Fake Config Persister ---

// fakeConfigPersister records calls without writing to disk.
type fakeConfigPersister struct {
	mu              sync.Mutex
	savedSecrets    int
	savedConfigs    int
	lastSavedConfig *config.Config
}

func (f *fakeConfigPersister) SaveSecrets(_ string, cfg *config.Config) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.savedSecrets++
	f.lastSavedConfig = cfg
	return nil
}

func (f *fakeConfigPersister) SaveConfig(_ string, cfg *config.Config) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.savedConfigs++
	f.lastSavedConfig = cfg
	return nil
}

func (f *fakeConfigPersister) MarshalTOML(cfg *config.Config) (string, error) {
	return config.MarshalTOML(cfg)
}

// --- Fake TUN Provider ---

// fakeTUNProvider returns a fake TUN device instead of creating a real one.
type fakeTUNProvider struct {
	device *fakeTUNDevice
}

func (f *fakeTUNProvider) CreateTUN(name string, _ int) (tun.Device, error) {
	f.device = newFakeTUNDevice(name)
	return f.device, nil
}

func (f *fakeTUNProvider) CreateTUNFromFD(_ int) (tun.Device, error) {
	f.device = newFakeTUNDevice("tun-android")
	return f.device, nil
}

// --- Fake WireGuard Provider ---

// fakeWireGuardProvider returns a fake WireGuard device. Access to the device
// field is protected by a mutex because NewDevice is called from the agent
// goroutine while tests read it from the test goroutine.
type fakeWireGuardProvider struct {
	mu     sync.Mutex
	device *fakeWireGuardDevice
}

func (f *fakeWireGuardProvider) NewDevice(_ tunnel.DeviceConfig, _ tun.Device, _ conn.Bind, _ *slog.Logger) (WireGuardDevice, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.device = newFakeWireGuardDevice()
	return f.device, nil
}

func (f *fakeWireGuardProvider) getDevice() *fakeWireGuardDevice {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.device
}

// --- Test helpers ---

// testDeps returns a Deps with all fakes pre-wired. The returned struct
// contains references to each fake so tests can inspect recorded calls.
type testFakes struct {
	Network   *fakeNetworkManager
	NAT       *fakeNATSetup
	Auth      *fakeAuthRefresher
	Config    *fakeConfigPersister
	TUN       *fakeTUNProvider
	WireGuard *fakeWireGuardProvider
}

func newTestDeps() (Deps, *testFakes) {
	fakes := &testFakes{
		Network:   newFakeNetworkManager(),
		NAT:       newFakeNATSetup(),
		Auth:      newFakeAuthRefresher("test-jwt", "test-refresh"),
		Config:    &fakeConfigPersister{},
		TUN:       &fakeTUNProvider{},
		WireGuard: &fakeWireGuardProvider{},
	}
	return Deps{
		Network:   fakes.Network,
		NAT:       fakes.NAT,
		Auth:      fakes.Auth,
		Config:    fakes.Config,
		TUN:       fakes.TUN,
		WireGuard: fakes.WireGuard,
		// Signaling is set per-test since it needs the Hub URL.
		Signaling: nil,
	}, fakes
}
