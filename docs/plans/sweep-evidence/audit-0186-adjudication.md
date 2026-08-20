# ADR-0186 (untrusted input & disclosure) — audit adjudication

**Date:** 2026-08-21 · **Bundle audited:** `32f4e3e5` — the standalone ADR-0186 delivery
(spec `2026-08-21-untrusted-input-and-disclosure.md` + ADR-0186 + plan `2026-08-21-…`).
**Lenses:** execution · failure-modes · counting · **interaction** — four Opus agents, four
detached worktrees at the bundle commit, step-0 presence check passed in all four.
**Reports:** `audit-0186-{execution,failure-modes,counting,interaction}.md` (4,020 lines).

⚠ All four were killed mid-run by an API session limit. Because each brief required appending
findings **per finding**, essentially nothing was lost; two were already complete and two were
**resumed from transcript** (never replaced) to write their closing sections.

## ⛔ VERDICT: FAILS. But the failure is different in kind, and that is the finding.

**63 findings, 33 Critical.** Yet three of the four lenses independently concluded that the
*decisions* are largely sound and the **plan** is where this bundle breaks:

| lens | its own synthesis |
|---|---|
| execution | *"All four Criticals are one-line fixes, not design increments — which distinguishes this bundle from B3's D4/D5 and looks like it vindicates the re-cut."* |
| failure-modes | *"Six Criticals share one root cause: a decision stated in the ADR whose realisation lands in a package no phase assigns it to."* |
| interaction | *"Five of seven Criticals are a decision assuming a **channel** another decision owns and does not provide."* |

That is a **plan defect**, not a mechanism defect. Contrast B3, where the strict-reference rule
(backlog 103) and the claimant guard (backlog 124) were wrong *as mechanisms* and needed design
increments that did not exist. Nothing here is in that category.

⚠ **The counting lens is the exception and must not be discounted**: its seven Criticals are
enumeration failures in the bundle's own evidence, and they are the class this repo keeps
repeating.

## ⭐ The single highest-value adjudication

**Move the element bound from EVALUATION to ADMISSION** — bound the variable map when it enters
the instance, not when a predicate reads it.

The interaction lens identified this and it closes, together: **I-2** (D2's two halves are jointly
unsatisfiable), **I-9** (the byte cap admits 45,540 elements against an element cap of 10,000, so
the 10,001–45,540 window is persisted then permanently unevaluable with no repair verb), **I-14**,
and it makes **I-3** moot. Combined with the cost reframe below it also dissolves **E1**, **E2** and
**F14**. That is **one change closing roughly seven findings, most of them Critical.**

## Accepted Criticals, grouped by what must change

### A. D2's cost premise was wrong, and the mandate it forced is unnecessary (E1, E2, E3, F14, I-2)

⚠ **This is the controller's analytical error, caught independently by two lenses with independent
measurements.** ADR-0186 D2 justified a "count once per env" mandate with *"20–60× worse than the
cost the decision refused"*. That compared a **worst-case bound cost** (10,000 elements) against a
**typical-case ctx cost** (3 scalars).

| | execution lens | failure-modes lens |
|---|---|---|
| typical env, bound | +74 ns / 0 allocs | 64 ns |
| the ctx it refused | 866 ns | 820 ns |
| verdict | **~12× cheaper** | **~13× cheaper** |
| crossover | ~500 elements | — |
| worst case in context | 16.5 µs replaces a **2.458 s** evaluation | 0.0009 % of a 1.92 s evaluation |

⇒ **Delete the once-per-env mandate.** It is unimplementable (E1: `ConditionEvaluator` passes a
bare `map[string]any` and D2 refuses to change the signature; E2: `reflect.ValueOf(env).Pointer()`
is unsound — 200,000 distinct maps produced 82,473 addresses, 59 % collided, and a memoised
`count=2` was observed backing a map of real count 50,001, so the bound fails **open**) **and it was
never needed.**

