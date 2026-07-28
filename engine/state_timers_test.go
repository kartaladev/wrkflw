package engine

// state_timers_test.go — white-box tests for the timer lookup/sweep helpers.
// Covers ADR-0152: an empty identity key matches no record.
//
// Every empty-key case plants a record HOLDING the empty value, so the test
// genuinely reproduces the wildcard and fails if the guard is removed.

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCancelTimersByTaskID(t *testing.T) {
	t.Parallel()

	type testCase struct {
		name    string
		timers  []timerRecord
		taskID  string
		exclude string
		assert  func(t *testing.T, cancelled []string, s *InstanceState)
	}

	cases := []testCase{
		{
			name: "cancels every timer for the named task",
			timers: []timerRecord{
				{TimerID: "tm1", Kind: TimerDeadline, Token: "tokA", TaskID: "h1"},
				{TimerID: "tm2", Kind: TimerInWait, Token: "tokA", TaskID: "h1"},
				{TimerID: "tm3", Kind: TimerDeadline, Token: "tokB", TaskID: "h2"},
			},
			taskID: "h1",
			assert: func(t *testing.T, cancelled []string, s *InstanceState) {
				assert.ElementsMatch(t, []string{"tm1", "tm2"}, cancelled)
				require.Len(t, s.Timers, 1)
				assert.Equal(t, "tm3", s.Timers[0].TimerID)
			},
		},
		{
			// EXEMPTION: excludeTimerID "" means "exclude nothing".
			name: "empty excludeTimerID excludes nothing",
			timers: []timerRecord{
				{TimerID: "tm1", Kind: TimerDeadline, Token: "tokA", TaskID: "h1"},
				{TimerID: "tm2", Kind: TimerInWait, Token: "tokA", TaskID: "h1"},
			},
			taskID:  "h1",
			exclude: "",
			assert: func(t *testing.T, cancelled []string, s *InstanceState) {
				assert.ElementsMatch(t, []string{"tm1", "tm2"}, cancelled,
					"an empty excludeTimerID must not suppress any cancellation")
				assert.Empty(t, s.Timers)
			},
		},
		{
			name: "honours a populated excludeTimerID",
			timers: []timerRecord{
				{TimerID: "tm1", Kind: TimerDeadline, Token: "tokA", TaskID: "h1"},
				{TimerID: "tm2", Kind: TimerInWait, Token: "tokA", TaskID: "h1"},
			},
			taskID:  "h1",
			exclude: "tm1",
			assert: func(t *testing.T, cancelled []string, s *InstanceState) {
				assert.Equal(t, []string{"tm2"}, cancelled)
				require.Len(t, s.Timers, 1)
				assert.Equal(t, "tm1", s.Timers[0].TimerID)
			},
		},
		{
			// ADR-0152, the live defect. TimerRetry records carry no TaskID, so an
			// empty key swept every retry in the instance — including retries owned
			// by tokens in sibling scopes, wedging them in TokenWaiting forever.
			name: "empty task key cancels nothing",
			timers: []timerRecord{
				{TimerID: "tm1", Kind: TimerRetry, Token: "tokA", NodeID: "svcA", ScopeID: "sc1"},
				{TimerID: "tm2", Kind: TimerRetry, Token: "tokB", NodeID: "svcB", ScopeID: "sc2"},
				{TimerID: "tm3", Kind: TimerDeadline, Token: "tokC", TaskID: "h1"},
			},
			taskID: "",
			assert: func(t *testing.T, cancelled []string, s *InstanceState) {
				assert.Empty(t, cancelled, "an empty task key must cancel no timer")
				assert.Len(t, s.Timers, 3, "every record must survive an empty-key sweep")
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			s := &InstanceState{Timers: tc.timers}
			cancelled := s.cancelTimersByTaskID(tc.taskID, tc.exclude)
			tc.assert(t, cancelled, s)
		})
	}
}

