# Typed `schedule.TriggerSpec` (duration + expr) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace every string-typed timer/deadline/reminder duration in a process definition with a typed `schedule.TriggerSpec` value (static `AfterDuration` or dynamic `AfterExpr`), preserving today's expr-lang dynamic durations and keeping the wire format backward-compatible.

**Architecture:** A new leaf package `definition/schedule` holds `TriggerSpec` (one of: static `time.Duration`, expr-lang string, or — reserved for Plan B — a cron string) built by `AfterDuration`/`AfterExpr`/`Cron`. The model's string duration fields become `TriggerSpec`. The engine resolves a `TriggerSpec` to a `time.Duration` at arm time via a single helper: static durations pass through; expr strings go through the existing `EvalDuration`. The wire keeps the existing `*Duration`/`*Every` string field as the **expr** form and adds `*DurationNanos`/`*Cron` companions, so old definitions still load (as expr).

**Tech Stack:** Go 1.25, `expr-lang/expr` (via `internal/expreval`, reused unchanged), `clockwork` clock (unchanged). Cron (`gocron`) is **out of scope for this plan** — the `Cron` form and `*Cron` wire fields are added as inert storage here and wired to the scheduler in Plan B.

## Global Constraints

- Go 1.25; `go build ./...`, `go test ./...`, `golangci-lint run ./...` all clean before done.
- **Strict TDD**: no production code before a visible failing test (`go test ./<pkg>/...` showing red) — see CLAUDE.md "TDD Operational Discipline".
- Prefer **black-box tests** (`package <x>_test`). Use the project `table-test` skill for 2+ cases sharing a SUT call (assert-closure form, `t.Context()`).
- **Never import `clockwork`/`gocron` from engine/workflow code** — engine reaches durations only through `ConditionEvaluator`.
- expr-lang stays the mechanism for dynamic durations (`internal/expreval.EvalDuration` reused verbatim); do not hand-roll parsing.
- Touched packages keep **≥ 85%** line coverage.
- Breaking API changes are allowed (pre-v1.0); update ALL call sites in the same task that breaks them so the tree always compiles at task boundaries.
- New public symbols carry godoc; `definition/schedule` is library-consumed → include a testable `Example`.
- Module path: `github.com/zakyalvan/krtlwrkflw`. Next free ADR: **0102** (written in the final task).

---

## File Structure

- **Create** `definition/schedule/trigger.go` — `TriggerSpec` type, `AfterDuration`/`AfterExpr`/`Cron`, accessors, `FromWire`.
- **Create** `definition/schedule/trigger_test.go` + `example_test.go`.
- **Modify** `definition/model/node.go` — `WaitFields` string fields → `TriggerSpec`; carrier method return types.
- **Modify** `definition/model/node_wire.go` — add wire companion fields; `PutActivity`/`Activity`/`PutWait`/`Wait` encode/decode `TriggerSpec`.
- **Modify** `definition/model/accessors.go` — `DeadlineOf`/`ReminderOf` return `TriggerSpec`.
- **Modify** `definition/event/event.go` — `StartEvent`/`IntermediateCatchEvent`/`BoundaryEvent` `TimerDuration string` → `Timer schedule.TriggerSpec`; NodeSpec `ToWire`/`FromWire`.
- **Modify** `definition/event/options.go` — `WithStartTimer`/`WithCatchTimer`/`WithCatchDeadline`/`WithCatchReminder`/`WithBoundaryTimer` take `TriggerSpec`.
- **Modify** `definition/activity/options.go` — `WithDeadline`/`WithReminder` take `TriggerSpec`.
- **Modify** `engine/conditions.go` — add `ResolveTrigger` helper (or free function in engine) resolving a `TriggerSpec` → `time.Duration`.
- **Modify** engine arm sites: `engine/step_boundaries.go`, `engine/step_timers.go`, `engine/step_nodes.go`, `engine/step_eventsubprocess.go`.
- **Migrate** all `*_test.go` and `examples/` call sites of the changed options.
- **Create** `docs/adr/0102-typed-trigger-spec.md`.

---

## Task 1: `schedule.TriggerSpec` value type

**Files:**
- Create: `definition/schedule/trigger.go`
- Test: `definition/schedule/trigger_test.go`

**Interfaces:**
- Produces:
  - `type TriggerSpec struct{ ... }` (unexported fields)
  - `func AfterDuration(d time.Duration) TriggerSpec`
  - `func AfterExpr(code string) TriggerSpec`
  - `func Cron(expr string) TriggerSpec`
  - `func FromWire(nanos int64, expr, cron string) TriggerSpec`
  - `func (s TriggerSpec) IsZero() bool`
  - `func (s TriggerSpec) Duration() (time.Duration, bool)`
  - `func (s TriggerSpec) Expr() (string, bool)`
  - `func (s TriggerSpec) Cron() (string, bool)`
  - `func (s TriggerSpec) Nanos() int64` (for wire; 0 unless static)

- [ ] **Step 1: Write the failing test**

