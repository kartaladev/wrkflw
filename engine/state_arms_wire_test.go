package engine

// state_arms_wire_test.go — wire-format parity gate for Task B1
// (docs/plans/2026-07-13-engine-simplify-phase-b.md, ADR-0131).
//
// InstanceState (including ArmedEvents, Boundaries, and
// EventTriggeredSubprocesses) is persisted via plain json.Marshal
// (internal/persistence/store/store_core.go), with no custom
// MarshalJSON/UnmarshalJSON on the arm types. B1 extracts a shared
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
// White-box (package engine, not engine_test): the three arm types are
// unexported, mirroring the existing convention in state_esp_test.go and
// state_waiters_test.go.

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// goldenArmedEventJSON is the exact json.Marshal output of a fully-populated
// armedEvent under the pre-B1 (non-embedded) struct shape.
const goldenArmedEventJSON = `{"GatewayToken":"gw-tok-1","CatchNode":"catch-1","Flow":"flow-1","TimerID":"timer-1","Signal":"sig-1","Message":"msg-1","MessageKey":"key-1"}`

// goldenBoundaryArmJSON is the exact json.Marshal output of a fully-populated
// boundaryArm under the pre-B1 (non-embedded) struct shape.
const goldenBoundaryArmJSON = `{"HostToken":"host-tok-1","HostNode":"host-node-1","BoundaryNode":"bnd-node-1","Flow":"flow-2","NonInterrupting":true,"TimerID":"timer-2","Signal":"sig-2","Message":"msg-2","MessageKey":"key-2","Action":"action-1"}`

// goldenEventTriggeredSubprocessArmJSON is the exact json.Marshal output of a
// fully-populated eventTriggeredSubprocessArm under the pre-B1 (non-embedded)
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
		Flow:            "flow-2",
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
