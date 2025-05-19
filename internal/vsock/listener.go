package vsock

import (
	"fmt"
	"golang.org/x/sys/unix"
	"net"
	"os"
)

type listener struct {
	file *os.File
	port uint32
}

func Listen(port uint32) (net.Listener, error) {
	fd, err := unix.Socket(unix.AF_VSOCK, unix.SOCK_STREAM, 0)
	if err != nil {
		return nil, err
	}

	file := os.NewFile(uintptr(fd), "vsock")

	if err := unix.Bind(int(file.Fd()), &unix.SockaddrVM{
		CID:  unix.VMADDR_CID_ANY,
		Port: port,
	}); err != nil {
		return nil, err
	}

	if err := unix.Listen(int(file.Fd()), unix.SOMAXCONN); err != nil {
		return nil, err
	}

	return &listener{
		file: file,
		port: port,
	}, nil
}

func (listener *listener) Accept() (net.Conn, error) {
	fd, _, err := unix.Accept(int(listener.file.Fd()))
	if err != nil {
		return nil, err
	}

	file := os.NewFile(uintptr(fd), "vsock")

	peerName, err := unix.Getpeername(int(file.Fd()))
	if err != nil {
		return nil, fmt.Errorf("failed to get a peer name for an AF_VSOCK connection %w", err)
	}

	peerNameVM, ok := peerName.(*unix.SockaddrVM)
	if !ok {
		return nil, fmt.Errorf("accepted a non-AF_VSOCK connection on an AF_VSOCK socket")
	}

	return &conn{
		file:       file,
		localPort:  listener.port,
		remotePort: peerNameVM.Port,
	}, nil
}

func (listener *listener) Addr() net.Addr {
	return &addr{port: listener.port}
}

func (listener *listener) Close() error {
	return listener.file.Close()
}
