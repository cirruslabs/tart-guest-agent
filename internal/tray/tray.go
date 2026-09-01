package tray

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/cirruslabs/tart-guest-agent/internal/activity"
	"github.com/cirruslabs/tart-guest-agent/internal/doctor"
	"github.com/cirruslabs/tart-guest-agent/internal/notify"
	"github.com/cirruslabs/tart-guest-agent/internal/settings"
	"github.com/cirruslabs/tart-guest-agent/internal/ui"
	"github.com/cirruslabs/tart-guest-agent/internal/version"
	"go.uber.org/zap"
)

// Status represents the operational state of the guest agent.
type Status string

const (
	StatusActive    Status = "Active"
	StatusConnected Status = "Connected"
	StatusStandby   Status = "Standby"
	StatusError     Status = "Error"
)

// Tray manages the system tray / menu bar item and interactive actions.
type Tray struct {
	mu     sync.RWMutex
	status Status
}

// New creates a new Tray controller.
func New() *Tray {
	return &Tray{
		status: StatusActive,
	}
}

// SetStatus updates the status label shown in the tray menu.
func (t *Tray) SetStatus(s Status) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.status = s
	activity.Record(activity.CategorySystem, fmt.Sprintf("Status changed to %s", s), "", "info")
}

// Status returns the current operational status.
func (t *Tray) Status() Status {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.status
}

// MenuItems returns the list of formatted menu items for display.
func (t *Tray) MenuItems() []string {
	t.mu.RLock()
	defer t.mu.RUnlock()

	statusIcon := "🟢"
	if t.status == StatusError {
		statusIcon = "🔴"
	} else if t.status == StatusStandby {
		statusIcon = "🟡"
	}

	return []string{
		fmt.Sprintf("⚡ Tart Guest Agent (%s)", version.Version),
		fmt.Sprintf("%s Status: %s", statusIcon, t.status),
		"---",
		"🔔 Recent Activity & Notifications...",
		"⚙️ Settings & Preferences...",
		"🩺 Run Diagnostics (Doctor)...",
		"📂 Open Downloads Folder",
		"---",
		"❌ Quit",
	}
}

// HandleAction executes the requested action triggered from the tray menu.
func (t *Tray) HandleAction(action string) error {
	zap.S().Debugf("Tray menu action triggered: %s", action)

	switch action {
	case "notifications", "activity":
		return ui.ShowNotificationsPanel()
	case "settings":
		return ui.ShowSettingsDialog()
	case "doctor":
		report := doctor.RunDiagnosticsWithSelfTest(true)
		var sb strings.Builder
		sb.WriteString(fmt.Sprintf("=== Diagnostic Report (%s %s) ===\n\n", report.OS, report.Arch))
		for _, check := range report.Checks {
			sb.WriteString(fmt.Sprintf("%s %s: %s\n", check.Status, check.Name, check.Summary))
			if check.Remediation != "" {
				sb.WriteString(fmt.Sprintf("   ↳ Action: %s\n", check.Remediation))
			}
		}
		return ui.ShowDoctorDialog(sb.String(), report.Overall.String())
	case "downloads":
		return ui.OpenDownloadsFolder()
	default:
		return fmt.Errorf("unknown tray action: %s", action)
	}
}

// Run starts the tray controller and listens for context cancellation.
func (t *Tray) Run(ctx context.Context) error {
	zap.S().Infof("Starting Tart Guest Agent tray service (version %s)", version.Version)

	// Emit startup notification if enabled
	s := settings.Get()
	if s.StartupNotification && s.NotificationsEnabled {
		_ = notify.Send("Tart Guest Agent", "Agent active — Multi-format clipboard and SPICE file transfer ready.", notify.UrgencyLow)
	}

	activity.Record(activity.CategorySystem, "Tart Guest Agent started", fmt.Sprintf("Version %s", version.Version), "success")

	// Run platform-specific tray loop or monitor context
	select {
	case <-ctx.Done():
		zap.S().Infof("Stopping Tart Guest Agent tray service")
		return ctx.Err()
	}
}

// EmitStartupToast emits a desktop notification on agent startup.
func EmitStartupToast() {
	notify.StartupToast()
}

// EmitFileTransferNotification emits a notification when a file transfer completes.
func EmitFileTransferNotification(fileName string, fileSize int64) {
	notify.FileTransferCompleted(fileName, fileSize)
}

// EmitClipboardSyncNotification records a clipboard sync event.
func EmitClipboardSyncNotification(format string, size int) {
	notify.ClipboardSync(format, size)
}
