# 24. Durable async call activity via a call-link store and notifier

- Status: Accepted
- Date: 2026-06-22

## Context

A call-activity node starts a child process instance and parks the parent token
until the child finishes. The reference runtime ran the child **synchronously**:
`perform(StartSubInstance)` called `r.Run(child)` and translated the child's terminal
status into a `SubInstanceCompleted`/`SubInstanceFailed` trigger within the same
`deliverLoop`. A child that **parks** (its own human task, timer, signal, or nested
call activity) could not be re-entered synchronously, so the runtime returned a hard
error ("the synchronous runner does not support children that wait on human tasks,
timers, or events; async call activity is a future enhancement"). This is engine-core
tracked follow-up #3.

The mapping established a decisive fact: **the engine core is already async-ready.**
`engine.StartSubInstance`, `engine.SubInstanceCompleted{CommandID,Output}`,
`engine.SubInstanceFailed{CommandID,Err}`, the parent token park
(`State=TokenWaitingCommand, AwaitCommand=CommandID`), and the resume logic
(`engine/step.go:514–539`, matching `tokenAwaiting(CommandID)`) all exist and work.
The only thing missing is **delivering the resume trigger later, durably, and
crash-safely**, instead of synchronously from `perform`. So true async call activity
is a **runtime + persistence** change; `engine`/`model` are untouched.

The parent-resume must be (a) **crash-safe** — a parent that has parked on a call
activity must always have a durable path to resume, even if the runtime crashes
between the child finishing and the parent being notified — and (b) **decoupled**
from the parent's availability, because the child may finish long after the parent
parked, in a different `deliverLoop`. The team chose a durable, outbox-style
notification backed by a persistence correlation table (over an inline single-process
notify, and over encoding the parent in the child id or adding parent fields to the
pure `InstanceState`).

## Decision

Add a durable, opt-in async call-activity mechanism in the runtime + persistence
layers, leaving the engine untouched.

- **Correlation table `wrkflw_call_links`** — one row per child instance
  (`child_instance_id` PK; `parent_instance_id`, `parent_command_id`,
  `parent_def_id`/`parent_def_version`, `depth`, `status`
  running|completed|failed|notified, `output` JSONB, `error`, timestamps). It is both
  the parent↔child link and the durable parent-notification queue. The parent↔child
  correlation lives here, NOT as fields on `engine.InstanceState` (the pure engine
  type stays free of runtime metadata).
- **`runtime.CallLinkStore` port** (`ClaimPending`/`MarkNotified`/`LookupChild`) with
  a `MemCallLinkStore` (in-memory/test) and a Postgres implementation behind the
  `persistence` façade.
- **`perform(StartSubInstance)` becomes non-blocking:** resolve the child def, derive
  the child id (existing `<parent>-sub-c<N>` scheme), compute call-chain `depth`
  (reject `depth > maxCallDepth` — the renamed `maxCallActivityDepth` guard, now
  guarding self-referential definitions that async would otherwise spawn unboundedly),
  start the child's first burst, and **return `nil`** — no synchronous resume; the
  parent stays parked.
- **`deliverLoop` child-terminal hook:** on a transition into a terminal status, the
  runtime records the child's outcome so the call-link flips to `completed`/`failed`
  with the child's output/error.
- **`CallNotifier` driver** (relay-shaped): claims terminal-but-unnotified links
  (`FOR UPDATE SKIP LOCKED`), resolves the parent def via the `DefinitionRegistry`,
  delivers `SubInstanceCompleted{ParentCommandID,Output}` / `SubInstanceFailed{…}` to
  the parent via a `DeliverFunc`, and marks `notified`. It reuses the relay's per-row
  isolation + capped backoff (ADR-0017) and an optional LISTEN/NOTIFY wakeup
  (ADR-0022). **Idempotent:** a duplicate delivery finds the parent token already
  consumed (`ErrTokenNotFound`) and is treated as success.
- **Opt-in:** `runtime.NewRunner(..., WithCallLinks(store))`. Absent it,
  `perform(StartSubInstance)` keeps the synchronous behavior (a parking child still
  errors) — existing consumers and tests are unaffected.

Crash-safety rests on atomic link side-effects on the transactional `Store`
(ADR-0025): the link is created in the same tx as the child's `Create`, and flipped
to terminal in the same tx as the child's terminal `Commit`.

## Consequences

**Easier:** a child that parks on a human task, timer, signal, or nested call activity
now works — the headline capability the synchronous runner could not provide. The
parent resumes durably and crash-safely; a notifier restart re-delivers any pending
notification (at-least-once + idempotent). The engine stays pure and untouched
(determinism, the sealed sets, and `Step` are unchanged), so the BPMN execution
semantics carry over verbatim. The mechanism reuses the relay's claim/backoff/
LISTEN-NOTIFY patterns, so it is familiar operationally. The feature is fully opt-in.

**Harder / trade-offs:** a new table, a `CallLinkStore` port + two impls, and a
second relay-shaped driver are added surface and operational moving parts. The child
is still driven by whichever runtime delivers its triggers — this makes the *parent
notification* durable, not the child's execution distributed across machines (a later
concern). Cancellation propagation (parent cancel → child terminate; orphaned-child
cleanup when the parent is already terminal) is out of scope; an orphaned child result
is dropped (the parent `Deliver` no-ops on `ErrTokenNotFound`). `maxCallDepth` is a
global guard in v1. `SKIP LOCKED` gives throughput, not strict per-parent ordering —
immaterial here since each child resumes an independent parent token.
