package vsock

import (
	"net"
	"os"
	"time"
)

type conn struct {
	file       *os.File
	localPort  uint32
	remotePort uint32
}

func (conn *conn) Read(b []byte) (n int, err error) {
	return conn.file.Read(b)
}

func (conn *conn) Write(b []byte) (n int, err error) {
	return conn.file.Write(b)
}

func (conn *conn) SetDeadline(t time.Time) error {
	return conn.file.SetDeadline(t)
}

func (conn *conn) SetReadDeadline(t time.Time) error {
	return conn.file.SetReadDeadline(t)
}

func (conn *conn) SetWriteDeadline(t time.Time) error {
	return conn.file.SetWriteDeadline(t)
}

func (conn *conn) LocalAddr() net.Addr {
	return &addr{port: conn.localPort}
}

func (conn *conn) RemoteAddr() net.Addr {
	return &addr{port: conn.remotePort}
}

func (conn *conn) Close() error {
	return conn.file.Close()
}
