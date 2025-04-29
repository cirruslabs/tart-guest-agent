package vd

import (
	"bytes"
	"encoding/binary"
	"fmt"
)

type VDAgentAnnounceCapabilities struct {
	Request uint32
	Caps    uint32
}

func ReadVDAgentAnnounceCapabilities(buf []byte) (*VDAgentAnnounceCapabilities, error) {
	var vdAgentAnnounceCapabilities VDAgentAnnounceCapabilities

	if err := binary.Read(bytes.NewReader(buf), binary.LittleEndian, &vdAgentAnnounceCapabilities); err != nil {
		return nil, err
	}

	return &vdAgentAnnounceCapabilities, nil
}

func (vdAgentAnnounceCapabilities VDAgentAnnounceCapabilities) Encode() ([]byte, error) {
	buffer := &bytes.Buffer{}

	if err := binary.Write(buffer, binary.LittleEndian, &vdAgentAnnounceCapabilities); err != nil {
		return nil, err
	}

	return buffer.Bytes(), nil
}

func (vdAgentAnnounceCapabilities VDAgentAnnounceCapabilities) String() string {
	return fmt.Sprintf("VDAgentAnnounceCapabilities(request=%d, capabilities=%d)",
		vdAgentAnnounceCapabilities.Request, vdAgentAnnounceCapabilities.Caps)
}
