# ADR-0120 — Dedicated CompensationThrowEvent (scope-wide + targeted) — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Give intra-process compensation its own node type. Add a dedicated `CompensationThrowEvent` (kind `KindCompensationThrowEvent`) that owns BOTH targeted (`CompensateRef` → archived sub-process records) and scope-wide (empty ref → the throwing scope's completed compensable activities) compensation throws; migrate the existing targeted throw out of `IntermediateThrowEvent`, leaving ITE for cross-boundary throws (signal today; message/error later).

**Why a dedicated type (design decision, supersedes the umbrella spec's "no new node kind"):** A signal throw broadcasts **cross-process/cross-instance** (`BroadcastSignal` → "every waiting instance"); compensation is strictly **intra-instance** (`RootCompensations`/`ArchivedCompensations` live on `InstanceState`). Bundling both blast radii in one `IntermediateThrowEvent` allows nonsensical `signal + compensate` nodes and obscures the constraint. The user chose the dedicated type (over a shared type + Build guard) for the strongest type-level expression of the intra-process constraint. The library is unreleased, so moving `CompensateRef` out of ITE is a clean break — no aliases/migrators.

**Architecture:** Reuse the engine's existing single-cursor compensation walk (`compensationCursor`, `cursorRecords`, `stepCompensationAdvance`, `stepCompensationFinish`, `DeferredCompensationThrows`). The new `compensationThrowEventStrategy` owns both throw forms. A scope-wide throw is a non-archive resuming walk (`ArchiveKey==""` → reads the throwing scope's records); on finish it resumes forward past the throw AND clears the drained records (compensate-once). A targeted throw ports the existing archive-walk logic verbatim.

**Tech Stack:** Go 1.25. No new dependencies. Compensation actions run through the existing `InvokeAction` command round-trip.

## Global Constraints

- **Go 1.25**; module `github.com/kartaladev/wrkflw`.
- **TDD strict** (CLAUDE.md): failing test (visible RED via `go test ./<pkg>/...`) before implementation, for every new symbol/behaviour. Deletion/refactor keeps existing tests green before AND after; new behaviour (e.g. the ITE→new-type wire-name change) gets its own RED first.
- **BPMN conformance is the baseline** (user directive): default behavior must match BPMN compensation-throw — throwing-scope, reverse order, throw-then-continue (non-terminating), compensate-once, no propagation to enclosing scopes. The configurable root record breadth (`ScopeLocal`) is an addition, off by default.
- **Black-box tests** (`package <pkg>_test`); pair each `.go` with a same-named `_test.go`. Table tests use the project `table-test` skill: `assert`-closure form, `t.Context()` over `context.Background()`.
- **Error sentinel prefix:** `workflow-<pkg>: ...`.
- **Clean break / unreleased:** additive wire field `compensateScopeLocal` (`omitempty`); the compensation wire discriminator moves from `intermediateThrowEvent`+`compensateRef` to the new `compensationThrowEvent` kind. No migrators.
- **Do NOT import** watermill/casbin/gocron/clockwork from engine/workflow code.
- **Naming/idioms:** always-on `cc-skills-golang` skills. Godoc every exported symbol. Follow existing compensation code structure (`engine/step_compensation.go`, `engine/step_nodes.go`).
- **Verification per touched package:** `go test -race ./... && go tool cover` ≥ 85%; `go test ./...` root green; `golangci-lint run ./...` clean.

---

## Background: the existing compensation machinery (READ before Task 1)

The implementer MUST read these — the feature reuses them:

