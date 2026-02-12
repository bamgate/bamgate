package tunnel

import (
	"encoding/binary"
	"fmt"
	"net"
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

// --- Netlink message construction ---
//
// These functions build raw netlink messages. The message format is:
//   nlmsghdr | payload (ifaddrmsg/ifinfomsg) | attributes (rtattr...)
//
// Using raw construction avoids pulling in a netlink library.

const (
	nlmsgHdrLen  = 16 // sizeof(nlmsghdr)
	ifaddrmsgLen = 8  // sizeof(ifaddrmsg)
	ifinfomsgLen = 16 // sizeof(ifinfomsg)
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

// rtaAlignLen rounds a length up to the nearest 4-byte boundary (RTA_ALIGN).
func rtaAlignLen(l int) int {
	return (l + 3) &^ 3
}
