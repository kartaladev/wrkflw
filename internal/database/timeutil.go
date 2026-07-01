package database

import (
	"context"
	"fmt"
	"time"
)

// UTC returns t in UTC without changing the instant. Use it on every time.Time
// scanned from the database so callers always receive UTC-located values.
func UTC(t time.Time) time.Time { return t.UTC() }

// Dialect selects the probe SQL for ProbeUTC.
type Dialect int

const (
	Postgres Dialect = iota
	MySQL
)

// ProbeUTC verifies the connection interprets stored datetimes as UTC. It reads a
// known literal timestamp and fails if the scanned INSTANT drifted from the known
// UTC value — which happens for a MySQL DSN missing loc=UTC (the DATETIME string is
// parsed in the host zone, shifting the instant). Postgres TIMESTAMPTZ always
// preserves the instant, so this passes regardless of the returned Location; the
// read-side Location is handled by UTC() normalization. Instant-equality is used
// (not a zone-offset check) precisely because pgx may return TIMESTAMPTZ in
// time.Local without the instant being wrong. Call once at Open for fail-fast.
//
// Note: this probes the read-back interpretation (MySQL loc). The MySQL session
// time_zone (which governs DEFAULT CURRENT_TIMESTAMP(6) columns) is enforced
// separately by persistence.MySQLDSN.
func ProbeUTC(ctx context.Context, q Querier, d Dialect) error {
	known := time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)
	var sql string
	switch d {
	case Postgres:
		sql = `SELECT TIMESTAMPTZ '2000-01-01 00:00:00+00'`
	case MySQL:
		sql = `SELECT TIMESTAMP('2000-01-01 00:00:00')`
	default:
		return fmt.Errorf("workflow-database: probe: unknown dialect %d", d)
	}
	var got time.Time
	if err := q.QueryRow(ctx, sql).Scan(&got); err != nil {
		return fmt.Errorf("workflow-database: probe query: %w", err)
	}
	if !got.Equal(known) {
		return fmt.Errorf("workflow-database: connection is not UTC (read %s, want %s); "+
			"for MySQL set DSN parseTime=true&loc=UTC (see persistence.MySQLDSN)",
			got.Format(time.RFC3339Nano), known.Format(time.RFC3339Nano))
	}
	return nil
}
