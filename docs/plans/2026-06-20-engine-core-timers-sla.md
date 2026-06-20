# Engine Core — Timers & SLA (Plan 5 of 8) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: superpowers:subagent-driven-development (or executing-plans). Checkbox steps.
>
> **Handover note:** Targets the design spec (§4/§5/§8) and assumes Plans 1–4 merged. Contracts (Trigger/Command/ports) are fixed by the spec; ground the exact `engine/step.go`/`runtime` edits against current code before editing. SDD review loop is the safety net.

**Goal:** Add time-driven behavior: the Timer intermediate catch event (wait a duration then continue), human-task **SLA** (on breach, run alternative action(s) then take an alternative path), and **in-wait reminders** (actions executed *during* a wait). All deterministic under a fake clock.

**Architecture:** The engine emits `ScheduleTimer` commands carrying an **absolute `FireAt`** it computes from `trigger.OccurredAt() + duration` (time as data — engine still never reads a clock). The runtime owns a `Scheduler` port (in-memory fake here; gocron later) that fires `TimerFired{TimerID}` when the shared `clock.Clock` reaches `FireAt`. Durations come from the model (static or an `expr` that evaluates to a Go duration).

**Tech Stack:** Go 1.25, `jonboulle/clockwork` (test clock, via the `clock.Clock` port — never imported by engine), `expr-lang/expr` (via `expreval`), `testify`.

## Global Constraints

- Go **1.25**; module `github.com/zakyalvan/krtlwrkflw`; root packages.
- Engine never reads a clock: `ScheduleTimer.FireAt = trg.OccurredAt() + dur`, computed in `Step` from data. `clockwork` only enters via the runtime's `Scheduler`/`clock.Clock` (ADR-0003). No `time.Now()` in engine.
- `Step` deterministic + pure; public signature unchanged. Black-box tests with a **fake clock** (`clockwork.NewFakeClock()`), `assert`-closure tables, `t.Context()`.
- Coverage ≥ 85% touched packages; `-race` green; lint clean. Conventional Commits; commit per green step.

## Prerequisite & contracts

- Command (sealed): add `ScheduleTimer{TimerID string; Token string; FireAt time.Time; Kind TimerKind}` and `CancelTimer{TimerID string}`. `TimerKind` ∈ `TimerIntermediate | TimerSLA | TimerInWait`.
- Trigger (sealed): add `TimerFired{TimerID string}` (already named in the spec).
- `Token` parks on a timer via `AwaitCommand = TimerID` (reuse the correlation field).
- Deterministic `TimerID` = `<instanceID>-tm<seq>` (new `TimerSeq` counter on `InstanceState`).
- model `Node` (timer intermediate) gains `TimerDuration string` (expr evaluating to a duration, e.g. `"duration('PT3H')"` or a number of seconds — see Task 1 for the agreed form). User-task SLA fields: `SLADuration string`, `SLAFlow string` (the alternative outgoing flow id taken on breach), `SLAAction string` (optional action to invoke on breach), `ReminderEvery string` + `ReminderAction string` (in-wait reminder).

---

## File Structure

```
expreval/expreval.go        # MODIFY: add EvalDuration(code, env) (time.Duration, error)
model/definition.go         # MODIFY: timer + SLA + reminder fields on Node
engine/command.go           # MODIFY: ScheduleTimer, CancelTimer, TimerKind
engine/trigger.go           # MODIFY: TimerFired (+ constructor)
engine/state.go             # MODIFY: TimerSeq; (optional) per-token timer bookkeeping
engine/step.go              # MODIFY: timer intermediate case; SLA + reminder on AwaitHuman; TimerFired handler
engine/step_timer_test.go
runtime/scheduler.go        # Scheduler port + MemScheduler (fake-clock driven)
runtime/runner.go           # MODIFY: perform ScheduleTimer/CancelTimer; drive timers via clock
runtime/timer_example_test.go
```

---

### Task 1: `expreval.EvalDuration` + agree the duration form

**Decision (record in an ADR if contested):** timer durations are `expr` expressions evaluating to either a Go `time.Duration`, an integer number of **seconds**, or a string parseable by `time.ParseDuration` (e.g. `"3h"`). `EvalDuration` normalizes all three.

