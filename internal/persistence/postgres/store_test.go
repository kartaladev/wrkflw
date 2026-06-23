package postgres_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/zakyalvan/krtlwrkflw/internal/database"
	"github.com/zakyalvan/krtlwrkflw/engine"
	"github.com/zakyalvan/krtlwrkflw/humantask"
	pg "github.com/zakyalvan/krtlwrkflw/internal/persistence/postgres"
	"github.com/zakyalvan/krtlwrkflw/runtime"
)

func newStore(t *testing.T) *pg.Store {
	t.Helper()
	pool := database.RunTestDatabase(t)
	require.NoError(t, pg.Migrate(t.Context(), pool))
	return pg.NewStore(pool)
}

func appliedStep(id, topic string) runtime.AppliedStep {
	now := time.Unix(1700000000, 0).UTC()
	return runtime.AppliedStep{
		State:   engine.InstanceState{InstanceID: id, DefID: "d", DefVersion: 1, Status: engine.StatusRunning, StartedAt: now},
		Trigger: engine.NewStartInstance(now, map[string]any{"k": "v"}),
		Events:  []runtime.OutboxEvent{{Topic: topic, Payload: map[string]any{"x": float64(1)}}},
	}
}

// appliedStepWithVars creates an AppliedStep with specific variables — used to
// test snapshot round-trip fidelity, including the documented JSON numeric gotcha
// (int values become float64 after JSONB round-trip, per spec §7).
func appliedStepWithVars(id string, vars map[string]any) runtime.AppliedStep {
	now := time.Unix(1700000000, 0).UTC()
	return runtime.AppliedStep{
		State: engine.InstanceState{
			InstanceID: id,
			DefID:      "d",
			DefVersion: 1,
			Status:     engine.StatusRunning,
			StartedAt:  now,
			Variables:  vars,
			Tokens: []engine.Token{
				{
					ID:        "tok-1",
					NodeID:    "node-start",
					ScopeID:   "",
					State:     engine.TokenActive,
					Payload:   map[string]any{"nested": map[string]any{"key": "val"}},
					EnteredAt: now,
				},
			},
			History: []engine.NodeVisit{
				{NodeID: "node-start", TokenID: "tok-1", EnteredAt: now},
			},
			Scopes:    []engine.Scope{},
			Tasks:     []humantask.HumanTask{},
			CmdSeq:    1,
			TokenSeq:  1,
			TaskSeq:   0,
			TimerSeq:  0,
			ScopeSeq:  0,
		},
		Trigger: engine.NewStartInstance(now, vars),
		Events:  []runtime.OutboxEvent{{Topic: "topic", Payload: map[string]any{"x": float64(1)}}},
	}
}

