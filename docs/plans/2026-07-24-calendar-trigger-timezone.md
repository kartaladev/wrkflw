# Calendar/Cron Trigger Timezone Resolution Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make recurring calendar (Daily/Weekly/Monthly) and cron triggers resolve their at-times in UTC by default on the live scheduler — matching the pure `Trigger.Next` reference — with a `scheduler.WithLocation(*time.Location)` opt-out for host-local or named-zone resolution.

**Architecture:** Three seams. (1) `scheduler.Trigger.Next` is made the *uniform* UTC reference by forcing `after.UTC()` in the cron branch (the calendar branch already does). (2) The internal gocron engine always pins `gocron.WithLocation(loc)`, resolving `loc` to `time.UTC` when unset — it never falls through to gocron's `time.Local` default. (3) A new façade option `scheduler.WithLocation` threads a `*time.Location` through `config` → a new internal `gocron.WithLocation` option → the engine. `nil` is ignored on both layers (resolves to UTC).

**Tech Stack:** Go 1.25, `go-co-op/gocron` v2.22.0 (pinned, ADR-0135), `jonboulle/clockwork` (fake clock), `robfig/cron/v3` (pure cron computation), testify.

## Global Constraints

- Go 1.25; `gocron` pinned to **v2.22.0** (ADR-0135) — do not change the version.
- Never import gocron/clockwork outside `scheduler/internal/gocron` and the façade wiring points that already do (`scheduler/scheduler.go`). The engine/runtime/definition code depends on the `scheduler.Trigger` / `scheduler.Scheduler` seams only.
- TDD strict (CLAUDE.md rule #6): write the failing test, run it red, then implement. Every new/changed symbol gets a visible red state.
- Black-box tests preferred (`package <pkg>_test`). Follow the existing test style in each package: `clockwork.NewFakeClockAt`, `sched.NewGocronScheduler(sched.WithClock(clk))`, `s.ScheduleJob(ctx, id, trig, fn, oneShot)`, `s.NextRun(id)`.
- Table tests: when ≥2 cases exercise the same call with varying inputs, use the project `table-test` skill's `assert` closure form (not `want`/`wantErr` fields), `t.Context()` over `context.Background()`.
- Error sentinels use the `workflow-<pkg>:` prefix convention (not adding any here, but honor it if one arises).
- Verification floor: `go test -race ./...` green, ≥85% line coverage on touched packages, `golangci-lint run ./...` clean. Hot paths (trigger resolution, arm/fire) covered first.
- This is a **breaking behavior change for non-UTC hosts** — ADR-0136 + CHANGELOG breaking-behavior entry are part of the deliverable bundle.

---

### Task 1: Make `Trigger.Next` uniformly UTC (cron branch)

Force `after.UTC()` in the cron branch of `Trigger.Next` so every recurring kind (calendar already; cron now) resolves against UTC. This makes the pure/persisted reference match the default (UTC) live path.

**Files:**
- Modify: `scheduler/trigger.go` (the `triggerCron` case in `Trigger.Next`, ~line 215-220; and the godoc on `Cron`/`Next`)
- Test: `scheduler/trigger_test.go` (add a cron-location regression case)

**Interfaces:**
- Consumes: nothing new.
- Produces: `Trigger.Next(after time.Time) (time.Time, bool)` — unchanged signature; cron branch now computes in UTC regardless of `after`'s location.

- [ ] **Step 1: Write the failing test**

Add to `scheduler/trigger_test.go` (black-box `scheduler_test`). The cron expression `"0 9 * * *"` means 09:00 daily. Passing `after` in a non-UTC zone must still yield 09:00 **UTC**, proving the branch no longer honors `after`'s location.

```go
func TestTriggerNext_CronResolvesInUTCRegardlessOfAfterLocation(t *testing.T) {
	// after is 2026-01-01 00:00:00 in UTC+2 == 2025-12-31 22:00:00 UTC.
	plusTwo := time.FixedZone("plusTwo", 2*60*60)
	after := time.Date(2026, 1, 1, 0, 0, 0, 0, plusTwo)

	trig := scheduler.Cron("0 9 * * *")
	got, ok := trig.Next(after)
	require.True(t, ok)

	// Uniform-UTC reference: next 09:00 is computed in UTC. From
	// 2025-12-31 22:00 UTC, the next 09:00 UTC is 2026-01-01 09:00 UTC.
	want := time.Date(2026, 1, 1, 9, 0, 0, 0, time.UTC)
	assert.True(t, got.Equal(want), "want %s, got %s", want, got)
	assert.Equal(t, time.UTC, got.Location())
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test -run '^TestTriggerNext_CronResolvesInUTCRegardlessOfAfterLocation$' ./scheduler/`
Expected: FAIL — current cron branch uses `after` as-is (UTC+2), so `got` is `2026-01-01 09:00 +02:00` (== `07:00 UTC`), not `09:00 UTC`; the `Equal` and `Location` assertions fail.

- [ ] **Step 3: Write minimal implementation**

In `scheduler/trigger.go`, change the `triggerCron` case of `Trigger.Next` from:

```go
	case triggerCron:
		sched, err := cron.ParseStandard(t.cron)
		if err != nil {
			return time.Time{}, false
		}
		return sched.Next(after), true
```

to:

```go
	case triggerCron:
		sched, err := cron.ParseStandard(t.cron)
		if err != nil {
			return time.Time{}, false
		}
		// Resolve in UTC so Next is the uniform UTC reference across all
		// recurring kinds (the calendar branch already normalizes to UTC in
		// calendarNext). This matches the default (UTC) live scheduler; see
		// docs/adr/0136-calendar-trigger-timezone.md.
		return sched.Next(after.UTC()), true
```

Update the `Next` godoc sentence that lists cron to note UTC resolution, and add to the `Cron` constructor godoc: `// [Trigger.Next] resolves the expression in UTC (the uniform reference for all recurring kinds).`

- [ ] **Step 4: Run test to verify it passes**

Run: `go test -run '^TestTriggerNext_CronResolvesInUTCRegardlessOfAfterLocation$' ./scheduler/`
Expected: PASS. Then `go test ./scheduler/` to confirm no calendar-`Next` regressions.

- [ ] **Step 5: Commit** — deferred. Per CLAUDE.md feature-bundle rule, do NOT commit per-task; the whole delivery lands in one commit at the end (Task 6). Stage nothing yet.

---

### Task 2: Internal gocron `WithLocation` option + default-UTC pin

Give the internal engine a `loc *time.Location` field and a `WithLocation` option, and make `NewGocronScheduler` **always** append `gocron.WithLocation(loc)`, defaulting `loc` to `time.UTC`.

**Files:**
- Modify: `scheduler/internal/gocron/scheduler.go` (`GocronScheduler` struct: add `loc`; add `WithLocation` option; `NewGocronScheduler`: resolve + append `gocron.WithLocation`)
- Modify: `scheduler/internal/gocron/trigger.go` (godoc on `Daily`/`Weekly`/`Monthly`: replace the "time.Local default / pre-existing discrepancy" note with the UTC-default statement)
- Test: `scheduler/internal/gocron/location_option_test.go` (new)

**Interfaces:**
- Consumes: `Trigger.Next` uniform-UTC (Task 1) — not directly called, but the contract the default aligns to.
- Produces:
  - `func WithLocation(loc *time.Location) Option` — nil ignored; sets `s.loc`.
  - `NewGocronScheduler` default resolves `loc == nil` → `time.UTC` and always pins `gocron.WithLocation(loc)`.

- [ ] **Step 1: Write the failing test**

Create `scheduler/internal/gocron/location_option_test.go` (black-box `gocron_test`, matching the existing `sched "…/scheduler/internal/gocron"` import alias used across the package's tests):

```go
package gocron_test

import (
	"context"
	"testing"
	"time"

	"github.com/jonboulle/clockwork"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	sched "github.com/kartaladev/wrkflw/scheduler/internal/gocron"
)

// A Daily job at 09:00 must resolve its at-time in the scheduler's configured
// location. Default (no WithLocation) == UTC; WithLocation(loc) == loc;
// WithLocation(nil) falls back to UTC (never gocron's time.Local default).
func TestGocronScheduler_WithLocation(t *testing.T) {
	// Fake "now" is 2026-01-01 00:00:00 UTC. The next 09:00 in a given zone,
	// expressed as an absolute instant, differs by that zone's offset.
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	plusThree := time.FixedZone("plusThree", 3*60*60)

	cases := []struct {
		name string
		opts []sched.Option
		// wantUTC is the expected NextRun expressed in UTC.
		wantUTC time.Time
	}{
		{
			name:    "default pins UTC",
			opts:    nil,
			wantUTC: time.Date(2026, 1, 1, 9, 0, 0, 0, time.UTC),
		},
		{
			name:    "WithLocation(nil) falls back to UTC",
			opts:    []sched.Option{sched.WithLocation(nil)},
			wantUTC: time.Date(2026, 1, 1, 9, 0, 0, 0, time.UTC),
		},
		{
			name:    "WithLocation(+3) resolves at-time in +3",
			opts:    []sched.Option{sched.WithLocation(plusThree)},
			// 09:00 at UTC+3 == 06:00 UTC.
			wantUTC: time.Date(2026, 1, 1, 6, 0, 0, 0, time.UTC),
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			clk := clockwork.NewFakeClockAt(start)
			opts := append([]sched.Option{sched.WithClock(clk)}, c.opts...)
			s, err := sched.NewGocronScheduler(opts...)
			require.NoError(t, err)
			t.Cleanup(func() { _ = s.Close() })

			_, err = s.ScheduleJob(t.Context(), "daily-9am",
				sched.Daily(1, sched.ClockTime{Hour: 9}),
				func(context.Context) error { return nil }, false)
			require.NoError(t, err)

			got, ok := s.NextRun("daily-9am")
			require.True(t, ok)
			assert.True(t, got.UTC().Equal(c.wantUTC),
				"want %s UTC, got %s", c.wantUTC, got.UTC())
		})
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test -run '^TestGocronScheduler_WithLocation$' ./scheduler/internal/gocron/`
Expected: FAIL to **compile** — `undefined: sched.WithLocation`. That compile failure is the red state.

- [ ] **Step 3: Write minimal implementation**

In `scheduler/internal/gocron/scheduler.go`:

Add a field to the `GocronScheduler` struct (next to `clk`):

```go
	// loc is the timezone the scheduler resolves calendar at-times and cron
	// expressions against. nil means "unset"; NewGocronScheduler resolves an
	// unset loc to time.UTC (it never falls through to gocron's time.Local
	// default). Set via WithLocation. See ADR-0136.
	loc *time.Location
```

Add the option (place it after `WithClock`):

```go
// WithLocation sets the timezone in which the scheduler resolves calendar
// at-times (Daily/Weekly/Monthly) and cron expressions. Default: [time.UTC],
// which matches the pure scheduler.Trigger.Next reference. A nil value is
// ignored (the default UTC is used). The nil-guard matters for two reasons:
// (1) gocron's own default when no location is pinned is time.Local, so an
// unset location must be resolved to UTC before construction; and (2)
// gocron.WithLocation(nil) returns ErrWithLocationNil and would fail scheduler
// construction, so nil must never be forwarded. Pass time.Local for host-local
// resolution, or any named zone. Named zones with DST resolve at-times per that
// zone's DST rules on the live scheduler; the UTC reference does not observe
// DST, so the two diverge across DST boundaries. In a multi-replica deployment
// (WithLocker/WithElector) every replica must use the same location. See
// ADR-0136.
func WithLocation(loc *time.Location) Option {
	return func(s *GocronScheduler) {
		if loc != nil {
			s.loc = loc
		}
	}
}
```

In `NewGocronScheduler`, after the clock is resolved (`if s.clk == nil { s.clk = clockwork.NewRealClock() }`) and before building `gocronOpts`, resolve the location; then add it as the first gocron option:

```go
	// Resolve the effective location: option-provided or UTC default. This is
	// pinned explicitly so gocron never falls back to its own time.Local
	// default (ADR-0136).
	loc := s.loc
	if loc == nil {
		loc = time.UTC
	}
```

and prepend `gocron.WithLocation(loc)` to the **existing** `gocronOpts` slice
literal — do NOT replace it. The real literal already contains `WithClock`,
`WithMonitorStatus`, and the full `WithGlobalJobOptions(WithEventListeners(...))`
block, and the code conditionally appends `WithDistributedLocker` /
`WithDistributedElector` afterward. Add only the one line as the first element:

```go
	gocronOpts := []gocron.SchedulerOption{
		gocron.WithLocation(loc), // ADR-0136: pin location (default UTC)
		gocron.WithClock(s.clk),
		gocron.WithMonitorStatus(newMonitorStatus(s.tel)),
		// … existing WithGlobalJobOptions block and conditional
		// locker/elector appends remain exactly as they are …
	}
```

In `scheduler/internal/gocron/trigger.go`, update the `Daily`/`Weekly`/`Monthly` godocs: replace the "resolved in the live scheduler's location (time.Local by default)" wording with: `// The live scheduler resolves these in its configured location — time.UTC by default, or the zone set via WithLocation (ADR-0136).`

- [ ] **Step 4: Run test to verify it passes**

Run: `go test -run '^TestGocronScheduler_WithLocation$' ./scheduler/internal/gocron/`
Expected: PASS (all three subtests). Then `go test ./scheduler/internal/gocron/` — the existing `trigger_test.go` calendar cases stay green because they **self-advance the fake clock to the live `NextRun`** (`clk.Advance(next.Sub(clk.Now())…)`) and assert `fired`, which is location-agnostic — not because they assert UTC day/hour. Two guard cases (`Weekly (empty-weekdays guard)`, `Monthly (empty-days guard)`) assert `.Weekday()`/`.Day()`, which are location-sensitive only at extreme offsets; verify they still pass and note any assertion moved to UTC in the commit body.

- [ ] **Step 5: Commit** — deferred (feature-bundle; see Task 6).

---

### Task 3: Façade `scheduler.WithLocation` option

Expose `scheduler.WithLocation(*time.Location)` on the public constructor and thread it to the internal engine option.

**Files:**
- Modify: `scheduler/scheduler.go` (`config` struct: add `loc`; add `WithLocation` option after `WithClock`; `internalOpts()`: append `gocronsched.WithLocation`)
- Test: `scheduler/location_option_test.go` (new)

**Interfaces:**
- Consumes: `gocronsched.WithLocation(loc *time.Location) Option` (Task 2).
- Produces: `func WithLocation(loc *time.Location) Option` on package `scheduler` — nil ignored; stored in `config.loc`; threaded via `internalOpts`.

- [ ] **Step 1: Write the failing test**

Create `scheduler/location_option_test.go` (black-box `scheduler_test`). This is an end-to-end assertion through the public façade, mirroring Task 2 but via `scheduler.NewScheduler` + `scheduler.WithLocation`:

```go
package scheduler_test

import (
	"context"
	"testing"
	"time"

	"github.com/jonboulle/clockwork"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kartaladev/wrkflw/scheduler"
)

func TestNativeScheduler_WithLocation(t *testing.T) {
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	plusThree := time.FixedZone("plusThree", 3*60*60)

	// table-test skill (MANDATORY): per-case `assert` closure, not want/wantErr fields.
	cases := []struct {
		name   string
		opts   []scheduler.Option
		assert func(t *testing.T, got time.Time)
	}{
		{
			name: "default pins UTC",
			opts: nil,
			assert: func(t *testing.T, got time.Time) {
				assert.True(t, got.Equal(time.Date(2026, 1, 1, 9, 0, 0, 0, time.UTC)),
					"want 09:00 UTC, got %s", got)
			},
		},
		{
			name: "WithLocation(+3) resolves at-time in +3",
			opts: []scheduler.Option{scheduler.WithLocation(plusThree)},
			assert: func(t *testing.T, got time.Time) {
				// 09:00 at UTC+3 == 06:00 UTC.
				assert.True(t, got.Equal(time.Date(2026, 1, 1, 6, 0, 0, 0, time.UTC)),
					"want 06:00 UTC, got %s", got)
			},
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			clk := clockwork.NewFakeClockAt(start)
			opts := append([]scheduler.Option{scheduler.WithClock(clk)}, c.opts...)
			s, err := scheduler.NewScheduler(opts...)
			require.NoError(t, err)
			t.Cleanup(func() { _ = s.Close() })

			// Use the same job-construction the existing calendar test uses.
			// mustJob(t, id, kind, trig, fn) is the helper in scheduler_test.go;
			// if you construct directly, it is NewJobWithID(id, kind, trig, fn,
			// data, opts...) — NOT NewJob, which has no id parameter.
			job := mustJob(t, "daily-9am", timerJobKindForTest,
				scheduler.Daily(1, scheduler.ClockTime{Hour: 9}),
				func(context.Context) error { return nil })
			_, err = s.Schedule(t.Context(), job)
			require.NoError(t, err)

			// Scheduled(ctx, id) (ScheduledJob, error) re-fetches gocron's LIVE
			// NextRun, which respects WithLocation — so the +3 case reads the
			// loc-resolved (correct) instant, i.e. 06:00 UTC. (Schedule()'s own
			// return value, by contrast, is the Trigger.Next UTC reference.)
			sj, err := s.Scheduled(t.Context(), "daily-9am")
			require.NoError(t, err)
			c.assert(t, sj.NextRun().UTC())
		})
	}
}
```

**Note for the implementer:** read `scheduler/scheduler_test.go` first and mirror
`TestNativeSchedulerCalendarTriggers`'s setup exactly — reuse its `mustJob`
helper and the `JobKind` it uses (shown here as `timerJobKindForTest`; replace
with the real one). The existing calendar test does **not** call `Start` before
`Schedule`/`Scheduled` (the first `Schedule` auto-starts), so this test omits it
too. The façade `NextRun` source is already resolved — no fallback needed:
`Scheduled`/`List` surface gocron's location-resolved `NextRun`, so the `+3`
case asserts `06:00 UTC` directly.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test -run '^TestNativeScheduler_WithLocation$' ./scheduler/`
Expected: FAIL to compile — `undefined: scheduler.WithLocation`.

- [ ] **Step 3: Write minimal implementation**

In `scheduler/scheduler.go`:

Add to the `config` struct:

```go
	loc *time.Location // nil = UTC default (resolved in the internal engine)
```

Add the option after `WithClock`:

```go
// WithLocation sets the timezone in which the scheduler resolves recurring
// calendar at-times (Daily/Weekly/Monthly) and cron expressions. Default:
// [time.UTC], matching the [Trigger.Next] reference. A nil value is ignored
// (UTC is used). Pass [time.Local] for host-local resolution, or any named
// zone.
//
// Firing and rehydration are correct under any location. A non-UTC location
// only perturbs how the next-run instant is REPORTED, and the surfaces differ:
// the persisted/admin next-fire and Schedule()'s return value use the UTC
// [Trigger.Next] reference, while Scheduled/List re-fetch the live,
// location-resolved next-run. This never affects when a timer fires (recurring
// timers re-arm from the trigger, not from the reported next-run).
//
// Named zones with DST resolve at-times per that zone's DST rules on the live
// scheduler; the UTC reference does not observe DST, so the two diverge across
// DST boundaries. In a multi-replica deployment (see [WithLocker] /
// [WithElector]) every replica MUST use the same location. See ADR-0136.
func WithLocation(loc *time.Location) Option {
	return func(c *config) {
		if loc != nil {
			c.loc = loc
		}
	}
}
```

In `internalOpts()`, append the threaded option (after the `WithClock` line, before the `logger` block):

```go
	if s.cfg.loc != nil {
		opts = append(opts, gocronsched.WithLocation(s.cfg.loc))
	}
```

(The internal engine defaults an unset location to UTC, so the façade only forwards a non-nil override — consistent with how `timeSkew`/`logger` are threaded.)

- [ ] **Step 4: Run test to verify it passes**

Run: `go test -run '^TestNativeScheduler_WithLocation$' ./scheduler/`
Expected: PASS. Then `go test ./scheduler/`.

- [ ] **Step 5: Commit** — deferred (feature-bundle; see Task 6).

---

### Task 4: Update the calendar-triggers integration test to the UTC default

`TestNativeSchedulerCalendarTriggers` currently documents the `time.Local` behavior. Re-point its assertions to the new UTC default.

**COUPLING:** Task 4 must land *together with* Task 2. The moment Task 2's default-UTC pin lands, this test — whose expectations are built in `time.Local` — fails on any non-UTC host (CI running `TZ=UTC` masks it). Do not defer Task 4's rewrite to Task 6's verification; complete it as soon as Task 2 changes the default.

**Files:**
- Modify: `scheduler/scheduler_test.go` (`TestNativeSchedulerCalendarTriggers`)

**Interfaces:**
- Consumes: the UTC-default behavior from Tasks 2–3.
- Produces: nothing (test-only).

- [ ] **Step 1: Read and identify the local-time assertions**

Run: `go test -run '^TestNativeSchedulerCalendarTriggers$' ./scheduler/ -v`
Read the test body. Identify every assertion that computes an expected instant via `time.Local` (or that comments "documents actual (time.Local) behavior").

- [ ] **Step 2: Run to confirm current pass, then rewrite assertions to UTC**

Move **both** the reference instant and the expected instant to `time.UTC` —
not only the expected one. In this test (`scheduler_test.go:63-64`), `refTime`
and `wantFire` are both built in `time.Local`:

```go
refTime  := time.Date(2026, time.January, 1, 8, 0, 0, 0, time.Local)
wantFire := time.Date(2026, time.January, 1, 9, 0, 0, 0, time.Local)
```

`refTime` seeds the fake clock (`NewFakeClockAt(refTime)`) **and** the advance
delta (`fakeClock.Advance(wantFire.Sub(fakeClock.Now())…)`). If only `wantFire`
moves to UTC while `refTime` stays `time.Local`, the fake-clock start and the
day-boundary math diverge on a non-UTC host and the advance can cross a day
boundary → flaky/failing. Change **both** to `time.UTC`:

```go
refTime  := time.Date(2026, time.January, 1, 8, 0, 0, 0, time.UTC)
wantFire := time.Date(2026, time.January, 1, 9, 0, 0, 0, time.UTC) // UTC default since ADR-0136
```

Assert with `got.UTC().Equal(wantFire)` if the assertion compares instants. On a
non-UTC host this is the red→green for the behavior change; on a `TZ=UTC` host
the expectation is now host-independent and stays green.

- [ ] **Step 3: Run test to verify it passes**

Run: `go test -run '^TestNativeSchedulerCalendarTriggers$' ./scheduler/`
Expected: PASS on any host (UTC and non-UTC), because the expectation is now host-independent (UTC).

- [ ] **Step 4: Commit** — deferred (feature-bundle; see Task 6).

---

### Task 5: Runtime NextRun characterization guard (NOT a red-green cycle)

**This task is a regression/characterization guard, not a TDD red-green cycle.**
`calendarNext` has always normalized to UTC, so `newScheduledTimerJob`'s
`NextRun` for a calendar trigger was already UTC before this change — there is
no observable red state, and per CLAUDE.md's TDD rules ("pure refactor with no
behavioural change ⇒ no new test needed, but existing tests must still pass")
that is acceptable *as a characterization test*. Its purpose is to lock the UTC
reference so a future location-aware `Trigger.Next` (spec Option 3) cannot
silently break the default. Do not present it as a red→green cycle in the
commit/audit trail.

It documents that the default-UTC persisted/reported `NextRun` for a calendar
trigger agrees with the default live fire, and that recurring re-arm is
trigger-driven (not `NextRun`-driven).

**Files:**
- Test: `runtime/timerops_location_test.go` (new) — OR extend an existing runtime timer test if one already exercises calendar arming (check `runtime/timerops_test.go` first).

**Interfaces:**
- Consumes: `scheduler.Daily`, the runtime arm path (`timerJobsFor`/`newScheduledTimerJob`).
- Produces: nothing (test-only).

- [ ] **Step 1: Read the runtime arm-path tests**

Run: `ls runtime/*_test.go && grep -rn "Daily\|newScheduledTimerJob\|timerJobsFor\|NextRun" runtime/*_test.go`
Identify the lightest existing harness that arms a calendar timer and lets you read the resulting `scheduledTimerJob.NextRun()`. Reuse it — do not build a new ProcessDriver from scratch if a helper exists.

- [ ] **Step 2: Write the failing test**

Assert that for a `Daily(1, 09:00)` trigger armed at a known `now`, `newScheduledTimerJob`'s `NextRun()` is the UTC 09:00 instant (the reference), which — post-Tasks 2–3 — equals what the default live scheduler will fire. Because `newScheduledTimerJob` is unexported, place this test in `package runtime` (white-box) in `runtime/timerops_location_test.go`:

```go
package runtime

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/kartaladev/wrkflw/scheduler"
)

// The reported NextRun for a calendar trigger is the UTC reference, matching
// the default (UTC) live scheduler (ADR-0136).
func TestNewScheduledTimerJob_CalendarNextRunIsUTC(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.FixedZone("plusTwo", 2*60*60))
	j := &timerJob{trig: scheduler.Daily(1, scheduler.ClockTime{Hour: 9})}

	sj := newScheduledTimerJob(j, now)

	want := time.Date(2026, 1, 1, 9, 0, 0, 0, time.UTC)
	assert.True(t, sj.NextRun().UTC().Equal(want),
		"want %s UTC, got %s", want, sj.NextRun().UTC())
}
```

**Note:** confirm `timerJob`'s field name for the trigger is `trig` (per `runtime/timerjob.go`) and that constructing it with only `trig` set is sufficient for `newScheduledTimerJob` (it reads `j.trig` only). If `newScheduledTimerJob` touches other fields, populate them minimally.

- [ ] **Step 3: Run the characterization test (expected PASS immediately)**

Run: `go test -run '^TestNewScheduledTimerJob_CalendarNextRunIsUTC$' ./runtime/`
Expected: PASS immediately — `calendarNext` forced UTC even before this change, so this is a characterization/regression guard, not a red→green cycle (see the task header). If it FAILS, the `timerJob` construction is wrong (not the assertion) — fix the wiring.

- [ ] **Step 4: Commit** — deferred (feature-bundle; see Task 6).

---

### Task 6: ADR-0136, CHANGELOG, doc sweep, and the single feature-bundle commit

Write the ADR, the breaking-behavior CHANGELOG entry, finish the godoc sweep, run the full verification, then land everything in ONE commit.

**Files:**
- Create: `docs/adr/0136-calendar-trigger-timezone.md` (Nygard template)
- Modify: `CHANGELOG.md` (breaking-behavior entry under the unreleased/v0.1.0 section)
- Verify/finish: godoc edits from Tasks 1–2 (both `scheduler/trigger.go` and `scheduler/internal/gocron/trigger.go`), and the `ClockTime` godoc in `scheduler/trigger.go` (drop the "pre-existing discrepancy" language, state UTC default + `WithLocation`)
- The spec `docs/specs/2026-07-24-calendar-trigger-timezone-followup.md` is already updated (Decided).

**Interfaces:** none (docs + commit).

- [ ] **Step 1: Write ADR-0136** using the Nygard template (Status/Date, Context, Decision, Consequences). Status: Accepted, Date: 2026-07-24. Context: the UTC/local split (calendar UTC-reference vs live time.Local; cron's incidental agreement). Decision: pin `gocron.WithLocation`, default UTC; add `scheduler.WithLocation`; make `Trigger.Next` uniformly UTC (cron branch). Consequences: breaking for non-UTC hosts; UTC hosts unaffected; residual custom-location cosmetic `NextRun` mismatch (display/ops-stats only, recurring re-arm unaffected); future-work hook to make `Trigger.Next` location-aware. Cross-reference the spec.

- [ ] **Step 2: Update `ClockTime` godoc** in `scheduler/trigger.go` to: `// ClockTime is a wall-clock time-of-day … [Trigger.Next] resolves it in UTC, and the live scheduler resolves it in UTC by default (override with scheduler.WithLocation). See ADR-0136.` Remove the old "live scheduler resolves in time.Local" sentence.

- [ ] **Step 3: Add the CHANGELOG breaking-behavior entry.** Under the appropriate section, e.g.:

```markdown
### Changed (breaking behavior)
- Recurring **calendar** (`Daily`/`Weekly`/`Monthly`) and **cron** triggers now
  resolve their at-times in **UTC by default** on the live scheduler, matching
  `scheduler.Trigger.Next`. Previously the live scheduler used the host's
  `time.Local`. Deployments running `TZ=UTC` (typical containers) are
  unaffected. Non-UTC hosts that intend host-local resolution must now pass
  `scheduler.WithLocation(time.Local)` (or any named `*time.Location`). See
  ADR-0136.
```

- [ ] **Step 4: Full verification.**

Run: `go build ./...`
Run: `go test -race -coverprofile=cover.out ./... && go tool cover -func=cover.out | tail -1`
Expected: all green; coverage ≥85% on `scheduler`, `scheduler/internal/gocron`, `runtime` (touched packages), with the trigger-resolution and arm paths covered.
Run: `golangci-lint run ./...`
Expected: clean.

- [ ] **Step 5: Delivery Gate — `/code-review` then `/security-review`** on the pending change; fix ALL findings by folding into the working tree (no separate commits yet). Adjudicate any false-positives explicitly.

- [ ] **Step 6: Single feature-bundle commit.**

```bash
git add -A
git commit -m "$(cat <<'EOF'
feat(scheduler): default-UTC calendar/cron trigger resolution (ADR-0136)

Pin gocron.WithLocation(time.UTC) by default so recurring calendar and cron
triggers resolve at-times in UTC on the live scheduler, matching the pure
Trigger.Next reference. Add scheduler.WithLocation(*time.Location) to opt into
time.Local or a named zone; make Trigger.Next uniformly UTC (cron branch).
Breaking behavior for non-UTC hosts only. Bundles ADR-0136, spec, plan,
CHANGELOG, tests.

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_016FzuYkwCwdXJ9VWchXAcB4
EOF
)"
```

- [ ] **Step 7: Merge to main.**

```bash
git fetch origin && git merge --no-edit origin/main   # reconcile any advance
git checkout main && git merge --no-ff -
git push
```

(Run on a feature branch created before Task 1; see Execution Handoff.)

---

## Self-Review

**Spec coverage:**
- Decision #1 (pin UTC default, never fall to time.Local) → Task 2. ✅
- Decision #2 (`scheduler.WithLocation`, nil-guarded) → Task 3 (façade) + Task 2 (internal option). ✅
- Decision #3 (`Trigger.Next` uniformly UTC, cron branch) → Task 1. ✅
- Cron in scope (global gocron location) → Tasks 1–2. ✅
- Consistency contract / cosmetic-only custom-location caveat → Task 5 (runtime NextRun guard) + godoc in Task 3/6. ✅
- Breaking-behavior for non-UTC hosts, ADR + CHANGELOG → Task 6. ✅
- Testing plan (gocron default/loc/nil, façade thread, Trigger cron regression, integration update, runtime consistency) → Tasks 1–5. ✅
- Doc sweep (both trigger.go godocs, ClockTime) → Tasks 1, 2, 6. ✅

**Placeholder scan:** No "TBD/TODO". Task 3 flags a genuine API-shape uncertainty (does the façade surface a gocron-computed or Trigger.Next-computed NextRun for calendar jobs?) with a concrete fallback assertion, not a placeholder — the implementer resolves it by reading `scheduler_test.go`, and both branches are fully specified.

**Type consistency:** `WithLocation(loc *time.Location) Option` — identical signature at both `scheduler` and `scheduler/internal/gocron` layers. `config.loc` / `GocronScheduler.loc` both `*time.Location`. `Trigger.Next(after time.Time) (time.Time, bool)` unchanged. `newScheduledTimerJob(j *timerJob, now time.Time) *scheduledTimerJob` and field `trig` match `runtime/timerjob.go`.

## Notes on residual risk (for the executor)
- **Façade NextRun source is RESOLVED (adversarial audit, verified against source).** `scheduler.NativeScheduler.Scheduled(ctx, id)` / `List` re-fetch gocron's live, location-resolved `NextRun` (`scheduler/scheduler.go:658`, respecting `WithLocation`); `Schedule()`'s return value uses the UTC `Trigger.Next` (`:523`). Task 3 asserts through `Scheduled`, so the `+3` case reads `06:00 UTC` directly — no fallback needed.
- **Linchpin CONFIRMED (verified against gocron v2.22.0 source).** `gocron.WithLocation` governs `CronJob` (via a `CRON_TZ=` prefix) as well as `DailyJob`/`WeeklyJob`/`MonthlyJob`; there is no per-`CronJob` location, so cron cannot be exempted. `At`/`After`/`Every`/`EveryRandom` are absolute/duration and location-invariant — correctly out of scope. gocron's no-pin default is `time.Local`, and `gocron.WithLocation(nil)` returns `ErrWithLocationNil` (so nil must be resolved to UTC on our side, never forwarded).
- **Test breakage inventory.** After Task 2, re-run `./scheduler/...` and `./runtime/...`. `TestNativeSchedulerCalendarTriggers` (`scheduler/scheduler_test.go`) needs both `refTime` and `wantFire` moved to UTC (Task 4). The `scheduler/internal/gocron/trigger_test.go` calendar cases stay green (they self-advance to the live `NextRun`); watch the two `.Weekday()`/`.Day()` guard cases. `runtime/timerops_convert_test.go` asserts conversion equivalence (not fire instants) and is unaffected. Note any moved assertion in the commit body.
- **Optional (nice-to-have) DST test.** A single `scheduler/internal/gocron` test asserting a `Daily` fire under `WithLocation(<a DST zone>)` across a spring-forward boundary would document the live-path DST behavior called out in the spec/ADR. Not required for delivery; add if cheap.
