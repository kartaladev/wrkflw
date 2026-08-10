# Spec — normal execution must stop when the instance stops being normally executable

- Date: 2026-08-08
- Status: **IMPLEMENTED**. Rule-#9 audit complete pre-implementation (3 Opus auditors, isolated
  worktrees, ~35 findings); accepted fixes folded in. **Implementation then refuted two of this
  spec's own claims** — see §5.1 and §4.5, both amended in place with the measurement.
- Base: all runs against `main` @ `7180114` (re-derive with `git rev-parse --short main`)
- ADRs produced: **0168** (compensation walk blocks completion), **0169** (mid-delivery
  terminal re-check), **0170** (an unhandled error does not restart a live walk — **added by
  the audit**, §5.4)
- Predecessor evidence: `docs/specs/2026-08-08-adr-0158-premise-evidence.md` — this spec
  **corrects one of its conclusions**; see §6.

## 1. Context

Two defects were found on `main` while re-deriving ADR-0158's premises. Neither is caused
by any in-flight delivery. Both were re-derived **by execution** for this spec against
`7180114`, not inherited from the evidence file.

They share a shape — normal token execution proceeding against an instance that is no
longer normally executable — but they have **different predicates**, and conflating them
produces a fix that is wrong for one of the two (§6). They therefore ship as two ADRs in
one bundle.

Neither defect is covered by any existing test in either direction: with each candidate fix
applied, `go test ./engine/` still returned EXIT=0 except for the single fixture collision
recorded in §3.4.

## 2. Terminology: `StatusCompensating` has two meanings

This distinction is load-bearing for every decision below and is not stated anywhere in the
existing ADRs.

| route | meaning | tokens | arms |
|---|---|---|---|
| `beginCompensation` (error / cancel / reverse) | **whole-instance rollback** | all cancelled | boundary + gateway drained; ESP **not** |
| `startCompensationWalk` (in-definition `CompensationThrowEvent`) | **a local walk while the rest of the process continues** | only the throw token consumed | none drained |

EXECUTED (`beginCompensation`, one arm seeded per family, **plus at least one
`RootCompensations` record** — see the preconditions below):

```
BEFORE                  status=running       ArmedEvents=1 Boundaries=2 ESP=1
AFTER beginCompensation status=running       ArmedEvents=0 Boundaries=0 ESP=1 tokens=0
AFTER re-resolve: boundaryArmBySignal(s2)=false armedEventBySignal(sx)=false espArmBySignal(sy)=true
```

⚠ **Two preconditions, without which a rebuild of this probe gives the OPPOSITE ESP answer.**
The audit rebuilt it naively and measured
`status=terminated ArmedEvents=0 Boundaries=0 ESP=0 … espArmBySignal(sy)=false`:

1. **There must be a compensation record to walk.** With none, `beginCompensation` runs
   straight through to the terminal finish and `endInstance` drains ESP too. The ESP row above
   holds only for a *walking* rollback — which is the case that matters, but it is a
   precondition, not a general property.
2. **`status=running` in the output is a white-box artefact** of calling `beginCompensation`
   directly. Every real caller sets `StatusCompensating` first (verified at all four call
   sites).

The boundary/gateway half was additionally confirmed end-to-end through the public path:
`CancelRequested` on a live two-boundary-arm instance →
`status=compensating boundaries=0 armedEvents=0 activeCmd="ic-c2"`, `boundaryArmBySignal("s2")=false`.

⚠ Minor imprecision retained knowingly: the table labels the `beginCompensation` column
"whole-instance rollback", but `stepCompensateRequested` reaches it with `ToNode`/`ReverseNode`
set, producing a *resuming* partial/reverse walk. The arm-drain behaviour is the same; the
label is loose.

**Consequence.** For boundary and gateway arms, a *live arm on a `Compensating` instance* is
reachable **only** through `startCompensationWalk` — the case where continuing is correct
BPMN. The whole-instance rollback has already drained those two families.

