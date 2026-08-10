# 171. A compensation walk owns its record source and its resume scope

- Status: Accepted
- Date: 2026-08-10

> Ships in the same bundle as [ADR-0168](0168-a-compensation-walk-blocks-completion.md),
> [ADR-0169](0169-a-delivery-stops-at-a-mid-delivery-terminal.md) and
> [ADR-0170](0170-an-unhandled-error-does-not-restart-a-live-compensation-walk.md).
> It closes a defect the bundle's own audit surfaced: the three ADRs above teach the
> engine to *wait* for a compensation walk, and this one makes the thing being waited
> for survive long enough to finish.

## Context

A compensation **throw** is throw-then-continue: `startCompensationWalk`
(`engine/step_nodes.go`) consumes only the throwing token, and every sibling branch in
the same scope keeps running. A sibling that reaches its scope's end event drives
`exitSubprocessScope` → `exitRegularSubprocessScope`, which calls
`archiveCompensations` (nilling the scope's `Compensations`) and then `closeScope`
(removing the `Scope` entry entirely) — while the walk's cursor still names that scope
in `ScopeID` and `ResumeScope`, and `stepCompensationAdvance` re-reads its records
**live** through `cursorRecords` → `compensationRecordsForScope` → `scopeByID`.

### Measured reproduction

Fixture: a sub-process whose body is
`svcA(compensable) → svcB(compensable) → innerFork ⇒ { throwInner → innerEnd1 ; sibling(user task) → innerEnd2 }`.
Executed on `fix/compensation-walk-and-mid-delivery-terminal` at the ADR-0168/0169/0170
bundle commit:

```
WALK START:                 status=compensating activeCmd="zzd-c3" scopeID="zzd-s1" invokes=[undoB] tokens=1
AFTER SIBLING DRAINS SCOPE: status=compensating activeCmd="zzd-c3" scopeID="zzd-s1" scopes=0 tokens=0 invokes=[]
AFTER WALK ActionCompleted: panic: runtime error: index out of range [0] with length 0
```

The panic is raised inside `stepCompensationAdvance` at `rec := records[nextIdx]` —
`nextIdx` is 0 and `records` is nil. ⚠ **This is a panic in the pure engine core, i.e.
in the library consumer's own process.** `wrkflw` ships as an importable module; there
is no daemon of ours to absorb it.

### The one-record variant is a permanent wedge, not a panic

The audit predicted a second shape and marked it `ASSUMPTION (unverified)`. It was
built and run. **It reproduces**, exactly as predicted. With a single compensation
record the walk's next index is `-1`, so `stepCompensationAdvance` routes to the finish
instead of indexing — and the finish then resumes into a scope that no longer exists:

```
WALK START:          status=compensating activeCmd="zzd1-c2" scopeID="zzd1-s1" startCount=1 nextIdx=0 invokes=[undoA] tokens=1
AFTER SIBLING DRAINS: status=compensating activeCmd="zzd1-c2" scopes=0 tokens=0
AFTER WALK ActionCompleted: err=workflow-engine: defForScope: unknown scope "zzd1-s1"
REDELIVERY 1 against persisted state: err=workflow-engine: defForScope: unknown scope "zzd1-s1"
REDELIVERY 2 against persisted state: err=workflow-engine: defForScope: unknown scope "zzd1-s1"
REDELIVERY 3 against persisted state: err=workflow-engine: defForScope: unknown scope "zzd1-s1"
persisted state remains: status=compensating tokens=0 scopes=0 activeCmd="zzd1-c2"
```

`Step` returns an error, so the caller discards the working clone and the durable state
never advances. Every redelivery repeats. The instance is stuck at `compensating`
forever with no trigger able to move it.

### The defect is pre-existing, in a different guise

This is **not** a regression the bundle introduced. Reverting ADR-0168's three
`len(c.s.Tokens) == 0 && c.s.Compensating.ActiveCmdID == ""` conjuncts to the
pre-bundle `len(c.s.Tokens) == 0` and re-running both fixtures:

```
two-record fixture:  AFTER SIBLING DRAINS SCOPE: status=completed activeCmd="" scopes=0 tokens=0
                     WARN trigger rejected on terminal instance … command_id="zzd-c3" outcome=dropped
one-record fixture:  AFTER SIBLING DRAINS: status=completed activeCmd="" scopes=0 tokens=0
                     persisted state remains: status=completed   (undoA never ran at all)
```

So on `main` the same race silently reports `completed` over an unfinished rollback and
drops the walk's command — the exact family ADR-0168 exists to close. ADR-0168 converts
that silence into a loud panic (two records) or a wedge (one record), which is why this
ADR belongs in the same bundle: shipping 0168 without it trades a silent wrong answer
for a crash.

### The same race is already in ADR-0168's own shipped fixtures

`TestCompensationWalkBlocksRootEventSubprocessCompletion` and
`TestCompensationWalkBlocksNestedEventSubprocessCompletion` drive a compensation throw
inside an event sub-process whose sibling drains the scope. They stopped exactly **one
`Step` short** of the failure. Driven one step further on the unpatched bundle:

```
root:   AFTER DRAIN: scopeID="i-root-esp-s1"   resumeScope="i-root-esp-s1"   scopes=0
        AFTER undoB COMPLETES: err=workflow-engine: defForScope: unknown scope "i-root-esp-s1"
nested: AFTER DRAIN: scopeID="i-nested-esp-s2" resumeScope="i-nested-esp-s2" scopes=0
        AFTER undoB COMPLETES: err=workflow-engine: defForScope: unknown scope "i-nested-esp-s2"
```

Both wedge, on every redelivery. What those tests pinned as an *"ACCEPTED COST: the
fallthrough retires this scope's event-sub-process arms"* was a symptom of the same
premature scope close.

### Why the live read was wrong

`cursorRecords` resolved the walk's records **on every advance**, from state the walk
does not own. A cursor is a promise about a sequence: "I am at index *i* of *these*
records, counting down." Re-deriving the sequence each step makes that promise depend on
every concurrent writer of `s.Scopes` — sibling drains, error-boundary teardowns,
event-sub-process interrupts. The index was stable; the thing it indexed was not.

`ScopeID`/`ResumeScope` have the same shape of bug one level up: they are *names* to be
resolved later, and the referent can be deleted between naming and resolution.

## Decision

**A compensation throw walk owns its record source, and the scope it will resume into
either outlives it or is recognised as gone.** Four parts, each independently exercised.
(Part 4 was added at the delivery gate, 2026-08-10; the ADR originally shipped with three
and recorded the two routes part 4 closes as accepted bounds.)

**1. The record source is PINNED onto the cursor at walk start.**
`compensationCursor` gains `Records []CompensationRecord`, set by
`startCompensationWalk` from a `cloneCompensationRecords` snapshot.
`cursorRecords` prefers it over any live read. The walk therefore iterates the exact
sequence it committed to, regardless of what happens to `s.Scopes`.

It is set for **throw** walks only. The `beginCompensation` family does not pin, and
does not need to: its prologue cancels every token, so no concurrent branch survives to
mutate anything, and its record source is `RootCompensations` (its `scopeID` is the
`const scopeID = ""`), which no scope teardown can nil.

`Records` is the cursor's only non-scalar field, so `cloneState` deep-copies it
explicitly; the `InstanceState` doc comment claiming the cursor needs "no extra
deep-copy code" was corrected in the same change.

**2. A normal scope exit is HELD while a live walk names that scope as its resume
target.** `exitSubprocessScope` gains

```go
if c.s.compensationWalkHoldsScope(currentScopeID) {
    return cmds, true, nil
}
```

immediately after the existing `tokensInScope != 0` check, and returns the same way that
"not drained yet" path does. This is not a stall: the walk's own finish places a token
at the throw's successor **inside that scope**, and that token re-runs the exit once the
cursor is clear — the cursor is cleared by `stepCompensationFinish` before `applyFinish`
resumes, so there is no ordering hazard.

**The predicate is `ResumeScope` alone.** It was written as
`ScopeID == scopeID || ResumeScope == scopeID` and the first disjunct was then measured
to be dead: both throw forms set `ResumeScope` to the throwing token's own scope, so a
scope-wide throw's `ScopeID` is either equal to it or empty. Removing the `ScopeID`
disjunct leaves the engine suite at `EXIT=0`; removing the `ResumeScope` disjunct turns
the targeted-throw case red. The dead disjunct was deleted rather than shipped — the
walk's *records* need no protection from this function, because part 1 pins them.

**3. `stepCompensationAdvance` bounds-checks its source.** The eligibility test gains a
third disjunct, `nextIdx >= len(records)`, so a vanished or exhausted source routes to
the walk's **finish** and never to an index expression.

This element is **reachable and tested**, which was the condition for adding it at all.
It is unreachable for a walk *this build* started — part 1 pins, and `beginCompensation`
cannot lose its source — but it is reachable across a **rolling upgrade**: a walk already
in flight when the process restarted was persisted by the old code, so its cursor
deserializes with `Records == nil`, `cursorRecords` falls back to the live read, and that
read now returns nothing. `TestCompensationAdvanceFinishesWhenRecordSourceIsGone` builds
exactly that state (white-box, because ADR-0171 makes it unreachable by driving the
engine) and panics with `index out of range [0] with length 0` without the disjunct.

`InstanceState` is persisted with plain `json.Marshal`/`Unmarshal`
(`internal/persistence/store/store_core.go`) and no `DisallowUnknownFields`, so adding
`Records` is additive: old rows decode with a nil pin and take the fallback above.

**4. A resume into a scope that no longer exists is DROPPED, not attempted (added at the
gate, 2026-08-10).** `applyFinish` checks `s.scopeByID(plan.resumeScope)` before placing the
resume token; when the scope is gone it logs a warning and places nothing, and — because a
dropped resume can leave the instance with no token at all — completes the instance if
nothing else is running.

Only a compensation **throw** carries a non-empty `resumeScope` (`walkPartial` and
`walkReverse` always resume at the root scope), so this changes no other walk.

The hold in part 2 covers the routes a scope exit can defer. It does not cover the two that
cannot be deferred, and both were measured to place the resume token in a pruned scope, after
which `drive`'s `defForScope` failed and **every subsequent `Step`** returned
`workflow-engine: defForScope: unknown scope …`:

- the **error boundary** on the enclosing sub-process (`propagateError`'s enclosing-scope
  route), previously recorded below as an accepted bound;
- **`exitNestedEventSubprocessScope`**, which closes the ENCLOSING scope when a nested event
  sub-process is the last thing running inside it — previously recorded below as neither
  fixed nor demonstrated. It is now demonstrated:
  `TestCompensationWalkSurvivesNestedEventSubprocessTearingDownItsResumeScope` drives a
  nested **interrupting** event sub-process inside the walk's own scope and, before part 4,
  wedged on `unknown scope "esp-td-s1"`.

Extending the hold to that second site was rejected. On the interrupting route the enclosing
scope's work has just been **cancelled** by the interrupt, so resuming a leftover throw branch
inside it is not the correct outcome even when it is possible; and the error-boundary route
cannot be held at all. Recovering at the resume closes both with one predicate.

⚠ The check is scope **existence**, deliberately, and not "can this scope be resolved". A scope
that still exists but whose opening node has vanished from the parent definition is a corrupt
snapshot, not a race, and still surfaces as an error —
`TestCompensationResumeSurfacesAnUnresolvableScopeAsAnError`.

### Parts 1 and 2 are both load-bearing; neither subsumes the other

Part 2 alone does not save the records, because not every teardown *can* be deferred. An
**error boundary** on the enclosing sub-process must fire, and `propagateError`'s
enclosing-scope route calls `cancelScopeSubtree` + `closeScope(errScopeID)`. Measured on
a fixture where a sibling fails with `E1` mid-walk:

```
WITH the pin:    AFTER ERROR BOUNDARY: scopes=0 activeCmd="zzeb-c3"
                 AFTER WALK ActionCompleted: invokes=[undoA]            ← the record is still compensated
WITHOUT the pin: AFTER WALK ActionCompleted: err=… unknown scope        ← undoA NEVER invoked; the walk skipped it
```

Without the pin the bounds check routes the walk straight to its finish and the
remaining compensation is **silently dropped**. Pinned by
`TestCompensationWalkKeepsItsRecordsWhenAnErrorBoundaryTearsDownItsScope`.

Part 1 alone does not save the *resume*: with records pinned, the one-record and
targeted-throw fixtures still finish into a scope that no longer exists and wedge on
`defForScope`. Only the hold prevents that.

## Consequences

**Positive.**

- The engine core no longer panics on this race. For a library, that is the whole point:
  the panic ran in the consumer's process.
- A sibling branch draining its scope mid-walk is now ordinary: the walk completes every
  record in reverse order, resumes at the throw's successor, and the deferred scope exit
  then runs and completes the instance. Measured for all three shapes (two records, one
  record, targeted throw): `[undoB undoA]` / `[undoA]` / `[undoA]`, each ending
  `status=completed tokens=0`.
- **ADR-0168's "ACCEPTED COST" is retired.** With the exit held, the two event
  sub-process fixtures keep their arms (`EventTriggeredSubprocesses` stays 2 instead of
  going 2 → 0) and the instance completes normally once the walk drains. ADR-0168's
  Consequences are amended accordingly; the loss it recorded was a consequence of the
  premature close, not of deferring completion.
- ADR-0170's deferral is unaffected. Measured on a scope-wide walk that takes an
  unhandled error mid-flight, with the pin, without the pin, and without the hold — all
  three byte-identical: the walk drains `undoB` then `undoA`, `applyFinish` consumes the
  pending outcome, `status=failed`, `RootCompensations` 0. **No strand, no double-walk,
  no behavioural delta.** Reverting ADR-0170's own guard still turns its three tests red,
  so its coverage is intact.

**Negative / accepted.**

- ✅ **CLOSED AT THE GATE (2026-08-10) — these two bullets used to read "an error-boundary
  teardown over a live walk still strands the walk's resume" (pinned as a `KNOWN LIMITATION`
  assertion) and "`exitNestedEventSubprocessScope`'s close of the ENCLOSING scope is not
  held … neither fixed nor demonstrated".** Both are closed by Decision part 4. The
  `KNOWN LIMITATION` assertion in
  `TestCompensationWalkKeepsItsRecordsWhenAnErrorBoundaryTearsDownItsScope` — which pinned
  `defForScope: unknown scope "bteardown-s1"` — is replaced by assertions that the finish
  succeeds and the boundary's own handler branch keeps the instance running. The second
  route was demonstrated first
  (`TestCompensationWalkSurvivesNestedEventSubprocessTearingDownItsResumeScope`, RED on the
  wedge) and then closed by the same predicate.
