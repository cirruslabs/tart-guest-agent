package vd_test

import (
	"encoding/binary"
	"io"
	"testing"

	"github.com/cirruslabs/tart-guest-agent/internal/spice/vd"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDecodeVDAgentFileXferData_ValidChunk(t *testing.T) {
	payload := []byte("hello world")
	msg := vd.VDAgentFileXferData{
		ID:   42,
		Size: uint64(len(payload)),
		Data: payload,
	}

	encoded, err := msg.Encode()
	require.NoError(t, err)

	decoded, err := vd.DecodeVDAgentFileXferData(encoded)
	require.NoError(t, err)
	assert.Equal(t, uint32(42), decoded.ID)
	assert.Equal(t, uint64(len(payload)), decoded.Size)
	assert.Equal(t, payload, decoded.Data)
}

func TestDecodeVDAgentFileXferData_ValidEOF(t *testing.T) {
	msg := vd.VDAgentFileXferData{
		ID:   101,
		Size: 0,
		Data: nil,
	}

	encoded, err := msg.Encode()
	require.NoError(t, err)

	decoded, err := vd.DecodeVDAgentFileXferData(encoded)
	require.NoError(t, err)
	assert.Equal(t, uint32(101), decoded.ID)
	assert.Equal(t, uint64(0), decoded.Size)
	assert.Empty(t, decoded.Data)
}

func TestDecodeVDAgentFileXferData_BufferTooShort(t *testing.T) {
	shortBuf := []byte{1, 2, 3, 4, 5}
	_, err := vd.DecodeVDAgentFileXferData(shortBuf)
	require.ErrorIs(t, err, io.ErrUnexpectedEOF)
}

func TestDecodeVDAgentFileXferData_SizeMismatch_Truncated(t *testing.T) {
	// Header declares Size=100, but only 5 bytes of data are attached
	buf := make([]byte, 12+5)
	binary.LittleEndian.PutUint32(buf[:4], 55)
	binary.LittleEndian.PutUint64(buf[4:12], 100)
	copy(buf[12:], []byte("short"))

	decoded, err := vd.DecodeVDAgentFileXferData(buf)
	assert.Nil(t, decoded)
	require.Error(t, err)
	require.ErrorIs(t, err, io.ErrUnexpectedEOF)
	assert.Contains(t, err.Error(), "filexfer data truncated")
}

func TestDecodeVDAgentFileXferData_SizeMismatch_Overflow(t *testing.T) {
	// Header declares Size=1, but 20 bytes of data are attached
	payload := []byte("12345678901234567890")
	buf := make([]byte, 12+len(payload))
	binary.LittleEndian.PutUint32(buf[:4], 77)
	binary.LittleEndian.PutUint64(buf[4:12], 1)
	copy(buf[12:], payload)

	decoded, err := vd.DecodeVDAgentFileXferData(buf)
	assert.Nil(t, decoded)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "filexfer data overflow")
}

func TestDecodeVDAgentFileXferData_SizeMismatch_ZeroWithPayload(t *testing.T) {
	// Header declares Size=0, but 10 bytes of data are attached
	payload := []byte("extra_data")
	buf := make([]byte, 12+len(payload))
	binary.LittleEndian.PutUint32(buf[:4], 88)
	binary.LittleEndian.PutUint64(buf[4:12], 0)
	copy(buf[12:], payload)

	decoded, err := vd.DecodeVDAgentFileXferData(buf)
	assert.Nil(t, decoded)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "filexfer data overflow")
}
