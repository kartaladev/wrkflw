# Event-based Start Events (ADR-0121) — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Let a process instance be started by a message, a signal, or a timer — BPMN-faithfully — through the existing `DeliverMessage`/`BroadcastSignal`/scheduler entry points, with no new public methods and no durable subscription store.

**Architecture:** An `internal`-to-`runtime` `eventStart` collaborator composed into `ProcessDriver` holds correlate-then-create / fan-out / node-resolution. Both delivery facades become def-less publishers: `DeliverMessage` correlates-to-running-then-creates from the unique message-start; `BroadcastSignal` resumes waiters + fans out to every matching signal-start. Timer-starts arm the scheduler at boot. Definitions are enumerated through a new opt-in `DefinitionLister` capability — the definition is the source of truth; nothing new is persisted.

**Tech Stack:** Go 1.25, `expr-lang/expr`, `go-co-op/gocron` (via `scheduling.Scheduler`/`Elector`), `jonboulle/clockwork` (via `clock.Clock`), `internal/persistence/dialect.Locker` for advisory locking, testify + `runtimetest` helpers.

## Global Constraints

- Go 1.25; single module `github.com/zakyalvan/krtlwrkflw`; public packages at repo root (no `pkg/`).
- **TDD strict** (CLAUDE.md): no production code before a failing test; visible RED via `go test ./<pkg>/...` before GREEN. One test file per `.go` (`_test.go`), black-box (`<pkg>_test`).
- Custom skills override generic: `table-test` (assert-closure form, `t.Context()` over `context.Background()`), `use-mockgen` (`--typed`), `use-testcontainers` (`database.RunTestDatabase`). Always-on Go skills per CLAUDE.md; start each task from `cc-skills-golang:golang-how-to`.
- Never import watermill/casbin/gocron/clockwork directly from engine/workflow code — go through the in-repo abstraction.
- Error sentinels use the `workflow-<pkg>: …` prefix.
- Library is **unreleased** ⇒ clean breaks allowed (no wire aliases, no deprecation shims).
- Per touched package: `go test -race` green, ≥85% line coverage; `go test ./...` green; `golangci-lint run ./...` clean.
- Spec: `docs/specs/2026-07-10-event-based-start-design.md` (authoritative; section refs below are to it).

---

## File Structure

- `definition/model/validate.go` — relax start-count rules; union reachability; new sentinels (Task 1).
- `engine/trigger.go` — `StartInstance.StartNodeID` (Task 2).
- `engine/step_triggers.go` — generalize `handleStartInstance` node resolution; lift invariant; `ErrNoManualStart` (Task 2).
- `runtime/kernel/definition_lister.go` (new) — `DefinitionLister` capability (Task 3).
- `runtime/kernel/mem_definition_registry.go`, `runtime/kernel/definition_registry.go`, `runtime/kernel/caching_definition_registry.go` — implement/passthrough `ListDefinitions` (Task 3).
- `runtime/event_start.go` (new) — `eventStart` unit: start-node resolution helpers + active-correlation map (Task 4).
- `runtime/processdriver_message.go` — `DeliverMessage` def-drop + correlate-then-create (Task 5).
- `runtime/processdriver.go` — compose `eventStart`; terminal eviction hook in `deliverLoop`; create-at-node helper (Tasks 4–5).
- `service/service.go`, `service/request.go`, `transport/http/{httpcore,stdlib,gin,fiber}` — propagate `DeliverMessage` def-drop; drop `DeliverMessageRequest.DefRef` (Task 5).
- `runtime/processdriver_signal.go` — `BroadcastSignal` fan-out create (Task 6).
- `runtime/definition_registry.go` — message-name uniqueness at register; delivery-time backstop (Task 7).
- `runtime/timerops.go` — `RehydrateStartTimers`; timer-start fire creates instance (Task 8).
- `runtime/processdriver.go` (`Drive`) — plain-drive manual-start resolution / error (Task 8).
- `examples/scenarios/event_start/main.go` (new) + 7 existing example call sites (Task 9).
- `docs/adr/0121-event-based-start.md` (new) (Task 10).

---

## Task 1: Validation relaxation for multiple / event starts

**Files:**
- Modify: `definition/model/validate.go`
- Test: `definition/model/validate_test.go`

**Interfaces:**
- Produces: sentinels `ErrMultipleManualStarts`, `ErrAmbiguousStartTrigger` (exported `error` vars); `ErrMultipleStartEvents` is **removed** (multiple starts now legal). Helper `isNoneStart(event.StartEvent) bool` (unexported) and `startTriggerKind(event.StartEvent)` semantics documented below.
- Consumes: `event.StartEvent` fields `MessageName`, `SignalName`, `Timer`, `CorrelationKey`; `d.StartNodes()`.

A start is a **manual-start** when `MessageName == "" && SignalName == "" && Timer == nil`. A start is **event-triggered** otherwise; it must set exactly one trigger family (message: `MessageName != ""`; signal: `SignalName != ""`; timer: `Timer != nil`) — two or more set ⇒ `ErrAmbiguousStartTrigger`.

- [ ] **Step 1: Write failing tests**

Add to `definition/model/validate_test.go` (table-test, assert-closure form):

