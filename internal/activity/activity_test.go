package activity

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestActivityManager(t *testing.T) {
	mgr := NewManager(3)
	require.NotNil(t, mgr)

	mgr.Record(CategoryClipboardText, "Copied text", "Hello World", "success")
	mgr.Record(CategoryClipboardImage, "Copied image", "PNG 800x600", "info")
	mgr.Record(CategoryFileTransfer, "Downloaded file", "report.pdf", "success")

	events := mgr.List()
	require.Len(t, events, 3)
	assert.Equal(t, "Downloaded file", events[0].Title) // Newest first
	assert.Equal(t, "Copied image", events[1].Title)
	assert.Equal(t, "Copied text", events[2].Title)

	// Capacity eviction
	mgr.Record(CategoryDoctor, "Doctor check", "Healthy", "success")
	eventsAfterEviction := mgr.List()
	require.Len(t, eventsAfterEviction, 3)
	assert.Equal(t, "Doctor check", eventsAfterEviction[0].Title)
	assert.Equal(t, "Downloaded file", eventsAfterEviction[1].Title)
	assert.Equal(t, "Copied image", eventsAfterEviction[2].Title)

	mgr.Clear()
	assert.Empty(t, mgr.List())
}

func TestGlobalActivityTracker(t *testing.T) {
	Clear()
	e := Record(CategorySystem, "Agent started", "SPICE listening", "info")
	assert.NotEmpty(t, e.ID)

	events := List()
	require.Len(t, events, 1)
	assert.Equal(t, "Agent started", events[0].Title)
}
