package vd

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
)

type VDAgentClipboardGrab struct {
	Selection uint8
	_         [3]uint8
	Type      uint32
}

func DecodeVDAgentClipboardGrab(r io.Reader) (*VDAgentClipboardGrab, error) {
	var vdAgentClipboardGrab VDAgentClipboardGrab

	if err := binary.Read(r, binary.LittleEndian, &vdAgentClipboardGrab); err != nil {
		return nil, err
	}

	return &vdAgentClipboardGrab, nil
}

func (vdAgentClipboardGrab VDAgentClipboardGrab) Encode() ([]byte, error) {
	buffer := &bytes.Buffer{}

	if err := binary.Write(buffer, binary.LittleEndian, &vdAgentClipboardGrab); err != nil {
		return nil, err
	}

	return buffer.Bytes(), nil
}

func (vdAgentClipboardGrab VDAgentClipboardGrab) String() string {
	return fmt.Sprintf("VDAgentClipboardGrab(selection=%d, type=%d)",
		vdAgentClipboardGrab.Selection, vdAgentClipboardGrab.Type)
}
