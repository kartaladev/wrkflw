# 168. An instance with a compensation walk in flight is not complete

- Status: Accepted
- Date: 2026-08-08

> Ships with [ADR-0169](0169-a-delivery-stops-at-a-mid-delivery-terminal.md) in one
> bundle: two defects of the same family with two different predicates.
> Design: `docs/specs/2026-08-08-compensation-walk-and-mid-delivery-terminal.md`.
> Plan: `docs/plans/2026-08-08-compensation-walk-and-mid-delivery-terminal.md`.

## Context

A process whose rollback is in flight can be reported `completed`, with the outstanding
compensation action's result silently discarded. Reproduced by execution on `main` @
`7180114`:

```
start → svcSaga(doA / undoA) → fork(parallel) ⇒
    branch A: taskA(user) [bndA interrupting, signal "s1"] → rb(CompensateThrow) → endA
    branch B: taskB(user) [bndB interrupting, signal "s2"] → endB
```

```
AFTER SIGNAL#1 (s1)  status=compensating tokens=1 boundaries=1 activeCmd="i1-c2"
                     cmds=[UpdateTask{i1-h1 taskA cancelled}, InvokeAction{i1-c2 undoA}]
AFTER SIGNAL#2 (s2)  status=completed    tokens=0 boundaries=0 activeCmd=""
                     cmds=[UpdateTask{i1-h2 taskB cancelled}, CompleteInstance{Result:map[]}]
WARN trigger rejected on terminal instance ... trigger=engine.ActionCompleted
     status=completed outcome=dropped command_id=i1-c2
```

`CompleteInstance{}` is published for a process whose compensation never ran to completion,
and `undoA`'s `ActionCompleted` is then refused by dispatch's terminal guard (ADR-0165). The
rollback silently never finishes.

**Root cause.** `startCompensationWalk` (`engine/step_nodes.go`) consumes the throwing
token before parking the walk on `Compensating.ActiveCmdID`. The three normal-completion
sites in `engine/step_nodes.go` — the `if len(c.s.Tokens) == 0` guards in `exitRootScope`,
`exitRootEventSubprocessScope` and `exitNestedEventSubprocessScope` — read only the token
count, so "no tokens left" reads true while a walk is outstanding.

`Status.IsTerminal()` is false here and does not stop it. That is deliberate and must stay
so: `StatusCompensating` is non-terminal precisely so the walk's own `ActionCompleted` can
be dispatched.

**`StatusCompensating` has two distinct meanings**, and no prior ADR states this:

| route | meaning | tokens | arms |
|---|---|---|---|
| `beginCompensation` (error / cancel / reverse) | whole-instance rollback | all cancelled | boundary + gateway drained; ESP **not** |
| `startCompensationWalk` (in-definition `CompensationThrowEvent`) | a local walk while the rest of the process continues | only the throw token consumed | none drained |

Measured, one arm seeded per family:

```
BEFORE                  status=running  ArmedEvents=1 Boundaries=2 ESP=1
AFTER beginCompensation status=running  ArmedEvents=0 Boundaries=0 ESP=1 tokens=0
```

So for boundary and gateway arms, a live arm on a `Compensating` instance is reachable
**only** through `startCompensationWalk` — where the sibling branch continuing is correct
BPMN. Branch B's signal in the reproduction above is *legitimate*; the defect is not that it
fired, but that the instance completed while a walk was outstanding.

## Decision

**An instance is complete when it has no tokens AND no compensation walk in flight.** The
three normal-completion guards gain the cursor conjunct:

```go
if len(c.s.Tokens) == 0 && c.s.Compensating.ActiveCmdID == "" {
```

Executed against the same reproduction:

```
AFTER SIGNAL#2 (s2)  status=compensating tokens=0 activeCmd="i1-c2"
                     cmds=[UpdateTask{i1-h2 taskB cancelled}]     ← delivery still honoured
AFTER undoA(i1-c2)   status=completed    cmds=[CompleteInstance{Result:map[]}]
```

The walk's own finish (`stepCompensationFinish`) resumes at the throw's resume node and
drives to completion — **provided the walk's `ActionCompleted`/`ActionFailed` arrives**. If it
never does, the instance is stuck; see the escape matrix under Consequences. That is a real,
accepted cost of this decision, not a hypothetical.

