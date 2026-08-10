# A compensation walk's finish consumes exactly the records it drained

**Status**: rule-#9 audit COMPLETE (3 lenses, 11 findings accepted); design corrected; ready to implement
**Date**: 2026-08-11
**ADR**: 0173
**Branch**: `feat/compensation-double-run-on-scope-teardown`
**Base**: `main` @ `1618b29` (ADR-0158 + ADR-0172, merge `fb60df0`)

Handover next-work item 2 / backlog item 4: *"double-compensation when a scope is
torn down mid-walk"*, found by `/security-review` during the ADR-0168–0171
delivery and recorded there as PRE-EXISTING on `main`.

Every claim in §1 and §3 below was produced by **running** a probe against an
unmodified `main` tree and pasting the output. The probes live in
`scratchpad/zz_probe*.go` and the audit's counter-fixtures in
`scratchpad/audit{1,2,3}/`; neither is in the commit. They are the raw material
for §6's tests — ⚠ a probe is not a test until it asserts, see §6.

⚠ **§4 onward was REWRITTEN after the rule-#9 audit.** §8 records what changed and
why. Where §2 and §3 describe the design as first drafted, they are kept as the
decision record; §4 is what ships.

---

## 1. What `main` actually does

### 1.1 The defect as recorded, and the two ways the record was wrong

The backlog entry said: *"Measured on `main` and `HEAD` with 1 record: `undoB1`
dispatched twice"*, naming `CancelRequested` as the route.

Re-measured. Both details understate it:

- with **two** records, **every** drained record re-runs, not one;
- `CancelRequested` is not the only route — an admin `CompensateRequested` on a
  **`completed`** instance reaches it too, and is *admitted precisely because*
  the leaked records make `hasCompensationRecordsToWalk` true (ADR-0164
  carve-out #1).

### 1.2 Route 1 — an error boundary tears down the enclosing sub-process

Fixture: `errorBoundaryTeardownDef` (already in
`engine/step_compensation_scope_drain_test.go`), two compensable records.

```
PRE-TEARDOWN:  scopes=1 scopeID="probe1-s1" scopeRecords=2 root=0 archive=nil
               cursor.ScopeID="probe1-s1" cursor.StartRecordCount=2
POST-TEARDOWN: scopes=0 root=0 archive={outer=[undoA undoB]}
WALK DRAINED:  dispatched=[undoB undoA] status=running archive={outer=[undoA undoB]}
AFTER CANCEL:  re-dispatched=[undoB undoA] → final status=terminated
```

### 1.3 Route 2 — a nested interrupting event sub-process closes the scope

Fixture: `interruptingESPTeardownDef` (same file), one record.

```
AFTER ESP DRAIN:  scopes=0 root=0 archive={outer=[undoA]}
WALK DRAINED:     status=completed archive={outer=[undoA]}
ADMIN COMPENSATE ON THE COMPLETED INSTANCE: re-dispatched=[undoA]
```

`undoA` had already been dispatched when the walk started, so this is a genuine
second run. The instance is `completed`, and the surviving archive is what lets
the rollback in.

### 1.4 Control — a normal sibling drain is NOT affected

Fixture: `scopeWideDrainDef([]string{"A","B"})`.

```
CONTROL after walk: first=[undoA] status=completed scopes=0 root=0 archive=nil
```

ADR-0171's `compensationWalkHoldsScope` defers that scope exit until the walk has
finished and cleared its own prefix, so nothing is archived behind it. This
control is load-bearing: it is what proves the defect is about teardowns that
**cannot** be deferred, not about scope exits in general.

### 1.5 Mechanism

`startCompensationWalk` pins the walk's records onto the cursor (ADR-0171) but
the **live** list stays in the scope. Compensate-once bookkeeping happens only at
`stepCompensationFinish`, via `clearRecordsPrefix(s, cur.ScopeID, StartRecordCount)`.

When a teardown that cannot be deferred runs mid-walk:

1. `archiveCompensations(scopeID)` appends the scope's **whole** live list —
   including the prefix the walk has already committed to — to
   `ArchivedCompensations[scope.NodeID]`, and nils the scope's list;
2. `closeScope(scopeID)` removes the scope;
3. at finish, `clearRecordsPrefix` looks the scope up, gets `nil`, and **no-ops**;
4. any later walk calls `consolidateArchiveIntoRoot`, which folds the archive
   into `RootCompensations`, and re-dispatches them.

### 1.6 The mirror-image defect on the targeted branch

While reading the same finish, a second defect was found and reproduced. A
**targeted** throw's finish runs `delete(s.ArchivedCompensations, archiveKey)` —
the whole key — although `archiveCompensations`'s own doc comment states that a
sub-process entered more than once accumulates both visits into that one slot.

Fixture (new): `start → sub → fork ⇒ { rb(CompensateThrow ref="sub") → endA ;
sibling(UserTask) → sub }`, so the sibling re-enters `sub` while the walk from
the first visit is outstanding.

```
AFTER 1ST VISIT:   archive={sub=[undoInner]} cursor.ArchiveKey="sub" pinned=1 dispatched=[undoInner]
AFTER 2ND VISIT:   archive={sub=[undoInner undoInner]} (walk still pinned at 1, deferred throws=1)
AFTER WALK FINISH: archive={} root=0 dispatched=[]
```

The second visit's record — genuinely uncompensated — is **silently deleted**,
and the deferred throw that pops at finish finds an empty slot.

This is ADR-0120 review A1's rule (*clear only the prefix you drained; a record a
concurrent sibling appended mid-walk must survive*) present on the scope-wide
branch and **absent** on the targeted one.

