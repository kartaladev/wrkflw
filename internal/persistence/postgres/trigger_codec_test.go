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
// EXHAUSTIVENESS GUARD: every entry in this table MUST correspond to one of the
// 13 sealed variants declared in engine/trigger.go. When a new variant is added
// to the sealed set it MUST be added here too; the constant-count assertion below
// acts as an explicit reminder.
//
// Currently covered (13 variants):
//
//	StartInstance, ActionCompleted, ActionFailed,
//	HumanCompleted, HumanClaimed, HumanReassigned,
//	TimerFired, SignalReceived, MessageReceived,
//	SubInstanceCompleted, SubInstanceFailed,
//	CompensateRequested, CancelRequested
const totalTriggerVariants = 13

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

	// Exhaustiveness guard: if this fails, a variant was added to the table above
	// without updating totalTriggerVariants, or vice-versa.
	require.Equal(t, totalTriggerVariants, len(tests),
		"test table must cover exactly %d sealed Trigger variants", totalTriggerVariants)

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			data, kind, err := pg.MarshalTrigger(tc.in)
			require.NoError(t, err)
			require.NotEmpty(t, kind)

			got, err := pg.UnmarshalTrigger(kind, data)
			require.NoError(t, err)
			require.True(t, tc.in.OccurredAt().Equal(got.OccurredAt()),
				"OccurredAt mismatch: want %v got %v", tc.in.OccurredAt(), got.OccurredAt())
			tc.assert(t, got)
		})
	}
}

func TestUnmarshalTriggerUnknownKind(t *testing.T) {
	_, err := pg.UnmarshalTrigger("does.not.exist", []byte(`{}`))
	require.Error(t, err)
}
