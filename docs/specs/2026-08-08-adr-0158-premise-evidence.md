# Evidence — ADR-0158 premise re-derivation and status-re-check adjudication

- Date: 2026-08-08
- Status: **evidence only.** Not a spec, not a decision. This is the executed
  ground truth a future ADR-0158 rewrite must build on, preserved because it
  cost three Opus agents to produce and existed only in a session scratchpad.
- Base: all runs against `main` @ `9e96112` on 2026-08-07/08.

> ⚠ Two of the findings here are **bugs in shipped code on `main`**, not
> properties of any in-flight delivery. See the adjudication section.

> 🛑 **CORRECTION (2026-08-08, added by the ADR-0168/0169/0170 bundle).** This
> file's recommended predicate — **`s.Status != StatusRunning`** — is **REFUTED
> BY EXECUTION**. It appears in **§Q4(c)** and in **"Two NEW pre-existing bugs"
> item 1**, both marked in place below. The original text is left untouched
> deliberately: this file is evidence, and laundering it would hide that the
> shape was ever recommended. **Do not carry `!= StatusRunning` into the future
> ADR-0158 rewrite.** The refutation is
> `docs/specs/2026-08-08-compensation-walk-and-mid-delivery-terminal.md` §6.

---

# Adjudication — ADR-0158's per-iteration status re-check

Tie-breaker between premise agents A and B. All executed against `main` @ `9e96112`.
The agent could not write this file itself (harness blocked subagent report-file
writes), so it is transcribed here by the controller. Raw runs were in
`/tmp/q{1,2,2b,3,3b}*.log` in that agent's worktree and are gone once cleaned.

## Q1 — the writers, enumerated (not sampled)

`endInstance` (`engine/state.go`) is the **sole writer of a terminal status**;
`applyTerminate` routes through it. No composite-literal `InstanceState{Status:…}`
write exists.

| path (symbol) | status set | ArmedEvents | Boundaries | ESP arms | drive-reachable |
|---|---|---|---|---|---|
| `endInstance` (all 9 callers) | Completed/Failed/Terminated | drained | drained | drained | yes |
| `handleUnhandledError` → `beginCompensation` | Compensating | drained | drained | **NOT** | **YES** |
| `applyFinish` → `beginCompensation` | Compensating | drained | drained | **NOT** | no |
| `stepCompensateRequested` → `beginCompensation` | Compensating | drained | drained | **NOT** | no |
| `handleCancelRequested` → `beginCompensation` | Compensating | drained | drained | **NOT** | no |
| `startCompensationWalk` | Compensating | **NOT** | **NOT** | **NOT** | **YES** |

Mechanism: `endInstance` → `cancelAllScheduledWork` = `cancelAllTimers` +
`cancelAllArmsAndBoundaries` + `removeAllEventTriggeredSubprocessArms`.
`beginCompensation` calls only the first two, and `cancelAllArmsAndBoundaries`'s
own doc comment says it **deliberately** does not drain
`s.EventTriggeredSubprocesses`. `startCompensationWalk` touches no arm family.

Measured, one arm seeded per family:
```
Q1a[completed] AFTER status=completed  ArmedEvents=0 Boundaries=0 ESP=0  re-resolve all false
Q1a[failed]    AFTER status=failed     ArmedEvents=0 Boundaries=0 ESP=0
Q1a[terminated]AFTER status=terminated ArmedEvents=0 Boundaries=0 ESP=0
Q1b AFTER status=compensating ArmedEvents=0 Boundaries=0 ESP=1  espArm re-resolves LIVE
Q1c AFTER status=compensating ArmedEvents=1 Boundaries=1 ESP=1  ALL THREE re-resolve LIVE
```

⚠ **Compensating sites are FIVE, not four** (B's count omitted
`handleCancelRequested`), and four of the five *do* drain two of three families
(B's "they drain nothing" over-generalised its own evidence — a recap-sentence
failure).

## Q2 — post-terminal resurrection via tier 4

`handleUnhandledError`'s failFast branch fails the instance **without dropping
tokens**, so tier 4 resumes a snapshotted token and drives it on a dead instance.
Proven by instrumented panic at the arm-append sites:
```
panic: PROBE C Q2: boundary arm appended on TERMINAL instance status=failed
  engine.armBoundaries      engine/step_boundaries.go:70
  engine.userTaskStrategy.enter engine/step_nodes.go:734
  engine.drive              engine/step.go:258
  engine.resumeAndDrive     engine/step_state.go:220
  engine.handleSignalReceived engine/step_triggers.go:823
```
A real command escapes — on a `failed` instance:
`cmd engine.ScheduleTimer {TimerID:i1-tm1 Token:i1-t3 Kind:TimerIntermediate}`.
ADR-0161's filter exempts `ScheduleTimer`, so it reaches the runtime.

Instrumentation discriminates (the probe tripped it) and the whole container-free
corpus ran with **0 hits** — no existing test covers this route.

## Q3 — `StatusCompensating` is the real justification

`IsTerminal()` is true only for Completed/Failed/Terminated (`engine/state.go`).
Executed end-to-end through the public `Step` API:
```
Q3b AFTER SIGNAL#1 status=compensating Compensating.ActiveCmdID=i1-c2  Boundaries=1 (bndB live)
Q3b AFTER SIGNAL#2 status=completed tokens=0 ... cmd engine.CompleteInstance {Result:map[]}
Q3b AFTER SIGNAL#2 Compensating.ActiveCmdID=""
WARN trigger rejected on terminal instance trigger=engine.ActionCompleted status=completed outcome=dropped command_id=i1-c2
```
**A process whose rollback is in flight is flipped to `completed`,
`CompleteInstance{}` is published, and the compensation action's result is then
silently dropped. The rollback never finishes.**

