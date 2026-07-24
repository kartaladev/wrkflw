package scheduler_test

import (
	"testing"
	"time"

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
			name:  "cron resolves in UTC regardless of after location",
			after: time.Date(2026, 1, 1, 0, 0, 0, 0, time.FixedZone("plusTwo", 2*60*60)),
			trig:  scheduler.Cron("0 9 * * *"),
			assert: func(t *testing.T, next time.Time, ok bool) {
				// after is 2026-01-01 00:00 in UTC+2 (== 2025-12-31 22:00 UTC); the
				// next 09:00 is computed in UTC (uniform reference), so 2026-01-01
				// 09:00 UTC — NOT 09:00 in +02:00. Regression guard for ADR-0136.
				want := time.Date(2026, 1, 1, 9, 0, 0, 0, time.UTC)
				if !ok || !next.Equal(want) || next.Location() != time.UTC {
					t.Fatalf("next=%v ok=%v want %v UTC", next, ok, want)
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
			name: "weekly with no weekdays reports no future fire",
			trig: scheduler.Weekly(1, nil, scheduler.ClockTime{Hour: 9}),
			assert: func(t *testing.T, next time.Time, ok bool) {
				if ok {
					t.Fatalf("want ok=false, got next=%v", next)
				}
			},
		},
		{
			name: "monthly with no days-of-month reports no future fire",
			trig: scheduler.Monthly(1, nil, scheduler.ClockTime{Hour: 9}),
			assert: func(t *testing.T, next time.Time, ok bool) {
				if ok {
					t.Fatalf("want ok=false, got next=%v", next)
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