```go
func TestValidateStartEvents(t *testing.T) {
	tests := map[string]struct {
		def    *model.ProcessDefinition
		assert func(t *testing.T, err error)
	}{
		"two manual starts rejected": {
			def: twoManualStartDef(),
			assert: func(t *testing.T, err error) {
				assert.ErrorIs(t, err, model.ErrMultipleManualStarts)
			},
		},
		"one none + one message start allowed": {
			def: noneAndMessageStartDef(),
			assert: func(t *testing.T, err error) { assert.NoError(t, err) },
		},
		"two event starts allowed": {
			def: signalAndTimerStartDef(),
			assert: func(t *testing.T, err error) { assert.NoError(t, err) },
		},
		"message start without name rejected": {
			def: messageStartMissingNameDef(),
			assert: func(t *testing.T, err error) {
				assert.ErrorIs(t, err, model.ErrEventStartMissingTrigger)
			},
		},
		"start with signal and timer rejected": {
			def: signalPlusTimerOneNodeDef(),
			assert: func(t *testing.T, err error) {
				assert.ErrorIs(t, err, model.ErrAmbiguousStartTrigger)
			},
		},
		"node reachable from a non-first start is not unreachable": {
			def: twoStartsBothReachDef(),
			assert: func(t *testing.T, err error) {
				assert.NotErrorIs(t, err, model.ErrUnreachableNode)
			},
		},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			tc.assert(t, model.Validate(tc.def))
		})
	}
}
```

Add the small `*Def()` builders in the test file using `definition/event` + `definition/flow` (mirror existing builders already in `validate_test.go`). `ErrEventStartMissingTrigger` is a new sentinel (below).

- [ ] **Step 2: Run — verify RED**

Run: `go test ./definition/model/... -run TestValidateStartEvents -v`
Expected: FAIL — `undefined: model.ErrMultipleManualStarts` (and siblings), or assertion failures.

- [ ] **Step 3: Implement**

In `validate.go` add sentinels near the existing block (line ~38):

```go
// ErrMultipleManualStarts is returned when a definition has more than one
// trigger-less ("none") start event; at most one is allowed.
ErrMultipleManualStarts = errors.New("workflow-definition: multiple manual start events")
// ErrAmbiguousStartTrigger is returned when a start event sets more than one
// trigger family (message/signal/timer).
ErrAmbiguousStartTrigger = errors.New("workflow-definition: start event has ambiguous trigger")
// ErrEventStartMissingTrigger is returned when an event start declares a trigger
// family incompletely (e.g. a message start with no message name).
ErrEventStartMissingTrigger = errors.New("workflow-definition: event start missing trigger detail")
```

Delete `ErrMultipleStartEvents` and its use. Replace the `switch` at ~194:

```go
starts := d.StartNodes()
if len(starts) == 0 {
	errs = append(errs, ErrNoStartEvent)
}
var manualCount int
for _, s := range starts {
	se, ok := s.(event.StartEvent)
	if !ok {
		continue
	}
	fams := 0
	if se.MessageName != "" {
		fams++
	}
	if se.SignalName != "" {
		fams++
	}
	if se.Timer != nil {
		fams++
	}
	switch {
	case fams == 0:
		manualCount++
	case fams > 1:
		errs = append(errs, fmt.Errorf("%w: node %q", ErrAmbiguousStartTrigger, se.ID()))
	}
	// A message start with a correlation key but no name, etc., is caught as fams==0
	// only when nothing is set; a partially-set family (none possible here since a
	// family is defined by its own field being non-empty) needs no extra check.
}
if manualCount > 1 {
	errs = append(errs, ErrMultipleManualStarts)
}
```

Replace the reachability gate at ~286 (was `len(starts) == 1`) with a **union over all starts**:

```go
if starts := d.StartNodes(); len(starts) > 0 {
	reached = map[string]bool{}
	for _, s := range starts {
		for id := range forwardReachable(d, s.ID()) {
			reached[id] = true
		}
	}
}
```

(Adjust `reached` type to `map[string]bool` if `forwardReachable` returns a set; keep the existing `reached == nil` skip semantics for the 0-start case.)

- [ ] **Step 4: Run — verify GREEN**

Run: `go test ./definition/model/... -v` then `go test ./definition/...`
Expected: PASS. Fix any pre-existing test that asserted `ErrMultipleStartEvents` (repoint to the new rules).

- [ ] **Step 5: Commit**

```bash
git add definition/model/validate.go definition/model/validate_test.go
git commit -m "feat(definition): allow multiple/event start events in validation (ADR-0121)"
```

---

## Task 2: Engine multi-start seam — `StartNodeID` on the trigger

**Files:**
- Modify: `engine/trigger.go` (`StartInstance`, `NewStartInstance`)
- Modify: `engine/step_triggers.go` (`handleStartInstance`)
- Test: `engine/step_triggers_test.go` (or a new `engine/start_event_test.go`)

**Interfaces:**
- Produces: `StartInstance.StartNodeID string`; `NewStartInstance(at, vars)` keeps today's signature (StartNodeID defaults `""`); new option `NewStartInstanceAt(at, nodeID, vars)` **or** an exported field set by the caller. Use an exported field + a variadic-free helper: add `func NewStartInstanceAtNode(at time.Time, nodeID string, vars map[string]any) StartInstance`. Sentinel `ErrNoManualStart` (engine).
- Consumes: `def.StartNodes()`, `s.placeToken(id, at)`.

Empty `StartNodeID` ⇒ resolve the sole **manual-start** (a start with no message/signal/timer). No manual-start ⇒ `ErrNoManualStart`. Non-empty ⇒ place token there (must be a start node).

- [ ] **Step 1: Write failing tests**

