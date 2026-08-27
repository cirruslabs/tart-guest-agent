package doctor

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// CheckDisplaySession inspects the active graphical session, Wayland, XWayland, and DBus environment.
func CheckDisplaySession() CheckResult {
	res := CheckResult{
		Category: "Display",
		Name:     "Display Session",
	}

	if runtime.GOOS == "darwin" {
		res.Status = StatusOK
		res.Summary = "macOS WindowServer active"
		res.Details = "Native Cocoa GUI session"
		return res
	}

	if runtime.GOOS != "linux" {
		res.Status = StatusOK
		res.Summary = fmt.Sprintf("%s display session", runtime.GOOS)
		return res
	}

	// Linux Environment Analysis
	waylandDisplay := os.Getenv("WAYLAND_DISPLAY")
	display := os.Getenv("DISPLAY")
	xdgRuntimeDir := os.Getenv("XDG_RUNTIME_DIR")
	sessionType := os.Getenv("XDG_SESSION_TYPE")
	desktop := os.Getenv("XDG_CURRENT_DESKTOP")

	var details []string
	if desktop != "" {
		details = append(details, fmt.Sprintf("Desktop: %s", desktop))
	}
	if sessionType != "" {
		details = append(details, fmt.Sprintf("Session Type: %s", sessionType))
	}

	// Check Wayland Socket
	var waylandOK bool
	if waylandDisplay != "" {
		details = append(details, fmt.Sprintf("WAYLAND_DISPLAY=%s", waylandDisplay))
		if xdgRuntimeDir != "" {
			sockPath := filepath.Join(xdgRuntimeDir, waylandDisplay)
			if info, err := os.Stat(sockPath); err == nil && (info.Mode()&os.ModeSocket != 0) {
				waylandOK = true
				details = append(details, fmt.Sprintf("Wayland Socket: %s (active)", sockPath))
			} else {
				details = append(details, fmt.Sprintf("Wayland Socket: %s (not found or not a socket)", sockPath))
			}
		}
	}

	// Check X11 / XWayland DISPLAY
	var x11OK bool
	if display != "" {
		details = append(details, fmt.Sprintf("DISPLAY=%s", display))
		// Check for socket in /tmp/.X11-unix/X0 or similar
		dispNum := strings.TrimPrefix(display, ":")
		dispNum = strings.Split(dispNum, ".")[0]
		xSockPath := filepath.Join("/tmp/.X11-unix", "X"+dispNum)
		if info, err := os.Stat(xSockPath); err == nil && (info.Mode()&os.ModeSocket != 0) {
			x11OK = true
			details = append(details, fmt.Sprintf("X11/XWayland Socket: %s (active)", xSockPath))
		} else {
			x11OK = true // In some containers/Wayland bridges, display socket might be in abstract namespace
			details = append(details, fmt.Sprintf("X11/XWayland Display: %s", display))
		}
	}

	res.Details = strings.Join(details, "\n")

	if waylandOK && x11OK {
		res.Status = StatusOK
		res.Summary = fmt.Sprintf("Wayland (%s) + XWayland (%s) active", waylandDisplay, display)
		return res
	}

	if waylandOK && !x11OK {
		res.Status = StatusWarn
		res.Summary = fmt.Sprintf("Wayland (%s) active, but DISPLAY is not set", waylandDisplay)
		res.Remediation = "Export 'DISPLAY=:0' or add 'Environment=DISPLAY=:0' to tart-vdagent.service (required for clipboard libX11 bridge)."
		return res
	}

	if !waylandOK && x11OK {
		res.Status = StatusOK
		res.Summary = fmt.Sprintf("X11 session (%s) active", display)
		return res
	}

	res.Status = StatusError
	res.Summary = "No active display session found (WAYLAND_DISPLAY and DISPLAY missing)"
	res.Remediation = "Ensure you are logged into a graphical desktop session (Wayland or X11), and that tart-vdagent is started within the user session."
	return res
}
