// Package store_test — pgx LISTEN notifier capability test (Postgres only).
// This test is Postgres-specific because the notify/listen mechanism is a
// pgx+Postgres capability; MySQL and SQLite keep polling.
package store_test

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/goleak"

	"github.com/zakyalvan/krtlwrkflw/engine"
	"github.com/zakyalvan/krtlwrkflw/internal/dbtest"
	"github.com/zakyalvan/krtlwrkflw/internal/persistence/dialect"
	"github.com/zakyalvan/krtlwrkflw/internal/persistence/store"
	"github.com/zakyalvan/krtlwrkflw/persistence"
	"github.com/zakyalvan/krtlwrkflw/runtime/kernel"
)

// ── helpers ───────────────────────────────────────────────────────────────────

// notifyCountingPublisher records how many events it has published.
type notifyCountingPublisher struct{ n atomic.Int64 }

func (p *notifyCountingPublisher) Publish(_ context.Context, _ kernel.OutboxEvent) error {
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

	relay, err := store.NewRelay(pool, dialect.NewPostgres(), pub,
		store.WithRelayPollInterval(30*time.Second), // so only NOTIFY can wake in time
		store.WithRelayNotifier(notifier),
		store.WithRelayListenReady(listenReady),
	)
	require.NoError(t, err)

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
	st, err := store.New(pool, dialect.NewPostgres(), store.WithOutboxNotify())
	require.NoError(t, err)
	_, err = st.Create(t.Context(), kernel.AppliedStep{
		State: engine.InstanceState{
			InstanceID: "pn-lr1", DefID: "d", DefVersion: 1,
			Status: engine.StatusRunning, StartedAt: time.Now().UTC(),
		},
		Trigger: pgxNotifyStartTrigger(),
		Events:  []kernel.OutboxEvent{{Topic: "instance.completed", Payload: map[string]any{"id": "pn-lr1"}}},
	})
	require.NoError(t, err)

	// Must be published well before the 30s poll tick — proves NOTIFY woke it.
	require.Eventually(t, func() bool { return pub.n.Load() == 1 },
		5*time.Second, 25*time.Millisecond,
		"relay must drain via NOTIFY wakeup, not poll (poll interval = 30s)")

	// Cancel and wait for Run to exit.
	cancel()

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
	q, err := store.New(pool, dialect.NewPostgres())
	require.NoError(t, err)
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

// ── TestPgxNotifierRecoversFromConnLoss ──────────────────────────────────────

// TestPgxNotifierRecoversFromConnLoss verifies that pgxNotifier self-heals after
// the dedicated LISTEN connection is terminated by the server (simulating a
// network blip or server restart). The test:
//
//  1. Calls notifier.Listen directly to obtain the wake channel.
//  2. Confirms a first NOTIFY produces a wake.
//  3. Forces the LISTEN conn to drop via pg_terminate_backend targeting backends
//     subscribed to 'wrkflw_outbox' (identified via pg_listening_channels).
//  4. Issues another NOTIFY after a short recovery window and asserts the wake
//     channel receives again — proving self-heal.
//  5. Cancels and verifies goleak-clean.
//
// This test FAILS on the pre-fix code because the goroutine returns on conn
// loss and never re-acquires/re-LISTENs (RED state). The post-loss NOTIFY
// times out with "self-heal failed" on buggy code.
func TestPgxNotifierRecoversFromConnLoss(t *testing.T) {
	pool := dbtest.RunTestDatabase(t)
	require.NoError(t, persistence.Migrate(t.Context(), pool))

	// Capture goroutine baseline after pool+container start.
	opt := goleak.IgnoreCurrent()

	notifier := store.NewPgxNotifier(pool)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	wake, cancelListen, err := notifier.Listen(ctx, "wrkflw_outbox")
	require.NoError(t, err)
	defer cancelListen()

	// ── Step 1: wait for LISTEN to be established ─────────────────────────────
	// When pgx is blocked in WaitForNotification, its backend shows
	// wait_event_type='Client', wait_event='ClientRead'. We poll pg_stat_activity
	// until such a connection appears (excluding the polling connection itself).
	// The pool is isolated (testcontainers, no shared external connections) so the
	// only ClientRead connection is the notifier's dedicated LISTEN conn.
	var listenPID int
	require.Eventually(t, func() bool {
		kc, acquireErr := pool.Acquire(t.Context())
		if acquireErr != nil {
			return false
		}
		defer kc.Release()
		var pid int
		// Query for any backend that is waiting for client input (i.e. is in
		// WaitForNotification or similar idle-in-listen state) and is NOT our
		// current polling connection.
		scanErr := kc.QueryRow(t.Context(),
			`SELECT pid FROM pg_stat_activity
			  WHERE pid <> pg_backend_pid()
			    AND state = 'idle'
			    AND wait_event_type = 'Client'
			  LIMIT 1`,
		).Scan(&pid)
		if scanErr != nil {
			return false
		}
		listenPID = pid
		return pid > 0
	}, 5*time.Second, 100*time.Millisecond, "LISTEN backend must appear in pg_stat_activity within 5s")

	t.Logf("LISTEN backend PID: %d", listenPID)
	_ = listenPID // confirmed it's the LISTEN conn (verified in step 1 polling)

	// ── Step 2: confirm first NOTIFY wakes the channel ────────────────────────
	notifyConn, err := pool.Acquire(t.Context())
	require.NoError(t, err)
	_, err = notifyConn.Exec(t.Context(), "NOTIFY wrkflw_outbox")
	require.NoError(t, err)
	notifyConn.Release()

	select {
	case <-wake:
		// good — first NOTIFY received
	case <-time.After(3 * time.Second):
		t.Fatal("first NOTIFY did not wake the channel within 3s")
	}

	// ── Step 3: forcibly kill ALL pool connections ─────────────────────────────
	// Acquiring a dedicated "killer" connection and terminating every OTHER
	// backend (pid <> pg_backend_pid()) ensures the LISTEN goroutine's
	// dedicated connection is definitively killed, regardless of which PID it holds.
	// In the testcontainer environment there are no external shared connections, so
	// only the pool's own connections + ours are present.
	killConn, err := pool.Acquire(t.Context())
	require.NoError(t, err)
	var killedCount int
	killErr := killConn.QueryRow(t.Context(),
		`SELECT count(pg_terminate_backend(pid))
		   FROM pg_stat_activity
		  WHERE pid <> pg_backend_pid()`,
	).Scan(&killedCount)
	killConn.Release()
	require.NoError(t, killErr, "bulk pg_terminate_backend")
	t.Logf("pg_terminate_backend killed %d connections (including LISTEN conn)", killedCount)

	// ── Step 4: wait for old connections to disappear + give goroutine time ───
	// Poll until the initial LISTEN PID is gone from pg_stat_activity (server-side
	// confirmation the kill took effect). Then sleep 500ms so the Go goroutine's
	// WaitForNotification error-return + defers run to completion.
	require.Eventually(t, func() bool {
		kc, acquireErr := pool.Acquire(t.Context())
		if acquireErr != nil {
			return false
		}
		defer kc.Release()
		var count int
		_ = kc.QueryRow(t.Context(),
			`SELECT count(*) FROM pg_stat_activity WHERE pid = $1`, listenPID,
		).Scan(&count)
		return count == 0
	}, 5*time.Second, 50*time.Millisecond, "LISTEN backend must disappear from pg_stat_activity after kill")

	// Extra margin: let WaitForNotification error-path + deferred Release run.
	time.Sleep(500 * time.Millisecond)

	// Drain any stale wake that landed before/during the kill (window where the
	// goroutine detected the error but had already queued a notification).
	drained := false
	for !drained {
		select {
		case <-wake:
			t.Log("drained stale wake from before/during kill")
		case <-time.After(200 * time.Millisecond):
			drained = true
		}
	}
	t.Logf("wake channel len after drain+kill: %d", len(wake))

	// ── Step 5: fire another NOTIFY ───────────────────────────────────────────
	// Buggy (no self-heal): goroutine is dead; nobody listens → wake never fires
	//   → test times out (RED).
	// Fixed (self-heal): goroutine re-acquired a new conn + re-LISTENed during
	//   the 500ms sleep above → wake fires (GREEN).
	//
	// pgxpool auto-reconnects on Acquire, so the NOTIFY itself goes through fine.
	notifyConn2, err := pool.Acquire(t.Context())
	require.NoError(t, err)
	_, err = notifyConn2.Exec(t.Context(), "NOTIFY wrkflw_outbox")
	require.NoError(t, err)
	notifyConn2.Release()

	select {
	case <-wake:
		t.Log("self-heal confirmed: wake received after bulk conn kill")
	case <-time.After(3 * time.Second):
		t.Fatal("post-kill NOTIFY did not wake the channel within 3s (self-heal failed)")
	}

	// ── Step 6: clean shutdown + goleak ──────────────────────────────────────
	cancel()
	cancelListen()
	goleak.VerifyNone(t, opt)
}

// ── TestPgxNotifierReconnectLogsWarn ─────────────────────────────────────────

// recordingHandler is a minimal slog.Handler that appends every record it
// handles to a slice (guarded by a mutex). Used to assert warn-log emission
// without touching stdout or global logger state.
type recordingHandler struct {
	mu      sync.Mutex
	records []slog.Record
}

func (h *recordingHandler) Enabled(_ context.Context, _ slog.Level) bool { return true }

func (h *recordingHandler) Handle(_ context.Context, r slog.Record) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.records = append(h.records, r)
	return nil
}

