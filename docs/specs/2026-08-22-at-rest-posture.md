# §AT-REST — the at-rest posture is stated once and machine-checked

**Status:** ⚠ **audit round 1 FOLDED** (64 findings, 17 Critical) · a round-2 interaction pass
over the changed decisions is OWED before implementation · **Date:** 2026-08-22
**Adjudication:** `docs/plans/sweep-evidence/audit-0187-adjudication.md` (round 1) and
`reaudit-0187-{interaction,counting}.md` (round 2) — read them before this file. **Round 1: 64
findings, 17 Critical. Round 2 (scoped): 34 findings, 11 Critical. Six decisions changed and THREE
were added** (D2b, D11, D12 — round 2 caught this sentence undercounting to "two", omitting D2b).
**ADR:** `docs/adr/0187-at-rest-classification-is-machine-checked.md`
**Plan:** `docs/plans/2026-08-22-at-rest-posture.md`
**Evidence:** `docs/specs/2026-08-22-adr-0187-measurements.md` (**E1–E15**, every claim below executed)
**Backlog:** 100, 101 · **Branch:** `design/at-rest-posture` · **Base:** `afef1404`

> Cut out of ADR-0186's bundle on 2026-08-21 after that delivery's first audit, where it collected
> two Criticals — **both pure scope corrections with stated fixes**. Held since in
> `docs/specs/2026-08-21-untrusted-input-deferred-slices.md` §AT-REST, which remains the record of
> what its audits established. This document supersedes that section's design; the findings it
> carries are answered below and re-derived rather than inherited.

---

## The problem

Nothing `wrkflw` stores is encrypted, redacted, or tamper-evident (**E13**), and the library has
never said so in a form a consumer can act on. That is two distinct gaps:

1. **Posture is undocumented.** A consumer deciding what to encrypt at the column level, or
   answering a personal-data question about their deployment, has nothing to read.
2. **Every previous attempt to write it down rotted.** The enumeration of what sits in the clear has
   been wrong **four times** in this lineage: 2 → "at least six" → 12 → "48 columns", and the audit
   refuted the fourth too. The third rot happened *inside a paragraph warning that it rots*, in a
   sentence that counted its own markdown rows.

⚠ **Gap 2 is the harder one, and it determines the shape of this delivery.** This is the one
decision in the repo whose **deliverable IS the enumeration**: a consumer who encrypts the columns
we name and leaves the rest in the clear has been harmed by our documentation. Prose that is
correct on the day it is written is not an acceptable artifact here.

## D10 (non-goal) — the deferral is the decision, not an omission

⚠ **Round 1 correction:** this section was previously unnumbered while the interaction pass below
referred to a `D10` and to "ten decisions". The ADR numbered it 10 all along; this document did not.

This delivery ships **no mechanism**. That is deliberate and is restated here so a later reader does
not mistake it for unfinished work:

- **No `persistence.VariableCodec`.** A column-level codec without a key-rotation and key-loss story
  is worse than none: it converts a confidentiality problem into an availability problem, and a
  consumer who loses a key loses every running process instance.
- **No hash-chained journal.** A hash chain whose head lives in the same database the attacker
  already writes to is theatre. ADR-0145 explicitly rules `engine.NodeVisit` out as the place to
  carry actor provenance, so the obvious in-band home for a chain is closed by an existing decision.

Both remain open backlog items. What ships is the **statement of posture** and the machinery that
keeps it true.

## Decisions

⚠ **Two decisions were ADDED in round 1** and are stated in full in
`docs/adr/0187-at-rest-classification-is-machine-checked.md`:

- **D11 — the parser FAILS CLOSED.** Any statement it does not recognise is an error, not a skip.
  Without it, the `ALTER TABLE … ADD COLUMN` migration that ADR-0132 says to expect contributes zero
  columns: nothing unclassified, `SECURITY.md` regenerating identically, every non-Docker guard
  green, and a new column silently absent from a **security** document. Verified free —
  `grep -ric "alter table"` over all four migration files returns **0** everywhere.
