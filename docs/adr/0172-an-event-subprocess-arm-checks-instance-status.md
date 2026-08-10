# 172. An event sub-process arm does not spawn work into a dying instance

- Status: Accepted
- Date: 2026-08-10
- Amends: [ADR-0124](0124-repeatable-noninterrupting.md) Decision item 4

> Ships with [ADR-0158](0158-signal-fires-every-matching-arm.md) in one bundle:
> the fan-out multiplies the number of event-sub-process arms that fire per
> delivery, which is what makes this hole routinely reachable.
>
> Design: `docs/specs/2026-08-10-signal-fanout-and-esp-status.md`.
> Executed premises: `docs/specs/2026-08-10-signal-fanout-premise-evidence.md`
> section C (claims E1–E8).

## Context

`fireEventTriggeredSubprocessArm` (`engine/step_eventsubprocess.go:156`) guards on
instance status for the **root scope only**:

```go
if ea.EnclosingScopeID != "" {
    scope := s.scopeByID(ea.EnclosingScopeID)
    if scope == nil { return nil, nil }        // non-root: scope liveness ONLY
} else {
    if s.Status != StatusRunning { return nil, nil }   // root: a STATUS check
}
```

This ADR was opened against an inherited two-direction description of the defect.
**Re-derivation by execution corrected three of the four inherited claims**, and
the corrections change what is being fixed. They are recorded here rather than
silently applied, per CLAUDE.md's rule on restating inherited claims.

### Correction 1 — the terminal formulation is REFUTED end-to-end

The inherited claim was that a non-root arm "fires into a completed instance".
At the *function* level that is true (evidence E1a): called directly with a live
enclosing scope, the non-root branch opens a child scope, places a token and emits
a live `InvokeAction` on `completed`, `failed` **and** `terminated`, while the root
branch cleanly no-ops.

But **no public `Step` route reaches it** (evidence E1b). Two independent defences,
both measured:

- `endInstance` → `cancelAllScheduledWork` drains `EventTriggeredSubprocesses`
  **2 → 0** (ADR-0164), so a terminal instance carries no arms; and
- on a hand-forged legacy shape (terminal + 2 live arms + live scope — a row
  persisted before ADR-0164), `SignalReceived` and `TimerFired` are refused at
  `dispatch` with `outcome=dropped` (ADR-0165). All three ESP delivery triggers
  carry `rejectSilently`.

**A design that states this defect as "fires on a terminal instance" would be
designing against a premise the code no longer has.**

### Correction 2 — the reachable defect is `StatusCompensating`

`beginCompensation` **deliberately does not drain ESP arms**
(`engine/state_arms.go:132-137`), because a walk may still *resume* the instance.

Measured (evidence E3), stated at the precision the evidence supports: for the
three `beginCompensation` writers whose post-state was observed **while the walk
was still in flight** — cancel (`W2`), admin full rollback (`W3`) and error
rollback (`W1`) — ESP arms go **2 → 2** while gateway and boundary arms go
**1 → 0**. The fourth (`W4`, the deferred-cancel finish re-entry) was only
observed **after** its terminal transition, where `endInstance`'s sweep has
already taken ESP to `0`; it is classified by its shared `beginCompensation` call,
not by an in-flight measurement of its own.

⚠ **The fifth writer is different and the distinction is load-bearing.**
`startCompensationWalk` (the compensation **throw**) does **not** call
`beginCompensation` at all, and drains **neither** ESP **nor** gateway arms
(measured `ArmedEvents 1 → 1`). A recap that says "all `StatusCompensating` paths
drain gateway and boundary arms" is false, and it is what hid the gateway-arm hole
that Decision 1's dispatch-site placement now closes.

So the arms survive for the whole duration of every rollback — **including the
rollbacks that will terminate.** Measured end-to-end (evidence E1c), during a
cancel rollback:

```
rollback in flight:          status=compensating tokens=0 ESP=2 cursorActive="e2d-c1"
NON-ROOT arm during rollback: status=compensating tokens=1 ESP=1
  cmds=[{e2d-c2 nested-esp-action ...}]        ← live action dispatched to a worker
after walk finish:            status=terminated tokens=1 ESP=0
WARN trigger rejected on terminal instance ... trigger=engine.ActionCompleted outcome=dropped command_id=e2d-c2
```

