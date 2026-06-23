# Design: scope-targeted compensation (compensation throw event + archive-by-scope)

**Date:** 2026-06-23
**Status:** Approved design (execution pending) — user authorized engine/model change.
**Track:** Backlog top pick (Correctness). The LARGEST engine/model change; reverses part of ADR-0013.
**ADR:** 0039.

> **Execution note.** This is the single largest, highest-risk change in the backlog (core state
> machine + model + a reversal of the ADR-0013 hoist), with real interaction hazards (below). It is
> specced in full here so it can be executed as a focused, well-reviewed effort. Phases 1–4 below are
> the SDD task breakdown. **Phase 0 (the double-compensation hazard) is the load-bearing design
> decision — read it first.**

## 0. The interaction hazards (read first)

Three existing decisions constrain this work:

- **ADR-0013 hoist:** on a sub-process's *normal* exit (`engine/step.go:1106` `hoistCompensations`),
  the closed scope's `Compensations` are appended to the parent/root list, then the scope is closed —
  **erasing scope identity**. The admin/cancel/error walks operate on `RootCompensations`.
- **ADR-0034 compensation-on-error/cancel:** an instance cancel/error runs the reverse walk over
  `RootCompensations` and **clears them on full-rollback finish** (idempotent re-cancel).
- **`Compensate{ScopeID, FromNode}`** (engine/command.go) exists but is **inert** — nothing emits it,
  and the walk machinery (`beginCompensation`, `compensationCursor.ScopeID`, `compensationRecordsForScope`)
  *already* supports a non-root `ScopeID`.

**The hazard:** to compensate a *closed* sub-process by name, its records must remain addressable
after close (archive-by-scope). But if a record is BOTH archived (for a compensation throw) AND
hoisted to root (for the cancel/error walk), it can be compensated **twice** — once by the throw,
once by the instance cancel/error walk. Double-compensation of money-moving actions is the exact bug
class the C1 (ADR-0034) opus review caught. **The design MUST make each completed compensable
activity compensable at most once.**

### Decision (Phase 0): move-on-compensate, single ownership

A compensation record has exactly one "owner" at any time:
- While its scope is **open**, it lives in `scope.Compensations` (owned by that scope).
- On **normal scope exit**, it is **archived by the sub-process node id** into a new
  `InstanceState.ArchivedCompensations map[string][]CompensationRecord` (keyed by the sub-process
  node id) — **instead of** being hoisted to the parent/root. *(This reverses ADR-0013's hoist
  destination: records become addressable by their originating sub-process rather than flattened to
  root.)*
- A **compensation throw event** referencing sub-process node `X` runs `X`'s archived records (reverse
  order) and then **removes them from the archive** (compensated once, can't re-run).
- **Instance cancel/error (ADR-0034)** must still compensate *everything completed*: the walk now
  iterates `RootCompensations` **plus** all `ArchivedCompensations` (root-scope records were never
  archived; sub-process records live in the archive). It clears both on finish. A throw-compensated
  sub-process is already removed from the archive, so the cancel walk won't double-run it.

This keeps single ownership: a record is in exactly one of {open scope, archive, already-run}. The
ADR-0013 hoist is **replaced** by archive-by-scope; the root walk is **extended** to include the
archive. (ADR-0013's "hoist on close so the root walk sees them" intent is preserved — the records
are still reachable by the root cancel/error walk, just via the archive instead of a flattened list.)

## 1. Model (Phase 1)

A **compensation throw intermediate event**. Reuse `KindIntermediateThrowEvent` (already exists) with
a new field rather than a new NodeKind (less churn, no NodeKind-JSON change):

```go
// model.Node gains:
//   CompensateRef string  // on a KindIntermediateThrowEvent: the node id of the completed activity /
//                         // sub-process whose compensation to run. Empty = compensate the whole
//                         // current scope (root). Non-empty names a (sub-process) node whose
//                         // archived compensations run, then execution continues past the throw.
```

`Validate` (ADR-0030 file): a `KindIntermediateThrowEvent` with `CompensateRef` set is a
**compensation throw**; `CompensateRef`, when non-empty, must name an existing node (else a new
`ErrCompensateRefNotFound`). A compensation throw is a normal flow node (1-in/1-out) — the
reachability + no new pairing rules apply unchanged.

## 2. Engine (Phases 2–3)

### 2.1 Archive-by-scope (Phase 2 — replaces the hoist)

- Add `InstanceState.ArchivedCompensations map[string][]CompensationRecord` (keyed by sub-process node
  id). `cloneState` deep-copies it.
- At the sub-process normal-exit site (`step.go:1106`), replace `hoistCompensations(child, parent)`
  with `archiveCompensations(childScopeID, subProcessNodeID)`: move `scope.Compensations` into
  `ArchivedCompensations[subProcessNodeID]` (append; a sub-process entered twice accumulates), then
  `closeScope`. Root-scope records are unaffected (never archived).
- Extend the cancel/error walk source: `beginCompensation` for the **root/instance** outcome walks
  `RootCompensations` **followed by** the flattened `ArchivedCompensations` (deterministic order:
  archive entries by ascending sub-process node id, each in reverse completion order). Clear both on
  finish. *(This is the careful part — the cursor/`compensationRecordsForScope` must yield a single
  ordered sequence; introduce a helper `allCompensationRecords(s)` returning the combined reverse-order
  sequence, and have `stepCompensationFinish` clear both lists.)*

### 2.2 Compensate producer + handler (Phase 3)

- **Producer:** when a token reaches a `KindIntermediateThrowEvent` with `CompensateRef`, emit
  `Compensate{ScopeID: "", FromNode: ""}` is NOT enough — the existing `Compensate` targets a
  *scope*, but a throw targets an *archived sub-process by node id*. Extend `Compensate` to carry the
  archived target: reuse `FromNode` as "the sub-process node id whose archive to compensate" (or add a
  field). Park the throw token awaiting the compensation sub-walk's completion.
- **Handler `stepCompensate` (new):** runs the reverse walk over the target's records
  (`ArchivedCompensations[ref]`, or `RootCompensations` when ref empty) using the existing cursor
  machinery with a **new finish mode: resume-at-the-throw's-successor** (not terminate, not
  ToNode-rollback). On finish, remove the compensated records from the archive and move the throw
  token along its single outgoing flow (`StatusRunning`). This requires the cursor to remember the
  resume node (the throw's successor) — extend `compensationCursor` with a `ResumeNode string` (or
  reuse `ToNode` semantics carefully — but ToNode means "rollback target", different; prefer a
  distinct field).