- **D12 — rendering is DETERMINISTIC**, sorted by `(table, column)` with a fixed dialect order. Both
  generator inputs are Go maps and Go randomises map iteration.
  ⚠ **Round 2 corrected this justification.** "995 of 1000 renders differed" was **one stochastic
  sample, inherited from a round-1 lens report and restated as a constant in three documents**;
  re-measured it ranges **949–995**. The stable figure is **87 distinct render orders**, so an
  undetermined generator would have gone green roughly **1 run in 87** — meaning the real hazard is a
  **~1 % flaky green**, which is strictly worse than the deterministic red the original wording
  claimed. The fix is unchanged; the reason for it is not.

### D1 — Migration sets are DISCOVERED by a stated RULE, never listed

The rule is: **every directory named `migrations`, and every direct child directory of one, that
contains at least one `.sql` file.** Because dialect cannot be inferred from a path, discovery is
paired with a **declaration** of what each directory is, checked both ways.

⚠⚠ **Round 1 correction.** This decision previously specified the glob `**/migrations/*.sql`.
Executed, it matches **one** of the four files — only the casbin set — because the three dialect
schemas sit one level deeper. It would have found casbin and **lost the three the hardcoded list
already had**. E1 missed it because its probe used `find`, a different method from the one D1
prescribed.

**Why.** The previous design walked three directories — `migrations/{postgres,mysql,sqlite}` — and
there is a **fourth**: `internal/authz/casbin/migrations/0001_casbin_rule.sql` creates a tenth table
holding the deployment's casbin policy (**E1**). The bundle this was cut from blanks the 403
response body *because policy source is sensitive*, and then omitted the table that stores that same
policy at rest.

⚠ **The fourth directory was not overlooked once — it is excluded by construction.** All three
introspection helpers the repo already has filter `table_name LIKE 'wrkflw_%'` (**E2**), and
`casbin_rule` does not match. Any future design that reuses that machinery inherits the same blind
spot. Discovery removes the class: a fifth migration directory yields unclassified columns, which
**fails the guard** (D6).

### D2 — Classification is by LOGICAL ROLE and is dialect-invariant

Each `table.column` gets exactly one class describing **what the value is**, not what type stores it.
The same class holds on Postgres, MySQL and SQLite.

**Why, with the measurement that justifies it.** The audit's finding was that "48 free-form columns"
is a Postgres number and SQLite has 67, and it required the classification to be per-dialect *or
explicitly justified as dialect-invariant*. Both numbers reproduce exactly (**E5**) — and the
mechanism is that the **entire 19-column divergence is the `TIMESTAMPTZ → TEXT` mapping**. A
timestamp written by the engine is not free-form consumer data in any dialect.

⇒ the divergence is an artifact of deriving a security classification from a **physical type**.
Classifying by role removes it at the root rather than documenting it twice. The physical type is
still emitted, per dialect, as an annotation — it is a fact about storage, not about sensitivity.

### D2b — Reserved-word column aliases are NORMALIZED before any set operation

MySQL declares `wrkflw_journal.trigger_` (`trigger` is reserved); Postgres and SQLite declare
`trigger`. The canonical name comes from `dialect.*.JournalTriggerColumn()`, exactly as
`normalizeMySQLTriggerColumn` does in `migration_parity_test.go`.

⚠⚠ **Round 1 correction, found by ALL FOUR lenses.** Without it the cross-dialect key union is
**88** while the classification covers **87**, so the completeness guard fails permanently and the
delivery could never go green. This document did not mention `trigger_` once across four files and
fifteen measurements — and the repo had already solved it **in the very file E12 cites as the
convention being reused**.

### D3 — The classification key is `table.column`, never `column`

**Why.** Exactly one column name in the schema carries two different classes (**E7**), and it is a
trap rather than a curiosity:

- `wrkflw_human_task.claimed_by` is a **human principal** — the ADR-0098 schema comment calls it
  "the application-maintained scalar projection of `claim.actor.id`". Class `actor`.
- `wrkflw_call_links.claimed_by` is a **worker lease owner** — `WithCallLinkLease(owner string, ttl)`
  sets it so `ClaimPending` can hide rows from concurrent workers. Class `reference`.

