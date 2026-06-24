# 47. Carry the source definition ref through the outbox

- Status: Accepted
- Date: 2026-06-24

## Context

Process-instance chaining (ADR-0045) projects each terminal outbox event into a
`ChainEvent` whose `PredecessorDef` ("defID:version") lets a `SuccessorPolicy`
route on the predecessor's *definition*, not just its id. The chaining handler
already reads a `def` message-metadata key for it — but nothing ever set that
key. The publisher set only `topic` and `instance_id` metadata, `OutboxEvent`
had no field to carry a def, and the Postgres outbox table had no column for it.
So `PredecessorDef` was structurally always empty over the built-in
publisher/relay pipeline (flagged by the ADR-0045 whole-branch review and left as
a deferred follow-up).

## Decision

Carry the source instance's definition ref end-to-end through the outbox, as a
small additive change:

- **`runtime.OutboxEvent` gains a `Def string` field** ("defID:version" of the
  instance that produced the event). Additive — existing producers/consumers that
  ignore it are unaffected.
- **`runtime.terminalOutboxEvent`** stamps `Def` from `st.DefID` / `st.DefVersion`
  on the terminal event it derives (ADR-0046).
- **The watermill publisher** sets a `def` message-metadata key from
  `OutboxEvent.Def` (alongside the existing `topic` / `instance_id`). The chaining
  handler already projects `def` → `ChainEvent.PredecessorDef`, so this is the
  last missing link.
- **The Postgres outbox** persists it: migration `0009_outbox_def.sql` adds a
  `def TEXT NOT NULL DEFAULT ''` column; `writeOutbox` writes `ev.Def`; the relay
  selects it back and sets `OutboxEvent.Def` on the event it republishes — so the
  durable (production) path carries the def, not just the in-process path.

## Consequences

- A chaining `SuccessorPolicy` can now route on `ChainEvent.PredecessorDef` over
  the built-in pipeline. The field/metadata/column are empty (`""`) for
  pre-migration rows and for events produced before this change — consumers must
  tolerate an empty def (it is best-effort routing context, not a key).
- The change is additive across the public API (`OutboxEvent.Def`), the wire
  (a new optional metadata key), and the schema (a defaulted column) — no
  migration of existing data is required, and consumers that ignore `def` are
  unaffected.
- Engine/model production diff remains **ZERO**: the def is read from the existing
  `InstanceState` fields in `runtime`; the engine is untouched.