⚠ The ESP row is a **third, separate defect** (a non-root ESP arm survives a whole-instance
rollback, and `fireEventTriggeredSubprocessArm`'s status check covers the root scope only).
It is **out of scope** here — see §7.

## 3. Defect 1 — a rolled-back process is reported `completed`

### 3.1 Fixture

```
start → svcSaga(doA/undoA) → fork ⇒
  A: taskA → endTaskA ;  bndA(signal s1, interrupting) → rb(CompensateThrow) → endA
  B: taskB → endTaskB ;  bndB(signal s2, interrupting) → endB
```

⚠ Written as an arrow chain (`taskA [bndA] → rb`) this is ambiguous and **does not build**:
it is the *boundary's* outgoing flow that reaches `rb`, not `taskA`'s, and `taskA`/`taskB` each
still need their own normal outgoing flow. `rb` **must** carry an outgoing flow or
`compensationThrowEventStrategy.enter` auto-advances and **no walk starts at all** — the same
trap already documented at `engine/step_stale_commands_e2e_test.go:236`. The explicit form
above is the one that was executed.

### 3.2 EXECUTED on `7180114` — verbatim

```
AFTER START                    status=running      tokens=1 boundaries=0 activeCmd=""
AFTER doA (both branches parked) status=running    tokens=2 boundaries=2 activeCmd=""
AFTER SIGNAL#1 (s1)            status=compensating tokens=1 boundaries=1 activeCmd="i1-c2"
                               cmds=[UpdateTask{i1-h1 taskA cancelled}, InvokeAction{i1-c2 undoA}]
AFTER SIGNAL#2 (s2)            status=completed    tokens=0 boundaries=0 activeCmd=""
                               cmds=[UpdateTask{i1-h2 taskB cancelled}, CompleteInstance{Result:map[]}]
WARN trigger rejected on terminal instance instance_id=i1 trigger=engine.ActionCompleted
     status=completed outcome=dropped command_id=i1-c2
AFTER undoA(i1-c2)             err=<nil>  status=completed  cmds=[]
```

**The rollback silently never finishes.** The cursor is cleared, `CompleteInstance{}` is
published for a process whose compensation is still outstanding, and `undoA`'s result is
then refused by dispatch's terminal guard.

### 3.3 Root cause

`startCompensationWalk` (`engine/step_nodes.go`) **consumes the throwing token**
(`c.s.consumeToken(tok, c.at)`) before parking the walk on `Compensating.ActiveCmdID`. The
three normal-completion sites read only the token count:

- `exitRootScope` — the guard and its `endInstance` call
- `exitRootEventSubprocessScope` — same shape
- `exitNestedEventSubprocessScope` — same shape, in the grandparent-is-root branch

(Cited by symbol on purpose. This list originally carried line numbers — `:215`/`:326`/`:424` —
and **all three had rotted by the time this bundle shipped**, moving to 225/345/468 under its
own edits. The guards are the three `if len(c.s.Tokens) == 0` occurrences in
`engine/step_nodes.go`; `grep -n 'len(c.s.Tokens) == 0'` is the authority.)

So `len(s.Tokens) == 0` reads true while a walk is in flight. `IsTerminal()` is false here
and does not stop it — `StatusCompensating` is deliberately not terminal so that the walk's
own `ActionCompleted` can be dispatched.

### 3.4 EXECUTED — candidate fix at the completion site

Applied to `exitRootScope` only:

```go
if len(c.s.Tokens) == 0 && c.s.Compensating.ActiveCmdID == "" {
```

Same probe:

```
AFTER SIGNAL#2 (s2)  status=compensating tokens=0 boundaries=0 activeCmd="i1-c2"
                     cmds=[UpdateTask{i1-h2 taskB cancelled}]        ← delivery honoured
AFTER undoA(i1-c2)   status=completed    cmds=[CompleteInstance{Result:map[]}]
```

Correct on every axis: branch B's signal is honoured, the rollback finishes, **then** the
instance completes.

The audit independently re-ran this with the conjunct applied to **all three** sites (which is
what §5.1 ships, and what §3.4 originally under-reported by describing only the one-site
experiment): `go test ./engine/` → `EXIT=1`, `grep -c '^--- FAIL'` → **1**, the same test at
`:528`. It also held across the whole container-free corpus — `processtest`, `service`,
`runtime/{calllink,signal,task}`, `transport/http/*` all `EXIT=0`.

`go test ./engine/` → **EXIT=1, exactly one failure**:

```
--- FAIL: TestActionCompletedOnTerminalInstanceIsNoOp
    step_terminal_test.go:528: control: the sibling branch must have completed the instance
```

That test (ADR-0165, `engine/step_terminal_test.go`) **manufactures its scenario out of
this bug**: a sibling branch completing the instance out from under a live walk is how it
strands a command. Its own comment (`:440`) records that it deliberately avoided
`forceTerminate` because that "drops tokens for an unrelated reason" and would not exercise
"no token awaits this command against an **otherwise-normal completion**". Post-0168 there
is no such thing as an otherwise-normal completion that strands a walk — that is precisely
the property being established. The test needs re-fixturing; see §5.3.

## 4. Defect 2 — mid-delivery resurrection (an eighth route)

### 4.1 Fixture

```
start → fork(parallel) ⇒
    branch A: taskA(user) [bndA interrupting, signal "x"] → errEnd(EndError "boom", uncaught)
    branch B: catchB(intermediate catch, signal "x") → taskB(user) [bndT timer boundary 60m] → endB
```

### 4.2 EXECUTED on `7180114` — ONE `SignalReceived{"x"}` delivery

```
AFTER START    status=running tokens=2 boundaries=1
AFTER SIGNAL x status=failed  tokens=1 boundaries=1 timers=0
  token id=i1-t3 node=taskB state=1                        ← zombie token on a FAILED instance
  cmd UpdateTask{i1-h1 taskA cancelled}
  cmd FailInstance{Err:boom}                               ← instance dies here
  cmd UpdateTask{i1-h2 taskB cancelled}                    ← record minted AFTER the terminal event
  cmd ScheduleTimer{i1-tm1 Token:i1-t3 …}                  ← live timer for a dead instance
```

### 4.3 Mechanism (source-verified, not inferred)

1. Tier 2 fires `bndA` → `drive` → uncaught `boom` → `handleUnhandledError`'s failFast branch
   → `endInstance(StatusFailed)`. That branch **deliberately does not drop tokens**
   (`handleUnhandledError`'s failFast branch, `engine/step_errors.go`, ADR-0164
   Decision 3), so branch B's token survives.
2. Tier 4 then loops `snapshotIDs` — taken by `tokenIDsAwaitingSignal`, **before** tiers
   1–3 — resumes `catchB`'s token and drives it into `taskB` on the dead instance, minting
   task `h2` and arming `bndT`.
3. `dropStaleTokenCommands` runs last. `liveAwaiters` returns an **empty** map for a terminal
   instance (`liveAwaiters`, `engine/step_stale_commands.go`), so `AwaitHuman{h2}` is
   stale → dropped, and
   because the record is still open it is cancelled → the post-`FailInstance` `UpdateTask`.
4. **`ScheduleTimer` is deliberately exempt from that filter** (`filterableCommand`,
   `step_stale_commands.go`, ADR-0161), so it escapes to the runtime.

ADR-0161's filter is therefore **containing** the damage post-hoc, not preventing it: the
token still moved, a node visit was still recorded, a task record was minted and cancelled,
`s.Boundaries` still carries an arm on a failed instance, and the `ScheduleTimer` still
escapes.

### 4.4 Why this is not one of the routes ADR-0164 / ADR-0165 closed

ADR-0164 closed seven resurrection routes. ADR-0165 then replaced eight hand-copied
`IsTerminal()` guard sites with one `terminalPolicy()`-driven guard at `dispatch` entry,
closing six further routes. **Every route closed by that entry guard is entry-level** — the
trigger arrives at an *already*-terminal instance.

⚠ Two precisions; the obvious shorthand ("all seven are entry-level, so this is the eighth")
is wrong on both halves:

- **Not all of ADR-0164's seven are entry-level.** Its first route is a **stale compensation
  cursor** — state, not a trigger arrival — closed by `endInstance`'s cursor clear
  (`engine/state.go:356`), which has nothing to do with `terminalPolicy()`.
- **No single ordered list of routes exists.** ADR-0164 counts seven; ADR-0165's Consequences
  says "six routes close"; ADR-0165's amendment note inside ADR-0164 counts "six … plus a
  seventh (`CancelRequested`)" — a *different* set. So this bundle claims **a further route**,
  never "the eighth".

This one is **mid-delivery**: the instance is `running` when `dispatch` admits the trigger and
goes terminal between tiers, where no entry guard can see it. ADR-0164's own Consequences
state it is "a list of routes closed, not a proof that none remain."

### 4.5 Exposure is confined to `handleSignalReceived` (source-verified)

Signal delivery is the only broadcast handler: four sequential dispatch points in one
delivery. Timer and message go through `dispatchArmCascade`
(`engine/step_arm_dispatch.go:28`), which is **first-match-wins**, and both callers `return`
immediately on `matched` (`handleTimerFired` and `handleMessageReceived` in
`engine/step_triggers.go`) — exactly one dispatch point
per delivery, after which nothing else runs.

⚠ The tier-4 loop is itself multi-iteration, so a terminal transition can also occur *within*
it (token A drives into an uncaught error; token B then resumes). The guard belongs inside
the loop, not only ahead of it.

🛑 **ADDED 2026-08-09, measured during implementation: tier 4 is not merely the *worst* exposure
— it is the ONLY one.** Deleting the tiers-1–3 guard leaves the whole `engine` package at
`EXIT=0` even with this bundle's five new tests present; deleting the tier-4 in-loop guard sends
`TestSignalDeliveryStopsInsideTheTokenLoop` RED. `endInstance` → `cancelAllScheduledWork` drains
every arm family on the way to terminal, so tiers 2 and 3 have nothing left to find. Tier 4 is
exposed precisely because it iterates a **snapshot taken before tiers 1–3** and owns no arm
state for that drain to clear. The tiers-1–3 guard ships as deliberate defence in depth; ADR-0169
Decision 2's structural argument for putting it in one place rather than three is unaffected.

## 5. Decisions

### 5.1 ADR-0168 — an instance with a compensation walk in flight is not complete

**Predicate:** `len(s.Tokens) == 0 && s.Compensating.ActiveCmdID == ""` at the three normal-
completion sites (§3.3).

**Deliberately unchanged:** `forceTerminate`, cancel and the fail paths still terminate
immediately and clear the cursor (ADR-0164). An **explicit termination outranks an in-flight
walk**; only *normal* completion defers. This keeps `TestForceTerminationClearsCompensationCursor`
correct as written.

**Reachability of the three sites — RESOLVED BY IMPLEMENTATION.**

🛑 **CORRECTED 2026-08-09.** This paragraph read: *"The two ESP root-exit sites' reachability
with a live cursor is **not demonstrated**, and measured: patching `exitRootScope` alone leaves
the full suite at `EXIT=0`, so the other two conjuncts are **provably non-discriminating
today**."* Implementation discharged the duty this paragraph imposed by **building** the
reproductions, and **both sites are reachable and both conjuncts discriminate**.

The `EXIT=0` measurement was correct and its inference was not: it established that **nothing
covered those sites**, which this spec's own §6 spends a section warning is not the same as a
behavioural claim. "Provably non-discriminating" was the over-reaching recap; "no existing test
discriminates" was what had actually been measured.

Verified by the controller independently of the implementing agent: reverting **only** the two
ESP conjuncts while leaving `exitRootScope` patched sends both new tests RED
(`an event-sub-process exit must not complete an instance whose walk is in flight`), restored
byte-clean afterwards.

Reproduction shape: a **non-interrupting** ESP whose body is `svc(compensable) → fork ⇒
{CompensateThrow → end ; end}`, throw branch first; the nested variant nests that ESP in a
regular sub-process with no outgoing flow. Interrupting starts do not work — they tear down the
enclosing scope's other ESP arms at trigger time. Measured unpatched, both fixtures: the
instance goes `completed`, `CompleteInstance` is published, and the walk's `undoB` is **dropped
inside the same step** as a stale command — strictly worse than §3.2's `exitRootScope` defect,
where the action at least escaped to the runtime.

⚠ **Their fallthrough is not inert** — with the conjunct added, a live cursor falls past the
completion branch into each function's tail, which calls
`removeEventTriggeredSubprocessArmsForScope` and returns non-completing, **retiring that scope's
ESP arms** while the instance stays `Compensating`. Measured `EventTriggeredSubprocesses`
**2 → 0**.

🛑 **CORRECTED AGAIN 2026-08-10 — that "measured, accepted cost" was NOT a cost. It was a
second defect wearing this one's clothes, and it is fixed by ADR-0171 in this same bundle.**
This paragraph previously closed: *"A measured, accepted cost; the owed ESP ADR (§7) should
revisit it."* The arm loss was never a consequence of deferring completion — it was a symptom of
the **scope being archived and closed underneath the live walk**. The two fixtures that
"measured the cost" were stopping exactly **one `Step` short of the real failure**: driven one
step further on the pre-0171 tree they wedge permanently, every redelivery returning
`workflow-engine: defForScope: unknown scope "i-root-esp-s1"`. With ADR-0171's held exit the
arms stay **2**, the scope stays open, and the instance completes once the walk drains.

⚠ **The lesson, which is the same one three times in this delivery.** An "accepted cost" is a
claim about behaviour and needs the same execution discipline as any other — *and it needs to be
driven far enough to see the end state*. A fixture that stops at the first surprising
observation will happily certify that surprise as the design's price. **Drive the fixture to
termination before calling anything a cost.**

⚠ **The `:330` precedent must be updated, not just cited.** `engine/step_nodes.go:330` carries
a "DEFENSIVE, and unreachable today" comment whose stated reason is that *"nothing can be left
for `len(c.s.Tokens) != 0` to find"*. Adding the conjunct changes the entry condition of that
tail, so the reason becomes wrong even if the tail stays unreached. This bundle cites it as
precedent **and** invalidates it; Task 1 must rewrite the reason.

### 5.2 ADR-0169 — a delivery stops dispatching when its own drive turns the instance terminal

**Predicate: `IsTerminal()`.** Not `!= StatusRunning` — see §6.

**Form (decided):** fold tiers 1–3 into a slice of lookup+fire closures checked once per
iteration, plus one check inside the tier-4 loop. **Two guard sites, not four.** A fourth arm
family added later inherits the guard rather than needing a fifth copy — ADR-0165's argument
at smaller scale.

**Abort return (decided):** `StepResult{State: *s, Commands: signalCmds}` — the partial
commands the earlier tiers legitimately produced, *including* the `FailInstance` that made
the instance terminal. Dropping them would discard the terminal event itself.

**Adjudicated, not silent:** no Warn is emitted on abort. This is an owner decision. The cost
is recorded explicitly in the ADR's Consequences: a stopped dispatch leaves **no operator-
visible trace**, so "why did my catch never fire?" has no log answer — the same gap ADR-0161
cites as its reason for logging every dropped command, and ADR-0165 for logging every
rejected trigger. Revisit if it surfaces in support.

### 5.3 Re-fixturing `TestActionCompletedOnTerminalInstanceIsNoOp`

Its assertions require, at step 3, a **terminal instance with `Tokens` empty**. Change: the
sibling branch's `end2` becomes a force-termination end event, leaving the two-Step-call
structure its comment requires intact (walk starts live and unfiltered in step 2; termination
in step 3).

⚠ **Use `event.WithForceTermination("<reason>", event.OutcomeAbort)` — two arguments, and the
outcome named explicitly.** `OutcomeComplete` is the **zero value**, so the obvious writing
`WithForceTermination(...)` without an outcome bakes the §5.1/F4 counterexample — a
`CompleteInstance` published while a walk is outstanding — into the permanent suite as the
sanctioned fixture. `OutcomeAbort` also keeps this fixture visibly distinct from
`TestForceTerminationClearsCompensationCursor`'s.

⚠ **The spec previously claimed this preserves "the walk's command outstanding" at step 3. It
does not, and cannot.** Measured: force-termination *clears* the cursor (`activeCmd=""`) — the
very behaviour `TestForceTerminationClearsCompensationCursor` pins (ADR-0164). Post-0168 there
is **no reachable state** combining a terminal instance with a live compensation cursor. What
the re-fixtured test actually covers: a stray `ActionCompleted` whose command is owned by
**nothing** — no token, no cursor — tolerated as a no-op rather than `ErrTokenNotFound`. State
that; do not restate the old requirement.

⚠ Implementation must **verify by execution** that step 2's two positive controls still hold
(`Status.IsTerminal()` false, `Compensating.ActiveCmdID` non-empty) — they are what make the
rest of the test non-vacuous. Measured under the re-fixture: `isTerminal=false`,
`ActiveCmdID="i-probe-t7-c2"`, and step 4 still reaches ADR-0165's dispatch guard (WARN
observed), so the test does not go vacuous. It is also **fix-independent** — it passes on
unpatched `main` — which is why its task lands first (§8).