`IsTerminal()` reads **false** here and does not stop it. `!= StatusRunning` reads
**true** and does — verified by adding `if s.Status != StatusRunning { return nil, nil }`
to `fireBoundaryArm`:
```
Q3b AFTER SIGNAL#2 status=compensating ActiveCmdID="i1-c2"
Q3b ActionCompleted(i1-c2): err=<nil> cmds=1 status=running   ← walk completes, instance resumes
```
`go test ./engine/...` with that guard → EXIT=0, so **no existing test covers it
either way**.

`grep -c 'Status' engine/step_boundaries.go engine/step_gateways.go` → **0 and 0**.
Neither fire function mentions `Status` at all.

## Q4 — the answer

- **(a) NECESSARY — yes, but not for the ADR's stated reason.** Termination is
  fully covered by re-resolution (Q1). The check is necessary for
  **`StatusCompensating`**, which ADR-0158 never mentions.
- **(b) SUFFICIENT** for the single-delivery window 0158 opens; **not** for the
  same hazard across deliveries, nor the timer/message cascades, nor Q2.
- **(c) PREDICATE: `s.Status != StatusRunning`**, not `IsTerminal()`. Gate **all
  four tiers**, not just 1–3.

> 🛑 **CORRECTION — (c) is REFUTED.** See ADR-0168/0169/0170 and
> `docs/specs/2026-08-08-compensation-walk-and-mid-delivery-terminal.md` §6.
>
> `!= StatusRunning` applied to `fireBoundaryArm` was executed against the
> bundle's §3.1 fixture. It saves the rollback and then **strands the instance
> forever** — signal `s2` is silently swallowed, `taskB` stays parked with
> `bndB` still armed, and signals are one-shot broadcasts, so nothing
> redelivers:
>
> ```
> AFTER SIGNAL#2 (s2)  status=compensating tokens=1 boundaries=1 activeCmd="i1-c2" cmds=[]
> AFTER undoA(i1-c2)   status=running      tokens=1 boundaries=1 activeCmd=""      cmds=[]
> ```
>
> **The predicate conflates the two meanings of `StatusCompensating`.** A
> `beginCompensation` rollback has already drained the boundary and gateway
> arms, so the guard is a no-op there; a `startCompensationWalk` local throw
> leaves the process legitimately running, so the guard blocks exactly the arms
> that *should* fire. It is inverted with respect to its own purpose.
>
> **The refuted shape is ALREADY SHIPPED** at `engine/step_eventsubprocess.go:167`
> (`fireEventTriggeredSubprocessArm`, root scope only), where it was measured
> silencing a legitimate signal during a local compensation throw. That is
> backlog work with its own ADR owed — and that ADR must not re-derive the
> predicate from this file.
>
> The correct decomposition is **two** predicates, neither of them
> `!= StatusRunning`: `Compensating.ActiveCmdID` guards *completion* (ADR-0168);
> `IsTerminal()` guards *mid-delivery dispatch* (ADR-0169).

## Verdict on the two premise agents

- **A** right that the fire functions have no guard (stronger than A knew: zero
  `Status` occurrences) and right on the bottom line. **Overreached** by inferring
  load-bearing-ness from shape; A's probe used `forceTerminate`, whose token-nilling
  made tier 4 look accidentally safe. On the `handleUnhandledError` route tokens
  survive and tier 4 does **not** no-op — worse than A's account. A never reached
  the real justification.
- **B** right that `endInstance` is the sole terminal writer and drains all three,
  so the ADR's stated reason really is refuted; right to flag Compensating. **Wrong
  three ways**: five sites not four; "drain nothing" over-generalised; and B stopped
  before constructing the consequence, which is the orphaned compensation walk.

**Third answer: keep the re-check, delete its justification, change its predicate,
widen its scope to all four tiers.**

## Two NEW pre-existing bugs on `main` (independent of ADR-0158)

1. **Boundary/gateway arms fire on a `StatusCompensating` instance** and can
   destroy an in-flight compensation walk, reporting a rolled-back process as
   `completed` while its compensation action result is dropped. Verified fix
   shape: `if s.Status != StatusRunning { return nil, nil }` in `fireBoundaryArm`;
   engine suite stays green with it. **Untested either way.**

   > 🛑 **CORRECTION — the BUG is real; the "verified fix shape" is REFUTED.**
   > See §Q4(c)'s correction block above and
   > `docs/specs/2026-08-08-compensation-walk-and-mid-delivery-terminal.md` §6.
   > Executed, `!= StatusRunning` in `fireBoundaryArm` saves the rollback and
   > strands the instance forever. The bug itself is closed by **ADR-0168**
   > with a different predicate: `len(s.Tokens) == 0 && s.Compensating.ActiveCmdID == ""`
   > at the three normal-completion sites — guarding *completion* rather than
   > *arm firing*.
   >
   > ⚠ **"engine suite stays green with it" was the evidence offered here, and
   > it is not evidence of correctness** — it measures test coverage. The suite
   > is `EXIT=0` under the refuted shape precisely because nothing covers this
   > path in either direction, which this same paragraph says one sentence
   > later. The same predicate is **already shipped** at
   > `engine/step_eventsubprocess.go:167` and misbehaves there exactly as
   > predicted.
2. **Post-terminal resurrection via tier 4** after `handleUnhandledError`'s
   failFast branch: zombie token, new boundary arm, and an un-filtered
   `ScheduleTimer` on a `failed` instance. ⚠ Check against ADR-0164's five known
   resurrection routes — this may be a sixth.

## Also to correct in the bundles

- ADR-0124 Decision item 4's "harmless lingering arm" sentence.
- ADR-0158's "`fireBoundaryArm` only no-ops by accident" — the `hostTok == nil`
  no-op is deliberate and commented.

