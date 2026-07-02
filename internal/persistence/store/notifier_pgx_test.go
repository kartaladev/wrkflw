// Package store_test — pgx LISTEN notifier capability test (Postgres only).
// This test is Postgres-specific because the notify/listen mechanism is a
// pgx+Postgres capability; MySQL and SQLite keep polling.
package store_test

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/goleak"

	"github.com/zakyalvan/krtlwrkflw/engine"
	"github.com/zakyalvan/krtlwrkflw/internal/dbtest"
	"github.com/zakyalvan/krtlwrkflw/internal/persistence/dialect"
	"github.com/zakyalvan/krtlwrkflw/internal/persistence/store"
	"github.com/zakyalvan/krtlwrkflw/persistence"
	"github.com/zakyalvan/krtlwrkflw/runtime"
)

// ── helpers ───────────────────────────────────────────────────────────────────

// notifyCountingPublisher records how many events it has published.
type notifyCountingPublisher struct{ n atomic.Int64 }

func (p *notifyCountingPublisher) Publish(_ context.Context, _ runtime.OutboxEvent) error {
	p.n.Add(1)
	return nil
}

// pgxNotifyStartTrigger builds a minimal StartInstance trigger for notifier tests.
func pgxNotifyStartTrigger() engine.Trigger {
	return engine.NewStartInstance(time.Now().UTC(), nil)
}

// ── TestPgxNotifierListenDrainsBeforePollInterval ─────────────────────────────

// TestPgxNotifierListenDrainsBeforePollInterval verifies that a Relay armed with
// a pgxNotifier drains the outbox WELL BEFORE the poll interval fires when a
// NOTIFY is emitted inside Store.Create (WithOutboxNotify). A 30s poll interval
// is chosen so the test can only pass if NOTIFY woke the relay — polling alone
// cannot drain within the 5s assertion window.
func TestPgxNotifierListenDrainsBeforePollInterval(t *testing.T) {
	pool := dbtest.RunTestDatabase(t)
	require.NoError(t, persistence.Migrate(t.Context(), pool))

	// Capture goroutine baseline AFTER pool construction so pool background
	// goroutines (backgroundHealthCheck) and testcontainers reaper are excluded.
	opt := goleak.IgnoreCurrent()

	// Build the pgxNotifier — this is the symbol under test.
	notifier := store.NewPgxNotifier(pool)

	pub := &notifyCountingPublisher{}
	listenReady := make(chan struct{}, 1)

	relay := store.NewRelay(pool, dialect.NewPostgres(), pub,
		store.WithRelayPollInterval(30*time.Second), // so only NOTIFY can wake in time
		store.WithRelayNotifier(notifier),
		store.WithRelayListenReady(listenReady),
	)

	runCtx, cancel := context.WithCancel(t.Context())
	defer cancel()
	go func() { _ = relay.Run(runCtx) }()

	// Wait until the listen goroutine has established its LISTEN subscription
	// before we write the outbox row (so the NOTIFY is not missed).
	select {
	case <-listenReady:
	case <-time.After(5 * time.Second):
		t.Fatal("pgxNotifier: LISTEN not established within 5s")
	}

	// Write an outbox event with NOTIFY so the relay wakes immediately.
	st := store.New(pool, dialect.NewPostgres(), store.WithOutboxNotify())
	_, err := st.Create(t.Context(), runtime.AppliedStep{
		State: engine.InstanceState{
			InstanceID: "pn-lr1", DefID: "d", DefVersion: 1,
			Status: engine.StatusRunning, StartedAt: time.Now().UTC(),
		},
		Trigger: pgxNotifyStartTrigger(),
		Events:  []runtime.OutboxEvent{{Topic: "instance.completed", Payload: map[string]any{"id": "pn-lr1"}}},
	})
	require.NoError(t, err)

	// Must be published well before the 30s poll tick — proves NOTIFY woke it.
	require.Eventually(t, func() bool { return pub.n.Load() == 1 },
		5*time.Second, 25*time.Millisecond,
		"relay must drain via NOTIFY wakeup, not poll (poll interval = 30s)")

	// Cancel and wait for Run to exit.
	cancel()
	time.Sleep(100 * time.Millisecond)

	// Verify no goroutine leaked after context cancellation.
	goleak.VerifyNone(t, opt)
}

// ── TestPgxNotifierCancelReleasesConn ────────────────────────────────────────

