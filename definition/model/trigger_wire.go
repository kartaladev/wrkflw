package model

import (
	"time"

	"github.com/zakyalvan/krtlwrkflw/definition/schedule"
)

// TriggerWire is the JSON encoding of a schedule.TriggerSpec.
type TriggerWire struct {
	Kind        string              `json:"kind"`
	Nanos       int64               `json:"nanos,omitempty"`
	At          *time.Time          `json:"at,omitempty"`
	Expr        string              `json:"expr,omitempty"`
	Cron        string              `json:"cron,omitempty"`
	MinNanos    int64               `json:"minNanos,omitempty"`
	MaxNanos    int64               `json:"maxNanos,omitempty"`
	Interval    uint                `json:"interval,omitempty"`
	AtTimes     []schedule.ClockTime `json:"atTimes,omitempty"`
	Weekdays    []int               `json:"weekdays,omitempty"`
	DaysOfMonth []int               `json:"daysOfMonth,omitempty"`
}

// PutTrigger encodes s, or returns nil when s is unset.
func PutTrigger(s schedule.TriggerSpec) *TriggerWire {
	if s.IsZero() {
		return nil
	}
	w := &TriggerWire{Kind: kindString(s.Kind())}
	switch {
	case s.Kind() == schedule.KindOneTime:
		if at, ok := s.AbsTime(); ok {
			w.At = &at
		} else {
			d, _ := s.Duration()
			w.Nanos = int64(d)
		}
	case s.Kind() == schedule.KindDuration:
		d, _ := s.Duration()
		w.Nanos = int64(d)
	case s.Kind() == schedule.KindExpr || s.Kind() == schedule.KindEveryExpr:
		w.Expr, _, _ = s.Expr()
	case s.Kind() == schedule.KindCron:
		w.Cron, _ = s.CronExpr()
	case s.Kind() == schedule.KindDurationRand:
		mn, mx, _ := s.Random()
		w.MinNanos, w.MaxNanos = int64(mn), int64(mx)
	case s.Kind() == schedule.KindDaily || s.Kind() == schedule.KindWeekly || s.Kind() == schedule.KindMonthly:
		interval, days, wds, at, _ := s.Calendar()
		w.Interval, w.AtTimes, w.DaysOfMonth = interval, at, days
		for _, d := range wds {
			w.Weekdays = append(w.Weekdays, int(d))
		}
	}
	return w
}

// ReadTrigger decodes w into a TriggerSpec. When w is nil, a non-empty flatExpr
// (the legacy string form) decodes as AfterExpr, or EveryExpr when recurringFlat.
func ReadTrigger(w *TriggerWire, flatExpr string, recurringFlat bool) schedule.TriggerSpec {
	if w == nil {
		if flatExpr == "" {
			return schedule.TriggerSpec{}
		}
		if recurringFlat {
			return schedule.EveryExpr(flatExpr)
		}
		return schedule.AfterExpr(flatExpr)
	}
	switch w.Kind {
	case "onetime":
		if w.At != nil {
			return schedule.At(*w.At)
		}
		return schedule.AfterDuration(time.Duration(w.Nanos))
	case "duration":
		return schedule.Every(time.Duration(w.Nanos))
	case "expr":
		return schedule.AfterExpr(w.Expr)
	case "everyExpr":
		return schedule.EveryExpr(w.Expr)
	case "cron":
		return schedule.Cron(w.Cron)
	case "durationRand":
		return schedule.EveryRandom(time.Duration(w.MinNanos), time.Duration(w.MaxNanos))
	case "daily", "weekly", "monthly":
		wds := make([]time.Weekday, len(w.Weekdays))
		for i, d := range w.Weekdays {
			wds[i] = time.Weekday(d)
		}
		switch w.Kind {
		case "daily":
			return schedule.Daily(w.Interval, w.AtTimes...)
		case "weekly":
			return schedule.Weekly(w.Interval, wds, w.AtTimes...)
		default:
			return schedule.Monthly(w.Interval, w.DaysOfMonth, w.AtTimes...)
		}
	}
	return schedule.TriggerSpec{}
}

func kindString(k schedule.Kind) string {
	switch k {
	case schedule.KindOneTime:
		return "onetime"
	case schedule.KindDuration:
		return "duration"
	case schedule.KindExpr:
		return "expr"
	case schedule.KindEveryExpr:
		return "everyExpr"
	case schedule.KindCron:
		return "cron"
	case schedule.KindDurationRand:
		return "durationRand"
	case schedule.KindDaily:
		return "daily"
	case schedule.KindWeekly:
		return "weekly"
	case schedule.KindMonthly:
		return "monthly"
	default:
		return ""
	}
}
