// Package dialect abstracts the SQL-text and driver-error differences between
// supported database backends (PostgreSQL, MySQL, SQLite). It is orthogonal to
// the access mechanism (pgx vs database/sql): a [Dialect] value travels beside
// a connection/querier and is chosen once at startup based on the configured
// backend.
//
// Capability interfaces ([Notifier], [Locker]) are optional extensions. Callers
// that require a capability should type-assert the [Dialect] value; if the
// assertion fails, or if the implementation returns [ErrUnsupported], the
// capability is unavailable for that backend/access combination.
package dialect

import (
	"context"
	"errors"
)

// ErrUnsupported is returned by a capability that the dialect or
// backend-and-access combination does not provide (for example, advisory
// locking on SQLite). Callers must match it with [errors.Is].
var ErrUnsupported = errors.New("workflow-dialect: capability not supported")

// Dialect abstracts the SQL-text and driver-error differences between
// backends. It is stateless and safe for concurrent use.
//
// The interface is intentionally larger than a single concern because the
// dialect value is shared across all persistence sites; splitting it into many
// small interfaces would force callers to carry multiple values for the same
// backend choice.
type Dialect interface {
	// Name returns a stable, lowercase identifier for the dialect
	// (e.g. "postgres", "mysql", "sqlite").
	Name() string

	// Rebind converts a query written with ? placeholders into this
	// dialect's placeholder style ($1, $2, … for Postgres; ? unchanged for
	// MySQL and SQLite).
	Rebind(query string) string

	// UpsertTimer returns the conflict clause appended to the shared base
	// INSERT for the timer upsert site.
	UpsertTimer() string

	// UpsertDefinition returns the conflict clause appended to the shared
	// base INSERT for the process-definition upsert site.
	UpsertDefinition() string

	// InsertIgnorePrefix returns the INSERT keyword prefix used for the
	// dedup idempotency check. The full statement is assembled as:
	//
	//	<prefix> INTO wrkflw_processed_message (...) VALUES (...) <suffix>
	//
	// Postgres and SQLite use a plain "INSERT" prefix with an
	// "ON CONFLICT DO NOTHING" suffix ([InsertIgnoreDedup]). MySQL uses an
	// "INSERT IGNORE" prefix with an empty suffix.
	InsertIgnorePrefix() string

	// InsertIgnoreDedup returns the conflict clause (suffix) appended to
	// the dedup INSERT. Use together with [InsertIgnorePrefix]:
	//
	//	<InsertIgnorePrefix()> INTO ... VALUES ... <InsertIgnoreDedup()>
	//
	// Postgres/SQLite: " ON CONFLICT DO NOTHING". MySQL: "".
	InsertIgnoreDedup() string

	// JournalTriggerColumn returns the journal payload column name:
	// "trigger" on Postgres and SQLite, "trigger_" on MySQL (reserved word).
	JournalTriggerColumn() string

	// OutboxStatsQuery returns the dialect's pending/dead/age aggregate
	// query for the wrkflw_outbox table.
	OutboxStatsQuery() string

	// NotifyStatement returns the dialect's LISTEN/NOTIFY wake statement
	// (e.g. "NOTIFY <channel>" for Postgres). Backends that do not support
	// native pub/sub return an empty string.
	NotifyStatement(channel string) string

	// SupportsReturning reports whether the backend supports
	// UPDATE … RETURNING. When true, the leased-claim path uses a single
	// round-trip UPDATE … RETURNING instead of a SELECT … FOR UPDATE
	// SKIP LOCKED followed by a separate UPDATE.
	SupportsReturning() bool

	// SupportsSkipLocked reports whether the backend supports
	// FOR UPDATE SKIP LOCKED in SELECT queries. SQLite does not; the relay
	// drain and leased-claim sites branch on this value to choose a
	// compatible locking strategy.
	SupportsSkipLocked() bool

	// IsUniqueViolation reports whether err represents a unique-constraint
	// violation in this dialect's driver.
	IsUniqueViolation(err error) bool

	// IsRetryableConflict reports whether err represents a transient
	// serialization or deadlock error that the caller should retry.
	IsRetryableConflict(err error) bool

	// IncidentCountExpr returns a dialect-specific SQL expression that computes
	// the number of incidents stored in the snapshot JSON column. The expression
	// must alias itself as incident_count and evaluate to an integer (0 when the
	// Incidents key is absent or not an array). It is embedded directly into the
	// SELECT column list of the instance-lister query.
	//
	// Example outputs:
	//   Postgres: "CASE WHEN jsonb_typeof(snapshot->'Incidents') = 'array'
	//                   THEN jsonb_array_length(snapshot->'Incidents')
	//                   ELSE 0 END AS incident_count"
	//   MySQL:    "CASE WHEN JSON_TYPE(JSON_EXTRACT(snapshot, '$.Incidents')) = 'ARRAY'
	//                   THEN JSON_LENGTH(JSON_EXTRACT(snapshot, '$.Incidents'))
	//                   ELSE 0 END AS incident_count"
	//   SQLite:   "CASE WHEN json_type(snapshot, '$.Incidents') = 'array'
	//                   THEN json_array_length(snapshot, '$.Incidents')
	//                   ELSE 0 END AS incident_count"
	IncidentCountExpr() string

	// KeysetCursorPredicate returns the WHERE clause fragment (including the
	// leading AND keyword and the trailing space) used for keyset pagination in
	// the instance-lister query when a cursor is present.
	//
	// The predicate binds two values in this order: cursorTime (time.Time or
	// RFC3339Nano string for SQLite) and cursorID (string). Postgres uses a
	// row-value comparison; MySQL and SQLite use an explicit OR decomposition.
	//
	// Callers append this to the rest of the WHERE clause when hasCursor is true:
	//
	//	"AND (started_at, instance_id) < (?, ?)"          // Postgres (after Rebind → $1,$2)
	//	"AND (started_at < ? OR (started_at = ? AND instance_id < ?))"  // MySQL/SQLite
	//
	// The ? placeholders in the returned string are in ? style and are converted
	// to the backend's native form via [Dialect.Rebind].
	//
	// IMPORTANT: MySQL/SQLite bind cursorTime TWICE (once for the < branch and
	// once for the = branch). Use [Dialect.KeysetCursorArgCount] to determine
	// how many times cursorTime must be repeated in the args slice.
	KeysetCursorPredicate() string

	// KeysetCursorArgCount returns the number of bind arguments consumed by
	// [KeysetCursorPredicate]: 2 for Postgres (cursorTime, cursorID), 3 for
	// MySQL and SQLite (cursorTime, cursorTime, cursorID).
	KeysetCursorArgCount() int

	// TimestampsAsText reports whether this dialect stores timestamp columns as
	// RFC3339Nano TEXT strings rather than native time.Time values.
	//
	// When true (SQLite only), the store must format time.Time values with
	// time.RFC3339Nano before binding them as query arguments, and must parse
	// the raw string back into time.Time when scanning results. Postgres and
	// MySQL bind and scan time.Time natively, so both return false.
	//
	// This flag is the single decision point for time (de)serialization across
	// all store and lister sites; callers must not compare [Dialect.Name] to
	// "sqlite" directly.
	TimestampsAsText() bool
}

// Notifier is the receive side of a database-level pub/sub channel. Only the
// (pgx, Postgres) combination provides a meaningful implementation; all other
// backends return [ErrUnsupported] from [Listen].
//
// Listen subscribes to channel and returns:
//   - a read-only wake channel that receives an empty struct whenever a
//     notification arrives,
//   - a cancel func the caller MUST invoke to release the subscription, and
//   - an error if the subscription could not be established.
type Notifier interface {
	Listen(ctx context.Context, channel string) (<-chan struct{}, func(), error)
}

// Locker is a distributed advisory lock backed by the database. Postgres uses
// session-level advisory locks; MySQL uses GET_LOCK / RELEASE_LOCK. SQLite
// provides no advisory locking — its implementation must return
// [ErrUnsupported] from both methods.
type Locker interface {
	// TryLock attempts to acquire the advisory lock identified by key without
	// blocking. It returns (true, nil) on success, (false, nil) when the lock
	// is already held by another session, and (false, err) on error.
	TryLock(ctx context.Context, key string) (bool, error)

	// Unlock releases an advisory lock previously acquired by [TryLock].
	Unlock(ctx context.Context, key string) error
}