### 1.7 One invariant covers both

- scope-wide + torn-down scope → the finish clears **less** than it drained
  → double-compensation;
- targeted + concurrent re-entry → the finish deletes **more** than it drained
  → lost compensation.

> **A compensation walk's finish consumes exactly the records that walk drained —
> no more, no less.**

Compensation actions are nowhere in this repo required to be idempotent, so a
double-run is a real integrity impact (a refund applied twice); a lost record
leaves committed work permanently un-rolled-back.

---

## 2. Options considered

**A. Skip at archive time.** `archiveCompensations` refuses to archive the prefix
a live scope-wide walk committed to. One function, no new persisted state.
**Spiked and measured**: routes 1–3 clean, control unchanged, `./engine` EXIT=0.
Rejected on the evidence in §3 — it drops records the walk never dispatched.

**B. Delete from the archive at finish.** Symmetric to `deleteArchive`. Needs the
scope's NodeID *and* an offset on the cursor anyway (a slot accumulates visits),
so it costs the same persisted state as D while fixing strictly less.

**C. Clear each record as it is dispatched.** Removes the class rather than the
instance, but rewrites ADR-0120's index-based tail-retention rule, changes what a
mid-walk cancel sees, and is the largest blast radius on the hottest compensation
path. Rejected as disproportionate.

**D. Split the live list by what the walk has actually dispatched, and park the
remainder in the archive under a window.** **Chosen.** ⚠ As first drafted, D
removed that window only at the walk's *finish*; the rule-#9 audit refuted that
with a fixture reaching a finish that never happens, so the shipped form consumes
the window incrementally as the walk dispatches. §4.2.

---

## 3. Why option A was rejected — the abandoned-walk measurement

Option A drops the **whole** `StartRecordCount` prefix, on the premise that the
walk always drains it from its pinned snapshot. That premise has a reachable
counterexample.

`NextIndex` is the index of the record currently **in flight**; the walk counts
down. So at any instant, `[NextIndex .. StartRecordCount-1]` have been dispatched
and `[0 .. NextIndex-1]` have **not**. A route that abandons the walk between the
teardown and its drain therefore strands the un-dispatched head.

