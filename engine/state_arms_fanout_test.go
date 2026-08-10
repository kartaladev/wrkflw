package engine

// state_arms_fanout_test.go — ADR-0158: a broadcast signal fires EVERY matching
// arm per family, so the lookups return every matching arm's IDENTITY rather
// than the first matching arm.
//
// Identities are values, not pointers: removeArmsWhere reallocates the backing
// array and the per-family wrappers assign it over the field, so a pointer taken
// before an earlier fire addresses the DETACHED array where the removed arm is
// still intact — dispatching through it would fire an already-retired arm.
//
// ⚠ There is deliberately NO SORT. ADR-0158 Decision 2 specified a per-family
// NonInterrupting sort and execution refuted it in BOTH directions: whether an
// earlier arm's effects are destroyable depends on the arm's BODY, not its flag.
// The "declaration order" rows below exist to keep a sort from coming back.

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestArmedEventIDsBySignal(t *testing.T) {
	t.Parallel()

	type testCase struct {
		name   string
		state  *InstanceState
		signal string
		assert func(t *testing.T, got []gatewayArmID)
	}

	cases := []testCase{
		{
			name: "every matching arm is returned, in slice order",
			state: &InstanceState{ArmedEvents: []armedEvent{
				{GatewayToken: "gwA", CatchNode: "catchA", triggerMatch: triggerMatch{Signal: "x"}},
				{GatewayToken: "gwB", CatchNode: "catchB", triggerMatch: triggerMatch{Signal: "x"}},
			}},
			signal: "x",
			assert: func(t *testing.T, got []gatewayArmID) {
				assert.Equal(t, []gatewayArmID{
					{GatewayToken: "gwA", CatchNode: "catchA"},
					{GatewayToken: "gwB", CatchNode: "catchB"},
				}, got, "both matching gateway arms must be returned, in declaration order")
			},
		},
		{
			name: "identical identity tuples collapse to one",
			state: &InstanceState{ArmedEvents: []armedEvent{
				{GatewayToken: "gwA", CatchNode: "catchA", Flow: "f1", triggerMatch: triggerMatch{Signal: "x"}},
				{GatewayToken: "gwA", CatchNode: "catchA", Flow: "f2", triggerMatch: triggerMatch{Signal: "x"}},
			}},
			signal: "x",
			assert: func(t *testing.T, got []gatewayArmID) {
				assert.Equal(t, []gatewayArmID{{GatewayToken: "gwA", CatchNode: "catchA"}}, got,
					"model.Validate accepts two flows between one pair, so colliding identities must de-duplicate")
			},
		},
		{
			name: "non-signal arms are never returned",
			state: &InstanceState{ArmedEvents: []armedEvent{
				{GatewayToken: "gwA", CatchNode: "catchTimer", triggerMatch: triggerMatch{TimerID: "tm1"}},
				{GatewayToken: "gwA", CatchNode: "catchMsg", triggerMatch: triggerMatch{Message: "m"}},
				{GatewayToken: "gwA", CatchNode: "catchErr"},
			}},
			signal: "x",
			assert: func(t *testing.T, got []gatewayArmID) {
				assert.Empty(t, got, "timer, message and error-shaped arms must not match a signal lookup")
			},
		},
		{
			name: "an empty signal name matches nothing",
			state: &InstanceState{ArmedEvents: []armedEvent{
				{GatewayToken: "gwA", CatchNode: "catchErr"},
			}},
			signal: "",
			assert: func(t *testing.T, got []gatewayArmID) {
				assert.Empty(t, got, "ADR-0152 defence in depth: an empty key names no arm")
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			tc.assert(t, tc.state.armedEventIDsBySignal(tc.signal))
		})
	}
}