**1. Only *normal* completion defers. Explicit termination is unchanged.** `forceTerminate`,
cancel, and the **failFast** fail path (the no-compensation-records branch of
`handleUnhandledError`) still terminate immediately; `endInstance` clears the cursor
(`engine/state.go:356`). An operator or definition that says "stop now" outranks an in-flight
walk. This keeps `TestForceTerminationClearsCompensationCursor` correct as written.

⚠ Stated as a closed set rather than "the fail paths", which was false: the **other** fail
branch — compensate-then-fail, taken whenever the process has anything to compensate, i.e.
every process this ADR is written for — does not terminate immediately and does not clear the
cursor. It starts its own walk. See
[ADR-0170](0170-an-unhandled-error-does-not-restart-a-live-compensation-walk.md), which is in
this bundle precisely because that branch could overwrite the cursor this decision now gates
completion on.

**2. The predicate is `Compensating.ActiveCmdID`, NOT `Status != StatusRunning`.** The
premise-evidence file that surfaced this defect
(`docs/specs/2026-08-08-adr-0158-premise-evidence.md`, §Q4c) proposed
`if s.Status != StatusRunning { return nil, nil }` in `fireBoundaryArm`, recording that the
engine suite stays green with it. **That shape was executed here and is refuted.** Against
the same fixture it swallows signal `s2` entirely:

```
AFTER SIGNAL#2 (s2)  status=compensating tokens=1 boundaries=1 cmds=[]
AFTER undoA(i1-c2)   status=running      tokens=1 boundaries=1 cmds=[]
```

The rollback survives, but `taskB` is parked forever with `bndB` still armed and no delivery
left to resume it — signals are one-shot broadcasts. Suite-green established only that
**nothing covers this either way**, not that the shape was correct. Blocking the arm fire
would also be inverted with respect to its own purpose: it is a no-op on the whole-instance
rollback route (those arms are already drained) and blocks exactly the local-throw route
where firing is legitimate.

**3. All three sites are reachable, and all three conjuncts discriminate. Their fallthrough is
NOT inert.**

🛑 **AMENDED DURING IMPLEMENTATION (2026-08-09) — this decision previously said the opposite.**
It read: *"Reachability of the two event-sub-process sites is not claimed … patching that site
alone leaves `go test ./engine/` at EXIT=0, so the other two conjuncts are non-discriminating
today and must not be written up as mutations 'caught'."* **That is refuted by execution.**
Implementation discharged the plan's reachability duty by *building* the reproductions, and
both sites are reachable with a live cursor and both conjuncts discriminate.

The old measurement ("patch `exitRootScope` alone → suite `EXIT=0`") was itself correct. It
established only that **no test covered those sites** — not that they were unreachable. That is
precisely the "suite-green is not verification" error this same ADR's Decision 2 documents
against the premise-evidence file, recurring one decision later inside the ADR that documents
it. The reachability duty existed because the design could not settle the question by reading;
running it settled the question the other way.

**The reproduction shape** (documented nowhere in the bundle before implementation): a
**non-interrupting** event sub-process whose body is `svc(compensable) → fork ⇒ {CompensateThrow
→ end ; end}`, throw branch declared first. An *interrupting* start does not work — it tears
down the enclosing scope's other event-sub-process arms at trigger time. The nested variant
puts that event sub-process inside a regular sub-process with **no outgoing flow**, so
`resumeInParentScope` finds nothing, `grandparentScopeID` is `""`, and the completion check is
reached. Pinned by `TestCompensationWalkBlocksRootEventSubprocessCompletion` and
`TestCompensationWalkBlocksNestedEventSubprocessCompletion`.

Measured on **unpatched** `step_nodes.go`, both fixtures:

```
status=completed activeCmd="" cmds=[CompleteInstance]
WARN dropping command whose awaiter this step cancelled   ← undoB never reaches the runtime
```

and with the conjunct:

```
status=compensating activeCmd="ip-c3" cmds=[InvokeAction undoB]
```

⚠ **These two sites are strictly WORSE than `exitRootScope`.** There the compensation action
escaped to the runtime and its result was refused later; here `dropStaleTokenCommands` drops
the `InvokeAction` **inside the same step**, so the rollback never reaches the runtime at all.

