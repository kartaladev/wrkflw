package schedule_test

import (
	"testing"
	"time"

	"github.com/kartaladev/wrkflw/definition/schedule"
)

func TestTriggerSpecKinds(t *testing.T) {
	cases := map[string]struct {
		spec      schedule.TriggerSpec
		kind      schedule.Kind
		recurring bool
	}{
		"AfterDuration": {schedule.AfterDuration(time.Hour), schedule.KindOneTime, false},
		"At":            {schedule.At(time.Unix(0, 0)), schedule.KindOneTime, false},
		"AfterExpr":     {schedule.AfterExpr(`"1h"`), schedule.KindExpr, false},
		"Every":         {schedule.Every(time.Minute), schedule.KindDuration, true},
		"EveryExpr":     {schedule.EveryExpr(`"1h"`), schedule.KindEveryExpr, true},
		"EveryRandom":   {schedule.EveryRandom(time.Second, time.Minute), schedule.KindDurationRand, true},
		"Cron":          {schedule.Cron(`0 9 * * *`), schedule.KindCron, true},
		"Daily":         {schedule.Daily(1, schedule.ClockTime{Hour: 9}), schedule.KindDaily, true},
		"Weekly":        {schedule.Weekly(1, []time.Weekday{time.Monday}, schedule.ClockTime{Hour: 9}), schedule.KindWeekly, true},
		"Monthly":       {schedule.Monthly(1, []int{1, 15}, schedule.ClockTime{Hour: 9}), schedule.KindMonthly, true},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			assert := func(cond bool, msg string) {
				if !cond {
					t.Fatalf("%s: %s (spec kind=%d)", name, msg, tc.spec.Kind())
				}
			}
			assert(!tc.spec.IsZero(), "must not be zero")
			assert(tc.spec.Kind() == tc.kind, "kind mismatch")
			assert(tc.spec.Recurring() == tc.recurring, "recurring mismatch")
		})
	}
	if !(schedule.TriggerSpec{}).IsZero() {
		t.Fatal("zero value must be IsZero")
	}
	if d, ok := schedule.AfterDuration(90 * time.Minute).Duration(); !ok || d != 90*time.Minute {
		t.Fatalf("Duration() = %v %v", d, ok)
	}
	if e, k, ok := schedule.AfterExpr(`"1h"`).Expr(); !ok || e != `"1h"` || k != schedule.KindExpr {
		t.Fatalf("Expr() = %q %d %v", e, k, ok)
	}

	// Test AbsTime accessor
	absTime := time.Unix(1000000, 0)
	t.Run("AbsTime", func(t *testing.T) {
		at, ok := schedule.At(absTime).AbsTime()
		if !ok || at != absTime {
			t.Fatalf("AbsTime() = %v %v, want %v true", at, ok, absTime)
		}
	})

	// Test CronExpr accessor
	t.Run("CronExpr", func(t *testing.T) {
		cron, ok := schedule.Cron(`0 9 * * *`).CronExpr()
		if !ok || cron != `0 9 * * *` {
			t.Fatalf("CronExpr() = %q %v", cron, ok)
		}
	})

	// Test Random accessor
	t.Run("Random", func(t *testing.T) {
		min, max, ok := schedule.EveryRandom(time.Second, time.Minute).Random()
		if !ok || min != time.Second || max != time.Minute {
			t.Fatalf("Random() = %v %v %v", min, max, ok)
		}
	})

	// Test Calendar accessor
	t.Run("Calendar", func(t *testing.T) {
		days := []int{1, 15}
		times := []schedule.ClockTime{{Hour: 9}, {Hour: 18}}
		interval, retDays, _, times_, ok := schedule.Monthly(2, days, times...).Calendar()
		if !ok || interval != 2 {
			t.Fatalf("Calendar() interval: got %d %v, want 2 true", interval, ok)
		}
		if len(retDays) != 2 || retDays[0] != 1 || retDays[1] != 15 {
			t.Fatalf("Calendar() days: got %v", retDays)
		}
		if len(times_) != 2 || times_[0].Hour != 9 || times_[1].Hour != 18 {
			t.Fatalf("Calendar() times: got %v", times_)
		}
	})

	// Test Duration accessor on recurring Every
	t.Run("EveryDuration", func(t *testing.T) {
		d, ok := schedule.Every(15 * time.Minute).Duration()
		if !ok || d != 15*time.Minute {
			t.Fatalf("Every Duration() = %v %v", d, ok)
		}
	})

	// Test negative cases (wrong accessor type)
	t.Run("WrongAccessors", func(t *testing.T) {
		// AfterDuration should not return AbsTime
		if _, ok := schedule.AfterDuration(time.Hour).AbsTime(); ok {
			t.Fatal("AfterDuration.AbsTime() should return false")
		}
		// At should not return Duration
		if _, ok := schedule.At(time.Unix(1000, 0)).Duration(); ok {
			t.Fatal("At.Duration() should return false")
		}
		// Every should not return CronExpr
		if _, ok := schedule.Every(time.Minute).CronExpr(); ok {
			t.Fatal("Every.CronExpr() should return false")
		}
		// Cron should not return Random
		if _, _, ok := schedule.Cron(`0 9 * * *`).Random(); ok {
			t.Fatal("Cron.Random() should return false")
		}
		// Daily should not return Expr
		if _, _, ok := schedule.Daily(1, schedule.ClockTime{Hour: 9}).Expr(); ok {
			t.Fatal("Daily.Expr() should return false")
		}
	})
}
