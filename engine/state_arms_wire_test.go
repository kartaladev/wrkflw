package engine

// state_arms_wire_test.go — wire-format parity gate for the shared
// triggerMatch embed.
//
// InstanceState (including ArmedEvents, Boundaries, and
// EventTriggeredSubprocesses) is persisted via plain json.Marshal
// (internal/persistence/store/store_core.go), with no custom
// MarshalJSON/UnmarshalJSON on the arm types. The refactor extracts a shared
// `triggerMatch{TimerID, Signal, Message, MessageKey}` struct and embeds it
// ANONYMOUSLY into armedEvent, boundaryArm, and eventTriggeredSubprocessArm.
// Go promotes an anonymous embedded struct's fields into the parent JSON
// object, so this is expected to be wire-safe with no migration.
//
// The golden fixtures below were captured by marshaling a fully-populated
// value of each arm type under the PRE-embed code (each field declared
// directly on the struct). This test must pass both BEFORE and AFTER the
// embed — that is the parity proof.
//
// ⚠ goldenBoundaryArmJSON was subsequently rewritten, deliberately, when #212
// removed boundaryArm.Flow. That is a real change to the persisted shape, not
// a re-capture of the same one, so the pre-#212 field set is kept verbatim as
// legacyBoundaryArmJSON and pinned in the DECODE direction by
// TestBoundaryArmWireDecodesPre212Snapshots. Nothing else about the parity
// proof moves: every other key, and both other arm types, are untouched.
//
// The removal is asymmetric, and this is a measured fact about behaviour rather
// than a statement about what is supported:
//
//   - A post-#212 build reading a pre-#212 arm DECODES it (the surplus "Flow"
//     key is discarded) and ROUTES it — the flow is re-derived from
//     BoundaryNode, which every pre-#212 snapshot already carries. Both legs are
//     pinned: decode by TestBoundaryArmWireDecodesPre212Snapshots, routing by
//     TestBoundaryArmRoutesAfterASnapshotRoundTrip.
//   - A PRE-#212 build reading a POST-#212 arm decodes it too, but CANNOT route
//     it: the old fire path resolves ba.Flow, which is now absent, and returns
//     `boundary %q: outgoing flow "" not found`. It fails closed and loudly, it
//     affects in-flight boundary arms only, and it needs no pathological
//     definition. Recorded here so a mixed-version window is a known quantity;
//     what is or is not supported is not this file's to declare.
//
// White-box (package engine, not engine_test): the three arm types are
// unexported, mirroring the existing convention in state_esp_test.go and
// state_waiters_test.go.

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kartaladev/wrkflw/definition/activity"
	"github.com/kartaladev/wrkflw/definition/event"
	"github.com/kartaladev/wrkflw/definition/flow"
	"github.com/kartaladev/wrkflw/definition/gateway"
	"github.com/kartaladev/wrkflw/definition/model"
)

// goldenArmedEventJSON is the exact json.Marshal output of a fully-populated
// armedEvent under the pre-embed (non-embedded) struct shape.
const goldenArmedEventJSON = `{"GatewayToken":"gw-tok-1","CatchNode":"catch-1","Flow":"flow-1","TimerID":"timer-1","Signal":"sig-1","Message":"msg-1","MessageKey":"key-1"}`

// goldenBoundaryArmJSON is the exact json.Marshal output of a fully-populated
// boundaryArm under the pre-embed (non-embedded) struct shape, less the "Flow"
// key that #212 removed.
const goldenBoundaryArmJSON = `{"HostToken":"host-tok-1","HostNode":"host-node-1","BoundaryNode":"bnd-node-1","NonInterrupting":true,"TimerID":"timer-2","Signal":"sig-2","Message":"msg-2","MessageKey":"key-2","Action":"action-1"}`

// legacyBoundaryArmJSON is goldenBoundaryArmJSON as it stood before #212, i.e.
// the shape a boundary arm persisted by an older build actually has on disk.
// It differs in exactly one key: "Flow", the authored outgoing-flow ID that the
// fire path used to re-resolve.
const legacyBoundaryArmJSON = `{"HostToken":"host-tok-1","HostNode":"host-node-1","BoundaryNode":"bnd-node-1","Flow":"flow-2","NonInterrupting":true,"TimerID":"timer-2","Signal":"sig-2","Message":"msg-2","MessageKey":"key-2","Action":"action-1"}`

