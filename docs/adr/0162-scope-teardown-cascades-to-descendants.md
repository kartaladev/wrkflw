# 162. Tearing down a scope tears down its descendants

- Status: Accepted
- Date: 2026-08-02

> First ADR of the scope-lifecycle-correctness delivery (2a), alongside
> [ADR-0163](0163-cancelling-a-token-cancels-its-task.md) and
> ADR-0164 (terminal transitions) was split out into **delivery 2b** on the
> round-2 audit's recommendation and is parked on `parked/terminal-transitions`
> with its own plan, `docs/plans/2026-08-02-terminal-transitions.md`.
> Design: `docs/specs/2026-08-02-scope-lifecycle-correctness.md`.
>
> Supersedes the unmerged draft of this number carried on
> `parked/scope-and-fanout-design`, which scoped itself to token cancellation
> only and explicitly deferred compensation archiving. That deferral is
> overruled here — see Decision, point 4.

## Context

`s.Scopes` is a tree. `openScope(nodeID, parentScopeID)` records a `ParentID`
(`engine/state_compensation.go`), and a sub-process entered from inside
another sub-process nests (the `openScope` call in `subProcessStrategy.enter`,
`engine/step_nodes.go`). Three separate pieces of
the engine walk that tree as if it were flat.

**1. Both abnormal teardowns match tokens on exact scope equality.**
`fireEventTriggeredSubprocessArm` (`engine/step_eventsubprocess.go`)
collects tokens whose `ScopeID == ea.EnclosingScopeID`, cancels each, then
retires the scope's event-sub-process arms. The `consume` closure in
`propagateError` (`engine/step_errors.go`) does the same for
`errScopeID`, then calls `s.closeScope(errScopeID)`. Neither descends. So a token
an earlier arm pushed into a nested sub-process survives an interrupt that was
supposed to kill it, and the instance keeps running the very activity the
interrupt targeted.

This is reachable in one delivery today. An interrupting signal boundary on a
root-scope host routes to a `SubProcess`; `drive` enters it, opening scope `S`
with the inner start token at `ScopeID == S` (`subProcessStrategy.enter` in
`engine/step_nodes.go`). A root-level interrupting event sub-process then
fires on the same signal and cancels root-scope tokens only — the token in
`S` survives.

**2. The error path leaves tokens naming scopes that no longer exist.**
`closeScope` **does** prune transitively — the scope plus every descendant
reachable through `ParentID` (`engine/state_compensation.go`, ADR-0130) —
and its doc comment states that callers "remain responsible for any per-scope
cleanup outside `s.Scopes` (cancelling tokens, arms, timers) before invoking
closeScope". The caller does that cleanup for **one** scope. The surviving
descendant tokens then name a scope absent from `s.Scopes`.

ADR-0130 saw this and deferred it by name: its Consequences record that
"`step_errors.go`'s enclosing-scope boundary routing does not cancel tokens in
descendant scopes when tearing down a scope that contains a live nested
sub-process" is "**not** addressed here … it should be tracked as its own
follow-up if a live scenario needing it is identified"
(`docs/adr/0130-closescope-descendant-cascade.md:86-91`). This ADR **is**
that follow-up: the live scenario is problem 3 below, and it does not merely
leak — it wedges the instance permanently.

**3. The drain checks enumerate direct children only, which wedges the
instance permanently.** All three sub-process exits ask `tokensInScope(sc.ID)` —
an exact-match count (`engine/state_compensation.go`) — over scopes whose
`ParentID` is the scope being examined (`exitRootEventSubprocessScope`,
`exitNestedEventSubprocessScope`, and `exitRegularSubprocessScope`, all in
`engine/step_nodes.go`). A grandchild scope holding the live token is invisible, so the exit
declares the subtree drained and calls `closeScope`, which prunes it. From that
commit onward `defForScope` cannot resolve the surviving token's scope
(`engine/step_state.go`), `drive` propagates the error
(`engine/step.go`), and **every subsequent `Step` fails identically**. No
trigger can advance the instance and none can terminate it, because every path
runs through `drive`. Recovery requires direct surgery on the persisted state.

