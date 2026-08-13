# Engine visibility and truthfulness (ADR-0177, ADR-0178, ADR-0180)

**Status**: design, **audited** (3 lenses, 27 findings, all accepted — §8)
**Date**: 2026-08-13
**Bundle**: A — one delivery, three ADRs, one commit
**Base**: `main` at the ADR-0176 merge `52bf0f80` (+ docs-only commits, head `12c9d7e3`)

This bundle closes `HANDOVER.md` ▶ NEXT WORK items **1** and **4**. Item **3** (a failed
compensation action) was originally part of this bundle as ADR-0179 and was **split out into its
own delivery** after the rule-#9 audit put 4 of its 6 Criticals there — see §9.

Three decisions, one theme: **the engine answers questions it cannot answer, and stays silent
about things it should say.** It reports "no armed timers" when four of eight arm sites are
invisible to the predicate; it acts on a dying instance; and it returns `nil` — success — for two
commands that did nothing.

---

## §0 — Reading guide, and what is evidence here

Every factual claim about current behaviour was **executed** before it was written (CLAUDE.md
**Premise Discipline**). Transcripts live beside this file:

| record | covers |
|---|---|
| `2026-08-13-adr-0177-premise-evidence.md` | timer-arm visibility, the five sources, `processtest` promotion |
| `2026-08-13-adr-0178-0180-premise-evidence.md` | dying-instance timers, the citation, duplicate start, dropped cancel |
| `2026-08-13-adr-0177-0180-audit-lens-{a,b,c}.md` | the rule-#9 audit that rewrote this document |

⚠ Premises here are **not** inherited from `HANDOVER.md`; re-derivation refuted six of its
statements (§7). Do not restore a handover phrasing without re-executing it — restating strips the
hedge, which is how three of those six became false.

⚠ **This document was materially wrong before its audit.** §8 records what changed and why. Read
§8 before trusting any sentence that survived from the pre-audit draft.

---

## §1 — ADR-0177: an instance's timer arms are enumerable

### The measured problem

`HasArmedTimers()` reads `s.Timers` only. Executed, six fixtures (`EXIT=0`, `--- PASS`):

| fixture | `len(s.Timers)` | `HasArmedTimers()` | where the arm lives |
|---|---|---|---|
| boundary timer on a user task | 0 | **false** | `s.Boundaries[0].TimerID` |
| boundary timer on a receive task | 0 | **false** | `s.Boundaries` |
| event-gateway timer arm | 0 | **false** | `s.ArmedEvents[0].TimerID` |
| event sub-process timer start | 0 | **false** | `s.EventTriggeredSubprocesses[0].TimerID` |
| plain intermediate-catch timer | 0 | **false** | **nothing** — only `tok.AwaitCommand` |
| CONTROL: user-task deadline | 1 | **true** | `s.Timers[0]{Kind: TimerDeadline}` |

The control row makes the other five non-vacuous.

**Eight production arm sites, five timer kinds.** Four sites arm as `TimerIntermediate` and are
never written into `s.Timers`; four are. The guard/no-guard split is the record/no-record split.

### Decision

`TimerWaiter{TimerID, Kind, NodeID, TokenID}` plus a `TimerWaiters()` authority on
`InstanceState`, following `state_waiters.go`'s `MessageWaiter` pattern: flat exported struct, five
per-source accessors, one composing authority over a `timerWaitersOf[T]` sibling of the existing
generic scanners. Deterministic order, `nil` when empty.

**`Token.AwaitTimer`** is added and written where the plain intermediate catch event arms.
⚠ **Dual-write**: `AwaitCommand` keeps its current value so `handleTimerFired`'s path-5
fall-through is untouched. `AwaitTimer` is an enumeration marker, not a dispatch key.

⚠⚠ **`AwaitTimer` MUST also be CLEARED** wherever `AwaitCommand` is cleared — **seven** production
sites (`step_gateways.go:243`, `step_timers.go:83`, `step_triggers.go:112/376/569/741/1002`). The
pre-audit draft specified only the set side. Left unset-only, the field stays populated after the
first fire, so `HasArmedTimers()` returns `true` forever, `Classify` reports `ReasonTimer` for a
park with nothing armed, and `AutoTimers()` spins on an id path 5 treats as a stale no-op —
**inverting the purpose of this ADR**. The fix is one `Token.clearAwait()` helper used at all seven
sites, with its own RED test. ⚠ One of those sites (`:569`) is inside the path-5 fall-through the
dual-write rationale says not to touch; clearing there is a *field write*, not a dispatch change,
and is explicitly in scope.

