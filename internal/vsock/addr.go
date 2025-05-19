package vsock

import "fmt"

type addr struct {
	port uint32
}

func (addr *addr) Network() string {
	return "vsock"
}

func (addr *addr) String() string {
	return fmt.Sprintf("%d", addr.port)
}
