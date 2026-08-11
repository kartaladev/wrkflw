# A dying instance harvests its open scopes, then closes them

**Status:** audited (33 findings adjudicated, §9), ready for implementation
**Date:** 2026-08-11
**ADR:** [ADR-0174](../adr/0174-a-dying-instance-harvests-its-open-scopes.md)
**Plan:** [2026-08-11-dying-instance-harvests-open-scopes](../plans/2026-08-11-dying-instance-harvests-open-scopes.md)
**Audit record:** [2026-08-11-adr-0174-audit-evidence.md](2026-08-11-adr-0174-audit-evidence.md)
**Supersedes a claim in:** [ADR-0162](../adr/0162-scope-teardown-cascades-to-descendants.md) Consequences

---

## 1. What this delivery is, in one sentence

When an instance's fate becomes terminal, every open scope's compensation records
are **harvested** into `ArchivedCompensations`, and only then are the scopes
**closed** — so that no completed compensable activity is stranded, and no
terminal snapshot carries a zombie `Scope`.

Both halves of that invariant are violated on `main` today.

## 2. How this item was framed, and why the framing was too small

`docs/plans/HANDOVER.md` carried this as *"zombie scopes — ADR-0162 ships a stale
sentence claiming `endInstance` closes them; it never touches `s.Scopes`"*, listed
alongside a documentation-retention item. Source-verified: the sentence really is
stale (§3.1), and `endInstance` really never touches `s.Scopes`
(`engine/state.go:380-391`, nine statements, none of them mentioning `Scopes`).

But the zombie scope is the **symptom**. Execution (§4) shows the harm is
**stranded compensation records**: on the two most common abnormal-exit routes the
engine skips a compensation walk that would otherwise undo completed work, and the
records then become permanently unreachable.

⚠ **It is a gap, not a violated contract**, and an earlier draft of this spec got
that wrong in two ways at once. It asserted *"ADR-0034 specifies the walk runs
before terminating"* and cited `ADR-0034 §"Terminal unhandled error: run
compensation walk before terminating"` — **a section that does not exist**; the
string is a code comment in `engine/step_errors.go`. ADR-0034's actual Decision
routes the error and cancel paths through the walk **when `RootCompensations` is
non-empty**, and the measured shape has `root=0`, so `main` *conforms*. ADR-0034
never contemplated records sitting in an open scope, so its *intent* is unmet for
that shape. The delivery is still worth doing; the framing overstated it.

### 2.1 The mechanism, in one paragraph

