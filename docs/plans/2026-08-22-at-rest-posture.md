# §AT-REST / ADR-0187 Implementation Plan

## ▶ Progress — `/code-review` findings FIXED; `/security-review` still owed

**Branch:** `design/at-rest-posture` (do not quote a SHA here — the bundle is amended).
**State:** all 8 tasks complete. The owner-invoked **`/code-review` ran and its findings are all
fixed** in one wave, on a follow-up commit (the base feature commit was NOT amended — the owner
squashes). Report with per-finding evidence:
`.superpowers/sdd/2026-08-22-at-rest-posture/code-review-fix-report.md`.

**Verification after the fix wave (all by exit code, Docker up):**
`go test -race -coverprofile=cover.out ./...` **EXIT=0** · `go test ./...` **EXIT=0** ·
`golangci-lint run ./...` **EXIT=0, 0 issues** ·
`internal/atrest` **92.6%**, `internal/persistence/store` **88.1%** — both clear the 85% floor ·
repo-wide filtered **75.8%** (was 75.6% before this wave), still below the floor and still
**measured pre-existing**.

**What shipped:** `internal/atrest/{schema,discover,classification,render}.go` + tests;
`internal/persistence/store/atrest_crosscheck_test.go` (Docker-gated, now key-level);
`scripts/gen-at-rest.sh`; and the generated `## Data at rest` section of `SECURITY.md` — 87 rows,
per-dialect type and `keyed`.

**⛔ REMAINING BEFORE MERGE — owner-invoked only:** `/security-review`.

### What `/code-review` corrected (2026-08-23) — headline items

1. ⛔ **"Authorization policy is durable at rest in TWO places" was FALSE — there is a third.**
   `wrkflw_definitions.definition` holds the marshalled definition, and every node's
   `eligible_roles`/`eligible_privileges`/`eligible_expr` are inside it. The sentence was restated in
   **nine** places across this bundle (the review found six). The column keeps class `freeform`; the
   locations are now a derived, two-way machine-checked enumeration (`atrest.PolicyAtRestLocations`).
2. ⛔ **The published table asserted a MySQL column name that does not exist** — D2b's canonicalization
   reached the rendered cell, so a DBA reading "mysql type" for `wrkflw_journal.trigger` would target
   a name MySQL rejects. The cell now discloses the DECLARED name; the key set stays canonical.