// goldenEventTriggeredSubprocessArmJSON is the exact json.Marshal output of a
// fully-populated eventTriggeredSubprocessArm under the pre-embed (non-embedded)
// struct shape.
const goldenEventTriggeredSubprocessArmJSON = `{"EnclosingScopeID":"scope-1","EventSubprocessNode":"esp-node-1","NonInterrupting":true,"Signal":"sig-3","TimerID":"timer-3","Message":"msg-3","MessageKey":"key-3"}`

// fullyPopulatedArmedEvent returns an armedEvent with every field set to a
// distinct non-zero value, matching the fixture that produced
// goldenArmedEventJSON.
func fullyPopulatedArmedEvent() armedEvent {
	return armedEvent{
		GatewayToken: "gw-tok-1",
		CatchNode:    "catch-1",
		Flow:         "flow-1",
		triggerMatch: triggerMatch{
			TimerID:    "timer-1",
			Signal:     "sig-1",
			Message:    "msg-1",
			MessageKey: "key-1",
		},
	}
}

// fullyPopulatedBoundaryArm returns a boundaryArm with every field set to a
// distinct non-zero value, matching the fixture that produced
// goldenBoundaryArmJSON.
func fullyPopulatedBoundaryArm() boundaryArm {
	return boundaryArm{
		HostToken:       "host-tok-1",
		HostNode:        "host-node-1",
		BoundaryNode:    "bnd-node-1",
		NonInterrupting: true,
		triggerMatch: triggerMatch{
			TimerID:    "timer-2",
			Signal:     "sig-2",
			Message:    "msg-2",
			MessageKey: "key-2",
		},
		Action: "action-1",
	}
}

// fullyPopulatedEventTriggeredSubprocessArm returns an
// eventTriggeredSubprocessArm with every field set to a distinct non-zero
// value, matching the fixture that produced
// goldenEventTriggeredSubprocessArmJSON.
func fullyPopulatedEventTriggeredSubprocessArm() eventTriggeredSubprocessArm {
	return eventTriggeredSubprocessArm{
		EnclosingScopeID:    "scope-1",
		EventSubprocessNode: "esp-node-1",
		NonInterrupting:     true,
		triggerMatch: triggerMatch{
			Signal:     "sig-3",
			TimerID:    "timer-3",
			Message:    "msg-3",
			MessageKey: "key-3",
		},
	}
}

// assertJSONFieldSetEqual asserts that two JSON object strings carry the same
// field set (name -> value), independent of field order. It unmarshals both
// into map[string]any and compares those.
func assertJSONFieldSetEqual(t *testing.T, wantJSON, gotJSON string) {
	t.Helper()

	var want, got map[string]any
	require.NoError(t, json.Unmarshal([]byte(wantJSON), &want))
	require.NoError(t, json.Unmarshal([]byte(gotJSON), &got))
	assert.Equal(t, want, got, "JSON field set changed: wire format is no longer identical")
}

func TestArmWireParity_ArmedEvent(t *testing.T) {
	t.Parallel()

	want := fullyPopulatedArmedEvent()

	// (a) unmarshal golden -> fully-populated value (including trigger fields,
	// promoted or not).
	var got armedEvent
	require.NoError(t, json.Unmarshal([]byte(goldenArmedEventJSON), &got))
	assert.Equal(t, want, got, "unmarshal of golden JSON did not reproduce the fully-populated armedEvent")

	// (b) marshal of a populated value has the same field set as the golden
	// fixture (order-insensitive).
	b, err := json.Marshal(want)
	require.NoError(t, err)
	assertJSONFieldSetEqual(t, goldenArmedEventJSON, string(b))
}

