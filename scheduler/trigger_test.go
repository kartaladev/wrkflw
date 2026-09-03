package scheduler_test

import (
	"math"
	"slices"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kartaladev/wrkflw/scheduler"
)

// TestTrigger_Next covers Next(after) for every constructor, per the locked
// semantics: it computes the first fire strictly after the given instant.
func TestTrigger_Next(t *testing.T) {
	t.Parallel()

	after := time.Date(2026, 7, 22, 8, 0, 0, 0, time.UTC) // a Wednesday

	type testCase struct {
		name   string
		after  time.Time // optional per-case reference instant; zero uses the default `after` above
		trig   scheduler.Trigger
		assert func(t *testing.T, next time.Time, ok bool)
	}

	tests := []testCase{
		{
			name: "at future returns the instant",
			trig: scheduler.At(after.Add(time.Hour)),
			assert: func(t *testing.T, next time.Time, ok bool) {
				if !ok || !next.Equal(after.Add(time.Hour)) {
					t.Fatalf("next=%v ok=%v", next, ok)
				}
			},
		},
		{
			name: "at past fires immediately: already-due one-shot returns the past instant",
			trig: scheduler.At(after.Add(-time.Hour)),
			assert: func(t *testing.T, next time.Time, ok bool) {
				want := after.Add(-time.Hour)
				if !ok || !next.Equal(want) {
					t.Fatalf("next=%v ok=%v want %v,true", next, ok, want)
				}
			},
		},
		{
			name: "at zero time reports no future fire (zero-Trigger misuse rule)",
			trig: scheduler.At(time.Time{}),
			assert: func(t *testing.T, next time.Time, ok bool) {
				if ok {
					t.Fatalf("want ok=false, got next=%v", next)
				}
			},
		},
		{
			name: "after fires at after+duration",
			trig: scheduler.After(30 * time.Minute),
			assert: func(t *testing.T, next time.Time, ok bool) {
				want := after.Add(30 * time.Minute)
				if !ok || !next.Equal(want) {
					t.Fatalf("next=%v ok=%v want %v", next, ok, want)
				}
			},
		},
		{
			name: "after zero duration fires immediately at after",
			trig: scheduler.After(0),
			assert: func(t *testing.T, next time.Time, ok bool) {
				if !ok || !next.Equal(after) {
					t.Fatalf("next=%v ok=%v want %v,true", next, ok, after)
				}
			},
		},
		{
			name: "after negative duration fires immediately: already-due one-shot",
			trig: scheduler.After(-time.Hour),
			assert: func(t *testing.T, next time.Time, ok bool) {
				want := after.Add(-time.Hour)
				if !ok || !next.Equal(want) {
					t.Fatalf("next=%v ok=%v want %v,true", next, ok, want)
				}
			},
		},
		{
			name: "every fires at after+duration",
			trig: scheduler.Every(2 * time.Hour),
			assert: func(t *testing.T, next time.Time, ok bool) {
				want := after.Add(2 * time.Hour)
				if !ok || !next.Equal(want) {
					t.Fatalf("next=%v ok=%v want %v", next, ok, want)
				}
			},
		},
		{
			name: "every zero duration reports no sane future fire",
			trig: scheduler.Every(0),
			assert: func(t *testing.T, next time.Time, ok bool) {
				if ok {
					t.Fatalf("want ok=false, got next=%v", next)
				}
			},
		},
		{
			name: "every negative duration reports no sane future fire",
			trig: scheduler.Every(-time.Minute),
			assert: func(t *testing.T, next time.Time, ok bool) {
				if ok {
					t.Fatalf("want ok=false, got next=%v", next)
				}
			},
		},
		{
			name: "every random reports the earliest bound after+min",
			trig: scheduler.EveryRandom(5*time.Minute, time.Hour),
			assert: func(t *testing.T, next time.Time, ok bool) {
				want := after.Add(5 * time.Minute)
				if !ok || !next.Equal(want) {
					t.Fatalf("next=%v ok=%v want %v", next, ok, want)
				}
			},
		},
		{
			name: "every random zero min reports no sane future fire",
			trig: scheduler.EveryRandom(0, time.Minute),
			assert: func(t *testing.T, next time.Time, ok bool) {
				if ok {
					t.Fatalf("want ok=false, got next=%v", next)
				}
			},
		},
		{
			name: "cron weekday morning",
			trig: scheduler.Cron("0 9 * * 1-5"),
			assert: func(t *testing.T, next time.Time, ok bool) {
				want := time.Date(2026, 7, 22, 9, 0, 0, 0, time.UTC)
				if !ok || !next.Equal(want) {
					t.Fatalf("next=%v ok=%v want %v", next, ok, want)
				}
			},
		},
		{
			name: "cron unparseable expression reports ok=false",
			trig: scheduler.Cron("not a cron expression"),
			assert: func(t *testing.T, next time.Time, ok bool) {
				if ok {
					t.Fatalf("want ok=false, got next=%v", next)
				}
			},
		},
		{
			name:  "cron resolves in after's location",
			after: time.Date(2026, 1, 1, 0, 0, 0, 0, time.FixedZone("plusTwo", 2*60*60)),
			trig:  scheduler.Cron("0 9 * * *"),
			assert: func(t *testing.T, next time.Time, ok bool) {
				// after is 2026-01-01 00:00 +02:00; the next 09:00 is resolved in
				// that same +02:00 zone, i.e. 2026-01-01 09:00 +02:00.
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
		{
			name: "daily fires at the next matching clock time same day",
			trig: scheduler.Daily(1, scheduler.ClockTime{Hour: 9}),
			assert: func(t *testing.T, next time.Time, ok bool) {
				want := time.Date(2026, 7, 22, 9, 0, 0, 0, time.UTC)
				if !ok || !next.Equal(want) {
					t.Fatalf("next=%v ok=%v want %v", next, ok, want)
				}
			},
		},
		{
			name: "weekly fires on the next matching weekday",
			trig: scheduler.Weekly(1, []time.Weekday{time.Monday}, scheduler.ClockTime{Hour: 9}),
			assert: func(t *testing.T, next time.Time, ok bool) {
				// after is a Wednesday; the prior Monday already passed, so the
				// next Monday occurrence is a full week out.
				want := time.Date(2026, 7, 27, 9, 0, 0, 0, time.UTC)
				if !ok || !next.Equal(want) {
					t.Fatalf("next=%v ok=%v want %v", next, ok, want)
				}
			},
		},
		{
			name: "monthly wraps into the next month once the day-of-month has passed",
			trig: scheduler.Monthly(1, []int{15}, scheduler.ClockTime{Hour: 9}),
			assert: func(t *testing.T, next time.Time, ok bool) {
				// the 15th of July has already passed relative to after (July 22).
				want := time.Date(2026, 8, 15, 9, 0, 0, 0, time.UTC)
				if !ok || !next.Equal(want) {
					t.Fatalf("next=%v ok=%v want %v", next, ok, want)
				}
			},
		},
		{
			name: "daily with no at-times defaults to midnight",
			trig: scheduler.Daily(1),
			assert: func(t *testing.T, next time.Time, ok bool) {
				// after is July 22 08:00; the same-day midnight has already
				// passed, so the next occurrence is the following midnight.
				want := time.Date(2026, 7, 23, 0, 0, 0, 0, time.UTC)
				if !ok || !next.Equal(want) {
					t.Fatalf("next=%v ok=%v want %v", next, ok, want)
				}
			},
		},
		{
			name: "weekly with no at-times defaults to midnight",
			trig: scheduler.Weekly(1, []time.Weekday{time.Thursday}),
			assert: func(t *testing.T, next time.Time, ok bool) {
				// after is a Wednesday; the next Thursday midnight is the
				// following day.
				want := time.Date(2026, 7, 23, 0, 0, 0, 0, time.UTC)
				if !ok || !next.Equal(want) {
					t.Fatalf("next=%v ok=%v want %v", next, ok, want)
				}
			},
		},
		{
			name: "monthly with no at-times defaults to midnight",
			trig: scheduler.Monthly(1, []int{23}),
			assert: func(t *testing.T, next time.Time, ok bool) {
				// after is July 22; the 23rd's midnight is the next day.
				want := time.Date(2026, 7, 23, 0, 0, 0, 0, time.UTC)
				if !ok || !next.Equal(want) {
					t.Fatalf("next=%v ok=%v want %v", next, ok, want)
				}
			},
		},
		{
			// This test used to assert ok=false. The gocron
			// adapter substitutes Sunday for an empty weekday set before
			// arming, so the trigger does fire and Next now says so.
			name: "weekly with no weekdays takes the substituted Sunday",
			trig: scheduler.Weekly(1, nil, scheduler.ClockTime{Hour: 9}),
			assert: func(t *testing.T, next time.Time, ok bool) {
				// after is Wednesday 2026-07-22; the coming Sunday is the 26th.
				want := time.Date(2026, 7, 26, 9, 0, 0, 0, time.UTC)
				if !ok || !next.Equal(want) {
					t.Fatalf("next=%v ok=%v want %v", next, ok, want)
				}
			},
		},
		{
			// This test used to assert ok=false. The same
			// adapter substitutes the 1st for an empty day-of-month set.
			name: "monthly with no days-of-month takes the substituted first of the month",
			trig: scheduler.Monthly(1, nil, scheduler.ClockTime{Hour: 9}),
			assert: func(t *testing.T, next time.Time, ok bool) {
				// after is 2026-07-22; July's 1st has passed.
				want := time.Date(2026, 8, 1, 9, 0, 0, 0, time.UTC)
				if !ok || !next.Equal(want) {
					t.Fatalf("next=%v ok=%v want %v", next, ok, want)
				}
			},
		},
		{
			name: "zero trigger reports no future fire",
			trig: scheduler.Trigger{},
			assert: func(t *testing.T, next time.Time, ok bool) {
				if ok || !next.IsZero() {
					t.Fatalf("want next=zero ok=false, got next=%v ok=%v", next, ok)
				}
			},
		},
		{
			name:  "daily interval>1 jumps by interval once the current day is past",
			after: time.Date(2026, 7, 10, 10, 0, 0, 0, time.UTC), // past the day's 09:00
			trig:  scheduler.Daily(2, scheduler.ClockTime{Hour: 9}),
			assert: func(t *testing.T, next time.Time, ok bool) {
				// matches the live gocron first fire, not the interval-blind
				// "next matching day" (which would be 07-11).
				want := time.Date(2026, 7, 12, 9, 0, 0, 0, time.UTC)
				if !ok || !next.Equal(want) {
					t.Fatalf("next=%v ok=%v want %v", next, ok, want)
				}
			},
		},
		{
			name:  "daily interval>1 same-period fire is unchanged (regression guard)",
			after: time.Date(2026, 7, 10, 8, 0, 0, 0, time.UTC), // before the day's 09:00
			trig:  scheduler.Daily(2, scheduler.ClockTime{Hour: 9}),
			assert: func(t *testing.T, next time.Time, ok bool) {
				want := time.Date(2026, 7, 10, 9, 0, 0, 0, time.UTC)
				if !ok || !next.Equal(want) {
					t.Fatalf("next=%v ok=%v want %v", next, ok, want)
				}
			},
		},
		{
			name:  "weekly interval>1 multi-weekday jumps to the next interval-week",
			after: time.Date(2026, 7, 22, 10, 0, 0, 0, time.UTC), // Wednesday, past this week's 09:00
			trig:  scheduler.Weekly(2, []time.Weekday{time.Monday, time.Wednesday}, scheduler.ClockTime{Hour: 9}),
			assert: func(t *testing.T, next time.Time, ok bool) {
				// the very next Monday is only one interval-week out (ignored);
				// the interval-2 grid lands on the Monday two weeks out instead.
				want := time.Date(2026, 8, 3, 9, 0, 0, 0, time.UTC)
				if !ok || !next.Equal(want) {
					t.Fatalf("next=%v ok=%v want %v", next, ok, want)
				}
			},
		},
		{
			name:  "monthly interval>1 jumps by interval months",
			after: time.Date(2026, 1, 31, 10, 0, 0, 0, time.UTC), // past the day's 09:00
			trig:  scheduler.Monthly(2, []int{31}, scheduler.ClockTime{Hour: 9}),
			assert: func(t *testing.T, next time.Time, ok bool) {
				// February (interval-blind next match) has no 31st; the
				// interval-2 grid lands on March, which does.
				want := time.Date(2026, 3, 31, 9, 0, 0, 0, time.UTC)
				if !ok || !next.Equal(want) {
					t.Fatalf("next=%v ok=%v want %v", next, ok, want)
				}
			},
		},
		{
			name: "daily zero interval reports no future fire (mod-by-zero guard)",
			trig: scheduler.Daily(0, scheduler.ClockTime{Hour: 9}),
			assert: func(t *testing.T, next time.Time, ok bool) {
				if ok {
					t.Fatalf("want ok=false, got next=%v", next)
				}
			},
		},
		{
			name:  "monthly interval anchored on a day-less month exhausts the scan bound",
			after: time.Date(2026, 2, 15, 0, 0, 0, 0, time.UTC), // February
			trig:  scheduler.Monthly(12, []int{31}, scheduler.ClockTime{Hour: 9}),
			assert: func(t *testing.T, next time.Time, ok bool) {
				// interval=12 (yearly) always lands the grid back on February,
				// which never has a 31st — every scanned candidate is rejected
				// and the bounded scan exhausts without a match.
				if ok {
					t.Fatalf("want ok=false, got next=%v", next)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			a := after
			if !tt.after.IsZero() {
				a = tt.after
			}

			next, ok := tt.trig.Next(a)
			tt.assert(t, next, ok)
		})
	}
}

// TestTrigger_NextAgreesWithLiveScheduler pins Next against the instants the
// LIVE gocron scheduler actually arms. Every want below was measured by
// arming the same Trigger through NativeScheduler.Activate with the clock
// pinned at the case's anchor and reading the engine's own next run back
// through Scheduled. The agreement is required: before it, the whole
// non-control half of this table reported ok=false with the zero time, so the
// runtime persisted a zero next_run for definitions that arm and fire.
func TestTrigger_NextAgreesWithLiveScheduler(t *testing.T) {
	t.Parallel()

	var (
		tueFeb10 = time.Date(2026, 2, 10, 9, 30, 0, 0, time.UTC) // a month with no 31st
		satJan31 = time.Date(2026, 1, 31, 9, 30, 0, 0, time.UTC) // the last day of its month
		thuApr30 = time.Date(2026, 4, 30, 23, 0, 0, 0, time.UTC) // last day, past midnight's at-time
		sunNov01 = time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)  // exactly midnight on a Sunday
	)

	type testCase struct {
		name   string
		after  time.Time
		trig   scheduler.Trigger
		assert func(t *testing.T, next time.Time, ok bool)
	}

	// fires asserts Next reports want. armed is the instant gocron was measured
	// to schedule for the same trigger at the same anchor.
	fires := func(want time.Time) func(*testing.T, time.Time, bool) {
		return func(t *testing.T, next time.Time, ok bool) {
			t.Helper()
			if !ok || !next.Equal(want) {
				t.Fatalf("next=%v ok=%v, want %v ok=true", next, ok, want)
			}
		}
	}
	neverDue := func(t *testing.T, next time.Time, ok bool) {
		t.Helper()
		if ok {
			t.Fatalf("want ok=false, got next=%v", next)
		}
	}

	tests := []testCase{
		// An empty weekday set: scheduler/internal/gocron/trigger.go
		// substitutes []time.Weekday{time.Sunday} before handing the job to
		// gocron, so the trigger arms and fires on Sundays.
		{
			name:   "weekly with no weekdays takes the substituted Sunday",
			after:  tueFeb10,
			trig:   scheduler.Weekly(1, nil),
			assert: fires(time.Date(2026, 2, 15, 0, 0, 0, 0, time.UTC)),
		},
		{
			name:   "weekly with no weekdays on a Sunday midnight anchor skips the due-now instant",
			after:  sunNov01,
			trig:   scheduler.Weekly(1, nil),
			assert: fires(time.Date(2026, 11, 8, 0, 0, 0, 0, time.UTC)),
		},
		{
			name:   "weekly with no weekdays honours the interval grid",
			after:  tueFeb10,
			trig:   scheduler.Weekly(2, nil),
			assert: fires(time.Date(2026, 2, 22, 0, 0, 0, 0, time.UTC)),
		},
		{
			name:   "weekly with an empty non-nil weekday slice crosses the month boundary",
			after:  satJan31,
			trig:   scheduler.Weekly(1, []time.Weekday{}),
			assert: fires(time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)),
		},
		// An out-of-range weekday: gocron's guard is `wd >= lastRun.Weekday()`
		// and its candidate day is `lastRun.Day() + (wd - lastRun.Weekday())`,
		// so a weekday above Saturday always matches on gocron's FIRST pass —
		// landing that many days after the anchor and bypassing the interval.
		{
			name:   "weekly out-of-range weekday lands wd-anchor days out",
			after:  tueFeb10,
			trig:   scheduler.Weekly(1, []time.Weekday{time.Weekday(9)}),
			assert: fires(time.Date(2026, 2, 17, 0, 0, 0, 0, time.UTC)),
		},
		{
			name:   "weekly out-of-range weekday measures from the anchor weekday, not the normalised one",
			after:  sunNov01,
			trig:   scheduler.Weekly(1, []time.Weekday{time.Weekday(9)}),
			assert: fires(time.Date(2026, 11, 10, 0, 0, 0, 0, time.UTC)),
		},
		{
			name:   "weekly out-of-range weekday ignores the interval",
			after:  tueFeb10,
			trig:   scheduler.Weekly(2, []time.Weekday{time.Weekday(9)}),
			assert: fires(time.Date(2026, 2, 17, 0, 0, 0, 0, time.UTC)),
		},
		{
			name:   "weekly out-of-range weekday 8 on a Sunday anchor is eight days out, not one",
			after:  sunNov01,
			trig:   scheduler.Weekly(1, []time.Weekday{time.Weekday(8)}),
			assert: fires(time.Date(2026, 11, 9, 0, 0, 0, 0, time.UTC)),
		},
		{
			name:   "weekly far out-of-range weekday keeps counting past a fortnight",
			after:  tueFeb10,
			trig:   scheduler.Weekly(1, []time.Weekday{time.Weekday(13)}),
			assert: fires(time.Date(2026, 2, 21, 0, 0, 0, 0, time.UTC)),
		},
		{
			name:  "weekly out-of-range weekday beats an in-range weekday deferred to the next week",
			after: tueFeb10,
			// Sunday is BEFORE the Tuesday anchor, so gocron defers it to the
			// next interval week (Feb 15) while Weekday(9) matches on the first
			// pass (Feb 17) — and gocron returns the first pass, not the
			// chronologically earlier instant.
			trig:   scheduler.Weekly(1, []time.Weekday{time.Sunday, time.Weekday(9)}),
			assert: fires(time.Date(2026, 2, 17, 0, 0, 0, 0, time.UTC)),
		},
		{
			name:  "weekly negative weekday stays never-due",
			after: tueFeb10,
			// Measured: Activate returns nil but gocron computes a zero next
			// run and drops the job, so it silently never fires.
			trig:   scheduler.Weekly(1, []time.Weekday{time.Weekday(-1)}),
			assert: neverDue,
		},
		// An empty day-of-month set is substituted with []int{1} by the same
		// adapter file.
		{
			name:   "monthly with no days-of-month takes the substituted first of the month",
			after:  tueFeb10,
			trig:   scheduler.Monthly(1, nil),
			assert: fires(time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)),
		},
		{
			name:   "monthly with no days-of-month on a due-now anchor moves to the next month",
			after:  sunNov01,
			trig:   scheduler.Monthly(1, nil),
			assert: fires(time.Date(2026, 12, 1, 0, 0, 0, 0, time.UTC)),
		},
		// A negative day-of-month counts back from the end of the candidate
		// month (gocron's handleNegativeDays): -1 is the last day.
		{
			name:   "monthly day -1 is the last day of a short month",
			after:  tueFeb10,
			trig:   scheduler.Monthly(1, []int{-1}),
			assert: fires(time.Date(2026, 2, 28, 0, 0, 0, 0, time.UTC)),
		},
		{
			name:  "monthly day -1 is recomputed per month, not fixed at the anchor's length",
			after: satJan31,
			// January's last day is the anchor itself and its midnight has
			// passed, so the next fire is February's 28th — not a 31st.
			trig:   scheduler.Monthly(1, []int{-1}),
			assert: fires(time.Date(2026, 2, 28, 0, 0, 0, 0, time.UTC)),
		},
		{
			name:   "monthly day -2 is the second-to-last day",
			after:  thuApr30,
			trig:   scheduler.Monthly(1, []int{-2}),
			assert: fires(time.Date(2026, 5, 30, 0, 0, 0, 0, time.UTC)),
		},
		{
			name:   "monthly negative day still obeys the interval grid",
			after:  satJan31,
			trig:   scheduler.Monthly(2, []int{-1}),
			assert: fires(time.Date(2026, 3, 31, 0, 0, 0, 0, time.UTC)),
		},
		{
			name:  "monthly day -31 skips the months too short to hold it",
			after: tueFeb10,
			// February maps -31 to a day before the month starts, so it is
			// skipped; March maps it to the 1st.
			trig:   scheduler.Monthly(1, []int{-31}),
			assert: fires(time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)),
		},
		{
			name:   "monthly mixes positive and negative days",
			after:  tueFeb10,
			trig:   scheduler.Monthly(1, []int{1, -1}),
			assert: fires(time.Date(2026, 2, 28, 0, 0, 0, 0, time.UTC)),
		},
		// A cron expression that parses but matches nothing: robfig/cron gives
		// up after five years and returns the ZERO time with no error, which
		// Next used to report as (zero, true) — defeating every ok-keyed gate.
		{
			name:   "cron that can never match reports never-due instead of a zero instant",
			after:  tueFeb10,
			trig:   scheduler.Cron("0 0 30 2 *"),
			assert: neverDue,
		},
		// REGRESSION PIN, not new behaviour: this shape must KEEP reporting
		// never-due. It is what lets the runtime refuse the arm before
		// gocron's monthlyJob.next spins forever on it.
		{
			name:   "monthly every 12 months on the 31st stays never-due from a February anchor",
			after:  tueFeb10,
			trig:   scheduler.Monthly(12, []int{31}),
			assert: neverDue,
		},
		{
			name:  "at-times are ordered by minute and second, not just hour",
			after: time.Date(2026, 2, 10, 9, 0, 0, 0, time.UTC),
			// Given out of order and differing below the hour: the earliest
			// still-future at-time wins. gocron sorts a job's at-times the same
			// way at setup, so both agree on which one comes first. This pins
			// the comparator that was moved from sort.Slice to
			// slices.SortFunc — the hour tie-breaks had no test before.
			trig: scheduler.Daily(1,
				scheduler.ClockTime{Hour: 9, Minute: 30, Second: 10},
				scheduler.ClockTime{Hour: 9, Minute: 5},
				scheduler.ClockTime{Hour: 9, Minute: 30, Second: 5},
			),
			assert: fires(time.Date(2026, 2, 10, 9, 5, 0, 0, time.UTC)),
		},
		{
			name:  "at-time ordering breaks a minute tie on seconds",
			after: time.Date(2026, 2, 10, 9, 10, 0, 0, time.UTC),
			trig: scheduler.Daily(1,
				scheduler.ClockTime{Hour: 9, Minute: 30, Second: 10},
				scheduler.ClockTime{Hour: 9, Minute: 30, Second: 5},
			),
			assert: fires(time.Date(2026, 2, 10, 9, 30, 5, 0, time.UTC)),
		},
		// The shapes gocron REFUSES at setup. Next used to report a fire
		// instant for each of these, so the runtime armed them, wrote a
		// durable row with that instant inside the commit transaction, and
		// only then failed post-commit where failure is WARN-only — the timer
		// never fires, the token parks forever, and the row re-fails on every
		// boot. Same failure mode as blocker 2, without the zero literal.
		{
			name:  "monthly rejects the whole day list when one entry is out of range",
			after: tueFeb10,
			// gocron validates every entry and rejects the JOB, not the entry
			// (job.go:522-524), so a single typo beside a valid day is fatal.
			trig:   scheduler.Monthly(1, []int{15, 32}),
			assert: neverDue,
		},
		{
			name:   "monthly rejects a zero day beside a valid one",
			after:  tueFeb10,
			trig:   scheduler.Monthly(1, []int{15, 0}),
			assert: neverDue,
		},
		{
			name:   "monthly rejects a negative day below -31 beside a valid one",
			after:  tueFeb10,
			trig:   scheduler.Monthly(1, []int{15, -32}),
			assert: neverDue,
		},
		{
			name:   "monthly accepts the boundary days gocron accepts",
			after:  tueFeb10,
			trig:   scheduler.Monthly(1, []int{31, -31}),
			assert: fires(time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)),
		},
		{
			name:  "every-random rejects a min above its max",
			after: tueFeb10,
			// gocron's guard is min >= max (job.go:356), so the runtime must
			// not arm one — it has a NON-zero next_run, which is why no
			// next_run-keyed guard could ever see it.
			trig:   scheduler.EveryRandom(2*time.Hour, time.Hour),
			assert: neverDue,
		},
		{
			name:   "every-random rejects equal bounds",
			after:  tueFeb10,
			trig:   scheduler.EveryRandom(time.Hour, time.Hour),
			assert: neverDue,
		},
		{
			name:   "every-random still reports the earliest bound for a valid range",
			after:  tueFeb10,
			trig:   scheduler.EveryRandom(time.Hour, 2*time.Hour),
			assert: fires(tueFeb10.Add(time.Hour)),
		},
		{
			name:  "daily rejects an hour above 23",
			after: tueFeb10,
			// ClockTime's fields are unvalidated uints all the way from the
			// definition YAML; Go's time.Date would silently roll Hour 25 into
			// the next day at 01:00 and persist that as the fire time.
			trig:   scheduler.Daily(1, scheduler.ClockTime{Hour: 25}),
			assert: neverDue,
		},
		{
			name:   "weekly rejects a minute above 59",
			after:  tueFeb10,
			trig:   scheduler.Weekly(1, []time.Weekday{time.Monday}, scheduler.ClockTime{Minute: 99}),
			assert: neverDue,
		},
		{
			name:   "monthly rejects a second above 59",
			after:  tueFeb10,
			trig:   scheduler.Monthly(1, []int{15}, scheduler.ClockTime{Second: 60}),
			assert: neverDue,
		},
		{
			name:  "one bad at-time poisons the whole trigger, as it does for gocron",
			after: tueFeb10,
			trig: scheduler.Daily(1,
				scheduler.ClockTime{Hour: 9},
				scheduler.ClockTime{Hour: 24},
			),
			assert: neverDue,
		},
		{
			name:   "the at-time boundary values gocron accepts still fire",
			after:  tueFeb10,
			trig:   scheduler.Daily(1, scheduler.ClockTime{Hour: 23, Minute: 59, Second: 59}),
			assert: fires(time.Date(2026, 2, 10, 23, 59, 59, 0, time.UTC)),
		},
		// Controls: shapes that already agreed with the scheduler must not move.
		{
			name:   "control in-range weekday is unchanged",
			after:  sunNov01,
			trig:   scheduler.Weekly(1, []time.Weekday{time.Monday}),
			assert: fires(time.Date(2026, 11, 2, 0, 0, 0, 0, time.UTC)),
		},
		{
			name:   "control positive day-of-month is unchanged",
			after:  tueFeb10,
			trig:   scheduler.Monthly(1, []int{15}),
			assert: fires(time.Date(2026, 2, 15, 0, 0, 0, 0, time.UTC)),
		},
		{
			name:   "control daily is unchanged",
			after:  tueFeb10,
			trig:   scheduler.Daily(1),
			assert: fires(time.Date(2026, 2, 11, 0, 0, 0, 0, time.UTC)),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			next, ok := tt.trig.Next(tt.after)
			tt.assert(t, next, ok)
		})
	}
}

