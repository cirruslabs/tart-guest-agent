package vd

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
	"unicode"
	"unicode/utf8"
)

// File transfer status codes
const (
	VD_AGENT_FILE_XFER_STATUS_CAN_SEND_DATA = iota
	VD_AGENT_FILE_XFER_STATUS_CANCELLED
	VD_AGENT_FILE_XFER_STATUS_ERROR
	VD_AGENT_FILE_XFER_STATUS_SUCCESS
	VD_AGENT_FILE_XFER_STATUS_NOT_ENOUGH_SPACE
	VD_AGENT_FILE_XFER_STATUS_SESSION_LOCKED
	VD_AGENT_FILE_XFER_STATUS_VDAGENT_NOT_CONNECTED
	VD_AGENT_FILE_XFER_STATUS_DISABLED
)

// isTextMetadata returns true if the buffer represents text-based metadata or a bare filename
// rather than a standard binary [uint64 size][filename] payload.
func isTextMetadata(data []byte) bool {
	trimmed := bytes.TrimLeft(data, " \t\r\n")
	if bytes.HasPrefix(trimmed, []byte("[")) {
		return true
	}
	if bytes.HasPrefix(trimmed, []byte("name=")) || bytes.HasPrefix(trimmed, []byte("size=")) {
		return true
	}
	if bytes.Contains(data, []byte("\n")) {
		return true
	}

	// Check if data is a bare filename (e.g. "report.pdf\0" or "document.txt")
	nulIdx := bytes.IndexByte(data, 0x00)
	var candidate []byte
	if nulIdx >= 0 {
		candidate = data[:nulIdx]
	} else {
		candidate = data
	}

	if len(candidate) > 0 && utf8.Valid(candidate) {
		isAllPrintable := true
		for _, r := range string(candidate) {
			if !unicode.IsPrint(r) {
				isAllPrintable = false
				break
			}
		}
		if isAllPrintable {
			return true
		}
	}

	return false
}

// VDAgentFileXferStart initiates a file transfer task.
type VDAgentFileXferStart struct {
	ID       uint32
	FileSize uint64 // Advertised total file size (parsed from binary size field or 0 if omitted)
	Data     []byte // Variable length metadata (key-value or raw filename)
}

func DecodeVDAgentFileXferStart(buf []byte) (*VDAgentFileXferStart, error) {
	if len(buf) < 4 {
		return nil, io.ErrUnexpectedEOF
	}

	id := binary.LittleEndian.Uint32(buf[:4])
	rem := buf[4:]

	var fileSize uint64
	data := rem

	// If rem is not text metadata or a bare filename, and has at least 8 bytes for size
	if !isTextMetadata(rem) && len(rem) >= 8 {
		fileSize = binary.LittleEndian.Uint64(rem[:8])
		data = rem[8:]
	}

	return &VDAgentFileXferStart{
		ID:       id,
		FileSize: fileSize,
		Data:     data,
	}, nil
}

func (msg VDAgentFileXferStart) Encode() ([]byte, error) {
	buffer := &bytes.Buffer{}

	if err := binary.Write(buffer, binary.LittleEndian, msg.ID); err != nil {
		return nil, err
	}

	if msg.FileSize > 0 && !isTextMetadata(msg.Data) {
		if err := binary.Write(buffer, binary.LittleEndian, msg.FileSize); err != nil {
			return nil, err
		}
	}

	if _, err := buffer.Write(msg.Data); err != nil {
		return nil, err
	}

	return buffer.Bytes(), nil
}

func (msg VDAgentFileXferStart) String() string {
	return fmt.Sprintf("VDAgentFileXferStart(id=%d, metadata=%d bytes)", msg.ID, len(msg.Data))
}

// VDAgentFileXferStatus sends status or progress for a transfer task.
type VDAgentFileXferStatus struct {
	ID     uint32
	Result uint32
	Data   []byte // Optional detailed error payload
}

func DecodeVDAgentFileXferStatus(buf []byte) (*VDAgentFileXferStatus, error) {
	if len(buf) < 8 {
		return nil, io.ErrUnexpectedEOF
	}

	r := bufio.NewReader(bytes.NewReader(buf))
	var header struct {
		ID     uint32
		Result uint32
	}

	if err := binary.Read(r, binary.LittleEndian, &header); err != nil {
		return nil, err
	}

	data, err := io.ReadAll(r)
	if err != nil {
		return nil, err
	}

	return &VDAgentFileXferStatus{
		ID:     header.ID,
		Result: header.Result,
		Data:   data,
	}, nil
}

func (msg VDAgentFileXferStatus) Encode() ([]byte, error) {
	buffer := &bytes.Buffer{}

	header := struct {
		ID     uint32
		Result uint32
	}{
		ID:     msg.ID,
		Result: msg.Result,
	}

	if err := binary.Write(buffer, binary.LittleEndian, header); err != nil {
		return nil, err
	}

	if len(msg.Data) > 0 {
		if _, err := buffer.Write(msg.Data); err != nil {
			return nil, err
		}
	}

	return buffer.Bytes(), nil
}

func (msg VDAgentFileXferStatus) String() string {
	return fmt.Sprintf("VDAgentFileXferStatus(id=%d, result=%d)", msg.ID, msg.Result)
}

// VDAgentFileXferData streams a binary chunk for an ongoing file transfer task.
type VDAgentFileXferData struct {
	ID   uint32
	Size uint64
	Data []byte
}

func DecodeVDAgentFileXferData(buf []byte) (*VDAgentFileXferData, error) {
	if len(buf) < 12 {
		return nil, io.ErrUnexpectedEOF
	}

	r := bufio.NewReader(bytes.NewReader(buf))
	var header struct {
		ID   uint32
		Size uint64
	}

	if err := binary.Read(r, binary.LittleEndian, &header); err != nil {
		return nil, err
	}

	data, err := io.ReadAll(r)
	if err != nil {
		return nil, err
	}

	return &VDAgentFileXferData{
		ID:   header.ID,
		Size: header.Size,
		Data: data,
	}, nil
}

func (msg VDAgentFileXferData) Encode() ([]byte, error) {
	buffer := &bytes.Buffer{}

	header := struct {
		ID   uint32
		Size uint64
	}{
		ID:   msg.ID,
		Size: msg.Size,
	}

	if err := binary.Write(buffer, binary.LittleEndian, header); err != nil {
		return nil, err
	}

	if _, err := buffer.Write(msg.Data); err != nil {
		return nil, err
	}

	return buffer.Bytes(), nil
}

func (msg VDAgentFileXferData) String() string {
	return fmt.Sprintf("VDAgentFileXferData(id=%d, chunk_size=%d, data_len=%d)",
		msg.ID, msg.Size, len(msg.Data))
}
