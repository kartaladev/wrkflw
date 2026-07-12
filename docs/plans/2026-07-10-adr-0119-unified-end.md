# ADR-0119 — Unified End Event with Force-Termination — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Fold `TerminateEndEvent` into `EndEvent` via a `WithForceTermination(reason, outcome)` option that implements the engine's first real terminate semantics, with a selectable terminal outcome (successful halt → `StatusCompleted`, or abort → `StatusTerminated`).

**Architecture:** `EndEvent` gains three fields (`ForceTermination`, `TerminationReason`, `Outcome`) and an `EndOption` interface (mirroring `StartOption`/`CatchOption`). When a force-termination end is entered, `endEventStrategy.enter` runs the same cancel-cleanup already used by `handleCancelRequested`'s immediate-termination path (cancel open tasks, timers, arms/boundaries, ESP arms) and ends at the selected status. The bespoke `TerminateEndEvent` kind, its wire name, constructor, and builder method are deleted (clean break — library unreleased). A single-end def carrying force-termination is redundant, not an error: it emits an slog WARN at registration time.

**Tech Stack:** Go 1.25, `github.com/expr-lang/expr` (unaffected), stdlib `log/slog`. No new dependencies.

## Global Constraints

- **Go 1.25**; module path `github.com/kartaladev/wrkflw`.
- **TDD strict** (CLAUDE.md): no production code before a failing test. Every new symbol / behavioural change gets a visible RED (`go test ./<pkg>/...` fails) before GREEN. Deletion/refactor tasks keep existing tests green before AND after; new *behaviour* within them (e.g. a retired wire name now erroring) gets its own RED test first.
- **Black-box tests** (`package <pkg>_test`) preferred. Pair each `.go` with a same-named `_test.go`.
- **Table tests** use the project `table-test` skill: `assert` closure form (not `want`/`wantErr` fields), `t.Context()` over `context.Background()`.
- **Error sentinel prefix:** `workflow-<pkg>: ...`.
- **Clean break:** library is unreleased — no wire aliases, no migrators. `terminateEndEvent` wire name is deleted; unmarshalling it now errors.
- **Naming/idioms:** always-on `cc-skills-golang` skills (code-style, naming, error-handling, structs-interfaces, documentation). Godoc on every exported symbol.
- **Verification per touched package:** `go test -race ./... && go tool cover` ≥ 85% line coverage; `go test ./...` from root green; `golangci-lint run ./...` clean.

---

## File Structure

- `definition/event/event.go` — **Modify.** Add `TerminationOutcome` enum + `String()`; add three fields to `EndEvent`; change `NewEnd` signature to `(id string, opts ...EndOption)`; delete `TerminateEndEvent` struct + `Kind()` + `NewTerminateEnd`; update `EndEvent` `RegisterKind` ToWire/FromWire; delete `KindTerminateEndEvent` `RegisterKind`.
- `definition/event/options.go` — **Modify.** Add `EndOption` interface; extend `WithName` union to include `EndOption`; add `WithForceTermination`.
- `definition/model/node_wire.go` — **Modify.** Add `ForceTermination bool`, `TerminationReason string`, `TerminationOutcome string` fields.
- `definition/model/definition.go` — **Modify.** Delete the `KindTerminateEndEvent` iota constant.
- `definition/model/validate.go` — **Modify.** Drop `KindTerminateEndEvent` from the `isEnd` predicate.
- `definition/build/build.go` — **Modify.** Delete `AddTerminateEndEvent`; change `AddEndEvent` signature to `(id string, opts ...event.EndOption)`.
- `engine/step_nodes.go` — **Modify.** Add force-termination branch + `forceTerminate` helper at the top of `endEventStrategy.enter`; update the `nodeStrategies` "kinds NOT in this map" comment.
- `engine/step.go` — **Modify.** Update the unhandled-kind comment (remove `KindTerminateEndEvent`).
- `runtime/definition_registry.go` — **Modify.** Add `forceTerminationWarnings` pure helper + slog WARN on register (both `RegisterDefinition` and `MustRegisterDefinition`).
- `examples/scenarios/terminate_end/main.go` — **Create.** Reference wiring: parallel fork, one branch aborts (cancels sibling), companion path shows `OutcomeComplete`.
- `docs/adr/0119-unified-end-event.md` — **Create.** Nygard-template ADR.
- Tests: `definition/event/event_test.go`, `definition/event/options_test.go` (or existing), `definition/model/*_test.go` (wire round-trip, kind-name), `engine/*_test.go` (force-terminate behaviour), `runtime/definition_registry_test.go`.

---

## Task 1: Model — `TerminationOutcome` enum, `EndEvent` fields, `EndOption`, `WithForceTermination`

