# 39. Scope-targeted compensation: compensation throw event + archive-by-scope

- Status: Accepted
- Date: 2026-06-23

## Context

The engine records compensation records per completed compensable activity. ADR-0013 *hoists* a
closed sub-process's records to the parent/root on normal exit (erasing scope identity); ADR-0034 runs
the reverse walk over `RootCompensations` on instance cancel/error and clears them. The
`Compensate{ScopeID, FromNode}` command exists but is inert — nothing emits it, and there is no way
for a process to trigger compensation of a specific completed sub-process (the BPMN compensation-throw
pattern). ADR-0013 deferred this as "substantial … real false-positive risk."

The load-bearing hazard: to compensate a *closed* sub-process by name, its records must remain
addressable after close. But if a record is both addressable-by-scope **and** hoisted to root, it can
be compensated **twice** (once by a throw, once by the instance cancel/error walk) — double-running
money-moving actions, the exact bug class the C1/ADR-0034 review caught.

## Decision

Add scope-targeted compensation via a **compensation throw intermediate event**, with a
**single-ownership** record model that makes each completed compensable activity compensable at most
once. Engine + model change (the user authorized it).

- **Model:** `KindIntermediateThrowEvent` gains `Node.CompensateRef string` — the node id of the
  completed (sub-process) activity whose compensation to run (empty = the whole current/root scope).
  `Validate` rejects a dangling ref (`ErrCompensateRefNotFound`).
- **Archive-by-scope (reverses ADR-0013's hoist destination):** on normal sub-process exit, the closed
  scope's records are **archived** into `InstanceState.ArchivedCompensations` keyed by the sub-process
  node id, **instead of** hoisted to root. Root-scope records are unaffected. Records thus live in
  exactly one place: an open scope, the archive, or already-run.
- **Compensation throw producer + handler:** reaching a compensation throw runs the target's archived
  records (reverse order) via the existing cursor machinery with a new **resume-and-continue** finish
  mode, then **removes them from the archive** and advances the token past the throw.
- **Cancel/error walk (ADR-0034) extended:** it now compensates `RootCompensations` **plus** the
  archive (deterministic sorted order) and clears both — so a throw-compensated sub-process is already
  gone from the archive and is not double-run.

`Step` stays pure/deterministic (sorted archive iteration); `cloneState` deep-copies the archive.

## Consequences

**Positive**
- Processes can trigger scope-targeted compensation (the BPMN compensation-throw pattern); the inert
  `Compensate` command is finally wired.
- Single-ownership guarantees no double-compensation across throw / cancel / error paths.
- The ADR-0034 cancel/error semantics are preserved (everything completed still compensates on
  cancel) — now sourced from root + archive.

**Negative / trade-offs**
- **Reverses ADR-0013's hoist destination** (records archived-by-scope, not flattened to root). The
  ADR-0013 hoist tests are updated to assert archive-by-scope (a deliberate behaviour change, not a
  silent break).
- Adds an `InstanceState` field (`ArchivedCompensations`) → larger snapshot for deeply-nested
  compensable processes; `cloneState`/JSONB grow accordingly.
- The highest-risk change in the backlog (core state machine; interacts with ADR-0013/0034/0030) —
  warrants an adversarial whole-branch review focused on the no-double-compensation invariant.

**Deferred**
- Compensation **boundary** events (vs the throw event) and nested-scope archive addressing beyond a
  single sub-process node id.
- A compensation throw inside a still-running sibling scope (cross-scope targeting) — v1 targets
  archived (completed) sub-processes + root.
- **Compensation throw concurrent with an open parallel fork (v1 limitation).** The throw walk runs
  instance-wide (`StatusCompensating`); a sibling token in a parallel branch could complete during the
  walk and is not paused. v1 scopes the compensation throw to the main flow (no concurrent parallel
  branch). A follow-up should add a `Validate` rule rejecting a compensation throw reachable under an
  open parallel fork, or pause sibling tokens during the walk. (A throw with no outgoing flow is
  already guarded in `Step` — it auto-advances instead of terminating — and `Validate` forbids it via
  `ErrDeadEnd`.)
