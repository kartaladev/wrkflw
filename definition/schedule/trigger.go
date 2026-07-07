// Package schedule defines TriggerSpec — the gocron-neutral, serializable
// "when a timer fires" value used by activity deadlines/reminders and timer
// events. Exactly one form is set; the zero value is unset. TriggerSpec never
// imports gocron; the scheduler adapter maps it to a gocron JobDefinition.
package schedule

import "time"

// Kind identifies which trigger form a TriggerSpec carries.
type Kind uint8

const (
	KindUnset        Kind = iota
	KindOneTime           // AfterDuration / At
	KindDuration          // Every
	KindDurationRand      // EveryRandom
	KindCron
	KindDaily
	KindWeekly
	KindMonthly
	KindExpr      // AfterExpr — one-shot dynamic (engine-resolved)
	KindEveryExpr // EveryExpr — recurring dynamic interval (engine-resolved)
)

// ClockTime is a wall-clock time-of-day for calendar triggers (maps to gocron NewAtTime).
type ClockTime struct{ Hour, Minute, Second uint }

// TriggerSpec specifies when a timer fires. Build with the constructors below.
type TriggerSpec struct {
	kind     Kind
	dur      time.Duration // OneTime(after) / Duration
	at       time.Time     // OneTime(absolute)
	expr     string        // Expr / EveryExpr
	cron     string
	min, max time.Duration // DurationRand
	interval uint          // Daily/Weekly/Monthly
	atTimes  []ClockTime
	weekdays []time.Weekday
	days     []int
}

// AfterDuration builds a one-shot trigger that fires after a fixed duration from the moment it is scheduled.
func AfterDuration(d time.Duration) TriggerSpec { return TriggerSpec{kind: KindOneTime, dur: d} }

// At builds a one-shot trigger that fires at the given absolute wall-clock time.
func At(t time.Time) TriggerSpec { return TriggerSpec{kind: KindOneTime, at: t} }

// AfterExpr builds a one-shot dynamic trigger whose delay duration is resolved at runtime by the engine
// via expr-lang expression evaluation (e.g. `"2h"`). The expression must evaluate to a duration string.
func AfterExpr(code string) TriggerSpec { return TriggerSpec{kind: KindExpr, expr: code} }

// Every builds a recurring trigger that fires repeatedly on a fixed interval.
func Every(d time.Duration) TriggerSpec { return TriggerSpec{kind: KindDuration, dur: d} }

// EveryExpr builds a recurring dynamic-interval trigger whose interval is resolved at runtime by the
// engine via expr-lang expression evaluation. The expression must evaluate to a duration string.
func EveryExpr(code string) TriggerSpec { return TriggerSpec{kind: KindEveryExpr, expr: code} }

// EveryRandom builds a recurring trigger that fires at a random interval uniformly distributed between
// min and max. The scheduler defers the exact form to the native gocron random-duration job definition.
func EveryRandom(min, max time.Duration) TriggerSpec {
	return TriggerSpec{kind: KindDurationRand, min: min, max: max}
}

// Cron builds a recurring trigger driven by a standard cron expression (e.g. "0 9 * * 1-5").
// The scheduler defers the exact form to the native gocron cron job definition.
func Cron(expr string) TriggerSpec { return TriggerSpec{kind: KindCron, cron: expr} }

// Daily builds a recurring trigger that fires every interval days, optionally at specified
// wall-clock times. The scheduler defers the exact form to the native gocron daily job definition.
func Daily(interval uint, at ...ClockTime) TriggerSpec {
	return TriggerSpec{kind: KindDaily, interval: interval, atTimes: at}
}

// Weekly builds a recurring trigger that fires every interval weeks on the given weekdays, optionally
// at specified wall-clock times. The scheduler defers the exact form to the native gocron weekly job
// definition.
func Weekly(interval uint, days []time.Weekday, at ...ClockTime) TriggerSpec {
	return TriggerSpec{kind: KindWeekly, interval: interval, weekdays: days, atTimes: at}
}

// Monthly builds a recurring trigger that fires every interval months on the given days of the month,
// optionally at specified wall-clock times. The scheduler defers the exact form to the native gocron
// monthly job definition.
func Monthly(interval uint, days []int, at ...ClockTime) TriggerSpec {
	return TriggerSpec{kind: KindMonthly, interval: interval, days: days, atTimes: at}
}

// Kind returns the trigger form carried by this spec.
func (s TriggerSpec) Kind() Kind { return s.kind }

// IsZero reports whether the spec is unset (zero value).
func (s TriggerSpec) IsZero() bool { return s.kind == KindUnset }

// Recurring reports whether the trigger fires repeatedly. OneTime and the
// one-shot Expr form are non-recurring; all others recur.
func (s TriggerSpec) Recurring() bool {
	switch s.kind {
	case KindUnset, KindOneTime, KindExpr:
		return false
	default:
		return true
	}
}

// Duration returns the duration and ok=true for AfterDuration (KindOneTime without an absolute time)
// and Every (KindDuration). Returns ok=false for all other forms.
func (s TriggerSpec) Duration() (time.Duration, bool) {
	if (s.kind == KindOneTime && s.at.IsZero()) || s.kind == KindDuration {
		return s.dur, true
	}
	return 0, false
}

// AbsTime returns the absolute fire time and ok=true for At (KindOneTime with a non-zero time).
// Returns ok=false for all other forms.
func (s TriggerSpec) AbsTime() (time.Time, bool) {
	if s.kind == KindOneTime && !s.at.IsZero() {
		return s.at, true
	}
	return time.Time{}, false
}

// Expr returns the expression code, the kind (KindExpr or KindEveryExpr), and ok=true for AfterExpr
// and EveryExpr. Returns ok=false for all other forms.
func (s TriggerSpec) Expr() (string, Kind, bool) {
	if s.kind == KindExpr || s.kind == KindEveryExpr {
		return s.expr, s.kind, true
	}
	return "", KindUnset, false
}

// CronExpr returns the cron expression and ok=true for Cron (KindCron).
// Returns ok=false for all other forms.
func (s TriggerSpec) CronExpr() (string, bool) {
	if s.kind == KindCron {
		return s.cron, true
	}
	return "", false
}

// Random returns the min/max bounds and ok=true for EveryRandom (KindDurationRand).
// Returns ok=false for all other forms.
func (s TriggerSpec) Random() (time.Duration, time.Duration, bool) {
	if s.kind == KindDurationRand {
		return s.min, s.max, true
	}
	return 0, 0, false
}

// Calendar returns the interval, days-of-month, weekdays, at-times, and ok=true for Daily, Weekly,
// and Monthly (KindDaily/KindWeekly/KindMonthly). Returns ok=false for all other forms.
func (s TriggerSpec) Calendar() (uint, []int, []time.Weekday, []ClockTime, bool) {
	switch s.kind {
	case KindDaily, KindWeekly, KindMonthly:
		return s.interval, s.days, s.weekdays, s.atTimes, true
	default:
		return 0, nil, nil, nil, false
	}
}