**Files:**
- Modify: `definition/event/event.go` (EndEvent struct ~35, NewEnd ~153)
- Modify: `definition/event/options.go` (option interfaces ~8, WithName ~39)
- Test: `definition/event/end_option_test.go` (create)

**Interfaces:**
- Consumes: `model.Base`, `model.NewBase`, existing `nameOpt`.
- Produces:
  - `type TerminationOutcome int` with `const (OutcomeComplete TerminationOutcome = iota; OutcomeAbort)` and `func (TerminationOutcome) String() string` → `"complete"` / `"abort"`.
  - `EndEvent` fields: `ForceTermination bool`, `TerminationReason string`, `Outcome TerminationOutcome`.
  - `type EndOption interface{ applyEnd(n *EndEvent) }`.
  - `func WithForceTermination(reason string, outcome TerminationOutcome) EndOption`.
  - `func NewEnd(id string, opts ...EndOption) model.Node` (breaking: was `name ...string`).
  - `WithName(...)` return type extended to also satisfy `EndOption`.

- [ ] **Step 1: Write the failing test**

Create `definition/event/end_option_test.go`:

```go
package event_test

import (
	"testing"

	"github.com/kartaladev/wrkflw/definition/event"
)

func TestTerminationOutcomeString(t *testing.T) {
	t.Parallel()
	cases := []struct {
		outcome event.TerminationOutcome
		assert  func(t *testing.T, got string)
	}{
		{event.OutcomeComplete, func(t *testing.T, got string) {
			if got != "complete" {
				t.Fatalf("OutcomeComplete.String() = %q, want %q", got, "complete")
			}
		}},
		{event.OutcomeAbort, func(t *testing.T, got string) {
			if got != "abort" {
				t.Fatalf("OutcomeAbort.String() = %q, want %q", got, "abort")
			}
		}},
	}
	for _, c := range cases {
		c.assert(t, c.outcome.String())
	}
}

func TestNewEndWithForceTermination(t *testing.T) {
	t.Parallel()
	n := event.NewEnd("halt", event.WithName("Halt"), event.WithForceTermination("fraud detected", event.OutcomeAbort))
	ev, ok := n.(event.EndEvent)
	if !ok {
		t.Fatalf("NewEnd returned %T, want event.EndEvent", n)
	}
	if !ev.ForceTermination {
		t.Fatal("ForceTermination = false, want true")
	}
	if ev.TerminationReason != "fraud detected" {
		t.Fatalf("TerminationReason = %q, want %q", ev.TerminationReason, "fraud detected")
	}
	if ev.Outcome != event.OutcomeAbort {
		t.Fatalf("Outcome = %v, want OutcomeAbort", ev.Outcome)
	}
	if ev.Name() != "Halt" {
		t.Fatalf("Name() = %q, want %q", ev.Name(), "Halt")
	}
}

func TestNewEndPlain(t *testing.T) {
	t.Parallel()
	ev := event.NewEnd("done").(event.EndEvent)
	if ev.ForceTermination {
		t.Fatal("plain NewEnd should not force-terminate")
	}
	if ev.Outcome != event.OutcomeComplete {
		t.Fatalf("plain NewEnd Outcome = %v, want OutcomeComplete (zero value)", ev.Outcome)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./definition/event/...`
Expected: compile FAIL — `undefined: event.TerminationOutcome`, `event.WithForceTermination`, and `NewEnd` signature mismatch.

- [ ] **Step 3: Write minimal implementation**

In `definition/event/event.go`, replace the `EndEvent` type and `NewEnd`:

```go
// TerminationOutcome selects the terminal status a force-termination end event
// drives the instance to.
type TerminationOutcome int

const (
	// OutcomeComplete ends the instance at StatusCompleted — a successful
	// business halt that cancels remaining parallel work.
	OutcomeComplete TerminationOutcome = iota
	// OutcomeAbort ends the instance at StatusTerminated — an abort.
	OutcomeAbort
)

// String returns the stable lowercase name of the outcome ("complete"/"abort"),
// used for wire encoding and logging.
func (o TerminationOutcome) String() string {
	switch o {
	case OutcomeAbort:
		return "abort"
	default:
		return "complete"
	}
}

// EndEvent is the workflow end event: a normal process completion point. When
// ForceTermination is set (via WithForceTermination) it instead terminates the
// whole instance — cancelling remaining parallel tokens, timers, boundaries,
// event sub-process arms, and open tasks — and ends at the Outcome-selected
// status carrying TerminationReason.
type EndEvent struct {
	model.Base
	// ForceTermination, when true, makes this end event terminate the entire
	// instance rather than just consuming its own token.
	ForceTermination bool
	// TerminationReason is a human-readable reason recorded on force-termination
	// (empty when ForceTermination is false).
	TerminationReason string
	// Outcome selects the terminal status on force-termination. Ignored when
	// ForceTermination is false.
	Outcome TerminationOutcome
}

// Kind returns model.KindEndEvent.
func (EndEvent) Kind() model.NodeKind { return model.KindEndEvent }
```

