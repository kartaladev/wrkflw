package store_test

import (
	"database/sql"
	"slices"
	"sort"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kartaladev/wrkflw/internal/atrest"
	casbinauthz "github.com/kartaladev/wrkflw/internal/authz/casbin"
	"github.com/kartaladev/wrkflw/internal/dbtest"
	"github.com/kartaladev/wrkflw/internal/persistence/dialect"
	"github.com/kartaladev/wrkflw/internal/persistence/store"
)

// TestAtRestParseMatchesLiveIntrospection_SQLite asserts the DDL reader's view
// of the SQLite schema equals what a real SQLite database actually creates.
// The everyday at-rest guard (internal/atrest) parses SQL text so it needs no
// Docker; this is (part of) the test that proves the parse is not quietly
// lying. Kept in its own test function, separate from the
// Postgres/MySQL leg below: dbtest.RunTestDatabase / RunTestMySQL FAIL rather
// than skip when Docker is unavailable, which would otherwise drag this
// Docker-free SQLite check down with them.
func TestAtRestParseMatchesLiveIntrospection_SQLite(t *testing.T) {
	ctx := t.Context()

	root, err := atrest.ModuleRoot()
	require.NoError(t, err)
	parsed, err := atrest.LoadSchemas(root)
	require.NoError(t, err)

	sqliteDB := dbtest.RunTestSQLite(t)
	sm, err := store.NewSQLiteMigrator(sqliteDB)
	require.NoError(t, err)
	require.NoError(t, sm.Up(ctx))
	assert.ElementsMatch(t, parsedColumns(parsed["sqlite"], "wrkflw_"),
		liveColumns(introspectSQLite(t, sqliteDB)), "sqlite: parsed vs live")
}

// TestAtRestParseMatchesLiveIntrospection_PostgresAndMySQL is the Docker-gated
// half of the same cross-check, over Postgres and MySQL. ⚠ dbtest.RunTestDatabase
// / RunTestMySQL FAIL (require.NoError on a shared error) rather than skip when
// the daemon is unavailable — there is no silent skip to worry about here, only
// a hard failure that must be reported honestly.
func TestAtRestParseMatchesLiveIntrospection_PostgresAndMySQL(t *testing.T) {
	ctx := t.Context()

	root, err := atrest.ModuleRoot()
	require.NoError(t, err)
	parsed, err := atrest.LoadSchemas(root)
	require.NoError(t, err)

	pool := dbtest.RunTestDatabase(t)
	pm, err := store.NewPostgresMigrator(pool)
	require.NoError(t, err)
	require.NoError(t, pm.Up(ctx))
	assert.ElementsMatch(t, parsedColumns(parsed["postgres"], "wrkflw_"),
		liveColumns(introspectPostgres(t, pool)), "postgres: parsed vs live")

	mysqlDB := dbtest.RunTestMySQL(t) // already migrated
	mysqlLive := introspectMySQL(t, mysqlDB)
	// D2b normalizes MySQL's reserved-word column trigger_ -> trigger inside
	// LoadSchemas, but live MySQL introspection genuinely returns "trigger_".
	// Apply the identical normalization to the introspected side (exactly as
	// migration_parity_test.go's normalizeMySQLTriggerColumn already does for
	// its own comparison) or this test can never go green.
	normalizeMySQLTriggerColumn(mysqlLive)
	assert.ElementsMatch(t, parsedColumns(parsed["mysql"], "wrkflw_"),
		liveColumns(mysqlLive), "mysql: parsed vs live")
}

// TestAtRestParseMatchesLiveIntrospection_CasbinRule cross-checks the one
// table the wrkflw_* prefix filter can never see. casbin_rule is Postgres-only
// and applied by its own migrator, so it is cross-checked separately from
// the wrkflw_* set — the parity helpers filter it out (LIKE 'wrkflw_%').
func TestAtRestParseMatchesLiveIntrospection_CasbinRule(t *testing.T) {
	pool := dbtest.RunTestDatabase(t)
	require.NoError(t, casbinauthz.MigrateCasbin(t.Context(), pool))

	live := columnsOfTable(t, pool, "casbin_rule")
	root, err := atrest.ModuleRoot()
	require.NoError(t, err)
	parsed, err := atrest.LoadSchemas(root)
	require.NoError(t, err)

	assert.ElementsMatch(t, []string{"id", "ptype", "v0", "v1", "v2", "v3", "v4", "v5"}, live)
	assert.ElementsMatch(t, live, parsedColumnNames(parsed["postgres"], "casbin_rule"))
}

