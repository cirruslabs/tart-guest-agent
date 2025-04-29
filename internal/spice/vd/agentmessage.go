package vd

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
)

type VDAgentMessage struct {
	VDAgentMessageInner
	Data []byte
}

type VDAgentMessageInner struct {
	Protocol uint32
	Type     uint32
	Opaque   uint64
	Size     uint32
}

func ReadVDAgentMessage(r io.Reader) (*VDAgentMessage, error) {
	var vdiAgentMessage VDAgentMessage

	if err := binary.Read(r, binary.LittleEndian, &vdiAgentMessage.VDAgentMessageInner); err != nil {
		return nil, err
	}

	vdiAgentMessage.Data = make([]byte, vdiAgentMessage.Size)

	if _, err := io.ReadFull(r, vdiAgentMessage.Data); err != nil {
		return nil, err
	}

	return &vdiAgentMessage, nil
}

func (vdiAgentMessage *VDAgentMessage) Encode() ([]byte, error) {
	buffer := &bytes.Buffer{}

	if err := binary.Write(buffer, binary.LittleEndian, vdiAgentMessage.VDAgentMessageInner); err != nil {
		return nil, err
	}

	if _, err := buffer.Write(vdiAgentMessage.Data); err != nil {
		return nil, err
	}

	return buffer.Bytes(), nil
}

func (vdiAgentMessage VDAgentMessage) String() string {
	return fmt.Sprintf("%#+v", vdiAgentMessage.VDAgentMessageInner)
}