Replace `NewEnd`:

```go
// NewEnd constructs an EndEvent. Use WithName for a display name and
// WithForceTermination to make it terminate the whole instance.
func NewEnd(id string, opts ...EndOption) model.Node {
	n := EndEvent{Base: model.NewBase(id, "")}
	for _, o := range opts {
		o.applyEnd(&n)
	}
	return n
}
```

In `definition/event/options.go`, add the `EndOption` interface (near the other option interfaces ~line 25):

```go
// EndOption configures an EndEvent.
type EndOption interface{ applyEnd(n *EndEvent) }
```

Extend `nameOpt` to satisfy `EndOption` (add next to the other `applyX` methods ~line 34):

```go
func (o nameOpt) applyEnd(n *EndEvent) { n.SetName(o.name) }
```

Extend the `WithName` return union to include `EndOption` (~line 39):

```go
// WithName sets the display name on a start, end, catch, boundary, or event
// sub-process node. IntermediateThrowEvent uses WithThrowName instead.
func WithName(name string) interface {
	StartOption
	EndOption
	CatchOption
	BoundaryOption
	EventSubProcessOption
} {
	return nameOpt{name}
}
```

Add `WithForceTermination` (with a small option type) near the end-of-file options:

```go
type forceTerminationOpt struct {
	reason  string
	outcome TerminationOutcome
}

func (o forceTerminationOpt) applyEnd(n *EndEvent) {
	n.ForceTermination = true
	n.TerminationReason = o.reason
	n.Outcome = o.outcome
}

// WithForceTermination makes an EndEvent terminate the whole instance when
// reached, cancelling remaining parallel work. outcome selects the terminal
// status: OutcomeComplete → StatusCompleted (successful halt), OutcomeAbort →
// StatusTerminated (abort). reason is recorded for observability.
//
// Force-termination is only meaningful in a definition with multiple end events
// (or parallel branches) to cancel; on a single-end definition it is redundant
// (a WARN is logged at registration).
func WithForceTermination(reason string, outcome TerminationOutcome) EndOption {
	return forceTerminationOpt{reason: reason, outcome: outcome}
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./definition/event/...`
Expected: the three new tests PASS. (Other packages will not compile yet — that is expected; Tasks 2–3 fix them. Do NOT run `go build ./...` until Task 3.)

- [ ] **Step 5: Commit**

```bash
git add definition/event/event.go definition/event/options.go definition/event/end_option_test.go
git commit -m "feat(definition/event): EndEvent force-termination option + TerminationOutcome (ADR-0119)"
```

---

## Task 2: Wire round-trip for force-termination

**Files:**
- Modify: `definition/model/node_wire.go` (struct ~44)
- Modify: `definition/event/event.go` (KindEndEvent RegisterKind ~235)
- Test: `definition/event/wire_end_test.go` (create)

**Interfaces:**
- Consumes: `EndEvent`, `TerminationOutcome`, `OutcomeComplete`, `OutcomeAbort` from Task 1; `model.NodeWire`; the round-trip helpers `definition.NewBuilder(...).Build()` → marshal/unmarshal, OR the model registry `ToWire`/`FromWire` directly.
- Produces: `NodeWire.ForceTermination bool` (`json:"forceTermination,omitempty"`), `NodeWire.TerminationReason string` (`json:"terminationReason,omitempty"`), `NodeWire.TerminationOutcome string` (`json:"terminationOutcome,omitempty"`). EndEvent `ToWire`/`FromWire` map `Outcome` ↔ the `"complete"`/`"abort"` string.

- [ ] **Step 1: Write the failing test**