```go
// definition/schedule/trigger_test.go
package schedule_test

import (
	"testing"
	"time"

	"github.com/zakyalvan/krtlwrkflw/definition/schedule"
)

func TestTriggerSpecForms(t *testing.T) {
	t.Run("zero is unset", func(t *testing.T) {
		var s schedule.TriggerSpec
		if !s.IsZero() {
			t.Fatal("zero TriggerSpec must be IsZero")
		}
	})

	t.Run("AfterDuration", func(t *testing.T) {
		s := schedule.AfterDuration(90 * time.Minute)
		if s.IsZero() {
			t.Fatal("must not be zero")
		}
		d, ok := s.Duration()
		if !ok || d != 90*time.Minute {
			t.Fatalf("Duration() = %v, %v; want 90m, true", d, ok)
		}
		if s.Nanos() != int64(90*time.Minute) {
			t.Fatalf("Nanos() = %d", s.Nanos())
		}
		if _, ok := s.Expr(); ok {
			t.Fatal("Expr() must be false for a duration spec")
		}
	})

	t.Run("AfterExpr", func(t *testing.T) {
		s := schedule.AfterExpr(`slaHours * 3600`)
		e, ok := s.Expr()
		if !ok || e != `slaHours * 3600` {
			t.Fatalf("Expr() = %q, %v", e, ok)
		}
		if _, ok := s.Duration(); ok {
			t.Fatal("Duration() must be false for an expr spec")
		}
	})

	t.Run("Cron", func(t *testing.T) {
		s := schedule.Cron(`0 9 * * *`)
		c, ok := s.Cron()
		if !ok || c != `0 9 * * *` {
			t.Fatalf("Cron() = %q, %v", c, ok)
		}
	})

	t.Run("FromWire round-trips each form", func(t *testing.T) {
		if d, ok := schedule.FromWire(int64(time.Hour), "", "").Duration(); !ok || d != time.Hour {
			t.Fatalf("nanos form = %v, %v", d, ok)
		}
		if e, ok := schedule.FromWire(0, "3h", "").Expr(); !ok || e != "3h" {
			t.Fatalf("expr form = %q, %v", e, ok)
		}
		if c, ok := schedule.FromWire(0, "", "0 9 * * *").Cron(); !ok || c != "0 9 * * *" {
			t.Fatalf("cron form = %q, %v", c, ok)
		}
	})
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./definition/schedule/...`
Expected: FAIL — `package .../definition/schedule` does not exist / undefined symbols.

- [ ] **Step 3: Write minimal implementation**

```go
// definition/schedule/trigger.go

// Package schedule defines TriggerSpec, the typed "when a timer fires"
// value used by activity deadlines/reminders and by timer events. Exactly one
// of three forms is set: a static duration (AfterDuration), an expr-lang
// expression evaluated to a duration at runtime (AfterExpr), or a cron
// expression (Cron). The zero value is "unset".
package schedule

import "time"

// TriggerSpec specifies WHEN a timer fires. Build it with AfterDuration,
// AfterExpr, or Cron. The zero value is unset (IsZero reports true).
type TriggerSpec struct {
	dur  time.Duration // static, relative delay; used when > 0 and expr/cron empty
	expr string        // expr-lang expression over process vars → duration
	cron string        // cron expression (recurring / next occurrence)
}

// AfterDuration builds a static, typed relative-delay spec, e.g.
// AfterDuration(30*time.Minute).
func AfterDuration(d time.Duration) TriggerSpec { return TriggerSpec{dur: d} }

// AfterExpr builds a dynamic spec: an expr-lang expression evaluated against the
// process variables at runtime, yielding a duration — e.g. AfterExpr("slaHours * 3600").
func AfterExpr(code string) TriggerSpec { return TriggerSpec{expr: code} }

// Cron builds a cron-schedule spec, e.g. Cron("0 9 * * *"). The cron form is
// scheduled by the gocron-backed scheduler (wired in a later change).
func Cron(expr string) TriggerSpec { return TriggerSpec{cron: expr} }

// FromWire reconstructs a TriggerSpec from its serialized trio (see the wire
// encoding): a non-zero nanos gives the duration form; else a non-empty cron
// gives the cron form; else a non-empty expr gives the expr form; else zero.
func FromWire(nanos int64, expr, cron string) TriggerSpec {
	switch {
	case nanos != 0:
		return TriggerSpec{dur: time.Duration(nanos)}
	case cron != "":
		return TriggerSpec{cron: cron}
	case expr != "":
		return TriggerSpec{expr: expr}
	default:
		return TriggerSpec{}
	}
}

// IsZero reports whether no form is set.
func (s TriggerSpec) IsZero() bool {
	return s.dur == 0 && s.expr == "" && s.cron == ""
}

// Duration returns the static duration and true when this is a duration spec.
func (s TriggerSpec) Duration() (time.Duration, bool) {
	if s.dur != 0 && s.expr == "" && s.cron == "" {
		return s.dur, true
	}
	return 0, false
}

// Expr returns the expr-lang expression and true when this is an expr spec.
func (s TriggerSpec) Expr() (string, bool) {
	if s.expr != "" {
		return s.expr, true
	}
	return "", false
}

// Cron returns the cron expression and true when this is a cron spec.
func (s TriggerSpec) Cron() (string, bool) {
	if s.cron != "" {
		return s.cron, true
	}
	return "", false
}

// Nanos returns the static duration as nanoseconds for serialization, or 0 when
// this is not a duration spec.
func (s TriggerSpec) Nanos() int64 {
	if d, ok := s.Duration(); ok {
		return int64(d)
	}
	return 0
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./definition/schedule/...`
Expected: PASS.

- [ ] **Step 5: Add a testable Example, run, commit**

```go
// definition/schedule/example_test.go
package schedule_test

import (
	"fmt"
	"time"

	"github.com/zakyalvan/krtlwrkflw/definition/schedule"
)

func ExampleAfterDuration() {
	s := schedule.AfterDuration(90 * time.Minute)
	d, _ := s.Duration()
	fmt.Println(d)
	// Output: 1h30m0s
}
```

Run: `go test ./definition/schedule/...` (Expected: PASS), then:

```bash
git add definition/schedule/
git commit -m "feat(schedule): typed TriggerSpec (AfterDuration/AfterExpr/Cron)"
```

---

## Task 2: Wire companion fields on `NodeWire`

Add the serialization companions so a `TriggerSpec` can round-trip. Additive and backward-compatible: the existing string fields (`timerDuration`, `deadlineDuration`, `reminderEvery`) remain and carry the **expr** form; new `*Nanos` (int64) and `*Cron` (string) fields carry the other two forms.

