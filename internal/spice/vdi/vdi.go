package vdi

import (
	"bytes"
	"encoding/binary"
	"github.com/cirruslabs/tart-guest-agent/internal/spice/vd"
	"io"
)

type VDI struct {
	inner     io.ReadWriter
	remaining uint64
}

type chunkHeader struct {
	Port uint32
	Size uint32
}

func New(inner io.ReadWriter) *VDI {
	return &VDI{
		inner: inner,
	}
}

func (vdi *VDI) Read(buf []byte) (int, error) {
	// Read payload
readPayload:
	if vdi.remaining > 0 {
		toRead := min(len(buf), int(vdi.remaining))

		n, err := vdi.inner.Read(buf[:toRead])
		if err != nil {
			return 0, err
		}

		vdi.remaining -= uint64(n)

		return n, nil
	}

	// Read header
	var vdiChunkHeader chunkHeader

	if err := binary.Read(vdi.inner, binary.LittleEndian, &vdiChunkHeader); err != nil {
		return 0, err
	}

	vdi.remaining = uint64(vdiChunkHeader.Size)

	goto readPayload
}

func (vdi *VDI) Write(buf []byte) (int, error) {
	// Write header
	buffer := &bytes.Buffer{}

	vdiChunkHeader := chunkHeader{
		Port: vd.VDP_CLIENT_PORT,
		Size: uint32(len(buf)),
	}
	if err := binary.Write(buffer, binary.LittleEndian, &vdiChunkHeader); err != nil {
		return 0, err
	}

	// Write payload
	return vdi.inner.Write(append(buffer.Bytes(), buf...))
}
