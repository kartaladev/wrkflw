package runtime_test

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jonboulle/clockwork"
	"github.com/stretchr/testify/require"
	"github.com/zakyalvan/krtlwrkflw/model"
	"github.com/zakyalvan/krtlwrkflw/runtime"
)

// countingRegistry is a fake DefinitionRegistry that counts Lookup calls.
type countingRegistry struct {
	calls atomic.Int64
	def   *model.ProcessDefinition
	err   error
	// block is an optional channel; if non-nil, Lookup blocks until it is closed.
	block chan struct{}
}

func (c *countingRegistry) Lookup(_ context.Context, _ string) (*model.ProcessDefinition, error) {
	if c.block != nil {
		<-c.block
	}
	c.calls.Add(1)
	return c.def, c.err
}

// fakeClock is a controllable clock for TTL testing.
type fakeClock struct {
	mu  sync.Mutex
	now time.Time
}

func newFakeClock(t time.Time) *fakeClock { return &fakeClock{now: t} }

func (f *fakeClock) Now() time.Time {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.now
}

func (f *fakeClock) Advance(d time.Duration) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.now = f.now.Add(d)
}

func TestCachingDefinitionRegistry(t *testing.T) {
	t.Parallel()

	baseDef := &model.ProcessDefinition{ID: "d", Version: 1}
	baseTime := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	ttl := time.Minute

	tests := map[string]struct {
		assert func(t *testing.T, backing *countingRegistry, clk *fakeClock, c *runtime.CachingDefinitionRegistry)
	}{
		"second lookup served from cache": {
			assert: func(t *testing.T, backing *countingRegistry, _ *fakeClock, c *runtime.CachingDefinitionRegistry) {
				got1, err := c.Lookup(t.Context(), "d:1")
				require.NoError(t, err)
				require.Equal(t, "d", got1.ID)

				got2, err := c.Lookup(t.Context(), "d:1")
				require.NoError(t, err)
				require.Equal(t, "d", got2.ID)

				require.Equal(t, int64(1), backing.calls.Load(), "backing must be called exactly once")
			},
		},
		"ttl expiry triggers a fresh backing call": {
			assert: func(t *testing.T, backing *countingRegistry, clk *fakeClock, c *runtime.CachingDefinitionRegistry) {
				_, err := c.Lookup(t.Context(), "d:1")
				require.NoError(t, err)
				require.Equal(t, int64(1), backing.calls.Load())

				// Advance past TTL.
				clk.Advance(ttl + time.Second)

				_, err = c.Lookup(t.Context(), "d:1")
				require.NoError(t, err)
				require.Equal(t, int64(2), backing.calls.Load(), "backing must be called again after TTL expires")
			},
		},
		"concurrent misses collapse to one backing call": {
			assert: func(t *testing.T, backing *countingRegistry, _ *fakeClock, c *runtime.CachingDefinitionRegistry) {
				// Single-flight: all 50 goroutines race on the same uncached key;
				// only one backing call must happen.
				block := make(chan struct{})
				backing.block = block

				// Deterministic barrier: track when all caller goroutines are ready to call Lookup.
				const numGoroutines = 50
				var arrived sync.WaitGroup
				arrived.Add(numGoroutines)

				var wg sync.WaitGroup
				for range numGoroutines {
					wg.Add(1)
					go func() {
						defer wg.Done()
						arrived.Done() // Signal that this caller is about to call Lookup
						_, _ = c.Lookup(t.Context(), "d:1")
					}()
				}
				// Wait for all N caller goroutines to signal readiness before unblocking the backing stub.
				arrived.Wait()
				close(block)
				wg.Wait()

				require.Equal(t, int64(1), backing.calls.Load(), "singleflight must collapse concurrent misses to one call")
			},
		},
		"miss propagation — ErrDefinitionNotFound is returned and not cached": {
			assert: func(t *testing.T, backing *countingRegistry, _ *fakeClock, c *runtime.CachingDefinitionRegistry) {
				backing.def = nil
				backing.err = runtime.ErrDefinitionNotFound

				_, err := c.Lookup(t.Context(), "missing:1")
				require.ErrorIs(t, err, runtime.ErrDefinitionNotFound)

				// Second call: negative results must NOT be cached, so backing is called again.
				_, err = c.Lookup(t.Context(), "missing:1")
				require.ErrorIs(t, err, runtime.ErrDefinitionNotFound)
				require.Equal(t, int64(2), backing.calls.Load(), "errors must not be cached")
			},
		},
		"different defRefs cached independently": {
			assert: func(t *testing.T, backing *countingRegistry, _ *fakeClock, c *runtime.CachingDefinitionRegistry) {
				_, err := c.Lookup(t.Context(), "d:1")
				require.NoError(t, err)

				// Change the def the backing returns for a second key.
				backing.def = &model.ProcessDefinition{ID: "e", Version: 2}

				got, err := c.Lookup(t.Context(), "e:2")
				require.NoError(t, err)
				require.Equal(t, "e", got.ID)

				require.Equal(t, int64(2), backing.calls.Load(), "each distinct defRef is a separate cache entry")
			},
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			clk := newFakeClock(baseTime)
			backing := &countingRegistry{def: baseDef}
			c := runtime.NewCachingDefinitionRegistry(backing, ttl, runtime.WithCachingDefinitionRegistryClock(clk))
			tc.assert(t, backing, clk, c)
		})
	}
}