```go
func TestHandleStartInstanceResolvesNode(t *testing.T) {
	tests := map[string]struct {
		def    *model.ProcessDefinition
		nodeID string
		assert func(t *testing.T, st engine.InstanceState, err error)
	}{
		"empty node id uses the manual start": {
			def: oneNoneStartLinearDef(), nodeID: "",
			assert: func(t *testing.T, st engine.InstanceState, err error) {
				require.NoError(t, err)
				assert.Equal(t, engine.StatusCompleted, st.Status)
			},
		},
		"explicit node id seeds that start": {
			def: twoStartsDef(), nodeID: "msgStart",
			assert: func(t *testing.T, st engine.InstanceState, err error) {
				require.NoError(t, err)
				// token placed on msgStart's downstream path
			},
		},
		"empty node id with only event starts errors": {
			def: onlyMessageStartDef(), nodeID: "",
			assert: func(t *testing.T, st engine.InstanceState, err error) {
				assert.ErrorIs(t, err, engine.ErrNoManualStart)
			},
		},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			st := engine.InstanceState{InstanceID: "i1"}
			out, err := engine.Step(tc.def, st, 0,
				engine.NewStartInstanceAtNode(time.Unix(0, 0), tc.nodeID, nil), engine.StepOptions{})
			tc.assert(t, out.State, err)
		})
	}
}
```

(Use the actual `engine.Step` entry the package exposes; mirror an existing `step_triggers_test.go` invocation for the exact call shape.)

- [ ] **Step 2: Run — verify RED**

Run: `go test ./engine/... -run TestHandleStartInstanceResolvesNode -v`
Expected: FAIL — `undefined: engine.NewStartInstanceAtNode` / `engine.ErrNoManualStart`.

- [ ] **Step 3: Implement**

`engine/trigger.go`:

```go
type StartInstance struct {
	baseTrigger
	Vars map[string]any
	// StartNodeID is the start node to seed. Empty resolves the definition's
	// manual (trigger-less) start; non-empty seeds that specific start node.
	StartNodeID string
}

func NewStartInstance(at time.Time, vars map[string]any) StartInstance {
	return StartInstance{baseTrigger: baseTrigger{at: at}, Vars: vars}
}

func NewStartInstanceAtNode(at time.Time, nodeID string, vars map[string]any) StartInstance {
	return StartInstance{baseTrigger: baseTrigger{at: at}, Vars: vars, StartNodeID: nodeID}
}
```

`engine/step_triggers.go` — add sentinel and generalize `handleStartInstance`:

```go
// ErrNoManualStart is returned when a StartInstance with an empty StartNodeID is
// applied to a definition that has no manual (trigger-less) start event.
var ErrNoManualStart = errors.New("workflow-engine: definition has no manual start event")

func handleStartInstance(def *model.ProcessDefinition, s *InstanceState, t StartInstance, opt StepOptions) (StepResult, error) {
	s.Status = StatusRunning
	s.StartedAt = t.OccurredAt()
	s.DefID = def.ID
	s.DefVersion = def.Version
	mergeVars(s, t.Vars)
	s.StartVariables = copyVars(s.Variables)

	startID := t.StartNodeID
	if startID == "" {
		n, err := resolveManualStart(def)
		if err != nil {
			return StepResult{}, err
		}
		startID = n
	}
	s.placeToken(startID, t.OccurredAt())
	// ... unchanged: armEventSubprocesses + drive ...
}

// resolveManualStart returns the id of the definition's single manual (trigger-less)
// start event, or ErrNoManualStart if there is none.
func resolveManualStart(def *model.ProcessDefinition) (string, error) {
	for _, s := range def.StartNodes() {
		se, ok := s.(event.StartEvent)
		if !ok {
			continue
		}
		if se.MessageName == "" && se.SignalName == "" && se.Timer == nil {
			return se.ID(), nil
		}
	}
	return "", ErrNoManualStart
}
```

Remove the `len(starts) != 1` guard. Import `errors` and `definition/event` if not present.

- [ ] **Step 4: Run — verify GREEN**

Run: `go test ./engine/... -v`
Expected: PASS. Existing single-manual-start drives still pass (empty StartNodeID → resolveManualStart).

- [ ] **Step 5: Commit**

```bash
git add engine/trigger.go engine/step_triggers.go engine/*_test.go
git commit -m "feat(engine): StartNodeID on StartInstance; resolve manual-start (ADR-0121)"
```

---

## Task 3: `DefinitionLister` enumeration capability

**Files:**
- Create: `runtime/kernel/definition_lister.go`
- Modify: `runtime/kernel/mem_definition_registry.go`, `runtime/kernel/definition_registry.go` (Map), `runtime/kernel/caching_definition_registry.go`
- Test: `runtime/kernel/definition_lister_test.go`

**Interfaces:**
- Produces: `type DefinitionLister interface { ListDefinitions(ctx context.Context) []*model.ProcessDefinition }`. `*MemDefinitionRegistry`, `*MapDefinitionRegistry`, `*CachingDefinitionRegistry` implement it. Listed definitions are **deduplicated by pinned Qualifier** (both stores index each def under `<ID>` and `<ID>:<Version>` — list each concrete def once).
- Consumes: internal maps of each registry.

- [ ] **Step 1: Write failing test**

```go
func TestMemDefinitionRegistryListsDistinct(t *testing.T) {
	reg := kernel.NewMemDefinitionRegistry()
	require.NoError(t, reg.Register(defWithID(t, "A", 1)))
	require.NoError(t, reg.Register(defWithID(t, "B", 1)))

	var lister kernel.DefinitionLister = reg
	got := lister.ListDefinitions(t.Context())

	ids := make([]string, 0, len(got))
	for _, d := range got {
		ids = append(ids, d.ID)
	}
	assert.ElementsMatch(t, []string{"A", "B"}, ids) // each concrete def once, not 4
}
```

