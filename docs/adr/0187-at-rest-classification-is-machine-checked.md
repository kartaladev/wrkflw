# 187. The at-rest data classification is generated and machine-checked, never written by hand

> ## ▶ AUDIT ROUNDS 1 AND 2 FOLDED 2026-08-22 — **64 findings / 17 Critical**, then **34 / 11**
>
> ⭐⭐⭐ **Round 2's finding above the findings: the round-1 revision corrected each decision WHERE IT
> IS DEFINED and left every place it is CONSUMED.** The round-1 headline Critical — the false safety
> claim withdrawn below — was still live in **four** places afterwards, including as a **code
> instruction to the generator**, where it would have shipped hard-coded into `render.go`. Had round 2
> not run, the round-1 fix would have been cosmetic. ⭐ Remedy adopted as standing practice: **after
> writing a corrected value, `grep` for the OLD one.**
>
> ### Round 1 — four Opus lenses
>
> Adjudication: `docs/plans/sweep-evidence/audit-0187-adjudication.md`. Lens reports:
> `audit-0187-{execution,failure-modes,counting,interaction}.md` (3,612 lines).
>
> **Six decisions changed and THREE were added** (D2b, D11, D12). ⚠ Round 2 caught this sentence
> undercounting: it said "two", omitting **D2b** — the very decision the adjudication calls the hub
> that D6's new invariant depends on, and the scope statement for the mandatory round-2 interaction
> pass. The four findings that matter, each verified by the controller independently:
> 1. ⛔ **The headline safety sentence was FALSE.** `casbin_rule.ptype` is class `policy` and carries
>    `casbin_rule_ptype_idx`; `RemovePolicy` additionally filters all seven policy columns by
>    equality. *"Every `freeform` and `policy` column is index-free and can be encrypted without
>    breaking an index"* is **withdrawn**. Real numbers: Postgres **29 of 87** keyed, **policy-keyed
>    = 1**. ⚠ The claim was measured over 79 columns and asserted over 87 — *a boundary derived
>    correctly at one level, then asserted one level up without re-derivation*, which is the ADR-0186
>    lesson quoted in `HANDOVER.md` and reproduced here.
> 2. ⛔ **The discovery glob `**/migrations/*.sql` matched 1 of 4 files** — and lost the three the old
>    hardcoded list had. Decision 1 now states the RULE, not a glob.
> 3. ⛔ **MySQL names the journal column `trigger_`** (reserved word), so the cross-dialect key union
>    is 88 while the classification covers 87 — the guard could never go green. **All four lenses
>    found it; the bundle never mentioned it once.** The repo already solved it in
>    `normalizeMySQLTriggerColumn`, in the very file this ADR cites as the convention being reused.
> 4. ⛔ **The dialect-invariance guard was vacuous by construction** — a flat `map[ColumnKey]Class`
>    makes divergence unrepresentable (fuzzed over 200,000 inputs: zero non-empty results). Replaced
>    by a key-set agreement invariant that **fails today**, which is strictly stronger.
>
> ⚠ **The finding rate matches the ADR-0186 trend** (seven rounds at 57–65 regardless of scope).
> Judge round 2 against that trend, not against a hope of convergence.

- Status: **ACCEPTED for implementation** — rule-#9 audit **rounds 1 and 2 both folded**
  (64 findings / 17 Critical, then 34 / 11). Round 2 was scoped to interaction + counting by owner
  decision. ⚠ **Three residuals are carried, not closed** — see the end of
  `docs/plans/sweep-evidence/reaudit-0187-adjudication.md`: D2b/D11/D12 have no derived pairwise
  interactions; ~~decision 7's cross-check is column-names-only~~ (**closed** by `/code-review` —
  it now compares the whole key set); and decision 9's `-update` flag propagates a
  wrong-but-consistent classification.
- Date: 2026-08-22
- Relates to: **ADR-0186** (this decision was D3/D6 of that bundle and was cut out on 2026-08-21
  after its first audit), **ADR-0132** (the consolidated single-migration schema this classifies),
  **ADR-0145** (rules `engine.NodeVisit` out as the carrier of actor provenance, which closes the
  obvious in-band home for a journal hash chain), **ADR-0098** (the human-task schema, whose own
  comment supplies the `claimed_by` index rationale), **ADR-0081/0082** (the neutral store and its
  three dialects), **ADR-0164** (the machine-checked-invariant precedent this reuses).
