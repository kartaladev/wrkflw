# 60. Implement SendTask via a pluggable MessageSink port

- Status: Superseded by ADR-0067
- Date: 2026-06-25

## Context

`model.KindSendTask` was never wired into the engine's node-strategy registry
(`nodeStrategies` in `engine/step_nodes.go`). At node entry it fell through to the
"unhandled node kinds" branch in `drive()` (`engine/step.go`), which parks the token
with `tok.State = TokenWaitingCommand` and nothing else — the token parked forever with
no command to ever resume it. The consequence was that a BPMN SendTask was a permanent
dead end: it emitted no outbound message and never advanced.

`model.SendTask` already carried `MessageName`. The sibling message-receive node
(ReceiveTask, ADR-0057) and the service-action node (ServiceTask → `InvokeAction`) were
both implemented as registry strategies; SendTask was the remaining unimplemented
message node.

The outbound side is fundamentally a consumer concern: a sent message might be delivered
intra-engine to a parked ReceiveTask, published to an external broker / the eventing
outbox, or both. Hard-wiring any one of those into the engine core would couple the
library to a transport/eventing choice and violate the library-first, vendor-swappable
constraints (CLAUDE.md). The user chose a **pluggable port** for that flexibility,
mirroring how ServiceTask routes through the consumer-supplied action catalog.

## Decision

Implement SendTask as a **fire-and-forget** node that emits a new sealed
`engine.SendMessage` command, which the runtime routes through a consumer-wired
`MessageSink` port. The change spans `model`, `engine`, and `runtime` and keeps `Step`
pure and deterministic:

- **`model.SendTask.CorrelationKey` (`model/node.go`, `model/node_constructors.go`,
  `model/node_wire.go`)** — an additive optional expr expression mirroring
  ReceiveTask's. `WithCorrelationKey` now applies to both ReceiveTask and SendTask (it
  returns an option satisfying `receiveTaskOption` *and* `sendTaskOption`); `NewSendTask`
  accepts `sendTaskOption`, with all shared activity options still usable. The
  (de)serialization in `node_wire.go` carries the new field.
- **`engine.SendMessage{Name, CorrelationKey, Payload}` (`engine/command.go`)** — a new
  member of the sealed `Command` set (`isCommand()`), carrying the resolved correlation
  key and a copy of the instance variables.
- **`sendTaskStrategy` (`engine/step_nodes.go`)** — registered in `nodeStrategies` for
  `KindSendTask`. On entry it resolves `CorrelationKey` via the Step-scoped evaluator
  (`EvalString`, deterministic against instance variables, per ADR-0056), emits a single
  `SendMessage` with `copyVars(Variables)` as payload, then **auto-advances** the token
  along its single outgoing flow leaving `tok.State == TokenActive` (like
  `startEventStrategy`). It does not park — there is no resume trigger. A bad
  correlation-key expression surfaces a wrapped
  `workflow-engine: send task %q correlation key:` error rather than advancing silently.
- **`MessageSink` port + `WithMessageSink` (`runtime/message_sink.go`, `runtime/runner.go`)**
  — `MessageSink.Send(ctx, OutboundMessage{InstanceID, Name, CorrelationKey, Payload})`.
  `perform` handles `engine.SendMessage`: if no sink is configured it returns a descriptive
  `no MessageSink configured (use WithMessageSink)` error (mirroring the AwaitHuman
  "no ActorResolver configured" guard); otherwise it calls `Send` and surfaces its error.
- Stale comments enumerating the intentionally-unhandled kinds in `engine/step.go` and
  `engine/step_nodes.go` were updated to drop `KindSendTask`.

## Consequences

- A SendTask now emits an outbound message and completes (fire-and-forget) instead of
  parking forever. The sink **owns routing** — intra-engine delivery (e.g. via
  `Runner.DeliverMessage`), external publish to a broker / the eventing outbox, or both —
  so the engine core stays free of any transport/eventing vendor choice.
- **Sink errors surface but the message can strand**: the runtime order is
  `Step → Commit(state) → perform(SendMessage)` — the auto-advanced (often already
  terminal/Completed) instance state is persisted **before** `sink.Send` runs (the same
  commit-before-perform shape as `ThrowSignal`). `Send` is invoked synchronously and a
  non-nil error is returned to the caller of `Run`/`Deliver`, but by then the instance has
  already durably advanced, so the failed message is **not re-delivered or re-emitted**:
  SendTask is **best-effort**, and a sink error **silently strands** the message (it is
  never sent) rather than causing a double-send. Consumers who need atomic / at-least-once
  send should wire the sink to the **transactional outbox** — writing the outbound message
  in the same transaction as the state commit — so the relayer guarantees eventual delivery.
- `Step` remains pure and deterministic — the correlation key evaluates through the
  injected evaluator, not the wall clock; the outbound side effect is a `Command` the
  runtime performs, not something `Step` executes. The `FuzzStep` corpus runs clean.
- `KindSendTask` moves from the intentionally-unhandled set into the arm-bearing registry
  (15 entries, was 14); the registry-invariant test was updated accordingly.
- Scope was confined to `engine/`, `model/`, `runtime/`, and `docs/`; no transport,
  persistence, or scheduling code was touched.