- [ ] **Step 2: Run — verify RED**

Run: `go test ./runtime/kernel/... -run TestMemDefinitionRegistryListsDistinct -v`
Expected: FAIL — `kernel.DefinitionLister` undefined / `ListDefinitions` not implemented.

- [ ] **Step 3: Implement**

`definition_lister.go`:

```go
package kernel

import (
	"context"
	"github.com/zakyalvan/krtlwrkflw/definition/model"
)

// DefinitionLister is an OPTIONAL capability a DefinitionRegistry may implement
// so the event-start subsystem can find definitions subscribing to a message,
// signal, or timer start. Registries that do not implement it disable
// event-based *start* (correlate-to-running still works).
type DefinitionLister interface {
	// ListDefinitions returns each registered definition exactly once.
	ListDefinitions(ctx context.Context) []*model.ProcessDefinition
}
```

`MemDefinitionRegistry.ListDefinitions` (dedupe by identity — iterate the map, collect distinct `*ProcessDefinition` pointers under lock):

```go
func (r *MemDefinitionRegistry) ListDefinitions(context.Context) []*model.ProcessDefinition {
	r.mu.RLock()
	defer r.mu.RUnlock()
	seen := make(map[*model.ProcessDefinition]struct{}, len(r.defs))
	out := make([]*model.ProcessDefinition, 0, len(r.defs))
	for _, d := range r.defs { // r.defs is the internal map keyed by Qualifier
		if _, ok := seen[d]; ok {
			continue
		}
		seen[d] = struct{}{}
		out = append(out, d)
	}
	return out
}
```

(Confirm the internal field name/lock — adapt to the actual `MemDefinitionRegistry` struct. Do the equivalent for `MapDefinitionRegistry`. `CachingDefinitionRegistry.ListDefinitions` type-asserts its delegate to `DefinitionLister` and passes through, returning `nil` when the delegate lacks the capability.)

- [ ] **Step 4: Run — verify GREEN**

Run: `go test ./runtime/kernel/... -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add runtime/kernel/definition_lister.go runtime/kernel/*registry*.go runtime/kernel/definition_lister_test.go
git commit -m "feat(kernel): DefinitionLister enumeration capability (ADR-0121)"
```

---

## Task 4: `eventStart` unit — start-node resolution + active-correlation map

**Files:**
- Create: `runtime/event_start.go`
- Test: `runtime/event_start_test.go`
- Modify: `runtime/processdriver.go` (add `es *eventStart` field, construct in `NewProcessDriver`)

**Interfaces:**
- Produces (all `internal` to package `runtime`):
  - `type eventStart struct { mu sync.Mutex; active map[msgKey]string }` — `active` maps a message-start `(name,key)` to the instance it created.
  - `func newEventStart() *eventStart`
  - `func messageStartNode(def *model.ProcessDefinition, name string) (nodeID string, ok bool)` — the def's message-start whose `MessageName == name`.
  - `func signalStartDefs(defs []*model.ProcessDefinition, name string) []signalStartHit` where `signalStartHit struct { Def *model.ProcessDefinition; NodeID string }` — every def+node with a signal-start on `name`.
  - `func uniqueMessageStartDef(defs []*model.ProcessDefinition, name string) (*model.ProcessDefinition, string, int)` — the single message-start def for `name`; the `int` is the match count (0 miss, 1 unique, ≥2 ambiguous).
  - `func timerStartDefs(defs []*model.ProcessDefinition) []timerStartHit` where `timerStartHit struct { Def *model.ProcessDefinition; NodeID string; Trigger schedule.TriggerSpec }`.
- Consumes: `event.StartEvent` fields; `def.StartNodes()`; `msgKey` (already defined in `processdriver_waiters.go`).

- [ ] **Step 1: Write failing tests** (white-box `package runtime`, since these are internal helpers)

```go
func TestSignalStartDefsFindsAllMatches(t *testing.T) {
	pay := defWithSignalStart(t, "payment", "order.completed")
	ship := defWithSignalStart(t, "shipment", "order.completed")
	other := defWithSignalStart(t, "audit", "unrelated")

	hits := signalStartDefs([]*model.ProcessDefinition{pay, ship, other}, "order.completed")

	ids := []string{}
	for _, h := range hits {
		ids = append(ids, h.Def.ID)
	}
	assert.ElementsMatch(t, []string{"payment", "shipment"}, ids)
}

func TestUniqueMessageStartDefCounts(t *testing.T) {
	tests := map[string]struct {
		defs  []*model.ProcessDefinition
		name  string
		count int
	}{
		"miss":      {defs: nil, name: "x", count: 0},
		"unique":    {defs: []*model.ProcessDefinition{defWithMessageStart(t, "A", "m")}, name: "m", count: 1},
		"ambiguous": {defs: []*model.ProcessDefinition{defWithMessageStart(t, "A", "m"), defWithMessageStart(t, "B", "m")}, name: "m", count: 2},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			_, _, n := uniqueMessageStartDef(tc.defs, tc.name)
			assert.Equal(t, tc.count, n)
		})
	}
}
```

- [ ] **Step 2: Run — verify RED**

Run: `go test ./runtime/... -run 'TestSignalStartDefs|TestUniqueMessageStartDef' -v`
Expected: FAIL — undefined helpers.

