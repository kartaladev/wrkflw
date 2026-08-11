# 175. An operator escape from a stalled compensation walk

- Status: Accepted (audited — 3 lenses, 3 Criticals, ~30 findings adjudicated)
- Date: 2026-08-12

> Design and every measurement:
> [`docs/specs/2026-08-12-operator-escape-from-a-stalled-compensation-walk.md`](../specs/2026-08-12-operator-escape-from-a-stalled-compensation-walk.md).
> Rule-#9 audit record:
> [`docs/specs/2026-08-12-adr-0175-audit-evidence.md`](../specs/2026-08-12-adr-0175-audit-evidence.md).
> Plan:
> [`docs/plans/2026-08-12-stalled-compensation-walk-escape.md`](../plans/2026-08-12-stalled-compensation-walk-escape.md).
>
> Adjacent but deliberately **not** closed here: a retry/incident story for a
> compensation action that returns `ActionFailed` (that changes
> [ADR-0034](0034-compensation-on-error-cancel.md) Decision 4's best-effort-skip
> contract and deserves its own decision).

## Context

There are **three** compensation dispatch sites (`grep compensationInvoke(`):
`beginCompensation`, `startCompensationWalk` (the compensation-throw walk's first dispatch,
in `engine/step_nodes.go`), and `stepCompensationAdvance`. Each dispatches one compensation
`InvokeAction` and records its id in `s.Compensating.ActiveCmdID`. The walk advances **only**
on a trigger carrying that id — `ActionCompleted` (advance) or `ActionFailed` (best-effort
skip, ADR-0034 Decision 4).

Measured on `main` @ `5270838`, the state of an instance whose compensation action never
reported back:

```
status=compensating  tokens=0  timers=0  incidents=0
signalWaiters=[]  msgWaiters=[]  root=2
cursor{ActiveCmdID:"probe-1-c3" NextIndex:1 FinalStatus:terminated FinalErr:"cancelled"}
```

For a walk started by `beginCompensation` the prologue has cleared every token and timer, so
nothing in the persisted state can wake it. (A throw walk deliberately leaves sibling tokens
running; what it also lacks is anything that can advance **the walk**.) No clock and no
scheduler can move it. Exactly one operator action does move it — `ActionFailed` carrying the
cursor's `ActiveCmdID` — and one destroys it:

| lever | measured result |
|---|---|
| 2nd `CancelRequested` | `err=nil`, **0 commands**, status unchanged |
| `CompensateRequested` | `err=nil`, **0 commands**, unchanged |
| `NewReverseToStart` / `NewReverseToNode` | error: *cannot reverse instance while a compensation walk is in flight* |
| `ActionFailed{CommandID: cursor.ActiveCmdID}` | ✅ advances the walk |
| `StartInstance` | ⚠ accepted — restarts the instance from the top |

Against a **resuming** (compensation-throw) walk the cancel is worse than a no-op: it sets
`PendingCancel` + `PendingFinalStatus=terminated` + `PendingFinalErr="cancelled"` to be
consumed by an `applyFinish` that never runs. That is a deadlock, not an omission.

Two facts make this worse than the one-line framing in `docs/plans/HANDOVER.md` suggested:

1. **Through the `service` / HTTP operator surface the escape count is zero.**
   `CancelInstance` is a measured no-op here, `resolve-incident` has no incident to act on,
   and `ReverseInstance` is not on `service.Service` at all. The working lever is reachable
   one level down, via the exported `ProcessDriver.ApplyTrigger` — but only for a caller that
   already holds the dispatched `CommandID` (its own out-of-process worker), and **not at all
   after a driver restart**.
2. **The cancel reports success.** `CancelInstance` returns `err=nil` and a state, so the
   caller is told the cancel worked while nothing happened.

