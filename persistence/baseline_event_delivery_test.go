package persistence_test

// baseline_event_delivery_test.go characterizes the event-delivery relay
// scenarios (openspec/specs/event-delivery) through the relays the persistence
// package's PUBLIC constructors build — NewRelay, NewMySQLRelay and
// NewSQLiteRelay — on real Postgres and MySQL containers and in-process SQLite.
// The shared relay behind them is covered in internal/persistence/store; these
// tests pin that the public constructors hand consumers the same behaviour.

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/jonboulle/clockwork"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kartaladev/wrkflw/engine"
	"github.com/kartaladev/wrkflw/internal/dbtest"
	"github.com/kartaladev/wrkflw/persistence"
	"github.com/kartaladev/wrkflw/runtime/kernel"
)

// ── publishers ───────────────────────────────────────────────────────────────

// baselineRelayPub records every published event and fails the ones whose
// dedup key is in fail.
type baselineRelayPub struct {
	mu      sync.Mutex
	fail    map[string]bool
	failAll bool
	events  []kernel.OutboxEvent
	calls   map[string]int
}

func newBaselineRelayPub(failKeys ...string) *baselineRelayPub {
	p := &baselineRelayPub{fail: map[string]bool{}, calls: map[string]int{}}
	for _, k := range failKeys {
		p.fail[k] = true
	}
	return p
}

func newFailingBaselineRelayPub() *baselineRelayPub {
	p := newBaselineRelayPub()
	p.failAll = true
	return p
}

func (p *baselineRelayPub) Publish(_ context.Context, ev kernel.OutboxEvent) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.calls[ev.DedupKey]++
	if p.failAll || p.fail[ev.DedupKey] {
		return errors.New("baseline: publish failed")
	}
	p.events = append(p.events, ev)
	return nil
}

// published returns the number of successful publishes.
func (p *baselineRelayPub) published() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.events)
}

// publishedKey reports whether an event with dedup key was published.
func (p *baselineRelayPub) publishedKey(key string) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, ev := range p.events {
		if ev.DedupKey == key {
			return true
		}
	}
	return false
}

// publishedTopic reports whether an event on topic was published.
func (p *baselineRelayPub) publishedTopic(topic string) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, ev := range p.events {
		if ev.Topic == topic {
			return true
		}
	}
	return false
}

// callsFor returns how many times Publish was attempted for dedup key.
func (p *baselineRelayPub) callsFor(key string) int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.calls[key]
}

// ── per-dialect outbox harness ───────────────────────────────────────────────

// baselineOutbox is one dialect's outbox: a database/sql handle for seeding and
// reading rows, plus the persistence package's public relay constructor.
type baselineOutbox struct {
	name     string
	db       *sql.DB
	postgres bool
	sqlite   bool
	newRelay func(pub kernel.OutboxPublisher, opts ...persistence.RelayOption) (persistence.Relay, error)
}

// forEachBaselineOutbox runs fn once per SQL dialect over a fresh migrated
// database, mirroring the dialect harness of the neighbouring conformance tests.
func forEachBaselineOutbox(t *testing.T, fn func(t *testing.T, o baselineOutbox)) {
	t.Helper()
	forEachBaselineOutboxOf(t, []string{"postgres", "mysql", "sqlite"}, fn)
}

