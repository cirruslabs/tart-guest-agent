package vdagent

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"os"
	"sync"

	"github.com/cirruslabs/tart-guest-agent/internal/spice/filexfer"
	"github.com/cirruslabs/tart-guest-agent/internal/spice/imageopt"
	"github.com/cirruslabs/tart-guest-agent/internal/spice/vd"
	"github.com/cirruslabs/tart-guest-agent/internal/spice/vdi"
	"go.uber.org/zap"
	"golang.design/x/clipboard"
	"golang.org/x/sync/errgroup"
)

func findSerialPortPath() string {
	candidates := []string{
		"/dev/tty.com.redhat.spice.0",
		"/dev/virtio-ports/com.redhat.spice.0",
		"/dev/vport7p0",
	}
	for _, path := range candidates {
		if _, err := os.Stat(path); err == nil {
			return path
		}
	}
	return "/dev/tty.com.redhat.spice.0"
}

type VDAgent struct {
	serialPort         *os.File
	vdi                *vdi.VDI
	writeMu            sync.Mutex
	lastClipboardState []byte
	lastClipboardType  uint32
	clipMu             sync.Mutex
	fileXferMgr        *filexfer.Manager
}

func New() (*VDAgent, error) {
	portPath := findSerialPortPath()
	sp, err := os.OpenFile(portPath, os.O_RDWR, 0)
	if err != nil {
		return nil, err
	}

	if err := clipboard.Init(); err != nil {
		_ = sp.Close()
		return nil, err
	}

	return &VDAgent{
		serialPort:  sp,
		vdi:         vdi.New(sp),
		fileXferMgr: filexfer.NewManager(),
	}, nil
}

func (agent *VDAgent) writeMessage(msgType uint32, data []byte) error {
	agent.writeMu.Lock()
	defer agent.writeMu.Unlock()

	msg := vd.VDAgentMessage{
		VDAgentMessageInner: vd.VDAgentMessageInner{
			Protocol: vd.VD_AGENT_PROTOCOL,
			Type:     msgType,
			Size:     uint32(len(data)),
		},
		Data: data,
	}
	encoded, err := msg.Encode()
	if err != nil {
		return err
	}
	_, err = agent.vdi.Write(encoded)
	return err
}

func (agent *VDAgent) sendCapabilities(request uint32) error {
	ourCapabilities := vd.VDAgentAnnounceCapabilities{
		Request: request,
		Caps:    vd.VD_AGENT_CAP_CLIPBOARD_BY_DEMAND | vd.VD_AGENT_CAP_CLIPBOARD_SELECTION,
	}
	encoded, err := ourCapabilities.Encode()
	if err != nil {
		return err
	}
	zap.S().Debugf("O: VD_AGENT_ANNOUNCE_CAPABILITIES (request=%d)", request)
	return agent.writeMessage(vd.VD_AGENT_ANNOUNCE_CAPABILITIES, encoded)
}

func (agent *VDAgent) Run(ctx context.Context) error {
	// Send initial capability announcement immediately on startup
	if err := agent.sendCapabilities(1); err != nil {
		return fmt.Errorf("failed to send initial capabilities: %w", err)
	}

	g, gCtx := errgroup.WithContext(ctx)

	// Goroutine 1: Guest -> Host Clipboard Watcher
	g.Go(func() error {
		clipboardCh := clipboard.Watch(gCtx)
		for {
			select {
			case <-gCtx.Done():
				return gCtx.Err()
			case newClipboardState, ok := <-clipboardCh:
				if !ok {
					return nil
				}
				var clipType uint32
				if newClipboardState.Format == clipboard.FmtImage {
					clipType = vd.VD_AGENT_CLIPBOARD_IMAGE_PNG
				} else {
					clipType = vd.VD_AGENT_CLIPBOARD_UTF8_TEXT
				}

				if err := agent.processClipboardState(newClipboardState.Bytes, clipType); err != nil {
					if gCtx.Err() != nil {
						return gCtx.Err()
					}
					return fmt.Errorf("failed to process clipboard state: %w", err)
				}
			}
		}
	})

	// Goroutine 2: Host -> Guest Inbound Serial Reader
	g.Go(func() error {
		go func() {
			<-gCtx.Done()
			_ = agent.serialPort.Close()
		}()

		for {
			if gCtx.Err() != nil {
				return gCtx.Err()
			}

			vdiAgentMessage, err := agent.readMessage()
			if err != nil {
				if gCtx.Err() != nil {
					return gCtx.Err()
				}
				return fmt.Errorf("serial read failed: %w", err)
			}

			if err := agent.handleMessage(vdiAgentMessage); err != nil {
				if gCtx.Err() != nil {
					return gCtx.Err()
				}
				return fmt.Errorf("failed handling message type %d: %w", vdiAgentMessage.Type, err)
			}
		}
	})

	return g.Wait()
}