- Backlog: **100**, **101**. ⚠ 104, 54, 65, 99 belong to the four other deferred deliveries in
  `docs/specs/2026-08-21-untrusted-input-deferred-slices.md`.
- Spec: `docs/specs/2026-08-22-at-rest-posture.md` ·
  Evidence: `docs/specs/2026-08-22-adr-0187-measurements.md` (E1–E15)

## Context

`wrkflw` stores process snapshots, event payloads, process definitions, human-task variables and —
in a Postgres deployment using the `FromDB` policy source — the deployment's casbin policy. **None
of it is encrypted, redacted, or tamper-evident**, and the library has never told a consumer so in a
form they can act on (E13). `wrkflw_journal` carries no hash, prev-hash or signature column (E4).

Two distinct problems follow, and only the second shapes this decision.

**The posture is undocumented.** A consumer applying column-level encryption, or answering a
personal-data question about their own deployment, has nothing to read.

**Every previous attempt to write it down was wrong.** The enumeration of what sits in the clear has
rotted **four times** in this lineage: 2 → "at least six" → 12 → "48 columns", and ADR-0186's first
audit refuted the fourth as well. The third rot occurred *inside a paragraph warning that the
enumeration rots*, in a sentence that counted its own markdown rows.

⚠ **This is the one decision in the repo whose deliverable IS the enumeration.** A consumer who
encrypts the columns we name and leaves the rest in the clear has been harmed by our documentation.
Prose that happens to be correct on the day it is written is therefore not an acceptable artifact,
and "be more careful next time" is not a mechanism — it has now failed four times.

Two findings from ADR-0186's audit define the corrections this decision owes.

**The enumeration walked three migration directories; there are four.**
`internal/authz/casbin/migrations/0001_casbin_rule.sql` creates a tenth table with seven free-form
`TEXT` columns holding the deployment's casbin policy, applied by the module-root public
`casbinauthz.MigrateCasbin` (E1, E3). The bundle this was cut from blanks the 403 response body
*because policy source is sensitive*, and then omitted the table that stores that policy at rest.
⚠ The omission is **structural, not careless**: all three schema-introspection helpers the repo
already owns filter `table_name LIKE 'wrkflw_%'`, and `casbin_rule` does not match (E2). Any design
reusing that machinery inherits the same blind spot.

**"48 free-form columns" is a Postgres number; SQLite has 67.** Both reproduce exactly (E5) — the
single-dialect blind spot appearing inside the very number meant to fix it.

## Decision

**The at-rest classification is a stated judgement about logical role, and everything derivable from
it is generated and machine-checked. `SECURITY.md` is an output, not a document anyone edits.**

**1. Migration sets are DISCOVERED by a stated RULE, never by a hardcoded list.** The rule is:
*every directory named `migrations`, and every direct child directory of one, that contains at least
one `.sql` file.* Because dialect cannot be inferred from a path — the casbin set is named
`migrations`, not `postgres` — discovery is paired with a **declaration** of what each discovered
directory is, checked **both ways**: an undeclared directory fails, and a declaration matching no
directory fails.

⚠⚠ **Round 1 correction.** This decision previously specified the glob `**/migrations/*.sql`.
Executed, that matches **one** of the four files — only `internal/authz/casbin/migrations/` — because
the three dialect schemas sit one level deeper at `migrations/<dialect>/*.sql`. Implemented
literally it would have found casbin and **lost the three the hardcoded list already had**: the
mirror image of the omission this decision exists to close. E1 never caught it because its probe was
`find . -name '*.sql'`, a different method from the one the decision prescribed. (Go's
`filepath.Glob` has no `**` at all.)

**2. Classification is by LOGICAL ROLE and is dialect-invariant; the physical type is a separate,
generated annotation.** Justification, measured: the Postgres type histogram is 36 `TEXT` + 12
`JSONB` + **19 `TIMESTAMPTZ`** + 6 `INT` + 3 `SMALLINT` + 2 `BIGINT` + 1 `BIGSERIAL` = 79, and
SQLite maps `TIMESTAMPTZ → TEXT`, so 48 + 19 = 67 (E5). **The entire divergence is the timestamp
mapping**, so the 48-vs-67 gap is an artifact of deriving a *security* classification from a
*physical type*.

