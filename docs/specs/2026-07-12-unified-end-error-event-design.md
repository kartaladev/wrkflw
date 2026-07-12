# Unified end event: fold `ErrorEndEvent` into `EndEvent`

- Date: 2026-07-12
- Status: Approved (design) — implementation pending
- Related: ADR-0119 (unified terminate-end), ADR-0127 (this decision)

## Problem

The engine carries two distinct end-event node kinds:

- **`KindEndEvent`** (`EndEvent`) — normal process completion. Since ADR-0119 it
  also carries a force-termination mode (`ForceTermination bool`,
  `TerminationReason`, `Outcome`) set via `WithForceTermination`.
- **`KindErrorEndEvent`** (`ErrorEndEvent`) — a *separate kind* that throws a
  workflow error (`ErrorCode`) when reached; the error is caught by an enclosing
  boundary error event (instance may recover) or, if uncaught, fails the
  instance. Handled by a dedicated `errorEndEventStrategy`.

In the BPMN2 metamodel a "terminate end" and an "error end" are **not** separate
element types: both are an *End Event* carrying a different (optional) event
definition — `terminateEventDefinition` vs `errorEventDefinition`, mutually
exclusive, at most one per end event. ADR-0119 already began aligning to this by
folding terminate into `EndEvent`. `ErrorEndEvent` is the last standalone end
kind; folding it in completes the alignment and removes one node kind.

### The pivot: an explicit discriminator is mandatory

`ErrorEndEvent` uses its **type** as the discriminator today, and `ErrorCode ==
""` is a *valid* anonymous catch-all error end. Once folded into `EndEvent`,
"`ErrorCode` is non-empty" cannot mean "this is an error end" — a normal end and
an anonymous error end would be indistinguishable. The unified `EndEvent`
therefore needs an **explicit behavior discriminator**, not a nullable payload
field.

## Goals

- One end-event kind (`KindEndEvent`) covering all three behaviors: normal,
  terminate, error.
- BPMN error-end semantics preserved as the **minimal requirement**: throw a
  named (or catch-all) error, caught by a boundary error handler up the scope
  chain, else fail the instance. No behavioral change to throwing, catching,
  recovery, or the uncaught path.
- Strict parity with ADR-0119's shape: a single discriminator, a `WithXxx`
  option, deletion of the dedicated constructor and wire name.

## Non-goals (YAGNI)

- No new end event definitions (escalation, signal, message, cancel).
- No change to uncaught-error handling, recovery routing, or force-termination.
- No back-compat aliases or wire migrators — the library is unreleased.

## Decisions (locked during brainstorming)

1. **Unified end-definition enum** (not parallel bool flags). A single
   `EndBehavior` discriminator replaces `ForceTermination bool`. Mutual
   exclusivity of terminate vs error is *structural* — one field holds one
   value — so no cross-field validation guard is needed.
2. **`WithErrorCode` option**, mirroring `WithForceTermination`. Named for the
   existing `ErrorCode` field and the boundary `WithErrorCode` option.
3. **Remove the dedicated constructors** (`NewErrorEnd`, `AddErrorEndEvent`) for
   strict ADR-0119 parity. `NewEnd(id, WithErrorCode(code))` /
   `AddEndEvent(id, WithErrorCode(code))` is the single path.

## Design

### 1. Model (`definition/event/event.go`)

```go
// EndBehavior selects what an EndEvent does when a token reaches it — BPMN's
// (optional) end event definition: none | terminate | error, mutually exclusive.
type EndBehavior int

const (
    EndNormal    EndBehavior = iota // plain completion (no event definition)
    EndTerminate                    // force-terminate the instance (ADR-0119)
    EndError                        // throw a workflow error (BPMN error end event)
)

// String → "normal" / "terminate" / "error" (wire encoding + logging).

type EndEvent struct {
    model.Base
    Behavior          EndBehavior
    // EndTerminate payload (zero unless Behavior == EndTerminate):
    TerminationReason string
    Outcome           TerminationOutcome
    // EndError payload (ErrorCode "" == anonymous catch-all; only meaningful
    // when Behavior == EndError):
    ErrorCode         string
}
```

`ErrorEndEvent` struct and its `Kind()` method are **deleted**.

### 2. Options (`definition/event/options.go`)

- `WithForceTermination(reason, outcome)` — unchanged signature; now sets
  `Behavior = EndTerminate` plus payload.
- **New** `WithErrorCode(errorCode string) EndOption` — sets `Behavior =
  EndError`, `ErrorCode = errorCode`.
- Applying both is author error; **last option wins** (standard functional-option
  semantics). Documented on both options. No guard.

### 3. Constructors / builder

