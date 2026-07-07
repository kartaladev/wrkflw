package gocron

import (
	"fmt"
	"time"

	"github.com/go-co-op/gocron/v2"

	"github.com/zakyalvan/krtlwrkflw/definition/schedule"
)

// jobDefinition maps a neutral TriggerSpec to a gocron JobDefinition and
// reports whether it is a one-shot (so the caller adds WithLimitedRuns(1) and
// removes the job from the tracking map after it fires).
func jobDefinition(t schedule.TriggerSpec, now time.Time) (gocron.JobDefinition, bool, error) {
	switch t.Kind() {
	case schedule.KindOneTime:
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

	case schedule.KindDuration:
		d, _ := t.Duration()
		return gocron.DurationJob(d), false, nil

	case schedule.KindDurationRand:
		mn, mx, _ := t.Random()
		return gocron.DurationRandomJob(mn, mx), false, nil

	case schedule.KindCron:
		expr, _ := t.CronExpr()
		return gocron.CronJob(expr, false), false, nil

	case schedule.KindDaily:
		interval, _, _, at, _ := t.Calendar()
		return gocron.DailyJob(interval, clockTimesToAtTimes(at)), false, nil

	case schedule.KindWeekly:
		interval, _, weekdays, at, _ := t.Calendar()
		if len(weekdays) == 0 {
			weekdays = []time.Weekday{time.Sunday}
		}
		return gocron.WeeklyJob(interval, gocron.NewWeekdays(weekdays[0], weekdays[1:]...), clockTimesToAtTimes(at)), false, nil

	case schedule.KindMonthly:
		interval, days, _, at, _ := t.Calendar()
		if len(days) == 0 {
			days = []int{1}
		}
		return gocron.MonthlyJob(interval, gocron.NewDaysOfTheMonth(days[0], days[1:]...), clockTimesToAtTimes(at)), false, nil

	default:
		return nil, false, fmt.Errorf("workflow-scheduler: unschedulable trigger kind %d", t.Kind())
	}
}

// clockTimesToAtTimes converts a slice of schedule.ClockTime values into the
// gocron.AtTimes value required by DailyJob, WeeklyJob, and MonthlyJob. When
// cs is empty it defaults to midnight (00:00:00).
func clockTimesToAtTimes(cs []schedule.ClockTime) gocron.AtTimes {
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
