//go:build linux && !android

package tunnel

import (
	"encoding/binary"
	"fmt"
	"net"
	"os"
	"os/exec"
	"strings"
	"unsafe"

	"golang.org/x/sys/unix"
)

// AddAddress assigns an IP address in CIDR notation to a network interface.
// This replaces `ip addr add <cidr> dev <ifName>`.
// Requires CAP_NET_ADMIN.
func AddAddress(ifName string, cidr string) error {
	ip, ipNet, err := net.ParseCIDR(cidr)
	if err != nil {
		return fmt.Errorf("parsing CIDR %q: %w", cidr, err)
	}

	ifIndex, err := interfaceIndex(ifName)
	if err != nil {
		return err
	}

	// Determine address family and address bytes.
	family := uint8(unix.AF_INET)
	ipBytes := ip.To4()
	if ipBytes == nil {
		family = unix.AF_INET6
		ipBytes = ip.To16()
	}
	prefixLen, _ := ipNet.Mask.Size()

	// Open a netlink route socket.
	fd, err := unix.Socket(unix.AF_NETLINK, unix.SOCK_RAW|unix.SOCK_CLOEXEC, unix.NETLINK_ROUTE)
	if err != nil {
		return fmt.Errorf("creating netlink socket: %w", err)
	}
	defer unix.Close(fd)

	if err := unix.Bind(fd, &unix.SockaddrNetlink{Family: unix.AF_NETLINK}); err != nil {
		return fmt.Errorf("binding netlink socket: %w", err)
	}

	// Build the RTM_NEWADDR message.
	msg := buildNewAddrMsg(ifIndex, family, uint8(prefixLen), ipBytes)

	if err := unix.Sendto(fd, msg, 0, &unix.SockaddrNetlink{Family: unix.AF_NETLINK}); err != nil {
		return fmt.Errorf("sending RTM_NEWADDR: %w", err)
	}

	// Read the ACK/error response.
	if err := readNetlinkAck(fd); err != nil {
		return fmt.Errorf("adding address %s to %s: %w", cidr, ifName, err)
	}

	return nil
}

// SetLinkUp brings a network interface into the UP state.
// This replaces `ip link set <ifName> up`.
// Requires CAP_NET_ADMIN.
func SetLinkUp(ifName string) error {
	ifIndex, err := interfaceIndex(ifName)
	if err != nil {
		return err
	}

	fd, err := unix.Socket(unix.AF_NETLINK, unix.SOCK_RAW|unix.SOCK_CLOEXEC, unix.NETLINK_ROUTE)
	if err != nil {
		return fmt.Errorf("creating netlink socket: %w", err)
	}
	defer unix.Close(fd)

	if err := unix.Bind(fd, &unix.SockaddrNetlink{Family: unix.AF_NETLINK}); err != nil {
		return fmt.Errorf("binding netlink socket: %w", err)
	}

	// Build the RTM_NEWLINK message to set IFF_UP.
	msg := buildSetLinkUpMsg(ifIndex)

	if err := unix.Sendto(fd, msg, 0, &unix.SockaddrNetlink{Family: unix.AF_NETLINK}); err != nil {
		return fmt.Errorf("sending RTM_NEWLINK: %w", err)
	}

	if err := readNetlinkAck(fd); err != nil {
		return fmt.Errorf("setting %s up: %w", ifName, err)
	}

	return nil
}

// interfaceIndex returns the kernel interface index for the named interface.
func interfaceIndex(name string) (int32, error) {
	iface, err := net.InterfaceByName(name)
	if err != nil {
		return 0, fmt.Errorf("looking up interface %q: %w", name, err)
	}
	return int32(iface.Index), nil
}

