package filexfer

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	"github.com/cirruslabs/tart-guest-agent/internal/spice/vd"
	"go.uber.org/zap"
)

type transferTask struct {
	id         uint32
	name       string
	totalSize  uint64
	bytesRcvd  uint64
	file       *os.File
	targetPath string
}

// Manager handles SPICE file transfer sessions (VD_AGENT_FILE_XFER_*).
type Manager struct {
	mu          sync.Mutex
	tasks       map[uint32]*transferTask
	downloadDir string
}

// NewManager initializes the file transfer manager with a target download directory.
func NewManager() *Manager {
	dir := defaultDownloadDir()
	if err := os.MkdirAll(dir, 0755); err != nil {
		zap.S().Warnf("failed to create download dir %s: %v, falling back to temp dir", dir, err)
		dir = filepath.Join(os.TempDir(), "tart-transfers")
		_ = os.MkdirAll(dir, 0755)
	}

	return &Manager{
		tasks:       make(map[uint32]*transferTask),
		downloadDir: dir,
	}
}

func defaultDownloadDir() string {
	home, err := os.UserHomeDir()
	if err == nil && home != "" {
		downloads := filepath.Join(home, "Downloads")
		if info, err := os.Stat(downloads); err == nil && info.IsDir() {
			return downloads
		}
		return home
	}
	return filepath.Join(os.TempDir(), "tart-transfers")
}

// SetDownloadDir allows overriding the target directory for received files.
func (m *Manager) SetDownloadDir(dir string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.downloadDir = dir
}

// MaxActiveTransfers bounds the maximum number of concurrent active transfer tasks.
const MaxActiveTransfers = 64

// HandleStart initiates a new file transfer from VDAgentFileXferStart metadata.
func (m *Manager) HandleStart(msg *vd.VDAgentFileXferStart) (*vd.VDAgentFileXferStatus, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Bound the number of active transfers to prevent resource/FD exhaustion
	if len(m.tasks) >= MaxActiveTransfers {
		if _, exists := m.tasks[msg.ID]; !exists {
			zap.S().Warnf("filexfer: active transfer limit (%d) reached, rejecting task id=%d", MaxActiveTransfers, msg.ID)
			return &vd.VDAgentFileXferStatus{
				ID:     msg.ID,
				Result: vd.VD_AGENT_FILE_XFER_STATUS_ERROR,
			}, fmt.Errorf("active transfer limit (%d) reached", MaxActiveTransfers)
		}
	}

	fileName, fileSize := parseMetadata(msg.Data)
	if fileName == "" {
		fileName = fmt.Sprintf("tart_transfer_%d.dat", msg.ID)
	}

	// Reject oversized transfers before creating files or inviting data
	if fileSize > 0 {
		if avail, err := getAvailableDiskSpace(m.downloadDir); err == nil && avail < fileSize {
			zap.S().Warnf("filexfer: not enough disk space for %s: requires %d bytes, available %d bytes",
				fileName, fileSize, avail)
			return &vd.VDAgentFileXferStatus{
				ID:     msg.ID,
				Result: vd.VD_AGENT_FILE_XFER_STATUS_NOT_ENOUGH_SPACE,
			}, fmt.Errorf("not enough disk space: required %d bytes, available %d bytes", fileSize, avail)
		}
	}

	// Sanitize filename to prevent directory traversal
	cleanName := filepath.Base(fileName)
	cleanName = strings.ReplaceAll(cleanName, "\x00", "")
	if cleanName == "." || cleanName == ".." || cleanName == "/" || cleanName == "\\" || cleanName == "" || strings.Contains(cleanName, "..") {
		cleanName = fmt.Sprintf("tart_transfer_%d.dat", msg.ID)
	}

	targetPath := filepath.Join(m.downloadDir, cleanName)

	// Avoid overwriting by appending suffix if needed
	uniquePath, err := getUniqueFilePath(targetPath)
	if err != nil {
		zap.S().Errorf("filexfer: failed to determine unique target path for %s: %v", targetPath, err)
		return &vd.VDAgentFileXferStatus{
			ID:     msg.ID,
			Result: vd.VD_AGENT_FILE_XFER_STATUS_ERROR,
		}, err
	}
	targetPath = uniquePath

	file, err := os.OpenFile(targetPath, os.O_CREATE|os.O_WRONLY|os.O_EXCL, 0644)
	if err != nil {
		zap.S().Errorf("filexfer: failed to create target file %s: %v", targetPath, err)
		return &vd.VDAgentFileXferStatus{
			ID:     msg.ID,
			Result: vd.VD_AGENT_FILE_XFER_STATUS_ERROR,
		}, err
	}

	// Clean up any existing active task with duplicate ID
	if existingTask, exists := m.tasks[msg.ID]; exists {
		_ = existingTask.file.Close()
		_ = os.Remove(existingTask.targetPath)
		delete(m.tasks, msg.ID)
		zap.S().Warnf("filexfer: task id=%d already exists; cleaned up previous transfer", msg.ID)
	}

	task := &transferTask{
		id:         msg.ID,
		name:       cleanName,
		totalSize:  fileSize,
		bytesRcvd:  0,
		file:       file,
		targetPath: targetPath,
	}
	m.tasks[msg.ID] = task

	zap.S().Infof("filexfer: started transfer id=%d for file=%s (expected size=%d bytes) -> %s",
		msg.ID, cleanName, fileSize, targetPath)

	return &vd.VDAgentFileXferStatus{
		ID:     msg.ID,
		Result: vd.VD_AGENT_FILE_XFER_STATUS_CAN_SEND_DATA,
	}, nil
}

