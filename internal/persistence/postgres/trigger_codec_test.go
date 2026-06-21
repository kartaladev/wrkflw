package postgres_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/zakyalvan/krtlwrkflw/authz"
	"github.com/zakyalvan/krtlwrkflw/engine"
	pg "github.com/zakyalvan/krtlwrkflw/internal/persistence/postgres"
)

// TestTriggerCodecRoundTrip asserts that every sealed engine.Trigger variant
// survives a MarshalTrigger→UnmarshalTrigger round-trip losslessly.
//
// EXHAUSTIVENESS GUARD: the test collects the set of kind strings emitted by
// MarshalTrigger for each table case and cross-checks it against pg.AllTriggerKinds.
// This fails if:
//   - A kind constant exists with no corresponding table row, or
//   - A table row maps to a kind not in the canonical set.
// When a new variant is added to the sealed set, add a table case and the
// corresponding kind constant to trigger_codec.go; AllTriggerKinds will then
// include it automatically.
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

	// Collect the set of kinds emitted by MarshalTrigger for each table case.
	// This exhaustiveness guard fails if:
	//   - A kind from pg.AllTriggerKinds is not covered by the table, or
	//   - A table case emits a kind not in pg.AllTriggerKinds.
	var gotKinds []string
	for name, tc := range tests {
		data, kind, err := pg.MarshalTrigger(tc.in)
		require.NoError(t, err, "MarshalTrigger failed for %q", name)
		require.NotEmpty(t, kind, "MarshalTrigger returned empty kind for %q", name)
		gotKinds = append(gotKinds, kind)

		got, err := pg.UnmarshalTrigger(kind, data)
		require.NoError(t, err, "UnmarshalTrigger failed for %q", name)
		require.True(t, tc.in.OccurredAt().Equal(got.OccurredAt()),
			"OccurredAt mismatch: want %v got %v", tc.in.OccurredAt(), got.OccurredAt())
		tc.assert(t, got)
	}

	// Exhaustiveness cross-check: every declared kind must be tested, and no
	// table case may emit an unknown kind.
	require.ElementsMatch(t, pg.AllTriggerKinds, gotKinds,
		"test table kinds do not match pg.AllTriggerKinds: declared=%v, got=%v",
		pg.AllTriggerKinds, gotKinds)
}

func TestActionFailedNotRetryable(t *testing.T) {
	at := time.Unix(1700000000, 0).UTC()
	in := engine.NewActionFailed(at, "cmd-fatal", "unrecoverable error", false)

	data, kind, err := pg.MarshalTrigger(in)
	require.NoError(t, err)
	require.Equal(t, "action_failed", kind)

	got, err := pg.UnmarshalTrigger(kind, data)
	require.NoError(t, err)

	af := got.(engine.ActionFailed)
	require.Equal(t, "cmd-fatal", af.CommandID)
	require.Equal(t, "unrecoverable error", af.Err)
	require.False(t, af.Retryable, "Retryable must be false for non-retryable action failures")
}

func TestUnmarshalTriggerUnknownKind(t *testing.T) {
	_, err := pg.UnmarshalTrigger("does.not.exist", []byte(`{}`))
	require.Error(t, err)
}
