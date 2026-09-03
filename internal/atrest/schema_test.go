package atrest_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kartaladev/wrkflw/internal/atrest"
)

func TestParseSQL_ColumnsTypesAndDownCutoff(t *testing.T) {
	t.Parallel()

	const src = `-- +goose Up
CREATE TABLE t_one (
    a TEXT PRIMARY KEY,
    b JSONB NOT NULL,
    -- a comment that is not a column
    c TIMESTAMPTZ
);

-- +goose Down
DROP TABLE IF EXISTS t_one;
CREATE TABLE t_ghost (x TEXT);
`

	got, err := atrest.ParseSQL("postgres", src)
	require.NoError(t, err)

	assert.Equal(t, "postgres", got.Dialect)
	assert.Len(t, got.Columns, 3, "three columns; the comment is not one")

	assert.Equal(t, "TEXT", got.Columns[atrest.ColumnKey{Table: "t_one", Column: "a"}].Type)
	assert.Equal(t, "JSONB", got.Columns[atrest.ColumnKey{Table: "t_one", Column: "b"}].Type)
	assert.Equal(t, "TIMESTAMPTZ", got.Columns[atrest.ColumnKey{Table: "t_one", Column: "c"}].Type)

	assert.NotContains(t, got.Columns, atrest.ColumnKey{Table: "t_ghost", Column: "x"},
		"everything after -- +goose Down is the rollback script and must not be parsed as schema")
}

