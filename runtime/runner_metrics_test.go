package runtime_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jonboulle/clockwork"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/attribute"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"

	"github.com/zakyalvan/krtlwrkflw/action"
	"github.com/zakyalvan/krtlwrkflw/engine"
	"github.com/zakyalvan/krtlwrkflw/model"
	"github.com/zakyalvan/krtlwrkflw/runtime"
)

// counterValueEmit is like counterValue (defined in observability_test.go) but
// matches attribute values using attribute.Value.Emit(), which handles all
// attribute types (BOOL, INT64, STRING, …) — not just STRING. This is needed
// when the counter carries a BOOL attribute (e.g. "retryable").
func counterValueEmit(rm metricdata.ResourceMetrics, name string, filter map[string]string) int64 {
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			if m.Name != name {
				continue
			}
			sum, ok := m.Data.(metricdata.Sum[int64])
			if !ok {
				return 0
			}
			var total int64
			for _, dp := range sum.DataPoints {
				if dpEmitMatch(dp.Attributes, filter) {
					total += dp.Value
				}
			}
			return total
		}
	}
	return 0
}

// dpEmitMatch reports whether all key/value pairs in filter are present in
// attrs, comparing each value via attribute.Value.Emit() so that BOOL and
// INT64 attributes are matched by their string representation.
func dpEmitMatch(attrs attribute.Set, filter map[string]string) bool {
	for k, want := range filter {
		v, ok := attrs.Value(attribute.Key(k))
		if !ok || v.Emit() != want {
			return false
		}
	}
	return true
}

// failingActionDef builds start → task("fail") → end with no retry policy,
// so the first (and only) action failure drives the instance to StatusFailed.
func failingActionDef() *model.ProcessDefinition {
	return &model.ProcessDefinition{
		ID: "failing-action-metrics", Version: 1,
		Nodes: []model.Node{
			model.NewStartEvent("start"),
			model.NewServiceTask("task", model.WithActionName("fail")),
			model.NewEndEvent("end"),
		},
		Flows: []model.SequenceFlow{
			{ID: "f1", Source: "start", Target: "task"},
			{ID: "f2", Source: "task", Target: "end"},
		},
	}
}

// timerMetricsDef builds start → timer-catch("1h") → end.
func timerMetricsDef() *model.ProcessDefinition {
	return &model.ProcessDefinition{
		ID: "timer-metrics", Version: 1,
		Nodes: []model.Node{
			model.NewStartEvent("start"),
			model.NewIntermediateCatchEvent("wait1h", model.WithTimerDuration(`"1h"`)),
			model.NewEndEvent("end"),
		},
		Flows: []model.SequenceFlow{
			{ID: "f1", Source: "start", Target: "wait1h"},
			{ID: "f2", Source: "wait1h", Target: "end"},
		},
	}
}

// TestActionFailuresCounter asserts that wrkflw_action_failures_total is
// incremented with the correct action and retryable attributes when a service
// action returns an error (non-retryable path, no retry policy on the definition).
func TestActionFailuresCounter(t *testing.T) {
	t.Parallel()

	type testCase struct {
		name       string
		actionErr  error
		wantAction string
		wantRetry  string
		wantCount  int64
	}

	cases := []testCase{
		{
			// Plain error: IsRetryable returns true by default (no Retryabler interface).
			name:       "plain error increments action_failures with retryable=true",
			actionErr:  errors.New("transient failure"),
			wantAction: "fail",
			wantRetry:  "true",
			wantCount:  1,
		},
		{
			// NonRetryable-wrapped error: IsRetryable returns false.
			name:       "non-retryable error increments action_failures with retryable=false",
			actionErr:  action.NonRetryable(errors.New("hard failure")),
			wantAction: "fail",
			wantRetry:  "false",
			wantCount:  1,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			reader := sdkmetric.NewManualReader()
			mp := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))

			cat := action.NewMapCatalog(map[string]action.ServiceAction{
				"fail": action.Func(func(_ context.Context, _ map[string]any) (map[string]any, error) {
					return nil, tc.actionErr
				}),
			})

			r := runtime.NewRunner(cat, mustMemStore(t),
				runtime.WithMeterProvider(mp),
			)

			st, err := r.Run(t.Context(), failingActionDef(), "fail-metrics-1", nil)
			require.NoError(t, err, "Run must not return an error even when an action fails")
			assert.Equal(t, engine.StatusFailed, st.Status,
				"instance must reach StatusFailed after non-retried action failure")

			rm := collect(t, reader)
			// Use counterValueEmit to handle the BOOL "retryable" attribute.
			got := counterValueEmit(rm, "wrkflw_action_failures_total",
				map[string]string{"action": tc.wantAction, "retryable": tc.wantRetry})
			assert.EqualValues(t, tc.wantCount, got,
				"wrkflw_action_failures_total{action=%s,retryable=%s} = %d, want %d",
				tc.wantAction, tc.wantRetry, got, tc.wantCount)
		})
	}
}

// TestTimerFiredCounter asserts that wrkflw_timer_fired_total is incremented
// when a timer fires via the fake-clock + MemScheduler path.
func TestTimerFiredCounter(t *testing.T) {
	t.Parallel()

	reader := sdkmetric.NewManualReader()
	mp := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))

	startAt := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	fc := clockwork.NewFakeClockAt(startAt)

	cat := action.NewMapCatalog(nil)
	sched := runtime.NewMemScheduler(runtime.WithMemSchedulerClock(fc))
	store := mustMemStore(t)

	r := runtime.NewRunner(cat, store,
		runtime.WithRunnerClock(fc),
		runtime.WithScheduler(sched),
		runtime.WithMeterProvider(mp),
	)

	def := timerMetricsDef()
	const instanceID = "timer-metrics-1"

	// Run → parks at the intermediate timer node; no timer fired yet.
	parked, err := r.Run(t.Context(), def, instanceID, nil)
	require.NoError(t, err)
	require.Equal(t, engine.StatusRunning, parked.Status)

	// Counter must be 0 before tick.
	rmBefore := collect(t, reader)
	assert.EqualValues(t, 0, counterValue(rmBefore, "wrkflw_timer_fired_total", nil),
		"wrkflw_timer_fired_total must be 0 before timer fires")

	// Advance past the 1-hour timer and tick the scheduler.
	fc.Advance(1*time.Hour + 1*time.Second)
	require.NoError(t, sched.Tick(t.Context()))

	// Counter must be 1 after the timer fires.
	rmAfter := collect(t, reader)
	got := counterValue(rmAfter, "wrkflw_timer_fired_total", nil)
	assert.EqualValues(t, 1, got,
		"wrkflw_timer_fired_total = %d, want 1 after timer fires", got)
}
