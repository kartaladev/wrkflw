package store_test

import (
	"testing"
	"time"

	"github.com/kartaladev/wrkflw/authz"
	"github.com/kartaladev/wrkflw/engine"
	st "github.com/kartaladev/wrkflw/internal/persistence/store"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestTriggerCodecRoundTrip asserts that every sealed engine.Trigger variant
// survives a MarshalTrigger→UnmarshalTrigger round-trip losslessly.
//
// EXHAUSTIVENESS GUARD: the test collects the set of kind strings emitted by
// MarshalTrigger for each table case and cross-checks it against st.AllTriggerKinds.
func TestTriggerCodecRoundTrip(t *testing.T) {
	at := time.Unix(1700000000, 0).UTC()
	actor := authz.Actor{ID: "u1", Roles: []string{"r"}, Attributes: map[string]any{"k": "v"}}
	payload := map[string]any{"k": "v"}

	tests := map[string]struct {
		in     engine.Trigger
		assert func(t *testing.T, got engine.Trigger)
	}{
		"StartInstance": {
			in: engine.NewStartInstance(at, payload),
			assert: func(t *testing.T, got engine.Trigger) {
				require.IsType(t, engine.StartInstance{}, got)
				require.Equal(t, payload, got.(engine.StartInstance).Vars)
			},
		},
		"ActionCompleted": {
			in: engine.NewActionCompleted(at, "c1", payload),
			assert: func(t *testing.T, got engine.Trigger) {
				require.IsType(t, engine.ActionCompleted{}, got)
				require.Equal(t, "c1", got.(engine.ActionCompleted).CommandID)
				require.Equal(t, payload, got.(engine.ActionCompleted).Output)
			},
		},
		"ActionFailed": {
			in: engine.NewActionFailed(at, "c1", "boom", true),
			assert: func(t *testing.T, got engine.Trigger) {
				require.Equal(t, "boom", got.(engine.ActionFailed).Err)
				require.Equal(t, "c1", got.(engine.ActionFailed).CommandID)
				require.True(t, got.(engine.ActionFailed).Retryable)
				require.Equal(t, 0.0, got.(engine.ActionFailed).JitterFraction, "NewActionFailed must produce zero jitter")
			},
		},
		"ResolveIncident": {
			in: engine.NewResolveIncident(at, "p-inc0", 3),
			assert: func(t *testing.T, got engine.Trigger) {
				require.IsType(t, engine.ResolveIncident{}, got)
				require.Equal(t, "p-inc0", got.(engine.ResolveIncident).IncidentID)
				require.Equal(t, 3, got.(engine.ResolveIncident).AddAttempts)
			},
		},
		"HumanCompleted": {
			in: engine.NewHumanCompleted(at, "t1", payload, actor),
			assert: func(t *testing.T, got engine.Trigger) {
				require.Equal(t, actor, got.(engine.HumanCompleted).Actor)
				require.Equal(t, "t1", got.(engine.HumanCompleted).TaskToken)
				require.Equal(t, payload, got.(engine.HumanCompleted).Output)
			},
		},
		"HumanClaimed": {
			in: engine.NewHumanClaimed(at, "t1", actor),
			assert: func(t *testing.T, got engine.Trigger) {
				require.Equal(t, actor, got.(engine.HumanClaimed).Actor)
				require.Equal(t, "t1", got.(engine.HumanClaimed).TaskToken)
			},
		},
		"HumanReassigned": {
			in: engine.NewHumanReassigned(at, "t1", "a", "b", actor),
			assert: func(t *testing.T, got engine.Trigger) {
				require.Equal(t, "b", got.(engine.HumanReassigned).To)
				require.Equal(t, "a", got.(engine.HumanReassigned).From)
				require.Equal(t, actor, got.(engine.HumanReassigned).By)
			},
		},
		"TimerFired": {
			in: engine.NewTimerFired(at, "tm1"),
			assert: func(t *testing.T, got engine.Trigger) {
				require.Equal(t, "tm1", got.(engine.TimerFired).TimerID)
			},
		},
		"SignalReceived": {
			in: engine.NewSignalReceived(at, "sig", payload),
			assert: func(t *testing.T, got engine.Trigger) {
				require.Equal(t, "sig", got.(engine.SignalReceived).Name)
				require.Equal(t, payload, got.(engine.SignalReceived).Payload)
			},
		},
		"MessageReceived": {
			in: engine.NewMessageReceived(at, "msg", "key", payload),
			assert: func(t *testing.T, got engine.Trigger) {
				require.Equal(t, "key", got.(engine.MessageReceived).CorrelationKey)
				require.Equal(t, "msg", got.(engine.MessageReceived).Name)
				require.Equal(t, payload, got.(engine.MessageReceived).Payload)
			},
		},
		"SubInstanceCompleted": {
			in: engine.NewSubInstanceCompleted(at, "c1", payload),
			assert: func(t *testing.T, got engine.Trigger) {
				require.IsType(t, engine.SubInstanceCompleted{}, got)
				require.Equal(t, "c1", got.(engine.SubInstanceCompleted).CommandID)
				require.Equal(t, payload, got.(engine.SubInstanceCompleted).Output)
			},
		},
		"SubInstanceFailed": {
			in: engine.NewSubInstanceFailed(at, "c1", "err"),
			assert: func(t *testing.T, got engine.Trigger) {
				require.Equal(t, "err", got.(engine.SubInstanceFailed).Err)
				require.Equal(t, "c1", got.(engine.SubInstanceFailed).CommandID)
			},
		},
		"CompensateRequested": {
			in: engine.NewCompensateRequested(at, "n1"),
			assert: func(t *testing.T, got engine.Trigger) {
				require.Equal(t, "n1", got.(engine.CompensateRequested).ToNode)
			},
		},
		"CancelRequested": {
			in: engine.NewCancelRequested(at),
			assert: func(t *testing.T, got engine.Trigger) {
				require.IsType(t, engine.CancelRequested{}, got)
			},
		},
	}

	var gotKinds []string
	for name, tc := range tests {
		data, kind, err := st.MarshalTrigger(tc.in)
		require.NoError(t, err, "MarshalTrigger failed for %q", name)
		require.NotEmpty(t, kind, "MarshalTrigger returned empty kind for %q", name)
		gotKinds = append(gotKinds, kind)

		got, err := st.UnmarshalTrigger(kind, data)
		require.NoError(t, err, "UnmarshalTrigger failed for %q", name)
		require.True(t, tc.in.OccurredAt().Equal(got.OccurredAt()),
			"OccurredAt mismatch: want %v got %v", tc.in.OccurredAt(), got.OccurredAt())
		tc.assert(t, got)
	}

	require.ElementsMatch(t, st.AllTriggerKinds, gotKinds,
		"test table kinds do not match st.AllTriggerKinds: declared=%v, got=%v",
		st.AllTriggerKinds, gotKinds)
}

// TestActionFailedJitterRoundTrip asserts that JitterFraction survives a
// MarshalTrigger→UnmarshalTrigger round-trip.
func TestActionFailedJitterRoundTrip(t *testing.T) {
	at := time.Unix(1700000000, 0).UTC()
	in := engine.NewActionFailed(at, "c-jit", "boom-jit", true, engine.WithJitter(0.375))

	data, kind, err := st.MarshalTrigger(in)
	require.NoError(t, err)
	require.Equal(t, "action_failed", kind)

	got, err := st.UnmarshalTrigger(kind, data)
	require.NoError(t, err)

	af, ok := got.(engine.ActionFailed)
	require.True(t, ok)
	require.Equal(t, "c-jit", af.CommandID)
	require.Equal(t, "boom-jit", af.Err)
	require.True(t, af.Retryable)
	require.Equal(t, 0.375, af.JitterFraction, "JitterFraction must survive round-trip")
	require.True(t, in.OccurredAt().Equal(got.OccurredAt()))
}

func TestActionFailedNotRetryable(t *testing.T) {
	at := time.Unix(1700000000, 0).UTC()
	in := engine.NewActionFailed(at, "cmd-fatal", "unrecoverable error", false)

	data, kind, err := st.MarshalTrigger(in)
	require.NoError(t, err)
	require.Equal(t, "action_failed", kind)

	got, err := st.UnmarshalTrigger(kind, data)
	require.NoError(t, err)

	af := got.(engine.ActionFailed)
	require.Equal(t, "cmd-fatal", af.CommandID)
	require.Equal(t, "unrecoverable error", af.Err)
	require.False(t, af.Retryable, "Retryable must be false for non-retryable action failures")
}

func TestUnmarshalTriggerUnknownKind(t *testing.T) {
	_, err := st.UnmarshalTrigger("does.not.exist", []byte(`{}`))
	require.Error(t, err)
}

// TestActionFailedJitterBackwardCompat verifies that an old journal row written
// before JitterFraction existed (no "jitter" key in JSON) unmarshals cleanly
// with JitterFraction==0.
func TestActionFailedJitterBackwardCompat(t *testing.T) {
	oldPayload := []byte(`{"at":"2024-01-01T00:00:00Z","command_id":"old-cmd","err":"old error","retryable":true}`)
	got, err := st.UnmarshalTrigger("action_failed", oldPayload)
	require.NoError(t, err)
	af, ok := got.(engine.ActionFailed)
	require.True(t, ok)
	require.Equal(t, "old-cmd", af.CommandID)
	require.Equal(t, "old error", af.Err)
	require.True(t, af.Retryable)
	require.Equal(t, 0.0, af.JitterFraction, "missing jitter key must default to 0 — no migration needed")
}

// TestCompensateRequestedReverseRoundTrip asserts that the ReverseNode/ResetVars/
// RestoreTargetVars fields added for ReverseInstance (ADR-0109/FU#1) survive a
// MarshalTrigger→UnmarshalTrigger round-trip, and that a plain ToNode-only
// trigger still round-trips with ReverseNode/ResetVars/RestoreTargetVars left at
// their zero values (back-compat with journal rows written before these fields
// existed).
func TestCompensateRequestedReverseRoundTrip(t *testing.T) {
	t.Parallel()

	at := time.Unix(1700000000, 0).UTC()

	type testCase struct {
		name   string
		in     engine.CompensateRequested
		assert func(t *testing.T, got engine.CompensateRequested)
	}

	cases := []testCase{
		{
			name: "plain ToNode leaves ReverseNode, ResetVars and RestoreTargetVars at zero value",
			in:   engine.NewCompensateRequested(at, "n1"),
			assert: func(t *testing.T, got engine.CompensateRequested) {
				assert.Equal(t, "n1", got.ToNode)
				assert.Empty(t, got.ReverseNode)
				assert.False(t, got.ResetVars)
				assert.False(t, got.RestoreTargetVars)
			},
		},
		{
			name: "ReverseToStart round-trips ReverseNode and ResetVars, leaves RestoreTargetVars false",
			in:   engine.NewReverseToStart(at, "start"),
			assert: func(t *testing.T, got engine.CompensateRequested) {
				assert.Empty(t, got.ToNode)
				assert.Equal(t, "start", got.ReverseNode)
				assert.True(t, got.ResetVars)
				assert.False(t, got.RestoreTargetVars)
			},
		},
		{
			name: "ReverseToNode round-trips ToNode and RestoreTargetVars, leaves ReverseNode/ResetVars false",
			in:   engine.NewReverseToNode(at, "X"),
			assert: func(t *testing.T, got engine.CompensateRequested) {
				assert.Equal(t, "X", got.ToNode)
				assert.Empty(t, got.ReverseNode)
				assert.False(t, got.ResetVars)
				assert.True(t, got.RestoreTargetVars)
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			data, kind, err := st.MarshalTrigger(tc.in)
			require.NoError(t, err)
			require.Equal(t, "compensate_requested", kind)

			got, err := st.UnmarshalTrigger(kind, data)
			require.NoError(t, err)

			cr, ok := got.(engine.CompensateRequested)
			require.True(t, ok)
			require.True(t, tc.in.OccurredAt().Equal(got.OccurredAt()))
			tc.assert(t, cr)
		})
	}
}