**2b. Reserved-word column aliases are NORMALIZED to the canonical name before any set operation.**
MySQL declares `wrkflw_journal.trigger_` because `trigger` is reserved; Postgres and SQLite declare
`trigger`. The canonical name is taken from `dialect.*.JournalTriggerColumn()`, exactly as
`normalizeMySQLTriggerColumn` does in `migration_parity_test.go`.

⚠⚠ **The normalization is an EXACT `(table, column)` key match — never a prefix, suffix or substring
rule.** `wrkflw_timers` declares `trigger_kind` and `trigger_payload`; any `strings.HasPrefix(col,
"trigger_")`-shaped rule silently rewrites both, and no otherwise-prescribed test would catch it
because the counts stay 79. It is normalization of **one known alias**, not of a pattern.

⚠ **It must be applied to BOTH sides of the Docker-gated cross-check (decision 7), not just to the
parse.** Live MySQL introspection genuinely returns `trigger_`; comparing a normalized parse against
raw introspection makes the one test that proves the parser honest unable ever to pass. The repo's
precedent normalizes the **live** side (`migration_parity_test.go:71`).

⚠⚠ **Round 1 correction, found by all four lenses.** Without this, the cross-dialect key union is
**88** while the classification covers **87**, so the completeness guard fails permanently and the
delivery could never reach green. The bundle did not mention `trigger_` once across four documents
and fifteen measurements — the **fifth** time this lineage has claimed a gap the repo had already
filled, and this time in the very file cited as the convention being reused.

**3. The classification key is `table.column`.** Exactly one column name carries two classes (E7):
`wrkflw_human_task.claimed_by` is a human principal (`actor`), while `wrkflw_call_links.claimed_by`
is a worker lease owner set by `WithCallLinkLease(owner string, ttl)` (`reference`).

**4. Six classes** — `reference` 27, `timestamp` 19, `scalar` 17, `freeform` 11, `policy` 8,
`actor` 5, totalling **87** = 79 `wrkflw_*` columns (present, after 2b, under identical names in
every dialect) + 8 `casbin_rule` columns (Postgres and `FromDB` only).

**5. `wrkflw_human_task.eligibility` is `policy`, not `freeform`** — **four** `Authorize` sites, all
in `runtime/task/service.go` (199, 234, 255, 306), pass `task.Eligibility`, hydrated from that
column (E8).

