package persistence

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

// pinger is an internal seam so PingCheck can back either a pgxpool.Pool or a
// *sql.DB (or any other database client that can report liveness) without
// exposing the concrete type in the public struct.
type pinger interface {
	Ping(ctx context.Context) error
}

// sqlDBPinger wraps *sql.DB to satisfy [pinger]. *sql.DB exposes PingContext
// rather than Ping, so a thin adapter bridges the naming difference.
type sqlDBPinger struct{ db *sql.DB }

func (a sqlDBPinger) Ping(ctx context.Context) error { return a.db.PingContext(ctx) }

// PingCheck is a readiness probe that reports the database reachable by issuing
// a Ping with the request context (ADR-0054). It structurally satisfies the
// rest.HealthCheck contract (Name + Check), so a consumer registers it with
// rest.NewHealthHandler to wire /readyz to a database:
//
//	handler := rest.NewHealthHandler(persistence.NewPingCheck(pool))
//
// It is defined here (not in transport/rest) so the database driver dependency
// stays out of the transport package and persistence keeps no import on a transport.
type PingCheck struct {
	ping pinger
	name string
	// nilSource is set when the source (pool or db) was nil at construction time.
	// We store this explicitly so that Check can return the expected "nil pool" /
	// "nil db" error without a nil-interface-pointer dereference.
	nilSource bool
	nilMsg    string
}

// PingOption configures a [PingCheck].
type PingOption func(*PingCheck)

// WithPingName overrides the probe's reported name (default "postgres" for pgx,
// "mysql" for MySQL), so a deployment with multiple pools can distinguish them in
// the /readyz body. An empty name is ignored.
func WithPingName(name string) PingOption {
	return func(c *PingCheck) {
		if name != "" {
			c.name = name
		}
	}
}

// NewPingCheck returns a [PingCheck] over pool. The default probe name is
// "postgres"; override it with [WithPingName].
//
// A nil pool is accepted; [PingCheck.Check] will return an error containing
// "nil pool" so TestPingCheckNilPool continues to pass.
func NewPingCheck(pool *pgxpool.Pool, opts ...PingOption) PingCheck {
	c := PingCheck{name: "postgres"}
	if pool == nil {
		c.nilSource = true
		c.nilMsg = "nil pool"
	} else {
		// *pgxpool.Pool already has Ping(ctx context.Context) error, so it
		// satisfies pinger directly — no adapter needed.
		c.ping = pool
	}
	for _, o := range opts {
		o(&c)
	}
	return c
}

// NewMySQLPingCheck returns a [PingCheck] over a *sql.DB (MySQL). The default
// probe name is "mysql"; override it with [WithPingName].
//
// A nil *sql.DB is accepted; [PingCheck.Check] will return an error containing
// "nil db" so callers can detect a mis-configured probe at startup.
func NewMySQLPingCheck(db *sql.DB, opts ...PingOption) PingCheck {
	c := PingCheck{name: "mysql"}
	if db == nil {
		c.nilSource = true
		c.nilMsg = "nil db"
	} else {
		c.ping = sqlDBPinger{db: db}
	}
	for _, o := range opts {
		o(&c)
	}
	return c
}

// Name returns the probe's name as it appears in the readiness response.
func (c PingCheck) Name() string { return c.name }

// Check pings the underlying database with ctx. It returns a non-nil error when
// the source (pool/db) was nil at construction time or the database is
// unreachable (or ctx is done), which drives /readyz to 503.
func (c PingCheck) Check(ctx context.Context) error {
	if c.nilSource {
		return fmt.Errorf("workflow-persistence: ping check %q: %s", c.name, c.nilMsg)
	}
	if err := c.ping.Ping(ctx); err != nil {
		return fmt.Errorf("workflow-persistence: ping check %q: %w", c.name, err)
	}
	return nil
}