The rollback dispatched real work to a real worker, then terminated the instance.
The worker's `ActionCompleted` is dropped and the terminated instance is left
carrying **one orphan token in `TokenWaiting` awaiting a command that can never be
applied**.

### Correction 3 — effect (c) is STALE, and its figure was never measured

The inherited description carried an ADR-0168 "accepted cost" in which a root-site
tail retired a scope's ESP arms (`2 → 0`) while the instance stayed
`Compensating`. **That tail was already removed by ADR-0171**, in the same
delivery, and the removal is pinned by a fixture-audited, mutation-verified test
(`TestRootEventSubprocessExitKeepsRootArmsWhenTheWalkCanResume`; re-inserting the
removal takes arms **1 → 0** and the test goes RED). The inherited "2 → 0" figure
does not come from that fixture and is `ASSUMPTION (unverified)`. **This ADR does
not restate effect (c) as open.**

### Direction (b) reproduces as inherited

A **local** compensation throw leaves the instance legitimately running — cursor
`ResumeNode:after`, `FinalStatus:running` — yet `!= StatusRunning` silences the
root arm (evidence E2). The delivery is nonetheless **consumed**:
`vars=map[payload:1]` proves `markMatched` merged the payload before the fire
no-op'd, and signals are one-shot broadcasts, so nothing redelivers. Driven to
termination, the walk resumes, a *second* delivery fires normally and the instance
completes — so the measured cost is exactly **one silently swallowed delivery**.

### The predicate is completely uncovered

Measured (evidence E8): deleting `s.Status != StatusRunning` outright leaves
`./engine/...` **and every container-free package** at `EXIT=0`. Nothing existing
will catch a regression there, and nothing existing will confirm a change.
**This delivery owes new tests.**

## Decision

**Replace the root-only status check with one predicate applied to every arm:
a dying instance spawns no new work, whichever scope the arm belongs to.**

```go
// spawnsNewWork reports whether the instance may still start new work. It is an
// ALLOW-list: an unrecognised Status fails CLOSED.
func (s *InstanceState) spawnsNewWork() bool {
    switch s.Status {
    case StatusRunning:
        return true
    case StatusCompensating:
        return !s.Compensating.walkTerminates(s.PendingCancel)
    default:
        return false // terminal, or out of range
    }
}
```

applied at **three** kinds of site — and the count is itself a gate correction
(this ADR said "two" until `/code-review` measured the third gap):

- `fireEventTriggeredSubprocessArm`, retaining the non-root scope-liveness check
  below it;
- the **arm** dispatch guard shared by every family — `handleSignalReceived`'s
  tier loop and `dispatchArmCascade` — replacing ADR-0169's `IsTerminal()`; and
- the **standalone-token fall-throughs**: `handleSignalReceived`'s tier-4 loop,
  `handleTimerFired`'s intermediate-timer resume, and
  `handleMessageReceived`'s parked-message resume. Only the signal one existed
  before the gate.

### 1. `walkTerminates`, NOT `walkMode() == walkAdmin`

⚠ **This ADR first specified `walkMode() == walkAdmin || s.PendingCancel`. The
audit refuted it by execution, and the refutation is the defect this ADR exists to
close.**

`PendingCancel` does **not** imply the walk terminates. `consumePendingCancel` is
set on the throw plans (`engine/step_compensation.go:680`) and the reverse plan
(`:737`), but **not** on `walkPartial` (`:710-722`, which carries only
`resume: true, resumeAt: toNode`). Meanwhile `handleCancelRequested`'s deferral
predicate reads `ResumeNode != "" || ReverseNode != ""` while `walkMode()` gives
`ToNode` **precedence** over `ReverseNode` — so a cursor carrying both is
`walkPartial` *and* deferral-eligible. `CompensateRequested` is a public struct
with public fields, so the shape is reachable through the public API.

Measured: such a walk **resumes** with `PendingCancel` still true, and the
refuted predicate silences a non-root arm on it —

```
MAIN:     cmds=[{ap8-c4 nested-esp-action … payload:1}]  → FINAL status=running tokens=2
PREDICATE: cmds=[]  vars=map[payload:1]                  → FINAL status=running tokens=1
```