Create `definition/event/wire_end_test.go`. Use the same round-trip mechanism existing event wire tests use (inspect a neighbouring `*_wire_test.go` or the model's kind-registry round-trip in `definition/model`). Skeleton asserting the fields survive:

```go
package event_test

import (
	"testing"

	"github.com/kartaladev/wrkflw/definition/event"
	"github.com/kartaladev/wrkflw/definition/model"
)

func TestEndEventWireRoundTrip(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		node model.Node
	}{
		{"plain", event.NewEnd("done")},
		{"abort", event.NewEnd("halt", event.WithForceTermination("fraud", event.OutcomeAbort))},
		{"complete", event.NewEnd("stop", event.WithForceTermination("enough", event.OutcomeComplete))},
	}
	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			var w model.NodeWire
			model.NodeToWire(c.node, &w) // use the actual exported round-trip entry point
			got := model.NodeFromWire(w)  // ^ replace both with the real API from a neighbouring wire test
			gotEnd := got.(event.EndEvent)
			srcEnd := c.node.(event.EndEvent)
			if gotEnd.ForceTermination != srcEnd.ForceTermination ||
				gotEnd.TerminationReason != srcEnd.TerminationReason ||
				gotEnd.Outcome != srcEnd.Outcome {
				t.Fatalf("round-trip mismatch: got %+v, want %+v", gotEnd, srcEnd)
			}
		})
	}
}
```

> Implementer note: the exact round-trip entry point (`model.NodeToWire`/`NodeFromWire` vs a builder+JSON marshal) MUST match how the existing event kinds are round-trip-tested — read one existing wire test in `definition/model` or `definition/event` first and mirror it. The assertion logic above is correct regardless of mechanism.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./definition/event/...`
Expected: FAIL — `Outcome`/`TerminationReason` come back zero (ToWire/FromWire don't carry them yet), or compile error on missing `NodeWire` fields.

- [ ] **Step 3: Write minimal implementation**

In `definition/model/node_wire.go`, add the three fields near the other end-event/flag fields (after `ErrorCode`):

```go
	ForceTermination   bool   `json:"forceTermination,omitempty"`
	TerminationReason  string `json:"terminationReason,omitempty"`
	TerminationOutcome string `json:"terminationOutcome,omitempty"`
```

In `definition/event/event.go`, replace the `KindEndEvent` registration:

```go
	model.RegisterKind(model.KindEndEvent, model.NodeSpec{
		Name: "endEvent",
		FromWire: func(b model.Base, w model.NodeWire) model.Node {
			outcome := OutcomeComplete
			if w.TerminationOutcome == "abort" {
				outcome = OutcomeAbort
			}
			return EndEvent{
				Base:              b,
				ForceTermination:  w.ForceTermination,
				TerminationReason: w.TerminationReason,
				Outcome:           outcome,
			}
		},
		ToWire: func(n model.Node, w *model.NodeWire) {
			v := n.(EndEvent)
			w.ForceTermination, w.TerminationReason = v.ForceTermination, v.TerminationReason
			if v.ForceTermination {
				w.TerminationOutcome = v.Outcome.String()
			}
		},
	})
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./definition/event/...`
Expected: all three sub-cases PASS.

- [ ] **Step 5: Commit**

```bash
git add definition/model/node_wire.go definition/event/event.go definition/event/wire_end_test.go
git commit -m "feat(definition): wire round-trip for end-event force-termination (ADR-0119)"
```

---

## Task 3: Delete `TerminateEndEvent` (fold into `EndEvent`)

**Files:**
- Modify: `definition/event/event.go` (delete `TerminateEndEvent` struct ~40, `Kind()` ~44, `NewTerminateEnd` ~157, `RegisterKind(KindTerminateEndEvent)` ~240)
- Modify: `definition/model/definition.go` (delete `KindTerminateEndEvent` iota const ~20)
- Modify: `definition/model/validate.go` (isEnd predicate ~205)
- Modify: `definition/build/build.go` (delete `AddTerminateEndEvent` ~78; change `AddEndEvent` ~75 to options)
- Modify: `engine/step.go` (comment ~158), `engine/step_nodes.go` (comment ~58)
- Modify tests: `definition/model/node_test.go` (~59-67), `definition/model/nodekind_json_test.go` (~26)
- Test: add to `definition/model/nodekind_json_test.go` a RED assertion that `"terminateEndEvent"` no longer unmarshals.

**Interfaces:**
- Consumes: Tasks 1–2 (EndEvent now carries force-termination).
- Produces: `AddEndEvent(id string, opts ...event.EndOption) *Builder`; no `TerminateEndEvent`, `KindTerminateEndEvent`, `NewTerminateEnd`, `AddTerminateEndEvent`, or `"terminateEndEvent"` wire name anywhere.

- [ ] **Step 1: Write the failing test** (new behaviour: retired wire name errors)

Add to `definition/model/nodekind_json_test.go`:

```go
func TestNodeKindTerminateEndRetired(t *testing.T) {
	t.Parallel()
	var k model.NodeKind
	if err := k.UnmarshalJSON([]byte(`"terminateEndEvent"`)); err == nil {
		t.Fatal("UnmarshalJSON(\"terminateEndEvent\") = nil error, want error (kind retired)")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./definition/model/...`
Expected: FAIL — `terminateEndEvent` still resolves (its `RegisterKind` is still present), so `err == nil`.

- [ ] **Step 3: Delete the terminate-end machinery**

`definition/event/event.go`:
- Delete the `TerminateEndEvent` struct and its `Kind()` method.
- Delete `NewTerminateEnd`.
- Delete the `model.RegisterKind(model.KindTerminateEndEvent, ...)` block.
- Update the package doc comment (line ~1-5) to drop "terminate-end".

`definition/model/definition.go`: delete the `KindTerminateEndEvent` line from the iota block (safe — wire is name-based, ordinals are never persisted).

`definition/model/validate.go` (~205): change

```go
		isEnd := n.Kind() == KindEndEvent || n.Kind() == KindTerminateEndEvent || n.Kind() == KindErrorEndEvent
```

to

```go
		isEnd := n.Kind() == KindEndEvent || n.Kind() == KindErrorEndEvent
```

`definition/build/build.go`:
- Delete `AddTerminateEndEvent`.
- Change `AddEndEvent`:

```go
// AddEndEvent adds an EndEvent. Use event.WithName and event.WithForceTermination.
func (b *Builder) AddEndEvent(id string, opts ...event.EndOption) *Builder {
	return b.Add(event.NewEnd(id, opts...))
}
```

`engine/step.go` (~158) and `engine/step_nodes.go` (~58): remove `KindTerminateEndEvent` from the "unhandled/NOT-in-map" comments.

Fix the existing tests that reference the retired kind:
- `definition/model/node_test.go` (~59-67): delete the `KindTerminateEndEvent` assertion block (the `NewTerminateEnd` case). Keep the `KindEndEvent` case.
- `definition/model/nodekind_json_test.go` (~26): delete the `{model.KindTerminateEndEvent, `"terminateEndEvent"`}` table row.

Grep for any remaining references and fix them:

```bash
grep -rn "TerminateEnd\|KindTerminateEndEvent\|terminateEndEvent\|NewTerminateEnd\|AddTerminateEndEvent" --include="*.go" .
```

Any hit outside this task's edits (e.g. a scenario or another test using `NewTerminateEnd`) must be migrated to `event.NewEnd(id, event.WithForceTermination(reason, event.OutcomeAbort))`.

- [ ] **Step 4: Run tests to verify green**

Run: `go build ./... && go test ./definition/... ./engine/...`
Expected: build succeeds; the new `TestNodeKindTerminateEndRetired` PASSES; all other definition/engine tests PASS.

- [ ] **Step 5: Commit**

```bash
git add -A
git commit -m "refactor(definition): delete TerminateEndEvent kind, fold into EndEvent (ADR-0119)"
```

---

## Task 4: Engine — force-termination in `endEventStrategy.enter`

**Files:**
- Modify: `engine/step_nodes.go` (`endEventStrategy.enter` ~214)
- Test: `engine/end_force_termination_test.go` (create)

**Interfaces:**
- Consumes: `event.EndEvent` (Task 1); `InstanceState` helpers `cancelOpenTasks()`, `cancelAllTimers()`, `cancelAllArmsAndBoundaries()`, `removeAllEventSubprocessArms()`, `closeVisit`, `copyVars`; commands `CompleteInstance`, `FailInstance`, `CancelTimer`; statuses `StatusCompleted`, `StatusTerminated`.
- Produces: force-termination behaviour on `KindEndEvent` entry. `endEventStrategy.enter` returns `halt=true` on the force path (mirrors `errorEndEventStrategy`).

- [ ] **Step 1: Write the failing test**

Create `engine/end_force_termination_test.go`. Model a parallel fork: `start → fork(parallel) → {A: userTask parkA, B: end-force}`; when B is a force-termination end, the instance ends terminally and A's parked task is cancelled and timers/arms swept. Two cases (abort vs complete). Mirror the construction style of an existing engine test that drives a parallel fork with a parked user task (read one first, e.g. a `state_esp_test.go` or a parallel-gateway test, to copy the harness/driver setup and command-assertion helpers).

```go
package engine_test

import (
	"testing"

	"github.com/kartaladev/wrkflw/definition/event"
	// ... same imports the neighbouring engine behaviour tests use
)

func TestForceTerminationOutcome(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		endOpt  event.EndOption
		assert  func(t *testing.T, res /* the engine result/state type */ any)
	}{
		{
			name:   "abort",
			endOpt: event.WithForceTermination("fraud", event.OutcomeAbort),
			assert: func(t *testing.T, res any) {
				// Status == StatusTerminated
				// a FailInstance command with Err containing the reason was emitted
				// the sibling's open task command is Cancelled
				// cancelAllTimers / cancelAllArmsAndBoundaries commands present
			},
		},
		{
			name:   "complete",
			endOpt: event.WithForceTermination("enough", event.OutcomeComplete),
			assert: func(t *testing.T, res any) {
				// Status == StatusCompleted
				// a CompleteInstance command carrying the instance vars was emitted
				// the sibling's open task is Cancelled
			},
		},
	}
	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			// build def: start -> parallel fork -> {branchA userTask, branchB end(c.endOpt)}
			// drive to the point branchB's end is entered
			// c.assert(t, result)
		})
	}
}
```

> Implementer note: fill the harness from an existing engine parallel-fork test. The behavioural assertions above are the contract; do not weaken them. Assert on `InstanceState.Status` and the emitted `[]Command` slice (types `FailInstance`, `CompleteInstance`, and the task-cancel / `CancelTimer` commands).

- [ ] **Step 2: Run test to verify it fails**

Run: `go test -run TestForceTerminationOutcome ./engine/...`
Expected: FAIL — force-termination is not implemented; the def either completes normally (only branchB's token consumed) or the sibling task is not cancelled.

- [ ] **Step 3: Write minimal implementation**

At the very top of `endEventStrategy.enter` (before `currentScopeID := tok.ScopeID`), add:

```go
	if ev, ok := node.(event.EndEvent); ok && ev.ForceTermination {
		return forceTerminate(c, tok, ev)
	}
