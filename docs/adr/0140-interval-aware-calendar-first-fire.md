# 140. Interval-aware `calendarNext` first fire

Status: Accepted — 2026-07-27. Completes the deferred follow-up named in
[ADR-0137](0137-location-aware-trigger-next.md); refines the calendar-recurrence
semantics established in [ADR-0136](0136-calendar-trigger-timezone.md).

## Context

`scheduler.Trigger.Next(after)` is the **pure** recurring-fire computation used
both by tests and, at runtime, to compute the reported/persisted `NextRun`
(`scheduler/scheduler.go:578`, `runtime/timerjob.go:71`). For calendar triggers
(`Daily`/`Weekly`/`Monthly`) it delegates to `calendarNext`
(`scheduler/trigger.go:303`), which scans forward **one civil day at a time** in
`after`'s location and returns the first day matching the kind's day filter at
one of the (sorted) at-times.

The **live** fire path does not use `calendarNext`: it hands the trigger's
`interval`/day-set/at-times to `gocron.DailyJob`/`WeeklyJob`/`MonthlyJob`
(`scheduler/internal/gocron/trigger.go:203-219`), and gocron computes each
`NextRun` itself.

`calendarNext` **never receives `interval`** — the call site passes only
`kind, days, weekdays, atTimes`. So for an `interval>1` calendar trigger whose
current-period at-times are already past `after`, the day-by-day scan returns
the **next matching period-day** (interval ignored), while gocron jumps by
`interval`:

- daily: gocron jumps to `lastRun.Day() + interval` once the current day is exhausted (`job.go:1327`);
- weekly: to the Sunday-anchored week `interval` weeks ahead — `(lastRun.Day() - lastRun.Weekday()) + interval*7` (`job.go:1393`);
- monthly: to day 1 of `lastRun.Month() + interval`, advancing another `interval` months if that month has no matching day (`job.go:1469-1474`).

