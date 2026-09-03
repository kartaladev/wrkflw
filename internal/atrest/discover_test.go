package atrest_test

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kartaladev/wrkflw/internal/atrest"
)

func TestDiscoverMigrationDirs_FindsAllFourAndAllAreDeclared(t *testing.T) {
	t.Parallel()

	root, err := atrest.ModuleRoot()
	require.NoError(t, err)

	dirs, err := atrest.DiscoverMigrationDirs(root)
	require.NoError(t, err)

	assert.ElementsMatch(t, []string{
		"internal/authz/casbin/migrations",
		"internal/persistence/store/migrations/mysql",
		"internal/persistence/store/migrations/postgres",
		"internal/persistence/store/migrations/sqlite",
	}, dirs, "four migration directories exist; a hardcoded three-directory list is what lost casbin_rule")

	for _, d := range dirs {
		assert.Contains(t, atrest.MigrationSets, d,
			"a discovered migration directory with no MigrationSets entry must fail: "+
				"its columns would otherwise never be classified")
	}
	for declared := range atrest.MigrationSets {
		assert.Contains(t, dirs, declared,
			"a MigrationSets entry matching no directory is stale — delete it, or the next "+
				"undeclared migration set hides under it")
	}
}

// TestDiscoverMigrationDirs_FailsClosedOnDeeperNesting is the regression
// guard for deeper nesting: DiscoverMigrationDirs only matches a directory
// named "migrations" or whose PARENT is named "migrations" — a *.sql
// directory nested one level deeper under a "migrations" ancestor (e.g.
// migrations/postgres/v2/*.sql) matches neither rule and, before this fix,
// was silently discovered as nothing. Fail closed instead: error rather
// than let those migrations become invisible to every downstream at-rest
// classification while the generated security document stays green.
func TestDiscoverMigrationDirs_FailsClosedOnDeeperNesting(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module scratch\n\ngo 1.25\n"), 0o600))
	deep := filepath.Join(dir, "migrations", "postgres", "v2")
	require.NoError(t, os.MkdirAll(deep, 0o750))
	require.NoError(t, os.WriteFile(
		filepath.Join(deep, "0001_x.sql"),
		[]byte("-- +goose Up\nCREATE TABLE t (a TEXT);\n"),
		0o600,
	))

	_, err := atrest.DiscoverMigrationDirs(dir)
	require.Error(t, err, "migrations/postgres/v2 is two levels below \"migrations\", "+
		"which neither the base==\"migrations\" nor parentBase==\"migrations\" rule reaches")
	assert.Contains(t, err.Error(), "migrations/postgres/v2")
}