## Residual uncertainty (marked, not hidden)

- **ASSUMPTION (argued, not proven):** no snapshotted tiers-1–3 identity can
  re-resolve live after a mid-delivery terminal transition. Absence of a
  construction, not a proof.
- **ASSUMPTION (unverified):** that the escaping `ScheduleTimer` actually arms a
  gocron job for a dead instance — observed in `StepResult.Commands` only; the
  runtime is Docker-gated.
- Three non-drive-reachable Compensating sites classified by their shared
  `beginCompensation` call, not executed individually.
- **Not executed:** whether a per-iteration check interacts badly with `mergeVars`'
  merge-once semantics when a delivery aborts mid-way. The ADR should state the
  intended semantics.

---

# Appendix A — premise agent A (dispatch structure, claims C1–C12)

# FINDINGS-A — ADR-0158 premise re-derivation vs main @ 9e96112

## C1 — CONFIRMED (locations MOVED)
Draft: handleSignalReceived at engine/step_triggers.go:655-757, four tiers gw->boundary->eventsub->token.
Found: engine/step_triggers.go:728 (doc :725-727), body to :830.
 :762 tier1 armedEventBySignal -> resolveGatewayWin
 :775 tier2 boundaryArmBySignal -> fireBoundaryArm
 :788 tier3 eventTriggeredSubprocessArmBySignal -> fireEventTriggeredSubprocessArm
 :801 tier4 loop over snapshotIDs
Dispatched from engine/step.go:194.
EXTRA: snapshotIDs := s.tokenIDsAwaitingSignal(t.Name) at :757 — taken BEFORE tiers 1-3.
Consequence: line range wrong by ~73 lines; structure/order intact.

## C2 — CONFIRMED (locations MOVED)
armBySignal now engine/state_arms.go:225 (draft 219); first-match loop, ""-guard ADR-0152.
armedEventBySignal :308, boundaryArmBySignal :341, eventTriggeredSubprocessArmBySignal :368.
removeArmsWhere :262-274 (draft 256-268); still make([]T,0,len(arms)) + field reassign => pointer-detachment argument holds.

## C5 — CONFIRMED (add line numbers)
engine/step_arm_dispatch.go:28 dispatchArmCascade; asymmetry comment verbatim at :24-27.

## C3 — CONFIRMED BY EXECUTION
Two parallel standalone catches on "x": ONE delivery advanced BOTH (tokens c1,c2 -> a1,a2; tasks 0->2).

## C4 — CONFIRMED (MOVED 671-676 -> 744-749)

## C6 — CONFIRMED BY EXECUTION
Two timer boundaries on one host: ScheduleTimer TimerID="i1-tm1" and "i1-tm2". Distinct.
armByTimer guards ""=no match (state_arms.go:210-213).

## C7 — CONFIRMED BY EXECUTION; ADR-0166 changed NOTHING in engine
Two parallel message catches on "m": ONE delivery resumed ONE (c1 advanced to e1 and completed; c2 still parked).
Boundary vs event-sub on same message name: BOUNDARY won, event-sub arm stayed armed (evtsubs=1).
ADR-0166 is processtest-only ("Deliberately not addressed: Any change to engine").

## C8 — REPRODUCES ON MAIN (headline defect is REAL)
AFTER START: tokens=2 (workA i1-t2, workB i1-t3) boundaries=2 tasks=2 SignalWaiters=[escalate escalate]
AFTER 1x SignalReceived{escalate}: tokens=2 (workB STILL PARKED i1-t3; i1-t4 at escA) boundaries=1 tasks=3
  cmds=2: UpdateTask{i1-h1 state=cancelled}, AwaitHuman{i1-h3}
AFTER 2nd delivery: boundaries=0, tokens = escA + escB.
=> exactly ONE host interrupted per delivery; the survivor's arm persists.
processtest (shipped harness, ADR-0166): DriveToCompletion FAILS
  "workflow-processtest: unhandled park: human-task at node \"workB\"", notify count 1, 2 park visits.

## C9 — CONFIRMED BY EXECUTION (order is outcome-affecting)
signal: interrupting ROOT event-sub (tier3) + standalone catch (tier4) on "x" -> event-sub cancelled the catch token; history has NO "after" visit.
message: boundary arm + event-sub arm on "m" -> boundary fired, evtsubs still 1.

## C10 — CONFIRMED BY EXECUTION
cloneState at engine/step.go:84 (line NUMBER STILL CORRECT). err=... generate token id ...; returned StepResult zero;
caller state DeepEqual vs pre-call snapshot = TRUE (tokens 2, boundaries 2, tasks both unclaimed).

## C11 — CONFIRMED BY EXECUTION
MACRO start + MICRO signal: ONE Step emitted AwaitHuman x2 (parks twice in one Micro step).
firstActive global: with MICRO start, the boundary arm's drive advanced a DIFFERENT branch's token.

## C12 — 0165 is COMPLEMENTARY, not redundant, no conflict
SignalReceived.terminalPolicy() = rejectSilently (engine/trigger.go:487).
Guard is at dispatch ENTRY only (engine/step.go:129). Executed: re-delivery to terminal instance ->
WARN "trigger rejected on terminal instance ... outcome=dropped signal_name=x", err=nil, cmds=0, vars=map[] (no injection).
Mid-delivery terminal executed: tier2 boundary force-terminated (Status=terminated tokens=0); tier4 skipped only
because forceTerminate nils s.Tokens (tok==nil), NOT because of a status check. 0158's per-iteration check STANDS.
fireBoundaryArm and resolveGatewayWin have NO status guard; only step_eventsubprocess.go:167 (`s.Status != StatusRunning`, root scope only).

---

# Appendix B — premise agent B (arms mechanics, claims C13–C24)