### 2.3 Determinism / purity
`Step` stays pure/deterministic; the archive map iteration must be **sorted by key** for determinism.
`cloneState` deep-copies the archive. No new imports.

## 3. Testing strategy (Phase 4)

- **engine (`engine_test`):** sub-process completes → its records archived (not in RootCompensations);
  a compensation throw referencing it runs its compensation (reverse order) then resumes past the
  throw; a second throw to the same ref is a no-op (already removed); instance cancel/error (ADR-0034)
  still compensates root + archived (and a throw-compensated sub-process is NOT double-run); ADR-0013
  behaviour change — the existing hoist tests are updated to assert archive-by-scope; determinism +
  `cloneState` over `ArchivedCompensations`.
- **model (`model_test`):** `Validate` — compensation throw with a dangling `CompensateRef` →
  `ErrCompensateRefNotFound`; a normal throw (no ref) unaffected.
- **runtime (`runtime_test`):** e2e — a process with a sub-process (compensable inner task) then a
  compensation throw referencing it runs the inner compensation and continues to completion.

**Gate:** `go test -race -p 1 ./...` green; ≥85% engine/model/runtime; lint clean; `Step`
pure/deterministic; `cloneState` test extended; **the ADR-0013 hoist tests are updated (behaviour
change), NOT silently broken** — the change from hoist-to-root to archive-by-scope is deliberate and
re-asserted.

## 4. SDD task breakdown (when executed)

1. **Phase 1** — `model.Node.CompensateRef` + `Validate` (`ErrCompensateRefNotFound`) + tests.
2. **Phase 2** — `ArchivedCompensations` + `cloneState`; replace hoist with `archiveCompensations`;
   extend the cancel/error walk to include the archive (`allCompensationRecords`); update the
   ADR-0013 hoist tests to archive-by-scope; assert no double-compensation on cancel. **Highest-risk
   phase** (touches ADR-0013 + ADR-0034 interaction).
3. **Phase 3** — compensation-throw producer + `stepCompensate` handler + `compensationCursor.ResumeNode`
   + resume-and-continue finish mode + remove-from-archive-on-compensate.
4. **Phase 4** — runtime e2e + ADR-0039 + HANDOVER/memory + full gate + opus whole-branch review
   (scrutinize the double-compensation invariant especially).

## 5. ADR

| ADR | Decision |
|---|---|
| **0039** | Scope-targeted compensation via a **compensation throw intermediate event** (`KindIntermediateThrowEvent` + `Node.CompensateRef`). On normal sub-process exit, compensation records are **archived by sub-process node id** (`InstanceState.ArchivedCompensations`) **instead of hoisted to root** (reversing ADR-0013's hoist destination) — preserving scope identity and **single ownership** so a record is compensated at most once. A compensation throw runs its target's archived records (reverse order) then resumes past the throw; the ADR-0034 cancel/error walk is extended to compensate root **plus** archive (and clears both), so a throw-compensated sub-process is not double-run. Wires the formerly-inert `Compensate` command. `Step` stays pure/deterministic; `cloneState` extended; deterministic sorted archive iteration. |
