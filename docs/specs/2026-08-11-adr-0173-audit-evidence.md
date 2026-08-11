# ADR-0173 — rule-#9 audit evidence

Companion to
[`2026-08-11-compensation-walk-record-ownership.md`](2026-08-11-compensation-walk-record-ownership.md)
(§8 carries the adjudication) and
[ADR-0173](../adr/0173-a-compensation-walk-finish-consumes-exactly-what-it-drained.md).

Three Opus auditors, each in its own `git worktree` at the pre-implementation
bundle commit, each briefed to **attack and to EXECUTE** rather than read. Lenses:
(1) the teardown/archive seam, (2) cursor indices and persistence, (3) callers and
test falsifiability.

⚠ **Why this file exists.** The bundle originally cited the auditors' own
`AUDIT-LENS{1,2,3}.md` write-ups in their worktrees. Those worktrees were removed
at merge, taking the write-ups with them, leaving three dangling citations in a
shipped spec and ADR. The delivery immediately before this one committed its audit
record in-repo for exactly this reason
(`2026-08-08-adr-0168-0170-audit-evidence.md`); this delivery failed to copy that
practice and is doing so retroactively. **Verbatim tool output below is reproduced
from the audit reports; the surrounding prose is a summary, not a transcript.**

---

## Findings that CHANGED the design

### 1. CRITICAL — a pre-ADR-0171 cursor regressed into permanent LOST compensation

Found **independently by all three lenses**, which is the strongest convergence an
audit has produced in this repo.

Decision 2 as first written rested on *"the archived head is records the walk **will
still dispatch** from its pinned snapshot"*. A cursor persisted before ADR-0171 has
**no pinned snapshot** — ADR-0171's own bounds-check comment names that state as
reachable after a process restart. The teardown parked the un-dispatched head under
a window (nothing in the predicate inspected `Records`), `cursorRecords` fell back
to a live read the teardown had just nilled, `stepCompensationAdvance`'s
`nextIdx >= len(records)` disjunct routed the walk straight to its finish, and the
finish then deleted the window anyway.

```
spike:  PRE-0171 CURSOR: NextIndex=1 StartRecordCount=2 Records=nil dispatched=[undoB]
        POST-TEARDOWN:   archive={outer=[undoA] } window{key="outer" off=0 cnt=1}
        AFTER FINISH:    dispatched=[undoB] archive={}
        AFTER CANCEL:    recovered=[]              ← undoA dispatched by nobody, then deleted
main:   AFTER CANCEL:    recovered=[undoB undoA]   ← undoB double-runs, undoA IS compensated
```

Verbatim the trade §3 of the spec rejects option A for. **Fix, verified by
execution:** add `len(cur.Records) > 0` to the partition predicate — the pre-0171
cursor returns to `recovered=[undoB undoA]`, every other route stays fixed.

Lens 2 reached the same conclusion by a second route: ADR-0171's scope-exit hold
keys on `ResumeScope` while Decision 1 keys on `ScopeID`. Equal for cursors this
build writes, **not** for a pre-0171 one (`ScopeID` set, `ResumeScope` empty — the
exact state `step_compensation_cursor_migration_test.go` builds), so a *deferrable*
exit could fire the partition too.

### 2. HIGH — the abandoned-walk Consequence was FALSE

The ADR promised *"an abandoned walk's un-dispatched remainder stays recoverable …
with nothing double-run alongside it"*. That generalised from one fixture: 2
records, abandoned **immediately** after the teardown. Lens 2 varied the **body
shape** — 3 records, one advance into the archived head before the
force-termination — and the head record was dispatched by the walk **and** left in
the archive, because the design removed the window only at a finish that never
happens.

```
AFTER ONE ADVANCE: walkDispatched=[undoC undoB] archive={outer=[undoA undoB]}
AFTER TERMINATE:   status=terminated cursorActive="" archive={outer=[undoA undoB]}
ADMIN COMPENSATE:  dispatched=[undoB undoA]     ← undoB runs twice
```

`main` on the same fixture: `[undoC undoB undoA]`. So the design **reduced the
double-run from 2 to 1 rather than closing it** — while the abandoned-walk route
was the *entire* justification for choosing it over the simpler option A.

**Fix:** consume the window incrementally, in `stepCompensationAdvance`, rather
than at the finish.

### 3. HIGH — a third teardown route, on the call site the documents called impossible

The spec asserted *"the predicate is false at the other `archiveCompensations` call
sites, which name scopes this walk does not own."* False. `cancelScopeSubtree`'s
**descendant loop** reaches the walk's own scope whenever an ancestor is torn down.
Measured on `main` with a walk in a nested scope and a boundary on the enclosing
sub-process:

```
WALK START:    cursor.ScopeID="r3-s2" NextIndex=1 StartRecordCount=2 dispatched=[undoB]
POST-TEARDOWN: archive={outer=[undoOuter undoOuter2] inner=[undoA undoB] }
AFTER CANCEL:  re-dispatched=[undoB undoA undoOuter2 undoOuter]   ← two double-runs
```

The design already handled it; the **false belief left the route untested**.

### 4. HIGH — a third re-entry route, and the only automatic one

`step_errors.go:253` — an unhandled error consults `ArchivedCompensations` and runs
a walk before terminating. Measured on `main`: after the teardown walk, an unhandled
error re-runs `[undoB undoA]`. The bundle named only the two operator-initiated
routes (`CancelRequested`, admin `CompensateRequested`).

### 5. HIGH — the test plan was substantially defective

- **T6 and T7 were VACUOUS** — no prescribed mutation turned either RED. Dropped.
- **`zz_probe4` could not fail as written** (logs only, plus an early `return`), and
  it was the intended basis for T4.
- **2 of 8 mutations were inert, 1 mis-described, 1 missing.** Mutation #2 only
  discriminates when the *other* scope carries **≥ 2 records**; with one it is an
  identity.

---

## Findings accepted without changing the decision

6. **Two predicate conjuncts provably non-discriminating** (lenses 1 and 2).
   `walkThrowScopeWide` is implied — `compensationCursor.ScopeID` is non-empty only
   for a scope-wide throw. `scopeID != ""` is unreachable — the `scope == nil` early
   return fires first for the implicit root. Each dropped individually left the whole
   engine suite at EXIT=0. Both removed.
7. **The empty-map wart is PRE-EXISTING and needs both paths** (all three lenses).
   `archive={}` already occurs on `main` via `applyFinish`'s whole-key delete, so
   nilling only in the new branch would leave it alive one branch over.
8. **"No data migration" is one-directional** (lens 2). Whole-`InstanceState` JSON
   round-trips without `DisallowUnknownFields`, so new-reads-old is safe, but
   **old-reads-new silently drops the three window fields and re-serializes without
   them**, reinstating the double-run.
9. **Enumeration rot in the spec** (lenses 2 and 3): "four other
   `archiveCompensations` call sites" → **five**; `consolidateArchiveIntoRoot` "the
   only drain" → **two** call sites. Conclusions survived; the enumerations did not.
10. **`finishPlan.validate()` gained no invariant.** One was added. ⚠ A second,
    symmetrical one was **refuted on contact** — asserting that `archiveWindowKey`
    and `archiveWindowCount` are set together panicked the suite, because the usual
    finish carries a non-empty key with a **zero** count.
11. **Two further doc comments go false**, including
    `step_compensation.go`'s *"a second throw to the same ref finds len == 0 and
    no-ops"*, which narrows an ADR-0120 contract and belongs in its Amends.

---

## Claims that SURVIVED execution

Load-bearing rather than assumed, each checked by running something:

- **`NextIndex` really is dispatched-and-in-flight** — verified over a 3-record
  walk; all three writers set it in the same call that emits `records[NextIndex]`,
  and `NextIndex=0` on the last record makes the head correctly empty. A *failed*
  compensation action does not shortcut the walk either.
- **The archive slot is append-only between teardown and finish** — the conclusion
  held, though the justification first written for it was wrong on two counts
  (see finding 9). `Status` measured `compensating` across a mid-walk teardown, and
  the only `StatusRunning` writes are `handleStartInstance` and `applyFinish` after
  the cursor is cleared.
- **No persistence projection to update** — `Compensating` round-trips as
  whole-struct JSON and is deliberately absent from `service.ProcessInstance`, so
  the plan's silence on persistence was correct.
- **`cloneState` needs no change**, and nothing in `engine` asserts the cursor's
  field count or uses a positional literal.
- **Decision 3's leading-prefix premise** — the drained records really are the
  slot's leading prefix; `consolidateArchiveIntoRoot` sorts `RootCompensations`,
  never a slot.
- **`drainedCount == 0` is a safe legacy sentinel** — the throw producer's
  `len(records) == 0` guard means a modern targeted walk always has
  `StartRecordCount ≥ 1`.
- **Blast radius is package-local** — **zero** readers of `ArchivedCompensations` or
  `Scope.Compensations` outside package `engine`.

---

## What the audit did NOT catch

Recorded because it is the point of the two later gates:

- `/code-review` found **4 findings**, the sharpest being that
  `finishPlan.archiveConsumed` — a contract the ADR commits to — had **zero
  coverage**, and the controller's own 10-mutation table missed it because that line
  was never mutated. **Sixth consecutive delivery** where the real gate found what
  the design audit did not.
- `/security-review` returned **0 vulnerabilities** but surfaced a real robustness
  **regression against `main`**: two places read a persisted cursor unclamped, and
  `main` absorbs the same input.

Detail for both: the plan's `▶ Progress` block.
