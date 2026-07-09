package runtime_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jonboulle/clockwork"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	"github.com/zakyalvan/krtlwrkflw/action"
	"github.com/zakyalvan/krtlwrkflw/authz"
	"github.com/zakyalvan/krtlwrkflw/clock"
	"github.com/zakyalvan/krtlwrkflw/definition/activity"
	"github.com/zakyalvan/krtlwrkflw/definition/event"
	"github.com/zakyalvan/krtlwrkflw/definition/flow"
	"github.com/zakyalvan/krtlwrkflw/definition/model"
	"github.com/zakyalvan/krtlwrkflw/engine"
	"github.com/zakyalvan/krtlwrkflw/humantask"
	"github.com/zakyalvan/krtlwrkflw/runtime"
	"github.com/zakyalvan/krtlwrkflw/runtime/internal/runtimetest"
	"github.com/zakyalvan/krtlwrkflw/runtime/task"
)

// collect gathers a ResourceMetrics snapshot from a ManualReader.
func collect(t *testing.T, reader *sdkmetric.ManualReader) metricdata.ResourceMetrics {
	t.Helper()
	var rm metricdata.ResourceMetrics
	require.NoError(t, reader.Collect(context.Background(), &rm))
	return rm
}

// counterValue sums all Int64Sum data points whose attributes contain all
// entries in filter (nil filter matches any). Returns the summed value, or 0
// if the metric is not found.
func counterValue(rm metricdata.ResourceMetrics, name string, filter map[string]string) int64 {
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
				if dpAttributesMatch(dp.Attributes, filter) {
					total += dp.Value
				}
			}
			return total
		}
	}
	return 0
}

// dpAttributesMatch reports whether all key/value pairs in filter are present
// in the attribute.Set attrs.
func dpAttributesMatch(attrs attribute.Set, filter map[string]string) bool {
	for k, want := range filter {
		v, ok := attrs.Value(attribute.Key(k))
		if !ok || v.AsString() != want {
			return false
		}
	}
	return true
}

// histogramCount returns the total number of observations recorded for a
// Float64Histogram instrument (sum of all data-point Counts).
func histogramCount(rm metricdata.ResourceMetrics, name string) uint64 {
	return histogramCountFiltered(rm, name, nil)
}

// histogramCountFiltered returns the total Count across data-points whose
// attributes contain all key/value pairs in filter (nil filter matches any).
func histogramCountFiltered(rm metricdata.ResourceMetrics, name string, filter map[string]string) uint64 {
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			if m.Name != name {
				continue
			}
			hist, ok := m.Data.(metricdata.Histogram[float64])
			if !ok {
				return 0
			}
			var total uint64
			for _, dp := range hist.DataPoints {
				if dpAttributesMatch(dp.Attributes, filter) {
					total += dp.Count
				}
			}
			return total
		}
	}
	return 0
}

// TestNewRunnerWithObservabilityOptions verifies a Runner can be constructed
// with each of the three observability options without panicking.
func TestNewRunnerWithObservabilityOptions(t *testing.T) {
	t.Parallel()

	type testCase struct {
		name   string
		opt    runtime.Option
		assert func(t *testing.T, driver *runtime.ProcessDriver)
	}

	cases := []testCase{
		{
			name: "with logger",
			opt:  runtime.WithLogger(slog.New(slog.NewTextHandler(io.Discard, nil))),
			assert: func(t *testing.T, driver *runtime.ProcessDriver) {
				require.NotNil(t, driver)
			},
		},
		{
			name: "with tracer provider",
			opt:  runtime.WithTracerProvider(sdktrace.NewTracerProvider()),
			assert: func(t *testing.T, driver *runtime.ProcessDriver) {
				require.NotNil(t, driver)
			},
		},
		{
			name: "with meter provider",
			opt:  runtime.WithMeterProvider(sdkmetric.NewMeterProvider()),
			assert: func(t *testing.T, driver *runtime.ProcessDriver) {
				require.NotNil(t, driver)
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			driver := runtimetest.MustRunner(t, action.NewCatalog(nil), runtimetest.MustMemStore(t),
				tc.opt,
			)
			tc.assert(t, driver)
		})
	}
}