**Files:**
- Modify: `definition/model/node_wire.go`
- Test: `definition/model/node_wire_test.go` (create if absent)

**Interfaces:**
- Consumes: `schedule.TriggerSpec`, `schedule.FromWire`, `TriggerSpec.Nanos/Expr/Cron` (Task 1).
- Produces: new `NodeWire` fields `TimerDurationNanos int64`, `TimerCron string`, `DeadlineDurationNanos int64`, `DeadlineCron string`, `ReminderEveryNanos int64`, `ReminderCron string`; helpers `putTrigger`/`readTrigger`.

- [ ] **Step 1: Write the failing test**

```go
// definition/model/node_wire_test.go
package model_test

import (
	"testing"
	"time"

	"github.com/zakyalvan/krtlwrkflw/definition/model"
	"github.com/zakyalvan/krtlwrkflw/definition/schedule"
)

func TestTriggerWireRoundTrip(t *testing.T) {
	t.Run("duration → nanos", func(t *testing.T) {
		var w model.NodeWire
		model.PutTrigger(schedule.AfterDuration(time.Hour), &w.TimerDurationNanos, &w.TimerDuration, &w.TimerCron)
		if w.TimerDurationNanos != int64(time.Hour) || w.TimerDuration != "" || w.TimerCron != "" {
			t.Fatalf("wire = %+v", w)
		}
		got := model.ReadTrigger(w.TimerDurationNanos, w.TimerDuration, w.TimerCron)
		if d, ok := got.Duration(); !ok || d != time.Hour {
			t.Fatalf("read = %v %v", d, ok)
		}
	})
	t.Run("expr string preserved (old form)", func(t *testing.T) {
		got := model.ReadTrigger(0, "3h", "")
		if e, ok := got.Expr(); !ok || e != "3h" {
			t.Fatalf("read = %q %v", e, ok)
		}
	})
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./definition/model/... -run TestTriggerWireRoundTrip`
Expected: FAIL — `w.TimerDurationNanos` undefined, `model.PutTrigger`/`ReadTrigger` undefined.

- [ ] **Step 3: Add fields + helpers**

In `definition/model/node_wire.go`, add to the `NodeWire` struct (immediately after the existing `TimerDuration`, `DeadlineDuration`, `ReminderEvery` fields respectively):

```go
	TimerDuration         string             `json:"timerDuration,omitempty"`
	TimerDurationNanos    int64              `json:"timerDurationNanos,omitempty"`
	TimerCron             string             `json:"timerCron,omitempty"`
	DeadlineDuration      string             `json:"deadlineDuration,omitempty"`
	DeadlineDurationNanos int64              `json:"deadlineDurationNanos,omitempty"`
	DeadlineCron          string             `json:"deadlineCron,omitempty"`
	DeadlineFlow          string             `json:"deadlineFlow,omitempty"`
	DeadlineAction        string             `json:"deadlineAction,omitempty"`
	ReminderEvery         string             `json:"reminderEvery,omitempty"`
	ReminderEveryNanos    int64              `json:"reminderEveryNanos,omitempty"`
	ReminderCron          string             `json:"reminderCron,omitempty"`
	ReminderAction        string             `json:"reminderAction,omitempty"`
```

Add the two helpers (new file `definition/model/trigger_wire.go` to keep `node_wire.go` focused):

```go
// definition/model/trigger_wire.go
package model

import "github.com/zakyalvan/krtlwrkflw/definition/schedule"

// PutTrigger projects a TriggerSpec onto its serialized trio. The expr form uses
// the pre-existing string field (backward compatible); static durations use the
// nanos field; cron uses the cron field.
func PutTrigger(s schedule.TriggerSpec, nanos *int64, expr *string, cron *string) {
	*nanos, *expr, *cron = 0, "", ""
	if d, ok := s.Duration(); ok {
		*nanos = int64(d)
		return
	}
	if c, ok := s.Cron(); ok {
		*cron = c
		return
	}
	if e, ok := s.Expr(); ok {
		*expr = e
	}
}

// ReadTrigger reconstructs a TriggerSpec from its serialized trio.
func ReadTrigger(nanos int64, expr, cron string) schedule.TriggerSpec {
	return schedule.FromWire(nanos, expr, cron)
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./definition/model/... -run TestTriggerWireRoundTrip`
Expected: PASS. (`go build ./...` still green — fields are additive.)

- [ ] **Step 5: Commit**

```bash
git add definition/model/node_wire.go definition/model/trigger_wire.go definition/model/node_wire_test.go
git commit -m "feat(model): add TriggerSpec wire companion fields + Put/ReadTrigger"
```

---

## Task 3: Migrate `WaitFields` (deadline + reminder) to `TriggerSpec`

This is an atomic type change: `WaitFields.DeadlineDuration string` → `DeadlineTimer schedule.TriggerSpec`, and `ReminderEvery string` → `ReminderEvery schedule.TriggerSpec`. It breaks the carrier methods, `DeadlineOf`/`ReminderOf`, the `PutActivity/Activity/PutWait/Wait` wire helpers, the activity/catch options, and the engine deadline/reminder sites — all updated in this one task so the tree compiles at the end.

**Files:**
- Modify: `definition/model/node.go`, `definition/model/node_wire.go`, `definition/model/accessors.go`
- Modify: `definition/activity/options.go`, `definition/event/options.go`
- Modify: `engine/step_nodes.go` (deadline + reminder), `engine/step_timers.go` (reminder reschedule)
- Test: `definition/model/accessors_test.go` (adjust), `engine/step_timer_test.go` (adjust)