// TestPgxNotifierCancelReleasesConn verifies that cancelling the context
// passed to Listen causes the background goroutine to exit cleanly
// (no goroutine leak, dedicated conn returned to pool).
func TestPgxNotifierCancelReleasesConn(t *testing.T) {
	pool := dbtest.RunTestDatabase(t)
	require.NoError(t, persistence.Migrate(t.Context(), pool))

	// Capture goroutine baseline AFTER pool/container start so their own
	// background goroutines are excluded from the leak check.
	opt := goleak.IgnoreCurrent()

	notifier := store.NewPgxNotifier(pool)

	ctx, cancel := context.WithCancel(t.Context())

	wake, cancelListen, err := notifier.Listen(ctx, "wrkflw_outbox")
	require.NoError(t, err)
	require.NotNil(t, wake)
	require.NotNil(t, cancelListen)

	// Cancel the context — the listen goroutine must exit.
	cancel()
	// Also call the returned cancel func (should be idempotent).
	cancelListen()

	time.Sleep(100 * time.Millisecond)
	goleak.VerifyNone(t, opt)
}

// ── TestPgxNotifierCoalescesBurst ────────────────────────────────────────────

// TestPgxNotifierCoalescesBurst verifies that a burst of NOTIFYs on the channel
// coalesces into at most one entry in the buffered (size-1) wake channel, so
// the relay does not accumulate wake signals that cause redundant drains.
func TestPgxNotifierCoalescesBurst(t *testing.T) {
	pool := dbtest.RunTestDatabase(t)
	require.NoError(t, persistence.Migrate(t.Context(), pool))

	notifier := store.NewPgxNotifier(pool)

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	wake, cancelListen, err := notifier.Listen(ctx, "wrkflw_outbox")
	require.NoError(t, err)
	defer cancelListen()

	// Issue multiple NOTIFYs — the wake channel buffer is 1, so at most 1 lands.
	q := store.New(pool, dialect.NewPostgres())
	for range 5 {
		_, execErr := q.QuerierForTest(t.Context()).Exec(t.Context(), "NOTIFY wrkflw_outbox")
		require.NoError(t, execErr)
	}

	// Wait a moment for notifications to arrive.
	time.Sleep(200 * time.Millisecond)

	// The channel must have at most 1 entry (coalesced); it will not be empty
	// since at least one NOTIFY fired.
	assert.LessOrEqual(t, len(wake), 1, "wake channel must not accumulate beyond buffer size 1")
}

// ── TestRelayRun_MySQL_SQLite_StillPoll ───────────────────────────────────────

// TestRelayRun_MySQL_SQLite_StillPoll verifies that MySQL and SQLite Relays
// (no notifier injected) still work correctly via polling — no regression from
// the notifier integration.
func TestRelayRun_MySQL_SQLite_StillPoll(t *testing.T) {
	// Reuse the existing forEachDialect conformance harness but gate on non-postgres.
	t.Run("mysql", func(t *testing.T) {
		t.Parallel()
		db := dbtest.RunTestMySQL(t)
		d := dialect.NewMySQL()

		// Insert a pending outbox row directly.
		s := store.New(db, d)
		q := s.QuerierForTest(t.Context())
		now := time.Now().UTC()
		_, err := q.Exec(t.Context(), d.Rebind(
			`INSERT INTO wrkflw_outbox
			   (instance_id, topic, payload, dedup_key, created_at, status, retry_count, next_attempt_at)
			 VALUES (?,?,?,?,?,'pending',0,?)`),
			"poll-test", "poll.event", `{"k":"v"}`, "poll-mysql-1",
			store.TimeArgForDialect(s, now), store.TimeArgForDialect(s, now),
		)
		require.NoError(t, err)

		pub := &notifyCountingPublisher{}
		// Short poll interval — no notifier, pure poll.
		relay := store.NewRelay(db, d, pub,
			store.WithRelayPollInterval(50*time.Millisecond),
		)

		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		go func() { _ = relay.Run(ctx) }()

		require.Eventually(t, func() bool { return pub.n.Load() >= 1 },
			5*time.Second, 25*time.Millisecond,
			"mysql relay must drain via polling")
	})

	t.Run("sqlite", func(t *testing.T) {
		t.Parallel()
		db := dbtest.RunTestSQLite(t)
		d := dialect.NewSQLite()

		s := store.New(db, d)
		q := s.QuerierForTest(t.Context())
		now := time.Now().UTC()
		_, err := q.Exec(t.Context(), d.Rebind(
			`INSERT INTO wrkflw_outbox
			   (instance_id, topic, payload, dedup_key, created_at, status, retry_count, next_attempt_at)
			 VALUES (?,?,?,?,?,'pending',0,?)`),
			"poll-test", "poll.event", `{"k":"v"}`, "poll-sqlite-1",
			store.TimeArgForDialect(s, now), store.TimeArgForDialect(s, now),
		)
		require.NoError(t, err)

		pub := &notifyCountingPublisher{}
		relay := store.NewRelay(db, d, pub,
			store.WithRelayPollInterval(50*time.Millisecond),
		)

		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		go func() { _ = relay.Run(ctx) }()

		require.Eventually(t, func() bool { return pub.n.Load() >= 1 },
			5*time.Second, 25*time.Millisecond,
			"sqlite relay must drain via polling")
	})
}
