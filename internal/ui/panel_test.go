package ui

import (
	"testing"
	"time"

	"github.com/cirruslabs/tart-guest-agent/internal/activity"
	"github.com/stretchr/testify/assert"
)

func TestFormatRecentNotificationsEmpty(t *testing.T) {
	text := FormatRecentNotifications(nil)
	assert.Contains(t, text, "No recent activity")
}

func TestFormatRecentNotificationsWithEvents(t *testing.T) {
	events := []activity.Event{
		{
			ID:        "test1",
			Timestamp: time.Now(),
			Category:  activity.CategoryClipboardText,
			Title:     "Copied 42 chars",
			Detail:    "Sample Text",
			Status:    "success",
		},
		{
			ID:        "test2",
			Timestamp: time.Now(),
			Category:  activity.CategoryFileTransfer,
			Title:     "Received screenshot.png",
			Detail:    "1.2MB",
			Status:    "success",
		},
	}

	text := FormatRecentNotifications(events)
	assert.Contains(t, text, "Recent Guest Agent Activity")
	assert.Contains(t, text, "Copied 42 chars")
	assert.Contains(t, text, "Received screenshot.png")
}

func TestCategoryIcons(t *testing.T) {
	assert.Equal(t, "📋", categoryIcon(activity.CategoryClipboardText, "success"))
	assert.Equal(t, "🖼️", categoryIcon(activity.CategoryClipboardImage, "success"))
	assert.Equal(t, "📁", categoryIcon(activity.CategoryFileTransfer, "success"))
	assert.Equal(t, "❌", categoryIcon(activity.CategorySystem, "error"))
	assert.Equal(t, "⚠️", categoryIcon(activity.CategorySystem, "warning"))
}
