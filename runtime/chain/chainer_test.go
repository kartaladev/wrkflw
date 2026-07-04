package chain_test

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zakyalvan/krtlwrkflw/engine"
	"github.com/zakyalvan/krtlwrkflw/definition"
	"github.com/zakyalvan/krtlwrkflw/runtime"
	"github.com/zakyalvan/krtlwrkflw/runtime/chain"
	"github.com/zakyalvan/krtlwrkflw/runtime/internal/runtimetest"
	"github.com/zakyalvan/krtlwrkflw/runtime/kernel"
)

// startCall records one InstanceStarter.Run invocation.
type startCall struct {
	def  *definition.ProcessDefinition
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

func (s *recordingStarter) Run(_ context.Context, def *definition.ProcessDefinition, id string, vars map[string]any) (engine.InstanceState, error) {
	s.calls = append(s.calls, startCall{def: def, id: id, vars: vars})
	return s.state, s.err
}

func fulfillmentDef() *definition.ProcessDefinition {
	return &definition.ProcessDefinition{ID: "fulfillment", Version: 1}
}

func TestChainerHandle(t *testing.T) {
	errTransient := errors.New("db down")

	completedEv := chain.ChainEvent{
		PredecessorID:            "p1",
		PredecessorDefinitionRef: "approval:1",
		Outcome:                  kernel.OutcomeCompleted,
		Result:                   map[string]any{"orderID": "o-7"},
	}

	// successorFor returns a policy that always starts fulfillmentDef with the
	// event's Result as the successor vars.
	successorFor := func() chain.SuccessorPolicy {
		return func(_ context.Context, ev chain.ChainEvent) (chain.SuccessorDecision, bool) {
			return chain.SuccessorDecision{Def: fulfillmentDef(), Vars: ev.Result}, true
		}
	}

	tests := map[string]struct {
		ev          chain.ChainEvent
		policy      chain.SuccessorPolicy
		prepopulate *kernel.ChainLink
		noLinks     bool
		starterErr  error
		assert      func(t *testing.T, gotErr error, starter *recordingStarter, links *kernel.MemChainLinkStore)
	}{
		"no successor (ok=false) does nothing": {
			ev: completedEv,
			policy: func(context.Context, chain.ChainEvent) (chain.SuccessorDecision, bool) {
				return chain.SuccessorDecision{}, false
			},
			assert: func(t *testing.T, gotErr error, starter *recordingStarter, links *kernel.MemChainLinkStore) {
				require.NoError(t, gotErr)
				assert.Empty(t, starter.calls, "no policy successor => no start")
				hops, _ := links.ListByPredecessor(t.Context(), "p1")
				assert.Empty(t, hops, "no link recorded")
			},
		},
		"no successor (nil Def) does nothing": {
			ev: completedEv,
			policy: func(context.Context, chain.ChainEvent) (chain.SuccessorDecision, bool) {
				return chain.SuccessorDecision{Def: nil}, true
			},
			assert: func(t *testing.T, gotErr error, starter *recordingStarter, links *kernel.MemChainLinkStore) {
				require.NoError(t, gotErr)
				assert.Empty(t, starter.calls)
			},
		},
		"happy path records link and starts successor with deterministic id": {
			ev:     completedEv,
			policy: successorFor(),
			assert: func(t *testing.T, gotErr error, starter *recordingStarter, links *kernel.MemChainLinkStore) {
				require.NoError(t, gotErr)
				require.Len(t, starter.calls, 1)
				assert.Equal(t, "p1-next-completed", starter.calls[0].id)
				assert.Equal(t, "fulfillment", starter.calls[0].def.ID)
				assert.Equal(t, map[string]any{"orderID": "o-7"}, starter.calls[0].vars)

				got, ok, _ := links.LookupBySuccessor(t.Context(), "p1-next-completed")
				require.True(t, ok)
				assert.Equal(t, "p1", got.PredecessorID)
				assert.Equal(t, kernel.OutcomeCompleted, got.Outcome)
			},
		},
		"already-recorded link still attempts the start (idempotent via ErrInstanceExists)": {
			ev:          completedEv,
			policy:      successorFor(),
			prepopulate: &kernel.ChainLink{PredecessorID: "p1", Outcome: kernel.OutcomeCompleted, SuccessorID: "p1-next-completed"},
			assert: func(t *testing.T, gotErr error, starter *recordingStarter, links *kernel.MemChainLinkStore) {
				require.NoError(t, gotErr)
				// A pre-existing link must NOT suppress the start: the link may have
				// been recorded by a prior delivery whose start then failed. Re-attempt
				// the start; Store.Create's ErrInstanceExists makes a genuine duplicate
				// a no-op, so this recovers a lost successor without double-starting.
				require.Len(t, starter.calls, 1, "the start must be (re)attempted even when the link exists")
				assert.Equal(t, "p1-next-completed", starter.calls[0].id)
			},
		},
		"duplicate instance start is treated as no-op": {
			ev:         completedEv,
			policy:     successorFor(),
			starterErr: kernel.ErrInstanceExists,
			assert: func(t *testing.T, gotErr error, starter *recordingStarter, links *kernel.MemChainLinkStore) {
				require.NoError(t, gotErr, "ErrInstanceExists from the starter is a clean no-op")
				require.Len(t, starter.calls, 1)
			},
		},
		"transient start error propagates for redelivery": {
			ev:         completedEv,
			policy:     successorFor(),
			starterErr: errTransient,
			assert: func(t *testing.T, gotErr error, starter *recordingStarter, links *kernel.MemChainLinkStore) {
				require.Error(t, gotErr)
				assert.ErrorIs(t, gotErr, errTransient, "transient error must remain inspectable so the broker re-delivers")
			},
		},
		"no link store still starts (deterministic id only)": {
			ev:      completedEv,
			policy:  successorFor(),
			noLinks: true,
			assert: func(t *testing.T, gotErr error, starter *recordingStarter, _ *kernel.MemChainLinkStore) {
				require.NoError(t, gotErr)
				require.Len(t, starter.calls, 1)
				assert.Equal(t, "p1-next-completed", starter.calls[0].id)
			},
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			links := kernel.NewMemChainLinkStore()
			if tc.prepopulate != nil {
				require.NoError(t, links.Record(t.Context(), *tc.prepopulate))
			}
			starter := &recordingStarter{err: tc.starterErr}
			opts := []chain.ChainerOption{}
			if !tc.noLinks {
				opts = append(opts, chain.WithChainLinks(links))
			}
			c := runtimetest.MustChainer(t, starter, tc.policy, opts...)
			err := c.Handle(t.Context(), tc.ev)
			tc.assert(t, err, starter, links)
		})
	}
}

