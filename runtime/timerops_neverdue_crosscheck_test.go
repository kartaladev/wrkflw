package runtime

import (
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kartaladev/wrkflw/definition/schedule"
	"github.com/kartaladev/wrkflw/scheduler"
)

// crossCheckAnchors are the fixed anchors every cross-check row is probed at.
// They are the five instants ADR-0182's probe P-A measured the §3.1 corpus at
// (docs/specs/2026-08-13-adr-0181-0182-audit-adjudication.md:19), and they are
// chosen so the anchor-dependent calendar branches are actually reachable: a
// February (no 31st, and 28 days), a 31-day month, a month end, a Sunday (the
// weekday the scheduler substitutes for an empty weekday set), and a leap day.
var crossCheckAnchors = []time.Time{
	time.Date(2026, time.February, 1, 0, 0, 0, 0, time.UTC),  // a February
	time.Date(2026, time.August, 1, 0, 0, 0, 0, time.UTC),    // a 31-day month
	time.Date(2026, time.January, 31, 0, 0, 0, 0, time.UTC),  // a month end
	time.Date(2026, time.June, 14, 0, 0, 0, 0, time.UTC),     // a Sunday
	time.Date(2028, time.February, 29, 0, 0, 0, 0, time.UTC), // a leap day
}

// crossCheckViolation records one anchor at which schedule.TriggerSpec.NeverDue
// reported true and the production chain nevertheless found a fire instant.
type crossCheckViolation struct {
	anchor time.Time
	next   time.Time
}

// crossCheckNeverDue drives one spec through the PRODUCTION chain
// (schedule.TriggerSpec -> convertTrigger -> scheduler.Trigger.Next) and
// reports the spec's NeverDue verdict, any conversion refusal, and every
// anchor that contradicts a true verdict.
//
// It probes ONE direction only:
//
//	spec.NeverDue() == true  =>  Next is !ok at EVERY anchor.
//
// The converse is deliberately NOT probed. NeverDue is sound but knowingly not
// complete (spec §1): Monthly(12,[31]) is !ok in February and ok in August, and
// cron and the engine-resolved expression forms report false by design, so a
// two-way "verdict == verdict" assertion is unsatisfiable rather than merely
// strict. What this catches is UNSOUNDNESS — a NeverDue that refuses a spec
// which actually fires — which is the failure mode that actually occurred: the
// pre-audit design would have rejected Weekly(1,[Weekday(9)]) and
// Monthly(1,[-1]), both measurably due.
func crossCheckNeverDue(spec schedule.TriggerSpec) (bool, []crossCheckViolation, error) {
	neverDue := spec.NeverDue()

	trigger, err := convertTrigger(spec)
	if err != nil {
		return neverDue, nil, err
	}
	if !neverDue {
		return false, nil, nil
	}

	var violations []crossCheckViolation
	for _, anchor := range crossCheckAnchors {
		if next, ok := trigger.Next(anchor); ok {
			violations = append(violations, crossCheckViolation{anchor: anchor, next: next})
		}
	}
	return true, violations, nil
}

// reportCrossCheck applies crossCheckNeverDue's outcome to t and reports the
// spec's NeverDue verdict so a caller can prove its corpus is non-degenerate.
//
// A conversion refusal is handled EXPLICITLY rather than being swallowed by a
// nil-check: KindUnset, KindExpr and KindEveryExpr hit convertTrigger's default
// branch and never become a scheduler.Trigger at all, so there is no Next to
// contradict NeverDue and the implication is vacuously true for them. The
// refusal must still be the documented one.
func reportCrossCheck(t *testing.T, label string, spec schedule.TriggerSpec) bool {
	t.Helper()

	neverDue, violations, err := crossCheckNeverDue(spec)
	if err != nil {
		require.ErrorIsf(t, err, scheduler.ErrUnsupportedTrigger,
			"%s: convertTrigger refused the spec with an undocumented error", label)
		t.Logf("%s: SKIPPED (implication vacuous) — kind %v never converts to a scheduler.Trigger: %v",
			label, spec.Kind(), err)
		return neverDue
	}

	for _, v := range violations {
		t.Errorf("%s: NeverDue() reports true but the production chain found a fire instant at anchor %s: next=%s — the predicate is UNSOUND",
			label, v.anchor.Format(time.RFC3339), v.next.Format(time.RFC3339))
	}
	return neverDue
}

