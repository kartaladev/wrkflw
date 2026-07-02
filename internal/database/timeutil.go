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
	// SQLite selects the in-process SQLite probe path in [ProbeUTC]. The probe
	// executes SELECT datetime('2000-01-01 00:00:00') and verifies that the
	// returned TEXT string matches the expected UTC literal exactly.
	//
	// modernc.org/sqlite returns DATETIME values as plain TEXT strings rather
	// than time.Time; the probe scans into a string and compares it directly.
	// This validates that the SQLite connection's time functions return UTC-consistent
	// values — a no-op for a typical in-process SQLite, but a useful smoke-check
	// that the connection is healthy and the datetime function is available.
	SQLite
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
	switch d {
	case Postgres:
		known := time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)
		query := `SELECT TIMESTAMPTZ '2000-01-01 00:00:00+00'`
		var got time.Time
		if err := q.QueryRow(ctx, query).Scan(&got); err != nil {
			return fmt.Errorf("workflow-database: probe query: %w", err)
		}
		if !got.Equal(known) {
			return fmt.Errorf("workflow-database: connection is not UTC (read %s, want %s); "+
				"for MySQL set DSN parseTime=true&loc=UTC (see persistence.MySQLDSN)",
				got.Format(time.RFC3339Nano), known.Format(time.RFC3339Nano))
		}
	case MySQL:
		known := time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)
		query := `SELECT TIMESTAMP('2000-01-01 00:00:00')`
		var got time.Time
		if err := q.QueryRow(ctx, query).Scan(&got); err != nil {
			return fmt.Errorf("workflow-database: probe query: %w", err)
		}
		if !got.Equal(known) {
			return fmt.Errorf("workflow-database: connection is not UTC (read %s, want %s); "+
				"for MySQL set DSN parseTime=true&loc=UTC (see persistence.MySQLDSN)",
				got.Format(time.RFC3339Nano), known.Format(time.RFC3339Nano))
		}
	case SQLite:
		// modernc.org/sqlite returns DATETIME values as TEXT strings rather than
		// time.Time. Scan the literal directly into a string and verify it matches
		// the expected UTC representation. This confirms the connection is healthy
		// and the datetime function returns UTC-consistent values.
		const wantText = "2000-01-01 00:00:00"
		query := `SELECT datetime('2000-01-01 00:00:00')`
		var got string
		if err := q.QueryRow(ctx, query).Scan(&got); err != nil {
			return fmt.Errorf("workflow-database: probe query: %w", err)
		}
		if got != wantText {
			return fmt.Errorf("workflow-database: sqlite probe: got %q, want %q", got, wantText)
		}
	default:
		return fmt.Errorf("workflow-database: probe: unknown dialect %d", d)
	}
	return nil
}
