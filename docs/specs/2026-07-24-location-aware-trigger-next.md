# Location-aware `Trigger.Next` (collapse NextRun surfaces)

Status: Decided (2026-07-24). Implementation via ADR-0137, which refines
ADR-0136 decision #3.

## Context

ADR-0136 ([[calendar-trigger-timezone-shipped]],
`docs/adr/0136-calendar-trigger-timezone.md`) made recurring calendar
(Daily/Weekly/Monthly) and cron triggers resolve at-times in **UTC by default**
on the live scheduler, and added `scheduler.WithLocation(*time.Location)` to opt
into a different zone. To keep the pure reference consistent with the default
live path, ADR-0136 also made `scheduler.Trigger.Next` a **uniform UTC
reference** (its cron branch was forced to `after.UTC()`; the calendar branch
via `calendarNext` already forced UTC).

That left a known, documented gap: under a **custom** non-UTC `WithLocation`, the
next-run instant is *reported* by three surfaces that no longer agree:

- **Persisted / admin** — `runtime/timerops.go` persists
  `Trigger.Next(now).UTC()`; feeds `MIN(next_run)` → `TimerStats.NextFireAt`
  (`internal/persistence/store/timerstore.go`) and the admin HTTP endpoint
  (`transport/http/httpcore/admin_endpoints.go`). **UTC.**
- **Façade `Schedule()` return** — `scheduler/scheduler.go:560`,
  `Trigger.Next(s.now())`. **UTC.**
- **Façade `Scheduled(id)` / `List()`** — re-fetch gocron's *live*,
  location-resolved `NextRun`. **loc-resolved (the actually-correct instant).**

The divergence is display-only — firing and rehydration are correct in every
configuration (recurring timers re-arm from the trigger, not from `NextRun`) —
but an operator reading the admin API sees UTC while `Scheduled`/`List` report
the `loc` instant for the same job. ADR-0136 named collapsing these surfaces
(making `Trigger.Next` location-aware) as deferred future work. This document is
that work.

### Why the surfaces diverge, mechanically

Both *computing* surfaces call `Trigger.Next(clockNow)` where `clockNow` is not
location-adjusted, and `Trigger.Next` forces UTC internally. The live surface
(`Scheduled`/`List`) re-fetches gocron's own `NextRun(id)`, which gocron computes
in its pinned location (ADR-0136). So the fix is to make the two computing
surfaces resolve in that same location.

The runtime is the harder of the two: it persists `NextRun` **before** arming
(direct-Save inside the state-commit transaction, ADR-0134), so it cannot read
gocron's post-arm value — it must compute its own `Trigger.Next`, and therefore
must be told the scheduler's location. (There is one **load-bearing** runtime
compute site — `timerJobsFor` → `timerJob.spec.NextRun`, which `jobStore.Save`
persists — and two **cosmetic** wrapper sites, `buildTimerJob` on rehydration
and the fresh-arm wrap in `processdriver.go`, whose `scheduledTimerJob.NextRun`
only feeds `Activate` (which ignores it) and is overwritten by the live gocron
value on any `Scheduled`/`List` read. All three are threaded for internal
consistency, but only the first changes a persisted value.)

## Options considered

1. **Leave it (document-only).** Rejected here — this document exists to close
   the gap ADR-0136 deferred.
2. **`Trigger.Next` respects `after.Location()`; thread the scheduler location to
   both computing callers.** ✅ **Chosen.** Pure, no signature change,
   default-preserving.
3. **Add a location parameter to `Trigger.Next(after, loc)`.** Rejected —
   breaking signature, more call sites to churn, and `after` already carries a
   location that expresses caller intent.
4. **Post-arm reconciliation write.** Keep `Trigger.Next` uniform-UTC; after the
   post-commit `Activate`, read the live gocron `Scheduled(id).NextRun()` and
   issue a second `UpsertJob` to overwrite the persisted `next_run` with the
   loc-resolved instant. Rejected: it adds an extra hot-path write per arm; a
   crash between commit and reconcile leaves the UTC value until the next
   fire/rehydrate; and it fixes neither direct `Trigger.Next` consumers nor the
   `Schedule()`-return surface. Fixing the pure function (Option 2) needs zero
   extra I/O and repairs every surface at once.

For threading the location to the runtime, three sub-options were weighed:

- **A. Capability interface (chosen).** Expose `Location() *time.Location` on
  `*NativeScheduler`; the runtime type-asserts an opt-in interface it defines
  locally, defaulting to `time.UTC` when absent. Non-breaking — consumers /
  doubles implementing `scheduler.Scheduler` (e.g. `processtest.MemScheduler`)
  are unaffected. Matches the codebase's existing capability-interface pattern
  (`Notifier` / `Locker` / `Elector`).