**Interfaces:**
- Consumes: `schedule.TriggerSpec`, `model.PutTrigger`/`ReadTrigger` (Tasks 1–2), engine `ResolveTrigger` (Task 5 — but this task only needs the expr/duration eval, so it calls `resolveTriggerDuration` defined here and reused by Task 5; see note).
- Produces:
  - `WaitFields{ DeadlineTimer schedule.TriggerSpec; DeadlineFlow, DeadlineAction string; ReminderEvery schedule.TriggerSpec; ReminderAction string }`
  - `DeadlineOf(n) (schedule.TriggerSpec, string, string)`
  - `ReminderOf(n) (schedule.TriggerSpec, string)`
  - `activity.WithDeadline(t schedule.TriggerSpec, flowID, action string)`
  - `activity.WithReminder(t schedule.TriggerSpec, action string)`
  - `event.WithCatchDeadline(t schedule.TriggerSpec, flowID, action string)`
  - `event.WithCatchReminder(t schedule.TriggerSpec, action string)`

> **Ordering note:** to keep TDD honest, first land the engine duration-resolution helper (Task 5's `ResolveTrigger`) OR inline a small resolver here. To avoid a forward dependency, this task introduces `engine.ResolveTrigger` (moved fully into Task 5's file but created here). Simplest: do **Task 5 Step 3 (the helper)** first, then this task. The plan lists Task 5 after for narrative clarity, but the helper function body is small and shown in Task 5; implement it before this task's engine edits.

- [ ] **Step 1: Write/adjust the failing test**

```go
// definition/model/accessors_test.go — replace the deadline/reminder assertions
func TestDeadlineOfTyped(t *testing.T) {
	n := activity.NewUserTask("ut", nil,
		activity.WithDeadline(schedule.AfterDuration(2*time.Hour), "sla-flow", "sla-act"),
		activity.WithReminder(schedule.AfterExpr(`"4h"`), "remind-act"),
	)
	spec, flow, action := model.DeadlineOf(n)
	if d, ok := spec.Duration(); !ok || d != 2*time.Hour || flow != "sla-flow" || action != "sla-act" {
		t.Fatalf("DeadlineOf = %v %q %q", d, flow, action)
	}
	every, ra := model.ReminderOf(n)
	if e, ok := every.Expr(); !ok || e != `"4h"` || ra != "remind-act" {
		t.Fatalf("ReminderOf = %q %q", e, ra)
	}
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./definition/model/... -run TestDeadlineOfTyped`
Expected: FAIL — `WithDeadline`/`WithReminder` signatures still take strings; `DeadlineOf` returns strings.

- [ ] **Step 3: Change the model, accessors, wire, and options**

`definition/model/node.go` — `WaitFields`:

```go
type WaitFields struct {
	// DeadlineTimer is the timer spec for the deadline (AfterDuration/AfterExpr/Cron).
	DeadlineTimer schedule.TriggerSpec
	// DeadlineFlow is the ID of the sequence flow to take on deadline breach.
	DeadlineFlow string
	// DeadlineAction is the name of the action.Action to invoke on deadline breach.
	DeadlineAction string
	// ReminderEvery is the timer spec for the reminder interval.
	ReminderEvery schedule.TriggerSpec
	// ReminderAction is the name of the action.Action to invoke for each reminder.
	ReminderAction string
}

func (w WaitFields) deadline() (schedule.TriggerSpec, string, string) {
	return w.DeadlineTimer, w.DeadlineFlow, w.DeadlineAction
}
func (w WaitFields) reminder() (schedule.TriggerSpec, string) {
	return w.ReminderEvery, w.ReminderAction
}
```

Add `import "github.com/zakyalvan/krtlwrkflw/definition/schedule"` to `node.go`.

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

`definition/model/node_wire.go` — `PutActivity`/`Activity`/`PutWait`/`Wait` (deadline+reminder portions):

```go
func (w *NodeWire) PutActivity(a ActivityFields) {
	w.RetryPolicy, w.RecoveryFlow = a.RetryPolicy, a.RecoveryFlow
	w.CompensationAction, w.CancelHandler = a.CompensationAction, a.CancelHandler
	PutTrigger(a.DeadlineTimer, &w.DeadlineDurationNanos, &w.DeadlineDuration, &w.DeadlineCron)
	w.DeadlineFlow, w.DeadlineAction = a.DeadlineFlow, a.DeadlineAction
	PutTrigger(a.ReminderEvery, &w.ReminderEveryNanos, &w.ReminderEvery, &w.ReminderCron)
	w.ReminderAction = a.ReminderAction
}

func (w NodeWire) Activity() ActivityFields {
	return ActivityFields{
		WaitFields:         w.Wait(),
		RetryPolicy:        w.RetryPolicy,
		RecoveryFlow:       w.RecoveryFlow,
		CompensationAction: w.CompensationAction,
		CancelHandler:      w.CancelHandler,
	}
}

func (w NodeWire) Wait() WaitFields {
	return WaitFields{
		DeadlineTimer:  ReadTrigger(w.DeadlineDurationNanos, w.DeadlineDuration, w.DeadlineCron),
		DeadlineFlow:   w.DeadlineFlow,
		DeadlineAction: w.DeadlineAction,
		ReminderEvery:  ReadTrigger(w.ReminderEveryNanos, w.ReminderEvery, w.ReminderCron),
		ReminderAction: w.ReminderAction,
	}
}

func (w *NodeWire) PutWait(a WaitFields) {
	PutTrigger(a.DeadlineTimer, &w.DeadlineDurationNanos, &w.DeadlineDuration, &w.DeadlineCron)
	w.DeadlineFlow, w.DeadlineAction = a.DeadlineFlow, a.DeadlineAction
	PutTrigger(a.ReminderEvery, &w.ReminderEveryNanos, &w.ReminderEvery, &w.ReminderCron)
	w.ReminderAction = a.ReminderAction
}
```

`definition/activity/options.go`:

```go
// WithDeadline sets the deadline timer, flow, and action.
func WithDeadline(t schedule.TriggerSpec, flowID, action string) activityOnlyOption {
	return withActivity(func(a *model.ActivityFields) {
		a.DeadlineTimer, a.DeadlineFlow, a.DeadlineAction = t, flowID, action
	})
}

// WithReminder sets the reminder timer and action.
func WithReminder(t schedule.TriggerSpec, action string) activityOnlyOption {
	return withActivity(func(a *model.ActivityFields) {
		a.ReminderEvery, a.ReminderAction = t, action
	})
}
```

Add `import "github.com/zakyalvan/krtlwrkflw/definition/schedule"` to `activity/options.go`.

`definition/event/options.go`:

```go
func WithCatchDeadline(t schedule.TriggerSpec, flowID, action string) CatchOption {
	return catchFuncOpt{func(n *IntermediateCatchEvent) {
		n.DeadlineTimer, n.DeadlineFlow, n.DeadlineAction = t, flowID, action
	}}
}

func WithCatchReminder(t schedule.TriggerSpec, action string) CatchOption {
	return catchFuncOpt{func(n *IntermediateCatchEvent) { n.ReminderEvery, n.ReminderAction = t, action }}
}
```

Add the `schedule` import to `event/options.go`.

`engine/step_nodes.go` — deadline site (was lines 510–533): replace the string read with the resolver from Task 5:

```go
	// If the node carries a deadline, schedule the deadline timer.
	if spec, flow, action := model.DeadlineOf(node); !spec.IsZero() {
		_ = flow
		_ = action
		dur, err := ResolveTrigger(c.eval, spec, c.s.Variables)
		if err != nil {
			return cmds, false, fmt.Errorf("workflow-engine: deadline node %q: %w", node.ID(), err)
		}
		fireAt := c.at.Add(dur)
		deadlineTimerID := c.s.nextTimerID()
		cmds = append(cmds, ScheduleTimer{TimerID: deadlineTimerID, Token: tok.ID, FireAt: fireAt, Kind: TimerDeadline})
		c.s.Timers = append(c.s.Timers, timerRecord{TimerID: deadlineTimerID, Kind: TimerDeadline, Token: tok.ID, TaskToken: taskToken, NodeID: node.ID(), ScopeID: tok.ScopeID})
		ht.DueAt = &fireAt
	}
```

> Keep the existing use of `ut.DeadlineDuration`/`ut.ReminderEvery` semantics by switching to `model.DeadlineOf(node)`/`model.ReminderOf(node)` (the typed accessors), since `ut.DeadlineDuration` no longer exists. Apply the same to the reminder site (was lines 537–557) using `model.ReminderOf(node)` and `ResolveTrigger`.

`engine/step_timers.go` — reminder reschedule (was line 181): the handler currently re-reads `reminderEvery` (a string it captured). Change the captured value to a `schedule.TriggerSpec` (from `model.ReminderOf(node)`) and resolve via `ResolveTrigger`:

```go
	spec, _ := model.ReminderOf(node)
	dur, err := ResolveTrigger(eval, spec, s.Variables)
	if err != nil {
		return StepResult{}, fmt.Errorf("workflow-engine: reminder node %q re-schedule: %w", node.ID(), err)
	}
```

- [ ] **Step 4: Run tests to verify green**

Run: `go test ./definition/... ./engine/...`
Expected: the adjusted `TestDeadlineOfTyped` PASSES; fix any remaining compile errors in these packages' tests (call-site migrations happen in Task 6). `go build ./...` may still fail in OTHER packages (event structs, examples) until Tasks 4 & 6 — that is expected; this task's own packages compile.

- [ ] **Step 5: Commit**

```bash
git add definition/model/ definition/activity/options.go definition/event/options.go engine/step_nodes.go engine/step_timers.go
git commit -m "refactor(model): WaitFields deadline/reminder → schedule.TriggerSpec"
```

---

## Task 4: Migrate event `TimerDuration` fields to `Timer schedule.TriggerSpec`

`StartEvent`/`IntermediateCatchEvent`/`BoundaryEvent` (and the event-gateway catch arm + event-subprocess start) currently hold `TimerDuration string`. Rename to `Timer schedule.TriggerSpec`; update NodeSpec `ToWire`/`FromWire` and the timer options; update the engine timer arm sites.

**Files:**
- Modify: `definition/event/event.go` (structs + 3 NodeSpecs), `definition/event/options.go` (`WithStartTimer`, `WithCatchTimer`, `WithBoundaryTimer`)
- Modify: `engine/step_nodes.go` (ICE arm ~583, event-gateway arm ~765), `engine/step_boundaries.go` (~44), `engine/step_eventsubprocess.go` (~53)
- Test: `definition/event/event_test.go` (adjust), `engine/step_boundaries_test.go`/`engine/receive_task_test.go` (adjust)

**Interfaces:**
- Consumes: `schedule.TriggerSpec`, `ResolveTrigger` (Task 5), `PutTrigger`/`ReadTrigger`.
- Produces:
  - `StartEvent.Timer`, `IntermediateCatchEvent.Timer`, `BoundaryEvent.Timer` (all `schedule.TriggerSpec`).
  - `event.WithStartTimer(t schedule.TriggerSpec)`, `event.WithCatchTimer(t schedule.TriggerSpec)`, `event.WithBoundaryTimer(t schedule.TriggerSpec)`.

- [ ] **Step 1: Write/adjust the failing test**

