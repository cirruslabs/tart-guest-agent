package vsock

import (
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

	return &conn{
		file:       os.NewFile(uintptr(fd), "vsock"),
		localPort:  listener.port,
		remotePort: 0,
	}, nil
}

func (listener *listener) Addr() net.Addr {
	return &addr{port: listener.port}
}

func (listener *listener) Close() error {
	return listener.file.Close()
}