# FINDINGS-B — re-derivation of ADR-0158 premises (C13-C24)

Worktree wt-b, pinned to main @ 9e96112 (detached).
Method: Premise Discipline — claims about current behaviour EXECUTED, output recorded.

## C13 — VERDICT: CONFIRMED (line refs MOVED; hazard UNDERSTATED)

Draft asserts: removeArmsWhere (engine/state_arms.go:256-268) allocates a fresh
backing array and assigns it over the field, so a *boundaryArm captured beforehand
keeps pointing into the DETACHED old array where the removed arm is still intact.

Found today: removeArmsWhere at engine/state_arms.go:262-274 (make at :263).
armBySignal at engine/state_arms.go:225 (draft said :219). Both MOVED.
Wrappers assigning over the field: removeArmedEventsForGateway :326-330,
removeBoundaryArmsForHost :359-363, removeEventTriggeredSubprocessArmsForScope :390-394.

Command: go test -run '^TestProbeC13PointerAliasingAfterRemove$' -v ./engine/  (EXIT=0)
OBSERVED:
  C13 before: len=2 captured={HostToken:tokA ... Signal:escalate ...}
  C13 after: len(s.Boundaries)=1 cancelTimerIDs=[]
  C13 backing array reallocated: oldPtr=0x79de8ed0ac80 newPtr=0x79de8ed0adc0 same=false
  C13 pointee AFTER removal: {HostToken:tokA ... Signal:escalate ...}
  C13 pointee still names the retired arm (HostToken==tokA): true
  C13b len=1 pointee={HostToken:tokB ...} intact=true
  C13c survivor pointer aliases new slice elem0: false

Consequence: premise holds; the value-identity decision is correctly motivated.
TWO corrections owed: (1) line refs; (2) the draft names only the REMOVED arm.
Row C13c shows a pointer to a SURVIVING arm is also detached, so a WRITE through a
pre-removal pointer is silently dropped, not just a stale read.

## C16 — VERDICT: CONFIRMED (existence claim, compile-verified)

armedEvent (engine/state_arms.go:50-60) has fields GatewayToken, CatchNode, Flow,
triggerMatch — NO NonInterrupting. boundaryArm (:68-86) and
eventTriggeredSubprocessArm (:102-118) each declare NonInterrupting bool.

Command: go vet ./engine/ with a probe reading ae.NonInterrupting  (EXIT=1)
OBSERVED: engine/zz_probe_b_compile_test.go:7:9: ae.NonInterrupting undefined
          (type armedEvent has no field or method NonInterrupting)
Runtime probe: C16 boundaryArm.NonInterrupting=true eventTriggeredSubprocessArm.NonInterrupting=true
              C16 armedEvent fields: {GatewayToken: CatchNode: Flow: triggerMatch:{...}}

Consequence: the ADR's "tier 1 keeps plain slice order" rationale stands.

## C14 — VERDICT: CONFIRMED (line ref MOVED)

Draft: resolveGatewayWin removes EVERY arm of the resolved gateway
(engine/step_gateways.go:266), so two same-signal arms on one gateway token can
never both fire.
Found: engine/step_gateways.go:271  s.removeArmedEventsForGateway(ae.GatewayToken)
inside resolveGatewayWin (declared :214). Draft :266 is stale by 5 lines.

Command: go test -run '^TestProbeC14TwoSameSignalArmsOneGateway$' -v ./engine/ (EXIT=0)
OBSERVED:
  C14 model.Validate(def) = <nil>
  C14 after start: ArmedEvents=2 tokens=1
  C14   token id=c14-i1-t1 node=evtgw state=1 awaitCmd="evtgw:c14-i1-t1"
  C14 after signal: status=running ArmedEvents=0 tokens=1
  C14   token id=c14-i1-t1 node=svcA state=1
  C14 InvokeAction names = [actA]  (len=1)
Consequence: premise holds. Only actA fires; ArmedEvents drops 2->0 in one step.
Tier-1 plurality is meaningful only across DISTINCT gateway tokens, as the ADR says.

## C15 — VERDICT: CONFIRMED (executed, with a valid definition)

Draft: resolveGatewayWin does NOT consume the gateway token, so a branch looping
back to the same gateway re-arms (GatewayToken, CatchNode) byte-identically.

FIRST ATTEMPT was itself a false premise trap: the naive loop
(catchA -> evtgw directly) is REJECTED by Validate:
  C15 model.Validate(def) = workflow-definition: gateway both splits and joins: node "evtgw"
so a reader could dismiss the ABA as unreachable. It is NOT. Routing the loop
through an exclusive-gateway merge gives evtgw one incoming flow and Validate
accepts it.

Command: go test -run '^TestProbeC15bValidSynchronousGatewayLoop$' -v ./engine/ (EXIT=0)
OBSERVED:
  C15b model.Validate(def) = <nil>
  C15b after start: tokens=1 ArmedEvents=[{GatewayToken:c15b-i1-t1 CatchNode:catchA Flow:f2 triggerMatch:{TimerID: Signal:sig ...}} {GatewayToken:c15b-i1-t1 CatchNode:catchB Flow:f3 triggerMatch:{... Signal:other ...}}]
    token id=c15b-i1-t1 node=evtgw state=1 awaitCmd="evtgw:c15b-i1-t1"
  C15b after signal#1: tokens=1 ArmedEvents=[{GatewayToken:c15b-i1-t1 CatchNode:catchA Flow:f2 ... Signal:sig ...} {GatewayToken:c15b-i1-t1 CatchNode:catchB Flow:f3 ... Signal:other ...}]
    token id=c15b-i1-t1 node=evtgw state=1 awaitCmd="evtgw:c15b-i1-t1"
  C15b after signal#2: identical again.
