package doctor

import (
	"fmt"
	"os/exec"
	"runtime"
	"strings"

	"golang.design/x/clipboard"
)

// CheckClipboard evaluates clipboard subsystem readiness, CLI utilities, and supported formats.
func CheckClipboard() CheckResult {
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

	res.Status = StatusOK
	res.Summary = "Text and Image clipboard active"
	res.Details = strings.Join(details, "\n")
	return res
}
