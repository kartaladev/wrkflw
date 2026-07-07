# Spec: typed `schedule.TriggerSpec` (duration/expr/cron) for all timer sites

- **Date:** 2026-07-07
- **Status:** Approved (design)
- **ADR:** 0102 (typed trigger spec + cron scheduling) — **landed FIRST**, before
  the boundary-enhancements spec (which shifts to ADR-0103/0104).
- **Depends on / blocks:** blocks `2026-07-07-boundary-event-enhancements.md`
  (its `WithBoundaryTimer`/deadline/reminder options must be designed against
  `TriggerSpec` from the start).

## Context

Every timer/deadline/reminder "duration" in a definition is currently an
**expr-lang expression string**, evaluated against the instance variables at
runtime by `internal/expreval.EvalDuration` (`internal/expreval/expreval.go:151`):
a string is `time.ParseDuration`'d, a number is seconds, and — crucially — the
expression may reference process variables (e.g. `region == "EU" ? "72h" :
"24h"`). This dynamic capability is locked by the tech stack ("expr-lang for …
timer durations"), so changing it requires an ADR.

Goals:
1. **Type-safety** for the common static case — accept `time.Duration`, not a
   stringly-typed literal.
2. **Preserve** dynamic, variable-driven durations (no capability regression).
3. **Add cron** scheduling, using gocron's native cron support (pinned v2.21.2),
   for recurring/scheduled firing (reminders, "fire daily at 09:00", etc.).

`RetryPolicy` already uses typed `time.Duration` (`definition/model/retry.go`) —
out of scope; this spec covers only the string-duration timer sites.

## Design: `schedule.TriggerSpec`

New leaf package `definition/schedule` with one value type carrying exactly one
of three firing forms, built by three constructors:

```go
package schedule

import "time"

// TriggerSpec specifies WHEN a timer fires. Exactly one form is set; the zero
// value is "unset". Build via AfterDuration / AfterExpr / Cron.
type TriggerSpec struct {
	dur  time.Duration // static, typed, relative delay
	expr string        // expr-lang expression over process vars → duration
	cron string        // cron expression (recurring / next occurrence)
}

// AfterDuration builds a static, typed relative-delay spec: AfterDuration(30*time.Minute).
func AfterDuration(d time.Duration) TriggerSpec { return TriggerSpec{dur: d} }

// AfterExpr builds a dynamic spec: an expr-lang expression evaluated against
// the instance variables, yielding a duration — e.g. AfterExpr(`slaHours * 3600`).
func AfterExpr(code string) TriggerSpec { return TriggerSpec{expr: code} }

// Cron builds a cron-schedule spec, evaluated by the gocron-backed scheduler —
// e.g. Cron(`0 9 * * 1-5`) (09:00 on weekdays).
func Cron(expr string) TriggerSpec { return TriggerSpec{cron: expr} }

// Accessors for the engine + serialization (exact names TBD in impl):
func (s TriggerSpec) IsZero() bool
func (s TriggerSpec) Duration() (time.Duration, bool)
func (s TriggerSpec) Expr() (string, bool)
func (s TriggerSpec) Cron() (string, bool)
```

Usage:

```go
event.WithBoundaryTimer(schedule.AfterDuration(1 * time.Hour))
event.WithBoundaryTimer(schedule.AfterExpr(`region == "EU" ? "72h" : "24h"`))
event.WithBoundaryTimer(schedule.Cron(`0 9 * * *`))

activity.WithDeadlineFlow(schedule.AfterDuration(30*time.Minute), "overdue")   // see boundary spec: WithDeadline split
activity.WithWaitReminder(schedule.AfterDuration(1*time.Hour), "nudge")        // every hour
activity.WithWaitReminder(schedule.Cron(`0 9 * * 1-5`), "nudge")       // 09:00 weekdays
```

## Option refactor (string → `TriggerSpec`)

All of these change their duration/every parameter from `string` to
`schedule.TriggerSpec` (breaking, pre-v1.0):

This spec changes the **current** option names (it lands first); the boundary
spec's later split/rename carries the `TriggerSpec` type forward:

- `event.WithBoundaryTimer(spec)`  (`definition/event/options.go`)
- `event.WithStartTimer(spec)`
- `event.WithCatchTimer(spec)`
- `event.WithCatchDeadline(spec, flowID, action)` — duration arg → spec
- `event.WithCatchReminder(spec, action)` — every arg → spec
- `activity.WithDeadline(spec, flowID, action)` — duration arg → spec
- `activity.WithReminder(spec, action)` — every arg → spec

Sequencing: land `TriggerSpec` here first (options above take `TriggerSpec`);
then the boundary-enhancements spec performs the `WithDeadline` →
`WithDeadlineFlow(spec, flow)` + `WithDeadlineAction(action)` split and the
`WithReminder` → `WithWaitReminder(spec, action)` rename, preserving the
`TriggerSpec` duration type.

## Model refactor

The string duration fields become `TriggerSpec`:

- `StartEvent.TimerDuration`, `IntermediateCatchEvent.TimerDuration`,
  `BoundaryEvent.TimerDuration` (`string`) → `Timer schedule.TriggerSpec`.
- `model.WaitFields.DeadlineDuration` (`string`) → `DeadlineTimer TriggerSpec`
  (keep `DeadlineFlow`/`DeadlineAction` as-is).
- `model.WaitFields.ReminderEvery` (`string`) → `ReminderEvery TriggerSpec`.

Accessors `DeadlineOf`/`ReminderOf` adjust their return types accordingly.

## Wire / serialization (backward-compatible, additive)

A `TriggerSpec` serializes across the existing string field (as the **expr**
form) plus two new optional companions, per timer kind. Old definitions load
unchanged; `AfterDuration` uses the nanos field, `Cron` the cron field:

```go
// NodeWire — per timer kind (timer / deadline / reminder):
TimerDuration       string `json:"timerDuration,omitempty"`       // EXISTING → Expr
TimerDurationNanos  int64  `json:"timerDurationNanos,omitempty"`  // NEW: AfterDuration(...)
TimerCron           string `json:"timerCron,omitempty"`           // NEW: Cron(...)
// …and deadlineDuration/deadlineDurationNanos/deadlineCron,
//    reminderEvery/reminderEveryNanos/reminderCron similarly.
```

A `ToWire`/`FromWire` helper encodes/decodes a `TriggerSpec` to/from the trio so
each leaf `NodeSpec` stays a one-liner. Round-trip is byte-stable: old string →
Expr → same string; `AfterDuration` → nanos; `Cron` → cron.

## Engine resolution

A single helper resolves a `TriggerSpec` at arm time:

- `Duration` set → use as-is (no eval).
- `Expr` set → existing `EvalDuration(expr, s.Variables)` (unchanged path;
  preserves dynamic durations).
- `Cron` set → hand the cron string to the scheduler (see below); the engine
  emits a boundary/timer arm carrying the cron rather than a computed `FireAt`.

`armBoundaries`, the deadline/reminder arming, and the catch/start timer arming
all route through this helper. Precedence is moot (one form set), but define
Duration → Expr → Cron for a defensively-constructed spec.

## Scheduler port + cron (gocron)

- Extend the scheduler port with a cron-capable method (e.g.
  `ScheduleCron(id, cronExpr string, callback func())`), implemented in the
  runtime's gocron adapter over gocron's native `CronJob` (pinned v2.21.2).
  gocron stays behind the port — never imported from engine/workflow code.
- `ScheduleTimer` gains an optional `Cron string` field (mutually exclusive with
  `FireAt`); the runtime routes it to `ScheduleCron`.
- **One-shot** sites (boundary/catch/start timer, deadline): fire on the next
  cron occurrence, then disarm (cancel the cron job after first fire).
- **Recurring** sites (reminder): fire on each occurrence until the host leaves
  the wait (existing reminder-cancellation path cancels the cron job).
- **Determinism:** the shared `clockwork` fake clock drives gocron (per ADR-0003
  wiring), so cron firing is deterministic in tests via `clk.Advance` + tick.
- `MemScheduler` (in-mem test scheduler) also gains cron support so `processtest`
  and in-memory examples can drive cron deterministically.

## Testing (strict TDD — red before green for every new symbol)

- `schedule` package: `AfterDuration`/`AfterExpr`/`Cron` set the right form; accessors +
  `IsZero`; the zero value is unset.
- Wire: round-trip for each form (string→Expr byte-identical; After→nanos;
  Cron→cron); negative unmarshal cases; old-definition compatibility (a
  `timerDuration:"1h"` fixture loads as Expr).
- Engine: resolves Duration (no eval), Expr (via EvalDuration, incl. a
  variable-driven expression), and Cron (routes to scheduler) at boundary,
  deadline, reminder, and catch/start timer sites.
- Scheduler: gocron adapter fires a cron job under the fake clock (deterministic);
  one-shot disarms after first fire; recurring fires repeatedly and is cancelled
  on host exit. `MemScheduler` cron parity.
- All existing timer/deadline/reminder call sites migrated to `TriggerSpec` and
  green (the ~19 deadline + ~11 reminder + timer/catch/start sites).
- godoc `Example` for `schedule.AfterDuration`/`AfterExpr`/`Cron`.

## Non-goals

- No change to `RetryPolicy` (already typed).
- No removal of expr durations (preserved as the `Expr` form).
- No cron *actions* — cron only schedules WHEN a timer fires; the action/flow it
  triggers is unchanged.
- No new expr features; `EvalDuration` is reused verbatim for the `Expr` form.

## Verification checklist

- [ ] `definition/schedule.TriggerSpec` + `AfterDuration`/`AfterExpr`/`Cron` + accessors.
- [ ] All timer/deadline/reminder/catch/start options accept `TriggerSpec`;
      model fields converted; all call sites migrated and green.
- [ ] Wire additive fields; old string-duration definitions still load (as Expr);
      round-trip byte-stable per form.
- [ ] Engine resolves all three forms at every timer site; dynamic expr durations
      still work.
- [ ] Scheduler port + gocron adapter + `MemScheduler` support cron; deterministic
      under the fake clock; one-shot disarm + recurring cancel verified.
- [ ] godoc `Example`s present and passing.
- [ ] ADR-0102 written (Nygard): typed trigger spec + cron; note it extends (not
      removes) the expr-lang-for-durations decision.
- [ ] `go build ./...`, `go test ./...`, `golangci-lint run ./...` clean; touched
      packages ≥ 85% coverage.
- [ ] Update the boundary-enhancements spec's ADR numbers to 0103/0104.
