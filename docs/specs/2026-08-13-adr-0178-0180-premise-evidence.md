# Recon C — the four pre-existing defects the ADR-0175 audit opened

Worktree: `/Users/zakyalvan/Documents/RND/wrkflw/.claude/worktrees/agent-a58c34227e95eea81`
HEAD: `12c9d7e3 docs: codify that docs/specs holds a delivery's evidence records too` — tree CLEAN. Bundle present.
Date of run: 2026-08-13. Docker NOT used; everything below is in container-free `engine`.

All probes lived in a throwaway `engine/zz_recon_probe_test.go` (`package engine_test`), deleted at the end.

---

## DEFECT (0) — TimerInWait reminder on a `spawnsNewWork()==false` instance — **CONFIRMED**

### The claim
> "A `TimerInWait` reminder fired on a `spawnsNewWork()==false` instance emits a real `InvokeAction`" —
> an ADR-0172 hole in `handleTimerFired` **path 4**, "reachable via a throw walk carrying `PendingCancel`".

### Verdict
**CONFIRMED, and UNDERSTATED.** The reminder path is one of **three** unguarded path-4 handlers, and the
`TimerDeadline` one is strictly worse than the reminder the handover names.

### The code

`engine/step_triggers.go:497` `handleTimerFired`. The path-4 record switch:

```go
// engine/step_triggers.go:528-540
rec := s.timerByID(t.TimerID)
if rec != nil {
    switch rec.Kind {
    case TimerDeadline:            // :532
        return handleDeadlineFired(ctx, def, s, *rec, t.OccurredAt(), resolvePolicy(opt))
    case TimerInWait:              // :534
        return handleReminderFired(def, s, *rec)
    case TimerRetry:               // :536
        return handleRetryFired(ctx, def, s, *rec, t.OccurredAt(), resolvePolicy(opt))
    case TimerCompensationStall:   // :538
        return handleCompensationStallFired(s, *rec, t.OccurredAt())
    }
}
```

The only `!s.spawnsNewWork()` guard in this function is at **`engine/step_triggers.go:564`**, which is
**36 lines BELOW** the switch — it protects path 5 (standalone-intermediate token fall-through) only.
Every one of the four `return`s above jumps over it.

- `handleReminderFired` — `engine/step_timers.go:163`. Emits via `emitFireOnceAction`
  (`engine/step_cancel.go:121`). **No `spawnsNewWork` check anywhere in the function** (source-verified:
  `grep -n spawnsNewWork engine/step_timers.go` yields only the comment at :118).
- `handleDeadlineFired` — `engine/step_timers.go:24`. Same: no check.
- `handleRetryFired` — `engine/step_timers.go:253`. Same: no check.
- `handleCompensationStallFired` — `engine/step_timers.go:132`. No check either, but it is **safe by
  construction**: it emits `Commands: nil` on both return paths and only appends an `Incident`. That is
  exactly what `engine/step_compensation_stall_incident_test.go:12-16` already says in a comment:
  *"⚠ Path 4 is NOT inherently safe on a dying instance: a TimerInWait reminder was measured emitting a
  real InvokeAction from there."* — i.e. the ADR-0175 delivery **already knew** and left it open.

### EXECUTED — reminder (the claimed defect)

Fixture: parallel fork, branch A = UserTask with `WithWaitAction(EveryExpr("1h"), "remind")` +
`WithWaitDeadline(AfterExpr("3h"), "escalate")` + `WithDeadlineAction("notify")`; branch B =
`event.NewCompensateThrow("compThrow", WithCompensateRef("ref1"))` with `ArchivedCompensations["ref1"]`
pre-seeded. `StartInstance` parks the task (arming both timers) and starts a **throw** walk; a
`CancelRequested` then arrives mid-walk and is **deferred** (`engine/step_triggers.go:192`
`s.PendingCancel = true`), which is precisely the "throw walk carrying `PendingCancel`" route.

```
$ go test -count=1 -run '^TestReconDefect0ReminderOnDyingInstance$' -v ./engine/... ; echo "EXIT=$?"
EXIT=0
=== RUN   TestReconDefect0ReminderOnDyingInstance
STEP 1 — StartInstance: status=compensating pendingCancel=false spawnsNewWork=true
  commands: 4 command(s)
    [0] engine.ScheduleTimer {TimerID:recon-0-tm1 Token:recon-0-t2 ... Kind:TimerDeadline}
    [1] engine.ScheduleTimer {TimerID:recon-0-tm2 Token:recon-0-t2 ... Kind:TimerInWait}
    [2] engine.AwaitHuman {TaskID:recon-0-h1 Eligibility:{Roles:[manager] ...}}
    [3] InvokeAction{Name:"undo1" CommandID:"recon-0-c1" FireAndForget:false}
  reminder timer id = "recon-0-tm2"
STEP 2 — CancelRequested mid-walk: status=compensating pendingCancel=true spawnsNewWork=false openTasks=1
  commands: 0 command(s)
STEP 3 — TimerFired(reminder) on spawnsNewWork=false instance:
  commands: 1 command(s)
    [0] InvokeAction{Name:"remind" CommandID:"recon-0-c2" FireAndForget:true}
  post-state: status=compensating pendingCancel=true spawnsNewWork=false
--- PASS
```

**Observable difference**: on an instance the engine has already decided is dying
(`spawnsNewWork()==false`, a deferred cancel pending), a live `InvokeAction{Name:"remind"}` is handed to
the consumer's runtime and a reminder email/webhook goes out. Path 5, one screen further down, would
have returned `Commands: nil` for the same instance.

