package vd

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
)

type VDAgentClipboard struct {
	VDAgentClipboardInner
	Data []byte
}

type VDAgentClipboardInner struct {
	Selection uint8
	_         [3]uint8
	Type      uint32
}

func DecodeVDAgentClipboard(buf []byte) (*VDAgentClipboard, error) {
	var vdAgentClipboard VDAgentClipboard

	r := bufio.NewReader(bytes.NewReader(buf))

	err := binary.Read(r, binary.LittleEndian, &vdAgentClipboard.VDAgentClipboardInner)
	if err != nil {
		return nil, err
	}

	vdAgentClipboard.Data, err = io.ReadAll(r)
	if err != nil {
		return nil, err
	}

	return &vdAgentClipboard, nil
}

func (vdAgentClipboard VDAgentClipboard) Encode() ([]byte, error) {
	buffer := &bytes.Buffer{}

	if err := binary.Write(buffer, binary.LittleEndian, &vdAgentClipboard.VDAgentClipboardInner); err != nil {
		return nil, err
	}

	if _, err := buffer.Write(vdAgentClipboard.Data); err != nil {
		return nil, err
	}

	return buffer.Bytes(), nil
}

func (vdAgentClipboard VDAgentClipboard) String() string {
	return fmt.Sprintf("VDAgentClipboard(selection=%d, type=%d, data=%d bytes)",
		vdAgentClipboard.Selection, vdAgentClipboard.Type, len(vdAgentClipboard.Data))
}
