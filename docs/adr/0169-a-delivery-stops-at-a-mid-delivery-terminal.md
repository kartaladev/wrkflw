# 169. A trigger delivery stops dispatching when its own drive turns the instance terminal

- Status: Accepted
- Date: 2026-08-08

> Ships with [ADR-0168](0168-a-compensation-walk-blocks-completion.md) in one bundle: two
> defects of the same family with two different predicates.
> Design: `docs/specs/2026-08-08-compensation-walk-and-mid-delivery-terminal.md`.
> Plan: `docs/plans/2026-08-08-compensation-walk-and-mid-delivery-terminal.md`.

## Context

[ADR-0164](0164-terminal-transitions-are-one-path.md) closed **seven** resurrection routes.
[ADR-0165](0165-triggers-declare-their-terminal-policy.md) then replaced the eight hand-copied
`IsTerminal()` guard sites with one `terminalPolicy()`-driven guard at `dispatch` entry,
closing six further routes. **Every route closed by that entry guard is entry-level**: the
trigger arrives at an *already*-terminal instance.

⚠ Two precisions, because the obvious shorthand is wrong. First, *not* all of ADR-0164's seven
are entry-level — its first route is a **stale compensation cursor**, which is state rather
than a trigger arrival and is closed by `endInstance`'s cursor clear (`engine/state.go:356`),
nothing to do with `terminalPolicy()`. Second, there is **no single ordered list of routes** any
document maintains: ADR-0164 counts seven, ADR-0165's own Consequences says "six routes close",
and ADR-0165's amendment note in ADR-0164 counts "six … plus a seventh (`CancelRequested`)" —
a *different* set. So this ADR claims **a further route**, not "the eighth".

That further route is **structurally different**. The instance is `running` when
`dispatch` admits the trigger and goes terminal **between dispatch tiers**, where no entry
guard can see it. ADR-0164's own Consequences anticipated this: *"a list of routes closed,
not a proof that none remain."*

Reproduced by execution on `main` @ `7180114`, in **one** `SignalReceived{"x"}` delivery:

```
start → fork(parallel) ⇒
    branch A: taskA(user) [bndA interrupting, signal "x"] → errEnd(EndError "boom", uncaught)
    branch B: catchB(catch, signal "x") → taskB(user) [bndT timer boundary 60m] → endB
```

```
AFTER START    status=running tokens=2 boundaries=1
AFTER SIGNAL x status=failed  tokens=1 boundaries=1
  token id=i1-t3 node=taskB state=1               ← zombie token on a FAILED instance
  cmd UpdateTask{i1-h1 taskA cancelled}
  cmd FailInstance{Err:boom}                      ← instance dies here
  cmd UpdateTask{i1-h2 taskB cancelled}           ← record minted AFTER the terminal event
  cmd ScheduleTimer{i1-tm1 Token:i1-t3 …}         ← live timer for a dead instance
```

**Mechanism**, source-verified rather than inferred:

1. Tier 2 fires `bndA` → `drive` → uncaught `boom` → `handleUnhandledError`'s failFast branch
   → `endInstance(StatusFailed)`. That branch **deliberately does not drop tokens**
   (`handleUnhandledError`'s failFast branch, `engine/step_errors.go`, ADR-0164
   Decision 3), so branch B's token survives.
2. Tier 4 loops `snapshotIDs` — taken by `tokenIDsAwaitingSignal`, **before** tiers 1–3 —
   resumes `catchB`'s token and drives it into `taskB` on the dead instance, minting task
   `h2` and arming `bndT`.
3. `dropStaleTokenCommands` runs last. `liveAwaiters` returns an **empty** map for a terminal
   instance (`liveAwaiters`, `engine/step_stale_commands.go`), so `AwaitHuman{h2}` is
   stale → dropped, and
   because the record is still open it is cancelled — that is the post-`FailInstance`
   `UpdateTask`.
4. **`ScheduleTimer` is deliberately exempt from that filter** (`filterableCommand`,
   `engine/step_stale_commands.go`, ADR-0161), so it escapes to the runtime.

ADR-0161's filter is therefore **containing** the damage post-hoc, not preventing it. The
token still moved, a node visit was still recorded, a task record was minted and cancelled,
`s.Boundaries` still carries an arm on a failed instance, and the `ScheduleTimer` still
escapes.

**The exposure this ADR closes is confined to `handleSignalReceived`: it is the only handler
with multiple *arm*-dispatch points per delivery.** It has three arm-dispatch points plus one
per snapshotted token — at least four, and **unbounded in the number of parked tokens** (the
T6 fixture below measures five). Timer and message go through
`dispatchArmCascade` (`engine/step_arm_dispatch.go:28`), which is first-match-wins, and both
callers `return` immediately on `matched` (`handleTimerFired` and
`handleMessageReceived` in `engine/step_triggers.go`), so exactly
one dispatch point runs per delivery and nothing follows it.

