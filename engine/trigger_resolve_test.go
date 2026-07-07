package engine_test

import (
	"testing"
	"time"

	"github.com/zakyalvan/krtlwrkflw/definition/schedule"
	"github.com/zakyalvan/krtlwrkflw/engine"
	"github.com/zakyalvan/krtlwrkflw/internal/expreval"
)

func TestResolveTrigger(t *testing.T) {
	ev := expreval.New()

	type testCase struct {
		name   string
		spec   schedule.TriggerSpec
		env    map[string]any
		assert func(t *testing.T, got schedule.TriggerSpec, err error)
	}

	cases := []testCase{
		{
			name: "AfterExpr resolves to a concrete duration",
			spec: schedule.AfterExpr(`h * 3600`),
			env:  map[string]any{"h": 2},
			assert: func(t *testing.T, got schedule.TriggerSpec, err error) {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				d, ok := got.Duration()
				if !ok || d != 2*time.Hour {
					t.Fatalf("resolved = %v %v, want 2h true", d, ok)
				}
				if got.Kind() != schedule.KindOneTime {
					t.Fatalf("AfterExpr kind = %d, want KindOneTime", got.Kind())
				}
			},
		},
		{
			name: "EveryExpr resolves to a recurring duration interval",
			spec: schedule.EveryExpr(`"1h"`),
			env:  nil,
			assert: func(t *testing.T, got schedule.TriggerSpec, err error) {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				if got.Kind() != schedule.KindDuration {
					t.Fatalf("EveryExpr kind = %d, want KindDuration", got.Kind())
				}
				d, ok := got.Duration()
				if !ok || d != time.Hour {
					t.Fatalf("resolved = %v %v, want 1h true", d, ok)
				}
			},
		},
		{
			name: "Cron passes through unchanged and does not error",
			spec: schedule.Cron(`0 9 * * *`),
			env:  nil,
			assert: func(t *testing.T, got schedule.TriggerSpec, err error) {
				if err != nil {
					t.Fatalf("resolve must not fail for native forms: %v", err)
				}
				if got.Kind() != schedule.KindCron {
					t.Fatalf("cron must pass through unchanged, kind = %d", got.Kind())
				}
			},
		},
		{
			name: "AfterDuration passes through unchanged",
			spec: schedule.AfterDuration(90 * time.Minute),
			env:  nil,
			assert: func(t *testing.T, got schedule.TriggerSpec, err error) {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				d, ok := got.Duration()
				if !ok || d != 90*time.Minute {
					t.Fatalf("resolved = %v %v, want 90m true", d, ok)
				}
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := engine.ResolveTrigger(ev, tc.spec, tc.env)
			tc.assert(t, got, err)
		})
	}
}
