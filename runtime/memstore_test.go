package runtime_test

import (
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/zakyalvan/krtlwrkflw/engine"
	"github.com/zakyalvan/krtlwrkflw/runtime"
)

func step(id, topic string) runtime.AppliedStep {
	return runtime.AppliedStep{
		State:   engine.InstanceState{InstanceID: id, Status: engine.StatusRunning},
		Trigger: engine.NewStartInstance(time.Unix(0, 0), map[string]any{"k": "v"}),
		Events:  []runtime.OutboxEvent{{Topic: topic, Payload: map[string]any{"x": 1}}},
	}
}

func TestMemStoreCreateLoadRoundTrip(t *testing.T) {
	ms := runtime.NewMemStore()
	tok, err := ms.Create(t.Context(), step("i1", "instance.completed"))
	require.NoError(t, err)

	st, loaded, err := ms.Load(t.Context(), "i1")
	require.NoError(t, err)
	require.Equal(t, "i1", st.InstanceID)
	require.Equal(t, tok, loaded)
}

func TestMemStoreCreateDuplicate(t *testing.T) {
	ms := runtime.NewMemStore()
	_, err := ms.Create(t.Context(), step("dup", "a"))
	require.NoError(t, err)

	// A second Create for the same instance id must not silently overwrite; it
	// returns the typed ErrInstanceExists so callers (the Chainer) can treat a
	// duplicate start as a no-op (ADR-0045).
	_, err = ms.Create(t.Context(), step("dup", "b"))
	require.ErrorIs(t, err, runtime.ErrInstanceExists)

	// The original instance is intact (not clobbered by the rejected Create).
	st, _, loadErr := ms.Load(t.Context(), "dup")
	require.NoError(t, loadErr)
	require.Equal(t, "dup", st.InstanceID)
}

func TestMemStoreLoadMissing(t *testing.T) {
	ms := runtime.NewMemStore()
	_, _, err := ms.Load(t.Context(), "nope")
	require.ErrorIs(t, err, runtime.ErrInstanceNotFound)
}

func TestMemStoreCommit(t *testing.T) {
	tests := map[string]struct {
		assert func(t *testing.T, ms *runtime.MemStore)
	}{
		"advances token": {
			assert: func(t *testing.T, ms *runtime.MemStore) {
				tok, err := ms.Create(t.Context(), step("i1", "a"))
				require.NoError(t, err)
				next, err := ms.Commit(t.Context(), tok, step("i1", "b"))
				require.NoError(t, err)
				require.NotEqual(t, tok, next)
			},
		},
		"stale token conflicts": {
			assert: func(t *testing.T, ms *runtime.MemStore) {
				tok, err := ms.Create(t.Context(), step("i1", "a"))
				require.NoError(t, err)
				_, err = ms.Commit(t.Context(), tok, step("i1", "b")) // advances past tok
				require.NoError(t, err)
				_, err = ms.Commit(t.Context(), tok, step("i1", "c")) // stale
				require.ErrorIs(t, err, runtime.ErrConcurrentUpdate)
			},
		},
		"captures outbox events": {
			assert: func(t *testing.T, ms *runtime.MemStore) {
				tok, err := ms.Create(t.Context(), step("i1", "instance.completed"))
				require.NoError(t, err)
				_, err = ms.Commit(t.Context(), tok, step("i1", "instance.failed"))
				require.NoError(t, err)
				topics := make([]string, 0)
				for _, e := range ms.Events() {
					topics = append(topics, e.Topic)
				}
				require.Equal(t, []string{"instance.completed", "instance.failed"}, topics)
			},
		},
		"records journal entries": {
			assert: func(t *testing.T, ms *runtime.MemStore) {
				tok, err := ms.Create(t.Context(), step("i1", "a"))
				require.NoError(t, err)
				_, err = ms.Commit(t.Context(), tok, step("i1", "b"))
				require.NoError(t, err)
				entries, err := ms.Entries(t.Context(), "i1")
				require.NoError(t, err)
				require.Len(t, entries, 2)
			},
		},
		"commit on missing instance": {
			assert: func(t *testing.T, ms *runtime.MemStore) {
				_, err := ms.Commit(t.Context(), 1, step("missing-id", "a"))
				require.ErrorIs(t, err, runtime.ErrInstanceNotFound)
			},
		},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			tc.assert(t, runtime.NewMemStore())
		})
	}
}

// TestMemStoreConcurrentSafe verifies that MemStore is safe for concurrent use
// from multiple goroutines. The test is designed to expose data races when run
// with -race.
func TestMemStoreConcurrentSafe(t *testing.T) {
	const (
		numWorkers = 20
		numCommits = 10
	)

	ms := runtime.NewMemStore()
	ctx := t.Context()

	var wg sync.WaitGroup

	// Start a goroutine that continuously reads Events() while workers are active.
	stop := make(chan struct{})
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
				_ = ms.Events()
			}
		}
	}()

	// Start N worker goroutines, each working on a distinct instanceID.
	var workerWg sync.WaitGroup
	for i := range numWorkers {
		workerWg.Add(1)
		go func(idx int) {
			defer workerWg.Done()
			instID := fmt.Sprintf("inst-%d", idx)
			s := step(instID, fmt.Sprintf("topic-%d", idx))

			tok, err := ms.Create(ctx, s)
			if err != nil {
				return
			}

			for range numCommits {
				tok, err = ms.Commit(ctx, tok, step(instID, fmt.Sprintf("topic-%d-commit", idx)))
				if err != nil {
					return
				}
				_, _, _ = ms.Load(ctx, instID)
				_, _ = ms.Entries(ctx, instID)
			}
		}(i)
	}

	workerWg.Wait()
	close(stop)
	wg.Wait()
}