**Concrete divergence:** `Daily(2, 09:00 UTC)`, `after = 2026-07-10T10:00Z` (past
the day's 09:00). `calendarNext` → **2026-07-11 09:00**; live gocron → **2026-07-12
09:00** — a full day apart, on different points of the interval-2 grid.

ADR-0137 named this as an out-of-scope, pre-existing "day-scan gap" and corrected
the false godoc claim that `Next` matched gocron's first-fire. The persisted /
admin `NextRun` and the `Schedule()`-return value therefore disagree with the
actual live fire for `interval>1` calendar triggers. `interval==1` and all cron
already converge.

The interval grid is anchored to the **reference instant** (`after` on the pure
side, `s.now()`/`lastRun` on the live side) — there is no persisted "day 0"
epoch. Both sides agree on the anchor concept; the only defect is that
`calendarNext` never applies `interval` to grid membership.

### Options considered

1. **Port gocron's three `*Job.next` methods into `calendarNext`.** Faithful but
   error-prone: reimplements firstPass/jump, negative-day-of-month handling,
   month-overflow loops, and DST policy branches — a large surface with many
   edge cases, and easy to drift from gocron on a later gocron bump.
2. **Keep the day-by-day scan; add an interval-grid predicate per kind** (chosen).
   The scan already enumerates only real civil days and correctly handles
   day-of-month overflow (a non-existent day never appears) and location. We add
   one predicate that accepts a scanned day only when it lies on the
   interval grid anchored at `after`'s day/week/month.
3. **Document the divergence as permanent and leave `Next` interval-blind.**
   Rejected: the persisted/admin `NextRun` staying wrong for a supported trigger
   shape is a real correctness defect, not a documentation matter.

## Decision

Make `calendarNext` **interval-aware** by threading `interval` in and adding a
per-kind grid predicate to the existing forward day scan. A scanned day at civil
offset `i` from the start of `after`'s day is accepted only when its **period
index** is a multiple of `interval`:

- **daily** — period index `= i`; accept iff `i % interval == 0`.
- **weekly** — period index `= (int(after.Weekday()) + i) / 7` (Sunday-anchored
  week index, integer civil-day division); accept iff `weekIndex % interval == 0`,
  in addition to the existing weekday-set filter.
- **monthly** — period index `= (day.Year()-after.Year())*12 + int(day.Month()-after.Month())`;
  accept iff `monthIndex % interval == 0`, in addition to the existing
  day-of-month-set filter.

These anchors mirror gocron exactly: index 0 is `after`'s own day/week/month
(considered only for at-times strictly after `after`, unchanged), and index
`k*interval` (k≥1) is gocron's `interval`-jumped period, whose at-times are all
eligible. Day-of-month overflow and "month has no matching day" are handled for
free — the scan simply never offers a non-existent day, so it advances to the
next grid period that does, matching gocron's advance-another-interval loop.

**Scan bound must scale with `interval`.** The forward scan is bounded by
`maxCalendarScanDays` (5 years) so a degenerate shape cannot spin forever. That
bound was sized for the interval-blind scan, which always found a match within
days. Once the grid predicate is applied, the first *accepted* period for a large
interval can lie past 5 years (e.g. `Monthly(60, …)` ≈ "every 5 years",
`Weekly(260, …)`, `Daily(1830, …)`), so a fixed 5-year bound would exhaust and
return `ok=false` while live gocron loops on and fires — reintroducing the exact
pure-vs-live divergence this ADR closes. The loop bound is therefore scaled to
`maxCalendarScanDays * int(interval)` (which is exactly `maxCalendarScanDays` at
`interval==1`, preserving the byte-identical guarantee). A large-interval
convergence row guards this.

**`interval==1` is byte-identical to the prior behavior**: every period index is
a multiple of 1, so the predicate is always true and the scan is unchanged. This
is the regression-safety guarantee.

**`interval==0`** (a degenerate input the constructors do not forbid) is guarded:
`calendarNext` reports `ok=false` for it rather than dividing by zero, consistent
with `Every`/`EveryRandom` reporting `ok=false` for a non-positive interval that
"never advances". The live path also rejects a zero interval at schedule time
(gocron), so no valid schedule reaches a zero-interval `Next`.

**Correctness oracle:** convergence with the live gocron `NextRun`. Tests
construct both a `scheduler.Trigger` and a live gocron-backed scheduler at the
same fake `now`, and assert `Trigger.Next(now) == live NextRun` across
`interval ∈ {1,2,3}` × {daily, weekly (multi-weekday), monthly (multi-day incl.
day 31)} × {`after` before / equal-to / past the current period's at-times}. The
existing convergence test `TestNativeScheduler_ScheduleReturnMatchesLocation`
(`scheduler/location_option_test.go:109`) is extended to `interval>1`.

### Non-goals / explicitly out of scope

- **At-time ordering parity is unchanged.** The `interval==1` path is byte-identical,
  so whatever at-time ordering behavior exists today is preserved. Convergence
  tests for `interval>1` use at-time patterns already known to converge at
  `interval==1`, isolating the interval dimension.
- **DST-policy parity.** wrkflw configures gocron with no non-default DST policy,
  so both sides normalize spring-forward gaps identically via `time.Date`. The
  gocron `DaylightSavingsTimeSkip`/`RunAfterTransition` branches are not exercised
  and are not replicated.
- **Negative day-of-month** (gocron's "day from end") is not part of wrkflw's
  `Monthly(days []int)` surface and is not added here.
- **gocron-sort dependency.** The whole convergence story assumes gocron sorts
  at-times, weekdays, and days-of-month (verified for the pinned v2.22.0:
  `util.go` `convertAtTimesToDateTime`/`removeSliceDuplicatesInt`, `job.go`
  weekday sort). A future gocron bump must re-verify these sorts — the
  pinned-version convergence test is the guard, and will fail loudly if a bump
  changes ordering.

## Consequences

- The persisted/admin `NextRun` and `Schedule()`-return value now match the live
  first fire for `interval>1` calendar triggers, closing the ADR-0137 gap. This
  is a **behavior change for `interval>1` calendar triggers only** — the value
  returned by `Next` changes (to the correct one). Pre-v0.1.0, no stability
  promise; recorded in CHANGELOG.
- `interval==1` and cron are provably unchanged.
- `interval==0` now returns `ok=false` from `Next` (was: next matching day).
  A degenerate input; the live path already rejects it.
- The stale godoc caveats on `Trigger.Next`, the `interval` struct field, and
  `calendarNext` (which document the gap as permanent) are rewritten to state the
  interval-aware behavior.
- The fix stays within the pure, gocron-neutral `scheduler` package — no gocron
  import is added; the grid predicate is a self-contained civil-date computation
  validated against gocron by test.