func TestArmWireParity_BoundaryArm(t *testing.T) {
	t.Parallel()

	want := fullyPopulatedBoundaryArm()

	var got boundaryArm
	require.NoError(t, json.Unmarshal([]byte(goldenBoundaryArmJSON), &got))
	assert.Equal(t, want, got, "unmarshal of golden JSON did not reproduce the fully-populated boundaryArm")

	b, err := json.Marshal(want)
	require.NoError(t, err)
	assertJSONFieldSetEqual(t, goldenBoundaryArmJSON, string(b))
}

func TestArmWireParity_EventTriggeredSubprocessArm(t *testing.T) {
	t.Parallel()

	want := fullyPopulatedEventTriggeredSubprocessArm()

	var got eventTriggeredSubprocessArm
	require.NoError(t, json.Unmarshal([]byte(goldenEventTriggeredSubprocessArmJSON), &got))
	assert.Equal(t, want, got, "unmarshal of golden JSON did not reproduce the fully-populated eventTriggeredSubprocessArm")

	b, err := json.Marshal(want)
	require.NoError(t, err)
	assertJSONFieldSetEqual(t, goldenEventTriggeredSubprocessArmJSON, string(b))
}

// TestBoundaryArmWireDecodesPre212Snapshots pins the rolling-upgrade direction
// of #212's field removal: a boundary arm persisted by a build that still wrote
// "Flow" decodes into the current struct, and decodes to exactly the same value
// as a snapshot written after the removal.
//
// It holds because Store.Load unmarshals the snapshot with a plain
// json.Unmarshal and never sets DisallowUnknownFields
// (internal/persistence/store/store_core.go), so a key with no corresponding
// field is discarded rather than rejected.
//
// This is the DECODE leg only. That the arm which comes back also ROUTES — with
// no backfill, because fireBoundaryArm re-derives the outgoing flow from
// BoundaryNode, which every pre-#212 snapshot already carries — is a separate
// claim and is exercised separately, by
// TestBoundaryArmRoutesAfterASnapshotRoundTrip. Asserting it here would be
// asserting it nowhere.
func TestBoundaryArmWireDecodesPre212Snapshots(t *testing.T) {
	t.Parallel()

	type testCase struct {
		name   string
		snap   string
		assert func(t *testing.T, arm boundaryArm, err error)
	}

	cases := []testCase{
		{
			name: "a snapshot written before the Flow field was removed",
			snap: legacyBoundaryArmJSON,
			assert: func(t *testing.T, arm boundaryArm, err error) {
				require.NoError(t, err, "an unknown key must be discarded, not rejected")
				assert.Equal(t, fullyPopulatedBoundaryArm(), arm,
					"the surplus Flow key must not change any field the engine still reads")
				assert.Equal(t, "bnd-node-1", arm.BoundaryNode,
					"the key the fire path resolves by must survive the upgrade")
			},
		},
		{
			name: "a snapshot written after the Flow field was removed",
			snap: goldenBoundaryArmJSON,
			assert: func(t *testing.T, arm boundaryArm, err error) {
				require.NoError(t, err)
				assert.Equal(t, fullyPopulatedBoundaryArm(), arm)
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			var arm boundaryArm
			err := json.Unmarshal([]byte(tc.snap), &arm)
			tc.assert(t, arm, err)
		})
	}
}

// wireRoundTripDef is the #212 fixture in miniature, inside package engine so
// the white-box wire tests can drive it: a decoy blank-ID flow declared FIRST,
// the boundary's own blank-ID outgoing flow declared last.
//
//	start → work (UserTask, interrupting message boundary "bnd") → endWork
//	        decoySource (UserTask) --""--> decoyTarget (action "decoy-action")
//	        bnd --""--> realTarget (action "real-action")
func wireRoundTripDef() *model.ProcessDefinition {
	return &model.ProcessDefinition{
		ID: "p-wire", Version: 1,
		Nodes: []model.Node{
			event.NewStart("start"),
			gateway.NewParallel("fork"),
			activity.NewUserTask("work"),
			event.NewBoundary("bnd", "work", event.WithMessageCorrelator("cancel", "")),
			activity.NewUserTask("decoySource"),
			activity.NewServiceTask("decoyTarget", activity.WithTaskAction("decoy-action")),
			activity.NewServiceTask("realTarget", activity.WithTaskAction("real-action")),
			event.NewEnd("endWork"),
			event.NewEnd("endDecoy"),
			event.NewEnd("endReal"),
		},
		Flows: []flow.SequenceFlow{
			{Source: "decoySource", Target: "decoyTarget"},
			{ID: "f-start", Source: "start", Target: "fork"},
			{ID: "f-fork-work", Source: "fork", Target: "work"},
			{ID: "f-fork-decoy", Source: "fork", Target: "decoySource"},
			{ID: "f-work-end", Source: "work", Target: "endWork"},
			{ID: "f-decoy-end", Source: "decoyTarget", Target: "endDecoy"},
			{Source: "bnd", Target: "realTarget"},
			{ID: "f-real-end", Source: "realTarget", Target: "endReal"},
		},
	}
}

