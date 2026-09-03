package engine_test

// step_start_duplicate_test.go — a second StartInstance on an
// already-started instance is refused instead of superimposing a second start.
//
// Measured on `main` before the guard: Step(StartInstance) on a live instance
// returned err=<nil> and took tokens 1 → 2, tasks 1 → 2, overwriting
// StartVariables.

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kartaladev/wrkflw/definition/activity"
	"github.com/kartaladev/wrkflw/definition/event"
	"github.com/kartaladev/wrkflw/definition/flow"
	"github.com/kartaladev/wrkflw/definition/model"
	"github.com/kartaladev/wrkflw/engine"
)

// startGuardDef parks at a user task so a started instance stays alive with one
// token — the state a second StartInstance would superimpose onto.
//
//	start → approve (KindUserTask) → end
func startGuardDef() *model.ProcessDefinition {
	return &model.ProcessDefinition{
		ID: "start-guard-def", Version: 1,
		Nodes: []model.Node{
			event.NewStart("start"),
			activity.NewUserTask("approve"),
			event.NewEnd("end"),
		},
		Flows: []flow.SequenceFlow{
			{ID: "f1", Source: "start", Target: "approve"},
			{ID: "f2", Source: "approve", Target: "end"},
		},
	}
}

// TestSecondStartInstanceIsRefused pins the refusal across three rows. Rows (a)
// and (b) pass under a StartedAt-only predicate; row (c) is the one that fails
// it, because engine.Step is public API and the caller supplies the trigger's
// OccurredAt — a zero one leaves StartedAt.IsZero().
func TestSecondStartInstanceIsRefused(t *testing.T) {
	t.Parallel()

	at := time.Date(2026, 8, 13, 10, 0, 0, 0, time.UTC)

	type testCase struct {
		name   string
		seed   func(t *testing.T, def *model.ProcessDefinition) engine.InstanceState
		at     time.Time
		assert func(t *testing.T, res engine.StepResult, err error)
	}

	// startOnce runs one StartInstance from a pristine state and returns the result.
	startOnce := func(t *testing.T, def *model.ProcessDefinition, startAt time.Time) engine.InstanceState {
		t.Helper()
		res, err := engine.Step(t.Context(), def, engine.InstanceState{InstanceID: "sg1"},
			engine.NewStartInstance(startAt, map[string]any{"v": 1}), engine.StepOptions{})
		require.NoError(t, err, "the FIRST start must always succeed")
		require.Len(t, res.State.Tokens, 1, "first start must place exactly one token")
		return res.State
	}

	cases := []testCase{
		{
			name: "a second start on a live instance is refused",
			seed: func(t *testing.T, def *model.ProcessDefinition) engine.InstanceState {
				return startOnce(t, def, at)
			},
			at: at.Add(time.Hour),
			assert: func(t *testing.T, res engine.StepResult, err error) {
				require.ErrorIs(t, err, engine.ErrInstanceAlreadyStarted,
					"a second start must be refused with ErrInstanceAlreadyStarted")
				assert.ErrorIs(t, err, engine.ErrInvalidTransition,
					"the sentinel must classify as a wrong-state transition")
				assert.Empty(t, res.Commands, "a refused start must emit no commands")
			},
		},
		{
			name: "CONTROL: a genuinely fresh instance still starts",
			seed: func(_ *testing.T, _ *model.ProcessDefinition) engine.InstanceState {
				return engine.InstanceState{InstanceID: "sg-fresh"}
			},
			at: at,
			assert: func(t *testing.T, res engine.StepResult, err error) {
				require.NoError(t, err, "a never-started instance must still start")
				assert.Len(t, res.State.Tokens, 1, "the start must place its token")
				assert.Equal(t, engine.StatusRunning, res.State.Status)
			},
		},
		{
			name: "a second start after a ZERO-OccurredAt start is refused",
			seed: func(t *testing.T, def *model.ProcessDefinition) engine.InstanceState {
				st := startOnce(t, def, time.Time{})
				require.True(t, st.StartedAt.IsZero(),
					"the fixture must leave StartedAt zero, or the row proves nothing")
				return st
			},
			at: time.Time{},
			assert: func(t *testing.T, res engine.StepResult, err error) {
				require.ErrorIs(t, err, engine.ErrInstanceAlreadyStarted,
					"a zero OccurredAt must not defeat the guard")
				assert.Empty(t, res.Commands, "a refused start must emit no commands")
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			def := startGuardDef()
			seeded := tc.seed(t, def)

			res, err := engine.Step(t.Context(), def, seeded,
				engine.NewStartInstance(tc.at, map[string]any{"restart": true}), engine.StepOptions{})
			tc.assert(t, res, err)
		})
	}
}