func TestParseSQL_RealWorldTraps(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		src    string
		assert func(t *testing.T, s atrest.Schema, err error)
	}{
		{
			name: "trap 1: multi-line CREATE INDEX with multi-space ON",
			src: `-- +goose Up
CREATE TABLE t (a TEXT, b TEXT);
CREATE INDEX t_pending_idx ON t (a)
    WHERE b IN ('completed','failed');
CREATE INDEX idx_t_b   ON t (b);
`,
			assert: func(t *testing.T, s atrest.Schema, err error) {
				require.NoError(t, err)
				assert.Len(t, s.Columns, 2)
				assert.Contains(t, s.Columns[atrest.ColumnKey{Table: "t", Column: "a"}].Keys, "index")
				assert.Contains(t, s.Columns[atrest.ColumnKey{Table: "t", Column: "b"}].Keys, "index",
					"idx_t_b has three spaces before ON; a naive 'CREATE INDEX [^ ]* ON ' split loses it")
				assert.Contains(t, s.Columns[atrest.ColumnKey{Table: "t", Column: "b"}].Keys, "index-predicate",
					"a partial index's WHERE column is read by the planner")
			},
		},
		{
			name: "trap 2: MySQL inline INDEX clause is an index, not a column",
			src: `-- +goose Up
CREATE TABLE t (
    a VARCHAR(64) NOT NULL,
    b VARCHAR(64) NOT NULL,
    PRIMARY KEY (a),
    INDEX idx_t_b (b)
);
`,
			assert: func(t *testing.T, s atrest.Schema, err error) {
				require.NoError(t, err)
				assert.Len(t, s.Columns, 2, "INDEX idx_t_b (b) is not a third column")
				assert.Contains(t, s.Columns[atrest.ColumnKey{Table: "t", Column: "a"}].Keys, "PK")
				assert.Contains(t, s.Columns[atrest.ColumnKey{Table: "t", Column: "b"}].Keys, "index")
			},
		},
		{
			name: "trap 3: MySQL table-level CONSTRAINT FOREIGN KEY is not a column",
			src: `-- +goose Up
CREATE TABLE t (
    a VARCHAR(64) NOT NULL,
    b BIGINT NOT NULL,
    PRIMARY KEY (a, b),
    CONSTRAINT fk_t_other FOREIGN KEY (a) REFERENCES other(a)
);
`,
			assert: func(t *testing.T, s atrest.Schema, err error) {
				require.NoError(t, err)
				assert.Len(t, s.Columns, 2, "the CONSTRAINT clause is not a third column")
				assert.Contains(t, s.Columns[atrest.ColumnKey{Table: "t", Column: "a"}].Keys, "PK")
				assert.NotContains(t, s.Columns[atrest.ColumnKey{Table: "t", Column: "a"}].Keys, "FK",
					"foreign keys are deliberately OUT of the keyed derivation")
			},
		},
		{
			name: "inline UNIQUE is the ONLY key on wrkflw_outbox.dedup_key",
			src: `-- +goose Up
CREATE TABLE t (
    a TEXT NOT NULL,
    dedup_key TEXT NOT NULL UNIQUE
);
`,
			assert: func(t *testing.T, s atrest.Schema, err error) {
				require.NoError(t, err)
				assert.Contains(t, s.Columns[atrest.ColumnKey{Table: "t", Column: "dedup_key"}].Keys, "UNIQUE",
					"wrkflw_outbox.dedup_key is keyed SOLELY by an inline UNIQUE in all three "+
						"dialects; miss this shape and the 29/28/28 keyed census is wrong by one")
			},
		},
		{
			name: "additional trap (found by probing the real migrations, not in the brief): " +
				"inline single-column PRIMARY KEY is a PK, not a plain column",
			src: `-- +goose Up
CREATE TABLE t (
    id BIGINT PRIMARY KEY,
    b TEXT
);
`,
			assert: func(t *testing.T, s atrest.Schema, err error) {
				require.NoError(t, err)
				assert.Contains(t, s.Columns[atrest.ColumnKey{Table: "t", Column: "id"}].Keys, "PK",
					"wrkflw_instances.instance_id, wrkflw_outbox.id, and casbin_rule.id are all PK "+
						"via this inline column-level shape, not a table-level PRIMARY KEY (...) "+
						"clause; probing the real migration files (not part of the brief's fixtures) "+
						"found this shape produced zero Keys entries before the fix")
			},
		},
		{
			name: "regression: a multi-byte rune in a WHERE predicate must not merge into a token " +
				"(gosec G115, found by golangci-lint, not the brief's fixtures)",
			src: "-- +goose Up\n" +
				"CREATE TABLE t (a TEXT, b TEXT);\n" +
				"CREATE INDEX idx ON t (b) WHERE aš > 0;\n",
			assert: func(t *testing.T, s atrest.Schema, err error) {
				require.NoError(t, err)
				assert.Contains(t, s.Columns[atrest.ColumnKey{Table: "t", Column: "a"}].Keys, "index-predicate",
					"a byte(rune) truncation of U+0161 aliases to ASCII 'a' (0x61); a tokenizer that "+
						"truncates runes to bytes would wrongly keep column a merged into one 'aš' "+
						"token and never recognize the real reference to a")
			},
		},
		{
			name: "depth-aware body split: a DEFAULT containing commas is one column",
			src: `-- +goose Up
CREATE TABLE t (
    a TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ', 'now')),
    b TEXT
);
`,
			assert: func(t *testing.T, s atrest.Schema, err error) {
				require.NoError(t, err)
				assert.Len(t, s.Columns, 2, "the comma inside strftime(...) must not split a column")
			},
		},
		{
			// A table-level CONSTRAINT spelling of UNIQUE is how
			// production DDL usually declares it; before the fix this derived NO key
			// at all (recognised by bodyClauseSkipPrefixes, unhandled by
			// applyTableLevelKeyClause's switch, silently dropped).
			name: "CONSTRAINT ... UNIQUE (...) re-dispatches to the UNIQUE key",
			src: `-- +goose Up
CREATE TABLE t (
    e TEXT NOT NULL,
    f TEXT NOT NULL,
    CONSTRAINT uq_ef UNIQUE (e, f)
);
`,
			assert: func(t *testing.T, s atrest.Schema, err error) {
				require.NoError(t, err)
				assert.Contains(t, s.Columns[atrest.ColumnKey{Table: "t", Column: "e"}].Keys, "UNIQUE")
				assert.Contains(t, s.Columns[atrest.ColumnKey{Table: "t", Column: "f"}].Keys, "UNIQUE")
			},
		},
		{
			// A table-level CONSTRAINT spelling of PRIMARY KEY, same gap.
			name: "CONSTRAINT ... PRIMARY KEY (...) re-dispatches to the PK key",
			src: `-- +goose Up
CREATE TABLE t (
    g TEXT NOT NULL,
    CONSTRAINT pk_g PRIMARY KEY (g)
);
`,
			assert: func(t *testing.T, s atrest.Schema, err error) {
				require.NoError(t, err)
				assert.Contains(t, s.Columns[atrest.ColumnKey{Table: "t", Column: "g"}].Keys, "PK")
			},
		},
		{
			// A bare table-level KEY name (col) (MySQL's non-PRIMARY secondary-index
			// shorthand) must be treated the same as INDEX name (col).
			name: "bare KEY name (col) is treated as INDEX",
			src: `-- +goose Up
CREATE TABLE t (
    e TEXT NOT NULL,
    KEY idx_e (e)
);
`,
			assert: func(t *testing.T, s atrest.Schema, err error) {
				require.NoError(t, err)
				assert.Contains(t, s.Columns[atrest.ColumnKey{Table: "t", Column: "e"}].Keys, "index")
			},
		},
		{
			// FOREIGN KEY spelled via a named CONSTRAINT must still be
			// deliberately excluded, not treated as unrecognised.
			name: "CONSTRAINT ... FOREIGN KEY (...) stays excluded, not an error",
			src: `-- +goose Up
CREATE TABLE t (
    a TEXT NOT NULL,
    CONSTRAINT fk_t_other FOREIGN KEY (a) REFERENCES other(a)
);
`,
			assert: func(t *testing.T, s atrest.Schema, err error) {
				require.NoError(t, err)
				assert.Empty(t, s.Columns[atrest.ColumnKey{Table: "t", Column: "a"}].Keys,
					"FOREIGN is deliberately excluded from key membership")
			},
		},
		{
			// A CONSTRAINT clause this parser cannot decompose must fail
			// closed rather than silently derive no key — the
			// asymmetry the finding names: an UNRECOGNISED clause is already
			// caught by the completeness guard, but a RECOGNISED-but-unhandled
			// one (CONSTRAINT/KEY) was caught by nothing before this fix.
			// RE-FIXTURED after /code-review: a named
			// CONSTRAINT whose shape the parser cannot classify is still a parse
			// error. The original fixture used CHECK, which was a defect rather
			// than a feature — the UNNAMED "CHECK (a > 0)" silently injected a
			// phantom column named CHECK while the NAMED form hard-failed the
			// whole load. Both forms are now recognised and keyless (see
			// TestParseSQL_BodyClauseClassification), so the fixture had to become
			// a genuinely unknown shape or it could no longer fail for the reason
			// it claims.
			name: "an undecomposable CONSTRAINT clause is a parse error, not a silent no-op",
			src: `-- +goose Up
CREATE TABLE t (
    a INT NOT NULL,
    b INT NOT NULL,
    CONSTRAINT p_a PERIOD FOR SYSTEM_TIME (a, b)
);
`,
			assert: func(t *testing.T, _ atrest.Schema, err error) {
				require.Error(t, err)
			},
		},
		{
			// applyCreateIndex must fail closed on a malformed
			// CREATE INDEX statement instead of returning silently.
			name: "CREATE INDEX with no ON clause is a parse error, not a silent no-op",
			src: `-- +goose Up
CREATE TABLE t (a TEXT);
CREATE INDEX idx_bad;
`,
			assert: func(t *testing.T, _ atrest.Schema, err error) {
				require.Error(t, err)
			},
		},
		{
			name: "CREATE INDEX with no column list is a parse error, not a silent no-op",
			src: `-- +goose Up
CREATE TABLE t (a TEXT);
CREATE INDEX idx_bad ON t;
`,
			assert: func(t *testing.T, _ atrest.Schema, err error) {
				require.Error(t, err)
			},
		},
		{
			// fields[1] alone truncates a multi-word type at its first space.
			// ⚠ This was filed as "latent, verified absent from the corpus" and
			// that premise was FALSE. The already-published document carried
			// `BIGINT` for wrkflw_outbox.id on MySQL, where the DDL declares
			// `BIGINT AUTO_INCREMENT` — a live truncation in the shipped
			// document, found by diffing the regenerated file against a backup.
			// This is a fix, not only a regression guard.
			name: "multi-word column types are captured in full, not truncated at the first space",
			src: `-- +goose Up
CREATE TABLE t (
    a DOUBLE PRECISION,
    b CHARACTER VARYING(255) NOT NULL,
    c TIMESTAMP WITH TIME ZONE,
    d BIGINT UNSIGNED DEFAULT 0
);
`,
			assert: func(t *testing.T, s atrest.Schema, err error) {
				require.NoError(t, err)
				assert.Equal(t, "DOUBLE PRECISION", s.Columns[atrest.ColumnKey{Table: "t", Column: "a"}].Type)
				assert.Equal(t, "CHARACTER VARYING(255)", s.Columns[atrest.ColumnKey{Table: "t", Column: "b"}].Type,
					"CHARACTER and CHARACTER VARYING are different storage types")
				assert.Equal(t, "TIMESTAMP WITH TIME ZONE", s.Columns[atrest.ColumnKey{Table: "t", Column: "c"}].Type)
				assert.Equal(t, "BIGINT UNSIGNED", s.Columns[atrest.ColumnKey{Table: "t", Column: "d"}].Type)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := atrest.ParseSQL("test", tt.src)
			tt.assert(t, got, err)
		})
	}
}

