package tunnel

import (
	"net"
	"testing"
)

func TestShouldSkipInterface(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		iface  net.Interface
		expect bool
	}{
		{
			name: "loopback",
			iface: net.Interface{
				Name:  "lo",
				Flags: net.FlagLoopback | net.FlagUp,
			},
			expect: true,
		},
		{
			name: "down interface",
			iface: net.Interface{
				Name:  "eth0",
				Flags: 0, // not up
			},
			expect: true,
		},
		{
			name: "docker bridge",
			iface: net.Interface{
				Name:  "docker0",
				Flags: net.FlagUp,
			},
			expect: true,
		},
		{
			name: "veth pair",
			iface: net.Interface{
				Name:  "veth1234abc",
				Flags: net.FlagUp,
			},
			expect: true,
		},
		{
			name: "docker compose bridge",
			iface: net.Interface{
				Name:  "br-abc123def",
				Flags: net.FlagUp,
			},
			expect: true,
		},
		{
			name: "virbr libvirt",
			iface: net.Interface{
				Name:  "virbr0",
				Flags: net.FlagUp,
			},
			expect: true,
		},
		{
			name: "wireguard",
			iface: net.Interface{
				Name:  "wg0",
				Flags: net.FlagUp,
			},
			expect: true,
		},
		{
			name: "tun device",
			iface: net.Interface{
				Name:  "tun0",
				Flags: net.FlagUp,
			},
			expect: true,
		},
		{
			name: "tailscale",
			iface: net.Interface{
				Name:  "tailscale0",
				Flags: net.FlagUp,
			},
			expect: true,
		},
		{
			name: "physical ethernet",
			iface: net.Interface{
				Name:  "eth0",
				Flags: net.FlagUp,
			},
			expect: false,
		},
		{
			name: "wifi",
			iface: net.Interface{
				Name:  "wlan0",
				Flags: net.FlagUp,
			},
			expect: false,
		},
		{
			name: "eno physical",
			iface: net.Interface{
				Name:  "eno1",
				Flags: net.FlagUp,
			},
			expect: false,
		},
		{
			name: "enp physical",
			iface: net.Interface{
				Name:  "enp0s3",
				Flags: net.FlagUp,
			},
			expect: false,
		},
		{
			name: "macOS utun",
			iface: net.Interface{
				Name:  "utun3",
				Flags: net.FlagUp,
			},
			expect: true,
		},
		{
			name: "podman",
			iface: net.Interface{
				Name:  "podman0",
				Flags: net.FlagUp,
			},
			expect: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := shouldSkipInterface(tc.iface)
			if got != tc.expect {
				t.Errorf("shouldSkipInterface(%q, flags=%v) = %v, want %v",
					tc.iface.Name, tc.iface.Flags, got, tc.expect)
			}
		})
	}
}

func TestDiscoverLocalSubnets_ExcludesCIDR(t *testing.T) {
	t.Parallel()

	// Run discovery with and without an exclude CIDR.
	// We can't predict the exact results (depends on the host), but
	// we can verify that the exclude filter works if the host has
	// any interfaces at all.
	all, err := DiscoverLocalSubnets("")
	if err != nil {
		t.Fatalf("DiscoverLocalSubnets(%q): %v", "", err)
	}

	if len(all) == 0 {
		t.Skip("no non-virtual subnets found on this host")
	}

	// Exclude the first discovered subnet.
	excludeCIDR := all[0].CIDR
	filtered, err := DiscoverLocalSubnets(excludeCIDR)
	if err != nil {
		t.Fatalf("DiscoverLocalSubnets(%q): %v", excludeCIDR, err)
	}

	// The filtered list should have one fewer entry.
	if len(filtered) != len(all)-1 {
		t.Errorf("expected %d subnets after excluding %q, got %d",
			len(all)-1, excludeCIDR, len(filtered))
	}

	// The excluded CIDR should not appear in the filtered list.
	for _, s := range filtered {
		if s.CIDR == excludeCIDR {
			t.Errorf("excluded CIDR %q still present in filtered results", excludeCIDR)
		}
	}
}

func TestDiscoverLocalSubnets_NoDuplicates(t *testing.T) {
	t.Parallel()

	subnets, err := DiscoverLocalSubnets("")
	if err != nil {
		t.Fatalf("DiscoverLocalSubnets: %v", err)
	}

	seen := make(map[string]bool)
	for _, s := range subnets {
		if seen[s.CIDR] {
			t.Errorf("duplicate CIDR %q in results", s.CIDR)
		}
		seen[s.CIDR] = true
	}
}

func TestDiscoverLocalSubnets_NoLoopback(t *testing.T) {
	t.Parallel()

	subnets, err := DiscoverLocalSubnets("")
	if err != nil {
		t.Fatalf("DiscoverLocalSubnets: %v", err)
	}

	for _, s := range subnets {
		if s.Interface == "lo" || s.Interface == "lo0" {
			t.Errorf("loopback interface %q should be filtered out, got CIDR %q",
				s.Interface, s.CIDR)
		}
		// Also verify no 127.0.0.0/8 subnets.
		_, ipNet, err := net.ParseCIDR(s.CIDR)
		if err != nil {
			t.Errorf("invalid CIDR %q: %v", s.CIDR, err)
			continue
		}
		if ipNet.IP[0] == 127 {
			t.Errorf("loopback subnet %q should be filtered out", s.CIDR)
		}
	}
}