⚠ **"Never reports back" excludes the default in-process path.** `performInvokeAction` bounds
every non-fire-and-forget invocation with `defaultActionTimeout` (30 s) and converts failure
into an `ActionFailed` that advances the walk. The cause this design assumes — a lost
callback, or a worker that died between dispatch and reply, which includes the engine's own
process restarting mid-walk — is therefore an out-of-process or crash phenomenon, and any
detection mechanism that does not survive a crash misses the case it exists for.

## Decision

**1. Detection is opt-in, timer-based, and lives in the engine core.**

A new `TimerKind`, `TimerCompensationStall` (**appended** to the iota block so no existing
persisted value shifts), is armed at **all three** compensation dispatch sites —
`beginCompensation` (after `cancelAllTimers`, which nils `s.Timers`), **`startCompensationWalk`**,
and `stepCompensationAdvance` — for `StepOptions.CompensationStallAfter`, fed by
`runtime.WithCompensationStallTimeout(d)`. **Zero disables, and zero is the default.**

Arming `startCompensationWalk` is not optional: it is the throw walk's *first* dispatch, so
without it a single-record throw walk gets no detection at all — and it is the site the
measured deferred-cancel deadlock arises at.

Durability and rehydration come free through ADR-0134's `ScheduleTimer` path (`kind` is a
plain `SMALLINT` with no CHECK constraint in any dialect), which is why detection is not
built in the runtime alongside a second durable record.

The record is **walk-scoped, not token-scoped**: `Token: ""` (ADR-0152 makes an empty key
name no record, and the runtime never reads the field) plus a new `timerRecord.CommandID`
carrying the guarded `ActiveCmdID`. The outstanding timer is cancelled by one shared
cancel-then-arm helper, **and explicitly in `stepCompensationFinish` before its plan
switch** — only *terminal* finishes reach `cancelAllTimers`, so the four resume finishes
would otherwise leak the record onto a live instance.

⚠ A single engine-wide window is a deliberate v1 simplification, **not** a
precedent-backed choice: `effectiveRetryPolicy` is a three-tier chain precisely because one
timeout for every action is wrong. A per-node tier is backlog.

**2. A breach raises an incident and changes nothing else.**

The fire handler is a case in `handleTimerFired`'s path-4 `Kind` switch, which sits **ahead**
of the `!s.spawnsNewWork()` guard — load-bearing, because the walks that terminate
(`walkAdmin`, covering cancel, error **and the admin full rollback**; `walkReverse`; and any
throw walk carrying `PendingCancel`) are exactly the walks for which `spawnsNewWork()` is
false. It appends an `Incident` with `TokenID: ""`, the timer record's `CommandID`, and a new
`Incident.Kind` of `IncidentCompensationStall`; it drops the timer record; it emits **no
commands** and does not touch the cursor.

⚠ **Path 4 is not inherently safe on a dying instance** — a `TimerInWait` reminder was
measured emitting a real `InvokeAction` there (a pre-existing ADR-0172 hole). This placement
is safe **only because this handler emits nothing**, which is a constraint on the handler,
not a property inherited from the location.

`IncidentKind`'s zero value is `IncidentAction`, so every existing incident keeps its meaning.

**The incident is retired wherever the walk moves on.** `stepCompensationAdvance` clears
every open `IncidentCompensationStall` matching the cursor's `ActiveCmdID` before recomputing
the cursor — so a late `ActionCompleted`, a late `ActionFailed` and `skip` all sweep through
one path — and `endInstance` sweeps any remainder, reading the cursor **before** clearing it.

⚠ **This ordering requirement is CONFIRMED, and an earlier revision of this ADR wrongly
"corrected" it away.** `stepCompensationFinish` clears `s.Compensating` before it calls
`applyFinish`, so `endInstance`'s remainder sweep runs with `ActiveCmdID == ""` and
early-returns — it does NOT cover a verb that delegates to the finish. The escape verbs must
retire their own incident first, and they do.