// TestCrossCheckColumns_CatchesTableIdentityAndPKDrift is the regression
// guard, Docker-free: it drives parsedColumns/liveColumns
// directly over synthetic fixtures reproducing the two ways the OLD
// bare-name comparison (parsedColumnNames/liveColumnNames, flattening
// across ALL tables) went blind. Proven live, against real containers,
// before this fix: refiling every wrkflw_timers column under a fabricated
// table name still passed the cross-check, because the flattened column
// NAME set is unchanged regardless of which table a name nominally
// belongs to.
func TestCrossCheckColumns_CatchesTableIdentityAndPKDrift(t *testing.T) {
	t.Parallel()

	t.Run("a column relabeled under a fabricated table name is caught", func(t *testing.T) {
		t.Parallel()

		parsed := atrest.Schema{Dialect: "test", Columns: map[atrest.ColumnKey]atrest.Column{
			{Table: "wrkflw_timers_typo", Column: "timer_id"}: {
				Table: "wrkflw_timers_typo", Name: "timer_id", Type: "TEXT", Keys: []string{"PK"},
			},
		}}
		live := logicalSchema{
			"wrkflw_timers": {"timer_id": colFacts{PrimaryKey: true}},
		}

		assert.NotElementsMatch(t, parsedColumns(parsed, "wrkflw_"), liveColumns(live),
			"a column filed under the WRONG table name must be caught, not matched by bare name alone")
	})

	t.Run("a PRIMARY KEY membership divergence between parsed and live is caught", func(t *testing.T) {
		t.Parallel()

		parsed := atrest.Schema{Dialect: "test", Columns: map[atrest.ColumnKey]atrest.Column{
			{Table: "wrkflw_outbox", Column: "id"}: {Table: "wrkflw_outbox", Name: "id", Type: "BIGINT"}, // no PK recorded
		}}
		live := logicalSchema{
			"wrkflw_outbox": {"id": colFacts{PrimaryKey: true}}, // live DB says it IS the PK
		}

		assert.NotElementsMatch(t, parsedColumns(parsed, "wrkflw_"), liveColumns(live),
			"a PRIMARY KEY membership divergence must be caught, not silently matched on name alone")
	})

	t.Run("agreeing (table, column, isPK) triples still match", func(t *testing.T) {
		t.Parallel()

		parsed := atrest.Schema{Dialect: "test", Columns: map[atrest.ColumnKey]atrest.Column{
			{Table: "wrkflw_outbox", Column: "id"}: {
				Table: "wrkflw_outbox", Name: "id", Type: "BIGINT", Keys: []string{"PK", "index"},
			},
		}}
		live := logicalSchema{
			"wrkflw_outbox": {"id": colFacts{PrimaryKey: true}},
		}

		assert.ElementsMatch(t, parsedColumns(parsed, "wrkflw_"), liveColumns(live),
			"agreement must still be recognised as agreement — this guard must not become a permanent fail")
	})
}

// TestEveryParsedTableIsCrossChecked is the closing guard for the
// uncross-checked-table hole (A19): every table the parser discovers must
// appear in SOME live cross-check above. Without this, a fifth migration set
// creating non-wrkflw_ tables would be discovered and parsed while being
// silently absent from every live comparison, and nothing in this file would
// say so — the per-column guards above each pin the tables their own
// assertions name, and none of them notices a table no assertion mentions.
func TestEveryParsedTableIsCrossChecked(t *testing.T) {
	root, err := atrest.ModuleRoot()
	require.NoError(t, err)
	parsed, err := atrest.LoadSchemas(root)
	require.NoError(t, err)

	assert.Empty(t, uncrossCheckedTables(parsed, coveredTables),
		"every table the parser discovers, across ALL THREE dialects, must be "+
			"compared against a live database by some test in this file")
}

// tableCoverage pairs one exemption from TestEveryParsedTableIsCrossChecked
// with the tests that actually cross-check the exempted tables against a live
// database. An entry must set exactly one of table (an exact name) or prefix
// (a name prefix); see exempts for what happens when it does not.
//
// by holds the test FUNCTIONS THEMSELVES, not their names, and that is the
// whole point of the type: delete a leg and this file stops compiling, so an
// exemption cannot outlive the justification for it.
//
// What this replaced was a pair of literal conditions —
// `tbl == "casbin_rule" || strings.HasPrefix(tbl, "wrkflw_")` — under a comment
// saying they were derived from the legs. They were not; they only described
// them, and the description failed OPEN. Deleting both casbin legs (and the
// casbinauthz import that goes unused with them) left casbin_rule cross-checked
// against a live database by nothing, while TestEveryParsedTableIsCrossChecked
// and the whole package stayed green against real Postgres, MySQL and SQLite
// containers. A guard whose exemptions merely describe its legs cannot see a
// leg go missing.
type tableCoverage struct {
	table  string
	prefix string
	by     []func(*testing.T)
}

// exempts reports whether tbl is one of the tables this entry's legs
// cross-check.
//
// An entry is honoured only if it is WELL FORMED: it must name at least one
// leg, and it must set exactly one of table or prefix. A malformed entry
// exempts NOTHING, so the tables it meant to hide are reported instead. Both
// halves of that fail closed deliberately:
//
//   - No leg means no live cross-check, so there is nothing to exempt on. An
//     entry stripped of its legs stops hiding its tables rather than going on
//     hiding them silently.
//   - Setting BOTH table and prefix used to drop table and match on prefix
//     alone. That fails OPEN whenever the prefix is broader than the discarded
//     name: {table: "audit_log", prefix: "audit"} exempted audit_secret and
//     auditor_notes as well, on legs that cover neither. Someone who writes
//     both fields is trying to be MORE specific, not less, so silently
//     widening the exemption inverts the intent — and it is the very shape
//     this type exists to remove, one layer further in.
//
// Setting neither is malformed for the same reason it is dangerous: an empty
// prefix would make strings.HasPrefix true for every table and exempt the
// whole schema.
func (c tableCoverage) exempts(tbl string) bool {
	if len(c.by) == 0 {
		return false
	}
	if (c.table == "") == (c.prefix == "") {
		return false
	}
	if c.prefix != "" {
		return strings.HasPrefix(tbl, c.prefix)
	}

	return tbl == c.table
}

