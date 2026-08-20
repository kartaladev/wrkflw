# B3 authz/security bundle — rule-#9 audit adjudication

**Date:** 2026-08-20 · **Bundle audited:** `3f317b63` (spec + ADR-0185 + ADR-0186 + plan)
**Lenses:** execution · failure-modes · counting — three Opus agents, three detached worktrees at
the bundle commit, step-0 presence check passed in all three.

## ⛔ VERDICT: THE BUNDLE FAILS ITS AUDIT. It is NOT an input to implementation.

**58 findings, 12 Critical.** Four decisions must change before any code is written. This is not a
"fold the fixes and proceed" outcome — two of the Criticals invalidate a Decision's mechanism
outright, and one would have shipped a feature that denies every user.

## Accepted Criticals (all 12 accepted; none rejected)

### A. ADR-0185 Decision 4's escape hatch does not exist
`has(vars, "k")` is **not a function in expr v1.17.8**. Executed: `has(vars,"tier")` →
`invalid operation: cannot call nil (1:1)`. `AllowUndefinedVariables` resolves `has` to nil, so it
**compiles and fails at run time**, and `RoleAuthorizer` wraps run errors as `ErrNotAuthorized` ⇒
**a predicate written to the ADR's own prescription denies everyone, permanently.** Plan phase 1
test 3 cannot pass.
**Fix:** replace with a form that exists — verified working: `"k" in vars`, `vars?.k`,
`vars.k ?? default`, `get(vars,"k")`. Re-run every predicate example in the bundle against the
pinned expr version before rewriting the ADR.

### B. `Reassign` → `Complete` bypasses Decision 5's claimant guard (found INDEPENDENTLY by two lenses)
`Reassign` authorizes `By` against `task.Eligibility` only — the exact set-membership check the ADR
says cannot distinguish the claimant — and the completion path then overwrites `task.Claim` with the
new assignee from an unvalidated body string. An eligible actor reassigns another's task to
themselves, then completes as claimant. The one input required (the current claimant's id) is
disclosed by **item 54, in this same bundle**.
⚠ **ADR-0185's Consequences sentence — "can no longer complete a task somebody else holds" — is
FALSE as written.** The ADR names `Reassign` as the mitigation; it is the escalation.
**Fix:** the guard must cover `Reassign`, not just `Claim`/`Complete`, and the Consequences text must
be rewritten, not patched.

### C. The upgrade strands every in-flight human task (found INDEPENDENTLY by two lenses)
Eligibility is a **stored** field frozen into the task record at creation; all four `Authorize` sites
read the stored spec, never the definition. `AuthzSpec` has **no json tags**, so a new binary decodes
pre-upgrade rows with `Open == false` ⇒ open tasks become unclaimable, uncompletable **and**
unreassignable, with no repair verb, and re-authoring the definition does not fix them.
⚠ The plan's deployment gate guards the **opposite** direction (old reader, new row), which the repo
already declares out of contract. **The phase table has no persistence phase at all.**
**Fix:** tri-state `Open`, plus a data migration phase, or an explicit drain-before-upgrade contract.
Cheapest alternative offered: scope Decision 3 to the mint site rather than to authorization.

### D. The fix ships everywhere except where the ADR sends people
`internal/authz/casbin/authorizer.go` builds its **own** `expreval.New()` and its own
*"an empty spec allows"* godoc, and stays fail-open. ADR-0185 D4's "structural rather than
conventional" scoping argument reasons over **two** evaluator instances; **four** `expreval.New(`
instances exist. Worse, **Decision 3 tells consumers "a consumer who wants privileges evaluated
wires the casbin authorizer"**, and CLAUDE.md makes casbin the baseline.
**Fix:** hoist the spec-stated gate above the implementations (e.g. into `runtime/task.TaskService`
before all four `Authorize` sites) so every `Authorizer` inherits it.

### E. ADR-0186 Decision 2 checks the wrong invariant and costs ~10×
The ADR justifies the `ctx` on `ConditionEvaluator` against the *import* rule (`purity_test`). The
invariant actually locked at `engine/conditions.go:29-43` is **deterministic replay / never spawns a
goroutine** (ADR-0003/0049/0056), which `purity_test` structurally cannot see. `expreval.run` is
synchronous when `timeout<=0`, so honouring a ctx forces the engine default onto the goroutine path.
Benchmarked on an ordinary gateway condition: **99.43 ns/op, 3 allocs → 965.2 ns/op, 9 allocs.**
The prescribed test passes under either horn.
**Fix:** re-decide D2 with the real invariant stated and the cost quoted, or drop it.

### F. Counting and anchoring failures
- **Pin count is 29, not 23** — the arithmetic was right, the **grep was the wrong net**:
  `ReassignInput.By` is tagged `"by"`, not `"actor"`, hiding six reassign-body pins. Per package:
  stdlib **5** (plan says 3), gin **7** (says 5), fiber **5** (says 4). ⚠ **Two missed pins assert
  403 and would still pass after D1 — from the zero actor — while testing nothing.**
- **Every `step_triggers.go` citation is 10 lines stale at the bundle's own commit.**
  `handleHumanCompleted` is `:849`, not `:839`. The bundle anchored to base `70a631e9`, but
  `3f317b63` itself edits that file. The prescribed `awk` window starts 10 lines early and ends 23
  short; its lone "hit" is a godoc line. Over the true body `:849-983` the count is **zero** —
  conclusion confirmed, measurement wrong.
  ⇒ **Re-anchor every citation to the bundle commit, and prefer symbol names to line numbers.**
- **Decision 3's blast radius was never counted, and its one quantifier is false.** "Every existing
  definition with no eligibility becomes invalid": **274** `NewUserTask` sites, **128** without an
  eligibility dimension, but only **5** reach `model.Validate`. The authoring gate touches ~5 sites;
  the runtime rule hits all 128 across `engine`, `runtime`, `processtest`, `service` — **none of
  which has a phase**, and phase 4 verifies only `./definition/...`.

## Accepted Majors (fold when the bundle is revised)

- The **400 arm the bundle deliberately preserves echoes submitted values verbatim** via jsonschema
  `pattern` — this resolves spec §4.7's own `ASSUMPTION (unverified)` **against** the bundle, and
  phase 7 test 3's second control would have **pinned the leak in**.
- `WithActorResolver` **collides with an existing symbol** (`service.WithActorResolver`, and 180 hits
  across three packages) meaning candidate expansion — the opposite direction of data flow. Rename.
- **No decision for "nothing put an actor in the context"**: zero actor + `Open:true` = anonymous
  claim/complete, `Actor{ID:""}` in the audit record (no empty-ID guard exists anywhere), and D5's
  guard degenerates to `"" == ""`. `ActorResolver`'s error path is likewise undefined and untested.
- **`RedactVariables` is bypassed wholesale by `CustomizeConfig.InstanceMapper`**, which receives the
  raw `InstanceState` — the documented response-customization feature disables the new control.
- **Phases 3 and 4 are circular** (phase 3 writes a field phase 4 creates).
- **Zombie scope**: D2's "same knob" is built as two unconnected knobs in different packages and
  different units, with no plumbing to `engine/conditions.go:43`.
- **The 256 KiB default is refuted by the bundle's own O(n²) table** (~43–50k elements ⇒ ~45–60 s of
  unpreemptible CPU).
