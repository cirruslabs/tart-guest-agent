//go:build windows

package vsock

import (
	"fmt"
	"net"
)

func Listen(port uint32) (net.Listener, error) {
	return net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
}
