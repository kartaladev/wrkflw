package dialect

import (
	"errors"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5/pgconn"
)

// postgres is the stateless Postgres SQL dialect. It is safe for concurrent use.
type postgres struct{}

// NewPostgres returns the Postgres SQL dialect. The returned value implements
// [Dialect] and is stateless and safe for concurrent use.
func NewPostgres() Dialect { return postgres{} }

// Name returns the stable lowercase identifier for this dialect.
func (postgres) Name() string { return "postgres" }

// Rebind converts a query written with ? placeholders into Postgres-style $1,
// $2, … numbered placeholders.
func (postgres) Rebind(query string) string {
	var b strings.Builder
	n := 0
	for i := 0; i < len(query); i++ {
		if query[i] == '?' {
			n++
			b.WriteByte('$')
			b.WriteString(strconv.Itoa(n))
			continue
		}
		b.WriteByte(query[i])
	}
	return b.String()
}

// UpsertTimer returns the ON CONFLICT clause for the timer upsert site.
func (postgres) UpsertTimer() string {
	return " ON CONFLICT (instance_id, timer_id)" +
		" DO UPDATE SET fire_at = EXCLUDED.fire_at, kind = EXCLUDED.kind," +
		" def_id = EXCLUDED.def_id, def_version = EXCLUDED.def_version"
}

// UpsertDefinition returns the ON CONFLICT clause for the process-definition
// upsert site.
func (postgres) UpsertDefinition() string {
	return " ON CONFLICT (def_id, version) DO UPDATE SET definition = EXCLUDED.definition"
}

// InsertIgnorePrefix returns the INSERT keyword prefix for the dedup idempotency
// check. Postgres uses a plain "INSERT" prefix paired with an
// "ON CONFLICT DO NOTHING" suffix ([InsertIgnoreDedup]).
func (postgres) InsertIgnorePrefix() string { return "INSERT" }

// InsertIgnoreDedup returns the conflict suffix for the dedup INSERT.
func (postgres) InsertIgnoreDedup() string { return " ON CONFLICT DO NOTHING" }

// JournalTriggerColumn returns the journal payload column name used by Postgres.
func (postgres) JournalTriggerColumn() string { return "trigger" }

// NotifyStatement returns a bare NOTIFY statement for the given channel.
// The channel name is a compile-time constant in the store; it is never
// user-supplied and does not require parameterisation.
func (postgres) NotifyStatement(channel string) string { return "NOTIFY " + channel }

// SupportsReturning reports that Postgres supports UPDATE … RETURNING.
func (postgres) SupportsReturning() bool { return true }

// SupportsSkipLocked reports that Postgres supports FOR UPDATE SKIP LOCKED.
func (postgres) SupportsSkipLocked() bool { return true }

// OutboxStatsQuery returns the aggregate query for the wrkflw_outbox table.
func (postgres) OutboxStatsQuery() string {
	return `SELECT count(*) FILTER (WHERE status = 'pending'),
	        count(*) FILTER (WHERE status = 'dead'),
	        COALESCE(EXTRACT(EPOCH FROM now()-min(created_at) FILTER (WHERE status = 'pending')), 0)
	   FROM wrkflw_outbox`
}

// IsUniqueViolation reports whether err is (or wraps) a Postgres
// unique-constraint violation (SQLSTATE 23505).
func (postgres) IsUniqueViolation(err error) bool {
	var pg *pgconn.PgError
	return errors.As(err, &pg) && pg.Code == "23505"
}

// IsRetryableConflict reports whether err is (or wraps) a Postgres
// serialization failure (SQLSTATE 40001) that the caller should retry.
func (postgres) IsRetryableConflict(err error) bool {
	var pg *pgconn.PgError
	return errors.As(err, &pg) && pg.Code == "40001"
}

// IncidentCountExpr returns the Postgres JSONB expression that counts incidents
// stored in the snapshot column. jsonb_typeof guards against a missing or non-array
// Incidents key; jsonb_array_length yields the count when the key is present.
func (postgres) IncidentCountExpr() string {
	return `CASE WHEN jsonb_typeof(snapshot->'Incidents') = 'array'
	             THEN jsonb_array_length(snapshot->'Incidents')
	             ELSE 0 END AS incident_count`
}

// KeysetCursorPredicate returns the Postgres row-value keyset predicate.
// Postgres evaluates (col1, col2) < ($n, $m) correctly for TIMESTAMPTZ
// and VARCHAR comparisons, matching the MemInstanceStore skip logic exactly.
func (postgres) KeysetCursorPredicate() string {
	return "AND (started_at, instance_id) < (?, ?) "
}

// KeysetCursorArgCount returns 2 because Postgres row-value comparison
// binds cursorTime once and cursorID once.
func (postgres) KeysetCursorArgCount() int { return 2 }

// TimestampsAsText reports that Postgres stores timestamps as native
// TIMESTAMPTZ values. The pgx driver binds and scans time.Time directly;
// no RFC3339Nano text encoding is needed.
func (postgres) TimestampsAsText() bool { return false }
