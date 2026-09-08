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
	Types     []uint32
}

func DecodeVDAgentClipboardGrab(data []byte) (*VDAgentClipboardGrab, error) {
	if len(data) < 4 {
		return nil, io.ErrUnexpectedEOF
	}

	grab := &VDAgentClipboardGrab{
		Selection: data[0],
	}

	typesData := data[4:]
	numTypes := len(typesData) / 4
	grab.Types = make([]uint32, numTypes)
	r := bytes.NewReader(typesData)
	for i := 0; i < numTypes; i++ {
		if err := binary.Read(r, binary.LittleEndian, &grab.Types[i]); err != nil {
			return nil, err
		}
	}

	return grab, nil
}

func (vdAgentClipboardGrab VDAgentClipboardGrab) Encode() ([]byte, error) {
	buffer := &bytes.Buffer{}

	header := struct {
		Selection uint8
		_         [3]uint8
	}{
		Selection: vdAgentClipboardGrab.Selection,
	}

	if err := binary.Write(buffer, binary.LittleEndian, header); err != nil {
		return nil, err
	}

	for _, t := range vdAgentClipboardGrab.Types {
		if err := binary.Write(buffer, binary.LittleEndian, t); err != nil {
			return nil, err
		}
	}

	return buffer.Bytes(), nil
}

func (vdAgentClipboardGrab VDAgentClipboardGrab) String() string {
	return fmt.Sprintf("VDAgentClipboardGrab(selection=%d, types=%v)",
		vdAgentClipboardGrab.Selection, vdAgentClipboardGrab.Types)
}