⭐ **The n = 10,000 default is VALIDATED**: extrapolated 2.442 s, measured 2.458 s — 0.65 % error.
It is genuinely derived.

### B. Decisions whose realisation no phase assigns (F1, F2, F3, F19, F15)

- **F1/F2** — the correlation id **cannot be produced by `ClassifyError`**: its signature carries no
  context and no config, and changing it is an unlisted breaking change to an exported seam. And no
  phase builds the **log half** of the join: the three `writeErr`s log only `>= 500` and `httpcore`
  never logs at all. The entire justification for blanking 403 is a join nobody built.
- **F3** — the 400 allow-list's **deny half** is built by no phase; `errors.go:36-50` renders all
  eight sentinels through one `err.Error()` and no test covers the seven non-validation ones.
- **F19** — phase 7's "sentinel classified 413" is built by no phase and is unschedulable as the
  phase table is drawn; it would ship as an empty-bodied 500.
- **F15** — D2 bounds **one of the two evaluator surfaces it itself enumerates**; both ABAC
  evaluators have no options seam, and their 5 s timeout converts CPU burn into goroutine
  accumulation.

### C. Guards that refuse the useful case (F4, F9, F10, F18)

- **F4** — the static-400 default **destroys actionable messages three prior ADRs deliberately
  added**. Four of seven blanked sentinels echo no caller value at all, and three carry in-code
  ADR-0146/0152/0183 rationale demanding they stay actionable. `ErrBadInput` — the highest-volume
  400 — becomes `"invalid input"` on all 26 routes.
- **F9** — `WithAllowedHosts` is **unimplementable** in the prescribed `net.Dialer.Control` hook:
  executed, the hook receives only the resolved `IP:port`. The fine-grained escape hatch does not
  work, leaving only wholesale `WithUnrestrictedTransport()`.
- **F10** — D3 **silently collides with the existing `WithHTTPClient`**; depending on option order,
  either the security control or the consumer's instrumented client is dropped. ⚠ This is the exact
  collision class the bundle flags for `runtime.WithMaxEvalElements` and misses here.
- **F18** — the default-ON caps can **wedge an instance permanently**: first-party `httpcall` writes
  up to 10 MiB into `vars["httpBody"]` (40× the 256 KiB cap), no verb shrinks variables, and a
  persist-boundary refusal blocks even cancel.

### D. Enumeration and premise failures in the bundle's own evidence (C1, C4, C5, C6, C7, C8, C10, F5, F6, F7, E4)

- **C1/F5** — *"every one of the 39 decode sites already wraps in `ErrBadInput`"* is **FALSE: 36 do;
  3 discard the decode error entirely**, so an oversize body there is silently swallowed and returns
  **2xx**. ⚠ The net was wrong again — third consecutive round.
- **C5/F6** — `TestActionableViewRedactsTaskVars`, which the plan bills as *"the control that
  decides D4's placement"*, **cannot fail**: executed, `ActionableView` has **no `Vars` field** and
  never reads `t.Vars`. The ADR premise it rests on is false. ⚠ Prescribed by the controller.
- **C4** — the read-path enumeration is stated as 6 + 2 = 8; source has **6 + 2 + 3 = 11** — three
  admin endpoints call `NewInstanceView` directly.
- **C6/F7** — redaction covers `variables` **only**; the snapshot also emits token payloads, raw
  incident error strings, actor attribute maps and the whole definition. The hook signature
  `func(map[string]any) map[string]any` is instance-blind and scope-blind.
- **C7** — `SECURITY.md` is prescribed to name *"the two"* plaintext columns; there are at least
  **six**, including `wrkflw_human_task.vars` — the same process variables, in a second table.
- **E4** — the "value-free" 400 rendering **is not value-free**: `InstanceLocation` is
  instance-derived, so a card number submitted as an object key renders verbatim as
  `at '/4111-1111-1111-1111': violates type`. The prescribed test's closed-`properties` fixture is
  green against the leak.
