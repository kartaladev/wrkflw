package store_test

import (
	"database/sql"
	"fmt"
	"strings"
	"testing"
	"time"

	_ "github.com/go-sql-driver/mysql" // register "mysql" driver for the pinned-connection pool
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	_ "modernc.org/sqlite" // register "sqlite" driver

	"github.com/kartaladev/wrkflw/internal/dbtest"
	"github.com/kartaladev/wrkflw/internal/persistence/dialect"
	"github.com/kartaladev/wrkflw/internal/persistence/store"
	"github.com/kartaladev/wrkflw/runtime/kernel"
)

// The armed-timer page must SEEK the composite keyset index, not scan the table
// (ADR-0159). Correctness tests cannot see the difference — a full scan returns
// exactly the same rows — so the guard has to read the query planner, and the
// fixture has to be large enough and the cursor deep enough that a scan and a
// seek are distinguishable.
//
// The numbers below are load-bearing:
//   - seekRowCount is large enough that a scan is expensive and, on Postgres,
//     that the planner has a real choice to make.
//   - seekCursorRow sits NEAR THE END of the key range. With the cursor at the
//     start, a non-seeking predicate also touches few rows before its first
//     match, and the regression is invisible.
const (
	seekRowCount   = 10_000
	seekCursorRow  = 9_500
	seekPageLimit  = 50
	seekInstanceID = "seek-inst"
	seekIndexName  = "wrkflw_timers_keyset_idx"
)

// seekBase anchors the fixture's next_run values. Second granularity keeps every
// row distinct on all three backends (MySQL's DATETIME(6) is the coarsest at
// microseconds) so key order equals row index order.
var seekBase = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

// seekTimerID is zero-padded so lexicographic timer_id order — the third
// component of the keyset — matches numeric order.
func seekTimerID(i int) string { return fmt.Sprintf("t%05d", i) }

func seekNextRun(i int) time.Time { return seekBase.Add(time.Duration(i) * time.Second) }

// seedSeekFixture bulk-inserts seekRowCount wrkflw_timers rows.
//
// It writes raw multi-row INSERTs rather than looping over UpsertJob: 10 000
// single-row upserts across three backends dominates the package's runtime.
// wrkflw_timers carries no foreign key to wrkflw_instances on any dialect (only
// wrkflw_journal does), so no instance rows are needed.
//
// next_run is bound through the dialect's timestamp codec via
// [store.TimeArgForDialectValue]. On a TEXT-timestamp backend a raw time.Time
// would be stringified in Go's default non-ISO8601 form, the keyset predicate
// would then match nothing, and the test would "prove" a seek over an empty
// range.
func seedSeekFixture(t *testing.T, conn any, d dialect.Dialect) {
	t.Helper()

	s, err := store.New(conn, d)
	require.NoError(t, err)
	q := s.QuerierForTest(t.Context())

	const batch = 500
	for start := 0; start < seekRowCount; start += batch {
		end := min(start+batch, seekRowCount)

		var sb strings.Builder
		sb.WriteString(`INSERT INTO wrkflw_timers ` +
			`(instance_id, timer_id, next_run, kind, def_id, def_version, trigger_kind, trigger_payload) VALUES `)
		args := make([]any, 0, (end-start)*8)
		for i := start; i < end; i++ {
			if i > start {
				sb.WriteString(",")
			}
			sb.WriteString("(?,?,?,?,?,?,?,?)")
			args = append(args,
				seekInstanceID, seekTimerID(i), store.TimeArgForDialectValue(d, seekNextRun(i)),
				0, "seek-def", 1, 0, nil)
		}

		_, err := q.Exec(t.Context(), d.Rebind(sb.String()), args...)
		require.NoError(t, err, "bulk insert wrkflw_timers rows [%d,%d)", start, end)
	}
}

// seekPageStatement returns the EXACT statement and bind arguments
// [store.TimerStore.ListArmedPage] issues for a cursor at seekCursorRow. It goes
// through ListArmedPageSQLForTest deliberately: a hand-copied duplicate would
// keep reporting a seek long after the production predicate stopped being one.
func seekPageStatement(t *testing.T, ts *store.TimerStore) (cursor, sqlText string, args []any) {
	t.Helper()
	cursor, err := kernel.EncodeArmedTimerCursor(seekNextRun(seekCursorRow), seekInstanceID, seekTimerID(seekCursorRow))
	require.NoError(t, err)
	sqlText, args, err = ts.ListArmedPageSQLForTest(cursor, seekPageLimit+1)
	require.NoError(t, err)
	return cursor, sqlText, args
}