- **`engine/step_nodes.go` `intermediateThrowEventStrategy.enter`** — today: `CompensateRef != ""` (targeted archive throw: reads `ArchivedCompensations[ref]`, starts a resuming walk or defers), `SignalName != ""` (signal), else park. Tasks 3 & 4 move the compensation branch to the new strategy and leave ITE with signal + park only.
- **`engine/step_compensation.go`**:
  - `compensationRecordsForScope(s, scopeID)` — `""` → `RootCompensations`; else `scopeByID(scopeID).Compensations`.
  - `cursorRecords(s, cur)` — `ArchiveKey != ""` → `ArchivedCompensations[ArchiveKey]`; else `compensationRecordsForScope(s, cur.ScopeID)`. **Scope-wide uses `ArchiveKey==""` — no new record-source code.**
  - `consolidateArchiveIntoRoot(s)` — merges `ArchivedCompensations` into `RootCompensations` (sorted), nils the archive. **Scope-wide-at-root default calls this (whole-instance).**
  - `stepCompensationAdvance` — emits the next reverse `InvokeAction` via `cursorRecords`, then `stepCompensationFinish`. **Unchanged.**
  - `stepCompensationFinish` `resumeNode != ""` branch — resumes forward (throw-then-continue), `deleteArchive: archiveKey`, RETAINS records. **Task 3 extends it to also clear the throwing scope's records when `ArchiveKey==""`.**
  - `applyFinish` pending-cancel block (~547) — comment assumes throw walks RETAIN `RootCompensations`; **Task 3 updates it for the scope-wide case.**
- **`compensationCursor`** fields: `ScopeID`, `ArchiveKey` (empty for scope-wide), `ResumeNode`, `ResumeScope`, `NextIndex`, `ActiveCmdID` (all value-scalars).
- **Migration surface (targeted-throw call sites → new type), from grep:** `runtime/scope_compensation_test.go:61`; `definition/model/validate_test.go:688,708,751`; `definition/model/node_test.go:276`; `definition/event/event_test.go:58,112`; `engine/step_compensation_throw_test.go:59,217,218,315,512,699`; `engine/step_compensation_parallel_throw_test.go:60`. **`event_test.go:58` combines `WithThrowSignalName` + `WithCompensateRef` on ONE node — impossible under the split; rework into the signal case only (or two nodes).** Signal-only throws (`WithThrowSignalName`, bare) STAY on `NewIntermediateThrow`.

---

## File Structure

