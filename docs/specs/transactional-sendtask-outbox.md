# Transactional SendTask delivery via the event outbox

- Status: Approved (brainstorming), pending implementation plan
- Date: 2026-06-26
- Supersedes: the `MessageSink` port of ADR-0060
- ADR to record on implementation: **0066**

## Problem

A BPMN SendTask emits an outbound message (ADR-0060). Today that message is routed
through a consumer-supplied `MessageSink` port whose `Send` method is invoked
**after** the state commit (commit-before-perform):

```
Step → Commit(state) → perform(SendMessage) → msgSink.Send(OutboundMessage)
```

Because `Send` runs in a **separate transaction from the state commit**, a crash
between the commit and `Send` leaves the instance durably advanced past the SendTask
while the message is **never sent and never retried**. ADR-0060 documents this honestly
as best-effort: "a sink error silently strands the message." Consumers needing
atomic / at-least-once delivery were told to "wire the sink to the transactional
outbox" — but that is unreachable through the `Send` port itself, since `Send` is
post-commit by construction.

## Goal & guarantee

Deliver a SendTask's outbound message with **true transactional-outbox semantics**:

- The message is written into the **existing `wrkflw_outbox` in the same transaction
  as the state commit** — all-or-nothing, no crash window.
- It is relayed **at-least-once** by the **existing** `Relay`/`Publisher`, inheriting
  retry/backoff, dead-lettering, NOTIFY, and `dedup_key` deduplication for free.
- `engine/` and `model/` stay **zero-diff**. `engine.SendMessage` and
  `sendTaskStrategy` are untouched; the change is confined to `runtime/`, with
  reference wiring in `eventing/` and documentation.

## Decision: Option A + Replace

Two decisions were taken during brainstorming:

1. **Option A — atomicity via the existing event outbox.** The outbound message is
   carried as an `OutboxEvent` in `AppliedStep.Events` (written by the existing
   `writeOutbox` inside the commit tx), rather than performed after commit. A truly
   atomic design *cannot* be a `MessageSink.Send` implementation, because `Send` runs
   post-commit; atomicity requires the message to ride `AppliedStep`. Option A reuses
   the proven relay/DLQ/NOTIFY/dedup machinery and unifies SendTask delivery with the
   existing event-driven chaining pattern (`eventing.NewChainHandler`).

2. **Replace — the event outbox becomes the only path.** `SendMessage` always emits a
   `message.<Name>` outbox event; the `MessageSink` port and `WithMessageSink` are
   retired. This reverses part of ADR-0060.

### Flexibility note (consumer customization)

ADR-0060's `MessageSink` let a consumer customize message handling synchronously,
in-process, via a one-method Go interface, with no broker required. Under Replace, that
customization **relocates to a subscriber on the `message.*` topic**: the consumer still
receives the full data and writes arbitrary routing logic (intra-engine delivery,
external publish, fan-out, transform), and gains natural pub/sub fan-out (multiple
independent subscribers). The trade-off, accepted deliberately: the customization is now
**async, after relay, and effectively requires wiring the eventing/outbox stack** rather
than a lightweight synchronous hook. Because the subscriber is now the *only* customization
path, a reference `eventing.NewMessageHandler` helper + runnable Example is in scope so the
feature is reachable end-to-end through the public API.

## Design

### Public contract

- **Topic:** `message.<MessageName>` (namespaced vs `instance.*`; lets a consumer
  subscribe per message name, the natural BPMN correlation model).
- **Payload (JSONB):** `{"messageName": <name>, "correlationKey": <key>, "variables": <sender vars copy>}`.
- **Metadata** (carried by the existing watermill publisher from `OutboxEvent` fields):
  `instance_id`, `definition_ref`.
- **Dedup key:** `<instanceID>:<seq>:<eventIndex>` — produced for free by the existing
  `writeOutbox`; strong and collision-free (no `MessageSink` seq-less weakness).

### Core mechanism (runtime-only)

New derivation in `runtime/outbox.go`, mirroring `terminalOutboxEvent`:

```go
// outboundMessageEvents turns each SendMessage command into a message.* outbox
// event so a SendTask message is written atomically in the state-commit tx and
// relayed at-least-once, exactly like a domain event (ADR-0066).
func outboundMessageEvents(st engine.InstanceState, cmds []engine.Command) []OutboxEvent {
	var out []OutboxEvent
	for _, c := range cmds {
		m, ok := c.(engine.SendMessage)
		if !ok {
			continue
		}
		out = append(out, OutboxEvent{
			Topic:         "message." + m.Name,
			Payload:       map[string]any{"messageName": m.Name, "correlationKey": m.CorrelationKey, "variables": m.Payload},
			InstanceID:    st.InstanceID,
			DefinitionRef: instanceDefRef(st),
		})
	}
	return out
}
```

Wire it where events are already assembled (`runner.go` deliverLoop):

```go
events := terminalOutboxEvent(prevStatus, st, res.Commands)
events = append(events, outboundMessageEvents(st, res.Commands)...)
```

The existing `Store.writeOutbox` persists these rows in the commit tx; the existing
`Relay` drains them; the existing watermill `Publisher` publishes `message.<Name>`.

