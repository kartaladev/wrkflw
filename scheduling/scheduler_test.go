package scheduling_test

import (
	"io"
	"sync"
	"testing"
	"time"

	"github.com/jonboulle/clockwork"
	"github.com/stretchr/testify/require"

	"github.com/zakyalvan/krtlwrkflw/runtime"
	"github.com/zakyalvan/krtlwrkflw/scheduling"
)

func TestNewScheduler_SatisfiesPortAndFires(t *testing.T) {
	fakeClock := clockwork.NewFakeClock()
	s, err := scheduling.NewScheduler(fakeClock)
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })

	// Runtime checks that the façade satisfies the required contracts.
	var _ runtime.Scheduler = s
	var _ io.Closer = s

	var wg sync.WaitGroup
	wg.Add(1)
	s.Schedule("t1", fakeClock.Now().Add(3*time.Second), func() { wg.Done() })
	require.NoError(t, fakeClock.BlockUntilContext(t.Context(), 1))
	fakeClock.Advance(3 * time.Second)
	wg.Wait()
}

func TestScheduler_Cancel_NoOp(t *testing.T) {
	fakeClock := clockwork.NewFakeClock()
	s, err := scheduling.NewScheduler(fakeClock)
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })

	// Cancel on an unknown ID must not panic or error.
	s.Cancel("nonexistent")

	// Schedule then cancel — callback must NOT fire.
	fired := false
	s.Schedule("t2", fakeClock.Now().Add(1*time.Second), func() { fired = true })
	s.Cancel("t2")

	// Advance past the would-be fire time; nothing should fire.
	// Drain any remaining waiters on the fake clock.
	require.NoError(t, fakeClock.BlockUntilContext(t.Context(), 0))
	fakeClock.Advance(2 * time.Second)
	require.False(t, fired, "cancelled timer must not fire")
}