```go
// definition/event/event_test.go
func TestBoundaryTimerTyped(t *testing.T) {
	n := event.NewBoundary("b", "host", event.WithBoundaryTimer(schedule.AfterDuration(time.Hour)))
	be := n.(event.BoundaryEvent)
	if d, ok := be.Timer.Duration(); !ok || d != time.Hour {
		t.Fatalf("Timer = %v %v", d, ok)
	}
	// wire round-trip
	w := model.ToWireForTest(n) // or the existing marshal helper in this package
	got := model.FromWireForTest(w)
	if d, ok := got.(event.BoundaryEvent).Timer.Duration(); !ok || d != time.Hour {
		t.Fatalf("round-trip Timer = %v %v", d, ok)
	}
}
```

> If the event_test package has no `ToWire`/`FromWire` test helper, assert via the definition-level JSON marshal already used in `definition/model/definition_test.go` (a full-definition round-trip). Use whichever round-trip helper the package already has; do not invent one.

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./definition/event/... -run TestBoundaryTimerTyped`
Expected: FAIL — `WithBoundaryTimer` takes a string; `BoundaryEvent.Timer` undefined.

- [ ] **Step 3: Change structs, NodeSpecs, options, engine sites**

`definition/event/event.go` — replace `TimerDuration string` with `Timer schedule.TriggerSpec` in `StartEvent`, `IntermediateCatchEvent`, `BoundaryEvent` (and add the `schedule` import). Update the three NodeSpecs:

```go
model.RegisterKind(model.KindStartEvent, model.NodeSpec{
	Name: "startEvent",
	FromWire: func(b model.Base, w model.NodeWire) model.Node {
		return StartEvent{Base: b, SignalName: w.SignalName, MessageName: w.MessageName, CorrelationKey: w.CorrelationKey,
			Timer: model.ReadTrigger(w.TimerDurationNanos, w.TimerDuration, w.TimerCron)}
	},
	ToWire: func(n model.Node, w *model.NodeWire) {
		v := n.(StartEvent)
		w.SignalName, w.MessageName, w.CorrelationKey = v.SignalName, v.MessageName, v.CorrelationKey
		model.PutTrigger(v.Timer, &w.TimerDurationNanos, &w.TimerDuration, &w.TimerCron)
	},
})

model.RegisterKind(model.KindIntermediateCatchEvent, model.NodeSpec{
	Name: "intermediateCatchEvent",
	FromWire: func(b model.Base, w model.NodeWire) model.Node {
		return IntermediateCatchEvent{Base: b, WaitFields: w.Wait(),
			Timer: model.ReadTrigger(w.TimerDurationNanos, w.TimerDuration, w.TimerCron),
			SignalName: w.SignalName, MessageName: w.MessageName, CorrelationKey: w.CorrelationKey}
	},
	ToWire: func(n model.Node, w *model.NodeWire) {
		v := n.(IntermediateCatchEvent)
		model.PutTrigger(v.Timer, &w.TimerDurationNanos, &w.TimerDuration, &w.TimerCron)
		w.SignalName, w.MessageName, w.CorrelationKey = v.SignalName, v.MessageName, v.CorrelationKey
		w.PutWait(v.WaitFields)
	},
})

model.RegisterKind(model.KindBoundaryEvent, model.NodeSpec{
	Name: "boundaryEvent",
	FromWire: func(b model.Base, w model.NodeWire) model.Node {
		return BoundaryEvent{Base: b, AttachedTo: w.AttachedTo, NonInterrupting: w.NonInterrupting, ErrorCode: w.ErrorCode,
			SignalName: w.SignalName, MessageName: w.MessageName, CorrelationKey: w.CorrelationKey,
			Timer: model.ReadTrigger(w.TimerDurationNanos, w.TimerDuration, w.TimerCron)}
	},
	ToWire: func(n model.Node, w *model.NodeWire) {
		v := n.(BoundaryEvent)
		w.AttachedTo, w.NonInterrupting, w.ErrorCode = v.AttachedTo, v.NonInterrupting, v.ErrorCode
		w.SignalName, w.MessageName, w.CorrelationKey = v.SignalName, v.MessageName, v.CorrelationKey
		model.PutTrigger(v.Timer, &w.TimerDurationNanos, &w.TimerDuration, &w.TimerCron)
	},
})
```

`definition/event/options.go`:

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

Engine sites — replace `n.TimerDuration != ""` / `EvalDuration(n.TimerDuration, ...)` with the typed spec + `ResolveTrigger`:

- `engine/step_boundaries.go` (~40): `if !n.Timer.IsZero() { dur, err := ResolveTrigger(eval, n.Timer, s.Variables); ... }` (keep the rest — `s.nextTimerID()`, `ScheduleTimer{FireAt: at.Add(dur), Kind: TimerIntermediate}`).
- `engine/step_nodes.go` ICE (~582): `ice.Timer` in place of `ice.TimerDuration`.
- `engine/step_nodes.go` event-gateway (~764): `ce.Timer` in place of `ce.TimerDuration` (the event-gateway catch is an `IntermediateCatchEvent`, so its field is `Timer`).
- `engine/step_eventsubprocess.go` (~50): `se.Timer` in place of `se.TimerDuration`.

Each becomes:

```go
if !X.Timer.IsZero() {
	dur, err := ResolveTrigger(eval, X.Timer, s.Variables)
	if err != nil { return ...fmt.Errorf(...) }
	// unchanged: nextTimerID + ScheduleTimer{FireAt: at.Add(dur), Kind: ...}
}
```

- [ ] **Step 4: Run tests**

Run: `go test ./definition/event/... ./engine/...`
Expected: `TestBoundaryTimerTyped` PASSES; engine timer tests compile/adjust. Remaining failures only in packages with un-migrated call sites (Task 6).

- [ ] **Step 5: Commit**

```bash
git add definition/event/ engine/step_boundaries.go engine/step_nodes.go engine/step_eventsubprocess.go
git commit -m "refactor(event): timer fields → schedule.TriggerSpec"
```

---

## Task 5: Engine `ResolveTrigger` helper

The single point that turns a `TriggerSpec` into a `time.Duration` for arm sites. Static → pass through; expr → `EvalDuration`; cron → error for now (wired in Plan B).

> Implement this BEFORE Tasks 3–4's engine edits (they call it). It is listed here for narrative grouping.

**Files:**
- Create: `engine/trigger_resolve.go`
- Test: `engine/trigger_resolve_test.go`

**Interfaces:**
- Consumes: `ConditionEvaluator.EvalDuration` (unchanged), `schedule.TriggerSpec`.
- Produces: `func ResolveTrigger(eval ConditionEvaluator, spec schedule.TriggerSpec, env map[string]any) (time.Duration, error)` and sentinel `var ErrCronUnsupported = errors.New("workflow-engine: cron trigger not supported without a cron-capable scheduler")`.

- [ ] **Step 1: Failing test**

```go
// engine/trigger_resolve_test.go
package engine_test

