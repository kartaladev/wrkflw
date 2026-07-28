package engine

// step_cancel_test.go — white-box tests for cancelTokenWaits.
//
// ADR-0152 regression: cancelTokenWaits sweeps a token's waits by task key
// (tok.AwaitCommand). A token parked on a signal/message, or simply active, has an
// empty AwaitCommand. Because TimerRetry records carry no TaskID, an unguarded sweep
// matched every retry timer in the INSTANCE — including retries owned by tokens in
// sibling scopes that were not being cancelled. Those tokens then sat in TokenWaiting
// forever with their timer cancelled in the scheduler: the instance neither completed
// nor failed.

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCancelTokenWaitsLeavesSiblingScopeRetriesIntact(t *testing.T) {
	t.Parallel()

	at := time.Unix(0, 0).UTC()

	// tokSwept is being cancelled: it is parked on a MESSAGE, so its AwaitCommand
	// is empty — the exact shape that triggered the bug.
	// tokSibling lives in a DIFFERENT scope, is mid-retry, and must survive.
	s := &InstanceState{
		Status: StatusRunning,
		Tokens: []Token{
			{ID: "tokSwept", State: TokenWaiting, NodeID: "recv", ScopeID: "scopeA", AwaitMessage: "msg"},
			{ID: "tokSibling", State: TokenWaiting, NodeID: "svcB", ScopeID: "scopeB", AwaitCommand: "tmRetry"},
		},
		Timers: []timerRecord{
			{TimerID: "tmRetry", Kind: TimerRetry, Token: "tokSibling", NodeID: "svcB", ScopeID: "scopeB"},
		},
		History: []NodeVisit{
			{TokenID: "tokSwept", NodeID: "recv", EnteredAt: at},
			{TokenID: "tokSibling", NodeID: "svcB", EnteredAt: at},
		},
	}

	swept := s.Tokens[0]
	cmds := cancelTokenWaits(s, &swept, at, CloseKindBoundaryInterrupted)

	// The sibling's retry timer must survive in state...
	require.Len(t, s.Timers, 1, "the sibling scope's retry timer must not be swept")
	assert.Equal(t, "tmRetry", s.Timers[0].TimerID)

	// ...and the sweep must emit nothing at all: the swept token owns no waits of
	// its own, so every one of the three sweeps inside cancelTokenWaits is empty.
	assert.Empty(t, cmds, "a swept token with no waits of its own emits no commands")

	// The swept token itself is consumed, and the sibling still exists.
	assert.Nil(t, s.tokenByID("tokSwept"), "the swept token is consumed")
	require.NotNil(t, s.tokenByID("tokSibling"), "the sibling token must survive")
	assert.Equal(t, TokenWaiting, s.tokenByID("tokSibling").State)
}
