package engine

// state_timer_waiters_predicates_test.go — white-box tests for the two TimerKind
// predicates that replaced the single walkScoped() boolean (ADR-0179 Decision 4).
//
// They live in an in-package file because both predicates are unexported and
// because the HasArmedTimers control below plants a timerRecord directly.
// state_timer_waiters_test.go, the same-named sibling, is package engine_test
// and cannot reach either.
//
// The predicates are pure functions of the kind: no context, no clock, no state,
// so the table carries no ctx modifier.

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestTimerKindPredicates pins the two answers walkScoped() used to conflate.
// The pair exists because TimerCompensationRetry answers them DIFFERENTLY: it
// belongs to a compensation walk (so ADR-0178's dying-instance guard must let it
// through, exactly like a stall timer) yet it is forward work (so HasArmedTimers
// must SEE it, unlike a stall timer, which is a detection deadline whose firing
// manufactures the very incident it exists to detect).
//
// Every TimerKind is a row: a new kind added without a considered answer to both
// questions makes this table incomplete rather than silently inheriting a
// default.
func TestTimerKindPredicates(t *testing.T) {
	t.Parallel()

	type testCase struct {
		name   string
		kind   TimerKind
		assert func(t *testing.T, firesOnDyingInstance, detectionOnly bool)
	}

	cases := []testCase{
		{
			name: "an intermediate catch timer is ordinary forward work",
			kind: TimerIntermediate,
			assert: func(t *testing.T, firesOnDyingInstance, detectionOnly bool) {
				assert.False(t, firesOnDyingInstance)
				assert.False(t, detectionOnly)
			},
		},
		{
			name: "a deadline timer is ordinary forward work",
			kind: TimerDeadline,
			assert: func(t *testing.T, firesOnDyingInstance, detectionOnly bool) {
				assert.False(t, firesOnDyingInstance)
				assert.False(t, detectionOnly)
			},
		},
		{
			name: "an in-wait reminder is ordinary forward work",
			kind: TimerInWait,
			assert: func(t *testing.T, firesOnDyingInstance, detectionOnly bool) {
				assert.False(t, firesOnDyingInstance)
				assert.False(t, detectionOnly)
			},
		},
		{
			name: "an action-retry timer is ordinary forward work",
			kind: TimerRetry,
			assert: func(t *testing.T, firesOnDyingInstance, detectionOnly bool) {
				assert.False(t, firesOnDyingInstance)
				assert.False(t, detectionOnly)
			},
		},
		{
			name: "a compensation stall timer fires on a dying instance and is detection only",
			kind: TimerCompensationStall,
			assert: func(t *testing.T, firesOnDyingInstance, detectionOnly bool) {
				assert.True(t, firesOnDyingInstance,
					"ADR-0175: the terminating walks are the ones an operator most needs to see wedged")
				assert.True(t, detectionOnly,
					"firing a stall deadline manufactures the incident the window exists to detect")
			},
		},
		{
			name: "a compensation retry timer fires on a dying instance and is NOT detection only",
			kind: TimerCompensationRetry,
			assert: func(t *testing.T, firesOnDyingInstance, detectionOnly bool) {
				assert.True(t, firesOnDyingInstance,
					"the retry belongs to the walk, and a terminating walk must still be able to roll back")
				assert.False(t, detectionOnly,
					"ADR-0179: the retry exists to RE-DISPATCH; a harness must be able to fire it")
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			tc.assert(t, tc.kind.firesOnDyingInstance(), tc.kind.detectionOnly())
		})
	}
}

// TestHasArmedTimersDistinguishesRetryFromStall is the control the predicate
// split exists for. Extending walkScoped() to TimerCompensationRetry — the
// design this replaced — would leave the retry row measuring FALSE here, and a
// consumer driving the engine through processtest would then see
// Classify.Reason="unknown", AutoTimers fires=false and ErrUnhandledPark.
//
// The stall row is the opposite direction: it is the ADR-0175 behaviour, and it
// is the regression guard that the split did not invert the answer the shipped
// kind already had.
//
// ⚠ LIMIT OF THIS TEST. Nothing arms a TimerCompensationRetry yet, so the record
// is planted DIRECTLY in the fixture. The end-to-end control — that a REAL
// compensation-retry backoff measures HasArmedTimers()==true — belongs to the
// step that lands retryFailedCompensation (ADR-0179 P1-D) and is NOT covered
// here. Do not assume it is.
func TestHasArmedTimersDistinguishesRetryFromStall(t *testing.T) {
	t.Parallel()

	type testCase struct {
		name   string
		timers []timerRecord
		assert func(t *testing.T, armed bool)
	}

	cases := []testCase{
		{
			name:   "a compensation retry record is work a harness may fire",
			timers: []timerRecord{{TimerID: "tm-retry", Kind: TimerCompensationRetry, CommandID: "cmd-1"}},
			assert: func(t *testing.T, armed bool) {
				assert.True(t, armed)
			},
		},
		{
			name:   "a compensation stall record alone is not",
			timers: []timerRecord{{TimerID: "tm-stall", Kind: TimerCompensationStall, CommandID: "cmd-1"}},
			assert: func(t *testing.T, armed bool) {
				assert.False(t, armed)
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			s := &InstanceState{Timers: tc.timers}
			require.Len(t, s.TimerWaiters(), len(tc.timers),
				"precondition: the record is the instance's ONLY timer waiter, so HasArmedTimers reflects its kind alone")

			tc.assert(t, s.HasArmedTimers())
		})
	}
}