// coveredTables is the live exemption list for TestEveryParsedTableIsCrossChecked:
// every table matched here is compared against a real database by the legs named
// in its by field, so it is not reported as uncross-checked.
//
// Adding an entry means naming the legs that cover it, and deleting or renaming
// one of those legs stops this file compiling. The compiler checks the
// REFERENCE, not the leg's contents: a leg that has stopped cross-checking —
// gutted, skipped, or repointed at an unrelated func(*testing.T) — still
// satisfies it. TestCoveredTablesAreWellFormed pins the shape of the entries;
// nothing pins that a leg still does its job.
var coveredTables = []tableCoverage{
	{
		// Postgres-only, applied by its own migrator, and invisible to the
		// wrkflw_* prefix filters the parity helpers use (LIKE 'wrkflw_%').
		table: "casbin_rule",
		by: []func(*testing.T){
			TestAtRestParseMatchesLiveIntrospection_CasbinRule,
			TestAtRestKeysMatchLiveIntrospection_CasbinRule,
		},
	},
	{
		// The repo's own tables, cross-checked column-wise and key-wise on
		// every dialect that has them.
		prefix: "wrkflw_",
		by: []func(*testing.T){
			TestAtRestParseMatchesLiveIntrospection_SQLite,
			TestAtRestParseMatchesLiveIntrospection_PostgresAndMySQL,
			TestAtRestKeysMatchLiveIntrospection_SQLite,
			TestAtRestKeysMatchLiveIntrospection_PostgresAndMySQL,
		},
	},
}

// uncrossCheckedTables returns, sorted, every table in schemas' postgres,
// mysql and sqlite entries that no entry of covered exempts — i.e. every
// table the parser discovers FOR THOSE THREE DIALECTS that no test in this
// file compares against a live database.
//
// The three are hardcoded, and that is a real limit of this guard rather
// than a description of the schema: LoadSchemas keys schemas by whatever
// strings MigrationSets declares, so a fourth key is not visited here and
// its tables are not reported. That residual is pre-existing, is tracked
// separately, and this function does not close it.
//
// TestEveryParsedTableIsCrossChecked used to range only
// parsed["postgres"].Tables() — a table that exists ONLY in mysql or
// sqlite would never even be visited by the loop. Extracted as its own
// function so a synthetic fixture can drive it directly
// (TestUncrossCheckedTables), independent of what the real repo schema
// happens to contain today. covered is a parameter for the same reason: the
// exemption logic itself has to be drivable by a fixture.
func uncrossCheckedTables(schemas map[string]atrest.Schema, covered []tableCoverage) []string {
	missing := map[string]bool{}
	for _, dialectName := range []string{"postgres", "mysql", "sqlite"} {
		schema, ok := schemas[dialectName]
		if !ok {
			continue
		}
		for _, tbl := range schema.Tables() {
			if slices.ContainsFunc(covered, func(c tableCoverage) bool { return c.exempts(tbl) }) {
				continue
			}
			missing[tbl] = true
		}
	}

	names := make([]string, 0, len(missing))
	for tbl := range missing {
		names = append(names, tbl)
	}
	sort.Strings(names)

	return names
}

