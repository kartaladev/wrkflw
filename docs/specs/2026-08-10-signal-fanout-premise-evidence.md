# Evidence — ADR-0158 / ADR-0172 premise re-derivation

- Date: 2026-08-10
- Base: **`main` @ `571a380`**, branch `feat/signal-fanout-and-esp-status`.
- Status: **evidence only.** Not a spec, not a decision. This is the executed
  ground truth the delivery's spec, ADRs and plan build on.

> **Why this file exists.** The parked ADR-0158 draft was written against
> `9656799`, re-derived once at `9e96112`, and has since been overtaken by
> ADR-0167 and ADR-0168/0169/0170/0171 — the last of which rewrote
> `handleSignalReceived` outright. CLAUDE.md's Premise Discipline forbids a
> factual claim about current behaviour entering a design document unexecuted, so
> every premise was re-run from scratch rather than restated.
>
> ⚠ The predecessor evidence file
> `docs/specs/2026-08-08-adr-0158-premise-evidence.md` is **superseded for every
> claim it shares with this one**. It is kept because it records that
> `!= StatusRunning` was once recommended, and laundering that would hide it. Its
> §Q4(c) fix shape is REFUTED; do not carry it forward.

## How this was produced

Three Opus subagents, each in its **own `git worktree`** (so no agent could
measure against another's patched tree), plus measurements run directly by the
controller. Each was briefed to EXECUTE every claim and paste verbatim output,
to mark anything unexecuted as `ASSUMPTION (unverified)`, to judge every run by
its **exit code** rather than a piped tail, and to restore every patched file
from a snapshot and prove it with `git status --short` + `git diff`.

| section | source | claims |
|---|---|---|
| A | premise agent A | D1–D9 — the defect and the dispatch shape |
| B | premise agent B | M1–M10 — arms mechanics and identity |
| CTL | controller | intra-delivery arm creation; per-family ordering and its bound |
| C | premise agent C | E1–E8 — the event-sub-process status guard |

⚠ **Agent B's first M6 probe was VACUOUS and it said so.** Routing the
interrupting boundary to an end event completed the instance, so `endInstance`'s
terminal sweep also drained every arm and the observed 3→0 could not be
attributed to `cancelTokenWaits`. The fixture now routes to a parking `UserTask`.
M4/M7/M9 were re-checked for the same trap. This is recorded because a probe that
cannot discriminate is worse than no probe.

---

# Section A — the defect and the dispatch shape (agent A, claims D1–D9)


## Base
`git rev-parse --short HEAD` → `571a380`; `git status --short` → (clean); `go version` → `go1.26.4 darwin/arm64`.

---

## D1 — headline defect still reproduces. **VERDICT: CONFIRMED.**

Fixture `probeD1Def` (engine/zz_probe_a_d1_test.go, throwaway):
`start → fork(parallel) ⇒ {taskA[bndA sig "escalate", interrupting], taskB[bndB sig "escalate", interrupting]}`.
One `SignalReceived{Name:"escalate", Payload:{why:sla}}` via public `engine.Step`.

Command: `go test -run '^TestProbeD1SignalFanOut$' -v ./engine/ > /tmp/d1.log 2>&1; echo "EXIT=$?"` → `EXIT=0`, `=== RUN TestProbeD1SignalFanOut` present.

Verbatim:
```
BEFORE status=running tokens=2
  BEFORE token id=i1-t2 node=taskA state=1 awaitSignal="" awaitCommand="i1-h1" scope=""
  BEFORE token id=i1-t3 node=taskB state=1 awaitSignal="" awaitCommand="i1-h2" scope=""
BEFORE len(Boundaries)=2
  BEFORE boundary {HostToken:i1-t2 HostNode:taskA BoundaryNode:bndA Flow:f-bnda-esca NonInterrupting:false triggerMatch:{TimerID: Signal:escalate Message: MessageKey:} Action:}
  BEFORE boundary {HostToken:i1-t3 HostNode:taskB BoundaryNode:bndB Flow:f-bndb-escb NonInterrupting:false triggerMatch:{TimerID: Signal:escalate Message: MessageKey:} Action:}
BEFORE len(Tasks)=2
  BEFORE task id=i1-h1 node=taskA state=unclaimed
  BEFORE task id=i1-h2 node=taskB state=unclaimed
BEFORE len(ArmedEvents)=0 len(EventTriggeredSubprocesses)=0 len(Timers)=0
BEFORE Variables=map[]
BEFORE commands=2
  BEFORE cmd engine.AwaitHuman {TaskID:i1-h1 ...}
  BEFORE cmd engine.AwaitHuman {TaskID:i1-h2 ...}
BEFORE History=4  (start, fork, taskA, taskB — all close="")

AFTER status=running tokens=1
  AFTER token id=i1-t3 node=taskB state=1 awaitSignal="" awaitCommand="i1-h2" scope=""
AFTER len(Boundaries)=1
  AFTER boundary {HostToken:i1-t3 HostNode:taskB BoundaryNode:bndB Flow:f-bndb-escb NonInterrupting:false triggerMatch:{TimerID: Signal:escalate Message: MessageKey:} Action:}
AFTER len(Tasks)=2
  AFTER task id=i1-h1 node=taskA state=cancelled
  AFTER task id=i1-h2 node=taskB state=unclaimed
AFTER len(ArmedEvents)=0 len(EventTriggeredSubprocesses)=0 len(Timers)=0
AFTER Variables=map[why:sla]
AFTER commands=1
  AFTER cmd engine.UpdateTask {Task:{TaskID:i1-h1 ... State:cancelled ...}}
AFTER History=5 (start, fork, taskA[close=boundary_interrupted], taskB, endEscA)
```

**Delta:** tokens 2→1; `len(Boundaries)` 2→1; tasks 2→2 but only `i1-h1` → `cancelled`;
`Variables` `map[]` → `map[why:sla]`; commands: exactly **one** `UpdateTask`, nothing for taskB.
Exactly one host (taskA) interrupted; **taskB stays parked with `bndB` still armed** and its
task still `unclaimed`. The signal is consumed once and lost for the second family member.

---

## D2 — the same shape through the PUBLIC `processtest` harness. **VERDICT: CONFIRMED (with a new sub-finding).**

Fixture `probeD2Def` (processtest/zz_probe_a_d2_test.go, throwaway) — the D1 shape via
`definition.NewBuilder`, each boundary routed through a `notify` ServiceTask so a fire is
countable on the spy catalog.

Command: `go test -run '^TestProbeD2' -v ./processtest/ > /tmp/d2b.log 2>&1; echo "EXIT=$?"` → `EXIT=0`; all three `=== RUN` lines present.

### D2a — `PublishSignal` alone: the drive FAILS.
```
START status=running tokens=2
START len(Boundaries)=2 SignalWaiters=[escalate escalate] MessageWaiters=[]
START Classify reason=human-task node=taskA awaitingSignals=[escalate] openTasks=2 hasArmedTimers=false
DriveToCompletion err=workflow-processtest: unhandled park: human-task at node "taskB"
FINAL status=running tokens=1
  FINAL token node=taskB
FINAL len(Boundaries)=1 SignalWaiters=[escalate]
FINAL task taskA state=cancelled / taskB state=unclaimed
FINAL History: start, fork, taskA[close=boundary_interrupted], taskB, escA, endEscA
catalog notify count=1
```
So ADR-0166 DID make the arm-only park driveable — the FIRST arm fires — but the drive then
dies with `ErrUnhandledPark`. `PublishSignal`'s own `armFireLog` bound
(`processtest/handlers.go:191`, "fires once per instance per PARKED NODE") refuses the second
delivery: at the first park `waitingNodes = [taskA, taskB]` marks BOTH, so at the second park
`waitingNodes = [taskB]` is not fresh → `Pass()` → unhandled.

### D2b — two EXPLICIT deliveries straight through `h.Driver().ApplyTrigger`: both arms DO fire.
```
AFTER-1 status=running tokens=1  len(Boundaries)=1  taskA=cancelled taskB=unclaimed  notify… escA,endEscA
AFTER-2 status=completed tokens=0 len(Boundaries)=0 SignalWaiters=[]
  AFTER-2 task taskA=cancelled taskB=cancelled
  AFTER-2 History: start, fork, taskA[boundary_interrupted], taskB[boundary_interrupted], escA, endEscA, escB, endEscB
catalog notify count=2
```
⇒ `bndB` is **live and reachable** — the engine is not dropping it, it is deferring it to the
NEXT delivery. The defect is precisely *one delivery fires one arm per family*, not *the second
arm is dead*.

### D2c — the realistic consumer shape `Chain(PublishSignal, CompleteTasks)`: SILENT WRONG ANSWER.
```
DriveToCompletion err=<nil>
FINAL status=completed tokens=0
  FINAL task taskA=cancelled  taskB=completed
FINAL History: start, fork, taskA[boundary_interrupted], taskB, escA, endEscA, endB
catalog notify count=1
```
The broadcast escalated branch A and let branch B **complete normally down `endB`**, and the
drive reported **success**. This is the shape a consumer actually writes, and today it is
green while being semantically wrong.

### Sub-finding D2-x (new, not in the prior evidence file)
`InstanceState.SignalWaiters()` returns `[escalate escalate]` — the engine authority **does**
expose the multiplicity. `processtest.Classify`'s `Park.AwaitingSignals` collapses it to
`[escalate]`. Any ADR-0158 acceptance test written against `Park.AwaitingSignals` therefore
cannot see "two arms on one name"; `st.SignalWaiters()` can.

**Is the fan-out observable from the public harness?** YES, and cheaply falsifiable: with
fan-out, D2a's single `PublishSignal` delivery fires both arms in ONE Step, the drive
completes, and `notify count` goes 1 → 2. Today D2a errors — so a test asserting
`require.NoError(driveErr)` + `Count("notify") == 2` on D2a is RED today and GREEN after.

---

## D3 — current structure of `handleSignalReceived`. **VERDICT: CONFIRMED-BUT-MOVED** (ADR-0169 rewrote it; the prior evidence file's line numbers are dead).

`engine/step_triggers.go`, `func handleSignalReceived` — **line 741** (was elsewhere pre-0169).
Symbol layout (`grep -n` on the file, line numbers @ `571a380`):

| symbol | line | shape |
|---|---|---|
| `handleSignalReceived` | 741 | `(ctx, def, s *InstanceState, t SignalReceived, opt StepOptions) (StepResult, error)` |
| `snapshotIDs := s.tokenIDsAwaitingSignal(t.Name)` | **770** | taken BEFORE tiers 1–3 (BPMN delivery-instant snapshot) |
| `matched := false` / `var signalCmds []Command` | 772–773 | |
| `markMatched := func()` | **780** | closure; `mergeVars(s, t.Payload)` at **782**, once, guarded by `matched` |
| `tiers := []func() ([]Command, error){…}` | **804** | slice of 3 lookup-and-fire closures |
| tier 1 closure — `s.armedEventBySignal(t.Name)` → `resolveGatewayWin` | 806–813 (lookup 807, fire 812) | |
| tier 2 closure — `s.boundaryArmBySignal(t.Name)` → `fireBoundaryArm` | 815–822 (lookup 816, fire 821) | |
| tier 3 closure — `s.eventTriggeredSubprocessArmBySignal(t.Name)` → `fireEventTriggeredSubprocessArm` | 824–831 (lookup 825, fire 830) | |
| **tier terminal re-check loop** `for _, fire := range tiers { if s.Status.IsTerminal() {…} }` | **843–852** (guard **844**) | returns `StepResult{State:*s, Commands: signalCmds}` — PARTIAL commands kept |
| **tier-4 loop** `for _, tokenID := range snapshotIDs` | **861–885** (guard **862**) | per-iteration re-check INSIDE the loop |

⚠ Tiers 1–3 use SINGULAR first-match lookups; tier 4 loops. Each tier closure performs its
**own** lookup at the moment it runs — the file carries an explicit `⚠` comment (lines 792–803)
forbidding hoisting them, stating hoisting all three leaves `go test ./engine/` at EXIT=0 and
that it survives only because all three fire functions happen to re-validate.

### Execution proof that BOTH re-checks exist and RUN
Instrumented both guard sites with `fmt.Fprintf(os.Stderr, "PROBE-A-D3 …")` (patch to
`engine/step_triggers.go`, snapshot `/tmp/snap_step_triggers.go`, restored + md5-verified after).

`go test -run '^TestProbeD1SignalFanOut$' -v ./engine/` → `EXIT=0`:
```
PROBE-A-D3 tier-loop iter=0 status=running isTerminal=false
PROBE-A-D3 tier-loop iter=1 status=running isTerminal=false
PROBE-A-D3 tier-loop iter=2 status=running isTerminal=false
```
(no tier-4 iteration: D1's fixture parks on user tasks, `snapshotIDs` is empty.)

`go test -run 'TestSignalDeliveryStops' -v ./engine/` → `EXIT=0`, both `=== RUN` present:
```
PROBE-A-D3 tier-loop iter=0 status=running isTerminal=false
PROBE-A-D3 tier-loop iter=0 status=running isTerminal=false
PROBE-A-D3 tier-loop iter=1 status=running isTerminal=false
PROBE-A-D3 tier-loop iter=1 status=running isTerminal=false
PROBE-A-D3 tier-loop iter=2 status=running isTerminal=false
PROBE-A-D3 tier4-loop iter=0 token=i1-t2 status=running isTerminal=false
PROBE-A-D3 tier4-loop iter=1 token=i1-t3 status=failed  isTerminal=true
PROBE-A-D3 tier4-loop ABORT at iter=1
--- PASS: TestSignalDeliveryStopsInsideTheTokenLoop
PROBE-A-D3 tier-loop iter=2 status=failed isTerminal=true
PROBE-A-D3 tier-loop ABORT at iter=2
--- PASS: TestSignalDeliveryStopsAtMidDeliveryTerminal
```
Both guards **fire on real state** (`isTerminal=true` observed at tier-loop iter=2 and
tier4-loop iter=1) — not dead code. Restored: `md5` of `engine/step_triggers.go` == snapshot.

---

## D4 — call sites of the three singular signal wrappers. **VERDICT: CONFIRMED — exactly ONE call site each, all inside `handleSignalReceived`. ZERO test call sites.**

`grep -rn --include="*.go" "<name>" .` (repo root, includes all `_test.go`):
```
armedEventBySignal
engine/step_triggers.go:807:			ae := s.armedEventBySignal(t.Name)
engine/state_arms.go:306:// armedEventBySignal returns a pointer to the first armedEvent with the given
engine/state_arms.go:308:func (s *InstanceState) armedEventBySignal(name string) *armedEvent {
=== boundaryArmBySignal ===
engine/step_triggers.go:816:			ba := s.boundaryArmBySignal(t.Name)
engine/state_arms.go:339:// boundaryArmBySignal returns a pointer to the first boundaryArm with the given
engine/state_arms.go:341:func (s *InstanceState) boundaryArmBySignal(name string) *boundaryArm {
=== eventTriggeredSubprocessArmBySignal ===
engine/step_triggers.go:825:			ea := s.eventTriggeredSubprocessArmBySignal(t.Name)
engine/state_arms.go:366:// eventTriggeredSubprocessArmBySignal returns a pointer to the first
engine/state_arms.go:368:func (s *InstanceState) eventTriggeredSubprocessArmBySignal(name string) *eventTriggeredSubprocessArm {
```
(Only the declaration + doc line + the single call each — no other hit anywhere, tests included.)

**Executed proof (deletion probe).** Deleted all three method bodies from `engine/state_arms.go`
(snapshot `/tmp/snap_state_arms.go`); `go build ./...` → `EXIT=1` and `go vet ./...` → `EXIT=1`
(vet also compiles the Docker-only test packages), with **exactly three** errors, all in
`step_triggers.go`:
```
engine/step_triggers.go:807:12: s.armedEventBySignal undefined (type *InstanceState has no field or method armedEventBySignal)
engine/step_triggers.go:816:12: s.boundaryArmBySignal undefined (...)
engine/step_triggers.go:825:12: s.eventTriggeredSubprocessArmBySignal undefined (...)
```
Restored; `md5` matches snapshot; `go vet ./...` → `EXIT=0`.

⇒ **Deleting all three is safe** once tiers 1–3 stop calling them. Nothing else — no other
handler, no test — references them. **Caveat for the plan:** the shared generic
`armBySignal` (`engine/state_arms.go:225`) must NOT be deleted: `engine/state_arms_test.go:67`
(`TestArmBySignal`) calls it directly, and it is also the body of the three wrappers.
The by-TIMER and by-MESSAGE wrappers (`armedEventByTimer`/`ByMessage`, `boundaryArmByTimer`/
`ByMessage`, `eventTriggeredSubprocessArmByTimer`/`ByMessage`) are a separate set and stay.

---

## D5 — timer and message dispatch must stay first-match. **VERDICT: CONFIRMED.**

### D5a — routing through `dispatchArmCascade` (EXECUTED, not read).
Instrumented `dispatchArmCascade` (`engine/step_arm_dispatch.go:28`, snapshot
`/tmp/snap_step_arm_dispatch.go`, restored + md5-verified) with a stderr print on entry.
`go test -run '^TestProbeD5|^TestProbeD1SignalFanOut$' -v ./engine/` → `EXIT=0`:
```
=== RUN   TestProbeD1SignalFanOut
--- PASS: TestProbeD1SignalFanOut            ← ZERO "ENTERED" lines: signal does NOT use the cascade
=== RUN   TestProbeD5MessageStaysFirstMatch
PROBE-A-D5 dispatchArmCascade ENTERED status=running     (×1)
--- PASS
=== RUN   TestProbeD5TimerIDsUniquePerArm
PROBE-A-D5 dispatchArmCascade ENTERED status=running     (×2, one per TimerFired)
--- PASS
```
⇒ `handleMessageReceived` (call at `engine/step_triggers.go:1009`) and `handleTimerFired`
(call at `engine/step_triggers.go:501`) **do** route tiers 1–3 through `dispatchArmCascade`;
`handleSignalReceived` does **not** (it has its own `tiers` slice since ADR-0169).
This is the seam ADR-0158 must leave alone.

### D5b — TWO message boundary arms, same name, different hosts, ONE delivery → exactly ONE fires.
Fixture `probeD5MessageDef`; `go test -run '^TestProbeD5' -v ./engine/` → `EXIT=0`.
```
MSG-BEFORE status=running tokens=2  len(Boundaries)=2  MessageWaiters=[{cancelIt } {cancelIt }]
  boundary {HostToken:i1-t2 HostNode:taskA BoundaryNode:bndA ... Message:cancelIt MessageKey:}
  boundary {HostToken:i1-t3 HostNode:taskB BoundaryNode:bndB ... Message:cancelIt MessageKey:}
  tasks: i1-h1 unclaimed, i1-h2 unclaimed
MSG-AFTER  status=running tokens=1  len(Boundaries)=1  MessageWaiters=[{cancelIt }]
  tasks: i1-h1 cancelled, i1-h2 unclaimed
  Variables map[] → map[k:1]
  commands=1: engine.UpdateTask{... TaskID:i1-h1 State:cancelled}
  History: start, fork, taskA[boundary_interrupted], taskB, endCancA
```
**Exactly one arm fires. This is CORRECT (point-to-point) and ADR-0158 must NOT change it.**
Note the observable shape is byte-for-byte the same as D1's signal delivery — so any
fan-out test must distinguish the two by TRIGGER TYPE, not by end state.

### D5c — TimerID uniqueness per arm.
```
TMR-BEFORE len(Boundaries)=2
  boundary bndA ... TimerID:i1-tm1
  boundary bndB ... TimerID:i1-tm2
TMR scheduled timer ids=[i1-tm1 i1-tm2] (distinct=true)
TMR-AFTER-1 (fired i1-tm1): tokens=1 Boundaries=1(bndB, i1-tm2) taskA=cancelled
            commands: CancelTimer{i1-tm1}, UpdateTask{i1-h1 cancelled}
TMR-AFTER-2 (fired i1-tm2): status=completed tokens=0 Boundaries=0 taskB=cancelled
            commands: CancelTimer{i1-tm2}, UpdateTask{i1-h2 cancelled}, CompleteInstance{}
```
⇒ **A TimerID is unique per arm** (`i1-tm1` vs `i1-tm2` for two arms with the SAME 30m
duration), so a `TimerFired` names exactly one arm by construction. Fan-out is not merely
undesirable for timers — it is **meaningless**: the timer key is already 1:1 with the arm.

---

## D6 — cross-family broadcast already works. **VERDICT: CONFIRMED. This is the regression baseline.**

Fixture `probeD6Def` (engine/zz_probe_a_d6_test.go) — ONE signal name `"x"` matched by all four
dispatch points at once:
`start → fork ⇒ {A: gw(event-based) ⇒ gwSig(sig "x") | gwTmr(2h); B: taskB(user) ⊸ bndB(sig "x", interrupting); C: catchC(intermediate catch sig "x")}` plus a root **NON-interrupting** event
sub-process `esp` whose inner start is `espStart(sig "x")`.

Command: `go test -run '^TestProbeD6' -v ./engine/` → `EXIT=0`, `=== RUN` present.
```
D6-BEFORE status=running tokens=3
  token i1-t2 node=gw     awaitCommand="evtgw:i1-t2"
  token i1-t3 node=taskB  awaitCommand="i1-h1"
  token i1-t4 node=catchC awaitSignal="x"
D6-BEFORE len(Boundaries)=1  len(ArmedEvents)=2  len(EventTriggeredSubprocesses)=1  len(Tasks)=1
D6-BEFORE Variables=map[]  SignalWaiters=[x x x x]      ← FOUR waiters, one per family
D6-BEFORE commands=2: ScheduleTimer{i1-tm1 …}, AwaitHuman{i1-h1}

D6-AFTER status=running tokens=4
  token i1-t2 node=gwWork  awaitCommand="i1-c1"   ← tier 1 (event gateway) fired
  token i1-t4 node=cWork   awaitCommand="i1-c4"   ← tier 4 (parked catch token) fired
  token i1-t5 node=bndWork awaitCommand="i1-c2"   ← tier 2 (boundary) fired
  token i1-t6 node=espWork awaitCommand="i1-c3" scope="i1-s1"  ← tier 3 (ESP) fired
D6-AFTER len(Boundaries)=0 len(ArmedEvents)=0 len(EventTriggeredSubprocesses)=1
D6-AFTER Variables=map[p:7]     SignalWaiters=[x]
D6-AFTER commands=6, IN THIS ORDER:
  1 engine.CancelTimer  {TimerID:i1-tm1}                              (tier 1 loser arm)
  2 engine.InvokeAction {CommandID:i1-c1 Name:gw-action  …}           (tier 1)
  3 engine.UpdateTask   {TaskID:i1-h1 State:cancelled}                (tier 2 host teardown)
  4 engine.InvokeAction {CommandID:i1-c2 Name:bnd-action …}           (tier 2)
  5 engine.InvokeAction {CommandID:i1-c3 Name:esp-action …}           (tier 3)
  6 engine.InvokeAction {CommandID:i1-c4 Name:c-action   …}           (tier 4)
D6-AFTER History=10: start, fork, gw, taskB[boundary_interrupted], catchC,
                     gwWork, bndWork, espStart, espWork, cWork
```
⇒ All four families fire in **one** `StepResult`, in tier order, with the payload merged once.
Two facts a fan-out change must preserve:
- the **command ORDER** above (tier order, with each tier's teardown before its InvokeAction);
- `EventTriggeredSubprocesses` stays at **1** after the fire — a NON-interrupting ESP arm is
  repeatable (ADR-0124), which is why `SignalWaiters` is `[x]` and not `[]` afterwards.

---

## D7 — `mergeVars` / `markMatched` semantics. **VERDICT: CONFIRMED (both halves).**

Command: `go test -run '^TestProbeD7' -v ./engine/` → `EXIT=0`, both `=== RUN` present.

### (a) A delivery matching NOTHING leaves `State.Variables` untouched.
```
D7a BEFORE Variables=map[seed:kept] StartVariables=map[seed:kept]  SignalWaiters=[x x x x]
   delivered SignalReceived{Name:"no-such-signal", Payload:{poison:must-not-appear, seed:OVERWRITTEN}}
D7a AFTER  Variables=map[seed:kept]
D7a AFTER  tokens=3 boundaries=1 armedEvents=2 esp=1 commands=0
```
`poison` never appears and `seed` is NOT overwritten; nothing else moves; **zero commands**.

### (b) A matching delivery merges the payload EXACTLY ONCE.
```
D7b BEFORE Variables=map[seed:kept]
   delivered SignalReceived{Name:"x", Payload:{p:7, seed:REPLACED}}
D7b AFTER  Variables=map[p:7 seed:REPLACED]
```
"Exactly once" is not observable from the final map (merging twice is idempotent), so it was
**instrumented**: counters added around `markMatched` in `engine/step_triggers.go:780`
(snapshot `/tmp/snap_step_triggers.go`, restored + md5-verified).
`go test -run '^TestProbeD7|^TestProbeD6' -v ./engine/` → `EXIT=0`:
```
=== RUN   TestProbeD6CrossFamilyBroadcast
PROBE-A-D7 mergeVars FIRED call#1 vars-before=map[] payload=map[p:7]
PROBE-A-D7 TOTALS markMatchedCalls=4 mergeCalls=1 finalVars=map[p:7]
=== RUN   TestProbeD7NoMatchLeavesVariablesUntouched
PROBE-A-D7 TOTALS markMatchedCalls=0 mergeCalls=0 finalVars=map[seed:kept]
=== RUN   TestProbeD7MatchMergesPayload
PROBE-A-D7 mergeVars FIRED call#1 vars-before=map[seed:kept] payload=map[p:7 seed:REPLACED]
PROBE-A-D7 TOTALS markMatchedCalls=4 mergeCalls=1 finalVars=map[p:7 seed:REPLACED]
```
⇒ 4 `markMatched` calls → **1** `mergeVars`; 0 calls → 0 merges.
**Implication for ADR-0158:** a fan-out that fires N arms per family will call `markMatched`
N× more; the `matched` latch already makes that a no-op, so the once-only merge survives a
fan-out **for free** — but only if the fan-out keeps calling `markMatched` and never inlines
`mergeVars` into each fire.

---

## D8 — an ERROR mid-dispatch. **VERDICT: CONFIRMED — but the brief's SUGGESTED error source is REFUTED.**

Command: `go test -run '^TestProbeD8' -v ./engine/` → `EXIT=0`, all three `=== RUN` present.

### ⚠ D8-0 — REFUTED: "a boundary whose outgoing flow targets a MISSING NODE errors".
It does **not** error. It logs a WARN and **parks a token on the missing node**:
```
2026/08/10 11:55:51 WARN token routed to a missing node instance_id=i1 token_id=i1-t4 node_id=ghost
D8-ghost err=<nil>
D8-ghost result: status="running" tokens=2 cmds=1
  token i1-t3 node=taskB  awaitCommand="i1-h2"
  token i1-t4 node=ghost  state=1(TokenWaiting) awaitSignal="" awaitCommand=""
  len(Boundaries)=1  tasks: i1-h1 cancelled, i1-h2 unclaimed
  Variables=map[leak:yes]
  History: …, taskA[boundary_interrupted], taskB, ghost
```
Had I written the ADR against "flow targets a missing node ⇒ error", the whole D8 analysis
would have been built on a false premise. **Do not put that sentence in ADR-0158.**
(Side observation, own follow-up: the resulting token is a permanent wedge — `TokenWaiting`
at a node that does not exist, `AwaitCommand=""`, so nothing can ever resume it, and the
instance stays `running` forever. Not this delivery's problem; worth a backlog line.)

### D8-1 — the REAL reachable error: `fireBoundaryArm`'s "outgoing flow not found".
`engine/step_boundaries.go:123` — reached by arming with definition D and delivering with
definition D′ that has DROPPED the boundary's outgoing flow (a live shape: definitions are
looked up per-Step, arms come from persisted state).
```
D8-noflow BEFORE tokens=2 boundaries=2 tasks=2 vars=map[seed:kept]
D8-noflow err=workflow-engine: boundary "bndA": outgoing flow "f-bnda-ghost" not found
D8-noflow returned StepResult: status="" tokens=0 boundaries=0 tasks=0 vars=map[] cmds=0
D8-noflow StepResult == zero value? true
```
⇒ **`Step` returns a ZERO `StepResult`** (`reflect.DeepEqual(r2, engine.StepResult{}) == true`).
No partial state and no partial commands escape to the caller on an error.

### D8-2 — `Step` clones on entry; NO mutation escapes.
Symbol: `cloneState` — declared `engine/step_state.go:361`, called at **`engine/step.go:84`**
(`s := cloneState(st)`); it deep-copies `Variables` via `copyVars` (line 363) and
`StartVariables`.

The failing step is the sharp test: tier 2 calls `markMatched()` (`step_triggers.go:820`)
**before** `fireBoundaryArm` (821), so `mergeVars(s, {leak:"yes"})` DID run on the clone before
the error. Measured on the caller's own value:
```
D8-noflow CALLER AFTER tokens=2 boundaries=2 tasks=2 vars=map[seed:kept]
D8-noflow caller JSON identical before/after? true
D8-noflow reflect.DeepEqual(beforeCopy, afterCopy)=true
```
`leak` never reaches the caller's `Variables`, which is a **non-nil, non-empty pre-existing
map** — so this is not vacuous: an un-cloned map would have been written through.

And on the SUCCESS path (`TestProbeD8CloneOnSuccess`, D1 fixture, payload `{why:sla}`):
```
D8-clone caller vars after a SUCCESSFUL step=map[seed:kept]
D8-clone caller JSON identical before/after? true
```

**Implication for ADR-0158.** Today an error in ANY tier discards the whole delivery
(zero `StepResult`), so a fan-out that fires arm 1 successfully and errors on arm 2 would,
under today's contract, discard arm 1's effects too. That is an explicit design fork the ADR
must decide — it is NOT the same question as ADR-0169's mid-delivery TERMINAL, which
deliberately RETURNS the partial commands (`step_triggers.go:845`) rather than discarding them.
The two paths already disagree; fan-out multiplies the window.

---

## D9 — Micro mode. **VERDICT: CONFIRMED the option shape; the BEHAVIOUR is far more divergent than "one fewer node driven".**

### Option shape (verified against source, `engine/step.go:12–26`)
`type StepMode int`; `const ( Macro StepMode = iota; Micro )`; `StepOptions{Mode: engine.Micro}`.
So the brief's `StepOptions{Mode: Micro}` is right — the constant is package-level
`engine.Micro`, not a nested enum.

Command: `go test -run '^TestProbeD9' -v ./engine/` → `EXIT=0`, all three `=== RUN` present.

### D9a — one arm fires: MACRO vs MICRO on `probeD9Def` (bndA → escA1(svc a1) → escA2 → end)
```
MACRO-BEFORE tokens=2  taskA state=1(Waiting, i1-h1), taskB state=1(Waiting, i1-h2)
             len(Boundaries)=2 len(Tasks)=2 commands=2
MACRO-AFTER  tokens=2  taskB state=1(i1-h2), i1-t4 node=escA1 state=1(Waiting) awaitCommand="i1-c1"
             len(Boundaries)=1 Variables=map[why:sla]
             commands=2: UpdateTask{i1-h1 cancelled}, InvokeAction{i1-c1 a1}

MICRO-BEFORE tokens=2  taskA state=1(Waiting, i1-h1), taskB state=0(ACTIVE, no task, no arm)
             len(Boundaries)=1  len(Tasks)=1  commands=1        ← branch B never entered
MICRO-AFTER  tokens=2  taskB state=1(i1-h2), i1-t4 node=escA1 state=0(ACTIVE) awaitCommand=""
             len(Boundaries)=1 Variables=map[why:sla]
             commands=2: UpdateTask{i1-h1 cancelled}, AwaitHuman{i1-h2}   ← NO InvokeAction{a1}
             History: start, fork, taskA[boundary_interrupted], taskB, escA1
```
⇒ In Micro the boundary DOES fire (taskA interrupted, task cancelled, token placed at `escA1`),
but the token placed by the fire is left **`TokenActive` and un-driven, with NO command emitted
for it**. The drive spent its single stop on the OLDER leftover active token (taskB), which is
what emitted `AwaitHuman{i1-h2}`. The caller must Step again to make progress.

### D9b — the four-way D6 fixture in Micro (`TestProbeD9MicroFourWay`) — the sharp one
```
MICRO4-BEFORE tokens=3  gw state=1 awaitCommand="evtgw:i1-t2"
                        taskB  state=0(ACTIVE)  ← never entered: no task, no bndB arm
                        catchC state=0(ACTIVE) awaitSignal=""  ← NEVER PARKED ON THE SIGNAL
              len(Boundaries)=0 len(Tasks)=0 len(ArmedEvents)=2 len(ESP)=1 commands=1

MICRO4-AFTER-SIGNAL (one SignalReceived{"x"}) tokens=4
  i1-t2 gwWork   state=1 awaitCommand="i1-c1"     ← tier 1 FIRED and drove
  i1-t3 taskB    state=1 awaitCommand="i1-h1"     ← entered by THIS delivery's drive
  i1-t4 catchC   state=0(ACTIVE) awaitSignal=""   ← tier 4 MISSED IT ENTIRELY
  i1-t5 espStart state=0(ACTIVE) scope="i1-s1"    ← tier 3 fired but its token is un-driven
  len(Boundaries)=1  ← bndB armed DURING this delivery's own drive, so it NEVER fired
  len(ArmedEvents)=0  len(EventTriggeredSubprocesses)=1  Variables=map[p:7]
  commands=3: CancelTimer{i1-tm1}, InvokeAction{i1-c1 gw-action}, AwaitHuman{i1-h1}
  History: start, fork, gw, taskB, catchC, gwWork, espStart
```
Compare D6's MACRO run of the identical fixture: 4 tokens all parked, **6** commands,
4 InvokeActions, `len(Boundaries)=0`. In Micro only **1** InvokeAction is emitted.

**Three Micro-specific facts ADR-0158 must state (all measured):**
1. `snapshotIDs := s.tokenIDsAwaitingSignal(t.Name)` is taken over tokens that Micro may not
   have driven to their park yet — `catchC`'s token is `TokenActive` with `AwaitSignal=""`, so
   the snapshot is **empty** and tier 4 silently misses a catch the definition declares.
   The signal is consumed (`matched=true`, payload merged) and that catch is **not** re-armed
   for a later delivery — it is simply skipped, and the token later advances past `catchC`.
2. An arm created by the delivery's OWN drive (`bndB`) is not in any tier's view — it stays
   armed. This is the same "each tier does its own lookup" hazard the file's `⚠` comment at
   `step_triggers.go:792–803` warns about, one level up.
3. A tier's fire may leave its placed token `TokenActive` with **no command** — so
   `len(Commands)` is NOT a proxy for "how many arms fired" in Micro. Any fan-out acceptance
   test that counts commands must run in Macro, or count `History`/token placements instead.

⚠ Fact 1 is a **pre-existing defect independent of ADR-0158** (Micro + intermediate signal
catch loses a delivery). It is NOT introduced by fan-out, but fan-out will multiply the number
of tiers that can observe a half-driven state, so the ADR should either scope Micro out
explicitly or own it.

---

## Hygiene

Probes written and DELETED: `engine/zz_probe_a_d{1,5,6,7,8,9}_test.go`,
`processtest/zz_probe_a_d2_test.go`.
Files patched and RESTORED from snapshot (md5-verified each time):
- `engine/step_triggers.go` (snapshot `/tmp/snap_step_triggers.go`) — patched twice (D3, D7).
- `engine/step_arm_dispatch.go` (snapshot `/tmp/snap_step_arm_dispatch.go`) — patched once (D5).
- `engine/state_arms.go` (snapshot `/tmp/snap_state_arms.go`) — patched once (D4 deletion probe).

Final:
```
$ git status --short
(no output — clean)
$ git diff --stat
(no output — no tracked-file changes)
$ go test ./engine/... ./processtest/... > /tmp/final.log 2>&1; echo "EXIT=$?"
EXIT=0
ok  	github.com/kartaladev/wrkflw/engine	0.493s
ok  	github.com/kartaladev/wrkflw/processtest	0.927s
```

## Verdict summary

| claim | verdict |
|---|---|
| D1 headline defect reproduces | **CONFIRMED** |
| D2 processtest behaviour | **CONFIRMED** — first arm drives, then `ErrUnhandledPark`; chained handler gives a SILENT wrong answer |
| D3 `handleSignalReceived` structure | **CONFIRMED-BUT-MOVED** (ADR-0169 rewrote it; both re-checks EXIST and FIRE) |
| D4 wrapper call sites | **CONFIRMED** — exactly one each, all in `handleSignalReceived`; safe to delete |
| D5 timer/message stay first-match | **CONFIRMED** (cascade routing executed; TimerID unique per arm) |
| D6 cross-family broadcast baseline | **CONFIRMED** — all four fire in one StepResult, tier-ordered |
| D7 mergeVars/markMatched | **CONFIRMED** — 4 markMatched → 1 mergeVars; 0 → 0 |
| D8 error mid-dispatch | **CONFIRMED** (zero StepResult, clone holds) — but the brief's suggested error source (**missing target node**) is **REFUTED**: it parks, it does not error |
| D9 Micro mode | **CONFIRMED** shape; behaviour far more divergent than expected (3 new facts + 1 pre-existing defect) |

---

# Section B — arms mechanics and identity (agent B, claims M1–M10)

(Agent B's report, verbatim.)

Worktree: `/Users/zakyalvan/Documents/RND/wrkflw/.claude/worktrees/agent-a4fedaf89d26956bc` @ `571a380`.
Method: Premise Discipline — every claim about current behaviour EXECUTED, verbatim output pasted.
Docker NOT used. All probes container-free (`./engine/`).

---

## M1 — Pointer detachment. VERDICT: **CONFIRMED** (all three halves)

Current locations (re-derived, symbol names not line numbers):
`removeArmsWhere` — `engine/state_arms.go:262` (the `make([]T, 0, len(arms))` at `:263`).
Wrappers assigning over the field: `removeArmedEventsForGateway` (`:321`),
`removeBoundaryArmsForHost` (`:354`), `removeEventTriggeredSubprocessArmsForScope` (`:389`).
`armBySignal` — `engine/state_arms.go:225`.

Command: `go test -run '^TestProbeM1PointerDetachment$' -v ./engine/` → **EXIT=0**, `=== RUN` observed.

VERBATIM:
```
M1 before: len=2 oldBacking=0x6a86e20277c0 retired={HostToken:tokA HostNode:workA BoundaryNode:bndA Flow:fA NonInterrupting:false triggerMatch:{TimerID: Signal:escalate Message: MessageKey:} Action:} survivor={HostToken:tokB HostNode:workB BoundaryNode:bndB Flow:fB NonInterrupting:false triggerMatch:{TimerID: Signal:survivor Message: MessageKey:} Action:}
M1 after: len=1 cancelTimerIDs=[] newBacking=0x6a86e2027900 sameBackingArray=false
M1a retired pointee AFTER removal = {HostToken:tokA HostNode:workA BoundaryNode:bndA Flow:fA NonInterrupting:false triggerMatch:{TimerID: Signal:escalate Message: MessageKey:} Action:} ; names the retired arm (HostToken==tokA): true
M1b survivor pointer aliases new slice elem0: false (survivorPtr=0x6a86e2027858 newElem0=0x6a86e2027900)
M1c after write through stale survivor ptr: stalePointee.Signal="MUTATED_THROUGH_STALE_POINTER" liveState.Boundaries[0].Signal="survivor" writeVisible=false
```

Consequence: a `*boundaryArm` captured before a removal points into a DETACHED array;
the RETIRED arm is still fully intact there (stale read), AND a pointer to a
SURVIVING arm is likewise detached, so a WRITE through it is silently dropped
(`writeVisible=false`). Both halves argue for snapshotting identities as VALUES.

## M2 — Identity tuples. VERDICT: **CONFIRMED**

Command: `go test -run '^TestProbeM2IdentityTuples$' -v ./engine/` → **EXIT=0**

VERBATIM:
```
M2 armedEvent                   fields=[GatewayToken string(anon=false) CatchNode string(anon=false) Flow string(anon=false) triggerMatch engine.triggerMatch(anon=true)] hasNonInterrupting=false
M2 boundaryArm                  fields=[HostToken string(anon=false) HostNode string(anon=false) BoundaryNode string(anon=false) Flow string(anon=false) NonInterrupting bool(anon=false) triggerMatch engine.triggerMatch(anon=true) Action string(anon=false)] hasNonInterrupting=true
M2 eventTriggeredSubprocessArm  fields=[EnclosingScopeID string(anon=false) EventSubprocessNode string(anon=false) NonInterrupting bool(anon=false) triggerMatch engine.triggerMatch(anon=true)] hasNonInterrupting=true
M2 triggerMatch fields=[TimerID Signal Message MessageKey]
```

Compile-failure half (proving ABSENCE, not reading it from source):
probe `engine/zz_probe_b_compile_test.go` with `var ae armedEvent; fmt.Println(ae.NonInterrupting)`.
Command: `go vet ./engine/` → **EXIT=1**
```
# github.com/kartaladev/wrkflw/engine [github.com/kartaladev/wrkflw/engine.test]
engine/zz_probe_b_compile_test.go:7:17: ae.NonInterrupting undefined (type armedEvent has no field or method NonInterrupting)
```
After deleting the probe: `go vet ./engine/` → EXIT=0.

Consequence: `armedEvent` has NO `NonInterrupting`; `boundaryArm` and
`eventTriggeredSubprocessArm` each declare it. Identity tuples a value snapshot
can carry today: `armedEvent` = (GatewayToken, CatchNode, Flow, triggerMatch);
`boundaryArm` = (HostToken, HostNode, BoundaryNode, Flow, NonInterrupting,
triggerMatch, Action); `eventTriggeredSubprocessArm` = (EnclosingScopeID,
EventSubprocessNode, NonInterrupting, triggerMatch).

---

## M3 — `EnclosingScopeID == ""` is the VALID root identity. VERDICT: **CONFIRMED**

Command: `go test -run '^TestProbeM3RootScopeEspEmptyEnclosingScopeFires$' -v ./engine/` → **EXIT=0**, `=== RUN` observed.

VERBATIM:
```
M3 model.Validate = <nil>
M3 start err=<nil>
M3 after-start: status=running tokens=1 scopes=0 ArmedEvents=[] Boundaries=[] ESP=[{EnclosingScopeID: EventSubprocessNode:esp NonInterrupting:false triggerMatch:{TimerID: Signal:boom Message: MessageKey:}}]
M3 after-start:   token id=m3-i1-t1 node=host scope="" state=1 awaitCmd="m3-i1-h1" awaitSignal=""
M3   arm[0] EnclosingScopeID="" (isEmpty=true) node="esp" NonInterrupting=false Signal="boom"
M3 signal err=<nil> cmds=2
M3 after-signal: status=running tokens=1 scopes=1 ArmedEvents=[] Boundaries=[] ESP=[]
M3 after-signal:   token id=m3-i1-t2 node=esp-work scope="m3-i1-s1" state=1 awaitCmd="m3-i1-c1" awaitSignal=""
M3 after-signal:   cmd engine.UpdateTask {Task:{TaskID:m3-i1-h1 ... State:cancelled ...}}
M3 after-signal:   cmd engine.InvokeAction {CommandID:m3-i1-c1 Name:esp-action Scoped:<nil> Input:map[_idempotencyKey:m3-i1:esp-work] FireAndForget:false}
```

Consequence: a top-level event sub-process arms with `EnclosingScopeID == ""` and
FIRES — opens scope `m3-i1-s1`, places a token at `esp-work`, emits a live
`InvokeAction`. An ADR-0152-style "empty key matches no arm" guard applied to
`EnclosingScopeID` on a re-resolver would silently kill EVERY top-level event
sub-process. Note the asymmetry that already exists in the code and is
load-bearing: `removeArmedEventsForGateway` (`state_arms.go:321`) and
`removeBoundaryArmsForHost` (`:354`) DO early-return on an empty owner key, while
`removeEventTriggeredSubprocessArmsForScope` (`:389`) deliberately does NOT.

---

## M4 — `resolveGatewayWin` removes EVERY arm of the gateway. VERDICT: **CONFIRMED**

Current site: `s.removeArmedEventsForGateway(ae.GatewayToken)` inside
`resolveGatewayWin` (declared `engine/step_gateways.go:214`), at `:271`.

Fixture: ONE event gateway, TWO catch nodes BOTH on signal `sig`.
Command: `go test -run '^TestProbeM4TwoSameSignalArmsOneGateway$' -v ./engine/` → **EXIT=0**

VERBATIM:
```
M4 model.Validate = <nil>
M4 after-start: status=running tokens=1 scopes=0 ArmedEvents=[{GatewayToken:m4-i1-t1 CatchNode:catchA Flow:f2 triggerMatch:{TimerID: Signal:sig Message: MessageKey:}} {GatewayToken:m4-i1-t1 CatchNode:catchB Flow:f3 triggerMatch:{TimerID: Signal:sig Message: MessageKey:}}] Boundaries=[] ESP=[]
M4 after-start:   token id=m4-i1-t1 node=evtgw scope="" state=1 awaitCmd="evtgw:m4-i1-t1" awaitSignal=""
M4 signal err=<nil> cmds=1
M4 after-signal: status=running tokens=1 scopes=0 ArmedEvents=[] Boundaries=[] ESP=[]
M4 after-signal:   token id=m4-i1-t1 node=svcA scope="" state=1 awaitCmd="m4-i1-c1" awaitSignal=""
M4 after-signal:   cmd engine.InvokeAction {CommandID:m4-i1-c1 Name:actA Scoped:<nil> Input:map[_idempotencyKey:m4-i1:svcA] FireAndForget:false}
```

Consequence: first-event-wins is preserved. Two same-signal arms on ONE gateway
token can never both fire — `ArmedEvents` drops 2→0 in a single `Step` and only
`actA` is invoked. Tier-1 plurality is meaningful only ACROSS DISTINCT gateway
tokens. The instance stayed `running`, so the 2→0 is attributable to
`removeArmedEventsForGateway`, not to a terminal sweep.

---

## M5 — Gateway identity ABA. VERDICT: **CONFIRMED — both halves still hold at HEAD**

Command: `go test -run '^TestProbeM5GatewayIdentityABA$' -v ./engine/` → **EXIT=0**

VERBATIM:
```
M5a naive direct loop  -> model.Validate = workflow-definition: gateway both splits and joins: node "evtgw"
M5b merge-gateway loop -> model.Validate = <nil>
M5b start err=<nil>
M5b after-start: status=running tokens=1 scopes=0 ArmedEvents=[{GatewayToken:m5-i1-t1 CatchNode:catchA Flow:f3 triggerMatch:{TimerID: Signal:sig Message: MessageKey:}} {GatewayToken:m5-i1-t1 CatchNode:catchB Flow:f4 triggerMatch:{TimerID: Signal:other Message: MessageKey:}}] Boundaries=[] ESP=[]
M5b after-start:   token id=m5-i1-t1 node=evtgw scope="" state=1 awaitCmd="evtgw:m5-i1-t1" awaitSignal=""
M5b signal#1 err=<nil> cmds=0
M5b after-signal#1: status=running tokens=1 scopes=0 ArmedEvents=[{GatewayToken:m5-i1-t1 CatchNode:catchA Flow:f3 triggerMatch:{TimerID: Signal:sig Message: MessageKey:}} {GatewayToken:m5-i1-t1 CatchNode:catchB Flow:f4 triggerMatch:{TimerID: Signal:other Message: MessageKey:}}] Boundaries=[] ESP=[]
M5b after-signal#1:   token id=m5-i1-t1 node=evtgw scope="" state=1 awaitCmd="evtgw:m5-i1-t1" awaitSignal=""
M5b signal#2 err=<nil> cmds=0
M5b after-signal#2: status=running tokens=1 scopes=0 ArmedEvents=[{GatewayToken:m5-i1-t1 CatchNode:catchA Flow:f3 triggerMatch:{TimerID: Signal:sig Message: MessageKey:}} {GatewayToken:m5-i1-t1 CatchNode:catchB Flow:f4 triggerMatch:{TimerID: Signal:other Message: MessageKey:}}] Boundaries=[] ESP=[]
M5b after-signal#2:   token id=m5-i1-t1 node=evtgw scope="" state=1 awaitCmd="evtgw:m5-i1-t1" awaitSignal=""
```

Consequence: **the ABA IS STILL CONSTRUCTIBLE at HEAD.** ADR-0167 changed NEITHER
half: the naive `catchA -> evtgw` loop is STILL rejected by `model.Validate` with
the same message (`gateway both splits and joins: node "evtgw"`), and routing the
loop through an exclusive-gateway merge STILL validates. After the signal the
SAME token id (`m5-i1-t1`), the SAME `evtgw:` sentinel and byte-identical
`(GatewayToken, CatchNode, Flow, triggerMatch)` arms are back — re-armed
synchronously WITHIN one `Step` (the loop closes inside `drive`). An
"already-resolved gateway tokens" guard keyed on identity is therefore genuinely
load-bearing, and the ADR must cite the MERGE-GATEWAY shape: shown only the naive
loop, a reviewer would wrongly conclude the guard is dead code.

---

## M6 — Interrupting boundary consumes host + retires SIBLING arms. VERDICT: **CONFIRMED**

Path: `fireBoundaryArm` (`engine/step_boundaries.go:95`), interrupting branch
`:135-150`, `cancelTokenWaits(s, hostTok, at, CloseKindBoundaryInterrupted)` at
`:146` → `cancelTokenWaits` (`engine/step_cancel.go:16`) →
`s.removeBoundaryArmsForHost(tok.ID)` at `:23`.

Fixture guard: THREE arms on ONE host — interrupting signal `boom` (with a
boundary action), NON-interrupting signal `pulse`, and a TIMER arm.
⚠ **THE FIRST ATTEMPT WAS VACUOUS AND WAS FIXED.** Routing `bnd-int` to an end
event COMPLETED the instance (`status=completed`), so `endInstance`'s terminal
sweep also drains every arm and the observed 3→0 could NOT be attributed to
`cancelTokenWaits`. The fixture now routes the boundary to a parking `UserTask`
so the instance stays `running`.

Command: `go test -run '^TestProbeM6InterruptingBoundaryCancelsSiblings$' -v ./engine/` → **EXIT=0**

VERBATIM:
```
M6 model.Validate = <nil>
M6 after-start: status=running tokens=1 scopes=0 ArmedEvents=[] Boundaries=[{HostToken:m6-i1-t1 HostNode:host BoundaryNode:bnd-int Flow:f3 NonInterrupting:false triggerMatch:{TimerID: Signal:boom Message: MessageKey:} Action:after-int-action} {HostToken:m6-i1-t1 HostNode:host BoundaryNode:bnd-ni Flow:f4 NonInterrupting:true triggerMatch:{TimerID: Signal:pulse Message: MessageKey:} Action:} {HostToken:m6-i1-t1 HostNode:host BoundaryNode:bnd-timer Flow:f5 NonInterrupting:false triggerMatch:{TimerID:m6-i1-tm1 Signal: Message: MessageKey:} Action:}] ESP=[]
M6 after-start:   cmd engine.ScheduleTimer {TimerID:m6-i1-tm1 Token:m6-i1-t1 ... Kind:TimerIntermediate}
M6 signal(boom) err=<nil> cmds=4
M6 after-boom: status=running tokens=1 scopes=0 ArmedEvents=[] Boundaries=[] ESP=[]
M6 after-boom:   token id=m6-i1-t2 node=after-int scope="" state=1 awaitCmd="m6-i1-h2" awaitSignal=""
M6 after-boom:   cmd engine.InvokeAction {CommandID:m6-i1-c1 Name:after-int-action Scoped:<nil> Input:map[] FireAndForget:true}
M6 after-boom:   cmd engine.CancelTimer {TimerID:m6-i1-tm1}
M6 after-boom:   cmd engine.UpdateTask {Task:{TaskID:m6-i1-h1 ... State:cancelled ...}}
M6 after-boom:   cmd engine.AwaitHuman {TaskID:m6-i1-h2 ...}
M6 host token still present: false
M6 re-resolve boundaryArmBySignal(boom)=<nil> (pulse)=<nil> byTimer=<nil>
```

Consequence: the host token `m6-i1-t1` is GONE; ALL THREE arms are retired
(`Boundaries=[]`; all three re-resolvers return `<nil>`) while the instance is
still `running`. `CancelTimer{TimerID:m6-i1-tm1}` is emitted for the TIMER
sibling. A snapshot-then-fire-each design MUST re-resolve before each fire: a
snapshot taken before this fire still names `bnd-ni` and `bnd-timer`, neither of
which exists afterwards.

---

## M7 — Interrupting ESP retires the scope's arms AND every descendant's. VERDICT: **CONFIRMED** (call path RE-DERIVED)

CURRENT call path at HEAD (ADR-0162 moved it out of `step_eventsubprocess.go`):
```
engine/step_eventsubprocess.go:207   cmds = append(cmds, cancelScopeSubtree(s, ea.EnclosingScopeID, at, CloseKindBoundaryInterrupted)...)
engine/step_eventsubprocess.go:208   s.closeScopeDescendants(ea.EnclosingScopeID)
engine/step_cancel.go:81             func cancelScopeSubtree(s *InstanceState, scopeID string, at time.Time, kind CloseKind) []Command
engine/step_cancel.go:101              s.removeEventTriggeredSubprocessArmsForScope(scopeID)   // the NAMED scope
engine/step_cancel.go:113              s.removeEventTriggeredSubprocessArmsForScope(id)        // EVERY descendant
```
`removeEventTriggeredSubprocessArmsForScope` is NOT called from
`step_eventsubprocess.go` at all any more (its only mention there is a comment at
`:225`). `cancelScopeSubtree` has exactly two non-test call sites today:
`engine/step_eventsubprocess.go:207` and `engine/step_errors.go:468`.

Command: `go test -run '^TestProbeM7InterruptingEspRetiresSiblingAndDescendants$' -v ./engine/` → **EXIT=0**

VERBATIM:
```
M7 model.Validate = <nil>
M7 after-start: status=running tokens=1 scopes=1 ArmedEvents=[] Boundaries=[] ESP=[{EnclosingScopeID: EventSubprocessNode:espInt NonInterrupting:false triggerMatch:{TimerID: Signal:boom Message: MessageKey:}} {EnclosingScopeID: EventSubprocessNode:espNI NonInterrupting:true triggerMatch:{TimerID: Signal:pulse Message: MessageKey:}} {EnclosingScopeID:m7-i1-s1 EventSubprocessNode:iesp NonInterrupting:false triggerMatch:{TimerID: Signal:inner Message: MessageKey:}}]
M7 after-start:   token id=m7-i1-t2 node=n-work scope="m7-i1-s1" state=1 awaitCmd="m7-i1-h1" awaitSignal=""
M7   arm[0] scope="" node="espInt" NI=false signal="boom"
M7   arm[1] scope="" node="espNI" NI=true signal="pulse"
M7   arm[2] scope="m7-i1-s1" node="iesp" NI=false signal="inner"
M7 signal(boom) err=<nil> cmds=2
M7 after-boom: status=running tokens=1 scopes=1 ArmedEvents=[] Boundaries=[] ESP=[]
M7 after-boom:   token id=m7-i1-t3 node=espInt-work scope="m7-i1-s2" state=1 awaitCmd="m7-i1-c1" awaitSignal=""
M7 re-resolve espArmBySignal(pulse)=<nil> (inner)=<nil>
```

Consequence: after the INTERRUPTING root-scope fire the NON-interrupting SIBLING
arm (`espNI`/`pulse`) is GONE, and so is the DESCENDANT-scope arm
(`iesp`/`inner`, scope `m7-i1-s1`) — `ESP=[]`, both re-resolvers `<nil>`, with the
instance still `running` (so it is `cancelScopeSubtree`, not a terminal sweep).
A tier-3 snapshot taken before the fire names two arms that no longer exist.

---

## M9 — Non-interrupting arms STAY ARMED after firing. VERDICT: **CONFIRMED (both families)**

Command: `go test -run '^TestProbeM9' -v ./engine/` → **EXIT=0** (both `=== RUN` observed)

(a) NON-INTERRUPTING BOUNDARY — VERBATIM:
```
M9a model.Validate = <nil>
M9a start err=<nil> Boundaries=[{HostToken:m9a-i1-t1 HostNode:host BoundaryNode:bnd-ni Flow:f3 NonInterrupting:true triggerMatch:{TimerID: Signal:pulse Message: MessageKey:} Action:}]
M9a fire#1 err=<nil> Boundaries=[{HostToken:m9a-i1-t1 HostNode:host BoundaryNode:bnd-ni Flow:f3 NonInterrupting:true triggerMatch:{TimerID: Signal:pulse Message: MessageKey:} Action:}]
M9a fire#1 re-resolve boundaryArmBySignal(pulse)=&{HostToken:m9a-i1-t1 HostNode:host BoundaryNode:bnd-ni Flow:f3 NonInterrupting:true triggerMatch:{TimerID: Signal:pulse Message: MessageKey:} Action:} (nil? false)
M9a after-fire#1: status=running tokens=2 scopes=0 ... token m9a-i1-t1 node=host ; token m9a-i1-t2 node=ni-work
M9a fire#2 err=<nil> Boundaries=[{HostToken:m9a-i1-t1 ... Signal:pulse ...}]
M9a after-fire#2: status=running tokens=3 scopes=0 ... m9a-i1-t1 @host, m9a-i1-t2 @ni-work, m9a-i1-t3 @ni-work
```

(b) NON-INTERRUPTING EVENT SUB-PROCESS — VERBATIM:
```
M9b model.Validate = <nil>
M9b start err=<nil> ESP=[{EnclosingScopeID: EventSubprocessNode:espNI NonInterrupting:true triggerMatch:{TimerID: Signal:pulse Message: MessageKey:}}]
M9b fire#1 err=<nil> ESP=[{EnclosingScopeID: EventSubprocessNode:espNI NonInterrupting:true triggerMatch:{TimerID: Signal:pulse Message: MessageKey:}}]
M9b fire#1 re-resolve espArmBySignal(pulse)=&{EnclosingScopeID: EventSubprocessNode:espNI NonInterrupting:true triggerMatch:{TimerID: Signal:pulse Message: MessageKey:}} (nil? false)
M9b after-fire#1: status=running tokens=2 scopes=1 ... m9b-i1-t2 node=espNI-work scope="m9b-i1-s1"
M9b fire#2 err=<nil> ESP=[{EnclosingScopeID: EventSubprocessNode:espNI NonInterrupting:true triggerMatch:{TimerID: Signal:pulse Message: MessageKey:}}]
M9b after-fire#2: status=running tokens=3 scopes=2 ... m9b-i1-t3 node=espNI-work scope="m9b-i1-s2"
```

Consequence: **the fired arm is STILL RESOLVABLE BY THE SAME LOOKUP immediately
after it fires**, byte-identical, in BOTH families (ADR-0124 repeatability). A
tier loop written as "re-scan for the next match" would therefore find the SAME
arm forever — a non-terminating loop within one delivery. This is the strongest
constraint on ADR-0158's design: the loop MUST be driven by a pre-taken snapshot
of identities with a per-identity fire-once rule; re-resolution may be used ONLY
to check that a snapshotted identity still exists (M6/M7), never to select the
next arm.

---

## M8 — `model.Validate` and duplicate identities. VERDICT: **CONFIRMED — all three still ACCEPTED after ADR-0167**, and the collision is WORSE than previously recorded

ADR-0167 changed **decoding** (`ParseYAML` / `UnmarshalJSON` reject unknown
fields); it did not add structural uniqueness checks to `model.Validate`. Verified
by execution, not by reading the ADR:

Command: `go test -run '^TestProbeM8ValidateDuplicates$' -v ./engine/` → **EXIT=0**

VERBATIM:
```
M8a duplicate NODE IDS  -> model.Validate = <nil>
M8b two flows same pair -> model.Validate = <nil>
M8c duplicate FLOW IDS  -> model.Validate = <nil>
M8d duplicate BOUNDARY node ids -> model.Validate = <nil>
M8b start err=<nil> arms=2
M8b   arm[0] = {GatewayToken:m8b-i1-t1 CatchNode:catchA Flow:f2 triggerMatch:{TimerID: Signal:sig Message: MessageKey:}}
M8b   arm[1] = {GatewayToken:m8b-i1-t1 CatchNode:catchA Flow:f2dup triggerMatch:{TimerID: Signal:sig Message: MessageKey:}}
M8b   (GatewayToken,CatchNode) identical: true ; WHOLE arm identical: false (Flow "f2" vs "f2dup")
M8d start err=<nil> arms=2
M8d   arm[0] = {HostToken:m8d-i1-t1 HostNode:host BoundaryNode:bnd Flow:f3 NonInterrupting:true triggerMatch:{TimerID: Signal:boom Message: MessageKey:} Action:}
M8d   arm[1] = {HostToken:m8d-i1-t1 HostNode:host BoundaryNode:bnd Flow:f3 NonInterrupting:true triggerMatch:{TimerID: Signal:boom Message: MessageKey:} Action:}
M8d   WHOLE arm structs identical: true
M8d signal err=<nil> tokens=2 Boundaries=2
M8d after-signal: status=running tokens=2 scopes=0 ArmedEvents=[] Boundaries=[{HostToken:m8d-i1-t1 ... BoundaryNode:bnd Flow:f3 NonInterrupting:true ... Signal:boom ...} {HostToken:m8d-i1-t1 ... BoundaryNode:bnd Flow:f3 NonInterrupting:true ... Signal:boom ...}] ESP=[]
M8d after-signal:   token id=m8d-i1-t1 node=host scope="" state=1 awaitCmd="m8d-i1-h1" awaitSignal=""
M8d after-signal:   token id=m8d-i1-t2 node=after scope="" state=1 awaitCmd="m8d-i1-h2" awaitSignal=""
```

Consequences (three, and the third is NEW relative to the parked draft's C20):
1. **(a) duplicate node ids, (b) two flows between the same pair, and (c)
   duplicate flow ids are ALL still accepted** by `model.Validate` at HEAD.
2. **M8b**: a gateway can carry two arms whose `(GatewayToken, CatchNode)` is
   identical and which differ ONLY in `Flow`. De-duplicating on
   `(GatewayToken, CatchNode)` silently DISCARDS the loser's distinct `Flow` —
   the value `resolveGatewayWin`'s fallback branch (`moveAlongSingleFlow`) reads.
3. 🚨 **M8d is the case a value-identity design cannot survive**: duplicate
   BOUNDARY node ids produce two `boundaryArm` structs that are **byte-identical
   in every field** (`WHOLE arm structs identical: true`). No identity tuple
   drawn from the struct's fields can distinguish them. Today only ONE fires
   (tier-2 first-match: `tokens=2` = host + a single spawn). Under
   fire-each-per-family, the SAME author mistake would spawn TWO tokens per
   delivery, forever, because both arms are non-interrupting and stay armed
   (M9). ADR-0158 must therefore either (i) de-duplicate the snapshot by value,
   accepting that a legitimately-duplicated node fires once, or (ii) key the
   snapshot on SLICE INDEX / a synthesised per-arm id rather than field values —
   and if it does (ii), M1's detachment result means the index must be
   re-validated against the live slice before each fire, since removals
   renumber it.

---

## M10 — `TestNonInterruptingBoundarySignalNoSelfCascade`. VERDICT: **CONFIRMED — still there, NOT vacuous, CAN FAIL in both dimensions**

LOCATION (re-derived; it did NOT move): test `engine/step_events_test.go:1157`;
fixture `nonInterruptingBoundarySignalSelfCascadeDef` at
`engine/step_events_test.go:1130-1149`. File package is `engine_test`
(black-box).

FIXTURE AUDIT (the fixture, not the assertion text). The definition genuinely
declares the construct under test:
```
event.NewBoundary("bnd-pulse", "work", event.WithSignalName("pulse"), event.WithBoundaryNonInterrupting())   // :1136
event.NewIntermediateCatch("inner-catch", event.WithSignalName("pulse"))                                     // :1138
{ID: "f-bnd-catch", Source: "bnd-pulse", Target: "inner-catch"}                                              // :1146
```
i.e. a NON-interrupting signal boundary whose outgoing path leads to a catch on
the SAME signal name — the self-cascade is constructible, so this is not an
`assert.Empty(state.Boundaries)`-with-no-boundary-node vacuity. The test also
carries a fixture guard of its own (`require.Len(t, r1.State.Boundaries, 1,
"signal boundary arm must be recorded")`).

BASELINE
Command: `go test -run '^TestNonInterruptingBoundarySignalNoSelfCascade$' -v ./engine/` → **EXIT=0**
```
=== RUN   TestNonInterruptingBoundarySignalNoSelfCascade
--- PASS: TestNonInterruptingBoundarySignalNoSelfCascade (0.00s)
```
(`=== RUN` observed — it RAN; a `-run` filter on a nonexistent name would have
exited 0 with no `=== RUN` line.)

MUTATION 1 — drop the tier-4 token snapshot, re-scan instead
(`engine/step_triggers.go`, `for _, tokenID := range snapshotIDs` →
`_ = snapshotIDs` + `for _, tokenID := range s.tokenIDsAwaitingSignal(t.Name)`).
COMPILED. → **MUT1_EXIT=1**
```
=== RUN   TestNonInterruptingBoundarySignalNoSelfCascade
    step_events_test.go:1180:
        	Error:      	"[{i1-t1 work  1 i1-h1    map[] 2026-06-21 10:00:00 +0000 UTC 0 0001-01-01 00:00:00 +0000 UTC}]" should have 2 item(s), but has 1
        	Messages:   	host + inner-catch token must exist
--- FAIL: TestNonInterruptingBoundarySignalNoSelfCascade (0.00s)
```
(The re-scan consumes the freshly spawned `inner-catch` token in the same
delivery, so it advances to `end2` and only the host remains.)

MUTATION 2 — make the non-interrupting boundary fire twice (duplicate
`s.placeTokenInScope(flowTarget, hostScopeID, at)` in `fireBoundaryArm`'s
non-interrupting branch, `engine/step_boundaries.go:162`). COMPILED. → **MUT2_EXIT=1**
```
=== RUN   TestNonInterruptingBoundarySignalNoSelfCascade
    step_events_test.go:1180:
        	Error:      	"[{i1-t1 work ...} {i1-t2 inner-catch  1  pulse ...} {i1-t3 inner-catch  1  pulse ...}]" should have 2 item(s), but has 3
        	Messages:   	host + inner-catch token must exist
--- FAIL: TestNonInterruptingBoundarySignalNoSelfCascade (0.00s)
```

Both mutations COMPILED and both DISCRIMINATED, in the two DIFFERENT dimensions
ADR-0158 disturbs: the tier-4 snapshot AND one-fire-per-arm-identity. Both files
restored from snapshots; `git diff --stat -- engine/step_triggers.go
engine/step_boundaries.go` is EMPTY after each restore.

Consequence: ADR-0158 may keep citing this test as a real guard. It is the one
existing test that would go RED if the fan-out re-fires a non-interrupting
boundary arm within a single delivery — but note it constrains only the BOUNDARY
family; nothing equivalent exists for the gateway or ESP families.

---

## Cross-claim summary

| # | Claim | Verdict |
|---|---|---|
| M1 | `removeArmsWhere` detaches the backing array; stale pointers keep the retired arm intact AND drop writes to survivors | **CONFIRMED** (both halves) |
| M2 | Identity field sets; `armedEvent` has no `NonInterrupting`, the other two do | **CONFIRMED** (compile-verified absence) |
| M3 | `EnclosingScopeID == ""` is the VALID root identity and FIRES | **CONFIRMED** |
| M4 | `resolveGatewayWin` removes EVERY arm of the resolved gateway | **CONFIRMED** |
| M5 | Gateway ABA — naive loop Validate-rejected, merge loop re-arms byte-identically in ONE Step | **CONFIRMED, both halves unchanged by ADR-0167** |
| M6 | Interrupting boundary consumes host + retires all sibling arms, CancelTimer for the timer sibling | **CONFIRMED** (first fixture was vacuous; fixed) |
| M7 | Interrupting ESP retires the scope's AND every descendant scope's arms | **CONFIRMED**; call path re-derived to `step_cancel.go:101/:113` via `cancelScopeSubtree` |
| M8 | `model.Validate` accepts dup node ids / two flows per pair / dup flow ids | **CONFIRMED — all three still accepted**; NEW: byte-identical arm structs are constructible |
| M9 | Non-interrupting arms stay armed after firing (boundary AND ESP) | **CONFIRMED** (both families, re-resolvable immediately) |
| M10 | `TestNonInterruptingBoundarySignalNoSelfCascade` exists, is non-vacuous, CAN fail | **CONFIRMED** (2 compiling mutations, 2 REDs) |

### Ranked, most consequential for ADR-0158

1. **M9 + M6/M7 together are a design constraint, not a detail.** The fired
   non-interrupting arm is immediately re-resolvable by the same lookup, while
   an interrupting fire silently deletes SIBLING and DESCENDANT identities.
   So the loop must be snapshot-driven with fire-once-per-identity, and
   re-resolution used only as an existence check.
2. **M8d — byte-identical arms are constructible and `model.Validate` allows
   them.** A value-identity snapshot cannot distinguish them; the ADR must
   state whether it de-duplicates (changing behaviour for legitimately
   duplicated nodes) or indexes (which M1 shows must be re-validated).
3. **M3 — an ADR-0152-style empty-key guard on `EnclosingScopeID` would kill
   every top-level event sub-process.** The existing asymmetry in
   `state_arms.go` (gateway/host wrappers guard on ""; the scope wrapper does
   not) is deliberate and must be preserved.
4. **M1 — the pointer hazard drops WRITES to survivors**, not only reads of
   retired arms.
5. **M5 — the ABA needs the merge-gateway shape to be exhibited**; the naive
   loop is still Validate-rejected and would make the guard look like dead code.
6. **M4** bounds tier-1 plurality to distinct gateway tokens.
7. **M10** is a real guard, but only for the BOUNDARY family.

### Stale-citation deltas vs the parked draft / prior evidence doc
- `resolveGatewayWin` remove-all is at `engine/step_gateways.go:271` (decl `:214`).
- ESP root status check is `engine/step_eventsubprocess.go:159-170`.
- Interrupting-ESP arm retirement is `engine/step_cancel.go:101` and `:113`
  (via `cancelScopeSubtree`, decl `step_cancel.go:81`), called from
  `engine/step_eventsubprocess.go:207`.
- `handleSignalReceived` is now `engine/step_triggers.go:741`; its tier closures
  are `:804-832`, the tier loop `:843-852`, the tier-4 snapshot loop `:861`.
- `removeArmsWhere` `state_arms.go:262`; `armBySignal` `:225`.
- `TestNonInterruptingBoundarySignalNoSelfCascade` `engine/step_events_test.go:1157`
  — UNCHANGED.

### Not claimed
Nothing in M1–M10 was left unexecuted; there are no `ASSUMPTION (unverified)`
items in this half.

---

# Section CTL — controller-executed premises

# Controller-executed premise — arms created DURING a delivery are fired by a later tier

- Date: 2026-08-10
- Base: `main` @ `571a380`, branch `feat/signal-fanout-and-esp-status`
- Probe: `engine/zz_probe_ctl_test.go` (deleted after the run; body reproduced below)

## Claim under test

Today tiers 1–3 each perform their OWN lookup at the moment they run (ADR-0169
deliberately forbids hoisting them). A consequence nobody has recorded: an arm
**armed by an earlier tier's own drive**, which therefore did NOT exist at the
delivery instant, is visible to a later tier's lookup and fires in the SAME
delivery.

ADR-0158's identity **snapshot** would exclude it — the same device tier 4
already applies to tokens ("a token spawned by a non-interrupting arm during this
Step is NOT in the snapshot"). So the snapshot is not only a termination device
(the re-scan-finds-the-same-arm-forever argument); it also closes a
single-instant-semantics defect. That has to be stated, because it is a
**behaviour change beyond the headline fan-out** and needs its own test row.

## Fixture

```
start → evtgw(event-based) ⇒
    catchX (signal "x") → taskH(user) [bndH interrupting, signal "x"] → endH
    catchY (signal "y") → endY
bndH → endB
```

At delivery time: `ArmedEvents` = 2 (catchX/"x", catchY/"y"), `Boundaries` = **0**.
`bndH` cannot be armed until `taskH` is reached, and `taskH` is reached only by
tier 1's own fire.

## Command

```
go test -run '^TestProbeCtlArmCreatedDuringDelivery$' -v ./engine/ > ctl.log 2>&1; echo "EXIT=$?"
```

## OBSERVED (verbatim)

```
EXIT=0
=== RUN   TestProbeCtlArmCreatedDuringDelivery
CTL after start: status=running tokens=1 armedEvents=2 boundaries=0
CTL   token id=i1-t1 node=evtgw awaitSignal="" awaitCmd="evtgw:i1-t1"
2026/08/10 11:50:13 WARN dropping command whose awaiter this step cancelled instance_id=i1 command_kind=AwaitHuman correlation_id=i1-h1
CTL after signal x: status=completed tokens=0 armedEvents=0 boundaries=0 cmds=2
CTL   cmd engine.UpdateTask {Task:{TaskID:i1-h1 InstanceID:i1 NodeID:taskH ... State:cancelled ... CreatedAt:2026-08-10 10:00:01 +0000 UTC ...}}
CTL   cmd engine.CompleteInstance {Result:map[]}
CTL   visit node=start
CTL   visit node=evtgw
CTL   visit node=taskH
CTL   visit node=endB
--- PASS: TestProbeCtlArmCreatedDuringDelivery (0.00s)
PASS
ok  	github.com/kartaladev/wrkflw/engine	0.498s
```

## Verdict — CONFIRMED, and it is a defect in its own right

One `SignalReceived{"x"}` delivery, on unpatched `main`:

1. Tier 1 resolves `evtgw` → routes to `catchX`'s target `taskH` → `drive` parks
   there, mints human task `i1-h1`, and **arms `bndH` on `"x"`**.
2. Tier 2's lookup — which runs *after* tier 1's fire — finds `bndH` and fires
   it. `bndH` was not armed at the delivery instant.
3. `taskH` is consumed (`UpdateTask{State:cancelled}`), the boundary path reaches
   `endB`, and the instance completes.
4. The `AwaitHuman` for `i1-h1` is dropped by ADR-0161's stale-command filter —
   the `WARN dropping command whose awaiter this step cancelled` line above — so
   a human task is **minted and cancelled inside one step** and never becomes
   actionable.

**Discrimination check (the probe is not vacuous).** The history
`start → evtgw → taskH → endB` proves the boundary path ran: `endB` is reachable
ONLY from `bndH`. `taskH`'s normal outgoing flow targets `endH`, which never
appears. And `UpdateTask{cancelled}` is emitted only on an interrupting
cancellation, not on a normal completion.

## Consequences for the design

- The snapshot is **load-bearing for correctness**, not only for loop
  termination. The ADR must say so; the draft argued only termination.
- This is a **behaviour change the draft ADR never mentions** and must be a named
  test row: with the fan-out, `bndH` is absent from the boundary snapshot, so it
  does not fire, `taskH` stays parked as an actionable human task, and the
  instance stays `running`.
- ⚠ It also means the fan-out delivery is **not** a pure superset of today's
  behaviour. Some deliveries will fire FEWER arms than they do now. "0158 only
  makes more arms fire" would be a false recap sentence.

---

# The tier 2 → tier 3 variant — ALSO EXECUTED (was an assumption; now measured)

Probe `engine/zz_probe_ctl2_test.go` (deleted after the run).

## Fixture

```
start → host(service) → end
  bnd-x (interrupting, signal "x") → sub[ nested-start → nested-work → nested-end ]
                                     sub CONTAINS esp (interrupting, signal "x")
sub → end-diverted
```

At delivery time: `Boundaries` = 1 (`bnd-x`), **`EventTriggeredSubprocesses` = 0,
`Scopes` = 0**. The ESP arm cannot exist until `sub` is entered, and `sub` is
entered only by tier 2's own fire.

## Command

```
go test -run '^TestProbeCtl2ESPArmedDuringDelivery$' -v ./engine/ > ctl2.log 2>&1; echo "EXIT=$?"
```

## OBSERVED (verbatim)

```
EXIT=0
=== RUN   TestProbeCtl2ESPArmedDuringDelivery
CTL2 after start: status=running tokens=1 boundaries=1 esp=0 scopes=0
CTL2   token id=i1-t1 node=host scope=""
2026/08/10 11:52:14 WARN dropping command whose awaiter this step cancelled instance_id=i1 command_kind=InvokeAction correlation_id=i1-c2
CTL2 after signal x: status=running tokens=1 boundaries=0 esp=0 scopes=2 cmds=1
CTL2   token id=i1-t4 node=esp-work scope="i1-s2" state=1
CTL2   scope id=i1-s1 node=sub parent=""
CTL2   scope id=i1-s2 node=esp parent="i1-s1"
CTL2   cmd engine.InvokeAction {CommandID:i1-c3 Name:esp-action ... FireAndForget:false}
CTL2   visit node=start
CTL2   visit node=host
CTL2   visit node=sub
CTL2   visit node=nested-start
CTL2   visit node=nested-work
CTL2   visit node=esp-start
CTL2   visit node=esp-work
--- PASS: TestProbeCtl2ESPArmedDuringDelivery (0.00s)
PASS
```

## Verdict — CONFIRMED, and STRONGER than the tier 1 → tier 2 case

1. Tier 2 fires `bnd-x`, interrupts `host`, routes into `sub`. `drive` opens
   scope `i1-s1`, parks at `nested-work`, emits
   `InvokeAction{i1-c2 nested-action}`, and **arms `esp` on `"x"` for `i1-s1`**.
2. Tier 3's lookup finds that brand-new arm and fires it. It is **interrupting**,
   so `cancelScopeSubtree(i1-s1)` cancels the `nested-work` token tier 2 had
   created moments earlier.
3. `nested-action`'s `InvokeAction{i1-c2}` is dropped by ADR-0161's filter (the
   `WARN` above) — but **`nested-work` remains in `History` as a visited node**
   although its action never ran. The audit trail records work that did not
   happen.

**Discrimination check.** `esp=0` and `scopes=0` after start prove the arm did
not exist at the delivery instant; `scopes=2` with `i1-s2 parent=i1-s1` after
delivery proves both the sub-process scope and the ESP child scope were created
inside this one `Step`.

## Combined consequence for the design

Both intra-delivery routes are now executed, not argued:

| route | arm created by | fired by | effect today |
|---|---|---|---|
| tier 1 → tier 2 | gateway fire's drive parks at a user task | boundary tier | human task minted **and cancelled** in one step |
| tier 2 → tier 3 | boundary fire's drive enters a sub-process | event-sub tier | sub-process entered **and torn down** in one step; `History` keeps a visit for an action that never ran |

The ADR must state the snapshot as closing **both**, and must NOT generalise to
"tier N always feeds tier N+1" beyond these two measured routes.

---

# Ordering within a family — MEASURED, and it differs BY FAMILY

Probe `engine/zz_probe_ctl3_test.go` (`package engine`, deleted after the run).
Two ROOT-scope event sub-processes on signal `"x"` — `espNI` (non-interrupting)
and `espINT` (interrupting) — plus a parked `host` UserTask so the instance stays
`running` and the terminal sweep cannot be mistaken for the teardown under test.
The arms are fired directly via `fireEventTriggeredSubprocessArm`, with the
interrupting arm re-resolved by identity exactly as the fan-out loop would.

## OBSERVED (verbatim)

```
CTL3 A before: status=running tokens=1 scopes=0 esp=2
CTL3   token id=a1-t1 node=host scope="" state=1
CTL3 A after NI fire: status=running tokens=2 scopes=1 esp=2
CTL3   token id=a1-t1 node=host scope="" state=1
CTL3   token id=a1-t2 node=ni-work scope="a1-s1" state=1
CTL3   scope id=a1-s1 node=espNI parent=""
CTL3   cmd engine.InvokeAction {CommandID:a1-c1 Name:ni-action ... FireAndForget:false}
CTL3 A interrupting arm still resolvable: true
CTL3 A after INT fire: status=running tokens=1 scopes=1 esp=0
CTL3   token id=a1-t3 node=int-work scope="a1-s2" state=1
CTL3   scope id=a1-s2 node=espINT parent=""
CTL3   cmd engine.UpdateTask {... NodeID:host ... State:cancelled ...}
CTL3   cmd engine.InvokeAction {CommandID:a1-c2 Name:int-action ...}

CTL3 B after INT fire: status=running tokens=1 scopes=1 esp=0
CTL3   token id=b1-t2 node=int-work scope="b1-s1" state=1
CTL3   scope id=b1-s1 node=espINT parent=""
CTL3   cmd engine.UpdateTask {... NodeID:host ... State:cancelled ...}
CTL3   cmd engine.InvokeAction {CommandID:b1-c1 Name:int-action ...}
CTL3 B non-interrupting arm still resolvable: false
```

## Verdict

**Order A — non-interrupting FIRST.** The NI arm fires: opens scope `a1-s1`,
places `ni-work`, emits `InvokeAction{a1-c1 ni-action}`, and **stays armed**
(`esp=2`, confirming M9). The interrupting arm is then still resolvable, fires,
and `cancelScopeSubtree("")` destroys the ROOT subtree — **including the child
scope the NI fire had just opened**. Final state keeps only `a1-s2`; `a1-s1` and
`ni-work` are gone, so `a1-c1` has no surviving awaiter and is dropped by
ADR-0161's filter. Net: the NI arm did work, emitted a command, recorded a
`ni-work` visit, and was annihilated **inside the same delivery**.

**Order B — interrupting FIRST.** The interrupting arm fires once; the NI arm
then **re-resolves to nil** (`still resolvable: false`) and is skipped cleanly.
One scope, one command, no phantom visit.

## This CONTRADICTS the recommendation the controller gave the owner

The "non-interrupting first" recommendation was argued from the BOUNDARY family,
where it is correct: an interrupting boundary consumes only its HOST token and
that host's arms, so a token spawned by a non-interrupting sibling **survives**
and both arms genuinely take effect (2 tokens).

The ESP family has a different blast radius: an interrupting ESP fire calls
`cancelScopeSubtree(EnclosingScopeID)`, which destroys **every** token and scope
beneath the enclosing scope — necessarily including anything a non-interrupting
arm of that SAME scope created moments earlier. So for two ESP arms sharing an
enclosing scope it is **impossible for both to take effect**, whatever the order.
Non-interrupting-first only decides whether wasted work, a dropped command and a
misleading `History` visit are manufactured on the way.

⚠ The conflict is confined to arms sharing an enclosing scope. Two ESP arms in
SIBLING scopes do not interact — the interrupting one tears down only its own
subtree. **Not executed:** the sibling-scope case. Mark it
`ASSUMPTION (unverified)` until run.

## Consequence for the design

The ordering rule cannot be stated once for all three families without being
wrong for one of them. The ADR must either adopt per-family ordering and say why,
or adopt one rule and record the family it is wrong for, with this measurement.

---

# Sibling-scope ESP arms do NOT interact — EXECUTED (was the ordering rule's bound)

Probe `engine/zz_probe_ctl4_test.go` (`package engine`, deleted after the run).
Root parallel fork into two regular sub-processes; `sub1` contains `espA`
(NON-interrupting, signal `"x"`), `sub2` contains `espB` (interrupting, `"x"`).
Fired NON-interrupting first — the order that maximises the chance of cross-scope
damage. A `require.NotEqual` on the two `EnclosingScopeID`s is a fixture control:
without it the probe could silently degrade into the same-scope case it exists to
distinguish.

## OBSERVED (verbatim, abridged to the load-bearing lines)

```
CTL4 after start: status=running tokens=2 scopes=2 esp=2
CTL4   scope id=i1-s1 node=sub1 parent=""
CTL4   scope id=i1-s2 node=sub2 parent=""
CTL4   arm scope="i1-s1" node=espA nonInterrupting=true  signal=x
CTL4   arm scope="i1-s2" node=espB nonInterrupting=false signal=x

CTL4 after espA (non-interrupting, sub1): status=running tokens=3 scopes=3 esp=2
CTL4   token id=i1-t6 node=espA-work scope="i1-s3" state=1
CTL4   scope id=i1-s3 node=espA parent="i1-s1"
CTL4   cmd engine.InvokeAction {CommandID:i1-c1 Name:espA-action ...}

CTL4 after espB (interrupting, sub2): status=running tokens=3 scopes=4 esp=1
CTL4   token id=i1-t4 node=s1-work    scope="i1-s1" state=1
CTL4   token id=i1-t6 node=espA-work  scope="i1-s3" state=1
CTL4   token id=i1-t7 node=espB-work  scope="i1-s4" state=1
CTL4   arm scope="i1-s1" node=espA nonInterrupting=true signal=x
CTL4   cmd engine.UpdateTask {... NodeID:s2-work ... State:cancelled ...}
CTL4   cmd engine.InvokeAction {CommandID:i1-c2 Name:espB-action ...}
CTL4 VERDICT espA-work token survived espB's teardown: true
```

## Verdict — CONFIRMED. The ordering conflict IS confined to a shared enclosing scope.

`espB`'s interrupting fire calls `cancelScopeSubtree(i1-s2)` and touches only that
subtree: `s2-work` (`i1-t5`) is cancelled and `espB`'s own arm is retired
(`esp` 2 → 1). `espA`'s child scope `i1-s3` and its token `i1-t6` — created by the
non-interrupting fire one statement earlier, in the SIBLING subtree `i1-s1` — are
**untouched**, and `espA`'s arm stays armed (M9 repeatability).

Contrast with the same-scope case measured above, where the identical
non-interrupting-first order destroyed the work it had just created. **The
difference is scope relationship, not ordering.** So ADR-0158's per-family
ordering rule can state its bound as measured fact:

> Non-interrupting-first is wrong for the event-sub-process family **only when
> two arms share an enclosing scope**. Arms in sibling scopes are independent in
> either order.

## Residual

- **Not executed:** the ANCESTOR/DESCENDANT case — a non-interrupting arm in a
  scope nested INSIDE the interrupting arm's enclosing scope. `cancelScopeSubtree`
  descends, so it should behave like the same-scope case; that is an inference,
  not a measurement. Mark `ASSUMPTION (unverified)` unless executed.

---

# Section C — the event-sub-process status guard (agent C, claims E1–E8)

(Agent C's report, verbatim.)

Worktree: `/Users/zakyalvan/Documents/RND/wrkflw/.claude/worktrees/agent-a322bc797637c761a`
Base: `571a380` (main), clean at start and at end.
Date: 2026-08-10. Every verdict is by EXECUTION unless labelled `ASSUMPTION (unverified)`.
Probe files (all deleted at the end): `engine/zz_probe_c_{e1,e2,e3,e6,e7}_test.go`, all `package engine`.

---

## HEADLINE

1. **Direction (a) is CONFIRMED at the function level but REFUTED end-to-end for a
   TERMINAL instance.** `fireEventTriggeredSubprocessArm` called directly does fire a
   non-root arm on `completed`/`failed`/`terminated`; but no public `Step` route reaches
   it — ADR-0165's dispatch guard drops the trigger first, and ADR-0164's `endInstance`
   has already drained the arms.
2. **The reachable defect is `StatusCompensating`, not terminal.** A `beginCompensation`
   rollback (cancel / unhandled error / admin full rollback) leaves **every ESP arm
   armed** and the status non-terminal, so a non-root arm **fires into a rollback that
   will terminate the instance**: measured a live `InvokeAction`, a new scope, a new
   token — and after the walk finished, a `terminated` instance carrying **1 orphan
   token** whose `ActionCompleted` is then dropped.
3. **Direction (b) reproduces exactly as inherited.** A LOCAL throw walk silences the
   root arm; the delivery is still *consumed* (payload merged) so the signal is lost.
4. **Shape (A) — "add the same status check to the non-root branch" — is REFUTED by
   execution.** It breaks a real shipped behaviour
   (`TestCompensationWalkSurvivesNestedEventSubprocessTearingDownItsResumeScope`).
5. **The `!= StatusRunning` root predicate is COMPLETELY UNCOVERED.** Deleting it
   outright leaves `./engine/...` and every container-free package at EXIT=0.
6. **`s.Compensating.walkMode()` is the state field that distinguishes (ii) from (iii)** —
   measured `walkAdmin` for the three terminating rollbacks vs
   `walkThrowScopeWide`/`walkReverse`/`walkPartial` for the three resuming walks.
   A candidate predicate built on it goes RED→GREEN on the reproduction with the whole
   suite green.
7. **Claim (d) is CONFIRMED FALSE** — ADR-0124 line 62–63 still carries the parenthetical.
8. **Claim (c) is STALE** — the root-site arm retirement it calls an "accepted cost" was
   already REMOVED in the shipped ADR-0171 bundle, and is now pinned by a
   mutation-verified test.

---

## E1 — Direction (a): does a non-root arm fire into a terminal instance?

### E1a. Direct call (white box) — CONFIRMED

`engine/zz_probe_c_e1_test.go` calls `fireEventTriggeredSubprocessArm` directly with a
non-root arm whose enclosing scope `sc1` is alive, for five statuses, and the ROOT arm in
the same state.

```
go test -v -run '^TestProbeE1NonRootArmOnTerminalInstance$' ./engine/   → EXIT=0
```

```
NON-ROOT status=completed → err=<nil> status=completed tokens=1 scopes=2 espArms=0 cmds=1
    cmd engine.InvokeAction {CommandID:probe-e1-c1 Name:nested-esp-action ... }
    token id=probe-e1-t1 node=nesp-work scope="probe-e1-s1" state=1 await="probe-e1-c1"
    scope id=sc1 node=sub parent=""
    scope id=probe-e1-s1 node=nested-esp parent="sc1"
ROOT     status=completed → err=<nil> status=completed tokens=0 scopes=0 espArms=1 cmds=0

NON-ROOT status=failed     → ... tokens=1 scopes=2 espArms=0 cmds=1 (InvokeAction nested-esp-action)
ROOT     status=failed     → tokens=0 scopes=0 espArms=1 cmds=0
NON-ROOT status=terminated → ... tokens=1 scopes=2 espArms=0 cmds=1 (InvokeAction nested-esp-action)
ROOT     status=terminated → tokens=0 scopes=0 espArms=1 cmds=0
NON-ROOT status=running    → ... tokens=1 scopes=2 espArms=0 cmds=1
ROOT     status=running    → tokens=1 scopes=1 espArms=0 cmds=1 (InvokeAction root-esp-action)
NON-ROOT status=compensating → ... tokens=1 scopes=2 espArms=0 cmds=1
ROOT     status=compensating → tokens=0 scopes=0 espArms=1 cmds=0
```

**VERDICT E1a: CONFIRMED.** The non-root branch opens a child scope, places a token and
emits a live `InvokeAction` on all three terminal statuses. The root branch no-ops on
every status other than `running` — including `compensating`.

### E1b. End-to-end through the public `Step` API — REFUTED (for terminal)

`engine/zz_probe_c_e2_test.go`:

```
go test -v -run '^TestProbeE1EndToEndTerminal$' ./engine/   → EXIT=0
```

```
A before: cancel-no-records status=running tokens=1 scopes=1 ArmedEvents=1 Boundaries=1 EventTriggeredSubprocesses=2
A after : cancel-no-records status=terminated tokens=0 scopes=1 ArmedEvents=0 Boundaries=0 EventTriggeredSubprocesses=0 cmds=[{cancelled}]

WARN trigger rejected on terminal instance instance_id=e2b trigger=engine.SignalReceived status=completed outcome=dropped signal_name=boom-nested
B terminal+arm SignalReceived: err=<nil> status=completed ... EventTriggeredSubprocesses=2 cmds=[]

WARN trigger rejected on terminal instance instance_id=e2c trigger=engine.TimerFired status=failed outcome=dropped timer_id=tm-1
C terminal+timer-arm TimerFired: err=<nil> status=failed ... EventTriggeredSubprocesses=2 cmds=[]
```

Two independent defences, both measured:

* **The engine's terminal transitions drain the arms.** (A) `endInstance` →
  `cancelAllScheduledWork` took `EventTriggeredSubprocesses` **2 → 0** (ADR-0164).
* **The dispatch guard drops the trigger.** (B)/(C) hand-forge the shape the sweep cannot
  produce — terminal + 2 live arms + live scope, i.e. a row persisted before ADR-0164 —
  and `SignalReceived` / `TimerFired` are refused at `dispatch` (ADR-0165) with a WARN.
  `MessageReceived`, `SignalReceived` and `TimerFired` all carry `rejectSilently`
  (`engine/trigger.go:455,487,518`), so **all three ESP delivery routes are closed**.

**VERDICT E1b: REFUTED end-to-end.** There is no public-API route by which an ESP arm
fires on a TERMINAL instance today. Direction (a) as inherited ("fires into a
completed/terminal instance") is reachable **only** by calling the unexported function.
Note the residue: (A) leaves `scopes=1` — a zombie scope on a terminated instance.

### E1c. The route that IS reachable: `StatusCompensating` — CONFIRMED, and worse

```
go test -v -run '^TestProbeE1EndToEndCompensating$' ./engine/   → EXIT=0
```

```
rollback in flight: status=compensating tokens=0 scopes=1 ArmedEvents=0 Boundaries=0 EventTriggeredSubprocesses=2 cursorActive="e2d-c1"

NON-ROOT arm during rollback: status=compensating tokens=1 scopes=2 EventTriggeredSubprocesses=1 cursorActive="e2d-c1"
  cmds=[{e2d-c2 nested-esp-action <nil> map[_idempotencyKey:e2d:nesp-work] false}]
  tokens=[{ID:e2d-t1 NodeID:nesp-work ScopeID:e2d-s1 State:1 AwaitCommand:e2d-c2 ...}]
  scopes=[{ID:sc1 NodeID:sub ParentID:} {ID:e2d-s1 NodeID:nested-esp ParentID:sc1}]

ROOT arm during rollback: status=compensating tokens=0 scopes=1 EventTriggeredSubprocesses=2 cmds=[]

after walk finish: status=terminated tokens=1 scopes=2 EventTriggeredSubprocesses=0 cmds=[{cancelled}]
  surviving token e2d-t1 node=nesp-work scope="e2d-s1" state=1 await="e2d-c2"
esp InvokeAction cmdID="e2d-c2"
WARN trigger rejected on terminal instance instance_id=e2d trigger=engine.ActionCompleted status=terminated outcome=dropped command_id=e2d-c2
delivering esp ActionCompleted to the finished instance: err=<nil> status=terminated tokens=1 ... cmds=[]
```

Driven to termination (not stopped at the first surprise): the rollback dispatched
`nested-esp-action` to a real worker, then terminated the instance; the worker's
`ActionCompleted` is dropped, and the terminated instance is left carrying **one orphan
token in state `TokenWaiting` awaiting a command that can never be applied**.

**VERDICT E1: CONFIRMED-BUT-MOVED.** The hole is real, live and reachable — but its
predicate is `StatusCompensating`, not terminal. Any design that states direction (a) as
"fires on a terminal instance" is designing against a premise the code no longer has.

---

## E2 — Direction (b): a LOCAL throw silences the root arm — CONFIRMED

```
go test -v -run '^TestProbeE2LocalThrowSilencesRootArm$' ./engine/   → EXIT=0
```

```
started: status=running tokens=1 scopes=0 EventTriggeredSubprocesses=1
after LOCAL throw: status=compensating tokens=0 EventTriggeredSubprocesses=1 cursorActive="e2e-c2" cmds=[{e2e-c2 undo ...}]
  cursor={... ResumeNode:after ResumeScope: ... StartRecordCount:1 NextIndex:0 ActiveCmdID:e2e-c2 FinalStatus:running FinalErr:}

ROOT arm during LOCAL throw: status=compensating EventTriggeredSubprocesses=1
  cmds=[] vars=map[payload:1]                      ← SILENCED, but the payload WAS merged

after walk finish (resume): status=running tokens=1 EventTriggeredSubprocesses=1 cmds=[{e2e-c3 after-action ...}]
ROOT arm re-delivered while running: status=running tokens=1 scopes=1 EventTriggeredSubprocesses=0 cmds=[{e2e-c4 root-esp-action ...}]
  drive step 0: status=completed tokens=0 ... cmds=[{map[payload:1]}]
FINAL: status=completed tokens=0 scopes=0 ArmedEvents=0 Boundaries=0 EventTriggeredSubprocesses=0
```

**VERDICT E2: CONFIRMED.** The cursor's `FinalStatus:running` / `ResumeNode:after` says
the instance is legitimately still running; the root arm is nonetheless silenced.
Driven to TERMINATION: the walk resumes at `after`, the instance goes back to `running`,
a *second* delivery of the same signal fires normally, and the instance completes. So the
cost is exactly **one silently swallowed delivery**, with `vars=map[payload:1]` proving
the delivery was consumed (`markMatched` merged the payload before the fire no-op'd) —
it is not redelivered.

---

## E3 — Does `beginCompensation` drain ESP arms? — REFUTED (it does not)

### Writers of `StatusCompensating`, enumerated from source (not inherited)

`grep -rn "StatusCompensating" --include="*.go" . | grep -v _test.go`, filtered to
assignments — **five** write sites, four of which route into `beginCompensation`:

| # | site | trigger path | routes via |
|---|---|---|---|
| W1 | `engine/step_errors.go:254` | unhandled `ActionFailed` with records | `beginCompensation` |
| W2 | `engine/step_triggers.go:220` | `CancelRequested` with records | `beginCompensation` |
| W3 | `engine/step_compensation.go:211` | `CompensateRequested` (admin / reverse / partial) | `beginCompensation` |
| W4 | `engine/step_compensation.go:831` | `stepCompensationFinish` deferred-cancel/error re-entry | `beginCompensation` |
| W5 | `engine/step_nodes.go:1111` | `startCompensationWalk` (compensation THROW) | **does NOT** call `beginCompensation` |

(There is also a read-only `runtime/processdriver_reverse.go:99` and a
`transport/http/httpcore/admin_endpoints.go:26` mapping — neither is a write site.)

### Measurements (one arm per family seeded: gateway / boundary / ESP root + ESP non-root)

```
go test -v -run '^TestProbeE3' ./engine/   → EXIT=0
```

```
W2 before: status=running      tokens=1 scopes=1 ArmedEvents=1 Boundaries=1 EventTriggeredSubprocesses=2 cursorActive=""
W2 after : status=compensating tokens=0 scopes=1 ArmedEvents=0 Boundaries=0 EventTriggeredSubprocesses=2 cursorActive="w2-c1"
W3 before: status=running      tokens=1 scopes=1 ArmedEvents=1 Boundaries=1 EventTriggeredSubprocesses=2 cursorActive=""
W3 after : status=compensating tokens=0 scopes=1 ArmedEvents=0 Boundaries=0 EventTriggeredSubprocesses=2 cursorActive="w3-c1"
W1 before: status=running      tokens=1 scopes=1 ArmedEvents=1 Boundaries=1 EventTriggeredSubprocesses=2 cursorActive=""
W1 after : status=compensating tokens=0 scopes=1 ArmedEvents=0 Boundaries=0 EventTriggeredSubprocesses=2 cursorActive="w1-c1"
W4 walk-start:       status=compensating ArmedEvents=0 Boundaries=0 EventTriggeredSubprocesses=2 cursorActive="w4-c1"
W4 cancel-deferred:  status=compensating ArmedEvents=0 Boundaries=0 EventTriggeredSubprocesses=2 pendingCancel=true
W4 after finish re-entry: status=terminated ArmedEvents=0 Boundaries=0 EventTriggeredSubprocesses=0 cursorActive="" cmds=[{cancelled}]

W5 after start:  status=running      tokens=1 scopes=0 ArmedEvents=0 Boundaries=0 EventTriggeredSubprocesses=1
W5 before throw: status=running      tokens=1 scopes=1 ArmedEvents=1 Boundaries=1 EventTriggeredSubprocesses=2
W5 after throw:  status=compensating tokens=0 scopes=1 ArmedEvents=1 Boundaries=0 EventTriggeredSubprocesses=2 cursorActive="w5-c2"
```

**VERDICT E3: CONFIRMED (beginCompensation does NOT drain ESP arms).**

* All four `beginCompensation` writers: **ESP 2 → 2**, gateway **1 → 0**, boundary **1 → 0**.
  This is deliberate and documented at `engine/state_arms.go:132-137` —
  `cancelAllArmsAndBoundaries` "deliberately does NOT drain
  `s.EventTriggeredSubprocesses`" because a walk may still RESUME the instance.
* W5 (the throw walk) is different again: it drains **neither** ESP **nor** gateway arms
  (`ArmedEvents 1 → 1`); the boundary went 1→0 only because its host token completed.
* Only the terminal transition drains ESP (W4's finish: 2 → 0, via `endInstance`).

So the arms that direction (a) needs are kept alive *by design* for the whole duration of
every rollback — including the rollbacks that will terminate.

---

## E4 — Effect (c): the ADR-0168 tail — STALE (already fixed at the root site)

### The root site retires NOTHING today

`engine/step_nodes.go:376-397` (in `exitRootEventSubprocessScope`) states outright:
*"This tail retires NOTHING, and that is a correction to ADR-0168 Decision 3. It used to
call `removeEventTriggeredSubprocessArmsForScope("")` … documented afterwards as an
'accepted cost'. It is not a cost that can be accepted."* Source-verified: there is no
removal call between `exitRootEventSubprocessScope`'s completion branch and its
`return cmds, true, nil` at line 402.

### The pinning test, and its FIXTURE audit

`TestRootEventSubprocessExitKeepsRootArmsWhenTheWalkCanResume`
(`engine/step_compensation_walk_completion_test.go:685`).

Fixture check (not just the name): `rootWalkOutlivedByESPRunningOnDef` really declares a
non-interrupting root ESP (`idleESPBody`, `event.WithNonInterrupting()`), the test asserts
`require.Len(t, r.State.EventTriggeredSubprocesses, 1)` **before** the exit, and it
re-delivers `"boom"` after the resume and asserts a scope + task appear. Not vacuous.

**Mutation** — re-insert the removed line before `engine/step_nodes.go:402`:

```go
cmds = appendCancelTimers(cmds, c.s.removeEventTriggeredSubprocessArmsForScope("")) // MUTATION
```
```
go test -v -run 'TestRootEventSubprocessExitKeepsRootArmsWhenTheWalkCanResume' ./engine/  → EXIT=1
    step_compensation_walk_completion_test.go:734: "[]" should have 1 item(s), but has 0
        Messages: a deferred completion must not disarm the root event sub-processes
    step_compensation_walk_completion_test.go:744: "[]" should have 1 item(s), but has 0
        Messages: the resumed instance keeps its root event-sub-process arm
--- FAIL: TestRootEventSubprocessExitKeepsRootArmsWhenTheWalkCanResume
```

**VERDICT E4: the inherited claim is STALE.** Effect (c) describes ADR-0168 as-shipped;
ADR-0171 (same delivery) already withdrew it at the ROOT site, and the withdrawal is
mutation-verified. Measured arm loss under the mutation is **1 → 0** on this fixture — the
inherited number "2 → 0" is `ASSUMPTION (unverified)`; it does not come from this fixture.

### The NESTED site still retires — and nothing covers it

`engine/step_nodes.go:501` (`exitNestedEventSubprocessScope`'s tail) still calls
`removeEventTriggeredSubprocessArmsForScope(parentScopeID)`. That is defensible on its
face (the enclosing scope was just `closeScope`d two statements earlier), but two
comment claims about it are FALSE:

* `engine/step_nodes.go:486-489` says the nested completion conjunct is *"asserted by the
  named test"* (`TestCompensationWalkBlocksNestedEventSubprocessCompletion`).
  **Mutation** — delete `&& c.s.Compensating.ActiveCmdID == ""` at line 492:
  `go test ./engine/... → EXIT=1` with **only my own two RED probes failing**; no repo
  test fails. The test's own docstring (line 460-467) already admits this; the
  `step_nodes.go` comment contradicts it and is the stale one.
* **Mutation** — replace line 501 with `_ = parentScopeID`:
  `go test ./engine/... → EXIT=1`, again **only my two RED probes**. The nested tail's arm
  retirement is entirely uncovered.

For completeness, the third conjunct site `exitRootScope` (`engine/step_nodes.go:225`) IS
covered — deleting it fails `TestCompensationWalkInFlightBlocksCompletion`,
`TestCompensationWalkFinishCompletesInstance`,
`TestCompensationWalkSurvivesNestedEventSubprocessTearingDownItsResumeScope`; and the
root-ESP conjunct (line 354) fails `TestRootEventSubprocessExitKeepsRootArmsWhenTheWalkCanResume`
+ `TestRootEventSubprocessExitBlocksCompletionUnderRootWalk`.

---

## E5 — Claim (d): ADR-0124 Decision item 4 — CONFIRMED FALSE

`docs/adr/0124-repeatable-noninterrupting.md`, lines 58–64, verbatim:

```
58:4. **Runtime terminal guard:** `syncMsgWaiters` returns without re-registering, and `syncSignalBus`
59:   syncs an empty set, when `isTerminal(st.Status)`. A terminal instance holds no correlation
60:   waiter regardless of what arms linger in its snapshot. `isTerminal` excludes the transient
61:   `Compensating` state (which legitimately keeps its waiters). The engine is left untouched — a
62:   lingering arm in a terminal snapshot is harmless (`fireEventTriggeredSubprocessArm` is
63:   status-guarded to no-op on a non-`Running` instance); the runtime guard is the correctness
64:   boundary.
```

**VERDICT E5: the parenthetical on lines 62–63 is FALSE**, and E1a refutes it directly:
`fireEventTriggeredSubprocessArm` is status-guarded **only on the root branch**; the
non-root branch fired on `completed`, `failed` and `terminated`. Two further notes:

* Line 60–61 — *"`isTerminal` excludes the transient `Compensating` state (which
  legitimately keeps its waiters)"* — is the sentence that makes E1c reachable at the
  runtime layer, and it is **true**; the two sentences together are what make a non-root
  arm fire into a dying rollback.
* `engine/state_arms.go:175-181` already narrows the *harmlessness corollary* on the
  ADR-0164 ground ("the arm is now retired at completion instead"), but that note lives in
  the engine source; **the ADR file itself still carries the false parenthetical.**

---

## E6 — What is the CORRECT predicate?

### The meanings `s.Status` can carry when an ESP arm fires

| # | state | should the arm fire? | reachable? |
|---|---|---|---|
| (i) | `StatusRunning` | **YES** | measured (E1a, E2 re-delivery) |
| (ii) | `StatusCompensating` from a walk that will END the instance (cancel / unhandled error / admin full rollback / any walk with `PendingCancel`) | **NO** — the instance is dying; new work is orphaned | measured (E1c, E3 W1/W2/W3, E6 dump) |
| (iii) | `StatusCompensating` from a walk that will RESUME (compensation throw / full reverse / partial rollback) | **YES** — the instance is legitimately running | measured (E2, E6 dump, and `TestCompensationWalkSurvivesNestedEventSubprocessTearingDownItsResumeScope`) |
| (iv) | terminal (`completed`/`failed`/`terminated`) | **NO** | only reachable by a direct call (E1b) |

### The distinguishing state field: `s.Compensating.walkMode()` (+ `s.PendingCancel`)

`engine/state_compensation.go:203` — `walkMode()` is a pure function of the cursor:
`ResumeNode != ""` → throw; else `ToNode != ""` → partial; else `ReverseNode != ""` →
reverse; else `walkAdmin`. And `engine/step_compensation.go:666-745` derives
`finishPlan.resume = false` **exactly** in the `walkAdmin` branch — so
`walkMode() == walkAdmin` ⇔ *this walk terminates the instance*.

Executed (`engine/zz_probe_c_e6_test.go`):

```
go test -v -run '^TestProbeE6' ./engine/   → EXIT=0

W2 cancel rollback         → status=compensating walkMode=walkAdmin          ResumeNode="" ToNode="" ReverseNode="" FinalStatus=terminated FinalErr="cancelled" PendingCancel=false
W3 admin full rollback     → status=compensating walkMode=walkAdmin          ResumeNode="" ToNode="" ReverseNode="" FinalStatus=running    FinalErr=""          PendingCancel=false
W1 error rollback          → status=compensating walkMode=walkAdmin          ResumeNode="" ToNode="" ReverseNode="" FinalStatus=failed     FinalErr="boom"      PendingCancel=false
W3b full reverse (resumes) → status=compensating walkMode=walkReverse        ReverseNode="start"                    FinalStatus=running                        PendingCancel=false
W3b + deferred cancel      → status=compensating walkMode=walkReverse        ReverseNode="start"                    FinalStatus=running                        PendingCancel=true
W3c partial rollback       → status=compensating walkMode=walkPartial        ToNode="work"                          FinalStatus=running                        PendingCancel=false
W5 LOCAL throw (resumes)   → status=compensating walkMode=walkThrowScopeWide ResumeNode="after"                     FinalStatus=running                        PendingCancel=false
```

Two consequences the numbers force:

* **`FinalStatus` is NOT a usable discriminator.** The *admin full rollback* — which
  terminates — carries `FinalStatus=running` (the zero value, mapped to
  `StatusTerminated` only later, in `applyTerminate`). Anything keying on `FinalStatus`
  would classify it as resuming.
* **The classification is not static for the life of the walk.** `PendingCancel=true` on
  a `walkReverse` cursor means that resuming walk will now terminate
  (`engine/step_compensation.go:785`). The predicate must read it.

### Proposed predicate (measured, see E7)

```go
if s.Status.IsTerminal() ||
    (s.Status == StatusCompensating &&
        (s.Compensating.walkMode() == walkAdmin || s.PendingCancel)) {
    return nil, nil
}
if ea.EnclosingScopeID != "" {
    if s.scopeByID(ea.EnclosingScopeID) == nil {
        return nil, nil
    }
}
```

i.e. **"a dying instance spawns no new work, whichever scope the arm belongs to"** —
replacing "the root scope is only alive while the instance is `running`".

`ASSUMPTION (unverified):` whether `Status == StatusCompensating` with an EMPTY cursor
(`ActiveCmdID == ""`) is reachable. If it is, `walkMode()` returns `walkAdmin` for it and
the predicate would silence the arm; adding `&& s.Compensating.ActiveCmdID != ""` to the
compensating conjunct is the conservative variant. I did not find a route to that state
and did not spend the time to prove one does not exist.

---

## E7 — Which shape actually closes it? — shape (A) REFUTED, shape (B) already in place, shape (C) works

### Shape (A): put the same `!= StatusRunning` check on the non-root branch — **REFUTED**

Mutation M3 applied `if s.Status != StatusRunning { return nil, nil }` ahead of both branches:

```
go test ./engine/... → EXIT=1
--- FAIL: TestCompensationWalkSurvivesNestedEventSubprocessTearingDownItsResumeScope (0.00s)
    step_compensation_scope_drain_test.go:508: no human task for node "espTask"
```

That fixture (`engine/step_compensation_scope_drain_test.go:479`) fires a **nested
interrupting ESP arm on signal `"boom"` while a scope-wide throw walk is in flight** —
case (iii). Shape (A) silences it and the instance never opens the ESP body. Shape (A) is
a regression, not a fix.

Shape (A'), `IsTerminal()` on both branches (mutation M4), leaves `./engine/...` at
EXIT=0 — but it closes nothing reachable, because E1b showed terminal is already
unreachable. Suite-green here is evidence about the SUITE only.

### Shape (B): make terminal transitions drain the arms — **already true**

Measured in E1b(A) and E3(W4): `endInstance` → `cancelAllScheduledWork`
(`engine/state_arms.go:184-189`) took ESP arms 2 → 0 on both the immediate-cancel and the
walk-finish terminal paths. Combined with the ADR-0165 dispatch guard there is no
*terminal* gap left to close. **Shape (B) does not address the reachable defect**, which
lives in `StatusCompensating`.

### Shape (C): the E6 predicate — RED → GREEN on a reproduction

Reproduction `engine/zz_probe_c_e7_test.go` asserts the desired behaviour, so it is RED on
today's engine:

```
go test -v -run '^TestProbeE7' ./engine/   → EXIT=1   (BEFORE)
--- FAIL: TestProbeE7ReproNonRootArmFiresIntoDyingRollback
    a dying rollback dispatched new work: InvokeAction {CommandID:e7-c2 Name:nested-esp-action ...}
    a dying rollback placed 1 new token(s): [{ID:e7-t1 NodeID:nesp-work ScopeID:e7-s1 State:1 AwaitCommand:e7-c2 ...}]
    terminated with status=terminated tokens=1
    terminated instance carries 1 orphan token(s): [...]
--- PASS: TestProbeE7ControlThrowWalkStillFires
--- FAIL: TestProbeE7ControlRootArmDuringLocalThrow
    direction (b): the ROOT arm was SILENCED during a LOCAL throw walk; cmds=[]
```

With shape (C) applied to `engine/step_eventsubprocess.go:156-170`:

```
go test -v -run '^TestProbeE7' ./engine/   → EXIT=0   (AFTER)
--- PASS: TestProbeE7ReproNonRootArmFiresIntoDyingRollback   (terminated with status=terminated tokens=0)
--- PASS: TestProbeE7ControlThrowWalkStillFires
--- PASS: TestProbeE7ControlRootArmDuringLocalThrow

go test ./engine/...                                                          → EXIT=0
go test ./processtest/... ./service/... ./runtime/{signal,task,calllink}/... ./transport/http/...  → EXIT=0 (10 packages)
```

**VERDICT E7: shape (C) closes both directions at once** — the non-root arm stops
spawning work into a dying rollback (a), and the root arm starts firing during a resuming
walk (b) — while the two controls (throw-walk non-root fire; nested-ESP teardown fixture)
stay green.

---

## E8 — Blast radius: which tests depend on `!= StatusRunning`? — **NONE**

Mutations at `engine/step_eventsubprocess.go:167`, each run over the whole engine package
(exit codes, not pipelines):

| mutation | predicate | `go test ./engine/...` |
|---|---|---|
| M1 | `if s.Status.IsTerminal()` (root only) — lets Compensating fire | **EXIT=0**, `ok github.com/kartaladev/wrkflw/engine 0.488s` |
| M2 | guard **deleted** (`_ = s.Status`) | **EXIT=0**, `ok … 0.484s` |
| M3 | same predicate on BOTH branches | EXIT=1 — `TestCompensationWalkSurvivesNestedEventSubprocessTearingDownItsResumeScope` |
| M4 | `IsTerminal()` on BOTH branches | **EXIT=0**, `ok … 0.650s` |

M2 also run across every container-free package — **EXIT=0**:

```
ok processtest 0.483s · ok service 0.945s · ok runtime/signal 0.524s · ok runtime/task 1.629s
ok runtime/calllink 2.085s · ok transport/http/{fiber,gin,httpcore,parity,stdlib}
```

**VERDICT E8: NO test fails. LOUDLY: the `s.Status != StatusRunning` root-scope guard at
`engine/step_eventsubprocess.go:167` is entirely uncovered — it can be deleted with the
whole reachable test suite green.** Whatever this delivery does to that line, it owes new
tests: nothing existing will catch a regression, and nothing existing will confirm the
change either. The only mutation that produces a failure (M3) fails because it breaks the
*non-root* branch, not because anything covers the root one.

---

## Cross-cutting corrections the design bundle must carry

1. Restate direction (a) as **"a non-root arm fires while the instance is in a
   terminating compensation walk"**. The terminal formulation is refuted end-to-end.
2. Do not restate effect (c) as an open accepted cost — it was fixed at the root site by
   ADR-0171 and is mutation-pinned. Its "2 → 0" figure is unverified; measured 1 → 0.
3. `engine/step_nodes.go:486-489`'s claim that the nested conjunct is "asserted by the
   named test" is FALSE (mutation: suite green). Either fix the comment or add the test.
4. `docs/adr/0124-repeatable-noninterrupting.md:62-63` needs the parenthetical corrected
   in the ADR file itself, not only in the engine source note.
5. `beginCompensation`'s deliberate non-drain of ESP arms
   (`engine/state_arms.go:132-137`) is the enabling condition for the whole defect and is
   *correct* for resuming walks — the fix belongs at the fire site, not there.

---

## Hygiene

All probe files deleted; both patched files restored from snapshots
(`SNAP_step_eventsubprocess.go`, `SNAP_step_nodes.go`). Final state recorded below.