func TestBoundaryArmIDsBySignal(t *testing.T) {
	t.Parallel()

	type testCase struct {
		name   string
		state  *InstanceState
		signal string
		assert func(t *testing.T, got []boundaryArmID)
	}

	cases := []testCase{
		{
			name: "every matching arm is returned, in slice order",
			state: &InstanceState{Boundaries: []boundaryArm{
				{HostToken: "t1", BoundaryNode: "bndA", triggerMatch: triggerMatch{Signal: "x"}},
				{HostToken: "t2", BoundaryNode: "bndB", triggerMatch: triggerMatch{Signal: "x"}},
			}},
			signal: "x",
			assert: func(t *testing.T, got []boundaryArmID) {
				assert.Equal(t, []boundaryArmID{
					{HostToken: "t1", BoundaryNode: "bndA"},
					{HostToken: "t2", BoundaryNode: "bndB"},
				}, got, "both hosts' arms must be returned — this is the headline defect")
			},
		},
		{
			// REGRESSION GUARD against a re-introduced NonInterrupting sort.
			// Paired with the event-sub case below, which declares them the other
			// way round: a sort in EITHER direction breaks one of the two.
			name: "a non-interrupting arm declared SECOND stays second",
			state: &InstanceState{Boundaries: []boundaryArm{
				{HostToken: "t1", BoundaryNode: "bndInt", triggerMatch: triggerMatch{Signal: "x"}},
				{HostToken: "t1", BoundaryNode: "bndNI", NonInterrupting: true, triggerMatch: triggerMatch{Signal: "x"}},
			}},
			signal: "x",
			assert: func(t *testing.T, got []boundaryArmID) {
				assert.Equal(t, []boundaryArmID{
					{HostToken: "t1", BoundaryNode: "bndInt"},
					{HostToken: "t1", BoundaryNode: "bndNI"},
				}, got, "ADR-0158 Decision 2: declaration order, NOT a NonInterrupting sort")
			},
		},
		{
			// Colliding identities differ in fields that decide whether the host
			// is interrupted at all, so the tie-break is load-bearing.
			name: "colliding identities collapse to one",
			state: &InstanceState{Boundaries: []boundaryArm{
				{HostToken: "t1", BoundaryNode: "bnd", triggerMatch: triggerMatch{Signal: "x"}},
				{HostToken: "t1", BoundaryNode: "bnd", NonInterrupting: true, Action: "audit-action", triggerMatch: triggerMatch{Signal: "x"}},
			}},
			signal: "x",
			assert: func(t *testing.T, got []boundaryArmID) {
				assert.Equal(t, []boundaryArmID{{HostToken: "t1", BoundaryNode: "bnd"}}, got,
					"model.Validate accepts duplicate node ids, so colliding identities must de-duplicate")
			},
		},
		{
			name: "an empty signal name matches nothing",
			state: &InstanceState{Boundaries: []boundaryArm{
				{HostToken: "t1", BoundaryNode: "bndErr"},
			}},
			signal: "",
			assert: func(t *testing.T, got []boundaryArmID) {
				assert.Empty(t, got, "ADR-0152 defence in depth: an empty key names no arm")
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			tc.assert(t, tc.state.boundaryArmIDsBySignal(tc.signal))
		})
	}
}

