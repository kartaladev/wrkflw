package persistence_test

// caching_instance_store_runintx_test.go covers CachingInstanceStore.RunInTx
// (ADR-0134, Task 8): capability forwarding to a real SQL backing store, the
// degraded fallback when the backing store lacks the capability, and — the
// audited BLOCKER — that a rolled-back RunInTx evicts (never poisons) every
// instance the wrapper wrote through during fn. This needs a real transactional
// backing (SQLite via dbtest, no Docker daemon required), so it lives apart
// from the countingStore/MemInstanceStore-backed table in
// caching_instance_store_test.go.

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kartaladev/wrkflw/engine"
	"github.com/kartaladev/wrkflw/internal/dbtest"
	"github.com/kartaladev/wrkflw/internal/persistence/dialect"
	sqlstore "github.com/kartaladev/wrkflw/internal/persistence/store"
	"github.com/kartaladev/wrkflw/persistence"
	"github.com/kartaladev/wrkflw/persistence/cache/hotcache"
	"github.com/kartaladev/wrkflw/runtime/kernel"
)

// errWrapRunInTxBoom is an injected sentinel distinct from any package error.
var errWrapRunInTxBoom = errors.New("wrapper run in tx: boom")

// newSQLiteCachingStore wires a real SQLite-backed Store (a kernel.TxRunner)
// behind a CachingInstanceStore for the RunInTx forwarding tests. It returns
// both the wrapper and the raw backing store, so a test can assert on the
// wrapper's cached view AND independently confirm what the database actually
// persisted.
func newSQLiteCachingStore(t *testing.T) (wrapper *persistence.CachingInstanceStore, backing *sqlstore.Store) {
	t.Helper()
	db := dbtest.RunTestSQLite(t)
	backing, err := sqlstore.New(db, dialect.NewSQLite())
	require.NoError(t, err)
	wrapper, err = persistence.NewCachingInstanceStore(backing, kernel.AlwaysOwn{}, hotcache.New())
	require.NoError(t, err)
	return wrapper, backing
}

// TestCachingInstanceStoreRunInTxForwardsCapability covers the degraded
// fallback (backing without TxRunner: still just fn(ctx), a compatible
// contract with kernel.MemInstanceStore.RunInTx) and the capability-forwarding
// success path (backing is a real kernel.TxRunner).
func TestCachingInstanceStoreRunInTxForwardsCapability(t *testing.T) {
	t.Parallel()

	t.Run("backing without TxRunner falls back to fn(ctx)", func(t *testing.T) {
		t.Parallel()
		cs := &countingStore{backing: mustMemStore(t)}
		wrapper, err := persistence.NewCachingInstanceStore(cs, kernel.AlwaysOwn{}, hotcache.New())
		require.NoError(t, err)

		called := false
		err = wrapper.RunInTx(t.Context(), func(context.Context) error {
			called = true
			return nil
		})
		require.NoError(t, err)
		assert.True(t, called, "fn must run even in the degraded fallback")
	})

	t.Run("backing with TxRunner: fn success commits through the wrapper", func(t *testing.T) {
		t.Parallel()
		wrapper, _ := newSQLiteCachingStore(t)
		var _ kernel.TxRunner = wrapper // compile-time capability check

		id := "wrap-tx-commit"
		err := wrapper.RunInTx(t.Context(), func(txCtx context.Context) error {
			_, cerr := wrapper.Create(txCtx, kernel.AppliedStep{State: runningState(id), Trigger: startTrg()})
			return cerr
		})
		require.NoError(t, err)

		st, _, err := wrapper.Load(t.Context(), id)
		require.NoError(t, err)
		assert.Equal(t, id, st.InstanceID)
	})
}