**4. Abnormal teardown silently discards compensation records.** The normal exit
archives before closing (`exitRegularSubprocessScope` in `engine/step_nodes.go`);
neither abnormal teardown does. Records live in `s.RootCompensations` (root tokens),
`Scope.Compensations` (sub-process scopes), or `s.ArchivedCompensations[nodeID]`
once archived — and **neither reader ever inspects a live open
`Scope.Compensations`**. The admin walk reads `RootCompensations` after
`consolidateArchiveIntoRoot`; a targeted `CompensateThrow` reads
`s.ArchivedCompensations[cte.CompensateRef]` (the targeted branch of
`compensationThrowEventStrategy.enter` in `engine/step_nodes.go`).
So the archive is the only route by which a sub-process's completed work stays
compensable, and abnormal teardown never takes it. A sub-process whose `charge`
completed and whose `ship` then threw becomes permanently uncompensable, while
the identical sub-process exiting normally does not.

**5. Root-level interrupts leave zombie scopes.** After a root-level interrupting
event sub-process fires, cancelled descendant scopes are never closed, so a
*completed* instance can be committed carrying open `Scopes` entries.

**6. One "normal" exit has no descendant check at all, so the wedge and the
record loss both survive there.** An earlier draft of this ADR asserted that the
three other `closeScope` call sites — in `exitEventSubprocessScope`,
`exitNestedEventSubprocessScope`, and `exitRegularSubprocessScope` (all in
`engine/step_nodes.go`) — are normal drained exits with no live descendants,
and are therefore unaffected. The design audit refuted it for
`exitEventSubprocessScope`:

```go
// engine/step_nodes.go — exitEventSubprocessScope (before this ADR's fix)
func exitEventSubprocessScope(c *stepCtx, currentScopeID, parentScopeID string) ([]Command, bool, error) {
	c.s.closeScope(currentScopeID)   // ← unconditional: no child check, no archive
	if parentScopeID == "" {
		return exitRootEventSubprocessScope(c, currentScopeID)
	}
	return exitNestedEventSubprocessScope(c, currentScopeID, parentScopeID)
}
```

`exitRegularSubprocessScope`'s `closeScope` call is gated by its
`hasActiveChildren` check and `exitNestedEventSubprocessScope`'s by its
`hasOtherChildren` check — the two loops problem 3 widens.
`exitEventSubprocessScope` has neither. Its only upstream gate is the
**exact-match** self check `tokensInScope(currentScopeID) != 0` in
`exitSubprocessScope`, and a descendant scope's tokens carry the descendant's
own `ScopeID`.

Failure scenario: an event sub-process whose body is
`start(signal) → fork ⇒ { A: SubProcess "inner"[…UserTask…], B: end }`. Branch A
enters `inner`, opening scope `D` under the event sub-process's child scope `C`.
Branch B reaches the event sub-process's end event; `tokensInScope(C) == 0`, so
`exitEventSubprocessScope` runs `closeScope(C)`, which prunes `C` **and `D`**.
The `UserTask` token now names an absent scope — problem 3's permanent wedge,
at a site problem 3's fix does not reach.