```

Add the helper (near `endEventStrategy`):

```go
// forceTerminate implements a force-termination end event (ADR-0119): it cancels
// all remaining parallel work — open tasks, timers, boundaries/arms, and event
// sub-process arms — then ends the instance at the outcome-selected status.
// It mirrors the immediate-termination tail of handleCancelRequested and returns
// halt=true so drive() exits immediately (the instance is terminal).
func forceTerminate(c *stepCtx, tok *Token, ev event.EndEvent) ([]Command, bool, error) {
	ended := c.at
	c.s.EndedAt = &ended
	// Close every open visit and drop all tokens (including this end-event token).
	for i := range c.s.Tokens {
		t := &c.s.Tokens[i]
		c.s.closeVisit(t.ID, t.NodeID, c.at)
	}
	c.s.Tokens = nil

	// Reconcile open human tasks before the terminal command (matches ADR-0088).
	cmds := c.s.cancelOpenTasks()

	if ev.Outcome == event.OutcomeAbort {
		c.s.Status = StatusTerminated
		reason := ev.TerminationReason
		if reason == "" {
			reason = "force-terminated"
		}
		cmds = append(cmds, FailInstance{Err: reason})
	} else {
		c.s.Status = StatusCompleted
		cmds = append(cmds, CompleteInstance{Result: copyVars(c.s.Variables)})
	}

	cmds = append(cmds, c.s.cancelAllTimers()...)
	cmds = append(cmds, c.s.cancelAllArmsAndBoundaries()...)
	for _, timerID := range c.s.removeAllEventSubprocessArms() {
		cmds = append(cmds, CancelTimer{TimerID: timerID})
	}
	return cmds, true, nil
}
```

> Implementer note: confirm `FailInstance`/`CompleteInstance` field names against the immediate-termination path in `engine/step_triggers.go:172-194` and the normal end path in `endEventStrategy` (`CompleteInstance{Result: copyVars(c.s.Variables)}`, `FailInstance{Err: ...}`). Confirm `closeVisit`'s signature (`closeVisit(tok.ID, tok.NodeID, at)`).

- [ ] **Step 4: Run test to verify it passes**

Run: `go test -run TestForceTerminationOutcome ./engine/...`
Expected: both sub-cases PASS.

- [ ] **Step 5: Run the full engine suite (no regressions)**

Run: `go test -race ./engine/...`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add engine/step_nodes.go engine/end_force_termination_test.go
git commit -m "feat(engine): force-termination end event cancels parallel work (ADR-0119)"
```

