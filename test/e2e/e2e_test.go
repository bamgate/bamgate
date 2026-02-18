//go:build e2e

// Package e2e runs end-to-end tests for bamgate using Docker containers.
//
// Each test spins up a signaling hub + multiple bamgate peers, each in its
// own Docker container with a real TUN device and WireGuard tunnel. Traffic
// (ICMP ping) is verified end-to-end through the encrypted tunnel.
//
// Prerequisites:
//   - Docker with the compose plugin
//   - /dev/net/tun available on the host
//   - Run with: go test -tags e2e -v -timeout 120s ./test/e2e/
package e2e

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kuuji/bamgate/internal/config"
)

// peer describes a bamgate peer in the test topology.
type peer struct {
	name    string // docker compose service name and bamgate device name
	address string // WireGuard tunnel address (CIDR)
	ip      string // just the IP part (for ping targets)
}

var peers = []peer{
	{name: "alpha", address: "10.0.0.1/24", ip: "10.0.0.1"},
	{name: "bravo", address: "10.0.0.2/24", ip: "10.0.0.2"},
	{name: "charlie", address: "10.0.0.3/24", ip: "10.0.0.3"},
}

// composeFile is the path to the docker-compose.yml relative to the project root.
const composeFile = "test/e2e/docker-compose.yml"

// projectRoot returns the absolute path to the bamgate project root.
// It walks up from the test file's directory until it finds go.mod.
func projectRoot(t *testing.T) string {
	t.Helper()
	// We're in test/e2e/, so project root is ../..
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	root := filepath.Join(dir, "..", "..")
	root, err = filepath.Abs(root)
	if err != nil {
		t.Fatalf("abs: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "go.mod")); err != nil {
		t.Fatalf("project root not found (no go.mod at %s)", root)
	}
	return root
}

// generateConfigs creates TOML config files for each peer in a temporary
// directory structure: configs/{alpha,bravo,charlie}/config.toml
func generateConfigs(t *testing.T, configDir string) {
	t.Helper()
	for _, p := range peers {
		privKey, err := config.GeneratePrivateKey()
		if err != nil {
			t.Fatalf("generating key for %s: %v", p.name, err)
		}

		cfg := &config.Config{
			Device: config.DeviceConfig{
				Name:       p.name,
				PrivateKey: privKey,
				Address:    p.address,
			},
			Network: config.NetworkConfig{
				ServerURL: "ws://hub:8080",
			},
		}

		tomlStr, err := config.MarshalTOML(cfg)
		if err != nil {
			t.Fatalf("marshaling config for %s: %v", p.name, err)
		}

		peerDir := filepath.Join(configDir, p.name)
		if err := os.MkdirAll(peerDir, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", peerDir, err)
		}

		cfgPath := filepath.Join(peerDir, "config.toml")
		if err := os.WriteFile(cfgPath, []byte(tomlStr), 0o644); err != nil {
			t.Fatalf("writing config for %s: %v", p.name, err)
		}

		t.Logf("generated config for %s at %s", p.name, cfgPath)
	}
}

// compose runs docker compose with the given arguments from the project root.
func compose(t *testing.T, root string, args ...string) (string, error) {
	t.Helper()
	fullArgs := append([]string{"compose", "-f", composeFile}, args...)
	cmd := exec.Command("docker", fullArgs...)
	cmd.Dir = root
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	if err != nil {
		return stdout.String(), fmt.Errorf("%s\nstderr: %s", err, stderr.String())
	}
	return stdout.String(), nil
}

// dockerExec runs a command inside a running compose service container.
func dockerExec(t *testing.T, root, service string, args ...string) (string, error) {
	t.Helper()
	fullArgs := append([]string{"compose", "-f", composeFile, "exec", "-T", service}, args...)
	cmd := exec.Command("docker", fullArgs...)
	cmd.Dir = root
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	if err != nil {
		return stdout.String(), fmt.Errorf("%s\nstdout: %s\nstderr: %s", err, stdout.String(), stderr.String())
	}
	return stdout.String(), nil
}

// composeLogs fetches logs from a compose service.
func composeLogs(t *testing.T, root, service string) string {
	t.Helper()
	out, _ := compose(t, root, "logs", "--no-color", service)
	return out
}

// waitForTunnel polls a peer container until the bamgate0 TUN interface exists.
func waitForTunnel(t *testing.T, root, service string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		out, err := dockerExec(t, root, service, "ip", "link", "show", "bamgate0")
		if err == nil && strings.Contains(out, "bamgate0") {
			t.Logf("%s: TUN interface up", service)
			return
		}
		time.Sleep(500 * time.Millisecond)
	}
	// Dump logs for debugging.
	t.Logf("=== %s logs ===\n%s", service, composeLogs(t, root, service))
	t.Fatalf("timed out waiting for %s TUN interface", service)
}

