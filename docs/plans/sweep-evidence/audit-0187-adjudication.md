# ADR-0187 §AT-REST — audit round 1, adjudication

**Date:** 2026-08-22 · **Bundle commit audited:** `ebafdf0f` · **Adjudicator:** controller (main session)

Four Opus lenses on detached worktrees at the bundle commit: `audit-0187-{execution,failure-modes,counting,interaction}.md` (3,612 lines total).

**64 findings — 17 Critical, ~30 Major, rest Minor/Medium.**

| lens | findings | Critical |
|---|---|---|
| counting | 17 | 4 |
| execution | 10 | 4 |
| interaction | 19 | 3 |
| failure-modes | 18 | 6 |

⚠ **Note the rate against the ADR-0186 trend** (seven rounds at 57–65 findings regardless of scope):
this bundle drew **64 on round 1**. That is consistent with the finding rate being a property of the
process rather than of the bundle. It is recorded here so a later round is judged against the trend
and not against a hope of convergence.

## Controller's own verification

⚠ **A review finding is a CLAIM NEEDING EXECUTION.** Every Critical below was re-derived by the
controller independently before being accepted. One finding's *mechanism* was wrong while its
conclusion held (see A7), and one finding was **refuted outright** (A10).

Controller's independent census, all four migration files, three dialects:

```
postgres: columns=87  keyed=29   actor=1 policy=1 reference=15 scalar=8 timestamp=4
mysql:    columns=79  keyed=28   actor=1 reference=15 scalar=7 timestamp=5
          UNCLASSIFIED in mysql: wrkflw_journal.trigger_
sqlite:   columns=79  keyed=28   actor=1 reference=15 scalar=7 timestamp=5
```

---

## ACCEPTED — Critical, design-changing

### A1 — The published safety sentence is FALSE (counting C3 · execution X9 · failure-modes F8; interaction I5 supplies a second mechanism)

**Verified:** `internal/authz/casbin/migrations/0001_casbin_rule.sql:12` —
`CREATE INDEX casbin_rule_ptype_idx ON casbin_rule (ptype);`. `ptype` is class `policy` by D4.

The bundle states, in E9, ADR decision 8, spec D8, the ADR's Consequences, **and** the prescribed
`SECURITY.md` text: *"every `freeform` and `policy` column is index-free and can be encrypted
without breaking an index"*, over **both** policy locations. One of those locations was **outside
the measurement**: E9 derived `keyed` over the 79 `wrkflw_*` columns and the conclusion was restated
over all 87.

⚠ **Second, independent mechanism (interaction I5):** `internal/authz/casbin/pg_adapter.go`
`RemovePolicy` runs
`DELETE FROM casbin_rule WHERE ptype=$1 AND v0=$2 AND v1=$3 AND v2=$4 AND v3=$5 AND v4=$6 AND v5=$7`.
So **all seven** policy columns are equality predicates, not merely `ptype`'s index. Encrypting any
of them non-deterministically breaks `RemovePolicy` and `RemoveFilteredPolicy`.

⚠⚠ **This indicts the reasoning, not just the number.** The bundle's own D8 caveat says `keyed` is
a **lower bound** blind to query-level filtering — and the bundle then published a safety claim that
only a complete bound could support. The caveat predicted the defect and the headline ignored it.

**Shape:** *"a boundary derived correctly at one level, then asserted one level up without
re-derivation"* — the exact ADR-0186 lesson quoted in `HANDOVER.md`, reproduced here. It is also the
delivery's founding defect (losing the casbin table) **relocated from discovery into derivation**.

**Aggravating:** plan Task 5 prescribes `if k.Table == "casbin_rule" { continue }` three lines above
the assertion the plan describes as unable to rot. It cannot rot because it skips its counterexample.

**FIX (accepted):**
1. Derive `keyed` over **every table and every dialect**, casbin included. Delete the `continue`.
2. **Withdraw the "safe to encrypt" sentence entirely.** Replace with the derived, per-column fact
   (`keyed` / not keyed, per dialect) plus an explicit, separate hazard statement that
   **query-level equality predicates exist and are NOT in the derivation** — naming
   `casbin_rule.{ptype,v0..v5}` as the known instance.
3. Corrected numbers: Postgres **29 of 87** keyed, **policy-keyed = 1**.

### A2 — The discovery glob finds 1 of 4 files, and loses the three the hardcoded list had (counting C5 · execution X8)

**Verified:** `glob.glob('**/migrations/*.sql', recursive=True)` →
`['internal/authz/casbin/migrations/0001_casbin_rule.sql']` **only**. The three dialect schemas live
one level deeper at `migrations/<dialect>/*.sql`.

Implemented literally, the census is **8** columns, not 87 — the mirror image of the omission D1
exists to close. E1 never checked it: its probe was `find . -name '*.sql'`, a **different method**
from the one D1 prescribes. Go's `filepath.Glob` has no `**` at all.

