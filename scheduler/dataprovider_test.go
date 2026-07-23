package scheduler_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kartaladev/wrkflw/scheduler"
)

// TestNewStaticDataProvider folds all cases into one assert-closure body
// rather than a varying `input` field: each case's mutation-isolation setup
// is structurally different (mutate the constructor input vs. mutate a
// returned Get result), so the divergence lives inside the closure per the
// table-test skill's guidance for structurally-different setups.
func TestNewStaticDataProvider(t *testing.T) {
	t.Parallel()

	type testCase struct {
		name   string
		assert func(t *testing.T)
	}

	cases := []testCase{
		{
			name: "mutating the input map after construction does not leak into Get",
			assert: func(t *testing.T) {
				input := map[string]any{"count": 1}
				p := scheduler.NewStaticDataProvider(input)

				input["count"] = 999
				input["extra"] = "leaked"

				got, err := p.Get(t.Context())
				require.NoError(t, err)
				assert.Equal(t, map[string]any{"count": 1}, got)
			},
		},
		{
			name: "mutating a returned map does not leak into a later Get",
			assert: func(t *testing.T) {
				p := scheduler.NewStaticDataProvider(map[string]any{"count": 1})

				first, err := p.Get(t.Context())
				require.NoError(t, err)
				first["count"] = 999
				first["extra"] = "leaked"

				second, err := p.Get(t.Context())
				require.NoError(t, err)
				assert.Equal(t, map[string]any{"count": 1}, second)
			},
		},
		{
			name: "Static reports true",
			assert: func(t *testing.T) {
				p := scheduler.NewStaticDataProvider(map[string]any{"count": 1})
				assert.True(t, p.Static())
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			tc.assert(t)
		})
	}
}

func TestNewEmptyDataProvider(t *testing.T) {
	t.Parallel()

	p := scheduler.NewEmptyDataProvider()

	got, err := p.Get(t.Context())
	require.NoError(t, err)
	assert.NotNil(t, got)
	assert.Empty(t, got)
	assert.True(t, p.Static())
}
