package runtime_test

// terminal_incident_events_test.go — ADR-0164 Decision 3, pinned from OUTSIDE
// the engine.
//
// endInstance's removeOrphanedIncidents sweep retires exactly the incidents
// whose token the terminal transition dropped, and keeps the rest. That is an
// engine-side state change, but its consumer-visible effect lands here: the
// terminal outbox payload's "error" comes from terminalEventErr
// (runtime/outbox.go), which prefers st.Incidents[0].Error over the terminal
// FailInstance's Err, and the listing's IncidentCount counts the same slice.
// runtime/outbox_test.go feeds terminalEventErr hand-built states, so it keeps
// passing whatever the engine actually produces — these two tests drive the
// real engine instead.
//
// The two are a PAIR and only mean something together, mirroring the engine's
// own incident pair: dropping the sweep entirely must fail the cancel test,
// while widening it to a wholesale `s.Incidents = nil` must fail the
// surviving-token test. Neither mutation can satisfy both.
//
// The surviving-token case also pins ADR-0164's cross-instance consequence:
// terminalErr (runtime/processdriver_action.go) shares terminalEventErr's
// Incidents[0]-first preference and feeds kernel.CallOutcome.Err, which a child
// hands its parent through SubInstanceFailed. Whatever the sweep leaves behind
// is what a parent instance reports as its own failure text.

import (
	"context"
	"errors"
	"testing"
	"time"

	clockwork "github.com/jonboulle/clockwork"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kartaladev/wrkflw/action"
	"github.com/kartaladev/wrkflw/authz"
	"github.com/kartaladev/wrkflw/definition/activity"
	"github.com/kartaladev/wrkflw/definition/event"
	"github.com/kartaladev/wrkflw/definition/flow"
	"github.com/kartaladev/wrkflw/definition/gateway"
	"github.com/kartaladev/wrkflw/definition/model"
	"github.com/kartaladev/wrkflw/engine"
	"github.com/kartaladev/wrkflw/humantask"
	"github.com/kartaladev/wrkflw/runtime"
	"github.com/kartaladev/wrkflw/runtime/internal/runtimetest"
	"github.com/kartaladev/wrkflw/runtime/kernel"
)

// terminalIncidentErrText is the concrete failure the parked action reports. It
// is deliberately unlike every generic fallback terminalEventErr can produce, so
// an assertion that it is ABSENT cannot pass by coincidence.
const terminalIncidentErrText = "downstream ledger refused the charge"

// failingIncidentCatalog returns a catalog whose "charge" action always fails
// with terminalIncidentErrText, and whose "hold" action never returns a value.
func failingIncidentCatalog() action.Catalog {
	return action.NewCatalog(map[string]action.Action{
		"charge": action.ActionFunc(func(_ context.Context, _ map[string]any) (map[string]any, error) {
			return nil, errors.New(terminalIncidentErrText)
		}),
	})
}

// singleAttemptRetry is the default policy that turns the first action failure
// into a parked incident (MaxAttempts=1: exhausted immediately, no retry timer).
func singleAttemptRetry() model.RetryPolicy {
	return model.RetryPolicy{
		MaxAttempts:     1,
		InitialInterval: time.Second,
		BackoffCoef:     1,
		MaxInterval:     time.Minute,
	}
}

// terminalPayloadError returns the "error" string of the single outbox event
// carrying topic, failing the test unless exactly one such event exists.
func terminalPayloadError(t *testing.T, evs []kernel.OutboxEvent, topic string) string {
	t.Helper()

	var found []kernel.OutboxEvent
	for _, e := range evs {
		if e.Topic == topic {
			found = append(found, e)
		}
	}
	require.Len(t, found, 1, "expected exactly one %q event in %v", topic, topicsOf(evs))
	payload, ok := found[0].Payload["error"].(string)
	require.True(t, ok, "%q payload must carry a string \"error\" field, got %#v", topic, found[0].Payload)
	return payload
}