⚠ **The mis-correction is the lesson.** Deleting abandon's sweep left the whole engine suite
GREEN, and that was read as "redundant". It was UNTESTED, not redundant: the suite had no
assertion on incidents after abandon. `TestAbandonRetiresTheStallIncident` now exists and is
RED without the line. *A green suite is evidence about the suite, never about the engine.*
`endInstance`'s remainder sweep remains load-bearing for a different route — a walk killed
mid-flight WITHOUT going through the finish, e.g. a force-termination end event.

The engine does **not** auto-skip or auto-retry. Silently abandoning an undo action on a
timeout is how a booking never gets cancelled.

**3. Three operator verbs ride one new trigger.**

```go
type ResolveCompensationStall struct {
    baseTrigger
    CommandID   string // REQUIRED — must equal s.Compensating.ActiveCmdID
    IncidentID  string // optional: "" targets the in-flight walk
    Disposition CompensationDisposition // Retry | Skip | Abandon
}
```

**`CommandID` is required and cursor-matched**, making all three verbs naturally idempotent —
a replay finds the cursor moved on and is a clean no-op. Without it a compensation action was
measured running **twice**, with the original completion rejected as `no token awaiting
command`, which an at-least-once transport turns into a redelivery loop. It also supplies the
evidence of intent a bare "act on whatever is in flight" verb lacks: a 500 ms-old healthy
dispatch also satisfies "a walk is in flight".

`IncidentID` stays optional because detection defaults to off. A non-empty `IncidentID`
naming no open incident is an **error**, not an idempotent no-op.

- **retry** re-dispatches the record at `cur.NextIndex` under a fresh command id and
  cancel-then-re-arms the stall timer. ⚠ It needs its **own** bounds check — `cur.NextIndex`
  out of range routes to the finish; the ADR-0171 disjunct guards `NextIndex-1`, and a naive
  index panics inside the pure core.
- **skip** delegates to `stepCompensationAdvance` — the path the measured `ActionFailed` lever
  already takes, and the contract ADR-0034 Decision 4 states.
- **abandon** is **accepted only when `cur.walkMode() == walkAdmin`**; on any other walk it
  returns a named error naming `skip` instead. It then delegates to `stepCompensationFinish`
  with the terminate plan, **retaining records `[0 .. NextIndex-1]` and dropping the record at
  `NextIndex`**.

*Why abandon is restricted:* `stepCompensationFinish` selects its plan from `walkMode()`, so a
throw walk takes a **resume** plan and a "terminate plan only" override never applies.
Measured, abandon on a targeted throw destroyed the un-run stalled record
(`{sub=[undoIA,undoIB]} → {sub=[undoIA]}`, instance resumed) and on a scope-wide throw cleared
the whole drained prefix (`root=[]`). `skip` already drains a resuming walk to its natural
resume, so no escape is lost, and the alternative — re-parking records into the archive —
would add a new write direction into ADR-0173's audited ownership machinery.

*Why abandon drops the stalled record:* `consumeDispatchedRecord` acts only on a **pinned**
cursor, and only `startCompensationWalk` pins. On a `beginCompensation` walk it early-returns,
so `RootCompensations` still holds every record, run or not — retaining the whole list made
the later rollback re-dispatch `[undoB undoA]` with `undoB` already run. Retaining
`[0 .. NextIndex-1]` keeps strictly what was never dispatched; the record at `NextIndex` is
dropped because its action may still be in flight at the worker. ⚠ Accepted cost: if that
action genuinely never ran, its undo work is lost — `retry` is the verb for that case. This
makes abandon consistent with `skip`, which also gives up on the stalled record.

Abandon applies the walk's own recorded finish outcome, so on a walk carrying `PendingCancel`
`applyFinish`'s `consumePendingCancel` path runs.