func (agent *VDAgent) handleMessage(vdiAgentMessage *vd.VDAgentMessage) error {
	switch vdiAgentMessage.Type {
	case vd.VD_AGENT_ANNOUNCE_CAPABILITIES:
		vdAgentAnnounceCapabilities, err := vd.ReadVDAgentAnnounceCapabilities(vdiAgentMessage.Data)
		if err != nil {
			return err
		}

		zap.S().Debugf("I: VD_AGENT_ANNOUNCE_CAPABILITIES: %s", vdAgentAnnounceCapabilities)

		if vdAgentAnnounceCapabilities.Request != 0 {
			if err := agent.sendCapabilities(0); err != nil {
				return err
			}
		}
	case vd.VD_AGENT_CLIPBOARD_GRAB:
		vdAgentClipboardGrab, err := vd.DecodeVDAgentClipboardGrab(vdiAgentMessage.Data)
		if err != nil {
			return err
		}

		zap.S().Debugf("I: VD_AGENT_CLIPBOARD_GRAB (%d bytes): %s",
			len(vdiAgentMessage.Data), vdAgentClipboardGrab)

		reqType := uint32(vd.VD_AGENT_CLIPBOARD_UTF8_TEXT)
		if len(vdAgentClipboardGrab.Types) > 0 {
			reqType = vdAgentClipboardGrab.Types[0]
			for _, t := range vdAgentClipboardGrab.Types {
				if t == vd.VD_AGENT_CLIPBOARD_IMAGE_PNG ||
					t == vd.VD_AGENT_CLIPBOARD_IMAGE_BMP ||
					t == vd.VD_AGENT_CLIPBOARD_IMAGE_TIFF ||
					t == vd.VD_AGENT_CLIPBOARD_IMAGE_JPG {
					reqType = t
					break
				}
			}
		}

		ourClipboardRequest := vd.VDAgentClipboardRequest{
			Selection: vdAgentClipboardGrab.Selection,
			Type:      reqType,
		}
		ourClipboardRequestBytes, err := ourClipboardRequest.Encode()
		if err != nil {
			return err
		}

		zap.S().Debugf("O: VD_AGENT_CLIPBOARD_REQUEST (type=%d)", reqType)
		return agent.writeMessage(vd.VD_AGENT_CLIPBOARD_REQUEST, ourClipboardRequestBytes)

	case vd.VD_AGENT_CLIPBOARD:
		vdAgentClipboard, err := vd.DecodeVDAgentClipboard(vdiAgentMessage.Data)
		if err != nil {
			return err
		}

		switch vdAgentClipboard.Type {
		case vd.VD_AGENT_CLIPBOARD_IMAGE_PNG, vd.VD_AGENT_CLIPBOARD_IMAGE_BMP, vd.VD_AGENT_CLIPBOARD_IMAGE_TIFF, vd.VD_AGENT_CLIPBOARD_IMAGE_JPG:
			optimized := imageopt.OptimizeImage(vdAgentClipboard.Data)
			agent.clipMu.Lock()
			agent.lastClipboardState = optimized
			agent.lastClipboardType = vd.VD_AGENT_CLIPBOARD_IMAGE_PNG
			agent.clipMu.Unlock()

			clipboard.Write(clipboard.FmtImage, optimized)
			zap.S().Debugf("Wrote image clipboard data (%d bytes -> %d bytes)", len(vdAgentClipboard.Data), len(optimized))
		case vd.VD_AGENT_CLIPBOARD_UTF8_TEXT:
			fallthrough
		default:
			agent.clipMu.Lock()
			agent.lastClipboardState = vdAgentClipboard.Data
			agent.lastClipboardType = vd.VD_AGENT_CLIPBOARD_UTF8_TEXT
			agent.clipMu.Unlock()

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
			data = imageopt.OptimizeImage(clipboard.Read(clipboard.FmtImage))
			respType = vd.VD_AGENT_CLIPBOARD_IMAGE_PNG
		case vd.VD_AGENT_CLIPBOARD_UTF8_TEXT:
			fallthrough
		default:
			data = clipboard.Read(clipboard.FmtText)
			respType = vd.VD_AGENT_CLIPBOARD_UTF8_TEXT
		}

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

		zap.S().Debugf("O: VD_AGENT_CLIPBOARD (type=%d, %d bytes)", respType, len(data))
		return agent.writeMessage(vd.VD_AGENT_CLIPBOARD, ourAgentClipboardBytes)

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

		zap.S().Debugf("O: VD_AGENT_FILE_XFER_STATUS (task=%d, result=%d)", statusResp.ID, statusResp.Result)
		return agent.writeMessage(vd.VD_AGENT_FILE_XFER_STATUS, statusBytes)

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

		zap.S().Debugf("O: VD_AGENT_FILE_XFER_STATUS (task=%d, result=%d, completed=%v)",
			statusResp.ID, statusResp.Result, completed)
		return agent.writeMessage(vd.VD_AGENT_FILE_XFER_STATUS, statusBytes)

	case vd.VD_AGENT_FILE_XFER_STATUS:
		statusMsg, err := vd.DecodeVDAgentFileXferStatus(vdiAgentMessage.Data)
		if err != nil {
			return err
		}

		zap.S().Debugf("I: VD_AGENT_FILE_XFER_STATUS: %s", statusMsg)
		if statusMsg.Result == vd.VD_AGENT_FILE_XFER_STATUS_CANCELLED || statusMsg.Result == vd.VD_AGENT_FILE_XFER_STATUS_ERROR {
			agent.fileXferMgr.Cancel(statusMsg.ID)
		}
	case vd.VD_AGENT_CLIENT_DISCONNECTED:
		zap.S().Debugf("I: VD_AGENT_CLIENT_DISCONNECTED")
		agent.fileXferMgr.Close()
	default:
		zap.S().Debugf("I: unhandled message type: %d", vdiAgentMessage.Type)
	}
	return nil
}