**`HasArmedTimers()` is redefined** over `TimerWaiters()`, keeping ADR-0175's
`TimerCompensationStall` exclusion. ⚠ `walkScoped()` **does not exist today** — the exclusion is
an inline `tr.Kind != TimerCompensationStall` at `state_timers.go:146-152`. This bundle introduces
the named predicate; **ADR-0179 (bundle C) extends it** to its new kind.

### Why not the alternatives

- **Id-prefix sniffing** (`<instance>-tm<N>`) — rejected: parses meaning out of an identity,
  contradicting ADR-0152, and breaks silently if ids are ever made opaque.
- **Scoping to the four recorded sources** — cheaper, rejected by the owner in favour of
  completeness; it would leave the free `processtest.Classify` permanently wrong for plain-ICE.

### Blast radius, measured

`HasArmedTimers()` has **two** call sites repo-wide (`processtest/park.go:141` production,
`engine/step_compensation_stall_test.go:378` test). No production consumer outside `engine` reads
`InstanceState.Timers`. `STABILITY.md` puts the module pre-1.0 with no released tag.

### The consequence that needs pinning tests

`processtest.Classify`'s priority, verified character for character at `park.go:159-186`:

```
terminal > openTasks > incidents > signals > messages > HasArmedTimers > commandWait > unknown
```

Timers rank **below** messages and signals, so the three harness-level fixtures (probe 2 B/C/D)
stay `ReasonMessage`. ⚠ Note case **A** (boundary timer on a *user task*) is non-reclassified by
the **`openTasks`** rung, not the message rung — a separate pin.

A park can therefore only move if **every** rung above `timers` is empty for it, which leaves
`commandWait` and `unknown` as the only reasons the widening can displace. Derived from the ladder,
the shapes that flip are:

1. a **plain timer intermediate catch** — its token parks on the timer id, so it read `commandWait`;
2. an **event-based gateway whose arms are all timers** — its token parks on an `evtgw:` sentinel,
   so it read `commandWait` too;
3. an **arm-borne timer on an instance with no command wait at all**, which read `unknown`.

