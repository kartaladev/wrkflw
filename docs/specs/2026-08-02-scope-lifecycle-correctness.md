# Scope-lifecycle correctness

- Date: 2026-08-02
- Status: design agreed and rule-#9 audited (round 2, three Opus briefs, 31
  findings). **Split into two deliveries** on the audit's recommendation,
  adjudicated by the owner 2026-08-02:
  - **2a** — ADR-0162 + ADR-0163, branch `feat/scope-lifecycle-correctness`,
    plan `docs/plans/2026-08-02-scope-lifecycle-correctness.md`. Covers §1.1–1.6
    and §3.1–3.2.
  - **2b** — ADR-0164, branch `parked/terminal-transitions`, plan
    `docs/plans/2026-08-02-terminal-transitions.md`. Covers §1.7–1.9, §2.3–2.4
    and §3.3.

  This document stays whole: the nine defects share one root cause, and splitting
  the *design* would hide that. Only the **implementation** is split, for churn
  attribution — ADR-0163 and ADR-0164 each move command sequences independently,
  and one combined diff makes per-expectation adjudication impossible for a
  reviewer.
- Scope: `engine/` only. No new port, no storage, no transport, no public API
  **signature** change.
- Delivery 2 of the three-delivery split. Delivery 1 (ADR-0161, stale-command
  filtering) shipped as merge `bcde851`. Delivery 3 (ADR-0158, signal fan-out)
  ships last and on a clean base, by design.

This delivery fixes **nine** defects that share one root cause and one blast
radius: the engine tears execution down **one node, one token, or one scope at a
time**, while the things being torn down form **trees** — a scope tree, a
token-to-task-to-incident chain, and a set of terminal transitions that each
re-implement the same sweep. Every defect below is an instance of "the teardown
enumerated a level, not a subtree".

Eight of the nine were found by the five Opus audit briefs run over the earlier
packaging (recorded in `docs/plans/HANDOVER.md`). The ninth was found while
designing the fix for the sixth and is reproduced in §1.9.

## 0. Verification method

Every factual claim below was re-checked against `main` @ `17e148b` at design
time. Claims marked **REPRODUCED** were confirmed by running a throwaway test
against the real engine, not by reading code; the fixture and the captured
output are quoted inline so the implementation can recreate them as RED tests.
The scratch files were deleted — the tree is clean — and each becomes a real
test in the plan's phases.

## 1. Problems

### 1.1 An instance can wedge permanently (defect 1)

The three sub-process drain checks enumerate **direct children only**:

```go
// engine/step_nodes.go:304-311 (exitRootEventSubprocessScope), and the same
// shape at :354-362 (exitNestedEventSubprocessScope) and :406-413
// (exitRegularSubprocessScope)
for _, sc := range c.s.Scopes {
    if sc.ParentID == "" && sc.ID != currentScopeID {
        if c.s.tokensInScope(sc.ID) > 0 {   // ← immediate scope only
            hasOtherRootChildren = true
            break
        }
    }
}
```

`tokensInScope` (`state_compensation.go:232-240`) counts tokens whose `ScopeID`
equals the argument exactly. A **grandchild** scope holding the live token is
therefore invisible to all three checks, and the exit proceeds as if the subtree
had drained.

`closeScope` then prunes the subtree transitively (`state_compensation.go:290-310`,
ADR-0130), leaving a live token whose `ScopeID` names a scope that is no longer
in `s.Scopes`. `defForScope` cannot resolve it (`step_state.go:26-27`), `drive`
propagates that error (`step.go:179-182`), and **every subsequent `Step` fails
identically**. The instance is wedged: no trigger can advance it, and no trigger
can terminate it, because every path runs through `drive`.

Repro shape: the live token must sit in a **grandchild** of the scope being
exited. A direct child is already caught — `tokensInScope(sc.ID)` over
`sc.ParentID == currentScopeID` sees it (`step_nodes.go:405-413`) — so the naive
"token one level down" topology does **not** reproduce this. Minimum shape:
`outer[ fork ⇒ { branchA → inner[ deep[ UserTask ] ], branchB → end } ]`, where
branchB drains `outer` while branchA's token is parked in `deep`. The check looks
at `inner` (a direct child of `outer`), finds it empty, and declares `outer`
drained.

This is the most severe defect in the delivery — it is unrecoverable without
direct database surgery on the instance state.

### 1.2 An interrupt leaves descendant scopes running (defect 2)

Both abnormal teardowns match tokens on **exact scope equality** and do not
descend the scope tree:

```go
// engine/step_eventsubprocess.go:189-202 — interrupting event sub-process
var tokensToCancel []Token
for _, tok := range s.Tokens {
    if tok.ScopeID == ea.EnclosingScopeID {   // ← exact match
        tokensToCancel = append(tokensToCancel, tok)
    }
}
for _, tok := range tokensToCancel {
    cmds = append(cmds, cancelTokenWaits(s, &tok, at, CloseKindBoundaryInterrupted)...)
}

// engine/step_errors.go:377-394 — error boundary on an enclosing scope
tokensToCancel := make([]Token, 0, len(s.Tokens))
for _, tok := range s.Tokens {
    if tok.ScopeID == errScopeID {            // ← exact match
        tokensToCancel = append(tokensToCancel, tok)
    }
}
```

`s.Scopes` is a tree: `openScope(nodeID, parentScopeID)` records a `ParentID`
(`state_compensation.go:220-228`) and a sub-process entered from inside another
sub-process nests (`step_nodes.go:535`). So a scope being torn down can have live
descendants whose tokens survive the interrupt.

Reachable in a single delivery today: an interrupting signal boundary on a
root-scope host routes to a `SubProcess`; `drive` enters it, opening scope `S`
with the inner start token at `ScopeID == S` (`step_nodes.go:535-537`). A
root-level interrupting event sub-process then fires on the same signal and
cancels root-scope tokens only — the token in `S` survives. The instance now runs
both the event sub-process and the sub-process the interrupt was meant to kill.

The three other `closeScope` call sites (`step_nodes.go:283,372,423`) are
**normal** exits taken once the scope has drained, so they have no live
descendants and are not affected.

### 1.3 Zombie scopes (defect 3)

After a **root-level** interrupting event sub-process fires, the cancelled
descendant scopes are never closed. The path deliberately keeps the *enclosing*
scope open so the drain code can detect its children
(`step_eventsubprocess.go:185-187`), but for the root scope the enclosing scope is
implicit — there is no `Scope` object — while its real descendant scopes remain
in `s.Scopes` with no tokens and no owner. A **completed** instance can therefore
be committed carrying open `Scopes` entries.

### 1.4 Compensation records are silently dropped (defect 4)

Neither abnormal teardown calls `archiveCompensations` — and, as the design
audit found, neither do **two of the three normal exits** (defect 10, §1.4a).
Exactly one call site archives:

```go
// engine/step_nodes.go:421-423 — normal drained exit
cmds = appendCancelTimers(cmds, c.s.removeEventTriggeredSubprocessArmsForScope(currentScopeID))
c.s.archiveCompensations(currentScopeID)   // ← records survive
c.s.closeScope(currentScopeID)
```

Records live in three places: `s.RootCompensations` for root-level tokens,
`Scope.Compensations` for sub-process scopes (`recordCompensation`,
`state_compensation.go:197-213`), and `s.ArchivedCompensations[nodeID]` once
archived. There are **three** readers, and two of them never inspect a live open
`Scope.Compensations`: the admin/root walk reads `RootCompensations` after
`consolidateArchiveIntoRoot` (`:268-281`), and a targeted `CompensateThrow` reads
`s.ArchivedCompensations[cte.CompensateRef]` directly (`step_nodes.go:1015-1027`).
The third — a **scope-wide** `CompensateThrow` (`walkThrowScopeWide`, ADR-0120) —
*does* read them live, through `cursorRecords` → `compensationRecordsForScope`
(`engine/step_compensation.go:21,28-33`).

So **the archive is the only route by which a sub-process's completed work
survives its scope**, for the two readers that outlive teardown — and abnormal
teardown never takes it. The live-read path matters separately: it is the one
`cancelScopeSubtree` must not strand, because that helper nils
`scope.Compensations` on a scope the event-sub-process path deliberately leaves
**open**. See §3.1's note and the test table's scope-wide-throw row.

Worked case: sub-process `fulfil` = `start → charge → ship → end`, `charge`
compensable with action `refund`, error boundary `OutOfStock` on the `fulfil`
activity. `charge` completes and records `refund` into `S_fulfil.Compensations`.
`ship` throws `OutOfStock` → `propagateError` step 2 → the `consume` closure →
`closeScope("S_fulfil")` prunes the scope and the record with it. The card stays
charged, and neither a later `CompensateRequested` nor
`CompensateThrow(compensateRef: "fulfil")` can find it. Had `fulfil` exited
normally, both would refund.

Precision worth keeping: **root-level teardown loses nothing today.**
`archiveCompensations("")` is already a no-op because `scopeByID("")` is always
nil (the root scope is implicit) and `RootCompensations` is never pruned. The
loss is strictly for non-root scopes.

### 1.4a Two of the three NORMAL exits also drop records (defect 10, found by the audit)