### 5.4 ADR-0170 — an unhandled error does not restart an in-flight compensation walk

Added **during this bundle's audit**, which found it while attacking ADR-0168's dependency on
the cursor's lifetime. `handleUnhandledError`'s compensate branch calls `beginCompensation`
with no in-flight guard, unlike `stepCompensateRequested`. Measured on `main`:

```
AFTER s1 (walk starts)     activeCmd="i1-c2" resumeNode="endA"  InvokeAction{i1-c2 undoA}
AFTER s2 (uncaught error)  activeCmd="i1-c3" resumeNode="endA" finalStatus=failed finalErr="boom"
                                                               InvokeAction{i1-c3 undoA}
AFTER completing the cursor command → status=completed  cmds=[CompleteInstance]
```

Three defects: `undoA` dispatched **twice**; the first walk's command orphaned; and — worst —
**the uncaught error silently swallowed, the process reporting success**, because the inherited
`ResumeNode` beats the recorded `FinalStatus` in `stepCompensationFinish`.

**Decision:** when a walk is already in flight, do not start a second one. Cancel the remaining
tokens/timers/arms as `beginCompensation` would, then clear `ResumeNode`/`ResumeScope` and stamp
`FinalStatus=StatusFailed` / `FinalErr=errorCode` on the **existing** cursor.