// AddRoute adds a kernel route for the given destination subnet via the named
// interface. This replaces `ip route add <cidr> dev <ifName>`.
// Requires CAP_NET_ADMIN.
func AddRoute(ifName string, cidr string) error {
	_, ipNet, err := net.ParseCIDR(cidr)
	if err != nil {
		return fmt.Errorf("parsing CIDR %q: %w", cidr, err)
	}

	ifIndex, err := interfaceIndex(ifName)
	if err != nil {
		return err
	}

	family := uint8(unix.AF_INET)
	dstBytes := ipNet.IP.To4()
	if dstBytes == nil {
		family = unix.AF_INET6
		dstBytes = ipNet.IP.To16()
	}
	prefixLen, _ := ipNet.Mask.Size()

	fd, err := unix.Socket(unix.AF_NETLINK, unix.SOCK_RAW|unix.SOCK_CLOEXEC, unix.NETLINK_ROUTE)
	if err != nil {
		return fmt.Errorf("creating netlink socket: %w", err)
	}
	defer unix.Close(fd)

	if err := unix.Bind(fd, &unix.SockaddrNetlink{Family: unix.AF_NETLINK}); err != nil {
		return fmt.Errorf("binding netlink socket: %w", err)
	}

	msg := buildRouteMsg(unix.RTM_NEWROUTE, unix.NLM_F_REQUEST|unix.NLM_F_ACK|unix.NLM_F_CREATE|unix.NLM_F_EXCL,
		ifIndex, family, uint8(prefixLen), dstBytes)

	if err := unix.Sendto(fd, msg, 0, &unix.SockaddrNetlink{Family: unix.AF_NETLINK}); err != nil {
		return fmt.Errorf("sending RTM_NEWROUTE: %w", err)
	}

	if err := readNetlinkAck(fd); err != nil {
		return fmt.Errorf("adding route %s via %s: %w", cidr, ifName, err)
	}

	return nil
}

// RemoveRoute removes a kernel route for the given destination subnet via the
// named interface. This replaces `ip route del <cidr> dev <ifName>`.
// Requires CAP_NET_ADMIN.
func RemoveRoute(ifName string, cidr string) error {
	_, ipNet, err := net.ParseCIDR(cidr)
	if err != nil {
		return fmt.Errorf("parsing CIDR %q: %w", cidr, err)
	}

	ifIndex, err := interfaceIndex(ifName)
	if err != nil {
		return err
	}

	family := uint8(unix.AF_INET)
	dstBytes := ipNet.IP.To4()
	if dstBytes == nil {
		family = unix.AF_INET6
		dstBytes = ipNet.IP.To16()
	}
	prefixLen, _ := ipNet.Mask.Size()

	fd, err := unix.Socket(unix.AF_NETLINK, unix.SOCK_RAW|unix.SOCK_CLOEXEC, unix.NETLINK_ROUTE)
	if err != nil {
		return fmt.Errorf("creating netlink socket: %w", err)
	}
	defer unix.Close(fd)

	if err := unix.Bind(fd, &unix.SockaddrNetlink{Family: unix.AF_NETLINK}); err != nil {
		return fmt.Errorf("binding netlink socket: %w", err)
	}

	msg := buildRouteMsg(unix.RTM_DELROUTE, unix.NLM_F_REQUEST|unix.NLM_F_ACK,
		ifIndex, family, uint8(prefixLen), dstBytes)

	if err := unix.Sendto(fd, msg, 0, &unix.SockaddrNetlink{Family: unix.AF_NETLINK}); err != nil {
		return fmt.Errorf("sending RTM_DELROUTE: %w", err)
	}

	if err := readNetlinkAck(fd); err != nil {
		return fmt.Errorf("removing route %s via %s: %w", cidr, ifName, err)
	}

	return nil
}

// --- Netlink message construction ---
//
// These functions build raw netlink messages. The message format is:
//   nlmsghdr | payload (ifaddrmsg/ifinfomsg/rtmsg) | attributes (rtattr...)
//
// Using raw construction avoids pulling in a netlink library.

const (
	nlmsgHdrLen  = 16 // sizeof(nlmsghdr)
	ifaddrmsgLen = 8  // sizeof(ifaddrmsg)
	ifinfomsgLen = 16 // sizeof(ifinfomsg)
	rtmsgLen     = 12 // sizeof(rtmsg)
	rtaHdrLen    = 4  // sizeof(rtattr)
)

