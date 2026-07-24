package gocron

import (
	"errors"
	"fmt"
	"time"

	"github.com/go-co-op/gocron/v2"
)

// ErrUnsupportedTrigger is this engine's own sentinel for a TriggerDef kind
// jobDefinition cannot map to a gocron.JobDefinition — currently only the
// zero TriggerDef (TriggerDef{}). It intentionally mirrors
// scheduler.ErrUnsupportedTrigger's meaning without importing the parent
// scheduler package (see the package doc for the import-direction rule): the
// parent façade recognizes trigger-mapping failures via errors.Is against
// THIS sentinel and translates to its own vocabulary at the boundary.
var ErrUnsupportedTrigger = errors.New("workflow-scheduler: unsupported trigger")

// triggerDefKind identifies which form a TriggerDef carries. The zero kind
// (triggerDefUnset) makes the zero TriggerDef safely inert: jobDefinition
// rejects it with ErrUnsupportedTrigger rather than panicking.
type triggerDefKind uint8

const (
	triggerDefUnset triggerDefKind = iota
	triggerDefOneTime
	triggerDefDuration
	triggerDefDurationRand
	triggerDefCron
	triggerDefDaily
	triggerDefWeekly
	triggerDefMonthly
)

// ClockTime is this engine's own, package-local mirror of a wall-clock
// time-of-day (hour/minute/second), used by the calendar TriggerDef
// constructors ([Daily], [Weekly], [Monthly]) to say *when during the day* a
// recurring trigger fires. It exists so this package stays free of any
// dependency outside the standard library and gocron itself (the same
// import-direction rule TriggerDef's own doc describes) — the parent
// scheduler package's own ClockTime (scheduler/trigger.go) is converted to
// this shape at the façade boundary. Maps directly to
// gocron.NewAtTime(Hour, Minute, Second).
type ClockTime struct {
	Hour, Minute, Second uint
}

// TriggerDef is this gocron engine's own, package-local description of "when
// a job fires" — the decomposed shape [GocronScheduler.ScheduleJob] accepts
// instead of the parent scheduler.Trigger, so this package never has to
// import the parent scheduler package (import cycle: scheduler imports this
// engine). Build one with [At], [After], [Every], [EveryRandom], [Cron],
// [Daily], [Weekly], or [Monthly]. The zero TriggerDef (TriggerDef{}) is
// inert: jobDefinition rejects it with ErrUnsupportedTrigger.
//
// The accessor methods (AbsTime, Duration, Random, CronExpr, Calendar)
// mirror the parent scheduler.Trigger's accessors of the same names;
// jobDefinition consumes them without a cross-package Kind() switch (this
// package switches on the unexported kind field directly since jobDefinition
// lives in the same package as TriggerDef).
type TriggerDef struct {
	kind triggerDefKind

	at       time.Time     // OneTime(absolute) — At
	dur      time.Duration // OneTime(after) / Duration — After / Every
	min, max time.Duration // DurationRand — EveryRandom
	cron     string        // Cron

	interval uint // Daily/Weekly/Monthly
	days     []int
	weekdays []time.Weekday
	atTimes  []ClockTime
}

// At builds a one-shot TriggerDef that fires at the given absolute wall-clock time.
func At(t time.Time) TriggerDef { return TriggerDef{kind: triggerDefOneTime, at: t} }

// After builds a one-shot TriggerDef that fires after a fixed duration from
// the moment it is scheduled.
func After(d time.Duration) TriggerDef { return TriggerDef{kind: triggerDefOneTime, dur: d} }

// Every builds a recurring TriggerDef that fires repeatedly on a fixed interval.
func Every(d time.Duration) TriggerDef { return TriggerDef{kind: triggerDefDuration, dur: d} }

// EveryRandom builds a recurring TriggerDef that fires at a random interval
// uniformly distributed between min and max.
func EveryRandom(minimum, maximum time.Duration) TriggerDef {
	return TriggerDef{kind: triggerDefDurationRand, min: minimum, max: maximum}
}

// Cron builds a recurring TriggerDef driven by a standard cron expression
// (e.g. "0 9 * * 1-5").
func Cron(expr string) TriggerDef { return TriggerDef{kind: triggerDefCron, cron: expr} }

// Daily builds a recurring TriggerDef that fires every interval days,
// optionally at specified wall-clock times. The live scheduler resolves
// these in its configured location — time.UTC by default, or the zone set
// via WithLocation (ADR-0136). Omitting at defaults to midnight.
func Daily(interval uint, at ...ClockTime) TriggerDef {
	return TriggerDef{kind: triggerDefDaily, interval: interval, atTimes: at}
}

// Weekly builds a recurring TriggerDef that fires every interval weeks on
// the given weekdays, optionally at specified wall-clock times. The live
// scheduler resolves these in its configured location — time.UTC by
// default, or the zone set via WithLocation (ADR-0136). Omitting at
// defaults to midnight.
func Weekly(interval uint, days []time.Weekday, at ...ClockTime) TriggerDef {
	return TriggerDef{kind: triggerDefWeekly, interval: interval, weekdays: days, atTimes: at}
}