Such a route exists: a **force-termination end event** (ADR-0119). `forceTerminate`
documents that it deliberately runs no compensation, and `endInstance` clears the
cursor. Fixture (new): `errorBoundaryTeardownDef` whose boundary recovery branch
ends at `event.NewEnd("endKill", event.WithForceTermination("killed", event.OutcomeAbort))`.

Measured, `main`:

```
WALK START:       dispatched=[undoB]  NextIndex=1  StartRecordCount=2
POST-TEARDOWN:    archive={outer=[undoA undoB]}  cursorActive="abandon-c3"  NextIndex=1
AFTER TERMINATE:  status=terminated  cursorActive=""  archive={outer=[undoA undoB]}
ADMIN COMPENSATE: dispatched=[undoB undoA]
```

Measured, option A:

```
POST-TEARDOWN:    archive=nil
AFTER TERMINATE:  status=terminated  archive=nil
ADMIN COMPENSATE REFUSED: workflow-engine: instance is terminal: … nothing left to compensate (status terminated)
```

So option A trades one integrity defect for another: `undoB` stops
double-running, but `undoA` — dispatched by nobody — becomes unrecoverable.
Option D is correct on both dimensions (§5.4).

---

## 4. Design

> ⚠ **This section was rewritten after the rule-#9 audit.** Three Opus auditors,
> each in its own worktree, each executing rather than reading, returned one
> Critical and five High/Medium findings that changed it. §8 records the
> adjudication; the design below is the corrected one, and every number in §5 was
> re-measured against it.

The invariant, unchanged: **a compensation walk's finish consumes exactly the
records that walk drained — no more, no less.** What changed is *when* the
consuming happens.

### 4.1 Decision 1 — a mid-walk teardown archives only what the walk has not dispatched

In `archiveCompensations(scopeID)`, when a **pinned** scope-wide compensation
throw walk owns exactly this scope, partition the live records at the walk's own
indices:

| range | meaning | disposition |
|---|---|---|
| `[NextIndex .. drained-1]` | dispatched by this walk | **dropped** |
| `[0 .. NextIndex-1]` | committed to, not yet dispatched | **archived**, windowed (§4.2) |
| `[drained ..]` | appended by a live sibling mid-walk | **archived**, unwindowed |

`drained = min(StartRecordCount, len(records))`; `NextIndex` — the index of the
record currently **in flight**, the walk counting down — clamped into
`[0, drained]`.

The predicate is exactly three conjuncts, extracted as
`(*InstanceState).scopeWideWalkDraining(scopeID)`:

```go
cur.ActiveCmdID != "" && len(cur.Records) > 0 && cur.ScopeID == scopeID
```

- **`len(cur.Records) > 0` is the audit's Critical fix**, not a defensive extra.
  A cursor persisted before ADR-0171 carries no pinned snapshot, so §4.2's whole
  premise — *the walk will still dispatch the archived head* — is false for it:
  `cursorRecords` falls back to a live read the teardown just nilled, and
  `stepCompensationAdvance`'s documented `nextIdx >= len(records)` disjunct routes
  the walk straight to its finish, which then deletes a head nobody dispatched.
  Measured without the conjunct: `recovered=[]`. With it, the walk is left
  entirely alone and behaves exactly as on `main`. See §7.3 for the bound this
  leaves open.
- **`walkThrowScopeWide` and `scopeID != ""` were REMOVED as provably
  non-discriminating.** `compensationCursor.ScopeID` is non-empty only for a
  scope-wide throw (`beginCompensation` writes the const `""`; the targeted caller
  passes `{ArchiveKey: ref}`), so `ScopeID == scopeID` with a non-empty `scopeID`
  already implies the mode; and `scopeID != ""` sits *after* `archiveCompensations`'s
  `scope == nil` early return, which the implicit root scope always takes. Two
  auditors independently dropped each conjunct and measured the entire engine suite
  at EXIT=0 with zero behavioural difference. A conjunct nobody can fail is an
  untested guard, and this repo's rule is to delete it and say why rather than keep
  it for decoration.

