# 67. Transactional SendTask delivery via the event outbox (supersedes ADR-0060)

- Status: Accepted
- Date: 2026-06-26
- Supersedes: ADR-0060

## Context

ADR-0060 routed a SendTask's outbound message through a consumer-supplied `MessageSink`
port whose `Send` runs AFTER the state commit (commit-before-perform). Because `Send` is in
a separate transaction from the state commit, a crash between the commit and `Send` strands
the message: the instance has durably advanced past the SendTask but the message is never
sent and never retried. ADR-0060 documented this as best-effort. True atomicity is
unreachable through the `Send` port itself, since `Send` is post-commit by construction.

## Decision

Carry the outbound message as an `OutboxEvent` in `AppliedStep.Events`, derived from the
`engine.SendMessage` command at the deliverLoop edge (mirroring `terminalOutboxEvent`). The
existing `Store.writeOutbox` persists it inside the state-commit transaction; the existing
`Relay` drains it at-least-once; the existing watermill `Publisher` publishes it on topic
`message.<Name>` with payload `{messageName, correlationKey, variables}`. The `perform`
handler for `SendMessage` becomes a no-op. The `MessageSink`/`OutboundMessage` port and
`WithMessageSink` are RETIRED (Replace). Consumers customize delivery via a `message.*`
subscriber; a reference `eventing.NewMessageHandler` routes to `Runner.DeliverMessage` for
intra-engine resume of a parked ReceiveTask. `engine/` and `model/` are unchanged.

## Consequences

- SendTask delivery is atomic with state and at-least-once (retry/DLQ via the existing
  relay); the ADR-0060 stranding window is eliminated.
- The synchronous in-process `MessageSink` hook is gone; consumer customization moves to an
  async `message.*` subscriber (more durable, fan-out-capable, but broker-coupled).
- `Runner.DeliverMessage`'s waiter index is in-memory per Runner, so intra-engine
  correlation works within one process; cross-process correlation is the consumer's
  responsibility (an external subscriber / correlation store).
- Messages and domain events share `wrkflw_outbox`, namespaced by topic (`message.*` vs
  `instance.*`). Intentional reuse, no new table/migration.