// HandleData appends binary data chunk to an ongoing file transfer task.
func (m *Manager) HandleData(msg *vd.VDAgentFileXferData) (*vd.VDAgentFileXferStatus, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	task, exists := m.tasks[msg.ID]
	if !exists {
		zap.S().Warnf("filexfer: received data for unknown task id=%d", msg.ID)
		return &vd.VDAgentFileXferStatus{
			ID:     msg.ID,
			Result: vd.VD_AGENT_FILE_XFER_STATUS_ERROR,
		}, false, fmt.Errorf("task %d not found", msg.ID)
	}

	// If size is 0 or empty data, indicates completion
	if msg.Size == 0 || len(msg.Data) == 0 {
		return m.finishTask(task)
	}

	n, err := task.file.Write(msg.Data)
	if err != nil {
		zap.S().Errorf("filexfer: failed writing to %s: %v", task.targetPath, err)
		_ = task.file.Close()
		_ = os.Remove(task.targetPath)
		delete(m.tasks, msg.ID)
		return &vd.VDAgentFileXferStatus{
			ID:     msg.ID,
			Result: vd.VD_AGENT_FILE_XFER_STATUS_ERROR,
		}, false, err
	}

	task.bytesRcvd += uint64(n)

	// If totalSize was specified and reached
	if task.totalSize > 0 && task.bytesRcvd >= task.totalSize {
		return m.finishTask(task)
	}

	return &vd.VDAgentFileXferStatus{
		ID:     msg.ID,
		Result: vd.VD_AGENT_FILE_XFER_STATUS_CAN_SEND_DATA,
	}, false, nil
}

func (m *Manager) finishTask(task *transferTask) (*vd.VDAgentFileXferStatus, bool, error) {
	var err error
	if syncErr := task.file.Sync(); syncErr != nil && err == nil {
		err = syncErr
	}
	if closeErr := task.file.Close(); closeErr != nil && err == nil {
		err = closeErr
	}

	delete(m.tasks, task.id)

	if err != nil {
		zap.S().Errorf("filexfer: failed finalizing file %s: %v", task.targetPath, err)
		_ = os.Remove(task.targetPath)
		return &vd.VDAgentFileXferStatus{
			ID:     task.id,
			Result: vd.VD_AGENT_FILE_XFER_STATUS_ERROR,
		}, false, err
	}

	if task.totalSize > 0 && task.bytesRcvd != task.totalSize {
		zap.S().Errorf("filexfer: size mismatch for %s: expected %d bytes, received %d bytes",
			task.targetPath, task.totalSize, task.bytesRcvd)
		_ = os.Remove(task.targetPath)
		return &vd.VDAgentFileXferStatus{
			ID:     task.id,
			Result: vd.VD_AGENT_FILE_XFER_STATUS_ERROR,
		}, false, fmt.Errorf("transfer size mismatch: expected %d, got %d", task.totalSize, task.bytesRcvd)
	}

	zap.S().Infof("filexfer: completed transfer id=%d (%s, total=%d bytes) successfully",
		task.id, task.name, task.bytesRcvd)

	return &vd.VDAgentFileXferStatus{
		ID:     task.id,
		Result: vd.VD_AGENT_FILE_XFER_STATUS_SUCCESS,
	}, true, nil
}

// Cancel terminates and removes incomplete transfer files.
func (m *Manager) Cancel(id uint32) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if task, exists := m.tasks[id]; exists {
		_ = task.file.Close()
		_ = os.Remove(task.targetPath)
		delete(m.tasks, id)
		zap.S().Infof("filexfer: cancelled transfer id=%d and cleaned up %s", id, task.targetPath)
	}
}

// Close aborts all active transfers.
func (m *Manager) Close() {
	m.mu.Lock()
	defer m.mu.Unlock()

	for id, task := range m.tasks {
		_ = task.file.Close()
		_ = os.Remove(task.targetPath)
		delete(m.tasks, id)
	}
}

func parseMetadata(data []byte) (string, uint64) {
	str := string(bytes.TrimRight(data, "\x00"))
	var fileName string
	var fileSize uint64

	lines := strings.Split(str, "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "name=") {
			fileName = strings.TrimPrefix(line, "name=")
		} else if strings.HasPrefix(line, "size=") {
			if s, err := strconv.ParseUint(strings.TrimPrefix(line, "size="), 10, 64); err == nil {
				fileSize = s
			}
		}
	}

	// Fallback: If no INI key-value format was used, check if it is a bare filename
	if fileName == "" && len(str) > 0 && !strings.Contains(str, "[") {
		fileName = strings.TrimSpace(lines[0])
	}

	return fileName, fileSize
}

func getUniqueFilePath(path string) (string, error) {
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return path, nil
	}

	dir := filepath.Dir(path)
	ext := filepath.Ext(path)
	base := strings.TrimSuffix(filepath.Base(path), ext)

	for i := 1; i < 1000; i++ {
		candidate := filepath.Join(dir, fmt.Sprintf("%s (%d)%s", base, i, ext))
		if _, err := os.Stat(candidate); os.IsNotExist(err) {
			return candidate, nil
		}
	}

	return "", fmt.Errorf("could not find unique filename for %s after 999 attempts", path)
}
