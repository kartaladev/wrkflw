# Plan 1/3 — `schedule.TriggerSpec` (full gocron parity) + wire + options + engine resolve

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Introduce the gocron-neutral, serializable `schedule.TriggerSpec` (all ten trigger forms) and migrate every timer/deadline/reminder option, model field, wire encoding, and engine arm site onto it — **behavior-preserving**: forms that reduce to today's `FireAt` model (`AfterDuration`/`At`/`AfterExpr`, and `Every`/`EveryExpr` via the existing reminder path) work now; the natively-recurring forms (`Cron`/`Daily`/`Weekly`/`Monthly`/`EveryRandom`) are authorable and serializable but return `engine.ErrUnsupportedTrigger` until the scheduler lands (Plan 2).

**Architecture:** New leaf package `definition/schedule`. Model duration fields become `TriggerSpec`. Wire serializes a nested, discriminated `TriggerWire` object per timer slot, with backward-compatible decode of the old flat string fields (→ `AfterExpr`/`EveryExpr`). The engine resolves the two dynamic-expr forms via the existing `EvalDuration` and, for this plan, converts a resolved one-shot into today's `ScheduleTimer{FireAt}`; recurring `Every` keeps today's engine-reschedule path.

**Tech Stack:** Go 1.25, `expr-lang/expr` (via `internal/expreval`, reused). No `gocron`/`robfig/cron` in this plan.

## Global Constraints

- `go build ./...`, `go test ./...`, `golangci-lint run ./...` clean before done.
- **Strict TDD**: visible failing `go test ./<pkg>/...` before each new symbol (CLAUDE.md TDD discipline).
- Black-box tests (`package <x>_test`); `table-test` skill for 2+ cases; `t.Context()`.
- Never import `gocron`/`clockwork`/`robfig/cron` from engine/definition code.
- Breaking API allowed (pre-v1.0); migrate all call sites in the task that breaks them so the tree compiles at task boundaries.
- Touched packages ≥ 85% coverage; new public symbols carry godoc; `definition/schedule` ships a testable `Example`.
- Module path `github.com/kartaladev/wrkflw`. This is **Plan 1 of 3** (see revised spec `docs/specs/2026-07-07-typed-trigger-spec-and-cron.md`, ADR-0102). Plan 2 = scheduler port + gocron + MemScheduler; Plan 3 = JobStore + persistence + consistency + rehydration + ADR-0102 (supersedes ADR-0027 timer-write).

---

## File Structure

- **Create** `definition/schedule/trigger.go` — `TriggerSpec`, `Kind`, `ClockTime`, the ten constructors, accessors (`Kind`/`IsZero`/`Recurring`/typed getters), `FromWire`/`ToWire` helpers.
- **Create** `definition/schedule/{trigger_test.go, example_test.go}`.
- **Modify** `definition/model/node.go` — `WaitFields` deadline/reminder → `TriggerSpec`.
- **Create** `definition/model/trigger_wire.go` — `TriggerWire` + `PutTrigger`/`ReadTrigger` (nested encode/decode, flat-string back-compat).
- **Modify** `definition/model/node_wire.go` — add `TimerTrigger`/`DeadlineTrigger`/`ReminderTrigger *TriggerWire`; update `PutActivity`/`Activity`/`PutWait`/`Wait`.
- **Modify** `definition/model/accessors.go` — `DeadlineOf`/`ReminderOf` return `TriggerSpec`.
- **Modify** `definition/event/event.go` — event `TimerDuration string` → `Timer TriggerSpec`; three NodeSpecs.
- **Modify** `definition/event/options.go`, `definition/activity/options.go` — timer/deadline/reminder options take `TriggerSpec`.
- **Create** `engine/trigger_resolve.go` — `ResolveTrigger` + `ErrUnsupportedTrigger`.
- **Modify** engine arm sites: `engine/step_boundaries.go`, `engine/step_timers.go`, `engine/step_nodes.go`, `engine/step_eventsubprocess.go`.
- **Migrate** all `*_test.go` + `examples/` call sites.

---

## Task 1: `schedule` package — `TriggerSpec`, `Kind`, `ClockTime`, constructors, accessors

