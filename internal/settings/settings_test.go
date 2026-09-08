package settings

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDefaultSettings(t *testing.T) {
	s := DefaultSettings()
	require.NotNil(t, s)
	assert.True(t, s.NotificationsEnabled)
	assert.True(t, s.ImageClipboardEnabled)
	assert.True(t, s.FileTransferEnabled)
	assert.NotEmpty(t, s.DownloadDir)
}

func TestSaveAndLoadSettings(t *testing.T) {
	tempDir := t.TempDir()
	configPath := filepath.Join(tempDir, "settings.json")
	t.Setenv("TART_GUEST_CONFIG", configPath)

	s := DefaultSettings()
	s.ImageClipboardEnabled = false
	s.DownloadDir = "/tmp/custom_downloads"

	err := Save(s)
	require.NoError(t, err)

	loaded, err := Load()
	require.NoError(t, err)
	assert.False(t, loaded.ImageClipboardEnabled)
	assert.Equal(t, "/tmp/custom_downloads", loaded.DownloadDir)

	cached := Get()
	assert.False(t, cached.ImageClipboardEnabled)
	assert.Equal(t, "/tmp/custom_downloads", cached.DownloadDir)
}

func TestLoadNonExistentUsesDefault(t *testing.T) {
	tempDir := t.TempDir()
	configPath := filepath.Join(tempDir, "non_existent.json")
	t.Setenv("TART_GUEST_CONFIG", configPath)

	loaded, err := Load()
	require.NoError(t, err)
	assert.True(t, loaded.NotificationsEnabled)
	assert.True(t, loaded.ImageClipboardEnabled)
}