- `definition/model/definition.go` — **Modify.** Add `KindCompensationThrowEvent` iota constant.
- `definition/model/node_wire.go` — **Modify.** Add `CompensateScopeLocal bool` (`omitempty`). `CompensateRef` already exists (reused by the new kind).
- `definition/event/event.go` — **Modify.** Add `CompensationThrowEvent` type + `Kind()` + `NewCompensateThrow` + its `RegisterKind`. Later (Task 4) remove `CompensateRef` from `IntermediateThrowEvent` and its RegisterKind ToWire/FromWire.
- `definition/event/options.go` — **Modify.** Add `CompensateThrowOption` + `WithCompensateRef`(new)/`WithScopeLocalCompensation`/`WithCompensateThrowName`. Later (Task 4) remove `WithCompensateRef` from `ThrowOption`.
- `definition/model/validate.go` — **Modify.** Add the CompensateRef-must-exist rule for `KindCompensationThrowEvent`. Later (Task 4) remove it from `KindIntermediateThrowEvent`.
- `definition/model/yaml.go` — **Modify.** Decode `compensateScopeLocal` (and the new kind's `compensateRef`) via the shared NodeWire path.
- `engine/step_nodes.go` — **Modify.** Add `compensationThrowEventStrategy` (targeted + scope-wide), register it. Later (Task 4) strip the compensation branch from `intermediateThrowEventStrategy`.
- `engine/step_compensation.go` — **Modify.** Extend the `resumeNode != ""` finish branch + the `applyFinish` pending-cancel block to clear the throwing scope's records for scope-wide walks.
- `definition/build/build.go` — **Modify.** Add `AddCompensationThrow`.
- `examples/scenarios/compensation_throw/main.go` — **Create.**
- `docs/adr/0120-dedicated-compensation-throw.md` — **Create.**

---

## Task 1: New `CompensationThrowEvent` model type + constructor + wire (additive)

**Files:**
- Modify: `definition/model/definition.go` (iota block), `definition/model/node_wire.go`
- Modify: `definition/event/event.go`, `definition/event/options.go`
- Modify: `definition/model/yaml.go`
- Test: `definition/event/compensation_throw_test.go` (create)

**Interfaces:**
- Produces:
  - `model.KindCompensationThrowEvent` (new iota constant, appended at the END of the block to avoid reordering churn — wire is name-based so ordinal doesn't matter, but appending minimizes diff).
  - `event.CompensationThrowEvent{ model.Base; CompensateRef string; ScopeLocal bool }` with `Kind() == KindCompensationThrowEvent`.
  - `event.NewCompensateThrow(id string, opts ...CompensateThrowOption) model.Node`.
  - `event.CompensateThrowOption` + `WithCompensateRef(ref) CompensateThrowOption`, `WithScopeLocalCompensation() CompensateThrowOption`, `WithCompensateThrowName(name) CompensateThrowOption`.
  - `NodeWire.CompensateScopeLocal bool` (`json:"compensateScopeLocal,omitempty"`).
  - RegisterKind `compensationThrowEvent` round-tripping `CompensateRef` + `ScopeLocal`.

- [ ] **Step 1: Write the failing test**

Create `definition/event/compensation_throw_test.go` covering construction (scope-wide default: empty ref, ScopeLocal false; scope-local; targeted via WithCompensateRef; name) AND a JSON round-trip (mirror `TestEventRoundTrip` in `event_test.go`) asserting `CompensateRef`/`ScopeLocal` survive and the node comes back as `event.CompensationThrowEvent`.

```go
package event_test

import (
	"testing"

	"github.com/kartaladev/wrkflw/definition/event"
	"github.com/kartaladev/wrkflw/definition/model"
)

func TestNewCompensateThrow(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		node   func() model.Node
		assert func(t *testing.T, n model.Node)
	}{
		{"scope-wide default", func() model.Node { return event.NewCompensateThrow("rb") },
			func(t *testing.T, n model.Node) {
				c := n.(event.CompensationThrowEvent)
				if c.CompensateRef != "" || c.ScopeLocal {
					t.Fatalf("want scope-wide whole-instance, got %+v", c)
				}
				if c.Kind() != model.KindCompensationThrowEvent {
					t.Fatalf("Kind = %v", c.Kind())
				}
			}},
		{"scope-local", func() model.Node { return event.NewCompensateThrow("rb", event.WithScopeLocalCompensation()) },
			func(t *testing.T, n model.Node) {
				if !n.(event.CompensationThrowEvent).ScopeLocal {
					t.Fatal("want ScopeLocal")
				}
			}},
		{"targeted", func() model.Node { return event.NewCompensateThrow("rb", event.WithCompensateRef("sub")) },
			func(t *testing.T, n model.Node) {
				if n.(event.CompensationThrowEvent).CompensateRef != "sub" {
					t.Fatal("want CompensateRef=sub")
				}
			}},
	}
	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) { t.Parallel(); c.assert(t, c.node()) })
	}
}
```

Add a `TestCompensationThrowWireRoundTrip` mirroring `TestEventRoundTrip` (scope-wide, scope-local, targeted cases).

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./definition/event/...`
Expected: compile FAIL — undefined `CompensationThrowEvent`, `NewCompensateThrow`, `WithScopeLocalCompensation`, `WithCompensateThrowName`, `model.KindCompensationThrowEvent`.

- [ ] **Step 3: Write minimal implementation**

`definition/model/definition.go` — append to the iota block:

```go
	KindEventBasedGateway
	KindCompensationThrowEvent // ADR-0120: dedicated intra-process compensation throw
)
```

`definition/model/node_wire.go` — add near `CompensateRef`:

```go
	CompensateScopeLocal bool `json:"compensateScopeLocal,omitempty"`
```

`definition/event/event.go` — add the type, constructor, and registration:

```go
// CompensationThrowEvent triggers intra-process compensation when reached. It
// runs completed compensable activities' compensation actions in reverse order,
// then continues past the throw (it does NOT terminate). With CompensateRef set
// it targets a specific completed sub-process node's archived records; empty
// CompensateRef is scope-wide (the throwing scope's completed compensable
// activities). Unlike a signal throw (IntermediateThrowEvent), compensation
// never crosses process boundaries.
type CompensationThrowEvent struct {
	model.Base
	// CompensateRef names a completed sub-process node whose archived compensation
	// records to run (targeted). Empty = scope-wide.
	CompensateRef string
	// ScopeLocal narrows a scope-wide throw at the ROOT scope to root-direct
	// compensable activities, excluding records archived from completed
	// sub-processes. Default false = whole-instance (BPMN-conformant). Ignored for
	// a targeted throw and at a sub-process scope.
	ScopeLocal bool
}

// Kind returns model.KindCompensationThrowEvent.
func (CompensationThrowEvent) Kind() model.NodeKind { return model.KindCompensationThrowEvent }

// NewCompensateThrow constructs a compensation throw. With no options it is a
// scope-wide, whole-instance throw; WithCompensateRef makes it targeted,
// WithScopeLocalCompensation narrows the root breadth, WithCompensateThrowName
// sets a display name.
func NewCompensateThrow(id string, opts ...CompensateThrowOption) model.Node {
	n := CompensationThrowEvent{Base: model.NewBase(id, "")}
	for _, o := range opts {
		o(&n)
	}
	return n
}
```

Add the `RegisterKind` in `init()`:

```go
	model.RegisterKind(model.KindCompensationThrowEvent, model.NodeSpec{
		Name: "compensationThrowEvent",
		FromWire: func(b model.Base, w model.NodeWire) model.Node {
			return CompensationThrowEvent{Base: b, CompensateRef: w.CompensateRef, ScopeLocal: w.CompensateScopeLocal}
		},
		ToWire: func(n model.Node, w *model.NodeWire) {
			v := n.(CompensationThrowEvent)
			w.CompensateRef, w.CompensateScopeLocal = v.CompensateRef, v.ScopeLocal
		},
	})
```

`definition/event/options.go` — add:

```go
// CompensateThrowOption configures a CompensationThrowEvent.
type CompensateThrowOption func(n *CompensationThrowEvent)

// WithCompensateRef targets the compensation throw at a specific completed
// sub-process node's archived records (empty = scope-wide).
func WithCompensateRef(ref string) CompensateThrowOption {
	return func(n *CompensationThrowEvent) { n.CompensateRef = ref }
}

// WithScopeLocalCompensation narrows a scope-wide throw at the root scope to
// root-direct compensable activities (default is whole-instance).
func WithScopeLocalCompensation() CompensateThrowOption {
	return func(n *CompensationThrowEvent) { n.ScopeLocal = true }
}

// WithCompensateThrowName sets the display name on a compensation throw.
func WithCompensateThrowName(name string) CompensateThrowOption {
	return func(n *CompensationThrowEvent) { n.SetName(name) }
}
```

> NOTE: `WithCompensateRef` currently exists as a `ThrowOption` (for ITE). Two symbols of the same name can't coexist in one package. Because Task 4 removes the ITE `WithCompensateRef`, for Task 1 name the new one distinctly TEMPORARILY if needed — BUT prefer: do the ITE `WithCompensateRef` removal (its ThrowOption form) here in Task 1's edit is out of scope. SIMPLEST: keep the ITE `WithCompensateRef` ThrowOption in place for now and name the new option `WithCompensateRef` is a COLLISION. Resolve by making the new option the canonical `WithCompensateRef` and DELETING the ITE `WithCompensateRef` ThrowOption in THIS task (it is only used by targeted throws, migrated in Task 4 — so removing the ITE option now would break those call sites early). To keep Task 1 additive-only, TEMPORARILY name the new one `WithCompensateTargetRef` and rename it to `WithCompensateRef` in Task 4 after the ITE option is deleted. Pick ONE approach and note it in the report; the test in Step 1 must match the name chosen.

`definition/model/yaml.go` — ensure `compensateScopeLocal` decodes (add the field to the YAML node struct if it enumerates fields explicitly; if it delegates to NodeWire, no change).

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./definition/event/... ./definition/model/...`
Expected: PASS. `go build ./...` still succeeds (additive — ITE unchanged).

- [ ] **Step 5: Commit**

```bash
git add definition/model/definition.go definition/model/node_wire.go definition/model/yaml.go definition/event/event.go definition/event/options.go definition/event/compensation_throw_test.go
git commit -m "feat(definition): add CompensationThrowEvent kind + NewCompensateThrow (ADR-0120)"
```

---

## Task 2: Validation for `KindCompensationThrowEvent`

**Files:**
- Modify: `definition/model/validate.go`
- Test: `definition/model/validate_test.go`

**Interfaces:**
- Produces: a non-empty `CompensateRef` on a `KindCompensationThrowEvent` must reference an existing node → else `ErrCompensateRefNotFound` (the existing sentinel, now also applied to the new kind). Empty ref (scope-wide) is always valid.

- [ ] **Step 1: Write the failing test** — add a `validate_test.go` case: a `CompensationThrowEvent` with `WithCompensateRef("no-such")` fails with `ErrCompensateRefNotFound`; with a valid ref (or empty/scope-wide) passes.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./definition/model/... -run Validate`
Expected: FAIL — the new kind isn't validated (a bad ref passes).

- [ ] **Step 3: Write minimal implementation** — in `validate.go`, extend the compensate-ref check to also cover `KindCompensationThrowEvent` (read the current `n.Kind() != KindIntermediateThrowEvent` guard at ~line 511 and broaden it to include the new kind; use `toWire(n).CompensateRef`). Leave the ITE check in place for now (removed in Task 4).

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./definition/model/...`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add definition/model/validate.go definition/model/validate_test.go
git commit -m "feat(definition): validate CompensateRef on CompensationThrowEvent (ADR-0120)"
```

---

## Task 3: Engine — `compensationThrowEventStrategy` (targeted port + scope-wide + compensate-once)

**Files:**
- Modify: `engine/step_nodes.go` (new strategy + register), `engine/step_compensation.go` (finish clearing)
- Test: `engine/compensation_throw_test.go` (create)

**Interfaces:**
- Consumes: `event.CompensationThrowEvent`; the compensation machinery (see Background). `nodeStrategies` map.
- Produces: `KindCompensationThrowEvent` entry behaviour — targeted (archive walk, ported from ITE) and scope-wide (consolidate-at-root-default / records / serialize / resume). `stepCompensationFinish` clears the throwing scope's records for scope-wide (`ArchiveKey==""`) walks (compensate-once).

- [ ] **Step 1: Write the failing test**

Create `engine/compensation_throw_test.go`. Read `engine/step_compensation_throw_test.go` for the harness/assert helpers. Cover:
1. **Scope-wide reverse order + resume:** root saga (two service tasks with `WithCompensateAction`), reach `NewCompensateThrow("rb")`; assert compensation `InvokeAction`s in reverse order, instance RESUMES at the throw's successor (not terminated), `RootCompensations` cleared, a second throw no-ops.
2. **Targeted parity:** a `NewCompensateThrow("t", WithCompensateRef("sub"))` reproduces the existing targeted-throw behaviour (mirror an existing `step_compensation_throw_test.go` case) against the NEW kind.
3. **Scope-local vs whole-instance breadth** (if a sub-process is wired): default compensates a closed sub-process's archived records; `WithScopeLocalCompensation` does not. (If heavy, cover breadth with a focused unit test on record selection.)
4. **Compensate-once:** after a scope-wide throw, a `CancelRequested` does NOT re-run the already-run compensations.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test -run TestCompensationThrow ./engine/...`
Expected: FAIL — `KindCompensationThrowEvent` has no strategy (parks as unhandled), so nothing compensates.

- [ ] **Step 3: Write minimal implementation**

In `engine/step_nodes.go`, register and implement `compensationThrowEventStrategy`. Its `enter` mirrors the ITE compensation logic for the targeted case and adds the scope-wide case:

```go
model.KindCompensationThrowEvent: compensationThrowEventStrategy{},
```

```go
type compensationThrowEventStrategy struct{}

func (compensationThrowEventStrategy) enter(c *stepCtx, tok *Token, node model.Node) ([]Command, bool, error) {
	cte, ok := node.(event.CompensationThrowEvent)
	if !ok {
		tok.State = TokenWaitingCommand
		return nil, false, nil
	}
	var cmds []Command
	resumeNode := ""
	if out := c.tdef.Outgoing(node.ID()); len(out) > 0 {
		resumeNode = out[0].Target
	}
	tokScope := tok.ScopeID

	if cte.CompensateRef != "" {
		// Targeted throw — archive walk (ported verbatim from the former ITE branch):
		// read ArchivedCompensations[ref], resume past the throw. Serialize/defer if a
		// walk is in flight; auto-advance if no records or no successor.
		// ... (port the exact logic from intermediateThrowEventStrategy's CompensateRef branch)
	} else {
		// Scope-wide throw (ADR-0120): compensate the throwing scope's completed
		// compensable activities in reverse, then resume forward.
		if tokScope == "" && !cte.ScopeLocal {
			c.s.consolidateArchiveIntoRoot() // whole-instance at root (BPMN default)
		}
		records := compensationRecordsForScope(c.s, tokScope)
		if len(records) == 0 || resumeNode == "" {
			c.s.moveAlongSingleFlow(c.tdef, tok, c.at) // nothing to do; auto-advance
		} else if c.s.Compensating.ActiveCmdID != "" {
			tok.State = TokenWaitingCommand
			c.s.DeferredCompensationThrows = append(c.s.DeferredCompensationThrows, tok.ID)
		} else {
			c.s.consumeToken(tok, c.at)
			c.s.Status = StatusCompensating
			cmdID := c.s.nextCommandID()
			c.s.Compensating = compensationCursor{
				ScopeID: tokScope, ResumeNode: resumeNode, ResumeScope: tokScope,
				NextIndex: len(records) - 1, ActiveCmdID: cmdID,
			}
			cmds = append(cmds, InvokeAction{CommandID: cmdID, Name: records[len(records)-1].Action, Input: copyVars(records[len(records)-1].Input)})
			tok.State = TokenWaitingCommand
		}
	}
	return cmds, false, nil
}
```

> Implementer note: PORT the targeted branch from `intermediateThrowEventStrategy` (the `CompensateRef != ""` block) verbatim — same `ArchivedCompensations[ref]`, `resumeNode`, serialize/defer, cursor with `ArchiveKey: ref`. The ITE branch stays in place until Task 4 (transient duplication is fine and removed there). Confirm helper/field names against source.

In `engine/step_compensation.go` `stepCompensationFinish`, extend the `resumeNode != ""` branch to clear the throwing scope's records when the walk is non-archive (scope-wide):

```go
	case resumeNode != "":
		plan = finishPlan{
			resume: true, resumeAt: resumeNode, resumeScope: resumeScope,
			deleteArchive: archiveKey, popDeferred: true, consumePendingCancel: true,
		}
		if archiveKey == "" {
			// Scope-wide compensate throw (ADR-0120): drained records came from the
			// throwing scope's live list, not an archive — clear them (compensate-once)
			// so a later cancel/rollback cannot re-run them. Targeted throws
			// (archiveKey != "") instead retain RootCompensations (unrelated outer records).
			plan.doClearRecords = true
			plan.clearScope = scopeID
		}
```

Update the `applyFinish` pending-cancel comment (~549-557) to note the scope-wide (archiveKey=="") case clears its scope's records here, whereas a targeted throw retains RootCompensations.

- [ ] **Step 4: Run test to verify it passes**

Run: `go test -run TestCompensationThrow ./engine/...`
Expected: PASS.

- [ ] **Step 5: Full engine suite (no regressions)**

Run: `go test -race ./engine/...`
Expected: PASS — existing `step_compensation*_test.go` (ITE targeted throws still work via the unchanged ITE branch), reverse-instance, ESP suites all green.

- [ ] **Step 6: Commit**

```bash
git add engine/step_nodes.go engine/step_compensation.go engine/compensation_throw_test.go
git commit -m "feat(engine): compensationThrowEventStrategy (targeted + scope-wide, compensate-once) (ADR-0120)"
```

---

## Task 4: Migrate targeted throws to the new type; remove compensation from `IntermediateThrowEvent`

**Files:**
- Modify: `definition/event/event.go` (drop `CompensateRef` from `IntermediateThrowEvent` + its RegisterKind), `definition/event/options.go` (remove ITE `WithCompensateRef` ThrowOption; finalize the new option name), `definition/model/validate.go` (remove ITE compensate-ref check), `engine/step_nodes.go` (strip the compensation branch from `intermediateThrowEventStrategy`)
- Migrate call sites (all from the grep survey): `runtime/scope_compensation_test.go`, `definition/model/validate_test.go`, `definition/model/node_test.go`, `definition/event/event_test.go`, `engine/step_compensation_throw_test.go`, `engine/step_compensation_parallel_throw_test.go`, `definition/build/build_test.go` (if any).

**Interfaces:**
- Produces: `IntermediateThrowEvent` no longer has `CompensateRef`; the sole compensation-throw type is `CompensationThrowEvent`; `WithCompensateRef` is a `CompensateThrowOption`.

- [ ] **Step 1: Write the failing test** (new behaviour: ITE no longer compensates)

Add to `definition/event/event_test.go` (or a model test) an assertion that a round-tripped/authored `IntermediateThrowEvent` has no compensation behaviour — e.g. the wire for a signal throw carries no `compensateRef`, and (engine) a former bare/park throw still parks. Simplest discriminating RED: assert `event.IntermediateThrowEvent` has no `CompensateRef` field by constructing a targeted throw ONLY via `NewCompensateThrow` and asserting `NewIntermediateThrow` has no compensate option (the `WithCompensateRef` ThrowOption is gone). This is mostly a compile-level contract; the meaningful RED is the migrated call sites failing to compile until updated.

- [ ] **Step 2: Run to verify RED** — Run `go build ./...`; expected FAIL at the migrated call sites once `CompensateRef`/ITE `WithCompensateRef` are removed (do the removal, then observe the breakage, then fix call sites).

- [ ] **Step 3: Implement the removal + migration**

1. `definition/event/event.go`: remove `CompensateRef` from `IntermediateThrowEvent`; update its `RegisterKind` FromWire/ToWire to drop `CompensateRef`; update `NewIntermediateThrow` godoc.
2. `definition/event/options.go`: remove the ITE `WithCompensateRef` ThrowOption; if Task 1 used a temporary name (`WithCompensateTargetRef`), rename it to `WithCompensateRef` now (update Task-1 test).
3. `definition/model/validate.go`: remove the `KindIntermediateThrowEvent` compensate-ref check (kept only for the new kind).
4. `engine/step_nodes.go`: remove the `CompensateRef != ""` branch from `intermediateThrowEventStrategy.enter` (leaving `SignalName != ""` and the `else` park). Update the strategy godoc.
5. Migrate every targeted-throw call site: `event.NewIntermediateThrow(id, event.WithCompensateRef(ref))` → `event.NewCompensateThrow(id, event.WithCompensateRef(ref))`. For `event_test.go:58` (signal + compensate on ONE node — now impossible): split into the signal assertion only (drop the compensate part, or add a separate `NewCompensateThrow` node/assertion).
6. Grep-clean check: `grep -rn "IntermediateThrow.*CompensateRef\|WithCompensateRef" --include="*.go" .` — every `WithCompensateRef` must now be on a `NewCompensateThrow`/`CompensateThrowOption`, none on `NewIntermediateThrow`.

- [ ] **Step 4: Verify GREEN** — Run `go build ./... && go test ./definition/... ./engine/... ./runtime/...`; expected all PASS.

- [ ] **Step 5: Commit**

```bash
git add -A
git commit -m "refactor: move compensation throw out of IntermediateThrowEvent into CompensationThrowEvent (ADR-0120)"
```

---

## Task 5: Builder — `AddCompensationThrow`

**Files:** Modify `definition/build/build.go`; Test `definition/build/build_test.go`.

**Interfaces:** `func (b *Builder) AddCompensationThrow(id string, opts ...event.CompensateThrowOption) *Builder`.

- [ ] **Step 1: Failing test** — `NewBuilder(...).AddCompensationThrow("rb")...Build()` yields an `event.CompensationThrowEvent`.
- [ ] **Step 2: Run — FAIL** (`undefined: AddCompensationThrow`).
- [ ] **Step 3: Implement**:

```go
// AddCompensationThrow adds a compensation throw. Use event.WithCompensateRef
// (targeted), event.WithScopeLocalCompensation, and event.WithCompensateThrowName.
func (b *Builder) AddCompensationThrow(id string, opts ...event.CompensateThrowOption) *Builder {
	return b.Add(event.NewCompensateThrow(id, opts...))
}
```

- [ ] **Step 4: Run — PASS.**
- [ ] **Step 5: Commit** — `feat(definition/build): AddCompensationThrow builder method (ADR-0120)`.

---

## Task 6: Example — `examples/scenarios/compensation_throw`

**Files:** Create `examples/scenarios/compensation_throw/main.go`.

Distinct from `compensation_saga`/`reverse_rollback`. Read `manual_task/main.go` + an existing compensation example for driver/action-catalog idioms (no test helpers).

- [ ] **Step 1:** Root saga: `reserveHotel` + `reserveCar` service tasks each with `activity.WithCompensateAction(...)`; a validation failure routes to `NewCompensateThrow("rollback")` that rolls both back in reverse order, then CONTINUES to a "notify customer" step (throw-then-continue, not terminating). Package doc explains the flow and the intra-process nature (contrast with a signal throw). End with "This is a reference wiring example — not a shipped binary."
- [ ] **Step 2:** `go build ./examples/... && go run ./examples/scenarios/compensation_throw` — builds; prints reverse-order rollback then continuation. Delete any stray root binary; don't commit it.
- [ ] **Step 3: Commit** — `docs(examples): compensation_throw shows scope-wide throw-then-continue (ADR-0120)`.

---

## Task 7: ADR-0120

**Files:** Create `docs/adr/0120-dedicated-compensation-throw.md` (Nygard; house style per `docs/adr/0119-unified-end-event.md`).

- [ ] **Step 1: Write the ADR.** Content:
  - **Context:** compensation is intra-instance; signal throw is cross-instance broadcast (`BroadcastSignal` → every waiting instance). Bundling both in `IntermediateThrowEvent` mixed blast radii and allowed nonsensical signal+compensate nodes. The empty-`CompensateRef` scope-wide throw was stubbed/parked.
  - **Decision:** dedicated `CompensationThrowEvent` kind owns BOTH targeted and scope-wide compensation; ITE keeps cross-boundary throws (signal today; message/error later). This **supersedes the umbrella spec's "no new node kind"** — chosen by the user for the strongest type-level expression of the intra-process constraint; unreleased library ⇒ clean break, `CompensateRef` moved off ITE. BPMN-conformant defaults: throwing-scope, reverse order, throw-then-continue (non-terminating), compensate-once, no enclosing-scope propagation; reuses the single-cursor walk + `DeferredCompensationThrows`. Root default = whole-instance (consolidate archived sub-process records); `WithScopeLocalCompensation()` narrows to root-direct.
  - **Consequences:** new wire kind `compensationThrowEvent` + additive `compensateScopeLocal`; every targeted-throw author moves from `NewIntermediateThrow(WithCompensateRef)` to `NewCompensateThrow(WithCompensateRef)`; a node can no longer be both a signal and a compensation throw. Documented limitation: sub-process-scope throws compensate that scope's direct records only (nested closed-sub-process cascade not done — archive map isn't scope-partitioned). Targeted-throw + full-rollback runtime behaviour unchanged.
  - Reference the umbrella spec + this plan.
