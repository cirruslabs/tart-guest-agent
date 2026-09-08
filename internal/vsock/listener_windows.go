//go:build windows

package vsock

import (
	"fmt"
	"net"
)

func Listen(port uint32) (net.Listener, error) {
	return nil, fmt.Errorf("AF_VSOCK is not supported on Windows")
}