// Monthly builds a recurring TriggerDef that fires every interval months on
// the given days of the month, optionally at specified wall-clock times. The
// live scheduler resolves these in its configured location — time.UTC by
// default, or the zone set via WithLocation (ADR-0136). Omitting at defaults
// to midnight.
func Monthly(interval uint, days []int, at ...ClockTime) TriggerDef {
	return TriggerDef{kind: triggerDefMonthly, interval: interval, days: days, atTimes: at}
}

// IsZero reports whether t is the zero TriggerDef (built with no constructor).
func (t TriggerDef) IsZero() bool { return t.kind == triggerDefUnset }

// Duration returns the duration and ok=true for After (OneTime without an
// absolute time) and Every. Returns ok=false for all other forms.
func (t TriggerDef) Duration() (time.Duration, bool) {
	if (t.kind == triggerDefOneTime && t.at.IsZero()) || t.kind == triggerDefDuration {
		return t.dur, true
	}
	return 0, false
}

// AbsTime returns the absolute fire time and ok=true for At (OneTime with a
// non-zero time). Returns ok=false for all other forms.
func (t TriggerDef) AbsTime() (time.Time, bool) {
	if t.kind == triggerDefOneTime && !t.at.IsZero() {
		return t.at, true
	}
	return time.Time{}, false
}

// Random returns the min/max bounds and ok=true for EveryRandom. Returns
// ok=false for all other forms.
func (t TriggerDef) Random() (time.Duration, time.Duration, bool) {
	if t.kind == triggerDefDurationRand {
		return t.min, t.max, true
	}
	return 0, 0, false
}

// CronExpr returns the cron expression and ok=true for Cron. Returns
// ok=false for all other forms.
func (t TriggerDef) CronExpr() (string, bool) {
	if t.kind == triggerDefCron {
		return t.cron, true
	}
	return "", false
}

// Calendar returns the interval, days-of-month, weekdays, at-times, and
// ok=true for Daily, Weekly, and Monthly. Returns ok=false for all other
// forms.
func (t TriggerDef) Calendar() (uint, []int, []time.Weekday, []ClockTime, bool) {
	switch t.kind {
	case triggerDefDaily, triggerDefWeekly, triggerDefMonthly:
		return t.interval, t.days, t.weekdays, t.atTimes, true
	default:
		return 0, nil, nil, nil, false
	}
}

// jobDefinition maps a TriggerDef to a gocron JobDefinition and reports
// whether it is a one-shot (so the caller adds WithLimitedRuns(1) and
// removes the job from the tracking map after it fires).
func jobDefinition(t TriggerDef, now time.Time) (gocron.JobDefinition, bool, error) {
	switch t.kind {
	case triggerDefOneTime:
		if at, ok := t.AbsTime(); ok {
			// If the absolute time is in the past (or exactly now), gocron would
			// return ErrOneTimeJobStartDateTimePast — fire immediately instead.
			if !at.After(now) {
				return gocron.OneTimeJob(gocron.OneTimeJobStartImmediately()), true, nil
			}
			return gocron.OneTimeJob(gocron.OneTimeJobStartDateTime(at)), true, nil
		}
		d, _ := t.Duration()
		fireAt := now.Add(d)
		return gocron.OneTimeJob(gocron.OneTimeJobStartDateTime(fireAt)), true, nil

	case triggerDefDuration:
		d, _ := t.Duration()
		return gocron.DurationJob(d), false, nil

	case triggerDefDurationRand:
		mn, mx, _ := t.Random()
		return gocron.DurationRandomJob(mn, mx), false, nil

	case triggerDefCron:
		expr, _ := t.CronExpr()
		return gocron.CronJob(expr, false), false, nil

	case triggerDefDaily:
		interval, _, _, at, _ := t.Calendar()
		return gocron.DailyJob(interval, clockTimesToAtTimes(at)), false, nil

	case triggerDefWeekly:
		interval, _, weekdays, at, _ := t.Calendar()
		if len(weekdays) == 0 {
			weekdays = []time.Weekday{time.Sunday}
		}
		return gocron.WeeklyJob(interval, gocron.NewWeekdays(weekdays[0], weekdays[1:]...), clockTimesToAtTimes(at)), false, nil

	case triggerDefMonthly:
		interval, days, _, at, _ := t.Calendar()
		if len(days) == 0 {
			days = []int{1}
		}
		return gocron.MonthlyJob(interval, gocron.NewDaysOfTheMonth(days[0], days[1:]...), clockTimesToAtTimes(at)), false, nil

	default:
		return nil, false, fmt.Errorf("workflow-scheduler: unschedulable trigger kind %d: %w", t.kind, ErrUnsupportedTrigger)
	}
}

// clockTimesToAtTimes converts a slice of ClockTime values into the
// gocron.AtTimes value required by DailyJob, WeeklyJob, and MonthlyJob. When
// cs is empty it defaults to midnight (00:00:00).
func clockTimesToAtTimes(cs []ClockTime) gocron.AtTimes {
	if len(cs) == 0 {
		return gocron.NewAtTimes(gocron.NewAtTime(0, 0, 0))
	}
	first := gocron.NewAtTime(cs[0].Hour, cs[0].Minute, cs[0].Second)
	rest := make([]gocron.AtTime, 0, len(cs)-1)
	for _, c := range cs[1:] {
		rest = append(rest, gocron.NewAtTime(c.Hour, c.Minute, c.Second))
	}
	return gocron.NewAtTimes(first, rest...)
}