⚠ **CORRECTED BY IMPLEMENTATION — `abandon` is NOT what discharges the deferred-cancel
deadlock; `skip` is.** This decision originally claimed it was, and that claim became false
the moment the audit's C3 finding restricted abandon to `walkAdmin`. `PendingCancel` is
stamped by exactly two writers, and `handleCancelRequested` — the one the measured deadlock
goes through — requires `ResumeNode != "" || ReverseNode != ""`, i.e. a walk that RESUMES.
That is precisely the set C3 makes abandon refuse. The two halves of this ADR were
incompatible as written, and nobody re-checked the sentence when C3 landed. Measured:

```
PROBE[throw-walk]    mode=walkThrowScopeWide pendingCancel=false
PROBE[after-cancel]  cmds=0                  pendingCancel=true
PROBE[after-skip-1]  status=compensating     pendingCancel=true
PROBE[after-skip-2]  status=terminated       pendingCancel=false  cmds=[CancelTimer FailInstance{cancelled}]
```

`skip` drains the resuming walk to its finish, where `consumePendingCancel` runs — so the
escape from §Context's deadlock is real, it is just a different verb. Pinned by
`TestSkipDischargesTheDeferredCancelDeadlock`, which also asserts abandon is refused there.
The retained-outcome behaviour above still holds for a `walkAdmin` walk; what has never been
demonstrated is a `walkAdmin` walk that carries `PendingCancel` at all.

Because records are retained, **ADR-0164 Decision 2** still admits
one later admin action: a *full* rollback (`NewCompensateRequested(at, "")`), which flips the
terminated instance back to `compensating`. A partial rollback and `ReverseInstance` are both
refused on a terminal instance.

ADR-0173 preferred a double-run over a loss in exactly **one bounded case** (a pre-ADR-0171
unpinned cursor): *"The alternative loses the record outright, which is worse."* That
adjudication does not generalise to a record the walk has already dispatched; recruiting it as
a general preference is what licensed the double-run above.

**4. `handleResolveIncident` refuses a compensation incident.**

It removes the incident *before* looking up the token and returns no commands when the token
is nil — measured, it silently eats a `TokenID: ""` incident. So an operator hitting the
already-shipped HTTP `resolve-incident` endpoint would delete the stall incident and get
nothing. The refusal names the new verbs. ⚠ It guards the `StatusCompensating` window only;
on a terminal instance `dispatch` returns `ErrInstanceTerminal` first (ADR-0165), so the two
refusals must be told apart by test.

⚠ **CORRECTED BY IMPLEMENTATION:** this said the refusal is placed *"before the removal
line"*, implying the ordering is what prevents the loss. It is not. Measured, moving the
guard below the removal leaves every assertion green — `Step` returns the zero `StepResult`
on error, so the caller discards the clone the removal mutated. Returning an ERROR at all is
the protection; the placement is defence-in-depth.

**5. The cursor is projected so an operator can find and name a stall.**

`Compensating.ActiveCmdID` and a `compensating_since` reach `service.ProcessInstance`.
Without this, an instance **already stalled** before this delivery shipped is undetectable —
both arm sites are *dispatch* sites and a stalled walk never dispatches again, so a consumer
who upgrades *because* they have wedged instances would see zero incidents. The projection is
also what lets an operator read the `CommandID` decision 3 now requires. ⚠ It exposes an internal
command id — a deliberate trade. (Corrected: an earlier revision called this an
`<instance>-cN` sequence oracle. That form is only the deterministic fallback used when no
`IDGenerator` is injected; the runtime always injects xid per ADR-0149, and the same
generator mints the token and task ids the document already exposes.)

`retry` is a remote **re-execution primitive** and `abandon` is destructive and irreversible,
so the endpoint requires a privilege distinct from `resolve-incident`, with `abandon` gated
separately from `retry`/`skip`.

## Consequences

**Nothing breaks while detection is off.** With `CompensationStallAfter` at its zero default
no `ScheduleTimer` is added and no command stream changes shape.