func TestSchema_Tables(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		schema atrest.Schema
		assert func(t *testing.T, got []string)
	}{
		{
			name:   "empty schema has no tables",
			schema: atrest.Schema{Columns: map[atrest.ColumnKey]atrest.Column{}},
			assert: func(t *testing.T, got []string) {
				assert.Empty(t, got)
			},
		},
		{
			name: "table names are sorted and de-duplicated across columns",
			schema: atrest.Schema{
				Columns: map[atrest.ColumnKey]atrest.Column{
					{Table: "z_table", Column: "a"}: {Table: "z_table", Name: "a"},
					{Table: "a_table", Column: "a"}: {Table: "a_table", Name: "a"},
					{Table: "a_table", Column: "b"}: {Table: "a_table", Name: "b"},
				},
			},
			assert: func(t *testing.T, got []string) {
				assert.Equal(t, []string{"a_table", "z_table"}, got,
					"a_table has two columns but must appear once, and before z_table")
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := tt.schema.Tables()
			tt.assert(t, got)
		})
	}
}

// TestKeyedLowerBound_Postgres pins the postgres keyed census: 29 of the
// 87 columns are keyed, casbin_rule INCLUDED.
// The byClass map is the load-bearing assertion — a bare 29 would still
// pass if the derivation keyed the wrong 29 columns. Earlier drafts of
// this test skipped casbin_rule with `if k.Table == "casbin_rule" {
// continue }` and called the result an assertion that "cannot rot"; it
// could not rot because it skipped its own counterexample —
// casbin_rule.ptype is class policy and carries casbin_rule_ptype_idx,
// which falsifies a four-entry byClass map. Every table, every dialect.
func TestKeyedLowerBound_Postgres(t *testing.T) {
	t.Parallel()

	root, err := atrest.ModuleRoot()
	require.NoError(t, err)
	schemas, err := atrest.LoadSchemas(root)
	require.NoError(t, err)

	byClass := map[atrest.Class]int{}
	keyed := 0
	for k, col := range schemas["postgres"].Columns {
		if len(col.Keys) > 0 {
			keyed++
			byClass[atrest.Classification[k]]++
		}
	}

	assert.Equal(t, 29, keyed, "29 of the 87 postgres columns are keyed, casbin_rule INCLUDED")
	assert.Equal(t, map[atrest.Class]int{
		atrest.ClassReference: 15,
		atrest.ClassScalar:    8,
		atrest.ClassTimestamp: 4,
		atrest.ClassActor:     1,
		atrest.ClassPolicy:    1, // casbin_rule.ptype — the counterexample the old test skipped
	}, byClass, "policy-keyed is ONE, not zero; do not restate the withdrawn claim")
}

