package notify

import (
	"fmt"
	"os/exec"
	"runtime"

	"github.com/cirruslabs/tart-guest-agent/internal/activity"
	"github.com/cirruslabs/tart-guest-agent/internal/settings"
)

// Urgency defines desktop notification severity.
type Urgency string

const (
	UrgencyLow      Urgency = "low"
	UrgencyNormal   Urgency = "normal"
	UrgencyCritical Urgency = "critical"
)

// Send sends a desktop notification if notifications are enabled in settings.
func Send(title string, message string, urgency Urgency) error {
	s := settings.Get()
	if s != nil && !s.NotificationsEnabled {
		return nil
	}
	return SendDirect(title, message, urgency)
}

// SendDirect dispatches a desktop notification directly.
func SendDirect(title string, message string, urgency Urgency) error {
	switch runtime.GOOS {
	case "darwin":
		if path, err := exec.LookPath("osascript"); err == nil {
			script := fmt.Sprintf("display notification %q with title %q", message, title)
			cmd := exec.Command(path, "-e", script)
			return cmd.Run()
		}
	case "linux":
		if path, err := exec.LookPath("notify-send"); err == nil {
			urg := string(urgency)
			if urg == "" {
				urg = string(UrgencyNormal)
			}
			cmd := exec.Command(path, "-a", "Tart Guest Agent", "-u", urg, title, message)
			return cmd.Run()
		}
	}
	return nil
}

// FileTransferCompleted records and notifies of a completed file transfer.
func FileTransferCompleted(fileName string, fileSize int64) {
	activity.Record(activity.CategoryFileTransfer, fmt.Sprintf("Received %s", fileName), fmt.Sprintf("Size: %d bytes", fileSize), "success")
	_ = Send("File Received", fmt.Sprintf("Saved %s to Downloads (%d bytes)", fileName, fileSize), UrgencyNormal)
}

// StartupToast emits a notification when the guest services start.
func StartupToast() {
	s := settings.Get()
	if s != nil && s.StartupNotification && s.NotificationsEnabled {
		_ = Send("Tart Guest Agent", "Guest services active (Clipboard & File Transfer ready).", UrgencyLow)
	}
}

// ClipboardSync records an image clipboard sync event.
func ClipboardSync(format string, size int) {
	activity.Record(activity.CategoryClipboardImage, fmt.Sprintf("Synced image (%s)", format), fmt.Sprintf("Size: %d bytes", size), "info")
}
