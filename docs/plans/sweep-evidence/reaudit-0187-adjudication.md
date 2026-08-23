# ADR-0187 §AT-REST — audit ROUND 2 (scoped), adjudication

**Date:** 2026-08-22 · **Bundle commit audited:** `3d8a20ea` · **Adjudicator:** controller

Scope chosen by the owner: **two lenses, not four** — interaction (mandatory per rule #9, since the
revision changed six decisions and added three) and counting (re-deriving the numbers the revision
itself introduced, which nobody had audited). Execution and failure-modes were skipped: round 1
largely exhausted them on a design with no code.

**34 findings — 11 Critical** (interaction 17/7, counting 17/4).
Reports: `reaudit-0187-interaction.md` (665 lines), `reaudit-0187-counting.md` (927 lines).

## ⭐⭐⭐ The finding above the findings — both lenses reached it independently

**The round-1 revision corrected each decision WHERE IT IS DEFINED and left every place it is
CONSUMED.** Seven of counting's seventeen and five of interaction's seven Criticals are one defect:
*a number or decision was fixed, and the sentence describing it was not* — **always within 60 lines**,
and three times **in the exact text an implementer is told to trust over the code**.

Concretely, the round-1 headline Critical — a **false published safety claim** — was still live in
**four** places after being "withdrawn", including:

- `docs/plans/…-at-rest-posture.md` Task 6 step 3, as a **code instruction to the generator**, under
  a note saying these sentences are emitted by code rather than typed. **It would have shipped
  hard-coded into `render.go` and published to consumers.**
- `docs/adr/0187-…md` Consequences, restated as a **Good consequence 58 lines below its own
  withdrawal**, contradicting the ADR's own audit banner.
- the spec's "What `SECURITY.md` gains", still calling it "the actionable content".
- `…-measurements.md` E9, still asserting it over "both policy locations" while measured over the 79
  columns that exclude one of them — the round-1 scope error, still live in the evidence file.

⇒ **Had round 2 not run, the round-1 fix would have been cosmetic.** This is the concrete answer to
"is a second round worth it after the first one found the bug".

⭐ **The counting lens also supplied the mechanical remedy, now adopted as standing practice:**
**after writing a corrected value, `grep` for the OLD one.** It catches all seven.

## Accepted — Critical

- **B1 (interaction J1/J2/J5, counting K2)** — the withdrawn safety sentence survives in four places.
  **FIXED** at every site; the generator is now explicitly instructed **not** to emit it, and E9 in
  the evidence file carries the corrected per-dialect census.
- **B2 (J4, K6)** — **the round-1 key-set invariant had NO TRUE READING.** "Identical across all
  three dialects **and** the classification covers it exactly": `casbin_rule` is Postgres-only, so
  unscoped the sets are 87/79/79 and identity fails forever; scoped to `wrkflw_*` it is 79 against an
  87-key classification and coverage fails. Only the plan split it, silently. **FIXED:** ADR and spec
  now carry **6a (identity, scoped to `wrkflw_*`, 79)** and **6b (coverage, scoped to the union, 87)**
  as separate clauses, with a note that the scopes differ on purpose.
- **B3 (J8)** — **D2b × D7: the Docker cross-check could never go green.** D2b normalizes `trigger_`
  → `trigger` in the parse; live MySQL introspection genuinely returns `trigger_`. The one test that
  proves the parser honest was guaranteed a permanent one-column diff. **FIXED:** normalize **both**
  sides, per `migration_parity_test.go:71`.
- **B4 (J3, K7)** — the deleted `ClassDivergences` / `ClassDivergencesPerDialect` were still
  prescribed in Task 4's liveness guard, in a **RED step for a symbol that no longer exists**, and in
  the type roster — in the same file whose closing paragraph says round 1 deleted them. **FIXED:** the
  liveness guard now drives `KeysWithPrefix`, the matcher the production assertion actually uses.
- **B5 (J6, K1)** — Task 5's falsifiability prose still stated the withdrawn `27/15/7/4/1` census and
  "exactly four entries and no `freeform`/`policy` keys", 50 lines under code asserting **29** and a
  **five**-entry map. ⚠⚠ **The documented route to making the stale version pass is restoring the
  `casbin_rule` skip that caused the round-1 Critical.** **FIXED.**
- **B6 (K10)** — the pairwise recount the round-1 adjudication **ordered** was never done. There are
  **13 decisions ⇒ 78 pairs**; the spec still said "ten decisions", still described D1 as "discover by
  glob", and contained **no pair involving D2b, D11 or D12**. **FIXED** as far as honesty allows: the
  section is now banner-marked stale, the counts corrected, and the three underived decisions named
  as where a round 3 would start. ⚠ **Their interactions remain underived — this is an open residual,
  not a closed finding.**