// TestKeyedCountPerDialect pins keyed for all three dialects. keyed is
// dialect-DEPENDENT even though class is
// not — never assert only one dialect as "the" number.
func TestKeyedCountPerDialect(t *testing.T) {
	t.Parallel()

	root, err := atrest.ModuleRoot()
	require.NoError(t, err)
	schemas, err := atrest.LoadSchemas(root)
	require.NoError(t, err)

	count := func(d string) int {
		n := 0
		for _, col := range schemas[d].Columns {
			if len(col.Keys) > 0 {
				n++
			}
		}
		return n
	}
	assert.Equal(t, 29, count("postgres"), "87 columns incl. casbin_rule")
	assert.Equal(t, 28, count("mysql"), "79 columns; partial-index predicates folded into keys")
	assert.Equal(t, 28, count("sqlite"), "79 columns; no partial indexes")
}

// TestKeyedIsDialectDependent pins the specific case that makes keyed
// dialect-dependent: postgres has 5 partial indexes (WHERE predicate
// columns get "index-predicate"), while mysql and sqlite have none and
// instead fold the predicate's column into the ordinary key set.
func TestKeyedIsDialectDependent(t *testing.T) {
	t.Parallel()

	root, err := atrest.ModuleRoot()
	require.NoError(t, err)
	schemas, err := atrest.LoadSchemas(root)
	require.NoError(t, err)

	// wrkflw_outbox.status is an index PREDICATE column on postgres (partial index)
	// and is folded into the KEY on mysql/sqlite, which have no partial indexes.
	pg := schemas["postgres"].Columns[atrest.ColumnKey{Table: "wrkflw_outbox", Column: "status"}]
	assert.Contains(t, pg.Keys, "index-predicate")

	my := schemas["mysql"].Columns[atrest.ColumnKey{Table: "wrkflw_outbox", Column: "status"}]
	assert.NotContains(t, my.Keys, "index-predicate",
		"mysql has no partial indexes; it folds the predicate into the key instead")
}

