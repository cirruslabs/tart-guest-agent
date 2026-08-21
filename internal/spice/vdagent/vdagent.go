package vdagent

import (
	"bytes"
	"context"
	"errors"
	"github.com/cirruslabs/tart-guest-agent/internal/spice/filexfer"
	"github.com/cirruslabs/tart-guest-agent/internal/spice/vd"
	"github.com/cirruslabs/tart-guest-agent/internal/spice/vdi"
	"go.uber.org/zap"
	"golang.design/x/clipboard"
	"os"
	"time"
)

const serialPortPath = "/dev/tty.com.redhat.spice.0"

type VDAgent struct {
	serialPort         *os.File
	vdi                *vdi.VDI
	lastClipboardState []byte
	lastClipboardType  uint32
	fileXferMgr        *filexfer.Manager
}

func New() (*VDAgent, error) {
	sp, err := os.OpenFile(serialPortPath, os.O_RDWR, 0)
	if err != nil {
		return nil, err
	}

	if err := clipboard.Init(); err != nil {
		return nil, err
	}

	return &VDAgent{
		serialPort:  sp,
		vdi:         vdi.New(sp),
		fileXferMgr: filexfer.NewManager(),
	}, nil
}

func (agent *VDAgent) Run(ctx context.Context) error {
	subCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	// Watch both text and image clipboard changes
	clipboardCh := clipboard.Watch(subCtx)

	for {
		// Check for cancellation and clipboard changes
		select {
		case <-ctx.Done():
			return ctx.Err()
		case newClipboardState := <-clipboardCh:
			var clipType uint32
			if newClipboardState.Format == clipboard.FmtImage {
				clipType = vd.VD_AGENT_CLIPBOARD_IMAGE_PNG
			} else {
				clipType = vd.VD_AGENT_CLIPBOARD_UTF8_TEXT
			}

			if err := agent.processClipboardState(newClipboardState.Bytes, clipType); err != nil {
				return err
			}
			agent.lastClipboardState = newClipboardState.Bytes
			agent.lastClipboardType = clipType
		default:
			// Nothing, proceed
		}

		if err := agent.serialPort.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
			return err
		}

		vdiAgentMessage, err := vd.ReadVDAgentMessage(agent.vdi)
		if err != nil {
			if errors.Is(err, os.ErrDeadlineExceeded) {
				continue
			}

			return err
		}

		switch vdiAgentMessage.Type {
		case vd.VD_AGENT_ANNOUNCE_CAPABILITIES:
			vdAgentAnnounceCapabilities, err := vd.ReadVDAgentAnnounceCapabilities(vdiAgentMessage.Data)
			if err != nil {
				return err
			}

			zap.S().Debugf("I: VD_AGENT_ANNOUNCE_CAPABILITIES: %s", vdAgentAnnounceCapabilities)

			if vdAgentAnnounceCapabilities.Request == 0 {
				// No need to send our capabilities
				break
			}

			// Send our capabilities
			ourCapabilities := vd.VDAgentAnnounceCapabilities{
				Request: 0,
				Caps:    vd.VD_AGENT_CAP_CLIPBOARD_BY_DEMAND | vd.VD_AGENT_CAP_CLIPBOARD_SELECTION,
			}
			ourCapabilitiesBytes, err := ourCapabilities.Encode()
			if err != nil {
				return err
			}

			ourAgentMessage := vd.VDAgentMessage{
				VDAgentMessageInner: vd.VDAgentMessageInner{
					Protocol: vd.VD_AGENT_PROTOCOL,
					Type:     vd.VD_AGENT_ANNOUNCE_CAPABILITIES,
					Size:     uint32(len(ourCapabilitiesBytes)),
				},
				Data: ourCapabilitiesBytes,
			}
			ourAgentMessageBytes, err := ourAgentMessage.Encode()
			if err != nil {
				return err
			}

			if _, err := agent.vdi.Write(ourAgentMessageBytes); err != nil {
				return err
			}

			zap.S().Debugf("O: VD_AGENT_ANNOUNCE_CAPABILITIES")
		case vd.VD_AGENT_CLIPBOARD_GRAB:
			vdAgentClipboardGrab, err := vd.DecodeVDAgentClipboardGrab(bytes.NewReader(vdiAgentMessage.Data))
			if err != nil {
				return err
			}

			zap.S().Debugf("I: VD_AGENT_CLIPBOARD_GRAB (%d bytes): %s",
				len(vdiAgentMessage.Data), vdAgentClipboardGrab)

			reqType := vdAgentClipboardGrab.Type
			if reqType == 0 {
				reqType = vd.VD_AGENT_CLIPBOARD_UTF8_TEXT
			}

			ourClipboardRequest := vd.VDAgentClipboardRequest{
				Selection: vdAgentClipboardGrab.Selection,
				Type:      reqType,
			}
			ourClipboardRequestBytes, err := ourClipboardRequest.Encode()
			if err != nil {
				return err
			}

			ourAgentMessage := vd.VDAgentMessage{
				VDAgentMessageInner: vd.VDAgentMessageInner{
					Protocol: vd.VD_AGENT_PROTOCOL,
					Type:     vd.VD_AGENT_CLIPBOARD_REQUEST,
					Size:     uint32(len(ourClipboardRequestBytes)),
				},
				Data: ourClipboardRequestBytes,
			}
			ourAgentMessageBytes, err := ourAgentMessage.Encode()
			if err != nil {
				return err
			}

			if _, err := agent.vdi.Write(ourAgentMessageBytes); err != nil {
				return err
			}

			zap.S().Debugf("O: VD_AGENT_CLIPBOARD_REQUEST (type=%d)", reqType)
		case vd.VD_AGENT_CLIPBOARD:
			// Receive clipboard from host
			vdAgentClipboard, err := vd.DecodeVDAgentClipboard(vdiAgentMessage.Data)
			if err != nil {
				return err
			}

			zap.S().Debugf("I: VD_AGENT_CLIPBOARD: %s", vdAgentClipboard)

			switch vdAgentClipboard.Type {
			case vd.VD_AGENT_CLIPBOARD_IMAGE_PNG, vd.VD_AGENT_CLIPBOARD_IMAGE_BMP, vd.VD_AGENT_CLIPBOARD_IMAGE_TIFF, vd.VD_AGENT_CLIPBOARD_IMAGE_JPG:
				clipboard.Write(clipboard.FmtImage, vdAgentClipboard.Data)
				zap.S().Debugf("Wrote image clipboard data (%d bytes)", len(vdAgentClipboard.Data))
			case vd.VD_AGENT_CLIPBOARD_UTF8_TEXT:
				fallthrough
			default:
				clipboard.Write(clipboard.FmtText, vdAgentClipboard.Data)
				zap.S().Debugf("Wrote text clipboard data (%d bytes)", len(vdAgentClipboard.Data))
			}
		case vd.VD_AGENT_CLIPBOARD_REQUEST:
			vdAgentClipboardRequest, err := vd.DecodeVDAgentClipboardRequest(bytes.NewReader(vdiAgentMessage.Data))
			if err != nil {
				return err
			}

			zap.S().Debugf("I: VD_AGENT_CLIPBOARD_REQUEST: %s", vdAgentClipboardRequest)

			var data []byte
			respType := vdAgentClipboardRequest.Type

			switch respType {
			case vd.VD_AGENT_CLIPBOARD_IMAGE_PNG, vd.VD_AGENT_CLIPBOARD_IMAGE_BMP, vd.VD_AGENT_CLIPBOARD_IMAGE_TIFF, vd.VD_AGENT_CLIPBOARD_IMAGE_JPG:
				data = clipboard.Read(clipboard.FmtImage)
				respType = vd.VD_AGENT_CLIPBOARD_IMAGE_PNG
			case vd.VD_AGENT_CLIPBOARD_UTF8_TEXT:
				fallthrough
			default:
				data = clipboard.Read(clipboard.FmtText)
				respType = vd.VD_AGENT_CLIPBOARD_UTF8_TEXT
			}

			// Send clipboard
			ourAgentClipboard := vd.VDAgentClipboard{
				VDAgentClipboardInner: vd.VDAgentClipboardInner{
					Selection: vdAgentClipboardRequest.Selection,
					Type:      respType,
				},
				Data: data,
			}
			ourAgentClipboardBytes, err := ourAgentClipboard.Encode()
			if err != nil {
				return err
			}

			ourAgentMessage := vd.VDAgentMessage{
				VDAgentMessageInner: vd.VDAgentMessageInner{
					Protocol: vd.VD_AGENT_PROTOCOL,
					Type:     vd.VD_AGENT_CLIPBOARD,
					Size:     uint32(len(ourAgentClipboardBytes)),
				},
				Data: ourAgentClipboardBytes,
			}
			ourAgentMessageBytes, err := ourAgentMessage.Encode()
			if err != nil {
				return err
			}

			if _, err := agent.vdi.Write(ourAgentMessageBytes); err != nil {
				return err
			}

			zap.S().Debugf("O: VD_AGENT_CLIPBOARD (type=%d, %d bytes)", respType, len(data))
		case vd.VD_AGENT_FILE_XFER_START:
			startXferMsg, err := vd.DecodeVDAgentFileXferStart(vdiAgentMessage.Data)
			if err != nil {
				zap.S().Errorf("failed to decode VD_AGENT_FILE_XFER_START: %v", err)
				return err
			}

			zap.S().Debugf("I: VD_AGENT_FILE_XFER_START: %s", startXferMsg)

			statusResp, err := agent.fileXferMgr.HandleStart(startXferMsg)
			if err != nil {
				zap.S().Errorf("failed handling file transfer start: %v", err)
			}

			statusBytes, err := statusResp.Encode()
			if err != nil {
				return err
			}

			respMsg := vd.VDAgentMessage{
				VDAgentMessageInner: vd.VDAgentMessageInner{
					Protocol: vd.VD_AGENT_PROTOCOL,
					Type:     vd.VD_AGENT_FILE_XFER_STATUS,
					Size:     uint32(len(statusBytes)),
				},
				Data: statusBytes,
			}
			respBytes, err := respMsg.Encode()
			if err != nil {
				return err
			}

			if _, err := agent.vdi.Write(respBytes); err != nil {
				return err
			}

			zap.S().Debugf("O: VD_AGENT_FILE_XFER_STATUS (task=%d, result=%d)", statusResp.ID, statusResp.Result)
		case vd.VD_AGENT_FILE_XFER_DATA:
			dataXferMsg, err := vd.DecodeVDAgentFileXferData(vdiAgentMessage.Data)
			if err != nil {
				zap.S().Errorf("failed to decode VD_AGENT_FILE_XFER_DATA: %v", err)
				return err
			}

			zap.S().Debugf("I: VD_AGENT_FILE_XFER_DATA: %s", dataXferMsg)

			statusResp, completed, err := agent.fileXferMgr.HandleData(dataXferMsg)
			if err != nil {
				zap.S().Errorf("failed handling file transfer data: %v", err)
			}

			statusBytes, err := statusResp.Encode()
			if err != nil {
				return err
			}

			respMsg := vd.VDAgentMessage{
				VDAgentMessageInner: vd.VDAgentMessageInner{
					Protocol: vd.VD_AGENT_PROTOCOL,
					Type:     vd.VD_AGENT_FILE_XFER_STATUS,
					Size:     uint32(len(statusBytes)),
				},
				Data: statusBytes,
			}
			respBytes, err := respMsg.Encode()
			if err != nil {
				return err
			}

			if _, err := agent.vdi.Write(respBytes); err != nil {
				return err
			}

			zap.S().Debugf("O: VD_AGENT_FILE_XFER_STATUS (task=%d, result=%d, completed=%v)",
				statusResp.ID, statusResp.Result, completed)
		case vd.VD_AGENT_FILE_XFER_STATUS:
			statusMsg, err := vd.DecodeVDAgentFileXferStatus(vdiAgentMessage.Data)
			if err != nil {
				return err
			}

			zap.S().Debugf("I: VD_AGENT_FILE_XFER_STATUS: %s", statusMsg)
			if statusMsg.Result == vd.VD_AGENT_FILE_XFER_STATUS_CANCELLED || statusMsg.Result == vd.VD_AGENT_FILE_XFER_STATUS_ERROR {
				agent.fileXferMgr.Cancel(statusMsg.ID)
			}
		default:
			zap.S().Debugf("I: unhandled message type: %d", vdiAgentMessage.Type)
		}
	}
}

