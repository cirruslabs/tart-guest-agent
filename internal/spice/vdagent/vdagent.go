package vdagent

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"github.com/cirruslabs/tart-guest-agent/internal/spice/vd"
	"github.com/cirruslabs/tart-guest-agent/internal/spice/vdi"
	"go.uber.org/zap"
	"golang.design/x/clipboard"
	"os"
	"time"
)

const serialPortPath = "/dev/tty.com.redhat.spice.0"

// ErrSPICENotConnected is returned when the SPICE clipboard channel
// is not available. This typically happens when running headless or
// when clipboard sharing is not enabled on the host.
type ErrSPICENotConnected struct {
	Err error
}

func (e *ErrSPICENotConnected) Error() string {
	return fmt.Sprintf("SPICE clipboard channel not connected: %v", e.Err)
}

func (e *ErrSPICENotConnected) Unwrap() error {
	return e.Err
}

type VDAgent struct {
	serialPort           *os.File
	vdi                  *vdi.VDI
	lastClipboardState   []byte
	clipboardInitialized bool
}

func New() (*VDAgent, error) {
	sp, err := os.OpenFile(serialPortPath, os.O_RDWR, 0)
	if err != nil {
		return nil, err
	}

	// Note: clipboard.Init() is deferred until we confirm SPICE is connected.
	// This avoids expensive initialization when clipboard sharing is unavailable.

	return &VDAgent{
		serialPort: sp,
		vdi:        vdi.New(sp),
	}, nil
}

func (agent *VDAgent) Run(ctx context.Context) error {
	// clipboardCh is nil until clipboard is initialized.
	// A nil channel blocks forever on receive, which is fine for the select.
	var clipboardCh <-chan []byte

	// Create a child context for clipboard.Watch() so we can cancel it
	// when Run() exits, preventing goroutine leaks on retry.
	clipboardCtx, clipboardCancel := context.WithCancel(ctx)
	defer clipboardCancel()

	for {
		// Check for cancellation and clipboard changes
		select {
		case <-ctx.Done():
			return ctx.Err()
		case newClipboardState := <-clipboardCh:
			if err := agent.processClipboardState(newClipboardState); err != nil {
				return err
			}
			agent.lastClipboardState = newClipboardState
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

			// If we haven't successfully initialized clipboard yet,
			// this means SPICE was never connected - use special error
			// to signal longer backoff.
			if !agent.clipboardInitialized {
				return &ErrSPICENotConnected{Err: err}
			}

			return err
		}

		// First successful read from SPICE - now initialize clipboard
		if !agent.clipboardInitialized {
			if err := clipboard.Init(); err != nil {
				return fmt.Errorf("failed to initialize clipboard: %w", err)
			}
			clipboardCh = clipboard.Watch(clipboardCtx, clipboard.FmtText)
			agent.clipboardInitialized = true
			zap.S().Debug("SPICE connected, clipboard initialized")
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

			ourClipboardRequest := vd.VDAgentClipboardRequest{
				Type: 1,
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

			zap.S().Debugf("O: VD_AGENT_CLIPBOARD_REQUEST")
		case vd.VD_AGENT_CLIPBOARD:
			// Receive clipboard
			vdAgentClipboard, err := vd.DecodeVDAgentClipboard(vdiAgentMessage.Data)
			if err != nil {
				return err
			}

			zap.S().Debugf("I: VD_AGENT_CLIPBOARD: %s", vdAgentClipboard)

			clipboard.Write(clipboard.FmtText, vdAgentClipboard.Data)
		case vd.VD_AGENT_CLIPBOARD_REQUEST:
			vdAgentClipboardRequest, err := vd.DecodeVDAgentClipboardRequest(bytes.NewReader(vdiAgentMessage.Data))
			if err != nil {
				return err
			}

			zap.S().Debugf("I: VD_AGENT_CLIPBOARD_REQUEST: %s", vdAgentClipboardRequest)

			// Send clipboard
			ourAgentClipboard := vd.VDAgentClipboard{
				VDAgentClipboardInner: vd.VDAgentClipboardInner{
					Selection: vd.VD_AGENT_CLIPBOARD_SELECTION_CLIPBOARD,
					Type:      vd.VD_AGENT_CLIPBOARD_UTF8_TEXT,
				},
				Data: clipboard.Read(clipboard.FmtText),
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

			zap.S().Debugf("O: VD_AGENT_CLIPBOARD")
		default:
			zap.S().Debugf("I: unhandled message type: %d", vdiAgentMessage.Type)
		}
	}
}

func (agent *VDAgent) Close() error {
	return agent.serialPort.Close()
}

func (agent *VDAgent) processClipboardState(newClipboardState []byte) error {
	if bytes.Equal(agent.lastClipboardState, newClipboardState) {
		// Nothing changed since the last VD_AGENT_CLIPBOARD_GRAB from us
		return nil
	}

	ourGrab := vd.VDAgentClipboardGrab{
		Selection: vd.VD_AGENT_CLIPBOARD_SELECTION_CLIPBOARD,
		Type:      vd.VD_AGENT_CLIPBOARD_UTF8_TEXT,
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

	zap.S().Debugf("O: VD_AGENT_CLIPBOARD_GRAB")

	return nil

}
