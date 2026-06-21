package runtime_test

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/jonboulle/clockwork"

	"github.com/zakyalvan/krtlwrkflw/engine"
	"github.com/zakyalvan/krtlwrkflw/runtime"
)

// countingStore wraps a backing Store and counts Load calls (cache-miss proxy).
type countingStore struct {
	backing runtime.Store
	loads   atomic.Int64
}

func (c *countingStore) Create(ctx context.Context, s runtime.AppliedStep) (runtime.Token, error) {
	return c.backing.Create(ctx, s)
}
func (c *countingStore) Load(ctx context.Context, id string) (engine.InstanceState, runtime.Token, error) {
	c.loads.Add(1)
	return c.backing.Load(ctx, id)
}
func (c *countingStore) Commit(ctx context.Context, e runtime.Token, s runtime.AppliedStep) (runtime.Token, error) {
	return c.backing.Commit(ctx, e, s)
}

// neverOwn is an Ownership that never grants ownership (forces cache bypass).
type neverOwn struct{}

func (neverOwn) Acquire(context.Context, string) (bool, error) { return false, nil }
func (neverOwn) Release(context.Context, string) error         { return nil }

func runningState(id string) engine.InstanceState {
	return engine.InstanceState{InstanceID: id, DefID: "d", DefVersion: 1, Status: engine.StatusRunning, StartedAt: time.Unix(0, 0).UTC()}
}

func startTrg() engine.Trigger { return engine.NewStartInstance(time.Unix(0, 0).UTC(), nil) }

func TestCachingStoreServesOwnedLoadFromCache(t *testing.T) {
	cs := &countingStore{backing: runtime.NewMemStore()}
	clk := clockwork.NewFakeClock()
	store := runtime.NewCachingStore(cs, runtime.AlwaysOwn{}, clk)

	id := "c1"
	_, err := store.Create(t.Context(), runtime.AppliedStep{State: runningState(id), Trigger: startTrg()})
	require.NoError(t, err)

	// First owned Load after a write-through Create is a cache hit — no backing Load.
	st, _, err := store.Load(t.Context(), id)
	require.NoError(t, err)
	assert.Equal(t, id, st.InstanceID)
	assert.Equal(t, int64(0), cs.loads.Load(), "owned Load should be served from the write-through cache")

	// A second Load is also a hit.
	_, _, err = store.Load(t.Context(), id)
	require.NoError(t, err)
	assert.Equal(t, int64(0), cs.loads.Load())
}

func TestCachingStoreBypassesWhenNotOwned(t *testing.T) {
	cs := &countingStore{backing: runtime.NewMemStore()}
	store := runtime.NewCachingStore(cs, neverOwn{}, clockwork.NewFakeClock())

	id := "c2"
	_, err := store.Create(t.Context(), runtime.AppliedStep{State: runningState(id), Trigger: startTrg()})
	require.NoError(t, err)

	// Not owned ⇒ every Load hits the backing.
	_, _, err = store.Load(t.Context(), id)
	require.NoError(t, err)
	_, _, err = store.Load(t.Context(), id)
	require.NoError(t, err)
	assert.Equal(t, int64(2), cs.loads.Load())
}

func TestCachingStoreEvictsOnConcurrentUpdate(t *testing.T) {
	mem := runtime.NewMemStore()
	cs := &countingStore{backing: mem}
	store := runtime.NewCachingStore(cs, runtime.AlwaysOwn{}, clockwork.NewFakeClock())

	id := "c3"
	tok, err := store.Create(t.Context(), runtime.AppliedStep{State: runningState(id), Trigger: startTrg()})
	require.NoError(t, err)

	// Advance the backing out-of-band so the cached token is stale.
	_, err = mem.Commit(t.Context(), tok, runtime.AppliedStep{State: runningState(id), Trigger: startTrg()})
	require.NoError(t, err)

	// Commit via the cache with the stale token ⇒ ErrConcurrentUpdate ⇒ evict.
	_, err = store.Commit(t.Context(), tok, runtime.AppliedStep{State: runningState(id), Trigger: startTrg()})
	require.ErrorIs(t, err, runtime.ErrConcurrentUpdate)

	// Next owned Load must re-read the backing (entry was evicted).
	before := cs.loads.Load()
	_, _, err = store.Load(t.Context(), id)
	require.NoError(t, err)
	assert.Equal(t, before+1, cs.loads.Load())
}