The claim that "the normal exit always archives" was **false**, and the audit
caught it. There are four `closeScope` call sites and exactly **one**
`archiveCompensations` call site:

| `closeScope` site | what it closes | archives? |
|---|---|---|
| `engine/step_nodes.go:283` `exitEventSubprocessScope` | a drained event-sub-process **child** scope | **no** |
| `engine/step_nodes.go:372` `exitNestedEventSubprocessScope` | the **enclosing** scope after an interrupting event sub-process completed | **no** |
| `engine/step_nodes.go:423` `exitRegularSubprocessScope` | a drained regular sub-process scope | yes (`:422`) |
| `engine/step_errors.go:393` | the abnormal error-boundary teardown | **no** — §1.4 |

So a compensable activity that completes **inside an event sub-process** loses
its record on a perfectly **normal** drained exit today, and so does work
completed in a scope that an interrupting event sub-process then replaces.

This is the same defect as §1.4 at two more sites, and it invalidates the premise
§1.4 was originally argued from. Fixing §1.4 alone would leave the bundle
asserting "abnormal teardown should behave like normal teardown" while normal
teardown is itself inconsistent two ways out of three. It is therefore **in
scope**, as a two-line addition: `archiveCompensations` before `closeScope` at
both sites.

### 1.5 Cancelling a token orphans its human task (defects 5 and 7) — REPRODUCED

`cancelTokenWaits` (`step_cancel.go:12-30`) cancels deadline and reminder timers,
the token-keyed in-wait reminder, boundary arms and event-gateway arms, then
consumes the token. It **never touches `s.Tasks` or `s.Incidents`.**

`humantask.Cancelled` is written in exactly three places in the engine —
`state.go:301` (`cancelOpenTasks`, terminal sweeps), `step_timers.go:89`
(deadline breach), and `step_stale_commands.go:165` (ADR-0161's filter, which
only reaches tasks whose `AwaitHuman` was minted **in the same step**). None is
`cancelTokenWaits`. No existing test asserts task state on any boundary path.

The consequence is worse than the queued note recorded, because
`cancelTokenWaits` has **four** call sites, and the most common one is not a
scope teardown at all:

| call site | path |
|---|---|
| `step_boundaries.go:146` | interrupting boundary consumes its host |
| `step_compensation.go:226` | `beginCompensation` cancels in-flight tokens |
| `step_errors.go:389` | error-boundary enclosing-scope teardown |
| `step_eventsubprocess.go:201` | interrupting event-sub-process teardown |

**REPRODUCED** over the existing `interruptingMessageBoundaryDef()` fixture
(`engine/step_boundaries_test.go:49-67`) — `Start → UserTask("work") → End` with
an interrupting message boundary — driving the boundary in a **later step** than
the one that minted the task:

```
after step 1: task state=unclaimed open=true
after step 2: tokens=1 tasks=1
  task id=i1-h1 state=unclaimed open=true
  cmd engine.InvokeAction
UpdateTask commands emitted by step 2: 0
```

The host token is consumed, a token is placed on the boundary's outgoing flow,
and **zero `UpdateTask` commands are emitted**. The task stays `unclaimed` and
open in the consumer's `TaskStore`, still served by `ClaimableBy` / `AssignedTo`,
on a **still-running** instance, with no token left that could complete it. This
is the canonical "human task with escalation" topology named in CLAUDE.md's own
Architecture section, so it is more reachable than any scope defect here.

Defect 7 is the same defect reached through `propagateError`'s enclosing-scope
teardown: a sub-process containing `fork ⇒ {review: UserTask, work: ServiceTask}`
with an error boundary leaves `task … state=unclaimed open=true` on a
**completed** instance. Collapsing that topology into a single step yields
`Cancelled` instead, because ADR-0161's filter catches it — so today the outcome
depends on step granularity, which is not a property any definition author can
see or control.

Incidents are the same shape one field over: `Incident.TokenID` (`state.go:122`)
names the token that failed, and nothing removes an incident when that token is
cancelled. `handleResolveIncident` (`step_triggers.go:919-954`) already tolerates
a missing token — it clears the record and returns without re-invoking — so a
dangling incident is a projection defect rather than a crash: it stays visible on
a terminated or completed instance.

### 1.6 `cancelOpenTasks` hands live engine state to the task store (defect 8)

```go
// engine/state.go:297-306
func (s *InstanceState) cancelOpenTasks() []Command {
    var cmds []Command
    for i := range s.Tasks {
        if s.Tasks[i].IsOpen() {
            s.Tasks[i].State = humantask.Cancelled
            cmds = append(cmds, UpdateTask{Task: s.Tasks[i]})   // ← shallow copy
        }
    }
    return cmds
}
```

