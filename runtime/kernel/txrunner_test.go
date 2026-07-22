package kernel_test

import (
	"context"
	"errors"
	"testing"

	"github.com/kartaladev/wrkflw/runtime/internal/runtimetest"
	"github.com/kartaladev/wrkflw/runtime/kernel"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Compile-time check: MemInstanceStore satisfies the TxRunner capability.
var _ kernel.TxRunner = (*kernel.MemInstanceStore)(nil)

// errBoom is a sentinel injected error distinct from any package sentinel, so
// assertions can tell "fn's own error propagated" apart from a wrapped one.
var errBoom = errors.New("boom")

// TestMemInstanceStoreRunInTx covers ADR-0134's sequencing-only Mem contract:
// RunInTx is exactly `fn(ctx)` — no rollback. A Create performed by fn stays
// applied even when fn returns an error afterwards (mem has no undo).
func TestMemInstanceStoreRunInTx(t *testing.T) {
	t.Parallel()

	type testCase struct {
		name   string
		fn     func(ms *kernel.MemInstanceStore) func(ctx context.Context) error
		assert func(t *testing.T, ms *kernel.MemInstanceStore, err error)
	}

	cases := []testCase{
		{
			name: "fn success returns nil",
			fn: func(ms *kernel.MemInstanceStore) func(ctx context.Context) error {
				return func(ctx context.Context) error {
					_, err := ms.Create(ctx, step("tx-ok", "a"))
					return err
				}
			},
			assert: func(t *testing.T, ms *kernel.MemInstanceStore, err error) {
				require.NoError(t, err)
				_, _, lerr := ms.Load(t.Context(), "tx-ok")
				assert.NoError(t, lerr, "create must have applied")
			},
		},
		{
			name: "fn error propagates and does NOT undo an already-applied Create (no rollback)",
			fn: func(ms *kernel.MemInstanceStore) func(ctx context.Context) error {
				return func(ctx context.Context) error {
					if _, err := ms.Create(ctx, step("tx-noundo", "a")); err != nil {
						return err
					}
					return errBoom
				}
			},
			assert: func(t *testing.T, ms *kernel.MemInstanceStore, err error) {
				require.ErrorIs(t, err, errBoom, "RunInTx must surface fn's error unchanged")
				_, _, lerr := ms.Load(t.Context(), "tx-noundo")
				assert.NoError(t, lerr, "sequencing-only: the Create must remain applied despite fn's later error")
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			ms := runtimetest.MustMemStore(t)
			err := ms.RunInTx(t.Context(), tc.fn(ms))
			tc.assert(t, ms, err)
		})
	}
}
