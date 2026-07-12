# 127. Unified end event with error behavior

- Status: Accepted
- Date: 2026-07-12

Continues the BPMN2-alignment approach of folding bespoke node kinds into
modes on existing kinds (ADR-0119 did this for terminate-end). Design spec:
`docs/specs/2026-07-12-unified-end-error-event-design.md`.

## Context

After ADR-0119 the engine still carried two distinct end-event node kinds:

- `KindEndEvent` (`EndEvent`) — normal completion, plus an optional
  force-termination mode (`ForceTermination bool`, `TerminationReason`,
  `Outcome`).
- `KindErrorEndEvent` (`ErrorEndEvent`) — a separate kind that throws a workflow
  error (`ErrorCode`) when reached, caught by an enclosing boundary error event
  (the instance may recover) or, if uncaught, fails the instance. Handled by a
  dedicated `errorEndEventStrategy`.

In the BPMN2 metamodel neither "terminate end" nor "error end" is a separate
element type: both are an *End Event* carrying a different, mutually exclusive,
optional event definition (`terminateEventDefinition` vs `errorEventDefinition`
— at most one per end event). ADR-0119 aligned terminate to this; `ErrorEndEvent`
is the last standalone end kind. Folding it in completes the alignment and drops
one node kind.

The pivot that shapes the decision: `ErrorEndEvent` uses its **type** as the
discriminator today, and `ErrorCode == ""` is a *valid* anonymous catch-all error
end. Once error is folded into `EndEvent`, "`ErrorCode` is non-empty" cannot mean
"this is an error end" — a normal end and an anonymous error end would be
indistinguishable. The unified `EndEvent` therefore requires an **explicit
behavior discriminator**, not a nullable payload field. This also subsumes
ADR-0119's `ForceTermination bool`: a single discriminator is more faithful (BPMN
allows at most one event definition) and makes terminate-vs-error mutual
exclusivity structural rather than validated.

## Decision

Delete the `ErrorEndEvent` kind and fold its intent into `EndEvent` as one value
of an explicit end-behavior discriminator that also absorbs force-termination.

1. **`EndBehavior` discriminator on `EndEvent`**, replacing `ForceTermination
   bool`. A three-value enum:
   - **`EndNormal`** (iota-zero) — plain completion (BPMN: no event definition).
   - **`EndTerminate`** — force-terminate the instance (carries
     `TerminationReason` + `Outcome`, ADR-0119 semantics).
   - **`EndError`** — throw a workflow error (carries `ErrorCode`; `""` =
     anonymous catch-all).

   `String()` yields `"normal"`/`"terminate"`/`"error"` for wire and logging.
   Because `Behavior` holds exactly one value, terminate and error cannot
   coexist — the mutual exclusivity is structural, needing no validation guard.

2. **`WithErrorCode(errorCode string) EndOption`**, mirroring
   `WithForceTermination`. Named for the existing `ErrorCode` field and the
   boundary `WithErrorCode` option. It sets `Behavior = EndError`.
   `WithForceTermination` keeps its signature and now sets `Behavior =
   EndTerminate`. Applying both to one end event is author error resolved by
   last-option-wins (standard functional-option semantics), documented on both
   options.

3. **Dedicated constructors removed** (strict ADR-0119 parity). `NewErrorEnd` and
   the builder's `AddErrorEndEvent` are deleted; `NewEnd(id,
   WithErrorCode(code))` and `AddEndEvent(id, WithErrorCode(code))` are the
   single authoring path — just as terminate-end is authored via
   `WithForceTermination`.

4. **Engine dispatch folded.** `endEventStrategy.enter` switches on `Behavior`:
   `EndTerminate` → the existing `forceTerminate` helper; `EndError` → the
   **verbatim** body of the former `errorEndEventStrategy` (consume the token,
   `propagateError(..., ErrorCode, ...)`, return `halt=true`); `EndNormal` → the
   existing per-scope completion logic. `errorEndEventStrategy` and its
   `nodeStrategies[KindErrorEndEvent]` entry are deleted. The caught / recovered
   / uncaught-fails-instance behavior — the BPMN error-end semantics — is
   unchanged; only its dispatch site moves.

5. **Clean wire break.** The library is unreleased, so no aliases or migrators.
   The `forceTermination` bool wire field is retired for a single name-based
   discriminator `endBehavior` (`"terminate"`/`"error"`, `omitempty`; absent ⇒
   normal); terminate still writes `terminationReason`/`terminationOutcome`,
   error writes `errorCode`. The `errorEndEvent` wire name is deleted and
   unmarshalling it now errors (`unknown NodeKind name "errorEndEvent"`). The
   `KindErrorEndEvent` iota constant is removed — safe because the wire is
   name-based, not ordinal. Any stored definition using the old kind must be
   re-authored as `NewEnd(id, WithErrorCode(code))`.

6. **Validation.** End-node validation (`isEnd`) now covers only `KindEndEvent`.

## Consequences

- **One fewer node kind.** `ErrorEndEvent`, `KindErrorEndEvent`, `NewErrorEnd`,
  `AddErrorEndEvent`, `errorEndEventStrategy`, and the `errorEndEvent` wire name
  are gone. `KindEndEvent` is now the sole end kind, carrying all three
  behaviors.
- **BPMN-faithful end model.** An `EndEvent` carries at most one event definition
  (none/terminate/error) via a single discriminator, matching the metamodel and
  subsuming ADR-0119's separate bool. Adding future end definitions (escalation,
  signal, …) is a new enum value, not a new kind.
- **Behavior preserved, not redesigned.** Error throwing, boundary catching,
  recovery routing, and the uncaught path are byte-for-byte the former
  `errorEndEventStrategy`; this ADR is a structural fold, not a semantic change.
- **Breaking API and wire.** `NewErrorEnd`/`AddErrorEndEvent` call sites migrate
  to `WithErrorCode`; the `errorEndEvent` wire name and `forceTermination` wire
  bool are removed. Acceptable — the library is unreleased.
- **Parity-first execution.** Existing error-end tests are ported to the new API
  and made green *before* the old kind is deleted, so coverage of the error-end
  behavior is never dropped mid-change.
- **Out of scope (YAGNI):** other BPMN end event definitions (escalation, signal,
  message, cancel) are not added; uncaught-error handling and force-termination
  are unchanged.
