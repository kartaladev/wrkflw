# ADR-0187 §AT-REST — INTERACTION lens (rule #9 audit)

Worktree: `/private/tmp/wt-0187-interaction` @ `ebafdf0f`. Brief: take the ten decisions
PAIRWISE and derive what each does to the other's premises. 10 decisions = 45 pairs.

Decision glosses (used throughout):
- **D1** migrations are DISCOVERED by glob `**/migrations/*.sql`, never listed.
- **D2** classification is by LOGICAL ROLE, dialect-INVARIANT; physical type a separate generated annotation.
- **D3** classification key is `table.column`, never bare `column`.
- **D4** six classes (`reference` 27, `timestamp` 19, `scalar` 17, `freeform` 11, `policy` 8, `actor` 5 = 87).
- **D5** `wrkflw_human_task.eligibility` is `policy`, not `freeform`.
- **D6** the guard fails on omission, staleness, `SECURITY.md` drift, class divergence, parser dishonesty.
- **D7** the parser is cross-checked against live DB introspection (Docker-gated).
- **D8** a `keyed` annotation, machine-derived PER DIALECT, labelled a LOWER BOUND.
- **D9** the generator is a golden-file `-update` flag on a test, not a `main` package.
- **D10** NO mechanism ships (no `VariableCodec`, no hash-chained journal) — deliberately.

STATUS: COMPLETE. 19 findings (3 Critical, 6 Major, 8 Medium, 2 Low). All 45 pairs dispositioned — see the coverage note at the end. No item left UNVERIFIED; no Docker started.

---

## I1 — CRITICAL — **D2 × D3** (dialect-invariant class × `table.column` key)
### MySQL's journal payload column is named `trigger_`. The key set is **88**, not 87 — every count in the bundle is off by one, and `wrkflw_journal.trigger_` is UNCLASSIFIED.

**What D3 assumes.** `docs/adr/0187-...md` decision 3: *"The classification key is `table.column`."*
`docs/specs/2026-08-22-at-rest-posture.md` D3: *"Exactly one column name in the schema carries two
different classes (E7)"* — the design's only stated cross-table/cross-dialect key hazard is
`claimed_by`.

**What D2 assumes.** ADR decision 2: *"Classification is by LOGICAL ROLE and is dialect-invariant"*,
justified by *"**The entire divergence is the timestamp mapping.**"* (ADR §Decision 2; spec D2:
*"the **entire 19-column divergence** is the `TIMESTAMPTZ → TEXT` mapping"*).

**How they collide.** D2's invariance is asserted over `table.column` keys (D3's shape). But one
column's **name** — not its type — differs per dialect. `internal/persistence/store/migrations/mysql/0001_init.sql:31`
declares `trigger_ JSON NOT NULL`; postgres:30 and sqlite:40 declare `trigger`. So
`ColumnKey{wrkflw_journal, trigger}` and `ColumnKey{wrkflw_journal, trigger_}` are **two distinct
keys**, and the union across dialects is 80 `wrkflw_*` keys + 8 `casbin_rule` = **88**, not 87.
The divergence is therefore **not** entirely the timestamp mapping.

**Executed** (throwaway probe, worktree `ebafdf0f`, depth-aware body split identical to the plan's
task 1 step 3 rules):

```
postgres 79 cols, 79 distinct
mysql    79 cols, 79 distinct
sqlite   79 cols, 79 distinct
casbin   8
UNION wrkflw_* distinct table.column keys: 80
UNION + casbin: 88
in mysql not in postgres: [('wrkflw_journal', 'trigger_')]
in postgres not in mysql: [('wrkflw_journal', 'trigger')]
in sqlite not in postgres: []
```

**Blast radius — this breaks the bundle in eight places:**
1. **E4** (`docs/specs/2026-08-22-adr-0187-measurements.md:86`): *"Grand total **87** distinct
   `table.column` keys"* — FALSE, it is 88. E4's own per-dialect table is right (79/79/79); the
   *summary sentence over it* is the false one, which is exactly the Premise-Discipline recap failure.
2. **D4's table** (spec:106, ADR decision 4): `freeform` is **12**, not 11 (`trigger` and `trigger_`
   are both free-form). Total 88. Two of the six published counts are wrong.
3. **Plan task 3 step 1**: `assert.Len(atrest.Classification, 87)` fails.
4. **Plan task 3 step 3**: `TestClassificationPerClassCounts` asserts `ClassFreeform: 11` — fails.
5. **Plan task 2 step 5**: `assert.Len(schemas["postgres"].Columns, 87)` still holds (postgres is
   79+8), but the *stated rationale* "79 wrkflw_* columns present in every dialect" (ADR decision 4)
   is wrong: 78 are present in every dialect, one appears under two names.
6. **The completeness guard (D6 item 1) fires** on `wrkflw_journal.trigger_` — so the delivery's
   first real GREEN is unreachable from the spec's own classification table, which lists
   `wrkflw_journal … freeform: trigger` and nothing else (spec:212).
7. **D7's cross-check cannot catch it**: both sides of
   `ElementsMatch(parsedColumnNames(parsed["mysql"],…), liveColumnNames(introspectMySQL(…)))` say
   `trigger_`. The Docker-gated honesty check is blind to the one divergence that matters here.
8. **The generated `SECURITY.md` table** gains a row for a column that exists in exactly one dialect,
   with no marker saying so — while `casbin_rule` gets an explicit "Postgres + `FromDB` only"
   sentence. The two dialect-conditional cases are handled inconsistently.

**The hand-off nobody in the bundle agreed to.** The repo *already solved this*:
`internal/persistence/store/migration_parity_test.go:92-113` carries `normalizeMySQLTriggerColumn`,
which renames `trigger_`→`trigger` sourced from `dialect.NewMySQL().JournalTriggerColumn()`
(`internal/persistence/dialect/mysql.go:67`) so *"the parity comparison is not tripped by this one
intentional asymmetry"*. **Plan task 7 explicitly forbids reusing that machinery** — *"⚠ **Do NOT
change `colFacts` or the existing `introspect*` helpers.** … Add **sibling** functions."* — so the
new package inherits the raw name and none of the normalization. E12 lists that file as a convention
*reused*; it reuses the introspection helpers and skips the one normalization that this design needs.

**Proposed fix (concrete).**
- Canonicalise at parse time: `ParseSQL` maps `wrkflw_journal.trigger_` → `trigger` for the mysql
  dialect, sourced from `dialect.NewMySQL().JournalTriggerColumn()` (not a literal), with a comment
  citing `migration_parity_test.go:92`. Then 87 / `freeform` 11 stand and D2's "entire divergence is
  the timestamp mapping" becomes true *after canonicalisation* — say so in D2, do not leave it as an
  unqualified claim.
- Add a plan step pinning the canonicalisation itself (assert `ColumnKey{wrkflw_journal,"trigger_"}`
  is **absent** from `schemas["mysql"]` and `{…,"trigger"}` present), otherwise a future reserved-word
  rename in another dialect silently re-opens this.
- Correct E4's summary sentence from 87 to "87 after canonicalising the one reserved-word rename;
  88 raw".
- **If instead the design chooses to keep raw names**, then D2 is false as written and D4's counts
  are 88 / `freeform` 12 — either way the bundle as it stands cannot be implemented.

---

## I2 — CRITICAL — **D1 × D6** (glob discovery × "a new column fails the build"), with **ADR-0132**
### Discovery is at FILE granularity; comprehension is `CREATE TABLE`-only. An `ALTER TABLE … ADD COLUMN` migration is discovered, parsed to zero columns, and silently omitted — which is the exact failure D1 exists to close, re-opened one level down.

**What D1 assumes.** ADR decision 1: *"Discovery plus rule 5 below means a fifth migration directory
produces unclassified columns, which **fails the build** — the fourth-directory failure becomes
structurally unrepeatable rather than remembered."* Spec D1: *"a fifth migration directory yields
unclassified columns, which **fails the guard** (D6)."*

**What D6 assumes.** ADR §Consequences/Good: *"**Adding a column to any migration fails the build
until it is classified**; removing one fails until its entry is deleted."*

**How they collide.** The guard's input is `ParseSQL`, whose entire specification is
`docs/plans/2026-08-22-at-rest-posture.md:144-151`: truncate at `-- +goose Down`, strip `--`
comments, split on `;`, and *"for each **`CREATE TABLE <name> ( … )`**"* build columns. **There is no
rule for any other statement, and no error on an unrecognised one.** A migration containing only
`ALTER TABLE wrkflw_human_task ADD COLUMN reviewer_note TEXT;` parses to **zero** columns. The
completeness guard (D6 item 1) then sees nothing to be unclassified, `Render` emits no row, and
`SECURITY.md` states — with the full authority of a machine-checked invariant — that the column does
not exist. The ADR's headline Good consequence is false for the most likely future migration shape.

**This is not hypothetical; ADR-0132 makes it the declared plan.**
`docs/adr/0132-consolidated-single-file-migrations.md` Context: *"The Postgres set in particular
carried a long tail of small **`ALTER`/`RENAME` migrations** (`0003` adds outbox resilience columns,
`0006` adds call-link lease columns, `0009` adds `outbox.definition_ref`, `0011` renames
`timers.fire_at` → `next_run` …)"* — and its own decision text: *"**Once released, adding schema
changes will resume as new numbered files on top of the consolidated baseline**; this squash is a
one-time pre-1.0 cleanup, not a new policy of rewriting history."* The bundle's parser is specified
against the post-squash snapshot (four files, all pure `CREATE TABLE`) and inherits an assumption —
"migrations declare tables" — that ADR-0132 explicitly says will stop holding.