In `perform`, keep an explicit no-op so `SendMessage` does not hit the
`default: "unsupported command %T"` branch:

```go
case engine.SendMessage:
	// Delivered transactionally as a message.* outbox event (ADR-0066); nothing to perform.
	return nil, nil
```

### Removal (Replace)

- Delete `runtime/message_sink.go` (`MessageSink`, `OutboundMessage`) and
  `runtime/message_sink_test.go`.
- `runtime/runner.go`: remove the `msgSink` field, the `WithMessageSink` option, and
  the sink-`Send` body inside the `SendMessage` perform case (now the no-op above).

### Reference delivery side (reachability)

`Runner.DeliverMessage(ctx, def, name, correlationKey, payload)` already exists as the
intra-engine resume path — it correlates via an in-memory `msgWaiters` index and
delivers `engine.NewMessageReceived` to a parked ReceiveTask / message boundary.

New thin helper in `eventing/` (keeps `runtime` watermill-free, mirrors
`NewChainHandler`):

```go
// NewMessageHandler adapts a message.* outbox subscription to Runner.DeliverMessage,
// decoding the message payload and routing it intra-engine.
func NewMessageHandler(deliver MessageDeliverer, resolve DefinitionResolver) message.NoPublishHandlerFunc
```

Plus a runnable `Example` (gochannel) showing the full loop: SendTask → relay →
`message.<Name>` → handler → `DeliverMessage` resumes a parked ReceiveTask.

**Definition resolution (important):** `Runner.DeliverMessage(ctx, def, name, key, payload)`
correlates to the **receiver** instance (the one parked on `(name, key)`), which is *not*
the sender that emitted the message — and its `def` argument must therefore be the
**receiver's** definition. The handler only carries the sender's message, so the receiver
definition is supplied by the consumer-provided `DefinitionResolver`. In the homogeneous /
single-definition case (and the Example) this is one shared `*model.ProcessDefinition`; a
multi-definition consumer resolves it by their own strategy (e.g. message-name → owning
definition). This is a known limitation of `DeliverMessage`'s single-`def` API, not new to
this change.

**Documented caveat:** `DeliverMessage`'s waiter index is in-memory per `Runner`, so
intra-engine correlation works within one process; cross-process correlation is the
consumer's responsibility (an external subscriber/correlation store).

## Components & boundaries

| Package | Change |
|---|---|
| `runtime/outbox.go` | add `outboundMessageEvents` |
| `runtime/runner.go` | append message events in deliverLoop; `SendMessage` perform case → no-op; remove `msgSink` field + `WithMessageSink` |
| `runtime/message_sink.go` | **deleted** |
| `eventing/` | add `NewMessageHandler` + supporting `MessageDeliverer`/`DefinitionResolver` interfaces + Example |
| `internal/persistence/postgres/` | tests only (no schema change — reuses `wrkflw_outbox`) |
| `engine/`, `model/` | **zero diff** |
| `docs/` | ADR-0066; mark ADR-0060 Superseded; README; HANDOVER refresh |

No migration is required — the message rides the existing `wrkflw_outbox` table.

## Testing (TDD, strict red → green per symbol)

- `runtime/outbox_test.go` (`runtime_test`): table test for `outboundMessageEvents`
  — no `SendMessage` → nil; one → correct topic/payload/instance/defRef; multiple.
- deliverLoop integration (in-memory `Store`): a SendTask step yields the `message.*`
  event inside `AppliedStep.Events` (assert it is present alongside the state).
- `internal/persistence/postgres` (testcontainers via `database.RunTestDatabase`):
  committing a step carrying a `message.*` event writes the outbox row in the same tx;
  the relay drains and the publisher publishes it.
- `eventing` Example/handler test (gochannel): `message.*` → `DeliverMessage` resumes a
  parked ReceiveTask in-process.
- Remove the obsolete `message_sink_test.go` assertions.

## Verification

- `go test -race ./...` green (testcontainers / Docker daemon required).
- `runtime/` (and touched packages) ≥ 85% line coverage.
- `golangci-lint run ./...` clean; `gofmt` clean.
- `FuzzStep` corpus clean (engine untouched).
- `goleak` clean (no leaked relay/subscriber goroutines in tests).

## Consequences

- SendTask delivery is **durable and atomic** with state — the ADR-0060 stranding window
  is eliminated; delivery is at-least-once with retry/DLQ via the existing relay.
- The `MessageSink`/`OutboundMessage` synchronous in-process hook is **gone**; consumer
  customization moves to `message.*` subscribers (more durable, fan-out-capable, but
  async and broker-coupled). ADR-0060 is marked **Superseded by ADR-0066**.
- Messages and domain events share `wrkflw_outbox`, namespaced by topic prefix
  (`message.*` vs `instance.*`). This is intentional reuse, not coupling.
- `engine/` and `model/` remain zero-diff, honoring the productionization-run constraint
  that engine/model stay unchanged absent explicit confirmation.

## Out of scope / follow-ups

- Cross-process / cluster-wide message correlation (a correlation store keyed by
  `(messageName, correlationKey)`) — the in-memory `msgWaiters` index is single-process.
- A separate `wrkflw_message_outbox` table / dedicated relay (Option C) — rejected in
  favor of reusing the existing outbox.
