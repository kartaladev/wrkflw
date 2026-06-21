package watermill_test

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"

	"github.com/ThreeDotsLabs/watermill"
	"github.com/stretchr/testify/require"
	watermillpub "github.com/zakyalvan/krtlwrkflw/internal/eventing/watermill"
)

func TestNewWatermillLoggerForwardsToSlog(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))

	var wl watermill.LoggerAdapter = watermillpub.NewWatermillLogger(logger)
	wl.Info("subscriber started", watermill.LogFields{"topic": "instance.completed"})

	out := buf.String()
	require.Contains(t, out, "subscriber started")
	require.Contains(t, out, "instance.completed")
	require.True(t, strings.Contains(out, "topic"))
}