The emitted value shares the `Claim` / `Completion` pointees, the `Vars` map and
the candidate/eligibility slices with the record committed as instance state.
Latent only because both in-repo stores copy on ingest (`humantask/memory.go:35`;
the SQL store marshals to JSON) — but `TaskStore` is public API, and a consumer
store that retains the value verbatim would share mutable actor state across that
boundary. `HumanTask.Clone()` is the fix, the same one ADR-0161's filter took
after `/code-review` flagged it there (`step_stale_commands.go:166`).

### 1.7 No terminal transition clears the compensation cursor (defect 6)

`startCompensationWalk` (`step_nodes.go:982-994`) commits a token to a walk:

```go
c.s.consumeToken(tok, c.at)
c.s.Status = StatusCompensating
cmdID := c.s.nextCommandID()
cursor.ResumeNode = resumeNode      // :986
cursor.ResumeScope = resumeScope
cursor.NextIndex = len(records) - 1
cursor.ActiveCmdID = cmdID          // :989
c.s.Compensating = cursor           // :990
```

No terminal transition clears `s.Compensating` — not `forceTerminate`
(`step_nodes.go:478-504`), `handleUnhandledError` (`step_errors.go:246`),
`handleSubInstanceFailed` (`step_triggers.go:830`), nor `handleCancelRequested`'s
immediate tail (`step_triggers.go:216-234`). This contradicts the invariant
`beginCompensation`'s own comment asserts (`step_compensation.go:300-303`: "s.Compensating
is the zero cursor here"), and ADR-0161's `liveAwaiters` comment already names it
as "a separate defect, queued in `docs/plans/HANDOVER.md`"
(`step_stale_commands.go:40-44`).

A step can therefore commit with `Status == StatusTerminated` **and** a live
cursor carrying a stale `ResumeNode`. A later plain `CompensateRequested` passes
the terminal guard — scoped strictly to `t.ReverseNode != ""` at
`step_compensation.go:130` — and `beginCompensation` inherits the stale cursor at
`:306`, so `applyFinish` sets `Status = StatusRunning`, clears `EndedAt` and
places a token at the stale node. Repro: a fork whose first branch reaches a
`CompensateThrow` and whose second reaches an `End(WithForceTermination)`, then
deliver `CompensateRequested`.

### 1.8 Terminal transitions each re-implement the same sweep

Eight sites perform a terminal transition, and they agree on neither the work nor
the order:

| site | status | `EndedAt` | clears cursor | `cancelOpenTasks` | `cancelAllScheduledWork` |
|---|---|---|---|---|---|
| `step_nodes.go:216-220` `exitRootScope` | Completed | ✓ | ✗ | ✗ | ✗ |
| `step_nodes.go:321-324` root ESP exit | Completed | ✓ | ✗ | ✗ | ✗ |
| `step_nodes.go:384-387` nested ESP exit | Completed | ✓ | ✗ | ✗ | ✗ |
| `step_nodes.go:479-504` `forceTerminate` | Terminated/Completed | ✓ | ✗ | ✓ | ✓ |
| `step_errors.go:246-255` unhandled error | Failed | ✓ | ✗ | ✓ | ✓ |
| `step_triggers.go:216-234` cancel, no records | Terminated | ✓ | ✗ | ✓ | ✓ |
| `step_triggers.go:830-838` sub-instance failed | Failed | ✓ | ✗ | ✓ | ✓ |
| `step_compensation.go:766-787` `applyTerminate` | plan's | ✓ | (upstream, in `stepCompensationFinish:567`) | ✓ | ✓ |

⚠ The cursor cell for the last row says **upstream** deliberately.
`applyTerminate` itself contains no cursor assignment; the only clear on that
path is `s.Compensating = compensationCursor{}` in `stepCompensationFinish`
(`engine/step_compensation.go:552-567`), one function earlier. The distinction
matters because this row is what makes "route all eight through `endInstance`"
harmless: the clear `endInstance` performs is **redundant** here, not
conflicting — it re-zeroes a cursor an upstream caller already zeroed. Reading
the row as "`applyTerminate` clears it" would suggest splitting `endInstance`
into a clearing and a non-clearing variant to avoid a double write, which is
exactly the complication the unification exists to remove.

`applyTerminate` is already the shape a unified helper would take. The three
completion sites sweep neither tasks nor scheduled work — which is how the
non-interrupting root event-sub-process arm documented at
`step_eventsubprocess.go:222-225` survives into a terminal snapshot, an outcome
that comment argues is "harmless". `handleSubInstanceFailed` emits `FailInstance`
**before** the task cancels; every other site emits it after.

### 1.9 A partial rollback resurrects a terminal instance (defect 9, NEW) — REPRODUCED

