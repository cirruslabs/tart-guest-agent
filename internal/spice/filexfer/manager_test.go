package filexfer_test

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/cirruslabs/tart-guest-agent/internal/settings"
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

	// 2. Send data chunk 1 (23 bytes) -> intermediate chunk, no status reply needed
	chunk1 := []byte("%PDF-1.4 Mock PDF Data ")
	dataMsg1 := &vd.VDAgentFileXferData{
		ID:   42,
		Size: uint64(len(chunk1)),
		Data: chunk1,
	}
	status1, completed1, err := mgr.HandleData(dataMsg1)
	require.NoError(t, err)
	assert.False(t, completed1)
	assert.Nil(t, status1)

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
	assert.NotNil(t, status2)
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
		Data: []byte("name=aborted_file.svg\nsize=100\n"),
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

	// First transfer with ID 55 and filename retry.txt
	_, err = mgr.HandleStart(&vd.VDAgentFileXferStart{
		ID:   55,
		Data: []byte("name=retry.txt\nsize=10\n"),
	})
	require.NoError(t, err)
	targetPath := filepath.Join(tempDir, "retry.txt")
	assert.FileExists(t, targetPath)

	// Second transfer retry with same ID 55 and same filename retry.txt
	// Should clean up previous partial and reuse retry.txt (not retry (1).txt)
	_, err = mgr.HandleStart(&vd.VDAgentFileXferStart{
		ID:   55,
		Data: []byte("name=retry.txt\nsize=20\n"),
	})
	require.NoError(t, err)
	assert.FileExists(t, targetPath)
	assert.NoFileExists(t, filepath.Join(tempDir, "retry (1).txt"))
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
			Data: []byte("name=file.bin\nsize=10\n"),
		})
		require.NoError(t, err)
		assert.Equal(t, uint32(vd.VD_AGENT_FILE_XFER_STATUS_CAN_SEND_DATA), startStatus.Result)
	}

	// 65th transfer exceeding limit
	startStatus, err := mgr.HandleStart(&vd.VDAgentFileXferStart{
		ID:   9999,
		Data: []byte("name=overflow.bin\nsize=10\n"),
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

func TestFileXferManager_MalformedSize(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "filexfer_malformed_test_*")
	require.NoError(t, err)
	defer os.RemoveAll(tempDir)

	mgr := filexfer.NewManager()
	mgr.SetDownloadDir(tempDir)
	defer mgr.Close()

	// Malformed size exceeding uint64 overflow
	startStatus, err := mgr.HandleStart(&vd.VDAgentFileXferStart{
		ID:   91,
		Data: []byte("name=bad_size.bin\nsize=18446744073709551616\n"),
	})
	assert.Error(t, err)
	assert.Equal(t, uint32(vd.VD_AGENT_FILE_XFER_STATUS_ERROR), startStatus.Result)
	assert.NoFileExists(t, filepath.Join(tempDir, "bad_size.bin"))

	// Non-numeric size
	startStatus2, err2 := mgr.HandleStart(&vd.VDAgentFileXferStart{
		ID:   92,
		Data: []byte("name=nan_size.bin\nsize=not_a_number\n"),
	})
	assert.Error(t, err2)
	assert.Equal(t, uint32(vd.VD_AGENT_FILE_XFER_STATUS_ERROR), startStatus2.Result)
	assert.NoFileExists(t, filepath.Join(tempDir, "nan_size.bin"))
}

func TestFileXferManager_ReservedSpace(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "filexfer_reserved_test_*")
	require.NoError(t, err)
	defer os.RemoveAll(tempDir)

	mgr := filexfer.NewManager()
	mgr.SetDownloadDir(tempDir)
	defer mgr.Close()

	avail, err := filexfer.GetAvailableDiskSpace(tempDir)
	require.NoError(t, err)
	if avail < 1000 {
		t.Skip("insufficient disk space for test")
	}

	// Task 1 requests 60% of available space
	size1 := (avail * 6) / 10
	startStatus1, err := mgr.HandleStart(&vd.VDAgentFileXferStart{
		ID:   101,
		Data: []byte(fmt.Sprintf("name=part1.bin\nsize=%d\n", size1)),
	})
	require.NoError(t, err)
	assert.Equal(t, uint32(vd.VD_AGENT_FILE_XFER_STATUS_CAN_SEND_DATA), startStatus1.Result)

	// Task 2 requests 50% of available space -> combined exceeds 100% of available space
	size2 := (avail * 5) / 10
	startStatus2, err := mgr.HandleStart(&vd.VDAgentFileXferStart{
		ID:   102,
		Data: []byte(fmt.Sprintf("name=part2.bin\nsize=%d\n", size2)),
	})
	assert.Error(t, err)
	assert.Equal(t, uint32(vd.VD_AGENT_FILE_XFER_STATUS_NOT_ENOUGH_SPACE), startStatus2.Result)
	assert.NoFileExists(t, filepath.Join(tempDir, "part2.bin"))
}