- **C8** — *"three options writing one field is last-writer-wins, silently"* is **FALSE**: both
  existing options **document** last-wins in their godoc, twice.
- **C10** — the spec's header and its own §6 state **contradictory** splits of which parts of the
  inherited evidence file hold.
- **F22** — above fiber's 4 MiB app limit the adapter is **never reached**: plain-text 413, no
  `ErrorBody`, no correlation id, no log; `MaxBodyBytes > 4 MiB` is silently ignored.
- **E8** — fiber's `c.Body()` **decompresses**, so the pre-check returns **400, not 413**, on exactly
  the amplification case (63.7 KiB gzip → 64 MiB yields `len == 33`).

### E. Interaction Criticals not covered above (I-1, I-4, I-6, I-10, I-11)

- **I-6** — D5 moves submitted values **off the wire and onto `slog.Default()` by default**, into a
  sink D4 cannot redact and D6's at-rest enumeration excludes. The fix relocates the disclosure
  rather than removing it.
- **I-10** — **variables grow during execution**, so D1's cap fires with no HTTP caller present and
  D5's static `"request too large"` is simply false in that path.
- **I-11** — D4's redaction hook plus D4's prescribed **shallow** copy: redacting a nested secret
  **mutates shared cached instance state**.
- **I-4** — the D2×D3 OPEN question resolves **NO** and is unwireable: the one expression surface
  the bundle calls attacker-influenced is the one the bound cannot reach.
- **I-1** — the oversize→413 chain is unreachable at three decode sites and the three adapters
  diverge there.

## What HELD — do not re-litigate

- The `gate.go:45` `%s`-flattening premise (the reason the 400 rendering moved into
  `runtime/validation`) — **reproduced independently**.
- The **413-before-400** arm ordering requirement.
- The **eight**-sentinel count (⚠ the ADR *banner* said nine — inherited from the re-audit summary
  and restated without checking; corrected).
- The **ctx benchmark** (99 → 965 ns) and the **O(n²) ladder**, both reproduced independently by two
  lenses.
- **H1**: the n = 10,000 extrapolation validated to 0.65 %.
- **H5**: fiber's `BodyLimit` really is unreachable from a mounted group, and `len(c.Body())` really
  is reachable — **discharge that assumption, for uncompressed bodies only** (E8 covers the rest).
- Two plan-flagged OPEN items are now **settled with evidence**: the env bound does **not** reach
  `action/httpcall` (F17/I-4), and the fiber pre-check is viable only below 4 MiB (F22).
- 16 further survivors are listed in the failure-modes report's "What HELD" section.

## ⚠ What the controller got wrong, recorded so it is not repeated

1. **A worst-case cost stated as the general cost** (A above). Caught by two lenses independently.
   This is the *"verify recap sentences"* failure in a numeric form.
2. **A prescribed test that cannot fail** (C5/F6) — and it was billed in the plan as the control
   that decides a decision's placement. **Check the fixture, not the assertion.**
3. **The wrong grep net, for the third consecutive round** (C1/F5): 36 of 39, not 39 of 39.
4. **An inherited number restated without checking** (the banner's "nine sentinels" vs the body's
   correct "eight") — the fourth such failure this session, and a self-contradiction inside one
   document.
5. **A collision class flagged in one place and missed in another** (F10): the bundle warns about a
   third writer of `driver.conditionEval` and does not notice `WithHTTPClient`.

## Required next steps, in order

1. **Adjudicate the admission-boundary move first** — it collapses the largest cluster.
2. **Delete D2's once-per-env mandate** (unnecessary and unimplementable).
3. **Rework the plan's phase table** so every decision's realisation has a package assigned. Six
   Criticals are this one defect.
4. **Re-derive the three enumerations** that failed: decode sites (36/39), read paths (11 not 8),
   plaintext columns (six not two).
5. Re-audit. ⚠ A bundle whose decisions changed has not been audited.

**Do not implement.** Nothing here has been folded.
