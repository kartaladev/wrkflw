package model_test

import (
	"testing"
	"time"

	"github.com/zakyalvan/krtlwrkflw/definition/model"
	"github.com/zakyalvan/krtlwrkflw/definition/schedule"
)

func TestTriggerWireRoundTripAllKinds(t *testing.T) {
	t.Parallel()

	fixedTime := time.Date(2026, 7, 7, 9, 0, 0, 0, time.UTC)

	tests := map[string]func(t *testing.T){
		"AfterDuration": func(t *testing.T) {
			t.Parallel()
			orig := schedule.AfterDuration(90 * time.Minute)
			got := model.ReadTrigger(model.PutTrigger(orig), "", false)
			d, ok := got.Duration()
			if !ok || d != 90*time.Minute {
				t.Fatalf("Duration: got (%v, %v), want (90m, true)", d, ok)
			}
			if got.Kind() != schedule.KindOneTime {
				t.Fatalf("Kind: got %v, want KindOneTime", got.Kind())
			}
		},
		"At": func(t *testing.T) {
			t.Parallel()
			orig := schedule.At(fixedTime)
			got := model.ReadTrigger(model.PutTrigger(orig), "", false)
			at, ok := got.AbsTime()
			if !ok || !at.Equal(fixedTime) {
				t.Fatalf("AbsTime: got (%v, %v), want (%v, true)", at, ok, fixedTime)
			}
			if got.Kind() != schedule.KindOneTime {
				t.Fatalf("Kind: got %v, want KindOneTime", got.Kind())
			}
		},
		"Every": func(t *testing.T) {
			t.Parallel()
			orig := schedule.Every(time.Minute)
			got := model.ReadTrigger(model.PutTrigger(orig), "", false)
			d, ok := got.Duration()
			if !ok || d != time.Minute {
				t.Fatalf("Duration: got (%v, %v), want (1m, true)", d, ok)
			}
			if got.Kind() != schedule.KindDuration {
				t.Fatalf("Kind: got %v, want KindDuration", got.Kind())
			}
		},
		"AfterExpr": func(t *testing.T) {
			t.Parallel()
			orig := schedule.AfterExpr(`"1h"`)
			got := model.ReadTrigger(model.PutTrigger(orig), "", false)
			expr, k, ok := got.Expr()
			if !ok || expr != `"1h"` || k != schedule.KindExpr {
				t.Fatalf("Expr: got (%q, %v, %v), want (\"1h\", KindExpr, true)", expr, k, ok)
			}
		},
		"EveryExpr": func(t *testing.T) {
			t.Parallel()
			orig := schedule.EveryExpr(`"1h"`)
			got := model.ReadTrigger(model.PutTrigger(orig), "", false)
			expr, k, ok := got.Expr()
			if !ok || expr != `"1h"` || k != schedule.KindEveryExpr {
				t.Fatalf("Expr: got (%q, %v, %v), want (\"1h\", KindEveryExpr, true)", expr, k, ok)
			}
		},
		"EveryRandom": func(t *testing.T) {
			t.Parallel()
			orig := schedule.EveryRandom(time.Second, time.Minute)
			got := model.ReadTrigger(model.PutTrigger(orig), "", false)
			mn, mx, ok := got.Random()
			if !ok || mn != time.Second || mx != time.Minute {
				t.Fatalf("Random: got (%v, %v, %v), want (1s, 1m, true)", mn, mx, ok)
			}
			if got.Kind() != schedule.KindDurationRand {
				t.Fatalf("Kind: got %v, want KindDurationRand", got.Kind())
			}
		},
		"Cron": func(t *testing.T) {
			t.Parallel()
			orig := schedule.Cron(`0 9 * * *`)
			got := model.ReadTrigger(model.PutTrigger(orig), "", false)
			c, ok := got.CronExpr()
			if !ok || c != `0 9 * * *` {
				t.Fatalf("CronExpr: got (%q, %v), want (\"0 9 * * *\", true)", c, ok)
			}
		},
		"Daily": func(t *testing.T) {
			t.Parallel()
			orig := schedule.Daily(2, schedule.ClockTime{Hour: 9})
			got := model.ReadTrigger(model.PutTrigger(orig), "", false)
			interval, days, wds, atTimes, ok := got.Calendar()
			if !ok {
				t.Fatal("Calendar: ok=false")
			}
			if interval != 2 {
				t.Fatalf("interval: got %d, want 2", interval)
			}
			if len(atTimes) != 1 || atTimes[0].Hour != 9 {
				t.Fatalf("atTimes: got %v, want [{Hour:9}]", atTimes)
			}
			if len(days) != 0 {
				t.Fatalf("days: got %v, want empty", days)
			}
			if len(wds) != 0 {
				t.Fatalf("weekdays: got %v, want empty", wds)
			}
			if got.Kind() != schedule.KindDaily {
				t.Fatalf("Kind: got %v, want KindDaily", got.Kind())
			}
		},
		"Weekly": func(t *testing.T) {
			t.Parallel()
			orig := schedule.Weekly(1, []time.Weekday{time.Monday, time.Friday}, schedule.ClockTime{Hour: 8, Minute: 30})
			got := model.ReadTrigger(model.PutTrigger(orig), "", false)
			interval, days, wds, atTimes, ok := got.Calendar()
			if !ok {
				t.Fatal("Calendar: ok=false")
			}
			if interval != 1 {
				t.Fatalf("interval: got %d, want 1", interval)
			}
			if len(wds) != 2 || wds[0] != time.Monday || wds[1] != time.Friday {
				t.Fatalf("weekdays: got %v, want [Monday, Friday]", wds)
			}
			if len(days) != 0 {
				t.Fatalf("daysOfMonth: got %v, want empty (must not bleed into Monthly field)", days)
			}
			if len(atTimes) != 1 || atTimes[0].Hour != 8 || atTimes[0].Minute != 30 {
				t.Fatalf("atTimes: got %v, want [{Hour:8, Minute:30}]", atTimes)
			}
			if got.Kind() != schedule.KindWeekly {
				t.Fatalf("Kind: got %v, want KindWeekly", got.Kind())
			}
		},
		"Monthly": func(t *testing.T) {
			t.Parallel()
			orig := schedule.Monthly(3, []int{1, 15}, schedule.ClockTime{Hour: 0})
			got := model.ReadTrigger(model.PutTrigger(orig), "", false)
			interval, days, wds, atTimes, ok := got.Calendar()
			if !ok {
				t.Fatal("Calendar: ok=false")
			}
			if interval != 3 {
				t.Fatalf("interval: got %d, want 3", interval)
			}
			if len(days) != 2 || days[0] != 1 || days[1] != 15 {
				t.Fatalf("daysOfMonth: got %v, want [1, 15]", days)
			}
			if len(wds) != 0 {
				t.Fatalf("weekdays: got %v, want empty (must not bleed into Weekly field)", wds)
			}
			if len(atTimes) != 1 || atTimes[0].Hour != 0 {
				t.Fatalf("atTimes: got %v, want [{Hour:0}]", atTimes)
			}
			if got.Kind() != schedule.KindMonthly {
				t.Fatalf("Kind: got %v, want KindMonthly", got.Kind())
			}
		},
	}

	for name, fn := range tests {
		t.Run(name, fn)
	}
}

