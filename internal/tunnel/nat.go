//go:build linux

package tunnel

import (
	"fmt"
	"log/slog"
	"net"

	"github.com/google/nftables"
	"github.com/google/nftables/expr"
)

const (
	// nftTableName is the nftables table name used by riftgate.
	// All rules are scoped to this table so they don't interfere with
	// other firewall rules on the system.
	nftTableName = "riftgate"
)

// NATManager manages nftables rules for masquerading traffic from the
// WireGuard tunnel to local network subnets. It creates a dedicated "riftgate"
// table with a postrouting NAT chain.
//
// Requires CAP_NET_ADMIN.
type NATManager struct {
	log   *slog.Logger
	table *nftables.Table
	conn  *nftables.Conn
}

// NewNATManager creates a new NATManager.
func NewNATManager(logger *slog.Logger) *NATManager {
	return &NATManager{
		log: logger.With("component", "nat"),
	}
}

// SetupMasquerade creates nftables rules to masquerade traffic from the
// WireGuard subnet going out through the specified interface. This is
// equivalent to:
//
//	nft add table ip riftgate
//	nft add chain ip riftgate postrouting { type nat hook postrouting priority srcnat; }
//	nft add rule ip riftgate postrouting ip saddr <wgSubnet> oifname <outIface> masquerade
//
// The wgSubnet should be in CIDR notation (e.g., "10.0.0.0/24").
// The outIface is the network interface to masquerade on (e.g., "wlan0").
func (n *NATManager) SetupMasquerade(wgSubnet string, outIface string) error {
	ip, ipNet, err := net.ParseCIDR(wgSubnet)
	if err != nil {
		return fmt.Errorf("parsing WireGuard subnet %q: %w", wgSubnet, err)
	}

	// Only support IPv4 for now.
	ipv4 := ip.To4()
	if ipv4 == nil {
		return fmt.Errorf("only IPv4 subnets are supported for masquerade, got %q", wgSubnet)
	}

	networkAddr := ipNet.IP.To4()
	mask := ipNet.Mask

	c, err := nftables.New()
	if err != nil {
		return fmt.Errorf("connecting to nftables: %w", err)
	}
	n.conn = c

	// Create table.
	table := c.AddTable(&nftables.Table{
		Family: nftables.TableFamilyIPv4,
		Name:   nftTableName,
	})
	n.table = table

	// Create postrouting NAT chain.
	chain := c.AddChain(&nftables.Chain{
		Name:     "postrouting",
		Table:    table,
		Type:     nftables.ChainTypeNAT,
		Hooknum:  nftables.ChainHookPostrouting,
		Priority: nftables.ChainPriorityNATSource,
	})

	// Build the nftables rule:
	//   ip saddr & <mask> == <network> oifname <outIface> masquerade
	//
	// This is equivalent to: ip saddr <wgSubnet> oifname <outIface> masquerade
	//
	// The rule uses:
	// 1. payload: load source IP (4 bytes at offset 12 of network header)
	// 2. bitwise: AND with subnet mask
	// 3. cmp: compare with network address
	// 4. meta: load output interface name
	// 5. cmp: compare with interface name
	// 6. masquerade

	// Pad interface name to 16 bytes (IFNAMSIZ) with null bytes for nftables comparison.
	ifaceData := make([]byte, 16)
	copy(ifaceData, outIface)

	c.AddRule(&nftables.Rule{
		Table: table,
		Chain: chain,
		Exprs: []expr.Any{
			// Load source IP address into register 1.
			&expr.Payload{
				DestRegister: 1,
				Base:         expr.PayloadBaseNetworkHeader,
				Offset:       12, // IPv4 source address offset
				Len:          4,  // IPv4 address length
			},
			// Bitwise AND with subnet mask.
			&expr.Bitwise{
				SourceRegister: 1,
				DestRegister:   1,
				Len:            4,
				Mask:           mask,
				Xor:            []byte{0, 0, 0, 0},
			},
			// Compare with network address.
			&expr.Cmp{
				Op:       expr.CmpOpEq,
				Register: 1,
				Data:     networkAddr,
			},
			// Load output interface name into register 1.
			&expr.Meta{
				Key:      expr.MetaKeyOIFNAME,
				Register: 1,
			},
			// Compare with target interface name.
			&expr.Cmp{
				Op:       expr.CmpOpEq,
				Register: 1,
				Data:     ifaceData,
			},
			// Apply masquerade.
			&expr.Masq{},
		},
	})

	// Flush all buffered commands atomically.
	if err := c.Flush(); err != nil {
		return fmt.Errorf("applying nftables rules: %w", err)
	}

	n.log.Info("nftables masquerade rule added",
		"table", nftTableName,
		"subnet", wgSubnet,
		"out_iface", outIface,
	)

	return nil
}

// Cleanup removes the riftgate nftables table and all its rules.
// This is safe to call even if SetupMasquerade was never called.
func (n *NATManager) Cleanup() error {
	c := n.conn
	if c == nil {
		var err error
		c, err = nftables.New()
		if err != nil {
			return fmt.Errorf("connecting to nftables: %w", err)
		}
	}

	if n.table != nil {
		c.DelTable(n.table)
	} else {
		// Try to delete by name in case we're cleaning up from a previous run.
		c.DelTable(&nftables.Table{
			Family: nftables.TableFamilyIPv4,
			Name:   nftTableName,
		})
	}

	if err := c.Flush(); err != nil {
		// Table may not exist, which is fine.
		n.log.Debug("nftables cleanup (table may not have existed)", "error", err)
		return nil
	}

	n.log.Info("nftables riftgate table removed")
	return nil
}