// TestNeverDueAgreesWithScheduler is the ONLY mitigation for the second source
// of truth ADR-0182 knowingly accepts: definition/schedule.TriggerSpec.NeverDue
// duplicates, by hand, the anchor-independent !ok branches of
// scheduler.Trigger.Next. It lives in package runtime (an internal test file)
// because only here are the real unexported convertTrigger AND Trigger.Next
// both in scope, so the chain under test is exactly the production chain; a
// package model_test version would have to hand-roll the conversion — a third
// copy, blind to conversion drift by construction (ADR-0182 Consequences).
//
// Two halves, one property:
//
//   - the fixed §3.1 corpus, the regression floor;
//   - a DETERMINISTIC generated sweep, because a fixed corpus only ever
//     re-checks inputs already agreed on. No math/rand and no go test -fuzz:
//     the gate must be reproducible (spec §6.4).
//
// Mutation-verified 2026-08-14 against three unsound edits of NeverDue —
// Weekly's "ALL negative" weakened to "ANY negative", the Monthly day rule's
// negative-day allowance dropped, and a KindCron branch returning true — each
// of which turned this test RED.
func TestNeverDueAgreesWithScheduler(t *testing.T) {
	t.Parallel()

	t.Run("fixed corpus", func(t *testing.T) {
		t.Parallel()

		corpus := fixedNeverDueCorpus()
		require.NotEmpty(t, corpus)

		// Non-degeneracy: a predicate stuck at false would satisfy the
		// implication vacuously for every row, so the corpus must contain
		// rows on both sides of the verdict.
		var trueRows, falseRows int
		for _, row := range corpus {
			if row.spec.NeverDue() {
				trueRows++
				continue
			}
			falseRows++
		}
		assert.Positivef(t, trueRows, "corpus is degenerate: no row reports NeverDue()==true, so the implication holds vacuously")
		assert.Positivef(t, falseRows, "corpus is degenerate: every row reports NeverDue()==true, so it cannot pin the not-complete side")

		for _, row := range corpus {
			t.Run(row.label, func(t *testing.T) {
				t.Parallel()

				reportCrossCheck(t, row.label, row.spec)
			})
		}

		t.Logf("fixed corpus: %d specs x %d anchors; %d never-due, %d due", len(corpus), len(crossCheckAnchors), trueRows, falseRows)
	})

	t.Run("generated sweep", func(t *testing.T) {
		t.Parallel()

		sweep := generatedNeverDueSweep()
		require.NotEmpty(t, sweep)

		// Reported as one subtest rather than one per spec: at this case count
		// a t.Run per row would bury a -v run in thousands of PASS lines. The
		// row label travels in the failure message instead.
		var neverDueRows, dueRows, violations int
		for _, row := range sweep {
			neverDue, rowViolations, err := crossCheckNeverDue(row.spec)
			if err != nil {
				require.ErrorIsf(t, err, scheduler.ErrUnsupportedTrigger,
					"%s: convertTrigger refused the spec with an undocumented error", row.label)
				continue
			}
			if !neverDue {
				dueRows++
				continue
			}
			neverDueRows++

			for _, v := range rowViolations {
				violations++
				if violations <= 20 {
					t.Errorf("%s: NeverDue() reports true but the production chain found a fire instant at anchor %s: next=%s — the predicate is UNSOUND",
						row.label, v.anchor.Format(time.RFC3339), v.next.Format(time.RFC3339))
				}
			}
		}
		if violations > 20 {
			t.Errorf("generated sweep: %d violations in total, only the first 20 are shown above", violations)
		}

		assert.Positive(t, neverDueRows, "sweep is degenerate: no generated spec reports NeverDue()==true, so the implication holds vacuously")
		assert.Positive(t, dueRows, "sweep is degenerate: every generated spec reports NeverDue()==true")

		t.Logf("generated sweep: %d specs x %d anchors = %d Next probes; %d never-due, %d due",
			len(sweep), len(crossCheckAnchors), len(sweep)*len(crossCheckAnchors), neverDueRows, dueRows)
	})
}