— losing a delivery `main` delivers, on an instance that keeps running. **That is
direction (b) reintroduced by the fix for direction (a).**

The predicate is therefore a method that **mirrors `stepCompensationFinish`'s own
plan construction**, so the two cannot drift:

| `walkMode()` | terminates? | why |
|---|---|---|
| `walkAdmin` | **yes** | `finishPlan.resume = false` |
| `walkReverse` | **yes** — *by decision, see 1a* | `resume: true`, but see below |
| `walkPartial` | **no** | `resume: true`, `consumePendingCancel` **not** set |
| `walkThrowTargeted` | `pendingCancel` | `resume: true` + `consumePendingCancel` |
| `walkThrowScopeWide` | `pendingCancel` | `resume: true` + `consumePendingCancel` |

⚠ `walkThrowTargeted` is a **fifth** mode the first draft of this table omitted
(`engine/state_compensation.go:176-215` declares five).

⚠ **`FinalStatus` cannot be used.** The admin full rollback *terminates* yet
carries `FinalStatus=running` (the zero value, mapped to `StatusTerminated` only
later in `applyTerminate`). A predicate keyed on it classifies that walk as
resuming — exactly backwards.

### 1a. A full REVERSE is excluded from the "fires" set, deliberately

`walkReverse` resumes, so on the rule above the arm would fire. It is excluded
anyway, because firing it walks into machinery that is not ready:
`NewReverseToStart` sets `ResetVars: true`, and `finishPlan.rearmRootESP`
(`engine/step_compensation.go:736`, applied `:899-910`) re-arms **every** root
event sub-process on the resume. Measured with the arm unsilenced:

```
>>> ROOT ESP arm during a full reverse: tokens=1 scopes=1 ESP=0
    cmds=[{ap10-c5 esp-action … map[… payload:1] false}]
>>> FINAL after the reverse resume: status=running tokens=2 scopes=1 ESP=1
  token ap10-t2 node=esp-svc scope="ap10-s1"    ← ESP body still running
  token ap10-t3 node=svcA    scope=""           ← the reverse's own resume
  vars=map[]                                     ← payload:1 WIPED under the body
  ESP arms=1                                     ← one-shot arm RESURRECTED
```