**Executed.** `grep -rn "ALTER TABLE" --include='*.sql' .` → no matches today (worktree `ebafdf0f`),
which is *why* the design's premise looks safe. That is a fact about the squash, not about the parser.

**The asymmetry that makes this worse than an ordinary gap.** D6 is deliberately built to fail
LOUDLY in four other directions (omission, staleness, drift, divergence). This one direction fails
SILENTLY and produces a *published security document* that under-reports. Under-reporting is the
specific harm the spec names: *"a consumer who encrypts the columns we name and leaves the rest in
the clear has been harmed by our documentation"* (spec:30-31).

**Proposed fix (concrete).** Make the parser **total** rather than selective: `ParseSQL` returns an
error for any top-level statement whose leading keyword is not in an explicit allow-list
(`CREATE TABLE`, `CREATE INDEX`, `CREATE UNIQUE INDEX`, and — after the `-- +goose Down` cut —
nothing else). Add a plan task-1 test case asserting `ParseSQL` errors on
`ALTER TABLE t ADD COLUMN x TEXT;` with a message naming the statement, and a comment at the site
citing ADR-0132's "schema changes will resume as new numbered files". Then an unsupported migration
form is a **build failure demanding a parser decision**, which is the same shape D1 gives an
undeclared directory. Also state in the ADR that the classification's ground truth is
`CREATE TABLE`/`CREATE INDEX` DDL, so the limitation is a stated scope rather than a silence.

---

## I3 — MAJOR — **D6 × D9** (drift guard × the `-update` generator), and **spec × plan**
### The spec's generator command names a package and a test that the plan never creates — and `go test -run` on a nonexistent name **exits 0**, so `scripts/gen-at-rest.sh` reports success having regenerated nothing. The plan's own idempotence check cannot tell that apart from success.

**What D9 assumes.** Spec D9 (`docs/specs/2026-08-22-at-rest-posture.md:194-196`):
*"`scripts/gen-at-rest.sh` runs `go test **./internal/persistence/store/** -run
**TestAtRest_SecurityMdInSync** -update`, which rewrites a `<!-- BEGIN at-rest (generated) --> …`
block in `SECURITY.md`."* ADR decision 9 repeats the test name: *"a golden-file `-update` flag on the
test, wrapped by `scripts/gen-at-rest.sh`"* with the same
`./internal/persistence/store/ -run TestAtRest_SecurityMdInSync -update` invocation.

**What the plan builds.** `docs/plans/2026-08-22-at-rest-posture.md:59, 737, 931-932`: the test is
`TestSecurityMdInSync` in **`internal/atrest`**, and the script runs
`go test ./internal/atrest/ -run TestSecurityMdInSync -update -count=1`.

**How they collide.** Two independent mismatches — wrong package *and* wrong test name. Executed
against the repo, `./internal/persistence/store/` exists and compiles, so the spec's command is not
even a build error: it is `go test` on a real package with a filter matching no test.

**CLAUDE.md Common Pitfall #5 is the load-bearing fact:** *"`go test -run` on a name that does not
exist **exits 0** ('no tests to run')."* So the spec's `gen-at-rest.sh` — which carries
`set -euo pipefail` — runs both lines green and prints
`"SECURITY.md at-rest block regenerated and verified."` while `SECURITY.md` was never touched.

**And the plan's verification of the script cannot detect it** (task 8 step 2, lines 940-944):

```bash
cp SECURITY.md /tmp/sec.bak
./scripts/gen-at-rest.sh
diff SECURITY.md /tmp/sec.bak && echo "IDEMPOTENT"
```

A `diff` that is clean because the generator rewrote the file identically is **byte-indistinguishable**
from a `diff` that is clean because the generator did nothing. The step designed to prove the
generator works is vacuous against the generator's single most likely failure. This is the same
shape as the repo's own recurring "a test that could not fail" defect, applied to a shell step.

**Why this is an interaction and not a typo.** D6 item 3 (`SECURITY.md` drift fails the build) is
what makes a silently-no-op generator survivable *in CI* — a stale block is caught. But the
regeneration path is what a developer runs to *fix* the failure. A no-op generator turns "the guard
fired" into an unfixable loop, and the natural escape is to hand-edit `SECURITY.md` — which is
precisely what D9 exists to prevent. The two decisions are coupled through the developer's workflow,
and neither document models that loop.

**Proposed fix (concrete).**
1. Correct spec D9 and ADR decision 9 to the plan's package and test name, or move the test — one
   name, one package, spelled identically in all three documents.
2. Make the script prove the test **ran**: `go test ./internal/atrest/ -run '^TestSecurityMdInSync$'
   -update -count=1 -v` and `grep -q '^=== RUN   TestSecurityMdInSync$'` on the output, failing the
   script otherwise. Alternatively assert `--- PASS: TestSecurityMdInSync`.
3. Replace task 8 step 2's vacuous `diff` with a **falsifiable** round-trip: corrupt one generated
   row first (the task-6 step-4 `sed` already does exactly this and already requires `grep -c` = 1),
   then run the script and assert the file was **restored** to the backup. That distinguishes
   "regenerated" from "did nothing" in one step.

---

## I4 — CRITICAL — **D3 × D2** (`table.column` key shape × dialect-invariant class)
### D3's flat `map[ColumnKey]Class` makes D2's invariance a **type-level tautology**, so D6's item-4 guard is a total function returning `nil`. Executed: it reports zero divergences over a schema built to diverge three different ways.

**What D2 assumes.** ADR decision 6: the guard fails when *"a column whose class differs across
dialects"* is found. Spec D6 item 4: *"any column's **class** differs across dialects (the D2
invariance pin)"*. Spec §Falsifiability admits it *"**passes the moment it is written**"* but claims
it is rescued by a liveness guard plus *"a **real mutation** against a migration file on disk"*.

**What D3 assumes.** Plan task 3 Produces: `var Classification map[ColumnKey]Class` — **one flat map,
one class per `table.column`, no dialect dimension**. Plan task 4 step 1 calls
`atrest.ClassDivergences(schemas, atrest.Classification)`.

**How they collide.** A `map[ColumnKey]Class` cannot hold two classes for one key. `ClassDivergences`
therefore cannot return a non-empty result **for any schema whatsoever** — not "does not today", but
*cannot*, for all inputs. The plan's own Known-gap note (lines 1005-1010) requires
`ClassDivergences` to be *"implemented in terms of"* `ClassDivergencesPerDialect` — and the only way
to do so is to wrap the flat map in a constant function, which is exactly what erases the
divergence. **The note diagnoses the right hazard ("the liveness guard tests a different code path
from the pin") and prescribes a fix that does not remove it**: the liveness guard exercises the
varying-classification path, production takes the constant path, and no test ever runs the production
path over anything that could fail.

**Executed** (`/private/tmp/divprobe`, Go 1.25, both functions written exactly to the plan's stated
contract, run against a deliberately hostile 3-dialect fixture carrying a type divergence, a
postgres-only column, and the real `trigger`/`trigger_` rename from I1):

```
ClassDivergences over a hostile 3-dialect schema: []string(nil) (len=0)
ClassDivergencesPerDialect with a planted divergence: []string{"t.same"}
```

**And the prescribed mutation does not rescue it either.** Plan task 4 step 5 mutates
`sqlite/0001_init.sql` (`note` → `note_text`) and states the expected failure verbatim
(plan:620-621): *"`MUTATION EXIT=1` with **`wrkflw_human_task.note_text` unclassified** and
**`wrkflw_human_task.note` stale** — i.e. the **completeness and staleness guards** both fire."* By
the plan's own words the mutation exercises D6 items 1 and 2 — **not item 4**. So the spec's
requirement that the pin be non-vacuous via "a real mutation against a migration file on disk" is
prescribed and then not delivered by the prescribed command. `engine/terminal_sites_test.go`, the
cited precedent (E12), is being copied in form and not in effect.

**Why it matters rather than merely being ugly.** D2 is the decision that lets a **single** `class`
column sit beside per-dialect `type` and `keyed` columns in the published table (the author's own
UNRESOLVED D2×D8 presentation worry). The bundle presents item-4 as the *enforcement* that makes that
single column trustworthy. It enforces nothing; the invariance is an artifact of the data structure,
which is a fine design — but the bundle sells a guard where it has a type.

**Proposed fix (concrete).** Pick one, and say which in the ADR:
- **(a) Delete D6 item 4 and the pin**, and replace the claim with the honest one: *"the class is
  dialect-invariant **by construction** — `Classification` is keyed by `table.column` with no dialect
  dimension, so a per-dialect class is unrepresentable."* Keep `ClassDivergencesPerDialect` only if
  something else needs it; otherwise drop it and the liveness guard with it. This is the smaller,
  truer delivery.
- **(b) Keep the guard and give it something real to check**: make the pin assert the *composition*
  D2 actually asserts — that every dialect declaring a key resolves it to the **same class** through
  the same map **and** that no column is present in one dialect under a name absent from another
  (which is the check that catches I1's `trigger_`, and which today nothing performs). That guard is
  falsifiable, and the I1 mutation would fire it.
- Either way, task 4's mutation must state which guard it fires, and the `▶ Progress` block must
  record the observed text — a mutation whose expected failure is a *different* guard is not
  evidence for this one.

---

## I5 — MAJOR — **D5 × D8** (`eligibility` is `policy` × `keyed` is a LOWER BOUND)
### D5 puts `casbin_rule.v0..v5` in the class D8's headline sentence advertises as safe to encrypt. Those columns are **filtered in SQL by the repo's own adapter**, so the advice is wrong for the exact case D5 created — and the author marked this pair "Resolved" after checking only the safe direction.

**What the author checked.** Spec §Author's interaction pass:
*"**D5 (`eligibility` is `policy`) × D8 ("zero `policy` columns are keyed").** Checked, and it holds
independently of D5: `eligibility` is `JSONB` with no index, so it is unkeyed whether it is classed
`policy` or `freeform`. … **Resolved** — recorded because it looked like a dependency and is not."*

That is true and irrelevant. D5's substance is not that `eligibility` is `policy`; it is that
`eligibility` and `casbin_rule.{ptype,v0..v5}` **share a class** — spec D5: *"Giving both the same
class is what makes that sentence fall out of the table instead of needing to be remembered."* The
pair to derive is therefore *what D5's merge does to D8's advice about the merged class*, and that
direction was not examined.

**What D8's output claims.** Spec §What `SECURITY.md` gains and plan task 6 step 3 item 3, emitted by
the generator: *"**every `freeform` and `policy` column is index-free and can be encrypted without
breaking an index**"*. ADR §Consequences/Good: *"all 11 free-form columns and both policy locations
can be encrypted without breaking an index"*.

**What D8's own caveat says.** ADR decision 8: *"⚠ It is derived from DDL only; **query-level
filtering in the store layer is invisible to it**, and the generated text says so."*

**How they collide — executed.** `internal/authz/casbin/pg_adapter.go` filters on the policy columns
in SQL:

```
internal/authz/casbin/pg_adapter.go:51 : SELECT ptype, v0, v1, v2, v3, v4, v5 FROM casbin_rule ORDER BY id
internal/authz/casbin/pg_adapter.go:136: DELETE FROM casbin_rule
internal/authz/casbin/pg_adapter.go:137:   WHERE ptype=$1 AND v0=$2 AND v1=$3 AND v2=$4 AND v3=$5 AND v4=$6 AND v5=$7
internal/authz/casbin/pg_adapter.go:164: DELETE FROM casbin_rule WHERE `+where, args...   (RemoveFilteredPolicy)
```