// forEachBaselineOutboxOf is forEachBaselineOutbox restricted to dialects.
func forEachBaselineOutboxOf(t *testing.T, dialects []string, fn func(t *testing.T, o baselineOutbox)) {
	t.Helper()
	want := func(name string) bool { return slices.Contains(dialects, name) }

	if want("postgres") {
		t.Run("postgres", func(t *testing.T) {
			t.Parallel()
			pool := dbtest.RunTestDatabase(t)
			require.NoError(t, persistence.Migrate(t.Context(), pool))
			fn(t, baselineOutbox{
				name:     "postgres",
				db:       baselinePostgresDB(t, pool),
				postgres: true,
				newRelay: func(pub kernel.OutboxPublisher, opts ...persistence.RelayOption) (persistence.Relay, error) {
					return persistence.NewRelay(pool, pub, opts...)
				},
			})
		})
	}

	if want("mysql") {
		t.Run("mysql", func(t *testing.T) {
			t.Parallel()
			db := dbtest.RunTestMySQL(t) // already migrated
			fn(t, baselineOutbox{
				name: "mysql",
				db:   db,
				newRelay: func(pub kernel.OutboxPublisher, opts ...persistence.RelayOption) (persistence.Relay, error) {
					return persistence.NewMySQLRelay(db, pub, opts...)
				},
			})
		})
	}

	if want("sqlite") {
		t.Run("sqlite", func(t *testing.T) {
			t.Parallel()
			db := dbtest.RunTestSQLite(t) // already migrated
			fn(t, baselineOutbox{
				name:   "sqlite",
				db:     db,
				sqlite: true,
				newRelay: func(pub kernel.OutboxPublisher, opts ...persistence.RelayOption) (persistence.Relay, error) {
					return persistence.NewSQLiteRelay(db, pub, opts...)
				},
			})
		})
	}
}

// baselinePostgresDB wraps pool in a database/sql handle so every dialect's
// rows are seeded and read through one code path.
func baselinePostgresDB(t *testing.T, pool *pgxpool.Pool) *sql.DB {
	t.Helper()
	db := stdlib.OpenDBFromPool(pool)
	t.Cleanup(func() { _ = db.Close() })
	return db
}