// TestTrigger_NextMonthlyScanSkipsOffGridMonths guards the cost of the one
// shape that has to exhaust calendarNext's scan: a valid day-of-month that no
// month on the interval grid contains. The scan bound is scaled by interval,
// and interval is an unvalidated uint carried from the definition,
// so walking every day of every off-grid month made this linear in a
// consumer-supplied number — paid on the arm path, inside the commit
// transaction.
//
// Skipping each off-grid month in one step is what this pins. Measured for the
// trigger below: 633 ms before / 45 ms after, and 5.31 s before / 0.32 s after
// under -race, which is the slower of the two runs and therefore the one the
// bound must survive. 2 s sits ~6x above the passing measurement and ~2.6x
// below the failing one, so it discriminates in both directions without being
// flaky under load. (The interval is 12000 rather than a rounder 120000 to
// keep the FAILING case from costing 53 s under -race.)
//
// ⚠ It is NOT a claim that the scan is cheap: cost is still linear in
// interval, just with a ~16x smaller constant.
func TestTrigger_NextMonthlyScanSkipsOffGridMonths(t *testing.T) {
	t.Parallel()

	// February: no 31st, and with a 12000-month grid no candidate month has
	// one either, so the scan runs to exhaustion.
	after := time.Date(2026, 2, 4, 12, 0, 0, 0, time.UTC)

	type result struct {
		next time.Time
		ok   bool
	}
	done := make(chan result, 1)
	go func() {
		next, ok := scheduler.Monthly(12000, []int{31}).Next(after)
		done <- result{next: next, ok: ok}
	}()

	select {
	case got := <-done:
		if got.ok {
			t.Fatalf("want ok=false for a day no grid month holds, got %v", got.next)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Next did not return within 2s: the scan is walking off-grid months day by day")
	}
}

// bruteMonthlyNext is an independent reference for calendarNext's monthly
// answer: it walks GRID months one interval-stride at a time and checks each
// day of each, with no jump arithmetic to get wrong. Positive day-of-month
// sets only — negative days are recomputed per month by monthlyDayMatches and
// are covered by TestTrigger_Next's own cases.
func bruteMonthlyNext(after time.Time, interval uint, days []int, bound int) (time.Time, bool) {
	loc := after.Location()
	civil := func(t time.Time) int {
		y, m, d := t.Date()
		return int(time.Date(y, m, d, 0, 0, 0, 0, time.UTC).Unix() / 86400)
	}
	startIdx := civil(after)
	for k := 0; ; k++ {
		gm := time.Date(after.Year(), after.Month()+time.Month(uint(k)*interval), 1, 0, 0, 0, 0, loc)
		off := civil(gm) - startIdx
		if off > bound {
			return time.Time{}, false
		}
		daysIn := time.Date(gm.Year(), gm.Month()+1, 0, 0, 0, 0, 0, loc).Day()
		for d := 1; d <= daysIn; d++ {
			if off+d-1 > bound {
				return time.Time{}, false
			}
			if !slices.Contains(days, d) {
				continue
			}
			if candidate := time.Date(gm.Year(), gm.Month(), d, 0, 0, 0, 0, loc); candidate.After(after) {
				return candidate, true
			}
		}
	}
}

// TestTrigger_NextMonthlyGridJumpMatchesBruteForce guards the CORRECTNESS of
// the interval-stride jump that TestTrigger_NextMonthlyScanJumpsWholeGridStrides
// guards the COST of. A jump is arithmetic on a loop index, and the way it goes
// wrong is silent: it lands one day late and every grid month's 1st stops being
// tested.
//
// ⚠ This test exists because that exact failure was MEASURED to be invisible.
// Dropping the `- 1` from the jump's index arithmetic left the ENTIRE
// TestTrigger_* suite — including the grid-stride and interval-overflow tests
// and TestTrigger_NextAgreesWithLiveScheduler — green, while this comparison
// found mismatches immediately:
//
//	anchor=2026-02-04 days=[1] interval=2: got (zero,false) want (2026-04-01,true)
//	anchor=2026-02-04 days=[1] interval=3: got (zero,false) want (2026-05-01,true)
//
// i.e. Monthly(2, {1}) silently reporting "never due". That is what makes this
// test fail; a cost bound cannot catch it, because the broken version is fast.
func TestTrigger_NextMonthlyGridJumpMatchesBruteForce(t *testing.T) {
	t.Parallel()

	anchors := []time.Time{
		time.Date(2026, 2, 4, 12, 0, 0, 0, time.UTC),     // short month
		time.Date(2026, 1, 31, 0, 0, 0, 0, time.UTC),     // last day of a 31-day month
		time.Date(2024, 12, 15, 23, 59, 59, 0, time.UTC), // year boundary, leap year
		time.Date(2026, 8, 20, 12, 0, 0, 0, time.UTC),
		time.Date(2027, 3, 1, 0, 0, 0, 0, time.UTC), // first day of a month
	}
	daySets := [][]int{{1}, {31}, {15}, {29}, {30}, {1, 15, 31}, {28, 29}}
	intervals := []uint{1, 2, 3, 4, 5, 6, 7, 11, 12, 13, 24, 25, 60, 100, 120, 1000, 1200, 5000}

	for _, anchor := range anchors {
		for _, days := range daySets {
			for _, interval := range intervals {
				gotNext, gotOK := scheduler.Monthly(interval, days).Next(anchor)
				wantNext, wantOK := bruteMonthlyNext(anchor, interval, days, maxCalendarScanDaysForTest*int(interval))

				require.Equalf(t, wantOK, gotOK,
					"anchor=%s days=%v interval=%d: ok mismatch (got next=%v, want next=%v)",
					anchor.Format(time.RFC3339), days, interval, gotNext, wantNext)
				if wantOK {
					assert.Truef(t, gotNext.Equal(wantNext),
						"anchor=%s days=%v interval=%d: got %v, want %v",
						anchor.Format(time.RFC3339), days, interval, gotNext, wantNext)
				}
			}
		}
	}
}

// maxCalendarScanDaysForTest mirrors the unexported maxCalendarScanDays in
// trigger.go (366*5). It is duplicated rather than exported because these are
// black-box tests; TestTrigger_NextMonthlyScanJumpsWholeGridStrides fails if
// the production constant ever shrinks below it in a way that matters.
const maxCalendarScanDaysForTest = 366 * 5

// TestTrigger_NextMonthlyScanJumpsWholeGridStrides is the regression test for
// a cost defect: calendarNext's monthly scan skipped ONE month per step when a
// month was off the interval grid, so an unsatisfiable day-of-month re-tested
// and discarded interval-1 whole months and stayed linear in a
// consumer-supplied uint — on the arm path, inside the commit transaction.
//
// ⚠ A single measurement, Monthly(120000,{31}) at ~404 ms, makes this look
// like a non-issue. That number is real but it is one point on a straight
// line. Measured across the range (anchor
// 2026-02-04, day 31, intervals ≡ 0 mod 12 so every grid month is a February
// and the scan must exhaust):
//
//	interval    plain     -race
//	12000       0.047s    0.316s
//	120000      0.407s    —
//	300000      1.011s    —
//	786432      2.746s   20.677s
//	1044480     3.568s   27.159s   ← just under maxSchedulableInterval
//
// So the interval clamp bounds this at ~3.6 s / ~27 s rather than
// removing it. This test pins the fix that does remove it: jumping straight to
// the next ON-GRID month, which makes the iteration count depend on the scan
// bound's 5-year-per-interval-unit shape rather than on interval itself.
//
// What makes it fail without the jump: 3.568 s plain / 27.159 s under -race,
// both far above the 2 s bound. What makes the bound safe once fixed: the
// jump completes in milliseconds in both modes. -race is ~7.6x slower here
// (measured above), so a bound that discriminates must be checked in both —
// 2 s is ~1.8x below the plain failing time and ~13x below the -race one.
func TestTrigger_NextMonthlyScanJumpsWholeGridStrides(t *testing.T) {
	t.Parallel()

	// February anchor with day-of-month 31, and an interval divisible by 12 so
	// every grid month is also a February — no grid month can ever hold a 31st
	// and the scan is forced to exhaustion. 1044480 == 12 * 87040, just under
	// maxSchedulableInterval so the interval clamp does not short-circuit it.
	after := time.Date(2026, 2, 4, 12, 0, 0, 0, time.UTC)

	type result struct {
		next time.Time
		ok   bool
	}
	done := make(chan result, 1)
	go func() {
		next, ok := scheduler.Monthly(1044480, []int{31}).Next(after)
		done <- result{next: next, ok: ok}
	}()

	select {
	case got := <-done:
		assert.False(t, got.ok, "no grid month holds a 31st, so this must fail closed")
		assert.True(t, got.next.IsZero(), "a refused trigger must report the zero time, got %v", got.next)
	case <-time.After(2 * time.Second):
		t.Fatal("Next did not return within 2s: the scan is stepping one month at a time instead of jumping whole interval strides")
	}
}

// TestTrigger_NextCalendarIntervalCannotOverflow is the regression test for
// an integer-overflow defect. weeklyNext computed its interval-week jump as
// `after.Day() - int(after.Weekday()) + int(interval)*7` with interval an
// unvalidated uint carried from a consumer definition, and calendarNext its
// scan bound as `maxCalendarScanDays * int(interval)`. Both conversions wrap.
//
// What makes each row fail without the clamp (measured against the unclamped
// code, anchor Thu 2026-08-20T12:00Z, weekday set [Monday] so the first pass
// finds nothing and the interval-week pass runs):
//
//	interval        int(interval)*7   next                  ok
//	1               7                 2026-08-24            true
//	2               14                2026-08-31            true
//	MaxUint32       30064771065       82316573-12-27        true
//	MaxUint64/7     -2                zero                  false
//	MaxUint64       -7                2026-08-10            TRUE   ← 10 days in the PAST
//
// The MaxUint64 row is the defect: a next fire strictly BEFORE `after`,
// reported ok=true. A past next-run accepted as valid is the never-due /
// past-due-arm class this package exists to refuse; see maxSchedulableInterval
// for what gocron does when handed one.
//
// ⚠ Asserting only `ok` would pass today on the MaxUint64 row — it IS true.
// Every ok=true row therefore also asserts `!next.Before(after)`.
func TestTrigger_NextCalendarIntervalCannotOverflow(t *testing.T) {
	t.Parallel()

	// A Thursday. The weekday set is [Monday], which is < Thursday, so
	// weekdayAtTime's first pass finds nothing and the interval-week jump —
	// the overflowing expression — is what produces the answer.
	after := time.Date(2026, 8, 20, 12, 0, 0, 0, time.UTC)

	type testCase struct {
		name   string
		trig   scheduler.Trigger
		assert func(t *testing.T, next time.Time, ok bool)
	}

	// inFutureWhenOK is the invariant every calendar Next owes its caller: if
	// it reports a fire, that fire may not already have happened.
	inFutureWhenOK := func(t *testing.T, next time.Time, ok bool) {
		t.Helper()
		if ok {
			assert.Falsef(t, next.Before(after),
				"Next reported ok=true with a next-run BEFORE the reference instant: next=%v after=%v", next, after)
		}
	}

	refused := func(t *testing.T, next time.Time, ok bool) {
		t.Helper()
		assert.False(t, ok, "an interval this large must fail closed, not arm")
		assert.True(t, next.IsZero(), "a refused trigger must report the zero time, got %v", next)
	}

	cases := []testCase{
		{
			name: "weekly interval 1 is unaffected",
			trig: scheduler.Weekly(1, []time.Weekday{time.Monday}),
			assert: func(t *testing.T, next time.Time, ok bool) {
				require.True(t, ok)
				inFutureWhenOK(t, next, ok)
				assert.Equal(t, time.Date(2026, 8, 24, 0, 0, 0, 0, time.UTC), next)
			},
		},
		{
			name: "weekly interval 2 is unaffected",
			trig: scheduler.Weekly(2, []time.Weekday{time.Monday}),
			assert: func(t *testing.T, next time.Time, ok bool) {
				require.True(t, ok)
				inFutureWhenOK(t, next, ok)
				assert.Equal(t, time.Date(2026, 8, 31, 0, 0, 0, 0, time.UTC), next)
			},
		},
		{
			name:   "weekly MaxUint32 is refused rather than arming 80 million years out",
			trig:   scheduler.Weekly(math.MaxUint32, []time.Weekday{time.Monday}),
			assert: refused,
		},
		{
			name:   "weekly MaxUint64/7 is refused",
			trig:   scheduler.Weekly(math.MaxUint64/7, []time.Weekday{time.Monday}),
			assert: refused,
		},
		{
			// THE defect row: unclamped this returns 2026-08-10 (ten days
			// before `after`) with ok=true.
			name:   "weekly MaxUint64 is refused, never a PAST next-run with ok=true",
			trig:   scheduler.Weekly(math.MaxUint64, []time.Weekday{time.Monday}),
			assert: refused,
		},
		{
			name:   "daily MaxUint64 is refused (the scan bound overflows too)",
			trig:   scheduler.Daily(math.MaxUint64),
			assert: refused,
		},
		{
			name:   "monthly MaxUint64 is refused",
			trig:   scheduler.Monthly(math.MaxUint64, []int{1}),
			assert: refused,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			next, ok := tc.trig.Next(after)
			inFutureWhenOK(t, next, ok)
			tc.assert(t, next, ok)
		})
	}
}