Clearing `ResumeNode` is the load-bearing half: stamping the outcome alone is **not** sufficient,
since the swallowed-error case is precisely where both fields were already set correctly and were
ignored. A stamp-only shape was also measured to leave a third parallel branch **live and
completable by a human while the instance was doomed**; keeping the cancellation matches `main`.

```
AFTER s2 (uncaught error, walk live)  status=compensating tokens=0 activeCmd="i9-c2"
                                      resumeNode="" finalStatus=failed finalErr="boom"
                                      cmds=[UpdateTask, UpdateTask]     ← no second undoA
AFTER walk finishes                   status=failed tokens=0 cmds=[FailInstance]
```

`go test ./engine/` → **EXIT=0, zero failures**: no existing test pins the old behaviour either
way. Pre-existing on `main` (both probes re-run on unpatched code, identical output).

## 6. What this spec REFUTES in its own predecessor

`docs/specs/2026-08-08-adr-0158-premise-evidence.md` §Q4 concludes:

> **(c) PREDICATE: `s.Status != StatusRunning`**, not `IsTerminal()`. Gate **all four tiers**.

and records a "verified fix shape": `if s.Status != StatusRunning { return nil, nil }` in
`fireBoundaryArm`, noting the engine suite stays green with it.

**EXECUTED here, and refuted.** Applied to `fireBoundaryArm` against the §3.1 fixture:

```
AFTER SIGNAL#2 (s2)  status=compensating tokens=1 boundaries=1 activeCmd="i1-c2" cmds=[]
AFTER undoA(i1-c2)   status=running      tokens=1 boundaries=1 activeCmd=""      cmds=[]
```

The rollback survives, but **signal `s2` is silently swallowed and the instance is stranded
forever** — `taskB` stays parked with `bndB` still armed and no delivery left to resume it.
Signals are one-shot broadcasts; nothing redelivers. `go test ./engine/` → EXIT=0, which
establishes only that **nothing covers this either way**.

Two lessons, both instances of documented failure modes in `CLAUDE.md`:

1. **"The suite stays green" is not verification of a fix** — it is a measurement of test
   coverage. The evidence file offered suite-green as support for a shape that is wrong.
2. **`!= StatusRunning` conflates the two meanings of `Compensating` (§2).** It blocks
   boundary/gateway arms only on the `startCompensationWalk` route, where firing is
   legitimate, and is a no-op on the `beginCompensation` route, where those arms are already
   drained. It is inverted with respect to its own purpose — the same defect class ADR-0165
   shipped and CLAUDE.md rule #9 was amended for.

The correct decomposition is two predicates: `Compensating.ActiveCmdID` guards *completion*
(0168); `IsTerminal()` guards *mid-delivery dispatch* (0169). Neither is `!= StatusRunning`.