func TestFileXferManager_MissingSize(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "filexfer_nosize_test_*")
	require.NoError(t, err)
	defer os.RemoveAll(tempDir)

	mgr := filexfer.NewManager()
	mgr.SetDownloadDir(tempDir)
	defer mgr.Close()

	// Standard payload without size field (bare filename or name=)
	startStatus, err := mgr.HandleStart(&vd.VDAgentFileXferStart{
		ID:   103,
		Data: []byte("name=standard_file.bin\n"),
	})
	require.NoError(t, err)
	assert.Equal(t, uint32(vd.VD_AGENT_FILE_XFER_STATUS_CAN_SEND_DATA), startStatus.Result)
	assert.FileExists(t, filepath.Join(tempDir, "standard_file.bin"))

	// Data chunk followed by 0-byte EOF chunk
	dataChunk := []byte("hello stream")
	_, completed1, err := mgr.HandleData(&vd.VDAgentFileXferData{
		ID:   103,
		Size: uint64(len(dataChunk)),
		Data: dataChunk,
	})
	require.NoError(t, err)
	assert.False(t, completed1)

	statusEOF, completedEOF, err := mgr.HandleData(&vd.VDAgentFileXferData{
		ID:   103,
		Size: 0,
		Data: []byte{},
	})
	require.NoError(t, err)
	assert.True(t, completedEOF)
	assert.NotNil(t, statusEOF)
	assert.Equal(t, uint32(vd.VD_AGENT_FILE_XFER_STATUS_SUCCESS), statusEOF.Result)
}

func TestFileXferManager_OversizedChunk(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "filexfer_oversized_test_*")
	require.NoError(t, err)
	defer os.RemoveAll(tempDir)

	mgr := filexfer.NewManager()
	mgr.SetDownloadDir(tempDir)
	defer mgr.Close()

	startStatus, err := mgr.HandleStart(&vd.VDAgentFileXferStart{
		ID:   104,
		Data: []byte("name=capped.bin\nsize=10\n"),
	})
	require.NoError(t, err)
	assert.Equal(t, uint32(vd.VD_AGENT_FILE_XFER_STATUS_CAN_SEND_DATA), startStatus.Result)

	// Send chunk of 20 bytes (exceeds advertised size 10)
	status, completed, err := mgr.HandleData(&vd.VDAgentFileXferData{
		ID:   104,
		Size: 20,
		Data: []byte("12345678901234567890"),
	})
	assert.Error(t, err)
	assert.False(t, completed)
	assert.Equal(t, uint32(vd.VD_AGENT_FILE_XFER_STATUS_ERROR), status.Result)
	assert.NoFileExists(t, filepath.Join(tempDir, "capped.bin"))
}

