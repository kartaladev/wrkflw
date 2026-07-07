// Package schedule defines TriggerSpec — the gocron-neutral, serializable
// "when a timer fires" value used by activity deadlines/reminders and timer
// events. Exactly one form is set; the zero value is unset. TriggerSpec never
// imports gocron; the scheduler adapter maps it to a gocron JobDefinition.
package schedule

import "time"

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

func AfterDuration(d time.Duration) TriggerSpec { return TriggerSpec{kind: KindOneTime, dur: d} }
func At(t time.Time) TriggerSpec                { return TriggerSpec{kind: KindOneTime, at: t} }
func AfterExpr(code string) TriggerSpec         { return TriggerSpec{kind: KindExpr, expr: code} }
func Every(d time.Duration) TriggerSpec         { return TriggerSpec{kind: KindDuration, dur: d} }
func EveryExpr(code string) TriggerSpec         { return TriggerSpec{kind: KindEveryExpr, expr: code} }
func EveryRandom(min, max time.Duration) TriggerSpec {
	return TriggerSpec{kind: KindDurationRand, min: min, max: max}
}
func Cron(expr string) TriggerSpec { return TriggerSpec{kind: KindCron, cron: expr} }
func Daily(interval uint, at ...ClockTime) TriggerSpec {
	return TriggerSpec{kind: KindDaily, interval: interval, atTimes: at}
}
func Weekly(interval uint, days []time.Weekday, at ...ClockTime) TriggerSpec {
	return TriggerSpec{kind: KindWeekly, interval: interval, weekdays: days, atTimes: at}
}
func Monthly(interval uint, days []int, at ...ClockTime) TriggerSpec {
	return TriggerSpec{kind: KindMonthly, interval: interval, days: days, atTimes: at}
}

func (s TriggerSpec) Kind() Kind   { return s.kind }
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

func (s TriggerSpec) Duration() (time.Duration, bool) {
	if (s.kind == KindOneTime && s.at.IsZero()) || s.kind == KindDuration {
		return s.dur, true
	}
	return 0, false
}
func (s TriggerSpec) AbsTime() (time.Time, bool) {
	if s.kind == KindOneTime && !s.at.IsZero() {
		return s.at, true
	}
	return time.Time{}, false
}
func (s TriggerSpec) Expr() (string, Kind, bool) {
	if s.kind == KindExpr || s.kind == KindEveryExpr {
		return s.expr, s.kind, true
	}
	return "", KindUnset, false
}
func (s TriggerSpec) CronExpr() (string, bool) {
	if s.kind == KindCron {
		return s.cron, true
	}
	return "", false
}
func (s TriggerSpec) Random() (time.Duration, time.Duration, bool) {
	if s.kind == KindDurationRand {
		return s.min, s.max, true
	}
	return 0, 0, false
}
func (s TriggerSpec) Calendar() (uint, []int, []time.Weekday, []ClockTime, bool) {
	switch s.kind {
	case KindDaily, KindWeekly, KindMonthly:
		return s.interval, s.days, s.weekdays, s.atTimes, true
	default:
		return 0, nil, nil, nil, false
	}
}
