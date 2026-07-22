package scheduler_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kartaladev/wrkflw/scheduler"
)

// panicJobStoreThunk fails the test if invoked — WithJobStore must record
// the thunk without ever calling it (registration must stay lazy).
func panicJobStoreThunk() scheduler.JobStore {
	panic("thunk must not be invoked at registration time")
}

// fakeJobStore is a minimal [scheduler.JobStore] used only so distinct thunks
// produce distinct, identifiable values for the "last wins" assertion.
type fakeJobStore struct{ name string }

func (f *fakeJobStore) Load(ctx context.Context) ([]scheduler.ScheduledJob, error) {
	return nil, nil
}
func (f *fakeJobStore) Save(ctx context.Context, j scheduler.ScheduledJob) error { return nil }
func (f *fakeJobStore) Delete(ctx context.Context, id string) error              { return nil }

// TestWithJobStore verifies the kind-routed registration behavior of
// [scheduler.WithJobStore]: recording under kind, nil-thunk and
// empty-kind no-ops, last-registration-wins for a repeated kind, and that the
// thunk is never invoked at registration time (laziness).
func TestWithJobStore(t *testing.T) {
	type testCase struct {
		name   string
		opts   []scheduler.Option
		assert func(t *testing.T, stores map[scheduler.JobKind]func() scheduler.JobStore)
	}

	firstStore := &fakeJobStore{name: "first"}
	lastStore := &fakeJobStore{name: "last"}

	cases := []testCase{
		{
			name: "registers thunk under kind",
			opts: []scheduler.Option{
				scheduler.WithJobStore("wrkflw.timer", func() scheduler.JobStore { return firstStore }),
			},
			assert: func(t *testing.T, stores map[scheduler.JobKind]func() scheduler.JobStore) {
				require.Contains(t, stores, scheduler.JobKind("wrkflw.timer"))
				got := stores["wrkflw.timer"]()
				assert.Same(t, firstStore, got)
			},
		},
		{
			name: "nil thunk is ignored",
			opts: []scheduler.Option{
				scheduler.WithJobStore("wrkflw.timer", nil),
			},
			assert: func(t *testing.T, stores map[scheduler.JobKind]func() scheduler.JobStore) {
				assert.NotContains(t, stores, scheduler.JobKind("wrkflw.timer"))
			},
		},
		{
			name: "empty kind is ignored",
			opts: []scheduler.Option{
				scheduler.WithJobStore("", func() scheduler.JobStore { return firstStore }),
			},
			assert: func(t *testing.T, stores map[scheduler.JobKind]func() scheduler.JobStore) {
				assert.Empty(t, stores)
			},
		},
		{
			name: "same kind registered twice keeps the last",
			opts: []scheduler.Option{
				scheduler.WithJobStore("wrkflw.timer", func() scheduler.JobStore { return firstStore }),
				scheduler.WithJobStore("wrkflw.timer", func() scheduler.JobStore { return lastStore }),
			},
			assert: func(t *testing.T, stores map[scheduler.JobKind]func() scheduler.JobStore) {
				require.Contains(t, stores, scheduler.JobKind("wrkflw.timer"))
				got := stores["wrkflw.timer"]()
				assert.Same(t, lastStore, got)
			},
		},
		{
			name: "thunk is not invoked at registration time",
			opts: []scheduler.Option{
				scheduler.WithJobStore("wrkflw.reminder", panicJobStoreThunk),
			},
			assert: func(t *testing.T, stores map[scheduler.JobKind]func() scheduler.JobStore) {
				require.Contains(t, stores, scheduler.JobKind("wrkflw.reminder"))
				// Registration itself must not have called the thunk — reaching
				// this line without a panic is the proof.
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s, err := scheduler.NewScheduler(tc.opts...)
			require.NoError(t, err)
			t.Cleanup(func() { _ = s.Close() })

			tc.assert(t, scheduler.JobStores(s))
		})
	}
}
