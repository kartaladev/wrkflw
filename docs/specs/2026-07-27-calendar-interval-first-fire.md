# Spec: interval-aware `calendarNext` first fire

**Status:** design complete → implementation. Decision recorded in
[ADR-0140](../adr/0140-interval-aware-calendar-first-fire.md). Completes the
follow-up deferred by [ADR-0137](../adr/0137-location-aware-trigger-next.md).

## Problem

`scheduler.calendarNext` (`scheduler/trigger.go:303`) computes the pure
`Trigger.Next` first fire for `Daily`/`Weekly`/`Monthly` triggers by scanning
forward one civil day at a time. It never consults `interval`, so for an
`interval>1` calendar trigger whose current-period at-times are already past
`after`, it returns the next matching period-day while the live gocron scheduler
jumps by `interval`. The persisted/admin `NextRun` and `Schedule()`-return value
therefore disagree with the actual fire. See ADR-0140 §Context for the worked
divergence example.

## Design (chosen: day-scan + interval-grid predicate)

Thread `interval uint` into `calendarNext` and accept a scanned day only when its
period index (relative to `after`'s day/week/month) is a multiple of `interval`:

| kind    | period index for scanned `day` at civil offset `i` from start-of-`after`-day | additional accept condition |
|---------|------------------------------------------------------------------------------|-----------------------------|
| daily   | `i`                                                                          | `i % interval == 0`         |
| weekly  | `(int(after.Weekday()) + i) / 7`  (Sunday-anchored week index)               | `weekIndex % interval == 0` **and** weekday ∈ set |
| monthly | `(day.Year()-after.Year())*12 + (int(day.Month())-int(after.Month()))`       | `monthIndex % interval == 0` **and** day-of-month ∈ set |

Index 0 = `after`'s own day/week/month (at-times strictly after `after` only —
existing behavior). Index `k*interval` = gocron's `interval`-jumped period.
Day-of-month overflow (e.g. day 31 in a 30-day month) and "target month has no
matching day" are handled implicitly: the scan never lands on a non-existent day,
so it advances to the next grid period that exists — matching gocron's
advance-another-`interval` loop (verified against gocron for `Monthly({31}, 2)`
from a 30/31-day month, and for Feb).

### Anchor equivalence to gocron (why it converges)

- **daily** gocron: `startNextDay = lastRun.Day() + interval` (`job.go:1327`) ⇔ accept `i ∈ {0, interval, 2·interval, …}`.
- **weekly** gocron: `startOfNextIntervalWeek = (lastRun.Day() − lastRun.Weekday()) + interval·7` (`job.go:1393`) ⇔ Sunday-anchored `weekIndex % interval == 0`; firstPass considers `wd ≥ lastRun.Weekday()` ⇔ the scan starts at `after`'s day.
- **monthly** gocron: `from = Date(year, month+interval, 1)`, loop `+interval` months (`job.go:1469-1474`) ⇔ `monthIndex % interval == 0`.

### Edge cases

- `interval==1` → predicate always true → **byte-identical to current behavior** (regression bar).
- `interval==0` → `ok=false` (guard the modulo; consistent with `Every` non-positive → `ok=false`; live path rejects zero interval anyway — gocron `Err{Daily,Weekly,Monthly}JobZeroInterval`).
- **Large interval vs scan bound (audit F1):** the `maxCalendarScanDays` (5-year) loop bound must scale to `maxCalendarScanDays * int(interval)`, else a large-interval first fire (e.g. `Monthly(60)` ≈ every 5 years) lands past the bound → `ok=false` while gocron fires = the very divergence being closed. At `interval==1` the scaled bound equals the original (byte-identical).
- Location/DST: civil-day arithmetic on both sides; no non-default gocron DST policy configured (out of scope, ADR-0140 §Non-goals).
- At-time / weekday / day-of-month ordering parity: unchanged (interval==1 path untouched). gocron sorts all three (verified v2.22.0); a future bump re-verifies via the convergence test.

## Test plan (correctness oracle = convergence with live gocron)

1. **Pure unit table** — new `interval>1` cases in `TestTrigger_Next`
   (`scheduler/trigger_test.go:12`) asserting exact expected instants for
   daily/weekly/monthly with `after` positioned **past** the current period
   (the bug branch), plus a same-period case (`after` before the at-time) that
   must be unchanged.
2. **Convergence** — extend `TestNativeScheduler_ScheduleReturnMatchesLocation`
   (`scheduler/location_option_test.go:109`) to assert `Schedule()`-return
   `NextRun` (pure path) `==` live `Scheduled().NextRun()` (gocron path) for
   `interval ∈ {2,3}` × {daily, weekly multi-weekday, monthly incl. day 31} ×
   {`after` before / past current period}. This is the property the bug breaks;
   it must fail before the fix and pass after. In-memory gocron, no Docker.
3. **Regression** — the existing `interval==1` cases in `TestTrigger_Next`,
   `TestNativeSchedulerCalendarTriggers`, and the gocron-side `TriggerDef` tests
   must stay green unchanged.

## Blast radius

- `scheduler/trigger.go`: `calendarNext` signature (+`interval`), the call site in
  `Trigger.Next` (pass `t.interval`), the grid predicate, and the three godoc
  caveats (struct field :64-71, `Trigger.Next` :194-200, `calendarNext` :296-302)
  rewritten to state interval-aware behavior.
- `scheduler/trigger_test.go`, `scheduler/location_option_test.go`: new/extended tests.
- `CHANGELOG.md`: behavior-change note (interval>1 calendar `Next` now matches live fire).
- No change to the gocron adapter (`scheduler/internal/gocron/*`) — it already
  passes `interval` to gocron; only the pure side was blind.