The terminal guard is scoped strictly to reverse intent:

```go
// engine/step_compensation.go:130-133
if t.ReverseNode != "" && s.Status.IsTerminal() {
    return StepResult{}, fmt.Errorf("workflow-engine: cannot reverse a terminal instance (status %v)", s.Status)
}
s.Status = StatusCompensating
```

A **partial rollback** — `NewCompensateRequested(at, toNode)` with a non-empty
`toNode`, a public constructor at `engine/trigger.go:348` — has `ReverseNode == ""`
and slips past. `stepCompensationFinish` classifies it as `walkPartial`
(`engine/state_compensation.go:157-159,180`) and hands the plan to
`applyFinish` (`:683`), whose **shared resume block** — common to all four
resume modes, not a `walkPartial`-specific branch — sets
`s.Status = StatusRunning` (`:719`), `s.EndedAt = nil` (`:724`) and places the
token at the target (`:733`). That the block is shared is the point: no guard
can be added there without changing every resume mode, which is why the guard
belongs at trigger entry.

**REPRODUCED.** Definition `start → svc(charge/refund) → after(ship/unship) →
End(WithForceTermination("abort", OutcomeAbort))`; drive both actions to
completion so the token reaches the force-termination end with both compensation
records intact, then deliver `NewCompensateRequested(at, "svc")`:

```
after force-terminate: status=terminated terminal=true endedAt=2026-08-02T10:00:02Z tokens=0 records=2
partial rollback on terminal instance: err=<nil>
  RESULT status=compensating terminal=false endedAt=... tokens=0
    cmd engine.InvokeAction
```

The terminated instance is now `compensating` with a live compensation action in
flight; completing it resumes the instance at `svc` with `EndedAt` cleared.

**This is a distinct vector from §1.7.** The topology has no `CompensateThrow`, so
the cursor was the zero value — nothing stale was inherited. Clearing
`s.Compensating` on terminal transitions does **not** close it.

An earlier probe using `NewCancelRequested` failed to reproduce, because the
cancel path runs a compensation walk that **consumes** the records
(`records=0`), after which the partial rollback errors with "compensation target
node not found in scope records". `forceTerminate` is the right setup precisely
because it deliberately does not run compensation (`step_nodes.go:476-477`).

**The same hole is open in the documented `ReverseInstance` API**, found during
the design audit. `ReverseInstance(…, WithTargetNode(n))` routes through
`engine.NewReverseToNode` (`runtime/processdriver_reverse.go:104`), which sets
`ToNode` and `RestoreTargetVars` but leaves `ReverseNode` empty
(`engine/trigger.go:369-370`) — so the existing guard never fires for a targeted
reverse either. The facade pre-checks a terminal instance at
`runtime/processdriver_reverse.go:100-102`, but that is a `Load`ed snapshot, and
the engine guard exists precisely to cover the window between it and `Step`.
This makes ADR-0109:232-237's defense-in-depth claim untrue for half the
`ReverseInstance` API; ADR-0164's widening makes it true, and a correction note
is added to ADR-0109.

Note the asymmetry this exposes and preserves: compensating a *completed*
instance with a plain full rollback is a **legitimate admin action** — the
records are still there — and the comment at `step_compensation.go:120-129`
records that internal cancel and error paths re-deliver `CompensateRequested`
against already-terminal instances on purpose. The rule is therefore not "a
terminal instance is immutable" but **"a terminal instance may be compensated,
never resumed."**

## 2. Options considered

### 2.1 Compensation records on abnormal teardown (§1.4)

| option | verdict |
|---|---|
| **A. Archive the doomed subtree before pruning** | **Chosen.** Fixes both abnormal teardowns, and is the only chance to archive on the interrupting event-sub-process path, which keeps the enclosing scope open and whose later `closeScope(parentScopeID)` (`step_nodes.go:372`) does not archive either. Also rescues §1.3's zombie-scope records, which are unreachable today even though the scopes still exist. |
| B. Keep discarding, document in Consequences | Rejected. Ships a delivery whose thesis is "abnormal teardown should behave like normal teardown" while leaving the single most consequential difference in place, as silent data loss with no log, incident or trace. The drafted ADR-0162 proposed this; it is overruled here. |
| C. Archive on the error path only | Rejected. Reintroduces the asymmetry one layer down — two abnormal teardowns with two behaviours, differing on which happens to call `closeScope` — and leaves unfixed the half where records are *permanently* unreachable. |

Accepted cost of A: this is a genuine semantic change on **two** reader surfaces.
Beyond the root walk, a targeted `CompensateThrow(compensateRef: X)` that today
auto-advances on `len(records) == 0` (`step_nodes.go:1017`) will now start a real
walk and emit `InvokeAction`s. Both surfaces get their own test.

