# ADR-0187 at-rest posture — EXECUTION lens audit

Worktree: /private/tmp/wt-0187-execution (detached at ebafdf0f, bundle present — step 0 PASS).
Started 2026-08-22. Findings appended as they are established; NOTHING is fixed here.

## X1 — CRITICAL — MySQL's `wrkflw_journal` column is named `trigger_`, not `trigger`. The column-NAME set is NOT dialect-invariant, the union is 88 keys not 87, and the plan's completeness test cannot go green as written.

**Claims under attack (three, all load-bearing):**

- `docs/specs/2026-08-22-adr-0187-measurements.md` E4 heading: *"E4 — Column census: 79 `wrkflw_*` columns, **identical across all three dialects**"*
- `docs/adr/0187-at-rest-classification-is-machine-checked.md` D4: *"**87** = 79 `wrkflw_*` columns (present in every dialect) + 8 `casbin_rule` columns"*
- `docs/plans/2026-08-22-at-rest-posture.md` Task 3 step 1: `assert.Len(t, atrest.Classification, 87, "87 = 79 wrkflw_* + 8 casbin_rule (E6)")`, over an `inSchema` built as the **union across all dialects**.

**Probe — an independent parser (python3, balanced-paren + depth-aware split, deliberately NOT the author's awk), comparing column NAME SETS rather than counts:**

```
=== postgres: tables=9 columns=79   (per-table 9/6/12/4/3/13/8/7/17)
=== mysql:    tables=9 columns=79   (per-table 9/6/12/4/3/13/8/7/17)
=== sqlite:   tables=9 columns=79   (per-table 9/6/12/4/3/13/8/7/17)
=== casbin:   tables=1 columns=8

=== name-set differences across dialects ===
wrkflw_journal pg-my {'trigger', 'trigger_'} pg-sq set()
```

**Source confirmation:**

```
$ grep -n "trigger" .../mysql/0001_init.sql .../postgres/0001_init.sql .../sqlite/0001_init.sql
mysql/0001_init.sql:10:-- payload column is named "trigger_" because "trigger" is a reserved word
mysql/0001_init.sql:31:    trigger_    JSON         NOT NULL,
postgres/0001_init.sql:30:    trigger     JSONB       NOT NULL,
sqlite/0001_init.sql:34:-- "trigger" is NOT reserved in SQLite (unlike MySQL), so the column keeps the
sqlite/0001_init.sql:40:    trigger     TEXT    NOT NULL,

$ grep -rn "JournalTriggerColumn" --include='*.go' .
internal/persistence/dialect/dialect.go:73:   JournalTriggerColumn() string
internal/persistence/dialect/postgres.go:74:  func (postgres) JournalTriggerColumn() string { return "trigger" }
internal/persistence/dialect/sqlite.go:70:    func (sqliteDialect) JournalTriggerColumn() string { return "trigger" }
internal/persistence/dialect/mysql_test.go:184: assert.Equal(t, "trigger_", d.JournalTriggerColumn())

$ sed -n '362,364p' internal/persistence/store/store_core.go
// writeJournal inserts one row into wrkflw_journal on q. ... The payload column name
// is dialect-specific ("trigger" on PG/SQLite, "trigger_" on MySQL).
```

**And the repo already had to solve exactly this, in the very file E12 says this delivery reuses:**

```
$ grep -n "trigger" internal/persistence/store/migration_parity_test.go
39:// wrkflw_journal payload column "trigger_" because "trigger" is a reserved word
40:// in MySQL SQL syntax. Postgres and SQLite use "trigger". The dialect interface
41:// already surfaces this via JournalTriggerColumn(). We rename "trigger_" ->
42:// "trigger" in the MySQL schema before the final equality assertion so that the
67:  // Normalize reserved-word column rename: MySQL uses "trigger_" for the
103:  mysqlCol := dialect.NewMySQL().JournalTriggerColumn()        // "trigger_"
104:  canonicalCol := dialect.NewPostgres().JournalTriggerColumn() // "trigger"
```

**What it proves.**

1. **E4's headline quantifier is FALSE.** The per-table and per-dialect **counts** are identical (79/79/79) — which is exactly why the author's count-based census could not see it. The column **sets** are not identical. This is the count-hides-the-difference pathology, in the one document whose entire subject is enumerations rotting.
2. **The union of `table.column` keys across dialects is 80 `wrkflw_*` + 8 `casbin_rule` = 88, not 87.** `wrkflw_journal.trigger` (pg+sqlite) and `wrkflw_journal.trigger_` (mysql) are two distinct `ColumnKey`s under D3's own `table.column` keying rule.
3. **The plan's Task 3 `TestClassificationCoversTheSchemaExactly` CANNOT go green as written.** Its `inSchema` is the union over `schemas`; the spec's classification section lists `trigger` only. The run yields `unclassified = ["wrkflw_journal.trigger_"]`, so `assert.Empty(t, unclassified)` fails — while `assert.Len(t, atrest.Classification, 87)` passes. The implementing agent will hit this at Task 3 step 4 and will be forced to either add an 88th entry (contradicting every "87" in ADR D4, spec D4, the plan, and E6) or to normalize (an unstated design decision).
4. **Task 2's `TestLoadSchemas_ColumnCensus` still PASSES** (`schemas["mysql"].Columns` is 79). This is the narrow-fixture pathology named in my brief: the count-shaped test is green while the key-shaped one is red, so the bundle's own acceptance test for the parser gives false assurance.
5. **E6's "EXACT MATCH: yes"/`comm` result is therefore also suspect** — it was computed against a schema dump that either normalized `trigger_` or was Postgres-only. Either way it did not exercise the union the plan's test builds.
6. **D2's justification is incomplete as stated.** *"The **entire** 19-column divergence is the timestamp mapping"* is true of the **type** divergence; the bundle nowhere records that a **name** divergence also exists. D2's claim that classification "is dialect-invariant" holds for the *role* but not for the *key*, and `map[ColumnKey]Class` encodes the key.

**Proposed fix (concrete).**

- Add a normalization step to `LoadSchemas`, mirroring `migration_parity_test.go:67`: canonicalize `wrkflw_journal.trigger_` → `wrkflw_journal.trigger` using `dialect.NewMySQL().JournalTriggerColumn()` / `dialect.NewPostgres().JournalTriggerColumn()` rather than a hardcoded string pair, so the two stay coupled. Keep **87** and record the normalization as a stated rule with a comment naming this finding — exactly the shape D8/E15 chose for the FK exclusion.
- Add a **fourth parser trap** to Task 1 step 5's table: *"a dialect that renames a reserved-word column must normalize to the canonical name"*, with a fixture declaring `trigger_` in one dialect and `trigger` in another and asserting one merged key.
- Correct E4's heading and body: the **counts** are identical across dialects; the **name sets** differ in exactly one column, which is normalized.
- Add to Task 2's `TestLoadSchemas_ColumnCensus` a **key-set** assertion (`ElementsMatch` over sorted `table.column` strings for each dialect pair), not only `assert.Len`. The length assertion is what missed this.
- The generated `SECURITY.md` row for `wrkflw_journal.trigger` must state the MySQL physical name, or a consumer applying column encryption on MySQL will target a column that does not exist.

## (non-finding) E5 SURVIVES a stronger fixture than the author used

E5 (the evidence item claiming 48 TEXT-ish columns on postgres/mysql, 67 on sqlite, the whole
19-column gap being the `TIMESTAMPTZ→TEXT` mapping) reproduces, and it survives the fixture
variation the author did not run — a **set** comparison rather than a count comparison:

```
pg textish: 48 my: 48 sq: 67
in pg not my: []
in my not pg: []
in pg not sq: []
in sq not pg (expect the 19 timestamps): 19
['wrkflw_call_links.claimed_at', 'wrkflw_call_links.created_at', 'wrkflw_call_links.notified_at',
 'wrkflw_chain_links.created_at', 'wrkflw_definitions.created_at', 'wrkflw_human_task.claimed_at',
 'wrkflw_human_task.completed_at', 'wrkflw_human_task.created_at', 'wrkflw_human_task.due_at',
 'wrkflw_instances.ended_at', 'wrkflw_instances.started_at', 'wrkflw_instances.updated_at',
 'wrkflw_journal.applied_at', 'wrkflw_journal.occurred_at', 'wrkflw_outbox.created_at',
 'wrkflw_outbox.next_attempt_at', 'wrkflw_outbox.published_at',
 'wrkflw_processed_message.processed_at', 'wrkflw_timers.next_run']
```

Postgres histogram also reproduces exactly as E5 states it (36 TEXT + 12 JSONB + 19 TIMESTAMPTZ +
6 INT + 3 SMALLINT + 2 BIGINT + 1 BIGSERIAL = 79). The 19 names above are **identical** to the spec's
19 `timestamp`-class columns. MySQL's 48 is a genuinely identical *set*, not a coincidence of totals
(mysql composition: 30 `VARCHAR(255)` + 12 `JSON` + 3 `TEXT` + 2 `VARCHAR(50)` + 1 `VARCHAR(64)`).
**No finding.** D2's dialect-invariance justification stands on the *type* axis. (It does not stand
on the *name* axis — that is X1.)