---

## Task 5: Multi-end WARN at registration (slog)

**Files:**
- Modify: `runtime/definition_registry.go`
- Test: `runtime/definition_registry_test.go`

**Interfaces:**
- Consumes: `*model.ProcessDefinition`, `event.EndEvent`, `model.KindEndEvent`; stdlib `log/slog`.
- Produces:
  - `func forceTerminationWarnings(def *model.ProcessDefinition) []string` (unexported, pure): returns one warning string per force-termination end event **iff** the definition has exactly one end event total (redundant case); empty otherwise.
  - `RegisterDefinition` and `MustRegisterDefinition` log each warning via `slog.Default().Warn(...)` after a *successful* registration.

- [ ] **Step 1: Write the failing test**

Add to `runtime/definition_registry_test.go`:

```go
func TestForceTerminationWarnings(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		build  func() *model.ProcessDefinition
		assert func(t *testing.T, warns []string)
	}{
		{
			name: "single-end force-termination is redundant",
			build: func() *model.ProcessDefinition {
				def, err := definition.NewBuilder("single", 1).
					AddStartEvent("s").
					AddEndEvent("e", event.WithForceTermination("x", event.OutcomeAbort)).
					Connect("s", "e").
					Build()
				require.NoError(t, err)
				return def
			},
			assert: func(t *testing.T, warns []string) {
				require.Len(t, warns, 1)
			},
		},
		{
			name: "multi-end force-termination is meaningful",
			build: func() *model.ProcessDefinition {
				def, err := definition.NewBuilder("multi", 1).
					AddStartEvent("s").
					AddParallelGateway("fork").
					AddUserTask("a").
					AddEndEvent("ea").
					AddEndEvent("halt", event.WithForceTermination("x", event.OutcomeAbort)).
					Connect("s", "fork").Connect("fork", "a").Connect("a", "ea").Connect("fork", "halt").
					Build()
				require.NoError(t, err)
				return def
			},
			assert: func(t *testing.T, warns []string) {
				require.Empty(t, warns)
			},
		},
		{
			name: "no force-termination, no warnings",
			build: func() *model.ProcessDefinition {
				def, err := definition.NewBuilder("plain", 1).
					AddStartEvent("s").AddEndEvent("e").Connect("s", "e").Build()
				require.NoError(t, err)
				return def
			},
			assert: func(t *testing.T, warns []string) {
				require.Empty(t, warns)
			},
		},
	}
	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			c.assert(t, runtime.ExportForceTerminationWarnings(c.build()))
		})
	}
}
```