- [ ] **RED:** `expreval_test.go` table — `"3h"`→3h, `90`→90s, a `vars`-driven `"slaSeconds"` int→duration, non-duration→error. Run → fails.
- [ ] **GREEN:** add `func (e *Evaluator) EvalDuration(code string, env map[string]any) (time.Duration, error)`: run the program, switch on result type (`time.Duration` | integer kinds → seconds | string → `time.ParseDuration`), else error. Run → pass.
- [ ] Commit `feat(expreval): EvalDuration for timer expressions`.

---

### Task 2: timer/SLA/reminder commands, trigger, model fields

- [ ] **RED:** extend `engine/command_test.go`/`trigger_test.go` for `ScheduleTimer`/`CancelTimer`/`TimerKind`/`TimerFired`; add a `model` test for the new `Node` timer/SLA/reminder fields. Run → fails.
- [ ] **GREEN:** add the commands/trigger/constructor/`isCommand()`/`isTrigger()`; add `TimerSeq` to `InstanceState`; add `Node` fields `TimerDuration`, `SLADuration`, `SLAFlow`, `SLAAction`, `ReminderEvery`, `ReminderAction`. Run → pass.
- [ ] Commit `feat(engine,model): timer/SLA/reminder commands, trigger, and node fields`.

---

### Task 3: Timer intermediate catch event

**Behavior:** `drive` `KindIntermediateCatchEvent` (timer variant — has `TimerDuration`): compute `dur = expreval.EvalDuration(node.TimerDuration, vars)`; `FireAt = at + dur`; `TimerID = nextTimerID()`; emit `ScheduleTimer{TimerID, Token:tok.ID, FireAt, Kind:TimerIntermediate}`; park the token (`AwaitCommand=TimerID`). `Step` `TimerFired`: find token via `tokenAwaiting(TimerID)`; advance it (`moveAlongSingleFlow`); `drive`.

- [ ] **RED:** `step_timer_test.go` — `TestTimerIntermediateSchedulesAndResumes`: start→timer(`"1h"`)→service→end. First Step emits one `ScheduleTimer` with `FireAt == start+1h`, token parked. Feeding `TimerFired{thatID}` advances to the service task (emits `InvokeAction`). Run → fails (handled by `default`).
- [ ] **GREEN:** implement the case + `TimerFired` handler + `nextTimerID()`. Run → pass.
- [ ] Commit `feat(engine): timer intermediate catch event`.

---

### Task 4: human-task SLA (alternative action + alternative path on breach)

