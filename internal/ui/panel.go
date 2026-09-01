package ui

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/cirruslabs/tart-guest-agent/internal/activity"
	"github.com/cirruslabs/tart-guest-agent/internal/settings"
)

// FormatRecentNotifications formats the activity list for display in the UI panel.
func FormatRecentNotifications(events []activity.Event) string {
	if len(events) == 0 {
		return "No recent activity or notifications recorded."
	}

	var sb strings.Builder
	sb.WriteString("=== Recent Guest Agent Activity ===\n\n")

	for i, e := range events {
		if i >= 15 {
			sb.WriteString(fmt.Sprintf("... and %d older event(s)\n", len(events)-15))
			break
		}
		timeStr := e.Timestamp.Format("15:04:05")
		icon := categoryIcon(e.Category, e.Status)
		sb.WriteString(fmt.Sprintf("[%s] %s %s\n", timeStr, icon, e.Title))
		if e.Detail != "" {
			sb.WriteString(fmt.Sprintf("      ↳ %s\n", e.Detail))
		}
	}
	return sb.String()
}

func categoryIcon(cat activity.Category, status string) string {
	if status == "error" {
		return "❌"
	}
	if status == "warning" {
		return "⚠️"
	}
	switch cat {
	case activity.CategoryClipboardText:
		return "📋"
	case activity.CategoryClipboardImage:
		return "🖼️"
	case activity.CategoryFileTransfer:
		return "📁"
	case activity.CategoryDoctor:
		return "🩺"
	case activity.CategorySystem:
		return "⚡"
	default:
		return "ℹ️"
	}
}

// ShowNotificationsPanel displays the interactive notifications dialog.
func ShowNotificationsPanel() error {
	events := activity.List()
	text := FormatRecentNotifications(events)

	switch runtime.GOOS {
	case "darwin":
		escaped := strings.ReplaceAll(text, "\"", "\\\"")
		escaped = strings.ReplaceAll(escaped, "\n", "\\n")
		script := fmt.Sprintf(`display dialog "%s" with title "Tart Guest Agent — Notifications" buttons {"Clear History", "Open Downloads", "OK"} default button "OK" with icon note`, escaped)
		out, err := exec.Command("osascript", "-e", script).Output()
		if err == nil {
			response := string(out)
			if strings.Contains(response, "Clear History") {
				activity.Clear()
			} else if strings.Contains(response, "Open Downloads") {
				_ = OpenDownloadsFolder()
			}
		}
		return err
	case "linux":
		if path, err := exec.LookPath("zenity"); err == nil {
			return exec.Command(path, "--info", "--title=Tart Guest Agent — Notifications", "--text="+text, "--width=480", "--height=320").Run()
		}
		if path, err := exec.LookPath("kdialog"); err == nil {
			return exec.Command(path, "--title", "Tart Guest Agent — Notifications", "--msgbox", text).Run()
		}
		fmt.Println(text)
	}
	return nil
}

