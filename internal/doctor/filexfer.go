package doctor

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/cirruslabs/tart-guest-agent/internal/spice/filexfer"
)

// CheckFileTransfer verifies download directory writability and available disk space.
func CheckFileTransfer() CheckResult {
	res := CheckResult{
		Category: "FileTransfer",
		Name:     "File Transfer (Drag & Drop)",
	}

	homeDir, err := os.UserHomeDir()
	if err != nil {
		homeDir = "/tmp"
	}

	downloadDir := filepath.Join(homeDir, "Downloads")
	if err := os.MkdirAll(downloadDir, 0755); err != nil {
		res.Status = StatusError
		res.Summary = fmt.Sprintf("failed creating download dir %s: %v", downloadDir, err)
		res.Remediation = fmt.Sprintf("Ensure user home directory '%s' is writable.", homeDir)
		return res
	}

	// Test writability using unique temporary probe file
	probeFile, err := os.CreateTemp(downloadDir, ".tart_filexfer_probe_*")
	if err != nil {
		res.Status = StatusError
		res.Summary = fmt.Sprintf("download directory %s is not writable: %v", downloadDir, err)
		res.Remediation = fmt.Sprintf("Fix permissions on %s: 'chmod u+rwx %s'", downloadDir, downloadDir)
		return res
	}
	probePath := probeFile.Name()
	_ = probeFile.Close()
	_ = os.Remove(probePath)

	// Measure disk space via cross-platform helper
	freeBytes, err := filexfer.GetAvailableDiskSpace(downloadDir)
	if err != nil {
		res.Status = StatusWarn
		res.Summary = fmt.Sprintf("download dir %s writable (unable to measure free disk space)", downloadDir)
		return res
	}

	freeGB := float64(freeBytes) / (1024 * 1024 * 1024)

	res.Status = StatusOK
	res.Summary = fmt.Sprintf("Ready (%s, %.1f GB free space)", downloadDir, freeGB)
	res.Details = fmt.Sprintf("Download Path: %s\nAvailable Disk: %.1f GB", downloadDir, freeGB)
	return res
}