// TestCachingInstanceStoreRunInTxEvictsOnRollback is the audited BLOCKER case:
// the wrapper's Commit inside fn write-through caches the new state
// optimistically (the joined write's own Commit is a no-op — the outer
// RunInTx owns the real commit/rollback decision), so if fn then errors and
// the whole unit rolls back, that optimistic cache entry is exactly the
// poisoned value a subsequent Load must never see. RunInTx must evict it.
func TestCachingInstanceStoreRunInTxEvictsOnRollback(t *testing.T) {
	wrapper, backing := newSQLiteCachingStore(t)
	var _ kernel.TxRunner = wrapper // compile-time capability check
	_, isTxRunner := any(wrapper).(kernel.TxRunner)
	require.True(t, isTxRunner, "CachingInstanceStore must still satisfy kernel.TxRunner")

	id := "wrap-tx-rollback"
	preTok, err := wrapper.Create(t.Context(), kernel.AppliedStep{State: runningState(id), Trigger: startTrg()})
	require.NoError(t, err)
	preState, preTokLoaded, err := wrapper.Load(t.Context(), id)
	require.NoError(t, err)
	require.Equal(t, preTok, preTokLoaded)

	err = wrapper.RunInTx(t.Context(), func(txCtx context.Context) error {
		completed := runningState(id)
		completed.Status = engine.StatusCompleted
		if _, cerr := wrapper.Commit(txCtx, preTok, kernel.AppliedStep{State: completed, Trigger: startTrg()}); cerr != nil {
			return cerr
		}
		return errWrapRunInTxBoom
	})
	require.ErrorIs(t, err, errWrapRunInTxBoom)

	// Load through the WRAPPER must return the PRE-tx state: the cache must
	// have been evicted, never left holding the poisoned in-tx write.
	gotState, gotTok, err := wrapper.Load(t.Context(), id)
	require.NoError(t, err)
	assert.Equal(t, preState.Status, gotState.Status, "must be pre-tx status, not the rolled-back Completed write")
	assert.Equal(t, preTok, gotTok, "must be the pre-tx version token")

	// The backing store itself must agree — the write was truly rolled back
	// in the database, not merely absent from the wrapper's cache.
	backingState, backingTok, err := backing.Load(t.Context(), id)
	require.NoError(t, err)
	assert.Equal(t, engine.StatusRunning, backingState.Status)
	assert.Equal(t, preTok, backingTok)
}

// TestCachingInstanceStoreRunInTxEvictsOnPanic is the panic-path sibling of
// TestCachingInstanceStoreRunInTxEvictsOnRollback: if fn panics instead of
// returning an error, the inner store's own defer rolls the database back,
// but that alone does not evict the wrapper's optimistic write-through cache
// entry — a caller that recovers upstream would otherwise keep serving a
// poisoned entry until its TTL expires. RunInTx must evict on the panic path
// too, and must never recover the panic itself (it must propagate unchanged).
func TestCachingInstanceStoreRunInTxEvictsOnPanic(t *testing.T) {
	wrapper, backing := newSQLiteCachingStore(t)
	var _ kernel.TxRunner = wrapper // compile-time capability check

	id := "wrap-tx-panic"
	preTok, err := wrapper.Create(t.Context(), kernel.AppliedStep{State: runningState(id), Trigger: startTrg()})
	require.NoError(t, err)
	preState, preTokLoaded, err := wrapper.Load(t.Context(), id)
	require.NoError(t, err)
	require.Equal(t, preTok, preTokLoaded)

	panicked := func() (recovered any) {
		defer func() { recovered = recover() }()
		_ = wrapper.RunInTx(t.Context(), func(txCtx context.Context) error {
			completed := runningState(id)
			completed.Status = engine.StatusCompleted
			if _, cerr := wrapper.Commit(txCtx, preTok, kernel.AppliedStep{State: completed, Trigger: startTrg()}); cerr != nil {
				return cerr
			}
			panic("wrapper run in tx: kaboom")
		})
		return nil
	}()
	require.NotNil(t, panicked, "the panic must propagate out of RunInTx, not be swallowed")
	assert.Equal(t, "wrapper run in tx: kaboom", panicked)

	// Load through the WRAPPER must return the PRE-tx state: the cache must
	// have been evicted on the panic path, never left holding the poisoned
	// in-tx write.
	gotState, gotTok, err := wrapper.Load(t.Context(), id)
	require.NoError(t, err)
	assert.Equal(t, preState.Status, gotState.Status, "must be pre-tx status, not the panicked-mid-write Completed write")
	assert.Equal(t, preTok, gotTok, "must be the pre-tx version token")

	// The backing store itself must agree — the write was truly rolled back
	// in the database, not merely absent from the wrapper's cache.
	backingState, backingTok, err := backing.Load(t.Context(), id)
	require.NoError(t, err)
	assert.Equal(t, engine.StatusRunning, backingState.Status)
	assert.Equal(t, preTok, backingTok)
}
