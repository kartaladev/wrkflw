# Plan: interval-aware `calendarNext` first fire (ADR-0140)

> REQUIRED SUB-SKILL: superpowers:test-driven-development. Red-first, each step's
> failing run is a deliverable. Spec: `docs/specs/2026-07-27-calendar-interval-first-fire.md`.
> ADR: `docs/adr/0140-interval-aware-calendar-first-fire.md`.

**Goal:** Make `scheduler.calendarNext` interval-aware so `Trigger.Next` matches
the live gocron first fire for `interval>1` `Daily`/`Weekly`/`Monthly` triggers,
closing the ADR-0137 gap. `interval==1` must stay byte-identical.

**Architecture:** Single pure-package change in `scheduler/trigger.go` plus tests.
No gocron import; the fix is a civil-date grid predicate validated against gocron
by a convergence test. One feature bundle.

## Global constraints

- Module `github.com/kartaladev/wrkflw`, Go 1.25. Pure `scheduler` package stays
  gocron-neutral (no gocron import in `scheduler/trigger.go`).
- `interval==1` and cron: provably unchanged — the existing `interval==1` calendar
  tests and all cron tests must stay green with no edits.
- Correctness oracle is **convergence with live gocron `NextRun`**, asserted by an
  in-memory (no Docker) scheduler test.
- Location-aware (ADR-0137) preserved: all date math in `after.Location()`.
- `interval==0` → `ok=false` (guard the modulo).
- Pair `foo.go` with `foo_test.go`. `golangci-lint run ./...` clean; touched-package
  coverage ≥85% (hot path = `calendarNext` — cover all three kinds + the
  bug/same-period/overflow branches).

---

### Task 1: convergence + unit tests (RED), then interval-aware `calendarNext` (GREEN)

**Files:**
- Modify: `scheduler/trigger.go` — `calendarNext` (:303) signature + grid predicate; call site in `Trigger.Next` (:231); godoc caveats (:64-71, :194-200, :296-302).
- Modify: `scheduler/trigger_test.go` — new `interval>1` cases in `TestTrigger_Next`.
- Modify: `scheduler/location_option_test.go` — extend `TestNativeScheduler_ScheduleReturnMatchesLocation` to `interval ∈ {2,3}`.

**Interfaces:**
- `calendarNext(after time.Time, kind triggerKind, interval uint, days []int, weekdays []time.Weekday, atTimes []ClockTime) (time.Time, bool)` — `interval` added (place it right after `kind` to read naturally).
- Call site: `return calendarNext(after, t.kind, t.interval, t.days, t.weekdays, t.atTimes)`.

