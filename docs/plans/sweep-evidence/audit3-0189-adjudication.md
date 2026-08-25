# ADR-0189 — rule-#9 audit adjudication, ROUND 3 (final)

**Date:** 2026-08-25 · **Bundle:** `3e96e836` (re-cut to ONE decision) · four Opus lenses,
detached worktrees at the bundle commit, step-0 passed in all four.

## ⛔ VERDICT: FAILS. And the reason is NOT what rounds 1–2 concluded.

| lens | findings | C | inter-fix C |
|---|---|---|---|
| execution | 15 | 3 | 0 |
| failure-modes | 13 | 4 | 2 |
| counting | 13 | 5 | 2 |
| interaction | 18 | 7 | 6 |
| **total** | **59** | **19** | **10** |

## ⭐⭐⭐ THE SCOPE HYPOTHESIS IS REFUTED BY ITS OWN NEXT DATA POINT

| round | decisions | findings/lens | **Criticals/lens** |
|---|---|---|---|
| 1 | 2 | 12.0 | 1.75 |
| 2 | 9 | 14.5 | 3.75 |
| **3** | **1** | **14.75** | **4.75** |

Scope went **2 → 9 → 1**. Criticals/lens went **1.75 → 3.75 → 4.75**, monotonically **up**.
⇒ **Criticals/lens does not track scope.** Round 2's adjudication concluded it did, three lenses
asserted it independently, the owner acted on it, and **round 3 falsifies it.**

Two confounds, both of which I should have controlled for and did not:

1. **Each round's lenses are briefed with the previous round's findings** — including the exact
   classes the author gets wrong. The instrument was sharpened between measurements.
2. **The documents grew every round.** More stated claims ⇒ more falsifiable claims. Round 3's
   bundle asserts far more, in more detail, than round 1's.

⇒ **Criticals/lens is contaminated by brief quality and document surface. It cannot carry the
weight the pre-registered rule put on it.**

### ⚠ And the rule's calibration was SPLICED — counting lens, confirmed

The series `8.25 → 3.50` quoted in the pre-registered rule is **ADR-0186's rounds 3 and 7**
(`meta-analysis-audit-finding-rate.md:105,109`), **not this lineage**. All three ADR-0185 rounds
are absent, and one of them measured **5.50**, which breaks the monotone story the rule was built
on. Round 1's inherited *"fourth consecutive fall"* describes three points.
✅ The two ADR-0189 anchors I derived myself (1.75, 3.75) **re-derive exactly**, and now 4.75 does
too — but the *narrative* they were embedded in was assembled from another delivery's numbers.

**This is the same failure the audits have caught three times — an inherited series restated
without re-derivation — committed by the author in the very instrument built to stop it.**

## The confirmed Criticals, and what they attach to

Controller-executed unless noted.

| # | Critical | attaches to |
|---|---|---|
| **C1** | round-trip guard **still bricks the store**: guard admits depth 9999, store admits 9998; reproduced end-to-end on real SQLite, guard ADMITTED → `Upsert` OK → `Get` fails forever. Two lenses, two independent mechanisms. 20005-byte payload, so no size bound helps. | **`Attributes`** |
| **C2** | `fatal error: concurrent map iteration and map write` — one-level clone × per-request marshal. **Uncatchable; whole process dies.** New over HTTP. | **`Attributes`** × clone |
| **C3** | the dimension rule achieves **neither** property it is justified with. `{Roles:[""]}`, `{Attributes:{"x":nil}}` pass and are as unattributable as `Actor{}`; the deny-list fail-open closes in **1 of 8** shapes. `Roles:[]string{}` refused, `Roles:[]string{""}` admitted. | refusal rule, partly **`Attributes`** |
| **C4** | plan Task 13 prescribes writing into `SECURITY.md` that `InstanceRoutes`/`MessageRoutes` *"authenticate but do not authorize"* — true only under the **removed** decision, now the exact opposite of the truth, in the file embedders read. | the cut |
| **C5** | Decision 6 says a malformed claim returns 400 *"unchanged from today"*; residual 8 and test row 14 say 401. Executed: **401**. One fix falsified a sentence one bullet above it. | optional body |
| **C6** | plan Task 1 **still** prescribes a vacuous test: IN-clone deletion RED, **OUT-clone deletion GREEN**. The mutation step detects it; the fixture cannot fail. | test design |
| **C7** | member set is **50, not 48** — `stdlib/coverage_test.go:148` and `gin/gin_coverage_test.go:184` flip 400→401 under the optional-body change, in no net and no task. §2.6's two nets are both nets for the **DTO removal**; the optional body is a **third** behavioural change invisible to both. | counting |

⭐ **Five of seven attach to `Attributes` or to guarding it.** That is the signal rounds 1–3 kept
missing while arguing about scope.

## The author's removal grid — diagnosed backwards

⚠ **Its stated reason for existing is false.** It asserts *"every wrong cell involved the one entry
that was a REMOVAL"*. Round 2's own table shows that is **2 of 4**; **3 of 4 involve decision A,
`Attributes` flowing — which SURVIVES the cut.**

⇒ built on that inverted reading, the grid derives survivor×removed and removed×removed
exhaustively and contains **zero survivor×survivor pairs** — six undrawn pairs, and that is
**exactly where C1, C2 and C5 live.** Two lenses found this structural gap independently.

Interaction additionally: **5 of 8 survivor×removed cells wrong or materially incomplete**, and
the grid's survivor set is the *design's decisions*, so the plan's traceability table and §2.6's
member set had no cell at all — both broken.

## The pre-registered rule: fires row 3, and the rule itself was flawed

Row 3 fires on both clauses — Criticals/lens 4.75 ≥ 3, and 10 of 19 are inter-fix holes.
⛔ Round 3 was declared the last audit under all outcomes. **No round 4.**

⚠ Interaction F17: the rule lists *"a guard"* as a paradigm **local** Critical, so **as written it
would have authorised shipping C1 as a residual.** The loophole is not taken. The rule's intent
was *local = fixable without touching another decision*; a guard that cannot be made correct at
its current layer is not that.

## What HELD across three rounds — do not re-litigate

- The **compile-breaking ablation re-derives exact, line for line** (23/5/4), third round running.
- **74 of ~75 anchors resolve** (one break: `runtime/task/service.go:154` → `:139-141`).
- **Both resolver-timeout claims are true as stated**, and the caveat is correctly hedged.
- **No ADR-0186 413 regression** — `decodeOptionalRequestBody` preserves 413.
- **ADR-0187 needs no amendment** — verified three times now, including twice independently.
- §2.7's bodyless-claim 400 holds on all three adapters; §3.4's arm ordering survived unchallenged
  in all three rounds.
- ⭐ Round 1's labelled **prediction** about the two vacuous `stdlib` 403 pins was confirmed by
  execution. The one place the bundle's epistemics worked as designed.

## Root cause, stated once

The delivery kept **arguing about scope** while the defect generator was **one feature**:
`Attributes` flowing end to end. It brought a durable-write guard nobody could get right in two
attempts (C1 twice, by the same category of error one layer apart), a concurrency hazard invisible
until the guard existed (C2), and half of C3. Every round removed something *else*.
