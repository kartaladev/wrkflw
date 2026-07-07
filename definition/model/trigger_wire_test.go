package model_test

import (
	"testing"
	"time"

	"github.com/zakyalvan/krtlwrkflw/definition/model"
	"github.com/zakyalvan/krtlwrkflw/definition/schedule"
)

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
