# Calendar trigger timezone follow-up

Status: Open

## Context

`scheduler.Daily`, `scheduler.Weekly`, and `scheduler.Monthly`
(`scheduler/trigger.go`) build a `Trigger` whose calendar at-times are a
wall-clock hour/minute/second (`ClockTime`). Two different code paths resolve
that wall-clock time against two different timezones:

- `Trigger.Next` (`scheduler/trigger.go`, `calendarNext`) is a pure function
  that explicitly normalizes to `time.UTC` before scanning for the next
  occurrence. This path is deterministic and timezone-consistent.
- The live scheduler — reached at runtime via `runtime.convertTrigger`
  (`runtime/timerops.go:39-47`), which hands the `scheduler.Trigger` to the
  internal gocron adapter (`scheduler/internal/gocron/trigger.go`,
  `gocron.DailyJob` / `WeeklyJob` / `MonthlyJob`) — never calls
  `gocron.WithLocation(time.UTC)` when constructing the gocron scheduler.
  gocron therefore falls back to its own default, `time.Local`, to resolve
  the same at-times.

The result: a `Daily`/`Weekly`/`Monthly` trigger's at-times are computed in
UTC by `Trigger.Next` but actually fire, on the live scheduler, at that
wall-clock time in whatever timezone the host process's `time.Local` is set
to. This is **pre-existing behavior** — it predates the scheduler-owned
durable jobs program (ADR-0134) and was not introduced or changed by it. It
was found while writing `TestNativeSchedulerCalendarTriggers`
(`scheduler/scheduler_test.go`), which now documents actual (`time.Local`)
behavior in its assertions rather than the previously-misdocumented UTC
claim. The godoc for `Daily`/`Weekly`/`Monthly`/`ClockTime` in
`scheduler/trigger.go` has been corrected (2026-07-24) to state this
discrepancy honestly; this document tracks the underlying behavior decision,
which the doc fix deliberately did not make.

## Severity

- Affects every wire-serializable calendar trigger kind (`KindDaily`,
  `KindWeekly`, `KindMonthly` in `definition/schedule`), i.e. any process
  definition that schedules a daily/weekly/monthly timer or deadline.
- Any deployment where the host process's `time.Local` is not UTC will fire
  these triggers at the wrong wall-clock time relative to what a consumer
  reading the previously-published (incorrect) "(UTC)" godoc, or reasoning
  about `Trigger.Next` in isolation, would expect.
- Not a data-loss or correctness-of-persistence bug — the trigger still
  fires exactly once at its computed instant. The risk is purely "fires at
  the wrong wall-clock time vs. documented/assumed intent," which for
  business-hours-sensitive calendar triggers (e.g. "remind at 09:00") is a
  real operational problem in non-UTC deployments.

## Options

1. **Document as local, permanently.** Keep `Trigger.Next`'s pure UTC
   computation as the "reference" semantics (used for testing / determinism)
   but formally accept that the live scheduler resolves at-times in
   `time.Local`, and document this as the permanent contract. No code
   change beyond documentation (already done as of this doc). Lowest risk,
   but leaves a UTC/local split baked into the public API that is easy to
   get wrong again, and gives non-UTC-host deployments no way to pin
   behavior to UTC.

2. **Wire `gocron.WithLocation(time.UTC)`** in the internal gocron adapter so
   the live scheduler matches `Trigger.Next`'s UTC semantics exactly. This is
   a **behavior change** for any existing consumer/deployment currently
   relying (knowingly or not) on `time.Local` resolution — it needs its own
   ADR, a consumer-facing migration note (CHANGELOG breaking-behavior entry),
   and likely a way to opt back into local-time resolution for consumers who
   want it (e.g. a `WithSchedulerLocation` option threaded through
   `scheduler.NewScheduler`, mirroring `WithClock`).

## Decision

Pending — not decided by this document. Needs its own brainstorming pass
(per `CLAUDE.md`'s standing requirement to run `superpowers:brainstorming`
before behavior changes) and, if option 2 is chosen, an ADR before
implementation.