⚠ **AMENDED AFTER `/code-review` (owner gate).** This section originally said the widening flips
*one* shape. It reaches a fourth, and that one is a **regression**: a **boundary** or
**event-sub-process** timer arm sitting beside an **in-flight `InvokeAction` / child instance**
also flips `ReasonAsyncChild` → `ReasonTimer`, so `Chain(AutoTimers(), …)` fires the boundary to
its timeout instead of letting the action handler resolve the park — contradicting the contract
`processtest/handlers.go` documents for `AutoTimers` ("a park that merely has a *secondary* armed
timer … is left for the task handler"). Measured on `start → svc[action work] ⊸ bnd[timer 3h]`
built with `engine.Step` (instance `i`):

```
reason=timer node="svc" hasArmedTimers=true   token{awaitCmd:"i-c1" awaitTimer:""}
AutoTimers() → AdvanceTimers()
```

**Fix as shipped:** a timer outranks a command wait only when the timer is what the instance waits
**ON**. `processtest.primaryTimerPark` decides that from two state facts — a waiting token whose
`AwaitCommand` is a member of `state.TimerWaiters()`'s id set, or a non-empty
`state.TimerArmedEventWaiters()` — and everything else (boundary arm, event-sub-process arm) is
secondary. Measured on all five shapes, engine-built, instance `i`:

| shape | token `AwaitCommand` | waiter | verdict | `Reason` |
|---|---|---|---|---|
| `svc[work] ⊸ bnd[timer 3h]` | `i-c1` | `i-tm1` boundary | secondary | `async-child` |
| evtsub `onTimer[5h]` + `svc[work]` | `i-c1` | `i-tm1` esp arm | secondary | `async-child` |
| `t-catch[timer 1h]` | `i-tm1` | `i-tm1` token | primary | `timer` |
| `svc[retry]` after a retryable failure | `i-tm1` | `i-tm1` `TimerRetry` record | primary | `timer` |
| `egw ⊸ t-arm[timer 1h]` | `evtgw:i-t1` | `i-tm1` gateway arm | primary | `timer` |

⚠ The **adjudicated** fix handed to implementation was "the timer rung wins when some token has
`Token.AwaitTimer != ""`". Executed, it is **worse than the bug**: it reddens the last two rows.
The retry-backoff park was `ReasonTimer` since long before ADR-0177 — `HasArmedTimers` read any
non-`TimerCompensationStall` **record**, and a `TimerRetry` record is one (`engine/state_timers.go`
at `99da2026^`) — so that predicate silently stops `AutoTimers()` advancing every retry backoff, a
hot harness path. The event-gateway park would lose the very promotion ADR-0177 gave it.
`AwaitTimer` is also unset on a rehydrated pre-0177 token, whereas `AwaitCommand` has always been
persisted. Mutation M4 in the delivery log reproduces both failures.

### KNOWN LIMITATION (pinned, with its falsifier stated)

An instance parked on a plain intermediate-catch timer **before** this ships has no `AwaitTimer`
in its stored row, so that source stays invisible after rehydration until re-armed. Backfilling
would need the rejected id-sniffing.

⚠ The pin is **only** falsified by a backfill implemented *inside* `TimerTokenWaiters`; a backfill
done in a migration or the rehydrate path would leave it green. Stated explicitly, and paired with
the complementary assertion — the same token **with** `AwaitTimer` yields a waiter — which does
genuinely fail before the field exists.

---

## §2 — ADR-0178: a dying instance ignores a fired timer

### The measured problem

`handleTimerFired` has **five** numbered paths. Paths 1, 2, 3 and 5 are guarded by
`spawnsNewWork()`; **path 4 — the timer-record switch — is the only unguarded one**, and it is the
only path whose timers carry a record in `s.Timers`.

Executed on a throw walk carrying a deferred cancel (`spawnsNewWork()==false`), `EXIT=0`:

```
STEP 3 — TimerFired(reminder):
    [0] InvokeAction{Name:"remind" CommandID:"recon-0-c2" FireAndForget:true}

STEP 3 — TimerFired(DEADLINE):
    [0] InvokeAction{Name:"notify" ... FireAndForget:true}
    [1] engine.UpdateTask {Task:{... State:cancelled ...}}
    [2] engine.CancelTimer {...}
  tokens before: 1 → tokens after: (none)
```

⚠ **The deadline case is strictly worse than the reminder the backlog names, and was in no backlog
entry.** It dispatched an action, cancelled the open human task and **consumed the token**,
advancing a dying instance's live branch to an end event.

Two corrections to the inherited framing: the reminder is `FireAndForget: true` — an unwanted
**external side effect**, not the ADR-0172 "`ActionCompleted` lands on a terminated instance" mode;
and `handleRetryFired` is also unguarded and emits a **non**-fire-and-forget `InvokeAction`, so it
is the path that *would* reproduce that mode. ✅ **EXECUTED at implementation** (the assumption is
discharged): the retry fixture is constructible — a retryable `ActionFailed` on a sibling service
branch — and its command is measured
`InvokeAction{work, _idempotencyKey:dying-1:svc, FireAndForget:false}`.

### Decision

Guard path 4 on `spawnsNewWork()`, **exempting walk-scoped kinds** (today: `TimerCompensationStall`
alone). A refused fire retires the record, logs `slog.WarnContext` — the idiom in **four** `engine`
files — and emits exactly one command, `CancelTimer{rec.TimerID}`.

⚠⚠ **AMENDED AFTER `/code-review` (owner gate).** This said "returns no commands", which leaks a
scheduler job: retiring the record is what stops the terminal sweep (`endInstance` →
`cancelAllTimers`, which derives its `CancelTimer`s from `s.Timers`) from ever disarming it, and a
`TimerInWait` reminder is armed **recurring**. Measured with `dyingTimerDef` (`tm2` = the `Every 1h`
reminder):

```
(A) reminder fired once while dying → refusal commands=[]  … terminal cancelTimers=[tm1 tm3]
(B) reminder never fired            →                         terminal cancelTimers=[tm1 tm2 tm3]
```

The invariant is "no **work**"; a disarm is not work. `TestFiredTimerOnDyingInstanceOnlyDisarmsItsTimer`
(renamed from `…EmitsNothing`, which the fix made false) asserts the command set is **exactly**
`[CancelTimer{rec.TimerID}]`, so neither an omitted disarm nor a leaked `InvokeAction` passes.

⚠ **A blanket path-4 guard would regress ADR-0175**, whose stall incident is *meant* to fire on a
dying walk, pinned by `TestStallIncidentIsRaisedOnADyingWalk`.

⚠⚠ **The premise "a compensation walk is by definition no longer spawning forward work" is FALSE.**
Measured:

```
(A) THROW walk   ResumeNode="afterThrow"  => SpawnsNewWork = TRUE
(B) CANCEL walk  ResumeNode=""            => SpawnsNewWork = FALSE
```

`state.go:541` returns `!walkTerminates(PendingCancel)`, so **resuming** walks (throw, admin partial
rollback) do spawn work. The guard is still correct — terminating walks are still `false` — but any
test of it must **assert the premise** with `require.False(t, engine.SpawnsNewWork(&st))` before
firing, exactly as `TestStallIncidentIsRaisedOnADyingWalk` does. A plain
`driveToScopeWideThrow` fixture passes whether or not the guard is correct.

⚠⚠ **CORRECTED BY IMPLEMENTATION.** The audit prescribed *"use a cancel-started walk"*. Executed,
that fixture **cannot exist** for these three kinds: `beginCompensation`'s prologue cancels every
token and sweeps `s.Timers`, so a cancel-started walk holds **tokens=0 and timer records=0** and
path 4 has nothing to fire. The constructible fixture — and the one this bundle's own evidence file
already measured — is a **resuming throw walk carrying a deferred cancel** (`PendingCancel=true` →
`walkTerminates` → `spawnsNewWork()==false`). The requirement is therefore *"a walk that
**terminates**, asserted"*, not *"a walk that is cancel-started"*.
`TestStallIncidentIsRaisedOnADyingWalk` can use a cancel-started walk only because its record is
`TimerCompensationStall`, armed **after** that prologue.

---

## §3 — ADR-0180: a command that did nothing does not report success

### 3a — duplicate start

Executed: `engine.Step(StartInstance)` on a live compensating instance returns `err=<nil>` and
**superimposes a second start** — tokens 1 → 3, tasks 1 → 2, the old parked token and its task
still present, `StartVariables` overwritten, cursor byte-identical. Measured consequence: the
worker running the in-flight compensation reports back and is rejected with `ErrTokenNotFound`
(→ 422); a control proves the same trigger succeeds without the restart.

Two corrections, both **widening** the defect: it does not "restart from the top" (it
superimposes), and it is **not specific to `compensating`** — any non-terminal instance accepts it,
a plain `running` one going tokens 1 → 2. `Drive`'s refusal is store-level id-uniqueness, which
refuses a `running` id identically.

**Decision.** `handleStartInstance` refuses an already-started instance with
`ErrInstanceAlreadyStarted`.

⚠ **The predicate is NOT a `Status` test.** `StatusRunning = iota` is the **zero value**; a fresh
state already reads as `StatusRunning`, so a status-keyed guard refuses every legitimate start.

⚠⚠ **`StartedAt.IsZero()` alone is ALSO insufficient.** `s.StartedAt = t.OccurredAt()` is the only
writer, and `engine.Step` is public API where the caller supplies `at`. Executed with the naive
guard patched in:

```
CONTROL   start#1 err=<nil> tokens=1 StartedAt=2026-06-20 10:00:00 UTC
CONTROL   start#2 err=<already started> tokens=0     <- refused
ZERO-TIME start#1 err=<nil> tokens=1 StartedAt=0001-01-01 IsZero=true
ZERO-TIME start#2 err=<nil> tokens=2                 <- SUPERIMPOSED
```

The predicate is therefore
**`s.StartedAt.IsZero() && len(s.Tokens) == 0 && len(s.History) == 0`** — a start is refused when
any evidence of a prior start exists. Both of the pre-audit test rows passed under the defective
predicate; §6.4 adds the zero-time row that does not.

⚠ **Compatibility to execute, not assume**: the message-, signal- and timer-start paths lean on
`ErrInstanceExists` from `Store.Create` for their at-least-once no-op. They use `create=true` on a
**fresh** snapshot and never re-enter `handleStartInstance` on an existing one — confirmed by the
audit running the full container-free suite under a patched guard with no regression.

### 3b — dropped cancel

Executed, all three layers, against an admin **partial** rollback in flight:

| layer | result | did anything change? |
|---|---|---|
| `engine.Step` | `err=<nil>`, 0 commands | **state byte-identical to before the cancel** |
| `ProcessDriver.CancelInstance` | `err=<nil>` | persisted snapshot unchanged |
| `service.CancelInstance` | `err=<nil>` → **HTTP 200** | same |

then: `after the walk finishes: status=running tokens=1`. The operator gets a 200 and the instance
goes back to running.

`handleCancelRequested` has **five** return sites: 1 error, 2 doing real work, **2 returning `nil`
without terminating** — `:196` (deferred, `PendingCancel=true`) and `:210` (dropped). They must not
be collapsed: *"will terminate later"* vs *"will not terminate at all"*.

**Decision.** The `:210` **dropped** site returns `ErrCancelNotApplicable`; `service` maps it to
`ErrConflict` → **422**. The `:196` deferred site keeps returning `nil`. ⚠ **422, not 409** — `ErrConflict` and `ErrInvalidTransition` classify to 422 (`httpcore/errors.go:48`); 409 is `kernel.ErrConcurrentUpdate` alone. Corrected at implementation.

⚠⚠ **Corrected by implementation — the `:210` site serves TWO situations, and this spec generalised
from the one it measured.** It is reached (i) by an admin **partial** rollback, which *resumes*, so
the cancel is genuinely lost — the measured defect; and (ii) by a redundant cancel during a
**terminal** cancel/error walk, where the instance *will* terminate — ADR-0034's post-acceptance
**idempotent re-cancel**. Returning the sentinel from both turned
`TestSecondCancelMidCompensationWalkDoesNotDoubleCompensate` **RED**. The refusal is gated on the
existing shared predicate `!s.Compensating.walkTerminates(s.PendingCancel)`, and a third table row
pins the idempotent case.

⚠ **Both sentinels wrap `ErrInvalidTransition`** — measured, not stylistic: without the wrapping the
driver's answer reaches HTTP as a **500 with an empty body**, because `httpcore.ClassifyError` has
no arm for a bare sentinel. ✅ `transport/http` needed **no production edit**, as the plan predicted.

⚠⚠ **The sentinel MUST be a reporting outcome, not a propagation-halting error.** The pre-audit
draft reasoned that `propagateCancel`'s child loop would absorb it because it logs-and-continues.
The loop does absorb it — and the conclusion drawn was still wrong, proved by mutation:

```
(a) child status BEFORE parent cancel = running
    CancelInstance err = … cancel not applicable
    child status AFTER  parent cancel = running   (IsTerminal=false)

(b) WARN runtime: propagateCancel: cancel child instance failed child_id=…
    child must be Terminated:      expected 4 (terminated) actual 0 (running)
    grandchild must be Terminated: expected 4 (terminated) actual 0 (running)
```

Two independent failures: `runtime/processdriver_cancel.go:26-29` returns the error **before** the
propagation block at `:30-33` ever runs; and inside the loop, `continue` at `:89` skips the
**recursion** at `:92`, orphaning grandchildren. Today those children terminate. Naively
implemented, this decision leaves a terminated parent with a permanently running subtree —
**strictly worse than the silent `nil` it replaces.**

The required shape:
- in `CancelInstance`: `if err != nil && !errors.Is(err, engine.ErrCancelNotApplicable) { return st, err }`, so propagation still runs; return the sentinel **after** propagation;
- in the child loop: on that sentinel, log and **recurse anyway**.

⚠ **AMENDED AFTER `/code-review` (owner gate): "log" is not "log as a failure."** The WARN line in
(b) above is the *mutation's* output; the shipped loop kept it unconditional, so the by-design drop
was reported at WARN as `"cancel child instance failed"` — training an operator to ignore the one
line that means propagation genuinely could not reach a child. The failure WARN now sits **inside**
the `!errors.Is(cancelErr, engine.ErrCancelNotApplicable)` branch, and the drop has its own
`slog.LevelDebug` line, `"runtime: propagateCancel: child kept its own compensation walk; cancel
dropped"`, carrying `reason` rather than `error`. Recursion behaviour is unchanged.

⚠ The pre-audit draft also cited `CancelRequested.terminalPolicy() == rejectSilently` as the safety
argument. That is a **category error** — the policy governs triggers on *terminal* instances, and
`:210` is reachable only on a non-terminal compensating one. Removed.

**Breaking**: a consumer treating `err == nil` as "cancelled" now gets a **422** for the dropped case.

---

## §4 — Cross-decision interactions

1. **ADR-0177 × ADR-0178** — both consume the `walkScoped()` predicate this bundle introduces.
   Defined once, in one place.
2. **ADR-0180's start guard × ADR-0177's `AwaitTimer`** — none; the guard reads instance-level
   fields, the marker is on `Token`. Stated so the audit need not re-derive it.
3. ⚠ **Forward dependency on bundle C (ADR-0179).** Its `TimerCompensationRetry` kind must be added
   to `walkScoped()` when it lands, or ADR-0178's guard refuses every compensation retry (that
   timer fires precisely when a *terminating* walk is in flight). Bundle C owns that edit and its
   test; this bundle leaves the predicate extensible and says so.