// TestStepSpanAndLifecycleMetrics verifies that running a linear process to
// completion via [runtime.ProcessDriver.Drive] produces:
//   - at least one "wrkflw.step" span in the OTel trace,
//   - wrkflw_instances_started_total == 1,
//   - wrkflw_instances_completed_total{status="completed"} == 1,
//   - at least one observation in wrkflw_step_duration_seconds.
func TestStepSpanAndLifecycleMetrics(t *testing.T) {
	sr := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(sr))
	reader := sdkmetric.NewManualReader()
	mp := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))

	cat := action.NewCatalog(map[string]action.Action{
		"greet": action.ActionFunc(func(_ context.Context, _ map[string]any) (map[string]any, error) {
			return map[string]any{"greeted": true}, nil
		}),
	})

	driver := runtimetest.MustRunner(t, cat, runtimetest.MustMemStore(t),
		runtime.WithTracerProvider(tp),
		runtime.WithMeterProvider(mp),
	)

	// linearDef() is defined in example_test.go: start → greet (service) → end.
	_, err := driver.Drive(t.Context(), linearDef(), "i1", map[string]any{"name": "world"})
	require.NoError(t, err, "run must succeed for linear process")

	// Assert at least one wrkflw.step span was recorded.
	var sawStep bool
	for _, s := range sr.Ended() {
		if s.Name() == "wrkflw.step" {
			sawStep = true
		}
	}
	if !sawStep {
		t.Fatal("expected at least one 'wrkflw.step' span but none were recorded")
	}

	// Assert metric counters.
	rm := collect(t, reader)
	if v := counterValue(rm, "wrkflw_instances_started_total", nil); v != 1 {
		t.Fatalf("wrkflw_instances_started_total = %d, want 1", v)
	}
	if v := counterValue(rm, "wrkflw_instances_completed_total", map[string]string{"status": "completed"}); v != 1 {
		t.Fatalf("wrkflw_instances_completed_total{status=completed} = %d, want 1", v)
	}

	// Assert the step-duration histogram received at least one observation.
	if c := histogramCount(rm, "wrkflw_step_duration_seconds"); c == 0 {
		t.Fatal("wrkflw_step_duration_seconds has 0 observations, want ≥1")
	}
}

// paymentDef returns a minimal start→charge(service)→end process definition.
func paymentDef() *model.ProcessDefinition {
	return &model.ProcessDefinition{
		ID: "payment", Version: 1,
		Nodes: []model.Node{
			event.NewStart("start"),
			activity.NewServiceTask("charge", activity.WithTaskAction("charge")),
			event.NewEnd("end"),
		},
		Flows: []flow.SequenceFlow{
			{ID: "f1", Source: "start", Target: "charge"},
			{ID: "f2", Source: "charge", Target: "end"},
		},
	}
}

