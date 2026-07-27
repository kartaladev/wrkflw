package store_test

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/jonboulle/clockwork"
	"github.com/stretchr/testify/require"

	"github.com/kartaladev/wrkflw/internal/dbtest"
	"github.com/kartaladev/wrkflw/internal/persistence/dialect"
	"github.com/kartaladev/wrkflw/internal/persistence/store"
	"github.com/kartaladev/wrkflw/runtime/kernel"
)

// signalingRelayTickPub signals on passed every time Publish is called, so a
// test can synchronize on each relay pass that actually claims a due row,
// without sleeping or bare-counting.
type signalingRelayTickPub struct {
	passed chan struct{}
}

func (p *signalingRelayTickPub) Publish(context.Context, kernel.OutboxEvent) error {
	p.passed <- struct{}{}
	return nil
}

// seedRelayTickRow inserts a single pending wrkflw_outbox row due at
// nextAttempt, encoded via the dialect's own timeArg codec (exposed to tests
// via store.TimeArgForDialect) so the claim query's lexicographic/native
// comparison behaves exactly as production writes do.
func seedRelayTickRow(t *testing.T, db *sql.DB, d dialect.Dialect, dedup string, nextAttempt time.Time) {
	t.Helper()
	s, err := store.New(db, d)
	require.NoError(t, err)
	arg := store.TimeArgForDialect(s, nextAttempt)
	_, err = db.ExecContext(t.Context(),
		`INSERT INTO wrkflw_outbox
		   (instance_id, topic, payload, dedup_key, created_at, status, retry_count, next_attempt_at)
		 VALUES (?,?,?,?,?,'pending',0,?)`,
		"clock-driven-test", "test.event", `{}`, dedup, arg, arg,
	)
	require.NoError(t, err, "seed outbox row %s", dedup)
}

// TestRelay_TickIsClockDriven proves Run's poll ticker is routed through the
// injected clock (ADR-0138): under a clockwork.FakeClock, no wall time
// passes, so only fc.Advance(poll) — not real time — can make the row that is
// due only after one poll interval get claimed and published.
func TestRelay_TickIsClockDriven(t *testing.T) {
	const poll = time.Second
	fc := clockwork.NewFakeClock()
	db := dbtest.RunTestSQLite(t)
	d := dialect.NewSQLite()

	// Seed two due rows: one due immediately (claimed by Run's pre-tick
	// drain), one due only after one poll interval (claimed only once the
	// ticker fires post fc.Advance(poll)).
	seedRelayTickRow(t, db, d, "row-immediate", fc.Now())
	seedRelayTickRow(t, db, d, "row-after-tick", fc.Now().Add(poll))

	pub := &signalingRelayTickPub{passed: make(chan struct{}, 1)}
	r, err := store.NewRelay(db, d, pub,
		store.WithRelayClock(fc),
		store.WithRelayPollInterval(poll),
	)
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	runErr := make(chan error, 1)
	go func() { runErr <- r.Run(ctx) }()

	// Immediate drain, before the first tick: claims + publishes "row-immediate".
	<-pub.passed

	// Confirm the ticker waiter is armed (registered at NewTicker, i.e. at the
	// very start of Run), then advance the fake clock by exactly one poll
	// interval.
	require.NoError(t, fc.BlockUntilContext(t.Context(), 1))
	fc.Advance(poll)

	// This receive IS the assertion: a stdlib time.NewTicker never fires under
	// fc.Advance, so an unrouted Run would never claim/publish "row-after-tick".
	select {
	case <-pub.passed:
	case <-time.After(2 * time.Second):
		t.Fatal("no clock-driven relay pass")
	}

	cancel()
	select {
	case gotErr := <-runErr:
		require.ErrorIs(t, gotErr, context.Canceled, "Run must return ctx.Err() on cancellation")
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not return after ctx cancellation (goroutine leak)")
	}
}