---

## §5 — Doc-only corrections

**Thirteen** false claims in shipped code. All executed; none change behaviour.

⚠ **This count grew three times, which is the finding.** The pre-audit draft said **six**; the
counting lens found two more (rows 7–8); P1's implementation found a ninth the audit's
`engine`-only enumeration structurally could not see (row 9, in `processtest`); and P4's sweep found
**four more** while editing the same files (rows 10–13). Two of those four are in **production**
code making the same claim as the test comments the audit did catch. *An enumeration of
enumerations rots exactly like the enumerations in it.*

Rows 2 and 3 were **already fixed in passing by P1**, which had to rewrite `HasArmedTimers`'s doc
comment to change its body — verified against `HEAD~1`, which carried both defects verbatim.

| # | site | the false claim | the truth |
|---|---|---|---|
| 1 | `engine/state.go:226` | "the auxiliary bookkeeping table for **all** scheduled timers" | 4 of 8 arm sites never touch it |
| 2 | `engine/state_timers.go:138` | "`timerRecord.Kind` is unexported" | `TimerKind` and the field are exported; only the **struct type** is not. A consumer compiled and ran `st.Timers[i].Kind == engine.TimerCompensationStall` externally. What a consumer cannot do is **construct** one |
| 3 | `engine/state_timers.go:141-145` | names **three** invisible sources, says "all four" | **four** invisible; **five** total |
| 4 | `engine/step_triggers.go:524-526` | "`s.Timers` holds deadline, in-wait and retry records" | omits `TimerCompensationStall`, dispatched **13** lines below at `:537` |
| 5 | `engine/step_nodes.go:1135-1136` | "the **third of the three** compensation dispatch sites" | there are **four**; ADR-0175 added the fourth in the delivery that wrote this |
| 6 | `engine/step_triggers.go:291` | "(ADR-0034 §2.5)" | ADR-0034 has **zero** `§`. ⚠ Not invented: `docs/specs/2026-06-23-compensation-on-error-cancel-design.md:108` really is `### 2.5`. A wrong-**document** attribution; fix to `ADR-0034 Decision 4`, the form already used at **four** other Go sites |
| 7 | `engine/state_timers.go:14` | "`Kind` discriminates intermediate, deadline, and in-wait timers" | names 3 of 5; omits `TimerRetry` and `TimerCompensationStall` — and names `TimerIntermediate`, which by §1 never lands in a record |
| 8 | `engine/step_compensation_stall_incident_test.go:79-88` | "path 4 sits AHEAD of the `!spawnsNewWork()` guard" | **falsified by ADR-0178** — path 4 gains a guard; the stall record survives via the exemption |

