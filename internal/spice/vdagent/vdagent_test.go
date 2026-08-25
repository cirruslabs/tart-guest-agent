package vdagent

import (
	"runtime"
	"testing"

	"github.com/cirruslabs/tart-guest-agent/internal/spice/vd"
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

func TestSelectGrabRequestType(t *testing.T) {
	tests := []struct {
		name     string
		types    []uint32
		expected uint32
	}{
		{
			name:     "empty types defaults to UTF8_TEXT",
			types:    []uint32{},
			expected: vd.VD_AGENT_CLIPBOARD_UTF8_TEXT,
		},
		{
			name:     "single UTF8_TEXT",
			types:    []uint32{vd.VD_AGENT_CLIPBOARD_UTF8_TEXT},
			expected: vd.VD_AGENT_CLIPBOARD_UTF8_TEXT,
		},
		{
			name:     "single PNG",
			types:    []uint32{vd.VD_AGENT_CLIPBOARD_IMAGE_PNG},
			expected: vd.VD_AGENT_CLIPBOARD_IMAGE_PNG,
		},
		{
			name:     "image prioritized over UTF8_TEXT when text is first",
			types:    []uint32{vd.VD_AGENT_CLIPBOARD_UTF8_TEXT, vd.VD_AGENT_CLIPBOARD_IMAGE_PNG},
			expected: vd.VD_AGENT_CLIPBOARD_IMAGE_PNG,
		},
		{
			name:     "UTF8_TEXT selected when preceded by unsupported extension type",
			types:    []uint32{999 /* unsupported format */, vd.VD_AGENT_CLIPBOARD_UTF8_TEXT},
			expected: vd.VD_AGENT_CLIPBOARD_UTF8_TEXT,
		},
		{
			name:     "unknown types fallback to first entry",
			types:    []uint32{888, 777},
			expected: 888,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			actual := selectGrabRequestType(tc.types)
			if actual != tc.expected {
				t.Fatalf("expected type %d, got %d", tc.expected, actual)
			}
		})
	}
}
