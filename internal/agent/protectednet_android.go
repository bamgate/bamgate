//go:build android

package agent

/*
#include <ifaddrs.h>
#include <net/if.h>
#include <arpa/inet.h>
#include <string.h>
#include <stdlib.h>
*/
import "C"

import (
	"fmt"
	"net"
	"unsafe"

	transport "github.com/pion/transport/v4"
)

// platformInterfaces returns interfaces on Android using getifaddrs(3),
// which works in app sandboxes where /proc/net/* and /sys/class/net/*
// are blocked by SELinux, and netlink route sockets are denied.
func platformInterfaces() ([]*transport.Interface, error) {
	return cgoInterfaces()
}

// cgoInterfaces enumerates network interfaces using the C getifaddrs() libc
// function. This is the only reliable way to enumerate interfaces from an
// Android untrusted_app context.
func cgoInterfaces() ([]*transport.Interface, error) {
	var ifap *C.struct_ifaddrs
	if rc := C.getifaddrs(&ifap); rc != 0 {
		return nil, fmt.Errorf("getifaddrs failed with rc %d", rc)
	}
	defer C.freeifaddrs(ifap)

	// First pass: collect unique interfaces by name.
	type ifInfo struct {
		index int
		flags net.Flags
		mtu   int
		addrs []net.Addr
	}
	ifMap := make(map[string]*ifInfo)

	for ifa := ifap; ifa != nil; ifa = ifa.ifa_next {
		name := C.GoString(ifa.ifa_name)

		info, exists := ifMap[name]
		if !exists {
			info = &ifInfo{
				index: int(C.if_nametoindex(ifa.ifa_name)),
				flags: cFlagsToNetFlags(uint32(ifa.ifa_flags)),
			}
			ifMap[name] = info
		}

		if ifa.ifa_addr == nil {
			continue
		}

		family := ifa.ifa_addr.sa_family
		switch family {
		case C.AF_INET:
			sa := (*C.struct_sockaddr_in)(unsafe.Pointer(ifa.ifa_addr))
			ip := C.GoBytes(unsafe.Pointer(&sa.sin_addr), 4)

			var mask net.IPMask
			if ifa.ifa_netmask != nil {
				sm := (*C.struct_sockaddr_in)(unsafe.Pointer(ifa.ifa_netmask))
				mask = net.IPMask(C.GoBytes(unsafe.Pointer(&sm.sin_addr), 4))
			} else {
				mask = net.CIDRMask(24, 32)
			}

			info.addrs = append(info.addrs, &net.IPNet{
				IP:   net.IP(ip),
				Mask: mask,
			})

		case C.AF_INET6:
			sa := (*C.struct_sockaddr_in6)(unsafe.Pointer(ifa.ifa_addr))
			ip := C.GoBytes(unsafe.Pointer(&sa.sin6_addr), 16)

			var mask net.IPMask
			if ifa.ifa_netmask != nil {
				sm := (*C.struct_sockaddr_in6)(unsafe.Pointer(ifa.ifa_netmask))
				mask = net.IPMask(C.GoBytes(unsafe.Pointer(&sm.sin6_addr), 16))
			} else {
				mask = net.CIDRMask(64, 128)
			}

			info.addrs = append(info.addrs, &net.IPNet{
				IP:   net.IP(ip),
				Mask: mask,
			})
		}
	}

	// Build transport.Interface list.
	ifs := make([]*transport.Interface, 0, len(ifMap))
	for name, info := range ifMap {
		ifc := transport.NewInterface(net.Interface{
			Index: info.index,
			MTU:   1500, // getifaddrs doesn't provide MTU; use safe default
			Name:  name,
			Flags: info.flags,
		})

		for _, addr := range info.addrs {
			ifc.AddAddress(addr)
		}

		ifs = append(ifs, ifc)
	}

	return ifs, nil
}

// cFlagsToNetFlags converts C IFF_* flags to Go net.Flags.
func cFlagsToNetFlags(raw uint32) net.Flags {
	var f net.Flags
	if raw&C.IFF_UP != 0 {
		f |= net.FlagUp
	}
	if raw&C.IFF_BROADCAST != 0 {
		f |= net.FlagBroadcast
	}
	if raw&C.IFF_LOOPBACK != 0 {
		f |= net.FlagLoopback
	}
	if raw&C.IFF_POINTOPOINT != 0 {
		f |= net.FlagPointToPoint
	}
	if raw&C.IFF_RUNNING != 0 {
		f |= net.FlagRunning
	}
	if raw&C.IFF_MULTICAST != 0 {
		f |= net.FlagMulticast
	}
	return f
}