// TestActionSpanAndDurationMetric verifies that running a start→service-task→end
// process produces:
//   - spans "wrkflw.runner.Run", "wrkflw.step", and "wrkflw.action charge",
//   - exactly one observation in wrkflw_action_duration_seconds per outcome.
//
// M1: the error-outcome row additionally asserts that the span carries Error
// status, covering the restructured I1 path that was previously untraced.
func TestActionSpanAndDurationMetric(t *testing.T) {
	t.Parallel()

	type testCase struct {
		name        string
		actionFunc  func(_ context.Context, _ map[string]any) (map[string]any, error)
		wantOutcome string
		assert      func(t *testing.T, sr *tracetest.SpanRecorder, rm metricdata.ResourceMetrics)
	}

	cases := []testCase{
		{
			name: "ok outcome records span and metric",
			actionFunc: func(_ context.Context, _ map[string]any) (map[string]any, error) {
				return map[string]any{"charged": true}, nil
			},
			wantOutcome: "ok",
			assert: func(t *testing.T, sr *tracetest.SpanRecorder, rm metricdata.ResourceMetrics) {
				t.Helper()
				names := map[string]bool{}
				for _, s := range sr.Ended() {
					names[s.Name()] = true
				}
				for _, want := range []string{"wrkflw.runner.Run", "wrkflw.step", "wrkflw.action charge"} {
					assert.Truef(t, names[want], "missing span %q; got %v", want, names)
				}
				c := histogramCountFiltered(rm, "wrkflw_action_duration_seconds", map[string]string{"action": "charge", "outcome": "ok"})
				assert.EqualValues(t, 1, c, "wrkflw_action_duration_seconds{action=charge,outcome=ok} count = %d, want 1", c)
			},
		},
		{
			// M1: action returns an error; the restructured I1 span must be recorded
			// with Error status and the duration histogram must capture one sample
			// with outcome=error (elapsed==0 is honest: a.Do was called but failed).
			name: "error outcome records span with error status and metric",
			actionFunc: func(_ context.Context, _ map[string]any) (map[string]any, error) {
				return nil, errors.New("payment declined")
			},
			wantOutcome: "error",
			assert: func(t *testing.T, sr *tracetest.SpanRecorder, rm metricdata.ResourceMetrics) {
				t.Helper()
				var actionSpanFound bool
				for _, s := range sr.Ended() {
					if s.Name() == "wrkflw.action charge" {
						actionSpanFound = true
						assert.Equal(t, codes.Error, s.Status().Code,
							"action span must carry Error status on action failure")
					}
				}
				assert.True(t, actionSpanFound, "wrkflw.action charge span must be recorded even on failure")
				c := histogramCountFiltered(rm, "wrkflw_action_duration_seconds", map[string]string{"action": "charge", "outcome": "error"})
				assert.EqualValues(t, 1, c, "wrkflw_action_duration_seconds{action=charge,outcome=error} count = %d, want 1", c)
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			sr := tracetest.NewSpanRecorder()
			tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(sr))
			reader := sdkmetric.NewManualReader()
			mp := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))

			cat := action.NewCatalog(map[string]action.Action{
				"charge": action.ActionFunc(tc.actionFunc),
			})
			driver := runtimetest.MustRunner(t, cat, runtimetest.MustMemStore(t),
				runtime.WithTracerProvider(tp), runtime.WithMeterProvider(mp))

			_, _ = driver.Drive(t.Context(), paymentDef(), "i1", map[string]any{})

			rm := collect(t, reader)
			tc.assert(t, sr, rm)
		})
	}
}

// TestIncidentsResolvedMetric drives an instance to an incident (via MaxAttempts=1)
// then calls ResolveIncident and asserts wrkflw_incidents_resolved_total{def=...}==1.
// This is M2: covers the incidentsResolved counter that was previously untested.
func TestIncidentsResolvedMetric(t *testing.T) {
	t.Parallel()

	reader := sdkmetric.NewManualReader()
	mp := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))

	T := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	clk := clockwork.NewFakeClockAt(T)

	var calls atomic.Int32
	cat := action.NewCatalog(map[string]action.Action{
		"a": action.ActionFunc(func(_ context.Context, _ map[string]any) (map[string]any, error) {
			if calls.Add(1) == 1 {
				return nil, errors.New("first call fails")
			}
			return map[string]any{"done": true}, nil
		}),
	})

	def := &model.ProcessDefinition{
		ID: "incident-obs", Version: 1,
		Nodes: []model.Node{
			event.NewStart("start"),
			activity.NewServiceTask("task", activity.WithTaskAction("a")),
			event.NewEnd("end"),
		},
		Flows: []flow.SequenceFlow{
			{ID: "f1", Source: "start", Target: "task"},
			{ID: "f2", Source: "task", Target: "end"},
		},
	}

	driver := runtimetest.MustRunner(t, cat, runtimetest.MustMemStore(t),
		runtime.WithClock(clk),
		runtime.WithMeterProvider(mp),
		// MaxAttempts=1: first failure parks immediately as an incident.
		runtime.WithDefaultRetryPolicy(model.RetryPolicy{
			MaxAttempts:     1,
			InitialInterval: time.Second,
			BackoffCoef:     1,
			MaxInterval:     time.Minute,
		}),
	)

	// Step 1: Run → first action failure → incident, instance parks.
	st, err := driver.Drive(t.Context(), def, "obs-inc-1", nil)
	require.NoError(t, err)
	require.Equal(t, engine.StatusRunning, st.Status, "instance must park as running with an incident")
	require.Len(t, st.Incidents, 1, "want exactly one incident after first failure")

	incID := st.Incidents[0].ID

	// Step 2: ResolveIncident → action succeeds on second call → instance completes.
	st2, err := driver.ResolveIncident(t.Context(), def, "obs-inc-1", incID, 2)
	require.NoError(t, err)
	assert.Equal(t, engine.StatusCompleted, st2.Status, "instance must complete after resolve")

	// Step 3: Assert wrkflw_incidents_resolved_total{def=incident-obs} == 1.
	rm := collect(t, reader)
	v := counterValue(rm, "wrkflw_incidents_resolved_total", map[string]string{"def": "incident-obs"})
	assert.EqualValues(t, 1, v, "wrkflw_incidents_resolved_total{def=incident-obs} = %d, want 1", v)
}