| 10 | `engine/step_timers.go:117` | `handleCompensationStallFired`'s doc: path 4 "sits ahead of the `!spawnsNewWork()` guard" | **falsified by ADR-0178**, in *production* code — the same claim as row 8, which the audit caught only in a test |
| 11 | `engine/state_timers.go:8-10` | the `timerRecord` type doc says intermediate-catch timers are recorded here, "a single, unified dispatch table" | no `TimerIntermediate` value is ever appended to `s.Timers` — the four append sites carry the other kinds |
| 12 | `engine/step.go:84` | "the three compensation dispatch sites" | **four**; reworded to name `armCompensationStallTimer`'s call sites rather than count them |
| 13 | `engine/step_compensation_stall_test.go:8, :144` | both assert "THREE compensation dispatch sites"; `:144` enumerates them and calls the enumeration exhaustive | **four** — `retryStalledCompensation` was added by ADR-0175's own commit |

⚠ Row 6 is a lone slip **as worded** (of 25 `§` citations in Go code, the only one attaching a `§`
to an ADR containing none). Three further *dangling ADR-section* references of the same family
exist — `scheduler/trigger.go:382`, `scheduler/trigger_test.go:554` ("ADR-0176 §4"),
`engine/state_compensation.go:391` ("ADR-0174 §5.3"). **Out of scope here; backlog entry.**

