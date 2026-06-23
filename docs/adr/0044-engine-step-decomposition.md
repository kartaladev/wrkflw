# 44. Decompose engine/step.go via a node-kind strategy registry

- Status: Accepted
- Date: 2026-06-23

## Context

`engine/step.go` had grown to a 3251-line god file. It carried two large
dispatch switches plus all of the engine's cross-cutting machinery in one
place:

- `Step(def, st, trg, opt) (StepResult, error)` — a trigger type-switch with
  ~14 arms (StartInstance, ActionCompleted, CancelRequested, TimerFired with a
  nested SLA/InWait/Retry sub-dispatch, etc.).
- `drive(def, s, at, mode)` — a `switch node.Kind` with one arm per node kind
  driving token movement.
- The gateway fork/join algorithms, boundary and event-subprocess arming, the
  compensation walk, error propagation, the timer/SLA/reminder/retry handlers,
  and the state/token/var utility helpers.

This made the core state machine hard to navigate and review, and it stood
directly in front of sub-project ② (the `model.Node`-as-interface redesign),
whose engine migration would otherwise have to edit one monolith. We considered
keeping the `drive` switch and merely splitting helper files, but the node-kind
set is effectively closed and each arm is independent, so a strategy registry
expresses the dispatch more cleanly and lets ② change one small per-kind file at
a time.

## Decision

We decomposed `step.go` as a **behavior-preserving pure refactor**, sequenced
**before** ②:

- **Node-kind dispatch → strategy registry.** `drive()` is now a thin
  dispatcher over `var nodeStrategies map[model.NodeKind]nodeStrategy`. Each
  arm-bearing kind is a **stateless zero-size struct** implementing
  `nodeStrategy.enter(c *stepCtx, tok *Token, node model.Node) (cmds []Command,
  halt bool, err error)`. The registry is built once at package init and never
  mutated. Exactly the 13 kinds that had a `drive` arm are registered;
  the 7 intentionally-unhandled kinds (KindTerminateEndEvent, KindBusinessRuleTask,
  KindReceiveTask, KindSendTask, KindBoundaryEvent, KindEventSubProcess,
  KindUnspecified) keep falling through to the unchanged post-dispatch parking
  logic.
- **The `halt` return.** One arm (KindErrorEndEvent) originally did
  `return cmds, nil`, exiting `drive()` entirely rather than parking a token. A
  bare `tok.State` mutation cannot reproduce that, so the strategy contract
  carries an explicit `halt bool`; `drive()` returns immediately when a strategy
  halts. Only `errorEndEventStrategy` returns `halt=true`. This preserves the
  original semantics on the unhandled-error immediate-failure path, where a
  surviving parallel-sibling token must be abandoned with the instance `Failed`
  rather than driven further.
- **Trigger dispatch → extracted handlers.** `Step()` stays a thin
  trigger type-switch; each case body moved verbatim to a
  `handle<Trigger>(...)` function in `step_triggers.go`, taking plain params
  (`def, s, t, opt` as each body needs). The TimerFired SLA/InWait/Retry
  sub-dispatch is preserved unchanged.
- **`stepCtx` param-object.** `stepCtx{def, tdef, s, at, mode}` carries the
  repeated inputs to the node-dispatch layer (`tdef` is the per-token
  scope-resolved definition). It is intentionally scoped to that layer; trigger
  handlers use plain params instead of being force-fit into it.
- **Collaborator files.** The cross-cutting algorithms moved unchanged into
  topic files: `step_gateways.go`, `step_boundaries.go`,
  `step_eventsubprocess.go`, `step_compensation.go`, `step_errors.go`,
  `step_timers.go`, `step_state.go`, and `step_nodes.go` (the registry +
  strategies). Relocated helpers keep their current signatures.
- **Completeness test.** Moving from a `switch` to a `map` loses the
  compiler's exhaustiveness hint. `step_nodes_test.go` buys it back: it asserts
  the registry contains exactly the 13 arm-bearing kinds and pins the 7
  unhandled kinds as NOT registered, so a future stray registration or omission
  fails the build.

## Consequences

- `step.go` shrinks from 3251 to ~169 lines (thin `Step` type-switch + `drive`
  dispatcher + `stepCtx`); the engine is far easier to navigate and review, and
  ② can edit small per-kind strategy files instead of the monolith.
- ADR-0002 purity is preserved: `Step` remains pure (no I/O, no clock reads —
  time arrives as `at`), strategies are stateless, and engine import-purity
  stays `PURE` (no transport/vendor leaks). The refactor is behavior-identical;
  the full suite, engine coverage (≥85%), and lint are unchanged.
- The lost switch-exhaustiveness is covered by the completeness test rather than
  the compiler. The `halt` signal is a small permanent addition to the strategy
  contract that exactly reproduces the one arm that exited `drive` early; it
  was added with a guarding regression test
  (`TestErrorEndEventHaltsDriveOnImmediateFailure`).
- Threading `stepCtx` into the relocated helper functions was deferred as a
  future follow-up; helpers keep their original signatures for now.
- **ADR-number note:** this ADR is numbered 0044, which is greater than 0042
  (Node interface, ②) and 0043 (instance DTO, ③), yet this work **executes
  before** them. ADR numbers are chronological IDs, not an execution ordering.