// assertSeekPageContent pins that the plan under test is the plan for a page
// that actually returns the expected rows. Without it a predicate matching
// nothing would still report a tidy index seek.
func assertSeekPageContent(t *testing.T, ts *store.TimerStore, cursor string) {
	t.Helper()
	page, err := ts.ListArmedPage(t.Context(), kernel.ArmedTimerFilter{Cursor: cursor, Limit: seekPageLimit})
	require.NoError(t, err)
	require.Len(t, page.Items, seekPageLimit, "the page must be full — %d rows follow the cursor", seekRowCount-seekCursorRow-1)
	assert.Equal(t, seekTimerID(seekCursorRow+1), page.Items[0].TimerID, "the page resumes at the row after the cursor")
	assert.True(t, page.HasMore)
}

// explainPostgres runs EXPLAIN over sqlText and returns the plan as text.
func explainPostgres(t *testing.T, pool *pgxpool.Pool, sqlText string, args ...any) string {
	t.Helper()
	rows, err := pool.Query(t.Context(), "EXPLAIN "+sqlText, args...)
	require.NoError(t, err)
	defer rows.Close()

	var lines []string
	for rows.Next() {
		var line string
		require.NoError(t, rows.Scan(&line))
		lines = append(lines, line)
	}
	require.NoError(t, rows.Err())
	return strings.Join(lines, "\n")
}

// explainSQLRows runs sqlText on a database/sql handle and returns each result
// row as a column-name → value map, so MySQL's EXPLAIN can be asserted by
// column (type, key) rather than by substring over the whole row.
func explainSQLRows(t *testing.T, db *sql.DB, sqlText string, args ...any) []map[string]string {
	t.Helper()
	rows, err := db.QueryContext(t.Context(), sqlText, args...)
	require.NoError(t, err)
	defer func() { _ = rows.Close() }()

	cols, err := rows.Columns()
	require.NoError(t, err)

	var out []map[string]string
	for rows.Next() {
		cells := make([]sql.NullString, len(cols))
		dest := make([]any, len(cols))
		for i := range cells {
			dest[i] = &cells[i]
		}
		require.NoError(t, rows.Scan(dest...))

		row := make(map[string]string, len(cols))
		for i, c := range cols {
			row[c] = cells[i].String
		}
		out = append(out, row)
	}
	require.NoError(t, rows.Err())
	return out
}

// mysqlSessionStatus reads one SESSION status counter. The counters are
// per-connection, which is the entire reason the MySQL case runs over a
// one-connection pool.
//
// SHOW ... LIKE takes no bind placeholder, so name is interpolated. It is a
// compile-time constant naming a MySQL status variable, never external input.
func mysqlSessionStatus(t *testing.T, db *sql.DB, name string) int64 {
	t.Helper()
	var varName string
	var value int64
	require.NoError(t,
		db.QueryRowContext(t.Context(), "SHOW SESSION STATUS LIKE '"+name+"'").Scan(&varName, &value),
		"read session status %s", name)
	return value
}

