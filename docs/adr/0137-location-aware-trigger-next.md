# 137. Make `Trigger.Next` location-aware to collapse the NextRun surfaces

- Status: Accepted
- Date: 2026-07-24
- Refines: [ADR-0136](0136-calendar-trigger-timezone.md) (decision #3)

## Context

ADR-0136 pinned the live scheduler to UTC by default and added
`scheduler.WithLocation(*time.Location)`. To keep the pure reference aligned
with the default live path, it also made `scheduler.Trigger.Next` a **uniform
UTC reference** — forcing `after.UTC()` in the cron branch, with the calendar
branch (`calendarNext`) already forcing UTC. That was a **deliberate stepping
stone**: ADR-0136 prioritized fixing *firing correctness* for non-UTC hosts with
the minimal-risk contract touch, and consciously deferred unifying the
*display* surfaces — its own Consequences named "a future location-aware
`Trigger.Next`" as the follow-up — so that the pure-`Trigger.Next` contract
change and its blast radius could be isolated into this ADR. This ADR is that
deferred step, not a reversal of a considered decision.

That produced a documented, display-only gap: under a **custom** non-UTC
`WithLocation`, the next-run instant is reported by three surfaces that disagree.
Two of them *compute* the value via `Trigger.Next` and land on UTC:

- the runtime's persisted `NextRun` (`runtime/timerops.go`), which also feeds
  `TimerStats.NextFireAt` and the admin HTTP endpoint; and
- the façade `Schedule()` return value (`scheduler/scheduler.go`).

The third — `Scheduled(id)` / `List()` — re-fetches gocron's live,
location-resolved `NextRun`, which is the actually-correct instant. So under a
custom location an operator sees UTC from the admin API but the `loc` instant
from `Scheduled`/`List` for the same job. Firing and rehydration are unaffected
(recurring timers re-arm from the trigger, not from `NextRun`), so this is
display-only; ADR-0136 recorded collapsing the surfaces as deferred future work.

The runtime persists `NextRun` **before** arming (direct-Save inside the
state-commit transaction, ADR-0134), so it cannot read gocron's post-arm value —
it must compute its own `Trigger.Next` and therefore must know the scheduler's
location. The full analysis is in
`docs/specs/2026-07-24-location-aware-trigger-next.md`.

## Decision

We make `Trigger.Next` location-aware and thread the scheduler's configured
location to both computing surfaces, so all three surfaces agree under any
location while the default (UTC) path stays byte-identical.

1. **`Trigger.Next` resolves at-times in `after.Location()`.** In
   `scheduler/trigger.go`, `calendarNext` builds candidate at-times (and its
   day-scan start) in `after.Location()` rather than normalizing to
   `after.UTC()`, and the cron branch reverts to `sched.Next(after)` (dropping
   the ADR-0136 `after.UTC()`). The new contract: *`Trigger.Next` resolves
   recurring at-times in the location of `after`.* It remains pure, total, and
   deterministic. This **refines, and for the cron branch reverts, ADR-0136
   decision #3.**

2. **The scheduler exposes and uses its resolved location.**
   `func (s *NativeScheduler) Location() *time.Location` returns `config.loc` if
   set, else `time.UTC` (never nil). `Schedule()` computes the caller's
   `NextRun` as `j.Trigger().Next(s.now().In(s.location()))`.

3. **The runtime threads the location via a capability interface.** The runtime
   defines an unexported `locatedScheduler interface { Location() *time.Location }`,
   type-asserts its scheduler (defaulting to `time.UTC` when the assertion fails),
   and passes `now.In(loc)` to `Trigger.Next` at its two compute sites
   (`timerops.go`, `timerjob.go`). The existing `.UTC()` persistence
   normalization stays — it re-expresses the now-correct absolute instant. The
   capability-interface approach is non-breaking (consumers and doubles
   implementing `scheduler.Scheduler` need no new method) and mirrors the
   existing `Notifier` / `Locker` / `Elector` capability pattern.

Resulting contract:

| Config | Live fire | Persisted/admin + `Schedule()` | `Scheduled`/`List` | Agree? |
|---|---|---|---|---|
| Default (UTC) | UTC | UTC | UTC | yes (byte-identical to today) |
| `WithLocation(loc)` | `loc` | `loc` | `loc` | yes (the fix) |

## Consequences

- **Reporting is consistent under any location.** The persisted/admin `NextRun`,
  `Schedule()`'s return, and `Scheduled`/`List` all report the same instant the
  timer actually fires at — the display-only split ADR-0136 left open is closed.
- **The default path is byte-identical.** The default scheduler location is UTC,
  so every compute site passes `now.In(UTC)` and produces exactly today's UTC
  values. Only custom-location deployments change, from wrong-reported to
  correct-reported. No wire/persistence format change (the `next_run` column is
  the same type; only its value under a custom zone changes to the correct
  instant).
- **`Trigger.Next`'s contract changes** from "uniform UTC" to "resolves in
  `after`'s location." A consumer driving `Trigger.Next` directly with a non-UTC
  `after` now gets a result in that zone rather than UTC. Additive public API:
  `NativeScheduler.Location()`. No signatures change.
- **`interval > 1` calendar first-fire gap is NOT closed (separate, pre-existing
  bug).** `calendarNext` ignores the `interval` when computing the first fire
  (day-by-day scan → next matching day), while the live gocron calendar jobs jump
  by `interval` once the current period is exhausted. So for `interval>1` calendar
  triggers whose current-period at-times are already past, the persisted/admin and
  `Schedule()`-return surfaces still disagree with the live fire — in both the
  default and custom-location rows. This is a day-scan defect orthogonal to
  timezone, predating ADR-0136; it is out of scope here and tracked as a follow-up
  (interval-aware `calendarNext` first-fire). This ADR corrects the false godoc
  claim ("matching gocron's first-fire behaviour"). `interval==1` (the common
  calendar shape) and all cron fully converge.
- **Cron + non-IANA `time.FixedZone` residual.** `Trigger.Next`'s cron branch
  resolves via robfig against `after.Location()` (offset-based) and returns a
  value, while the live gocron cron path resolves by name
  (`CRON_TZ=<loc.String()>`) and fails fast at schedule time for a non-IANA
  `FixedZone` (ADR-0136). Under such a location a cron trigger never schedules,
  so there is no live fire to be inconsistent with. UTC / `time.Local` / IANA
  zones are unaffected. Documented on the `Trigger.Next` godoc.
- **DST transition instant.** `calendarNext` builds at-times with `time.Date`,
  whose normalization of a non-existent (spring-forward) or ambiguous (fall-back)
  local time may differ from gocron's own DST handling by one instance at the
  exact boundary. This is a rare, boundary-only discrepancy in a *reported*
  instant, strictly better than today's full-offset divergence for every fire.
  Documented.
- **Test doubles and the owned scheduler.** `processtest.MemScheduler` and the
  `runtime/internal/runtimetest` doubles have no location concept, so they resolve
  `Trigger.Next` in the clock's location (typically a UTC fake clock → unchanged);
  documented, no code change. When the runtime creates its own default scheduler
  it is UTC, so `Location()` returns UTC; a consumer wanting the owned scheduler in
  a custom zone must inject `WithScheduler(NewScheduler(WithLocation(loc)))` — a
  pre-existing gap, out of scope.
- **ADR-0136 relationship.** ADR-0136 stays Accepted; this ADR refines its
  decision #3. The `scheduler.WithLocation` option, the UTC default, and the
  live-scheduler pin from ADR-0136 are all unchanged.