// TestHumanTaskLifecycleCounter verifies that wrkflw_human_tasks_total is
// incremented for each lifecycle event emitted by Runner and TaskService.
//
//   - {event=created}    : emitted by Runner when it parks at a user task.
//   - {event=claimed}    : emitted by TaskService.Claim on success.
//   - {event=reassigned} : emitted by TaskService.Reassign on success.
//   - {event=completed}  : emitted by TaskService.Complete on success.
//
// Both Runner and TaskService use the same metric name and instrumentation
// scope so their increments aggregate into one stream.
func TestHumanTaskLifecycleCounter(t *testing.T) {
	t.Parallel()

	type eventCase struct {
		event  string
		assert func(t *testing.T, rm metricdata.ResourceMetrics)
	}

	manager := authz.Actor{ID: "alice", Roles: []string{"manager"}}
	admin := authz.Actor{ID: "admin", Roles: []string{"manager"}}

	cases := []eventCase{
		{
			event: "created",
			assert: func(t *testing.T, rm metricdata.ResourceMetrics) {
				t.Helper()
				v := counterValue(rm, "wrkflw_human_tasks_total", map[string]string{"event": "created"})
				assert.EqualValues(t, 1, v, "want wrkflw_human_tasks_total{event=created}==1")
			},
		},
		{
			event: "claimed",
			assert: func(t *testing.T, rm metricdata.ResourceMetrics) {
				t.Helper()
				v := counterValue(rm, "wrkflw_human_tasks_total", map[string]string{"event": "claimed"})
				assert.EqualValues(t, 1, v, "want wrkflw_human_tasks_total{event=claimed}==1")
			},
		},
		{
			event: "reassigned",
			assert: func(t *testing.T, rm metricdata.ResourceMetrics) {
				t.Helper()
				v := counterValue(rm, "wrkflw_human_tasks_total", map[string]string{"event": "reassigned"})
				assert.EqualValues(t, 1, v, "want wrkflw_human_tasks_total{event=reassigned}==1")
			},
		},
		{
			event: "completed",
			assert: func(t *testing.T, rm metricdata.ResourceMetrics) {
				t.Helper()
				v := counterValue(rm, "wrkflw_human_tasks_total", map[string]string{"event": "completed"})
				assert.EqualValues(t, 1, v, "want wrkflw_human_tasks_total{event=completed}==1")
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.event, func(t *testing.T) {
			t.Parallel()

			reader := sdkmetric.NewManualReader()
			mp := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))

			taskStore := humantask.NewMemTaskStore()
			resolver := humantask.NewStaticActorResolver(map[string][]authz.Actor{
				"manager": {manager, admin},
			})
			az := authz.RoleAuthorizer{}
			clk := clock.System()

			driver := runtimetest.MustRunner(t, nil, runtimetest.MustMemStore(t),
				runtime.WithClock(clk),
				runtime.WithHumanTasks(resolver, taskStore, az),
				runtime.WithMeterProvider(mp),
			)
			svc := runtimetest.MustTaskService(t, taskStore, az,
				task.WithClock(clk),
				task.WithTaskServiceMeterProvider(mp))

			def := runtimetest.ApprovalDef()
			const instID = "htlc-inst"

			// Run parks at the user task → emits {event=created}.
			_, err := driver.Drive(t.Context(), def, instID, nil)
			require.NoError(t, err, "Run must succeed and park at user task")

			claimable, err := taskStore.ClaimableBy(t.Context(), manager)
			require.NoError(t, err)
			require.Len(t, claimable, 1)
			taskToken := claimable[0].TaskToken

			if tc.event == "claimed" || tc.event == "reassigned" || tc.event == "completed" {
				// Claim → emits {event=claimed}.
				claimTrg, err := svc.Claim(t.Context(), taskToken, manager)
				require.NoError(t, err)
				_, err = driver.ApplyTrigger(t.Context(), def, instID, claimTrg)
				require.NoError(t, err)
			}

			if tc.event == "reassigned" || tc.event == "completed" {
				// Reassign → emits {event=reassigned}.
				reassignTrg, err := svc.Reassign(t.Context(), taskToken, manager.ID, admin.ID, admin)
				require.NoError(t, err)
				_, err = driver.ApplyTrigger(t.Context(), def, instID, reassignTrg)
				require.NoError(t, err)
			}

			if tc.event == "completed" {
				// Complete → emits {event=completed}.
				completeTrg, err := svc.Complete(t.Context(), taskToken, admin, map[string]any{"approved": true})
				require.NoError(t, err)
				_, err = driver.ApplyTrigger(t.Context(), def, instID, completeTrg)
				require.NoError(t, err)
			}

			rm := collect(t, reader)
			tc.assert(t, rm)
		})
	}
}