func TestTriggerWire(t *testing.T) {
	t.Run("cron round-trip", func(t *testing.T) {
		w := model.PutTrigger(schedule.Cron(`0 9 * * *`))
		if w == nil || w.Kind != "cron" || w.Cron != `0 9 * * *` {
			t.Fatalf("wire = %+v", w)
		}
		got := model.ReadTrigger(w, "", false)
		if c, ok := got.CronExpr(); !ok || c != `0 9 * * *` {
			t.Fatalf("read = %q %v", c, ok)
		}
	})
	t.Run("duration round-trip", func(t *testing.T) {
		w := model.PutTrigger(schedule.AfterDuration(time.Hour))
		if d, ok := model.ReadTrigger(w, "", false).Duration(); !ok || d != time.Hour {
			t.Fatalf("read = %v %v", d, ok)
		}
	})
	t.Run("nil spec → nil wire", func(t *testing.T) {
		if model.PutTrigger(schedule.TriggerSpec{}) != nil {
			t.Fatal("zero spec must encode as nil wire")
		}
	})
	t.Run("legacy flat string decodes as AfterExpr / EveryExpr", func(t *testing.T) {
		if _, k, ok := model.ReadTrigger(nil, "3h", false).Expr(); !ok || k != schedule.KindExpr {
			t.Fatalf("flat one-shot: %d %v", k, ok)
		}
		if _, k, ok := model.ReadTrigger(nil, "24h", true).Expr(); !ok || k != schedule.KindEveryExpr {
			t.Fatalf("flat recurring: %d %v", k, ok)
		}
	})
}