- **B. Add `Location()` to the `scheduler.Scheduler` port.** Rejected — breaking
  for every `Scheduler` implementer.
- **C. Separate `runtime.WithSchedulingLocation`.** Rejected — duplicates the
  scheduler's location in a second place; real drift risk (fire in one zone,
  persist in another).

## Decision

### 1. `Trigger.Next` resolves at-times in `after.Location()`

In `scheduler/trigger.go`:

- `calendarNext` builds candidate at-times with `after.Location()` instead of
  normalizing to `after.UTC()` — i.e. `time.Date(y, m, d, H, M, S, 0,
  after.Location())` and the day-scan `start` likewise in `after.Location()`.
- The cron branch reverts to `sched.Next(after)` (drop the ADR-0136
  `after.UTC()`), so robfig/cron resolves in `after`'s location.

New godoc contract: **`Trigger.Next` resolves recurring at-times in the location
of `after`.** It remains pure, total, and deterministic (same `after` → same
result). This refines — and for the cron branch reverts — ADR-0136 decision #3
(uniform UTC reference).

### 2. Scheduler exposes and uses its resolved location

In `scheduler/scheduler.go`:

- Add `func (s *NativeScheduler) Location() *time.Location` returning the
  resolved zone: `config.loc` if set, else `time.UTC` (never nil, mirroring the
  internal engine's nil→UTC default). Back it with a small unexported
  `s.location()` helper.
- `Schedule()` computes the caller's `NextRun` as
  `j.Trigger().Next(s.now().In(s.location()))`.

### 3. Runtime threads the location into its own `Trigger.Next` calls

In `runtime`:

- Define a local, unexported interface:
  `type locatedScheduler interface { Location() *time.Location }`.
- Resolve the effective location once from the driver's scheduler:
  `loc := time.UTC; if ls, ok := driver.sched.(locatedScheduler); ok && ls.Location() != nil { loc = ls.Location() }`.
- At the two compute sites — `timerops.go` (`strig.Next(now)` in `timerJobsFor`)
  and `timerjob.go` (`j.trig.Next(now)` in `newScheduledTimerJob`, reached via
  `buildTimerJob` on rehydration) — pass `now.In(loc)`. The existing `.UTC()`
  normalization before persistence stays (it re-expresses the now-correct
  absolute instant for storage).

## Consistency contract (after this change)

| Config | Live fire (gocron) | Persisted/admin + `Schedule()`-return | `Scheduled`/`List` | Agree? |
|---|---|---|---|---|
| Default (UTC) | UTC | UTC | UTC | ✅ *(interval==1; byte-identical to today)* |
| `WithLocation(loc)` | `loc` | `loc` | `loc` | ✅ *(interval==1; the fix — was UTC vs loc split)* |

**Default is byte-identical.** The default scheduler location is UTC, so every
compute site passes `now.In(UTC)` and produces exactly today's UTC values. Only
custom-location deployments change, and they change from wrong-reported to
correct-reported.

**The `✅` is for the timezone dimension and `interval==1` (the overwhelmingly
common calendar shape, plus all cron).** A separate, pre-existing `interval>1`
day-scan gap remains — see the residual caveats.

## Residual caveats (documented, not blockers)

- **`interval > 1` calendar first-fire day gap (pre-existing, orthogonal to this
  change, NOT fixed here).** `calendarNext` scans day-by-day and ignores the
  `interval` when computing the first fire (it returns the next matching day+1),
  whereas the live gocron `DailyJob`/`WeeklyJob`/`MonthlyJob` jumps by `interval`
  (`day+interval` / `interval*7` / `month+interval`) once the current period's
  at-times are exhausted. So a `Daily(2, 09:00)` armed after 09:00 persists a
  next-run one day out while the live fire is two days out — the persisted/admin
  and `Schedule()`-return surfaces do **not** match `Scheduled`/`List` for
  `interval>1` in that case. This is a **day-scan** bug in `calendarNext`,
  independent of timezone, and it predates ADR-0136 (it exists today under UTC).
  This bundle deliberately does **not** fix it — it is a separate concern with
  its own edge cases (period-boundary jump for daily/weekly/monthly) tracked as a
  follow-up (`calendarNext` interval-aware first-fire). The godoc claim "matching
  gocron's first-fire behaviour" is corrected to state this gap honestly. The
  common case (`interval==1`: "every day at 9", "every Monday", "1st of month")
  and all cron are unaffected and fully converge.
- **Cron + non-IANA `time.FixedZone`.** `Trigger.Next`'s cron branch resolves via
  robfig against `after.Location()` (offset-based) and returns a value, whereas
  the live gocron cron path resolves by *name* (`CRON_TZ=<loc.String()>`) and
  **fails fast at schedule time** for a non-IANA `FixedZone` (ADR-0136). So under
  such a location a cron trigger never actually schedules — there is no live
  fire to be inconsistent with. UTC / `time.Local` / IANA zones are unaffected.
  Documented on the `Trigger.Next` godoc.
- **DST transition instant.** `calendarNext` builds at-times with `time.Date`,
  whose normalization of a non-existent (spring-forward) or ambiguous
  (fall-back) local time may differ from gocron's own DST handling by one
  instance at the exact boundary. This is a rare, boundary-only discrepancy in a
  *reported* instant and is strictly better than today's full-offset divergence
  for every fire. Documented.
- **Test doubles.** `processtest.MemScheduler` and `runtime/internal/runtimetest`
  doubles have no location concept; they compute `Trigger.Next(clock.Now())`, so
  their reported `NextRun` now resolves in the clock's location (typically a UTC
  fake clock → unchanged). Documented; no code change.
