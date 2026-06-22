# Design: per-node + definition-scoped cancel handlers

**Date:** 2026-06-23
**Status:** Approved (user authorized engine change)
**Track:** Backlog top pick #2 (follow-up to ADR-0028 cancel actions + ADR-0032 cancel propagation).
**ADR:** 0035.

## 1. Problem & scope

ADR-0028 added **definition-scoped** cancel behaviour: `ProcessDefinition.CancelActions []string` —
fire-and-forget service actions invoked on `CancelRequested`. There is **no per-node** cancel hook:
when an instance is cancelled, the activities that were **active/in-flight** at that moment (parked at
a user task, timer, awaiting a service action, etc.) get no chance to clean up their in-flight work
(release a held lock, close an opened connection, void a pending hold).

This track adds a **per-node cancel handler** — a node-level fire-and-forget cleanup action that runs
for each **active** node when the instance is cancelled — *beside* the existing definition-scoped
`CancelActions`.

### Interpretation of "definition-scoped cancel handler" (please review)

The user asked for "per node cancel handler" **and** "a definition scoped cancel handler". The
engine already has the latter: **`ProcessDefinition.CancelActions` IS the definition-scoped cancel
handler** (ADR-0028) — definition-wide, fire-and-forget, run on every cancel. This track therefore
**adds the missing per-node hook** and treats `CancelActions` as the definition-scoped handler
(documented, not rebuilt). If a *distinct* definition-scoped handler shape is wanted (e.g. a single
handler invoked with cancellation context rather than a list of action names), that is a small
refinement on top — flagged here for the spec review.

### Distinct from error handling and compensation (per the user's earlier question)

Three separate activity-lifecycle hooks, deliberately **not** merged:
- **Error handler** (exists, ADR-0016): node *failed* → route the token (retry / error boundary /
  recovery flow / incident).
- **Cancel handler** (THIS track): node was *active/interrupted* when the instance/scope is cancelled
  → fire-and-forget cleanup of in-flight work.
- **Compensation** (exists + ADR-0034): node *completed* → reverse-order undo with the
  completion-time variable snapshot.

On a cancel they **compose**: compensate completed compensable nodes (ADR-0034) **and** run cancel
handlers for active nodes (this track) **and** run `def.CancelActions` (ADR-0028).

**In scope:** `model.Node.CancelHandler string`; engine emits its `InvokeCancelAction` for each active
node on `CancelRequested`. **No runtime change** (reuses the ADR-0028 `InvokeCancelAction`
fire-and-forget best-effort path). No new command, no migration.

**Out of scope:** scope-targeted compensation (ADR-0036); a distinct def-scoped handler shape (refinement).

## 2. Mechanism

### 2.1 Model

```go
// model.Node gains:
//   CancelHandler string  // optional ServiceAction name run (fire-and-forget) when THIS node is
//                         // active and the instance is cancelled. Empty = no handler.
```
No `Validate` rule (an optional single action name; empty = none — unlike `CancelActions` which
rejects empty *entries* in its list). Model production diff is one additive field.

### 2.2 Engine — emit on CancelRequested

In the `CancelRequested` handler (engine/step.go), **before** the compensation/immediate branch (the
compensation branch's `beginCompensation` clears tokens, so active nodes must be collected first),
build per-node cancel commands by iterating the live tokens:

```go
var nodeCancelCmds []Command
for i := range s.Tokens {
    tok := &s.Tokens[i]
    tdef, derr := defForScope(def, &s, tok.ScopeID) // scope-aware (token may be in a sub-process)
    if derr != nil { continue }                      // defensive; cancel must not fail
    if node, ok := tdef.Node(tok.NodeID); ok && node.CancelHandler != "" {
        nodeCancelCmds = append(nodeCancelCmds, InvokeCancelAction{Name: node.CancelHandler, Input: copyVars(s.Variables)})
    }
}
```

Prepend `nodeCancelCmds` alongside the existing `cancelActionCmds` (def.CancelActions) in **both**
branches (compensation and immediate), so on cancel the command order is:
`[def.CancelActions…, per-node CancelHandlers…, (compensation walk | FailInstance) …]`.

`InvokeCancelAction` is the ADR-0028 fire-and-forget command: the runtime runs it best-effort (logs
failures, never feeds a result back, never fails the cancel). No CommandID ⇒ `Step` stays
deterministic and pure; an active node with an empty `CancelHandler` contributes nothing (current
behaviour preserved when no node sets it).

### 2.3 Determinism / purity

`Step` stays pure/deterministic: the emission is a function of `(def, state)`; `InvokeCancelAction`
carries a variable snapshot (`copyVars`) like `CancelActions`. No clock/random, no new imports, no
`InstanceState`/`cloneState` change (the handler is on the model `Node`, not on state).

## 3. Testing strategy

- **engine (`engine_test`, table-driven):**
  - one active node with a `CancelHandler` → cancel emits its `InvokeCancelAction` (alongside any
    `def.CancelActions`).
  - multiple active nodes (parallel tokens) each with handlers → one `InvokeCancelAction` per active
    node; nodes without a handler contribute none.
  - active node in a **sub-process scope** with a handler → resolved via `defForScope`, emitted.
  - cancel-with-compensation (ADR-0034 path) + a per-node handler → both the per-node
    `InvokeCancelAction` and the compensation walk run; node handlers emitted before the walk.
  - no node sets `CancelHandler` → byte-for-behaviour identical to today (existing cancel tests green).
  - determinism: same `(def, state)` ⇒ same commands.
- **runtime (`runtime_test`):** e2e — a process parked at a user task whose node has a `CancelHandler`
  is cancelled → the handler action ran (best-effort), instance terminated. Reuses the ADR-0028
  `InvokeCancelAction` runtime path (no runtime production change).

**Gate:** `go test -race -p 1 ./...` green; ≥85% engine + runtime + model; `golangci-lint` clean;
engine import-purity; `Step` determinism; model diff = one additive field.

## 4. ADR

| ADR | Decision |
|---|---|
| **0035** | Add `model.Node.CancelHandler` — a per-node fire-and-forget cleanup action emitted (as an ADR-0028 `InvokeCancelAction`) for each **active** node on `CancelRequested`, beside the definition-scoped `def.CancelActions` (which is the definition-scoped cancel handler). Cancel handler ≠ error handler ≠ compensation (three distinct activity-lifecycle hooks); on cancel they compose. Scope-aware node resolution via `defForScope`. No runtime change, no new command, no migration; `Step` pure/deterministic; one additive model field. |
