package engine

// step_state_test.go — white-box tests for the token and visit lookup helpers.
// Covers ADR-0152. Each empty-key case plants a record holding the empty value.

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// parkedTokens returns one command-parked token, one signal-parked token, one
// message-parked token, and one plain active token whose Await* fields are empty.
func parkedTokens() []Token {
	return []Token{
		{ID: "tokCmd", State: TokenWaiting, NodeID: "nCmd", AwaitCommand: "c1"},
		{ID: "tokSig", State: TokenWaiting, NodeID: "nSig", AwaitSignal: "sig"},
		{ID: "tokMsg", State: TokenWaiting, NodeID: "nMsg", AwaitMessage: "msg", AwaitMessageKey: "k1"},
		{ID: "tokActive", State: TokenActive, NodeID: "nActive", ScopeID: "sc1"},
	}
}

func TestTokenAwaiting(t *testing.T) {
	t.Parallel()

	type testCase struct {
		name   string
		cmdID  string
		assert func(t *testing.T, tok *Token)
	}

	cases := []testCase{
		{
			name:  "returns the token parked on the command",
			cmdID: "c1",
			assert: func(t *testing.T, tok *Token) {
				require.NotNil(t, tok)
				assert.Equal(t, "tokCmd", tok.ID)
			},
		},
		{
			// Before ADR-0152 this returned tokSig, the first parked token whose
			// AwaitCommand is "" — a signal-parked token, not a command one.
			name:  "empty command id matches no token",
			cmdID: "",
			assert: func(t *testing.T, tok *Token) {
				assert.Nil(t, tok, "an empty command id must not match an unparked token")
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			s := &InstanceState{Tokens: parkedTokens()}
			tc.assert(t, s.tokenAwaiting(tc.cmdID))
		})
	}
}

func TestTokenIDsAwaitingSignal(t *testing.T) {
	t.Parallel()

	type testCase struct {
		name   string
		signal string
		assert func(t *testing.T, ids []string)
	}

	cases := []testCase{
		{
			name:   "returns tokens awaiting the signal",
			signal: "sig",
			assert: func(t *testing.T, ids []string) {
				assert.Equal(t, []string{"tokSig"}, ids)
			},
		},
		{
			// Before ADR-0152 this returned every token NOT awaiting a signal —
			// a SignalReceived{Name: ""} resumed them all.
			name:   "empty signal name matches no token",
			signal: "",
			assert: func(t *testing.T, ids []string) {
				assert.Empty(t, ids, "an empty signal name must not broadcast to unparked tokens")
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			s := &InstanceState{Tokens: parkedTokens()}
			tc.assert(t, s.tokenIDsAwaitingSignal(tc.signal))
		})
	}
}

func TestTokenAwaitingMessage(t *testing.T) {
	t.Parallel()

	type testCase struct {
		name    string
		message string
		key     string
		tokens  []Token
		assert  func(t *testing.T, tok *Token)
	}

	cases := []testCase{
		{
			name:    "returns the token on a matching name and key",
			message: "msg",
			key:     "k1",
			tokens:  parkedTokens(),
			assert: func(t *testing.T, tok *Token) {
				require.NotNil(t, tok)
				assert.Equal(t, "tokMsg", tok.ID)
			},
		},
		{
			// EXEMPTION: an empty correlationKey means "uncorrelated".
			name:    "empty correlation key still matches an uncorrelated token",
			message: "msg",
			key:     "",
			tokens: []Token{
				{ID: "tokPlain", State: TokenWaiting, AwaitMessage: "msg"},
			},
			assert: func(t *testing.T, tok *Token) {
				require.NotNil(t, tok, "an uncorrelated message must still match an uncorrelated token")
				assert.Equal(t, "tokPlain", tok.ID)
			},
		},
		{
			name:    "empty message name matches no token",
			message: "",
			key:     "",
			tokens:  parkedTokens(),
			assert: func(t *testing.T, tok *Token) {
				assert.Nil(t, tok, "an empty message name must not match an unparked token")
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			s := &InstanceState{Tokens: tc.tokens}
			tc.assert(t, s.tokenAwaitingMessage(tc.message, tc.key))
		})
	}
}

func TestTokenByIDAndRemoveToken(t *testing.T) {
	t.Parallel()

	type testCase struct {
		name    string
		tokenID string
		tokens  []Token
		assert  func(t *testing.T, tok *Token, afterRemove int)
	}

	// ghostTokens plants a token WITH an empty ID so the empty-key case is real.
	ghostTokens := func() []Token {
		return append(parkedTokens(), Token{ID: "", State: TokenActive, NodeID: "nGhost"})
	}

	cases := []testCase{
		{
			name:    "finds and removes the named token",
			tokenID: "tokCmd",
			tokens:  ghostTokens(),
			assert: func(t *testing.T, tok *Token, afterRemove int) {
				require.NotNil(t, tok)
				assert.Equal(t, "tokCmd", tok.ID)
				assert.Equal(t, 4, afterRemove)
			},
		},
		{
			name:    "empty token id finds and removes nothing",
			tokenID: "",
			tokens:  ghostTokens(),
			assert: func(t *testing.T, tok *Token, afterRemove int) {
				assert.Nil(t, tok, "an empty token id names no token")
				assert.Equal(t, 5, afterRemove, "an empty token id must remove nothing")
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			s := &InstanceState{Tokens: tc.tokens}
			tok := s.tokenByID(tc.tokenID)
			s.removeToken(tc.tokenID)
			tc.assert(t, tok, len(s.Tokens))
		})
	}
}

func TestOpenVisitFor(t *testing.T) {
	t.Parallel()

	type testCase struct {
		name    string
		tokenID string
		nodeID  string
		assert  func(t *testing.T, v *NodeVisit)
	}

	cases := []testCase{
		{
			name:    "returns the open visit for the pair",
			tokenID: "tokA",
			nodeID:  "n1",
			assert: func(t *testing.T, v *NodeVisit) {
				require.NotNil(t, v)
				assert.Equal(t, "n1", v.NodeID)
			},
		},
		{
			name:    "empty token id matches no visit",
			tokenID: "",
			nodeID:  "n1",
			assert: func(t *testing.T, v *NodeVisit) {
				assert.Nil(t, v)
			},
		},
		{
			name:    "empty node id matches no visit",
			tokenID: "tokA",
			nodeID:  "",
			assert: func(t *testing.T, v *NodeVisit) {
				assert.Nil(t, v)
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			// Plant visits holding the empty value on each component.
			s := &InstanceState{History: []NodeVisit{
				{TokenID: "tokA", NodeID: "n1", EnteredAt: time.Unix(0, 0).UTC()},
				{TokenID: "", NodeID: "n1", EnteredAt: time.Unix(0, 0).UTC()},
				{TokenID: "tokA", NodeID: "", EnteredAt: time.Unix(0, 0).UTC()},
			}}
			tc.assert(t, s.openVisitFor(tc.tokenID, tc.nodeID))
		})
	}
}
