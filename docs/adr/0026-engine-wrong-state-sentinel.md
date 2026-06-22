# 26. Engine-level wrong-state sentinel via error wrapping

- Status: Accepted
- Date: 2026-06-22

## Context

The engine (`engine/step.go`) returns `engine.ErrTokenNotFound` for **every** wrong-state
trigger: an action result, human-task claim/complete/reassign, message, or signal that arrives
for a token that is not awaiting it. All seven handlers (`ActionCompleted`, `ActionFailed`,
`HumanClaimed`, `HumanReassigned`, `HumanCompleted`, `MessageReceived`, `SignalReceived`) emit
`fmt.Errorf("%w: %q", ErrTokenNotFound, id)` — a single, unclassified sentinel.

The only typed wrong-state classification before this change lived at the **service seam**:
`service.ErrConflict` was produced by *pre-flight* state checks (`isTerminal(status)` and
`!task.IsOpen()`) run **before** calling the runner. This left two gaps:

1. **Direct engine/runtime consumers** — callers that embed the engine without the `service`
   layer — receive an error literally named "not found". There is no typed way to distinguish
   "the token exists but is in the wrong state" from "the token never existed", which invites
   incorrect HTTP 404 mapping. `ErrTokenNotFound` carries no semantic "this is a conflict"
   signal that a transport classifier can act on via `errors.Is`.

2. **Races that escape the pre-flight guard** — e.g. two concurrent completions of the same
   task, or a signal arriving as an instance transitions to terminal. The second operation
   passes the pre-flight check (the task appears `Open`), reaches the engine, and the returned
   `ErrTokenNotFound` falls through every transport classifier to **HTTP 500 /
   `codes.Internal`**, instead of the semantically correct 422 / `FailedPrecondition`.

The broader error taxonomy at this point:

- `engine.ErrTokenNotFound` — wrong-state (all seven handlers above).
- `engine.ErrNoMatchingFlow` — gateway has no matching/default outgoing flow;
  definition/data failure, not a caller wrong-state error.
- `engine.ErrUnknownTrigger` — unsupported trigger type; infrastructure/programming error.
- `runtime.ErrInstanceNotFound` (→ 404), `runtime.ErrConcurrentUpdate` (→ 409 / `Aborted`),
  `runtime.ErrDefinitionNotFound`, `runtime.ErrBadCursor` — unchanged.
- `service.ErrConflict` (→ 422 `conflict_state` / `FailedPrecondition`) — pre-flight only.

Separately, no consistent convention existed for error-message prefixes; the same package
appeared as `"engine: …"`, `"service: …"`, `"postgres: …"` etc., making grep and log
filtering ambiguous when multiple packages share a short prefix.

## Decision

### 1. Parent sentinel via wrapping

We introduce `engine.ErrInvalidTransition` as a parent sentinel and make `ErrTokenNotFound`
wrap it at declaration time:

```go
var ErrInvalidTransition = errors.New("workflow-engine: invalid state transition")

var ErrTokenNotFound = fmt.Errorf("workflow-engine: no token awaiting command: %w", ErrInvalidTransition)
```

Because `ErrTokenNotFound` itself wraps `ErrInvalidTransition`, **every existing call site
is untouched**: `fmt.Errorf("%w: %q", ErrTokenNotFound, id)` already chains through, so
`errors.Is(err, engine.ErrInvalidTransition)` holds at all seven handlers without touching
`engine/step.go`. `Step`'s computed `(state, commands)` output is byte-identical; only the
returned error value gains one link in its chain. The engine purity invariants (stdlib-only,
no vendor imports, deterministic, pure function) are preserved.

`ErrNoMatchingFlow` and `ErrUnknownTrigger` do **not** wrap `ErrInvalidTransition`. They
represent definition/data failure and infrastructure error respectively; mapping them to
422 / `FailedPrecondition` would misrepresent their severity to callers. They continue to
fall through to 500 / `codes.Internal`.

The three sentinel variables are relocated from `engine/step.go` to a dedicated
`engine/errors.go`, paired with `engine/errors_test.go` that asserts the wrapping graph:
`errors.Is(ErrTokenNotFound, ErrInvalidTransition)` is true; the same check against
`ErrNoMatchingFlow` and `ErrUnknownTrigger` is false.

