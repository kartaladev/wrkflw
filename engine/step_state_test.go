package engine

// step_state_test.go — white-box tests for the token and visit lookup helpers.
// Each empty-key case plants a record holding the empty value.

import (
	"encoding/json"
	"maps"
	"slices"
	"strings"
	"testing"
	"time"
	"unicode"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kartaladev/wrkflw/definition/activity"
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
			// Previously this returned tokSig, the first parked token whose
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
			// Previously this returned every token NOT awaiting a signal —
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

// ─────────────────────────────────────────────────────────────────────────────
// #150 — engine-stamped variable keys vs a case-variant in process variables
// ─────────────────────────────────────────────────────────────────────────────

// TestServiceActionInputReservesEngineStampedKeys is #150's red.
//
// serviceActionInput copies s.Variables wholesale and then stamps
// "_idempotencyKey". A process variable named "_idempotencykey" is a DIFFERENT
// map key, so it is copied through untouched and both reach the action. Once
// there, encoding/json folds case on decode and json.Marshal emits map keys
// byte-sorted — 'K' (0x4b) sorts before 'k' (0x6b) — so the lowercase variant is
// applied last and WINS for an In declaring the field. The action ends up with
// the caller's value where the engine's belongs. That mechanism is characterised
// where it lives, in action/typed_test.go; this test is about the engine never
// handing a case-variant over in the first place.
//
// THREE keys are reserved, not one. s.Variables carries "_errorMessage" and
// "_errorAttempts" too (step_triggers.go, the catch-flow branch), and
// serviceActionInput copies s.Variables wholesale, so they reach a Typed action's
// input by the same route "_idempotencyKey" does.
//
// The predicate is case-insensitively equal to a reserved name AND NOT byte-equal
// to it. The exact names are engine-written and MUST still arrive: stripping them
// would break the catch-flow contract pinned in engine/retry_test.go. Half these
// cases exist to hold that line — an assertion that "the action did not receive
// ATTACKER" is satisfied just as well by a rule that drops every underscore-
// prefixed key, so every refusal below has an at-limit accept beside it.
func TestServiceActionInputReservesEngineStampedKeys(t *testing.T) {
	t.Parallel()

	const (
		instanceID = "inst-1"
		nodeID     = "task"
		engineKey  = instanceID + ":" + nodeID
	)

	type testCase struct {
		name   string
		vars   map[string]any
		assert func(t *testing.T, in map[string]any)
	}

	cases := []testCase{
		{
			name: "REFUSE: a case-variant of the idempotency stamp never reaches the action",
			vars: map[string]any{"_idempotencykey": "ATTACKER"},
			assert: func(t *testing.T, in map[string]any) {
				assert.NotContains(t, in, "_idempotencykey",
					"a process variable case-folding onto the engine's stamp must be dropped "+
						"before the input is handed to the action")
				assert.Equal(t, engineKey, in["_idempotencyKey"],
					"and the engine's own stamp must survive — asserting only the refusal "+
						"would be satisfied by dropping both")
			},
		},
		{
			name: "REFUSE: every case-variant spelling, not just the all-lowercase one",
			vars: map[string]any{
				"_IdempotencyKey": "A1",
				"_IDEMPOTENCYKEY": "A2",
				"_idempotencyKEY": "A3",
			},
			assert: func(t *testing.T, in map[string]any) {
				for _, k := range []string{"_IdempotencyKey", "_IDEMPOTENCYKEY", "_idempotencyKEY"} {
					assert.NotContains(t, in, k, "case folding is not only about the final K")
				}
				assert.Equal(t, engineKey, in["_idempotencyKey"])
			},
		},
		{
			// The reason the predicate is EqualFold and not a case comparison.
			// encoding/json matches by FOLDING, and folding is wider than case:
			// U+212A KELVIN SIGN folds onto "k" and U+017F LONG S onto "s", so
			// both of these bind to the engine's field while looking nothing like
			// a case variant of it.
			name: "REFUSE: a NON-ASCII fold of the stamp, which is not a case variant at all",
			vars: map[string]any{
				"_idempotencyKey": "ATTACKER-KELVIN",
				"_errorMeſsage":   "ATTACKER-LONG-S",
			},
			assert: func(t *testing.T, in map[string]any) {
				assert.NotContains(t, in, "_idempotencyKey",
					"U+212A folds onto k, so this binds to _idempotencyKey")
				assert.NotContains(t, in, "_errorMeſsage",
					"U+017F folds onto s, so this binds to _errorMessage")
				assert.Equal(t, engineKey, in["_idempotencyKey"])
			},
		},
		{
			name: "REFUSE: case-variants of _errorMessage and _errorAttempts too",
			vars: map[string]any{
				"_errormessage":  "ATTACKER",
				"_ERRORATTEMPTS": 99,
			},
			assert: func(t *testing.T, in map[string]any) {
				assert.NotContains(t, in, "_errormessage",
					"serviceActionInput copies s.Variables wholesale, so all three engine-written "+
						"keys reach the action by the same route")
				assert.NotContains(t, in, "_ERRORATTEMPTS")
			},
		},
		{
			name: "ACCEPT at the limit: the EXACT engine-written keys still reach the action",
			vars: map[string]any{
				"_errorMessage":  "boom",
				"_errorAttempts": 3,
			},
			assert: func(t *testing.T, in map[string]any) {
				assert.Equal(t, "boom", in["_errorMessage"],
					"the exact names are engine-written and the catch-flow contract depends on "+
						"them arriving; reserving must drop the VARIANT, never the name")
				assert.Equal(t, 3, in["_errorAttempts"])
			},
		},
		{
			name: "ACCEPT at the limit: an exactly-matching caller _idempotencyKey is overwritten, not dropped",
			vars: map[string]any{"_idempotencyKey": "CALLER"},
			assert: func(t *testing.T, in map[string]any) {
				assert.Equal(t, engineKey, in["_idempotencyKey"],
					"the stamp is written after the copy, so an exact caller key is overwritten "+
						"by the engine's value — which is why a case-variant is the only attack here")
			},
		},
		{
			name: "ACCEPT at the limit: an ordinary process variable is untouched",
			vars: map[string]any{"customerId": "c-1", "amount": 42},
			assert: func(t *testing.T, in map[string]any) {
				assert.Equal(t, "c-1", in["customerId"])
				assert.Equal(t, 42, in["amount"])
			},
		},
		{
			name: "ACCEPT at the limit: an underscore-prefixed key that is NOT a reserved variant binds normally",
			vars: map[string]any{"_traceId": "t-1", "_error": "not a reserved stamp"},
			assert: func(t *testing.T, in map[string]any) {
				assert.Equal(t, "t-1", in["_traceId"],
					"THE discriminator: a rule that drops every underscore-prefixed key would "+
						"satisfy every refusal above and fail here")
				assert.Equal(t, "not a reserved stamp", in["_error"],
					"_error is injected into a clone for ErrorExpr only and is never persisted, "+
						"so it is not one of the reserved names")
			},
		},
		{
			name: "nil variables still produce an input carrying the stamp",
			vars: nil,
			assert: func(t *testing.T, in map[string]any) {
				require.NotNil(t, in)
				assert.Equal(t, engineKey, in["_idempotencyKey"])
				assert.Len(t, in, 1)
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			s := &InstanceState{InstanceID: instanceID, Variables: tc.vars}
			before := maps.Clone(tc.vars)

			in := serviceActionInput(s, activity.NewServiceTask(nodeID, activity.WithTaskAction("a")))

			assert.Equal(t, before, s.Variables,
				"building the input must not mutate the instance's own variables — the "+
					"reservation happens on the copy, and the instance keeps whatever the "+
					"process put there")
			tc.assert(t, in)
		})
	}
}

// foldVariants returns every single-rune substitution of name that
// encoding/json's foldName should still treat as equal to it, DERIVED by walking
// unicode.SimpleFold rather than written out.
//
// Deriving it is the point. An earlier version hardcoded "k"->U+212A and
// "s"->U+017F, which happens to be complete for today's three names and would
// silently give ZERO fold coverage to a fourth reserved name containing neither.
func foldVariants(name string) []string {
	var out []string
	rs := []rune(name)
	for i, r := range rs {
		for f := unicode.SimpleFold(r); f != r; f = unicode.SimpleFold(f) {
			v := slices.Clone(rs)
			v[i] = f
			out = append(out, string(v))
		}
	}
	return out
}

// asciiVariants returns the exact name plus ordinary case and near-miss forms.
// The near-misses must NOT fold, so they probe the boundary from the other side.
func asciiVariants(name string) []string {
	return []string{
		name,
		strings.ToLower(name),
		strings.ToUpper(name),
		name + "x",
		strings.TrimSuffix(name, "e") + "3",
	}
}

// TestReservedNameFoldMatchesEncodingJSON guards foldsOntoReservedName's
// load-bearing assumption: that strings.EqualFold decides the same question
// encoding/json's decoder decides when it matches an object key to a struct
// field.
//
// The two are NOT the same code. encoding/json matches through
// fields.byFoldedName[string(foldName(key))]; fold.go states the invariant on
// foldName — "foldName returns a folded string such that foldName(x) ==
// foldName(y) is identical to bytes.EqualFold(x, y)" — and implements it by
// folding each rune to the smallest member of its unicode.SimpleFold set.
//
// WHAT THIS TEST IS FOR, stated precisely because it is easy to over-read. It is
// NOT what establishes the equivalence: that was settled by an exhaustive sweep
// over every single-rune substitution at every position for all three names
// across the whole Unicode range, which found zero divergences in either
// direction. This test's job is to FAIL IF THE PREDICATE LATER CHANGES — a
// regression guard over a property already established, not the establishment of
// it.
//
// ⚠ It also pins a CONDITION. fold.go carries //go:build !goexperiment.jsonv2,
// so the invariant is stated for the path that ships. If the predicate and the
// decoder ever diverge — a toolchain change, a build tag, a rewritten matcher —
// the fix develops a hole in EXACTLY the direction it exists to close, and every
// other test here would stay green because they all use keys the two agree
// about.
func TestReservedNameFoldMatchesEncodingJSON(t *testing.T) {
	t.Parallel()

	// The tags are the reserved names, so the decoder is asked the same question
	// foldsOntoReservedName is.
	type reservedIn struct {
		IdempotencyKey string `json:"_idempotencyKey"`
		ErrorMessage   string `json:"_errorMessage"`
		ErrorAttempts  string `json:"_errorAttempts"`
	}
	field := map[string]func(reservedIn) string{
		"_idempotencyKey": func(v reservedIn) string { return v.IdempotencyKey },
		"_errorMessage":   func(v reservedIn) string { return v.ErrorMessage },
		"_errorAttempts":  func(v reservedIn) string { return v.ErrorAttempts },
	}

	compare := func(t *testing.T, reserved, k string) {
		t.Helper()
		raw, err := json.Marshal(map[string]any{k: "BOUND"})
		require.NoError(t, err)

		var got reservedIn
		require.NoError(t, json.Unmarshal(raw, &got))

		decoderBinds := field[reserved](got) == "BOUND"
		predicateSays := k == reserved || foldsOntoReservedName(k)

		require.Equal(t, decoderBinds, predicateSays,
			"strings.EqualFold and encoding/json disagree about key %q against "+
				"reserved name %q: the decoder binds it = %v, the reserve predicate "+
				"claims it = %v. The fix's coverage is defined by whatever the "+
				"DECODER folds, so a disagreement is a hole in serviceActionInput",
			k, reserved, decoderBinds, predicateSays)
	}

	// The anti-collapse floor is DERIVED from the same function that generates the
	// candidates, so it cannot be satisfied by a candidate set that lost its fold
	// coverage. An earlier version compared against len(names)*5, which was
	// exactly the ASCII-only count: deleting every fold candidate left it green.
	wantFold := 0
	for _, reserved := range reservedEngineVarNames {
		wantFold += len(foldVariants(reserved))
	}
	require.Positive(t, wantFold,
		"the reserved names yield no fold variants at all; this guard would be "+
			"comparing only keys that differ in plain ASCII case")

	var foldChecked, asciiChecked int
	for _, reserved := range reservedEngineVarNames {
		for _, k := range asciiVariants(reserved) {
			compare(t, reserved, k)
			asciiChecked++
		}
		for _, k := range foldVariants(reserved) {
			compare(t, reserved, k)
			foldChecked++
		}
	}

	assert.Equal(t, wantFold, foldChecked,
		"every derived fold variant must actually have been compared")
	t.Logf("compared %d keys across %d reserved names (%d ASCII, %d derived fold variants)",
		asciiChecked+foldChecked, len(reservedEngineVarNames), asciiChecked, foldChecked)
}
