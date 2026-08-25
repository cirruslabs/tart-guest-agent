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
	metadata := []byte("[vdagent-file-xfer]\nname=test_document.pdf\nsize=46\n")
	startMsg := &vd.VDAgentFileXferStart{
		ID:   42,
		Data: metadata,
	}

	startStatus, err := mgr.HandleStart(startMsg)
	require.NoError(t, err)
	assert.Equal(t, uint32(42), startStatus.ID)
	assert.Equal(t, uint32(vd.VD_AGENT_FILE_XFER_STATUS_CAN_SEND_DATA), startStatus.Result)

	// 2. Send data chunk 1 (23 bytes)
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

	// 3. Send data chunk 2 (23 bytes) -> completes transfer at totalSize
	chunk2 := []byte("Additional Stream Chunk")
	dataMsg2 := &vd.VDAgentFileXferData{
		ID:   42,
		Size: uint64(len(chunk2)),
		Data: chunk2,
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
	assert.Equal(t, append(chunk1, chunk2...), content)
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

func TestFileXferManager_DuplicateTaskID(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "filexfer_dup_test_*")
	require.NoError(t, err)
	defer os.RemoveAll(tempDir)

	mgr := filexfer.NewManager()
	mgr.SetDownloadDir(tempDir)
	defer mgr.Close()

	// First transfer with ID 55
	_, err = mgr.HandleStart(&vd.VDAgentFileXferStart{
		ID:   55,
		Data: []byte("name=first.txt\n"),
	})
	require.NoError(t, err)
	firstPath := filepath.Join(tempDir, "first.txt")
	assert.FileExists(t, firstPath)

	// Second transfer with same ID 55 should clean up previous
	_, err = mgr.HandleStart(&vd.VDAgentFileXferStart{
		ID:   55,
		Data: []byte("name=second.txt\n"),
	})
	require.NoError(t, err)
	assert.NoFileExists(t, firstPath)
	secondPath := filepath.Join(tempDir, "second.txt")
	assert.FileExists(t, secondPath)
}

func TestFileXferManager_SizeMismatch(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "filexfer_mismatch_test_*")
	require.NoError(t, err)
	defer os.RemoveAll(tempDir)

	mgr := filexfer.NewManager()
	mgr.SetDownloadDir(tempDir)
	defer mgr.Close()

	_, err = mgr.HandleStart(&vd.VDAgentFileXferStart{
		ID:   77,
		Data: []byte("name=short.bin\nsize=1000\n"),
	})
	require.NoError(t, err)

	// Send only 10 bytes then EOF
	_, _, err = mgr.HandleData(&vd.VDAgentFileXferData{
		ID:   77,
		Size: 10,
		Data: []byte("0123456789"),
	})
	require.NoError(t, err)

	status, _, err := mgr.HandleData(&vd.VDAgentFileXferData{
		ID:   77,
		Size: 0,
		Data: nil,
	})
	assert.Error(t, err)
	assert.Equal(t, uint32(vd.VD_AGENT_FILE_XFER_STATUS_ERROR), status.Result)
	assert.NoFileExists(t, filepath.Join(tempDir, "short.bin"))
}

func TestFileXferManager_MaxActiveTransfers(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "filexfer_limit_test_*")
	require.NoError(t, err)
	defer os.RemoveAll(tempDir)

	mgr := filexfer.NewManager()
	mgr.SetDownloadDir(tempDir)
	defer mgr.Close()

	for i := uint32(0); i < filexfer.MaxActiveTransfers; i++ {
		startStatus, err := mgr.HandleStart(&vd.VDAgentFileXferStart{
			ID:   i,
			Data: []byte("name=file.bin\n"),
		})
		require.NoError(t, err)
		assert.Equal(t, uint32(vd.VD_AGENT_FILE_XFER_STATUS_CAN_SEND_DATA), startStatus.Result)
	}

	// 65th transfer exceeding limit
	startStatus, err := mgr.HandleStart(&vd.VDAgentFileXferStart{
		ID:   9999,
		Data: []byte("name=overflow.bin\n"),
	})
	assert.Error(t, err)
	assert.Equal(t, uint32(vd.VD_AGENT_FILE_XFER_STATUS_ERROR), startStatus.Result)
}

func TestFileXferManager_NotEnoughSpace(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "filexfer_space_test_*")
	require.NoError(t, err)
	defer os.RemoveAll(tempDir)

	mgr := filexfer.NewManager()
	mgr.SetDownloadDir(tempDir)
	defer mgr.Close()

	// Exceedingly large file size (e.g. 100 Exabytes)
	startStatus, err := mgr.HandleStart(&vd.VDAgentFileXferStart{
		ID:   88,
		Data: []byte("name=gigantic_file.iso\nsize=18446744073709551610\n"),
	})
	assert.Error(t, err)
	assert.Equal(t, uint32(vd.VD_AGENT_FILE_XFER_STATUS_NOT_ENOUGH_SPACE), startStatus.Result)
	assert.NoFileExists(t, filepath.Join(tempDir, "gigantic_file.iso"))
}