// TestCancelOfIncidentInstanceReportsCancelledNotIncident pins the DROPPED-token
// half of ADR-0164's incident sweep, from the consumer's side.
//
// handleCancelRequested's immediate branch nils s.Tokens, so the parked
// incident's token is gone and endInstance retires the incident with it. The
// terminal payload therefore falls through terminalEventErr's Incidents[0]
// preference to the FailInstance Err ("cancelled"), and the listing reports
// IncidentCount==0. This is a deliberate, ADR-documented diagnostic loss — the
// concrete "downstream ledger refused the charge" no longer reaches the event —
// and it had no test outside engine/ before this pin.
func TestCancelOfIncidentInstanceReportsCancelledNotIncident(t *testing.T) {
	clk := clockwork.NewFakeClockAt(time.Date(2026, 8, 4, 9, 0, 0, 0, time.UTC))
	store := runtimetest.MustMemStore(t)
	driver := runtimetest.MustProcessDriver(t, failingIncidentCatalog(), store,
		runtime.WithClock(clk),
		runtime.WithDefaultRetryPolicy(singleAttemptRetry()),
	)

	def := &model.ProcessDefinition{
		ID: "cancel-incident", Version: 1,
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

	parked, err := driver.Drive(t.Context(), def, "ci-1", nil)
	require.NoError(t, err)
	require.Equal(t, engine.StatusRunning, parked.Status,
		"control: the instance must park running, not fail, under the raiseIncident policy")
	require.Len(t, parked.Incidents, 1,
		"control: the action failure must have parked ONE incident")
	require.Equal(t, terminalIncidentErrText, parked.Incidents[0].Error,
		"control: the parked incident must carry the concrete failure text")

	final, err := driver.CancelInstance(t.Context(), def, "ci-1")
	require.NoError(t, err)
	require.Equal(t, engine.StatusTerminated, final.Status)

	assert.Empty(t, final.Incidents,
		"ADR-0164: cancel drops every token, so the incident it carried is retired with it")
	assert.Equal(t, "cancelled", terminalPayloadError(t, store.Events(), "instance.terminated"),
		"with the incident swept, terminalEventErr falls through to the FailInstance Err")

	page, err := store.List(t.Context(), kernel.InstanceFilter{})
	require.NoError(t, err)
	require.Len(t, page.Items, 1)
	assert.Equal(t, 0, page.Items[0].IncidentCount,
		"a listing filter keyed on incident_count no longer matches a cancelled instance")
}

// TestUnhandledErrorKeepsIncidentOfSurvivingToken pins the SURVIVING-token half:
// the sweep must be narrow, not a wholesale clear.
//
// handleUnhandledError's immediate-fail branch sets StatusFailed WITHOUT
// dropping s.Tokens, so the parked incident's token survives the transition and
// its incident must survive with it. The terminal payload therefore still
// reports the concrete failure rather than the generic error code — which is
// also what a parent instance would receive, since terminalErr feeds
// CallOutcome.Err → SubInstanceFailed → the parent's own FailInstance.Err.
//
// Branch order is load-bearing: "charge" is wired FIRST so its incident is
// parked before the sibling branch is ever released toward the error end.
func TestUnhandledErrorKeepsIncidentOfSurvivingToken(t *testing.T) {
	clk := clockwork.NewFakeClockAt(time.Date(2026, 8, 4, 9, 0, 0, 0, time.UTC))
	store := runtimetest.MustMemStore(t)
	driver := runtimetest.MustProcessDriver(t, failingIncidentCatalog(), store,
		runtime.WithClock(clk),
		runtime.WithDefaultRetryPolicy(singleAttemptRetry()),
		runtime.WithHumanTasks(humantask.NewStaticActorResolver(nil), humantask.NewMemTaskStore(), authz.RoleAuthorizer{}),
	)

	// start → fork ⇒ { charge(ServiceTask, fails → incident) ;
	//                  gate(UserTask) → boom(error end, no boundary) }
	def := &model.ProcessDefinition{
		ID: "fail-keeps-incident", Version: 1,
		Nodes: []model.Node{
			event.NewStart("start"),
			gateway.NewParallel("fork"),
			activity.NewServiceTask("charge", activity.WithTaskAction("charge")),
			event.NewEnd("charge-end"),
			activity.NewUserTask("gate", activity.WithEligibleRoles("ops")),
			event.NewEnd("boom", event.WithErrorCode("E-FATAL")),
		},
		Flows: []flow.SequenceFlow{
			{ID: "f-start", Source: "start", Target: "fork"},
			{ID: "f-charge", Source: "fork", Target: "charge"},
			{ID: "f-charge-end", Source: "charge", Target: "charge-end"},
			{ID: "f-gate", Source: "fork", Target: "gate"},
			{ID: "f-boom", Source: "gate", Target: "boom"},
		},
	}

	parked, err := driver.Drive(t.Context(), def, "fki-1", nil)
	require.NoError(t, err)
	require.Equal(t, engine.StatusRunning, parked.Status)
	require.Len(t, parked.Incidents, 1,
		"control: the charge branch must have parked ONE incident")
	require.Len(t, parked.Tasks, 1,
		"control: the sibling branch must be parked on its human task")

	// Release the sibling branch into the error end event. No boundary matches
	// E-FATAL and there are no compensation records, so the instance fails
	// immediately — keeping every token, including the incident's.
	final, err := driver.ApplyTrigger(t.Context(), def, "fki-1",
		engine.NewHumanCompleted(clk.Now().Add(time.Minute), parked.Tasks[0].TaskID, engine.CompletionInput{}, authz.Actor{ID: "ops-1"}))
	require.NoError(t, err)
	require.Equal(t, engine.StatusFailed, final.Status,
		"control: the unhandled error must have failed the instance")

	var survivor bool
	for _, tok := range final.Tokens {
		if tok.ID == final.Incidents[0].TokenID {
			survivor = true
		}
	}
	require.True(t, survivor,
		"control: the incident's own token must survive this terminal transition — "+
			"that survival is the entire premise of the narrow sweep")

	require.Len(t, final.Incidents, 1,
		"ADR-0164: the sweep is narrow — an incident whose token survives must be KEPT, "+
			"not cleared wholesale")
	assert.Equal(t, terminalIncidentErrText,
		terminalPayloadError(t, store.Events(), "instance.failed"),
		"a retained incident is what terminalEventErr (and terminalErr, which feeds "+
			"CallOutcome.Err → the parent's SubInstanceFailed) reports as the concrete failure")

	page, err := store.List(t.Context(), kernel.InstanceFilter{})
	require.NoError(t, err)
	require.Len(t, page.Items, 1)
	assert.Equal(t, 1, page.Items[0].IncidentCount,
		"the listing must still surface the retained incident")
}