// TestParseSQL_StringLiteralsAndBodyBounds covers the two ways the reader used
// to mis-delimit a CREATE TABLE statement. Both were found by /code-review and
// reproduced before the fix:
//
//   - stripLineComments cut at the first "--" ANYWHERE in a line, including
//     inside a string literal, so `a TEXT DEFAULT '--x', b TEXT,` lost the rest
//     of its line and every column declared on it;
//   - parseCreateTable delimited the body with strings.LastIndex(stmt, ")")
//     instead of the matchingParen helper the same file already owns, so
//     `CREATE TABLE t (a TEXT, b TEXT) WITH (fillfactor=70);` published
//     `TEXT) WITH (fillfactor=70` verbatim as b's storage type.
func TestParseSQL_StringLiteralsAndBodyBounds(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		src    string
		assert func(t *testing.T, s atrest.Schema, err error)
	}{
		{
			name: "a -- inside a string literal is not a comment",
			src: `-- +goose Up
CREATE TABLE t (
    a TEXT NOT NULL DEFAULT '--x', b TEXT,
    c TEXT
);
`,
			assert: func(t *testing.T, s atrest.Schema, err error) {
				require.NoError(t, err)
				assert.Len(t, s.Columns, 3,
					"b shares its line with a literal containing --; cutting there deletes it silently")
				assert.Equal(t, "TEXT", s.Columns[atrest.ColumnKey{Table: "t", Column: "a"}].Type)
				assert.Equal(t, "TEXT", s.Columns[atrest.ColumnKey{Table: "t", Column: "b"}].Type)
			},
		},
		{
			name: "a real trailing -- comment is still stripped",
			src: `-- +goose Up
CREATE TABLE t (
    a TEXT, -- the first column
    b TEXT
);
`,
			assert: func(t *testing.T, s atrest.Schema, err error) {
				require.NoError(t, err)
				assert.Len(t, s.Columns, 2)
				assert.Equal(t, "TEXT", s.Columns[atrest.ColumnKey{Table: "t", Column: "a"}].Type)
			},
		},
		{
			name: "the body ends at the MATCHING paren, and trailing table options fail closed",
			src: `-- +goose Up
CREATE TABLE t (a TEXT, b TEXT) WITH (fillfactor=70);
`,
			assert: func(t *testing.T, s atrest.Schema, err error) {
				// Failing closed rather than ignoring the remainder is
				// deliberate: Postgres's INHERITS (parent) is a trailing clause
				// that ADDS COLUMNS, so a silently-ignored remainder can under-report
				// a security document's column census. No migration in this module
				// carries one today, so the strictness costs nothing now.
				require.Error(t, err)
				assert.Contains(t, err.Error(), "fillfactor",
					"the error must name the remainder it could not account for")
				assert.NotContains(t, s.Columns, atrest.ColumnKey{Table: "t", Column: "b"})
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, err := atrest.ParseSQL("postgres", tc.src)
			tc.assert(t, got, err)
		})
	}
}

