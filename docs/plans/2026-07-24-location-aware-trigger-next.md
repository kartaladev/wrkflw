# Location-aware `Trigger.Next` Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make `scheduler.Trigger.Next` resolve at-times in `after.Location()`, expose the scheduler's resolved location, and thread it into the runtime + `Schedule()` so all three NextRun reporting surfaces agree under any `WithLocation` — while the default (UTC) path stays byte-identical.

**Architecture:** `Trigger.Next` becomes location-aware (drops forced UTC in `calendarNext` and the cron branch). The two surfaces that *compute* NextRun (`scheduler.NativeScheduler.Schedule()` and the runtime's timer-arm/rehydrate paths) pass `now.In(loc)`, where `loc` is the scheduler's resolved location — exposed via a new `NativeScheduler.Location()` method and consumed by the runtime through an opt-in capability interface (default `time.UTC`).

**Tech Stack:** Go 1.25, `robfig/cron/v3` (cron computation), `jonboulle/clockwork` (fake clock), testify.

## Global Constraints

- Go 1.25. Implements ADR-0137 (which refines ADR-0136 decision #3).
- Never import gocron/clockwork outside `scheduler/internal/gocron` + existing façade wiring. This change touches `scheduler/trigger.go`, `scheduler/scheduler.go`, `runtime/`, none of which gain a gocron import.
- Strict TDD (CLAUDE.md #6): failing test first, observable red, then impl. Behavioral changes get a red state.
- `table-test` skill (mandatory): multi-case tables use the per-case `assert` closure form (NOT want/wantErr fields), `t.Context()`.
- Black-box tests (`package <pkg>_test`) where the API is exported; white-box (`package runtime`) only to reach unexported `timerJob`/`newScheduledTimerJob`.
- **Default-UTC invariant:** in the FINAL state (after T3), the default (no `WithLocation`) path produces exactly today's UTC NextRun values — **because every compute site applies `now.In(UTC)` before `Trigger.Next`, independent of the clock's own zone** (NOT because the test clocks are UTC — `clockwork.NewFakeClock()` seeds from `time.Now()`, i.e. `time.Local`, which on this host is WIB/UTC+7). `now.In(UTC)` re-expresses the same instant in UTC, and `Trigger.Next` with `loc=UTC` reproduces the old forced-`after.UTC()` path exactly.
- **Coupled sequence T1→T2→T3; do NOT "fix" intermediate breakage by re-pointing to Local.** T1 changes the `Trigger.Next` contract to respect `after.Location()`; only T2/T3 then feed it `now.In(UTC)`. In the *intermediate* state (T1 landed, T2/T3 not yet), a calendar/cron test that passes a `time.Local` clock `now` and asserts a UTC instant may break — this is an artifact of the incomplete state. Do **NOT** re-point such an assertion to a Local value: T2/T3 restore UTC and would re-break it. If one breaks at T1 on this WIB host, verify it returns to green at the END of T3, not by editing the assertion. (In practice the current calendar/cron+instant tests use explicit UTC/`FixedZone` seeds, so none is expected to break — but the rule stands.)
- Each task MUST run the FULL touched set — `go test ./scheduler/... ./runtime/...` — not just its own package, to catch cross-package ripple (the runtime calls `Trigger.Next`). The final verification (Task 4) additionally runs `TZ=Asia/Jakarta go test ./scheduler/... ./runtime/...` to prove non-UTC-host safety of the default path.
- Verification floor: `go test -race ./...` green, ≥85% coverage on touched packages, `golangci-lint run ./...` clean. Hot paths (trigger resolution, timer arm/fire) covered first.
- Per-task WIP commits (for clean review diffs); squashed into ONE feature-bundle commit before the Delivery Gate + merge (controller handles the squash).

---

### Task 1: `Trigger.Next` resolves at-times in `after.Location()`

Drop the forced UTC in both the calendar (`calendarNext`) and cron branches so `Trigger.Next` resolves in the location carried by `after`.

**Files:**
- Modify: `scheduler/trigger.go` (cron branch of `Trigger.Next`; `calendarNext`; godocs on `Trigger.Next`/`Cron`/`Daily`/`Weekly`/`Monthly`/`ClockTime`)
- Test: `scheduler/trigger_test.go` (re-point the ADR-0136 cron-UTC case; add a calendar location case)
- Test: `runtime/timerops_location_test.go` (re-point the ADR-0136 guard — the `calendarNext` contract change breaks it here, so it is re-pointed in THIS task, not Task 3)

**Interfaces:**
- Consumes: nothing new.
- Produces: `Trigger.Next(after time.Time) (time.Time, bool)` — unchanged signature; now resolves calendar/cron at-times in `after.Location()`.

- [ ] **Step 1: Write/adjust the failing tests**

In `scheduler/trigger_test.go`, the ADR-0136 case `"cron resolves in UTC regardless of after location"` currently asserts UTC for a `+02:00` `after`. Re-point it to the new contract and add a calendar case. Both go in the existing `TestTrigger_Next` table (per-case `after` override field already exists). Replace the cron case body and add a calendar row:

```go
{
    name:  "cron resolves in after's location",
    after: time.Date(2026, 1, 1, 0, 0, 0, 0, time.FixedZone("plusTwo", 2*60*60)),
    trig:  scheduler.Cron("0 9 * * *"),
    assert: func(t *testing.T, next time.Time, ok bool) {
        // after is 2026-01-01 00:00 +02:00; the next 09:00 is resolved in
        // that same +02:00 zone (ADR-0137), i.e. 2026-01-01 09:00 +02:00.
        want := time.Date(2026, 1, 1, 9, 0, 0, 0, time.FixedZone("plusTwo", 2*60*60))
        if !ok || !next.Equal(want) {
            t.Fatalf("next=%v ok=%v want %v", next, ok, want)
        }
    },
},
{
    name:  "daily resolves in after's location",
    after: time.Date(2026, 1, 1, 0, 0, 0, 0, time.FixedZone("plusTwo", 2*60*60)),
    trig:  scheduler.Daily(1, scheduler.ClockTime{Hour: 9}),
    assert: func(t *testing.T, next time.Time, ok bool) {
        want := time.Date(2026, 1, 1, 9, 0, 0, 0, time.FixedZone("plusTwo", 2*60*60))
        if !ok || !next.Equal(want) {
            t.Fatalf("next=%v ok=%v want %v", next, ok, want)
        }
    },
},
```

(Keep the existing UTC-`after` cron and calendar rows unchanged — they prove the default/UTC path is preserved.)

Also re-point the ADR-0136 runtime guard, which the `calendarNext` contract change breaks (it calls `newScheduledTimerJob(j, now)` with a `+02:00` `now` and asserts UTC). In `runtime/timerops_location_test.go`, DELETE `TestNewScheduledTimerJob_CalendarNextRunIsUTC` and replace it with:

```go
// newScheduledTimerJob resolves NextRun in the location of the now it is given
// (ADR-0137); the runtime passes now.In(scheduler location) at its call sites.
func TestNewScheduledTimerJob_CalendarNextRunHonorsNowLocation(t *testing.T) {
	plusTwo := time.FixedZone("plusTwo", 2*60*60)
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, plusTwo)
	j := &timerJob{trig: scheduler.Daily(1, scheduler.ClockTime{Hour: 9})}

	sj := newScheduledTimerJob(j, now)

	// 09:00 in +02:00 == 07:00 UTC.
	want := time.Date(2026, 1, 1, 7, 0, 0, 0, time.UTC)
	assert.True(t, sj.NextRun().UTC().Equal(want), "want %s UTC, got %s", want, sj.NextRun().UTC())
}
```

- [ ] **Step 2: Run to verify red**

Run: `go test -run '^TestTrigger_Next$' ./scheduler/` — FAIL (the two new/changed rows still see UTC from the current forced-UTC code: `next` is `09:00 +00:00`, want `09:00 +02:00`).
Run: `go test -run '^TestNewScheduledTimerJob_CalendarNextRunHonorsNowLocation$' ./runtime/` — FAIL (current `calendarNext` forces UTC, so it returns `09:00 UTC`, want `07:00 UTC`).

- [ ] **Step 3: Implement — drop the forced UTC**

In `scheduler/trigger.go`:

Cron branch of `Trigger.Next` — change:
```go
		// Resolve in UTC so Next is the uniform UTC reference ...
		return sched.Next(after.UTC()), true
```
to:
```go
		// Resolve in after's location (ADR-0137): robfig/cron computes Next in
		// the location of the passed instant. The runtime and the façade
		// Schedule() pass now.In(scheduler location), so this matches the live
		// scheduler; a UTC after (the default) yields UTC.
		return sched.Next(after), true
```

In `calendarNext`, resolve a `loc` once and build all instants in it:
```go
func calendarNext(after time.Time, kind triggerKind, days []int, weekdays []time.Weekday, atTimes []ClockTime) (time.Time, bool) {
	loc := after.Location()
	// (was: after = after.UTC())
```
then change the two `time.Date(...)` calls from `time.UTC` to `loc`:
```go
	start := time.Date(after.Year(), after.Month(), after.Day(), 0, 0, 0, 0, loc)
	...
			candidate := time.Date(day.Year(), day.Month(), day.Day(), int(ct.Hour), int(ct.Minute), int(ct.Second), 0, loc)
```
(Remove the `after = after.UTC()` line entirely; keep everything else — the sort, the weekday/day sets, the scan bound — unchanged. `after.After(candidate)` comparisons are instant comparisons, unaffected by representation.)

Update godocs (drop the "uniform UTC reference" wording from the ADR-0136 pass). **Also remove any surviving "matching gocron's first-fire behaviour" claim** (it is FALSE for `interval>1` — see below):
- `Trigger.Next`: "…[Daily], [Weekly], [Monthly], and [Cron] resolve the next matching occurrence in the location of `after` (ADR-0137). A UTC `after` yields UTC. Cron additionally: robfig/cron resolves the expression in `after`'s location; note the live gocron scheduler resolves cron by zone *name*, so a `Cron` trigger under a non-IANA `time.FixedZone` fails to schedule there (ADR-0136) — use UTC/Local/IANA zones with cron. **Interval caveat:** for `interval>1` calendar triggers whose current-period at-times are already past, Next returns the next matching period-day (interval ignored on the first fire), whereas the live scheduler jumps by `interval` — so the two can differ for the first fire of an `interval>1` trigger (a pre-existing day-scan gap, tracked separately)."
- Where the existing `interval` godoc on the `Trigger` struct / `Next` says the interval "affects only subsequent fires … matching gocron's first-fire behaviour," change it to state honestly that `calendarNext` ignores `interval` on the first fire and this can differ from gocron for `interval>1` (do NOT keep the "matching gocron" phrase).
- `Cron`: "…[Trigger.Next] resolves the expression in `after`'s location."
- `Daily`/`Weekly`/`Monthly`: "…[Trigger.Next] resolves at-times in `after`'s location (ADR-0137)."
- `ClockTime`: "…[Trigger.Next] resolves it in the location of the `after` instant it is given; the live scheduler resolves it in its configured zone (default UTC, see the scheduler's WithLocation)."

- [ ] **Step 4: Run to verify green + no cross-package ripple**

Run: `go test -run '^TestTrigger_Next$' ./scheduler/` (green)
Run: `go test -run '^TestNewScheduledTimerJob_CalendarNextRunHonorsNowLocation$' ./runtime/` (green)
Run: `go test ./scheduler/... ./runtime/...` — full touched set. The re-pointed guard above is the ONLY expected change; everything else stays green because the current calendar/cron+instant tests use explicit UTC/`FixedZone` seeds. If some OTHER test breaks because it passes a `time.Local` clock `now` and asserts UTC, that is the incomplete-intermediate-state artifact from the Global Constraints — do **NOT** re-point it to a Local value (T2/T3 restore UTC and will re-break it); leave it and confirm it returns to green at the end of Task 3. Only revert the production change if a NON-intermediate correctness break appears.
Run: `golangci-lint run ./scheduler/... ./runtime/...` — clean.

- [ ] **Step 5: Commit** — per-task WIP commit `feat(scheduler): Trigger.Next resolves in after's location (ADR-0137)` (controller squashes later). Trailers:
```
Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_016FzuYkwCwdXJ9VWchXAcB4
```

---

### Task 2: Scheduler exposes `Location()` and uses it in `Schedule()`

Add `NativeScheduler.Location()` (resolved zone, default UTC) and make `Schedule()` compute its returned `NextRun` in that zone.

**Files:**
- Modify: `scheduler/scheduler.go` (add unexported `location()`, exported `Location()`; `Schedule()` uses `s.now().In(s.location())`)
- Test: `scheduler/location_option_test.go` (add `Location()` + `Schedule()`-return cases) OR a new `scheduler/scheduler_location_method_test.go`

**Interfaces:**
- Consumes: `Trigger.Next` (Task 1).
- Produces: `func (s *NativeScheduler) Location() *time.Location` — returns `config.loc` if set, else `time.UTC` (never nil). This is the method the runtime's capability interface (Task 3) type-asserts.

- [ ] **Step 1: Write the failing test**

In `scheduler/location_option_test.go` (black-box `scheduler_test`), add a test for `Location()` and one asserting `Schedule()`-return matches `Scheduled()` under a custom zone:

```go
func TestNativeScheduler_LocationMethod(t *testing.T) {
	plusThree := time.FixedZone("plusThree", 3*60*60)
	cases := []struct {
		name string
		opts []scheduler.Option
		want *time.Location
	}{
		{name: "default is UTC", opts: nil, want: time.UTC},
		{name: "WithLocation reflected", opts: []scheduler.Option{scheduler.WithLocation(plusThree)}, want: plusThree},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s, err := scheduler.NewScheduler(c.opts...)
			require.NoError(t, err)
			t.Cleanup(func() { _ = s.Close() })
			assert.Equal(t, c.want, s.Location())
		})
	}
}
```

And a Schedule()-return case (reuse the `mustJob`/`JobKind` helpers from `scheduler_test.go`; under `WithLocation(+3)` a `Daily(09:00)` job's `Schedule()`-returned `NextRun().UTC()` must equal `06:00 UTC`, matching `Scheduled()`):

```go
func TestNativeScheduler_ScheduleReturnMatchesLocation(t *testing.T) {
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	plusThree := time.FixedZone("plusThree", 3*60*60)
	clk := clockwork.NewFakeClockAt(start)
	s, err := scheduler.NewScheduler(scheduler.WithClock(clk), scheduler.WithLocation(plusThree))
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })

	sj, err := s.Schedule(t.Context(), mustJob(t, "daily-9am", surfaceKind,
		scheduler.Daily(1, scheduler.ClockTime{Hour: 9}), func() {}))
	require.NoError(t, err)
	// 09:00 at UTC+3 == 06:00 UTC — the Schedule()-return now matches the live fire.
	assert.True(t, sj.NextRun().UTC().Equal(time.Date(2026, 1, 1, 6, 0, 0, 0, time.UTC)),
		"want 06:00 UTC, got %s", sj.NextRun().UTC())

	// Prove CONVERGENCE with the live surface — the whole point of the change:
	// arm the job and confirm Scheduled()'s live gocron NextRun equals the
	// Schedule()-return value (both loc-resolved), not just the computed number.
	require.NoError(t, s.Activate(t.Context(), sj))
	live, err := s.Scheduled(t.Context(), "daily-9am")
	require.NoError(t, err)
	assert.True(t, live.NextRun().Equal(sj.NextRun()),
		"Schedule()-return %s must equal live Scheduled() %s", sj.NextRun(), live.NextRun())
}
```

**Note for the implementer:** confirm the exact `mustJob` signature and the `JobKind` (shown here as `surfaceKind`) from `scheduler/scheduler_test.go` and use those. If `Schedule()`/`Activate()` require the scheduler started, follow whatever `TestNativeScheduler_WithLocation` (already in this file) does. If `mustJob` builds an `ActivationAuto` job (armed by `Schedule` itself), the explicit `Activate` may be redundant — in that case read the live value directly via `Scheduled` after `Schedule`. Adjust to the real Job/activation shape; the assertion that matters is `live.NextRun().Equal(sj.NextRun())`.

- [ ] **Step 2: Run to verify red**

Run: `go test -run '^TestNativeScheduler_LocationMethod$|^TestNativeScheduler_ScheduleReturnMatchesLocation$' ./scheduler/`
Expected: `TestNativeScheduler_LocationMethod` FAILS to compile (`s.Location` undefined); after adding `Location()` but before the `Schedule()` change, `TestNativeScheduler_ScheduleReturnMatchesLocation` FAILS (Schedule returns `09:00 UTC`, want `06:00 UTC`).

- [ ] **Step 3: Implement**

In `scheduler/scheduler.go`, add the helpers near `now()`:
```go
// location resolves the effective timezone the scheduler resolves calendar
// at-times and cron expressions in: the WithLocation value if set, else
// time.UTC (never nil), mirroring the internal engine's nil->UTC default.
func (s *NativeScheduler) location() *time.Location {
	if s == nil || s.cfg.loc == nil {
		return time.UTC
	}
	return s.cfg.loc
}

// Location reports the timezone this scheduler resolves calendar at-times and
// cron expressions in (see [WithLocation]); time.UTC by default. The runtime
// reads this (via an opt-in capability interface) so the NextRun it computes
// and persists matches the live fire instant. See ADR-0137.
func (s *NativeScheduler) Location() *time.Location { return s.location() }
```
Change `Schedule()`'s compute line from:
```go
	next, ok := j.Trigger().Next(s.now())
```
to:
```go
	next, ok := j.Trigger().Next(s.now().In(s.location()))
```

- [ ] **Step 4: Run to verify green + full touched set**

Run: `go test -run '^TestNativeScheduler_LocationMethod$|^TestNativeScheduler_ScheduleReturnMatchesLocation$' ./scheduler/` (green)
Run: `go test ./scheduler/... ./runtime/...` (green — default path unchanged)
Run: `golangci-lint run ./scheduler/...` (clean)

- [ ] **Step 5: Commit** — WIP `feat(scheduler): expose Location() and resolve Schedule() NextRun in it (ADR-0137)`, same trailers.

---

### Task 3: Runtime threads the scheduler location into its `Trigger.Next` calls

The runtime learns the scheduler's location via an opt-in capability interface and passes `now.In(loc)` at **all three** of its `Trigger.Next` compute sites. Note (from the adversarial audit): only ONE site is load-bearing — `timerJobsFor` computes `timerJob.spec.NextRun`, which `jobStore.Save` persists. The other two are cosmetic wrappers (`buildTimerJob` on rehydration; the fresh-arm wrap in `processdriver.go`) whose `scheduledTimerJob.NextRun` only feeds `Activate` (which ignores it) and is overwritten by the live gocron value on any `Scheduled`/`List` read. All three are threaded so the fresh-arm wrapper is internally consistent with its own persisted descriptor.

**Files:**
- Modify: `runtime/timerops.go` (add `schedulingLocation()` helper + `locatedScheduler` interface; `timerJobsFor` uses `now.In(...)`; `buildTimerJob` uses `now.In(...)`)
- Modify: `runtime/processdriver.go:683` (the fresh-arm `newScheduledTimerJob(j, driver.clk.Now())` → `now.In(...)`)
- Unchanged: `runtime/timerjob.go:71` `newScheduledTimerJob` — it uses the `now` it is given; callers adjust the location
- Test: `runtime/timerops_location_test.go` (add the `schedulingLocation()` test — the guard re-point already happened in Task 1)

**Interfaces:**
- Consumes: `NativeScheduler.Location()` (Task 2) via the local capability interface.
- Produces: nothing exported.

- [ ] **Step 1: Write the failing test**

The ADR-0136 guard was already re-pointed in Task 1 (to `TestNewScheduledTimerJob_CalendarNextRunHonorsNowLocation`) — do NOT touch it here. This task adds a test proving `schedulingLocation()` reads the capability interface. Because it touches unexported symbols, keep `package runtime`.

Add a test for `schedulingLocation()` resolving from a scheduler that reports a location vs one that does not. The implementer should check `runtime/internal/runtimetest` for an existing double to give a `Location()` method; if none is reachable, write a tiny local fake in the test file that embeds `scheduler.Scheduler` (nil) and returns a fixed location:
```go
type locFake struct{ scheduler.Scheduler; loc *time.Location }
func (f locFake) Location() *time.Location { return f.loc }

func TestSchedulingLocation(t *testing.T) {
	plusThree := time.FixedZone("plusThree", 3*60*60)
	cases := []struct {
		name  string
		sched scheduler.Scheduler
		want  *time.Location
	}{
		{name: "capability reports zone", sched: locFake{loc: plusThree}, want: plusThree},
		{name: "no capability defaults UTC", sched: nil, want: time.UTC},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			d := &ProcessDriver{sched: c.sched}
			assert.Equal(t, c.want, d.schedulingLocation())
		})
	}
}
```
**Note for the implementer:** confirm `ProcessDriver`'s scheduler field is `sched scheduler.Scheduler` (`runtime/processdriver.go:41`). If constructing a bare `&ProcessDriver{sched: ...}` is insufficient because `schedulingLocation()` touches other fields, keep the helper dependent ONLY on `driver.sched` so the bare construction works. A nil `sched` must yield `time.UTC` (guard the type-assert).

- [ ] **Step 2: Run to verify red**

Run: `go test -run '^TestSchedulingLocation$' ./runtime/`
Expected: FAILS to compile — `schedulingLocation` undefined. That compile error is the red state.

- [ ] **Step 3: Implement**

In `runtime/timerops.go`, add the capability interface and helper (place near the top, after imports):
```go
// locatedScheduler is the opt-in capability a scheduler implements to report
// the timezone it resolves calendar/cron at-times in (see
// scheduler.NativeScheduler.Location). The runtime type-asserts its scheduler
// against this so the NextRun it computes and persists matches the live fire
// instant under a non-UTC scheduler location (ADR-0137). A scheduler that does
// not implement it (foreign doubles) is treated as UTC.
type locatedScheduler interface {
	Location() *time.Location
}

// schedulingLocation resolves the timezone the runtime should compute NextRun
// in: the scheduler's reported location, or time.UTC when the scheduler does
// not report one.
func (driver *ProcessDriver) schedulingLocation() *time.Location {
	if ls, ok := driver.sched.(locatedScheduler); ok {
		if loc := ls.Location(); loc != nil {
			return loc
		}
	}
	return time.UTC
}
```

In `timerJobsFor`, change:
```go
	now := driver.clk.Now()
```
to:
```go
	now := driver.clk.Now().In(driver.schedulingLocation())
```

In `buildTimerJob` (`runtime/timerops.go`), change:
```go
	return newScheduledTimerJob(j, driver.clk.Now()), nil
```
to:
```go
	return newScheduledTimerJob(j, driver.clk.Now().In(driver.schedulingLocation())), nil
```

In the fresh-arm loop (`runtime/processdriver.go`, ~line 683), change:
```go
				sj := newScheduledTimerJob(j, driver.clk.Now())
```
to:
```go
				sj := newScheduledTimerJob(j, driver.clk.Now().In(driver.schedulingLocation()))
```
(This wrapper's `NextRun` is cosmetic — the persisted value comes from `j.spec.NextRun` set by `timerJobsFor` — but threading it keeps the fresh-arm wrapper consistent with its own persisted descriptor and removes a latent trap.)

(No change to `newScheduledTimerJob` itself — it correctly resolves in the location of the `now` it is handed.)

- [ ] **Step 4: Run to verify green + full touched set**

Run: `go test -run '^TestSchedulingLocation$|^TestNewScheduledTimerJob_CalendarNextRunHonorsNowLocation$' ./runtime/` (green)
Run: `go test ./scheduler/... ./runtime/...` (green — default path unchanged; the owned/default scheduler reports UTC so `now.In(UTC)` preserves today's values)
Run: `golangci-lint run ./runtime/...` (clean)

- [ ] **Step 5: Commit** — WIP `feat(runtime): resolve timer NextRun in the scheduler's location (ADR-0137)`, same trailers.

---

### Task 4: End-to-end consistency test, godoc/CHANGELOG sweep, Delivery Gate

Prove the three surfaces agree end-to-end under a custom location, finish the docs, run full verification + the Delivery Gate, squash, merge.

**Files:**
- Test: `runtime/` — an integration-style test (reuse the runtime test harness / `processtest` if it already exercises a driver with a custom-location scheduler) asserting the persisted/reported `NextRun` for a calendar timer equals the live loc-resolved fire. If no lightweight harness reaches this, rely on the Task-2/3 unit coverage and state so.
- Modify: `CHANGELOG.md`
- Verify: godocs from Tasks 1–2 are complete and accurate.
- ADR `docs/adr/0137-location-aware-trigger-next.md` and spec `docs/specs/2026-07-24-location-aware-trigger-next.md` already exist.

**Interfaces:** none.

- [ ] **Step 1: End-to-end consistency check.** Read `runtime/`'s existing timer tests and `processtest` for a harness that constructs a `ProcessDriver` with a `WithScheduler(scheduler.NewScheduler(scheduler.WithLocation(loc)))`. If one exists, add a test: arm a `Daily(09:00)` timer, assert the persisted/reported `NextRun` == the scheduler's live `Scheduled().NextRun()` (both loc-resolved). If constructing that harness is heavy, document that Task 2 (`Schedule()`-return == `Scheduled()`) plus Task 3 (`schedulingLocation` + `now.In(loc)`) already cover the seam, and skip the redundant integration test rather than build a bespoke harness.

- [ ] **Step 2: CHANGELOG — SUPERSEDE the ADR-0136 clause, do not append a "refines yesterday" bullet.** Both ADR-0136 and ADR-0137 sit in the same **Unreleased** section (nothing tagged), so consumers should read ONE coherent net behavior, not a diff of two same-day intermediate states. The ADR-0136 bullet contains a now-false clause: *"Under a non-UTC location the trigger fires in that zone while `Trigger.Next` stays UTC — a reporting-only difference…"*. **Edit that ADR-0136 bullet in place** so its net story is: calendar/cron at-times resolve in UTC by default; `scheduler.WithLocation` opts into another zone; `scheduler.Trigger.Next` and the persisted/admin + `Schedule()`-return next-fire all resolve in the scheduler's configured location (new `scheduler.NativeScheduler.Location()`), matching the live `Scheduled()`/`List()` instant; default (UTC) unaffected; `Cron` under a non-IANA `time.FixedZone` cannot schedule on the live engine; and (honestly) `interval>1` calendar first-fire may still differ (separate pre-existing gap). Add `(ADR-0136, ADR-0137)` to the bullet's tag. Do NOT leave the "Trigger.Next stays UTC — reporting-only difference" wording anywhere.

- [ ] **Step 3: Godoc verification.** Confirm the Task-1 godoc edits on `Trigger.Next`/`Cron`/`Daily`/`Weekly`/`Monthly`/`ClockTime` are accurate and drop the "uniform UTC reference" language; confirm `NativeScheduler.Location()` godoc reads well. Fix any residual "uniform UTC" mentions in `scheduler/` godocs.

- [ ] **Step 4: Full verification.**
```bash
go build ./...
go test -race -coverprofile=cover.out ./... && go tool cover -func=cover.out | tail -1
TZ=Asia/Jakarta go test ./scheduler/... ./runtime/...   # non-UTC-host default-path safety
golangci-lint run ./...
```
Expected: green (including the non-UTC-host run); ≥85% on `scheduler`, `scheduler/internal/gocron`, `runtime`; new symbols (`Location`, `schedulingLocation`) and the `Trigger.Next`/`calendarNext` hot path covered; lint clean.

- [ ] **Step 5: Delivery Gate.** Final whole-branch review (most-capable model), then `/security-review`; fold all Critical/Important findings into the working tree (adjudicate false-positives explicitly). Minor findings → roll-up for triage.

- [ ] **Step 6: Squash + commit.** Squash the WIP per-task commits into ONE feature-bundle commit (message `feat(scheduler): location-aware Trigger.Next (ADR-0137)`, body summarizing the three seams + default-preserving + refines-ADR-0136, standard trailers), bundling ADR, spec, plan, CHANGELOG, tests.

- [ ] **Step 7: Merge.**
```bash
git fetch origin && git checkout main && git merge --no-ff --no-edit feat/location-aware-trigger-next && git push
```

---

## Self-Review

**Spec coverage:**
- Decision #1 (`Trigger.Next` resolves in `after.Location()`, calendar + cron) → Task 1. ✅
- Decision #2 (`NativeScheduler.Location()` + `Schedule()` uses it) → Task 2. ✅
- Decision #3 (runtime capability interface + `now.In(loc)` at both sites) → Task 3. ✅
- Default-UTC byte-identical invariant → per-task full-touched-set runs + the UTC-`after` rows kept in Task 1. ✅
- Residual caveats (cron FixedZone, DST, doubles, owned scheduler) → godocs (Task 1) + ADR/spec (already written). ✅
- CHANGELOG + ADR-0137 → Task 4 (+ existing ADR/spec). ✅
- Tests (trigger loc cases, Location(), Schedule()-return, runtime guard re-point + schedulingLocation, end-to-end) → Tasks 1–4. ✅

**Placeholder scan:** none. Task 3's local `locFake` and Task 4's end-to-end are described with a concrete fallback ("skip if harness heavy, unit coverage suffices"), not a TODO.

**Type consistency:** `Location() *time.Location` on `*NativeScheduler` (Task 2) is exactly what `locatedScheduler interface { Location() *time.Location }` (Task 3) type-asserts. `schedulingLocation() *time.Location` and `location()`/`Location()` all return non-nil. `Trigger.Next(after time.Time) (time.Time, bool)` unchanged. `newScheduledTimerJob(j *timerJob, now time.Time)` unchanged.

## Notes for the executor
- **The coupling is real but default-safe.** After Task 1, the runtime/scheduler still pass a plain clock `now`; because the test fake clocks are UTC-located and the default scheduler location is UTC, the default path stays UTC through every intermediate state. Run `./scheduler/... ./runtime/...` after EACH task to catch any non-UTC-clock test that asserted UTC.
- The ADR-0136 tests added last program are the main things that move: the cron-`Trigger.Next` regression case (Task 1) and the runtime `...CalendarNextRunIsUTC` guard (Task 3). Both are re-pointed, not deleted-without-replacement.