// waitForPing polls until a ping from src to dstIP succeeds.
func waitForPing(t *testing.T, root, srcService, dstIP string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		_, err := dockerExec(t, root, srcService, "ping", "-c", "1", "-W", "1", dstIP)
		if err == nil {
			return
		}
		time.Sleep(500 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for ping from %s to %s", srcService, dstIP)
}

// ping runs a single ping and returns an error if it fails.
func ping(t *testing.T, root, srcService, dstIP string) error {
	t.Helper()
	_, err := dockerExec(t, root, srcService, "ping", "-c", "3", "-W", "2", dstIP)
	return err
}

// --- Tests ---

// TestE2E_ThreePeerMesh verifies that three bamgate peers can establish a
// full mesh of WireGuard tunnels over WebRTC and ping each other through
// the encrypted tunnel.
func TestE2E_ThreePeerMesh(t *testing.T) {
	root := projectRoot(t)
	configDir := filepath.Join(root, "test", "e2e", "configs")

	// Clean up any previous configs.
	os.RemoveAll(configDir)

	// Generate fresh configs with new key pairs.
	generateConfigs(t, configDir)

	// Build and start the containers.
	t.Log("building and starting containers...")
	if _, err := compose(t, root, "up", "-d", "--build"); err != nil {
		t.Fatalf("compose up: %v", err)
	}

	// Always tear down at the end.
	t.Cleanup(func() {
		t.Log("tearing down containers...")
		if _, err := compose(t, root, "down", "--volumes", "--remove-orphans", "--timeout", "10"); err != nil {
			t.Logf("compose down error: %v", err)
		}
		os.RemoveAll(configDir)
	})

	// Wait for all peers to create their TUN interfaces.
	t.Log("waiting for TUN interfaces...")
	for _, p := range peers {
		waitForTunnel(t, root, p.name, 30*time.Second)
	}

	// Wait for the mesh to establish by polling ping from alpha to others.
	// This gives WebRTC ICE time to negotiate.
	t.Log("waiting for mesh connectivity...")
	for _, p := range peers {
		for _, other := range peers {
			if p.name == other.name {
				continue
			}
			t.Logf("waiting for %s -> %s (%s)...", p.name, other.name, other.ip)
			waitForPing(t, root, p.name, other.ip, 30*time.Second)
		}
	}

	// Now run the actual ping assertions — 3 pings each direction.
	t.Log("running ping assertions...")
	for _, src := range peers {
		for _, dst := range peers {
			if src.name == dst.name {
				continue
			}
			t.Run(fmt.Sprintf("%s->%s", src.name, dst.name), func(t *testing.T) {
				if err := ping(t, root, src.name, dst.ip); err != nil {
					t.Logf("=== %s logs ===\n%s", src.name, composeLogs(t, root, src.name))
					t.Logf("=== %s logs ===\n%s", dst.name, composeLogs(t, root, dst.name))
					t.Errorf("ping %s -> %s failed: %v", src.name, dst.ip, err)
				}
			})
		}
	}
}

// TestE2E_PeerDeparture verifies that when a peer leaves, the remaining
// peers can still communicate, and that the departed peer can rejoin
// and restore full mesh connectivity.
func TestE2E_PeerDeparture(t *testing.T) {
	root := projectRoot(t)
	configDir := filepath.Join(root, "test", "e2e", "configs")

	os.RemoveAll(configDir)
	generateConfigs(t, configDir)

	t.Log("building and starting containers...")
	if _, err := compose(t, root, "up", "-d", "--build"); err != nil {
		t.Fatalf("compose up: %v", err)
	}

	t.Cleanup(func() {
		t.Log("tearing down containers...")
		if _, err := compose(t, root, "down", "--volumes", "--remove-orphans", "--timeout", "10"); err != nil {
			t.Logf("compose down error: %v", err)
		}
		os.RemoveAll(configDir)
	})

	// Wait for full mesh.
	t.Log("waiting for TUN interfaces...")
	for _, p := range peers {
		waitForTunnel(t, root, p.name, 30*time.Second)
	}
	t.Log("waiting for mesh connectivity...")
	for _, p := range peers {
		for _, other := range peers {
			if p.name == other.name {
				continue
			}
			waitForPing(t, root, p.name, other.ip, 30*time.Second)
		}
	}

	// Stop charlie.
	t.Log("stopping charlie...")
	if _, err := compose(t, root, "stop", "charlie"); err != nil {
		t.Fatalf("stopping charlie: %v", err)
	}

	// Wait a moment for the hub to detect charlie's departure.
	time.Sleep(3 * time.Second)

	// Alpha and bravo should still be able to ping each other.
	t.Log("verifying alpha <-> bravo connectivity after charlie's departure...")
	if err := ping(t, root, "alpha", "10.0.0.2"); err != nil {
		t.Errorf("alpha -> bravo failed after charlie left: %v", err)
	}
	if err := ping(t, root, "bravo", "10.0.0.1"); err != nil {
		t.Errorf("bravo -> alpha failed after charlie left: %v", err)
	}

	// Restart charlie (rejoin).
	t.Log("restarting charlie...")
	if _, err := compose(t, root, "start", "charlie"); err != nil {
		t.Fatalf("starting charlie: %v", err)
	}

	// Wait for charlie to rejoin and restore full mesh.
	waitForTunnel(t, root, "charlie", 30*time.Second)
	t.Log("waiting for charlie to rejoin mesh...")
	for _, p := range peers {
		if p.name == "charlie" {
			continue
		}
		waitForPing(t, root, "charlie", p.ip, 30*time.Second)
		waitForPing(t, root, p.name, "10.0.0.3", 30*time.Second)
	}

	// Final full-mesh verification.
	t.Log("verifying full mesh after charlie's return...")
	for _, src := range peers {
		for _, dst := range peers {
			if src.name == dst.name {
				continue
			}
			if err := ping(t, root, src.name, dst.ip); err != nil {
				t.Errorf("ping %s -> %s failed after rejoin: %v", src.name, dst.ip, err)
			}
		}
	}
}