- **The recovery is a DROP, not a relocation.** A resume whose scope is gone places no token
  anywhere: the throw branch simply ceases to exist. Whatever destroyed the scope has
  already routed the process onward (a boundary target, or the grandparent resume), so in
  practice something is still running; when nothing is, the instance completes. It is not
  possible to place the resume token in an ancestor scope instead — the resume node belongs
  to the destroyed scope's own definition.
- **ADR-0168's third conjunct lost its demonstrating test.** Reverting the conjunct at
  `exitNestedEventSubprocessScope` now leaves the engine suite at `EXIT=0`, because this
  ADR's hold returns before that site is entered by the fixture that used to reach it.
  Coverage for its *second* site was rebuilt here — `TestRootEventSubprocessExitBlocksCompletionUnderRootWalk`
  drives a **root-scope** walk (`ResumeScope == ""`, which the hold never matches) while
  an unrelated root-level event sub-process drains and exits; reverting conjunct 2 turns
  it red. Conjunct 1 (`exitRootScope`) was never affected: reverting it still turns
  `TestCompensationWalkInFlightBlocksCompletion` and
  `TestCompensationWalkFinishCompletesInstance` red. **Conjunct 3 is the one left
  untested**, and is on the backlog rather than removed — undemonstrated is not the same
  as unreachable, which is the precise error ADR-0168 itself had to amend.
