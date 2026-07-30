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
func (mysql) UpsertTimer() string {
	return "\n\t\t\tON DUPLICATE KEY UPDATE next_run=VALUES(next_run), kind=VALUES(kind)," +
		"\n\t\t\t                        def_id=VALUES(def_id), def_version=VALUES(def_version)," +
		"\n\t\t\t                        trigger_kind=VALUES(trigger_kind), trigger_payload=VALUES(trigger_payload)"
}

// UpsertDefinition returns the ON DUPLICATE KEY UPDATE clause for the
// process-definition upsert site.
func (mysql) UpsertDefinition() string {
	return "\n\t\t\t ON DUPLICATE KEY UPDATE definition = VALUES(definition)"
}

// UpsertTask returns the ON DUPLICATE KEY UPDATE clause for the human-task
// upsert site.
func (mysql) UpsertTask() string {
	return " ON DUPLICATE KEY UPDATE" +
		" instance_id=VALUES(instance_id), node_id=VALUES(node_id)," +
		" state=VALUES(state), claimed_by=VALUES(claimed_by)," +
		" claimed_at=VALUES(claimed_at), claim_actor=VALUES(claim_actor)," +
		" completed_by=VALUES(completed_by), completed_at=VALUES(completed_at)," +
		" outcome=VALUES(outcome), note=VALUES(note)," +
		" completion_actor=VALUES(completion_actor)," +
		" eligibility=VALUES(eligibility), candidates=VALUES(candidates)," +
		" vars=VALUES(vars), created_at=VALUES(created_at), due_at=VALUES(due_at)"
}

// InsertIgnorePrefix returns the INSERT keyword prefix for the dedup
// idempotency check. MySQL uses INSERT IGNORE as a prefix; the suffix
// ([InsertIgnoreDedup]) is empty.
func (mysql) InsertIgnorePrefix() string { return "INSERT IGNORE" }

// InsertIgnoreDedup returns an empty string. MySQL uses the INSERT IGNORE
// prefix form ([InsertIgnorePrefix]) rather than a trailing conflict clause.
func (mysql) InsertIgnoreDedup() string { return "" }

// JournalTriggerColumn returns "trigger_", the MySQL journal payload column
// name. The trailing underscore avoids a clash with the MySQL reserved word
// TRIGGER.
func (mysql) JournalTriggerColumn() string { return "trigger_" }

// OutboxStatsQuery returns the aggregate query for the wrkflw_outbox table.
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

// IncidentCountExpr returns the MySQL JSON expression that counts incidents
// stored in the snapshot column. JSON_TYPE returns NULL when the path is
// absent, so the CASE guard covers missing or null Incidents keys.
func (mysql) IncidentCountExpr() string {
	return `CASE WHEN JSON_TYPE(JSON_EXTRACT(snapshot, '$.Incidents')) = 'ARRAY'
	             THEN JSON_LENGTH(JSON_EXTRACT(snapshot, '$.Incidents'))
	             ELSE 0 END AS incident_count`
}

// KeysetCursorPredicate returns the MySQL keyset cursor predicate using an
// explicit OR decomposition:
// started_at < ? OR (started_at = ? AND instance_id < ?).
//
// The decomposition is required for the same measured reason as
// [mysql.ArmedTimerKeysetPredicate] below: MySQL's optimizer does not treat a
// row constructor as an index range condition, so the row-value spelling plans
// as a full index walk. (An earlier revision of this comment attributed it to
// cross-type row-value nullability semantics for DESC cursors; that was never
// the operative cause.)
func (mysql) KeysetCursorPredicate() string {
	return "AND (started_at < ? OR (started_at = ? AND instance_id < ?)) "
}

// KeysetCursorArgCount returns 3 because the MySQL predicate binds cursorTime
// twice (once for < and once for =) then cursorID.
func (mysql) KeysetCursorArgCount() int { return 3 }

// ArmedTimerKeysetPredicate returns the MySQL armed-timer keyset predicate as
// an explicit lexicographic OR decomposition.
//
// A row value MUST NOT be used here, and the divergence from Postgres and
// SQLite is deliberate: MySQL's optimizer does not treat a row constructor as
// an index range condition. Measured over 50k rows, the row-value form plans
// as `type: index` with `possible_keys: NULL` and reads rows in proportion to
// cursor depth (≈21,650 mid-table), so paging becomes quadratic overall —
// slower than the full scan this predicate exists to replace. The expanded
// form plans as `type: range` and reads about one page.
func (mysql) ArmedTimerKeysetPredicate() string {
	return "AND (next_run > ? OR (next_run = ? AND (instance_id > ? OR (instance_id = ? AND timer_id > ?)))) "
}

// ArmedTimerKeysetArgs binds next_run twice and instance_id twice — once for
// the strict-greater branch and once for the tie branch — matching the
// expanded predicate's five placeholders.
func (mysql) ArmedTimerKeysetArgs(nextRun any, instanceID, timerID string) []any {
	return []any{nextRun, nextRun, instanceID, instanceID, timerID}
}

// TimestampsAsText reports that MySQL stores timestamps as native DATETIME
// values (with loc=UTC in the DSN). The database/sql driver binds and scans
// time.Time directly; no RFC3339Nano text encoding is needed.
func (mysql) TimestampsAsText() bool { return false }
