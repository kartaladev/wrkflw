# Structural terminal-trigger guard

- Date: 2026-08-05
- Status: design agreed with the owner (six decisions, recorded in §6).
  **Rule-#9 audited 2026-08-05** — three Opus auditors on separate lenses
  (mechanism correctness / claim-by-claim fact-check / gaps + contradictions),
  41 findings accepted, 1 rejected. The audit changed two design decisions
  (§6 items 5 and 6), corrected a false justification, and found three
  prescribed tests that could not have failed. Adjudications are in §9.
- Scope: `engine/`, plus **one line in `runtime/task/service.go`** (aliasing its
  existing `ErrTaskNotOpen` to the engine's — see §4). Two new exported error
  sentinels (`engine.ErrInstanceTerminal` §2.3, `engine.ErrTaskNotOpen` §4);
  one **breaking** change to the sealed `engine.Trigger` interface; no new port,
  no storage change, no transport change.
- ADR: **0165** (next free on `main`; latest shipped is 0164).
- Discharges the first of the three follow-up ADRs owed by delivery 2b
  (ADR-0164). The other two — incident-history retention and zombie scopes —
  stay open and are explicitly **out of scope** (§8).

## 0. Verification method

Every factual claim in §1 was **reproduced by execution** against `main` @
`8832021`, not by reading code. A throwaway black-box test
(`engine/zz_scratch_repro_test.go`, `package engine_test`) drove a real
definition through real `engine.Step` calls; the captured output is quoted
inline. The scratch file was deleted and the tree is clean — each case becomes a
RED test in the plan.

This method is not ceremony. It **refuted one of this design's own starting
claims**: the working hypothesis (carried from the 2b handover) was that
`HumanCompleted` on a terminal instance flips the instance to `Completed`. It
does not — the observed harm is different and narrower, and §1.3 states what was
actually measured rather than what was expected. Two prior deliveries recorded
the same lesson from the other direction; here it caught an over-claim *before*
it reached an ADR.

### The fixtures — there are TWO, and the difference matters

⚠ **Audit correction.** An earlier draft of this section called the first fixture
"the precondition for every case below". That was false for §1.4 and it mattered:
a plan written against it prescribed a signal test that **passes before any
implementation exists**, because the shared fixture parks its surviving token on
`AwaitCommand`, no token awaits a signal, and `handleSignalReceived` defers
`mergeVars` until a match is found. Two auditors reproduced both fixtures and
confirmed. Each case below names the fixture it used.

**Fixture A — `terminalGuardDef`** (used by §1.1, §1.2, §1.3). A parallel fork
into a human branch and a service branch. Failing the service action with no
matching boundary fails the **instance** while leaving the sibling token parked —
`engine/step.go:101` documents exactly this (`handleUnhandledError` "fails the
instance while LEAVING siblings parked").

```
Start → fork ⇉ { approve (UserTask) , svc (ServiceTask) }
```

**Fixture B — `terminalGuardSignalDef`** (used by §1.4). Identical, except the
human branch is replaced by a signal intermediate catch, so the surviving token
parks on `AwaitSignal` rather than `AwaitCommand`:

```
Start → fork ⇉ { await (IntermediateCatch, WithSignalName("approved")) , svc (ServiceTask) }
```

Measured post-condition of **Fixture A**, the precondition for §1.1–§1.3:

```
status=failed  tokens=2  tasks=1
  token i1-t2 node=approve state=TokenWaiting awaitCmd=i1-h1
  token i1-t3 node=svc     state=TokenWaiting awaitCmd=i1-c1
  task  i1-h1 state=cancelled open=false
```

Two facts follow, and both are load-bearing for the whole design:

1. **`endInstance` never clears `s.Tokens`.** It sweeps status, `EndedAt`, the
   compensation cursor, orphaned incidents, tasks and scheduled work
   (`engine/state.go:351-362`) — tokens survive. ADR-0164 Decision 3
   *deliberately* keeps a token that holds an incident, so this is not an
   oversight to fix here.
2. **`tokenAwaiting` (`engine/step_state.go:79-89`) matches on `AwaitCommand`
   alone** — no instance-status check, no token-state check. Every handler that
   resumes a token by command id therefore finds a live target on a dead
   instance.

## 1. Problems

Delivery 2b (ADR-0164) established the invariant **"a terminal instance is never
resumed"** and enforced it by hand-copying

```go
if s.Status.IsTerminal() { slog.WarnContext(...); return StepResult{State: *s}, nil }
```

into individual handlers. Three successive review passes found **1 → 2 → 5**
instances of the same resurrection defect, each increase arriving *after* an ADR
claimed the class was closed. Today **7 of 15** trigger handlers carry a guard
(`engine/step_triggers.go:98, 304, 461, 516, 852, 903, 1057`), **plus a
conditional eighth** in `stepCompensateRequested` (`step_compensation.go:129`) —
eight guard *sites* in total, which is the number the plan removes. The remaining
handlers are protected by convention only, and a handler added later gets nothing
by construction.

The four cases below cover **five** currently-unguarded triggers, all reproduced.
A sixth (`MessageReceived`) is argued by analogy in §1.4. The audit found a
**seventh** route, in a handler this spec had classified as deliberately
terminal-tolerant — see §3.2.

### 1.1 `StartInstance` resurrects a dead instance into an incoherent state

```
RESURRECTED: status failed → running, endedAt=2026-08-05 12:01:00 +0000 UTC
```

`handleStartInstance` (`engine/step_triggers.go:28-54`) unconditionally sets
`s.Status = StatusRunning`, stamps `StartedAt`, places a token and drives. It
never clears `EndedAt`, so the result is a **running instance carrying an end
timestamp** — a state no other path can produce and no reader expects.

Unreachable from the runtime: `ProcessDriver` always delivers `StartInstance` to
a freshly constructed state (`runtime/processdriver.go:436`,
`st := engine.InstanceState{InstanceID: instanceID}`), and `StatusRunning = iota`
so a zero state is never terminal. The exposure is the **public library API** —
`engine.Step` is exported and pure, and an embedding consumer holds the state.

### 1.2 `HumanClaimed` / `HumanReassigned` re-open a cancelled task

```
task i1-h1 state=claimed open=true   (was cancelled)
commands emitted: 1
   cmd engine.UpdateTask
```

`endInstance` → `cancelOpenTasks` (`engine/state.go:370-385`) sets every open
task to `humantask.Cancelled` precisely so a terminated instance stops appearing
in inbox queries. `handleHumanClaimed` (`:432`) and `handleHumanReassigned`
(`:482`) then find that task by id, overwrite `task.State = humantask.Claimed`,
and emit `UpdateTask` — **putting the task back into the consumer's inbox
projection on a dead instance**, where it can never be completed.

Their sibling `handleHumanCandidatesResolved` (`:455`) has *both* a terminal
guard and an `!task.IsOpen()` check, and documents the reason: "once a task is
completed or cancelled, its candidate list is part of the audit record and a late
refresh must not rewrite history." Claim and reassign have neither. `grep` finds
no test pinning either behaviour.

**Reachability — narrower than it first appears, and stated precisely.**
`service.ClaimTask`, `CompleteTask`, `ReassignTask` and `RefreshTaskCandidates`
all route through `deliverTaskTrigger` (`service/service.go:544`), which
**already** rejects both keys before reaching the engine — `task.IsOpen()` at
`:549` and `isTerminal(st.Status)` at `:556`, both `ErrConflict`. So §1.2 and
§1.3 are **not** reachable through the `service/` facade. They are reachable:

- from a **direct `engine.Step` consumer** — which is the primary product
  surface, not a corner case: CLAUDE.md states the deliverable is the
  module-root public API and "every feature must be reachable and ergonomic
  through that API";
- from `runtime.ProcessDriver.ApplyTrigger`, which has no such pre-check;
- and through the **TOCTOU window in the service pre-check itself**: it tests a
  snapshot loaded at `:552`, then calls `ApplyTrigger` at `:559`, which re-loads
  under CAS. An instance that terminates between the two passes the check.

That last point is why the guard belongs in the engine regardless: the service
pre-check is best-effort defence-in-depth, and the engine is the only layer that
sees the state it actually mutates.

### 1.3 `HumanCompleted` executes post-mortem and falsifies the audit record

The expected finding was a `Failed → Completed` status flip. **That is not what
happens.** Measured delta:

```
BEFORE status=failed endedAt=12:01 tokens=2   task i1-h1 state=cancelled
AFTER  status=failed endedAt=12:01 tokens=1   task i1-h1 state=completed
   completion={Actor:{ID:alice} At:13:00 Outcome: Note:}
COMMANDS=1
   engine.UpdateTask {Task:{... State:completed ...}}
HISTORY=5
   visit approve tok=i1-t2 left=13:00
   visit end-a   tok=i1-t2 entered=13:00 left=13:00      ← appended post-mortem
```

So on a terminal instance `handleHumanCompleted` (`:635`):

- completes a **cancelled** task and stamps a `Completion` naming an actor at a
  time **59 minutes after the instance died**;
- advances the token `approve → end-a` and consumes it;
- appends a **post-mortem history visit**, corrupting the audit trail that
  `service/`'s `ProcessInstance` view renders;
- emits `UpdateTask{State: completed}` to the consumer-supplied `TaskStore`.

The instance status did **not** change here only because the sibling `svc` token
is still parked, so the end event does not complete the instance. Whether a
status flip is reachable is **topology-dependent and was not reproduced** — this
spec does not claim it.

This also **refutes carve-out #3 as the 2b handover states it.** "`HumanCompleted`
must keep erroring" is not a property of the handler: it errors only when
`tokenAwaiting` returns nil, i.e. only when the token happened to be swept. When
a token survives — the case ADR-0164 Decision 3 deliberately creates — it does not
error at all. The carve-out is real (the external caller must be told), but it is
currently **incidental, not guaranteed**. This delivery makes it guaranteed.

### 1.4 `SignalReceived` / `MessageReceived` advance tokens and mutate variables

**Fixture B**, not A — see the ⚠ above. On Fixture A this delivery is already a
clean no-op today, so a test built on A could never go red.

```
BEFORE status=failed tokens=2 history=4
signal err=<nil>
AFTER  status=failed tokens=1 history=5 vars=map[x:1]
   visit end-a tok=i1-t2 left=13:00        ← appended post-mortem
```

`handleSignalReceived` (`:725`) merges the trigger payload into `s.Variables`,
advances the parked token and drives — on a dead instance.

⚠ **`MessageReceived` was NOT reproduced.** It has the same shape via
`dispatchArmCascade` and the standalone point-to-point branch (`:961`), and is
classified by that analogy. The reproduced set is therefore **four cases covering
five triggers** (`StartInstance`, `HumanClaimed`, `HumanReassigned`,
`HumanCompleted`, `SignalReceived`); `MessageReceived` is the sixth, argued from
code shape. Every count elsewhere in this bundle must match that sentence — an
earlier draft said "five of seven reproduced", which double-counted
`StartInstance` and asserted `MessageReceived`.

This pair is also why the ordering matters: **delivery 3 (ADR-0158) makes one
broadcast signal fire *every* matching arm per family.** It multiplies traffic
through exactly this unguarded path, so this guard ships first.

## 2. Design

### 2.1 The mechanism: policy as a method on the sealed `Trigger` interface

`Trigger` is sealed by `isTrigger()` (`engine/trigger.go:14-17`), and every
trigger type embeds `baseTrigger`, which supplies it. Adding a policy method
**with** a `baseTrigger` default would let a new trigger inherit silently — the
same "protected by convention" failure this delivery exists to remove. So the
method is added to the interface and **deliberately not defaulted**:

```go
// terminalPolicy reports how Step must treat this trigger when the instance is
// already terminal. It is unexported: the Trigger interface is sealed, so every
// implementation lives in this package and must state its own policy. There is
// deliberately NO baseTrigger default — a new trigger type does not compile
// until its author has made this decision.
type terminalPolicy int

const (
    rejectSilently   terminalPolicy = iota // log and return the state unchanged
    rejectWithError                        // return ErrInstanceTerminal
    allowOnTerminal                        // the handler runs; it expects a terminal instance
)
```

`rejectSilently` is `iota` so that the zero value is the safe one, even though no
path can reach a zero policy while the interface stays undefaulted.

### 2.2 The enforcement point

One check in `dispatch` (`engine/step.go:120`), ahead of the type switch:

```go
func dispatch(ctx context.Context, def *model.ProcessDefinition, sp *InstanceState, trg Trigger, opt StepOptions) (StepResult, error) {
    if sp.Status.IsTerminal() {
        switch trg.terminalPolicy() {
        case rejectSilently:
            slog.WarnContext(ctx, "trigger rejected on terminal instance",
                "instance_id", sp.InstanceID,
                "trigger", fmt.Sprintf("%T", trg),
                "status", sp.Status.String(),
            )
            return StepResult{State: *sp, Commands: nil}, nil
        case rejectWithError:
            return StepResult{}, fmt.Errorf("%w: %v", ErrInstanceTerminal, sp.Status)
        case allowOnTerminal:
            // fall through
        }
    }
    switch t := trg.(type) { /* unchanged */ }
}
```

It sits in `dispatch` rather than `Step` so it runs **after** `validateTriggerKey`
and `cloneState` — preserving today's precedence, in which a malformed trigger
(ADR-0152, `ErrEmptyTriggerKey`) is rejected before any state-dependent check.

`StatusCompensating` is **not** terminal (`engine/state.go:54-61`), so every
in-flight compensation walk is unaffected — the same reasoning ADR-0164's
per-handler guards already rely on.

**The seven existing per-handler guards are then removed.** Once `dispatch`
rejects, the bodies at `engine/step_triggers.go:98, 304, 461, 516, 852, 903, 1057`
and the compensation check at `step_compensation.go:129` become unreachable.
Leaving them would recreate the drift this delivery removes — two places stating
the same policy, free to disagree. Removal is the load-bearing risk of this
change, so each removal must be **mutation-verified**: delete the `dispatch`
check, confirm the corresponding regression test goes RED, restore. A guard whose
removal breaks nothing was never pinned (delivery 2a found exactly that: removing
a shipped call site broke zero tests).

### 2.3 The instance-status sentinel

(The second sentinel, `ErrTaskNotOpen`, belongs to the task-lifetime key and is
introduced with it in §4.)

```go
// ErrInstanceTerminal reports that a trigger requiring a live instance was
// delivered to one that has already reached a terminal status. Wrapped in
// ErrInvalidTransition so errors.Is holds for both sentinels.
var ErrInstanceTerminal = fmt.Errorf(
    "workflow-engine: instance is terminal: %w", ErrInvalidTransition)
```

**It wraps `ErrInvalidTransition`, and that resolves the layering question by
itself.** `ErrInvalidTransition` (`engine/errors.go:8-13`) is documented as
classifying "a trigger that cannot be applied because the targeted
instance/token is not in a state that accepts it" — exactly this condition. The
wrapping form is the established house pattern:
`ErrTokenNotFound = fmt.Errorf("...: %w", ErrInvalidTransition)` (`:23`).

The payoff is that **no `service/` or `transport/` change is needed**:
`transport/http/httpcore/errors.go:48` already maps `engine.ErrInvalidTransition`
to **422 `conflict_state`**, and `service/service.go:561` already re-classifies it
as `ErrConflict`. A new admin-visible failure for `ResolveIncident` (§2.4) reaches
the caller correctly through machinery that already exists.

`ErrTaskNotOpen` (§4) wraps it for the same reason and lands on the same 422,
matching the `ErrConflict: task %q is not open` the service layer already returns
at `service/service.go:550`.

### 2.4 The classification

Owner-approved 2026-08-05, amended at the rule-#9 audit. **7 preserved, 8
changed.**

| Trigger | Policy | vs shipped |
|---|---|---|
| `StartInstance` | `rejectWithError` | **change** — §1.1 |
| `ActionCompleted` | `rejectSilently` | preserved (guard added by ADR-0164) |
| `ActionFailed` | `rejectSilently` | preserved (guard added by ADR-0164) |
| `CancelRequested` | `rejectSilently` | **change** — §3.2, owner-decided AT the audit |
| `CompensateRequested` | payload-dependent, see §3.1 | policy preserved (carve-out #1) + a new in-handler record guard, §3.2 |
| `HumanCandidatesResolved` | `rejectSilently` | preserved (guard **predates** ADR-0164) |
| `HumanClaimed` | `rejectWithError` | **change** — §1.2 |
| `HumanReassigned` | `rejectWithError` | **change** — §1.2 |
| `HumanCompleted` | `rejectWithError` | **change** — §1.3, carve-out #3 made guaranteed |
| `TimerFired` | `rejectSilently` | preserved (guard **predates** ADR-0164) |
| `SignalReceived` | `rejectSilently` | **change** — §1.4 |
| `MessageReceived` | `rejectSilently` | **change** — §1.4 (by analogy, NOT reproduced) |
| `SubInstanceCompleted` | `rejectSilently` | preserved (guard added by ADR-0164) |
| `SubInstanceFailed` | `rejectSilently` | preserved (guard added by ADR-0164) |
| `ResolveIncident` | `rejectWithError` | **change** — owner-decided, §6 item 3 |

Of the 8 changed: five close **reproduced** routes (`StartInstance`,
`HumanClaimed`, `HumanReassigned`, `HumanCompleted`, `SignalReceived`), one closes
an analogous route (`MessageReceived`, §1.4), one is the owner's admin-visibility
call (`ResolveIncident`), and one closes a route **the audit found**
(`CancelRequested`, §3.2).

⚠ `CompensateRequested` is "preserved" on *policy* only — its rejection **error
message changes** (§3.1), so it is not byte-identical.

⚠ Two "preserved" guards — `TimerFired` and `HumanCandidatesResolved` — **predate
ADR-0164**, which added only five. ADR-0164 states this itself; an earlier draft
of this spec attributed all seven to it.

**The axis is who delivers the trigger and whether that caller can distinguish
success from a no-op.** Async engine-originated echoes cannot and must stay
silent; synchronous external API calls must be told. This is not a stylistic
choice — §3.4 shows shipped code that breaks under the alternative.

## 3. The carve-outs

### 3.1 `CompensateRequested` — payload-dependent, and the mechanism absorbs it

The shipped guard (`engine/step_compensation.go:129-131`) rejects **only** when
the trigger expresses resume intent, and deliberately allows a plain full
rollback on a terminal instance. A per-**type** policy table cannot express that.
A method on the concrete type can, because it reads its own receiver:

```go
func (t CompensateRequested) terminalPolicy() terminalPolicy {
    if t.ReverseNode != "" || t.ToNode != "" {
        return rejectWithError // resume intent — cannot resume a terminal instance
    }
    return allowOnTerminal // plain full rollback must still work
}
```

**Error-precedence check.** Hoisting the guard from `:129` to `dispatch` moves it
ahead of two pure trigger-shape validations at `:83` and `:97`. Neither changes
outcome, because both malformed shapes carry `ReverseNode == "" && ToNode == ""`
and therefore classify `allowOnTerminal`, falling through to the same errors:

- `{ResetVars: true, ReverseNode: ""}` → still `"ResetVars requires ReverseNode"`.
- `{RestoreTargetVars: true, ToNode: ""}` → still `"RestoreTargetVars requires ToNode"`.

⚠ **Audit correction — that proof only covers the *minimal* malformed shapes.**
`CompensateRequested` is a public directly-constructible struct (which is the
whole reason the `:83`/`:97` guards exist), so a hand-built value can carry both a
malformed flag *and* a resume field. On a terminal instance those now report
terminal **first**:

- `{ResetVars: true, ToNode: "svc"}` — today `"ResetVars requires ReverseNode"`;
  after, `ErrInstanceTerminal` (because `ToNode != ""`).
- `{RestoreTargetVars: true, ReverseNode: "start"}` — today
  `"RestoreTargetVars requires ToNode"`; after, `ErrInstanceTerminal`.

This is arguably an *improvement* — the shape errors carry no sentinel and
currently classify **500**, while `ErrInstanceTerminal` classifies 422 — but the
change is real and must be pinned rather than asserted away. Both counterexamples
join the plan's pin list.

The in-flight-walk check at `:114` keys on `StatusCompensating`, which is not
terminal, so it is unreachable from the guarded path. The plan must pin all three
with tests; the existing `:129` check is then removed as dead code, and its
message is preserved by wrapping `ErrInstanceTerminal`.

### 3.2 `CancelRequested` — the audit overturned this carve-out

⚠⚠ **This section previously argued for `allowOnTerminal` on a justification that
is factually false.** It claimed `runtime/processdriver_cancel.go:83` "cancels
child instances in a loop where an error would abort the sweep partway". The
function's own doc comment says the opposite: *"Every error is logged and
swallowed — this is best-effort only (ADR-0032)."* The loop `continue`s. The only
real loss on error is recursion into that child's own subtree, so grandchildren
are skipped — not "the remaining children".

Worse, the argument never discriminated: **`rejectSilently` also returns `nil`**,
so it satisfies the child loop exactly as well. The bundle had chosen the
strictly more dangerous of two indistinguishable options.

**And `handleCancelRequested` is destructive on a terminal instance — route #7:**

- `forceTerminate` (`engine/step_nodes.go:548-557`) nils `s.Tokens` but **never
  clears `RootCompensations`**; `endInstance` doesn't either (the only clears are
  at walk finish, `step_compensation.go:459,488`).
- `handleCancelRequested:226` then branches on
  `len(s.RootCompensations) > 0 || len(s.ArchivedCompensations) > 0` **with no
  terminal check**, sets `s.Status = StatusCompensating` — the instance
  **leaves terminal status** — and re-runs the whole walk, re-emitting
  `InvokeAction` for every record. Money-moving compensation actions fire against
  a dead instance.
- The walk ends in `applyTerminate` → `endInstance(StatusTerminated, …)`,
  **overwriting** a `Completed`/`Failed` status and its `EndedAt`.
- At that step `prevStatus == StatusCompensating`, so `terminalOutboxEvent`
  (`runtime/outbox.go:34`) does **not** suppress: a **second terminal event** is
  published for an instance that already published its first, and
  `instActive.Add(ctx, -1)` (`runtime/processdriver.go:650`) fires twice, driving
  the active-instance gauge negative.

**Owner decision at the audit: narrow both carve-outs.**

1. **`CancelRequested` becomes `rejectSilently`.** Nothing is lost — the child
   loop is satisfied identically — and route #7 closes.
2. **`CompensateRequested` keeps `allowOnTerminal`** for the plain-rollback
   carve-out, but `stepCompensateRequested` gains a **narrow in-handler guard**
   rejecting a walk when the instance is terminal *and* compensation records
   survive. The policy method reads the trigger, not the state, so this one
   state-dependent condition stays where it can see the state.

Without (2), carve-out #1 carries the same exposure by the same mechanism.

⚠ `CancelRequested`'s godoc (`engine/trigger.go:396-401`) currently asserts *"no
harmful side effects occur since there are no live tokens or timers to cancel"* —
false on both halves. It must be corrected; the plan lists it.

### 3.3 `HumanCompleted` — the external caller must be told

`rejectWithError`, reached from `runtime/task/service.go:240` via the public
`service.CompleteTask`. As §1.3 shows, today's error is incidental; this makes it
a guarantee.

### 3.4 Why not "error for everything" — refuted by shipped code

`calllink.CallNotifier` (`runtime/calllink/notifier.go:212-215`):

```go
derr := n.deliver(ctx, parentDef, p.Link.ParentInstanceID, trg)
if derr != nil && !errors.Is(derr, engine.ErrTokenNotFound) {
    continue // leaves the link claimable for retry
}
```

A `SubInstance*` rejection returning a *new* sentinel is neither `nil` nor
`ErrTokenNotFound`, so the link is never marked notified and **redelivers
forever**. `engine/step_triggers.go:848-851` already documents this as the reason
2b's guard returns `nil`. Similarly `runtime/signal/signalbus.go:179-184` fans out to N
instances and `errors.Join`s failures, so an error for one terminal instance
would fail the **whole broadcast** for the publisher.

Conversely "silent for everything" would make `service.CompleteTask` report
success having done nothing.

## 4. Companion fix — the task-lifetime guard

Independent of instance status, and a **second key** the guard in §2.2 cannot
see. ADR-0163 cancels a human task via an interrupting boundary while the
**instance keeps running**, so a closed task exists on a live instance.

⚠⚠ **The audit rewrote this section.** The original design had three defects at
once, all confirmed by execution.

### 4.1 Where the guard goes — and where it CANNOT go

Add an `!task.IsOpen()` early-return, mirroring `handleHumanCandidatesResolved`'s
existing one (`engine/step_triggers.go:468-470`), to `handleHumanClaimed` (`:432`)
and `handleHumanReassigned` (`:482`). Both look the task up by id alone, so the
guard is reachable and genuinely needed — an auditor drove an interrupting
message boundary on a **running** instance and got:

```
AFTER BOUNDARY status=running  task i1-h1 state=cancelled open=false
CLAIM     err=<nil> commands=1 taskState=claimed      ← guard genuinely needed
REASSIGN  err=<nil> commands=1 taskState=claimed      ← guard genuinely needed
COMPLETE  err=workflow-engine: no token awaiting command: … "i1-h1"
```

That last line is the defect the audit found. **`handleHumanCompleted` looks the
TOKEN up first** (`:636`, `tokenAwaiting`) and returns `ErrTokenNotFound` at
`:638` before `TaskByID` at `:644` ever runs — so a guard placed "after the
`task == nil` check" is **dead code**. And no running-instance state reaches it:
every path that closes a task on a live instance also detaches its token
(`cancelTokenWaits` consumes the token, `step_cancel.go:39,58`; deadline expiry
clears `tok.AwaitCommand` *before* cancelling, `step_timers.go:83,89`;
`dropStaleTokenCommands` only closes tasks whose awaiter is already gone,
`step_stale_commands.go:166`). The one state where a closed task coexists with a
token awaiting it is `endInstance` → `cancelOpenTasks`, i.e. terminal — which
§2.2's guard now short-circuits.

**Owner decision: reorder `handleHumanCompleted` to resolve `TaskByID` before
`tokenAwaiting`.** That makes the guard reachable and upgrades a vague
`ErrTokenNotFound` to a precise `ErrTaskNotOpen` on the deadline-breach path. It
is a deliberate error change and is pinned by the plan.

### 4.2 All three error — the axis applies to both keys

The original design had claim and reassign return a **silent no-op** while
completion errored. The audit called that out as contradicting this ADR's own
stated axis: `service.ClaimTask`, `ReassignTask` and `CompleteTask` are the *same*
synchronous caller on the *same* `deliverTaskTrigger` route, so "the caller must
be told" applies identically. `service/service.go:550` already returns
`ErrConflict: task %q is not open` for exactly this.

**Owner decision: all three return the sentinel.**

### 4.3 The sentinel — unify, don't duplicate

⚠ **`ErrTaskNotOpen` already exists**: `runtime/task/service.go:46`,
`var ErrTaskNotOpen = errors.New("workflow-runtime: task is not open")`, returned
by `TaskService.RefreshCandidates` for the identical condition and documented on
the public `service.RefreshTaskCandidates` (`service/service.go:453`). An earlier
draft introduced a second same-named sentinel and justified it only against
`humantask.ErrTaskNotFound`, never mentioning the collision. Two sentinels for one
condition would mean `errors.Is(err, task.ErrTaskNotOpen)` returning **false** for
the engine's version.

**Owner decision: define it once in `engine`, and alias the runtime one.**

```go
// engine/errors.go — wrapped, so httpcore maps it to 422 like every other
// wrong-state transition.
ErrTaskNotOpen = fmt.Errorf("workflow-engine: human task is not open: %w", ErrInvalidTransition)
```

```go
// runtime/task/service.go — runtime already imports engine.
var ErrTaskNotOpen = engine.ErrTaskNotOpen
```

The alias also **fixes a live defect**: the runtime sentinel is currently
unwrapped, so `RefreshCandidates` on a closed task falls through
`httpcore.ClassifyError` to a **500 with an empty body**. After the alias it is a
422, consistent with the rest of the family.

It stays deliberately distinct from `humantask.ErrTaskNotFound` (used at `:646`
for a genuinely absent record): a closed task is present, and a caller must be
able to tell "no such task" from "too late".

## 5. Testing strategy

Hot paths first (Golang rule #8), and every test written RED before its
implementation.

1. **The exhaustiveness guarantee.** The compile-time half needs no test — it *is*
   the undefaulted interface method: a new trigger type fails to build at
   `dispatch`'s type switch. Pin the half that can silently rot (someone later
   adding a `baseTrigger` default) with a table naming all 15 concrete trigger
   types and their expected policy — the executable form of §2.4.

   ⚠ **Audit correction.** An earlier draft claimed this table "fails to compile
   if a type is added without a policy, because the table entry cannot be
   written". **That is false** — a 16th trigger simply omitted from the table
   compiles and the table passes green. The table needs its own rot guard, and
   the repo already has the right one: `TestValidateTriggerKindsAreExhaustive`
   (`engine/trigger_validate_test.go:82-125`) maintains an `all []Trigger` slice
   in `package engine` and length-asserts against it. **Derive the policy table
   from that slice, or assert its length against it**, so one list drives both.
   Note this would otherwise be the *third* hand-maintained 15-item trigger list
   (with `store.AllTriggerKinds`).
2. **The behavioural exhaustiveness table — one case per trigger, all 15.**
   (`table-test` skill: `assert` closures, not `want`/`wantErr` fields.) Drives
   every trigger against a terminal instance and asserts the *outcome*, not the
   policy value: state unchanged and `Commands == nil` for `rejectSilently`;
   `errors.Is(err, ErrInstanceTerminal)` for `rejectWithError`; handler reached
   for `allowOnTerminal`.

   ⚠ **The audit found this test missing from the plan entirely**, and with it the
   only engine-level coverage of `ResolveIncident` — the one behaviour change
   that is a pure owner decision rather than a reproduced defect. Without it,
   `ResolveIncident`'s silent→error flip was exercised *only* by a cross-layer
   `service`+`httpcore` test outside `engine/`, which may need containers and is
   gated on owner permission. It is now an explicit plan task.
3. **The four reproductions from §1 become regression tests**, using the §0
   fixture verbatim so each starts RED against current `main`.
4. **Carve-out pins**: the two `CompensateRequested` shape errors and the plain
   full rollback on a terminal instance (§3.1); the cancel-loop path (§3.2).
5. **Cross-layer pins**: the call-link notifier still retires a link when the
   parent is terminal (§3.4); `signalbus.Publish` returns `nil` when one target
   of a fan-out is terminal.
6. **The error-wrapping chain (§7).** Assert `errors.Is(err, engine.ErrInvalidTransition)`
   holds on the error returned by `service.ResolveIncident` against a terminal
   instance, and that `httpcore` maps it to 422. This is the test that makes §7's
   "no `service/` change needed" claim honest rather than assumed — a single `%v`
   on the path downgrades it to 500 silently, and nothing else would catch that.
   ⚠ `errors.Is(ErrInstanceTerminal, ErrTokenNotFound)` must be **false**: both
   wrap `ErrInvalidTransition` as siblings, and the call-link notifier's
   idempotency branch keys on `ErrTokenNotFound` specifically. Pin it.
7. **Mutation-verify every load-bearing test** — break the implementation on
   purpose, confirm RED, restore. Two prior deliveries shipped tests that could
   not fail, four of them originating in the plan's own test text. **This bundle's
   own audit found three more before implementation** (§9), which is the whole
   argument for the technique.

Floor: `engine` ≥ 85% (currently 91.8%; the floor is not the target).

## 6. Decisions taken this session

All six owner-decided 2026-08-05. Items 1-4 predate the audit; items 5-6 were
taken *because of* it. Do not re-litigate.

1. **Scope**: structural instance-status guard **plus** the narrow task-lifetime
   companion fix (§4). Two invariants, two mechanisms, one bundle — rejected both
   unifying them into a speculative "record lifetime" abstraction and deferring
   the task fix to the backlog.
2. **Rejection outcome**: three-valued (§2.1), preserving the existing
   silent/error split as a deliberate axis.
3. **`ResolveIncident` → `rejectWithError`**: today 2b's guard silently refuses
   an admin resolve, so the admin sees success while the incident stays
   unresolved. Its caller (`runtime/processdriver_incident.go:24`) propagates
   straight out with no retry loop behind it.
4. **Mechanism**: method on the sealed `Trigger` interface, no `baseTrigger`
   default (§2.1) — chosen over a fail-closed table in `dispatch` (two switches
   to keep in sync) and a test-only exhaustiveness check (does not enforce in
   `Step`, which the owner-decided scope requires).
5. **Narrow both terminal-tolerant carve-outs** (§3.2). `CancelRequested` becomes
   `rejectSilently`; `CompensateRequested` keeps `allowOnTerminal` but gains a
   narrow in-handler guard for surviving compensation records. Taken after the
   audit proved the original justification false and the route live. Rejected:
   keeping both and documenting the exposure (ships a known route), and making
   `terminalPolicy` state-aware (stops it being a property of the trigger and
   invites the sprawl this ADR removes).
6. **The task-lifetime guard errors on all three handlers, and the sentinel is
   unified** (§4). Rejected: silent no-op with the sentinel dropped (leaves the
   axis inconsistent), and keeping the spec's split with only a rename (two
   sentinels for one condition).

## 7. The `service/` question — resolved, no change needed

The 2b handover asks whether `service/` should surface "instance is terminal" for
an admin `ResolveIncident` the engine now refuses. Source-verification answers it
without widening this bundle's scope:

- **Wrapping `ErrInvalidTransition` (§2.3) is sufficient.** `service.ResolveIncident`
  (`service/service.go:481-495`) has no terminal pre-check and simply wraps the
  driver error with `%w`, preserving the chain — so
  `transport/http/httpcore/errors.go:48` maps it to **422 `conflict_state`**
  automatically. The admin gets told, through existing machinery.
  ⚠ Precision: `service/` does **not** re-classify for this route. The
  `errors.Is` test at `:561` and its `ErrConflict` re-wrap at `:562` live in
  `deliverTaskTrigger` only; `ResolveIncident` relies solely on `httpcore`'s arm.
  An earlier draft blurred the two.
- **The four human-task routes need nothing.** `deliverTaskTrigger` already
  rejects both keys ahead of the engine (§1.2), and re-classifies
  `ErrInvalidTransition` as `ErrConflict` at `:561-562`.

So the bundle stays `engine/`-only. **What the audit should verify** is the claim
above rather than re-open the decision: specifically that `%w`-wrapping survives
every hop from `handleResolveIncident` through `ProcessDriver.ResolveIncident` and
`service.ResolveIncident` to the transport mapper, since a single `%v` anywhere on
that path silently downgrades the response to 500.

## 8. Explicitly out of scope

- **Incident-history retention** — 2b's second owed ADR. `forceTerminate` and
  cancel's immediate branch still erase incident history. Untouched here.
- **Zombie scopes** — 2b's third owed ADR. Four terminal transitions set a
  terminal status without pruning `s.Scopes`, and ADR-0162 ships a now-stale
  sentence claiming `endInstance` closes them. Untouched here.
- **Clearing `s.Tokens` on terminal transitions.** Tempting, since it would kill
  the whole class at the root — but ADR-0164 Decision 3 deliberately keeps
  incident-holding tokens, and pruning interacts with `archiveCompensations`, the
  persisted snapshot shape and the `service/` audit view. This guard makes token
  survival safe rather than removing it.
- **Delivery 3 (ADR-0158)** signal fan-out, which ships after this.
- **The `processtest` `Classify` gap** (`processtest/park.go:107`), which blocks
  consumers from testing delivery 3's headline scenario. Separate backlog item.

## 9. Audit record (rule #9, 2026-08-05)

Three Opus auditors, one bundle, separate lenses: **mechanism correctness**,
**claim-by-claim fact-check** (71 claims), **gaps and cross-document
contradictions**. 42 findings; **41 accepted, 1 rejected**.

Five findings were raised independently by two or three auditors — the strongest
signal in the set, and all five were real.

### What the audit changed in the design (not just the prose)

1. **`CancelRequested` reclassified** `allowOnTerminal` → `rejectSilently`, and a
   new in-handler record guard added to `stepCompensateRequested`. The original
   justification was factually false and the route (#7) was live. §3.2.
2. **The task-lifetime guard reshaped** — all three handlers error, the
   `handleHumanCompleted` lookup order flips, and the sentinel is unified with
   the pre-existing `runtime/task.ErrTaskNotOpen`. §4.

### Three prescribed tests that could not have failed

This is the category that matters most, because each would have shipped as
coverage while proving nothing:

- **The Phase 4 mutation loop was a no-op mutation.** It disabled the `dispatch`
  check while every handler's own guard was still present — restoring exactly
  `main`'s behaviour. Seven identical green runs, read as "seven unpinned
  handlers". The procedure is now inverted: delete the handler guard first, *then*
  disable `dispatch`.
- **The signal test asserted a no-op that already holds.** Built on Fixture A,
  where no token awaits a signal. §0 now names both fixtures.
- **`TestHumanCompletedOnClosedTaskErrors` could never pass** — the guard it
  targeted was unreachable. §4.1.

### One finding rejected

Auditor 2 judged that correcting the `propagateCancel` claim leaves "the carve-out
conclusion unaffected". Rejected: auditor 1 and an independent reading show the
conclusion *is* affected, because `rejectSilently` satisfies the child loop
equally well — which is precisely what makes `allowOnTerminal` an unforced risk.

### Damage the audit prevented in the plan

- `liveAwaiters`' terminal guard (`step_stale_commands.go:59`) was absent from
  Phase 4.3's expected-grep list; an implementer following it literally would have
  deleted it and **re-opened ADR-0161's stale-command class**.
- Spec §4's sentinel snippet was unwrapped (`errors.New`) while §2.3 and the plan
  used the wrapping form — the bare one falls through to **HTTP 500**.
- `event.WithMessageName` does not exist (`WithMessageCorrelator` does).
- The plan quoted commit `c959b4e`, orphaned by its own amend — the exact trap
  CLAUDE.md rule #10 warns about.

### Accepted but deferred to the plan, not this spec

Shipped-ADR amendments (ADR-0164 and ADR-0109 both go stale), a `CHANGELOG.md`
breaking-changes entry, godoc for all 15 trigger types plus the interface, the
three stale in-body comments left by the guard removals, a `processtest` pin, and
a paragraph confirming the trigger codec and journal are unaffected.
