package store_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kartaladev/wrkflw/engine"
	"github.com/kartaladev/wrkflw/internal/persistence/store"
)

// TestMarshalTrigger covers the nil-Trigger guard (audit item 125).
//
// MarshalTrigger's godoc promises that an unhandled variant "returns a
// descriptive error rather than silently producing an empty payload". That held
// for an *unknown* variant — the type switch's default arm — but not for a *nil*
// one: `env := triggerEnvelope{At: t.OccurredAt()}` calls a method on the nil
// interface one line ABOVE the switch, so the default arm was unreachable.
//
// ⚠ What makes the nil case fail today is a **panic (SIGSEGV), not a red
// assertion line**. That is a valid RED, but the failure renders as a goroutine
// stack trace.
//
// The assertion is deliberately require.Error, NOT require.Panics: asserting the
// panic would pin the broken behaviour in place forever.
//
// This function is a pure codec — it needs no database and starts no container.
func TestMarshalTrigger(t *testing.T) {
	t.Parallel()

	occurred := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)

	type testCase struct {
		name    string
		trigger engine.Trigger
		assert  func(t *testing.T, data []byte, kind string, err error)
	}

	cases := []testCase{
		{
			name:    "nil trigger returns an error instead of panicking",
			trigger: nil,
			assert: func(t *testing.T, data []byte, kind string, err error) {
				require.Error(t, err, "a nil trigger must return an error, not panic")
				assert.Contains(t, err.Error(), "nil trigger",
					"the error must say what was wrong: %v", err)
				assert.Nil(t, data, "no payload may be produced for a nil trigger")
				assert.Empty(t, kind, "no kind discriminator may be produced for a nil trigger")
			},
		},
		{
			// Control: without it, the guard could be written to reject
			// everything and the nil case would still pass.
			name:    "a valid trigger still marshals",
			trigger: engine.NewStartInstance(occurred, map[string]any{"k": "v"}),
			assert: func(t *testing.T, data []byte, kind string, err error) {
				require.NoError(t, err)
				assert.Equal(t, "start_instance", kind)
				assert.NotEmpty(t, data)
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			data, kind, err := store.MarshalTrigger(tc.trigger)
			tc.assert(t, data, kind, err)
		})
	}
}
