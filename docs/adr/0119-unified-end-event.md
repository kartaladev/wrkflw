# 119. Unified end event with force-termination

- Status: Accepted
- Date: 2026-07-10

Part of the BPMN2-alignment effort (umbrella spec
`docs/specs/2026-07-10-bpmn2-alignment-design.md`), which folds bespoke node
kinds into flags/modes on existing kinds to be more faithful to the BPMN2
metamodel while **reducing** engine surface area.

## Context

The engine carried a distinct `TerminateEndEvent` node kind
(`KindTerminateEndEvent`, `NewTerminateEnd`, wire name `terminateEndEvent`)
intended to terminate the whole process — including parallel branches — when
reached. But it had **no implementation**: it was not in the engine's
`nodeStrategies` map, so a token reaching it simply **parked as an unhandled
kind** (`engine/step.go`) and the instance stalled forever. Meanwhile the
normal `EndEvent` only consumed its own token; when all tokens drained the
instance completed.

Two problems compounded:

1. **A dead kind.** `terminateEndEvent` was authorable and serializable but
   did nothing useful — a trap for anyone who reached for it.
2. **No way to force a terminal outcome.** There was no way for one branch of a
   parallel fork to end the whole instance early, in either direction:
   neither a **successful** early halt (declare done, cancel the rest) nor an
   **abort** (something went wrong, tear everything down). BPMN's terminate-end
   is only ever an abort-flavoured halt; real processes want both.

In the BPMN2 metamodel a "terminate end" is not a separate element type — it is
an end event carrying a *terminate event definition*. Modelling it as a flag on
`EndEvent` is both more faithful and less machinery.

## Decision

Delete the `TerminateEndEvent` kind and fold its intent into `EndEvent` as an
optional force-termination mode with a **selectable terminal outcome**:

1. **`ForceTermination bool`, `TerminationReason string`, and
   `Outcome TerminationOutcome` on `EndEvent`**, set via
   `WithForceTermination(reason string, outcome TerminationOutcome) EndOption`.
   `TerminationOutcome` is a two-value enum:
   - **`OutcomeComplete`** (iota-zero) → the instance ends at
     `StatusCompleted` — a *successful* business halt that cancels remaining
     parallel work.
   - **`OutcomeAbort`** → the instance ends at `StatusTerminated` — an abort.

   Both terminal outcomes are selectable at authoring time, satisfying the
   requirement that a force-termination express success *or* failure.

2. **`NewEnd` gains an `EndOption` interface**, mirroring the other event kinds
   (`StartOption`/`CatchOption`/`BoundaryOption`). `NewEnd(id string, opts
   ...EndOption)` replaces the old `NewEnd(id string, name ...string)`; the
   name-only common case is served by extending `WithName` to also satisfy
   `EndOption`. The builder's `AddEndEvent(id string, opts ...event.EndOption)`
   is relaxed likewise, and `AddTerminateEndEvent` is removed.

3. **First real terminate implementation (engine).**
   `endEventStrategy.enter` detects a force-termination `EndEvent` and runs the
   `forceTerminate` helper, which mirrors the immediate-termination tail of
   `handleCancelRequested`: close every open visit, drop all tokens, reconcile
   open human tasks to Cancelled (`cancelOpenTasks`), emit the terminal command
   (`FailInstance{Err: reason}` for abort, `CompleteInstance{Result: vars}` for
   complete), then sweep timers, boundaries/arms, and event-sub-process arms
   (`cancelAllTimers`, `cancelAllArmsAndBoundaries`, `removeAllEventSubprocessArms`).
   It returns `halt=true` so `drive()` exits immediately (the instance is
   terminal and its tokens are gone), mirroring `errorEndEventStrategy`. An
   empty reason falls back to `"force-terminated"`.

   The sweep is **scope-agnostic**: a force-termination end ends the *whole*
   instance regardless of the end event's scope. A force-termination end firing
   inside a sub-process scope still terminates the entire instance; scoped
   (sub-process-local) force-termination is not yet modelled. This is a
   deliberate first cut — strictly better than the previous behaviour (park
   forever) — and is documented on the `forceTerminate` helper.

4. **Redundancy is a WARN, not an error.** Force-termination is only meaningful
   when a definition has multiple end events (or parallel branches) to cancel.
   On a single-end definition it is merely redundant, not wrong. Rather than a
   hard `Validate` error, the runtime logs an slog **WARN at registration time**
   (`RegisterDefinition`/`MustRegisterDefinition`, via a pure
   `forceTerminationWarnings` helper) for each force-termination end that is the
   only end event in its definition. Definitions with ≥2 end events warn about
   nothing.

5. **Clean wire break.** The library is unreleased, so there are no aliases or
   migrators: the `terminateEndEvent` wire name is deleted and unmarshalling it
   now errors (`unknown NodeKind name "terminateEndEvent"`). The three new
   fields are additive on `NodeWire` (`forceTermination`, `terminationReason`,
   `terminationOutcome`, all `omitempty`); `terminationOutcome` is only written
   when `ForceTermination` is set. Any stored definition using the old kind must
   be re-authored as `NewEnd(id, WithForceTermination(reason, OutcomeAbort))`
   (abort = the old terminate-end semantics).

## Consequences

- **One fewer node kind.** `TerminateEndEvent`, `KindTerminateEndEvent`,
  `NewTerminateEnd`, `AddTerminateEndEvent`, and the `terminateEndEvent` wire
  name are gone. End-node validation (`isEnd`) now covers only `KindEndEvent`
  and `KindErrorEndEvent`. Removing the middle iota constant is safe because the
  wire is name-based, not ordinal.
- **The engine can now terminate.** For the first time a token can end the whole
  instance early and deterministically cancel sibling parallel work, in either
  terminal direction — a capability the previous `TerminateEndEvent` advertised
  but never delivered.
- **Both terminal outcomes at authoring time.** `OutcomeComplete` for a
  successful early halt, `OutcomeAbort` for an abort. The only differences
  between them are the terminal status and the emitted instance-completion
  command (`CompleteInstance` vs `FailInstance`); the parallel-work sweep is
  identical.
- **Redundant single-end force-termination is caught softly** — a WARN at
  registration, so a consumer learns about it without a build failing. The
  detection helper is pure and unit-tested; the logging is a thin side effect on
  the registration entry points.
- **Known limitation (tracked):** sub-process-scope force-termination terminates
  the whole instance. Scoped termination can be modelled later without changing
  the authoring API (the `EndEvent` fields already carry the intent). Documented
  on `forceTerminate`.
- Example: `examples/scenarios/terminate_end` shows a parallel fork where one
  branch reaches `NewEnd("halt", WithForceTermination("fraud detected",
  OutcomeAbort))` and cancels the sibling branch's in-flight user task, plus a
  companion run using `OutcomeComplete`.