## Accepted — Major

- **B7 (K12)** — "six changed and **two** added" undercounted: **three** (D2b, D11, D12), and the
  omitted one is the hub the others depend on. It was also the scope statement handed to this very
  round. **FIXED** in five places.
- **B8 (K13)** — D12's "995 of 1000" was **one stochastic sample inherited from a round-1 lens and
  restated as a constant in three documents**. Re-measured: **949–995**, stable form **87 distinct
  render orders** ⇒ an undetermined generator goes green ~**1 run in 87**. ⚠ **The real hazard is a
  ~1 % flaky green, which is strictly worse than the documented deterministic red.** **FIXED** — the
  decision stands, its justification is corrected.
- **B9 (K8)** — "there is no pin left: **all five** are real RED" is refuted by its own table two rows
  below. **Third consecutive wrong quantifier in that paragraph, the second introduced by the fix to
  the first**, in the section that names Premise Discipline. **FIXED:** four real RED, one needs a
  planted row.
- **B10 (J11)** — the normalization must be an **exact `(table, column)` match**, never a prefix rule:
  `wrkflw_timers.trigger_kind` and `trigger_payload` would both be mangled by a
  `HasPrefix("trigger_")` rule, and **no prescribed test would catch it** because the counts stay 79.
  **FIXED** as an explicit ⚠⚠ in ADR decision 2b.
- **B11 (J13)** — `wrkflw_outbox.dedup_key` is keyed **solely** by an inline `UNIQUE` in all three
  dialects, and Task 1's trap list omitted that shape — the one parser shape carrying the counts.
  **FIXED:** added as a trap test case.
- **B12 (K11)** — the round-1 adjudication referenced 34 of 64 findings; **30 were never mentioned**,
  and round-1 finding C1 among them was still unfixed (spec header said "E1–E13" for a file holding
  E1–E15). **FIXED.** ⚠ **Standing lesson: an adjudication that silently drops findings is
  indistinguishable from one that rejected them.**
- **B13 (J-/A13, A15, A19)** — three round-1 accepted fixes reached only one document or none:
  the `—`/`n/a` rendering distinction, the goose-no-checksum caveat, and the uncross-checked-table
  test. **ALL THREE FIXED**, the last as a new Task 7 step with real code.
- **B14 (K15, K16)** — the plan banner named the wrong set of changed tasks and the type roster kept
  two deleted symbols while omitting their replacement. **FIXED.**

## Confirmed correct — no action

- **K9 / interaction's independent re-derivation** — **every load-bearing number the revision
  introduced reproduces exactly** under two independently-written parsers: 87/79/79 columns,
  29/28/28 keyed, `policy`-keyed = 1, byClass 15/8/4/1/1, the six class counts 27/19/17/11/8/5, raw
  union 88 / normalized 87, post-normalization key-set agreement with `trigger_` the only divergence,
  the type histogram, 48/48/67, four `Authorize` sites, and the audit-meta split 17/10/19/18 and
  4/4/3/6. **The arithmetic was never the problem; the prose around it always was.**
- **K3** — D1's new discovery rule matches exactly **4** directories repo-wide, nothing unintended.
- **K4** — D11 (fail-closed parsing) is genuinely free: the `Up` halves contain **only**
  `CREATE TABLE` and `CREATE INDEX`, zero unrecognised statements.
- **"3,839 vs 3,612 lines"** — flagged by the controller as mutually exclusive; **both are correct**,
  naming different file sets that differ by exactly the 227-line adjudication. ⚠ Recorded because the
  controller raised it as a defect and it was not one.

## Out of scope — queued as backlog

- **B15 (K5) — a genuine PRE-EXISTING repo defect found in passing.** MySQL's `-- +goose Down` drops
  **8** tables where Postgres and SQLite drop **9**: `wrkflw_outbox` is created and **never dropped**,
  so a MySQL rollback leaves it behind. Verified:
  `grep -c "^DROP TABLE"` → postgres 9, mysql 8, sqlite 9. **Not fixed here** — it is a migration
  rollback bug, unrelated to at-rest posture. **Filed as backlog 140.**

## Residuals carried into implementation

1. **D2b, D11 and D12 have no derived pairwise interactions** (B6). A round 3 would start there.
2. **D7's cross-check remains column-names-only** while the parser traps that justified it are all in
   the index path (round-1 residual, unchanged).
3. **The `-update` flag still propagates a wrong-but-consistent classification** (round-1 D6×D9,
   accepted as a limitation; no second independent derivation exists).
