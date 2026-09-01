package activity

import (
	"fmt"
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

// Manager manages a thread-safe ring buffer of activity events.
type Manager struct {
	mu        sync.RWMutex
	maxEvents int
	events    []Event
}

var defaultManager = NewManager(100)

// NewManager creates an activity manager with a defined maximum event capacity.
func NewManager(maxEvents int) *Manager {
	if maxEvents <= 0 {
		maxEvents = 100
	}
	return &Manager{
		maxEvents: maxEvents,
		events:    make([]Event, 0, maxEvents),
	}
}

// Record appends a new event, evicting the oldest if capacity is reached.
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

	return event
}

// List returns a copy of all recorded events in reverse chronological order (newest first).
func (m *Manager) List() []Event {
	m.mu.RLock()
	defer m.mu.RUnlock()

	n := len(m.events)
	result := make([]Event, n)
	for i := 0; i < n; i++ {
		result[i] = m.events[n-1-i]
	}
	return result
}

// Clear removes all recorded events.
func (m *Manager) Clear() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.events = m.events[:0]
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
