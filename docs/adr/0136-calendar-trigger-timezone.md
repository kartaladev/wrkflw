# 136. Resolve calendar/cron trigger at-times in UTC by default

- Status: Accepted
- Date: 2026-07-24

## Context

Recurring calendar triggers (`scheduler.Daily`, `scheduler.Weekly`,
`scheduler.Monthly`) carry a wall-clock at-time (`ClockTime`), and
`scheduler.Cron` carries a standard cron expression. Both say *when during the
day* a trigger fires. Two code paths resolve that wall-clock time against two
different timezones:

- **Pure / persisted path** — `scheduler.Trigger.Next` (`scheduler/trigger.go`)
  is a pure function used both by tests *and* in the live runtime to compute the
  reported/persisted `NextRun` (`runtime/timerjob.go`, `runtime/timerops.go`)
  and the façade's `ScheduledJob` metadata. Its **calendar** branch
  (`calendarNext`) forces `time.UTC`; its **cron** branch did *not* — it
  resolved in the caller-supplied `after`'s location.
- **Live path** — `runtime.convertTrigger` hands the `scheduler.Trigger` to the
  internal gocron adapter (`scheduler/internal/gocron`), whose
  `NewGocronScheduler` never called `gocron.WithLocation(...)`. gocron therefore
  fell back to its own default, `time.Local`, to resolve **both** calendar
  at-times and cron expressions.

For a host whose `time.Local ≠ UTC`, the result was a split: calendar triggers
fired at their at-time in `time.Local` while `Trigger.Next` computed and
persisted the UTC instant (they disagree); cron triggers happened to agree only
because both the pure path (`after` = host-local `now`) and the live path landed
on `time.Local`. Containers commonly run `TZ=UTC`, so a large share of
deployments observed no discrepancy — the divergence bit only non-UTC hosts, and
there for business-hours-sensitive calendar triggers ("remind at 09:00") it is a
real operational fault. This predates the scheduler-owned durable jobs program
(ADR-0134) and was not introduced by it; it surfaced while writing
`TestNativeSchedulerCalendarTriggers`, whose assertions documented the actual
`time.Local` behavior.

gocron applies its scheduler location globally to `DailyJob`/`WeeklyJob`/
`MonthlyJob` **and** `CronJob` (there is no per-job location for `CronJob`), so
any single `WithLocation` pin necessarily governs cron as well as calendar —
cron cannot be exempted. See the full analysis in
`docs/specs/2026-07-24-calendar-trigger-timezone-followup.md`.

## Decision

We resolve calendar and cron trigger at-times in **UTC by default** on the live
scheduler, matching the pure `Trigger.Next` reference, and make that default
overridable:

1. **Pin the live scheduler's location explicitly.**
   `scheduler/internal/gocron.NewGocronScheduler` always appends
   `gocron.WithLocation(loc)`, resolving an unset `loc` to `time.UTC`. It will
   never fall through to gocron's `time.Local` default.

2. **Add `scheduler.WithLocation(*time.Location)`.** A new façade option
   (parallel to `WithClock`), threaded through the façade `config` to a new
   internal `gocron.WithLocation` option. A `nil` value is ignored on both
   layers and resolves to UTC. The nil-guard matters for two reasons: (1)
   gocron's own default when no location is pinned is `time.Local`
   (`gocron.scheduler.go`), so an unset location must be resolved to `time.UTC`
   *before* the scheduler is constructed; and (2) `gocron.WithLocation(nil)`
   returns `ErrWithLocationNil` and would fail `gocron.NewScheduler`, so a nil
   must never reach it. Consumers pass `time.Local` for host-local resolution,
   or any named zone.

3. **Make `Trigger.Next` uniformly UTC.** The cron branch now normalizes
   `after.UTC()` before delegating to the cron schedule, so every recurring kind
   (calendar already; cron now) resolves against UTC. Without this, pinning the
   live scheduler to UTC would *fix* calendar but *introduce* a new cron
   divergence (live→UTC, pure→still `time.Local`).

The resulting contract:

| Config | Live fire (gocron) | `Trigger.Next` reference | Agree? |
|---|---|---|---|
| Default (UTC) | UTC | UTC | yes — the fix |
| `WithLocation(loc≠UTC)` | `loc` | UTC | fire correct in `loc`; NextRun *reporting* surfaces diverge — see Consequences |

## Consequences

- **Correctness for the common case.** On the default path, the live fire, the
  persisted `NextRun`, and the documented reference all agree in UTC. Non-UTC
  hosts no longer fire calendar triggers at an unintended wall-clock time.
