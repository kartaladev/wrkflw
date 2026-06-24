# 57. Implement ReceiveTask as a message-receive node with host boundaries

- Status: Accepted
- Date: 2026-06-25

## Context

`model.KindReceiveTask` was never wired into the engine's node-strategy registry
(`nodeStrategies` in `engine/step_nodes.go`). At node entry it fell through to the
"unhandled node kinds" branch in `drive()` (`engine/step.go`), which parks the token
with `tok.State = TokenWaitingCommand` and nothing else. The consequence was that a
ReceiveTask:

- set **no** `AwaitMessage` / `AwaitMessageKey` on its token, so a matching
  `MessageReceived` trigger could never resume it (`handleMessageReceived` resumes the
  standalone parked-message token by looking it up via `tokenAwaitingMessage`, which only
  matches tokens that actually advertise an awaited message); and
- **never armed** any boundary events attached to the ReceiveTask host, so a timer/
  message boundary on a ReceiveTask was silently inert.

`model.ReceiveTask` already carries `MessageName` and `CorrelationKey`, mirroring the
message variant of `IntermediateCatchEvent`, which *is* implemented. ADR-0053 (message
boundary events) explicitly deferred ReceiveTask-host boundary support as a known
limitation. This left the BPMN ReceiveTask a park-only dead end.

## Decision

Implement ReceiveTask as a first-class message-receive node by registering a
`receiveTaskStrategy` and disarming its boundaries on resume. The change is confined to the
engine core and keeps `Step` pure and deterministic:

- **`receiveTaskStrategy` (`engine/step_nodes.go`)** — registered in `nodeStrategies` for
  `KindReceiveTask`. On entry it resolves `CorrelationKey` via the Step-scoped evaluator
  (`EvalString`, deterministic against instance variables, per ADR-0056), parks the token
  with `AwaitMessage = MessageName` and `AwaitMessageKey = resolvedKey` (mirroring the
  `IntermediateCatchEvent` message branch), and arms any host boundaries via the existing
  `armBoundaries` helper (mirroring `serviceTaskStrategy`). A bad correlation-key expression
  surfaces a wrapped `workflow-engine: receive task %q correlation key:` error rather than
  parking silently.
- **Boundary disarm on message-resume (`engine/step_triggers.go`)** — the standalone
  parked-message resume path in `handleMessageReceived` now disarms the resuming host
  token's boundary arms with the existing `removeBoundaryArmsForHost` idiom, prepending a
  `CancelTimer` for each removed arm to the returned commands. This makes the
  "message arrives before the boundary fires" race resolve cleanly: the host resumes and
  any leftover boundary arm (and its scheduled timer) is cancelled.
- The boundary-fires-first and instance-cancel paths already disarm host boundaries via
  `removeBoundaryArmsForHost` keyed on the host token, so they needed no change.
- Stale comments enumerating the intentionally-unhandled kinds in `engine/step.go` and
  `engine/step_nodes.go` were updated to drop `KindReceiveTask`.

## Consequences

- A ReceiveTask now genuinely awaits its message and resumes to completion on delivery,
  and boundaries attached to a ReceiveTask host arm, interrupt, and disarm symmetrically
  with ServiceTask/UserTask hosts. This **closes the ADR-0053 deferred limitation**.
- The change is **engine-only**; no transport, persistence, scheduling, or runtime
  production code is touched. `Step` remains a pure, deterministic function — correlation
  keys and timer durations evaluate through the injected evaluator, not the wall clock.
- `KindReceiveTask` moves from the intentionally-unhandled set into the arm-bearing
  registry (14 entries, was 13); the registry-invariant test was updated accordingly.
- `KindSendTask` remains an unimplemented fall-through — outbound-message emission is a
  separate concern and out of scope here.