// TestParseSQL_BodyClauseClassification covers the CREATE TABLE body's one
// real decision — is this clause a COLUMN DECLARATION or a table-level
// constraint? — which used to be spread over three disagreeing matchers.
// /code-review reproduced all four resulting defects:
//
//   - an exact first-word match dropped a column literally named `key`,
//     `index` or `unique` (symmetrically across dialects, so the completeness
//     guard stayed green);
//   - the same match turned `UNIQUE(g)` — no space before the paren — into a
//     PHANTOM column named "UNIQUE(g)";
//   - `CONSTRAINT pk_g PRIMARY KEY(g)` HARD-FAILED the whole schema load while
//     the unnamed `PRIMARY KEY(g)` parsed fine;
//   - `CHECK` and `EXCLUDE` were missing from the skip list, so an unnamed
//     CHECK injected a phantom column while the named form hard-errored.
//
// The switch on the leading keyword is now the single enumeration, and its
// default branch means "column declaration" — which is also why the old
// "unrecognised table-level clause" error (0 coverage hits, unreachable by
// construction) is gone.
func TestParseSQL_BodyClauseClassification(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		src    string
		assert func(t *testing.T, s atrest.Schema, err error)
	}{
		{
			name: "columns literally named key/index/exclude survive",
			src: `-- +goose Up
CREATE TABLE t (
    a TEXT,
    key VARCHAR(255) NOT NULL,
    index INT DEFAULT 0,
    exclude TEXT
);
`,
			assert: func(t *testing.T, s atrest.Schema, err error) {
				require.NoError(t, err)
				assert.Len(t, s.Columns, 4, "a legal unquoted identifier must not be eaten as a clause keyword")
				assert.Equal(t, "VARCHAR(255)", s.Columns[atrest.ColumnKey{Table: "t", Column: "key"}].Type)
				assert.Equal(t, "INT", s.Columns[atrest.ColumnKey{Table: "t", Column: "index"}].Type)
			},
		},
		{
			name: "a table-level clause glued to its paren is still a clause, not a column",
			src: `-- +goose Up
CREATE TABLE t (
    g TEXT,
    h TEXT,
    UNIQUE(g),
    KEY idx_t_h(h)
);
`,
			assert: func(t *testing.T, s atrest.Schema, err error) {
				require.NoError(t, err)
				assert.Len(t, s.Columns, 2, "UNIQUE(g) and KEY idx_t_h(h) are clauses, not phantom columns")
				assert.Contains(t, s.Columns[atrest.ColumnKey{Table: "t", Column: "g"}].Keys, "UNIQUE")
				assert.Contains(t, s.Columns[atrest.ColumnKey{Table: "t", Column: "h"}].Keys, "index")
			},
		},
		{
			name: "named and unnamed PRIMARY KEY agree, with or without a space before the paren",
			src: `-- +goose Up
CREATE TABLE t (g TEXT, CONSTRAINT pk_g PRIMARY KEY(g));
CREATE TABLE u (g TEXT, PRIMARY KEY(g));
CREATE TABLE v (g TEXT, CONSTRAINT pk_v PRIMARY KEY (g));
`,
			assert: func(t *testing.T, s atrest.Schema, err error) {
				require.NoError(t, err)
				for _, table := range []string{"t", "u", "v"} {
					assert.Contains(t, s.Columns[atrest.ColumnKey{Table: table, Column: "g"}].Keys, "PK",
						"%s: all three spellings of a single-column primary key must agree", table)
				}
			},
		},
		{
			name: "CHECK and EXCLUDE clauses, named or unnamed, are recognised and derive no key",
			src: `-- +goose Up
CREATE TABLE t (
    a INT,
    b INT,
    CHECK (a > 0),
    CONSTRAINT c_b CHECK (b > 0),
    EXCLUDE USING gist (a WITH =)
);
`,
			assert: func(t *testing.T, s atrest.Schema, err error) {
				require.NoError(t, err)
				assert.Len(t, s.Columns, 2, "no phantom CHECK/EXCLUDE column")
				assert.Empty(t, s.Columns[atrest.ColumnKey{Table: "t", Column: "a"}].Keys,
					"a CHECK/EXCLUDE clause is not a key")
			},
		},
		{
			name: "a table-level constraint naming an unknown column fails closed",
			src: `-- +goose Up
CREATE TABLE t (a TEXT, PRIMARY KEY (zzz));
`,
			assert: func(t *testing.T, _ atrest.Schema, err error) {
				require.Error(t, err)
				assert.Contains(t, err.Error(), "zzz",
					"the error must name the column it could not resolve")
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, err := atrest.ParseSQL("postgres", tc.src)
			tc.assert(t, got, err)
		})
	}
}

