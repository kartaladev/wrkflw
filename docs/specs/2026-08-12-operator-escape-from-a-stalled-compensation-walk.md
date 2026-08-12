# An operator escape from a stalled compensation walk

**Status:** IMPLEMENTED. Audited (3 lenses, 3 Criticals, ~30 findings adjudicated — §9), then
built — with six design claims corrected by execution, each marked ⚠ **CORRECTED BY
IMPLEMENTATION** inline and listed in the plan's `▶ Progress`.
**Date:** 2026-08-12
**ADR:** [ADR-0175](../adr/0175-an-operator-escape-from-a-stalled-compensation-walk.md)
**Plan:** [2026-08-12-stalled-compensation-walk-escape](../plans/2026-08-12-stalled-compensation-walk-escape.md)
**Audit record:** [2026-08-12-adr-0175-audit-evidence.md](2026-08-12-adr-0175-audit-evidence.md)
**Base:** `main` at `5270838` (ADR-0174)

---

## 1. What this delivery is, in one sentence

A compensation walk whose dispatched action never reports back is **permanently stuck and
permanently invisible**; this delivery gives the engine an opt-in way to **notice** the
stall and gives an operator three explicit verbs — **retry**, **skip**, **abandon** — to get
the instance moving again.

⚠ **"Never reports back" excludes the default in-process path.** `performInvokeAction` wraps
every non-fire-and-forget invocation in `actionContextFor(actx, timeout)` with
`defaultActionTimeout` at 30 s (P17), and turns the failure into an `ActionFailed` that
`handleActionFailed` routes to `stepCompensationAdvance` — so that path **self-heals**. The
stall shapes that actually survive are: a driver crash between commit and reply; a lost
callback from an out-of-process worker; `WithActionTimeout(0)`; and an action that ignores
`ctx` (which hangs the caller rather than persisting a stalled state).

## 2. How this item was framed, and what execution added

`docs/plans/HANDOVER.md` carried this as *"a walk whose `ActionCompleted` never arrives
leaves the instance permanently stuck, and `CancelRequested` emits ZERO commands"*. Both
halves are true and were re-measured here (§4), then independently reproduced by two audit
lenses. Execution added three things the framing did not contain:

1. **The one lever that works is unreachable through the operator API.** Through the
   `service` / HTTP operator surface the escape count is **zero**: `CancelInstance` is a
   measured no-op here, `resolve-incident` has no incident to act on, and `ReverseInstance`
   is not on `service.Service` at all. The lever *is* reachable one level down, via the
   exported `ProcessDriver.ApplyTrigger` — but only for a caller that already holds the
   dispatched `CommandID` (its own out-of-process worker), and **not at all after a driver
   restart**, which is the stall shape this design assumes is most common.
2. **The cancel reports success.** `CancelInstance` returns `err=nil` and a state, so the
   caller is told the cancel worked while nothing happened.
3. **`StartInstance` is accepted on a compensating instance** and restarts it from the top —
   destructive, not an escape. See §4.4 and §8.

### 2.1 The mechanism, in one paragraph

There are **three** compensation dispatch sites (`grep compensationInvoke(`):
`beginCompensation`, `startCompensationWalk` (the compensation-throw walk's first dispatch,
in a different file), and `stepCompensationAdvance`. Each dispatches one compensation
`InvokeAction` and records its id in `s.Compensating.ActiveCmdID`. The walk then advances
**only** on a trigger carrying that id — `ActionCompleted` (advance) or `ActionFailed`
(best-effort skip, ADR-0034 Decision 4).

Nothing else in the instance state references the walk **when it was started by
`beginCompensation`**: its prologue cancels every token and every timer, so `tokens=0,
timers=0` (measured, §4.1). A throw walk started by `startCompensationWalk` consumes only
the throwing token and deliberately leaves siblings running, so its instance may still hold
tokens, timers and waiters — what it does *not* hold is anything that can advance **the
walk**. Either way, if neither trigger arrives, no clock and no scheduler can move it.
Exactly one operator action does move it — `ActionFailed` carrying the cursor's
`ActiveCmdID` — and one destroys it (`StartInstance`, §4.4).

## 3. Source-verified premises

Symbol names in preference to line numbers, which rot. Rows marked ⚡ were **executed** by
the audit rather than source-read.

