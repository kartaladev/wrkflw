package schedule_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/kartaladev/wrkflw/definition/schedule"
)

// TestTriggerSpecNeverDue carries BOTH directions in one table on purpose.
// The predicate is sound but not complete, so "never due" and "due" are not
// two independent corpora: a row that moves between them changes the meaning
// of the other side. Keeping them together makes an over-eager predicate fail
// in the same table that an under-eager one does.
//
// Every row's expectation was measured through the production chain
// (runtime.convertTrigger -> scheduler.Trigger.Next) at five anchors; see
// docs/specs/2026-08-13-never-due-gate-and-orphan-reclamation.md §3.1.
func TestTriggerSpecNeverDue(t *testing.T) {
	t.Parallel()

	nineAM := schedule.ClockTime{Hour: 9}

	type testCase struct {
		name   string
		spec   schedule.TriggerSpec
		assert func(t *testing.T, neverDue bool)
	}

	neverDue := func(t *testing.T, got bool) {
		t.Helper()
		assert.True(t, got, "spec can never fire at any anchor: NeverDue() must report true")
	}
	due := func(t *testing.T, got bool) {
		t.Helper()
		assert.False(t, got, "spec was measured DUE at some anchor: NeverDue() must report false")
	}

	cases := []testCase{
		// ---- never due: non-positive fixed interval ----
		{name: "Every(0)", spec: schedule.Every(0), assert: neverDue},
		{name: "Every(-1s)", spec: schedule.Every(-time.Second), assert: neverDue},

		// ---- never due: EveryRandom bounds ----
		{name: "EveryRandom(5s,5s) min==max", spec: schedule.EveryRandom(5*time.Second, 5*time.Second), assert: neverDue},
		{name: "EveryRandom(10s,5s) min>max", spec: schedule.EveryRandom(10*time.Second, 5*time.Second), assert: neverDue},
		{name: "EveryRandom(0,5s) min==0", spec: schedule.EveryRandom(0, 5*time.Second), assert: neverDue},
		{name: "EveryRandom(-1s,5s) min<0", spec: schedule.EveryRandom(-time.Second, 5*time.Second), assert: neverDue},

		// ---- never due: zero calendar interval (all three calendar kinds) ----
		{name: "Daily(0)", spec: schedule.Daily(0, nineAM), assert: neverDue},
		{name: "Weekly(0,[Mon])", spec: schedule.Weekly(0, []time.Weekday{time.Monday}, nineAM), assert: neverDue},
		{name: "Monthly(0,[15])", spec: schedule.Monthly(0, []int{15}, nineAM), assert: neverDue},

		// ---- never due: out-of-range at-time ----
		{name: "Daily(1,{Hour:24})", spec: schedule.Daily(1, schedule.ClockTime{Hour: 24}), assert: neverDue},
		{name: "Daily(1,{Minute:60})", spec: schedule.Daily(1, schedule.ClockTime{Minute: 60}), assert: neverDue},
		{name: "Daily(1,{Second:60})", spec: schedule.Daily(1, schedule.ClockTime{Second: 60}), assert: neverDue},
		{name: "Weekly(1,[Mon],{Hour:25})", spec: schedule.Weekly(1, []time.Weekday{time.Monday}, schedule.ClockTime{Hour: 25}), assert: neverDue},
		{name: "Monthly(1,[15],{Hour:25})", spec: schedule.Monthly(1, []int{15}, schedule.ClockTime{Hour: 25}), assert: neverDue},

		// ---- never due: out-of-range day-of-month ----
		{name: "Monthly(1,[0])", spec: schedule.Monthly(1, []int{0}, nineAM), assert: neverDue},
		{name: "Monthly(1,[32])", spec: schedule.Monthly(1, []int{32}, nineAM), assert: neverDue},
		{name: "Monthly(1,[-32])", spec: schedule.Monthly(1, []int{-32}, nineAM), assert: neverDue},
		{name: "Monthly(1,[15,32]) one bad day poisons the set", spec: schedule.Monthly(1, []int{15, 32}, nineAM), assert: neverDue},

		// ---- never due: ALL-negative weekday set ----
		{name: "Weekly(1,[-1])", spec: schedule.Weekly(1, []time.Weekday{-1}, nineAM), assert: neverDue},
		{name: "Weekly(1,[-1,-3])", spec: schedule.Weekly(1, []time.Weekday{-1, -3}, nineAM), assert: neverDue},
		{name: "Weekly(4,[-1])", spec: schedule.Weekly(4, []time.Weekday{-1}, nineAM), assert: neverDue},

		// ---- DUE: anchor-dependent calendar specs the predicate must not judge ----
		{name: "Monthly(12,[31]) anchor-dependent", spec: schedule.Monthly(12, []int{31}, nineAM), assert: due},
		{name: "Monthly(1,[31])", spec: schedule.Monthly(1, []int{31}, nineAM), assert: due},

		// ---- DUE: negative days-of-month count back from month end ----
		{name: "Monthly(1,[-1]) last day of month", spec: schedule.Monthly(1, []int{-1}, nineAM), assert: due},
		{name: "Monthly(1,[-31]) 1st of a 31-day month", spec: schedule.Monthly(1, []int{-31}, nineAM), assert: due},

		// ---- DUE: empty sets are substituted by the scheduler ----
		{name: "Monthly(1,nil) empty day set substitutes the 1st", spec: schedule.Monthly(1, nil, nineAM), assert: due},
		{name: "Weekly(1,nil) empty weekday set substitutes Sunday", spec: schedule.Weekly(1, nil, nineAM), assert: due},

		// ---- DUE: a weekday above Saturday stays a raw day offset ----
		{name: "Weekly(1,[Weekday(7)])", spec: schedule.Weekly(1, []time.Weekday{time.Weekday(7)}, nineAM), assert: due},
		{name: "Weekly(1,[Weekday(9)])", spec: schedule.Weekly(1, []time.Weekday{time.Weekday(9)}, nineAM), assert: due},

		// ---- DUE: a MIXED weekday set — hence ALL negative, never ANY negative ----
		{name: "Weekly(1,[Weekday(-1),Monday]) mixed set", spec: schedule.Weekly(1, []time.Weekday{time.Weekday(-1), time.Monday}, nineAM), assert: due},

		// ---- DUE: ordinary well-formed specs ----
		{name: "Weekly(1,[Mon])", spec: schedule.Weekly(1, []time.Weekday{time.Monday}, nineAM), assert: due},
		{name: "Daily(1,09:00)", spec: schedule.Daily(1, nineAM), assert: due},
		{name: "Daily(1) no at-times defaults to midnight", spec: schedule.Daily(1), assert: due},
		{name: "Every(1h)", spec: schedule.Every(time.Hour), assert: due},
		{name: "EveryRandom(5s,10s)", spec: schedule.EveryRandom(5*time.Second, 10*time.Second), assert: due},

		// ---- DUE by DELIBERATE OMISSION: cron is out of scope (ADR-0182 §3.2).
		// The first two rows below ARE never due when executed, and the predicate
		// still reports false: judging them would force robfig/cron/v3 into the
		// definition layer, which the owner declined. Do not "fix" these rows —
		// ADR-0176's arm guard is the layer that catches them.
		{name: "Cron(unparseable) out of scope", spec: schedule.Cron("not a cron"), assert: due},
		{name: "Cron(30 February) out of scope", spec: schedule.Cron("0 0 30 2 *"), assert: due},
		{name: "Cron(weekdays 9am) genuinely due", spec: schedule.Cron("0 9 * * 1-5"), assert: due},

		// ---- DUE: one-shot and unset forms ----
		{name: "AfterDuration(1h)", spec: schedule.AfterDuration(time.Hour), assert: due},
		{name: "At(fixed instant)", spec: schedule.At(time.Date(2026, time.August, 14, 9, 0, 0, 0, time.UTC)), assert: due},
		{name: "zero value", spec: schedule.TriggerSpec{}, assert: due},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			tc.assert(t, tc.spec.NeverDue())
		})
	}
}
