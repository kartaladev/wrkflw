package dialect

import (
	"errors"

	mysqldriver "github.com/go-sql-driver/mysql"
)

// mysql is the stateless MySQL SQL dialect. It is safe for concurrent use.
type mysql struct{}

// NewMySQL returns the MySQL SQL dialect. The returned value implements
// [Dialect] and is stateless and safe for concurrent use.
//
// MySQL uses ? as its native placeholder style (no rebind required), does not
// support UPDATE … RETURNING, uses INSERT IGNORE for idempotent inserts, and
// names the journal payload column trigger_ (reserved word in MySQL).
func NewMySQL() Dialect { return mysql{} }

// Name returns the stable lowercase identifier for this dialect.
func (mysql) Name() string { return "mysql" }

// Rebind returns query unchanged. MySQL uses ? as its native placeholder
// style, so no rewriting is required.
func (mysql) Rebind(query string) string { return query }

// UpsertTimer returns the ON DUPLICATE KEY UPDATE clause for the timer upsert
// site.
// Copied verbatim from internal/persistence/mysql/store.go mysqlUpsertTimer.
func (mysql) UpsertTimer() string {
	return "\n\t\t\tON DUPLICATE KEY UPDATE fire_at=VALUES(fire_at), kind=VALUES(kind)," +
		"\n\t\t\t                        def_id=VALUES(def_id), def_version=VALUES(def_version)"
}

// UpsertDefinition returns the ON DUPLICATE KEY UPDATE clause for the
// process-definition upsert site.
// Copied verbatim from internal/persistence/mysql/definitions.go PutDefinition.
func (mysql) UpsertDefinition() string {
	return "\n\t\t\t ON DUPLICATE KEY UPDATE definition = VALUES(definition)"
}

// InsertIgnorePrefix returns the INSERT keyword prefix for the dedup
// idempotency check. MySQL uses INSERT IGNORE as a prefix; the suffix
// ([InsertIgnoreDedup]) is empty.
// Copied from internal/persistence/mysql/dedup.go Seen.
func (mysql) InsertIgnorePrefix() string { return "INSERT IGNORE" }

// InsertIgnoreDedup returns an empty string. MySQL uses the INSERT IGNORE
// prefix form ([InsertIgnorePrefix]) rather than a trailing conflict clause.
func (mysql) InsertIgnoreDedup() string { return "" }

// JournalTriggerColumn returns "trigger_", the MySQL journal payload column
// name. The trailing underscore avoids a clash with the MySQL reserved word
// TRIGGER.
func (mysql) JournalTriggerColumn() string { return "trigger_" }

// OutboxStatsQuery returns the aggregate query for the wrkflw_outbox table.
// Copied verbatim from internal/persistence/mysql/relay.go OutboxStats.
// Uses status='pending'/'dead' column values — not dead_lettered_at IS NULL.
func (mysql) OutboxStatsQuery() string {
	return `SELECT COALESCE(SUM(status='pending'), 0),
		        COALESCE(SUM(status='dead'), 0),
		        COALESCE(TIMESTAMPDIFF(SECOND, MIN(CASE WHEN status='pending' THEN created_at END), NOW()), 0)
		   FROM wrkflw_outbox`
}

// NotifyStatement returns an empty string. MySQL has no native LISTEN/NOTIFY
// mechanism; the relay falls back to polling.
func (mysql) NotifyStatement(string) string { return "" }

// SupportsReturning reports that MySQL does not support UPDATE … RETURNING.
// The leased-claim path uses a SELECT … FOR UPDATE SKIP LOCKED followed by a
// separate UPDATE instead of a single round-trip.
func (mysql) SupportsReturning() bool { return false }

// SupportsSkipLocked reports that MySQL 8.0 supports FOR UPDATE SKIP LOCKED
// in SELECT queries.
func (mysql) SupportsSkipLocked() bool { return true }

// IsUniqueViolation reports whether err is (or wraps) a MySQL unique-key
// violation (error number 1062).
func (mysql) IsUniqueViolation(err error) bool {
	var me *mysqldriver.MySQLError
	return errors.As(err, &me) && me.Number == 1062
}

// IsRetryableConflict reports whether err is (or wraps) a MySQL deadlock
// (error number 1213) or lock-wait timeout (error number 1205) — both are
// transient concurrency errors the caller should retry.
func (mysql) IsRetryableConflict(err error) bool {
	var me *mysqldriver.MySQLError
	return errors.As(err, &me) && (me.Number == 1213 || me.Number == 1205)
}
