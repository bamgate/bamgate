package tunnel

import (
	"encoding/binary"
	"testing"

	"golang.org/x/sys/unix"
)

func TestBuildNewAddrMsg_IPv4(t *testing.T) {
	t.Parallel()

	addr := []byte{10, 0, 0, 1}
	msg := buildNewAddrMsg(5, unix.AF_INET, 24, addr)

	// Verify nlmsghdr.
	msgLen := binary.LittleEndian.Uint32(msg[0:4])
	if int(msgLen) != len(msg) {
		t.Errorf("nlmsg_len = %d, want %d", msgLen, len(msg))
	}
	msgType := binary.LittleEndian.Uint16(msg[4:6])
	if msgType != unix.RTM_NEWADDR {
		t.Errorf("nlmsg_type = %d, want RTM_NEWADDR (%d)", msgType, unix.RTM_NEWADDR)
	}

	// Verify ifaddrmsg.
	off := nlmsgHdrLen
	if msg[off] != unix.AF_INET {
		t.Errorf("ifa_family = %d, want AF_INET (%d)", msg[off], unix.AF_INET)
	}
	if msg[off+1] != 24 {
		t.Errorf("ifa_prefixlen = %d, want 24", msg[off+1])
	}
	ifIndex := binary.LittleEndian.Uint32(msg[off+4 : off+8])
	if ifIndex != 5 {
		t.Errorf("ifa_index = %d, want 5", ifIndex)
	}

	// Verify IFA_LOCAL attribute.
	off = nlmsgHdrLen + ifaddrmsgLen
	rtaType := binary.LittleEndian.Uint16(msg[off+2 : off+4])
	if rtaType != unix.IFA_LOCAL {
		t.Errorf("first attr type = %d, want IFA_LOCAL (%d)", rtaType, unix.IFA_LOCAL)
	}
	gotAddr := msg[off+rtaHdrLen : off+rtaHdrLen+4]
	for i := range addr {
		if gotAddr[i] != addr[i] {
			t.Errorf("IFA_LOCAL addr byte %d = %d, want %d", i, gotAddr[i], addr[i])
		}
	}
}

func TestBuildNewAddrMsg_IPv6(t *testing.T) {
	t.Parallel()

	addr := make([]byte, 16)
	addr[0] = 0xfd
	addr[15] = 0x01
	msg := buildNewAddrMsg(3, unix.AF_INET6, 64, addr)

	off := nlmsgHdrLen
	if msg[off] != unix.AF_INET6 {
		t.Errorf("ifa_family = %d, want AF_INET6 (%d)", msg[off], unix.AF_INET6)
	}
	if msg[off+1] != 64 {
		t.Errorf("ifa_prefixlen = %d, want 64", msg[off+1])
	}
}

func TestBuildSetLinkUpMsg(t *testing.T) {
	t.Parallel()

	msg := buildSetLinkUpMsg(7)

	// Verify nlmsghdr.
	msgLen := binary.LittleEndian.Uint32(msg[0:4])
	if int(msgLen) != len(msg) {
		t.Errorf("nlmsg_len = %d, want %d", msgLen, len(msg))
	}
	msgType := binary.LittleEndian.Uint16(msg[4:6])
	if msgType != unix.RTM_NEWLINK {
		t.Errorf("nlmsg_type = %d, want RTM_NEWLINK (%d)", msgType, unix.RTM_NEWLINK)
	}

	// Verify ifinfomsg.
	off := nlmsgHdrLen
	ifIndex := binary.LittleEndian.Uint32(msg[off+4 : off+8])
	if ifIndex != 7 {
		t.Errorf("ifi_index = %d, want 7", ifIndex)
	}
	flags := binary.LittleEndian.Uint32(msg[off+8 : off+12])
	if flags != unix.IFF_UP {
		t.Errorf("ifi_flags = 0x%x, want IFF_UP (0x%x)", flags, unix.IFF_UP)
	}
	change := binary.LittleEndian.Uint32(msg[off+12 : off+16])
	if change != unix.IFF_UP {
		t.Errorf("ifi_change = 0x%x, want IFF_UP (0x%x)", change, unix.IFF_UP)
	}
}