func TestEventTriggeredSubprocessArmIDsBySignal(t *testing.T) {
	t.Parallel()

	type testCase struct {
		name   string
		state  *InstanceState
		signal string
		assert func(t *testing.T, got []eventSubArmID)
	}

	cases := []testCase{
		{
			name: "every matching arm is returned, in slice order",
			state: &InstanceState{EventTriggeredSubprocesses: []eventTriggeredSubprocessArm{
				{EnclosingScopeID: "s1", EventSubprocessNode: "espA", triggerMatch: triggerMatch{Signal: "x"}},
				{EnclosingScopeID: "s2", EventSubprocessNode: "espB", triggerMatch: triggerMatch{Signal: "x"}},
			}},
			signal: "x",
			assert: func(t *testing.T, got []eventSubArmID) {
				assert.Equal(t, []eventSubArmID{
					{EnclosingScopeID: "s1", EventSubprocessNode: "espA"},
					{EnclosingScopeID: "s2", EventSubprocessNode: "espB"},
				}, got, "arms in sibling scopes must both be returned")
			},
		},
		{
			// REGRESSION GUARD, the mirror of the boundary case: here the
			// non-interrupting arm is declared FIRST. A sort in either direction
			// breaks one of the two rows.
			name: "a non-interrupting arm declared FIRST stays first",
			state: &InstanceState{EventTriggeredSubprocesses: []eventTriggeredSubprocessArm{
				{EnclosingScopeID: "s1", EventSubprocessNode: "espNI", NonInterrupting: true, triggerMatch: triggerMatch{Signal: "x"}},
				{EnclosingScopeID: "s1", EventSubprocessNode: "espInt", triggerMatch: triggerMatch{Signal: "x"}},
			}},
			signal: "x",
			assert: func(t *testing.T, got []eventSubArmID) {
				assert.Equal(t, []eventSubArmID{
					{EnclosingScopeID: "s1", EventSubprocessNode: "espNI"},
					{EnclosingScopeID: "s1", EventSubprocessNode: "espInt"},
				}, got, "ADR-0158 Decision 2: declaration order, NOT a NonInterrupting sort")
			},
		},
		{
			// The ROOT scope is named by the empty string. Guarding it the way
			// the gateway and boundary families guard their owner keys would
			// silently disable every top-level event sub-process.
			name: "a ROOT-scope arm has a valid empty EnclosingScopeID",
			state: &InstanceState{EventTriggeredSubprocesses: []eventTriggeredSubprocessArm{
				{EnclosingScopeID: "", EventSubprocessNode: "espRoot", triggerMatch: triggerMatch{Signal: "x"}},
			}},
			signal: "x",
			assert: func(t *testing.T, got []eventSubArmID) {
				assert.Equal(t, []eventSubArmID{{EnclosingScopeID: "", EventSubprocessNode: "espRoot"}}, got,
					"the root scope is a valid identity, not a missing key")
			},
		},
		{
			name: "an empty signal name matches nothing",
			state: &InstanceState{EventTriggeredSubprocesses: []eventTriggeredSubprocessArm{
				{EnclosingScopeID: "", EventSubprocessNode: "espRoot"},
			}},
			signal: "",
			assert: func(t *testing.T, got []eventSubArmID) {
				assert.Empty(t, got, "ADR-0152 defence in depth: an empty key names no arm")
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			tc.assert(t, tc.state.eventTriggeredSubprocessArmIDsBySignal(tc.signal))
		})
	}
}

// ── …ByID re-resolvers (ADR-0158) ────────────────────────────────────────────
//
// A snapshotted identity is re-resolved immediately before its arm fires, and
// skipped when it no longer resolves. Re-resolution is an EXISTENCE CHECK ONLY:
// it must never be used to select the NEXT arm, or the non-terminating re-scan
// that ADR-0158 rejected comes back — a non-interrupting arm stays armed after
// firing, so a re-scan would find the same arm forever.

func TestArmedEventByID(t *testing.T) {
	t.Parallel()

	state := func() *InstanceState {
		return &InstanceState{ArmedEvents: []armedEvent{
			{GatewayToken: "gwA", CatchNode: "catchA", Flow: "f1", triggerMatch: triggerMatch{Signal: "x"}},
			{GatewayToken: "gwB", CatchNode: "catchB", Flow: "f2", triggerMatch: triggerMatch{Signal: "x"}},
		}}
	}

	t.Run("resolves after an earlier arm was removed", func(t *testing.T) {
		t.Parallel()

		s := state()
		s.removeArmedEventsForGateway("gwA")

		got := s.armedEventByID(gatewayArmID{GatewayToken: "gwB", CatchNode: "catchB"})
		if assert.NotNil(t, got, "the surviving arm must still resolve after a sibling was retired") {
			assert.Equal(t, "f2", got.Flow, "and it must be the SURVIVING arm, not a stale copy")
		}
	})

	t.Run("a retired arm resolves to nil", func(t *testing.T) {
		t.Parallel()

		s := state()
		s.removeArmedEventsForGateway("gwA")

		assert.Nil(t, s.armedEventByID(gatewayArmID{GatewayToken: "gwA", CatchNode: "catchA"}),
			"a retired identity must be skipped, not fired")
	})

	t.Run("an empty gateway token names no arm", func(t *testing.T) {
		t.Parallel()

		assert.Nil(t, state().armedEventByID(gatewayArmID{CatchNode: "catchA"}),
			"ADR-0152: an empty owner key names no arm, matching removeArmedEventsForGateway")
	})
}

