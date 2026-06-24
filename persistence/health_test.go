package persistence_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zakyalvan/krtlwrkflw/internal/database"
	"github.com/zakyalvan/krtlwrkflw/persistence"
	rest "github.com/zakyalvan/krtlwrkflw/transport/rest"
)

func TestPingCheck(t *testing.T) {
	t.Parallel()

	pool := database.RunTestDatabase(t)

	type testCase struct {
		name   string
		check  func() persistence.PingCheck
		ctx    func(ctx context.Context) context.Context // nil means identity
		assert func(t *testing.T, err error)
	}

	cases := []testCase{
		{
			name:  "healthy pool pings ok",
			check: func() persistence.PingCheck { return persistence.NewPingCheck(pool) },
			assert: func(t *testing.T, err error) {
				require.NoError(t, err)
			},
		},
		{
			name: "custom name is used",
			check: func() persistence.PingCheck {
				return persistence.NewPingCheck(pool, persistence.WithPingName("primary-db"))
			},
			assert: func(t *testing.T, err error) {
				require.NoError(t, err)
			},
		},
		{
			name:  "canceled context fails the ping",
			check: func() persistence.PingCheck { return persistence.NewPingCheck(pool) },
			ctx: func(ctx context.Context) context.Context {
				cctx, cancel := context.WithCancel(ctx)
				cancel()
				return cctx
			},
			assert: func(t *testing.T, err error) {
				require.Error(t, err)
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			ctx := t.Context()
			if tc.ctx != nil {
				ctx = tc.ctx(ctx)
			}

			check := tc.check()
			// PingCheck must satisfy the rest.HealthCheck contract so it can be
			// registered with rest.NewHealthHandler.
			var _ rest.HealthCheck = check
			assert.NotEmpty(t, check.Name())

			tc.assert(t, check.Check(ctx))
		})
	}
}

// TestPingCheckDefaultName asserts the default probe name without needing a DB.
func TestPingCheckDefaultName(t *testing.T) {
	t.Parallel()

	check := persistence.NewPingCheck(nil)
	assert.Equal(t, "postgres", check.Name())
}

// TestPingCheckNilPool asserts a nil pool fails the probe (no DB needed).
func TestPingCheckNilPool(t *testing.T) {
	t.Parallel()

	check := persistence.NewPingCheck(nil)
	err := check.Check(t.Context())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "nil pool")
}
