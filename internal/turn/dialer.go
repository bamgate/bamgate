package turn

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"

	"github.com/coder/websocket"
)

// WSProxyDialer implements proxy.Dialer by dialing a WebSocket to the TURN
// server and returning a net.Conn. pion/ice's relay candidate gathering uses
// this interface to establish TCP connections to TURN servers — we intercept
// those connections and route them over WebSocket to our Cloudflare Worker.
//
// The net.Conn returned wraps the WebSocket with proper *net.TCPAddr values
// for LocalAddr() and RemoteAddr(), which pion/ice requires (forced type assertion).
type WSProxyDialer struct {
	// TURNEndpoint is the WebSocket URL for the TURN relay (e.g. "wss://worker.workers.dev/turn").
	TURNEndpoint string

	// AuthToken is the bearer token for authenticating the WebSocket upgrade.
	AuthToken string
}

// Dial implements proxy.Dialer. The network and addr parameters come from pion/ice's
// relay candidate gathering — they describe the TURN server address. We ignore them
// and connect to our TURNEndpoint via WebSocket instead.
func (d *WSProxyDialer) Dial(network, addr string) (net.Conn, error) {
	ctx := context.Background()

	wsConn, _, err := websocket.Dial(ctx, d.TURNEndpoint, &websocket.DialOptions{
		HTTPHeader: http.Header{
			"Authorization": []string{"Bearer " + d.AuthToken},
		},
	})
	if err != nil {
		return nil, fmt.Errorf("dialing TURN WebSocket %s: %w", d.TURNEndpoint, err)
	}

	// Convert WebSocket to net.Conn. Binary messages map 1:1 to STUN/TURN frames.
	netConn := websocket.NetConn(ctx, wsConn, websocket.MessageBinary)

	// Wrap to provide *net.TCPAddr for LocalAddr()/RemoteAddr().
	// pion/ice does a forced type assertion: conn.LocalAddr().(*net.TCPAddr)
	// websocket.NetConn returns a mock addr for dialed connections, which panics.
	return &turnConn{
		Conn:       netConn,
		localAddr:  &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 0},
		remoteAddr: parseTCPAddr(addr),
	}, nil
}

// TURNServerURL derives the TURN server URL from the signaling server URL.
// It converts a WebSocket signaling URL like "wss://worker.workers.dev/connect"
// to a TURN URL like "turn:worker.workers.dev:443?transport=tcp".
//
// We use "turn:" (not "turns:") because the WebSocket connection already provides TLS.
// With a proxy dialer set, pion/ice does NOT add TLS on top — the proxy dialer
// is responsible for the transport. See pion/ice gather.go for details.
func TURNServerURL(serverURL string) (string, error) {
	u, err := url.Parse(serverURL)
	if err != nil {
		return "", fmt.Errorf("parsing server URL: %w", err)
	}

	host := u.Hostname()
	port := u.Port()
	if port == "" {
		switch u.Scheme {
		case "wss", "https":
			port = "443"
		case "ws", "http":
			port = "80"
		default:
			port = "443"
		}
	}

	return fmt.Sprintf("turn:%s:%s?transport=tcp", host, port), nil
}

// TURNWebSocketURL derives the TURN WebSocket endpoint URL from the signaling
// server URL. It replaces the path with "/turn".
func TURNWebSocketURL(serverURL string) (string, error) {
	u, err := url.Parse(serverURL)
	if err != nil {
		return "", fmt.Errorf("parsing server URL: %w", err)
	}

	// Ensure WebSocket scheme.
	switch u.Scheme {
	case "wss", "ws":
		// Already WebSocket.
	case "https":
		u.Scheme = "wss"
	case "http":
		u.Scheme = "ws"
	default:
		u.Scheme = "wss"
	}

	u.Path = "/turn"
	u.RawQuery = ""
	return u.String(), nil
}

// turnConn wraps a net.Conn and overrides LocalAddr/RemoteAddr to return
// *net.TCPAddr, which pion/ice requires (forced type assertion in gather.go).
type turnConn struct {
	net.Conn
	localAddr  *net.TCPAddr
	remoteAddr *net.TCPAddr
}

func (c *turnConn) LocalAddr() net.Addr {
	return c.localAddr
}

func (c *turnConn) RemoteAddr() net.Addr {
	return c.remoteAddr
}

// parseTCPAddr parses "host:port" into a *net.TCPAddr. Falls back to a
// reasonable default if parsing fails.
func parseTCPAddr(addr string) *net.TCPAddr {
	host, portStr, err := net.SplitHostPort(addr)
	if err != nil {
		// addr might be just a host without port.
		return &net.TCPAddr{IP: net.ParseIP(strings.TrimSpace(addr)), Port: 443}
	}

	ip := net.ParseIP(host)
	if ip == nil {
		// Hostname — resolve it. Fall back to loopback if resolution fails.
		ips, err := net.LookupIP(host)
		if err != nil || len(ips) == 0 {
			ip = net.IPv4(127, 0, 0, 1)
		} else {
			ip = ips[0]
		}
	}

	port := 443
	if n, err := net.LookupPort("tcp", portStr); err == nil {
		port = n
	}

	return &net.TCPAddr{IP: ip, Port: port}
}