The same site also never archives, so compensable work completed inside **any**
event sub-process is dropped on its **normal** exit — problem 4 on the very path
problem 4 uses as its counter-example ("had `fulfil` exited normally, both would
refund"). `exitNestedEventSubprocessScope` shares the archiving half: it closes
the *enclosing* scope after an interrupting event sub-process completed,
discarding whatever that scope had recorded before the interrupt.

Exactly one of the four `closeScope` call sites archives today
(`exitRegularSubprocessScope`).

Left alone, ADR-0158 (delivery 3) makes problem 1 easier to reach: firing every
matching arm means a boundary arm that opens a sub-process and an interrupting
event-sub-process arm can both fire from one delivery even when neither is the
first match in its family. That dependency is why ADR-0158 ships last.

## Decision

Make the scope **subtree**, not the scope, the unit of teardown.

**1. One descendant-collection helper, shared with `closeScope`.**

```go
// descendantScopeIDs returns scopeID plus every scope transitively nested inside
// it. NO existence guard — see the note below.
func (s *InstanceState) descendantScopeIDs(scopeID string) map[string]bool
```

A single forward pass over `s.Scopes` suffices, reusing the argument `closeScope`
already relies on: `openScope` always appends a child after its parent and
`ScopeSeq` is monotonic, so by the time a scope is visited its parent's doomed
status is known. `closeScope` is refactored onto this helper so the two cannot
drift.

**The guard asymmetry is load-bearing.** `closeScope` opens with
`if s.scopeByID(scopeID) == nil { return }`, and `scopeByID("")` is **always**
nil because the root scope is implicit. Carrying that guard into
`descendantScopeIDs` would make every root-level teardown a silent no-op;
removing it from `closeScope` would turn `closeScope("")` into an instance-wide
scope wipe. The helper has no guard; `closeScope` keeps its own.

**2. One subtree-teardown helper, used by both abnormal paths.**

```go
// cancelScopeSubtree cancels every token in scopeID and in all its descendant
// scopes, retires their event-sub-process arms, archives their compensation
// records, and returns the CancelTimer commands. It does NOT close the scopes —
// the caller decides, because the interrupting event-sub-process path
// deliberately keeps the enclosing scope open so the drain code can detect its
// children.
func cancelScopeSubtree(s *InstanceState, scopeID string, at time.Time, kind CloseKind) []Command
```

Cancellation stays `cancelTokenWaits` per token, so deadline and reminder timers,
in-wait reminders, boundary arms and event-gateway arms are retired exactly as
today — only the **set of tokens** widens.

The root scope is addressed by the empty string, so `cancelScopeSubtree(s, "", …)`
means "the root scope and every scope in the instance". That is the correct
reading for a root-level interrupting event sub-process: BPMN interrupting event
sub-processes at process level terminate all other activity in the process.

**3. Descendant scopes are closed; the enclosing scope's fate is unchanged.**

```go
// closeScopeDescendants prunes every scope nested inside scopeID, KEEPING
// scopeID itself.
func (s *InstanceState) closeScopeDescendants(scopeID string)
```

The event-sub-process path calls this, preserving its existing post-condition
that the enclosing scope stays open and the event sub-process's child scope is
opened afterwards. The error path keeps its `closeScope(errScopeID)`, which now
prunes a tree whose tokens, arms and records have already been retired.

**4. Abnormal teardown archives compensation records, like the normal exit.**
`cancelScopeSubtree` calls `archiveCompensations` for each doomed scope,
iterating `s.Scopes` in **slice order, never map order** — parent before child,
deterministic. `archiveCompensations("")` remains a no-op by construction, so
root-level teardown loses nothing that it does not already lose.

This overrules the parked draft's deferral. Discarding is not the conservative
choice: it is silent data loss with no log, no incident and no trace, in a
delivery whose thesis is that abnormal teardown should behave like normal
teardown.

**5. Drain checks count the subtree, through one shared predicate.**

```go
// tokensInScopeSubtree counts tokens in scopeID and in every scope nested
// inside it.
func (s *InstanceState) tokensInScopeSubtree(scopeID string) int

// hasChildScopeWithTokens reports whether any child scope of parentID — other
// than exceptID — holds a token anywhere in its subtree.
func (s *InstanceState) hasChildScopeWithTokens(parentID, exceptID string) bool
```

The three checks in `exitRootEventSubprocessScope`,
`exitNestedEventSubprocessScope`, and `exitRegularSubprocessScope` (all in
`engine/step_nodes.go`) are four copies of one question once point 6 adds the
fourth, so they become four calls to one predicate rather than four
hand-rolled loops:

| site | call |
|---|---|
| `exitRootEventSubprocessScope` | `hasChildScopeWithTokens("", currentScopeID)` |
| `exitNestedEventSubprocessScope` | `hasChildScopeWithTokens(parentScopeID, currentScopeID)` |
| `exitRegularSubprocessScope` | `hasChildScopeWithTokens(currentScopeID, "")` |
| `exitEventSubprocessScope` (new, point 6) | `hasChildScopeWithTokens(currentScopeID, "")` |

`exceptID == ""` means "no exception": scope IDs are always non-empty because
the root scope is implicit and has no `Scope` entry, so `sc.ID != ""` never
excludes a real scope. The last two rows taking identical arguments is the
evidence that this is one question, not four — `exitEventSubprocessScope` and
`exitRegularSubprocessScope` ask literally the same thing about the same
scope, which is precisely why `exitEventSubprocessScope` having no check at
all was invisible for so long.

This is the same argument as points 1–3: the delivery's thesis is that
duplicated enumerations drift, and shipping a **fourth** copy of a loop while
arguing that would be self-refuting. A fifth scope exit added later gets the
check by calling a named predicate instead of by remembering to re-derive it
(decided 2026-08-03, on the pre-implementation conflict scan).

**6. `exitEventSubprocessScope` gains the descendant check it never had, and
both event-sub-process exits archive.** Widening the three existing checks is not
sufficient, because `exitEventSubprocessScope` has no check to widen. It gains
one of the same shape, plus the archive call its sibling
`exitRegularSubprocessScope` already makes:

```go
func exitEventSubprocessScope(c *stepCtx, currentScopeID, parentScopeID string) ([]Command, bool, error) {
	// A descendant scope holding a live token must keep this scope from being
	// pruned: closeScope cascades (ADR-0130), so closing here would orphan that
	// token and wedge the instance. The self check upstream (in
	// exitSubprocessScope) is exact-match and cannot see it.
	if c.s.hasChildScopeWithTokens(currentScopeID, "") {
		return nil, false, nil
	}
	c.s.archiveCompensations(currentScopeID)
	c.s.closeScope(currentScopeID)
	…
}
```

and `exitNestedEventSubprocessScope` archives the enclosing scope before its
`closeScope(parentScopeID)` call. Together with `exitRegularSubprocessScope` and
`cancelScopeSubtree`, **every** scope close now archives first.

## Consequences

**Positive.**

- The permanent instance wedge is closed. No token can survive naming a
  `ScopeID` absent from `s.Scopes`, so `defForScope` cannot fail to resolve a
  live token's scope, so `drive` cannot fail for that reason.
- An interrupt stops what the interrupt was supposed to stop. Nested execution
  no longer outlives the scope that contained it.
- **A torn-down scope no longer leaves zombie entries in `s.Scopes` — on the
  two abnormal-teardown paths this ADR fixes.** The scope is exactly
  `fireEventTriggeredSubprocessArm`'s interrupting event-sub-process teardown
  (`engine/step_eventsubprocess.go`, via `closeScopeDescendants`) and
  `propagateError`'s error-boundary teardown (`engine/step_errors.go`, via
  `closeScope`). It is deliberately *not* "a completed instance no longer
  carries open `Scopes` entries": four terminal transitions set a terminal
  `Status` while leaving `s.Scopes` untouched entirely —
  `forceTerminate` (`engine/step_nodes.go`), `handleCancelRequested`'s
  immediate-termination branch (`engine/step_triggers.go`),
  `handleUnhandledError`'s immediate-failure branch
  (`engine/step_errors.go`), and `handleSubInstanceFailed`'s failure
  tail (`engine/step_triggers.go`). A force-termination end event fired
  inside a sub-process is explicitly scope-agnostic and ends the whole instance
  (`engine/step_nodes.go`), so it still commits a terminal snapshot
  carrying open `Scopes`. This ADR therefore claims the narrower thing that is
  true.

  > ⚠ **CORRECTION (2026-08-11, ADR-0174).** This bullet originally deferred the
  > wider claim with *"Closing that is `endInstance`'s job in ADR-0164 (delivery
  > 2b). Until 2b lands, this ADR claims the narrower thing that is true."*
  > **Delivery 2b landed as ADR-0164 and `endInstance` never closed a scope** —
  > the sentence was false for 11 ADRs, and pointed every later reader at a
  > guarantee nobody had built. The deferral is now discharged by
  > [ADR-0174](0174-a-dying-instance-harvests-its-open-scopes.md), which harvests
  > each open scope's compensation records and then sets `s.Scopes = nil` in
  > `endInstance`.
  >
  > The delay was not cosmetic. Because those four terminal transitions left the
  > scope open, the compensation records inside it never reached
  > `ArchivedCompensations`, and every records-exist predicate reads only the
  > archive and `RootCompensations` — so an unhandled error or an operator cancel
  > inside a record-holding sub-process **skipped the compensation walk entirely**
  > and the records became permanently unreachable. Measured on `main`: a
  > sub-process holding `undo-inner` emitted `FailInstance` and no `InvokeAction`
  > at all, and a later admin rollback was refused as *"nothing left to
  > compensate"*. The lesson recorded in ADR-0174: a deferral is only honest while
  > someone still owns it, and this one outlived its owner.