**The fallthrough is not inert** — this half of the original text stands, and is now measured
rather than hypothesised. With the conjunct added, a live cursor falls *past* the completion
branch into each function's tail, which calls `removeEventTriggeredSubprocessArmsForScope` and
returns non-completing — **retiring that scope's event-sub-process arms** while the instance
stays `Compensating`. A throw walk's resume does not re-arm them (`finishPlan.rearmRootESP` is
set only on the full-reverse resume at root scope), so the effect is **silent permanent loss of
those event sub-processes**. Asserted explicitly, as this decision required: a second,
never-triggered standby event sub-process is armed in the scope and measured
`EventTriggeredSubprocesses` **2 → 0** while the instance stays `Compensating`.

That loss is a **measured, accepted cost** of this decision, not an incidental — deferring
completion is still strictly better than publishing `CompleteInstance` over an unfinished
rollback and dropping the compensation action. The owed event-sub-process ADR (see
Consequences / spec §7) should revisit it.

🛑 **AMENDED (2026-08-10) — the accepted cost above is RETIRED by
[ADR-0171](0171-a-compensation-walk-owns-its-record-source-and-resume-scope.md), in this same
bundle.** The arm loss was not a consequence of deferring completion; it was a symptom of the
scope being archived and closed *underneath the live walk*. Both fixtures above were stopping
exactly one `Step` short of the real failure — driven one step further on the pre-0171 tree
they wedge permanently
(`err=workflow-engine: defForScope: unknown scope "i-root-esp-s1"` / `"…-s2"`, on every
redelivery). ADR-0171 holds that scope exit while a walk names the scope as its resume target,
so measured now: `EventTriggeredSubprocesses` stays **2**, the scope stays open, and once the
walk drains the deferred exit runs and the instance **completes**. The two tests assert the new
behaviour; their `require.Empty` on the arms is gone.

⚠ **Consequence for THIS decision's own coverage.** Because ADR-0171's hold returns before
`exitEventSubprocessScope` is entered, those two fixtures no longer reach sites 2 and 3 with a
live cursor. Re-measured after ADR-0171: reverting conjunct 1 (`exitRootScope`) still turns
`TestCompensationWalkInFlightBlocksCompletion` and `TestCompensationWalkFinishCompletesInstance`
red; conjunct 2 (`exitRootEventSubprocessScope`) is re-covered by ADR-0171's
`TestRootEventSubprocessExitBlocksCompletionUnderRootWalk`, which drives a **root-scope** walk
the hold never matches while an unrelated root-level event sub-process exits; **conjunct 3
(`exitNestedEventSubprocessScope`) can now be reverted with the engine suite at `EXIT=0`** and
is on the backlog. It is kept, not deleted — undemonstrated is not unreachable, which is the
exact error this decision already had to amend once.

🛑 **AMENDED AGAIN AT THE GATE (2026-08-10) — the amendment above closed the arm loss for the
two fixtures it names, and NOT in general. `exitRootEventSubprocessScope`'s tail no longer
retires anything.** ADR-0171's hold keys on the walk's `ResumeScope`, so it cannot match a walk
rooted at the **root** scope while an unrelated root-level event sub-process exits — the exact
shape `TestRootEventSubprocessExitBlocksCompletionUnderRootWalk` drives. That fixture reaches
the tail, and it completed the instance one `Step` later, so the retirement was invisible in it.
Measured on a variant with a **user task** between the throw and its end event, so the walk's
resume leaves the instance running
(`TestRootEventSubprocessExitKeepsRootArmsWhenTheWalkCanResume`):

```
before the exit         arms=1
after the exit          arms=0   status=compensating
after the walk resume   arms=0   status=running
re-deliver "boom"       scopes=0  ← the event sub-process is no longer triggerable, forever
```

The arm was **non-interrupting**, which ADR-0124 makes repeatable, so this is a silent
capability loss on a live instance. The `removeEventTriggeredSubprocessArmsForScope("")` call
is therefore **removed from that tail**. It leaks nothing: the arms it retired belong to the
ROOT scope, which is implicit and never closed, and every terminal end of the walk routes
through `endInstance`, whose `cancelAllScheduledWork` sweeps them.

Re-arming on the throw resume (setting `finishPlan.rearmRootESP` there, the cheaper-looking
option) was **rejected**: `armEventTriggeredSubprocesses` re-arms every root event sub-process
in the definition, which would resurrect an INTERRUPTING one-shot arm that had already fired
and been legitimately retired.