// TestParseSQL_CreateIndexShapes covers the four CREATE INDEX defects
// /code-review found and reproduced. Each one silently UNDER-derives `keyed`,
// which the generated document publishes as a lower bound and which the "no
// actor-classed column is indexed" sentence used to convert into a false
// safety claim:
//
//   - the table name was taken as the whole span between ON and the first "(",
//     so "ON t USING gin (col)" yielded the table "t USING gin" and the index
//     vanished — GIN/GiST on JSONB being the normal way to index this schema's
//     eligibility/candidates/vars/snapshot/payload columns — and "ON public.t"
//     lost the index the same way;
//   - byte offsets were computed on strings.ToUpper(stmt) and then applied to
//     the ORIGINAL stmt; ToUpper is not length-preserving in UTF-8 (U+0131 'ı'
//     is two bytes and upper-cases to the one-byte 'I'), so one non-ASCII rune
//     before ON or WHERE skewed every later slice and lost the index;
//   - CREATE UNIQUE INDEX recorded a plain "index", dropping the uniqueness
//     that tells a consumer deterministic ciphertext leaks duplicate detection;
//   - the partial-index predicate scan did not exclude string-literal contents,
//     so WHERE status = 'error' annotated a column named "error" as
//     index-predicate. That one OVER-claims, and postgres/0001_init.sql already
//     carries `WHERE status IN ('completed','failed')` on tables that also
//     declare error/output/outcome.
func TestParseSQL_CreateIndexShapes(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		src    string
		assert func(t *testing.T, s atrest.Schema, err error)
	}{
		{
			name: "USING <method> is recognised and the index still lands",
			src: `-- +goose Up
CREATE TABLE t (a TEXT, col JSONB);
CREATE INDEX i ON t USING gin (col);
`,
			assert: func(t *testing.T, s atrest.Schema, err error) {
				require.NoError(t, err)
				assert.Contains(t, s.Columns[atrest.ColumnKey{Table: "t", Column: "col"}].Keys, "index")
			},
		},
		{
			name: "a schema-qualified table resolves to the table",
			src: `-- +goose Up
CREATE TABLE t (a TEXT);
CREATE INDEX i ON public.t (a);
`,
			assert: func(t *testing.T, s atrest.Schema, err error) {
				require.NoError(t, err)
				assert.Contains(t, s.Columns[atrest.ColumnKey{Table: "t", Column: "a"}].Keys, "index")
			},
		},
		{
			name: "an unrecognised remainder between the table and the column list fails closed",
			src: `-- +goose Up
CREATE TABLE t (a TEXT);
CREATE INDEX i ON t NULLS NOT DISTINCT (a);
`,
			assert: func(t *testing.T, _ atrest.Schema, err error) {
				require.Error(t, err)
				assert.Contains(t, err.Error(), "NULLS")
			},
		},
		{
			name: "a non-ASCII rune before ON does not skew the offsets",
			src: `-- +goose Up
CREATE TABLE t (a TEXT, b TEXT);
CREATE INDEX "ıx" ON t (a) WHERE b IS NOT NULL;
`,
			assert: func(t *testing.T, s atrest.Schema, err error) {
				require.NoError(t, err)
				assert.Contains(t, s.Columns[atrest.ColumnKey{Table: "t", Column: "a"}].Keys, "index")
				assert.Contains(t, s.Columns[atrest.ColumnKey{Table: "t", Column: "b"}].Keys, "index-predicate")
			},
		},
		{
			name: "CREATE UNIQUE INDEX records its uniqueness",
			src: `-- +goose Up
CREATE TABLE t (a TEXT);
CREATE UNIQUE INDEX u ON t (a);
`,
			assert: func(t *testing.T, s atrest.Schema, err error) {
				require.NoError(t, err)
				keys := s.Columns[atrest.ColumnKey{Table: "t", Column: "a"}].Keys
				assert.Contains(t, keys, "UNIQUE",
					"uniqueness is what tells a consumer deterministic ciphertext leaks duplicate detection")
				assert.Contains(t, keys, "index")
			},
		},
		{
			name: "a string literal in a partial-index predicate is not a column reference",
			src: `-- +goose Up
CREATE TABLE t (a TEXT, status TEXT, error TEXT);
CREATE INDEX i ON t (a) WHERE status = 'error';
`,
			assert: func(t *testing.T, s atrest.Schema, err error) {
				require.NoError(t, err)
				assert.Contains(t, s.Columns[atrest.ColumnKey{Table: "t", Column: "status"}].Keys, "index-predicate")
				assert.Empty(t, s.Columns[atrest.ColumnKey{Table: "t", Column: "error"}].Keys,
					"'error' is the literal the predicate compares against, not the column named error")
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, err := atrest.ParseSQL("postgres", tc.src)
			tc.assert(t, got, err)
		})
	}
}
