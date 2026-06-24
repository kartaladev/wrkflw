package postgres_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/zakyalvan/krtlwrkflw/engine"
	"github.com/zakyalvan/krtlwrkflw/internal/database"
	pg "github.com/zakyalvan/krtlwrkflw/internal/persistence/postgres"
	"github.com/zakyalvan/krtlwrkflw/runtime"
)

// defCapturePub records the full OutboxEvent of each Publish call.
type defCapturePub struct {
	mu     sync.Mutex
	events []runtime.OutboxEvent
}

func (p *defCapturePub) Publish(_ context.Context, ev runtime.OutboxEvent) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.events = append(p.events, ev)
	return nil
}

// TestRelayCarriesDefThroughOutbox is the ADR-0047 durable-path round-trip: a
// terminal event's Def is persisted by Store.Create (writeOutbox) and read back
// by the relay onto the republished OutboxEvent, so the publisher can set the
// "def" metadata the chaining handler projects into PredecessorDefinitionRef.
func TestRelayCarriesDefThroughOutbox(t *testing.T) {
	pool := database.RunTestDatabase(t)
	require.NoError(t, pg.Migrate(t.Context(), pool))
	store := pg.NewStore(pool)
	ctx := t.Context()

	now := time.Unix(1700000000, 0).UTC()
	_, err := store.Create(ctx, runtime.AppliedStep{
		State: engine.InstanceState{
			InstanceID: "i1", DefID: "approval", DefVersion: 3,
			Status: engine.StatusCompleted, StartedAt: now,
		},
		Trigger: engine.NewStartInstance(now, nil),
		Events: []runtime.OutboxEvent{{
			Topic: "instance.completed", Payload: map[string]any{"ok": true},
			InstanceID: "i1", DefinitionRef: "approval:3",
		}},
	})
	require.NoError(t, err)

	pub := &defCapturePub{}
	relay := pg.NewRelay(pool, pub)
	n, err := relay.DrainOnce(ctx)
	require.NoError(t, err)
	require.Equal(t, 1, n)
	require.Len(t, pub.events, 1)
	require.Equal(t, "approval:3", pub.events[0].DefinitionRef, "the predecessor def must survive the outbox round-trip")
}