- [ ] **Step 1 — RED: pure unit cases.** Add to `TestTrigger_Next` (table form per the project `table-test` skill — per-case `assert` closure, `t.Context()` not needed here as `Next` is pure) cases asserting exact instants:
  - `Daily(2, 09:00)`, after `2026-07-10T10:00Z` (past 09:00) → expect `2026-07-12T09:00Z` (currently returns 07-11).
  - `Weekly(2, {Mon,Wed}, 09:00)`, after `2026-07-22T10:00Z` (Wed, past this week's Wed 09:00) → expect `2026-08-03T09:00Z` (Monday two interval-weeks out; verified against gocron in the ADR-0140 audit).
  - `Monthly(2, {31}, 09:00)`, after `2026-01-31T10:00Z` → expect `2026-03-31T09:00Z`.
  - One **same-period unchanged** case: `Daily(2, 09:00)`, after `2026-07-10T08:00Z` → `2026-07-10T09:00Z` (must already pass, guards no-regression on the before-at-time branch).
  Run `go test ./scheduler/ -run TestTrigger_Next` → the interval>1 cases FAIL (interval currently ignored). Capture output.

- [ ] **Step 2 — RED: convergence.** Extend `TestNativeScheduler_ScheduleReturnMatchesLocation` with `interval ∈ {2,3}` rows for daily/weekly(multi-weekday)/monthly(incl. day 31), each with `after`/fake-clock positioned past the current period. **Include one large-interval row (audit F1)** — e.g. `Monthly(60, {28})` positioned past the anchor — so the scaled scan bound is exercised (this row would return `ok=false`/wrong under an unscaled 5-year bound while gocron fires). Assert `Schedule()`-return `NextRun == Scheduled().NextRun()`. Run `go test ./scheduler/ -run ScheduleReturnMatchesLocation` → FAILS on interval>1 rows (pure vs gocron disagree). Capture output. (In-memory gocron; no Docker.)

- [ ] **Step 3 — GREEN: implement the grid predicate.** In `calendarNext`:
  - Add `interval uint` param; if `interval == 0` return `time.Time{}, false`.
  - **Scale the loop bound (audit F1):** `bound := maxCalendarScanDays * int(interval)` and loop `for i := 0; i <= bound; i++`. At `interval==1` this equals `maxCalendarScanDays` exactly (byte-identical).
  - In the scan loop, before the existing `switch kind` day-filter, compute and apply the period-index predicate:
    - `triggerDaily`: `if i%int(interval) != 0 { continue }`.
    - `triggerWeekly`: `weekIndex := (int(after.Weekday()) + i) / 7; if weekIndex%int(interval) != 0 { continue }` (in addition to the weekday-set check).
    - `triggerMonthly`: `monthIndex := (day.Year()-after.Year())*12 + (int(day.Month())-int(after.Month())); if monthIndex%int(interval) != 0 { continue }` (in addition to the day-set check).
  - Keep everything else (at-time sort, `.After(after)` check, location) exactly as-is.
  Update the call site to pass `t.interval`.
  Run Steps 1 & 2 tests → PASS.

- [ ] **Step 4 — GREEN: full package + regression.** Run `go test ./scheduler/... -race` → all PASS (existing `interval==1` calendar tests, `TestNativeSchedulerCalendarTriggers`, gocron-side `TriggerDef` tests unchanged and green). Confirm `go test ./scheduler/internal/gocron/... -race` green.

- [ ] **Step 5 — docs: rewrite the three godoc caveats.** Update the `interval` struct-field comment (:64-71), the `Trigger.Next` interval caveat (:194-200), and the `calendarNext` doc (:296-302) to state interval-aware first-fire matching the live scheduler. Remove the "tracked separately"/"day-scan gap" language.

- [ ] **Step 6 — CHANGELOG + verify.** Add a CHANGELOG entry (behavior change: interval>1 calendar `Trigger.Next`/persisted `NextRun` now matches the live first fire; interval==1 unchanged; ADR-0140). Run `go test -race ./... `, coverage ≥85% on `scheduler`, `golangci-lint run ./...` clean.

- [ ] **Step 7 — commit (WIP, folded at delivery).**
  ```
  git add -A
  git commit -m "fix(scheduler): interval-aware calendar first fire (ADR-0140)"
  ```

---

## Self-review
- **Spec coverage:** grid predicate (all 3 kinds) → Step 3; convergence oracle → Step 2; interval==1 regression → Steps 1/4; interval==0 guard → Step 3; scan-bound scaling (F1) → Step 3 + large-interval row Step 2; godoc → Step 5; CHANGELOG → Step 6. No spec section unmapped.
- **Regression safety:** interval==1 predicate always true (`i%1==0`) AND scaled bound `= maxCalendarScanDays*1` → scan unchanged; guarded by the same-period unit case + full-suite green.
- **Audit F1–F4 folded:** bound scaling (F1, Steps 2/3), concrete weekly case (F2, Step 1), monthIndex wording (F3, Step 3), gocron-sort dependency noted in ADR non-goals (F4).
- **Oracle:** convergence test fails before (Step 2 RED), passes after (Step 3) — the property is real, not self-referential.
- **Placeholders:** none — every code step names the exact edit; expected instants are computed from fixed `after` values in the tests.
