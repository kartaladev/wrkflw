# ADR-0187 re-audit — INTERACTION lens (round 2)

Worktree `/private/tmp/wt-0187-interaction2` detached at `3d8a20ea`
("docs(security): design the at-rest posture as a generated, machine-checked artifact").
Step 0 verified: spec, ADR, plan, adjudication all present.

Subject: the SEAMS BETWEEN THE FIXES of round 1's revision — six changed decisions,
two added, seven removals. Round 1 findings are settled and NOT re-litigated here.

Findings appended as they are established.

---
## J1 — CRITICAL — D8's withdrawal × the spec's "What `SECURITY.md` gains" section

**PAIR:** D8 (the machine-derived `keyed` annotation, whose blanket "safe to encrypt" sentence was
WITHDRAWN in the revision) × the spec section listing what the generated `SECURITY.md` block emits.

**What each side assumes.** D8's fix assumes that withdrawing the claim at the point of derivation
removes it from the delivery. The "What `SECURITY.md` gains" section assumes the claim is still
true and still the deliverable's headline — it was written in round 0 and not revisited.

**Evidence — the withdrawal**, `docs/specs/2026-08-22-at-rest-posture.md:224-238`:

> ⚠⚠ **Round 1 correction — this section previously published a FALSE safety claim** … It read:
> *"zero `freeform` and zero `policy` columns carry an index, so all 11 free-form columns and both
> policy locations can be encrypted without breaking a single index."* **`casbin_rule.ptype` is
> class `policy` and carries `casbin_rule_ptype_idx`.** … **Withdrawn.**
> ⇒ **no blanket "safe to encrypt" sentence is emitted.**

**Evidence — the same claim, still prescribed as output**, same file, lines 301-304:

> - the two sentences that fall out of the classification and are the actionable content:
>   **every `freeform` and `policy` column is index-free and can be encrypted without breaking an
>   index**, and **`wrkflw_human_task.claimed_by` is indexed, so encrypting it non-deterministically
>   costs the `AssignedTo` lookup**;

This is the WITHDRAWN sentence, verbatim in substance, prescribed as one of "the two sentences …
that are the actionable content" of the generated security document. The removal was applied where
the claim was derived and not where it is consumed. An implementer following the spec's own
deliverable list emits the false claim into `SECURITY.md` — the exact defect round 1 rated Critical.

**Fix.** Replace that bullet with what D8 now says: per-column `keyed` per dialect, plus the
separate statement that query-level equality predicates exist outside the derivation, naming
`casbin_rule.{ptype,v0..v5}`. Keep only the `wrkflw_human_task.claimed_by` half, which survives.

## J2 — CRITICAL — D8's withdrawal × the ADR's "Good" consequences

**PAIR:** D8 (the `keyed` annotation whose blanket safety sentence was withdrawn) × the ADR's
Consequences section, which still sells that sentence as one of the delivery's two selling points.

**Evidence — the withdrawal**, `docs/adr/0187-at-rest-classification-is-machine-checked.md:155-167`:

> ⚠⚠ **Round 1 correction — this decision previously published a FALSE safety claim.** It stated
> that *"every `freeform` and `policy` column is index-free …"* … **It is withdrawn.**
> … No blanket "safe to encrypt" sentence is emitted.

**Evidence — the same claim, still a Good consequence**, same file, lines 213-215:

> - Two non-obvious, actionable facts reach the consumer as generated output rather than as prose
>   someone must maintain: **every `freeform` and `policy` column is index-free**, and
>   `wrkflw_human_task.claimed_by` is not.

The withdrawn claim is restated as fact **58 lines below its own withdrawal, in the same document**.
It is also self-contradicting against the ADR's own audit banner at lines 10-13, which calls it
FALSE. Decision 8 measures `policy`-keyed = **1** (`casbin_rule.ptype`), so "every `policy` column is
index-free" is false by the ADR's own number.

**Why it is an interaction, not a leftover.** The withdrawal was made inside decision 8's own text.
The Consequences section is written against the *value proposition* of decision 8, and nothing in
the revision's grid pointed at it. Same defect class as J1 in the spec: the fix was applied at the
derivation site and not at the two consumption sites.

**Fix.** Rewrite the bullet to the surviving facts: `wrkflw_human_task.claimed_by` — the one PII
column a consumer most wants to encrypt — is indexed; and authorization policy at rest carries
both an index (`casbin_rule_ptype_idx`) and seven equality predicates outside the DDL derivation.
Note the count also drops from "two facts" to whatever survives; do not leave the numeral.

## J3 — CRITICAL — the REMOVAL of `ClassDivergencesPerDialect` × the surviving liveness-guard fixture

**PAIR:** the removal of the two divergence functions (`ClassDivergences`,
`ClassDivergencesPerDialect` — the vacuous class-divergence detectors round 1 deleted) × plan
Task 4's liveness-guard step, which still calls one of them.

**The controller's own change list records the fixture as REMOVED.** It is not. `docs/plans/2026-08-22-at-rest-posture.md:604-649` still prescribes, verbatim:

> - [ ] **Step 3: Write the LIVENESS GUARD — the step that makes the pin worth having**
> …
> 	got := atrest.ClassDivergencesPerDialect(schemas, perDialect)
> 	assert.Len(t, got, 1, "the detector must report the planted divergence")

and line 653 doubles down:

> Step 3 is a genuine RED: `ClassDivergencesPerDialect` does not exist yet.

while the same file's self-review, line 1100-1104, says the opposite:

> ⚠⚠ **The "Known gap" previously recorded here was itself the defect.** … **Round 1 deleted both**
> and replaced them with `KeysWithPrefix` plus a key-set assertion that is RED today.