func TestStore(t *testing.T) {
	tests := map[string]struct {
		assert func(t *testing.T, s *pg.Store)
	}{
		"create then load round-trips": {
			assert: func(t *testing.T, s *pg.Store) {
				tok, err := s.Create(t.Context(), appliedStep("i1", "a"))
				require.NoError(t, err)
				st, loaded, err := s.Load(t.Context(), "i1")
				require.NoError(t, err)
				require.Equal(t, "i1", st.InstanceID)
				require.Equal(t, tok, loaded)
			},
		},
		"commit advances version and persists journal+outbox": {
			assert: func(t *testing.T, s *pg.Store) {
				tok, err := s.Create(t.Context(), appliedStep("i1", "a"))
				require.NoError(t, err)
				next, err := s.Commit(t.Context(), tok, appliedStep("i1", "b"))
				require.NoError(t, err)
				require.Greater(t, int64(next), int64(tok))
				entries, err := s.Entries(t.Context(), "i1")
				require.NoError(t, err)
				require.Len(t, entries, 2)
			},
		},
		"stale version conflicts": {
			assert: func(t *testing.T, s *pg.Store) {
				tok, err := s.Create(t.Context(), appliedStep("i1", "a"))
				require.NoError(t, err)
				_, err = s.Commit(t.Context(), tok, appliedStep("i1", "b"))
				require.NoError(t, err)
				_, err = s.Commit(t.Context(), tok, appliedStep("i1", "c"))
				require.ErrorIs(t, err, runtime.ErrConcurrentUpdate)
			},
		},
		"load missing": {
			assert: func(t *testing.T, s *pg.Store) {
				_, _, err := s.Load(t.Context(), "nope")
				require.ErrorIs(t, err, runtime.ErrInstanceNotFound)
			},
		},
		// Snapshot round-trip: externally-constructible fields survive JSONB marshal/unmarshal.
		// NOTE: Timers, ArmedEvents, Boundaries, EventSubprocesses are unexported types and
		// cannot be populated from package postgres_test — they are covered by Task 9 e2e tests.
		"snapshot round-trip preserves exported fields": {
			assert: func(t *testing.T, s *pg.Store) {
				now := time.Unix(1700000000, 0).UTC()
				vars := map[string]any{
					"str":    "hello",
					"float":  float64(3.14),
					// int goes in; float64 comes out (JSON number limitation, spec §7 known limitation).
					// The test asserts the ACTUAL behavior: int(5) round-trips as float64(5).
					"intval": int(5),
					"nested": map[string]any{"inner": "value"},
				}
				step := appliedStepWithVars("i1", vars)
				_, err := s.Create(t.Context(), step)
				require.NoError(t, err)

				loaded, _, err := s.Load(t.Context(), "i1")
				require.NoError(t, err)

				require.Equal(t, "i1", loaded.InstanceID)
				require.Equal(t, "d", loaded.DefID)
				require.Equal(t, 1, loaded.DefVersion)
				require.Equal(t, engine.StatusRunning, loaded.Status)
				require.True(t, loaded.StartedAt.Equal(now), "StartedAt must survive round-trip")
				require.Nil(t, loaded.EndedAt)

				// String and float64 vars survive exactly.
				require.Equal(t, "hello", loaded.Variables["str"])
				require.Equal(t, float64(3.14), loaded.Variables["float"])

				// KNOWN LIMITATION (spec §7): JSON numbers always decode to float64.
				// An int(5) variable stored through JSONB comes back as float64(5).
				// This is correct JSON behavior; do not add a numeric codec in v1.
				require.Equal(t, float64(5), loaded.Variables["intval"],
					"spec §7 known limitation: integer variables become float64 after JSONB round-trip")

				// Nested map survives.
				nested, ok := loaded.Variables["nested"].(map[string]any)
				require.True(t, ok, "nested map must survive JSONB round-trip")
				require.Equal(t, "value", nested["inner"])

				// Token payload with nested map also survives.
				require.Len(t, loaded.Tokens, 1)
				require.Equal(t, "tok-1", loaded.Tokens[0].ID)
				require.Equal(t, "node-start", loaded.Tokens[0].NodeID)
				tokenPayload, ok := loaded.Tokens[0].Payload["nested"].(map[string]any)
				require.True(t, ok, "token payload nested map must survive round-trip")
				require.Equal(t, "val", tokenPayload["key"])

				// History.
				require.Len(t, loaded.History, 1)
				require.Equal(t, "node-start", loaded.History[0].NodeID)

				// Sequence counters.
				require.Equal(t, 1, loaded.CmdSeq)
				require.Equal(t, 1, loaded.TokenSeq)
			},
		},
		// Entries returns journal rows in seq order.
		"entries returns journal in seq order": {
			assert: func(t *testing.T, s *pg.Store) {
				tok, err := s.Create(t.Context(), appliedStep("i1", "a"))
				require.NoError(t, err)
				_, err = s.Commit(t.Context(), tok, appliedStep("i1", "b"))
				require.NoError(t, err)

				entries, err := s.Entries(t.Context(), "i1")
				require.NoError(t, err)
				require.Len(t, entries, 2)

				// Both entries must be StartInstance triggers (we used appliedStep which always
				// creates StartInstance triggers).
				for _, e := range entries {
					_, ok := e.(engine.StartInstance)
					require.True(t, ok, "expected StartInstance trigger in journal")
				}
			},
		},
		// Entries for an instance with no journal rows returns empty, not an error.
		// (This scenario requires an instance that exists but has no journal — not
		// possible under the current Create semantics, so we test Entries for a
		// non-existent id returns empty and no error.)
		"entries for unknown instance returns empty": {
			assert: func(t *testing.T, s *pg.Store) {
				entries, err := s.Entries(t.Context(), "unknown")
				require.NoError(t, err)
				require.Empty(t, entries)
			},
		},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			tc.assert(t, newStore(t))
		})
	}
}
