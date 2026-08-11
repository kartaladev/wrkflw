# 174. A dying instance harvests its open scopes, then closes them

- Status: Accepted
- Date: 2026-08-11

> Closes the zombie-scope debt [ADR-0162](0162-scope-teardown-cascades-to-descendants.md)
> deferred to ADR-0164 and ADR-0164 never picked up — and, in the course of measuring
> it, the substantially larger defect the zombie scope was a symptom of: completed
> compensable work that is never undone and cannot be reached.
>
> Design and every measurement:
> [`docs/specs/2026-08-11-dying-instance-harvests-open-scopes.md`](../specs/2026-08-11-dying-instance-harvests-open-scopes.md).
> Rule-#9 audit record (33 findings, three lenses):
> [`docs/specs/2026-08-11-adr-0174-audit-evidence.md`](../specs/2026-08-11-adr-0174-audit-evidence.md).

## Context

`Scope.Compensations` is the live store for compensation records completed inside an
open sub-process scope. Records leave it on a **normal** scope exit, when
`archiveCompensations` moves them into `ArchivedCompensations[scope.NodeID]` so that
scope identity survives for scope-targeted compensation
([ADR-0039](0039-scope-targeted-compensation.md)).

A terminal transition is an **abnormal** exit that closes no scope. `endInstance`
(`engine/state.go:380-391`) stamps the status and `EndedAt`, clears the compensation
cursor, sweeps orphaned incidents, cancels open tasks and scheduled work — and never
touches `s.Scopes`. So a terminal snapshot can carry an open `Scope`, and that scope
can carry compensation records.

The three readers that decide whether a **dying** instance has anything to
compensate look at `RootCompensations` + `ArchivedCompensations` and never at an open
scope:

| Site | Role |
|---|---|
| `engine/step_errors.go:253` | unhandled error: compensate, or fail immediately |
| `engine/step_triggers.go:213` | cancel: compensate, or terminate immediately |
| `engine/step_compensation.go:91` (`hasCompensationRecordsToWalk`) | admin-rollback guard |