## X2 — MAJOR — the `keyed` total is 27 on Postgres but **28 on MySQL and SQLite**, and the single most actionable sentence bound for `SECURITY.md` is generalized from a one-dialect measurement — the exact error the ADR's own Context indicts

**Claims under attack:**

- `docs/adr/0187-...md` D8: *"Measured on Postgres: **27 of 79** columns are keyed, of which **zero are `freeform` and zero are `policy`** — **so all 11 free-form columns and both policy locations can be encrypted without breaking an index**"*
- `docs/specs/2026-08-22-at-rest-posture.md` § "What `SECURITY.md` gains": *"the two sentences that fall out of the classification and are the actionable content: **every `freeform` and `policy` column is index-free and can be encrypted without breaking an index**"* — stated with **no dialect qualifier**, and this is the text that gets published to consumers.
- `docs/plans/...md` Task 5: the only prescribed keyed assertions are `TestKeyedLowerBound_Postgres` (postgres only) and `TestKeyedIsDialectDependent` (pins exactly one column, `wrkflw_outbox.status`).

**Probe.** Independent key derivation (PK membership incl. inline `col TYPE PRIMARY KEY`, table-level
`PRIMARY KEY(...)`, `UNIQUE`, MySQL inline `INDEX name (cols)`, `CREATE INDEX` column lists, and
partial-index `WHERE` predicate columns filtered to real columns of that table), run per dialect:

```
postgres 27
mysql 28
sqlite 28

mysql - postgres:    ['wrkflw_call_links.claimed_at [timestamp]', 'wrkflw_call_links.notified_at [timestamp]']
postgres - mysql:    ['wrkflw_instances.ended_at [timestamp]']
sqlite - postgres:   ['wrkflw_call_links.claimed_at [timestamp]', 'wrkflw_call_links.notified_at [timestamp]']
postgres - sqlite:   ['wrkflw_instances.ended_at [timestamp]']

freeform/policy keyed anywhere?:
   postgres NONE
   mysql NONE
   sqlite NONE
```

My postgres derivation matches **E9 exactly** — 27 distinct keyed columns, `byClass` = `reference` 15,
`scalar` 7, `timestamp` 4, `actor` 1, the single keyed `actor` being
`wrkflw_human_task.claimed_by`. That agreement is what makes the mysql/sqlite 28 trustworthy.

**Mechanism of the delta.** MySQL and SQLite have no partial indexes, so
`wrkflw_call_links_pending_idx` is `ON wrkflw_call_links (status, notified_at, claimed_at, child_instance_id)`
— folding two extra `timestamp` columns into the key — while Postgres keeps
`ON wrkflw_call_links (child_instance_id) WHERE status IN ('completed','failed')`. Conversely
Postgres's `wrkflw_instances_status_idx ... WHERE ended_at IS NULL` makes `ended_at` a predicate
column that MySQL/SQLite do not key at all. Net: `timestamp` keyed is 4 on pg, **5** on mysql/sqlite.

**What it proves.**

1. **The bare number `27` is a Postgres number.** D8 does label it "Measured on Postgres", so it is
   not false — but the bundle never records the other two, and `keyed` is the one annotation D8
   itself says is dialect-dependent. E10 gives per-dialect *index counts* and stops; there is no
   per-dialect *keyed-column* count anywhere in the bundle.
2. **The actionable sentence is generalized from one dialect.** "every `freeform` and `policy` column
   is index-free" is published unqualified. I verified it **does** hold on all three (`NONE` above) —
   but the bundle derived it on Postgres alone. This is structurally the same move the ADR's own
   Context condemns: *"'48 free-form columns' is a Postgres number; SQLite has 67 — the single-dialect
   blind spot appearing inside the very number meant to fix it."* It reappears one decision later.
3. **Nothing in the plan pins it on MySQL or SQLite.** Task 5's `byClass` equality — described in the
   plan as *"the 'zero freeform, zero policy' claim expressed as an equality rather than as prose, so
   it cannot rot"* — is asserted for **postgres only**. A future MySQL-only index on
   `wrkflw_human_task.vars` (`freeform`) would leave every prescribed test green while `SECURITY.md`
   published a false safety claim. The claim CAN rot, on two of three dialects.

**Proposed fix.**

