# 25. Atomic call-link side-effects on the transactional Store

- Status: Accepted
- Date: 2026-06-22

## Context

Durable async call activity (ADR-0024) requires two writes to be **crash-safe**:
1. the `wrkflw_call_links` row must exist **if and only if** the child instance exists
   — otherwise a crash between child-create and link-record leaves a parent parked
   forever with no link, or a link pointing at a child that was never created;
2. the link must flip to `completed`/`failed` **if and only if** the child reaches
   terminal status — otherwise a crash between the child's terminal commit and the
   link flip leaves a finished child whose parent is never notified.

Both demand that the call-link write share the child instance's **transaction**. The
runtime `Store` already commits snapshot + journal + outbox atomically per applied
step (ADR-0007), owning its own `pgx.Tx` internally. The question is how the call-link
write joins that tx without (a) introducing a second, uncoordinated transaction, (b)
leaking call-activity concerns into the engine, or (c) forcing every `Store` caller to
know about call links.

Options considered: a separate `CallLinkStore.Record`/`MarkTerminal` call right after
the child `Create`/`Commit` (two transactions — a crash window reopens the exact
problem); a shared externally-managed `pgx.Tx` threaded through both the `Store` and
the `CallLinkStore` (couples the two ports and complicates the `MemStore` path); or
extending the existing atomic unit, `AppliedStep`, with the optional side-effects.

## Decision

Extend `runtime.AppliedStep` with two **optional, additive** call-link side-effects
that the `Store` persists in the same transaction as the step:

```go
type AppliedStep struct {
    State   engine.InstanceState
    Trigger engine.Trigger
    Events  []OutboxEvent
    // NewCallLink, when non-nil, inserts a wrkflw_call_links row in this step's tx.
    // Set on the child's first Create so the link exists iff the child exists.
    NewCallLink *CallLink
    // CallOutcome, when non-nil, flips THIS instance's call-link to terminal in this
    // step's tx. Set on the child's terminal Commit so the notification is durable
    // iff the child is terminal.
    CallOutcome *CallOutcome
}
```

- `Store.Create` honors `NewCallLink` (one extra `INSERT INTO wrkflw_call_links` in
  the create tx). `Store.Commit` honors `CallOutcome` (one extra
  `UPDATE wrkflw_call_links SET status=…, output=…, error=… WHERE child_instance_id=…`
  in the commit tx — affecting zero rows for a root instance with no link, a clean
  no-op).
- Both fields are **nil for every existing caller**, so the change is behavior- and
  byte-compatible for all current flows (the SQL is only emitted when the field is
  set). `MemStore` honors the fields against its in-memory `MemCallLinkStore`.
- The `Store` remains the single atomic-write authority; no second transaction, no
  externally-threaded tx, no engine change. The read/claim side
  (`CallLinkStore.ClaimPending`/`MarkNotified`/`LookupChild`) stays a separate port —
  only the **write** side is fused into the `Store` for atomicity.

## Consequences

**Easier:** the two crash-safety invariants hold by construction — the link's
existence is tied to the child's existence, and the link's terminal flip is tied to
the child's terminal commit, because each pair commits in one transaction. No new
transaction-coordination machinery; the extension is two nullable struct fields and a
conditional SQL statement in `Create`/`Commit`. Existing `Store` callers are
unaffected (fields default nil). The `MemStore` mirrors the same semantics for the
pure/test path.

**Harder / trade-offs:** `AppliedStep` — previously a pure (state, trigger, events)
value — now carries call-activity-specific optional fields, a mild widening of the
runtime persistence contract toward one feature. The `Store` implementations gain a
conditional write each. A `CallOutcome` is set on every terminal commit even for root
instances (no link), costing one no-op `UPDATE … WHERE child_instance_id=<root>` that
matches zero rows; acceptable (terminal commits are rare relative to step commits, and
the predicate hits the PK index). An alternative that kept `AppliedStep` pure would
require a shared-tx seam between `Store` and `CallLinkStore` — rejected as more
coupling for no crash-safety benefit.
