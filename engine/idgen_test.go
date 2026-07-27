package engine_test

import (
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kartaladev/wrkflw/definition/activity"
	"github.com/kartaladev/wrkflw/definition/event"
	"github.com/kartaladev/wrkflw/definition/flow"
	"github.com/kartaladev/wrkflw/definition/model"
	"github.com/kartaladev/wrkflw/definition/schedule"
	"github.com/kartaladev/wrkflw/engine"
)

// seqIDGen is a deterministic stand-in for a real generator: it mints
// "gen-1", "gen-2", … so a test can assert an id came from the injected seam
// rather than the engine's built-in counter.
type seqIDGen struct{ n atomic.Int64 }

func (g *seqIDGen) NewID() (string, error) {
	return fmt.Sprintf("gen-%d", g.n.Add(1)), nil
}

// errIDGen always fails, standing in for a generator whose entropy source is
// exhausted (e.g. UUID v7).
type errIDGen struct{ err error }

func (g errIDGen) NewID() (string, error) { return "", g.err }

// idGenDef exercises several id kinds in one run: a sub-process SCOPE, an inner
// TOKEN, a user TASK with a deadline TIMER, and the outer tokens.
//
//	Start → sub[ inner-start → approve(deadline 3h) → inner-end ] → End
func idGenDef() *model.ProcessDefinition {
	inner := &model.ProcessDefinition{
		ID: "inner", Version: 1,
		Nodes: []model.Node{
			event.NewStart("inner-start"),
			activity.NewUserTask("approve",
				activity.WithWaitDeadline(schedule.AfterExpr(`"3h"`), "esc"),
			),
			event.NewEnd("inner-end"),
			event.NewEnd("esc-end"),
		},
		Flows: []flow.SequenceFlow{
			{ID: "if1", Source: "inner-start", Target: "approve"},
			{ID: "if2", Source: "approve", Target: "inner-end"},
			{ID: "esc", Source: "approve", Target: "esc-end"},
		},
	}
	return &model.ProcessDefinition{
		ID: "p-idgen", Version: 1,
		Nodes: []model.Node{
			event.NewStart("start"),
			activity.NewSubProcess("sub", inner),
			event.NewEnd("end"),
		},
		Flows: []flow.SequenceFlow{
			{ID: "f1", Source: "start", Target: "sub"},
			{ID: "f2", Source: "sub", Target: "end"},
		},
	}
}

// TestStepIDGen covers the injected id-generation seam (ADR-0149): with no
// generator the engine keeps its deterministic "<instance>-<prefix><n>"
// counter byte-for-byte, an injected generator supplies every id the engine
// stamps, and a failing generator surfaces as a step error rather than a panic
// or an instance carrying blank ids.
func TestStepIDGen(t *testing.T) {
	t.Parallel()

	at := time.Date(2026, 7, 27, 9, 0, 0, 0, time.UTC)

	type testCase struct {
		name   string
		opts   engine.StepOptions
		assert func(t *testing.T, res engine.StepResult, err error)
	}

	cases := []testCase{
		{
			name: "no generator keeps the deterministic counter",
			opts: engine.StepOptions{},
			assert: func(t *testing.T, res engine.StepResult, err error) {
				require.NoError(t, err)
				require.Len(t, res.State.Tokens, 1)
				assert.Equal(t, "i1-t2", res.State.Tokens[0].ID, "inner token keeps the -tN form")
				require.Len(t, res.State.Tasks, 1)
				assert.Equal(t, "i1-h1", res.State.Tasks[0].TaskID)
				require.Len(t, res.State.Timers, 1)
				assert.Equal(t, "i1-tm1", res.State.Timers[0].TimerID)
				require.Len(t, res.State.Scopes, 1)
				assert.Equal(t, "i1-s1", res.State.Scopes[0].ID)
			},
		},
		{
			name: "injected generator supplies every id",
			opts: engine.StepOptions{IDGenerator: &seqIDGen{}},
			assert: func(t *testing.T, res engine.StepResult, err error) {
				require.NoError(t, err)
				require.Len(t, res.State.Tokens, 1)
				require.Len(t, res.State.Tasks, 1)
				require.Len(t, res.State.Timers, 1)
				require.Len(t, res.State.Scopes, 1)

				ids := []string{
					res.State.Tokens[0].ID,
					res.State.Tasks[0].TaskID,
					res.State.Timers[0].TimerID,
					res.State.Scopes[0].ID,
				}
				seen := make(map[string]bool, len(ids))
				for _, id := range ids {
					assert.True(t, strings.HasPrefix(id, "gen-"), "id %q must come from the injected generator", id)
					assert.False(t, seen[id], "id %q was minted twice", id)
					seen[id] = true
				}
			},
		},
		{
			name: "generator failure surfaces as a step error",
			opts: engine.StepOptions{IDGenerator: errIDGen{err: errors.New("entropy exhausted")}},
			assert: func(t *testing.T, _ engine.StepResult, err error) {
				require.Error(t, err)
				assert.Contains(t, err.Error(), "entropy exhausted")
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			res, err := engine.Step(t.Context(), idGenDef(), engine.InstanceState{InstanceID: "i1"},
				engine.NewStartInstance(at, nil), tc.opts)
			tc.assert(t, res, err)
		})
	}
}
