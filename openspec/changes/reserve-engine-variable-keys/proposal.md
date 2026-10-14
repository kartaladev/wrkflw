# Proposal

## Why

The engine writes three process variables itself: `_idempotencyKey`, `_errorMessage` and `_errorAttempts`. A caller can still set any of them, exactly or as a key that encoding/json folds onto them. kartaladev/wrkflw#187, a release blocker for `cut-v0-release`, shows two consequences. A caller can fabricate or suppress the `_errorMessage` a definition routes on. A caller's fold-variant also wins over the engine's own value in five action inputs that the primary service-action filter does not cover. kartaladev/wrkflw#186 is the related expression hazard. The no-shadowing barrier and the soundness of the code-keyed program cache both hold only because no `expr.Env` is passed, and nothing reliably detects the loss of either.

Re-measured on `main` at `9c6dd2c1` (go1.26.9, expr v1.17.8). Probes ran from the scratchpad through `go test -overlay`, so nothing was written to the repository.

| Premise | Measurement on main |
|---|---|
| #187: a start variable `_errormessage` beats the engine's `_errorMessage` in a user task's completion-action input | **Reproduced.** After a recovered failure the input holds `{"_errorAttempts":1,"_errorMessage":"ENGINE-ERR","_errormessage":"SPOOF"}`, and a `json:"_errorMessage"` field decodes `"SPOOF"`. Control: with no start variable, the same field decodes `"ENGINE-ERR"`. |
| #187: five action inputs are built from the variables without the filter | **Holds, with stale lines.** Completion action `engine/step_triggers.go:867` (the issue says `:852`; the comment at `step_state.go:418` still says `:852`). Cancel actions `:180` and `:204`. `step_cancel.go:127` is `emitFireOnceAction`, the fire-and-forget boundary and handler action, not a "cancel action". Compensation `step_compensation.go:671`. |
| #187: `mergeVars` has eight sites, all in `step_triggers.go`, and none re-stamps | **Holds, lines drifted.** The sites are now `:45 :160 :891 :986 :1078 :1256 :1360 :1397`. The engine write moved to `:562`. `TestErrorMessageIsCallerWritable` pins fabrication and suppression today. |
| #187: the call activity passes the parent's variables to the child | **Holds** (`step_nodes.go:1149`). Also found: the child's whole variable map merges back into the parent on completion (`:1256`, fed from `runtime/processdriver_action.go:667`). A send task's payload (`step_nodes.go:140`) and a user task's eligibility snapshot (`:764`) are further copies. |
| #186: no `expr.Env`, and the cache is keyed by the code string alone | **Holds** (`internal/expreval/expreval.go:102-114`). |
| #186: `TestBuiltinsAreNotShadowableByEnvKeys` fires when `expr.Env` is added and reports 6 shadowable identifiers | **Stale as stated.** With the natural mutation (pass the env to `compile` and add `expr.Env(env)`), the guard **passes**. The cache serves the program compiled for the base env to the planted evaluation, so the cache unsoundness hides the barrier's loss from its own guard. It reports 6 only when the cache is also disabled. Under the natural mutation, the only failure in `internal/expreval`, `engine`, `runtime` and `authz` is one subtest of `engine`'s `TestBoundaryRoutingUnderCallerChosenKeyNames`. |

## What Changes

- **BREAKING (small).** None of the five remaining action inputs carries a key that folds onto an engine-stamped name without being that name. These are the user-task and receive-task completion action, the instance and node cancel actions, the fire-and-forget boundary and handler action, and the compensation action. The primary service action already withholds such keys. The exact engine-written names still arrive. This fixes the reproduced instance of kartaladev/wrkflw#187. It does not depend on Q1.
- **BREAKING, pending Q1–Q3 (recommended option specified).** A variables map supplied from outside the engine is refused with a new exported sentinel, `engine.ErrReservedVariable`, when any of its keys equals or folds onto `_idempotencyKey`, `_errorMessage` or `_errorAttempts`. The refusal names the key and leaves the instance unchanged. The maps covered are start variables, a human completion's output, a message payload and a signal payload. This closes fabrication and suppression at the ingress, as #187 asks, without silently dropping a caller's key. A call activity reached after a recovered failure must still start its child.
- Two current promises of expression evaluation are written down and guarded, from kartaladev/wrkflw#186:
  - a process variable named after an expression builtin never changes what a condition calls;
  - a condition's result depends only on the variables of that evaluation, whatever was evaluated before it.
  The shadowing guard is made cache-proof, so it fails on the mutation it exists for. A cache-order guard is added.
