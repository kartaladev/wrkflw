package engine

// state_arms_test.go — white-box tests for the generic arm lookups. Covers
// ADR-0152: an empty identity key matches no arm, while an empty correlationKey
// keeps its documented "uncorrelated" meaning.

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// armsFixture returns one timer arm, one signal arm, one message arm, and — the
// case that makes an empty key dangerous — one ERROR-boundary-shaped arm whose
// four triggerMatch fields are all empty (see step_boundaries.go:38-70).
func armsFixture() []armedEvent {
	return []armedEvent{
		{GatewayToken: "gw1", CatchNode: "catchTimer", triggerMatch: triggerMatch{TimerID: "tm1"}},
		{GatewayToken: "gw1", CatchNode: "catchSignal", triggerMatch: triggerMatch{Signal: "sig"}},
		{GatewayToken: "gw1", CatchNode: "catchMsg", triggerMatch: triggerMatch{Message: "msg", MessageKey: "k1"}},
		{GatewayToken: "gw1", CatchNode: "catchErr"},
	}
}

func TestArmBySignal(t *testing.T) {
	t.Parallel()

	type testCase struct {
		name   string
		signal string
		assert func(t *testing.T, arm *armedEvent)
	}

	cases := []testCase{
		{
			name:   "returns the signal arm",
			signal: "sig",
			assert: func(t *testing.T, arm *armedEvent) {
				require.NotNil(t, arm)
				assert.Equal(t, "catchSignal", arm.CatchNode)
			},
		},
		{
			name:   "returns nil for an unknown signal",
			signal: "other",
			assert: func(t *testing.T, arm *armedEvent) {
				assert.Nil(t, arm)
			},
		},
		{
			// Before ADR-0152 this returned the TIMER arm, and in production an
			// ERROR-boundary arm — which fireBoundaryArm then uses to interrupt a
			// live host activity.
			name:   "empty signal name matches no arm",
			signal: "",
			assert: func(t *testing.T, arm *armedEvent) {
				assert.Nil(t, arm, "an empty signal name must not match a timer, message, or error arm")
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			tc.assert(t, armBySignal(armsFixture(), tc.signal))
		})
	}
}

func TestArmByTimer(t *testing.T) {
	t.Parallel()

	type testCase struct {
		name    string
		timerID string
		assert  func(t *testing.T, arm *armedEvent)
	}

	cases := []testCase{
		{
			name:    "returns the timer arm",
			timerID: "tm1",
			assert: func(t *testing.T, arm *armedEvent) {
				require.NotNil(t, arm)
				assert.Equal(t, "catchTimer", arm.CatchNode)
			},
		},
		{
			name:    "empty timer id matches no arm",
			timerID: "",
			assert: func(t *testing.T, arm *armedEvent) {
				assert.Nil(t, arm, "an empty timer id must not match a signal, message, or error arm")
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			tc.assert(t, armByTimer(armsFixture(), tc.timerID))
		})
	}
}

func TestArmByMessage(t *testing.T) {
	t.Parallel()

	type testCase struct {
		name    string
		message string
		key     string
		arms    []armedEvent
		assert  func(t *testing.T, arm *armedEvent)
	}

	cases := []testCase{
		{
			name:    "returns the message arm on a matching key",
			message: "msg",
			key:     "k1",
			arms:    armsFixture(),
			assert: func(t *testing.T, arm *armedEvent) {
				require.NotNil(t, arm)
				assert.Equal(t, "catchMsg", arm.CatchNode)
			},
		},
		{
			name:    "returns nil when the key does not match",
			message: "msg",
			key:     "other",
			arms:    armsFixture(),
			assert: func(t *testing.T, arm *armedEvent) {
				assert.Nil(t, arm)
			},
		},
		{
			// EXEMPTION: an empty correlationKey means "uncorrelated" and must keep
			// matching an arm that is itself uncorrelated.
			name:    "empty correlation key still matches an uncorrelated arm",
			message: "msg",
			key:     "",
			arms: []armedEvent{
				{GatewayToken: "gw1", CatchNode: "catchMsg", triggerMatch: triggerMatch{Message: "msg"}},
			},
			assert: func(t *testing.T, arm *armedEvent) {
				require.NotNil(t, arm, "an uncorrelated message must still match an uncorrelated arm")
				assert.Equal(t, "catchMsg", arm.CatchNode)
			},
		},
		{
			name:    "empty message name matches no arm",
			message: "",
			key:     "",
			arms:    armsFixture(),
			assert: func(t *testing.T, arm *armedEvent) {
				assert.Nil(t, arm, "an empty message name must not match a timer or error arm")
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			tc.assert(t, armByMessage(tc.arms, tc.message, tc.key))
		})
	}
}

func TestRemoveArmedEventsForGateway(t *testing.T) {
	t.Parallel()

	type testCase struct {
		name   string
		owner  string
		assert func(t *testing.T, cancelled []string, s *InstanceState)
	}

	cases := []testCase{
		{
			name:  "removes the named gateway's arms",
			owner: "gw1",
			assert: func(t *testing.T, cancelled []string, s *InstanceState) {
				assert.Equal(t, []string{"tm1"}, cancelled)
				require.Len(t, s.ArmedEvents, 1, "the orphan arm must remain")
				assert.Empty(t, s.ArmedEvents[0].GatewayToken)
			},
		},
		{
			// Without the guard this removes the planted empty-GatewayToken arm.
			name:  "empty gateway token removes nothing",
			owner: "",
			assert: func(t *testing.T, cancelled []string, s *InstanceState) {
				assert.Empty(t, cancelled)
				assert.Len(t, s.ArmedEvents, 2, "an empty gateway token names no arm")
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			s := &InstanceState{ArmedEvents: []armedEvent{
				{GatewayToken: "gw1", CatchNode: "catchTimer", triggerMatch: triggerMatch{TimerID: "tm1"}},
				{GatewayToken: "", CatchNode: "orphan", triggerMatch: triggerMatch{TimerID: "tm2"}},
			}}
			tc.assert(t, s.removeArmedEventsForGateway(tc.owner), s)
		})
	}
}

func TestRemoveBoundaryArmsForHost(t *testing.T) {
	t.Parallel()

	type testCase struct {
		name   string
		host   string
		assert func(t *testing.T, cancelled []string, s *InstanceState)
	}

	cases := []testCase{
		{
			name: "removes the named host's arms",
			host: "tokA",
			assert: func(t *testing.T, cancelled []string, s *InstanceState) {
				assert.Equal(t, []string{"tm9"}, cancelled)
				assert.Len(t, s.Boundaries, 1)
			},
		},
		{
			name: "empty host token removes nothing",
			host: "",
			assert: func(t *testing.T, cancelled []string, s *InstanceState) {
				assert.Empty(t, cancelled)
				assert.Len(t, s.Boundaries, 2, "an empty host token names no arm")
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			s := &InstanceState{Boundaries: []boundaryArm{
				{HostToken: "tokA", triggerMatch: triggerMatch{TimerID: "tm9"}},
				{HostToken: "", triggerMatch: triggerMatch{TimerID: "tm10"}},
			}}
			tc.assert(t, s.removeBoundaryArmsForHost(tc.host), s)
		})
	}
}
