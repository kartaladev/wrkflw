package engine_test

// step_harvest_terminal_admission_test.go — ADR-0174, T11: on the terminal snapshot the
// harvest produces, EXACTLY ONE trigger reaches a handler — a plain full
// engine.CompensateRequested with both ToNode and ReverseNode empty.
//
// ⚠ Why this exists when a sibling already covers the policy split. Two different premises:
//
//   - engine/step_terminal_dispatch_test.go (TestTerminalDispatchOutcomes, ADR-0165) is the
//     EXHAUSTIVENESS guard. It is white-box (package engine), driven off allTriggerVariants,
//     and so a 16th trigger cannot land without a row. Its terminal fixtures are a
//     StatusFailed instance with parked waiters and a force-terminated instance whose
//     records sit in RootCompensations. Neither has ever had an open scope.
//   - THIS table is black-box and runs on the ADR-0174 substrate: a terminal snapshot whose
//     Scopes were HARVESTED and then nilled, whose records live in
//     ArchivedCompensations["sub"] rather than at root. That is a state no test could
//     produce before this delivery, and the question it answers is whether the new snapshot
//     shape is as inert to the trigger surface as the old one — in particular whether the
//     one admitted rollback can still find records that reached the archive by harvest
//     rather than by a normal scope exit.
//
// So the enumeration below is deliberately NOT presented as exhaustive: it is
// hand-maintained, and a NEW trigger will not appear in it (the sibling catches that). What
// it does catch is an EXISTING trigger being reclassified to allowOnTerminal — the change
// that would let a dead instance be resurrected, or its harvested records be walked twice.
// The admitted-count assertion in the parent's Cleanup is what turns that into a failure
// rather than a quietly passing extra row.
//
// terminalPolicy is unexported by design, so every row asserts BEHAVIOUR through the
// exported engine.Step: an error, a byte-identical no-op, or a walk that dispatched.
//
// No `ctx` case modifier (table-test skill rule 3): engine.Step documents ctx as carrying no
// cancellation semantics — it is used only for trace-correlated logging and is never
// inspected for control flow.

import (
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kartaladev/wrkflw/engine"
)

