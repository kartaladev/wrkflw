// Package postgres provides the Postgres-backed leader [scheduling.Elector] for
// multi-replica single-leader timer firing (ADR-0059, ADR-0102). It is the
// database-specific layer: importing pgx here is expected and keeps the public
// scheduling façade neutral of any DB driver.
package postgres

import (
	"context"
	"io"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jonboulle/clockwork"

	gocronsched "github.com/zakyalvan/krtlwrkflw/internal/scheduling/gocron"
	"github.com/zakyalvan/krtlwrkflw/scheduling"
)

// Option configures the Postgres leader elector built by [NewElector].
type Option func(*settings)

type settings struct {
	clk  clockwork.Clock
	opts []gocronsched.ElectorOption
}

// WithElectorKey overrides the leader-lock key (default: a fixed well-known
// constant). Give each independent engine sharing one database a distinct key so
// their leader elections do not contend. An empty value is ignored.
func WithElectorKey(key string) Option {
	return func(s *settings) {
		if key != "" {
			s.opts = append(s.opts, gocronsched.WithElectorKey(key))
		}
	}
}

// WithClock sets the [clockwork.Clock] that drives the leadership heartbeat ticker
// (default: a real clock). Pass the same clock used to build the scheduler so a
// fake clock advances both timer firing and heartbeat ticks in tests. A nil value
// is ignored.
func WithClock(clk clockwork.Clock) Option {
	return func(s *settings) {
		if clk != nil {
			s.clk = clk
		}
	}
}

// WithHeartbeatInterval overrides how often the elected leader re-validates its
// dedicated connection (default: an internal sane value). It bounds the residual
// split-brain window to at most one interval (ADR-0061). A non-positive value is
// ignored.
func WithHeartbeatInterval(d time.Duration) Option {
	return func(s *settings) {
		if d > 0 {
			s.opts = append(s.opts, gocronsched.WithHeartbeatInterval(d))
		}
	}
}

// WithOnLeadershipAcquired registers a callback invoked each time this elector
// wins (or re-wins) leadership. It runs asynchronously and never blocks timer
// firing. Wire it to runtime.ProcessDriver.RehydrateTimers so a new leader re-arms
// the persisted timer set on leadership acquisition (Option A, ADR-0072). A nil
// value is ignored.
func WithOnLeadershipAcquired(fn func(context.Context)) Option {
	return func(s *settings) {
		if fn != nil {
			s.opts = append(s.opts, gocronsched.WithOnLeadershipAcquired(fn))
		}
	}
}

// Elector is the Postgres-backed leader [scheduling.Elector]. Beyond IsLeader it
// exposes Close (so scheduling.Scheduler.Close releases its dedicated connection)
// and BackendPID (for operator correlation / heartbeat tests).
type Elector interface {
	scheduling.Elector
	io.Closer
	// BackendPID returns the Postgres backend PID of the elector's dedicated
	// connection; 0 after Close.
	BackendPID() uint32
}

// NewElector acquires a dedicated session connection from pool and returns a
// Postgres-backed leader [scheduling.Elector]: exactly one replica wins the leader
// advisory lock and runs ALL timer fires; the others' IsLeader reports it is not
// leader so the scheduler skips their jobs. On leader death the connection drops,
// Postgres releases the lock, and a follower wins it on its next attempt.
//
// Pass the returned value to scheduling.WithElector. Its dedicated connection is
// released by scheduling.Scheduler.Close (which closes the elector when it
// implements io.Closer) or by calling [Elector.Close] directly.
func NewElector(ctx context.Context, pool *pgxpool.Pool, opts ...Option) (Elector, error) {
	var s settings
	for _, o := range opts {
		o(&s)
	}
	if s.clk != nil {
		// Prepend so a caller-supplied clock option (if any) still wins.
		s.opts = append([]gocronsched.ElectorOption{gocronsched.WithElectorClock(s.clk)}, s.opts...)
	}
	e, err := gocronsched.NewPostgresElector(ctx, pool, s.opts...)
	if err != nil {
		return nil, err
	}
	return e, nil
}