// TestCachingDefinitionRegistry_ImplementsInterface checks the compile-time interface assertion.
func TestCachingDefinitionRegistry_ImplementsInterface(t *testing.T) {
	var _ runtime.DefinitionRegistry = (*runtime.CachingDefinitionRegistry)(nil)
	t.Log("CachingDefinitionRegistry satisfies runtime.DefinitionRegistry")
}

// TestCachingDefinitionRegistry_NonErrNotCached verifies that arbitrary (non-not-found) errors
// are also not cached.
func TestCachingDefinitionRegistry_NonErrNotCached(t *testing.T) {
	t.Parallel()
	sentinel := errors.New("transient error")
	backing := &countingRegistry{err: sentinel}
	c := runtime.NewCachingDefinitionRegistry(backing, time.Minute)

	_, err := c.Lookup(t.Context(), "d:1")
	require.ErrorIs(t, err, sentinel)

	_, err = c.Lookup(t.Context(), "d:1")
	require.ErrorIs(t, err, sentinel)
	require.Equal(t, int64(2), backing.calls.Load(), "transient errors must not be cached")
}

// TestNewCachingDefinitionRegistryDefaultUsesSystemClock verifies that omitting the clock
// option defaults to clock.System() and the registry still caches correctly.
func TestNewCachingDefinitionRegistryDefaultUsesSystemClock(t *testing.T) {
	t.Parallel()
	backing := &countingRegistry{def: &model.ProcessDefinition{ID: "d", Version: 1}}
	c := runtime.NewCachingDefinitionRegistry(backing, time.Minute) // no clock option
	_, err := c.Lookup(t.Context(), "d:1")
	require.NoError(t, err)
	_, err = c.Lookup(t.Context(), "d:1") // within TTL → cache hit, no second backing call
	require.NoError(t, err)
	require.Equal(t, int64(1), backing.calls.Load(), "second lookup within TTL should hit cache under the system clock")
}

// TestNewCachingDefinitionRegistryWithClockOption verifies that WithCachingDefinitionRegistryClock
// injects a fake clock so TTL expiry can be controlled deterministically in tests.
func TestNewCachingDefinitionRegistryWithClockOption(t *testing.T) {
	t.Parallel()
	fake := clockwork.NewFakeClockAt(time.Unix(1000, 0))
	backing := &countingRegistry{def: &model.ProcessDefinition{ID: "d", Version: 1}}
	c := runtime.NewCachingDefinitionRegistry(backing, time.Minute, runtime.WithCachingDefinitionRegistryClock(fake))
	_, err := c.Lookup(t.Context(), "d:1")
	require.NoError(t, err)
	fake.Advance(2 * time.Minute) // past TTL → next lookup re-hits backing
	_, err = c.Lookup(t.Context(), "d:1")
	require.NoError(t, err)
	require.Equal(t, int64(2), backing.calls.Load(), "lookup after TTL expiry on the fake clock should re-call backing")
}