---

## §6 — Test plan, with what makes each test fail today

⚠ Check the **fixture**, not the assertion line. This repo has shipped six tests that could not
fail in one delivery.

**6.1 — `TimerWaiters` enumerates all five sources.** Fails today: the symbol does not exist
(compile error = valid RED). Each sub-case's fixture must declare a **real** arm node.
**Reclassification, three directions** (`TestTimerArmReclassifiesOnlyTheLowestPark`): (a) a timer
arm with no task/signal/message flips `ReasonAsyncChild` → `ReasonTimer`; (b) a boundary timer
**plus an awaited message** stays `ReasonMessage`; (c) ⚠ a boundary timer **plus an open user
task** stays `ReasonHumanTask` — the `openTasks` rung, which (b) does not pin.

⚠ **AMENDED AFTER `/code-review`.** Three directions are not enough: they pin only the rungs
**above** `timers`, and the widening's regression is on the rung **below** it. Five more rows, in
`TestSecondaryTimerArmDoesNotOutrankACommandWait`, pin the `commandWait` boundary — each asserting
both the `Reason` **and** the `AutoTimers()` decision it drives, because a `Reason` nobody acts on
is not what the contract is about:

| row | fixture | expected | fails today? |
|---|---|---|---|
| (a) | `svc[work] ⊸ bnd[timer 3h]`, action in flight | `async-child` + `Pass()` | **yes** — measured `timer` + `AdvanceTimers()` |
| (b) | event-sub-process timer arm + action in flight | `async-child` + `Pass()` | **yes** — same |
| (c) | plain timer intermediate catch | `timer` + `AdvanceTimers()` | no — regression guard |
| (d) | event-based gateway, timer arm only | `timer` + `AdvanceTimers()` | no — regression guard |
| (e) | retry backoff record | `timer` + `AdvanceTimers()` | no — regression guard |

