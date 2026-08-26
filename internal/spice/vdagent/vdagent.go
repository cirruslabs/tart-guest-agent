package vdagent

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
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
	if runtime.GOOS == "linux" {
		candidates := []string{
			"/dev/virtio-ports/com.redhat.spice.0",
		}
		// Also scan /sys/class/virtio-ports to resolve the device dynamically
		if entries, err := os.ReadDir("/sys/class/virtio-ports"); err == nil {
			for _, entry := range entries {
				nameBytes, err := os.ReadFile(filepath.Join("/sys/class/virtio-ports", entry.Name(), "name"))
				if err == nil && strings.TrimSpace(string(nameBytes)) == "com.redhat.spice.0" {
					candidates = append(candidates, filepath.Join("/dev", entry.Name()))
				}
			}
		}
		candidates = append(candidates, "/dev/tty.com.redhat.spice.0")

		for _, path := range candidates {
			if _, err := os.Stat(path); err == nil {
				return path
			}
		}
		return "/dev/virtio-ports/com.redhat.spice.0"
	}

	// Darwin / macOS guests
	candidates := []string{
		"/dev/tty.com.redhat.spice.0",
		"/dev/cu.com.redhat.spice.0",
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
	clipboardEnabled   bool
}

func New() (*VDAgent, error) {
	portPath := findSerialPortPath()
	sp, err := os.OpenFile(portPath, os.O_RDWR, 0)
	if err != nil {
		return nil, err
	}

	clipboardEnabled := true
	if err := clipboard.Init(); err != nil {
		zap.S().Warnf("clipboard initialization failed (%v); clipboard sharing disabled, file transfer will remain active", err)
		clipboardEnabled = false
	}

	return &VDAgent{
		serialPort:       sp,
		vdi:              vdi.New(sp),
		fileXferMgr:      filexfer.NewManager(),
		clipboardEnabled: clipboardEnabled,
	}, nil
}