### 2.2 Where open-task and incident cleanup lives (§1.5)

| option | verdict |
|---|---|
| **A. Inside `cancelTokenWaits`** | **Chosen.** One edit fixes all four call sites including the reproduced boundary case, and it matches the function's own documented contract — "cancels every wait attached to tok". An open human task *is* a wait attached to the token. Self-deduplicating: `cancelOpenTasks` only touches `IsOpen()` records, so a caller that also sweeps terminally emits nothing twice. |
| B. An explicit `cancelOpenTaskForToken` called per site | Rejected. Four sites to remember, and a fifth teardown added later silently re-introduces the bug — the same structural argument that motivates `cancelScopeSubtree` over per-site fixes. |
| C. A scope-level `cancelOpenTasksInScope` at the two teardowns | Rejected. Leaves the reproduced boundary-on-`UserTask` case — the most reachable instance of the defect — entirely unfixed, and does nothing for incidents. |

Accepted cost of A: the widest behavioural change in the delivery. Every
interrupt path now emits `UpdateTask`, so existing command-sequence assertions
move. That is a test-expectation change, not a regression, and each one must be
inspected rather than mechanically re-baselined.

### 2.3 Terminal-transition unification (§1.7, §1.8)

| option | verdict |
|---|---|
| **A. One helper doing state *and* sweeps, at all 8 sites** | **Chosen.** Makes every terminal transition identical and incidentally closes the completion-path leaks in §1.8, including the root event-sub-process arm currently argued "harmless" — a claim the audit must now re-test rather than inherit. |
| B. State-only helper (status, `EndedAt`, cursor) | Rejected as insufficiently ambitious given the table in §1.8: it would leave three terminal transitions still sweeping nothing. |
| C. Surgical cursor clear at the affected sites, no helper | Rejected. The invariant stays a convention rather than an enforced path, so a ninth terminal transition re-opens it. |

### 2.4 The terminal guard (§1.9)

| option | verdict |
|---|---|
| **A. Reject *resuming* intent: `ReverseNode != "" \|\| ToNode != ""`** | **Chosen.** A plain full rollback (both empty) keeps working, so the deliberate internal re-delivery path at `step_compensation.go:120-129` is untouched. Rejects **up front**, before any compensation action is invoked. |
| B. Reject all compensation against a terminal instance | Rejected. Breaks the documented internal re-delivery behaviour, dragging those call sites into this delivery. |
| C. Guard at the resume site inside `stepCompensationFinish` | Rejected. The rollback's `InvokeAction`s have already fired by then — real side effects execute, and only then does the step error. |

## 3. Design

### 3.1 ADR-0162 — tearing down a scope tears down its descendants

Two state-level helpers next to the existing scope tree in
`engine/state_compensation.go`, one step-level helper in `engine/step_cancel.go`.

```go
// descendantScopeIDs returns scopeID plus every scope transitively nested inside
// it. A single forward pass suffices: openScope always appends a child after its
// parent (ScopeSeq is monotonic and a ParentID must already exist when a scope is
// opened), so by the time a scope is visited its parent's doomed status is known.
//
// It deliberately has NO existence guard. scopeByID("") is always nil because the
// root scope is implicit, so guarding here would make root-level teardown a
// silent no-op. closeScope keeps its own guard for its own reasons.
func (s *InstanceState) descendantScopeIDs(scopeID string) map[string]bool

// closeScopeDescendants prunes every scope nested inside scopeID from s.Scopes,
// KEEPING scopeID itself. The interrupting event-sub-process teardown needs
// exactly this: the enclosing scope stays open so the drain code can detect its
// children, while its descendants must not survive the interrupt.
func (s *InstanceState) closeScopeDescendants(scopeID string)

// tokensInScopeSubtree counts tokens in scopeID and in every scope nested inside
// it. The drain checks need this, not tokensInScope: a grandchild scope holding
// the live token must keep the subtree from being declared drained.
func (s *InstanceState) tokensInScopeSubtree(scopeID string) int
```

```go
// cancelScopeSubtree cancels every token in scopeID and in all its descendant
// scopes, retires their event-sub-process arms, archives their compensation
// records, and returns the CancelTimer commands. It does NOT close the scopes —
// the caller decides, because the interrupting event-sub-process path
// deliberately keeps the enclosing scope open.
func cancelScopeSubtree(s *InstanceState, scopeID string, at time.Time, kind CloseKind) []Command
```