⚠ **This ADR previously cited `engine/step_nodes.go:330`'s "DEFENSIVE, and unreachable today"
comment as its precedent for keeping such a guard. Adding the conjunct changes the entry
condition of that very tail, so the comment's stated reason — "nothing can be left for
`len(c.s.Tokens) != 0` to find" — becomes wrong.**

🛑 **AMENDED (2026-08-09):** this went further than predicted. The bundle expected the comment's
*reason* to need rewriting while the tail stayed unreached. Measured, **the tail is LIVE** —
`TestCompensationWalkBlocksRootEventSubprocessCompletion` reaches it — so **both halves** of
that comment were false, not just the reason: it is neither "defensive" nor "unreachable
today". No defensive-guard precedent is being invoked at all any more; all three conjuncts are
load-bearing on covered paths. The comment has been restated from the measurement rather than
patched.

Two further pre-existing false comments were found at the same site and corrected in this
bundle: `exitRootEventSubprocessScope`'s doc comment and its in-branch comment both attributed
the completion branch to the **interrupting** case. A non-interrupting event sub-process reaches
it whenever it outlives the root scope's tokens, so the branch is decided by **drain state**,
not by interrupting-ness — the framing `exitNestedEventSubprocessScope` already used correctly.

## Consequences

**Positive.**

- **On the normal-completion route**, a `CompleteInstance` event can no longer describe a
  process whose rollback did not finish. The audit view, `incident_count` and every downstream
  consumer of the terminal event stop being lied to for that route.
  ⚠ The qualifier is load-bearing and was missing from the first draft of this ADR. An
  explicit `forceTerminate` with `OutcomeComplete` is a **fourth**
  `endInstance(StatusCompleted, …, CompleteInstance{…})` site (in `exitSubProcessScope`,
  `engine/step_nodes.go`),
  deliberately exempted by Decision 1, and it still emits `CompleteInstance` while a walk is
  outstanding. Measured byte-identical with and without this ADR's patch:
  `AFTER SIGNAL s2 -> forceTerminate(OutcomeComplete) status=completed activeCmd="" cmds=[CompleteInstance{Result:map[]}]`,
  with `undoA` still outstanding. That is Decision 1's accepted cost, not an oversight — but
  the unqualified claim was false.
- A compensation action's `ActionCompleted` is no longer discarded by the terminal guard on
  this route, so the walk runs to its designed end.
- Legitimate parallel-branch progress during an in-definition compensation throw is
  preserved. The fix costs no delivery.
- The two meanings of `StatusCompensating` are now written down, which is what made the
  originally-proposed fix look plausible.

**Negative / accepted.**

- 🚨 **A walk whose result never arrives now hangs the instance permanently, and no operator
  trigger can release it.** This is the sharpest cost of the decision. Once completion is
  deferred the instance holds **zero tokens**, so only `ActionCompleted`/`ActionFailed` **for
  the cursor's own command** can move it. Measured against the deferred state:

  | trigger | result |
  |---|---|
  | `CancelRequested` | `err=<nil>`, sets `PendingCancel=true`, emits **zero commands**, still `compensating` |
  | `CancelRequested` (again) | unchanged |
  | `CompensateRequested` | documented silent no-op on the same predicate |
  | stray `SignalReceived` | unchanged |
  | `ActionCompleted`(cursor cmd) | releases it → `completed` |

  There is no engine-side liveness backstop: `compensationInvoke` emits a bare `InvokeAction`
  with no timer or deadline, and `handleActionFailed` short-circuits compensation commands to
  `stepCompensationAdvance` **before** the retry-policy block, so there is no retry either.
  **Pre-0168 the sibling's completion was an escape — a wrong one, but terminal. This ADR
  removes that escape and replaces it with a state no trigger can leave.** The trade (a
  visibly-stuck instance instead of a silently-wrong "completed" one) is deliberate and
  owner-adjudicated; an operator escape hatch is owed as its own ADR and is on the backlog.
