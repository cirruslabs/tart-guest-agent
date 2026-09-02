package vdi_test

import (
	"bytes"
	"encoding/binary"
	"testing"

	"github.com/cirruslabs/tart-guest-agent/internal/spice/vdi"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestVDI_ReadOversizedChunkRejected(t *testing.T) {
	// Chunk header with Port=1, Size=0xffffffff (4GB)
	var buf bytes.Buffer
	binary.Write(&buf, binary.LittleEndian, uint32(1))
	binary.Write(&buf, binary.LittleEndian, uint32(0xffffffff))

	v := vdi.New(&buf)
	readBuf := make([]byte, 100)
	n, err := v.Read(readBuf)
	assert.Error(t, err)
	assert.Equal(t, 0, n)
	assert.Contains(t, err.Error(), "exceeds maximum allowable limit")
}

func TestVDI_ReadWriteRoundTrip(t *testing.T) {
	var buf bytes.Buffer
	v := vdi.New(&buf)

	payload := []byte("hello vdi chunk")
	n, err := v.Write(payload)
	require.NoError(t, err)
	assert.Equal(t, len(payload), n)

	readBuf := make([]byte, 100)
	rn, err := v.Read(readBuf)
	require.NoError(t, err)
	assert.Equal(t, len(payload), rn)
	assert.Equal(t, payload, readBuf[:rn])
}