- **A held scope inherits the stalled-walk hang.** ADR-0168 already accepts that a walk
  whose result never arrives hangs the instance with no operator escape. Under this ADR
  the sub-process scope stays open for that duration too. It is the same hang, one
  structure wider; the owed operator-escape ADR covers both.
- **The cursor is no longer all-scalar.** `Records` duplicates the drained prefix for the
  walk's duration, so an in-flight walk's persisted snapshot grows by roughly the size of
  the records it is walking (each carrying an `Input` variable snapshot). Bounded by walk
  duration and by the scope's compensable-activity count.
- **The pin's defensive copy is not behaviourally pinned.** Replacing
  `cloneCompensationRecords(records)` with a bare `cursor.Records = records` leaves the
  suite at `EXIT=0`, because nothing currently mutates a stored record's `Input` in place
  (`compensationInvoke` already `copyVars`es it) and `cloneState` re-separates the two on
  the next `Step`. The copy is kept anyway — it is the same snapshot-boundary discipline
  the four other `cloneCompensationRecords` call sites use, and aliasing a slice whose
  owner keeps appending is the classic append-aliasing footgun. Disclosed rather than
  claimed as verified. `cloneState`'s deep copy of the field **is** pinned, by
  `TestCloneStateDeepCopiesCompensatingRecords`.

**Deliberately not addressed.**

- The **operator escape from a stalled compensation walk** (ADR-0168's sharpest cost) —
  unchanged here.
- The **event-sub-process arm hole in both directions**, including the shipped
  `!= StatusRunning` predicate at `engine/step_eventsubprocess.go`'s
  `fireEventTriggeredSubprocessArm` — unchanged here.
- **`processtest.Classify` returning `ReasonUnknown` for a zero-token `Compensating`
  instance** — this ADR widens the window in which that state exists (a held scope keeps
  the instance parked slightly longer) without changing the classification.
- **Targeted-throw archive growth.** A sub-process exiting mid-walk appends to
  `ArchivedCompensations[nodeID]`, and a targeted throw's finish deletes the whole key.
  Whether that can lose a sibling-appended record was **not** measured here and is not
  claimed either way.