// TestUncrossCheckedTables drives uncrossCheckedTables over synthetic fixtures.
// It pins the check in BOTH directions, because a guard that refuses everything
// satisfies a "reported" assertion as easily as a correct one: a genuinely
// uncovered table must be reported, and a genuinely covered one must not be.
//
// TestEveryParsedTableIsCrossChecked calls the same helper but is deliberately
// not folded in here: it loads the real repo schema off disk rather than taking
// a fixture, so it has no shared call shape with these rows.
func TestUncrossCheckedTables(t *testing.T) {
	t.Parallel()

	// schemasWith builds a fixture in which each named dialect holds exactly
	// the tables listed for it, one column each. All three dialects are always
	// present; any not named is present but empty. Tables CAN be placed in more
	// than one dialect — schemaOf below could not do that, which is why the
	// dedup row that used it never actually exercised dedup.
	schemasWith := func(byDialect map[string][]string) map[string]atrest.Schema {
		schemas := map[string]atrest.Schema{}
		for _, dialectName := range []string{"postgres", "mysql", "sqlite"} {
			cols := map[atrest.ColumnKey]atrest.Column{}
			for _, tbl := range byDialect[dialectName] {
				cols[atrest.ColumnKey{Table: tbl, Column: "id"}] = atrest.Column{
					Table: tbl, Name: "id", Type: "BIGINT",
				}
			}
			schemas[dialectName] = atrest.Schema{Dialect: dialectName, Columns: cols}
		}

		return schemas
	}

	// schemaOf is the single-dialect shorthand: only dialectName is populated.
	schemaOf := func(dialectName string, tables ...string) map[string]atrest.Schema {
		return schemasWith(map[string][]string{dialectName: tables})
	}

	noopLeg := func(*testing.T) {}

	type testCase struct {
		name    string
		schemas map[string]atrest.Schema
		covered []tableCoverage
		assert  func(t *testing.T, got []string)
	}

	cases := []testCase{
		{
			name:    "a mysql-only uncovered table is reported, so every dialect is seen",
			schemas: schemaOf("mysql", "audit_log"),
			covered: coveredTables,
			assert: func(t *testing.T, got []string) {
				assert.Equal(t, []string{"audit_log"}, got,
					"a mysql-only, uncovered table must be reported as uncross-checked")
			},
		},
		{
			name:    "an exact-name entry exempts its table",
			schemas: schemaOf("postgres", "casbin_rule"),
			covered: coveredTables,
			assert: func(t *testing.T, got []string) {
				assert.Empty(t, got,
					"casbin_rule is cross-checked by the casbin legs and must stay exempt")
			},
		},
		{
			name:    "a prefix entry exempts every table carrying the prefix",
			schemas: schemaOf("sqlite", "wrkflw_timers", "wrkflw_outbox"),
			covered: coveredTables,
			assert: func(t *testing.T, got []string) {
				assert.Empty(t, got,
					"wrkflw_* tables are cross-checked by the wrkflw_* legs and must stay exempt")
			},
		},
		{
			name:    "covered and uncovered tables in one schema: only the uncovered one is reported",
			schemas: schemaOf("postgres", "casbin_rule", "wrkflw_timers", "audit_log"),
			covered: coveredTables,
			assert: func(t *testing.T, got []string) {
				assert.Equal(t, []string{"audit_log"}, got,
					"the exemptions must hide exactly their own tables, not suppress the report")
			},
		},
		{
			name:    "an entry naming no leg exempts nothing",
			schemas: schemaOf("postgres", "casbin_rule"),
			covered: []tableCoverage{{table: "casbin_rule"}},
			assert: func(t *testing.T, got []string) {
				assert.Equal(t, []string{"casbin_rule"}, got,
					"an exemption with no live cross-check behind it must not exempt anything")
			},
		},
		{
			name:    "an entry naming a leg exempts, so the previous row failed for the missing leg",
			schemas: schemaOf("postgres", "casbin_rule"),
			covered: []tableCoverage{{table: "casbin_rule", by: []func(*testing.T){noopLeg}}},
			assert: func(t *testing.T, got []string) {
				assert.Empty(t, got,
					"the same fixture with a leg attached must be exempt — otherwise the row "+
						"above proves nothing about the leg")
			},
		},
		{
			// C1: the row this replaces used a single-dialect fixture, so it
			// never placed a table in two dialects and the dedup mutant
			// survived it. Its message claimed sorted AND deduplicated; only
			// sorted was pinned. Split into two rows, one claim each.
			name: "one table present in all three dialects is reported exactly once",
			schemas: schemasWith(map[string][]string{
				"postgres": {"audit_log"},
				"mysql":    {"audit_log", "zeta_log"},
				"sqlite":   {"audit_log"},
			}),
			covered: coveredTables,
			assert: func(t *testing.T, got []string) {
				assert.Equal(t, []string{"audit_log", "zeta_log"}, got,
					"audit_log is discovered in all three dialects and must be reported "+
						"once, not three times")
			},
		},
		{
			name:    "the report is sorted, not in map-iteration order",
			schemas: schemaOf("postgres", "zeta_log", "audit_log"),
			covered: coveredTables,
			assert: func(t *testing.T, got []string) {
				assert.Equal(t, []string{"audit_log", "zeta_log"}, got,
					"the report must be sorted for determinism")
			},
		},
		{
			// C5: an entry setting BOTH table and prefix used to drop table and
			// match on prefix alone, exempting every table the broader prefix
			// reached. "audit" reaches all three of these; the legs cover only
			// audit_log. Fail closed: a malformed entry exempts nothing.
			name:    "an entry setting both table and prefix exempts nothing",
			schemas: schemaOf("postgres", "audit_log", "audit_secret", "auditor_notes"),
			covered: []tableCoverage{
				{table: "audit_log", prefix: "audit", by: []func(*testing.T){noopLeg}},
			},
			assert: func(t *testing.T, got []string) {
				assert.Equal(t, []string{"audit_log", "audit_secret", "auditor_notes"}, got,
					"a malformed entry must exempt nothing — dropping table and widening "+
						"to the prefix would hide audit_secret and auditor_notes on legs "+
						"that cover neither")
			},
		},
		{
			name:    "an entry setting neither table nor prefix exempts nothing",
			schemas: schemaOf("postgres", "anything_at_all"),
			covered: []tableCoverage{{by: []func(*testing.T){noopLeg}}},
			assert: func(t *testing.T, got []string) {
				assert.Equal(t, []string{"anything_at_all"}, got,
					"an empty prefix would make strings.HasPrefix true for every table; "+
						"the entry must be rejected instead")
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			tc.assert(t, uncrossCheckedTables(tc.schemas, tc.covered))
		})
	}
}