func TestDefaultDownloadDir(t *testing.T) {
	tempHome, err := os.MkdirTemp("", "fake_home_*")
	require.NoError(t, err)
	defer os.RemoveAll(tempHome)

	oldHome := os.Getenv("HOME")
	os.Setenv("HOME", tempHome)
	defer os.Setenv("HOME", oldHome)

	settings.Reset()
	defer settings.Reset()

	dir := filexfer.DefaultDownloadDir()
	assert.Equal(t, filepath.Join(tempHome, "Downloads"), dir)
	assert.DirExists(t, dir)
}

func TestDefaultDownloadDir_ConfiguredSettingsFirst(t *testing.T) {
	tempCustom, err := os.MkdirTemp("", "custom_dl_*")
	require.NoError(t, err)
	defer os.RemoveAll(tempCustom)

	tempCfg := filepath.Join(tempCustom, "settings.json")
	oldCfg := os.Getenv("TART_GUEST_CONFIG")
	os.Setenv("TART_GUEST_CONFIG", tempCfg)
	defer func() {
		os.Setenv("TART_GUEST_CONFIG", oldCfg)
		settings.Reset()
	}()

	s := settings.DefaultSettings()
	s.DownloadDir = tempCustom
	require.NoError(t, settings.Save(s))

	dir := filexfer.DefaultDownloadDir()
	assert.Equal(t, tempCustom, dir)
}

func TestHandleStart_FileTransferDisabled(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "filexfer_disabled_*")
	require.NoError(t, err)
	defer os.RemoveAll(tempDir)

	tempCfg := filepath.Join(tempDir, "settings.json")
	oldCfg := os.Getenv("TART_GUEST_CONFIG")
	os.Setenv("TART_GUEST_CONFIG", tempCfg)
	defer func() {
		os.Setenv("TART_GUEST_CONFIG", oldCfg)
		settings.Reset()
	}()

	s := settings.DefaultSettings()
	s.FileTransferEnabled = false
	require.NoError(t, settings.Save(s))

	mgr := filexfer.NewManager()
	defer mgr.Close()

	metadata := []byte("[vdagent-file-xfer]\nname=test.txt\nsize=10\n")
	status, err := mgr.HandleStart(&vd.VDAgentFileXferStart{
		ID:   201,
		Data: metadata,
	})
	assert.Error(t, err)
	assert.Equal(t, uint32(vd.VD_AGENT_FILE_XFER_STATUS_ERROR), status.Result)
}

func TestHandleData_SizeLessTransfer(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "filexfer_sizeless_*")
	require.NoError(t, err)
	defer os.RemoveAll(tempDir)

	mgr := filexfer.NewManager()
	mgr.SetDownloadDir(tempDir)
	defer mgr.Close()

	// Size-less start (size=0 / omitted)
	metadata := []byte("[vdagent-file-xfer]\nname=stream.dat\n")
	status, err := mgr.HandleStart(&vd.VDAgentFileXferStart{
		ID:   202,
		Data: metadata,
	})
	require.NoError(t, err)
	assert.Equal(t, uint32(vd.VD_AGENT_FILE_XFER_STATUS_CAN_SEND_DATA), status.Result)

	// Send chunk
	status, completed, err := mgr.HandleData(&vd.VDAgentFileXferData{
		ID:   202,
		Size: 5,
		Data: []byte("hello"),
	})
	require.NoError(t, err)
	assert.False(t, completed)
	assert.Nil(t, status)

	// Finish transfer with empty chunk
	status, completed, err = mgr.HandleData(&vd.VDAgentFileXferData{
		ID:   202,
		Size: 0,
		Data: nil,
	})
	require.NoError(t, err)
	assert.True(t, completed)
	assert.Equal(t, uint32(vd.VD_AGENT_FILE_XFER_STATUS_SUCCESS), status.Result)

	content, err := os.ReadFile(filepath.Join(tempDir, "stream.dat"))
	require.NoError(t, err)
	assert.Equal(t, "hello", string(content))
}