- The comment enumerating the unfiltered sites (`engine/step_state.go`) is corrected. It currently cites `step_triggers.go:852` and calls `emitFireOnceAction` a cancel action.
- Persisted formats: **no change** under the recommended Q1 option. Snapshots and journal rows that already hold a reserved key or a fold-variant still load. The refusal applies to incoming maps only, and the action-input filter covers stored variants. The rejected "namespace the engine's keys" option would change the snapshot format; see design.
- Default security posture: **tightened.** Callers can no longer write engine-stamped variables, and the expression barrier gains a guard that actually fires.
- Consumer go.mod: no impact.

Absorbed issues:
- kartaladev/wrkflw#187: absorbed. Its line numbers were found stale on re-measurement, and its "cancel action at `step_cancel.go:127`" is a mislabel. The defect itself reproduces.
- kartaladev/wrkflw#186: absorbed. Its claim that the shadowing guard fires under the `expr.Env` mutation was found stale. The two hazards interact: the cache unsoundness hides the barrier's loss.

Decisions the ingress requirement cannot be specified without (design, Open Questions). No maintainer decision is recorded on either issue thread. Specs carry the recommended option, marked pending.
- **Q1.** What reserving means: refuse the write (recommended), namespace the engine's keys, or strip them.
- **Q2.** Which keys: the three names exactly and their fold-variants (recommended), fold-variants only, or every `_`-prefixed key.
- **Q3.** Maps the engine or consumer code originates: an action's completion output, a child's start input and completion output, and an `OutcomeVariable` naming a reserved key.
- **Q4.** (#186) Refuse `expr.Env` at the evaluator seam structurally (recommended), or key the cache by environment shape.

Out of scope:
- Recursive exact-key checking under strict typed input, and lenient `In` validation: kartaladev/wrkflw#142 and #137, owned by `harden-typed-action-input`.
- Absent-variable refusal, the expression timeout sentinel, and anything else in `refuse-silent-expression-outcomes`. That change already depends on there being no `expr.Env` (its design D1). This change keeps that true and does not touch its requirements.
- Exporting other refusal sentinels: `export-refusal-sentinels`.
- Payload size and key-count ceilings on inbound maps: `bound-untrusted-input`.
- Persisting pending commands such as a `StartSubInstance` written before an upgrade: `persist-pending-commands`. See the coupling note in design.
- Disclosure of variables through read routes: `define-access-and-disclosure`.
- `_error`, the ephemeral boundary-expression binding. It is injected into a clone and never persisted, and it stays an ordinary caller variable elsewhere, as the baseline row "Keys that fold onto no reserved name still arrive" pins.
- kartaladev/wrkflw#141 (closed). Its measurement is baselined as "Instance data is bound as data", and that requirement is unchanged here.

**Coordination**
- Size: L
- Depends on: `establish-baseline-specs`
- Blocks: `cut-v0-release`
- Readiness: human

## Capabilities

### New Capabilities

None.

### Modified Capabilities

- `process-variables`:
  - ADDED: no action input other than the primary one carries a fold-variant of an engine-stamped name;
  - ADDED: externally supplied variables may not name an engine-stamped variable (pending Q1–Q3).
- `expression-evaluation`:
  - ADDED: a variable's name never changes which function a condition calls;
  - ADDED: a condition's result depends only on that evaluation's variables.

## Impact

- `engine/step_state.go`: the fold-variant filter is shared by every action-input site, and the site-enumerating comment is corrected. A reserved-key check runs at the ingress (pending Q1).
- `engine/step_triggers.go`, `engine/step_cancel.go`, `engine/step_compensation.go`: the action-input sites, and the `mergeVars` ingress sites.
- `engine/errors.go`: `ErrReservedVariable`. This file is shared with `refuse-silent-expression-outcomes` and `export-refusal-sentinels`.
- `engine/step_nodes.go`: the call-activity child input (Q3).
- `transport/http/httpcore/errors.go`: a 400 arm for `ErrReservedVariable`, applied identically by stdlib, gin and fiber.
- `internal/expreval/expreval.go`, `internal/expreval/shadowing_test.go`, plus a new cache-order test. `expreval.go` is shared with `refuse-silent-expression-outcomes`.
- Tests that flip: `TestErrorMessageIsCallerWritable` (fabrication and suppression become refusals), plus any test or example that starts an instance with a reserved name.