// TestChainerHandleRetriesStartAfterTransientFailure is the lost-successor
// regression (whole-branch review CRITICAL): if the link is recorded but the
// successor start then fails transiently, a redelivery must RE-ATTEMPT the start
// — not be suppressed by the now-existing link. Recording the link before the
// start must never drop the successor permanently.
func TestChainerHandleRetriesStartAfterTransientFailure(t *testing.T) {
	ctx := t.Context()
	links := kernel.NewMemChainLinkStore()
	starter := &recordingStarter{err: errors.New("db down")}
	policy := func(_ context.Context, ev chain.ChainEvent) (chain.SuccessorDecision, bool) {
		return chain.SuccessorDecision{Def: fulfillmentDef(), Vars: ev.Result}, true
	}
	c := runtimetest.MustChainer(t, starter, policy, chain.WithChainLinks(links))
	ev := chain.ChainEvent{PredecessorID: "p1", Outcome: kernel.OutcomeCompleted}

	// First delivery: the link is recorded, then the start fails transiently.
	require.Error(t, c.Handle(ctx, ev), "transient start failure must propagate (nack)")
	_, ok, _ := links.LookupBySuccessor(ctx, "p1-next-completed")
	require.True(t, ok, "the link was recorded before the failed start")

	// Redelivery with the transient condition cleared: the start MUST be retried
	// despite the existing link.
	starter.err = nil
	require.NoError(t, c.Handle(ctx, ev))
	require.Len(t, starter.calls, 2, "the successor start must be re-attempted on redelivery, not skipped")
}

