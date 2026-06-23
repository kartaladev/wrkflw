package postgres_test

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/zakyalvan/krtlwrkflw/internal/database"
	"github.com/zakyalvan/krtlwrkflw/engine"
	"github.com/zakyalvan/krtlwrkflw/internal/persistence/postgres"
	"github.com/zakyalvan/krtlwrkflw/runtime"
)

// countingPublisher records how many events it has published.
type countingPublisher struct{ n atomic.Int64 }

func (p *countingPublisher) Publish(_ context.Context, _ runtime.OutboxEvent) error {
	p.n.Add(1)
	return nil
}

// startTrigger builds a minimal StartInstance trigger for relay-listen tests.
func startTrigger(_ string) engine.Trigger {
	return engine.NewStartInstance(time.Now().UTC(), nil)
}

func TestRelayListenDrainsBeforePollInterval(t *testing.T) {
	pool := database.RunTestDatabase(t)
	require.NoError(t, postgres.Migrate(t.Context(), pool))

	pub := &countingPublisher{}
	// A deliberately long poll interval: only a NOTIFY wakeup can drain in time.
	relay := postgres.NewRelay(pool, pub,
		postgres.WithPollInterval(30*time.Second),
		postgres.WithListenNotify(),
	)

	runCtx, cancel := context.WithCancel(t.Context())
	defer cancel()
	go func() { _ = relay.Run(runCtx) }()

	// Give the listener a moment to LISTEN, then write an event WITH a NOTIFY.
	time.Sleep(200 * time.Millisecond)
	st := postgres.NewStore(pool, postgres.WithOutboxNotify())
	_, err := st.Create(t.Context(), runtime.AppliedStep{
		State:   engine.InstanceState{InstanceID: "lr1", DefID: "d", DefVersion: 1, Status: engine.StatusRunning, StartedAt: time.Now().UTC()},
		Trigger: startTrigger("lr1"),
		Events:  []runtime.OutboxEvent{{Topic: "instance.completed", Payload: map[string]any{"id": "lr1"}}},
	})
	require.NoError(t, err)

	// Must be published well before the 30s poll tick.
	require.Eventually(t, func() bool { return pub.n.Load() == 1 }, 5*time.Second, 25*time.Millisecond)
}
