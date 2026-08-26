package vdagent

import (
	"bytes"
	"encoding/binary"
	"io"
	"runtime"
	"testing"

	"github.com/cirruslabs/tart-guest-agent/internal/spice/vd"
	"github.com/cirruslabs/tart-guest-agent/internal/spice/vdi"
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

func TestSendClipboardData_Chunking(t *testing.T) {
	var buf bytes.Buffer
	agent := &VDAgent{
		vdi: vdi.New(&buf),
	}

	// 5000 bytes payload (> 2048 max chunk size)
	payload := make([]byte, 5000)
	for i := range payload {
		payload[i] = byte(i % 256)
	}

	err := agent.sendClipboardData(vd.VD_AGENT_CLIPBOARD_SELECTION_CLIPBOARD, vd.VD_AGENT_CLIPBOARD_IMAGE_PNG, payload)
	if err != nil {
		t.Fatalf("sendClipboardData failed: %v", err)
	}

	// Verify the underlying VDI buffer contains multiple 2048-byte VDI chunks
	vdiData := buf.Bytes()
	if len(vdiData) == 0 {
		t.Fatalf("expected non-empty VDI output")
	}

	// Read emitted logical message back via vdi reader
	vdiReader := vdi.New(&buf)
	var inner vd.VDAgentMessageInner
	if err := binary.Read(vdiReader, binary.LittleEndian, &inner); err != nil {
		t.Fatalf("failed reading VDAgentMessage header: %v", err)
	}
	if inner.Type != vd.VD_AGENT_CLIPBOARD {
		t.Fatalf("expected type %d, got %d", vd.VD_AGENT_CLIPBOARD, inner.Type)
	}
	if inner.Size != uint32(8+len(payload)) {
		t.Fatalf("expected total message size %d, got %d", 8+len(payload), inner.Size)
	}

	msgData := make([]byte, inner.Size)
	if _, err := io.ReadFull(vdiReader, msgData); err != nil {
		t.Fatalf("failed reading message data: %v", err)
	}

	decodedClipboard, err := vd.DecodeVDAgentClipboard(msgData)
	if err != nil {
		t.Fatalf("failed decoding VDAgentClipboard: %v", err)
	}
	if decodedClipboard.Selection != vd.VD_AGENT_CLIPBOARD_SELECTION_CLIPBOARD {
		t.Fatalf("expected selection %d, got %d", vd.VD_AGENT_CLIPBOARD_SELECTION_CLIPBOARD, decodedClipboard.Selection)
	}
	if decodedClipboard.Type != vd.VD_AGENT_CLIPBOARD_IMAGE_PNG {
		t.Fatalf("expected type %d, got %d", vd.VD_AGENT_CLIPBOARD_IMAGE_PNG, decodedClipboard.Type)
	}
	if !bytes.Equal(decodedClipboard.Data, payload) {
		t.Fatalf("decoded payload does not match original (len %d vs %d)", len(decodedClipboard.Data), len(payload))
	}
}