`closeScope` is refactored onto `descendantScopeIDs` so the two cannot drift,
**keeping** its own `if s.scopeByID(scopeID) == nil { return }` guard. This
asymmetry is load-bearing and is the single easiest thing to get wrong in this
delivery: carrying the guard into the helper makes root-level teardown a silent
no-op; removing it from `closeScope` turns `closeScope("")` into an
instance-wide scope wipe.

Archiving iterates `s.Scopes` in **slice order, never map order** — parent before
child, deterministic. `archiveCompensations("")` remains a no-op by construction.

Call-site changes:

- `step_eventsubprocess.go:189-207` → `cancelScopeSubtree(s, ea.EnclosingScopeID, …)`
  followed by `closeScopeDescendants(ea.EnclosingScopeID)`. Post-conditions
  otherwise unchanged: the enclosing scope stays open and the event
  sub-process's child scope is opened afterwards.
- `step_errors.go:377-394` → `cancelScopeSubtree(s, errScopeID, …)` followed by
  the existing `closeScope(errScopeID)`, which now prunes a tree whose tokens,
  arms and records have already been retired.
- `step_nodes.go:304-311, 354-362, 406-413` → `tokensInScopeSubtree(sc.ID)`.

`cancelScopeSubtree(s, "", …)` means "the root scope and every scope in the
instance", which is the correct reading for a root-level interrupting event
sub-process: BPMN interrupting event sub-processes at process level terminate all
other activity in the process.

Cancellation stays `cancelTokenWaits` per token, so deadline and reminder timers,
in-wait reminders, boundary arms and event-gateway arms are retired exactly as
today — only the **set of tokens** widens.

### 3.2 ADR-0163 — cancelling a token cancels its open task and incidents

```go
func cancelTokenWaits(s *InstanceState, tok *Token, at time.Time, closeKind CloseKind) []Command {
    // …existing timer / in-wait reminder / boundary-arm / event-gateway sweep…

    // An open human task is a wait attached to this token. AwaitCommand is the
    // taskID for a UserTask (step_nodes.go:679) and a command ID otherwise,
    // where TaskByID returns nil — so this is a natural no-op for non-task
    // tokens, the same assumption cancelTimersByTaskID already makes at :15.
    //
    // Clone before the record escapes: the command is handed to a
    // consumer-supplied TaskStore while the record it was built from is
    // committed as instance state (ADR-0161, step_stale_commands.go:157-166).
    if task := s.TaskByID(tok.AwaitCommand); task != nil && task.IsOpen() {
        task.State = humantask.Cancelled
        cmds = append(cmds, UpdateTask{Task: task.Clone()})
    }
    // An incident names the token that failed (Incident.TokenID, state.go:122).
    // Cancelling that token must retire it, or it stays visible on a completed
    // or terminated instance with nothing left to resolve.
    s.removeIncidentsForToken(tok.ID)

    // …consume the token…
}
```

Ordering: the task cancel and incident sweep run **before** `consumeTokenAs`, so
`tok.AwaitCommand` and `tok.ID` are read while the token is still coherent.

`cancelOpenTasks` (`state.go:302`) switches its shallow copy to `task.Clone()`.

New helper:

```go
// removeIncidentsForToken drops every open incident raised against tokenID.
// Order-preserving over the remaining records for deterministic output.
func (s *InstanceState) removeIncidentsForToken(tokenID string)
```

### 3.3 ADR-0164 — terminal transitions are one path

```go
// endInstance performs the terminal transition: status, EndedAt, a cleared
// compensation cursor, and the projection sweeps every terminal path owes.
//
// The terminal command is threaded through rather than appended by the caller so
// the emitted order stays [task cancels…, terminal, scheduled-work cancels…] —
// exactly what applyTerminate, handleUnhandledError, forceTerminate and
// handleCancelRequested emit today. Pass nil where a site emits no terminal
// command of its own.
func (s *InstanceState) endInstance(status Status, at time.Time, terminal Command) []Command {
    s.Status = status
    ended := at
    s.EndedAt = &ended
    s.Compensating = compensationCursor{}
    cmds := s.cancelOpenTasks()
    if terminal != nil {
        cmds = append(cmds, terminal)
    }
    return append(cmds, s.cancelAllScheduledWork()...)
}
```

Applied at all eight sites in §1.8's table. Two deliberate normalizations:

1. The three completion sites gain the task and scheduled-work sweeps they lack
   today. The "harmless" claim at `step_eventsubprocess.go:222-225` becomes
   moot — the arm is now retired at completion.
2. `handleSubInstanceFailed` (`step_triggers.go:830-838`) currently emits
   `FailInstance` first; it moves to the canonical position.

`applyTerminate` keeps its `applyPlanRecordClearing(s, plan)` call between the
status assignment and the sweeps — record clearing is walk-plan-specific and does
not belong in the shared helper.