// TestBoundaryArmRoutesAfterASnapshotRoundTrip is the ROUTING leg of the
// upgrade claim, which TestBoundaryArmWireDecodesPre212Snapshots' decode leg
// cannot reach: an arm is armed, persisted, read back, and FIRED.
//
// The pre-#212 row injects "Flow" into the persisted arm exactly as an older
// build wrote it. The arm still routes to the boundary's own target, because
// the fire path re-derives the flow from BoundaryNode and never reads a
// persisted flow reference — so a pre-#212 snapshot is not merely accepted, its
// stale blank reference is ignored. Without this leg, an edit that reintroduces
// any read of a persisted flow reference would leave the decode test green.
func TestBoundaryArmRoutesAfterASnapshotRoundTrip(t *testing.T) {
	t.Parallel()

	type testCase struct {
		name string
		// mutate rewrites the persisted boundary arm to look like some other
		// build's output before it is read back.
		mutate func(t *testing.T, arm map[string]any)
	}

	cases := []testCase{
		{
			name: "a snapshot this build wrote",
			mutate: func(t *testing.T, arm map[string]any) {
				t.Helper()
				assert.NotContains(t, arm, "Flow",
					"instrument: this build must not persist a flow reference")
			},
		},
		{
			name: "a snapshot a pre-#212 build wrote, carrying a stale blank Flow",
			mutate: func(t *testing.T, arm map[string]any) {
				t.Helper()
				require.NotContains(t, arm, "Flow",
					"instrument: the key under test must be the one this row injects")
				arm["Flow"] = ""
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			ctx := t.Context()
			def := wireRoundTripDef()
			require.NoError(t, model.Validate(def), "precondition")

			at := time.Date(2026, 6, 25, 10, 0, 0, 0, time.UTC)
			r1, err := Step(ctx, def, InstanceState{InstanceID: "i1"},
				NewStartInstance(at, nil), StepOptions{})
			require.NoError(t, err)
			require.Len(t, r1.State.Boundaries, 1, "precondition: the boundary must be armed")

			// Persist, rewrite the arm, read back.
			raw, err := json.Marshal(r1.State)
			require.NoError(t, err)
			var snap map[string]any
			require.NoError(t, json.Unmarshal(raw, &snap))
			arms, ok := snap["Boundaries"].([]any)
			require.True(t, ok, "instrument: the snapshot must carry a Boundaries array")
			require.Len(t, arms, 1)
			arm, ok := arms[0].(map[string]any)
			require.True(t, ok, "instrument: the arm must decode as an object")
			tc.mutate(t, arm)

			rewritten, err := json.Marshal(snap)
			require.NoError(t, err)
			var restored InstanceState
			require.NoError(t, json.Unmarshal(rewritten, &restored))
			require.Len(t, restored.Boundaries, 1, "the arm must survive the round trip")

			r2, err := Step(ctx, def, restored,
				NewMessageReceived(at.Add(time.Minute), "cancel", "", nil), StepOptions{})
			require.NoError(t, err)

			var actions []string
			for _, c := range r2.Commands {
				if ia, ok := c.(InvokeAction); ok {
					actions = append(actions, ia.Name)
				}
			}
			assert.Contains(t, actions, "real-action", "the restored arm must route to its own target")
			assert.NotContains(t, actions, "decoy-action", "a stale flow reference must not divert it")
		})
	}
}