// ShowSettingsDialog presents an interactive settings dialog allowing the user to view and update preferences.
func ShowSettingsDialog() error {
	s := settings.Get()

	switch runtime.GOOS {
	case "darwin":
		currentSummary := fmt.Sprintf(
			"Tart Guest Agent Settings:\n\n"+
				"• Desktop Notifications: %s\n"+
				"• Image Clipboard Sync: %s\n"+
				"• File Transfer Daemon: %s\n"+
				"• Auto Disk Resizing: %s\n"+
				"• Downloads Folder: %s\n\n"+
				"Toggle a feature or change preferences:",
			boolStatus(s.NotificationsEnabled),
			boolStatus(s.ImageClipboardEnabled),
			boolStatus(s.FileTransferEnabled),
			boolStatus(s.AutoResizeEnabled),
			s.DownloadDir,
		)
		escaped := strings.ReplaceAll(currentSummary, "\"", "\\\"")
		escaped = strings.ReplaceAll(escaped, "\n", "\\n")

		script := fmt.Sprintf(`choose from list {"Toggle Notifications (%s)", "Toggle Image Clipboard (%s)", "Toggle File Transfer (%s)", "Toggle Auto Disk Resize (%s)", "Reset to Defaults"} with title "Tart Guest Agent — Settings" with prompt "%s" OK button name "Toggle" cancel button name "Close"`,
			toggleAction(s.NotificationsEnabled),
			toggleAction(s.ImageClipboardEnabled),
			toggleAction(s.FileTransferEnabled),
			toggleAction(s.AutoResizeEnabled),
			escaped,
		)

		out, err := exec.Command("osascript", "-e", script).Output()
		if err != nil {
			return nil // User clicked Close / Cancel
		}

		choice := strings.TrimSpace(string(out))
		if strings.Contains(choice, "Notifications") {
			s.NotificationsEnabled = !s.NotificationsEnabled
		} else if strings.Contains(choice, "Image Clipboard") {
			s.ImageClipboardEnabled = !s.ImageClipboardEnabled
		} else if strings.Contains(choice, "File Transfer") {
			s.FileTransferEnabled = !s.FileTransferEnabled
		} else if strings.Contains(choice, "Auto Disk Resize") {
			s.AutoResizeEnabled = !s.AutoResizeEnabled
		} else if strings.Contains(choice, "Reset to Defaults") {
			s = settings.DefaultSettings()
		}

		if err := settings.Save(s); err != nil {
			return err
		}
		activity.Record(activity.CategorySystem, "Settings updated", fmt.Sprintf("Notifications: %v, Images: %v, FileXfer: %v", s.NotificationsEnabled, s.ImageClipboardEnabled, s.FileTransferEnabled), "info")
		return nil

	case "linux":
		if path, err := exec.LookPath("zenity"); err == nil {
			summary := fmt.Sprintf("Current Settings:\nNotifications: %v\nImage Clipboard: %v\nFile Transfer: %v\nDownloads: %s",
				s.NotificationsEnabled, s.ImageClipboardEnabled, s.FileTransferEnabled, s.DownloadDir)
			return exec.Command(path, "--info", "--title=Tart Guest Agent — Settings", "--text="+summary).Run()
		}
		fmt.Printf("Current Settings: %+v\n", s)
	}
	return nil
}

// ShowDoctorDialog displays diagnostic results in an interactive dialog.
func ShowDoctorDialog(reportText string, overall string) error {
	activity.Record(activity.CategoryDoctor, "Diagnostics run", fmt.Sprintf("Status: %s", overall), "info")

	switch runtime.GOOS {
	case "darwin":
		escaped := strings.ReplaceAll(reportText, "\"", "\\\"")
		escaped = strings.ReplaceAll(escaped, "\n", "\\n")
		script := fmt.Sprintf(`display dialog "%s" with title "Tart Guest Agent — Diagnostics" buttons {"OK"} default button "OK" with icon note`, escaped)
		return exec.Command("osascript", "-e", script).Run()
	case "linux":
		if path, err := exec.LookPath("zenity"); err == nil {
			return exec.Command(path, "--info", "--title=Tart Guest Agent — Diagnostics", "--text="+reportText, "--width=480", "--height=340").Run()
		}
	}
	fmt.Println(reportText)
	return nil
}

// OpenDownloadsFolder opens the configured downloads directory in the system file manager.
func OpenDownloadsFolder() error {
	dir := settings.Get().DownloadDir
	if dir == "" {
		home, _ := os.UserHomeDir()
		dir = filepath.Join(home, "Downloads")
	}

	switch runtime.GOOS {
	case "darwin":
		return exec.Command("open", dir).Start()
	case "linux":
		return exec.Command("xdg-open", dir).Start()
	}
	return nil
}

func boolStatus(val bool) string {
	if val {
		return "ENABLED [✓]"
	}
	return "DISABLED [✗]"
}

func toggleAction(current bool) string {
	if current {
		return "Disable"
	}
	return "Enable"
}
