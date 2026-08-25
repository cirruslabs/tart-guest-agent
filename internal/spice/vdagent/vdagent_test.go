package vdagent

import (
	"runtime"
	"testing"
)

func TestFindSerialPortPath(t *testing.T) {
	path := findSerialPortPath()
	if path == "" {
		t.Fatalf("expected non-empty serial port path")
	}

	if runtime.GOOS == "linux" && path != "/dev/virtio-ports/com.redhat.spice.0" && path != "/dev/tty.com.redhat.spice.0" {
		t.Logf("discovered Linux serial port path: %s", path)
	}

	if runtime.GOOS == "darwin" && path != "/dev/tty.com.redhat.spice.0" && path != "/dev/cu.com.redhat.spice.0" {
		t.Logf("discovered Darwin serial port path: %s", path)
	}
}
