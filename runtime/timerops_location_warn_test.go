package runtime_test

// timerops_location_warn_test.go
//
// schedulingLocation falls back to time.UTC when the configured scheduler does
// not implement the opt-in Location() capability. The fallback was SILENT: a
// foreign scheduler resolving at-times in, say, Asia/Jakarta had every persisted
// NextRun computed 7 hours away from the instant it actually fires, with no log
// line at any level. The fallback stays (it is the only safe default); it must
// simply announce itself, once per driver, at the moment it is first relied on.
//
// ⚠ Two of the three seams the audit blamed are NOT broken: NativeScheduler.Location
// IS exported (scheduler/scheduler.go), and jobStore.Save's job-implementation
// assertion returns a typed error rather than falling back silently
// (runtime/jobstore.go). Only the UTC fallback is real.

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/jonboulle/clockwork"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kartaladev/wrkflw/engine"
	"github.com/kartaladev/wrkflw/processtest"
	"github.com/kartaladev/wrkflw/runtime"
	"github.com/kartaladev/wrkflw/runtime/internal/runtimetest"
)

// locationWarnLines returns the decoded JSON log records in buf whose message
// mentions the missing scheduling-location capability.
func locationWarnLines(t *testing.T, buf *bytes.Buffer) []map[string]any {
	t.Helper()

	var out []map[string]any
	for _, line := range strings.Split(strings.TrimSpace(buf.String()), "\n") {
		if line == "" {
			continue
		}
		var rec map[string]any
		require.NoError(t, json.Unmarshal([]byte(line), &rec), "log line must be JSON: %s", line)
		if msg, _ := rec["msg"].(string); strings.Contains(msg, "does not report a scheduling location") {
			out = append(out, rec)
		}
	}
	return out
}

// TestSchedulerWithoutLocationCapabilityWarnsOnce pins the fallback warning:
// arming a timer against a scheduler that omits Location() must announce the
// UTC assumption.
//
// What makes it fail before the fix: schedulingLocation's fallback contains no
// log statement of any level, so the buffer holds zero matching records however
// many timers are armed.
//
// The second Drive is load-bearing in the other direction: the warning is a
// standing configuration fact, not a per-arm event, so a fix that logs on every
// arm would spam an operator once per timer and is rejected here.
func TestSchedulerWithoutLocationCapabilityWarnsOnce(t *testing.T) {
	t.Parallel()

	ctx := t.Context()

	fc := clockwork.NewFakeClockAt(time.Date(2026, 8, 20, 9, 0, 0, 0, time.UTC))
	sched := processtest.NewMemScheduler(processtest.WithMemSchedulerClock(fc))

	_, located := any(sched).(interface{ Location() *time.Location })
	require.False(t, located,
		"fixture: the double must NOT report a location, or this test proves nothing")

	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn}))

	store := runtimetest.MustMemStore(t)
	driver := runtimetest.MustProcessDriver(t, nil, store,
		runtime.WithClock(fc),
		runtime.WithScheduler(sched),
		runtime.WithLogger(logger),
	)

	assert.Empty(t, locationWarnLines(t, &buf),
		"construction alone must not warn: a driver that never arms a timer never relies on the fallback")

	def := conflictTimerDef()
	first, err := driver.Drive(ctx, def, "locwarn-1", nil)
	require.NoError(t, err)
	require.Equal(t, engine.StatusRunning, first.Status, "fixture: the instance must park at the timer, i.e. a timer was armed")

	warns := locationWarnLines(t, &buf)
	require.Len(t, warns, 1, "arming a timer under a location-less scheduler must warn exactly once")
	assert.Contains(t, warns[0], "scheduler_type", "the warning must name the offending scheduler")
	assert.Equal(t, "UTC", warns[0]["assumed_location"], "the warning must state the location it assumed")

	second, err := driver.Drive(ctx, def, "locwarn-2", nil)
	require.NoError(t, err)
	require.Equal(t, engine.StatusRunning, second.Status)

	assert.Len(t, locationWarnLines(t, &buf), 1,
		"the warning is once per driver — a second armed timer must not repeat it")
}