// TestTimerStoreListArmedPageSeeksIndex proves, per dialect, that a page deep
// into the key range is an index SEEK and not a table scan (ADR-0159).
//
// It is deliberately NOT built on forEachDialect: the MySQL case needs a
// single-connection pool it constructs itself (session status counters are
// per-connection) and must not run in parallel, since FLUSH STATUS is a
// server-level statement.
func TestTimerStoreListArmedPageSeeksIndex(t *testing.T) {
	t.Run("postgres", func(t *testing.T) {
		pool := dbtest.RunTestDatabase(t)
		require.NoError(t, store.MigratePostgres(t.Context(), pool))

		d := dialect.NewPostgres()
		seedSeekFixture(t, pool, d)

		// Without statistics the planner assumes a tiny table and sequential-
		// scans it, so the absence of an index node would prove nothing.
		_, err := pool.Exec(t.Context(), "ANALYZE wrkflw_timers")
		require.NoError(t, err)

		ts, err := store.NewTimerStore(pool, d)
		require.NoError(t, err)

		cursor, sqlText, args := seekPageStatement(t, ts)
		plan := explainPostgres(t, pool, sqlText, args...)
		t.Logf("postgres plan:\n%s", plan)

		assert.Contains(t, plan, seekIndexName, "the page must be planned over the composite keyset index")
		// Row-value form: the whole cursor triple has to reach the index as ONE
		// range condition. A plan that shows the comparison as a Filter (or no
		// Index Cond at all) is reading rows and discarding them.
		assert.Contains(t, plan, "Index Cond: (ROW(", "the cursor triple must be an index range condition, not a filter")
		// An Index Only Scan is impossible here and must never be asserted: the
		// projection selects trigger_payload, kind, def_id and def_version,
		// none of which are in the index.
		assert.NotContains(t, plan, "Seq Scan on wrkflw_timers", "a sequential scan is the regression this test exists to catch")

		assertSeekPageContent(t, ts, cursor)
	})

	// NOT parallel: FLUSH STATUS is a server-level statement and the counters
	// it resets are read back on the same connection a few statements later.
	t.Run("mysql", func(t *testing.T) {
		// RunTestMySQL hands back an 8-connection pool. Session status counters
		// are per-connection, so if FLUSH STATUS, the page query and SHOW
		// SESSION STATUS land on three different connections the test reads 0
		// and passes no matter what the predicate does. Build the pool here,
		// pinned to exactly one connection.
		dsn := dbtest.RunTestMySQLDSN(t)
		db, err := sql.Open("mysql", dsn)
		require.NoError(t, err)
		db.SetMaxOpenConns(1)
		db.SetMaxIdleConns(1)
		db.SetConnMaxLifetime(0)
		db.SetConnMaxIdleTime(0)
		t.Cleanup(func() { _ = db.Close() })
		require.NoError(t, db.PingContext(t.Context()))
		require.NoError(t, store.MigrateMySQL(t.Context(), db))

		d := dialect.NewMySQL()
		seedSeekFixture(t, db, d)
		_, err = db.ExecContext(t.Context(), "ANALYZE TABLE wrkflw_timers")
		require.NoError(t, err)

		ts, err := store.NewTimerStore(db, d)
		require.NoError(t, err)

		cursor, sqlText, args := seekPageStatement(t, ts)

		// PRIMARY assertion: the planner's own verdict.
		plan := explainSQLRows(t, db, "EXPLAIN "+sqlText, args...)
		t.Logf("mysql plan: %v", plan)
		require.Len(t, plan, 1, "single-table SELECT plans as one row")
		assert.Equal(t, "range", plan[0]["type"],
			"the expanded lexicographic OR must plan as a range scan; a row-value predicate degrades to type=index with possible_keys=NULL")
		assert.Equal(t, seekIndexName, plan[0]["key"], "the range must be taken on the composite keyset index")

		// Volume backstop. Handler_read_next counts next-row-in-key-order
		// requests, so a full index scan from the start of the key range reports
		// roughly seekCursorRow of them while a seek reports about one page.
		_, err = db.ExecContext(t.Context(), "FLUSH STATUS")
		require.NoError(t, err)
		assertSeekPageContent(t, ts, cursor)
		readNext := mysqlSessionStatus(t, db, "Handler_read_next")
		t.Logf("mysql Handler_read_next=%d (page limit %d)", readNext, seekPageLimit)

		// Positive is not padding: a 50-row page necessarily walks ~50 rows in
		// key order. Zero means the counters were read on a different connection
		// than the query ran on, which is the silent-pass this whole setup exists
		// to prevent.
		require.Positive(t, readNext, "Handler_read_next is 0 — the query and SHOW SESSION STATUS did not share a connection")
		assert.LessOrEqual(t, readNext, int64(4*seekPageLimit),
			"a %d-row page must read about a page of rows, not scan to the cursor", seekPageLimit)
	})

	t.Run("sqlite", func(t *testing.T) {
		db := dbtest.RunTestSQLite(t)

		d := dialect.NewSQLite()
		seedSeekFixture(t, db, d)

		ts, err := store.NewTimerStore(db, d)
		require.NoError(t, err)

		cursor, sqlText, args := seekPageStatement(t, ts)

		// EXPLAIN QUERY PLAN, never plain EXPLAIN — the latter returns VDBE
		// bytecode rows, in which no index name appears as plan text.
		rows := explainSQLRows(t, db, "EXPLAIN QUERY PLAN "+sqlText, args...)
		var details []string
		for _, r := range rows {
			details = append(details, r["detail"])
		}
		plan := strings.Join(details, "\n")
		t.Logf("sqlite plan:\n%s", plan)

		// SEARCH, with a trailing "(" introducing the index constraint — not
		// merely "USING INDEX". Asserting the index name alone is NOT enough:
		// a predicate the index cannot seek on degrades to
		// "SCAN wrkflw_timers USING INDEX wrkflw_timers_keyset_idx", which walks
		// all 10 000 rows in key order and still names the index. Measured: an
		// index-defeating rewrite of the same predicate produces exactly that
		// line, and only the SEARCH/"(" pair tells the two apart.
		assert.Contains(t, plan, "SEARCH wrkflw_timers USING INDEX "+seekIndexName+" (",
			"the page must SEARCH (seek into) the keyset index with the cursor triple as an index constraint")
		assert.NotContains(t, plan, "SCAN wrkflw_timers",
			"a full walk — table or index — is the regression this test exists to catch")

		assertSeekPageContent(t, ts, cursor)
	})
}
