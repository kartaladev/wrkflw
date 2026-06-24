package postgres_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zakyalvan/krtlwrkflw/engine"
	"github.com/zakyalvan/krtlwrkflw/internal/database"
	"github.com/zakyalvan/krtlwrkflw/internal/persistence/postgres"
	"github.com/zakyalvan/krtlwrkflw/runtime"
)

// TestStoreCreateWithNotifyButNoEvents exercises the maybeNotify early-return
// when notify=true but the step produced no outbox events. The early return is
// !s.notify || len(events) == 0, so this covers the len==0 branch with notify on.
func TestStoreCreateWithNotifyButNoEvents(t *testing.T) {
	pool := database.RunTestDatabase(t)
	require.NoError(t, postgres.Migrate(t.Context(), pool))

	st := postgres.NewStore(pool, postgres.WithOutboxNotify())

	now := time.Now().UTC()
	tok, err := st.Create(t.Context(), runtime.AppliedStep{
		State: engine.InstanceState{
			InstanceID: "notify-noev-create-1",
			DefID:      "d",
			DefVersion: 1,
			Status:     engine.StatusRunning,
			StartedAt:  now,
		},
		Trigger: engine.NewStartInstance(now, nil),
		Events:  nil, // no events — maybeNotify must short-circuit here
	})
	require.NoError(t, err)
	assert.Equal(t, runtime.Token(1), tok, "first create must return token 1")
}

// TestStoreCommitOutboxPayloadMarshalError verifies that Commit returns an error
// when the outbox event payload cannot be marshaled to JSON. Covers writeOutbox
// error path in Commit (store.go lines 197-199).
func TestStoreCommitOutboxPayloadMarshalError(t *testing.T) {
	pool := database.RunTestDatabase(t)
	require.NoError(t, postgres.Migrate(t.Context(), pool))

	st := postgres.NewStore(pool)

	now := time.Now().UTC()
	tok, err := st.Create(t.Context(), runtime.AppliedStep{
		State: engine.InstanceState{
			InstanceID: "commit-outbox-err-1",
			DefID:      "d",
			DefVersion: 1,
			Status:     engine.StatusRunning,
			StartedAt:  now,
		},
		Trigger: engine.NewStartInstance(now, nil),
		Events:  nil,
	})
	require.NoError(t, err)

	// Commit with an unmarshalable event payload.
	_, err = st.Commit(t.Context(), tok, runtime.AppliedStep{
		State: engine.InstanceState{
			InstanceID: "commit-outbox-err-1",
			DefID:      "d",
			DefVersion: 1,
			Status:     engine.StatusRunning,
			StartedAt:  now,
		},
		Trigger: engine.NewStartInstance(now, nil),
		Events: []runtime.OutboxEvent{
			{Topic: "bad.event", Payload: map[string]any{"ch": make(chan struct{})}},
		},
	})
	require.Error(t, err, "Commit must return an error when event payload cannot be marshaled")
	assert.Contains(t, err.Error(), "write outbox",
		"error must reference the outbox write operation")
}

// TestStoreCommitWithNotifyButNoEvents exercises the maybeNotify early-return
// in Commit when notify=true but the commit step has no outbox events.
func TestStoreCommitWithNotifyButNoEvents(t *testing.T) {
	pool := database.RunTestDatabase(t)
	require.NoError(t, postgres.Migrate(t.Context(), pool))

	st := postgres.NewStore(pool, postgres.WithOutboxNotify())

	now := time.Now().UTC()
	// First, create the instance (with an event so this step exercises the notify path).
	tok, err := st.Create(t.Context(), runtime.AppliedStep{
		State: engine.InstanceState{
			InstanceID: "notify-noev-commit-1",
			DefID:      "d",
			DefVersion: 1,
			Status:     engine.StatusRunning,
			StartedAt:  now,
		},
		Trigger: engine.NewStartInstance(now, nil),
		Events:  []runtime.OutboxEvent{{Topic: "instance.started", Payload: map[string]any{"id": "notify-noev-commit-1"}}},
	})
	require.NoError(t, err)

	// Commit with no events — maybeNotify must short-circuit on len(events)==0.
	next, err := st.Commit(t.Context(), tok, runtime.AppliedStep{
		State: engine.InstanceState{
			InstanceID: "notify-noev-commit-1",
			DefID:      "d",
			DefVersion: 1,
			Status:     engine.StatusRunning,
			StartedAt:  now,
		},
		Trigger: engine.NewStartInstance(now, nil),
		Events:  nil, // empty events — maybeNotify early-return
	})
	require.NoError(t, err)
	assert.Greater(t, int64(next), int64(tok), "Commit must advance the token")
}

// TestStoreCreateWithoutNotify exercises the maybeNotify path where notify=false.
// Even if events are non-empty, the !s.notify branch short-circuits without issuing NOTIFY.
func TestStoreCreateWithoutNotify(t *testing.T) {
	pool := database.RunTestDatabase(t)
	require.NoError(t, postgres.Migrate(t.Context(), pool))

	// No WithOutboxNotify — notify is false.
	st := postgres.NewStore(pool)

	now := time.Now().UTC()
	tok, err := st.Create(t.Context(), runtime.AppliedStep{
		State: engine.InstanceState{
			InstanceID: "no-notify-create-1",
			DefID:      "d",
			DefVersion: 1,
			Status:     engine.StatusRunning,
			StartedAt:  now,
		},
		Trigger: engine.NewStartInstance(now, nil),
		// Non-empty events; maybeNotify must short-circuit on !s.notify.
		Events: []runtime.OutboxEvent{{Topic: "instance.started", Payload: map[string]any{"id": "no-notify-create-1"}}},
	})
	require.NoError(t, err)
	assert.Equal(t, runtime.Token(1), tok, "Create without notify must succeed")
}

// TestStoreCreateOutboxPayloadMarshalError verifies that Create returns an error
// when the outbox event payload cannot be marshaled to JSON. This covers the
// json.Marshal error path in writeOutbox (line 259-261 in store.go).
func TestStoreCreateOutboxPayloadMarshalError(t *testing.T) {
	pool := database.RunTestDatabase(t)
	require.NoError(t, postgres.Migrate(t.Context(), pool))

	st := postgres.NewStore(pool)

	now := time.Now().UTC()
	// Channels cannot be marshaled to JSON, which will cause writeOutbox to fail.
	_, err := st.Create(t.Context(), runtime.AppliedStep{
		State: engine.InstanceState{
			InstanceID: "outbox-marshal-err-1",
			DefID:      "d",
			DefVersion: 1,
			Status:     engine.StatusRunning,
			StartedAt:  now,
		},
		Trigger: engine.NewStartInstance(now, nil),
		Events: []runtime.OutboxEvent{
			{Topic: "bad.event", Payload: map[string]any{"ch": make(chan struct{})}},
		},
	})
	require.Error(t, err, "Create must return an error when event payload cannot be marshaled")
	assert.Contains(t, err.Error(), "write outbox",
		"error must reference the outbox write operation")
}
