package activity

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"time"

	"github.com/google/uuid"
)

type Category string

const (
	CategoryClipboardText  Category = "clipboard-text"
	CategoryClipboardImage Category = "clipboard-image"
	CategoryFileTransfer   Category = "file-transfer"
	CategorySystem         Category = "system"
	CategoryDoctor         Category = "doctor"
)

// Event captures a single notification or operational activity event in the guest agent.
type Event struct {
	ID        string    `json:"id"`
	Timestamp time.Time `json:"timestamp"`
	Category  Category  `json:"category"`
	Title     string    `json:"title"`
	Detail    string    `json:"detail"`
	Status    string    `json:"status"` // "success", "warning", "error", "info"
}

// ActivityFilePath resolves the path to the activity.json file across platforms.
func ActivityFilePath() string {
	if custom := os.Getenv("TART_GUEST_ACTIVITY"); custom != "" {
		return custom
	}

	home, _ := os.UserHomeDir()
	if runtime.GOOS == "darwin" {
		return filepath.Join(home, "Library", "Application Support", "tart-guest-agent", "activity.json")
	}

	if xdg := os.Getenv("XDG_STATE_HOME"); xdg != "" {
		return filepath.Join(xdg, "tart-guest-agent", "activity.json")
	}

	return filepath.Join(home, ".local", "state", "tart-guest-agent", "activity.json")
}

// Manager manages a thread-safe ring buffer of activity events with disk persistence.
type Manager struct {
	mu        sync.Mutex
	maxEvents int
	events    []Event
}

var defaultManager = NewManager(100)

func (m *Manager) saveLocked() {
	path := ActivityFilePath()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return
	}
	data, err := json.MarshalIndent(m.events, "", "  ")
	if err != nil {
		return
	}
	_ = os.WriteFile(path, data, 0644)
}

func (m *Manager) loadLocked() {
	path := ActivityFilePath()
	data, err := os.ReadFile(path)
	if err != nil {
		return
	}
	var loaded []Event
	if err := json.Unmarshal(data, &loaded); err == nil && len(loaded) > 0 {
		m.events = loaded
	}
}

// NewManager creates an activity manager with a defined maximum event capacity.
func NewManager(maxEvents int) *Manager {
	if maxEvents <= 0 {
		maxEvents = 100
	}
	m := &Manager{
		maxEvents: maxEvents,
		events:    make([]Event, 0, maxEvents),
	}
	m.loadLocked()
	return m
}

// Record appends a new event, evicting the oldest if capacity is reached, and persists to disk.
func (m *Manager) Record(category Category, title string, detail string, status string) Event {
	m.mu.Lock()
	defer m.mu.Unlock()

	if status == "" {
		status = "info"
	}

	event := Event{
		ID:        uuid.NewString()[:8],
		Timestamp: time.Now(),
		Category:  category,
		Title:     title,
		Detail:    detail,
		Status:    status,
	}

	if len(m.events) >= m.maxEvents {
		// Evict oldest
		m.events = append(m.events[1:], event)
	} else {
		m.events = append(m.events, event)
	}

	m.saveLocked()
	return event
}

// List returns a copy of all recorded events in reverse chronological order (newest first).
func (m *Manager) List() []Event {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.loadLocked()

	n := len(m.events)
	result := make([]Event, n)
	for i := 0; i < n; i++ {
		result[i] = m.events[n-1-i]
	}
	return result
}

// Clear removes all recorded events and deletes the persistent file.
func (m *Manager) Clear() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.events = m.events[:0]
	_ = os.Remove(ActivityFilePath())
}

// Global convenience functions
func Record(category Category, title string, detail string, status string) Event {
	return defaultManager.Record(category, title, detail, status)
}

func Recordf(category Category, status string, format string, a ...any) Event {
	title := fmt.Sprintf(format, a...)
	return defaultManager.Record(category, title, "", status)
}

func List() []Event {
	return defaultManager.List()
}

func Clear() {
	defaultManager.Clear()
}
