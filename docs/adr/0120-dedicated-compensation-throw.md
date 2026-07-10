# 120. Dedicated compensation-throw event

- Status: Accepted
- Date: 2026-07-10

Part of the BPMN2-alignment effort (umbrella spec
`docs/specs/2026-07-10-bpmn2-alignment-design.md`), which folds bespoke node
kinds into flags/modes on existing kinds to be more faithful to the BPMN2
metamodel while **reducing** engine surface area. See also the implementation
plan, `docs/plans/2026-07-10-adr-0120-compensate-throw.md`.

## Context

`IntermediateThrowEvent` carried a `CompensateRef` field: set it, and the node
threw a **targeted** compensation (replay a specific completed sub-process
node's archived compensation actions in reverse); leave it unset with a
signal payload, and the same node threw a **cross-instance broadcast**
(`BroadcastSignal` wakes every waiting instance). The empty-`CompensateRef`,
no-signal case — a **scope-wide** compensation throw (compensate everything
the current scope has completed so far, not just one named sub-process) — was
part of the original design intent but stubbed/parked: there was no producer
for it.

Bundling both intents in one kind was a problem, not a convenience:

1. **Mismatched blast radius.** Compensation is strictly **intra-instance**:
   its state (`RootCompensations`, `ArchivedCompensations`) lives on
   `InstanceState` and a throw only ever affects the throwing instance's own
   tokens. A signal throw is the opposite — `BroadcastSignal` reaches *every*
   instance waiting on that signal name, cross-instance. One node kind
   spanning both blast radii made the type system unable to express "this
   effect stays inside this instance" versus "this effect reaches other
   instances."
2. **Nonsensical combinations.** Nothing prevented authoring a node with both
   a signal name *and* `CompensateRef` set, a combination with no coherent
   BPMN meaning and no defined engine behaviour.
3. **A parked feature.** The scope-wide throw (empty `CompensateRef`, the
   BPMN "compensate the current scope" case) had no implementation path
   distinguishable from "no signal, no ref, do nothing."

## Decision

We introduce a dedicated node kind, `CompensationThrowEvent`
(`model.KindCompensationThrowEvent`, wire name `compensationThrowEvent`,
constructed via `event.NewCompensateThrow`), that owns **both** compensation
forms:

- **Targeted** (`WithCompensateRef(ref)`): replays the archived compensation
  records of a specific completed sub-process node, exactly as the old
  `IntermediateThrowEvent` compensation branch did — same archive source
  (`ArchivedCompensations[ref]`), same reverse-order walk, same
  resume-past-the-throw behaviour.
- **Scope-wide** (no ref): replays the throwing scope's own completed
  compensable activities. At the root scope this defaults to
  **whole-instance** breadth — `consolidateArchiveIntoRoot` first merges every
  archived sub-process's records into `RootCompensations` so they are
  compensated too, which is the BPMN-conformant reading of "compensate this
  scope." `WithScopeLocalCompensation()` narrows this to root-direct records
  only, for callers who want the pre-consolidation behaviour.

`IntermediateThrowEvent` keeps the cross-**instance** boundary-crossing
throws — signal today, message/error later — and **loses** `CompensateRef`
entirely. A node can therefore no longer express both effects at once: the
kind itself now encodes the intra-instance/cross-instance split, which is a
stronger, type-level guarantee than a shared kind with a mutual-exclusion
comment or runtime guard could give.

This **supersedes the umbrella spec's stated preference for "no new node
kind"** (extend `IntermediateThrowEvent` further). The user made this call
explicitly during Task-1 brainstorming, favoring the strongest available
expression of the intra-instance constraint over a smaller kind count. The
library is unreleased, so the break is clean: no wire alias, no migrator: any
existing targeted-throw definition using `IntermediateThrowEvent` +
`CompensateRef` must be re-authored as `NewCompensateThrow(WithCompensateRef(ref))`
/ `AddCompensationThrow`.

Runtime semantics are BPMN-conformant by default:

- **Throwing-scope**: a throw compensates records scoped to where it fires
  (the throwing token's scope), not an enclosing or unrelated scope.
- **Reverse order**: compensation actions run in the reverse order their
  activities completed, matching BPMN's "undo most-recent-first" semantics.
- **Throw-then-continue**: a compensation throw is **non-terminating** — it
  resumes the token past the throw's single outgoing flow
  (`compensationCursor.ResumeNode`) once the walk finishes, rather than
  ending the instance. This mirrors what the targeted branch already did in
  `IntermediateThrowEvent`.
- **Compensate-once**: the records a scope-wide throw drains are cleared on
  walk finish, so a second throw of the same scope (or a later cancel/error
  walk) does not re-run already-compensated activities.
- **No enclosing-scope propagation**: compensating a scope never reaches
  outward into an enclosing scope's own records; only the throwing scope
  (whole-instance at root, sub-process-local elsewhere) is in play.

The implementation (`compensationThrowEventStrategy` in
`engine/step_nodes.go`) reuses the existing machinery rather than adding a
parallel path: the single-cursor compensation walk (`compensationCursor`,
serialized per ADR-0071 so only one walk runs at a time — a concurrent throw
defers itself onto `DeferredCompensationThrows`), and `cursorRecords`
dispatching on whether the cursor carries an `ArchiveKey` (targeted, reads
`ArchivedCompensations[ref]`) or not (scope-wide, reads
`compensationRecordsForScope`). No new walk engine was written; only the
*producer* — deciding which records feed the existing cursor — is new.

## Consequences

- **One new wire kind, one new field.** `compensationThrowEvent` joins the
  wire vocabulary; `CompensateScopeLocal` (`compensateScopeLocal` on the
  wire, `omitempty`) is additive. Both are new, not migrations of existing
  wire data, since the library is unreleased.
- **Every targeted-throw call site moves.** All existing authors of a
  targeted compensation throw move from
  `NewIntermediateThrow(id, WithCompensateRef(ref))` to
  `NewCompensateThrow(id, WithCompensateRef(ref))` /
  `Builder.AddCompensationThrow(id, opts...)`. `IntermediateThrowEvent` no
  longer accepts `CompensateRef` at all — this is a compile-time break, not a
  silent behaviour change, so misuse fails fast.
- **A node can no longer be both a signal throw and a compensation throw.**
  The previously-possible (and meaningless) combination is now inexpressible
  by construction rather than merely undocumented.
- **Targeted-throw and full-rollback runtime behaviour is unchanged.** The
  targeted branch was ported verbatim (same archive read, same reverse
  order, same resume, same single-ownership consume via the `ArchiveKey`
  cursor); the previously-separate cancel/error/admin rollback walks
  (`compensationRecordsForScope`, `consolidateArchiveIntoRoot`) are unmodified
  and simply now also serve the new scope-wide throw producer.
- **Known limitation (tracked): no nested-scope cascade.** A scope-wide throw
  fired from inside a sub-process scope compensates that scope's **direct**
  completed records only. If that scope itself contains an already-closed
  nested sub-process with its own archived records, those are not cascaded
  into the throw — `ArchivedCompensations` is a flat map keyed by sub-process
  node ID, not partitioned by enclosing scope, so there is no scope-tree
  structure to walk for a nested cascade. This is the same shape of
  limitation ADR-0119 documented for scope-agnostic force-termination:
  strictly better than the previous parked state, with the narrower
  multi-level case left for a future ADR if a caller needs it.
- Example: `examples/scenarios/compensation_throw` shows a scope-wide throw
  compensating prior completed activities in reverse order, then the process
  continuing normally past the throw (throw-then-continue).
