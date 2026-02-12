package control

import (
	"path/filepath"
	"testing"
	"time"
)

func TestServer_StartStopFetchStatus(t *testing.T) {
	t.Parallel()

	socketPath := filepath.Join(t.TempDir(), "test.sock")

	provider := func() Status {
		return Status{
			Device:        "test-device",
			Address:       "10.0.0.1/24",
			ServerURL:     "https://example.com/connect",
			UptimeSeconds: 42.5,
			Peers: []PeerStatus{
				{
					ID:             "laptop",
					Address:        "10.0.0.2/24",
					State:          "connected",
					ICEType:        "host",
					Routes:         []string{"192.168.1.0/24"},
					ConnectedSince: time.Date(2026, 2, 12, 10, 0, 0, 0, time.UTC),
				},
			},
		}
	}

	srv := NewServer(socketPath, provider, nil)

	if err := srv.Start(); err != nil {
		t.Fatalf("Start() error: %v", err)
	}
	defer srv.Stop()

	// Fetch status.
	status, err := FetchStatus(socketPath)
	if err != nil {
		t.Fatalf("FetchStatus() error: %v", err)
	}

	if status.Device != "test-device" {
		t.Errorf("Device = %q, want %q", status.Device, "test-device")
	}
	if status.Address != "10.0.0.1/24" {
		t.Errorf("Address = %q, want %q", status.Address, "10.0.0.1/24")
	}
	if len(status.Peers) != 1 {
		t.Fatalf("len(Peers) = %d, want 1", len(status.Peers))
	}
	if status.Peers[0].ID != "laptop" {
		t.Errorf("Peers[0].ID = %q, want %q", status.Peers[0].ID, "laptop")
	}
	if status.Peers[0].ICEType != "host" {
		t.Errorf("Peers[0].ICEType = %q, want %q", status.Peers[0].ICEType, "host")
	}
	if len(status.Peers[0].Routes) != 1 || status.Peers[0].Routes[0] != "192.168.1.0/24" {
		t.Errorf("Peers[0].Routes = %v, want [192.168.1.0/24]", status.Peers[0].Routes)
	}
}

func TestFetchStatus_NoServer(t *testing.T) {
	t.Parallel()

	socketPath := filepath.Join(t.TempDir(), "nonexistent.sock")

	_, err := FetchStatus(socketPath)
	if err == nil {
		t.Fatal("expected error when server is not running, got nil")
	}
}
