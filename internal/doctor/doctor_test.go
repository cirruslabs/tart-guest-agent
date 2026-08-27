package doctor_test

import (
	"bytes"
	"os"
	"testing"

	"github.com/cirruslabs/tart-guest-agent/internal/doctor"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRunDiagnostics(t *testing.T) {
	report := doctor.RunDiagnostics()
	require.NotNil(t, report)
	assert.NotEmpty(t, report.OS)
	assert.NotEmpty(t, report.Arch)
	assert.NotEmpty(t, report.Checks)

	var buf bytes.Buffer
	report.PrintReport(&buf)
	output := buf.String()

	assert.Contains(t, output, "Tart Guest Agent Diagnostic")
	assert.Contains(t, output, "Detected Capabilities")
	assert.Contains(t, output, "SPICE Serial")
	assert.Contains(t, output, "Text Clipboard")
	assert.Contains(t, output, "Image Clipboard")
	assert.Contains(t, output, "File Transfer")
}

func TestCheckDisplaySession(t *testing.T) {
	res := doctor.CheckDisplaySession()
	assert.Equal(t, "Display", res.Category)
	assert.NotEmpty(t, res.Name)
	assert.NotEmpty(t, res.Summary)
}

func TestCheckConflictingDaemons(t *testing.T) {
	res := doctor.CheckConflictingDaemons()
	assert.Equal(t, "SPICE", res.Category)
	assert.NotEmpty(t, res.Name)
}

func TestCheckFileTransfer(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "doctor_filexfer_*")
	require.NoError(t, err)
	defer os.RemoveAll(tempDir)

	oldHome := os.Getenv("HOME")
	os.Setenv("HOME", tempDir)
	defer os.Setenv("HOME", oldHome)

	res := doctor.CheckFileTransfer()
	assert.Equal(t, "FileTransfer", res.Category)
	assert.Equal(t, doctor.StatusOK, res.Status)
	assert.Contains(t, res.Summary, "Downloads")
}