Three hazards in one run: two concurrent tokens after a "reset to start"; the ESP
body's variables erased beneath it; and an **interrupting one-shot arm re-armed
while its body still executes** — which `engine/step_nodes.go:390-397` already
cites, verbatim, as the reason an equivalent re-arm was rejected elsewhere
(*"would resurrect an INTERRUPTING one-shot arm that had already fired and been
legitimately retired"*).

**Accepted cost:** a signal aimed at a root event sub-process during a full
reverse is still swallowed — the same one-delivery loss direction (b) describes,
now **scoped and deliberate** rather than accidental. Widening it is
`rearmRootESP`'s problem, not this ADR's.

### 1b. It is an ALLOW-list, so an unknown Status fails closed

`Status.IsTerminal`'s own godoc records that *"an out-of-range Status value is
also treated as not terminal"*. A deny-list predicate therefore **starts firing
arms** on an out-of-range status. Measured against a real armed root arm with
`Status` forced to `engine.Status(9)`:

```
MAIN:       out-of-range status=unknown: ESP arms=1 scopes=0 cmds=[]                    ← silenced
DENY-LIST:  out-of-range status=unknown: ESP arms=0 scopes=1 cmds=[{ap4-c2 esp-action}] ← FIRES
```

The `switch` above is written as an allow-list for exactly this reason, and
`IsTerminal()` disappears from the predicate: terminal statuses fall to `default`.

### 2. Why not the two obvious alternatives

**Shape (A) — put the same `!= StatusRunning` check on the non-root branch — is
REFUTED by execution.** It breaks a real shipped behaviour:

```
--- FAIL: TestCompensationWalkSurvivesNestedEventSubprocessTearingDownItsResumeScope
    step_compensation_scope_drain_test.go:508: no human task for node "espTask"
```

That fixture fires a nested interrupting ESP arm **while a scope-wide throw walk is
in flight** — a legitimately-running instance. Shape (A) is a regression, not a
fix. Its variant `IsTerminal()` on both branches leaves the suite `EXIT=0` but
closes nothing reachable, since terminal is already unreachable (Correction 1).
⚠ Suite-green there is evidence about the **suite**, not the engine.

**Shape (B) — make terminal transitions drain the arms — is already true**
(evidence E1b, E3): `endInstance` → `cancelAllScheduledWork` already takes ESP arms
2 → 0. It does not address the reachable defect, which lives in
`StatusCompensating`.

**Shape (D) — drain ESP arms at `beginCompensation`, but only for TERMINATING
walks — was not considered by the first draft and is recorded here with its data.**
The rejection above ("`beginCompensation`'s non-drain is correct and stays")
defends the non-drain *for resuming walks* and then over-generalises. The narrow
version (`outcome.ToNode == "" && outcome.ReverseNode == ""`) excludes every
resuming walk by construction, and the throw walk never routes through
`beginCompensation` at all. Executed, shape (D) breaks exactly **one** existing
assertion, and that one is about *when* a `CancelTimer` is emitted
(`TestCancelRequestedTerminate_CancelsRootEventSubprocessTimer`). It closes
direction (a) for signal *and* timer and keeps `walkMode()` out of the fire path.

**Shape (C) is still chosen**, because shape (D) leaves the **deferred-cancel
window** open: a walk that becomes terminating *after* it started is exactly the
case `beginCompensation` cannot see, and it is the case F1 above is about. Shape
(D) is recorded so the choice is visible rather than excluded by an over-general
sentence.

`beginCompensation`'s non-drain therefore stays, and the fix stays at the fire and
dispatch sites.

### 3. This is measured, not argued

The predicate went **RED → GREEN** on a built reproduction, closing both
directions at once, while two controls — a throw-walk non-root fire, and the
nested-ESP teardown fixture — stayed green, along with `./engine/...` and ten
container-free packages. ⚠ That was measured for the *first* draft's predicate;
the `walkTerminates` correction in Decision 1 and the reverse exclusion in
Decision 1a are **not** yet covered by that run, and the plan's Phase 3 owes both
their own reproductions.

### 4. It applies to every trigger kind, not just signal

`fireEventTriggeredSubprocessArm` is reached from the timer and message paths via
`dispatchArmCascade` as well as from `handleSignalReceived`. The predicate governs
ESP delivery for all three trigger kinds: *a dying instance spawns no new work* is
not a signal-specific rule.

⚠ **It takes THREE guards, not one, and that was found at the delivery gate.**
`dispatchArmCascade` covers the ARM families for all three trigger kinds. But
`handleTimerFired` and `handleMessageReceived` each ALSO resume a standalone
parked token after the cascade returns `matched=false`, and those fall-throughs
had no check: `/code-review` measured a live `InvokeAction` dispatched to a worker
on both paths, on an instance whose in-flight rollback then terminated it. An
earlier revision of this bundle asserted in a source comment that the callers
"appl[y] the same rule for itself" — **they did not**. Each token fall-through now
carries its own guard, ahead of `mergeVars` so the delivery is not consumed, and a
fourth trigger kind would need one too.

⚠ **Why the cascade guard cannot simply be moved into the fire function.** The cascade runs `onMatch` — which merges the payload
and sets `matched` — **before** the fire. A fire that silently no-ops therefore
*consumes* the message and short-circuits the fall-through, which is precisely the
silent-swallow shape direction (b) describes, reintroduced on the message path.
Checking eligibility **ahead of** the ESP dispatch point avoids it.

### 5. The empty-cursor case: measured, and the previous rationale was FALSE

⚠ This ADR previously rejected the conservative variant
`&& s.Compensating.ActiveCmdID != ""` on the grounds that *"`ActiveCmdID` is
transiently empty between records within a live walk"*. **That sentence was an
unexecuted behavioural claim, and it is false.** Measured: a three-record admin
walk stamps `c4 → c5 → c6` with no gap, and a corpus-wide invariant
(`Compensating && ActiveCmdID == ""` → panic) leaves four container-free packages
at `EXIT=0`. **No `Step` in the reachable corpus produces that state** —
`stepCompensationAdvance`, `beginCompensation` and `startCompensationWalk` all
either stamp it or finish the walk.

That also **resolves the open assumption**: the empty-cursor state is not
engine-reachable. It survives only as a legacy-persisted row or a hand-built
`InstanceState`. For those, `walkTerminates` classifies an empty cursor as
`walkAdmin` → terminating → arms silenced, which is the conservative direction for
*not spawning work*. The conjunct is **not** adopted, but now for a measured
reason rather than a false one.

## Consequences

**Positive.**

- A terminating rollback no longer dispatches new actions to workers, and no
  longer leaves a terminated instance carrying an orphan `TokenWaiting` token
  whose `ActionCompleted` can never be applied.
- A *resuming* compensation walk (throw / reverse / partial) no longer silently
  swallows a signal delivery aimed at a root event sub-process.
- One predicate replaces a root/non-root asymmetry that no document could state
  correctly — ADR-0124's own attempt at it is the false sentence below.

**Negative / accepted costs.**

- **Behaviour change with no opt-out.** A definition that (unknowingly) relied on
  a root ESP arm being silenced during any compensation now sees it fire during
  **throw and partial** walks (not reverse — Decision 1a).
- `walkTerminates` and `PendingCancel` become inputs to the arm-fire path and the
  dispatch guard, coupling both to the compensation cursor. Real rather than
  incidental — *is this instance dying* has no cheaper answer — but new surface,
  and it must be kept in step with `stepCompensationFinish`'s plan construction.
- **The guard now governs every arm family, not just event sub-processes.**
  Replacing ADR-0169's `IsTerminal()` at the dispatch site means gateway and
  boundary arms are also refused on a dying instance. That closes the hole where a
  gateway arm fired into a terminating rollback while the ESP arm beside it was
  silenced — but it is a wider behaviour change than the ADR's title suggests, and
  it lands in the same delivery that multiplies gateway firing (ADR-0158).
- ⚠ **A second counterexample to "resume ⇒ does not terminate", not closed here.**
  `applyFinish` also terminates a *resume* plan when the resume is dropped and no
  tokens remain (`resumeDropped && len(s.Tokens) == 0 → endInstance(StatusCompleted)`).
  An arm firing during such a walk **places a token and thereby suppresses that
  recovery completion**, leaving the instance `Running` with only the event
  sub-process body. `walkTerminates` cannot see it: the outcome depends on token
  count at finish, not on the cursor. Recorded as a known limitation with its own
  follow-up, not silently absorbed.
- The predicate is **entirely uncovered today** (evidence E8), so its correctness
  rests wholly on tests this delivery adds. Every one of them must be
  mutation-verified; a passing suite proves nothing here, as shape (A′) showed.

### Corrections owed to other documents

1. **ADR-0124 Decision item 4** (`docs/adr/0124-repeatable-noninterrupting.md:62-63`)
   states a lingering arm in a terminal snapshot is harmless because
   `fireEventTriggeredSubprocessArm` "is status-guarded to no-op on a non-`Running`
   instance". **That parenthetical is false** — the guard was root-only. It is
   corrected in this bundle. (Its neighbouring sentence, that `isTerminal` excludes
   the transient `Compensating` state, is *true*, and the two together are what
   made the defect reachable at the runtime layer.)
2. **`engine/step_nodes.go:483-491` carries TWO false coverage claims, not one.**
   Besides "that too is asserted by the named test" (`:491`), the block also
   states the conjunct *"DOES discriminate: TestCompensationWalkBlocksNestedEvent­SubprocessCompletion
   reaches it, and without the conjunct that fixture completed the instance,
   published CompleteInstance and dropped the walk's InvokeAction as stale"*.
   Measured false: deleting `&& c.s.Compensating.ActiveCmdID == ""` at `:492`
   leaves `go test ./engine/...` at `EXIT=0` with the named test **running** and
   passing. Its own docstring
   (`engine/step_compensation_walk_completion_test.go:460-467`) already admits
   this. **Correcting only the last clause would leave an equally false sentence
   one line above.** Both are corrected in this bundle.
3. **`engine/step_nodes.go:501`** — `exitNestedEventSubprocessScope`'s arm
   retirement — is likewise uncovered (mutation: suite green). Left in place,
   recorded as a coverage gap rather than removed on a green suite.
