# ADR-0187 audit — FAILURE-MODES lens

Worktree: /private/tmp/wt-0187-failure-modes @ ebafdf0f (bundle present, step 0 OK)
Started: 2026-08-22. Constraint: NO Docker containers.

Findings appended below as they are established.

---

## F1 — CRITICAL — The dialect-invariance guard (D6 clause 4) is VACUOUS BY CONSTRUCTION, and the plan's own stated remedy does not cure it

**Quoted, `docs/adr/0187-at-rest-classification-is-machine-checked.md` decision 6:**
> "and a column whose **class** differs across dialects [fails]"

**Quoted, `docs/specs/2026-08-22-at-rest-posture.md` D6 clause 4:**
> "4. any column's **class** differs across dialects (the D2 invariance pin);"

**Quoted, `docs/plans/2026-08-22-at-rest-posture.md` Task 4 Step 2:**
> "`ClassDivergences(schemas map[string]Schema, cls map[ColumnKey]Class) []string` returns one
> string per column whose class is not identical across every dialect that declares it."

**Why it fails.** D3 (the decision that the classification key is `table.column`, never `column`)
makes `Classification` a `map[ColumnKey]Class` — a structure with **no dialect axis**. Class is
therefore a pure function of `ColumnKey`. "The class differs across dialects" is not a property the
data model can express, so `ClassDivergences` cannot return a non-empty result for ANY input. It is
not "a pin whose property already holds" (the spec's framing); it is a detector that cannot fire.

**Evidence (executed).** I implemented the prescribed signature exactly as specified and fuzzed it
over 200,000 randomised inputs — random per-dialect column subsets crossed with random flat
classifications:

```
$ cd /private/tmp/claude-501/atrest-probe && go run .
trials=200000  trials where ClassDivergences returned NON-EMPTY: 0
EXIT=0
```

**The plan diagnoses this and then prescribes a non-fix.** Plan, final section:
> "⚠ **Known gap, stated rather than hidden:** task 4's liveness guard needs
> `ClassDivergencesPerDialect` ... while task 4 step 1 asserts through `ClassDivergences` ...
> **Both** must exist, and the second must be implemented in terms of the first, or the liveness
> guard tests a different code path from the pin"

Implementing `ClassDivergences(s, cls)` as `ClassDivergencesPerDialect(s, func(string) map[ColumnKey]Class { return cls })`
— the constant function, the only bridge available — still cannot diverge, because the constant
function returns the same map for every dialect. The prescribed remedy makes the two share a code
path while leaving the production call site unfalsifiable. The spec's falsifiability table is
therefore under-stated: it says the pin "⚠ **passes the moment it is written**" and attributes that
to "the property already holds", when the truth is that no state of the repo, now or ever, can make
it fail.

**This is the exact failure CLAUDE.md's Premise Discipline names** ("this repo has shipped seven
tests that could not fail"), landing on the one guard the spec singles out for a liveness guard.

**Proposed fix — replace the tautology with the two properties that ARE falsifiable and that D2
actually rests on:**

1. **`TestColumnSetIsIdenticalAcrossDialects`** — assert the set of `wrkflw_*` `ColumnKey`s is
   identical across postgres/mysql/sqlite, reported as a symmetric difference. This is the real
   invariant E4 (the 79/79/79 census) measured, expressed as a set rather than three magic numbers,
   and it is exactly what Task 4 step 5's on-disk mutation (`note` → `note_text` in sqlite only)
   actually makes fire (see F2).
2. **`TestPhysicalTypeDivergenceIsOnlyTheTimestampMapping`** — for every `wrkflw_*` column, assert
   the postgres and sqlite declared types are equal **or** are exactly (`TIMESTAMPTZ`, `TEXT`).
   This is the measured premise of E5 and of D2's entire justification ("the entire 19-column
   divergence is the `TIMESTAMPTZ → TEXT` mapping"), and it is falsified by any migration edit that
   introduces a second mapping — which is what would silently invalidate D2's dialect-invariance
   argument.
3. Keep `ClassDivergencesPerDialect` + its liveness guard only if it is described honestly: as a
   **shape assertion that nobody made the classification per-dialect**. That fact is better carried
   by the type of `Classification` and a comment than by a test that claims to check data.
4. Amend the spec's falsifiability table row from "⚠ passes the moment it is written — the property
   already holds" to "⚠ **cannot fail — the classification has no dialect axis**", and amend
   ADR-0187 decision 6 clause 4 and spec D6 clause 4 to name the two properties above instead.

---

## F2 — CRITICAL — Task 4's "real mutation" proves a DIFFERENT guard than the one it is under, and its stated expected output is factually wrong

**Quoted, `docs/plans/2026-08-22-at-rest-posture.md` Task 4 (titled "The dialect-invariance pin —
with a liveness guard and a real mutation"), Step 5:**
> "**Step 5: MUTATION — prove the pin fires against a REAL file**
> …
> Expected: `MUTATION EXIT=1` with **`wrkflw_human_task.note_text` unclassified** and
> **`wrkflw_human_task.note` stale** — i.e. the completeness and staleness guards both fire."

**Quoted, `docs/specs/2026-08-22-at-rest-posture.md` falsifiability section:**
> "2. a **real mutation** against a migration file on disk (`cp` backup, restore, `diff` byte-exact),
> with the observed failure text recorded in the plan."

**Two defects, both executed.**

**(a) The mutation does not exercise the pin.** The step heading claims it will "prove the pin
fires"; the Expected line then names the completeness and staleness guards instead. The
dialect-invariance pin (`TestNoColumnChangesClassAcrossDialects`) is untouched by this mutation and
— per F1 (the guard is vacuous because `Classification` has no dialect axis) — cannot be touched by
any mutation. So after Task 4 completes, the only guard the spec explicitly demanded an on-disk
mutation for is the only guard that never got one. This is CLAUDE.md's recorded ADR-0175 lesson
("two prescribed mutations were the wrong mutation") repeating verbatim.

**(b) The staleness guard does NOT fire, contrary to the Expected line.** Task 3 Step 1's prescribed
`inSchema` is a **union over all dialects**:
```go
for _, s := range schemas {
    for k := range s.Columns {
        inSchema[k] = true
    }
}
```
Renaming `note` → `note_text` in **sqlite only** leaves `wrkflw_human_task.note` present in the
postgres and mysql schemas, so it stays in `inSchema` and is not stale. Executed, reproducing the
plan's own prescribed logic and its own prescribed mutation:
```
$ go run .   # /private/tmp/claude-501/atrest-probe, mutsim()
unclassified = [wrkflw_human_task.note_text]  (len 1)
stale        = []  (len 0)
plan claims BOTH fire. staleness fires? false
```
I also verified the prescribed `sed` pattern is applicable, so this is not a no-op mutation:
```
$ grep -c '^    note         TEXT,' internal/persistence/store/migrations/sqlite/0001_init.sql
1
```
An implementer following the plan will observe `EXIT=1` — the run "passes" — and record an
observed-failure text that contradicts the plan's Expected line. Per CLAUDE.md, "a claimed RED that
was not observed is a false claim like any other"; here the RED is observed but attributed to a
guard that did not fire.

**Proposed fix.**
1. Retitle Task 4 step 5 to what it is: **"MUTATION — prove the COMPLETENESS guard fires against a
   real file"**, and correct the Expected line to `unclassified = [wrkflw_human_task.note_text]`,
   `stale = []`, stating explicitly that staleness does not fire because `inSchema` is a union.
2. Add a **second** on-disk mutation that does fire the staleness guard: delete the same column from
   **all three** migration files (`note` in postgres, mysql and sqlite), which leaves the
   classification entry orphaned. Expected: `stale = [wrkflw_human_task.note]`, `unclassified = []`.
3. Repoint the "real mutation against a migration file" requirement at the two replacement
   invariants proposed in F1 — the column-set-identity check is falsified by exactly the sqlite-only
   rename already prescribed, so the existing mutation becomes a genuine RED for a genuine guard
   with no extra work.

---

## F3 — MAJOR — The staleness guard is never observed RED: the spec promises a seeding step that no plan task contains

**Quoted, `docs/specs/2026-08-22-at-rest-posture.md` falsifiability table:**
> "| no entry names an absent column | real RED once the map is deliberately seeded with one bogus
> row during the RED step |"

**Why it fails.** The plan's Task 3 has five steps: Step 1 writes the test **with the map still
EMPTY**, Step 2 runs RED, Step 3 fills the map from the spec, Step 4 GREEN + the `claimed_by` pin,
Step 5 commit. **No step seeds a bogus row.** With an empty `Classification`, the staleness loop
iterates zero times and `assert.Empty(t, stale)` passes vacuously during the very RED the spec cites
as its falsification. Executed above (F2): with the map correct, staleness also stays empty under
the only on-disk mutation the plan prescribes.

⇒ The staleness assertion is committed having never been seen to fail — the "assertion whose fixture
cannot make it fail" pattern CLAUDE.md flags ("⚠ A matching line of test text proves nothing about
whether an assertion can fail. Check the *fixture*, not the line").

**Proposed fix.** Insert a step between Task 3 Step 2 and Step 3: seed
`Classification[ColumnKey{Table: "wrkflw_human_task", Column: "note_deleted"}] = ClassFreeform`,
run, **record the observed `stale = [wrkflw_human_task.note_deleted]` failure text**, then remove the
bogus row before Step 3. Alternatively adopt F2's fix (2), which achieves the same via an on-disk
mutation and additionally proves the guard works end-to-end through the parser.

---

## F4 — CRITICAL — The parser FAILS OPEN on any statement it does not recognise, so the next schema change the repo's own migration policy prescribes (`ALTER TABLE`) is invisible to every non-Docker guard

**Quoted, `docs/plans/2026-08-22-at-rest-posture.md` Task 1 Step 3 (the entire parser contract):**
> "Write `internal/atrest/schema.go` with the types above and a `ParseSQL` that:
> 1. truncates `sqlText` at the first `-- +goose Down` line;
> 2. strips `--` comments to end-of-line;
> 3. splits into statements on `;`;
> 4. for each `CREATE TABLE <name> ( … )`, splits the body on top-level commas …
> 5. skips body clauses beginning `PRIMARY`, `UNIQUE`, `FOREIGN`, `CONSTRAINT`, `KEY`, `INDEX`."

There is **no rule 6**. A statement that is not `CREATE TABLE` (or, after Task 5,
`CREATE INDEX`) is silently dropped. `ParseSQL`'s only documented error is presumably a malformed
`CREATE TABLE`; nothing in any task prescribes an "unrecognised statement" failure.

**Why this is the sharpest failure mode in the bundle.** ADR-0132 (the consolidated
single-file-per-dialect migration decision this bundle classifies) states the forward policy
verbatim:
> "Once released, adding schema changes will resume as **new numbered files on top of the
> consolidated baseline**; this squash is a one-time pre-1.0 cleanup, not a new policy of rewriting
> history."

New numbered files on a consolidated baseline are `ALTER TABLE … ADD COLUMN` / `DROP COLUMN` /
`RENAME COLUMN`. Trace what happens the day one lands:

| step | outcome |
|---|---|
| `0002_add_tenant.sql` declares `ALTER TABLE wrkflw_instances ADD COLUMN tenant_id TEXT` | parser silently ignores it |
| completeness guard (Task 3) — any unclassified column? | **no** — the parser never saw it. GREEN |
| staleness guard | **no stale entry**. GREEN |
| dialect-invariance pin | vacuous anyway (F1). GREEN |
| `SECURITY.md` drift guard (Task 6) | regenerates **identically** from the same wrong schema. GREEN |
| `scripts/gen-at-rest.sh` | prints "SECURITY.md at-rest block regenerated and verified." |
| Docker-gated cross-check (Task 7) | **the only detector** — and it skips without a daemon |

⇒ A new column holding process data lands in the database, is absent from the published
classification, and **every everyday guard is green**. That is precisely the harm ADR-0187's own
Context sentence names: *"A consumer who encrypts the columns we name and leaves the rest in the
clear has been harmed by our documentation."* The mechanism built to make the enumeration unrottable
produces **zero signal** in the exact scenario the schema will next evolve through.

The ADR's Consequences under-state this. It says:
> "**The everyday guard trusts the parser**; only the Docker-gated cross-check proves it honest. On
> a machine with no Docker daemon, a parser bug and a classification bug are **indistinguishable**."

They are not indistinguishable — this class is **undetectable**. There is no failing test to
attribute to anything; the suite is green and the document is wrong.

**Evidence that the fix is free today (executed):** the entire migration corpus contains three
statement kinds, and every `DROP` sits after the goose `Down` marker the parser already truncates at.
```
$ grep -rhoiE "^\s*(CREATE TABLE|CREATE INDEX|CREATE UNIQUE INDEX|DROP TABLE|ALTER TABLE|INSERT|COMMENT ON)" \
    internal/persistence/store/migrations/*/*.sql internal/authz/casbin/migrations/*.sql | ... | sort | uniq -c
  31 CREATE INDEX
  28 CREATE TABLE
  27 DROP TABLE
```
```
$ per-file goose-Down line vs first DROP line
mysql/0001_init.sql       DownLine=150  firstDROPline=152
postgres/0001_init.sql    DownLine=162  firstDROPline=164
sqlite/0001_init.sql      DownLine=157  firstDROPline=159
casbin/0001_casbin_rule.sql DownLine=14 firstDROPline=15
```
Zero `ALTER TABLE`. So a fail-closed whitelist accepts 100 % of today's corpus and costs nothing.

**Proposed fix — make the parser FAIL CLOSED, and make that a Task 1 deliverable, not a comment:**
1. `ParseSQL` returns an error for any statement (after `Down` truncation and comment stripping)
   whose leading keywords are not in the whitelist `{CREATE TABLE, CREATE INDEX, CREATE UNIQUE INDEX}`.
   Error text must name the statement and say what to do: *"internal/atrest: unsupported statement
   `ALTER TABLE …` in internal/persistence/store/migrations/postgres/0002_x.sql — the at-rest DDL
   reader understands CREATE TABLE and CREATE INDEX only (ADR-0187 D7). Teach it this statement form
   and add a case to TestParseSQL_RealWorldTraps, or the column it declares will be missing from
   SECURITY.md while sitting in the clear."*
2. Add a Task 1 test case (real RED today):
   `{name: "unsupported statement fails closed", src: "-- +goose Up\nALTER TABLE t ADD COLUMN x TEXT;\n", assert: requires an error mentioning ALTER TABLE}`.
   What makes it fail today: the prescribed parser ignores it and returns a nil error with an empty
   schema.
3. Add the same for `DROP TABLE` **before** the `Down` marker, so a mis-placed `Down` cannot silently
   erase a table.
4. Amend ADR-0187 Consequences: replace "a parser bug and a classification bug are
   indistinguishable" with the stronger, true statement — "an **unrecognised statement form** is
   silently invisible to every non-Docker guard, which is why the parser fails closed on any
   statement it does not understand."
5. Cross-reference `internal/persistence/store/migrations_count_test.go`
   (`TestMigrations_OneFilePerDialect`, which asserts exactly one `*.sql` per dialect and is today's
   only thing holding the `0002` scenario off) from `internal/atrest/schema.go`, so whoever relaxes
   that count is pointed at the parser they must teach.

---

## F5 — MAJOR — The classification describes a FRESHLY MIGRATED database, never a deployed one, and the repo already documents the skew mechanism the bundle omits

**Quoted, `docs/specs/2026-08-22-at-rest-posture.md`, "What `SECURITY.md` gains":**
> "- an explicit statement that **consumer-supplied migrations are outside this table's scope** — the
> invariant covers only the migrations embedded in this module;"

That is the only scoping caveat the generated document carries. It does not say the table describes
what a **fresh** `Up()` produces.

**Why it fails.** goose keys applied migrations by version number and stores **no checksum**, so an
in-place edit of `0001_init.sql` never re-applies to a database already at version 1. The repo does
not merely permit this — it has **already done it**, and documented the consequence. Verbatim from
`internal/persistence/store/migration_parity_test.go:230-236`:
> "This must FAIL LOUDLY rather than skip when the index is absent. goose keys by version and stores
> no checksum, so an in-place edit of `0001_init.sql` never re-applies to a database already migrated
> at version 1 — a persistent local database or a reused test DSN would silently keep the old index
> and lose the benefit entirely."

and its assertion message:
> "`%s: %s missing — a database migrated before the in-place 0001_init.sql edit keeps the old index
> forever`"

So the in-place-edit path is a **used** path (ADR-0159's `wrkflw_timers_keyset_idx` replacement), not
a hypothetical. Applied to this bundle: a column renamed or dropped by a future in-place edit
disappears from the classification and from `SECURITY.md`, while the **old** column still exists,
still holds data at rest, in every database migrated before the edit. The enumeration is then wrong
in the harmful direction — a column in the clear that the document does not name — and no guard in
the bundle can see it, because every guard reads the file.

The Docker-gated cross-check (D7) **cannot** catch this either: it migrates a **fresh** container, so
parse and introspection agree by construction. The bundle presents D7 as the honesty mechanism for
the parse; it is not a mechanism for the deployment.

**Also a missed reuse.** CLAUDE.md records the ADR-0186 lesson *"FOUR times it claimed a gap the repo
had already filled ⇒ search the repo for an existing convention BEFORE writing a new symbol."*
This bundle's 15 measurements (E1–E15) never touch `migration_parity_test.go`'s goose-checksum
comment, despite E12 citing that very file for its `introspect*` helpers.

**Proposed fix.**
1. Add one generated sentence to the `SECURITY.md` block, beside the consumer-migrations caveat:
   *"This table describes the schema a **fresh** migration produces at this commit. goose keys by
   version and stores no checksum, so a database migrated before an in-place edit of a migration file
   may still carry columns this table no longer names, or lack columns it does name. Verify against
   your own deployment before relying on it for a data inventory."*
2. Add E16 to `docs/specs/2026-08-22-adr-0187-measurements.md` recording the
   `migration_parity_test.go:230-236` comment and the ADR-0159 in-place edit as the executed proof
   that this path is used, not hypothetical.
3. Add the same caveat to ADR-0187 Consequences → Neutral, beside the existing consumer-migrations
   line, so the two scoping limits sit together.

---

## F6 — CRITICAL — `Render` has NO prescribed ordering, so the golden file churns on every run and `scripts/gen-at-rest.sh` can never succeed

**Quoted, `docs/plans/2026-08-22-at-rest-posture.md` Task 6 Interfaces:**
> "Produces: `func Render(schemas map[string]Schema, cls map[ColumnKey]Class) (string, error)`"

**Quoted, Task 8 Step 2:**
> "```bash
> cp SECURITY.md /tmp/sec.bak
> ./scripts/gen-at-rest.sh
> diff SECURITY.md /tmp/sec.bak && echo "IDEMPOTENT"
> ```"

**Quoted, Task 8 Step 1, `scripts/gen-at-rest.sh`:**
> "go test ./internal/atrest/ -run TestSecurityMdInSync -update -count=1
> go test ./internal/atrest/ -run TestSecurityMdInSync -count=1
> echo "SECURITY.md at-rest block regenerated and verified.""

**Why it fails.** Both inputs to `Render` are Go maps (`Schema.Columns` is
`map[ColumnKey]Column`; `Classification` is `map[ColumnKey]Class`). Go randomises map iteration
order **per range statement, within a single process**. The bundle prescribes an ordering **nowhere**
— I grepped all three documents for `sort|order|determinis|stable|churn`; the only hits are
`DiscoverMigrationDirs` returning sorted directory paths, two `sort.Strings` calls on failure-message
slices, and prose about "non-deterministic encryption". `Render` is unconstrained.

Consequences, in order of how they bite:
1. `scripts/gen-at-rest.sh` writes the block, then immediately re-asserts it in a **second process**
   — a fresh random order. The script's own verification step fails, so the script **never prints
   its success line**. The delivery's generator does not work as specified.
2. `TestSecurityMdInSync` fails on essentially every run of `go test ./...` — a permanent red, not a
   flake.
3. Task 8 Step 2's `IDEMPOTENT` check is the first place this is noticed, i.e. **after all six
   preceding tasks are built and committed**.

**Evidence (executed).** Rendering the same 87-entry map — the real size — 1000 times in one process,
exactly as the plan's interface prescribes:
```
$ cd /private/tmp/claude-501/atrest-probe && go run .
Render() over the SAME 87-entry map, 1000 runs in ONE process: 995 differed from the first
```

**Proposed fix.** Make ordering a stated part of `Render`'s contract in Task 6, and give it a test:
1. Emit rows in **migration-declaration order** — table order as the tables appear across the
   discovered files, column order as declared inside each `CREATE TABLE` — not alphabetical. That
   keeps the published table diffable against the migration file a reader will open next to it, and
   makes an added column a **one-line** diff instead of reshuffling the block. This requires
   `ParseSQL` to record declaration order (a `Ordinal int` on `Column` and a `Tables []string` slice
   on `Schema`), which is a Task 1 interface change and must be added there, not discovered in
   Task 6.
2. Fall back to a documented total order (`table` then `column`, both `sort.Strings`) for any table
   whose declaration order is unknown, so the function is total.
3. Add the falsifiable test now missing:
   `TestRenderIsDeterministic` — call `Render` twice on the same inputs and `assert.Equal`. ⚠ On its
   own that is weak (two calls can coincide); make it 50 iterations, which the measurement above
   shows fails ~99.5 % of the time against an unordered implementation.
4. Task 6's `assert.Equal(t, next, string(current), "SECURITY.md's at-rest block is stale — run
   scripts/gen-at-rest.sh")` message is **actively misleading** under a non-deterministic renderer:
   it tells the developer to run a script that will not fix it. Once ordering is fixed the message is
   correct; without the fix it sends every reader down the wrong path.

---

## F7 — MAJOR — The author's `-update` framing is INCOMPLETE: it books a solvable MECHANICAL hole into the unsolvable JUDGEMENT bucket. The generator script bypasses the very guards it exists to serve, and `Render`'s behaviour on an unclassified column is undefined

**Quoted, `docs/specs/2026-08-22-at-rest-posture.md`, author's interaction pass:**
> "**D6 × D9 (the golden-file `-update` flag).** ⚠ **UNRESOLVED (and it is a real limitation, not a
> presentation one):** `-update` rewrites `SECURITY.md` from the classification *without judging it*.
> The guard therefore protects against **drift**, never against **wrong** — a misclassified column is
> propagated into the published document by the very mechanism meant to keep it honest, and the run
> is green. Nothing in this bundle detects a wrong-but-consistent classification. The mitigation
> shipped is weak on purpose … a stronger one would need a second, independent derivation, which does
> not exist."

The framing is right about the *judgement* half — no mechanism can decide whether `note` is
`freeform` or `actor`. It is **wrong that this is one problem**. There is a second, purely mechanical
failure riding in the same sentence, and calling the pair "UNRESOLVED, weak on purpose" launders the
fixable half into an accepted cost.

**The mechanical half — the script bypasses the guards.** Quoted, Task 8 Step 1:
```bash
go test ./internal/atrest/ -run TestSecurityMdInSync -update -count=1
go test ./internal/atrest/ -run TestSecurityMdInSync -count=1
echo "SECURITY.md at-rest block regenerated and verified."
```
`-run TestSecurityMdInSync` excludes **every other guard in the package**:
`TestClassificationCoversTheSchemaExactly` (completeness + staleness),
`TestClassificationPerClassCounts`, `TestLoadSchemas_ColumnCensus`,
`TestKeyedLowerBound_Postgres`, `TestNoColumnChangesClassAcrossDialects`. Trace the everyday
scenario:

1. Someone adds a column to `0001_init.sql` (today's only evolution path — see F4/F5).
2. They run `./scripts/gen-at-rest.sh`, the natural "regenerate the docs" reflex.
3. `Render` is called with a schema containing a column absent from `Classification`.
   **The bundle never says what it does.** `cls[k]` on a missing key yields the zero value
   `Class("")`, so the row is emitted with a blank class cell — or, if `Render` iterates `cls` rather
   than the schema, the column is **silently omitted from the published table entirely**, which is
   the exact harm ADR-0187 exists to prevent.
4. The script's second run compares the file to the same wrong render. It passes.
5. The script prints **"SECURITY.md at-rest block regenerated and verified."**

The success line is scoped-true and globally false — the same defect shape CLAUDE.md flags for
package-scoped lint ("`golangci-lint run ./engine/...` is not `golangci-lint run ./...`"). Here the
tool that regenerates the artifact reports it verified while the guard that would have refused it was
never invoked.

Note also that the two halves compound: the *judgement* hole the author documents requires a human to
be wrong; this one requires nobody to be wrong at all.

**Proposed fix (all three are cheap and none needs a "second independent derivation"):**
1. **`Render` fails closed.** It returns an error naming every schema column with no `Classification`
   entry, and every `Classification` entry naming no schema column. Then `-update` **cannot write a
   document with a hole in it**, and step 3 above becomes a hard stop with an actionable message
   rather than a silent blank cell. This is the same fail-closed principle as F4 and costs one
   `if len(missing) > 0 { return "", fmt.Errorf(...) }`.
2. **`scripts/gen-at-rest.sh` verifies the whole package**: change the second line to
   `go test ./internal/atrest/ -count=1` (no `-run`). The success line then means what it says.
   Keep `-run … -update` on the first line — that one must stay narrow.
3. **Amend the interaction pass** to split D6 × D9 into two entries: "**RESOLVED (mechanical):**
   `Render` fails closed on an unclassified column and `gen-at-rest.sh` verifies the whole package"
   and "**ACCEPTED (judgement):** a *correct-shaped but wrongly-classified* column is propagated;
   the only control is that the classification is a reviewed Go literal in a feature commit." Only
   the second is genuinely unresolvable, and stating it alone makes the residual honest instead of
   over-broad.

---

## F8 — CRITICAL — The bundle's HEADLINE actionable claim is FALSE: `casbin_rule.ptype` is a `policy` column AND it is indexed. The prescribed test is scoped so it can never detect the counterexample

**The claim, quoted three times from the bundle:**

`docs/adr/0187-at-rest-classification-is-machine-checked.md`, decision 8:
> "Measured on Postgres: **27 of 79** columns are keyed, of which **zero are `freeform` and zero are
> `policy`** — so all 11 free-form columns and **both policy locations** can be encrypted without
> breaking an index"

`docs/adr/...`, Consequences → Good:
> "Two non-obvious, actionable facts reach the consumer as generated output … : **every `freeform`
> and `policy` column is index-free**, and `wrkflw_human_task.claimed_by` is not."

`docs/specs/2026-08-22-at-rest-posture.md`, "What `SECURITY.md` gains" — i.e. **this is published to
the consumer**:
> "the two sentences that fall out of the classification and are the actionable content:
> **every `freeform` and `policy` column is index-free and can be encrypted without breaking an
> index**"

**The counterexample, executed.** The classification (spec, "The classification") states:
> "**`casbin_rule`** (8) — scalar: `id` · policy: `ptype`, `v0`, `v1`, `v2`, `v3`, `v4`, `v5`"

and the casbin DDL declares:
```
$ cat internal/authz/casbin/migrations/0001_casbin_rule.sql
    id    BIGSERIAL PRIMARY KEY,
    ptype TEXT NOT NULL,
    ...
CREATE INDEX casbin_rule_ptype_idx ON casbin_rule (ptype);
```
⇒ **`casbin_rule.ptype` is classified `policy` and carries `casbin_rule_ptype_idx`.**
⇒ `casbin_rule.id` is classified `scalar` and is a `BIGSERIAL PRIMARY KEY`, also keyed.

So over the **87 columns the document publishes**, the keyed census is 29, not 27, and by class it is
`reference` 15, `scalar` **8**, `timestamp` 4, `actor` 1, **`policy` 1** — not the four-entry map the
plan asserts.

**How the false claim was produced — and why nothing in the bundle can catch it.** E9 is *correctly
scoped*: "27 of **79**" — the `wrkflw_*` census, which by construction excludes `casbin_rule`. Every
restatement one level up **drops the scope** and asserts the property over the whole classification,
including the phrase "**both policy locations**", which explicitly names `casbin_rule` as one of them.
This is exactly the CLAUDE.md Premise Discipline failure mode — *"the false claims that survive review
are almost never in the detailed reasoning — they are the summary sentence appended to it,
over-generalising what it compressed"* — and the ADR-0186 lesson *"a boundary derived correctly at one
level, then asserted one level up without re-derivation."*

The prescribed guard **inherits the same blind spot**. Plan Task 5 Step 1:
```go
for k, col := range schemas["postgres"].Columns {
    if k.Table == "casbin_rule" {
        continue // not part of the 79-column wrkflw_* census (E4)
    }
    ...
}
assert.Equal(t, map[atrest.Class]int{
    atrest.ClassReference: 15, atrest.ClassScalar: 7,
    atrest.ClassTimestamp: 4,  atrest.ClassActor: 1,
}, byClass, "zero freeform and zero policy columns are keyed — the actionable finding (E9)")
```
The `continue` removes the only counterexample, and the assertion's own message then claims the
property the fixture excluded. The plan even flags this map as "the load-bearing assertion" and as
the claim "expressed as an equality rather than as prose, so it cannot rot" — it is neither: it is
scoped-true and its message is false.

**The consumer harm is the one the ADR is written to prevent.** A consumer told "both policy
locations can be encrypted without breaking an index" who then encrypts `casbin_rule.ptype`
non-deterministically loses `casbin_rule_ptype_idx` — and casbin's DB adapter filters on `ptype`
on the policy-load path, so this is a query the deployment actually runs. The published document
would have caused the harm, which is the exact sentence in ADR-0187's Context: *"a consumer who
encrypts the columns we name and leaves the rest in the clear has been harmed by our documentation."*

**Related scope-drop, same cause.** E10's "`postgres: CREATE INDEX=11`" is also a store-directory
number: the real Postgres+`FromDB` deployment declares **12** (11 + `casbin_rule_ptype_idx`). E10
does not state that scope.

**Proposed fix.**
1. **Delete the `continue` in Task 5's `TestKeyedLowerBound_Postgres`** and assert the true full-87
   census: keyed 29; `byClass` = `{reference: 15, scalar: 8, timestamp: 4, actor: 1, policy: 1}`.
   Keep a **second** assertion for the `wrkflw_*`-only 27/15/7/4/1 if that scoped number is still
   wanted, but label it as scoped in the assertion message.
2. **Rewrite the published sentence** to the true, still-actionable form, and derive it rather than
   hardcode it: *"Every `freeform` column is index-free. Of the `policy` columns,
   `wrkflw_human_task.eligibility` and `casbin_rule.v0`–`v5` are index-free, but **`casbin_rule.ptype`
   carries `casbin_rule_ptype_idx`** — encrypting it non-deterministically costs the policy-load
   lookup."* This is a **better** deliverable than the original: it now names two columns a consumer
   must think twice about (`wrkflw_human_task.claimed_by`, `casbin_rule.ptype`) instead of one.
3. **Make the sentence generated, not typed.** Task 6 step 3 items 2–3 must emit the
   index-free/keyed split by **computing** it from `Keys` and `Class`, so it cannot be restated
   wrongly. A hardcoded string in `render.go` reproduces this exact defect the next time an index is
   added.
4. **Correct ADR decision 8, ADR Consequences → Good, spec D8 and spec "What `SECURITY.md` gains"**,
   and add an `E9b` to the measurements file recording the casbin index and the 29-column full census
   — the measurement that refuted the claim, per rule #11.
5. Add the scope label to E10 (`CREATE INDEX=11` is the **store** migrations; a Postgres + `FromDB`
   deployment has 12).

---

## F9 — MAJOR — The `casbin_rule` optionality statement misleads in BOTH directions, and the harmful direction is the one a MySQL/SQLite consumer reads

**Quoted, `docs/specs/2026-08-22-at-rest-posture.md`, "What `SECURITY.md` gains":**
> "an explicit statement that **`casbin_rule` exists only under Postgres + `FromDB`**"

**Quoted, `docs/adr/…0187…md` decision 4:**
> "8 `casbin_rule` columns (Postgres and `FromDB` only)"

**Why it fails — direction 1 (false negative, the harmful one).** "Postgres" in that sentence is
ambiguous between *the wrkflw store's dialect* and *the pool casbin is given*. They are **independent**:
```go
// casbinauthz/casbinauthz.go:92
func FromDB(ctx context.Context, pool *pgxpool.Pool, opts ...DBOption) Option
// casbinauthz/casbinauthz.go:236
func MigrateCasbin(ctx context.Context, pool *pgxpool.Pool) error
```
Both take an arbitrary `*pgxpool.Pool` with no coupling to the store's dialect or connection. Nothing
prevents — and for a SQLite/embedded deployment nothing is unusual about — a consumer running the
wrkflw store on **MySQL or SQLite** while pointing `FromDB` at a Postgres pool for policy. That
consumer reads "exists only under Postgres" beside a table whose MySQL and SQLite type columns are
blank for `casbin_rule`, concludes the row does not apply to them, and **omits their live policy table
from their data inventory** — the same class of omission (a policy table missing from the
enumeration) that ADR-0187 was cut out of ADR-0186 to fix.

**Direction 2 (false positive, milder).** A Postgres consumer using `FromStrings` or `FromEnforcer`
(E3: "**Only `FromDB` puts policy in a table**") sees 8 `casbin_rule` rows presented with identical
visual weight to the 79 always-present ones, in a table whose columns are — per the spec — `table`,
`column`, `class`, physical type per dialect, `keyed` per dialect. **There is no column that carries
"present when".** Optionality lives entirely in one prose sentence somewhere above 87 rows.

**Proposed fix.**
1. Reword the generated sentence to name the *pool*, not the deployment dialect: *"`casbin_rule` is
   created only by `casbinauthz.MigrateCasbin`, and holds policy only under the `FromDB` policy
   source. It is a **Postgres** table, applied to whatever pool you pass — **including when the
   wrkflw store itself runs on MySQL or SQLite**. `FromEnforcer` and `FromStrings` store no policy in
   any table."*
2. Render `casbin_rule` in its **own sub-section** under a heading such as
   `### Optional — casbin policy store (created by MigrateCasbin, used by FromDB)`, rather than as
   eight more rows of the main table. Blank per-dialect cells inside one 87-row table read as "no
   type in that dialect", not as "this table may not exist".
3. Carry the distinction in data, not prose: add a `Presence` field to the `MigrationSet` declaration
   (`"always"` vs a free-text condition) and emit it, so the optionality cannot be edited away
   without touching Go.

---

## F10 — MAJOR — D1's "structurally unrepeatable" claim is FALSE as stated: discovery is keyed on a directory NAME, which is a convention with no enforcement

**Quoted, `docs/adr/…0187…md` decision 1:**
> "**1. Migrations are discovered by glob (`**/migrations/*.sql`), never listed.** A hardcoded
> directory list is what lost `casbin_rule`. Discovery plus rule 5 below means a fifth migration
> directory produces unclassified columns, which **fails the build** — the fourth-directory failure
> becomes **structurally unrepeatable** rather than remembered."

**Quoted, plan Task 2 Step 3:**
> "`DiscoverMigrationDirs` walks `root` and returns every directory named `migrations` **or** whose
> parent is named `migrations`, containing at least one `.sql` file"

**Why it fails.** The mechanism is not structural — it is a **naming convention**. A fifth set placed
in `internal/foo/sql/`, `internal/foo/schema/`, or `internal/foo/ddl/` is invisible: no unclassified
column, no failing build, and the omission is exactly as silent as the `casbin_rule` omission the
decision claims to make unrepeatable. The failure class is not closed; it is renamed.

The converse leaks too: a `.sql` file that sits in a `migrations` directory but is **not** embedded
(a leftover, a fixture) is parsed and classified, publishing columns of a table nothing creates.

**The authoritative ground truths already exist in the repo and neither is used.** Executed:
```
$ find . -name '*.sql' | sort            # 4 files, all migrations, zero fixtures
./internal/authz/casbin/migrations/0001_casbin_rule.sql
./internal/persistence/store/migrations/{mysql,postgres,sqlite}/0001_init.sql

$ grep -rn "go:embed" --include='*.go' . # 4 directives — what production ACTUALLY applies
internal/persistence/store/migrate_{mysql,postgres,sqlite}.go, internal/authz/casbin/migrate.go
```

**Proposed fix (either alone closes the class; both is better and both are cheap today):**
1. **Discover by extension, not by directory name.** `DiscoverMigrationDirs` walks `root` for
   `*.sql` files and groups by containing directory, with an **explicit, reviewed** exclusion list for
   non-migration SQL. Today that list is empty (`find` returns exactly the four migration files), so
   the change costs nothing and makes the ADR's "structurally unrepeatable" sentence true. A future
   `testdata/*.sql` fixture then requires someone to write down an exclusion — a decision, not a
   silence, which is the same shape D8/E15 chose for the FK rule.
2. **Cross-check discovery against `//go:embed`.** Add a test that extracts every `//go:embed`
   pattern from the repo's `*.go` files, resolves it against its package directory, and asserts the
   resulting directory set equals `DiscoverMigrationDirs(root)`. This closes the converse (a
   discovered-but-unapplied file) and ties the classification to what is actually shipped rather
   than to what is on disk.
3. Until one of those lands, **downgrade the ADR sentence** from "structurally unrepeatable" to
   "closed for any migration set following the `migrations/` naming convention" — a hedge that is at
   least true.

---

## F11 — CRITICAL — MySQL declares `wrkflw_journal.trigger_`, not `trigger`. The bundle never mentions it once, the union is 88 keys not 87, and the delivery CANNOT reach green as planned

**Quoted, `docs/specs/2026-08-22-at-rest-posture.md`, "The classification":**
> "**`wrkflw_journal`** (6) — reference: `instance_id` · scalar: `seq`, `kind` · freeform: `trigger`
> · timestamp: `occurred_at`, `applied_at`"

**Quoted, `docs/specs/2026-08-22-adr-0187-measurements.md` E4 heading:**
> "## E4 — Column census: 79 `wrkflw_*` columns, **identical across all three dialects**"

**Quoted, E6:**
> "87 classification rows, checked against the **87** machine-dumped `table.column` keys with `comm`:
> `unclassified … <empty>` / `stale … <empty>` / `EXACT MATCH: yes`"

**Quoted, plan Task 3 Step 1 and Task 2 Step 5:**
> "`assert.Len(t, atrest.Classification, 87, "87 = 79 wrkflw_* + 8 casbin_rule (E6)")`"
> "`assert.Len(t, schemas["postgres"].Columns, 87, …)` / `assert.Len(t, schemas["mysql"].Columns, 79)`"

**The refutation, executed.** MySQL cannot use `trigger` as a column identifier (reserved word), so
the migration declares `trigger_`:
```
$ sed -n '/CREATE TABLE wrkflw_journal/,/^);/p' internal/persistence/store/migrations/mysql/0001_init.sql
    trigger_    JSON         NOT NULL,
$ ... postgres/0001_init.sql
    trigger     JSONB       NOT NULL,
$ ... sqlite/0001_init.sql
    trigger     TEXT    NOT NULL,
```
Mechanical set diff of the column names in all three files:
```
postgres: 79 columns   mysql: 79 columns   sqlite: 79 columns
=== postgres vs mysql symmetric difference ===
wrkflw_journal.trigger
        wrkflw_journal.trigger_
=== postgres vs sqlite symmetric difference ===
(empty)
=== UNION size across all three ===
      80
```
⇒ the union of `wrkflw_*` `table.column` keys is **80**, not 79. With casbin the guard's universe is
**88**, not 87.

**The bundle never mentions this.** `grep -niE "trigger_|reserved|JournalTriggerColumn|normaliz"`
across all four bundle documents returns exactly one hit — `wrkflw_timers.trigger_kind` /
`trigger_payload`, an unrelated substring match. Fifteen measurements and none of them opened the
MySQL journal table.

**What breaks, concretely:**

1. **The completeness guard fails and the delivery cannot go green.** Plan Task 3 Step 1 builds
   `inSchema` as the **union over all dialects**; `wrkflw_journal.trigger_` has no classification
   entry, so `unclassified = [wrkflw_journal.trigger_]` forever. An implementer will hit this at
   Task 3 Step 4 ("GREEN") and have to invent a resolution the bundle never adjudicated.
2. **Three hardcoded numbers are wrong**: `Classification` len 87, `schemas["postgres"].Columns` len
   87 (that one is fine — postgres has 79 + 8), and the per-class `freeform` count 11 (it becomes 12
   if `trigger_` is classified as an 88th entry).
3. **E4's heading is false as written.** The three dialects have identical column *counts*, not
   identical column *names*. This is the CLAUDE.md quantifier failure — "identical" asserted over a
   measurement that only established "79 = 79 = 79".
4. **E6's measurement does not correspond to the guard it certifies.** E6 dumped **87** keys and got
   an exact match; the prescribed guard compares against the **88**-key union. E6's dump was
   therefore one dialect (79) + casbin (8), not the union. A measurement that certifies a set the
   test does not use is exactly what the evidence file exists to prevent.
5. **My F1 replacement invariant is affected too**: `TestColumnSetIsIdenticalAcrossDialects` must
   carry this one documented exception, or it fails on day one.

**The repo already solved this, and E12 cites the very file without noticing.**
`internal/persistence/store/migration_parity_test.go:99-112`:
```go
// normalizeMySQLTriggerColumn renames the MySQL-specific journal payload column
// name back to the canonical name used by Postgres and SQLite. MySQL disallows
// "trigger" as a column identifier (reserved word) …
mysqlCol := dialect.NewMySQL().JournalTriggerColumn()        // "trigger_"
canonicalCol := dialect.NewPostgres().JournalTriggerColumn() // "trigger"
```
Its own comment notes that sourcing the name from the `dialect` package "stays in sync with the
migration automatically" — the convention this bundle should reuse rather than rediscover, per the
recorded ADR-0186 lesson.

**Proposed fix.**
1. **Normalize at parse time, sourced from the dialect package** — not from a literal. `LoadSchemas`
   maps the MySQL journal payload column back to the canonical name using
   `dialect.NewMySQL().JournalTriggerColumn()` / `dialect.NewPostgres().JournalTriggerColumn()`,
   exactly as `normalizeMySQLTriggerColumn` does. The classification stays at 87 keys and the plan's
   numbers stand.
2. **But the published document MUST still name the real MySQL column.** A MySQL DBA doing a data
   inventory who is told to look at `wrkflw_journal.trigger` will not find it. Add a per-dialect
   **physical name** annotation beside the per-dialect physical type — populated only where it
   differs from the canonical key — and emit a footnote for this row. Without it, the deliverable is
   wrong for one of its three supported dialects in exactly the "column you cannot find" direction.
3. **Add the divergence as a first-class test**, so a second reserved-word rename cannot land
   silently: assert the set of canonical `ColumnKey`s is identical across dialects **after**
   normalization, and that the set of normalizations applied is exactly the one documented (today:
   `{mysql: wrkflw_journal.trigger_ → trigger}`). A second entry appearing without a decision then
   fails the build.
4. **Correct E4's heading** to "79 `wrkflw_*` columns in every dialect; the column *names* are
   identical except `wrkflw_journal.trigger` (MySQL: `trigger_`, reserved word)", and **re-run E6's
   `comm` against the UNION of all three dialects**, recording the new output. E6 as it stands
   certifies a set the guard does not compare against.

---

## F12 — MAJOR — The author states the cross-check-coverage hole and then LAUNDERS it: the plan contains none of the fix, and Task 7 invents three helpers no task declares

**Quoted, `docs/specs/2026-08-22-at-rest-posture.md`, author's interaction pass:**
> "**D7 × D1 (discovery).** ⚠ The cross-check reuses helpers that filter `LIKE 'wrkflw_%'` (E2), so
> `casbin_rule` needs its own cross-check — which the plan's task 7 adds explicitly. **But a FIFTH
> migration set creating non-`wrkflw_` tables would be discovered, parsed and classified while being
> silently absent from the cross-check.** The guard that fails on an unclassified column has no
> counterpart that fails on an uncross-checked table. **Stated as a known hole; a cheap fix is a test
> asserting every parsed table appears in some cross-check.**"

**Why it fails.** The fix is named, costed as "cheap", and then **not written into the plan**:
```
$ grep -niE "every parsed table|appears in some cross-check|uncross|cross-check.*coverage" \
    docs/plans/2026-08-22-at-rest-posture.md
GREP EXIT=1   (no matches)
```
Task 7 has five steps and none of them adds it. Rule #9's requirement is that findings be
*adjudicated*, not that they be *mentioned*: writing "stated as a known hole" beside a fix the author
calls cheap, and then omitting the fix, converts a solvable defect into an accepted cost by
paperwork. This is the pattern the brief asked me to check for, and this is the clearest instance.

**End-to-end trace of the hole** (a fifth set adding a non-`wrkflw_`, non-casbin table `tenant_config`):
| stage | outcome |
|---|---|
| discovery | finds it (if the dir is named `migrations` — see F10) |
| parser | parses it (if `CREATE TABLE` — see F4) |
| completeness guard | **fires** → developer classifies the columns. Correct. |
| `Render` / `SECURITY.md` | emits `tenant_config` rows |
| Task 7 `parsedColumnNames(parsed["postgres"], "wrkflw_")` | filters `tenant_config` **out** |
| Task 7 `liveColumnNames(introspectPostgres(...))` | helper's SQL filters `LIKE 'wrkflw_%'` → **out** |
| `assert.ElementsMatch` | **passes** — both sides empty of the new table |
⇒ the published rows for the new table are never validated against a real database **even with
Docker running**. D7's honesty mechanism has a hole exactly the shape of the one D1 exists to close.

**Secondary defect in the same task.** Task 7's code uses three helpers —
`parsedColumnNames`, `liveColumnNames`, `columnsOfTable` — that appear in **no** task's `Produces`
block, while the plan's self-review asserts:
> "**Type consistency:** `ColumnKey`, `Column`, … `MigrationSet` — **each defined in exactly one
> task's Produces block** and spelled identically at every later use."
That enumeration is incomplete by three symbols. Worse, Task 7 Step 2 states the RED as
> "the helpers `parsedColumnNames` / `liveColumnNames` do not exist. Compile error."
omitting `columnsOfTable`, which also does not exist (`grep -n "func columnsOfTable"
internal/persistence/store/migration_parity_test.go` → no match).

**Proposed fix.**
1. **Write the stated fix into Task 7** as a new step: `TestEveryParsedTableIsCrossChecked` — collect
   the distinct table names in `LoadSchemas(root)`, collect the table names each cross-check
   assertion covers, and fail on any parsed table in neither. Seed it with the two coverage sets that
   exist today (`wrkflw_%` and `casbin_rule`) so a third table forces someone to add a cross-check.
   This is the counterpart the author correctly identified as missing.
2. Add `parsedColumnNames`, `liveColumnNames` and `columnsOfTable` to Task 7's `Produces` block with
   signatures, and correct Step 2's RED text to name all three.
3. Correct the self-review's "Type consistency" claim, or scope it ("the exported `internal/atrest`
   API"), since as written it is an over-reaching quantifier of the kind the plan's own Task 8 Step 3
   is supposed to hunt.

---

## F13 — MAJOR — "dbtest skips when unavailable" is FALSE. `internal/dbtest` has no `t.Skip` at all; it FAILS. A prescribed verification step tells the implementer to look for an outcome that cannot occur

**Quoted, `docs/plans/2026-08-22-at-rest-posture.md` Task 7 Step 1 (in the prescribed test body):**
> "`// Postgres + MySQL: Docker-gated; dbtest skips when unavailable.`"

**Quoted, Task 7 Step 4:**
> "⚠ Confirm with `-v` that the subtests **ran** rather than skipped. **A `dbtest` skip is not a
> pass**, and reporting it as one is the exact failure CLAUDE.md's Docker carve-out forbids."

**The refutation, executed.**
```
$ grep -rn "Skip" internal/dbtest/ --include='*.go' | grep -v _test.go
GREP-EXIT=1        # zero t.Skip calls in non-test dbtest code
```
`RunTestDatabase` ends its container resolution with a hard failure, not a skip
(`internal/dbtest/postgres.go:217`):
```go
	require.NoError(t, sharedErr)
```
and its own doc comment says "**Requires** a running Docker daemon, UNLESS `EnvPostgresDSN` points at
an already-running server".

⇒ Without Docker (and without `WRKFLW_TEST_POSTGRES_DSN`), the prescribed cross-check **FAILS**; it
does not skip. Three consequences:
1. The prescribed comment shipped into the committed test file is a false claim about current
   behaviour — the exact defect the Delivery Gate item 2 sweep exists to catch, pre-planted in the
   plan.
2. Task 7 Step 4's verification instruction is unactionable: it tells the implementer to distinguish
   *ran* from *skipped* when the real outcomes are *passed* from *failed*.
3. ADR-0187's Consequence — "On a machine with no Docker daemon, a parser bug and a classification
   bug are indistinguishable" — is optimistic in the wrong direction. On such a machine the whole
   `internal/persistence/store` package is red for an unrelated reason, so the cross-check yields no
   signal at all rather than an ambiguous one.

**A fourth, structural issue in the same test.** `TestAtRestParseMatchesLiveIntrospection` runs
SQLite, Postgres and MySQL **inline in one function**. A Postgres failure aborts before MySQL is
compared, so one dialect's regression hides the next one's. Split them into `t.Run` subtests so all
three report.

**Proposed fix.** Replace the comment with the truth — `// Postgres + MySQL: these REQUIRE Docker (or
WRKFLW_TEST_POSTGRES_DSN / WRKFLW_TEST_MYSQL_DSN); dbtest fails, it does not skip.` — rewrite Task 7
Step 4's instruction to check pass/fail rather than ran/skipped, split the three backends into
subtests, and amend the ADR Consequence to say that without Docker the cross-check produces **no
signal**, which is what makes the fail-closed parser of F4 the load-bearing mitigation rather than an
optional extra.

---

## F14 — MAJOR — The guard's failure text explains WHY but never WHAT TO DO, and the anti-rot delivery hardcodes ~15 counts that must be hand-updated on every schema change

**(a) Failure text.** Four of the five prescribed assertion messages state a rationale and no action.

| test | prescribed message | what a developer learns |
|---|---|---|
| completeness (Task 3) | "every stored column must carry a stated class — a consumer who encrypts the columns we name and leaves the rest in the clear has been harmed by our documentation" | why it matters; **not** that they must edit `internal/atrest/classification.go`, nor that they must then run `scripts/gen-at-rest.sh` |
| declaration (Task 2) | "a discovered migration directory with no MigrationSets entry must fail: its columns would otherwise never be classified" | why; **not** the file (`internal/atrest/discover.go`) or the shape of an entry |
| census (Task 2) | "79 wrkflw_* + 8 casbin_rule (E3, E4)" | a bare number diff; **no** action at all |
| keyed census (Task 5) | "zero freeform and zero policy columns are keyed — the actionable finding (E9)" | ⚠ **actively wrong on failure**: the message asserts the property that has just become false |
| drift (Task 6) | "SECURITY.md's at-rest block is stale — run `scripts/gen-at-rest.sh`" | ✅ the only one that names an action |

Task 6's message is the model; the others should follow it. A schema change today produces **two
sequential red runs** (completeness, then drift) and the first one does not point at the second.

**(b) The anti-rot delivery hardcodes its own enumerations.** ADR-0187's thesis is that hand-written
counts rot — "it has now failed four times". The prescribed guard then hardcodes, across two test
files: `87` (Task 2 postgres), `79` (mysql), `79` (sqlite), `87` (Task 3 classification length), the
six per-class counts `27/19/17/11/8/5`, `27` (Task 5 keyed), and the four `byClass` entries
`15/7/4/1` — roughly **fifteen literals** that every future schema change must update by hand, none
of whose failure messages says so. Two of them are already wrong (F8's `byClass`, F11's `87`), which
is the thesis proving itself on the delivery's own artifacts.

**Proposed fix.**
1. Give every assertion an action clause naming the file to edit and the script to run afterwards.
   Rewrite Task 5's message to what it must say **when it fails**: *"an index now covers a
   `freeform`/`policy` column. That column can no longer be encrypted with a non-deterministic scheme
   without losing the index — re-derive the actionable sentences in `render.go` and amend ADR-0187
   D8."*
2. Keep **one** deliberate total as a tripwire (the classification length) and **derive** the rest:
   the per-class counts and the keyed census are facts about the map, so assert relationships
   (e.g. "the per-class counts sum to `len(Classification)`", "no `freeform` column is keyed" as a
   predicate over the data) rather than transcribed integers. A predicate cannot rot; a literal must
   be maintained.
3. Add the update instruction to the one literal that survives: *"if you added a column deliberately,
   update this number, classify it in `classification.go`, and run `scripts/gen-at-rest.sh`."*

---

## F15 — MAJOR — One class per column cannot express "holds both", and D4's own justification for splitting `actor` from `freeform` makes the omission actively harmful

**Quoted, `docs/specs/2026-08-22-at-rest-posture.md` D4:**
> "**`actor` vs `freeform`** — 'which columns hold personal data' is a different question from 'which
> columns hold secrets', and a two-class `sensitive`/`metadata` split collapses them."

**Quoted, D2:**
> "Each `table.column` gets **exactly one class**"

**Why it fails.** The split is justified by the personal-data question, and the model then answers
that question **wrongly for the columns most likely to hold personal data**. The `freeform` class is
defined as "arbitrary process data — **assume it carries secrets**". Arbitrary process data is
exactly where a consumer's personal data lives: `wrkflw_human_task.vars` (task variables),
`wrkflw_instances.snapshot` (process-instance variables), `wrkflw_chain_links.start_vars`,
`wrkflw_journal.trigger` (trigger payloads), `wrkflw_definitions.definition`. A DPO or engineer who
takes the document at its word and answers "which columns hold personal data?" with the **5 `actor`
columns** has excluded the 11 `freeform` ones — and `snapshot` and `vars` are, in practice, the most
likely location of personal data in the whole schema.

This is the same harm direction as ADR-0187's founding sentence, applied to the other question the
classification claims to answer.

**Proposed fix (either; the second is nearly free).**
1. Make the class a **set** — `map[ColumnKey][]Class` — so `freeform` columns can also carry `actor`
   where they may contain principal data. This is the honest model but changes D3's map type, the six
   counts, and the render, so it is a real scope increase.
2. **Cheaper and sufficient:** keep one class, and make the generated class-definition text carry the
   containment explicitly — *"`actor` marks columns whose **entire purpose** is to identify a
   principal. It is **not** an exhaustive list of where personal data can be found: every `freeform`
   column carries arbitrary consumer-supplied process data and must be assumed to contain personal
   data as well as secrets."* Emit it from the generator, beside the class definitions, so it cannot
   be edited out of `SECURITY.md`.
3. Either way, amend D4's justification paragraph — as written it claims the split *answers* the
   personal-data question, and it does not.

---

## F16 — MINOR — "The classification is documentation-only; nothing at runtime reads it" is a rot vector no guard covers: a column's ROLE can change with no schema change at all

**Quoted, `docs/adr/…0187…md` Consequences → Neutral:**
> "The classification is documentation-only: nothing at runtime reads it, so it lives in
> test-adjacent code and adds no production surface."

**Why it fails.** Every guard in D6 keys on the **schema** — a column appearing, disappearing, or its
DDL changing. None keys on what the column **holds**. A column can be repurposed with zero schema
delta and the classification silently becomes wrong, with all five guards green:

- `wrkflw_call_links.claimed_by` is `reference` **because** `WithCallLinkLease(owner string, ttl)`
  puts a worker lease owner there (E7). If a caller starts passing a human operator id as the lease
  owner — nothing forbids it, the parameter is a bare `string` — the column becomes `actor` and the
  document keeps saying `reference`. This is the *same* trap D3 exists to prevent, arriving through
  behaviour instead of through a map key.
- `wrkflw_outbox.topic` and `wrkflw_processed_message.subscriber` are `reference` on the grounds that
  they are consumer-authored strings (open question 1). A consumer who templates instance data into a
  topic name puts process data in a `reference` column.

Nothing detects either. The only anchor is the reviewer of the feature commit that changes the write
site — and that reviewer has no reason to open `internal/atrest/classification.go`.

**Proposed fix (cheap, no new mechanism).** Carry the **write site** in the classification as a
comment per group — e.g. `// wrkflw_call_links.claimed_by: written by WithCallLinkLease(owner, ttl)
in internal/persistence/store/call_links.go — a worker lease owner, NOT a person (ADR-0187 D3/E7)`.
Then a `grep` for the symbol from the changing code finds the classification, and a rename of the
writer surfaces it. State in ADR-0187 Consequences that role drift is **out of the guard's reach** and
that the comments are the only control, rather than leaving "documentation-only" to read as
"therefore low-risk".

---

## F17 — MINOR — The `keyed` derivation has no prescribed test for INLINE `PRIMARY KEY` or INLINE `UNIQUE`, which is how 14 of the real key declarations are written

**Quoted, plan Task 1 Step 3, the parser rules:**
> "4. … takes the first token as the column name, the second as the type;
> 5. **skips** body clauses beginning `PRIMARY`, `UNIQUE`, `FOREIGN`, `CONSTRAINT`, `KEY`, `INDEX`."

**Quoted, plan Task 5 Interfaces:**
> "Produces: `Column.Keys []string` populated with `"PK"`, `"UNIQUE"`, `"index"`, `"index-predicate"`."

Rule 5 handles a **table-level** `PRIMARY KEY (…)` clause; rule 4 reads a column's name and type and
discards the rest of the line. **No rule reads `PRIMARY KEY` or `UNIQUE` off the tail of a column
declaration**, and neither of the plan's `PK`-bearing trap cases uses that form — both are
`PRIMARY KEY (a)` / `PRIMARY KEY (a, b)` table-level clauses.

**Evidence (executed).** The real corpus is mostly the untested form:
```
$ grep -nE "^\s+\w+\s+\w+.*PRIMARY KEY" .../migrations/*/0001_init.sql .../casbin/migrations/*.sql
  → 11 inline PRIMARY KEY declarations (postgres 3, mysql 3, sqlite 4, casbin 1)
$ grep -cE "^\s+PRIMARY KEY \(" .../postgres/0001_init.sql
  → 6 table-level
$ grep -rniE "unique" .../migrations/
  → 3 inline UNIQUE (wrkflw_outbox.dedup_key, one per dialect) — ZERO table-level UNIQUE
```
⇒ `UNIQUE` exists **only** in the inline form the plan never tests, so the `"UNIQUE"` key kind ships
with no prescribed case at all.

Severity is Minor rather than Major because Task 5's real-schema census (`27` keyed) would fail loudly
if inline forms were dropped — the implementer is caught, late. But the plan's own claim that the
three trap cases "are the parser's real acceptance test" is short by the two commonest declarations
in the corpus.

**Proposed fix.** Add two cases to `TestParseSQL_RealWorldTraps`:
`{name: "inline PRIMARY KEY on the column line", src: "CREATE TABLE t (a TEXT PRIMARY KEY, b TEXT);"}`
asserting `Keys` contains `"PK"` for `a` and is empty for `b`; and
`{name: "inline UNIQUE", src: "CREATE TABLE t (a TEXT NOT NULL UNIQUE, b TEXT);"}` asserting
`"UNIQUE"`. Both fail today against Task 1's step-3 implementation, which reads only name and type.

---

## F18 — MAJOR — The author's interaction pass picked the wrong three: its three UNRESOLVED items are the LEAST severe interactions in the bundle, and the three Criticals here are all pairs it did not derive

**Quoted, `docs/specs/2026-08-22-at-rest-posture.md`:**
> "Ten decisions ship together. Below is what each does to the others' premises, derived pairwise.
> The three marked **UNRESOLVED** are handed to the audit's interaction lens as its starting grid"

**Adjudicating the three it raised:**

| stated UNRESOLVED | my verdict |
|---|---|
| **D2 × D8 (presentation)** — per-dialect `keyed`/type columns beside an invariant `class` may read as all-per-dialect | Real, **Minor**. A preamble sentence fixes it. Correctly identified, over-weighted. |
| **D4 × open questions 1 & 2** — reclassifying `candidates` or `subscriber`/`topic` breaks two hardcoded counts | Real, but this is a **process note about the adjudication step**, not a design interaction. It is also **subsumed by F14(b)**, which shows ~15 hardcoded counts are brittle for reasons unrelated to those two questions. |
| **D6 × D9 (`-update` propagates wrong)** | Real, **but mis-scoped** — see F7. It bundles an unsolvable judgement problem with a solvable mechanical one (`Render` undefined on an unclassified column; `gen-at-rest.sh` verifying one test out of six) and books both as "weak on purpose". |

**The pairs it did NOT derive — every Critical in this audit is one of them:**

- **D3 × D6 clause 4** → **F1 (Critical)**. D3 makes the classification a `map[ColumnKey]Class` with
  no dialect axis; D6 clause 4 asserts the class does not differ **across dialects**. D3 deletes the
  dimension D6 clause 4 measures. The pass *did* derive `D3 × everything`, but only against `keyed`
  ("The key shape is a third dimension away from `keyed` … Consistent") — it checked D3 against the
  one place the missing dimension is harmless and not against the one place it is fatal.
- **D1 × D8** → **F8 (Critical)**. D1's whole achievement is pulling `casbin_rule` into the
  classification. That table contains `casbin_rule_ptype_idx` on a column D4/D5 class `policy` —
  so **D1 is what falsifies D8's headline claim** that zero `policy` columns are keyed. A textbook
  "the fix opens a hole in the decision it ships beside", and the two decisions are the two the ADR
  is proudest of.
- **D1 × D2 × the corpus** → **F11 (Critical)**. D1 pulls in all four migration sets and D2 asserts a
  single class per `table.column` across dialects; MySQL's reserved-word `trigger_` means the key
  sets are not identical, so the universe is 88 keys and the classification covers 87. The pass
  derived `D1 × D2` and stopped at "dialect cannot be inferred from a path" — the declaration
  problem — never reaching "do the dialects agree on column NAMES".
- **D9 × D6** (mechanical half) → **F7**; **D7 × D1** was derived and then **laundered** → **F12**.

**Proposed fix.** Re-run the interaction pass with the pairing driven by *what each decision removes
or adds to the data model*, not by *what each decision is about*: D3 removes a dimension (→ check
every guard that quantifies over it); D1 adds a table (→ recheck every claim quantified over "all
columns"); D2 asserts cross-dialect sameness (→ enumerate the dialects' actual differences, which is
one `comm` away and would have found `trigger_` in under a minute). Record the three new
UNRESOLVED/RESOLVED entries above in the spec's interaction pass so the next reader sees the pairs
that actually mattered.

---

## F8 — CORRECTION AND EXTENSION (self-refuted mechanism, conclusion strengthened)

In F8 I wrote that "casbin's DB adapter filters on `ptype` **on the policy-load path**". I executed
that claim and it is **false**; the conclusion is unaffected and in fact gets stronger.

```
$ grep -rn "casbin_rule" --include='*.go' . | grep -v _test
internal/authz/casbin/pg_adapter.go:51:  `SELECT ptype, v0, v1, v2, v3, v4, v5 FROM casbin_rule ORDER BY id`   <- LoadPolicy: full scan, no ptype predicate
internal/authz/casbin/pg_adapter.go:136: `DELETE FROM casbin_rule WHERE ptype=$1 AND v0=$2 AND …`               <- RemovePolicy
internal/authz/casbin/pg_adapter.go:148-164: `DELETE FROM casbin_rule WHERE ptype = $1 [AND vN = $M]…`             <- RemoveFilteredPolicy
```
`LoadPolicy` scans; the index serves the **mutation** paths `RemovePolicy` and
`RemoveFilteredPolicy`.

**Two things this sharpens:**

1. Encrypting `casbin_rule.ptype` non-deterministically does not merely cost an index scan — it
   breaks the **equality predicate itself**. `DELETE … WHERE ptype = $1` would match zero rows and
   policy revocation would silently stop working. That is a correctness failure, strictly worse than
   the performance framing, and it makes the corrected published sentence more valuable, not less.
2. The same applies to `v0`–`v5`, which are `policy`, **unindexed**, and still appear in equality
   predicates at `pg_adapter.go:137` and `:159`. So even the columns for which "index-free" is
   literally true are **not** safe to encrypt non-deterministically. D8's lower-bound caveat is worded
   as:
   > "It is derived from DDL only; **query-level filtering in the store layer** is invisible to it"

   — and `internal/authz/casbin/pg_adapter.go` is **not the store layer**. The caveat as written does
   not cover the one place where the gap actually bites.

**Additional proposed fix (folds into F8's fix 2 and 4):** broaden the generated caveat from "the
store layer" to "**anywhere in this module** — including `internal/authz/casbin/pg_adapter.go`, whose
`RemovePolicy` / `RemoveFilteredPolicy` match on `ptype` and `v0`–`v5` by equality", and state
plainly that *index-free does not mean encryption-safe*: a column used in an equality predicate
breaks under any non-deterministic scheme whether or not an index exists. Without that sentence the
document's headline advice is unsafe for **seven** of the eight `casbin_rule` columns, not just for
`ptype`.

---

# Summary — FAILURE-MODES lens, ADR-0187 §AT-REST bundle @ `ebafdf0f`

| ID | Severity | One-line issue | Verdict |
|---|---|---|---|
| **F1** | **Critical** | The dialect-invariance guard (D6 clause 4) cannot fire: D3's `map[ColumnKey]Class` has no dialect axis. Fuzzed 200k inputs, 0 non-empty. The plan's own stated remedy does not cure it | **Confirmed by execution** — replace with two falsifiable invariants (column-set identity, type-divergence-is-only-the-timestamp-mapping) |
| **F2** | **Critical** | Task 4's "prove the pin fires" mutation exercises the completeness guard instead, and its Expected line wrongly claims staleness fires (`inSchema` is a union) | **Confirmed by execution** — retitle, correct Expected, add a second all-dialect deletion mutation |
| **F3** | Major | The staleness guard is never observed RED: the spec promises a bogus-row seeding that no plan step contains; an empty map makes it vacuous during the prescribed RED | **Confirmed by reading the prescribed steps** — insert the seeding step |
| **F4** | **Critical** | The parser FAILS OPEN on unrecognised statements. ADR-0132's stated forward policy is new numbered files (`ALTER TABLE`), which every non-Docker guard would silently miss — no unclassified column, no drift, all green | **Confirmed** (corpus has 0 `ALTER`, so fail-closed is free today) — whitelist statements and error out |
| **F5** | Major | The classification describes a **freshly migrated** DB; goose stores no checksum so in-place edits never re-apply. The repo already documents this at `migration_parity_test.go:230-236` and has already used the path (ADR-0159) | **Confirmed** — add a generated caveat + E16 |
| **F6** | **Critical** | No ordering is prescribed for `Render`; both inputs are maps. 995/1000 renders of the same 87-entry map differed. `scripts/gen-at-rest.sh` can never print its success line | **Confirmed by execution** — emit in declaration order; add a determinism test |
| **F7** | Major | The `-update` framing books a **mechanical** hole into the unsolvable **judgement** bucket: `Render`'s behaviour on an unclassified column is undefined, and `gen-at-rest.sh` verifies 1 test of 6 yet prints "verified" | **Confirmed** — `Render` fails closed; script runs the whole package; split the interaction entry in two |
| **F8** | **Critical** | The bundle's headline published claim — "every `freeform` and `policy` column is index-free … both policy locations can be encrypted without breaking an index" — is **false**: `casbin_rule.ptype` is `policy` and carries `casbin_rule_ptype_idx`. Task 5's test `continue`s past `casbin_rule`, so the guard can never detect it | **Confirmed by execution**. Corrected mechanism: the index serves `RemovePolicy`/`RemoveFilteredPolicy`, so encryption breaks the predicate, not just the scan. Also true of unindexed `v0`–`v5` |
| **F9** | Major | "`casbin_rule` exists only under Postgres + `FromDB`" misleads a MySQL/SQLite consumer (`FromDB` takes its own pool) into omitting a live policy table, and gives Postgres+`FromStrings` consumers 8 phantom rows with no "present when" column | **Confirmed** — reword to name the pool; render casbin in its own sub-section; carry presence in data |
| **F10** | Major | D1's "structurally unrepeatable" is false: discovery keys on the directory **name**. A fifth set in `internal/foo/sql/` is invisible; a non-embedded `.sql` in a `migrations/` dir is published | **Confirmed** — discover by `*.sql` with a reviewed exclusion list, and/or cross-check against `//go:embed` |
| **F11** | **Critical** | MySQL declares `wrkflw_journal.trigger_` (reserved word). The union is **88** keys, not 87; the classification covers 87; the completeness guard fails and the delivery cannot go green. The bundle never mentions it once; E4's "identical across all three dialects" is false and E6's `comm` used a universe the guard does not use | **Confirmed by execution** (`comm` diff: exactly one gap) — normalize via `dialect.*.JournalTriggerColumn()` and emit a per-dialect physical **name** |
| **F12** | Major | The spec names the "fifth set is never cross-checked" hole and calls the fix cheap; the plan contains **none** of it. Task 7 also invents 3 helpers no `Produces` block declares, contradicting the self-review | **Confirmed** (`grep` returns nothing) — add `TestEveryParsedTableIsCrossChecked` |
| **F13** | Major | "dbtest skips when unavailable" is false — `internal/dbtest` has zero `t.Skip`; it `require.NoError`s. A prescribed verification step tells the implementer to check for an outcome that cannot occur | **Confirmed by execution** — correct the comment, the step, and the ADR consequence |
| **F14** | Major | 4 of 5 assertion messages explain **why** and never **what to do** (Task 5's is wrong on failure); the anti-rot delivery hardcodes ~15 counts, two of which are already wrong | **Confirmed** — add action clauses; derive counts as predicates, keep one tripwire |
| **F15** | Major | One class per column cannot express "holds both". D4 justifies `actor` by the personal-data question, then omits `snapshot`/`vars` — the likeliest homes of personal data — from that answer | **Confirmed by reading D4 against the class table** — emit an explicit non-exhaustiveness sentence |
| **F16** | Minor | Role drift needs no schema change: `wrkflw_call_links.claimed_by` becomes `actor` the day a human id is passed to `WithCallLinkLease`. No guard covers it | **Confirmed** — carry write-site comments; say so in Consequences |
| **F17** | Minor | No prescribed test for inline `PRIMARY KEY` (11 occurrences) or inline `UNIQUE` (3, the only form in the corpus) — the two commonest key declarations | **Confirmed by execution** — add two trap cases |
| **F18** | Major | The author's interaction pass picked the three least severe pairs; F1, F8 and F11 are each a pair it did not derive — and **F8 is caused by D1**, the decision the bundle is built on | **Confirmed** — re-run the pass keyed on what each decision adds/removes from the data model |

**Counts: 18 findings — 6 Critical, 10 Major, 2 Minor.**
Critical: F1, F2, F4, F6, F8, F11. Major: F3, F5, F7, F9, F10, F12, F13, F14, F15, F18.
Minor: F16, F17.

**Bottom line for the controller.** Three of the six Criticals (F6 render determinism, F11 `trigger_`,
F1 vacuous pin) mean the delivery **cannot reach green as written** — they are blocking, not
advisory. One (F8) means the bundle's headline deliverable, the sentence it exists to publish, is
**false today** and its guard is scoped so it can never say so. One (F4) means the mechanism's
central promise — that the enumeration cannot rot silently — has an open door in the direction the
schema will next evolve. Every one of these was found by executing something; none is visible from
reading the documents against each other.

**Evidence artifacts:** probe module at `/private/tmp/claude-501/atrest-probe` (F1 fuzz, F2 guard
simulation, F6 determinism); column-set dumps at `/tmp/cols_{postgres,mysql,sqlite}.txt` (F11).

**Not verified (needs Docker):** nothing in this report required a container. The Docker-gated
cross-check's own behaviour (Task 7) is `UNVERIFIED (needs Docker)`; F13's finding is about
`dbtest`'s source, which I read rather than ran.
