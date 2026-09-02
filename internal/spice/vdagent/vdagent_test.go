package vdagent

import (
	"bytes"
	"encoding/binary"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/cirruslabs/tart-guest-agent/internal/settings"
	"github.com/cirruslabs/tart-guest-agent/internal/spice/vd"
	"github.com/cirruslabs/tart-guest-agent/internal/spice/vdi"
	"golang.design/x/clipboard"
)

func TestFindSerialPortPath(t *testing.T) {
	path := FindSerialPortPath()
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
			name:         "PNG prioritized over uncompressed TIFF/BMP when TIFF is first",
			types:        []uint32{vd.VD_AGENT_CLIPBOARD_IMAGE_TIFF, vd.VD_AGENT_CLIPBOARD_IMAGE_BMP, vd.VD_AGENT_CLIPBOARD_IMAGE_PNG},
			expectedType: vd.VD_AGENT_CLIPBOARD_IMAGE_PNG,
			expectedOK:   true,
		},
		{
			name:         "single TIFF fallback when PNG not listed",
			types:        []uint32{vd.VD_AGENT_CLIPBOARD_IMAGE_TIFF},
			expectedType: vd.VD_AGENT_CLIPBOARD_IMAGE_TIFF,
			expectedOK:   true,
		},
		{
			name:         "single BMP fallback when PNG not listed",
			types:        []uint32{vd.VD_AGENT_CLIPBOARD_IMAGE_BMP},
			expectedType: vd.VD_AGENT_CLIPBOARD_IMAGE_BMP,
			expectedOK:   true,
		},
		{
			name:         "TIFF image prioritized over UTF8_TEXT when PNG not listed",
			types:        []uint32{vd.VD_AGENT_CLIPBOARD_UTF8_TEXT, vd.VD_AGENT_CLIPBOARD_IMAGE_TIFF},
			expectedType: vd.VD_AGENT_CLIPBOARD_IMAGE_TIFF,
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

func TestGetAvailableGrabTypes(t *testing.T) {
	// Only valid text
	types := getAvailableGrabTypes([]clipboard.Format{clipboard.FmtText}, false, true)
	if len(types) != 1 || types[0] != vd.VD_AGENT_CLIPBOARD_UTF8_TEXT {
		t.Fatalf("expected [UTF8_TEXT], got %v", types)
	}

	// Empty/invalid text (e.g. after release)
	types = getAvailableGrabTypes([]clipboard.Format{clipboard.FmtText}, false, false)
	if len(types) != 0 {
		t.Fatalf("expected empty types for empty text, got %v", types)
	}

	// Valid image format
	types = getAvailableGrabTypes([]clipboard.Format{clipboard.FmtImage}, true, false)
	if len(types) != 1 || types[0] != vd.VD_AGENT_CLIPBOARD_IMAGE_PNG {
		t.Fatalf("expected [IMAGE_PNG], got %v", types)
	}

	// Invalid/unservable image format
	types = getAvailableGrabTypes([]clipboard.Format{clipboard.FmtImage}, false, false)
	if len(types) != 0 {
		t.Fatalf("expected empty types for invalid image, got %v", types)
	}

	// Both valid image and valid text
	types = getAvailableGrabTypes([]clipboard.Format{clipboard.FmtImage, clipboard.FmtText}, true, true)
	if len(types) != 2 || types[0] != vd.VD_AGENT_CLIPBOARD_IMAGE_PNG || types[1] != vd.VD_AGENT_CLIPBOARD_UTF8_TEXT {
		t.Fatalf("expected [IMAGE_PNG, UTF8_TEXT], got %v", types)
	}

	// Neither image nor text available (e.g. cleared clipboard)
	types = getAvailableGrabTypes(nil, false, false)
	if len(types) != 0 {
		t.Fatalf("expected empty types when no formats available, got %v", types)
	}
}

func TestHasServableClipboardFormat(t *testing.T) {
	// Empty formats
	if hasServableClipboardFormat(nil) {
		t.Fatalf("expected nil formats to return false")
	}
	if hasServableClipboardFormat([]clipboard.Format{}) {
		t.Fatalf("expected empty formats to return false")
	}

	// Text format present
	if !hasServableClipboardFormat([]clipboard.Format{clipboard.FmtText}) {
		t.Fatalf("expected FmtText to return true")
	}

	// Image format present
	if !hasServableClipboardFormat([]clipboard.Format{clipboard.FmtImage}) {
		t.Fatalf("expected FmtImage to return true")
	}

	// Unsupported custom / file format (e.g. format 3)
	customFormat := clipboard.Format(99)
	if hasServableClipboardFormat([]clipboard.Format{customFormat}) {
		t.Fatalf("expected custom unsupported format to return false")
	}

	// Custom format alongside text
	if !hasServableClipboardFormat([]clipboard.Format{customFormat, clipboard.FmtText}) {
		t.Fatalf("expected custom format + FmtText to return true")
	}
}

func TestFindRunningClipboardManagers(t *testing.T) {
	// Must run without panicking and return a slice
	managers := FindRunningClipboardManagers()
	_ = managers

	// Validate known managers definitions
	if len(KnownClipboardManagers) == 0 {
		t.Fatalf("expected non-empty KnownClipboardManagers list")
	}
	for _, mgr := range KnownClipboardManagers {
		if mgr.Name == "" || mgr.ProcessName == "" || mgr.Description == "" {
			t.Fatalf("invalid KnownClipboardManager entry: %+v", mgr)
		}
	}
}

type failingWriter struct{}

func (f *failingWriter) Write(p []byte) (n int, err error) {
	return 0, io.ErrClosedPipe
}

func (f *failingWriter) Read(p []byte) (n int, err error) {
	return 0, io.EOF
}

func TestProcessClipboardState_WriteFailureDoesNotCorruptState(t *testing.T) {
	agent := &VDAgent{
		vdi: vdi.New(&failingWriter{}),
	}

	text := []byte("hello world")
	err := agent.processClipboardState(text, vd.VD_AGENT_CLIPBOARD_UTF8_TEXT)
	if err == nil {
		t.Fatalf("expected error from failing writer")
	}

	agent.clipMu.Lock()
	defer agent.clipMu.Unlock()

	// Memory state must remain uncommitted if writeMessage fails
	if agent.lastClipboardState != nil {
		t.Fatalf("expected lastClipboardState to remain nil on write failure, got %q", agent.lastClipboardState)
	}
	if agent.lastAdvertisedTypes != nil {
		t.Fatalf("expected lastAdvertisedTypes to remain nil on write failure, got %v", agent.lastAdvertisedTypes)
	}
}

func TestProcessClipboardState_SuccessfulWriteCommitsState(t *testing.T) {
	var buf bytes.Buffer
	agent := &VDAgent{
		vdi: vdi.New(&buf),
	}

	text := []byte("test grab")
	err := agent.processClipboardState(text, vd.VD_AGENT_CLIPBOARD_UTF8_TEXT)
	if err != nil {
		t.Fatalf("processClipboardState failed: %v", err)
	}

	agent.clipMu.Lock()
	defer agent.clipMu.Unlock()

	if !bytes.Equal(agent.lastClipboardState, text) {
		t.Fatalf("expected lastClipboardState to be %q, got %q", text, agent.lastClipboardState)
	}
	if len(agent.lastAdvertisedTypes) == 0 {
		t.Fatalf("expected lastAdvertisedTypes to be non-empty")
	}
}

func TestSelectGrabRequestType_ImageDisabled(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "settings_test_*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	tempCfg := filepath.Join(tempDir, "settings.json")
	oldCfg := os.Getenv("TART_GUEST_CONFIG")
	os.Setenv("TART_GUEST_CONFIG", tempCfg)
	defer func() {
		os.Setenv("TART_GUEST_CONFIG", oldCfg)
		settings.Reset()
	}()

	s := settings.DefaultSettings()
	s.ImageClipboardEnabled = false
	if err := settings.Save(s); err != nil {
		t.Fatalf("failed to save settings: %v", err)
	}

	types := []uint32{vd.VD_AGENT_CLIPBOARD_IMAGE_PNG, vd.VD_AGENT_CLIPBOARD_UTF8_TEXT}
	selected, ok := selectGrabRequestType(types)
	if !ok || selected != vd.VD_AGENT_CLIPBOARD_UTF8_TEXT {
		t.Fatalf("expected selectGrabRequestType to choose UTF8_TEXT when image is disabled, got %d (ok=%v)", selected, ok)
	}

	imageOnly := []uint32{vd.VD_AGENT_CLIPBOARD_IMAGE_PNG}
	_, okImageOnly := selectGrabRequestType(imageOnly)
	if okImageOnly {
		t.Fatalf("expected selectGrabRequestType to return false for image-only grab when image clipboard is disabled")
	}
}

func TestGetAvailableGrabTypes_ImageDisabled(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "settings_test_*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	tempCfg := filepath.Join(tempDir, "settings.json")
	oldCfg := os.Getenv("TART_GUEST_CONFIG")
	os.Setenv("TART_GUEST_CONFIG", tempCfg)
	defer func() {
		os.Setenv("TART_GUEST_CONFIG", oldCfg)
		settings.Reset()
	}()

	s := settings.DefaultSettings()
	s.ImageClipboardEnabled = false
	if err := settings.Save(s); err != nil {
		t.Fatalf("failed to save settings: %v", err)
	}

	formats := []clipboard.Format{clipboard.FmtImage, clipboard.FmtText}
	grabTypes := getAvailableGrabTypes(formats, true, true)
	if len(grabTypes) != 1 || grabTypes[0] != vd.VD_AGENT_CLIPBOARD_UTF8_TEXT {
		t.Fatalf("expected getAvailableGrabTypes to omit PNG when image is disabled, got %v", grabTypes)
	}
}

func TestVDAgent_ReadMessage_OversizedPayloadRejected(t *testing.T) {
	// Craft header with inner.Size = 0xffffffff (4GB)
	var buf bytes.Buffer
	binary.Write(&buf, binary.LittleEndian, vd.VD_AGENT_PROTOCOL)
	binary.Write(&buf, binary.LittleEndian, vd.VD_AGENT_CLIPBOARD)
	binary.Write(&buf, binary.LittleEndian, uint32(0)) // opaque
	binary.Write(&buf, binary.LittleEndian, uint32(0xffffffff)) // 4GB size

	agent := &VDAgent{
		vdi: vdi.New(&buf),
	}

	msg, err := agent.readMessage()
	if err == nil {
		t.Fatalf("expected error for oversized message, got message: %v", msg)
	}
	if msg != nil {
		t.Fatalf("expected nil message when size is rejected")
	}
}