**IMPRECISION in the handover, in the engine's favour**: the reminder action is `FireAndForget: true`.
So it is a *real dispatched side effect*, but it is **not** the ADR-0172 failure mode quoted at
`engine/step_triggers.go:550-553` ("whose `ActionCompleted` then lands on an instance the in-flight
rollback has already terminated") — nothing comes back. Severity: unwanted external side effect, not
state corruption.

### EXECUTED — deadline (NOT in the handover; strictly worse)

Same fixture, same dying state, firing the `TimerDeadline` record instead:

```
$ go test -count=1 -run '^TestReconDefect0DeadlineOnDyingInstance$' -v ./engine/... ; echo "EXIT=$?"
EXIT=0
=== RUN   TestReconDefect0DeadlineOnDyingInstance
STEP 2 — deferred cancel: status=compensating pendingCancel=true spawnsNewWork=false
  tokens before: {id:recon-0d-t2 node:userTask state:1 await:recon-0d-h1}
STEP 3 — TimerFired(DEADLINE) on spawnsNewWork=false instance:
  commands: 3 command(s)
    [0] InvokeAction{Name:"notify" CommandID:"recon-0d-c2" FireAndForget:true}
    [1] engine.UpdateTask {Task:{TaskID:recon-0d-h1 ... State:cancelled ...}}
    [2] engine.CancelTimer {TimerID:recon-0d-tm2}
  tokens after:  (none)
  post-state: status=compensating openTasks=0
--- PASS
```

**Observable difference**: the deadline breach not only dispatched `notify`, it **advanced the dying
instance's live branch** — the token moved off `userTask` down the `escalate` flow to `escalateEnd` and
was consumed (`tokens before: 1 → tokens after: 0`), and the open human task was cancelled. That is real
token-state mutation on an instance a deferred cancel is about to terminate, i.e. exactly the class
ADR-0172 exists to stop. Any fix for (0) that only patches `handleReminderFired` leaves this behind.

`handleRetryFired` was not executed on a dying instance (constructing a mid-walk in-flight retry is a
larger fixture). **ASSUMPTION (unverified):** by source inspection it has no guard either and its
`reinvokeServiceAction` emits a **non**-fire-and-forget `InvokeAction`, so it would be the one path that
does reproduce the quoted ADR-0172 "ActionCompleted lands on a terminated instance" failure.

### RE-COUNT: paths in `handleTimerFired` — **5 numbered, 6 terminal outcomes**

The function's own comment (`engine/step_triggers.go:498-503`) numbers **5** dispatch paths. Source-verified:

| # | What | Site | `spawnsNewWork` guard? | Timer kinds that reach it |
|---|---|---|---|---|
| 1 | event-based-gateway arm | inside `dispatchArmCascade`, `engine/step_triggers.go:512-521` | **YES** — `engine/step_arm_dispatch.go:54` | `TimerIntermediate` (armed at `engine/step_nodes.go:988`) |
| 2 | boundary-event arm | same call | **YES** — same guard | `TimerIntermediate` (`engine/step_boundaries.go:54`) |
| 3 | event-sub-process arm | same call | **YES** — same guard, **plus** a second at `engine/step_eventsubprocess.go:172` | `TimerIntermediate` (`engine/step_eventsubprocess.go:97`) |
| 4 | timer-record switch | `engine/step_triggers.go:528-540` | **NO** | `TimerDeadline`, `TimerInWait`, `TimerRetry`, `TimerCompensationStall` — **4** cases |
| 5 | standalone intermediate token | `engine/step_triggers.go:542-582` | **YES** — `engine/step_triggers.go:564` | `TimerIntermediate` (`engine/step_nodes.go:831`), token parks on the TimerID |

Terminal outcomes = 1 (cascade match) + 4 (record kinds) + 1 (token fall-through) = **6**, plus the
`tok == nil` clean no-op at `:548` and three `return StepResult{}, err` error exits.

Paths 1–3 and 5 are all `TimerIntermediate` and all guarded. **Path 4 is the ONLY unguarded path, and it
is the only path whose timers carry a record in `s.Timers`.** So the guard/no-guard split is exactly the
record/no-record split.

`s.Timers` writers — **4** production sites, source-verified
(`grep -n "Timers = append" engine/*.go` minus tests; `engine/step_state.go:395` is a snapshot copy, not
a writer of new records):

- `engine/step_nodes.go:705` → `TimerInWait`
- `engine/step_nodes.go:778` → `TimerDeadline`
- `engine/step_triggers.go:334` → `TimerRetry`
- `engine/step_compensation.go:478` → `TimerCompensationStall`

So `TimerIntermediate` is **never** written into `s.Timers` by this engine, and the path-4 switch's
missing `default` (fall-through to path 5) is unreachable from engine-written state — only from a
hand-crafted or foreign-persisted snapshot rehydrated through `engine/step_state.go:395`.

### RE-COUNT: callers of `spawnsNewWork()` — **6 production sites** (+1 test export)

1. `engine/step_arm_dispatch.go:54` — in `dispatchArmCascade` (covers timer, signal and message arm families).
2. `engine/step_triggers.go:564` — in `handleTimerFired`, path-5 token fall-through.
3. `engine/step_triggers.go:939` — in `handleSignalReceived`, ahead of each fan-out tier.
4. `engine/step_triggers.go:957` — in `handleSignalReceived`, inside the parked-token resume loop.
5. `engine/step_triggers.go:1139` — in `handleMessageReceived`, standalone-message token resume.
6. `engine/step_eventsubprocess.go:172` — in `fireEventTriggeredSubprocessArm`.

Plus `engine/export_test.go:141` (`SpawnsNewWork`, test-only export). **There is no site in
`engine/step_timers.go`** — that file mentions `spawnsNewWork` only in the comment at :118 explaining
that the stall handler runs *ahead* of the guard.

### Blast radius

Callers of the defective path: `handleTimerFired` is reached only from `engine/step.go:235`
(`dispatch` → `Step`). Fix is **entirely engine-internal**: add `if !s.spawnsNewWork() { return
StepResult{State: *s, Commands: nil}, nil }` either ahead of the path-4 switch (covers all four kinds —
but would **regress ADR-0175**, whose stall incident deliberately fires on dying walks) or inside
`handleReminderFired`/`handleDeadlineFired`/`handleRetryFired` individually. **No exported signature
changes.** The stall handler must be excluded whichever way it is done — `engine/step_compensation_
stall_incident_test.go:88-102` (`TestStallIncidentIsRaisedOnADyingWalk`) pins the opposite behaviour.

---

## DEFECT (1) — `ADR-0034 §2.5` citation — **IMPRECISE (the defect is real, the diagnosis is off)**

### The claim
> "`engine/step_triggers.go:291` cites a nonexistent `ADR-0034 §2.5`" — that ADR has no numbered
> sections and the real contract is **Decision 4**. Doc-only.

### Verdict
**IMPRECISE.** Both factual halves are TRUE and the line number has NOT rotted. But the handover
frames it as an *invented* section number; it is not. `§2.5` is a **real, exact-content-matching
section of the sibling SPEC**. This is a wrong-document attribution, not a fabricated citation — which
makes the fix unambiguous rather than a judgement call.

### The code — CURRENT line is still **291** (no rot)

```go
// engine/step_triggers.go:288-291, inside handleActionFailed (declared at :288... actually :288 is the
// func; the comment block is :288-291)
	// Best-effort compensation: if the engine is compensating and the failed
	// command is the active compensation action, skip that record and advance
	// the walk rather than re-entering propagateError/retry (ADR-0034 §2.5).
```

Verified current: `grep -n "ADR-0034" engine/step_triggers.go` →
```
224:		// Compensation walk before termination (ADR-0034). The harvest above is what
291:	// the walk rather than re-entering propagateError/retry (ADR-0034 §2.5).
```

### EXECUTED

```
$ grep -c "§" docs/adr/0034-compensation-on-error-cancel.md ; echo "EXIT=$?"
0
EXIT=1
$ grep -n "2\.5" docs/adr/0034-compensation-on-error-cancel.md ; echo "EXIT=$?"
EXIT=1                       (no match — no "2.5" anywhere in the ADR)
$ grep -n "^[0-9]\. " docs/adr/0034-compensation-on-error-cancel.md
21:1. **Parametrize the walk's terminal outcome** ...
26:2. **Extract `beginCompensation(...)`** ...
30:3. **Wire CancelRequested and propagateError-terminal** ...
32:4. **Best-effort compensation:** an `ActionFailed` matching the cursor's `ActiveCmdID` while
```

ADR-0034's headings are `## Context` / `## Decision` / `## Consequences` /
`## Post-acceptance fix (2026-06-23): idempotent re-cancel` — **no numbered sections**, **zero `§`**.
Its Decision list has exactly **4** items; item **4** (line 32) is the best-effort-skip contract the
comment describes. Handover: RIGHT on both counts.

### What the handover MISSES

```
$ grep -n "^###" docs/specs/2026-06-23-compensation-on-error-cancel-design.md
...
108:### 2.5 Compensation-action failure is best-effort
```

`docs/specs/2026-06-23-compensation-on-error-cancel-design.md:108` **§2.5** reads:

> "When `Status == StatusCompensating` and an `ActionFailed` arrives whose `CommandID ==
> Compensating.ActiveCmdID`, route it to **advance** (skip the failed record, continue the walk) — a
> failed compensation must not re-enter `propagateError`/retry or strand the instance."

That is the comment's sentence, near-verbatim. So the author cited the **spec's** §2.5 and typed the
**ADR's** number. The correct fix is one of two exact strings, not a guess:
`(ADR-0034 Decision 4)` or `(0034 spec §2.5)`.

### RE-COUNT: is this the only offender?

`grep -rn "§" --include="*.go" .` returns **25** `§` citations across the repo. **`engine/step_triggers.go:291`
is the ONLY one that attaches a `§` to an ADR that has no `§`.** Every other Go-code `§` either points at a
`docs/specs/*.md` (which do use `###` numbering) or at an ADR that genuinely contains `§`
(ADR-0165, 0173, 0174, 0176 all appear in `grep -rlc "§" docs/adr/`). So this is a lone slip, not a pattern.

The repo already uses the CORRECT form at **4** other Go sites — corroborating that `Decision 4` is the
house convention for this contract:
- `engine/step_compensation.go:521` — "(best-effort skip, ADR-0034 Decision 4)"
- `engine/trigger.go:920` — "ADR-0034 Decision 4's best-effort…"
- `engine/step_compensation_stall_verbs_test.go:60` — "ADR-0034 Decision 4's best-effort skip"
- `engine/step_compensation_stall_incident_test.go:140` — "ADR-0034 Decision 4 makes a…"

### Blast radius
**Zero.** It is a `//` comment on a line whose code is `if s.Status == StatusCompensating && ...`.
One-line doc edit, no test, no signature, no behaviour. Cheapest possible fix; worth folding into
whatever bundle next touches `engine/step_triggers.go`.
---

## DEFECT (2) — `StartInstance` accepted on a `compensating` instance — **CONFIRMED, but the framing is WRONG in two ways**

### The claim
> "`StartInstance` is accepted on a `compensating` instance and **restarts it from the top** with a stale
> cursor. Reachable through `engine.Step`/`ApplyTrigger`, **not `Drive` (which fails with
> `ErrInstanceExists`)**. On a resuming walk it also leaves `PendingCancel=true`."

### Verdict
**CONFIRMED as a defect** — every quoted consequence reproduces. But the diagnosis is wrong twice, and
both corrections widen the bug:

1. **It does not "restart from the top".** It **superimposes a second start** on the live instance:
   the pre-existing tokens, human tasks and armed timers all SURVIVE, and a fresh start token is added
   alongside them. Tokens 1 → 3, tasks 1 → 2, in one step.
2. **It is not specific to `compensating`.** `StartInstance` is accepted on **any non-terminal**
   instance — `running` included, measured below. `compensating` is merely the most damaging case.
   And `Drive`'s refusal is a **store-level id-uniqueness** check, not a status check: it refuses to
   re-start a `running` instance identically. So the `Drive`-vs-`ApplyTrigger` contrast is real but
   has nothing to do with the walk.

### The code

`engine/step_triggers.go:28` `handleStartInstance` — **the whole function, no guard**:

```go
func handleStartInstance(ctx context.Context, def *model.ProcessDefinition, s *InstanceState, t StartInstance, opt StepOptions) (StepResult, error) {
	s.Status = StatusRunning     // :29 — unconditional
	s.StartedAt = t.OccurredAt() // :30 — unconditional
	...
	s.placeToken(startID, t.OccurredAt())   // :43 — ADDS a token; s.Tokens is never cleared
```

It never inspects the incoming `Status`, never clears `s.Tokens`/`s.Tasks`/`s.Timers`, never resets
`s.Compensating`, never clears `s.PendingCancel`.

The only upstream gate is `dispatch`'s terminal guard at `engine/step.go:173`
(`if sp.Status.IsTerminal()`), whose own comment says **"StatusCompensating is NOT terminal, so
in-flight compensation walks are unaffected."** `StartInstance.terminalPolicy()` is `rejectWithError`
(`engine/trigger.go:115`) — so the trigger IS refused on terminated/failed/completed and admitted on
`running` and `compensating`.

### EXECUTED — engine half (`engine.Step`)

Fixture as for defect (0): a throw walk with a live parked user task in a parallel branch.

```
$ go test -count=1 -run '^TestReconDefect2StartInstanceOnCompensating$' -v ./engine/... ; echo "EXIT=$?"
EXIT=0
A) RESUMING throw walk, PendingCancel=false
   BEFORE: status=compensating startedAt=2026-06-21T10:00:00Z pendingCancel=false
           tokens={id:recon-2-t2 node:userTask state:1 await:recon-2-h1}
           cursor={... ResumeNode:joinGW ... ActiveCmdID:recon-2-c1 ...}
           vars=map[v:1] startVars=map[v:1] tasks=1
   Step(StartInstance) err = <nil>
   AFTER:  status=running startedAt=2026-06-21T11:00:00Z pendingCancel=false
           tokens={id:recon-2-t2 node:userTask ...} {id:recon-2-t5 node:userTask ...} {id:recon-2-t6 node:joinGW ...}
           cursor={... ActiveCmdID:recon-2-c1 ...}
           vars=map[restart:true v:1] startVars=map[restart:true v:1] tasks=2
     commands: 3 command(s)
    [0] ScheduleTimer{TimerID:recon-2-tm3 Kind:TimerDeadline}
    [1] ScheduleTimer{TimerID:recon-2-tm4 Kind:TimerInWait}
    [2] AwaitHuman{TaskID:recon-2-h2 ...}
           cursor UNCHANGED across the restart? true

B) same walk after a DEFERRED cancel, PendingCancel=true
   BEFORE: status=compensating pendingCancel=true tokens=1 tasks=1
   Step(StartInstance) err = <nil>
   AFTER:  status=running  pendingCancel=true tokens=3 tasks=2   <- PendingCancel SURVIVES onto a "running" instance
           cursor UNCHANGED across the restart? true
```

Observable difference, item by item:
- **caller is told nothing**: `err = <nil>`.
- `status` `compensating` -> `running`; `StartedAt` overwritten (10:00 -> 11:00).
- **tokens 1 -> 3**, **tasks 1 -> 2**, **two extra timers armed** — the old parked `userTask` token
  `recon-2-t2` and its task `recon-2-h1` are STILL THERE next to the new `recon-2-t5`/`recon-2-h2`.
  A single human task became two.
- `StartVariables` are overwritten with the restart's vars — the audit record of what the process was
  started with is destroyed.
- the compensation cursor is **byte-identical** (`cursor UNCHANGED == true`) and still names
  `ActiveCmdID: recon-2-c1` on an instance whose status is now `running`.
- **B) `PendingCancel=true` survives onto a `running` instance** — exactly as the handover says.

### EXECUTED — the consequence of the stale cursor

```
$ go test -count=1 -run '^TestReconDefect2OrphanedCompensationCommand$' -v ./engine/... ; echo "EXIT=$?"
EXIT=0
in-flight compensation command = "recon-2b-c1"
CONTROL  ActionCompleted(recon-2b-c1) on compensating: err=<nil> status=running
DEFECT   ActionCompleted(recon-2b-c1) after restart:   err=workflow-engine: no token awaiting command: workflow-engine: invalid state transition: "recon-2b-c1"
         errors.Is(err, engine.ErrTokenNotFound) = true
```

The worker that is still running `undo1` reports back and is now rejected with `ErrTokenNotFound`
(-> `ErrInvalidTransition` -> HTTP 422), because `handleActionCompleted`'s compensation route
(`engine/step_triggers.go:84`) is gated on `s.Status == StatusCompensating`, which the restart cleared.
The control row proves the same trigger succeeds without the restart.

### EXECUTED — runtime half: `ApplyTrigger` accepts, `Drive` refuses

```
$ go test -count=1 -run '^TestReconDefect2DriveVsApplyTrigger$' -v ./processtest/... ; echo "EXIT=$?"
EXIT=0
seeded snapshot: status=compensating tokens=0 cursor.ActiveCmdID="i1-c3"
ApplyTrigger(StartInstance) on compensating: err=<nil>
   -> status=failed tokens=0 vars=map[restart:true] cursor.ActiveCmdID="" pendingCancel=false
   -> PERSISTED status=failed tokens=0 cursor.ActiveCmdID=""
Drive(same id) on compensating:            err=workflow-runtime: commit: workflow-runtime: instance already exists
   errors.Is(err, kernel.ErrInstanceExists) = true
   -> returned status=running tokens=1
   -> PERSISTED status=compensating tokens=0 cursor.ActiveCmdID="i1-c3" (UNCHANGED?)
CONTROL Drive(existing RUNNING id):        err=workflow-runtime: commit: workflow-runtime: instance already exists (ErrInstanceExists=true)
CONTROL ApplyTrigger(StartInstance) on RUNNING: err=<nil> tokens=2
```

Both halves of the handover's contrast reproduce:
- `ProcessDriver.ApplyTrigger(..., NewStartInstance(...))` -> **`err=<nil>`, ACCEPTED and PERSISTED**.
  Here the restart drove into service task `a` with no action registered, so the instance ended
  `failed` and the compensation cursor was wiped (`ActiveCmdID "i1-c3"` -> `""`) — the in-flight
  `undoB` can never be answered.
- `ProcessDriver.Drive(same id)` -> `errors.Is(err, kernel.ErrInstanceExists) == true` and the
  **persisted snapshot is untouched** (still `compensating`, still `i1-c3`). Clean refusal.

**The two CONTROL rows are the correction to the handover.** `Drive` refuses an existing **running**
id with the same `ErrInstanceExists` — its guard is `MemInstanceStore.Create`'s
`if _, exists := m.instances[...]` (`runtime/kernel/memstore.go:97`, mirrored by
`internal/persistence/store/store_core.go:99`), reached because `Drive` calls
`deliverLoop(..., create=true, ...)` at `runtime/processdriver.go:448`. And `ApplyTrigger` accepts
`StartInstance` on a plain **running** instance too, tokens 1 -> 2. So the accurate statement is:

> `StartInstance` is accepted on any NON-TERMINAL instance and adds a second start,
> not "`StartInstance` is accepted on a compensating instance".

### RE-COUNT: entry points that can start an instance — **7**, of which **2** are unguarded

Derived from `grep -rn "NewStartInstance" --include="*.go" .` (non-test) plus the exported surfaces:

| # | Entry point | Site | Guard against an already-`compensating` (or `running`) instance |
|---|---|---|---|
| 1 | `engine.Step` + `NewStartInstance` / `NewStartInstanceAtNode` | `engine/step.go:122` -> `engine/step_triggers.go:28` | **NONE.** Only `dispatch`'s terminal guard (`engine/step.go:173`), and `StatusCompensating`/`StatusRunning` are not terminal. |
| 2 | `runtime.ProcessDriver.ApplyTrigger` | `runtime/processdriver.go:533` | **NONE.** Takes an arbitrary `engine.Trigger`; `deliverLoop(..., create=false, ...)` -> no uniqueness check, no status check. |
| 3 | `runtime.ProcessDriver.Drive` | `runtime/processdriver.go:424`, trigger at `:448` | id-uniqueness only (`create=true` -> `Store.Create` -> `ErrInstanceExists`). Refuses `running` and `compensating` alike. |
| 4 | `runtime.ProcessDriver.createAtNode` (unexported) | `runtime/processdriver.go:475`, trigger at `:484` | same `create=true` guard. Reached from **3** public paths: `DeliverMessage` (`processdriver_message.go:99`), `BroadcastSignal` signal-start (`processdriver_signal.go:61`), timer-start fire (`timerops.go:445`). |
| 5 | `runtime.ProcessDriver.runChild` (unexported) | `runtime/processdriver_child.go:58`, trigger at `:60` | same `create=true` guard. Reached from the call-activity dispatch (`processdriver_action.go:580`). |
| 6 | `service.ProcessEngine.StartInstance` | `service/service.go:336` | **fresh id minted every call** (`e.idgen.NewID()` at `:341`) then `Drive` at `:345` — cannot target an existing instance. |
| 7 | `transport/http/httpcore.StartInstance` (-> gin/fiber/stdlib groups) | `transport/http/httpcore/endpoints.go:25` | delegates to #6; inherits its fresh-id guard. **No HTTP route accepts an arbitrary trigger** — `grep -rn "engine.Trigger" transport/ service/` finds only `service.deliverTaskTrigger` (`service/service.go:574`), which is internal and human-task-only. |

Not an entry point: `internal/persistence/store/trigger_codec.go:158` decodes a persisted
`StartInstance` from the journal (replay/audit), and `processtest.Harness.Start`
(`processtest/harness.go:232`) just calls `Drive`.

**Count: 7 entry points; 2 unguarded (#1, #2); 3 guarded by id-uniqueness (#3, #4, #5); 2 guarded by
fresh-id minting (#6, #7).**

### Blast radius

- The unguarded pair are both **public API**: `engine.Step` is THE library surface, and
  `ProcessDriver.ApplyTrigger` is documented as the general trigger entry point (the signal bus,
  calllink notifier and timer-fire callback all wire through it — `runtime/signal/signalbus.go:37`,
  `runtime/calllink/notifier.go:28`, `runtime/timerops.go:317`).
- The fix is **engine-internal and needs no signature change**: `handleStartInstance` (or `dispatch`)
  refuses an instance that has already been started. `StartInstance.terminalPolicy()` is already
  `rejectWithError`, so extending the refusal reuses the existing `ErrInstanceTerminal` shape — though
  a new sentinel (e.g. `ErrInstanceAlreadyStarted`) reads better and is additive.
- WARNING — compatibility to measure before fixing: whatever refuses a second `StartInstance` must not
  break the message/signal/timer start-dedup paths, which deliberately lean on `ErrInstanceExists`
  from `Store.Create` for their at-least-once no-op (`runtime/processdriver_message.go:100`,
  `runtime/chain/chainer.go:208`). Those go through `create=true` and never re-enter
  `handleStartInstance` on an existing snapshot, so an engine-level guard should be orthogonal — but
  that is the one thing to verify by execution.
---

## DEFECT (3) — `CancelInstance` reports success for a cancel that did nothing — **CONFIRMED**

### The claim
> "`CancelInstance` reports success for a cancel that did nothing."

### Verdict
**CONFIRMED**, and it reaches the HTTP surface as a **200**. The strongest case is not a harmless
idempotent re-cancel: it is a cancel **dropped** against an admin partial rollback, after which the
"cancelled" instance **resumes running**. The engine's own comment calls it an accepted limitation;
nothing propagates that to the caller.

### The code

`runtime/processdriver_cancel.go:17` — **3 return sites**, exactly one non-error, carrying no
"did anything happen" signal:

```
20: return engine.InstanceState{}, ErrDriverShuttingDown   // driver draining
28: return st, err                                          // applyTrigger failed
34: return st, nil                                          // SUCCESS — whatever the step did or did not do
```

`service/service.go:529` — **4 return sites**, one non-error:

```
532: return nil, fmt.Errorf("workflow-service: cancel instance: %w", err)          // resolveDefinition
535: return nil, fmt.Errorf("%w: instance %q is already terminal", ErrConflict, ...) // TERMINAL guard
539: return nil, fmt.Errorf("workflow-service: cancel instance: %w", err)          // driver error
541: return e.instance(def, st), nil                                               // SUCCESS
```

`transport/http/httpcore/admin_endpoints.go:116` maps that success to `http.StatusOK` verbatim.

The no-op originates in `engine/step_triggers.go:210`:

```go
// A TERMINAL (cancel/error/full-rollback) or admin PARTIAL-rollback (ToNode set)
// walk is already in flight. ...
// Limitation: a cancel racing an admin PARTIAL rollback is therefore dropped
// (the partial walk resumes at its ToNode) — a rare admin-debug edge accepted
// in exchange for the no-double-compensation guarantee.
return StepResult{State: *s, Commands: nil}, nil
```

The limitation is documented in the engine and **never surfaced to the caller** — no error, no
sentinel, no field, no log.

### EXECUTED

Fixture: two compensable service tasks complete, then `CompensateRequested(at, "a")` starts an admin
**partial** rollback (`ToNode: "a"`) whose `undoB` is in flight. Then a cancel arrives.

```
$ go test -count=1 -run '^TestReconDefect3CancelReportsSuccessForNothing$' -v ./processtest/... ; echo "EXIT=$?"
EXIT=0
PRECONDITION: partial rollback in flight — status=compensating activeCmd="i1-c3" toNode="a" tokens=0
              dispatched compensation "undoB" as "i1-c3"

engine.Step(CancelRequested) on a partial walk: err=<nil> commands=0
   status compensating -> compensating ; pendingCancel false -> false ; tokens 0 -> 0
   state byte-identical to before the cancel? true
   after the walk finishes: status=running tokens=1  <-- the 'cancelled' instance is ALIVE

ProcessDriver.CancelInstance on a partial walk: err=<nil>
   returned status=compensating tokens=0 activeCmd="i1-c3"
   PERSISTED status=compensating activeCmd="i1-c3" (unchanged from the seed? true)

service.CancelInstance (the HTTP 200 path): err=<nil>
   ProcessInstance.State().Status = compensating  (HTTP would answer 200)
CONTRAST service.CancelInstance on a TERMINAL instance: err=workflow-service: conflicting state: instance "i1" is already terminal
2026/08/13 19:28:52 WARN trigger rejected on terminal instance instance_id=i1 trigger=engine.CancelRequested status=terminated outcome=dropped
CONTRAST ProcessDriver.CancelInstance on a TERMINAL instance: err=<nil> status=terminated
```

**The success return alongside the evidence that nothing changed** — all three layers:

| layer | result | did anything change? |
|---|---|---|
| `engine.Step` | `err=<nil>`, **0 commands** | **`state byte-identical to before the cancel? true`** — a `%+v` comparison of the whole `InstanceState` |
| `runtime.ProcessDriver.CancelInstance` | `err=<nil>` | persisted snapshot unchanged: still `compensating`, still `activeCmd="i1-c3"` |
| `service.ProcessEngine.CancelInstance` | `err=<nil>` -> HTTP **200** + `InstanceView` | same |

And the damage: `after the walk finishes: status=running tokens=1`. The operator who cancelled gets a
200, and the instance goes back to **running**.

### There are TWO "success for nothing" routes — only one is harmful

1. **Dropped against a terminal / admin-partial walk in flight** (`engine/step_triggers.go:210`).
   Reaches `service` and HTTP as a 200 because `compensating` is not terminal, so the
   `service/service.go:534` `isTerminal` guard does not fire. **This is the real defect.**
2. **Silent no-op on an already-terminal instance** (`CancelRequested.terminalPolicy() ==
   rejectSilently`, `engine/trigger.go:833`; dropped at `engine/step.go:207`). Deliberate idempotency,
   and it at least emits `WARN trigger rejected on terminal instance … outcome=dropped` (captured in
   the run above). The `service` layer refuses it outright with `ErrConflict`, so it never reaches HTTP
   — but `ProcessDriver.CancelInstance` (public API) still answers `err=<nil> status=terminated`.

A third, adjacent case worth naming: the **deferred** cancel (`engine/step_triggers.go:196`, sets
`PendingCancel=true` on a resuming walk) also returns `nil` while the instance is still alive. That one
did record intent, so it is "success for something that has not happened yet" rather than "success for
nothing" — measured in the defect-(0) probe, where `CancelRequested` returned `0 commands` and left the
instance `compensating`.

### RE-COUNT: `handleCancelRequested` return sites — **5**

`engine/step_triggers.go:125-273`:

```
196: return StepResult{State: *s, Commands: cmds}, nil   // DEFERRED (resuming walk): PendingCancel=true, instance alive
210: return StepResult{State: *s, Commands: nil}, nil    // DROPPED (terminal/partial walk in flight): pure no-op
234: return StepResult{}, err                            // beginCompensation failed
250: return res, nil                                     // compensation walk started (real work)
272: return StepResult{State: *s, Commands: cmds}, nil   // immediate termination (real work)
```

**5 sites: 1 error, 2 that do real work, 2 that return `nil` without terminating the instance.**
Plus a 6th route to a `nil` answer that never enters the handler at all: `dispatch`'s
`rejectSilently` arm at `engine/step.go:207` for a terminal instance.

### Blast radius

- Reaches the **public HTTP admin surface** (`transport/http/{stdlib,gin,fiber}` all route
  `DELETE`/cancel through `httpcore.CancelInstance` at `transport/http/httpcore/admin_endpoints.go:116`
  — `transport/http/stdlib/groups.go:268`, `transport/http/gin/groups.go:297`,
  `transport/http/fiber/groups.go:286`).
- A truthful answer requires **signalling from the engine outward**, so it is NOT purely internal:
  `StepResult` carries `State` + `Commands` only, and every layer above infers "it worked" from
  `err == nil`. Options, cheapest first:
  1. **Non-breaking**: return a distinguishable error (e.g. `ErrCancelDeferred` /
     `ErrCancelNotApplicable`) from the `:210` site, and let `service` map it to `ErrConflict` (409).
     `CancelRequested.terminalPolicy()` is `rejectSilently` precisely so `propagateCancel`'s child loop
     can swallow errors (`engine/trigger.go:821-824`), and that loop already logs-and-continues
     (`runtime/processdriver_cancel.go:80-88`) — so a new error here would be absorbed there by design.
     **This must be measured, not assumed.**
  2. **Breaking**: add an outcome field to `StepResult`. Touches every handler.
  3. **Observability-only**: emit the same `WARN … outcome=dropped` line the terminal arm already
     emits, and leave the API contract alone. Closes the "invisible" half, not the "lied to" half.
- Whichever is chosen, the `:196` deferred site and the `:210` dropped site are semantically different
  ("will terminate later" vs "will not terminate at all") and should not be collapsed into one answer.

---

## Appendix — full probe sources (deleted from the worktree after the run)

Preserved verbatim **inline below**, in this appendix — there are no separate `probe-*.txt`
sidecar files; these fenced blocks are the whole record.

### engine/zz_recon_probe_test.go (package engine_test)

```go
package engine_test

// THROWAWAY RECON PROBE — not a deliverable. Delete before any commit.

import (
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/kartaladev/wrkflw/definition/activity"
	"github.com/kartaladev/wrkflw/definition/event"
	"github.com/kartaladev/wrkflw/definition/flow"
	"github.com/kartaladev/wrkflw/definition/gateway"
	"github.com/kartaladev/wrkflw/definition/model"
	"github.com/kartaladev/wrkflw/definition/schedule"
	"github.com/kartaladev/wrkflw/engine"
)

// reconReminderThrowDef:
//
//	start → forkGW (parallel)
//	          ├─ userTask (reminder every 1h → action "remind"; deadline 3h → "escalate")
//	          └─ compThrow (CompensateRef "ref1")
//	        both → joinGW → end
func reconReminderThrowDef() *model.ProcessDefinition {
	return &model.ProcessDefinition{
		ID: "recon-reminder-throw", Version: 1,
		Nodes: []model.Node{
			event.NewStart("start"),
			gateway.NewParallel("forkGW"),
			activity.NewUserTask("userTask",
				activity.WithEligibleRoles("manager"),
				activity.WithWaitDeadline(schedule.AfterExpr(`"3h"`), "escalate"),
				activity.WithDeadlineAction("notify"),
				activity.WithWaitAction(schedule.EveryExpr(`"1h"`), "remind"),
			),
			event.NewCompensateThrow("compThrow", event.WithCompensateRef("ref1")),
			gateway.NewParallel("joinGW"),
			event.NewEnd("end"),
			event.NewEnd("escalateEnd"),
		},
		Flows: []flow.SequenceFlow{
			{ID: "f-start-fork", Source: "start", Target: "forkGW"},
			{ID: "f-fork-ut", Source: "forkGW", Target: "userTask"},
			{ID: "f-fork-th", Source: "forkGW", Target: "compThrow"},
			{ID: "f-ut-join", Source: "userTask", Target: "joinGW"},
			{ID: "f-th-join", Source: "compThrow", Target: "joinGW"},
			{ID: "f-join-end", Source: "joinGW", Target: "end"},
			{ID: "escalate", Source: "userTask", Target: "escalateEnd"},
		},
	}
}

func reconTimerID(t *testing.T, cmds []engine.Command, kind engine.TimerKind) string {
	t.Helper()
	for _, c := range cmds {
		if st, ok := c.(engine.ScheduleTimer); ok && st.Kind == kind {
			return st.TimerID
		}
	}
	t.Fatalf("no ScheduleTimer of kind %v in %v", kind, cmds)
	return ""
}

func reconDumpCmds(label string, cmds []engine.Command) {
	fmt.Printf("  %s: %d command(s)\n", label, len(cmds))
	for i, c := range cmds {
		switch v := c.(type) {
		case engine.InvokeAction:
			fmt.Printf("    [%d] InvokeAction{Name:%q CommandID:%q FireAndForget:%v}\n", i, v.Name, v.CommandID, v.FireAndForget)
		default:
			fmt.Printf("    [%d] %T %+v\n", i, c, c)
		}
	}
}

// TestReconDefect0ReminderOnDyingInstance probes handover backlog item 0.
func TestReconDefect0ReminderOnDyingInstance(t *testing.T) {
	def := reconReminderThrowDef()
	at := time.Date(2026, 6, 21, 10, 0, 0, 0, time.UTC)

	st := engine.InstanceState{
		InstanceID: "recon-0",
		ArchivedCompensations: map[string][]engine.CompensationRecord{
			"ref1": {{NodeID: "act1", Action: "undo1", CompletedAt: at}},
		},
	}

	r1, err := engine.Step(t.Context(), def, st, engine.NewStartInstance(at, nil), engine.StepOptions{})
	require.NoError(t, err)
	fmt.Printf("STEP 1 — StartInstance: status=%v pendingCancel=%v spawnsNewWork=%v\n",
		r1.State.Status, r1.State.PendingCancel, engine.SpawnsNewWork(&r1.State))
	reconDumpCmds("commands", r1.Commands)

	reminderID := reconTimerID(t, r1.Commands, engine.TimerInWait)
	fmt.Printf("  reminder timer id = %q\n", reminderID)

	// Cancel arrives mid-walk on a RESUMING (throw) walk → deferred: PendingCancel=true.
	r2, err := engine.Step(t.Context(), def, r1.State,
		engine.NewCancelRequested(at.Add(30*time.Minute)), engine.StepOptions{})
	require.NoError(t, err)
	fmt.Printf("STEP 2 — CancelRequested mid-walk: status=%v pendingCancel=%v spawnsNewWork=%v openTasks=%d\n",
		r2.State.Status, r2.State.PendingCancel, engine.SpawnsNewWork(&r2.State), reconCountOpenTasks(r2.State))
	reconDumpCmds("commands", r2.Commands)

	// Now fire the in-wait reminder on the DYING instance.
	r3, err := engine.Step(t.Context(), def, r2.State,
		engine.NewTimerFired(at.Add(time.Hour), reminderID), engine.StepOptions{})
	require.NoError(t, err)
	fmt.Printf("STEP 3 — TimerFired(reminder) on spawnsNewWork=%v instance:\n", engine.SpawnsNewWork(&r2.State))
	reconDumpCmds("commands", r3.Commands)
	fmt.Printf("  post-state: status=%v pendingCancel=%v spawnsNewWork=%v\n",
		r3.State.Status, r3.State.PendingCancel, engine.SpawnsNewWork(&r3.State))
}

// TestReconDefect0DeadlineOnDyingInstance is the SAME fixture, firing the
// path-4 TimerDeadline record instead of the TimerInWait one.
func TestReconDefect0DeadlineOnDyingInstance(t *testing.T) {
	def := reconReminderThrowDef()
	at := time.Date(2026, 6, 21, 10, 0, 0, 0, time.UTC)

	st := engine.InstanceState{
		InstanceID: "recon-0d",
		ArchivedCompensations: map[string][]engine.CompensationRecord{
			"ref1": {{NodeID: "act1", Action: "undo1", CompletedAt: at}},
		},
	}
	r1, err := engine.Step(t.Context(), def, st, engine.NewStartInstance(at, nil), engine.StepOptions{})
	require.NoError(t, err)
	deadlineID := reconTimerID(t, r1.Commands, engine.TimerDeadline)

	r2, err := engine.Step(t.Context(), def, r1.State,
		engine.NewCancelRequested(at.Add(30*time.Minute)), engine.StepOptions{})
	require.NoError(t, err)
	fmt.Printf("STEP 2 — deferred cancel: status=%v pendingCancel=%v spawnsNewWork=%v\n",
		r2.State.Status, r2.State.PendingCancel, engine.SpawnsNewWork(&r2.State))
	fmt.Printf("  tokens before: %s\n", reconTokens(r2.State))

	r3, err := engine.Step(t.Context(), def, r2.State,
		engine.NewTimerFired(at.Add(3*time.Hour), deadlineID), engine.StepOptions{})
	require.NoError(t, err)
	fmt.Printf("STEP 3 — TimerFired(DEADLINE) on spawnsNewWork=false instance:\n")
	reconDumpCmds("commands", r3.Commands)
	fmt.Printf("  tokens after:  %s\n", reconTokens(r3.State))
	fmt.Printf("  post-state: status=%v openTasks=%d\n", r3.State.Status, reconCountOpenTasks(r3.State))
}

// TestReconDefect2StartInstanceOnCompensating probes handover backlog item 2,
// engine half: engine.Step accepts StartInstance on a compensating instance.
func TestReconDefect2StartInstanceOnCompensating(t *testing.T) {
	def := reconReminderThrowDef()
	at := time.Date(2026, 6, 21, 10, 0, 0, 0, time.UTC)
	st := engine.InstanceState{
		InstanceID: "recon-2",
		ArchivedCompensations: map[string][]engine.CompensationRecord{
			"ref1": {{NodeID: "act1", Action: "undo1", CompletedAt: at}},
		},
	}
	r1, err := engine.Step(t.Context(), def, st, engine.NewStartInstance(at, map[string]any{"v": 1}), engine.StepOptions{})
	require.NoError(t, err)

	fmt.Printf("A) RESUMING throw walk, PendingCancel=false\n")
	reconStartOnState(t, def, r1.State, at.Add(time.Hour), "recon-2")

	r2, err := engine.Step(t.Context(), def, r1.State,
		engine.NewCancelRequested(at.Add(30*time.Minute)), engine.StepOptions{})
	require.NoError(t, err)
	fmt.Printf("\nB) same walk after a DEFERRED cancel, PendingCancel=true\n")
	reconStartOnState(t, def, r2.State, at.Add(2*time.Hour), "recon-2")
}

// TestReconDefect2OrphanedCompensationCommand shows the consequence: after the
// restart, the compensation action that was in flight can no longer report back.
func TestReconDefect2OrphanedCompensationCommand(t *testing.T) {
	def := reconReminderThrowDef()
	at := time.Date(2026, 6, 21, 10, 0, 0, 0, time.UTC)
	st := engine.InstanceState{
		InstanceID: "recon-2b",
		ArchivedCompensations: map[string][]engine.CompensationRecord{
			"ref1": {{NodeID: "act1", Action: "undo1", CompletedAt: at}},
		},
	}
	r1, err := engine.Step(t.Context(), def, st, engine.NewStartInstance(at, nil), engine.StepOptions{})
	require.NoError(t, err)
	var undoCmdID string
	for _, c := range r1.Commands {
		if ia, ok := c.(engine.InvokeAction); ok && ia.Name == "undo1" {
			undoCmdID = ia.CommandID
		}
	}
	require.NotEmpty(t, undoCmdID)
	fmt.Printf("in-flight compensation command = %q\n", undoCmdID)

	// CONTROL: without the restart, the completion advances the walk.
	ctrl, cerr := engine.Step(t.Context(), def, r1.State,
		engine.NewActionCompleted(at.Add(time.Minute), undoCmdID, nil), engine.StepOptions{})
	fmt.Printf("CONTROL  ActionCompleted(%s) on compensating: err=%v status=%v\n", undoCmdID, cerr, ctrl.State.Status)

	// DEFECT: restart first, then the worker reports back.
	r2, err := engine.Step(t.Context(), def, r1.State,
		engine.NewStartInstance(at.Add(time.Hour), nil), engine.StepOptions{})
	require.NoError(t, err)
	_, aerr := engine.Step(t.Context(), def, r2.State,
		engine.NewActionCompleted(at.Add(time.Hour+time.Minute), undoCmdID, nil), engine.StepOptions{})
	fmt.Printf("DEFECT   ActionCompleted(%s) after restart:   err=%v\n", undoCmdID, aerr)
	fmt.Printf("         errors.Is(err, engine.ErrTokenNotFound) = %v\n", errors.Is(aerr, engine.ErrTokenNotFound))
}

func reconStartOnState(t *testing.T, def *model.ProcessDefinition, before engine.InstanceState, at time.Time, _ string) {
	t.Helper()
	fmt.Printf("   BEFORE: status=%v startedAt=%v pendingCancel=%v tokens=%s\n",
		before.Status, before.StartedAt.Format(time.RFC3339), before.PendingCancel, reconTokens(before))
	fmt.Printf("           cursor=%s\n", engine.CompensationCursorView(&before))
	fmt.Printf("           vars=%v startVars=%v tasks=%d timers-armed(open tasks)=%d\n",
		before.Variables, before.StartVariables, len(before.Tasks), reconCountOpenTasks(before))

	res, err := engine.Step(t.Context(), def, before,
		engine.NewStartInstance(at, map[string]any{"restart": true}), engine.StepOptions{})
	fmt.Printf("   Step(StartInstance) err = %v\n", err)
	if err != nil {
		return
	}
	after := res.State
	fmt.Printf("   AFTER:  status=%v startedAt=%v pendingCancel=%v tokens=%s\n",
		after.Status, after.StartedAt.Format(time.RFC3339), after.PendingCancel, reconTokens(after))
	fmt.Printf("           cursor=%s\n", engine.CompensationCursorView(&after))
	fmt.Printf("           vars=%v startVars=%v tasks=%d\n", after.Variables, after.StartVariables, len(after.Tasks))
	reconDumpCmds("   commands", res.Commands)
	fmt.Printf("           cursor UNCHANGED across the restart? %v\n",
		engine.CompensationCursorView(&before) == engine.CompensationCursorView(&after))
}

func reconTokens(s engine.InstanceState) string {
	out := ""
	for _, tok := range s.Tokens {
		out += fmt.Sprintf("{id:%s node:%s state:%v await:%s} ", tok.ID, tok.NodeID, tok.State, tok.AwaitCommand)
	}
	if out == "" {
		return "(none)"
	}
	return out
}

func reconCountOpenTasks(s engine.InstanceState) int {
	n := 0
	for _, task := range s.Tasks {
		if task.IsOpen() {
			n++
		}
	}
	return n
}
```

### processtest/zz_recon_probe_test.go (package processtest_test)

```go
package processtest_test

// THROWAWAY RECON PROBE — not a deliverable. Delete before any commit.

import (
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/kartaladev/wrkflw/definition/activity"
	"github.com/kartaladev/wrkflw/definition/event"
	"github.com/kartaladev/wrkflw/definition/flow"
	"github.com/kartaladev/wrkflw/definition/model"
	"github.com/kartaladev/wrkflw/engine"
	"github.com/kartaladev/wrkflw/runtime"
	"github.com/kartaladev/wrkflw/runtime/kernel"
)

func reconSagaDef() *model.ProcessDefinition {
	return &model.ProcessDefinition{
		ID: "recon-saga", Version: 1,
		Nodes: []model.Node{
			event.NewStart("start"),
			activity.NewServiceTask("a", activity.WithTaskAction("doA"), activity.WithCompensateAction("undoA")),
			activity.NewServiceTask("b", activity.WithTaskAction("doB"), activity.WithCompensateAction("undoB")),
			activity.NewReceiveTask("wait", "go"),
			event.NewEnd("end"),
		},
		Flows: []flow.SequenceFlow{
			{ID: "f1", Source: "start", Target: "a"},
			{ID: "f2", Source: "a", Target: "b"},
			{ID: "f3", Source: "b", Target: "wait"},
			{ID: "f4", Source: "wait", Target: "end"},
		},
	}
}

// reconCompensatingState builds a mid-walk compensating snapshot with the pure
// engine (the same construction processtest/park_compensation_stall_test.go uses).
func reconCompensatingState(t *testing.T, id string) (engine.InstanceState, *model.ProcessDefinition) {
	t.Helper()
	def := reconSagaDef()
	at := time.Date(2026, 8, 12, 9, 0, 0, 0, time.UTC)
	opt := engine.StepOptions{}

	res, err := engine.Step(t.Context(), def, engine.InstanceState{InstanceID: id},
		engine.NewStartInstance(at, nil), opt)
	require.NoError(t, err)
	for i, name := range []string{"doA", "doB"} {
		var cmdID string
		for _, c := range res.Commands {
			if ia, ok := c.(engine.InvokeAction); ok && ia.Name == name {
				cmdID = ia.CommandID
			}
		}
		require.NotEmpty(t, cmdID)
		res, err = engine.Step(t.Context(), def, res.State,
			engine.NewActionCompleted(at.Add(time.Duration(i+1)*time.Second), cmdID, nil), opt)
		require.NoError(t, err)
	}
	res, err = engine.Step(t.Context(), def, res.State,
		engine.NewCancelRequested(at.Add(time.Minute)), opt)
	require.NoError(t, err)
	require.Equal(t, engine.StatusCompensating, res.State.Status)
	return res.State, def
}

// TestReconDefect2DriveVsApplyTrigger is the runtime half of backlog item 2.
func TestReconDefect2DriveVsApplyTrigger(t *testing.T) {
	compensating, def := reconCompensatingState(t, "i1")
	fmt.Printf("seeded snapshot: status=%v tokens=%d cursor.ActiveCmdID=%q\n",
		compensating.Status, len(compensating.Tokens), reconActiveCmd(compensating))

	// ---- half 1: ApplyTrigger ACCEPTS StartInstance on the compensating instance.
	storeA, err := kernel.NewMemInstanceStore()
	require.NoError(t, err)
	_, err = storeA.Create(t.Context(), kernel.AppliedStep{State: compensating, Trigger: engine.NewStartInstance(time.Now(), nil)})
	require.NoError(t, err)
	driverA, err := runtime.NewProcessDriver(runtime.WithInstanceStore(storeA))
	require.NoError(t, err)

	outA, errA := driverA.ApplyTrigger(t.Context(), def, "i1", engine.NewStartInstance(time.Now(), map[string]any{"restart": true}))
	fmt.Printf("ApplyTrigger(StartInstance) on compensating: err=%v\n", errA)
	fmt.Printf("   -> status=%v tokens=%d vars=%v cursor.ActiveCmdID=%q pendingCancel=%v\n",
		outA.Status, len(outA.Tokens), outA.Variables, reconActiveCmd(outA), outA.PendingCancel)
	persisted, _, lerr := storeA.Load(t.Context(), "i1")
	require.NoError(t, lerr)
	fmt.Printf("   -> PERSISTED status=%v tokens=%d cursor.ActiveCmdID=%q\n",
		persisted.Status, len(persisted.Tokens), reconActiveCmd(persisted))

	// ---- half 2: Drive REFUSES the same id.
	storeB, err := kernel.NewMemInstanceStore()
	require.NoError(t, err)
	_, err = storeB.Create(t.Context(), kernel.AppliedStep{State: compensating, Trigger: engine.NewStartInstance(time.Now(), nil)})
	require.NoError(t, err)
	driverB, err := runtime.NewProcessDriver(runtime.WithInstanceStore(storeB))
	require.NoError(t, err)

	outB, errB := driverB.Drive(t.Context(), def, "i1", map[string]any{"restart": true})
	fmt.Printf("Drive(same id) on compensating:            err=%v\n", errB)
	fmt.Printf("   errors.Is(err, kernel.ErrInstanceExists) = %v\n", errors.Is(errB, kernel.ErrInstanceExists))
	fmt.Printf("   -> returned status=%v tokens=%d\n", outB.Status, len(outB.Tokens))
	persistedB, _, lerrB := storeB.Load(t.Context(), "i1")
	require.NoError(t, lerrB)
	fmt.Printf("   -> PERSISTED status=%v tokens=%d cursor.ActiveCmdID=%q (UNCHANGED?)\n",
		persistedB.Status, len(persistedB.Tokens), reconActiveCmd(persistedB))

	// ---- control: Drive also refuses a plain RUNNING instance — its guard is the
	// store's id uniqueness, not any status check.
	storeC, err := kernel.NewMemInstanceStore()
	require.NoError(t, err)
	driverC, err := runtime.NewProcessDriver(runtime.WithInstanceStore(storeC))
	require.NoError(t, err)
	sigDef := reconParkDef()
	_, err = driverC.Drive(t.Context(), sigDef, "i2", nil)
	require.NoError(t, err)
	_, errC := driverC.Drive(t.Context(), sigDef, "i2", nil)
	fmt.Printf("CONTROL Drive(existing RUNNING id):        err=%v (ErrInstanceExists=%v)\n",
		errC, errors.Is(errC, kernel.ErrInstanceExists))
	outD, errD := driverC.ApplyTrigger(t.Context(), sigDef, "i2", engine.NewStartInstance(time.Now(), nil))
	fmt.Printf("CONTROL ApplyTrigger(StartInstance) on RUNNING: err=%v tokens=%d\n", errD, len(outD.Tokens))
}

func reconParkDef() *model.ProcessDefinition {
	return &model.ProcessDefinition{
		ID: "recon-park", Version: 1,
		Nodes: []model.Node{
			event.NewStart("start"),
			activity.NewReceiveTask("wait", "go"),
			event.NewEnd("end"),
		},
		Flows: []flow.SequenceFlow{
			{ID: "f1", Source: "start", Target: "wait"},
			{ID: "f2", Source: "wait", Target: "end"},
		},
	}
}

func reconActiveCmd(s engine.InstanceState) string {
	return s.Compensating.ActiveCmdID
}
```

### processtest/zz_recon_probe3_test.go (package processtest_test)

```go
package processtest_test

// THROWAWAY RECON PROBE — not a deliverable. Delete before any commit.

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/kartaladev/wrkflw/action"
	"github.com/kartaladev/wrkflw/engine"
	"github.com/kartaladev/wrkflw/runtime"
	"github.com/kartaladev/wrkflw/runtime/kernel"
	"github.com/kartaladev/wrkflw/service"
)

// TestReconDefect3CancelReportsSuccessForNothing probes handover backlog item 3.
func TestReconDefect3CancelReportsSuccessForNothing(t *testing.T) {
	def := reconSagaDef()
	at := time.Date(2026, 8, 12, 9, 0, 0, 0, time.UTC)
	opt := engine.StepOptions{}

	// Drive a saga to two compensable records + a parked receive task.
	res, err := engine.Step(t.Context(), def, engine.InstanceState{InstanceID: "i1"},
		engine.NewStartInstance(at, nil), opt)
	require.NoError(t, err)
	for i, name := range []string{"doA", "doB"} {
		var cmdID string
		for _, c := range res.Commands {
			if ia, ok := c.(engine.InvokeAction); ok && ia.Name == name {
				cmdID = ia.CommandID
			}
		}
		require.NotEmpty(t, cmdID)
		res, err = engine.Step(t.Context(), def, res.State,
			engine.NewActionCompleted(at.Add(time.Duration(i+1)*time.Second), cmdID, nil), opt)
		require.NoError(t, err)
	}
	require.Equal(t, engine.StatusRunning, res.State.Status)

	// An ADMIN PARTIAL rollback (ToNode set) is now in flight.
	res, err = engine.Step(t.Context(), def, res.State,
		engine.NewCompensateRequested(at.Add(time.Minute), "a"), opt)
	require.NoError(t, err)
	partial := res.State
	fmt.Printf("PRECONDITION: partial rollback in flight — status=%v activeCmd=%q toNode=%q tokens=%d\n",
		partial.Status, partial.Compensating.ActiveCmdID, partial.Compensating.ToNode, len(partial.Tokens))
	var undoCmdID string
	for _, c := range res.Commands {
		if ia, ok := c.(engine.InvokeAction); ok {
			undoCmdID = ia.CommandID
			fmt.Printf("              dispatched compensation %q as %q\n", ia.Name, ia.CommandID)
		}
	}
	require.NotEmpty(t, undoCmdID)

	// --- Layer 1: engine.Step
	after, err := engine.Step(t.Context(), def, partial, engine.NewCancelRequested(at.Add(2*time.Minute)), opt)
	fmt.Printf("\nengine.Step(CancelRequested) on a partial walk: err=%v commands=%d\n", err, len(after.Commands))
	fmt.Printf("   status %v -> %v ; pendingCancel %v -> %v ; tokens %d -> %d\n",
		partial.Status, after.State.Status, partial.PendingCancel, after.State.PendingCancel,
		len(partial.Tokens), len(after.State.Tokens))
	fmt.Printf("   state byte-identical to before the cancel? %v\n",
		fmt.Sprintf("%+v", partial) == fmt.Sprintf("%+v", after.State))

	// The walk then finishes and the instance RESUMES RUNNING — the cancel is gone.
	resumed, rerr := engine.Step(t.Context(), def, after.State,
		engine.NewActionCompleted(at.Add(3*time.Minute), undoCmdID, nil), opt)
	require.NoError(t, rerr)
	fmt.Printf("   after the walk finishes: status=%v tokens=%d  <-- the 'cancelled' instance is ALIVE\n",
		resumed.State.Status, len(resumed.State.Tokens))

	// --- Layer 2: runtime.ProcessDriver.CancelInstance
	store, err := kernel.NewMemInstanceStore()
	require.NoError(t, err)
	_, err = store.Create(t.Context(), kernel.AppliedStep{State: partial, Trigger: engine.NewStartInstance(at, nil)})
	require.NoError(t, err)
	driver, err := runtime.NewProcessDriver(runtime.WithInstanceStore(store))
	require.NoError(t, err)
	drvOut, drvErr := driver.CancelInstance(t.Context(), def, "i1")
	fmt.Printf("\nProcessDriver.CancelInstance on a partial walk: err=%v\n", drvErr)
	fmt.Printf("   returned status=%v tokens=%d activeCmd=%q\n", drvOut.Status, len(drvOut.Tokens), drvOut.Compensating.ActiveCmdID)
	persisted, _, _ := store.Load(t.Context(), "i1")
	fmt.Printf("   PERSISTED status=%v activeCmd=%q (unchanged from the seed? %v)\n",
		persisted.Status, persisted.Compensating.ActiveCmdID,
		persisted.Compensating.ActiveCmdID == partial.Compensating.ActiveCmdID)

	// --- Layer 3: service.ProcessEngine.CancelInstance (what HTTP sees)
	reg := kernel.NewMemDefinitionRegistry()
	require.NoError(t, reg.Register(def))
	store2, err := kernel.NewMemInstanceStore()
	require.NoError(t, err)
	_, err = store2.Create(t.Context(), kernel.AppliedStep{State: partial, Trigger: engine.NewStartInstance(at, nil)})
	require.NoError(t, err)
	driver2, err := runtime.NewProcessDriver(
		runtime.WithInstanceStore(store2),
		runtime.WithDefinitions(reg),
	)
	require.NoError(t, err)
	svc, err := service.NewProcessEngine(
		service.WithProcessDriver(driver2),
		service.WithInstanceStore(store2),
		service.WithDefinitions(reg),
	)
	require.NoError(t, err)
	pi, serr := svc.CancelInstance(t.Context(), service.CancelInstanceRequest{InstanceID: "i1"})
	fmt.Printf("\nservice.CancelInstance (the HTTP 200 path): err=%v\n", serr)
	if pi != nil {
		fmt.Printf("   ProcessInstance.State().Status = %v  (HTTP would answer 200)\n", pi.State().Status)
	}

	// CONTRAST 1: on an already-TERMINAL instance the service layer DOES refuse.
	termState := partial
	termState.Status = engine.StatusTerminated
	store3, err := kernel.NewMemInstanceStore()
	require.NoError(t, err)
	_, err = store3.Create(t.Context(), kernel.AppliedStep{State: termState, Trigger: engine.NewStartInstance(at, nil)})
	require.NoError(t, err)
	driver3, err := runtime.NewProcessDriver(runtime.WithInstanceStore(store3), runtime.WithDefinitions(reg))
	require.NoError(t, err)
	svc3, err := service.NewProcessEngine(
		service.WithProcessDriver(driver3), service.WithInstanceStore(store3), service.WithDefinitions(reg))
	require.NoError(t, err)
	_, terr := svc3.CancelInstance(t.Context(), service.CancelInstanceRequest{InstanceID: "i1"})
	fmt.Printf("CONTRAST service.CancelInstance on a TERMINAL instance: err=%v\n", terr)

	// CONTRAST 2: the DRIVER (no service guard) succeeds on a terminal instance too.
	drvTerm, drvTermErr := driver3.CancelInstance(t.Context(), def, "i1")
	fmt.Printf("CONTRAST ProcessDriver.CancelInstance on a TERMINAL instance: err=%v status=%v\n",
		drvTermErr, drvTerm.Status)
	_ = context.Background
	_ = action.Action(nil)
}
```