// TestDeliverSpan verifies that Runner.ApplyTrigger produces a "wrkflw.runner.ApplyTrigger" span.
// This is M3: covers the ApplyTrigger entry-point span that was previously untested.
func TestDeliverSpan(t *testing.T) {
	t.Parallel()

	sr := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(sr))

	T := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	clk := clockwork.NewFakeClockAt(T)

	// Use a message-catch process: start → catch-message → end.
	// The CorrelationKey is a literal expression `"ord-42"` (quoted so the engine
	// evaluates it to the string "ord-42" without referencing a process variable).
	// After Run parks at the catch-message node, we ApplyTrigger a MessageReceived
	// trigger — that single ApplyTrigger call must produce a "wrkflw.runner.ApplyTrigger" span.
	msgDef := &model.ProcessDefinition{
		ID: "msg-deliver-obs", Version: 1,
		Nodes: []model.Node{
			event.NewStart("start"),
			event.NewIntermediateCatch("catch", event.WithMessageCorrelator("pay.confirmed", `"ord-42"`)),
			event.NewEnd("end"),
		},
		Flows: []flow.SequenceFlow{
			{ID: "f1", Source: "start", Target: "catch"},
			{ID: "f2", Source: "catch", Target: "end"},
		},
	}

	store := runtimetest.MustMemStore(t)
	driver := runtimetest.MustRunner(t, nil, store, runtime.WithClock(clk), runtime.WithTracerProvider(tp))

	// Run parks at the catch-message node.
	parked, err := driver.Drive(t.Context(), msgDef, "del-obs-1", nil)
	require.NoError(t, err)
	require.Equal(t, engine.StatusRunning, parked.Status, "instance must park at the catch-message node")

	// ApplyTrigger a MessageReceived trigger — this is the ApplyTrigger call we want to trace.
	trg := engine.NewMessageReceived(clk.Now(), "pay.confirmed", "ord-42", map[string]any{"ref": "pay-1"})
	final, err := driver.ApplyTrigger(t.Context(), msgDef, "del-obs-1", trg)
	require.NoError(t, err)
	assert.Equal(t, engine.StatusCompleted, final.Status, "instance must complete after message delivery")

	// Assert the "wrkflw.runner.ApplyTrigger" span was recorded.
	var deliverSpanFound bool
	for _, s := range sr.Ended() {
		if s.Name() == "wrkflw.runner.ApplyTrigger" {
			deliverSpanFound = true
		}
	}
	assert.True(t, deliverSpanFound, "wrkflw.runner.ApplyTrigger span must be recorded by Runner.ApplyTrigger")
}