### 2. Service classification (race-gap closure)

In `service/service.go`, the shared helper `deliverTaskTrigger` (called by `ClaimTask`,
`CompleteTask`, and `ReassignTask`) classifies an engine wrong-state error that escapes the
pre-flight guard using double `%w` multi-wrap (Go 1.20+):

```go
if errors.Is(err, engine.ErrInvalidTransition) {
    return zero, fmt.Errorf("%w: %w", ErrConflict, err)
}
```

This preserves both `errors.Is(err, service.ErrConflict)` and
`errors.Is(err, engine.ErrInvalidTransition)` through the chain — single `%w` / `%v` would
lose the cause. The classification complements, not replaces, the existing pre-flight guards.

This classification is **deliberately not** applied in `DeliverSignal` or `DeliverMessage`:

- `SignalReceived` uses broadcast semantics — a signal matching no awaiting token is a clean
  no-op, never an error.
- `DeliverMessage` routes via the runner's waiter table and returns `nil` when no instance
  is waiting.

Adding classification on those paths would be unreachable dead code. If a future engine
change makes signal/message delivery error on no-match, classification can be added then
(YAGNI).

### 3. Transport fallback for bare-runner consumers

Both transport classifiers (see ADR-0011 for the consumer-mounted-transport model) gain a
direct `engine.ErrInvalidTransition` case, so a consumer that mounts a transport over a bare
runner without the `service` facade still receives correct codes:

- `transport/rest` `classifyError`: → **HTTP 422**, error code `"conflict_state"`.
- `transport/grpc` `mapToGRPCStatus`: → **`codes.FailedPrecondition`**.

Cases are ordered so they do not shadow the more-specific `runtime.ErrConcurrentUpdate`
(409 / `Aborted`) or the `*NotFound` cases.

### 4. `workflow-` error-message prefix convention

We adopt the convention that every production error message prefixes its package-name segment
with `workflow-` — e.g. `"service: conflicting state"` → `"workflow-service: conflicting
state"`, `"engine: …"` → `"workflow-engine: …"`, `"postgres: …"` → `"workflow-postgres: …"`.

The sweep covers ~188 sites across ~13 packages (postgres ×57, engine ×41, runtime ×32,
model ×20, service ×17, casbin ×9, expreval ×7, casbinauthz ×3, eventing ×2, humantask ×1,
authz ×1). Non-package wrapping words that appear in test fixtures (`wrap:`, `outer:`,
`inner:`, `poison:`, etc.) are not package-name sentinels and are left unchanged. Assertions
that use `errors.Is` are unaffected; test files that string-match on `.Error()` or
`ErrorContains` are updated in the same change.

The new sentinels introduced here (`ErrInvalidTransition`, `ErrTokenNotFound`) already carry
the `"workflow-engine: …"` prefix and serve as the canonical example.

## Consequences

**Easier / better:**

- Direct engine/runtime consumers can classify a wrong-state trigger with
  `errors.Is(err, engine.ErrInvalidTransition)` without depending on the `service` layer.
  The misleading "not found" semantics for an existing-but-wrong-state instance are removed.
- The service-layer race gap is closed: a trigger that passes the pre-flight check but loses
  a concurrent race maps to 422 `conflict_state` / `codes.FailedPrecondition` rather than
  500 / `codes.Internal`.
- The transport fallback ensures correctness for consumers using a bare runner (ADR-0011).
- The `workflow-` prefix makes log filtering and grepping unambiguous across the module's
  packages.

**Harder / trade-offs:**

- Every `ErrTokenNotFound` error now has one additional link in its wrapping chain.
  The overhead is negligible; error allocation occurs only on the failure path.
- The `workflow-` prefix sweep is a one-time mechanical cost (~188 sites). String-matching
  tests that check `.Error()` output must be updated; tests using `errors.Is` are unaffected.
- `DeliverSignal` and `DeliverMessage` do not classify `ErrInvalidTransition`. If a future
  engine change introduces a wrong-state error on those paths, a follow-up classification
  branch will be needed.
- `ErrNoMatchingFlow` and `ErrUnknownTrigger` remain 500 / `codes.Internal`. Consumers must
  not conflate them with wrong-state transitions.