`Scope.Compensations` is the live store for records completed inside an open
sub-process scope. Records move to `ArchivedCompensations[scope.NodeID]` on a
**normal** scope exit, via `archiveCompensations`. A terminal transition is an
**abnormal** exit that never archives. The three readers that decide whether a
**dying** instance has anything to compensate look at `RootCompensations` +
`ArchivedCompensations` and never at an open scope — so an abnormal exit both
skips the walk it would otherwise run and leaves the records permanently
unreachable, since `consolidateArchiveIntoRoot` (the only path **from the
archive** into a root walk's record set) also drains the archive only.

⚠ **Not "every reader".** An earlier draft wrote *"every 'are there records to
compensate?' reader … never at an open scope"*, and that is false:
`compensationRecordsForScope` reads the open scope's live list as a records-exist
decision at `step_nodes.go:1204`, and `step_nodes.go:1160` is a fourth such reader
on the archive. Neither is a dying-instance decision, so the design is unaffected
— but this spec's own grep (`RootCompensations) > 0` / `len(s.ArchivedCompensations)`)
structurally cannot see either, and the next reader's enumeration should not
repeat the gap.

## 3. Source-verified premises

Every claim in this section was read out of the tree at `02b72be`, and every one
was **independently re-derived by all three audit lenses** (§9). Behavioural
claims are in §4 and were **executed**.

### 3.1 ADR-0162's stale sentence

`docs/adr/0162-scope-teardown-cascades-to-descendants.md:299-302` says, of a
terminal snapshot carrying open `Scopes`:

> Closing that is `endInstance`'s job in [ADR-0164](../adr/0164-terminal-transitions-are-one-path.md)
> (delivery 2b). Until 2b lands, this ADR claims the narrower thing that is true.

2b landed as ADR-0164 (merge `583537f`) and `endInstance` does **not** close
scopes. The sentence has been false for 11 ADRs. ⚠ Note it was *honest when
written* — it scoped its own claim correctly and deferred the rest. It rotted
because the deferral target shipped without the deferred work, which is the
failure mode rule #11 exists for.

### 3.2 The three predicate sites — an exact enumeration

| Site | Role | Consequence of the omission |
|---|---|---|
| `engine/step_errors.go:253` | unhandled error: compensate-or-fail | walk skipped, instance fails immediately |
| `engine/step_triggers.go:213` | cancel: compensate-or-terminate | walk skipped, instance terminates immediately |
| `engine/step_compensation.go:91` (`hasCompensationRecordsToWalk`) | admin rollback guard | rollback refused as *"nothing left to compensate"* |

Two further grep hits are internal guards on `consolidateArchiveIntoRoot` /
`dropArchiveRecordAt` (`state_compensation.go:447`, `:491`) and are **not**
records-exist decisions. The enumeration is **three**, not five — confirmed by
three independent re-derivations. See §2.1 for the two readers this grep cannot
see.

### 3.3 `archiveCompensations` already does the per-scope work

`(*InstanceState).archiveCompensations(scopeID)` already keys the archive by
`scope.NodeID`, nils the scope's list, early-returns on
`len(scope.Compensations) == 0` (`state_compensation.go:341-343`), and — since
ADR-0173 — partitions the records around a live walk via `partitionForLiveWalk`.
The harvest **reuses** it per open scope rather than reimplementing archival. This
is the single most important design constraint: re-deriving archival would fork
ADR-0173's partitioning.

### 3.4 `endInstance` has ten call sites

`step_compensation.go:978`, `step_compensation.go:1032`, `step_errors.go:267`,
`step_nodes.go:226`, `:355`, `:504`, `:634`, `:636`, `step_triggers.go:260`,
`step_triggers.go:1061`. ADR-0164's "all eight terminal sites" is now **ten call
sites** (some share a terminal path); the count in that ADR is stale but its
*architecture* claim — one terminal path — holds and is what makes a single
backstop possible.

### 3.5 No existing test pins a zombie scope on a terminal instance

The only two assertions requiring a non-empty `Scopes` after a compensation walk
— `engine/step_compensation_walk_completion_test.go:391` (`require.NotEmpty`,
asserting `StatusCompensating`) and `:750` (`assert.Len(…, 1)`, asserting
`StatusRunning`) — both assert on a **non-terminal** instance, and both fixtures
genuinely open a scope. Neither is vacuous, and neither is terminal.

**Independently confirmed the strong way:** two audit lenses implemented the
entire proposal, including `s.Scopes = nil` in `endInstance`, and ran
`go test ./engine/` → **EXIT=0**. The fix does not have to fight the suite.

## 4. Executed evidence (scratch — numbers kept, probes deleted)

Throwaway probes run against `02b72be` under `engine/` (container-free), deleted
after recording. M1–M3 were reproduced **exactly** by an audit lens, including
scope IDs and all three refusal strings. Fixture shape: a sub-process `sub` whose
body completes a compensable service task `inner-svc`
(`CompensateAction: "undo-inner"`), so the record lands in the **open** scope, and
the instance then dies by one of three routes.

### M1 — force-termination end fired *inside* the sub-process

The route ADR-0162 explicitly names as still leaving a zombie.

```
after start:                     status=running    scopes=1 tokens=1
after force-term-inside-sub:     status=terminated scopes=1 tokens=0  terminal=true
  ZOMBIE scope id="i-zombie-s1" node="sub" parent="" comps=1
    record node="inner-svc" action="undo-inner"
  RootCompensations=0  ArchivedCompensations=map[]
rollback: err="workflow-engine: instance is terminal: workflow-engine: invalid
          state transition: nothing left to compensate (status terminated)"
          cmds=0 dispatched=""
```

### M2 — unhandled error inside the sub-process (no boundary handler)

```
mid:                        status=running scopes=1 scopeComps=1 root=0 archived=map[]
after unhandled error:      status=failed  scopes=1 tokens=1 root=0 archived=map[]
  ZOMBIE scope id="i-zombie2-s1" node="sub" comps=1
    STRANDED record node="inner-svc" action="undo-inner"
cmds=1 → engine.FailInstance          ← NO compensation InvokeAction
rollback: err="… nothing left to compensate (status failed)" cmds=0 dispatched=""
```

Note `tokens=1`: this branch deliberately keeps its token
(`step_errors.go:264-266`, so `removeOrphanedIncidents` retains the incidents
whose token survives). That surviving token is the source of the referential
hazard — but see M4: it is **not** the shape where the hazard survives the fix.

### M3 — operator cancel while parked inside the sub-process

```
after cancel: status=terminated tokens=0 scopes=1 root=0 archived=map[]
  ZOMBIE scope id="i-zombie3-s1" node="sub" comps=1
    STRANDED record node="inner-svc" action="undo-inner"
compensationDispatched=""   → engine.FailInstance
```

⚠ The command **count** here is fixture-dependent — with a human-task park an
audit lens measured `cmds=2 [UpdateTask FailInstance]`, because the parked task is
cancelled. The robust criterion, and the one T1/T2 must use, is the **absence of a
compensation `InvokeAction` named `undo-inner`**, not a command count.

### M4 — the referential hazard: where it actually lives

⚠ **An earlier draft of this section recorded a false measurement**, and the
correction is the most instructive thing in this spec. It claimed that on a
terminal instance with `Scopes` nilled and *"harvest excluded"*, an admin rollback
`dispatched="i-zombie4-c3"` and *"undo-inner runs"*. Run under the stated
conditions it is **REFUSED**:

```
M4 after nilling: root=0 archived=map[] scopes=0
M4 cancel-after-clear:   err=<nil> status=failed cmds=0
M4 rollback-after-clear: err="… nothing left to compensate (status failed)"
```

It was false *necessarily*: `Scopes` is the only place the record lives, so
"harvest excluded" plus a nil `Scopes` destroys it. The original probe had
silently seeded `RootCompensations` by hand — i.e. it measured a **harvested**
state while the surrounding prose said harvest was excluded. **A recorded
measurement whose stated conditions differ from what was executed is a false
claim, however real the numbers.**

**Where the hazard actually lives.** After the fix, M2's route no longer reaches
the immediate-failure branch at all: the harvest makes the predicate true,
`beginCompensation` runs, and its prologue cancels every token.

```
M2, patched: status=compensating tokens=0 scopes=1 root=1
             cmds=1 [engine.InvokeAction] invoked=[undo-inner]
```

So the surviving-token hazard exists **only in the no-record shape**, which the
original probe never built:

```
NO-RECORD unhandled error: status=failed tokens=1 scopes=0 root=0 archived=map[]
    token id="i-nr-t2" node="inner-boom" scope="i-nr-s1" state=1   ← names a DEAD scope
  cancel-after-clear:   err=<nil> cmds=0     (dropped by the terminal guard)
  rollback-after-clear: REFUSED "nothing left to compensate"
```

**Is that dangling reference ever resolved?** An audit lens re-derived all 15
`terminalPolicy()` values (exactly **one** trigger — a plain full
`CompensateRequested` — reaches a handler on a terminal instance), then **mutated
`defForScope` to panic** and swept 20 triggers over three cleared-`Scopes`
terminal fixtures carrying surviving in-scope tokens: **zero reaches**. So no
route wedges.

⚠ But the absolute form — *"nothing resolves a token's scope"* — is **refuted**:
with exported readers enabled the panic fires through
`engine.FailingActionName` (`runtime/processdriver_action.go:201`), and
`engine.TargetNode` (`runtime/processdriver.go:814`) is a second, running *before*
`Step`. Both fail soft to `ok=false`, so there is no wedge — but an in-`Step`
enumeration structurally cannot see that class of entry point, and the absolute
sentence must not be restated.

### M5 — what `endInstance` entries actually look like

`endInstance` was instrumented and the whole engine suite run (`EXIT=0`).

**(a) Filtered to `len(s.Scopes) > 0`: four entries, `scopeRecords=0` in all four.**

```
caller=step_triggers.go:260      status=terminated scopes=1 scopeRecords=0 activeCmd="" root=0 archived=0
caller=step_errors.go:267        status=failed     scopes=1 scopeRecords=0 activeCmd="" root=0 archived=0
caller=step_compensation.go:1032 status=failed     scopes=1 scopeRecords=0 activeCmd="" root=0 archived=0
caller=step_compensation.go:1032 status=failed     scopes=1 scopeRecords=0 activeCmd="" root=0 archived=0
```

**(b)** `scopeRecords=0` in all four means the engine suite has **zero coverage of
the harmful shape**. That is why the defect survived ADR-0162, 0164 and 0173, and
it means every test in §7 is new coverage rather than a re-assertion. It does
**not** mean the harvest is a no-op in production: M1–M3 produce exactly the
shape.

**(c) ⚠ The filter manufactured a false premise, and this is the correction.** An
earlier draft concluded from (a) that `activeCmd=""` at *every* `endInstance`
entry, and therefore that a harvest there could never collide with ADR-0173's
window. Instrumented **unconditionally** there are **226** entries and **five with
a live cursor**, all from `forceTerminate` (`step_nodes.go:634`), which clears no
cursor and closes no scope:

```
PROBE_ENDINSTANCE caller=step_nodes.go:634 status=compensating scopes=0 \
  activeCmd="ab-c6" curScope="ab-s1" curRecords=3 startCount=3 nextIdx=1 root=0 archived=1
```

The true statement is *"`activeCmd == ""` at every entry that has an open scope, in
the current suite"* — which, given (b), is a far weaker premise than it looked.
**An instrumentation filter can manufacture the premise it is measuring.**

### M6 — the harvest must precede the cursor clear, and the orderings ARE distinguishable

`partitionForLiveWalk` returns `head + tail` where `head = records[:NextIndex]`,
`tail = records[StartRecordCount:]` — it **drops**
`records[NextIndex:StartRecordCount]`, the records a live walk has already
dispatched. So the drop is what **prevents** re-archiving them.

⚠ An earlier draft claimed the two orderings were *"indistinguishable today"* and
justified the choice as defensive against a *"future"* live-cursor caller. Both
halves were false. The caller is `forceTerminate`, today. Measured on a
force-terminated scope-wide walk over 3 records that had dispatched two
(`NextIndex=1`, `StartRecordCount=3`):

```
harvest BEFORE the clear:  archived=map[sub:[undoA]]              rollback → [undoA]                 ✅
harvest AFTER  the clear:  archived=map[sub:[undoA undoB undoC]]  rollback → [undoC undoB undoA]     ❌
main control:              scopes=1 comps=[undoA undoB undoC]     rollback → REFUSED
```

The rejected ordering re-runs two money-moving actions. Decision stands; its
justification is now the measurement rather than an appeal to hypotheticals.

### M7 — the harvest retires an incident, changing an emitted event payload

On the shape ADR-0164 Decision 3 deliberately preserves — an unhandled error whose
token survives carrying an incident — the harvest makes the records-exist
predicate true, so `beginCompensation` runs, its prologue cancels every token, and
the incident is retired with them:

```
main:    status=failed incidents=1   invoked=[]
patched: status=failed incidents=0   invoked=[undoX]
```

`runtime/outbox.go`'s `terminalEventErr` prefers `Incidents[0].Error`, so the
`instance.failed` payload and `incident_count` both change. Inherent to
compensating before terminating, but it crosses a package boundary and consumers
observe it.

⚠ **Two corrections, both found by executing T12 and both originating in this
section.** They are recorded rather than silently patched because the first one
*caused* a wrong test.

- **The mechanism named here was WRONG.** An earlier draft said the walk cancels
  the token "and `removeOrphanedIncidents` retires the incident". It does not:
  `removeOrphanedIncidents` is called **only** from `endInstance`, and by then
  there is nothing left to sweep. The incident is already gone one Step earlier,
  retired per-token inside the prologue by
  `cancelTokenWaits → s.removeIncidentsForToken(tok.ID)`
  (`engine/step_cancel.go:56`). T12's first draft asserted `Len(Incidents, 1)`
  mid-walk **on the strength of that sentence** and went RED. A false mechanism in
  a spec does not stay in the spec; it becomes a wrong assertion.
- **The token counts do not travel.** An earlier draft recorded `tokens 2 → 0`,
  inherited from the audit's fixture. On T12's three-branch fixture the measured
  value is `tokens=3` on `main`. The **property** (`incidents 1 → 0`, `invoked []
  → [undoX]`) reproduces exactly; the **counts** are fixture-specific. T12 cites
  its own measured numbers rather than restating these.

### M8 — an event-sub-process teardown cannot double-harvest

Two lenses independently: `cancelScopeSubtree` archives the **enclosing** scope
too, so both scopes are already emptied and the harvest's `len == 0` early return
fires. Compensations `[undoDComp undoNComp]`, **once each**. This closes the
obligation a third lens had to leave `ASSUMPTION (unverified)`.

## 5. Design

### 5.1 The new helper

```go
// harvestOpenScopeCompensations moves every OPEN scope's compensation records into
// ArchivedCompensations, keyed by that scope's NodeID, exactly as a normal scope
// exit would. It is the abnormal-exit counterpart of the archival a normal exit
// performs, and exists because a terminal transition closes no scope: without it,
// a record completed inside a still-open sub-process is unreachable forever
// (ADR-0174).
//
// It deliberately does NOT remove the Scope entries. Closing them is endInstance's
// job, which is what makes this safe to call at the two sites where the instance
// may yet keep running — both may still hand off to a compensation walk.
//
// A live walk's already-dispatched records are DROPPED by partitionForLiveWalk, so
// harvesting cannot re-archive them. Note the teardown-window STAMP it writes is
// then discarded by endInstance's cursor clear; only the drop protects, and that
// suffices only because the one site reached with a live cursor is a terminal
// transition where the walk never advances again.
//
// Scopes are visited in slice order, which is parent-before-child (openScope
// appends a child after its parent). Ordering across scopes does not affect the
// resulting walk: consolidateArchiveIntoRoot stable-sorts by (CompletedAt, NodeID).
func (s *InstanceState) harvestOpenScopeCompensations()
```

Implementation: snapshot the scope IDs, then call the existing
`s.archiveCompensations(id)` for each. Iterating IDs rather than the slice keeps it
correct if `archiveCompensations` ever mutates `s.Scopes`.

**Why not fold this into `consolidateArchiveIntoRoot`.** That function runs on
*every* walk, including scope-targeted ones (ADR-0039), where an open sibling
scope's records must **not** be hoisted. Harvest is a *dying-instance* operation;
consolidate is a *walk* operation.

### 5.2 Call sites — three, each placement load-bearing

1. **`handleUnhandledError`** — immediately before the predicate at
   `step_errors.go:253`, and **after** the in-flight-walk guard at `:246`, which
   must keep winning (ADR-0170: a walk in flight *is* the rollback).
2. **`handleCancelRequested`** — immediately before `step_triggers.go:213`, after
   its own in-flight guard at `:163`.
3. **`endInstance`** — the backstop for paths that consult no predicate
   (`forceTerminate`, the completion sites, `handleSubInstanceFailed`), placed
   **before** `s.Compensating = compensationCursor{}` per M6, followed by
   `s.Scopes = nil`.

At sites 1 and 2 the harvest makes the existing predicate correct **without
changing its text** — it already reads `ArchivedCompensations`. That is the point
of harvesting rather than teaching the readers: one helper, not a clause
hand-copied into four readers that a fifth will later omit. (Rejected for exactly
the reason ADR-0165 collapsed eight hand-copied guards into one `terminalPolicy()`.)

### 5.3 NOT done: recovering records already stranded on pre-ADR-0174 rows

An earlier draft harvested inside `beginCompensation` and taught
`hasCompensationRecordsToWalk` to **count** open scopes, so that instances already
terminated with a zombie scope became recoverable. **The audit killed it, and the
owner adjudicated it out.** Measured — a `main`-written row whose open scope
belonged to an **abandoned** walk:

```
main writes the row, new build reads it:
  scope comps=[undoA undoB undoC]      rollback re-dispatched=[undoC undoB undoA]
                                        ← undoB + undoC DOUBLE-RUN; only [undoA] was owed
main baseline: rollback REFUSED
```

It is not fixable by better code: `main`'s `endInstance` zeroed the cursor, so such
a row carries **no record of what was dispatched** and is indistinguishable from a
never-walked row. Buying reachability by introducing a double-run contradicts
ADR-0173, shipped the same day to prevent exactly that.

Both halves were dropped **together**: counting open scopes without harvesting
would admit a walk that then finds nothing and re-stamps the terminal transition
for zero benefit — what ADR-0165's guard exists to prevent.

Dropping it also removed one further High finding, measured: the harvest fired on
**resuming** walks (admin partial rollback, full reverse) on a **running** instance
— partial rollback `[]` → `[undoInner]`, and a later cancel
`[undoRoot undoRoot]` → `[undoInner undoRoot undoInner undoRoot]` — and *created* a
live zombie scope alongside a new one on the same node.

⚠ **CORRECTION.** An earlier draft of this section claimed dropping Decision 4 removed
**two** further High findings, the second being *"an unpinned pre-ADR-0171 live cursor
got no window at all … converting 'permanently lost' into 'run twice'"*. **That was
false.** That finding lives at the `endInstance` harvest (Decision 2's third site), which
Decision 4's deletion never touched, so it survives — see §5.3.1. Both `/code-review`
and `/security-review` re-found it independently at the gate. It is the exact
over-generalising recap sentence Premise Discipline warns about: three findings were
compressed into one dismissal without re-verifying each, and the one that did not belong
went unchecked for a full delivery.

**Consequence, recorded as a bound:** records already stranded on pre-ADR-0174 rows
stay unreachable. Strictly no worse than `main`. Backlog item.

### 5.3.1 BOUND: a pre-ADR-0171 unpinned cursor keeps ADR-0173's accepted double-run

`scopeWideWalkDraining` requires `len(cur.Records) > 0`, so a cursor persisted **before
ADR-0171** (`Records == nil`) bypasses `partitionForLiveWalk` entirely. When such a
cursor is live and `forceTerminate` reaches `endInstance`, the harvest archives the whole
list — the already-dispatched prefix included — and because the archive is then non-empty
a plain full rollback is admitted on the terminal instance and re-dispatches them.
Measured, same input to both trees (cursor `ScopeID=s1, ActiveCmdID=c1, NextIndex=1,
StartRecordCount=3, Records=null`; scope holding `[undoA undoB undoC]` with `undoC` and
`undoB` already dispatched):

```
main:   archive[sub]=[]                       hasRecordsToWalk=false  rollback dispatched []
branch: archive[sub]=[undoA undoB undoC]      hasRecordsToWalk=true   rollback dispatched [undoC]
```

**Accepted, not fixed, deliberately.** The tempting fix — widen
`scopeWideWalkDraining`, or window off the scalar `NextIndex`/`StartRecordCount` when
`Records` is nil — **reverses a shipped ADR-0173 decision**, whose own doc comment
states: *"partitioning on its behalf would delete records nobody ever runs … Deliberate:
losing the record outright is worse."* Without a pinned snapshot those indices are not
trustworthy against a live list, and `TestArchiveCompensationsPartitionRequiresALiveWalk`
guards the adjacent conjunct. Reversing that preference on untrusted indices risks
**deleting compensation records that were never executed**, which is worse than the
double-run.

Scope of the exposure: `main` already reaches `archiveCompensations` with a live legacy
cursor via `cancelScopeSubtree`, so the **class** is pre-existing and accepted in
writing; this delivery adds a fourth route to it. Reachability needs a restart across the
ADR-0171 boundary (merged 2026-08-10, **no release tag exists**) *with* a scope-wide walk
mid-flight, then a force-termination end, then a manual admin rollback. `/security-review`
adjudicated it **REAL-BUT-NOT-SECURITY at 2/10** — no attacker in the loop, no privilege
boundary crossed. Backlog item; a real fix belongs with ADR-0173's cursor-migration story,
not here.

### 5.4 What this does NOT change

- Normal scope exit — `archiveCompensations` is called, not modified.
- Scope-targeted compensation (ADR-0039) — the archive key stays `scope.NodeID`.
- ADR-0173's ownership invariant — a live walk's already-dispatched records are
  still **dropped** by `partitionForLiveWalk`. ⚠ Precisely: the **drop** is what
  protects; the window **stamp** `archiveCompensations` writes is destroyed by
  `endInstance`'s cursor clear on the next line. An earlier draft said "the window
  still applies", which is half true and would mislead a maintainer who later adds
  a harvest at a site where the walk continues.
- ADR-0170's in-flight deferrals — the harvest is inserted *after* both guards.
- An interrupting event-sub-process teardown — cannot double-harvest (M8).

## 6. What breaks

⚠ **Behaviour changes, release-note material.**

| Shape | `main` | After |
|---|---|---|
| Unhandled error inside a sub-process holding records | `failed` immediately, no walk | compensates, **then** fails |
| Operator cancel inside a sub-process holding records | `terminated` immediately, no walk | compensates, **then** terminates |
| Admin rollback on an instance terminated via an `endInstance`-only route | refused, *"nothing left to compensate"* | walks |
| Any terminal snapshot | may carry open `Scopes` | never does |
| `instance.failed` payload where an incident-carrying token survived | `incident_count` ≥ 1, error from `Incidents[0]` | incident retired, payload changes (M7) |
| Persisted `Scopes` on **every** terminal transition | `[]` | `null` |
| Rollback on a `failed` instance with a harvested archive | refused | flips to `terminated`, drops tokens, moves `EndedAt` |

**Migration.** No data migration is required. A new build reading an old row leaves
its stranded records alone (§5.3); an old build reading a new row sees records in
`ArchivedCompensations`, which is where a normal scope exit has always put them,
and `"Scopes":null` where it previously wrote `"Scopes":[]` — inert, since no
reader outside `engine/` touches the field and every reader inside uses
`len`/`range`.

⚠ This spec does **not** claim the stronger "safe in both directions" property. An
earlier draft did, calling it *"the opposite of ADR-0173's mixed-version
position"*; it was the most confident sentence in the bundle and the audit refuted
it (§5.3).

**The `[]` → `null` change is deliberate**, not incidental: `nil` matches the
normalisation ADR-0173 chose for the archive map. It fires on ordinary completions
too, not only on zombie-carrying instances.

## 7. Test plan

Hot paths first (Golang rule #8). Every test states **what makes it fail today** —
and four tests in the audited draft **could not fail**, which is why each row now
also names the mutation that must see it.

| # | Test | Fails today because |
|---|---|---|
| T1 | unhandled error inside a sub-process holding a record dispatches `undo-inner` before failing | today **no compensation `InvokeAction` is emitted** at all (M2). ⚠ Assert the absence/presence of the `undo-inner` invoke, **not** a command count (M3) |
| T2 | operator cancel in the same shape dispatches `undo-inner` before terminating | same, via M3 |
| T3 | force-termination end inside a sub-process leaves the record **archived** under `sub` | today `ArchivedCompensations=map[]`, record in `Scopes[0].Compensations` (M1) |
| T4 | no terminal snapshot carries an open `Scope` | today `scopes=1` on all three routes. ⚠ **As implemented it asserts the force-termination route only** — the error/cancel routes end `compensating` and only reach a terminal status once the walk finishes, so covering them needs the walk driven to completion. The row said "all three routes" before implementation and that was an over-claim |
| T5 | admin rollback walks on an instance terminated via an **`endInstance`-only** route (force-termination) | today refused (M1). ⚠ **Restricted deliberately**: on the error/cancel routes the post-fix instance is `compensating`, not terminal — mid-walk a rollback is swallowed by the in-flight guard (0 commands) and post-walk it is correctly refused because the records were consumed. The audited draft demanded all three routes and was unsatisfiable |
| T6 | *(removed — legacy-row recovery is out of scope, §5.3)* | — |
| T7 | **nested** scopes: on the force-termination route both parent and child archive under **their own `NodeID` keys** | today both stranded. ⚠ Route matters: on the error/cancel routes `beginCompensation` consolidates immediately, so the archive is observably `map[]` and the keys cannot be asserted — assert record presence and reverse order in `RootCompensations` there instead |
| T8 | a dying instance whose open scopes hold **no** records still clears `Scopes`, archives nothing, and leaves `Scopes` **exactly `nil`** | today `Scopes` survives. ⚠ Assert `== nil`, **not** `assert.Empty` — `Empty` passes for both `[]` and `nil` and so cannot see the §6 shape change |
| T9 | an in-flight walk still **defers** at both sites (ADR-0170) | ⚠ the audited draft's falsifier was not one: both guards read only the cursor, so harvest placement above or below them changes nothing they observe. Assert instead on **the record set the deferral leaves behind** |
| T10 | a **force-terminated** scope-wide walk over ≥3 records, advanced ≥1 step, archives only the undispatched remainder | ⚠ re-fixtured. The audited draft used a walk's own *finish*, where the cursor is already zero (`step_compensation.go:709`) so **both orderings compute identically** — verbatim ADR-0173's "recomputes identically" defect. RED for harvest-after-clear: `[undoC undoB undoA]` vs `[undoA]` (M6). The walk must be **advanced**, so the OFFSET moves |
| T11 | on a terminal instance the only `allowOnTerminal` trigger is a plain full rollback, and its walk uses the root scope | ⚠ replaced. The audited draft asserted "stays non-wedging", which **cannot fail** — nothing on a terminal instance resolves a token scope. The structural form breaks if a future trigger is flipped to `allowOnTerminal` |
| T12 | 🆕 the incident/`instance.failed` payload change is pinned (M7) | today `incidents=1`; the fix retires it, and `runtime/outbox.go` reads `Incidents[0].Error` |

Every test is **mutation-verified**: break the production line on purpose, observe
RED, restore from snapshot, `diff`. The mutation must see the **wiring** — delete a
*call site*, not only a body. A mutation that fails to compile is not a RED; one
that cannot discriminate is not verification.

⚠ **A `go test` run that reports `EXIT=0` may be a CACHE HIT.** An auditor's
panic-probe run reported green while the code under it panicked (Go caches on
observed `os.Getenv`). Confirm with `-v` that the test actually ran.

## 8. Audit obligations — status

| # | Obligation | Status |
|---|---|---|
| 1 | enumerate readers of `Token.ScopeID` on a terminal instance | **ANSWERED** (M4): zero reaches in-`Step`; two exported soft readers outside it; no wedge |
| 2 | does harvesting change the ADR-0170 deferral path? | **ANSWERED**: guards read only the cursor; T9 re-specified |
| 3 | `endInstance` backstop reachable with a live cursor? | **ANSWERED — YES** (M5c): `forceTerminate`, five entries |
| 4 | event-sub-process double-harvest? | **ANSWERED — NO** (M8) |
| 5 | verify the counts of three and ten | **ANSWERED**: both correct, three independent re-derivations |
| 6 | attack "no data migration in either direction" | **ANSWERED — claim refuted** (§5.3); the claim is gone |

## 9. Audit adjudication

Three Opus lenses in separate worktrees, all briefed to execute. **33 findings: 1
Critical, 8 High, 1 Medium-High.** Full record, with verbatim output:
[`2026-08-11-adr-0174-audit-evidence.md`](2026-08-11-adr-0174-audit-evidence.md).

**The design changed four times:**

1. **Decision 4 (legacy-row recovery) DELETED** — Critical. It re-ran
   already-compensated actions and the dispatch record is unrecoverable. Owner
   adjudicated (offered accept-and-document / opt-in-admin-operation / drop):
   **drop**. Removed two further High findings with it.
2. **Decision 3's justification replaced** — the orderings are distinguishable
   *today* via `forceTerminate`; "indistinguishable, defensive" was false. The
   decision itself was right.
3. **M5(a) rescoped** — the `len(Scopes) > 0` filter manufactured a false
   universal.
4. **M4 rewritten** — its third measured line was false as labelled, and the
   hazard was mis-attributed to the one shape where the fix eliminates it.

**Four prescribed tests could not fail** (T5 unsatisfiable, T9/T10/T11 vacuous);
all four re-specified above. One new test (T12) was added for a consequence nobody
had noticed.

**Adjudicated as accepted-and-documented rather than fixed:** the incident/event
payload change (M7), the `[]` → `null` persisted shape change, the
`failed` → `terminated` rollback flip, and the stranded-legacy-row bound.

**Rejected findings:** none outright. One was downgraded — a placement
disagreement between two lenses, verified harmless by execution.
