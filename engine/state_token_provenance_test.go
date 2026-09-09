package engine_test

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kartaladev/wrkflw/definition/model"
	"github.com/kartaladev/wrkflw/engine"
)

// goldenLegacyTokenJSON is the exact json.Marshal output of a fully-populated
// engine.Token as the struct stood BEFORE ArrivalFlowID was added — i.e. what
// every snapshot row written by a pre-#120 binary looks like. InstanceState is
// JSON-encoded whole by internal/persistence/store's marshalSnapshot with no
// struct tags, so these are the durable key names, not an invention of this test.
const goldenLegacyTokenJSON = `{"ID":"t1","NodeID":"join","ScopeID":"s1","State":2,` +
	`"AwaitCommand":"c1","AwaitSignal":"sig","AwaitMessage":"msg","AwaitMessageKey":"k",` +
	`"AwaitTimer":"tm1","Payload":{"x":1},"EnteredAt":"2026-06-20T10:00:00Z",` +
	`"RetryAttempts":2,"RetryStartedAt":"2026-06-20T09:00:00Z"}`

// TestTokenArrivalFlowIDIsAdditiveOnTheWire pins that appending ArrivalFlowID did
// not change how any previously-written token decodes.
//
// This is the half of the change no engine-level test reaches: an instance that
// was mid-flight when the binary was upgraded is read back through plain
// json.Unmarshal (internal/persistence/store/store_core.go), and there is no
// DisallowUnknownFields anywhere on that path in either direction.
func TestTokenArrivalFlowIDIsAdditiveOnTheWire(t *testing.T) {
	t.Parallel()

	type testCase struct {
		name   string
		encode func(t *testing.T) []byte
		assert func(t *testing.T, got engine.Token, err error)
	}

	cases := []testCase{
		{
			name: "a token written before the field existed decodes with empty provenance",
			encode: func(_ *testing.T) []byte {
				return []byte(goldenLegacyTokenJSON)
			},
			assert: func(t *testing.T, got engine.Token, err error) {
				require.NoError(t, err)
				assert.Empty(t, got.ArrivalFlowID,
					"a legacy row carries no provenance; it must decode empty, not fail")
				// Every pre-existing field must still land where it did, or the
				// append moved something it should not have.
				assert.Equal(t, "t1", got.ID)
				assert.Equal(t, "join", got.NodeID)
				assert.Equal(t, "s1", got.ScopeID)
				assert.Equal(t, engine.TokenJoining, got.State)
				assert.Equal(t, "tm1", got.AwaitTimer)
				assert.Equal(t, 2, got.RetryAttempts)
			},
		},
		{
			name: "a token written by this binary round-trips its provenance",
			encode: func(t *testing.T) []byte {
				b, err := json.Marshal(engine.Token{
					ID: "t1", NodeID: "join", ScopeID: "s1", State: engine.TokenJoining,
					EnteredAt: time.Date(2026, 6, 20, 10, 0, 0, 0, time.UTC),
					// A live snapshot may hold a provenance whose flow is not the
					// join's — the field is rewritten on every hop, so it always
					// names the LAST edge crossed.
					ArrivalFlowID: "f5",
				})
				require.NoError(t, err)
				return b
			},
			assert: func(t *testing.T, got engine.Token, err error) {
				require.NoError(t, err)
				assert.Equal(t, "f5", got.ArrivalFlowID)
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			var got engine.Token
			err := json.Unmarshal(tc.encode(t), &got)
			tc.assert(t, got, err)
		})
	}
}

// TestParallelJoinAcceptsLegacyTokensWithoutProvenance pins the meaning of an
// EMPTY ArrivalFlowID at a converging parallel gateway: it satisfies at most one
// otherwise-unsatisfied incoming flow.
//
// The rule exists for instances that were already running when the field was
// added — their parked tokens have no provenance and must not deadlock — and it
// covers the other flow-free populations too (an instance start, a sub-process
// start in a fresh child scope, a compensation-walk relocation). "At most one"
// is the load-bearing half: consuming an empty-provenance token per flow would
// let a single unknown token satisfy an entire join.
//
// Each case is a hand-built InstanceState standing in for a snapshot decoded
// mid-flight, driven through the same engine.Step call.
func TestParallelJoinAcceptsLegacyTokensWithoutProvenance(t *testing.T) {
	t.Parallel()

	at := time.Date(2026, 9, 10, 10, 0, 0, 0, time.UTC)

	// legacyState is a diamondDef instance upgraded mid-flight: branch "a" has
	// already reached the join (its token predates the field, so no provenance),
	// and branch "b" is still parked on its action command.
	legacyState := func(parked []engine.Token) engine.InstanceState {
		return engine.InstanceState{
			InstanceID: "i1",
			Status:     engine.StatusRunning,
			TokenSeq:   len(parked) + 1,
			CmdSeq:     1,
			Tokens: append(append([]engine.Token{}, parked...), engine.Token{
				ID: "t-b", NodeID: "b", State: engine.TokenWaiting,
				AwaitCommand: "i1-c1", EnteredAt: at,
			}),
			History: []engine.NodeVisit{
				{NodeID: "b", TokenID: "t-b", EnteredAt: at},
			},
		}
	}

	type testCase struct {
		name   string
		state  engine.InstanceState
		assert func(t *testing.T, res engine.StepResult, err error)
	}

	cases := []testCase{
		{
			name: "a legacy token with no provenance satisfies the flow the arriving token did not",
			state: legacyState([]engine.Token{
				{ID: "t-a", NodeID: "join", State: engine.TokenJoining, EnteredAt: at},
			}),
			assert: func(t *testing.T, res engine.StepResult, err error) {
				require.NoError(t, err)
				// Branch b arrives over f5 and covers it; the provenance-less token
				// is then spent on f4. Deadlocking here would strand every instance
				// that was mid-join at upgrade time.
				assert.Equal(t, engine.StatusCompleted, res.State.Status,
					"an upgraded instance must still be able to complete its join")
				assert.Empty(t, res.State.Tokens)
			},
		},
		{
			name: "two provenance-less tokens satisfy one flow each, exactly as arrival counting did",
			state: legacyState([]engine.Token{
				{ID: "t-a", NodeID: "join", State: engine.TokenJoining, EnteredAt: at},
				{ID: "t-x", NodeID: "join", State: engine.TokenJoining, EnteredAt: at},
			}),
			assert: func(t *testing.T, res engine.StepResult, err error) {
				require.NoError(t, err)
				// Both flows are covered by the two unknown tokens before branch b
				// even arrives, so the join has already fired by the time it does —
				// and b's own token is the surplus left parked at the join.
				assert.True(t, visited(res.State, "end"))
				surplus := tokensAt(res.State, "join")
				assert.Len(t, surplus, 1,
					"only one token per incoming flow is consumed, whatever its provenance")
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			def := diamondDef()
			require.NoError(t, model.Validate(def))
			require.Len(t, def.Incoming("join"), 2, "fixture shape guard")

			res, err := engine.Step(t.Context(), def, tc.state,
				engine.NewActionCompleted(at.Add(time.Second), "i1-c1", nil), engine.StepOptions{})
			tc.assert(t, res, err)
		})
	}
}