// TestCoveredTablesAreWellFormed pins the shape of the SHIPPED exemption list,
// which the rows above cannot: they drive synthetic lists, so a malformed entry
// in coveredTables itself would only surface as a table mysteriously being
// reported. exempts fails closed on a malformed entry, so this test is the
// thing that says WHY rather than leaving the next reader to work it out.
//
// It does not — and cannot — check that a leg still cross-checks anything. The
// compiler checks that the reference resolves; nothing checks the body.
func TestCoveredTablesAreWellFormed(t *testing.T) {
	t.Parallel()

	require.NotEmpty(t, coveredTables, "an empty exemption list would make the guard vacuous")

	for _, c := range coveredTables {
		name := c.table
		if name == "" {
			name = c.prefix + "*"
		}

		t.Run(name, func(t *testing.T) {
			t.Parallel()

			assert.NotEmpty(t, c.by,
				"an exemption claims a live cross-check exists, so it must name at least one leg")
			assert.NotEqual(t, c.table == "", c.prefix == "",
				"an entry must set exactly one of table or prefix: setting both silently "+
					"widens the exemption to the prefix, setting neither matches everything")
		})
	}
}

// parsedColumnNames returns the bare column names (no table qualifier) of
// every ColumnKey in s whose table name carries prefix, per
// atrest.ColumnKeysWithPrefix. prefix is either a true prefix ("wrkflw_") or a full
// table name used as a self-matching prefix ("casbin_rule"). Used only by
// the casbin_rule cross-check (TestAtRestParseMatchesLiveIntrospection_
// CasbinRule), which already pins an exact table name and so is not
// exposed to the table-identity blindness parsedColumns/liveColumns fix
// below.
func parsedColumnNames(s atrest.Schema, prefix string) []string {
	keys := atrest.ColumnKeysWithPrefix(s, prefix)
	names := make([]string, 0, len(keys))
	for _, k := range keys {
		names = append(names, k.Column)
	}
	return names
}

// crossCheckColumn identifies one column for the parsed-vs-live
// wrkflw_*-table cross-check by (table, column, isPK) rather than by bare
// column name alone. Comparing bare names only was
// blind to which TABLE a column belonged to — a column refiled under a
// wrong/fabricated table name still matched by name — and blind to every
// key property, including PRIMARY KEY membership.
type crossCheckColumn struct {
	Table      string
	Column     string
	PrimaryKey bool
}

// parsedColumns returns the (table, column, isPK) triples the atrest
// parser derived for every ColumnKey in s whose table name carries
// prefix, per atrest.ColumnKeysWithPrefix.
func parsedColumns(s atrest.Schema, prefix string) []crossCheckColumn {
	keys := atrest.ColumnKeysWithPrefix(s, prefix)
	cols := make([]crossCheckColumn, 0, len(keys))
	for _, k := range keys {
		cols = append(cols, crossCheckColumn{
			Table:      k.Table,
			Column:     k.Column,
			PrimaryKey: slices.Contains(s.Columns[k].Keys, "PK"),
		})
	}
	return cols
}

// liveColumns flattens a logicalSchema (as returned by introspectPostgres
// / introspectMySQL / introspectSQLite in migration_parity_test.go) into
// its (table, column, isPK) triples — consuming colFacts.PrimaryKey rather
// than adding a new field to it (colFacts and the introspect* helpers are
// consumed here, not modified).
func liveColumns(s logicalSchema) []crossCheckColumn {
	var cols []crossCheckColumn
	for table, facts := range s {
		for name, f := range facts {
			cols = append(cols, crossCheckColumn{Table: table, Column: name, PrimaryKey: f.PrimaryKey})
		}
	}
	return cols
}

// columnsOfTable returns the bare column names of one Postgres table,
// unfiltered by the "wrkflw_%" prefix the parity helpers apply — needed here
// because casbin_rule falls outside that prefix.
func columnsOfTable(t *testing.T, pool *pgxpool.Pool, table string) []string {
	t.Helper()
	rows, err := pool.Query(t.Context(), `
		SELECT column_name FROM information_schema.columns
		WHERE table_schema = 'public' AND table_name = $1`, table)
	require.NoError(t, err)
	defer rows.Close()
	var cols []string
	for rows.Next() {
		var c string
		require.NoError(t, rows.Scan(&c))
		cols = append(cols, c)
	}
	require.NoError(t, rows.Err(), "casbin_rule columns iteration error")
	return cols
}

// ── Live key/index cross-check (the /code-review P3-b fix) ───────────────────
//
// crossCheckColumn above carries (Table, Column, PrimaryKey) only, so the live
// comparison validated ZERO of the UNIQUE / index / index-predicate facts — and
// those are exactly what the generated `keyed` column publishes. 18 postgres
// wrkflw_* columns carry a non-PK key, and every CREATE INDEX defect
// /code-review found in the parser passed all four cross-check tests. The
// helpers below are SIBLINGS: colFacts and the introspect* helpers in
// migration_parity_test.go are consumed, never modified.

