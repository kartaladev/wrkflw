# Design: compensation on error / cancel

**Date:** 2026-06-23
**Status:** Approved (user authorized engine change)
**Track:** Consolidated-backlog top pick (Correctness). Builds on ADR-0013 (compensation hoist) + ADR-0028 (cancel).
**ADR:** 0034.

## 1. Problem & scope

The engine records a `CompensationRecord` for every completed compensable activity
(`Node.CompensationAction != ""`), hoisted to `RootCompensations` on scope close (ADR-0013). Today
those records are run **only** by the admin `CompensateRequested` trigger (reverse-order walk via
`StatusCompensating` + `compensationCursor`, finishing with `StatusTerminated` for a full rollback).

On the two terminal paths that matter most for sagas — an **unhandled error** (`propagateError`
terminal: `StatusFailed` + `FailInstance`) and a **cancel** (`CancelRequested`: `StatusTerminated` +
`FailInstance{cancelled}`) — the engine **drops** the compensation records and terminates without
undoing completed work. This track makes both paths **run the compensation walk before
terminating**, so completed compensable activities are rolled back on failure/cancel.

**In scope (engine only; no model change):**
- Route the terminal **unhandled-error** path and the **cancel** path through the existing
  compensation walk when `RootCompensations` is non-empty; preserve exact current behaviour when it
  is empty.
- Parametrize the walk's terminal outcome (the walk currently hard-codes `StatusTerminated` with no
  `FailInstance`): error ⇒ `StatusFailed` + `FailInstance{errorCode}`; cancel ⇒ `StatusTerminated` +
  `FailInstance{"cancelled"}`; admin full-rollback ⇒ unchanged (`StatusTerminated`, no `FailInstance`).
- Make compensation **best-effort**: if a compensation action itself fails during the walk, log and
  advance to the next record rather than stranding the instance.

**Out of scope (separate tracks):** scope-targeted compensation / `Compensate` producer (ADR-0035);
per-node & definition cancel handlers (ADR-0036). Cancel-actions (`InvokeCancelAction`, ADR-0028)
are unchanged and still fire alongside.

## 2. Mechanism

### 2.1 Carry the terminal outcome on the cursor

`compensationCursor` (engine/state.go) currently holds `ScopeID, ToNode, NextIndex, ActiveCmdID`.
Add the terminal outcome the finish step must apply:

```go
type compensationCursor struct {
    ScopeID     string
    ToNode      string
    NextIndex   int
    ActiveCmdID string
    // Terminal outcome applied by stepCompensationFinish on a full walk (ToNode==""):
    FinalStatus Status // StatusTerminated (admin/cancel) or StatusFailed (error); zero ⇒ Terminated (back-compat)
    FinalErr    string // when non-empty, finish emits FailInstance{Err: FinalErr}
}
```

- `cloneState` must copy the two new fields (they are value types — a struct copy already covers
  them since `Compensating` is a value field; verify `cloneState` copies `Compensating` by value).
- Persistence: `compensationCursor` is part of `InstanceState` JSONB; the additive fields serialize
  automatically (encoding/json). A persisted in-flight compensation from before the change
  deserializes with `FinalStatus=0` ⇒ Terminated, matching the prior admin behaviour. No migration.

### 2.2 `stepCompensationFinish` honours the outcome

On the full-rollback branch (`toNode == ""`), instead of hard-coding `StatusTerminated` with no
command:
```go
if toNode == "" {
    s.Status = cur.FinalStatus            // default StatusTerminated when zero
    if s.Status == 0 { s.Status = StatusTerminated }
    ended := at; s.EndedAt = &ended
    var cmds []Command
    if cur.FinalErr != "" { cmds = append(cmds, FailInstance{Err: cur.FinalErr}) }
    cmds = append(cmds, s.cancelAllTimers()...)             // idempotent; walk already cancelled most
    cmds = append(cmds, s.cancelAllArmsAndBoundaries()...)
    s.Compensating = compensationCursor{}
    return StepResult{State: *s, Commands: cmds}, nil
}
```
(`stepCompensationFinish` gains access to the cursor's `FinalStatus`/`FinalErr` — read them from
`s.Compensating` before clearing it. The partial-rollback `toNode != ""` branch is unchanged —
admin partial rollback resumes at `toNode`.)

The admin `CompensateRequested` path keeps `FinalStatus`/`FinalErr` zero ⇒ identical behaviour
(`StatusTerminated`, no `FailInstance`).

### 2.3 A shared initiator