func (agent *VDAgent) Close() error {
	agent.fileXferMgr.Close()
	return agent.serialPort.Close()
}

func (agent *VDAgent) processClipboardState(newClipboardState []byte, clipType uint32) error {
	if bytes.Equal(agent.lastClipboardState, newClipboardState) && agent.lastClipboardType == clipType {
		// Nothing changed since the last VD_AGENT_CLIPBOARD_GRAB from us
		return nil
	}

	ourGrab := vd.VDAgentClipboardGrab{
		Selection: vd.VD_AGENT_CLIPBOARD_SELECTION_CLIPBOARD,
		Type:      clipType,
	}
	ourGrabBytes, err := ourGrab.Encode()
	if err != nil {
		return err
	}

	ourAgentMessage := vd.VDAgentMessage{
		VDAgentMessageInner: vd.VDAgentMessageInner{
			Protocol: vd.VD_AGENT_PROTOCOL,
			Type:     vd.VD_AGENT_CLIPBOARD_GRAB,
			Size:     uint32(len(ourGrabBytes)),
		},
		Data: ourGrabBytes,
	}
	ourAgentMessageBytes, err := ourAgentMessage.Encode()
	if err != nil {
		return err
	}

	if _, err := agent.vdi.Write(ourAgentMessageBytes); err != nil {
		return err
	}

	zap.S().Debugf("O: VD_AGENT_CLIPBOARD_GRAB (type=%d)", clipType)

	return nil
}
