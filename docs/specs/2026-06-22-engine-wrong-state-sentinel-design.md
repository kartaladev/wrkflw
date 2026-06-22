# Engine-level wrong-state sentinel + `workflow-` error-prefix sweep — design

**Date:** 2026-06-22
**Status:** Proposed (awaiting user approval)
**Track:** Correctness / robustness (consolidated-backlog top pick #1)
**ADR:** 0026 (Engine-level wrong-state sentinel via error wrapping)

## Context

The engine returns `engine.ErrTokenNotFound` for **every** wrong-state trigger case — a
signal/message/action-result/human trigger that arrives for a token that is **not awaiting**
it (the instance still exists; the token has already moved on, the task is already closed, or
the trigger lost a race). Concretely, `engine/step.go` returns
`fmt.Errorf("%w: %q", ErrTokenNotFound, id)` from the `ActionCompleted`, `ActionFailed`,
`HumanClaimed`, `HumanReassigned`, `HumanCompleted`, `MessageReceived`, and `SignalReceived`
handlers.

Today the only typed classification of "wrong state" lives at the **`service` seam**:
`service.ErrConflict` is produced by *pre-flight* state checks (`isTerminal(status)` and
`!task.IsOpen()`) **before** calling the runner. It does **not** catch engine errors. Two gaps
follow:

1. **Direct engine/runtime consumers** (embedding the engine without the `service` layer) get an
   error literally named "not found", which invites a wrong **404** mapping even though the
   instance exists and the correct semantics is **conflict / 422**. There is no typed way to
   classify it.
2. **A race that slips past the `service` pre-flight check** — e.g. two concurrent completions of
   the same task, or a signal arriving as an instance finishes — reaches the engine, comes back as
   `ErrTokenNotFound`, and falls through the transport classifiers to **HTTP 500 / `codes.Internal`**
   instead of 422 / `FailedPrecondition`.

This track closes both gaps with a typed, purity-preserving engine sentinel, and adopts a new
project-wide error-message convention along the way.

### Current error taxonomy (verified)

- `engine.ErrTokenNotFound` — "no token awaiting command" → **all** wrong-state cases above.
- `engine.ErrNoMatchingFlow` — gateway has no matching/default outgoing flow. **Definition/data
  failure, not caller wrong-state.** Stays a hard error.
- `engine.ErrUnknownTrigger` — unsupported trigger type. **Infrastructure/programming error.**
  Stays a hard error.
- `runtime.ErrInstanceNotFound` (→ 404), `runtime.ErrConcurrentUpdate` (→ 409 / `Aborted`),
  `runtime.ErrDefinitionNotFound`, `runtime.ErrBadCursor` — unchanged.
- `service.ErrConflict` (→ 422 `conflict_state` / `FailedPrecondition`) — produced only by
  pre-flight checks today.

## Goals

1. Give **direct engine/runtime consumers** a typed sentinel to classify wrong-state transitions
   via `errors.Is`.
2. Close the **service-layer race gap** so a wrong-state error that escapes pre-flight maps to
   422 / `FailedPrecondition`, not 500 / `Internal`.
3. Keep the engine **pure and behavior-identical** — no change to `Step`'s `(state, commands)`
   output; only the error chain is enriched.
4. Adopt the **`workflow-` error-message prefix** convention repo-wide.

## Non-goals

- No new state guards inside `Step` (e.g. an explicit terminal-instance rejection). The engine
  already returns `ErrTokenNotFound` transitively for triggers to a finished instance (no awaiting
  tokens); adding guards would change engine behavior and is out of scope.
- `ErrNoMatchingFlow` and `ErrUnknownTrigger` are **not** reclassified as wrong-state.
- No change to `runtime.ErrConcurrentUpdate` semantics (optimistic-lock retry, 409 / `Aborted`) —
  it is distinct from wrong-state.

## Design

### 1. Core sentinel via wrapping (`engine`)

Introduce a **parent sentinel** that the existing `ErrTokenNotFound` wraps:

```go
// ErrInvalidTransition classifies a trigger that cannot be applied because the targeted
// instance/token is not in a state that accepts it. The instance exists — this is a
// conflict, not a "not found". Consumers classify with errors.Is(err, ErrInvalidTransition).
var ErrInvalidTransition = errors.New("workflow-engine: invalid state transition")

// ErrTokenNotFound is one kind of invalid transition: the targeted command/task token is
// not awaiting. It wraps ErrInvalidTransition so errors.Is holds for both sentinels.
var ErrTokenNotFound = fmt.Errorf("workflow-engine: no token awaiting command: %w", ErrInvalidTransition)
```

Because `ErrTokenNotFound` itself now wraps `ErrInvalidTransition`, **every existing call site is
untouched**: `fmt.Errorf("%w: %q", ErrTokenNotFound, id)` already chains through, so
`errors.Is(err, engine.ErrInvalidTransition)` holds at all seven handlers. `Step`'s computed
`(state, commands)` is unchanged; only the returned error value gains one link in its chain. The
engine purity invariants (stdlib-only, deterministic, pure) are preserved — no new imports, no
logic change.

`ErrNoMatchingFlow` and `ErrUnknownTrigger` remain standalone (they do **not** wrap
`ErrInvalidTransition`) and keep mapping to 500 / `Internal`.

### 2. File placement (cleanup honoring the 1:1 test convention)

Relocate the engine sentinel `var (...)` block from `engine/step.go` to a new
**`engine/errors.go`**, paired with **`engine/errors_test.go`**. This is a behavior-preserving
move (existing tests must still pass). `engine/errors_test.go` asserts the wrapping graph:

- `errors.Is(engine.ErrTokenNotFound, engine.ErrInvalidTransition)` is **true**.
- `errors.Is(engine.ErrNoMatchingFlow, engine.ErrInvalidTransition)` is **false**.
- `errors.Is(engine.ErrUnknownTrigger, engine.ErrInvalidTransition)` is **false**.

### 3. Service classification (closes the race gap)

In `service/service.go`, on the paths that call the runner (`DeliverSignal`, `DeliverMessage`,
`deliverTaskTrigger`), classify a leaked engine wrong-state error into the existing `ErrConflict`,
**after** the runner returns:

```go
if err != nil {
    if errors.Is(err, engine.ErrInvalidTransition) {
        return zero, fmt.Errorf("%w: %v", ErrConflict, err)
    }
    return zero, err
}
```

This complements — does not replace — the pre-flight `isTerminal`/`IsOpen` guards, covering the
concurrent-race path that escapes them. `ErrConflict` already wraps its cause, so
`errors.Is(err, service.ErrConflict)` continues to hold and the cause stays inspectable.

### 4. Transport fallback

Add a direct `engine.ErrInvalidTransition` case to both transport classifiers, so a consumer that
mounts a transport over a **bare runner** (without the `service` facade) still gets correct codes:

- `transport/rest` `classifyError`: → **HTTP 422**, error code `"conflict_state"`.
- `transport/grpc` `mapToGRPCStatus`: → **`codes.FailedPrecondition`**.

Ordering note: place the `engine.ErrInvalidTransition` / `service.ErrConflict` cases so they do not
shadow the more specific `runtime.ErrConcurrentUpdate` (409 / `Aborted`) and the `*NotFound` cases.
Since the sentinels are unrelated chains this is not a real conflict, but the test table will pin
each mapping explicitly.

### 5. `workflow-` error-prefix sweep (repo-wide)

Adopt the convention: **every production error message prefixes its package-name segment with
`workflow-`** — e.g. `"service: conflicting state"` → `"workflow-service: conflicting state"`,
`"engine: ..."` → `"workflow-engine: ..."`, `"postgres: ..."` → `"workflow-postgres: ..."`.

Scope of the sweep (verified counts of production, non-test error literals carrying a package
prefix): postgres ×57, engine ×41, runtime ×32, model ×20, service ×17, casbin ×9, expreval ×7,
casbinauthz ×3, eventing ×2, humantask ×1, authz ×1 — **~188 sites** across ~13 packages.

Rules for the sweep:

- Prefix **only genuine package-name segments** on production code. Non-package wrapping words
  seen in the inventory (`wrap:`, `outer:`, `inner:`, `poison:`, `deliver:`, `broker:`, `x:`) are
  test fixtures or mid-chain wrap labels — leave them unless they are a package-name sentinel.
- Assertions use `errors.Is`, so message-text changes are safe for those. The **~22 test files**
  that reference `.Error()` / `ErrorContains` / `Contains(... err ...)` are the risk surface: any
  that string-match on a changed prefix must be updated in the same change.
- This is a mechanical, behavior-preserving change verified by the full test suite going green.

### 6. ADR-0026

Record the decision in `docs/adr/0026-engine-wrong-state-sentinel.md` (Nygard template):
parent-sentinel-via-wrapping, why `ErrTokenNotFound` wraps it, the scope boundary (excludes
`ErrNoMatchingFlow` / `ErrUnknownTrigger`), the service/transport classification, and the
`workflow-` prefix convention.

## Error-flow summary (after this change)

```
engine.Step  → ErrTokenNotFound  (wraps engine.ErrInvalidTransition)
   ↓ runtime wraps: "workflow-runtime: step: %w"
runtime returns (errors.Is → ErrInvalidTransition still holds)
   ↓ service classifies: errors.Is(ErrInvalidTransition) ⇒ wrap as service.ErrConflict
   ↓ transport maps service.ErrConflict (or, bare-runner, engine.ErrInvalidTransition directly)
HTTP 422 "conflict_state"  /  gRPC codes.FailedPrecondition
```

## Testing strategy

- **engine** (`engine/errors_test.go`): the wrapping graph (§2); a behavioral test that one
  late/wrong-state trigger (e.g. `SignalReceived` for a non-awaiting token) returns an error
  satisfying both `errors.Is(err, ErrTokenNotFound)` and `errors.Is(err, ErrInvalidTransition)`.
- **service** (`service/errors_test.go`): a concurrent / post-pre-flight wrong-state race returns
  `service.ErrConflict` (e.g. two completions of the same task; the second escapes pre-flight and is
  classified from the engine error). Existing pre-flight `ErrConflict` tests stay green.
- **transport/rest** + **transport/grpc**: extend the mapping tables with an
  `engine.ErrInvalidTransition` row (→ 422 `conflict_state` / `FailedPrecondition`).
- **sweep**: whole-suite `go test -race ./...` green is the proof; no behavior changes expected.
- All tests black-box (`package <pkg>_test`), table-driven with the `assert`-closure form
  (project `table-test` skill), `t.Context()`, asserting via `errors.Is`.

## Verification gate

- `go test -race ./...` green (Postgres pkg with limited container parallelism, `-p 1`).
- Touched packages ≥ 85% line coverage.
- `golangci-lint run ./...` clean.
- Engine/model purity intact: no transport/storage/bus/time-vendor imports added; `Step` output
  unchanged.

## Risks & mitigations

- **String-matching tests break under the sweep** → grep the ~22 `.Error()`/`Contains` test files
  first; convert to `errors.Is` where they match a sentinel, or update the expected substring.
- **A non-package prefix accidentally rewritten** → the sweep prefixes only package-name segments;
  reviewer checks the diff for `wrap:`/`outer:`/etc. false positives.
- **Transport classifier ordering** → explicit per-sentinel test rows pin each mapping.
```