func TestBuildRouteMsg_IPv4(t *testing.T) {
	t.Parallel()

	dst := []byte{192, 168, 1, 0}
	msg := buildRouteMsg(unix.RTM_NEWROUTE,
		unix.NLM_F_REQUEST|unix.NLM_F_ACK|unix.NLM_F_CREATE|unix.NLM_F_EXCL,
		5, unix.AF_INET, 24, dst)

	// Verify nlmsghdr.
	msgLen := binary.LittleEndian.Uint32(msg[0:4])
	if int(msgLen) != len(msg) {
		t.Errorf("nlmsg_len = %d, want %d", msgLen, len(msg))
	}
	msgType := binary.LittleEndian.Uint16(msg[4:6])
	if msgType != unix.RTM_NEWROUTE {
		t.Errorf("nlmsg_type = %d, want RTM_NEWROUTE (%d)", msgType, unix.RTM_NEWROUTE)
	}

	// Verify rtmsg.
	off := nlmsgHdrLen
	if msg[off] != unix.AF_INET {
		t.Errorf("rtm_family = %d, want AF_INET (%d)", msg[off], unix.AF_INET)
	}
	if msg[off+1] != 24 {
		t.Errorf("rtm_dst_len = %d, want 24", msg[off+1])
	}
	if msg[off+4] != unix.RT_TABLE_MAIN {
		t.Errorf("rtm_table = %d, want RT_TABLE_MAIN (%d)", msg[off+4], unix.RT_TABLE_MAIN)
	}
	if msg[off+6] != unix.RT_SCOPE_LINK {
		t.Errorf("rtm_scope = %d, want RT_SCOPE_LINK (%d)", msg[off+6], unix.RT_SCOPE_LINK)
	}
	if msg[off+7] != unix.RTN_UNICAST {
		t.Errorf("rtm_type = %d, want RTN_UNICAST (%d)", msg[off+7], unix.RTN_UNICAST)
	}

	// Verify RTA_DST attribute.
	off = nlmsgHdrLen + rtmsgLen
	rtaType := binary.LittleEndian.Uint16(msg[off+2 : off+4])
	if rtaType != unix.RTA_DST {
		t.Errorf("first attr type = %d, want RTA_DST (%d)", rtaType, unix.RTA_DST)
	}
	gotDst := msg[off+rtaHdrLen : off+rtaHdrLen+4]
	for i := range dst {
		if gotDst[i] != dst[i] {
			t.Errorf("RTA_DST byte %d = %d, want %d", i, gotDst[i], dst[i])
		}
	}

	// Verify RTA_OIF attribute.
	dstAttrLen := rtaAlignLen(rtaHdrLen + len(dst))
	off += dstAttrLen
	rtaType = binary.LittleEndian.Uint16(msg[off+2 : off+4])
	if rtaType != unix.RTA_OIF {
		t.Errorf("second attr type = %d, want RTA_OIF (%d)", rtaType, unix.RTA_OIF)
	}
	ifIndex := binary.LittleEndian.Uint32(msg[off+rtaHdrLen : off+rtaHdrLen+4])
	if ifIndex != 5 {
		t.Errorf("RTA_OIF = %d, want 5", ifIndex)
	}
}

func TestBuildRouteMsg_IPv6(t *testing.T) {
	t.Parallel()

	dst := make([]byte, 16)
	dst[0] = 0xfd
	dst[1] = 0x00
	msg := buildRouteMsg(unix.RTM_NEWROUTE,
		unix.NLM_F_REQUEST|unix.NLM_F_ACK|unix.NLM_F_CREATE|unix.NLM_F_EXCL,
		3, unix.AF_INET6, 64, dst)

	off := nlmsgHdrLen
	if msg[off] != unix.AF_INET6 {
		t.Errorf("rtm_family = %d, want AF_INET6 (%d)", msg[off], unix.AF_INET6)
	}
	if msg[off+1] != 64 {
		t.Errorf("rtm_dst_len = %d, want 64", msg[off+1])
	}
}

func TestBuildRouteMsg_Delete(t *testing.T) {
	t.Parallel()

	dst := []byte{10, 0, 0, 0}
	msg := buildRouteMsg(unix.RTM_DELROUTE,
		unix.NLM_F_REQUEST|unix.NLM_F_ACK,
		7, unix.AF_INET, 24, dst)

	msgType := binary.LittleEndian.Uint16(msg[4:6])
	if msgType != unix.RTM_DELROUTE {
		t.Errorf("nlmsg_type = %d, want RTM_DELROUTE (%d)", msgType, unix.RTM_DELROUTE)
	}

	flags := binary.LittleEndian.Uint16(msg[6:8])
	wantFlags := uint16(unix.NLM_F_REQUEST | unix.NLM_F_ACK)
	if flags != wantFlags {
		t.Errorf("nlmsg_flags = 0x%x, want 0x%x", flags, wantFlags)
	}
}

func TestRtaAlignLen(t *testing.T) {
	t.Parallel()

	tests := []struct {
		in, want int
	}{
		{4, 4},
		{5, 8},
		{7, 8},
		{8, 8},
		{9, 12},
	}
	for _, tt := range tests {
		if got := rtaAlignLen(tt.in); got != tt.want {
			t.Errorf("rtaAlignLen(%d) = %d, want %d", tt.in, got, tt.want)
		}
	}
}
