# 178. A dying instance ignores a fired timer

- Status: Proposed (**audited** — rule-#9, 3 lenses; this ADR was corrected by it)
- Date: 2026-08-13

> Design and every measurement:
> [`docs/specs/2026-08-13-engine-visibility-and-truthfulness.md`](../specs/2026-08-13-engine-visibility-and-truthfulness.md) §2.
> Premise evidence: `docs/specs/2026-08-13-adr-0178-0180-premise-evidence.md`, defect (0).
> Plan: [`docs/plans/2026-08-13-engine-visibility-and-truthfulness.md`](../plans/2026-08-13-engine-visibility-and-truthfulness.md).
>
> Closes backlog **0** (opened by the ADR-0175 audit).

## Context

ADR-0172 established that an instance which no longer spawns forward work must not be given new
work. `spawnsNewWork()` has **six** production call sites, and `handleTimerFired` guards four of
its five numbered paths with it.

**Path 4 — the timer-record switch — is unguarded**, and it is exactly the path whose timers carry
a record in `s.Timers`. The guard/no-guard split is the record/no-record split. The only
`spawnsNewWork` check in the function sits 36 lines *below* the switch and protects path 5 alone;
all four `return`s in the switch jump over it.

Executed on a compensation throw walk carrying a deferred cancel (`spawnsNewWork()==false`,
`PendingCancel=true`), `EXIT=0`:

```
STEP 3 — TimerFired(reminder):
    [0] InvokeAction{Name:"remind" CommandID:"recon-0-c2" FireAndForget:true}

STEP 3 — TimerFired(DEADLINE):
    [0] InvokeAction{Name:"notify" ... FireAndForget:true}
    [1] engine.UpdateTask {Task:{... State:cancelled ...}}
    [2] engine.CancelTimer {...}
  tokens before: 1 → tokens after: (none)
```

⚠ **The deadline case is strictly worse than the reminder the backlog names, and was in no
backlog entry.** It dispatched an action, cancelled the open human task and **consumed the token**,
advancing a dying instance's live branch to an end event. A fix patching only `handleReminderFired`
leaves it behind.

Two corrections to the inherited framing:

- The reminder is `FireAndForget: true` — an unwanted **external side effect**, not the ADR-0172
  "`ActionCompleted` lands on a terminated instance" mode. Real, but not what the backlog asserts.
- `handleRetryFired` is also unguarded and emits a **non**-fire-and-forget `InvokeAction`, so it is
  the path that *would* reproduce the quoted ADR-0172 mode. ✅ **EXECUTED at implementation**
  (assumption discharged): measured `InvokeAction{work, _idempotencyKey:dying-1:svc,
  FireAndForget:false}` on a dying instance. Previously established
  by source inspection only; implementation must execute it.

The ADR-0175 delivery already knew: `engine/step_compensation_stall_incident_test.go:12-16` says
*"⚠ Path 4 is NOT inherently safe on a dying instance: a TimerInWait reminder was measured emitting
a real InvokeAction from there."* It was left open.

## Decision

Guard path 4 on `spawnsNewWork()`, **exempting walk-scoped timer kinds** (today
`TimerCompensationStall` alone). A refused fire retires the timer record, logs
`slog.WarnContext` — the idiom in **four** `engine` files — and emits exactly one command:
`CancelTimer{rec.TimerID}`.

```go
if !rec.Kind.walkScoped() && !s.spawnsNewWork() { … refuse … }
```

⚠⚠ **Amended in-bundle after `/code-review` (owner gate).** This ADR originally said the refusal
"returns no commands". That is a **scheduler-job leak**, because retiring the record is exactly
what stops the later terminal sweep (`endInstance` → `cancelAllTimers`, which derives its
`CancelTimer`s from `s.Timers`) from ever disarming it — and a `TimerInWait` reminder is armed with
a **recurring** trigger, so its job outlives the instance and keeps firing against a terminated one
forever. Measured with this bundle's own `dyingTimerDef` fixture (`tm2` = the `Every 1h` reminder):

```
(A) reminder fired once while dying → refusal commands=[]  … terminal cancelTimers=[tm1 tm3]
(B) reminder never fired            →                         terminal cancelTimers=[tm1 tm2 tm3]
```

The refusal is therefore the **last** chance to disarm the job, and emitting `CancelTimer` there is
a correctness requirement, not tidiness. "No **work**" is the invariant; a disarm is not work.
`TestFiredTimerOnDyingInstanceOnlyDisarmsItsTimer` (renamed from
`…EmitsNothing`, which the fix made false) asserts the command set is **exactly**
`[CancelTimer{rec.TimerID}]`, so neither an omitted disarm nor a leaked `InvokeAction` passes.

Rejected: a **blanket** path-4 guard. It would regress ADR-0175, whose stall incident is *meant*
to fire on a dying walk — pinned by `TestStallIncidentIsRaisedOnADyingWalk`. The exemption is a
correctness requirement, not an optimisation.

⚠⚠ **The premise "a compensation walk is by definition no longer spawning forward work" is FALSE**,
and the pre-audit draft relied on it. Measured:

```
(A) THROW walk   ResumeNode="afterThrow"  => SpawnsNewWork = TRUE
(B) CANCEL walk  ResumeNode=""            => SpawnsNewWork = FALSE
```

`state.go:541` returns `!walkTerminates(PendingCancel)`, so **resuming** walks (throw, admin partial
rollback) do spawn work. The decision is unaffected — terminating walks are still `false` — but any
test of this guard must **assert the premise** with `require.False(t, engine.SpawnsNewWork(&st))`
before firing. A plain `driveToScopeWideThrow` fixture passes whether or not the guard is correct —
a vacuous test of the very thing this ADR decides.

⚠⚠ **Amended in-bundle after implementation refuted the audit's prescribed fixture.** The audit
required *"a cancel-started walk"*. Executed, that fixture **cannot exist** for the three guarded
kinds: `beginCompensation`'s prologue cancels every token and sweeps `s.Timers`, leaving
**tokens=0, timer records=0** — path 4 has nothing to fire. The constructible fixture is a
**resuming throw walk carrying a deferred cancel** (`PendingCancel=true` → `walkTerminates` →
`spawnsNewWork()==false`). The requirement is *"a walk that **terminates**, asserted"*.
`TestStallIncidentIsRaisedOnADyingWalk` gets away with a cancel-started walk only because its record
is `TimerCompensationStall`, armed **after** that prologue.

## Consequences

**Positive.** The three token-hung timer kinds (`TimerInWait`, `TimerDeadline`, `TimerRetry`) stop
acting on instances the engine has already decided are dying. The worst measured case — a deadline
consuming a token and advancing a dying branch — is closed. The fix is entirely engine-internal:
no exported signature changes.

**Negative / accepted.**

- ⚠ **Forward dependency on ADR-0179 (bundle C).** Its compensation retry timer fires *precisely
  when* a terminating walk is in flight, i.e. `spawnsNewWork()==false`. If bundle C does not add
  `TimerCompensationRetry` to `walkScoped()`, this guard refuses every compensation retry and that
  ADR silently never works. Bundle C owns the edit and the test; this ADR guarantees only that the
  predicate is named and extensible rather than inlined.
- A refused fire **retires** the record rather than leaving it armed, so a timer that would have
  re-fired is gone. On a dying instance that is the intent; it is stated because it is a state
  mutation on a refusal path. Because retiring it also removes it from the terminal sweep's view,
  the refusal itself emits the `CancelTimer` — see the amendment under Decision.
- ⚠ This ADR **falsifies a comment in the existing test suite**:
  `engine/step_compensation_stall_incident_test.go:79-88` (and the `require.Len` message at `:99`)
  asserts that "path 4 sits AHEAD of the `!spawnsNewWork()` guard". After this change path 4 *has* a
  guard and the stall record survives only via the exemption. Corrected in the same bundle.
- A consumer relying on a deadline action firing during a rollback loses it. That behaviour was
  never intended and is the defect being fixed.