- Add the two missing measurements to E9/E10: **keyed = 27 (postgres), 28 (mysql), 28 (sqlite)**, with
  the `wrkflw_call_links.{notified_at,claimed_at}` / `wrkflw_instances.ended_at` delta and its
  partial-index mechanism, so the bundle states the number it will publish per dialect.
- Change Task 5's `TestKeyedLowerBound_Postgres` into a **table test over all three dialects** (per
  the `table-test` skill's `assert` closure form), asserting per-dialect totals 27/28/28 and the
  per-dialect `byClass` map. Rename it `TestKeyedLowerBound` — the `_Postgres` suffix is what makes
  the single-dialect scope look intentional rather than incomplete.
- Add one dialect-independent assertion that is the real safety claim, so it cannot rot anywhere:
  `for each dialect: assert.Empty(columns whose class is freeform or policy and len(Keys) > 0)`.
- Make the generated `SECURITY.md` sentence per-dialect, or state it as "on every dialect this module
  ships migrations for", backed by the assertion above.

## X3 — (confirmation, folded into X1) the spec's 87-row classification is name-exact ONLY under the unstated `trigger_` normalization

Probe: the spec's § "The classification" transcribed into a map and `comm`-diffed both ways against
the parsed schema union:

```
spec classification entries: 87
spec per-class: {'reference': 27, 'scalar': 17, 'freeform': 11, 'timestamp': 19, 'actor': 5, 'policy': 8}
real distinct table.column (normalized): 87
in spec not schema: []
in schema not spec: []
```

The six per-class counts in D4 are **correct**, and the name set matches **exactly** — but only
because my probe applied `trigger_ → trigger`. Remove that one line and the schema union is 88 and
`in schema not spec: ['wrkflw_journal.trigger_']`. This is the positive control for X1: the spec's
enumeration is right, and the *guard the plan prescribes for it* fails against the real schema.

## X4 — CRITICAL — `ClassDivergences(schemas, Classification)` is **empty by construction for every possible input**. The D2 dialect-invariance pin is the eighth test in this repo that cannot fail, and D6 guarantee 4 is unimplementable as designed.

**Claims under attack:**

- `docs/adr/0187-...md` D6: *"the guard fails on … **class divergence** … **a column whose class differs across dialects** fails"*
- `docs/specs/2026-08-22-at-rest-posture.md` D6 item 4: *"any column's **class** differs across dialects (the D2 invariance pin)"*
- `docs/specs/...md` § Falsifiability: *"⚠ **The invariance pin (D2) is a PIN, not a RED-green fix**, because **the property already holds**."*
- `docs/plans/...md` Task 4 step 1: `assert.Empty(t, atrest.ClassDivergences(schemas, atrest.Classification), ...)`
- `docs/plans/...md` Task 4 step 2: *"`ClassDivergences(schemas map[string]Schema, cls map[ColumnKey]Class) []string` **returns one string per column whose class is not identical across every dialect that declares it**."*

**Probe — this one is settled by the signature, so the "probe" is a derivation over the prescribed
type, executed as a Go program rather than argued:**

Given `cls map[ColumnKey]Class` — **one** flat map, per D3 keyed on `table.column` — the class of a
column `k` is `cls[k]`. That expression contains no dialect term. For every dialect `d` that declares
`k`, the looked-up class is the identical value `cls[k]`. Therefore "the set of columns whose class is
not identical across every dialect that declares it" is **the empty set for every `schemas` argument
and every `cls` argument**. There is no input — no migration edit, no added column, no dropped column,
no dialect rename — that makes `ClassDivergences(schemas, Classification)` non-empty.

Corroborating execution — I ran the plan's **own** Task 4 step 5 mutation in this worktree
(`note` → `note_text` in sqlite only; the `sed` matches, `grep -c note_text` = **1**, see X5) and
computed what the guards see:

```
AFTER MUTATION (union semantics, plan Task 3 step 1):
  unclassified: ['wrkflw_human_task.note_text', 'wrkflw_journal.trigger_']
  stale       : []
```

The mutation produces an **unclassified** column. It produces **no class divergence**, because
`note_text` is declared by one dialect only and `note` is declared by two dialects that read the same
map entry. The step the plan calls *"the step that makes the pin worth having"* exercises Task 3's
completeness guard, not Task 4's pin.

**What it proves.**

1. **`TestNoColumnChangesClassAcrossDialects` cannot fail.** Not "passes because the property holds" —
   passes because the predicate is unsatisfiable over the prescribed types. The spec's falsifiability
   table attributes the green to *"the property already holds"*, which is the wrong diagnosis and is
   what let it through. Per CLAUDE.md: *"a mutation that cannot discriminate is evidence the CLAIM is
   wrong, not that the test is weak"*.
2. **The liveness guard proves the wrong thing.** `TestClassDivergenceDetectorFires` calls
   `ClassDivergencesPerDialect(schemas, perDialect)` with a **per-dialect classification function**,
   and its own fixture comment says *"A per-dialect classification, **which the production one is NOT
   allowed to be**"*. So the guard demonstrates that the detector fires on an input shape production
   is architecturally forbidden from producing. That is the repo's recorded *"a CITED test is not a
   COVERING test"* failure in its purest form — and the plan's closing "Known gap" note sees the
   two-function split but frames it as a code-path-sharing concern, not as vacuity.
3. **D6's fourth guarantee does not exist.** The ADR promises the build fails when a column's class
   differs across dialects. With one flat map, that state is unrepresentable, so nothing can fail on
   it. The ADR is promising behaviour nobody can build — the ADR-0162 zombie-scope shape.
4. **It is not merely harmless.** X1 shows the schema *does* contain a per-dialect column-name
   divergence (`trigger` / `trigger_`). A design that cannot represent per-dialect classification also
   cannot express the one real dialect asymmetry in the schema, which is why X1 surfaces as an
   unclassified column rather than as a divergence.

**Proposed fix — pick one, but D6 guarantee 4 must either become real or be deleted from the ADR:**

- **(a) Delete the guarantee.** Drop D6 item 4, drop `ClassDivergences`, drop Task 4 step 1, and state
  in D2 that dialect-invariance is enforced *structurally* — a single flat `map[ColumnKey]Class` makes
  divergence unrepresentable — rather than *by a test*. Keep `ClassDivergencesPerDialect` only if a
  per-dialect classification is actually introduced. This is the honest minimum and it is cheap.
- **(b) Make it real.** Have `LoadSchemas` produce a per-dialect classification view and have
  `ClassDivergences` compare **the schema's own evidence** across dialects rather than the map's — e.g.
  assert that the set of `table.column` keys is identical across dialects (which is the invariant that
  can genuinely fail, and which X1 shows **fails today**). That converts a vacuous pin into the test
  that would have caught `trigger_`.
