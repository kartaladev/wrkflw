package watermill_test

import (
	"bytes"
	"errors"
	"log/slog"
	"strings"
	"testing"

	"github.com/ThreeDotsLabs/watermill"
	"github.com/stretchr/testify/require"
	watermillpub "github.com/zakyalvan/krtlwrkflw/internal/eventing/watermill"
)

func newTestLogger(buf *bytes.Buffer) watermill.LoggerAdapter {
	logger := slog.New(slog.NewTextHandler(buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	return watermillpub.NewWatermillLogger(logger)
}

func TestNewWatermillLoggerForwardsToSlog(t *testing.T) {
	var buf bytes.Buffer
	wl := newTestLogger(&buf)
	wl.Info("subscriber started", watermill.LogFields{"topic": "instance.completed"})

	out := buf.String()
	require.Contains(t, out, "subscriber started")
	require.Contains(t, out, "instance.completed")
	require.True(t, strings.Contains(out, "topic"))
}

func TestWatermillLoggerError(t *testing.T) {
	var buf bytes.Buffer
	wl := newTestLogger(&buf)
	err := errors.New("broker connection refused")
	wl.Error("publish failed", err, watermill.LogFields{"topic": "instance.completed"})

	out := buf.String()
	require.Contains(t, out, "publish failed")
	require.Contains(t, out, "broker connection refused")
	require.Contains(t, out, "error")
}

func TestWatermillLoggerDebug(t *testing.T) {
	var buf bytes.Buffer
	wl := newTestLogger(&buf)
	wl.Debug("polling outbox", watermill.LogFields{"batch": 100})

	out := buf.String()
	require.Contains(t, out, "polling outbox")
	require.Contains(t, out, "batch")
}

func TestWatermillLoggerWith(t *testing.T) {
	var buf bytes.Buffer
	wl := newTestLogger(&buf)

	child := wl.With(watermill.LogFields{"component": "relay"})
	child.Info("drain started", watermill.LogFields{"topic": "instance.completed"})

	out := buf.String()
	require.Contains(t, out, "drain started")
	// Parent fields must appear in child log output.
	require.Contains(t, out, "component")
	require.Contains(t, out, "relay")
	// Child-call fields must also appear.
	require.Contains(t, out, "instance.completed")
}
