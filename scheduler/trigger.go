package scheduler

import (
	"cmp"
	"slices"
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
// day* a recurring trigger fires. [Trigger.Next] resolves it in the location
// of the after instant it is given; the live scheduler resolves it in its
// configured zone (default UTC, see the scheduler's WithLocation). See the
// timezone note on [Daily].
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
	// triggerDaily, triggerWeekly, and triggerMonthly. calendarNext is
	// interval-aware (ADR-0140): it accepts a scanned day only when its
	// period index (day/week/month, anchored at the after instant given to
	// [Trigger.Next]) is a multiple of interval, matching the live
	// scheduler's first fire for any interval. days is used by
	// triggerMonthly (day-of-month); weekdays is used by triggerWeekly.
	// atTimes is shared by all three calendar kinds; an empty atTimes
	// defaults to midnight (00:00:00).
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
// actual random delay for each fire. This constructor validates nothing;
// [Trigger.Next] is where the bounds are judged, and it reports ok=false for
// a non-positive min (the same never-advances reason as [Every]) AND for
// min >= max, which the live scheduler refuses outright.
func EveryRandom(minimum, maximum time.Duration) Trigger {
	return Trigger{kind: triggerEveryRandom, min: minimum, max: maximum}
}

// Cron builds a recurring Trigger driven by a standard five-field cron
// expression (e.g. "0 9 * * 1-5"), parsed with [cron.ParseStandard]. An
// expression that fails to parse is not rejected here — Trigger has no
// validation step of its own — but [Trigger.Next] then always reports
// ok=false for it; the scheduler enforces expression validity at schedule
// time. [Trigger.Next] resolves the expression in after's location.
func Cron(expr string) Trigger {
	return Trigger{kind: triggerCron, cron: expr}
}

// Daily builds a recurring Trigger that fires every interval days, at each
// of the given wall-clock times. Omitting at defaults to midnight.
//
// Timezone note (ADR-0137): [Trigger.Next] resolves at-times in after's
// location. The runtime and the façade Schedule() pass now.In(scheduler
// location), so this matches the live scheduler's own at-time resolution;
// a UTC after (the default) yields UTC.
func Daily(interval uint, at ...ClockTime) Trigger {
	return Trigger{kind: triggerDaily, interval: interval, atTimes: at}
}

// Weekly builds a recurring Trigger that fires every interval weeks, on each
// of the given weekdays, at each of the given wall-clock times. Omitting at
// defaults to midnight. See the timezone note on [Daily]: [Trigger.Next]
// resolves at-times in after's location (ADR-0137).
//
// An empty days is not an error and not never-due: the scheduler substitutes
// Sunday before arming, and [Trigger.Next] reports that (ADR-0176). A weekday
// outside Sunday–Saturday is likewise accepted and NOT normalised into the
// week — the scheduler treats it as a raw offset from the day it is armed on,
// so time.Weekday(8) fires eight days after a Sunday arm, not the next day.
// Prefer the named constants.
func Weekly(interval uint, days []time.Weekday, at ...ClockTime) Trigger {
	return Trigger{kind: triggerWeekly, interval: interval, weekdays: days, atTimes: at}
}

// Monthly builds a recurring Trigger that fires every interval months, on
// each of the given days of the month, at each of the given wall-clock
// times. Omitting at defaults to midnight. A day of the month that does not
// exist in a given month (e.g. 31 in February) is simply skipped that month.
// See the timezone note on [Daily]: [Trigger.Next] resolves at-times in
// after's location (ADR-0137).
//
// An empty days is not an error and not never-due: the scheduler substitutes
// the 1st before arming, and [Trigger.Next] reports that (ADR-0176). A
// NEGATIVE day counts back from the end of each candidate month — -1 is the
// last day, so it selects the 28th in February and the 31st in March. The
// scheduler rejects a day of 0, or one outside -31..31, at arm time.
//
// ⚠ A day-of-month that no month on the interval grid can hold (e.g.
// Monthly(12, []int{31}) armed in a February) is never due. [Trigger.Next]
// reports ok=false for it and the runtime refuses to arm it — deliberately,
// because gocron v2.22.0 searches for such a day without a bound (ADR-0176).
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
// clock.
//
// The second return value is false when Next finds no fire instant. That
// happens on these branches, and no others:
//
//   - the zero Trigger, and an [At] built from the zero time;
//   - a [Cron] expression that fails to parse, or one that parses but whose
//     next occurrence robfig/cron cannot find within the five years it
//     searches (e.g. "0 0 30 2 *", 30 February);
//   - a non-positive duration for [Every], or a non-positive min or a
//     min >= max for [EveryRandom];
//   - a zero interval for [Daily], [Weekly], or [Monthly];
//   - a [Weekly] whose weekdays are all negative;
//   - a [Daily] or [Monthly] whose forward scan exhausts its bound (5 years,
//     scaled by interval) without a match — which is how a day-of-month no
//     qualifying month contains, such as Monthly(12, []int{31}) anchored in
//     February, is reported.
//
// ok=false is a statement about Next, not a guarantee about the live
// scheduler. The two were reconciled by ADR-0176 across every case measured
// there (docs/specs/2026-08-13-adr-0176-measurements.md §13–§14), which is
// what lets the runtime treat ok=false as "do not arm this" — but they are
// separate implementations, and gocron remains the authority on what fires.
//
// Note in particular that an empty weekday or day-of-month set is NOT
// never-due: the gocron adapter substitutes Sunday and the 1st respectively
// before arming, and Next mirrors that substitution. A negative day-of-month
// counts back from month end (-1 is the last day), and a weekday above
// Saturday stays a raw day offset from the anchor rather than being
// normalised into the week.
//
// Per constructor: [At] and [After] are one-shot and report ok=true even
// when the target instant is at or before after — an already-due one-shot
// means "fire immediately on arm"; the scheduler never drops past-due
// one-shots. [At] of the zero time is the one exception: it is treated as a
// misused, never-armed Trigger and reports ok=false, matching the
// zero-Trigger rule. [Every] reports (after+d, true) for a positive d, and
// ok=false for a non-positive d (which would otherwise never advance).
// [EveryRandom] reports the earliest possible bound (after+min, true) for a
// min in (0, max) — the live scheduler resolves the actual draw per fire —
// and ok=false for a non-positive min, for the same reason as [Every], or for
// min >= max, which the live scheduler refuses outright and which no
// next_run-keyed guard downstream would catch (ADR-0176).
//
// [Cron], [Daily], [Weekly], and [Monthly] resolve the next matching
// occurrence in the location of after (ADR-0137). A UTC after yields UTC.
// [Cron] additionally: robfig/cron resolves the expression in after's
// location; note the live gocron scheduler resolves cron by zone name, so a
// [Cron] trigger under a non-IANA time.FixedZone fails to schedule there
// (ADR-0136) — use UTC/Local/IANA zones with cron. [Daily], [Weekly], and
// [Monthly] are interval-aware (ADR-0140): the first fire Next reports for
// an interval>1 calendar trigger matches the live scheduler's first fire,
// including when the current period's at-times have already passed. An
// omitted atTimes defaults to midnight (00:00:00).
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
		if t.min <= 0 || t.min >= t.max {
			// The live scheduler refuses min >= max outright, so reporting a
			// fire instant here would arm a job that can never run — and,
			// unlike the never-due kinds, it has a NON-zero next run, so no
			// next_run-keyed guard downstream would catch it (ADR-0176).
			return time.Time{}, false
		}
		return after.Add(t.min), true
	case triggerCron:
		sched, err := cron.ParseStandard(t.cron)
		if err != nil {
			return time.Time{}, false
		}
		// Resolve in after's location (ADR-0137): robfig/cron computes Next in
		// the location of the passed instant. The runtime and the façade
		// Schedule() pass now.In(scheduler location), so this matches the live
		// scheduler; a UTC after (the default) yields UTC.
		next := sched.Next(after)
		if next.IsZero() {
			// A parseable expression that matches no instant robfig/cron can
			// find — it searches five years ahead, then gives up and returns
			// the ZERO time with no error. "0 0 30 2 *" (30 February) is the
			// ordinary typo form. Reporting (zero, true) here made every
			// ok-keyed caller persist a zero next_run (ADR-0176).
			return time.Time{}, false
		}
		return next, true
	case triggerDaily, triggerWeekly, triggerMonthly:
		return calendarNext(after, t.kind, t.interval, t.days, t.weekdays, t.atTimes)
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

// maxCalendarScanDays bounds the forward day-by-day scan in calendarNext, at
// interval==1, so a degenerate calendar shape (e.g. an empty weekday set)
// cannot spin forever; it is generous enough (5 years) that no real
// interval==1 calendar trigger ever hits it. calendarNext scales this bound
// by interval (ADR-0140) — at interval==1 the scaled bound equals this
// constant exactly (byte-identical); for interval>1 it grows so a large
// interval's first fire (e.g. "every 8 years") still lies within scan range,
// matching the live scheduler rather than exhausting into ok=false.
const maxCalendarScanDays = 366 * 5

// calendarNext computes the first fire after the given instant for
// triggerDaily, triggerWeekly, and triggerMonthly, in after's location
// (ADR-0137), at one of atTimes (sorted ascending; midnight if atTimes is
// empty). Its answers are reconciled with the live scheduler (ADR-0176): a
// weekday set is handled by weeklyNext, which transcribes gocron's own
// algorithm, while triggerDaily and triggerMonthly scan forward day by day
// for the first day passing kind's filter AND lying on the interval grid
// anchored at after's own day/month (ADR-0140). A scanned day's period index
// — its day offset for triggerDaily, its month offset for triggerMonthly,
// both relative to after — must be a multiple of interval to be accepted;
// day-of-month overflow (e.g. day 31 in a 30-day month) and "the target month
// has no matching day" are handled implicitly, since the scan never lands on
// a day that doesn't exist.
//
// An empty day-of-month set is substituted with the 1st, mirroring the
// substitution the gocron adapter applies before arming (see
// scheduler/internal/gocron/trigger.go). A NEGATIVE day-of-month counts back
// from the end of the candidate month (-1 is the last day), recomputed per
// month as gocron's handleNegativeDays does.
//
// It returns ok=false if interval is zero (never advances, would divide by
// zero) or if the scan exhausts its (interval-scaled) bound without finding a
// match — which is how a day-of-month no qualifying month contains, such as
// Monthly(12, []int{31}) anchored in February, is reported. That answer is
// load-bearing: it is what lets the runtime refuse the arm before gocron's
// own unbounded search spins on it (ADR-0176 §4).
func calendarNext(after time.Time, kind triggerKind, interval uint, days []int, weekdays []time.Weekday, atTimes []ClockTime) (time.Time, bool) {
	if interval == 0 {
		return time.Time{}, false
	}

	if !clockTimesSchedulable(atTimes) {
		return time.Time{}, false
	}

	loc := after.Location()
	sortedTimes := sortedClockTimes(atTimes)

	if kind == triggerWeekly {
		return weeklyNext(after, interval, weekdays, sortedTimes)
	}

	var positiveDays map[int]bool
	var negativeDays []int
	if kind == triggerMonthly {
		if !monthDaysSchedulable(days) {
			return time.Time{}, false
		}
		if len(days) == 0 {
			days = []int{1}
		}
		positiveDays = make(map[int]bool, len(days))
		for _, d := range days {
			if d < 0 {
				negativeDays = append(negativeDays, d)
				continue
			}
			positiveDays[d] = true
		}
	}

	start := time.Date(after.Year(), after.Month(), after.Day(), 0, 0, 0, 0, loc)
	// Scaled per ADR-0140 audit F1: at interval==1 this equals
	// maxCalendarScanDays exactly (byte-identical scan range).
	bound := maxCalendarScanDays * int(interval)
	for i := 0; i <= bound; i++ {
		day := start.AddDate(0, 0, i)

		switch kind {
		case triggerDaily:
			if i%int(interval) != 0 {
				continue
			}
		case triggerMonthly:
			monthIndex := (day.Year()-after.Year())*12 + (int(day.Month()) - int(after.Month()))
			if monthIndex%int(interval) != 0 {
				// Skip the WHOLE month in one step. The grid test is
				// per-month, so every remaining day here would be rejected
				// too, and walking them is what made an unsatisfiable
				// day-of-month cost time linear in a consumer-supplied
				// interval — measured at 6.3 s for Monthly(120000, {31}), on
				// the arm path, inside the commit transaction. Advancing to
				// this month's last day leaves the loop's i++ on the 1st of
				// the next. The set of days actually TESTED is unchanged.
				i += daysInMonth(day) - day.Day()
				continue
			}
			if !monthlyDayMatches(day, positiveDays, negativeDays) {
				continue
			}
		}

		for _, ct := range sortedTimes {
			candidate := time.Date(day.Year(), day.Month(), day.Day(), int(ct.Hour), int(ct.Minute), int(ct.Second), 0, loc)
			if candidate.After(after) {
				return candidate, true
			}
		}
	}

	return time.Time{}, false
}

// weeklyNext transcribes gocron v2.22.0's weeklyJob.next (job.go:1387-1447),
// which is the algorithm that actually schedules a [Weekly] trigger once the
// runtime arms it. Reproducing it here rather than approximating it is what
// makes [Trigger.Next] agree with the live scheduler for the whole weekly
// kind (ADR-0176; ADR-0140 promised the agreement, a scan over a weekday-set
// membership test could not deliver it).
//
// gocron makes two passes. The FIRST considers only weekdays at or after the
// anchor's own weekday, placing each at anchorDay + (weekday - anchorWeekday)
// and taking the earliest at-time strictly after the anchor. The SECOND, used
// only when the first finds nothing, restarts from the Sunday that begins the
// week interval weeks on and accepts an at-time at or after it. Two
// consequences are not obvious and are pinned by tests:
//
//   - A weekday ABOVE Saturday is not normalised into the week — it stays a
//     raw day offset, so it always matches on the first pass, lands that many
//     days past the anchor, and never consults the interval grid at all.
//   - Because the first pass wins outright, an out-of-range weekday beats an
//     in-range one that the anchor has already passed, even when the in-range
//     one would fire sooner.
//
// An empty weekday set is substituted with Sunday, mirroring the substitution
// the gocron adapter applies before arming (scheduler/internal/gocron/trigger.go);
// a NEGATIVE weekday matches in neither pass, which reports ok=false for a
// spec gocron would arm and then silently never fire.
func weeklyNext(after time.Time, interval uint, weekdays []time.Weekday, sortedTimes []ClockTime) (time.Time, bool) {
	if len(weekdays) == 0 {
		weekdays = []time.Weekday{time.Sunday}
	}
	sortedDays := slices.Clone(weekdays)
	slices.Sort(sortedDays)

	if next, ok := weekdayAtTime(after, sortedDays, sortedTimes, true); ok {
		return next, true
	}

	// The start of the week interval weeks on: back up to this week's Sunday,
	// then add whole weeks. Go's date normalisation carries the day number
	// across month and year ends.
	from := time.Date(after.Year(), after.Month(),
		after.Day()-int(after.Weekday())+int(interval)*7, 0, 0, 0, 0, after.Location())
	return weekdayAtTime(from, sortedDays, sortedTimes, false)
}

// weekdayAtTime is gocron's weeklyJob.nextWeekDayAtTime: the first at-time on
// the first candidate weekday that is strictly after ref (firstPass) or not
// before it (the interval-week pass). Weekdays and at-times must both be
// sorted ascending, as gocron sorts them at setup.
func weekdayAtTime(ref time.Time, sortedDays []time.Weekday, sortedTimes []ClockTime, firstPass bool) (time.Time, bool) {
	for _, wd := range sortedDays {
		if wd < ref.Weekday() {
			continue
		}
		offset := int(wd) - int(ref.Weekday())
		for _, ct := range sortedTimes {
			candidate := time.Date(ref.Year(), ref.Month(), ref.Day()+offset,
				int(ct.Hour), int(ct.Minute), int(ct.Second), 0, ref.Location())
			if firstPass {
				if candidate.After(ref) {
					return candidate, true
				}
				continue
			}
			if !candidate.Before(ref) {
				return candidate, true
			}
		}
	}
	return time.Time{}, false
}

// monthlyDayMatches reports whether day satisfies a monthly trigger's
// day-of-month filter. A negative entry counts back from the end of THAT
// day's month (-1 is its last day), recomputed per month exactly as gocron's
// monthlyJob.handleNegativeDays does — so -1 selects the 28th in February and
// the 31st in March. An entry the month is too short to hold (-31 in
// February) maps below the 1st and simply matches nothing that month.
func monthlyDayMatches(day time.Time, positiveDays map[int]bool, negativeDays []int) bool {
	if positiveDays[day.Day()] {
		return true
	}
	if len(negativeDays) == 0 {
		return false
	}
	lastDay := daysInMonth(day)
	for _, neg := range negativeDays {
		if lastDay+1+neg == day.Day() {
			return true
		}
	}
	return false
}

// daysInMonth returns the number of days in t's month, via Go's date
// normalisation of "day 0 of the next month".
func daysInMonth(t time.Time) int {
	return time.Date(t.Year(), t.Month()+1, 0, 0, 0, 0, 0, t.Location()).Day()
}

// clockTimesSchedulable reports whether every at-time is one the live
// scheduler will accept: hours 0–23, minutes and seconds 0–59, matching
// gocron's own setup-time validation. ONE bad entry poisons the whole trigger,
// exactly as it does for gocron, which rejects the job rather than the entry.
//
// This exists because [ClockTime]'s fields are plain uints carried unvalidated
// from the definition, and Go's time.Date would silently normalise an
// out-of-range value into a different instant — an hour of 25 becomes 01:00 the
// NEXT day. Reporting that as the fire time made the runtime persist it in the
// commit transaction and only then fail at Activate, where failure is WARN-only:
// the timer never fires and the row re-fails on every boot (ADR-0176).
func clockTimesSchedulable(cs []ClockTime) bool {
	for _, c := range cs {
		if c.Hour > 23 || c.Minute > 59 || c.Second > 59 {
			return false
		}
	}
	return true
}

// monthDaysSchedulable reports whether every day-of-month is one the live
// scheduler will accept: 1..31 or -31..-1, never 0. gocron rejects the whole
// job on the first bad entry (job.go:522-524), so a single typo beside valid
// days is fatal there and must be here too. An empty list is schedulable — the
// adapter substitutes the 1st.
func monthDaysSchedulable(days []int) bool {
	for _, d := range days {
		if d == 0 || d > 31 || d < -31 {
			return false
		}
	}
	return true
}

// sortedClockTimes returns cs sorted ascending, defaulting to midnight when
// cs is empty. gocron sorts a job's at-times the same way at setup
// (convertAtTimesToDateTime), so both agree on which at-time comes first.
func sortedClockTimes(cs []ClockTime) []ClockTime {
	if len(cs) == 0 {
		return []ClockTime{{}}
	}
	out := slices.Clone(cs)
	slices.SortFunc(out, func(a, b ClockTime) int {
		if a.Hour != b.Hour {
			return cmp.Compare(a.Hour, b.Hour)
		}
		if a.Minute != b.Minute {
			return cmp.Compare(a.Minute, b.Minute)
		}
		return cmp.Compare(a.Second, b.Second)
	})
	return out
}
