package filexfer_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/cirruslabs/tart-guest-agent/internal/spice/filexfer"
	"github.com/cirruslabs/tart-guest-agent/internal/spice/vd"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFileXferManager_EndToEnd(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "filexfer_test_*")
	require.NoError(t, err)
	defer os.RemoveAll(tempDir)

	mgr := filexfer.NewManager()
	mgr.SetDownloadDir(tempDir)
	defer mgr.Close()

	// 1. Send start message
	metadata := []byte("[vdagent-file-xfer]\nname=test_document.pdf\nsize=24\n")
	startMsg := &vd.VDAgentFileXferStart{
		ID:   42,
		Data: metadata,
	}

	startStatus, err := mgr.HandleStart(startMsg)
	require.NoError(t, err)
	assert.Equal(t, uint32(42), startStatus.ID)
	assert.Equal(t, uint32(vd.VD_AGENT_FILE_XFER_STATUS_CAN_SEND_DATA), startStatus.Result)

	// 2. Send data chunk 1
	chunk1 := []byte("%PDF-1.4 Mock PDF Data ")
	dataMsg1 := &vd.VDAgentFileXferData{
		ID:   42,
		Size: uint64(len(chunk1)),
		Data: chunk1,
	}
	status1, completed1, err := mgr.HandleData(dataMsg1)
	require.NoError(t, err)
	assert.False(t, completed1)
	assert.Equal(t, uint32(vd.VD_AGENT_FILE_XFER_STATUS_CAN_SEND_DATA), status1.Result)

	// 3. Send final EOF chunk (size 0)
	dataMsg2 := &vd.VDAgentFileXferData{
		ID:   42,
		Size: 0,
		Data: nil,
	}
	status2, completed2, err := mgr.HandleData(dataMsg2)
	require.NoError(t, err)
	assert.True(t, completed2)
	assert.Equal(t, uint32(vd.VD_AGENT_FILE_XFER_STATUS_SUCCESS), status2.Result)

	// 4. Verify file exists on disk with correct content
	savedFilePath := filepath.Join(tempDir, "test_document.pdf")
	assert.FileExists(t, savedFilePath)

	content, err := os.ReadFile(savedFilePath)
	require.NoError(t, err)
	assert.Equal(t, chunk1, content)
}

func TestFileXferManager_Cancel(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "filexfer_cancel_test_*")
	require.NoError(t, err)
	defer os.RemoveAll(tempDir)

	mgr := filexfer.NewManager()
	mgr.SetDownloadDir(tempDir)
	defer mgr.Close()

	startMsg := &vd.VDAgentFileXferStart{
		ID:   99,
		Data: []byte("name=aborted_file.svg\n"),
	}

	_, err = mgr.HandleStart(startMsg)
	require.NoError(t, err)

	targetFile := filepath.Join(tempDir, "aborted_file.svg")
	assert.FileExists(t, targetFile)

	mgr.Cancel(99)
	assert.NoFileExists(t, targetFile)
}