func TestCancelTimersForToken(t *testing.T) {
	t.Parallel()

	type testCase struct {
		name    string
		tokenID string
		exclude string
		assert  func(t *testing.T, cancelled []string, s *InstanceState)
	}

	cases := []testCase{
		{
			name:    "cancels the token's timers except the excluded one",
			tokenID: "tokA",
			exclude: "t2",
			assert: func(t *testing.T, cancelled []string, s *InstanceState) {
				assert.Equal(t, []string{"t1"}, cancelled)
				remaining := []string{s.Timers[0].TimerID, s.Timers[1].TimerID, s.Timers[2].TimerID}
				assert.ElementsMatch(t, []string{"t2", "t3", "t4"}, remaining)
			},
		},
		{
			// Without the guard this sweeps the planted empty-Token record t4.
			name:    "empty token key cancels nothing",
			tokenID: "",
			assert: func(t *testing.T, cancelled []string, s *InstanceState) {
				assert.Empty(t, cancelled)
				assert.Len(t, s.Timers, 4, "every record must survive an empty-key sweep")
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			s := &InstanceState{Timers: []timerRecord{
				{TimerID: "t1", Kind: TimerInWait, Token: "tokA"},
				{TimerID: "t2", Kind: TimerIntermediate, Token: "tokA"},
				{TimerID: "t3", Kind: TimerInWait, Token: "tokB"},
				{TimerID: "t4", Kind: TimerRetry, Token: ""},
			}}
			tc.assert(t, s.cancelTimersForToken(tc.tokenID, tc.exclude), s)
		})
	}
}

func TestTimerByID(t *testing.T) {
	t.Parallel()

	type testCase struct {
		name    string
		timerID string
		assert  func(t *testing.T, rec *timerRecord)
	}

	cases := []testCase{
		{
			name:    "returns the matching record",
			timerID: "tm1",
			assert: func(t *testing.T, rec *timerRecord) {
				require.NotNil(t, rec)
				assert.Equal(t, "tm1", rec.TimerID)
			},
		},
		{
			name:    "returns nil for an unknown id",
			timerID: "nope",
			assert: func(t *testing.T, rec *timerRecord) {
				assert.Nil(t, rec)
			},
		},
		{
			// The fixture plants a record WITH an empty TimerID, so this fails
			// without the guard.
			name:    "returns nil for an empty id",
			timerID: "",
			assert: func(t *testing.T, rec *timerRecord) {
				assert.Nil(t, rec, "an empty timer id names no record")
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			s := &InstanceState{Timers: []timerRecord{
				{TimerID: "tm1", Kind: TimerRetry},
				{TimerID: "", Kind: TimerRetry, Token: "tokGhost"},
			}}
			tc.assert(t, s.timerByID(tc.timerID))
		})
	}
}

func TestRemoveTimer(t *testing.T) {
	t.Parallel()

	type testCase struct {
		name    string
		timerID string
		assert  func(t *testing.T, s *InstanceState)
	}

	cases := []testCase{
		{
			name:    "removes the matching record",
			timerID: "tm1",
			assert: func(t *testing.T, s *InstanceState) {
				require.Len(t, s.Timers, 2)
				assert.Equal(t, "tm2", s.Timers[0].TimerID)
			},
		},
		{
			// Without the guard this deletes the planted empty-TimerID record.
			name:    "empty id removes nothing",
			timerID: "",
			assert: func(t *testing.T, s *InstanceState) {
				assert.Len(t, s.Timers, 3, "an empty timer id names no record")
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			s := &InstanceState{Timers: []timerRecord{
				{TimerID: "tm1", Kind: TimerRetry},
				{TimerID: "tm2", Kind: TimerRetry},
				{TimerID: "", Kind: TimerRetry, Token: "tokGhost"},
			}}
			s.removeTimer(tc.timerID)
			tc.assert(t, s)
		})
	}
}
