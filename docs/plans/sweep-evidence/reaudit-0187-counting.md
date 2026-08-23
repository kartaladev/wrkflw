# ADR-0187 re-audit (ROUND 2) — COUNTING lens

Worktree: `/private/tmp/wt-0187-counting2` detached at `3d8a20ea`.
Scope: the numbers the REVISION introduced. Round 1's 64 findings are settled.
Status: COMPLETE — 17 findings: 4 Critical (K1, K2, K7, K10), 7 Major (K6, K8, K11, K12, K13, K15, K16), 2 Medium (K4, K17), 2 Minor (K5, K14), 2 confirmed-no-defect (K3, K9). Summary table at the end.

---

## K1 — CRITICAL — the plan's prose still describes the WITHDRAWN four-entry `byClass` map, three lines under the corrected five-entry code

**Quoted verbatim**, `docs/plans/2026-08-22-at-rest-posture.md:788-793` (Task 5, "The `keyed`
derivation, per dialect"):

> **What makes these fail today:** task 1's reader records key membership only for the shapes its own
> trap tests exercised; neither the **27/15/7/4/1** census nor the per-dialect predicate distinction has
> ever been computed over the real files. ⚠ **The `byClass` map is the load-bearing assertion** — the
> bare **`27`** would still pass if the derivation keyed the wrong 27 columns.
>
> ⚠ **The `assert.Equal` on `byClass` has exactly four entries and no `freeform`/`policy` keys.** That
> is the "zero freeform, zero policy" claim expressed as an equality rather than as prose, so it
> cannot rot: `assert.Equal` on the whole map fails if a fifth class appears.

The Go code **immediately above it** (same file, lines 725-742) is the revised version and says the
opposite:

```go
	assert.Equal(t, 29, keyed, "29 of the 87 postgres columns are keyed, casbin_rule INCLUDED")
	assert.Equal(t, map[atrest.Class]int{
		atrest.ClassReference: 15,
		atrest.ClassScalar:    8,
		atrest.ClassTimestamp: 4,
		atrest.ClassActor:     1,
		atrest.ClassPolicy:    1, // casbin_rule.ptype — the counterexample the old test skipped
	}, byClass, "policy-keyed is ONE, not zero; do not restate the withdrawn claim")
```

**Independent re-derivation** (own parser, `scratchpad/ddl.py` + `keyed.py`; keyed = PK ∪ UNIQUE ∪
index column lists ∪ partial-index `WHERE` predicate columns):

```
postgres: keyed 27 of 79 columns          <- wrkflw_* only
casbin:   keyed 2 of 8 columns
            casbin_rule.id
            casbin_rule.ptype
POSTGRES DEPLOYMENT (wrkflw_* + casbin_rule): keyed 29 of 87
```

The code block's `29` and the five-entry map are CORRECT. The prose under it is the **round-1
withdrawn** census (`27` total; `reference 15, scalar 7, timestamp 4, actor 1`; "zero freeform, zero
policy"). Three separate defects in six lines:

1. `27/15/7/4/1` is the superseded census. Correct: `29 / reference 15 / scalar 8 / timestamp 4 /
   actor 1 / policy 1`.
2. "the bare `27` would still pass" — the assertion in the code is `29`.
3. "**has exactly four entries and no `freeform`/`policy` keys** … the 'zero freeform, zero policy'
   claim expressed as an equality" — the map has **five** entries and **does** carry
   `ClassPolicy: 1`. This is the exact sentence ADR-0187 decision 8 says is **withdrawn**, restated
   as a plan instruction.

**Why this is Critical and not cosmetic.** The plan is the implementation input. An implementer who
reads the prose as authoritative (it is the "what makes this fail today" falsifiability statement,
which CLAUDE.md's Premise Discipline says the implementer MUST honour) writes the four-entry map,
sees RED against the real schema, and the documented way to make a four-entry map green is to
restore `if k.Table == "casbin_rule" { continue }` — the exact skip the same task's comment says was
the round-1 Critical. The bundle would then re-ship the withdrawn safety claim with a green test
under it.

**Fix.** Replace the three sentences with: *"neither the 29 / reference 15 / scalar 8 / timestamp 4 /
actor 1 / policy 1 census nor the per-dialect predicate distinction has ever been computed over the
real files. The bare `29` would still pass if the derivation keyed the wrong 29 columns. The
`byClass` map has FIVE entries including `ClassPolicy: 1`; `assert.Equal` over the whole map fails if
that entry is dropped, which is the withdrawn 'zero policy' claim expressed as an equality that
fails."*

---

## K2 — CRITICAL — the WITHDRAWN safety sentence survives in FOUR places, and the plan still tells the generator to PUBLISH it

The revision withdrew *"every `freeform` and `policy` column is index-free and can be encrypted
without breaking an index"* in ADR decision 8 and spec D8. It left the same sentence standing —
unhedged, as an instruction or as a fact — in four other places, one of which is the **generator's
content specification**.

**Site 1 — ADR Consequences → Good**, `docs/adr/0187-at-rest-classification-is-machine-checked.md:213-215`:

> - Two non-obvious, actionable facts reach the consumer as generated output rather than as prose
>   someone must maintain: **every `freeform` and `policy` column is index-free**, and
>   `wrkflw_human_task.claimed_by` is not.

Same file, line 156, 57 lines earlier: *"**It is withdrawn.**"*

**Site 2 — spec, "What `SECURITY.md` gains"**, `docs/specs/2026-08-22-at-rest-posture.md:299-302`:

> - the two sentences that fall out of the classification and are **the actionable content**:
>   **every `freeform` and `policy` column is index-free and can be encrypted without breaking an
>   index**, and **`wrkflw_human_task.claimed_by` is indexed, …**

Same file, line 233, 66 lines earlier: *"⇒ **no blanket \"safe to encrypt\" sentence is emitted.**"*

**Site 3 — plan, Task 6 step 3, the generator's content list**,
`docs/plans/2026-08-22-at-rest-posture.md:865-867`:

> 3. **the two actionable sentences**: every `freeform` and `policy` column is index-free and can be
>    encrypted without breaking an index; and `wrkflw_human_task.claimed_by` **is** indexed, so
>    encrypting it non-deterministically costs the `AssignedTo` lookup;

followed 8 lines later by:

> ⚠ **Sentences 3–7 are emitted BY THE GENERATOR, not typed into `SECURITY.md`.**

**Site 4 — the evidence file, E9**, `docs/specs/2026-08-22-adr-0187-measurements.md:190,196,204-205`,
still headed with the superseded scope and carrying the refuted star, with no round-1 annotation:

> ## E9 — The `keyed` lower bound: **27 of 79** Postgres columns
> …
> ```
> DISTINCT KEYED COLUMNS: 27 of 79
> --- keyed, by class ---
>   15 reference
>    7 scalar
>    4 timestamp
>    1 actor
> ```
> ⭐ **Zero `freeform` and zero `policy` columns are keyed.** All 11 free-form columns and **both policy
> locations are index-free in Postgres.**

**Independent re-derivation** (own parser; `casbin_rule` included, which is what "both policy
locations" quantifies over):

```
casbin:   keyed 2 of 8 columns
    casbin_rule.id
    casbin_rule.ptype
POSTGRES DEPLOYMENT (wrkflw_* + casbin_rule): keyed 29 of 87
```

and the DDL itself, `internal/authz/casbin/migrations/0001_casbin_rule.sql:11`:

```sql
CREATE INDEX casbin_rule_ptype_idx ON casbin_rule (ptype);
```

**Corrected value.** `policy`-keyed = **1**, not 0. "Both policy locations are index-free" is
**FALSE**: `wrkflw_human_task.eligibility` is index-free, `casbin_rule.ptype` is not.

**Anchor error in site 4 specifically.** E9's `27 of 79` is measured over `wrkflw_*` only, and its
star sentence quantifies over "**both** policy locations" — one of which (`casbin_rule`) is **not in
the 79**. That is the identical derived-at-79 / asserted-at-87 scope error the ADR banner names as
round 1's decisive Critical, still live in the evidence file the ADR cites as its proof.

**Why Critical, not Major.** Site 3 is executable design: the generator ships the sentence into
`SECURITY.md`, i.e. to consumers. ADR-0187's own Context says *"A consumer who encrypts the columns
we name and leaves the rest in the clear has been harmed by our documentation."* A consumer reading
the generated line encrypts `casbin_rule.ptype` and breaks `casbin_rule_ptype_idx` **and**
`RemovePolicy`'s seven-column equality delete. Sites 1 and 2 are the ADR/spec asserting the same to
a reviewer.

**Fix.**
- Site 1: replace with *"one non-obvious, actionable fact … : `wrkflw_human_task.claimed_by` is
  keyed. The `keyed` column per dialect is emitted per row; no blanket safe-to-encrypt sentence is
  emitted (decision 8)."*
- Site 2 and site 3: replace item 3 with the D8-sanctioned pair — the per-column `keyed` annotation
  plus the explicit statement that query-level equality predicates exist outside the derivation,
  naming `casbin_rule.{ptype,v0..v5}`. Delete "the two actionable sentences" framing.
- Site 4: retitle E9 *"the `keyed` lower bound: 27 of 79 `wrkflw_*` Postgres columns; 29 of 87 for a
  Postgres + `FromDB` deployment"*, add the casbin sub-count (`id` via PK, `ptype` via
  `casbin_rule_ptype_idx`), replace the star with *"zero `freeform` and zero `policy` columns are
  keyed **within `wrkflw_*`**; over the full 87, `policy`-keyed = 1 (`casbin_rule.ptype`)"*, and add
  the byClass line `scalar 7 → 8` for `casbin_rule.id`.

---

## K3 — CONFIRMED (no defect) — D1's discovery rule matches exactly 4 directories repo-wide; nothing unintended is pulled in

**Quoted verbatim**, ADR decision 1, `docs/adr/0187-at-rest-classification-is-machine-checked.md:83-85`:

> The rule is: *every directory named `migrations`, and every direct child directory of one, that
> contains at least one `.sql` file.*

**Independent re-derivation** (`os.walk` over the whole worktree, `.git` excluded):

```
candidate dirs matched by D1's RULE (a dir named 'migrations' + its DIRECT children):
  ./internal/authz/casbin/migrations                    .sql=1  -> DISCOVERED
  ./internal/persistence/store/migrations               .sql=0  -> skipped (no .sql)
  ./internal/persistence/store/migrations/mysql         .sql=1  -> DISCOVERED
  ./internal/persistence/store/migrations/postgres      .sql=1  -> DISCOVERED
  ./internal/persistence/store/migrations/sqlite        .sql=1  -> DISCOVERED
TOTAL DISCOVERED: 4
```

```
$ find . -type d -iname '*migration*' -not -path './.git/*'
./internal/persistence/store/migrations
./internal/authz/casbin/migrations
$ ls -d vendor          -> No such file or directory
$ find . -type d -name testdata -not -path './.git/*'
./definition/testdata
./definition/model/testdata
```

The `.sql`-file predicate is load-bearing exactly once: `store/migrations` is a candidate that holds
no `.sql` and is correctly skipped. No `vendor/` exists; neither `testdata` directory is named
`migrations` nor is a direct child of one. **Verdict: 4 is right, the rule is safe today.** No fix.

---

## K4 — MEDIUM — D11's "verified free" evidence tests ONE statement form and is restated as a claim about ALL statements

**Quoted verbatim**, ADR decision 11,
`docs/adr/0187-at-rest-classification-is-machine-checked.md:200-202`:

> Verified free: `grep -ric "alter table"` over all four migration files returns
> **0** everywhere, so failing closed costs nothing today and converts the most likely future breakage
> from silent to loud.

Restated in the spec, `docs/specs/2026-08-22-at-rest-posture.md:64`:

> `grep -ric "alter table"` over all four migration files returns **0** everywhere.

**The grep's own result is true.** Verbatim:

```
internal/authz/casbin/migrations/0001_casbin_rule.sql:0
internal/persistence/store/migrations/mysql/0001_init.sql:0
internal/persistence/store/migrations/postgres/0001_init.sql:0
internal/persistence/store/migrations/sqlite/0001_init.sql:0
```

**But the ANCHOR shifts between the evidence and the conclusion.** `grep "alter table"` counts one
statement form; *"failing closed costs nothing today"* quantifies over **every** statement form the
parser does not recognise. `CREATE UNIQUE INDEX`, `CREATE TABLE … AS`, `CREATE TRIGGER`, `CREATE
VIEW`, `INSERT`, `SET`, `PRAGMA`, `-- +goose StatementBegin` and a stray trailing token would each
break the delivery on day one and none of them is in the grep. E14 rules out only the goose
directives.

**Correct evidence** — re-derived by tokenising the Up half of all four files (same rule the plan
prescribes for `ParseSQL`: truncate at `-- +goose Down`, strip `--`, split on `;`) and histogramming
the leading two tokens:

```
--- ALL statement leading keywords, UP half only ---
postgres {'CREATE TABLE': 9, 'CREATE INDEX': 11}
mysql    {'CREATE TABLE': 9, 'CREATE INDEX': 8}
sqlite   {'CREATE TABLE': 9, 'CREATE INDEX': 11}
casbin   {'CREATE TABLE': 1, 'CREATE INDEX': 1}
```

and my own parser reports `0 UNRECOGNISED` for all four files.

**The conclusion HOLDS** — the Up halves contain `CREATE TABLE` and `CREATE INDEX` and nothing else,
so D11 is genuinely free today. The defect is that the bundle's stated evidence does not establish
its stated conclusion; a reader (or a later editor) who trusts the grep as the check will re-run the
grep, not the census, and the next `CREATE UNIQUE INDEX` lands with the guard reporting green
justification.

**Fix.** Replace the grep in both places with the census that actually bounds the claim:

> Verified free: tokenising the `-- +goose Up` half of all four migration files yields **only**
> `CREATE TABLE` (9/9/9/1) and `CREATE INDEX` (11/8/11/1) — **no other statement form exists in the
> corpus**, so failing closed costs nothing today.

and add it to `docs/specs/2026-08-22-adr-0187-measurements.md` as a new E-numbered measurement (E14
covers goose directives only, not statement forms).

---

## K5 — MINOR (repo defect surfaced while re-deriving, not a bundle number) — MySQL's `-- +goose Down` drops 8 tables; Postgres and SQLite drop 9

Re-derived over the whole file with no `Down` truncation:

```
--- WHOLE FILE (no Down truncation), leading keywords ---
postgres {'CREATE TABLE': 9, 'CREATE INDEX': 11, 'DROP TABLE': 9}
mysql    {'CREATE TABLE': 9, 'CREATE INDEX': 8,  'DROP TABLE': 8}
sqlite   {'CREATE TABLE': 9, 'CREATE INDEX': 11, 'DROP TABLE': 9}
casbin   {'CREATE TABLE': 1, 'CREATE INDEX': 1,  'DROP TABLE': 1}
```

`internal/persistence/store/migrations/mysql/0001_init.sql` creates `wrkflw_outbox` and never drops
it: its `Down` list is `wrkflw_human_task, wrkflw_chain_links, wrkflw_timers, wrkflw_call_links,
wrkflw_processed_message, wrkflw_definitions, wrkflw_journal, wrkflw_instances` — `wrkflw_outbox` is
absent, where both other dialects list it.

**Not in scope for ADR-0187's numbers** (the parser truncates at `Down`, so this cannot affect any
classification count) and **not a round-2 blocker**. Recorded because the delivery's premise is that
the migration corpus is machine-checked and this asymmetry is invisible to every guard the bundle
prescribes — `goose down` on MySQL leaves `wrkflw_outbox` behind. Suggest a backlog item, or a
one-line extension of `migration_parity_test.go` asserting each dialect's `Down` drops exactly the
tables its `Up` creates.

---

## K6 — MAJOR (ANCHOR) — "the normalized key set" means 79 in one half of the guard sentence and 87 in the other

**Quoted verbatim**, ADR decision 6,
`docs/adr/0187-at-rest-classification-is-machine-checked.md:130-133`:

> An unclassified column fails; a classification entry naming an absent column fails (self-cleaning);
> a `SECURITY.md` block differing from the generator fails; **the normalized `table.column` key set
> must be identical across all three dialects, and the classification must cover it exactly**; and a
> Docker-gated test fails if the parse disagrees with live introspection.

and spec D6 clause 4, `docs/specs/2026-08-22-at-rest-posture.md:184-185`:

> 4. the normalized `table.column` **key set** is not identical across all three dialects, or the
>    classification does not cover it exactly (replaces the vacuous class-divergence pin — see below);

**Independent re-derivation.** The two halves of that sentence quantify over different sets:

```
NORMALIZED wrkflw_* per-dialect=[79, 79, 79]   union=79   intersection=79
NORMALIZED union + casbin = 87
```

- **"identical across all three dialects"** can only be true over the **79** `wrkflw_*` keys.
  `schemas["postgres"]` is **87** columns — the plan asserts exactly that at
  `docs/plans/2026-08-22-at-rest-posture.md:389`, `assert.Len(t, schemas["postgres"].Columns, 87,
  "79 wrkflw_* + 8 casbin_rule (E3, E4)")` — while `schemas["mysql"]` and `schemas["sqlite"]` are
  79. Read literally, the ADR's guard **compares 87 against 79 and can never go green** — the same
  never-green failure mode as the `trigger_` break it was written to fix.
- **"the classification must cover it exactly"** can only be true over the **87** keys; the
  classification map is 87 (`docs/plans/2026-08-22-at-rest-posture.md:484`). Applied to the 79-key
  set instead, the 8 `casbin_rule` entries become *stale*, which D6 clause 2 says **fails**.

The plan is right and the ADR/spec are wrong: `docs/plans/2026-08-22-at-rest-posture.md:581-583`
scopes the identity check explicitly —

```go
	// wrkflw_* only: casbin_rule is postgres-only by construction (E3) and its
	// absence elsewhere is not a divergence.
	pg := atrest.KeysWithPrefix(schemas["postgres"], "wrkflw_")
```

— and puts the coverage check in a **different test** in a **different task** (task 3). The ADR and
spec compress two differently-scoped assertions into one sentence with one "key set" referent.

**Corrected value / fix.** Split the clause in both documents:

> 4. the normalized `table.column` key set **restricted to `wrkflw_*`** (79 keys) is not identical
>    across all three dialects — `casbin_rule` is Postgres-only by construction (E3) and its absence
>    elsewhere is not a divergence; **and** (clause 1+2, separately) the classification does not
>    cover the **87**-key union of every discovered dialect exactly.

---

## K7 — CRITICAL — the revision replaced the invariant and the mutation but left the LIVENESS GUARD pointing at the deleted design, and it resurrects the function the same task says not to resurrect

Task 4 of the plan, `docs/plans/2026-08-22-at-rest-posture.md:551-694`, is four steps that no longer
agree with each other.

**Step 1 (revised)** asserts key-set agreement via `atrest.KeysWithPrefix`.

**Step 2 (NOT revised)**, line 587-589:

> `KeysWithPrefix(s Schema, prefix string) []ColumnKey` … **It must be an exported function taking its
> inputs as parameters**, not a closure over package state — **otherwise step 3 cannot run it over a
> fixture**.

**Step 3 (NOT revised)**, lines 593-628, never calls `KeysWithPrefix`. Its fixture drives a different
function entirely:

```go
	got := atrest.ClassDivergencesPerDialect(schemas, perDialect)
	assert.Len(t, got, 1, "the detector must report the planted divergence")
```

and its fixture is built from

```go
	// A per-dialect classification, which the production one is NOT allowed to be.
	perDialect := func(d string) map[atrest.ColumnKey]atrest.Class {
```

**Step 1's own warning**, lines 564-567, says of that family of functions:

> A lens fuzzed the prescribed signature over 200,000 randomised inputs and got zero non-empty
> results. **Do not resurrect it.**

Three consequences, all concrete:

1. **The liveness guard does not guard the assertion it is attached to.** Nothing in task 4 ever runs
   `KeysWithPrefix` over a fixture, so step 2's entire stated rationale is unfulfilled and the
   key-set matcher ships with no liveness proof — the exact "a detector that reports nothing ever
   cannot pass" gap that `engine/terminal_sites_test.go` (E12) exists to close.
2. **It prescribes new production code with no production caller.** `ClassDivergencesPerDialect` is
   invoked only by its own test, over a `perDialect` classification the same file says the
   production classification "is NOT allowed to be". It cannot fire against the real schema; it is a
   second vacuous guard replacing the first, one indirection further out.
3. **The commit message at step 6** — `git commit -m "test(atrest): pin the dialect-invariance of the
   class, with a liveness guard"` — and the **task heading** at line 551 — *"Task 4: The
   dialect-invariance pin"* — both name the deleted design.

**The spec carries the same stale description**, `docs/specs/2026-08-22-at-rest-posture.md:334-338`:

> 1. a **liveness guard** runs **the same matcher** over an in-test fixture carrying one rogue column
>    (**classified differently in two dialects**) and one innocent one, asserting 1 and 0;
> 2. a **real mutation** against a **migration file on disk** (`cp` backup, restore, `diff`
>    byte-exact) …

Neither survives the revision: (1) "the same matcher" is now a key-set matcher, and "one rogue column
classified differently in two dialects" is the state the same page calls **unrepresentable** in a
flat `map[ColumnKey]Class`; (2) the plan's revised step 5 mutates **`internal/atrest/discover.go`**,
a Go source file, not a migration file — and the same task's worktree warning at line 555-557 still
says *"Step 5 mutates a **migration file** on disk"*.

**Fix.** Re-point steps 2–3 at the surviving matcher: a fixture of two dialect schemas whose
`wrkflw_`-prefixed key sets differ by exactly one planted key and agree on one innocent key, asserting
`KeysWithPrefix` yields sets that differ in exactly the planted key and match elsewhere. Delete
`ClassDivergencesPerDialect` from the plan entirely. Retitle task 4 *"The cross-dialect key-set
invariant"*, fix its commit message, correct the worktree warning to name `discover.go`, and rewrite
spec items 1–2 to describe the key-set matcher and the `discover.go` ablation.

---

## K8 — MAJOR (quantifier, introduced BY the round-1 fix) — "all five are real RED" is false for row 2, in the paragraph that invokes Premise Discipline by name

**Quoted verbatim**, `docs/specs/2026-08-22-at-rest-posture.md:315-319`:

> ⚠⚠ **Round 1 correction.** This paragraph previously read *"Three of the five are real RED; the
> fourth is a pin"* — **wrong in both halves**: four were real RED and the **fifth** was the pin. It
> was wrong in the section that invokes Premise Discipline by name. After D6's clause-4 replacement
> there is **no pin left**: **all five are real RED**, because the key-set invariant fails today
> without D2b.

**Its own table, two lines below, refutes it** (`docs/specs/2026-08-22-at-rest-posture.md:323`):

> | no entry names an absent column | real RED **once the map is deliberately seeded with one bogus
> row during the RED step** |

A failure that exists only after a bogus row is deliberately planted is not a "real RED today" — it
is a pin plus a planted fixture, which is the exact category the same spec elsewhere calls a test
that cannot fail. Re-derived from the plan's own code, `docs/plans/2026-08-22-at-rest-posture.md:474-482`:
in the RED step `Classification` starts **empty**, so the `stale` slice is empty and
`assert.Empty(t, stale, …)` **passes vacuously**. Only the sibling `unclassified` assertion in the
same function is RED. Four of the five rows are real RED; the staleness row is not.

**This is the round-1 fix introducing a new false quantifier while removing the old one** — the named
`## Premise Discipline` failure mode (*"two were introduced by the very edits removing earlier
ones"*), landing for the **third** consecutive time in this one paragraph (`3 of 5` → `4 + pin` →
`all 5`).

**Corrected value / fix.** *"After D6's clause-4 replacement, **four of the five are real RED today**
and the fifth — the staleness assertion — is green until a bogus row is planted, which the plan's RED
step does deliberately. Prefer naming the closed set over counting it: the four that fail on an
unmodified tree are completeness, `SECURITY.md` drift, live-introspection agreement, and normalized
key-set agreement."*

---

## K9 — CONFIRMED (no defect) — every audit-meta count in the banner re-derives exactly, including the two line counts my brief flagged as mutually exclusive

**The two line counts are NOT in conflict; each names its own set.**

> `docs/plans/HANDOVER.md:29` — "as `audit-0187-{execution,failure-modes,counting,interaction,**adjudication**}.md` (**3,839** lines)"
> `docs/adr/…:6` and `audit-0187-adjudication.md:5` — "`audit-0187-{execution,failure-modes,counting,interaction}.md` (**3,612** lines)"

```
$ wc -l audit-0187-*.md
     227 audit-0187-adjudication.md
     740 audit-0187-counting.md
     717 audit-0187-execution.md
    1103 audit-0187-failure-modes.md
    1052 audit-0187-interaction.md
    3839 total
$ cat audit-0187-{execution,failure-modes,counting,interaction}.md | wc -l
    3612
```

**3,839** = all five files (HANDOVER names five). **3,612** = the four lens reports (ADR/adjudication
name four). `3839 − 3612 = 227` = the adjudication. Both correct, both correctly scoped.

**Per-lens split re-derived from each report's own self-count:**

| lens | report's own words | findings | Critical |
|---|---|---|---|
| counting | summary table rows `C1`–`C17`; Criticals `C2, C3, C4, C5` | 17 | 4 |
| execution | *"Four Criticals (X1, X4, X8, X9), five Majors (X2, X5, X6, X7, X10), one Minor (X11)"* (`X3` is a positive control, not a finding) | 10 | 4 |
| interaction | *"19 findings (3 Critical, 6 Major, 8 Medium, 2 Low)"* | 19 | 3 |
| failure-modes | *"Counts: 18 findings — 6 Critical, 10 Major, 2 Minor"* | 18 | 6 |

`17+10+19+18 = 64` ✓ · `4+4+3+6 = 17` ✓ · Majors `10+5+6+10 = 31`, stated as *"~30 Major"* — hedged
and correct.

**"All four lenses found `trigger_`"** — verified per report: counting `C4`, execution `X1`,
interaction `I1`, failure-modes `F11`. The adjudication's A3 heading names exactly those four. ✓

**The controller's own census in the adjudication re-derives exactly against my parser:**

> ```
> postgres: columns=87  keyed=29   actor=1 policy=1 reference=15 scalar=8 timestamp=4
> mysql:    columns=79  keyed=28   actor=1 reference=15 scalar=7 timestamp=5
> sqlite:   columns=79  keyed=28   actor=1 reference=15 scalar=7 timestamp=5
> ```

My MySQL keyed set of 28, classified by hand from the class definitions, splits
`reference 15 / scalar 7 / timestamp 5 / actor 1` — identical. Postgres `29 = 15+8+4+1+1` — identical.

**Also re-derived exact, all inherited from round 1 and correctly restated:**

```
postgres: total=79 textish=48  hist={TEXT:36, TIMESTAMPTZ:19, JSONB:12, INT:6, SMALLINT:3, BIGINT:2, BIGSERIAL:1}
mysql:    total=79 textish=48
sqlite:   total=79 textish=67
```
(ADR decision 2's histogram `36+12+19+6+3+2+1 = 79`, and E5's `48/48/67`, both exact)

```
$ grep -rn "Authorize(ctx" --include='*.go' . | grep -v _test | grep -v "func "
runtime/task/service.go:199 / 234 / 255 / 306   <- four, all task.Eligibility
```
(ADR decision 5's "**four** `Authorize` sites … (199, 234, 255, 306)" — exact)

Column census 79/79/79 + 8 = 87; raw cross-dialect union 88; normalized union = intersection =
per-dialect = 79, all three sets **identical**, `trigger_` the **only** divergence:

```
mysql - postgres: [('wrkflw_journal', 'trigger_')]
postgres - mysql: [('wrkflw_journal', 'trigger')]
postgres - sqlite: []   sqlite - postgres: []
NORMALIZED per-dialect=[79, 79, 79]  union=79  intersection=79   identical=True (all 3 pairs)
RAW union + casbin(8) = 88   NORMALIZED union + casbin = 87
```

**No defect.** The revision's headline arithmetic is sound. The defects are in the *prose around* the
numbers (K1, K2), the *scope* of the sentences that use them (K6), and the *quantifiers* (K8, K13).

---

## K10 — CRITICAL — the adjudication ordered the pairwise grid recomputed; it was not, and the interaction section still says "Ten decisions" and still describes D1 as a glob

**The order**, `docs/plans/sweep-evidence/audit-0187-adjudication.md:198-201` (A12):

> **FIX:** number the non-goal as D10 in the spec; **the pair count becomes 45 + the two new decisions
> from A5/A6 → recompute.**

**What shipped**, `docs/specs/2026-08-22-at-rest-posture.md:395`, unchanged from `ebafdf0f`:

> **Ten decisions ship together.** Below is what each does to the others' premises, derived pairwise.

and its first pair heading, line 399:

> **D1 (discover by glob) × D6 (fail on an unclassified column).**

**Independent re-derivation of the decision list** (`git show ebafdf0f:… | grep -nE '^\*\*[0-9]+[a-z]?\.'`
vs the same over HEAD):

```
BEFORE (ebafdf0f): decisions 1,2,3,4,5,6,7,8,9,10                    = 10  -> C(10,2) = 45 pairs
AFTER  (3d8a20ea): decisions 1,2,2b,3,4,5,6,7,8,9,10,11,12           = 13  -> C(13,2) = 78 pairs
```

The section's stated scope (**10** decisions, **45** pairs) is short by **3 decisions and 33 pairs**.
It contains no pair involving `D2b` (normalization), `D11` (fail-closed parsing) or `D12`
(determinism) — the three decisions the revision introduced and therefore the only ones whose
interactions have never been derived by anyone.

**And the section is now self-refuting.** Its own round-1 banner, three lines above, says:

> ⚠ It also claimed "ten decisions … 45 pairs" while writing 8, and while this document defined only
> nine. **Do not read the section below as coverage; read it as a sample that missed its own subject.**

The revision wrote a warning that the "ten decisions" claim was wrong and left the claim standing.

**Severity rationale.** CLAUDE.md rule #9's interaction clause is explicit that fixing N decisions and
re-auditing is not the same as auditing N fixed decisions, and the adjudication's own closing note
(lines 215-227) says a round-2 interaction pass is **owed** and "the pairwise grid must be re-derived
against the **new** decision list, including the survivor×new pairs". The grid the round-2 lens
inherits is the round-1 grid, scoped to a decision list that no longer exists, describing D1 by the
glob that was deleted. Two of the three unexamined new decisions already show a live interaction: see
K7 (D2b's key-set invariant × the D2 class-divergence liveness guard) and K14 (D12 × D9's
`gen-at-rest.sh`).

**Fix.** Rewrite the section header to *"Thirteen decisions ship together (D1, D2, D2b, D3–D12) — 78
pairs"*, restate D1 as the **rule**, and hand the round-2 interaction lens the explicit
survivor×{D2b, D11, D12} grid (36 pairs) as its starting point rather than the stale 45.

---

## K11 — MAJOR — 30 of the 64 round-1 findings are never mentioned in the adjudication, and at least one of the silent ones is a live, unfixed defect

**The claim**, `docs/adr/0187-at-rest-classification-is-machine-checked.md:3` and
`docs/specs/2026-08-22-at-rest-posture.md:3-6`:

> ## ▶ **AUDIT ROUND 1 FOLDED** 2026-08-22 — **64 findings, 17 Critical**, four Opus lenses
> … six decisions changed and two were added, and **the corrections below are summarised there in
> full**.

**Independent re-derivation** — every finding ID the adjudication references:

```
$ grep -oE "\b(C|X|I|F)[0-9]{1,2}\b" audit-0187-adjudication.md | sort -u
C11 C12 C16 C17 C2 C3 C4 C5   (8 of 17 counting)
X1 X10 X2 X4 X5 X6 X7 X8 X9   (9 of 10 execution)
I1 I13 I2 I4 I5 I6 I7 I8      (8 of 19 interaction)
F1 F11 F12 F13 F2 F4 F5 F6 F8 (9 of 18 failure-modes)
                              = 34 distinct IDs of 64
```

**30 findings are neither accepted, rejected, deferred, nor mentioned.** The adjudication's `REJECTED`
section holds exactly one entry (A10). CLAUDE.md's Delivery Gate is explicit: *"Findings you
adjudicate as false-positive or out-of-scope must be stated explicitly with the reason — **silence is
not an adjudication**."*

**Proof that the silence hides live defects — round-1 counting finding `C1`** (the spec's
evidence-range citation), a MAJOR, appears nowhere in the adjudication and is **still unfixed at HEAD**:

`docs/specs/2026-08-22-at-rest-posture.md:9`:

> **Evidence:** `docs/specs/2026-08-22-adr-0187-measurements.md` (**E1–E13**, every claim below executed)

```
$ grep -c "^## E" docs/specs/2026-08-22-adr-0187-measurements.md
15
$ grep -n "^## E" … | tail -3
268:## E13 …
313:## E14 — goose directives: only `Up` and `Down` appear
330:## E15 — There is exactly ONE foreign key, declared THREE different ways
$ grep -on "E1[45]" docs/specs/2026-08-22-at-rest-posture.md
354:E14      368:E15      376:E15
```

The evidence file holds **E1–E15**, and the spec cites **E14 and E15 in its own body** while its
header advertises E1–E13. Corrected value: **E1–E15**. This is the identical *"an inherited range that
was correct when written was not re-derived"* defect the delivery exists to prevent, surviving the
audit that named it.

**Fix.** (a) Correct the range to `E1–E15`. (b) Add a dispositions table to the adjudication covering
all 64 IDs — one line each, `folded / already-covered-by-Ax / rejected-because / deferred-to-round-2`
— so "FOLDED" is a checkable claim rather than a summary of the 34 that were convenient.

---

## K12 — MAJOR — "six decisions changed and two were added" undercounts the ADDITIONS; three were added, and the uncounted one is the decision the others depend on

**The claim**, restated verbatim in **five** places — the ADR banner (line 8), the spec header (line
6), `HANDOVER.md:31`, the adjudication's closing note (line 217, *"This revision changes SIX
decisions and adds TWO"*), and the commit message of `3d8a20ea`.

**Independent re-derivation against the real diff** (`ebafdf0f` → `3d8a20ea`, both reachable):

```
$ git show ebafdf0f:docs/adr/0187-…md | grep -nE "^\*\*[0-9]+[a-z]?\."
  -> 1,2,3,4,5,6,7,8,9,10                       (10 decisions)
$ grep -nE "^\*\*[0-9]+[a-z]?\." docs/adr/0187-…md
  -> 1,2,2b,3,4,5,6,7,8,9,10,11,12              (13 decisions)
```

**Added: THREE — `D2b`, `D11`, `D12`** — not two. The diff shows `2b` as pure insertion:

```
> **2b. Reserved-word column aliases are NORMALIZED to the canonical name before any set operation.**
```

**Substantively changed: THREE — `D1` (glob → stated rule), `D6` (clause 4: class divergence →
key-set disagreement), `D8` (safety claim withdrawn, 27-of-79 → 29-of-87 per dialect)**, plus `D7`
which gained a *"Round 1 residual, stated not fixed"* annotation without its decision changing. The
diffs to `D2`, `D3`, `D4` and `D5` are prose trims and cross-references, not decision changes. So the
"six" is only reachable by counting cosmetic edits, and no document names which six.

**Why the undercount matters concretely, and it is not bookkeeping.** This sentence is the **scope
statement for the mandatory round-2 interaction pass** — it is what tells that lens which decisions to
take pairwise. `D2b` is missing from it, and `D2b` is the decision the adjudication's own closing note
says the others hang off:

> ⚠ Recorded for that round: **A1's fix … directly interacts with A3's fix** (name normalization) …
> **A4's fix depends on A3's**: the key-set invariant is only true after normalization, so ordering
> them wrongly makes the new strongest test fail for the wrong reason.

`A3` **is** `D2b`. The note names it as the hub of two interactions and the headline count leaves it
out of both buckets. K7 is a live consequence: the guard replaced under `D6` was never re-pointed at
`D2b`'s matcher.

**Fix.** Replace the count with the named set, in all five places (CLAUDE.md rule #13 — prefer naming
a closed set over counting it):

> **Three decisions changed — D1 (discovery rule), D6 (clause 4), D8 (the withdrawn safety claim) —
> D7 gained a stated residual, and three were added: D2b (reserved-word normalization), D11
> (fail-closed parsing), D12 (deterministic rendering).**

---

## K13 — MAJOR — D12's "995 of 1000" is one stochastic sample restated as a constant, and the "could never" it justifies is FALSE: the undetermined generator goes green about 1 run in 87

**Quoted verbatim**, ADR decision 12,
`docs/adr/0187-at-rest-classification-is-machine-checked.md:194-199`:

> ⚠ **Added in round 1.** Both generator inputs are Go maps and Go randomises map iteration; a lens
> measured **995 of 1000** renders of the same 87-entry input differing. Because
> `scripts/gen-at-rest.sh` writes and then re-asserts in a *second process*, the undetermined version
> **could never have printed its success line**, and the plan's `diff`-based idempotence check **could
> never have passed**.

Restated in `docs/specs/2026-08-22-at-rest-posture.md:65-68` — *"which would have made
`scripts/gen-at-rest.sh` **unable ever** to print its success line"* — and in
`docs/plans/2026-08-22-at-rest-posture.md:854-858` — *"without this it can **never** print its success
line and Task 8's `diff`-based idempotence check can **never** pass."*

**Provenance: inherited, not measured by the author.** The figure exists once,
`audit-0187-failure-modes.md:347`, and is restated as bare fact in the adjudication, the ADR, the spec
and the plan. Per Premise Discipline: *"Re-verify claims you inherit before restating them. Restating
strips the hedge."*

**Independent re-derivation** — a Go program building an 87-entry `map[key]string` with this schema's
real table/column shape and rendering it unsorted 1000 times, six consecutive runs:

```
map entries: 87
1000 renders of the same 87-entry map: differed from first = 989, identical to first = 11
distinct render orders observed = 87
... = 992, identical =  8   distinct orders = 87
... = 978, identical = 22   distinct orders = 87
... = 995, identical =  5   distinct orders = 87
... = 949, identical = 51   distinct orders = 87
... = 990, identical = 10   distinct orders = 87
```

**Two corrections.**

1. **"995 of 1000" is a sample, not a measurement of a constant.** Observed range over six runs:
   **949–995**. It reproduces (I hit 995 exactly once) but re-running gives a different number every
   time, and the documents present it as a fixed fact in three places. The **stable** figure — invariant
   across all six runs — is **87 distinct render orders**, which is the real mechanism: Go randomises
   the start bucket and the intra-bucket offset, not the full permutation, so the order space is ~n,
   not n!.

2. **"could never have printed its success line" / "unable ever" is FALSE.** With 87 distinct orders,
   two independent renders agree with probability ≈ **1/87 ≈ 1.1 %**. `scripts/gen-at-rest.sh`'s
   write-then-re-assert-in-a-second-process would therefore have **succeeded roughly one run in
   87**, not never. That is a strictly worse failure mode than the one documented: not a deterministic
   red that stops the delivery, but a **~1 % flaky green** — a lucky CI run ships a nondeterministic
   generator, and every subsequent `-update` rewrites `SECURITY.md` into a different row order,
   producing spurious diffs on unrelated PRs.

**Corrected value / fix.** Replace the sentence in all three documents with the stable mechanism and
the honest probability:

> Both generator inputs are Go maps; Go randomises the iteration start, giving **87 distinct render
> orders** for an 87-entry map (measured: 949–995 of 1000 renders differ from the first, six runs).
> `scripts/gen-at-rest.sh` writes and re-asserts in a second process, so an undetermined renderer
> would pass roughly **1 run in 87** — a ~1 % flaky green, not a reliable red. D12 therefore also
> earns a determinism test that renders twice **in one process** and asserts byte equality, which
> fails deterministically rather than 99 % of the time.

⚠ The plan's Task 6 already prescribes *"Add a test that renders twice and asserts equality"*
(line 858), which is the right test — but the plan states its falsifiability as "can never pass",
inheriting the same false quantifier. That test fails ~99 % of the time against an undetermined
renderer, not 100 %: state it that way, or render **three** times to drive the escape probability to
~0.01 %.

---

## K14 — MINOR — the `200,000`-input fuzz figure is likewise inherited and restated in four documents; the conclusion is sound and the number is unfalsifiable-as-stated

**Quoted verbatim**, ADR decision 6, `docs/adr/0187-at-rest-classification-is-machine-checked.md:136-141`:

> a lens fuzzed the prescribed signature over **200,000** randomised inputs and got **zero** non-empty
> results.

Restated at `docs/adr/…:24` (banner), `docs/specs/2026-08-22-at-rest-posture.md:194-196`, and
`docs/plans/2026-08-22-at-rest-posture.md:566-568`.

**Provenance:** one origin, `audit-0187-failure-modes.md:29-34`
(`trials=200000  trials where ClassDivergences returned NON-EMPTY: 0`), restated four times.

**Not re-derived here** — the fuzz targets `ClassDivergences`, which does not exist in the repo; its
signature exists only inside the plan. **The conclusion is sound by construction and needs no fuzz at
all**: `ClassDivergences(schemas map[string]Schema, cls map[ColumnKey]Class)` takes **one** flat
class map with no dialect term, so "this column's class differs between dialects" has no
representation in the input type. That is a **type-level** proof; 200,000 trials is weaker evidence
for it than one sentence about the signature, and it invites the reader to think the bound is
empirical (i.e. that a 200,001st input might differ).

**Fix.** Keep the number as colour if desired, but lead with the type argument: *"the parameter is a
single `map[ColumnKey]Class` with no dialect term, so a per-dialect divergence is **unrepresentable in
the input** — the guard cannot fire for any input, which a 200,000-trial fuzz confirmed empirically."*

---

## K15 — MAJOR — the plan's banner names the wrong SET of changed tasks: it lists Task 3 (unchanged) and omits Task 8 (changed)

**Quoted verbatim**, `docs/plans/2026-08-22-at-rest-posture.md:8`:

> 64 findings, 17 Critical. **Tasks 2, 3, 4, 5, 6 and 7 all changed.**

**Independent re-derivation** (md5 of each `## Task N:` block, `ebafdf0f` vs `3d8a20ea`):

```
Task 1: UNCHANGED
Task 2: CHANGED
Task 3: UNCHANGED     <- named in the banner
Task 4: CHANGED
Task 5: CHANGED
Task 6: CHANGED
Task 7: CHANGED
Task 8: CHANGED       <- omitted from the banner
```

**Corrected value: Tasks 2, 4, 5, 6, 7 and 8.** The *count* (six) is right and the *membership* is
wrong in both directions — the shape the `## Premise Discipline` section calls out: *"Prefer naming a
closed set over counting it."* A worker resuming from the banner re-reads an unchanged Task 3 and
skips a changed Task 8, which is the delivery-gate task (script, docs sweep, verification commands).

**Fix.** `**Tasks 2, 4, 5, 6, 7 and 8 all changed; Tasks 1 and 3 did not.**`

---

## K16 — MAJOR — the plan's "Type consistency" list is stale in three directions and contradicts its own next paragraph

**Quoted verbatim**, `docs/plans/2026-08-22-at-rest-posture.md:1094-1097`:

> **Type consistency:** `ColumnKey`, `Column`, `Schema`, `Class`, `Classification`, `LoadSchemas`,
> `ModuleRoot`, **`ClassDivergences`**, **`ClassDivergencesPerDialect`**, `Render`, `ReplaceBlock`,
> `MigrationSets`, `MigrationSet` — **each defined in exactly one task's Produces block and spelled
> identically at every later use.**

and the paragraph immediately below it, lines 1099-1105:

> Round 1 **deleted both** and replaced them with **`KeysWithPrefix`** plus a key-set assertion that
> is RED today.

**Independent re-derivation** — every mention of these three symbols at HEAD:

```
$ grep -n "ClassDivergences\|KeysWithPrefix" docs/plans/2026-08-22-at-rest-posture.md
565: ⚠⚠ ROUND 1 REPLACED THIS TASK'S ASSERTION. It previously called `ClassDivergences(schemas, cls)`
583:     pg := atrest.KeysWithPrefix(schemas["postgres"], "wrkflw_")
584:     my := atrest.KeysWithPrefix(schemas["mysql"], "wrkflw_")
585:     sq := atrest.KeysWithPrefix(schemas["sqlite"], "wrkflw_")
600: `KeysWithPrefix(s Schema, prefix string) []ColumnKey` returns the sorted keys …
637:     got := atrest.ClassDivergencesPerDialect(schemas, perDialect)
653: Step 3 is a genuine RED: `ClassDivergencesPerDialect` does not exist yet.
1096: … `ClassDivergences`, `ClassDivergencesPerDialect`, …
1101–1104: … `ClassDivergences` **could not fail at all** … Round 1 deleted **both** …
```

Three defects in a four-line claim:

1. **`ClassDivergences` is listed but defined in NO task's `Produces` block** — it survives only in
   prose describing its own deletion. The list's stated invariant ("each defined in exactly one
   task's Produces block") is false for it.
2. **`KeysWithPrefix` is missing from the list** although Task 4 steps 1–2 both define and use it.
   It is the one symbol the revision actually introduced.
3. **"Round 1 deleted both" is FALSE.** Task 4 step 3 still *prescribes implementing*
   `ClassDivergencesPerDialect` (line 637), and line 653 states its RED as a delivery requirement.
   This is the same defect as **K7**, reached from the opposite direction — the summary says the
   function is gone, the task says build it.

**Fix.** Drop `ClassDivergences` and `ClassDivergencesPerDialect` from the list, add `KeysWithPrefix`,
and delete `ClassDivergencesPerDialect` from Task 4 step 3 per K7. Then the sentence
*"Round 1 deleted both"* becomes true.

---

## K17 — MEDIUM — two more stale "glob" / "class divergence" labels survive in the plan's own summary surfaces

Both are the deleted design named as current, in the two places a reader looks first and last.

**Site 1 — the plan's Architecture paragraph**, `docs/plans/2026-08-22-at-rest-posture.md:22-25`:

> Guards fail on an unclassified column, a stale classification entry, a `SECURITY.md` that disagrees
> with the generator, **a class that differs across dialects**, and — Docker-gated — a parse that
> disagrees with live database introspection.

ADR decision 6 and spec D6 clause 4 both replaced "a class that differs across dialects" with the
key-set invariant (see K6); the plan's own Task 4 step 1 asserts the key set. Correct text: *"…a
normalized `wrkflw_*` key set that is not identical across the three dialects…"*.

**Site 2 — the plan's Self-review table**, `docs/plans/2026-08-22-at-rest-posture.md:1075`:

> | **D1 discovery by glob**, never a listed directory | 2 (plus the two-way declaration check) |

D1 is now *"every directory named `migrations`, and every direct child directory of one, that contains
at least one `.sql` file"* — the glob was the round-1 Critical (`A2`). Correct label: *"D1 discovery by
a stated rule, never a listed directory"*.

Same class as spec line 399's `**D1 (discover by glob) × D6**` (reported under K10).

---

# SUMMARY

Round 2, COUNTING lens. **17 findings — 4 Critical (K1, K2, K7, K10), 7 Major (K6, K8, K11, K12, K13, K15, K16), 2 Medium (K4, K17), 2 Minor (K5, K14), 2 confirmed-no-defect (K3, K9).**

⚠ That severity split was itself miscounted in the first draft of this line (as "3 Critical, 8 Major, 3 Medium, 1 Minor") and corrected by recounting the finding headings. Recorded because it is this lens's own rule catching this lens: a summary sentence appended to correct detail, over-generalising what it compressed.

⚠ **Headline: the revision's ARITHMETIC is sound; every load-bearing number re-derives exactly
(K9).** Every defect below is in the **prose, scope or quantifier wrapped around a correct number** —
which is the failure mode this lens was briefed on, and it landed 15 times.

| ID | severity | claimed | actual | verdict |
|---|---|---|---|---|
| **K1** | **CRITICAL** | plan Task 5's falsifiability prose: the `byClass` census is "**27/15/7/4/1**", the assertion is "the bare **27**", and the map "has exactly **four** entries and no `freeform`/`policy` keys" | the code 50 lines above asserts **29** and a **five**-entry map including `ClassPolicy: 1`; the prose is the round-1 **withdrawn** census verbatim | **CONFIRMED** — an implementer following the falsifiability statement writes the 4-entry map and the documented way to green it is to restore the `casbin_rule` skip |
| **K2** | **CRITICAL** | the withdrawn sentence *"every `freeform` and `policy` column is index-free and can be encrypted without breaking an index"* survives in ADR Consequences (l.214), spec "What SECURITY.md gains" (l.299-302), plan Task 6 step 3 item 3 (l.865), and E9 (l.204) | `casbin_rule.ptype` is class `policy` and carries `casbin_rule_ptype_idx`; **`policy`-keyed = 1**. Plan item 3 tells the **generator to publish it to consumers** | **CONFIRMED FALSE ×4** — E9 still asserts it over "both policy locations" while measured over the 79 that exclude one of them: the round-1 scope error, live |
| **K7** | **CRITICAL** | plan Task 4 steps 2–3: `KeysWithPrefix` is exported "otherwise **step 3 cannot run it over a fixture**"; step 3 is the liveness guard | step 3 never calls `KeysWithPrefix` — it drives `ClassDivergencesPerDialect`, which step 1 says **"Do not resurrect"**, over a per-dialect map production "is NOT allowed to be". Task title, commit message, worktree warning and spec items 1–2 all still describe the deleted design | **CONFIRMED** — the key-set matcher ships with **no liveness proof**, and a second unfireable guard is prescribed |
| **K10** | **CRITICAL** | spec l.395 *"**Ten** decisions ship together"*, pairwise; adjudication A12 ordered "the pair count becomes 45 + the two new → **recompute**" | 13 decisions at HEAD (D1, D2, **D2b**, D3–D12) ⇒ **78** pairs. No pair involves D2b, D11 or D12 — the only three whose interactions nobody has derived. D1 still labelled "discover by **glob**" | **NOT DONE** — the round-2 interaction lens inherits a grid scoped to a decision list that no longer exists |
| **K6** | MAJOR | ADR d.6 / spec D6.4: "**the normalized `table.column` key set** must be identical across all three dialects, **and** the classification must cover it exactly" | "identical" is only true over **79** (`wrkflw_*`); `schemas["postgres"]` is **87**. "cover exactly" is only true over **87**; over 79 the 8 casbin entries are *stale*, which clause 2 fails. Plan scopes it right (`KeysWithPrefix(…, "wrkflw_")`), ADR/spec do not | **CONFIRMED anchor shift** — read literally the guard compares 87 to 79 and can never go green |
| **K8** | MAJOR | spec l.319 *"there is **no pin left**: **all five** are real RED"* | its own table row 2 says *"real RED **once the map is deliberately seeded with one bogus row**"*; in the RED step `Classification` is empty so `assert.Empty(stale)` passes vacuously. **Four** are real RED | **CONFIRMED** — third consecutive wrong quantifier in that paragraph (3-of-5 → 4+pin → all-5), introduced **by** the round-1 fix, in the section that names Premise Discipline |
| **K11** | MAJOR | ADR banner "**AUDIT ROUND 1 FOLDED** — 64 findings"; spec "summarised there **in full**" | the adjudication references **34** of 64 IDs; **30 are never mentioned**, and `REJECTED` holds one entry. Silent finding `C1` is **still live**: spec header says evidence is "**E1–E13**" while the file holds **E1–E15** and the spec body cites E14 and E15 | **CONFIRMED** — "silence is not an adjudication" (CLAUDE.md Delivery Gate) |
| **K12** | MAJOR | "six decisions changed and **two** were added" — in ADR banner, spec header, HANDOVER, adjudication note and the commit message | **three** added (**D2b**, D11, D12) and **three** substantively changed (D1, D6, D8; D7 gained a residual). The uncounted addition, **D2b**, is the one the adjudication's own note calls the hub — *"A4's fix depends on A3's"* | **CONFIRMED undercount** — and it is the scope statement for the mandatory round-2 interaction pass |
| **K13** | MAJOR | D12: "a lens measured **995 of 1000** renders differing … the undetermined version **could never** have printed its success line" (ADR, spec, plan) | six runs: **949–995** of 1000 — a stochastic sample, not a constant. The stable figure is **87 distinct render orders** ⇒ two renders agree ≈ **1/87 ≈ 1.1 %**, so `gen-at-rest.sh` would have gone green ~**1 run in 87**, not never | **INHERITED + FALSE QUANTIFIER** — the real hazard is a ~1 % **flaky green**, strictly worse than the documented deterministic red |
| **K4** | MEDIUM | ADR d.11 / spec: "Verified free: `grep -ric \"alter table\"` … returns **0** everywhere, so failing closed **costs nothing today**" | the grep result is true, but it tests **one** statement form while the conclusion quantifies over **all** unrecognised forms | **CONCLUSION HOLDS, EVIDENCE TOO NARROW** — re-derived properly: the Up halves contain only `CREATE TABLE` (9/9/9/1) and `CREATE INDEX` (11/8/11/1), **0 unrecognised** |
| **K15** | MAJOR | plan banner: "**Tasks 2, 3, 4, 5, 6 and 7** all changed" | md5 per task block, `ebafdf0f` vs HEAD: **2, 4, 5, 6, 7, 8**. Task 3 is **unchanged**; Task 8 (the delivery gate) **is** changed | **CONFIRMED** — count right, membership wrong both ways |
| **K16** | MAJOR | plan: type-consistency list holds `ClassDivergences` + `ClassDivergencesPerDialect`, "each defined in exactly one task's Produces block"; next paragraph: "Round 1 **deleted both**" | `ClassDivergences` is in no Produces block; **`KeysWithPrefix` is missing from the list**; and `ClassDivergencesPerDialect` is **still prescribed** at Task 4 step 3 l.637/653 | **CONFIRMED ×3** — same defect as K7 from the opposite end |
| **K17** | MEDIUM | plan Architecture l.22-25 "guards fail on … **a class that differs across dialects**"; self-review l.1075 "**D1 discovery by glob**" | both name the deleted design; D6.4 is the key-set invariant, D1 is a stated rule | **CONFIRMED stale labels** |
| **K14** | MINOR | "fuzzed over **200,000** randomised inputs: **zero** non-empty" — restated in 4 documents | inherited from one lens report; not re-derived (the function does not exist). The conclusion is **type-level**, not empirical: a single flat `map[ColumnKey]Class` makes divergence unrepresentable | **CONCLUSION SOUND, EVIDENCE MISFRAMED** — a fuzz invites the reader to think the bound is empirical |
| **K5** | MINOR | — (not a bundle claim) | MySQL's `-- +goose Down` drops **8** tables; Postgres and SQLite drop **9**. `wrkflw_outbox` is created and never dropped on MySQL | **REPO DEFECT** surfaced while re-deriving; invisible to every guard this bundle prescribes |
| **K3** | ✅ none | ADR d.1's rule matches four migration directories | `os.walk` repo-wide: exactly **4** discovered, `store/migrations` correctly skipped (no `.sql`), no `vendor/`, neither `testdata` dir matches | **CONFIRMED CORRECT** |
| **K9** | ✅ none | 64 findings / 17 Critical; per-lens 17-4 / 10-4 / 19-3 / 18-6; 3,839 vs 3,612 lines; "all four lenses found `trigger_`"; keyed 29-of-87 / 28-of-79 / 28-of-79 and 27/28/28; byClass 15/8/4/1/1; 87 = 79+8; union 88 raw / 87 normalized; type histogram; 48/48/67; four `Authorize` sites | **every one re-derives exactly** with an independent parser. The two line counts each name their own file set and differ by exactly the 227-line adjudication | **CONFIRMED CORRECT** |

## What this lens did NOT verify

- **`UNVERIFIED (needs Docker)`** — Task 7's live-introspection cross-check assertions. No container
  was started. Everything above was derived from SQL text, Go source, `git`, and one pure-Go Go
  program; the SQLite path needs no daemon.
- The **per-column** class assignment of all 87 columns could not be fully re-derived: the bundle
  publishes the six class **counts** and a per-table prose list, not a machine-readable 87-row table.
  I verified the counts are mutually consistent and that the keyed `byClass` split (15/8/4/1/1)
  forces `casbin_rule.id` ⇒ `scalar` and `wrkflw_chain_links.outcome` ⇒ `reference`; the remaining
  assignments are stated judgements, which D4 says is intentional.

## The one-line lesson

**Seven of the seventeen findings (K1, K2, K7, K10, K15, K16, K17) are the same defect: the revision
corrected a NUMBER and left the SENTENCE that describes it.** In every case the corrected value sits
within 60 lines of the stale description, and in three of them (K1, K7, K16) the stale description is
the *falsifiability statement* or the *type-consistency claim* — i.e. the text an implementer is
instructed to trust over the code. A revision pass that greps for the **old** value after fixing the
new one would have caught all seven.