// buildNewAddrMsg constructs an RTM_NEWADDR netlink message.
func buildNewAddrMsg(ifIndex int32, family uint8, prefixLen uint8, addr []byte) []byte {
	// Calculate attribute sizes: IFA_LOCAL + IFA_ADDRESS
	addrAttrLen := rtaAlignLen(rtaHdrLen + len(addr))
	attrsLen := addrAttrLen * 2

	totalLen := nlmsgHdrLen + ifaddrmsgLen + attrsLen
	buf := make([]byte, totalLen)

	// nlmsghdr
	binary.LittleEndian.PutUint32(buf[0:4], uint32(totalLen))                                                    // nlmsg_len
	binary.LittleEndian.PutUint16(buf[4:6], unix.RTM_NEWADDR)                                                    // nlmsg_type
	binary.LittleEndian.PutUint16(buf[6:8], unix.NLM_F_REQUEST|unix.NLM_F_ACK|unix.NLM_F_CREATE|unix.NLM_F_EXCL) // nlmsg_flags
	binary.LittleEndian.PutUint32(buf[8:12], 1)                                                                  // nlmsg_seq
	binary.LittleEndian.PutUint32(buf[12:16], 0)                                                                 // nlmsg_pid

	// ifaddrmsg
	off := nlmsgHdrLen
	buf[off] = family                                                // ifa_family
	buf[off+1] = prefixLen                                           // ifa_prefixlen
	buf[off+2] = 0                                                   // ifa_flags
	buf[off+3] = unix.RT_SCOPE_UNIVERSE                              // ifa_scope
	binary.LittleEndian.PutUint32(buf[off+4:off+8], uint32(ifIndex)) // ifa_index

	// IFA_LOCAL attribute
	off = nlmsgHdrLen + ifaddrmsgLen
	binary.LittleEndian.PutUint16(buf[off:off+2], uint16(rtaHdrLen+len(addr))) // rta_len
	binary.LittleEndian.PutUint16(buf[off+2:off+4], unix.IFA_LOCAL)            // rta_type
	copy(buf[off+rtaHdrLen:], addr)

	// IFA_ADDRESS attribute
	off += addrAttrLen
	binary.LittleEndian.PutUint16(buf[off:off+2], uint16(rtaHdrLen+len(addr))) // rta_len
	binary.LittleEndian.PutUint16(buf[off+2:off+4], unix.IFA_ADDRESS)          // rta_type
	copy(buf[off+rtaHdrLen:], addr)

	return buf
}

// buildSetLinkUpMsg constructs an RTM_NEWLINK netlink message that sets IFF_UP.
func buildSetLinkUpMsg(ifIndex int32) []byte {
	totalLen := nlmsgHdrLen + ifinfomsgLen
	buf := make([]byte, totalLen)

	// nlmsghdr
	binary.LittleEndian.PutUint32(buf[0:4], uint32(totalLen))
	binary.LittleEndian.PutUint16(buf[4:6], unix.RTM_NEWLINK)
	binary.LittleEndian.PutUint16(buf[6:8], unix.NLM_F_REQUEST|unix.NLM_F_ACK)
	binary.LittleEndian.PutUint32(buf[8:12], 1)  // nlmsg_seq
	binary.LittleEndian.PutUint32(buf[12:16], 0) // nlmsg_pid

	// ifinfomsg
	off := nlmsgHdrLen
	buf[off] = unix.AF_UNSPEC // ifi_family
	// buf[off+1] is padding
	// ifi_type (uint16) at off+2 = 0
	binary.LittleEndian.PutUint32(buf[off+4:off+8], uint32(ifIndex)) // ifi_index
	binary.LittleEndian.PutUint32(buf[off+8:off+12], unix.IFF_UP)    // ifi_flags
	binary.LittleEndian.PutUint32(buf[off+12:off+16], unix.IFF_UP)   // ifi_change (mask)

	return buf
}

// readNetlinkAck reads and validates the netlink ACK response.
// Returns nil on success or an error describing the failure.
func readNetlinkAck(fd int) error {
	buf := make([]byte, 4096)
	n, _, err := unix.Recvfrom(fd, buf, 0)
	if err != nil {
		return fmt.Errorf("reading netlink response: %w", err)
	}
	if n < nlmsgHdrLen {
		return fmt.Errorf("netlink response too short: %d bytes", n)
	}

	// Parse the nlmsghdr.
	msgType := binary.LittleEndian.Uint16(buf[4:6])
	if msgType == unix.NLMSG_ERROR {
		// The error code is a signed int32 right after the nlmsghdr.
		if n < nlmsgHdrLen+4 {
			return fmt.Errorf("truncated NLMSG_ERROR response")
		}
		errno := *(*int32)(unsafe.Pointer(&buf[nlmsgHdrLen]))
		if errno == 0 {
			return nil // ACK (error code 0 = success)
		}
		return fmt.Errorf("netlink error: %s", unix.Errno(-errno))
	}

	return nil
}

