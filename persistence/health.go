package persistence

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

// PingCheck is a readiness probe over a [pgxpool.Pool] that reports the database
// reachable by issuing pool.Ping with the request context (ADR-0054). It
// structurally satisfies the rest.HealthCheck contract (Name + Check), so a
// consumer registers it with rest.NewHealthHandler to wire /readyz to Postgres:
//
//	handler := rest.NewHealthHandler(persistence.NewPingCheck(pool))
//
// It is defined here (not in transport/rest) so the pgx dependency stays out of
// the transport package and persistence keeps no import on a transport.
type PingCheck struct {
	pool *pgxpool.Pool
	name string
}

// PingOption configures a [PingCheck].
type PingOption func(*PingCheck)

// WithPingName overrides the probe's reported name (default "postgres"), so a
// deployment with multiple pools can distinguish them in the /readyz body. An
// empty name is ignored.
func WithPingName(name string) PingOption {
	return func(c *PingCheck) {
		if name != "" {
			c.name = name
		}
	}
}

// NewPingCheck returns a [PingCheck] over pool. The default probe name is
// "postgres"; override it with [WithPingName].
func NewPingCheck(pool *pgxpool.Pool, opts ...PingOption) PingCheck {
	c := PingCheck{pool: pool, name: "postgres"}
	for _, o := range opts {
		o(&c)
	}
	return c
}

// Name returns the probe's name as it appears in the readiness response.
func (c PingCheck) Name() string { return c.name }

// Check pings the pool with ctx. It returns a non-nil error when the pool is nil
// or the database is unreachable (or ctx is done), which drives /readyz to 503.
func (c PingCheck) Check(ctx context.Context) error {
	if c.pool == nil {
		return fmt.Errorf("workflow-persistence: ping check %q: nil pool", c.name)
	}
	if err := c.pool.Ping(ctx); err != nil {
		return fmt.Errorf("workflow-persistence: ping check %q: %w", c.name, err)
	}
	return nil
}
