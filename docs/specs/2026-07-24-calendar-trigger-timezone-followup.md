# Calendar/cron trigger timezone resolution

Status: Decided (2026-07-24) — supersedes the "Decision: Pending" state this
document opened with. Implementation via ADR-0136.

## Context

Recurring calendar (`scheduler.Daily`, `scheduler.Weekly`, `scheduler.Monthly`)
and `scheduler.Cron` triggers carry a wall-clock at-time (`ClockTime`, or the
cron expression's fields) that says *when during the day* the trigger fires.
Two different code paths resolve that wall-clock time against two different
timezones:

- **Pure / persisted path** — `Trigger.Next` (`scheduler/trigger.go`) is a pure
  function used both by this package's tests *and* in the live runtime to
  compute the reported/persisted `NextRun` (`runtime/timerjob.go`,
  `runtime/timerops.go`) and the façade's `ScheduledJob` metadata
  (`scheduler/scheduler.go`). Its **calendar** branch (`calendarNext`) forces
  `time.UTC`. Its **cron** branch does *not* — `sched.Next(after)` resolves in
  `after`'s own location.
- **Live path** — `runtime.convertTrigger` (`runtime/timerops.go`) hands the
  `scheduler.Trigger` to the internal gocron adapter
  (`scheduler/internal/gocron/trigger.go`), whose `NewGocronScheduler`
  (`scheduler/internal/gocron/scheduler.go`) **never** calls
  `gocron.WithLocation(...)`. gocron therefore falls back to its own default,
  `time.Local`, to resolve both calendar at-times **and** cron expressions.

### The resulting split (pre-existing, environment-dependent)

For a host whose `time.Local ≠ UTC`:

| Kind | Pure `Trigger.Next` / persisted `NextRun` | Live gocron fire | Agree today? |
|---|---|---|---|
| Calendar (Daily/Weekly/Monthly) | UTC | `time.Local` | ❌ no |
| Cron | `after.Location()` (= `now`, i.e. `time.Local`) | `time.Local` | ✅ yes |

So calendar triggers already fire at the wrong wall-clock time relative to the
persisted/documented UTC intent on non-UTC hosts; cron happens to agree only
because both sides land on `time.Local`. This predates the scheduler-owned
durable jobs program (ADR-0134); it was not introduced by it. It was found
while writing `TestNativeSchedulerCalendarTriggers`
(`scheduler/scheduler_test.go`), whose assertions currently document the actual
`time.Local` behavior.

**Containers commonly run `TZ=UTC`**, so a large share of real deployments
observe no discrepancy at all today — the bug bites only non-UTC hosts.

### Severity

- Affects every wire-serializable calendar trigger kind (`KindDaily`,
  `KindWeekly`, `KindMonthly` in `definition/schedule`) and, once the fix pins a
  location, cron (`KindCron`) as well.
- On any non-UTC host, a "remind at 09:00" daily/weekly/monthly trigger fires at
  09:00 *host-local*, not the 09:00 UTC that `Trigger.Next` computes and
  persists — a real operational problem for business-hours-sensitive triggers.
- Not a data-loss bug: the trigger still fires exactly once per occurrence. The
  risk is "fires at the wrong wall-clock time vs. documented/persisted intent,"
  plus a reported-vs-actual `NextRun` mismatch for calendar triggers.

## Options considered

1. **Document as `time.Local`, permanently.** Keep the UTC/local split as the
   contract. No code change beyond docs. Rejected: bakes a UTC/local
   inconsistency into the public API, gives non-UTC hosts no way to pin UTC, and
   leaves reported `NextRun` disagreeing with the actual fire for calendar
   triggers.
2. **Default UTC + opt-out location option.** ✅ **Chosen.** Pin the live
   scheduler to `time.UTC` so it matches the UTC reference, and expose
   `scheduler.WithLocation(*time.Location)` so a consumer can opt into
   `time.Local` or any named zone.
3. **Full consistency: location flows through `after`.** Make `calendarNext`
   respect `after.Location()` and thread a configured location through both the
   runtime arm-path and gocron so both paths always agree for any zone.
   Rejected for now as larger blast radius (touches the pure `Trigger.Next`
   contract and the runtime timer path); retained as a **future-work hook** for
   the residual custom-location caveat below.

## Decision

Adopt **Option 2**, with cron included in scope (it cannot be exempted — see
below).

### 1. Pin the live scheduler to a configured location, default UTC

In `scheduler/internal/gocron/scheduler.go`, `NewGocronScheduler` **always**
appends `gocron.WithLocation(loc)`, where `loc` is resolved as:

```go
loc := s.loc          // set by the new internal WithLocation option
if loc == nil {
    loc = time.UTC    // never fall through to gocron's time.Local default
}
gocronOpts = append(gocronOpts, gocron.WithLocation(loc))
```

This affects **both** calendar at-times and cron expressions, because gocron
applies its scheduler location globally to `DailyJob`/`WeeklyJob`/`MonthlyJob`
**and** `CronJob` — there is no per-job location for `CronJob`, so cron cannot
be exempted.

### 2. Add `scheduler.WithLocation(*time.Location)`

A new façade option on `scheduler.NewScheduler`, parallel to `WithClock` /
`WithLocker` / `WithElector`:

- Stores the location in the façade `config`.
- Threads through to a **new internal** `gocron.WithLocation(*time.Location)`
  option, which sets `s.loc` on the `GocronScheduler`.
- **`nil` is ignored** (nil-guarded like the other options): the resolution
  above then yields `time.UTC`. The guard must live on our side for two
  reasons: (1) gocron's *own* default when no location is pinned is
  `time.Local` — so an unset location must be resolved to `time.UTC` before
  construction, not left to gocron; and (2) `gocron.WithLocation(nil)` returns
  `ErrWithLocationNil` and would fail `gocron.NewScheduler`, so a nil must never
  be forwarded.

### 3. Make `Trigger.Next` uniformly UTC

Force `after.UTC()` in the `triggerCron` branch of `Trigger.Next`
(`scheduler/trigger.go`), so the cron branch matches the calendar branch. After
this, `Trigger.Next` is the **uniform UTC reference** for *every* recurring
kind, and it agrees exactly with the default (UTC) live path.

Without this step, pinning the live scheduler to UTC would *fix* calendar but
*introduce* a new cron divergence (live→UTC, pure→still `time.Local`).

## Consistency contract

| Config | Live fire (gocron) | `Trigger.Next` reference | Agree? |
|---|---|---|---|
| **Default (UTC)** | UTC | UTC | ✅ yes — the fix |
| `WithLocation(loc≠UTC)` | `loc` | UTC | ⚠️ fire correct in `loc`; reporting surfaces diverge — see below |

**Firing and rehydration are correct in every configuration.** What a custom
non-UTC location perturbs is only how the next-run instant is *reported*, and
there are **three reporting surfaces that do not agree** under a custom
location:

- **Persisted / admin** — `runtime/timerops.go` persists
  `Trigger.Next(now).UTC()`; it feeds `MIN(next_run)` → `TimerStats.NextFireAt`
  (`internal/persistence/store/timerstore.go`) and the admin HTTP endpoint's
  per-timer `fire_at` + aggregate `next_fire_at`
  (`transport/http/httpcore/admin_endpoints.go`). **UTC.**
- **Façade `Schedule()` return** — stamped from `Trigger.Next`. **UTC.**
- **Façade `Scheduled(id)` / `List()`** — re-fetch gocron's *live* `NextRun`,
  which respects `WithLocation`. **loc-resolved (correct), not UTC.**

So under `WithLocation(loc≠UTC)` an operator reading the admin API sees UTC
while `Scheduled`/`List` report the `loc` instant for the same job. None of
this affects *when* a timer fires:

- Recurring calendar/cron triggers re-arm from the stored trigger via
  `rehydrateTrigger` (`runtime/timerops.go`), never from `NextRun`. Only
  *non-recurring* one-shots re-arm via `schedule.At(NextRun)`, and one-shots
  carry a location-invariant absolute instant. `PruneTimers` excludes recurring
  rows regardless of their `next_run`, so a stale UTC value never prunes them.

On the **default UTC path there is no split** — all three surfaces read UTC.

Making `Trigger.Next` location-aware (Option 3) would collapse the three
surfaces to one under any location; it is deferred as future work and
referenced from the godoc.

### Two live-path-only behaviors under a named zone

Both apply **only** when a consumer sets a non-UTC `WithLocation`; the default
UTC path is unaffected.

- **DST.** `calendarNext` builds instants in UTC (no DST; every day is 24h),
  but the live scheduler under `WithLocation(America/New_York)` resolves
  at-times per that zone's DST rules. The UTC reference and the live fire
  therefore diverge by a full hour across a DST boundary, and a non-existent
  spring-forward local at-time (e.g. Daily `02:30` on the transition day) is
  gocron-defined. Documented on the `WithLocation` godoc.
- **Multi-replica.** With `WithLocker` / `WithElector`, every replica MUST be
  constructed with the *same* `WithLocation` (as they already must share clock
  semantics); different locations compute different fire wall-times for the same
  timerID, risking multiple fires (`WithLocker`) or a schedule shift on failover
  (`WithElector`). Doc note; no code change.

## Blast radius / breaking behavior

- **Behavior change for non-UTC hosts only.** Calendar + cron triggers move from
  host-local to UTC by default. Hosts already running `TZ=UTC` (typical
  containers) observe **no change**.
- Requires **ADR-0136** and a **CHANGELOG breaking-behavior entry** documenting
  the default shift and the `scheduler.WithLocation(time.Local)` opt-back-in
  migration for consumers who intend host-local resolution.
- Cron is explicitly in scope even though the original follow-up named only
  calendar — the `WithLocation` pin is global and cannot exempt it.

## Testing (TDD, hot-path-first)

Hot paths first, including failure/edge branches, per CLAUDE.md Golang rule #8.

- **`scheduler/internal/gocron`**
  - Default construction pins `time.UTC` (fire-instant assertion for a calendar
    trigger *and* a cron trigger under a simulated non-UTC intent, driven by the
    fake clock).
  - `WithLocation(loc)` is respected (fires resolve in `loc`).
  - `WithLocation(nil)` resolves to UTC (guard behavior).
- **`scheduler` façade**
  - `WithLocation` threads to the underlying engine; nil ignored.
  - No interaction/conflict with `WithClock` / `WithLocker` / `WithElector`.
- **`scheduler.Trigger`**
  - Cron branch is now UTC-uniform: regression test asserting a cron
    `Trigger.Next` result no longer depends on `after`'s (non-UTC) location.
  - Existing calendar `Next` UTC tests continue to pass unchanged.
- **`scheduler` (integration)**
  - Update `TestNativeSchedulerCalendarTriggers` to assert the new UTC default
    rather than the current `time.Local` behavior.
- **`runtime`**
  - Reported/persisted `NextRun` for a calendar trigger matches the default
    (UTC) live fire; re-arm of a recurring calendar trigger is unaffected by a
    custom `WithLocation` (rehydrates from the trigger, not `NextRun`).

## Documentation

- Update the timezone notes on `Daily`/`Weekly`/`Monthly`/`ClockTime` in both
  `scheduler/trigger.go` and `scheduler/internal/gocron/trigger.go` to state the
  new UTC default and point at `scheduler.WithLocation` for opting into a
  different zone; drop the "pre-existing discrepancy / planned follow-up"
  language.
- Document `Trigger.Next` as the uniform UTC reference, and note the
  custom-location cosmetic `NextRun` caveat + the Option 3 future-work hook.
- CHANGELOG breaking-behavior entry (see Blast radius).
- ADR-0136 (Nygard template).