**Files:** Create `definition/schedule/trigger.go`, `definition/schedule/trigger_test.go`.

**Interfaces — Produces:**
- `type Kind uint8` with `KindUnset, KindOneTime, KindDuration, KindDurationRand, KindCron, KindDaily, KindWeekly, KindMonthly, KindExpr, KindEveryExpr`.
- `type ClockTime struct{ Hour, Minute, Second uint }`.
- `type TriggerSpec struct{...}` (unexported fields).
- Constructors: `AfterDuration(time.Duration)`, `At(time.Time)`, `AfterExpr(string)`, `Every(time.Duration)`, `EveryExpr(string)`, `EveryRandom(min, max time.Duration)`, `Cron(string)`, `Daily(interval uint, at ...ClockTime)`, `Weekly(interval uint, days []time.Weekday, at ...ClockTime)`, `Monthly(interval uint, days []int, at ...ClockTime)`.
- Accessors: `Kind() Kind`, `IsZero() bool`, `Recurring() bool`, and typed getters `Duration() (time.Duration, bool)`, `AbsTime() (time.Time, bool)`, `Expr() (string, Kind, bool)`, `CronExpr() (string, bool)`, `Random() (min, max time.Duration, bool)`, `Calendar() (interval uint, days []int, weekdays []time.Weekday, at []ClockTime, ok bool)`.

- [ ] **Step 1: Write the failing test**

```go
// definition/schedule/trigger_test.go
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
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./definition/schedule/...`
Expected: FAIL — package/symbols undefined.

- [ ] **Step 3: Implement `definition/schedule/trigger.go`**

```go
// Package schedule defines TriggerSpec — the gocron-neutral, serializable
// "when a timer fires" value used by activity deadlines/reminders and timer
// events. Exactly one form is set; the zero value is unset. TriggerSpec never
// imports gocron; the scheduler adapter maps it to a gocron JobDefinition.
package schedule

import "time"

type Kind uint8

const (
	KindUnset Kind = iota
	KindOneTime      // AfterDuration / At
	KindDuration     // Every
	KindDurationRand // EveryRandom
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
```

- [ ] **Step 4: Run to verify it passes**

Run: `go test ./definition/schedule/...`
Expected: PASS.

- [ ] **Step 5: Add Example, run, commit**

```go
// definition/schedule/example_test.go
package schedule_test

import (
	"fmt"
	"time"

	"github.com/kartaladev/wrkflw/definition/schedule"
)

func ExampleAfterDuration() {
	d, _ := schedule.AfterDuration(90 * time.Minute).Duration()
	fmt.Println(d)
	// Output: 1h30m0s
}
```

Run: `go test ./definition/schedule/...` (PASS), then:

```bash
git add definition/schedule/
git commit -m "feat(schedule): TriggerSpec with full gocron trigger parity"
```

---

## Task 2: `TriggerWire` nested encoding + back-compat decode

**Files:** Create `definition/model/trigger_wire.go`, `definition/model/trigger_wire_test.go`.

**Interfaces:**
- Consumes: `schedule.TriggerSpec` + accessors (Task 1).
- Produces:
  - `type TriggerWire struct{ Kind string; Nanos int64; At *time.Time; Expr string; Cron string; MinNanos, MaxNanos int64; Interval uint; AtTimes []schedule.ClockTime; Weekdays []int; DaysOfMonth []int }` with JSON tags.
  - `func PutTrigger(s schedule.TriggerSpec) *TriggerWire` (nil when `s.IsZero()`).
  - `func ReadTrigger(w *TriggerWire, flatExpr string, recurringFlat bool) schedule.TriggerSpec` — decodes the nested form; when `w == nil` but `flatExpr != ""`, returns `AfterExpr(flatExpr)` (or `EveryExpr` when `recurringFlat`).

- [ ] **Step 1: Failing test**

```go
// definition/model/trigger_wire_test.go
package model_test

import (
	"testing"
	"time"

	"github.com/kartaladev/wrkflw/definition/model"
	"github.com/kartaladev/wrkflw/definition/schedule"
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
```

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./definition/model/... -run TestTriggerWire`
Expected: FAIL — `model.TriggerWire`/`PutTrigger`/`ReadTrigger` undefined.

- [ ] **Step 3: Implement `definition/model/trigger_wire.go`**

```go
package model