import (
	"errors"
	"testing"
	"time"

	"github.com/zakyalvan/krtlwrkflw/definition/schedule"
	"github.com/zakyalvan/krtlwrkflw/engine"
	"github.com/zakyalvan/krtlwrkflw/internal/expreval"
)

func TestResolveTrigger(t *testing.T) {
	ev := expreval.New() // concrete engine.ConditionEvaluator (internal/expreval)

	t.Run("static duration", func(t *testing.T) {
		d, err := engine.ResolveTrigger(ev, schedule.AfterDuration(time.Hour), nil)
		if err != nil || d != time.Hour {
			t.Fatalf("= %v, %v", d, err)
		}
	})
	t.Run("expr over vars", func(t *testing.T) {
		d, err := engine.ResolveTrigger(ev, schedule.AfterExpr(`h * 3600`), map[string]any{"h": 2})
		if err != nil || d != 2*time.Hour {
			t.Fatalf("= %v, %v", d, err)
		}
	})
	t.Run("cron unsupported here", func(t *testing.T) {
		_, err := engine.ResolveTrigger(ev, schedule.Cron(`0 9 * * *`), nil)
		if !errors.Is(err, engine.ErrCronUnsupported) {
			t.Fatalf("want ErrCronUnsupported, got %v", err)
		}
	})
}
```

> The concrete `ConditionEvaluator` is `expreval.New()` (`internal/expreval/expreval.go:52`), verified. In non-test engine code the evaluator arrives via `StepOptions` / `resolveEvaluator`; `ResolveTrigger` receives it as a parameter, so no engine-internal construction is needed.

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./engine/... -run TestResolveTrigger`
Expected: FAIL — `engine.ResolveTrigger` / `engine.ErrCronUnsupported` undefined.

- [ ] **Step 3: Implement**

```go
// engine/trigger_resolve.go
package engine

import (
	"errors"
	"time"

	"github.com/zakyalvan/krtlwrkflw/definition/schedule"
)

// ErrCronUnsupported is returned by ResolveTrigger for a cron TriggerSpec until
// a cron-capable scheduler path is wired (Plan B).
var ErrCronUnsupported = errors.New("workflow-engine: cron trigger not supported without a cron-capable scheduler")

// ResolveTrigger converts a TriggerSpec to a relative delay for arm sites:
// a static duration passes through; an expr-lang spec is evaluated against env
// via the ConditionEvaluator; a cron spec returns ErrCronUnsupported for now.
func ResolveTrigger(eval ConditionEvaluator, spec schedule.TriggerSpec, env map[string]any) (time.Duration, error) {
	if d, ok := spec.Duration(); ok {
		return d, nil
	}
	if e, ok := spec.Expr(); ok {
		return eval.EvalDuration(e, env)
	}
	if _, ok := spec.Cron(); ok {
		return 0, ErrCronUnsupported
	}
	return 0, nil // zero spec → zero delay (fires immediately); callers guard with IsZero
}
```

- [ ] **Step 4: Run to verify passes**