| # | Claim | Where |
|---|---|---|
| P1 ⚡ | `handleTimerFired` path 4 (`s.timerByID` → `Kind` switch) runs **before** the `!s.spawnsNewWork()` guard, which only gates the path-5 token fall-through | `engine/step_triggers.go` |
| P2 | Paths 1–3 (`dispatchArmCascade`) match by TimerID against the gateway/boundary/event-sub arm tables only, so a TimerID in none of them falls through | same |
| P3 ⚡ | `spawnsNewWork()` returns `false` for `StatusCompensating` when `walkTerminates(s.PendingCancel)` — i.e. for `walkAdmin`, `walkReverse`, or a throw walk carrying `PendingCancel`; a `walkPartial` and an uncancelled throw stay live | `engine/state.go` |
| P4 ⚡ | `cancelTimersWhere` treats an empty key as naming no record (ADR-0152), so a `timerRecord` with `Token: ""` is never swept by token-keyed cleanup | `engine/state_timers.go` |
| P5 ⚡ | `removeOrphanedIncidents` **keeps** an incident with `TokenID: ""`, its comment calling itself *"a guard against the next terminal site rather than a live defect"* | `engine/state.go` |
| P6 | `removeIncidentsForToken("")` returns early (ADR-0152) | `engine/state.go` |
| P7 ⚡ | `handleResolveIncident` removes the incident **before** looking up `s.tokenByID`, and returns no commands when the token is nil. Measured on a `TokenID:""` incident: `err=<nil>, cmds=[], incidents=0` — it silently eats it | `engine/step_triggers.go` |
| P8 ⚡ | `consumeDispatchedRecord` drops from the **archive**, never from `cur.Records`, and **early-returns on `len(cur.Records) == 0`** | `engine/state_compensation.go` |
| P9 | `stepCompensationAdvance` routes `nextIdx >= len(records)` to `stepCompensationFinish` (ADR-0171's third disjunct). ⚠ It guards `NextIndex-1`, **not** `NextIndex` | `engine/step_compensation.go` |
| P10 ⚡ | `beginCompensation` calls `s.cancelAllTimers()`, which sets `s.Timers = nil` | `engine/step_compensation.go` |
| P11 ⚡ | `ScheduleTimer.Token` is **never read** — confirmed by renaming it: all eight sites are writes. `engine/state_arms.go` already documents an intentional `Token == ""` | `runtime/timerops.go` |
| P12 | `TimerFired.terminalPolicy()` is `rejectSilently` | `engine/trigger.go` |
| P13 | `StepOptions` carries engine-wide policy defaults. ⚠ **It is not a precedent for a flat knob**: `effectiveRetryPolicy` resolves a THREE-tier chain (`OverrideRetryPolicy` > node > `DefaultRetryPolicy`) precisely because one timeout for every action is wrong — see §5.1 | `engine/step.go`, `engine/step_state.go` |
| P14 | `TimerKind` today has four values, persisted as an integer | `engine/command.go` |
| P15 | The admin-trigger idiom is one type with several constructors (`CompensateRequested`) | `engine/trigger.go` |
| P16 | `service.Service` exposes `CancelInstance` and `ResolveIncident`; it does **not** expose reverse | `service/service.go` |
| P17 | `defaultActionTimeout` is 30 s and bounds each in-process action invocation, converting a hang into an `ActionFailed` that advances the walk — see §1 | `runtime/processdriver_options.go` |
| P18 ⚡ | **Measured:** `ProcessDriver.Drive` on an existing instance id fails — `workflow-runtime: commit: workflow-runtime: instance already exists`, `errors.Is(err, kernel.ErrInstanceExists) == true`. The failure comes from the commit inside `deliverLoop(..., create=true)`, which `Drive` calls **directly** (it does not route through `createAtNode`). `perform` runs only after a successful commit, so no side effect escapes | `runtime/processdriver.go` |

## 4. Executed evidence (scratch — numbers kept, probes deleted)

Throwaway probe files were written into `engine/`, run with `go test -count=1`, and deleted.
Fixture: `start → svcA(doA/undoA) → svcB(doB/undoB) → human(UserTask) → end`, driven to a
park on `human`, then `CancelRequested` to start a **terminal** walk. Resuming-walk rows
reuse `rootSagaWithScopeWideThrow`.

**Every number below was independently reproduced by two audit lenses.**

### 4.1 The stall state itself

```
PROBE[cancel-starts-walk] status=compensating cmds=2 [UpdateTask InvokeAction]
                          tokens=0 root=2 arch=0 scopes=0 pendingCancel=false
PROBE[cursor] activeCmdID="probe-1-c3" nextIndex=1
              finalStatus=terminated finalErr="cancelled"
PROBE[timers] timers=0 incidents=0 waiters(signal=[] msg=[])
```

`timers=0`, `incidents=0`, both waiter sets empty, `tokens=0`. **Nothing in the persisted
state can wake this instance.** The load-bearing measurement of the delivery.

### 4.2 Every operator lever against a stalled TERMINAL walk

| lever | measured result |
|---|---|
| 2nd `CancelRequested` | `err=nil`, **cmds=0**, `status=compensating` unchanged |
| `CompensateRequested(now, "")` | `err=nil`, **cmds=0**, unchanged |
| `NewReverseToStart` / `NewReverseToNode` | error: `cannot reverse instance while a compensation walk is in flight` |
| `ActionFailed{CommandID: cursor.ActiveCmdID}` | ✅ `cmds=1 [InvokeAction]` — advances to `undoA` |
| `ActionFailed{CommandID: "not-a-real-cmd"}` | error: `no token awaiting command: … invalid state transition` |
| `StartInstance` | ⚠ `err=nil`, `status=running`, `tokens=1`, re-emits `doA` |

### 4.3 The same levers against a stalled RESUMING (throw) walk

```
PROBE[cursor2] activeCmdID="probe-2-c3" nextIndex=1 finalStatus=running
               resumeNode="afterThrow" archiveKey=""
PROBE[leverA-cancel-defers]      err=<nil> cmds=0 pendingCancel=true
PROBE[leverA-outcome]            pendingFinalStatus=terminated pendingFinalErr="cancelled"
PROBE[leverB-cancel-after-defer] err=<nil> cmds=0 pendingCancel=true
```

A deadlock, not merely a no-op: the cancel is deferred to an `applyFinish` that never runs.
⚠ This walk's first dispatch came from `startCompensationWalk` — the site the pre-audit
design failed to arm (§9, C1).

### 4.4 The `StartInstance` anomaly, pinned

```
PROBE[startinstance-on-compensating] status=running cmds=1 [InvokeAction] tokens=1 root=2
PROBE[restart-cursor] activeCmdID="probe-1-c3" nextIndex=1 finalStatus=terminated
                      startedAt=2026-08-12 11:00:00 +0000 UTC
PROBE[late-undoB-after-restart] err=… no token awaiting command … "probe-1-c3"
```

Reachable only through `engine.Step` / `ApplyTrigger`, not through `Drive` (P18). §8.

### 4.5 What the audit measured that this spec had wrong

```
# C2 — abandon retains ALREADY-RUN records on a beginCompensation walk
RootCompensations AFTER dispatching "undoB": [undoA undoB]
consumeDispatchedRecord was a NO-OP (len(cur.Records)==0)
later-full-rollback: cmds=1 [InvokeAction undoB]     <== undoB had already run

# C3 — abandon on a throw walk destroys un-run records
targeted:   archive {sub=[undoIA,undoIB]} -> {sub=[undoIA]}; abandon leaves it; status=running
scope-wide: root=[] with undoA never dispatched

# A-4 — a stall incident survives a NORMAL terminal finish, with no verb involved
PROBE[post-terminal] status=terminated incidents=1
                     Incidents[0].Error == "compensation action stalled"
```

## 5. Design

### 5.1 Detection (opt-in)

A new `TimerKind`, `TimerCompensationStall`, **appended** to the iota block (P14 — an
existing constant's value must not shift, or every persisted timer row is reinterpreted).

Configured by `StepOptions.CompensationStallAfter time.Duration`, fed by
`runtime.WithCompensationStallTimeout(d)`. **Zero disables, and zero is the default.**

⚠ **A flat engine-wide knob is a deliberate v1 simplification, not a precedent-backed
choice.** P13's `effectiveRetryPolicy` is a three-tier chain *because* one timeout for every
action is wrong: a ledger reversal returns in milliseconds, a manual-approval-gated refund
takes hours, and a single window forces the operator to size for the slowest. A per-node
tier is backlog (§8), not scope.

`timerRecord` gains a `CommandID` field. The record is **walk-scoped, not token-scoped**:

```go
timerRecord{
    TimerID:   s.nextTimerID(),
    Kind:      TimerCompensationStall,
    Token:     "",              // P4/P11: never swept, never read
    NodeID:    rec.NodeID,
    ScopeID:   cur.ScopeID,
    CommandID: cur.ActiveCmdID, // what makes a late fire safe
}
```

**Arm sites — all THREE compensation dispatch sites (`grep compensationInvoke(`), and
nowhere else:**

- `beginCompensation`, at its first `compensationInvoke`. ⚠ **After** `s.cancelAllTimers()`
  (P10 — it nils `s.Timers`, so an earlier arm is silently lost).
- **`startCompensationWalk`** (`engine/step_nodes.go`) — the compensation-THROW walk's first
  dispatch, in a different file from the other two. This is the site §4.3's measured
  deadlock arises at; without it a single-record throw walk gets no detection at all.
- `stepCompensationAdvance`, at each subsequent `compensationInvoke`.

**Cancel sites.** The outstanding stall timer is cancelled by a single
cancel-then-arm helper used at every arm site, **and explicitly in
`stepCompensationFinish` before its plan switch, so all five walk modes are covered**.
⚠ Only *terminal* finishes reach `cancelAllTimers` via `endInstance`; the four **resume**
finishes touch `s.Timers` not at all, so without the explicit cancel the record survives
onto a Running instance (measured, A-1).

### 5.2 Fire → incident, and nothing else

A `TimerCompensationStall` case in `handleTimerFired`'s path-4 `Kind` switch. P1+P2 make it
reachable: path 4 sits ahead of the `spawnsNewWork()` guard, so a **dying** instance still
gets the fire — load-bearing, because the walks that terminate (`walkAdmin`, covering
cancel, error **and the admin full rollback**; `walkReverse`; and any throw walk carrying
`PendingCancel`) are exactly the walks for which `spawnsNewWork()` is false.

⚠ **Path 4 is not inherently safe on a dying instance.** The audit measured a `TimerInWait`
reminder emitting a real `InvokeAction` there on `spawnsNewWork()==false` — a pre-existing
ADR-0172 hole (§8). Our placement is safe **only because this handler emits no commands**.
That is a constraint on the handler, not a property inherited from the location.

Guard, then act:

```
if s.Status != StatusCompensating                -> drop the record, no-op
if rec.CommandID != s.Compensating.ActiveCmdID   -> drop the record, no-op   (late fire)
otherwise: append Incident{ID: s.nextIncidentID(), TokenID: "", NodeID: rec.NodeID,
                           ScopeID: rec.ScopeID, CommandID: rec.CommandID,
                           Error: "compensation action stalled",
                           Kind: IncidentCompensationStall, CreatedAt: at}
           s.removeTimer(rec.TimerID)
           return no commands, cursor UNTOUCHED
```

`Incident` gains `Kind IncidentKind`, with `IncidentAction` at iota 0.

**The incident is retired wherever the walk moves on.** `stepCompensationAdvance` clears
every open `IncidentCompensationStall` whose `CommandID` equals the cursor's `ActiveCmdID`
*before* it recomputes the cursor — so a late `ActionCompleted`, a late `ActionFailed` and
the `skip` verb all sweep it through one shared path. `endInstance` sweeps any remainder.

⚠ **Ordering trap — CONFIRMED as originally written.** On the escape verbs the sweep must run
**before** delegating to `stepCompensationFinish`, which clears `s.Compensating` before
calling `applyFinish`; `endInstance`'s remainder sweep therefore runs with `ActiveCmdID == ""`
and early-returns.

⚠ An earlier revision of this spec "corrected" that away, claiming the abandon-path sweep was
redundant because `endInstance` covered it. It does not. The claim came from deleting the
line and observing a GREEN engine suite — but the suite simply had no assertion on incidents
after abandon. `TestAbandonRetiresTheStallIncident` now supplies it and is RED without the
line. *A green suite is evidence about the suite, never about the engine.*

Measured without the sweep: `incidents=1` after a *normal* terminal finish, with
`Incidents[0].Error == "compensation action stalled"` becoming the instance's published cause
of death (A-4); and the same surviving record on the abandon path.

### 5.3 The escape trigger

```go
type CompensationDisposition int

const (
    CompensationRetry CompensationDisposition = iota
    CompensationSkip
    CompensationAbandon
)

type ResolveCompensationStall struct {
    baseTrigger
    CommandID   string // REQUIRED — must equal s.Compensating.ActiveCmdID
    IncidentID  string // optional: "" targets the in-flight walk
    Disposition CompensationDisposition
}

func NewRetryStalledCompensation(at time.Time, commandID, incidentID string) ResolveCompensationStall
func NewSkipStalledCompensation(at time.Time, commandID, incidentID string) ResolveCompensationStall
func NewAbandonCompensationWalk(at time.Time, commandID, incidentID string) ResolveCompensationStall
```

`terminalPolicy()` is `rejectWithError`.

**`CommandID` is required and cursor-matched.** That makes all three verbs naturally
idempotent — a replay finds the cursor already moved on and is a clean no-op — and it is the
same guard shape the fire handler uses. Without it the audit measured a compensation action
running **twice**, with the original completion rejected as `no token awaiting command`,
which an at-least-once action transport turns into a redelivery loop. It also supplies the
evidence-of-intent that a bare "act on whatever is in flight" verb lacks: a 500 ms-old
healthy dispatch satisfies "a walk is in flight", so without a named command id `skip` could
silently drop a compensation that was about to succeed.

**`IncidentID` stays optional** because detection defaults to off: with
`CompensationStallAfter == 0` no incident exists and there would be nothing to name. A
non-empty `IncidentID` naming no open `IncidentCompensationStall` is an **error**, not the
idempotent no-op `handleResolveIncident` uses — an operator who mistypes an id must not
silently get a walk-wide action.

| verb | behaviour |
|---|---|
| **retry** | `records := cursorRecords(s, cur)`; if `cur.NextIndex < 0 \|\| cur.NextIndex >= len(records)` the source shrank or vanished ⇒ route to `stepCompensationFinish`. ⚠ This is retry's **own** bound — P9 guards `NextIndex-1`, and a naive `records[cur.NextIndex]` **panics inside the pure core** (measured: `len(records)=0` at `NextIndex=1`). Otherwise emit `compensationInvoke(records[cur.NextIndex], s.nextCommandID())`, set `cur.ActiveCmdID`, cancel-then-re-arm the stall timer, and do **not** re-run `consumeDispatchedRecord` (ownership transferred at the original dispatch). |
| **skip** | delegate to `stepCompensationAdvance` — byte-identical to §4.2's measured-working `ActionFailed` path, and the contract ADR-0034 Decision 4 already states. |
| **abandon** | **accepted only when `cur.walkMode() == walkAdmin`**; on any other mode return a named error telling the operator to use `skip`. Then delegate to `stepCompensationFinish` with the terminate plan, retaining records `[0 .. NextIndex-1]` and dropping the record at `NextIndex`. |

**Why abandon is restricted (§9 C3).** `stepCompensationFinish` picks its plan from
`walkMode()`, so a throw walk takes a **resume** plan and a "terminate plan only" override
never applies. Measured: a targeted throw's in-flight record was already consumed from the
archive at dispatch and abandon never puts it back (`{sub=[undoIA,undoIB]} → {sub=[undoIA]}`,
instance **resumes**); a scope-wide throw's `drainedCount = StartRecordCount` clears the
whole drained prefix (`root=[]`, `undoA` never dispatched). `skip` already drains a resuming
walk to its natural resume, so no escape is lost.

**Why abandon drops the stalled record (§9 C2).** `consumeDispatchedRecord` acts only on a
**pinned** cursor, and only `startCompensationWalk` pins. On a `beginCompensation` walk it
early-returns (P8), so `RootCompensations` still holds every record — run or not. Retaining
the whole list is therefore not "keeping the un-run records"; measured, the admin rollback
this design promises re-dispatched `[undoB undoA]` with `undoB` already run. Retaining
`[0 .. NextIndex-1]` keeps strictly what the walk never dispatched; the record at
`NextIndex` is dropped because its action may still be in flight at the worker.

⚠ **Accepted cost:** if the stalled action genuinely never ran, that undo work is lost.
`retry` is the verb for that case. This makes abandon consistent with `skip`, which also
gives up on the stalled record.

**Abandon applies the walk's own recorded finish outcome** (`FinalStatus` / `FinalErr`), so
on a `walkAdmin` walk carrying `PendingCancel` `applyFinish`'s `consumePendingCancel` path
runs.

⚠ **CORRECTED BY IMPLEMENTATION: `skip`, not `abandon`, is what discharges the
deferred-cancel deadlock.** `PendingCancel` is stamped by two writers only, and the one the
§4.3 deadlock goes through — `handleCancelRequested` — requires `ResumeNode != "" ||
ReverseNode != ""`, i.e. a walk that RESUMES. C3 (below) makes abandon refuse exactly those
walks, so the original claim was invalidated by the audit's own fix and nobody re-checked it.
Measured:

```
PROBE[throw-walk]    mode=walkThrowScopeWide pendingCancel=false
PROBE[after-cancel]  cmds=0                  pendingCancel=true
PROBE[after-skip-1]  status=compensating     pendingCancel=true
PROBE[after-skip-2]  status=terminated       pendingCancel=false  cmds=[CancelTimer FailInstance{cancelled}]
```

The escape is real, the verb is `skip` (`TestSkipDischargesTheDeferredCancelDeadlock`). A
`walkAdmin` walk carrying `PendingCancel` has never been demonstrated to be reachable.

Because records are retained, **ADR-0164 Decision 2** still admits one later admin action: a
*full* rollback, `NewCompensateRequested(at, "")`, which flips the terminated instance back
to `compensating` (measured `err=nil cmds=1 [undoB]`). A partial rollback and
`ReverseInstance` are both refused on a terminal instance, so "a later admin rollback" means
the full-rollback form only.

ADR-0173 preferred a double-run over a loss in exactly **one bounded case** (a pre-ADR-0171
unpinned cursor): *"The alternative loses the record outright, which is worse."* That
adjudication does not generalise to a record the walk has already dispatched — recruiting it
as a general preference for retention is what licensed C2's double-run.

**No walk in flight**, or a `CommandID` that does not match ⇒ an error **and** a
`slog.Warn` operator record, mirroring `dispatch`'s own refusals.

### 5.4 The refusal that prevents data loss

Per P7, `handleResolveIncident` removes the incident **before** the token lookup and returns
no commands when the token is nil — measured, it silently eats a `TokenID: ""` incident. So
an operator hitting the **already-shipped** HTTP `resolve-incident` endpoint would delete the
stall incident and get nothing, making the stall invisible as well as unresolved.

`handleResolveIncident` refuses `inc.Kind != IncidentAction`, returning an error that names
the new verbs.

⚠ **CORRECTED BY IMPLEMENTATION: the guard's POSITION is not what protects the incident.**
This said the refusal must sit *before* the removal line, and §7's T11b prescribed a mutation
moving it below, expecting the *still present* assertion to redden. Measured, moving it below
leaves the test fully GREEN: `Step` returns the ZERO `StepResult` on error, so the caller
discards the clone whose slice the removal mutated. The GUARD is load-bearing; its placement
is defence-in-depth and readability. The mutation that does redden is deleting the guard
outright — `err=<nil>` with the incident consumed, which is the pre-0175 behaviour.

⚠ The refusal guards the `StatusCompensating` window only: on a terminal instance
`dispatch`'s structural guard returns `ErrInstanceTerminal` first (ADR-0165). The two
refusals must be told apart by test (§7 T11/T11c), or T11 passes for the wrong reason.

### 5.5 Surface

As built:

```
engine.ResolveCompensationStall trigger  (+ NewResolveCompensationStall for the layers)
  → ProcessDriver.ResolveCompensationStall(ctx, def, instanceID, commandID, incidentID, disposition)
  → service.ResolveCompensationStall(ctx, ResolveCompensationStallRequest)
  → POST /admin/instances/{id}/compensation/resolve-stall
```

⚠ The HTTP body is REQUIRED, unlike `resolve-incident`'s optional one: `command_id` and
`disposition` are both mandatory and neither may default.
`httpcore.ParseCompensationDisposition` fails closed on an empty or unknown verb, because the
zero value of `CompensationDisposition` is `CompensationRetry` — a remote re-execution
primitive — so a defaulted verb would re-invoke an action nobody asked for.

⚠ Admin routes are **not** part of `Mount`; a consumer opts into them separately so they can
sit behind different authorization. The route is registered in all three adapters' own
`AdminRoutes`, pinned by `TestParity_PostResolveCompensationStall_400` (a missing mount shows
up as a 404 there).

Plus a **projection**: `Compensating.ActiveCmdID` and a `compensating_since` reach
`service.ProcessInstance`, so an operator can (a) enumerate wedged instances by listing
`status=compensating` and (b) read the `CommandID` the verbs now require. Without it, an
instance that was **already stalled** before this delivery shipped is undetectable — both
arm sites are *dispatch* sites and a stalled walk never dispatches again, so a consumer who
upgrades *because* they have wedged instances would see zero incidents (A-2).

⚠ Surfacing `CommandID` is a deliberate choice with a cost, but a smaller one than stated
here originally. `<instance>-cN` is only the deterministic fallback `nextID` uses with no
`IDGenerator` injected; the runtime always injects xid (ADR-0149), and the same generator
mints the `tokens[].id` and `tasks[].task_id` the same document already exposes. Confirmed by
`/security-review`, which dismissed it as an oracle on exactly these grounds.

**Authorization.** `retry` is a remote **re-execution primitive** (it re-invokes a named
action with the record's captured input) and `abandon` is destructive and irreversible. The
endpoint requires a privilege distinct from `resolve-incident`, and `abandon` is gated
separately from `retry`/`skip`. ⚠ This surface carries two known-open defects — self-asserted
actor identity and a fail-open `AuthzSpec` — so Phase 5 must exercise the authorization
path, not only the happy path.

## 6. What breaks

**Nothing while detection is off; three things once it is on — plus one that is
flag-independent.**

With `CompensationStallAfter` at its zero default no `ScheduleTimer` is added and no command
stream changes shape.

Enabled:

- (a) a surviving stall incident becomes `Incidents[0]` on a terminal instance, so
  `runtime/outbox.go`'s `terminalEventErr` and `runtime/processdriver_action.go`'s
  `terminalErr` report `"compensation action stalled"` where they reported `"cancelled"`
  (measured). §5.2's retirement sweep is what closes this; the entry stays because the
  sweep is now load-bearing rather than incidental.
- (b) the `incidentsRaised` metric counts stalls, and `incident_count` — projected in all
  three dialects as a JSON array length — inflates for a compensating instance.
- (c) `processtest.Classify` reports `ReasonIncident` where it reported `ReasonUnknown`, and
  a `TimerCompensationStall` record would otherwise make `Park.HasArmedTimers` true, so
  `processtest.AutoTimers()` would **fire the stall timer by itself** in a consumer's
  harness (measured — the exclusion test is RED without the fix). ⚠ Decision:
  `TimerCompensationStall` records are **excluded from `HasArmedTimers`**.

  ⚠ **As built, the exclusion had to move into the ENGINE.** `timerRecord.Kind` is
  unexported, so `processtest` — a different package — physically cannot tell a stall
  deadline from an armed timer by reading `state.Timers`. `Classify` now calls a new
  exported `engine.InstanceState.HasArmedTimers()`, which carries the pre-existing KNOWN GAP
  (it reads `s.Timers` only, so boundary / event-gateway / event-sub-process timer arms stay
  invisible — blocker 9, still open).

Flag-independent:

- ⚠ **`Incident.Kind` and `timerRecord.CommandID` enter the persisted snapshot** —
  `store_core.go` marshals the whole `InstanceState`. ADR-0173's mixed-version rule applies
  verbatim: an old build round-trips a new snapshot with `Kind` **dropped**, degrading an
  `IncidentCompensationStall` into a resolvable `IncidentAction` that the shipped
  `resolve-incident` endpoint will then delete — the exact data loss §5.4 exists to prevent.
  **Do not run pre-0175 and post-0175 builds against the same instance store.**
- A 16th trigger variant reddens **THREE** exhaustiveness tables plus the codec:
  `trigger_validate_test.go` (verified by removing the registration),
  `trigger_terminal_policy_test.go` and `step_terminal_dispatch_test.go`.
  `MarshalTrigger` **hard-errors** on an unhandled variant, so omitting the codec case fails
  the journal write and therefore the whole `ApplyTrigger`.

  ⚠ **CORRECTED BY IMPLEMENTATION: this said FOUR and named
  `step_harvest_terminal_admission_test.go`.** Measured, that file does NOT redden, and its
  own header says why: *"the enumeration below is deliberately NOT presented as exhaustive:
  it is hand-maintained, and a NEW trigger will not appear in it (the sibling catches
  that)."* It guards an EXISTING trigger being reclassified to `allowOnTerminal`, which is a
  different property.
- Abandon leaves `RootCompensations` as an **array** where every terminate finish today
  persists `null` — a stored-shape change on terminal instances, analogous to ADR-0174's
  `Scopes` `[]`→`null`.

The one behavioural change to an existing path is §5.4's refusal branch, which can only fire
for an incident kind that does not exist before this delivery.

## 7. Test plan

Hot paths first. Each row states what makes it fail today.

| # | Test | What makes it fail today |
|---|---|---|
| T1 | `beginCompensation` with `CompensationStallAfter > 0` emits `ScheduleTimer{Kind: TimerCompensationStall}` | the kind does not exist |
| T1b | **mutation**: hoist the arm above `cancelAllTimers` ⇒ T1 reddens | the P10 trap |
| **T1c** | a scope-wide throw's **FIRST** dispatch arms a stall timer | `startCompensationWalk` has no arm — the C1 gap |
| T2 | with `CompensationStallAfter == 0`, `beginCompensation` emits exactly `[UpdateTask, InvokeAction{undoB}]` and `s.Timers` is empty — an explicit **golden list captured from `main`** (no test can assert equality with another git revision) | reddens if the arm is unconditional |
| T2b | the advance cancels the previous stall timer and arms one carrying the NEW `ActiveCmdID`; `len(Timers) == 1` | the advance site does not arm |
| **T2c** | a **resume** finish emits `CancelTimer` and leaves `s.Timers` empty | measured leak — no finish-site cancel exists (A-1) |
| T3 | fire ⇒ one `Incident{Kind: IncidentCompensationStall, TokenID: ""}`, **zero commands**, cursor byte-identical | no handler |
| T4 | fire on a **dying** walk still raises the incident | the P1 test |
| T4b | **mutation**: move the case below the `spawnsNewWork()` guard ⇒ T4 reddens | |
| T5 | a stall timer whose `CommandID` ≠ `ActiveCmdID` ⇒ no incident, no commands | |
| T5b | **mutation**: delete the `CommandID` comparison ⇒ T5 reddens | |
| T6 | **retry** re-dispatches under a NEW command id; `len(Timers) == 1` carrying it | verb does not exist |
| T6b | retry on a walk with a non-empty `ArchiveKey` still finds the record | pins the audit's confirmed result |
| T7 | **skip** advances — same stream as the measured `ActionFailed` lever | |
| T8 | **abandon** on a `walkAdmin` walk ⇒ terminal, `RootCompensations == [0..NextIndex-1]` | |
| **T8b** | **mutation**: retain the whole list ⇒ the later-rollback assertion reddens on `[undoB undoA]` vs `[undoA]` | the C2 measurement |
| **T8c** | **abandon** on a throw / partial / reverse walk ⇒ **named error**, state untouched | the C3 refusal |
| T9 | **abandon** on a `walkAdmin` walk carrying `PendingCancel` ⇒ deferred cancel consumed, instance terminates | §4.3's deadlock discharged |
| T10 | a late `ActionCompleted` after an incident ⇒ advance **and** incident cleared | no sweep exists |
| **T10b** | the same for a late `ActionFailed` | both route to `stepCompensationAdvance`; a sweep placed in `handleActionCompleted` would miss this |
| **T10c** | a **normal** terminal finish leaves **zero** stall incidents, and `terminalEventErr` reports `"cancelled"` | measured today as `incidents=1` / `"compensation action stalled"` (A-4) |
| T11 | `ResolveIncident` on an `IncidentCompensationStall`, instance **`StatusCompensating`** ⇒ error, incident **still present** | today it removes it and returns nil (P7) |
| T11b | **mutation**: move the guard below the removal line ⇒ T11's *still present* assertion reddens | ⚠ if it does not, the assertion checks only the error and is worthless |
| **T11c** | the same call on a **terminated** instance ⇒ `ErrInstanceTerminal` | tells the two refusals apart |
| T12 | all three verbs with no walk in flight, and with a non-matching `CommandID` ⇒ error + Warn record (swap the handler per `observability_noop_test.go`; the core logs through package-level `slog`) | |
| T13 | **retry** against an unpinned cursor with a gone source ⇒ routes to finish, **no panic** | measured `len(records)=0` at `NextIndex=1` (A-5) |
| T13b | trigger codec round-trip for all three dispositions | the kind is unknown to the codec |
| **T13c** | the four exhaustiveness tables classify the new variant | A-6 |
| T15 | end-to-end through `service` + HTTP, **including the authorization path** | surface does not exist |

**Regression pins** (not falsifiable today — stated separately rather than dressed up as
tests that can fail):

- T14 — a stall timer fired on a terminal instance is dropped at dispatch (P12). ⚠ Its
  fixture must carry a `timerRecord{Kind: TimerCompensationStall}` and a terminal `Status`,
  or it is a test about `TimerFired` in general and is already covered.
- `TimerCompensationStall.String()` needs its **own** assertion: `command_test.go`'s TimerKind test names
  `TimerRetry` explicitly, so a missing case would ship silently (A-25).

⚠ Every skip-vs-abandon test needs a fixture with **two** compensable activities; a
one-record walk finishes on the first advance and cannot discriminate the verbs. Check the
FIXTURE, not the assertion text.

## 8. Backlog opened or confirmed by this spec

**Pre-existing defects the audit found in shipped code:**

1. ⚠ **A `TimerInWait` reminder fired on a `spawnsNewWork()==false` instance emits a real
   `InvokeAction`** — an ADR-0172 hole in `handleTimerFired` path 4. Reachable via a throw
   walk carrying `PendingCancel`. Own ADR.
2. **`engine/step_triggers.go:291` cites a nonexistent `ADR-0034 §2.5`** — that ADR has no
   numbered sections; the contract is Decision 4. This false comment is what propagated six
   times into this bundle before the audit caught it.
3. **`StartInstance` is accepted on a `compensating` instance** and restarts it from the top
   with a stale cursor (§4.4). Reachable only through `engine.Step`/`ApplyTrigger`, not
   `Drive` (P18). On a resuming walk it also leaves `PendingCancel=true` on the now-Running
   instance — the same defect as handover backlog item 14. Own ADR.
4. **`CancelInstance` reports success for a cancel that did nothing** (§4.2).

**Found by `/code-review`, ACCEPTED and FIXED in this bundle:**

4d. **The abandon-path incident sweep is LOAD-BEARING, not redundant** — an interim revision
    of this spec claimed otherwise. `stepCompensationFinish` clears the cursor before
    `applyFinish`, so `endInstance`'s sweep early-returns. `TestAbandonRetiresTheStallIncident`
    added; RED without the line.
4e. **`cancelCompensationStallTimers` left `Timers` as `[]`** when the stall record was the
    last one, on every RESUME finish. Now nils it, matching `cancelAllTimers`.
4f. **`disposition` was written into EVERY journal payload** (one flat envelope shared by all
    17 kinds). Now a `*int` with `omitempty`; a pointer to 0 still emits, so a stored `retry`
    stays distinguishable from a missing field.
4g. **`compensating.since` rendered year 1** for a walk already in flight at upgrade — exactly
    the population the projection exists to triage. Now `*time.Time`, omitted when unknown.
4h. **`abandon` does not run `consumePendingCancel`** — `walkAdmin`'s plan sets `resume:false`
    and `applyFinish` gates that branch on `plan.resume`, so `PendingCancel` is left set on the
    terminated instance. Comment corrected.

**Found by `/code-review`, ADJUDICATED as out of scope (documented, not fixed):**

4i. **`service.Service` gains a method — a BREAKING interface change** for any consumer that
    implements or decorates it. Accepted: the alternative (an optional capability interface,
    as `TimerAdmin` etc. use) hides a core operator verb behind a type assertion. ⚠ Must be
    called out in the release note.
4j. **The stall incident's `ScopeID` is empty for a TARGETED throw**, whose cursor is
    `{ArchiveKey: ref}` with no scope. Faithful to the cursor but ambiguous with root;
    documented at the arm site, with `NodeID` named as the field to read.
4k. **A late reply to a superseded compensation command returns `ErrTokenNotFound`** rather
    than being treated as a benign duplicate the way `runtime/calllink` treats it. Real, and
    exactly the out-of-process population this feature targets — but it changes the
    ActionCompleted contract, so it belongs with backlog item 7 (the `ActionFailed`
    compensation-retry story), not here.

**Found by IMPLEMENTATION (new):**

4b. **With detection OFF, the cancel path already turns `s.Timers` from nil into an empty
    non-nil slice** — `beginCompensation`'s prologue runs `cancelTimersByTaskID` for the
    parked human task, which rebuilds the slice. Measured on `main` @ `5270838`:
    `timersNil=false`. Pre-existing and unrelated to this delivery, but it means the persisted
    `timers` shape already drifts null→[] on that path. (The throw path does leave nil, which
    is why the new writer's no-op-when-disabled property is pinned there instead.)

4c. **`Incident` gains a wire field.** `service`'s `incidentJSON` now carries `kind`, and a
    new `compensating` object carries `active_command_id` / `since` / `scope_id`. Both are
    additive, but a consumer pinning the instance document byte-for-byte will see them.

**Deferred from this design:**

5. **A per-node `CompensationStallAfter` tier**, mirroring `effectiveRetryPolicy`'s
   three-tier chain. The flat knob cannot fit both a millisecond ledger reversal and an
   hours-long manual-approval refund.
6. **A bound on repeated `retry`.** `Incident.Attempts` stays 0 and the cursor carries no
   counter, unlike `tok.RetryAttempts`. Retries are unbounded by design in v1; each cycle
   raises a fresh incident.
7. **A retry/incident story for a compensation action returning `ActionFailed`** (handover
   backlog item 11) — now adjacent, since compensation has an incident kind. Deliberately
   out of scope: it changes ADR-0034 Decision 4's contract.

## 9. Audit adjudication

Full record: [`2026-08-12-adr-0175-audit-evidence.md`](2026-08-12-adr-0175-audit-evidence.md).

Three Opus lenses (execution / consistency / failure-modes), each in its own worktree.
**Three Criticals**, two of them found independently by two lenses:

- **C1** — three compensation dispatch sites, not two; `startCompensationWalk` was unarmed,
  so a single-record throw walk got no detection at all. Controller-verified. Folded into
  §2.1, §5.1 and T1c.
- **C2** — abandon retained already-run records; measured double-run `[undoB undoA]`.
  Resolved by retaining `[0 .. NextIndex-1]` and dropping the stalled record.
- **C3** — abandon on a throw walk destroyed un-run records. Resolved by refusing abandon
  on any non-`walkAdmin` walk.

**What survived execution:** P1 (twice, once with a control), P11 (by mutation),
retry-readability with a non-empty `ArchiveKey`, crash survival of detection, abandon
discharging the deferred cancel, and every §4 measurement reproduced exactly under two
lenses.

⚠ **The failures were all in generalisation, not in measurement**: a two-item enumeration
that had rotted, three false quantifiers, an inherited citation restated six times, and a
paraphrased quote recruited beyond its bounded scope. The numbers were honest; the sentences
summarising them were not.