func (agent *VDAgent) writeMessageLocked(msgType uint32, data []byte) error {
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

func (agent *VDAgent) writeMessage(msgType uint32, data []byte) error {
	agent.writeMu.Lock()
	defer agent.writeMu.Unlock()
	return agent.writeMessageLocked(msgType, data)
}

func (agent *VDAgent) sendClipboardData(selection uint8, clipType uint32, data []byte) error {
	agent.writeMu.Lock()
	defer agent.writeMu.Unlock()

	var payload []byte
	if clipType != vd.VD_AGENT_CLIPBOARD_NONE && len(data) > 0 {
		payload = data
	} else {
		clipType = vd.VD_AGENT_CLIPBOARD_NONE
	}

	ourAgentClipboard := vd.VDAgentClipboard{
		VDAgentClipboardInner: vd.VDAgentClipboardInner{
			Selection: selection,
			Type:      clipType,
		},
		Data: payload,
	}
	encoded, err := ourAgentClipboard.Encode()
	if err != nil {
		return err
	}

	zap.S().Debugf("O: VD_AGENT_CLIPBOARD (selection=%d, type=%d, %d bytes)", selection, clipType, len(payload))
	return agent.writeMessageLocked(vd.VD_AGENT_CLIPBOARD, encoded)
}

func (agent *VDAgent) sendCapabilities(request uint32) error {
	var caps uint32
	if agent.clipboardEnabled {
		caps = vd.VD_AGENT_CAP_CLIPBOARD_BY_DEMAND | vd.VD_AGENT_CAP_CLIPBOARD_SELECTION
	}
	ourCapabilities := vd.VDAgentAnnounceCapabilities{
		Request: request,
		Caps:    caps,
	}
	encoded, err := ourCapabilities.Encode()
	if err != nil {
		return err
	}
	zap.S().Debugf("O: VD_AGENT_ANNOUNCE_CAPABILITIES (request=%d, caps=%d)", request, caps)
	return agent.writeMessage(vd.VD_AGENT_ANNOUNCE_CAPABILITIES, encoded)
}

func (agent *VDAgent) Run(ctx context.Context) error {
	// Send initial capability announcement immediately on startup
	if err := agent.sendCapabilities(1); err != nil {
		return fmt.Errorf("failed to send initial capabilities: %w", err)
	}

	g, gCtx := errgroup.WithContext(ctx)

	// Goroutine 1: Guest -> Host Clipboard Watcher (only if clipboard is enabled)
	if agent.clipboardEnabled {
		g.Go(func() error {
			textCh := clipboard.Watch(gCtx, clipboard.FmtText)
			imageCh := clipboard.Watch(gCtx, clipboard.FmtImage)

			for {
				select {
				case <-gCtx.Done():
					return gCtx.Err()
				case textData, ok := <-textCh:
					if !ok {
						return nil
					}
					if err := agent.processClipboardState(textData.Bytes, vd.VD_AGENT_CLIPBOARD_UTF8_TEXT); err != nil {
						if gCtx.Err() != nil {
							return gCtx.Err()
						}
						return fmt.Errorf("failed to process text clipboard state: %w", err)
					}
				case imgData, ok := <-imageCh:
					if !ok {
						return nil
					}
					if err := agent.processClipboardState(imgData.Bytes, vd.VD_AGENT_CLIPBOARD_IMAGE_PNG); err != nil {
						if gCtx.Err() != nil {
							return gCtx.Err()
						}
						return fmt.Errorf("failed to process image clipboard state: %w", err)
					}
				}
			}
		})
	}

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

func selectGrabRequestType(types []uint32) (uint32, bool) {
	// 1. Prefer compressed PNG image format first
	for _, t := range types {
		if t == vd.VD_AGENT_CLIPBOARD_IMAGE_PNG {
			return t, true
		}
	}
	// 2. Fallback to other supported image formats (BMP, TIFF, JPG)
	for _, t := range types {
		if t == vd.VD_AGENT_CLIPBOARD_IMAGE_BMP ||
			t == vd.VD_AGENT_CLIPBOARD_IMAGE_TIFF ||
			t == vd.VD_AGENT_CLIPBOARD_IMAGE_JPG {
			return t, true
		}
	}
	// 3. Fallback to UTF-8 text format
	for _, t := range types {
		if t == vd.VD_AGENT_CLIPBOARD_UTF8_TEXT {
			return t, true
		}
	}
	return 0, false
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
		if !agent.clipboardEnabled {
			zap.S().Debugf("ignoring VD_AGENT_CLIPBOARD_GRAB because clipboard is disabled")
			return nil
		}

		vdAgentClipboardGrab, err := vd.DecodeVDAgentClipboardGrab(vdiAgentMessage.Data)
		if err != nil {
			return err
		}

		zap.S().Debugf("I: VD_AGENT_CLIPBOARD_GRAB (%d bytes, selection=%d): %s",
			len(vdiAgentMessage.Data), vdAgentClipboardGrab.Selection, vdAgentClipboardGrab)

		// Ignore non-CLIPBOARD selections (e.g. PRIMARY/SECONDARY)
		if vdAgentClipboardGrab.Selection != vd.VD_AGENT_CLIPBOARD_SELECTION_CLIPBOARD {
			zap.S().Debugf("ignoring grab for non-clipboard selection %d", vdAgentClipboardGrab.Selection)
			return nil
		}

		reqType, ok := selectGrabRequestType(vdAgentClipboardGrab.Types)
		if !ok {
			zap.S().Debugf("ignoring grab with unsupported format types: %v", vdAgentClipboardGrab.Types)
			return nil
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
		if !agent.clipboardEnabled {
			zap.S().Debugf("ignoring VD_AGENT_CLIPBOARD because clipboard is disabled")
			return nil
		}
		vdAgentClipboard, err := vd.DecodeVDAgentClipboard(vdiAgentMessage.Data)
		if err != nil {
			return err
		}

		// Ignore non-CLIPBOARD selections
		if vdAgentClipboard.Selection != vd.VD_AGENT_CLIPBOARD_SELECTION_CLIPBOARD {
			zap.S().Debugf("ignoring clipboard data for non-clipboard selection %d", vdAgentClipboard.Selection)
			return nil
		}

		switch vdAgentClipboard.Type {
		case vd.VD_AGENT_CLIPBOARD_IMAGE_PNG, vd.VD_AGENT_CLIPBOARD_IMAGE_BMP, vd.VD_AGENT_CLIPBOARD_IMAGE_TIFF, vd.VD_AGENT_CLIPBOARD_IMAGE_JPG:
			optimized, err := imageopt.OptimizeImage(vdAgentClipboard.Data)
			if err != nil {
				zap.S().Warnf("ignoring invalid/unsafe incoming clipboard image (%d bytes): %v", len(vdAgentClipboard.Data), err)
				return nil
			}

			agent.clipMu.Lock()
			agent.lastClipboardState = optimized
			agent.lastClipboardType = vd.VD_AGENT_CLIPBOARD_IMAGE_PNG
			agent.clipMu.Unlock()

			clipboard.Write(clipboard.FmtImage, optimized)
			zap.S().Debugf("Wrote image clipboard data (%d bytes -> %d bytes)", len(vdAgentClipboard.Data), len(optimized))
		case vd.VD_AGENT_CLIPBOARD_UTF8_TEXT:
			agent.clipMu.Lock()
			agent.lastClipboardState = vdAgentClipboard.Data
			agent.lastClipboardType = vd.VD_AGENT_CLIPBOARD_UTF8_TEXT
			agent.clipMu.Unlock()

			clipboard.Write(clipboard.FmtText, vdAgentClipboard.Data)
			zap.S().Debugf("Wrote text clipboard data (%d bytes)", len(vdAgentClipboard.Data))
		default:
			zap.S().Warnf("ignoring unsupported clipboard data type %d", vdAgentClipboard.Type)
		}

	case vd.VD_AGENT_CLIPBOARD_REQUEST:
		if !agent.clipboardEnabled {
			zap.S().Debugf("ignoring VD_AGENT_CLIPBOARD_REQUEST because clipboard is disabled")
			return nil
		}

		vdAgentClipboardRequest, err := vd.DecodeVDAgentClipboardRequest(bytes.NewReader(vdiAgentMessage.Data))
		if err != nil {
			return err
		}

		zap.S().Debugf("I: VD_AGENT_CLIPBOARD_REQUEST: %s", vdAgentClipboardRequest)

		// Ignore non-CLIPBOARD selections
		if vdAgentClipboardRequest.Selection != vd.VD_AGENT_CLIPBOARD_SELECTION_CLIPBOARD {
			zap.S().Debugf("ignoring clipboard request for non-clipboard selection %d", vdAgentClipboardRequest.Selection)
			return nil
		}

		var data []byte
		respType := vdAgentClipboardRequest.Type

		switch respType {
		case vd.VD_AGENT_CLIPBOARD_IMAGE_PNG, vd.VD_AGENT_CLIPBOARD_IMAGE_BMP, vd.VD_AGENT_CLIPBOARD_IMAGE_TIFF, vd.VD_AGENT_CLIPBOARD_IMAGE_JPG:
			rawImg := clipboard.Read(clipboard.FmtImage)
			if len(rawImg) > 0 {
				if optimized, err := imageopt.OptimizeImage(rawImg); err == nil {
					data = optimized
					respType = vd.VD_AGENT_CLIPBOARD_IMAGE_PNG
				} else {
					zap.S().Warnf("failed to optimize local clipboard image for request: %v", err)
				}
			}
		case vd.VD_AGENT_CLIPBOARD_UTF8_TEXT:
			fallthrough
		default:
			data = clipboard.Read(clipboard.FmtText)
			respType = vd.VD_AGENT_CLIPBOARD_UTF8_TEXT
		}

		if len(data) == 0 {
			zap.S().Debugf("no clipboard data available for requested type %d, replying with VD_AGENT_CLIPBOARD_NONE", respType)
			return agent.sendClipboardData(vdAgentClipboardRequest.Selection, vd.VD_AGENT_CLIPBOARD_NONE, nil)
		}

		zap.S().Debugf("sending clipboard response (type=%d, %d bytes)", respType, len(data))
		return agent.sendClipboardData(vdAgentClipboardRequest.Selection, respType, data)

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

		if statusResp == nil {
			return nil
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

func getAvailableGrabTypes(primaryType uint32, hasImage bool, hasText bool) []uint32 {
	var types []uint32
	if hasImage {
		types = append(types, vd.VD_AGENT_CLIPBOARD_IMAGE_PNG)
	}
	if hasText {
		types = append(types, vd.VD_AGENT_CLIPBOARD_UTF8_TEXT)
	}
	if len(types) == 0 {
		types = append(types, primaryType)
	}
	return types
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

	hasImage := clipType == vd.VD_AGENT_CLIPBOARD_IMAGE_PNG || len(clipboard.Read(clipboard.FmtImage)) > 0
	hasText := clipType == vd.VD_AGENT_CLIPBOARD_UTF8_TEXT || len(clipboard.Read(clipboard.FmtText)) > 0
	types := getAvailableGrabTypes(clipType, hasImage, hasText)

	ourGrab := vd.VDAgentClipboardGrab{
		Selection: vd.VD_AGENT_CLIPBOARD_SELECTION_CLIPBOARD,
		Types:     types,
	}
	ourGrabBytes, err := ourGrab.Encode()
	if err != nil {
		return err
	}

	zap.S().Debugf("O: VD_AGENT_CLIPBOARD_GRAB (types=%v)", types)
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