⚠⚠ **`/code-review` correction (rule #11), folded 2026-08-23: authorization policy is durable at rest
in THREE places, not two — and this ADR, the spec, the plan, the measurements and the generated
`SECURITY.md` all said two.** The third is **`wrkflw_definitions.definition`**, class `freeform`:
`definition/model/node_wire.go` declares `EligibleRoles` / `EligiblePrivileges` / `EligibleExpr` on
`NodeWire`, and `internal/persistence/store/definitions.go`'s `PutDefinition` `json.Marshal`s the
whole `ProcessDefinition` into that column. Executed — marshalling a definition whose user task
carries all three options emits
`"eligible_roles":["manager"],"eligible_privileges":["approve:invoice"],"eligible_expr":"vars.amount < 100"`.
A consumer who encrypts the two `policy`-classed locations therefore leaves per-node eligibility
rules in the clear: the precise harm this ADR's Context names.

**The column keeps class `freeform`; the enumeration moves instead.** One column carries one class,
and this column's logical role is "the serialized process definition" — arbitrary consumer-authored
content, possibly PII, which is what `freeform`'s published description warns about and what a
reader most needs. Reclassifying it `policy` would trade that warning away and shift the D4 counts
(`freeform` 11→10, `policy` 8→9) to say something less true, not more. Instead the locations are
now a **stated, derived, machine-checked enumeration** — `atrest.PolicyAtRestLocations` — from which
the generated sentence takes its count (`englishNumber(len(applicable))`); `Render` fails closed
when a declared location stops resolving, and when any `policy`-classed column is covered by no
location. `TestDefinitionEligibilityFieldsAreTheDeclaredSet` additionally pins the three `Eligible*`
`NodeWire` fields, so a fourth one fails the build rather than silently widening what that column
holds. The count was retyped in **nine** places across this bundle; the retyping is what is fixed,
not only the number.

**6. The guard fails on omission, staleness, drift, KEY-SET DISAGREEMENT, and parser dishonesty.**
An unclassified column fails; a classification entry naming an absent column fails (self-cleaning);
a `SECURITY.md` block differing from the generator fails; and a Docker-gated test fails if the parse
disagrees with live introspection. The key-set guarantee is **two separately-scoped clauses**:

- **6a (identity, scoped to `wrkflw_*`)** — the normalized `table.column` key set of the **79**
  `wrkflw_*` columns is identical across Postgres, MySQL and SQLite.
- **6b (coverage, scoped to the UNION)** — the classification covers the union of every dialect's
  keys — **87** — exactly: no unclassified key, no stale entry.

⚠⚠ **Round 2 correction: these were one sentence and it had NO TRUE READING.** It asserted the key
set is "identical across all three dialects **and** the classification covers it exactly". Because
`casbin_rule` is Postgres-only (`id BIGSERIAL`), unscoped the sets are **87 / 79 / 79** and the
identity conjunct fails forever; scoped to `wrkflw_*` it is **79** against an **87**-key
classification and the coverage conjunct fails. Only the plan split it, silently, across two tests.
The two scopes are different on purpose and must never be recombined.

⚠⚠ **Round 1 correction (retained).** Clause 6a replaced a guard asserting that no column's *class*
differs across dialects. That guard was **vacuous by construction**: the classification is a flat
`map[ColumnKey]Class` with no dialect term, so divergence is unrepresentable — fuzzed over 200,000
randomised inputs, **zero** non-empty results. 6a is strictly stronger because it **fails today** if
2b is dropped.

**7. The parser is cross-checked against live introspection, because writing this design's own
probes broke it three times, and implementing it found a FOURTH** — multi-line `CREATE INDEX` with a
multi-space `ON`; MySQL's inline `INDEX name (col)` clauses; MySQL's table-level
`CONSTRAINT … FOREIGN KEY (…)` (E11, E15); and **inline single-column `PRIMARY KEY`** (E16).

⚠⚠ **Rule #11 correction, from implementation not from either audit.** The bundle enumerated **three**
parser traps. The fourth — `id BIGSERIAL PRIMARY KEY` declared inline on the column rather than as a
table-level `PRIMARY KEY (…)` clause — and a parser that misses it silently under-reports `keyed`,
which is **exactly the count decision 8 publishes**. ⚠ An enumeration rotted again — in the delivery
whose entire subject is enumerations rotting, and after two audit rounds one of which was dedicated
to re-counting. It was found by probing the real files, not by reading.

⚠⚠⚠ **And it rotted a THIRD time. This paragraph said the inline form occurs FOUR times; the shipped
comment in `schema.go` said THREE; the migrations declare FIVE.** `/code-review` found it. The set is
**closed and pinned, not counted in prose** — `TestInlinePrimaryKeySitesAreTheDeclaredSet` re-derives
it from the four migration files by an independent method and asserts the whole set, so a sixth site
names itself instead of arriving as an off-by-one:
`casbin_rule.id`, `wrkflw_instances.instance_id`, `wrkflw_outbox.id` and
`wrkflw_call_links.child_instance_id` in every dialect that declares them, plus
**`wrkflw_human_task.task_id` — inline in SQLite ONLY**, where Postgres and MySQL spell the same key
as a table-level `PRIMARY KEY (task_id)`. That per-dialect asymmetry is why both earlier counts were
low: each was derived from one dialect's file and asserted over the corpus.
⚠ ~~**Round 1 residual, stated not fixed:** all three traps are in the **index** path, while the
cross-check compares **column names only**… Either extend it to index membership or read decision 7
as narrower than it sounds.~~ ✅ **CLOSED by `/code-review` (2026-08-23): extended to index
membership.** The cross-check now compares the whole `Keys` set — `PK`, `UNIQUE`, `index`,
`index-predicate` — per `(table, column)` against live introspection in all three dialects, plus
`casbin_rule` (whose leg compared bare column names and so verified neither its primary key nor its
`ptype` index). New sibling helpers only; `colFacts` and the `introspect*` helpers are untouched, as
`TestMigrationParity_LogicalSchemaConverges` depends on their exclusions. Proven by ablation, not by
a green run: disabling the inline-`UNIQUE` derivation leaves
`TestAtRestParseMatchesLiveIntrospection_SQLite` **PASSING** and fails the new key check; disabling
the partial-index predicate derivation leaves the Postgres/MySQL column leg **PASSING** and fails
the new one. That is the residual, reproduced.

⚠⚠ **`/code-review` fold (2026-08-23) — the hand-rolled DDL reader was wrong in nine more ways, all
found by EXECUTING it, seven of them latent until a migration changes.** They are recorded here
because decision 7 is the decision that says this reader must be proven honest, and because seven of
the nine would first surface as a FALSE PUBLISHED SENTENCE rather than as a build failure:

1. the `CREATE INDEX` table name was the whole span between `ON` and the first `(`, so
   `ON t USING gin (col)` resolved to the table `"t USING gin"` and the index vanished silently —
   GIN/GiST is the normal way to index this schema's JSONB columns. `ON public.t (a)` failed the
   same way. Now: first token after `ON`, schema qualifier stripped, `USING <method>` recognised,
   anything else an error;
2. the `CREATE TABLE` body was delimited with `strings.LastIndex(stmt, ")")` instead of the file's
   own `matchingParen`, so `CREATE TABLE t (a TEXT, b TEXT) WITH (fillfactor=70)` published
   `TEXT) WITH (fillfactor=70` as a storage type. Now `matchingParen`, and a non-empty remainder
   after the column list fails closed — Postgres's `INHERITS (parent)` is a trailing clause that
   ADDS COLUMNS;
3. body-clause routing did an exact first-word match, which failed in BOTH directions: a column
   literally named `key`/`index` was silently DROPPED (symmetrically across dialects, so 6a stayed
   green), and `UNIQUE(g)` with no space became a PHANTOM column;
4. `CONSTRAINT pk_g PRIMARY KEY(g)` HARD-FAILED the whole schema load while unnamed `PRIMARY KEY(g)`
   parsed — three different matchers for one decision;
5. byte offsets were computed on `strings.ToUpper(stmt)` and applied to the ORIGINAL string.
   `ToUpper` is not length-preserving in UTF-8 (`ı` is 2 bytes → 1), so one non-ASCII rune before
   `ON`/`WHERE` skewed every later slice and lost the index;
6. `CREATE UNIQUE INDEX` recorded a plain `index`, dropping the one spelling of uniqueness — which
   is precisely the fact that tells a consumer deterministic ciphertext leaks duplicate detection;
7. the partial-index predicate scan did not exclude string literals, so `WHERE status = 'error'`
   annotated a column named `error`. That one OVER-claims, and `postgres/0001_init.sql` already
   carries `WHERE status IN ('completed','failed')` on tables that declare `error`/`output`/`outcome`;
8. `CHECK` and `EXCLUDE` were missing from the skip list, so an unnamed `CHECK (a > 0)` injected a
   phantom column while the NAMED form hard-errored — the fail-closed fix was half-applied;
9. `stripLineComments` cut at the first `--` anywhere in a line, including inside a string literal,
   deleting every column declared after it on that line.

⚠ **The "unrecognised table-level clause" error decision 11 leans on was UNREACHABLE** (0 coverage
hits): only the six skip-list keywords could reach it, and the switch handled all six. The skip list
is gone; the switch on the leading keyword is now the single enumeration and its `default` branch
means "column declaration", which fixes (3) and (8) structurally rather than by adding entries to a
list. The fail-closed guarantee is now delivered by reachable errors: a table-level constraint naming
a column its own `CREATE TABLE` never declared, an unaccounted-for remainder after a column list, and
an unaccounted-for clause between a `CREATE INDEX`'s table and its column list.

⚠ **None of the nine changed today's output** — the census, the per-class counts and the
Postgres 29 / MySQL 28 / SQLite 28 `keyed` totals are unchanged, which is exactly why they survived
two audit rounds and a full implementation.

**8. A `keyed` annotation is machine-derived per dialect over EVERY table, and is labelled a LOWER
BOUND.** It records PK membership (both the table-level `PRIMARY KEY (…)` clause **and** the inline
single-column form — E16), `UNIQUE`, and `CREATE INDEX` column lists including partial-index `WHERE`
predicate columns. **Foreign keys are deliberately EXCLUDED**, as a stated rule rather than an
accident of the parser: E15 measured that the schema's only foreign key is
`wrkflw_journal.instance_id`, which is already keyed via the table-level `PRIMARY KEY (instance_id,
seq)`, so including or excluding foreign keys changes **no row** of today's output. A future foreign
key on a non-PK column is therefore a decision someone makes, not a silence someone inherits.

⚠ **Recorded here in response to a Task 1 review finding.** The exclusion was decided in the spec's
open question 3 and measured in E15, but never stated in this decision — while the shipped code at
`internal/atrest/schema.go:278` cites "ADR-0187 D8" for it. That was a dangling citation (the class
tracked as backlog 134) and the citation is now true. Measured: **Postgres 29 of 87, MySQL 28 of 79, SQLite 28 of 79** (over
`wrkflw_*` alone: 27 / 28 / 28 — the divergence is partial-index folding).

⚠⚠ **Round 1 correction — this decision previously published a FALSE safety claim.** It stated that
*"every `freeform` and `policy` column is index-free and can be encrypted without breaking an
index"*, over **both** policy locations. `casbin_rule.ptype` is class `policy` and carries
`casbin_rule_ptype_idx`. The claim was derived over 79 columns and asserted over 87. **It is
withdrawn.**

⚠⚠ **A second, independent mechanism makes the withdrawal necessary regardless of any index.**
`internal/authz/casbin/pg_adapter.go` `RemovePolicy` runs
`DELETE FROM casbin_rule WHERE ptype=$1 AND v0=$2 AND … AND v5=$7` — **all seven** policy columns are
equality predicates. Encrypting any of them non-deterministically breaks `RemovePolicy` and
`RemoveFilteredPolicy`. ⇒ **the generated document states `keyed` per column and states, separately
and explicitly, that query-level equality predicates exist outside the derivation**, naming
`casbin_rule.{ptype,v0..v5}` as the known instance. No blanket "safe to encrypt" sentence is emitted.

⚠ **The bundle's own caveat predicted this and the headline ignored it.** `keyed` was labelled a
lower bound blind to query-level filtering, and a safety claim only a complete bound could support
was published anyway.

**9. The generator is a golden-file `-update` flag on the test**, wrapped by
`scripts/gen-at-rest.sh`. It needs no `main` package.

**10. No mechanism ships, and that is the decision.** No `persistence.VariableCodec`: a column codec
without a key-rotation and key-loss story converts a confidentiality problem into an availability
one. No hash-chained journal: a chain whose head lives in the database the attacker already writes to
is theatre, and ADR-0145 closes the obvious in-band home for it.

**11. The parser FAILS CLOSED.** Any statement it does not recognise is an **error**, not a skip.

⚠⚠ **Added in round 1.** The parser reads `CREATE TABLE` and `CREATE INDEX` only, and ADR-0132
states verbatim that future *"schema changes will resume as new numbered files on top of the
consolidated"* set — i.e. `ALTER TABLE … ADD COLUMN`. Against such a file the old design read zero
columns: nothing unclassified, `SECURITY.md` regenerating identically, every non-Docker guard green,
and the new column silently absent from a **security** document. That falsified this ADR's headline
Good consequence. Verified free: `grep -ric "alter table"` over all four migration files returns
**0** everywhere, so failing closed costs nothing today and converts the most likely future breakage
from silent to loud.

**12. Rendering is DETERMINISTIC.** All output is sorted by `(table, column)`, with dialect columns
in a fixed dialect order.

⚠ **Added in round 1; its justification corrected in round 2.** Both generator inputs are Go maps
and Go randomises map iteration. The round-1 figure "995 of 1000 renders differed" was **one
stochastic sample restated as a constant**; re-measured it ranges **949–995**. The stable number is
**87 distinct render orders**, so an undetermined generator would have gone green about **1 run in
87**. ⇒ the hazard is a **~1 % flaky green**, not the deterministic red the original wording
claimed — strictly worse, and a reason to keep D12 rather than to soften it.

## Consequences

**Good.**

- The enumeration cannot rot silently. Adding a column to any migration fails the build until it is
  classified; removing one fails until its entry is deleted; editing `SECURITY.md` by hand fails.
  ⚠ **This consequence is true only because of decision 11 (fail-closed parsing), added in round 1.**
  As originally written the parser read `CREATE TABLE` only, so a column added by the `ALTER TABLE`
  migration ADR-0132 says to expect would have been silently invisible — and this bullet would have
  been false for the most likely future migration shape.
- The fourth-migration-directory class of failure is closed by construction (decision 1), not by
  vigilance.
- ⚠⚠ **Round 2 correction: this bullet previously restated the WITHDRAWN safety claim as a Good
  consequence, 58 lines below decision 8 withdrawing it.** One actionable fact survives the
  withdrawal, not two: **`wrkflw_human_task.claimed_by` is indexed**, so encrypting it
  non-deterministically costs the `AssignedTo` lookup. The other — "every `freeform` and `policy`
  column is index-free" — is false and must not reappear anywhere.
- The classification surfaces that authorization policy is at rest in **three** places — the two
  `policy`-classed ones (`casbin_rule`, `wrkflw_human_task.eligibility`) plus
  `wrkflw_definitions.definition`, whose serialized nodes carry `eligible_roles` /
  `eligible_privileges` / `eligible_expr`. The existing `wrkflw_%`-filtered machinery could not have
  shown the first of those. ⚠ This bullet said **two** until `/code-review` refuted it; see decision
  5's folded correction. The count is now derived from `atrest.PolicyAtRestLocations` and
  machine-checked in both directions, so it is no longer a sentence anyone can restate wrongly.
- It reuses three conventions the repo already had rather than inventing them (E12) — the parity
  test's introspection helpers, the self-cleaning allow-list, and the liveness-guarded AST pin.

**Bad / accepted costs.**

- **A hand-rolled DDL reader now exists** and must be maintained alongside the migrations. Mitigated
  by decision 7, not eliminated. If it ever grows beyond the four files it reads, it is the wrong
  tool and should be replaced by introspection-only with a Docker requirement.
- **The everyday guard trusts the parser**; only the Docker-gated cross-check proves it honest. On a
  machine with no Docker daemon, a parser bug and a classification bug are indistinguishable. The
  plan therefore requires the cross-check to run before merge, not merely to exist.
  ⚠ **Round 1 residual, CLOSED by `/code-review` before merge:** the cross-check compared **column
  names only**, while every parser trap that justified it is in the **index** path (decision 7). It
  now compares the full per-column key set against live introspection in all three dialects and for
  `casbin_rule`. The cost stands: on a machine with no Docker daemon a parser bug and a
  classification bug are still indistinguishable, so the plan's requirement that the cross-check RUN
  before merge is unchanged — and it is now a much stronger thing to run.
- **The classification describes a FRESH database, not necessarily a deployed one.** goose keys by
  version and stores no checksum, so an in-place edit of a consolidated migration never re-applies
  to an already-migrated database (the repo documents this at `migration_parity_test.go:230-236`).
  A long-lived deployment can therefore hold a schema the generated table does not describe. Stated
  in the generated `SECURITY.md`; not otherwise mitigated.
- **`keyed` is a lower bound that reads like a guarantee** if the caveat is dropped in a later edit.
  The caveat is emitted by the generator, so removing it requires editing code, not prose.
- **Six classes is a judgement that will be argued with.** Two are carried into the audit as open
  questions on purpose (`subscriber`/`topic` as `reference`; `candidates` as `actor` rather than
  `policy`). The invariant forces the judgement to be *stated*; it cannot make it *right*.
- **`SECURITY.md` gains a large generated table** — 87 rows with per-dialect columns. It is long. The
  alternative is a shorter table that is wrong.

**Neutral.**

- Consumer-supplied migrations are out of scope; the generated text says the table covers only the
  migrations embedded in this module.
- The classification is documentation-only: nothing at runtime reads it. ⚠ **Round 1 correction:**
  it does **not** "add no production surface" — the parser and classification live in a new
  non-test `internal/atrest` package so that `go build ./...` and the 85 % coverage floor both
  apply to them. The surface is internal and no consumer can import it, which is the property that
  actually matters; the original wording overstated it.
