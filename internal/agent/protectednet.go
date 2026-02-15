package agent

import (
	"context"
	"fmt"
	"net"
	"syscall"

	transport "github.com/pion/transport/v4"
)

// protectedNet implements pion's transport.Net interface using Go's standard
// net package and protects all created sockets from VPN routing.
//
// On Android, each socket must be protected via VpnService.protect(fd) before
// it can send traffic — otherwise the socket's traffic goes through the VPN
// tunnel, creating a routing loop.
//
// This implementation avoids pion's stdnet.Net which calls anet.Interfaces()
// under the hood — that triggers netlink operations blocked by SELinux on
// Android (untrusted_app cannot bind netlink_route_socket).
type protectedNet struct {
	protector SocketProtector
}

// newProtectedNet creates a transport.Net that protects all sockets via
// the provided SocketProtector callback.
func newProtectedNet(protector SocketProtector) transport.Net {
	return &protectedNet{protector: protector}
}

// Interfaces returns the system's network interfaces.
// On Android, this uses /sys/class/net instead of netlink (which is blocked
// by SELinux). On other platforms, it uses Go's standard net.Interfaces().
func (n *protectedNet) Interfaces() ([]*transport.Interface, error) {
	return platformInterfaces()
}

// InterfaceByIndex returns the interface specified by index.
func (n *protectedNet) InterfaceByIndex(index int) (*transport.Interface, error) {
	ifs, err := n.Interfaces()
	if err != nil {
		return nil, err
	}
	for _, ifc := range ifs {
		if ifc.Index == index {
			return ifc, nil
		}
	}
	return nil, fmt.Errorf("%w: index=%d", transport.ErrInterfaceNotFound, index)
}

// InterfaceByName returns the interface specified by name.
func (n *protectedNet) InterfaceByName(name string) (*transport.Interface, error) {
	ifs, err := n.Interfaces()
	if err != nil {
		return nil, err
	}
	for _, ifc := range ifs {
		if ifc.Name == name {
			return ifc, nil
		}
	}
	return nil, fmt.Errorf("%w: %s", transport.ErrInterfaceNotFound, name)
}

// ListenPacket creates a packet connection and protects the underlying socket.
func (n *protectedNet) ListenPacket(network, address string) (net.PacketConn, error) {
	conn, err := net.ListenPacket(network, address)
	if err != nil {
		return nil, err
	}
	if err := n.protectConn(conn); err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("protecting packet conn: %w", err)
	}
	return conn, nil
}

// ListenUDP creates a UDP connection and protects the underlying socket.
func (n *protectedNet) ListenUDP(network string, locAddr *net.UDPAddr) (transport.UDPConn, error) {
	conn, err := net.ListenUDP(network, locAddr)
	if err != nil {
		return nil, err
	}
	if err := n.protectConn(conn); err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("protecting UDP conn: %w", err)
	}
	return conn, nil
}

// DialUDP dials a UDP connection and protects the underlying socket.
func (n *protectedNet) DialUDP(network string, laddr, raddr *net.UDPAddr) (transport.UDPConn, error) {
	conn, err := net.DialUDP(network, laddr, raddr)
	if err != nil {
		return nil, err
	}
	if err := n.protectConn(conn); err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("protecting UDP dial conn: %w", err)
	}
	return conn, nil
}

// Dial creates a connection and protects the underlying socket.
func (n *protectedNet) Dial(network, address string) (net.Conn, error) {
	conn, err := net.Dial(network, address)
	if err != nil {
		return nil, err
	}
	if err := n.protectConn(conn); err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("protecting dial conn: %w", err)
	}
	return conn, nil
}

// DialTCP creates a TCP connection and protects the underlying socket.
func (n *protectedNet) DialTCP(network string, laddr, raddr *net.TCPAddr) (transport.TCPConn, error) {
	conn, err := net.DialTCP(network, laddr, raddr)
	if err != nil {
		return nil, err
	}
	if err := n.protectConn(conn); err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("protecting TCP dial conn: %w", err)
	}
	return conn, nil
}

// ListenTCP acts like Listen for TCP networks.
func (n *protectedNet) ListenTCP(network string, laddr *net.TCPAddr) (transport.TCPListener, error) {
	l, err := net.ListenTCP(network, laddr)
	if err != nil {
		return nil, err
	}
	return protectedTCPListener{l}, nil
}

// ResolveIPAddr returns an address of IP end point.
func (n *protectedNet) ResolveIPAddr(network, address string) (*net.IPAddr, error) {
	return net.ResolveIPAddr(network, address)
}

// ResolveUDPAddr returns an address of UDP end point.
func (n *protectedNet) ResolveUDPAddr(network, address string) (*net.UDPAddr, error) {
	return net.ResolveUDPAddr(network, address)
}

// ResolveTCPAddr returns an address of TCP end point.
func (n *protectedNet) ResolveTCPAddr(network, address string) (*net.TCPAddr, error) {
	return net.ResolveTCPAddr(network, address)
}

// CreateDialer creates an instance of transport.Dialer.
func (n *protectedNet) CreateDialer(d *net.Dialer) transport.Dialer {
	return protectedDialer{d, n}
}

// CreateListenConfig creates an instance of transport.ListenConfig.
func (n *protectedNet) CreateListenConfig(d *net.ListenConfig) transport.ListenConfig {
	return protectedListenConfig{d, n}
}

// protectedDialer wraps a net.Dialer and protects created connections.
type protectedDialer struct {
	*net.Dialer
	pn *protectedNet
}

func (d protectedDialer) Dial(network, address string) (net.Conn, error) {
	conn, err := d.Dialer.Dial(network, address)
	if err != nil {
		return nil, err
	}
	if err := d.pn.protectConn(conn); err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("protecting dialed conn: %w", err)
	}
	return conn, nil
}

// protectedListenConfig wraps a net.ListenConfig.
type protectedListenConfig struct {
	*net.ListenConfig
	pn *protectedNet
}

func (lc protectedListenConfig) Listen(ctx context.Context, network, address string) (net.Listener, error) {
	return lc.ListenConfig.Listen(ctx, network, address)
}

func (lc protectedListenConfig) ListenPacket(ctx context.Context, network, address string) (net.PacketConn, error) {
	conn, err := lc.ListenConfig.ListenPacket(ctx, network, address)
	if err != nil {
		return nil, err
	}
	if err := lc.pn.protectConn(conn); err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("protecting listen packet conn: %w", err)
	}
	return conn, nil
}

// protectedTCPListener wraps a net.TCPListener.
type protectedTCPListener struct {
	*net.TCPListener
}

func (l protectedTCPListener) AcceptTCP() (transport.TCPConn, error) {
	return l.TCPListener.AcceptTCP()
}

// protectConn extracts the file descriptor from a connection and calls
// the SocketProtector to exempt it from VPN routing.
func (n *protectedNet) protectConn(conn interface{}) error {
	type syscallConner interface {
		SyscallConn() (syscall.RawConn, error)
	}

	sc, ok := conn.(syscallConner)
	if !ok {
		return nil
	}

	rawConn, err := sc.SyscallConn()
	if err != nil {
		return fmt.Errorf("getting raw conn: %w", err)
	}

	var protectErr error
	if controlErr := rawConn.Control(func(fd uintptr) {
		if !n.protector.Protect(int(fd)) {
			protectErr = fmt.Errorf("VpnService.protect(%d) returned false", fd)
		}
	}); controlErr != nil {
		return fmt.Errorf("rawconn control: %w", controlErr)
	}

	return protectErr
}
