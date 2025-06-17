package rpc

import (
	"context"
	"fmt"
	"net"
)

func (rpc *RPC) ResolveIP(ctx context.Context, _ *ResolveIPRequest) (*ResolveIPResponse, error) {
	ifaceAddrs, err := net.InterfaceAddrs()
	if err != nil {
		return nil, fmt.Errorf("failed to retrieve interface addresses: %w", err)
	}

	for _, ifaceAddr := range ifaceAddrs {
		// Addresses returned by net.InterfaceAddrs()
		// generally are of type *net.IPNet
		ipNet, ok := ifaceAddr.(*net.IPNet)
		if !ok {
			continue
		}

		// Only interested in IPv4 addresses
		if ipNet.IP.To4() == nil {
			continue
		}

		// Only interested in global unicast addresses
		//
		// Note that Golang's "net" package also includes
		// IPv4 private address space in this definition.
		if !ipNet.IP.IsGlobalUnicast() {
			continue
		}

		return &ResolveIPResponse{
			Ip: ipNet.IP.String(),
		}, nil
	}

	return nil, fmt.Errorf("cannot identify VMs IP address")
}
