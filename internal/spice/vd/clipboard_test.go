package vd_test

import (
	"bytes"
	"testing"

	"github.com/cirruslabs/tart-guest-agent/internal/spice/vd"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestClipboardTextAndImageEncoding(t *testing.T) {
	// 1. Text clipboard
	textData := []byte("Hello, Webomage & Tart!")
	textClip := vd.VDAgentClipboard{
		VDAgentClipboardInner: vd.VDAgentClipboardInner{
			Selection: vd.VD_AGENT_CLIPBOARD_SELECTION_CLIPBOARD,
			Type:      vd.VD_AGENT_CLIPBOARD_UTF8_TEXT,
		},
		Data: textData,
	}

	encodedText, err := textClip.Encode()
	require.NoError(t, err)

	decodedText, err := vd.DecodeVDAgentClipboard(encodedText)
	require.NoError(t, err)
	assert.Equal(t, uint8(vd.VD_AGENT_CLIPBOARD_SELECTION_CLIPBOARD), decodedText.Selection)
	assert.Equal(t, uint32(vd.VD_AGENT_CLIPBOARD_UTF8_TEXT), decodedText.Type)
	assert.Equal(t, textData, decodedText.Data)

	// 2. Image PNG clipboard
	fakePngHeader := []byte{0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A, 0x00, 0x00, 0x00, 0x0D}
	imageClip := vd.VDAgentClipboard{
		VDAgentClipboardInner: vd.VDAgentClipboardInner{
			Selection: vd.VD_AGENT_CLIPBOARD_SELECTION_CLIPBOARD,
			Type:      vd.VD_AGENT_CLIPBOARD_IMAGE_PNG,
		},
		Data: fakePngHeader,
	}

	encodedImg, err := imageClip.Encode()
	require.NoError(t, err)

	decodedImg, err := vd.DecodeVDAgentClipboard(encodedImg)
	require.NoError(t, err)
	assert.Equal(t, uint8(vd.VD_AGENT_CLIPBOARD_SELECTION_CLIPBOARD), decodedImg.Selection)
	assert.Equal(t, uint32(vd.VD_AGENT_CLIPBOARD_IMAGE_PNG), decodedImg.Type)
	assert.Equal(t, fakePngHeader, decodedImg.Data)
}

func TestClipboardGrabEncoding(t *testing.T) {
	grab := vd.VDAgentClipboardGrab{
		Selection: vd.VD_AGENT_CLIPBOARD_SELECTION_CLIPBOARD,
		Types:     []uint32{vd.VD_AGENT_CLIPBOARD_IMAGE_PNG, vd.VD_AGENT_CLIPBOARD_UTF8_TEXT},
	}

	encoded, err := grab.Encode()
	require.NoError(t, err)

	decoded, err := vd.DecodeVDAgentClipboardGrab(encoded)
	require.NoError(t, err)
	assert.Equal(t, uint8(vd.VD_AGENT_CLIPBOARD_SELECTION_CLIPBOARD), decoded.Selection)
	assert.Equal(t, []uint32{vd.VD_AGENT_CLIPBOARD_IMAGE_PNG, vd.VD_AGENT_CLIPBOARD_UTF8_TEXT}, decoded.Types)
}

func TestClipboardRequestEncoding(t *testing.T) {
	req := vd.VDAgentClipboardRequest{
		Selection: vd.VD_AGENT_CLIPBOARD_SELECTION_CLIPBOARD,
		Type:      vd.VD_AGENT_CLIPBOARD_IMAGE_PNG,
	}

	encoded, err := req.Encode()
	require.NoError(t, err)

	decoded, err := vd.DecodeVDAgentClipboardRequest(bytes.NewReader(encoded))
	require.NoError(t, err)
	assert.Equal(t, uint8(vd.VD_AGENT_CLIPBOARD_SELECTION_CLIPBOARD), decoded.Selection)
	assert.Equal(t, uint32(vd.VD_AGENT_CLIPBOARD_IMAGE_PNG), decoded.Type)
}

func TestClipboardReleaseEncoding(t *testing.T) {
	rel := vd.VDAgentClipboardRelease{
		Selection: vd.VD_AGENT_CLIPBOARD_SELECTION_CLIPBOARD,
	}

	encoded, err := rel.Encode()
	require.NoError(t, err)

	decoded, err := vd.DecodeVDAgentClipboardRelease(bytes.NewReader(encoded))
	require.NoError(t, err)
	assert.Equal(t, uint8(vd.VD_AGENT_CLIPBOARD_SELECTION_CLIPBOARD), decoded.Selection)
	assert.Contains(t, rel.String(), "selection=0")
}

func TestFileXferStart_StandardBinarySize(t *testing.T) {
	// Standard SPICE start message: [id: 4 bytes][size: 8 bytes][filename: NUL-terminated string]
	raw := []byte{
		0x05, 0x00, 0x00, 0x00, // ID = 5
		0x20, 0x10, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, // Size = 0x1020 (4128 bytes)
		'a', 'r', 'c', 'h', 'i', 'v', 'e', '.', 't', 'a', 'r', 0x00, // Filename = "archive.tar"
	}

	msg, err := vd.DecodeVDAgentFileXferStart(raw)
	require.NoError(t, err)
	assert.Equal(t, uint32(5), msg.ID)
	assert.Equal(t, uint64(4128), msg.FileSize)
	assert.Equal(t, "archive.tar\x00", string(msg.Data))
}

func TestFileXferStart_IniFormat(t *testing.T) {
	// INI-style metadata: [id: 4 bytes][ini text]
	raw := []byte{
		0x06, 0x00, 0x00, 0x00, // ID = 6
		'[', 'v', 'd', 'a', 'g', 'e', 'n', 't', '-', 'f', 'i', 'l', 'e', '-', 'x', 'f', 'e', 'r', ']', '\n',
		'n', 'a', 'm', 'e', '=', 'd', 'o', 'c', '.', 'p', 'd', 'f', '\n',
		's', 'i', 'z', 'e', '=', '1', '0', '2', '4', '\n',
	}

	msg, err := vd.DecodeVDAgentFileXferStart(raw)
	require.NoError(t, err)
	assert.Equal(t, uint32(6), msg.ID)
	assert.Equal(t, uint64(0), msg.FileSize) // parsed in manager parseMetadata
	assert.Contains(t, string(msg.Data), "name=doc.pdf")
}

func TestFileXferStart_BareFilename_Short(t *testing.T) {
	// Bare filename < 8 bytes: "doc.txt\0"
	raw := []byte{
		0x07, 0x00, 0x00, 0x00, // ID = 7
		'd', 'o', 'c', '.', 't', 'x', 't', 0x00,
	}

	msg, err := vd.DecodeVDAgentFileXferStart(raw)
	require.NoError(t, err)
	assert.Equal(t, uint32(7), msg.ID)
	assert.Equal(t, uint64(0), msg.FileSize)
	assert.Equal(t, "doc.txt\x00", string(msg.Data))
}

func TestFileXferStart_KeyValue_SizeLess(t *testing.T) {
	// Key-value without bracket header: "name=report.pdf\n"
	raw := []byte{
		0x08, 0x00, 0x00, 0x00, // ID = 8
		'n', 'a', 'm', 'e', '=', 'r', 'e', 'p', 'o', 'r', 't', '.', 'p', 'd', 'f', '\n',
	}

	msg, err := vd.DecodeVDAgentFileXferStart(raw)
	require.NoError(t, err)
	assert.Equal(t, uint32(8), msg.ID)
	assert.Equal(t, uint64(0), msg.FileSize)
	assert.Equal(t, "name=report.pdf\n", string(msg.Data))
}

func TestFileXferStart_BinarySizeWithNewlineByte(t *testing.T) {
	// Size = 10 (0x0a), which contains byte 0x0a ('\n')
	raw := []byte{
		0x09, 0x00, 0x00, 0x00, // ID = 9
		0x0a, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, // Size = 10
		't', 'e', 's', 't', '.', 't', 'x', 't', 0x00,
	}

	msg, err := vd.DecodeVDAgentFileXferStart(raw)
	require.NoError(t, err)
	assert.Equal(t, uint32(9), msg.ID)
	assert.Equal(t, uint64(10), msg.FileSize)
	assert.Equal(t, "test.txt\x00", string(msg.Data))
}