// buildRouteMsg constructs an RTM_NEWROUTE or RTM_DELROUTE netlink message.
// It adds a route for the given destination prefix via the specified interface.
func buildRouteMsg(msgType uint16, flags uint16, ifIndex int32, family uint8, prefixLen uint8, dst []byte) []byte {
	// Attributes: RTA_DST + RTA_OIF
	dstAttrLen := rtaAlignLen(rtaHdrLen + len(dst))
	oifAttrLen := rtaAlignLen(rtaHdrLen + 4) // interface index is uint32

	totalLen := nlmsgHdrLen + rtmsgLen + dstAttrLen + oifAttrLen
	buf := make([]byte, totalLen)

	// nlmsghdr
	binary.LittleEndian.PutUint32(buf[0:4], uint32(totalLen)) // nlmsg_len
	binary.LittleEndian.PutUint16(buf[4:6], msgType)          // nlmsg_type
	binary.LittleEndian.PutUint16(buf[6:8], flags)            // nlmsg_flags
	binary.LittleEndian.PutUint32(buf[8:12], 1)               // nlmsg_seq
	binary.LittleEndian.PutUint32(buf[12:16], 0)              // nlmsg_pid

	// rtmsg
	off := nlmsgHdrLen
	buf[off] = family                                   // rtm_family
	buf[off+1] = prefixLen                              // rtm_dst_len
	buf[off+2] = 0                                      // rtm_src_len
	buf[off+3] = 0                                      // rtm_tos
	buf[off+4] = unix.RT_TABLE_MAIN                     // rtm_table
	buf[off+5] = unix.RTPROT_BOOT                       // rtm_protocol
	buf[off+6] = unix.RT_SCOPE_LINK                     // rtm_scope
	buf[off+7] = unix.RTN_UNICAST                       // rtm_type
	binary.LittleEndian.PutUint32(buf[off+8:off+12], 0) // rtm_flags

	// RTA_DST attribute
	off = nlmsgHdrLen + rtmsgLen
	binary.LittleEndian.PutUint16(buf[off:off+2], uint16(rtaHdrLen+len(dst))) // rta_len
	binary.LittleEndian.PutUint16(buf[off+2:off+4], unix.RTA_DST)             // rta_type
	copy(buf[off+rtaHdrLen:], dst)

	// RTA_OIF attribute (output interface index)
	off += dstAttrLen
	binary.LittleEndian.PutUint16(buf[off:off+2], uint16(rtaHdrLen+4)) // rta_len
	binary.LittleEndian.PutUint16(buf[off+2:off+4], unix.RTA_OIF)      // rta_type
	binary.LittleEndian.PutUint32(buf[off+rtaHdrLen:off+rtaHdrLen+4], uint32(ifIndex))

	return buf
}

// --- IPv4 forwarding via netlink ---
//
// Per-interface IPv4 forwarding is controlled via the IFLA_AF_SPEC > AF_INET >
// IFLA_INET_CONF netlink attribute. This avoids writing to /proc/sys, which
// requires CAP_DAC_OVERRIDE or root. The netlink approach only requires
// CAP_NET_ADMIN.
//
// The kernel's ipv4_devconf array indexes are defined in
// include/uapi/linux/ip.h. The forwarding index is IPV4_DEVCONF_FORWARDING = 1.

const (
	// ipv4DevconfForwarding is the index into the ipv4_devconf array for the
	// forwarding setting. Defined in include/uapi/linux/ip.h as
	// IPV4_DEVCONF_FORWARDING = 1.
	ipv4DevconfForwarding = 1
)

// GetForwarding reads the current IPv4 forwarding state for a network interface.
// Returns true if forwarding is enabled. This reads from /proc/sys which is
// world-readable and requires no special capabilities.
func GetForwarding(ifName string) (bool, error) {
	path := fmt.Sprintf("/proc/sys/net/ipv4/conf/%s/forwarding", ifName)
	data, err := os.ReadFile(path)
	if err != nil {
		return false, fmt.Errorf("reading forwarding state for %s: %w", ifName, err)
	}
	return strings.TrimSpace(string(data)) == "1", nil
}