## Decision

**`handleSignalReceived` re-checks `Status.IsTerminal()` between dispatch points and stops
the delivery when its own drive has made the instance terminal.**

**1. The predicate is `IsTerminal()`, not `!= StatusRunning`.** The premise-evidence file
proposed `!= StatusRunning` across all four tiers
(`docs/specs/2026-08-08-adr-0158-premise-evidence.md` §Q4c). That predicate is refuted by
[ADR-0168](0168-a-compensation-walk-blocks-completion.md), which measured it swallowing a
legitimate signal and stranding an instance forever. `IsTerminal()` excludes
`StatusCompensating`, so an in-definition compensation throw on one branch does not silence
arms on another — the case ADR-0168 exists to preserve.

The two ADRs are therefore complementary, not overlapping: `Compensating.ActiveCmdID` guards
*completion*; `IsTerminal()` guards *mid-delivery dispatch*. Neither is `!= StatusRunning`.

**2. Two guard sites, not four.** Tiers 1–3 are folded into a slice of lookup-and-fire
closures checked once per iteration, plus one check inside the tier-4 token loop:

```go
for _, tier := range tiers {
    if s.Status.IsTerminal() { return StepResult{State: *s, Commands: signalCmds}, nil }
    ...
}
for _, tokenID := range snapshotIDs {
    if s.Status.IsTerminal() { return StepResult{State: *s, Commands: signalCmds}, nil }
    ...
}
```

The hand-copied alternative needs **three** guards, not four — before tier 2, before tier 3,
and inside the tier-4 loop. A guard before tier 1 would be dead code, because `dispatch`'s
entry guard has already refused an instance that was terminal on arrival. Hand-copied guards
are the pattern ADR-0165 was written to eliminate, and its record is unambiguous: three
successive review passes found 1 → 2 → 5 instances of the same hand-copied defect, each
increase arriving after an ADR had claimed the class closed. A fourth arm family added later
inherits this guard instead of needing another copy.

