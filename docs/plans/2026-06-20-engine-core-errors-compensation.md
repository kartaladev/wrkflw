# Engine Core — Errors, Compensation & Micro-Step (Plan 8 of 8) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: superpowers:subagent-driven-development (or executing-plans). Checkbox steps.
>
> **Handover note:** Targets the design spec (§3/§5/§6/§10) and assumes Plans 1–7 merged. Contracts fixed by the spec; ground exact edits against current code. SDD review loop is the safety net. This is the final engine-core plan: after it, the pure engine covers the full BPMN scope of sub-project #1.

**Goal:** Add Error end events + Boundary Error events (throw/catch errors with scope propagation), retry-aware failure handling, **Compensation** (per-node compensation actions + reverse-order rollback, including admin-triggered `CompensateRequested`), and finally implement true **Micro-step** mode (replacing `ErrMicroNotImplemented`).

**Architecture:** Errors throw within a scope and propagate outward to the nearest matching boundary error handler; unhandled errors fail the instance (`FailInstance`). Each completed compensable activity records a `CompensationRecord` in its scope; `Compensate` walks those records in reverse completion order, emitting each node's compensation `InvokeAction`. Micro-step makes `drive` advance exactly one node and return.

**Tech Stack:** Go 1.25, `testify`.

## Global Constraints

- Go **1.25**; root packages; engine pure (no transport/storage/bus/time-vendor; no `time.Now()`). `Step` deterministic + pure; public signature unchanged. Black-box tests, `assert`-closure tables, `t.Context()`. Coverage ≥ 85% touched; `-race` green; lint clean. Conventional Commits; commit per green step.

## Prerequisite & contracts

- Trigger (sealed): `CompensateRequested{ToNode string}` (admin/debug rollback), `CancelRequested{}` (spec §5).
- Command (sealed): `Compensate{ScopeID string; FromNode string}`, `FailInstance{Err string}` (already exists). Compensation actions are emitted as ordinary `InvokeAction`s by the engine while compensating.
- model `Node`: `ErrorCode string` (error end / boundary error matching); `CompensationAction string` (the action name run to compensate a completed activity); boundary error reuses Plan-6 boundary fields with an `ErrorCode`.
- `Scope.Compensations []CompensationRecord` where `CompensationRecord{NodeID, Action string; CompletedAt time.Time; Input map[string]any}` — appended when a compensable activity completes (wire this into the ServiceTask/sub-process completion paths from earlier plans).
- `Status` already includes `StatusFailed`, `StatusCompensating`, `StatusTerminated`.

---

## File Structure

```
model/definition.go        # MODIFY: ErrorCode, CompensationAction fields; validate boundary-error attachment
engine/trigger.go          # MODIFY: CompensateRequested, CancelRequested
engine/command.go          # MODIFY: Compensate
engine/state.go            # MODIFY: CompensationRecord; record-on-complete helper
engine/step.go             # MODIFY: error throw/propagate; boundary error; compensation; cancel; record compensations; MICRO mode
engine/step_errors_test.go
engine/step_compensation_test.go
engine/step_micro_test.go
runtime/runner.go          # MODIFY: perform Compensate-driven InvokeActions; surface failures
runtime/errors_example_test.go
```

---

### Task 1: record compensation data on activity completion

**Behavior:** when a compensable activity completes (a `ServiceTask`/sub-process with a non-empty `CompensationAction`), append a `CompensationRecord{NodeID, Action, CompletedAt:at, Input:<the activity's input/vars snapshot>}` to its enclosing `Scope.Compensations`. (Hook into the existing `ActionCompleted` and sub-process-exit paths.)

- [ ] **RED:** `step_compensation_test.go` — `TestCompletedActivityRecordsCompensation`: after a service task with `CompensationAction:"refund"` completes, the enclosing scope's `Compensations` contains a record for that node. Run → fails.
- [ ] **GREEN:** add `CompensationRecord`; append on completion (respect scope; the instance root may be an implicit scope — create one at `StartInstance` if scopes are otherwise only opened by sub-processes). Run → pass.
- [ ] Commit `feat(engine): record compensation data on compensable activity completion`.

---

### Task 2: Error end + boundary error (throw, catch, propagate)