func (agent *VDAgent) Close() error {
	agent.fileXferMgr.Close()
	return agent.serialPort.Close()
}

func (agent *VDAgent) processClipboardState(newClipboardState []byte, clipType uint32) error {
	agent.clipMu.Lock()
	if bytes.Equal(agent.lastClipboardState, newClipboardState) && agent.lastClipboardType == clipType {
		agent.clipMu.Unlock()
		return nil
	}
	agent.lastClipboardState = newClipboardState
	agent.lastClipboardType = clipType
	agent.clipMu.Unlock()

	ourGrab := vd.VDAgentClipboardGrab{
		Selection: vd.VD_AGENT_CLIPBOARD_SELECTION_CLIPBOARD,
		Types:     []uint32{clipType},
	}
	ourGrabBytes, err := ourGrab.Encode()
	if err != nil {
		return err
	}

	zap.S().Debugf("O: VD_AGENT_CLIPBOARD_GRAB (type=%d)", clipType)
	return agent.writeMessage(vd.VD_AGENT_CLIPBOARD_GRAB, ourGrabBytes)
}

func (agent *VDAgent) readMessage() (*vd.VDAgentMessage, error) {
	var inner vd.VDAgentMessageInner
	if err := binary.Read(agent.vdi, binary.LittleEndian, &inner); err != nil {
		return nil, err
	}

	data := make([]byte, inner.Size)
	if _, err := io.ReadFull(agent.vdi, data); err != nil {
		return nil, err
	}

	return &vd.VDAgentMessage{
		VDAgentMessageInner: inner,
		Data:                data,
	}, nil
}