A `column`-keyed map merges a process identity with a human identity and mis-states one of them. The
guard pins the key shape so this cannot be "simplified" later.

### D4 — Six classes

| class | count | definition |
|---|---|---|
| `reference` | **27** | an identifier or label naming an engine or definition object |
| `timestamp` | **19** | an instant written by the engine |
| `scalar` | **17** | a closed-domain scalar, counter or version number |
| `freeform` | **11** | arbitrary process data — **assume it carries secrets** |
| `policy` | **8** | an authorization rule |
| `actor` | **5** | identifies a human principal |
| | **87** | 79 `wrkflw_*` (every dialect) + 8 `casbin_rule` (Postgres + `FromDB` only) |

Counts are executed and the set is exact: `comm` against the machine-dumped schema returns empty in
**both** directions — no unclassified column, no stale entry (**E6**).

⚠ **`scalar`, not `enum`.** The bucket holds `version`, `seq`, `retry_count` and `depth` — counters
and version numbers, not enumerations.

⚠ **Four of the six counts refute the author's own pre-count estimates** (`freeform` 15→11,
`reference` 22→27, `scalar` 14→17, `actor` 6→5), which had already been put in front of the owner as
part of a question. Recorded because it is the count-them-again rule catching the author, in the
document whose entire subject is enumerations rotting.

**Two classes exist because they answer questions a coarser split cannot:**

- **`actor` vs `freeform`** — "which columns hold personal data" is a different question from "which
  columns hold secrets", and a two-class `sensitive`/`metadata` split collapses them.
- **`policy`** — see D5.

### D5 — `wrkflw_human_task.eligibility` classifies as `policy`, not `freeform`

**Why.** Re-derived, not inherited (**E8**): **four** `Authorize` call sites, all in
`runtime/task/service.go` (lines 199, 234, 255, 306), pass `task.Eligibility`, which
`humantask_store.go` unmarshals from the `eligibility` column.