// TestChainerSatisfiedByRunner pins the InstanceStarter contract to *runtime.ProcessDriver.
func TestChainerSatisfiedByRunner(t *testing.T) {
	var _ chain.InstanceStarter = (*runtime.ProcessDriver)(nil)
}

// TestNewChainerNilGuards asserts the constructor returns ErrNilDependency on a
// nil starter or policy — a Chainer is unusable without both.
func TestNewChainerNilGuards(t *testing.T) {
	policy := chain.SuccessorPolicy(func(_ context.Context, _ chain.ChainEvent) (chain.SuccessorDecision, bool) {
		return chain.SuccessorDecision{}, false
	})
	_, err := chain.NewChainer(nil, policy)
	require.ErrorIs(t, err, kernel.ErrNilDependency, "nil starter must return ErrNilDependency")
	_, err = chain.NewChainer(&recordingStarter{}, nil)
	require.ErrorIs(t, err, kernel.ErrNilDependency, "nil policy must return ErrNilDependency")
	c, err := chain.NewChainer(&recordingStarter{}, policy)
	require.NoError(t, err, "valid args must succeed")
	require.NotNil(t, c, "valid args must return a non-nil Chainer")
}

// TestWithClockNilFallsBackToSystem asserts that passing a nil clock to
// WithClock does NOT overwrite the constructor's clock.System() default.
// The guard is verified by exercising Handle on a path that calls clk.Now()
// (ChainLink.CreatedAt stamping) — a nil clock would panic.
func TestWithClockNilFallsBackToSystem(t *testing.T) {
	policy := func(_ context.Context, ev chain.ChainEvent) (chain.SuccessorDecision, bool) {
		return chain.SuccessorDecision{Def: fulfillmentDef(), Vars: ev.Result}, true
	}
	links := kernel.NewMemChainLinkStore()
	starter := &recordingStarter{}
	c := runtimetest.MustChainer(t, starter, policy,
		chain.WithChainLinks(links),
		chain.WithClock(nil), // must be ignored — default clock.System() must survive
	)
	ev := chain.ChainEvent{
		PredecessorID: "p-nil-clk",
		Outcome:       kernel.OutcomeCompleted,
		Result:        map[string]any{"k": "v"},
	}
	// If the nil-guard is absent the nil clock's Now() call panics here.
	assert.NotPanics(t, func() {
		_ = c.Handle(t.Context(), ev)
	}, "WithClock(nil) must be ignored; clk.Now() must not panic")
}

func TestNewChainerFailsFast(t *testing.T) {
	t.Parallel()

	policy := chain.SuccessorPolicy(func(_ context.Context, _ chain.ChainEvent) (chain.SuccessorDecision, bool) {
		return chain.SuccessorDecision{}, false
	})
	starter := &recordingStarter{}
	type testCase struct {
		name    string
		starter chain.InstanceStarter
		policy  chain.SuccessorPolicy
		assert  func(t *testing.T, c *chain.Chainer, err error)
	}
	cases := []testCase{
		{
			name:    "nil starter",
			starter: nil,
			policy:  policy,
			assert: func(t *testing.T, c *chain.Chainer, err error) {
				require.ErrorIs(t, err, kernel.ErrNilDependency)
				require.Nil(t, c)
			},
		},
		{
			name:    "nil policy",
			starter: starter,
			policy:  nil,
			assert: func(t *testing.T, c *chain.Chainer, err error) {
				require.ErrorIs(t, err, kernel.ErrNilDependency)
				require.Nil(t, c)
			},
		},
		{
			name:    "valid args",
			starter: starter,
			policy:  policy,
			assert: func(t *testing.T, c *chain.Chainer, err error) {
				require.NoError(t, err)
				require.NotNil(t, c)
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			c, err := chain.NewChainer(tc.starter, tc.policy)
			tc.assert(t, c, err)
		})
	}
}
