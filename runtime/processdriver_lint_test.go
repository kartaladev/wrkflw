package runtime_test

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zakyalvan/krtlwrkflw/action"
	"github.com/zakyalvan/krtlwrkflw/definition"
	"github.com/zakyalvan/krtlwrkflw/definition/activity"
	"github.com/zakyalvan/krtlwrkflw/definition/event"
	"github.com/zakyalvan/krtlwrkflw/definition/schedule"
	"github.com/zakyalvan/krtlwrkflw/runtime"
)

// lintNoopCatalog resolves a single no-op action.
func lintNoopCatalog() action.Catalog {
	return action.NewCatalog(map[string]action.Action{
		"noop": action.ActionFunc(func(context.Context, map[string]any) (map[string]any, error) { return nil, nil }),
	})
}

// TestProcessDriverLogsLintWarnings verifies the driver runs definition.Lint once
// per definition on Drive and logs each warning at WARN — non-fatal, deduped.
func TestProcessDriverLogsLintWarnings(t *testing.T) {
	t.Run("reminder on a ServiceTask logs one WARN, deduped across drives", func(t *testing.T) {
		var buf bytes.Buffer
		logger := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn}))

		def, err := definition.NewBuilder("lint-demo", 1).
			Add(event.NewStart("start")).
			Add(activity.NewServiceTask("svc",
				activity.WithActionName("noop"),
				activity.WithWaitReminder(schedule.Every(30*time.Minute), "nudge"))).
			Add(event.NewEnd("end")).
			Connect("start", "svc").
			Connect("svc", "end").
			Build()
		require.NoError(t, err)

		driver, err := runtime.NewProcessDriver(runtime.WithActionCatalog(lintNoopCatalog()), runtime.WithLogger(logger))
		require.NoError(t, err)
		t.Cleanup(func() { _ = driver.Shutdown(context.Background()) })

		// Drive the SAME definition twice; the lint warning must be logged once.
		_, err = driver.Drive(t.Context(), def, "i1", nil)
		require.NoError(t, err, "lint warning must be non-fatal")
		_, err = driver.Drive(t.Context(), def, "i2", nil)
		require.NoError(t, err)

		lines := splitNonEmpty(buf.Bytes())
		require.Len(t, lines, 1, "exactly one lint WARN across two drives of the same def; got: %s", buf.String())

		var m map[string]any
		require.NoError(t, json.Unmarshal(lines[0], &m))
		assert.Equal(t, "WARN", m["level"])
		assert.Equal(t, "definition lint warning", m["msg"])
		assert.Equal(t, "svc", m["node_id"])
		assert.Equal(t, "reminder-ignored", m["rule"])
	})

	t.Run("a clean definition logs nothing", func(t *testing.T) {
		var buf bytes.Buffer
		logger := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn}))

		def, err := definition.NewBuilder("clean", 1).
			Add(event.NewStart("start")).
			Add(activity.NewServiceTask("svc", activity.WithActionName("noop"))).
			Add(event.NewEnd("end")).
			Connect("start", "svc").
			Connect("svc", "end").
			Build()
		require.NoError(t, err)

		driver, err := runtime.NewProcessDriver(runtime.WithActionCatalog(lintNoopCatalog()), runtime.WithLogger(logger))
		require.NoError(t, err)
		t.Cleanup(func() { _ = driver.Shutdown(context.Background()) })

		_, err = driver.Drive(t.Context(), def, "i1", nil)
		require.NoError(t, err)
		assert.Empty(t, splitNonEmpty(buf.Bytes()), "a clean definition must log no lint warnings")
	})
}