Consequence: byte-identical re-arm, SAME token id, SAME sentinel, WITHIN ONE Step
(the loop closes synchronously inside drive). The "already-resolved gateway tokens"
guard is genuinely load-bearing. The ADR should cite the merge-gateway shape,
because the obvious direct loop is Validate-rejected and would let a reviewer
wrongly conclude the guard is dead code.

## C20 — VERDICT: CONFIRMED and UNDERSTATED

Draft: model.Validate enforces NEITHER unique node ids NOR a single flow between a
given pair, so a definition can produce two arms with the same identity tuple.

Command: go test -run '^TestProbeC20ValidateDuplicates$' -v ./engine/ (EXIT=0)
OBSERVED:
  C20a duplicate node ids -> model.Validate = <nil>
  C20b two flows same pair -> model.Validate = <nil>
  C20b arms after start: n=2 [{GatewayToken:c20b-i1-t1 CatchNode:catchA Flow:f2 ... Signal:sig ...} {GatewayToken:c20b-i1-t1 CatchNode:catchA Flow:f2dup ... Signal:sig ...}]
  C20c duplicate flow ids -> model.Validate = <nil>
Consequence: both halves hold AND the collision is realised end-to-end: two arms
whose (GatewayToken, CatchNode) identity is identical and which differ only in Flow.
UNDERSTATED: Validate also accepts DUPLICATE FLOW IDS (C20c) - a third hole the ADR
does not name. Also note the de-dup on (GatewayToken, CatchNode) DISCARDS the second
arm's distinct Flow value; that is the intended degradation but should be stated,
since Flow is what resolveGatewayWin's fallback branch (moveAlongSingleFlow) uses.

## C17 — VERDICT: CONFIRMED as a code fact; its STATED CONSEQUENCE is REFUTED

Draft: fireEventTriggeredSubprocessArm checks status for the ROOT SCOPE ONLY
(engine/step_eventsubprocess.go:148-159); without a per-iteration status re-check a
later arm fires into a completed or terminated instance.

Found: fireEventTriggeredSubprocessArm is at engine/step_eventsubprocess.go:156
(declaration); the root-only status check is :159-170 (draft :148-159 -> MOVED +8).
Structure unchanged: non-root -> only `s.scopeByID(id) == nil` is checked;
root ("") -> `s.Status != StatusRunning`.

Command: go test -run '^TestProbeC17StatusCheckRootScopeOnly$' -v ./engine/ (EXIT=0)
OBSERVED:
  C17a ROOT arm, Status=completed -> err=<nil> cmds=0 scopes=0 tokens=0
  C17b NON-ROOT arm, Status=completed -> err=<nil> cmds=1 scopes=2 tokens=1
  C17b   scope id=p17b-s1 node=esp parent="sc1"
  C17b   token id=p17b-t1 node=esp-work scope=p17b-s1 state=1
  C17b   cmd engine.InvokeAction {CommandID:p17b-c1 Name:esp-action ... }
  C17b status AFTER = completed
So the asymmetry is real and severe: given a live scope entry, a NON-root ESP arm
fires into a COMPLETED instance, opens a scope, places a token and emits a live
InvokeAction. The root arm cleanly no-ops.

BUT the draft's REASON for the per-iteration status re-check does not survive:

Command: go test -run '^TestProbeC17TerminalDrainsAllArms$' -v ./engine/ (EXIT=0)
OBSERVED:
  C17c after endInstance: status=completed cmds=0 ArmedEvents=0 Boundaries=0 ESP=0 Scopes=1
  C17c re-resolve armedEventBySignal=<nil> boundaryArmBySignal=<nil> espArmBySignal=<nil>
  C17c   ZOMBIE scope id=sc1 node=sub parent=""
endInstance (engine/state.go:352) -> cancelAllScheduledWork (state_arms.go:186)
drains ALL THREE arm families to zero, across root AND non-root scopes. Every
terminal Status assignment in non-test engine code is inside endInstance (grep for
`.Status = Status` finds only StatusCompensating/StatusRunning elsewhere). So once
an earlier arm makes the instance terminal, RE-RESOLUTION ALONE already returns nil
for every remaining snapshot identity. The status re-check is defensive, not
load-bearing; the ADR overstates it.

⚠ NEW GAP the ADR does not consider: StatusCompensating is NOT terminal
(Status.IsTerminal(), engine/state.go:55, returns true only for
Completed/Failed/Terminated) and is set OUTSIDE endInstance at four sites
(step_nodes.go:1037, step_compensation.go:203, :779, step_errors.go:237). An
earlier arm's drive raising an unhandled error puts the instance into
StatusCompensating WITHOUT draining arms, so a later arm in the same snapshot fires
into a compensating instance and an `IsTerminal()` re-check will NOT stop it. The
ADR's "Status and errors" section must address Compensating explicitly.
Also confirmed in passing: the ZOMBIE SCOPE (backlog 5b) is real - endInstance
leaves s.Scopes populated.

## C18 — VERDICT: CONFIRMED in outcome; the cited MECHANISM and its SCOPE are WRONG

Draft: the interrupting fire calls removeEventTriggeredSubprocessArmsForScope
(engine/step_eventsubprocess.go:207), which retires every arm of THAT SCOPE, so
after an interrupting fire the non-interrupting arm never runs and can never run
again for that instance.

Found: step_eventsubprocess.go:207 is now
  cmds = append(cmds, cancelScopeSubtree(s, ea.EnclosingScopeID, at, CloseKindBoundaryInterrupted)...)