- [ ] **Step 3: Implement** `runtime/event_start.go` with the helpers above (iterate `def.StartNodes()`, type-assert `event.StartEvent`, match the relevant field). Add `es: newEventStart()` to `NewProcessDriver` and an `es *eventStart` field to the driver struct.

- [ ] **Step 4: Run — verify GREEN**

Run: `go test ./runtime/... -run 'TestSignalStartDefs|TestUniqueMessageStartDef|TestMessageStartNode' -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add runtime/event_start.go runtime/event_start_test.go runtime/processdriver.go
git commit -m "feat(runtime): eventStart start-node resolution helpers (ADR-0121)"
```

---

## Task 5: `DeliverMessage` def-drop + correlate-then-create

**Concurrency design (decided — deterministic id, the `Chainer` pattern):** message-start dedup
is NOT an in-process map or an advisory lock. The created instance's id is a **deterministic
function of `(messageName, correlationKey)`**, and `Store.Create`'s `kernel.ErrInstanceExists`
is the authoritative dedup — fully multi-replica + restart safe, DB row = shared state, no new
schema. This means **Task 4's `eventStart` struct (active map + mu + `newEventStart`) is now
obsolete and is REMOVED in this task** (its package-level helpers stay). There is **no**
`lockCorrelation`, **no** `dialect.Locker`, and **no** terminal eviction hook. Trade-off
(accepted): a correlation key is single-use per instance lifetime.

**Files:**
- Modify: `runtime/processdriver_message.go`, `runtime/processdriver.go` (create-at-node helper; REMOVE the `es *eventStart` field + its construction added in Task 4)
- Modify: `runtime/event_start.go` (DELETE the obsolete `eventStart` struct + `newEventStart`; keep the helper funcs)
- Modify: `service/service.go`, `service/request.go` (drop `DeliverMessageRequest.DefRef`), `transport/http/{httpcore,stdlib,gin,fiber}/*.go`
- Modify (callers): 7 `examples/scenarios/*/main.go`, all `*_test.go` calling `DeliverMessage`
- Test: `runtime/processdriver_message_test.go`

**Interfaces:**
- Produces:
  - `func (driver *ProcessDriver) DeliverMessage(ctx context.Context, name, correlationKey string, payload map[string]any) error` (**`def` removed**).
  - unexported `func messageStartInstanceID(name, correlationKey string) string` — a deterministic, collision-safe id (hash-based, e.g. `"msgstart-" + hex(sha256(name "\x00" key))`).
  - unexported `func (driver *ProcessDriver) createAtNode(ctx context.Context, def *model.ProcessDefinition, nodeID, instanceID string, vars map[string]any) (engine.InstanceState, error)` — seeds `engine.NewStartInstanceAtNode`; `instanceID` empty ⇒ generate via `driver.idgen` (signal/timer), non-empty ⇒ use it verbatim (message-start deterministic id); runs `deliverLoop`.
  - Sentinel `ErrAmbiguousMessageStart = errors.New("workflow-runtime: ambiguous message start")`.
- Consumes: Task 2 `engine.NewStartInstanceAtNode`; Task 3 `kernel.DefinitionLister`; Task 4 helpers `uniqueMessageStartDef`/`messageStartNode`; `driver.findMessageWaiter`; `driver.ApplyTrigger`; `driver.deliverLoop`; `driver.defsReg.Lookup`; `kernel.ErrInstanceExists`.

- [ ] **Step 1: Write failing tests**

```go
func TestDeliverMessageStartsWhenNoWaiter(t *testing.T) {
	ctx := t.Context()
	reg := kernel.NewMemDefinitionRegistry()
	def := orderMessageStartDef("order.created", "orderId") // message start
	require.NoError(t, reg.Register(def))
	store := runtimetest.MustMemStore(t)
	driver := runtimetest.MustRunner(t, nil, store, runtime.WithDefinitions(reg))

	err := driver.DeliverMessage(ctx, "order.created", "42", map[string]any{"orderId": "42"})
	require.NoError(t, err)

	// exactly one instance created for key 42
	// (assert via store enumeration or a completion side-effect in the def)
}

func TestDeliverMessageConcurrentSameKeyCreatesOne(t *testing.T) {
	ctx := t.Context()
	// register a message-start def; fire N concurrent DeliverMessage(name, "42", …).
	// Deterministic id + Store.Create ErrInstanceExists => exactly ONE instance is created,
	// the rest are clean no-ops. Assert exactly one instance with id == messageStartInstanceID(name,"42").
	// Run under: go test -race
}

func TestDeliverMessageSameKeyAfterCompletionIsNoop(t *testing.T) {
	// deliver once (creates+completes), deliver again with the same key → no-op (id row still
	// present ⇒ ErrInstanceExists). Documents the single-use-per-lifetime trade-off.
}

func TestDeliverMessageNoMatchIsNoop(t *testing.T) {
	// no waiter, no message-start def → nil error, no instance
}
```

- [ ] **Step 2: Run — verify RED**

Run: `go test ./runtime/... -run TestDeliverMessage -v`
Expected: FAIL — signature mismatch (`def` removed) / new behavior missing.

- [ ] **Step 3: Implement**

`processdriver_message.go`:

```go
func (driver *ProcessDriver) DeliverMessage(ctx context.Context, name, correlationKey string, payload map[string]any) error {
	// 1. Correlate to a running waiter parked on this message (intermediate catch / boundary /
	//    event-gateway arm). The instance's def is resolved from its own DefID — caller no
	//    longer supplies it.
	if instanceID, found := driver.findMessageWaiter(name, correlationKey); found {
		def, err := driver.resolveInstanceDef(ctx, instanceID)
		if err != nil {
			return err
		}
		trg := engine.NewMessageReceived(driver.clk.Now(), name, correlationKey, payload)
		_, err = driver.ApplyTrigger(ctx, def, instanceID, trg)
		return err
	}
	// 2. No running waiter → try a message-start create.
	def, nodeID, n := uniqueMessageStartDef(driver.listDefinitions(ctx), name)
	switch {
	case n == 0:
		return nil // genuinely unmatched — clean no-op
	case n > 1:
		return fmt.Errorf("%w: %q", ErrAmbiguousMessageStart, name)
	}
	// Deterministic id: concurrent/duplicate/redelivered messages for the same (name,key) all
	// compute the same id; Store.Create lets exactly one win, the rest get ErrInstanceExists.
	id := messageStartInstanceID(name, correlationKey)
	switch _, err := driver.createAtNode(ctx, def, nodeID, id, payload); {
	case errors.Is(err, kernel.ErrInstanceExists):
		return nil // an instance already exists for this key (running or completed) — no-op dedup
	case err != nil:
		return err
	}
	return nil
}
```

Add helpers to `processdriver.go`:
- `resolveInstanceDef(ctx, instanceID)`: `store.Load` the instance, `defsReg.Lookup(model.Version(st.DefID, st.DefVersion))`.
- `listDefinitions(ctx)`: type-assert `driver.defsReg` to `kernel.DefinitionLister`; return `nil` when unsupported.
- `createAtNode(ctx, def, nodeID, instanceID, vars)`: if `instanceID == ""` generate via `driver.idgen`; seed `engine.NewStartInstanceAtNode(driver.clk.Now(), nodeID, vars)`; run `deliverLoop` with that id. It surfaces `kernel.ErrInstanceExists` from `store.Create` unchanged (do NOT swallow it inside `createAtNode` — the caller decides).
- `messageStartInstanceID(name, correlationKey)`: deterministic, collision-safe (hash the two fields with a separator so different `(name,key)` never collide).
- Sentinel `ErrAmbiguousMessageStart = errors.New("workflow-runtime: ambiguous message start")`.

REMOVE the now-obsolete `eventStart` struct/`newEventStart`/`es` field (Task 4 scaffold) — the deterministic id needs no in-process correlation state. Keep the package-level helper funcs (`messageStartNode`/`signalStartDefs`/`uniqueMessageStartDef`/`timerStartDefs`).

Then propagate the signature change to every caller:
- `service/service.go:362` — call `e.driver.DeliverMessage(ctx, req.Name, req.CorrelationKey, req.Payload)`; drop `def` lookup if it existed only for this.
- `service/request.go` — remove `DefRef` from `DeliverMessageRequest` (and its validation).
- `transport/http/{httpcore,stdlib,gin,fiber}` — drop DefRef from the delivery request wiring.
- Examples (`message_correlation`, `timer_boundary`, `event_based_gateway`, `message_boundary`, `catch_event_reminder`, and any others) — drop the `def` argument.
- All `*_test.go` calling `DeliverMessage(ctx, def, …)` → `DeliverMessage(ctx, …)`.

- [ ] **Step 4: Run — verify GREEN (whole module — this is the breaking-change task)**

Run: `go build ./... && go test -race ./runtime/... ./service/... ./transport/... -v`
Then: `go test ./...`
Expected: PASS across the module (build must be green — all callers migrated).

- [ ] **Step 5: Commit**

```bash
git add -A
git commit -m "feat(runtime): DeliverMessage def-less correlate-then-create; migrate callers (ADR-0121)"
```

---

## Task 6: `BroadcastSignal` signal-start fan-out create

**Files:**
- Modify: `runtime/processdriver_signal.go`
- Test: `runtime/processdriver_signal_test.go`

**Interfaces:**
- Produces: `BroadcastSignal(ctx, name, payload)` unchanged signature; now also creates one instance per signal-start hit. Relaxed nil-`sigbus` guard: error only when there is neither a bus nor any signal-start match.
- Consumes: Task 4 `signalStartDefs`; Task 5 `createAtNode`; Task 3 `listDefinitions`; existing `driver.sigbus.Publish`.

- [ ] **Step 1: Write failing test**

```go
func TestBroadcastSignalFansOutToStartDefs(t *testing.T) {
	ctx := t.Context()
	reg := kernel.NewMemDefinitionRegistry()
	require.NoError(t, reg.Register(signalStartDef(t, "payment", "order.completed")))
	require.NoError(t, reg.Register(signalStartDef(t, "shipment", "order.completed")))
	store := runtimetest.MustMemStore(t)
	driver := runtimetest.MustRunner(t, nil, store, runtime.WithDefinitions(reg))

	require.NoError(t, driver.BroadcastSignal(ctx, "order.completed", map[string]any{"orderId": "7"}))

	// assert one payment instance and one shipment instance were created.
}
```

Also add a case: with no `sigbus` and ≥1 signal-start, `BroadcastSignal` still creates (no error).

- [ ] **Step 2: Run — verify RED**

Run: `go test ./runtime/... -run TestBroadcastSignalFansOut -v`
Expected: FAIL — no instances created (or nil-bus error).

- [ ] **Step 3: Implement**