// crossCheckRow is one labelled spec. There is deliberately no per-row expected
// verdict and no per-row assert closure: the asserted property is the SAME
// one-directional implication for every row, and a per-row expectation is
// exactly the two-way "verdict == verdict" shape audit A-F4 rejected as
// unsatisfiable. The per-row verdicts live in
// definition/schedule/trigger_neverdue_test.go, which is the corpus this table
// mirrors.
type crossCheckRow struct {
	label string
	spec  schedule.TriggerSpec
}

// fixedNeverDueCorpus is the §3.1 corpus, the same specs
// definition/schedule/trigger_neverdue_test.go drives TriggerSpec.NeverDue
// with. Kept in the same order and with the same labels so a drift between the
// two files is visible in a diff.
func fixedNeverDueCorpus() []crossCheckRow {
	nineAM := schedule.ClockTime{Hour: 9}

	return []crossCheckRow{
		// ---- never due: non-positive fixed interval ----
		{label: "Every(0)", spec: schedule.Every(0)},
		{label: "Every(-1s)", spec: schedule.Every(-time.Second)},

		// ---- never due: EveryRandom bounds ----
		{label: "EveryRandom(5s,5s) min==max", spec: schedule.EveryRandom(5*time.Second, 5*time.Second)},
		{label: "EveryRandom(10s,5s) min>max", spec: schedule.EveryRandom(10*time.Second, 5*time.Second)},
		{label: "EveryRandom(0,5s) min==0", spec: schedule.EveryRandom(0, 5*time.Second)},
		{label: "EveryRandom(-1s,5s) min<0", spec: schedule.EveryRandom(-time.Second, 5*time.Second)},

		// ---- never due: zero calendar interval (all three calendar kinds) ----
		{label: "Daily(0)", spec: schedule.Daily(0, nineAM)},
		{label: "Weekly(0,[Mon])", spec: schedule.Weekly(0, []time.Weekday{time.Monday}, nineAM)},
		{label: "Monthly(0,[15])", spec: schedule.Monthly(0, []int{15}, nineAM)},

		// ---- never due: out-of-range at-time ----
		{label: "Daily(1,{Hour:24})", spec: schedule.Daily(1, schedule.ClockTime{Hour: 24})},
		{label: "Daily(1,{Minute:60})", spec: schedule.Daily(1, schedule.ClockTime{Minute: 60})},
		{label: "Daily(1,{Second:60})", spec: schedule.Daily(1, schedule.ClockTime{Second: 60})},
		{label: "Weekly(1,[Mon],{Hour:25})", spec: schedule.Weekly(1, []time.Weekday{time.Monday}, schedule.ClockTime{Hour: 25})},
		{label: "Monthly(1,[15],{Hour:25})", spec: schedule.Monthly(1, []int{15}, schedule.ClockTime{Hour: 25})},

		// ---- never due: out-of-range day-of-month ----
		{label: "Monthly(1,[0])", spec: schedule.Monthly(1, []int{0}, nineAM)},
		{label: "Monthly(1,[32])", spec: schedule.Monthly(1, []int{32}, nineAM)},
		{label: "Monthly(1,[-32])", spec: schedule.Monthly(1, []int{-32}, nineAM)},
		{label: "Monthly(1,[15,32]) one bad day poisons the set", spec: schedule.Monthly(1, []int{15, 32}, nineAM)},

		// ---- never due: ALL-negative weekday set ----
		{label: "Weekly(1,[-1])", spec: schedule.Weekly(1, []time.Weekday{-1}, nineAM)},
		{label: "Weekly(1,[-1,-3])", spec: schedule.Weekly(1, []time.Weekday{-1, -3}, nineAM)},
		{label: "Weekly(4,[-1])", spec: schedule.Weekly(4, []time.Weekday{-1}, nineAM)},

		// ---- due: anchor-dependent calendar specs the predicate must not judge ----
		{label: "Monthly(12,[31]) anchor-dependent", spec: schedule.Monthly(12, []int{31}, nineAM)},
		{label: "Monthly(1,[31])", spec: schedule.Monthly(1, []int{31}, nineAM)},

		// ---- due: negative days-of-month count back from month end ----
		{label: "Monthly(1,[-1]) last day of month", spec: schedule.Monthly(1, []int{-1}, nineAM)},
		{label: "Monthly(1,[-31]) 1st of a 31-day month", spec: schedule.Monthly(1, []int{-31}, nineAM)},

		// ---- due: empty sets are substituted by the scheduler ----
		{label: "Monthly(1,nil) empty day set substitutes the 1st", spec: schedule.Monthly(1, nil, nineAM)},
		{label: "Weekly(1,nil) empty weekday set substitutes Sunday", spec: schedule.Weekly(1, nil, nineAM)},

		// ---- due: a weekday above Saturday stays a raw day offset ----
		{label: "Weekly(1,[Weekday(7)])", spec: schedule.Weekly(1, []time.Weekday{time.Weekday(7)}, nineAM)},
		{label: "Weekly(1,[Weekday(9)])", spec: schedule.Weekly(1, []time.Weekday{time.Weekday(9)}, nineAM)},

		// ---- due: a MIXED weekday set — hence ALL negative, never ANY negative ----
		{label: "Weekly(1,[Weekday(-1),Monday]) mixed set", spec: schedule.Weekly(1, []time.Weekday{time.Weekday(-1), time.Monday}, nineAM)},

		// ---- due: ordinary well-formed specs ----
		{label: "Weekly(1,[Mon])", spec: schedule.Weekly(1, []time.Weekday{time.Monday}, nineAM)},
		{label: "Daily(1,09:00)", spec: schedule.Daily(1, nineAM)},
		{label: "Daily(1) no at-times defaults to midnight", spec: schedule.Daily(1)},
		{label: "Every(1h)", spec: schedule.Every(time.Hour)},
		{label: "EveryRandom(5s,10s)", spec: schedule.EveryRandom(5*time.Second, 10*time.Second)},

		// ---- cron is OUT OF SCOPE for the predicate (ADR-0182 §3.2): every
		// KindCron spec reports false, including the two that genuinely never
		// fire. They are carried here so the third mutation below — a KindCron
		// branch returning true — has a due cron row to be caught by.
		{label: "Cron(unparseable) out of scope", spec: schedule.Cron("not a cron")},
		{label: "Cron(30 February) out of scope", spec: schedule.Cron("0 0 30 2 *")},
		{label: "Cron(weekdays 9am) genuinely due", spec: schedule.Cron("0 9 * * 1-5")},

		// ---- one-shot and unset forms ----
		{label: "AfterDuration(1h)", spec: schedule.AfterDuration(time.Hour)},
		{label: "At(fixed instant)", spec: schedule.At(time.Date(2026, time.August, 14, 9, 0, 0, 0, time.UTC))},
		{label: "zero value (KindUnset — never converts)", spec: schedule.TriggerSpec{}},
	}
}

