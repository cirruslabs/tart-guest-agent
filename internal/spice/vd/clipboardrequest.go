package vd

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
)

type VDAgentClipboardRequest struct {
	Selection uint8
	_         [3]uint8
	Type      uint32
}

func DecodeVDAgentClipboardRequest(r io.Reader) (*VDAgentClipboardRequest, error) {
	var vdAgentClipboardRequest VDAgentClipboardRequest

	if err := binary.Read(r, binary.LittleEndian, &vdAgentClipboardRequest); err != nil {
		return nil, err
	}

	return &vdAgentClipboardRequest, nil
}

func (vdAgentClipboardRequest VDAgentClipboardRequest) Encode() ([]byte, error) {
	buffer := &bytes.Buffer{}

	if err := binary.Write(buffer, binary.LittleEndian, &vdAgentClipboardRequest); err != nil {
		return nil, err
	}

	return buffer.Bytes(), nil
}

func (vdAgentClipboardRequest VDAgentClipboardRequest) String() string {
	return fmt.Sprintf("VDAgentClipboardRequest(selection=%d, type=%d)",
		vdAgentClipboardRequest.Selection, vdAgentClipboardRequest.Type)
}