Extract `beginCompensation(def, s, finalStatus Status, finalErr string, at, mode)` from the body of
`stepCompensateRequested` (the token-cancel pre-commands + record lookup + first `InvokeAction` +
cursor set, now also stamping `FinalStatus`/`FinalErr`). `stepCompensateRequested` calls it with
`(0, "", …)` (admin). The error/cancel paths call it with their outcome. If there are **no**
records, `beginCompensation` returns the same finish-immediately result (which now applies the
terminal outcome) — so an empty-records cancel/error still terminates with the right status +
`FailInstance`, exactly as today.

### 2.4 Wire the two terminal paths

- **CancelRequested** (step.go:118-154): keep emitting `InvokeCancelAction` (fire-and-forget, ADR-0028).
  Then, **if `len(RootCompensations) > 0`**, call `beginCompensation(def, s, StatusTerminated,
  "cancelled", …)` and return its result (prepending the `InvokeCancelAction` cmds + token clears).
  Else: current behaviour verbatim (clear tokens, `StatusTerminated`, `FailInstance{cancelled}`,
  cancel timers/arms). (`beginCompensation` itself clears tokens, so avoid double-clearing — route
  token-cancel through `beginCompensation` in the compensating branch.)
- **propagateError terminal-unhandled** (step.go:~1995): **if `len(RootCompensations) > 0`**, call
  `beginCompensation(def, s, StatusFailed, errorCode, …)`. Else: current behaviour
  (`StatusFailed`, `FailInstance{errorCode}`, cancel timers/arms). Only the terminal-unhandled branch
  changes — retry, catch-flow, error-boundary, and incident branches are untouched (they are not
  terminal failures).

### 2.5 Compensation-action failure is best-effort

When `Status == StatusCompensating` and an `ActionFailed` arrives whose `CommandID ==
Compensating.ActiveCmdID`, route it to **advance** (skip the failed record, continue the walk) — a
failed compensation must not re-enter `propagateError`/retry or strand the instance. Add this
dispatch alongside the existing `ActionCompleted`→`stepCompensationAdvance`. The skipped failure is
surfaced via the existing event/journal (and a `slog` line at the runtime edge if available; the
engine stays pure — it just routes to advance). Saga semantics: compensation is best-effort.

## 3. Determinism & purity

`Step` stays pure and deterministic: the cursor extension is data, the routing is a function of
`(state, trigger)`, and compensation `InvokeAction` inputs come from the stored `CompensationRecord`
snapshots. No clock/random in `Step`. `cloneState` copies the new fields. The change adds **no model
types** and no transport/storage/vendor imports.

## 4. Testing strategy (engine black-box `engine_test`, table-driven, assert-closure)

- **cancel with compensation:** start → svc(compensable "refund") completes → user task parked →
  `CancelRequested` → asserts: `StatusCompensating` then, after the compensation `ActionCompleted`,
  `StatusTerminated` + `FailInstance{cancelled}`; the refund compensation `InvokeAction` was emitted.
- **error with compensation:** completed compensable node then an unhandled failing node →
  compensation runs → `StatusFailed` + `FailInstance{errorCode}` after the walk.
- **empty records unchanged:** cancel/error with no compensable completed nodes → immediate
  `StatusTerminated`/`StatusFailed` + `FailInstance` (byte-for-byte current behaviour; existing
  cancel/error tests stay green).
- **admin CompensateRequested unchanged:** existing tests (full rollback → Terminated, partial →
  resume at ToNode) stay green (FinalStatus/FinalErr zero).
- **best-effort comp-action failure:** two compensable nodes; the first-compensated action fails →
  the walk still runs the second and reaches the terminal outcome (no strand, no propagateError).
- **multi-step walk + determinism:** same `(state, trigger)` ⇒ same commands; `cloneState` test
  extended for `Compensating.FinalStatus/FinalErr`.
- **runtime** (`runtime_test`): an e2e (MemStore) — a process with a compensable service task that
  is cancelled mid-flight runs the compensation action then terminates.

**Gate:** `go test -race -p 1 ./...` green; ≥85% on `engine` + `runtime`; `golangci-lint` clean;
**engine import-purity intact** (no new imports); model production diff ZERO; determinism/`cloneState`
tests pass.

## 5. ADR

| ADR | Decision |
|---|---|
| **0034** | On an unhandled terminal error and on cancel, run the existing reverse-order compensation walk before terminating (when `RootCompensations` is non-empty), via a shared `beginCompensation` initiator; the walk's terminal outcome is parametrized on the `compensationCursor` (`FinalStatus`+`FinalErr`) — error⇒Failed+`FailInstance{errorCode}`, cancel⇒Terminated+`FailInstance{cancelled}`, admin full-rollback⇒unchanged. Compensation-action failure during the walk is best-effort (skip+advance, never re-enter propagateError). Empty-records paths and the admin path are byte-for-byte unchanged. Engine-only; `Step` pure/deterministic; `cloneState` extended; no model change, no migration. |
