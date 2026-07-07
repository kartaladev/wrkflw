package engine

import (
	"errors"
	"testing"
	"time"

	"github.com/zakyalvan/krtlwrkflw/definition/schedule"
)

func TestTriggerDelay(t *testing.T) {
	now := time.Date(2026, 7, 7, 9, 0, 0, 0, time.UTC)

	type testCase struct {
		name   string
		spec   schedule.TriggerSpec
		assert func(t *testing.T, got time.Duration, err error)
	}

	cases := []testCase{
		{
			name: "AfterDuration yields its duration",
			spec: schedule.AfterDuration(3 * time.Hour),
			assert: func(t *testing.T, got time.Duration, err error) {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				if got != 3*time.Hour {
					t.Fatalf("delay = %v, want 3h", got)
				}
			},
		},
		{
			name: "At yields the difference from now",
			spec: schedule.At(now.Add(30 * time.Minute)),
			assert: func(t *testing.T, got time.Duration, err error) {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				if got != 30*time.Minute {
					t.Fatalf("delay = %v, want 30m", got)
				}
			},
		},
		{
			name: "Every yields its interval as the delay",
			spec: schedule.Every(15 * time.Minute),
			assert: func(t *testing.T, got time.Duration, err error) {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				if got != 15*time.Minute {
					t.Fatalf("delay = %v, want 15m", got)
				}
			},
		},
		{
			name: "Cron returns ErrUnsupportedTrigger",
			spec: schedule.Cron("0 9 * * *"),
			assert: func(t *testing.T, _ time.Duration, err error) {
				if !errors.Is(err, ErrUnsupportedTrigger) {
					t.Fatalf("cron delay err = %v, want ErrUnsupportedTrigger", err)
				}
			},
		},
		{
			name: "Daily returns ErrUnsupportedTrigger",
			spec: schedule.Daily(1),
			assert: func(t *testing.T, _ time.Duration, err error) {
				if !errors.Is(err, ErrUnsupportedTrigger) {
					t.Fatalf("daily delay err = %v, want ErrUnsupportedTrigger", err)
				}
			},
		},
		{
			name: "EveryRandom returns ErrUnsupportedTrigger",
			spec: schedule.EveryRandom(time.Minute, time.Hour),
			assert: func(t *testing.T, _ time.Duration, err error) {
				if !errors.Is(err, ErrUnsupportedTrigger) {
					t.Fatalf("everyRandom delay err = %v, want ErrUnsupportedTrigger", err)
				}
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := triggerDelay(tc.spec, now)
			tc.assert(t, got, err)
		})
	}
}
