package vdagent

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/cirruslabs/tart-guest-agent/internal/settings"
	"github.com/cirruslabs/tart-guest-agent/internal/spice/filexfer"
	"github.com/cirruslabs/tart-guest-agent/internal/spice/imageopt"
	"github.com/cirruslabs/tart-guest-agent/internal/spice/vd"
	"github.com/cirruslabs/tart-guest-agent/internal/spice/vdi"
	"go.uber.org/zap"
	"golang.design/x/clipboard"
	"golang.org/x/sync/errgroup"
)

// FindSerialPortPath locates the appropriate SPICE virtio-serial character device for the current OS.
func FindSerialPortPath() string {
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

// ClipboardManagerInfo represents a detected running clipboard manager.
type ClipboardManagerInfo struct {
	Name        string
	ProcessName string
	Description string
	PIDs        []string
}

// KnownClipboardManagers defines known third-party clipboard managers that can interfere with SPICE synchronization.
var KnownClipboardManagers = []struct {
	Name        string
	ProcessName string
	Description string
}{
	{Name: "Diodon", ProcessName: "diodon", Description: "GTK+ clipboard manager"},
	{Name: "CopyQ", ProcessName: "copyq", Description: "Advanced clipboard manager with history"},
	{Name: "GPaste", ProcessName: "gpaste-daemon", Description: "GNOME clipboard management daemon"},
	{Name: "GPaste", ProcessName: "gpaste", Description: "GNOME clipboard management tool"},
	{Name: "Parcellite", ProcessName: "parcellite", Description: "Lightweight GTK+ clipboard manager"},
	{Name: "ClipIt", ProcessName: "clipit", Description: "Lightweight GTK+ clipboard manager"},
	{Name: "XFCE Clipman", ProcessName: "xfce4-clipman", Description: "XFCE clipboard manager plugin"},
	{Name: "Greenclip", ProcessName: "greenclip", Description: "Rofi/Dmenu clipboard daemon"},
	{Name: "Clipman", ProcessName: "clipman", Description: "Wayland clipboard manager"},
	{Name: "Clipster", ProcessName: "clipster", Description: "Python clipboard manager"},
	{Name: "Klipper", ProcessName: "klipper", Description: "KDE clipboard tool"},
	{Name: "wl-clip-persist", ProcessName: "wl-clip-persist", Description: "Wayland clipboard persistence tool"},
}

// FindRunningClipboardManagers scans for active third-party clipboard manager processes.
func FindRunningClipboardManagers() []ClipboardManagerInfo {
	if runtime.GOOS != "linux" && runtime.GOOS != "darwin" {
		return nil
	}

	var found []ClipboardManagerInfo
	for _, mgr := range KnownClipboardManagers {
		cmd := exec.Command("pgrep", "-x", mgr.ProcessName)
		out, err := cmd.Output()
		if err == nil {
			pids := strings.Fields(string(out))
			if len(pids) > 0 {
				found = append(found, ClipboardManagerInfo{
					Name:        mgr.Name,
					ProcessName: mgr.ProcessName,
					Description: mgr.Description,
					PIDs:        pids,
				})
			}
		}
	}
	return found
}

type VDAgent struct {
	serialPort            *os.File
	vdi                   *vdi.VDI
	writeMu               sync.Mutex
	lastClipboardState    []byte
	lastClipboardType     uint32
	lastAdvertisedTypes   []uint32
	lastHostGrabTypes     []uint32
	lastRawImageState     []byte
	lastOptimizedImage    []byte
	isHostOwned           bool
	selfTextWritePending  bool
	selfImageWritePending bool
	clipMu                sync.Mutex
	fileXferMgr           *filexfer.Manager
	clipboardEnabled      bool
}

func New() (*VDAgent, error) {
	portPath := FindSerialPortPath()
	sp, err := os.OpenFile(portPath, os.O_RDWR, 0)
	if err != nil {
		return nil, err
	}

	if err := clipboard.Init(); err != nil {
		_ = sp.Close()
		return nil, fmt.Errorf("clipboard initialization failed: %w", err)
	}

	return &VDAgent{
		serialPort:       sp,
		vdi:              vdi.New(sp),
		fileXferMgr:      filexfer.NewManager(),
		clipboardEnabled: true,
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
	// Warn if third-party clipboard managers are active
	for _, mgr := range FindRunningClipboardManagers() {
		zap.S().Warnf("detected active clipboard manager '%s' (PID %s); third-party clipboard managers can cause grab echo loops or overwrite synchronized clipboard contents", mgr.Name, strings.Join(mgr.PIDs, ", "))
	}

	// Send initial capability announcement immediately on startup
	if err := agent.sendCapabilities(1); err != nil {
		return fmt.Errorf("failed to send initial capabilities: %w", err)
	}

	g, gCtx := errgroup.WithContext(ctx)

	// Goroutine 1: Guest -> Host Clipboard Watcher (only if clipboard is enabled)
	if agent.clipboardEnabled {
		g.Go(func() error {
			textCh := clipboard.Watch(gCtx, clipboard.FmtText)
			pollTicker := time.NewTicker(500 * time.Millisecond)
			defer pollTicker.Stop()

			var (
				lastRawImageState []byte
				lastImageState    []byte
				lastImageValid    bool
			)

			for {
				select {
				case <-gCtx.Done():
					return gCtx.Err()
				case <-pollTicker.C:
					formats := clipboard.Formats()
					var (
						lastTextValid     bool
						textChanged       bool
						imageChanged      bool
						candidateRawImage []byte
						candidateOptImage []byte
						isImageEcho       bool
					)
					if slices.Contains(formats, clipboard.FmtText) {
						rawText := clipboard.Read(clipboard.FmtText)
						if len(rawText) > 0 {
							lastTextValid = true
							agent.clipMu.Lock()
							isEcho := agent.selfTextWritePending && bytes.Equal(agent.lastClipboardState, rawText)
							if !isEcho && (!bytes.Equal(agent.lastClipboardState, rawText) || agent.lastClipboardType != vd.VD_AGENT_CLIPBOARD_UTF8_TEXT) {
								textChanged = true
							}
							agent.clipMu.Unlock()
						}
					}

					imageEnabled := true
					if s := settings.Get(); s != nil {
						imageEnabled = s.ImageClipboardEnabled
					}

					if imageEnabled && slices.Contains(formats, clipboard.FmtImage) {
						rawImg := clipboard.Read(clipboard.FmtImage)
						if len(rawImg) > 0 {
							if bytes.Equal(lastRawImageState, rawImg) {
								candidateRawImage = lastRawImageState
								candidateOptImage = lastImageState
								lastImageValid = len(lastImageState) > 0
							} else {
								if optimized, err := imageopt.OptimizeImage(rawImg); err == nil {
									lastImageValid = true
									candidateRawImage = rawImg
									candidateOptImage = optimized
									agent.clipMu.Lock()
									isImageEcho = agent.selfImageWritePending && bytes.Equal(agent.lastOptimizedImage, optimized)
									agent.clipMu.Unlock()

									if !isImageEcho && !bytes.Equal(lastImageState, optimized) {
										imageChanged = true
									}
								} else {
									lastImageValid = false
									zap.S().Warnf("ignoring unservable guest clipboard image (%d bytes): %v", len(rawImg), err)
								}
							}
						}
					}

					types := getAvailableGrabTypes(formats, lastImageValid, lastTextValid)

					agent.clipMu.Lock()
					hadContent := (agent.lastClipboardState != nil && len(agent.lastClipboardState) > 0) || len(agent.lastAdvertisedTypes) > 0
					sameTypes := slices.Equal(agent.lastAdvertisedTypes, types)
					isHost := agent.isHostOwned
					agent.clipMu.Unlock()

					if len(types) == 0 {
						if hadContent {
							releaseMsg := vd.VDAgentClipboardRelease{
								Selection: vd.VD_AGENT_CLIPBOARD_SELECTION_CLIPBOARD,
							}
							releaseBytes, err := releaseMsg.Encode()
							if err != nil {
								if gCtx.Err() != nil {
									return gCtx.Err()
								}
								return fmt.Errorf("failed to encode clipboard release: %w", err)
							}
							zap.S().Debugf("O: VD_AGENT_CLIPBOARD_RELEASE")
							if err := agent.writeMessage(vd.VD_AGENT_CLIPBOARD_RELEASE, releaseBytes); err != nil {
								if gCtx.Err() != nil {
									return gCtx.Err()
								}
								return fmt.Errorf("failed to write clipboard release: %w", err)
							}

							lastRawImageState = nil
							lastImageState = nil
							agent.clipMu.Lock()
							agent.lastClipboardState = nil
							agent.lastClipboardType = vd.VD_AGENT_CLIPBOARD_NONE
							agent.lastAdvertisedTypes = nil
							agent.isHostOwned = false
							agent.lastRawImageState = nil
							agent.lastOptimizedImage = nil
							agent.selfImageWritePending = false
							agent.clipMu.Unlock()
						}
						continue
					}

					// If host currently owns clipboard and no local clipboard content changed, suppress re-grabbing
					if isHost && !textChanged && !imageChanged {
						continue
					}

					// If advertised formats changed or local content changed, send fresh grab
					if (!isHost && !sameTypes) || (textChanged && lastTextValid) || (imageChanged && lastImageValid) {
						ourGrab := vd.VDAgentClipboardGrab{
							Selection: vd.VD_AGENT_CLIPBOARD_SELECTION_CLIPBOARD,
							Types:     types,
						}
						ourGrabBytes, err := ourGrab.Encode()
						if err != nil {
							if gCtx.Err() != nil {
								return gCtx.Err()
							}
							return fmt.Errorf("failed to encode clipboard grab: %w", err)
						}
						zap.S().Debugf("O: VD_AGENT_CLIPBOARD_GRAB (types=%v)", types)
						if err := agent.writeMessage(vd.VD_AGENT_CLIPBOARD_GRAB, ourGrabBytes); err != nil {
							if gCtx.Err() != nil {
								return gCtx.Err()
							}
							return fmt.Errorf("failed to write clipboard grab: %w", err)
						}

						lastRawImageState = candidateRawImage
						lastImageState = candidateOptImage
						agent.clipMu.Lock()
						agent.lastAdvertisedTypes = types
						agent.isHostOwned = false
						agent.lastRawImageState = candidateRawImage
						agent.lastOptimizedImage = candidateOptImage
						if isImageEcho {
							agent.selfImageWritePending = false
						}
						agent.clipMu.Unlock()
					}

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

func hasServableClipboardFormat(formats []clipboard.Format) bool {
	for _, f := range formats {
		if f == clipboard.FmtText || f == clipboard.FmtImage {
			return true
		}
	}
	return false
}

func selectGrabRequestType(types []uint32) (uint32, bool) {
	imageEnabled := true
	if s := settings.Get(); s != nil {
		imageEnabled = s.ImageClipboardEnabled
	}

	if imageEnabled {
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

		formats := clipboard.Formats()
		var (
			baselineData []byte
			baselineType uint32 = vd.VD_AGENT_CLIPBOARD_NONE
		)
		if slices.Contains(formats, clipboard.FmtImage) {
			rawImg := clipboard.Read(clipboard.FmtImage)
			if len(rawImg) > 0 {
				baselineData = rawImg
				baselineType = vd.VD_AGENT_CLIPBOARD_IMAGE_PNG
			}
		}
		if baselineType == vd.VD_AGENT_CLIPBOARD_NONE && slices.Contains(formats, clipboard.FmtText) {
			rawText := clipboard.Read(clipboard.FmtText)
			if len(rawText) > 0 {
				baselineData = rawText
				baselineType = vd.VD_AGENT_CLIPBOARD_UTF8_TEXT
			}
		}

		agent.clipMu.Lock()
		agent.lastHostGrabTypes = vdAgentClipboardGrab.Types
		agent.lastAdvertisedTypes = nil
		agent.isHostOwned = true
		agent.lastClipboardState = baselineData
		agent.lastClipboardType = baselineType
		agent.selfImageWritePending = false
		agent.selfTextWritePending = false
		agent.clipMu.Unlock()

		reqType, ok := selectGrabRequestType(vdAgentClipboardGrab.Types)
		if !ok {
			zap.S().Debugf("clearing stale clipboard on grab with unsupported format types: %v", vdAgentClipboardGrab.Types)
			agent.clipMu.Lock()
			agent.isHostOwned = true
			agent.lastAdvertisedTypes = nil
			agent.lastClipboardState = nil
			agent.lastClipboardType = vd.VD_AGENT_CLIPBOARD_NONE
			agent.clipMu.Unlock()

			clipboard.Write(clipboard.FmtText, nil)
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

		agent.clipMu.Lock()
		if !agent.isHostOwned {
			agent.clipMu.Unlock()
			zap.S().Debugf("ignoring stale host clipboard reply; guest has reclaimed clipboard ownership")
			return nil
		}
		agent.clipMu.Unlock()

		switch vdAgentClipboard.Type {
		case vd.VD_AGENT_CLIPBOARD_NONE:
			agent.clipMu.Lock()
			wasHostOwned := agent.isHostOwned
			agent.isHostOwned = false
			if wasHostOwned {
				agent.lastClipboardState = nil
				agent.lastClipboardType = vd.VD_AGENT_CLIPBOARD_NONE
				agent.lastAdvertisedTypes = nil
			}
			agent.clipMu.Unlock()

			if wasHostOwned {
				clipboard.Write(clipboard.FmtText, nil)
				zap.S().Debugf("Cleared agent-owned clipboard on VD_AGENT_CLIPBOARD_NONE")
			}
		case vd.VD_AGENT_CLIPBOARD_IMAGE_PNG, vd.VD_AGENT_CLIPBOARD_IMAGE_BMP, vd.VD_AGENT_CLIPBOARD_IMAGE_TIFF, vd.VD_AGENT_CLIPBOARD_IMAGE_JPG:
			optimized, err := imageopt.OptimizeImage(vdAgentClipboard.Data)
			if err != nil {
				zap.S().Warnf("ignoring invalid/unsafe incoming clipboard image (%d bytes): %v", len(vdAgentClipboard.Data), err)
				agent.clipMu.Lock()
				hostGrabTypes := agent.lastHostGrabTypes
				agent.clipMu.Unlock()

				// If host grab also offered UTF8_TEXT, fall back to requesting text representation
				if slices.Contains(hostGrabTypes, vd.VD_AGENT_CLIPBOARD_UTF8_TEXT) {
					zap.S().Debugf("falling back to text request after rejected incoming image")
					textReq := vd.VDAgentClipboardRequest{
						Selection: vdAgentClipboard.Selection,
						Type:      vd.VD_AGENT_CLIPBOARD_UTF8_TEXT,
					}
					textReqBytes, err := textReq.Encode()
					if err == nil {
						return agent.writeMessage(vd.VD_AGENT_CLIPBOARD_REQUEST, textReqBytes)
					}
				}

				agent.clipMu.Lock()
				wasHostOwned := agent.isHostOwned
				agent.isHostOwned = false
				if wasHostOwned {
					agent.lastClipboardState = nil
					agent.lastClipboardType = vd.VD_AGENT_CLIPBOARD_NONE
					agent.lastAdvertisedTypes = nil
				}
				agent.clipMu.Unlock()

				if wasHostOwned {
					clipboard.Write(clipboard.FmtText, nil)
				}
				return nil
			}

			agent.clipMu.Lock()
			if !agent.isHostOwned {
				agent.clipMu.Unlock()
				zap.S().Debugf("ignoring stale host image reply; guest has reclaimed clipboard ownership")
				return nil
			}
			agent.lastClipboardState = optimized
			agent.lastClipboardType = vd.VD_AGENT_CLIPBOARD_IMAGE_PNG
			agent.lastAdvertisedTypes = []uint32{vd.VD_AGENT_CLIPBOARD_IMAGE_PNG}
			agent.lastRawImageState = optimized
			agent.lastOptimizedImage = optimized
			agent.isHostOwned = true
			agent.selfImageWritePending = true
			agent.clipMu.Unlock()

			clipboard.Write(clipboard.FmtImage, optimized)
			zap.S().Debugf("Wrote image clipboard data (%d bytes -> %d bytes)", len(vdAgentClipboard.Data), len(optimized))
		case vd.VD_AGENT_CLIPBOARD_UTF8_TEXT:
			agent.clipMu.Lock()
			if !agent.isHostOwned {
				agent.clipMu.Unlock()
				zap.S().Debugf("ignoring stale host text reply; guest has reclaimed clipboard ownership")
				return nil
			}
			agent.lastClipboardState = vdAgentClipboard.Data
			agent.lastClipboardType = vd.VD_AGENT_CLIPBOARD_UTF8_TEXT
			agent.lastAdvertisedTypes = []uint32{vd.VD_AGENT_CLIPBOARD_UTF8_TEXT}
			agent.isHostOwned = true
			agent.selfTextWritePending = true
			agent.clipMu.Unlock()

			clipboard.Write(clipboard.FmtText, vdAgentClipboard.Data)
			zap.S().Debugf("Wrote text clipboard data (%d bytes)", len(vdAgentClipboard.Data))
		default:
			zap.S().Warnf("ignoring unsupported clipboard data type %d", vdAgentClipboard.Type)
		}

	case vd.VD_AGENT_CLIPBOARD_RELEASE:
		if !agent.clipboardEnabled {
			zap.S().Debugf("ignoring VD_AGENT_CLIPBOARD_RELEASE because clipboard is disabled")
			return nil
		}

		vdAgentClipboardRelease, err := vd.DecodeVDAgentClipboardRelease(bytes.NewReader(vdiAgentMessage.Data))
		if err != nil {
			return err
		}

		zap.S().Debugf("I: VD_AGENT_CLIPBOARD_RELEASE (%d bytes, selection=%d)",
			len(vdiAgentMessage.Data), vdAgentClipboardRelease.Selection)

		if vdAgentClipboardRelease.Selection != vd.VD_AGENT_CLIPBOARD_SELECTION_CLIPBOARD {
			zap.S().Debugf("ignoring release for non-clipboard selection %d", vdAgentClipboardRelease.Selection)
			return nil
		}

		agent.clipMu.Lock()
		wasHostOwned := agent.isHostOwned
		agent.isHostOwned = false
		if wasHostOwned {
			agent.lastClipboardState = nil
			agent.lastClipboardType = vd.VD_AGENT_CLIPBOARD_NONE
			agent.lastAdvertisedTypes = nil
		}
		agent.clipMu.Unlock()

		if wasHostOwned {
			clipboard.Write(clipboard.FmtText, nil)
			zap.S().Debugf("Cleared agent-owned clipboard on VD_AGENT_CLIPBOARD_RELEASE")
		} else {
			zap.S().Debugf("Preserved newer guest-owned clipboard on VD_AGENT_CLIPBOARD_RELEASE")
		}
		return nil

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
			imageEnabled := true
			if s := settings.Get(); s != nil {
				imageEnabled = s.ImageClipboardEnabled
			}
			if !imageEnabled {
				zap.S().Debugf("ignoring image clipboard request because image clipboard is disabled in settings")
				return agent.sendClipboardData(vdAgentClipboardRequest.Selection, vd.VD_AGENT_CLIPBOARD_NONE, nil)
			}
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
		agent.clipMu.Lock()
		agent.lastAdvertisedTypes = nil
		agent.lastHostGrabTypes = nil
		agent.lastClipboardState = nil
		agent.lastClipboardType = vd.VD_AGENT_CLIPBOARD_NONE
		agent.lastRawImageState = nil
		agent.lastOptimizedImage = nil
		agent.isHostOwned = false
		agent.selfTextWritePending = false
		agent.selfImageWritePending = false
		agent.clipMu.Unlock()
	default:
		zap.S().Debugf("I: unhandled message type: %d", vdiAgentMessage.Type)
	}
	return nil
}

func (agent *VDAgent) Close() error {
	agent.fileXferMgr.Close()
	return agent.serialPort.Close()
}

func getAvailableGrabTypes(formats []clipboard.Format, isImageValid bool, isTextValid bool) []uint32 {
	var types []uint32
	imageEnabled := true
	if s := settings.Get(); s != nil {
		imageEnabled = s.ImageClipboardEnabled
	}
	if imageEnabled && slices.Contains(formats, clipboard.FmtImage) && isImageValid {
		types = append(types, vd.VD_AGENT_CLIPBOARD_IMAGE_PNG)
	}
	if slices.Contains(formats, clipboard.FmtText) && isTextValid {
		types = append(types, vd.VD_AGENT_CLIPBOARD_UTF8_TEXT)
	}
	return types
}

func (agent *VDAgent) processClipboardState(newClipboardState []byte, clipType uint32) error {
	formats := clipboard.Formats()
	var (
		isImageValid      bool
		candidateRawImage []byte
		candidateOptImage []byte
	)
	imageEnabled := true
	if s := settings.Get(); s != nil {
		imageEnabled = s.ImageClipboardEnabled
	}
	if imageEnabled && slices.Contains(formats, clipboard.FmtImage) {
		rawImg := clipboard.Read(clipboard.FmtImage)
		if len(rawImg) > 0 {
			agent.clipMu.Lock()
			cachedRaw := agent.lastRawImageState
			cachedOpt := agent.lastOptimizedImage
			agent.clipMu.Unlock()

			if len(cachedOpt) > 0 && bytes.Equal(cachedRaw, rawImg) {
				isImageValid = true
				candidateRawImage = cachedRaw
				candidateOptImage = cachedOpt
			} else {
				if optimized, err := imageopt.OptimizeImage(rawImg); err == nil {
					isImageValid = true
					candidateRawImage = rawImg
					candidateOptImage = optimized
				} else {
					zap.S().Warnf("ignoring unservable guest clipboard image in text update (%d bytes): %v", len(rawImg), err)
				}
			}
		}
	}

	hasText := len(newClipboardState) > 0 && clipType == vd.VD_AGENT_CLIPBOARD_UTF8_TEXT
	types := getAvailableGrabTypes(formats, isImageValid, hasText)
	if hasText && !slices.Contains(types, vd.VD_AGENT_CLIPBOARD_UTF8_TEXT) {
		types = append(types, vd.VD_AGENT_CLIPBOARD_UTF8_TEXT)
	}

	agent.clipMu.Lock()
	isSelfWrite := agent.selfTextWritePending &&
		bytes.Equal(agent.lastClipboardState, newClipboardState) &&
		agent.lastClipboardType == clipType &&
		slices.Equal(agent.lastAdvertisedTypes, types)

	if isSelfWrite {
		agent.selfTextWritePending = false
		agent.clipMu.Unlock()
		zap.S().Debugf("suppressing self-write echo grab for inbound host text clipboard")
		return nil
	}

	if !agent.isHostOwned &&
		bytes.Equal(agent.lastClipboardState, newClipboardState) &&
		agent.lastClipboardType == clipType &&
		slices.Equal(agent.lastAdvertisedTypes, types) {
		agent.clipMu.Unlock()
		return nil
	}
	agent.clipMu.Unlock()

	if len(types) == 0 {
		zap.S().Debugf("no servable clipboard formats available, releasing clipboard")
		releaseMsg := vd.VDAgentClipboardRelease{
			Selection: vd.VD_AGENT_CLIPBOARD_SELECTION_CLIPBOARD,
		}
		releaseBytes, err := releaseMsg.Encode()
		if err != nil {
			return err
		}
		zap.S().Debugf("O: VD_AGENT_CLIPBOARD_RELEASE")
		if err := agent.writeMessage(vd.VD_AGENT_CLIPBOARD_RELEASE, releaseBytes); err != nil {
			return err
		}

		agent.clipMu.Lock()
		agent.selfTextWritePending = false
		agent.lastClipboardState = nil
		agent.lastClipboardType = vd.VD_AGENT_CLIPBOARD_NONE
		agent.lastAdvertisedTypes = nil
		agent.isHostOwned = false
		agent.lastRawImageState = nil
		agent.lastOptimizedImage = nil
		agent.clipMu.Unlock()
		return nil
	}

	ourGrab := vd.VDAgentClipboardGrab{
		Selection: vd.VD_AGENT_CLIPBOARD_SELECTION_CLIPBOARD,
		Types:     types,
	}
	ourGrabBytes, err := ourGrab.Encode()
	if err != nil {
		return err
	}

	zap.S().Debugf("O: VD_AGENT_CLIPBOARD_GRAB (types=%v)", types)
	if err := agent.writeMessage(vd.VD_AGENT_CLIPBOARD_GRAB, ourGrabBytes); err != nil {
		return err
	}

	agent.clipMu.Lock()
	agent.selfTextWritePending = false
	agent.lastClipboardState = newClipboardState
	agent.lastClipboardType = clipType
	agent.lastAdvertisedTypes = types
	agent.isHostOwned = false
	if isImageValid {
		agent.lastRawImageState = candidateRawImage
		agent.lastOptimizedImage = candidateOptImage
	} else {
		agent.lastRawImageState = nil
		agent.lastOptimizedImage = nil
	}
	agent.clipMu.Unlock()
	return nil
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