⚠ Rows (c)–(e) are **not** decoration and are not vacuous: every one of their tokens also parks on
a command (`i-tm1`, `i-tm1`, `evtgw:i-t1`), asserted per-row by a `hasCommandWait` control, so any
predicate that simply lets a command wait win reddens all three. Four mutations are recorded, one
per clause of the predicate plus one restoring the adjudicated `AwaitTimer` form; each reddens a
different, non-empty subset of rows.

⚠ These fixtures are built with `engine.Step`, **not** the harness: the harness resolves an action
through its catalog synchronously, so a service task either completes in the same drive or —
measured with the action name absent — fails the whole instance (`status=failed`,
`reason=terminal`), and an in-flight command wait is unreachable that way.

**6.2 — the dying-instance guard.** Reminder, deadline **and** retry, each on a
`spawnsNewWork()==false` instance, asserting a retired record and — ⚠ **amended after
`/code-review`** — a command set of **exactly** `[CancelTimer{rec.TimerID}]` rather than the
originally prescribed "zero commands", which pinned a recurring-scheduler-job leak. Fails today:
measured, both before the guard (live `InvokeAction`s) and after it (`commands=[]`). ⚠ The fixture must be a **cancel-started** walk with
`require.False(t, engine.SpawnsNewWork(&st))` asserted before firing — a throw-walk fixture passes
regardless of the guard. ⚠ The retry sub-case is the `ASSUMPTION (unverified)` one: if it cannot be
constructed, say so in the plan's Progress block and downgrade the claim rather than assert it
untested. Plus the **exemption**: a stall timer still fires on a dying walk.

**6.3 — `Token.AwaitTimer` is set AND cleared.** Set at the arm site; cleared at all seven sites.
Fails today: no field. ⚠ The clear test is the one that catches the inverted-purpose defect — it
fires the timer, resumes, and asserts `HasArmedTimers()` is false afterwards.

**6.4 — duplicate start refused.** Three rows: (a) a second start is refused; (b) **control** — a
genuinely fresh instance still starts; (c) ⚠ a **zero-`OccurredAt`** start followed by a second
start is refused. Rows (a) and (b) both pass under the defective `StartedAt`-only predicate; row
(c) is the one that fails.

**6.5 — dropped cancel is truthful, and still propagates.** `engine.Step` returns
`ErrCancelNotApplicable`; `service` maps to **422**; the **deferred** site still returns `nil`.
⚠ **Plus the two child-propagation cases**, which fail today under a naive implementation and are
the reason this decision nearly shipped a regression: (a) a parent whose own cancel is dropped
still terminates its children; (b) a child whose cancel is dropped does not orphan its
grandchildren.

**Mutation duty.** Break the production line, observe RED, restore from a `cp` backup (⚠ never
`git checkout <path>` — restores from the index, has destroyed uncommitted work here twice),
`diff` to confirm.

---

## §7 — Where `HANDOVER.md` was wrong (six, all executed)

1. "a boundary or event-gateway timer arm is still invisible" — **under-counts**; four sources are,
   and the plain intermediate catch event appears in no enumeration of the invisible sources,
   including `HANDOVER.md`.
2. "`Park.HasArmedTimers` therefore inherits the defect" — **partial only**; `harnessEnv.classify`
   overrides it for plain-ICE under a `Harness`. The free `Classify` inherits it fully.
3. "`timerRecord.Kind` is unexported, so `processtest` physically cannot exclude a stall timer" —
   **false**, and committed in the source. Makes `TimerWaiters()` an ergonomics decision, not an
   impossibility fix.
4. "Ownership transfers on dispatch, so the record is consumed" — **false as a general claim**
   (moved to bundle C with ADR-0179).