> **AMENDED by [ADR-0158](0158-signal-fires-every-matching-arm.md) (2026-08-10).**
> The signal fan-out this paragraph anticipated has landed, and it changes the
> requirement rather than violating it. Tiers 1–3 are now **one closure per
> snapshotted arm identity**, and each closure **re-resolves its identity** before
> firing. What may not be hoisted is the RESOLUTION — the reason below still
> holds exactly. What IS now hoisted, deliberately, is the ENUMERATION: confining
> a delivery to the arms armed at the delivery instant is a second, independent
> correctness requirement (measured: without it, a later tier fires an arm an
> earlier tier's own drive created). Decision 2's structural argument is
> unaffected; the guard is still written once for all families, and now runs once
> per ARM rather than once per family.
>
> ⚠ ADR-0158 also replaces this ADR's `IsTerminal()` predicate at the same site
> with `spawnsNewWork()` (see
> [ADR-0172](0172-an-event-subprocess-arm-checks-instance-status.md)), because
> `IsTerminal()` is false for `StatusCompensating` and a tier's drive can begin a
> TERMINATING rollback mid-delivery.

⚠ **Each closure performs its own lookup at the moment it runs. The three lookups must NOT be
hoisted.** Today each tier's lookup runs *after* the previous tier's fire has mutated state.
Pre-resolving all three into variables is the refactor a slice literal invites, and **the suite
does not protect against it**: measured, hoisting the three lookups leaves `go test ./engine/`
at **EXIT=0**. It survives today only because all three fire functions happen to re-validate
before acting (`fireBoundaryArm` re-reads the host token, `resolveGatewayWin` re-reads the
gateway token, `fireEventTriggeredSubprocessArm` re-checks the scope) — an accident of three
independent implementations, not a stated invariant, and exactly what ADR-0158's fan-out will
disturb. This requirement carries a matching code comment above the slice.

🛑 **AMENDED DURING IMPLEMENTATION (2026-08-09) — only ONE of the two guard sites closes an
observable defect.** The tier-4 in-loop guard does. **The tiers-1–3 guard has no observable
exposure today**, and this ADR must not be read as claiming otherwise.

Measured by the controller, on the built tree, with this bundle's own five new regression
tests present:

| mutation | result |
|---|---|
| delete the **tiers-1–3** guard entirely | whole `engine` package **EXIT=0**, 0 failures |
| delete the **tier-4 in-loop** guard | **EXIT=1** — `TestSignalDeliveryStopsInsideTheTokenLoop` RED |

**Cause.** Every terminal transition routes through `endInstance` → `cancelAllScheduledWork`,
which drains `ArmedEvents`, `Boundaries` **and** every event-sub-process arm across all scopes.
By the time tiers 2 and 3 run their lookups there is nothing left to find, so a guard ahead of
them changes no output. The only dispatch point with genuine exposure is tier 4, whose loop
walks a **pre-taken token snapshot** and therefore owns no arm state for the drain to clear.

Executed with all three arm families armed and tier 1's win killing the instance, the output is
**byte-identical** on the guarded tree and on unpatched `main`, for two distinct terminal routes:

```
uncaught error    both trees: status=failed     boundaries=0 esp=0
                              cmds=[UpdateTask{i1-h1 cancelled}, FailInstance{boom}]
force-termination both trees: status=terminated boundaries=0 esp=0
                              cmds=[UpdateTask{i1-h1 cancelled}, FailInstance{gateway-terminates}]
```

**Decision 2's argument is unaffected** — it is *structural*, about where a guard must live so a
fourth arm family inherits it rather than needing a fifth hand-copy. That case does not depend
on the tiers-1–3 guard being individually load-bearing today. The tiers-1–3 guard is retained as
**defence in depth**, deliberately and with its status recorded, because the arm-drain it relies
on is a property of `cancelAllScheduledWork` that nothing states as an invariant — and the owed
event-sub-process ADR (see ADR-0168 Consequences) is expected to narrow exactly that drain.

⚠ This also means the plan's **T9 cannot fail today** and is a *pin*, not a falsifier. See the
Consequences section and spec §8.

**3. The check belongs INSIDE the tier-4 loop, not only ahead of it.** The loop is
multi-iteration, so a terminal transition can occur within it — token A drives into an
uncaught error and token B then resumes. A guard placed only ahead of the loop passes the
headline reproduction and still admits this one; the plan prescribes a test whose sole
purpose is to falsify that weaker placement.

**4. An aborted delivery returns its partial commands.**
`StepResult{State: *s, Commands: signalCmds}` — everything the earlier tiers legitimately
produced, including the `FailInstance` that made the instance terminal.

⚠ **Not** because dropping them would discard the terminal event — that reason was in this
ADR's first draft and is **false**. `terminalOutboxEvent` (`runtime/outbox.go`) is
**status-driven at the deliverLoop terminal edge, not derived from the terminal command**
(ADR-0046), and `runtime/processdriver_action.go:291` performs nothing post-commit for
`CompleteInstance`/`FailInstance`. `instance.failed` would still publish. The two real reasons:

- **The earlier tiers' task-store reconciliation would be lost.** In the measured
  reproduction that is `UpdateTask{taskA cancelled}` — dropping it leaves `taskA` open in the
  TaskStore against a failed instance, precisely the ADR-0089 defect.
- **The terminal event's error payload would degrade.** `terminalEventErr`
  (`runtime/outbox.go`) falls back to `st.Incidents[0].Error` and then to the literal
  `"instance failed"`, so `"boom"` would be lost.

No double-publish results: `terminalOutboxEvent` gates on `prevStatus.IsTerminal()` and one
`Step` produces one terminal edge — returning `FailInstance` alongside an already-`StatusFailed`
state is exactly what happens on `main` today.

**`mergeVars` keeps its merge-once semantics and does not move.** It runs inside the first tier
that *matches*, so an abort **after** a match returns state whose `Variables` carry the delivery
payload, and an abort **before** any match returns them untouched. The payload is merged by the
tier that legitimately fired, never by the abort. (Stated here because the spec delegated these
semantics to this ADR; leaving that open is the ADR-0162 zombie-scope shape.)

**5. No Warn is emitted on abort — adjudicated, not overlooked.** This is an owner decision,
recorded here rather than passed over in silence because it cuts against two neighbouring
precedents: ADR-0161 logs every dropped command, and ADR-0165 logs every rejected trigger,
both on the argument that a suppressed effect otherwise leaves no trace anywhere — no error,
no event, no history entry. The accepted cost is that *"why did my catch never fire?"* has no
log answer for this route.

⚠ The cost is larger than "no new log line": **this fix removes the one trace that exists
today.** On `main` the zombie `AwaitHuman` is dropped by `dropStaleTokenCommands`, which emits
`WARN dropping command whose awaiter this step cancelled … command_kind=AwaitHuman`. With the
guard applied that command is never produced, so the WARN disappears and nothing replaces it.
Measured — `main`: `WARN dropping command whose awaiter this step cancelled instance_id=i2
command_kind=AwaitHuman correlation_id=i2-h1`; patched: no WARN at all. The route goes from
one confusing-but-present log line to **completely silent**. Verified that no existing test
asserts on log output for this path, so nothing breaks. Revisit if it surfaces in support.

## Consequences

**Positive.**

- A terminal instance is no longer driven forward by the remainder of the delivery that killed
  it: no zombie token **movement** — no node visit on a dead instance, no task record minted
  after the terminal event, no boundary arm installed on a dead instance, and no
  `ScheduleTimer` escaping to arm a job for an instance that no longer exists. **The token
  itself still survives** (see Negative); it simply stays parked where it was. Measured on the
  headline reproduction: the surviving token stays at `catchB` with `AwaitSignal="x"` instead
  of being driven to `taskB`, and `Boundaries` goes 1 → 0.
  ⚠ An earlier draft of this bullet said "No zombie token", which its own Negative bullet
  three entries below refutes — the summary-sentence over-generalisation CLAUDE.md's Premise
  Discipline section names.
- ADR-0161's `liveAwaiters` terminal exclusion returns to being defence in depth for this
  route rather than the only thing limiting the blast radius.
- The guard is structural within `handleSignalReceived`: a new arm family inherits it.

**Negative / accepted.**

- **A silent stop** (Decision 5). An operator has no log record distinguishing "the signal
  matched nothing" from "the signal matched, and dispatch stopped because the instance died
  mid-delivery."
- **Tokens still survive the failFast branch.** This ADR stops them being *driven*; it does
  not change ADR-0164 Decision 3's deliberate choice to keep them, nor the incidents attached
  to them. A surviving token on a failed instance remains observable in committed state.
- Folding tiers 1–3 into a closure slice makes the three arm families structurally uniform,
  which is a readability change to a hot path. The tier ORDER (gateway → boundary →
  event-sub-process) and the broadcast semantics are unchanged; the order is load-bearing and
  pinned by existing tests.

**Explicitly NOT a proof that the class is closed.** This ADR closes one mid-delivery route,
found by executing one reproduction. ADR-0164 claimed completeness twice and was wrong both
times. The argument in Context bounds only the *arm-dispatch* handlers (timer and message
return immediately on first match); it is source-verified, not exhaustive.

Two things the bundle's audit established, recorded so the next sweep starts from a measured
baseline rather than a guess:

- **`drive` itself is a multi-iteration loop with no `IsTerminal()` check**
  (`engine/step.go`), present in *every* handler. Probed with a parallel fork whose first
  branch reaches an uncaught `EndError` while the second is still active, inside one
  `StartInstance`: `status=failed tokens=1`, `cmds=[FailInstance{boom}]` — branch B is never
  driven, because an error-behaviour end event exits the whole drive loop via `halt`
  (ADR-0127). **That `halt` is why this defect is signal-only**: only a handler with a
  *second* dispatch point after the drive is exposed. Note the residue — a `TokenActive`
  token on a failed instance — is blocked from resuming by ADR-0165's entry guard.
- Every other handler (`handleStartInstance`, `handleActionCompleted`/`Failed`,
  `handleHumanCompleted`, `handleSubInstanceCompleted`/`Failed`, `handleResolveIncident`,
  `handleCancelRequested`, `handleDeadlineFired`, `handleReminderFired`, `handleRetryFired`,
  `handleCompensateRequested`, `handleHumanClaimed`, `handleHumanCandidatesResolved`,
  `handleHumanReassigned`) was read and ends at a single
  `drive`/`endInstance`/`routeToBoundary` with no trailing dispatch. No counterexample was
  found — which is not the same as none existing.

  🛑 **AMENDED 2026-08-10 (gate).** The list originally said "every other handler" and then
  named **nine**, omitting the four now appended. The omitted four were read at the gate and
  the **conclusion survives** — none has a trailing dispatch — so this was a false quantifier
  rather than a missed defect. Recorded rather than silently corrected because this sentence
  *is* the stated bound for "no counterexample was found": a bound that under-counts what it
  covered is worth less than it appears. `CLAUDE.md`'s Premise Discipline names this exact
  shape — verify every *every*, and prefer naming a closed set over asserting one.

⚠ The Context bound's enumeration is **not** original to this ADR: `engine/step.go` already
carries the same list in ADR-0161's comment. That comment reasons about **command
destruction**, a different property from terminal transitions, so the enumeration is reused
here rather than inherited as a conclusion.

**Relationship to ADR-0158.** The future signal-fan-out ADR fires *every* matching arm per
family rather than the first, which multiplies the dispatch points inside one delivery and so
multiplies this exposure. This decision is an input to that rewrite; ADR-0158 must not
re-derive the predicate from the refuted `!= StatusRunning` shape.
