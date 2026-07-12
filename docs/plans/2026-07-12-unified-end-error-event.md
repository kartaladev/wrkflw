# Unified end/error event Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Fold `ErrorEndEvent` into `EndEvent` behind a single `EndBehavior` discriminator (`Normal|Terminate|Error`), so `KindEndEvent` is the sole end-event kind — matching BPMN's "end event carries at most one event definition."

**Architecture:** Replace ADR-0119's `EndEvent.ForceTermination bool` with an `EndBehavior` enum that also carries the new error behavior. `WithErrorCode` sets `EndError`; `WithForceTermination` sets `EndTerminate`. The engine's `endEventStrategy` switches on `Behavior`; the former `errorEndEventStrategy` body moves verbatim into the `EndError` branch. Wire uses one name-based `endBehavior` discriminator, replacing the `forceTermination` bool. Parity-first: port all call sites to the new API and get green, then delete the old kind.

**Tech Stack:** Go 1.25, `expr-lang/expr`, table tests (project `table-test` skill), black-box `_test` packages.

## Global Constraints

- Go 1.25; module path `github.com/kartaladev/wrkflw`.
- TDD strict: every new symbol gets a failing (RED) test run via `go test` BEFORE implementation. Pure deletions/refactors add no new test but must stay green before AND after.
- Prefer black-box tests (`package <pkg>_test`). Use `t.Context()` not `context.Background()`. Table tests use the `assert` closure form (project `table-test` skill), not `want`/`wantErr` fields.
- Error sentinels use the `"workflow-<pkg>: ..."` prefix (existing convention; none new expected here).
- Library is unreleased: no back-compat aliases or wire migrators. Clean breaks are expected.
- Ask before committing is waived within plan execution — commit at each task's final step.
- Coverage ≥ 85% line on touched packages; `golangci-lint run ./...` clean at the end.

---

### Task 1: `EndBehavior` enum + `String()`

Introduces the discriminator type standalone (no consumers yet), so its RED state is isolated.

**Files:**
- Modify: `definition/event/event.go` (add type near `TerminationOutcome`, ~line 48)
- Test: `definition/event/event_test.go`

**Interfaces:**
- Produces: `type EndBehavior int`; consts `EndNormal` (iota 0), `EndTerminate`, `EndError`; method `func (EndBehavior) String() string` → `"normal"`/`"terminate"`/`"error"`.

- [ ] **Step 1: Write the failing test**

Add to `definition/event/event_test.go`:

```go
func TestEndBehaviorString(t *testing.T) {
	t.Parallel()
	cases := map[string]struct {
		in   event.EndBehavior
		want string
	}{
		"normal":    {event.EndNormal, "normal"},
		"terminate": {event.EndTerminate, "terminate"},
		"error":     {event.EndError, "error"},
		"unknown":   {event.EndBehavior(99), "normal"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, c.want, c.in.String())
		})
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./definition/event/...`
Expected: FAIL — `undefined: event.EndBehavior` (build error).

- [ ] **Step 3: Write minimal implementation**

In `definition/event/event.go`, immediately after the `TerminationOutcome`
block (after line ~69):