> The pure helper is unexported; expose it to the black-box `runtime_test` package via a test-only export file `runtime/export_test.go` (`package runtime; func ExportForceTerminationWarnings(d *model.ProcessDefinition) []string { return forceTerminationWarnings(d) }`). Create that file as part of Step 3.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./runtime/... -run TestForceTerminationWarnings`
Expected: compile FAIL — `undefined: runtime.ExportForceTerminationWarnings` / `forceTerminationWarnings`.

- [ ] **Step 3: Write minimal implementation**

Create `runtime/export_test.go`:

```go
package runtime

import "github.com/kartaladev/wrkflw/definition/model"

// ExportForceTerminationWarnings exposes forceTerminationWarnings to black-box tests.
func ExportForceTerminationWarnings(d *model.ProcessDefinition) []string {
	return forceTerminationWarnings(d)
}
```

In `runtime/definition_registry.go`, add imports (`log/slog`, `github.com/kartaladev/wrkflw/definition/event`) and:

```go
// forceTerminationWarnings returns a non-fatal warning for each force-termination
// end event in a definition that has only a single end event: force-termination
// exists to cancel *other* branches, so on a single-end definition it is merely
// redundant. Definitions with ≥2 end events produce no warning.
func forceTerminationWarnings(def *model.ProcessDefinition) []string {
	if def == nil {
		return nil
	}
	var ends, forced []string
	for _, n := range def.Nodes {
		if n.Kind() != model.KindEndEvent {
			continue
		}
		ends = append(ends, n.ID())
		if ev, ok := n.(event.EndEvent); ok && ev.ForceTermination {
			forced = append(forced, n.ID())
		}
	}
	if len(ends) > 1 {
		return nil
	}
	var warns []string
	for _, id := range forced {
		warns = append(warns, fmt.Sprintf(
			"workflow-runtime: end event %q in definition %q forces termination but is the only end event; force-termination has no other branch to cancel (redundant)",
			id, def.ID))
	}
	return warns
}

// warnForceTermination logs each forceTerminationWarnings entry at WARN.
func warnForceTermination(def *model.ProcessDefinition) {
	for _, w := range forceTerminationWarnings(def) {
		slog.Default().Warn(w)
	}
}
```

Wire into both registration entry points:

```go
func RegisterDefinition(def *model.ProcessDefinition) error {
	if err := defaultDefinitionRegistry.Register(def); err != nil {
		return err
	}
	warnForceTermination(def)
	return nil
}

func MustRegisterDefinition(def *model.ProcessDefinition) {
	defaultDefinitionRegistry.MustRegister(def)
	warnForceTermination(def)
}
```

(Add the `fmt` import if not already present.)

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./runtime/... -run TestForceTerminationWarnings`
Expected: all three sub-cases PASS.

- [ ] **Step 5: Add a register-logs-WARN test**

Add a test that installs a capturing `slog` handler via `slog.SetDefault`, registers a single-end force-termination def through `runtime.RegisterDefinition` (into the global registry, using a unique ID/version to avoid the first-registration-wins clash), and asserts a WARN record was emitted. Restore the previous default logger with `t.Cleanup`. Do NOT `t.Parallel()` this one (it mutates the global default logger).

