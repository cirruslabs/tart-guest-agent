package vd

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
)

type VDAgentClipboardRelease struct {
	Selection uint8
	_         [3]uint8
}

func DecodeVDAgentClipboardRelease(r io.Reader) (*VDAgentClipboardRelease, error) {
	var vdAgentClipboardRelease VDAgentClipboardRelease

	if err := binary.Read(r, binary.LittleEndian, &vdAgentClipboardRelease); err != nil {
		return nil, err
	}

	return &vdAgentClipboardRelease, nil
}

func (vdAgentClipboardRelease VDAgentClipboardRelease) Encode() ([]byte, error) {
	buffer := &bytes.Buffer{}

	if err := binary.Write(buffer, binary.LittleEndian, &vdAgentClipboardRelease); err != nil {
		return nil, err
	}

	return buffer.Bytes(), nil
}

func (vdAgentClipboardRelease VDAgentClipboardRelease) String() string {
	return fmt.Sprintf("VDAgentClipboardRelease(selection=%d)", vdAgentClipboardRelease.Selection)
}
