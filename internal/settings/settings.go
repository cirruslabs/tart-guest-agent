package settings

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"sync"
)

// Settings encapsulates configurable user preferences for Tart Guest Agent.
type Settings struct {
	NotificationsEnabled   bool   `json:"notifications_enabled"`
	ImageClipboardEnabled  bool   `json:"image_clipboard_enabled"`
	FileTransferEnabled    bool   `json:"file_transfer_enabled"`
	DownloadDir            string `json:"download_dir"`
	AutoResizeEnabled      bool   `json:"auto_resize_enabled"`
	StartupNotification    bool   `json:"startup_notification"`
}

var (
	currentSettings *Settings
	settingsMu      sync.RWMutex
)

// DefaultSettings returns the default runtime settings.
func DefaultSettings() *Settings {
	home, _ := os.UserHomeDir()
	downloadDir := filepath.Join(home, "Downloads")
	if home == "" {
		downloadDir = os.TempDir()
	}

	return &Settings{
		NotificationsEnabled:  true,
		ImageClipboardEnabled: true,
		FileTransferEnabled:   true,
		DownloadDir:           downloadDir,
		AutoResizeEnabled:     true,
		StartupNotification:   true,
	}
}

// ConfigFilePath resolves the path to the user's settings.json file across platforms.
func ConfigFilePath() string {
	if custom := os.Getenv("TART_GUEST_CONFIG"); custom != "" {
		return custom
	}

	home, _ := os.UserHomeDir()
	if runtime.GOOS == "darwin" {
		return filepath.Join(home, "Library", "Application Support", "tart-guest-agent", "settings.json")
	}

	if xdg := os.Getenv("XDG_CONFIG_HOME"); xdg != "" {
		return filepath.Join(xdg, "tart-guest-agent", "settings.json")
	}

	return filepath.Join(home, ".config", "tart-guest-agent", "settings.json")
}

// Load loads settings from disk, creating default settings if none exist.
func Load() (*Settings, error) {
	settingsMu.Lock()
	defer settingsMu.Unlock()

	path := ConfigFilePath()
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			currentSettings = DefaultSettings()
			return currentSettings, nil
		}
		currentSettings = DefaultSettings()
		return currentSettings, err
	}

	s := DefaultSettings()
	if err := json.Unmarshal(data, s); err != nil {
		currentSettings = DefaultSettings()
		return currentSettings, err
	}

	currentSettings = s
	return currentSettings, nil
}

// Save persists the provided settings to disk.
func Save(s *Settings) error {
	settingsMu.Lock()
	defer settingsMu.Unlock()

	path := ConfigFilePath()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}

	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}

	if err := os.WriteFile(path, data, 0644); err != nil {
		return err
	}

	currentSettings = s
	return nil
}

// Get returns the current cached settings, loading defaults if not yet initialized.
func Get() *Settings {
	settingsMu.RLock()
	if currentSettings != nil {
		defer settingsMu.RUnlock()
		// Return copy
		cp := *currentSettings
		return &cp
	}
	settingsMu.RUnlock()

	s, _ := Load()
	cp := *s
	return &cp
}

// Reset clears cached settings in memory and forces re-evaluation on next access.
func Reset() {
	settingsMu.Lock()
	defer settingsMu.Unlock()
	currentSettings = nil
}
