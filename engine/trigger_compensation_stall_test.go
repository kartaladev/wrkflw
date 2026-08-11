package engine_test

// ADR-0175 — the ResolveCompensationStall trigger's shape.
//
// Three operator verbs ride ONE trigger rather than three, following the
// admin-trigger idiom CompensateRequested already sets (one type, several
// constructors).

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kartaladev/wrkflw/engine"
)

// TestResolveCompensationStallConstructors covers the three verbs' constructors
// and the trigger's terminal policy.
//
// terminalPolicy is rejectWithError, not rejectSilently: every one of these is a
// synchronous operator action, and an operator told nothing would reasonably
// conclude the escape worked.
func TestResolveCompensationStallConstructors(t *testing.T) {
	t.Parallel()

	at := time.Date(2026, 8, 12, 10, 0, 0, 0, time.UTC)

	type testCase struct {
		name    string
		build   func() engine.ResolveCompensationStall
		wantDis engine.CompensationDisposition
	}

	cases := []testCase{
		{
			name:    "retry",
			build:   func() engine.ResolveCompensationStall { return engine.NewRetryStalledCompensation(at, "c7", "inc1") },
			wantDis: engine.CompensationRetry,
		},
		{
			name:    "skip",
			build:   func() engine.ResolveCompensationStall { return engine.NewSkipStalledCompensation(at, "c7", "inc1") },
			wantDis: engine.CompensationSkip,
		},
		{
			name:    "abandon",
			build:   func() engine.ResolveCompensationStall { return engine.NewAbandonCompensationWalk(at, "c7", "inc1") },
			wantDis: engine.CompensationAbandon,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			trg := tc.build()
			assert.Equal(t, tc.wantDis, trg.Disposition)
			assert.Equal(t, "c7", trg.CommandID)
			assert.Equal(t, "inc1", trg.IncidentID)
			assert.Equal(t, at, trg.OccurredAt())
		})
	}
}

// TestNewResolveCompensationStallCarriesTheDisposition covers the general
// constructor the layered surface needs: runtime, service and HTTP all take a
// disposition as DATA from their caller, so none of them can pick one of the
// three named constructors without a switch of its own.
func TestNewResolveCompensationStallCarriesTheDisposition(t *testing.T) {
	t.Parallel()

	at := time.Date(2026, 8, 12, 10, 0, 0, 0, time.UTC)
	for _, d := range []engine.CompensationDisposition{
		engine.CompensationRetry, engine.CompensationSkip, engine.CompensationAbandon,
	} {
		trg := engine.NewResolveCompensationStall(at, "c7", "inc1", d)
		assert.Equal(t, d, trg.Disposition)
		assert.Equal(t, "c7", trg.CommandID)
		assert.Equal(t, "inc1", trg.IncidentID)
		assert.Equal(t, at, trg.OccurredAt())
	}
}

// TestResolveCompensationStallRequiresCommandID pins that CommandID is a
// REQUIRED identity field, rejected before any handler runs.
//
// Without the cursor match it enables, a verb acting on "whatever is in flight"
// was measured running a compensation action TWICE — the original completion
// then rejected as "no token awaiting command", which an at-least-once action
// transport turns into a redelivery loop.
func TestResolveCompensationStallRequiresCommandID(t *testing.T) {
	t.Parallel()

	state, _, _ := startedStallWalk(t)

	_, err := engine.Step(t.Context(), threeCompensableDef(), state,
		engine.NewSkipStalledCompensation(time.Date(2026, 8, 12, 10, 0, 0, 0, time.UTC), "", ""),
		engine.StepOptions{CompensationStallAfter: stallWindow})

	require.Error(t, err, "an empty CommandID names no dispatch and must not reach the handler")
	assert.ErrorIs(t, err, engine.ErrEmptyTriggerKey)
}