removeEventTriggeredSubprocessArmsForScope is NOT called from this file at all any
more (its only mention here is a comment, :225). It now lives behind
cancelScopeSubtree in a DIFFERENT FILE: engine/step_cancel.go:101 (named scope) and
engine/step_cancel.go:113 (EVERY DESCENDANT scope). ADR-0162 moved it.
=> The draft's citation is wrong in file, function and scope. Today the interrupting
fire retires every arm of the scope's WHOLE SUBTREE, not just "that scope".

Command: go test -run '^TestProbeC18InterruptingRetiresSiblingNonInterrupting$' -v ./engine/ (EXIT=0)
OBSERVED:
  C18 before: arms=2 [{EnclosingScopeID:sc1 EventSubprocessNode:esp NonInterrupting:false ... Signal:boom ...} {EnclosingScopeID:sc1 EventSubprocessNode:espni NonInterrupting:true ... Signal:boom ...}]
  C18 fire(interrupting) -> err=<nil> cmds=1
  C18 after: arms=0 []
  C18 re-resolve by signal 'boom' -> <nil>
Consequence: the OUTCOME the ADR relies on holds - both arms retired, the
non-interrupting sibling is unreachable afterwards. The ADR must re-cite
engine/step_cancel.go:101/:113 (via cancelScopeSubtree) and widen the wording from
"that scope" to "that scope and every descendant scope".

## C19 — VERDICT: CONFIRMED (executed)

Draft: an interrupting boundary consumes its host and cancels that host's sibling
arms via cancelTokenWaits.
Found: fireBoundaryArm engine/step_boundaries.go:95; the interrupting branch is
:135-150, cancelTokenWaits at :146.

Command: go test -run '^TestProbeC19BoundaryConsumesHostAndCancelsSiblings$' -v ./engine/ (EXIT=0)
OBSERVED:
  C19 model.Validate = <nil>
  C19 before: tokens=1 boundaries=3
  C19 fire(interrupting bnd-int) -> err=<nil>
  C19 after: tokens=1 boundaries=0 []
  C19 host token still present: false
  C19   token id=p19-t1 node=after-int state=1
  C19   cmd engine.CancelTimer {TimerID:tm-9}
  C19   cmd engine.InvokeAction {CommandID:p19-c1 Name:after-int-action ...}
  C19 re-resolve boundaryArmBySignal('boom') = <nil>
Fixture guard: 3 arms on the SAME host - interrupting signal, NON-interrupting
signal, and a TIMER arm - so the assertion can distinguish "all siblings retired"
from "only the fired one". Host token h1 is gone; all three arms retired;
CancelTimer emitted for the timer sibling.
Consequence: the ADR's justification for mandatory RE-RESOLUTION holds exactly.

## C21 — VERDICT: SPLIT. Invariant CONFIRMED (executed, discriminating). The
##        "a test pins that invariant" claim is REFUTED.

Draft: "This is only correct because no drive-reachable code writes s.Variables;
a test pins that invariant."