5. "Measured `re-dispatched=[]` where `main` gave `[undoB undoA]`" — **wrong provenance**; that is
   ADR-0175's abandon-verb measurement (bundle C).
6. "`StartInstance` … restarts it from the top … not `Drive`" — **wrong twice** (§3a).

---

## §8 — Audit record and adjudication

Three Opus lenses (execution, failure-modes, re-counting), each in its own worktree. ⚠ **All three
found the bundle ABSENT at their worktree base** and recovered via the briefed `git merge` — the
step-0 instruction is what made this audit possible at all.

**27 findings, all accepted.** Full write-ups: `2026-08-13-adr-0177-0180-audit-lens-{a,b,c}.md`.

Landing in this bundle:

| finding | severity | what changed here |
|---|---|---|
| `Token.AwaitTimer` never cleared | CRITICAL | §1 — seven clear sites, `clearAwait()`, §6.3 |
| dropped-cancel error orphans the child subtree | CRITICAL | §3b — sentinel as reporting outcome; §6.5's two propagation cases |
| zero `OccurredAt` defeats the start guard | MAJOR | §3a — predicate widened; §6.4 row (c) |
| "a walk by definition does not spawn work" is false | MAJOR | §2 — measured closed set; fixture named |
| eight false comments, not six | MAJOR | §5 rows 7–8 |
| `rejectSilently` cited as the safety argument | MAJOR | §3b — category error, removed |
| KNOWN LIMITATION pin near-vacuous | MINOR | §1 — falsifier stated + complementary assertion |
| `walkScoped()` described as already existing | MINOR | §1 — it does not; this bundle introduces it |
| `slog.WarnContext` in six files | MINOR | §2 — **four** (six is the count for any `slog.`) |
| "no enumeration anywhere" over-reaching | MINOR | §7.1 narrowed |
| "all three" fixtures under-counts the boundary family | MINOR | §1, §6.1(c) |
| line rot: `state.go:225→226`, "twelve→13 lines", `cancel.go:80-88→83-90` | COSMETIC | §5, §3b |
| dangling ADR-§ refs elsewhere | NARROWED | §5 — real, but `scheduler`-side; backlog |

Moved to **bundle C** with ADR-0179: `DispatchedCmdIDs` swallowing live replies; `cloneState`
slice aliasing; the retry timer never retired at walk finish; the cause-of-death edit living in
`runtime`, not `engine`; the fifth dispatch site; `RetryAttempts` reset semantics; the boundedness
claim; the backoff-redelivery window; "reusing `retryStalledCompensation`" as an unexecuted
analogy; retirement sites being **five**, not four.

---

## §9 — Scope changes made by the audit

**ADR-0179 was removed from this bundle.** The audit put 4 of 6 Criticals and ~12 of 27 findings
on it alone, and its corrected design — a retry state machine, a redelivery window, a fifth
dispatch site, a `cloneState` fix, a `runtime` edit, a retirement-survival requirement — is a
delivery in its own right. It ships as **bundle C** with its own rule-#9 audit of the rewritten
design, because a design that just failed its audit is not an implementation input.

Also out of scope, deliberately: per-node compensation retry policy (backlog 4); backfilling
`AwaitTimer` onto stored rows (§1); the three dangling ADR-§ references in `scheduler` (§5);
narrowing `ProcessDriver.CancelInstance`'s `err=<nil>` on an already-terminal instance (§3b).

---

## §10 — Risks, migration, release notes

| risk | mitigation |
|---|---|
| `HasArmedTimers()` widening reclassifies parks and `AutoTimers()` fires new timers | §6.1's three directions |
| `AwaitTimer` set-but-never-cleared inverts the ADR | §6.3's clear test |
| a `Status`- or `StartedAt`-only start guard | §6.4 row (c) |
| `ErrCancelNotApplicable` orphaning children | §6.5's two propagation cases |

**Persisted-shape change**: `Token.AwaitTimer` only (additive; zero value correct for every other
node kind). ⚠ Persistence is whole-state `json.Marshal` with no `DisallowUnknownFields`, so new
fields round-trip on upgrade — and a **downgrade silently drops them**, after which a parked
plain-ICE token is invisible to `TimerWaiters()` again. Same class as the KNOWN LIMITATION.

**Release notes**: (a) `CancelInstance` may now return **422** where it returned 200 — **breaking**;
(b) a second `StartInstance` on a live instance is refused — breaking for anyone relying on the
current superimposition, which corrupts state; (c) `HasArmedTimers()` now reports boundary,
event-gateway, event-sub-process and plain-intermediate-catch arms.