import (
	"time"

	"github.com/kartaladev/wrkflw/definition/schedule"
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
```

- [ ] **Step 4: Run to verify it passes**

Run: `go test ./definition/model/... -run TestTriggerWire`
Expected: PASS. `go build ./...` still green (additive).

- [ ] **Step 5: Commit**

```bash
git add definition/model/trigger_wire.go definition/model/trigger_wire_test.go
git commit -m "feat(model): TriggerWire nested encoding + legacy flat-string decode"
```

---

## Task 3: Migrate `WaitFields` (deadline/reminder) + `NodeWire` slots + accessors + options

Atomic type change (compiles at task end): `WaitFields.DeadlineDuration/ReminderEvery string` → `TriggerSpec`; `NodeWire` gains `TimerTrigger/DeadlineTrigger/ReminderTrigger *TriggerWire`; `PutActivity/Activity/PutWait/Wait`, `DeadlineOf/ReminderOf`, and the activity/catch options migrate together.

**Files:** `definition/model/node.go`, `definition/model/node_wire.go`, `definition/model/accessors.go`, `definition/activity/options.go`, `definition/event/options.go`; adjust `definition/model/accessors_test.go`.

**Interfaces — Produces:**
- `WaitFields{ DeadlineTimer schedule.TriggerSpec; DeadlineFlow, DeadlineAction string; ReminderEvery schedule.TriggerSpec; ReminderAction string }`.
- `DeadlineOf(n) (schedule.TriggerSpec, string, string)`, `ReminderOf(n) (schedule.TriggerSpec, string)`.
- `activity.WithDeadline(t schedule.TriggerSpec, flowID, action string)`, `activity.WithReminder(t schedule.TriggerSpec, action string)`, `event.WithCatchDeadline(t, flowID, action)`, `event.WithCatchReminder(t, action)`.

- [ ] **Step 1: Adjust the accessor test (red)**

```go
// definition/model/accessors_test.go — replace deadline/reminder assertions
func TestDeadlineReminderTyped(t *testing.T) {
	n := activity.NewUserTask("ut", nil,
		activity.WithDeadline(schedule.AfterDuration(2*time.Hour), "sla", "notify"),
		activity.WithReminder(schedule.Every(time.Hour), "remind"),
	)
	spec, flow, action := model.DeadlineOf(n)
	if d, ok := spec.Duration(); !ok || d != 2*time.Hour || flow != "sla" || action != "notify" {
		t.Fatalf("DeadlineOf = %v %q %q", d, flow, action)
	}
	every, ra := model.ReminderOf(n)
	if d, ok := every.Duration(); !ok || d != time.Hour || ra != "remind" {
		t.Fatalf("ReminderOf = %v %q", d, ra)
	}
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./definition/model/... -run TestDeadlineReminderTyped`
Expected: FAIL — options take strings; accessors return strings.

- [ ] **Step 3: Apply the migration**

`definition/model/node.go` — `WaitFields` (add `schedule` import):

```go
type WaitFields struct {
	DeadlineTimer  schedule.TriggerSpec
	DeadlineFlow   string
	DeadlineAction string
	ReminderEvery  schedule.TriggerSpec
	ReminderAction string
}
func (w WaitFields) deadline() (schedule.TriggerSpec, string, string) {
	return w.DeadlineTimer, w.DeadlineFlow, w.DeadlineAction
}
func (w WaitFields) reminder() (schedule.TriggerSpec, string) { return w.ReminderEvery, w.ReminderAction }
```

`definition/model/accessors.go`:

```go
func DeadlineOf(n Node) (schedule.TriggerSpec, string, string) {
	if w, ok := n.(interface {
		deadline() (schedule.TriggerSpec, string, string)
	}); ok {
		return w.deadline()
	}
	return schedule.TriggerSpec{}, "", ""
}
func ReminderOf(n Node) (schedule.TriggerSpec, string) {
	if w, ok := n.(interface {
		reminder() (schedule.TriggerSpec, string)
	}); ok {
		return w.reminder()
	}
	return schedule.TriggerSpec{}, ""
}
```

`definition/model/node_wire.go` — remove the three flat string timer fields (`TimerDuration`, `DeadlineDuration`, `ReminderEvery`) **but keep them decodable** by adding `//nolint`-free legacy fields OR retain them for back-compat read. Concretely, KEEP the flat string fields for legacy decode and ADD the nested pointers:

```go
	// legacy flat forms (decoded via ReadTrigger's flatExpr path; not written by ToWire)
	TimerDuration    string       `json:"timerDuration,omitempty"`
	DeadlineDuration string       `json:"deadlineDuration,omitempty"`
	ReminderEvery    string       `json:"reminderEvery,omitempty"`
	// nested trigger forms (canonical)
	TimerTrigger     *TriggerWire `json:"timerTrigger,omitempty"`
	DeadlineTrigger  *TriggerWire `json:"deadlineTrigger,omitempty"`
	ReminderTrigger  *TriggerWire `json:"reminderTrigger,omitempty"`
```

Update the wait helpers:

```go
func (w *NodeWire) PutActivity(a ActivityFields) {
	w.RetryPolicy, w.RecoveryFlow = a.RetryPolicy, a.RecoveryFlow
	w.CompensationAction, w.CancelHandler = a.CompensationAction, a.CancelHandler
	w.PutWait(a.WaitFields)
}
func (w NodeWire) Activity() ActivityFields {
	return ActivityFields{WaitFields: w.Wait(), RetryPolicy: w.RetryPolicy, RecoveryFlow: w.RecoveryFlow, CompensationAction: w.CompensationAction, CancelHandler: w.CancelHandler}
}
func (w *NodeWire) PutWait(a WaitFields) {
	w.DeadlineTrigger = PutTrigger(a.DeadlineTimer)
	w.DeadlineFlow, w.DeadlineAction = a.DeadlineFlow, a.DeadlineAction
	w.ReminderTrigger = PutTrigger(a.ReminderEvery)
	w.ReminderAction = a.ReminderAction
}
func (w NodeWire) Wait() WaitFields {
	return WaitFields{
		DeadlineTimer:  ReadTrigger(w.DeadlineTrigger, w.DeadlineDuration, false),
		DeadlineFlow:   w.DeadlineFlow,
		DeadlineAction: w.DeadlineAction,
		ReminderEvery:  ReadTrigger(w.ReminderTrigger, w.ReminderEvery, true),
		ReminderAction: w.ReminderAction,
	}
}
```

`definition/activity/options.go` (add `schedule` import):

```go
func WithDeadline(t schedule.TriggerSpec, flowID, action string) activityOnlyOption {
	return withActivity(func(a *model.ActivityFields) { a.DeadlineTimer, a.DeadlineFlow, a.DeadlineAction = t, flowID, action })
}
func WithReminder(t schedule.TriggerSpec, action string) activityOnlyOption {
	return withActivity(func(a *model.ActivityFields) { a.ReminderEvery, a.ReminderAction = t, action })
}
```

`definition/event/options.go` (add `schedule` import):

```go
func WithCatchDeadline(t schedule.TriggerSpec, flowID, action string) CatchOption {
	return catchFuncOpt{func(n *IntermediateCatchEvent) { n.DeadlineTimer, n.DeadlineFlow, n.DeadlineAction = t, flowID, action }}
}
func WithCatchReminder(t schedule.TriggerSpec, action string) CatchOption {
	return catchFuncOpt{func(n *IntermediateCatchEvent) { n.ReminderEvery, n.ReminderAction = t, action }}
}
```

- [ ] **Step 4: Run tests**

Run: `go test ./definition/model/... -run TestDeadlineReminderTyped`
Expected: PASS. Other packages may not yet compile (event structs Task 4, call sites Task 6) — that's expected.

- [ ] **Step 5: Commit**

```bash
git add definition/model/ definition/activity/options.go definition/event/options.go
git commit -m "refactor(model): WaitFields deadline/reminder → schedule.TriggerSpec (nested wire)"
```

---

## Task 4: Migrate event `Timer` fields + NodeSpecs + timer options

**Files:** `definition/event/event.go`, `definition/event/options.go`; adjust `definition/event/event_test.go`.

**Interfaces — Produces:** `StartEvent.Timer`, `IntermediateCatchEvent.Timer`, `BoundaryEvent.Timer` (all `schedule.TriggerSpec`); `event.WithStartTimer(TriggerSpec)`, `WithCatchTimer(TriggerSpec)`, `WithBoundaryTimer(TriggerSpec)`.

- [ ] **Step 1: Adjust event test (red)** — assert `WithBoundaryTimer(schedule.AfterDuration(time.Hour))` sets `Timer`, and a full-definition JSON round-trip preserves it (use the round-trip helper already in `definition/model/definition_test.go`; if the event package lacks one, add the assertion at the definition level in a new `definition/event/wire_test.go` that marshals a one-node def and unmarshals it).

- [ ] **Step 2: Run to verify it fails** — `go test ./definition/event/...` → FAIL (`Timer` undefined / option takes string).

- [ ] **Step 3: Implement** — in `event.go` replace `TimerDuration string` with `Timer schedule.TriggerSpec` in the three structs (add `schedule` import); update the three NodeSpecs:

```go
// startEvent
FromWire: func(b model.Base, w model.NodeWire) model.Node {
	return StartEvent{Base: b, SignalName: w.SignalName, MessageName: w.MessageName, CorrelationKey: w.CorrelationKey,
		Timer: model.ReadTrigger(w.TimerTrigger, w.TimerDuration, false)}
},
ToWire: func(n model.Node, w *model.NodeWire) {
	v := n.(StartEvent)
	w.SignalName, w.MessageName, w.CorrelationKey = v.SignalName, v.MessageName, v.CorrelationKey
	w.TimerTrigger = model.PutTrigger(v.Timer)
},
// intermediateCatchEvent: same, plus w.PutWait(v.WaitFields) in ToWire and WaitFields: w.Wait() in FromWire (as today)
// boundaryEvent: same Timer handling; keep AttachedTo/NonInterrupting/ErrorCode/Signal/Message/CorrelationKey
```

In `options.go`:

```go
func WithStartTimer(t schedule.TriggerSpec) StartOption {
	return startFuncOpt{func(n *StartEvent) { n.Timer = t }}
}
func WithCatchTimer(t schedule.TriggerSpec) CatchOption {
	return catchFuncOpt{func(n *IntermediateCatchEvent) { n.Timer = t }}
}
func WithBoundaryTimer(t schedule.TriggerSpec) BoundaryOption {
	return boundaryFuncOpt{func(n *BoundaryEvent) { n.Timer = t }}
}
```

- [ ] **Step 4: Run** — `go test ./definition/event/...` → PASS (engine/call-site failures remain until Tasks 5–6).

- [ ] **Step 5: Commit**

```bash
git add definition/event/
git commit -m "refactor(event): timer fields → schedule.TriggerSpec (nested wire)"
```

---

## Task 5: `engine.ResolveTrigger` + arm-site migration (behavior-preserving)

`ResolveTrigger` resolves the two expr forms and passes native forms through. For THIS plan, the arm sites still emit today's `ScheduleTimer{FireAt}` for the forms that reduce to a one-shot delay (`AfterDuration`/`At`/`AfterExpr`, and — via the existing reminder path — `Every`/`EveryExpr` as an interval); non-reducible forms (`Cron`/`Daily`/`Weekly`/`Monthly`/`EveryRandom`) return `ErrUnsupportedTrigger` (wired live in Plan 2).

**Files:** Create `engine/trigger_resolve.go`, `engine/trigger_resolve_test.go`; modify `engine/step_boundaries.go`, `engine/step_timers.go`, `engine/step_nodes.go`, `engine/step_eventsubprocess.go`; adjust affected engine tests.

**Interfaces — Produces:**
- `var ErrUnsupportedTrigger = errors.New("workflow-engine: trigger kind needs a native scheduler (not available in this build)")`.
- `func ResolveTrigger(eval ConditionEvaluator, spec schedule.TriggerSpec, env map[string]any) (schedule.TriggerSpec, error)` — `AfterExpr`→`AfterDuration(EvalDuration)`, `EveryExpr`→`Every(EvalDuration)`, else unchanged.
- `func triggerDelay(spec schedule.TriggerSpec, now time.Time) (time.Duration, error)` — one-shot delay for the reducible forms (`AfterDuration`→d; `At`→`at.Sub(now)`; `Every`→d as the reminder interval); returns `ErrUnsupportedTrigger` for cron/calendar/random.

- [ ] **Step 1: Failing test**

```go
// engine/trigger_resolve_test.go
package engine_test

import (
	"errors"
	"testing"
	"time"

	"github.com/kartaladev/wrkflw/definition/schedule"
	"github.com/kartaladev/wrkflw/engine"
	"github.com/kartaladev/wrkflw/internal/expreval"
)

func TestResolveTrigger(t *testing.T) {
	ev := expreval.New()
	if r, err := engine.ResolveTrigger(ev, schedule.AfterExpr(`h * 3600`), map[string]any{"h": 2}); err != nil {
		t.Fatal(err)
	} else if d, ok := r.Duration(); !ok || d != 2*time.Hour {
		t.Fatalf("resolved = %v %v", d, ok)
	}
	if r, err := engine.ResolveTrigger(ev, schedule.EveryExpr(`"1h"`), nil); err != nil {
		t.Fatal(err)
	} else if r.Kind() != schedule.KindDuration {
		t.Fatalf("everyExpr kind = %d", r.Kind())
	}
	if r, _ := engine.ResolveTrigger(ev, schedule.Cron(`0 9 * * *`), nil); r.Kind() != schedule.KindCron {
		t.Fatal("cron must pass through unchanged")
	}
	if _, err := engine.ResolveTrigger(ev, schedule.Cron(`x`), nil); err != nil {
		t.Fatalf("resolve must not fail for native forms: %v", err)
	}
	if _, err := engineTriggerDelayCron(); !errors.Is(err, engine.ErrUnsupportedTrigger) {
		t.Fatalf("cron delay must be ErrUnsupportedTrigger, got %v", err)
	}
}
```

> `engineTriggerDelayCron` is a tiny test helper calling an exported test seam or, simpler, assert `ErrUnsupportedTrigger` through a boundary-arm step with a cron timer (see Step 3). Prefer asserting via `armBoundaries` behavior if `triggerDelay` stays unexported.

- [ ] **Step 2: Run to verify it fails** — `go test ./engine/... -run TestResolveTrigger` → FAIL (undefined).

- [ ] **Step 3: Implement `engine/trigger_resolve.go`**

```go
package engine

import (
	"errors"
	"time"

	"github.com/kartaladev/wrkflw/definition/schedule"
)

var ErrUnsupportedTrigger = errors.New("workflow-engine: trigger kind needs a native scheduler (not available in this build)")

// ResolveTrigger resolves the dynamic expr forms to concrete durations and
// returns all other forms unchanged. Reuses EvalDuration for the expr path.
func ResolveTrigger(eval ConditionEvaluator, spec schedule.TriggerSpec, env map[string]any) (schedule.TriggerSpec, error) {
	code, kind, ok := spec.Expr()
	if !ok {
		return spec, nil
	}
	d, err := eval.EvalDuration(code, env)
	if err != nil {
		return schedule.TriggerSpec{}, err
	}
	if kind == schedule.KindEveryExpr {
		return schedule.Every(d), nil
	}
	return schedule.AfterDuration(d), nil
}

// triggerDelay returns the one-shot delay for a reducible trigger. Native
// recurring/calendar forms return ErrUnsupportedTrigger (Plan 2 wires them).
func triggerDelay(spec schedule.TriggerSpec, now time.Time) (time.Duration, error) {
	if d, ok := spec.Duration(); ok { // AfterDuration or Every (interval)
		return d, nil
	}
	if at, ok := spec.AbsTime(); ok {
		return at.Sub(now), nil
	}
	return 0, ErrUnsupportedTrigger
}
```

Then migrate each arm site: replace `if X.Timer/spec != "" { dur := EvalDuration(...) }` with:

```go
spec, err := ResolveTrigger(eval, X.Timer /* or model.DeadlineOf(node)/ReminderOf(node) */, s.Variables)
if err != nil { return ...fmt.Errorf(...) }
if !spec.IsZero() {
	dur, err := triggerDelay(spec, at)
	if err != nil { return ...fmt.Errorf("workflow-engine: %q: %w", node.ID(), err) }
	// unchanged: nextTimerID + ScheduleTimer{FireAt: at.Add(dur), Kind: ...}
}
```

Sites: `step_boundaries.go` armBoundaries (`n.Timer`); `step_nodes.go` deadline (`model.DeadlineOf(node)`), reminder (`model.ReminderOf(node)`), ICE (`ice.Timer`), event-gateway (`ce.Timer`); `step_eventsubprocess.go` (`se.Timer`); `step_timers.go` handleReminderFired (re-read via `model.ReminderOf(node)` → `ResolveTrigger` → `triggerDelay`, keep the reschedule as today for `Every`).

- [ ] **Step 4: Run** — `go test ./engine/...` → the resolve test passes; adjust engine timer tests that referenced string durations to use `schedule.AfterExpr(...)`/`AfterDuration(...)`.

- [ ] **Step 5: Commit**

```bash
git add engine/
git commit -m "feat(engine): ResolveTrigger + arm sites on TriggerSpec (native forms deferred)"
```

---

## Task 6: Migrate all remaining call sites + full green

Wrap every existing duration literal in the appropriate constructor. Existing tests used **expr strings** → wrap in `schedule.AfterExpr(...)` (deadlines/timers) or `schedule.EveryExpr(...)` (reminders) to keep byte-identical wire + behavior. Examples may use typed `AfterDuration`/`Every`.

**Files:** the ~30 sites from the codebase scan (`runtime/*_test.go`, `internal/persistence/store/definitions_conformance_test.go`, `definition/**/*_test.go`, `engine/*_test.go`, `examples/scenarios/{boundary_timer,inwait_reminder,timer_boundary,...}`). Add the `schedule` import (and `time` where `AfterDuration`/`Every` used).

- [ ] **Step 1: Migrate** — e.g. `activity.WithDeadline(\`"30m"\`, "esc", "notify")` → `activity.WithDeadline(schedule.AfterExpr(\`"30m"\`), "esc", "notify")`; `activity.WithReminder(\`"1h"\`, "remind")` → `activity.WithReminder(schedule.EveryExpr(\`"1h"\`), "remind")`; `event.WithBoundaryTimer(\`"30m"\`)` → `event.WithBoundaryTimer(schedule.AfterExpr(\`"30m"\`))`. Examples: prefer `schedule.AfterDuration(time.Hour)` / `schedule.Every(30*time.Minute)`.

- [ ] **Step 2: Build + test**

Run: `go build ./... && go test ./...`
Expected: PASS. Verify no missed sites:
Run: `grep -rn "WithDeadline(\\|WithReminder(\\|WithBoundaryTimer(\\|WithCatchTimer(\\|WithStartTimer(\\|WithCatchDeadline(\\|WithCatchReminder(" --include=*.go . | grep -v "schedule\\.\\|func With"`
Expected: no output.

- [ ] **Step 3: Run migrated examples** — `go run ./examples/scenarios/timer_boundary/ && go run ./examples/scenarios/inwait_reminder/` → same output as before. Remove stray binaries (`rm -f timer_boundary inwait_reminder`).

- [ ] **Step 4: Coverage + lint** — `go test -race -coverprofile=cover.out ./definition/... ./engine/... && go tool cover -func=cover.out | tail -1` (≥85%); `golangci-lint run ./...` (0 issues).

- [ ] **Step 5: Commit**

```bash
git add -A
git commit -m "refactor: migrate all timer/deadline/reminder call sites to schedule.TriggerSpec"
```

---

## Self-Review (spec coverage for Plan 1)

- `TriggerSpec` full parity → Task 1. Nested wire + legacy decode → Tasks 2–4. Options/model/accessors → Tasks 3–4. Engine `ResolveTrigger` (expr resolved; natives pass through) → Task 5. Call sites/examples → Task 6.
- **Deferred to Plan 2/3 (documented):** native scheduling of `Cron`/`Daily`/`Weekly`/`Monthly`/`EveryRandom` (`ErrUnsupportedTrigger` here); scheduler port redesign; `JobStore`; persistence schema; consistency; self-rehydration; ADR-0102 (which will record superseding ADR-0027's timer-write portion). Plan 1 is behavior-preserving and independently shippable.
