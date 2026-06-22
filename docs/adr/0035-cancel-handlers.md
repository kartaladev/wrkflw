# 35. Per-node cancel handlers (beside definition-scoped CancelActions)

- Status: Accepted
- Date: 2026-06-23

## Context

ADR-0028 added definition-scoped cancel behaviour (`ProcessDefinition.CancelActions`, fire-and-forget
on `CancelRequested`). There is no **per-node** cancel hook: when an instance is cancelled, the
activities that were **active/in-flight** at that moment get no chance to clean up the work they had
started (release a held lock, close an opened connection, void a pending hold). The user asked for a
per-node cancel handler plus a definition-scoped one — the latter already exists as `CancelActions`.

Cancel handling is a distinct activity-lifecycle event from error handling (node *failed* → route the
token; ADR-0016) and compensation (node *completed* → reverse-order undo; ADR-0013/0034). On a cancel
they compose. (See the spec's "distinct from error handling" note.)

## Decision

Add `model.Node.CancelHandler string` — an optional service-action name run **fire-and-forget** for
each **active** node when the instance is cancelled. The engine's `CancelRequested` handler, before
the compensation/immediate branch (which clears tokens), iterates the live tokens, resolves each
token's node scope-aware via `defForScope`, and emits an ADR-0028 `InvokeCancelAction{Name:
node.CancelHandler}` for every active node whose handler is set. These are emitted beside
`def.CancelActions` (the definition-scoped cancel handler). The runtime already runs
`InvokeCancelAction` best-effort (logs, never feeds back, never fails the cancel) — **no runtime
change, no new command, no migration**. `CancelActions` is treated as the definition-scoped cancel
handler (documented, not rebuilt).

`Step` stays pure and deterministic (`InvokeCancelAction` has no CommandID; emission is a function of
`(def, state)`; variable snapshot via `copyVars`). The change is one additive model field; no
`InstanceState`/`cloneState` change.

## Consequences

**Positive**
- In-flight activities can clean up on cancel — closing the gap between definition-wide cancel actions
  and per-activity cleanup.
- Reuses the ADR-0028 fire-and-forget path entirely (no runtime change); cancel still never fails.
- On a cancel, per-node handlers compose cleanly with `def.CancelActions` (ADR-0028) and compensation
  (ADR-0034). Distinct from error handling, which is untouched.
- Back-compat: a definition that sets no `CancelHandler` behaves byte-for-behaviour as today.

**Negative / trade-offs**
- Fire-and-forget means a cancel-handler failure is logged, not retried (consistent with ADR-0028);
  a handler that must succeed needs its own internal retry.
- The handler runs only for nodes **active at cancel time**; nodes already completed are the domain
  of compensation, not cancel handlers (by design).

**Deferred**
- A distinct definition-scoped *handler* shape (vs the `CancelActions` action-name list) if a future
  need arises — `CancelActions` is the definition-scoped handler for now.
- Scope-targeted compensation (ADR-0036).
