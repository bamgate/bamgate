//go:build !android

package agent

import (
	"net"

	transport "github.com/pion/transport/v4"
)

// platformInterfaces returns the system's network interfaces using Go's
// standard net package. This works on Linux (non-Android) and macOS.
func platformInterfaces() ([]*transport.Interface, error) {
	oifs, err := net.Interfaces()
	if err != nil {
		return nil, err
	}

	ifs := make([]*transport.Interface, 0, len(oifs))
	for i := range oifs {
		ifc := transport.NewInterface(oifs[i])

		addrs, err := oifs[i].Addrs()
		if err != nil {
			return nil, err
		}
		for _, addr := range addrs {
			ifc.AddAddress(addr)
		}

		ifs = append(ifs, ifc)
	}
	return ifs, nil
}