A consumer who follows the generated advice and encrypts `v0..v5` with any non-deterministic scheme
does not merely *lose an index* — `RemovePolicy` and `RemoveFilteredPolicy` **match zero rows and
report success**, so policy deletions silently do not take effect. That is an authorization
**correctness** failure, in the one table whose whole reason for being in this bundle is that it
holds authorization policy. The DDL-only derivation cannot see it, exactly as the caveat says; but
the caveat is a footnote on `keyed` while the encrypt-safely claim is the section's headline
"actionable content".

**The asymmetry is the defect.** For `wrkflw_human_task.claimed_by` the bundle goes out of its way to
name the concrete cost (*"encrypting it non-deterministically costs the `AssignedTo` lookup"*). For
`casbin_rule.v0..v5` — a worse, silent, security-relevant cost, discoverable by one grep in this
repo — it says the opposite. D5's merge is what made one sentence cover both.

**Proposed fix (concrete).**
1. Do not let the generator emit an unqualified "can be encrypted" sentence. Emit
   *"no `freeform` or `policy` column carries a **DDL-declared index** (`keyed` is empty for all
   of them). This is a statement about indexes only — it is **not** a claim that these columns are
   safe to encrypt: `keyed` is a lower bound and the store and adapter layers filter on column values
   in SQL."*
2. Add a **named exception** to the generated text: `casbin_rule.{ptype,v0..v5}` are matched by
   equality in `DELETE` predicates (`internal/authz/casbin/pg_adapter.go:136-137,164`), so encrypting
   them non-deterministically breaks `RemovePolicy`/`RemoveFilteredPolicy` silently. If the bundle
   will not enumerate query-level filtering in general, it must at least not advertise the one case
   it has already been shown.
