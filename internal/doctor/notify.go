package doctor

import (
	"fmt"
	"os/exec"
	"runtime"
	"strings"
)

// Urgency defines desktop notification severity.
type Urgency string

const (
	UrgencyLow      Urgency = "low"
	UrgencyNormal   Urgency = "normal"
	UrgencyCritical Urgency = "critical"
)

// SendNotification sends a desktop notification across Linux and macOS environments.
func SendNotification(title string, message string, urgency Urgency) error {
	switch runtime.GOOS {
	case "linux":
		if path, err := exec.LookPath("notify-send"); err == nil {
			urg := string(urgency)
			if urg == "" {
				urg = string(UrgencyNormal)
			}
			cmd := exec.Command(path, "-a", "Tart Guest Agent", "-u", urg, title, message)
			return cmd.Run()
		}
	case "darwin":
		if path, err := exec.LookPath("osascript"); err == nil {
			script := fmt.Sprintf("display notification %q with title %q", message, title)
			cmd := exec.Command(path, "-e", script)
			return cmd.Run()
		}
	}
	return nil
}

// NotifyReport emits a desktop notification summarizing the diagnostic report.
func (r *Report) NotifyReport() error {
	var title string
	var urgency Urgency

	switch r.Overall {
	case StatusOK:
		title = "Tart Guest Agent: Healthy"
		urgency = UrgencyLow
	case StatusWarn:
		title = "Tart Guest Agent: Warnings Detected"
		urgency = UrgencyNormal
	case StatusError:
		title = "Tart Guest Agent: Setup Required"
		urgency = UrgencyCritical
	}

	var msgs []string
	for _, check := range r.Checks {
		if check.Status != StatusOK {
			msg := fmt.Sprintf("%s: %s", check.Name, check.Summary)
			if check.Remediation != "" {
				msg += fmt.Sprintf(" (Action: %s)", check.Remediation)
			}
			msgs = append(msgs, msg)
		}
	}

	if len(msgs) == 0 {
		return SendNotification(title, "All guest agent components ready and operational.", urgency)
	}

	return SendNotification(title, strings.Join(msgs, "\n"), urgency)
}
