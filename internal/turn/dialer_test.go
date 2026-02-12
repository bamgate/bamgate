package turn

import (
	"net"
	"testing"
)

func TestTURNServerURL(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		serverURL string
		want      string
	}{
		{
			name:      "wss with explicit path",
			serverURL: "wss://riftgate.example.workers.dev/connect?room=default",
			want:      "turn:riftgate.example.workers.dev:443?transport=tcp",
		},
		{
			name:      "wss bare",
			serverURL: "wss://riftgate.example.workers.dev",
			want:      "turn:riftgate.example.workers.dev:443?transport=tcp",
		},
		{
			name:      "https",
			serverURL: "https://riftgate.example.workers.dev/connect",
			want:      "turn:riftgate.example.workers.dev:443?transport=tcp",
		},
		{
			name:      "ws localhost",
			serverURL: "ws://localhost:8787/connect",
			want:      "turn:localhost:8787?transport=tcp",
		},
		{
			name:      "custom port",
			serverURL: "wss://example.com:8443/connect",
			want:      "turn:example.com:8443?transport=tcp",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := TURNServerURL(tt.serverURL)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestTURNWebSocketURL(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		serverURL string
		want      string
	}{
		{
			name:      "wss with path",
			serverURL: "wss://riftgate.example.workers.dev/connect?room=default",
			want:      "wss://riftgate.example.workers.dev/turn",
		},
		{
			name:      "https to wss",
			serverURL: "https://riftgate.example.workers.dev/connect",
			want:      "wss://riftgate.example.workers.dev/turn",
		},
		{
			name:      "http to ws",
			serverURL: "http://localhost:8787/connect",
			want:      "ws://localhost:8787/turn",
		},
		{
			name:      "ws stays ws",
			serverURL: "ws://localhost:8787/connect",
			want:      "ws://localhost:8787/turn",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := TURNWebSocketURL(tt.serverURL)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestParseTCPAddr(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		addr     string
		wantIP   net.IP
		wantPort int
	}{
		{
			name:     "host:port",
			addr:     "127.0.0.1:3478",
			wantIP:   net.IPv4(127, 0, 0, 1),
			wantPort: 3478,
		},
		{
			name:     "ip only",
			addr:     "10.0.0.1",
			wantIP:   net.ParseIP("10.0.0.1"),
			wantPort: 443,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := parseTCPAddr(tt.addr)
			if _, ok := interface{}(got).(*net.TCPAddr); !ok {
				t.Fatal("not a *net.TCPAddr")
			}
			if !got.IP.Equal(tt.wantIP) {
				t.Errorf("IP: got %v, want %v", got.IP, tt.wantIP)
			}
			if got.Port != tt.wantPort {
				t.Errorf("Port: got %d, want %d", got.Port, tt.wantPort)
			}
		})
	}
}

func TestTurnConn_AddressTypes(t *testing.T) {
	t.Parallel()

	// Verify that turnConn's LocalAddr/RemoteAddr return *net.TCPAddr
	// (required by pion/ice's forced type assertion).
	c := &turnConn{
		localAddr:  &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 1234},
		remoteAddr: &net.TCPAddr{IP: net.IPv4(10, 0, 0, 1), Port: 443},
	}

	local, ok := c.LocalAddr().(*net.TCPAddr)
	if !ok {
		t.Fatal("LocalAddr() is not *net.TCPAddr")
	}
	if local.Port != 1234 {
		t.Errorf("local port: got %d, want 1234", local.Port)
	}

	remote, ok := c.RemoteAddr().(*net.TCPAddr)
	if !ok {
		t.Fatal("RemoteAddr() is not *net.TCPAddr")
	}
	if remote.Port != 443 {
		t.Errorf("remote port: got %d, want 443", remote.Port)
	}
}