- Either way, **replace Task 4 step 5's mutation** with one that exercises whatever the guard actually
  reads (see X5), and record the *observed* failure text, not the predicted one.

## X5 — MAJOR — Task 4 step 5 prescribes a mutation whose stated expected output is impossible: the staleness guard cannot fire, because `inSchema` is the UNION across dialects

**Claim under attack** — `docs/plans/2026-08-22-at-rest-posture.md` Task 4 step 5:

> Expected: `MUTATION EXIT=1` with **`wrkflw_human_task.note_text` unclassified** and
> **`wrkflw_human_task.note` stale** — i.e. the completeness and staleness guards **both** fire.

**Probe.** Ran the prescribed `sed` verbatim in this worktree, verified it applied, then computed both
guard outputs under the plan's own Task 3 step 1 semantics (`inSchema` = union over `schemas`):

```
$ sed -i '' 's/^    note         TEXT,/    note_text    TEXT,/' .../sqlite/0001_init.sql
$ grep -c 'note_text' .../sqlite/0001_init.sql
1                      <- the sed DOES match; the mutation is applicable
$ grep -n "note" .../sqlite/0001_init.sql
131:-- ({actor, timestamp} / {actor, timestamp, outcome?, note?}); NULL means the
145:    note_text    TEXT,

AFTER MUTATION (union semantics, plan Task 3 step 1):
  unclassified: ['wrkflw_human_task.note_text', 'wrkflw_journal.trigger_']
  stale       : []
$ cp /tmp/sqlite_init.bak .../sqlite/0001_init.sql && diff ... && echo RESTORED CLEAN
RESTORED CLEAN
```

**What it proves.** The `sed` is fine — my brief's hypothesis that it might not match is **refuted**,
recorded here as a false positive rather than skipped. But `wrkflw_human_task.note` is still declared
by Postgres and MySQL, so it remains in the union, so `!inSchema[k]` is false and it is **never**
reported stale. Only the completeness guard fires. An agent following the plan is told to expect an
observation that cannot occur — and this repo's recorded failure mode is that the predicted text gets
copied into the `▶ Progress` block as though observed.

Note the second row: `wrkflw_journal.trigger_` appears unclassified **with or without** the mutation
(X1), which will make the mutation's failure output ambiguous — two unclassified columns, one of them
pre-existing — and could easily be read as "the mutation fired" when half of it is X1.

**Proposed fix.**

- Correct the expected text to: *"`MUTATION EXIT=1`, `unclassified = [wrkflw_human_task.note_text]`,
  `stale = []` — only the completeness guard fires, because `note` survives in the union via Postgres
  and MySQL."*
- To exercise the **staleness** guard, prescribe a **separate** mutation that removes the column from
  **all three** dialects (or, cheaper and hermetic, a unit test that calls the guard's exported
  comparison helper with a fabricated schema missing one classified key — no file mutation at all).
- Fix X1 first, or this mutation's output is contaminated by a pre-existing unclassified column.

## X6 — MAJOR — "dbtest skips when unavailable" is FALSE: `dbtest` has no skip path at all, it fails. And the SQLite cross-check — the only Docker-free one — is trapped inside the Docker-requiring test function.

**Claim under attack** — `docs/plans/2026-08-22-at-rest-posture.md` Task 7 step 1, in the prescribed
test body:

```go
	// Postgres + MySQL: Docker-gated; dbtest skips when unavailable.
	pool := dbtest.RunTestDatabase(t)
```

**Probe.**

```
$ grep -rn "t\.Skip\|Skipf\|SkipNow" internal/dbtest/
internal/dbtest/dbname_test.go:39:  t.Skipf("armed only by the child process of TestPerTestDatabaseNames... ")
internal/dbtest/dbname_test.go:61:  t.Skip("already inside the child process; ...")
```

Both hits are in `dbname_test.go` — dbtest's own tests. **No production dbtest helper contains a skip.**
The failure path is an assertion:

```
$ sed -n '218p' internal/dbtest/postgres.go
	require.NoError(t, sharedErr)          <- inside sharedBase(t), reached by RunTestDatabase
$ sed -n '188,192p' internal/dbtest/postgres.go
		container, err := startContainer(ctx, defaultDBName, defaultUser, defaultPassword)
		if err != nil {
			sharedErr = fmt.Errorf("start shared postgres container: %w", err)
```

`require.NoError` calls `t.FailNow`. With no Docker daemon the test **FAILS**; it does not skip.
(Docker probe on this machine: `docker UP` — I did **not** start a container, per my brief.)

**What it proves.**

1. **A false claim about current behaviour has entered the plan**, and it is a claim in *prescribed
   code that will be committed as a comment* — the exact category the Delivery Gate item 2 sweep is
   supposed to catch and that this repo has shipped repeatedly (13 false comments in one delivery).
2. **The design intent behind D7 is partly defeated.** ADR-0187's accepted costs say *"On a machine
   with no Docker daemon, a parser bug and a classification bug are indistinguishable."* The SQLite
   cross-check needs **no Docker** and could close a third of that gap — but the plan puts SQLite,
   Postgres and MySQL in **one test function**, whose Postgres call hard-fails without a daemon. There
   is no Docker-free green cross-check available on such a machine, even though one is trivially
   constructible.
3. It also means the plan's Task 7 step 4 instruction *"Confirm with `-v` that the subtests **ran**
   rather than skipped. A `dbtest` skip is not a pass"* is guarding against a state that cannot occur,
   while the state that *can* occur (a hard failure that looks like a parser bug) is unaddressed.

**Proposed fix.**

- Delete the false comment. Replace with: *"Requires a running Docker daemon; `dbtest` fails (does not
  skip) without one — see `internal/dbtest/postgres.go:218`."*
- **Split Task 7 into two test functions**: `TestAtRestParseMatchesLiveIntrospection_SQLite`
  (pure-Go, no Docker, runs in the container-free subset) and
  `TestAtRestParseMatchesLiveIntrospection_PostgresMySQL` (Docker). Then a Docker-less contributor gets
  one third of D7's assurance instead of none, and the ADR's accepted-cost paragraph can be softened
  accurately.
- Update ADR-0187's "Bad / accepted costs" bullet to say the SQLite arm is Docker-free, once split.

## X7 — MAJOR — Task 7 hand-rolls SQLite setup that `dbtest.RunTestSQLite(t)` already provides, violating CLAUDE.md Golang rule #3 — inside the bundle whose E12 exists to stop exactly this

**Claim under attack** — `docs/plans/...md` Task 7 step 1:

```go
	// SQLite: pure Go, no container.
	sqliteDB, err := sql.Open("sqlite", "file:atrest?mode=memory&cache=shared")
	require.NoError(t, err)
	sqliteDB.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = sqliteDB.Close() })
	sm, err := store.NewSQLiteMigrator(sqliteDB)
	require.NoError(t, err)
	require.NoError(t, sm.Up(ctx))
```

**Probe.**

```
$ grep -rn "^func RunTest" internal/dbtest/*.go
internal/dbtest/postgres.go:127:func RunTestDatabase(t *testing.T, opts ...TestOption) *pgxpool.Pool
internal/dbtest/mysql.go:168:func RunTestMySQL(t *testing.T) *sql.DB
internal/dbtest/mysql.go:200:func RunTestMySQLDSN(t *testing.T) string
internal/dbtest/sqlite.go:22:func RunTestSQLite(t *testing.T) *sql.DB

$ sed -n '14,40p' internal/dbtest/sqlite.go
// RunTestSQLite opens a fresh file-backed SQLite database in t.TempDir(),
// configures it with WAL journal mode, a 5 000 ms busy timeout, and foreign-key
// enforcement, applies all wrkflw SQLite migrations via [store.MigrateSQLite],
// and returns the *sql.DB torn down via t.Cleanup.
// ... SetMaxOpenConns(1) enforces single-writer access ...
```

**What it proves.** All seven prescribed lines are `dbtest.RunTestSQLite(t)` — including the
`SetMaxOpenConns(1)` and the `t.Cleanup` close, and the migration is already applied inside it, so
`NewSQLiteMigrator` + `Up` is a redundant second application. CLAUDE.md Golang rule #3 is explicit:
*"For database tests, use the shared `internal/dbtest` helpers — … `dbtest.RunTestSQLite(t)`"*, and
the `use-testcontainers` skill says *"call the existing helper directly — do not spin up your own …
or write a parallel helper."*

Two further defects in the same eight lines:

- The prescribed code has **no blank import of `modernc.org/sqlite`**, so `sql.Open("sqlite", …)` would
  fail at runtime with *unknown driver "sqlite"* unless something else in `store_test` registers it.
  `dbtest/sqlite.go` carries `_ "modernc.org/sqlite" // register "sqlite" driver` precisely for this.
- `"file:atrest?mode=memory&cache=shared"` is a **shared-cache in-memory** DSN — a process-global name.
  Two tests in the package using it would share one database. `RunTestSQLite` uses `t.TempDir()`
  file-backed isolation instead.

This is the ADR-0186 lesson repeating verbatim: *"FOUR times it claimed a gap the repo had already
filled ⇒ search the repo for an existing convention BEFORE writing a new symbol"* — and it lands in the
bundle whose **E12** section is titled *"Repo conventions this delivery reuses rather than reinvents"*
and which lists `migration_parity_test.go` but not `dbtest/sqlite.go`.

**Proposed fix.** Replace the eight lines with `sqliteDB := dbtest.RunTestSQLite(t)` and delete the
`store.NewSQLiteMigrator` call. Add `dbtest.RunTestSQLite` to E12's list of reused conventions.

## X8 — CRITICAL — the glob ADR-0187 decision 1 is built on, `**/migrations/*.sql`, matches **1 of the 4** migration files. It finds `casbin_rule` and MISSES all three dialect schemas — the fourth-directory failure, inverted.

**Claims under attack (the ADR's and the spec's central mechanism, stated identically in both):**

- `docs/adr/0187-...md` D1: *"**1. Migrations are discovered by glob (`**/migrations/*.sql`), never listed.** A hardcoded directory list is what lost `casbin_rule`."*
- `docs/specs/2026-08-22-at-rest-posture.md` D1: *"The schema walk globs `**/migrations/*.sql` from the module root. It does not hardcode a directory list."*
- `docs/plans/...md` header: *"parses every **discovered** migration file … glob `**/migrations/*.sql`"* (File-structure table, `discover.go` row).

**Probe — run the literal glob, both plausible readings:**

```
$ shopt -s globstar; ls -1 **/migrations/*.sql
internal/authz/casbin/migrations/0001_casbin_rule.sql

$ python3 -c "import glob; print(glob.glob('**/migrations/*.sql', recursive=True))"
['internal/authz/casbin/migrations/0001_casbin_rule.sql']
COUNT: 1

$ python3 -c "import glob; print(glob.glob('**/migrations/*/*.sql', recursive=True))"
internal/persistence/store/migrations/mysql/0001_init.sql
internal/persistence/store/migrations/postgres/0001_init.sql
internal/persistence/store/migrations/sqlite/0001_init.sql
```

Ground truth (E1 reproduces):

```
$ find . -type d -name migrations -not -path './.git/*'
./internal/persistence/store/migrations
./internal/authz/casbin/migrations
```

The three dialect schemas live **one level deeper** — `migrations/<dialect>/*.sql` — so the prescribed
glob cannot reach them. `internal/persistence/store/migrations` itself contains **no** `.sql` file.

**What it proves.**

1. **The decision's stated mechanism is wrong, and wrong in the funniest possible direction.** D1 exists
   because a hardcoded three-directory list lost the *casbin* set. The replacement glob finds *only*
   the casbin set and loses the three the old list had. Implemented literally, the census would be **8
   columns, not 87**, and `TestClassificationCoversTheSchemaExactly` would report 79 stale entries.
2. **The evidence never checked it.** E1's probe is `find . -name '*.sql' | sort` — a *different and
   broader* method than the design prescribes. So the measurement that "confirms F5" confirms that four
   files exist; it does **not** confirm that D1's discovery rule finds them. Premise Discipline's
   "prescribed tests must be falsifiable" has a mirror image here: a *prescribed mechanism* must be
   executed, not just its motivation.
3. **The plan silently diverges from the ADR rather than correcting it.** Task 2 step 3 prose says
   `DiscoverMigrationDirs` returns *"every directory named `migrations` **or whose parent is named
   `migrations`**, containing at least one `.sql` file"* — which is correct and finds all four (I
   verified: the two-`migrations`-dir + three-child structure yields exactly the four E1 lists). But
   the plan never says the ADR's glob is wrong, so the durable record (the ADR) ships a false
   mechanism while the disposable one (the plan) has the right one. Per rule #11 the ADR is what must
   be amended.
4. **`TestDiscoverMigrationDirs_FindsAllFourAndAllAreDeclared` would still pass** with the plan's rule,
   so no prescribed test catches the ADR/plan divergence.

**Proposed fix.**

- Amend ADR-0187 decision 1 and spec D1 to state the **actual** rule, not a glob that does not work:
  *"Migration sets are discovered by walking the module root for directories named `migrations` and
  their immediate children, keeping those that contain at least one `.sql` file — never by a hardcoded
  list. Note the two-level shape: `internal/authz/casbin/migrations/*.sql` and
  `internal/persistence/store/migrations/<dialect>/*.sql`, so a single `**/migrations/*.sql` glob finds
  only the first."* Naming the trap in the ADR is what stops the next reader "simplifying" it back.