**The refuted shape is already SHIPPED in one place, and it misbehaves exactly as predicted.**
`fireEventTriggeredSubprocessArm` gates root-scope event-sub-process arms on
`if s.Status != StatusRunning` (`engine/step_eventsubprocess.go:167`). Executed against a
*legitimate* local compensation throw — a sibling branch still live, correct BPMN — the
one-shot broadcast signal is swallowed and the root ESP arm never fires:

```
AFTER doA (local throw started a walk; sibling 'hold' live)  status=compensating tokens=1 esp=1 activeCmd="il-c2"
AFTER SIGNAL boom during a LOCAL compensation throw          status=compensating tokens=1 esp=1 activeCmd="il-c2"
RESULT: status=compensating esp=1 commands=0   ← root ESP arm SILENCED, nothing redelivers
```

This is the same stranding §6 measures for `fireBoundaryArm`, already in production. It
strengthens the argument above from "we reasoned this predicate is wrong" to "this predicate
is wrong and here is the shipped instance". The future ESP ADR must therefore **not** re-derive
`!= StatusRunning` from that file — see §7.

**Action:** the evidence file's Q4(c) and its "Two NEW pre-existing bugs" item 1 must carry a
correction note pointing here. The file is a preserved input to the future ADR-0158 rewrite;
leaving the refuted shape unmarked would propagate it.

## 7. Scope

**In:** the three decisions in §5 (ADR-0168, ADR-0169, ADR-0170), the re-fixture in §5.3,
regression tests (§8), the correction note on the evidence file (§6), and the two stale
in-code citations noted at the end of this section.

**Out, each with a reason:**

- **The ESP hole (§2), which has TWO directions.** Its own ADR must cover both or it will fix
  one and ship the other:
  1. *Arms firing when they should not* — `fireEventTriggeredSubprocessArm` checks status for
     the root scope only, and `beginCompensation` does not drain ESP arms, so a **non-root**
     ESP arm fires into a completed instance and emits a live `InvokeAction`.
  2. *Arms not firing when they should* — the root check is the refuted `!= StatusRunning`
     predicate, measured silencing a legitimate signal in §6. **ADR-0168 lengthens the window
     in which it is true**, so this direction gets marginally worse in this bundle.

  It also falsifies **ADR-0124 Decision item 4's** "harmless lingering arm" sentence.
