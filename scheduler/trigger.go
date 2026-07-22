package scheduler

import (
	"sort"
	"time"

	"github.com/robfig/cron/v3"
)

// triggerKind identifies which form a Trigger carries. The zero kind
// (triggerUnset) makes the zero Trigger safely inert: IsZero reports true,
// Recurring reports false, and Next always reports ok=false.
type triggerKind uint8

const (
	triggerUnset triggerKind = iota
	triggerAt
	triggerAfter
	triggerEvery
	triggerEveryRandom
	triggerCron
	triggerDaily
	triggerWeekly
	triggerMonthly
)

// ClockTime is a wall-clock time-of-day (hour/minute/second) used by the
// calendar constructors ([Daily], [Weekly], [Monthly]) to say *when during the
// day* a recurring trigger fires. [Trigger.Next] resolves it in UTC (see
// [Daily]), but the live scheduler resolves it in the scheduler's local
// timezone (time.Local) — see the timezone note on [Daily].
type ClockTime struct {
	Hour, Minute, Second uint
}

// Trigger is the scheduler's own, gocron-neutral description of "when a job
// fires". Build one with [At], [After], [Every], [EveryRandom], [Cron],
// [Daily], [Weekly], or [Monthly]. The zero Trigger is a valid, inert value:
// [Trigger.IsZero] reports true and [Trigger.Next] always reports ok=false.
//
// Trigger is a pure value type: [Trigger.Next] performs no I/O and reads no
// wall clock — the caller supplies "now" (or any reference instant) as
// after, keeping the computation deterministic and testable. Validity beyond
// shape (e.g. an unparseable cron expression) is likewise reported through
// Next's ok result rather than by panicking, so a Trigger built from
// untrusted input is always safe to hold; the scheduler enforces validity at
// schedule time.
type Trigger struct {
	kind triggerKind

	// at holds the absolute fire time for triggerAt.
	at time.Time

	// dur holds the fixed delay/interval for triggerAfter and triggerEvery.
	dur time.Duration

	// min, max hold the bounds for triggerEveryRandom.
	min, max time.Duration

	// cron holds the standard cron expression for triggerCron.
	cron string

	// interval, days, weekdays, atTimes hold the calendar shape for
	// triggerDaily, triggerWeekly, and triggerMonthly. interval affects only
	// subsequent fires (the live scheduler's business), never the first one
	// Next computes. days is used by triggerMonthly (day-of-month); weekdays
	// is used by triggerWeekly. atTimes is shared by all three calendar
	// kinds; an empty atTimes defaults to midnight (00:00:00).
	interval uint
	days     []int
	weekdays []time.Weekday
	atTimes  []ClockTime
}

// At builds a one-shot Trigger that fires at the given absolute instant. If
// t is at or before the instant later passed to [Trigger.Next], the trigger
// is already due and Next reports it (t, true) rather than ok=false — an
// already-due one-shot fires immediately on arm. The zero time is the one
// exception: it signals a Trigger that was never really armed, so
// [Trigger.Next] reports ok=false for it, matching the zero-Trigger rule.
func At(t time.Time) Trigger {
	return Trigger{kind: triggerAt, at: t}
}

// After builds a one-shot Trigger that fires once, a fixed duration after
// the reference instant passed to [Trigger.Next]. A zero or negative d means
// the trigger is already due when armed; [Trigger.Next] then fires
// immediately (reports ok=true) rather than dropping the fire.
func After(d time.Duration) Trigger {
	return Trigger{kind: triggerAfter, dur: d}
}

// Every builds a recurring Trigger that fires repeatedly on a fixed
// interval. d must be positive: a zero or negative d would never advance
// between fires, so [Trigger.Next] reports ok=false for it rather than
// spinning.
func Every(d time.Duration) Trigger {
	return Trigger{kind: triggerEvery, dur: d}
}

// EveryRandom builds a recurring Trigger that fires at a random interval
// uniformly distributed between min and max. [Trigger.Next] reports the
// earliest possible bound (after+min); the live scheduler resolves the
// actual random delay for each fire. min must be positive, or
// [Trigger.Next] reports ok=false for the same never-advances reason as
// [Every]. Bounds are not validated here — a min greater than max is not
// rejected by this constructor; that validation happens at schedule time,
// and until then Next always uses min as the earliest bound regardless.
func EveryRandom(minimum, maximum time.Duration) Trigger {
	return Trigger{kind: triggerEveryRandom, min: minimum, max: maximum}
}

// Cron builds a recurring Trigger driven by a standard five-field cron
// expression (e.g. "0 9 * * 1-5"), parsed with [cron.ParseStandard]. An
// expression that fails to parse is not rejected here — Trigger has no
// validation step of its own — but [Trigger.Next] then always reports
// ok=false for it; the scheduler enforces expression validity at schedule
// time.
func Cron(expr string) Trigger {
	return Trigger{kind: triggerCron, cron: expr}
}