- Add to E1 an executed probe of the **discovery rule itself** (not just a `find` of `.sql` files),
  showing it returns the four directories.
- Add a negative case to `TestDiscoverMigrationDirs_...`: assert
  `internal/persistence/store/migrations` (the `.sql`-free parent) is **not** returned, so the
  "contains at least one `.sql`" clause is pinned rather than incidental.

## X9 — CRITICAL — **`casbin_rule.ptype` is a `policy` column and it IS indexed.** The bundle's single most actionable published sentence — "zero `policy` columns are keyed / both policy locations can be encrypted without breaking an index" — is FALSE, and the guard prescribed to stop it from rotting `continue`s past the exact table that refutes it.

**Claims under attack (all five say the same false thing; the last one is the one consumers read):**

- `docs/specs/2026-08-22-adr-0187-measurements.md` E9: *"⭐ **Zero `freeform` and zero `policy` columns are keyed.** All 11 free-form columns and **both policy locations** are index-free in Postgres."*
- `docs/adr/0187-...md` D8: *"**27 of 79** columns are keyed, of which **zero are `freeform` and zero are `policy`** — so all 11 free-form columns and **both policy locations can be encrypted without breaking an index**"*
- `docs/adr/0187-...md` Consequences/Good: *"Two non-obvious, actionable facts reach the consumer as generated output …: **every `freeform` and `policy` column is index-free**"*
- `docs/specs/2026-08-22-at-rest-posture.md` D8: *"⭐ **zero `freeform` and zero `policy` columns carry an index**, so all 11 free-form columns and **both policy locations can be encrypted without breaking a single index**"*
- `docs/specs/...md` § "What `SECURITY.md` gains": *"the two sentences that fall out of the classification and **are the actionable content**: **every `freeform` and `policy` column is index-free and can be encrypted without breaking an index**"* ← **this is generated into `SECURITY.md` and shipped to consumers.**

**Probe — read the fourth migration file the whole bundle exists to stop losing:**

```
$ cat internal/authz/casbin/migrations/0001_casbin_rule.sql
-- +goose Up
CREATE TABLE casbin_rule (
    id    BIGSERIAL PRIMARY KEY,
    ptype TEXT NOT NULL,
    v0    TEXT NOT NULL DEFAULT '',
    ...
    v5    TEXT NOT NULL DEFAULT ''
);
CREATE INDEX casbin_rule_ptype_idx ON casbin_rule (ptype);      <-- HERE

-- +goose Down
DROP TABLE casbin_rule;
```

Independent key derivation over **all four** migration sets:

```
casbin_rule keyed columns:
    casbin_rule.id    ['PK']    -> class: scalar
    casbin_rule.ptype ['index'] -> class: policy

POLICY-class columns that ARE keyed, across the WHOLE schema (all 4 migration sets):
   casbin: casbin_rule.ptype ['index'] -> policy

TOTAL distinct table.column keyed in a POSTGRES DEPLOYMENT (79 wrkflw + 8 casbin):
    29
```

`ptype` is classified `policy` by the bundle's own D4 (*"**`casbin_rule`** (8) — scalar: `id` · policy:
`ptype`, `v0`, `v1`, `v2`, `v3`, `v4`, `v5`"*).

**Why the author's probe missed it — and why the prescribed guard would have missed it too.**

E9's own heading scopes the derivation to *"27 of **79 Postgres columns**"* — the `wrkflw_*` census.
`casbin_rule` was excluded from the measurement, and the conclusion was then stated over **both** policy
locations, one of which was outside the measurement. And the plan hard-codes that exclusion into the
test:

```go
// docs/plans/2026-08-22-at-rest-posture.md, Task 5 step 1
	for k, col := range schemas["postgres"].Columns {
		if k.Table == "casbin_rule" {
			continue // not part of the 79-column wrkflw_* census (E4)
		}
```

The plan describes that very test as *"the 'zero freeform, zero policy' claim expressed as an equality
rather than as prose, **so it cannot rot**"*. It cannot rot because it `continue`s past its own
counterexample. This is the narrow-fixture failure in its purest form: the probe passes, the premise is
false, and execution alone does not catch it unless the fixture is varied — which is what this lens did.

**What it proves, and the concrete harm.**

1. **The published sentence is false.** A `policy` column *is* keyed. "Both policy locations can be
   encrypted without breaking an index" is wrong about the location that the ADR's own Context calls the
   more sensitive one.
2. **The harm is the exact harm the spec opens by naming.** The spec's premise: *"a consumer who
   encrypts the columns we name and leaves the rest in the clear **has been harmed by our
   documentation**."* A consumer who reads the generated `SECURITY.md`, encrypts `casbin_rule.ptype`
   non-deterministically, and deploys, silently disables `casbin_rule_ptype_idx` — the index on the
   discriminator column casbin filters every policy load by. That is a production authorization
   hot-path regression caused directly by our generated document.
3. **It reproduces the delivery's own founding defect one layer down.** ADR-0187 exists because a prior
   enumeration *"walked three migration directories; there are four"*. This bundle discovers the fourth
   directory, classifies its columns — and then **excludes it from the derivation whose conclusion it
   publishes**. The fourth-directory blind spot survived the fix, relocated from *discovery* into
   *derivation*.
4. **`27` is also incomplete as the headline number.** In a Postgres deployment using `FromDB`, the
   keyed count is **29 of 87**, not 27 of 79. The bundle never states a whole-schema figure.
5. **The ADR contradicts itself about this table's classes.** Context calls `casbin_rule`
   *"a tenth table with **seven free-form `TEXT` columns**"* while D4 classes all seven `policy` and
   zero `freeform`. In a document whose deliverable is a class literally named `freeform`, that is a
   defect, not loose prose.

**Proposed fix (all of these; this one is not cosmetic).**

- **Correct E9, ADR D8, spec D8 and the ADR's Consequences bullet.** The true statements are:
  *"All 11 `freeform` columns are index-free on every dialect. **Of the 8 `policy` columns, 7 are
  index-free and `casbin_rule.ptype` is indexed** (`casbin_rule_ptype_idx`); `wrkflw_human_task.eligibility`
  is index-free."* Give it the same ⭐ treatment `wrkflw_human_task.claimed_by` gets, because it is the
  same shape of finding: an at-rest-sensitive column you cannot blindly encrypt.
- **Delete the `if k.Table == "casbin_rule" { continue }` from Task 5's test** and assert the whole
  parsed schema. If a per-table split is wanted, assert both the `wrkflw_*` subset **and** the total —
  never only the subset that yields the desired answer.