and line 1096 still lists both deleted symbols in the **Type consistency** roster:

> `ColumnKey`, `Column`, `Schema`, `Class`, `Classification`, `LoadSchemas`, `ModuleRoot`,
> **`ClassDivergences`, `ClassDivergencesPerDialect`**, `Render`, `ReplaceBlock`, …

**Three compounding consequences, none of them cosmetic.**

1. An implementer executing Task 4 serially writes `ClassDivergencesPerDialect` into
   `internal/atrest` — resurrecting the machinery round 1 deleted, as **non-test production code**
   whose only caller is a test, inside a package carrying an 85 % coverage floor.
2. Its fixture plants a **class** divergence via a per-dialect classification function. The
   production classification is a flat `map[ColumnKey]Class` and the invariant that replaced it is
   about **key sets**, not classes. So the liveness guard proves a detector fires on a property the
   delivery no longer guards — it is a liveness guard for a deleted guard.
3. Task 4's `⚠ **This is a PIN, not a RED-green fix.** The property already holds` (line 562) is the
   pre-revision framing, directly contradicted by the same task's line 596 (`**This is a real RED,
   not a pin**`) and by the spec's corrected falsifiability paragraph (spec lines 316-320: "After
   D6's clause-4 replacement there is **no pin left**").

**Fix.** Delete Task 4 steps 3 and the surrounding pin framing, or re-point the liveness guard at
`KeysWithPrefix` (fixture: two dialects, one column present in both and one present in only one;
assert the diff names exactly the second). Strike both function names from the Type-consistency
roster. Fix the "PIN, not RED-green" preamble.

## J4 — CRITICAL — D6's new key-set invariant × D1/D8's inclusion of the Postgres-only `casbin_rule`

**PAIR:** D6 clause 4 (the key-set agreement invariant that replaced the vacuous class-divergence
guard) × D1 (discovery, which pulls `internal/authz/casbin/migrations` into scope) and D8 (the
`keyed` widening, which made the per-dialect totals 87/79/79 explicit).

**As stated, clause 4 has no true reading.** ADR decision 6,
`docs/adr/0187-at-rest-classification-is-machine-checked.md:132-133`:

> **the normalized `table.column` key set must be identical across all three dialects, and the
> classification must cover it exactly**

Spec D6 clause 4, `docs/specs/2026-08-22-at-rest-posture.md:185`, is the same, unscoped.

`casbin_rule` is **Postgres-only** — executed: `internal/authz/casbin/migrations/0001_casbin_rule.sql`
declares `id BIGSERIAL PRIMARY KEY`, a Postgres-only type, and the plan's own `MigrationSets`
declares it `Dialects: []string{"postgres"}` (plan line 362-365). Therefore:

- read **unscoped** (all tables): pg = 87 keys, mysql = 79, sqlite = 79 ⇒ conjunct (a) "identical
  across all three dialects" is **FALSE today** and can never be made true;
- read **scoped to `wrkflw_*`** (79 keys, which is what the plan actually implements —
  `KeysWithPrefix(schemas["postgres"], "wrkflw_")`, plan line 583): conjunct (b) "the
  classification must cover it exactly" is **FALSE** — `Classification` has 87 entries against a
  79-key set.

The plan silently resolves this by splitting the clause across two tasks — Task 3 does completeness
over the **union** (87), Task 4 does agreement over the **`wrkflw_`-scoped** set (79) — and by
adding a comment the ADR and spec never contain (plan line 581-582: "wrkflw_* only: casbin_rule is
postgres-only by construction (E3) and its absence elsewhere is not a divergence").

**Why it is an interaction.** Before the revision, D8 was scoped to `wrkflw_*` (79) and the guard
was about classes, so no scope question arose. Widening D8 to every table and replacing the guard
with a set-equality invariant, in the same revision, created a scope that neither fix had to state
— and neither did.

**Fix.** Restate decision 6 clause 4 in both the ADR and the spec as two separately-scoped
conjuncts: *the normalized `wrkflw_*` key set is identical across all three dialects* **and** *the
classification covers the union of all dialects' key sets exactly*. Say why the scopes differ
(`casbin_rule` is Postgres-only by construction). Anything less leaves the authority documents
asserting an invariant no implementation can satisfy.

## J5 — CRITICAL — D8's withdrawal × plan Task 6, where the withdrawn sentence is a CODE instruction

**PAIR:** D8's withdrawal of the blanket "safe to encrypt" sentence × plan Task 6 step 3, the step
that writes the generator.

`docs/plans/2026-08-22-at-rest-posture.md:865-867`, inside the list of what `Render` must emit:

> 3. **the two actionable sentences**: every `freeform` and `policy` column is index-free and can be
>    encrypted without breaking an index; and `wrkflw_human_task.claimed_by` **is** indexed, …

immediately followed at line 875 by:

> ⚠ **Sentences 3–7 are emitted BY THE GENERATOR, not typed into `SECURITY.md`.**

This is the third surviving copy of the withdrawn claim (J1 spec, J2 ADR, J5 plan) and the only one
that is an **instruction to write code**. Following the plan verbatim ships the false safety claim
into the published `SECURITY.md`, hard-coded in `render.go` — where, per the ADR's own consequence
"removing it requires editing code, not prose", it becomes *harder* to remove than the prose was.

It is falsified twice over by the revision's own numbers: `policy`-keyed = 1
(`casbin_rule.ptype` / `casbin_rule_ptype_idx`), and `RemovePolicy`'s seven-column equality
`DELETE`.

**Fix.** Replace item 3 with what D8 now prescribes: per-column `keyed` per dialect; the
`wrkflw_human_task.claimed_by` fact; and a separate statement that query-level equality predicates
exist outside the derivation, naming `casbin_rule.{ptype,v0..v5}`. Then re-check the "Sentences 3-7"
numbering, which shifts.

## J6 — CRITICAL — D8's `casbin_rule` widening × Task 5's own commentary and stale census numbers

**PAIR:** D8 widened the `keyed` derivation to every table and every dialect (pg 29/87, policy-keyed
= 1) × plan Task 5, whose **assertions** were updated and whose **prose around them** was not.

The assertion was correctly updated (`docs/plans/2026-08-22-at-rest-posture.md:735-741`):

> 	assert.Equal(t, map[atrest.Class]int{
> 		atrest.ClassReference: 15, atrest.ClassScalar: 8, atrest.ClassTimestamp: 4,
> 		atrest.ClassActor: 1, atrest.ClassPolicy: 1, // casbin_rule.ptype …
> 	}, byClass, "policy-keyed is ONE, not zero; do not restate the withdrawn claim")

Two paragraphs below, lines 788-793, three claims about that same code are stale or false:

> **What makes these fail today:** … neither the **27/15/7/4/1 census** nor the per-dialect predicate
> distinction has ever been computed …
> ⚠ **The `byClass` map is the load-bearing assertion** — the bare **`27`** would still pass …
> ⚠ **The `assert.Equal` on `byClass` has exactly four entries and no `freeform`/`policy` keys.**
> That is the "zero freeform, zero policy" claim expressed as an equality rather than as prose, so
> it cannot rot …

- `27/15/7/4/1` is the pre-revision, `wrkflw_`-only census. The map above it is `29 = 15/8/4/1/1`.
- "the bare `27`" — the assertion above it is `assert.Equal(t, 29, keyed, …)`.
- "**exactly four entries and no `freeform`/`policy` keys**" is **false about the code five lines
  above it**, which has **five** entries, one of them `ClassPolicy: 1`. The sentence then re-derives
  the withdrawn safety claim ("the 'zero freeform, zero policy' claim expressed as an equality") from
  a map that contains the counterexample.

A reviewer reading the rationale rather than the literal — the normal reading order — is told the
withdrawn claim is now machine-enforced. It is the round-1 Critical resurrected as a justification.

**Fix.** Rewrite the three notes to the post-revision numbers: the census is 29 = 15/8/4/1/1; the
bare `29` is insufficient without `byClass`; and `byClass` has **five** entries, whose `ClassPolicy:
1` is the counterexample that killed the blanket claim — an `assert.Equal` over the whole map is
what stops a later edit from dropping it back to four.

## J7 — MAJOR — D1's rule-not-glob fix × the plan's architecture prose, file table and self-review

**PAIR:** D1 (discovery is now a stated RULE — every directory named `migrations` and every direct
child of one containing at least one `.sql`; the glob `**/migrations/*.sql` was refuted as matching
1 of 4 files) × the three places in the plan that still describe the glob, plus D6's deleted
class-divergence guard still described as shipping.

`docs/plans/2026-08-22-at-rest-posture.md`:

- line 22 (Architecture): "Guards fail on an unclassified column, a stale classification entry, a
  `SECURITY.md` that disagrees with the generator, **a class that differs across dialects**, and …"
  — the guard round 1 deleted as vacuous.
- line 60 (File structure): "`internal/atrest/discover.go` | **glob `**/migrations/*.sql`**; the
  migration-set **declaration** and its two-way check" — the refuted glob, as the file's stated
  responsibility. Go's `filepath.Glob` has no `**` at all (ADR line 98), so an implementer following
  the file table reproduces the exact round-1 Critical.
- line 65: "`classification_test.go` | completeness, staleness, **dialect-invariance**, liveness
  guard".
- line 1076 (self-review): "D1 discovery **by glob**, never a listed directory | 2 …".

Task 2's body (lines 369-373) states the correct rule, so the plan disagrees with itself at four
places. The three summary/index locations are exactly where Premise Discipline says the surviving
false claims live.

**Fix.** Propagate the rule into all four: architecture prose → "a normalized key set that differs
across dialects"; file table → "discover directories named `migrations` or whose parent is, holding
at least one `.sql`"; test-file row → "completeness, staleness, key-set agreement"; self-review row
→ "D1 discovery by a stated rule, never a listed directory".

## J8 — CRITICAL — D2b (normalization) × D7 (the live-introspection cross-check)

**PAIR:** D2b (reserved-word aliases normalized to the canonical name **inside `LoadSchemas`**) ×
D7 / plan Task 7 (the Docker-gated test asserting the parse equals live introspection).

**What each side assumes.** D2b assumes normalization can be done once, at parse time, and that
every downstream consumer wants canonical names. D7 was written before D2b existed and assumes the
parsed names and the introspected names are directly comparable.

**They are not.** Plan Task 2 step 5 asserts the raw alias must NOT escape `LoadSchemas`
(`docs/plans/2026-08-22-at-rest-posture.md:398-403`):

> 	assert.Contains(t, schemas["mysql"].Columns,
> 		atrest.ColumnKey{Table: "wrkflw_journal", Column: "trigger"}, "normalized to the canonical name")
> 	assert.NotContains(t, schemas["mysql"].Columns,
> 		atrest.ColumnKey{Table: "wrkflw_journal", Column: "trigger_"},
> 		"the raw MySQL alias must not escape LoadSchemas")

Plan Task 7 then compares that normalized parse against **raw** introspection, line 951-952:

> 	assert.ElementsMatch(t, parsedColumnNames(parsed["mysql"], "wrkflw_"),
> 		liveColumnNames(introspectMySQL(t, mysqlDB)), "mysql: parsed vs live")

A live MySQL database has a column literally named `trigger_` — executed:
`internal/persistence/store/migrations/mysql/0001_init.sql:31` is `trigger_ JSON NOT NULL`, and
`internal/persistence/dialect/mysql.go:67` is `func (mysql) JournalTriggerColumn() string { return
"trigger_" }`. So parsed yields `trigger`, live yields `trigger_`, and `ElementsMatch` **fails with
a two-element diff**. The Docker-gated cross-check — the only thing that proves the parser honest —
can never go green as prescribed.

**The repo already shows the missing half.** `internal/persistence/store/migration_parity_test.go:71`
applies the normalization to the **introspected** side, not the parsed one:

> 	mysqlSchema := introspectMySQL(t, mysqlDB)
> 	…
> 	normalizeMySQLTriggerColumn(mysqlSchema)

and `normalizeMySQLTriggerColumn` (lines 99-113) mutates a `logicalSchema` built from live rows. The
convention the bundle claims to reuse normalizes the **live** side; D2b normalizes the **parsed**
side; the plan applies neither to Task 7.

**Fix.** Task 7 must apply the same canonicalization to `liveColumnNames`, and say so — either by
calling the existing `normalizeMySQLTriggerColumn` on the introspected schema before extracting
names, or by mapping `dialect.NewMySQL().JournalTriggerColumn()` →
`dialect.NewPostgres().JournalTriggerColumn()` in `liveColumnNames`. Add an assertion that the
MySQL leg actually sees `wrkflw_journal`'s payload column, so a future silent drop is loud.

## J9 — MAJOR — A13 accepted but never applied × D8's widening × D12's fixed dialect order

**PAIR:** A13 (the accepted round-1 finding that an empty `keyed` cell cannot distinguish "column
absent in this dialect" from "present but unindexed"; fix: *render `—` vs `n/a` distinctly*) × D8
(the widening that put `casbin_rule`'s 8 Postgres-only rows into the table) and D12 (deterministic
rendering with "a fixed dialect order").

**A13 is recorded as ACCEPTED** — `docs/plans/sweep-evidence/audit-0187-adjudication.md:181-182`:

> - **A13 — the empty `keyed` cell is ambiguous** (interaction I8): it cannot distinguish "column
>   absent in this dialect" from "present but unindexed". **FIX:** render `—` vs `n/a` distinctly.

**It appears in none of the three documents.** The spec's deliverable list
(`docs/specs/2026-08-22-at-rest-posture.md:300`) still says only:

> - the 87-row table — `table`, `column`, `class`, physical type per dialect, `keyed` per dialect;

and the plan's generator instruction (`docs/plans/2026-08-22-at-rest-posture.md:864`) is the same
sentence. No `—`/`n/a` distinction anywhere; `grep` for `n/a` in the bundle returns nothing.

**D8's widening made it worse, not neutral.** Before the revision `casbin_rule` was excluded from the
`keyed` derivation; now the 87-row table has **8 rows** (`casbin_rule.{id,ptype,v0..v5}`) whose MySQL
and SQLite physical-type **and** `keyed` cells are both empty — 16 ambiguous cells in the security
document whose entire purpose is telling a consumer what is stored where. A MySQL consumer reading
blank `keyed` for `casbin_rule.ptype` concludes "not indexed" when the truth is "this table does not
exist in your deployment".

**D12 leaves the same gap half-closed.** It fixes ordering but says nothing about the absent cell,
and **the "fixed dialect order" is never actually named** — ADR line 192-193 says "with dialect
columns in a fixed dialect order", plan line 857 says "emit dialect columns in a fixed order",
neither states *which*. Two implementers pick different orders; the golden file churns.

**Fix.** Apply A13: render `n/a` for a column absent in that dialect and `—` for present-but-unkeyed,
and state the rule in the generated legend. Name the dialect order explicitly (e.g.
`postgres, mysql, sqlite`) in D12 so it is a spec fact, not an implementer's choice.

## J10 — MAJOR — D2b (canonical names) × D9/D6 (the generated artifact is for CONSUMERS)

**PAIR:** D2b (normalize `trigger_` → `trigger` before any set operation) × the deliverable itself —
the generated `## Data at rest` table a consumer reads to decide what to encrypt.

**What each side assumes.** D2b was introduced for one reason: to make an internal set-equality
guard satisfiable. Its scope, as written, is "before any set operation". The generated table is not
a set operation — it is consumer-facing output, and it is rendered from the same normalized
`Schema` values.

**Consequence, unstated anywhere in the bundle.** The published table will carry a row
`wrkflw_journal | trigger | freeform | JSON | …` for MySQL. **No MySQL deployment has a column named
`trigger`.** Executed: the migration declares `trigger_` (`…/migrations/mysql/0001_init.sql:31`) and
`store_core.go:313` and `:370` both go through `s.dialect.JournalTriggerColumn()` at query time
precisely because the physical name differs. A consumer following the generated document to write
`ALTER TABLE wrkflw_journal ... trigger ...` on MySQL gets an error, and the document that exists to
stop enumeration rot has itself published a name that does not exist.

The bundle never asks this question: D2b is stated (ADR decision 2b, spec D2b) purely as a guard
mechanic, and neither the spec's "What `SECURITY.md` gains" list nor plan Task 6's seven-item
generator list mentions the alias.

**Fix.** Either render the per-dialect physical column name alongside the canonical key (a
`mysql name` cell, empty unless it differs), or emit an explicit note in the generated legend:
*"column names are canonical; MySQL declares `wrkflw_journal.trigger` as `trigger_` because
`trigger` is reserved — see `dialect.JournalTriggerColumn()`."* Add it to D2b's own text so the
normalization decision carries its own disclosure obligation.

## J11 — MAJOR — D2b stated generically × the schema's other `trigger_*` columns

**PAIR:** D2b (normalization sourced from `dialect.*.JournalTriggerColumn()`) × D8's widening of the
derivation to every table, which makes the normalization run over a larger column population than
the parity test it copies.

**The precedent scopes itself to one table; D2b does not.**
`internal/persistence/store/migration_parity_test.go:106-112`:

> 	journal, ok := s["wrkflw_journal"]
> 	if !ok { return }
> 	if f, exists := journal[mysqlCol]; exists { journal[canonicalCol] = f; delete(journal, mysqlCol) }

ADR decision 2b (line 107-110) and spec D2b (109-113) state only *"reserved-word column aliases are
NORMALIZED to the canonical name via `dialect.*.JournalTriggerColumn()`"* — **no table scope**, and
the plan's implementation note is `// (Exact edit depends on how you implemented it …)` (line 668).

**Why that is dangerous here specifically.** The same schema declares
`wrkflw_timers.trigger_kind` and `wrkflw_timers.trigger_payload` (executed:
`…/migrations/mysql/0001_init.sql:99-100`). An implementer who reads D2b as a name transform —
`strings.TrimSuffix(name, "_")`, or `strings.Replace(name, "trigger_", "trigger", 1)` — mangles both
into `triggerkind` / `triggerpayload` on MySQL.

**The prescribed tests do not catch it cleanly.** Task 2's census still passes (a rename does not
change `Len`, still 79), and Task 2's two assertions look only at `wrkflw_journal`. The break
surfaces two tasks later, in Task 4's `ElementsMatch`, as an opaque diff — after the classification
map has been written against the wrong names.

**Fix.** State the scope in D2b: the normalization applies to **`wrkflw_journal` only**, is an exact
whole-name match against `dialect.<d>.JournalTriggerColumn()`, and is never a substring or suffix
transform. Add a Task 2 assertion that `wrkflw_timers.trigger_kind` and `.trigger_payload` survive
untouched in the MySQL schema — it fails today against any substring implementation and passes
against the correct one.

## J12 — MAJOR — D11 (fail closed) × the ADR Good consequence D11 was added to rescue

**PAIR:** D11 (any unrecognised statement is an error) × the ADR consequence *"Adding a column to
any migration fails the build until it is classified"*, which D11's own note claims it makes true.

`docs/adr/0187-at-rest-classification-is-machine-checked.md:205-210`:

> - The enumeration cannot rot silently. Adding a column to any migration fails the build until it is
>   classified … ⚠ **This consequence is true only because of decision 11 (fail-closed parsing) …**

**It is still false, in the opposite direction.** With D11, the `ALTER TABLE … ADD COLUMN` file
ADR-0132 predicts does not "fail until it is classified" — `ParseSQL` returns an **error**, so
`LoadSchemas` errors, so every guard fails at `require.NoError` **before any classification is even
consulted**. Classifying the new column cannot make the build green; only extending the parser can.
`scripts/gen-at-rest.sh` also fails, so `SECURITY.md` cannot be regenerated in the meantime.

That is arguably the right trade — loud beats silent — but the bundle prescribes **no remediation
path** for it, and the consequence sentence tells a future maintainer to expect one that does not
exist. Executed and confirmed free today: the four migration files' `Up` sections contain **only**
`CREATE TABLE` and `CREATE INDEX` statements (no `ALTER`, `INSERT`, `PRAGMA`, `SET`, `DROP` before
the `Down` marker), so nothing bricks now.

Second, smaller edge in the same pair: D1 discovers directories by **name**, anywhere under the
module root. Combined with fail-closed parsing and the two-way `MigrationSets` check, any stray
directory named `migrations` holding a `.sql` file — a scratch dump, a fixture, an untracked local
file — now **fails the whole `internal/atrest` package**, not just its own check. Before this
revision the glob was inert and a stray file was harmless.

**Fix.** Add to the ADR's consequence and to `SECURITY.md`'s generated caveat: *a migration shape the
reader does not recognise fails the build, and the remedy is to teach `internal/atrest/schema.go`
that shape — classification alone will not clear it.* Optionally scope `DiscoverMigrationDirs` to
tracked directories, or state that untracked `.sql` under a `migrations` directory is a deliberate
hard failure.

## J13 — MAJOR — D8's widening (the census IS the acceptance test) × Task 1's trap enumeration

**PAIR:** D8 (the `keyed` derivation now covers every table and dialect, and the plan turns the
per-dialect counts 29/28/28 into assertions) × Task 1, which enumerates the parser shapes to test —
three "traps" plus a depth-aware split.

**Executed, independent parse of all four migration files** (my own reader, D2b normalization
applied): postgres 87 columns / 29 keyed; mysql 79 / 28; sqlite 79 / 28; postgres keyed-by-class =
`reference 15, scalar 8, timestamp 4, actor 1, policy 1`; classification 27/19/17/11/8/5 = 87; zero
stale entries; the `wrkflw_*` key sets identical across all three dialects after normalization.
**Every number the revision introduced reproduces.** So D8's new figures are sound — the defect is
in what proves them.

**The one shape those figures depend on has no prescribed test.** Same run:
`wrkflw_outbox.dedup_key` is the **only** column keyed *solely* by an inline column-level `UNIQUE`,
and it is so in **all three dialects** (`postgres/0001_init.sql:45`, `sqlite:52`, `mysql:43` — all
`dedup_key … NOT NULL UNIQUE`). Drop that shape from the reader and every prescribed count moves:
29→28, 28→27, 28→27, and `reference` 15→14. Task 1's trap tests cover multi-line `CREATE INDEX`,
MySQL inline `INDEX`, MySQL table-level `CONSTRAINT`, and the `strftime` comma — **not** inline
`UNIQUE`. Task 5's Produces block lists `"UNIQUE"` as a `Keys` value with no test behind it.

**Why it is an interaction.** Before the revision `keyed` was a `wrkflw_`-only lower bound whose exact
value carried a weaker claim; the revision made the per-dialect counts the derivation's acceptance
criteria, which promotes every contributing parser shape to load-bearing. The trap list was not
re-derived against that promotion.

**Fix.** Add an inline-`UNIQUE` case to Task 1's `TestParseSQL_RealWorldTraps`
(`a TEXT NOT NULL UNIQUE` ⇒ `Keys` contains `"UNIQUE"`), and note in Task 5 that `dedup_key` is the
sole real instance, so the three counts depend on it.

## J14 — MAJOR — accepted fixes A15 and A19 recorded in the adjudication, applied to no document that drives implementation

**PAIR:** the adjudication (an INPUT to round 2, not a conclusion) × the three bundle documents.

**A15** — `audit-0187-adjudication.md:186-189`: *"goose stores no checksum, so the classification
describes a FRESH database … **FIX:** state it in the generated `SECURITY.md`."* It reached the ADR's
Bad-costs list (`0187-…md:232-236`, "Stated in the generated `SECURITY.md`") and **nowhere else**:
the spec's "What `SECURITY.md` gains" list (spec 297-310) has seven bullets and none is this, and
plan Task 6 step 3's generator list (plan 863-873) has seven items and none is this. The ADR
promises the generator emits a caveat that neither the spec nor the implementation instruction
tells anyone to emit.

**A19** — `audit-0187-adjudication.md:203-204`: *"the spec's own 'cheap fix' for the
uncross-checked-table hole appears nowhere in the plan. **FIX:** add the task, or withdraw the
sentence."* **Neither was done.** The sentence still stands at spec line 447:

> Stated as a known hole; a cheap fix is a test asserting every parsed table appears in some
> cross-check.

and plan Task 7 still adds only the two cross-checks it had. This interacts with D1 and D11: the
revision made discovery broader (any `migrations`-named directory) and parsing fatal, so the class
of "table that is parsed, classified and never cross-checked" is now **easier** to enter than when
A19 was filed, not harder.

**Fix.** Add the caveat to the spec's deliverable list and to plan Task 6 step 3 (A15). For A19,
either add a step to Task 7 asserting every parsed table name appears in some cross-check's
expected set, or delete the "cheap fix" sentence from the spec and state the hole as accepted.

## J15 — MAJOR — the revision's own decision count × A12's accepted recompute, and D7's residual missing from the spec

**PAIR:** the ADDITION of D2b, D11 and D12 × the spec's author interaction pass and the D7 residual,
both of which were sized against the pre-revision decision list.

**(a) The pair count was accepted for recompute and was not recomputed.**
`audit-0187-adjudication.md:177-180` (A12): *"number the non-goal as D10 in the spec; the pair count
becomes 45 + the two new decisions from A5/A6 → **recompute**."* The spec's D10 numbering **was**
applied (spec line 37). The recompute was not: spec line 395 still reads

> Ten decisions ship together.

There are now **thirteen** decisions in the ADR-plus-spec set (D1, D2, D2b, D3–D12). The round-1
verdict box directly above that sentence (spec 385-393) criticises this exact section for claiming
"ten decisions … 45 pairs" while writing 8 — and the corrected sentence still says ten. This is the
Premise Discipline recap failure reproducing inside the paragraph that documents it.

**(b) D7's residual reached the ADR and not the spec.** ADR decision 7 carries it twice (lines
145-148 and 229-231): *"the cross-check compares **column names only**, while all three parser traps
that justified it are in the **index** path."* The spec's D7 (lines 199-210) is the pre-revision
text with **no residual at all**, and still costs the cross-check as free reuse ("which already
exist in the same package (E12)"), which A16 explicitly asked to correct. Plan Task 7 line 907-908
likewise still says "with no refactoring (E12)" while its own steps add three new sibling helpers
(`parsedColumnNames`, `liveColumnNames`, `columnsOfTable`).

**Fix.** Recount and restate the spec's interaction-pass preamble against the real decision list.
Copy D7's residual into the spec's D7 and correct the "no refactoring" costing in both the spec and
plan Task 7.

## J16 — MINOR — symbol ownership contradictions created by the Task-4 replacement

**PAIR:** the replacement of `ClassDivergences*` with `KeysWithPrefix` (Task 4) and the widening of
`keyed` (Task 5) × the plan's per-task **Files** and **Produces** blocks.

- `KeysWithPrefix` is introduced by Task 4 step 2 as an **exported production function**, but Task 4's
  Files block (plan lines 553-555) lists only `Modify: internal/atrest/classification_test.go`, and
  its commit line (line 689) stages `classification_test.go` + `classification.go`. No task declares
  where `KeysWithPrefix` lives.
- Task 4 step 5's mutation edits `internal/atrest/discover.go`, a file Task 4's Files block does not
  mention.
- `Column.Keys` is asserted by Task 1 step 5 (`"PK"`, `"index"`, `"index-predicate"`) and
  simultaneously claimed as Task 5's output (plan line 702: *"Produces: `Column.Keys []string`
  populated with …"*), so the self-review's *"each defined in exactly one task's **Produces** block"*
  (line 1097) is false for it as well as for the two deleted divergence functions (J3).
- Deleting `if k.Table == "casbin_rule" { continue }` means `byClass[atrest.Classification[k]]++` now
  runs over `casbin_rule` keys; a missing classification entry increments the **zero-value** `Class`
  ("") rather than erroring, so a normalization or classification miss shows up as a mystery `"": 1`
  bucket in an `assert.Equal` diff.

**Fix.** Give `KeysWithPrefix` a declared home (`schema.go` alongside `Schema`), add `discover.go` to
Task 4's Files, move `Column.Keys` ownership wholly into Task 1 (Task 5 consumes it), and have Task
5's loop `require` a classification hit rather than silently bucketing the zero value.

## J17 — MINOR — the falsifiability table's new row describes a different test's failure

**PAIR:** D6's new key-set invariant × the spec's falsifiability table, rewritten in the same
revision.

Spec line 328:

> | the normalized key set agrees across dialects | **fails today** without D2b — MySQL's `trigger_`
> makes the union 88 against a 87-key classification |

The union-88-vs-87 failure is the **completeness** guard (plan Task 3,
`TestClassificationCoversTheSchemaExactly`). The row's own assertion — key-set agreement (plan Task
4, `TestNormalizedKeySetAgreesAcrossDialects`) — fails differently: an `ElementsMatch` diff of
`trigger` vs `trigger_` between the Postgres and MySQL 79-key sets. Both are real REDs; the table
attributes one test's failure mode to the other. Premise Discipline requires the prescribed test to
state what makes **that** test fail.

**Fix.** Split the row, or restate it as *"`ElementsMatch` reports `trigger` present only in
postgres/sqlite and `trigger_` only in mysql"*.

---

## Summary

| ID | severity | the PAIR | one-line |
|---|---|---|---|
| J1 | **CRITICAL** | D8's withdrawal × spec "What `SECURITY.md` gains" | the withdrawn "safe to encrypt" sentence is still the spec's stated deliverable |
| J2 | **CRITICAL** | D8's withdrawal × ADR Consequences | the same claim restated as a Good consequence 58 lines below its own withdrawal |
| J3 | **CRITICAL** | removal of `ClassDivergences*` × plan Task 4 | the liveness-guard fixture recorded as REMOVED is still prescribed, and both deleted symbols are still in the type roster |
| J4 | **CRITICAL** | D6's key-set invariant × D1/D8 pulling in Postgres-only `casbin_rule` | clause 4 as written in ADR+spec has no true reading; only the plan silently scopes it |
| J5 | **CRITICAL** | D8's withdrawal × plan Task 6 step 3 | the withdrawn sentence survives as a **code instruction** to the generator |
| J6 | **CRITICAL** | D8's casbin widening × Task 5's own commentary | prose below the fixed assertion says the map "has exactly four entries and no policy keys" — it has five, one being `ClassPolicy: 1`; stale 27/15/7/4/1 census |
| J7 | MAJOR | D1's rule-not-glob fix × plan architecture/file-table/self-review | the refuted glob `**/migrations/*.sql` is still `discover.go`'s stated responsibility |
| J8 | **CRITICAL** | D2b (normalize parsed) × D7 (compare against raw live) | Task 7's MySQL leg compares `trigger` against live `trigger_` — the cross-check can never go green |
| J9 | MAJOR | A13 (accepted, unapplied) × D8 widening × D12 | 16 ambiguous cells for Postgres-only `casbin_rule`; and D12's "fixed dialect order" is never named |
| J10 | MAJOR | D2b × the consumer-facing generated artifact | the published table names `wrkflw_journal.trigger`, a column no MySQL deployment has |
| J11 | MAJOR | D2b stated unscoped × `wrkflw_timers.trigger_kind/_payload` | a substring/suffix implementation mangles two real columns; no prescribed test catches it at Task 2 |
| J12 | MAJOR | D11 (fail closed) × the ADR consequence it was added to rescue | an `ALTER TABLE` migration fails before classification is consulted; consequence still false, no remediation path |
| J13 | MAJOR | D8's census-as-acceptance-test × Task 1's trap enumeration | inline `UNIQUE` is untested and is the sole shape carrying `dedup_key` into all three counts |
| J14 | MAJOR | adjudication × the three documents | A15 and A19, both ACCEPTED, applied to no document that drives implementation |
| J15 | MAJOR | D2b/D11/D12 additions × A12's accepted recompute, and D7's residual | "Ten decisions ship together" against thirteen; D7's residual reached the ADR only |
| J16 | MINOR | Task-4 replacement + Task-5 widening × per-task Files/Produces blocks | `KeysWithPrefix` has no declared home; `Column.Keys` claimed by two tasks; zero-value `Class` bucket |
| J17 | MINOR | D6's new invariant × the rewritten falsifiability table | the new row states the *completeness* guard's failure mode for the *agreement* test |

**7 Critical · 8 Major · 2 Minor = 17.**

⚠ **Concentration, and it is the finding above the findings.** Five of the seven Criticals (J1, J2,
J5, J6, and the roster half of J3) are **one fix applied at its derivation site and nowhere else**.
The revision edited D8 and D6 where they are *defined* and left every place they are *consumed* —
the spec's deliverable list, the ADR's Consequences, the plan's generator instruction, the plan's
architecture prose, file table, self-review roster, and Task 5's rationale. A revision's blast
radius is its **consumers**, and the changed-decision grid does not name them. Compare the standing
lesson from ADR-0179 (*"when a feature revises shared helpers, enumerate the BYPASSERS, not the
callers"*): here the dual applies — when a revision withdraws a claim, enumerate every restatement.

⚠ **The withdrawn safety sentence survives in THREE of the four documents.** The adjudication
records A1's fix as accepted and lists five places the claim appeared ("E9, ADR decision 8, spec D8,
the ADR's Consequences, **and** the prescribed `SECURITY.md` text"). Two of those five were fixed.

## Verification performed (executed, not read)

Independent reader over all four migration files, D2b normalization applied, run in the worktree:

```
postgres columns= 87 keyed= 29     mysql columns= 79 keyed= 28     sqlite columns= 79 keyed= 28
pg-vs-mysql  onlyPG=[] onlymysql=[]        pg-vs-sqlite onlyPG=[] onlysqlite=[]
classification size: 87   per class: reference 27, timestamp 19, scalar 17, freeform 11, policy 8, actor 5
postgres keyed byClass: reference 15, scalar 8, timestamp 4, actor 1, policy 1
mysql / sqlite keyed byClass: reference 15, scalar 7, timestamp 5, actor 1
stale classification entries: []   union size: 87
UNIQUE-only keyed, every dialect: wrkflw_outbox.dedup_key
postgres outbox.status keys: ['index-predicate']   mysql/sqlite: ['index']
```

⇒ **Every number the revision introduced reproduces**: 87/79/79, 29/28/28, `policy`-keyed = 1, the
six class counts, zero stale entries, and post-normalization key-set agreement across dialects. Plan
Task 5's `byClass` map (15/8/4/1/1) and `TestKeyedIsDialectDependent` are both correct as written.
The defects above are in what the documents *say about* those numbers, not in the numbers.

Also executed: the four migration files' `Up` sections contain only `CREATE TABLE` and
`CREATE INDEX` (D11 costs nothing today); `find . -name '*.sql'` returns exactly the four files;
`casbin_rule` declares `id BIGSERIAL PRIMARY KEY` (Postgres-only, confirming J4's premise);
`normalizeMySQLTriggerColumn` mutates the **introspected** schema (`migration_parity_test.go:71`,
confirming J8); `wrkflw_timers.trigger_kind` / `trigger_payload` exist (confirming J11).

## Coverage note — the survivor × removed grid

**Removed items (7), each paired against the surviving decisions and tasks:**

| removed | pairs derived | outcome |
|---|---|---|
| R1 `ClassDivergences` + `ClassDivergencesPerDialect` | × Task 4 steps 3-4; × plan self-review type roster; × D6 clause 4; × D12 | **J3** (both still prescribed and still rostered). D12 pair dismissed — determinism binds `Render`, not a `[]string` detector. |
| R2 the class-divergence guard (old D6 clause 4) | × plan Architecture prose; × file-structure table; × D4's 87; × D7 | **J7** (still described as shipping), **J4** (its replacement's scope). D7 pair dismissed — the deleted guard was never cross-checked, so removing it takes nothing from D7. |
| R3 `if k.Table == "casbin_rule" { continue }` | × Task 5 rationale; × Task 5 loop semantics; × D5 | **J6**, **J16**. D5 pair dismissed by execution — `wrkflw_human_task.eligibility` is unkeyed in all three dialects, so removing the skip does not touch D5's class or its "two places" sentence. |
| R4 the "safe to encrypt" sentence | × spec deliverable list; × ADR Consequences; × plan Task 6 step 3; × D10; × D9 | **J1, J2, J5**. D10 pair dismissed — withdrawing an encryption-safety claim cannot conflict with "no codec ships". D9 pair dismissed — the golden-file mechanism is indifferent to the sentence's content. |
| R5 old Task 4 mutation (`note`→`note_text`) | × D2b; × Task 4 Files block | Correctly re-aimed at the normalization (verified consistent with what Task 4 asserts). **J16** for the Files block, which still omits `discover.go`. |
| R6 plan self-review "Known gap" note | × the type roster it sat beside; × D11 | **J3**. D11 pair dismissed — unrelated subject matter. |
| R7 old liveness-guard fixture | × Task 4 | **J3 — R7 WAS NOT ACTUALLY REMOVED.** The change list records it as removed; plan lines 604-649 still carry it verbatim. |

**Added × survivor pairs derived:** D11×D1 (**J12**), D11×D7 (examined — today's corpus parses
cleanly, so no break beyond J12's stray-directory blast radius), D11×D9/`gen-at-rest.sh` (**J12**),
D12×D8×A13 (**J9**), D12×D6 clause 3 (dismissed — determinism is precisely what makes the drift
guard satisfiable; correctly paired), D2b×D3 (**J8, J10, J11**), D2b×D8 — the controller's first
lead — (**confirmed and deepened into J11**: the hazard is not casbin, which has no alias, but the
`trigger_kind`/`trigger_payload` columns an unscoped rule would mangle), D2b×D4 (dismissed by
execution — 87 holds after normalization), D6-new×D2b ordering — the controller's second lead —
(**confirmed sound in the plan**, Task 2 precedes Task 4; the defect is one level up, in the
invariant's **scope**: **J4**), D6-new×D4's 87 (**J4**), D8-withdrawal×D5 (dismissed — D5's "two
places" sentence survives unchanged and is still true).

**Deliberately not re-litigated (round 1, settled):** the per-dialect-vs-invariant presentation
asymmetry the author's own pass marked UNRESOLVED; the `-update`-propagates-a-wrong-classification
limitation (D6×D9); the fifth-migration-set uncross-checked hole *as a design gap* — though its
**unapplied fix** is reported as J14.

**Not verified (out of reach):** anything requiring Docker — plan Task 7's Postgres and MySQL legs
were reasoned from the migration text and the existing introspection helpers, never run.
`UNVERIFIED (needs Docker)`.