func TestBoundaryArmByID(t *testing.T) {
	t.Parallel()

	t.Run("colliding identities resolve to the FIRST in slice order", func(t *testing.T) {
		t.Parallel()

		// model.Validate accepts duplicate node ids, and colliding arms are NOT
		// interchangeable: these two differ in the fields that decide whether the
		// delivery interrupts the host at all.
		s := &InstanceState{Boundaries: []boundaryArm{
			{HostToken: "t1", BoundaryNode: "bnd", triggerMatch: triggerMatch{Signal: "x"}},
			{HostToken: "t1", BoundaryNode: "bnd", NonInterrupting: true, Action: "audit-action", triggerMatch: triggerMatch{Signal: "x"}},
		}}

		got := s.boundaryArmByID(boundaryArmID{HostToken: "t1", BoundaryNode: "bnd"})
		if assert.NotNil(t, got) {
			assert.False(t, got.NonInterrupting, "the FIRST colliding arm wins, matching armIDsBySignal's de-dup")
			assert.Empty(t, got.Action, "the FIRST colliding arm wins")
		}
	})

	t.Run("an empty host token names no arm", func(t *testing.T) {
		t.Parallel()

		s := &InstanceState{Boundaries: []boundaryArm{{BoundaryNode: "bnd", triggerMatch: triggerMatch{Signal: "x"}}}}

		assert.Nil(t, s.boundaryArmByID(boundaryArmID{BoundaryNode: "bnd"}),
			"ADR-0152: an empty owner key names no arm, matching removeBoundaryArmsForHost")
	})
}

func TestEventTriggeredSubprocessArmByID(t *testing.T) {
	t.Parallel()

	t.Run("a ROOT-scope arm resolves on an EMPTY EnclosingScopeID", func(t *testing.T) {
		t.Parallel()

		// THE TRAP. Guarding the empty key here — as the gateway and boundary
		// re-resolvers correctly do for THEIR owner fields — would silently
		// disable every top-level event sub-process.
		s := &InstanceState{EventTriggeredSubprocesses: []eventTriggeredSubprocessArm{
			{EnclosingScopeID: "", EventSubprocessNode: "espRoot", triggerMatch: triggerMatch{Signal: "x"}},
		}}

		got := s.eventTriggeredSubprocessArmByID(eventSubArmID{EventSubprocessNode: "espRoot"})
		if assert.NotNil(t, got, "the root scope is a VALID identity, not a missing key") {
			assert.Equal(t, "espRoot", got.EventSubprocessNode)
		}
	})

	t.Run("a retired arm resolves to nil", func(t *testing.T) {
		t.Parallel()

		s := &InstanceState{EventTriggeredSubprocesses: []eventTriggeredSubprocessArm{
			{EnclosingScopeID: "s1", EventSubprocessNode: "espA", triggerMatch: triggerMatch{Signal: "x"}},
		}}
		s.removeEventTriggeredSubprocessArmsForScope("s1")

		assert.Nil(t, s.eventTriggeredSubprocessArmByID(eventSubArmID{EnclosingScopeID: "s1", EventSubprocessNode: "espA"}))
	})
}
