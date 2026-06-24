package runtime_test

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zakyalvan/krtlwrkflw/engine"
	"github.com/zakyalvan/krtlwrkflw/model"
	"github.com/zakyalvan/krtlwrkflw/runtime"
)

// startCall records one InstanceStarter.Run invocation.
type startCall struct {
	def  *model.ProcessDefinition
	id   string
	vars map[string]any
}

// recordingStarter is an ad-hoc InstanceStarter double (single test file; the
// repo convention is real fakes + ad-hoc doubles, not generated mocks).
type recordingStarter struct {
	calls []startCall
	err   error
	state engine.InstanceState
}

func (s *recordingStarter) Run(_ context.Context, def *model.ProcessDefinition, id string, vars map[string]any) (engine.InstanceState, error) {
	s.calls = append(s.calls, startCall{def: def, id: id, vars: vars})
	return s.state, s.err
}

func fulfillmentDef() *model.ProcessDefinition {
	return &model.ProcessDefinition{ID: "fulfillment", Version: 1}
}

func TestChainerHandle(t *testing.T) {
	errTransient := errors.New("db down")

	completedEv := runtime.ChainEvent{
		PredecessorID:  "p1",
		PredecessorDef: "approval:1",
		Outcome:        runtime.OutcomeCompleted,
		Result:         map[string]any{"orderID": "o-7"},
	}

	// successorFor returns a policy that always starts fulfillmentDef with the
	// event's Result as the successor vars.
	successorFor := func() runtime.SuccessorPolicy {
		return func(_ context.Context, ev runtime.ChainEvent) (runtime.SuccessorDecision, bool) {
			return runtime.SuccessorDecision{Def: fulfillmentDef(), Vars: ev.Result}, true
		}
	}

	tests := map[string]struct {
		ev          runtime.ChainEvent
		policy      runtime.SuccessorPolicy
		prepopulate *runtime.ChainLink
		noLinks     bool
		starterErr  error
		assert      func(t *testing.T, gotErr error, starter *recordingStarter, links *runtime.MemChainLinkStore)
	}{
		"no successor (ok=false) does nothing": {
			ev:     completedEv,
			policy: func(context.Context, runtime.ChainEvent) (runtime.SuccessorDecision, bool) { return runtime.SuccessorDecision{}, false },
			assert: func(t *testing.T, gotErr error, starter *recordingStarter, links *runtime.MemChainLinkStore) {
				require.NoError(t, gotErr)
				assert.Empty(t, starter.calls, "no policy successor => no start")
				hops, _ := links.ListByPredecessor(t.Context(), "p1")
				assert.Empty(t, hops, "no link recorded")
			},
		},
		"no successor (nil Def) does nothing": {
			ev:     completedEv,
			policy: func(context.Context, runtime.ChainEvent) (runtime.SuccessorDecision, bool) { return runtime.SuccessorDecision{Def: nil}, true },
			assert: func(t *testing.T, gotErr error, starter *recordingStarter, links *runtime.MemChainLinkStore) {
				require.NoError(t, gotErr)
				assert.Empty(t, starter.calls)
			},
		},
		"happy path records link and starts successor with deterministic id": {
			ev:     completedEv,
			policy: successorFor(),
			assert: func(t *testing.T, gotErr error, starter *recordingStarter, links *runtime.MemChainLinkStore) {
				require.NoError(t, gotErr)
				require.Len(t, starter.calls, 1)
				assert.Equal(t, "p1-next-completed", starter.calls[0].id)
				assert.Equal(t, "fulfillment", starter.calls[0].def.ID)
				assert.Equal(t, map[string]any{"orderID": "o-7"}, starter.calls[0].vars)

				got, ok, _ := links.LookupBySuccessor(t.Context(), "p1-next-completed")
				require.True(t, ok)
				assert.Equal(t, "p1", got.PredecessorID)
				assert.Equal(t, runtime.OutcomeCompleted, got.Outcome)
			},
		},
		"already-recorded link skips the start (exactly-once backstop)": {
			ev:          completedEv,
			policy:      successorFor(),
			prepopulate: &runtime.ChainLink{PredecessorID: "p1", Outcome: runtime.OutcomeCompleted, SuccessorID: "p1-next-completed"},
			assert: func(t *testing.T, gotErr error, starter *recordingStarter, links *runtime.MemChainLinkStore) {
				require.NoError(t, gotErr, "duplicate hop is a clean no-op")
				assert.Empty(t, starter.calls, "must not start when the link already exists")
			},
		},
		"duplicate instance start is treated as no-op": {
			ev:         completedEv,
			policy:     successorFor(),
			starterErr: runtime.ErrInstanceExists,
			assert: func(t *testing.T, gotErr error, starter *recordingStarter, links *runtime.MemChainLinkStore) {
				require.NoError(t, gotErr, "ErrInstanceExists from the starter is a clean no-op")
				require.Len(t, starter.calls, 1)
			},
		},
		"transient start error propagates for redelivery": {
			ev:         completedEv,
			policy:     successorFor(),
			starterErr: errTransient,
			assert: func(t *testing.T, gotErr error, starter *recordingStarter, links *runtime.MemChainLinkStore) {
				require.Error(t, gotErr)
				assert.ErrorIs(t, gotErr, errTransient, "transient error must remain inspectable so the broker re-delivers")
			},
		},
		"no link store still starts (deterministic id only)": {
			ev:      completedEv,
			policy:  successorFor(),
			noLinks: true,
			assert: func(t *testing.T, gotErr error, starter *recordingStarter, _ *runtime.MemChainLinkStore) {
				require.NoError(t, gotErr)
				require.Len(t, starter.calls, 1)
				assert.Equal(t, "p1-next-completed", starter.calls[0].id)
			},
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			links := runtime.NewMemChainLinkStore()
			if tc.prepopulate != nil {
				require.NoError(t, links.Record(t.Context(), *tc.prepopulate))
			}
			starter := &recordingStarter{err: tc.starterErr}
			opts := []runtime.ChainerOption{}
			if !tc.noLinks {
				opts = append(opts, runtime.WithChainLinks(links))
			}
			c := runtime.NewChainer(starter, tc.policy, opts...)
			err := c.Handle(t.Context(), tc.ev)
			tc.assert(t, err, starter, links)
		})
	}
}

// TestChainerSatisfiedByRunner pins the InstanceStarter contract to *runtime.Runner.
func TestChainerSatisfiedByRunner(t *testing.T) {
	var _ runtime.InstanceStarter = (*runtime.Runner)(nil)
}

// TestNewChainerNilGuards asserts the constructor fails fast on a nil starter or
// policy — a Chainer is unusable without both.
func TestNewChainerNilGuards(t *testing.T) {
	policy := func(context.Context, runtime.ChainEvent) (runtime.SuccessorDecision, bool) {
		return runtime.SuccessorDecision{}, false
	}
	assert.Panics(t, func() { runtime.NewChainer(nil, policy) }, "nil starter must panic")
	assert.Panics(t, func() { runtime.NewChainer(&recordingStarter{}, nil) }, "nil policy must panic")
	assert.NotPanics(t, func() { runtime.NewChainer(&recordingStarter{}, policy) }, "both set must construct")
}
