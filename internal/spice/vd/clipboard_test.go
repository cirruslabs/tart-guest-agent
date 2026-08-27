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