```go
func (driver *ProcessDriver) BroadcastSignal(ctx context.Context, name string, payload map[string]any) error {
	hits := signalStartDefs(driver.listDefinitions(ctx), name)
	if driver.sigbus == nil && len(hits) == 0 {
		return fmt.Errorf("workflow-runtime: BroadcastSignal %q: no SignalBus configured and no signal start (use WithSignalBus)", name)
	}
	var errs []error
	if driver.sigbus != nil {
		if err := driver.sigbus.Publish(ctx, name, payload); err != nil {
			errs = append(errs, err)
		}
	}
	for _, h := range hits {
		if _, err := driver.createAtNode(ctx, h.Def, h.NodeID, "", payload); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}
```

(Signal-start create passes an empty correlation key; it does not record in the `active` map.)

- [ ] **Step 4: Run — verify GREEN**

Run: `go test -race ./runtime/... -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add runtime/processdriver_signal.go runtime/processdriver_signal_test.go
git commit -m "feat(runtime): BroadcastSignal fan-out create from signal starts (ADR-0121)"
```

---

## Task 7: Message-name uniqueness at registration

**Files:**
- Modify: `runtime/definition_registry.go`
- Test: `runtime/definition_registry_test.go`

**Interfaces:**
- Produces: `RegisterDefinition`/`MustRegisterDefinition` reject a message-start-name collision with `ErrDuplicateMessageStart` (new sentinel). The scan-then-register runs under a package-level `sync.Mutex` (`registerMu`) so concurrent registrations cannot both pass.
- Consumes: Task 3 `DefinitionLister` on `defaultDefinitionRegistry`; Task 4 message-start extraction.

- [ ] **Step 1: Write failing test**

```go
func TestRegisterDefinitionRejectsDuplicateMessageStart(t *testing.T) {
	// NOTE: uses the process-global registry — run isolated or reset via an
	// isolated registry helper if the suite provides one.
	a := messageStartDef(t, "A", "order.created")
	b := messageStartDef(t, "B", "order.created")
	require.NoError(t, runtime.RegisterDefinition(a))
	err := runtime.RegisterDefinition(b)
	assert.ErrorIs(t, err, runtime.ErrDuplicateMessageStart)
}
```

- [ ] **Step 2: Run — verify RED**

Run: `go test ./runtime/... -run TestRegisterDefinitionRejectsDuplicateMessageStart -v`
Expected: FAIL — `undefined: runtime.ErrDuplicateMessageStart` / no rejection.

- [ ] **Step 3: Implement**

```go
var (
	registerMu sync.Mutex
	// ErrDuplicateMessageStart is returned when a definition's message-start name
	// collides with an already-registered message-start (names must be unique).
	ErrDuplicateMessageStart = errors.New("workflow-runtime: duplicate message start name")
)

func RegisterDefinition(def *model.ProcessDefinition) error {
	registerMu.Lock()
	defer registerMu.Unlock()
	if err := checkMessageStartUnique(defaultDefinitionRegistry, def); err != nil {
		return err
	}
	if err := defaultDefinitionRegistry.Register(def); err != nil {
		return err
	}
	warnForceTermination(def)
	return nil
}

// checkMessageStartUnique rejects def if any of its message-start names is already
// claimed by an existing message-start in the registry.
func checkMessageStartUnique(reg *kernel.MemDefinitionRegistry, def *model.ProcessDefinition) error {
	incoming := messageStartNames(def)
	if len(incoming) == 0 {
		return nil
	}
	for _, existing := range reg.ListDefinitions(context.Background()) {
		if existing.ID == def.ID { // re-register of same def id is fine
			continue
		}
		for _, n := range messageStartNames(existing) {
			if incoming[n] {
				return fmt.Errorf("%w: %q", ErrDuplicateMessageStart, n)
			}
		}
	}
	return nil
}
```

`messageStartNames(def) map[string]bool` iterates `def.StartNodes()` collecting non-empty `MessageName`s. Apply the same guard in `MustRegisterDefinition` (panic on the returned error).

- [ ] **Step 4: Run — verify GREEN**

Run: `go test ./runtime/... -run TestRegisterDefinition -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add runtime/definition_registry.go runtime/definition_registry_test.go
git commit -m "feat(runtime): reject duplicate message-start names at registration (ADR-0121)"
```

---

## Task 8: Timer-start arming/rehydration + plain-`Drive` resolution

**Files:**
- Modify: `runtime/timerops.go` (`RehydrateStartTimers`; timer-start fire path)
- Modify: `runtime/processdriver.go` (`Drive` manual-start resolution / friendly error)
- Test: `runtime/timerops_test.go` (or `rehydrate_test.go`), `runtime/processdriver_defaults_test.go`

**Interfaces:**
- Produces: `func (driver *ProcessDriver) RehydrateStartTimers(ctx context.Context) error` — enumerate `timerStartDefs`, arm each on the scheduler; a fire callback runs `createAtNode(ctx, def, nodeID, "", nil)`. `Drive` on a def with only event-starts returns a wrapped `engine.ErrNoManualStart` with the friendly message.
- Consumes: Task 4 `timerStartDefs`; `driver.sched`/`armTimer`; Task 5 `createAtNode`; Task 2 `engine.ErrNoManualStart`.

- [ ] **Step 1: Write failing tests**

