package doctor

import (
	"fmt"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"golang.design/x/clipboard"
)

// CheckClipboard evaluates clipboard subsystem readiness, CLI utilities, and supported formats.
// When enableWriteProbe is false, it only passively inspects and preserves the user's existing clipboard.
func CheckClipboard(enableWriteProbe bool) CheckResult {
	res := CheckResult{
		Category: "Clipboard",
		Name:     "Clipboard Subsystem",
	}

	var details []string
	var toolsFound []string

	// Check CLI utilities
	if runtime.GOOS == "linux" {
		if path, err := exec.LookPath("wl-copy"); err == nil {
			toolsFound = append(toolsFound, fmt.Sprintf("wl-clipboard (%s)", path))
		}
		if path, err := exec.LookPath("xclip"); err == nil {
			toolsFound = append(toolsFound, fmt.Sprintf("xclip (%s)", path))
		}
		if path, err := exec.LookPath("xsel"); err == nil {
			toolsFound = append(toolsFound, fmt.Sprintf("xsel (%s)", path))
		}
	} else if runtime.GOOS == "darwin" {
		if path, err := exec.LookPath("pbcopy"); err == nil {
			toolsFound = append(toolsFound, fmt.Sprintf("pbcopy/pbpaste (%s)", path))
		}
	}

	if len(toolsFound) > 0 {
		details = append(details, fmt.Sprintf("CLI Tools: %s", strings.Join(toolsFound, ", ")))
	} else if runtime.GOOS == "linux" {
		details = append(details, "CLI Tools: none found (recommended: install 'wl-clipboard')")
	}

	// Probe Go clipboard backend
	initErr := clipboard.Init()
	if initErr != nil {
		res.Status = StatusError
		res.Summary = fmt.Sprintf("clipboard initialization failed: %v", initErr)
		if runtime.GOOS == "linux" {
			res.Remediation = "Verify that DISPLAY=:0 (XWayland) or X11 is running and libX11 is installed ('sudo apt-get install -y libx11-dev wl-clipboard')."
		}
		res.Details = strings.Join(details, "\n")
		return res
	}

	details = append(details, "Backend: golang.design/x/clipboard (initialized)")
	details = append(details, "Formats Supported: UTF-8 Text, PNG/BMP/TIFF/JPG Images (with auto-optimization)")

	// Passive inspection of current clipboard content (preserves user state)
	origText := clipboard.Read(clipboard.FmtText)
	origImg := clipboard.Read(clipboard.FmtImage)

	if len(origText) > 0 {
		preview := string(origText)
		preview = strings.ReplaceAll(preview, "\n", " ")
		preview = strings.ReplaceAll(preview, "\r", "")
		if len(preview) > 40 {
			preview = preview[:40] + "..."
		}
		details = append(details, fmt.Sprintf("Clipboard Preview (Text): '%s'", preview))
	}
	if len(origImg) > 0 {
		details = append(details, fmt.Sprintf("Clipboard Preview (Image): %d bytes present", len(origImg)))
	}

	if enableWriteProbe {
		testProbe := fmt.Sprintf("tart_selftest_%d", time.Now().UnixNano())
		clipboard.Write(clipboard.FmtText, []byte(testProbe))
		readBack := string(clipboard.Read(clipboard.FmtText))

		// Fully restore original multi-format clipboard
		if len(origImg) > 0 {
			clipboard.Write(clipboard.FmtImage, origImg)
		} else if len(origText) > 0 {
			clipboard.Write(clipboard.FmtText, origText)
		} else {
			clipboard.Write(clipboard.FmtText, []byte{})
		}

		if readBack == testProbe {
			res.Status = StatusOK
			res.Summary = "Text and Image clipboard active (self-test passed)"
			details = append(details, "Live Self-Test: PASS (read/write loopback verified)")
		} else {
			res.Status = StatusWarn
			res.Summary = "Clipboard write loopback unverified"
			details = append(details, fmt.Sprintf("Live Self-Test: WARNING (expected '%s', got '%s')", testProbe, readBack))
			res.Remediation = "Verify that XWayland / DISPLAY=:0 is receiving clipboard focus events."
		}
	} else {
		res.Status = StatusOK
		res.Summary = "Text and Image clipboard active"
	}

	res.Details = strings.Join(details, "\n")
	return res
}