3. ⛔ **A universal negative derived from a lower bound** ("no `actor`-classed column is indexed in
   any dialect") turned every silent under-derivation in the parser into a false safety claim.
4. ⛔ **The casbin availability note was a false if-and-only-if** — presence depends on
   `casbinauthz.MigrateCasbin` having run, not on the `FromDB` source.
5. ⛔ **Nine DDL-reader defects**, all executed, seven latent until a migration changes; the
   "unrecognised table-level clause" fail-closed error was UNREACHABLE. See ADR-0187 decision 7's
   fold note.
6. ⛔ **`scripts/gen-at-rest.sh` reproduced Common Pitfall #5** — a rename of `TestSecurityMdInSync`
   would have made it print "regenerated and verified" having done neither.
7. ⛔ **The live cross-check validated ZERO of the UNIQUE/index/index-predicate facts** the `keyed`
   column publishes. Now key-level in all three dialects and for `casbin_rule`. Ablation: with the
   inline-`UNIQUE` derivation disabled, the OLD cross-check still PASSES and the new one fails.

### What implementation corrected in the design (rule #11)

1. **A FOURTH parser trap.** The bundle enumerated three; inline single-column `PRIMARY KEY` is a
   fourth, occurring 4× in the corpus, and two of those (`casbin_rule.id`, `wrkflw_outbox.id`) sit
   inside the 29-of-87 keyed count decision 8 publishes. Folded into ADR decision 7 + evidence E16.
2. **The FK exclusion was cited but never recorded.** Shipped code cited "ADR-0187 D8" for it while
   decision 8 never said it. Now stated there.
3. **⭐ C1 — the drift guard could not see a whole category of claim.** `Render` retyped
   `casbin_rule.{ptype,v0..v5}` are class `policy` as prose; since `TestSecurityMdInSync` compares
   `SECURITY.md` against `Render`'s OUTPUT, a sentence inside `Render` was compared only against
   itself. A reciprocal class swap left the entire suite green while the document contradicted its
   own table. Now derived, with an error sentinel when the derived set is not exactly those seven.
4. **⭐ I3 was filed as "latent" and was LIVE.** The shipped `SECURITY.md` published `BIGINT` for
   `wrkflw_outbox.id` on MySQL where the DDL declares `BIGINT AUTO_INCREMENT` — a false type already
   in the security document. Found by diffing the regenerated file against a backup.
5. **The live cross-check compared bare column names**, blind to table identity and every key. Now
   compares `(table, column, isPK)`.
6. **Three recognised-but-unhandled DDL clauses derived no key silently** (`CONSTRAINT … UNIQUE`,
   `CONSTRAINT … PRIMARY KEY`, `KEY name (col)`). Now re-dispatched; undecomposable ones fail closed.

### Residuals — real, parked, and stated in the artifact where they affect a reader

- ~~**`keyed`'s UNIQUE/index facts have NO live cross-check** — only PK is verified against a real
  database…~~ ✅ **CLOSED by `/code-review` (2026-08-23).** `TestAtRestKeysMatchLiveIntrospection_*`
  compares the whole `Keys` set per `(table, column)` against live Postgres, MySQL and SQLite, and
  against `casbin_rule`. `colFacts` and the `introspect*` helpers are still untouched — the new
  queries are siblings. Ablation: with the inline-`UNIQUE` derivation disabled the OLD column-level
  cross-check still passes and the new key check fails; same result for the partial-index predicate
  derivation on Postgres.
- ~~**Three more published sentences are retyped-not-derived**, the same class as C1: the
  `RemovePolicy` `WHERE` clause, "durable at rest in **two** places", "one of **six** classes". All
  three verified TRUE today; none guarded.~~ ⚠⚠ **CLOSED and REFUTED by `/code-review` (2026-08-23).**
  The residual said all three were "verified TRUE today". **"Durable at rest in two places" was
  FALSE when that line was written** — `wrkflw_definitions.definition` is a third location — which is
  what parking a retyped sentence as "true today, unguarded" costs. All three are now derived: the
  locations from `atrest.PolicyAtRestLocations` (checked in both directions), the class count from
  `len(classOrder)`, the `RemovePolicy` column list from the classification (C1's original fix).
- **A `CREATE INDEX` naming a table declared in a DIFFERENT migration file derives no key, silently**
  — latent today (each of the 4 migration dirs holds exactly 1 file), live the moment a `0002_*.sql`
  lands. The published caveat now says this rather than claiming the parser fails the build.
- `AUTO_INCREMENT` is published as part of the MySQL type. It is an attribute, not a type; kept
  deliberately because the storage shape is security-relevant, at the cost of dialect asymmetry.


> **For agentic workers:** REQUIRED SUB-SKILL: `superpowers:subagent-driven-development`.
> Steps use checkbox (`- [ ]`) syntax for tracking.

> ## ⚠⚠ AUDIT ROUND 1 FOLDED 2026-08-22 — read `docs/plans/sweep-evidence/audit-0187-adjudication.md` first
>
> Round 1: 64 findings, 17 Critical. Round 2 (scoped: interaction + counting): 34 more, 11 Critical.
> **Tasks 1, 2, 4, 5, 6, 7 and 8 all changed** — Task 3 did not. The four that change what you
> build: the parser must **fail closed** on unrecognised statements (new D11); MySQL's journal column
> is **`trigger_`** and must be normalized before any set operation (new D2b) or nothing goes green;
> the dialect-invariance guard was **vacuous by construction** and is replaced by a key-set invariant
> that **fails today**; and the `keyed` derivation must cover **casbin_rule** — the old exclusion is
> what made this delivery publish a false safety claim.

**Goal:** Ship a generated, machine-checked statement of what `wrkflw` stores in the clear, so the
enumeration that has rotted four times cannot rot a fifth.

**Architecture:** A new module-level `internal/atrest` package parses every **discovered**
migration file into a per-dialect schema, joins it against a hand-stated classification of each
`table.column`'s logical role, and renders a block into `SECURITY.md`. Guards fail on an
unclassified column, a stale classification entry, a `SECURITY.md` that disagrees with the
generator, a class that differs across dialects, and — Docker-gated — a parse that disagrees with
live database introspection.

**Tech Stack:** Go 1.25 · `stretchr/testify` · the existing `dbtest` helpers · no new dependencies.

**Spec:** `docs/specs/2026-08-22-at-rest-posture.md`
**ADR:** `docs/adr/0187-at-rest-classification-is-machine-checked.md`
**Evidence:** `docs/specs/2026-08-22-adr-0187-measurements.md` (E1–E15 — every number below traces
to one of these; do not restate a number without re-deriving it)

## Global Constraints

- **Go 1.25.** One `go.mod` at the repo root. No new third-party dependency.
- **TDD strict.** No production code before an observed failing test. A `Write` of an impl file
  with no `go test` Bash call between it and its test file is a process failure, not a shortcut.
- **This delivery is ENTIRELY SERIAL.** Tasks 1–6 all touch `internal/atrest`; task 7 touches
  `internal/persistence/store`. Per CLAUDE.md rule #11 agents fan out **by Go package**, and
  concurrent agents inside one package break each other's `go test` compile. **Dispatch one agent
  at a time and review its diff before the next.**
- **Black-box tests** (`package atrest_test`), per Golang rule #5.
- **Table tests use the project `table-test` skill's `assert` closure form**, not `want`/`wantErr`
  fields. Use `t.Context()`, never `context.Background()`.
- **Coverage floor 85 %** on `internal/atrest`, measured by `scripts/coverage.sh`. It is a floor:
  the parser's error branches are hot paths for this package and are covered first.
- ⚠ **`clockwork.NewFakeClock()` seeds from wall time** — irrelevant here (no clock), noted so it is
  not introduced.
- ⚠ **Restore every mutation from a `cp` backup, never `git checkout <path>`** — that restores from
  the index and destroys uncommitted work.
- ⚠ **A mutation ablation must NOT run in a shared working tree.** Task 4 mutates a migration file;
  give that agent a `git worktree` and say so in its brief.

---

## File structure

| file | responsibility |
|---|---|
| `internal/atrest/schema.go` | DDL reader: statements → tables, columns, physical types, key membership |
| `internal/atrest/discover.go` | glob `**/migrations/*.sql`; the migration-set **declaration** and its two-way check |
| `internal/atrest/classification.go` | the 87-row `table.column → Class` judgement, and `Class` itself |
| `internal/atrest/render.go` | renders the `SECURITY.md` block from schema + classification |
| `internal/atrest/schema_test.go` | parser tests incl. the three traps |
| `internal/atrest/discover_test.go` | discovery + declaration guards |
| `internal/atrest/classification_test.go` | completeness, staleness, dialect-invariance, liveness guard |
| `internal/atrest/render_test.go` | golden-file drift test + `-update` flag |
| `internal/persistence/store/atrest_crosscheck_test.go` | Docker-gated: parse == live introspection |
| `scripts/gen-at-rest.sh` | thin wrapper: `go test ./internal/atrest/ -run TestSecurityMdInSync -update` |
| `SECURITY.md` | gains the generated `## Data at rest` block |

---

## Task 1: The DDL reader

**Files:**
- Create: `internal/atrest/schema.go`
- Test: `internal/atrest/schema_test.go`

**Interfaces:**
- Consumes: nothing.
- Produces: `type Class string`; `type ColumnKey struct{ Table, Column string }`;
  `type Column struct{ Table, Name, Type string; Keys []string }`;
  `type Schema struct{ Dialect string; Columns map[ColumnKey]Column }`;
  `func (Schema) Tables() []string` — sorted, de-duplicated table names;
  `func ParseSQL(dialect, sqlText string) (Schema, error)`.

**Why `ColumnKey` and not a bare column name:** exactly one column name in the schema carries two
different classes — `wrkflw_human_task.claimed_by` is a human principal while
`wrkflw_call_links.claimed_by` is a worker lease owner (E7). A `map[string]` merges them.

- [ ] **Step 1: Write the failing test — columns, types, and the goose `Down` cutoff**

```go
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
```

**What makes this fail today:** `internal/atrest` does not exist. Compile error
`no required module provides package .../internal/atrest`. That is a valid RED.

⚠ The `t_ghost` assertion is the load-bearing one — E14 established that `Up`/`Down` are the only
goose directives in these four files, so the `Down` cutoff is the *only* directive rule the reader
needs, and a reader that ignores it silently doubles every table.

- [ ] **Step 2: Run it and observe RED**

```
go test ./internal/atrest/... 2>&1; echo "EXIT=$?"
```

Expected: build failure naming the missing package. **Read the log; judge by `EXIT`, never by a
`| grep | head` tail** — `head` closes the pipe and failures never render.

- [ ] **Step 3: Implement the minimum**

Write `internal/atrest/schema.go` with the types above and a `ParseSQL` that:
1. truncates `sqlText` at the first `-- +goose Down` line;
2. strips `--` comments to end-of-line;
3. splits into statements on `;`;
4. for each `CREATE TABLE <name> ( … )`, splits the body on top-level commas (**depth-aware** — a
   `DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ', 'now'))` in the SQLite outbox contains commas inside
   parentheses) and takes the first token as the column name, the second as the type;
5. skips body clauses beginning `PRIMARY`, `UNIQUE`, `FOREIGN`, `CONSTRAINT`, `KEY`, `INDEX`.

- [ ] **Step 4: Run it and observe GREEN**

```
go test ./internal/atrest/... -count=1 2>&1; echo "EXIT=$?"
```

⚠ `-count=1`: a cache hit can report `EXIT=0` over code that panics.

- [ ] **Step 5: Write the failing test for the three real parser traps**

All three were found by **executing** throwaway probes while designing (E11, E15), not by reading.

```go
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
					"foreign keys are deliberately OUT of the keyed derivation (ADR-0187 D8; see E15)")
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
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := atrest.ParseSQL("test", tt.src)
			tt.assert(t, got, err)
		})
	}
}
```

**What makes each case fail today:** step 3's minimum implementation parses no indexes at all, so
every `Keys` assertion fails. Trap 1's multi-space case additionally fails any line-oriented reader.

⚠ **The FK assertion is a `NotContains`, which is the weakest shape in this file.** It is here to
pin a *stated* exclusion (E15: the only FK column is `wrkflw_journal.instance_id`, already keyed via
PK, so including FKs changes no row **today**). Because a `NotContains` over an unimplemented feature
passes vacuously, task 1 must **also** add a comment in `schema.go` at the skip site naming E15, and
the reviewer must check that comment exists.

- [ ] **Step 6: Run it and observe RED, then implement index parsing, then GREEN**

```
go test ./internal/atrest/... -run TestParseSQL_RealWorldTraps -count=1 -v 2>&1; echo "EXIT=$?"
```

⚠ Confirm the subtests actually **ran** (`-v`) — `go test -run` on a name that does not exist
exits 0.

- [ ] **Step 7: Commit**

```bash
git add internal/atrest/schema.go internal/atrest/schema_test.go
git commit -m "feat(atrest): read migration DDL into a per-dialect schema"
```

---

## Task 2: Discovery, and the migration-set declaration

**Files:**
- Create: `internal/atrest/discover.go`
- Test: `internal/atrest/discover_test.go`

**Interfaces:**
- Consumes: `ParseSQL`, `Schema` from task 1.
- Produces: `func ModuleRoot() (string, error)`; `func DiscoverMigrationDirs(root string) ([]string, error)`;
  `type MigrationSet struct{ Dialects []string; Note string }`;
  `var MigrationSets map[string]MigrationSet`; `func LoadSchemas(root string) (map[string]Schema, error)`.

⚠⚠ **Read this before writing the code — it is the design's sharpest edge.** ADR-0187 decision 1
says migrations are *discovered*, never listed. But **dialect cannot be inferred from a path**:
three directories are named `postgres`/`mysql`/`sqlite`, and the fourth is
`internal/authz/casbin/migrations`, named for neither. So discovery finds **directories** and a
**declaration** (`MigrationSets`) says what each one is — and the declaration is checked **both
ways**. A directory with no declaration fails; a declaration matching no directory fails. Without the
two-way check this is hardcoding moved one level down, which is the exact defect F5 filed.

- [ ] **Step 1: Write the failing test**

```go
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
	}, dirs, "four migration directories exist (E1); a hardcoded three-directory list is what lost casbin_rule")

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
```

**What makes it fail today:** `DiscoverMigrationDirs` does not exist — compile error. After that,
the `ElementsMatch` is a **real** assertion: it fails against any implementation that walks a
hardcoded list, and it fails if a fifth directory is added without a declaration.

- [ ] **Step 2: RED** — `go test ./internal/atrest/... -count=1 2>&1; echo "EXIT=$?"`

- [ ] **Step 3: Implement**

```go
// MigrationSets declares what each discovered migration directory IS. Discovery
// (DiscoverMigrationDirs) finds the directories; this map says which dialects
// each one applies to, because dialect CANNOT be inferred from the path — the
// casbin set is named "migrations", not "postgres".
//
// It is checked BOTH WAYS by TestDiscoverMigrationDirs_FindsAllFourAndAllAreDeclared:
// a discovered directory absent here fails, and an entry here matching no
// discovered directory fails. Do not "simplify" it into a hardcoded walk — a
// hardcoded three-directory list is exactly what lost casbin_rule (ADR-0187 D1).
var MigrationSets = map[string]MigrationSet{
	"internal/persistence/store/migrations/postgres": {Dialects: []string{"postgres"}},
	"internal/persistence/store/migrations/mysql":    {Dialects: []string{"mysql"}},
	"internal/persistence/store/migrations/sqlite":   {Dialects: []string{"sqlite"}},
	"internal/authz/casbin/migrations": {
		Dialects: []string{"postgres"},
		Note:     "present only under the FromDB casbin policy source; Postgres only (E3)",
	},
}
```

`ModuleRoot` walks up from the caller's working directory to the first `go.mod`.
`DiscoverMigrationDirs` walks `root` and returns every directory named `migrations` **or** whose
parent is named `migrations`, containing at least one `.sql` file, as slash-separated paths relative
to `root`, sorted. `LoadSchemas` merges each set's parsed statements into the `Schema` of every
dialect the set declares.

- [ ] **Step 4: GREEN** — same command, `EXIT=0`.

- [ ] **Step 5: Write the failing test for the real column census**

```go
func TestLoadSchemas_ColumnCensus(t *testing.T) {
	t.Parallel()

	root, err := atrest.ModuleRoot()
	require.NoError(t, err)
	schemas, err := atrest.LoadSchemas(root)
	require.NoError(t, err)

	// E4: 79 wrkflw_* columns in every dialect; casbin_rule adds 8 to postgres only.
	assert.Len(t, schemas["postgres"].Columns, 87, "79 wrkflw_* + 8 casbin_rule (E3, E4)")
	assert.Len(t, schemas["mysql"].Columns, 79)
	assert.Len(t, schemas["sqlite"].Columns, 79)

	// D2b: MySQL declares the journal payload column as trigger_ ("trigger" is
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
}

func TestParserFailsClosedOnUnrecognisedStatements(t *testing.T) {
	t.Parallel()

	// D11: ADR-0132 says future schema changes "resume as new numbered files",
	// i.e. ALTER TABLE. A CREATE TABLE-only reader sees zero columns there:
	// nothing unclassified, SECURITY.md regenerating identically, every
	// non-Docker guard green, and a new column silently absent from a SECURITY
	// document. Verified free: the corpus has ZERO "alter table" today.
	_, err := atrest.ParseSQL("postgres", "-- +goose Up\nALTER TABLE wrkflw_instances ADD COLUMN secret TEXT;\n")
	require.Error(t, err, "an unrecognised statement must be an ERROR, never a skip")
	assert.Contains(t, err.Error(), "ALTER TABLE")
}
```

**What makes it fail today:** the three parser traps. A reader that miscounts MySQL's inline
`INDEX`, its table-level `CONSTRAINT`, or the SQLite `DEFAULT (strftime(…, 'now'))` comma reports
80 or 81 for MySQL and 80 for SQLite. These three numbers are the parser's real acceptance test.

- [ ] **Step 6: RED, implement, GREEN, commit**

```bash
git add internal/atrest/discover.go internal/atrest/discover_test.go
git commit -m "feat(atrest): discover migration sets and declare what each one is"
```

---

## Task 3: The classification, with completeness and staleness guards

**Files:**
- Create: `internal/atrest/classification.go`
- Test: `internal/atrest/classification_test.go`

**Interfaces:**
- Consumes: `ColumnKey`, `Schema`, `LoadSchemas`, `ModuleRoot`.
- Produces: `var Classification map[ColumnKey]Class`, and the six `Class` constants
  `ClassFreeform`, `ClassActor`, `ClassPolicy`, `ClassReference`, `ClassScalar`, `ClassTimestamp`.

- [ ] **Step 1: Write the failing test FIRST, with the map still EMPTY**

```go
func TestClassificationCoversTheSchemaExactly(t *testing.T) {
	t.Parallel()

	root, err := atrest.ModuleRoot()
	require.NoError(t, err)
	schemas, err := atrest.LoadSchemas(root)
	require.NoError(t, err)

	inSchema := map[atrest.ColumnKey]bool{}
	for _, s := range schemas {
		for k := range s.Columns {
			inSchema[k] = true
		}
	}

	var unclassified []string
	for k := range inSchema {
		if _, ok := atrest.Classification[k]; !ok {
			unclassified = append(unclassified, k.Table+"."+k.Column)
		}
	}
	sort.Strings(unclassified)
	assert.Empty(t, unclassified,
		"every stored column must carry a stated class — a consumer who encrypts the columns we "+
			"name and leaves the rest in the clear has been harmed by our documentation")

	var stale []string
	for k := range atrest.Classification {
		if !inSchema[k] {
			stale = append(stale, k.Table+"."+k.Column)
		}
	}
	sort.Strings(stale)
	assert.Empty(t, stale,
		"a classification entry naming an absent column is stale — delete it, or the next "+
			"unclassified column hides under it")

	assert.Len(t, atrest.Classification, 87, "87 = 79 wrkflw_* + 8 casbin_rule (E6)")
}
```

**What makes it fail today:** `Classification` starts **empty**, so all 87 columns report
unclassified. This is a **real RED**, not a pin.

- [ ] **Step 2: RED — capture the failure text**

```
go test ./internal/atrest/... -run TestClassificationCoversTheSchemaExactly -count=1 -v 2>&1; echo "EXIT=$?"
```

Paste the list of 87 unclassified columns into the task's write-up. It is the work list.

- [ ] **Step 3: Fill the map from the spec's classification section**

Copy it from `docs/specs/2026-08-22-at-rest-posture.md` § "The classification". Counts to land on
(E6): `reference` 27, `timestamp` 19, `scalar` 17, `freeform` 11, `policy` 8, `actor` 5 = **87**.

⚠ **Do not re-derive these counts from the spec's prose — assert them.** Add:

```go
func TestClassificationPerClassCounts(t *testing.T) {
	t.Parallel()
	counts := map[atrest.Class]int{}
	for _, c := range atrest.Classification {
		counts[c]++
	}
	assert.Equal(t, map[atrest.Class]int{
		atrest.ClassReference: 27,
		atrest.ClassTimestamp: 19,
		atrest.ClassScalar:    17,
		atrest.ClassFreeform:  11,
		atrest.ClassPolicy:    8,
		atrest.ClassActor:     5,
	}, counts, "E6; four of these six refuted the author's own pre-count estimates")
}
```

- [ ] **Step 4: GREEN, then pin the `claimed_by` split**

```go
func TestClaimedByIsTwoDifferentColumns(t *testing.T) {
	t.Parallel()

	assert.Equal(t, atrest.ClassActor,
		atrest.Classification[atrest.ColumnKey{Table: "wrkflw_human_task", Column: "claimed_by"}],
		"a human principal: ADR-0098 calls it the scalar projection of claim.actor.id")
	assert.Equal(t, atrest.ClassReference,
		atrest.Classification[atrest.ColumnKey{Table: "wrkflw_call_links", Column: "claimed_by"}],
		"a worker lease owner: WithCallLinkLease(owner string, ttl) — not a person (E7)")
}
```

**What makes it fail:** it fails against any `map[string]Class` keyed on the bare column name, which
is the refactor this pin exists to block.

- [ ] **Step 5: Commit**

```bash
git add internal/atrest/classification.go internal/atrest/classification_test.go
git commit -m "feat(atrest): state the at-rest class of all 87 stored columns"
```

---

## Task 4: The dialect-invariance pin — with a liveness guard and a real mutation

**Files:**
- Modify: `internal/atrest/schema.go` (this is where `KeysWithPrefix` lives — it operates on `Schema`)
- Modify: `internal/atrest/classification_test.go`
- Test: same test file

**Interfaces:**
- Consumes: `Schema`, `ColumnKey`, `LoadSchemas`, `ModuleRoot` from tasks 1–2.
- Produces: `func KeysWithPrefix(s Schema, prefix string) []ColumnKey` — the sorted keys of one
  dialect's schema whose **table name** carries the prefix. Exported and parameter-taking, so the
  liveness guard in step 3 can drive the same matcher the production assertion uses.

⚠⚠ **This agent gets its own `git worktree`.** Step 5 ablates the normalization; a previous session
lost ~40 minutes to a "hang" that was one agent's live ablation observed by another. State the
worktree in the brief — it is not inherited.

⚠⚠ **ROUND-2 CORRECTION — this task is a REAL RED, not a pin.** Earlier text here read *"This is a
PIN, not a RED-green fix. The property already holds."* That was true of the guard round 1 deleted
and is **false** of the key-set invariant that replaced it: without D2b's normalization MySQL's
`trigger_` makes the key sets disagree, so step 1 **fails today**. The liveness guard and mutation
in steps 3–5 are kept anyway — a guard that is RED today becomes a pin the moment it is fixed.

⚠⚠ **ROUND 1 REPLACED THIS TASK'S ASSERTION.** It previously called `ClassDivergences(schemas, cls)`
where `cls` is a flat `map[ColumnKey]Class` — **with no dialect term, so a class cannot differ and
the result is empty for every possible input.** A lens fuzzed the prescribed signature over 200,000
randomised inputs and got zero non-empty results. Do not resurrect it.

- [ ] **Step 1: Write the KEY-SET agreement assertion — it fails today**

```go
func TestNormalizedKeySetAgreesAcrossDialects(t *testing.T) {
	t.Parallel()

	root, err := atrest.ModuleRoot()
	require.NoError(t, err)
	schemas, err := atrest.LoadSchemas(root)
	require.NoError(t, err)

	// wrkflw_* only: casbin_rule is postgres-only by construction (E3) and its
	// absence elsewhere is not a divergence.
	pg := atrest.KeysWithPrefix(schemas["postgres"], "wrkflw_")
	my := atrest.KeysWithPrefix(schemas["mysql"], "wrkflw_")
	sq := atrest.KeysWithPrefix(schemas["sqlite"], "wrkflw_")

	assert.ElementsMatch(t, pg, my, "postgres vs mysql normalized key set")
	assert.ElementsMatch(t, pg, sq, "postgres vs sqlite normalized key set")
	assert.Len(t, pg, 79)
}
```

**What makes it fail today:** MySQL declares `wrkflw_journal.trigger_`. Without D2b's normalization
the MySQL set contains `trigger_` and the Postgres set contains `trigger`, so `ElementsMatch` fails
with a two-line diff. **This is a real RED, not a pin** — which is the whole reason it replaced the
guard it replaced.

- [ ] **Step 2: Extract the matcher so a fixture can drive it**

`KeysWithPrefix(s Schema, prefix string) []ColumnKey` returns the sorted keys of one dialect's schema
whose table name carries the prefix. **It must be an exported function taking its inputs as
parameters**, not a closure over package state — otherwise step 3 cannot run it over a fixture.

- [ ] **Step 3: Write the LIVENESS GUARD — the step that makes the pin worth having**

```go
func TestKeySetMatcherFires(t *testing.T) {
	t.Parallel()

	shared := atrest.ColumnKey{Table: "t", Column: "agrees"}
	onlyPG := atrest.ColumnKey{Table: "t", Column: "only_in_pg"}
	otherTable := atrest.ColumnKey{Table: "other", Column: "ignored"}

	// pg carries one column sqlite lacks, plus a column under a DIFFERENT table
	// prefix that the matcher must exclude rather than report as a divergence.
	schemas := map[string]atrest.Schema{
		"pg": {Dialect: "pg", Columns: map[atrest.ColumnKey]atrest.Column{
			shared:     {Table: "t", Name: "agrees", Type: "TEXT"},
			onlyPG:     {Table: "t", Name: "only_in_pg", Type: "TEXT"},
			otherTable: {Table: "other", Name: "ignored", Type: "TEXT"},
		}},
		"sq": {Dialect: "sq", Columns: map[atrest.ColumnKey]atrest.Column{
			shared: {Table: "t", Name: "agrees", Type: "TEXT"},
		}},
	}

	// ⚠⚠ ROUND 2: this guard previously drove ClassDivergencesPerDialect, a symbol
	// round 1 DELETED and step 1 says not to resurrect. It now drives the matcher
	// the production assertion actually uses, which is the whole point of a
	// liveness guard — a guard exercising a different function proves nothing
	// about the one that ships.
	pgKeys := atrest.KeysWithPrefix(schemas["pg"], "t")
	sqKeys := atrest.KeysWithPrefix(schemas["sq"], "t")

	assert.NotElementsMatch(t, pgKeys, sqKeys,
		"the matcher must SEE the planted key divergence (t.only_in_pg)")
	assert.ElementsMatch(t, pgKeys, atrest.KeysWithPrefix(schemas["pg"], "t"),
		"and must be stable for identical input — a matcher that reports everything is as "+
			"useless as one that reports nothing")
}
```

⚠ **Check the FIXTURE, not the assertion line.** The fixture above declares **two dialects** and
**two columns**, one of each kind. A fixture with one dialect, or with only the rogue column, makes
this test unfalsifiable — which is precisely how two "tests that really cover this" in this repo
turned out to be tests that could not fail.

- [ ] **Step 4: Run steps 1 + 3 and observe GREEN for 1, RED-then-GREEN for 3**

Step 3 is a genuine RED: `KeysWithPrefix` does not exist yet.

- [ ] **Step 5: MUTATION — prove the guard fires against a REAL file**

⚠⚠ **ROUND 1 REPLACED THE MUTATION TOO.** The old one renamed `note` → `note_text` in SQLite only
and expected the *staleness* guard to fire. Simulated, it produces `unclassified=1, stale=0` —
because `inSchema` is a **union** and `note` survives via Postgres and MySQL. It exercised a
different guard from the one it claimed to verify, and its stated expectation was wrong as well.
The mutation below ablates **D2b's normalization**, which is what this task's assertion actually
depends on.

```bash
cp internal/atrest/discover.go /tmp/discover.bak
# Ablate D2b: make LoadSchemas stop normalizing the MySQL reserved-word alias.
# (Exact edit depends on how you implemented it — the point is that trigger_
# reaches the returned Schema unchanged.)
$EDITOR internal/atrest/discover.go   # disable the JournalTriggerColumn normalization
grep -c "JournalTriggerColumn" internal/atrest/discover.go   # REQUIRE 0 before trusting the run
go test ./internal/atrest/... -count=1 2>&1; echo "MUTATION EXIT=$?"
cp /tmp/discover.bak internal/atrest/discover.go
diff internal/atrest/discover.go /tmp/discover.bak && echo "RESTORED CLEAN"
go test ./internal/atrest/... -count=1 2>&1; echo "AFTER RESTORE EXIT=$?"
```

Expected: `MUTATION EXIT=1` with `TestNormalizedKeySetAgreesAcrossDialects` reporting
`trigger_` present in MySQL and absent in Postgres, **and** `TestClassificationCoversTheSchemaExactly`
reporting `wrkflw_journal.trigger_` unclassified. `AFTER RESTORE EXIT=0`, `diff` clean.

⚠ **Verify the ablation actually applied before trusting the result** — the `grep -c` must print 0.
A mutation that does not apply is not a RED, and a green suite then looks like a passing mutation.

⚠ **Restore with `cp`, never `git checkout <path>`.**

- [ ] **Step 6: Record the observed failure text in this plan's `▶ Progress` block, then commit**

```bash
git add internal/atrest/classification_test.go internal/atrest/classification.go
git commit -m "test(atrest): pin the dialect-invariance of the class, with a liveness guard"
```

---

## Task 5: The `keyed` derivation, per dialect

**Files:**
- Modify: `internal/atrest/schema.go`
- Test: `internal/atrest/schema_test.go`

**Interfaces:**
- Produces: `Column.Keys []string` populated with `"PK"`, `"UNIQUE"`, `"index"`, `"index-predicate"`.

⚠ **`keyed` is dialect-DEPENDENT although `class` is not** (E10). Postgres declares 11 indexes of
which 5 are partial; MySQL 8 plus 3 inline, none partial; SQLite 11, none partial. Emit per dialect.
⚠ **This corrected the author's own design**, which had `keyed` riding along with the invariant
class. Do not re-merge them.

- [ ] **Step 1: Write the failing test against the real schema**

```go
func TestKeyedLowerBound_Postgres(t *testing.T) {
	t.Parallel()

	root, err := atrest.ModuleRoot()
	require.NoError(t, err)
	schemas, err := atrest.LoadSchemas(root)
	require.NoError(t, err)

	// ⚠⚠ ROUND 1: this loop previously did `if k.Table == "casbin_rule" { continue }`
	// and the plan called the resulting assertion one that "cannot rot". It could
	// not rot because it SKIPPED ITS OWN COUNTEREXAMPLE: casbin_rule.ptype is
	// class policy and carries casbin_rule_ptype_idx, which falsified the safety
	// sentence this delivery was built to publish. Every table, every dialect.
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

func TestKeyedCountPerDialect(t *testing.T) {
	t.Parallel()

	root, err := atrest.ModuleRoot()
	require.NoError(t, err)
	schemas, err := atrest.LoadSchemas(root)
	require.NoError(t, err)

	// keyed is dialect-DEPENDENT (E10) — assert all three, never one as "the" number.
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
		"mysql has no partial indexes; it folds the predicate into the key instead (E10)")
}
```

**What makes these fail today:** task 1's reader records key membership only for the shapes its own
trap tests exercised; neither the **29 / 15-8-4-1-1** census nor the per-dialect predicate
distinction has ever been computed over the real files. ⚠ **The `byClass` map is the load-bearing
assertion** — the bare `29` would still pass if the derivation keyed the wrong 29 columns.

⚠⚠ **ROUND 2 CORRECTION — this paragraph previously stated the WITHDRAWN census** (`27/15/7/4/1`,
"the bare `27`", and *"the `byClass` map has exactly four entries and no `freeform`/`policy` keys …
the 'zero freeform, zero policy' claim expressed as an equality"*). The map has **five** entries and
one of them is `ClassPolicy: 1`. This mattered more than a stale number: an implementer trusting the
falsifiability prose over the code would have written the four-entry map, and **the documented route
to making it pass is restoring the `casbin_rule` skip that caused the round-1 Critical.**

⚠ The `assert.Equal` on the whole `byClass` map is still the right shape — it fails if any class
appears, disappears or moves. What it now pins is that **`policy`-keyed is exactly 1**, not zero.

- [ ] **Step 2: RED, implement, GREEN, commit**

```bash
git add internal/atrest/schema.go internal/atrest/schema_test.go
git commit -m "feat(atrest): derive the keyed lower bound per dialect"
```

---

## Task 6: The generator and the `SECURITY.md` drift guard

**Files:**
- Create: `internal/atrest/render.go`, `internal/atrest/render_test.go`
- Modify: `SECURITY.md`

**Interfaces:**
- Produces: `func Render(schemas map[string]Schema, cls map[ColumnKey]Class) (string, error)`;
  the markers `<!-- BEGIN at-rest (generated) -->` / `<!-- END at-rest -->`.

- [ ] **Step 1: Write the failing drift test with its `-update` flag**

```go
var update = flag.Bool("update", false, "rewrite the generated SECURITY.md block instead of asserting it")

func TestSecurityMdInSync(t *testing.T) {
	root, err := atrest.ModuleRoot()
	require.NoError(t, err)
	schemas, err := atrest.LoadSchemas(root)
	require.NoError(t, err)

	block, err := atrest.Render(schemas, atrest.Classification)
	require.NoError(t, err)

	path := filepath.Join(root, "SECURITY.md")
	current, err := os.ReadFile(path)
	require.NoError(t, err)

	next, err := atrest.ReplaceBlock(string(current), block)
	require.NoError(t, err, "SECURITY.md must carry the BEGIN/END at-rest markers")

	if *update {
		require.NoError(t, os.WriteFile(path, []byte(next), 0o644))
		t.Log("SECURITY.md rewritten; re-run without -update")
		return
	}

	assert.Equal(t, next, string(current),
		"SECURITY.md's at-rest block is stale — run scripts/gen-at-rest.sh")
}
```

**What makes it fail today:** `SECURITY.md` carries **no** `BEGIN/END at-rest` markers, so
`ReplaceBlock` errors and `require.NoError` fails. That is a **real RED**, and it stays red until
step 3 adds the markers *and* the generated content matches.

- [ ] **Step 2: RED** — run it, observe the missing-marker failure.

- [ ] **Step 3: Implement `Render` + `ReplaceBlock`; add the markers to `SECURITY.md`; generate**

⚠⚠ **D12 — `Render` MUST be deterministic.** Both inputs are Go maps and Go randomises map
iteration. Sort rows by `(table, column)` and emit dialect columns in a fixed order. Add a test that
renders twice and asserts equality.

⚠ **The hazard is a FLAKY GREEN, not a reliable red** — which is why this cannot be left to chance.
Round 1 justified D12 with "995 of 1000 renders differed"; round 2 re-measured that as a single
stochastic sample ranging **949–995**, whose stable form is **87 distinct render orders**. So
`scripts/gen-at-rest.sh` — which writes and then re-asserts **in a second process** — would have
passed roughly **1 run in 87**, and Task 8's `diff` idempotence check likewise. An intermittently
passing generator is worse than one that never passes, because nobody investigates a green run.

The block is a new `## Data at rest` section carrying, per ADR-0187 decision 9 and the spec's
"What `SECURITY.md` gains":

1. the six class definitions and what to assume about each;
2. the 87-row table — `table`, `column`, `class`, physical type per dialect, `keyed` per dialect;
3. ⚠⚠ **DO NOT emit a blanket "safe to encrypt" sentence.** This step previously instructed you to
   generate *"every `freeform` and `policy` column is index-free and can be encrypted without
   breaking an index"* — the round-1 Critical, which would have shipped **hard-coded into
   `render.go`**. It is false: `casbin_rule.ptype` is class `policy` and indexed. Emit instead the
   one derived warning that survives: **`wrkflw_human_task.claimed_by` IS indexed, so encrypting it
   non-deterministically costs the `AssignedTo` lookup**;
4. the **lower bound** caveat on `keyed`, phrased as a HAZARD: derived from DDL only and blind to
   query-level `WHERE`/`ORDER BY`, with `casbin_rule.{ptype,v0..v5}` named as the known instance
   (`RemovePolicy` filters all seven by equality);
4b. the **goose-no-checksum** caveat: the table describes a **fresh** database; a deployment migrated
   before an in-place edit can hold a schema it does not describe;
4c. `—` for "present, unindexed" and `n/a` for "absent in this dialect", rendered **distinctly**;
5. `casbin_rule` exists **only** in a Postgres deployment that has run `casbinauthz.MigrateCasbin`
   (⚠ corrected from "only under `FromDB`": that call is never auto-run, so `FromDB` without it has
   no table and any other policy source WITH it does), and authorization policy is at rest in
   **three** places — `casbin_rule`, `wrkflw_human_task.eligibility`, and
   `wrkflw_definitions.definition` — with the count derived from `atrest.PolicyAtRestLocations`;
6. consumer-supplied migrations are **out of scope**;
7. a pointer to ADR-0187's non-goals, so nobody infers that a codec exists.

⚠ **Sentences 3–7 are emitted BY THE GENERATOR, not typed into `SECURITY.md`.** If they are typed
in, the next `-update` deletes them.

- [ ] **Step 3b: Prove the drift guard pins EVERY ENTRY, not just the totals**

⚠⚠ **This step exists because of a Task 3 review finding, and it is the ONLY protection the other
85 classification entries have.** Task 3's guards are `TestClassificationCoversTheSchemaExactly`
(which key set is covered) and `TestClassificationPerClassCounts` (the six totals). A reviewer
mutated two entries' classes **reciprocally** — `wrkflw_outbox.id` scalar→freeform and
`wrkflw_journal.trigger` freeform→scalar — preserving every total, and **all three Task 3 tests
still passed**. Only the two `claimed_by` columns are individually pinned.

Because the generated block carries `class` **per row**, `TestSecurityMdInSync` is what closes that
gap. Prove it:

```bash
cp internal/atrest/classification.go /tmp/classification.bak
# Reciprocal swap: totals unchanged, two entries wrong.
#   wrkflw_outbox.id          ClassScalar   -> ClassFreeform
#   wrkflw_journal.trigger    ClassFreeform -> ClassScalar
$EDITOR internal/atrest/classification.go
go test ./internal/atrest/... -run 'TestClassificationPerClassCounts|TestClassificationCoversTheSchemaExactly' -count=1 2>&1; echo "TASK3 GUARDS EXIT=$?"   # expect 0 — they cannot see it
go test ./internal/atrest/... -run TestSecurityMdInSync -count=1 2>&1; echo "DRIFT GUARD EXIT=$?"                                                          # expect 1 — it must
cp /tmp/classification.bak internal/atrest/classification.go
diff internal/atrest/classification.go /tmp/classification.bak && echo "RESTORED CLEAN"
```

⚠ **If `DRIFT GUARD EXIT` is 0, stop and report it** — it means the rendered block does not carry
the class per row, and the classification has no per-entry regression protection anywhere in the
delivery.

- [ ] **Step 4: GREEN, then prove the drift guard actually guards**

```bash
cp SECURITY.md /tmp/security.bak
sed -i '' 's/| wrkflw_human_task | claimed_by | actor |/| wrkflw_human_task | claimed_by | reference |/' SECURITY.md
grep -c '| wrkflw_human_task | claimed_by | reference |' SECURITY.md   # require 1 before trusting the run
go test ./internal/atrest/... -run TestSecurityMdInSync -count=1 2>&1; echo "DRIFT EXIT=$?"
cp /tmp/security.bak SECURITY.md
diff SECURITY.md /tmp/security.bak && echo "RESTORED CLEAN"
```

Expected `DRIFT EXIT=1`. ⚠ If the `grep -c` prints 0 the `sed` did not match and the green run
proves nothing — adjust the pattern to the generated table's real column order first.

- [ ] **Step 5: Commit**

```bash
git add internal/atrest/render.go internal/atrest/render_test.go SECURITY.md
git commit -m "feat(atrest): generate the at-rest section of SECURITY.md"
```

---

## Task 7: The Docker-gated cross-check — is the parser honest?

**Files:**
- Create: `internal/persistence/store/atrest_crosscheck_test.go` (**`package store_test`**)

⚠ **Different Go package from tasks 1–6.** It lives here so it can call the existing
`introspectPostgres` / `introspectMySQL` / `introspectSQLite` in `migration_parity_test.go` with no
refactoring (E12).
⚠ **This task needs Docker.** State that explicitly in the agent's brief — CLAUDE.md's standing
Docker permission covers only the Verification coverage and no-regressions runs.
⚠ **Do NOT change `colFacts` or the existing `introspect*` helpers.** They deliberately exclude
physical types and filter `LIKE 'wrkflw_%'`; that exclusion is intentional and
`TestMigrationParity_LogicalSchemaConverges` depends on it. Add **sibling** functions.

- [ ] **Step 1: Write the failing test**

```go
// TestAtRestParseMatchesLiveIntrospection asserts the DDL reader's view of the
// schema equals what the database actually creates. The everyday at-rest guard
// parses SQL text so it needs no Docker; this is the test that proves the parse
// is not quietly lying (ADR-0187 D7).
func TestAtRestParseMatchesLiveIntrospection(t *testing.T) {
	ctx := t.Context()

	root, err := atrest.ModuleRoot()
	require.NoError(t, err)
	parsed, err := atrest.LoadSchemas(root)
	require.NoError(t, err)

	// ⚠⚠ ROUND 1: the SQLite leg must live in its OWN test function, NOT here.
	// dbtest does NOT skip when Docker is unavailable — its helpers
	// require.NoError on the shared error, so the whole function FAILS and the
	// Docker-free SQLite check is lost with it. Use the repo helper rather than
	// hand-rolling a DSN (Golang rule #3): dbtest.RunTestSQLite(t).
	sqliteDB := dbtest.RunTestSQLite(t)
	sm, err := store.NewSQLiteMigrator(sqliteDB)
	require.NoError(t, err)
	require.NoError(t, sm.Up(ctx))
	assert.ElementsMatch(t, parsedColumnNames(parsed["sqlite"], "wrkflw_"),
		liveColumnNames(introspectSQLite(t, sqliteDB)), "sqlite: parsed vs live")

	// Postgres + MySQL: Docker required. ⚠ dbtest FAILS rather than skips.
	pool := dbtest.RunTestDatabase(t)
	pm, err := store.NewPostgresMigrator(pool)
	require.NoError(t, err)
	require.NoError(t, pm.Up(ctx))
	assert.ElementsMatch(t, parsedColumnNames(parsed["postgres"], "wrkflw_"),
		liveColumnNames(introspectPostgres(t, pool)), "postgres: parsed vs live")

	mysqlDB := dbtest.RunTestMySQL(t) // already migrated
	assert.ElementsMatch(t, parsedColumnNames(parsed["mysql"], "wrkflw_"),
		liveColumnNames(introspectMySQL(t, mysqlDB)), "mysql: parsed vs live")
}
```

⚠⚠ **ROUND 2 — NORMALIZE THE LIVE SIDE TOO, or this test can NEVER go green.** D2b normalizes
MySQL's `trigger_` → `trigger` inside `LoadSchemas`, but **live MySQL introspection genuinely
returns `trigger_`**. Comparing a normalized parse against raw introspection guarantees a one-column
diff forever — and this is the only test that proves the parser honest. Apply the same
`dialect.*.JournalTriggerColumn()` normalization to the introspected side, exactly as
`migration_parity_test.go:71` already does for its own comparison.

⚠ **The `"wrkflw_"` prefix argument is deliberate and must stay.** The existing helpers filter
`LIKE 'wrkflw_%'` (E2), so `casbin_rule` is invisible to them; comparing an unfiltered parse against
a filtered introspection would fail on Postgres for a reason that is not a parser bug.
⚠ **`casbin_rule` is therefore NOT cross-checked by this test.** Step 3 adds it separately; do not
let it be silently uncovered — that table is the whole reason F5 existed.

- [ ] **Step 2: RED** — the helpers `parsedColumnNames` / `liveColumnNames` do not exist. Compile error.

- [ ] **Step 3: Implement, and add the casbin cross-check**

```go
// casbin_rule is Postgres-only and applied by its own migrator (E3), so it is
// cross-checked separately from the wrkflw_* set — the parity helpers filter it out.
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
```

- [ ] **Step 4: GREEN with Docker up**

```
docker info >/dev/null 2>&1 && echo "docker up" || echo "docker DOWN — say so, do not substitute"
go test ./internal/persistence/store/ -run 'TestAtRestParse' -count=1 -v 2>&1; echo "EXIT=$?"
```

⚠⚠ **ROUND 1 correction: `dbtest` does NOT skip when Docker is unavailable.** Its production
helpers `require.NoError(t, mysqlSharedErr)`, so a missing daemon **fails** the test. (The two
`t.Skip` calls in `internal/dbtest` are in `dbname_test.go`, gating an unrelated child-process
helper.) So: if the daemon is down, say so and label the result partial — do not read a failure as
a skip, and do not read a skip that never happens as a pass.

- [ ] **Step 5: Close the uncross-checked-table hole (A19)**

The spec named this "cheap fix" and round 1 found it appeared in no task. Add it here:

```go
// Every table the parser discovers must appear in SOME cross-check. Without
// this, a fifth migration set creating non-wrkflw_ tables is discovered,
// parsed and classified while being silently absent from every live
// comparison — the guard that fails on an unclassified COLUMN has no
// counterpart that fails on an uncross-checked TABLE.
func TestEveryParsedTableIsCrossChecked(t *testing.T) {
	root, err := atrest.ModuleRoot()
	require.NoError(t, err)
	parsed, err := atrest.LoadSchemas(root)
	require.NoError(t, err)

	crossChecked := map[string]bool{"casbin_rule": true} // covered by the test above
	for _, tbl := range parsed["postgres"].Tables() {
		if strings.HasPrefix(tbl, "wrkflw_") {
			crossChecked[tbl] = true
		}
		assert.True(t, crossChecked[tbl],
			"table %q is parsed and classified but never compared against a live database", tbl)
	}
}
```

- [ ] **Step 6: Commit**

```bash
git add internal/persistence/store/atrest_crosscheck_test.go
git commit -m "test(store): cross-check the at-rest DDL reader against live introspection"
```

---

## Task 8: The script, the docs sweep, and the delivery gate

**Files:**
- Create: `scripts/gen-at-rest.sh`
- Modify: `docs/plans/2026-08-22-at-rest-posture.md` (the `▶ Progress` block), `docs/plans/HANDOVER.md`

- [ ] **Step 1: Write `scripts/gen-at-rest.sh`**

```bash
#!/usr/bin/env bash
# Regenerate the "Data at rest" block in SECURITY.md from the migration schema
# and the stated column classification (ADR-0187).
#
# The classification itself is a JUDGEMENT and lives in
# internal/atrest/classification.go. Everything else in the block is derived.
set -euo pipefail
cd "$(dirname "$0")/.."
go test ./internal/atrest/ -run TestSecurityMdInSync -update -count=1
go test ./internal/atrest/ -run TestSecurityMdInSync -count=1
echo "SECURITY.md at-rest block regenerated and verified."
```

`chmod +x scripts/gen-at-rest.sh`. Match the four existing scripts' header style.

- [ ] **Step 2: Verify the script round-trips**

```bash
cp SECURITY.md /tmp/sec.bak
./scripts/gen-at-rest.sh
diff SECURITY.md /tmp/sec.bak && echo "IDEMPOTENT"
```

- [ ] **Step 3: Sweep the diff for unexecuted claims and over-reaching quantifiers**

Per the Delivery Gate item 2. Every `all` / `none` / `only` / `every` / `never` / `always` and every
explicit count in the new code comments, the generated `SECURITY.md` text, the ADR and the spec gets
re-verified **as if it stood alone**. False claims in committed comments have reached this gate
repeatedly and are cheapest to kill here.

- [ ] **Step 4: Verification**

```bash
docker info >/dev/null 2>&1 && echo "docker up" || echo "docker DOWN"
go test -race -coverprofile=cover.out ./... 2>&1; echo "EXIT=$?"
scripts/coverage.sh cover.out                      # >= 85% on internal/atrest; hot paths first
go test ./... 2>&1; echo "EXIT=$?"                 # no regressions
command -v golangci-lint >/dev/null && golangci-lint run ./... ; echo "LINT EXIT=$?"
```

⚠ `scripts/coverage.sh` only **reports**; its exit code proves nothing — read the number.
⚠ If Docker is down, say so and label any container-free subset as **partial**. If
`golangci-lint` is absent, offer install-or-skip and report which happened. Never substitute
`go vet` and call it lint.

- [ ] **Step 5: Update this plan's `▶ Progress` block and `docs/plans/HANDOVER.md`** (rule #10),
      then hand to the owner for `/code-review` and `/security-review` — **owner-invoked only**.

- [ ] **Step 6: Fold everything into ONE feature commit**

```bash
git add -A
git commit --amend    # fold; do NOT stack fix: commits (Git Discipline)
```

---

## Self-review against the spec

| spec decision | task |
|---|---|
| D1 discovery by glob, never a listed directory | 2 (plus the two-way declaration check) |
| D2 role-based, dialect-invariant classification | 3, 4 |
| D3 key is `table.column` | 1 (type), 3 (`claimed_by` pin) |
| D4 six classes, 87 columns, the six counts | 3 |
| D5 `eligibility` is `policy` | 3 (in the map), 6 (the policy-locations sentence — **three** places, derived from `atrest.PolicyAtRestLocations`; it said "two" until `/code-review`) |
| D6 guard fails on omission / staleness / drift / **key-set disagreement** / parser dishonesty | 3, 4, 6, 7 |
| D2b reserved-word normalization (`trigger_`) | 2 (parse), 4 (the invariant that depends on it) |
| D11 parser fails closed on unrecognised statements | 2 |
| D12 deterministic rendering | 6 |
| D7 cross-check against live introspection | 7 |
| D8 `keyed`, per dialect, lower bound | 5 (derivation), 6 (the caveat text) |
| D9 golden-file generator, no `main` | 6, 8 |
| D10 no mechanism ships | — nothing in any task builds one; asserted by the reviewer |
| non-goals restated in `SECURITY.md` | 6 step 3 item 7 |
| open questions 1 and 2 (`subscriber`/`topic`; `candidates`) | carried into the audit, **not** resolved by implementation |

**Placeholder scan:** no `TBD`, no "add appropriate error handling", no "similar to Task N". Every
code step carries real code.

**Type consistency:** `ColumnKey`, `Column`, `Schema`, `Class`, `Classification`, `LoadSchemas`,
`ModuleRoot`, `KeysWithPrefix`, `Render`, `ReplaceBlock`, `MigrationSets`, `MigrationSet` — each
defined in exactly one task's **Produces** block and spelled identically at every later use.

⚠⚠ **ROUND 2: this roster previously still listed `ClassDivergences` and `ClassDivergencesPerDialect`
— both DELETED in round 1 — and omitted `KeysWithPrefix`, which replaced them.** The paragraph below
it said round 1 deleted both while Task 4 still prescribed one. A type roster that contradicts its
own tasks is worse than none.

⚠⚠ **The "Known gap" previously recorded here was itself the defect.** It read that
`ClassDivergences` and `ClassDivergencesPerDialect` must both exist "or the liveness guard tests a
different code path from the pin". The real problem was one level down: `ClassDivergences` **could
not fail at all**, so no arrangement of the two functions would have helped. Round 1 deleted both and
replaced them with `KeysWithPrefix` plus a key-set assertion that is RED today. Recorded because
diagnosing a hazard one level above the actual defect is how the bundle talked itself past it.