- **Replace the prose invariant with a derived one** so it cannot be wrong again:
  `assert.Empty(t, columnsWhere(class ∈ {freeform, policy} && len(Keys) > 0))` — over **every** dialect
  (X2) and **every** table (this finding). Today that assertion **FAILS**, which makes it a genuine RED
  and the strongest test in the delivery. The generated sentence must then be rendered *from* the
  derivation, not typed as a constant, or it will disagree with the table beneath it.
- **Fix the ADR Context's "seven free-form `TEXT` columns"** → "seven `TEXT` columns holding
  authorization policy (class `policy`)".
- Add `casbin_rule_ptype_idx` to E10's per-dialect index counts; postgres is **11 + 1 = 12** indexes in a
  `FromDB` deployment, and E10 currently counts only `internal/persistence/store/migrations/*`.

## X10 — MAJOR — the spec and the plan disagree on **which package the guard lives in and what the generator test is called**; taking the spec's version yields a generator that silently regenerates nothing and reports success

**Claims in conflict:**

- `docs/specs/2026-08-22-at-rest-posture.md` D6, line 139: *"The invariant lives in **`internal/persistence/store/atrest_test.go`** (`package store_test`) and fails when: …"*
- `docs/specs/...md` D9, lines 194–195: *"`scripts/gen-at-rest.sh` runs `go test **./internal/persistence/store/** -run **TestAtRest_SecurityMdInSync** -update`"*
- `docs/plans/2026-08-22-at-rest-posture.md` architecture + file table + Task 8: everything is in **`internal/atrest`**, and the test is **`TestSecurityMdInSync`** (no `TestAtRest_` prefix):
  `go test ./internal/atrest/ -run TestSecurityMdInSync -update -count=1`
- The ADR names only `scripts/gen-at-rest.sh` and takes no position, so nothing adjudicates the conflict.

**Probe.**

```
$ grep -n "internal/persistence/store/atrest\|internal/atrest\|TestAtRest_SecurityMdInSync\|TestSecurityMdInSync" docs/specs/2026-08-22-at-rest-posture.md
139:The invariant lives in `internal/persistence/store/atrest_test.go` (`package store_test`) and fails
195:`go test ./internal/persistence/store/ -run TestAtRest_SecurityMdInSync -update`, which rewrites a

$ grep -n "internal/atrest\|TestSecurityMdInSync" docs/plans/2026-08-22-at-rest-posture.md | head -3
56:| `internal/atrest/classification_test.go` | completeness, staleness, dialect-invariance, liveness guard |
59:| `scripts/gen-at-rest.sh` | thin wrapper: `go test ./internal/atrest/ -run TestSecurityMdInSync -update` |
737:func TestSecurityMdInSync(t *testing.T) {
```

**What it proves.**

1. **Two independent divergences**, package *and* test name, in the document pair where the spec is the
   design authority and the plan is the implementation of it.
2. **The spec's version is silently vacuous.** CLAUDE.md Common Pitfall 5: *"`go test -run` on a name
   that does not exist **exits 0** ('no tests to run')."* A `gen-at-rest.sh` reconciled to the spec's D9
   line would run a `-run` filter matching nothing, exit 0, write nothing, and then execute the script's
   own final line — `echo "SECURITY.md at-rest block regenerated and verified."` — under `set -euo
   pipefail`. The generator would report success having done nothing, forever, and `SECURITY.md` would
   quietly freeze at whatever it said the day the marker was added. That is the exact trap the repo has
   already written down.
3. **The spec's placement also breaks D7's stated premise.** D7: *"The everyday guard parses SQL text,
   **so it runs with no Docker and no database**."* `internal/persistence/store` is a Docker-requiring
   test package (its parity tests call `dbtest.RunTestDatabase`, which fails without a daemon — X6), so
   putting the everyday guard there means a plain `go test ./internal/persistence/store/...` cannot run
   it Docker-free. The plan's `internal/atrest` placement is the correct one; the spec's is wrong.

**Proposed fix.** Amend spec D6 line 139 to `internal/atrest/classification_test.go` (`package
atrest_test`) and D9 lines 194–195 to `go test ./internal/atrest/ -run TestSecurityMdInSync -update`,
matching the plan. Add to `scripts/gen-at-rest.sh` a guard against the vacuous-filter trap — run with
`-v` and assert the test actually executed, or add `-count=1` plus a `grep -q '^=== RUN   TestSecurityMdInSync'`
check — because `-run` filters silently succeeding is a known repo hazard and this script is the one
place it would never be noticed.

## X11 — MINOR — the generated table's `keyed` / physical-type columns are undefined for `casbin_rule` on MySQL and SQLite, and Task 6's drift `sed` assumes an unpadded row format

**Claims:** spec § "What `SECURITY.md` gains": *"the 87-row table — `table`, `column`, `class`, physical
type per dialect, **`keyed` per dialect**"*; plan Task 6 step 4:
`sed -i '' 's/| wrkflw_human_task | claimed_by | actor |/| wrkflw_human_task | claimed_by | reference |/' SECURITY.md`

**Probe / observation.** `casbin_rule` exists **only** under Postgres + `FromDB` (E3, verified:
`goose.DialectPostgres` at `internal/authz/casbin/migrate.go:34`, `id BIGSERIAL PRIMARY KEY`). Its 8 rows
therefore have no MySQL or SQLite physical type and no MySQL or SQLite `keyed` value. Nothing in the
spec, ADR or plan says what those cells contain — blank, `—`, `n/a`. Since `Render` must be
deterministic for the drift guard, that choice is load-bearing and is currently unspecified.

On the `sed`: the pattern requires exactly one space of padding around each cell. A generated markdown
table that column-aligns (the natural thing to write) would not match. The plan **does** guard this
(*"⚠ If the `grep -c` prints 0 the `sed` did not match and the green run proves nothing"*), which is why
this is Minor rather than Major — but the guard is a manual instruction, and the same class of mutation
in Task 4 was prescribed with a wrong expected output (X5).

**Proposed fix.** State the empty-cell rendering in the spec's "What `SECURITY.md` gains" list (suggest
`—` with a footnote pointing at the "Postgres + `FromDB` only" sentence, which the spec already
requires). Prefer a `Render`-level unit test asserting one known row verbatim over a `sed` on the
published file — it is deterministic, needs no restore, and cannot silently no-op.

---

## Evidence items that SURVIVED execution (verified, no finding)