// pgIndexColumnsQuery lists every (table, column) that participates in any
// index on a wrkflw_* table, with the two flags that decide the key's kind.
// Unlike postgresExplicitIndexQuery (which lists index NAMES and therefore has
// to exclude the implicit ones), this needs the implicit indexes too: a PRIMARY
// KEY and a UNIQUE constraint are precisely the key facts being checked.
const pgIndexColumnsQuery = `
	SELECT t.relname, a.attname, i.indisprimary, i.indisunique
	FROM   pg_index i
	JOIN   pg_class     t ON t.oid = i.indrelid
	JOIN   pg_namespace n ON n.oid = t.relnamespace
	JOIN   pg_attribute a ON a.attrelid = t.oid AND a.attnum = ANY(i.indkey)
	WHERE  n.nspname = 'public' AND t.relname LIKE 'wrkflw\_%'`

// pgPartialIndexQuery lists the rendered WHERE predicate of every partial index
// on a wrkflw_* table, which is where the parser's "index-predicate" annotation
// comes from.
const pgPartialIndexQuery = `
	SELECT t.relname, pg_get_expr(i.indpred, i.indrelid)
	FROM   pg_index i
	JOIN   pg_class     t ON t.oid = i.indrelid
	JOIN   pg_namespace n ON n.oid = t.relnamespace
	WHERE  n.nspname = 'public' AND t.relname LIKE 'wrkflw\_%' AND i.indpred IS NOT NULL`

// mysqlIndexColumnsQuery lists every indexed (table, column) with the index
// name and uniqueness. Foreign-key backing indexes are excluded exactly as
// mysqlExplicitIndexQuery excludes them: MySQL auto-creates an index for
// fk_journal_instance, and FOREIGN KEY columns are deliberately kept out of
// `keyed`, so including it would report a divergence that is a decision.
const mysqlIndexColumnsQuery = `
	SELECT s.table_name, s.column_name, s.index_name, s.non_unique
	FROM   information_schema.statistics s
	WHERE  s.table_schema = DATABASE()
	  AND  s.table_name LIKE 'wrkflw\_%'
	  AND  s.index_name NOT IN (
	         SELECT tc.constraint_name
	         FROM   information_schema.table_constraints tc
	         WHERE  tc.table_schema = DATABASE()
	           AND  tc.constraint_type = 'FOREIGN KEY')`

// sqlitePrimaryKeyQuery and sqliteIndexColumnsQuery read the pragma
// table-valued functions rather than interpolating table names into PRAGMA
// statements. index_list.origin distinguishes a PRIMARY KEY backing index
// ("pk"), a UNIQUE constraint backing index ("u") and a CREATE INDEX ("c") —
// which is what keeps a TEXT primary key from being reported as UNIQUE.
const (
	sqlitePrimaryKeyQuery = `
		SELECT m.name, ti.name
		FROM   sqlite_master m
		JOIN   pragma_table_info(m.name) ti
		WHERE  m.type = 'table' AND m.name LIKE 'wrkflw\_%' ESCAPE '\' AND ti.pk > 0`

	sqliteIndexColumnsQuery = `
		SELECT m.name, ii.name, il.origin, il."unique"
		FROM   sqlite_master m
		JOIN   pragma_index_list(m.name) il
		JOIN   pragma_index_info(il.name) ii
		WHERE  m.type = 'table' AND m.name LIKE 'wrkflw\_%' ESCAPE '\'`
)

// parsedKeys returns the atrest parser's key annotations for every key-BEARING
// column whose table carries prefix, sorted. Columns with no key are omitted so
// both sides of the comparison describe the same thing.
func parsedKeys(s atrest.Schema, prefix string) map[atrest.ColumnKey][]string {
	out := map[atrest.ColumnKey][]string{}
	for _, k := range atrest.ColumnKeysWithPrefix(s, prefix) {
		col := s.Columns[k]
		if len(col.Keys) == 0 {
			continue
		}
		keys := slices.Clone(col.Keys)
		sort.Strings(keys)
		out[k] = keys
	}
	return out
}

// addLiveKey records one key label for one live column, de-duplicated.
func addLiveKey(keys map[atrest.ColumnKey][]string, table, column, label string) {
	k := atrest.ColumnKey{Table: table, Column: column}
	if !slices.Contains(keys[k], label) {
		keys[k] = append(keys[k], label)
	}
}

// sortLiveKeys sorts each column's labels so the comparison is order-free.
func sortLiveKeys(keys map[atrest.ColumnKey][]string) map[atrest.ColumnKey][]string {
	for k := range keys {
		sort.Strings(keys[k])
	}
	return keys
}

// withoutSQLLiterals blanks single-quoted string literals so a coarse
// identifier scan of a rendered index predicate cannot mistake a literal's
// contents for a column name — `WHERE status = 'error'` must not annotate a
// column named error.
func withoutSQLLiterals(s string) string {
	var b strings.Builder
	inLiteral := false
	for i := range len(s) {
		switch {
		case s[i] == '\'':
			inLiteral = !inLiteral
			b.WriteByte(' ')
		case inLiteral:
			b.WriteByte(' ')
		default:
			b.WriteByte(s[i])
		}
	}
	return b.String()
}