// SetForwarding enables or disables IPv4 forwarding on a network interface
// using netlink RTM_SETLINK with IFLA_AF_SPEC. This replaces writing to
// /proc/sys/net/ipv4/conf/<ifName>/forwarding.
// Requires CAP_NET_ADMIN.
func SetForwarding(ifName string, enabled bool) error {
	ifIndex, err := interfaceIndex(ifName)
	if err != nil {
		return err
	}

	fd, err := unix.Socket(unix.AF_NETLINK, unix.SOCK_RAW|unix.SOCK_CLOEXEC, unix.NETLINK_ROUTE)
	if err != nil {
		return fmt.Errorf("creating netlink socket: %w", err)
	}
	defer unix.Close(fd)

	if err := unix.Bind(fd, &unix.SockaddrNetlink{Family: unix.AF_NETLINK}); err != nil {
		return fmt.Errorf("binding netlink socket: %w", err)
	}

	msg := buildSetForwardingMsg(ifIndex, enabled)

	if err := unix.Sendto(fd, msg, 0, &unix.SockaddrNetlink{Family: unix.AF_NETLINK}); err != nil {
		return fmt.Errorf("sending RTM_SETLINK for forwarding: %w", err)
	}

	if err := readNetlinkAck(fd); err != nil {
		return fmt.Errorf("setting forwarding on %s: %w", ifName, err)
	}

	return nil
}

// buildSetForwardingMsg constructs an RTM_SETLINK netlink message with nested
// IFLA_AF_SPEC > AF_INET > IFLA_INET_CONF attributes to set IPv4 forwarding.
//
// The message structure is:
//
//	nlmsghdr
//	ifinfomsg
//	IFLA_AF_SPEC (nested) {
//	    AF_INET (nested) {
//	        IFLA_INET_CONF: [type=IPV4_DEVCONF_FORWARDING, value=0|1]
//	    }
//	}
//
// The IFLA_INET_CONF payload is an array of {type, value} pairs where type is
// the nla_type (the devconf index) and value is a uint32.
func buildSetForwardingMsg(ifIndex int32, enabled bool) []byte {
	val := uint32(0)
	if enabled {
		val = 1
	}

	// Inner: IFLA_INET_CONF attribute containing one devconf entry.
	// The kernel expects the devconf entries as nested rtattrs within
	// IFLA_INET_CONF, where each nested attr has type = devconf index
	// and data = uint32 value.
	inetConfEntry := rtaAlignLen(rtaHdrLen + 4)  // single u32 entry
	inetConfAttrLen := rtaHdrLen + inetConfEntry // IFLA_INET_CONF header + entry

	// Middle: AF_INET attribute containing IFLA_INET_CONF.
	afInetAttrLen := rtaHdrLen + rtaAlignLen(inetConfAttrLen)

	// Outer: IFLA_AF_SPEC attribute containing AF_INET.
	afSpecAttrLen := rtaHdrLen + rtaAlignLen(afInetAttrLen)

	totalLen := nlmsgHdrLen + ifinfomsgLen + rtaAlignLen(afSpecAttrLen)
	buf := make([]byte, totalLen)

	// nlmsghdr
	binary.LittleEndian.PutUint32(buf[0:4], uint32(totalLen))
	binary.LittleEndian.PutUint16(buf[4:6], unix.RTM_SETLINK)
	binary.LittleEndian.PutUint16(buf[6:8], unix.NLM_F_REQUEST|unix.NLM_F_ACK)
	binary.LittleEndian.PutUint32(buf[8:12], 1)  // nlmsg_seq
	binary.LittleEndian.PutUint32(buf[12:16], 0) // nlmsg_pid

	// ifinfomsg
	off := nlmsgHdrLen
	buf[off] = unix.AF_UNSPEC
	binary.LittleEndian.PutUint32(buf[off+4:off+8], uint32(ifIndex))
	// ifi_flags and ifi_change = 0 (not changing link flags)

	// IFLA_AF_SPEC (NLA_F_NESTED | IFLA_AF_SPEC)
	off = nlmsgHdrLen + ifinfomsgLen
	binary.LittleEndian.PutUint16(buf[off:off+2], uint16(afSpecAttrLen))
	binary.LittleEndian.PutUint16(buf[off+2:off+4], unix.NLA_F_NESTED|unix.IFLA_AF_SPEC)

	// AF_INET (NLA_F_NESTED | AF_INET)
	off += rtaHdrLen
	binary.LittleEndian.PutUint16(buf[off:off+2], uint16(afInetAttrLen))
	binary.LittleEndian.PutUint16(buf[off+2:off+4], unix.NLA_F_NESTED|unix.AF_INET)

	// IFLA_INET_CONF
	off += rtaHdrLen
	binary.LittleEndian.PutUint16(buf[off:off+2], uint16(inetConfAttrLen))
	binary.LittleEndian.PutUint16(buf[off+2:off+4], unix.IFLA_INET_CONF)

	// Devconf entry: type = IPV4_DEVCONF_FORWARDING, value = 0 or 1.
	off += rtaHdrLen
	binary.LittleEndian.PutUint16(buf[off:off+2], uint16(rtaHdrLen+4))
	binary.LittleEndian.PutUint16(buf[off+2:off+4], ipv4DevconfForwarding)
	binary.LittleEndian.PutUint32(buf[off+rtaHdrLen:off+rtaHdrLen+4], val)

	return buf
}

