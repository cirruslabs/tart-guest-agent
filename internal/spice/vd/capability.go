package vd

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"strings"
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
	var capabilities []string

	if (vdAgentAnnounceCapabilities.Caps & VD_AGENT_CAP_MOUSE_STATE) != 0 {
		capabilities = append(capabilities, "VD_AGENT_CAP_MOUSE_STATE")
	}
	if (vdAgentAnnounceCapabilities.Caps & VD_AGENT_CAP_MONITORS_CONFIG) != 0 {
		capabilities = append(capabilities, "VD_AGENT_CAP_MONITORS_CONFIG")
	}
	if (vdAgentAnnounceCapabilities.Caps & VD_AGENT_CAP_REPLY) != 0 {
		capabilities = append(capabilities, "VD_AGENT_CAP_REPLY")
	}
	if (vdAgentAnnounceCapabilities.Caps & VD_AGENT_CAP_CLIPBOARD) != 0 {
		capabilities = append(capabilities, "VD_AGENT_CAP_CLIPBOARD")
	}
	if (vdAgentAnnounceCapabilities.Caps & VD_AGENT_CAP_DISPLAY_CONFIG) != 0 {
		capabilities = append(capabilities, "VD_AGENT_CAP_DISPLAY_CONFIG")
	}
	if (vdAgentAnnounceCapabilities.Caps & VD_AGENT_CAP_CLIPBOARD_BY_DEMAND) != 0 {
		capabilities = append(capabilities, "VD_AGENT_CAP_CLIPBOARD_BY_DEMAND")
	}
	if (vdAgentAnnounceCapabilities.Caps & VD_AGENT_CAP_CLIPBOARD_SELECTION) != 0 {
		capabilities = append(capabilities, "VD_AGENT_CAP_CLIPBOARD_SELECTION")
	}
	if (vdAgentAnnounceCapabilities.Caps & VD_AGENT_CAP_SPARSE_MONITORS_CONFIG) != 0 {
		capabilities = append(capabilities, "VD_AGENT_CAP_SPARSE_MONITORS_CONFIG")
	}
	if (vdAgentAnnounceCapabilities.Caps & VD_AGENT_CAP_GUEST_LINEEND_LF) != 0 {
		capabilities = append(capabilities, "VD_AGENT_CAP_GUEST_LINEEND_LF")
	}
	if (vdAgentAnnounceCapabilities.Caps & VD_AGENT_CAP_GUEST_LINEEND_CRLF) != 0 {
		capabilities = append(capabilities, "VD_AGENT_CAP_GUEST_LINEEND_CRLF")
	}
	if (vdAgentAnnounceCapabilities.Caps & VD_AGENT_CAP_MAX_CLIPBOARD) != 0 {
		capabilities = append(capabilities, "VD_AGENT_CAP_MAX_CLIPBOARD")
	}
	if (vdAgentAnnounceCapabilities.Caps & VD_AGENT_CAP_AUDIO_VOLUME_SYNC) != 0 {
		capabilities = append(capabilities, "VD_AGENT_CAP_AUDIO_VOLUME_SYNC")
	}
	if (vdAgentAnnounceCapabilities.Caps & VD_AGENT_CAP_MONITORS_CONFIG_POSITION) != 0 {
		capabilities = append(capabilities, "VD_AGENT_CAP_MONITORS_CONFIG_POSITION")
	}
	if (vdAgentAnnounceCapabilities.Caps & VD_AGENT_CAP_FILE_XFER_DISABLED) != 0 {
		capabilities = append(capabilities, "VD_AGENT_CAP_FILE_XFER_DISABLED")
	}
	if (vdAgentAnnounceCapabilities.Caps & VD_AGENT_CAP_FILE_XFER_DETAILED_ERRORS) != 0 {
		capabilities = append(capabilities, "VD_AGENT_CAP_FILE_XFER_DETAILED_ERRORS")
	}
	if (vdAgentAnnounceCapabilities.Caps & VD_AGENT_CAP_GRAPHICS_DEVICE_INFO) != 0 {
		capabilities = append(capabilities, "VD_AGENT_CAP_GRAPHICS_DEVICE_INFO")
	}
	if (vdAgentAnnounceCapabilities.Caps & VD_AGENT_CAP_CLIPBOARD_NO_RELEASE_ON_REGRAB) != 0 {
		capabilities = append(capabilities, "VD_AGENT_CAP_CLIPBOARD_NO_RELEASE_ON_REGRAB")
	}
	if (vdAgentAnnounceCapabilities.Caps & VD_AGENT_CAP_CLIPBOARD_GRAB_SERIAL) != 0 {
		capabilities = append(capabilities, "VD_AGENT_CAP_CLIPBOARD_GRAB_SERIAL")
	}
	if (vdAgentAnnounceCapabilities.Caps & VD_AGENT_END_CAP) != 0 {
		capabilities = append(capabilities, "VD_AGENT_END_CAP")
	}

	return fmt.Sprintf("VDAgentAnnounceCapabilities(request=%d, capabilities=%s)",
		vdAgentAnnounceCapabilities.Request, strings.Join(capabilities, "|"))
}