**Enabled, three things change.** (a) A surviving stall incident would become `Incidents[0]`
on a terminal instance, and both `runtime/outbox.go`'s `terminalEventErr` and
`runtime/processdriver_action.go`'s `terminalErr` return that slot unconditionally — so
decision 2's retirement sweep is load-bearing, not incidental; without it a walk that
recovered on its own publishes `"compensation action stalled"` as its cause of death, to the
outbox and to a call-activity **parent**. (b) The `incidentsRaised` metric counts stalls, and
`incident_count` — a JSON array length in all three dialects — inflates. (c)
`processtest.Classify` reports `ReasonIncident` where it reported `ReasonUnknown`;
`TimerCompensationStall` records are therefore **excluded from `Park.HasArmedTimers`**, or
`processtest.AutoTimers()` would fire stall timers by itself inside consumers' harnesses.
`processtest` is a shipped consumer harness, so this is a decision, not a side effect.

**`Incident` no longer implies a token.** `removeOrphanedIncidents` already keeps an
empty-`TokenID` incident, its comment calling itself *"a guard against the next terminal site
rather than a live defect"* — this delivery is that site. The **six** known incident readers
are: `runtime/outbox.go`'s `terminalEventErr`, `runtime/processdriver_action.go`'s
`terminalErr`, the `incidentsRaised` metric, `runtime/kernel`'s `IncidentCount`,
`processtest.Classify`, and `service/instance.go`'s audit view.

⚠ **Mixed-version deployment is unsafe.** `Incident.Kind` and `timerRecord.CommandID` enter
the persisted snapshot (`store_core.go` marshals the whole `InstanceState`), so ADR-0173's
mixed-version rule applies verbatim: an old build round-trips a new snapshot with `Kind`
**dropped**, degrading an `IncidentCompensationStall` into a resolvable `IncidentAction` that
the shipped endpoint will then delete — the exact data loss decision 4 exists to prevent.
**Do not run pre-0175 and post-0175 builds against the same instance store.**

**A 16th trigger variant is not free.** THREE exhaustiveness tables plus the codec must be
extended, and `MarshalTrigger` hard-errors on an unhandled variant, failing the journal write
and therefore the whole `ApplyTrigger`. (⚠ Corrected from "four": measured,
`step_harvest_terminal_admission_test.go` does not redden for a new variant — its own header
states the enumeration is deliberately hand-maintained and non-exhaustive.)

**Abandon changes a persisted shape**: `RootCompensations` becomes an array where every
terminate finish today writes `null` — analogous to ADR-0174's `Scopes` `[]`→`null`.

**The detection half is invisible until configured.** A consumer who never reads the release
note gets the escape verbs but not the incident that tells them to use one. Chosen
deliberately over defaulting detection on, which would alter every consumer's compensation
command stream — but it means the operators who most need the feature are the ones least
likely to have enabled it. Flipping the default is its own ADR and its own breaking change.

**Retry assumes compensation actions are idempotent** on the re-dispatch path, and is
**unbounded**: the cursor carries no counter (unlike `tok.RetryAttempts`) and each cycle
raises a fresh incident. Skip carries no idempotency assumption; abandon carries it only for
the record it drops.

**A new `TimerKind` enters the pure core**, with the obligation that every terminal and
teardown path either cancels the stall timer or is covered by `cancelAllTimers` — which the
four resume finishes are **not**. This is the fourth delivery in five to touch the
compensation-walk finish machinery (ADR-0168–0171, 0173, 0174), and each of the previous ones
had design claims corrected by execution; this one had three Criticals corrected at audit.

**Scoped out, recorded as backlog:** the ADR-0172 path-4 reminder hole; the false
`ADR-0034 §2.5` citation in `engine/step_triggers.go`; `StartInstance` accepted on a
compensating instance; `CancelInstance` reporting success for a no-op cancel; a per-node stall
window; and a bound on repeated retry.
