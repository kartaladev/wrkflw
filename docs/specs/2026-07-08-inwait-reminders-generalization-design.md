# In-wait reminders for ReceiveTask & IntermediateCatchEvent + Lint + rename

Status: Approved (design), 2026-07-08. Next: implementation plan.

## Problem

In-wait *reminders* (`WithReminder`) — a recurring fire-once action that nudges
while a token is parked — are only ever **armed for `UserTask`**. The engine reads
reminders generically (`model.ReminderOf`, via the structural `reminder()` on
`WaitFields`, which both `ActivityFields` and `IntermediateCatchEvent` carry), but:

- The **arm** site lives only in `userTaskStrategy.enter` (`engine/step_nodes.go`).
- `event.WithCatchReminder` exists and round-trips through serialization but the
  engine's `intermediateCatchEventStrategy` never arms it — **a dead option**.
- `ReceiveTask` parks awaiting a message (`receiveTaskStrategy` → `TokenWaitingCommand`,
  `AwaitMessage`), accepts `activity.WithReminder`, but the reminder is **never armed**.
- No validation surfaces the silently-ignored cases (worst failure mode: no error,
  no effect).

Goal: make in-wait reminders work for the other parking kinds
(`ReceiveTask`, `IntermediateCatchEvent`), surface unsupported-option usage as a
non-fatal warning, and rename `WithReminder` → `WithWaitReminder`.

## Non-goals

- Type-safe per-activity-kind options (compile-time rejection of invalid options)
  — deferred to its own future project; `Lint` is the interim guard.
- Reminders on non-parking kinds (`ServiceTask`/`SendTask`/`BusinessRuleTask`) —
  they execute synchronously and never park, so a reminder is meaningless there;
  `Lint` warns instead.
- Changing reminder *semantics* for `UserTask` (behaviour-preserving).

## Design

### 1. Renames (mechanical, behaviour-preserving, compiler-verified)

- `activity.WithReminder` → `activity.WithWaitReminder`. Matches the ADR-0103
  boundary-enhancements spec (`WithReminder` → `WithWaitReminder`).
- `event.WithCatchReminder` → `event.WithCatchWaitReminder` (same concept; keeps
  the two option names parallel). *(Confirm at spec review.)*

Update all call sites, README/docs snippets, godoc cross-refs. No behaviour change.

### 2. Generalize in-wait reminders (engine core — stays pure)

The **fire** path is already kind-agnostic (`handleReminderFired` resolves the
reminder via `model.ReminderOf(node)`). Only **arm**, **staleness**, and **cancel**
are `UserTask`-specific.

**Arm.** Extract the reminder-arm block from `userTaskStrategy.enter` (resolve
`ReminderEvery` via `ResolveTrigger`; if non-zero, emit
`ScheduleTimer{Kind: TimerInWait}` and append a `timerRecord{Kind: TimerInWait,
Token, TaskToken, NodeID, ScopeID}`) into a shared helper, e.g.

```go
// armWaitReminder appends the ScheduleTimer command + timer record for a node's
// in-wait reminder, if one is configured. cancelKey is the token whose resume
// must cancel the reminder (the parked token for ReceiveTask/catch; the task
// token for UserTask).
func armWaitReminder(c *stepCtx, tok *Token, node model.Node, cancelKey string, cmds []Command) ([]Command, error)
```

Call it from `userTaskStrategy.enter` (cancelKey = task token, preserving today's
behaviour byte-for-byte), `receiveTaskStrategy.enter`, and
`intermediateCatchEventStrategy.enter` (cancelKey = the parked token id).

**Staleness** (`handleReminderFired`). Today it treats the reminder as live only
while `TaskByToken(rec.TaskToken).IsOpen()` (a `HumanTask`). Generalize to
*"live while the parked token is still waiting"*:

- If the reminder's token still parks awaiting **and** (for a `UserTask`) its task
  is open → live: emit the fire-once `InvokeAction` (unchanged), no reschedule.
- Otherwise stale → remove the record, no-op (unchanged).

Concretely: keep the `HumanTask`-open check when a task exists for the token;
otherwise treat the reminder as live iff `s.tokenAwaiting(rec.Token)` is still
parked (`TokenWaitingCommand` with an `Await*` set). This preserves `UserTask`
behaviour and adds the token-parked path for `ReceiveTask`/catch.

**Cancel.** A `TimerInWait` re-fires natively on its recurrence, so it must be
*cancelled* (scheduler job removed) when the wait resolves, else it fires forever
and `handleReminderFired` no-ops on every stale fire. Today only
`handleHumanCompleted` cancels (via `cancelTimersByTaskToken(t.TaskToken)`). Add
the same cancellation at the other resume points, keyed on the **resuming token**:

- `handleMessageReceived` — after `tok.AwaitMessage/Key = ""` (covers `ReceiveTask`
  and message catch).
- `handleSignalReceived` — after `tok.AwaitSignal = ""` (signal catch).
- `handleTimerFired`, intermediate branch — after `tok.AwaitCommand = ""` (timer
  catch): cancel the token's in-wait reminder (a *different* timer than the
  intermediate that just fired).

The reminder record already carries `Token = <parked token id>`. The plan
introduces a cancel-by-parked-token path (either a `cancelTimersForToken(tokenID)`
matching `rec.Token`, or recording `TaskToken = tokenID` for these kinds and
reusing `cancelTimersByTaskToken`) and calls it at the three resume points above.

**Interrupt/consume sites need auditing, not assumption.** The existing
compensation / error-boundary / event-subprocess / cancel paths cancel via
`cancelTimersByTaskToken(tok.AwaitCommand, …)`. For a parked catch/receive token
`AwaitCommand` is the *intermediate timer id* (timer catch) or empty
(signal/message/receive) — **not** the parked token id — so those sites would
**miss** a token-keyed reminder and leak it. The plan MUST enumerate every
`Await*`-clearing / `consumeToken` / interrupt site and ensure each cancels the
parked token's in-wait reminder (most cleanly by matching on `rec.Token`). A test
per kind asserts *no reminder fires after the wait ends* — including the
interrupt/cancel paths, not just the happy-path resume.

Engine core imports no logger and gains no transport/storage coupling.

### 3. `definition.Lint(def) []Warning` (pure) + driver logging

A new pure function in the `definition` layer:

```go
type Warning struct {
    NodeID string
    Rule   string // stable machine-readable code, e.g. "reminder-ignored"
    Detail string // human-readable
}
func Lint(def *model.ProcessDefinition) []Warning
```

v1 rule: a node carries a wait-reminder (`ReminderOf(node)` non-zero) but its kind
does not arm one — i.e. any kind other than `UserTask`, `ReceiveTask`,
`IntermediateCatchEvent`. (Extensible: more set-but-ignored detections can be added
later without touching the engine.)

`ProcessDriver` calls `Lint(def)` **once per `(def.ID, def.Version)`** (deduped via
a small set guarded by a mutex) on the first `Drive`/`Deliver`, logging each
`Warning` at `WARN` via its `slog` logger. Non-fatal — the process runs normally.

### 4. Example scenario (requested)

`examples/scenarios/catch_event_reminder/main.go` — an intermediate catch event
awaiting a signal (`WithCatchSignal`) with a recurring in-wait reminder
(`WithCatchWaitReminder(every "30m", "nudge")`). While parked, advancing the fake
clock fires the reminder N times (each runs the nudge action); then publishing the
signal resumes the instance and the reminder stops (no further nudges on later
ticks). Deterministic: `*clockwork.FakeClock` + a real `scheduling.NewScheduler`
+ done-channel/count observation, matching the other timer examples. README gains a
scenario entry. This example is the executable proof that catch-event reminders
now actually arm and cancel.

## Testing (strict TDD)

Engine behavioural tests (black-box `engine_test` where possible, table per
`table-test` skill):

- `ReceiveTask` reminder: parks on message; a reminder fires while waiting; the
  message arrival cancels it (no fire after resume).
- Each catch variant (signal / message / timer): reminder fires while waiting;
  the resolving event cancels it.
- `UserTask` reminder regression: unchanged (arm/fire/cancel identical).
- Staleness: reminder that fires after the wait resolved is a clean no-op with the
  record removed.

`definition.Lint`: table tests over kinds (reminder on `ServiceTask` → warning;
on `UserTask`/`ReceiveTask`/catch → none).

`ProcessDriver` lint-logging: warns once per definition, deduped, non-fatal, at
WARN; a clean definition logs nothing.

Renames: compiler-verified. Example: runs clean (asserted output).

## Risks

- **Cancel coverage is the sharp edge.** If any resume/interrupt path for a
  parking token is missed, its reminder scheduler job leaks and keeps firing
  (each fire a harmless no-op, but a real resource leak). Mitigation: the plan
  enumerates every `Await*`-clearing / token-consuming site via Explore and adds a
  test that asserts *no reminder fires after resume* for each kind.
- **`timerRecord.TaskToken` overloading.** For non-`UserTask` reminders it holds
  the parked token id rather than a task token. Documented on the field; the
  cancel helpers already match on it.

## Rollout

Single branch, strict TDD, merged `--no-ff` after `go test -race ./...` +
`golangci-lint` green and a `/code-review` pass, consistent with recent work.
Deferred type-safe options tracked as a follow-up project.