- **Breaking behavior for non-UTC hosts only.** Deployments whose `time.Local`
  was not UTC and that relied (knowingly or not) on host-local resolution will
  see calendar and cron triggers move to UTC. `TZ=UTC` deployments (typical
  containers) are unaffected. The migration is a single option:
  `scheduler.WithLocation(time.Local)`. Recorded as a breaking-behavior entry in
  the CHANGELOG.
- **Residual custom-location caveat is display-only, and the reporting surfaces
  do not agree with each other.** Firing and rehydration are correct in every
  configuration (see the re-arm point below); what a non-UTC `WithLocation`
  perturbs is only how the next-run instant is *reported*, and there are three
  such surfaces that diverge under a custom location:
  - **Persisted / admin path** — `runtime/timerops.go` persists
    `nextRun = Trigger.Next(now).UTC()`; it feeds `MIN(next_run)` →
    `TimerStats.NextFireAt` (`internal/persistence/store/timerstore.go`) and is
    serialized by the admin HTTP endpoint
    (`transport/http/httpcore/admin_endpoints.go`, per-timer `fire_at` and the
    aggregate `next_fire_at`). This surface is **UTC**.
  - **Façade `Schedule()` return** — `scheduler.NativeScheduler.Schedule` stamps
    `NextRun` from `Trigger.Next` (**UTC**).
  - **Façade `Scheduled(id)` / `List()`** — these re-fetch gocron's *live*
    `NextRun`, which respects `WithLocation` and is therefore the **loc-resolved
    (correct)** instant — not UTC.
  So under `WithLocation(loc≠UTC)`, an operator reading the admin API sees UTC
  while `Scheduled`/`List` report the loc instant, for the same job. None of
  this affects *when* a timer fires; it is a reporting inconsistency to be aware
  of, documented on the `WithLocation` godoc. (The default UTC path has no such
  split — all three surfaces read UTC.)
- **Re-arm is trigger-driven, never `NextRun`-driven, for recurring triggers.**
  Recurring calendar/cron timers re-arm from the stored `Trigger`
  (`rehydrateTrigger`, `runtime/timerops.go`); only *non-recurring* one-shots
  re-arm via `schedule.At(NextRun)`, and one-shots carry a location-invariant
  absolute instant. So the reporting divergence above never leaks into firing or
  rehydration. The `PruneTimers` path is likewise safe: it excludes recurring
  rows regardless of their (UTC) `next_run`.
- **DST applies on the live path under a named zone, not on the UTC reference.**
  Under `WithLocation(America/New_York)` (or any DST zone), gocron resolves
  calendar at-times per that zone's DST rules, while `calendarNext` (the UTC
  reference / persisted `NextRun`) has no DST — every day is 24h. The two
  therefore diverge by a full hour across a DST boundary, and a non-existent
  spring-forward local at-time (e.g. a Daily `02:30` on the transition day) is
  resolved by gocron's rules, not the UTC scan. The default UTC path is DST-free
  and unaffected; a future location-aware `Trigger.Next` (below) would have to
  define non-existent/ambiguous-time semantics. Stated on the `WithLocation`
  godoc.
- **Multi-replica deployments must use an identical `WithLocation` on every
  replica.** Because the location changes the actual fire instant of every
  calendar/cron job, two replicas with different locations would compute
  different fire wall-times for the same timerID — under `WithLocker` (key =
  timerID) that risks a fire in each distinct window; under `WithElector` a
  failover to a differently-configured leader shifts the schedule. Same
  discipline as the shared clock. Doc note on `WithLocation`; no code change.
- **Future-work hook.** Closing even the cosmetic gap would require making
  `Trigger.Next` location-aware (resolve against `after.Location()` and thread
  the configured location through the runtime arm-path) — Option 3 in the spec,
  deferred as it touches the pure `Trigger.Next` contract and the runtime timer
  path. Referenced from the `WithLocation` godoc.
- **Cron pulled into scope.** The original follow-up named only calendar kinds;
  because the `WithLocation` pin is global, cron's resolution changes too. This
  is stated in the godoc and CHANGELOG so it is not a silent surprise.
- **Cron requires a name-resolvable location.** A `Cron` trigger resolves its
  location by *name*: gocron builds `CRON_TZ=<location.String()>` and robfig/cron
  re-parses it via `time.LoadLocation`. So a `Cron` trigger under a non-IANA
  location (e.g. an anonymous `time.FixedZone`) fails fast at schedule time with
  `ErrCronJobParse` — never a silent misfire. `time.UTC` (the default),
  `time.Local`, and IANA zones all have resolvable names, so the common cases are
  unaffected. Calendar triggers use the location's offset directly and accept any
  `*time.Location`. Documented on the `WithLocation` godoc; a live test covers
  both the IANA-cron and DST paths.