- [ ] **Step 2: Commit** — `docs(adr): ADR-0120 dedicated compensation throw`.

---

## Final Verification

- [ ] `go test -race -coverprofile=cover.out ./... && go tool cover -func=cover.out | tail -1` — root green; touched packages (`definition/event`, `definition/model`, `definition/build`, `engine`) ≥ 85%.
- [ ] `go test ./...` root — no regressions (compensation, reverse-instance, ESP, signal-throw suites).
- [ ] `golangci-lint run ./...` clean.
- [ ] `grep -rn "WithCompensateRef" --include="*.go" .` — every hit is a `CompensateThrowOption` on `NewCompensateThrow`/`AddCompensationThrow`; none on `NewIntermediateThrow`.
- [ ] `go run ./examples/scenarios/compensation_throw` — reverse-order rollback then continues.
- [ ] Whole-branch `/code-review` (high, multi-finder + opus composition) before merge — engine-behaviour + a type-split refactor touching compensation; point it at BPMN conformance (reverse order, throw-then-continue, compensate-once, no enclosing-scope propagation), targeted-throw parity vs the old ITE path, and the signal-throw non-regression.

## Verification Checklist (spec coverage)

- [x] Dedicated `CompensationThrowEvent` kind owning targeted + scope-wide — Tasks 1, 3, 4.
- [x] `NewCompensateThrow` + `WithCompensateRef`/`WithScopeLocalCompensation`/`WithCompensateThrowName` — Task 1.
- [x] Wire round-trip (`compensationThrowEvent` + `compensateScopeLocal`) — Task 1.
- [x] Validation of `CompensateRef` for the new kind — Task 2.
- [x] Scope-wide producer + throw-then-continue + compensate-once — Task 3.
- [x] Compensation removed from `IntermediateThrowEvent`; targeted throws migrated — Task 4.
- [x] `WithScopeLocalCompensation` configurable breadth (default whole-instance/BPMN) — Tasks 1, 3.
- [x] Builder — Task 5.
- [x] Example — Task 6.
- [x] ADR-0120 (Nygard) — Task 7.