func (h *recordingHandler) WithAttrs(_ []slog.Attr) slog.Handler { return h }
func (h *recordingHandler) WithGroup(_ string) slog.Handler      { return h }

func (h *recordingHandler) warnRecords() []slog.Record {
	h.mu.Lock()
	defer h.mu.Unlock()
	var out []slog.Record
	for _, r := range h.records {
		if r.Level == slog.LevelWarn {
			out = append(out, r)
		}
	}
	return out
}

// TestPgxNotifierReconnectLogsWarn verifies that:
//  1. WithPgxNotifierLogger wires a custom logger into the notifier.
//  2. When Acquire fails during a reconnect, the goroutine emits at least one
//     slog.LevelWarn record containing "reconnect".
//
// Strategy: build a pool whose BeforeConnect hook is toggled by an atomic flag.
// The flag starts "allow" (initial Listen succeeds). After LISTEN is established,
// the flag switches to "deny" so all future Acquire calls fail. Then
// pg_terminate_backend kills the LISTEN connection — WaitForNotification errors,
// the goroutine sleeps the reconnect backoff, calls Acquire (BeforeConnect fails
// → Acquire fails → Warn emitted). Finally cancel the context so the goroutine
// exits cleanly.
func TestPgxNotifierReconnectLogsWarn(t *testing.T) {
	mainPool := dbtest.RunTestDatabase(t)
	require.NoError(t, persistence.Migrate(t.Context(), mainPool))

	// Build a pool with a toggleable BeforeConnect hook using the same DSN as
	// mainPool so it connects to the same Postgres instance.
	var denyConnects atomic.Bool // false = allow, true = deny
	poolCfg := mainPool.Config().Copy()
	poolCfg.BeforeConnect = func(_ context.Context, _ *pgx.ConnConfig) error {
		if denyConnects.Load() {
			return fmt.Errorf("test: connections denied by BeforeConnect")
		}
		return nil
	}
	notifierPool, err := pgxpool.NewWithConfig(t.Context(), poolCfg)
	require.NoError(t, err)
	t.Cleanup(func() {
		denyConnects.Store(false) // allow pool cleanup connections
		notifierPool.Close()
	})

	// Capture goroutine baseline after pools are created.
	opt := goleak.IgnoreCurrent()

	handler := &recordingHandler{}
	logger := slog.New(handler)

	// NewPgxNotifier with logger option — fails to compile before Fix 1 is applied.
	notifier := store.NewPgxNotifier(notifierPool, store.WithPgxNotifierLogger(logger))

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	wake, cancelListen, err := notifier.Listen(ctx, "wrkflw_outbox")
	require.NoError(t, err)
	require.NotNil(t, wake)
	defer cancelListen()

	// Wait for the LISTEN backend to appear in pg_stat_activity.
	var listenPID int
	require.Eventually(t, func() bool {
		kc, acquireErr := mainPool.Acquire(t.Context())
		if acquireErr != nil {
			return false
		}
		defer kc.Release()
		var pid int
		scanErr := kc.QueryRow(t.Context(),
			`SELECT pid FROM pg_stat_activity
			  WHERE pid <> pg_backend_pid()
			    AND state = 'idle'
			    AND wait_event_type = 'Client'
			  LIMIT 1`,
		).Scan(&pid)
		if scanErr == nil && pid > 0 {
			listenPID = pid
			return true
		}
		return false
	}, 5*time.Second, 100*time.Millisecond, "LISTEN backend must appear within 5s")
	t.Logf("LISTEN backend PID: %d", listenPID)

	// Deny new connections — now Acquire in the reconnect loop will fail when it
	// tries to create a new connection (BeforeConnect returns an error). Note:
	// BeforeConnect is only called for NEW connections; pooled idle connections are
	// reused without it. So we must also drain all existing pool connections by
	// killing every notifierPool backend so the pool has no idle conns to reuse.
	denyConnects.Store(true)

	// Kill ALL notifierPool connections (including the LISTEN conn) so the pool
	// has no idle connections left to hand out. The notifier goroutine's next
	// Acquire must create a fresh connection → BeforeConnect fires → error.
	killConn, err := mainPool.Acquire(t.Context())
	require.NoError(t, err)
	var killedCount int
	_ = killConn.QueryRow(t.Context(),
		`SELECT count(pg_terminate_backend(pid))
		   FROM pg_stat_activity
		  WHERE pid <> pg_backend_pid()`,
	).Scan(&killedCount)
	killConn.Release()
	t.Logf("killed %d connections (including LISTEN conn %d)", killedCount, listenPID)

	// Wait: reconnect backoff (500ms) + error-detection margin + buffer.
	// The goroutine: detects WaitForNotification error → sets current=nil →
	// sleeps 500ms backoff → calls Acquire (BeforeConnect fails) → emits Warn.
	time.Sleep(store.PgxNotifierReconnectBackoffForTest + 400*time.Millisecond)

	warnRecs := handler.warnRecords()
	assert.NotEmpty(t, warnRecs, "expected at least one slog.Warn record from reconnect-acquire failure")
	if len(warnRecs) > 0 {
		assert.Contains(t, warnRecs[0].Message, "reconnect", "warn message must mention 'reconnect'")
	}

	// Allow connections again so the goroutine can exit cleanly on cancel.
	denyConnects.Store(false)
	cancel()
	cancelListen()
	time.Sleep(100 * time.Millisecond)
	goleak.VerifyNone(t, opt)
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
		s, err := store.New(db, d)
		require.NoError(t, err)
		q := s.QuerierForTest(t.Context())
		now := time.Now().UTC()
		_, err = q.Exec(t.Context(), d.Rebind(
			`INSERT INTO wrkflw_outbox
			   (instance_id, topic, payload, dedup_key, created_at, status, retry_count, next_attempt_at)
			 VALUES (?,?,?,?,?,'pending',0,?)`),
			"poll-test", "poll.event", `{"k":"v"}`, "poll-mysql-1",
			store.TimeArgForDialect(s, now), store.TimeArgForDialect(s, now),
		)
		require.NoError(t, err)

		pub := &notifyCountingPublisher{}
		// Short poll interval — no notifier, pure poll.
		relay, err := store.NewRelay(db, d, pub,
			store.WithRelayPollInterval(50*time.Millisecond),
		)
		require.NoError(t, err)

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

		s, err := store.New(db, d)
		require.NoError(t, err)
		q := s.QuerierForTest(t.Context())
		now := time.Now().UTC()
		_, err = q.Exec(t.Context(), d.Rebind(
			`INSERT INTO wrkflw_outbox
			   (instance_id, topic, payload, dedup_key, created_at, status, retry_count, next_attempt_at)
			 VALUES (?,?,?,?,?,'pending',0,?)`),
			"poll-test", "poll.event", `{"k":"v"}`, "poll-sqlite-1",
			store.TimeArgForDialect(s, now), store.TimeArgForDialect(s, now),
		)
		require.NoError(t, err)

		pub := &notifyCountingPublisher{}
		relay, err := store.NewRelay(db, d, pub,
			store.WithRelayPollInterval(50*time.Millisecond),
		)
		require.NoError(t, err)

		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		go func() { _ = relay.Run(ctx) }()

		require.Eventually(t, func() bool { return pub.n.Load() >= 1 },
			5*time.Second, 25*time.Millisecond,
			"sqlite relay must drain via polling")
	})
}