// rebind rewrites ? placeholders to $n for Postgres.
func (o baselineOutbox) rebind(query string) string {
	if !o.postgres {
		return query
	}
	var b strings.Builder
	n := 0
	for _, r := range query {
		if r == '?' {
			n++
			fmt.Fprintf(&b, "$%d", n)
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

// timeArg encodes ts the way the dialect's outbox columns store it: SQLite
// keeps TEXT timestamps as UTC RFC3339 with a fixed nine-digit fraction.
func (o baselineOutbox) timeArg(ts time.Time) any {
	if o.sqlite {
		return ts.UTC().Format("2006-01-02T15:04:05.000000000Z07:00")
	}
	return ts.UTC()
}

// seed inserts one outbox row and returns its id. lastErr is stored only for
// a non-empty value.
func (o baselineOutbox) seed(t *testing.T, dedup, status string, retry int, at time.Time, lastErr string) int64 {
	t.Helper()
	var le any
	if lastErr != "" {
		le = lastErr
	}
	_, err := o.db.ExecContext(t.Context(), o.rebind(
		`INSERT INTO wrkflw_outbox
		   (instance_id, topic, payload, dedup_key, created_at, status, retry_count, next_attempt_at, last_error)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`),
		"baseline-inst", "baseline.event", `{"k":"v"}`, dedup,
		o.timeArg(at), status, retry, o.timeArg(at), le,
	)
	require.NoError(t, err, "seed outbox row %s on %s", dedup, o.name)

	var id int64
	require.NoError(t, o.db.QueryRowContext(t.Context(),
		o.rebind(`SELECT id FROM wrkflw_outbox WHERE dedup_key = ?`), dedup).Scan(&id))
	return id
}

// statusAndRetry reads the status and retry count of the row with dedup.
func (o baselineOutbox) statusAndRetry(t *testing.T, dedup string) (string, int) {
	t.Helper()
	var (
		status string
		retry  int
	)
	require.NoError(t, o.db.QueryRowContext(t.Context(),
		o.rebind(`SELECT status, retry_count FROM wrkflw_outbox WHERE dedup_key = ?`), dedup).
		Scan(&status, &retry))
	return status, retry
}

// countStatus counts the rows in status.
func (o baselineOutbox) countStatus(t *testing.T, status string) int {
	t.Helper()
	var n int
	require.NoError(t, o.db.QueryRowContext(t.Context(),
		o.rebind(`SELECT COUNT(*) FROM wrkflw_outbox WHERE status = ?`), status).Scan(&n))
	return n
}

// startBaselineRun runs relay.Run on its own goroutine and returns a function
// that cancels it and returns Run's error. The same stop runs at cleanup, so
// no Run outlives its database.
func startBaselineRun(t *testing.T, relay persistence.Relay) (stop func() error) {
	t.Helper()
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- relay.Run(ctx) }()

	var (
		once   sync.Once
		runErr error
	)
	stop = func() error {
		once.Do(func() {
			cancel()
			select {
			case runErr = <-done:
			case <-time.After(2 * time.Second):
				runErr = errors.New("Run did not return within 2s of cancellation")
			}
		})
		return runErr
	}
	t.Cleanup(func() { _ = stop() })
	return stop
}

// ── scenarios ────────────────────────────────────────────────────────────────

// TestBaselineRelayDrainsEveryPendingRowViaFacade characterizes "A drain
// publishes every pending row on every dialect" through the public relays:
// DrainOnce returns the row count, the publisher received every row, no row
// remains pending and a second DrainOnce returns 0.
func TestBaselineRelayDrainsEveryPendingRowViaFacade(t *testing.T) {
	cases := []struct {
		name   string
		rows   int
		assert func(t *testing.T, o baselineOutbox, n int, err error, pub *baselineRelayPub)
	}{
		{
			name: "3 rows",
			rows: 3,
			assert: func(t *testing.T, o baselineOutbox, n int, err error, pub *baselineRelayPub) {
				require.NoError(t, err)
				assert.Equal(t, 3, n)
				assert.Equal(t, 3, pub.published())
				assert.Equal(t, 0, o.countStatus(t, "pending"))
			},
		},
		{
			name: "0 rows",
			rows: 0,
			assert: func(t *testing.T, o baselineOutbox, n int, err error, pub *baselineRelayPub) {
				require.NoError(t, err)
				assert.Equal(t, 0, n)
				assert.Equal(t, 0, pub.published())
				assert.Equal(t, 0, o.countStatus(t, "pending"))
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			forEachBaselineOutbox(t, func(t *testing.T, o baselineOutbox) {
				at := time.Now().UTC().Truncate(time.Second)
				for i := range tc.rows {
					o.seed(t, fmt.Sprintf("drain-%d", i), "pending", 0, at, "")
				}
				pub := newBaselineRelayPub()
				relay, err := o.newRelay(pub)
				require.NoError(t, err)

				n, err := relay.DrainOnce(t.Context())
				tc.assert(t, o, n, err, pub)

				again, err := relay.DrainOnce(t.Context())
				require.NoError(t, err)
				assert.Equal(t, 0, again, "a second DrainOnce publishes nothing")
			})
		})
	}
}

// TestBaselineRelayIsolatesAndQuarantinesAPoisonRowViaFacade characterizes
// "Poison isolation and quarantine on every dialect" through the public relays.
func TestBaselineRelayIsolatesAndQuarantinesAPoisonRowViaFacade(t *testing.T) {
	forEachBaselineOutbox(t, func(t *testing.T, o baselineOutbox) {
		base := time.Now().UTC().Truncate(time.Second)
		fc := clockwork.NewFakeClockAt(base)
		o.seed(t, "poison", "pending", 0, base, "")
		o.seed(t, "healthy", "pending", 0, base, "")

		const maxDelivery = 3
		pub := newBaselineRelayPub("poison")
		relay, err := o.newRelay(pub,
			persistence.WithRelayClock(fc),
			persistence.WithMaxDeliveryAttempts(maxDelivery),
			persistence.WithRelayBackoff(time.Second, 30*time.Second),
		)
		require.NoError(t, err)

		n, err := relay.DrainOnce(t.Context())
		require.NoError(t, err)
		assert.Equal(t, 1, n, "only the healthy row is published")
		hStatus, _ := o.statusAndRetry(t, "healthy")
		assert.Equal(t, "published", hStatus)
		pStatus, pRetry := o.statusAndRetry(t, "poison")
		assert.Equal(t, "pending", pStatus)
		assert.Equal(t, 1, pRetry)

		for attempt := 1; pRetry < maxDelivery; attempt++ {
			require.LessOrEqual(t, attempt, maxDelivery, "poison never reached %d attempts", maxDelivery)
			fc.Advance(2 * time.Minute)
			_, err = relay.DrainOnce(t.Context())
			require.NoError(t, err)
			pStatus, pRetry = o.statusAndRetry(t, "poison")
		}
		assert.Equal(t, "dead", pStatus, "poison is quarantined at %d attempts", maxDelivery)

		fc.Advance(2 * time.Minute)
		n, err = relay.DrainOnce(t.Context())
		require.NoError(t, err)
		assert.Equal(t, 0, n, "a dead row is no longer claimed")
		assert.Equal(t, maxDelivery, pub.callsFor("poison"), "poison is not attempted after quarantine")
		assert.Equal(t, 1, pub.callsFor("healthy"), "healthy published exactly once")
	})
}

// TestBaselineRelayDefersAFailedRowUntilItsBackoffElapses characterizes "A
// failed row is not claimed before its backoff elapses" through the public
// relays: a second DrainOnce before the backoff does not call the publisher for
// the row and leaves its retry count at 1. Advancing past the backoff is the
// negative control that the row was deferred rather than dropped.
func TestBaselineRelayDefersAFailedRowUntilItsBackoffElapses(t *testing.T) {
	forEachBaselineOutbox(t, func(t *testing.T, o baselineOutbox) {
		base := time.Now().UTC().Truncate(time.Second)
		fc := clockwork.NewFakeClockAt(base)
		o.seed(t, "flaky", "pending", 0, base, "")

		pub := newBaselineRelayPub("flaky")
		relay, err := o.newRelay(pub,
			persistence.WithRelayClock(fc),
			persistence.WithRelayBackoff(time.Minute, time.Hour),
		)
		require.NoError(t, err)

		_, err = relay.DrainOnce(t.Context())
		require.NoError(t, err)
		require.Equal(t, 1, pub.callsFor("flaky"), "first Publish failed")

		fc.Advance(time.Second) // well inside the one-minute backoff
		_, err = relay.DrainOnce(t.Context())
		require.NoError(t, err)
		assert.Equal(t, 1, pub.callsFor("flaky"), "the publisher is not called before the backoff elapses")
		_, retry := o.statusAndRetry(t, "flaky")
		assert.Equal(t, 1, retry, "retry count is still 1")

		fc.Advance(2 * time.Hour)
		_, err = relay.DrainOnce(t.Context())
		require.NoError(t, err)
		assert.Equal(t, 2, pub.callsFor("flaky"), "the row is claimed again once its backoff elapsed")
	})
}

// TestBaselineRelayAbsorbsAPublishFailureViaFacade characterizes "A publish
// failure is absorbed, not propagated" through the public relays.
func TestBaselineRelayAbsorbsAPublishFailureViaFacade(t *testing.T) {
	forEachBaselineOutbox(t, func(t *testing.T, o baselineOutbox) {
		o.seed(t, "doomed", "pending", 0, time.Now().UTC().Truncate(time.Second), "")

		relay, err := o.newRelay(newFailingBaselineRelayPub())
		require.NoError(t, err)

		n, err := relay.DrainOnce(t.Context())
		require.NoError(t, err, "a publish failure is absorbed")
		assert.Equal(t, 0, n)
		status, _ := o.statusAndRetry(t, "doomed")
		assert.Equal(t, "pending", status, "the row is still pending")
	})
}

// TestBaselineRelayRunAbsorbsFailuresAndExitsOnCancelViaFacade characterizes
// "Run absorbs failures and exits on cancel on every dialect" through the
// public relays.
func TestBaselineRelayRunAbsorbsFailuresAndExitsOnCancelViaFacade(t *testing.T) {
	forEachBaselineOutbox(t, func(t *testing.T, o baselineOutbox) {
		o.seed(t, "doomed", "pending", 0, time.Now().UTC().Truncate(time.Second), "")

		relay, err := o.newRelay(newFailingBaselineRelayPub(), persistence.WithPollInterval(10*time.Millisecond))
		require.NoError(t, err)

		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		done := make(chan error, 1)
		go func() { done <- relay.Run(ctx) }()

		// Waits for both asserted facts: the retry count reached 1 AND Run is
		// still running.
		require.Eventually(t, func() bool {
			select {
			case err := <-done:
				t.Errorf("Run terminated while publishes were failing: %v", err)
				return true
			default:
			}
			var retry int
			if err := o.db.QueryRowContext(t.Context(),
				o.rebind(`SELECT retry_count FROM wrkflw_outbox WHERE dedup_key = ?`), "doomed").Scan(&retry); err != nil {
				return false
			}
			return retry >= 1
		}, 5*time.Second, 20*time.Millisecond, "Run must keep retrying on %s", o.name)

		cancel()
		select {
		case err := <-done:
			assert.ErrorIs(t, err, context.Canceled)
		case <-time.After(2 * time.Second):
			t.Fatalf("Run did not return within 2s of cancellation on %s", o.name)
		}
	})
}

// TestBaselineRelayListsAndRedrivesDeadRowsViaFacade characterizes "Listing
// and redriving on every dialect" through the public relays.
func TestBaselineRelayListsAndRedrivesDeadRowsViaFacade(t *testing.T) {
	forEachBaselineOutbox(t, func(t *testing.T, o baselineOutbox) {
		at := time.Now().UTC().Truncate(time.Second)
		deadID := o.seed(t, "dead-row", "dead", 5, at, "boom")
		pendingID := o.seed(t, "pending-row", "pending", 0, at, "")

		pub := newBaselineRelayPub()
		relay, err := o.newRelay(pub)
		require.NoError(t, err)

		dead, err := relay.ListDeadLettered(t.Context(), 10)
		require.NoError(t, err)
		require.Len(t, dead, 1, "only the dead row is listed")
		assert.Equal(t, deadID, dead[0].ID)
		assert.Equal(t, "baseline.event", dead[0].Topic)
		assert.Equal(t, "baseline-inst", dead[0].InstanceID)
		assert.Equal(t, 5, dead[0].RetryCount)
		assert.Equal(t, "boom", dead[0].LastError)

		n, err := relay.Redrive(t.Context())
		require.NoError(t, err)
		assert.Equal(t, 0, n, "Redrive with no ids")

		n, err = relay.Redrive(t.Context(), pendingID)
		require.NoError(t, err)
		assert.Equal(t, 0, n, "Redrive ignores a pending row")

		n, err = relay.Redrive(t.Context(), deadID)
		require.NoError(t, err)
		assert.Equal(t, 1, n, "Redrive re-queues the dead row")
		status, _ := o.statusAndRetry(t, "dead-row")
		assert.Equal(t, "pending", status)

		_, err = relay.DrainOnce(t.Context())
		require.NoError(t, err)
		assert.True(t, pub.publishedKey("dead-row"), "the next DrainOnce publishes the redriven row")
	})
}

// TestBaselineRelayOutboxStatsViaFacade characterizes "Stats on every dialect"
// through the public relays.
func TestBaselineRelayOutboxStatsViaFacade(t *testing.T) {
	forEachBaselineOutbox(t, func(t *testing.T, o baselineOutbox) {
		at := time.Now().UTC().Add(-5 * time.Second)
		o.seed(t, "stats-pending-1", "pending", 0, at, "")
		o.seed(t, "stats-pending-2", "pending", 0, at, "")
		o.seed(t, "stats-dead-1", "dead", 5, at, "err")

		relay, err := o.newRelay(newBaselineRelayPub())
		require.NoError(t, err)

		stats, err := relay.OutboxStats(t.Context())
		require.NoError(t, err)
		assert.Equal(t, int64(2), stats.Pending)
		assert.Equal(t, int64(1), stats.Dead)
		assert.Greater(t, stats.OldestPendingAge, time.Duration(0))
	})
}

// TestBaselineRelayDrainsOnNotificationViaFacade characterizes "Postgres drains
// on notification before the poll interval" through persistence.WithOutboxNotify
// on the store and persistence.WithListenNotify on the relay.
func TestBaselineRelayDrainsOnNotificationViaFacade(t *testing.T) {
	t.Parallel()
	pool := dbtest.RunTestDatabase(t)
	require.NoError(t, persistence.Migrate(t.Context(), pool))
	o := baselineOutbox{name: "postgres", db: baselinePostgresDB(t, pool), postgres: true}

	st, err := persistence.OpenPostgres(t.Context(), pool, persistence.WithOutboxNotify())
	require.NoError(t, err)

	// A row present before Run: Run's start-up drain publishes it, which tells
	// the test that drain is over, so the row committed below can only be
	// reached by a notification (or by the 30s poll tick, which the budget
	// below rules out).
	o.seed(t, "before-run", "pending", 0, time.Now().UTC().Truncate(time.Second), "")

	pub := newBaselineRelayPub()
	relay, err := persistence.NewRelay(pool, pub,
		persistence.WithListenNotify(),
		persistence.WithPollInterval(30*time.Second),
	)
	require.NoError(t, err)
	stop := startBaselineRun(t, relay)

	// Wait for both preconditions: the start-up drain published the first row
	// AND the relay's LISTEN session is established.
	require.Eventually(t, func() bool {
		var listeners int
		if err := o.db.QueryRowContext(t.Context(),
			`SELECT COUNT(*) FROM pg_stat_activity
			  WHERE datname = current_database() AND query = 'LISTEN wrkflw_outbox'`).Scan(&listeners); err != nil {
			return false
		}
		return listeners >= 1 && pub.publishedKey("before-run")
	}, 5*time.Second, 20*time.Millisecond, "relay must finish its start-up drain and LISTEN")

	now := time.Now().UTC()
	_, err = st.Create(t.Context(), kernel.AppliedStep{
		State: engine.InstanceState{
			InstanceID: "notify-1", DefID: "d", DefVersion: 1,
			Status: engine.StatusRunning, StartedAt: now,
		},
		Trigger: engine.NewStartInstance(now, nil),
		Events:  []kernel.OutboxEvent{{Topic: "baseline.notified", Payload: map[string]any{"id": "notify-1"}}},
	})
	require.NoError(t, err)

	require.Eventually(t, func() bool { return pub.publishedTopic("baseline.notified") },
		5*time.Second, 20*time.Millisecond,
		"the relay must drain on NOTIFY, well before its 30s poll interval")
	require.ErrorIs(t, stop(), context.Canceled)
}

// TestBaselineRelayDrainsByPollingViaFacade characterizes "MySQL and SQLite
// still drain by polling" through the public relays. The second row is seeded
// only after Run's start-up drain has published the first, so only a poll tick
// can publish it.
func TestBaselineRelayDrainsByPollingViaFacade(t *testing.T) {
	forEachBaselineOutboxOf(t, []string{"mysql", "sqlite"}, func(t *testing.T, o baselineOutbox) {
		o.seed(t, "poll-1", "pending", 0, time.Now().UTC().Truncate(time.Second), "")

		pub := newBaselineRelayPub()
		relay, err := o.newRelay(pub, persistence.WithPollInterval(50*time.Millisecond))
		require.NoError(t, err)
		stop := startBaselineRun(t, relay)

		require.Eventually(t, func() bool { return pub.publishedKey("poll-1") },
			5*time.Second, 20*time.Millisecond, "the pending row is published on %s", o.name)

		o.seed(t, "poll-2", "pending", 0, time.Now().UTC().Truncate(time.Second), "")
		require.Eventually(t, func() bool { return pub.publishedKey("poll-2") },
			5*time.Second, 20*time.Millisecond, "a row committed while Run is idle is published by polling on %s", o.name)
		require.ErrorIs(t, stop(), context.Canceled)
	})
}