```go
// EndBehavior selects what an EndEvent does when a token reaches it. It mirrors
// BPMN's optional end event definition — none, terminate, or error — which are
// mutually exclusive (an end event carries at most one).
type EndBehavior int

const (
	// EndNormal is a plain completion point (BPMN: no event definition).
	EndNormal EndBehavior = iota
	// EndTerminate force-terminates the whole instance (ADR-0119). Payload:
	// TerminationReason + Outcome.
	EndTerminate
	// EndError throws a workflow error caught by a boundary error event (BPMN
	// error end event). Payload: ErrorCode.
	EndError
)

// String returns the stable lowercase name ("normal"/"terminate"/"error"),
// used for wire encoding and logging.
func (b EndBehavior) String() string {
	switch b {
	case EndTerminate:
		return "terminate"
	case EndError:
		return "error"
	default:
		return "normal"
	}
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./definition/event/...`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add definition/event/event.go definition/event/event_test.go
git commit -m "feat(event): add EndBehavior discriminator enum (ADR-0127)"
```

---

### Task 2: Refactor terminate onto `Behavior` (behavior-preserving) + wire `endBehavior`

Replaces `EndEvent.ForceTermination bool` with `Behavior EndBehavior`, updates
every terminate consumer, and swaps the wire `forceTermination` bool for a
name-based `endBehavior` discriminator. No new *engine* behavior — but the wire
field rename IS observable, so its round-trip test is updated RED-first.

**Files:**
- Modify: `definition/event/event.go` (`EndEvent` struct ~76; `KindEndEvent` `FromWire`/`ToWire` ~296-317)
- Modify: `definition/event/options.go` (`forceTerminationOpt.applyEnd` ~276)
- Modify: `definition/model/node_wire.go` (swap `ForceTermination bool` for `EndBehavior string` ~57)
- Modify: `definition/model/yaml.go` (add `EndBehavior` to `nodeYAML` + its `toWire` copy)
- Modify: `engine/step_nodes.go` (`endEventStrategy.enter` guard ~218)
- Modify: tests referencing `.ForceTermination` / `forceTermination` wire key (see Step 2 grep)
- Test: `definition/event/wire_end_test.go` (or wherever the terminate round-trip lives)

**Interfaces:**
- Produces: `EndEvent.Behavior EndBehavior` (replaces `ForceTermination bool`); `EndEvent.Outcome`, `EndEvent.TerminationReason` unchanged. `NodeWire.EndBehavior string` (`json:"endBehavior,omitempty"`) replaces `NodeWire.ForceTermination bool`. Engine terminate guard becomes `ev.Behavior == event.EndTerminate`.

- [ ] **Step 1: Write/adjust the failing test**

Find the existing terminate wire round-trip test:

```bash
grep -rn "forceTermination\|ForceTermination\|WithForceTermination" definition/event/*_test.go
```

In the terminate round-trip test, change the wire-shape assertion from the
`forceTermination` bool to the new discriminator. Example (adapt to the actual
test's variable names):

```go
// after marshalling a NewEnd(id, WithForceTermination("boom", OutcomeAbort)):
assert.Equal(t, "terminate", wire.EndBehavior)
assert.Equal(t, "boom", wire.TerminationReason)
assert.Equal(t, "abort", wire.TerminationOutcome)
// round-trip back:
got := decoded.(event.EndEvent)
assert.Equal(t, event.EndTerminate, got.Behavior)
assert.Equal(t, event.OutcomeAbort, got.Outcome)
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./definition/event/...`
Expected: FAIL — `wire.EndBehavior` undefined and/or `got.Behavior` undefined (build error).

- [ ] **Step 3: Implement the refactor**

3a. `definition/model/node_wire.go` — replace the `ForceTermination bool` field
(line ~57) and its comment with:

```go
	// EndBehavior is the name-based discriminator for an EndEvent's behavior
	// (ADR-0127): "terminate" or "error"; empty means a normal end. It replaces
	// the former forceTermination bool. TerminationReason/TerminationOutcome are
	// written only for "terminate"; ErrorCode only for "error".
	EndBehavior           string             `json:"endBehavior,omitempty"`
```

3b. `definition/model/yaml.go` — add an `EndBehavior` field to `nodeYAML`
(alongside `ErrorCode`, ~line 45) and copy it into `NodeWire` in `toWire`
(~line 124):

```go
	EndBehavior           string          `yaml:"endBehavior,omitempty"`
```
```go
		EndBehavior:           ny.EndBehavior,
```

3c. `definition/event/event.go` — `EndEvent` struct (~76): replace
`ForceTermination bool` with `Behavior EndBehavior` (keep `TerminationReason`,
`Outcome`):

```go
type EndEvent struct {
	model.Base
	// Behavior selects what happens when a token reaches this end event
	// (ADR-0127). EndNormal (default) completes; EndTerminate force-terminates
	// the instance; EndError throws ErrorCode.
	Behavior EndBehavior
	// TerminationReason is recorded on EndTerminate (empty otherwise).
	TerminationReason string
	// Outcome selects the terminal status on EndTerminate. Ignored otherwise.
	Outcome TerminationOutcome
	// ErrorCode is the workflow error thrown on EndError ("" = anonymous
	// catch-all). Ignored unless Behavior == EndError.
	ErrorCode string
}
```

3d. `definition/event/event.go` — `KindEndEvent` `FromWire`/`ToWire` (~296-317):

```go
		FromWire: func(b model.Base, w model.NodeWire) model.Node {
			e := EndEvent{Base: b}
			switch w.EndBehavior {
			case "terminate":
				e.Behavior = EndTerminate
				e.TerminationReason = w.TerminationReason
				e.Outcome = OutcomeComplete
				if w.TerminationOutcome == "abort" {
					e.Outcome = OutcomeAbort
				}
			case "error":
				e.Behavior = EndError
				e.ErrorCode = w.ErrorCode
			}
			return e
		},
		ToWire: func(n model.Node, w *model.NodeWire) {
			v := n.(EndEvent)
			w.EndBehavior = ""
			switch v.Behavior {
			case EndTerminate:
				w.EndBehavior = "terminate"
				w.TerminationReason = v.TerminationReason
				w.TerminationOutcome = v.Outcome.String()
			case EndError:
				w.EndBehavior = "error"
				w.ErrorCode = v.ErrorCode
			}
		},
```

3e. `definition/event/options.go` — `forceTerminationOpt.applyEnd` (~276):

```go
func (o forceTerminationOpt) applyEnd(n *EndEvent) {
	n.Behavior = EndTerminate
	n.TerminationReason = o.reason
	n.Outcome = o.outcome
}
```

3f. `engine/step_nodes.go` — terminate guard (~218):

```go
	if ev, ok := node.(event.EndEvent); ok && ev.Behavior == event.EndTerminate {
		return forceTerminate(c, ev)
	}
```

3g. Fix remaining compile breaks from the field rename across the repo:

```bash
grep -rn "\.ForceTermination\b" --include="*.go" .
```
Replace each `x.ForceTermination` read with `x.Behavior == event.EndTerminate`
(engine/test code) and each write via the option. Update any `forceTerminationWarnings`
helper in runtime that inspects `.ForceTermination`.

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./definition/... ./engine/... ./runtime/...`
Expected: PASS (terminate behavior unchanged; wire shape now `endBehavior`).

- [ ] **Step 5: Commit**

```bash
git add -A
git commit -m "refactor(event)!: EndEvent.Behavior replaces ForceTermination; wire endBehavior (ADR-0127)"
```

---

### Task 3: `WithErrorCode` option + `EndError` model/wire/engine

Adds the error behavior end-to-end (option → wire → engine) so `NewEnd(id,
WithErrorCode(code))` throws and is caught, running IN PARALLEL with the still-live
`KindErrorEndEvent`. New symbols get RED-first tests.

**Files:**
- Modify: `definition/event/options.go` (new `WithErrorCode` + `errorCodeOpt`)
- Modify: `engine/step_nodes.go` (`endEventStrategy.enter` — add `EndError` branch)
- Test: `definition/event/options_test.go`, `engine/step_errorend_drive_test.go` (add new-API case)

**Interfaces:**
- Consumes: `EndEvent.Behavior`/`ErrorCode` (Task 2), `EndError` (Task 1), `forceTerminate` and `errorEndEventStrategy`'s propagateError call (existing).
- Produces: `func WithErrorCode(errorCode string) EndOption` → sets `Behavior=EndError`, `ErrorCode=errorCode`. `endEventStrategy.enter` handles `EndError` by throwing via `propagateError` and returning `halt=true`.

- [ ] **Step 1a: Write the failing option test**

Add to `definition/event/options_test.go`:

```go
func TestWithErrorCode(t *testing.T) {
	t.Parallel()
	n := event.NewEnd("e", event.WithErrorCode("ORDER_REJECTED")).(event.EndEvent)
	assert.Equal(t, event.EndError, n.Behavior)
	assert.Equal(t, "ORDER_REJECTED", n.ErrorCode)
}

func TestWithErrorCodeEmptyIsCatchAll(t *testing.T) {
	t.Parallel()
	n := event.NewEnd("e", event.WithErrorCode("")).(event.EndEvent)
	assert.Equal(t, event.EndError, n.Behavior) // still error behavior, not EndNormal
	assert.Empty(t, n.ErrorCode)
}
```

- [ ] **Step 1b: Run to verify it fails**

Run: `go test ./definition/event/...`
Expected: FAIL — `undefined: event.WithErrorCode`.

- [ ] **Step 1c: Implement the option**

In `definition/event/options.go`, near `forceTerminationOpt`:

```go
type errorCodeOpt struct{ code string }

func (o errorCodeOpt) applyEnd(n *EndEvent) {
	n.Behavior = EndError
	n.ErrorCode = o.code
}

// WithErrorCode makes an EndEvent throw a workflow error when reached (BPMN
// error end event). The error is caught by an enclosing boundary error event —
// matched by exact errorCode or, if errorCode is "", as an anonymous catch-all —
// otherwise it fails the instance. Mutually exclusive with WithForceTermination;
// if both are applied the last one wins.
func WithErrorCode(errorCode string) EndOption {
	return errorCodeOpt{code: errorCode}
}
```

Also extend the `WithForceTermination` doc comment with a "Mutually exclusive
with WithErrorCode; last one wins." sentence.

- [ ] **Step 1d: Run to verify it passes**

Run: `go test ./definition/event/...`
Expected: PASS.

- [ ] **Step 2a: Write the failing engine test**

In `engine/step_errorend_drive_test.go` (black-box `engine_test`), add a case
that authors the error end via the NEW API and asserts it is caught by a
boundary error event exactly as the old `NewErrorEnd` form. Model it on the
existing error-end drive test in the same file, but build the end node with
`event.NewEnd("err", event.WithErrorCode("BOOM"))`. Assert the instance routes
to the boundary recovery flow (not the default-completion path) — e.g. the
recovery ServiceTask's action is invoked and the instance ends non-Failed.

- [ ] **Step 2b: Run to verify it fails**

Run: `go test ./engine/ -run ErrorEnd`
Expected: FAIL — the `EndError` `EndEvent` falls through to the normal
per-scope completion branch (no throw), so the boundary is never hit.

- [ ] **Step 2c: Implement the `EndError` branch**

In `engine/step_nodes.go`, in `endEventStrategy.enter`, right after the
`EndTerminate` guard (~line 220), add:

```go
	if ev, ok := node.(event.EndEvent); ok && ev.Behavior == event.EndError {
		// Error end event (ADR-0127): throw ev.ErrorCode from the token's scope.
		// propagateError walks the scope chain to a matching boundary error
		// handler (may catch + recover) or fails the instance. Body preserved
		// verbatim from the former errorEndEventStrategy.
		currentScopeID := tok.ScopeID
		c.s.consumeToken(tok, c.at)
		errCmds, propErr := propagateError(c.def, c.s, currentScopeID, "", "", ev.ErrorCode, nil, c.at, c.mode, c.eval, false)
		if propErr != nil {
			return nil, false, propErr
		}
		return errCmds, true, nil
	}
```

- [ ] **Step 2d: Run to verify it passes**

Run: `go test ./engine/...`
Expected: PASS (both the new-API case and every existing `NewErrorEnd`-based
case pass — old kind still handled by `errorEndEventStrategy`).

- [ ] **Step 3: Commit**

```bash
git add definition/event/options.go definition/event/options_test.go engine/step_nodes.go engine/step_errorend_drive_test.go
git commit -m "feat(event): WithErrorCode + EndError behavior on EndEvent (ADR-0127)"
```

---

### Task 4: Parity port — migrate all `ErrorEndEvent` call sites to the new API

Rewrites every author-side and test-side use of the old kind to the unified
`EndEvent` form, keeping the whole suite green. The old kind stays defined (still
compiles) — only its *usages* move. This is the parity net before deletion.

**Files (all references — from the blast-radius grep):**
- Modify: `definition/build/build.go` (`AddErrorEndEvent` body → delegate; keep signature for now OR remove — see Step 1)
- Modify: `definition/README.md`
- Modify (tests): `internal/persistence/store/definitions_conformance_test.go`, `definition/model/node_test.go`, `definition/model/accessors_test.go`, `definition/model/nodekind_json_test.go`, `definition/build/build_test.go`, `definition/event/event_test.go`, `engine/step_boundaries_action_test.go`, `engine/step_errors_test.go`, `engine/step_errorend_drive_test.go`, `engine/reminder_interrupt_test.go`, `engine/boundary_error_matching_test.go`, `engine/step_nodes_test.go`

**Interfaces:**
- Consumes: `event.NewEnd`, `event.WithErrorCode` (Task 3), builder `AddEndEvent` (existing).
- Produces: zero remaining non-definition references to `NewErrorEnd`/`AddErrorEndEvent` at the call sites; all end-error nodes are now `KindEndEvent` with `Behavior==EndError`.

- [ ] **Step 1: Port constructor call sites (mechanical, uniform)**

Canonical transformation, apply everywhere:

```go
// before
event.NewErrorEnd("e", "CODE")            → event.NewEnd("e", event.WithErrorCode("CODE"))
event.NewErrorEnd("e", "CODE", "Name")    → event.NewEnd("e", event.WithErrorCode("CODE"), event.WithName("Name"))
b.AddErrorEndEvent("e", "CODE")           → b.AddEndEvent("e", event.WithErrorCode("CODE"))
b.AddErrorEndEvent("e", "CODE", "Name")   → b.AddEndEvent("e", event.WithErrorCode("CODE"), event.WithName("Name"))
```

Find every site: `grep -rn "NewErrorEnd\|AddErrorEndEvent" --include="*.go" .`
(exclude `definition/event/event.go` and `definition/build/build.go` definitions
themselves — those are deleted in Task 5).

- [ ] **Step 2: Port direct type / kind assertions in tests**

```bash
grep -rn "ErrorEndEvent\|KindErrorEndEvent" --include="*_test.go" .
```

Update each:
- `node.(event.ErrorEndEvent).ErrorCode` → `node.(event.EndEvent).ErrorCode`
- `n.Kind() == model.KindErrorEndEvent` → `n.Kind() == model.KindEndEvent` (plus,
  where the test distinguishes error ends, also assert `.Behavior == event.EndError`)
- `event.ErrorEndEvent{...}` struct literals → `event.NewEnd(id, event.WithErrorCode(code))`
  (or `event.EndEvent{Base: ..., Behavior: event.EndError, ErrorCode: code}`)
- In `nodekind_json_test.go`, the `"errorEndEvent"` name case: move its
  expectation to Task 5 (it becomes an "unknown kind" assertion once the wire
  registration is deleted). For now, if the test round-trips a `KindEndEvent`
  error node, assert wire `endBehavior == "error"` and `errorCode` set.

- [ ] **Step 3: Update `definition/README.md`**

Delete the `| KindErrorEndEvent | ... |` row; update the `KindEndEvent` row's
constructor cell to include `WithErrorCode(code)`:

```
| `KindEndEvent` | `event.NewEnd(id, opts...)` (`WithName`, `WithForceTermination(reason, outcome)`, `WithErrorCode(code)`) |
```

- [ ] **Step 4: Run the full suite green**

Run: `go test ./...`
Expected: PASS. `errorEndEventStrategy` still handles nothing new; the ported
sites now produce `KindEndEvent` nodes routed through the Task 3 `EndError`
branch.

- [ ] **Step 5: Commit**

```bash
git add -A
git commit -m "refactor(event): port all ErrorEndEvent call sites to WithErrorCode (ADR-0127)"
```

---

### Task 5: Delete the dead kind and all its plumbing

Pure removal — no behavior change, suite already green through the new API.
Removes the type, kind constant, constructors, engine strategy, wire
registration, and validation branch; updates comments.

**Files:**
- Modify: `definition/event/event.go` (delete `ErrorEndEvent` struct + `Kind()`; delete `NewErrorEnd`; delete `RegisterKind(model.KindErrorEndEvent, …)` block)
- Modify: `definition/model/definition.go` (delete `KindErrorEndEvent` iota const)
- Modify: `definition/model/validate.go` (`isEnd` → `KindEndEvent` only, ~276)
- Modify: `definition/build/build.go` (delete `AddErrorEndEvent`)
- Modify: `engine/step_nodes.go` (delete `errorEndEventStrategy` type + `nodeStrategies[model.KindErrorEndEvent]` entry ~70; update comment ~42)
- Modify: `engine/step.go` (update `ErrorEndEvent` comment ~151)
- Modify: `engine/step_errors.go` (update `KindErrorEndEvent` doc comments ~91/98)
- Modify: `definition/model/nodekind_json_test.go` (assert `"errorEndEvent"` now errors)

**Interfaces:**
- Produces: no remaining references to `ErrorEndEvent`, `KindErrorEndEvent`, `NewErrorEnd`, `AddErrorEndEvent`, `errorEndEventStrategy`, or the `errorEndEvent` wire name anywhere in the repo.

- [ ] **Step 1: Write the failing "unknown kind" test**

In `definition/model/nodekind_json_test.go`, replace the `"errorEndEvent"`
round-trip case with an assertion that decoding it now errors:

```go
func TestErrorEndEventWireNameRemoved(t *testing.T) {
	t.Parallel()
	_, err := model.NodeFromWire(model.NodeWire{ID: "e", Kind: "errorEndEvent"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "errorEndEvent")
}
```

(Adjust `model.NodeFromWire` to the actual decode entry point — grep
`func.*FromWire\|func NodeFrom` in `definition/model`.)

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./definition/model/ -run ErrorEndEventWireNameRemoved`
Expected: FAIL — `"errorEndEvent"` still decodes successfully (registration
present).

- [ ] **Step 3: Delete everything**

3a. `definition/event/event.go`: delete the `ErrorEndEvent` struct + its
`Kind()` method (~92-100), delete `NewErrorEnd` (~217-220), delete the
`RegisterKind(model.KindErrorEndEvent, …)` block (~318-322).

3b. `definition/model/definition.go`: delete the `KindErrorEndEvent` line from
the iota const block (~20).

3c. `definition/model/validate.go` (~276):

```go
	isEnd := n.Kind() == KindEndEvent
```

3d. `definition/build/build.go`: delete the `AddErrorEndEvent` method (~80).

3e. `engine/step_nodes.go`: delete the `errorEndEventStrategy` type + its
`enter` method (~742-773) and the `model.KindErrorEndEvent: errorEndEventStrategy{},`
registry line (~70). Update the comment at ~42 to name the `EndError` branch of
`endEventStrategy` as the halting case.

3f. `engine/step.go` (~151) and `engine/step_errors.go` (~91/98): update the
comments that say "ErrorEndEvent" / "KindErrorEndEvent" to reference an
"error-behavior end event".

- [ ] **Step 4: Verify no references remain, suite green**

```bash
grep -rn "ErrorEndEvent\|KindErrorEndEvent\|NewErrorEnd\|AddErrorEndEvent\|errorEndEventStrategy\|errorEndEvent" --include="*.go" .
```
Expected: only the new "unknown kind" test's string literal `"errorEndEvent"`.

Run: `go test ./...`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add -A
git commit -m "refactor(event)!: delete ErrorEndEvent kind, folded into EndEvent (ADR-0127)"
```

---

### Task 6: Verification & review gate

**Files:** none (verification only).

- [ ] **Step 1: Coverage + race on touched packages**

Run: `go test -race -coverprofile=cover.out ./... && go tool cover -func=cover.out | tail -1`
Expected: PASS, ≥ 85% line coverage; confirm `definition/event` and `engine`
are not regressed.

- [ ] **Step 2: Lint clean**

Run: `golangci-lint run ./...`
Expected: no findings.

- [ ] **Step 3: Whole-branch review**

Run `/code-review` over the branch; adjudicate and fix all findings (don't
auto-apply blindly). Then `/security-review`. Fix anything real.

- [ ] **Step 4: Final commit if review produced fixes**

```bash
git add -A
git commit -m "fix(event): address review findings for unified end/error event (ADR-0127)"
```

---

## Self-Review

**Spec coverage:**
- EndBehavior enum → Task 1. ✅
- `WithErrorCode` + removed `NewErrorEnd`/`AddErrorEndEvent` → Task 3 (add) + Task 5 (remove). ✅
- Struct field discriminator replacing `ForceTermination` → Task 2. ✅
- Wire `endBehavior` discriminator, `forceTermination` retired, `errorEndEvent` name errors → Task 2 (rename) + Task 5 (name removal). ✅
- Engine fold (verbatim error branch, delete `errorEndEventStrategy`) → Task 3 (add) + Task 5 (delete). ✅
- `isEnd` → `KindEndEvent` only → Task 5. ✅
- README + comments → Task 4 (README) + Task 5 (comments). ✅
- Parity-first ordering → Tasks 3→4→5. ✅
- Verification checklist (race/coverage/lint/reviews) → Task 6. ✅

**Placeholder scan:** engine test in Task 3 Step 2a is described (model-on-existing) rather than fully transcribed because it must mirror an existing file's harness the implementer will read in-place; the assertion contract is explicit. All other code steps carry literal code.

**Type consistency:** `EndBehavior`/`EndNormal`/`EndTerminate`/`EndError`, `EndEvent.Behavior`, `NodeWire.EndBehavior`, `WithErrorCode` used identically across Tasks 1–5. ✅
