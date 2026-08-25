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
		name         string
		types        []uint32
		expectedType uint32
		expectedOK   bool
	}{
		{
			name:         "empty types returns false",
			types:        []uint32{},
			expectedType: 0,
			expectedOK:   false,
		},
		{
			name:         "unsupported file list type returns false",
			types:        []uint32{vd.VD_AGENT_CLIPBOARD_FILE_LIST},
			expectedType: 0,
			expectedOK:   false,
		},
		{
			name:         "single UTF8_TEXT",
			types:        []uint32{vd.VD_AGENT_CLIPBOARD_UTF8_TEXT},
			expectedType: vd.VD_AGENT_CLIPBOARD_UTF8_TEXT,
			expectedOK:   true,
		},
		{
			name:         "single PNG",
			types:        []uint32{vd.VD_AGENT_CLIPBOARD_IMAGE_PNG},
			expectedType: vd.VD_AGENT_CLIPBOARD_IMAGE_PNG,
			expectedOK:   true,
		},
		{
			name:         "image prioritized over UTF8_TEXT when text is first",
			types:        []uint32{vd.VD_AGENT_CLIPBOARD_UTF8_TEXT, vd.VD_AGENT_CLIPBOARD_IMAGE_PNG},
			expectedType: vd.VD_AGENT_CLIPBOARD_IMAGE_PNG,
			expectedOK:   true,
		},
		{
			name:         "UTF8_TEXT selected when preceded by unsupported extension type",
			types:        []uint32{999 /* unsupported format */, vd.VD_AGENT_CLIPBOARD_UTF8_TEXT},
			expectedType: vd.VD_AGENT_CLIPBOARD_UTF8_TEXT,
			expectedOK:   true,
		},
		{
			name:         "all unknown types return false",
			types:        []uint32{888, 777},
			expectedType: 0,
			expectedOK:   false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			actualType, actualOK := selectGrabRequestType(tc.types)
			if actualOK != tc.expectedOK || actualType != tc.expectedType {
				t.Fatalf("expected (%d, %v), got (%d, %v)", tc.expectedType, tc.expectedOK, actualType, actualOK)
			}
		})
	}
}
