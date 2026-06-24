package postgres_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/zakyalvan/krtlwrkflw/engine"
	"github.com/zakyalvan/krtlwrkflw/internal/database"
	"github.com/zakyalvan/krtlwrkflw/internal/persistence/postgres"
	"github.com/zakyalvan/krtlwrkflw/runtime"
)

// startNotifyTrigger builds a minimal StartInstance trigger for notify tests.
func startNotifyTrigger(id string) engine.Trigger {
	return engine.NewStartInstance(time.Now().UTC(), nil)
}

func TestStoreOutboxNotifyWakesListener(t *testing.T) {
	pool := database.RunTestDatabase(t)
	require.NoError(t, postgres.Migrate(t.Context(), pool))

	// A dedicated LISTEN connection.
	lconn, err := pool.Acquire(t.Context())
	require.NoError(t, err)
	defer lconn.Release()
	_, err = lconn.Exec(t.Context(), "LISTEN wrkflw_outbox")
	require.NoError(t, err)

	st := postgres.NewStore(pool, postgres.WithOutboxNotify())

	// Create an instance whose first step emits an outbox event.
	_, err = st.Create(t.Context(), runtime.AppliedStep{
		State:   engine.InstanceState{InstanceID: "n1", DefID: "d", DefVersion: 1, Status: engine.StatusRunning, StartedAt: time.Now().UTC()},
		Trigger: startNotifyTrigger("n1"),
		Events:  []runtime.OutboxEvent{{Topic: "instance.completed", Payload: map[string]any{"id": "n1"}}},
	})
	require.NoError(t, err)

	// The notification must arrive promptly.
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	n, err := lconn.Conn().WaitForNotification(ctx)
	require.NoError(t, err)
	require.Equal(t, "wrkflw_outbox", n.Channel)
}