- Work that completed inside a torn-down sub-process stays compensable, so an
  error boundary that routes to a "notify and roll back" handler can actually
  roll back.
- **A sub-process closed by the event-sub-process exit is now compensable at
  all.** Recording a `SubProcess` node's *own* `CompensateAction` lived only in
  `exitRegularSubprocessScope`, so a sub-process whose scope is pruned by
  `exitNestedEventSubprocessScope` instead — because a nested event sub-process
  outlived its drain, or because an interrupt emptied the scope around it — was
  silently non-compensable: a later `CompensateRequested` or error rollback
  walked straight past it. This ADR widened that exit's reach (the child-scope
  deferral makes it the closing path in strictly more cases) and added the
  archive at that site for exactly this reason, so leaving the *record* step
  missing was incoherent. Both exits now share `recordSubProcessCompensation`,
  and a `CompensationRecord` exists where none did before — a behaviour change,
  in the same direction as the rest of this ADR. Found by `/code-review` on the
  built change, not by the design audit.
- Five helpers, five call sites: a later abnormal teardown site added
  inherits the correct behaviour instead of re-introducing the bug.
- **ADR-0130's deferred follow-up is closed.** The gap it named and left open —
  boundary routing that tears down a scope without cancelling tokens in its
  descendants — is exactly what `cancelScopeSubtree` removes. ADR-0130's own
  division of labour is preserved: `closeScope` still only prunes the scope
  tree, and callers still own the per-scope cleanup; there is now a shared
  helper that does it for the whole subtree.

