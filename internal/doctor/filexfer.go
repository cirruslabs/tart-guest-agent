package doctor

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall"
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

	// Test writability
	probeFile := filepath.Join(downloadDir, ".tart_filexfer_probe")
	if err := os.WriteFile(probeFile, []byte("ok"), 0644); err != nil {
		res.Status = StatusError
		res.Summary = fmt.Sprintf("download directory %s is not writable: %v", downloadDir, err)
		res.Remediation = fmt.Sprintf("Fix permissions on %s: 'chmod u+rwx %s'", downloadDir, downloadDir)
		return res
	}
	_ = os.Remove(probeFile)

	// Measure disk space
	var stat syscall.Statfs_t
	if err := syscall.Statfs(downloadDir, &stat); err != nil {
		res.Status = StatusWarn
		res.Summary = fmt.Sprintf("download dir %s writable (unable to measure free disk space)", downloadDir)
		return res
	}

	freeBytes := stat.Bavail * uint64(stat.Bsize)
	freeGB := float64(freeBytes) / (1024 * 1024 * 1024)

	res.Status = StatusOK
	res.Summary = fmt.Sprintf("Ready (%s, %.1f GB free space)", downloadDir, freeGB)
	res.Details = fmt.Sprintf("Download Path: %s\nAvailable Disk: %.1f GB", downloadDir, freeGB)
	return res
}