- **An operator escape from a stalled compensation walk.** ADR-0168's deferral means a walk
  whose `ActionCompleted` never arrives leaves the instance permanently stuck, and
  `CancelRequested` cannot release it (measured escape matrix in ADR-0168's Consequences).
  Likely shape: let `CancelRequested` terminate immediately when the walk has no live tokens.
  Behaviour change on an ADR-0039/0109 path — needs its own evidence. Own ADR; backlog.
- **`processtest.Classify` has no reason for a compensation-walk park.** Measured
  `reason="unknown"` → `ErrUnhandledPark`. Reachable on `main` already via the rollback route;
  ADR-0168 makes it reachable on the ordinary throw route. Either add a `ReasonCompensation`
  (surfacing the awaited command id) or document it in `Classify`'s godoc with a regression
  test pinning today's `ReasonUnknown` — §8 T10 takes the cheaper option for this bundle.
  Same class as backlog 6b (`Park.HasArmedTimers`).

  🛑 **SEVERITY CORRECTED 2026-08-09 — the blast radius stated here and in audit finding D-13
  is overstated.** Both claimed a consumer *"using the shipped harness to drive a definition
  with an in-definition `CompensationThrowEvent` … now hits a hard stop where it previously ran
  to completion."* That reproduction was **built and executed** during implementation — the
  ADR-0168 fixture driven through `processtest.Harness` with ordinary synchronous catalog
  actions — and it **never reaches the park**:

  ```
  after Start: status=running   tokens=2 activeCmd=""      classify="human-task"
  after s1:    status=running   tokens=1 activeCmd=""      classify="human-task"
  after s2:    status=completed tokens=0 activeCmd=""      classify="terminal"
  undoA invocations=1
  ```

  The runtime performs the walk's compensating `InvokeAction` and feeds its `ActionCompleted`
  back **inside the same `ApplyTrigger`**, so the walk finishes before control returns to
  `drive`. The gap is real, but it bites a consumer classifying a **stored mid-walk snapshot**,
  not the default synchronous drive loop. `ReasonUnknown` itself was re-measured post-Tasks 1–4
  and still holds, on both a hand-built snapshot and a real engine-produced mid-walk state.

  **ASSUMPTION (unverified):** that no *other* harness shape reaches the park either. One shape
  was executed; the negative is not established.
- **ADR-0158 (signal fan-out).** Unchanged; both decisions here are inputs to its rewrite.
- **Zombie scopes / incident-history retention** — the two ADRs still owed by delivery 2b.

## 8. Test plan

Every prescribed test states **what makes it fail today**. A test that cannot fail is a
defect (CLAUDE.md, Premise Discipline).

Every test below was **built and run against unpatched `main`** during the bundle's audit, so
the "fails today" column is measured, not predicted.

| # | Test | Fails today because |
|---|---|---|
| T1 | §3.1 fixture: after signal #2, `Status == StatusCompensating` and `Compensating.ActiveCmdID` non-empty | today it is `completed` with the cursor cleared (§3.2). **VALID — measured RED** |
| T2 | Same fixture: `ActionCompleted(undoA)` then completes the instance, `CompleteInstance` emitted **once** | today that trigger is refused `outcome=dropped` and no `CompleteInstance` follows. **VALID — measured RED**; also catches a wrong ADR-0169 predicate |
| T3 | Same fixture: signal #2 **is** honoured — `taskB`'s record goes `cancelled` | ⚠ **passes today AND after** — a deliberate regression pin, not a RED. Say so in its doc comment. It discriminates **twice**: measured RED under `!= StatusRunning` in `fireBoundaryArm` *and* under the same wrong predicate in `handleSignalReceived`. The existing suite is `EXIT=0` under both, so **T3 is the only thing in the repo guarding that predicate** |
| **T4′** | §4.1 fixture: **no `ScheduleTimer`**; no `UpdateTask` after `FailInstance`; the surviving token is still at `catchB` with `AwaitSignal == "x"`; `len(Boundaries) == 0`; `taskB` never appears in `History` | today the token is driven to `taskB`, `Boundaries == 1`, and a live `ScheduleTimer{Token:i1-t3}` escapes |
| T5 | §4.1 fixture: `FailInstance{boom}` is emitted **and no command follows it** *for this fixture* | the "no command follows" clause is the only falsifier — today `ScheduleTimer` is last. Measured RED. Also discriminates against the `nil`-commands abort (§5.2 Decision 4) |
| T6 | Tier-4 **intra-loop** abort: two snapshotted catch tokens, the first drives into an uncaught error | 🏆 measured RED on `main` **and** RED under the ahead-of-loop guard, GREEN in-loop. Fixture control verified: exactly two tokens await the signal, `catch1` first |
| T7 | Re-fixtured `TestActionCompletedOnTerminalInstanceIsNoOp` retains both step-2 positive controls | §5.3. Fix-independent — passes on unpatched `main`, so its task lands **first** |
| **T8** | One definition where the same signal name matches a gateway arm, a boundary arm **and** an ESP arm; assert the commands appear in gateway → boundary → ESP order | **nothing pins tier order today.** Measured: swapping tier 2↔3 → `EXIT=0`; swapping 1↔2 → `EXIT=0`. Deleting a tier *is* caught (2/7/17 failures). The closure-slice refactor is unprotected against exactly the mutation a slice literal invites |
| **T9** | Tier 1→2 guard: a gateway win drives into an uncaught error end; a boundary arm on the same signal must not fire | 🛑 **CORRECTED 2026-08-09 — this row read "no test exercises the tier-1→2 guard, the only guard site with zero coverage", implying T9 would be a falsifier. It CANNOT fail, and no fixture can make it.** Every terminal transition runs `endInstance` → `cancelAllScheduledWork`, which drains `ArmedEvents`, `Boundaries` and all ESP arms, so tiers 2–3 find nothing to fire with or without the guard — output byte-identical to unpatched `main` on two terminal routes. Kept as an explicitly-labelled **PIN**. See ADR-0169 Decision 2's amendment |
| **T10** | `processtest.Classify` on a zero-token `Compensating` instance returns `ReasonUnknown` | passes today — a **pin** recording the known gap (§7) so it is documented rather than a surprise. Pair with a godoc note on `Classify` |

⚠ **T4 as originally prescribed asserted `len(Tokens) == 0` and was VACUOUS** — measured `1`
before the fix and `1` after, because the fix declines to *drive* the token rather than
deleting it. It contradicted ADR-0169's own Consequences. All three auditors flagged it
independently. T4′ replaces it; do not reinstate the count assertion.

**Mutation duty.** T1, T4′ and T6 are load-bearing. For each: break the production line on
purpose, observe RED, restore from a snapshot, `diff` to confirm. A mutation that fails to
**compile** is not a RED; one that cannot **discriminate** is not verification.

🛑 **CORRECTED 2026-08-09 — this note read: "Only ONE of ADR-0168's three conjuncts
discriminates … do not manufacture a third mutation from the two ESP sites to hit a quota."
Refuted.** All three conjuncts discriminate, each against its own fixture (§5.1). The
`EXIT=0`-with-one-site-patched measurement was about *test coverage at the time*, not about the
conjuncts. The two ESP mutations are therefore **real mutations to run**, not a manufactured
quota — and they are additional to, not substitutes for, the T1/T4′/T6 mutation duty above.

**Coverage.** `engine` baseline to hold: **91.9 %**. Repo: **73.9 %** (post-ADR-0167;
re-derive rather than trusting this line).

## 9. Assumptions (unverified — marked, not hidden)

- **ASSUMPTION (unverified):** that the escaping `ScheduleTimer` actually arms a gocron job
  for a dead instance. Observed only in `StepResult.Commands`; the runtime path is
  Docker-gated. Inherited from the evidence file and **not** re-derived here.
- ~~**ASSUMPTION (unverified):** that sites `:327` and `:425` are reachable with a live
  compensation cursor.~~ **DISCHARGED 2026-08-09 — and it resolved AGAINST the spec's
  expectation.** Both sites are reachable and both conjuncts discriminate; reproductions built,
  independently re-verified by revert-and-run. See §5.1. Left visible rather than deleted: the
  spec had gone further than this assumption licensed, asserting in §5.1 and §8 that the two
  conjuncts were "provably non-discriminating" — a marked assumption in one section restated as
  plain fact in two others, which is the exact quantifier/recap failure `CLAUDE.md`'s Premise
  Discipline names.
- ~~Not executed: `mergeVars` merge-once across an aborted delivery.~~ **Discharged** — the
  semantics are now stated in ADR-0169 Decision 4: `mergeVars` runs inside the first tier that
  *matches*, so an abort after a match returns state carrying the payload and an abort before
  any match leaves `Variables` untouched. (Left visible rather than deleted: the audit found
  the spec had delegated this to ADR-0169 while ADR-0169 never mentioned `mergeVars` — the
  ADR-0162 zombie-scope shape, caught here rather than after shipping.)
- **ASSUMPTION (unverified):** the repo-wide 73.9 % coverage floor. Requires Docker; not
  re-measured during the audit. The `engine` baseline of **91.9 % was** re-measured and holds.

## 10. Where the executed evidence lives

**`docs/specs/2026-08-08-adr-0168-0170-audit-evidence.md`** — the rule-#9 audit's full record,
in the repo: three auditors' findings with verbatim measurements (§A test-plan lens, §B design
lens, §C premise lens), the measured ADR-0170 patch (§D), and four compiling, executed test
sources (§E). Read §C's "Confirmed" list before re-deriving anything — it records what was
already checked.

### 10.1 Controller probe sources (superseded by the file above)

The four probes backing §2, §3.2, §3.4, §4.2 and §6 are preserved in this session's
scratchpad (`zz_probe_bug1_test.go`, `zz_probe_bug1b_test.go`, `zz_probe_bug2_test.go`, plus
`probeA.log` / `probeB.log` / `suiteA.log` / `suiteB.log`). They are throwaway white-box
`package engine` tests; the numbers above are what they printed. Implementation should
rebuild them as the real regression tests in §8 rather than restoring them verbatim.
