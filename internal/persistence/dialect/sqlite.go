package dialect

import (
	"errors"

	sqlitedriver "modernc.org/sqlite"
	sqlitelib "modernc.org/sqlite/lib"
)

// sqliteDialect is the stateless SQLite SQL dialect (modernc.org/sqlite). It
// is safe for concurrent use.
type sqliteDialect struct{}

// NewSQLite returns the SQLite SQL dialect (modernc.org/sqlite). The returned
// value implements [Dialect] and is stateless and safe for concurrent use.
//
// SQLite ≥ 3.35 is assumed, which enables RETURNING support. The bundled
// modernc.org/sqlite v1.53.0 ships SQLite 3.49.x, well above that threshold.
func NewSQLite() Dialect { return sqliteDialect{} }

// Name returns the stable lowercase identifier for this dialect.
func (sqliteDialect) Name() string { return "sqlite" }

// Rebind returns the query unchanged because SQLite uses ? as its native
// placeholder style — no rewriting is needed.
func (sqliteDialect) Rebind(query string) string { return query }

// UpsertTimer returns the ON CONFLICT clause for the timer upsert site.
// The conflict target and updated columns mirror the Postgres dialect exactly;
// SQLite uses lowercase "excluded." instead of Postgres's "EXCLUDED.".
func (sqliteDialect) UpsertTimer() string {
	return " ON CONFLICT (instance_id, timer_id)" +
		" DO UPDATE SET fire_at = excluded.fire_at, kind = excluded.kind," +
		" def_id = excluded.def_id, def_version = excluded.def_version"
}

// UpsertDefinition returns the ON CONFLICT clause for the process-definition
// upsert site. Mirrors the Postgres dialect (same conflict target and updated
// column) with lowercase "excluded.".
func (sqliteDialect) UpsertDefinition() string {
	return " ON CONFLICT (def_id, version) DO UPDATE SET definition = excluded.definition"
}

// InsertIgnorePrefix returns the INSERT keyword prefix for the dedup
// idempotency check. SQLite uses a plain "INSERT" prefix paired with an
// "ON CONFLICT DO NOTHING" suffix ([InsertIgnoreDedup]), identical to Postgres.
func (sqliteDialect) InsertIgnorePrefix() string { return "INSERT" }

// InsertIgnoreDedup returns the conflict suffix for the dedup INSERT.
// SQLite uses the same "ON CONFLICT DO NOTHING" clause as Postgres.
func (sqliteDialect) InsertIgnoreDedup() string { return " ON CONFLICT DO NOTHING" }

// JournalTriggerColumn returns the journal payload column name. "trigger" is
// not a reserved keyword in SQLite (unlike MySQL where it requires escaping).
func (sqliteDialect) JournalTriggerColumn() string { return "trigger" }

// NotifyStatement returns an empty string because SQLite has no native
// pub/sub or LISTEN/NOTIFY mechanism.
func (sqliteDialect) NotifyStatement(_ string) string { return "" }

// SupportsReturning reports that SQLite ≥ 3.35 supports UPDATE … RETURNING.
func (sqliteDialect) SupportsReturning() bool { return true }

// SupportsSkipLocked reports that SQLite does not support FOR UPDATE SKIP
// LOCKED. Relay-drain and leased-claim sites must use an alternative locking
// strategy when this returns false.
func (sqliteDialect) SupportsSkipLocked() bool { return false }

// OutboxStatsQuery returns the aggregate query for the wrkflw_outbox table.
// Semantically equivalent to the Postgres/MySQL versions — same column order
// (pending count, dead count, oldest-pending age in seconds) and same
// status column values ('pending'/'dead').
//
// SQLite has no FILTER aggregate clause or EXTRACT function, so the query
// uses CASE/WHEN inside COALESCE(SUM(…)) for counts and julianday arithmetic
// for the age: (julianday('now') − julianday(MIN(created_at))) × 86400.
func (sqliteDialect) OutboxStatsQuery() string {
	return `SELECT` +
		` COALESCE(SUM(CASE WHEN status='pending' THEN 1 ELSE 0 END),0),` +
		` COALESCE(SUM(CASE WHEN status='dead' THEN 1 ELSE 0 END),0),` +
		` COALESCE(CAST((julianday('now') - julianday(MIN(CASE WHEN status='pending' THEN created_at END))) * 86400 AS INTEGER), 0)` +
		` FROM wrkflw_outbox`
}

// IsUniqueViolation reports whether err is (or wraps) a SQLite
// unique-constraint violation. Both a UNIQUE index/constraint
// (SQLITE_CONSTRAINT_UNIQUE, code 2067) and a PRIMARY KEY collision
// (SQLITE_CONSTRAINT_PRIMARYKEY, code 1555) count: a duplicate insert on a
// PRIMARY KEY column (e.g. wrkflw_instances.instance_id) is a unique violation,
// matching the Postgres/MySQL behaviour that maps it to ErrInstanceExists.
func (sqliteDialect) IsUniqueViolation(err error) bool {
	var se *sqlitedriver.Error
	if !errors.As(err, &se) {
		return false
	}
	return se.Code() == sqlitelib.SQLITE_CONSTRAINT_UNIQUE ||
		se.Code() == sqlitelib.SQLITE_CONSTRAINT_PRIMARYKEY
}

// IsRetryableConflict reports whether err is (or wraps) a transient SQLite
// locking error that the caller should retry: SQLITE_BUSY (the database file
// is locked by another connection) or SQLITE_LOCKED (a table is locked within
// the same connection, e.g. shared-cache mode).
func (sqliteDialect) IsRetryableConflict(err error) bool {
	var se *sqlitedriver.Error
	return errors.As(err, &se) && (se.Code() == sqlitelib.SQLITE_BUSY || se.Code() == sqlitelib.SQLITE_LOCKED)
}

// IncidentCountExpr returns the SQLite JSON1 expression that counts incidents
// stored in the snapshot TEXT column. json_type returns NULL when the path is
// absent; the CASE guard evaluates to 0 in that case.
func (sqliteDialect) IncidentCountExpr() string {
	return `CASE WHEN json_type(snapshot, '$.Incidents') = 'array'
	             THEN json_array_length(snapshot, '$.Incidents')
	             ELSE 0 END AS incident_count`
}

// KeysetCursorPredicate returns the SQLite keyset cursor predicate. SQLite does
// not guarantee correct row-value comparison semantics for mixed-type columns,
// so the predicate uses the same explicit OR decomposition as MySQL.
// Note: started_at is stored as RFC3339Nano TEXT, so lexicographic < and =
// comparisons work correctly when timestamps are normalised to UTC (ADR-0080).
func (sqliteDialect) KeysetCursorPredicate() string {
	return "AND (started_at < ? OR (started_at = ? AND instance_id < ?)) "
}

// KeysetCursorArgCount returns 3 because the SQLite predicate binds cursorTime
// twice (once for < and once for =) then cursorID.
func (sqliteDialect) KeysetCursorArgCount() int { return 3 }

// TimestampsAsText reports that SQLite stores timestamp columns as RFC3339Nano
// TEXT strings (ADR-0080). The modernc.org/sqlite driver does not natively
// encode time.Time to ISO8601; callers must format values with
// time.RFC3339Nano before binding and parse them back when scanning.
func (sqliteDialect) TimestampsAsText() bool { return true }