3. Consider whether the `keyed` derivation should carry a second, hand-stated dimension
   `query-filtered` for columns known to appear in SQL predicates — it would be a judgement like
   `class` (so D6's staleness/completeness machinery covers it) rather than a derivation.

---

## I6 — MAJOR — **D7 × D8** (cross-check the parser × `keyed` derived by that parser)
### All three parser traps that justify D7 live in the INDEX/constraint path, and D7's prescribed cross-check compares **column names only**. The half of the parser that broke three times is the half nothing checks — and the repo's existing index introspection is names-only **by deliberate design**, so the mechanism D7 says it reuses cannot do the job.

**What D7 assumes.** ADR decision 7: *"The parser is cross-checked against live introspection,
because writing this design's own probes broke it three times."* It then names the three:
multi-line `CREATE INDEX` with a multi-space `ON`; MySQL's inline `INDEX name (col)`; MySQL's
table-level `CONSTRAINT … FOREIGN KEY`. ADR §Consequences: *"**The everyday guard trusts the
parser**; only the Docker-gated cross-check proves it honest."*

**What D8 assumes.** ADR decision 8: `keyed` *"records PK membership, `UNIQUE`, and `CREATE INDEX`
column lists including partial-index `WHERE` predicate columns"* — every one of those facts comes out
of the parser's index/constraint path, and none of them out of its column path.

**How they collide.** Plan task 7's assertions are, verbatim:

```go
assert.ElementsMatch(t, parsedColumnNames(parsed["postgres"], "wrkflw_"),
    liveColumnNames(introspectPostgres(t, pool)), "postgres: parsed vs live")
```

Column **names**. Nothing compares `Column.Keys`. So the cross-check certifies exactly the parser
output that the traps did not corrupt, and certifies nothing about the output they did. Trap 1
(*"a first probe emitted four rows whose table name was the literal string `CREATE`"*) affects only
index attribution — it is invisible to a name comparison, and it is the trap the ADR leads with.

**The reuse D7 promises is not available — executed.** Spec D7: *"A separate Docker-gated test
asserts the parse agrees with `introspectPostgres` / `introspectMySQL` / `introspectSQLite`, which
**already exist in the same package** (E12)."* Those helpers return `logicalSchema` =
`map[table]map[column]colFacts` where `colFacts` is `{Nullable, PrimaryKey}`
(`internal/persistence/store/migration_parity_test.go:22-29`) — no index information at all. The only
index introspection in the repo is `indexNames` (line 217), used by
`TestMigrationParity_IndexNamesConverge`, whose own doc comment forbids the comparison D8 would need:

```
// NAMES ONLY. Index COLUMN LISTS diverge on purpose and must not be compared:
// Postgres expresses wrkflw_outbox_dead_idx and wrkflw_call_links_pending_idx
// as PARTIAL indexes (`... WHERE status = 'dead'`), while MySQL and SQLite have
// no partial-index support and fold the predicate column into the key instead
// (`(status, id)`).                       — migration_parity_test.go:376-381
```

So cross-checking `keyed` requires **new** per-dialect index-column introspection (pg_index /
`information_schema.STATISTICS` / `PRAGMA index_info`) that does not exist, and the parity test's own
comment explains why the repo deliberately never wrote it. The bundle costed D7 as "reuse existing
helpers, no refactoring" (E12) and that costing holds only for the half of D7 that does not matter.

**Note this is *not* the same gap the author recorded.** Their D7×D1 note is about a *fifth
migration set's tables* being uncross-checked. This is about the *keyed dimension of the four sets
that are*.

**What is at stake.** `keyed` is the source of both of the bundle's two advertised "actionable"
sentences and of the whole D2×D8 per-dialect presentation. An unchecked derivation feeding a
published security claim is the ADR's own stated failure class: *"A hand-rolled DDL reader that
nothing checks is the same class of defect as a count that rots."*

**Proposed fix (concrete).** Pick one and say which in the ADR:
- **(a)** Extend task 7 with a sibling `liveIndexedColumns(dialect)` for all three dialects
  (`pg_index`+`pg_attribute`, `information_schema.STATISTICS`, `PRAGMA index_list`/`index_info`) and
  assert `parsed keyed ⊆ live keyed` **per dialect** (subset, not equality — `keyed` is a declared
  lower bound and partial-index predicate columns are not index key columns in any catalog). Budget
  it as real work, not as reuse.
- **(b)** Accept the gap explicitly: state in ADR §Consequences/Bad that **`keyed` is parser-only and
  is NOT cross-checked**, that trap 1 lived in exactly that path, and that the mitigation is the
  hand-written per-dialect assertions in plan task 5 — which today pin postgres (27 + a 4-entry
  `byClass` map) and a single mysql column, and pin **SQLite not at all** (see I9).
- Either way, delete or qualify the ADR's *"only the Docker-gated cross-check proves it honest"* —
  as prescribed it proves half the parser honest.

---

## I7 — MAJOR — **D4 × the open questions**, against the author's own note
### The author's UNRESOLVED D4×open-questions note names ONE broken test ("update task 3"). Executed: accepting open question 1 also breaks **task 5**, in a different Go test, with a number the note never mentions — because `wrkflw_processed_message.subscriber` is a **PK column and therefore keyed**.

**What the author wrote.** Spec §Author's interaction pass:
*"**D4 (the six counts) × open questions 1 and 2.** ⚠⚠ **UNRESOLVED, and it constrains the audit's
own output.** The plan hardcodes `reference` 27, `timestamp` 19, `scalar` 17, `freeform` 11, `policy`
8, `actor` 5 as test assertions. … **Any reclassification the audit accepts changes two of those six
numbers and breaks a prescribed test.** This is stated so the adjudication step knows it must
re-derive the counts *and* **update task 3**."*

**How it is incomplete.** Open question 1 asks whether `wrkflw_processed_message.subscriber` and
`wrkflw_outbox.topic` deserve a consumer-controlled sub-class rather than `reference`. The note
tracks only the *classification* counts (task 3). But plan **task 5** carries a second, independent
hardcoded census over the same classification:

```go
assert.Equal(t, map[atrest.Class]int{
    atrest.ClassReference: 15,
    atrest.ClassScalar:    7,
    atrest.ClassTimestamp: 4,
    atrest.ClassActor:     1,
}, byClass, "zero freeform and zero policy columns are keyed — the actionable finding (E9)")
```

and the plan itself calls that map *"the load-bearing assertion"* and notes *"`assert.Equal` on the
whole map **fails if a fifth class appears**"* (plan:706-711). That is exactly what open question 1
would do.

**Executed** — I independently re-derived the postgres keyed set from
`internal/persistence/store/migrations/postgres/0001_init.sql` (PK membership + `UNIQUE` +
`CREATE INDEX` key columns + partial-index `WHERE` predicate columns), per table:

```
instances 4 (instance_id, status, ended_at[pred], started_at)   journal 2 (instance_id, seq)
outbox    4 (id, dedup_key[UNIQUE], next_attempt_at, status[pred])
definitions 2 (def_id, version)      processed_message 2 (subscriber, message_id)
call_links 3 (child_instance_id, parent_instance_id, status[pred])
timers 3 (instance_id, timer_id, next_run)
chain_links 3 (predecessor_instance_id, outcome, successor_instance_id)
human_task 4 (task_id, instance_id, state, claimed_by)
TOTAL 27  ->  reference 15, scalar 7, timestamp 4, actor 1
```

E9's 27 and its 15/7/4/1 split **reproduce exactly** — good. And `subscriber` is one of those 15
(`PRIMARY KEY (subscriber, message_id)`, postgres:71), while `topic` is unindexed. So accepting open
question 1 yields `ClassReference: 14` plus a fifth entry `ClassConsumerControlled: 1` — task 5's
`assert.Equal` fails, and the failure surfaces during *implementation*, which is precisely the
outcome the author's note exists to prevent: *"rather than accepting a class change and discovering
the breakage during implementation."*

**Why this is the interaction lens's finding and not the counting lens's.** The note is a *stated*
interaction. Stating it converted an unknown into an accepted, bounded cost — and the bound is wrong.
Marking something "UNRESOLVED but stated" is the laundering the brief warns about: the adjudicator
reads "update task 3", updates task 3, and ships a broken task 5.

**Proposed fix (concrete).** Correct the note to name **both** call sites and their numbers:
task 3's `TestClassificationPerClassCounts` (`reference` 27→25, new class 2) **and** task 5's
`TestKeyedLowerBound_Postgres` `byClass` (`reference` 15→14, new class 1). Better: remove the
duplication that creates the coupling — have task 5 assert the *properties* it actually cares about
(`byClass[ClassFreeform] == 0 && byClass[ClassPolicy] == 0` and `keyed == 27`) rather than an
exhaustive map whose other entries are incidental. The exhaustive map buys "fails if a fifth class
appears" at the cost of "fails whenever anything is reclassified" — and a guard that fires on every
legitimate edit is the pressure that produces careless `-update`s (see I3).

---

## I8 — MAJOR — **D2 × D8**, the pair the author left UNRESOLVED — sharpened to a concrete, falsifiable defect
### An empty per-dialect `keyed` cell means EITHER "column absent in this dialect" OR "column present but unindexed". Those are opposite security facts, and the table as specified cannot distinguish them. This is not a presentation worry; it is a data-model gap, and I1 and `casbin_rule` are both live instances of it.

**What the author left open.** Spec §Author's interaction pass:
*"**D2 × D8 (`keyed` is per dialect).** … ⚠ **UNRESOLVED (presentation):** the generated table will
carry per-dialect columns for physical type and `keyed` beside a single `class` column. A reader
scanning it may reasonably infer that everything is per-dialect… The render must state the asymmetry
in the table's own preamble; **whether that is sufficient is a judgement the audit should attack**."*

**It is not sufficient, and the problem is not the preamble.** The row set is the **union** across
dialects (D6 item 1 unions every dialect's columns; the classification is one flat map). But the
per-dialect `type` and `keyed` columns are **partial functions** — undefined for a dialect that does
not have the column. The spec never says what is rendered there, and there are only two options and
both are wrong:

| row | postgres `keyed` | mysql `keyed` | sqlite `keyed` | what a reader concludes |
|---|---|---|---|---|
| `casbin_rule.v0` | *(empty — unindexed)* | *(empty — absent)* | *(empty — absent)* | "unindexed everywhere" — **wrong**, it does not exist on mysql/sqlite |
| `casbin_rule.id` | `PK` | *(empty — absent)* | *(empty — absent)* | "no key on mysql" — **wrong**, no table |
| `wrkflw_journal.trigger` (I1) | *(empty)* | *(empty — absent!)* | *(empty)* | "present and unindexed in all three" — **wrong on mysql** |
| `wrkflw_journal.trigger_` (I1) | *(empty — absent)* | *(empty)* | *(empty — absent)* | a phantom column in two dialects |

Every one of those four rows renders **identically** to a genuinely-present-but-unindexed column such
as `wrkflw_human_task.eligibility`. A consumer's whole use of this document is *"which columns in MY
deployment hold what, and can I encrypt them"* — and for the two dialect-conditional cases the table
answers a question about a schema they do not run. `casbin_rule` gets a prose caveat elsewhere in
the block; `trigger`/`trigger_` gets nothing, because the bundle does not know it exists (I1).

**How the two decisions produce it.** D2 makes `class` invariant, which licenses **one row per
`table.column`**. D8 makes `keyed` variant, which requires **one cell per dialect**. Neither decision
owns the third case — a column that is *absent* in a dialect — and each assumes the other handles it.
D2 assumes every classified column exists in every dialect (spec D4: *"79 `wrkflw_*` (every
dialect)"*); D8 assumes the row's existence is a given and only its keying varies.

**Proposed fix (concrete).** Make presence a rendered value, not an absence: emit a tri-state per
dialect — `—` for *not present in this dialect*, `no` for *present, no DDL key*, and the key list
otherwise. Assert it in a plan test: `Render` must produce `—` for
`ColumnKey{casbin_rule, v0}` under mysql and sqlite, and a distinguishable value for
`ColumnKey{wrkflw_human_task, eligibility}`. That single change also renders I1's rename honestly if
the design chooses not to canonicalise, and it removes the need for the preamble to carry the whole
warning. Add a matching sentence to ADR decision 8 so the asymmetry is a stated design property
rather than a render detail.

---

## I9 — MEDIUM — **D8 × D6** (`keyed` per dialect × what the guard actually pins)
### `keyed` is emitted for three dialects and pinned for **one and a half**. SQLite's `keyed` column reaches the published document with no test and no cross-check.

**What D8 promises.** ADR decision 8 emits `keyed` *per dialect*, with E10's per-dialect index counts
(*"Postgres declares 11 indexes of which 5 are partial, MySQL 8 plus 3 inline with none partial,
SQLite 11 with none partial"*) as the justification.

**What the plan pins.** Task 5 has exactly two tests: `TestKeyedLowerBound_Postgres` (postgres only —
`27` plus the 4-entry `byClass` map) and `TestKeyedIsDialectDependent`, which asserts **one column**
(`wrkflw_outbox.status` carries `index-predicate` on postgres and not on mysql). **SQLite appears in
neither.** Combined with I6 (the cross-check compares column names, never keys), SQLite's entire
`keyed` column is derived by a hand-rolled parser, asserted by nothing, and published.

**Why SQLite specifically is the risky one.** E5's whole argument for D2 is that SQLite is the
dialect whose *physical* declarations diverge most (`TIMESTAMPTZ → TEXT`, 19 columns). Its index DDL
is also the one written last. And per CLAUDE.md, SQLite is the only dialect that runs **without
Docker** — so it is the dialect whose parse a developer can check for free, and the one the plan
declines to check.

**Proposed fix.** Add a third assertion to task 5 mirroring the postgres one for sqlite
(`keyed == N` plus its `byClass` map, both re-derived by execution first — do not assume 27), and
state in the ADR which dialects are pinned. If the numbers turn out identical to postgres except for
the five partial-index predicate columns, say that as the assertion — it is the cheapest possible
statement of E10 as a test rather than as prose.

---

## I10 — MEDIUM — **D1 / D7 × ADR-0132** (parse the migration FILES × goose has no checksum)
### The table describes what a **fresh** deployment would store. goose keys by version with no checksum, so an in-place edit of `0001_init.sql` never re-applies — and D7's cross-check migrates a fresh database from the same file, so it is structurally incapable of noticing.

**What D1/D7 assume.** D1 reads the four `*.sql` files off disk; D7 asserts *"the parse agrees with
live introspection"* by running the migrator against a **new** container/in-memory database
(plan task 7 steps 1 and 3: `store.NewSQLiteMigrator(...)`, `pm.Up(ctx)`, `dbtest.RunTestDatabase`).
Both sides of the cross-check therefore derive from the same file. It proves the *parser* honest; it
proves nothing about any *deployed* schema.

**What ADR-0132 assumes.** It squashed 11+4+3 migration files into one per dialect, justified by
*"**The library is unreleased** — there are no git tags and no shipped version. **No consumer has a
database provisioned from these migrations**, so there is no deployed goose version history
(`goose_db_version`) to preserve."* That premise is the licence to edit `0001_init.sql` in place —
and it expires at the first release. After that, an edit to `0001_init.sql` diverges every existing
deployment from the file, permanently and silently, because goose matches on version number only.

**How they collide.** The bundle's deliverable is a consumer-facing statement of what *their*
database holds. Its ground truth is a file whose relationship to their database is "was applied at
some past version, and has been edited since". The bundle carries an explicit scope statement for
one adjacent case — *"consumer-supplied migrations are outside this table's scope"* (spec:245) —
which shows the authors were thinking about scope, and stops one step short of the case that will
bite: the deployment provisioned from an **older revision of our own file**.

**Proposed fix.** One generated sentence, plus one ADR line. Generated: *"This table is derived from
the migration DDL in this module at this version. goose applies migrations by version number with no
checksum, so a database provisioned from an earlier release of this module may differ; check
`goose_db_version` against this module's version."* ADR: record under Consequences/Neutral that the
classification's ground truth is the module's current DDL, not any live deployment, and that D7
cannot detect the difference by construction.

---

## I11 — MEDIUM — **D3 × D7** (`table.column` key × the cross-check's comparison shape)
### The cross-check compares **bare column names**, discarding exactly the table dimension D3 exists to preserve. A parser that attributed the right columns to the wrong tables passes it.

**What D3 assumes.** ADR decision 3: *"The classification key is `table.column`."* Spec D3: a
`column`-keyed map *"merges a process identity with a human identity and mis-states one of them"* —
`claimed_by` is the named case.

**What D7 does.** Plan task 7:
`assert.ElementsMatch(t, parsedColumnNames(parsed["postgres"], "wrkflw_"), liveColumnNames(introspectPostgres(t, pool)))`.
That `parsedColumnNames` returns **bare** names is pinned by the casbin sibling in step 3, which
compares its output against `[]string{"id","ptype","v0","v1","v2","v3","v4","v5"}` — bare names, no
table qualifier. `introspectPostgres` returns `logicalSchema` = `map[table]map[column]colFacts`
(`migration_parity_test.go:26-29`), so `liveColumnNames` must flatten the table dimension away to
produce a comparable `[]string`.

**How they collide.** `ElementsMatch` over bare names is a multiset comparison. `claimed_by` appears
twice on each side and matches by count; so does `instance_id` (in six tables), `status`, `created_at`
and `def_id`. A parser bug that attached a column to the adjacent `CREATE TABLE` — which is precisely
what E11's trap 1 did at the *table-name* level (*"four rows whose table name was the literal string
`CREATE`"*) — is invisible here as long as the multiset is unchanged. The one honesty check in the
delivery throws away the one distinction the delivery's key shape exists to make.

**Proposed fix.** Compare `table.column` strings on both sides:
`parsedColumnKeys(schema, prefix) []string` and `liveColumnKeys(logicalSchema) []string`, each
emitting `table + "." + column`. It is the same amount of code, it makes the cross-check strictly
stronger, and it puts D3's key shape under the only test that runs against a real database. Keep the
casbin sibling consistent (`"casbin_rule.id"`, …) so one helper serves both.

---

## I12 — MEDIUM — **D8 × D10** (tell consumers what is safe to encrypt × ship no mechanism)
### The author dismissed this pair in one sentence. The advice presupposes a substitution point, and D10 declines to build the only one inside our stack — so the bundle should name where a consumer is expected to act, or the "actionable" content is not actionable.

**What the author wrote.** *"**D8 × D10 (no mechanism ships).** Telling a consumer which columns are
safe to encrypt while shipping no codec is coherent — the encryption they would apply is at their own
layer or the database's, not ours. **No conflict.**"*

**Executed check of "their own layer".** The SQL store is `internal/persistence/store` — a consumer
**cannot import it** (CLAUDE.md: *"`internal/` — non-exported implementation details … that consumers
must not import"*). Its constructors take `(conn any, d dialect.Dialect, opts ...Option)` and there
is **no value-transform / codec option** on any of them (`store.go:132`, `humantask_store.go:87`,
`definitions.go:83`, …). The only `Codec` in the persistence tree is the *cache* value codec
(`persistence/cache/codec.go`), which sits in front of the store and never touches the column bytes.
So "their own layer" means one of exactly two things, and the bundle names neither:

1. **Implement the `persistence` interfaces from scratch** — possible (they are root-package
   interfaces), but then the consumer owns their own schema and this table describes a schema they do
   not use. The advice is self-defeating in that branch.
2. **Database-side**: pgcrypto in a view + `INSTEAD OF` triggers, a client-side-encryption proxy, or
   TDE/full-disk. Only the first two give **column** granularity; TDE gives none, which makes the
   "which columns" framing irrelevant for the most commonly deployed option.

**Why this matters more than a wording nit.** The ADR sells this as a headline benefit —
*"Two non-obvious, **actionable** facts reach the consumer"* — and D10's rationale for shipping no
codec is a *risk* argument (key rotation, key loss), not a *"someone else has this covered"* argument.
Together they produce: "here is what you should encrypt; we have deliberately not built the place
where you would do it, for good reasons; we will not say where else you might."

**Proposed fix.** Two sentences in the generated block: name the substitution points that actually
exist for a consumer using **this** store (database-side column encryption via view+trigger, or a
proxy — and note that TDE/full-disk protects the media, not a compromised database session), and
state plainly that this module provides no column codec and links ADR-0187's non-goal. That converts
D8's list from advice-without-a-verb into advice a reader can act on, and it costs nothing. Also
amend the author's interaction-pass line from "No conflict" to the stated limitation — a dismissal
that was never derived is worse than an open question.

---

## I3 — CORRECTION (executed after writing it; the conclusion holds, the mechanism was half wrong)

I asserted that the spec's `go test ./internal/persistence/store/ -run TestAtRest_SecurityMdInSync
-update` would **exit 0 silently**. Executed (`/private/tmp/flagprobe`, Go 1.25, three cases):

```
=== A: flag defined, -run matches (happy path) ===
=== RUN   TestSecurityMdInSync ... --- PASS ... ok    EXIT=0

=== B: flag defined, -run matches NOTHING (the RENAME case) ===
ok   flagprobe/withflag  0.123s [no tests to run]      EXIT=0      <-- silent no-op, CONFIRMED

=== C: flag NOT defined in that package (the SPEC's command) ===
flag provided but not defined: -update
FAIL flagprobe/noflag  0.374s                          EXIT=1      <-- LOUD, my claim was WRONG
```

**Corrected finding.** The spec's/ADR's command fails **loudly** (`-update` is undefined in
`store_test`), so a developer following the spec gets a hard error and falls back to the plan. That
half of I3 is a **documentation defect, not a silent-failure hazard** — downgrade to MEDIUM.

**The other half stands and is the dangerous one (case B).** Once the flag *is* defined in
`internal/atrest`, `-run` on a name that no longer matches exits **0** with `[no tests to run]`, so
any later rename of `TestSecurityMdInSync` turns `scripts/gen-at-rest.sh` into a green no-op — and
the plan's task-8 step-2 verification (`diff SECURITY.md /tmp/sec.bak && echo "IDEMPOTENT"`) passes
**precisely because nothing happened**. That is a test that cannot fail, in a shell step, guarding
the mechanism D9 exists to provide. Fixes 2 and 3 in I3 remain as written; fix 1 is now a
documentation correction rather than a functional one.

---

## I13 — MAJOR — **D6 × D7 × D9**, spec vs plan
### The spec places the whole invariant in `internal/persistence/store` (`package store_test`); the plan builds a new `internal/atrest` package. D7's stated cost basis — *"which already exist in the same package"* — is true only under the spec's placement, and the plan silently invalidated it without amending the spec or the ADR.

**Verbatim, spec D6** (`docs/specs/2026-08-22-at-rest-posture.md:139`):
*"The invariant lives in **`internal/persistence/store/atrest_test.go`** (`package store_test`) and
fails when: …"*

**Verbatim, spec D7** (lines 155-158): *"A separate Docker-gated test asserts the parse agrees with
`introspectPostgres` / `introspectMySQL` / `introspectSQLite`, which **already exist in the same
package** (E12)."*

**Verbatim, plan** (Architecture, line 9 and the file-structure table, lines 50-59): *"A new
module-level **`internal/atrest`** package"* with `schema.go`, `discover.go`, `classification.go`,
`render.go` and their tests — and task 7 alone in `internal/persistence/store`, flagged
*"⚠ **Different Go package from tasks 1–6.**"*

**Executed:** `grep -n "internal/atrest" docs/adr/0187-*.md docs/specs/2026-08-22-at-rest-posture.md`
returns **nothing**. The package the entire delivery is built in is named in the plan only. The ADR
names no location at all.

**What breaks in the seam.**
1. **D7's "no refactoring" costing evaporates.** Under the plan, task 7 must import `internal/atrest`
   from `store_test` and write new bridging helpers (`parsedColumnNames`, `liveColumnNames`,
   `columnsOfTable`) — that is refactoring-equivalent work the spec's cost argument excluded. The
   `E12` "conventions reused rather than reinvented" consequence in the ADR is measured against the
   spec's placement.
2. **The export surface is now load-bearing and nowhere justified.** `ModuleRoot`, `LoadSchemas`,
   `Schema`, `Column`, `ColumnKey`, `Class`, `Classification`, `ClassDivergences`,
   `ClassDivergencesPerDialect`, `MigrationSets`, `Render`, `ReplaceBlock` must all be **exported
   from a non-test package** solely so a test in a different package can call them. The ADR's closing
   Neutral line — *"The classification is documentation-only: nothing at runtime reads it, so it
   lives in **test-adjacent code** and adds no production surface"* — is then **false as written**:
   under the plan it is a production (non-`_test.go`) package compiled into any build that imports it,
   with twelve exported identifiers. Under the spec's placement it would genuinely have been
   test-only. This is the ADR promising a property the plan removed.
3. **D9's script path** points at the spec's package (see I3).
4. **Coverage.** A new non-test package carries the 85 % floor (plan Global Constraints). The
   `if *update { … }` branch of the golden test never executes in a normal run, and `Render`'s
   dialect-conditional cells (I8) are the least-covered part. Neither the spec nor the ADR anticipated
   a coverage obligation because neither anticipated a package.

**Proposed fix.** Decide the placement once, in the ADR, and make all three documents say it.
If `internal/atrest` (the plan's choice, and the better one — it keeps the everyday guard out of a
Docker-heavy package): amend spec D6 and D7, and **correct the ADR's Neutral line** to
*"lives in a non-exported `internal/atrest` package with no runtime caller"*. If `store_test`: the
plan must be rewritten and D7's reuse argument survives intact. Do not leave the documents describing
a delivery in a package that does not exist.

---

## I14 — MEDIUM — **D1 × D6**, the declaration's blind spot
### The two-way `MigrationSets` check validates that a directory is *declared*, never that the declaration *routes its columns anywhere*. A set declared with an empty `Dialects` slice passes both directions and contributes zero columns — silently, which is F5's failure mode with one extra step.

**What D1 assumes** (plan task 2, the design's self-described *"sharpest edge"*, lines 291-297):
*"discovery finds **directories** and a **declaration** (`MigrationSets`) says what each one is — and
the declaration is checked **both ways**. A directory with no declaration fails; a declaration
matching no directory fails. **Without the two-way check this is hardcoding moved one level down**,
which is the exact defect F5 filed."*

**What D6 assumes.** ADR decision 1: a new migration set *"produces unclassified columns, which fails
the build."*

**How they collide.** `LoadSchemas` is specified as *"merges each set's parsed statements into the
`Schema` of **every dialect the set declares**"* (plan:363-364). Its input is therefore the
**declaration**, not the discovery. Both directions of the check are satisfied by
`{Dialects: []string{}}` — the directory is declared, the declaration matches a directory — and the
set's columns are merged into no dialect, never enter `inSchema`, and are never unclassified. The
census tests assert only `schemas["postgres"|"mysql"|"sqlite"]` lengths, so a fifth dialect key
appearing or not appearing is unasserted. The guard that exists to make omission impossible is
satisfied by a declaration that omits.

An empty slice is the *natural* value for someone adding a migration set whose dialect they are
unsure of — which is exactly the situation that produced the casbin omission in the first place.
It is also what a mechanical merge conflict resolution tends to leave behind.

**Proposed fix (one line, in the test that already exists).** Extend
`TestDiscoverMigrationDirs_FindsAllFourAndAllAreDeclared`'s first loop:

```go
for _, d := range dirs {
    set, ok := atrest.MigrationSets[d]
    assert.True(t, ok, "...")
    assert.NotEmpty(t, set.Dialects,
        "a declared set with no dialects routes its columns into no Schema, so its "+
            "columns are never classified and never published — declare the dialect or "+
            "delete the directory: "+d)
}
```

Also add the reciprocal: assert every dialect named in any `MigrationSets` entry is one of the three
the store supports, so a typo (`"postgress"`) fails at the declaration rather than materialising a
fourth `Schema` that nothing asserts over.

---

## I15 — MEDIUM — **D8 × D9** (`keyed` derivation × the golden-file generator)
### The plan never says whether the two "actionable sentences" are **computed from** the classification or **written as literal strings** in `Render`. If literal, the delivery's entire thesis is defeated in its own headline output: a hand-written claim about a derived property, republished as generated, and D6's drift guard proves it consistent with itself forever.

**What the ADR promises.** §Consequences/Good: *"**Two non-obvious, actionable facts reach the
consumer as generated output rather than as prose someone must maintain**: every `freeform` and
`policy` column is index-free, and `wrkflw_human_task.claimed_by` is not."*

**What the plan specifies.** Task 6 step 3 item 3: *"**the two actionable sentences**: every
`freeform` and `policy` column is index-free and can be encrypted without breaking an index; and
`wrkflw_human_task.claimed_by` **is** indexed…"* followed by *"⚠ **Sentences 3–7 are emitted BY THE
GENERATOR, not typed into `SECURITY.md`.** If they are typed in, the next `-update` deletes them."*

That warning distinguishes *typed into the markdown* from *emitted by the generator*. It does **not**
distinguish *emitted as a literal Go string* from *computed from the data*. Sentences 4–7 (the
lower-bound caveat, the casbin scope, the consumer-migrations scope, the non-goals pointer) are
legitimately literal — they are policy statements. **Sentence 3 is not**: it asserts a property
(`∀c ∈ freeform ∪ policy: keys(c) = ∅`) that the generator has in hand at render time.

**Why the literal reading is the likely one and the damaging one.** The plan hands the implementer a
finished English sentence and no derivation. A literal `Render` output is consistent with every
prescribed test: task 5 asserts the property over the *classification*, task 6 asserts `SECURITY.md`
equals `Render(...)`. Nothing asserts the *sentence* agrees with the *data*. So the day someone adds
`CREATE INDEX … ON wrkflw_instances (snapshot)` — or reclassifies a keyed column to `freeform` — task
5's `byClass` `assert.Equal` fires (good), the developer updates the expected map (natural), and the
sentence in the published security document now states the exact opposite of the truth, with the
drift guard green because `SECURITY.md` still matches `Render`. **That is D6×D9's "protects against
drift, never against wrong" — but arriving through a route the author's note does not cover**: their
note is about a *misclassification* being propagated; this is a *derived claim* decoupled from its
derivation.

**Proposed fix (concrete).** Specify in plan task 6 and in ADR decision 9 that sentence 3 is
**computed**, and that `Render` returns an error if its premise is false:

```go
var keyedSensitive []string
for k, c := range cls {
    if c != ClassFreeform && c != ClassPolicy { continue }
    for _, s := range schemas { if len(s.Columns[k].Keys) > 0 { keyedSensitive = append(...) } }
}
if len(keyedSensitive) > 0 {
    // Emit the enumeration instead of the blanket claim — never the blanket claim.
}
```

Then add a plan test that plants an index on a `freeform` column in a fixture schema and asserts
`Render` does **not** emit the blanket sentence. That is a falsifiable test of the delivery's single
most load-bearing published sentence, and today nothing tests it at all.

---

## I16 — MEDIUM — **D5 × D7 × the Docker gate**
### `casbin_rule` — the table whose omission is the ADR's headline finding and D5's second policy location — is cross-checked by a **Postgres-only, Docker-gated** test, and no gate anywhere fails when it skips. The ADR asserts a requirement the plan does not implement.

**What the ADR asserts.** §Consequences/Bad: *"**The everyday guard trusts the parser**; only the
Docker-gated cross-check proves it honest. On a machine with no Docker daemon, a parser bug and a
classification bug are indistinguishable. **The plan therefore requires the cross-check to run before
merge, not merely to exist.**"*

**What the plan implements.** Task 7 step 4 tells the *implementing agent* to check
`docker info` and confirm with `-v` that subtests ran. Task 8 step 4 (the Delivery Gate run) says
*"⚠ If Docker is down, say so and label any container-free subset as **partial**"* — i.e. it
**permits** the delivery to complete with the cross-check unrun, per CLAUDE.md's carve-out. There is
no assertion, script check, or checklist item that fails when `TestAtRestParseMatchesLiveIntrospection`
skipped. "Requires to run before merge" is stated in the ADR and delegated to a human's attention in
the plan.

**Why casbin_rule sharpens it.** SQLite runs container-free, so the wrkflw_* parse gets *some* live
validation on any machine. `casbin_rule` is Postgres-only (E3) — so on a Docker-less machine the one
table the whole delivery exists to stop forgetting has **zero** live validation, and per I6 its
`keyed` derivation (`id BIGSERIAL PRIMARY KEY`) has none anywhere.

**Proposed fix.** Give the requirement teeth rather than prose: have the cross-check test record that
it ran (e.g. write a marker, or assert `!testing.Short()` plus an explicit
`WRKFLW_REQUIRE_DOCKER=1` env in the delivery checklist that turns a `dbtest` skip into a failure —
check whether `dbtest` already offers this before inventing it). At minimum, add an explicit
verification-checklist line in task 8: *"`TestAtRestParseMatchesLiveIntrospection` and
`…_CasbinRule` both show `--- PASS`, not `--- SKIP`, in the `-v` output; a delivery where they
skipped is not deliverable"* — and soften the ADR sentence if the team will not accept that gate.

---

## I17 — LOW — **D1 × D8** (glob discovery × the keyed census)
### Task 5's keyed census re-introduces a hand-maintained table filter (`if k.Table == "casbin_rule" { continue }`) inside the package built to eliminate hand-maintained table filters.

E2 diagnoses the original omission as *structural*: *"all three schema-introspection helpers the repo
already owns filter `table_name LIKE 'wrkflw_%'`, and `casbin_rule` does not match … Any design
reusing that machinery inherits the same blind spot."* Plan task 5 step 1 then writes:

```go
if k.Table == "casbin_rule" {
    continue // not part of the 79-column wrkflw_* census (E4)
}
```

A sixth migration set adding a non-`wrkflw_` table is not skipped by that line, so the `27` assertion
would break loudly — which is why this is LOW, not MAJOR. But the *shape* is the one E2 warns about,
and the `"wrkflw_"` prefix argument in task 7 (`parsedColumnNames(parsed["postgres"], "wrkflw_")`) is
the same shape again, this time load-bearing. Prefer expressing the exclusion positively — census
over `MigrationSets["…/store/migrations/postgres"]`'s own tables — so it derives from the declaration
D1 already maintains rather than from a literal table name.

---

## I18 — LOW — **D1 × D3** (multi-set discovery × a flat per-dialect `Schema`)
### `LoadSchemas` merges several migration sets into one `map[ColumnKey]Column` per dialect. Two sets declaring the same `table.column` silently last-wins; nothing errors.

Today only postgres receives two sets (store + casbin) and no table name collides, so this is latent.
It becomes live the moment a consumer-facing or feature-specific migration set is added — the exact
event D1 is designed to make safe. One line in `LoadSchemas`: error on a duplicate `ColumnKey` from a
different source directory, naming both. It costs nothing and turns a silent overwrite into the
same loud failure D1 gives every other new-set mistake.

---

## I19 — MEDIUM — **D10 × ADR-0145** (an external hand-off nobody in the bundle agreed to)
### D10's second justification cites ADR-0145 as closing "the obvious in-band home" for a hash chain. ADR-0145 is about **actor provenance in a projection**, not about tamper-evidence — and the obvious in-band home for a journal chain is `wrkflw_journal`, which ADR-0145 does not touch.

**What the bundle claims.** ADR-0187 decision 10: *"No hash-chained journal: a chain whose head lives
in the database the attacker already writes to is theatre, **and ADR-0145 closes the obvious in-band
home for it**."* Spec §Non-goals: *"**ADR-0145 explicitly rules `engine.NodeVisit` out as the place to
carry actor provenance, so the obvious in-band home for a chain is closed by an existing decision.**"*
The ADR's Relates-to line repeats it: *"**ADR-0145** (rules `engine.NodeVisit` out as the carrier of
actor provenance, **which closes the obvious in-band home for a journal hash chain**)"*.

**What ADR-0145 actually decides** (`docs/adr/0145-nodevisit-audit-linkage-and-token-state-rename.md`,
read verbatim):
- Decision 1: *"Add `NodeVisit.TaskToken string` … consumers resolve the actor and the rest from the
  linked `tasks` entry. **Remove** `actor_id` from the **history projection**."*
- Decision 2: a close-reason field. Decision 3: a token-state rename.
- Closing line: *"`NodeVisit` is persisted **only inside the untagged `snapshot` blob**, so the new
  fields are additive and safe."*

**Three ways the citation does not carry the weight put on it.**
1. **Different subject.** ADR-0145 is about *who acted*, surfaced through a task link instead of a
   duplicated field. Nothing in it concerns integrity, hashing, or tamper-evidence.
2. **It does not "rule out" `NodeVisit` as a carrier** — it *adds* two fields to `NodeVisit` in the
   same decision. It removes one field from a *rendered projection*.
3. **Wrong candidate entirely.** A journal hash chain's obvious home is `wrkflw_journal`, whose PK is
   `(instance_id, seq)` in all three dialects — an append-only, ordered, per-instance sequence, i.e.
   textbook chain substrate — and which E13 measured as carrying *"No hash column, no prev-hash
   column, no signature column."* `NodeVisit` is not the journal; by ADR-0145's own last line it is
   not even a table, it lives inside `wrkflw_instances.snapshot`.

**Why it matters.** D10 is a decision to **not build a security control**, and a deferral's whole
value is the quality of its reasoning — this is the paragraph a future session will read before
deciding whether to revisit backlog 101. The "theatre" argument (a chain head stored where the
attacker writes) is sound and sufficient on its own. Bolting on a borrowed citation makes the
deferral look doubly-supported when it is singly-supported, and it is exactly the inherited-claim
restatement Premise Discipline warns about: *"Re-verify claims you inherit before restating them.
Restating strips the hedge."*

**Proposed fix.** Delete the ADR-0145 clause from decision 10, the spec's non-goals, and the
Relates-to line. Replace with the accurate statement: *"The obvious in-band home is `wrkflw_journal`
— `(instance_id, seq)` is already an append-only ordered sequence (E13: it carries no hash, prev-hash
or signature column). We decline it because a chain whose head lives in the same database the
attacker already writes to is theatre; a real design needs an out-of-band head (external anchor,
signed export, or a separate trust domain), which is out of scope here."* That is a better deferral
record and it costs one paragraph.

---

# (a) Summary table

| ID | severity | pair | one-line collision | verdict |
|---|---|---|---|---|
| **I1** | **CRITICAL** | D2 × D3 | MySQL names the journal payload `trigger_`; the union of `table.column` keys is **88**, not 87, `freeform` is 12 not 11, and `wrkflw_journal.trigger_` is unclassified — so D2's "the entire divergence is the timestamp mapping" is false and four prescribed assertions cannot pass | **EXECUTED — confirmed** |
| **I2** | **CRITICAL** | D1 × D6 (+ADR-0132) | discovery is per-file, comprehension is `CREATE TABLE`-only; an `ALTER TABLE … ADD COLUMN` migration parses to zero columns and is silently omitted — while ADR-0132 states schema changes *will* resume as new numbered files | **EXECUTED — confirmed** |
| **I4** | **CRITICAL** | D3 × D2 | a flat `map[ColumnKey]Class` makes per-dialect divergence unrepresentable, so D6's item-4 guard is a total function returning `nil`; the liveness guard tests a path production never takes and the prescribed mutation fires two *other* guards | **EXECUTED — proven vacuous** |
| **I3** | MEDIUM (was MAJOR) | D6 × D9 | spec/ADR name a package and test the plan never creates; `-run` on a renamed test exits **0**, and the plan's `diff`-based idempotence check cannot tell "regenerated" from "did nothing" | **EXECUTED — half refuted, half confirmed** |
| **I5** | MAJOR | D5 × D8 | D5 merges `casbin_rule.v0..v5` into the class D8 advertises as safe to encrypt; the repo's own adapter filters those columns in `DELETE … WHERE`, so encryption silently breaks `RemovePolicy` | **EXECUTED — confirmed** |
| **I6** | MAJOR | D7 × D8 | all three parser traps that justify D7 live in the index/constraint path; D7's cross-check compares **column names only**, and the repo's index introspection is names-only *by deliberate design* | **EXECUTED — confirmed** |
| **I7** | MAJOR | D4 × open Qs | the author's own UNRESOLVED note names one broken test; accepting open question 1 also breaks task 5's `byClass` map, because `processed_message.subscriber` is a PK column | **EXECUTED — E9's 27 / 15,7,4,1 independently reproduced** |
| **I8** | MAJOR | D2 × D8 | *(the pair the author left UNRESOLVED)* an empty per-dialect `keyed` cell cannot distinguish "absent in this dialect" from "present, unindexed" — a data-model gap, not a preamble problem | derived; 4 live rows named |
| **I13** | MAJOR | D6 × D7 × D9 | spec places the invariant in `store_test`, plan builds `internal/atrest`; D7's "same package, no refactoring" costing and the ADR's "test-adjacent code, no production surface" both become false | **EXECUTED — grep: neither ADR nor spec names the package** |
| **I9** | MEDIUM | D8 × D6 | `keyed` is emitted for three dialects and pinned for one and a half; SQLite's `keyed` column is published with no test and no cross-check | derived from plan task 5 |
| **I10** | MEDIUM | D1/D7 × ADR-0132 | goose has no checksum, so an edited `0001_init.sql` never re-applies; D7 migrates a *fresh* database from the same file and is structurally blind to deployed drift | derived |
| **I11** | MEDIUM | D3 × D7 | the cross-check compares bare column names, discarding the table dimension D3 exists to preserve; a mis-attributing parser passes | derived from plan task 7's casbin assertion |
| **I12** | MEDIUM | D8 × D10 | "which columns to encrypt" presupposes a substitution point; the store is `internal/`, has no codec option, and D10 declines to add one — the bundle names no alternative | **EXECUTED — no codec seam in any store constructor** |
| **I14** | MEDIUM | D1 × D6 | the two-way `MigrationSets` check validates that a set is *declared*, never that it routes columns anywhere; `Dialects: []string{}` passes both directions and silently contributes nothing | derived |
| **I15** | MEDIUM | D8 × D9 | the plan never says whether the two "actionable sentences" are computed or literal; if literal, a hand-written claim about a derived property is republished as generated and the drift guard keeps it consistent with itself forever | derived |
| **I16** | MEDIUM | D5 × D7 | the ADR asserts *"the plan requires the cross-check to run before merge"*; the plan implements no gate that fails when it skips, and `casbin_rule` is Postgres-Docker-only | derived |
| **I19** | MEDIUM | D10 × ADR-0145 | D10 cites ADR-0145 as closing "the obvious in-band home" for a hash chain; ADR-0145 is about actor provenance in a projection, *adds* fields to `NodeVisit`, and the real candidate is `wrkflw_journal` | **EXECUTED — ADR-0145 read verbatim** |
| **I17** | LOW | D1 × D8 | task 5's keyed census re-introduces a hand-maintained table filter (`if k.Table == "casbin_rule"`) inside the package built to eliminate one | derived |
| **I18** | LOW | D1 × D3 | `LoadSchemas` merges multiple sets into one per-dialect map; a duplicate `table.column` from two sets silently last-wins | derived |

**Totals: 3 Critical, 6 Major, 8 Medium, 2 Low = 19.** Six were established by execution
(I1, I2, I3, I4, I5+I6+I7, I12, I13, I19); the rest are derived from the documents' own text with the
colliding sentences quoted.

**The three the author self-reported, re-adjudicated:**
- **D2×D8** ("presentation") — **not a presentation problem**; upgraded to a data-model gap, I8.
- **D4×open-questions** — **undercounted**; it breaks two tasks, not one, I7.
- **D6×D9** ("wrong-but-consistent") — **correctly identified**, and it has a second route the note
  does not cover (a derived claim decoupled from its derivation), I15; plus the regeneration path
  itself can no-op, I3.

⚠ **Not one of the three highest-severity findings (I1, I2, I4) appears in the author's interaction
pass**, and all three are in pairs the author *did* consider (D1×D2/D1×D6 and D2×D8/D3×everything) —
the pairs were named and then derived only in the direction that was already understood.

---

# (b) Coverage note — all 45 pairs

Legend: **F** = finding raised · **A** = author covered and I concur · **A+** = author covered,
extended/corrected by me · **D** = dismissed, reason given.

| pair | disposition |
|---|---|
| D1×D2 | **A+** — author's "discovery cannot tell you a dialect ⇒ declaration" is right; extended by **I14** (the declaration can be empty) |
| D1×D3 | **F I18** — multi-set merge into one keyed map |
| D1×D4 | **F** (folded into I1/I7) — discovery determines the row set the counts are over |
| D1×D5 | **D** — `wrkflw_human_task` is in the store set however it is found; the eligibility judgement has no dependency on how the file was located |
| D1×D6 | **F I2, I14** — the headline mechanism pair |
| D1×D7 | **A** — author's fifth-set-uncross-checked hole is real. ⚠ their "cheap fix" (*"a test asserting every parsed table appears in some cross-check"*) is **not cheap**: the cross-checks live in `store_test` and the parse in `internal/atrest`, so no single package can see both. Note it as such |
| D1×D8 | **F I17** — hand-maintained table filter in the keyed census |
| D1×D9 | **D** — `Render` consumes whatever `LoadSchemas` returns; discovery adds no premise the render depends on beyond the row set already covered by I1/I8 |
| D1×D10 | **D** — nothing ships at runtime, so discovery has no runtime consumer to collide with |
| D2×D3 | **F I1, I4** — the pair that carries both the wrong count and the vacuous guard |
| D2×D4 | **F** (folded into I1, I8) — 87 and `policy` 8 are Postgres+`FromDB` numbers presented as the schema's |
| D2×D5 | **D** — executed: `eligibility` is declared in all three dialects' `wrkflw_human_task`; its *role* is identical and its physical type varies exactly as D2 predicts. No collision |
| D2×D6 | **F I4** |
| D2×D7 | **D** — the cross-check introspects schema shape; `class` is never introspected, so no premise crosses the seam |
| D2×D8 | **A+ → F I8** — author marked UNRESOLVED (presentation); re-adjudicated as a data-model gap |
| D2×D9 | **F** (folded into I8) — the render is where the asymmetry lands |
| D2×D10 | **D** — no mechanism reads the class |
| D3×D4 | **F I1** — the counts are counts of `ColumnKey`s |
| D3×D5 | **D** — `eligibility` occurs in exactly one table; the key shape is immaterial to it |
| D3×D6 | **F I1** — the completeness/staleness guards are keyed by `ColumnKey` |
| D3×D7 | **F I11** — the cross-check discards the table dimension |
| D3×D8 | **A** — author's *"two lookup shapes coexist … a later simplification would reintroduce the `claimed_by` merge"* is correct and adequately stated; its render consequence is I8 |
| D3×D9 | **D** — `Render` iterates the same keys the classification uses; no independent premise |
| D3×D10 | **D** — no mechanism consumes the key |
| D4×D5 | **D** — D5 is *why* `policy` is 8 (1 `eligibility` + 7 `casbin_rule.{ptype,v0..v5}`); verified consistent, not a collision |
| D4×D6 | **D** (noted) — `87` is hardcoded in three assertions plus the golden file, so one legitimate schema change requires four edits. Friction, not a defect; it feeds the pressure described in I3 and I7's fix |
| D4×D7 | **D** — the cross-check asserts set membership, never a count |
| D4×D8 | **F I7** — the second, unnoticed census |
| D4×D9 | **D** — the generated block publishes rows, not the six class counts, so a count change does not silently alter published prose |
| D4×D10 | **D** — no mechanism |
| D5×D6 | **D** — `eligibility` is one classification entry; the guards treat it like any other |
| D5×D7 | **F I16** — `casbin_rule`'s cross-check is Postgres-Docker-only with no skip gate |
| D5×D8 | **A+ → F I5** — author checked the safe direction and marked "Resolved"; the unsafe direction is live |
| D5×D9 | **F** (folded into I8) — the "policy is at rest in two places" sentence must be dialect-conditional |
| D5×D10 | **D** — coherent: D5 says where policy is, D10 says we protect none of it. Both stated |
| D6×D7 | **F I13, I16** |
| D6×D8 | **F I9** — three dialects emitted, one and a half pinned |
| D6×D9 | **A+ → F I3, I15** — author's UNRESOLVED "protects against drift, never against wrong" is correct; two further routes found |
| D6×D10 | **D** — the guard checks a document, not a mechanism; D10 ships no mechanism to guard |
| D7×D8 | **F I6** — the traps are in the half D7 does not check |
| D7×D9 | **D** — independent: the generator never consumes introspection, and the cross-check never reads `SECURITY.md` |
| D7×D10 | **D** — no mechanism to cross-check |
| D8×D9 | **F I15** — the actionable sentences |
| D8×D10 | **A+ → F I12** — author's "No conflict" dismissal was never derived |
| D9×D10 | **D** — the generated block points at the non-goals; consistent by construction |

**External hand-offs derived (per the brief):**
- **ADR-0132** (consolidated migrations, goose keyed by version, no checksum) → **I2** (ALTER
  migrations are the declared future) and **I10** (deployed drift is undetectable by D7).
- **ADR-0098** (human-task schema, the `claimed_by` index) → **checked, no finding.** The bundle's use
  of the schema comment (*"keeps `AssignedTo`'s lookup indexed"*) is accurate, and my independent
  keyed re-derivation puts `wrkflw_human_task.claimed_by` in the keyed set as the single `actor`
  column, exactly as E9 says.
- **ADR-0145** (`NodeVisit`) → **I19**; the citation does not support the claim built on it.
- **ADR-0095** (admin routes default-absent) → **no interaction found.** The delivery adds no route
  and no runtime surface; `SECURITY.md` gains a section only. Dismissed rather than skipped.
- **ADR-0081/0082** (neutral store, three dialects, SQLite omits `Notifier`/`Locker`) → **no
  interaction found.** Those capability interfaces are Go-level, not schema-level; all three dialects
  declare the same nine `wrkflw_*` tables (verified: 79/79/79 columns), so the omission does not
  reach the classification. The one dialect-level schema asymmetry that *does* exist is `trigger_`,
  which is **I1**.
- **`migration_parity_test.go`'s `colFacts`/`introspect*`** → **I1** (the `normalizeMySQLTriggerColumn`
  the plan forbids itself from reusing), **I6** (names-only index introspection by design), **I11**
  (the `logicalSchema` shape the cross-check flattens). The plan's instruction *"add sibling
  functions"* is right in spirit; what it misses is that one of the existing helpers encodes a
  **normalization the new code needs** and another encodes a **deliberate refusal** to compare what
  D8 needs compared.
- **Task 7's package boundary** (`store_test` vs `atrest`) → **I13**. What crosses: twelve exported
  identifiers that exist as exported solely for that crossing, in a package the ADR calls
  "test-adjacent … no production surface".

**Budget note:** no item left UNVERIFIED. No Docker was started; every Postgres/MySQL-dependent claim
above was established from the migration DDL and the repo's own test source, never from a live
database — the one place that mattered (I6's "the repo has no index-column introspection") was
established by reading `migration_parity_test.go`, which is stronger evidence than a live query would
have been.