```go
func TestRehydrateStartTimersFiresCreatesInstance(t *testing.T) {
	ctx := t.Context()
	fc := clockwork.NewFakeClock()
	reg := kernel.NewMemDefinitionRegistry()
	require.NoError(t, reg.Register(timerStartDef(t, "cron", schedule.AfterDuration(time.Hour))))
	store := runtimetest.MustMemStore(t)
	driver := runtimetest.MustRunner(t, nil, store, runtime.WithClock(fc), runtime.WithDefinitions(reg) /*, WithScheduler(...)*/)

	require.NoError(t, driver.RehydrateStartTimers(ctx))
	fc.Advance(time.Hour + time.Minute) // drive the fake clock/scheduler
	// assert one instance of "cron" was created
}

func TestDriveErrorsWhenOnlyEventStarts(t *testing.T) {
	ctx := t.Context()
	def := onlyMessageStartDef()
	driver := runtimetest.MustRunner(t, nil, runtimetest.MustMemStore(t))
	_, err := driver.Drive(ctx, def, "x", nil)
	assert.ErrorIs(t, err, engine.ErrNoManualStart)
}
```

(Match the existing scheduler-test harness pattern in `rehydrate_test.go` for arming/firing with a fake clock; reuse `armTimer` as `RehydrateTimers` does.)

- [ ] **Step 2: Run — verify RED**

Run: `go test ./runtime/... -run 'TestRehydrateStartTimers|TestDriveErrorsWhenOnlyEventStarts' -v`
Expected: FAIL — `undefined: RehydrateStartTimers`; `Drive` returns nil instead of the error.

- [ ] **Step 3: Implement**

`RehydrateStartTimers` mirrors `RehydrateTimers` (`timerops.go:206`) but sources arms from `timerStartDefs(driver.listDefinitions(ctx))` and its fire callback calls `createAtNode`. `Drive` (`processdriver.go:301`) already seeds `engine.NewStartInstance` (empty StartNodeID); the engine now returns `ErrNoManualStart` for only-event-start defs — wrap it once with a friendly hint:

```go
// inside Drive, after deliverLoop:
if errors.Is(err, engine.ErrNoManualStart) {
	return out, fmt.Errorf("workflow-runtime: definition %s has no plain start; use an event entry point (DeliverMessage / BroadcastSignal / timer start): %w", def.ID, err)
}
```

- [ ] **Step 4: Run — verify GREEN**

Run: `go test -race ./runtime/... -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add runtime/timerops.go runtime/processdriver.go runtime/*_test.go
git commit -m "feat(runtime): RehydrateStartTimers + plain-Drive manual-start resolution (ADR-0121)"
```

---

## Task 9: Example `event_start`

**Files:**
- Create: `examples/scenarios/event_start/main.go`

**Interfaces:**
- Consumes: the full public surface — `BroadcastSignal`, `DeliverMessage`, `RegisterDefinition`, builders.

Illustrate a genuinely **external** trigger (not "a predecessor process completed" — that is Chainer's turf per the spec's *Relationship to Chainer* section). Show an inbound **signal** `order.received` fanning out to start **payment** and **shipment** definitions; shipment then parks on a `payment.completed` **message** that correlates-then-completes it. Keep it a thin, self-contained `main` per the examples convention (engine mechanics, not testing helpers). A short header comment states the positioning: event-start (this) is preferred for BPMN-native process-to-process choreography; `runtime/chain.Chainer` remains for lineage / outcome-routing / exactly-once-durable chaining.

- [ ] **Step 1: Write the example** (buildable `main`), then a doc-style walk-through in comments.

- [ ] **Step 2: Verify it builds and runs**

Run: `go build ./examples/... && go run ./examples/scenarios/event_start`
Expected: prints the fan-out + correlation trace; exits 0.

- [ ] **Step 3: Commit**

```bash
git add examples/scenarios/event_start
git commit -m "docs(examples): event_start signal fan-out + message correlation (ADR-0121)"
```

---

## Task 10: ADR-0121

**Files:**
- Create: `docs/adr/0121-event-based-start.md` (Nygard template)

- [ ] **Step 1: Write the ADR** — Status/Date, Context, Decision, Consequences — distilled from `docs/specs/2026-07-10-event-based-start-design.md` (placement, def-less publish, BPMN semantics, no durable store + `DefinitionLister`, `StartNodeID` seam, concurrency: Elector / fan-out-safe / advisory-lock message dedup with the documented cross-replica limitation and deferred durable follow-up). **Include a "Relationship to Chainer (ADR-0045)" subsection** with the retained-but-event-start-preferred positioning: prefer signal choreography (throw-at-end → signal-start) for process-to-process starts; use `Chainer` only for lineage / outcome-based routing / exactly-once-durable chaining.

- [ ] **Step 2: Commit**

```bash
git add docs/adr/0121-event-based-start.md
git commit -m "docs(adr): ADR-0121 event-based start events"
```

---

## Verification Checklist (whole feature)

- [ ] `go build ./...` green; `go test -race ./...` green.
- [ ] Touched packages ≥85% line coverage (`go test -race -coverprofile=cover.out ./... && go tool cover -func=cover.out | tail -1`).
- [ ] `golangci-lint run ./...` clean.
- [ ] Spec §1–§9 each map to a task (validation, engine seam, capability, eventStart, DeliverMessage, BroadcastSignal, registration uniqueness, timer + plain-Drive, example, ADR).
- [ ] `go test -race` specifically on concurrent identical-`(name,key)` `DeliverMessage` and concurrent signal fan-out (Task 5/6).
- [ ] Whole-branch `/code-review` (high, multi-finder + opus composition) before merge — point it at: correlate-then-create atomicity, the advisory-lock cross-replica limitation matching the spec, message-name uniqueness TOCTOU, union-reachability correctness, and the `DeliverMessage` caller migration completeness (grep `.md`/`.go` for stale `DeliverMessage(ctx, def,`).