- `NewErrorEnd` **deleted**; `NewEnd(id, WithErrorCode(code))` is the only path.
- `AddErrorEndEvent` **deleted** from `definition/build/build.go`;
  `AddEndEvent(id, WithErrorCode(code))` is the path.

### 4. Wire (`event.go` `RegisterKind`)

Retire the `forceTermination` bool wire field in favor of one name-based
discriminator (clean break, per ADR-0119 precedent):

- Add `NodeWire.EndBehavior string` (`omitempty`) — `"terminate"` | `"error"`;
  absent ⇒ normal.
- `KindEndEvent` `ToWire`: write `EndBehavior` (only when non-normal); terminate
  writes `terminationReason` / `terminationOutcome`; error writes `errorCode`.
- `KindEndEvent` `FromWire`: switch on `EndBehavior` → reconstruct `Behavior` +
  payload.
- The `RegisterKind(model.KindErrorEndEvent, …)` block is **deleted**;
  unmarshalling `"errorEndEvent"` now errors (`unknown NodeKind name`).
- `model.KindErrorEndEvent` iota constant **removed** — safe because the wire is
  name-based, not ordinal.

### 5. Engine (`engine/step_nodes.go`, `engine/step.go`)

`endEventStrategy.enter` switches on `ev.Behavior`:

- `EndTerminate` → existing `forceTerminate(c, ev)`.
- `EndError` → the **verbatim** body of today's `errorEndEventStrategy`:
  consume the token, `propagateError(c.def, c.s, currentScopeID, "", "",
  ev.ErrorCode, nil, c.at, c.mode, c.eval, false)`, return `halt=true`. Caught /
  recovered / uncaught-fails-instance behavior is unchanged.
- default `EndNormal` → existing per-scope completion logic.

`errorEndEventStrategy` and its `nodeStrategies[model.KindErrorEndEvent]` entry
are **deleted**. Comments naming `ErrorEndEvent` as the halting kind
(`step_nodes.go:42`, `step.go:151`, `step_errors.go`) are updated to name the
error-behavior `EndEvent` branch.

### 6. Validation (`definition/model/validate.go`)

`isEnd := n.Kind() == KindEndEvent || n.Kind() == KindErrorEndEvent` becomes
`isEnd := n.Kind() == KindEndEvent`. The dead-end / no-outgoing-flow rules for
end events are otherwise unchanged.

### 7. Docs

- `definition/README.md`: drop the `KindErrorEndEvent` row; update the
  `KindEndEvent` row to list `WithErrorCode`.
- ADR-0127 (Nygard template).

## Implementation approach — parity-first

Retiring a live kind must not lose coverage. Order:

1. Port every `NewErrorEnd(id, code)` call site (~9 files, mostly tests) to
   `NewEnd(id, WithErrorCode(code))`, and every `ErrorEndEvent{…}` /
   `KindErrorEndEvent` assertion to the new shape.
2. Get the whole suite green through the new API.
3. **Then** delete `ErrorEndEvent`, `KindErrorEndEvent`, `NewErrorEnd`,
   `AddErrorEndEvent`, `errorEndEventStrategy`, and the `errorEndEvent` wire
   registration.

TDD strict: `EndBehavior` + `String`, `WithErrorCode`, the wire round-trip for
the error behavior, and the engine error-branch each get a failing (RED) test
before implementation. Existing error-end drive/boundary tests are the
regression net for the engine branch.

## Affected files

- `definition/event/event.go`, `definition/event/options.go`
- `definition/model/definition.go` (kind constant), `definition/model/validate.go`
- `definition/build/build.go`
- `engine/step_nodes.go`, `engine/step.go`, `engine/step_errors.go` (comments)
- `definition/README.md`
- Tests: `definition/event/event_test.go`, `definition/model/{node,accessors,nodekind_json}_test.go`,
  `definition/build/build_test.go`, `engine/{step_errors,step_errorend_drive,step_nodes,boundary_error_matching,step_boundaries_action,reminder_interrupt}_test.go`,
  `internal/persistence/store/definitions_conformance_test.go`

## Verification checklist

- [ ] `go test ./...` green (parity port complete before any deletion).
- [ ] New RED-first tests exist for `EndBehavior.String`, `WithErrorCode`, wire
      round-trip (error), engine error-branch.
- [ ] `go test -race` + coverage ≥ 85% on `definition/event`, `engine`.
- [ ] `golangci-lint run ./...` clean.
- [ ] No remaining references to `ErrorEndEvent`, `KindErrorEndEvent`,
      `NewErrorEnd`, `AddErrorEndEvent`, `errorEndEvent` (wire), `errorEndEventStrategy`.
- [ ] `/code-review` (whole branch) + `/security-review` clean.