// generatedNeverDueSweep builds the deterministic sweep: nested loops over
// small grids chosen to straddle the boundaries NeverDue actually tests, with
// no randomness and no fuzzing, so the same specs are produced on every run.
//
// The grids are boundary-straddling on purpose — interval at and either side of
// zero, day-of-month across ±31/±32 and zero, weekdays below Sunday and above
// Saturday, and clock fields at and one past their legal maxima — and every set
// grid includes MULTI-element sets: Weekly's rule is "len>0 && ALL negative", so
// singleton sets alone cannot tell it apart from "ANY negative", and Monthly's
// rule is "any bad day poisons the set".
func generatedNeverDueSweep() []crossCheckRow {
	intervals := []uint{0, 1, 2}
	rows := make([]crossCheckRow, 0, 1400)

	// Daily gets the full 3x3x3 clock grid: the at-time rule is shared by all
	// three calendar kinds, so it is swept exhaustively once here and sampled
	// for Weekly/Monthly, whose own grids carry the day/weekday dimension.
	for _, interval := range intervals {
		for _, at := range fullClockGrid() {
			rows = append(rows, crossCheckRow{
				label: fmt.Sprintf("sweep Daily(%d,%s)", interval, formatClockTimes(at)),
				spec:  schedule.Daily(interval, at...),
			})
		}
	}

	for _, interval := range intervals {
		for _, weekdays := range weekdaySets() {
			for _, at := range compactClockGrid() {
				rows = append(rows, crossCheckRow{
					label: fmt.Sprintf("sweep Weekly(%d,%s,%s)", interval, formatWeekdays(weekdays), formatClockTimes(at)),
					spec:  schedule.Weekly(interval, weekdays, at...),
				})
			}
		}
	}

	for _, interval := range intervals {
		for _, days := range monthDaySets() {
			for _, at := range compactClockGrid() {
				rows = append(rows, crossCheckRow{
					label: fmt.Sprintf("sweep Monthly(%d,%v,%s)", interval, days, formatClockTimes(at)),
					spec:  schedule.Monthly(interval, days, at...),
				})
			}
		}
	}

	for _, d := range []time.Duration{-time.Hour, -time.Nanosecond, 0, time.Nanosecond, time.Hour} {
		rows = append(rows, crossCheckRow{
			label: fmt.Sprintf("sweep Every(%s)", d),
			spec:  schedule.Every(d),
		})
	}

	bounds := []time.Duration{-time.Second, 0, time.Second, 5 * time.Second}
	for _, minimum := range bounds {
		for _, maximum := range bounds {
			rows = append(rows, crossCheckRow{
				label: fmt.Sprintf("sweep EveryRandom(%s,%s)", minimum, maximum),
				spec:  schedule.EveryRandom(minimum, maximum),
			})
		}
	}

	return rows
}