Run: `go test ./engine/... -run TestResolveTrigger`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add engine/trigger_resolve.go engine/trigger_resolve_test.go
git commit -m "feat(engine): ResolveTrigger (static/expr → duration; cron deferred)"
```

---

## Task 6: Migrate all remaining call sites + full green

Every test/example that constructs a timer/deadline/reminder option now passes a `TriggerSpec`. The current-string call sites (from the codebase scan) are enumerated below; wrap each duration literal in `schedule.AfterExpr(...)` when it was an expr string (backtick-quoted like `` `"1h"` `` or ISO-like `"PT24H"`), or `schedule.AfterDuration(...)` for a genuine static value. **Preserve existing semantics:** the existing tests used expr strings, so wrap them in `AfterExpr` to keep byte-identical wire + identical behavior (do NOT silently convert to `AfterDuration`).

**Files (migrate the option calls; add `schedule` import to each):**
- `runtime/timer_example_test.go:89`, `runtime/deadline_fireforget_e2e_test.go:86`
- `internal/persistence/store/definitions_conformance_test.go:44,45`
- `definition/activity/activity_test.go:21,22,140`
- `definition/model/node_test.go:86,87`, `definition/model/definition_test.go:263,277,290,291`, `definition/model/accessors_test.go:67,74,108,147,148`
- `engine/step_timer_test.go:165,353,564,702`, `engine/step_timers_fireforget_test.go:23,42`, `engine/step_subprocess_test.go:1031,1619`
- `examples/scenarios/boundary_timer/main.go:67`, `examples/scenarios/inwait_reminder/main.go:55`, `examples/scenarios/timer_boundary/main.go` (WithBoundaryTimer call)

- [ ] **Step 1: Migrate each call site (mechanical)**

Transformation rules (apply verbatim):
- `activity.WithDeadline(\`"30m"\`, "escalate", "notify")` → `activity.WithDeadline(schedule.AfterExpr(\`"30m"\`), "escalate", "notify")`
- `activity.WithReminder(\`"1h"\`, "remind")` → `activity.WithReminder(schedule.AfterExpr(\`"1h"\`), "remind")`
- `activity.WithReminder(\`"1h"\`, "")` → `activity.WithReminder(schedule.AfterExpr(\`"1h"\`), "")`
- `activity.WithDeadline("PT24H", "sla-breach", "notify-manager")` → `activity.WithDeadline(schedule.AfterExpr("PT24H"), ...)` (kept as expr; PT24H is a test literal parsed by the expr path — preserve as-is)
- `activity.WithDeadline(\`"1h"\`, "", "")` → `activity.WithDeadline(schedule.AfterExpr(\`"1h"\`), "", "")`
- `event.WithBoundaryTimer(\`"30m"\`)` → `event.WithBoundaryTimer(schedule.AfterExpr(\`"30m"\`))`
- `event.WithCatchTimer(...)` / `event.WithStartTimer(...)` similarly.

For the examples that read best as typed static values, prefer `AfterDuration`:
- `examples/scenarios/boundary_timer/main.go:67` (soon `user_deadline`): `activity.WithDeadline(schedule.AfterDuration(time.Hour), "review-overdue", "notify-overdue")` — this changes the wire from expr `"1h"` to nanos, which is fine for an example (no persisted fixtures). Add `"time"` import if needed.
- `examples/scenarios/inwait_reminder/main.go:55`: `activity.WithReminder(schedule.AfterDuration(30*time.Minute), "nudge-reviewer")`.
- `examples/scenarios/timer_boundary/main.go`: `event.WithBoundaryTimer(schedule.AfterDuration(30*time.Minute))`.

Add `"github.com/zakyalvan/krtlwrkflw/definition/schedule"` (and `"time"` where `AfterDuration` is used) to each file's imports.

- [ ] **Step 2: Build + full test**

Run: `go build ./... && go test ./...`
Expected: PASS across the module. Fix any stragglers the scan missed (grep to be sure):

Run: `grep -rn "WithDeadline(\`\\|WithDeadline(\"\\|WithReminder(\`\\|WithBoundaryTimer(\`\\|WithCatchTimer(\`\\|WithStartTimer(\`\\|WithCatchDeadline(\\|WithCatchReminder(" --include=*.go . | grep -v schedule.After`
Expected: no output (every call site now wraps a `TriggerSpec`).

- [ ] **Step 3: Run the changed examples to confirm behavior**

Run: `go run ./examples/scenarios/timer_boundary/ && go run ./examples/scenarios/inwait_reminder/`
Expected: same terminal output as before this plan (timer fires / reminders fire).

- [ ] **Step 4: Coverage + lint**

Run: `go test -race -coverprofile=cover.out ./definition/... ./engine/... && go tool cover -func=cover.out | tail -1`
Expected: total ≥ 85%.
Run: `golangci-lint run ./...`
Expected: 0 issues. Remove any stray example binaries (`rm -f timer_boundary inwait_reminder boundary_timer`).

- [ ] **Step 5: Commit**

```bash
git add -A
git commit -m "refactor: migrate all timer/deadline/reminder call sites to schedule.TriggerSpec"
```

---

## Task 7: ADR-0102

**Files:**
- Create: `docs/adr/0102-typed-trigger-spec.md`

- [ ] **Step 1: Write the ADR (Nygard template)**

```markdown
# 0102. Typed schedule.TriggerSpec for timer durations

- Status: Accepted
- Date: 2026-07-07

## Context

Timer/deadline/reminder durations were expr-lang strings evaluated against
process variables at runtime (dynamic). This gave flexibility but no type
safety for the common static case, and no cron scheduling. The expr-lang
mechanism for durations is a locked tech-stack decision.

## Decision

Introduce `definition/schedule.TriggerSpec`, a value with three forms —
`AfterDuration(time.Duration)` (static), `AfterExpr(string)` (expr-lang →
duration, the preserved dynamic form), and `Cron(string)` (reserved; wired to
gocron in a follow-up). All timer/deadline/reminder options and model fields
take a `TriggerSpec`. The engine resolves it via `ResolveTrigger`
(static pass-through; expr via the existing `EvalDuration`). The wire keeps the
existing string field as the expr form and adds `*Nanos`/`*Cron` companions, so
old definitions load unchanged. This EXTENDS, not replaces, the expr-lang
decision.

## Consequences

- Type-safe static durations; dynamic expr durations preserved; cron enabled by
  a follow-up (Plan B).
- Breaking Go API (option signatures) — pre-v1.0, all call sites migrated.
- Backward-compatible wire; old JSON/YAML durations decode as the expr form.
- `ResolveTrigger` returns `ErrCronUnsupported` until the cron scheduler lands.
```

- [ ] **Step 2: Commit**

```bash
git add docs/adr/0102-typed-trigger-spec.md
git commit -m "docs(adr): 0102 typed schedule.TriggerSpec for timer durations"
```

---

## Self-Review Notes (spec coverage)

- Type-safety (static) → Tasks 1, 3, 4, 6. Dynamic expr preserved → Task 5 (`ResolveTrigger` expr path) + Task 6 (wrap literals in `AfterExpr`). Backward-compatible wire → Task 2 + Task 3/4 NodeSpecs. All option sites → Tasks 3, 4, 6. Engine resolution at every timer site → Tasks 3, 4, 5. ADR → Task 7.
- **Cron is intentionally deferred** to Plan B (`ResolveTrigger` returns `ErrCronUnsupported`; `Cron`/`*Cron` fields are inert storage). The scheduler-port `ScheduleCron`, gocron `CronJob`, `MemScheduler` cron, and cron emission are Plan B.
- Determinism/replay unaffected: durations resolve exactly as before (expr path byte-identical; static path is new but equivalent).
```