| item | claim | verdict |
|---|---|---|
| E1 | four migration directories / four `go:embed` sets | **HOLDS** — `find` reproduces exactly; 2 dirs named `migrations`, 4 `.sql` files |
| E2 | all three introspect helpers filter `LIKE 'wrkflw_%'` | **HOLDS** — `introspectPostgres/MySQL/SQLite` at `migration_parity_test.go:115/160/182` |
| E3 | `casbin_rule` is Postgres-only and `FromDB`-only | **HOLDS** — `goose.DialectPostgres` (`internal/authz/casbin/migrate.go:34`), `id BIGSERIAL PRIMARY KEY`; `FromEnforcer`/`FromStrings`/`FromDB` at `casbinauthz.go:59/75/92` |
| E4 | 79 `wrkflw_*` columns, per-table 9/6/12/4/3/13/8/7/17, +8 casbin = 87 | **COUNTS HOLD**; the "identical across all three dialects" quantifier does **not** — see X1 |
| E5 | 48 / 48 / 67 TEXT-ish; whole divergence is `TIMESTAMPTZ→TEXT` | **HOLDS**, and survives a set-level (not count-level) re-derivation |
| E6 | classification covers the schema exactly, 87, six class counts | **COUNTS HOLD** (27/19/17/11/8/5); exactness holds only under the unstated `trigger_` normalization — X1/X3 |
| E7 | `claimed_by` is the one name with two classes | **HOLDS** — `WithCallLinkLease(owner string, ttl)` at `call_links.go:40`, `ClaimPending` at `:119` |
| E8 | exactly four `Authorize` sites read `task.Eligibility` | **HOLDS** — broad `grep '\.Authorize('` finds 4 non-test production sites, all `runtime/task/service.go` 199/234/255/306 |
| E9 | postgres keyed 27, byClass ref 15 / scalar 7 / ts 4 / actor 1 | **HOLDS for the 79 `wrkflw_*` subset** — my independent derivation matches digit-for-digit. Its *conclusion* about `policy` does not — X9. Per-dialect totals missing — X2 |
| E10 | pg 11 idx / 5 partial · mysql 8 + 3 inline / 0 · sqlite 11 / 0 | **HOLDS** for `internal/persistence/store/migrations/*`; omits `casbin_rule_ptype_idx` — X9 |
| E11 | parser traps 1 (multi-space `ON`, multi-line) and 2 (MySQL inline `INDEX`) | **HOLDS** — both present in the real files |
| E12 | repo conventions reused | **HOLDS** for the three cited (`migration_parity_test.go`, `runtime/monitor/internal_leak_test.go`, `engine/terminal_sites_test.go`, `scripts/` has 4 entries); **incomplete** — misses `dbtest.RunTestSQLite` (X7) and the parity test's own `trigger_` normalization (X1) |
| E13 | no encryption/redaction/tamper-evidence at rest; the "exits 1" correction | **HOLDS** — raw non-test grep yields exactly `doc.go:66` (5xx redaction) and `action/email/email.go:127` (SMTP TLS) |
| E14 | only `Up`/`Down` goose directives; no `StatementBegin/End` | **HOLDS** — 4 `Up`, 4 `Down`, zero `StatementBegin`/`StatementEnd` repo-wide |
| E15 | exactly one FK, three declaration forms, already keyed via PK | **HOLDS** — the three lines reproduce; `wrkflw_journal` PK is `(instance_id, seq)` in all three dialects |

## Summary

| ID | severity | claim under attack | verdict |
|---|---|---|---|
| **X1** | **Critical** | E4: 79 columns "identical across all three dialects"; D4's 87; plan's completeness test | **REFUTED** — MySQL names it `trigger_`; the cross-dialect key union is 88, and `TestClassificationCoversTheSchemaExactly` cannot go green as written |
| **X2** | Major | D8: keyed "27 of 79"; SECURITY.md's unqualified "every `freeform`/`policy` column is index-free" | **INCOMPLETE** — keyed is 27 (pg) / **28** (mysql) / **28** (sqlite); the safety sentence is generalized from one dialect and pinned on one dialect |
| **X3** | — | spec's 87-row classification vs the real schema | **NAME-EXACT**, but only under X1's unstated normalization (positive control for X1) |
| **X4** | **Critical** | D6 item 4 + Task 4 step 1: the class-divergence guard | **VACUOUS BY CONSTRUCTION** — one flat `map[ColumnKey]Class` makes divergence unrepresentable; the liveness guard exercises a shape production is forbidden to produce |
| **X5** | Major | Task 4 step 5's mutation expects "note unclassified **and** note stale" | **IMPOSSIBLE** — `inSchema` is the union, so `note` survives via pg+mysql; `stale = []`. (The `sed` itself **does** match — my brief's hypothesis refuted) |
| **X6** | Major | Task 7: "dbtest skips when unavailable" | **FALSE** — `dbtest` has no skip path; `require.NoError(t, sharedErr)` fails. Also traps the Docker-free SQLite arm inside the Docker-requiring function |
| **X7** | Major | Task 7 hand-rolls SQLite setup | **REDUNDANT + BROKEN** — `dbtest.RunTestSQLite(t)` exists (CLAUDE.md Golang rule #3); prescribed code also lacks the `modernc.org/sqlite` blank import and uses a process-global shared-cache DSN |
| **X8** | **Critical** | ADR D1 + spec D1: discovery by glob `**/migrations/*.sql` | **REFUTED** — that glob matches **1 of 4** files (finds casbin, loses all three dialect schemas). The plan's prose rule is correct; the ADR's is not |
| **X9** | **Critical** | E9/D8/spec D8/SECURITY.md: "zero `policy` columns are keyed; both policy locations can be encrypted without breaking an index" | **REFUTED** — `CREATE INDEX casbin_rule_ptype_idx ON casbin_rule (ptype)`; `ptype` is class `policy`. The prescribed guard `continue`s past `casbin_rule`, so it could never catch this |
| **X10** | Major | spec D6/D9 place the guard in `internal/persistence/store` as `TestAtRest_SecurityMdInSync`; plan uses `internal/atrest` / `TestSecurityMdInSync` | **CONTRADICTION** — the spec's form yields a `-run` filter matching nothing, exit 0, and a generator that reports success having written nothing |
| **X11** | Minor | 87-row table's per-dialect cells for `casbin_rule`; Task 6's drift `sed` | **UNDERSPECIFIED** — no rendering stated for absent-dialect cells; `sed` assumes unpadded markdown |

**Four Criticals (X1, X4, X8, X9), five Majors (X2, X5, X6, X7, X10), one Minor (X11).**

Two of the four Criticals (X8, X9) are the delivery's **own founding defect recurring**: X8 relocates the
lost-migration-directory bug into the discovery rule, X9 relocates it into the derivation. One (X4) is the
eighth test in this repo that cannot fail. One (X1) is a real dialect divergence that the count-based
census was structurally unable to see — and that the repo had already solved, in the very file E12 cites.