// rtaAlignLen rounds a length up to the nearest 4-byte boundary (RTA_ALIGN).
func rtaAlignLen(l int) int {
	return (l + 3) &^ 3
}

// SetDNS configures per-interface DNS servers and search domains using
// systemd-resolved via the resolvectl command. This sets DNS only for the
// specified interface, leaving system-wide DNS unaffected.
// Falls back to writing /etc/resolv.conf if systemd-resolved is not available.
func SetDNS(ifName string, servers []string, searchDomains []string) error {
	if len(servers) == 0 && len(searchDomains) == 0 {
		return nil
	}

	// Try systemd-resolved first (resolvectl).
	if _, err := exec.LookPath("resolvectl"); err == nil {
		return setDNSResolvectl(ifName, servers, searchDomains)
	}

	// Fallback: write to /etc/resolv.conf (less ideal but works everywhere).
	return setDNSResolvConf(servers, searchDomains)
}

// RevertDNS removes per-interface DNS configuration set by SetDNS.
func RevertDNS(ifName string) error {
	// With systemd-resolved, DNS config is automatically removed when the
	// interface goes down. We call revert explicitly for clean teardown.
	if _, err := exec.LookPath("resolvectl"); err == nil {
		cmd := exec.Command("resolvectl", "revert", ifName)
		if out, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("resolvectl revert %s: %w (output: %s)",
				ifName, err, strings.TrimSpace(string(out)))
		}
		return nil
	}

	// No cleanup needed for /etc/resolv.conf fallback — the file is not
	// interface-scoped, so we can't safely remove entries. The system will
	// return to its normal DNS config on reboot or DHCP renewal.
	return nil
}

// setDNSResolvectl uses resolvectl to set per-interface DNS.
func setDNSResolvectl(ifName string, servers []string, searchDomains []string) error {
	if len(servers) > 0 {
		args := append([]string{"dns", ifName}, servers...)
		cmd := exec.Command("resolvectl", args...)
		if out, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("resolvectl dns %s: %w (output: %s)",
				ifName, err, strings.TrimSpace(string(out)))
		}
	}

	if len(searchDomains) > 0 {
		args := append([]string{"domain", ifName}, searchDomains...)
		cmd := exec.Command("resolvectl", args...)
		if out, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("resolvectl domain %s: %w (output: %s)",
				ifName, err, strings.TrimSpace(string(out)))
		}
	}

	return nil
}

// setDNSResolvConf is a basic fallback that prepends nameserver entries to
// /etc/resolv.conf. This is a best-effort approach for systems without
// systemd-resolved.
func setDNSResolvConf(servers []string, searchDomains []string) error {
	existing, err := os.ReadFile("/etc/resolv.conf")
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("reading /etc/resolv.conf: %w", err)
	}

	var lines []string
	lines = append(lines, "# Added by bamgate")
	for _, s := range servers {
		lines = append(lines, "nameserver "+s)
	}
	if len(searchDomains) > 0 {
		lines = append(lines, "search "+strings.Join(searchDomains, " "))
	}

	newContent := strings.Join(lines, "\n") + "\n" + string(existing)
	if err := os.WriteFile("/etc/resolv.conf", []byte(newContent), 0644); err != nil {
		return fmt.Errorf("writing /etc/resolv.conf: %w", err)
	}

	return nil
}