⚠ **The spec previously claimed the predicate "is false at the other
`archiveCompensations` call sites, which name scopes this walk does not own."
That was FALSE**, and it is the reason a whole route went unnoticed — see §4.1a.
There are **five** other call sites, not four (`engine/step_cancel.go:102,114`;
`engine/step_nodes.go:311,462,537`).

### 4.1a Route 3 — an ancestor teardown reaches the walk's scope through the descendant loop

`cancelScopeSubtree` archives the named scope **and then every descendant in
`s.Scopes` slice order**. When a walk is running in a *nested* scope and an
**ancestor** is torn down, the descendant loop calls `archiveCompensations` on the
walk's own scope. Measured on `main` with a walk in `inner` and a boundary on
`outer`:

```
WALK START:    cursor.ScopeID="r3-s2" NextIndex=1 StartRecordCount=2 dispatched=[undoB]
POST-TEARDOWN: archive={outer=[undoOuter undoOuter2] inner=[undoA undoB]}
AFTER CANCEL:  re-dispatched=[undoB undoA undoOuter2 undoOuter]
```

Two double-runs, from a route neither the backlog entry nor the first draft of
this spec knew about. The design handles it correctly — the predicate keys on
`ScopeID`, not on which call site invoked it — but the false belief left it
**untested**, which is the finding. It gets its own test (§6 T9).

### 4.2 Decision 2 — the archived head is consumed AS THE WALK DISPATCHES IT

The archived head is records the walk **will still dispatch**. Leaving them in the
archive until the walk's finish is the same double-run in a new place *for any
route that never reaches that finish*.

The first design removed the window only at the finish. The audit refuted it with
a fixture the author's own probe was too small to reach: three records, a
teardown, then **one advance into the archived head**, then an abandonment.

```
first design: walk dispatched [undoC undoB], ADMIN COMPENSATE → [undoB undoA]   ← undoB twice
main:         walk dispatched [undoC undoB], ADMIN COMPENSATE → [undoC undoB undoA]
```

So the first design reduced the abandoned-walk double-run from two to one rather
than closing it — while the abandoned-walk route was the *entire* justification
for choosing it over the simpler option A (§3). ⚠ The author's probe had two
records and abandoned immediately; this is the "generalised from one fixture"
failure this repo logged in the previous delivery, repeated.

