# ADR-0184 bundle — rule-#9 audit adjudication

**Date:** 2026-08-18 · **Bundle:** spec + ADR-0184 + plan, branch
`feat/test-wait-budget-and-conformance-completeness`
**Lenses:** A (execution), B (failure modes / cross-document), C (re-counting) — all Opus, all in
detached worktrees created **at the bundle commit** so the documents were present by construction.

**Outcome: every finding ACCEPTED. Two required a decision between offered options; both are
recorded below with the reason.** The bundle was revised before any implementation started.

## Why this audit worked

All three lenses were briefed to **execute**, and lens A and C each **implemented Task 1 for real**
before judging it. That is what caught the Critical: three lenses independently refuted the same
prediction, which no amount of re-reading would have found. The one finding only the counting lens
found (C/F1, the 13-vs-15 file count) is the third consecutive delivery where that dedicated lens
caught something the other two missed — it stays mandatory.

## Findings and adjudication

| # | Lens | Sev | Finding | Adjudication |
|---|---|---|---|---|
| 1 | **A-01 + B1 + F4** | **Critical** | `writeOnlyTaskStore`'s count stays **1**, not 2. `checkTaskStoreConformance` returns early at the read-back guard, so the new inbox check is never evaluated for it. | **ACCEPTED — option (a).** Corrected the prediction; `writeOnlyTaskStore` moved to *unaffected* with the real mechanism. The design question it exposes → **backlog 47**. Rejected option (b) (reorder the check so the claim becomes true) as an unscoped change to what an exported helper reports. |
| 2 | A-02 + F5 | Major | The bundle stated one quantity **four** inconsistent ways: "three" (ADR), "two + one comment" (§4.3), "5" (§5.1), four literals (§4.3 table). Executed answer: **one expectation + one comment**. | **ACCEPTED.** Stated identically in ADR Consequences, §4.3, §5.1 and §7, always with the noun. |
| 3 | **B6** | Major | 30 s × 31 serial sites = 930 s vs `go test`'s **600 s** default → binary killed with a goroutine dump printing **no assertion messages**. CI passes no `-timeout`. | **ACCEPTED — budget lowered to 10 s** (310 s, inside the ceiling; still ~1000× the measured 0.01 s fire). Chose this over raising CI's timeout: it needs no CI change and keeps red legible. The derivation rule (`budget × densest package's site count < 600 s`) is now stated in ADR Decision 3. Enforcement gap → **backlog 48**. |
| 4 | B3 | Major | `inboxAssigned` dereferences `c.task.Claim` — **panics in exported API** on a nil claim, killing a consumer's test binary. | **ACCEPTED, both halves**: a guard in the check (defence, works in any repo) *and* case-set invariants (detection, works here), including the empty-`Actor.ID` clause since `AssignedTo("")` returns nothing. |
| 5 | B4 | Major | `inboxNone` at iota 0 violates `cc-skills-golang:golang-naming` ("not optional"); a forgotten `listedBy` silently inherits the weakest contract — this ADR's own vacuity one layer up. A `listedBy` on a rejected case is a silent no-op. | **ACCEPTED.** Added `inboxUnset` at iota 0, rejected by an invariant test; all **eight** cases now declare `listedBy` explicitly. |
| 6 | **F6** | Major | *"the helper rises off 77.8 %"* is **false** — subprocess coverage is not merged; measured still 77.8 % with both guard blocks at hit-count 0. | **ACCEPTED.** Spec §5.3 and the plan's Verification now say it **stays** at 77.8 %, with an explicit ⚠ not to "fix" it by re-introducing the withdrawn seam. This would otherwise have failed the gate for a correctly-working delivery. |
| 7 | B2 | Major | The bundle never named the **three** in-repo stores the exported helper actually runs against (`MemTaskStore`, SQLite `HumanTaskStore`, `CachingTaskStore`). | **ACCEPTED.** All three measured passing; named in §6.3 and ADR Consequences; new plan **Step 8a** runs them. Tightening an exported contract without checking the module's own implementations would have been this delivery's defect class applied to itself. |
| 8 | A-03 | Major | Plan Task 3 Step 4's first verification command **cannot** reach its expected exit code — its `-A6` window matches `clk.Advance(time.Second)`, so it reports "not done" after a perfect conversion. | **ACCEPTED.** Replaced with a positive count-based check, plus **Step 4a** which proves the command discriminates by reverting one site and observing 39. |
| 9 | A-04 | Major | The second verification command is **vacuous for the 4 multi-line `Never` sites** — exactly where an accidental edit is most likely. | **ACCEPTED.** Replaced with a structural before/after budget extraction that covers multi-line calls. |
| 10 | B5 | Major | §4.3's "unaffected" list was **right by accident**: two of five reasons were false. `rejectingTaskStore` is protected by an early return, not by "an extra failure doesn't flip `NotEmpty`". | **ACCEPTED.** Rewrote the list around the two real mechanisms and added **§6.10** — the early-return premise that both this and finding 1 turn on, which the bundle stated nowhere. |
| 11 | F1 | Major | *"15 test files"* is wrong — **13** carry an `Eventually`; the 2 extra are `Never`-only files the delivery **forbids** touching. | **ACCEPTED.** Corrected in the plan's File Structure, Task 3 Files and spec §6.5, with the two forbidden files named explicitly. |
| 12 | F2 | Low | ADR §4 quantified over `require.Never`, but **6 of 16** sites are `assert.Never`. | **ACCEPTED.** Reworded to cover both across ADR, spec and plan. |
| 13 | F3 | Info | §6.1's *"comments rather than calls: 0"* is pattern-dependent and false as written. | **ACCEPTED.** Replaced with the line-by-line enumeration as the basis for 40. |
| 14 | A-05 | Info | `-count=25 -race` on `TestGocronScheduleJobTriggers` is cheap; no defect. | **Noted**, step kept. |

## Verified-correct (recorded so nobody re-derives them)

Lens C independently re-derived and **confirmed**: the 40-site count; the 30/8/2 budget
distribution; **all 40 line numbers** in the plan's Task 3 table; the 6/31/2/1 per-package split;
16 `Never` sites at 100/150/200/300 ms; the 4 test packages; 5 legal + 3 rejected cases and every
case name the bundle spells; the vacuity claim (§6.2); the constructibility claim (§6.3); the
`failedSubtests` hazard; and that the 7 stand-in stores are a complete split.

## Process note for the next delivery

The controller's own prediction was the Critical — again. On ADR-0179 four of six wrong counts were
the controller's; on ADR-0183 the controller's briefs were wrong roughly five times. Here the wrong
claim was a **reasoning-by-analogy** step (*"nothing persisted, so the inbox assertion also fails"*)
about a code path that is **unreachable**. The lesson is narrower than "check your counts":

⭐ **A prediction about what a test will report is a claim about CONTROL FLOW, not about the store's
behaviour. Trace the early returns between the assertion and the entry point before predicting a
count.** Every stand-in the bundle got wrong (`writeOnly`, `rejecting`) was protected by a `return`
the bundle never mentioned.
