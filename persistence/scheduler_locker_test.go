package persistence_test

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/kartaladev/wrkflw/internal/persistence/dialect"
	"github.com/kartaladev/wrkflw/persistence"
	"github.com/kartaladev/wrkflw/scheduler"
)

// fakeDialectLocker is an in-memory dialect.Locker for exercising the scheduler
// lock bridge without a database.
type fakeDialectLocker struct {
	tryOK     bool
	tryErr    error
	unlockErr error

	lockedKey   string
	unlockedKey string
}

func (l *fakeDialectLocker) TryLock(_ context.Context, key string) (bool, error) {
	l.lockedKey = key
	return l.tryOK, l.tryErr
}

func (l *fakeDialectLocker) Unlock(_ context.Context, key string) error {
	l.unlockedKey = key
	return l.unlockErr
}

func TestNewSchedulerLocker(t *testing.T) {
	type tc struct {
		name   string
		fake   *fakeDialectLocker
		assert func(t *testing.T, l scheduler.Locker, fake *fakeDialectLocker)
	}

	cases := []tc{
		{
			name: "acquired lock unlocks via dialect.Unlock",
			fake: &fakeDialectLocker{tryOK: true},
			assert: func(t *testing.T, l scheduler.Locker, fake *fakeDialectLocker) {
				held, err := l.Lock(t.Context(), "timer-1")
				require.NoError(t, err)
				require.NotNil(t, held)
				require.Equal(t, "timer-1", fake.lockedKey)

				require.NoError(t, held.Unlock(t.Context()))
				require.Equal(t, "timer-1", fake.unlockedKey)
			},
		},
		{
			name: "contended lock returns an error and does not unlock",
			fake: &fakeDialectLocker{tryOK: false},
			assert: func(t *testing.T, l scheduler.Locker, fake *fakeDialectLocker) {
				held, err := l.Lock(t.Context(), "timer-2")
				require.Error(t, err)
				require.Nil(t, held)
				require.Empty(t, fake.unlockedKey)
			},
		},
		{
			name: "try error propagates",
			fake: &fakeDialectLocker{tryErr: errors.New("boom")},
			assert: func(t *testing.T, l scheduler.Locker, fake *fakeDialectLocker) {
				held, err := l.Lock(t.Context(), "timer-3")
				require.Error(t, err)
				require.Nil(t, held)
			},
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var dl dialect.Locker = c.fake
			l := persistence.NewSchedulerLocker(dl)
			c.assert(t, l, c.fake)
		})
	}
}