**The corrected design consumes the window incrementally.** Three new scalar
`compensationCursor` fields record where the head landed —
`TeardownArchiveKey` (the closed scope's `NodeID`), `TeardownArchiveOffset`
(`len(slot)` before the append), `TeardownArchiveCount` — and
`consumeDispatchedRecord(idx)`, called at each dispatch, drops
`slot[Offset+idx]` and sets `Count = idx`.

The indices stay simple because **the walk counts down**: the record being
dispatched is always the window's *last* element, so removing it never shifts
anything still inside the window. The finish removes whatever remains (normally
nothing), which keeps the terminate and `consumePendingCancel` paths correct
without a special case.

`TeardownArchiveKey == ""` means *no teardown happened* — the value on every
untorn-down walk and on every pre-0173 persisted cursor.

**The append-only premise this offset rests on survives**, but the justification
originally given for it did not: `consolidateArchiveIntoRoot` has **two** call
sites (`engine/step_compensation.go:312` and the throw producer at
`engine/step_nodes.go:1197`), not one, and `beginCompensation`'s guards test
`s.Status == StatusCompensating && ActiveCmdID != ""`, not "a cursor is live". All
three auditors independently confirmed the conclusion by execution: `Status`
measured `compensating` across a mid-walk teardown, and the only writes of
`StatusRunning` are `handleStartInstance` and `applyFinish` *after* the cursor is
cleared.

### 4.3 Decision 3 — a targeted throw consumes its archive slot as it dispatches

The same rule, applied to the branch whose record source **is** the archive. A
targeted walk pins `ArchivedCompensations[ref]` at start; `consumeDispatchedRecord`
drops each record from that slot as it is dispatched (at walk start too, since
`startCompensationWalk` emits the first one), so the slot always holds exactly the
un-dispatched remainder plus any sibling-appended tail. The finish then deletes the
key only when the slot is empty.

This replaces the finish-time whole-key `delete`, and it subsumes the
prefix-trimming variant this spec previously proposed: with the slot consumed
incrementally there is no offset to compute and no `drainedCount` to plumb.

Gated on `len(cur.Records) > 0` for the same reason as §4.1: a pre-ADR-0171
targeted cursor reads the slot **live** through `cursorRecords`'s `ArchiveKey`
branch, so shrinking it underneath would break its absolute indices. Such a cursor
keeps today's whole-key delete.

⚠ Two doc comments become false the moment this lands and must be amended in the
same bundle: `StartRecordCount`'s *"Zero for every other walk (targeted throw, …)"*
and `step_compensation.go:674`'s *"a second throw to the same ref finds len == 0
and no-ops"* — the latter narrows an ADR-0120 contract and belongs in that ADR's
Amends.

### 4.4 What is deliberately NOT changed

- `clearRecordsPrefix`'s no-op on a missing scope: correct, because Decision 1
  leaves nothing in that list to clear.
- ADR-0119's contract that a force-termination end event runs no compensation.
  This delivery makes the un-dispatched remainder **recoverable by an explicit
  admin rollback**; it does not make `forceTerminate` compensate.
- ADR-0171's record-source pinning. This delivery changes ownership bookkeeping
  around it, nothing about the pinning itself.
- The pre-ADR-0171 cursor's behaviour, deliberately (§7.3).

---

## 5. Measured outcome of the corrected design

From `scratchpad/validated-design.patch` — a design probe, not the implementation.

| # | route | `main` | corrected design |
|---|---|---|---|
| 1 | boundary teardown, 2 records, later cancel | `re-dispatched=[undoB undoA]` | `[]`, `archive=nil` |
| 2 | nested-ESP teardown, admin rollback on the completed instance | `re-dispatched=[undoA]` | refused, `nothing left to compensate` |
| 3 | **ancestor teardown via the descendant loop** (§4.1a) | `[undoB undoA undoOuter2 undoOuter]` | `[undoOuter2 undoOuter]` — only genuinely-uncompensated outer records |
| 4 | **unhandled error re-entering the leaked archive** | re-runs `[undoB undoA]` | `[]`, takes the immediate-fail branch |
| 5 | targeted throw + mid-walk re-entry | `archive={}`, 2nd visit **lost** | tail retained; deferred throw compensates it |
| 6 | abandoned walk, 2 records, immediate | `[undoB undoA]` — one double-run, one recovery | `[undoA]` |
| 7 | **abandoned walk, 3 records, one advance into the head** | `[undoC undoB undoA]` | `[undoA]`; walk had dispatched `[undoC undoB]` |
| 8 | **pre-ADR-0171 cursor + teardown** | `recovered=[undoB undoA]` | identical — the walk is left alone (§7.3) |
| 9 | control: normal sibling drain | `archive=nil` | `archive=nil` |

Rows 3, 4, 7 and 8 exist only because the audit built them.

`go test ./engine/` EXIT=0 throughout. ⚠ Evidence about the **suite**, not the
design: no pre-existing test covered any state in §1, which is why they shipped.

### 5.7 The empty-map wart is PRE-EXISTING, and must be fixed on both paths

⚠ Corrected by the audit. `ArchivedCompensations` as a non-nil **empty map** already
occurs on `main`, produced by `applyFinish`'s existing whole-key `delete` — probe 3
prints `archive={}` on an unmodified tree. So this is not a wart the new code
introduces, and nilling the map only in the new branch would leave the identical
wart alive one branch over. The single helper that removes an archive record nils
the map when its last key goes, and every deletion path routes through it.

⚠ Also corrected: §5.7 previously claimed "nothing reads it wrongly" after
enumerating **two** readers. There are **four**, and the two omitted count map
**keys**, not records.

---

## 6. Test plan, and what makes each test fail

Package `engine`, container-free. Black-box `engine_test`. ⚠ `engine/` mixes
`package engine` and `package engine_test` — `head -1` any existing file before
writing into it.

| # | Test | Fails today because |
|---|---|---|
| T1 | boundary teardown, 2 records, later `CancelRequested` re-dispatches nothing | `[undoB undoA]` (§1.2) |
| T2 | nested-ESP teardown, admin rollback on the completed instance is refused | `[undoA]` (§1.3) |
| T3 | targeted throw + mid-walk re-entry retains and compensates the 2nd visit | `archive={}`, record lost (§1.6) |
| T4 | abandoned walk, 2 records: exactly the un-dispatched head recovers | `[undoB undoA]` (§3) |
| T5 | a sibling record appended mid-walk survives a teardown, in completion order | archived whole → `[undoC]` vs `[undoC undoB undoA]` |
| T8 | `ArchivedCompensations` is nil, not an empty map, after the last key goes | `archive={}` on **both** paths (§5.7) |
| **T9** | ancestor teardown via the descendant loop compensates each record once | `[undoB undoA undoOuter2 undoOuter]` (§4.1a) |
| **T10** | an unhandled error after a teardown walk re-runs nothing | re-runs `[undoB undoA]` (§5 row 4) |
| **T11** | a pre-ADR-0171 cursor is left ALONE by the partition | pins §7.3's bound; RED if the `len(Records) > 0` conjunct is dropped |
| **T12** | abandon AFTER advancing into the head recovers exactly the remainder | `[undoC undoB undoA]`; and RED against the first design (`[undoB undoA]`) |

**T6 and T7 are DROPPED.** Both were prescribed as pinning tests, and all three
auditors independently confirmed that **no prescribed mutation turns either RED** —
they were vacuous. T6's coverage (the normal sibling drain is unaffected) is
already carried by the pre-existing `TestCompensationWalkSurvivesSiblingDrainingItsScope`;
T7's (legacy cursor behaviour) is carried by T11 plus the pre-existing
`step_compensation_cursor_migration_test.go`.

⚠ `zz_probe4` **cannot fail as written** — it only logs, with an early `return` on
the refusal path. T4 must assert, not log. Promoting a probe into a test without
adding assertions is how this repo shipped six unfailable tests in one delivery.

### Mutations owed

Each: break the line, confirm the anchor is **present and compiles**, run
`go test ./engine/... > log 2>&1; echo EXIT=$?`, restore from snapshot, `diff`.

| # | Mutation | Must turn RED |
|---|---|---|
| M1 | drop `len(cur.Records) > 0` | T11 |
| M2 | drop `cur.ScopeID == scopeID` | T9 — ⚠ **only with ≥ 2 records in the other scope**; with one it is an identity (measured) |
| M3 | drop `cur.ActiveCmdID != ""` | *(unknown — if nothing goes RED, the conjunct is undiscriminating and must be deleted or documented as uncovered, not kept)* |
| M4 | archive the whole prefix instead of the head | T4 |
| M5 | skip `consumeDispatchedRecord` at advance | T12 |
| M6 | skip `consumeDispatchedRecord` at targeted walk start | T3 |
| M7 | skip the tail from the archive | T5 |
| M8 | drop the finish's residual-window removal | T1 |
| M9 | restore the unconditional whole-key `delete` | T3 |
| M10 | drop the map-nilling in the shared helper | T8 |

⚠ The spec's previous mutation list had **two inert entries and one
mis-described**; M1/M2's discriminating fixtures come from the audit, not from
the author.

---

## 7. Bounds this delivery does NOT close

Each is measured, and each is a deliberate stopping point rather than an oversight.

**7.1 The targeted branch's abandoned walk.** Decision 3 consumes the slot as the
walk dispatches, so an abandoned targeted walk now leaves exactly its remainder —
the same property Decision 2 gives the scope-wide branch. ⚠ The audit measured the
*first* design as leaving this open; the corrected design closes it, and T3 plus a
targeted-abandon case pin it.

**7.2 Mixed-version deployments.** "No data migration" is true in one direction
only. The store round-trips whole-`InstanceState` JSON with no
`DisallowUnknownFields` on that path, so **new code reading an old cursor** is
safe. **Old code reading a new cursor silently drops the three window fields and
re-serializes without them**, reinstating the double-run. A rolling deploy must
therefore not run pre-0173 and post-0173 engine builds against the same instance
store. Recorded in the ADR's Consequences.

**7.3 A pre-ADR-0171 cursor keeps `main`'s behaviour, including its double-run.**
Such a cursor has no pinned snapshot, so nothing can dispatch its head; the
partition declines and the archive keeps everything. Measured: `recovered=[undoB
undoA]`, identical to `main`. This is the correct choice — the alternative loses a
record outright — but it *is* an uncompensated defect surviving on old rows, and
it is the same population as the ADR-0167 deployment audit. T11 pins it so nobody
"fixes" it by widening the predicate.

---

## 8. Audit record (rule #9)

Three Opus auditors, isolated `git worktree`s, briefed to attack and to **execute**.
Lenses: (1) the teardown/archive seam, (2) cursor indices and persistence, (3)
callers and test falsifiability. Full write-ups: `scratchpad/audit{1,2,3}/AUDIT-LENS*.md`.

**Accepted, design-changing:**

1. **Critical — pre-ADR-0171 cursor regression** (all three lenses, converging
   independently). → `len(cur.Records) > 0` conjunct (§4.1), T11, §7.3.
2. **High — the abandoned-walk Consequence was false** (lens 2). The first design
   reduced the double-run from 2 to 1. → consume-as-dispatched (§4.2/§4.3), T12.
3. **High — route 3 exists** (lenses 1 and 3). → §4.1a, T9.
4. **High — a third re-entry route, the only automatic one** (lens 3). → §5 row 4, T10.
5. **High — the test plan was substantially defective**: T6/T7 vacuous, `zz_probe4`
   unfailable, 2 of 8 mutations inert, 1 mis-described, 1 missing. → §6 rewritten.
6. **Medium — two predicate conjuncts provably redundant** (lenses 1 and 2). → removed.
7. **Medium — the empty-map wart is pre-existing and needs both paths** (all three). → §5.7.
8. **Medium — "no data migration" is one-directional** (lens 2). → §7.2.
9. **Low/Medium — enumeration rot in this spec**: "four call sites" → five; "the only
   drain" → two. → §4.1, §4.2.
10. **Low — `finishPlan.validate()` gains no invariant** (lens 2). → one added.
    ⚠ **A second, symmetrical one was REFUTED by execution**: asserting that
    `archiveWindowKey` and `archiveWindowCount` are set together panicked the suite
    immediately, because the USUAL finish carries a non-empty key with a ZERO count —
    `consumeDispatchedRecord` empties the window as the walk dispatches and only the
    key survives. A plausible symmetry, wrong on contact with the built code.
11. **Low — two further doc comments go false** (lens 3). → §4.3.

**Claims that SURVIVED execution**, and are therefore load-bearing rather than
assumed: `NextIndex` really is dispatched-and-in-flight (verified over a 3-record
walk, including `NextIndex=0` making the head correctly empty); the append-only
premise (for §4.2's corrected reasons); Decision 3's leading-prefix premise; **no
persistence projection to update** — `Compensating` round-trips as whole-struct
JSON and is deliberately absent from `service.ProcessInstance`, so the plan's
silence on persistence was correct; `cloneState` needs no change, and no test
anywhere asserts the cursor's field count or uses a positional literal; a failed
compensation action cannot end a walk early; and **zero readers of
`ArchivedCompensations` or `Scope.Compensations` outside package `engine`** — the
blast radius really is package-local.