(a) THE PINNING TEST DOES NOT EXIST.
    grep over engine/ and docs/ for the invariant by phrasing found nothing.
    Searching by behaviour instead: the only three test files mentioning mergeVars
    are engine/step_events_test.go (:441-443, :1202), engine/step_terminal_test.go
    (:1273) and engine/step_terminal_policy_test.go (:98). All three pin the
    mergeVars-ON-NO-MATCH behaviour ("no-match signal must not mutate instance
    variables"), which is a DIFFERENT invariant: it constrains handleSignalReceived,
    not drive. Nothing in the repo pins "drive does not write s.Variables".
    => the ADR asserts a safety net that is not there.

(b) THE INVARIANT ITSELF HOLDS over the whole reachable corpus.
    Writers of s.Variables in non-test engine code (grep, existence claim):
      engine/step_state.go:319   (mergeVars itself)
      engine/step_state.go:363   (cloneState)
      engine/step_compensation.go:796  s.Variables = copyVars(s.StartVariables)
      engine/step_compensation.go:801  s.Variables = copyVars(plan.restoreVars)
      engine/step_triggers.go:331-335  _errorMessage / _errorAttempts
    The two compensation writes are guarded by plan.resetVars / plan.restoreVars,
    which are set ONLY on walkReverse and on a partial rollback with
    restoreTargetVars (step_compensation.go:697, :712) - i.e. only from an explicit
    Reverse trigger, never from a drive-started (walkThrow) walk. The
    step_triggers.go writes are in a trigger handler.
    ⚠ Reachability by reading is exactly what Premise Discipline forbids, so it was
    EXECUTED: `drive` was temporarily wrapped so that it panics if fmt.Sprint(s.Variables)
    differs across the call, and the container-free corpus was run.
    Commands / OBSERVED:
      go test ./engine/...                       -> ENGINE_EXIT=0  (ok engine 0.542s)
      go test ./processtest/... ./service/... ./runtime/{calllink,signal,task}/... -> EXIT=0
    DISCRIMINATION CHECK (the probe is not vacuous): adding
    `s.Variables["__c21_discriminator"] = 1` after the inner call produced
      MUTATED_EXIT=1
      --- FAIL: TestDrive_MissingNodePark_LogsWarn
      panic: PROBE C21 VIOLATION: drive wrote s.Variables: map[] -> map[__c21_discriminator:1]
      at engine/step.go:229 (drive), from engine/observability_noop_test.go:374
    engine/step.go was then restored from a snapshot; `git diff -- engine/step.go`
    is EMPTY and `go test ./engine/` is EXIT=0.
Consequence: keep the invariant sentence, DELETE (or replace) "a test pins that
invariant". If the ADR wants the safety net it claims, the delivery must ADD the
test - and the drive-wrapper above is the shape that provably discriminates. Note
also the invariant is empirical over the exercised corpus, not a proof; phrasing it
as an ASSUMPTION with the probe recorded would be honest.

## C22 — VERDICT: CONFIRMED, and the test CAN FAIL (two independent REDs)

Draft: TestNonInterruptingBoundarySignalNoSelfCascade (engine/step_events_test.go:1157)
still exists and passes unchanged.
Found: engine/step_events_test.go:1157 - line number UNCHANGED (the only draft
reference in my set that did not move). Fixture
nonInterruptingBoundarySignalSelfCascadeDef at :1130-1149.

FIXTURE AUDIT (not just assertion text): the fixture DOES declare the construct
under test - event.NewBoundary("bnd-pulse","work", WithSignalName("pulse"),
WithBoundaryNonInterrupting()) at :1136, whose outgoing flow f-bnd-catch targets
event.NewIntermediateCatch("inner-catch", WithSignalName("pulse")) at :1138. The
self-cascade it guards against is genuinely constructible. Not a Boundaries-empty-
with-no-boundary-node vacuity.

Baseline: go test -run '^TestNonInterruptingBoundarySignalNoSelfCascade$' -v ./engine/
  EXIT=0 ; === RUN / --- PASS (so it RAN, not "no tests to run")

MUTATION 1 - drop the tier-4 snapshot (engine/step_triggers.go:804,
`for _, tokenID := range snapshotIDs` -> re-scan `s.tokenIDsAwaitingSignal(t.Name)`):
  MUTATED_EXIT=1
  step_events_test.go:1180: "[{i1-t1 work  1 i1-h1 ...}]" should have 2 item(s), but has 1
  Messages: host + inner-catch token must exist
  --- FAIL: TestNonInterruptingBoundarySignalNoSelfCascade

MUTATION 2 - make the non-interrupting boundary fire twice (duplicate
`s.placeTokenInScope(flowTarget, hostScopeID, at)` in fireBoundaryArm's
non-interrupting branch, engine/step_boundaries.go:162):
  MUT2_EXIT=1
  ... should have 2 item(s), but has 3   (i1-t2 and i1-t3 both at inner-catch)
  --- FAIL

Both mutations COMPILED and both DISCRIMINATED. Both files restored from snapshots;
`git status --short` shows no tracked modification.
Consequence: the ADR may keep citing this test - it is a real guard for BOTH halves
of the new design (the token snapshot AND one-fire-per-arm-identity). Line ref :1157
is still correct.

## C23 — VERDICT: source-CONFIRMED for the no-self-exclusion premise;
##        ASSUMPTION (unverified) for the 2^n amplification

Draft: performThrowSignal publishes to the signal bus with NO self-exclusion
(runtime/processdriver_action.go:490) - the basis for the claimed 2^n amplification.

Found (existence/location claim, source-verified): performThrowSignal is declared at
runtime/processdriver_action.go:486; the publish is EXACTLY at :490 -
  if err := driver.sigbus.Publish(ctx, cmd.Name, cmd.Payload); err != nil {
Line number UNCHANGED. The whole function is :486-494 and contains only a nil-bus
guard, the Publish, and a return.
runtime/signal/signalbus.go:154 `func (b *SignalBus) Publish(ctx context.Context,
name string, payload map[string]any) error` takes NO originating-instance argument,
and its body (:154-184) delivers to EVERY id in b.waiters[name] in sorted order.
Self-exclusion is not merely absent - it is not EXPRESSIBLE at this call site
without an interface change. Premise holds.

⚠ NOT EXECUTED: runtime/ as a whole is Docker-gated (main_test.go and friends import
internal/dbtest), so the amplification itself was not run. I mark it
ASSUMPTION (unverified): that two non-interrupting arms each throwing "x" compound
to 2^n deliveries and outbox events. The structural precondition is verified (the
arms stay armed => the instance stays subscribed => Publish re-delivers to it), but
the growth rate, and whether any outbox/dedup layer damps it, were not measured.
The ADR states 2^n as fact; it should either be executed or hedged.

⚠ NEW, unrecorded asymmetry found while verifying this: performThrowSignal calls
driver.sigbus.Publish DIRECTLY, whereas the external entry point
ProcessDriver.BroadcastSignal (runtime/processdriver_signal.go:32-64) ALSO walks
signalStartDefs and calls createAtNode for each signal-start hit. So an in-definition
ThrowSignal does NOT create signal-start instances while an external BroadcastSignal
does. This bounds the ADR's blow-up to existing waiters (no instance explosion) and
is worth one line in the Consequences.

## C24 — VERDICT: CONFIRMED (both quotes verbatim) - but the draft's LINKS are BROKEN

(a) ADR-0124 Decision item 3. Real file: docs/adr/0124-repeatable-noninterrupting.md
    lines 54-57, VERBATIM:
      "3. **Per-delivery-once preserved:** each trigger delivery fires each matching arm at most once
         (handlers call the fire function once per delivery; by-name lookup returns the first match), so
         a single delivery still spawns exactly one parallel path — `TestNonInterruptingBoundarySignalNoSelfCascade`
         holds. Repeatability is *across* deliveries."
    The draft's paraphrase is accurate.

(b) ADR-0154 Consequences. Real file:
    docs/adr/0154-signal-waiters-include-boundary-and-gateway-arms.md lines 111-130,
    VERBATIM (opening and closing):
      "- **Not addressed here, and important: one delivery still fires only the FIRST
         matching arm per family.** `handleSignalReceived`
         (`engine/step_triggers.go`) dispatches steps 1–3 with singular lookups
         (`armedEventBySignal` / `boundaryArmBySignal` /
         `eventTriggeredSubprocessArmBySignal`, each returning the first match), while
         step 4 loops over every matching token. ..."
      "... It is recorded rather than fixed because correcting it means
         snapshot-then-loop across three arm families, with the same
         re-arm-during-delivery hazard the token path already snapshots against — a
         behaviour change with its own blast radius that deserves its own ADR and
         audit rather than being folded into this one."
    Both halves confirmed; 0154 does record the gap as knowingly left open, and even
    prescribes snapshot-then-loop, which is what 0158 does.

BROKEN LINKS in the draft (checked with test -f against docs/adr/):
  MISS  0124-repeatable-non-interrupting-boundaries-and-event-subprocesses.md
        -> real: 0124-repeatable-noninterrupting.md
  OK    0161-stale-token-command-filtering.md
  MISS  0162-interrupt-cancels-descendant-scopes.md
        -> real: 0162-scope-teardown-cascades-to-descendants.md
Also: the draft's header note "ADR numbers 0155–0157 remain reserved" is still TRUE
(0155/0156/0157 do not exist on main), but 0158 is ALSO still free - the numbering
premise survives.

⚠⚠ INHERITED FALSE CLAIM (the class CLAUDE.md warns about). ADR-0124 Decision item 4
(0124-repeatable-noninterrupting.md:62-63) states:
      "a lingering arm in a terminal snapshot is harmless (`fireEventTriggeredSubprocessArm` is
       status-guarded to no-op on a non-`Running` instance)"
My C17 probe REFUTES this as written: the status guard applies to the ROOT scope only.
A non-root ESP arm fires into a completed instance and emits a live InvokeAction
(C17b output above). ADR-0158 amends ADR-0124 item 3; the audit should decide whether
it must also correct item 4, and must NOT restate 0124's harmlessness sentence.

## Summary

| # | Claim (short) | Verdict |
|---|---|---|
| C13 | removeArmsWhere detaches the array; stale pointer keeps the retired arm intact | CONFIRMED (refs MOVED; hazard UNDERSTATED) |
| C14 | resolveGatewayWin removes EVERY arm of the gateway | CONFIRMED (ref MOVED :266->:271) |
| C15 | Gateway token not consumed => byte-identical ABA re-arm | CONFIRMED (needs a merge-gateway loop; the naive loop is Validate-rejected) |
| C16 | armedEvent has no NonInterrupting; the other two do | CONFIRMED (compile-verified) |
| C17 | ESP status check is ROOT SCOPE ONLY | CONFIRMED as fact; its STATED CONSEQUENCE REFUTED; new Compensating gap |
| C18 | Interrupting ESP fire retires every arm of that scope | CONFIRMED in outcome; MECHANISM + SCOPE citation WRONG |
| C19 | Interrupting boundary consumes host, cancels siblings | CONFIRMED |
| C20 | Validate allows dup node ids / dup flows => colliding identities | CONFIRMED and UNDERSTATED (dup FLOW IDS too) |
| C21 | No drive-reachable code writes s.Variables; "a test pins that invariant" | Invariant CONFIRMED (executed+discriminating); the TEST CLAIM is REFUTED |
| C22 | TestNonInterruptingBoundarySignalNoSelfCascade exists, passes, CAN fail | CONFIRMED (2 mutations, 2 REDs) |
| C23 | performThrowSignal publishes with no self-exclusion | source-CONFIRMED; 2^n = ASSUMPTION (unverified) |
| C24 | ADR-0124 item 3 and ADR-0154 Consequences say what the draft says | CONFIRMED verbatim; draft's LINKS broken; 0124 item 4 is an INHERITED FALSE claim |

## Most dangerous findings (ranked)

1. C21 - "a test pins that invariant" is FALSE. The ADR leans a correctness argument
   (mergeVars merge-once) on a safety net that does not exist. The invariant is true
   today but nothing stops the next change from breaking it silently.
2. C24 / C17 - ADR-0124 item 4's "status-guarded to no-op on a non-Running instance"
   is FALSE for non-root scopes, and ADR-0158 is amending exactly that ADR. An
   inherited, restated false claim is the documented failure mode here.
3. C17 - the ADR's justification for the per-iteration status re-check is REFUTED
   (endInstance drains all three families, so re-resolution alone suffices) while a
   REAL gap it never considers - StatusCompensating, non-terminal, set at four sites
   OUTSIDE endInstance, draining nothing - goes unaddressed. The ADR argues for the
   guard it does not need and omits the case it does.
4. C18 - the cited mechanism moved FILE and WIDENED (cancelScopeSubtree retires the
   whole descendant subtree). An implementer following the ADR would look in
   step_eventsubprocess.go and find nothing.
5. C15 - the ABA is real but only through a merge-gateway loop; the obvious direct
   loop is Validate-rejected. Without that detail a reviewer can wrongly delete the
   already-resolved-gateway guard as dead code.
6. C13 - the pointer hazard also silently DROPS WRITES to surviving arms, not just
   reads of removed ones.
7. C20 - Validate also accepts duplicate FLOW IDS, and de-dup on (GatewayToken,
   CatchNode) discards the loser's distinct Flow.
8. C23 - the 2^n figure is unexecuted; and ThrowSignal vs BroadcastSignal differ on
   signal-start creation, which the ADR does not mention.
9. Stale citations across the board: state_arms.go:256-268 -> :262-274;
   armBySignal :219 -> :225; step_gateways.go:266 -> :271;
   step_eventsubprocess.go:148-159 -> :159-170; :207 -> step_cancel.go:101/:113;
   handleSignalReceived :655-757 -> :728+ (comment :671-676 -> :744-749);
   two BROKEN ADR filename links. Only step_events_test.go:1157 and
   processdriver_action.go:490 survived unchanged.

## Hygiene
All probe files (engine/zz_probe_b*_test.go) DELETED; engine/step.go,
engine/step_triggers.go and engine/step_boundaries.go restored from snapshots.
`git status --short` shows only the untracked 0158-*.md and FINDINGS-B.md.
`go test ./engine/...` FINAL_ENGINE_EXIT=0.