// TestTrigger_Recurring covers Recurring() per constructor kind.
func TestTrigger_Recurring(t *testing.T) {
	t.Parallel()

	type testCase struct {
		name   string
		trig   scheduler.Trigger
		assert func(t *testing.T, recurring bool)
	}

	tests := []testCase{
		{
			name: "zero trigger is not recurring",
			trig: scheduler.Trigger{},
			assert: func(t *testing.T, recurring bool) {
				if recurring {
					t.Fatalf("want recurring=false")
				}
			},
		},
		{
			name: "at is not recurring",
			trig: scheduler.At(time.Now()),
			assert: func(t *testing.T, recurring bool) {
				if recurring {
					t.Fatalf("want recurring=false")
				}
			},
		},
		{
			name: "after is not recurring",
			trig: scheduler.After(time.Minute),
			assert: func(t *testing.T, recurring bool) {
				if recurring {
					t.Fatalf("want recurring=false")
				}
			},
		},
		{
			name: "every is recurring",
			trig: scheduler.Every(time.Minute),
			assert: func(t *testing.T, recurring bool) {
				if !recurring {
					t.Fatalf("want recurring=true")
				}
			},
		},
		{
			name: "every random is recurring",
			trig: scheduler.EveryRandom(time.Minute, time.Hour),
			assert: func(t *testing.T, recurring bool) {
				if !recurring {
					t.Fatalf("want recurring=true")
				}
			},
		},
		{
			name: "cron is recurring",
			trig: scheduler.Cron("0 9 * * 1-5"),
			assert: func(t *testing.T, recurring bool) {
				if !recurring {
					t.Fatalf("want recurring=true")
				}
			},
		},
		{
			name: "daily is recurring",
			trig: scheduler.Daily(1, scheduler.ClockTime{Hour: 9}),
			assert: func(t *testing.T, recurring bool) {
				if !recurring {
					t.Fatalf("want recurring=true")
				}
			},
		},
		{
			name: "weekly is recurring",
			trig: scheduler.Weekly(1, []time.Weekday{time.Monday}, scheduler.ClockTime{Hour: 9}),
			assert: func(t *testing.T, recurring bool) {
				if !recurring {
					t.Fatalf("want recurring=true")
				}
			},
		},
		{
			name: "monthly is recurring",
			trig: scheduler.Monthly(1, []int{15}, scheduler.ClockTime{Hour: 9}),
			assert: func(t *testing.T, recurring bool) {
				if !recurring {
					t.Fatalf("want recurring=true")
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			tt.assert(t, tt.trig.Recurring())
		})
	}
}

// TestTrigger_Accessors covers the package-internal accessors
// (AbsTime/Duration/Random/CronExpr/Calendar) that mirror the internal
// gocron engine's own TriggerDef accessors of the same names: each accessor
// must report ok=true with the right value for its own kind, and ok=false
// for every other kind.
func TestTrigger_Accessors(t *testing.T) {
	t.Parallel()

	at := time.Date(2026, 7, 22, 9, 0, 0, 0, time.UTC)
	clockTimes := []scheduler.ClockTime{{Hour: 9}}
	weekdays := []time.Weekday{time.Monday}
	days := []int{15}

	type testCase struct {
		name   string
		trig   scheduler.Trigger
		assert func(t *testing.T, trig scheduler.Trigger)
	}

	tests := []testCase{
		{
			name: "at reports AbsTime only",
			trig: scheduler.At(at),
			assert: func(t *testing.T, trig scheduler.Trigger) {
				got, ok := trig.AbsTime()
				if !ok || !got.Equal(at) {
					t.Fatalf("AbsTime()=%v,%v want %v,true", got, ok, at)
				}
				if _, ok := trig.Duration(); ok {
					t.Fatalf("Duration() ok=true, want false")
				}
			},
		},
		{
			name: "after reports Duration only",
			trig: scheduler.After(time.Minute),
			assert: func(t *testing.T, trig scheduler.Trigger) {
				got, ok := trig.Duration()
				if !ok || got != time.Minute {
					t.Fatalf("Duration()=%v,%v want %v,true", got, ok, time.Minute)
				}
				if _, ok := trig.AbsTime(); ok {
					t.Fatalf("AbsTime() ok=true, want false")
				}
			},
		},
		{
			name: "every reports Duration only",
			trig: scheduler.Every(2 * time.Hour),
			assert: func(t *testing.T, trig scheduler.Trigger) {
				got, ok := trig.Duration()
				if !ok || got != 2*time.Hour {
					t.Fatalf("Duration()=%v,%v want %v,true", got, ok, 2*time.Hour)
				}
			},
		},
		{
			name: "every random reports Random only",
			trig: scheduler.EveryRandom(time.Minute, time.Hour),
			assert: func(t *testing.T, trig scheduler.Trigger) {
				min, max, ok := trig.Random()
				if !ok || min != time.Minute || max != time.Hour {
					t.Fatalf("Random()=%v,%v,%v want %v,%v,true", min, max, ok, time.Minute, time.Hour)
				}
				if _, ok := trig.Duration(); ok {
					t.Fatalf("Duration() ok=true, want false")
				}
			},
		},
		{
			name: "cron reports CronExpr only",
			trig: scheduler.Cron("0 9 * * 1-5"),
			assert: func(t *testing.T, trig scheduler.Trigger) {
				got, ok := trig.CronExpr()
				if !ok || got != "0 9 * * 1-5" {
					t.Fatalf("CronExpr()=%q,%v want %q,true", got, ok, "0 9 * * 1-5")
				}
				if _, _, ok := trig.Random(); ok {
					t.Fatalf("Random() ok=true, want false")
				}
			},
		},
		{
			name: "daily reports Calendar only",
			trig: scheduler.Daily(3, clockTimes...),
			assert: func(t *testing.T, trig scheduler.Trigger) {
				interval, gotDays, gotWeekdays, gotAt, ok := trig.Calendar()
				if !ok || interval != 3 || len(gotDays) != 0 || len(gotWeekdays) != 0 || len(gotAt) != 1 || gotAt[0] != clockTimes[0] {
					t.Fatalf("Calendar()=%v,%v,%v,%v,%v unexpected", interval, gotDays, gotWeekdays, gotAt, ok)
				}
				if _, ok := trig.CronExpr(); ok {
					t.Fatalf("CronExpr() ok=true, want false")
				}
			},
		},
		{
			name: "weekly reports Calendar with weekdays",
			trig: scheduler.Weekly(1, weekdays, clockTimes...),
			assert: func(t *testing.T, trig scheduler.Trigger) {
				interval, gotDays, gotWeekdays, gotAt, ok := trig.Calendar()
				if !ok || interval != 1 || len(gotDays) != 0 || len(gotWeekdays) != 1 || gotWeekdays[0] != time.Monday || len(gotAt) != 1 {
					t.Fatalf("Calendar()=%v,%v,%v,%v,%v unexpected", interval, gotDays, gotWeekdays, gotAt, ok)
				}
			},
		},
		{
			name: "monthly reports Calendar with days-of-month",
			trig: scheduler.Monthly(1, days, clockTimes...),
			assert: func(t *testing.T, trig scheduler.Trigger) {
				interval, gotDays, gotWeekdays, gotAt, ok := trig.Calendar()
				if !ok || interval != 1 || len(gotDays) != 1 || gotDays[0] != 15 || len(gotWeekdays) != 0 || len(gotAt) != 1 {
					t.Fatalf("Calendar()=%v,%v,%v,%v,%v unexpected", interval, gotDays, gotWeekdays, gotAt, ok)
				}
			},
		},
		{
			name: "zero trigger reports ok=false for every accessor",
			trig: scheduler.Trigger{},
			assert: func(t *testing.T, trig scheduler.Trigger) {
				if _, ok := trig.AbsTime(); ok {
					t.Fatalf("AbsTime() ok=true, want false")
				}
				if _, ok := trig.Duration(); ok {
					t.Fatalf("Duration() ok=true, want false")
				}
				if _, _, ok := trig.Random(); ok {
					t.Fatalf("Random() ok=true, want false")
				}
				if _, ok := trig.CronExpr(); ok {
					t.Fatalf("CronExpr() ok=true, want false")
				}
				if _, _, _, _, ok := trig.Calendar(); ok {
					t.Fatalf("Calendar() ok=true, want false")
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			tt.assert(t, tt.trig)
		})
	}
}

// TestTrigger_IsZero covers the zero-value detection used by validation
// callers (e.g. "was this node ever given a trigger at all?").
func TestTrigger_IsZero(t *testing.T) {
	t.Parallel()

	type testCase struct {
		name   string
		trig   scheduler.Trigger
		assert func(t *testing.T, isZero bool)
	}

	tests := []testCase{
		{
			name: "zero value is zero",
			trig: scheduler.Trigger{},
			assert: func(t *testing.T, isZero bool) {
				if !isZero {
					t.Fatalf("want isZero=true")
				}
			},
		},
		{
			name: "constructed trigger is not zero",
			trig: scheduler.After(time.Minute),
			assert: func(t *testing.T, isZero bool) {
				if isZero {
					t.Fatalf("want isZero=false")
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			tt.assert(t, tt.trig.IsZero())
		})
	}
}