**Behavior:** `drive` `KindErrorEndEvent`: throw an error with `node.ErrorCode` from the token's scope. Error propagation: search the token's scope chain (innermost outward) for a boundary error event (`KindBoundaryEvent` with matching `ErrorCode`, or catch-all empty code) attached to an activity on that scope; if found, cancel the scope's tokens (interrupting) and route along the boundary's outgoing flow. If none up to the root, set `StatusFailed` + emit `FailInstance`. A `ServiceTask` failure (`ActionFailed`) with no retry budget left routes through the same error-propagation path (treat as throwing the action's error code).

- [ ] **RED:** `step_errors_test.go` — `TestErrorEndCaughtByBoundary` (error end inside a sub-process caught by a boundary error on the sub-process activity → recovery path), `TestUnhandledErrorFailsInstance`, `TestActionFailedPropagatesToBoundaryError`. Run → fails.
- [ ] **GREEN:** implement error throw + scope-chain propagation + boundary-error catch; integrate `ActionFailed` into propagation. Run → pass.
- [ ] Commit `feat(engine): error end and boundary error with scope propagation`.

---

### Task 3: Compensation (reverse-order rollback + admin trigger)

**Behavior:** `Step` `CompensateRequested{ToNode}` (admin/debug): set `Status=StatusCompensating`; for the relevant scope, walk `Compensations` in **reverse completion order**, emitting an `InvokeAction` for each record's `Action` (with its recorded `Input`), down to (and excluding) `ToNode`; then place a token back at `ToNode` (rollback target) and resume (`Status=StatusRunning`), or complete compensation if `ToNode` is empty. A `Compensate{ScopeID,FromNode}` command (engine-internal, e.g. emitted on a cancel/error path) drives the same reverse walk for a scope. Compensation actions completing (`ActionCompleted`) advance the compensation sequence deterministically.

- [ ] **RED:** `step_compensation_test.go` — `TestCompensateRequestedRollsBackInReverseOrder`: three completed compensable activities; `CompensateRequested{ToNode:"step1"}` emits their compensation actions in reverse order and parks a token at `step1`. Run → fails.
- [ ] **GREEN:** implement the reverse walk, status transitions, and per-action sequencing (one `InvokeAction` at a time, advancing on each `ActionCompleted`, to keep ordering deterministic and observable). Run → pass.
- [ ] Commit `feat(engine): compensation rollback in reverse order with admin trigger`.

---

### Task 4: Cancel instance

**Behavior:** `Step` `CancelRequested{}`: consume all tokens, `Status=StatusTerminated`, emit `FailInstance`-like terminal (or a dedicated terminal command — reuse `FailInstance{Err:"cancelled"}` or add `TerminateInstance`). Cancel any armed timers/awaits (`CancelTimer` for pending timers).

- [ ] **RED:** `step_errors_test.go` — `TestCancelRequestedTerminates`: a running instance with a parked token is cancelled → `StatusTerminated`, tokens cleared, pending timers cancelled. Run → fails.
- [ ] **GREEN:** implement cancel. Run → pass.
- [ ] Commit `feat(engine): cancel instance with timer/await cleanup`.

---

### Task 5: Micro-step mode

**Behavior:** replace the Plan-1 `ErrMicroNotImplemented` guard. When `opt.Mode == Micro`, `drive` advances **exactly one node** for the single active token, then returns (leaving any newly-active tokens for subsequent `Step(Micro)` calls). Macro behavior is unchanged. Remove `ErrMicroNotImplemented` (or keep it unused/deprecated — prefer removal + delete its test, replacing with real micro tests).

- [ ] **RED:** `step_micro_test.go` — `TestMicroStepAdvancesOneNode`: on a linear Start→Service→End, `Step(Micro)` after start advances exactly one node (start→service, emitting the `InvokeAction`) and does not run further; the macro equivalent reaches the same parked state in one call. Replace `TestStepMicroModeNotImplemented`. Run → fails.
- [ ] **GREEN:** thread `opt.Mode` into `drive` (or a `driveOne` vs `driveAll`); after one node-advance in Micro, return. Ensure determinism + purity preserved. Run → pass.
- [ ] Commit `feat(engine): implement micro-step mode`.

---

### Task 6: runtime — compensation/error surfacing + e2e

**Behavior:** `runner.perform` already runs `InvokeAction` (used for compensation actions too) and handles `FailInstance`; ensure `Compensate`-driven sequences run to completion and that an admin `CompensateRequested` can be delivered via `Runner.Deliver`. e2e (`errors_example_test.go`): a saga-style process (book → pay → ship) where `pay` fails → boundary error → compensation rolls back `book` (refund/cancel) in reverse order; assert the compensation actions ran in order and the instance ends `Failed`/`Compensating→Terminated` as designed.

- [ ] **RED:** `errors_example_test.go`. Run → fails.
- [ ] **GREEN:** wire delivery of `CompensateRequested`; ensure perform handles the compensation `InvokeAction` stream. `go test -race ./...`, coverage, `golangci-lint run ./...` → green/clean.
- [ ] Commit `feat(runtime): compensation/error saga e2e`.

---

## Verification Checklist (Plan 8)

- [ ] Compensable activities record `CompensationRecord`s in their scope on completion.
- [ ] Error end / action failure propagate out the scope chain to a matching boundary error handler; unhandled → `FailInstance`.
- [ ] `CompensateRequested` rolls back in reverse completion order to the target node; status transitions Running→Compensating→Running/Terminated are correct.
- [ ] Cancel terminates the instance and cleans up armed timers/awaits.
- [ ] Micro-step advances exactly one node per `Step`; macro unchanged; `ErrMicroNotImplemented` removed and its test replaced.
- [ ] Saga e2e rolls back correctly; `Step` deterministic + pure; engine no `time.Now()`; `-race` green; coverage ≥ 85%; lint clean.

## Self-Review Notes

- **Spec coverage:** §3 error end + boundary error; §5 Compensate + CompensateRequested + CancelRequested; §6 compensation reverse-order walk; §10 — and finally the configurable Micro mode promised in Plan 1. After this plan the engine core covers the full broad-BPMN scope of sub-project #1.
- **Determinism:** compensation emits one `InvokeAction` at a time, advancing on each `ActionCompleted`, so rollback order is observable and reproducible; reverse walk over the recorded slice is deterministic.
- **Retry/resilience boundary:** `ActionFailed.Retryable` informs the runtime's retry policy (backoff/poison) — the *engine* routes a non-retryable/exhausted failure to error propagation. The retry executor (backoff, max attempts, poison queue) is a runtime concern layered on `InvokeAction.RetryPolicy`; note it for the persistence/runtime productionization sub-projects beyond the engine core.
- **Grounding required:** read merged `engine/step.go` (scopes from Plan 7, boundaries from Plan 6, timers from Plan 5, human tasks from Plan 4) before editing; error propagation and micro-step both touch `drive` broadly — re-run the full engine suite after each.
- **End state:** with Plans 1–8 merged, sub-project #1 (the pure engine core + reference runtime) is complete. The next sub-projects (separate spec → plan cycles) are the productionization layers: Postgres persistence + cache, watermill/outbox eventing, gocron scheduler, casbin authorizer, REST/gRPC transports, admin monitoring, and `ProcessInstance` response customization.