⇒ **authorization policy is durable in three places** (⚠ this spec said *two* until `/code-review`
refuted it on 2026-08-23 — see ADR-0187 decision 5's folded correction):
`wrkflw_human_task.eligibility` (every dialect, always); `casbin_rule.{ptype,v0..v5}` (Postgres, and
only where `casbinauthz.MigrateCasbin` has been run — `FromEnforcer` and `FromStrings` put nothing in
a table, **E3**); and **`wrkflw_definitions.definition`**, class `freeform`, which holds the
marshalled `ProcessDefinition` including every node's `eligible_roles` / `eligible_privileges` /
`eligible_expr` (`definition/model/node_wire.go` × `internal/persistence/store/definitions.go`
`PutDefinition`). A consumer hardening "the policy store" who protects only `casbin_rule` has
protected a third of it. Class alone cannot carry that sentence — the third location is
deliberately `freeform` — so the locations are a derived, machine-checked enumeration
(`atrest.PolicyAtRestLocations`) that `Render` counts and validates in both directions.

### D6 — The guard fails on drift, on omission, AND on staleness

The invariant lives in `internal/atrest` (`package atrest_test`) and fails when:

1. a schema column has **no** classification entry — so a new column must be classified;
2. a classification entry names a column **not in** the schema — self-cleaning, per the
   `knownOpenInternalLeaks` convention (**E12**), so a dropped column cannot leave a stale row that
   the next one hides under;
3. `SECURITY.md`'s generated block differs from what the generator would emit;
4a. **identity, scoped to `wrkflw_*`** — the normalized key set of the **79** `wrkflw_*` columns is
   not identical across Postgres, MySQL and SQLite; **or**
4b. **coverage, scoped to the UNION** — the classification does not cover the union of every
   dialect's keys (**87**) exactly.

   ⚠⚠ **Round 2: these were ONE clause and it had no true reading.** `casbin_rule` is Postgres-only,
   so unscoped the sets are 87/79/79 and identity fails forever; scoped to `wrkflw_*` it is 79
   against an 87-key classification and coverage fails. **The two scopes differ on purpose.**
5. the parsed schema disagrees with **live introspection** — Docker-gated (D7).

⚠ 1 + D1 together are what make the fourth-directory failure structurally unrepeatable: discovery
finds a new migration set, and its columns are unclassified, and that **fails**. ⚠ That chain holds
only with **D11** (fail-closed parsing) in place — without it a set the parser cannot read
contributes no columns, so nothing is unclassified and nothing fails.

⚠⚠ **Round 1 correction to clause 4.** It previously asserted that no column's *class* differs
across dialects. That guard was **vacuous by construction**: the classification is a flat
`map[ColumnKey]Class` with no dialect term, so divergence is unrepresentable — fuzzed over 200,000
randomised inputs, **zero** non-empty results. The key-set invariant replacing it is strictly
stronger because it **fails today** if D2b is dropped.

### D7 — The parser is cross-checked against live introspection

The everyday guard parses SQL text, so it runs with no Docker and no database. A separate
Docker-gated test asserts the parse agrees with `introspectPostgres` / `introspectMySQL` /
`introspectSQLite`, which already exist in the same package (**E12**).

**Why a cross-check is not optional.** Writing this design's own throwaway probes surfaced **two
parser traps** (**E11**): multi-line `CREATE INDEX` statements with a multi-space `ON` (a first probe
emitted four rows whose table name was the literal string `CREATE`), and MySQL's inline
`INDEX name (col)` clauses inside the `CREATE TABLE` body, which must count as indexes but not as
columns. A hand-rolled DDL reader that nothing checks is the same class of defect as the count that
rotted four times.

### D8 — A machine-derived `keyed` annotation, explicitly a LOWER BOUND, emitted PER DIALECT

`keyed` records that the database itself keys on a column: primary-key membership, `UNIQUE`, and
`CREATE INDEX` column lists **including partial-index `WHERE` predicate columns**.

**Why it earns its place.** It turns an inert list into an actionable one, and it cannot rot because
nobody writes it. Measured over **every table and every dialect**: **Postgres 29 of 87, MySQL 28 of
79, SQLite 28 of 79** (over `wrkflw_*` alone: 27 / 28 / 28 — the divergence is partial-index
folding). The one keyed `actor` column is `wrkflw_human_task.claimed_by`, whose index the ADR-0098
schema comment says "keeps `AssignedTo`'s lookup indexed" — so the single PII column a consumer most
wants to encrypt is exactly the one where a non-deterministic scheme silently costs them a lookup.

⚠⚠ **Round 1 correction — this section previously published a FALSE safety claim** and called it
"the actionable content this document exists to produce". It read: *"zero `freeform` and zero
`policy` columns carry an index, so all 11 free-form columns and both policy locations can be
encrypted without breaking a single index."* **`casbin_rule.ptype` is class `policy` and carries
`casbin_rule_ptype_idx`.** The claim was derived over 79 columns and asserted over 87. **Withdrawn.**

⚠⚠ **A second mechanism makes the withdrawal necessary regardless of any index.**
`internal/authz/casbin/pg_adapter.go` `RemovePolicy` runs
`DELETE FROM casbin_rule WHERE ptype=$1 AND v0=$2 AND … AND v5=$7` — **all seven** policy columns are
equality predicates, so encrypting any of them non-deterministically breaks `RemovePolicy` and
`RemoveFilteredPolicy`.

⇒ **no blanket "safe to encrypt" sentence is emitted.** The generated document states `keyed` per
column and states, separately, that **query-level equality predicates exist outside the derivation**,
naming `casbin_rule.{ptype,v0..v5}`.

⚠ **This document's own caveat predicted the defect and its headline ignored it** — `keyed` was
labelled a lower bound blind to query-level filtering, and a claim only a complete bound could
support was published anyway.

⚠ **`keyed` is dialect-DEPENDENT, unlike `class`** (**E10**): Postgres declares 11 indexes of which
5 are partial; MySQL 8 plus 3 inline, none partial; SQLite 11, none partial. MySQL and SQLite fold
partial predicates into the key instead. It is therefore emitted per dialect.
⚠⚠ **This corrects the author's own proposal**, which had `keyed` riding along with the invariant
`class`. It was wrong, and the measurement is what refuted it.

⚠ **It is a LOWER BOUND and the generated text must say so.** It is derived from DDL only; columns
filtered or ordered by query text in the store layer are invisible to it. Claiming it enumerates
"the columns you cannot encrypt" would be exactly the over-reaching quantifier Premise Discipline
warns about.

### D9 — The generator is a golden-file `-update` flag, not a `main` package

`scripts/gen-at-rest.sh` runs
`go test ./internal/atrest/ -run TestSecurityMdInSync -update`, which rewrites a
`<!-- BEGIN at-rest (generated) --> … <!-- END at-rest -->` block in `SECURITY.md`.

**Why.** It answers the audit's F13 — *"`SECURITY.md` cannot disagree with the classification, but no
generator exists"* — with a mechanism rather than a claim. A golden-file update flag needs no `main`
package, so CLAUDE.md's no-`cmd/` rule is not engaged at all, and `scripts/` already holds four
shell entry points while the repo has no `go:generate` convention beyond mockgen (**E12**).

## The classification

One class per `table.column`. Dialect-invariant (D2 — classification by logical role rather
than physical type). Keyed per dialect and physical type per dialect are **generated**, not
transcribed here — this document states the judgement and delegates every derivable fact to the
generator, which is the whole point of D6 (the guard that fails on drift, omission and staleness).

**`wrkflw_instances`** (9) — reference: `instance_id`, `def_id` · scalar: `def_version`, `status`, `version` · freeform: `snapshot` · timestamp: `started_at`, `ended_at`, `updated_at`

**`wrkflw_journal`** (6) — reference: `instance_id` · scalar: `seq`, `kind` · freeform: `trigger` · timestamp: `occurred_at`, `applied_at`

**`wrkflw_outbox`** (12) — scalar: `id`, `status`, `retry_count` · reference: `instance_id`, `topic`, `dedup_key`, `definition_ref` · freeform: `payload`, `last_error` · timestamp: `created_at`, `published_at`, `next_attempt_at`

**`wrkflw_definitions`** (4) — reference: `def_id` · scalar: `version` · freeform: `definition` · timestamp: `created_at`

**`wrkflw_processed_message`** (3) — reference: `subscriber`, `message_id` · timestamp: `processed_at`

**`wrkflw_call_links`** (13) — reference: `child_instance_id`, `parent_instance_id`, `parent_command_id`, `parent_def_id`, `claimed_by` · scalar: `parent_def_version`, `depth`, `status` · freeform: `output`, `error` · timestamp: `created_at`, `notified_at`, `claimed_at`

**`wrkflw_timers`** (8) — reference: `instance_id`, `timer_id`, `def_id` · timestamp: `next_run` · scalar: `kind`, `def_version`, `trigger_kind` · freeform: `trigger_payload`

**`wrkflw_chain_links`** (7) — reference: `predecessor_instance_id`, `outcome`, `successor_instance_id`, `predecessor_definition_ref`, `successor_definition_ref` · freeform: `start_vars` · timestamp: `created_at`

**`wrkflw_human_task`** (17) — reference: `task_id`, `instance_id`, `node_id`, `outcome` · scalar: `state` · actor: `claimed_by`, `claim_actor`, `completed_by`, `completion_actor`, `candidates` · timestamp: `claimed_at`, `completed_at`, `created_at`, `due_at` · freeform: `note`, `vars` · policy: `eligibility`

**`casbin_rule`** (8) — scalar: `id` · policy: `ptype`, `v0`, `v1`, `v2`, `v3`, `v4`, `v5`

---

## What `SECURITY.md` gains

A generated section, `## Data at rest`, carrying:

- the six class definitions and what a consumer should assume about each;
- the 87-row table — `table`, `column`, `class`, physical type per dialect, `keyed` per dialect;
- ⚠ **NO blanket "safe to encrypt" sentence.** The withdrawn claim (round 1) must not reappear here:
  `casbin_rule.ptype` is class `policy` AND indexed, and `RemovePolicy` filters all seven policy
  columns by equality. What is emitted instead is (a) `keyed` **per column, per dialect**, derived;
  (b) the one derived warning that survives — **`wrkflw_human_task.claimed_by` is indexed, so
  encrypting it non-deterministically costs the `AssignedTo` lookup**; and (c) the hazard statement
  below;
- the explicit lower-bound caveat on `keyed` (D8), stated as a HAZARD rather than a footnote:
  **query-level equality predicates exist outside the derivation**, with
  `casbin_rule.{ptype,v0..v5}` named as the known instance;
- ⚠ the **goose-no-checksum** caveat (A15): the table describes a **fresh** database, and a
  long-lived deployment migrated before an in-place edit can hold a schema it does not describe;
- ⚠ **`—` (present, unindexed) and `n/a` (absent in this dialect) must render DISTINCTLY** (A13);
  D8's widening to all tables created 16 cells where the difference matters;
- an explicit statement that **`casbin_rule` exists only in a Postgres deployment that has run
  `casbinauthz.MigrateCasbin`** (⚠ corrected from "only under `FromDB`", which is a false
  if-and-only-if: the call is never auto-run, and a deployment that made it keeps the table under any
  policy source), and that authorization policy is at rest in **three** places (D5, as corrected —
  the third is `wrkflw_definitions.definition`), with the count DERIVED from
  `atrest.PolicyAtRestLocations` rather than typed;
- an explicit statement that **consumer-supplied migrations are outside this table's scope** — the
  invariant covers only the migrations embedded in this module;
- a pointer to the non-goals above, so a reader does not infer that a codec exists.

## Falsifiability — what makes each prescribed assertion fail TODAY

Per Premise Discipline, a prescribed test must state what makes it fail.

⚠⚠ **Round 1 correction.** This paragraph previously read *"Three of the five are real RED; the
fourth is a pin"* — **wrong in both halves**: four were real RED and the **fifth** was the pin. It
was wrong in the section that invokes Premise Discipline by name.

⚠⚠ **Round 2: the sentence that REPLACED it was wrong too** — it said *"there is no pin left: all
five are real RED"*, refuted by its own table two rows below, where the staleness guard needs a
deliberately planted bogus entry to go red. **Four are real RED; the fifth needs a planted row.**
Third consecutive wrong quantifier in this paragraph, the second introduced by a fix to the first.

| assertion | what makes it fail today |
|---|---|
| every schema column has a classification | the classification map starts **empty** — real RED |
| no entry names an absent column | real RED once the map is deliberately seeded with one bogus row during the RED step |
| `SECURITY.md` matches the generator | **no such block exists** in `SECURITY.md` — real RED |
| the parser agrees with live introspection | the parser does not exist — **compile-error RED** |
| the normalized key set agrees across dialects | **fails today** without D2b — MySQL's `trigger_` makes the union 88 against a 87-key classification |

⚠⚠ **Round 1 replaced this entirely.** It previously read: *"The invariance pin (D2) is a PIN, not
a RED-green fix, because the property already holds"* — and the guard it defended could not fail at
all. The key-set invariant that replaces it is a genuine RED, so the liveness-guard-plus-mutation
ceremony below is no longer load-bearing for *this* assertion. It is **kept anyway**, because a
guard that is RED today becomes a pin the moment it is fixed, and this is the repo's established way
of keeping such a guard honest (`engine/terminal_sites_test.go`, **E12**):

1. a **liveness guard** runs the same matcher over an in-test fixture carrying one rogue column
   (classified differently in two dialects) and one innocent one, asserting 1 and 0 — so a detector
   that can never fire cannot pass;
2. a **real mutation** against a migration file on disk (`cp` backup, restore, `diff` byte-exact),
   with the observed failure text recorded in the plan.

⚠ **Check the FIXTURE, not the assertion line.** This repo has shipped seven tests that could not
fail. A test asserting "no class diverges" over a fixture with one dialect is worthless.

## Assumptions (unverified)

- `ASSUMPTION (unverified)`: no consumer currently relies on the absence of a `## Data at rest`
  section in `SECURITY.md`. The section is additive and the file is documentation, so the risk is
  taken rather than measured.

⚠ **A second assumption was drafted here and then executed rather than shipped.** It read: *"goose's
`Up`/`Down` markers are the only directive form a naive statement splitter must respect."* That is
now **E14**, a measurement: four files, four `Up`, four `Down`, and **no `StatementBegin` /
`StatementEnd` anywhere**. Recorded because the honest move on a cheap claim is to run it, not to
label it.

## Open questions carried into the audit

1. **Do `wrkflw_processed_message.subscriber` and `wrkflw_outbox.topic` deserve `reference` or a
   consumer-controlled sub-class?** Both are consumer-authored strings, unlike engine-generated ids
   that share the class. The bundle takes `reference` and states the judgement; an auditor should
   attack it.
2. **Is `candidates` `actor` or `policy`?** It lists principals who may act, which is arguably a rule
   rather than an identity. The bundle takes `actor` on the grounds that it enumerates principals
   rather than expressing a predicate; `eligibility`, which does express predicates, is `policy`.
3. ~~**Should the `keyed` derivation include foreign-key columns?**~~ **RESOLVED by measurement
   (E15), and it was resolved in the opposite direction to the way it was framed.** There is exactly
   **one** foreign key in the whole schema — `wrkflw_journal.instance_id` — and that column is
   **already `keyed` via primary-key membership**, because `wrkflw_journal`'s PK is
   `(instance_id, seq)` in all three dialects. Including or excluding FK columns changes **no** row
   of today's output. The plan therefore prescribes the exclusion as a **stated rule carrying a
   comment**, not as an accident of the parser, so that a future FK on a non-PK column is a decision
   someone makes rather than a silence someone inherits.

   ⚠ E15 also surfaced **parser trap 3**: MySQL declares that FK as a table-level
   `CONSTRAINT … FOREIGN KEY (…)` clause **inside** the `CREATE TABLE` body, where Postgres and
   SQLite declare it inline on the column. A parser that read that line as a column would report 80
   columns for MySQL and break dialect parity.

---

## Author's interaction pass (written BEFORE the audit, per rule #9's corollary)

> ⚠⚠ **ROUND 1 VERDICT ON THIS SECTION: the three pairs it self-reported as UNRESOLVED were the
> three LEAST severe interactions in the bundle.** Each of the audit's structural Criticals — the
> false safety claim (D1×D8), the vacuous invariance guard (D2×D3), the `trigger_` key-set break
> (D2×D3), and the fail-open parser (D1×D6) — is a pair this section **did not derive at all**.
> ⚠ Worse, **the false safety claim is CAUSED BY D1**, the decision the bundle is built around:
> pulling `casbin_rule` into scope is exactly what falsifies D8's headline. The section reasoned
> about D1's benefits and never asked what D1 hands D8.
> ⚠ It also claimed "ten decisions … 45 pairs" while writing 8, and while this document defined only
> nine. Do not read the section below as coverage; read it as a sample that missed its own subject.
> ⚠⚠ **Round 2: the recount the round-1 adjudication ORDERED was never done.** There are now
> **13 decisions** (D1, D2, D2b, D3–D12) and therefore **78 pairs**. The section below still says
> "ten decisions ship together", still describes D1 as "discover by glob" — the wording round 1
> replaced — and contains **no pair involving D2b, D11 or D12**, the three added decisions, whose
> interactions are therefore the ones nobody has derived. **Round 3, if run, starts here.**

Ten decisions ship together. Below is what each does to the others' premises, derived pairwise. The
three marked **UNRESOLVED** are handed to the audit's interaction lens as its starting grid, not as
solved problems.

⚠ **Stale below this line — retained as the record of what the author derived pre-audit, NOT as
current design.** D1 is no longer "discover by glob".

**D1 (discover by glob) × D6 (fail on an unclassified column).** These two are the mechanism, not
two features: discovery finds a new migration set, its columns have no class, the guard fails.
Neither alone closes F5 — a glob whose output nothing checks is decoration, and a completeness check
over a hardcoded list is F5 again. Resolved.

**D1 × D2 (classification is dialect-invariant).** ⚠ Discovery cannot tell you a *dialect*:
`internal/authz/casbin/migrations` is named for neither its dialect nor its engine. "Dialect-
invariant" is therefore meaningless for a directory whose dialect is unknown. Resolved by the plan's
`MigrationSets` **declaration**, checked both ways — but note the shape this forces: the delivery is
*discovery plus declaration*, not discovery alone, and a reviewer who reads only ADR-0187 decision 1
will not expect the declaration to exist.

**D2 × D8 (`keyed` is per dialect).** The two axes disagree deliberately: `class` is invariant,
`keyed` is not (E10). ⚠ **UNRESOLVED (presentation):** the generated table will carry per-dialect
columns for physical type and `keyed` beside a single `class` column. A reader scanning it may
reasonably infer that everything is per-dialect, or that `class` is a Postgres fact like "48". The
render must state the asymmetry in the table's own preamble; whether that is sufficient is a
judgement the audit should attack.

**D4 (the six counts) × open questions 1 and 2.** ⚠⚠ **UNRESOLVED, and it constrains the audit's own
output.** ⚠ **Round 1 correction: it breaks TWO tasks, not one** — task 5's `byClass` map too,
because `wrkflw_processed_message.subscriber` is a primary-key column and therefore keyed. The plan hardcodes `reference` 27, `timestamp` 19, `scalar` 17, `freeform` 11, `policy` 8,
`actor` 5 as test assertions. Open question 2 asks whether `candidates` is `actor` or `policy`; open
question 1 asks the same of `subscriber`/`topic`. **Any reclassification the audit accepts changes
two of those six numbers and breaks a prescribed test.** This is stated so the adjudication step
knows it must re-derive the counts *and* update task 3, rather than accepting a class change and
discovering the breakage during implementation.

**D5 (`eligibility` is `policy`) × D8.** ⚠⚠ **Round 1: this "Resolved" was WRONG, and the way it was
wrong is instructive.** The check performed was that `eligibility` is unkeyed whether classed
`policy` or `freeform` — true, and irrelevant. The pair that mattered ran the other way: D5 puts
`casbin_rule.{ptype,v0..v5}` and `eligibility` in **one class**, and D8 then made a safety claim over
**that whole class** — which `casbin_rule_ptype_idx` and `RemovePolicy`'s seven-column `DELETE`
predicate falsify. **Only the safe direction was checked, and the pair was marked resolved on it.**

**D6 × D9 (the golden-file `-update` flag).** ⚠ **UNRESOLVED (and it is a real limitation, not a
presentation one):** `-update` rewrites `SECURITY.md` from the classification *without judging it*.
The guard therefore protects against **drift**, never against **wrong** — a misclassified column is
propagated into the published document by the very mechanism meant to keep it honest, and the run is
green. Nothing in this bundle detects a wrong-but-consistent classification. The mitigation shipped
is weak on purpose (the classification is a reviewed Go literal in a feature commit); a stronger one
would need a second, independent derivation, which does not exist.

**D7 (cross-check against live introspection) × D1 (discovery).** ⚠ The cross-check reuses helpers
that filter `LIKE 'wrkflw_%'` (E2), so `casbin_rule` needs its own cross-check — which the plan's
task 7 adds explicitly. **But a FIFTH migration set creating non-`wrkflw_` tables would be
discovered, parsed and classified while being silently absent from the cross-check.** The guard that
fails on an unclassified column has no counterpart that fails on an uncross-checked table. Stated
as a known hole; a cheap fix is a test asserting every parsed table appears in some cross-check.

**D8 × D10 (no mechanism ships).** Telling a consumer which columns are safe to encrypt while
shipping no codec is coherent — the encryption they would apply is at their own layer or the
database's, not ours. No conflict.

**D3 (`table.column` key) × everything.** The key shape is a third dimension away from `keyed`,
which is `(dialect, table, column)`. The plan resolves this by holding `Column` values inside a
per-dialect `Schema` while the classification is a flat `map[ColumnKey]Class`. Consistent, but it
means two different lookup shapes coexist, and a later "simplification" that flattens them would
reintroduce the `claimed_by` merge D3 exists to prevent.
