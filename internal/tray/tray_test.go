package tray

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTrayStatusAndMenu(t *testing.T) {
	tray := New()
	require.NotNil(t, tray)

	assert.Equal(t, StatusActive, tray.Status())
	items := tray.MenuItems()
	assert.NotEmpty(t, items)
	assert.Contains(t, items[1], "Active")

	tray.SetStatus(StatusConnected)
	assert.Equal(t, StatusConnected, tray.Status())
	itemsConnected := tray.MenuItems()
	assert.Contains(t, itemsConnected[1], "Connected")
}

func TestTrayRunContextCancel(t *testing.T) {
	tray := New()
	ctx, cancel := context.WithCancel(context.Background())

	errCh := make(chan error, 1)
	go func() {
		errCh <- tray.Run(ctx)
	}()

	time.Sleep(10 * time.Millisecond)
	cancel()

	select {
	case err := <-errCh:
		assert.ErrorIs(t, err, context.Canceled)
	case <-time.After(time.Second):
		t.Fatal("tray.Run did not terminate on context cancellation")
	}
}

func TestNotificationEmitters(t *testing.T) {
	EmitStartupToast()
	EmitClipboardSyncNotification("PNG", 1024)
	EmitFileTransferNotification("test.txt", 42)
}

func TestTrayDispatch(t *testing.T) {
	tray := New()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() {
		_ = tray.Run(ctx)
	}()

	time.Sleep(10 * time.Millisecond)
	// Dispatch known action
	Dispatch("downloads")
	time.Sleep(10 * time.Millisecond)
}