- [ ] **Step 6: Run and commit**

Run: `go test ./runtime/...`
Expected: PASS.

```bash
git add runtime/definition_registry.go runtime/export_test.go runtime/definition_registry_test.go
git commit -m "feat(runtime): WARN on redundant single-end force-termination at register (ADR-0119)"
```

---

## Task 6: Example scenario — `examples/scenarios/terminate_end`

**Files:**
- Create: `examples/scenarios/terminate_end/main.go`

**Interfaces:**
- Consumes: the public root API (`definition`, `definition/event`, `definition/activity`, `runtime`), `event.WithForceTermination`, `event.OutcomeAbort`, `event.OutcomeComplete`.

Per the `examples-dir-purpose` convention: examples demonstrate engine INTERNAL mechanics for a library consumer; do NOT wire in `processtest`/test helpers.

- [ ] **Step 1: Write the example `main.go`**

A parallel fork where one branch, on a business condition, force-terminates. Mirror the structure and doc-comment style of `examples/scenarios/manual_task/main.go` (package doc explaining the flow, a `run()` that builds the def, registers it, drives it, and prints the terminal status). Show:
- `NewEnd("halt", WithForceTermination("fraud detected", OutcomeAbort))` on one branch cancelling the sibling branch's in-flight user task (instance → `StatusTerminated`).
- A companion note/path showing `OutcomeComplete` → `StatusCompleted`.

- [ ] **Step 2: Verify it builds and runs**

Run: `go build ./examples/... && go run ./examples/scenarios/terminate_end`
Expected: builds; prints the terminal status (Terminated for the abort path).

- [ ] **Step 3: Commit**

```bash
git add examples/scenarios/terminate_end/main.go
git commit -m "docs(examples): terminate_end shows force-termination abort + complete (ADR-0119)"
```

---

## Task 7: ADR-0119

**Files:**
- Create: `docs/adr/0119-unified-end-event.md`

- [ ] **Step 1: Write the ADR** (Nygard template — Status/Date, Context, Decision, Consequences), using `docs/adr/0118-manual-user-task.md` for house style. Content mirrors the umbrella spec's ADR-0119 section: delete `TerminateEndEvent`; `EndEvent` gains `ForceTermination`/`TerminationReason`/`Outcome`; `WithForceTermination(reason, outcome)` with `OutcomeComplete`→`StatusCompleted` / `OutcomeAbort`→`StatusTerminated`; engine reuses the cancel-cleanup helpers; single-end redundancy = slog WARN at registration (not a hard error); clean wire break (`terminateEndEvent` name deleted, no migrators). Reference the umbrella spec `docs/specs/2026-07-10-bpmn2-alignment-design.md`.

- [ ] **Step 2: Commit**

```bash
git add docs/adr/0119-unified-end-event.md
git commit -m "docs(adr): ADR-0119 unified end event with force-termination"
```

---

## Final Verification

- [ ] `go test -race -coverprofile=cover.out ./... && go tool cover -func=cover.out | tail -1` — root green; touched packages (`definition/event`, `definition/model`, `definition/build`, `engine`, `runtime`) ≥ 85% line coverage.
- [ ] `go test ./...` from repo root — no regressions.
- [ ] `golangci-lint run ./...` — clean.
- [ ] `grep -rn "TerminateEnd\|terminateEndEvent" --include="*.go" .` — no matches (all folded/deleted).
- [ ] `go run ./examples/scenarios/terminate_end` — runs and prints Terminated.
- [ ] Whole-branch `/code-review` (high, multi-finder + opus composition) before merge — this includes an engine-behaviour change (Task 4), so a full reviewer pass is warranted per the risk-scale rule.

## Verification Checklist (spec coverage)

- [x] Delete `TerminateEndEvent`/`KindTerminateEndEvent`/`NewTerminateEnd`/`terminateEndEvent` wire — Task 3.
- [x] `EndEvent` gains `ForceTermination`, `TerminationReason`, `Outcome` — Task 1.
- [x] `WithForceTermination(reason, TerminationOutcome)`, both outcomes selectable — Task 1.
- [x] `NewEnd` gains `EndOption`; `WithName` name-only shim — Task 1.
- [x] Wire additive fields, round-tripped — Task 2.
- [x] First real terminate impl reusing `cancelAllTimers`/`cancelAllArmsAndBoundaries`/`removeAllEventSubprocessArms`/`cancelOpenTasks` — Task 4.
- [x] Multi-end-only rule = WARN, not hard error (decided: slog WARN at register) — Task 5.
- [x] `examples/scenarios/terminate_end` — Task 6.
- [x] ADR-0119 (Nygard) — Task 7.