// identifierTokens returns every maximal run of identifier characters in s.
func identifierTokens(s string) []string {
	return strings.FieldsFunc(s, func(r rune) bool {
		return r != '_' && (r < 'a' || r > 'z') && (r < 'A' || r > 'Z') && (r < '0' || r > '9')
	})
}

// liveKeysPostgres derives, from a live Postgres database, the same key
// vocabulary the parser records: PK, UNIQUE, index and index-predicate.
func liveKeysPostgres(t *testing.T, pool *pgxpool.Pool) map[atrest.ColumnKey][]string {
	t.Helper()
	ctx := t.Context()

	keys := map[atrest.ColumnKey][]string{}

	rows, err := pool.Query(ctx, pgIndexColumnsQuery)
	require.NoError(t, err)
	for rows.Next() {
		var (
			table, column       string
			isPrimary, isUnique bool
		)
		require.NoError(t, rows.Scan(&table, &column, &isPrimary, &isUnique))
		switch {
		case isPrimary:
			addLiveKey(keys, table, column, "PK")
		case isUnique:
			addLiveKey(keys, table, column, "UNIQUE")
		default:
			addLiveKey(keys, table, column, "index")
		}
	}
	rows.Close()
	require.NoError(t, rows.Err())

	columnsByTable := map[string]map[string]bool{}
	colRows, err := pool.Query(ctx, `
		SELECT table_name, column_name FROM information_schema.columns
		WHERE table_schema = 'public' AND table_name LIKE 'wrkflw\_%'`)
	require.NoError(t, err)
	for colRows.Next() {
		var table, column string
		require.NoError(t, colRows.Scan(&table, &column))
		if columnsByTable[table] == nil {
			columnsByTable[table] = map[string]bool{}
		}
		columnsByTable[table][column] = true
	}
	colRows.Close()
	require.NoError(t, colRows.Err())

	predRows, err := pool.Query(ctx, pgPartialIndexQuery)
	require.NoError(t, err)
	for predRows.Next() {
		var table, predicate string
		require.NoError(t, predRows.Scan(&table, &predicate))
		for _, token := range identifierTokens(withoutSQLLiterals(predicate)) {
			if columnsByTable[table][token] {
				addLiveKey(keys, table, token, "index-predicate")
			}
		}
	}
	predRows.Close()
	require.NoError(t, predRows.Err())

	return sortLiveKeys(keys)
}

// liveKeysMySQL derives the same key vocabulary from a live MySQL database.
// MySQL has no partial indexes, so "index-predicate" cannot arise: the two
// Postgres partial indexes are declared there as composite indexes that fold
// the predicate column into the key, which is a documented, deliberate
// divergence (see TestMigrationParity_IndexNamesConverge).
func liveKeysMySQL(t *testing.T, db *sql.DB) map[atrest.ColumnKey][]string {
	t.Helper()

	keys := map[atrest.ColumnKey][]string{}

	rows, err := db.QueryContext(t.Context(), mysqlIndexColumnsQuery)
	require.NoError(t, err)
	defer func() { _ = rows.Close() }()

	mysqlTriggerCol := dialect.NewMySQL().JournalTriggerColumn()
	canonicalTriggerCol := dialect.NewPostgres().JournalTriggerColumn()

	for rows.Next() {
		var (
			table, column, indexName string
			nonUnique                int
		)
		require.NoError(t, rows.Scan(&table, &column, &indexName, &nonUnique))
		// D2b: live MySQL genuinely returns trigger_; the parse is normalized to
		// the canonical name, so normalize this side too (the repo's precedent
		// normalizes the LIVE side — migration_parity_test.go).
		if table == "wrkflw_journal" && column == mysqlTriggerCol {
			column = canonicalTriggerCol
		}
		switch {
		case indexName == "PRIMARY":
			addLiveKey(keys, table, column, "PK")
		case nonUnique == 0:
			addLiveKey(keys, table, column, "UNIQUE")
		default:
			addLiveKey(keys, table, column, "index")
		}
	}
	require.NoError(t, rows.Err())

	return sortLiveKeys(keys)
}

// liveKeysSQLite derives the same key vocabulary from a live SQLite database.
// index_list.origin is what keeps a TEXT PRIMARY KEY's backing autoindex from
// being reported as a UNIQUE constraint.
func liveKeysSQLite(t *testing.T, db *sql.DB) map[atrest.ColumnKey][]string {
	t.Helper()
	ctx := t.Context()

	keys := map[atrest.ColumnKey][]string{}

	pkRows, err := db.QueryContext(ctx, sqlitePrimaryKeyQuery)
	require.NoError(t, err)
	for pkRows.Next() {
		var table, column string
		require.NoError(t, pkRows.Scan(&table, &column))
		addLiveKey(keys, table, column, "PK")
	}
	_ = pkRows.Close()
	require.NoError(t, pkRows.Err())

	idxRows, err := db.QueryContext(ctx, sqliteIndexColumnsQuery)
	require.NoError(t, err)
	defer func() { _ = idxRows.Close() }()
	for idxRows.Next() {
		var (
			table, column, origin string
			unique                int
		)
		require.NoError(t, idxRows.Scan(&table, &column, &origin, &unique))
		switch origin {
		case "pk":
			addLiveKey(keys, table, column, "PK")
		case "u":
			addLiveKey(keys, table, column, "UNIQUE")
		default:
			if unique == 1 {
				addLiveKey(keys, table, column, "UNIQUE")
			}
			addLiveKey(keys, table, column, "index")
		}
	}
	require.NoError(t, idxRows.Err())

	return sortLiveKeys(keys)
}

