package doctor

import (
	"fmt"
	"io"
	"os"
	"runtime"
	"strings"
)

type Status int

const (
	StatusOK Status = iota
	StatusWarn
	StatusError
)

func (s Status) String() string {
	switch s {
	case StatusOK:
		return "[✓]"
	case StatusWarn:
		return "[!]"
	case StatusError:
		return "[✗]"
	default:
		return "[?]"
	}
}

// CheckResult represents the outcome of an individual diagnostic check.
type CheckResult struct {
	Category    string
	Name        string
	Status      Status
	Summary     string
	Details     string
	Remediation string
}

// Capabilities represents the detected operational capabilities of the agent.
type Capabilities struct {
	TextClipboard  bool
	ImageClipboard bool
	FileTransfer   bool
	DisplaySession bool
	SerialPort     bool
}

// Report aggregates all diagnostic check results and capabilities.
type Report struct {
	OS           string
	Arch         string
	Checks       []CheckResult
	Capabilities Capabilities
	Overall      Status
}

// RunDiagnostics executes passive diagnostic probes without mutating user state.
func RunDiagnostics() *Report {
	return RunDiagnosticsWithSelfTest(false)
}

// RunDiagnosticsWithSelfTest executes diagnostic probes with optional active write loopback self-testing.
func RunDiagnosticsWithSelfTest(enableSelfTest bool) *Report {
	report := &Report{
		OS:      runtime.GOOS,
		Arch:    runtime.GOARCH,
		Overall: StatusOK,
	}

	// 1. SPICE Serial Port Check
	spiceCheck := CheckSpicePort()
	report.Checks = append(report.Checks, spiceCheck)
	if spiceCheck.Status == StatusOK {
		report.Capabilities.SerialPort = true
	} else if spiceCheck.Status == StatusError {
		report.Overall = StatusError
	} else if spiceCheck.Status == StatusWarn && report.Overall != StatusError {
		report.Overall = StatusWarn
	}

	// 2. Conflicting Daemons Check
	conflictCheck := CheckConflictingDaemons()
	report.Checks = append(report.Checks, conflictCheck)
	if conflictCheck.Status == StatusError {
		report.Overall = StatusError
	} else if conflictCheck.Status == StatusWarn && report.Overall != StatusError {
		report.Overall = StatusWarn
	}

	// 3. Display / Graphical Environment Check
	displayCheck := CheckDisplaySession()
	report.Checks = append(report.Checks, displayCheck)
	if displayCheck.Status == StatusOK {
		report.Capabilities.DisplaySession = true
	} else if displayCheck.Status == StatusError {
		report.Overall = StatusError
	} else if displayCheck.Status == StatusWarn && report.Overall != StatusError {
		report.Overall = StatusWarn
	}

	// 4. Clipboard Backend & Tools Check (passive unless enableSelfTest is requested)
	clipCheck := CheckClipboard(enableSelfTest)
	report.Checks = append(report.Checks, clipCheck)
	if clipCheck.Status == StatusOK {
		report.Capabilities.TextClipboard = true
		report.Capabilities.ImageClipboard = true
	} else if clipCheck.Status == StatusWarn {
		report.Capabilities.TextClipboard = true
		if report.Overall != StatusError {
			report.Overall = StatusWarn
		}
	} else if clipCheck.Status == StatusError {
		report.Overall = StatusError
	}

	// 5. File Transfer Check
	fileXferCheck := CheckFileTransfer()
	report.Checks = append(report.Checks, fileXferCheck)
	if fileXferCheck.Status == StatusOK {
		report.Capabilities.FileTransfer = true
	} else if fileXferCheck.Status == StatusError {
		report.Overall = StatusError
	} else if fileXferCheck.Status == StatusWarn && report.Overall != StatusError {
		report.Overall = StatusWarn
	}

	return report
}

// PrintReport formats and outputs the report to the provided writer.
func (r *Report) PrintReport(w io.Writer) {
	fmt.Fprintf(w, "\n=== Tart Guest Agent Diagnostic (%s %s) ===\n\n", r.OS, r.Arch)

	for _, check := range r.Checks {
		fmt.Fprintf(w, "%s %s: %s\n", check.Status, check.Name, check.Summary)
		if check.Details != "" {
			lines := strings.Split(check.Details, "\n")
			for _, line := range lines {
				if strings.TrimSpace(line) != "" {
					fmt.Fprintf(w, "    • %s\n", line)
				}
			}
		}
		if check.Remediation != "" {
			fmt.Fprintf(w, "    ↳ Action: %s\n", check.Remediation)
		}
	}

	fmt.Fprintf(w, "\n--- Detected Capabilities ---\n")
	printCap := func(name string, ok bool) {
		if ok {
			fmt.Fprintf(w, "  ✓ %-18s [ENABLED]\n", name)
		} else {
			fmt.Fprintf(w, "  ✗ %-18s [DISABLED / LIMITED]\n", name)
		}
	}
	printCap("SPICE Serial", r.Capabilities.SerialPort)
	printCap("Display Session", r.Capabilities.DisplaySession)
	printCap("Text Clipboard", r.Capabilities.TextClipboard)
	printCap("Image Clipboard", r.Capabilities.ImageClipboard)
	printCap("File Transfer", r.Capabilities.FileTransfer)

	fmt.Fprintf(w, "\n--- Overall Status ---\n")
	switch r.Overall {
	case StatusOK:
		fmt.Fprintf(w, "Status: HEALTHY (All guest agent components ready)\n\n")
	case StatusWarn:
		fmt.Fprintf(w, "Status: WARNING (Some features may be degraded or limited)\n\n")
	case StatusError:
		fmt.Fprintf(w, "Status: ERROR (Required prerequisites missing; see actions above)\n\n")
	}
}

// PrintDoctorReport executes diagnostics, prints to stdout, optionally sends desktop notification, and returns an exit code.
func PrintDoctorReport(enableSelfTest bool, enableNotify bool) int {
	report := RunDiagnosticsWithSelfTest(enableSelfTest)
	report.PrintReport(os.Stdout)
	if enableNotify {
		_ = report.NotifyReport()
	}
	if report.Overall == StatusError {
		return 1
	}
	return 0
}