// TestLoadSchemas_MigrationSetsReconciliation exercises the two-way check
// described at atrest.MigrationSets: a directory DiscoverMigrationDirs finds
// that has no MigrationSets entry must fail LoadSchemas, and — separately —
// a MigrationSets entry matching no discovered directory must also fail.
// Both cases use synthetic module roots (a scratch go.mod + migrations
// tree) so the check is exercised against the real, unmodified
// atrest.MigrationSets: a directory under a synthetic root can never
// collide with the four real entries, so it is guaranteed "discovered but
// undeclared"; an empty synthetic root guarantees every real entry is
// "declared but undiscovered".
func TestLoadSchemas_MigrationSetsReconciliation(t *testing.T) {
	t.Parallel()

	type testCase struct {
		name   string
		root   func(t *testing.T) string
		assert func(t *testing.T, schemas map[string]atrest.Schema, err error)
	}

	cases := []testCase{
		{
			name: "a discovered directory with no MigrationSets entry fails",
			root: func(t *testing.T) string {
				t.Helper()
				dir := t.TempDir()
				require.NoError(t, os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module scratch\n\ngo 1.25\n"), 0o600))
				undeclared := filepath.Join(dir, "somewhere", "migrations")
				require.NoError(t, os.MkdirAll(undeclared, 0o750))
				require.NoError(t, os.WriteFile(
					filepath.Join(undeclared, "0001_x.sql"),
					[]byte("-- +goose Up\nCREATE TABLE t (a TEXT);\n"),
					0o600,
				))
				return dir
			},
			assert: func(t *testing.T, schemas map[string]atrest.Schema, err error) {
				require.Error(t, err)
				assert.Nil(t, schemas)
				assert.Contains(t, err.Error(), "somewhere/migrations")
			},
		},
		{
			name: "a MigrationSets entry matching no discovered directory fails",
			root: func(t *testing.T) string {
				t.Helper()
				dir := t.TempDir()
				require.NoError(t, os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module scratch\n\ngo 1.25\n"), 0o600))
				return dir
			},
			assert: func(t *testing.T, schemas map[string]atrest.Schema, err error) {
				require.Error(t, err)
				assert.Nil(t, schemas)
				assert.Contains(t, err.Error(), "MigrationSets")
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			root := tc.root(t)
			schemas, err := atrest.LoadSchemas(root)
			tc.assert(t, schemas, err)
		})
	}
}

// TestLoadSchemas_StaleMigrationSetsReportedDeterministically is the
// determinism regression guard: reconcileMigrationSets used to range
// MigrationSets and return on the FIRST stale entry it happened to visit —
// nondeterministic under Go's randomised map iteration, and with two or
// more stale entries the reported one varies run to run. A synthetic root
// with NO migrations directories at all makes every real MigrationSets
// entry (all four) "declared but undiscovered", so the error must name ALL
// FOUR, in the same sorted order, on every call — not just one of them.
func TestLoadSchemas_StaleMigrationSetsReportedDeterministically(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module scratch\n\ngo 1.25\n"), 0o600))

	wantSortedDirs := []string{
		"internal/authz/casbin/migrations",
		"internal/persistence/store/migrations/mysql",
		"internal/persistence/store/migrations/postgres",
		"internal/persistence/store/migrations/sqlite",
	}

	const iterations = 5
	for i := range iterations {
		schemas, err := atrest.LoadSchemas(dir)
		require.Error(t, err)
		assert.Nil(t, schemas)

		lastIdx := -1
		for _, want := range wantSortedDirs {
			idx := strings.Index(err.Error(), want)
			require.Greaterf(t, idx, lastIdx,
				"iteration %d: every stale entry must be named, in sorted order, not just the "+
					"first one a map range happened to visit: %s", i, err.Error())
			lastIdx = idx
		}
	}
}

func TestLoadSchemas_ColumnCensus(t *testing.T) {
	t.Parallel()

	root, err := atrest.ModuleRoot()
	require.NoError(t, err)
	schemas, err := atrest.LoadSchemas(root)
	require.NoError(t, err)

	// 79 wrkflw_* columns in every dialect; casbin_rule adds 8 to postgres only.
	assert.Len(t, schemas["postgres"].Columns, 87, "79 wrkflw_* + 8 casbin_rule")
	assert.Len(t, schemas["mysql"].Columns, 79)
	assert.Len(t, schemas["sqlite"].Columns, 79)

	// MySQL declares the journal payload column as trigger_ ("trigger" is
	// reserved). LoadSchemas normalizes it to the canonical name BEFORE returning,
	// sourced from dialect.*.JournalTriggerColumn() exactly as
	// normalizeMySQLTriggerColumn does in migration_parity_test.go. Without this
	// the cross-dialect key union is 88 and NOTHING downstream can go green.
	assert.Contains(t, schemas["mysql"].Columns,
		atrest.ColumnKey{Table: "wrkflw_journal", Column: "trigger"},
		"normalized to the canonical name")
	assert.NotContains(t, schemas["mysql"].Columns,
		atrest.ColumnKey{Table: "wrkflw_journal", Column: "trigger_"},
		"the raw MySQL alias must not escape LoadSchemas")

	// The normalization must be an EXACT (table, column) match, never a
	// prefix/suffix/substring rule: wrkflw_timers declares trigger_kind and
	// trigger_payload, which share the "trigger" prefix with the journal's
	// trigger_/trigger column but are themselves canonical (not reserved
	// words) in every dialect and must survive untouched.
	assert.Contains(t, schemas["mysql"].Columns,
		atrest.ColumnKey{Table: "wrkflw_timers", Column: "trigger_kind"},
		"trigger_kind must survive normalization unchanged — not caught by a prefix rule")
	assert.Contains(t, schemas["mysql"].Columns,
		atrest.ColumnKey{Table: "wrkflw_timers", Column: "trigger_payload"},
		"trigger_payload must survive normalization unchanged — not caught by a prefix rule")
}

func TestParserFailsClosedOnUnrecognisedStatements(t *testing.T) {
	t.Parallel()

	// Future schema changes resume as new numbered migration files, i.e.
	// ALTER TABLE. A CREATE TABLE-only reader sees zero columns there:
	// nothing unclassified, the generated block regenerating identically,
	// every non-Docker guard green, and a new column silently absent from
	// the security document. Verified free: the corpus has ZERO
	// "alter table" today.
	_, err := atrest.ParseSQL("postgres", "-- +goose Up\nALTER TABLE wrkflw_instances ADD COLUMN secret TEXT;\n")
	require.Error(t, err, "an unrecognised statement must be an ERROR, never a skip")
	assert.Contains(t, err.Error(), "ALTER TABLE")
}

// TestMigrationSetNotesCarryNoInternalEvidenceLabel guards against a
// consumer-facing regression: MigrationSet.Note is published verbatim into
// the generated "Data at rest" section by Render (it sources the casbin
// availability sentence from this field rather than retyping it, so there is
// one copy of the fact). An internal evidence-record id such as "(E3)",
// which points at an internal measurement record, is meaningless to a reader
// of a public security document. Note is consumer-facing prose from the
// moment it is written, not an internal scratch comment — this test is the
// regression guard for that rule.
func TestMigrationSetNotesCarryNoInternalEvidenceLabel(t *testing.T) {
	t.Parallel()

	evidenceLabel := regexp.MustCompile(`\(E\d+(?:,\s*E\d+)*\)`)

	for dir, set := range atrest.MigrationSets {
		assert.False(t, evidenceLabel.MatchString(set.Note),
			"MigrationSets[%q].Note = %q carries an internal evidence-record id "+
				"(e.g. (E3)) that a security-document reader cannot resolve — Note is published "+
				"verbatim, so state the fact plainly instead", dir, set.Note)
	}
}

// TestCasbinNoteIsConditionedOnTheMigrationHavingRun pins the casbin
// availability sentence Render publishes verbatim. The original wording —
// "present only under the FromDB casbin policy source; Postgres only" — reads
// as an if-and-only-if and is false in BOTH directions, because
// casbinauthz.MigrateCasbin is an explicit standalone call that is never
// auto-run (internal/authz/casbin/migrate.go documents exactly that):
//
//   - a deployment that runs MigrateCasbin and then wires FromStrings or
//     FromEnforcer HAS the table, though it never uses the FromDB source;
//   - a deployment that wires FromDB without ever running MigrateCasbin does
//     NOT have it (the authorizer fails against a missing relation instead).
//
// Presence is therefore conditional on the MIGRATION having been applied, not
// on which policy source is wired, and the published sentence must say so.
func TestCasbinNoteIsConditionedOnTheMigrationHavingRun(t *testing.T) {
	t.Parallel()

	note := atrest.MigrationSets["internal/authz/casbin/migrations"].Note

	assert.Contains(t, note, "casbinauthz.MigrateCasbin",
		"the note must condition presence on the migration call a consumer can actually make")
	assert.NotContains(t, note, "present only under the FromDB casbin policy source",
		"the FromDB-only wording is a false if-and-only-if: MigrateCasbin is never auto-run, "+
			"and a deployment that ran it keeps the table under any policy source")
}

// TestLoadSchemas_KeepsTheMySQLDeclaredColumnName pins the two halves of the
// MySQL normalization that must NOT be conflated: the map KEY is canonicalized
// so cross-dialect set operations work, while Column.Name keeps the name the
// dialect's migration actually declares. Losing the declared name is how the
// generated table came to publish "trigger" under a "mysql type" heading for a
// column MySQL declares as "trigger_".
func TestLoadSchemas_KeepsTheMySQLDeclaredColumnName(t *testing.T) {
	t.Parallel()

	root, err := atrest.ModuleRoot()
	require.NoError(t, err)
	schemas, err := atrest.LoadSchemas(root)
	require.NoError(t, err)

	key := atrest.ColumnKey{Table: "wrkflw_journal", Column: "trigger"}

	mysqlCol, ok := schemas["mysql"].Columns[key]
	require.True(t, ok, "the mysql column must be reachable under the CANONICAL key")
	assert.Equal(t, "trigger_", mysqlCol.Name,
		"the mysql column must keep the name its migration DECLARES")

	pgCol, ok := schemas["postgres"].Columns[key]
	require.True(t, ok)
	assert.Equal(t, "trigger", pgCol.Name, "postgres declares the canonical name already")
}

// TestInlinePrimaryKeySitesAreTheDeclaredSet closes an enumeration that rotted
// inside the delivery whose subject is enumerations rotting. The count of
// columns declaring PRIMARY KEY inline (on the column, not as a table-level
// clause) was written down twice and was wrong both times: the comment in
// schema.go said THREE, the design said FOUR, and the migrations
// declare FIVE. The fifth is wrkflw_human_task.task_id, inline in SQLite only —
// Postgres and MySQL spell that one as a table-level PRIMARY KEY (task_id).
//
// It pins the closed SET rather than a count, so a sixth site names itself in
// the failure instead of being reported as "want 5, got 6". What makes it fail:
// adding, removing or relocating any inline PRIMARY KEY in any of the four
// migration files.
//
// Method (stated because it is a test-local re-derivation, not the parser's):
// scan each migration file for CREATE TABLE headers, then for body lines whose
// FIRST token is an identifier — not a table-level clause keyword — that also
// carry PRIMARY KEY.
func TestInlinePrimaryKeySitesAreTheDeclaredSet(t *testing.T) {
	t.Parallel()

	root, err := atrest.ModuleRoot()
	require.NoError(t, err)

	var (
		createTable = regexp.MustCompile(`(?i)^\s*CREATE\s+TABLE\s+(?:IF\s+NOT\s+EXISTS\s+)?([A-Za-z_][A-Za-z0-9_]*)`)
		columnDecl  = regexp.MustCompile(`(?i)^\s*([A-Za-z_][A-Za-z0-9_]*)\s+\S`)
		primaryKey  = regexp.MustCompile(`(?i)\bPRIMARY\s+KEY\b`)
		clauseWords = map[string]bool{
			"PRIMARY": true, "UNIQUE": true, "FOREIGN": true, "CONSTRAINT": true,
			"CHECK": true, "KEY": true, "INDEX": true, "EXCLUDE": true, "CREATE": true,
		}
	)

	var got []string
	for dir := range atrest.MigrationSets {
		entries, err := os.ReadDir(filepath.Join(root, filepath.FromSlash(dir)))
		require.NoError(t, err)

		for _, entry := range entries {
			if !strings.EqualFold(filepath.Ext(entry.Name()), ".sql") {
				continue
			}
			content, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(dir), entry.Name()))
			require.NoError(t, err)

			table := ""
			for _, line := range strings.Split(string(content), "\n") {
				if m := createTable.FindStringSubmatch(line); m != nil {
					table = m[1]
					continue
				}
				if table == "" || !primaryKey.MatchString(line) {
					continue
				}
				m := columnDecl.FindStringSubmatch(line)
				if m == nil || clauseWords[strings.ToUpper(m[1])] {
					continue
				}
				got = append(got, dir+" "+table+"."+m[1])
			}
		}
	}
	sort.Strings(got)

	assert.Equal(t, []string{
		"internal/authz/casbin/migrations casbin_rule.id",
		"internal/persistence/store/migrations/mysql wrkflw_call_links.child_instance_id",
		"internal/persistence/store/migrations/mysql wrkflw_instances.instance_id",
		"internal/persistence/store/migrations/mysql wrkflw_outbox.id",
		"internal/persistence/store/migrations/postgres wrkflw_call_links.child_instance_id",
		"internal/persistence/store/migrations/postgres wrkflw_instances.instance_id",
		"internal/persistence/store/migrations/postgres wrkflw_outbox.id",
		"internal/persistence/store/migrations/sqlite wrkflw_call_links.child_instance_id",
		"internal/persistence/store/migrations/sqlite wrkflw_human_task.task_id",
		"internal/persistence/store/migrations/sqlite wrkflw_instances.instance_id",
		"internal/persistence/store/migrations/sqlite wrkflw_outbox.id",
	}, got, "the inline PRIMARY KEY sites are a closed set of FIVE distinct columns; "+
		"wrkflw_human_task.task_id is inline in SQLite only")
}