// TestAtRestKeysMatchLiveIntrospection_SQLite is the Docker-free half of the
// key-level cross-check: every PK / UNIQUE / index annotation the parser
// derived for SQLite must equal what a migrated SQLite database actually
// carries. Before this, the live comparison checked PRIMARY KEY membership and
// nothing else, so every CREATE INDEX defect in the parser — a lost GIN index,
// a lost schema-qualified index, a lost index after a non-ASCII rune, a unique
// index recorded as a plain one — passed the cross-check untouched.
func TestAtRestKeysMatchLiveIntrospection_SQLite(t *testing.T) {
	ctx := t.Context()

	root, err := atrest.ModuleRoot()
	require.NoError(t, err)
	parsed, err := atrest.LoadSchemas(root)
	require.NoError(t, err)

	sqliteDB := dbtest.RunTestSQLite(t)
	sm, err := store.NewSQLiteMigrator(sqliteDB)
	require.NoError(t, err)
	require.NoError(t, sm.Up(ctx))

	want := parsedKeys(parsed["sqlite"], "wrkflw_")
	require.NotEmpty(t, want, "precondition: the parse must record some keys, or this compares nothing")
	assert.Equal(t, want, liveKeysSQLite(t, sqliteDB), "sqlite: parsed vs live key annotations")
}

// TestAtRestKeysMatchLiveIntrospection_PostgresAndMySQL is the Docker-gated
// half, and the only place the index-predicate annotation is checked against a
// real planner-visible partial index.
func TestAtRestKeysMatchLiveIntrospection_PostgresAndMySQL(t *testing.T) {
	ctx := t.Context()

	root, err := atrest.ModuleRoot()
	require.NoError(t, err)
	parsed, err := atrest.LoadSchemas(root)
	require.NoError(t, err)

	pool := dbtest.RunTestDatabase(t)
	pm, err := store.NewPostgresMigrator(pool)
	require.NoError(t, err)
	require.NoError(t, pm.Up(ctx))

	wantPG := parsedKeys(parsed["postgres"], "wrkflw_")
	require.NotEmpty(t, wantPG)
	assert.Equal(t, wantPG, liveKeysPostgres(t, pool), "postgres: parsed vs live key annotations")

	mysqlDB := dbtest.RunTestMySQL(t) // already migrated
	wantMySQL := parsedKeys(parsed["mysql"], "wrkflw_")
	require.NotEmpty(t, wantMySQL)
	assert.Equal(t, wantMySQL, liveKeysMySQL(t, mysqlDB), "mysql: parsed vs live key annotations")
}

// TestAtRestKeysMatchLiveIntrospection_CasbinRule closes the second half of the
// P3-b finding: the casbin leg compared BARE COLUMN NAMES, so casbin_rule — the
// one table outside the wrkflw_ prefix, and the one whose ptype index the
// withdrawn round-1 safety claim turned on — had neither its primary key nor
// its ptype index verified against a live database.
func TestAtRestKeysMatchLiveIntrospection_CasbinRule(t *testing.T) {
	pool := dbtest.RunTestDatabase(t)
	require.NoError(t, casbinauthz.MigrateCasbin(t.Context(), pool))

	root, err := atrest.ModuleRoot()
	require.NoError(t, err)
	parsed, err := atrest.LoadSchemas(root)
	require.NoError(t, err)

	live := map[atrest.ColumnKey][]string{}
	rows, err := pool.Query(t.Context(), `
		SELECT t.relname, a.attname, i.indisprimary, i.indisunique
		FROM   pg_index i
		JOIN   pg_class     t ON t.oid = i.indrelid
		JOIN   pg_namespace n ON n.oid = t.relnamespace
		JOIN   pg_attribute a ON a.attrelid = t.oid AND a.attnum = ANY(i.indkey)
		WHERE  n.nspname = 'public' AND t.relname = 'casbin_rule'`)
	require.NoError(t, err)
	defer rows.Close()
	for rows.Next() {
		var (
			table, column       string
			isPrimary, isUnique bool
		)
		require.NoError(t, rows.Scan(&table, &column, &isPrimary, &isUnique))
		switch {
		case isPrimary:
			addLiveKey(live, table, column, "PK")
		case isUnique:
			addLiveKey(live, table, column, "UNIQUE")
		default:
			addLiveKey(live, table, column, "index")
		}
	}
	require.NoError(t, rows.Err())

	want := parsedKeys(parsed["postgres"], "casbin_rule")
	require.NotEmpty(t, want)
	assert.Equal(t, want, sortLiveKeys(live), "casbin_rule: parsed vs live key annotations")
}