- **Owned scheduler location — and clock.** When the runtime creates its own
  default scheduler (no `WithScheduler`), that scheduler is UTC, so `Location()`
  returns UTC. A consumer who wants the *owned* scheduler in a custom zone must
  inject their own via `WithScheduler(NewScheduler(WithLocation(loc)))` — a
  pre-existing gap, out of scope here. Note also that the owned default scheduler
  is built with **no** `WithClock`, so it runs on the wall clock while the runtime
  computes NextRun from `driver.clk`; surface agreement therefore assumes the
  runtime and scheduler share a clock (they do under a shared `WithClock` in tests
  and the wall clock in production, per ADR-0003). A divergent clock source is a
  separate, pre-existing concern independent of location.

## Blast radius

- No public **signature** changes; `Trigger.Next`'s *contract* changes (UTC →
  after's location) and its cron/calendar behavior for a non-UTC `after` changes.
  Additive public API: `NativeScheduler.Location()`.
- Behavior change is observable only for consumers who either drive `Trigger.Next`
  directly with a non-UTC `after`, or run the scheduler under a non-UTC
  `WithLocation` (where the persisted/`Schedule()` NextRun now matches the fire).
  Default-UTC deployments: no change.
- Requires **ADR-0137** (refines/partly reverts ADR-0136 #3) and a CHANGELOG
  entry. Not a wire/persistence format change (the persisted `next_run` column is
  the same type; only its value under a custom zone changes to the correct
  instant).

## Testing (TDD, hot-path-first)

- **`scheduler.Trigger`**
  - `calendarNext` resolves in `after.Location()`: a `Daily(09:00)` `Next` with a
    `+02:00` `after` returns `09:00 +02:00` (not `09:00 UTC`).
  - Re-point the ADR-0136 cron-UTC regression case: `Cron("0 9 * * *")` `Next`
    with a `+02:00` `after` now returns `09:00 +02:00` (was asserted UTC).
  - A UTC `after` still yields UTC for both calendar and cron (default preserved).
- **`scheduler` façade**
  - `Location()` returns UTC by default and the configured zone under
    `WithLocation`.
  - `Schedule()`-return `NextRun` for a `Daily(09:00)` job under
    `WithLocation(+3)` is the `+3`-resolved instant (== `Scheduled().NextRun()`),
    and under default is UTC.
- **`runtime`**
  - The persisted/reported `NextRun` for a calendar timer under a custom-location
    scheduler (via the capability interface) matches the live loc-resolved fire;
    under a UTC/absent-capability scheduler it is UTC (default preserved).
  - Re-point the ADR-0136 `TestNewScheduledTimerJob_CalendarNextRunIsUTC` guard:
    with a UTC-resolving scheduler it stays UTC; add a case proving loc-resolution
    when the scheduler reports a custom location.
- All existing calendar/cron/scheduler/runtime suites stay green under the
  default (UTC) path.

## Documentation

- `Trigger.Next` / `Daily` / `Weekly` / `Monthly` / `Cron` / `ClockTime` godocs:
  state the new "resolves in `after`'s location" contract and the
  cron-FixedZone + DST caveats; drop the "uniform UTC reference" language from the
  ADR-0136 pass.
- `NativeScheduler.Location()` godoc.
- ADR-0137 (Nygard), refining ADR-0136 decision #3.
- CHANGELOG entry.
