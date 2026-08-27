package doctor

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"

	"github.com/cirruslabs/tart-guest-agent/internal/spice/vdagent"
)

// CheckSpicePort verifies the presence and accessibility of the SPICE virtio-serial device.
func CheckSpicePort() CheckResult {
	res := CheckResult{
		Category: "SPICE",
		Name:     "SPICE Serial Port",
	}

	portPath := vdagent.FindSerialPortPath()
	if _, err := os.Stat(portPath); err != nil {
		res.Status = StatusError
		res.Summary = "SPICE serial port not found"
		res.Details = fmt.Sprintf("Checked path: %s (%v)", portPath, err)
		if runtime.GOOS == "linux" {
			res.Remediation = "Verify that the VM is running inside Tart and that virtio-serial device is configured."
		} else {
			res.Remediation = "Verify Tart SPICE serial port /dev/tty.com.redhat.spice.0 is available."
		}
		return res
	}

	// Resolve symlinks if any
	resolvedPath, err := filepath.EvalSymlinks(portPath)
	if err == nil && resolvedPath != portPath {
		res.Details = fmt.Sprintf("Resolved port: %s -> %s", portPath, resolvedPath)
	} else {
		res.Details = fmt.Sprintf("Port path: %s", portPath)
	}

	// Try opening port read-write
	f, err := os.OpenFile(portPath, os.O_RDWR, 0)
	if err != nil {
		if errors.Is(err, syscall.EBUSY) {
			if isHeldByTartAgent() {
				res.Status = StatusOK
				res.Summary = fmt.Sprintf("%s is connected (in use by active tart-guest-agent daemon)", portPath)
				res.Details += "\nDaemon: tart-guest-agent / tart-vdagent.service active"
				return res
			}
			res.Status = StatusError
			res.Summary = fmt.Sprintf("%s is BUSY (held by another process)", portPath)
			res.Remediation = "Stop conflicting service: run 'sudo systemctl disable --now spice-vdagentd' or check running processes."
			return res
		}
		if os.IsPermission(err) {
			res.Status = StatusError
			res.Summary = fmt.Sprintf("%s permission denied", portPath)
			res.Remediation = fmt.Sprintf("Add user to dialout group: 'sudo usermod -aG dialout %s' or adjust udev permissions.", os.Getenv("USER"))
			return res
		}
		res.Status = StatusWarn
		res.Summary = fmt.Sprintf("%s cannot be opened: %v", portPath, err)
		res.Remediation = "Ensure the current user has read/write permissions on the SPICE serial device."
		return res
	}
	_ = f.Close()

	res.Status = StatusOK
	res.Summary = fmt.Sprintf("%s is accessible (read/write)", portPath)
	return res
}

// CheckConflictingDaemons checks for legacy or conflicting spice-vdagentd services.
func CheckConflictingDaemons() CheckResult {
	res := CheckResult{
		Category: "SPICE",
		Name:     "Conflicting Daemons",
	}

	if runtime.GOOS != "linux" {
		res.Status = StatusOK
		res.Summary = "None detected"
		return res
	}

	// Check if systemd has spice-vdagentd active
	cmd := exec.Command("systemctl", "is-active", "spice-vdagentd")
	out, err := cmd.Output()
	statusStr := strings.TrimSpace(string(out))

	if err == nil && statusStr == "active" {
		res.Status = StatusError
		res.Summary = "conflicting spice-vdagentd service is running"
		res.Details = "spice-vdagentd holds exclusive lock on /dev/virtio-ports/com.redhat.spice.0, blocking tart-guest-agent."
		res.Remediation = "Disable and stop conflicting service: run 'sudo systemctl disable --now spice-vdagentd'"
		return res
	}

	// Check for any running spice-vdagent processes
	pgrepCmd := exec.Command("pgrep", "-f", "spice-vdagentd")
	if pgrepOut, pgrepErr := pgrepCmd.Output(); pgrepErr == nil && len(strings.TrimSpace(string(pgrepOut))) > 0 {
		res.Status = StatusError
		res.Summary = "spice-vdagentd process detected"
		res.Details = fmt.Sprintf("Process PID(s): %s", strings.TrimSpace(string(pgrepOut)))
		res.Remediation = "Kill and disable spice-vdagentd: 'sudo killall -9 spice-vdagentd && sudo systemctl disable --now spice-vdagentd'"
		return res
	}

	res.Status = StatusOK
	res.Summary = "No conflicting daemons running"
	return res
}

func isHeldByTartAgent() bool {
	if runtime.GOOS == "linux" {
		if out, err := exec.Command("systemctl", "--user", "is-active", "tart-vdagent.service").Output(); err == nil && strings.TrimSpace(string(out)) == "active" {
			return true
		}
		if out, err := exec.Command("systemctl", "is-active", "tart-guest-agent.service").Output(); err == nil && strings.TrimSpace(string(out)) == "active" {
			return true
		}
	}
	if out, err := exec.Command("pgrep", "-f", "tart-guest-agent").Output(); err == nil {
		pids := strings.Fields(string(out))
		myPid := fmt.Sprintf("%d", os.Getpid())
		for _, pid := range pids {
			if pid != myPid {
				return true
			}
		}
	}
	return false
}