func TestOnlyAPlainFullRollbackIsAdmittedOnAHarvestedTerminalInstance(t *testing.T) {
	t.Parallel()

	def := forceTermInsideSubDef(true)
	_, terminal := driveToForceTerminationInsideSub(t, def, "i-harvest-admission")

	// Controls. Every row below is meaningless unless the substrate really is the
	// post-harvest terminal snapshot: dead, no open scope, and records reachable ONLY
	// through the archive key the harvest minted.
	require.True(t, terminal.Status.IsTerminal(), "control: the instance must be dead")
	require.Nil(t, terminal.Scopes, "control: the harvest closed every scope (ADR-0174)")
	require.Equal(t, []string{"undo-inner"}, actionsOf(terminal.ArchivedCompensations["sub"]),
		"control: the record must be in the ARCHIVE under the scope's node id — that is the "+
			"substrate this table is about, and it is what the admitted rollback must be able to walk")
	require.Empty(t, terminal.RootCompensations,
		"control: nothing at root, so an admitted walk can only be reading the harvest's output")

	// Delivered strictly after the instance died, so no row can be explained away as a
	// race with the terminal transition itself.
	deliveredAt := harvestT0.Add(time.Hour)

	// admissions counts the rows that reached a handler. The property is "exactly one",
	// and it is checked in the parent's Cleanup, which runs after every parallel subtest
	// has finished. Without it, flipping a second trigger to allowOnTerminal would simply
	// turn one row red and leave the INVARIANT unstated.
	var admissions atomic.Int64

	// rejectsWithError: a synchronous external caller must be TOLD its request was
	// refused. Step returns the zero StepResult on error.
	rejectsWithError := func(t *testing.T, r engine.StepResult, err error) {
		t.Helper()
		assert.ErrorIs(t, err, engine.ErrInstanceTerminal,
			"a synchronous caller must be told the instance is terminal")
		assert.Empty(t, r.Commands, "a refused trigger must dispatch nothing")
		assert.Zero(t, r.State.Status, "Step must return the zero StepResult on error")
	}

	// staysSilent: the trigger arrives from the engine's own asynchronous machinery
	// (scheduler, broadcast fan-out, worker relay), whose caller cannot distinguish a
	// no-op from success and must not retry. Byte-identical state: a silent DROP, not a
	// silent APPLY.
	staysSilent := func(t *testing.T, r engine.StepResult, err error) {
		t.Helper()
		require.NoError(t, err,
			"an asynchronously-delivered trigger must be dropped silently, not surfaced to a caller "+
				"that cannot act on it")
		assert.Empty(t, r.Commands, "a silently-dropped trigger must dispatch nothing")
		assert.Equal(t, terminal, r.State,
			"and it must leave the harvested terminal snapshot byte-identical — including "+
				"Scopes nil and the archive untouched")
	}

	// runsHandler: the one trigger that deliberately operates ON a terminal instance.
	runsHandler := func(t *testing.T, r engine.StepResult, err error) {
		t.Helper()
		admissions.Add(1)
		require.NoError(t, err,
			"a plain full rollback is a legitimate admin action on a terminal instance (ADR-0164 carve-out #1)")
		assert.Equal(t, engine.StatusCompensating, r.State.Status,
			"the walk must actually start, not be silently swallowed")
		assert.Equal(t, []string{"undo-inner"}, invokedActionNames(r.Commands),
			"and it must walk the HARVESTED record, proving the archive key the harvest minted is "+
				"reachable by consolidateArchiveIntoRoot")
	}

	type testCase struct {
		name    string
		trigger engine.Trigger
		assert  func(t *testing.T, r engine.StepResult, err error)
	}

	cases := []testCase{
		// Synchronous external callers: refused with ErrInstanceTerminal.
		{
			name:    "StartInstance_is_refused",
			trigger: engine.NewStartInstance(deliveredAt, nil),
			assert:  rejectsWithError,
		},
		{
			name:    "ResolveIncident_is_refused",
			trigger: engine.NewResolveIncident(deliveredAt, "i-harvest-admission-i1", 1),
			assert:  rejectsWithError,
		},
		{
			// The one CompensateRequested shape that must NOT be admitted. ToNode set is
			// resume intent: a partial rollback would leave the instance Running, i.e.
			// resurrect it. Its policy reads the receiver, which is why this row and the
			// admitted one below are not the same row with a different argument.
			name:    "a_partial_rollback_with_ToNode_set_is_refused",
			trigger: engine.NewCompensateRequested(deliveredAt, "inner-svc"),
			assert:  rejectsWithError,
		},

		// Engine-internal asynchronous delivery: dropped silently.
		{
			name:    "a_late_ActionCompleted_is_dropped_silently",
			trigger: engine.NewActionCompleted(deliveredAt, "i-harvest-admission-c1", nil),
			assert:  staysSilent,
		},
		{
			name:    "a_late_ActionFailed_is_dropped_silently",
			trigger: engine.NewActionFailed(deliveredAt, "i-harvest-admission-c1", "late-boom", false),
			assert:  staysSilent,
		},
		{
			name:    "a_late_TimerFired_is_dropped_silently",
			trigger: engine.NewTimerFired(deliveredAt, "i-harvest-admission-tm1"),
			assert:  staysSilent,
		},
		{
			name:    "a_broadcast_SignalReceived_is_dropped_silently",
			trigger: engine.NewSignalReceived(deliveredAt, "any-signal", nil),
			assert:  staysSilent,
		},
		{
			name:    "a_broadcast_MessageReceived_is_dropped_silently",
			trigger: engine.NewMessageReceived(deliveredAt, "any-message", "", nil),
			assert:  staysSilent,
		},
		{
			// ⚠ Reclassified from allowOnTerminal to rejectSilently by ADR-0165. Unguarded
			// it force-terminates an already-terminal instance, stamping StatusTerminated
			// over it — which on THIS substrate would also re-enter the harvest.
			name:    "a_redundant_CancelRequested_is_dropped_silently",
			trigger: engine.NewCancelRequested(deliveredAt),
			assert:  staysSilent,
		},

		// The ONE admitted trigger.
		{
			name:    "a_plain_full_CompensateRequested_is_admitted_and_walks_the_harvested_record",
			trigger: engine.NewCompensateRequested(deliveredAt, ""),
			assert:  runsHandler,
		},
	}

	t.Cleanup(func() {
		assert.EqualValues(t, 1, admissions.Load(),
			"EXACTLY ONE of these triggers may reach a handler on a terminal instance. A second "+
				"admission means an existing trigger was reclassified to allowOnTerminal, which is "+
				"how a dead instance gets resurrected or its harvested records walked twice")
	})

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			r, err := engine.Step(t.Context(), def, terminal, tc.trigger, engine.StepOptions{})
			tc.assert(t, r, err)
		})
	}
}