**Supersession of a documented invariant.**

- **ADR-0039's "on normal sub-process exit" becomes incomplete, not wrong.**
  That ADR archives a closed scope's records into `ArchivedCompensations` keyed
  by the sub-process node ID on **normal** exit
  (`docs/adr/0039-scope-targeted-compensation.md:29-32`); abnormal teardown now
  archives on the same key by the same call. Its **single-ownership** invariant —
  a record lives in exactly one place, an open scope, the archive, or already-run
  — survives intact, because `archiveCompensations` *moves* the records and nils
  `scope.Compensations` (`engine/state_compensation.go`). Nothing is
  copied into two owners, so the no-double-compensation guarantee ADR-0039 rests
  on is unaffected.

**Negative / accepted costs.**

- **Breaking behaviour, in three directions.** A definition whose nested
  sub-process kept running after an interrupt now has it cancelled. A drain
  check that used to declare a subtree finished now waits for it. A
  compensation walk now sees records it previously could not. All three were
  defects, so there is no opt-out.
- **A targeted `CompensateThrow` changes observable behaviour.** A throw naming
  a torn-down sub-process today auto-advances on `len(records) == 0` (in the
  targeted branch of `compensationThrowEventStrategy.enter`,
  `engine/step_nodes.go`); it will now start a real walk and emit
  `InvokeAction`s. Definitions relying on the auto-advance will run
  compensation actions they did not previously run. This is the intended fix,
  not a side effect, and it carries its own test.
- More `CancelTimer` commands and more closed visits in a single interrupt
  step, proportional to the size of the cancelled subtree.
- Descendant collection is `O(len(s.Scopes))` per teardown, and
  `tokensInScopeSubtree` is `O(len(s.Scopes) + len(s.Tokens))` per drain check —
  paid once per sub-process exit rather than once per direct child. Scope counts
  are bounded by nesting depth times concurrent sub-processes, and this matches
  what `closeScope` already pays.
- Archiving nils `scope.Compensations` on a scope the event-sub-process path
  leaves **open**, and a scope-wide throw walk *does* read live records via
  `cursorRecords` → `compensationRecordsForScope`
  (`engine/step_compensation.go`) — so there are three readers, not two.
  The design audit **verified this safe**: after the interrupt the enclosing
  scope `E` holds zero tokens and never regains one, because the event
  sub-process's child scope is opened *under* `E` and its drain resumes in the
  **grandparent** (`exitNestedEventSubprocessScope`, `engine/step_nodes.go`).
  No throw token can therefore carry `ScopeID == E`, and nothing can append to
  `E.Compensations` afterwards — both `recordCompensation` sites —
  `handleActionCompleted` in `engine/step_triggers.go`, and the call inside
  `recordSubProcessCompensation` in `engine/step_nodes.go` — require a live
  token or a completing child in `E`. `StartRecordCount` is captured from the
  list actually read (in `compensationThrowEventStrategy.enter`'s scope-wide
  branch, `engine/step_nodes.go`), so the prefix arithmetic is unaffected.
- **Two further reader surfaces change**, beyond the targeted `CompensateThrow`
  above. A **root-level scope-wide** `CompensationThrowEvent` consolidates the
  archive first (the `consolidateArchiveIntoRoot` call in
  `compensationThrowEventStrategy.enter`'s scope-wide branch,
  `engine/step_nodes.go`) and will now run records the interrupt used to
  discard. And **branch selection** in `handleUnhandledError`
  (`engine/step_errors.go`) and `handleCancelRequested`
  (`engine/step_triggers.go`) gates on
  `len(s.RootCompensations) > 0 || len(s.ArchivedCompensations) > 0` — a teardown
  that previously left the archive empty now populates it, so a later unhandled
  error or admin cancel switches from the single-step immediate-failure branch to
  a multi-step compensation walk with a different terminal command sequence. This
  is the consequence most likely to move an existing test expectation, and it has
  its own test.