(Two mid-flight readers *do* consult an open scope — `compensationRecordsForScope` at
`step_nodes.go:1204` and the archive lookup at `:1160` — but neither is a
dying-instance decision. They are noted because a grep for the predicate above cannot
see them, and the next reader's enumeration should not repeat that gap.)

`consolidateArchiveIntoRoot` — the only path **from the archive** into a root walk's
record set — likewise drains the archive only. So an abnormal exit both **skips the
walk it would otherwise run** and leaves the records **permanently unreachable**.

### Measured on `main` @ `02b72be`

Fixture: sub-process `sub` whose body completes a compensable service task `inner-svc`
(`CompensateAction: "undo-inner"`), so the record sits in the **open** scope; the
instance then dies by one of three routes.

**Unhandled error inside the sub-process:**

```
after unhandled error: status=failed scopes=1 tokens=1 root=0 archived=map[]
  ZOMBIE scope id="i-zombie2-s1" node="sub" comps=1
    STRANDED record node="inner-svc" action="undo-inner"
cmds=1 → engine.FailInstance          ← no compensation InvokeAction at all
```

**Operator cancel while parked inside the sub-process** behaves the same, as does a
**force-termination end fired inside the sub-process** — the route ADR-0162 names
explicitly as still leaving a zombie. On all three, a later admin rollback is refused
with a message that is factually false:

```
rollback: err="workflow-engine: instance is terminal: workflow-engine: invalid
          state transition: nothing left to compensate (status terminated)"
          cmds=0 dispatched=""
```

`undo-inner` never runs, and never can. Compensation actions are nowhere required to
be idempotent — nor, here, to be *reachable*: a booking is made and never undone.

⚠ **This is a gap, not a violated contract**, and the distinction was corrected by
the audit. [ADR-0034](0034-compensation-on-error-cancel.md) routes the error and
cancel paths through the compensation walk **when `RootCompensations` is non-empty**;
the measured shape has `root=0`, so `main` *conforms* to ADR-0034 as written.
ADR-0034 never contemplated records sitting in an open scope, so its intent — undo
completed compensable work before terminating — is simply unmet for that shape. An
earlier draft of this ADR cited a section of ADR-0034 that does not exist (the string
is a code comment in `step_errors.go`) and called the behaviour a violated contract.

### Why it survived three ADRs

`endInstance` was instrumented to report what it was handed. Filtered to entries with
an open scope, the whole engine suite yields **four**, and every one reports
`scopeRecords=0`: the suite has **zero coverage of the record-holding shape**, which
is how this passed ADR-0162, ADR-0164 and ADR-0173.

⚠ That filter also manufactured a false premise. An earlier draft concluded
`activeCmd=""` at *every* `endInstance` entry, and therefore that a harvest there
could never collide with ADR-0173's teardown window. Instrumented
**unconditionally**, there are **226** entries and **five with a live cursor**, all
from `forceTerminate` (`step_nodes.go:634`), which clears no cursor and closes no
scope:

```
PROBE_ENDINSTANCE caller=step_nodes.go:634 status=compensating scopes=0 \
  activeCmd="ab-c6" curScope="ab-s1" curRecords=3 startCount=3 nextIdx=1
```

### ADR-0162's stale sentence

ADR-0162's Consequences says of a terminal snapshot carrying open `Scopes`: *"Closing
that is `endInstance`'s job in ADR-0164 (delivery 2b). Until 2b lands, this ADR claims
the narrower thing that is true."* 2b landed as ADR-0164; `endInstance` closes no
scope. The sentence has been false for 11 ADRs. It was **honest when written** — it
scoped its claim and deferred the rest — and rotted because the deferral target
shipped without the deferred work.

## Decision

**When an instance's fate becomes terminal, it harvests its open scopes' compensation
records into the archive, and only then closes those scopes.**

1. **A new `(*InstanceState).harvestOpenScopeCompensations()`** moves every open
   scope's records into `ArchivedCompensations`, keyed by that scope's `NodeID`,
   exactly as a normal exit would. It **reuses** `archiveCompensations` per scope
   rather than reimplementing archival — which is what keeps ADR-0173's partitioning
   un-forked — and it deliberately does **not** remove the `Scope` entries, so it is
   safe at the sites where the instance may yet hand off to a compensation walk.

2. **It is called at five sites**, each placement load-bearing:
   - `handleUnhandledError`, before the predicate at `step_errors.go:253` and **after**
     the in-flight-walk guard, which must keep winning (ADR-0170).
   - `handleCancelRequested`, before `step_triggers.go:213`, after its own in-flight
     guard at `:163`.
   - `endInstance`, as the backstop for the paths that consult no predicate
     (`forceTerminate`, the completion sites, `handleSubInstanceFailed`) — placed
     **before** the cursor clear, then followed by `s.Scopes = nil`.
   - `stepCompensateRequested`, **gated on the walk terminating**
     (`ToNode == "" && ReverseNode == ""`), so an admin rollback of a **live** instance
     compensates work completed inside an open sub-process on the FIRST attempt.
   - `applyFinish`'s deferred re-entry, **after** `applyPlanRecordClearing`, so a
     deferred cancel or deferred error (ADR-0170) does not terminate *around* a sibling
     scope's completed work. No gate: that branch is reached only for a terminal
     outcome.

   At the first two sites the harvest makes the existing predicate correct **without
   changing its text**, because it already reads `ArchivedCompensations`.

   > ⚠ **CORRECTION (delivery gate).** An earlier draft named only the first three, and
   > `/code-review` measured both consequences. On the admin-rollback route the walk
   > dispatched **nothing**, flipped the instance to `terminated`, and only then did
   > `endInstance` archive the record — so a **second** rollback was needed to run
   > `undo-inner`, with the operator told nothing. On the deferred-cancel route the
   > remainder walk terminated with `invoked=[]` and `undoB` archived on an already-dead
   > instance. Both were collateral damage from deleting the original Decision 4
   > *wholesale*: that decision harvested inside `beginCompensation`, which was unsafe
   > for legacy terminal rows and for resuming walks — but two of its four callers were
   > neither. **The terminality gate is what the blanket version lacked**, and it is
   > mutation-verified: removing it reddens the full-reverse guard test.

3. **The harvest precedes the cursor clear**, and that ordering is load-bearing
   **today**, not defensively. `forceTerminate` reaches `endInstance` with a live
   scope-wide cursor (see Context), and `partitionForLiveWalk` **drops** the records
   such a walk has already dispatched. Both orderings were measured on a
   force-terminated 3-record walk that had dispatched two:

   ```
   harvest BEFORE the clear:  archived=map[sub:[undoA]]                rollback → [undoA]
   harvest AFTER  the clear:  archived=map[sub:[undoA undoB undoC]]    rollback → [undoC undoB undoA]
   ```

   The rejected ordering re-runs two money-moving actions. An earlier draft justified
   this decision as protection against a "future" live-cursor caller and asserted the
   orderings were indistinguishable today; both halves were false.

**Rejected: recovering records already stranded on pre-ADR-0174 rows.** An earlier
draft harvested inside `beginCompensation` and taught
`hasCompensationRecordsToWalk` to count open scopes, so that instances already
terminated with a zombie scope became recoverable. Measured, that **re-runs
compensation actions an abandoned walk had already dispatched**: a `main`-written row
re-dispatched `[undoC undoB undoA]` where only `[undoA]` was owed, against a `main`
that refuses the rollback outright. It is not fixable — `main`'s `endInstance` zeroed
the cursor, so such a row carries no record of what was dispatched and is
indistinguishable from a never-walked row. Buying reachability by introducing a
double-run contradicts ADR-0173, shipped the same day to prevent exactly that. Both
halves were dropped together: counting without harvesting would admit a walk that
then finds nothing and re-stamps the terminal transition for zero benefit, which is
what ADR-0165's guard exists to prevent. Dropping it also removed one further
High-severity finding — the harvest firing on **resuming** walks (an admin partial
rollback and a full reverse, on a *running* instance).

> ⚠ **CORRECTION (delivery gate).** This paragraph originally claimed the deletion
> removed **two** further High findings, the second being an unpinned pre-ADR-0171 live
> cursor getting no window. **That was false**: that finding lives at the `endInstance`
> harvest, which the deletion never touched, and it survives — both `/code-review` and
> `/security-review` re-found it independently. It is now stated as an explicit bound in
> the spec's §5.3.1 with its measurement, and left **accepted rather than fixed**,
> because the fix would reverse ADR-0173's documented preference (*"partitioning on its
> behalf would delete records nobody ever runs … losing the record outright is worse"*)
> on indices that are untrustworthy without a pinned snapshot. `/security-review`
> adjudicated it REAL-BUT-NOT-SECURITY at 2/10: no attacker in the loop, the class is
> pre-existing on `main` via `cancelScopeSubtree`, and reachability requires a restart
> across an unreleased one-day-old commit boundary. Three findings had been compressed
> into one dismissal without re-verifying each — the recap failure Premise Discipline
> exists to catch.

**Rejected: teaching the readers instead of moving the data** — leaving `s.Scopes`
alone and adding an open-scope clause to the three predicates plus
`consolidateArchiveIntoRoot`. It fixes the skipped walk with no token hazard, but it
hand-copies one clause into four readers that a fifth will later omit, and it leaves
the zombie scope in every terminal snapshot — so ADR-0162's promise stays open and
this item returns. ADR-0165 spent a whole delivery collapsing eight hand-copied
guards into one `terminalPolicy()`; adding four is the wrong direction.

**Rejected: splitting the zombie removal into a follow-up.** Delivery 2b already
deferred it once, and that deferral is why this ADR exists.

## Consequences

**Positive.**

- **Completed compensable work inside a sub-process is undone** when an unhandled
  error or an operator cancel kills the instance, instead of being silently skipped.
  This is what ADR-0034 intended and never covered for this shape.
- **No completed compensable activity is stranded** by an abnormal exit, and an admin
  rollback that reaches `endInstance` without consulting a predicate now walks
  instead of refusing.
- **No terminal snapshot carries an open `Scope`** — ADR-0162's deferred half, closed,
  and its stale sentence corrected in the same bundle.
- The fix is one helper at three sites, not a clause in four readers.

**Negative / risks.**

- ⚠ **Behaviour change, release-note material.** An unhandled error or cancel inside a
  record-holding sub-process no longer terminates immediately; it compensates first.
  In-flight instances persisted under the old behaviour change behaviour on their next
  terminal transition. That is the fix working.
- ⚠ **An emitted event payload changes.** On the shape ADR-0164 Decision 3
  deliberately preserves — an unhandled error whose token survives carrying an
  incident — the walk now cancels that token and the incident is retired with it:
  measured `incidents=1, invoked=[]` on `main` versus `incidents=0,
  invoked=[undoX]` after. `runtime/outbox.go`'s `terminalEventErr` prefers
  `Incidents[0].Error`, so the `instance.failed` payload and `incident_count` both
  change. This is inherent to compensating before terminating, but it crosses a
  package boundary and consumers observe it. Pinned by T12.

  > ⚠ **CORRECTION (implementation).** An earlier draft of this bullet named
  > `removeOrphanedIncidents` as what retires the incident. It is not:
  > `removeOrphanedIncidents` runs only from `endInstance`, by which point nothing
  > is left to sweep. The retirement happens a Step earlier, per token, in
  > `cancelTokenWaits → s.removeIncidentsForToken(tok.ID)`
  > (`engine/step_cancel.go:56`). T12's first draft asserted the incident was still
  > present mid-walk **because** of that sentence, and went RED — a false mechanism
  > in a design document becomes a wrong test. The draft's `tokens 2 → 0` was also
  > fixture-specific (measured `3` on T12's fixture); the property holds, the counts
  > do not travel.
- ⚠ **Records already stranded on pre-ADR-0174 rows stay unreachable.** Deliberate
  (see the rejected option): recovering them safely needs information the row does
  not carry. Strictly no worse than `main`, and logged as backlog.
- ⚠ **A surviving token can name a closed scope.** On `handleUnhandledError`'s route
  this is confined to the **no-record** shape: the harvest finds nothing, so that
  branch still takes its immediate failure and deliberately keeps its token, whereas
  with records it now enters `beginCompensation`, whose prologue cancels every token
  (`tokens=0`).
  ⚠ **But that is not the only route, and an earlier draft said "only" here and was
  wrong.** `handleSubInstanceFailed`'s tail (`engine/step_triggers.go:1072`) consults
  no records predicate, explicitly does not drop its tokens, and calls `endInstance`
  — so on that route the **with-records** shape also commits a token naming a closed
  scope. The two bullets contradicted each other, since this ADR cites that same tail
  below for the `failed` → `terminated` flip. Found by `/code-review`. Measured
  non-wedging: on a terminal
  instance exactly one trigger reaches a handler (a plain full `CompensateRequested`),
  and it walks the root scope. ⚠ It is **not** true that nothing resolves a token's
  scope — `engine.FailingActionName` (`runtime/processdriver_action.go:201`) and
  `engine.TargetNode` (`runtime/processdriver.go:814`, which runs before `Step`) both
  do, from outside `Step` where an in-`Step` enumeration cannot see them. Both fail
  soft to `ok=false`, so there is no wedge — but the absolute form of that sentence
  was refuted by execution and must not be restated.
- ⚠ **A rollback on a `failed` instance can now flip it to `terminated`**, dropping
  surviving tokens and moving `EndedAt`, because the harvest gives it a non-empty
  archive to walk (reachable via `handleSubInstanceFailed`'s tail). Same class as the
  re-stamp ADR-0165's guard documents.
- The harvest is `O(len(s.Scopes))` per terminal transition, once, on a path that
  already sweeps tasks, timers and arms.

**Neutral.**

- **The persisted `Scopes` shape changes from `[]` to `null`** on **every** terminal
  transition, ordinary completions included: `closeScope` leaves a non-nil empty
  slice, `s.Scopes = nil` does not. `InstanceState` round-trips as whole-struct JSON,
  so this is a persisted-shape change on a hot path. It is inert — no reader outside
  `engine/` touches the field, and every reader inside uses `len`/`range` — and `nil`
  matches the normalisation ADR-0173 chose for the archive map. Chosen deliberately
  rather than by accident, which is why it is recorded.
- **No data migration is required.** A new build reading an old row leaves its
  stranded records alone (see above); an old build reading a new row sees records in
  `ArchivedCompensations`, which is where a normal scope exit has always put them.
  ⚠ Unlike an earlier draft, this ADR does **not** claim the stronger "safe in both
  directions" property — that claim was the bundle's most confident sentence and the
  audit refuted it.
- Normal scope exit, scope-targeted compensation (ADR-0039), ADR-0173's ownership
  invariant and ADR-0170's in-flight deferrals are all untouched. An interrupting
  event-sub-process teardown cannot double-harvest: `cancelScopeSubtree` archives the
  enclosing scope too, so both scopes are already emptied and the harvest's `len == 0`
  early return fires (measured).