And the guard:

```go
// engine/step_compensation.go:130-133 — a terminal instance may be compensated,
// never resumed. ToNode joins ReverseNode because applyFinish's SHARED resume
// block (:683, resume at :719-733) — which walkPartial reaches via
// stepCompensationFinish's plan — resumes at ToNode, resurrecting the instance
// just as a full reverse would. The block is shared by all four resume modes,
// so it cannot itself carry the guard.
if (t.ReverseNode != "" || t.ToNode != "") && s.Status.IsTerminal() {
    return StepResult{}, fmt.Errorf("workflow-engine: cannot resume a terminal instance (status %v)", s.Status)
}
```

## 4. Testing

Hot paths and their failure branches first (Golang rule #8). Every defect gets a
test that **fails against `main`**; the two REPRODUCED probes in §1.5 and §1.9
become RED tests verbatim, including their fixtures.

| # | test focus | shape |
|---|---|---|
| 1 | instance wedge | 3-level nest, middle scope's token descended, sibling reaches middle scope's end event; assert `Step` still succeeds and no token names an absent scope |
| 2 | descendant tokens cancelled | interrupting signal boundary opens a `SubProcess`, root-level interrupting ESP fires on the same signal; assert the nested token is gone |
| 3 | no zombie scopes | after a root-level interrupting ESP completes, assert `len(s.Scopes) == 0` on the completed instance |
| 4 | records survive teardown | `fulfil`/`charge`/`refund` torn down by an error boundary; assert `ArchivedCompensations["fulfil"]` holds the record **and** that a targeted `CompensateThrow` now walks it |
| 5, 7 | task cancelled on interrupt | the `interruptingMessageBoundaryDef()` probe; assert `Cancelled` + exactly one `UpdateTask`, and the `fork ⇒ {UserTask, ServiceTask}` sub-process variant across a step boundary |
| 10 | the wedge at the unguarded exit | event sub-process body `start(signal) → fork ⇒ { A: SubProcess "inner"[UserTask], B: end }`; branch B drains, `exitEventSubprocessScope` must **not** close while branch A's token sits in the grandchild scope. Assert the **follow-up** `Step` still succeeds — the pre-fix failure is never in the draining step |
| 10 | the archive at the unguarded exit | drive an event sub-process containing a completed compensable activity to its **normal** exit; assert the record is archived under the event sub-process node id |
| 8 | no aliasing | mutate the emitted `UpdateTask.Task`'s `Vars`/`Claim`; assert committed state is unaffected — on `cancelOpenTasks` **and** on the five other emitters (`step_timers.go:90`, `step_triggers.go:379,411,428,628`), whose absence of aliasing is additionally checked by `grep -c 'UpdateTask{Task: \*' engine/*.go` → 0 |
| D7 | branch selection moves | the `fulfil` teardown leaves the archive populated, so a later `CancelRequested` takes the multi-step compensation walk instead of the single-step immediate termination; assert the `InvokeAction` and `StatusCompensating` |
| 6 | no resurrection via stale cursor | fork: branch 1 → `CompensateThrow`, branch 2 → `End(WithForceTermination)`; assert `s.Compensating` is zero, then deliver `CompensateRequested` and assert no resumption |
| 9 | no resurrection via partial rollback | the §1.9 fixture; assert the step errors and state is unchanged |
| — | guard still permits | plain `NewCompensateRequested(at, "")` against a terminal instance still walks, proving the internal re-delivery path is intact |

Load-bearing tests are **mutation-verified**: break the implementation on
purpose, confirm the test fails, restore from a `/tmp` snapshot and `diff`. The
standing lesson is that ADR-0159's review found five tests that could not fail
and certified nothing.

## 5. Explicitly out of scope

- **ADR-0158 signal fan-out** — delivery 3, deliberately last so it lands on a
  base where the defects it amplifies are already fixed.
- **Relocating the scope-tree cluster** (`openScope`, `tokensInScope`,
  `closeScope`, `scopeByID`) out of `state_compensation.go` into a
  `state_scopes.go`. Tempting while adding three neighbours, but it is a
  rename-only churn that would bury the behavioural diff this delivery must be
  reviewable as.
- **Scoped force-termination.** `forceTerminate` remains scope-agnostic
  (`step_nodes.go:470-474`); a force-termination end inside a sub-process still
  ends the whole instance.
- The pre-v0.1.0 blockers listed in `docs/plans/HANDOVER.md` (strict definition
  decoding, MySQL zero `next_run`, `Upsert` claim invariant, ADR-0159's two
  misnamed symbols, the `processtest` boundary-arm park, the pgx-notifier load
  flake).