**FIX (accepted):** state the **rule**, not a glob — *"every directory named `migrations`, and every
direct child directory of one, containing at least one `.sql` file"* — in the ADR **and** the spec.
The plan's prose already said this; the durable record did not.

### A3 — MySQL names the journal column `trigger_` (counting C4 · execution X1 · interaction I1 · failure-modes F11 — all four lenses)

**Verified:** `internal/persistence/store/migrations/mysql/0001_init.sql:31` — `trigger_ JSON NOT NULL`.
`trigger` is reserved in MySQL. Controller's own parse reports
`UNCLASSIFIED in mysql: wrkflw_journal.trigger_`.

E4's *"79 columns, identical across all three dialects"* is true as a **count** and false as a
**set**. The cross-dialect key union is **88**, and the classification covers 87, so the prescribed
completeness guard **fails permanently** and plan Task 3 cannot reach green as written.

⚠ **The bundle never mentions this once across four documents and 15 measurements** — and the repo
already solved it in `normalizeMySQLTriggerColumn` (`migration_parity_test.go:92-113`), sourced from
`dialect.NewMySQL().JournalTriggerColumn()`, **in the very file E12 cites as the convention being
reused**. Fifth instance in this lineage of claiming a gap the repo had already filled.

**FIX (accepted):** normalize dialect reserved-word aliases to the canonical name via
`dialect.*.JournalTriggerColumn()` **before** the guard's set operations, exactly as the parity test
does. Classification stays at 87 keys. Record the normalization as an explicit decision, not an
implementation detail.

### A4 — The dialect-invariance guard is vacuous BY CONSTRUCTION (execution X4 · interaction I4 · failure-modes F1)

**Verified by reading the prescribed signature:**
`ClassDivergences(schemas map[string]Schema, cls map[ColumnKey]Class) []string`. `cls` has **no
dialect term**, so a class cannot differ per dialect and the result is empty for every possible
input. The failure-modes lens fuzzed it over 200,000 randomised inputs: **zero** non-empty results.
D6's fourth guarantee is **unrepresentable**, not merely unfalsified.

The plan's own "Known gap" note diagnosed the hazard and prescribed a remedy that does not cure it.

**FIX (accepted), and it is better than the thing it replaces:** delete the class-divergence guard.
Replace with a **key-set agreement invariant** — after normalization (A3), the `table.column` key
set must be identical across all three dialects, and the classification must cover that set exactly.
⭐ **This is a REAL assertion that FAILS TODAY** (on `trigger_` if normalization is dropped), turning
the bundle's one vacuous pin into its strongest RED.

### A5 — The parser is `CREATE TABLE`-only and FAILS OPEN (interaction I2 · failure-modes F4)

**Verified:** `docs/adr/0132-*.md:33` states future *"schema changes will resume as new numbered
files on top of the consolidated"* set — i.e. `ALTER TABLE … ADD COLUMN`. The prescribed parser
reads **zero** columns from such a file: the completeness guard stays silent, `SECURITY.md` asserts
the column does not exist, and every non-Docker guard is green.

⇒ ADR-0187's **headline Good consequence** — *"adding a column to any migration fails the build
until it is classified"* — is **false for the most likely future migration shape**.

**Verified the fix is free:** `grep -ric "alter table"` over all four files returns **0** everywhere.

**FIX (accepted):** the parser **FAILS CLOSED** — any statement it does not recognise is an error,
not a skip. Zero cost today, and it converts the most likely future breakage from silent to loud.
Add as a new numbered decision.

### A6 — `Render` is nondeterministic (failure-modes F6)

Both inputs are Go maps and no ordering is prescribed; Go randomises map iteration. The lens measured
**995 of 1000** renders of the same 87-entry map differing. `scripts/gen-at-rest.sh` writes and then
re-asserts **in a second process**, so as written it can **never** print its success line, and plan
Task 8's `diff`-based idempotence check can never pass.

**FIX (accepted):** all rendered output is sorted by `(table, column)`; dialect columns sorted by a
fixed dialect order. Add as a new numbered decision — determinism is a property of the artifact, not
an implementation detail.

---

## ACCEPTED — Major

- **A7 — "`dbtest` skips when unavailable" is FALSE** (execution X6 · failure-modes F13). The
  production helpers `require.NoError(t, mysqlSharedErr)`, so a missing daemon **fails** the test.
  ⚠ **The failure-modes lens's stated mechanism was imprecise** — it reported "zero `t.Skip` in
  `internal/dbtest`"; there are **2**, both in `dbname_test.go` gating an unrelated child-process
  helper. **Conclusion accepted, mechanism corrected** — the ADR-0186 shape where a finding's
  mechanism is refuted while its conclusion holds. **FIX:** correct the plan's wording, and split the
  Docker-free SQLite cross-check out of the Docker-requiring function.