**Behavior:** when entering a user task (Plan 4's `AwaitHuman` path), if `node.SLADuration != ""`: also emit `ScheduleTimer{TimerID, Token, FireAt:at+sla, Kind:TimerSLA}` and set the `HumanTask.DueAt`. Record the SLA TimerID against the task (add `SLATimerID` to the in-state task or a side map). On `TimerFired` for an SLA timer whose task is **still not completed**: (a) if `node.SLAAction != ""`, emit `InvokeAction` for it (fire-and-forget alternative action); (b) move the token along `node.SLAFlow` (the alternative path) and `drive`; (c) mark the task `Cancelled`, emit `UpdateTask`, and `CancelTimer` any reminder timer. If the task was already completed before the SLA fired, the SLA `TimerFired` is a no-op (token no longer parked on it).

- [ ] **RED:** `step_timer_test.go` — `TestUserTaskSLABreachTakesAlternativePath`: start→userTask(role, SLA `"3h"`, SLAFlow→`escalate`, SLAAction `notify`)→normal end; escalate→escalate end. Enter task → `AwaitHuman` + `ScheduleTimer(SLA)`. Without completing, feed the SLA `TimerFired` → emits `InvokeAction("notify")`, token moves to `escalate`, task `Cancelled`. Also `TestUserTaskCompletedBeforeSLAIgnoresTimer`: complete the task, then a late SLA `TimerFired` is a no-op (no commands, instance already past). Run → fails.
- [ ] **GREEN:** implement SLA scheduling on entry + SLA-breach handling in `TimerFired` (distinguish `Kind`/correlate to the task; check the token is still parked). Reuse `moveTokenToTarget` with the resolved `SLAFlow` target. Run → pass.
- [ ] Commit `feat(engine): human-task SLA breach — alternative action and path`.

---

### Task 5: in-wait reminders

**Behavior:** when entering a user task with `node.ReminderEvery != ""`: emit a `ScheduleTimer{Kind:TimerInWait}` at `at+every`. On its `TimerFired`, if the task is still open: emit `InvokeAction(node.ReminderAction)` and re-schedule the next reminder (`FireAt = firedAt + every`) — a repeating in-wait action — until the task completes or SLA fires (then `CancelTimer`). Reminders never move the token.

- [ ] **RED:** `TestInWaitReminderRepeatsUntilCompletion`: enter task with `ReminderEvery "1h"`, `ReminderAction "remind"`. Each reminder `TimerFired` emits `InvokeAction("remind")` + a fresh `ScheduleTimer`. Completing the task then a late reminder `TimerFired` is a no-op. Run → fails.
- [ ] **GREEN:** implement reminder scheduling + repeat. Ensure determinism (TimerIDs from counter). Run → pass.
- [ ] Commit `feat(engine): in-wait reminder actions during human-task wait`.

---

### Task 6: runtime `Scheduler` + perform timers + fake-clock e2e

**Behavior:**
- `runtime/scheduler.go`: `Scheduler` port — `Schedule(timerID string, fireAt time.Time, fire func())`, `Cancel(timerID string)`. `MemScheduler` holds pending timers and, driven by a `clock.Clock`, fires those whose `FireAt <= clock.Now()` when `Tick()` is called (tests advance a `clockwork.FakeClock` then call `Tick`/`Run`). Gocron-backed impl is a later sub-project.
- `runner.perform`: `ScheduleTimer` → `Scheduler.Schedule(timerID, FireAt, func(){ deliver TimerFired{timerID} })`; `CancelTimer` → `Scheduler.Cancel`. `NewRunner` gains a `Scheduler` + `clock.Clock` (it already takes a clock from Plan 1; extend with scheduler).
- e2e (`timer_example_test.go`) with `clockwork.NewFakeClock()`: a timer-intermediate process parks; advancing the fake clock past `FireAt` and ticking the scheduler fires `TimerFired`, and the instance completes. An SLA process: not completing the task, advance past the SLA, assert the alternative path ran.

- [ ] **RED:** `timer_example_test.go`. Run → fails (`undefined: runtime.MemScheduler`, `NewRunner` arity).
- [ ] **GREEN:** implement `MemScheduler` (clock-driven, deterministic order by FireAt then TimerID), `perform` cases, runner wiring. `go test -race ./...`, coverage, lint → green.
- [ ] Commit `feat(runtime): in-memory clock-driven scheduler + timer/SLA e2e`.

---

## Verification Checklist (Plan 5)

- [ ] `EvalDuration` parses duration/seconds/string forms; errors otherwise.
- [ ] Timer intermediate event computes `FireAt = OccurredAt + dur` (engine reads no clock), parks, and resumes on `TimerFired`.
- [ ] User-task SLA breach runs the alternative action and takes the alternative path; a task completed before its SLA makes the SLA `TimerFired` a no-op.
- [ ] In-wait reminders repeat until completion/SLA, then stop (`CancelTimer`).
- [ ] Under a fake clock, advancing time + ticking the `MemScheduler` deterministically fires timers; the same fake clock would drive gocron later (ADR-0003).
- [ ] `Step` deterministic + pure; engine has no `time.Now()` and no `clockwork` import; `-race` green; coverage ≥ 85%; lint clean.

## Self-Review Notes

- **Spec coverage:** §5 ScheduleTimer/CancelTimer/TimerFired + Kind (SLA/Intermediate/InWait), the requirements' 3-working-day SLA + in-wait email examples, §4 timing via OccurredAt. Boundary timer events deferred to Plan 6 (they reuse `ScheduleTimer` but need the boundary-attachment model).
- **Determinism under time:** absolute `FireAt` is computed from `OccurredAt` (data); `MemScheduler` fires in `(FireAt, TimerID)` order; a fake clock makes time-travel tests exact — no real waiting.
- **Grounding required:** read the merged `engine/step.go` (UserTask/AwaitHuman path from Plan 4), `runtime/runner.go`, and `runtime/taskservice.go` before editing; `NewRunner` arity changes again — update call sites/tests in the same task.
- **Working-days nuance:** "3 working days" implies a business-calendar duration. Plan 5 supports wall-clock durations; a business-calendar resolver (skip weekends/holidays) is a follow-up — note it where `SLADuration` is evaluated.
