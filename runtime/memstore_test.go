package runtime_test

import (
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