- **An existing test's fixture is invalidated, and it is worth dwelling on.**
  `TestActionCompletedOnTerminalInstanceIsNoOp` (ADR-0165, `engine/step_terminal_test.go`)
  **manufactures its scenario out of this bug** — a sibling branch completing the instance
  out from under a live walk is how it strands a command — and its comment records that it
  deliberately chose that route over `forceTerminate` to obtain an "otherwise-normal
  completion". Post-0168 no such completion exists; that is the property being established.
  **ADR-0165 saw this exact state and read it as normal.**
  ⚠ The re-fixtured test does **not** preserve "a terminal instance with the walk's command
  outstanding" — measured, force-termination *clears* the cursor (`activeCmd=""`), which is
  what `TestForceTerminationClearsCompensationCursor` pins. Post-0168 there is **no** reachable
  state combining a terminal instance with a live compensation cursor. What the re-fixtured
  test covers is a stray `ActionCompleted` whose command is owned by **nothing** — no token,
  no cursor — tolerated as a no-op rather than `ErrTokenNotFound`. It must be re-fixtured onto
  an explicit termination using **`OutcomeAbort`**: `OutcomeComplete` is the zero value and
  would bake the F4 counterexample above into the permanent suite as the sanctioned fixture.
- 🚨 **Deferring completion turns a silent wrong answer into a panic, on one route.** When the
  branch that drains the last token is a sibling inside the *throwing scope*, that drain also
  archives and closes the scope the walk is reading. Pre-0168 the instance went `completed` and
  the walk's command was dropped; with this decision's conjuncts in place the walk survives to
  its next `ActionCompleted` and indexes a nil slice —
  `panic: runtime error: index out of range [0] with length 0`, **inside the pure engine core,
  i.e. in the consumer's process.** That is why
  [ADR-0171](0171-a-compensation-walk-owns-its-record-source-and-resume-scope.md) ships in this
  same bundle rather than after it: 0168 must not be delivered alone.
- An instance can now sit at `StatusCompensating` with zero tokens. Any consumer inferring
  "no tokens ⇒ finished" from state alone is wrong, and was already wrong for the window
  between the walk starting and finishing; this widens that window.
  **One such consumer exists in this repo's public API:** `processtest.Classify`
  (`processtest/park.go`) has no reason for "parked on a compensation walk" — `ReasonAsyncChild`
  requires a **token** carrying `AwaitCommand`, and the walk is awaited via
  `Compensating.ActiveCmdID`, which no token carries. Measured:
  `Classify(zero-token Compensating) → reason="unknown"`, which `processtest`'s `drive` turns
  into `ErrUnhandledPark`. The state was already reachable on `main` via the whole-instance
  rollback route, so this ADR does not create the hole — it makes it reachable on the ordinary
  in-definition-throw route. Same class as backlog item 6b (`Park.HasArmedTimers`).
- The guard reads instance state, not the trigger, so it cannot be expressed through
  ADR-0165's `terminalPolicy()` seam — the same limitation `stepCompensateRequested`'s
  in-handler guard has, recorded there as the third gap owed by delivery 2b.

**Corrections owed elsewhere.**

- `docs/specs/2026-08-08-adr-0158-premise-evidence.md` §Q4c and its "Two NEW pre-existing
  bugs" item 1 carry the refuted `!= StatusRunning` shape. That file is a preserved input to
  the future ADR-0158 rewrite; a correction note pointing here ships in this bundle so the
  shape does not propagate.

**Deliberately not addressed.**

- The **ESP arm hole — in BOTH directions.** Its own ADR must cover both, or it will fix one
  and ship the other:
  1. *Arms firing when they should not.* `fireEventTriggeredSubprocessArm` checks status for
     the root scope only, and `beginCompensation` does not drain ESP arms, so a **non-root**
     ESP arm fires into a completed instance and emits a live `InvokeAction`.
  2. *Arms NOT firing when they should.* The root-scope check is literally
     `if s.Status != StatusRunning` (`engine/step_eventsubprocess.go:167`) — **a shipped
     instance of the very predicate this bundle refutes** (see the spec's §6). Measured
     against a legitimate local compensation throw with a live sibling branch, a one-shot
     broadcast signal is swallowed and the root ESP arm never fires:
     `AFTER SIGNAL boom during a LOCAL compensation throw … esp=1 commands=0`. Nothing
     redelivers it. **ADR-0168 lengthens the window in which that predicate is true**, so this
     direction gets marginally worse here.

  It also falsifies **ADR-0124 Decision item 4's** "harmless lingering arm" sentence.
- **An operator escape from a stalled compensation walk** — the hang recorded under Negative.
  Most likely shape: let `CancelRequested` terminate immediately when the walk has **no live
  tokens** left to wait for. That is a behaviour change on an ADR-0039/0109 path and needs its
  own evidence, so it is deliberately not slipped in here.
- **Incident-history retention** and **zombie scopes** — still owed by delivery 2b.