// Daily builds a recurring Trigger that fires every interval days, at each
// of the given wall-clock times. Omitting at defaults to midnight.
//
// Timezone note: [Trigger.Next] (the pure computation used by this package's
// own tests and by anything driving Trigger directly) resolves at-times in
// UTC. The live scheduler built from this Trigger, however, currently
// resolves at-times in the scheduler's local timezone (time.Local) — the
// internal gocron adapter does not pin gocron.WithLocation(time.UTC), so
// gocron falls back to its own time.Local default. This split is a
// pre-existing, environment-dependent discrepancy, not a documented
// contract; making the live scheduler UTC-fixed (or configurable) is a
// planned follow-up — see
// docs/specs/2026-07-24-calendar-trigger-timezone-followup.md.
func Daily(interval uint, at ...ClockTime) Trigger {
	return Trigger{kind: triggerDaily, interval: interval, atTimes: at}
}

// Weekly builds a recurring Trigger that fires every interval weeks, on each
// of the given weekdays, at each of the given wall-clock times. Omitting at
// defaults to midnight. See the timezone note on [Daily]: at-times resolve
// in UTC via [Trigger.Next] but in time.Local on the live scheduler.
func Weekly(interval uint, days []time.Weekday, at ...ClockTime) Trigger {
	return Trigger{kind: triggerWeekly, interval: interval, weekdays: days, atTimes: at}
}

// Monthly builds a recurring Trigger that fires every interval months, on
// each of the given days of the month, at each of the given wall-clock
// times. Omitting at defaults to midnight. A day of the month that does not
// exist in a given month (e.g. 31 in February) is simply skipped that month.
// See the timezone note on [Daily]: at-times resolve in UTC via
// [Trigger.Next] but in time.Local on the live scheduler.
func Monthly(interval uint, days []int, at ...ClockTime) Trigger {
	return Trigger{kind: triggerMonthly, interval: interval, days: days, atTimes: at}
}

// IsZero reports whether t is the zero Trigger (built with no constructor).
func (t Trigger) IsZero() bool { return t.kind == triggerUnset }

// Recurring reports whether t fires repeatedly. The zero Trigger, [At], and
// [After] are one-shot; every other constructor is recurring.
func (t Trigger) Recurring() bool {
	switch t.kind {
	case triggerUnset, triggerAt, triggerAfter:
		return false
	default:
		return true
	}
}

// Next computes the next DUE fire instant for a trigger armed at after. It
// is a pure, total function: it never panics and never reads the wall
// clock. The second return value is false exactly when the trigger can
// never fire meaningfully — the zero Trigger, an [At] built from the zero
// time, a [Cron] expression that failed to parse, or a recurring interval
// that is non-positive (see [Every] and [EveryRandom]).
//
// Per constructor: [At] and [After] are one-shot and report ok=true even
// when the target instant is at or before after — an already-due one-shot
// means "fire immediately on arm"; the scheduler never drops past-due
// one-shots. [At] of the zero time is the one exception: it is treated as a
// misused, never-armed Trigger and reports ok=false, matching the
// zero-Trigger rule. [Every] reports (after+d, true) for a positive d, and
// ok=false for a non-positive d (which would otherwise never advance).
// [EveryRandom] reports the earliest possible bound (after+min, true) for a
// positive min — the live scheduler resolves the actual draw per fire — and
// ok=false for a non-positive min, for the same reason as [Every]; bounds
// are not validated here (e.g. min>max), so Next always uses min as the
// earliest bound regardless. [Cron] parses expr with the standard
// five-field parser and delegates to it. [Daily], [Weekly], and [Monthly]
// report the next matching calendar occurrence strictly after after, in
// UTC — the interval only constrains subsequent fires, not this first one
// (matching gocron's first-fire behaviour); an omitted atTimes defaults to
// midnight (00:00:00).
func (t Trigger) Next(after time.Time) (time.Time, bool) {
	switch t.kind {
	case triggerAt:
		if t.at.IsZero() {
			return time.Time{}, false
		}
		return t.at, true
	case triggerAfter:
		return after.Add(t.dur), true
	case triggerEvery:
		if t.dur <= 0 {
			return time.Time{}, false
		}
		return after.Add(t.dur), true
	case triggerEveryRandom:
		if t.min <= 0 {
			return time.Time{}, false
		}
		return after.Add(t.min), true
	case triggerCron:
		sched, err := cron.ParseStandard(t.cron)
		if err != nil {
			return time.Time{}, false
		}
		return sched.Next(after), true
	case triggerDaily, triggerWeekly, triggerMonthly:
		return calendarNext(after, t.kind, t.days, t.weekdays, t.atTimes)
	default:
		return time.Time{}, false
	}
}