- **Static reference extraction is depth-1** and misses nested/dynamic/zero-reference predicates, so
  D4 over-claims closure; "five predicate forms" describes an **unbounded class as closed**.
- **`runtime` exports `WithConditionEvaluator`/`WithExpressionTimeout` on the changed interface** with
  no phase and no breaking-change entry. **`ErrorBody`'s correlation id** is an unowned wire change,
  also absent. **413** is asserted with no `MaxBytesError`→status mapping across three adapters.
- **ADR-0117 Decision 3 is reversed too**, not just Decision 1 — only Decision 1 is annotated.
- **12 `examples/scenarios` mains, not 13** (the twelfth rotted enumeration this session).

## What HELD (do not re-litigate)

Every premise for **51, 52, 53, 103, 104, 98, 54**; the §2.4.1 refutation of the triage's proposed
fix; the **`MaxNodes` inversion** (also stated outright in the vendor godoc at `expr.go:221`); the
two-evaluator-*surface* correction; fiber's 4 MiB being the framework's own. Arithmetic held almost
uniformly: 26 routes = 9+15+2, 39 = 13×3, 10 options, 6 `DurableProvider` methods, 6 journal columns,
5 echoing 4xx arms, 3 `stdlib.Mount` examples, 4 `Authorize` sites, `DefaultMaxNodes = 1e4`, and 28
further citations. **ADR numbers 0185/0186 are genuinely free.** The **parked item-102 decision is
genuinely carried** (ADR-0185 D6 + plan phase 8 + a `SECURITY.md` obligation) — no finding.

## Required next steps, in order

1. **Revise the bundle** against A–F above. A, B, C and E are decision changes, not text fixes.
2. **Re-anchor all citations to the revised bundle's own commit**; prefer symbols to line numbers.
3. **Re-audit.** A bundle whose Decisions changed has not been audited — the rule-#9 checkpoint
   applies to the revised bundle, not the original.
4. Only then implement, fanned out by Go package.

## Process lesson

**Two lenses independently found the same two Criticals** (Reassign→Complete; the upgrade stranding).
That convergence is the strongest signal a design audit can produce, and neither would have been
found by reading alone — one needed the *stored*-vs-*declared* distinction, the other needed a
runtime probe of the vendor library.

⚠ **The counting lens again found what the others could not**, for the second bundle running: the
wrong-net grep (29 vs 23) and the stale anchor. Note the failure mode is **not** bad arithmetic —
every sum in the bundle was correct. It was **the net and the anchor**. Future counting briefs should
say so explicitly: *verify what the grep matches, and what commit the line numbers are against.*