- **A8 — `keyed` is stated for Postgres only** (counting C11/C12 · execution X2). Real per-dialect
  values: **29 / 28 / 28** (87-column pg incl. casbin; 79-column mysql/sqlite). Over `wrkflw_*` only:
  27 / 28 / 28. The divergence is partial-index folding. **FIX:** assert all three; never state one
  as "the" number.
- **A9 — spec and plan disagree on package and test name** (counting C17 · execution X10 ·
  interaction I13). Spec: `internal/persistence/store/atrest_test.go`, `TestAtRest_SecurityMdInSync`.
  Plan: `internal/atrest`, `TestSecurityMdInSync`. ⚠ Dangerous because `go test -run <nonexistent>`
  **exits 0** — a generator would report success having written nothing. **FIX:** spec adopts the
  plan's names. Also correct the ADR's "adds no production surface", which the new package falsifies.
- **A11 — the falsifiability recap is wrong in both halves** (counting C16). It says *"Three of the
  five are real RED; the fourth is a pin"*; the table has **four** real RED and the **fifth** is the
  pin — in the section that invokes Premise Discipline by name. **FIX:** rewrite; after A4 it becomes
  **five** real RED and no pin.
- **A12 — the spec numbers D1–D9 but its interaction pass says "ten decisions" and cites `D10`**
  (counting C2). The ADR has 10; the spec left the non-goal unnumbered. ⚠ The lens's claim that the
  *ADR* has nine is **wrong** (verified: 10 numbered decisions). **FIX:** number the non-goal as D10
  in the spec; the pair count becomes 45 + the two new decisions from A5/A6 → recompute.
- **A13 — the empty `keyed` cell is ambiguous** (interaction I8): it cannot distinguish "column
  absent in this dialect" from "present but unindexed". **FIX:** render `—` vs `n/a` distinctly.
- **A14 — a reclassification breaks TWO tasks, not one** (interaction I7). The author's own
  interaction pass said Task 3; Task 5's `byClass` map also breaks, because
  `processed_message.subscriber` is a PK column. **FIX:** correct the interaction pass.
- **A15 — goose stores no checksum, so the classification describes a FRESH database**
  (failure-modes F5/F12). An in-place migration edit never re-applies to an already-migrated
  database, so a deployed schema can differ from the declared one. The repo already documents this at
  `migration_parity_test.go:230-236`. **FIX:** state it in the generated `SECURITY.md`.
- **A16 — D7's cross-check is column-names-only, but all three parser traps are in the INDEX path**
  (interaction I6). So the cross-check does not check what D7 was justified by, and the repo's index
  introspection is names-only *by deliberate design*. **FIX:** either cross-check index membership or
  state the residual honestly — and correct D7's "reuse, no refactoring" costing.
- **A17 — Task 4's mutation exercises the WRONG guard** (execution X5 · failure-modes F2). Renaming
  `note` in one dialect produces an *absent* column, which the invariance guard cannot report; the
  plan's own Expected line names Task 3's guards. Simulated: `unclassified=1, stale=0` — so even the
  stated expectation is wrong. ⚠ **Execution lens refuted its own brief's hypothesis** that the `sed`
  would not match: it does match. **FIX:** after A4, mutate the **normalization** instead — drop
  `trigger_`→`trigger` and observe the key-set guard fire.
- **A18 — Task 7 hand-rolls SQLite setup** that `dbtest.RunTestSQLite(t)` already provides
  (execution X7), violating Golang rule #3, and omits the `modernc.org/sqlite` blank import.
  **FIX:** use the helper.
- **A19 — the spec's own "cheap fix" for the uncross-checked-table hole appears nowhere in the plan**
  (failure-modes F12). **FIX:** add the task, or withdraw the sentence.

---

## REJECTED

- **A10 — "the ADR has nine decisions"** (counting C2, first half). **REFUTED by execution:**
  `grep -nE '^\*\*[0-9]+\.'` over the ADR returns **10** numbered decisions, 1–10, with
  *"10. No mechanism ships"* present and defined. The lens generalised a real spec defect (A12) into
  a false claim about the ADR. The spec half is accepted as A12.

## Adjudication note

⚠⚠ **This revision changes SIX decisions and adds TWO** (A5 fail-closed parsing, A6 determinism).
CLAUDE.md rule #9 is explicit that *"fixing N decisions and re-auditing is NOT the same as auditing
N fixed decisions"* — each fix changes the premises the others were written against. A round-2
interaction pass over the changed set is therefore **owed**, and the pairwise grid must be re-derived
against the **new** decision list, including the survivor×new pairs.

⚠ Recorded for that round: **A1's fix (deriving `keyed` over casbin) directly interacts with A3's
fix (name normalization)** — normalization is defined over `wrkflw_journal` only, and `casbin_rule`
has no reserved-word alias, so the two must not be applied as one pass. **A4's fix depends on A3's**:
the key-set invariant is only true after normalization, so ordering them wrongly makes the new
strongest test fail for the wrong reason.