// AbsTime returns the absolute fire time and ok=true for [At]. It returns
// ok=false for every other Trigger kind. Mirrors the shape of the internal
// gocron engine's own TriggerDef.AbsTime accessor.
func (t Trigger) AbsTime() (time.Time, bool) {
	if t.kind == triggerAt {
		return t.at, true
	}
	return time.Time{}, false
}

// Duration returns the fixed delay/interval and ok=true for [After] and
// [Every]. It returns ok=false for every other Trigger kind. Mirrors the
// shape of the internal gocron engine's own TriggerDef.Duration accessor.
func (t Trigger) Duration() (time.Duration, bool) {
	if t.kind == triggerAfter || t.kind == triggerEvery {
		return t.dur, true
	}
	return 0, false
}

// Random returns the min/max bounds and ok=true for [EveryRandom]. It
// returns ok=false for every other Trigger kind. Mirrors the shape of the
// internal gocron engine's own TriggerDef.Random accessor.
func (t Trigger) Random() (time.Duration, time.Duration, bool) {
	if t.kind == triggerEveryRandom {
		return t.min, t.max, true
	}
	return 0, 0, false
}

// CronExpr returns the cron expression and ok=true for [Cron]. It returns
// ok=false for every other Trigger kind. Mirrors the shape of the internal
// gocron engine's own TriggerDef.CronExpr accessor.
func (t Trigger) CronExpr() (string, bool) {
	if t.kind == triggerCron {
		return t.cron, true
	}
	return "", false
}

// Calendar returns the interval, days-of-month, weekdays, and at-times, and
// ok=true for [Daily], [Weekly], and [Monthly]. It returns ok=false for
// every other Trigger kind. Mirrors the shape of the internal gocron
// engine's own TriggerDef.Calendar accessor.
func (t Trigger) Calendar() (uint, []int, []time.Weekday, []ClockTime, bool) {
	switch t.kind {
	case triggerDaily, triggerWeekly, triggerMonthly:
		return t.interval, t.days, t.weekdays, t.atTimes, true
	default:
		return 0, nil, nil, nil, false
	}
}

// maxCalendarScanDays bounds the forward day-by-day scan in calendarNext so
// a degenerate calendar shape (e.g. an empty weekday set) cannot spin
// forever; it is generous enough (5 years) that no real calendar trigger
// ever hits it.
const maxCalendarScanDays = 366 * 5

// calendarNext scans forward from after, day by day in UTC, for the first
// instant matching kind's day filter (any day for triggerDaily, a weekday in
// weekdays for triggerWeekly, a day-of-month in days for triggerMonthly) at
// one of atTimes (sorted; midnight if atTimes is empty). It returns
// ok=false only if the day filter is empty for a kind that requires one, or
// if the scan exhausts its bound without finding a match.
func calendarNext(after time.Time, kind triggerKind, days []int, weekdays []time.Weekday, atTimes []ClockTime) (time.Time, bool) {
	after = after.UTC()

	times := atTimes
	if len(times) == 0 {
		times = []ClockTime{{}}
	}
	sortedTimes := append([]ClockTime(nil), times...)
	sort.Slice(sortedTimes, func(i, j int) bool {
		a, b := sortedTimes[i], sortedTimes[j]
		if a.Hour != b.Hour {
			return a.Hour < b.Hour
		}
		if a.Minute != b.Minute {
			return a.Minute < b.Minute
		}
		return a.Second < b.Second
	})

	var weekdaySet map[time.Weekday]bool
	if kind == triggerWeekly {
		weekdaySet = make(map[time.Weekday]bool, len(weekdays))
		for _, d := range weekdays {
			weekdaySet[d] = true
		}
		if len(weekdaySet) == 0 {
			return time.Time{}, false
		}
	}

	var daySet map[int]bool
	if kind == triggerMonthly {
		daySet = make(map[int]bool, len(days))
		for _, d := range days {
			daySet[d] = true
		}
		if len(daySet) == 0 {
			return time.Time{}, false
		}
	}

	start := time.Date(after.Year(), after.Month(), after.Day(), 0, 0, 0, 0, time.UTC)
	for i := 0; i <= maxCalendarScanDays; i++ {
		day := start.AddDate(0, 0, i)

		switch kind {
		case triggerWeekly:
			if !weekdaySet[day.Weekday()] {
				continue
			}
		case triggerMonthly:
			if !daySet[day.Day()] {
				continue
			}
		}

		for _, ct := range sortedTimes {
			candidate := time.Date(day.Year(), day.Month(), day.Day(), int(ct.Hour), int(ct.Minute), int(ct.Second), 0, time.UTC)
			if candidate.After(after) {
				return candidate, true
			}
		}
	}

	return time.Time{}, false
}
