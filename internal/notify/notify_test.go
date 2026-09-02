package notify

import (
	"testing"

	"github.com/cirruslabs/tart-guest-agent/internal/activity"
	"github.com/cirruslabs/tart-guest-agent/internal/settings"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNotificationDispatch(t *testing.T) {
	t.Setenv("TART_GUEST_ACTIVITY", t.TempDir()+"/activity_notify.json")
	activity.Clear()

	s := settings.DefaultSettings()
	s.NotificationsEnabled = true
	require.NoError(t, settings.Save(s))

	err := Send("Test Title", "Test Message", UrgencyLow)
	assert.NoError(t, err)

	FileTransferCompleted("test.pdf", 1024)
	events := activity.List()
	require.Len(t, events, 1)
	assert.Contains(t, events[0].Title, "Received test.pdf")

	ClipboardSync("PNG", 2048)
	eventsAfterClip := activity.List()
	require.Len(t, eventsAfterClip, 2)
	assert.Contains(t, eventsAfterClip[0].Title, "Synced image (PNG)")
}

func TestNotificationsDisabledInSettings(t *testing.T) {
	s := settings.DefaultSettings()
	s.NotificationsEnabled = false
	require.NoError(t, settings.Save(s))

	// When disabled, Send returns nil without invoking system tools
	err := Send("Silent Title", "Silent Message", UrgencyNormal)
	assert.NoError(t, err)
}