// fullClockGrid enumerates every combination of the boundary clock values, plus
// the omitted-at-times form (which the scheduler defaults to midnight).
func fullClockGrid() [][]schedule.ClockTime {
	hours := []uint{0, 23, 24}
	minutes := []uint{0, 59, 60}
	seconds := []uint{0, 59, 60}

	grid := [][]schedule.ClockTime{nil}
	for _, h := range hours {
		for _, m := range minutes {
			for _, s := range seconds {
				grid = append(grid, []schedule.ClockTime{{Hour: h, Minute: m, Second: s}})
			}
		}
	}
	return grid
}

// compactClockGrid samples the at-time dimension for the kinds whose own grid
// already spans days or weekdays: the omitted form, two legal times, one past
// each of the three field maxima, and a two-element set in which only the
// second at-time is out of range.
func compactClockGrid() [][]schedule.ClockTime {
	return [][]schedule.ClockTime{
		nil,
		{{Hour: 0}},
		{{Hour: 9, Minute: 30}},
		{{Hour: 24}},
		{{Minute: 60}},
		{{Second: 60}},
		{{Hour: 9}, {Hour: 24}},
	}
}

// weekdaySets enumerates the empty set, every singleton, and every unordered
// pair over the weekday grid. The pairs are what exercise "ALL negative": a
// mixed set such as {-1, Saturday} is due.
func weekdaySets() [][]time.Weekday {
	values := []time.Weekday{-3, -1, 0, 6, 7, 9}

	sets := [][]time.Weekday{nil}
	for i, a := range values {
		sets = append(sets, []time.Weekday{a})
		for _, b := range values[i+1:] {
			sets = append(sets, []time.Weekday{a, b})
		}
	}
	return sets
}

// monthDaySets enumerates the empty set, every singleton, and every unordered
// pair over the day-of-month grid, which straddles both legal ranges (1..31 and
// -31..-1) and every value just outside them.
func monthDaySets() [][]int {
	values := []int{-32, -31, -1, 0, 1, 28, 31, 32}

	sets := [][]int{nil}
	for i, a := range values {
		sets = append(sets, []int{a})
		for _, b := range values[i+1:] {
			sets = append(sets, []int{a, b})
		}
	}
	return sets
}

// formatWeekdays renders a weekday set as raw integers. time.Weekday.String
// formats an out-of-range value through an UNSIGNED conversion, so a swept
// weekday of -3 would otherwise appear in a failure message as
// "%!Weekday(18446744073709551613)" and the failing row would be unreadable.
func formatWeekdays(ws []time.Weekday) string {
	if len(ws) == 0 {
		return "[]"
	}
	out := "["
	for i, w := range ws {
		if i > 0 {
			out += " "
		}
		out += fmt.Sprintf("%d", int(w))
	}
	return out + "]"
}

// formatClockTimes renders an at-time set into a stable label.
func formatClockTimes(cs []schedule.ClockTime) string {
	if len(cs) == 0 {
		return "[]"
	}
	out := "["
	for i, c := range cs {
		if i > 0 {
			out += " "
		}
		out += fmt.Sprintf("%02d:%02d:%02d", c.Hour, c.Minute, c.Second)
	}
	return out + "]"
}
